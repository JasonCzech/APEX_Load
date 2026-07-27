package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestJavaVMArgumentsNeverBecomeSecretBindingsOrPersistThroughAuthoringSessions(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	defer store.Close()
	const projectID = "project-java-vm-argv"
	seedAuthoringScriptProjects(t, store, projectID)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 1})

	const createCanary = "q7W9pL2xN8vC4mR6tY1kH5sF3dJ0aB9uE2zG"
	unsafeDraft := javaVMDraft([]string{"-Xmx512m", "-Dlabel=" + createCanary})
	createUnsafe := authoringSessionRequest{
		ProjectID: projectID,
		Source:    "manual",
		Name:      "Unsafe Java argv",
		Draft:     *unsafeDraft,
	}
	response := serveAuthoringSessionJSON(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", createUnsafe)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe create status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	assertValueFreeJavaVMRejection(t, response.Body.String(), createCanary)
	if sessions, err := store.ListAuthoringSessions(projectID); err != nil {
		t.Fatalf("ListAuthoringSessions: %v", err)
	} else if len(sessions) != 0 {
		t.Fatalf("unsafe authoring draft reached storage: %#v", sessions)
	}

	safeDraft := javaVMDraft([]string{"-Xmx512m", "-Dendpoint=https://example.test/safe/path?view=full#section"})
	createSafe := authoringSessionRequest{
		ProjectID: projectID,
		Source:    "manual",
		Name:      "Safe Java argv",
		Draft:     *safeDraft,
	}
	response = serveAuthoringSessionJSON(t, server, http.MethodPost, "/api/v1/scripts/authoring/sessions", createSafe)
	if response.Code != http.StatusCreated {
		t.Fatalf("safe create status = %d, want %d; body=%s", response.Code, http.StatusCreated, response.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode safe authoring session: %v", err)
	}
	if created.ID == "" || created.Draft == nil ||
		len(created.Draft.Config.JavaVuser.AdditionalVMParameters) != 2 ||
		created.Draft.Config.JavaVuser.AdditionalVMParameters[1] != safeDraft.Config.JavaVuser.AdditionalVMParameters[1] {
		t.Fatalf("safe Java VM parameters did not round-trip: %#v", created.Draft)
	}

	const updateCanary = "authoring-update-api-key-canary"
	unsafeUpdate := createSafe
	unsafeUpdate.Name = "Unsafe Java argv update"
	unsafeUpdate.Draft = *javaVMDraft([]string{"-DserviceApiKey=" + updateCanary})
	response = serveAuthoringSessionJSON(
		t,
		server,
		http.MethodPut,
		"/api/v1/scripts/authoring/sessions/"+created.ID,
		unsafeUpdate,
	)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("unsafe update status = %d, want %d; body=%s", response.Code, http.StatusBadRequest, response.Body.String())
	}
	assertValueFreeJavaVMRejection(t, response.Body.String(), updateCanary)
	persisted, err := store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession: %v", err)
	}
	if persisted.Draft == nil ||
		len(persisted.Draft.Config.JavaVuser.AdditionalVMParameters) != 2 ||
		persisted.Draft.Config.JavaVuser.AdditionalVMParameters[1] != safeDraft.Config.JavaVuser.AdditionalVMParameters[1] {
		t.Fatalf("blocked update changed persisted Java VM parameters: %#v", persisted.Draft)
	}

	// Model a pre-fix row so every read surface also proves defense in depth.
	const legacyCanary = "legacy-java-vm-password-never-disclose"
	now := time.Now().UTC()
	legacy := &storage.AuthoringSession{
		ID:        "legacy-java-vm-authoring-session",
		ProjectID: projectID,
		Source:    "manual",
		Name:      "Legacy unsafe Java argv",
		Status:    "editing",
		Draft:     javaVMDraft([]string{"-Dlabel=api_key=" + legacyCanary}),
		CreatedAt: now,
		UpdatedAt: now,
	}
	if err := store.SaveAuthoringSession(legacy); err != nil {
		t.Fatalf("seed legacy authoring session: %v", err)
	}

	for name, path := range map[string]string{
		"get":  "/api/v1/scripts/authoring/sessions/" + legacy.ID,
		"list": "/api/v1/scripts/authoring/sessions?project_id=" + projectID,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, path, nil)
			result := httptest.NewRecorder()
			server.mux.ServeHTTP(result, request)
			if result.Code != http.StatusOK {
				t.Fatalf("%s status = %d; body=%s", name, result.Code, result.Body.String())
			}
			if strings.Contains(result.Body.String(), legacyCanary) ||
				!strings.Contains(result.Body.String(), credential.Redacted) {
				t.Fatalf("%s response did not mask legacy Java VM argv: %s", name, result.Body.String())
			}
			if strings.Contains(result.Body.String(), "secret_binding_requirements") ||
				strings.Contains(result.Body.String(), "{{secret.") {
				t.Fatalf("%s response offered a forbidden Java VM secret binding: %s", name, result.Body.String())
			}
		})
	}
}

func javaVMDraft(parameters []string) *dsl.Script {
	return &dsl.Script{
		Name:     "Java VM argument review",
		Version:  "1",
		Protocol: "java_vuser",
		Config: dsl.ScriptConfig{JavaVuser: dsl.JavaVuserConfig{
			AdditionalVMParameters: append([]string(nil), parameters...),
		}},
		Actions: []dsl.Action{{
			Name: "Run action",
			Type: "java_vuser_run",
			Params: map[string]any{
				"class":  "Actions",
				"method": "action",
			},
		}},
	}
}

func serveAuthoringSessionJSON(
	t *testing.T,
	server *Server,
	method string,
	path string,
	value any,
) *httptest.ResponseRecorder {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal authoring request: %v", err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(payload))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	return response
}

func assertValueFreeJavaVMRejection(t *testing.T, body, canary string) {
	t.Helper()
	if strings.Contains(body, canary) {
		t.Fatalf("Java VM rejection leaked source value: %s", body)
	}
	if strings.Contains(body, "secret_binding_requirements") ||
		strings.Contains(body, "{{secret.") {
		t.Fatalf("Java VM rejection offered a forbidden secret binding: %s", body)
	}
	if !strings.Contains(body, "cannot be stored or injected securely") {
		t.Fatalf("Java VM rejection omitted value-free remediation: %s", body)
	}
}
