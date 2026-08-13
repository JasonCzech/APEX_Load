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
// has its bounded typed transaction map passed through the same compaction, so
// this is also what makes the two paths agree.
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

// TestCompactTerminalManagedTestRetainsCISLAServerTelemetryPeaks pins the
// evidence terminal CI SLA evaluation still needs after the per-transaction map
// is released. max_avg_server_processing_ms / max_avg_transfer_ms are evaluated
// against the worst transaction and have no other terminal source, so dropping
// Transactions without this bounded rollup makes every terminal GitHub Check
// report them as "Not available".
func TestCompactTerminalManagedTestRetainsCISLAServerTelemetryPeaks(t *testing.T) {
	live := unboundedTerminalLiveMetrics()
	live.Transactions = map[string]*TxMetrics{
		"browse": {Count: 5, ServerTelemetry: &ServerTelemetrySummary{
			Samples: 6, AvgServerProcessingMs: 120, AvgTransferMs: 95,
		}},
		"checkout": {Count: 3, ServerTelemetry: &ServerTelemetrySummary{
			Samples: 4, AvgServerProcessingMs: 310, AvgTransferMs: 12,
			RequestPayloadHistogram: map[string]int64{"1k": 4},
		}},
		"unsampled": {Count: 1, ServerTelemetry: &ServerTelemetrySummary{
			Samples: 0, AvgServerProcessingMs: 900, AvgTransferMs: 900,
		}},
	}
	testRun := &ManagedTest{Status: "COMPLETE", History: []*LiveMetrics{live}, LiveMetrics: live}

	compactTerminalManagedTestLocked(testRun)

	tail := testRun.LiveMetrics
	if tail.Transactions != nil {
		t.Fatalf("compaction stopped releasing the per-transaction map: %#v", tail.Transactions)
	}
	processing, ok := ciGitHubCheckMaxServerTelemetry(tail, slaTelemetryMetricAvgServerProcessingMs)
	if !ok || processing != 310 {
		t.Fatalf("terminal avg server processing = %v (available %t), want the worst transaction's 310", processing, ok)
	}
	transfer, ok := ciGitHubCheckMaxServerTelemetry(tail, slaTelemetryMetricAvgTransferMs)
	if !ok || transfer != 95 {
		t.Fatalf("terminal avg transfer = %v (available %t), want the worst transaction's 95", transfer, ok)
	}
	// The rollup is bounded by the metric set, not by transaction cardinality,
	// and it must not pin the histograms the retained transaction carried.
	if len(tail.slaTelemetryPeaks) != 2 {
		t.Fatalf("retained SLA telemetry peaks = %#v, want one entry per evaluated metric", tail.slaTelemetryPeaks)
	}
	peak := tail.slaTelemetryPeaks[slaTelemetryMetricAvgServerProcessingMs]
	if peak == nil || peak.RequestPayloadHistogram != nil {
		t.Fatalf("retained peak pinned unbounded payload histograms: %#v", peak)
	}
	// Metrics that compaction does not retain must degrade honestly rather than
	// report a max taken over the retained subset.
	if value, available := ciGitHubCheckMaxServerTelemetry(tail, "avg_consumer_lag_ms"); available {
		t.Fatalf("unretained metric reported %v as available terminal evidence", value)
	}
	// Compaction stays idempotent: a second pass must not lose the peaks.
	compactTerminalManagedTestLocked(testRun)
	if _, ok := ciGitHubCheckMaxServerTelemetry(testRun.LiveMetrics, slaTelemetryMetricAvgServerProcessingMs); !ok {
		t.Fatal("re-compacting an already-compacted terminal run dropped the retained SLA telemetry peaks")
	}
	// Compaction copies: the pre-compaction snapshots still see the live map.
	if live.Transactions == nil {
		t.Fatal("SLA peak retention mutated the live metrics point in place")
	}
}
