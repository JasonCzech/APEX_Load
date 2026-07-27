package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestResolvePostmanSaveProjectIDAllowsServerOwnedProtectedCandidate(t *testing.T) {
	body := []byte(`{
		"import_session_id":"protected-session-1",
		"project_id":"project-a"
	}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader(body),
	)
	projectID, status, message := resolvePostmanSaveProjectID(request, nil)
	if status != 0 || message != "" || projectID != "project-a" {
		t.Fatalf(
			"protected commit scope = project %q status %d message %q, want project-a success",
			projectID,
			status,
			message,
		)
	}
}

func TestResolvePostmanSaveProjectIDStillRequiresLegacyScript(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader([]byte(`{"project_id":"project-a"}`)),
	)
	_, status, message := resolvePostmanSaveProjectID(request, nil)
	if status != http.StatusBadRequest || message != "script is required" {
		t.Fatalf("legacy commit resolution = status %d message %q, want script-required 400", status, message)
	}
}

func TestResolvePostmanSaveProjectIDScopesLegacyCommitFromScript(t *testing.T) {
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader([]byte(`{
			"project_id":"untrusted-top-level-project",
			"script":{"project_id":"script-project"}
		}`)),
	)
	projectID, status, message := resolvePostmanSaveProjectID(request, nil)
	if status != 0 || message != "" || projectID != "script-project" {
		t.Fatalf(
			"legacy commit scope = project %q status %d message %q, want script-project success",
			projectID,
			status,
			message,
		)
	}
}

func TestProtectedImportCommitResolverBindsProjectEditorScope(t *testing.T) {
	body := []byte(`{
		"import_session_id":"protected-session-1",
		"project_id":"project-a"
	}`)
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader(body),
	)
	response := httptest.NewRecorder()
	resolution := authResolution{User: &AuthenticatedUser{
		ID: "project-editor",
		ProjectRoles: map[string]map[string]struct{}{
			"project-a": {RoleEditor: {}},
		},
	}}
	if !authorizeAccessRequirement(
		response,
		request,
		nil,
		resolution,
		editorProjectAccess(resolvePostmanSaveProjectID),
	) {
		t.Fatalf("protected commit authorization failed: status=%d body=%s", response.Code, response.Body.String())
	}
	if projectID, scoped := authorizedProjectFromRequest(request); !scoped || projectID != "project-a" {
		t.Fatalf("authorized project = %q, %t; want project-a, true", projectID, scoped)
	}
}

func TestResolvePostmanSaveProjectIDDoesNotProbeProtectedReplaceTarget(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/postman/save",
		bytes.NewReader([]byte(`{
			"import_session_id":"missing-session",
			"project_id":"project-a",
			"mode":"replace",
			"target_script_id":"nonexistent-script"
		}`)),
	)
	projectID, status, message := resolvePostmanSaveProjectID(request, store)
	if status != 0 || message != "" || projectID != "project-a" {
		t.Fatalf(
			"protected replace resolution = project %q status %d message %q, want project-a success without target lookup",
			projectID,
			status,
			message,
		)
	}
}
