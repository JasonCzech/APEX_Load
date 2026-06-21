package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// TestMergeDistributedEventHubsTelemetry verifies the EH.4 streaming-ingestion
// signals survive the controller-side merge: counters accumulate as totals,
// end-to-end latency pools across generators, the per-partition produce map is
// merged across generators, and skew is recomputed POST-merge from the pooled
// map (not averaged from per-generator skews).
func TestMergeDistributedEventHubsTelemetry(t *testing.T) {
	m := &TxMetrics{}
	// Generator A: produces to partitions 0 (x2) and 1 (x1), one throttle.
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "0", RequestPayloadKB: 1})
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "0", RequestPayloadKB: 1})
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "1", RequestPayloadKB: 1})
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{ThrottledRequests: 1, ServerBusyCount: 1, TransferMs: 2})
	// Generator B: produces to partition 1 (x1) + an observer consume.
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "1", RequestPayloadKB: 1})
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesReceived: 1, EndToEndLatencyMs: 50, ResponsePayloadKB: 1})

	finalizeAPIServerTelemetrySummary(m.ServerTelemetry)

	st := m.ServerTelemetry
	if st == nil {
		t.Fatal("ServerTelemetry summary is nil")
	}
	if st.TotalThrottledRequests != 1 || st.TotalServerBusyCount != 1 {
		t.Fatalf("counters throttle=%d serverBusy=%d, want 1/1", st.TotalThrottledRequests, st.TotalServerBusyCount)
	}
	// Pooled per-partition map: partition 0 -> 2, partition 1 -> 2 (1 from each
	// generator). The breakdown is retained for the dashboard.
	if st.PartitionEvents["0"] != 2 || st.PartitionEvents["1"] != 2 {
		t.Fatalf("pooled PartitionEvents = %v, want {0:2, 1:2}", st.PartitionEvents)
	}
	// {0:2, 1:2} is perfectly even -> skew 0.
	if st.PartitionSkewPct != 0 {
		t.Fatalf("PartitionSkewPct = %v, want 0 for even pooled {0:2,1:2}", st.PartitionSkewPct)
	}
	// One consume sample with E2E 50; produces have no E2E. Because the merge here
	// mixes them (artificial — real runs bucket per transaction), the average is
	// 50 / Samples. Assert the max instead, which is dilution-independent.
	if st.MaxEndToEndLatencyMs != 50 {
		t.Fatalf("MaxEndToEndLatencyMs = %v, want 50", st.MaxEndToEndLatencyMs)
	}
}

// TestMergeDistributedEventHubsSkewRecomputedPostMerge proves skew is a property
// of the POOLED partition map: each generator alone looks even, but pooled they
// are uneven, so skew must be positive only after merging.
func TestMergeDistributedEventHubsSkewRecomputedPostMerge(t *testing.T) {
	m := &TxMetrics{}
	// Generator A: only partition 0. Generator B: only partition 0 again (so the
	// pooled map is {0: 3} -> single partition -> skew 0). Then add partition 1
	// once so pooled {0:3, 1:1} is uneven.
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "0", RequestPayloadKB: 1})
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "0", RequestPayloadKB: 1})
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "0", RequestPayloadKB: 1})
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{MessagesSent: 1, PartitionID: "1", RequestPayloadKB: 1})
	finalizeAPIServerTelemetrySummary(m.ServerTelemetry)
	if m.ServerTelemetry.PartitionSkewPct <= 0 {
		t.Fatalf("pooled {0:3,1:1} should have positive skew, got %v", m.ServerTelemetry.PartitionSkewPct)
	}
}
