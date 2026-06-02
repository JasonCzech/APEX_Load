package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestHandleImportK6ReturnsScriptAndReviewDiagnostics(t *testing.T) {
	server := NewServer(ServerConfig{})
	body := []byte(`{
		"project_id": "project-k6",
		"name": "Checkout k6",
		"content": "import http from 'k6/http';\nimport { check } from 'k6';\nexport const options = { vus: 2, duration: '30s' };\nexport default function () { const res = http.get('https://api.example.com/health', { headers: { 'X-Test': 'yes' } }); check(res, { 'ok': r => r.status === 200 }); }"
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/import/k6", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Script      dsl.Script `json:"script"`
		Warnings    []string   `json:"warnings"`
		Assumptions []string   `json:"assumptions"`
		Stats       struct {
			HTTPRequests int `json:"http_requests"`
			Checks       int `json:"checks"`
		} `json:"stats"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if payload.Script.ProjectID != "project-k6" || payload.Script.Protocol != "http" || len(payload.Script.Actions) != 1 {
		t.Fatalf("script = %#v", payload.Script)
	}
	if payload.Script.Actions[0].Headers["X-Test"] != "yes" {
		t.Fatalf("headers = %#v, want imported header", payload.Script.Actions[0].Headers)
	}
	if payload.Stats.HTTPRequests != 1 || payload.Stats.Checks != 1 {
		t.Fatalf("stats = %#v, want request/check counts", payload.Stats)
	}
	if len(payload.Assumptions) == 0 || len(payload.Warnings) == 0 {
		t.Fatalf("payload assumptions/warnings = %#v/%#v", payload.Assumptions, payload.Warnings)
	}
}

func TestHandleExportK6ReturnsSubsetScript(t *testing.T) {
	server := NewServer(ServerConfig{})
	body := []byte(`{
		"script": {
			"name": "Export k6",
			"project_id": "project-k6",
			"protocol": "http",
			"config": {"base_url": "https://api.example.com"},
			"actions": [{"name": "Health", "type": "request", "method": "GET", "target": "/health"}]
		}
	}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/scripts/export/k6", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Script string `json:"script"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("json.Unmarshal returned error: %v", err)
	}
	if !strings.Contains(payload.Script, "http.get") || !strings.Contains(payload.Script, "BASE_URL") {
		t.Fatalf("exported script = %q", payload.Script)
	}
}

func TestHandleK6SupportMatrix(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/scripts/import/k6/support-matrix", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "k6-http") || !strings.Contains(resp.Body.String(), "k6-browser") {
		t.Fatalf("support matrix body = %s", resp.Body.String())
	}
}
