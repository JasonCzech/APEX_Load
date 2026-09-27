package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestHandleHealthCheckReturnsCapacityAvailabilitySnapshot(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	entries := []GeneratorInventoryEntry{
		{Target: "gen-a:9090", GeneratorID: "gen-a", Status: "connected", DrainState: "active", AvailableCapacity: 100, MaxVUsers: 1000},
		{Target: "gen-b:9090", GeneratorID: "gen-b", Status: "connected", DrainState: "active", AvailableCapacity: 100, MaxVUsers: 1000},
		{Target: "gen-c:9090", GeneratorID: "gen-c", Status: "healthy", DrainState: "active", AvailableCapacity: 100, MaxVUsers: 1000},
		{Target: "gen-d:9090", GeneratorID: "gen-d", Status: "connected", DrainState: "cordoned", AvailableCapacity: 100, MaxVUsers: 1000},
	}
	server.runner.setExecutionBackend(stubInventoryBackend{entries: entries})
	server.runner.setCachedGeneratorInventory(entries, time.Now())

	settings := server.runner.GetSystemSettings()
	settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "critical-checkout", ReservedGenerators: 1}}
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
			ReservedGenerators            int      `json:"reserved_generators"`
			SharedAvailableGenerators     int      `json:"shared_available_generators"`
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
	if payload.CapacityAvailability.ReservedGenerators != 1 {
		t.Fatalf("reserved generators = %d, want 1", payload.CapacityAvailability.ReservedGenerators)
	}
	if payload.CapacityAvailability.SharedAvailableGenerators != 2 {
		t.Fatalf("shared available generators = %d, want 2", payload.CapacityAvailability.SharedAvailableGenerators)
	}
}
