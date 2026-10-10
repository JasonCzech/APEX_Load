package api

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/recorder"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestProjectDataCollectionsRequireScopeForLocalBaselineViewer(t *testing.T) {
	server, store := newLocalAuthTestServer(t, false)
	otherProjectID := saveLocalScopeTestProject(t, store)

	projectCollections := []string{
		"/api/v1/results/imports",
		"/api/v1/fleet/capacity",
		"/api/v1/fleet/load-zones",
		"/api/v1/system/usage",
		"/api/v1/system/usage/export",
		"/api/v1/system/usage/snapshots",
		"/api/v1/run-calendar",
	}
	requestStatus := func(cookie *http.Cookie, path string) int {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(cookie)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response.Code
	}

	for _, username := range []string{"domainadmin", "user", "dev"} {
		t.Run(username, func(t *testing.T) {
			login, cookie := localLogin(t, server, username, username)
			if login.Code != http.StatusOK || cookie == nil {
				t.Fatalf("login = %d cookie=%v body=%s", login.Code, cookie, login.Body.String())
			}
			for _, path := range projectCollections {
				if got := requestStatus(cookie, path); got != http.StatusForbidden {
					t.Errorf("unfiltered %s status = %d, want 403", path, got)
				}
				if got := requestStatus(cookie, path+"?project_id="+storage.DefaultProjectID); got != http.StatusOK {
					t.Errorf("bound-project %s status = %d, want 200", path, got)
				}
				if got := requestStatus(cookie, path+"?project_id="+otherProjectID); got != http.StatusForbidden {
					t.Errorf("foreign-project %s status = %d, want 403", path, got)
				}
			}
			for _, path := range []string{"/api/v1/system/entitlements/usage", "/api/v1/schedules/health", "/api/v1/monitoring/prometheus/graphs"} {
				if got := requestStatus(cookie, path); got != http.StatusForbidden {
					t.Errorf("global project-data %s status = %d, want 403", path, got)
				}
			}
		})
	}

	login, adminCookie := localLogin(t, server, "admin", "admin")
	if login.Code != http.StatusOK || adminCookie == nil {
		t.Fatalf("admin login = %d cookie=%v body=%s", login.Code, adminCookie, login.Body.String())
	}
	for _, path := range append(projectCollections, "/api/v1/system/entitlements/usage", "/api/v1/schedules/health", "/api/v1/monitoring/prometheus/graphs") {
		if got := requestStatus(adminCookie, path); got != http.StatusOK {
			t.Errorf("admin unfiltered %s status = %d, want 200", path, got)
		}
	}
}

func TestProjectDataCollectionPolicyPreservesNonLocalGlobalViewer(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "global-viewer.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	now := time.Now().UTC()
	user := &storage.User{ID: uuid.NewString(), Subject: "external-global-viewer", Active: true, CreatedAt: now, UpdatedAt: now}
	if err := store.SaveUser(user); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveUserRoleBinding(&storage.UserRoleBinding{
		ID: uuid.NewString(), UserID: user.ID, ScopeType: "global", Role: RoleViewer, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: "127.0.0.1:0"})
	settings := enableTrustedHeaderAuth(t, server)
	paths := []string{
		"/api/v1/results/imports",
		"/api/v1/fleet/capacity",
		"/api/v1/fleet/load-zones",
		"/api/v1/system/usage",
		"/api/v1/system/usage/export",
		"/api/v1/system/usage/snapshots",
		"/api/v1/run-calendar",
		"/api/v1/system/entitlements/usage",
		"/api/v1/schedules/health",
		"/api/v1/monitoring/prometheus/graphs",
	}
	for _, path := range paths {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set(settings.Auth.UserHeader, user.Subject)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		if response.Code != http.StatusOK {
			t.Errorf("global viewer GET %s = %d, want 200 body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestProjectDataCollectionRequirementsRetainScopeGateWhenFiltered(t *testing.T) {
	for _, path := range []string{
		"/api/v1/results/imports",
		"/api/v1/fleet/capacity",
		"/api/v1/fleet/load-zones",
		"/api/v1/system/usage",
		"/api/v1/system/usage/export",
		"/api/v1/system/usage/snapshots",
		"/api/v1/run-calendar",
	} {
		t.Run(path, func(t *testing.T) {
			unfiltered, ok := accessRequirementForRequest(httptest.NewRequest(http.MethodGet, path, nil))
			if !ok || !unfiltered.RequireUnrestrictedViewer || unfiltered.ProjectLookup != nil {
				t.Fatalf("unfiltered requirement = %#v, want unrestricted-viewer gate", unfiltered)
			}
			filtered, ok := accessRequirementForRequest(httptest.NewRequest(http.MethodGet, path+"?project_id="+storage.DefaultProjectID, nil))
			if !ok || filtered.RequireUnrestrictedViewer || filtered.ProjectLookup == nil {
				t.Fatalf("filtered requirement = %#v, want project lookup", filtered)
			}
			if filtered.ServiceAccountScope != unfiltered.ServiceAccountScope {
				t.Fatalf("service-account scope changed from %q to %q", unfiltered.ServiceAccountScope, filtered.ServiceAccountScope)
			}
		})
	}

	for _, path := range []string{"/api/v1/system/entitlements/usage", "/api/v1/schedules/health", "/api/v1/monitoring/prometheus/graphs"} {
		requirement, ok := accessRequirementForRequest(httptest.NewRequest(http.MethodGet, path, nil))
		if !ok || !requirement.RequireUnrestrictedViewer {
			t.Fatalf("global project-data %s requirement = %#v, want unrestricted-viewer gate", path, requirement)
		}
	}
}

func TestFleetCapacityProjectSlotsAreRestrictedToAuthorizedProject(t *testing.T) {
	slots := []FleetProjectSlotAccounting{
		{ProjectID: storage.DefaultProjectID, ActiveSlots: 4},
		{ProjectID: "foreign-project", ActiveSlots: 9},
	}
	filtered := fleetProjectSlotsForProject(slots, storage.DefaultProjectID)
	if len(filtered) != 1 || filtered[0].ProjectID != storage.DefaultProjectID || filtered[0].ActiveSlots != 4 {
		t.Fatalf("filtered slots = %#v, want only the authorized project", filtered)
	}
}

func TestFleetLoadZoneInputsAreRestrictedToAuthorizedProject(t *testing.T) {
	tests := []*ManagedTest{
		{ID: "authorized", ProjectID: storage.DefaultProjectID},
		{ID: "foreign", ProjectID: "foreign-project"},
		nil,
	}
	filtered := fleetTestsForProject(tests, storage.DefaultProjectID)
	if len(filtered) != 1 || filtered[0].ID != "authorized" {
		t.Fatalf("filtered tests = %#v, want only the authorized project", filtered)
	}
}

func TestGlobalStatusRecorderOverlayHonorsLocalProjectScope(t *testing.T) {
	server, _ := newLocalAuthTestServer(t, false)
	localViewer := &AuthenticatedUser{
		LocalScopeBoundViewer: true,
		GlobalRoles:           map[string]struct{}{RoleViewer: {}},
		ProjectRoles: map[string]map[string]struct{}{
			storage.DefaultProjectID: {RoleViewer: {}},
		},
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/system/services", nil)
	request = withAuthenticatedContext(request, authResolution{User: localViewer})
	bound := &recorder.Session{ID: "bound", ProjectID: storage.DefaultProjectID}
	foreign := &recorder.Session{ID: "foreign", ProjectID: "foreign-project"}
	if got := server.visibleRecorderSessionForRequest(request, bound, ""); got != bound {
		t.Fatalf("bound recorder overlay = %#v, want visible", got)
	}
	if got := server.visibleRecorderSessionForRequest(request, foreign, ""); got != nil {
		t.Fatalf("foreign recorder overlay = %#v, want hidden", got)
	}
	if got := server.visibleRecorderSessionForRequest(request, foreign, storage.DefaultProjectID); got != nil {
		t.Fatalf("query-mismatched recorder overlay = %#v, want hidden", got)
	}

	externalViewer := &AuthenticatedUser{GlobalRoles: map[string]struct{}{RoleViewer: {}}}
	externalRequest := httptest.NewRequest(http.MethodGet, "/api/v1/system/services", nil)
	externalRequest = withAuthenticatedContext(externalRequest, authResolution{User: externalViewer})
	if got := server.visibleRecorderSessionForRequest(externalRequest, foreign, ""); got != foreign {
		t.Fatalf("established global viewer overlay = %#v, want preserved", got)
	}
}

func TestReservedLocalSubjectsCannotUseExternalProviders(t *testing.T) {
	localServer, store := newLocalAuthTestServer(t, false)
	login, cookie := localLogin(t, localServer, "admin", "admin")
	if login.Code != http.StatusOK || cookie == nil {
		t.Fatalf("local fixture login = %d cookie=%v body=%s", login.Code, cookie, login.Body.String())
	}

	settings := localServer.runner.GetSystemSettings()
	settings.Auth.Mode = authModeTrustedHeaders
	if _, err := localServer.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("persist trusted-header mode: %v", err)
	}
	externalServer := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: "127.0.0.1:0"})
	externalSettings := externalServer.authSettingsForRequest()
	if externalSettings.Mode != authModeTrustedHeaders {
		t.Fatalf("restarted auth mode = %q, want trusted_headers", externalSettings.Mode)
	}

	for _, subject := range []string{"local:admin", " LOCAL:admin "} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
		request.Header.Set(externalSettings.UserHeader, subject)
		response := httptest.NewRecorder()
		externalServer.mux.ServeHTTP(response, request)
		if response.Code != http.StatusUnauthorized {
			t.Errorf("reserved trusted-header subject %q status = %d, want 401", subject, response.Code)
		}
	}
	if _, _, err := authenticatedUserFromOIDCClaims(map[string]any{"sub": "local:admin"}, time.Now().Add(time.Hour), externalSettings); err == nil {
		t.Fatal("OIDC accepted a subject in the reserved local namespace")
	}

	const apiKey = "reserved-subject-service-account-key"
	now := time.Now().UTC()
	if err := store.SaveServiceAccount(&storage.ServiceAccount{
		ID: "reserved-subject-service-account", Name: "Reserved Subject Regression", KeyHash: hashAPIKey(apiKey), Active: true,
		GlobalRoles: []string{RoleSystemAdmin}, Scopes: []string{"system:read"}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	serviceRequest := httptest.NewRequest(http.MethodGet, "/api/v1/system/settings", nil)
	serviceRequest.Header.Set("X-APEXLoad-API-Key", apiKey)
	serviceResponse := httptest.NewRecorder()
	externalServer.mux.ServeHTTP(serviceResponse, serviceRequest)
	if serviceResponse.Code != http.StatusOK {
		t.Fatalf("service account status = %d, want 200 body=%s", serviceResponse.Code, serviceResponse.Body.String())
	}
}

func TestAuthStatusContainingLogoutCSRFIsNotCacheable(t *testing.T) {
	server, _ := newLocalAuthTestServer(t, false)
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("auth status = %d body=%s", response.Code, response.Body.String())
	}
	if got := response.Header().Get("Cache-Control"); got != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", got)
	}
}

func saveLocalScopeTestProject(t *testing.T, store *storage.SQLiteStore) string {
	t.Helper()
	now := time.Now().UTC()
	const (
		domainID     = "local-scope-other-domain"
		repositoryID = "local-scope-other-repository"
		categoryID   = "local-scope-other-category"
		projectID    = "local-scope-other-project"
	)
	if err := store.SaveDomain(&storage.Domain{ID: domainID, Name: "Local Scope Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveApplicationRepository(&storage.ApplicationRepository{ID: repositoryID, DomainID: domainID, Name: "Local Scope Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRepositoryCategory(&storage.RepositoryCategory{ID: categoryID, DomainID: domainID, RepositoryID: repositoryID, Name: "Local Scope Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveProject(&storage.Project{ID: projectID, DomainID: domainID, CategoryID: categoryID, Name: "Local Scope Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	return projectID
}

// view_global_observers tells the dashboard whether the controller-wide
// observer routes (schedule health, platform graphs) will answer: Local Test
// Login's seeded scope-bound viewer may read settings (view_system) but not
// those, exactly as the routes enforce; an established global viewer may.
func TestEffectiveAccessGlobalObserversMatchesUnrestrictedRoutes(t *testing.T) {
	server, _ := newLocalAuthTestServer(t, false)
	allowed := func(user *AuthenticatedUser, id string) bool {
		for _, resource := range buildUserEffectiveAccess(server.store, authAccessMatrix, user, storage.DefaultProjectID, "", "") {
			for _, operation := range resource.Operations {
				if operation.ID == id {
					return operation.Allowed
				}
			}
		}
		t.Fatalf("operation %q missing from effective access", id)
		return false
	}
	routeAllows := func(user *AuthenticatedUser, path string) bool {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		requirement, found := accessRequirementForRequest(request)
		if !found {
			t.Fatalf("route %s not in catalog", path)
		}
		return userHasGlobalGrantForRequirement(user, requirement)
	}
	localEditor := &AuthenticatedUser{
		LocalScopeBoundViewer: true,
		GlobalRoles:           map[string]struct{}{RoleViewer: {}},
		ProjectRoles:          map[string]map[string]struct{}{storage.DefaultProjectID: {RoleEditor: {}}},
	}
	if !allowed(localEditor, "view_system") || allowed(localEditor, "view_global_observers") || routeAllows(localEditor, "/api/v1/schedules/health") {
		t.Fatal("a scope-bound local viewer reads settings but not the controller-wide observers")
	}
	externalViewer := &AuthenticatedUser{GlobalRoles: map[string]struct{}{RoleViewer: {}}}
	if !allowed(externalViewer, "view_global_observers") || !routeAllows(externalViewer, "/api/v1/schedules/health") {
		t.Fatal("an established global viewer reads the controller-wide observers")
	}
}
