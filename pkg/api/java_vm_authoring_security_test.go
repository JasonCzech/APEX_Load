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
