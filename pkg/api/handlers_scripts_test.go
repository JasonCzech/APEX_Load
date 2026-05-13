package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

const sampleLoadRunnerActionC = `
#include "web_api.h"

Action()
{
	web_url("Homepage",
		"URL=https://example.com/app/home",
		"Resource=0",
		"RecContentType=text/html",
		LAST);

	web_set_max_html_param_len("4096");

	return 0;
}
`

func TestHandleCreateScriptRejectsMissingBaseURL(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"script": map[string]any{
			"name":     "No Base URL",
			"protocol": "http",
			"actions": []map[string]any{{
				"name":   "Ping",
				"type":   "request",
				"target": "/ping",
			}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "config.base_url is empty") {
		t.Fatalf("body = %q, want base_url validation error", resp.Body.String())
	}
}

func TestHandleUpdateScriptRejectsInvalidScript(t *testing.T) {
	server := NewServer(ServerConfig{})
	stored := server.runner.StoreScript(&dsl.Script{
		Name:     "Valid Script",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://api.example.com",
		},
		Actions: []dsl.Action{{
			Name:   "Ping",
			Type:   "request",
			Target: "/ping",
		}},
	})

	payload := map[string]any{
		"script": map[string]any{
			"name":     "Broken Script",
			"protocol": "http",
			"config": map[string]any{
				"base_url": "https://api.example.com",
			},
			"actions": []map[string]any{{
				"name":   "Ping",
				"target": "/ping",
			}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+stored.ID, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "missing type") {
		t.Fatalf("body = %q, want missing type validation error", resp.Body.String())
	}
}

func TestHandleImportSample(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name":   "Inventory Lookup",
		"format": "rest_json",
		"content": `{
			"requests": [
				{
					"request": {
						"method": "POST",
						"url": "https://api.example.com/v1/session",
						"headers": {"Content-Type": "application/json"},
						"body": {"username": "demo", "password": "secret"}
					},
					"response": {
						"status": 201,
						"body": {"sessionId": "session-12345"}
					}
				},
				{
					"request": {
						"method": "GET",
						"url": "https://api.example.com/v1/session/session-12345/items"
					},
					"response": {
						"status": 200,
						"body": {"items": []}
					}
				}
			]
		}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Scripts []json.RawMessage `json:"scripts"`
		Script  struct {
			Name    string `json:"name"`
			Actions []struct {
				Target string `json:"target"`
			} `json:"actions"`
		} `json:"script"`
		Warnings     []string       `json:"warnings"`
		Assumptions  []string       `json:"assumptions"`
		Unsupported  []string       `json:"unsupported_elements"`
		Summary      map[string]any `json:"summary"`
		Correlations []struct {
			Name string `json:"name"`
		} `json:"correlations"`
		Stats struct {
			ConvertedActions int `json:"converted_actions"`
			Assertions       int `json:"assertions"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if result.Script.Name != "Inventory Lookup" {
		t.Fatalf("script name = %q, want Inventory Lookup", result.Script.Name)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts count = %d, want 1", len(result.Scripts))
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("action count = %d, want 2", len(result.Script.Actions))
	}
	if result.Stats.ConvertedActions != 2 {
		t.Fatalf("converted_actions = %d, want 2", result.Stats.ConvertedActions)
	}
	if result.Stats.Assertions != 2 {
		t.Fatalf("assertions = %d, want 2", result.Stats.Assertions)
	}
	if len(result.Correlations) == 0 {
		t.Fatal("expected correlations in response")
	}
	if len(result.Assumptions) != 0 {
		t.Fatalf("assumptions = %v, want none", result.Assumptions)
	}
	if len(result.Unsupported) != 0 {
		t.Fatalf("unsupported_elements = %v, want none", result.Unsupported)
	}
	if len(result.Summary) != 0 {
		t.Fatalf("summary = %#v, want empty", result.Summary)
	}
	if result.Script.Actions[1].Target != "/v1/session/{{var.corr_sessionid}}/items" {
		t.Fatalf("second target = %q, want correlated target", result.Script.Actions[1].Target)
	}
}

func TestHandleImportSampleBadRequest(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/sample", bytes.NewBufferString(`{"format":"curl","content":""}`))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusBadRequest, resp.Body.String())
	}
}

func TestHandleImportLoadRunnerReturnsStandardizedEnvelope(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	addFile := func(name string, content string) {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	addFile("Demo.usr", `[General]
Type=Web
RuntimeSettings=default.cfg

[Parameters]
Parameter1=LoginUsers.prm

[Actions]
Action1=Action.c
`)
	addFile("Action.c", sampleLoadRunnerActionC)
	addFile("default.cfg", `[HTTP]
ConnectTimeout=45
`)
	addFile("LoginUsers.prm", `[LoginUser]
Type=File
Table=Users.dat
ColumnName=username
SelectNextRow=Sequential
UpdateValueOn=Each Iteration
`)
	addFile("Users.dat", "username\nalice\nbob\n")
	if err := writer.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", "application/zip")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Scripts []json.RawMessage `json:"scripts"`
		Script  struct {
			Protocol string `json:"protocol"`
			Name     string `json:"name"`
		} `json:"script"`
		Warnings            []string       `json:"warnings"`
		UnsupportedElements []string       `json:"unsupported_elements"`
		Assumptions         []string       `json:"assumptions"`
		Summary             map[string]any `json:"summary"`
		Stats               struct {
			ConvertedActions int `json:"converted_actions"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts count = %d, want 1", len(result.Scripts))
	}
	if result.Script.Protocol != "http" || result.Script.Name == "" {
		t.Fatalf("script = %#v, want http script with name", result.Script)
	}
	if result.Stats.ConvertedActions == 0 {
		t.Fatalf("converted_actions = %d, want > 0", result.Stats.ConvertedActions)
	}
	if len(result.UnsupportedElements) != 1 || result.UnsupportedElements[0] != "web_set_max_html_param_len" {
		t.Fatalf("unsupported_elements = %v, want [web_set_max_html_param_len]", result.UnsupportedElements)
	}
	if len(result.Assumptions) != 0 {
		t.Fatalf("assumptions = %v, want none", result.Assumptions)
	}
	if got := result.Summary["project_file"]; got != "Demo.usr" {
		t.Fatalf("summary.project_file = %#v, want Demo.usr", got)
	}
	if got := result.Summary["mapped_runtime_file"]; got != "default.cfg" {
		t.Fatalf("summary.mapped_runtime_file = %#v, want default.cfg", got)
	}
	parameterFiles, ok := result.Summary["parameter_files"].([]any)
	if !ok || len(parameterFiles) == 0 {
		t.Fatalf("summary.parameter_files = %#v, want non-empty slice", result.Summary["parameter_files"])
	}
	projectSections, ok := result.Summary["project_sections"].([]any)
	if !ok || len(projectSections) == 0 {
		t.Fatalf("summary.project_sections = %#v, want non-empty slice", result.Summary["project_sections"])
	}
	if got := result.Summary["unsupported_function_count"]; got != float64(1) {
		t.Fatalf("summary.unsupported_function_count = %#v, want 1", got)
	}
}

func TestHandleValidateLoadRunnerImport(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "demo.zip")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}

	zipWriter := zip.NewWriter(part)
	addFile := func(name string, content string) {
		entry, err := zipWriter.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	addFile("Demo.usr", `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action1=Action.c
`)
	addFile("Action.c", sampleLoadRunnerActionC)
	addFile("default.cfg", `[HTTP]
ConnectTimeout=45
`)
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	writer.WriteField("preserve_transactions", "false")
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner/validate", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Scripts []json.RawMessage `json:"scripts"`
		Script  struct {
			Actions []struct {
				Name string `json:"name"`
			} `json:"actions"`
		} `json:"script"`
		Stats struct {
			ConvertedActions int `json:"converted_actions"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts count = %d, want 1", len(result.Scripts))
	}
	if len(result.Script.Actions) == 0 {
		t.Fatal("expected at least one converted action")
	}
	if got := result.Script.Actions[0].Name; got != "Homepage" {
		t.Fatalf("first action name = %q, want Homepage when preserve_transactions=false", got)
	}
	if result.Stats.ConvertedActions == 0 {
		t.Fatal("expected converted actions in validation response")
	}
}

func TestHandleValidateLoadRunnerImportMatchesImportEnvelope(t *testing.T) {
	server := NewServer(ServerConfig{})
	archive := buildLoadRunnerImportArchive(t, true)

	importReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner", bytes.NewReader(archive))
	importReq.Header.Set("Content-Type", "application/zip")
	importResp := httptest.NewRecorder()
	server.mux.ServeHTTP(importResp, importReq)
	if importResp.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d body=%s", importResp.Code, http.StatusOK, importResp.Body.String())
	}

	validateReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner/validate", bytes.NewReader(archive))
	validateReq.Header.Set("Content-Type", "application/zip")
	validateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(validateResp, validateReq)
	if validateResp.Code != http.StatusOK {
		t.Fatalf("validate status = %d, want %d body=%s", validateResp.Code, http.StatusOK, validateResp.Body.String())
	}

	importEnvelope := decodeImportEnvelope(t, importResp.Body.Bytes())
	validateEnvelope := decodeImportEnvelope(t, validateResp.Body.Bytes())
	assertSharedImportEnvelopeKeys(t, importEnvelope)
	assertSharedImportEnvelopeKeys(t, validateEnvelope)
	if !reflect.DeepEqual(importEnvelope, validateEnvelope) {
		t.Fatalf("expected identical loadrunner import envelopes\nimport=%s\nvalidate=%s", importResp.Body.String(), validateResp.Body.String())
	}
}

func TestHandleImportJMeter(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "checkout.jmx")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	part.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan"><stringProp name="TestPlan.comments"/></TestPlan>
    <hashTree>
      <ThreadGroup testname="Users">
        <stringProp name="ThreadGroup.num_threads">5</stringProp>
        <stringProp name="ThreadGroup.ramp_time">10</stringProp>
      </ThreadGroup>
      <hashTree>
        <ConfigTestElement testname="Defaults" guiclass="HttpDefaultsGui">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
        </ConfigTestElement>
        <hashTree/>
        <HTTPSamplerProxy testname="Home">
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`))
	writer.WriteField("include_assertions", "true")
	writer.WriteField("scenario_draft", "true")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Scripts []struct {
			Name    string `json:"name"`
			Actions []struct {
				Target string `json:"target"`
			} `json:"actions"`
		} `json:"scripts"`
		ScenarioDraft any      `json:"scenario_draft"`
		Correlations  []any    `json:"correlations"`
		Assumptions   []string `json:"assumptions"`
		Unsupported   []string `json:"unsupported_elements"`
		Stats         struct {
			ConvertedActions int `json:"converted_actions"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 1 || result.Scripts[0].Actions[0].Target != "/home" {
		t.Fatalf("unexpected converted scripts: %#v", result.Scripts)
	}
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("converted actions = %d, want 1", result.Stats.ConvertedActions)
	}
	if result.ScenarioDraft == nil {
		t.Fatal("expected scenario draft in response")
	}
	if len(result.Correlations) != 0 {
		t.Fatalf("correlations = %v, want none", result.Correlations)
	}
	if len(result.Assumptions) == 0 {
		t.Fatal("expected assumptions in response")
	}
	if len(result.Unsupported) != 0 {
		t.Fatalf("unsupported_elements = %v, want none", result.Unsupported)
	}
}

func TestHandleValidateJMeterImport(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "validate.jmx")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	part.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan" />
    <hashTree>
      <ThreadGroup testname="Users">
        <stringProp name="ThreadGroup.num_threads">2</stringProp>
      </ThreadGroup>
      <hashTree>
        <HTTPSamplerProxy testname="Ping">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/ping</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`))
	writer.WriteField("scenario_draft", "true")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/validate", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Scripts       []StoredScript `json:"scripts"`
		ScenarioDraft any            `json:"scenario_draft"`
		Warnings      []string       `json:"warnings"`
		Correlations  []any          `json:"correlations"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 1 {
		t.Fatalf("scripts = %d, want 1", len(result.Scripts))
	}
	if result.ScenarioDraft == nil {
		t.Fatal("expected scenario draft in validation response")
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %v, want none", result.Warnings)
	}
	if len(result.Correlations) != 0 {
		t.Fatalf("correlations = %v, want none", result.Correlations)
	}
	if got := server.runner.ListScripts(); len(got) != 0 {
		t.Fatalf("validate should not persist scripts, got %d", len(got))
	}
}

func TestHandleValidateJMeterImportMatchesImportEnvelope(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, contentType := buildJMeterImportMultipartBody(t, true)

	importReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", bytes.NewReader(body))
	importReq.Header.Set("Content-Type", contentType)
	importResp := httptest.NewRecorder()
	server.mux.ServeHTTP(importResp, importReq)
	if importResp.Code != http.StatusOK {
		t.Fatalf("import status = %d, want %d body=%s", importResp.Code, http.StatusOK, importResp.Body.String())
	}

	validateReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/validate", bytes.NewReader(body))
	validateReq.Header.Set("Content-Type", contentType)
	validateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(validateResp, validateReq)
	if validateResp.Code != http.StatusOK {
		t.Fatalf("validate status = %d, want %d body=%s", validateResp.Code, http.StatusOK, validateResp.Body.String())
	}

	importEnvelope := decodeImportEnvelope(t, importResp.Body.Bytes())
	validateEnvelope := decodeImportEnvelope(t, validateResp.Body.Bytes())
	assertSharedImportEnvelopeKeys(t, importEnvelope)
	assertSharedImportEnvelopeKeys(t, validateEnvelope)
	if !reflect.DeepEqual(importEnvelope, validateEnvelope) {
		t.Fatalf("expected identical jmeter import envelopes\nimport=%s\nvalidate=%s", importResp.Body.String(), validateResp.Body.String())
	}
}

func TestHandleJMeterSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/jmeter/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix struct {
			Supported   []string `json:"supported"`
			Partial     []string `json:"partial"`
			Unsupported []string `json:"unsupported"`
		} `json:"support_matrix"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.SupportMatrix.Supported) == 0 {
		t.Fatal("expected supported entries in support matrix")
	}
	if len(result.SupportMatrix.Unsupported) == 0 {
		t.Fatal("expected unsupported entries in support matrix")
	}
}

func TestHandleSaveJMeterImportPersistsScriptsAndScenario(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"scripts": []map[string]any{{
			"name":     "Imported_JMeter",
			"version":  "1.0.0",
			"protocol": "http",
			"config":   map[string]any{"base_url": "https://api.example.com"},
			"actions": []map[string]any{{
				"name":   "Home",
				"type":   "GET",
				"method": "GET",
				"target": "/home",
			}},
		}},
		"scenario_draft": map[string]any{
			"name":          "Imported JMeter Scenario",
			"duration":      "2m",
			"scenario_mode": "vuser_driven",
			"groups": []map[string]any{{
				"name":           "Users",
				"script_name":    "Imported_JMeter",
				"vusers":         5,
				"ramp_up":        "10s",
				"initialization": "at_runtime",
			}},
		},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter/save", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusCreated, resp.Body.String())
	}
	var result struct {
		Count    int             `json:"count"`
		Scripts  []StoredScript  `json:"scripts"`
		Scenario *StoredScenario `json:"scenario"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Count != 1 || len(result.Scripts) != 1 || result.Scripts[0].ID == "" {
		t.Fatalf("unexpected scripts response: %#v", result)
	}
	if result.Scenario == nil || result.Scenario.ScriptCount != 1 {
		t.Fatalf("expected saved scenario, got %#v", result.Scenario)
	}
}

func buildLoadRunnerImportArchive(t *testing.T, includeParameterFiles bool) []byte {
	t.Helper()

	var body bytes.Buffer
	writer := zip.NewWriter(&body)
	addFile := func(name string, content string) {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	userFile := `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action1=Action.c
`
	if includeParameterFiles {
		userFile = `[General]
Type=Web
RuntimeSettings=default.cfg

[Parameters]
Parameter1=LoginUsers.prm

[Actions]
Action1=Action.c
`
	}
	addFile("Demo.usr", userFile)
	addFile("Action.c", sampleLoadRunnerActionC)
	addFile("default.cfg", `[HTTP]
ConnectTimeout=45
`)
	if includeParameterFiles {
		addFile("LoginUsers.prm", `[LoginUser]
Type=File
Table=Users.dat
ColumnName=username
SelectNextRow=Sequential
UpdateValueOn=Each Iteration
`)
		addFile("Users.dat", "username\nalice\nbob\n")
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	return body.Bytes()
}

func buildJMeterImportMultipartBody(t *testing.T, scenarioDraft bool) ([]byte, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "checkout.jmx")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	if _, err := part.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan"><stringProp name="TestPlan.comments"/></TestPlan>
    <hashTree>
      <ThreadGroup testname="Users">
        <stringProp name="ThreadGroup.num_threads">5</stringProp>
        <stringProp name="ThreadGroup.ramp_time">10</stringProp>
      </ThreadGroup>
      <hashTree>
        <ConfigTestElement testname="Defaults" guiclass="HttpDefaultsGui">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
        </ConfigTestElement>
        <hashTree/>
        <HTTPSamplerProxy testname="Home">
          <stringProp name="HTTPSampler.path">/home</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`)); err != nil {
		t.Fatalf("Write returned error: %v", err)
	}
	if err := writer.WriteField("include_assertions", "true"); err != nil {
		t.Fatalf("WriteField(include_assertions) returned error: %v", err)
	}
	if scenarioDraft {
		if err := writer.WriteField("scenario_draft", "true"); err != nil {
			t.Fatalf("WriteField(scenario_draft) returned error: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart.Close returned error: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
}

func decodeImportEnvelope(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(body, &envelope); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	return envelope
}

func assertSharedImportEnvelopeKeys(t *testing.T, envelope map[string]any) {
	t.Helper()
	for _, key := range []string{"scripts", "script", "warnings", "stats", "correlations", "assumptions", "unsupported_elements", "scenario_draft", "summary", "result"} {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("envelope missing key %q: %#v", key, envelope)
		}
	}
}
