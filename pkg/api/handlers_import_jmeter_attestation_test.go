package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestJMeterAttestedSaveRestoresCredentialUsesSelectedFolderAndReplays(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	folder := &storage.Folder{
		ID: "jmeter-selected-folder", Name: "Selected", ProjectID: storage.DefaultProjectID,
		Category: "scripts", CreatedAt: time.Now().UTC(),
	}
	if err := store.SaveFolder(folder); err != nil {
		t.Fatal(err)
	}
	const credentialValue = "jmeter-attested-exact-token-8bf3"
	source := &dsl.Script{
		Name: "Attested_JMeter", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config: dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source: "jmeter",
			Preserved: map[string]any{
				jmeterDirectImportOperationKey: "jmeter-attested-replay-operation",
			},
		}},
		Actions: []dsl.Action{{
			Name: "Request", Type: "request", Method: http.MethodGet, Target: "/orders",
			HeaderList: dsl.HeaderList{{Name: "Authorization", Value: "Bearer " + credentialValue}},
		}},
	}
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", nil),
		scriptImportResponseOptions{Script: source, Scripts: []*dsl.Script{source}},
	)
	previewJSON, err := json.Marshal(preview)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(previewJSON, []byte(credentialValue)) {
		t.Fatal("JMeter preview did not preserve the exact credential")
	}
	maskedScripts, ok := preview["scripts"].([]any)
	if !ok || len(maskedScripts) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	maskedPayload, _ := json.Marshal(maskedScripts[0])
	var submitted dsl.Script
	if err := json.Unmarshal(maskedPayload, &submitted); err != nil {
		t.Fatal(err)
	}
	if importDraftAttestationToken(&submitted) == "" ||
		!strings.Contains(submitted.Actions[0].HeaderList[0].Value, credentialValue) {
		t.Fatalf("JMeter preview omitted exact attested draft: %#v", submitted)
	}
	submitted.FolderID = &folder.ID
	requestBody, err := json.Marshal(map[string]any{"scripts": []dsl.Script{submitted}})
	if err != nil {
		t.Fatal(err)
	}

	first := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", requestBody, "")
	if first.Code != http.StatusCreated {
		t.Fatalf("first save status = %d body=%s", first.Code, first.Body.String())
	}
	second := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", requestBody, "")
	if second.Code != http.StatusCreated {
		t.Fatalf("replay status = %d body=%s", second.Code, second.Body.String())
	}
	var firstResult, secondResult struct {
		Scripts []StoredScript `json:"scripts"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &firstResult); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(second.Body.Bytes(), &secondResult); err != nil {
		t.Fatal(err)
	}
	if len(firstResult.Scripts) != 1 || len(secondResult.Scripts) != 1 ||
		firstResult.Scripts[0].ID != secondResult.Scripts[0].ID {
		t.Fatalf("receipt replay changed assets: first=%#v second=%#v", firstResult, secondResult)
	}
	stored, ok := server.runner.GetScript(firstResult.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("stored attested JMeter script not found")
	}
	if got := stored.Script.Actions[0].HeaderList[0].Value; got != "Bearer "+credentialValue {
		t.Fatalf("stored credential = %q", got)
	}
	if stored.FolderID == nil || *stored.FolderID != folder.ID {
		t.Fatalf("stored folder = %#v, want %q", stored.FolderID, folder.ID)
	}
}

func TestJMeterScenarioScriptNameMatchingIsCaseInsensitive(t *testing.T) {
	server, store := newAuditTestServer(t)
	defer store.Close()
	source := &dsl.Script{
		Name: "Checkout Users", Version: "1.0.0", Protocol: "http", ProjectID: storage.DefaultProjectID,
		Config:   dsl.ScriptConfig{BaseURL: "https://api.example.test"},
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Source: "jmeter", Preserved: map[string]any{}}},
		Actions:  []dsl.Action{{Name: "Request", Type: "request", Method: http.MethodGet, Target: "/orders"}},
	}
	preview := server.newScriptImportResponseWithSourceSecrets(
		httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", nil),
		scriptImportResponseOptions{Script: source, Scripts: []*dsl.Script{source}},
	)
	maskedScripts, ok := preview["scripts"].([]any)
	if !ok || len(maskedScripts) != 1 {
		t.Fatalf("preview scripts = %#v", preview["scripts"])
	}
	requestBody, err := json.Marshal(map[string]any{
		"scripts": maskedScripts,
		"scenario_draft": map[string]any{
			"name": "Case-insensitive JMeter scenario",
			"groups": []any{map[string]any{
				"name": "", "script_name": "checkout users", "vusers": 1,
			}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	response := postJSON(t, server, "/api/v1/scripts/import/jmeter/save", requestBody, "")
	if response.Code != http.StatusCreated {
		t.Fatalf("save status = %d body=%s", response.Code, response.Body.String())
	}
	var result struct {
		Scripts  []StoredScript  `json:"scripts"`
		Scenario *StoredScenario `json:"scenario"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Scenario == nil || len(result.Scenario.Scripts) != 1 || len(result.Scripts) != 1 {
		t.Fatalf("saved import = %#v", result)
	}
	if result.Scenario.Scripts[0].ScriptID != result.Scripts[0].ID || result.Scenario.Scripts[0].ScriptName != "Checkout Users" {
		t.Fatalf("scenario group = %#v, script = %#v", result.Scenario.Scripts[0], result.Scripts[0])
	}
}
