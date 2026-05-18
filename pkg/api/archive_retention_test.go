package api

import (
	"testing"
	"time"

	"github.com/jasonczech/stormrunner/pkg/storage"
)

func TestPlanArchiveRetentionPruneAppliesSpecificPolicies(t *testing.T) {
	now := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	oldCompletedAt := now.Add(-10 * 24 * time.Hour)
	recentCompletedAt := now.Add(-2 * 24 * time.Hour)
	settings := ArchiveRetentionSettings{
		Enabled:              true,
		DefaultRetentionDays: 30,
		Policies: []ArchiveRetentionPolicy{{
			Name:          "failed-checkout",
			ProjectID:     "checkout",
			RunStatus:     "FAILED",
			ArtifactType:  "bundle",
			RetentionDays: 7,
		}},
	}
	tests := []*ManagedTest{
		{
			ID:        "old-failed",
			Status:    "FAILED",
			ProjectID: "checkout",
			Archive:   &storage.RunArchiveRecord{TestID: "old-failed", ArchivePath: "/archives/old", CompletedAt: oldCompletedAt},
		},
		{
			ID:        "recent-failed",
			Status:    "FAILED",
			ProjectID: "checkout",
			Archive:   &storage.RunArchiveRecord{TestID: "recent-failed", ArchivePath: "/archives/recent", CompletedAt: recentCompletedAt},
		},
		{
			ID:        "old-complete-default",
			Status:    "COMPLETE",
			ProjectID: "checkout",
			Archive:   &storage.RunArchiveRecord{TestID: "old-complete-default", ArchivePath: "/archives/default", CompletedAt: oldCompletedAt},
		},
	}

	candidates := planArchiveRetentionPrune(now, settings, tests)
	if len(candidates) != 1 {
		t.Fatalf("candidates = %#v, want one expired failed checkout archive", candidates)
	}
	candidate := candidates[0]
	if candidate.TestID != "old-failed" || candidate.PolicyName != "failed-checkout" || candidate.RetentionDays != 7 {
		t.Fatalf("candidate = %#v", candidate)
	}
	if !candidate.ExpiredAt.Equal(oldCompletedAt.Add(7 * 24 * time.Hour)) {
		t.Fatalf("expired at = %s, want completion + retention", candidate.ExpiredAt)
	}
}

func TestPlanArchiveRetentionPruneHonorsDisabledSetting(t *testing.T) {
	now := time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC)
	candidates := planArchiveRetentionPrune(now, ArchiveRetentionSettings{Enabled: false, DefaultRetentionDays: 1}, []*ManagedTest{{
		ID:      "old",
		Status:  "COMPLETE",
		Archive: &storage.RunArchiveRecord{TestID: "old", ArchivePath: "/archives/old", CompletedAt: now.Add(-365 * 24 * time.Hour)},
	}})
	if len(candidates) != 0 {
		t.Fatalf("candidates = %#v, want none while retention disabled", candidates)
	}
}
