package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func rootCauseSignal(t *testing.T, summary RunRootCauseSummary, name string) RootCauseSignal {
	t.Helper()
	for _, signal := range summary.Signals {
		if signal.Name == name {
			return signal
		}
	}
	t.Fatalf("signals = %#v, want %q", summary.Signals, name)
	return RootCauseSignal{}
}

func completeRootCauseReport(manifest *RunArchiveManifest) *RunArchiveReport {
	return &RunArchiveReport{
		Manifest:     manifest,
		Overview:     RunArchiveOverview{Transactions: 1000, IntervalSummaries: 60},
		Completeness: RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"},
	}
}

func TestRootCauseSLAState(t *testing.T) {
	recorded := RunArchiveProvenance{Groups: []TestGroup{{Name: "checkout"}}}
	withGoals := RunArchiveProvenance{Groups: recorded.Groups, GoalConfig: &GoalConfig{P95LatencyMs: ptr(250.0)}}
	tests := []struct {
		name         string
		manifest     *RunArchiveManifest
		wantSignal   string
		wantFinding  bool
		wantStatus   string
		wantNarrated string
	}{
		{
			// SLAEvaluated is vacuously true when nothing was configured.
			name:         "no criteria configured is not passed",
			manifest:     &RunArchiveManifest{Provenance: recorded, SLAEvaluated: true},
			wantSignal:   archiveSLANotConfigured,
			wantStatus:   "healthy",
			wantNarrated: "No SLA criteria were configured",
		},
		{
			name:         "legacy manifest without provenance is unknown",
			manifest:     &RunArchiveManifest{SLAEvaluated: true},
			wantSignal:   archiveSLAUnknown,
			wantStatus:   "healthy",
			wantNarrated: "does not record SLA criteria",
		},
		{
			name:         "configured and evaluated passes",
			manifest:     &RunArchiveManifest{Provenance: withGoals, SLAEvaluated: true},
			wantSignal:   archiveSLAPassed,
			wantStatus:   "healthy",
			wantNarrated: "archived SLA checks passed",
		},
		{
			name:        "configured but not evaluable is reviewed",
			manifest:    &RunArchiveManifest{Provenance: withGoals},
			wantSignal:  archiveSLANotEvaluated,
			wantFinding: true,
			wantStatus:  "degraded",
		},
		{
			name:        "per-group response-time threshold counts as configured",
			manifest:    &RunArchiveManifest{Provenance: RunArchiveProvenance{Groups: []TestGroup{{Name: "checkout", TransactionResponseTimeSLAMs: ptr(400.0)}}}, SLAEvaluated: true},
			wantSignal:  archiveSLAPassed,
			wantStatus:  "healthy",
		},
		{
			name:        "breach wins",
			manifest:    &RunArchiveManifest{Provenance: withGoals, SLAEvaluated: true, SLABreached: true, SLADetails: []string{"p95 400 ms > 250 ms"}},
			wantSignal:  archiveSLABreached,
			wantFinding: true,
			wantStatus:  "breached",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			summary := buildRunRootCauseSummary("run", completeRootCauseReport(tt.manifest))
			if got := rootCauseSignal(t, summary, "SLA").Status; got != tt.wantSignal {
				t.Fatalf("SLA signal = %q, want %q", got, tt.wantSignal)
			}
			hasFinding := false
			for _, finding := range summary.TopFindings {
				if finding.Category == "sla" {
					hasFinding = true
				}
			}
			if hasFinding != tt.wantFinding {
				t.Fatalf("SLA finding present = %v, want %v (%#v)", hasFinding, tt.wantFinding, summary.TopFindings)
			}
			if summary.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", summary.Status, tt.wantStatus)
			}
			if tt.wantNarrated != "" && !strings.Contains(summary.Summary, tt.wantNarrated) {
				t.Fatalf("summary = %q, want it to mention %q", summary.Summary, tt.wantNarrated)
			}
			if tt.wantSignal != archiveSLAPassed && strings.Contains(summary.Summary, "SLA checks passed") {
				t.Fatalf("summary = %q claims an SLA pass for state %q", summary.Summary, tt.wantSignal)
			}
		})
	}
}

func TestRootCauseStatusUsesFindingsBeforeTruncation(t *testing.T) {
	report := completeRootCauseReport(&RunArchiveManifest{Provenance: RunArchiveProvenance{Groups: []TestGroup{{Name: "g"}}}})
	report.ByAction = map[string]RunArchiveActionStats{}
	for i := 0; i < 7; i++ {
		report.ByAction[fmt.Sprintf("action-%d", i)] = RunArchiveActionStats{DiagnosticFindings: []protocol.DiagnosticFinding{{Rule: "slow_stream", Message: "advisory"}}}
	}
	// 5% errors scores exactly 50, tying the seven advisories added before it.
	report.Overview.Errors = 50
	report.Overview.ErrorPct = 5

	summary := buildRunRootCauseSummary("run", report)
	if summary.Status != "breached" {
		t.Fatalf("status = %q, want breached from the critical error finding", summary.Status)
	}
	if len(summary.TopFindings) != rootCauseMaxTopFindings {
		t.Fatalf("top findings = %d, want %d", len(summary.TopFindings), rootCauseMaxTopFindings)
	}
	if summary.TopFindings[0].Category != "errors" || summary.TopFindings[0].Severity != "critical" {
		t.Fatalf("lead finding = %#v, want the critical error finding ahead of tied advisories", summary.TopFindings[0])
	}
	if !strings.Contains(summary.Summary, "2 further finding(s)") {
		t.Fatalf("summary = %q, want the omitted finding count", summary.Summary)
	}
	actions := strings.Join(summary.NextActions, " ")
	if !strings.Contains(actions, "group failures by action") {
		t.Fatalf("next actions = %q, want the errors action", actions)
	}
}

func TestRootCauseConfidenceReflectsLeadEvidence(t *testing.T) {
	manifest := &RunArchiveManifest{Provenance: RunArchiveProvenance{Groups: []TestGroup{{Name: "g"}}}}
	tests := []struct {
		name   string
		mutate func(*RunArchiveReport)
		want   string
	}{
		{name: "measured lead with two references on a complete archive", mutate: func(r *RunArchiveReport) { r.Overview.Errors, r.Overview.ErrorPct = 10, 1 }, want: "high"},
		{name: "advisory lead is capped at medium", mutate: func(r *RunArchiveReport) {
			r.StatisticalFindings = []StatisticalFinding{{AnomalyEvidence: analysis.AnomalyEvidence{MetricName: "tps", BaselineBuckets: 30, ZScore: 4}, Advisory: true}}
		}, want: "medium"},
		{name: "single-reference measured lead is capped at medium", mutate: func(r *RunArchiveReport) { r.ResourcePeaks.CPUProcessPct = 85 }, want: "medium"},
		{name: "no findings on a complete archive", mutate: func(*RunArchiveReport) {}, want: "high"},
		{name: "no findings on an incomplete archive is not reassuring", mutate: func(r *RunArchiveReport) {
			r.Completeness = RunArchiveCompletenessScore{ScorePct: 80}
		}, want: "low"},
		{name: "completeness still caps a strong lead", mutate: func(r *RunArchiveReport) {
			r.Overview.Errors, r.Overview.ErrorPct = 10, 1
			r.Completeness = RunArchiveCompletenessScore{ScorePct: 50}
		}, want: "low"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := completeRootCauseReport(manifest)
			tt.mutate(report)
			if got := buildRunRootCauseSummary("run", report).Confidence; got != tt.want {
				t.Fatalf("confidence = %q, want %q", got, tt.want)
			}
		})
	}
}
