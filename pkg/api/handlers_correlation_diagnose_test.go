package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestListCorrelationRulesEndpoint(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/correlations/rules", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var body struct {
		Rules []struct {
			ID     string `json:"id"`
			Family string `json:"family"`
		} `json:"rules"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Rules) == 0 {
		t.Fatalf("expected rule catalog, got none")
	}
}

func TestDiagnoseCorrelationsEndpointDetectsStaleValue(t *testing.T) {
	server := NewServer(ServerConfig{})

	payload := correlationDiagnoseRequest{
		Recorded: []correlationStep{
			{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-AAA-1111-recorded"}`},
			{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded"},
		},
		Replay: []correlationStep{
			{ID: 1, ResponseBody: `{"csrf_token":"TOKEN-BBB-2222-replayed"}`},
			{ID: 2, RequestBody: "csrf=TOKEN-AAA-1111-recorded"},
		},
	}
	b, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader(b))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var out correlationDiagnoseResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Blocking || out.Counts.Errors != 1 {
		t.Fatalf("expected 1 blocking error, got %+v", out)
	}
	if len(out.Suggestions) != 1 || out.Suggestions[0].FailingStepID != 2 {
		t.Fatalf("unexpected suggestions: %+v", out.Suggestions)
	}
}

func TestDiagnoseCorrelationsEndpointValidatesBody(t *testing.T) {
	server := NewServer(ServerConfig{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader([]byte(`{"replay":[]}`)))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for missing recorded steps, got %d", resp.Code)
	}
}
