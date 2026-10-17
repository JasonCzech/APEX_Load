package contentmutation

import (
	"errors"
	"testing"
)

func TestResolveWriteClassifiesExactTransitionEvidence(t *testing.T) {
	type occurrence struct {
		parent, token string
		revision      int
	}
	intended := &occurrence{"parent", "attempt", 4}
	lostReply := errors.New("reply lost")
	readFailure := errors.New("read failed")
	for _, tc := range []struct {
		name                   string
		changed                bool
		writeErr               error
		current                *occurrence
		readErr                error
		lostBefore, lostDuring bool
		want                   WriteState
		reads                  int
	}{
		{name: "successful write needs no read", changed: true, want: WriteApplied},
		{name: "lost response exact transition", writeErr: lostReply, current: intended, want: WriteApplied, reads: 1},
		{name: "false reply already applied", current: intended, want: WriteApplied, reads: 1},
		{name: "parent edit", current: &occurrence{"different", "attempt", 4}, want: WriteConflict, reads: 1},
		{name: "other transition", current: &occurrence{"parent", "other", 4}, want: WriteConflict, reads: 1},
		{name: "different revision", current: &occurrence{"parent", "attempt", 5}, want: WriteConflict, reads: 1},
		{name: "deleted", want: WriteConflict, reads: 1},
		{name: "write and read failed", writeErr: lostReply, readErr: readFailure, want: WriteUnknown, reads: 1},
		{name: "read failed after false reply", readErr: readFailure, want: WriteUnknown, reads: 1},
		{name: "failed write unmatched evidence", writeErr: lostReply, want: WriteUnknown, reads: 1},
		{name: "lease lost before read", lostBefore: true, want: WriteOwnershipLost},
		{name: "lease lost during exact readback", current: intended, lostDuring: true, want: WriteOwnershipLost, reads: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owned, reads := !tc.lostBefore, 0
			got := ResolveWrite(tc.changed, tc.writeErr, func() bool { return owned }, func() (*occurrence, error) {
				reads++
				if tc.lostDuring {
					owned = false
				}
				return tc.current, tc.readErr
			}, func(current *occurrence) bool { return current != nil && *current == *intended })
			if got.State != tc.want || reads != tc.reads {
				t.Fatalf("resolution=%#v reads=%d; want %v/%d", got, reads, tc.want, tc.reads)
			}
			if tc.reads > 0 && !errors.Is(got.ReadError, tc.readErr) {
				t.Fatalf("read error lost: %v", got.ReadError)
			}
		})
	}
}
