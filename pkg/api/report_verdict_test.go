package api

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDeriveReportVerdict(t *testing.T) {
	for _, tc := range []struct {
		name                                               string
		configured, evaluated, breach, completed, evidence bool
		want                                               string
	}{
		{name: "no criteria", completed: true, evidence: true, want: reportVerdictNotAssessed},
		{name: "criteria without samples", configured: true, completed: true, evidence: true, want: reportVerdictConditional},
		{name: "passing complete workload", configured: true, evaluated: true, completed: true, evidence: true, want: reportVerdictPass},
		{name: "early stop", configured: true, evaluated: true, evidence: true, want: reportVerdictConditional},
		{name: "missing evidence", configured: true, evaluated: true, completed: true, want: reportVerdictConditional},
		{name: "breach takes priority", breach: true, want: reportVerdictFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &RunArchiveReport{Manifest: &RunArchiveManifest{SLABreached: tc.breach}}
			if tc.configured {
				r.Manifest.Provenance.GoalConfig = &GoalConfig{P95LatencyMs: ptr(100.0)}
			}
			if tc.evaluated {
				r.ByAction = map[string]RunArchiveActionStats{"Checkout": {Transactions: 2, P95Ms: 50}}
			}
			if tc.completed {
				r.Manifest.TerminationReason = "workload_completed"
			}
			if tc.evidence {
				r.Completeness = RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"}
			}
			got := deriveReportVerdict(r)
			if got.Status != tc.want {
				t.Fatalf("got %#v, want %s", got, tc.want)
			}
			if tc.want != reportVerdictPass && strings.Contains(got.Explanation, "targets were met") {
				t.Fatal(got.Explanation)
			}
		})
	}
	if got := deriveReportVerdict(nil); got.Status != reportVerdictConditional {
		t.Fatal(got)
	}
}

func TestDeriveReportFindings(t *testing.T) {
	now := time.Date(2026, time.May, 16, 10, 0, 0, 0, time.UTC)
	report := &RunArchiveReport{
		Manifest: &RunArchiveManifest{SLABreached: true, SLADetails: []string{"p95 latency exceeded 800ms"}},
		Overview: RunArchiveOverview{
			Transactions:              1000,
			ThroughputMissedIntervals: 2,
			MaxThroughputGapPct:       12.5,
			PeakTargetTPS:             85,
		},
		ErrorPatterns: []RunArchiveErrorPattern{
			{Signature: "503 pool timeout", Sample: "HTTP 503 pool timeout", Count: 80, FirstSeen: now, LatestSeen: now.Add(2 * time.Minute), Actions: []string{"SubmitOrder"}},
		},
		Completeness: RunArchiveCompletenessScore{
			Findings: []RunArchiveCompletenessFinding{{Dimension: "telemetry_gaps", Severity: "warning", Message: "Telemetry was degraded for one or more intervals."}},
		},
	}

	findings := deriveReportFindings(report)
	if len(findings) < 4 {
		t.Fatalf("expected at least 4 findings (sla, error pattern, completeness, throughput), got %d: %#v", len(findings), findings)
	}
	if findings[0].ID != "F-01" {
		t.Fatalf("first finding ID = %q, want F-01", findings[0].ID)
	}
	if findings[0].Severity != "major" || !strings.Contains(findings[0].Observed, "p95 latency") {
		t.Fatalf("first finding should be the SLA breach, got %#v", findings[0])
	}
	// The error pattern accounts for >=1% of transactions, so it should be major.
	var sawErrorPattern bool
	for _, f := range findings {
		if strings.Contains(f.Observed, "HTTP 503 pool timeout") {
			sawErrorPattern = true
			if f.Severity != "major" {
				t.Fatalf("dominant error pattern should be major, got %q", f.Severity)
			}
		}
	}
	if !sawErrorPattern {
		t.Fatal("expected an error-pattern finding")
	}
	// IDs must be sequential (F-01, F-02, …).
	for i, f := range findings {
		if want := fmt.Sprintf("F-%02d", i+1); f.ID != want {
			t.Fatalf("finding %d ID = %q, want %q", i, f.ID, want)
		}
	}
}

func TestDeriveReportFindingsEmptyForCleanRun(t *testing.T) {
	report := &RunArchiveReport{
		Manifest:     &RunArchiveManifest{},
		Overview:     RunArchiveOverview{Transactions: 500},
		Completeness: RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"},
	}
	if findings := deriveReportFindings(report); len(findings) != 0 {
		t.Fatalf("expected no findings for a clean run, got %#v", findings)
	}
}

func TestBuildSLAComplianceRows(t *testing.T) {
	report := &RunArchiveReport{
		Manifest: &RunArchiveManifest{
			Provenance: RunArchiveProvenance{
				GoalConfig: &GoalConfig{P95LatencyMs: ptr(800.0), MaxErrorPct: ptr(1.0)},
			},
		},
		ByAction: map[string]RunArchiveActionStats{
			"FastAction":   {Transactions: 100, P95Ms: 300, ErrorPct: 0.1}, // well under SLA -> pass
			"BorderAction": {Transactions: 200, P95Ms: 760, ErrorPct: 0.2}, // within 10% of SLA -> warn
			"SlowAction":   {Transactions: 50, P95Ms: 900, ErrorPct: 0.0},  // over SLA -> fail
			"ErrorAction":  {Transactions: 75, P95Ms: 200, ErrorPct: 5.0},  // error rate over limit -> fail
		},
	}

	rows := buildSLAComplianceRows(report)
	if len(rows) != 4 {
		t.Fatalf("expected 4 rows, got %d", len(rows))
	}
	// Ordered by volume descending: BorderAction(200), FastAction(100), ErrorAction(75), SlowAction(50).
	if rows[0].Name != "BorderAction" {
		t.Fatalf("rows not ordered by volume desc: %#v", rows)
	}
	kinds := map[string]string{}
	for _, r := range rows {
		kinds[r.Name] = r.ResultKind
		if !r.HasSLA || r.SLAMs != 800 {
			t.Fatalf("row %s should carry the 800ms SLA, got %#v", r.Name, r)
		}
	}
	if kinds["FastAction"] != "pass" {
		t.Fatalf("FastAction kind = %q, want pass", kinds["FastAction"])
	}
	if kinds["BorderAction"] != "warn" {
		t.Fatalf("BorderAction kind = %q, want warn", kinds["BorderAction"])
	}
	if kinds["SlowAction"] != "fail" {
		t.Fatalf("SlowAction kind = %q, want fail", kinds["SlowAction"])
	}
	if kinds["ErrorAction"] != "fail" {
		t.Fatalf("ErrorAction kind = %q, want fail (error rate over limit)", kinds["ErrorAction"])
	}
}

func TestBuildSLAComplianceRowsNoSLAConfigured(t *testing.T) {
	report := &RunArchiveReport{
		Manifest: &RunArchiveManifest{},
		ByAction: map[string]RunArchiveActionStats{
			"Action": {Transactions: 10, P95Ms: 999, ErrorPct: 0.5},
		},
	}
	rows := buildSLAComplianceRows(report)
	if len(rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(rows))
	}
	if rows[0].HasSLA {
		t.Fatalf("HasSLA should be false with no goal config, got %#v", rows[0])
	}
	if rows[0].ResultKind != "na" || rows[0].Result != "—" {
		t.Fatalf("expected na/— result with no SLA, got %q/%q", rows[0].ResultKind, rows[0].Result)
	}
}

func TestBuildSLAComplianceRowsZeroErrorThreshold(t *testing.T) {
	report := &RunArchiveReport{
		Manifest: &RunArchiveManifest{
			Provenance: RunArchiveProvenance{
				GoalConfig: &GoalConfig{MaxErrorPct: ptr(0.0)},
			},
		},
		ByAction: map[string]RunArchiveActionStats{
			"CleanAction": {Transactions: 10, P95Ms: 999, ErrorPct: 0},
			"ErrorAction": {Transactions: 5, P95Ms: 100, ErrorPct: 0.1},
		},
	}
	rows := buildSLAComplianceRowMap(report)
	if rows["CleanAction"].ResultKind != "pass" {
		t.Fatalf("zero-error action should pass a zero max_error_pct guardrail, got %#v", rows["CleanAction"])
	}
	if rows["ErrorAction"].ResultKind != "fail" {
		t.Fatalf("non-zero errors should fail a zero max_error_pct guardrail, got %#v", rows["ErrorAction"])
	}
	rollup := buildSLARollup(rows)
	if rollup.Evaluated != 2 || rollup.Passed != 1 || rollup.Failed != 1 || rollup.WorstKind != "fail" {
		t.Fatalf("zero-error threshold rollup = %+v", rollup)
	}
}

func TestBuildSLARollupCountsWorstKind(t *testing.T) {
	rows := map[string]SLAComplianceRow{
		"ok":      {Name: "ok", ResultKind: "pass"},
		"near":    {Name: "near", ResultKind: "warn"},
		"broken":  {Name: "broken", ResultKind: "fail"},
		"unknown": {Name: "unknown", ResultKind: "na"},
	}

	rollup := buildSLARollup(rows)
	if rollup.Evaluated != 3 || rollup.Passed != 1 || rollup.Warned != 1 || rollup.Failed != 1 || rollup.NA != 1 {
		t.Fatalf("rollup counts = %+v", rollup)
	}
	if rollup.WorstKind != "fail" {
		t.Fatalf("worst kind = %q, want fail", rollup.WorstKind)
	}

	empty := buildSLARollup(nil)
	if empty.Evaluated != 0 || empty.WorstKind != "na" {
		t.Fatalf("empty rollup = %+v, want zero counts and na worst kind", empty)
	}
}

func TestApplyVerdictOverrideUnknownStatusIsConditionalNotPass(t *testing.T) {
	base := ReportVerdict{Status: reportVerdictFail, Failed: true, Explanation: "SLA breached"}
	// An out-of-vocabulary override must not fall through to clean green pass.
	got := applyVerdictOverride("Blocked", "", base)
	if got.Failed {
		t.Fatalf("unknown override should not be Failed: %+v", got)
	}
	if !got.Conditional {
		t.Fatalf("unknown override must be Conditional (warn styling): %+v", got)
	}
	if got.Status != "Blocked" {
		t.Fatalf("submitted label should be preserved, got %q", got.Status)
	}
	// Known values still map correctly.
	if v := applyVerdictOverride("pass", "", base); v.Conditional || v.Failed || v.Status != reportVerdictPass {
		t.Fatalf("pass override = %+v", v)
	}
	if v := applyVerdictOverride("fail", "", base); !v.Failed || v.Status != reportVerdictFail {
		t.Fatalf("fail override = %+v", v)
	}
}

func TestCanonicalReportFindingSeverity(t *testing.T) {
	cases := map[string]string{
		"major": "major", "Minor": "minor", "info": "info", "": "info",
		"critical": "major", "blocker": "major", "high": "major",
		"warning": "minor", "low": "minor", "weird-value": "major",
	}
	for in, want := range cases {
		if got := canonicalReportFindingSeverity(in); got != want {
			t.Errorf("canonicalReportFindingSeverity(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestReportAcceptanceRequiresEveryConfiguredCriterion(t *testing.T) {
	for _, tc := range []struct {
		name      string
		configure func(*RunArchiveReport)
		want      string
	}{
		{"load controls alone", func(r *RunArchiveReport) {
			r.Manifest.Provenance.GoalConfig = &GoalConfig{MinVUsers: ptr(1), StopOnSLABreach: true}
			r.Manifest.SLAEvaluated = true
		}, reportVerdictNotAssessed},
		{"unevaluated imported threshold", func(r *RunArchiveReport) {
			r.DashboardPanels = &RunArchiveDashboardPanels{Dashboards: []RunArchiveDashboard{{Panels: []RunArchiveDashboardPanel{{ThresholdConfigured: true}}}}}
		}, reportVerdictConditional},
		{"passing imported threshold", func(r *RunArchiveReport) {
			r.DashboardPanels = &RunArchiveDashboardPanels{Dashboards: []RunArchiveDashboard{{Panels: []RunArchiveDashboardPanel{{ThresholdConfigured: true, ThresholdEvaluated: true}}}}}
		}, reportVerdictPass},
		{"partial criterion coverage", func(r *RunArchiveReport) {
			r.Manifest.Provenance.GoalConfig = &GoalConfig{P95LatencyMs: ptr(100.0), MaxLCPMs: ptr(2500.0)}
			r.ByAction = map[string]RunArchiveActionStats{"checkout": {Transactions: 1, P95Ms: 50}}
		}, reportVerdictConditional},
		{"unachieved load", func(r *RunArchiveReport) {
			r.Manifest.Provenance.GoalConfig = &GoalConfig{P95LatencyMs: ptr(100.0)}
			r.ByAction = map[string]RunArchiveActionStats{"checkout": {Transactions: 1, P95Ms: 50}}
			r.Manifest.Provenance.TotalVUsers = 10
			r.Overview.PeakActiveVUsers = 1
		}, reportVerdictConditional},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &RunArchiveReport{Manifest: &RunArchiveManifest{TerminationReason: "workload_completed"}, Completeness: RunArchiveCompletenessScore{Status: "complete", ScorePct: 100}}
			tc.configure(r)
			if got := deriveReportVerdict(r); got.Status != tc.want {
				t.Fatalf("got %+v, want %s", got, tc.want)
			}
		})
	}
}

// A run whose only problem is unscored telemetry is not a pass: whether its
// telemetry met the budget is unknown.
func TestReportVerdictTreatsUnscoredTelemetryAsAConcern(t *testing.T) {
	passing := func() *RunArchiveReport {
		r := &RunArchiveReport{Manifest: &RunArchiveManifest{TerminationReason: "workload_completed"}}
		r.Manifest.Provenance.GoalConfig = &GoalConfig{P95LatencyMs: ptr(100.0)}
		r.ByAction = map[string]RunArchiveActionStats{"Checkout": {Transactions: 2, P95Ms: 50}}
		r.Completeness = RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"}
		r.Overview.Telemetry = &RunArchiveTelemetrySummary{}
		return r
	}
	if got := deriveReportVerdict(passing()); got.Status != reportVerdictPass {
		t.Fatalf("baseline verdict %#v", got)
	}
	r := passing()
	r.Overview.Telemetry.UnscoredIntervals = 3
	got := deriveReportVerdict(r)
	if got.Status != reportVerdictConditional || !strings.Contains(strings.Join(reportConditionalConcerns(r), ";"), "could not be scored for 3 interval(s)") {
		t.Fatalf("unscored telemetry verdict %#v concerns %v", got, reportConditionalConcerns(r))
	}
}
