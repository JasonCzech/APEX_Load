package api

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestWriteRunArchiveReportPDFWithProfile(t *testing.T) {
	report := samplePDFRunArchiveReport()

	var engineer bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &engineer, "engineer"); err != nil {
		t.Fatalf("write engineer pdf returned error: %v", err)
	}
	engineerPDF := engineer.String()
	for _, expected := range []string{
		"%PDF-1.4",
		"APEX Load Archive Report",
		"Profile: Detailed Technical",
		"Top Actions",
		"Browser Metrics",
		"Resource Peaks",
		"checkout: 42 tx",
	} {
		if !strings.Contains(engineerPDF, expected) {
			t.Fatalf("engineer pdf missing %q:\n%s", expected, engineerPDF)
		}
	}

	var executive bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &executive, "executive"); err != nil {
		t.Fatalf("write executive pdf returned error: %v", err)
	}
	executivePDF := executive.String()
	if !strings.Contains(executivePDF, "Profile: Executive Summary") {
		t.Fatalf("executive pdf missing profile label:\n%s", executivePDF)
	}
	if strings.Contains(executivePDF, "Top Actions") || strings.Contains(executivePDF, "Browser Metrics") {
		t.Fatalf("executive pdf should omit engineer drilldown sections:\n%s", executivePDF)
	}

	var audit bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &audit, "audit"); err != nil {
		t.Fatalf("write audit pdf returned error: %v", err)
	}
	auditPDF := audit.String()
	for _, expected := range []string{
		"Profile: Audit",
		"Run Identity and Authorization",
		"Schema Compatibility",
		"Artifact Integrity",
		"approval-1 approved",
		"release gate approved",
	} {
		if !strings.Contains(auditPDF, expected) {
			t.Fatalf("audit pdf missing %q:\n%s", expected, auditPDF)
		}
	}
	if strings.Contains(auditPDF, "Top Actions") || strings.Contains(auditPDF, "Browser Metrics") {
		t.Fatalf("audit pdf should omit engineer drilldown sections:\n%s", auditPDF)
	}
}

func TestWriteRunArchiveReportPDFRejectsUnsupportedProfiles(t *testing.T) {
	report := samplePDFRunArchiveReport()

	var unknown bytes.Buffer
	if err := writeRunArchiveReportPDFWithProfile(report, &unknown, "unknown"); err == nil || !strings.Contains(err.Error(), "unknown report profile") {
		t.Fatalf("unknown pdf error = %v, want unknown report profile", err)
	}
}

func samplePDFRunArchiveReport() *RunArchiveReport {
	completedAt := time.Date(2026, time.June, 2, 19, 0, 0, 0, time.UTC)
	return &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		Manifest: &RunArchiveManifest{
			SchemaVersion: archiveSchemaVersion,
			TestID:        "test-pdf",
			TestName:      "Checkout Evidence",
			CompletedAt:   completedAt,
			Provenance: RunArchiveProvenance{
				Governance: &RunArchiveGovernanceSnapshot{
					ExecutionPlanApproval: &RunArchiveExecutionPlanApprovalSnapshot{
						ID:             "approval-1",
						PlanID:         "plan-1",
						Policy:         "quota_burst",
						Status:         "approved",
						RequestedBy:    "user:requester",
						ResolvedBy:     "user:approver",
						DecisionReason: "release gate approved",
						RequiredRole:   RoleProjectAdmin,
					},
				},
			},
		},
		Overview: RunArchiveOverview{
			Transactions:     42,
			Errors:           2,
			ErrorPct:         4.76,
			AvgResponseMs:    125,
			MinResponseMs:    75,
			MaxResponseMs:    390,
			PeakTPS:          12.5,
			PeakActiveVUsers: 8,
			Browser: &BrowserLiveMetrics{
				Samples:     4,
				AvgFCPMs:    130,
				AvgLCPMs:    280,
				AvgCLSScore: 0.031,
				AvgINPMs:    64,
			},
		},
		Completeness: RunArchiveCompletenessScore{
			ScorePct: 96,
			Status:   "complete",
		},
		ChangeAttribution: RunArchiveChangeAttribution{
			ScenarioRevision:  "scenario-17",
			Environment:       "perf-lab",
			ScriptRevisions:   []string{"checkout-v3"},
			GeneratorVersions: []string{"apexload-0.2.0"},
		},
		EvidenceTiers: []RunArchiveEvidenceTier{
			{Tier: "summary", Label: "Summary", Status: "present", Count: 1},
		},
		SummaryTimeline: []RunArchiveSummaryPoint{
			{Timestamp: completedAt.Add(-time.Minute), ActiveVUsers: 8, TPS: 12.5, P95Ms: 210, ErrorPct: 4.76, TotalTx: 42, TotalErrors: 2},
		},
		ByAction: map[string]RunArchiveActionStats{
			"checkout": {Transactions: 42, Errors: 2, ErrorPct: 4.76, AvgResponseMs: 125, MinResponseMs: 75, MaxResponseMs: 390},
		},
		ResourcePeaks: RunArchiveResourcePeaks{
			CPUProcessPct: 67,
			MemProcessMB:  512,
			NetSentKbps:   1200,
			NetRecvKbps:   1800,
		},
	}
}
