package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandleDiagnoseSelectors(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := `{
  "steps": [
    {"action_name": "Click Go", "selector": "#go-old", "status": "failed", "error": "timeout",
     "candidates": [
       {"kind": "css", "value": "#go-old", "confidence": 90},
       {"kind": "css", "value": "[data-testid=\"go\"]", "confidence": 95}
     ]},
    {"action_name": "Click Pay", "selector": "#pay", "status": "healed", "healed_selector": "form #pay",
     "candidates": [{"kind": "css", "value": "form #pay", "confidence": 68}]},
    {"action_name": "Click OK", "selector": "#ok", "status": "ok",
     "candidates": [{"kind": "css", "value": "#ok", "confidence": 90}]}
  ]
}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/selectors/diagnose", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		Suggestions []struct {
			Severity          string `json:"severity"`
			ActionName        string `json:"action_name"`
			SuggestedSelector string `json:"suggested_selector"`
			ReviewAction      string `json:"review_action"`
		} `json:"suggestions"`
		Blocking bool `json:"blocking"`
		Counts   struct {
			Errors   int `json:"errors"`
			Warnings int `json:"warnings"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Suggestions) != 2 {
		t.Fatalf("suggestions = %+v", result.Suggestions)
	}
	if result.Suggestions[0].Severity != "error" || result.Suggestions[0].SuggestedSelector != `[data-testid="go"]` {
		t.Errorf("failed suggestion = %+v", result.Suggestions[0])
	}
	if result.Suggestions[1].ReviewAction != "auto_apply_available" {
		t.Errorf("healed suggestion = %+v", result.Suggestions[1])
	}
	if !result.Blocking || result.Counts.Errors != 1 || result.Counts.Warnings != 1 {
		t.Errorf("blocking=%v counts=%+v", result.Blocking, result.Counts)
	}
}

func TestHandleDiagnoseSelectorsValidation(t *testing.T) {
	server := NewServer(ServerConfig{})
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: "{broken"},
		{name: "no steps", body: `{"steps": []}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/selectors/diagnose", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.Code)
			}
		})
	}
}
