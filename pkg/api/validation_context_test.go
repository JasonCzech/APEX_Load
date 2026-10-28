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
	"github.com/jasonczech/apexload/pkg/importartifact"
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

func TestValidationContextChangesWithSecretAvailabilityButNotItsValue(t *testing.T) {
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

	// Validation depends on whether a secret is configured, not on its
	// value, so a new value leaves the context as it was and no guess of a
	// value can be confirmed against it.
	secrets.mu.Lock()
	secrets.values[storage.DefaultProjectID+"/api_token"] = "replaced-secret-value"
	secrets.mu.Unlock()
	replaced, code := probeValidationContext(t, server, map[string]any{"project_id": storage.DefaultProjectID, "script": script})
	if code != http.StatusOK || replaced.Revision != bound.Revision {
		t.Fatalf("replacing a secret's value changed the probed context: %d %+v", code, replaced)
	}
	payload, _ := json.Marshal(replaced)
	if strings.Contains(string(payload), "replaced-secret-value") || strings.Contains(string(payload), "first-secret-value") {
		t.Fatal("the context discloses a secret value")
	}
}

// failingApplications cannot read any application.
type failingApplications struct{}

func (failingApplications) GetApplication(string) (*storage.Application, error) {
	return nil, errors.New("application store unreachable")
}

func TestValidationContextFollowsTheResolversApplicationBinding(t *testing.T) {
	records, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "context-applications.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = records.Close() })
	now := time.Now().UTC()
	for _, project := range []string{"shared-project", "other-project"} {
		if err := records.SaveProject(&storage.Project{ID: project, Name: project, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	for id, project := range map[string]string{"app-a": "shared-project", "app-x": "other-project"} {
		if err := records.SaveApplication(&storage.Application{ID: id, ProjectID: project, Name: id, CreatedAt: now, UpdatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	wrapper, err := importartifact.NewLocalAESKeyWrapper(importartifact.LocalKeyOptions{Key: bytes.Repeat([]byte{0x42}, 32)})
	if err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: records, BrunoArtifactWrapper: wrapper})
	for path, value := range map[string]string{
		"/api/v1/projects/shared-project/secrets": "project-password",
		"/api/v1/applications/app-x/secrets":      "foreign-application-password",
	} {
		if response := secretAPIRequest(t, server, http.MethodPost, path, map[string]any{
			"secrets": []map[string]string{{"name": "LOGIN_PASSWORD", "value": value}},
		}); response.Code != http.StatusCreated {
			t.Fatalf("%s: %d %s", path, response.Code, response.Body.String())
		}
	}
	script := func(id, project string) *dsl.Script {
		return &dsl.Script{ID: id, Name: id, Version: "1", Protocol: "http", ProjectID: project,
			Config:  dsl.ScriptConfig{BaseURL: "https://example.test"},
			Actions: []dsl.Action{{Name: "Call", Type: "request", Target: "/?p={{secret.LOGIN_PASSWORD}}"}}}
	}
	for id, place := range map[string][2]string{"assigned-script": {"shared-project", "app-a"}, "foreign-script": {"other-project", "app-x"}} {
		if _, err := server.runner.StoreScriptDurable(script(id, place[0])); err != nil {
			t.Fatal(err)
		}
		assignment, err := records.ListLibraryAssignments("script", []string{id})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := records.ReplaceLibraryAssignment("script", id, place[0], ptrString(place[1]), nil, assignment[id].Revision); err != nil {
			t.Fatal(err)
		}
	}
	probe := func(draft *dsl.Script) *importreview.ValidationContext {
		t.Helper()
		context, code := probeValidationContext(t, server, map[string]any{"project_id": "shared-project", "script": draft})
		if code != http.StatusOK {
			t.Fatalf("probe of %q: %d", draft.ID, code)
		}
		return context
	}

	unassigned := probe(script("", "shared-project"))
	assigned := probe(script("assigned-script", "shared-project"))
	if hasContextFinding(unassigned, "secret_unavailable", "") || hasContextFinding(assigned, "secret_unavailable", "") {
		t.Fatalf("the project secret is unavailable: %+v %+v", unassigned.Findings, assigned.Findings)
	}
	if assigned.Components.SecretReferences == unassigned.Components.SecretReferences || assigned.Revision == unassigned.Revision {
		t.Fatal("resolving through an application did not change the secret-reference binding")
	}
	// A draft's ID is checked durably, not against this replica's cache: a
	// script stored by another replica keeps its application binding.
	if _, err := records.CreateScriptAuthoringIfAbsent(script("uncached-script", "shared-project")); err != nil {
		t.Fatal(err)
	}
	uncachedAssignment, err := records.ListLibraryAssignments("script", []string{"uncached-script"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := records.ReplaceLibraryAssignment("script", "uncached-script", "shared-project", ptrString("app-a"), nil, uncachedAssignment["uncached-script"].Revision); err != nil {
		t.Fatal(err)
	}
	if _, cached := server.runner.GetScript("uncached-script"); cached {
		t.Fatal("the fixture script is in this replica's cache")
	}
	if uncached := probe(script("uncached-script", "shared-project")); uncached.Components.SecretReferences != assigned.Components.SecretReferences {
		t.Fatal("a script missing from the cache lost its application binding")
	}
	if _, err := server.runner.projectSecretStore.Replace(context.Background(), "shared-project", "LOGIN_PASSWORD", "rotated-password", "tester"); err != nil {
		t.Fatal(err)
	}
	if rotated := probe(script("", "shared-project")); rotated.Revision != unassigned.Revision {
		t.Fatal("rotating a secret value changed the context")
	}

	// Another project's script ID neither borrows its application's secrets
	// nor reveals them: the draft resolves as an unsaved draft of its project.
	if borrowed := probe(script("foreign-script", "shared-project")); borrowed.Revision != unassigned.Revision {
		t.Fatalf("a foreign script ID changed the draft's context: %+v", borrowed)
	}

	// An application in another project, or one that no longer exists,
	// resolves nothing, and there is then nothing for the re-check to find.
	for _, application := range []string{"app-x", "missing-application"} {
		binding := server.runner.secretBindingForScope("shared-project", application)
		if !binding.Refused || binding.Err != nil || binding.ApplicationID != "" {
			t.Fatalf("binding to %s = %+v", application, binding)
		}
		if err := server.secretProviderFailure(context.Background(), binding, []string{"LOGIN_PASSWORD"}); err != nil {
			t.Fatalf("re-check of a refused binding to %s: %v", application, err)
		}
	}

	// A failed application read is a failed read, not a refused binding.
	server.runner.applicationStore = failingApplications{}
	if _, code := probeValidationContext(t, server, map[string]any{"project_id": "shared-project", "script": script("assigned-script", "shared-project")}); code != http.StatusServiceUnavailable {
		t.Fatalf("an unreadable application answered %d, not 503", code)
	}
}

func TestValidationContextBindsADraftToItsAuthorizedProject(t *testing.T) {
	server, store, _ := validationContextFixture(t)
	now := time.Now()
	if err := store.SaveProject(&storage.Project{ID: "context-folder-project", Name: "Folder project", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveFolder(&storage.Folder{ID: "context-folder", ProjectID: "context-folder-project", Name: "Scripts", Category: "scripts", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	enableTrustedHeaderAuth(t, server)
	send := func(path string, body map[string]any) *httptest.ResponseRecorder {
		payload, _ := json.Marshal(body)
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Request-User", "folder-viewer")
		req.Header.Set("X-APEXLoad-Project-Roles", "context-folder-project="+RoleViewer)
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, req)
		return response
	}
	draft := secretReferencingScript()
	draft.ProjectID = ""
	validated := send("/api/v1/scripts/validate", map[string]any{"folder_id": "context-folder", "script": draft, "fingerprint": "sdf1-test"})
	var validation importreview.ScriptValidationResponse
	if err := json.Unmarshal(validated.Body.Bytes(), &validation); err != nil || validated.Code != http.StatusOK || validation.ValidationContext == nil {
		t.Fatalf("validation of a folder draft: %d %s", validated.Code, validated.Body.String())
	}
	for _, body := range []map[string]any{
		{"folder_id": "context-folder", "script": draft},
		{"project_id": "context-folder-project", "script": draft},
	} {
		response := send("/api/v1/scripts/validation-context", body)
		var probed importreview.ValidationContext
		if err := json.Unmarshal(response.Body.Bytes(), &probed); err != nil || response.Code != http.StatusOK || probed.Revision != validation.ValidationContext.Revision {
			t.Fatalf("probe %v disagrees with the folder draft's validation: %d %s", body, response.Code, response.Body.String())
		}
	}
	// The draft's own project must be the authorized one.
	elsewhere := secretReferencingScript()
	if response := send("/api/v1/scripts/validate", map[string]any{"folder_id": "context-folder", "script": elsewhere, "fingerprint": "sdf1-test"}); response.Code != http.StatusBadRequest {
		t.Fatalf("a draft naming another project: %d %s", response.Code, response.Body.String())
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
	// Another project's script is as hidden as a missing one.
	if response := send(map[string]any{"project_id": storage.DefaultProjectID, "script_id": foreign.ID}); response.Code != http.StatusNotFound {
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
	project := scriptSecretBinding{ProjectID: storage.DefaultProjectID}
	err := server.secretProviderFailure(context.Background(), project, []string{"api_token"})
	if !errors.Is(err, errSecretResolutionInconsistent) {
		t.Fatalf("a stored value the resolver missed: %v", err)
	}
	if err := server.secretProviderFailure(context.Background(), project, []string{"really_missing"}); err != nil {
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

func validateScenarioWithContext(t *testing.T, server *Server, req CreateScenarioRequest, contextOnly bool) (*LoadModelValidationReport, int) {
	t.Helper()
	path := "/api/v1/scenarios/validate-load-model"
	if contextOnly {
		path += "?context_only=true"
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.mux.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		return nil, recorder.Code
	}
	var report LoadModelValidationReport
	if err := json.Unmarshal(recorder.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode scenario validation report: %v", err)
	}
	return &report, recorder.Code
}

func TestScenarioValidationContextTracksConstituentScriptsPoliciesApprovalsAndSecrets(t *testing.T) {
	server, store, projectSecrets := validationContextFixture(t)

	draftScript := secretReferencingScript()
	draftScript.ID = "scenario-vctx-script"
	draftScript.Name = "Constituent Script"
	storedScript, err := server.runner.CreateScriptAuthoringDurable(draftScript)
	if err != nil {
		t.Fatal(err)
	}

	scenarioReq := CreateScenarioRequest{
		Name:     "Validated Scenario",
		Duration: "30s",
		Groups: []CreateGroupEntry{{
			Name:     "MainGroup",
			ScriptID: storedScript.ID,
			VUsers:   2,
		}},
	}

	initialReport, code := validateScenarioWithContext(t, server, scenarioReq, false)
	if code != http.StatusOK || initialReport == nil || initialReport.ValidationContext == nil {
		t.Fatalf("initial scenario validation status = %d, report = %+v", code, initialReport)
	}
	initialCtx := initialReport.ValidationContext

	// 1. Secret provisioning changes secret_references component without changing others;
	// rotating the secret's value does not change secret_references.
	projectSecrets.mu.Lock()
	projectSecrets.values[storage.DefaultProjectID+"/api_token"] = "value-v1"
	projectSecrets.mu.Unlock()
	afterSecretSet, code := validateScenarioWithContext(t, server, scenarioReq, true)
	if code != http.StatusOK || afterSecretSet.ValidationContext == nil {
		t.Fatalf("after secret set status = %d", code)
	}
	if afterSecretSet.ValidationContext.Components.SecretReferences == initialCtx.Components.SecretReferences {
		t.Fatal("expected secret_references component to change when referenced secret becomes available")
	}
	if afterSecretSet.ValidationContext.Components.Scripts != initialCtx.Components.Scripts {
		t.Fatal("scripts component changed on secret set")
	}
	projectSecrets.mu.Lock()
	projectSecrets.values[storage.DefaultProjectID+"/api_token"] = "value-v2-rotated"
	projectSecrets.mu.Unlock()
	afterSecretRotate, code := validateScenarioWithContext(t, server, scenarioReq, true)
	if code != http.StatusOK || afterSecretRotate.ValidationContext == nil {
		t.Fatalf("after secret rotate status = %d", code)
	}
	if afterSecretRotate.ValidationContext.Revision != afterSecretSet.ValidationContext.Revision {
		t.Fatal("rotating secret value must not invalidate scenario validation context")
	}

	// 2. Editing a constituent script changes the scripts component.
	_, tag := requireAuthoringAPIProjection(t, server, storedScript.ID)
	updatedScript := *storedScript.Script
	updatedScript.Name = "Constituent Script Edited"
	updatedScript.Actions[0].Target = "/orders/v2?token={{secret.api_token}}"
	if putRec := authoringAPIRequest(t, server, http.MethodPut, "/api/v1/scripts/"+storedScript.ID, map[string]any{"script": &updatedScript}, tag); putRec.Code != http.StatusOK {
		t.Fatalf("update constituent script: %d %s", putRec.Code, putRec.Body.String())
	}
	afterScriptEdit, code := validateScenarioWithContext(t, server, scenarioReq, true)
	if code != http.StatusOK || afterScriptEdit.ValidationContext == nil {
		t.Fatalf("after script edit status = %d", code)
	}
	if afterScriptEdit.ValidationContext.Components.Scripts == afterSecretRotate.ValidationContext.Components.Scripts {
		t.Fatal("expected scripts component to change after constituent script edit")
	}

	// 3. Project policy change changes the policy component.
	project, err := store.GetProject(storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	project.Settings.Authoring = &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: true}
	if err := store.SaveProject(project); err != nil {
		t.Fatal(err)
	}
	afterPolicyChange, code := validateScenarioWithContext(t, server, scenarioReq, true)
	if code != http.StatusOK || afterPolicyChange.ValidationContext == nil {
		t.Fatalf("after policy change status = %d", code)
	}
	if afterPolicyChange.ValidationContext.Components.Policy == afterScriptEdit.ValidationContext.Components.Policy {
		t.Fatal("expected policy component to change after project policy update")
	}

	// 4. Deleting a constituent script marks scripts component changed on context_only probe (200 OK).
	if delRec := authoringAPIRequest(t, server, http.MethodDelete, "/api/v1/scripts/"+storedScript.ID, nil, ""); delRec.Code != http.StatusOK {
		t.Fatalf("delete constituent script: %d %s", delRec.Code, delRec.Body.String())
	}
	afterScriptDelete, code := validateScenarioWithContext(t, server, scenarioReq, true)
	if code != http.StatusOK || afterScriptDelete.ValidationContext == nil {
		t.Fatalf("after script delete probe status = %d", code)
	}
	if afterScriptDelete.ValidationContext.Components.Scripts == afterPolicyChange.ValidationContext.Components.Scripts {
		t.Fatal("expected scripts component to change when constituent script is deleted")
	}

	// 5. Project-scoped header resolves the exact same project ID on both full validation
	// and ?context_only=true (including empty-group drafts via resolveScenarioValidationContextProjectID).
	opsProject := &storage.Project{
		ID:        "proj-ops",
		Name:      "Ops",
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		Settings: storage.ProjectSettings{
			Authoring: &storage.ProjectAuthoringPolicy{RequireScenarioSaveIfMatch: false},
		},
	}
	if err := store.SaveProject(opsProject); err != nil {
		t.Fatal(err)
	}
	opsScript := secretReferencingScript()
	opsScript.ID = "scenario-vctx-ops-script"
	opsScript.ProjectID = "proj-ops"
	if _, err := server.runner.CreateScriptAuthoringDurable(opsScript); err != nil {
		t.Fatal(err)
	}
	scopedEmptyReq := httptest.NewRequest(http.MethodPost, "/api/v1/scenarios/validate-load-model", nil)
	scopedEmptyReq = scopedEmptyReq.WithContext(context.WithValue(scopedEmptyReq.Context(), authorizedProjectContextKey{}, "proj-ops"))
	if got := server.resolveScenarioValidationContextProjectID(scopedEmptyReq, CreateScenarioRequest{Name: "Empty Draft", Duration: "30s"}); got != "proj-ops" {
		t.Fatalf("resolveScenarioValidationContextProjectID for empty draft = %q, want proj-ops", got)
	}
	enableTrustedHeaderAuth(t, server)
	sendProjectScoped := func(contextOnly bool) *LoadModelValidationReport {
		t.Helper()
		path := "/api/v1/scenarios/validate-load-model"
		if contextOnly {
			path += "?context_only=true"
		}
		raw, err := json.Marshal(CreateScenarioRequest{
			Name:     "Ops Draft",
			Duration: "30s",
			Groups:   []CreateGroupEntry{{Name: "OpsGroup", ScriptID: opsScript.ID, VUsers: 1}},
		})
		if err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-Auth-Request-User", "ops-editor")
		req.Header.Set("X-APEXLoad-Project-Roles", "proj-ops="+RoleEditor)
		rec := httptest.NewRecorder()
		server.mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("project-scoped scenario validation (context_only=%v) = %d: %s", contextOnly, rec.Code, rec.Body.String())
		}
		var rep LoadModelValidationReport
		if err := json.Unmarshal(rec.Body.Bytes(), &rep); err != nil {
			t.Fatal(err)
		}
		return &rep
	}
	fullOpsReport := sendProjectScoped(false)
	probeOpsReport := sendProjectScoped(true)
	if fullOpsReport.ValidationContext == nil || probeOpsReport.ValidationContext == nil {
		t.Fatalf("expected non-nil ValidationContext on both full and context_only reports")
	}
	if fullOpsReport.ValidationContext.Revision != probeOpsReport.ValidationContext.Revision {
		t.Fatalf("full validation context revision (%q) != context_only revision (%q)",
			fullOpsReport.ValidationContext.Revision, probeOpsReport.ValidationContext.Revision)
	}
	if fullOpsReport.ValidationContext.Components.Policy == afterPolicyChange.ValidationContext.Components.Policy {
		t.Fatal("expected proj-ops policy digest to differ from default project policy digest")
	}
}
