package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPrometheusMessagingMetrics(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.tests["run-eh"] = &ManagedTest{
		ID:     "run-eh",
		Name:   "eh-run",
		Status: "RUNNING",
		LiveMetrics: &LiveMetrics{
			DependencyHealth: map[string]*TxMetrics{
				"telemetry": {
					Name:             "telemetry",
					DependencyType:   "eventhubs",
					DependencyTarget: "telemetry",
					ServerTelemetry: &ServerTelemetrySummary{
						Samples:                3000,
						AvgEndToEndLatencyMs:   12.5,
						PartitionSkewPct:       18.5,
						TotalThrottledRequests: 4,
						TotalServerBusyCount:   1,
						AvgMessagesSentPerSec:  900,
					},
				},
				// A non-messaging dependency must be ignored.
				"https://api.example": {
					Name:            "https://api.example",
					DependencyType:  "http",
					ServerTelemetry: &ServerTelemetrySummary{Samples: 10, AvgServerProcessingMs: 40},
				},
			},
		},
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	out := resp.Body.String()

	wants := []string{
		`# TYPE apexload_messaging_e2e_latency_ms gauge`,
		`apexload_messaging_e2e_latency_ms{test_id="run-eh",protocol="eventhubs",target="telemetry"} 12.5`,
		`apexload_messaging_partition_skew_pct{test_id="run-eh",protocol="eventhubs",target="telemetry"} 18.5`,
		`apexload_messaging_throttled_total{test_id="run-eh",protocol="eventhubs",target="telemetry"} 5`,
	}
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Fatalf("metrics output missing %q\n---\n%s", w, out)
		}
	}
	// The HTTP dependency must not be emitted as a messaging metric.
	if strings.Contains(out, `protocol="http"`) {
		t.Fatalf("non-messaging dependency leaked into messaging metrics:\n%s", out)
	}
}

func TestPrometheusMessagingMetricsAbsentWhenNoMessaging(t *testing.T) {
	server := NewServer(ServerConfig{})
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.Code)
	}
	if strings.Contains(resp.Body.String(), "apexload_messaging_") {
		t.Fatal("messaging metrics emitted with no messaging telemetry present")
	}
}
