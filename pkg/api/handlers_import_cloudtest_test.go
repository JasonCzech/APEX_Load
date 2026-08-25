package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleImportCloudTestReturnsStandardReviewEnvelope(t *testing.T) {
	server := NewServer(ServerConfig{})
	requestBody, err := json.Marshal(map[string]any{
		"project_id": "project-cloudtest",
		"filename":   "repository.xml",
		"content": `<Repository>
  <Clip name="Checkout"><HTTP authorization="Bearer must-never-escape"/></Clip>
  <Composition name="Sale"><Track name="East" clip="Checkout" offset="60000" users="10"/></Composition>
</Repository>`,
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/cloudtest", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "must-never-escape") {
		t.Fatal("unapproved source attribute leaked into import response")
	}
	var envelope map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"scripts", "script", "warnings", "stats", "unsupported_elements", "review_sections", "summary", "preflight", "accounting"} {
		if _, exists := envelope[key]; !exists {
			t.Fatalf("standard import envelope is missing %q: %#v", key, envelope)
		}
	}
	scripts, ok := envelope["scripts"].([]any)
	if !ok || len(scripts) != 1 {
		t.Fatalf("scripts = %#v", envelope["scripts"])
	}
	script, _ := scripts[0].(map[string]any)
	if script["project_id"] != "project-cloudtest" || script["generated_from"] != "cloudtest" {
		t.Fatalf("script provenance = %#v", script)
	}
	actions, _ := script["actions"].([]any)
	if len(actions) == 0 {
		t.Fatalf("review actions = %#v", actions)
	}
	metadata, _ := actions[0].(map[string]any)["metadata"].(map[string]any)
	if metadata["execution_blocker"] != "cloudtest_fixture_mapping_unproven" {
		t.Fatalf("action metadata = %#v", metadata)
	}
	result, _ := envelope["result"].(map[string]any)
	dispositions, _ := result["dispositions"].([]any)
	if len(dispositions) != 5 {
		t.Fatalf("dispositions = %#v", dispositions)
	}
	for _, raw := range dispositions {
		disposition, _ := raw.(map[string]any)
		if disposition["disposition"] != "blocked_draft" || disposition["fixture_proven"] != false {
			t.Fatalf("unsafe source disposition = %#v", disposition)
		}
	}
}

func TestHandleCloudTestSupportMatrixPublishesFixtureGate(t *testing.T) {
	server := NewServer(ServerConfig{})
	response := httptest.NewRecorder()
	server.mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/cloudtest/support-matrix", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", response.Code, response.Body.String())
	}
	var payload struct {
		FixtureGate   string `json:"fixture_gate"`
		SupportMatrix []struct {
			ID            string `json:"id"`
			FixtureProven bool   `json:"fixture_proven"`
		} `json:"support_matrix"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload.FixtureGate == "" || len(payload.SupportMatrix) == 0 {
		t.Fatalf("support payload = %#v", payload)
	}
	for _, entry := range payload.SupportMatrix {
		if entry.ID == "" || entry.FixtureProven {
			t.Fatalf("support entry = %#v", entry)
		}
	}
}

func TestCloudTestUsesGenericBatchCommitProfile(t *testing.T) {
	if got := normalizeGenericImportSourceProfile("cloudtest"); got != "cloudtest" {
		t.Fatalf("normalize cloudtest = %q", got)
	}
}

func TestCloudTestGenericBatchCommitRetainsEncryptedSource(t *testing.T) {
	server, store, _ := newImportSourceLifecycleServer(t)
	source := `<Repository><Clip name="Retained"><HTTP/></Clip></Repository>`
	previewRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/cloudtest?filename=repository.xml&project_id="+storage.DefaultProjectID,
		strings.NewReader(source),
	)
	preview := httptest.NewRecorder()
	server.mux.ServeHTTP(preview, previewRequest)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", preview.Code, preview.Body.String())
	}
	var inspection struct {
		Scripts          []dsl.Script `json:"scripts"`
		ImportSessionIDs []string     `json:"import_session_ids"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if len(inspection.Scripts) != 1 || len(inspection.ImportSessionIDs) != 1 || inspection.ImportSessionIDs[0] == "" {
		t.Fatalf("protected inspection = %#v body=%s", inspection, preview.Body.String())
	}
	commit := postGenericImportBatch(t, server, map[string]any{
		"operation_id":       "cloudtest-source-retention",
		"source_profile":     "cloudtest",
		"scripts":            inspection.Scripts,
		"import_session_ids": inspection.ImportSessionIDs,
	})
	if commit.Code != http.StatusCreated {
		t.Fatalf("commit status = %d body=%s", commit.Code, commit.Body.String())
	}
	var committed struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(commit.Body.Bytes(), &committed); err != nil {
		t.Fatal(err)
	}
	if len(committed.Scripts) != 1 {
		t.Fatalf("commit response = %#v", committed)
	}
	revision, err := store.GetActiveImportSourceRevision(committed.Scripts[0].ID, storage.DefaultProjectID)
	if err != nil {
		t.Fatalf("GetActiveImportSourceRevision: %v", err)
	}
	if revision == nil || revision.SourceFormat != "cloudtest" || revision.RawArtifactID == "" || revision.SanitizedAvailable {
		t.Fatalf("retained CloudTest source = %#v", revision)
	}
	artifact, err := store.GetImportSourceArtifact(revision.RawArtifactID, storage.DefaultProjectID)
	if err != nil {
		t.Fatalf("GetImportSourceArtifact: %v", err)
	}
	if artifact.SizeBytes != int64(len(source)) || artifact.EncryptionMetadata == nil {
		t.Fatalf("retained CloudTest artifact = %#v", artifact)
	}
}

func TestCloudTestGenericBatchCommitRejectsUnattestedExecutableReplacement(t *testing.T) {
	server := NewServer(ServerConfig{})
	previewRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/cloudtest?filename=repository.xml&project_id="+storage.DefaultProjectID,
		strings.NewReader(`<Repository><Clip name="One"><HTTP/></Clip><Clip name="Two"><HTTP/></Clip></Repository>`),
	)
	preview := httptest.NewRecorder()
	server.mux.ServeHTTP(preview, previewRequest)
	if preview.Code != http.StatusOK {
		t.Fatalf("preview status = %d body=%s", preview.Code, preview.Body.String())
	}
	var inspection struct {
		Scripts []dsl.Script `json:"scripts"`
	}
	if err := json.Unmarshal(preview.Body.Bytes(), &inspection); err != nil {
		t.Fatal(err)
	}
	if len(inspection.Scripts) != 2 {
		t.Fatalf("scripts = %d, want 2", len(inspection.Scripts))
	}
	inspection.Scripts[1].Actions = []dsl.Action{{
		Name: "forged executable request", Type: "request", Method: http.MethodGet, Target: "https://example.test",
	}}
	commit := postGenericImportBatch(t, server, map[string]any{
		"operation_id":   "cloudtest-unattested-replacement",
		"source_profile": "cloudtest",
		"scripts":        inspection.Scripts,
	})
	if commit.Code != http.StatusConflict || !strings.Contains(commit.Body.String(), "must retain") {
		t.Fatalf("commit status = %d body=%s, want 409 fixture blocker", commit.Code, commit.Body.String())
	}
}
