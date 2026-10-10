package api

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

// sampleReleaseTrendSummary builds a regressed two-run trend (early -> late) and
// returns the compact summary for the later run, which has a baseline.
func sampleReleaseTrendSummary(t *testing.T) *RunArchiveReleaseTrendSummary {
	t.Helper()
	base := time.Date(2026, time.May, 20, 12, 0, 0, 0, time.UTC)
	trend := buildReleaseTrend("rel-1", []releaseTrendRun{
		trendRunFixture("run-early", base, 1.0, 100, 100, false),
		trendRunFixture("run-late", base.Add(time.Hour), 3.0, 130, 80, true),
	})
	summary := buildRunArchiveReleaseTrendSummary(trend, "run-late")
	if summary == nil {
		t.Fatal("expected a non-nil summary for run-late")
	}
	return summary
}

func sampleReleaseTrendReport() *RunArchiveReport {
	return &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		Manifest:      &RunArchiveManifest{TestID: "run-late", TestName: "Late Run", Status: "COMPLETE"},
		Overview:      RunArchiveOverview{Transactions: 1000, ErrorPct: 3, AvgResponseMs: 130, PeakTPS: 50, P95Ms: 200},
		Completeness:  RunArchiveCompletenessScore{ScorePct: 80, Status: "needs_review"},
		ByAction:      map[string]RunArchiveActionStats{"checkout": {Transactions: 1000}},
	}
}

func TestBuildRunArchiveReleaseTrendSummary(t *testing.T) {
	base := time.Date(2026, time.May, 20, 12, 0, 0, 0, time.UTC)
	trend := buildReleaseTrend("rel-1", []releaseTrendRun{
		trendRunFixture("run-early", base, 1.0, 100, 100, false),
		trendRunFixture("run-late", base.Add(time.Hour), 3.0, 130, 80, true),
	})

	t.Run("baseline run carries deltas and regressions", func(t *testing.T) {
		summary := buildRunArchiveReleaseTrendSummary(trend, "run-late")
		if summary == nil {
			t.Fatal("expected non-nil summary")
		}
		if !summary.HasBaseline || summary.Baseline == nil {
			t.Fatalf("expected a baseline, got %#v", summary)
		}
		if summary.Baseline.RunID != "run-early" {
			t.Fatalf("baseline run = %q, want run-early", summary.Baseline.RunID)
		}
		if summary.RunCount != 2 {
			t.Fatalf("run count = %d, want 2", summary.RunCount)
		}
		if summary.Current.RunID != "run-late" {
			t.Fatalf("current run = %q, want run-late", summary.Current.RunID)
		}
		// Deltas/regressions are surfaced at the top level and blanked on the
		// embedded points so each point stays a clean snapshot.
		if summary.Deltas == nil || summary.Deltas.ErrorPctDelta != 2.0 {
			t.Fatalf("deltas = %#v, want error pct delta 2.0", summary.Deltas)
		}
		if summary.Current.Deltas != nil || summary.Baseline.Deltas != nil {
			t.Fatal("embedded points should not carry deltas")
		}
		if !summary.Regressed || len(summary.Regressions) != 4 {
			t.Fatalf("regressions = %#v, want 4", summary.Regressions)
		}
		if summary.Trajectory.AvgResponseTrendPct != 30 {
			t.Fatalf("trajectory avg response pct = %v, want 30", summary.Trajectory.AvgResponseTrendPct)
		}
		if summary.Note != "" {
			t.Fatalf("baseline summary should not carry a first-run note, got %q", summary.Note)
		}
	})

	t.Run("first run has no baseline but renders gracefully", func(t *testing.T) {
		summary := buildRunArchiveReleaseTrendSummary(trend, "run-early")
		if summary == nil {
			t.Fatal("expected non-nil summary for the first run")
		}
		if summary.HasBaseline || summary.Baseline != nil {
			t.Fatalf("first run should have no baseline, got %#v", summary)
		}
		if summary.Deltas != nil || summary.Regressed {
			t.Fatalf("first run should have no deltas/regressions, got %#v", summary)
		}
		if summary.Note == "" {
			t.Fatal("first run should carry an explanatory note")
		}
	})

	t.Run("unknown run returns nil", func(t *testing.T) {
		if got := buildRunArchiveReleaseTrendSummary(trend, "run-missing"); got != nil {
			t.Fatalf("unknown run = %#v, want nil", got)
		}
	})

	t.Run("empty run id returns nil", func(t *testing.T) {
		if got := buildRunArchiveReleaseTrendSummary(trend, "  "); got != nil {
			t.Fatalf("empty run id = %#v, want nil", got)
		}
	})
}

func TestRenderExecutiveHTMLReportRendersReleaseTrend(t *testing.T) {
	report := sampleReleaseTrendReport()
	report.ReleaseTrend = sampleReleaseTrendSummary(t)

	html, err := renderRunArchiveHTMLReportWithProfile(report, "executive")
	if err != nil {
		t.Fatalf("render executive html: %v", err)
	}
	body := string(html)
	// html/template renders a leading "+" as the &#43; entity (a browser shows "+"),
	// so assert on the unescaped tail of each delta rather than the literal sign.
	for _, want := range []string{
		"Release trend",
		"run-early",            // baseline run name (fixture names match the id)
		"2.0 pts",              // error rate delta (rendered &#43;2.0 pts)
		"error rate regressed", // regression flag
		"Prior run",            // table header
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("executive HTML missing %q:\n%s", want, body)
		}
	}
	if !strings.Contains(body, "&#43;2.0 pts") {
		t.Fatalf("executive HTML should render the signed error-rate delta, got:\n%s", body)
	}
}

func TestRenderExecutiveHTMLReportReleaseTrendNoBaseline(t *testing.T) {
	report := sampleReleaseTrendReport()
	base := time.Date(2026, time.May, 20, 12, 0, 0, 0, time.UTC)
	trend := buildReleaseTrend("rel-1", []releaseTrendRun{
		trendRunFixture("run-late", base, 3.0, 130, 80, true),
	})
	report.ReleaseTrend = buildRunArchiveReleaseTrendSummary(trend, "run-late")

	html, err := renderRunArchiveHTMLReportWithProfile(report, "executive")
	if err != nil {
		t.Fatalf("render executive html: %v", err)
	}
	body := string(html)
	if !strings.Contains(body, "Release trend") {
		t.Fatalf("expected Release trend heading, got:\n%s", body)
	}
	if !strings.Contains(body, "First archived run for this release") {
		t.Fatalf("expected no-baseline note, got:\n%s", body)
	}
	// With no baseline there is no comparison table.
	if strings.Contains(body, "Prior run") {
		t.Fatalf("no-baseline trend should omit the comparison table, got:\n%s", body)
	}
}

func TestRenderExecutiveHTMLReportReleaseTrendUnavailable(t *testing.T) {
	report := sampleReleaseTrendReport() // ReleaseTrend stays nil (no release association)

	html, err := renderRunArchiveHTMLReportWithProfile(report, "executive")
	if err != nil {
		t.Fatalf("render executive html: %v", err)
	}
	body := string(html)
	if !strings.Contains(body, "Release trend") {
		t.Fatalf("executive report should still render the Release trend heading, got:\n%s", body)
	}
	if !strings.Contains(body, "not associated with a release") {
		t.Fatalf("expected the unavailable note, got:\n%s", body)
	}
}

func TestEngineerHTMLReportOmitsReleaseTrend(t *testing.T) {
	report := sampleReleaseTrendReport()
	report.ReleaseTrend = sampleReleaseTrendSummary(t)

	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatalf("render engineer html: %v", err)
	}
	if strings.Contains(string(html), "Release trend") {
		t.Fatalf("engineer profile should omit the executive-only Release Trend section")
	}
}

func TestWriteExecutiveReleaseTrendPDFAndCSV(t *testing.T) {
	report := sampleReleaseTrendReport()
	report.ReleaseTrend = sampleReleaseTrendSummary(t)

	var pdf bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &pdf, "executive"); err != nil {
		t.Fatalf("write executive pdf: %v", err)
	}
	pdfBody := pdf.String()
	// The PDF content stream escapes "(" and ")", so assert on paren-free fragments.
	for _, want := range []string{"Release trend", "run-early", "Regressions vs prior run"} {
		if !strings.Contains(pdfBody, want) {
			t.Fatalf("executive pdf missing %q", want)
		}
	}

	var csvBuf bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &csvBuf, "executive"); err != nil {
		t.Fatalf("write executive csv: %v", err)
	}
	csvBody := csvBuf.String()
	for _, want := range []string{
		"release_trend,,release_id,rel-1",
		"release_trend,baseline,run_id,run-early",
		"release_trend,current,run_id,run-late",
		"release_trend,delta,error_pct_delta,2",
		"release_trend,,regressed,true",
	} {
		if !strings.Contains(csvBody, want) {
			t.Fatalf("executive csv missing row %q:\n%s", want, csvBody)
		}
	}
}

func TestEngineerReleaseTrendCSVOmitted(t *testing.T) {
	report := sampleReleaseTrendReport()
	report.ReleaseTrend = sampleReleaseTrendSummary(t)

	var csvBuf bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &csvBuf, "engineer"); err != nil {
		t.Fatalf("write engineer csv: %v", err)
	}
	if strings.Contains(csvBuf.String(), "release_trend") {
		t.Fatalf("engineer csv should omit the release_trend section")
	}
}
