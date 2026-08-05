package api

import (
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRecordReleaseSLABreachAppliesWaiversPerRunScopeAndTime(t *testing.T) {
	evaluatedAt := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	oldCompletion := evaluatedAt.Add(-2 * time.Hour)
	newCompletion := evaluatedAt.Add(-30 * time.Minute)
	waiverCreated := evaluatedAt.Add(-time.Hour)
	response := ReleaseSLAGovernanceResponse{GeneratedAt: evaluatedAt}
	waivers := []*storage.ReleaseSLAWaiver{{
		ID: "waiver-old", Scope: "test:old-run", Status: "active", CreatedAt: waiverCreated,
	}}

	recordReleaseSLABreach(&response, ReleaseSLAGovernanceRun{TestID: "old-run", CompletedAt: &oldCompletion, SLABreached: true}, waivers)
	recordReleaseSLABreach(&response, ReleaseSLAGovernanceRun{TestID: "new-run", CompletedAt: &newCompletion, SLABreached: true}, waivers)
	if response.Summary.FailedRuns != 2 || response.Summary.WaivedBreaches != 1 || response.Summary.UnwaivedBreaches != 1 {
		t.Fatalf("summary = %#v, want one scoped historical waiver and one uncovered breach", response.Summary)
	}
}

func TestReleaseSLAWaiverCoverageRejectsExpiredFutureAndWrongScope(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	completed := now.Add(-2 * time.Hour)
	run := ReleaseSLAGovernanceRun{TestID: "run-1", CompletedAt: &completed}
	expired := now.Add(-time.Minute)
	tests := []struct {
		name   string
		waiver *storage.ReleaseSLAWaiver
		want   bool
	}{
		{name: "release scope historical", waiver: &storage.ReleaseSLAWaiver{Scope: "release", Status: "active", CreatedAt: now.Add(-time.Hour)}, want: true},
		{name: "exact test scope", waiver: &storage.ReleaseSLAWaiver{Scope: "run-1", Status: "active", CreatedAt: now.Add(-time.Hour)}, want: true},
		{name: "wrong test scope", waiver: &storage.ReleaseSLAWaiver{Scope: "run-2", Status: "active", CreatedAt: now.Add(-time.Hour)}},
		{name: "created before breach", waiver: &storage.ReleaseSLAWaiver{Scope: "release", Status: "active", CreatedAt: completed.Add(-time.Minute)}},
		{name: "expired", waiver: &storage.ReleaseSLAWaiver{Scope: "release", Status: "active", CreatedAt: now.Add(-time.Hour), ExpiresAt: &expired}},
		{name: "inactive", waiver: &storage.ReleaseSLAWaiver{Scope: "release", Status: "revoked", CreatedAt: now.Add(-time.Hour)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := releaseSLAWaiverCoversRun(tc.waiver, run, now); got != tc.want {
				t.Fatalf("coverage = %t, want %t", got, tc.want)
			}
		})
	}
}
