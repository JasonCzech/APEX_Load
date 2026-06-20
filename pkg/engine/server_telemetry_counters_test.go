package engine

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// TestMergeServerTelemetryReliabilityCountersAreTotals verifies the P1.1
// reliability counters accumulate as summed run totals (not averaged) while the
// existing float fields keep their averaging semantics.
func TestMergeServerTelemetryReliabilityCountersAreTotals(t *testing.T) {
	summary := &ServerTelemetrySummary{}
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{
		SendFailureCount:  1,
		RedeliveryCount:   2,
		DeadLetteredCount: 1,
		DuplicateCount:    1,
		OutOfOrderCount:   1,
		CommitCount:       3,
		AbortCount:        1,
		ConsumerLagMs:     10,
	})
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{
		SendFailureCount: 2,
		RedeliveryCount:  1,
		CommitCount:      1,
		ConsumerLagMs:    30,
	})
	finalizeServerTelemetrySummary(summary)

	checks := []struct {
		name string
		got  int64
		want int64
	}{
		{"send_failure", summary.SendFailureCount, 3},
		{"redelivery", summary.RedeliveryCount, 3},
		{"dead_lettered", summary.DeadLetteredCount, 1},
		{"duplicate", summary.DuplicateCount, 1},
		{"out_of_order", summary.OutOfOrderCount, 1},
		{"commit", summary.CommitCount, 4},
		{"abort", summary.AbortCount, 1},
	}
	for _, c := range checks {
		if c.got != c.want {
			t.Errorf("%s counter = %d, want %d (counters must be summed totals, not averaged)", c.name, c.got, c.want)
		}
	}
	if summary.AvgConsumerLagMs != 20 {
		t.Errorf("AvgConsumerLagMs = %v, want 20 (float fields still averaged)", summary.AvgConsumerLagMs)
	}
}

// TestControlOnlyTelemetryDoesNotDiluteAverages locks in the fix that control
// ops (jms_commit/jms_rollback/kafka_commit), which carry only reliability
// counters, accumulate those counters but are NOT counted toward the averaging
// divisor — so they cannot dilute average payload/latency/throughput.
func TestControlOnlyTelemetryDoesNotDiluteAverages(t *testing.T) {
	summary := &ServerTelemetrySummary{}
	// One real measured consume sample.
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{
		ResponsePayloadKB: 10,
		MessagesReceived:  1,
		MessageLatencyMs:  20,
	})
	// A commit and a rollback (control-only).
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{CommitCount: 1})
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{AbortCount: 1})
	finalizeServerTelemetrySummary(summary)

	if summary.Samples != 1 {
		t.Fatalf("Samples = %d, want 1 (control ops must not count as samples)", summary.Samples)
	}
	if summary.AvgResponsePayloadKB != 10 {
		t.Fatalf("AvgResponsePayloadKB = %v, want 10 (must not be diluted by control ops)", summary.AvgResponsePayloadKB)
	}
	if summary.AvgMessageLatencyMs != 20 {
		t.Fatalf("AvgMessageLatencyMs = %v, want 20 (must not be diluted)", summary.AvgMessageLatencyMs)
	}
	if summary.CommitCount != 1 || summary.AbortCount != 1 {
		t.Fatalf("counters commit=%d abort=%d, want 1/1 (must accumulate from control ops)", summary.CommitCount, summary.AbortCount)
	}
}
