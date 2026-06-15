package api

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestDeriveReportVerdict(t *testing.T) {
	tests := []struct {
		name        string
		report      *RunArchiveReport
		wantStatus  string
		wantFailed  bool
		wantCondtnl bool
	}{
		{
			name: "clean run passes",
			report: &RunArchiveReport{
				Manifest:     &RunArchiveManifest{},
				Completeness: RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"},
			},
			wantStatus: reportVerdictPass,
		},
		{
			name: "sla breach fails",
			report: &RunArchiveReport{
				Manifest:     &RunArchiveManifest{SLABreached: true, SLADetails: []string{"Latency breached"}},
				Completeness: RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"},
			},
			wantStatus: reportVerdictFail,
			wantFailed: true,
		},
		{
			name: "degraded telemetry is conditional",
			report: &RunArchiveReport{
				Manifest: &RunArchiveManifest{},
				Overview: RunArchiveOverview{
					Telemetry: &RunArchiveTelemetrySummary{Status: "degraded", DegradedIntervals: 2},
				},
				Completeness: RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"},
			},
			wantStatus:  reportVerdictConditional,
			wantCondtnl: true,
		},
		{
			name: "throughput miss is conditional",
			report: &RunArchiveReport{
				Manifest:     &RunArchiveManifest{},
				Overview:     RunArchiveOverview{ThroughputMissedIntervals: 3, PeakTargetTPS: 85},
				Completeness: RunArchiveCompletenessScore{ScorePct: 100, Status: "complete"},
			},
			wantStatus:  reportVerdictConditional,
			wantCondtnl: true,
		},
		{
			name: "incomplete data is conditional",
			report: &RunArchiveReport{
				Manifest:     &RunArchiveManifest{},
				Completeness: RunArchiveCompletenessScore{ScorePct: 82, Status: "needs_review"},
			},
			wantStatus:  reportVerdictConditional,
			wantCondtnl: true,
		},
		{
			name:        "nil report is conditional",
			report:      nil,
			wantStatus:  reportVerdictConditional,
			wantCondtnl: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := deriveReportVerdict(tt.report)
			if got.Status != tt.wantStatus {
				t.Fatalf("status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.Failed != tt.wantFailed {
				t.Fatalf("failed = %v, want %v", got.Failed, tt.wantFailed)
			}
			if got.Conditional != tt.wantCondtnl {
				t.Fatalf("conditional = %v, want %v", got.Conditional, tt.wantCondtnl)
			}
			if strings.TrimSpace(got.Explanation) == "" {
				t.Fatal("explanation must not be empty")
			}
		})
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
