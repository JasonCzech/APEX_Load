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
	responseJSON := resp.Body.String()
	for _, forbidden := range []string{
		"tok-aaaa-1111-secret",
		"tok-bbbb-2222-secret",
		"55512345",
		`"Value"`,
		`"SampleValue"`,
		`"value_b"`,
		`"sample_value"`,
	} {
		if strings.Contains(responseJSON, forbidden) {
			t.Fatalf("diff response disclosed recording value or value field %q: %s", forbidden, responseJSON)
		}
	}
	var result struct {
		Results []correlationDiffResult `json:"results"`
		Counts  struct {
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
	if result.Results[0].Name == "" || result.Results[0].Source == "" || result.Results[0].Expression == "" || result.Results[0].DiffReason == "" {
		t.Errorf("safe result lost useful extractor/classification structure: %+v", result.Results[0])
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

func TestHandleDiffCorrelationsRejectsDuplicateIDs(t *testing.T) {
	server := NewServer(ServerConfig{})
	for _, body := range []string{
		`{"recording_a":[{"id":1},{"id":1}],"recording_b":[{"id":1}]}`,
		`{"recording_a":[{"id":1}],"recording_b":[{"id":1},{"id":1}]}`,
	} {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diff", strings.NewReader(body))
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "duplicate id") {
			t.Fatalf("duplicate observations = status %d body=%s, want 400", resp.Code, resp.Body.String())
		}
	}
}

func TestHandleDiffCorrelationsRejectsOversizedObservationSet(t *testing.T) {
	server := NewServer(ServerConfig{})
	steps := make([]correlationStep, maxRecordedBaselineObservations+1)
	for index := range steps {
		steps[index] = correlationStep{ID: index}
	}
	body, err := json.Marshal(correlationDiffRequest{RecordingA: steps, RecordingB: []correlationStep{{ID: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diff", strings.NewReader(string(body)))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized observations = status %d body=%s, want 413", resp.Code, resp.Body.String())
	}
}

// A single response beyond the per-observation candidate cap is truncated by
// the engine and the diff succeeds; exhausting the aggregate candidate budget
// across many such responses truncates the second-recording index instead of
// failing, leaving un-indexed steps classified unknown for manual review.
func TestHandleDiffCorrelationsSurfacesWorkLimitExhaustion(t *testing.T) {
	server := NewServer(ServerConfig{})
	overflowBody := "[" + strings.Repeat(`{"token":"token-abcdef"},`, maxCorrelationCandidatesPerObservationForAPITest()) + `{"token":"token-abcdef"}]`

	singleBody, err := json.Marshal(correlationDiffRequest{
		RecordingA: []correlationStep{{ID: 1}},
		RecordingB: []correlationStep{{ID: 1, ResponseBody: overflowBody}},
	})
	if err != nil {
		t.Fatal(err)
	}
	singleReq := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diff", strings.NewReader(string(singleBody)))
	singleResp := httptest.NewRecorder()
	server.mux.ServeHTTP(singleResp, singleReq)
	if singleResp.Code != http.StatusOK {
		t.Fatalf("single candidate overflow = status %d body=%s, want truncated 200", singleResp.Code, singleResp.Body.String())
	}

	overflowSteps := make([]correlationStep, 0, 5)
	for id := 1; id <= 5; id++ {
		overflowSteps = append(overflowSteps, correlationStep{ID: id, ResponseBody: overflowBody})
	}
	// Recording A anchors a real correlation at step 5 — the step the
	// truncated second-recording index never reaches.
	body, err := json.Marshal(correlationDiffRequest{
		RecordingA: []correlationStep{
			{ID: 5, ResponseBody: `{"session_token":"corr-value-1234567890"}`},
			{ID: 6, RequestURL: "https://example.test/next?session_token=corr-value-1234567890"},
		},
		RecordingB: overflowSteps,
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diff", strings.NewReader(string(body)))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("aggregate work exhaustion = status %d body=%s, want truncated 200", resp.Code, resp.Body.String())
	}
	if !strings.Contains(resp.Body.String(), "reached its work limit") {
		t.Fatalf("aggregate work exhaustion body=%s, want truncation reason on un-indexed steps", resp.Body.String())
	}
}
