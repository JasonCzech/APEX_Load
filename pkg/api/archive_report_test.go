package api

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

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
		SourceProfile:     "vugen_web2",
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
	for _, format := range []string{"json", "standalone_html", "csv", "transaction_jsonl", "resource_jsonl", "interval_summary_jsonl", "transactions_parquet", "resources_parquet", "interval_summaries_parquet"} {
		if outputs[format].Status != "compatible" {
			t.Fatalf("output %q = %#v, want compatible", format, outputs[format])
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
	record, err := writer.Finalize("COMPLETE", time.Now(), "", false, nil)
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
	if summaries[0].Telemetry == nil || summaries[0].Telemetry.SampleAgeMs != 2100 || summaries[0].Telemetry.GeneratorProcessingLagMs != 650 {
		t.Fatalf("summary telemetry = %#v, want telemetry lag evidence persisted", summaries[0].Telemetry)
	}
	if summaries[0].TelemetryStatus == nil || summaries[0].TelemetryStatus.Status != "degraded" || len(summaries[0].TelemetryStatus.Conditions) != 2 {
		t.Fatalf("summary telemetry status = %#v, want degraded telemetry health persisted", summaries[0].TelemetryStatus)
	}
}
