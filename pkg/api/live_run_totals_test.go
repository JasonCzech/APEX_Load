package api

import (
	"math"
	"testing"
	"time"
)

func runTotalsSample(at time.Time, tx, errs int64, groups map[string][2]int64) *LiveMetrics {
	live := &LiveMetrics{Timestamp: at, TotalTx: tx, TotalErrors: errs, GroupMetrics: map[string]*GroupLiveMetrics{}}
	for name, counts := range groups {
		live.GroupMetrics[name] = &GroupLiveMetrics{Name: name, Transactions: counts[0], Errors: counts[1]}
	}
	return live
}

func TestRunTotalsAccumulateWindowsIntoRunToDate(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	var acc runTotalsAccumulator
	// A run failing ~1%: most one-second windows carry no error at all, which
	// is exactly why the window error rate read 0.00% on a failing run.
	windows := []struct{ tx, errs int64 }{{99, 1}, {100, 0}, {100, 0}, {98, 2}, {100, 0}}
	var last *LiveMetrics
	for i, w := range windows {
		last = runTotalsSample(base.Add(time.Duration(i)*time.Second), w.tx, w.errs, nil)
		acc.apply(last, nil)
	}
	if last.ErrorPct != 0 {
		t.Fatalf("window error pct should stay the window value, got %v", last.ErrorPct)
	}
	if deref(last.RunTotalTx) != 497 || deref(last.RunTotalErrors) != 3 {
		t.Fatalf("run totals = %d tx / %d errors, want 497 / 3", deref(last.RunTotalTx), deref(last.RunTotalErrors))
	}
	if want := 3.0 / 500 * 100; math.Abs(deref(last.RunErrorPct)-want) > 1e-9 {
		t.Fatalf("run error pct = %v, want %v", deref(last.RunErrorPct), want)
	}
}

func TestRunTotalsRevisionReplacesLatestWindowAndIgnoresOlder(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	var acc runTotalsAccumulator
	acc.apply(runTotalsSample(base, 10, 0, map[string][2]int64{"Browse": {10, 0}}), nil)
	acc.apply(runTotalsSample(base.Add(time.Second), 5, 1, map[string][2]int64{"Browse": {5, 1}}), nil)

	// A distributed revision of the latest window folds in a late generator.
	revised := runTotalsSample(base.Add(time.Second), 8, 2, map[string][2]int64{"Browse": {6, 1}, "Cart": {2, 1}})
	acc.apply(revised, nil)
	if deref(revised.RunTotalTx) != 18 || deref(revised.RunTotalErrors) != 2 {
		t.Fatalf("after revision totals = %d / %d, want 18 / 2", deref(revised.RunTotalTx), deref(revised.RunTotalErrors))
	}
	if browse := revised.GroupMetrics["Browse"]; deref(browse.RunTransactions) != 16 || deref(browse.RunErrors) != 1 {
		t.Fatalf("Browse run totals = %d / %d, want 16 / 1", deref(browse.RunTransactions), deref(browse.RunErrors))
	}
	if cart := revised.GroupMetrics["Cart"]; deref(cart.RunTransactions) != 2 || deref(cart.RunErrors) != 1 || math.Abs(deref(cart.RunErrorPct)-100.0/3) > 1e-9 {
		t.Fatalf("Cart run totals = %+v", cart)
	}

	// An out-of-order older window must not be counted twice.
	stale := runTotalsSample(base, 10, 0, nil)
	acc.apply(stale, nil)
	if deref(stale.RunTotalTx) != 18 || deref(stale.RunTotalErrors) != 2 {
		t.Fatalf("stale window changed totals to %d / %d", deref(stale.RunTotalTx), deref(stale.RunTotalErrors))
	}
}

func TestRunTotalsSeedFromHistoryAfterFailover(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	history := []*LiveMetrics{
		runTotalsSample(base, 50, 5, map[string][2]int64{"Checkout": {50, 5}}),
		runTotalsSample(base.Add(time.Second), 40, 0, map[string][2]int64{"Checkout": {40, 0}}),
	}
	var acc runTotalsAccumulator
	next := runTotalsSample(base.Add(2*time.Second), 10, 0, map[string][2]int64{"Checkout": {10, 0}})
	acc.apply(next, history)
	if deref(next.RunTotalTx) != 100 || deref(next.RunTotalErrors) != 5 {
		t.Fatalf("seeded totals = %d / %d, want 100 / 5", deref(next.RunTotalTx), deref(next.RunTotalErrors))
	}
	if got := next.GroupMetrics["Checkout"]; deref(got.RunTransactions) != 100 || deref(got.RunErrors) != 5 {
		t.Fatalf("seeded group totals = %+v", got)
	}
}

func TestPublishLiveMetricsCarriesRunTotals(t *testing.T) {
	runner := NewTestRunner(1, NewHub(), nil)
	run := &ManagedTest{ID: "run-totals", Status: "RUNNING"}
	runner.tests[run.ID] = run
	base := time.Now().Add(-time.Minute)
	for i, counts := range [][2]int64{{100, 1}, {100, 0}, {99, 1}} {
		if !runner.publishLiveMetrics(run, runTotalsSample(base.Add(time.Duration(i)*time.Second), counts[0], counts[1], nil), nil) {
			t.Fatalf("publish %d was not installed", i)
		}
	}
	runner.mu.RLock()
	defer runner.mu.RUnlock()
	if deref(run.LiveMetrics.RunTotalTx) != 299 || deref(run.LiveMetrics.RunTotalErrors) != 2 {
		t.Fatalf("installed run totals = %d / %d, want 299 / 2", deref(run.LiveMetrics.RunTotalTx), deref(run.LiveMetrics.RunTotalErrors))
	}
	if got := deref(run.History[len(run.History)-1].RunErrorPct); math.Abs(got-2.0/301*100) > 1e-9 {
		t.Fatalf("history run error pct = %v", got)
	}
}

func deref[T any](value *T) T {
	var zero T
	if value == nil {
		return zero
	}
	return *value
}

func TestGroupFilterScopesRunTotalsToSelectedGroups(t *testing.T) {
	live := &LiveMetrics{
		TotalTx: 20, TotalErrors: 2, RunTotalTx: ptrTo(int64(1000)), RunTotalErrors: ptrTo(int64(40)),
		GroupMetrics: map[string]*GroupLiveMetrics{
			"Browse": {Name: "Browse", Transactions: 12, Errors: 1, RunTransactions: ptrTo(int64(600)), RunErrors: ptrTo(int64(10))},
			"Cart":   {Name: "Cart", Transactions: 8, Errors: 1, RunTransactions: ptrTo(int64(400)), RunErrors: ptrTo(int64(30))},
		},
	}
	applySelectedGroupAggregate(live, []*GroupLiveMetrics{live.GroupMetrics["Cart"]})
	if deref(live.RunTotalTx) != 400 || deref(live.RunTotalErrors) != 30 || math.Abs(deref(live.RunErrorPct)-30.0/430*100) > 1e-9 {
		t.Fatalf("filtered run totals = %v / %v / %v, want Cart's 400 / 30", deref(live.RunTotalTx), deref(live.RunTotalErrors), deref(live.RunErrorPct))
	}

	legacy := &LiveMetrics{RunTotalTx: ptrTo(int64(1000)), GroupMetrics: map[string]*GroupLiveMetrics{"Old": {Name: "Old", Transactions: 3}}}
	applySelectedGroupAggregate(legacy, []*GroupLiveMetrics{legacy.GroupMetrics["Old"]})
	if legacy.RunTotalTx != nil {
		t.Fatalf("filtered view kept whole-test run totals for a group without run counters: %v", *legacy.RunTotalTx)
	}
}
