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
	if _, err := server.runner.UpdateSystemSettings(settings); err != nil {
		t.Fatalf("UpdateSystemSettings returned error: %v", err)
	}
	completedAt := time.Date(2026, time.May, 16, 12, 0, 0, 0, time.UTC)
	server.runner.tests["complete"] = &ManagedTest{ID: "complete", Name: "Checkout", Status: "COMPLETE", CreatedAt: completedAt.Add(-time.Minute), EndedAt: &completedAt}
	server.runner.tests["running"] = &ManagedTest{ID: "running", Name: "Checkout", Status: "RUNNING", CreatedAt: completedAt}
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
		"stormrunner_test_count 3",
		"stormrunner_tests_by_status{status=\"COMPLETE\"} 1",
		"stormrunner_tests_by_status{status=\"RUNNING\"} 1",
		"stormrunner_tests_by_status{status=\"FAILED\"} 1",
		"stormrunner_capacity_available_generators{scope=\"configured\"} 5",
		"stormrunner_capacity_available_generators{scope=\"effective\"} 3",
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
