package api

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/jasonczech/apexload/pkg/convert/loadrunner"
	"github.com/jasonczech/apexload/pkg/convert/trueclient"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
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
	if created.AuthoringHealth == nil || created.AuthoringHealth.Status != "needs_review" {
		t.Fatalf("created authoring health = %#v, want needs_review", created.AuthoringHealth)
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
		Valid           bool            `json:"valid"`
		Issues          []string        `json:"issues"`
		AuthoringHealth AuthoringHealth `json:"authoring_health"`
	}
	if err := json.Unmarshal(validateResp.Body.Bytes(), &validatePayload); err != nil {
		t.Fatalf("json.Unmarshal(validate) returned error: %v", err)
	}
	if !validatePayload.Valid || len(validatePayload.Issues) != 0 {
		t.Fatalf("validate payload = %#v, want valid script with no issues", validatePayload)
	}
	if validatePayload.AuthoringHealth.Status != "needs_review" || validatePayload.AuthoringHealth.Score < 60 {
		t.Fatalf("authoring health = %#v, want reviewable score", validatePayload.AuthoringHealth)
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
	if updated.AuthoringHealth == nil || updated.AuthoringHealth.Status != "needs_review" {
		t.Fatalf("updated authoring health = %#v, want needs_review", updated.AuthoringHealth)
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
			Metadata struct {
				Import struct {
					Preserved map[string]any `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
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
		SourceReview map[string]any `json:"source_review"`
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
	if len(result.SourceReview) == 0 {
		t.Fatal("expected root source_review in response")
	}
	if result.Script.Metadata.Import.Preserved["source_review"] == nil {
		t.Fatalf("script metadata missing preserved source_review: %#v", result.Script.Metadata.Import.Preserved)
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

func TestHandleImportDevWebJSON(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name": "Socket Import",
		"content": `
load.action("Socket Flow", async function() {
  const ws = new load.WebSocket("wss://stream.example.test/orders");
  ws.send("{\"type\":\"subscribe\"}");
  ws.receive();
  ws.close();
});`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/devweb", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Script struct {
			Name     string `json:"name"`
			Protocol string `json:"protocol"`
			Actions  []struct {
				Type string `json:"type"`
				Body string `json:"body"`
			} `json:"actions"`
		} `json:"script"`
		Stats struct {
			ConvertedActions int `json:"converted_actions"`
			WebSocketActions int `json:"websocket_actions"`
		} `json:"stats"`
		ProtocolCompat struct {
			SourceFormat      string `json:"source_format"`
			CanonicalProtocol string `json:"canonical_protocol"`
			MappedSteps       int    `json:"mapped_steps"`
			PromotionStatus   string `json:"promotion_status"`
		} `json:"protocol_compat_report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Name != "Socket_Import" {
		t.Fatalf("script name = %q, want Socket_Import", result.Script.Name)
	}
	if result.Script.Protocol != "websocket" {
		t.Fatalf("protocol = %q, want websocket", result.Script.Protocol)
	}
	if result.Stats.ConvertedActions != 4 || result.Stats.WebSocketActions != 4 {
		t.Fatalf("stats = %#v", result.Stats)
	}
	if result.Script.Actions[1].Body != `{"type":"subscribe"}` {
		t.Fatalf("send body = %q", result.Script.Actions[1].Body)
	}
	if result.ProtocolCompat.SourceFormat != "DevWeb" || result.ProtocolCompat.CanonicalProtocol != "devweb" || result.ProtocolCompat.MappedSteps != 4 {
		t.Fatalf("protocol_compat_report = %#v", result.ProtocolCompat)
	}
}

func TestHandleImportHARJSON(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name": "HAR Import",
		"content": `{
  "log": {
    "entries": [{
      "request": {
        "method": "GET",
        "url": "https://api.example.test/health",
        "headers": []
      },
      "response": {"status": 200, "content": {"text": "{\"ok\":true}"}}
    }]
  }
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Script struct {
			Name     string `json:"name"`
			Protocol string `json:"protocol"`
			Actions  []struct {
				Method string `json:"method"`
				Target string `json:"target"`
			} `json:"actions"`
		} `json:"script"`
		Stats struct {
			ConvertedActions int `json:"converted_actions"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Name != "HAR Import" {
		t.Fatalf("script name = %q, want HAR Import", result.Script.Name)
	}
	if result.Script.Protocol != "http" {
		t.Fatalf("protocol = %q, want http", result.Script.Protocol)
	}
	if result.Stats.ConvertedActions != 1 {
		t.Fatalf("converted actions = %d, want 1", result.Stats.ConvertedActions)
	}
	if result.Script.Actions[0].Method != "GET" || result.Script.Actions[0].Target != "/health" {
		t.Fatalf("action = %#v", result.Script.Actions[0])
	}
}

func TestHandleImportHARHonorsIncludeStaticAssetsOption(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := map[string]any{
		"name": "HAR Import",
		"options": map[string]any{
			"include_static_assets": true,
		},
		"content": `{
  "log": {
    "entries": [
      {
        "request": {"method": "GET", "url": "https://api.example.test/app.js", "headers": []},
        "response": {"status": 200, "content": {"mimeType": "application/javascript", "text": "console.log(1)"}}
      },
      {
        "request": {"method": "GET", "url": "https://api.example.test/health", "headers": []},
        "response": {"status": 200, "content": {"mimeType": "application/json", "text": "{\"ok\":true}"}}
      }
    ]
  }
}`,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("json.Marshal returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/har", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Stats struct {
			ConvertedActions int `json:"converted_actions"`
			FilteredAssets   int `json:"filtered_assets"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Stats.ConvertedActions != 2 || result.Stats.FilteredAssets != 0 {
		t.Fatalf("stats = %#v, want static asset retained", result.Stats)
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
			Metadata struct {
				Import struct {
					Preserved map[string]json.RawMessage `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
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
		ProtocolCompat struct {
			SourceFormat      string `json:"source_format"`
			CanonicalProtocol string `json:"canonical_protocol"`
			ImportMode        string `json:"import_mode"`
			MappedSteps       int    `json:"mapped_steps"`
			PromotionStatus   string `json:"promotion_status"`
		} `json:"protocol_compat_report"`
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
	if len(result.UnsupportedElements) != 0 {
		t.Fatalf("unsupported_elements = %v, want none because parsed custom calls are review actions", result.UnsupportedElements)
	}
	if len(result.Assumptions) != 0 {
		t.Fatalf("assumptions = %v, want none", result.Assumptions)
	}
	assertReviewSection(t, result.ReviewSections, "preserved_metadata", "info", "Project file: Demo.usr")
	assertReviewSection(t, result.ReviewSections, "runtime_settings_parity", "info", "Executed: HTTP / Timeouts, redirects, and connection reuse [default.cfg]")
	assertReviewSection(t, result.ReviewSections, "runtime_settings_migration_readiness", "info", "Score: 100%")
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
	if got := result.Summary["unsupported_function_count"]; got != float64(0) {
		t.Fatalf("summary.unsupported_function_count = %#v, want 0", got)
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
	sourceReview, ok := result.Summary["source_review"].(map[string]any)
	if !ok {
		t.Fatalf("summary.source_review = %#v, want object", result.Summary["source_review"])
	}
	sourceFiles, ok := sourceReview["source_files"].([]any)
	if !ok || len(sourceFiles) == 0 {
		t.Fatalf("source_review.source_files = %#v, want non-empty slice", sourceReview["source_files"])
	}
	mappings, ok := sourceReview["mappings"].([]any)
	if !ok || len(mappings) == 0 {
		t.Fatalf("source_review.mappings = %#v, want non-empty slice", sourceReview["mappings"])
	}
	if _, ok := result.Script.Metadata.Import.Preserved["source_review"]; !ok {
		t.Fatalf("script metadata missing preserved source_review: %#v", result.Script.Metadata.Import.Preserved)
	}
	if _, ok := result.Script.Metadata.Import.Preserved["protocol_compat_report"]; !ok {
		t.Fatalf("script metadata missing preserved protocol_compat_report: %#v", result.Script.Metadata.Import.Preserved)
	}
	if result.ProtocolCompat.SourceFormat != "LoadRunner" || result.ProtocolCompat.CanonicalProtocol != "web_http_html" || result.ProtocolCompat.ImportMode != "executable" || result.ProtocolCompat.MappedSteps == 0 {
		t.Fatalf("protocol_compat_report = %#v", result.ProtocolCompat)
	}
}

func TestHandleValidateLoadRunnerImportReturnsSalesforceNativePreview(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("salesforce_native", "true"); err != nil {
		t.Fatalf("WriteField returned error: %v", err)
	}

	part, err := writer.CreateFormFile("file", "salesforce.zip")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	zipWriter := zip.NewWriter(part)
	addZipEntry := func(name string, content string) {
		entry, err := zipWriter.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	addZipEntry("Salesforce.usr", `[General]
Type=Web

[Actions]
Action1=Action.c
`)
	addZipEntry("Action.c", `
Action()
{
	web_url("Login", "URL=https://attone--perfhyper2.sandbox.my.salesforce.com/login", LAST);
	web_custom_request("Aura",
		"URL=https://attone--perfhyper2.sandbox.lightning.force.com/aura",
		"Method=POST",
		"EncType=application/json",
		"Body={\"descriptor\":\"apex://AccountController/ACTION$get\",\"token\":\"tok-abcdef123456\"}",
		LAST);
	return 0;
}
`)
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	harPart, err := writer.CreateFormFile("har_file", "salesforce.har")
	if err != nil {
		t.Fatalf("CreateFormFile har returned error: %v", err)
	}
	har := `{
	  "log": {"entries": [
	    {"request": {"method": "GET", "url": "https://attone--perfhyper2.sandbox.my.salesforce.com/login", "headers": []}, "response": {"headers": [], "content": {"text": "{\"access_token\":\"tok-abcdef123456\"}"}}},
	    {"request": {"method": "POST", "url": "https://attone--perfhyper2.sandbox.lightning.force.com/aura", "headers": [], "postData": {"text": "{\"token\":\"tok-abcdef123456\"}"}}, "response": {"headers": [], "content": {"text": "{\"actions\":[{\"state\":\"SUCCESS\"}]}"}}}
	  ]}
	}`
	if _, err := harPart.Write([]byte(har)); err != nil {
		t.Fatalf("Write har returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner/validate", bytes.NewReader(body.Bytes()))
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var result struct {
		Summary struct {
			SalesforceReview struct {
				Detected         bool `json:"detected"`
				HARMatchCoverage struct {
					MatchedRequests  int `json:"matched_requests"`
					ImportedRequests int `json:"imported_requests"`
				} `json:"har_match_coverage"`
				NativePreview struct {
					Script *dsl.Script `json:"script"`
				} `json:"native_preview"`
			} `json:"salesforce_review"`
		} `json:"summary"`
		ReviewSections []struct {
			Key string `json:"key"`
		} `json:"review_sections"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !result.Summary.SalesforceReview.Detected {
		t.Fatalf("salesforce review = %#v, want detected", result.Summary.SalesforceReview)
	}
	if result.Summary.SalesforceReview.HARMatchCoverage.MatchedRequests != 2 || result.Summary.SalesforceReview.HARMatchCoverage.ImportedRequests != 2 {
		t.Fatalf("coverage = %#v, want 2/2", result.Summary.SalesforceReview.HARMatchCoverage)
	}
	if result.Summary.SalesforceReview.NativePreview.Script == nil {
		t.Fatal("native preview script missing")
	}
	if !reviewSectionsContain(result.ReviewSections, "salesforce_review") {
		t.Fatalf("review sections = %#v, want salesforce_review", result.ReviewSections)
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

	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want no streaming compatibility warning", result.Warnings)
	}
	assertReviewSection(t, result.ReviewSections, "runtime_settings_parity", "info", "Executed: Streaming / Adaptive or fixed playback settings [default.cfg]")
	assertReviewSection(t, result.ReviewSections, "runtime_settings_migration_readiness", "info", "No imported runtime settings matched the selected block statuses.")
	statusCounts, ok := result.Summary["runtime_settings_status_counts"].(map[string]any)
	if !ok {
		t.Fatalf("summary.runtime_settings_status_counts = %#v, want map", result.Summary["runtime_settings_status_counts"])
	}
	if statusCounts["executed"] != float64(4) || statusCounts["partial"] != nil || statusCounts["compatibility_only"] != nil {
		t.Fatalf("summary.runtime_settings_status_counts = %#v, want executed=4", statusCounts)
	}
	runtimeParity, ok := result.Summary["runtime_settings_parity"].([]any)
	if !ok || len(runtimeParity) != 4 {
		t.Fatalf("summary.runtime_settings_parity = %#v, want 4 entries", result.Summary["runtime_settings_parity"])
	}
	readiness, ok := result.Summary["migration_readiness"].(map[string]any)
	if !ok || readiness["score_pct"] != float64(100) || readiness["blocked"] != false {
		t.Fatalf("summary.migration_readiness = %#v, want score=100 blocked=false", result.Summary["migration_readiness"])
	}
}

func TestHandleImportLoadRunnerGoldenFixtureRuntimeParity(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, contentType := loadRunnerFixtureMultipartBody(t, "../convert/loadrunner/testdata/golden_web_http_html", "golden-web.zip", map[string]string{
		"include_assertions":       "true",
		"preserve_transactions":    "true",
		"migration_block_statuses": "partial,compatibility_only",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner", bytes.NewReader(body))
	req.Header.Set("Content-Type", contentType)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Warnings       []string        `json:"warnings"`
		ReviewSections []reviewSection `json:"review_sections"`
		Summary        map[string]any  `json:"summary"`
		Script         struct {
			Protocol string `json:"protocol"`
			Config   struct {
				BaseURL string `json:"base_url"`
				Browser struct {
					DownloadResources string `json:"download_resources"`
					DeviceProfile     string `json:"device_profile"`
				} `json:"browser"`
			} `json:"config"`
		} `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}

	if result.Script.Protocol != "http" || result.Script.Config.BaseURL != "https://shop.example.test" {
		t.Fatalf("script = %#v, want converted golden HTTP script", result.Script)
	}
	if result.Script.Config.Browser.DownloadResources != "recorded" || result.Script.Config.Browser.DeviceProfile != "pixel_7" {
		t.Fatalf("browser config = %#v, want recorded pixel_7 settings", result.Script.Config.Browser)
	}
	if len(result.Warnings) != 0 {
		t.Fatalf("warnings = %#v, want no streaming compatibility warning", result.Warnings)
	}
	assertReviewSection(t, result.ReviewSections, "runtime_settings_parity", "warning", "Executed: HTTP / Timeouts, redirects, and connection reuse [default.cfg]")
	assertReviewSection(t, result.ReviewSections, "runtime_settings_migration_readiness", "blocker", "Blocking setting: Additional Attributes / Arbitrary RTS name/value metadata")

	statusCounts, ok := result.Summary["runtime_settings_status_counts"].(map[string]any)
	if !ok {
		t.Fatalf("summary.runtime_settings_status_counts = %#v, want map", result.Summary["runtime_settings_status_counts"])
	}
	if statusCounts["executed"] != float64(26) || statusCounts["partial"] != nil || statusCounts["compatibility_only"] != float64(1) {
		t.Fatalf("runtime_settings_status_counts = %#v, want executed=26 compatibility_only=1", statusCounts)
	}
	runtimeParity, ok := result.Summary["runtime_settings_parity"].([]any)
	if !ok || len(runtimeParity) != 27 {
		t.Fatalf("summary.runtime_settings_parity = %#v, want 27 entries", result.Summary["runtime_settings_parity"])
	}
	readiness, ok := result.Summary["migration_readiness"].(map[string]any)
	if !ok || readiness["score_pct"] != float64(97) || readiness["blocked"] != true {
		t.Fatalf("summary.migration_readiness = %#v, want score=97 blocked=true", result.Summary["migration_readiness"])
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

func TestHandleValidateLoadRunnerImportIncludesDetectedProtocol(t *testing.T) {
	server := NewServer(ServerConfig{})
	archive := buildLoadRunnerProtocolArchive(t, "ODBC", `Action()
{
    lrd_stmt("select id from customers where status = ?", "ParamStatus=active", LAST);
}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner/validate", bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/zip")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Script struct {
			Protocol string `json:"protocol"`
			Config   struct {
				Database struct {
					Driver string `json:"driver"`
				} `json:"database"`
			} `json:"config"`
		} `json:"script"`
		DetectedProtocol struct {
			Canonical string `json:"canonical_loadrunner_protocol"`
			Storm     string `json:"apexload_protocol"`
			Mode      string `json:"import_mode"`
		} `json:"detected_protocol"`
		Summary struct {
			ImportMode string `json:"import_mode"`
		} `json:"summary"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Protocol != "database" || result.Script.Config.Database.Driver != "odbc" {
		t.Fatalf("script = %#v, want database/odbc", result.Script)
	}
	if result.DetectedProtocol.Canonical != "database" || result.DetectedProtocol.Storm != "database" || result.DetectedProtocol.Mode != "executable" {
		t.Fatalf("detected_protocol = %#v, want executable database routing", result.DetectedProtocol)
	}
	if result.Summary.ImportMode != "executable" {
		t.Fatalf("summary.import_mode = %q, want executable", result.Summary.ImportMode)
	}
}

func TestHandleImportLoadRunnerRoutesDevWebArchive(t *testing.T) {
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
	addFile("LRD/DevWeb.usr", "[General]\nType=DevWeb\n")
	addFile("LRD/main.js", strings.Join([]string{
		`load.action("Browse", async function() {`,
		"  new load.WebRequest({ url: `${load.params.BASE_URL}/orders`, method: \"GET\" }).sendSync();",
		`});`,
	}, "\n"))
	addFile("LRD/parameters.yml", `
parameters:
- name: BASE_URL
  type: csv
  fileName: base.csv
  columnName: url
`)
	addFile("LRD/base.csv", "url\nhttps://api.example.test\n")
	addFile("LRD/rts.yml", "httpConnection:\n  connectTimeout: 7\n")
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
			Actions  []struct {
				Target string `json:"target"`
			} `json:"actions"`
			Metadata struct {
				Import struct {
					Preserved map[string]any `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
		} `json:"script"`
		DetectedProtocol struct {
			Canonical string `json:"canonical_loadrunner_protocol"`
			Storm     string `json:"apexload_protocol"`
			Mode      string `json:"import_mode"`
		} `json:"detected_protocol"`
		ProtocolCompat struct {
			SourceFormat      string         `json:"source_format"`
			CanonicalProtocol string         `json:"canonical_protocol"`
			ImportMode        string         `json:"import_mode"`
			RuntimeParity     map[string]int `json:"runtime_parity"`
			PromotionStatus   string         `json:"promotion_status"`
			NextActions       []string       `json:"next_actions"`
		} `json:"protocol_compat_report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Protocol != "http" || len(result.Script.Actions) != 1 || result.Script.Actions[0].Target != "{{data.base.url}}/orders" {
		t.Fatalf("script = %#v, want routed native DevWeb HTTP script", result.Script)
	}
	if result.DetectedProtocol.Canonical != "devweb" || result.DetectedProtocol.Storm != "http" || result.DetectedProtocol.Mode != "executable" {
		t.Fatalf("detected_protocol = %#v, want executable DevWeb routing", result.DetectedProtocol)
	}
	if result.ProtocolCompat.SourceFormat != "DevWeb" || result.ProtocolCompat.CanonicalProtocol != "devweb" || result.ProtocolCompat.ImportMode != "executable" || result.ProtocolCompat.RuntimeParity["executed"] != 1 {
		t.Fatalf("protocol_compat_report = %#v, want DevWeb runtime parity", result.ProtocolCompat)
	}
	if len(result.ProtocolCompat.NextActions) == 0 || !strings.Contains(result.ProtocolCompat.NextActions[0], "DevWeb replay") {
		t.Fatalf("next_actions = %#v, want DevWeb-specific validation action", result.ProtocolCompat.NextActions)
	}
	if result.Script.Metadata.Import.Preserved["loadrunner_endpoint_routed_to"] != "devweb" {
		t.Fatalf("preserved metadata = %#v, want DevWeb route marker", result.Script.Metadata.Import.Preserved)
	}
}

func TestHandleImportLoadRunnerReturnsReviewArtifactForExternalRunnerProtocol(t *testing.T) {
	server := NewServer(ServerConfig{})
	archive := buildLoadRunnerProtocolArchive(t, "SAP GUI", `Action()
{
    sapgui_logon("Login", "ASHOST=server", LAST);
}`)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/loadrunner", bytes.NewReader(archive))
	req.Header.Set("Content-Type", "application/zip")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Scripts          []json.RawMessage `json:"scripts"`
		Script           *json.RawMessage  `json:"script"`
		DetectedProtocol struct {
			Canonical string `json:"canonical_loadrunner_protocol"`
			Mode      string `json:"import_mode"`
		} `json:"detected_protocol"`
		ReviewArtifact struct {
			ID               string   `json:"id"`
			RequiredEvidence []string `json:"required_evidence"`
		} `json:"review_artifact"`
		ProtocolCompat struct {
			CanonicalProtocol    string   `json:"canonical_protocol"`
			ImportMode           string   `json:"import_mode"`
			PromotionStatus      string   `json:"promotion_status"`
			MappedSteps          int      `json:"mapped_steps"`
			ReviewGatedSteps     int      `json:"review_gated_steps"`
			UnsupportedFragments []string `json:"unsupported_fragments"`
			NextActions          []string `json:"next_actions"`
		} `json:"protocol_compat_report"`
		ReviewSections []struct {
			Key string `json:"key"`
		} `json:"review_sections"`
		RepairSuggestions []struct {
			Category string `json:"category"`
		} `json:"repair_suggestions"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 0 || result.Script != nil {
		t.Fatalf("scripts/script = %d/%#v, want no executable script", len(result.Scripts), result.Script)
	}
	if result.DetectedProtocol.Canonical != "sap_gui" || result.DetectedProtocol.Mode != "external_runner_required" {
		t.Fatalf("detected_protocol = %#v, want SAP GUI external runner", result.DetectedProtocol)
	}
	if result.ReviewArtifact.ID == "" || len(result.ReviewArtifact.RequiredEvidence) == 0 {
		t.Fatalf("review_artifact = %#v, want stored review evidence guidance", result.ReviewArtifact)
	}
	if result.ProtocolCompat.CanonicalProtocol != "sap_gui" || result.ProtocolCompat.ImportMode != "external_runner_required" || result.ProtocolCompat.PromotionStatus != "external_runner_required" {
		t.Fatalf("protocol_compat_report = %#v, want external-runner SAP GUI classification", result.ProtocolCompat)
	}
	if result.ProtocolCompat.MappedSteps != 0 || len(result.ProtocolCompat.NextActions) == 0 {
		t.Fatalf("protocol_compat_report = %#v, want no mapped steps and next actions", result.ProtocolCompat)
	}
	if result.ProtocolCompat.ReviewGatedSteps == 0 || !containsString(result.ProtocolCompat.UnsupportedFragments, "sapgui_logon") {
		t.Fatalf("protocol_compat_report = %#v, want preserved SAP GUI function inventory", result.ProtocolCompat)
	}
	if !responseHasReviewSection(result.ReviewSections, "review_artifact") || !responseHasRepairCategory(result.RepairSuggestions, "protocol") {
		t.Fatalf("review sections/suggestions = %#v/%#v, want protocol review guidance", result.ReviewSections, result.RepairSuggestions)
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
			Category               string   `json:"category"`
			Severity               string   `json:"severity"`
			Title                  string   `json:"title"`
			TargetAction           string   `json:"target_action"`
			SelectorStabilityScore int      `json:"selector_stability_score"`
			SelectorSignals        []string `json:"selector_signals"`
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
	assertReviewSection(t, result.ReviewSections, "browser_import_assumptions", "info", "Legacy desktop TruClient remains out of scope; TruClient - Web and TruClient 2.0 - Web are planned under the VuGen Web 2.0 profile.")
	assertRepairSuggestion(t, result.RepairSuggestions, "replay", "warning", "Run browser replay before saving as production-ready")
	assertRepairSuggestion(t, result.RepairSuggestions, "selector", "warning", "Validate selector for Evaluate JavaScript Fill 1")
	assertRepairSuggestion(t, result.RepairSuggestions, "wait", "info", "Add readiness wait after Evaluate JavaScript Fill 1")
	selectorScoreSeen := false
	for _, suggestion := range result.RepairSuggestions {
		if suggestion.Category == "selector" && suggestion.TargetAction == "Evaluate JavaScript Fill 1" {
			selectorScoreSeen = suggestion.SelectorStabilityScore > 0 && len(suggestion.SelectorSignals) > 0
		}
	}
	if !selectorScoreSeen {
		t.Fatalf("repair_suggestions = %#v, want selector stability score and signals", result.RepairSuggestions)
	}
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

func TestHandleImportLoadRunnerSurfacesTruClientWeb2ReviewSections(t *testing.T) {
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
	addFile("Web2/Web2.usr", `[General]
Type=TruClient 2.0 - Web
RuntimeSettings=default.cfg
`)
	addFile("Web2/default.xml", encodeUTF16LEForTruClientZip(`<?xml version="1.0" encoding="utf-16"?>
<truScript>
  <step type="block" action="action" actionName="Action">
    <step type="testObject" action="Navigate" stepId="nav-1" index="1" arguments="{&quot;Location&quot;:{&quot;value&quot;:&quot;\&quot;https://shop.example.com\&quot;&quot;}}" />
    <step type="testObject" action="Click" stepId="click-1" index="2" arguments="{&quot;Selector&quot;:{&quot;value&quot;:&quot;\&quot;#cart\&quot;&quot;}}" />
    <step type="util" action="Evaluate JavaScript" stepId="js-1" index="3" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;window.app.checkout()&quot;}}" />
  </step>
</truScript>`))
	addFile("Web2/ObjectRepository/MainObjects.xml", "<objects><object name=\"Cart\" /></objects>")
	addFile("Web2/parameters/users.dat", "username,password\nqa,secret\n")
	addFile("Web2/default.cfg", "[ThinkTime]\nOptions=RECORDED\n")
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
			Actions  []struct {
				Type     string         `json:"type"`
				Metadata map[string]any `json:"metadata"`
			} `json:"actions"`
		} `json:"script"`
		Warnings       []string `json:"warnings"`
		ReviewSections []struct {
			Key      string   `json:"key"`
			Severity string   `json:"severity"`
			Items    []string `json:"items"`
		} `json:"review_sections"`
		RepairSuggestions []struct {
			Category string `json:"category"`
			Severity string `json:"severity"`
			Title    string `json:"title"`
		} `json:"repair_suggestions"`
		Summary                 map[string]any `json:"summary"`
		TrueClient2CompatReport struct {
			PromotionStatus      string `json:"promotion_status"`
			PromotionAllowed     bool   `json:"promotion_allowed"`
			JavaScriptReviewGate string `json:"javascript_review_gate"`
			ReplayStatus         string `json:"replay_status"`
			ConvertedActions     int    `json:"converted_actions"`
		} `json:"trueclient2_compat_report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Script.Protocol != "browser" || len(result.Script.Actions) != 3 {
		t.Fatalf("script = %#v, want browser draft with three converted actions", result.Script)
	}
	if result.Script.Actions[2].Type != "browser_evaluate_js" || result.Script.Actions[2].Metadata["review_required"] != true {
		t.Fatalf("action[2] = %#v, want review-gated browser_evaluate_js", result.Script.Actions[2])
	}
	if !containsString(result.Warnings, "TruClient 2.0 - Web import is review-only; validate selectors, waits, runtime settings, and replay evidence before promotion.") {
		t.Fatalf("warnings = %#v, want review-only warning", result.Warnings)
	}
	assertReviewSection(t, result.ReviewSections, "web2_profile_summary", "warning", "Source script type: TruClient 2.0 - Web")
	assertReviewSection(t, result.ReviewSections, "web2_unsupported_source_fragments", "warning", "Evaluate JavaScript #3: Review-only TruClient step requires replay or guided repair before approval.")
	assertReviewSection(t, result.ReviewSections, "web2_object_identification", "warning", "Object repository: ObjectRepository/MainObjects.xml")
	assertReviewSection(t, result.ReviewSections, "web2_object_identification", "warning", "Selector candidates extracted: 1")
	assertReviewSection(t, result.ReviewSections, "web2_parameter_mapping", "info", "Parameter data: parameters/users.dat")
	assertReviewSection(t, result.ReviewSections, "web2_replay_readiness", "warning", "Run browser replay against the target application.")
	assertReviewSection(t, result.ReviewSections, "web2_http_conversion_eligibility", "info", "Capture network requests during replay before generating an HTTP draft.")
	assertRepairSuggestion(t, result.RepairSuggestions, "replay", "warning", "Attach browser replay evidence")
	assertRepairSuggestion(t, result.RepairSuggestions, "selector", "warning", "Review Web 2.0 object identification")
	assertRepairSuggestion(t, result.RepairSuggestions, "data", "info", "Verify Web 2.0 parameter data fit")
	assertRepairSuggestion(t, result.RepairSuggestions, "unsupported", "warning", "Resolve Web 2.0 source fragments")
	assertRepairSuggestion(t, result.RepairSuggestions, "conversion", "info", "Gate browser-to-HTTP conversion")
	if result.Summary["source_script_type"] != "TruClient 2.0 - Web" {
		t.Fatalf("summary.source_script_type = %#v, want TruClient 2.0 - Web", result.Summary["source_script_type"])
	}
	inspection, ok := result.Summary["trueclient_archive_inspection"].(map[string]any)
	if !ok {
		t.Fatalf("summary.trueclient_archive_inspection = %#v, want object", result.Summary["trueclient_archive_inspection"])
	}
	if inspection["step_inventory"] == nil || inspection["unsupported_fragments"] == nil {
		t.Fatalf("inspection = %#v, want step inventory and unsupported fragments", inspection)
	}
	readiness, ok := result.Summary["web2_migration_readiness"].(map[string]any)
	if !ok {
		t.Fatalf("summary.web2_migration_readiness = %#v, want readiness report", result.Summary["web2_migration_readiness"])
	}
	if readiness["imported_steps"] != float64(3) || readiness["converted_steps"] != float64(3) || readiness["conversion_to_http_readiness"] != "needs_replay_network" {
		t.Fatalf("web2 readiness = %#v, want import/convert counts and HTTP conversion readiness", readiness)
	}
	if readiness["replay_status"] != "required" || readiness["runtime_parity_status"] != "ready" {
		t.Fatalf("web2 readiness status = %#v, want replay required/runtime ready", readiness)
	}
	if result.TrueClient2CompatReport.PromotionAllowed || result.TrueClient2CompatReport.PromotionStatus != "blocked" || result.TrueClient2CompatReport.JavaScriptReviewGate != "required" || result.TrueClient2CompatReport.ReplayStatus != "required" || result.TrueClient2CompatReport.ConvertedActions != 3 {
		t.Fatalf("trueclient2_compat_report = %#v, want blocked review-gated report", result.TrueClient2CompatReport)
	}
}

func TestBrowserSelectorStabilityScoreRanksStableAndBrittleSelectors(t *testing.T) {
	stableScore, stableSignals := browserSelectorStabilityScore("[data-testid='submit-order']")
	brittleScore, brittleSignals := browserSelectorStabilityScore("body > div:nth-child(3) > button.generated-4821")
	if stableScore < 80 {
		t.Fatalf("stable score = %d signals=%v, want >= 80", stableScore, stableSignals)
	}
	if brittleScore >= stableScore || brittleScore > 40 {
		t.Fatalf("brittle score = %d stable=%d signals=%v, want lower brittle score", brittleScore, stableScore, brittleSignals)
	}
	if !strings.Contains(strings.Join(brittleSignals, ","), "positional selector") {
		t.Fatalf("brittle signals = %v, want positional selector signal", brittleSignals)
	}
}

func TestTrueClientJavaScriptReviewPolicyRequiresReviewerEvidence(t *testing.T) {
	inspection := &trueclient.ArchiveInspection{
		ScriptType: "TruClient 2.0 - Web",
		StepInventory: trueclient.StepInventory{
			Total:     1,
			Supported: map[string]int{"evaluate_javascript": 1},
		},
	}
	result := &loadrunner.ConvertResult{
		Script: &dsl.Script{
			Protocol: "browser",
			Actions: []dsl.Action{{
				Name:   "Checkout JS",
				Type:   "browser_evaluate_js",
				Target: "window.app.checkout()",
				Metadata: map[string]any{
					"review_gate":                "javascript",
					"javascript_review_approved": true,
					"review_reason":              "TruClient JavaScript could not be decomposed into native browser actions.",
				},
			}},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{Preserved: map[string]any{
				"browser_replay_status": "passed",
			}}},
		},
		DetectedProtocol: &loadrunner.DetectedProtocol{ImportMode: loadrunner.ImportModeExecutable},
		Stats:            loadrunner.ConvertStats{ConvertedActions: 1},
	}
	summary := loadRunnerImportSummary{
		ImportMode:           string(loadrunner.ImportModeExecutable),
		TrueClientInspection: inspection,
		SourceScriptType:     "TruClient 2.0 - Web",
		SourceProfile:        "vugen_web2 browser profile",
	}

	report := buildTrueClient2CompatReport(result, summary)
	if report.JavaScriptReviewGate != "required" || report.JavaScriptReview.PendingActions != 1 || report.PromotionAllowed {
		t.Fatalf("report = %#v, want pending JS gate without reviewer evidence", report)
	}

	result.Script.Actions[0].Metadata["javascript_review_reviewer"] = "qa-reviewer"
	result.Script.Actions[0].Metadata["javascript_review_approved_at"] = "2026-06-09T12:00:00Z"
	report = buildTrueClient2CompatReport(result, summary)
	if report.JavaScriptReviewGate != "approved" || report.JavaScriptReview.ApprovedActions != 1 || !report.PromotionAllowed {
		t.Fatalf("approved report = %#v, want approved JS gate and promotion allowed", report)
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
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelector('#displayName').value = 'APEX Load QA'&quot;}}" />
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
	if action := result.Script.Actions[1]; action.Type != "browser_fill" || action.Selector != "#displayName" || action.Value != "APEX Load QA" {
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
    <step type="util" action="Verify" index="4" arguments="{&quot;Text&quot;:{&quot;value&quot;:&quot;\&quot;Saved apexload\&quot;&quot;}}" />
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
	if assertions := result.Script.Actions[1].Assertions; len(assertions) != 1 || assertions[0].Type != "body_contains" || assertions[0].Value != "Saved apexload" {
		t.Fatalf("action[1].assertions = %#v, want body_contains Saved apexload", assertions)
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
    <step type="util" action="Evaluate JavaScript" index="2" arguments="{&quot;Code&quot;:{&quot;value&quot;:&quot;document.querySelector('#displayName').value = 'APEX Load QA';\nwindow.scrollTo(0, 400)&quot;}}" />
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
        <CacheManager testname="Cache">
          <boolProp name="clearEachIteration">true</boolProp>
        </CacheManager>
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
			Metadata struct {
				Import struct {
					Preserved map[string]any `json:"preserved"`
				} `json:"import"`
			} `json:"metadata"`
		} `json:"scripts"`
		ScenarioDraft any            `json:"scenario_draft"`
		Correlations  []any          `json:"correlations"`
		Assumptions   []string       `json:"assumptions"`
		Unsupported   []string       `json:"unsupported_elements"`
		SourceReview  map[string]any `json:"source_review"`
		Stats         struct {
			ConvertedActions int `json:"converted_actions"`
		} `json:"stats"`
		ProtocolCompat struct {
			SourceFormat      string         `json:"source_format"`
			CanonicalProtocol string         `json:"canonical_protocol"`
			MappedSteps       int            `json:"mapped_steps"`
			PromotionStatus   string         `json:"promotion_status"`
			RuntimeParity     map[string]int `json:"runtime_parity"`
		} `json:"protocol_compat_report"`
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
	if len(result.SourceReview) == 0 {
		t.Fatal("expected root source_review in response")
	}
	if result.Scripts[0].Metadata.Import.Preserved["source_review"] == nil {
		t.Fatalf("script metadata missing preserved source_review: %#v", result.Scripts[0].Metadata.Import.Preserved)
	}
	if result.Scripts[0].Metadata.Import.Preserved["protocol_compat_report"] == nil {
		t.Fatalf("script metadata missing preserved protocol_compat_report: %#v", result.Scripts[0].Metadata.Import.Preserved)
	}
	if result.ProtocolCompat.SourceFormat != "JMeter JMX" || result.ProtocolCompat.CanonicalProtocol != "jmeter" || result.ProtocolCompat.MappedSteps != 1 {
		t.Fatalf("protocol_compat_report = %#v", result.ProtocolCompat)
	}
	if result.ProtocolCompat.RuntimeParity["partial"] != 1 {
		t.Fatalf("runtime_parity = %#v, want one partial CacheManager mapping", result.ProtocolCompat.RuntimeParity)
	}
}

func TestHandleImportJMeterUsesCSVSidecarHeaders(t *testing.T) {
	server := NewServer(ServerConfig{})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "checkout.jmx")
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	if _, err := part.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan"/>
    <hashTree>
      <ThreadGroup testname="Users"/>
      <hashTree>
        <CSVDataSet testname="Customers">
          <stringProp name="filename">data/customers.csv</stringProp>
          <stringProp name="delimiter">,</stringProp>
        </CSVDataSet>
        <hashTree/>
        <HTTPSamplerProxy testname="Lookup">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/customers/${customer_id}</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`)); err != nil {
		t.Fatalf("Write JMX returned error: %v", err)
	}
	csvPart, err := writer.CreateFormFile("data_file", "customers.csv")
	if err != nil {
		t.Fatalf("CreateFormFile(data_file) returned error: %v", err)
	}
	if _, err := csvPart.Write([]byte("customer_id,zip\n42,02139\n")); err != nil {
		t.Fatalf("Write CSV returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Scripts []struct {
			DataSources []struct {
				Columns        []string `json:"columns"`
				FirstRowHeader bool     `json:"first_row_header"`
				InlineData     string   `json:"inline_data"`
			} `json:"data_sources"`
		} `json:"scripts"`
		ProtocolCompat struct {
			DataSources int `json:"data_sources"`
		} `json:"protocol_compat_report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].DataSources) != 1 {
		t.Fatalf("scripts = %#v, want one script with one data source", result.Scripts)
	}
	source := result.Scripts[0].DataSources[0]
	if !source.FirstRowHeader || strings.Join(source.Columns, ",") != "customer_id,zip" || source.InlineData == "" {
		t.Fatalf("data source = %#v, want sidecar-inferred header columns", source)
	}
	if result.ProtocolCompat.DataSources != 1 {
		t.Fatalf("protocol_compat_report.data_sources = %d, want 1", result.ProtocolCompat.DataSources)
	}
}

func TestHandleImportJMeterZipUsesJMXAndCSVSidecars(t *testing.T) {
	server := NewServer(ServerConfig{})
	var zipBody bytes.Buffer
	writer := zip.NewWriter(&zipBody)
	addFile := func(name string, content string) {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatalf("Create(%q) returned error: %v", name, err)
		}
		if _, err := entry.Write([]byte(content)); err != nil {
			t.Fatalf("Write(%q) returned error: %v", name, err)
		}
	}
	addFile("AOS/checkout.jmx", `<?xml version="1.0" encoding="UTF-8"?>
<jmeterTestPlan version="1.2">
  <hashTree>
    <TestPlan testname="Plan"/>
    <hashTree>
      <ThreadGroup testname="Users"/>
      <hashTree>
        <CSVDataSet testname="Customers">
          <stringProp name="filename">customers.csv</stringProp>
        </CSVDataSet>
        <hashTree/>
        <HTTPSamplerProxy testname="Lookup">
          <stringProp name="HTTPSampler.domain">api.example.com</stringProp>
          <stringProp name="HTTPSampler.protocol">https</stringProp>
          <stringProp name="HTTPSampler.path">/customers/${customer_id}</stringProp>
          <stringProp name="HTTPSampler.method">GET</stringProp>
        </HTTPSamplerProxy>
        <hashTree/>
      </hashTree>
    </hashTree>
  </hashTree>
</jmeterTestPlan>`)
	addFile("AOS/customers.csv", "customer_id,zip\n42,02139\n")
	if err := writer.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/jmeter?filename=aos.zip", bytes.NewReader(zipBody.Bytes()))
	req.Header.Set("Content-Type", "application/zip")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var result struct {
		Scripts []struct {
			Actions []struct {
				Target string `json:"target"`
			} `json:"actions"`
			DataSources []struct {
				Path           string   `json:"path"`
				Columns        []string `json:"columns"`
				FirstRowHeader bool     `json:"first_row_header"`
			} `json:"data_sources"`
		} `json:"scripts"`
		Summary struct {
			FileName string `json:"file_name"`
		} `json:"summary"`
		ProtocolCompat struct {
			DataSources int `json:"data_sources"`
		} `json:"protocol_compat_report"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if result.Summary.FileName != "AOS/checkout.jmx" {
		t.Fatalf("summary.file_name = %q, want selected inner JMX", result.Summary.FileName)
	}
	if len(result.Scripts) != 1 || len(result.Scripts[0].Actions) != 1 || result.Scripts[0].Actions[0].Target != "/customers/{{var.customer_id}}" {
		t.Fatalf("scripts = %#v, want converted JMX request", result.Scripts)
	}
	source := result.Scripts[0].DataSources[0]
	if source.Path != "customers.csv" || !source.FirstRowHeader || strings.Join(source.Columns, ",") != "customer_id,zip" {
		t.Fatalf("data source = %#v, want CSV sidecar header inference", source)
	}
	if result.ProtocolCompat.DataSources != 1 {
		t.Fatalf("protocol_compat_report.data_sources = %d, want 1", result.ProtocolCompat.DataSources)
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
		TrueClientScriptTypes []struct {
			CanonicalName string   `json:"CanonicalName"`
			DisplayName   string   `json:"DisplayName"`
			Status        string   `json:"Status"`
			Inputs        []string `json:"SupportedInputs"`
		} `json:"trueclient_script_types"`
		Web2ProfileCapabilities []struct {
			CanonicalName string `json:"CanonicalName"`
			DisplayName   string `json:"DisplayName"`
			Status        string `json:"Status"`
		} `json:"web2_profile_capabilities"`
		Web2FixtureContract []struct {
			ID       string `json:"ID"`
			Required bool   `json:"Required"`
			Status   string `json:"Status"`
		} `json:"web2_fixture_contract"`
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
	if containsLoadRunnerSupportMatrixStatus(result.SupportMatrix, "partial") {
		t.Fatalf("expected no partial loadrunner support matrix entries after RTS parity reconciliation, got %#v", result.SupportMatrix)
	}
	if !containsLoadRunnerSupportMatrixStatus(result.SupportMatrix, "compatibility_only") {
		t.Fatalf("expected compatibility-only loadrunner support matrix entries, got %#v", result.SupportMatrix)
	}
	if len(result.Notes) == 0 {
		t.Fatal("expected loadrunner support matrix notes")
	}
	if len(result.TestedExamples) == 0 || result.TestedExamples[0].Title == "" || result.TestedExamples[0].Test == "" {
		t.Fatalf("tested_examples = %#v, want populated tested examples", result.TestedExamples)
	}
	if !containsLoadRunnerSupportMatrixExample(result.TestedExamples, "Golden Web HTTP/HTML fixture") {
		t.Fatalf("tested_examples = %#v, want golden fixture example", result.TestedExamples)
	}
	if !containsLoadRunnerTrueClientScriptType(result.TrueClientScriptTypes, "truclient_mobile_web", "supported") {
		t.Fatalf("trueclient_script_types = %#v, want active Mobile Web row", result.TrueClientScriptTypes)
	}
	if !containsLoadRunnerTrueClientScriptType(result.TrueClientScriptTypes, "truclient2_web", "review_only") {
		t.Fatalf("trueclient_script_types = %#v, want review-only Web 2.0 row", result.TrueClientScriptTypes)
	}
	if !containsLoadRunnerTrueClientCapability(result.Web2ProfileCapabilities, "object_identification", "review_only") {
		t.Fatalf("web2_profile_capabilities = %#v, want review-only object identification capability", result.Web2ProfileCapabilities)
	}
	if !containsLoadRunnerWeb2Fixture(result.Web2FixtureContract, "web2_spa_checkout", true, "supported") {
		t.Fatalf("web2_fixture_contract = %#v, want required SPA checkout fixture", result.Web2FixtureContract)
	}
}

func TestAuthoringCapabilitiesEndpoint(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/capabilities", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload AuthoringCapabilityResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.ContractVersion != "authoring.v1" {
		t.Fatalf("contract version = %q, want authoring.v1", payload.ContractVersion)
	}
	if len(payload.Sources) < 9 {
		t.Fatalf("sources = %d, want source matrix entries", len(payload.Sources))
	}
	loadRunner, ok := payload.SourceMap["loadrunner"]
	if !ok {
		t.Fatal("expected loadrunner source map entry")
	}
	if !loadRunner.ImportReview || !loadRunner.RepairSuggestions || !loadRunner.SaveHandoff {
		t.Fatalf("loadrunner features = %#v, want shared review/repair/save lifecycle", loadRunner)
	}
	jmeter, ok := payload.SourceMap["jmeter"]
	if !ok {
		t.Fatal("expected jmeter source map entry")
	}
	if !jmeter.MultiScript || !jmeter.ScenarioDraft {
		t.Fatalf("jmeter features = %#v, want multi-script scenario draft support", jmeter)
	}
	aiGenerated, ok := payload.SourceMap["ai_generated"]
	if !ok {
		t.Fatal("expected ai_generated source map entry")
	}
	if !aiGenerated.Provenance || !aiGenerated.ApprovalGateCandidate {
		t.Fatalf("ai generated features = %#v, want provenance and approval gate candidate", aiGenerated)
	}
	web2, ok := payload.SourceMap["trueclient2_web"]
	if !ok {
		t.Fatal("expected trueclient2_web source map entry")
	}
	if !web2.BrowserReplay || !web2.ApprovalGateCandidate {
		t.Fatalf("trueclient2_web features = %#v, want review-only browser replay gated profile", web2)
	}
	if !containsAuthoringSourceStatus(payload.Sources, "trueclient2_web", "review_only") {
		t.Fatalf("sources = %#v, want trueclient2_web review_only source", payload.Sources)
	}
	vugenWeb2, ok := payload.SourceMap["vugen_web2"]
	if !ok {
		t.Fatal("expected vugen_web2 source map entry")
	}
	if !vugenWeb2.BrowserReplay || !vugenWeb2.ScenarioDraft || !vugenWeb2.ApprovalGateCandidate {
		t.Fatalf("vugen_web2 features = %#v, want replay, scenario draft, and approval-gated profile", vugenWeb2)
	}
	if !containsAuthoringSourceStatus(payload.Sources, "vugen_web2", "review_only") {
		t.Fatalf("sources = %#v, want vugen_web2 review_only source", payload.Sources)
	}
}

func TestAuthoringTemplatesEndpointReturnsValidProtocolStarters(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/templates?protocol=http", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Templates []AuthoringTemplate `json:"templates"`
		Count     int                 `json:"count"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Count != 1 || payload.Templates[0].Protocol != "http" {
		t.Fatalf("payload = %#v, want one HTTP template", payload)
	}
	if payload.Templates[0].RecommendedScenario.VUsers == 0 || payload.Templates[0].RecommendedScenario.Duration == "" {
		t.Fatalf("recommended scenario = %#v, want runnable scenario hints", payload.Templates[0].RecommendedScenario)
	}
	allReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/templates", nil)
	allResp := httptest.NewRecorder()
	server.mux.ServeHTTP(allResp, allReq)
	if allResp.Code != http.StatusOK {
		t.Fatalf("all templates status = %d, want %d body=%s", allResp.Code, http.StatusOK, allResp.Body.String())
	}
	var allPayload struct {
		Templates []AuthoringTemplate `json:"templates"`
		Count     int                 `json:"count"`
	}
	if err := json.Unmarshal(allResp.Body.Bytes(), &allPayload); err != nil {
		t.Fatalf("unmarshal all response: %v", err)
	}
	wantProtocols := map[string]bool{
		"http": true, "browser": true, "soap": true, "java_http": true, "websocket": true,
		"grpc": true, "jms-solace": true, "jmeter": true, "har": true, "devweb": true,
	}
	for _, template := range allPayload.Templates {
		delete(wantProtocols, template.Protocol)
	}
	if len(wantProtocols) != 0 {
		t.Fatalf("missing template protocols = %#v", wantProtocols)
	}
	for _, template := range authoringTemplateCatalog() {
		if issues := validateScript(&template.Script); len(issues) != 0 {
			t.Fatalf("template %s validation issues = %v", template.ID, issues)
		}
	}
	web2Req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/templates?protocol=browser&use_case=migration_review", nil)
	web2Resp := httptest.NewRecorder()
	server.mux.ServeHTTP(web2Resp, web2Req)
	if web2Resp.Code != http.StatusOK {
		t.Fatalf("web2 template status = %d, want %d body=%s", web2Resp.Code, http.StatusOK, web2Resp.Body.String())
	}
	var web2Payload struct {
		Templates []AuthoringTemplate `json:"templates"`
		Count     int                 `json:"count"`
	}
	if err := json.Unmarshal(web2Resp.Body.Bytes(), &web2Payload); err != nil {
		t.Fatalf("unmarshal web2 response: %v", err)
	}
	if web2Payload.Count != 1 || web2Payload.Templates[0].ID != "vugen-web2-review-profile" {
		t.Fatalf("web2 payload = %#v, want VuGen Web 2.0 review profile", web2Payload)
	}
}

func TestAuthoringComponentsEndpointReturnsReusableLibrary(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/components", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Components []AuthoringComponent `json:"components"`
		Count      int                  `json:"count"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Count != len(payload.Components) {
		t.Fatalf("count = %d, components = %d", payload.Count, len(payload.Components))
	}
	wantKinds := map[string]bool{
		"request_group":     true,
		"auth_flow":         true,
		"login_transaction": true,
		"data_pool":         true,
		"teardown":          true,
	}
	for _, component := range payload.Components {
		delete(wantKinds, component.Kind)
	}
	if len(wantKinds) != 0 {
		t.Fatalf("missing component kinds = %#v", wantKinds)
	}

	filterReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/components?kind=auth_flow&protocol=http", nil)
	filterResp := httptest.NewRecorder()
	server.mux.ServeHTTP(filterResp, filterReq)
	if filterResp.Code != http.StatusOK {
		t.Fatalf("filtered status = %d, want %d body=%s", filterResp.Code, http.StatusOK, filterResp.Body.String())
	}
	var filtered struct {
		Components []AuthoringComponent `json:"components"`
		Count      int                  `json:"count"`
	}
	if err := json.Unmarshal(filterResp.Body.Bytes(), &filtered); err != nil {
		t.Fatalf("unmarshal filtered response: %v", err)
	}
	if filtered.Count == 0 {
		t.Fatal("expected at least one HTTP auth flow component")
	}
	for _, component := range filtered.Components {
		if component.Kind != "auth_flow" || !componentSupportsProtocol(component, "http") {
			t.Fatalf("filtered component = %#v, want HTTP auth flow only", component)
		}
	}
	web2Req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/components?protocol=trueclient2_web", nil)
	web2Resp := httptest.NewRecorder()
	server.mux.ServeHTTP(web2Resp, web2Req)
	if web2Resp.Code != http.StatusOK {
		t.Fatalf("web2 components status = %d, want %d body=%s", web2Resp.Code, http.StatusOK, web2Resp.Body.String())
	}
	var web2Payload struct {
		Components []AuthoringComponent `json:"components"`
		Count      int                  `json:"count"`
	}
	if err := json.Unmarshal(web2Resp.Body.Bytes(), &web2Payload); err != nil {
		t.Fatalf("unmarshal web2 components: %v", err)
	}
	if web2Payload.Count == 0 || !componentSupportsProtocol(web2Payload.Components[0], "trueclient2_web") {
		t.Fatalf("web2 components = %#v, want reusable TrueClient 2.0 Web component", web2Payload)
	}
	wantWeb2Components := map[string]bool{
		"browser-login-transaction":    true,
		"web2-search-flow":             true,
		"web2-add-to-cart-flow":        true,
		"web2-checkout-flow":           true,
		"web2-iframe-flow":             true,
		"web2-modal-flow":              true,
		"web2-auth-callback-flow":      true,
		"web2-browser-logout-teardown": true,
	}
	for _, component := range web2Payload.Components {
		delete(wantWeb2Components, component.ID)
		if !componentSupportsProtocol(component, "trueclient2_web") {
			t.Fatalf("web2 component = %#v, want trueclient2_web support", component)
		}
	}
	if len(wantWeb2Components) != 0 {
		t.Fatalf("missing web2 components = %#v", wantWeb2Components)
	}
}

func TestDraftValidationReturnsBlockedAuthoringHealth(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body := []byte(`{"script":{"name":"","version":"1.0.0","protocol":"http","actions":[]}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/validate", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload scriptValidationResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Valid {
		t.Fatal("expected invalid draft")
	}
	if payload.AuthoringHealth.Status != "blocked" {
		t.Fatalf("authoring health status = %q, want blocked", payload.AuthoringHealth.Status)
	}
	if payload.AuthoringHealth.Score >= 60 {
		t.Fatalf("authoring health score = %d, want below 60", payload.AuthoringHealth.Score)
	}
}

func TestDraftValidationReturnsReplayPreflightWithSeededVariables(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body := []byte(`{
		"variables":{"account_id":"acct-123"},
		"script":{
			"name":"debug-preflight",
			"version":"1.0.0",
			"protocol":"http",
			"config":{"base_url":"https://example.test"},
			"actions":[{"name":"Account","type":"request","target":"/accounts/{{var.account_id}}"}]
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/validate", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload scriptValidationResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if payload.Preflight == nil {
		t.Fatal("expected validation response to include replay preflight")
	}
	if !payload.Preflight.Valid {
		t.Fatalf("preflight valid = false, missing variables=%v warnings=%v issues=%v", payload.Preflight.MissingVariables, payload.Preflight.Warnings, payload.Preflight.Issues)
	}
	if payload.Preflight.ActionCount != 1 {
		t.Fatalf("action count = %d, want 1", payload.Preflight.ActionCount)
	}
}

func TestDraftValidationReturnsDataSourceFitHealth(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body := []byte(`{"script":{
		"name":"data-fit",
		"version":"1.0.0",
		"protocol":"http",
		"goal_config":{"max_vusers":2},
		"config":{"base_url":"https://example.test","run_logic":{"iterations":3}},
		"data_sources":[{"name":"users","type":"csv","strategy":"unique","inline_data":"username\nalice\nbob\n","first_row_header":true}],
		"parameters":[{"name":"user","type":"file","data_source":"users","column":"username","selection":"unique","on_exhaustion":"error"},{"name":"missing","type":"file","data_source":"users","column":"password"}],
		"actions":[{"name":"Home","type":"request","target":"/"}]
	}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/validate", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload scriptValidationResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !payload.Valid {
		t.Fatalf("valid = false, want structural validation to pass; issues=%v", payload.Issues)
	}
	if payload.DataSourceFit == nil || payload.DataSourceFit.Status != "blocked" {
		t.Fatalf("data_source_fit = %#v, want blocked fit report", payload.DataSourceFit)
	}
	if payload.DataSourceFit.StatusCounts["blocked"] < 2 {
		t.Fatalf("status counts = %#v, want missing column and row-count blockers", payload.DataSourceFit.StatusCounts)
	}
	if payload.AuthoringHealth.Status != "blocked" {
		t.Fatalf("authoring health = %#v, want blocked from data-source fit", payload.AuthoringHealth)
	}
}

func TestDraftValidationDataSourceFitUsesAutoDelimiter(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	body := []byte(`{"script":{
		"name":"data-fit-auto",
		"version":"1.0.0",
		"protocol":"http",
		"config":{"base_url":"https://example.test"},
		"data_sources":[{"name":"users","type":"csv","strategy":"sequential","delimiter":"auto","inline_data":"username\tpassword\nalice\tpass1\nbob\tpass2\n","first_row_header":true}],
		"parameters":[{"name":"password","type":"file","data_source":"users","column":"password","selection":"sequential"}],
		"actions":[{"name":"Home","type":"request","target":"/"}]
	}}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/validate", bytes.NewReader(body))
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload scriptValidationResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if !payload.Valid {
		t.Fatalf("valid = false, want structural validation to pass; issues=%v", payload.Issues)
	}
	if payload.DataSourceFit == nil || payload.DataSourceFit.Status != "ready" {
		t.Fatalf("data_source_fit = %#v, want ready fit report", payload.DataSourceFit)
	}
}

func TestDraftValidationReturnsAIGuardrails(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	blockedBody := []byte(`{"script":{
		"name":"ai draft",
		"version":"1.0.0",
		"protocol":"http",
		"generated_by":"openai",
		"generated_from":"ai_generated",
		"config":{"base_url":"https://example.test"},
		"actions":[{"name":"Home","type":"request","target":"/"}]
	}}`)
	blockedReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/validate", bytes.NewReader(blockedBody))
	blockedResp := httptest.NewRecorder()
	server.mux.ServeHTTP(blockedResp, blockedReq)
	if blockedResp.Code != http.StatusOK {
		t.Fatalf("blocked status = %d, want %d body=%s", blockedResp.Code, http.StatusOK, blockedResp.Body.String())
	}
	var blocked scriptValidationResponse
	if err := json.Unmarshal(blockedResp.Body.Bytes(), &blocked); err != nil {
		t.Fatalf("unmarshal blocked response: %v", err)
	}
	if blocked.AIGuardrails == nil || blocked.AIGuardrails.Status != "blocked" {
		t.Fatalf("ai_guardrails = %#v, want blocked", blocked.AIGuardrails)
	}
	if blocked.AuthoringHealth.Status != "blocked" {
		t.Fatalf("authoring health = %#v, want blocked from AI guardrails", blocked.AuthoringHealth)
	}

	readyBody := []byte(`{"script":{
		"name":"ai draft",
		"version":"1.0.0",
		"protocol":"http",
		"generated_by":"openai",
		"generated_from":"ai_generated",
		"metadata":{"import":{"source":"ai_generated","preserved":{"diff_reviewed":true,"replay_status":"passed"}}},
		"config":{"base_url":"https://example.test"},
		"actions":[{"name":"Home","type":"request","target":"/"}]
	}}`)
	readyReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/validate", bytes.NewReader(readyBody))
	readyResp := httptest.NewRecorder()
	server.mux.ServeHTTP(readyResp, readyReq)
	if readyResp.Code != http.StatusOK {
		t.Fatalf("ready status = %d, want %d body=%s", readyResp.Code, http.StatusOK, readyResp.Body.String())
	}
	var ready scriptValidationResponse
	if err := json.Unmarshal(readyResp.Body.Bytes(), &ready); err != nil {
		t.Fatalf("unmarshal ready response: %v", err)
	}
	if ready.AIGuardrails == nil || ready.AIGuardrails.Status != "ready" {
		t.Fatalf("ai_guardrails = %#v, want ready", ready.AIGuardrails)
	}
	if ready.AuthoringHealth.Status == "blocked" {
		t.Fatalf("authoring health = %#v, want promotable AI draft", ready.AuthoringHealth)
	}
}

func TestAuthoringSessionLifecyclePersistsDraftReviewState(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	body := []byte(`{
		"project_id":"project-authoring",
		"source":"loadrunner",
		"name":"Checkout repair",
		"status":"reviewing",
		"draft":{"name":"Checkout","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"Home","type":"request","target":"/"}]},
		"review_state":{"active_tab":"runtime","pending_repairs":["correlation"]}
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created session: %v", err)
	}
	if created.ID == "" || created.ProjectID != "project-authoring" || created.Source != "loadrunner" {
		t.Fatalf("created session = %#v", created)
	}
	if created.Draft == nil || created.Draft.ProjectID != "project-authoring" {
		t.Fatalf("created draft = %#v, want project-bound draft", created.Draft)
	}
	if !created.ApprovalReady || len(created.NextActions) == 0 {
		t.Fatalf("created readiness = ready %v actions %#v, want promotable next action", created.ApprovalReady, created.NextActions)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/authoring/sessions?project_id=project-authoring", nil)
	listResp := httptest.NewRecorder()
	server.mux.ServeHTTP(listResp, listReq)
	if listResp.Code != http.StatusOK {
		t.Fatalf("list status = %d, want %d body=%s", listResp.Code, http.StatusOK, listResp.Body.String())
	}
	var listed struct {
		Sessions []*storage.AuthoringSession `json:"sessions"`
		Count    int                         `json:"count"`
	}
	if err := json.Unmarshal(listResp.Body.Bytes(), &listed); err != nil {
		t.Fatalf("unmarshal listed sessions: %v", err)
	}
	if listed.Count != 1 || listed.Sessions[0].ID != created.ID {
		t.Fatalf("listed sessions = %#v, want created session", listed)
	}
	if !listed.Sessions[0].ApprovalReady || len(listed.Sessions[0].NextActions) == 0 {
		t.Fatalf("listed readiness = ready %v actions %#v, want computed readiness", listed.Sessions[0].ApprovalReady, listed.Sessions[0].NextActions)
	}

	updateBody := []byte(`{
		"project_id":"project-authoring",
		"source":"loadrunner",
		"name":"Checkout repair updated",
		"status":"editing",
		"draft":{"name":"Checkout Updated","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"Cart","type":"request","target":"/cart"}]},
		"review_state":{"active_tab":"parameters"}
	}`)
	updateReq := httptest.NewRequest(http.MethodPut, "/api/v1/scripts/authoring/sessions/"+created.ID, bytes.NewReader(updateBody))
	updateResp := httptest.NewRecorder()
	server.mux.ServeHTTP(updateResp, updateReq)
	if updateResp.Code != http.StatusOK {
		t.Fatalf("update status = %d, want %d body=%s", updateResp.Code, http.StatusOK, updateResp.Body.String())
	}
	var updated storage.AuthoringSession
	if err := json.Unmarshal(updateResp.Body.Bytes(), &updated); err != nil {
		t.Fatalf("unmarshal updated session: %v", err)
	}
	if updated.Status != "editing" || updated.Draft == nil || updated.Draft.Name != "Checkout Updated" || updated.ReviewState["active_tab"] != "parameters" {
		t.Fatalf("updated session = %#v", updated)
	}

	deleteReq := httptest.NewRequest(http.MethodDelete, "/api/v1/scripts/authoring/sessions/"+created.ID, nil)
	deleteResp := httptest.NewRecorder()
	server.mux.ServeHTTP(deleteResp, deleteReq)
	if deleteResp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want %d body=%s", deleteResp.Code, http.StatusOK, deleteResp.Body.String())
	}
}

func TestApproveAuthoringSessionPromotesDraftToScriptLibrary(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	body := []byte(`{
		"project_id":"project-authoring",
		"source":"loadrunner",
		"name":"Checkout approval",
		"status":"ready",
		"draft":{"name":"Checkout Approved","version":"1.0.0","protocol":"http","config":{"base_url":"https://example.test"},"actions":[{"name":"Home","type":"request","target":"/"}]},
		"review_state":{"reviewer":"qa-lead"}
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created session: %v", err)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+created.ID+"/approve", nil)
	approveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approveResp, approveReq)
	if approveResp.Code != http.StatusOK {
		t.Fatalf("approve status = %d, want %d body=%s", approveResp.Code, http.StatusOK, approveResp.Body.String())
	}
	var approved authoringSessionApprovalResponse
	if err := json.Unmarshal(approveResp.Body.Bytes(), &approved); err != nil {
		t.Fatalf("unmarshal approval response: %v", err)
	}
	if approved.Script == nil || approved.Script.ID == "" || approved.Script.ProjectID != "project-authoring" {
		t.Fatalf("approved script = %#v, want project-bound stored script", approved.Script)
	}
	if approved.Session == nil || approved.Session.Status != "approved" {
		t.Fatalf("approved session = %#v, want approved status", approved.Session)
	}
	if approved.Session.ApprovalReady {
		t.Fatalf("approved session readiness = %#v, want no further approval", approved.Session)
	}
	if approved.Session.ReviewState["approved_script_id"] != approved.Script.ID {
		t.Fatalf("review state = %#v, want approved script id %q", approved.Session.ReviewState, approved.Script.ID)
	}
	if approved.AuthoringHealth.Status == "blocked" {
		t.Fatalf("authoring health = %#v, want promotable health", approved.AuthoringHealth)
	}
	if _, ok := server.runner.GetScript(approved.Script.ID); !ok {
		t.Fatalf("script %q was not stored in reusable library", approved.Script.ID)
	}

	persisted, err := store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession returned error: %v", err)
	}
	if persisted.Status != "approved" || persisted.ReviewState["approved_script_id"] != approved.Script.ID {
		t.Fatalf("persisted session = %#v, want approval state", persisted)
	}
}

func TestApproveAuthoringSessionRejectsBlockedDraft(t *testing.T) {
	server := NewServer(ServerConfig{})
	body := []byte(`{
		"project_id":"project-authoring",
		"source":"loadrunner",
		"name":"Broken approval",
		"status":"ready",
		"draft":{"name":"","version":"1.0.0","protocol":"http","actions":[]}
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created session: %v", err)
	}

	approveReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+created.ID+"/approve", nil)
	approveResp := httptest.NewRecorder()
	server.mux.ServeHTTP(approveResp, approveReq)
	if approveResp.Code != http.StatusBadRequest {
		t.Fatalf("approve status = %d, want %d body=%s", approveResp.Code, http.StatusBadRequest, approveResp.Body.String())
	}
	var validation scriptValidationResponse
	if err := json.Unmarshal(approveResp.Body.Bytes(), &validation); err != nil {
		t.Fatalf("unmarshal validation response: %v", err)
	}
	if validation.Valid || validation.AuthoringHealth.Status != "blocked" {
		t.Fatalf("validation = %#v, want blocked invalid draft", validation)
	}
	sessions, err := server.listAuthoringSessions("project-authoring")
	if err != nil {
		t.Fatalf("listAuthoringSessions returned error: %v", err)
	}
	enrichAuthoringSessions(sessions)
	if len(sessions) != 1 || sessions[0].Status != "ready" {
		t.Fatalf("sessions = %#v, want original ready session without approval", sessions)
	}
	if sessions[0].ApprovalReady || len(sessions[0].BlockingHealthInputs) == 0 || len(sessions[0].NextActions) == 0 {
		t.Fatalf("session readiness = ready %v blockers %#v actions %#v, want blocked approval guidance", sessions[0].ApprovalReady, sessions[0].BlockingHealthInputs, sessions[0].NextActions)
	}
}

func TestPreviewAuthoringCorrelationReturnsDiffWithoutPersisting(t *testing.T) {
	server := NewServer(ServerConfig{})
	body := []byte(`{
		"project_id":"project-authoring",
		"source":"har",
		"name":"Correlation preview",
		"status":"reviewing",
		"draft":{
			"name":"Correlated Flow",
			"version":"1.0.0",
			"protocol":"http",
			"config":{"base_url":"https://example.test"},
			"actions":[
				{"name":"Login","type":"request","method":"POST","target":"/login"},
				{"name":"Cart","type":"request","method":"GET","target":"/cart?session=abc12345","headers":{"X-Session":"abc12345"}}
			]
		}
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created session: %v", err)
	}

	previewBody := []byte(`{
		"name":"corr_session",
		"source":"body",
		"type":"jsonpath",
		"expression":"$.sessionId",
		"sample_value":"abc12345",
		"found_action_index":1
	}`)
	previewReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+created.ID+"/correlations/preview", bytes.NewReader(previewBody))
	previewResp := httptest.NewRecorder()
	server.mux.ServeHTTP(previewResp, previewReq)
	if previewResp.Code != http.StatusOK {
		t.Fatalf("preview status = %d, want %d body=%s", previewResp.Code, http.StatusOK, previewResp.Body.String())
	}
	var preview authoringCorrelationPreviewResponse
	if err := json.Unmarshal(previewResp.Body.Bytes(), &preview); err != nil {
		t.Fatalf("unmarshal preview response: %v", err)
	}
	if preview.Applied {
		t.Fatal("preview applied = true, want false")
	}
	if preview.Draft.Actions[0].Extractors[0].Name != "corr_session" {
		t.Fatalf("preview extractors = %#v, want corr_session on source action", preview.Draft.Actions[0].Extractors)
	}
	if preview.Draft.Actions[1].Target != "/cart?session={{var.corr_session}}" || preview.Draft.Actions[1].Headers["X-Session"] != "{{var.corr_session}}" {
		t.Fatalf("preview usage action = %#v, want variable replacements", preview.Draft.Actions[1])
	}
	if len(preview.Changes) != 3 {
		t.Fatalf("changes = %#v, want extractor plus target/header diffs", preview.Changes)
	}

	loaded, ok := server.getAuthoringSession(httptest.NewRecorder(), created.ID)
	if !ok {
		t.Fatal("expected persisted authoring session")
	}
	if len(loaded.Draft.Actions[0].Extractors) != 0 || loaded.Draft.Actions[1].Target != "/cart?session=abc12345" {
		t.Fatalf("persisted draft = %#v, want unchanged after preview", loaded.Draft)
	}
}

func TestApplyAuthoringCorrelationPersistsDraftAndReviewState(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	body := []byte(`{
		"project_id":"project-authoring",
		"source":"har",
		"name":"Correlation apply",
		"status":"reviewing",
		"draft":{
			"name":"Correlated Flow",
			"version":"1.0.0",
			"protocol":"http",
			"config":{"base_url":"https://example.test"},
			"actions":[
				{"name":"Login","type":"request","method":"POST","target":"/login"},
				{"name":"Cart","type":"request","method":"GET","target":"/cart?session=abc12345"}
			]
		}
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created session: %v", err)
	}

	applyBody := []byte(`{
		"name":"corr_session",
		"expression":"$.sessionId",
		"sample_value":"abc12345",
		"found_action":"Login",
		"apply":true
	}`)
	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+created.ID+"/correlations/preview", bytes.NewReader(applyBody))
	applyResp := httptest.NewRecorder()
	server.mux.ServeHTTP(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("apply status = %d, want %d body=%s", applyResp.Code, http.StatusOK, applyResp.Body.String())
	}
	var applied authoringCorrelationPreviewResponse
	if err := json.Unmarshal(applyResp.Body.Bytes(), &applied); err != nil {
		t.Fatalf("unmarshal apply response: %v", err)
	}
	if !applied.Applied {
		t.Fatal("applied = false, want true")
	}

	persisted, err := store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession returned error: %v", err)
	}
	if persisted.Status != "editing" {
		t.Fatalf("status = %q, want editing", persisted.Status)
	}
	if persisted.Draft.Actions[0].Extractors[0].Name != "corr_session" || persisted.Draft.Actions[1].Target != "/cart?session={{var.corr_session}}" {
		t.Fatalf("persisted draft = %#v, want applied correlation", persisted.Draft)
	}
	lastPreview, ok := persisted.ReviewState["last_correlation_preview"].(map[string]any)
	if !ok || lastPreview["name"] != "corr_session" {
		t.Fatalf("review state = %#v, want last correlation preview metadata", persisted.ReviewState)
	}
}

func TestBatchAuthoringCorrelationAppliesHighConfidenceAndBlocksMediumRisk(t *testing.T) {
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()
	server := NewServer(ServerConfig{Store: store})
	body := []byte(`{
		"project_id":"project-authoring",
		"source":"loadrunner",
		"name":"Correlation batch",
		"status":"reviewing",
		"draft":{
			"name":"Correlated Flow",
			"version":"1.0.0",
			"protocol":"http",
			"config":{"base_url":"https://example.test"},
			"actions":[
				{"name":"Login","type":"request","method":"POST","target":"/login"},
				{"name":"Cart","type":"request","method":"GET","target":"/cart?csrf=csrf-high-123456&order=order-medium-123456"}
			]
		}
	}`)
	createReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions", bytes.NewReader(body))
	createResp := httptest.NewRecorder()
	server.mux.ServeHTTP(createResp, createReq)
	if createResp.Code != http.StatusCreated {
		t.Fatalf("create status = %d, want %d body=%s", createResp.Code, http.StatusCreated, createResp.Body.String())
	}
	var created storage.AuthoringSession
	if err := json.Unmarshal(createResp.Body.Bytes(), &created); err != nil {
		t.Fatalf("unmarshal created session: %v", err)
	}

	applyBody := []byte(`{
		"apply": true,
		"auto_apply_high_confidence": true,
		"repairs": [
			{"name":"corr_csrf","expression":"$.csrf","sample_value":"csrf-high-123456","found_action_index":1,"used_action_indexes":[2],"confidence":0.91,"severity":"error"},
			{"name":"corr_order","expression":"$.order","sample_value":"order-medium-123456","found_action_index":1,"used_action_indexes":[2],"confidence":0.62,"severity":"error"}
		]
	}`)
	applyReq := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/authoring/sessions/"+created.ID+"/correlations/preview", bytes.NewReader(applyBody))
	applyResp := httptest.NewRecorder()
	server.mux.ServeHTTP(applyResp, applyReq)
	if applyResp.Code != http.StatusOK {
		t.Fatalf("batch apply status = %d, want %d body=%s", applyResp.Code, http.StatusOK, applyResp.Body.String())
	}
	var applied authoringCorrelationPreviewResponse
	if err := json.Unmarshal(applyResp.Body.Bytes(), &applied); err != nil {
		t.Fatalf("unmarshal batch apply response: %v", err)
	}
	if !applied.Applied || applied.AppliedCount != 1 || !applied.Blocked {
		t.Fatalf("batch response = %#v, want one applied and blocked medium risk", applied)
	}
	if len(applied.ReviewRequired) != 1 || applied.ReviewRequired[0] != "corr_order" {
		t.Fatalf("review_required = %#v, want corr_order", applied.ReviewRequired)
	}
	persisted, err := store.GetAuthoringSession(created.ID)
	if err != nil {
		t.Fatalf("GetAuthoringSession returned error: %v", err)
	}
	target := persisted.Draft.Actions[1].Target
	if !strings.Contains(target, "{{var.corr_csrf}}") || strings.Contains(target, "{{var.corr_order}}") {
		t.Fatalf("target = %q, want only high-confidence repair applied", target)
	}
	state, ok := persisted.ReviewState["last_correlation_batch_preview"].(map[string]any)
	if !ok || state["blocked"] != true {
		t.Fatalf("review state = %#v, want blocked batch metadata", persisted.ReviewState)
	}
}

type reviewSection struct {
	Key      string   `json:"key"`
	Severity string   `json:"severity"`
	Summary  string   `json:"summary"`
	Items    []string `json:"items"`
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

func containsLoadRunnerSupportMatrixExample(entries []struct {
	Title    string `json:"title"`
	Coverage string `json:"coverage"`
	Test     string `json:"test"`
}, want string) bool {
	for _, entry := range entries {
		if entry.Title == want {
			return true
		}
	}
	return false
}

func containsLoadRunnerTrueClientScriptType(entries []struct {
	CanonicalName string   `json:"CanonicalName"`
	DisplayName   string   `json:"DisplayName"`
	Status        string   `json:"Status"`
	Inputs        []string `json:"SupportedInputs"`
}, name string, status string) bool {
	for _, entry := range entries {
		if entry.CanonicalName == name && entry.Status == status && entry.DisplayName != "" && len(entry.Inputs) > 0 {
			return true
		}
	}
	return false
}

func containsLoadRunnerTrueClientCapability(entries []struct {
	CanonicalName string `json:"CanonicalName"`
	DisplayName   string `json:"DisplayName"`
	Status        string `json:"Status"`
}, name string, status string) bool {
	for _, entry := range entries {
		if entry.CanonicalName == name && entry.Status == status && entry.DisplayName != "" {
			return true
		}
	}
	return false
}

func containsLoadRunnerWeb2Fixture(entries []struct {
	ID       string `json:"ID"`
	Required bool   `json:"Required"`
	Status   string `json:"Status"`
}, id string, required bool, status string) bool {
	for _, entry := range entries {
		if entry.ID == id && entry.Required == required && entry.Status == status {
			return true
		}
	}
	return false
}

func containsAuthoringSourceStatus(sources []AuthoringSourceCapability, source string, status string) bool {
	for _, item := range sources {
		if item.Source == source && item.Status == status {
			return true
		}
	}
	return false
}

func loadRunnerFixtureMultipartBody(t *testing.T, fixtureDir string, archiveName string, fields map[string]string) ([]byte, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("WriteField(%q) returned error: %v", key, err)
		}
	}
	part, err := writer.CreateFormFile("file", archiveName)
	if err != nil {
		t.Fatalf("CreateFormFile returned error: %v", err)
	}
	zipWriter := zip.NewWriter(part)
	entries, err := os.ReadDir(fixtureDir)
	if err != nil {
		t.Fatalf("ReadDir(%q) returned error: %v", fixtureDir, err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		content, err := os.ReadFile(filepath.Join(fixtureDir, name))
		if err != nil {
			t.Fatalf("ReadFile(%q) returned error: %v", name, err)
		}
		fileWriter, err := zipWriter.Create(name)
		if err != nil {
			t.Fatalf("zip.Create(%q) returned error: %v", name, err)
		}
		if _, err := fileWriter.Write(content); err != nil {
			t.Fatalf("zip.Write(%q) returned error: %v", name, err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("multipart.Close returned error: %v", err)
	}
	return body.Bytes(), writer.FormDataContentType()
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

func buildLoadRunnerProtocolArchive(t *testing.T, scriptType string, action string) []byte {
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
	addFile("Protocol.usr", "[General]\nType="+scriptType+"\n\n[Actions]\nAction1=Action.c\n")
	addFile("Action.c", action)
	if err := writer.Close(); err != nil {
		t.Fatalf("zip.Close returned error: %v", err)
	}
	return body.Bytes()
}

func responseHasReviewSection(sections []struct {
	Key string `json:"key"`
}, key string) bool {
	for _, section := range sections {
		if section.Key == key {
			return true
		}
	}
	return false
}

func responseHasRepairCategory(suggestions []struct {
	Category string `json:"category"`
}, category string) bool {
	for _, suggestion := range suggestions {
		if suggestion.Category == category {
			return true
		}
	}
	return false
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
	for _, key := range []string{"scripts", "script", "warnings", "stats", "correlations", "assumptions", "unsupported_elements", "review_sections", "repair_suggestions", "scenario_draft", "summary", "detected_protocol", "review_artifact", "protocol_compat_report", "result"} {
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

func reviewSectionsContain(sections any, wantKey string) bool {
	value := reflect.ValueOf(sections)
	if value.Kind() != reflect.Slice {
		return false
	}
	for i := 0; i < value.Len(); i++ {
		section := value.Index(i)
		key := section.FieldByName("Key")
		if key.IsValid() && key.String() == wantKey {
			return true
		}
	}
	return false
}
