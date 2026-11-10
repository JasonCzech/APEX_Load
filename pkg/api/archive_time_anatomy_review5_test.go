package api

import (
	"slices"
	"testing"
)

// taCoverageAddsUp asserts in + outside + evicted + dropped == request_rows.
func taCoverageAddsUp(t *testing.T, anatomy *RunArchiveTimeAnatomy) {
	t.Helper()
	coverage := anatomy.Coverage
	if coverage.RequestsInTransactions+coverage.RequestsOutsideTransactions+anatomy.Truncation.PendingRequestsEvicted+anatomy.Truncation.PendingRequestsDropped != coverage.RequestRows {
		t.Fatalf("coverage %+v truncation %+v does not add up to request_rows", coverage, anatomy.Truncation)
	}
}

// A buffered request the per-identity cap displaces after a transaction
// already claimed it is in requests_in_transactions; it is not counted as
// dropped (of unknown placement) too. The cap is still a truncation reason
// and an enclosing window over the drop is still partial.
func TestTimeAnatomyKeyCapDisplacedClaimedRequestIsNotDropped(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, 2)
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 11, 1, 0, 10, "A", 1),
		taRequest(1, 11, 1, 10, 10, "B", 2),
		taTransaction(1, 11, 1, 0, 30, "T"),
		taRequest(1, 11, 1, 100, 10, "C", 3), // displaces claimed A
		taRequest(1, 11, 1, 110, 10, "D", 4), // displaces claimed B
		taTransaction(1, 11, 1, 100, 30, "U"),
		taRequest(1, 11, 1, 200, 10, "E", 5),     // displaces claimed C
		taRequest(1, 11, 1, 210, 10, "Loose", 6), // displaces claimed D
		taRequest(1, 11, 1, 220, 10, "F", 7),     // displaces unclaimed E
		taTransaction(1, 11, 1, 0, 300, "Iteration"),
	)
	anatomy := feeder.finalize(t)
	if anatomy.Truncation.PendingRequestsDropped != 1 || feeder.acc.evictedClaimed != 4 {
		t.Fatalf("dropped %d claimed-displaced %d, want 1 (E) and 4 (A-D)", anatomy.Truncation.PendingRequestsDropped, feeder.acc.evictedClaimed)
	}
	if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonPendingKeyCap) {
		t.Fatalf("reasons %v, want pending_key_cap", anatomy.Truncation.Reasons)
	}
	if entry := taEntry(t, anatomy, "Iteration"); entry.PartialInstances != 1 {
		t.Fatalf("Iteration partial %d, want 1 (it encloses dropped requests)", entry.PartialInstances)
	}
	taCoverageAddsUp(t, anatomy)
}

// When every request the per-VUser cap displaced was already matched and no
// later transaction encloses the drop, nothing was lost: no pending_key_cap
// reason ("the oldest were not matched") and no partial instance.
func TestTimeAnatomyKeyCapAllClaimedReportsNoReason(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, 2)
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 11, 1, 0, 10, "A", 1),
		taRequest(1, 11, 1, 10, 10, "B", 2),
		taTransaction(1, 11, 1, 0, 30, "T"),
		taRequest(1, 11, 1, 100, 10, "C", 3), // displaces claimed A
		taRequest(1, 11, 1, 110, 10, "D", 4), // displaces claimed B
		taTransaction(1, 11, 1, 100, 30, "U"),
	)
	anatomy := feeder.finalize(t)
	if anatomy.Truncation.PendingRequestsDropped != 0 || feeder.acc.evictedClaimed != 2 {
		t.Fatalf("dropped %d claimed-displaced %d, want 0 and 2", anatomy.Truncation.PendingRequestsDropped, feeder.acc.evictedClaimed)
	}
	if slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonPendingKeyCap) {
		t.Fatalf("reasons %v: pending_key_cap reported although every displaced request was matched", anatomy.Truncation.Reasons)
	}
	for _, name := range []string{"T", "U"} {
		if entry := taEntry(t, anatomy, name); entry.PartialInstances != 0 {
			t.Fatalf("%s partial %d, want 0", name, entry.PartialInstances)
		}
	}
	taCoverageAddsUp(t, anatomy)
}

// Once the work budget is spent, a refused main-flow window cannot leave an
// unclaimed dispatch request's placement unknown: no main-flow window ever
// claims one, so it ran outside every transaction. A refused window of a
// dispatch still makes it unknown.
func TestTimeAnatomyRefusedMainFlowWindowKeepsDispatchRequestsOutside(t *testing.T) {
	for _, test := range []struct {
		name          string
		refused       TransactionEvent
		outside, drop int64
	}{
		{"main-flow refused", taTransaction(1, 1, 1, 0, 200, "Late"), 1, 0},
		{"dispatch refused", taDispatch(taTransaction(1, 1, 1, 0, 200, "Late"), 5), 0, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			setTimeAnatomyCap(t, &maxTimeAnatomyWork, int64(60))
			feeder := newTAFeeder()
			feeder.feed(taDispatch(taRequest(1, 1, 1, 100, 10, "callback", 1), 5))
			taFiller(feeder, 10_000, 1_000_000, 40)
			if !feeder.acc.workExhausted {
				t.Fatal("the filler did not exhaust the budget")
			}
			feeder.feed(test.refused)
			anatomy := feeder.finalize(t)
			// Isolate the callback from the filler's own requests by
			// comparing against the same run without it.
			baseline := newTAFeeder()
			taFiller(baseline, 10_000, 1_000_000, 40)
			baseline.feed(test.refused)
			base := baseline.finalize(t)
			if got := anatomy.Coverage.RequestsOutsideTransactions - base.Coverage.RequestsOutsideTransactions; got != test.outside {
				t.Fatalf("callback outside %d, want %d", got, test.outside)
			}
			if got := anatomy.Truncation.PendingRequestsDropped - base.Truncation.PendingRequestsDropped; got != test.drop {
				t.Fatalf("callback dropped %d, want %d", got, test.drop)
			}
			taCoverageAddsUp(t, anatomy)
		})
	}
}
