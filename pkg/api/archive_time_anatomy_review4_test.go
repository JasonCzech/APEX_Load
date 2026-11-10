package api

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"
)

// taDispatch marks a row as recorded during async dispatch epoch (the
// engine's stamp: async_dispatch=true plus async_dispatch_epoch).
func taDispatch(event TransactionEvent, epoch int64) TransactionEvent {
	event.ProtocolEvidence = map[string]any{"async_dispatch": true, timeAnatomyDispatchEpochKey: epoch}
	return event
}

// A transaction opened inside an http_async callback claims the callback's
// own requests (same dispatch epoch), as the engine's sourceTransactionTakes
// rolls them up; a transaction opened outside the dispatch never claims
// them, and a dispatch request of another epoch is not claimed either.
// Archives without epochs keep the old exclude-all rule.
func TestTimeAnatomyCallbackOpenedTransactionClaimsItsDispatchRequests(t *testing.T) {
	pollReq := taDispatch(taRequest(1, 11, 1, 20, 30, "poll-req", 2), 7)
	pollReq.Phase = "async on_response"
	pollReq.TTFBMs, pollReq.ConnectMs = 20, 5
	branch := taDispatch(taRequest(1, 11, 1, 25, 10, "branch-req", 3), 7)
	other := taDispatch(taRequest(1, 11, 1, 30, 10, "other-dispatch", 4), 9)
	legacy := taRequest(1, 11, 1, 35, 10, "legacy-callback", 5)
	legacy.Phase = "async on_response"
	conversation := taDispatch(taRequest(1, 11, 1, 40, 10, "conversation", 6), 7)
	conversation.ProtocolEvidence["async_conversation"] = "poll"
	poll := taDispatch(taTransaction(1, 11, 1, 18, 40, "poll"), 7)
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 10, "A", 1),
		pollReq,
		branch,
		other,
		legacy,
		conversation,
		poll,
		taTransaction(1, 11, 1, 0, 100, "Login"),
	}
	anatomy := timeAnatomyForTest(t, events)

	entry := taEntry(t, anatomy, "poll")
	if entry.JoinedInstances != 1 || entry.Decomposition.AvgRequestsPerInstance != 2 {
		t.Fatalf("poll = joined %d decomposition %+v, want poll-req and branch-req", entry.JoinedInstances, entry.Decomposition)
	}
	var names []string
	for _, row := range entry.Requests {
		names = append(names, row.ActionName)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"branch-req", "poll-req"}) {
		t.Fatalf("poll rows %v, want [branch-req poll-req]", names)
	}
	if entry.Decomposition.Phases.ConnectMs <= 0 || entry.Decomposition.Phases.ServerMs <= 0 {
		t.Fatalf("poll decomposition %+v, want poll-req's connect and server time", entry.Decomposition)
	}

	login := taEntry(t, anatomy, "Login")
	if login.JoinedInstances != 1 || login.Decomposition.AvgRequestsPerInstance != 1 || len(login.Requests) != 1 || login.Requests[0].ActionName != "A" {
		t.Fatalf("Login = joined %d decomposition %+v rows %+v, want A only", login.JoinedInstances, login.Decomposition, login.Requests)
	}
	// A, poll-req and branch-req are inside a transaction; other-dispatch
	// (epoch 9, no transaction of its own), the unstamped callback step and
	// the conversation response are outside.
	if anatomy.Coverage.RequestsInTransactions != 3 || anatomy.Coverage.RequestsOutsideTransactions != 3 {
		t.Fatalf("coverage %+v", anatomy.Coverage)
	}
}

// The exclusion and epoch rules on their own, including the shapes a decoded
// archive and an in-memory row carry.
func TestTimeAnatomyDispatchEpochRules(t *testing.T) {
	for _, test := range []struct {
		name     string
		phase    string
		evidence map[string]any
		excluded bool
		epoch    int64
	}{
		{"legacy callback phase", "async on_response", nil, true, 0},
		{"legacy dispatch stamp", "", map[string]any{"async_dispatch": true}, true, 0},
		{"stamped callback", "async on_response", map[string]any{"async_dispatch": true, "async_dispatch_epoch": float64(3)}, false, 3},
		{"stamped branch", "action", map[string]any{"async_dispatch": true, "async_dispatch_epoch": int64(4)}, false, 4},
		{"json number", "", map[string]any{"async_dispatch": true, "async_dispatch_epoch": json.Number("12")}, false, 12},
		{"zero epoch", "", map[string]any{"async_dispatch": true, "async_dispatch_epoch": float64(0)}, true, 0},
		{"negative epoch", "", map[string]any{"async_dispatch": true, "async_dispatch_epoch": float64(-2)}, true, 0},
		{"fractional epoch", "", map[string]any{"async_dispatch": true, "async_dispatch_epoch": 1.5}, true, 0},
		{"string epoch", "", map[string]any{"async_dispatch": true, "async_dispatch_epoch": "3"}, true, 0},
		{"huge epoch", "", map[string]any{"async_dispatch": true, "async_dispatch_epoch": 1e300}, true, 0},
		{"conversation with epoch", "", map[string]any{"async_conversation": "x", "async_dispatch_epoch": float64(3)}, true, 3},
		{"background with epoch", "", map[string]any{"background_request": true, "async_dispatch_epoch": float64(3)}, true, 3},
		{"main flow", "action", nil, false, 0},
	} {
		tx := &TransactionEvent{Phase: test.phase, ProtocolEvidence: test.evidence}
		if got := timeAnatomyRequestExcluded(tx); got != test.excluded {
			t.Errorf("%s: excluded = %v, want %v", test.name, got, test.excluded)
		}
		if got := timeAnatomyDispatchEpoch(tx); got != test.epoch {
			t.Errorf("%s: epoch = %d, want %d", test.name, got, test.epoch)
		}
	}
	window := timeAnatomyClaimWindow{StartNs: 0, EndNs: 100}
	for _, test := range []struct {
		windowEpoch, childEpoch int64
		want                    bool
	}{
		{0, 0, true}, {5, 0, true}, {5, 5, true}, {0, 5, false}, {6, 5, false},
	} {
		window.DispatchEpoch = test.windowEpoch
		if got := timeAnatomyClaimsRequest(window, timeAnatomyClaimChild{StartNs: 10, DispatchEpoch: test.childEpoch}); got != test.want {
			t.Errorf("window epoch %d child epoch %d: claimed %v, want %v", test.windowEpoch, test.childEpoch, got, test.want)
		}
	}
}

// A dispatch request written after a main-flow transaction closed is not a
// late row of it: that transaction could never have claimed it.
func TestTimeAnatomyDispatchRequestIsNotLateForMainFlow(t *testing.T) {
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 11, 1, 0, 10, "A", 1),
		taTransaction(1, 11, 1, 0, 100, "Login"),
		taDispatch(taRequest(1, 11, 1, 20, 10, "callback", 2), 3),
		taRequest(1, 11, 1, 30, 10, "late-main", 3),
	)
	anatomy := feeder.finalize(t)
	if anatomy.Coverage.LateRequestRows != 1 {
		t.Fatalf("late rows %d, want 1 (late-main only)", anatomy.Coverage.LateRequestRows)
	}
}

// taClosedWorkload feeds a closed workload: vusers VUsers, iterations
// iterations each, every iteration requests requests split across
// transactions sibling transactions plus one iteration-level transaction
// enclosing them all.
func taClosedWorkload(f *taFeeder, vusers, iterations, requests, transactions int) {
	perTx := requests / transactions
	for iteration := 0; iteration < iterations; iteration++ {
		for vu := 0; vu < vusers; vu++ {
			life := uint64(vu+1) * 7919
			base := float64(iteration)*float64(requests*10+100) + float64(vu%7)
			for tx := 0; tx < transactions; tx++ {
				start := base + float64(tx*perTx*10)
				for step := 0; step < perTx; step++ {
					f.feed(taRequest(vu+1, life, int64(iteration+1), start+float64(step*10), 8, fmt.Sprintf("R%02d", tx*perTx+step), tx*perTx+step+1))
				}
				f.feed(taTransaction(vu+1, life, int64(iteration+1), start, float64(perTx*10), fmt.Sprintf("T%d", tx)))
			}
			f.feed(taTransaction(vu+1, life, int64(iteration+1), base, float64(requests*10), "Iteration"))
		}
	}
}

// The work budget grows with the archive: a closed workload that spends
// several units per row runs out of a fixed budget of the same size, but
// never out of one that grows per row. Each subtest uses a floor small
// enough that a fixed budget is spent.
func TestTimeAnatomyWorkBudgetScalesWithRows(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyWork, int64(1<<12))
	setTimeAnatomyCap(t, &timeAnatomyWorkRowsPerFloor, int64(1<<4)) // 256 units per row
	feeder := newTAFeeder()
	taClosedWorkload(feeder, 100, 10, 28, 4)
	anatomy := feeder.finalize(t)
	if feeder.acc.work <= maxTimeAnatomyWork {
		t.Fatalf("work %d does not exceed the floor %d: the fixture does not prove scaling", feeder.acc.work, maxTimeAnatomyWork)
	}
	if slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonWorkBudget) || feeder.acc.workExhausted {
		t.Fatalf("an ordinary closed workload exhausted the budget: work %d of %d over %d rows", feeder.acc.work, timeAnatomyWorkBudget(feeder.acc.rows), feeder.acc.rows)
	}
	for _, name := range []string{"T0", "T3", "Iteration"} {
		if entry := taEntry(t, anatomy, name); entry.PartialInstances != 0 || entry.JoinedInstances != entry.Instances {
			t.Fatalf("%s = joined %d of %d partial %d", name, entry.JoinedInstances, entry.Instances, entry.PartialInstances)
		}
	}
	if perRow := float64(feeder.acc.work) / float64(feeder.acc.rows); perRow > 64 {
		t.Fatalf("an ordinary closed workload spends %.1f units per row, want at most a quarter of the 256 allowed", perRow)
	}
}

// The budget's arithmetic: the floor alone for a small floor (tests that set
// maxTimeAnatomyWork keep their absolute budget), 256 units per row by
// default, saturating rather than overflowing.
func TestTimeAnatomyWorkBudgetArithmetic(t *testing.T) {
	if got := timeAnatomyWorkBudget(0); got != maxTimeAnatomyWork {
		t.Fatalf("budget(0) = %d", got)
	}
	if got, want := timeAnatomyWorkBudget(5_000_000), maxTimeAnatomyWork+256*5_000_000; got != want {
		t.Fatalf("budget(5M) = %d, want %d", got, want)
	}
	if got := timeAnatomyWorkBudget(1 << 62); got <= 0 {
		t.Fatalf("budget(2^62) = %d, want saturation", got)
	}
	setTimeAnatomyCap(t, &maxTimeAnatomyWork, int64(400))
	if got := timeAnatomyWorkBudget(1_000_000); got != 400 {
		t.Fatalf("budget with a 400-unit floor = %d, want 400", got)
	}
}

// A hostile archive still spends at most O(rows): wide sibling windows over
// one full identity buffer exhaust the per-row allowance, and the work never
// passes the budget for the rows merged.
func TestTimeAnatomyHostileWorkStaysPerRowBounded(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyWork, int64(1<<10))
	setTimeAnatomyCap(t, &timeAnatomyWorkRowsPerFloor, int64(1<<6)) // 16 units per row
	feeder := newTAFeeder()
	for index := 0; index < 1000; index++ {
		feeder.feed(taRequest(1, 11, 1, float64(index), 0.5, "R", 1))
	}
	for range 400 {
		feeder.feed(taTransaction(1, 11, 1, 0, 2000, "Wide"))
		if budget := timeAnatomyWorkBudget(feeder.acc.rows); feeder.acc.work > budget {
			t.Fatalf("work %d over the budget %d for %d rows", feeder.acc.work, budget, feeder.acc.rows)
		}
	}
	anatomy := feeder.finalize(t)
	if !feeder.acc.workExhausted || !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonWorkBudget) {
		t.Fatalf("hostile windows never exhausted the budget: work %d rows %d", feeder.acc.work, feeder.acc.rows)
	}
}

// A joined instance is charged for folding its children, not only for its
// candidate scan.
func TestTimeAnatomyJoinedChildrenAreCharged(t *testing.T) {
	const children = 300
	feeder := newTAFeeder()
	for index := 0; index < children; index++ {
		feeder.feed(taRequest(1, 11, 1, float64(index), 0.5, "R", 1))
	}
	before := feeder.acc.work
	feeder.feed(taTransaction(1, 11, 1, 0, 400, "Wide"))
	if spent, want := feeder.acc.work-before, int64(children+1)+timeAnatomyJoinWork(children); spent != want {
		t.Fatalf("a joined instance of %d children charged %d units, want %d (scan plus fold)", children, spent, want)
	}
	// When the budget pays for the scan but not the fold, the instance is
	// partial and its children stay claimed.
	setTimeAnatomyCap(t, &maxTimeAnatomyWork, int64(children+children+10))
	feeder = newTAFeeder()
	for index := 0; index < children; index++ {
		feeder.feed(taRequest(1, 11, 1, float64(index), 0.5, "R", 1))
	}
	feeder.feed(taTransaction(1, 11, 1, 0, 400, "Wide"))
	anatomy := feeder.finalize(t)
	entry := taEntry(t, anatomy, "Wide")
	if entry.PartialInstances != 1 || entry.JoinedInstances != 0 || anatomy.Coverage.RequestsInTransactions != children {
		t.Fatalf("Wide = joined %d partial %d, in transactions %d", entry.JoinedInstances, entry.PartialInstances, anatomy.Coverage.RequestsInTransactions)
	}
	if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonWorkBudget) {
		t.Fatalf("reasons %v", anatomy.Truncation.Reasons)
	}
}

// An evicted request a transaction already claimed is not reported as
// evicted (of unknown placement): it is in requests_in_transactions. An
// unclaimed one is.
func TestTimeAnatomyEvictedClaimedRequestsAreNotUnknown(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyIdentities, 1)
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 11, 1, 0, 10, "A", 1),
		taRequest(1, 11, 1, 10, 10, "B", 2),
		taTransaction(1, 11, 1, 0, 20, "T"),
		taRequest(1, 11, 1, 50, 10, "Loose", 3),
	)
	feeder.feed(taRequest(2, 22, 1, 100, 10, "C", 1)) // evicts identity 1
	feeder.feed(taTransaction(2, 22, 1, 100, 20, "U"))
	anatomy := feeder.finalize(t)
	if anatomy.Truncation.IdentitiesEvicted != 1 {
		t.Fatalf("identities evicted %d", anatomy.Truncation.IdentitiesEvicted)
	}
	if anatomy.Truncation.PendingRequestsEvicted != 1 || feeder.acc.evictedClaimed != 2 {
		t.Fatalf("evicted unclaimed %d claimed %d, want 1 (Loose) and 2 (A, B)", anatomy.Truncation.PendingRequestsEvicted, feeder.acc.evictedClaimed)
	}
	coverage := anatomy.Coverage
	if coverage.RequestsInTransactions+coverage.RequestsOutsideTransactions+anatomy.Truncation.PendingRequestsEvicted+anatomy.Truncation.PendingRequestsDropped != coverage.RequestRows {
		t.Fatalf("coverage %+v truncation %+v does not add up to request_rows", coverage, anatomy.Truncation)
	}
}

// Once the budget runs out, a buffered request that every window able to
// claim it had already scanned is still counted as outside any transaction;
// only one a refused window might have claimed is of unknown placement
// (dropped).
func TestTimeAnatomyWorkExhaustedKeepsKnownOutsideRequests(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyWork, int64(60))
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 1, 1, 0, 10, "Before", 1), // runs before T, outside it
		taRequest(1, 1, 1, 100, 10, "A", 2),
		taTransaction(1, 1, 1, 100, 20, "T"),
		taRequest(1, 1, 1, 500, 10, "B", 3),
	)
	taFiller(feeder, 10_000, 1_000_000, 40)
	if !feeder.acc.workExhausted {
		t.Fatal("the filler did not exhaust the budget")
	}
	feeder.feed(taTransaction(1, 1, 1, 490, 50, "Late")) // refused: B's placement is unknown
	anatomy := feeder.finalize(t)
	if outside := anatomy.Coverage.RequestsOutsideTransactions; outside < 1 {
		t.Fatalf("requests_outside_transactions = %d, want Before counted outside", outside)
	}
	if anatomy.Truncation.PendingRequestsDropped == 0 {
		t.Fatalf("truncation %+v, want B dropped", anatomy.Truncation)
	}
	coverage := anatomy.Coverage
	if coverage.RequestsInTransactions+coverage.RequestsOutsideTransactions+anatomy.Truncation.PendingRequestsEvicted+anatomy.Truncation.PendingRequestsDropped != coverage.RequestRows {
		t.Fatalf("coverage %+v truncation %+v does not add up to request_rows", coverage, anatomy.Truncation)
	}
}

// A 5M-row closed workload (500 VUsers, 28 requests in 4 transactions plus
// an iteration transaction per iteration) runs under the default budget
// without exhausting it.
func TestTimeAnatomyFiveMillionRowsStayInsideTheBudget(t *testing.T) {
	if testing.Short() {
		t.Skip("5M rows")
	}
	feeder := newTAFeeder()
	// 33 rows per VUser iteration: 500 x 304 x 33 = 5,016,000 rows.
	taClosedWorkload(feeder, 500, 304, 28, 4)
	if feeder.acc.rows < 5_000_000 {
		t.Fatalf("fed %d rows", feeder.acc.rows)
	}
	anatomy := feeder.finalize(t)
	if feeder.acc.workExhausted || slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonWorkBudget) {
		t.Fatalf("5M rows exhausted the budget: work %d of %d", feeder.acc.work, timeAnatomyWorkBudget(feeder.acc.rows))
	}
	if entry := taEntry(t, anatomy, "Iteration"); entry.PartialInstances != 0 || entry.JoinedInstances != entry.Instances {
		t.Fatalf("Iteration = joined %d of %d partial %d", entry.JoinedInstances, entry.Instances, entry.PartialInstances)
	}
	t.Logf("work %d over %d rows (%.1f per row; budget %d)", feeder.acc.work, feeder.acc.rows, float64(feeder.acc.work)/float64(feeder.acc.rows), timeAnatomyWorkBudget(feeder.acc.rows))
}

// BenchmarkTimeAnatomy5MRowsClosedWorkload is the 5M-row closed workload.
func BenchmarkTimeAnatomy5MRowsClosedWorkload(b *testing.B) {
	b.ReportAllocs()
	for range b.N {
		feeder := newTAFeeder()
		taClosedWorkload(feeder, 500, 304, 28, 4)
		b.ReportMetric(float64(feeder.acc.work)/float64(feeder.acc.rows), "work/row")
		anatomy := feeder.finalize(b)
		if feeder.acc.workExhausted || slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonWorkBudget) {
			b.Fatalf("work budget exhausted: %d of %d", feeder.acc.work, timeAnatomyWorkBudget(feeder.acc.rows))
		}
	}
}
