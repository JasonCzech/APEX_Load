package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestHandleEstimateTestReturnsDraftAdvisory(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-estimate-api",
		Name:     "Estimate API Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	body, _ := json.Marshal(CreateTestRequest{
		Duration: "5m",
		ScriptID: "script-estimate-api",
		VUsers:   12000,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/estimate", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload DraftCapacityEstimate
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.CapacityAdvisory == nil {
		t.Fatal("expected capacity advisory in draft estimate response")
	}
	if payload.TotalVUsers != 12000 {
		t.Fatalf("total vusers = %d, want 12000", payload.TotalVUsers)
	}
	if payload.CapacityAdvisory.RecommendedGenerators < 1 {
		t.Fatalf("recommended generators = %d, want >= 1", payload.CapacityAdvisory.RecommendedGenerators)
	}
}