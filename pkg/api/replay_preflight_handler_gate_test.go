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

func TestReplayStartHandlersRejectIncompletePreflight(t *testing.T) {
	tests := []struct {
		name            string
		path            string
		script          map[string]any
		wantMissingVar  string
		wantWarningText string
	}{
		{
			name: "classic replay unresolved json buffer",
			path: "/api/v1/scripts/replay",
			script: map[string]any{
				"name": "Classic", "version": "1.0.0", "protocol": "http",
				"config": map[string]any{"base_url": "https://example.test"},
				"actions": []map[string]any{{
					"name": "eval", "type": "source_json",
					"params": map[string]any{"json_object": "account", "buffer": "{{var.account_id}}"},
				}},
			},
			wantMissingVar: "account_id",
		},
		{
			name: "script debug unresolved json buffer",
			path: "/api/v1/scripts/debug/sessions",
			script: map[string]any{
				"name": "Debug", "version": "1.0.0", "protocol": "http",
				"config": map[string]any{"base_url": "https://example.test"},
				"actions": []map[string]any{{
					"name": "eval", "type": "source_json",
					"params": map[string]any{"json_object": "account", "buffer": "{{var.account_id}}"},
				}},
			},
			wantMissingVar: "account_id",
		},
		{
			name: "browser replay unbound imported data",
			path: "/api/v1/scripts/browser-replay/sessions",
			script: map[string]any{
				"name": "Browser", "version": "1.0.0", "protocol": "browser",
				"config": map[string]any{"browser": map[string]any{"engine": "chromium"}},
				"data_sources": []map[string]any{{
					"name": "users", "type": "file",
					"inline_data": "APEXLOAD_OPENAPI_FILE_PLACEHOLDER",
				}},
				"actions": []map[string]any{{
					"name": "capture", "type": "browser_screenshot",
				}},
			},
			wantWarningText: "generated OpenAPI placeholder",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := NewServer(ServerConfig{})
			body, err := json.Marshal(map[string]any{
				"project_id":   "preflight-project",
				"start_paused": true,
				"script":       test.script,
			})
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, test.path, bytes.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()

			server.mux.ServeHTTP(response, request)

			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d body=%s", response.Code, http.StatusBadRequest, response.Body.String())
			}
			var payload struct {
				Valid           bool             `json:"valid"`
				ReplaySessionID string           `json:"replay_session_id"`
				Preflight       *ReplayPreflight `json:"preflight"`
			}
			if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
				t.Fatalf("decode response: %v body=%s", err, response.Body.String())
			}
			if payload.Valid || payload.Preflight == nil || payload.Preflight.Valid {
				t.Fatalf("payload = %#v, want invalid preflight", payload)
			}
			if payload.ReplaySessionID != "" {
				t.Fatalf("replay_session_id = %q, want no session creation", payload.ReplaySessionID)
			}
			if test.wantMissingVar != "" && (!containsString(payload.Preflight.MissingVariables, test.wantMissingVar) ||
				replayPreflightCheckStatus(payload.Preflight, "variables") != "fail") {
				t.Fatalf("missing_variables = %#v checks=%#v, want %q to fail variable readiness", payload.Preflight.MissingVariables, payload.Preflight.Checks, test.wantMissingVar)
			}
			if test.wantWarningText != "" &&
				!strings.Contains(strings.Join(payload.Preflight.Warnings, "\n"), test.wantWarningText) {
				t.Fatalf("preflight warnings = %#v, want %q", payload.Preflight.Warnings, test.wantWarningText)
			}
		})
	}
}

func TestReplayPreflightSendsHTTPRequestPlaceholders(t *testing.T) {
	request := func(target string) dsl.Action {
		return dsl.Action{Name: "store", Type: "request", Method: http.MethodGet, Target: target}
	}
	tests := []struct {
		name         string
		script       *dsl.Script
		wantValid    bool
		wantBlocking string
	}{
		{
			name:      "request target",
			script:    &dsl.Script{Protocol: "http", Actions: []dsl.Action{request("/stores/{{var.store_ref}}")}},
			wantValid: true,
		},
		{
			name: "json helper buffer",
			script: &dsl.Script{Protocol: "http", Actions: []dsl.Action{{
				Name: "eval", Type: "source_json", Params: map[string]any{"json_object": "doc", "buffer": "{{var.store_ref}}"},
			}}},
			wantBlocking: "store_ref",
		},
		{
			name:         "non-http protocol",
			script:       &dsl.Script{Protocol: "browser", Actions: []dsl.Action{{Name: "open", Type: "browser_navigate", Target: "/stores/{{var.store_ref}}"}}},
			wantBlocking: "store_ref",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			preflight := buildReplayPreflight(test.script, nil, nil, nil)
			if !containsString(preflight.MissingVariables, "store_ref") {
				t.Fatalf("missing variables = %v", preflight.MissingVariables)
			}
			status := replayPreflightCheckStatus(preflight, "variables")
			warned := strings.Contains(strings.Join(preflight.Warnings, "\n"), "sent as placeholder text: store_ref")
			if test.wantValid {
				if !preflight.Valid || status != "warn" || !warned {
					t.Fatalf("valid=%v status=%q warnings=%v, want a placeholder warning", preflight.Valid, status, preflight.Warnings)
				}
				return
			}
			if preflight.Valid || status != "fail" || warned {
				t.Fatalf("valid=%v status=%q warnings=%v, want %s to block", preflight.Valid, status, preflight.Warnings, test.wantBlocking)
			}
		})
	}
}
