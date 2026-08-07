package api

import (
	"testing"
	"time"
)

func unboundedTerminalLiveMetrics() *LiveMetrics {
	return &LiveMetrics{
		Timestamp:        time.Unix(120, 0).UTC(),
		TotalTx:          3,
		ActiveVUsers:     4,
		P99Ms:            12,
		ErrorBreakdown:   map[string]int64{"500": 7},
		Transactions:     map[string]*TxMetrics{"checkout": {Count: 3}},
		DependencyHealth: map[string]*TxMetrics{"payments-db": {Count: 3}},
		DependencyMap:    &LiveDependencyMap{},
		ErrorPatterns:    []LiveErrorPattern{{Signature: "timeout"}},
		SOAPOperations:   map[string]*TxMetrics{"GetQuote": {Count: 1}},
		NetworkProfiles:  map[string]*TxMetrics{"3g": {Count: 1}},
		ErrorEvidence:    []LiveErrorEvidence{{}},
		GroupMetrics:     map[string]*GroupLiveMetrics{"buyers": {}},
		Browser:          &BrowserLiveMetrics{Samples: 5},
	}
}

// TestCompactTerminalManagedTestReleasesUnboundedMetricBreakdowns pins the
// resident cost of a run that terminalizes in-process. The single retained
// metrics point stays in tr.tests for the lifetime of the controller, so it
// must not keep the per-transaction / per-error / per-dependency maps whose
// size grows with the workload's cardinality. The same run re-hydrated at boot
// never carries them (intervalSummaryRecordToLiveMetrics does not populate
// them), so this is also what makes the two paths agree.
func TestCompactTerminalManagedTestReleasesUnboundedMetricBreakdowns(t *testing.T) {
	live := unboundedTerminalLiveMetrics()
	testRun := &ManagedTest{
		Status:      "COMPLETE",
		History:     []*LiveMetrics{{TotalTx: 1}, {TotalTx: 2}, live},
		LiveMetrics: live,
	}

	compactTerminalManagedTestLocked(testRun)

	if len(testRun.History) != 1 {
		t.Fatalf("compacted history = %d points, want 1", len(testRun.History))
	}
	tail := testRun.History[0]
	if tail.TotalTx != 3 {
		t.Fatalf("retained point = %#v, want the latest point", tail)
	}
	if testRun.LiveMetrics != tail {
		t.Fatalf("LiveMetrics (%p) and the retained history point (%p) diverged", testRun.LiveMetrics, tail)
	}
	if tail.ErrorBreakdown != nil || tail.Transactions != nil || tail.DependencyHealth != nil ||
		tail.DependencyMap != nil || tail.ErrorPatterns != nil || tail.SOAPOperations != nil ||
		tail.NetworkProfiles != nil || tail.ErrorEvidence != nil {
		t.Fatalf("terminal run retained cardinality-unbounded breakdowns: %#v", tail)
	}
	// Bounded rollups and scalars are still evidence a terminal list view needs.
	if tail.ActiveVUsers != 4 || tail.P99Ms != 12 || tail.Browser == nil || tail.GroupMetrics == nil {
		t.Fatalf("terminal compaction dropped bounded evidence: %#v", tail)
	}
	// Compaction must copy, never mutate: snapshots taken before compaction
	// (archive record, terminal event payload) still reference the live point.
	if live.Transactions == nil || live.ErrorBreakdown == nil {
		t.Fatal("compaction mutated the live metrics point in place")
	}

	// Repeated compaction of an already-compacted run must not reallocate.
	compactTerminalManagedTestLocked(testRun)
	if testRun.History[0] != tail || testRun.LiveMetrics != tail {
		t.Fatal("re-compacting an already-compacted terminal run replaced the retained point")
	}
}

// TestCompactTerminalManagedTestCompactsLiveMetricsWithoutHistory covers the
// path where the retained point is only reachable through LiveMetrics.
func TestCompactTerminalManagedTestCompactsLiveMetricsWithoutHistory(t *testing.T) {
	live := unboundedTerminalLiveMetrics()
	testRun := &ManagedTest{Status: "COMPLETE", LiveMetrics: live, Sparkline: []TestSparklinePoint{{}}}

	compactTerminalManagedTestLocked(testRun)

	if testRun.LiveMetrics == nil {
		t.Fatal("compaction dropped LiveMetrics entirely")
	}
	if testRun.LiveMetrics.Transactions != nil || testRun.LiveMetrics.ErrorBreakdown != nil ||
		testRun.LiveMetrics.DependencyHealth != nil || testRun.LiveMetrics.ErrorEvidence != nil {
		t.Fatalf("history-less terminal run retained unbounded breakdowns: %#v", testRun.LiveMetrics)
	}
	if testRun.LiveMetrics.TotalTx != 3 {
		t.Fatalf("compacted LiveMetrics lost its scalars: %#v", testRun.LiveMetrics)
	}
}
