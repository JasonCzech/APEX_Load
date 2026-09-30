package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/importartifact"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestApplicationSecretSharedByAssignedScriptsAndRotatedOnce(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	if err := records.SaveProject(&storage.Project{ID: "shared-project", Name: "Shared", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"app-a", "app-b"} {
		if err := records.SaveApplication(&storage.Application{ID: id, ProjectID: "shared-project", Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x42}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: records, BrunoArtifactWrapper: wrapper})
	project := secretAPIRequest(t, server, http.MethodPost, "/api/v1/projects/shared-project/secrets", map[string]any{
		"secrets": []map[string]string{{"name": "LOGIN_PASSWORD", "value": "project-password"}},
	})
	if project.Code != http.StatusCreated {
		t.Fatalf("project create: %d %s", project.Code, project.Body.String())
	}
	created := secretAPIRequest(t, server, http.MethodPost, "/api/v1/applications/app-a/secrets", map[string]any{
		"secrets": []map[string]string{{"name": "LOGIN_PASSWORD", "value": "application-password"}},
	})
	if created.Code != http.StatusCreated || strings.Contains(created.Body.String(), "application-password") {
		t.Fatalf("application create: %d %s", created.Code, created.Body.String())
	}
	statusA := secretAPIRequest(t, server, http.MethodPost, "/api/v1/applications/app-a/secrets/status", map[string]any{"names": []string{"LOGIN_PASSWORD"}})
	if statusA.Code != http.StatusOK || !strings.Contains(statusA.Body.String(), `"source_scope":"application"`) {
		t.Fatalf("application status: %d %s", statusA.Code, statusA.Body.String())
	}
	statusB := secretAPIRequest(t, server, http.MethodPost, "/api/v1/applications/app-b/secrets/status", map[string]any{"names": []string{"LOGIN_PASSWORD"}})
	if statusB.Code != http.StatusOK || !strings.Contains(statusB.Body.String(), `"source_scope":"project"`) {
		t.Fatalf("project fallback status: %d %s", statusB.Code, statusB.Body.String())
	}
	for _, id := range []string{"script-one", "script-two"} {
		script := &dsl.Script{ID: id, Name: id, Version: "1", Protocol: "http", ProjectID: "shared-project",
			Config: dsl.ScriptConfig{BaseURL: "https://example.test", HTTP: dsl.HTTPConfig{Auth: dsl.AuthConfig{
				Type: "basic", Username: "shared-login", Password: "{{secret.LOGIN_PASSWORD}}",
			}}},
			Actions: []dsl.Action{{Name: "Call", Type: "request", Target: "/"}},
		}
		if _, err := server.runner.StoreScriptDurable(script); err != nil {
			t.Fatal(err)
		}
		assignment, err := records.ListLibraryAssignments("script", []string{id})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := records.ReplaceLibraryAssignment("script", id, "shared-project", ptrString("app-a"), nil, assignment[id].Revision); err != nil {
			t.Fatal(err)
		}
		value, ok := engine.ResolveSecret(context.Background(), server.runner.secretResolverForScript(script), "LOGIN_PASSWORD")
		if !ok || value != "application-password" {
			t.Fatalf("%s got (%q, %t)", id, value, ok)
		}
	}
	scriptStatus := secretAPIRequest(t, server, http.MethodGet, "/api/v1/scripts/script-one/secret-status", nil)
	if scriptStatus.Code != http.StatusOK || !strings.Contains(scriptStatus.Body.String(), `"application_id":"app-a"`) ||
		!strings.Contains(scriptStatus.Body.String(), `"source_scope":"application"`) {
		t.Fatalf("assigned script effective status: %d %s", scriptStatus.Code, scriptStatus.Body.String())
	}
	other, ok := engine.ResolveSecret(context.Background(), server.runner.secretResolverForScope("shared-project", "app-b"), "LOGIN_PASSWORD")
	if !ok || other != "project-password" {
		t.Fatalf("other application got (%q, %t)", other, ok)
	}
	if _, ok := engine.ResolveSecret(context.Background(), server.runner.secretResolverForScope("different-project", "app-a"), "LOGIN_PASSWORD"); ok {
		t.Fatal("application value escaped its parent project")
	}
	usage := secretAPIRequest(t, server, http.MethodGet, "/api/v1/applications/app-a/secrets/LOGIN_PASSWORD/usage", nil)
	if usage.Code != http.StatusOK {
		t.Fatalf("usage: %d %s", usage.Code, usage.Body.String())
	}
	var snapshot struct {
		UsageRevision string `json:"usage_revision"`
		ConsumerCount int    `json:"consumer_count"`
	}
	if err := json.Unmarshal(usage.Body.Bytes(), &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ConsumerCount != 2 {
		t.Fatalf("application usage should include both assigned scripts, got %d", snapshot.ConsumerCount)
	}
	rotated := secretAPIRequest(t, server, http.MethodPut, "/api/v1/applications/app-a/secrets/LOGIN_PASSWORD", map[string]any{
		"value": "new-application-password", "expected_usage_revision": snapshot.UsageRevision,
		"acknowledge_shared_usage": true,
	})
	if rotated.Code != http.StatusOK || strings.Contains(rotated.Body.String(), "new-application-password") {
		t.Fatalf("rotation: %d %s", rotated.Code, rotated.Body.String())
	}
	for _, id := range []string{"script-one", "script-two"} {
		value, ok := engine.ResolveSecret(context.Background(), server.runner.secretResolverForScript(&dsl.Script{ID: id, ProjectID: "shared-project"}), "LOGIN_PASSWORD")
		if !ok || value != "new-application-password" {
			t.Fatalf("%s after rotation got (%q, %t)", id, value, ok)
		}
	}
	selected := secretAPIRequest(t, server, http.MethodPost, "/api/v1/applications/app-b/secrets", map[string]any{
		"secrets": []map[string]string{{"name": "LOGIN_PASSWORD", "value": "selected-application-password"}},
	})
	if selected.Code != http.StatusCreated {
		t.Fatalf("selected application create: %d %s", selected.Code, selected.Body.String())
	}
	if err := records.SaveRelease(&storage.Release{ID: "release-b", ApplicationID: "app-b", ProjectID: "shared-project", Name: "Release B", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ciResolver, err := server.ciRunSecretResolver(ciRunCreateRequest{ProjectID: "shared-project", ReleaseID: "release-b"})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := engine.ResolveSecret(context.Background(), ciResolver, "LOGIN_PASSWORD"); !ok || value != "selected-application-password" {
		t.Fatalf("managed CI validation ignored selected application: %q %t", value, ok)
	}
	legacyResolver, err := server.ciRunSecretResolver(ciRunCreateRequest{ProjectID: "shared-project"})
	if err != nil {
		t.Fatal(err)
	}
	if value, ok := engine.ResolveSecret(context.Background(), legacyResolver, "LOGIN_PASSWORD"); !ok || value != "project-password" {
		t.Fatalf("legacy CI validation should use the project: %q %t", value, ok)
	}
	stored, ok := server.runner.GetScript("script-one")
	if !ok {
		t.Fatal("script-one missing")
	}
	run := &ManagedTest{ID: "pinned-run", ProjectID: "shared-project", ApplicationID: "app-b",
		groupSpecs: []engine.GroupConfig{{Name: "group", Script: stored.Script}},
		Groups:     []TestGroup{{Name: "group", script: stored.Script}}, pinnedSecrets: &runPinnedSecretState{}}
	server.runner.tests[run.ID] = run
	originalProjectStore := server.runner.projectSecretStore
	server.runner.projectSecretStore = failingApplicationGetStore{Store: originalProjectStore}
	if _, err := server.runner.scriptShakeoutExecutableFingerprint(run.ID); err != nil {
		t.Fatalf("application-backed shakeout should not read the unavailable project provider: %v", err)
	}
	server.runner.projectSecretStore = originalProjectStore
	if err := server.runner.validateStartSecretRequirements(run.ID); err != nil {
		t.Fatal(err)
	}
	bound := server.runner.boundSecretResolverForRunGroup(run, 0, "shared-project")
	before, ok := engine.ResolveSecret(context.Background(), bound, "LOGIN_PASSWORD")
	if !ok || before != "selected-application-password" {
		t.Fatalf("run application should override script assignment, got %q", before)
	}
	rotatedSelected := secretAPIRequest(t, server, http.MethodPut, "/api/v1/applications/app-b/secrets/LOGIN_PASSWORD", map[string]any{
		"value": "rotated-selected-password",
	})
	if rotatedSelected.Code != http.StatusOK {
		t.Fatalf("selected application rotation: %d %s", rotatedSelected.Code, rotatedSelected.Body.String())
	}
	stillPinned, ok := engine.ResolveSecret(context.Background(), bound, "LOGIN_PASSWORD")
	if !ok || stillPinned != before {
		t.Fatalf("active run changed after rotation: %q", stillPinned)
	}
	nextRun := &ManagedTest{ID: "next-run", ProjectID: "shared-project", ApplicationID: "app-b",
		groupSpecs: []engine.GroupConfig{{Name: "group", Script: stored.Script}},
		Groups:     []TestGroup{{Name: "group"}}, pinnedSecrets: &runPinnedSecretState{}}
	server.runner.tests[nextRun.ID] = nextRun
	if err := server.runner.validateStartSecretRequirements(nextRun.ID); err != nil {
		t.Fatal(err)
	}
	after, ok := engine.ResolveSecret(context.Background(), server.runner.boundSecretResolverForRunGroup(nextRun, 0, "shared-project"), "LOGIN_PASSWORD")
	if !ok || after != "rotated-selected-password" {
		t.Fatalf("new run did not receive rotation: %q", after)
	}
	originalStore := server.applicationSecretStore
	server.applicationSecretStore = failingApplicationGetStore{Store: originalStore}
	server.runner.applicationSecretStore = server.applicationSecretStore
	failed := secretAPIRequest(t, server, http.MethodPost, "/api/v1/applications/app-a/secrets/status", map[string]any{"names": []string{"LOGIN_PASSWORD"}})
	if failed.Code != http.StatusServiceUnavailable || strings.Contains(failed.Body.String(), "project-password") {
		t.Fatalf("provider failure should fail closed: %d %s", failed.Code, failed.Body.String())
	}
	if _, ok := engine.ResolveSecret(context.Background(), server.runner.secretResolverForScope("shared-project", "app-a"), "LOGIN_PASSWORD"); ok {
		t.Fatal("provider failure fell back to project secret")
	}
	server.applicationSecretStore = originalStore
	server.runner.applicationSecretStore = originalStore
	for _, id := range []string{"script-one", "script-two"} {
		assignment, err := records.ListLibraryAssignments("script", []string{id})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := records.ReplaceLibraryAssignment("script", id, "shared-project", nil, nil, assignment[id].Revision); err != nil {
			t.Fatal(err)
		}
		value, ok := engine.ResolveSecret(context.Background(), server.runner.secretResolverForScript(&dsl.Script{ID: id, ProjectID: "shared-project"}), "LOGIN_PASSWORD")
		if !ok || value != "project-password" {
			t.Fatalf("unassigned %s should use project value, got %q", id, value)
		}
	}
	usageAfterUnassign := secretAPIRequest(t, server, http.MethodGet, "/api/v1/applications/app-a/secrets/LOGIN_PASSWORD/usage", nil)
	if usageAfterUnassign.Code != http.StatusOK || !strings.Contains(usageAfterUnassign.Body.String(), `"consumer_count":0`) {
		t.Fatalf("unassigned scripts remained application consumers: %d %s", usageAfterUnassign.Code, usageAfterUnassign.Body.String())
	}
	observedAuthorization := make(chan string, 1)
	protocolName := registerReplayAPIProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		observedAuthorization <- action.Headers["Authorization"]
		return &protocol.Result{Success: true, StatusCode: http.StatusOK}, nil
	})
	direct := &dsl.Script{ID: "direct-application-action", Name: "Direct application action", Version: "1", Protocol: protocolName,
		ProjectID: "shared-project", Config: dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{{Name: "Login", Type: "GET", Method: "GET", Target: "/login",
			Headers: map[string]string{"Authorization": "Bearer {{secret.LOGIN_PASSWORD}}"}}}}
	if _, err := server.runner.StoreScriptDurable(direct); err != nil {
		t.Fatal(err)
	}
	assignment, err := records.ListLibraryAssignments("script", []string{direct.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := records.ReplaceLibraryAssignment("script", direct.ID, "shared-project", ptrString("app-a"), nil,
		assignment[direct.ID].Revision); err != nil {
		t.Fatal(err)
	}
	replayed := secretAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+direct.ID+"/replay", map[string]any{})
	if replayed.Code != http.StatusOK || strings.Contains(replayed.Body.String(), "new-application-password") {
		t.Fatalf("direct replay failed or disclosed the secret: %d %s", replayed.Code, replayed.Body.String())
	}
	select {
	case got := <-observedAuthorization:
		if got != "Bearer new-application-password" {
			t.Fatalf("direct replay used %q", got)
		}
	default:
		t.Fatal("direct replay did not execute the assigned script")
	}
	assignment, err = records.ListLibraryAssignments("script", []string{direct.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := records.ReplaceLibraryAssignment("script", direct.ID, "shared-project", nil, nil,
		assignment[direct.ID].Revision); err != nil {
		t.Fatal(err)
	}
	appRecords := storage.ApplicationSecretStorage{SQLiteStore: records}
	if created, err := appRecords.CreateProjectSecretMetadata(&storage.ProjectSecretMetadata{
		ProjectID: storage.ApplicationSecretScopeID("app-a"), SecretName: "EXTERNAL_LOGIN", Provider: "azure_key_vault",
	}); err != nil || !created {
		t.Fatalf("index external secret: %v %t", err, created)
	}
	deleted := secretAPIRequest(t, server, http.MethodDelete, "/api/v1/applications/app-a", nil)
	if deleted.Code != http.StatusOK || strings.Contains(deleted.Body.String(), "application-password") ||
		!strings.Contains(deleted.Body.String(), `"external_secret_cleanup_required":true`) ||
		!strings.Contains(deleted.Body.String(), "EXTERNAL_LOGIN") {
		t.Fatalf("application delete: %d %s", deleted.Code, deleted.Body.String())
	}
	if listed, err := appRecords.ListProjectSecretMetadata(storage.ApplicationSecretScopeID("app-a")); err != nil || len(listed) != 0 {
		t.Fatalf("deleted application retained encrypted secrets: %v %d", err, len(listed))
	}
}

func TestApplicationSecretAuthorization(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "secrets.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	for _, projectID := range []string{"authorized-project", "other-project"} {
		if err := records.SaveProject(&storage.Project{ID: projectID, Name: projectID, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	if err := records.SaveApplication(&storage.Application{ID: "secret-app", ProjectID: "authorized-project", Name: "Secret app", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x43}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	appStore := &secretstore.DatabaseStore{Records: storage.ApplicationSecretStorage{SQLiteStore: records}, Wrapper: wrapper}
	server := NewServer(ServerConfig{Store: records, ApplicationSecretStore: appStore})
	enableTrustedHeaderAuth(t, server)
	for _, subject := range []string{"viewer", "editor", "admin", "outsider"} {
		if err := records.SaveUser(&storage.User{ID: subject, Subject: subject, Active: true, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for _, role := range []struct{ subject, projectID, role string }{
		{"viewer", "authorized-project", RoleViewer},
		{"editor", "authorized-project", RoleEditor},
		{"admin", "authorized-project", RoleProjectAdmin},
		{"outsider", "other-project", RoleProjectAdmin},
	} {
		if err := records.SaveUserRoleBinding(&storage.UserRoleBinding{ID: role.subject + "-binding", UserID: role.subject,
			ScopeType: "project", ScopeID: role.projectID, Role: role.role, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	path := "/api/v1/applications/secret-app/secrets"
	if response := secretAPIRequestAsSubject(t, server, http.MethodGet, path, "viewer", nil); response.Code != http.StatusOK {
		t.Fatalf("viewer list = %d %s", response.Code, response.Body.String())
	}
	if response := secretAPIRequestAsSubject(t, server, http.MethodGet, path, "outsider", nil); response.Code != http.StatusForbidden {
		t.Fatalf("cross-project list = %d %s", response.Code, response.Body.String())
	}
	if response := secretAPIRequestAsSubject(t, server, http.MethodPost, path, "viewer", map[string]any{
		"secrets": []map[string]string{{"name": "LOGIN", "value": "viewer-must-not-write"}},
	}); response.Code != http.StatusForbidden {
		t.Fatalf("viewer create = %d %s", response.Code, response.Body.String())
	}
	create := secretAPIRequestAsSubject(t, server, http.MethodPost, path, "editor", map[string]any{
		"secrets": []map[string]string{{"name": "LOGIN", "value": "editor-created"}},
	})
	if create.Code != http.StatusCreated || strings.Contains(create.Body.String(), "editor-created") {
		t.Fatalf("editor create = %d %s", create.Code, create.Body.String())
	}
	rotatePath := path + "/LOGIN"
	if response := secretAPIRequestAsSubject(t, server, http.MethodPut, rotatePath, "editor", map[string]any{"value": "editor-must-not-rotate"}); response.Code != http.StatusForbidden {
		t.Fatalf("editor rotate = %d %s", response.Code, response.Body.String())
	}
	rotate := secretAPIRequestAsSubject(t, server, http.MethodPut, rotatePath, "admin", map[string]any{"value": "admin-rotated"})
	if rotate.Code != http.StatusOK || strings.Contains(rotate.Body.String(), "admin-rotated") {
		t.Fatalf("admin rotate = %d %s", rotate.Code, rotate.Body.String())
	}
	serviceRequest := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"secrets":[{"name":"SERVICE","value":"forbidden"}]}`))
	serviceRequest.Header.Set("Content-Type", "application/json")
	serviceRequest.SetPathValue("id", "secret-app")
	serviceRequest = withAuthenticatedContext(serviceRequest, authResolution{ServiceAccount: &AuthenticatedServiceAccount{ID: "service-account"}})
	serviceResponse := httptest.NewRecorder()
	server.handleCreateApplicationSecrets(serviceResponse, serviceRequest)
	if serviceResponse.Code != http.StatusForbidden {
		t.Fatalf("service create = %d %s", serviceResponse.Code, serviceResponse.Body.String())
	}
}

type failingApplicationGetStore struct{ secretstore.Store }

func (failingApplicationGetStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	return "", secretstore.Metadata{}, errors.New("provider unavailable")
}
