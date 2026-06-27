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

func TestPrometheusMessagingMetricsSkipTerminalTests(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.tests["done"] = &ManagedTest{
		ID:     "done",
		Status: "COMPLETE",
		LiveMetrics: &LiveMetrics{DependencyHealth: map[string]*TxMetrics{
			"queue": {
				Name:             "queue",
				DependencyType:   "kafka",
				DependencyTarget: "orders",
				ServerTelemetry:  &ServerTelemetrySummary{Samples: 1, AvgConsumerLagMs: 99},
			},
		}},
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if strings.Contains(resp.Body.String(), `test_id="done"`) {
		t.Fatalf("terminal test emitted messaging metrics:\n%s", resp.Body.String())
	}
}

func TestPrometheusTelemetryPipelineMetrics(t *testing.T) {
	server := NewServer(ServerConfig{})
	server.runner.tests["run"] = &ManagedTest{
		ID:     "run",
		Status: "RUNNING",
		LiveMetrics: &LiveMetrics{Telemetry: &TelemetryHealth{
			DroppedResults:           2,
			SheddedArrivals:          3,
			DroppedSubscriberBatches: 4,
			WebsocketDroppedMessages: 5,
			WebsocketDroppedEvents:   6,
			SampleAgeMs:              1200,
			GeneratorProcessingLagMs: 100,
			TransportLagMs:           80,
			ControllerFanoutLagMs:    40,
		}},
	}

	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	out := resp.Body.String()
	wants := []string{
		`apexload_telemetry_dropped_results_total{test_id="run"} 2`,
		`apexload_telemetry_shedded_arrivals_total{test_id="run"} 3`,
		`apexload_telemetry_dropped_subscriber_batches_total{test_id="run"} 4`,
		`apexload_websocket_dropped_messages_total{test_id="run"} 5`,
		`apexload_websocket_dropped_events_total{test_id="run"} 6`,
		`apexload_telemetry_sample_age_ms{test_id="run"} 1200`,
		`apexload_telemetry_generator_processing_lag_ms{test_id="run"} 100`,
		`apexload_telemetry_transport_lag_ms{test_id="run"} 80`,
		`apexload_telemetry_controller_fanout_lag_ms{test_id="run"} 40`,
	}
	for _, want := range wants {
		if !strings.Contains(out, want) {
			t.Fatalf("metrics output missing %q\n---\n%s", want, out)
		}
	}
}
