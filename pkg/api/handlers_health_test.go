package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleHealthCheckReturnsCapacityAvailabilitySnapshot(t *testing.T) {
	t.Setenv("STORMRUNNER_LIVE_AVAILABLE_GENERATORS", "3")
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})

	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/system/health", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}

	var payload struct {
		Status               string `json:"status"`
		CapacityAvailability struct {
			Source                        string   `json:"source"`
			ConfiguredAvailableGenerators int      `json:"configured_available_generators"`
			EffectiveAvailableGenerators  int      `json:"effective_available_generators"`
			LiveAvailableGenerators       *int     `json:"live_available_generators"`
			Warnings                      []string `json:"warnings"`
		} `json:"capacity_availability"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.CapacityAvailability.Source != capacityAvailabilityConfiguredPlusLive {
		t.Fatalf("source = %q, want %q", payload.CapacityAvailability.Source, capacityAvailabilityConfiguredPlusLive)
	}
	if payload.CapacityAvailability.ConfiguredAvailableGenerators != 5 {
		t.Fatalf("configured available generators = %d, want 5", payload.CapacityAvailability.ConfiguredAvailableGenerators)
	}
	if payload.CapacityAvailability.EffectiveAvailableGenerators != 3 {
		t.Fatalf("effective available generators = %d, want 3", payload.CapacityAvailability.EffectiveAvailableGenerators)
	}
	if payload.CapacityAvailability.LiveAvailableGenerators == nil || *payload.CapacityAvailability.LiveAvailableGenerators != 3 {
		t.Fatalf("live available generators = %#v, want 3", payload.CapacityAvailability.LiveAvailableGenerators)
	}
}
