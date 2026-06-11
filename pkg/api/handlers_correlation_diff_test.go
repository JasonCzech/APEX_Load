package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func diffRecordingPayload(token string) string {
	return fmt.Sprintf(`[
		{"id": 1, "request_url": "https://shop.example/login",
		 "response_body": "{\"session_token\":\"%s\",\"api_version_id\":\"55512345\"}"},
		{"id": 2, "request_url": "https://shop.example/cart",
		 "request_headers": {"X-Session": "%s"},
		 "request_body": "{\"api_version_id\":\"55512345\"}",
		 "response_body": "{\"ok\":true}"}
	]`, token, token)
}

func TestHandleDiffCorrelations(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := fmt.Sprintf(`{"recording_a": %s, "recording_b": %s}`,
		diffRecordingPayload("tok-aaaa-1111-secret"),
		diffRecordingPayload("tok-bbbb-2222-secret"))

	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diff", strings.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var result struct {
		Results []struct {
			Name   string  `json:"Name"`
			Class  string  `json:"class"`
			ValueB string  `json:"value_b"`
			Conf   float64 `json:"Confidence"`
		} `json:"results"`
		Counts struct {
			Dynamic int `json:"dynamic"`
			Static  int `json:"static"`
			Unknown int `json:"unknown"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if result.Counts.Dynamic == 0 || result.Counts.Static == 0 {
		t.Errorf("counts = %+v, want both dynamic and static classifications", result.Counts)
	}
	if result.Results[0].Class != "dynamic" {
		t.Errorf("first result class = %q, want dynamic sorted first", result.Results[0].Class)
	}
}

func TestHandleDiffCorrelationsValidation(t *testing.T) {
	server := NewServer(ServerConfig{})
	tests := []struct {
		name string
		body string
	}{
		{name: "invalid json", body: "{broken"},
		{name: "missing recording_b", body: fmt.Sprintf(`{"recording_a": %s}`, diffRecordingPayload("x"))},
		{name: "empty recordings", body: `{"recording_a": [], "recording_b": []}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diff", strings.NewReader(tt.body))
			req.Header.Set("Content-Type", "application/json")
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400", resp.Code)
			}
		})
	}
}
