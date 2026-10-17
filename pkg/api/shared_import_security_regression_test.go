package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestSharedImportResponsePreservesCredentialDraftAndCreatesExactly(t *testing.T) {
	const credentialValue = "shared-import-exact-password-8f2a"
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("shared-opaque-import", credentialValue)
	response := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{Script: script},
	)
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(credentialValue)) {
		t.Fatalf("import response did not preserve exact credential: %s", encoded)
	}
	masked, ok := response["script"].(map[string]any)
	if !ok {
		t.Fatalf("response script = %T", response["script"])
	}
	payload, err := json.Marshal(masked)
	if err != nil {
		t.Fatal(err)
	}
	var submitted dsl.Script
	if err := json.Unmarshal(payload, &submitted); err != nil {
		t.Fatal(err)
	}
	if importDraftAttestationToken(&submitted) == "" {
		t.Fatalf("exact draft omitted save capability: %s", payload)
	}
	created := createAttestedImportScript(t, server, &submitted)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	durable, ok := server.runner.GetScript(submitted.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("opaque draft was not persisted")
	}
	if got := durable.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+credentialValue {
		t.Fatalf("durable exact credential = %q", got)
	}
}

func TestSharedImportResponseSentinelCannotEnterMigrationOrExecution(t *testing.T) {
	server := NewServer(ServerConfig{})
	script := attestedImportTestScript("shared-sentinel-import", "[rEdAcTeD]")
	response := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil),
		scriptImportResponseOptions{Script: script},
	)
	requirements, _ := response["secret_binding_requirements"].([]SecretBindingRequirement)
	if len(requirements) == 0 || requirements[0].DestinationValuePreserved ||
		requirements[0].PreservationUnavailableReason != "redacted_source_value" {
		t.Fatalf("requirements = %#v, want redacted source reason", requirements)
	}
	if response["source_secret_session_id"] != nil {
		t.Fatalf("redaction placeholder acquired migration session: %#v", response)
	}
	masked := response["script"].(map[string]any)
	payload, _ := json.Marshal(masked)
	var submitted dsl.Script
	if err := json.Unmarshal(payload, &submitted); err != nil {
		t.Fatal(err)
	}
	claim, claimErr := server.claimImportDraftAttestation(httptest.NewRequest(http.MethodPost, "/api/v1/scripts", nil), &submitted)
	if claimErr != nil || claim == nil || claim.Script == nil {
		t.Fatalf("claim unavailable placeholder draft: claim=%#v err=%v", claim, claimErr)
	}
	restored := claim.Script
	fingerprint, err := fingerprintImportDraftCredentials(restored)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint.FullDigest == "" {
		fingerprint.FullDigest, err = fullImportDraftExecutionDigest(restored)
		if err != nil {
			t.Fatal(err)
		}
	}
	expectedDigest, err := fullImportDraftExecutionDigest(script)
	if err != nil {
		t.Fatal(err)
	}
	if fingerprint.FullDigest != expectedDigest {
		t.Fatalf("unavailable-placeholder digest mismatch: got %s want %s restored=%#v prior=%#v", fingerprint.FullDigest, expectedDigest, restored.Actions[0].HeaderList, script.Actions[0].HeaderList)
	}

	server.releaseImportDraftAttestation(claim)
	created := createAttestedImportScript(t, server, &submitted)
	if created.Code != http.StatusCreated {
		t.Fatalf("create status = %d body=%s", created.Code, created.Body.String())
	}
	durable, ok := server.runner.GetScript(submitted.ID)
	if !ok || durable == nil || durable.Script == nil {
		t.Fatal("sentinel draft was not persisted")
	}
	got := durable.Script.Actions[0].HeaderList[0].Value
	if strings.Contains(strings.ToLower(got), "[redacted]") || !strings.Contains(got, "{{secret.") {
		t.Fatalf("durable unavailable credential = %q, want governed unconfigured reference", got)
	}
}

func TestLegacyImportCommitAppliesValidatedFolderOverride(t *testing.T) {
	server, _, _, projectID := newProjectSecretAPIServer(t)
	folder := &storage.Folder{ID: "legacy-import-folder", Name: "Imported", ProjectID: projectID, Category: "scripts", CreatedAt: time.Now().UTC()}
	if err := server.store.SaveFolder(folder); err != nil {
		t.Fatal(err)
	}
	script := attestedImportTestScript("legacy-folder-override", "folder-password")
	script.ProjectID = projectID
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", nil)
	response := server.newScriptImportResponseWithSourceSecrets(request, scriptImportResponseOptions{Script: script})
	masked := response["script"].(map[string]any)
	payload, _ := json.Marshal(masked)
	var submitted dsl.Script
	if err := json.Unmarshal(payload, &submitted); err != nil {
		t.Fatal(err)
	}
	folderID := folder.ID
	commitPayload, _ := json.Marshal(sharedImportCommitRequest{openAPIImportCommitRequest: openAPIImportCommitRequest{
		Script: submitted, ProjectID: projectID, FolderID: &folderID,
	}})
	commitRequest := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample/commit", bytes.NewReader(commitPayload))
	commitRequest.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	server.handleImportCommit(recorder, commitRequest, importCommitSpec{
		sourceProfile: "sample", auditProfile: "sample", displayName: "sample",
		allowedReplaceModes: []string{"full"}, actionKey: func(action dsl.Action) string { return action.Name },
		diff: func(existing, imported *dsl.Script, mode string, key func(dsl.Action) string) openAPIImportDiff {
			return openAPIImportDiff{}
		},
		merge:                 func(existing, imported *dsl.Script, key func(dsl.Action) string) *dsl.Script { return imported },
		mapCompatibleResponse: func(value map[string]any) map[string]any { return value },
	})
	if recorder.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", recorder.Code, recorder.Body.String())
	}
	stored, ok := server.runner.GetScript(submitted.ID)
	if !ok || stored == nil || stored.FolderID == nil || *stored.FolderID != folder.ID {
		t.Fatalf("stored folder = %#v, want %q", stored, folder.ID)
	}
}

func TestImportCandidateEnvelopeRejectsEncodedDisclosurePlaceholder(t *testing.T) {
	script := attestedImportTestScript("candidate-sentinel", "safe")
	requirement, _, err := credentialpolicy.NewRequirement(credentialpolicy.RequirementSpec{
		Key: "token", Label: "Token", Source: credentialpolicy.SourceRef{Format: "fixture"},
		Targets: []credentialpolicy.TargetRef{{Field: "header", RequestKey: "request"}},
		Value:   "%255Bredacted%255D", Mode: credentialpolicy.SecretReference,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope := importCandidateEnvelope{Version: importCandidateSessionVersion, Profile: "fixture", ProjectID: script.ProjectID, Script: *script}
	payload, err := marshalImportCandidateEnvelope(envelope, []credentialpolicy.Requirement{requirement})
	if err != nil {
		t.Fatal(err)
	}
	var stored importCandidateEnvelope
	if err := json.Unmarshal(payload, &stored); err != nil {
		t.Fatal(err)
	}
	if len(stored.CredentialRequirements) != 0 {
		t.Fatalf("candidate retained encoded placeholder: %#v", stored.CredentialRequirements)
	}
}
