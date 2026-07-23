package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	responseJSON := resp.Body.String()
	if bytes.Contains([]byte(responseJSON), []byte("TOKEN-AAA-1111-recorded")) || bytes.Contains([]byte(responseJSON), []byte("TOKEN-BBB-2222-replayed")) {
		t.Fatalf("diagnose response disclosed recorded or replay literals: %s", responseJSON)
	}
	var out correlationDiagnoseResponse
	if err := json.Unmarshal([]byte(responseJSON), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !out.Blocking || out.Counts.Errors != 1 {
		t.Fatalf("expected 1 blocking error, got %+v", out)
	}
	if len(out.Suggestions) != 1 || out.Suggestions[0].FailingStepID != 2 {
		t.Fatalf("unexpected suggestions: %+v", out.Suggestions)
	}
	if out.ComparedRecordedSteps != 2 || out.TotalRecordedSteps != 2 || out.Truncated {
		t.Fatalf("coverage = compared:%d total:%d truncated:%t, want complete 2/2", out.ComparedRecordedSteps, out.TotalRecordedSteps, out.Truncated)
	}
}

func TestDiagnoseCorrelationsEndpointReportsPartialReplayCoverage(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := correlationDiagnoseRequest{
		Recorded: []correlationStep{{ID: 1}, {ID: 2}},
		Replay:   []correlationStep{{ID: 1}},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	var out correlationDiagnoseResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ComparedRecordedSteps != 1 || out.TotalRecordedSteps != 2 || !out.Truncated {
		t.Fatalf("coverage = compared:%d total:%d truncated:%t, want partial 1/2", out.ComparedRecordedSteps, out.TotalRecordedSteps, out.Truncated)
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

func TestDiagnoseCorrelationsEndpointRejectsDuplicateStepIDs(t *testing.T) {
	server := NewServer(ServerConfig{})
	tests := []struct {
		name    string
		payload correlationDiagnoseRequest
	}{
		{
			name: "recorded",
			payload: correlationDiagnoseRequest{
				Recorded: []correlationStep{{ID: 1}, {ID: 1}},
			},
		},
		{
			name: "replay",
			payload: correlationDiagnoseRequest{
				Recorded: []correlationStep{{ID: 1}},
				Replay:   []correlationStep{{ID: 2}, {ID: 2}},
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			body, _ := json.Marshal(test.payload)
			req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader(body))
			resp := httptest.NewRecorder()
			server.mux.ServeHTTP(resp, req)
			if resp.Code != http.StatusBadRequest || !bytes.Contains(resp.Body.Bytes(), []byte("duplicate id")) {
				t.Fatalf("duplicate steps = status %d body=%s, want 400", resp.Code, resp.Body.String())
			}
		})
	}
}

func TestDiagnoseCorrelationsEndpointRejectsSingleNestedLeaf(t *testing.T) {
	server := NewServer(ServerConfig{})
	payload := correlationDiagnoseRequest{
		Recorded: []correlationStep{{ID: 0}},
		Replay:   []correlationStep{{ID: 0, ParentActionName: "sequence", StepPath: []string{"requests[0]"}}},
	}
	body, _ := json.Marshal(payload)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusBadRequest || !strings.Contains(resp.Body.String(), "nested replay leaf") {
		t.Fatalf("status = %d body=%s, want nested-leaf rejection", resp.Code, resp.Body.String())
	}
}

func TestDiagnoseCorrelationsEndpointRejectsOversizedObservationSet(t *testing.T) {
	server := NewServer(ServerConfig{})
	steps := make([]correlationStep, maxRecordedBaselineObservations+1)
	for index := range steps {
		steps[index] = correlationStep{ID: index}
	}
	body, err := json.Marshal(correlationDiagnoseRequest{Recorded: steps})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("oversized observations = status %d body=%s, want 413", resp.Code, resp.Body.String())
	}
}

func TestDiagnoseCorrelationsEndpointMakesZeroCoverageExplicit(t *testing.T) {
	server := NewServer(ServerConfig{})
	body, err := json.Marshal(correlationDiagnoseRequest{
		Recorded: []correlationStep{{ID: 1}},
		Replay:   []correlationStep{{ID: 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d body=%s", resp.Code, resp.Body.String())
	}
	if !bytes.Contains(resp.Body.Bytes(), []byte(`"compared_recorded_steps":0`)) ||
		!bytes.Contains(resp.Body.Bytes(), []byte(`"total_recorded_steps":1`)) {
		t.Fatalf("zero coverage was omitted from response: %s", resp.Body.String())
	}
}

func TestDiagnoseCorrelationsEndpointRejectsCandidateOverflow(t *testing.T) {
	server := NewServer(ServerConfig{})
	var overflowBody strings.Builder
	overflowBody.WriteByte('{')
	for index := 0; index <= maxCorrelationCandidatesPerObservationForAPITest(); index++ {
		if index > 0 {
			overflowBody.WriteByte(',')
		}
		fmt.Fprintf(&overflowBody, `"token_%d":"token-value-%06d"`, index, index)
	}
	overflowBody.WriteByte('}')
	body, err := json.Marshal(correlationDiagnoseRequest{
		Recorded: []correlationStep{{ID: 1, ResponseBody: overflowBody.String()}},
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/correlations/diagnose", bytes.NewReader(body))
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusUnprocessableEntity {
		t.Fatalf("candidate overflow = status %d body=%s, want fail-closed 422", resp.Code, resp.Body.String())
	}
}

// Keep the transport regression independent of unexported correlate constants.
// 1025 candidates is one above the engine's documented per-observation limit.
func maxCorrelationCandidatesPerObservationForAPITest() int { return 1024 }
