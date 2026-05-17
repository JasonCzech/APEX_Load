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
	"unicode/utf16"

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

func TestHandleCreateAndUpdateScriptPreservesExtendedRTSSettings(t *testing.T) {
	server := NewServer(ServerConfig{})
	cacheEnabled := true
	checkNewerVersions := true
	newUserPerIter := true
	projectID := "project-rts"
	folderID := "folder-rts"

	script := dsl.Script{
		Name:     "Extended RTS Browser Script",
		Version:  "1.0.0",
		Protocol: "browser",
		Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
			Source:           "loadrunner",
			SourceScriptName: "ExtendedRTS",
			Preserved: map[string]any{
				"runtime_cfg": "default.cfg",
			},
		}},
		Config: dsl.ScriptConfig{
			BaseURL: "https://example.com",
			Log: dsl.LogConfig{
				Level:           "debug",
				OnErrorOnly:     true,
				ExtendedOptions: []string{"parameter_substitution", "data_returned"},
			},
			HTTP: dsl.HTTPConfig{
				ProxyMode: "custom",
				Proxy: dsl.ProxyConfig{
					URL:      "http://proxy.example.com:8080",
					Username: "proxy-user",
					Password: "proxy-pass",
				},
			},
			Browser: dsl.BrowserConfig{
				Engine:             "chromium",
				Headless:           &cacheEnabled,
				CacheEnabled:       &cacheEnabled,
				CheckNewerVersions: &checkNewerVersions,
				NewUserPerIter:     &newUserPerIter,
				DownloadResources:  "all",
				Viewport: dsl.BrowserViewport{
					Width:  1440,
					Height: 900,
				},
			},
			WebRuntime: dsl.WebRuntimeConfig{
				AdditionalAttributes: map[string]string{
					"record_snapshot": "true",
				},
				DownloadFilters: dsl.DownloadFiltersConfig{
					Mode:  "allow_list",
					Hosts: []string{"cdn.example.com"},
				},
				ContentChecks: []dsl.ContentCheckConfig{{
					Text:        "Access Denied",
					Scope:       "main_document",
					FailIfFound: &cacheEnabled,
				}},
				NetworkSimulation: dsl.NetworkSimulationConfig{
					Mode:          "custom",
					BandwidthKbps: 2048,
					LatencyMs:     120,
				},
				Streaming: dsl.StreamingConfig{
					Mode:                "fixed",
					BufferSizeSeconds:   30,
					FixedBitrateKbps:    1200,
					BandwidthEstimation: "rolling",
				},
				DataFormatting: dsl.DataFormattingConfig{
					AutoDetect: true,
					Chain: []dsl.DFETransform{{
						Type: "gzip",
						Options: map[string]string{
							"mode": "decode",
						},
					}},
				},
			},
		},
		Actions: []dsl.Action{{
			Name:   "Open homepage",
			Type:   "browser_navigate",
			Target: "/",
		}},
	}

	createBody, err := json.Marshal(map[string]any{
		"project_id": projectID,
		"folder_id":  folderID,
		"script":     script,
	})
	if err != nil {
		t.Fatalf("json.Marshal(create) returned error: %v", err)
	}

	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts", bytes.NewReader(createBody))
	createReq.Header.Set("Content-Type", "application/json")
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)

	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}

	var created StoredScript
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("json.Unmarshal(create) returned error: %v", err)
	}
	if created.ProjectID != projectID {
		t.Fatalf("created project_id = %q, want %q", created.ProjectID, projectID)
	}
	if created.FolderID == nil || *created.FolderID != folderID {
		t.Fatalf("created folder_id = %v, want %q", created.FolderID, folderID)
	}
	if created.Script == nil {
		t.Fatal("created script = nil")
	}
	if !reflect.DeepEqual(created.Script.Config.WebRuntime, script.Config.WebRuntime) {
		t.Fatalf("created web_runtime = %#v, want %#v", created.Script.Config.WebRuntime, script.Config.WebRuntime)
	}
	if !reflect.DeepEqual(created.Script.Config.Browser, script.Config.Browser) {
		t.Fatalf("created browser config = %#v, want %#v", created.Script.Config.Browser, script.Config.Browser)
	}
	if !reflect.DeepEqual(created.Script.Metadata, script.Metadata) {
		t.Fatalf("created metadata = %#v, want %#v", created.Script.Metadata, script.Metadata)
	}

	validateReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/"+created.ID+"/validate", nil)
	validateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(validateResp, validateReq)

	if validateResp.Code != http.StatusOK {
		t.Fatalf("validate status = %d, want %d body=%s", validateResp.Code, http.StatusOK, validateResp.Body.String())
	}

	var validatePayload struct {
		Valid  bool     `json:"valid"`
		Issues []string `json:"issues"`
	}
	if err := json.Unmarshal(validateResp.Body.Bytes(), &validatePayload); err != nil {
		t.Fatalf("json.Unmarshal(validate) returned error: %v", err)
	}
	if !validatePayload.Valid || len(validatePayload.Issues) != 0 {
		t.Fatalf("validate payload = %#v, want valid script with no issues", validatePayload)
	}

	updatedScript := *created.Script
	updatedScript.Config.Browser.DownloadResources = "none"
	updatedScript.Config.WebRuntime.NetworkSimulation = dsl.NetworkSimulationConfig{
		Mode:      "preset",
		Preset:    "dsl",
		LatencyMs: 50,
	}
	updatedScript.Config.WebRuntime.DownloadFilters = dsl.DownloadFiltersConfig{
		Mode:  "block_list",
		Hosts: []string{"ads.example.com", "tracker.example.com"},
	}
	updatedScript.Config.WebRuntime.ContentChecks = []dsl.ContentCheckConfig{{
		Text:  "Service Unavailable",
		Scope: "any_response",
	}}

	updateBody, err := json.Marshal(map[string]any{"script": updatedScript})
	if err != nil {
		t.Fatalf("json.Marshal(update) returned error: %v", err)
	}

	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/"+created.ID, bytes.NewReader(updateBody))
	updateReq.Header.Set("Content-Type", "application/json")
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)

	if updateResp.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}

	var updated StoredScript
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("json.Unmarshal(update) returned error: %v", err)
	}
	if updated.Script == nil {
		t.Fatal("updated script = nil")
	}
	if updated.Script.Config.Browser.DownloadResources != "none" {
		t.Fatalf("updated download_resources = %q, want none", updated.Script.Config.Browser.DownloadResources)
	}
	if !reflect.DeepEqual(updated.Script.Config.WebRuntime.DownloadFilters, updatedScript.Config.WebRuntime.DownloadFilters) {
		t.Fatalf("updated download_filters = %#v, want %#v", updated.Script.Config.WebRuntime.DownloadFilters, updatedScript.Config.WebRuntime.DownloadFilters)
	}
	if !reflect.DeepEqual(updated.Script.Config.WebRuntime.NetworkSimulation, updatedScript.Config.WebRuntime.NetworkSimulation) {
		t.Fatalf("updated network_simulation = %#v, want %#v", updated.Script.Config.WebRuntime.NetworkSimulation, updatedScript.Config.WebRuntime.NetworkSimulation)
	}
	if !reflect.DeepEqual(updated.Script.Config.WebRuntime.ContentChecks, updatedScript.Config.WebRuntime.ContentChecks) {
		t.Fatalf("updated content_checks = %#v, want %#v", updated.Script.Config.WebRuntime.ContentChecks, updatedScript.Config.WebRuntime.ContentChecks)
	}
}

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
	writer := multipart.NewWriter(&body)
	var zipWriter *zip.Writer
	addFile := func(name string, content string) {
		entry, err := zipWriter.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	if err := writer.WriteField("migration_block_statuses", "partial,compatibility_only"); err != nil {
		t.Fatalf("WriteField returned error: %v", err)
	}
	part, err := writer.CreateFormFile("file", "demo.zip")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	zipWriter = zip.NewWriter(part)
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
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
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
		Warnings            []string `json:"warnings"`
		UnsupportedElements []string `json:"unsupported_elements"`
		Assumptions         []string `json:"assumptions"`
		ReviewSections      []struct {
			Key      string   `json:"key"`
			Title    string   `json:"title"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		RepairSuggestions []struct {
			Category string `json:"category"`
			Severity string `json:"severity"`
			Title    string `json:"title"`
		} `json:"repair_suggestions"`
		Summary map[string]any `json:"summary"`
		Stats   struct {
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
	assertReviewSection(t, result.ReviewSections, "unsupported_elements", "blocker", "web_set_max_html_param_len")
	assertReviewSection(t, result.ReviewSections, "preserved_metadata", "info", "Project file: Demo.usr")
	assertReviewSection(t, result.ReviewSections, "runtime_settings_parity", "info", "Executed: HTTP / Timeouts, redirects, and connection reuse [default.cfg]")
	assertReviewSection(t, result.ReviewSections, "runtime_settings_migration_readiness", "info", "Score: 100%")
	assertRepairSuggestion(t, result.RepairSuggestions, "unsupported", "blocker", "Replace unsupported web_set_max_html_param_len")
	assertRepairSuggestion(t, result.RepairSuggestions, "runtime", "info", "Review preserved runtime settings")
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
	runtimeParity, ok := result.Summary["runtime_settings_parity"].([]any)
	if !ok || len(runtimeParity) == 0 {
		t.Fatalf("summary.runtime_settings_parity = %#v, want non-empty slice", result.Summary["runtime_settings_parity"])
	}
	statusCounts, ok := result.Summary["runtime_settings_status_counts"].(map[string]any)
	if !ok || statusCounts["executed"] != float64(1) {
		t.Fatalf("summary.runtime_settings_status_counts = %#v, want executed=1", result.Summary["runtime_settings_status_counts"])
	}
	readiness, ok := result.Summary["migration_readiness"].(map[string]any)
	if !ok || readiness["score_pct"] != float64(100) || readiness["blocked"] != false {
		t.Fatalf("summary.migration_readiness = %#v, want score=100 blocked=false", result.Summary["migration_readiness"])
	}
}

func TestHandleImportLoadRunnerSummarizesRuntimeSettingParity(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	var zipWriter *zip.Writer
	addFile := func(name string, content string) {
		entry, err := zipWriter.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	if err := writer.WriteField("migration_block_statuses", "partial,compatibility_only"); err != nil {
		t.Fatalf("WriteField returned error: %v", err)
	}
	part, err := writer.CreateFormFile("file", "demo.zip")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	zipWriter = zip.NewWriter(part)
	addFile("Demo.usr", `[General]
Type=Web
RuntimeSettings=default.cfg

[Actions]
Action1=Action.c
`)
	addFile("Action.c", sampleLoadRunnerActionC)
	addFile("default.cfg", `[Run Logic]
Iterations=5
AutoTransactions=per_action

[Logging]
Level=extended
OnErrorOnly=1

[Streaming]
Mode=fixed
BufferSizeSeconds=30
`)
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Warnings       []string `json:"warnings"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Summary  string   `json:"summary"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		Summary map[string]any `json:"summary"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if len(result.Warnings) != 1 || !strings.Contains(strings.ToLower(result.Warnings[0]), "media playback semantics") {
		t.Fatalf("warnings = %#v, want streaming compatibility warning", result.Warnings)
	}
	assertReviewSection(t, result.ReviewSections, "warnings", "warning", streamingCompatibilityWarning())
	assertReviewSection(t, result.ReviewSections, "runtime_settings_parity", "warning", "Partial: Logging / Log level, error-only, and extended logging options [default.cfg]")
	assertReviewSection(t, result.ReviewSections, "runtime_settings_migration_readiness", "blocker", "Blocking setting: Logging / Log level, error-only, and extended logging options")
	statusCounts, ok := result.Summary["runtime_settings_status_counts"].(map[string]any)
	if !ok {
		t.Fatalf("summary.runtime_settings_status_counts = %#v, want map", result.Summary["runtime_settings_status_counts"])
	}
	if statusCounts["executed"] != float64(2) || statusCounts["partial"] != float64(1) || statusCounts["compatibility_only"] != float64(1) {
		t.Fatalf("summary.runtime_settings_status_counts = %#v, want executed=2 partial=1 compatibility_only=1", statusCounts)
	}
	runtimeParity, ok := result.Summary["runtime_settings_parity"].([]any)
	if !ok || len(runtimeParity) != 4 {
		t.Fatalf("summary.runtime_settings_parity = %#v, want 4 entries", result.Summary["runtime_settings_parity"])
	}
	readiness, ok := result.Summary["migration_readiness"].(map[string]any)
	if !ok || readiness["score_pct"] != float64(71) || readiness["blocked"] != true {
		t.Fatalf("summary.migration_readiness = %#v, want score=71 blocked=true", result.Summary["migration_readiness"])
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

func TestHandleImportLoadRunnerSupportsTruClientMobileWebArchive(t *testing.T) {
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
	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForTruClientZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="top" action="TopStep">
    <step type="block" action="Run Block" actionName="Run">
      <step type="control" action="Call Action" arguments="{&quot;Action Name&quot;:{&quot;value&quot;:&quot;Action&quot;}}" />
    </step>
    <step type="block" action="action" actionName="Action">
      <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/catalog\&quot;&quot;}}" />
      <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;$(\&quot;input[name='q']\&quot;).val(\&quot;QAInsights\&quot;);\ndocument.querySelector('#menu').click()&quot;}}" />
    </step>
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
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
		Script struct {
			Protocol string `json:"protocol"`
			Name     string `json:"name"`
			Actions  []struct {
				Type     string `json:"type"`
				Selector string `json:"selector"`
				Target   string `json:"target"`
				Value    string `json:"value"`
			} `json:"actions"`
		} `json:"script"`
		Warnings            []string `json:"warnings"`
		UnsupportedElements []string `json:"unsupported_elements"`
		ReviewSections      []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		RepairSuggestions []struct {
			Category     string `json:"category"`
			Severity     string `json:"severity"`
			Title        string `json:"title"`
			TargetAction string `json:"target_action"`
		} `json:"repair_suggestions"`
		Summary map[string]any `json:"summary"`
		Stats   struct {
			ConvertedActions int `json:"converted_actions"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Protocol != "browser" || result.Script.Name != "MobileWeb" {
		t.Fatalf("script = %#v, want browser script named MobileWeb", result.Script)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Type != "browser_navigate" || action.Target != "https://m.example.com/catalog" {
		t.Fatalf("action[0] = %#v, want browser_navigate to catalog", action)
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "input[name='q']" || action.Value != "QAInsights" {
		t.Fatalf("action[1] = %#v, want browser_fill on search input", action)
	}
	if action := result.Script.Actions[2]; action.Type != "browser_click" || action.Selector != "#menu" {
		t.Fatalf("action[2] = %#v, want browser_click on #menu", action)
	}
	if result.Stats.ConvertedActions != 3 {
		t.Fatalf("converted_actions = %d, want 3", result.Stats.ConvertedActions)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
	if len(result.UnsupportedElements) != 0 {
		t.Fatalf("unsupported_elements = %#v, want none", result.UnsupportedElements)
	}
	assertReviewSection(t, result.ReviewSections, "browser_import_assumptions", "info", "Legacy desktop TruClient remains out of scope.")
	assertRepairSuggestion(t, result.RepairSuggestions, "replay", "warning", "Run browser replay before saving as production-ready")
	assertRepairSuggestion(t, result.RepairSuggestions, "selector", "warning", "Validate selector for Evaluate JavaScript Fill 1")
	if got := result.Summary["project_file"]; got != "MobileWeb.usr" {
		t.Fatalf("summary.project_file = %#v, want MobileWeb.usr", got)
	}
	if got := result.Summary["mapped_runtime_file"]; got != "default.cfg" {
		t.Fatalf("summary.mapped_runtime_file = %#v, want default.cfg", got)
	}
	if got := result.Summary["unsupported_function_count"]; got != float64(0) {
		t.Fatalf("summary.unsupported_function_count = %#v, want 0", got)
	}
}

func TestHandleImportLoadRunnerMapsTruClientQuerySelectorValueAssignment(t *testing.T) {
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
	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForTruClientZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelector('#displayName').value = 'Stormrunner QA'&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
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
		Script struct {
			Actions []struct {
				Type     string `json:"type"`
				Selector string `json:"selector"`
				Value    string `json:"value"`
			} `json:"actions"`
		} `json:"script"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#displayName" || action.Value != "Stormrunner QA" {
		t.Fatalf("action = %#v, want browser_fill for #displayName", action)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestHandleImportLoadRunnerMapsTruClientSelectStep(t *testing.T) {
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
	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForTruClientZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="testObject" action="Select" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#country\&quot;&quot;},&quot;VisibleText&quot;:{&quot;value&quot;:&quot;\&quot;United States\&quot;&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
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
		Script struct {
			Actions []struct {
				Type     string `json:"type"`
				Selector string `json:"selector"`
				Value    string `json:"value"`
			} `json:"actions"`
		} `json:"script"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if action := result.Script.Actions[1]; action.Type != "browser_select" || action.Selector != "#country" || action.Value != "United States" {
		t.Fatalf("action = %#v, want browser_select for #country", action)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestHandleImportLoadRunnerPreservesTruClientTransactionsAndWaitSteps(t *testing.T) {
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
	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForTruClientZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="util" action="Start Transaction" index="1" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Login\&quot;&quot;}}" />
    <step type="testObject" action="Navigate" index="2" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="util" action="Wait" index="3" arguments="{&quot;Duration&quot;:{&quot;value&quot;:&quot;500&quot;}}" />
    <step type="testObject" action="Click" index="4" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
    <step type="util" action="End Transaction" index="5" arguments="{&quot;Transaction Name&quot;:{&quot;value&quot;:&quot;\&quot;Login\&quot;&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
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
		Script struct {
			Actions []struct {
				Name    string `json:"name"`
				Type    string `json:"type"`
				Value   string `json:"value"`
				WaitFor *struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"wait_for"`
			} `json:"actions"`
		} `json:"script"`
		Warnings []string `json:"warnings"`
		Stats    struct {
			ConvertedActions int `json:"converted_actions"`
			Transactions     int `json:"transactions"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Script.Actions) != 3 {
		t.Fatalf("actions len = %d, want 3", len(result.Script.Actions))
	}
	if action := result.Script.Actions[0]; action.Name != "Login_Navigate" || action.Type != "browser_navigate" {
		t.Fatalf("action[0] = %#v, want Login_Navigate browser_navigate", action)
	}
	if action := result.Script.Actions[1]; action.Name != "Login_Wait" || action.Type != "browser_wait" || action.WaitFor == nil || action.WaitFor.Type != "duration" || action.WaitFor.Value != "500" {
		t.Fatalf("action[1] = %#v, want Login_Wait browser_wait duration 500", action)
	}
	if action := result.Script.Actions[2]; action.Name != "Login_Click" || action.Type != "browser_click" {
		t.Fatalf("action[2] = %#v, want Login_Click browser_click", action)
	}
	if result.Stats.ConvertedActions != 3 || result.Stats.Transactions != 1 {
		t.Fatalf("stats = %#v, want converted_actions=3 transactions=1", result.Stats)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestHandleImportLoadRunnerMapsTruClientVerifyToAssertions(t *testing.T) {
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
	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForTruClientZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/login\&quot;&quot;}}" />
    <step type="util" action="Verify" index="2" arguments="{&quot;Title&quot;:{&quot;value&quot;:&quot;\&quot;Login Page\&quot;&quot;}}" />
    <step type="testObject" action="Click" index="3" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#submit\&quot;&quot;}}" />
    <step type="util" action="Verify" index="4" arguments="{&quot;Text&quot;:{&quot;value&quot;:&quot;\&quot;Saved stormrunner\&quot;&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
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
		Script struct {
			Actions []struct {
				Type       string `json:"type"`
				Assertions []struct {
					Type  string `json:"type"`
					Value string `json:"value"`
				} `json:"assertions"`
			} `json:"actions"`
		} `json:"script"`
		Warnings []string `json:"warnings"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Script.Actions) != 2 {
		t.Fatalf("actions len = %d, want 2", len(result.Script.Actions))
	}
	if assertions := result.Script.Actions[0].Assertions; len(assertions) != 1 || assertions[0].Type != "page_title" || assertions[0].Value != "Login Page" {
		t.Fatalf("action[0].assertions = %#v, want page_title Login Page", assertions)
	}
	if assertions := result.Script.Actions[1].Assertions; len(assertions) != 1 || assertions[0].Type != "body_contains" || assertions[0].Value != "Saved stormrunner" {
		t.Fatalf("action[1].assertions = %#v, want body_contains Saved stormrunner", assertions)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
}

func TestHandleImportLoadRunnerMapsTruClientScrollToSwipe(t *testing.T) {
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
	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForTruClientZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/home\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;window.scrollTo(0, 400)&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
`)
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
		Warnings            []string `json:"warnings"`
		UnsupportedElements []string `json:"unsupported_elements"`
		ReviewSections      []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		RepairSuggestions []struct {
			Category string `json:"category"`
			Severity string `json:"severity"`
			Title    string `json:"title"`
			Action   string `json:"action"`
		} `json:"repair_suggestions"`
		Stats struct {
			ConvertedActions int `json:"converted_actions"`
			Skipped          int `json:"skipped"`
		} `json:"stats"`
		Scripts []dsl.Script `json:"scripts"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Stats.ConvertedActions != 2 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want converted_actions=2 skipped=0", result.Stats)
	}
	if len(result.UnsupportedElements) != 0 {
		t.Fatalf("unsupported_elements = %#v, want none", result.UnsupportedElements)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 2 {
		t.Fatalf("scripts/actions = %#v, want one script with navigate and swipe", result.Scripts)
	}
	scroll := result.Scripts[0].Actions[1]
	if scroll.Type != "browser_swipe" || scroll.Selector != "body" || scroll.Params["direction"] != "up" || scroll.Params["distance"] != float64(400) {
		t.Fatalf("scroll action = %#v, want browser_swipe body direction=up distance=400", scroll)
	}
	assertReviewSection(t, result.ReviewSections, "browser_import_assumptions", "info", "Selector fidelity should be checked with browser replay before production use.")
	assertRepairSuggestion(t, result.RepairSuggestions, "replay", "warning", "Run browser replay before saving as production-ready")
}

func TestHandleImportLoadRunnerSurfacesMixedTruClientEvaluateJavaScriptUnsupportedStatements(t *testing.T) {
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
	addFile("MobileWeb.usr", `[General]
Type=TruClientMobileWeb
RuntimeSettings=default.cfg
`)
	addFile("default.xml", encodeUTF16LEForTruClientZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://m.example.com/profile\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelector('#displayName').value = 'Stormrunner QA';\nwindow.scrollTo(0, 400)&quot;}}" />
  </step>
</truScript>`))
	addFile("default.cfg", `[Browser Emulation]
DeviceProfile=iPhone_SE
TouchMode=1
`)
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
		Warnings            []string `json:"warnings"`
		UnsupportedElements []string `json:"unsupported_elements"`
		ReviewSections      []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		RepairSuggestions []struct {
			Category string `json:"category"`
			Severity string `json:"severity"`
			Title    string `json:"title"`
			Action   string `json:"action"`
		} `json:"repair_suggestions"`
		Stats struct {
			ConvertedActions int `json:"converted_actions"`
			Skipped          int `json:"skipped"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Stats.ConvertedActions != 3 || result.Stats.Skipped != 0 {
		t.Fatalf("stats = %#v, want converted_actions=3 skipped=0", result.Stats)
	}
	if len(result.UnsupportedElements) != 0 {
		t.Fatalf("unsupported_elements = %#v, want none", result.UnsupportedElements)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", result.Warnings)
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

func TestHandleLoadRunnerSupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/loadrunner/support-matrix", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		SupportMatrix []struct {
			ID      string   `json:"id"`
			Area    string   `json:"area"`
			Concept string   `json:"concept"`
			Fields  []string `json:"fields"`
			Status  string   `json:"status"`
			Notes   string   `json:"notes"`
		} `json:"support_matrix"`
		Notes          []string `json:"notes"`
		TestedExamples []struct {
			Title    string `json:"title"`
			Coverage string `json:"coverage"`
			Test     string `json:"test"`
		} `json:"tested_examples"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.SupportMatrix) == 0 {
		t.Fatal("expected loadrunner support matrix entries")
	}
	if result.SupportMatrix[0].ID == "" || result.SupportMatrix[0].Area == "" || result.SupportMatrix[0].Status == "" {
		t.Fatalf("unexpected support matrix entry: %#v", result.SupportMatrix[0])
	}
	if !containsLoadRunnerSupportMatrixStatus(result.SupportMatrix, "partial") {
		t.Fatalf("expected at least one partial loadrunner support matrix entry, got %#v", result.SupportMatrix)
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected loadrunner support matrix notes")
	}
	if len(result.TestedExamples) == 0 || result.TestedExamples[0].Title == "" || result.TestedExamples[0].Test == "" {
		t.Fatalf("tested_examples = %#v, want populated tested examples", result.TestedExamples)
	}
}

func containsLoadRunnerSupportMatrixStatus(entries []struct {
	ID      string   `json:"id"`
	Area    string   `json:"area"`
	Concept string   `json:"concept"`
	Fields  []string `json:"fields"`
	Status  string   `json:"status"`
	Notes   string   `json:"notes"`
}, want string) bool {
	for _, entry := range entries {
		if entry.Status == want {
			return true
		}
	}
	return false
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

func encodeUTF16LEForTruClientZip(text string) string {
	words := utf16.Encode([]rune(text))
	bytes := make([]byte, 0, len(words)*2)
	for _, word := range words {
		bytes = append(bytes, byte(word), byte(word>>8))
	}
	return string(bytes)
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
	for _, key := range []string{"scripts", "script", "warnings", "stats", "correlations", "assumptions", "unsupported_elements", "review_sections", "repair_suggestions", "scenario_draft", "summary", "result"} {
		if _, ok := envelope[key]; !ok {
			t.Fatalf("envelope missing key %q: %#v", key, envelope)
		}
	}
}

func assertRepairSuggestion(t *testing.T, suggestions any, wantCategory string, wantSeverity string, wantTitle string) {
	t.Helper()

	value := reflect.ValueOf(suggestions)
	if value.Kind() != reflect.Slice {
		t.Fatalf("repair suggestions = %#v, want slice", suggestions)
	}
	for i := 0; i < value.Len(); i++ {
		suggestion := value.Index(i)
		category := suggestion.FieldByName("Category")
		severity := suggestion.FieldByName("Severity")
		title := suggestion.FieldByName("Title")
		if !category.IsValid() || !severity.IsValid() || !title.IsValid() {
			continue
		}
		if category.String() == wantCategory && severity.String() == wantSeverity && title.String() == wantTitle {
			return
		}
	}
	t.Fatalf("repair_suggestions = %#v, want category=%q severity=%q title=%q", suggestions, wantCategory, wantSeverity, wantTitle)
}

func assertRepairSuggestionActionContains(t *testing.T, suggestions any, wantCategory string, wantTitle string, wantActionPart string) {
	t.Helper()

	value := reflect.ValueOf(suggestions)
	if value.Kind() != reflect.Slice {
		t.Fatalf("repair suggestions = %#v, want slice", suggestions)
	}
	for i := 0; i < value.Len(); i++ {
		suggestion := value.Index(i)
		category := suggestion.FieldByName("Category")
		title := suggestion.FieldByName("Title")
		action := suggestion.FieldByName("Action")
		if !category.IsValid() || !title.IsValid() || !action.IsValid() {
			continue
		}
		if category.String() == wantCategory && title.String() == wantTitle {
			if strings.Contains(action.String(), wantActionPart) {
				return
			}
			t.Fatalf("repair suggestion action = %q, want substring %q", action.String(), wantActionPart)
		}
	}
	t.Fatalf("repair_suggestions = %#v, want category=%q title=%q with action containing %q", suggestions, wantCategory, wantTitle, wantActionPart)
}

func assertReviewSection(t *testing.T, sections any, wantKey string, wantSeverity string, wantItem string) {
	t.Helper()

	value := reflect.ValueOf(sections)
	if value.Kind() != reflect.Slice {
		t.Fatalf("review sections = %#v, want slice", sections)
	}
	for i := 0; i < value.Len(); i++ {
		section := value.Index(i)
		key := section.FieldByName("Key")
		severity := section.FieldByName("Severity")
		items := section.FieldByName("Items")
		if !key.IsValid() || !severity.IsValid() || !items.IsValid() {
			continue
		}
		if key.String() != wantKey {
			continue
		}
		if severity.String() != wantSeverity {
			t.Fatalf("review section %q severity = %q, want %q", wantKey, severity.String(), wantSeverity)
		}
		for itemIndex := 0; itemIndex < items.Len(); itemIndex++ {
			if items.Index(itemIndex).String() == wantItem {
				return
			}
		}
		t.Fatalf("review section %q items = %#v, want item %q", wantKey, items.Interface(), wantItem)
	}
	t.Fatalf("review_sections = %#v, want section %q", sections, wantKey)
}
