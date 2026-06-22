package messaging

import "testing"

func TestParseAckMode(t *testing.T) {
	cases := map[string]AckMode{
		"":           AckAuto,
		"auto":       AckAuto,
		"AUTO":       AckAuto,
		"client":     AckClient,
		"dups_ok":    AckDupsOK,
		"dups-ok":    AckDupsOK,
		"transacted": AckTransacted,
		"nonsense":   AckAuto,
	}
	for in, want := range cases {
		if got := ParseAckMode(in); got != want {
			t.Errorf("ParseAckMode(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSessionTrackBoundsPending(t *testing.T) {
	s := New[int](AckClient)
	const extra = 500
	for i := 0; i < maxPendingItems+extra; i++ {
		s.Track(i)
	}
	if s.Pending() != maxPendingItems {
		t.Fatalf("pending = %d, want capped at %d", s.Pending(), maxPendingItems)
	}
	if s.DroppedPending() != extra {
		t.Fatalf("dropped = %d, want %d", s.DroppedPending(), extra)
	}
	// The oldest items are evicted and the most recent ones retained.
	settled := s.Ack()
	if len(settled) != maxPendingItems {
		t.Fatalf("Ack returned %d, want %d", len(settled), maxPendingItems)
	}
	if settled[len(settled)-1] != maxPendingItems+extra-1 {
		t.Fatalf("newest retained item = %d, want %d", settled[len(settled)-1], maxPendingItems+extra-1)
	}

	// auto mode never retains and therefore never evicts.
	a := New[int](AckAuto)
	for i := 0; i < 100; i++ {
		a.Track(i)
	}
	if a.Pending() != 0 || a.DroppedPending() != 0 {
		t.Fatalf("auto: pending=%d dropped=%d, want 0/0", a.Pending(), a.DroppedPending())
	}
}

// TestSessionObserve covers duplicate + out-of-order detection. (The
// redelivery-is-not-a-duplicate semantic is enforced at the protocol consume
// layer, which skips Observe for redelivered messages, and is tested there.)
func TestSessionObserve(t *testing.T) {
	s := New[int](AckAuto)
	check := func(seq int64, wantDup, wantOoo bool) {
		t.Helper()
		dup, ooo := s.Observe(seq)
		if dup != wantDup || ooo != wantOoo {
			t.Fatalf("Observe(%d) = (dup=%v, ooo=%v), want (%v, %v)", seq, dup, ooo, wantDup, wantOoo)
		}
	}
	check(10, false, false) // first observation
	check(20, false, false) // in order
	check(30, false, false) // in order
	check(25, false, true)  // unseen but < max(30) -> out of order
	check(20, true, false)  // already seen -> duplicate (takes priority over ooo)
	check(40, false, false) // new max -> clean
	check(-1, false, false) // negative sequence -> no-op
	if s.Duplicates() != 1 {
		t.Fatalf("Duplicates() = %d, want 1", s.Duplicates())
	}
	if s.OutOfOrders() != 1 {
		t.Fatalf("OutOfOrders() = %d, want 1", s.OutOfOrders())
	}
}

func TestSessionObserveWindowEviction(t *testing.T) {
	s := New[int](AckAuto)
	total := int64(maxObserveWindow + 100)
	for i := int64(1); i <= total; i++ {
		if dup, _ := s.Observe(i); dup {
			t.Fatalf("Observe(%d) unexpectedly duplicate", i)
		}
	}
	if len(s.seqOrder) != maxObserveWindow || len(s.seqSeen) != maxObserveWindow {
		t.Fatalf("window not bounded: order=%d seen=%d, want %d", len(s.seqOrder), len(s.seqSeen), maxObserveWindow)
	}
	// A once-seen but now-evicted sequence is below max, so re-observing it reads as
	// out-of-order rather than duplicate — the documented bounded-window trade-off.
	if dup, ooo := s.Observe(1); dup || !ooo {
		t.Fatalf("re-Observe(evicted 1) = (dup=%v, ooo=%v), want (false, true)", dup, ooo)
	}
}

func TestSessionAutoModeDoesNotRetain(t *testing.T) {
	s := New[string](AckAuto)
	if s.NeedsSettlement() {
		t.Fatal("auto mode must not need settlement")
	}
	s.Track("m1")
	if s.Pending() != 0 {
		t.Fatalf("auto mode must not retain pending items, got %d", s.Pending())
	}
}

func TestSessionClientAckSettlesPending(t *testing.T) {
	s := New[string](AckClient)
	if !s.NeedsSettlement() {
		t.Fatal("client mode must need settlement")
	}
	s.Track("m1")
	s.Track("m2")
	if s.Pending() != 2 {
		t.Fatalf("pending = %d, want 2", s.Pending())
	}
	settled := s.Ack()
	if len(settled) != 2 {
		t.Fatalf("Ack settled %d, want 2", len(settled))
	}
	if s.Pending() != 0 {
		t.Fatalf("pending after ack = %d, want 0", s.Pending())
	}
}

func TestSessionTransactedCommitAndRollback(t *testing.T) {
	s := New[string](AckTransacted)
	s.Track("a")
	s.Track("b")
	if got := s.Commit(); len(got) != 2 {
		t.Fatalf("Commit returned %d, want 2", len(got))
	}
	if s.Commits() != 1 || s.Pending() != 0 {
		t.Fatalf("after commit: commits=%d pending=%d, want 1/0", s.Commits(), s.Pending())
	}

	s.Track("c")
	redeliver := s.Rollback()
	if len(redeliver) != 1 || redeliver[0] != "c" {
		t.Fatalf("Rollback returned %v, want [c]", redeliver)
	}
	if s.Aborts() != 1 || s.Pending() != 0 {
		t.Fatalf("after rollback: aborts=%d pending=%d, want 1/0", s.Aborts(), s.Pending())
	}
}
