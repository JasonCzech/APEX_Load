package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHandlePrometheusMetricsExportsControllerState(t *testing.T) {
	t.Setenv("STORMRUNNER_LIVE_AVAILABLE_GENERATORS", "3")
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	settings := server.runner.GetSystemSettings()
	settings.General.MaxConcurrentTests = 7
	settings.General.MetricsIntervalSeconds = 3
	settings.CapacityPlanning.Availability.Source = capacityAvailabilityConfiguredPlusLive
	settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators = 5
	settings.CapacityPlanning.Reservations = []CapacityReservation{{ProjectID: "critical-checkout", ReservedGenerators: 1}}
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	completedAt := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	server.runner.tests["complete"] = &ManagedTest{ID: "complete", Name: "Checkout", Status: "COMPLETE", CreatedAt: completedAt.Add(-time.Minute), EndedAt: &completedAt}
	server.runner.tests["running"] = &ManagedTest{ID: "running", Name: "Checkout", Status: "RUNNING", CreatedAt: completedAt, TotalVUsers: 12, Groups: []TestGroup{{Name: "buyers", Protocol: "http", VUsers: 12}}}
	server.runner.tests["pending"] = &ManagedTest{ID: "pending", Name: "API", Status: "PENDING", CreatedAt: completedAt, TotalVUsers: 8, Groups: []TestGroup{{Name: "api", Protocol: "grpc", VUsers: 8}}}
	server.runner.tests["queued"] = &ManagedTest{ID: "queued", Name: "Browse", Status: "QUEUED", CreatedAt: completedAt, TotalVUsers: 5, Groups: []TestGroup{{Name: "browse", Protocol: "http", VUsers: 5}}}
	server.runner.tests["failed"] = &ManagedTest{ID: "failed", Name: "Checkout", Status: "FAILED", CreatedAt: completedAt.Add(time.Minute), EndedAt: &completedAt}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	if contentType := resp.Header().Get("Content-Type"); !strings.Contains(contentType, "text/plain") {
		t.Fatalf("content type = %q, want Prometheus text format", contentType)
	}
	body := resp.Body.String()
	checks := []string{
		"stormrunner_up 1",
		"stormrunner_websocket_clients 0",
		"stormrunner_test_count 5",
		"stormrunner_tests_by_status{status=\"COMPLETE\"} 1",
		"stormrunner_tests_by_status{status=\"RUNNING\"} 1",
		"stormrunner_tests_by_status{status=\"PENDING\"} 1",
		"stormrunner_tests_by_status{status=\"QUEUED\"} 1",
		"stormrunner_tests_by_status{status=\"FAILED\"} 1",
		"stormrunner_scaling_vusers{state=\"queued\"} 13",
		"stormrunner_scaling_vusers{state=\"active\"} 12",
		"stormrunner_scaling_tests{state=\"queued\"} 2",
		"stormrunner_scaling_tests{state=\"active\"} 1",
		"stormrunner_scaling_protocol_vusers{state=\"queued\",protocol=\"grpc\"} 8",
		"stormrunner_scaling_protocol_vusers{state=\"queued\",protocol=\"http\"} 5",
		"stormrunner_scaling_protocol_vusers{state=\"active\",protocol=\"http\"} 12",
		"stormrunner_capacity_available_generators{scope=\"configured\"} 5",
		"stormrunner_capacity_available_generators{scope=\"effective\"} 3",
		"stormrunner_capacity_available_generators{scope=\"reserved\"} 1",
		"stormrunner_capacity_available_generators{scope=\"shared\"} 2",
		"stormrunner_capacity_available_generators{scope=\"live\"} 3",
		"stormrunner_max_concurrent_tests 7",
		"stormrunner_metrics_interval_seconds 3",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("expected metrics body to contain %q, got %q", check, body)
		}
	}
}

func TestHandlePrometheusMetricsExportsGeneratorRegionStats(t *testing.T) {
	server := NewServer(ServerConfig{MaxConcurrent: 2, Addr: ":8080"})
	server.runner.setExecutionBackend(stubInventoryBackend{entries: []GeneratorInventoryEntry{
		{
			Target:             "gen-a:9090",
			GeneratorID:        "gen-a",
			Region:             "us-east",
			Status:             "connected",
			CapacityState:      "available",
			DrainState:         "active",
			CompatibilityState: "compatible",
			AvailableCapacity:  9000,
			ActiveVUsers:       1000,
		},
		{
			Target:             "gen-b:9090",
			GeneratorID:        "gen-b",
			Region:             "us-east",
			Status:             "connected",
			CapacityState:      "constrained",
			DrainState:         "cordoned",
			CompatibilityState: "compatible",
			AvailableCapacity:  7000,
			ActiveVUsers:       3000,
		},
		{
			Target:             "gen-c:9090",
			GeneratorID:        "gen-c",
			Status:             "connected",
			CapacityState:      "saturated",
			DrainState:         "active",
			CompatibilityState: "incompatible",
			AvailableCapacity:  5000,
			ActiveVUsers:       0,
		},
	}})

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d body=%s", resp.Code, http.StatusOK, resp.Body.String())
	}
	body := resp.Body.String()
	checks := []string{
		"stormrunner_generator_region_count{region=\"us-east\"} 2",
		"stormrunner_generator_region_available_capacity{region=\"us-east\"} 16000",
		"stormrunner_generator_region_active_vusers{region=\"us-east\"} 4000",
		"stormrunner_generator_region_count{region=\"unassigned\"} 1",
		"stormrunner_generator_region_available_capacity{region=\"unassigned\"} 5000",
		"stormrunner_generator_region_active_vusers{region=\"unassigned\"} 0",
		"stormrunner_generator_capacity_state_count{region=\"us-east\",state=\"available\"} 1",
		"stormrunner_generator_capacity_state_count{region=\"us-east\",state=\"constrained\"} 1",
		"stormrunner_generator_capacity_state_count{region=\"unassigned\",state=\"saturated\"} 1",
		"stormrunner_generator_drain_state_count{region=\"us-east\",state=\"active\"} 1",
		"stormrunner_generator_drain_state_count{region=\"us-east\",state=\"cordoned\"} 1",
		"stormrunner_generator_compatibility_state_count{region=\"us-east\",state=\"compatible\"} 2",
		"stormrunner_generator_compatibility_state_count{region=\"unassigned\",state=\"incompatible\"} 1",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("expected metrics body to contain %q, got %q", check, body)
		}
	}
}
