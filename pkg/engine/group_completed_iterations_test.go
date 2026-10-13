package engine

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Each group's snapshot carries the iterations it has completed so far in
// the run, a running total rather than the window's; a group the collector
// does not count iterations for says so instead of reporting zero.
func TestComputeSnapshotCarriesEachGroupsCompletedIterations(t *testing.T) {
	mc := NewMetricsCollector(100)
	mc.RecordCompletedIteration("checkout")
	mc.RecordCompletedIteration("checkout")
	mc.RecordCompletedIteration("checkout")
	mc.RecordCompletedIteration("")
	results := []*protocol.Result{
		{BusinessTransaction: true, Success: true, Group: "checkout", Duration: 10 * time.Millisecond},
		{BusinessTransaction: true, Success: true, Group: "browse", Duration: 10 * time.Millisecond},
	}
	snap := mc.computeSnapshot(results, time.Second)
	if checkout := snap.ByGroup["checkout"]; checkout == nil || !checkout.CompletedIterationsReported || checkout.CompletedIterations != 3 {
		t.Fatalf("checkout = %#v, want 3 reported iterations", checkout)
	}
	if browse := snap.ByGroup["browse"]; browse == nil || browse.CompletedIterationsReported {
		t.Fatalf("browse = %#v, want iterations not reported", browse)
	}
	if snap.CompletedIterations != 4 {
		t.Fatalf("run completed iterations = %d, want 4", snap.CompletedIterations)
	}
	// The next window still carries the run's total, not the window's.
	mc.RecordCompletedIteration("checkout")
	next := mc.computeSnapshot([]*protocol.Result{{BusinessTransaction: true, Success: true, Group: "checkout", Duration: time.Millisecond}}, time.Second)
	if checkout := next.ByGroup["checkout"]; checkout == nil || checkout.CompletedIterations != 4 {
		t.Fatalf("checkout next window = %#v, want 4", checkout)
	}
}
