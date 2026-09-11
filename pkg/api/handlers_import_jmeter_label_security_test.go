package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/jmeter"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type jmeterCredentialLabelPreview struct {
	Scripts       []dsl.Script          `json:"scripts"`
	ScenarioDraft *jmeter.ScenarioDraft `json:"scenario_draft"`
	Result        struct {
		Scripts       []dsl.Script          `json:"scripts"`
		ScenarioDraft *jmeter.ScenarioDraft `json:"scenario_draft"`
	} `json:"result"`
}

func inspectJMeterCredentialLabelPreview(
	t *testing.T,
	server *Server,
	password string,
) jmeterCredentialLabelPreview {
	t.Helper()
	jmx := fmt.Sprintf(`<jmeterTestPlan version="1.2"><hashTree>
  <TestPlan testname="Plan"/><hashTree>
    <ThreadGroup testname="Users %s">
      <stringProp name="ThreadGroup.num_threads">2</stringProp>
    </ThreadGroup><hashTree>
      <AuthManager testname="Auth"><collectionProp name="AuthManager.auth_list">
        <elementProp name="" elementType="Authorization">
          <stringProp name="Authorization.username">operator</stringProp>
          <stringProp name="Authorization.password">%s</stringProp>
          <stringProp name="Authorization.mechanism">BASIC</stringProp>
        </elementProp>
      </collectionProp></AuthManager><hashTree/>
      <HTTPSamplerProxy testname="Request %s">
        <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
        <stringProp name="HTTPSampler.protocol">https</stringProp>
        <stringProp name="HTTPSampler.path">/home</stringProp>
        <stringProp name="HTTPSampler.method">GET</stringProp>
      </HTTPSamplerProxy><hashTree/>
    </hashTree>
  </hashTree>
</hashTree></jmeterTestPlan>`, password, password, password)

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/jmeter?filename=labels.jmx&scenario_draft=true",
		strings.NewReader(jmx),
	)
	req.Header.Set("Content-Type", "application/xml")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), password) {
		t.Fatalf("JMeter preview did not preserve credential-bearing source labels or values: %s", resp.Body.String())
	}

	var result jmeterCredentialLabelPreview
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	return result
}

func TestHandleImportJMeterPreservesCredentialBearingResponseLabels(t *testing.T) {
	const password = "jmeter-response-label-canary-14c8"
	server := NewServer(ServerConfig{})
	result := inspectJMeterCredentialLabelPreview(t, server, password)
	assertJMeterResponseLabelsPreserved(t, result.Scripts, result.ScenarioDraft, password)
	assertJMeterResponseLabelsPreserved(t, result.Result.Scripts, result.Result.ScenarioDraft, password)
	if result.Scripts[0].Config.HTTP.Auth.Password != password ||
		importDraftAttestationToken(&result.Scripts[0]) == "" {
		t.Fatalf("editable import draft was not preserved and attested: %#v", result.Scripts[0])
	}
}

func TestHandleSaveJMeterImportPreservesDurableScenarioLabels(t *testing.T) {
	const password = "jmeter-durable-label-canary-98b6"
	server := NewServer(ServerConfig{})
	preview := inspectJMeterCredentialLabelPreview(t, server, password)
	payload := map[string]any{
		"scripts":        preview.Scripts,
		"scenario_draft": preview.ScenarioDraft,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/v1/scripts/import/jmeter/save",
		bytes.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}

	var response struct {
		Scripts  []StoredScript  `json:"scripts"`
		Scenario *StoredScenario `json:"scenario"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &response); err != nil {
		t.Fatalf("json.Unmarshal() error = %v", err)
	}
	if len(response.Scripts) != 1 || response.Scenario == nil ||
		len(response.Scenario.Scripts) != 1 {
		t.Fatalf("unexpected save response: %#v", response)
	}
	normalizedPassword := strings.ReplaceAll(password, "-", "_")
	if !strings.Contains(response.Scripts[0].Name, normalizedPassword) ||
		!strings.Contains(response.Scenario.Scripts[0].ScriptName, normalizedPassword) {
		t.Fatalf("save response did not preserve password-bearing labels: %s", resp.Body.String())
	}

	stored, ok := server.runner.GetScript(response.Scripts[0].ID)
	if !ok || stored == nil || stored.Script == nil {
		t.Fatal("saved JMeter script not found")
	}
	if stored.Script.Config.HTTP.Auth.Password != password {
		t.Fatalf("durable auth password = %#v, want exact imported value", stored.Script.Config.HTTP.Auth)
	}
	if !strings.Contains(stored.Name, normalizedPassword) ||
		!strings.Contains(stored.Script.Name, normalizedPassword) ||
		!strings.Contains(stored.Script.Actions[0].Name, normalizedPassword) {
		t.Fatalf("durable script labels did not retain source text: %#v", stored)
	}
	scenario, ok := server.runner.GetScenario(response.Scenario.ID)
	if !ok || scenario == nil || len(scenario.Scripts) != 1 {
		t.Fatal("saved JMeter scenario not found")
	}
	if !strings.Contains(scenario.Scripts[0].ScriptName, normalizedPassword) {
		t.Fatalf("durable scenario labels did not retain source text: %#v", scenario)
	}
}

func assertJMeterResponseLabelsPreserved(
	t *testing.T,
	scripts []dsl.Script,
	draft *jmeter.ScenarioDraft,
	password string,
) {
	t.Helper()
	normalizedPassword := strings.ReplaceAll(password, "-", "_")
	if len(scripts) != 1 || draft == nil || len(draft.Groups) != 1 {
		t.Fatalf("unexpected projected response scripts/draft: %#v %#v", scripts, draft)
	}
	for _, label := range []string{
		scripts[0].Name,
		scripts[0].Actions[0].Name,
		draft.Groups[0].ScriptName,
	} {
		if !strings.Contains(label, normalizedPassword) {
			t.Fatalf("JMeter label did not preserve source text: %q", label)
		}
	}
	if draft.Groups[0].ScriptName != scripts[0].Name {
		t.Fatalf("scenario script_name = %q, want %q", draft.Groups[0].ScriptName, scripts[0].Name)
	}
}
