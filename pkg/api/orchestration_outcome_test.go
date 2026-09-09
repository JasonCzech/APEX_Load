package api

import (
	"testing"
	"time"
)

func TestIntegrationOutcomeSeparatesCompletionFromAcceptance(t *testing.T) {
	start := time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC)
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: "run-1", Status: "COMPLETE", CompletedAt: start.Add(290 * time.Second), Provenance: RunArchiveProvenance{StartedAt: &start, Duration: "5m"}}, Completeness: RunArchiveCompletenessScore{Status: "partial", ScorePct: 80}}
	outcome := buildIntegrationOutcome(report)
	if outcome.SchemaVersion != 1 || outcome.ExecutionID != "run-1" || outcome.Assessment.Status != "not_assessed" {
		t.Fatalf("unexpected outcome: %+v", outcome)
	}
	if *outcome.Execution.PlannedDurationSeconds != 300 || *outcome.Execution.ObservedDurationSeconds != 290 {
		t.Fatalf("duration contract: %+v", outcome.Execution)
	}
	if outcome.Evidence.Status != "partial" || *outcome.Evidence.CoveragePercent != 80 {
		t.Fatalf("evidence contract: %+v", outcome.Evidence)
	}
	report.Manifest.SLABreached = true
	if buildIntegrationOutcome(report).Assessment.Status != "failed" {
		t.Fatal("breach must fail")
	}
	if buildIntegrationOutcome(nil) != nil {
		t.Fatal("missing report must not fabricate outcome")
	}
}
