package api

import (
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
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

// Requests are counted beside transactions: the run's and each group's
// attempted and failed requests accumulate to date, a revised window replaces
// its earlier requests too, and the request error rate is failures over
// attempts.
func TestRunTotalsCountRequestsToDate(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	sample := func(at time.Time, requests, requestErrors int64) *LiveMetrics {
		live := runTotalsSample(at, 0, 2, map[string][2]int64{"Inventory": {0, 2}})
		live.Requests, live.RequestErrors = requests, requestErrors
		live.GroupMetrics["Inventory"].Requests, live.GroupMetrics["Inventory"].RequestErrors = requests, requestErrors
		return live
	}
	var acc runTotalsAccumulator
	first := sample(base, 4, 2)
	acc.apply(first, nil)
	acc.apply(sample(base.Add(time.Second), 6, 3), nil)
	revised := sample(base.Add(time.Second), 8, 4)
	acc.apply(revised, nil)
	if deref(revised.RunTotalRequests) != 12 || deref(revised.RunRequestErrors) != 6 || deref(revised.RunRequestErrorPct) != 50 {
		t.Fatalf("run requests = %v / %v / %v%%, want 12 / 6 / 50%%", deref(revised.RunTotalRequests), deref(revised.RunRequestErrors), deref(revised.RunRequestErrorPct))
	}
	// Every iteration failed: the transaction error rate is 100%, the
	// request error rate is what the target saw.
	if deref(revised.RunErrorPct) != 100 {
		t.Fatalf("run transaction error pct = %v, want 100", deref(revised.RunErrorPct))
	}
	inventory := revised.GroupMetrics["Inventory"]
	if deref(inventory.RunRequests) != 12 || deref(inventory.RunRequestErrors) != 6 || deref(inventory.RunRequestErrorPct) != 50 {
		t.Fatalf("Inventory run requests = %v / %v / %v%%, want 12 / 6 / 50%%", deref(inventory.RunRequests), deref(inventory.RunRequestErrors), deref(inventory.RunRequestErrorPct))
	}

	// A local run's new accumulator starts from the windows in its history.
	resumed := sample(base.Add(2*time.Second), 2, 0)
	var takeover runTotalsAccumulator
	takeover.apply(resumed, []*LiveMetrics{first, revised})
	if deref(resumed.RunTotalRequests) != 14 || deref(resumed.RunRequestErrors) != 6 || deref(resumed.GroupMetrics["Inventory"].RunRequests) != 14 {
		t.Fatalf("resumed run requests = %v / %v, Inventory %v; want 14 / 6, 14", deref(resumed.RunTotalRequests), deref(resumed.RunRequestErrors), deref(resumed.GroupMetrics["Inventory"].RunRequests))
	}

	// A distributed run's starts from the latest run totals.
	window := &runTotalsWindow{runTotalsCounts: runTotalsCounts{requests: 3, requestErrors: 1}, groups: map[string]runTotalsCounts{"Inventory": {requests: 3, requestErrors: 1}}}
	var distributed runTotalsAccumulator
	distributed.countWindow(window, []*LiveMetrics{revised}, nil)
	next := sample(base.Add(3*time.Second), 3, 1)
	distributed.apply(next, nil)
	if deref(next.RunTotalRequests) != 15 || deref(next.RunRequestErrors) != 7 || deref(next.GroupMetrics["Inventory"].RunRequests) != 15 {
		t.Fatalf("distributed run requests = %v / %v, Inventory %v; want 15 / 7, 15", deref(next.RunTotalRequests), deref(next.RunRequestErrors), deref(next.GroupMetrics["Inventory"].RunRequests))
	}
}

// Request totals that would cover only part of a run are left out of its
// samples, rather than shown as run to date: after a takeover whose history
// kept no request counters, or once a generator that does not count requests
// contributes a window. Transaction totals carry on.
func TestRunTotalsLeaveOutIncompleteRequestTotals(t *testing.T) {
	base := time.Unix(1_800_000_000, 0)
	counted := &runTotalsWindow{runTotalsCounts: runTotalsCounts{tx: 5, requests: 3, requestErrors: 1}, groups: map[string]runTotalsCounts{"Inventory": {tx: 5, requests: 3, requestErrors: 1}}}
	publish := func(acc *runTotalsAccumulator) *LiveMetrics {
		live := runTotalsSample(base.Add(time.Minute), 5, 0, map[string][2]int64{"Inventory": {5, 0}})
		live.Requests, live.RequestErrors, live.RequestRate = 3, 1, 3
		live.GroupMetrics["Inventory"].Requests, live.GroupMetrics["Inventory"].RequestRate = 3, 3
		acc.apply(live, nil)
		// Unknown totals take the window counts with them, so the page does
		// not switch population on a window that happens to hold none.
		if live.RunTotalRequests == nil && (live.Requests != 0 || live.RequestRate != 0 || live.GroupMetrics["Inventory"].Requests != 0) {
			t.Fatalf("sample without request totals kept window requests %d at %v/s", live.Requests, live.RequestRate)
		}
		return live
	}

	older := runTotalsSample(base, 10, 0, nil)
	older.RunTotalTx, older.RunTotalErrors = ptrTo(int64(10)), ptrTo(int64(0))
	var upgraded runTotalsAccumulator
	upgraded.countWindow(counted, []*LiveMetrics{older}, nil)
	if live := publish(&upgraded); deref(live.RunTotalTx) != 15 || live.RunTotalRequests != nil || live.RunRequestErrorPct != nil || live.GroupMetrics["Inventory"].RunRequests != nil {
		t.Fatalf("after a point without request totals: %v tx, requests %v, Inventory %v; want 15 and none", deref(live.RunTotalTx), live.RunTotalRequests, live.GroupMetrics["Inventory"].RunRequests)
	}

	stored := []*LiveMetrics{runTotalsSample(base, 300, 3, nil), runTotalsSample(base.Add(time.Second), 200, 0, nil)}
	var adopted runTotalsAccumulator
	adopted.countWindow(counted, stored, []TestGroup{{Name: "Inventory"}})
	if live := publish(&adopted); deref(live.RunTotalTx) != 505 || live.RunTotalRequests != nil {
		t.Fatalf("after a takeover from stored history: %v tx, requests %v; want 505 and none", deref(live.RunTotalTx), live.RunTotalRequests)
	}

	var mixed runTotalsAccumulator
	mixed.countWindow(counted, nil, nil)
	if live := publish(&mixed); deref(live.RunTotalRequests) != 3 || live.Requests != 3 {
		t.Fatalf("every generator counting: requests %v, window %d; want 3 and 3", live.RunTotalRequests, live.Requests)
	}
	mixed.countWindow(&runTotalsWindow{runTotalsCounts: runTotalsCounts{tx: 7}, requestsUnknown: true}, nil, nil)
	if live := publish(&mixed); deref(live.RunTotalTx) != 12 || live.RunTotalRequests != nil {
		t.Fatalf("after a window from a generator that does not count requests: %v tx, requests %v; want 12 and none", deref(live.RunTotalTx), live.RunTotalRequests)
	}
}

// The live sample a local run publishes carries the engine's request
// counters for the run and each group.
func TestLiveMetricsFromSnapshotCarryRequests(t *testing.T) {
	live := buildLiveMetricsFromSnapshot(engine.MetricsSnapshot{
		Window: 2 * time.Second, Transactions: 1, Errors: 2, Requests: 6, RequestErrors: 2, RequestRate: 3,
		ByGroup: map[string]*engine.GroupMetrics{"Inventory": {Name: "Inventory", Errors: 2, Requests: 4, RequestErrors: 2, RequestRate: 2}},
	}, nil)
	if live.Requests != 6 || live.RequestErrors != 2 || live.RequestRate != 3 {
		t.Fatalf("sample requests = %d / %d at %v/s, want 6 / 2 at 3/s", live.Requests, live.RequestErrors, live.RequestRate)
	}
	if group := live.GroupMetrics["Inventory"]; group == nil || group.Requests != 4 || group.RequestErrors != 2 || group.RequestRate != 2 {
		t.Fatalf("Inventory = %+v, want 4 / 2 at 2/s", group)
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

	if live.Requests != 0 || live.RunTotalRequests != nil {
		t.Fatalf("Cart has no request run counters, filtered view = %d window, %v run", live.Requests, live.RunTotalRequests)
	}

	requests := &LiveMetrics{
		Requests: 50, RequestErrors: 5, RequestRate: 50, RunTotalRequests: ptrTo(int64(900)), RunRequestErrors: ptrTo(int64(90)),
		GroupMetrics: map[string]*GroupLiveMetrics{
			"Browse":    {Name: "Browse", Requests: 30, RequestRate: 30, RunTransactions: ptrTo(int64(1)), RunErrors: ptrTo(int64(0)), RunRequests: ptrTo(int64(600)), RunRequestErrors: ptrTo(int64(0))},
			"Inventory": {Name: "Inventory", Requests: 20, RequestErrors: 5, RequestRate: 20, RunTransactions: ptrTo(int64(0)), RunErrors: ptrTo(int64(9)), RunRequests: ptrTo(int64(300)), RunRequestErrors: ptrTo(int64(90))},
		},
	}
	applySelectedGroupAggregate(requests, []*GroupLiveMetrics{requests.GroupMetrics["Inventory"]})
	if requests.Requests != 20 || requests.RequestErrors != 5 || requests.RequestRate != 20 {
		t.Fatalf("filtered window requests = %d / %d at %v/s, want Inventory's 20 / 5 at 20/s", requests.Requests, requests.RequestErrors, requests.RequestRate)
	}
	if deref(requests.RunTotalRequests) != 300 || deref(requests.RunRequestErrors) != 90 || deref(requests.RunRequestErrorPct) != 30 {
		t.Fatalf("filtered run requests = %v / %v / %v%%, want 300 / 90 / 30%%", deref(requests.RunTotalRequests), deref(requests.RunRequestErrors), deref(requests.RunRequestErrorPct))
	}

	legacy := &LiveMetrics{RunTotalTx: ptrTo(int64(1000)), RunTotalRequests: ptrTo(int64(500)), GroupMetrics: map[string]*GroupLiveMetrics{"Old": {Name: "Old", Transactions: 3, RunTransactions: ptrTo(int64(3)), RunErrors: ptrTo(int64(0))}}}
	applySelectedGroupAggregate(legacy, []*GroupLiveMetrics{legacy.GroupMetrics["Old"]})
	if deref(legacy.RunTotalTx) != 3 || legacy.RunTotalRequests != nil {
		t.Fatalf("filtered view of a group without request run counters = %v tx, %v requests; want 3 and absent", deref(legacy.RunTotalTx), legacy.RunTotalRequests)
	}
	older := &LiveMetrics{RunTotalTx: ptrTo(int64(1000)), GroupMetrics: map[string]*GroupLiveMetrics{"Old": {Name: "Old", Transactions: 3}}}
	applySelectedGroupAggregate(older, []*GroupLiveMetrics{older.GroupMetrics["Old"]})
	if older.RunTotalTx != nil {
		t.Fatalf("filtered view kept whole-test run totals for a group without run counters: %v", *older.RunTotalTx)
	}
}
