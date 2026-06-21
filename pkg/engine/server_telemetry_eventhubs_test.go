package engine

import (
	"math"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestComputePartitionSkewPct(t *testing.T) {
	cases := []struct {
		name   string
		events map[string]int64
		want   float64
	}{
		{"empty", nil, 0},
		{"single partition", map[string]int64{"0": 100}, 0},
		{"perfectly even", map[string]int64{"0": 50, "1": 50, "2": 50}, 0},
		{"all zero", map[string]int64{"0": 0, "1": 0}, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ComputePartitionSkewPct(c.events); math.Abs(got-c.want) > 1e-9 {
				t.Fatalf("ComputePartitionSkewPct = %v, want %v", got, c.want)
			}
		})
	}

	// Skewed: one hot partition pushes the coefficient of variation up.
	if got := ComputePartitionSkewPct(map[string]int64{"0": 100, "1": 0}); got <= 0 {
		t.Fatalf("skewed distribution should yield positive skew, got %v", got)
	}
	// Two partitions {100,0}: mean 50, stddev 50, CV = 100%.
	if got := ComputePartitionSkewPct(map[string]int64{"0": 100, "1": 0}); math.Abs(got-100) > 1e-9 {
		t.Fatalf("CV of {100,0} = %v, want 100", got)
	}
}

func TestMergeServerTelemetryEventHubsCountersAreTotals(t *testing.T) {
	summary := &ServerTelemetrySummary{}
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{
		ThrottledRequests:  1,
		ServerBusyCount:    1,
		QuotaExceededCount: 1,
		EventTooLargeCount: 1,
		TransferMs:         5, // measured sample
	})
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{
		ThrottledRequests: 2,
		TransferMs:        5,
	})
	finalizeServerTelemetrySummary(summary)

	if summary.TotalThrottledRequests != 3 {
		t.Errorf("TotalThrottledRequests = %d, want 3", summary.TotalThrottledRequests)
	}
	if summary.TotalServerBusyCount != 1 {
		t.Errorf("TotalServerBusyCount = %d, want 1", summary.TotalServerBusyCount)
	}
	if summary.TotalQuotaExceededCount != 1 {
		t.Errorf("TotalQuotaExceededCount = %d, want 1", summary.TotalQuotaExceededCount)
	}
	if summary.TotalEventTooLargeCount != 1 {
		t.Errorf("TotalEventTooLargeCount = %d, want 1", summary.TotalEventTooLargeCount)
	}
}

func TestMergeServerTelemetryEventHubsPartitionSkew(t *testing.T) {
	// Produce transaction: three produces across two partitions (uneven 2:1).
	summary := &ServerTelemetrySummary{}
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{MessagesSent: 1, PartitionID: "0", RequestPayloadKB: 1})
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{MessagesSent: 1, PartitionID: "0", RequestPayloadKB: 1})
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{MessagesSent: 1, PartitionID: "1", RequestPayloadKB: 1})
	finalizeServerTelemetrySummary(summary)

	if summary.PartitionSkewPct <= 0 {
		t.Fatalf("PartitionSkewPct = %v, want > 0 for uneven {0:2, 1:1}", summary.PartitionSkewPct)
	}
	// The per-partition breakdown is retained for the dashboard, like size classes.
	if summary.PartitionEvents["0"] != 2 || summary.PartitionEvents["1"] != 1 {
		t.Fatalf("PartitionEvents = %v, want {0:2, 1:1}", summary.PartitionEvents)
	}
}

func TestMergeServerTelemetryEventHubsEndToEndLatency(t *testing.T) {
	// Observer-probe transaction: consumes only (homogeneous, mirroring how the
	// aggregator buckets per transaction — produces live in a separate summary).
	summary := &ServerTelemetrySummary{}
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{MessagesReceived: 1, EndToEndLatencyMs: 40, ResponsePayloadKB: 1})
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{MessagesReceived: 1, EndToEndLatencyMs: 20, ResponsePayloadKB: 1})
	finalizeServerTelemetrySummary(summary)

	if summary.AvgEndToEndLatencyMs != 30 {
		t.Fatalf("AvgEndToEndLatencyMs = %v, want 30 ((40+20)/2)", summary.AvgEndToEndLatencyMs)
	}
	if summary.MaxEndToEndLatencyMs != 40 {
		t.Fatalf("MaxEndToEndLatencyMs = %v, want 40", summary.MaxEndToEndLatencyMs)
	}
}

func TestConsumeProbeDoesNotPollutePartitionSkew(t *testing.T) {
	summary := &ServerTelemetrySummary{}
	// A consume probe that (hypothetically) carried a PartitionID must NOT add to
	// the produce-skew map, because MessagesSent == 0.
	mergeServerTelemetrySummary(summary, &protocol.ServerTelemetry{MessagesReceived: 1, PartitionID: "0", EndToEndLatencyMs: 10})
	finalizeServerTelemetrySummary(summary)
	if len(summary.PartitionEvents) != 0 {
		t.Fatalf("consume probe polluted PartitionEvents: %v", summary.PartitionEvents)
	}
}
