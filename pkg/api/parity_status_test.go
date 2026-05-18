package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleGetParityStatusReturnsReleaseGateSummary(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/parity-status", nil)
	resp := httptest.NewRecorder()

	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload ParityStatusResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Overall != "partial" {
		t.Fatalf("overall = %q, want partial", payload.Overall)
	}
	if len(payload.Areas) != 7 {
		t.Fatalf("area count = %d, want 7", len(payload.Areas))
	}
	required := map[string]bool{
		"architecture":  false,
		"fleet":         false,
		"authoring":     false,
		"load_modeling": false,
		"monitoring":    false,
		"reporting":     false,
		"scheduling":    false,
	}
	for _, area := range payload.Areas {
		if _, ok := required[area.ID]; ok {
			required[area.ID] = area.ReleaseGate && area.Status != "" && len(area.Evidence) > 0
		}
	}
	for id, ok := range required {
		if !ok {
			t.Fatalf("missing populated parity area %q in %#v", id, payload.Areas)
		}
	}
}

func TestAggregateParityStatusReportsBlockedGate(t *testing.T) {
	status := aggregateParityStatus([]ParityStatusArea{
		{ID: "ready", Status: "complete"},
		{ID: "blocked", Status: "partial", BlockingGate: true},
	})
	if status != "blocked" {
		t.Fatalf("status = %q, want blocked", status)
	}
}
