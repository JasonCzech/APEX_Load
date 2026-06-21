package api

import "testing"

func TestSampleEventHubsThrottleDiagnosticsFiresOnThrottle(t *testing.T) {
	live := &LiveMetrics{
		Transactions: map[string]*TxMetrics{
			"eventhubs_produce telemetry": {
				Name:            "eventhubs_produce telemetry",
				DependencyType:  "eventhubs",
				Count:           1000,
				ServerTelemetry: &ServerTelemetrySummary{TotalThrottledRequests: 12, AvgEndToEndLatencyMs: 80},
			},
		},
	}
	samples, conditions, summaries, severity, signature := sampleEventHubsThrottleDiagnostics(live)
	if len(conditions) != 1 || conditions[0] != "eventhubs_throttle_pressure" {
		t.Fatalf("conditions = %v, want [eventhubs_throttle_pressure]", conditions)
	}
	if severity != "warning" {
		t.Fatalf("severity = %q, want warning", severity)
	}
	if len(samples) != 1 || samples[0].Issue != "eventhubs_throttle" || samples[0].ThrottledRequests != 12 {
		t.Fatalf("unexpected samples: %#v", samples)
	}
	if len(summaries) == 0 || len(signature) == 0 {
		t.Fatalf("expected summaries + signature, got %v / %v", summaries, signature)
	}
}

func TestSampleEventHubsThrottleDiagnosticsFiresOnSkew(t *testing.T) {
	live := &LiveMetrics{
		Transactions: map[string]*TxMetrics{
			"eventhubs_produce telemetry": {
				Name:            "eventhubs_produce telemetry",
				DependencyType:  "eventhubs",
				ServerTelemetry: &ServerTelemetrySummary{PartitionSkewPct: 90}, // >= warn threshold
			},
		},
	}
	_, conditions, _, _, _ := sampleEventHubsThrottleDiagnostics(live)
	if len(conditions) != 1 || conditions[0] != "eventhubs_throttle_pressure" {
		t.Fatalf("expected skew to trigger the analyzer, got conditions %v", conditions)
	}
}

func TestSampleEventHubsThrottleDiagnosticsHealthy(t *testing.T) {
	live := &LiveMetrics{
		Transactions: map[string]*TxMetrics{
			// Healthy EH producer: no throttling, low skew.
			"eventhubs_produce telemetry": {
				DependencyType:  "eventhubs",
				ServerTelemetry: &ServerTelemetrySummary{PartitionSkewPct: 5, AvgEndToEndLatencyMs: 50},
			},
			// A JMS transaction must be ignored by this analyzer.
			"jms_consume orders": {
				DependencyType:  "jms_consume",
				ServerTelemetry: &ServerTelemetrySummary{TotalThrottledRequests: 99},
			},
		},
	}
	samples, conditions, _, severity, _ := sampleEventHubsThrottleDiagnostics(live)
	if len(conditions) != 0 || len(samples) != 0 || severity != "healthy" {
		t.Fatalf("expected healthy with no EH pressure, got conditions=%v samples=%v severity=%q", conditions, samples, severity)
	}
}
