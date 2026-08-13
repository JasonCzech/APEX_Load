package api

import (
	"fmt"
	"testing"
	"time"
)

// TestComparisonBaselineSurvivesTerminalCompactionOfTheBaselineRun proves a run
// that completed in this controller process still serves as the run-comparison
// baseline for the next run of the same scenario.
//
// Terminal compaction releases the cardinality-unbounded metric maps from the
// single retained metric point, and Transactions/DependencyHealth are exactly
// the two fields the baseline selector reads. The baseline builder has no
// archive fallback - it only inspects in-memory candidates - so without a
// breakdown captured before compaction, comparison_baseline disappears from
// every response for every run, not just after a restart.
func TestComparisonBaselineSurvivesTerminalCompactionOfTheBaselineRun(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	completedAt := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC)
	tail := &LiveMetrics{
		Timestamp: completedAt,
		Transactions: map[string]*TxMetrics{
			"POST /checkout": {Name: "POST /checkout", Count: 120, AvgMs: 210, P95Ms: 480},
		},
		DependencyHealth: map[string]*TxMetrics{
			"api.example.test": {Name: "api.example.test", DependencyType: "http", Count: 120, AvgMs: 180},
		},
		ErrorEvidence: []LiveErrorEvidence{{}},
	}
	baseline := &ManagedTest{
		ID:          "baseline",
		Name:        "Checkout Run",
		Status:      "RUNNING",
		ScenarioID:  "scenario-1",
		CreatedAt:   completedAt.Add(-5 * time.Minute),
		LiveMetrics: tail,
		History:     []*LiveMetrics{tail},
	}
	runner.tests[baseline.ID] = baseline
	runner.tests["current"] = &ManagedTest{
		ID:         "current",
		Name:       "Checkout Run",
		Status:     "RUNNING",
		ScenarioID: "scenario-1",
		CreatedAt:  completedAt.Add(10 * time.Minute),
	}

	// Terminalize the baseline run exactly the way every completion path does.
	runner.mu.Lock()
	baseline.Status = "COMPLETE"
	baseline.EndedAt = &completedAt
	compactTerminalManagedTestLocked(baseline)
	runner.mu.Unlock()

	// Guard the premise: the resident point really has been stripped, so a
	// passing assertion below can only come from the retained breakdown.
	runner.mu.RLock()
	residentTransactions := len(baseline.LiveMetrics.Transactions)
	residentDependencies := len(baseline.LiveMetrics.DependencyHealth)
	residentHistory := len(baseline.History)
	residentEvidence := len(baseline.LiveMetrics.ErrorEvidence)
	runner.mu.RUnlock()
	if residentTransactions != 0 || residentDependencies != 0 {
		t.Fatalf("terminal compaction no longer strips the resident tail (transactions=%d dependencies=%d); this test's premise is stale",
			residentTransactions, residentDependencies)
	}
	if residentHistory != 1 {
		t.Fatalf("retained history points = %d, want the single compacted tail", residentHistory)
	}
	if residentEvidence != 0 {
		t.Fatalf("compaction retained %d error-evidence entries; only the comparison breakdown may survive", residentEvidence)
	}

	current, ok := runner.GetTest("current")
	if !ok {
		t.Fatal("current run not found")
	}
	if current.ComparisonBaseline == nil {
		t.Fatal("comparison baseline is nil after the baseline run completed in-process")
	}
	if current.ComparisonBaseline.SourceTestID != "baseline" {
		t.Fatalf("baseline source = %q, want baseline", current.ComparisonBaseline.SourceTestID)
	}
	tx := current.ComparisonBaseline.Transactions["POST /checkout"]
	if tx == nil {
		t.Fatalf("baseline transactions = %#v, want the per-transaction breakdown", current.ComparisonBaseline.Transactions)
	}
	if tx.Count != 120 || tx.P95Ms != 480 {
		t.Fatalf("baseline transaction = %#v, want the terminal point's own values", tx)
	}
	if current.ComparisonBaseline.DependencyHealth["api.example.test"] == nil {
		t.Fatalf("baseline dependency health = %#v, want the dependency breakdown",
			current.ComparisonBaseline.DependencyHealth)
	}
}

// TestComparisonBreakdownCaptureIsPromotionOnly proves a second compaction of
// the same run cannot erase what the first captured. loadStoredTerminalTestSummary
// re-compacts a terminal run after loading stored history, and the retained
// breakdown must survive that second pass.
func TestComparisonBreakdownCaptureIsPromotionOnly(t *testing.T) {
	completedAt := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC)
	tail := &LiveMetrics{
		Timestamp:    completedAt,
		Transactions: map[string]*TxMetrics{"POST /checkout": {Name: "POST /checkout", Count: 5}},
	}
	test := &ManagedTest{
		ID:          "baseline",
		Status:      "COMPLETE",
		EndedAt:     &completedAt,
		LiveMetrics: tail,
		History:     []*LiveMetrics{tail},
	}
	compactTerminalManagedTestLocked(test)
	if test.comparisonBreakdown == nil || len(test.comparisonBreakdown.Transactions) != 1 {
		t.Fatalf("first compaction captured %#v, want the breakdown", test.comparisonBreakdown)
	}
	compactTerminalManagedTestLocked(test)
	if test.comparisonBreakdown == nil || len(test.comparisonBreakdown.Transactions) != 1 {
		t.Fatalf("second compaction dropped the captured breakdown: %#v", test.comparisonBreakdown)
	}
}

// TestComparisonBreakdownCaptureIsBounded proves the retained copy cannot
// reintroduce the unbounded residency terminal compaction exists to prevent:
// a dynamically named workload is truncated to the busiest entries.
func TestComparisonBreakdownCaptureIsBounded(t *testing.T) {
	transactions := make(map[string]*TxMetrics, maxComparisonBaselineBreakdownEntries*2)
	for index := 0; index < maxComparisonBaselineBreakdownEntries*2; index++ {
		name := fmt.Sprintf("GET /item/%04d", index)
		transactions[name] = &TxMetrics{Name: name, Count: int64(index)}
	}
	completedAt := time.Date(2026, time.August, 7, 10, 0, 0, 0, time.UTC)
	tail := &LiveMetrics{Timestamp: completedAt, Transactions: transactions}
	test := &ManagedTest{
		ID:          "wide",
		Status:      "COMPLETE",
		EndedAt:     &completedAt,
		LiveMetrics: tail,
		History:     []*LiveMetrics{tail},
	}
	compactTerminalManagedTestLocked(test)
	if test.comparisonBreakdown == nil {
		t.Fatal("no breakdown captured for a wide workload")
	}
	if got := len(test.comparisonBreakdown.Transactions); got != maxComparisonBaselineBreakdownEntries {
		t.Fatalf("retained transactions = %d, want the %d-entry bound", got, maxComparisonBaselineBreakdownEntries)
	}
	busiest := fmt.Sprintf("GET /item/%04d", maxComparisonBaselineBreakdownEntries*2-1)
	if test.comparisonBreakdown.Transactions[busiest] == nil {
		t.Fatalf("busiest transaction %q was truncated away", busiest)
	}
	if test.comparisonBreakdown.Transactions["GET /item/0000"] != nil {
		t.Fatal("quietest transaction survived a truncation that dropped busier ones")
	}
}
