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
		NetworkPhases: RunArchiveNetworkPhaseSummary{
			Samples: 100,
			Overall: RunArchiveNetworkPhases{Count: 100, AvgDNSMs: 2.5, AvgConnectMs: 7.1, AvgTLSMs: 11.0, AvgTTFBMs: 88.0, MaxTTFBMs: 300},
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
		"Detailed Technical Report",
		"Performance results for <strong>Checkout Flow</strong>",
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
		"Network Phase Breakdown",
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
	// The executive profile declares neither the actions nor the
	// generator-assignment sections, so the section-registry driver must omit
	// them entirely (no fail-open to another profile's content).
	for _, omit := range []string{"Action Summary", "Appendix C", `data-report-section="actions"`, `data-report-section="generator_manifest"`} {
		if strings.Contains(body, omit) {
			t.Fatalf("executive profile should omit technical drilldown %q, got %q", omit, body)
		}
	}
	// And it must include exactly its declared sections, in order.
	for _, want := range []string{`data-report-section="verdict"`, `data-report-section="executive_overview"`, `data-report-section="sla_rollup"`, `data-report-section="signoff"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("executive profile missing declared section marker %q", want)
		}
	}
	// Executive declares completeness + change_attribution; they render as the compact
	// Run Context block, not as engineer-only drilldowns or the network-phase section.
	if !strings.Contains(body, "Run Context &amp; Confidence") {
		t.Fatalf("executive profile should render the Run Context block, got %q", body)
	}
	if strings.Contains(body, "Network Phase Breakdown") {
		t.Fatalf("executive profile should omit the engineer-only network-phase section, got %q", body)
	}

	auditHTML, err := renderRunArchiveHTMLReportWithProfile(report, "audit")
	if err != nil {
		t.Fatalf("renderRunArchiveHTMLReportWithProfile audit returned error: %v", err)
	}
	auditBody := string(auditHTML)
	if !strings.Contains(auditBody, "Audit Report") || !strings.Contains(auditBody, "Run Identity") || !strings.Contains(auditBody, "Artifact Integrity") {
		t.Fatalf("audit profile should render audit evidence sections, got %q", auditBody)
	}
	if strings.Contains(auditBody, "Action Summary") || strings.Contains(auditBody, `data-report-section="actions"`) {
		t.Fatalf("audit profile should omit technical drilldown, got %q", auditBody)
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

// TestRenderRunArchiveHTMLReportHonorsIncludedSections proves the HTML renderer
// gates content blocks on the profile's IncludedSections via the shared section
// registry, exactly like the PDF/CSV renderers: a section a profile declares
// renders (with its data-report-section marker), and a profile that does not
// declare it omits it. This guards the single-source-of-truth contract — editing
// a profile's sections must change HTML output.
func TestRenderRunArchiveHTMLReportHonorsIncludedSections(t *testing.T) {
	report := &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		Manifest:      &RunArchiveManifest{TestID: "test-sections", TestName: "Section Gating", Status: "COMPLETE"},
		Overview: RunArchiveOverview{
			Transactions: 100,
			Browser:      &BrowserLiveMetrics{Samples: 4, AvgLCPMs: 280},
		},
		ByAction: map[string]RunArchiveActionStats{"checkout": {Transactions: 100}},
		NetworkPhases: RunArchiveNetworkPhaseSummary{
			Samples: 100,
			Overall: RunArchiveNetworkPhases{Count: 100, AvgTTFBMs: 88},
		},
	}

	// The engineer profile declares network_phases, browser, and actions, so all
	// three render (heading + data-report-section marker).
	html, err := renderRunArchiveHTMLReportWithProfile(report, "engineer")
	if err != nil {
		t.Fatalf("baseline render returned error: %v", err)
	}
	body := string(html)
	for _, want := range []string{
		"Network Phase Breakdown", "Browser Metrics", "Action Summary",
		`data-report-section="network_phases"`, `data-report-section="browser"`, `data-report-section="actions"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("engineer HTML should contain %q", want)
		}
	}

	// The executive profile declares none of those engineer drilldowns, so the
	// registry-driven renderer omits both their headings and their markers — proof
	// that IncludedSections, not a hardcoded template, drives the output.
	execHTML, err := renderRunArchiveHTMLReportWithProfile(report, "executive")
	if err != nil {
		t.Fatalf("executive render returned error: %v", err)
	}
	execBody := string(execHTML)
	for _, absent := range []string{
		"Network Phase Breakdown", "Browser Metrics", "Action Summary",
		`data-report-section="network_phases"`, `data-report-section="browser"`, `data-report-section="actions"`,
	} {
		if strings.Contains(execBody, absent) {
			t.Fatalf("executive HTML must omit engineer-only %q", absent)
		}
	}

	// And reportSectionsForProfile honors a synthetic reduced section set directly:
	// only declared+registered sections come back, in order.
	reduced := RunArchiveReportProfile{
		ID:               "engineer",
		IncludedSections: []string{"technical_overview", "actions"},
		SectionOrder:     []string{"technical_overview", "actions"},
	}
	var ids []string
	for _, section := range reportSectionsForProfile(reduced) {
		ids = append(ids, section.ID)
	}
	if strings.Join(ids, ",") != "technical_overview,actions" {
		t.Fatalf("reduced profile sections = %v, want [technical_overview actions]", ids)
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
	if !strings.Contains(body, "Conditional Pass") || !strings.Contains(body, "SLA breached: latency") {
		t.Fatal("expected analyst assessment to be shown separately from computed Fail")
	}
	if !strings.Contains(body, "Transient breach recovered") {
		t.Fatal("expected analyst verdict explanation")
	}
	if !strings.Contains(body, "Curated finding") {
		t.Fatal("expected analyst-curated finding to render")
	}
}

func TestReportReaderContextUsesNamesWithoutInventingEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, environment, expected string }{
		{"missing environment", "", "Not recorded"},
		{"configured environment", "Performance lab", "Performance lab"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			report := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: "internal-run-uuid", TestName: "Storefront usability verification", Provenance: RunArchiveProvenance{ProjectID: "internal-project-uuid", ScenarioID: "internal-scenario-uuid", ControllerEnv: map[string]string{"APEXLOAD_ENVIRONMENT": tc.environment}}}}
			report.ChangeAttribution = buildRunArchiveChangeAttribution(report.Manifest)
			html, err := renderRunArchiveHTMLReportWithProfile(report, "executive")
			if err != nil {
				t.Fatal(err)
			}
			for _, value := range []string{"Performance results for <strong>Storefront usability verification</strong>", tc.expected} {
				if !strings.Contains(string(html), value) {
					t.Fatalf("missing %q", value)
				}
			}
			for _, value := range []string{"internal-run-uuid", "internal-project-uuid", "internal-scenario-uuid", "profile generated from the archived run bundle"} {
				if strings.Contains(string(html), value) {
					t.Fatalf("internal metadata in executive report: %q", value)
				}
			}
			profile, _ := lookupRunArchiveReportProfile("executive")
			lines := strings.Join(buildRunArchiveReportPDFLines(report, profile, nil, nil), "\n")
			if !strings.Contains(lines, "Run: Storefront usability verification") || !strings.Contains(lines, "Environment: "+tc.expected) {
				t.Fatal(lines)
			}
			if strings.Contains(lines, "internal-run-uuid") || strings.Contains(lines, "internal-project-uuid") {
				t.Fatal("internal identifiers leaked into PDF reader context")
			}
			if report.Manifest.TestID != "internal-run-uuid" || report.Manifest.Provenance.ProjectID != "internal-project-uuid" {
				t.Fatal("raw provenance changed")
			}
		})
	}
	report := &RunArchiveReport{Manifest: &RunArchiveManifest{TestID: "internal-run-uuid"}}
	if reportRunName(report) != "Unnamed test" {
		t.Fatal("missing name must not become an ID")
	}
}

func TestReportPercentPreservesSmallNonzeroErrors(t *testing.T) {
	format := profileHTMLFuncMap["formatPercent"].(func(float64) string)
	for _, tc := range []struct {
		value float64
		want  string
	}{{0, "0.00%"}, {0.0001, "<0.01%"}, {0.01, "0.01%"}} {
		if got := format(tc.value); got != tc.want {
			t.Fatalf("format(%v)=%q, want %q", tc.value, got, tc.want)
		}
	}
}

func TestDefaultReportNarrativeNamesArchivePopulationAndNonzeroErrors(t *testing.T) {
	report := &RunArchiveReport{Overview: RunArchiveOverview{Transactions: 1000, Errors: 1, ErrorPct: 0.001}}
	for _, profile := range []string{"executive", "engineer", "audit"} {
		annotations := defaultReportAnnotations(report, "run", profile)
		if strings.Contains(annotations.ExecutiveNarrative, "transaction(s)") {
			t.Fatalf("%s mislabels archive totals: %s", profile, annotations.ExecutiveNarrative)
		}
		if profile != "audit" && !strings.Contains(annotations.ExecutiveNarrative, "<0.01%") {
			t.Fatalf("%s hides nonzero errors: %s", profile, annotations.ExecutiveNarrative)
		}
	}
}
