package api

import (
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestRenderRunArchiveHTMLReportIncludesBrowserAndSOAPSections(t *testing.T) {
	now := time.Date(2026, time.May, 15, 12, 30, 0, 0, time.UTC)
	report := &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		Completeness: RunArchiveCompletenessScore{
			ScorePct: 92,
			Status:   "needs_review",
			Findings: []RunArchiveCompletenessFinding{{
				Dimension: "telemetry_gaps",
				Severity:  "warning",
				Penalty:   8,
				Message:   "Telemetry was degraded for one or more archived intervals.",
			}},
		},
		ChangeAttribution: RunArchiveChangeAttribution{
			ScriptRevisions:       []string{"script-a"},
			ScenarioRevision:      "scenario-1",
			Environment:           "perf-lab",
			GeneratorVersions:     []string{"0.2.0"},
			DataRevision:          "script-a:data",
			RuntimeOverrides:      true,
			RuntimeOverrideFields: []string{"browser"},
		},
		Compatibility: RunArchiveReportCompatibility{
			Status:                   "compatible",
			ReportSchemaVersion:      runArchiveReportSchemaVersion,
			ManifestSchemaVersion:    archiveSchemaVersion,
			SupportedManifestSchemas: []string{archiveSchemaVersion},
			Outputs: []RunArchiveOutputCompatibility{{
				Format:        "standalone_html",
				Artifact:      "report",
				SchemaVersion: runArchiveReportSchemaVersion,
				Status:        "compatible",
			}},
		},
		Manifest: &RunArchiveManifest{
			TestID:      "test-123",
			TestName:    "Checkout Flow",
			Status:      "COMPLETE",
			CompletedAt: now,
			SLABreached: true,
			SLADetails:  []string{"Latency breached"},
		},
		Overview: RunArchiveOverview{
			Transactions:     120,
			Errors:           4,
			ErrorPct:         3.33,
			AvgResponseMs:    210.2,
			MinResponseMs:    80,
			MaxResponseMs:    800,
			ResourceSamples:  10,
			PeakTPS:          34.5,
			PeakActiveVUsers: 25,
			Browser: &BrowserLiveMetrics{
				Samples:         8,
				AvgFCPMs:        120,
				AvgLCPMs:        280,
				AvgCLSScore:     0.032,
				AvgINPMs:        65,
				AvgJSHeapUsedMB: 42,
			},
		},
		SummaryTimeline: []RunArchiveSummaryPoint{{
			Timestamp:    now,
			ActiveVUsers: 25,
			TPS:          34.5,
			ErrorPct:     3.33,
			P95Ms:        410.5,
			TotalTx:      120,
			TotalErrors:  4,
		}},
		ErrorPatterns: []RunArchiveErrorPattern{{
			Signature:  "soap fault: request validation failed for order #",
			Sample:     "soap fault: Request validation failed for order 1001",
			Count:      2,
			FirstSeen:  now,
			LatestSeen: now.Add(2 * time.Minute),
			Actions:    []string{"CreateOrder"},
			Snapshot:   &protocol.ErrorSnapshot{RequestURL: "https://soap.example.test/orders"},
		}},
		ResourcePeaks: RunArchiveResourcePeaks{
			CPUProcessPct: 64.2,
			MemProcessMB:  512,
			NetSentKbps:   220,
			NetRecvKbps:   330,
		},
		ByAction: map[string]RunArchiveActionStats{
			"checkout": {Transactions: 100, Errors: 4, ErrorPct: 4, AvgResponseMs: 210, MaxResponseMs: 800},
		},
		BySOAPOperation: map[string]RunArchiveActionStats{
			"CreateOrder": {Transactions: 30, Errors: 2, ErrorPct: 6.67, AvgResponseMs: 320, MaxResponseMs: 700},
		},
		BySOAPFaultCode: map[string]int64{
			"env:Sender": 2,
		},
		BySOAPFaultCategory: map[string]int64{
			"client": 2,
		},
		SOAPFaultTimeline: []RunArchiveSOAPFaultTimelineBucket{{
			Timestamp:  now,
			Total:      2,
			ByCode:     map[string]int64{"env:Sender": 2},
			ByCategory: map[string]int64{"client": 2},
		}},
		SOAPPayloadTimeline: []RunArchiveSOAPPayloadTimelineBucket{{
			Timestamp:             now,
			Samples:               3,
			AvgRequestPayloadKB:   12.5,
			AvgResponsePayloadKB:  18.8,
			AvgEnvelopeOverheadKB: 3.1,
			MaxEnvelopeOverheadKB: 5.2,
		}},
		BrowserByEngine: map[string]*BrowserEngineLiveMetrics{
			"webkit": {BrowserEngine: "webkit", Samples: 8, AvgLCPMs: 280, AvgCLSScore: 0.032, AvgINPMs: 65},
		},
		BrowserByPlatform: map[string]*BrowserPlatformLiveMetrics{
			"ios": {DevicePlatform: "ios", BrowserEngine: "webkit", Samples: 8, AvgLCPMs: 280, AvgCLSScore: 0.032, AvgINPMs: 65},
		},
		BrowserByDevice: map[string]*BrowserDeviceLiveMetrics{
			"iphone-se": {DeviceProfile: "iphone-se", DevicePlatform: "ios", DeviceTier: "low", BrowserEngine: "webkit", Samples: 4, AvgLCPMs: 300, AvgINPMs: 70},
		},
	}

	html, err := renderRunArchiveHTMLReport(report)
	if err != nil {
		t.Fatalf("renderRunArchiveHTMLReport returned error: %v", err)
	}

	body := string(html)
	checks := []string{
		"Checkout Flow",
		"Detailed Technical profile",
		"@media print",
		"@page",
		"break-inside: avoid",
		"Summary Timeline",
		"Completeness Score",
		"Telemetry was degraded",
		"Schema Compatibility",
		"Change Attribution",
		"perf-lab",
		runArchiveReportSchemaVersion,
		"Error Patterns",
		"soap fault: Request validation failed for order 1001",
		"retained",
		"2026-05-15T12:30:00Z",
		"410.5",
		"Browser Metrics",
		"Engine Rollup",
		"Platform Rollup",
		"iphone-se",
		"SOAP Diagnostics",
		"SOAP Operation Summary",
		"CreateOrder",
		"SOAP Fault Categories",
		"client",
		"SOAP Fault Timeline",
		"env:Sender (2)",
		"SOAP Payload Timeline",
		"Latency breached",
	}
	for _, check := range checks {
		if !strings.Contains(body, check) {
			t.Fatalf("expected report HTML to contain %q, got %q", check, body)
		}
	}
}

func TestRenderRunArchiveHTMLReportSupportsProfiles(t *testing.T) {
	report := &RunArchiveReport{
		Manifest: &RunArchiveManifest{
			TestID:   "test-123",
			TestName: "Checkout Flow",
			Status:   "COMPLETE",
		},
		Overview: RunArchiveOverview{},
		ByAction: map[string]RunArchiveActionStats{},
	}

	html, err := renderRunArchiveHTMLReportWithProfile(report, "executive")
	if err != nil {
		t.Fatalf("renderRunArchiveHTMLReportWithProfile returned error: %v", err)
	}
	body := string(html)
	if !strings.Contains(body, "Executive Summary Report") {
		t.Fatalf("expected executive profile label in HTML, got %q", string(html))
	}
	if strings.Contains(body, "Top Actions") || strings.Contains(body, "Appendix C") {
		t.Fatalf("executive profile should omit technical drilldown sections, got %q", body)
	}

	auditHTML, err := renderRunArchiveHTMLReportWithProfile(report, "audit")
	if err != nil {
		t.Fatalf("renderRunArchiveHTMLReportWithProfile audit returned error: %v", err)
	}
	auditBody := string(auditHTML)
	if !strings.Contains(auditBody, "Audit Evidence Package") || strings.Contains(auditBody, "Top Actions") {
		t.Fatalf("audit profile should render audit evidence without technical drilldown, got %q", auditBody)
	}

	if _, err := renderRunArchiveHTMLReportWithProfile(report, "unknown"); err == nil {
		t.Fatal("expected unknown profile to fail")
	}
}

func TestRenderRunArchiveHTMLReportIncludesManifestAppendix(t *testing.T) {
	report := &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		Manifest: &RunArchiveManifest{
			TestID:      "test-789",
			TestName:    "Peak Certification",
			Status:      "COMPLETE",
			SLABreached: true,
			SLADetails:  []string{"p95 latency exceeded SLA"},
			Provenance:  RunArchiveProvenance{GoalConfig: &GoalConfig{P95LatencyMs: ptr(800.0)}},
		},
		Overview: RunArchiveOverview{Transactions: 1000, P95Ms: 742, PeakTPS: 84.7, PeakActiveVUsers: 600},
		ByAction: map[string]RunArchiveActionStats{
			"SubmitOrder": {Transactions: 1000, P95Ms: 742, ErrorPct: 0.4},
		},
	}
	manifest := &TestRunManifest{
		TestID:   "test-789",
		TestName: "Peak Certification",
		LoadGenerators: TestRunManifestLoadGenerators{
			Assignment:      &GeneratorAssignmentSettings{Mode: "auto", ShardStrategy: "capacity_weighted"},
			GeneratorShards: []GeneratorShardSummary{{Group: "buyers", Target: "gen-pod-1", VUsers: 300, ShardStrategy: "capacity_weighted"}},
			GeneratorInventory: []GeneratorInventoryEntry{
				{Target: "gen-pod-1", Region: "eastus2", Status: "connected", HealthScore: 98, ActiveVUsers: 300, AvailableCapacity: 700, MaxVUsers: 1000, CapacityState: "available"},
			},
		},
		Scripts: []TestRunManifestScript{{GroupName: "buyers", ScriptName: "oms-order-flows", Protocol: "http", VUsers: 300, RampUp: "15m"}},
	}

	html, err := renderRunArchiveHTMLReportWithAnnotations(report, "engineer", nil, manifest)
	if err != nil {
		t.Fatalf("render with manifest returned error: %v", err)
	}
	body := string(html)
	// An SLA breach yields a Fail verdict; the manifest appendix renders the
	// assignment, shards, inventory, and scripts pulled from the manifest.
	for _, want := range []string{
		"Appendix C — Generator assignment manifest",
		"gen-pod-1",         // shard + inventory target
		"capacity_weighted", // assignment strategy
		"oms-order-flows",   // scripts & groups
		"eastus2",           // inventory region
		"SubmitOrder",       // SLA compliance row
		"Fail",              // verdict badge from SLABreached
		"Run at a glance",   // Section 2 heading
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("expected manifest report HTML to contain %q", want)
		}
	}
}

func TestRenderRunArchiveHTMLReportHandlesNilManifest(t *testing.T) {
	report := &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		Manifest:      &RunArchiveManifest{TestID: "test-000", TestName: "No Manifest", Status: "COMPLETE"},
		Overview:      RunArchiveOverview{},
		ByAction:      map[string]RunArchiveActionStats{},
	}
	html, err := renderRunArchiveHTMLReportWithAnnotations(report, "engineer", nil, nil)
	if err != nil {
		t.Fatalf("render with nil manifest returned error: %v", err)
	}
	body := string(html)
	if !strings.Contains(body, "Appendix C — Generator assignment manifest") {
		t.Fatal("expected manifest appendix heading even with nil manifest")
	}
	if !strings.Contains(body, "generator assignment manifest was not available") {
		t.Fatalf("expected unavailable note for nil manifest, got %q", body)
	}
}

func TestRenderRunArchiveHTMLReportHonorsVerdictAndFindingsOverrides(t *testing.T) {
	report := &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		Manifest:      &RunArchiveManifest{TestID: "t", TestName: "Override", Status: "COMPLETE", SLABreached: true, SLADetails: []string{"latency"}},
		Overview:      RunArchiveOverview{Transactions: 10},
		ByAction:      map[string]RunArchiveActionStats{},
	}
	annotations := &ReportAnnotations{
		TestID:             "t",
		Profile:            "executive",
		VerdictOverride:    "Conditional Pass",
		VerdictExplanation: "Transient breach recovered without intervention.",
		Findings:           []ReportFinding{{ID: "F-01", Title: "Curated finding", Severity: "minor", Observed: "analyst observation"}},
		SectionComments:    map[string]string{},
	}
	html, err := renderRunArchiveHTMLReportWithAnnotations(report, "executive", annotations, nil)
	if err != nil {
		t.Fatalf("render with overrides returned error: %v", err)
	}
	body := string(html)
	if !strings.Contains(body, "Conditional Pass") {
		t.Fatal("expected analyst verdict override to win over auto-derived Fail")
	}
	if !strings.Contains(body, "Transient breach recovered") {
		t.Fatal("expected analyst verdict explanation")
	}
	if !strings.Contains(body, "Curated finding") {
		t.Fatal("expected analyst-curated finding to render")
	}
}
