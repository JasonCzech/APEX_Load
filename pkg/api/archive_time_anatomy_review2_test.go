package api

import (
	"slices"
	"strings"
	"testing"
)

// taFiller feeds n single-iteration arrivals of three requests each, their
// transaction written right after them, with lifetimes firstLife.. and starts
// from offsetMs on.
func taFiller(f *taFeeder, offsetMs float64, firstLife uint64, n int) {
	for arrival := 0; arrival < n; arrival++ {
		vu, life := 100+arrival, firstLife+uint64(arrival)
		start := offsetMs + float64(arrival)*50
		for step := 0; step < 3; step++ {
			f.feed(taRequest(vu, life, 1, start+float64(step*10), 8, "F", step+1))
		}
		f.feed(taTransaction(vu, life, 1, start, 30, "Filler"))
	}
}

// Async-conversation responses and background requests are never part of a
// transaction (the live rollup's rule): a transaction window that contains
// them claims neither.
func TestTimeAnatomyAsyncAndBackgroundRequestsNotClaimed(t *testing.T) {
	poll := taRequest(1, 11, 1, 10, 500, "async poll", 2)
	poll.ProtocolEvidence = map[string]any{"async_conversation": "poll"}
	background := taRequest(1, 11, 1, 40, 20, "Beacon", 3)
	background.ProtocolEvidence = map[string]any{"async_conversation": "beacon", "background_request": true}
	ordinary := taRequest(1, 11, 1, 60, 10, "Other", 4)
	ordinary.ProtocolEvidence = map[string]any{"background_request": false}
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 30, "A", 1),
		poll,
		background,
		ordinary,
		taTransaction(1, 11, 1, 0, 100, "Login"),
	}
	anatomy := timeAnatomyForTest(t, events)
	entry := taEntry(t, anatomy, "Login")
	if entry.JoinedInstances != 1 || entry.Decomposition.AvgRequestsPerInstance != 2 || len(entry.Requests) != 2 {
		t.Fatalf("Login = joined %d decomposition %+v rows %+v, want A and Other only", entry.JoinedInstances, entry.Decomposition, entry.Requests)
	}
	for _, row := range entry.Requests {
		if row.ActionName != "A" && row.ActionName != "Other" {
			t.Fatalf("Login claimed %q", row.ActionName)
		}
	}
	if entry.Decomposition.OverlapMs != 0 || entry.Decomposition.Phases.ServerMs != 40 {
		t.Fatalf("decomposition %+v, want only A and Other's time", entry.Decomposition)
	}
	if anatomy.Coverage.RequestsOutsideTransactions != 2 || anatomy.Coverage.RequestsInTransactions != 2 || anatomy.Coverage.RequestRows != 4 {
		t.Fatalf("coverage %+v", anatomy.Coverage)
	}
	for _, test := range []struct {
		evidence map[string]any
		want     bool
	}{
		{nil, false},
		{map[string]any{}, false},
		{map[string]any{"async_conversation": "x"}, true},
		{map[string]any{"background_request": true}, true},
		{map[string]any{"background_request": false}, false},
		{map[string]any{"background_request": "true"}, false},
	} {
		if got := timeAnatomyRequestExcluded(&TransactionEvent{ProtocolEvidence: test.evidence}); got != test.want {
			t.Errorf("excluded(%v) = %v, want %v", test.evidence, got, test.want)
		}
	}
}

// A filtered buffer does not keep its peak backing array.
func TestTimeAnatomyFilteredBufferReleasesCapacity(t *testing.T) {
	var buffer timeAnatomyPendingBuffer
	for index := 0; index < 5000; index++ {
		buffer.insert(timeAnatomyPendingRequest{ordinal: int64(index), startNs: int64(index), iteration: int64(index / 10)})
	}
	buffer.filter(func(entry *timeAnatomyPendingRequest) bool { return entry.iteration >= 499 }, func(*timeAnatomyPendingRequest) {})
	if buffer.len() != 10 || cap(buffer.items) > 2*10+timeAnatomyPendingSlack {
		t.Fatalf("len %d cap %d after filtering to 10", buffer.len(), cap(buffer.items))
	}
	for index := 1; index < buffer.len(); index++ {
		if buffer.items[index-1].startNs > buffer.items[index].startNs {
			t.Fatal("filter lost the start order")
		}
	}
	buffer.filter(func(*timeAnatomyPendingRequest) bool { return false }, func(*timeAnatomyPendingRequest) {})
	if buffer.items != nil {
		t.Fatalf("empty buffer keeps cap %d", cap(buffer.items))
	}

	// Through the accumulator: one large iteration, then small ones.
	feeder := newTAFeeder()
	for index := 0; index < 3000; index++ {
		feeder.feed(taRequest(1, 11, 1, float64(index), 0.5, "Big", 1))
	}
	feeder.feed(taTransaction(1, 11, 1, 0, 3000, "T"))
	for iteration := int64(2); iteration <= 4; iteration++ {
		start := float64(iteration) * 10_000
		feeder.feed(taRequest(1, 11, iteration, start, 1, "Small", 1), taTransaction(1, 11, iteration, start, 1, "T"))
	}
	state := feeder.acc.identities[timeAnatomyIdentity{group: "g1", vuserID: 1, lifetime: 11}]
	if state == nil || cap(state.pending.items) > 2*state.pending.len()+timeAnatomyPendingSlack {
		t.Fatalf("identity buffer keeps cap %d for %d entries", cap(state.pending.items), state.pending.len())
	}
}

// An evicted request is counted as evicted, never as a request outside any
// transaction: the live VUser's transaction comes later and is partial.
func TestTimeAnatomyEvictedRequestsAreNotOutside(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 16)
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 1, 1, 0, 10, "A", 1),
		taRequest(1, 1, 1, 10, 10, "B", 2),
		taRequest(1, 1, 1, 20, 10, "C", 3),
	)
	taOpenWorkload(feeder, 20, 3, false)
	feeder.feed(taTransaction(1, 1, 1, 0, 100, "Outer"))
	anatomy := feeder.finalize(t)
	if anatomy.Truncation.PendingRequestsEvicted == 0 || anatomy.Coverage.PartialTransactionRows != 1 {
		t.Fatalf("truncation %+v coverage %+v", anatomy.Truncation, anatomy.Coverage)
	}
	if anatomy.Coverage.RequestsOutsideTransactions != 0 {
		t.Fatalf("requests_outside_transactions = %d, want 0 (evicted requests are not outside)", anatomy.Coverage.RequestsOutsideTransactions)
	}
	if feeder.acc.pendingTotal != 0 || feeder.acc.pendingBytes != 0 {
		t.Fatalf("after finalize pending %d bytes %d", feeder.acc.pendingTotal, feeder.acc.pendingBytes)
	}
}

// A live identity whose tombstone a colliding eviction overwrote still marks
// its returning transaction partial.
func TestTimeAnatomyOverwrittenTombstoneStaysPartial(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 16)
	const slots = uint64(1 << 14)
	x := timeAnatomyIdentity{group: "g1", vuserID: 1, lifetime: 1}
	var collider uint64
	for life := uint64(2); ; life++ {
		if timeAnatomyIdentityHash(timeAnatomyIdentity{group: "g1", vuserID: 2, lifetime: life})%slots == timeAnatomyIdentityHash(x)%slots {
			collider = life
			break
		}
	}
	feeder := newTAFeeder()
	feeder.feed(
		taRequest(1, 1, 1, 0, 10, "A", 1),
		taRequest(1, 1, 1, 10, 10, "B", 2),
		taRequest(1, 1, 1, 20, 10, "C", 3),
	)
	taFiller(feeder, 1000, 1_000_000, 10) // evicts X
	if feeder.acc.identities[x] != nil {
		t.Fatal("X was not evicted")
	}
	feeder.feed(
		taRequest(2, collider, 1, 5000, 10, "Y1", 1),
		taRequest(2, collider, 1, 5010, 10, "Y2", 2),
	)
	taFiller(feeder, 6000, 2_000_000, 10) // evicts Y, overwriting X's slot
	if feeder.acc.identities[timeAnatomyIdentity{group: "g1", vuserID: 2, lifetime: collider}] != nil {
		t.Fatal("Y was not evicted")
	}
	// X comes back: one more request, then its enclosing transaction, which
	// lost A, B and C.
	feeder.feed(taRequest(1, 1, 1, 30, 10, "D", 4), taTransaction(1, 1, 1, 0, 100, "Outer"))
	anatomy := feeder.finalize(t)
	outer := taEntry(t, anatomy, "Outer")
	if outer.JoinedInstances != 0 || outer.PartialInstances != 1 || outer.Decomposition.Instances != 0 {
		t.Fatalf("outer = joined %d partial %d decomposition %+v, want partial", outer.JoinedInstances, outer.PartialInstances, outer.Decomposition)
	}
	if filler := taEntry(t, anatomy, "Filler"); filler.JoinedInstances != 20 || filler.PartialInstances != 0 {
		t.Fatalf("filler joined %d partial %d, want 20/0", filler.JoinedInstances, filler.PartialInstances)
	}
}

// A late request row that an enclosing transaction still claims is not
// excluded, so it is not counted as late.
func TestTimeAnatomyLateRowClaimedByEnclosingIsNotLate(t *testing.T) {
	events := []TransactionEvent{
		taRequest(1, 11, 1, 0, 30, "A", 1),
		taTransaction(1, 11, 1, 0, 100, "Inner"),
		taRequest(1, 11, 1, 40, 30, "Late", 2),
		taTransaction(1, 11, 1, 0, 200, "Outer"),
	}
	anatomy := timeAnatomyForTest(t, events)
	if anatomy.Coverage.LateRequestRows != 0 {
		t.Fatalf("late_request_rows = %d, want 0 (Outer claims it)", anatomy.Coverage.LateRequestRows)
	}
	if outer := taEntry(t, anatomy, "Outer"); len(outer.Requests) != 2 {
		t.Fatalf("Outer rows = %+v", outer.Requests)
	}
}

// A reused legacy VUser ID (no lifetime) starts over at iteration 1: its live
// requests are not taken for retired ones of the previous lifetime, so global
// pressure evicts genuinely retired requests first.
func TestTimeAnatomyLegacyIDReuseIsNotRetired(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 4)
	feeder := newTAFeeder()
	for iteration := int64(1); iteration <= 3; iteration++ {
		start := float64(iteration-1) * 100
		tx := taTransaction(7, 0, iteration, start, 10, "Old")
		tx.EndsIteration = true
		feeder.feed(taRequest(7, 0, iteration, start, 5, "R", 1), tx)
	}
	feeder.feed(
		taRequest(9, 99, 1, 300, 5, "Z1", 1),
		taRequest(9, 99, 1, 310, 5, "Z2", 2),
		taTransaction(9, 99, 1, 300, 20, "Z"),
	)
	feeder.feed(
		taRequest(7, 0, 1, 1000, 5, "N1", 1),
		taRequest(7, 0, 1, 1010, 5, "N2", 2),
	)
	feeder.feed(
		taRequest(9, 99, 2, 1100, 5, "Z3", 1),
		taRequest(9, 99, 2, 1110, 5, "Z4", 2),
	)
	feeder.feed(taTransaction(7, 0, 1, 1000, 30, "New"))
	anatomy := feeder.finalize(t)
	entry := taEntry(t, anatomy, "New")
	if entry.JoinedInstances != 1 || entry.PartialInstances != 0 || entry.Decomposition.AvgRequestsPerInstance != 2 {
		t.Fatalf("New = joined %d partial %d decomposition %+v", entry.JoinedInstances, entry.PartialInstances, entry.Decomposition)
	}
}

// The join's CPU is bounded: once the work budget is spent, later
// transactions are partial (never joined with children missing) and later
// requests are dropped, with a reason.
func TestTimeAnatomyWorkBudget(t *testing.T) {
	for _, test := range []struct {
		name string
		feed func(f *taFeeder)
	}{
		{"wide windows", func(f *taFeeder) {
			for index := 0; index < 200; index++ {
				f.feed(taRequest(1, 11, 1, float64(index), 0.5, "R", 1))
			}
			for range 50 {
				f.feed(taTransaction(1, 11, 1, 0, 300, "Wide"))
			}
		}},
		{"reverse start order", func(f *taFeeder) {
			for index := 400; index > 0; index-- {
				f.feed(taRequest(1, 11, 1, float64(index), 0.5, "R", 1))
			}
			f.feed(taTransaction(1, 11, 1, 0, 500, "Wide"))
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			const budget = 2000
			setTimeAnatomyCap(t, &maxTimeAnatomyWork, budget)
			feeder := newTAFeeder()
			test.feed(feeder)
			if feeder.acc.work > budget {
				t.Fatalf("work %d over budget %d", feeder.acc.work, budget)
			}
			anatomy := feeder.finalize(t)
			if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonWorkBudget) || !anatomy.Truncation.Truncated {
				t.Fatalf("reasons %v", anatomy.Truncation.Reasons)
			}
			entry := taEntry(t, anatomy, "Wide")
			if entry.PartialInstances == 0 {
				t.Fatalf("Wide = joined %d partial %d, want partial instances once the budget is spent", entry.JoinedInstances, entry.PartialInstances)
			}
		})
	}
	// A normal archive stays far inside the default budget: a few units per
	// row (insert, scan and the joined children's fold), well under the
	// per-row allowance.
	feeder := newTAFeeder()
	taOpenWorkload(feeder, 2000, 5, false)
	anatomy := feeder.finalize(t)
	if slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonWorkBudget) || feeder.acc.work > 32*12_000 {
		t.Fatalf("work %d reasons %v", feeder.acc.work, anatomy.Truncation.Reasons)
	}
}

// An identity evicted by the identity cap is reported as such, not as the
// pending global cap.
func TestTimeAnatomyIdentityCapEvictionReason(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyIdentities, 2)
	feeder := newTAFeeder()
	feeder.feed(taRequest(1, 1, 1, 0, 10, "A", 1))
	feeder.feed(taRequest(2, 2, 1, 5, 10, "B", 1), taRequest(3, 3, 1, 6, 10, "C", 1))
	feeder.feed(taTransaction(1, 1, 1, 0, 50, "Outer"))
	anatomy := feeder.finalize(t)
	if outer := taEntry(t, anatomy, "Outer"); outer.PartialInstances != 1 || outer.JoinedInstances != 0 {
		t.Fatalf("outer = %+v", outer)
	}
	if !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonIdentityCap) || slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonPendingGlobalCap) {
		t.Fatalf("reasons %v, want identity_cap and not pending_global_cap", anatomy.Truncation.Reasons)
	}
}

// Oversized identifiers are not kept by instances, and the buffered strings
// count against a byte cap.
func TestTimeAnatomyBoundedStrings(t *testing.T) {
	feeder := newTAFeeder()
	feeder.feed(taRequest(1, 11, 1, 0, 10, "A", 1))
	huge := taTransaction(1, 11, 1, 0, 50, "T")
	huge.EventID = strings.Repeat("e", 1<<20)
	huge.TraceID = strings.Repeat("t", 1<<20)
	traceSent := true
	huge.TraceSent = &traceSent
	feeder.feed(huge)
	if feeder.acc.sampleBytes > 1<<16 {
		t.Fatalf("sample bytes %d after an oversized event_id", feeder.acc.sampleBytes)
	}
	name := feeder.acc.names["T"]
	if name.slowest.eventID != "" || name.slowest.traceID != "" {
		t.Fatal("slowest copy keeps the oversized identifiers")
	}

	setTimeAnatomyCap(t, &maxTimeAnatomyPendingBytes, 400)
	feeder = newTAFeeder()
	long := strings.Repeat("x", 90)
	for index := 0; index < 10; index++ {
		feeder.feed(taRequest(1, 11, 1, float64(index), 0.5, long, 1))
		if feeder.acc.pendingBytes > maxTimeAnatomyPendingBytes {
			t.Fatalf("pending bytes %d over cap", feeder.acc.pendingBytes)
		}
	}
	feeder.feed(taTransaction(1, 11, 1, 0, 20, "T"))
	anatomy := feeder.finalize(t)
	if entry := taEntry(t, anatomy, "T"); entry.PartialInstances != 1 || !slices.Contains(anatomy.Truncation.Reasons, timeAnatomyReasonPendingGlobalCap) {
		t.Fatalf("entry %+v reasons %v", entry, anatomy.Truncation.Reasons)
	}
	if feeder.acc.pendingBytes != 0 || feeder.acc.pendingTotal != 0 {
		t.Fatalf("after finalize pending %d bytes %d", feeder.acc.pendingTotal, feeder.acc.pendingBytes)
	}
}
