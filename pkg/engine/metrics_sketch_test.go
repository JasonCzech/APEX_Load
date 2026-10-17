package engine

import (
	"testing"
	"time"
)

// Every distribution a window sends carries the sketch, covering all of its
// observations, beside the t-digest that older controllers merge.
func TestSnapshotSendsSketchesBesideTDigests(t *testing.T) {
	results, _ := timeoutBoundaryResults(1, 500, 5)
	snap := NewMetricsCollector(10).computeSnapshot(results, time.Second)
	check := func(name string, count int64, digest *TDigestSnapshot, sketch *DurationSketchSnapshot, p99 time.Duration) {
		t.Helper()
		restored := DurationSketchForCount(sketch, count)
		if restored == nil || digest == nil || digest.Count != count {
			t.Fatalf("%s: sketch restores %v, digest %+v; want both covering %d observations", name, restored != nil, digest, count)
		}
		if got := time.Duration(restored.Quantile(0.99)); got != p99 {
			t.Fatalf("%s: p99 %v, the sent sketch's %v", name, p99, got)
		}
	}
	transaction := snap.ByTransaction["Checkout"]
	check("transaction", transaction.Count, transaction.DurationDigest, transaction.DurationSketch, transaction.P99)
	for name, action := range snap.ByAction {
		check(name, action.Count, action.DurationDigest, action.DurationSketch, action.P99)
	}
	group := snap.ByGroup["shop"]
	check("group", group.Transactions+group.Errors, group.DurationDigest, group.DurationSketch, group.P99)
}

func TestCloneDurationSketchSnapshotIsIndependent(t *testing.T) {
	if CloneDurationSketchSnapshot(nil) != nil {
		t.Fatal("clone of nil is not nil")
	}
	sketch := NewDurationSketch()
	sketch.Add(1e6)
	sketch.Add(2e6)
	snapshot := sketch.Snapshot()
	clone := CloneDurationSketchSnapshot(&snapshot)
	clone.Bins[0].Count = 99
	if snapshot.Bins[0].Count == 99 {
		t.Fatal("clone shares its buckets with the original")
	}
}

func TestDurationSketchForCountRefusesSketchesOfOtherObservations(t *testing.T) {
	sketch := NewDurationSketch()
	sketch.Add(1e6)
	sketch.Add(2e6)
	snapshot := sketch.Snapshot()
	if DurationSketchForCount(&snapshot, 2) == nil {
		t.Fatal("refused a sketch of exactly its observations")
	}
	for _, count := range []int64{0, 1, 3} {
		if DurationSketchForCount(&snapshot, count) != nil {
			t.Fatalf("restored a 2-observation sketch for %d observations", count)
		}
	}
	invalid := snapshot
	invalid.Version = 1
	if DurationSketchForCount(&invalid, 2) != nil || DurationSketchForCount(nil, 2) != nil {
		t.Fatal("restored an invalid or missing sketch")
	}
}
