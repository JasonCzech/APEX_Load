package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestLocalDomainAdminInheritedHandlerPermissions(t *testing.T) {
	server, _ := newLocalAuthTestServer(t, false)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source-secret migration: %v", err)
	}

	login, cookie := localLogin(t, server, "domainadmin", "domainadmin")
	if login.Code != http.StatusOK || cookie == nil {
		t.Fatalf("domainadmin login = %d cookie=%v body=%s", login.Code, cookie, login.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+storage.DefaultProjectID, nil)
	request.AddCookie(cookie)
	resolution, err := resolveRequestAuth(request, server.authSettingsForRequest(), server.store, server.activeLocalAuthGeneration())
	if err != nil || resolution.User == nil {
		t.Fatalf("resolve local domainadmin: user=%#v err=%v", resolution.User, err)
	}
	request = withAuthenticatedContext(request, resolution)

	if !server.canExportRawRecording(request, storage.DefaultProjectID) {
		t.Fatal("domainadmin domain project_admin did not authorize raw recording export in a child project")
	}
	if !server.sourceSecretMigrationAuthorized(request, storage.DefaultProjectID) {
		t.Fatal("domainadmin domain project_admin did not authorize source-secret migration in a child project")
	}
}

func TestLocalDomainAdminRepositoryBindingInheritsHandlerPermissions(t *testing.T) {
	server, store := newLocalAuthTestServer(t, false)
	if err := store.DeleteUserRoleBinding("local-auth-domainadmin-binding-2"); err != nil {
		t.Fatalf("remove default domain binding: %v", err)
	}
	now := time.Now().UTC()
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{
		ID:        "local-auth-domainadmin-repository-test",
		UserID:    "local-auth-domainadmin",
		ScopeType: "application_repository",
		ScopeID:   storage.DefaultApplicationRepositoryID,
		Role:      RoleProjectAdmin,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("save repository project_admin binding: %v", err)
	}
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source-secret migration: %v", err)
	}

	login, cookie := localLogin(t, server, "domainadmin", "domainadmin")
	if login.Code != http.StatusOK || cookie == nil {
		t.Fatalf("domainadmin login = %d cookie=%v body=%s", login.Code, cookie, login.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/projects/"+storage.DefaultProjectID, nil)
	request.AddCookie(cookie)
	resolution, err := resolveRequestAuth(request, server.authSettingsForRequest(), server.store, server.activeLocalAuthGeneration())
	if err != nil || resolution.User == nil {
		t.Fatalf("resolve repository-bound domainadmin: user=%#v err=%v", resolution.User, err)
	}
	request = withAuthenticatedContext(request, resolution)
	if !server.canExportRawRecording(request, storage.DefaultProjectID) {
		t.Fatal("repository project_admin did not authorize raw recording export in a child project")
	}
	if !server.sourceSecretMigrationAuthorized(request, storage.DefaultProjectID) {
		t.Fatal("repository project_admin did not authorize source-secret migration in a child project")
	}

	resources := buildUserEffectiveAccess(server.store, authAccessMatrix, resolution.User, storage.DefaultProjectID, storage.DefaultDomainID, storage.DefaultApplicationRepositoryID)
	for _, operationID := range []string{"view_tests", "manage_tests", "manage_repository_releases"} {
		operation := localInheritedRoleOperation(t, resources, operationID)
		if !operation.Allowed || operation.GrantSource != "application_repository" {
			t.Errorf("%s = %#v, want allowed from inherited repository role", operationID, operation)
		}
	}
}

func TestLocalDomainAdminEffectiveAccessReportsInheritedSource(t *testing.T) {
	server, _ := newLocalAuthTestServer(t, false)
	login, cookie := localLogin(t, server, "domainadmin", "domainadmin")
	if login.Code != http.StatusOK || cookie == nil {
		t.Fatalf("domainadmin login = %d cookie=%v body=%s", login.Code, cookie, login.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/effective-access?project_id="+storage.DefaultProjectID+
			"&domain_id="+storage.DefaultDomainID+
			"&application_repository_id="+storage.DefaultApplicationRepositoryID, nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("effective access = %d body=%s", response.Code, response.Body.String())
	}
	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode effective access: %v", err)
	}
	for _, operationID := range []string{"view_tests", "manage_tests", "manage_repository_releases"} {
		operation := localInheritedRoleOperation(t, payload.Resources, operationID)
		if !operation.Allowed || operation.GrantSource != "domain" {
			t.Errorf("%s = %#v, want allowed from inherited domain role", operationID, operation)
		}
	}

	foreignProjectID := saveLocalScopeTestProject(t, server.store.(*storage.SQLiteStore))
	foreign := httptest.NewRequest(http.MethodGet, "/api/v1/auth/effective-access?project_id="+foreignProjectID, nil)
	foreign.AddCookie(cookie)
	foreignResponse := httptest.NewRecorder()
	server.mux.ServeHTTP(foreignResponse, foreign)
	if foreignResponse.Code != http.StatusOK {
		t.Fatalf("foreign effective access = %d body=%s", foreignResponse.Code, foreignResponse.Body.String())
	}
	payload = AuthEffectiveAccessResponse{}
	if err := json.Unmarshal(foreignResponse.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode foreign effective access: %v", err)
	}
	for _, operationID := range []string{"view_tests", "manage_tests"} {
		if operation := localInheritedRoleOperation(t, payload.Resources, operationID); operation.Allowed {
			t.Errorf("foreign-project %s = %#v, want denied", operationID, operation)
		}
	}
}

func TestLocalDomainAdminPrincipalSimulationReportsInheritedSource(t *testing.T) {
	server, _ := newLocalAuthTestServer(t, false)
	login, cookie := localLogin(t, server, "admin", "admin")
	if login.Code != http.StatusOK || cookie == nil {
		t.Fatalf("admin login = %d cookie=%v body=%s", login.Code, cookie, login.Body.String())
	}

	request := httptest.NewRequest(http.MethodGet,
		"/api/v1/auth/effective-access/principal?principal_type=user&principal_id=local-auth-domainadmin&project_id="+storage.DefaultProjectID, nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("principal simulation = %d body=%s", response.Code, response.Body.String())
	}
	var payload AuthEffectiveAccessResponse
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode principal simulation: %v", err)
	}
	operation := localInheritedRoleOperation(t, payload.Resources, "manage_tests")
	if !operation.Allowed || operation.GrantSource != "domain" {
		t.Fatalf("manage_tests = %#v, want allowed from inherited domain role", operation)
	}
}

func TestInheritedHandlerRolesDoNotChangeEstablishedProviders(t *testing.T) {
	server, _ := newLocalAuthTestServer(t, false)
	settings := server.runner.GetSystemSettings()
	settings.ScriptImports.AllowSourceSecretMigration = true
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("enable source-secret migration: %v", err)
	}

	external := &AuthenticatedUser{
		Subject:         "external-domain-admin",
		GlobalRoles:     map[string]struct{}{RoleViewer: {}},
		ProjectRoles:    map[string]map[string]struct{}{},
		DomainRoles:     map[string]map[string]struct{}{storage.DefaultDomainID: {RoleProjectAdmin: {}}},
		RepositoryRoles: map[string]map[string]struct{}{},
	}
	request := withAuthenticatedContext(httptest.NewRequest(http.MethodGet, "/", nil), authResolution{User: external})
	if server.canExportRawRecording(request, storage.DefaultProjectID) {
		t.Fatal("established-provider domain role unexpectedly gained raw recording inheritance")
	}
	if server.sourceSecretMigrationAuthorized(request, storage.DefaultProjectID) {
		t.Fatal("established-provider domain role unexpectedly gained source-secret migration inheritance")
	}

	resources := buildUserEffectiveAccess(server.store, authAccessMatrix, external, storage.DefaultProjectID, storage.DefaultDomainID, storage.DefaultApplicationRepositoryID)
	if operation := localInheritedRoleOperation(t, resources, "manage_tests"); operation.Allowed {
		t.Fatalf("established-provider manage_tests = %#v, want prior direct-project semantics", operation)
	}
	view := localInheritedRoleOperation(t, resources, "view_tests")
	if !view.Allowed || view.GrantSource != "global" {
		t.Fatalf("established-provider view_tests = %#v, want unchanged global viewer grant", view)
	}
}

func localInheritedRoleOperation(t *testing.T, resources []AuthEffectiveAccessResource, operationID string) AuthEffectiveAccessOperation {
	t.Helper()
	for _, resource := range resources {
		for _, operation := range resource.Operations {
			if operation.ID == operationID {
				return operation
			}
		}
	}
	t.Fatalf("effective-access operation %q was not returned", operationID)
	return AuthEffectiveAccessOperation{}
}
