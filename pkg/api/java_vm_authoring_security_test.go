package api

import (
	"bytes"
	"encoding/json"
	"fmt"
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

func TestImportedJavaVMCredentialSurvivesAuthoringApprovalWithoutReentry(t *testing.T) {
	const (
		projectID = "project-imported-java-vm-argv"
		canary    = "imported-java-password-remains-runnable"
	)
	server := newJavaVuserGovernanceServer(t, projectID)
	draft := combinedJavaVuserGovernanceDraft(projectID)
	draft.Config.JavaVuser.AdditionalVMParameters = []string{
		"-Xmx512m",
		"-Ddatabase.password=" + canary,
	}
	markCredentialPreservingImportScript(draft)
	importRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/enterprise-archive?project_id="+projectID,
		nil,
	)
	if err := server.attachImportDraftAttestation(importRequest, draft); err != nil {
		t.Fatalf("attach import draft attestation: %v", err)
	}

	approvedResponse := attemptJavaVuserDraftApprovalThroughAPI(
		t,
		server,
		projectID,
		"enterprise_script_archive",
		draft,
	)
	if approvedResponse.Code != http.StatusOK {
		t.Fatalf(
			"approve imported Java Vuser status = %d body=%s",
			approvedResponse.Code,
			approvedResponse.Body.String(),
		)
	}
	if !strings.Contains(approvedResponse.Body.String(), canary) ||
		strings.Contains(approvedResponse.Body.String(), credential.Redacted) {
		t.Fatalf("approval response did not preserve Java VM credential: %s", approvedResponse.Body.String())
	}

	var approved authoringSessionApprovalResponse
	if err := json.Unmarshal(approvedResponse.Body.Bytes(), &approved); err != nil {
		t.Fatalf("decode approval: %v", err)
	}
	if approved.Script == nil {
		t.Fatalf("approval response omitted script: %#v", approved)
	}
	durable := server.runner.scripts[approved.Script.ID]
	if durable == nil || durable.Script == nil {
		t.Fatalf("approved script %q is not durable", approved.Script.ID)
	}
	if got := durable.Script.Config.JavaVuser.AdditionalVMParameters; len(got) != 2 ||
		got[1] != "-Ddatabase.password="+canary {
		t.Fatalf("durable imported Java VM argv = %#v", got)
	}
	if !isCredentialPreservingImport(durable.Script) {
		t.Fatal("approved imported Java Vuser lost credential-preserving provenance")
	}
	if issues := validateStoredExecutionScript(durable.Script); len(issues) != 0 {
		t.Fatalf("approved imported Java Vuser validation issues = %v", issues)
	}
}

// A saved credential-preserving Java import keeps its credential-bearing VM
// argument, which only its stored-execution validation accepts; the strict
// draft validator reports it as sensitive. The editor validates its draft through
// POST /scripts/validate once it holds session variables or an edit. That
// draft names the saved script, so it is judged as a debug start of the saved
// script judges it: resolved against the durable copy and validated with the
// stored-execution validator. A clean echo, the echo with variables and an
// unrelated edit report no static issue, and the debug start agrees; a draft
// that names no saved import keeps the strict validation.
func TestDraftValidationOfStoredCredentialPreservingImportMatchesDebugStart(t *testing.T) {
	const (
		projectID = "project-imported-java-vm-draft-validation"
		canary    = "imported-java-password-draft-validation"
	)
	server := newJavaVuserGovernanceServer(t, projectID)
	draft := combinedJavaVuserGovernanceDraft(projectID)
	draft.Config.JavaVuser.AdditionalVMParameters = []string{"-Xmx512m", "-Ddatabase.password=" + canary}
	markCredentialPreservingImportScript(draft)
	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-archive?project_id="+projectID, nil)
	if err := server.attachImportDraftAttestation(importRequest, draft); err != nil {
		t.Fatalf("attach import draft attestation: %v", err)
	}
	approved := approveJavaVuserDraftThroughAPI(t, server, projectID, draft)
	id := approved.Script.ID
	if stored, ok := server.runner.GetScript(id); !ok || !isCredentialPreservingImport(stored.Script) {
		t.Fatal("the approved import is not a saved credential-preserving import")
	}

	projection, _ := requireAuthoringAPIProjection(t, server, id)
	document := func(t *testing.T, edit func(document map[string]any)) map[string]any {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal([]byte(mustMarshalJSON(t, projection.Script)), &value); err != nil {
			t.Fatal(err)
		}
		if edit != nil {
			edit(value)
		}
		return value
	}
	echo := document(t, nil)
	// An edit outside the kept credential's execution context: the script's
	// name and its think time.
	edited := document(t, func(document map[string]any) {
		document["name"] = "Combined Java Vuser (renamed)"
		config, _ := document["config"].(map[string]any)
		config["think_time"] = map[string]any{"type": "fixed", "mean_ms": 250}
	})
	// An edit inside it, which the debug start refuses.
	retuned := document(t, func(document map[string]any) {
		config, _ := document["config"].(map[string]any)
		java, _ := config["java_vuser"].(map[string]any)
		java["max_processes"] = 20
	})
	unsaved := document(t, func(document map[string]any) { delete(document, "id") })

	vmIssue := func(issues []string) bool { return containsIssueText(issues, "additional_vm_parameters") }
	validate := func(t *testing.T, script map[string]any, variables map[string]string) scriptValidationResponse {
		t.Helper()
		body := map[string]any{"project_id": projectID, "script": script}
		if variables != nil {
			body["variables"] = variables
		}
		response := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/validate", body, "")
		if response.Code != http.StatusOK {
			t.Fatalf("validate = %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), canary) {
			t.Fatalf("draft validation disclosed the kept VM argument: %s", response.Body.String())
		}
		var decoded scriptValidationResponse
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return decoded
	}

	for _, check := range []struct {
		name      string
		script    map[string]any
		variables map[string]string
	}{
		{name: "clean echo", script: echo},
		{name: "echo with session variables", script: echo, variables: map[string]string{"orderId": "synthetic-order-1001"}},
		{name: "unrelated edit", script: edited},
	} {
		t.Run(check.name, func(t *testing.T) {
			got := validate(t, check.script, check.variables)
			if vmIssue(got.Issues) || got.Preflight == nil || vmIssue(got.Preflight.Issues) {
				t.Fatalf("draft validation issues = %v (preflight %+v), want no static VM argument issue", got.Issues, got.Preflight)
			}
		})
	}

	for index, script := range []map[string]any{echo, edited} {
		sessionID := fmt.Sprintf("88888888-8888-4888-8888-88888888888%d", index)
		started := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+id+"/debug/sessions", map[string]any{"project_id": projectID, "replay_session_id": sessionID, "script": script}, "")
		if started.Code == http.StatusOK {
			_, _ = server.scriptDebug.Stop(sessionID)
		}
		if started.Code != http.StatusOK || strings.Contains(started.Body.String(), "additional_vm_parameters") {
			t.Fatalf("debug start of draft %d = %d %s, want it started without a VM argument issue", index, started.Code, started.Body.String())
		}
	}

	refused := authoringAPIRequest(t, server, http.MethodPost, "/api/v1/scripts/"+id+"/debug/sessions", map[string]any{"project_id": projectID, "replay_session_id": "88888888-8888-4888-8888-888888888889", "script": retuned}, "")
	if refused.Code != http.StatusBadRequest || !strings.Contains(refused.Body.String(), "retained imported credentials") {
		t.Fatalf("debug start of the edit inside the credential context = %d %s, want the retained-credential refusal", refused.Code, refused.Body.String())
	}
	for name, script := range map[string]map[string]any{"edit the debug start refuses": retuned, "draft naming no saved import": unsaved} {
		if got := validate(t, script, nil); !vmIssue(got.Issues) {
			t.Fatalf("validation of the %s = %v, want the strict VM argument issue", name, got.Issues)
		}
	}
}

// Whether a draft naming a saved credential-preserving import reconciles
// depends on its kept credential values: an echo carrying the correct kept
// Java VM argument validates with the stored-execution validator, one carrying
// a wrong value is refused and validated strictly. GET /api/v1/scripts/{id}
// discloses those values to its readers, so the difference tells them nothing
// new; a caller that may validate drafts but that GET refuses (a service
// account holding scripts:write without read) gets the strict validation for
// both, byte-identical, while a project viewer and editor keep the trusted
// result for a clean echo.
func TestDraftValidationOfStoredCredentialPreservingImportRequiresReadAccess(t *testing.T) {
	const (
		projectID = "project-imported-java-vm-validation-read"
		canary    = "imported-java-password-validation-read"
		apiKey    = "validation-read-scripts-write-only-key"
	)
	server := newJavaVuserGovernanceServer(t, projectID)
	draft := combinedJavaVuserGovernanceDraft(projectID)
	draft.Config.JavaVuser.AdditionalVMParameters = []string{"-Xmx512m", "-Ddatabase.password=" + canary}
	markCredentialPreservingImportScript(draft)
	importRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/enterprise-archive?project_id="+projectID, nil)
	if err := server.attachImportDraftAttestation(importRequest, draft); err != nil {
		t.Fatalf("attach import draft attestation: %v", err)
	}
	id := approveJavaVuserDraftThroughAPI(t, server, projectID, draft).Script.ID
	projection, _ := requireAuthoringAPIProjection(t, server, id)
	echoKeeping := func(t *testing.T, kept string) map[string]any {
		t.Helper()
		var value map[string]any
		if err := json.Unmarshal([]byte(mustMarshalJSON(t, projection.Script)), &value); err != nil {
			t.Fatal(err)
		}
		config, _ := value["config"].(map[string]any)
		java, _ := config["java_vuser"].(map[string]any)
		parameters, _ := java["additional_vm_parameters"].([]any)
		if len(parameters) != 2 || parameters[1] != "-Ddatabase.password="+canary {
			t.Fatalf("projection Java VM arguments = %#v, want the kept value disclosed", parameters)
		}
		parameters[1] = "-Ddatabase.password=" + kept
		return value
	}
	correct := echoKeeping(t, canary)
	wrong := echoKeeping(t, "wrong-guess-at-the-kept-password")

	enableTrustedHeaderAuth(t, server)
	now := time.Now().UTC()
	if err := server.store.SaveServiceAccount(&storage.ServiceAccount{
		ID: "validation-scripts-writer", Name: "Validation scripts writer", KeyHash: hashAPIKey(apiKey), Active: true,
		ProjectRoles: map[string][]string{projectID: {RoleEditor}}, Scopes: []string{"scripts:write"}, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	writer := http.Header{"X-APEXLoad-API-Key": {apiKey}}
	readers := map[string]http.Header{
		"viewer": {"X-Auth-Request-User": {"validation-viewer"}, "X-APEXLoad-Project-Roles": {projectID + "=" + RoleViewer}},
		"editor": {"X-Auth-Request-User": {"validation-editor"}, "X-APEXLoad-Project-Roles": {projectID + "=" + RoleEditor}},
	}
	serve := func(t *testing.T, method, path string, body any, headers http.Header) *httptest.ResponseRecorder {
		t.Helper()
		var encoded []byte
		if body != nil {
			var err error
			if encoded, err = json.Marshal(body); err != nil {
				t.Fatal(err)
			}
		}
		request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
		request.Header.Set("Content-Type", "application/json")
		for name, values := range headers {
			for _, value := range values {
				request.Header.Add(name, value)
			}
		}
		response := httptest.NewRecorder()
		server.mux.ServeHTTP(response, request)
		return response
	}
	if got := serve(t, http.MethodGet, "/api/v1/scripts/"+id, nil, writer); got.Code != http.StatusForbidden {
		t.Fatalf("scripts:write-only read of the saved import = %d %s, want 403", got.Code, got.Body.String())
	}
	for name, headers := range readers {
		if got := serve(t, http.MethodGet, "/api/v1/scripts/"+id, nil, headers); got.Code != http.StatusOK || !strings.Contains(got.Body.String(), canary) {
			t.Fatalf("%s read of the saved import = %d, want 200 disclosing the kept value", name, got.Code)
		}
	}

	vmIssue := func(issues []string) bool { return containsIssueText(issues, "additional_vm_parameters") }
	validate := func(t *testing.T, script map[string]any, headers http.Header) (string, scriptValidationResponse) {
		t.Helper()
		response := serve(t, http.MethodPost, "/api/v1/scripts/validate", map[string]any{"project_id": projectID, "script": script}, headers)
		if response.Code != http.StatusOK {
			t.Fatalf("validate = %d %s", response.Code, response.Body.String())
		}
		if strings.Contains(response.Body.String(), canary) {
			t.Fatalf("draft validation disclosed the kept VM argument: %s", response.Body.String())
		}
		var decoded scriptValidationResponse
		if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
			t.Fatal(err)
		}
		return response.Body.String(), decoded
	}

	correctBody, correctResult := validate(t, correct, writer)
	wrongBody, _ := validate(t, wrong, writer)
	if correctBody != wrongBody {
		t.Fatalf("validation without read access told the correct kept value from a wrong one:\ncorrect: %s\nwrong:   %s", correctBody, wrongBody)
	}
	if !vmIssue(correctResult.Issues) {
		t.Fatalf("validation without read access = %v, want the strict VM argument issue", correctResult.Issues)
	}
	for name, headers := range readers {
		t.Run(name, func(t *testing.T) {
			if _, got := validate(t, correct, headers); vmIssue(got.Issues) || got.Preflight == nil || vmIssue(got.Preflight.Issues) {
				t.Fatalf("clean echo issues = %v (preflight %+v), want no static VM argument issue", got.Issues, got.Preflight)
			}
			if _, got := validate(t, wrong, headers); !vmIssue(got.Issues) {
				t.Fatalf("wrong kept value issues = %v, want the strict VM argument issue", got.Issues)
			}
		})
	}
}

func TestCredentialPreservingAuthoringCreateRetryDoesNotRequireReimport(t *testing.T) {
	const (
		projectID      = "project-imported-java-vm-idempotent"
		canary         = "imported-java-password-idempotent-retry"
		idempotencyKey = "enterprise-java-import:revision-17:item-4"
	)
	server := newJavaVuserGovernanceServer(t, projectID)
	draft := combinedJavaVuserGovernanceDraft(projectID)
	draft.Config.JavaVuser.AdditionalVMParameters = []string{
		"-Ddatabase.password=" + canary,
	}
	markCredentialPreservingImportScript(draft)
	importRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/enterprise-archive?project_id="+projectID,
		nil,
	)
	if err := server.attachImportDraftAttestation(importRequest, draft); err != nil {
		t.Fatalf("attach import draft attestation: %v", err)
	}
	create := authoringSessionRequest{
		ProjectID:      projectID,
		Source:         "enterprise_script_archive",
		Name:           "Imported Java idempotency",
		Status:         "ready",
		Draft:          *draft,
		IdempotencyKey: idempotencyKey,
	}
	first := serveAuthoringSessionJSON(
		t,
		server,
		http.MethodPost,
		"/api/v1/scripts/authoring/sessions",
		create,
	)
	if first.Code != http.StatusCreated {
		t.Fatalf("initial create status = %d body=%s", first.Code, first.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode initial authoring session: %v", err)
	}

	// The import capability is one-shot and has already been completed. A
	// response-lost retry must converge on the durable idempotency row rather
	// than asking the operator to import or enter the password again.
	retry := serveAuthoringSessionJSON(
		t,
		server,
		http.MethodPost,
		"/api/v1/scripts/authoring/sessions",
		create,
	)
	if retry.Code != http.StatusOK {
		t.Fatalf("retry create status = %d body=%s", retry.Code, retry.Body.String())
	}
	var replayed storage.AuthoringSession
	if err := json.Unmarshal(retry.Body.Bytes(), &replayed); err != nil {
		t.Fatalf("decode replayed authoring session: %v", err)
	}
	if replayed.ID != created.ID {
		t.Fatalf("replayed session ID = %q, want %q", replayed.ID, created.ID)
	}
	if !strings.Contains(retry.Body.String(), canary) ||
		strings.Contains(retry.Body.String(), credential.Redacted) {
		t.Fatalf("idempotent replay did not preserve imported Java password: %s", retry.Body.String())
	}
	persisted, err := server.loadAuthoringSession(created.ID)
	if err != nil {
		t.Fatalf("load authoring session: %v", err)
	}
	if got := persisted.Draft.Config.JavaVuser.AdditionalVMParameters; len(got) != 1 ||
		got[0] != "-Ddatabase.password="+canary {
		t.Fatalf("durable idempotent Java argv = %#v", got)
	}
}

func TestForgedImportMetadataCannotAuthorizeTransientJavaVMCredentials(t *testing.T) {
	const canary = "forged-import-marker-password"
	draft := javaVMDraft([]string{"-Ddatabase.password=" + canary})
	draft.Actions[0].Target = "Actions"
	draft.ProjectID = "project-forged-java-import"
	draft.Metadata = &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
		Source: "enterprise_script_archive",
		Preserved: map[string]any{
			dsl.ImportPreservedCredentialPolicyKey: dsl.ImportPreservedCredentialPolicyPreserve,
		},
	}}

	if issues := validateScriptForReplay(draft); !containsIssueText(issues, "sensitive_property") {
		t.Fatalf("untrusted replay validation issues = %v, want sensitive_property", issues)
	}
	if issues := validateScriptForReplayWithImportTrust(draft, true); len(issues) != 0 {
		t.Fatalf("trusted durable replay validation issues = %v", issues)
	}

	server := NewServer(ServerConfig{MaxConcurrent: 1})
	preparedResponse := httptest.NewRecorder()
	prepared, issues, _, ok := server.prepareClassicReplayScript(
		preparedResponse,
		draft,
		true,
	)
	if !ok || len(issues) != 0 {
		t.Fatalf(
			"prepare trusted durable import ok=%v issues=%v response=%s",
			ok,
			issues,
			preparedResponse.Body.String(),
		)
	}
	if got := prepared.Config.JavaVuser.AdditionalVMParameters; len(got) != 1 ||
		got[0] != "-Ddatabase.password="+canary {
		t.Fatalf("trusted replay preparation changed imported Java argv: %#v", got)
	}

	payload, err := json.Marshal(ReplayScriptRequest{
		ProjectID: draft.ProjectID,
		Script:    draft,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/replay",
		bytes.NewReader(payload),
	)
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("forged transient replay status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), canary) {
		t.Fatalf("forged transient replay rejection disclosed password: %s", response.Body.String())
	}
	if !strings.Contains(
		response.Body.String(),
		"config.java_vuser.additional_vm_parameters",
	) {
		t.Fatalf("forged transient replay rejection omitted JVM argv issue: %s", response.Body.String())
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
