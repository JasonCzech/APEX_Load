package generatorrpc

import (
	"testing"
	"time"
)

// The published clock advances on the monotonic clock, so a wall-clock step
// does not move what the generator stamps until a heartbeat re-anchors it.
func TestPublishedClockIgnoresWallStepsUntilAHeartbeat(t *testing.T) {
	now := time.Now()
	clock := newPublishedClock(now)
	later := now.Add(10 * time.Second)
	if got := clock.at(later); !got.Equal(now.Round(0).Add(10 * time.Second)) {
		t.Fatalf("published %s", got)
	}
	// The wall clock stepped 5 s back since the anchor: the anchor's wall time
	// now reads 5 s ahead of the wall clock, and published times keep their
	// pre-step continuity until the next heartbeat.
	stepped := &publishedClock{wall: now.Round(0).Add(5 * time.Second), mono: now}
	if got := stepped.at(now.Add(time.Second)); !got.Equal(now.Round(0).Add(6 * time.Second)) {
		t.Fatalf("published across a step %s", got)
	}
	if !stepped.reanchor(now) || !stepped.at(now).Equal(now.Round(0)) {
		t.Fatal("a heartbeat did not re-anchor a clock 5 s off the wall clock")
	}
	if clock.reanchor(now.Add(time.Second)) {
		t.Fatal("a clock within tolerance was re-anchored")
	}
}

// Drift within the tolerance is never corrected, even by an idle heartbeat;
// beyond it the anchor moves.
func TestPublishedClockKeepsSubSecondDrift(t *testing.T) {
	now := time.Now()
	for _, tc := range []struct {
		drift time.Duration
		moves bool
	}{
		{600 * time.Millisecond, false},
		{-600 * time.Millisecond, false},
		{1500 * time.Millisecond, true},
		{-1500 * time.Millisecond, true},
	} {
		clock := &publishedClock{wall: now.Round(0).Add(tc.drift), mono: now}
		if moved := clock.reanchor(now); moved != tc.moves {
			t.Fatalf("drift %s re-anchored %v, want %v", tc.drift, moved, tc.moves)
		}
		want := now.Round(0).Add(tc.drift)
		if tc.moves {
			want = now.Round(0)
		}
		if got := clock.at(now); !got.Equal(want) {
			t.Fatalf("drift %s published %s, want %s", tc.drift, got, want)
		}
	}
}
