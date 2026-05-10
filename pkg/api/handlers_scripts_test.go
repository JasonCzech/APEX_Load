package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

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
		Script struct {
			Name    string `json:"name"`
			Actions []struct {
				Target string `json:"target"`
			} `json:"actions"`
		} `json:"script"`
		Warnings     []string `json:"warnings"`
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
