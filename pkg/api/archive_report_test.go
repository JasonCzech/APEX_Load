package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestBuildRunArchiveReportComputesOverallPercentiles(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "pct-1",
		TestName:              "Percentiles",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}

	timestamp := time.Date(2026, time.May, 16, 10, 0, 0, 0, time.UTC)
	txFile, err := os.Create(filepath.Join(archiveDir, "transactions.jsonl"))
	if err != nil {
		t.Fatalf("Create transactions.jsonl returned error: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	// Durations 10,20,…,100 ms across 10 successful transactions.
	for i := 1; i <= 10; i++ {
		tx := TransactionEvent{
			Timestamp:  timestamp.Add(time.Duration(i) * time.Second),
			TestID:     "pct-1",
			TestName:   "Percentiles",
			ActionName: "Step",
			Success:    true,
			DurationMs: float64(i * 10),
		}
		if err := encoder.Encode(tx); err != nil {
			t.Fatalf("Encode transaction returned error: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("Close transactions.jsonl returned error: %v", err)
	}
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	approx := func(label string, got, want float64) {
		if diff := got - want; diff > 0.05 || diff < -0.05 {
			t.Fatalf("%s = %.3f, want ~%.3f", label, got, want)
		}
	}
	if report.ByUnknown["Step"].Transactions != 10 || len(report.ByRequest) != 0 || len(report.ByTransaction) != 0 {
		t.Fatalf("legacy records must have their own drill-down statistics: %+v", report.ByUnknown)
	}
	// Linear-interpolation percentiles over sorted [10..100].
	approx("p50", report.Overview.P50Ms, 55.0)
	approx("p90", report.Overview.P90Ms, 91.0)
	approx("p95", report.Overview.P95Ms, 95.5)
	approx("p99", report.Overview.P99Ms, 99.1)
}

func TestBuildRunArchiveReportAggregatesNetworkPhases(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "network-phase-1",
		TestName:              "Network Phase Rollup",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}

	timestamp := time.Date(2026, time.May, 16, 10, 0, 0, 0, time.UTC)
	txFile, err := os.Create(filepath.Join(archiveDir, "transactions.jsonl"))
	if err != nil {
		t.Fatalf("Create transactions.jsonl returned error: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	for _, tx := range []TransactionEvent{
		{Timestamp: timestamp, TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "Checkout", DependencyType: "http", DependencyTarget: "api.example.test", DNSMs: 1, ConnectMs: 2, TLSMs: 3, TTFBMs: 40, Success: true},
		{Timestamp: timestamp.Add(time.Second), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "Checkout", DependencyType: "http", DependencyTarget: "api.example.test", DNSMs: 3, ConnectMs: 6, TLSMs: 9, TTFBMs: 80, Success: true},
		{Timestamp: timestamp.Add(2 * time.Second), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "Health", DNSMs: 0, ConnectMs: 0, TLSMs: 0, TTFBMs: 0, Success: true},
	} {
		if err := encoder.Encode(tx); err != nil {
			t.Fatalf("Encode transaction returned error: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("Close transactions.jsonl returned error: %v", err)
	}
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.NetworkPhases.Samples != 2 {
		t.Fatalf("network phase samples = %d, want 2", report.NetworkPhases.Samples)
	}
	if got := report.NetworkPhases.Overall; got.Count != 2 || got.AvgDNSMs != 2 || got.AvgConnectMs != 4 || got.AvgTLSMs != 6 || got.AvgTTFBMs != 60 || got.MaxTTFBMs != 80 {
		t.Fatalf("overall network phases = %#v, want averaged DNS/connect/TLS/TTFB", got)
	}
	if got := report.NetworkPhases.ByAction["Checkout"]; got.Count != 2 || got.AvgTTFBMs != 60 {
		t.Fatalf("checkout network phases = %#v, want action rollup", got)
	}
	if got := report.NetworkPhases.ByDependency["http:api.example.test"]; got.Count != 2 || got.MaxConnectMs != 6 {
		t.Fatalf("dependency network phases = %#v, want dependency rollup", got)
	}
}

func TestBuildRunArchiveReportClustersErrorPatterns(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-1",
		TestName:              "Checkout",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}

	timestamp := time.Date(2026, time.May, 16, 10, 0, 0, 0, time.UTC)
	txFile, err := os.Create(filepath.Join(archiveDir, "transactions.jsonl"))
	if err != nil {
		t.Fatalf("Create transactions.jsonl returned error: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	for _, tx := range []TransactionEvent{
		{Timestamp: timestamp, TestID: "test-1", TestName: "Checkout", ActionName: "CreateOrder", Success: false, ErrorMessage: "Request validation failed for order 1001"},
		{Timestamp: timestamp.Add(20 * time.Second), TestID: "test-1", TestName: "Checkout", ActionName: "CreateOrder", Success: false, ErrorMessage: "Request validation failed for order 1002"},
		{Timestamp: timestamp.Add(40 * time.Second), TestID: "test-1", TestName: "Checkout", ActionName: "HealthCheck", Success: true},
	} {
		if err := encoder.Encode(tx); err != nil {
			t.Fatalf("Encode transaction returned error: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("Close transactions.jsonl returned error: %v", err)
	}
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if len(report.ErrorPatterns) != 1 {
		t.Fatalf("expected 1 clustered error pattern, got %#v", report.ErrorPatterns)
	}
	pattern := report.ErrorPatterns[0]
	if pattern.Count != 2 {
		t.Fatalf("pattern count = %d, want 2", pattern.Count)
	}
	if pattern.Signature != "request validation failed for order #" {
		t.Fatalf("pattern signature = %q, want normalized order signature", pattern.Signature)
	}
	if len(pattern.Actions) != 1 || pattern.Actions[0] != "CreateOrder" {
		t.Fatalf("pattern actions = %#v, want CreateOrder", pattern.Actions)
	}
	if pattern.FirstSeen != timestamp || pattern.LatestSeen != timestamp.Add(20*time.Second) {
		t.Fatalf("pattern timing = %#v, want first=%s latest=%s", pattern, timestamp, timestamp.Add(20*time.Second))
	}
}

func TestBuildRunArchiveReportDeclaresEvidenceTiers(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	requestedAt := time.Date(2026, time.May, 18, 11, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-evidence",
		TestName:              "Evidence Run",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		Provenance: RunArchiveProvenance{
			ProjectID:   "checkout",
			RequestedAt: requestedAt,
			TotalVUsers: 25,
			Duration:    "5m",
			Groups:      []TestGroup{{Name: "buyers", ScriptID: "script-1", Protocol: "browser", VUsers: 25}},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}

	txFile, err := os.Create(filepath.Join(archiveDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatalf("Create transactions returned error: %v", err)
	}
	txEncoder := json.NewEncoder(txFile)
	if err := txEncoder.Encode(TransactionEvent{
		Timestamp:    requestedAt,
		TestID:       manifest.TestID,
		TestName:     manifest.TestName,
		ActionName:   "Open Home",
		DurationMs:   120,
		Success:      false,
		ErrorMessage: "browser assertion failed",
		Snapshot: &protocol.ErrorSnapshot{
			ScreenshotBase64: "abc",
			HTML:             "<html></html>",
			DOMSummary:       &protocol.DOMSummary{Title: "Home"},
			ConsoleEntries:   []protocol.ConsoleEntry{{Level: "error", Text: "boom"}},
			NetworkEntries:   []protocol.NetworkEntry{{URL: "https://example.test/app"}},
		},
	}); err != nil {
		t.Fatalf("Encode transaction returned error: %v", err)
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("Close transactions returned error: %v", err)
	}

	resourceFile, err := os.Create(filepath.Join(archiveDir, manifest.ResourceSamplesFile))
	if err != nil {
		t.Fatalf("Create resources returned error: %v", err)
	}
	if err := json.NewEncoder(resourceFile).Encode(ResourceSample{
		Timestamp: requestedAt,
		TestID:    manifest.TestID,
		TestName:  manifest.TestName,
		External:  map[string]float64{"apm.error_rate": 0.5},
	}); err != nil {
		t.Fatalf("Encode resource returned error: %v", err)
	}
	if err := resourceFile.Close(); err != nil {
		t.Fatalf("Close resources returned error: %v", err)
	}

	summaryFile, err := os.Create(filepath.Join(archiveDir, manifest.IntervalSummariesFile))
	if err != nil {
		t.Fatalf("Create summaries returned error: %v", err)
	}
	if err := json.NewEncoder(summaryFile).Encode(IntervalSummaryRecord{
		Timestamp: requestedAt,
		TestID:    manifest.TestID,
		TestName:  manifest.TestName,
		TotalTx:   1,
	}); err != nil {
		t.Fatalf("Encode summary returned error: %v", err)
	}
	if err := summaryFile.Close(); err != nil {
		t.Fatalf("Close summaries returned error: %v", err)
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	tiers := make(map[string]RunArchiveEvidenceTier)
	for _, tier := range report.EvidenceTiers {
		tiers[tier.Tier] = tier
	}
	for _, tierName := range []string{"summary", "intervals", "sampled_diagnostics", "transaction_archive", "browser_artifacts", "provenance", "external_metrics"} {
		tier, ok := tiers[tierName]
		if !ok {
			t.Fatalf("missing evidence tier %q in %#v", tierName, report.EvidenceTiers)
		}
		if tier.Status != "present" {
			t.Fatalf("tier %q status = %q, want present", tierName, tier.Status)
		}
		if tier.Count == 0 {
			t.Fatalf("tier %q count = 0, want evidence count", tierName)
		}
	}
}

func TestBuildRunArchiveReportIncludesWeb2MigrationEvidence(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	startedAt := time.Date(2026, time.May, 18, 12, 30, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-web2",
		TestName:              "Web2 Migration Evidence",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}
	txFile, err := os.Create(filepath.Join(archiveDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatalf("Create transactions returned error: %v", err)
	}
	if err := json.NewEncoder(txFile).Encode(TransactionEvent{
		Timestamp:         startedAt,
		TestID:            manifest.TestID,
		TestName:          manifest.TestName,
		ActionName:        "Checkout_Click",
		DurationMs:        85,
		Success:           true,
		NetworkEntries:    []protocol.NetworkEntry{{URL: "https://example.test/api/cart", Method: "POST", StatusCode: 200}},
		DOMSummary:        &protocol.DOMSummary{Title: "Cart", ReadyState: "complete"},
		ClientMetrics:     &protocol.ClientMetrics{LCPMs: 250, BrowserEngine: "chromium"},
		SourceStepID:      "step-checkout",
		SourceStepIndex:   7,
		SourceAction:      "Click",
		SourceActionName:  "Click Checkout",
		SourceTransaction: "Checkout",
		SourceProfile:     "web2_browser_profile",
	}); err != nil {
		t.Fatalf("Encode transaction returned error: %v", err)
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("Close transactions returned error: %v", err)
	}
	for _, name := range []string{manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.Web2Migration == nil {
		t.Fatal("expected Web2 migration evidence")
	}
	if report.Web2Migration.Status != "ready_for_review" || !report.Web2Migration.ConversionToHTTPReady {
		t.Fatalf("web2 migration = %#v, want ready for HTTP conversion review", report.Web2Migration)
	}
	if report.Web2Migration.ImportedSteps != 1 || report.Web2Migration.ReplayedSteps != 1 || report.Web2Migration.TransactionGroups[0] != "Checkout" {
		t.Fatalf("web2 migration counts = %#v, want one replayed Checkout step", report.Web2Migration)
	}
	var csvBuf bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &csvBuf, "engineer"); err != nil {
		t.Fatalf("write engineer csv returned error: %v", err)
	}
	if !strings.Contains(csvBuf.String(), "web2_migration") || !strings.Contains(csvBuf.String(), "ready_for_review") {
		t.Fatalf("engineer csv missing Web2 migration rows:\n%s", csvBuf.String())
	}
}

func TestBuildRunArchiveReportDeclaresSchemaCompatibility(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:                archiveSchemaVersion,
		TestID:                       "test-schema",
		TestName:                     "Schema Run",
		TransactionsFile:             "transactions.jsonl",
		TransactionsParquetFile:      "transactions.parquet",
		ResourceSamplesFile:          "resources.jsonl",
		ResourceSamplesParquetFile:   "resources.parquet",
		IntervalSummariesFile:        "interval_summaries.jsonl",
		IntervalSummariesParquetFile: "interval_summaries.parquet",
		IntervalSummaryCount:         1,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}
	for _, name := range []string{
		manifest.TransactionsFile,
		manifest.TransactionsParquetFile,
		manifest.ResourceSamplesFile,
		manifest.ResourceSamplesParquetFile,
		manifest.IntervalSummariesParquetFile,
	} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}
	summaryFile, err := os.Create(filepath.Join(archiveDir, manifest.IntervalSummariesFile))
	if err != nil {
		t.Fatalf("Create interval summaries returned error: %v", err)
	}
	if err := json.NewEncoder(summaryFile).Encode(IntervalSummaryRecord{
		Timestamp: time.Date(2026, time.May, 18, 12, 0, 0, 0, time.UTC),
		TestID:    manifest.TestID,
		TestName:  manifest.TestName,
		Telemetry: &TelemetryHealth{SampleAgeMs: 50},
		TelemetryStatus: &TelemetryRunHealth{
			Status:   "healthy",
			Severity: "healthy",
		},
	}); err != nil {
		t.Fatalf("Encode interval summary returned error: %v", err)
	}
	if err := summaryFile.Close(); err != nil {
		t.Fatalf("Close interval summaries returned error: %v", err)
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.SchemaVersion != runArchiveReportSchemaVersion {
		t.Fatalf("report schema = %q, want %q", report.SchemaVersion, runArchiveReportSchemaVersion)
	}
	if len(report.ReportProfiles) != 3 || report.ReportProfiles[0].ID != "executive" || report.ReportProfiles[2].ID != "audit" {
		t.Fatalf("report profiles = %#v, want executive, engineer, audit", report.ReportProfiles)
	}
	if !runArchiveReportProfileSupportsFormat(report.ReportProfiles[0], "pdf") || !runArchiveReportProfileSupportsFormat(report.ReportProfiles[1], "pdf") || !runArchiveReportProfileSupportsFormat(report.ReportProfiles[2], "pdf") {
		t.Fatalf("report profile pdf formats = %#v, want executive+engineer+audit pdf", report.ReportProfiles)
	}
	if !runArchiveReportProfileSupportsFormat(report.ReportProfiles[0], "docx") || !runArchiveReportProfileSupportsFormat(report.ReportProfiles[1], "docx") || !runArchiveReportProfileSupportsFormat(report.ReportProfiles[2], "docx") {
		t.Fatalf("report profile docx formats = %#v, want executive+engineer+audit docx", report.ReportProfiles)
	}
	if report.ReportProfiles[2].BestFor == "" || len(report.ReportProfiles[2].SectionOrder) == 0 {
		t.Fatalf("audit profile metadata = %#v, want best_for and section_order", report.ReportProfiles[2])
	}
	if report.Compatibility.Status != "compatible" || report.Compatibility.ManifestSchemaVersion != archiveSchemaVersion {
		t.Fatalf("compatibility = %#v, want compatible archive schema", report.Compatibility)
	}
	if report.Completeness.Status != "complete" || report.Completeness.ScorePct != 100 {
		t.Fatalf("completeness = %#v, want complete score", report.Completeness)
	}
	outputs := make(map[string]RunArchiveOutputCompatibility)
	for _, output := range report.Compatibility.Outputs {
		outputs[output.Format] = output
	}
	for _, format := range []string{"json", "standalone_html", "csv", "pdf", "docx", "transaction_jsonl", "resource_jsonl", "interval_summary_jsonl", "transactions_parquet", "resources_parquet", "interval_summaries_parquet"} {
		if outputs[format].Status != "compatible" {
			t.Fatalf("output %q = %#v, want compatible", format, outputs[format])
		}
	}
}

// TestRunArchiveReportProfilesAreConsistentAndRenderable is the coverage matrix
// over the shared section registry. For every shipped profile it asserts:
//   - every declared section resolves to a registered renderer (a registered
//     profile that declares a section with no renderer fails here — the
//     build-time safety net the registry provides),
//   - SectionOrder covers IncludedSections,
//   - each declared section produces non-empty rendered output in HTML, PDF, and
//     CSV (the profile metadata is the single source of truth across formats),
//   - the full HTML render places each section, in SectionOrder.
func TestRunArchiveReportProfilesAreConsistentAndRenderable(t *testing.T) {
	report := consistencyMatrixReport()
	manifest := consistencyMatrixManifest()

	profiles := buildRunArchiveReportProfiles()
	if len(profiles) == 0 {
		t.Fatal("expected at least one report profile")
	}

	builtinIDs := map[string]bool{"executive": false, "engineer": false, "audit": false}

	for _, profile := range profiles {
		profile := profile
		if _, known := builtinIDs[profile.ID]; known {
			builtinIDs[profile.ID] = true
		}
		t.Run(profile.ID, func(t *testing.T) {
			// A registered profile with no renderer for a declared section is a
			// hard error: it would silently drop content at runtime.
			if missing := reportProfileSectionsMissingRenderers(profile); len(missing) > 0 {
				t.Fatalf("profile %q declares sections with no registered renderer: %v", profile.ID, missing)
			}
			if len(profile.IncludedSections) == 0 {
				t.Fatalf("profile %q declares no sections", profile.ID)
			}

			// IncludedSections and SectionOrder must be a duplicate-free bijection,
			// so the metadata is an unambiguous single source of truth.
			included := reportSectionSet(profile.IncludedSections)
			if len(included) != len(profile.IncludedSections) {
				t.Fatalf("profile %q has duplicate ids in IncludedSections: %v", profile.ID, profile.IncludedSections)
			}
			order := reportSectionSet(profile.SectionOrder)
			for _, id := range profile.IncludedSections {
				if _, ok := order[id]; !ok {
					t.Fatalf("profile %q section %q is included but absent from SectionOrder", profile.ID, id)
				}
			}
			for id := range order {
				if _, ok := included[id]; !ok {
					t.Fatalf("profile %q section %q is in SectionOrder but not IncludedSections", profile.ID, id)
				}
			}

			data := buildRunArchiveReportRenderData(report, profile, nil, manifest)
			data.Charts = buildReportCharts(report)

			for _, id := range profile.IncludedSections {
				renderer, ok := reportSectionRegistry[id]
				if !ok {
					t.Fatalf("profile %q section %q missing from registry", profile.ID, id)
				}

				htmlBody, err := renderer.HTML(&data)
				if err != nil {
					t.Fatalf("profile %q section %q HTML error: %v", profile.ID, id, err)
				}
				if strings.TrimSpace(string(htmlBody)) == "" {
					t.Fatalf("profile %q section %q produced empty HTML", profile.ID, id)
				}

				var pdfLines []string
				renderer.PDF(&data, &pdfLines)
				if len(pdfLines) == 0 {
					t.Fatalf("profile %q section %q produced no PDF lines", profile.ID, id)
				}

				csvRows := 0
				if err := renderer.CSV(&data, func(_, _, _, _ string) error { csvRows++; return nil }); err != nil {
					t.Fatalf("profile %q section %q CSV error: %v", profile.ID, id, err)
				}
				if csvRows == 0 {
					t.Fatalf("profile %q section %q produced no CSV rows", profile.ID, id)
				}
			}

			// The full HTML render places each declared section, in SectionOrder.
			htmlBytes, err := renderRunArchiveHTMLReportWithAnnotations(report, profile.ID, nil, manifest)
			if err != nil {
				t.Fatalf("profile %q HTML render error: %v", profile.ID, err)
			}
			full := string(htmlBytes)
			lastIdx := -1
			for _, id := range profile.SectionOrder {
				marker := fmt.Sprintf("data-report-section=%q", id)
				idx := strings.Index(full, marker)
				if idx < 0 {
					t.Fatalf("profile %q rendered HTML missing section marker %q", profile.ID, marker)
				}
				if idx < lastIdx {
					t.Fatalf("profile %q section %q rendered out of SectionOrder", profile.ID, id)
				}
				lastIdx = idx
			}

			var pdfBuf bytes.Buffer
			if err := writeRunArchiveReportPDFWithProfile(report, &pdfBuf, profile.ID); err != nil {
				t.Fatalf("profile %q PDF render error: %v", profile.ID, err)
			}
			var csvBuf bytes.Buffer
			if err := writeRunArchiveReportCSVWithProfile(report, &csvBuf, profile.ID); err != nil {
				t.Fatalf("profile %q CSV render error: %v", profile.ID, err)
			}
		})
	}

	// A profile that declares a section the registry doesn't know about must be
	// reported by the build-time check rather than silently fail-open.
	bogus := RunArchiveReportProfile{
		ID:               "bogus",
		IncludedSections: []string{"verdict", "does_not_exist"},
		SectionOrder:     []string{"verdict", "does_not_exist"},
	}
	missing := reportProfileSectionsMissingRenderers(bogus)
	if len(missing) != 1 || missing[0] != "does_not_exist" {
		t.Fatalf("expected unregistered section to be reported, got %v", missing)
	}

	for id, found := range builtinIDs {
		if !found {
			t.Errorf("expected built-in report profile %q to be defined", id)
		}
	}
}

// consistencyMatrixReport returns a richly-populated archive report so the
// coverage matrix exercises real data paths in every section.
func consistencyMatrixReport() *RunArchiveReport {
	completedAt := time.Date(2026, time.June, 9, 18, 0, 0, 0, time.UTC)
	scheduled := completedAt.Add(-time.Hour)
	matrixAvg := 20.0
	return &RunArchiveReport{
		SchemaVersion: runArchiveReportSchemaVersion,
		DashboardPanels: &RunArchiveDashboardPanels{
			CapturedAt: completedAt,
			Dashboards: []RunArchiveDashboard{{
				Title: "Infra",
				Panels: []RunArchiveDashboardPanel{
					{Title: "CPU", Kind: "line", Unit: "%", SeriesCount: 1, Min: &matrixAvg, Avg: &matrixAvg, Max: &matrixAvg, Last: &matrixAvg},
					{Title: "Up", Kind: "single_value", Value: &matrixAvg},
				},
			}},
		},
		Manifest: &RunArchiveManifest{
			SchemaVersion: archiveSchemaVersion,
			TestID:        "test-matrix",
			TestName:      "Coverage Matrix",
			Status:        "COMPLETE",
			CompletedAt:   completedAt,
			SLABreached:   true,
			SLADetails:    []string{"Checkout p95 exceeded SLA"},
			Artifacts: []RunArchiveArtifact{
				{Name: "transactions.jsonl", Kind: "transactions", Format: "jsonl", RecordType: "transaction", Count: 42, SizeBytes: 4096, SHA256: "abc123"},
			},
			Provenance: RunArchiveProvenance{
				ProjectID:            "checkout",
				ReleaseID:            "2026.06",
				ScenarioID:           "scenario-9",
				ScenarioMode:         "goal",
				TotalVUsers:          8,
				Duration:             "5m",
				StartDelay:           "30s",
				ScheduledStartAt:     &scheduled,
				CreatedBySubject:     "user:creator",
				CreatedByDisplayName: "Casey Creator",
				StartedBySubject:     "user:starter",
				StartedByDisplayName: "Sam Starter",
				StoppedBySubject:     "user:stopper",
				StoppedByDisplayName: "Sky Stopper",
				Governance: &RunArchiveGovernanceSnapshot{
					CapacityReservationID: "resv-1",
					LaunchPreflight:       &RunArchiveLaunchPreflightSnapshot{Status: "passed", Decision: "allow"},
					ExecutionPlanApproval: &RunArchiveExecutionPlanApprovalSnapshot{
						ID: "approval-1", PlanID: "plan-1", Policy: "quota_burst", Status: "approved",
						RequestedBy: "user:requester", ResolvedBy: "user:approver", DecisionReason: "release gate approved", RequiredRole: RoleProjectAdmin,
					},
				},
			},
		},
		Overview: RunArchiveOverview{
			Transactions: 42, Errors: 2, ErrorPct: 4.76,
			AvgResponseMs: 125, MinResponseMs: 75, MaxResponseMs: 390,
			P50Ms: 110, P90Ms: 300, P95Ms: 360, P99Ms: 388,
			PeakTPS: 12.5, PeakTargetTPS: 15, PeakActiveVUsers: 8,
			IntervalSummaries: 3, ResourceSamples: 3,
			Browser: &BrowserLiveMetrics{Samples: 4, AvgFCPMs: 130, AvgLCPMs: 280, AvgCLSScore: 0.031, AvgINPMs: 64, AvgJSHeapUsedMB: 40},
		},
		Completeness: RunArchiveCompletenessScore{
			ScorePct: 92, Status: "needs_review",
			Findings: []RunArchiveCompletenessFinding{{Dimension: "telemetry_gaps", Severity: "warning", Penalty: 8, Message: "Telemetry was degraded for one interval."}},
		},
		Compatibility: RunArchiveReportCompatibility{
			Status: "compatible", ReportSchemaVersion: runArchiveReportSchemaVersion, ManifestSchemaVersion: archiveSchemaVersion,
			Outputs: []RunArchiveOutputCompatibility{{Format: "csv", Artifact: "report", SchemaVersion: runArchiveReportSchemaVersion, Status: "compatible"}},
		},
		ChangeAttribution: RunArchiveChangeAttribution{
			ScriptRevisions: []string{"checkout-v3"}, ScenarioRevision: "scenario-9", Environment: "perf-lab",
			GeneratorVersions: []string{"apexload-0.2.0"}, DataRevision: "checkout-v3:data",
			RuntimeOverrides: true, RuntimeOverrideFields: []string{"iterations"},
		},
		EvidenceTiers: []RunArchiveEvidenceTier{{Tier: "summary", Label: "Summary", Status: "present", Count: 1, Reason: "interval summaries archived"}},
		SummaryTimeline: []RunArchiveSummaryPoint{
			{Timestamp: completedAt.Add(-time.Minute), ActiveVUsers: 8, TPS: 12.5, TargetTPS: 15, ThroughputGapPct: 16, ThroughputTargetMissed: true, P95Ms: 360, ErrorPct: 4.76, TotalTx: 42, TotalErrors: 2},
		},
		ByAction: map[string]RunArchiveActionStats{
			"checkout": {Transactions: 42, Errors: 2, ErrorPct: 4.76, AvgResponseMs: 125, MaxResponseMs: 390, P95Ms: 360},
		},
		BySOAPOperation: map[string]RunArchiveActionStats{
			"CreateOrder": {Transactions: 30, Errors: 2, ErrorPct: 6.67, AvgResponseMs: 320},
		},
		BySOAPFaultCode:     map[string]int64{"env:Sender": 2},
		BySOAPFaultCategory: map[string]int64{"client": 2},
		SOAPFaultTimeline:   []RunArchiveSOAPFaultTimelineBucket{{Timestamp: completedAt, Total: 2, ByCode: map[string]int64{"env:Sender": 2}, ByCategory: map[string]int64{"client": 2}}},
		SOAPPayloadTimeline: []RunArchiveSOAPPayloadTimelineBucket{{Timestamp: completedAt, Samples: 3, AvgRequestPayloadKB: 12.5, AvgResponsePayloadKB: 18.8, AvgEnvelopeOverheadKB: 3.1, MaxEnvelopeOverheadKB: 5.2}},
		BrowserByEngine:     map[string]*BrowserEngineLiveMetrics{"webkit": {BrowserEngine: "webkit", Samples: 4, AvgLCPMs: 280, AvgCLSScore: 0.031, AvgINPMs: 64}},
		BrowserByPlatform:   map[string]*BrowserPlatformLiveMetrics{"ios": {DevicePlatform: "ios", BrowserEngine: "webkit", Samples: 4, AvgLCPMs: 280, AvgCLSScore: 0.031, AvgINPMs: 64}},
		BrowserByDevice:     map[string]*BrowserDeviceLiveMetrics{"iphone-se": {DeviceProfile: "iphone-se", DevicePlatform: "ios", DeviceTier: "low", BrowserEngine: "webkit", Samples: 2, AvgLCPMs: 300, AvgINPMs: 70}},
		NetworkPhases: RunArchiveNetworkPhaseSummary{
			Samples: 2,
			Overall: RunArchiveNetworkPhases{Count: 2, AvgDNSMs: 2, AvgConnectMs: 4, AvgTLSMs: 6, AvgTTFBMs: 60, MaxTTFBMs: 80},
			ByHTTPVersion: map[string]RunArchiveNetworkPhases{
				"2": {Count: 1, AvgConnectMs: 4, AvgTLSMs: 12, AvgTTFBMs: 70, MaxTTFBMs: 70},
				"3": {Count: 1, AvgConnectMs: 4, AvgTTFBMs: 50, MaxTTFBMs: 50},
			},
			VersionMix: buildRunArchiveHTTPVersionMix(&protocol.HTTPVersionMix{
				Actions: 40, ByVersion: map[string]int64{"2": 10, "3": 30}, BySelection: map[string]int64{"upgraded": 36, "not_advertised": 4},
				ByFallback: map[string]int64{"quic_connect_failed": 6}, HTTP3Policies: 40, UpgradeEligible: 36, FellBack: 6, QUICAttempted: 36, QUICFailed: 6, Attempts: 46,
			}, nil),
		},
		Web2Migration: &RunArchiveWeb2MigrationEvidence{
			Status: "ready_for_review", ImportedSteps: 1, ReplayedSteps: 1, ConversionToHTTPReady: true,
			TransactionGroups: []string{"Checkout"}, ReplayEvidence: map[string]int{"network": 1},
		},
		ErrorPatterns: []RunArchiveErrorPattern{{
			Signature: "soap fault: request validation failed for order #", Sample: "soap fault: Request validation failed for order 1001",
			Count: 2, FirstSeen: completedAt.Add(-2 * time.Minute), LatestSeen: completedAt, Actions: []string{"CreateOrder"},
			Snapshot: &protocol.ErrorSnapshot{RequestURL: "https://soap.example.test/orders"},
		}},
		ArtifactInventory: []RunArchiveArtifact{{Name: "transactions.jsonl", RecordType: "transaction", Format: "jsonl", Count: 42, SizeBytes: 4096, SHA256: "abc123"}},
		ResourcePeaks:     RunArchiveResourcePeaks{CPUProcessPct: 67, MemProcessMB: 512, NetSentKbps: 1200, NetRecvKbps: 1800},
	}
}

func consistencyMatrixManifest() *TestRunManifest {
	return &TestRunManifest{
		TestID:   "test-matrix",
		TestName: "Coverage Matrix",
		LoadGenerators: TestRunManifestLoadGenerators{
			Assignment:      &GeneratorAssignmentSettings{Mode: "auto", ShardStrategy: "capacity_weighted"},
			GeneratorShards: []GeneratorShardSummary{{Group: "buyers", Target: "gen-pod-1", VUsers: 8, ShardStrategy: "capacity_weighted"}},
			GeneratorInventory: []GeneratorInventoryEntry{
				{Target: "gen-pod-1", Region: "eastus2", Status: "connected", HealthScore: 98, ActiveVUsers: 8, AvailableCapacity: 992, MaxVUsers: 1000, CapacityState: "available"},
			},
		},
		Scripts: []TestRunManifestScript{{GroupName: "buyers", ScriptName: "checkout-flow", Protocol: "http", VUsers: 8, RampUp: "1m"}},
	}
}

func TestWriteRunArchiveReportCSVAuditIncludesExecutionPlanApproval(t *testing.T) {
	report := samplePDFRunArchiveReport()
	var csvBuf bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &csvBuf, "audit"); err != nil {
		t.Fatalf("write audit csv returned error: %v", err)
	}
	body := csvBuf.String()
	for _, expected := range []string{"execution_plan_approval_plan_id", "plan-1", "execution_plan_approval_decision_reason", "release gate approved"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("audit csv missing %q:\n%s", expected, body)
		}
	}
	if strings.Contains(body, ",action,checkout,transactions,") {
		t.Fatalf("audit csv should omit action drilldown:\n%s", body)
	}
	// Audit must not leak performance-tuning metrics or the network-phase drilldown.
	for _, forbidden := range []string{"peak_tps", "network_phases"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("audit csv should omit tuning metric %q:\n%s", forbidden, body)
		}
	}
}

func TestWriteRunArchiveReportCSVEngineerIncludesNetworkPhases(t *testing.T) {
	report := samplePDFRunArchiveReport()
	var csvBuf bytes.Buffer
	if err := writeRunArchiveReportCSVWithProfile(report, &csvBuf, "engineer"); err != nil {
		t.Fatalf("write engineer csv returned error: %v", err)
	}
	body := csvBuf.String()
	for _, expected := range []string{"network_phases,overall,avg_dns_ms", "network_phases,overall,avg_ttfb_ms", "network_phases,checkout,"} {
		if !strings.Contains(body, expected) {
			t.Fatalf("engineer csv missing network-phase row %q:\n%s", expected, body)
		}
	}
}

func TestBuildRunArchiveReportFlagsUnsupportedManifestSchema(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:         "apexload.archive.v999",
		TestID:                "test-schema-old",
		TestName:              "Unsupported Schema Run",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}
	for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.Compatibility.Status != "incompatible" {
		t.Fatalf("compatibility status = %q, want incompatible", report.Compatibility.Status)
	}
	if len(report.Compatibility.Issues) == 0 || report.Compatibility.Issues[0].Code != "unsupported_manifest_schema" {
		t.Fatalf("compatibility issues = %#v, want unsupported manifest issue", report.Compatibility.Issues)
	}
}

func TestBuildRunArchiveReportAcceptsV1ManifestSchema(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersionV1,
		TestID:                "test-schema-v1",
		TestName:              "Legacy Schema Run",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}
	for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.Compatibility.Status != "compatible" || report.Compatibility.ManifestSchemaVersion != archiveSchemaVersionV1 {
		t.Fatalf("compatibility = %#v, want compatible v1 archive", report.Compatibility)
	}
}

func TestBuildRunArchiveReportScoresCompletenessFindings(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-incomplete",
		TestName:              "Incomplete Run",
		Error:                 "failed to flush interval archive",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		TransactionCount:      3,
		ResourceSampleCount:   2,
		IntervalSummaryCount:  2,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, manifest.TransactionsFile), []byte(`{"test_id":"test-incomplete","success":true}`+"\n"), 0o644); err != nil {
		t.Fatalf("WriteFile transactions returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, manifest.ResourceSamplesFile), nil, 0o644); err != nil {
		t.Fatalf("WriteFile resources returned error: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archiveDir, manifest.IntervalSummariesFile), nil, 0o644); err != nil {
		t.Fatalf("WriteFile summaries returned error: %v", err)
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.Completeness.Status != "incomplete" || report.Completeness.ScorePct >= 75 {
		t.Fatalf("completeness = %#v, want incomplete score below review threshold", report.Completeness)
	}
	findings := make(map[string]bool)
	for _, finding := range report.Completeness.Findings {
		findings[finding.Dimension] = true
	}
	for _, dimension := range []string{"archive_writer", "artifact_truncation", "missing_intervals"} {
		if !findings[dimension] {
			t.Fatalf("missing completeness dimension %q in %#v", dimension, report.Completeness.Findings)
		}
	}
}

func TestBuildRunArchiveReportIncludesChangeAttribution(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	iterations := 3
	manifest := RunArchiveManifest{
		SchemaVersion:         archiveSchemaVersion,
		TestID:                "test-attribution",
		TestName:              "Attribution Run",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		Provenance: RunArchiveProvenance{
			ScenarioID: "scenario-42",
			ProjectID:  "checkout",
			Groups: []TestGroup{
				{Name: "buyers", ScriptID: "script-a", ScriptName: "Checkout"},
				{Name: "repeat", ScriptID: "script-a", ScriptName: "Checkout"},
				{Name: "browse", ScriptID: "script-b", ScriptName: "Browse"},
			},
			RuntimeSettings: &RuntimeSettingsOverride{
				Iterations: &iterations,
				LogLevel:   "debug",
				BaseURL:    "https://checkout.example.test",
			},
			ControllerEnv: map[string]string{"APEXLOAD_ENVIRONMENT": "perf-lab"},
			GeneratorInventory: []GeneratorInventoryEntry{
				{Target: "generator-a", Version: "0.2.0"},
				{Target: "generator-b", Version: "0.2.0"},
				{Target: "generator-c", Version: "0.3.0"},
			},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("json.Marshal manifest returned error: %v", err)
	}
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile manifest returned error: %v", err)
	}
	for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatalf("WriteFile %s returned error: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	attribution := report.ChangeAttribution
	if strings.Join(attribution.ScriptRevisions, ",") != "script-a,script-b" {
		t.Fatalf("script revisions = %#v, want deduplicated script ids", attribution.ScriptRevisions)
	}
	if attribution.ScenarioRevision != "scenario-42" || attribution.Environment != "perf-lab" {
		t.Fatalf("scenario/environment attribution = %#v", attribution)
	}
	if strings.Join(attribution.GeneratorVersions, ",") != "0.2.0,0.3.0" {
		t.Fatalf("generator versions = %#v, want deduplicated versions", attribution.GeneratorVersions)
	}
	if !attribution.RuntimeOverrides || strings.Join(attribution.RuntimeOverrideFields, ",") != "iterations,log_level,base_url" {
		t.Fatalf("runtime override attribution = %#v", attribution)
	}
	if attribution.DataRevision != "script-a:data,script-b:data" {
		t.Fatalf("data revision = %q, want script data fingerprint", attribution.DataRevision)
	}
}

func TestTraceparentIDsExtractsAPMIdentifiers(t *testing.T) {
	const traceparent = "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01"

	traceID, spanID := traceparentIDs(traceparent)

	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" {
		t.Fatalf("trace id = %q, want W3C trace id", traceID)
	}
	if spanID != "00f067aa0ba902b7" {
		t.Fatalf("span id = %q, want W3C span id", spanID)
	}
	if traceID, spanID := traceparentIDs("corr-archive-1"); traceID != "" || spanID != "" {
		t.Fatalf("non-traceparent ids = (%q, %q), want empty", traceID, spanID)
	}
	if traceID, spanID := traceparentIDs("00-00000000000000000000000000000000-0000000000000000-01"); traceID != "" || spanID != "" {
		t.Fatalf("zero traceparent ids = (%q, %q), want empty", traceID, spanID)
	}
}

func TestBuildRunArchiveReportGroupsByNetworkProfile(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		TestID:                "test-network-profile",
		TestName:              "Network Profile Run",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	transactions := []TransactionEvent{
		{Timestamp: time.Now(), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "home", NetworkProfile: "preset:dsl", DurationMs: 100, Success: true},
		{Timestamp: time.Now(), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "checkout", NetworkProfile: "preset:dsl", DurationMs: 300, Success: false},
		{Timestamp: time.Now(), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "home", NetworkProfile: "custom:latency=200ms", DurationMs: 200, Success: true},
	}
	txFile, err := os.Create(filepath.Join(tempDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatalf("create transactions: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	for _, tx := range transactions {
		if err := encoder.Encode(tx); err != nil {
			t.Fatalf("encode transaction: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("close transactions: %v", err)
	}
	for _, name := range []string{manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(tempDir, name), nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: tempDir, ManifestPath: filepath.Join(tempDir, "manifest.json")})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	dsl := report.ByNetworkProfile["preset:dsl"]
	if dsl.Transactions != 2 || dsl.Errors != 1 || dsl.AvgResponseMs != 200 {
		t.Fatalf("preset:dsl stats = %#v, want two tx, one error, avg 200", dsl)
	}
	if report.ByNetworkProfile["custom:latency=200ms"].Transactions != 1 {
		t.Fatalf("custom network profile stats = %#v, want one tx", report.ByNetworkProfile["custom:latency=200ms"])
	}
}

func TestBuildRunArchiveReportGroupsByRegion(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		TestID:                "test-region",
		TestName:              "Region Run",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tempDir, "manifest.json"), manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	transactions := []TransactionEvent{
		{Timestamp: time.Now(), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "home", Region: "us-east", DurationMs: 100, Success: true},
		{Timestamp: time.Now(), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "checkout", Region: "us-east", DurationMs: 300, Success: false},
		{Timestamp: time.Now(), TestID: manifest.TestID, TestName: manifest.TestName, ActionName: "home", Region: "eu-west", DurationMs: 200, Success: true},
	}
	txFile, err := os.Create(filepath.Join(tempDir, manifest.TransactionsFile))
	if err != nil {
		t.Fatalf("create transactions: %v", err)
	}
	encoder := json.NewEncoder(txFile)
	for _, tx := range transactions {
		if err := encoder.Encode(tx); err != nil {
			t.Fatalf("encode transaction: %v", err)
		}
	}
	if err := txFile.Close(); err != nil {
		t.Fatalf("close transactions: %v", err)
	}
	for _, name := range []string{manifest.ResourceSamplesFile, manifest.IntervalSummariesFile} {
		if err := os.WriteFile(filepath.Join(tempDir, name), nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{TestID: manifest.TestID, ArchivePath: tempDir, ManifestPath: filepath.Join(tempDir, "manifest.json")})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	east := report.ByRegion["us-east"]
	if east.Transactions != 2 || east.Errors != 1 || east.AvgResponseMs != 200 {
		t.Fatalf("us-east stats = %#v, want two tx, one error, avg 200", east)
	}
	if report.ByRegion["eu-west"].Transactions != 1 || report.ByRegion["eu-west"].Errors != 0 {
		t.Fatalf("eu-west stats = %#v, want one tx, no errors", report.ByRegion["eu-west"])
	}
}

func TestRunArchiveWriterStampsRegionFromEnv(t *testing.T) {
	t.Setenv("APEXLOAD_REGION", "ap-south")
	w, err := newRunArchiveWriter(&ManagedTest{ID: "rgn-1", Name: "Region Stamp"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatalf("new writer: %v", err)
	}
	if w.region != "ap-south" {
		t.Fatalf("writer region = %q, want ap-south", w.region)
	}
	// A result without an explicit region inherits the writer's region; an
	// explicit per-result region wins (the distributed multi-region path).
	if got := firstNonEmpty("", w.region); got != "ap-south" {
		t.Fatalf("inherited region = %q", got)
	}
	if got := firstNonEmpty("us-west", w.region); got != "us-west" {
		t.Fatalf("explicit per-result region should win, got %q", got)
	}
}

func TestBuildRunArchiveReportIncludesThroughputGapEvidence(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		TestID:                "test-throughput-gap",
		TestName:              "Open Workload",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
		Provenance: RunArchiveProvenance{
			Groups: []TestGroup{{
				Name:           "buyers",
				ScriptID:       "script-1",
				Protocol:       "http",
				VUsers:         8,
				Stages:         []storage.LoadStage{{Name: "Warmup", Type: "ramp_up", Duration: "1m", TargetVUsers: 8}, {Name: "Steady", Type: "hold", Duration: "2m", TargetVUsers: 8}},
				RunLogicGoals:  &RunLogicGoalSnapshot{GlobalIterations: 10, MaxTransactions: 50},
				Initialization: "at_once",
			}},
		},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestPath := filepath.Join(tempDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile} {
		if err := os.WriteFile(filepath.Join(tempDir, name), nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	summaryFile, err := os.Create(filepath.Join(tempDir, manifest.IntervalSummariesFile))
	if err != nil {
		t.Fatalf("create summaries: %v", err)
	}
	encoder := json.NewEncoder(summaryFile)
	start := time.Date(2026, time.May, 18, 14, 0, 0, 0, time.UTC)
	for _, summary := range []IntervalSummaryRecord{
		{Timestamp: start, TestID: manifest.TestID, TestName: manifest.TestName, ActiveVUsers: 5, TPS: 24, TargetTPS: 25, ThroughputGapPct: 4, TotalTx: 24},
		{Timestamp: start.Add(time.Second), TestID: manifest.TestID, TestName: manifest.TestName, ActiveVUsers: 8, TPS: 18, TargetTPS: 25, ThroughputGapPct: 28, ThroughputTargetMissed: true, TotalTx: 42},
	} {
		if err := encoder.Encode(summary); err != nil {
			t.Fatalf("encode summary: %v", err)
		}
	}
	if err := summaryFile.Close(); err != nil {
		t.Fatalf("close summaries: %v", err)
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: tempDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.Overview.PeakTargetTPS != 25 {
		t.Fatalf("peak target TPS = %v, want 25", report.Overview.PeakTargetTPS)
	}
	if report.Overview.MaxThroughputGapPct != 28 {
		t.Fatalf("max throughput gap = %v, want 28", report.Overview.MaxThroughputGapPct)
	}
	if report.Overview.ThroughputMissedIntervals != 1 {
		t.Fatalf("missed intervals = %d, want 1", report.Overview.ThroughputMissedIntervals)
	}
	if report.Overview.LoadModelGoals == nil || report.Overview.LoadModelGoals.StageCount != 2 || report.Overview.LoadModelGoals.ConfiguredMaxTransactions != 50 || report.Overview.LoadModelGoals.OnceOnlyGroups != 1 {
		t.Fatalf("load model goals = %#v, want staged max-transaction once-only evidence", report.Overview.LoadModelGoals)
	}
	if report.Overview.GoalCompletion == nil || report.Overview.GoalCompletion.ConfiguredMaxTransactions != 50 || report.Overview.GoalCompletion.TransactionCompletionPct != 0 {
		t.Fatalf("goal completion = %#v, want configured transaction goal evidence", report.Overview.GoalCompletion)
	}
	if len(report.SummaryTimeline) != 2 {
		t.Fatalf("summary timeline len = %d, want 2", len(report.SummaryTimeline))
	}
	if !report.SummaryTimeline[1].ThroughputTargetMissed || report.SummaryTimeline[1].ThroughputGapPct != 28 {
		t.Fatalf("summary timeline[1] = %#v, want archived throughput miss", report.SummaryTimeline[1])
	}
}

func TestBuildRunArchiveReportIncludesTelemetryHealthEvidence(t *testing.T) {
	tempDir := t.TempDir()
	manifest := RunArchiveManifest{
		TestID:                "test-telemetry-health",
		TestName:              "Telemetry Evidence",
		TransactionsFile:      "transactions.jsonl",
		ResourceSamplesFile:   "resources.jsonl",
		IntervalSummariesFile: "interval_summaries.jsonl",
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	manifestPath := filepath.Join(tempDir, "manifest.json")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	for _, name := range []string{manifest.TransactionsFile, manifest.ResourceSamplesFile} {
		if err := os.WriteFile(filepath.Join(tempDir, name), nil, 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	summaryFile, err := os.Create(filepath.Join(tempDir, manifest.IntervalSummariesFile))
	if err != nil {
		t.Fatalf("create summaries: %v", err)
	}
	encoder := json.NewEncoder(summaryFile)
	start := time.Date(2026, time.May, 18, 16, 0, 0, 0, time.UTC)
	for _, summary := range []IntervalSummaryRecord{
		{
			Timestamp: start,
			TestID:    manifest.TestID,
			TestName:  manifest.TestName,
			Telemetry: &TelemetryHealth{
				SampleAgeMs:              900,
				GeneratorProcessingLagMs: 120,
			},
			TelemetryStatus: &TelemetryRunHealth{Status: "healthy", Severity: "healthy"},
		},
		{
			Timestamp: start.Add(time.Second),
			TestID:    manifest.TestID,
			TestName:  manifest.TestName,
			Telemetry: &TelemetryHealth{
				DroppedResults:           2,
				WebsocketDroppedMessages: 3,
				SampleAgeMs:              2500,
				GeneratorProcessingLagMs: 750,
				TransportLagMs:           450,
				ControllerFanoutLagMs:    350,
				BrowserRenderLagMs:       225,
			},
			TelemetryStatus: &TelemetryRunHealth{
				Status:          "breached",
				Severity:        "danger",
				SustainedBreach: true,
				Conditions:      []string{"collector_drops", "stale_sample"},
			},
		},
	} {
		if err := encoder.Encode(summary); err != nil {
			t.Fatalf("encode summary: %v", err)
		}
	}
	if err := summaryFile.Close(); err != nil {
		t.Fatalf("close summaries: %v", err)
	}

	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: tempDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	telemetry := report.Overview.Telemetry
	if telemetry == nil {
		t.Fatal("expected telemetry overview")
	}
	if telemetry.Status != "breached" || telemetry.Samples != 2 || telemetry.BreachedIntervals != 1 || telemetry.HealthyIntervals != 1 {
		t.Fatalf("telemetry overview = %#v, want breached summary", telemetry)
	}
	if telemetry.DroppedResults != 2 || telemetry.WebsocketDroppedMessages != 3 {
		t.Fatalf("telemetry drops = %#v, want collector/websocket drops", telemetry)
	}
	if telemetry.MaxSampleAgeMs != 2500 || telemetry.MaxGeneratorProcessingLagMs != 750 || telemetry.MaxBrowserRenderLagMs != 225 {
		t.Fatalf("telemetry lag maxima = %#v", telemetry)
	}
	if telemetry.ConditionCounts["collector_drops"] != 1 || telemetry.ConditionCounts["stale_sample"] != 1 {
		t.Fatalf("condition counts = %#v", telemetry.ConditionCounts)
	}
	if telemetry.FirstBreachAt == nil || !telemetry.FirstBreachAt.Equal(start.Add(time.Second)) {
		t.Fatalf("first breach at = %#v, want second summary timestamp", telemetry.FirstBreachAt)
	}
}

func TestRunArchiveWriterPersistsThroughputGapSummary(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "test-throughput-persist", Name: "Open Persist"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatalf("newRunArchiveWriter returned error: %v", err)
	}
	if err := writer.RecordIntervalSummary(&LiveMetrics{
		Timestamp:              time.Date(2026, time.May, 18, 15, 0, 0, 0, time.UTC),
		ActiveVUsers:           12,
		TPS:                    40,
		TargetTPS:              50,
		ThroughputGapPct:       20,
		ThroughputTargetMissed: true,
		TotalTx:                40,
		Transactions: map[string]*TxMetrics{
			"Search": {Name: "Search", TPS: 4, Count: 5, Errors: 1, AvgMs: 120},
		},
		Telemetry: &TelemetryHealth{
			SampleAgeMs:              2100,
			GeneratorProcessingLagMs: 650,
		},
		TelemetryStatus: &TelemetryRunHealth{
			Status:     "degraded",
			Severity:   "warning",
			Conditions: []string{"stale_sample", "generator_lag"},
		},
	}); err != nil {
		t.Fatalf("RecordIntervalSummary returned error: %v", err)
	}
	record, err := writer.Finalize("COMPLETE", time.Now(), "", false, false, nil)
	if err != nil {
		t.Fatalf("Finalize returned error: %v", err)
	}

	summaries, err := readAllArchiveSummaries(record)
	if err != nil {
		t.Fatalf("readAllArchiveSummaries returned error: %v", err)
	}
	if len(summaries) != 1 {
		t.Fatalf("summaries len = %d, want 1", len(summaries))
	}
	if summaries[0].TargetTPS != 50 || summaries[0].ThroughputGapPct != 20 || !summaries[0].ThroughputTargetMissed {
		t.Fatalf("summary = %#v, want throughput gap evidence persisted", summaries[0])
	}
	if search := summaries[0].Transactions["Search"]; search == nil || search.TPS != 4 || search.Count != 5 || search.Errors != 1 || search.AvgMs != 120 {
		t.Fatalf("summary transaction metrics = %#v, want compact Search series persisted", summaries[0].Transactions)
	}
	summaryData, err := os.ReadFile(filepath.Join(record.ArchivePath, "interval_summaries.jsonl"))
	if err != nil {
		t.Fatalf("ReadFile interval summaries returned error: %v", err)
	}
	var summaryJSON map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(summaryData), &summaryJSON); err != nil {
		t.Fatalf("Unmarshal interval summary JSONL returned error: %v", err)
	}
	if _, ok := summaryJSON["transactions"]; !ok {
		t.Fatalf("durable interval summary JSONL omitted transactions: %s", summaryData)
	}
	if summaries[0].Telemetry == nil || summaries[0].Telemetry.SampleAgeMs != 2100 || summaries[0].Telemetry.GeneratorProcessingLagMs != 650 {
		t.Fatalf("summary telemetry = %#v, want telemetry lag evidence persisted", summaries[0].Telemetry)
	}
	if summaries[0].TelemetryStatus == nil || summaries[0].TelemetryStatus.Status != "degraded" || len(summaries[0].TelemetryStatus.Conditions) != 2 {
		t.Fatalf("summary telemetry status = %#v, want degraded telemetry health persisted", summaries[0].TelemetryStatus)
	}
}

func TestRunArchiveWriterFinalizesArtifactHashes(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "test-artifact-hash", Name: "Artifact Hashes"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatalf("newRunArchiveWriter returned error: %v", err)
	}
	result := &protocol.Result{
		ActionName:  "Checkout",
		StartTime:   time.Date(2026, time.May, 18, 15, 0, 0, 0, time.UTC),
		Duration:    125 * time.Millisecond,
		Success:     true,
		DNSTime:     time.Millisecond,
		ConnectTime: 2 * time.Millisecond,
		TLSTime:     3 * time.Millisecond,
		TTFB:        40 * time.Millisecond,
	}
	if err := writer.RecordResult(result); err != nil {
		t.Fatalf("RecordResult returned error: %v", err)
	}
	if err := writer.RecordIntervalSummary(&LiveMetrics{
		Timestamp:    result.StartTime.Add(time.Second),
		ActiveVUsers: 1,
		TPS:          1,
		TotalTx:      1,
	}); err != nil {
		t.Fatalf("RecordIntervalSummary returned error: %v", err)
	}
	record, err := writer.Finalize("COMPLETE", result.StartTime.Add(2*time.Second), "", false, false, nil)
	if err != nil {
		t.Fatalf("Finalize returned error: %v", err)
	}

	manifestBytes, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatalf("ReadFile manifest returned error: %v", err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(manifestBytes, &manifest); err != nil {
		t.Fatalf("Unmarshal manifest returned error: %v", err)
	}
	if manifest.SchemaVersion != archiveSchemaVersion {
		t.Fatalf("manifest schema = %q, want %q", manifest.SchemaVersion, archiveSchemaVersion)
	}
	if len(manifest.Artifacts) == 0 {
		t.Fatal("expected finalized manifest artifact integrity entries")
	}
	var txArtifact *RunArchiveArtifact
	for i := range manifest.Artifacts {
		if manifest.Artifacts[i].Name == manifest.TransactionsFile {
			txArtifact = &manifest.Artifacts[i]
			break
		}
	}
	if txArtifact == nil {
		t.Fatalf("transactions artifact missing from %#v", manifest.Artifacts)
	}
	txBytes, err := os.ReadFile(filepath.Join(record.ArchivePath, manifest.TransactionsFile))
	if err != nil {
		t.Fatalf("ReadFile transactions returned error: %v", err)
	}
	expectedHash := fmt.Sprintf("%x", sha256.Sum256(txBytes))
	if txArtifact.Count != 1 || txArtifact.SizeBytes != int64(len(txBytes)) || txArtifact.SHA256 != expectedHash {
		t.Fatalf("transactions artifact = %#v, want count/size/hash", txArtifact)
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if len(report.ArtifactInventory) == 0 || report.ArtifactInventory[0].SHA256 == "" {
		t.Fatalf("report artifact inventory = %#v, want hashed artifacts", report.ArtifactInventory)
	}
}

func TestRunArchiveWriterPersistsEnhancedResourceFields(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "test-resource-impact", Name: "Resource Impact"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatalf("newRunArchiveWriter returned error: %v", err)
	}
	timestamp := time.Date(2026, time.June, 2, 17, 0, 0, 0, time.UTC)
	if err := writer.RecordResourceSample(timestamp, 8, &engine.SystemResources{
		CPUProcessPct:                     96,
		CPUFreePct:                        4,
		MemProcessMb:                      512,
		MemFreeMb:                         1024,
		NetSentKbps:                       20,
		NetRecvKbps:                       18,
		DiskReadKbps:                      2,
		DiskWriteKbps:                     3,
		CPUIOWaitPct:                      12,
		GCPauseMs:                         140,
		ContainerCPUUsagePct:              97,
		ContainerCPUThrottledPeriodsDelta: 3,
		ContainerCPUThrottledSecondsRate:  0.4,
		ContainerMemoryWorkingSetMB:       900,
		ContainerMemoryLimitMB:            1024,
		ContainerMemoryUsagePct:           91,
		ContainerOOMKillCountDelta:        1,
		NetDropInPerSec:                   2,
		NetErrorOutPerSec:                 1,
		TCPRetransmitsPerSec:              3,
		EphemeralPortUtilizationPct:       7,
		DiskIOTimePct:                     72,
		DiskWeightedIOTimePct:             80,
		DiskInProgress:                    2,
	}); err != nil {
		t.Fatalf("RecordResourceSample returned error: %v", err)
	}
	record, err := writer.Finalize("COMPLETE", timestamp, "", false, false, nil)
	if err != nil {
		t.Fatalf("Finalize returned error: %v", err)
	}

	resources, err := readAllArchiveResources(record)
	if err != nil {
		t.Fatalf("readAllArchiveResources returned error: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("resources len = %d, want 1", len(resources))
	}
	if resources[0].ContainerCPUThrottledSecondsRate != 0.4 || resources[0].ContainerMemoryUsagePct != 91 || resources[0].TCPRetransmitsPerSec != 3 {
		t.Fatalf("resource sample = %#v, want enhanced fields persisted", resources[0])
	}

	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.ResourcePeaks.ContainerCPUThrottledSecondsRate != 0.4 || report.ResourcePeaks.ContainerOOMKillCountDelta != 1 || report.ResourcePeaks.DiskIOTimePct != 72 {
		t.Fatalf("resource peaks = %#v, want enhanced peaks", report.ResourcePeaks)
	}

	var csvBuf bytes.Buffer
	if err := writeArchiveResourcesCSV(resources, &csvBuf); err != nil {
		t.Fatalf("writeArchiveResourcesCSV returned error: %v", err)
	}
	for _, expected := range []string{"container_cpu_throttled_seconds_rate", "container_memory_usage_pct", "tcp_retransmits_per_sec", "disk_io_time_pct"} {
		if !strings.Contains(csvBuf.String(), expected) {
			t.Fatalf("resource csv missing %q:\n%s", expected, csvBuf.String())
		}
	}

	parquetResources := toParquetResources(resources)
	if len(parquetResources) != 1 || parquetResources[0].ContainerCPUThrottledSecondsRate != 0.4 || parquetResources[0].DiskWeightedIOTimePct != 80 {
		t.Fatalf("parquet resources = %#v, want enhanced fields mapped", parquetResources)
	}

	oldArchiveDir := t.TempDir()
	oldManifestPath := filepath.Join(oldArchiveDir, "manifest.json")
	oldManifest := RunArchiveManifest{
		SchemaVersion:       archiveSchemaVersion,
		TestID:              "old-resource",
		TestName:            "Old Resource",
		ResourceSamplesFile: "resources.jsonl",
	}
	oldManifestBytes, err := json.Marshal(oldManifest)
	if err != nil {
		t.Fatalf("json.Marshal old manifest returned error: %v", err)
	}
	if err := os.WriteFile(oldManifestPath, oldManifestBytes, 0o644); err != nil {
		t.Fatalf("WriteFile old manifest returned error: %v", err)
	}
	oldResource := `{"timestamp":"2026-06-02T17:00:00Z","test_id":"old-resource","cpu_process_pct":12,"mem_process_mb":256}` + "\n"
	if err := os.WriteFile(filepath.Join(oldArchiveDir, "resources.jsonl"), []byte(oldResource), 0o644); err != nil {
		t.Fatalf("WriteFile old resources returned error: %v", err)
	}
	oldResources, err := readAllArchiveResources(&storage.RunArchiveRecord{ArchivePath: oldArchiveDir, ManifestPath: oldManifestPath})
	if err != nil {
		t.Fatalf("readAllArchiveResources old record returned error: %v", err)
	}
	if len(oldResources) != 1 || oldResources[0].CPUProcessPct != 12 || oldResources[0].ContainerCPUUsagePct != 0 {
		t.Fatalf("old resources = %#v, want old record read with missing enhanced fields zero-valued", oldResources)
	}
}

func TestRunArchiveReportSummarizesOracleDBExternalMetrics(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "test-oracle-db", Name: "Oracle DB Evidence"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatalf("newRunArchiveWriter returned error: %v", err)
	}
	start := time.Date(2026, time.June, 8, 10, 0, 0, 0, time.UTC)
	// Twelve one-minute intervals: correlation needs at least ten paired
	// intervals, each sample paired only with the interval that contains it.
	waits := make([]float64, 12)
	p95s := make([]float64, 12)
	for i := range waits {
		waits[i] = 80 + 36*float64(i)
		p95s[i] = 180 + 60*float64(i)
	}
	for i := range waits {
		ts := start.Add(time.Duration(i) * time.Minute)
		if err := writer.RecordResourceSample(ts, int64(20+i), &engine.SystemResources{
			External: map[string]float64{
				"oracle.db.wait_time_ms": waits[i],
				"oracle.db.lock_waits":   float64(1 + i/4),
				"oracle.db.cpu_pct":      70 + float64(i),
			},
		}); err != nil {
			t.Fatalf("RecordResourceSample[%d] returned error: %v", i, err)
		}
		if err := writer.RecordIntervalSummary(&LiveMetrics{
			Timestamp:     ts,
			ActiveVUsers:  int64(20 + i),
			TPS:           25,
			AvgResponseMs: p95s[i] / 2,
			P95Ms:         p95s[i],
			TotalTx:       int64(100 + i),
		}); err != nil {
			t.Fatalf("RecordIntervalSummary[%d] returned error: %v", i, err)
		}
	}
	record, err := writer.Finalize("COMPLETE", start.Add(12*time.Minute), "", false, false, nil)
	if err != nil {
		t.Fatalf("Finalize returned error: %v", err)
	}

	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatalf("buildRunArchiveReport returned error: %v", err)
	}
	if report.OracleDB == nil {
		t.Fatal("OracleDB evidence = nil, want oracle.db.* summary")
	}
	if report.OracleDB.Status != "partial" || report.OracleDB.Samples != 12 {
		t.Fatalf("OracleDB evidence = %#v, want partial evidence with twelve samples", report.OracleDB)
	}
	if got := report.OracleDB.MetricStats["oracle.db.wait_time_ms"].Peak; got != 476 {
		t.Fatalf("oracle wait peak = %v, want 476", got)
	}
	if len(report.OracleDB.Correlations) == 0 || report.OracleDB.Correlations[0].Strength != "strong" {
		t.Fatalf("OracleDB correlations = %#v, want strong P95 correlation", report.OracleDB.Correlations)
	}
	evidence := map[string]RunArchiveEvidenceTier{}
	for _, tier := range report.EvidenceTiers {
		evidence[tier.Tier] = tier
	}
	if evidence["oracle_db_metrics"].Status != "present" || evidence["oracle_db_metrics"].Count != 12 {
		t.Fatalf("oracle evidence tier = %#v, want present with twelve samples", evidence["oracle_db_metrics"])
	}

	root := buildRunRootCauseSummary("test-oracle-db", report)
	foundOracle := false
	for _, finding := range root.TopFindings {
		if finding.Category == "oracle_db" {
			foundOracle = true
			if finding.Severity != "warning" || !strings.Contains(finding.Detail, "wait time peaked") {
				t.Fatalf("oracle finding = %#v, want wait-time warning", finding)
			}
		}
	}
	if !foundOracle {
		t.Fatalf("root cause findings = %#v, want oracle_db finding", root.TopFindings)
	}
}

func TestBuildRunArchiveReportCountsErrorPatternsOncePerFailureAndPerStatus(t *testing.T) {
	archiveDir := t.TempDir()
	manifestPath := filepath.Join(archiveDir, "manifest.json")
	manifestBytes, _ := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "test-1", TestName: "Checkout", TransactionsFile: "transactions.jsonl", ResourceSamplesFile: "resources.jsonl", IntervalSummariesFile: "interval_summaries.jsonl"})
	if err := os.WriteFile(manifestPath, manifestBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, time.October, 8, 10, 0, 0, 0, time.UTC)
	var lines []byte
	for _, tx := range []TransactionEvent{
		// A failed request and the named transaction that wraps it are both archived.
		{Timestamp: at, ActionName: "GET /cart", Population: "action", Success: false, StatusCode: 404, ErrorMessage: "expected status 200, got 404"},
		{Timestamp: at, ActionName: "Add_To_Cart", Population: "transaction", Success: false, ErrorMessage: "expected status 200, got 404"},
		{Timestamp: at.Add(time.Second), ActionName: "GET /cart", Population: "action", Success: false, StatusCode: 404, ErrorMessage: "expected status 200, got 404"},
		{Timestamp: at.Add(time.Second), ActionName: "Add_To_Cart", Population: "transaction", Success: false, ErrorMessage: "expected status 200, got 404"},
		{Timestamp: at.Add(2 * time.Second), ActionName: "GET /checkout", Population: "action", Success: false, StatusCode: 503, ErrorMessage: "expected status 200, got 503"},
		{Timestamp: at.Add(2 * time.Second), ActionName: "Checkout", Population: "transaction", Success: false, ErrorMessage: "expected status 200, got 503"},
	} {
		tx.TestID, tx.TestName = "test-1", "Checkout"
		encoded, _ := json.Marshal(tx)
		lines = append(append(lines, encoded...), '\n')
	}
	if err := os.WriteFile(filepath.Join(archiveDir, "transactions.jsonl"), lines, 0o644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(archiveDir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := buildRunArchiveReport(&storage.RunArchiveRecord{ArchivePath: archiveDir, ManifestPath: manifestPath})
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int64{}
	for _, pattern := range report.ErrorPatterns {
		counts[pattern.Sample] = pattern.Count
	}
	if len(report.ErrorPatterns) != 2 || counts["expected status 200, got 404"] != 2 || counts["expected status 200, got 503"] != 1 {
		t.Fatalf("patterns = %#v, want 404 ×2 and 503 ×1 as separate rows", report.ErrorPatterns)
	}
}

func TestErrorStatusCodeUsesTheReceivedStatus(t *testing.T) {
	for source, want := range map[string]int{
		"expected status 200, got 404":         404,
		"expected status 200, got timeout":     0,
		"request failed with status 503":       503,
		"server returned 500 Internal Error":   500,
		"received status code 429 from origin": 429,
		"connection reset by peer":             0,
	} {
		if got := errorStatusCode(0, source); got != want {
			t.Errorf("errorStatusCode(%q) = %d, want %d", source, got, want)
		}
	}
	if got := errorStatusCode(502, "expected status 200, got 404"); got != 502 {
		t.Errorf("recorded status should win, got %d", got)
	}
}

func TestRecurringErrorPatternSeverityUsesItsOwnPopulation(t *testing.T) {
	// 15 of 1000 requests failed (1.5%); each failure is also archived on its
	// enclosing named transaction, so the mixed overview holds 2000 records.
	report := &RunArchiveReport{
		Overview: RunArchiveOverview{Transactions: 2000, Errors: 30},
		MetricPopulations: map[string]ArchiveMetricPopulation{
			"action":      {RunArchiveActionStats: RunArchiveActionStats{Transactions: 1000, Errors: 15}},
			"transaction": {RunArchiveActionStats: RunArchiveActionStats{Transactions: 1000, Errors: 15}},
		},
		ErrorPatterns: []RunArchiveErrorPattern{{Signature: "got # · http 503", Sample: "got 503", Count: 15, CountsByPopulation: map[string]int64{"action": 15, "transaction": 15}}},
	}
	if rate := archiveErrorPatternFailureRate(report, report.ErrorPatterns[0]); rate != 0.015 {
		t.Fatalf("failure rate = %v, want 0.015", rate)
	}
	var severity string
	for _, finding := range deriveReportFindings(report) {
		if finding.Title == "Recurring error pattern" {
			severity = finding.Severity
		}
	}
	if severity != "major" {
		t.Fatalf("severity = %q, want major for 1.5%% of requests", severity)
	}

	// Reports cached before per-population counts keep the overview ratio.
	legacy := RunArchiveErrorPattern{Count: 30}
	if rate := archiveErrorPatternFailureRate(report, legacy); rate != 0.015 {
		t.Fatalf("legacy failure rate = %v, want 0.015", rate)
	}
}
