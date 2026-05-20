package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleStartTestReturnsCapacityShortfallPayload(t *testing.T) {
	store := &metricsStoreSpy{}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-api-capacity",
		Name:     "API Capacity Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "API Start Block",
		Duration: "30s",
		ScriptID: "script-api-capacity",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/start", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusConflict {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusConflict, resp.Body.String())
	}
	var payload struct {
		Error             string            `json:"error"`
		Code              string            `json:"code"`
		CapacityAdvisory  *CapacityAdvisory `json:"capacity_advisory"`
		OverrideAvailable bool              `json:"override_available"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Code != "capacity_shortfall" {
		t.Fatalf("code = %q, want capacity_shortfall", payload.Code)
	}
	if payload.CapacityAdvisory == nil || !payload.CapacityAdvisory.RequiresOverride {
		t.Fatalf("expected advisory requiring override, got %#v", payload.CapacityAdvisory)
	}
	if !payload.OverrideAvailable {
		t.Fatal("expected override_available to be true")
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "capacity.placement.reject", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}
	if events[0].Status != "blocked" || events[0].ResourceID != testRun.ID {
		t.Fatalf("audit event = %#v, want blocked placement rejection for test", events[0])
	}
	if events[0].Details["recommended_generators"] == nil || events[0].Details["available_generators"] == nil || events[0].Details["reason"] != "capacity_shortfall" {
		t.Fatalf("audit details = %#v, want capacity shortfall sizing evidence", events[0].Details)
	}
}

func TestHandleStartTestAllowsOverridePayload(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:       "script-api-capacity-override",
		Name:     "API Capacity Override Script",
		Protocol: "http",
		Version:  "1.0.0",
		Actions:  []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 1
	settings.CapacityPlanning.Generator.MaxVUsers = 10000
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	testRun, err := server.runner.CreateTest(CreateTestRequest{
		Name:     "API Start Override",
		Duration: "30s",
		ScriptID: "script-api-capacity-override",
		VUsers:   15000,
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	body, _ := json.Marshal(StartTestOptions{OverrideCapacityShortfall: true})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+testRun.ID+"/start", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
}

func TestHandleStartTestReturnsPendingWhenProjectQuotaReached(t *testing.T) {
	store := &metricsStoreSpy{projectsByID: map[string]*storage.Project{
		"project-1": {
			ID:   "project-1",
			Name: "Payments",
			Settings: storage.ProjectSettings{
				MaxConcurrentTests: 1,
			},
		},
	}}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 2, Addr: ":8080"})
	server.runner.StoreScript(&dsl.Script{
		ID:        "script-api-project-quota",
		Name:      "API Project Quota Script",
		Protocol:  "http",
		Version:   "1.0.0",
		ProjectID: "project-1",
		Actions:   []dsl.Action{{Name: "step", Type: "request", Target: "/"}},
	})

	first, err := server.runner.CreateTest(CreateTestRequest{Name: "First", Duration: "30s", ScriptID: "script-api-project-quota", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(first) returned error: %v", err)
	}
	second, err := server.runner.CreateTest(CreateTestRequest{Name: "Second", Duration: "30s", ScriptID: "script-api-project-quota", VUsers: 1})
	if err != nil {
		t.Fatalf("CreateTest(second) returned error: %v", err)
	}

	server.runner.mu.Lock()
	server.runner.tests[first.ID].Status = "RUNNING"
	server.runner.mu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+second.ID+"/start", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	var payload struct {
		Status  string `json:"status"`
		TestID  string `json:"test_id"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.Status != "pending" {
		t.Fatalf("status = %q, want pending", payload.Status)
	}
	if payload.TestID != second.ID {
		t.Fatalf("test_id = %q, want %q", payload.TestID, second.ID)
	}
	events, err := store.ListAuditEvents(storage.AuditEventFilter{Action: "capacity.fair_share.defer", ProjectID: "project-1", Limit: 10})
	if err != nil {
		t.Fatalf("ListAuditEvents returned error: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("audit event count = %d, want 1", len(events))
	}
	if events[0].Status != "blocked" || events[0].ResourceID != second.ID {
		t.Fatalf("audit event = %#v, want blocked fair-share deferral for second test", events[0])
	}
	if events[0].Details["pending_reason"] == "" {
		t.Fatalf("audit details = %#v, want pending reason", events[0].Details)
	}
}
