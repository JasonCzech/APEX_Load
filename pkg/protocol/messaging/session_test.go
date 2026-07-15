package messaging

import (
	"strings"
	"testing"
)

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
	rejected := 0
	for i := 0; i < maxPendingItems+extra; i++ {
		if err := s.Track(i); err != nil {
			rejected++
		}
	}
	if s.Pending() != maxPendingItems {
		t.Fatalf("pending = %d, want capped at %d", s.Pending(), maxPendingItems)
	}
	if s.DroppedPending() != extra {
		t.Fatalf("dropped = %d, want %d", s.DroppedPending(), extra)
	}
	if rejected != extra {
		t.Fatalf("rejected = %d, want %d", rejected, extra)
	}
	// Existing unsettled items remain intact; later deliveries are rejected.
	settled := s.Ack()
	if len(settled) != maxPendingItems {
		t.Fatalf("Ack returned %d, want %d", len(settled), maxPendingItems)
	}
	if settled[len(settled)-1] != maxPendingItems-1 {
		t.Fatalf("last retained item = %d, want %d", settled[len(settled)-1], maxPendingItems-1)
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

func TestSessionTrackSizedRejectsBeforeByteOverflow(t *testing.T) {
	s := New[string](AckTransacted)
	if err := s.TrackSized("first", maxPendingBytes-1); err != nil {
		t.Fatal(err)
	}
	if err := s.TrackSized("overflow", 2); err == nil {
		t.Fatal("TrackSized unexpectedly exceeded aggregate byte budget")
	}
	if s.Pending() != 1 || s.PendingBytes() != maxPendingBytes-1 {
		t.Fatalf("pending after rejection = %d/%d bytes, want 1/%d", s.Pending(), s.PendingBytes(), maxPendingBytes-1)
	}
	settled := s.Rollback()
	if len(settled) != 1 || settled[0] != "first" || s.PendingBytes() != 0 {
		t.Fatalf("rollback after rejection = %v, bytes=%d", settled, s.PendingBytes())
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
	st := s.streams[""]
	if st == nil || len(st.order) != maxObserveWindow || len(st.seen) != maxObserveWindow {
		t.Fatalf("window not bounded: %+v, want order=seen=%d", st, maxObserveWindow)
	}
	// A once-seen but now-evicted sequence is below max, so re-observing it reads as
	// out-of-order rather than duplicate — the documented bounded-window trade-off.
	if dup, ooo := s.Observe(1); dup || !ooo {
		t.Fatalf("re-Observe(evicted 1) = (dup=%v, ooo=%v), want (false, true)", dup, ooo)
	}
}

func TestSessionObserveKeyedIsolatesStreams(t *testing.T) {
	// Per-partition offsets each start near zero and advance independently;
	// interleaving them must NOT be read as duplicate/out-of-order, but a real
	// repeat or regression WITHIN one stream still is.
	s := New[int](AckAuto)
	if dup, ooo := s.ObserveKeyed("topic/0", 0); dup || ooo {
		t.Fatalf("p0 seq0 = (dup=%v, ooo=%v), want clean", dup, ooo)
	}
	if dup, ooo := s.ObserveKeyed("topic/0", 1); dup || ooo {
		t.Fatalf("p0 seq1 = (dup=%v, ooo=%v), want clean", dup, ooo)
	}
	// Partition 1 starting back at 0 is a fresh stream, not a duplicate of p0's 0.
	if dup, ooo := s.ObserveKeyed("topic/1", 0); dup || ooo {
		t.Fatalf("p1 seq0 = (dup=%v, ooo=%v), want clean (independent stream)", dup, ooo)
	}
	if dup, ooo := s.ObserveKeyed("topic/1", 1); dup || ooo {
		t.Fatalf("p1 seq1 = (dup=%v, ooo=%v), want clean", dup, ooo)
	}
	if s.Duplicates() != 0 || s.OutOfOrders() != 0 {
		t.Fatalf("cross-stream interleave flagged: dup=%d ooo=%d, want 0/0", s.Duplicates(), s.OutOfOrders())
	}
	// A genuine repeat within a stream is still a duplicate.
	if dup, _ := s.ObserveKeyed("topic/0", 0); !dup {
		t.Fatal("re-observing p0 seq0 should be a duplicate within its own stream")
	}
	// A regression within a fresh stream is still out-of-order.
	if _, ooo := s.ObserveKeyed("topic/2", 5); ooo {
		t.Fatal("first observation of a stream cannot be out-of-order")
	}
	if _, ooo := s.ObserveKeyed("topic/2", 3); !ooo {
		t.Fatal("seq3 after seq5 within the same stream should be out-of-order")
	}
}

func TestSessionObserveHasAggregateStreamAndEntryBounds(t *testing.T) {
	s := New[int](AckAuto)
	for stream := 0; stream < maxObserveStreams+50; stream++ {
		s.ObserveKeyed(string(rune(stream+1)), int64(stream))
	}
	if got := s.ObservedStreams(); got != maxObserveStreams {
		t.Fatalf("observed streams = %d, want %d", got, maxObserveStreams)
	}
	for round := 0; round < maxObserveEntries; round++ {
		stream := string(rune((round % maxObserveStreams) + 1_000))
		s.ObserveKeyed(stream, int64(round))
	}
	if got := s.ObservedStreams(); got > maxObserveStreams {
		t.Fatalf("observed streams = %d, exceeds %d", got, maxObserveStreams)
	}
	if got := s.ObservedEntries(); got > maxObserveEntries {
		t.Fatalf("observed entries = %d, exceeds %d", got, maxObserveEntries)
	}
}

func TestObserveKeyedDoesNotRetainOversizedStreamKeys(t *testing.T) {
	s := New[int](AckAuto)
	oversized := strings.Repeat("destination", maxObserveStreamKeyBytes)
	s.ObserveKeyed(oversized, 1)
	if _, retained := s.streams[oversized]; retained {
		t.Fatal("observer retained the oversized provider-controlled stream key")
	}
	if len(s.streams) != 1 {
		t.Fatalf("observed streams = %d, want 1", len(s.streams))
	}
	for key := range s.streams {
		if !strings.HasPrefix(key, "sha256:") || len(key) != len("sha256:")+64 {
			t.Fatalf("bounded stream key = %q", key)
		}
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
