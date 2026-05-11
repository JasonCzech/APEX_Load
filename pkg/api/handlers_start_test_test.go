package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestHandleStartTestReturnsCapacityShortfallPayload(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
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
