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
	"github.com/jasonczech/apexload/pkg/internal/importreview"
	"github.com/jasonczech/apexload/pkg/secretstore"
	"github.com/jasonczech/apexload/pkg/storage"
)

func validationContextFixture(t *testing.T) (*Server, *storage.SQLiteStore, *runtimeProjectionSecretStore) {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "context.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	secrets := &runtimeProjectionSecretStore{values: map[string]string{}, attemptStore: store}
	return NewServer(ServerConfig{Store: store, ProjectSecretStore: secrets}), store, secrets
}

func secretReferencingScript() *dsl.Script {
	script := httpScript("", "Context script")
	script.ProjectID = storage.DefaultProjectID
	script.Actions[0].Target = "/orders?token={{secret.api_token}}"
	return script
}

func validateDraftForContext(t *testing.T, server *Server, script *dsl.Script) importreview.ScriptValidationResponse {
	t.Helper()
	response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validate", map[string]any{"project_id": storage.DefaultProjectID, "script": script, "fingerprint": "sdf1-test"}, "")
	if response.Code != http.StatusOK {
		t.Fatalf("validate: %d %s", response.Code, response.Body.String())
	}
	var body importreview.ScriptValidationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.ValidationContext == nil || !strings.HasPrefix(body.ValidationContext.Revision, "vctx1:") {
		t.Fatalf("validation carried no context: %s", response.Body.String())
	}
	return body
}

func probeValidationContext(t *testing.T, server *Server, body map[string]any) (*importreview.ValidationContext, int) {
	t.Helper()
	response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validation-context", body, "")
	if response.Code != http.StatusOK {
		return nil, response.Code
	}
	var context importreview.ValidationContext
	if err := json.Unmarshal(response.Body.Bytes(), &context); err != nil {
		t.Fatal(err)
	}
	return &context, response.Code
}

func hasContextFinding(context *importreview.ValidationContext, cause, subject string) bool {
	for _, finding := range context.Findings {
		if finding.Cause == cause && (subject == "" || finding.Subject == subject) {
			return true
		}
	}
	return false
}

func TestValidationContextChangesWithSecretAvailabilityAndRebinding(t *testing.T) {
	server, _, secrets := validationContextFixture(t)
	script := secretReferencingScript()

	missing := validateDraftForContext(t, server, script).ValidationContext
	if !hasContextFinding(missing, "secret_unavailable", "api_token") {
		t.Fatalf("an unavailable secret is not a contextual finding: %+v", missing.Findings)
	}
	if again := validateDraftForContext(t, server, script).ValidationContext; again.Revision != missing.Revision {
		t.Fatal("the context revision is not deterministic for an unchanged context")
	}

	secrets.mu.Lock()
	secrets.values[storage.DefaultProjectID+"/api_token"] = "first-secret-value"
	secrets.mu.Unlock()
	bound := validateDraftForContext(t, server, script).ValidationContext
	if bound.Revision == missing.Revision || bound.Components.SecretReferences == missing.Components.SecretReferences || hasContextFinding(bound, "secret_unavailable", "") {
		t.Fatalf("binding the secret did not change the context: %+v", bound)
	}
	if bound.Components.Policy != missing.Components.Policy || bound.Components.Capabilities != missing.Components.Capabilities || bound.Components.Approvals != missing.Components.Approvals {
		t.Fatal("a secret change moved an unrelated component")
	}

	secrets.mu.Lock()
	secrets.values[storage.DefaultProjectID+"/api_token"] = "rebound-secret-value"
	secrets.mu.Unlock()
	rebound, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script": script})
	if code != http.StatusOK || rebound.Revision == bound.Revision || rebound.Components.SecretReferences == bound.Components.SecretReferences {
		t.Fatalf("rebinding the secret did not change the probed context: %d %+v", code, rebound)
	}
	payload, _ := json.Marshal(rebound)
	if strings.Contains(string(payload), "rebound-secret-value") || strings.Contains(string(payload), "first-secret-value") {
		t.Fatal("the context discloses a secret value")
	}
}

func TestValidationContextChangesWithProjectPolicy(t *testing.T) {
	server, store, _ := validationContextFixture(t)
	script := secretReferencingScript()
	before := validateDraftForContext(t, server, script).ValidationContext
	project, err := store.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	after, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script": script})
	if code != http.StatusOK || after.Revision == before.Revision || after.Components.Policy == before.Components.Policy || after.Components.SecretReferences != before.Components.SecretReferences {
		t.Fatalf("a policy change did not move exactly the policy component: %+v vs %+v", before.Components, after.Components)
	}
}

func TestValidationContextReportsJavaApprovalAsContextual(t *testing.T) {
	server, _, _ := validationContextFixture(t)
	script := &dsl.Script{Name: "Java draft", Version: "1", Protocol: "java_vuser", ProjectID: storage.DefaultProjectID,
		Actions: []dsl.Action{{Name: "Run", Type: "java_vuser_run"}}}
	if !scriptRequiresJavaVuserAuthoringApproval(script) {
		t.Skip("this Java fixture does not require approval in this build")
	}
	context, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script": script})
	if code != http.StatusOK || !hasContextFinding(context, "approval_required", "") {
		t.Fatalf("Java approval is not a contextual finding: %d %+v", code, context)
	}
}

func TestValidationContextProbeMatchesValidationAndRefusesAmbiguousRequests(t *testing.T) {
	server, _, _ := validationContextFixture(t)
	script := secretReferencingScript()
	validated := validateDraftForContext(t, server, script).ValidationContext
	probed, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script": script})
	if code != http.StatusOK || probed.Revision != validated.Revision {
		t.Fatalf("the probe disagrees with validation for the same draft: %d %+v", code, probed)
	}
	if _, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID}); code != http.StatusBadRequest {
		t.Fatalf("a probe naming no script: %d", code)
	}
	if _, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script": script, "script_id": "x"}); code != http.StatusBadRequest {
		t.Fatalf("a probe naming both a draft and a stored script: %d", code)
	}
	if _, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script_id": "missing-script"}); code != http.StatusNotFound {
		t.Fatalf("a probe of a missing stored script: %d", code)
	}

	stored := secretReferencingScript()
	stored.ID = "context-stored-script"
	if _, err := server.runner.CreateScriptAuthoringDurable(stored); err != nil {
		t.Fatal(err)
	}
	response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/context-stored-script/validate", nil, "")
	var storedValidation importreview.ScriptValidationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &storedValidation); err != nil || storedValidation.ValidationContext == nil {
		t.Fatalf("stored validation carried no context: %s", response.Body.String())
	}
	probedStored, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script_id": "context-stored-script"})
	if code != http.StatusOK || probedStored.Revision != storedValidation.ValidationContext.Revision {
		t.Fatalf("the stored probe disagrees with stored validation: %d", code)
	}
}

func TestValidationContextIgnoresUnrelatedProjectSettings(t *testing.T) {
	server, store, _ := validationContextFixture(t)
	script := secretReferencingScript()
	before := validateDraftForContext(t, server, script).ValidationContext
	project, err := store.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.MaxVUsers = 77
	project.Settings.MaxConcurrentTests = 3
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	after, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script": script})
	if code != http.StatusOK || after.Revision != before.Revision {
		t.Fatalf("an unrelated project setting made the result stale: %d %+v", code, after)
	}
}

// failingSecretStore cannot be reached at all.
type failingSecretStore struct{ *runtimeProjectionSecretStore }

func (failingSecretStore) Get(context.Context, string, string) (string, secretstore.Metadata, error) {
	return "", secretstore.Metadata{}, errors.New("secret provider unreachable")
}

func TestValidationContextReadFailureIsNotAChange(t *testing.T) {
	server, store, secrets := validationContextFixture(t)
	script := secretReferencingScript()
	secrets.mu.Lock()
	secrets.values[storage.DefaultProjectID+"/api_token"] = "configured-value"
	secrets.mu.Unlock()
	validateDraftForContext(t, server, script)
	broken := NewServer(ServerConfig{Store: store, ProjectSecretStore: failingSecretStore{secrets}})
	if _, code := probeValidationContext(t, broken, map[string]any{"project_id": storage.DefaultProjectID, "script": script}); code != http.StatusServiceUnavailable {
		t.Fatalf("an unreachable secret provider answered %d, not 503", code)
	}
	response := authoringAPIRequest(t, broken, http.MethodPost, "/api/v1/scripts/validate", map[string]any{"project_id": storage.DefaultProjectID, "script": script, "fingerprint": "sdf1-test"}, "")
	var body importreview.ScriptValidationResponse
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusOK || body.ValidationContext != nil {
		t.Fatalf("validation with an unreachable provider: %d context=%v", response.Code, body.ValidationContext)
	}
}

func TestValidationContextStoredProbeRefusesANewerRevision(t *testing.T) {
	server, _, _ := validationContextFixture(t)
	stored := secretReferencingScript()
	stored.ID = "context-revision-script"
	created, err := server.runner.CreateScriptAuthoringDurable(stored)
	if err != nil {
		t.Fatal(err)
	}
	if _, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script_id": stored.ID, "content_revision": created.ContentRevision}); code != http.StatusOK {
		t.Fatalf("a probe at the current revision: %d", code)
	}
	response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validation-context", map[string]any{"project_id": storage.DefaultProjectID, "script_id": stored.ID, "content_revision": "scr-v1-" + strings.Repeat("0", 64)}, "")
	if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), "validation_context_revision_changed") {
		t.Fatalf("a probe at an older revision: %d %s", response.Code, response.Body.String())
	}
}

func TestValidationContextEndpointEnforcesProjectScope(t *testing.T) {
	server, store, _ := validationContextFixture(t)
	other := &storage.Project{ID: "context-other-project", Name: "Other", CreatedAt: time.Now(), UpdatedAt: time.Now()}
	if err := store.SaveProject(other); err != nil {
		t.Fatal(err)
	}
	foreign := secretReferencingScript()
	foreign.ID, foreign.ProjectID = "context-foreign-script", other.ID
	if _, err := server.runner.CreateScriptAuthoringDurable(foreign); err != nil {
		t.Fatal(err)
	}
	enableTrustedHeaderAuth(t, server)
	send := func(body map[string]any) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/validation-context", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Request-User", "viewer-user")
		req.Header.Set("X-APEXLoad-Project-Roles", storage.DefaultProjectID+"="+RoleViewer)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		return response
	}
	if response := send(map[string]any{"project_id": storage.DefaultProjectID, "script": secretReferencingScript()}); response.Code != http.StatusOK {
		t.Fatalf("a viewer probing a draft in their project: %d %s", response.Code, response.Body.String())
	}
	if response := send(map[string]any{"project_id": storage.DefaultProjectID, "script_id": foreign.ID}); response.Code != http.StatusForbidden && response.Code != http.StatusNotFound {
		t.Fatalf("a probe of another project's script: %d %s", response.Code, response.Body.String())
	}
	draft := secretReferencingScript()
	draft.ProjectID = other.ID
	if response := send(map[string]any{"project_id": other.ID, "script": draft}); response.Code != http.StatusForbidden && response.Code != http.StatusNotFound {
		t.Fatalf("a probe of a draft in another project: %d %s", response.Code, response.Body.String())
	}
}

func TestValidationContextTreatsAStoredValueTheResolverMissedAsAFailedRead(t *testing.T) {
	server, _, secrets := validationContextFixture(t)
	secrets.mu.Lock()
	secrets.values[storage.DefaultProjectID+"/api_token"] = "stored-value"
	secrets.mu.Unlock()
	// The resolver reported api_token missing although the project store
	// holds it (as when an application or assignment read failed).
	err := server.secretProviderFailure(context.Background(), storage.DefaultProjectID, secretReferencingScript(), []string{"api_token"})
	if !errors.Is(err, errSecretResolutionInconsistent) {
		t.Fatalf("a stored value the resolver missed: %v", err)
	}
	if err := server.secretProviderFailure(context.Background(), storage.DefaultProjectID, secretReferencingScript(), []string{"really_missing"}); err != nil {
		t.Fatalf("a genuinely unconfigured secret: %v", err)
	}
}

func TestValidationContextKeepsAProjectlessDraftsContext(t *testing.T) {
	server, _, secrets := validationContextFixture(t)
	secrets.mu.Lock()
	secrets.values[storage.DefaultProjectID+"/api_token"] = "default-project-value"
	secrets.mu.Unlock()
	draft := secretReferencingScript()
	draft.ProjectID = ""
	context, code := probeValidationContext(t, server, map[string]any{"script": draft})
	if code != http.StatusOK || context == nil {
		t.Fatalf("a draft without a project lost its context: %d", code)
	}
}
