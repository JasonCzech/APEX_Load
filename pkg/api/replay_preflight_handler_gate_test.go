package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
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
			name: "classic replay missing variable",
			path: "/api/v1/scripts/replay",
			script: map[string]any{
				"name": "Classic", "version": "1.0.0", "protocol": "http",
				"config": map[string]any{"base_url": "https://example.test"},
				"actions": []map[string]any{{
					"name": "account", "type": "request", "method": http.MethodGet,
					"target": "/accounts/{{var.account_id}}",
				}},
			},
			wantMissingVar: "account_id",
		},
		{
			name: "script debug missing variable",
			path: "/api/v1/scripts/debug/sessions",
			script: map[string]any{
				"name": "Debug", "version": "1.0.0", "protocol": "http",
				"config": map[string]any{"base_url": "https://example.test"},
				"actions": []map[string]any{{
					"name": "account", "type": "request", "method": http.MethodGet,
					"target": "/accounts/{{var.account_id}}",
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
			if test.wantMissingVar != "" && !containsString(payload.Preflight.MissingVariables, test.wantMissingVar) {
				t.Fatalf("missing_variables = %#v, want %q", payload.Preflight.MissingVariables, test.wantMissingVar)
			}
			if test.wantWarningText != "" &&
				!strings.Contains(strings.Join(payload.Preflight.Warnings, "\n"), test.wantWarningText) {
				t.Fatalf("preflight warnings = %#v, want %q", payload.Preflight.Warnings, test.wantWarningText)
			}
		})
	}
}
