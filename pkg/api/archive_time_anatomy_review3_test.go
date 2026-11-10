package api

import (
	"slices"
	"testing"
	"time"
)

// Requests recorded during async dispatch (an http_async callback's own
// requests, phase "async <branch>", and parallel branches a callback started,
// protocol_evidence async_dispatch=true) are left out of the live rollup, so
// the report never claims them either.
func TestTimeAnatomyAsyncDispatchRequestsNotClaimed(t *testing.T) {
	callback := taRequest(1, 11, 1, 20, 10, "GET /ack", 2)
	callback.Phase = "async on_response"
	branch := taRequest(1, 11, 1, 40, 10, "GET /branch", 3)
	branch.Phase = "action"
	branch.ProtocolEvidence = map[string]any{"async_dispatch": true}
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 10, "A", 1),
		callback,
		branch,
		taTransaction(1, 11, 1, 0, 100, "Login"),
	}
	anatomy := timeAnatomyForTest(t, events)
	entry := taEntry(t, anatomy, "Login")
	if entry.JoinedInstances != 1 || entry.Decomposition.AvgRequestsPerInstance != 1 || len(entry.Requests) != 1 || entry.Requests[0].ActionName != "A" {
		t.Fatalf("Login = joined %d decomposition %+v rows %+v, want A only", entry.JoinedInstances, entry.Decomposition, entry.Requests)
	}
	if anatomy.Coverage.RequestsOutsideTransactions != 2 || anatomy.Coverage.RequestsInTransactions != 1 {
		t.Fatalf("coverage %+v", anatomy.Coverage)
	}
	for _, test := range []struct {
		phase    string
		evidence map[string]any
		want     bool
	}{
		{"async on_response", nil, true},
		{"async on_request", map[string]any{}, true},
		{"Async On_Body_Chunk", nil, true},
		{"async", nil, false},
		{"asynchronous", nil, false},
		{"action", nil, false},
		{"", map[string]any{"async_dispatch": true}, true},
		{"", map[string]any{"async_dispatch": false}, false},
		{"", map[string]any{"async_dispatch": "true"}, false},
	} {
		if got := timeAnatomyRequestExcluded(&TransactionEvent{Phase: test.phase, ProtocolEvidence: test.evidence}); got != test.want {
			t.Errorf("excluded(phase %q, %v) = %v, want %v", test.phase, test.evidence, got, test.want)
		}
	}
}

// An iteration flush that releases requests is charged to the work budget.
func TestTimeAnatomyIterationFlushIsCharged(t *testing.T) {
	const requests = 500
	feeder := newTAFeeder()
	for index := 0; index < requests; index++ {
		feeder.feed(taRequest(1, 11, 1, float64(index), 0.5, "R", 1))
	}
	before := feeder.acc.work
	feeder.feed(taRequest(1, 11, 3, 1000, 0.5, "R", 1)) // retires iteration 1
	if feeder.acc.pendingTotal != 1 {
		t.Fatalf("pending %d after the flush, want 1", feeder.acc.pendingTotal)
	}
	if spent := feeder.acc.work - before; spent < requests {
		t.Fatalf("flush of %d requests charged %d work units", requests, spent)
	}
}

// taFlapWorkload is one identity whose request rows alternate between two
// adjacent iterations: nothing is ever retired, so the buffer stays full.
func taFlapWorkload(requests int) time.Duration {
	accumulator := newArchiveTimeAnatomyAccumulator()
	started := time.Now()
	for index := 0; index < requests; index++ {
		event := taRequest(1, 11, int64(1+index%2), float64(index), 0.5, "R", 1)
		accumulator.merge(&event, "action", int64(index))
	}
	return time.Since(started)
}

// Rows alternating between two adjacent iterations do not walk the full
// buffer per row: a flush that cannot retire anything is skipped.
func TestTimeAnatomyIterationFlappingCostIsFlat(t *testing.T) {
	const requests = 20_000
	measure := func(limit int) time.Duration {
		setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, limit)
		best := time.Duration(1 << 62)
		for range 3 {
			best = min(best, taFlapWorkload(requests))
		}
		return best
	}
	small, large := measure(128), measure(8192)
	if large > 8*small+20*time.Millisecond {
		t.Fatalf("flapping cost grows with the cap: cap 128 %v, cap 8192 %v", small, large)
	}
}

// When the current identity alone holds the global cap, every later request
// runs the eviction walk; that walk is charged, so it cannot outrun the
// budget.
func TestTimeAnatomyEvictionWalkIsCharged(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 16)
	feeder := newTAFeeder()
	for index := 0; index < 16; index++ {
		feeder.feed(taRequest(1, 11, 1, float64(index), 0.5, "R", 1))
	}
	before := feeder.acc.work
	const refused = 50
	for index := 0; index < refused; index++ {
		feeder.feed(taRequest(1, 11, 1, float64(100+index), 0.5, "R", 1))
	}
	if feeder.acc.pendingTotal != 16 {
		t.Fatalf("pending %d, want the cap 16 (current is never evicted)", feeder.acc.pendingTotal)
	}
	if spent := feeder.acc.work - before; spent < refused*16 {
		t.Fatalf("%d refused requests that each walked 16 entries charged %d work units", refused, spent)
	}
}

// Once the work budget is spent the join stops walking buffers (a later
// iteration does not flush), and requests buffered before it that no
// transaction could claim any more are counted as dropped, never as outside
// any transaction.
func TestTimeAnatomyWorkExhaustedStopsFlushesAndIsNotOutside(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyWork, 400)
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 1, 1, 0, 10, "A", 1),
		taRequest(1, 1, 1, 10, 10, "B", 2),
		taRequest(1, 1, 1, 20, 10, "C", 3),
	)
	taFiller(feeder, 1000, 1_000_000, 200)
	if !feeder.acc.workExhausted {
		t.Fatal("the filler did not exhaust the budget")
	}
	pending := feeder.acc.pendingTotal
	feeder.feed(taRequest(1, 1, 3, 5000, 10, "D", 1)) // would retire A, B and C
	if feeder.acc.pendingTotal != pending {
		t.Fatalf("pending %d -> %d: the buffer was walked after the budget was spent", pending, feeder.acc.pendingTotal)
	}
	feeder.feed(taTransaction(1, 1, 1, 0, 100, "Outer"))
	anatomy := feeder.finalize(t)
	if outer := taEntry(t, anatomy, "Outer"); outer.PartialInstances != 1 || outer.JoinedInstances != 0 {
		t.Fatalf("Outer = joined %d partial %d, want partial", outer.JoinedInstances, outer.PartialInstances)
	}
	if anatomy.Coverage.RequestsOutsideTransactions != 0 {
		t.Fatalf("requests_outside_transactions = %d, want 0 (every request had a transaction)", anatomy.Coverage.RequestsOutsideTransactions)
	}
	if anatomy.Truncation.PendingRequestsDropped < 4 || !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonWorkBudget) {
		t.Fatalf("truncation %+v", anatomy.Truncation)
	}
	if feeder.acc.work > 400 {
		t.Fatalf("work %d over budget", feeder.acc.work)
	}
}

// A legacy VUser ID reused after an ended iteration: the new lifetime's init
// requests (iteration 0) are live, not retired, so global pressure evicts
// genuinely retired requests first and the init transaction stays joined.
func TestTimeAnatomyLegacyReuseInitIsNotRetired(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 16)
	feeder := newTAFeeder()
	old := taTransaction(7, 0, 1, 0, 10, "Old")
	old.EndsIteration = true
	feeder.feed(taRequest(7, 0, 1, 0, 5, "R", 1), old)
	feeder.feed(
		taRequest(7, 0, 0, 20, 5, "I1", 1),
		taRequest(7, 0, 0, 25, 5, "I2", 2),
	)
	for index := 0; index < 6; index++ {
		feeder.feed(taRequest(9, 99, 1, float64(100+index*10), 5, "Z", 1))
	}
	for index := 0; index < 8; index++ {
		feeder.feed(taRequest(9, 99, 2, float64(200+index*10), 5, "Z", 1))
	}
	feeder.feed(taTransaction(7, 0, 0, 20, 20, "vuser_init"))
	anatomy := feeder.finalize(t)
	if feeder.acc.truncation.PendingRequestsEvicted == 0 {
		t.Fatal("no global-cap pressure: the fixture did not evict")
	}
	entry := taEntry(t, anatomy, "vuser_init")
	if entry.JoinedInstances != 1 || entry.PartialInstances != 0 || entry.Decomposition.AvgRequestsPerInstance != 2 {
		t.Fatalf("vuser_init = joined %d partial %d decomposition %+v", entry.JoinedInstances, entry.PartialInstances, entry.Decomposition)
	}
}
