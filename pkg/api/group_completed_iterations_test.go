package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
)

// A local run's groups carry their completed iterations; a group the engine
// does not count leaves the field out rather than reporting zero.
func TestLocalLiveGroupsCarryCompletedIterations(t *testing.T) {
	live := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{ByGroup: map[string]*engine.GroupMetrics{
		"checkout": {Name: "checkout", CompletedIterations: 12, CompletedIterationsReported: true},
		"browse":   {Name: "browse"},
	}}, nil)
	if got := live.GroupMetrics["checkout"].CompletedIterations; got == nil || *got != 12 {
		t.Fatalf("checkout iterations = %v, want 12", got)
	}
	if got := live.GroupMetrics["browse"].CompletedIterations; got != nil {
		t.Fatalf("browse iterations = %d, want not reported", *got)
	}
}

// Across generators a group's completed iterations are the sum of each
// generator's running total; one generator that does not count them leaves
// the group's count unknown rather than short.
func TestDistributedLiveGroupsSumCompletedIterations(t *testing.T) {
	combined, tps, groups := aggregateDistributedMetricsBatches("run", map[string]generatorrpc.MetricsBatch{
		"gen-a": {Groups: []generatorrpc.GroupMetric{
			{Name: "checkout", ActiveVUsers: 2, CompletedIterations: ptr(int64(5))},
			{Name: "browse", ActiveVUsers: 1, CompletedIterations: ptr(int64(2))},
		}},
		"gen-b": {Groups: []generatorrpc.GroupMetric{
			{Name: "checkout", ActiveVUsers: 2, CompletedIterations: ptr(int64(4))},
			{Name: "browse", ActiveVUsers: 1},
		}},
	})
	live := buildLiveMetricsFromDistributedBatch(&ManagedTest{}, combined, tps, groups)
	if got := live.GroupMetrics["checkout"].CompletedIterations; got == nil || *got != 9 {
		t.Fatalf("checkout iterations = %v, want 9", got)
	}
	if got := live.GroupMetrics["browse"].CompletedIterations; got != nil {
		t.Fatalf("browse iterations = %d, want unknown", *got)
	}
}

// A terminal batch rebuilt without its counters keeps each group's last
// reported iterations, and never overwrites ones it reports.
func TestTerminalBatchCarriesGroupCompletedIterations(t *testing.T) {
	groups := []generatorrpc.GroupMetric{{Name: "checkout"}, {Name: "browse", CompletedIterations: ptr(int64(9))}, {Name: "search"}}
	prior := []generatorrpc.GroupMetric{{Name: "checkout", CompletedIterations: ptr(int64(5))}, {Name: "browse", CompletedIterations: ptr(int64(7))}}
	carried := carryGroupCompletedIterations(groups, prior)
	if carried[0].CompletedIterations == nil || *carried[0].CompletedIterations != 5 || *carried[1].CompletedIterations != 9 || carried[2].CompletedIterations != nil {
		t.Fatalf("carried = %+v, want checkout 5, browse 9, search unknown", carried)
	}
	if groups[0].CompletedIterations != nil {
		t.Fatal("the terminal batch's own groups were modified")
	}
}

// A generator's terminal batch rebuilt without its counters keeps the
// groups' iterations its previous batch reported.
func TestStoredTerminalBatchKeepsGroupCompletedIterations(t *testing.T) {
	runner, testRun, backend := newDistributedThresholdTestRunner(t)
	running := distributedP99Batch(testRun.ID, 1000, 100)
	running.BatchSequence = 1
	running.Groups = []generatorrpc.GroupMetric{{Name: "shop", ActiveVUsers: 1, CompletedIterations: ptr(int64(5))}}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-a", running) {
		t.Fatal("running batch rejected")
	}
	terminal := distributedP99Batch(testRun.ID, 2000, 100)
	terminal.BatchSequence = 2
	terminal.LifecycleState = "COMPLETE"
	terminal.Groups = []generatorrpc.GroupMetric{{Name: "shop"}}
	if !backend.storeRemoteMetricsBatchOnly(runner, testRun, "generator-a", terminal) {
		t.Fatal("terminal batch rejected")
	}
	stored := testRun.remoteMetricsBatchesByTarget["generator-a"]
	if len(stored.Groups) != 1 || stored.Groups[0].CompletedIterations == nil || *stored.Groups[0].CompletedIterations != 5 {
		t.Fatalf("stored terminal groups = %+v, want shop with 5 iterations", stored.Groups)
	}
}
