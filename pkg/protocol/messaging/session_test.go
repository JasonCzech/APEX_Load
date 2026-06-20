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
	s.RecordRedeliveries(len(redeliver))
	if s.Redeliveries() != 1 {
		t.Fatalf("redeliveries = %d, want 1", s.Redeliveries())
	}
}
