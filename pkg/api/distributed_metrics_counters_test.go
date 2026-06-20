package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// TestMergeDistributedServerTelemetryReliabilityCounters verifies the P1.1
// reliability counters survive the distributed (controller-side) merge path and
// accumulate as totals across transactions.
func TestMergeDistributedServerTelemetryReliabilityCounters(t *testing.T) {
	m := &TxMetrics{}
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{
		SendFailureCount:  1,
		RedeliveryCount:   2,
		DeadLetteredCount: 1,
		DuplicateCount:    1,
		OutOfOrderCount:   1,
		CommitCount:       3,
		AbortCount:        1,
	})
	mergeDistributedServerTelemetry(m, &generatorrpc.ServerTelemetry{
		SendFailureCount: 2,
		CommitCount:      1,
	})

	st := m.ServerTelemetry
	if st == nil {
		t.Fatal("ServerTelemetry summary is nil")
	}
	if st.SendFailureCount != 3 || st.RedeliveryCount != 2 || st.DeadLetteredCount != 1 ||
		st.DuplicateCount != 1 || st.OutOfOrderCount != 1 || st.CommitCount != 4 || st.AbortCount != 1 {
		t.Fatalf("unexpected reliability counters: send=%d redeliv=%d dlq=%d dup=%d ooo=%d commit=%d abort=%d",
			st.SendFailureCount, st.RedeliveryCount, st.DeadLetteredCount, st.DuplicateCount,
			st.OutOfOrderCount, st.CommitCount, st.AbortCount)
	}
}
