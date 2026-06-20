package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestGroupActiveVUsersPrefersLiveCount(t *testing.T) {
	if got := groupActiveVUsers(&engine.GroupMetrics{ActiveVUsers: 7}, 100); got != 7 {
		t.Fatalf("live count = %d, want 7", got)
	}
	// Falls back to the configured size when the engine reports no live count.
	if got := groupActiveVUsers(&engine.GroupMetrics{ActiveVUsers: 0}, 100); got != 100 {
		t.Fatalf("fallback = %d, want configured 100", got)
	}
	if got := groupActiveVUsers(nil, 42); got != 42 {
		t.Fatalf("nil fallback = %d, want 42", got)
	}
}

// buildLiveMetricsFromSnapshot must surface the engine's live per-group active
// count (the ramping value) rather than the static configured size — this is what
// keeps the group-filtered load-impact x-axis from collapsing.
func TestBuildLiveMetricsFromSnapshotUsesLiveGroupVUsers(t *testing.T) {
	snap := engine.MetricsSnapshot{
		ByGroup: map[string]*engine.GroupMetrics{
			"buyers":   {Name: "buyers", ActiveVUsers: 6, Transactions: 3},
			"browsers": {Name: "browsers", ActiveVUsers: 0, Transactions: 1},
		},
	}
	// Configured sizes (static) differ from the live ramping counts.
	configured := map[string]int{"buyers": 50, "browsers": 30}

	live := buildLiveMetricsFromSnapshot(snap, configured)

	if live.GroupMetrics["buyers"].VUsers != 6 {
		t.Fatalf("buyers VUsers = %d, want live 6", live.GroupMetrics["buyers"].VUsers)
	}
	// No live count for browsers → static fallback.
	if live.GroupMetrics["browsers"].VUsers != 30 {
		t.Fatalf("browsers VUsers = %d, want configured 30 (fallback)", live.GroupMetrics["browsers"].VUsers)
	}
}
