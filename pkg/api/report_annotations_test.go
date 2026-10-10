package api

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func breachedReport() *RunArchiveReport {
	return &RunArchiveReport{
		Manifest: &RunArchiveManifest{
			TestID:      "t1",
			TestName:    "Checkout Flow",
			SLABreached: true,
			SLADetails:  []string{"P95 latency breached threshold"},
		},
		Overview: RunArchiveOverview{
			Transactions: 1000, PeakTPS: 42.5, ErrorPct: 2.1, AvgResponseMs: 180, PeakActiveVUsers: 50,
			MinResponseMs: 40, MaxResponseMs: 900,
		},
		ErrorPatterns: []RunArchiveErrorPattern{
			{Sample: "HTTP 500 on /checkout", Count: 12},
		},
	}
}

func TestDefaultReportAnnotationsSeedsBreachText(t *testing.T) {
	ann := defaultReportAnnotations(breachedReport(), "t1", "executive")

	if ann.Profile != "executive" {
		t.Fatalf("profile = %q, want executive", ann.Profile)
	}
	if !strings.Contains(ann.Title, "Executive report") {
		t.Fatalf("title = %q, want it to mention the profile label", ann.Title)
	}
	if !strings.Contains(ann.ExecutiveNarrative, "breached") {
		t.Fatalf("narrative should reflect the SLA breach: %q", ann.ExecutiveNarrative)
	}
	if sla := ann.SectionComments["sla"]; !strings.Contains(sla, "P95 latency breached threshold") {
		t.Fatalf("sla comment should include SLA detail, got %q", sla)
	}
	// The executive profile surfaces error context through its rendered "risks"
	// section, not an orphaned "errors" key.
	if risks := ann.SectionComments["risks"]; !strings.Contains(risks, "HTTP 500 on /checkout") {
		t.Fatalf("risks comment should mention top error pattern, got %q", risks)
	}
	if _, ok := ann.SectionComments["errors"]; ok {
		t.Fatalf("executive profile should not seed an unrendered errors comment")
	}
}

func TestDefaultReportAnnotationsPassingSLA(t *testing.T) {
	report := breachedReport()
	report.Manifest.SLABreached = false
	report.Manifest.SLADetails = nil
	report.ErrorPatterns = nil

	ann := defaultReportAnnotations(report, "t1", "engineer")
	if got := ann.SectionComments["sla"]; !strings.Contains(got, "No acceptance criteria") {
		t.Fatalf("sla comment = %q, want passing message", got)
	}
	if !strings.Contains(ann.SectionComments["errors"], "No significant error patterns") {
		t.Fatalf("errors comment = %q, want no-errors message", ann.SectionComments["errors"])
	}
	if !strings.Contains(ann.ExecutiveNarrative, "has not been assessed") {
		t.Fatalf("narrative = %q, want passing verdict", ann.ExecutiveNarrative)
	}
}

func newAnnotationsTestStore(t *testing.T) storage.Store {
	t.Helper()
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return store
}

func TestReportAnnotationsSaveLoadRoundTrip(t *testing.T) {
	store := newAnnotationsTestStore(t)

	in := &ReportAnnotations{
		TestID:             "t1",
		Profile:            "executive",
		Title:              "Custom Report Title",
		PreparedBy:         "Jane Analyst",
		ExecutiveNarrative: "Custom narrative.",
		SectionComments:    map[string]string{"overview": "custom overview"},
		UpdatedBy:          "jane",
	}
	if err := saveReportAnnotations(store, in); err != nil {
		t.Fatalf("saveReportAnnotations: %v", err)
	}

	got, err := loadReportAnnotations(store, "t1", "executive")
	if err != nil {
		t.Fatalf("loadReportAnnotations: %v", err)
	}
	if got == nil {
		t.Fatal("expected saved annotations, got nil")
	}
	if got.Title != in.Title || got.PreparedBy != in.PreparedBy || got.SectionComments["overview"] != "custom overview" {
		t.Fatalf("round-trip mismatch: %#v", got)
	}

	// Different profile must not collide.
	other, err := loadReportAnnotations(store, "t1", "engineer")
	if err != nil {
		t.Fatalf("loadReportAnnotations (engineer): %v", err)
	}
	if other != nil {
		t.Fatalf("expected nil for unsaved profile, got %#v", other)
	}
}

func TestResolveReportAnnotationsBackfillsDefaults(t *testing.T) {
	store := newAnnotationsTestStore(t)
	report := breachedReport()

	// Save a partial edit: only one section comment, no title/narrative.
	if err := saveReportAnnotations(store, &ReportAnnotations{
		TestID:          "t1",
		Profile:         "executive",
		SectionComments: map[string]string{"overview": "analyst overview note"},
	}); err != nil {
		t.Fatalf("saveReportAnnotations: %v", err)
	}

	resolved, err := resolveReportAnnotations(store, report, "t1", "executive")
	if err != nil {
		t.Fatalf("resolveReportAnnotations: %v", err)
	}
	if resolved.SectionComments["overview"] != "analyst overview note" {
		t.Fatalf("saved comment should be preserved, got %q", resolved.SectionComments["overview"])
	}
	if strings.TrimSpace(resolved.Title) == "" {
		t.Fatal("expected title to be backfilled from defaults")
	}
	if strings.TrimSpace(resolved.ExecutiveNarrative) == "" {
		t.Fatal("expected narrative to be backfilled from defaults")
	}
	if strings.TrimSpace(resolved.SectionComments["sla"]) == "" {
		t.Fatal("expected untouched sla section to be backfilled from defaults")
	}
}

func TestResolveReportAnnotationsUsesDefaultsWhenUnsaved(t *testing.T) {
	store := newAnnotationsTestStore(t)
	resolved, err := resolveReportAnnotations(store, breachedReport(), "t1", "executive")
	if err != nil {
		t.Fatalf("resolveReportAnnotations: %v", err)
	}
	if resolved == nil || strings.TrimSpace(resolved.ExecutiveNarrative) == "" {
		t.Fatalf("expected default annotations when nothing saved, got %#v", resolved)
	}
}

func TestDefaultNarrativeUsesComputedFailures(t *testing.T) {
	for _, source := range []string{"transaction", "dashboard"} {
		t.Run(source, func(t *testing.T) {
			report := &RunArchiveReport{Manifest: &RunArchiveManifest{}, MetricPopulations: map[string]ArchiveMetricPopulation{}}
			if source == "transaction" {
				report.Manifest.Provenance.GoalConfig = &GoalConfig{MaxErrorPct: ptr(0.0)}
				report.ByTransaction = map[string]RunArchiveActionStats{"checkout": {Transactions: 1, Errors: 1, ErrorPct: 100}}
			} else {
				report.DashboardPanels = &RunArchiveDashboardPanels{Dashboards: []RunArchiveDashboard{{Panels: []RunArchiveDashboardPanel{{ThresholdConfigured: true, ThresholdEvaluated: true, ThresholdBreached: true}}}}}
			}
			if got := deriveReportVerdict(report); got.Status != reportVerdictFail {
				t.Fatal(got)
			}
			for _, profile := range []string{"executive", "engineer"} {
				ann := defaultReportAnnotations(report, "review", profile)
				if !strings.Contains(ann.ExecutiveNarrative, "breached one or more SLA targets") || strings.Contains(ann.ExecutiveNarrative, "not been assessed") {
					t.Fatalf("%s: %s", profile, ann.ExecutiveNarrative)
				}
			}
		})
	}
}

func TestDefaultRiskCommentIncludesEvidenceLimitations(t *testing.T) {
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{}}
	report.Completeness.Findings = []RunArchiveCompletenessFinding{{Severity: "critical", Message: "Telemetry delivery gaps"}}
	report.Overview.Transactions = 9
	report.Overview.PeakActiveVUsers = 1
	report.Manifest.Provenance.TotalVUsers = 22
	got := defaultRiskComment(report)
	for _, want := range []string{"Telemetry delivery gaps", "9 archive records", "1 of 22", "termination evidence", "Absence of recorded errors"} {
		if !strings.Contains(got, want) {
			t.Errorf("risk comment %q missing %q", got, want)
		}
	}
	if got := defaultRiskComment(&RunArchiveReport{}); !strings.Contains(got, "unverified") {
		t.Fatalf("missing manifest: %s", got)
	}
	if got := defaultRiskComment(nil); !strings.Contains(got, "unavailable") {
		t.Fatalf("nil report: %s", got)
	}
}
