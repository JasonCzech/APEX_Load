package generatorrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	parquetlocal "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/reader"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func wireBrowserDocumentSummary() *protocol.BrowserDocumentSummary {
	yes, no := true, false
	var d protocol.BrowserMetricDistribution
	d.Add(0.1)
	return &protocol.BrowserDocumentSummary{Version: protocol.BrowserEvidenceVersion, Metrics: []protocol.BrowserDocumentMetricSummary{{Metric: "cls", Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, Unit: "score", Engine: "chromium", Group: "browser-group", Cohort: &protocol.BrowserRuntimeCohort{Version: 1, BrowserVersion: "141.0", Platform: "linux", ViewportWidth: 1440, ViewportHeight: 900, Headless: &yes, Mobile: &no, Touch: &no, DeviceScaleFactor: 1, CPUThrottleRate: 1, ShapingApplied: true, CacheEnabled: &yes, CacheContentURLs: &yes, CheckNewerVersions: &no, NewUserPolicy: "persistent_context", NewUserPolicyApplied: true}, FrameScope: "main_frame", LifecyclePhase: "navigation", EligibleFinal: 1, Distribution: d}}}
}
func TestBrowserDocumentsStandaloneWireAndJSONParquetArchive(t *testing.T) {
	summary := wireBrowserDocumentSummary()
	aggregates := metricsAggregateSet{BrowserDocuments: summary}
	if !metricsAggregateSetHasEvidence(aggregates) {
		t.Fatal("actionless document evidence lost")
	}
	batch := (&Service{}).metricsBatchForMetrics("run", 7, GeneratorStats{}, nil, aggregates, false, nil, "complete", "", nil)
	decoded, err := (&MetricsStream{stream: oneTimingBatchStream{batch: batch}}).Receive()
	if err != nil {
		t.Fatal(err)
	}
	if decoded.BrowserDocuments == nil || decoded.BrowserDocuments.Metrics[0].EligibleFinal != 1 || batch.GetFleetMetrics() != nil || len(batch.GetTransactions()) != 0 {
		t.Fatal("document evidence mixed with transaction/action populations")
	}
	decoded.BrowserDocuments.Metrics[0].Cohort.BrowserVersion = "mutated"
	if summary.Metrics[0].Cohort.BrowserVersion != "141.0" {
		t.Fatal("wire aliases source")
	}
	archived := archiveIntervalSummary{BrowserDocuments: browserDocumentsFromJSON(batch.GetBrowserDocumentSummaryJson(), 2)}
	raw, err := json.Marshal(archived)
	if err != nil {
		t.Fatal(err)
	}
	var restored archiveIntervalSummary
	if json.Unmarshal(raw, &restored) != nil {
		t.Fatal("JSON archive decode failed")
	}
	parquet := toParquetSummaries([]archiveIntervalSummary{restored})
	if got := browserDocumentsFromJSON(parquet[0].BrowserDocumentsJSON, 2); got == nil || got.Metrics[0].Distribution.Count != 1 {
		t.Fatal("Parquet archive lost document denominator")
	}
}
func TestBrowserDocumentsWireRefusesForgedAndOldPeers(t *testing.T) {
	if browserDocumentsFromJSON("", 0) != nil {
		t.Fatal("old peer invented observations")
	}
	for _, raw := range []string{"", "null", "{}", `{"version":2}`, `{"version":2,"metrics":null,"dropped_observations":0}`, `{"version":2,"extra":true}`, browserDocumentsJSON(wireBrowserDocumentSummary()) + "{}", strings.Repeat("x", maxBrowserDocumentSummaryBytes+1)} {
		s := browserDocumentsFromJSON(raw, 2)
		if s == nil || !s.Incomplete || len(s.Metrics) != 0 {
			t.Fatal("malformed input appeared healthy")
		}
	}
	s := wireBrowserDocumentSummary()
	s.Metrics[0].Distribution.Buckets = [192]int64{}
	s.Metrics[0].Distribution.Buckets[180] = 1
	if got := browserDocumentsFromJSON(browserDocumentsJSON(s), 2); got == nil || !got.Incomplete {
		t.Fatal("forged bin invented percentile")
	}
}
func TestBrowserDocumentsIdlePublisherDoesNotReplayTerminalSamples(t *testing.T) {
	service := &Service{publishInterval: time.Second}
	execution := &groupExecution{lastMetrics: &executionMetricsSnapshot{aggregates: metricsAggregateSet{BrowserDocuments: wireBrowserDocumentSummary()}, telemetry: MetricsTelemetry{WindowMs: 1000}}}
	idle, ok := service.collectExecutionMetrics(execution)
	if !ok || idle.aggregates.BrowserDocuments != nil {
		t.Fatal("periodic idle window replayed finalized document")
	}
	merged := mergeMetricsAggregateSets(map[string]metricsAggregateSet{"left": {BrowserDocuments: wireBrowserDocumentSummary()}, "right": {BrowserDocuments: wireBrowserDocumentSummary()}})
	if merged.BrowserDocuments.Metrics[0].Distribution.Count != 2 {
		t.Fatal("independent reporters did not merge distributions")
	}
	filtered := browserDocumentsForGroup(merged.BrowserDocuments, "other-group")
	if len(filtered.Metrics) != 0 {
		t.Fatal("cross-group document leakage")
	}
}

func TestBrowserDocumentParquetRefusesMalformedTypedSidecarBeforeConversion(t *testing.T) {
	generation := "0123456789abcdef0123456789abcdef"
	valid := protocol.BrowserDocumentEvidence{Version: 2, SourceGeneration: generation}
	raw, err := json.Marshal(valid)
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{"null": "null", "unknown": strings.TrimSuffix(string(raw), "}") + `,"extra":true}`, "identity": `{"version":2,"source_generation":"invalid"}`, "trailing": string(raw) + "{}", "valid": string(raw)}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "browser_documents.jsonl"), []byte(data+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			root, err := os.OpenRoot(dir)
			if err != nil {
				t.Fatal(err)
			}
			defer root.Close()
			converted := 0
			err = streamJSONLToParquetAtWithBudget(root, "browser_documents.jsonl", "browser_documents.parquet", nil, func(item protocol.BrowserDocumentEvidence) parquetBrowserDocumentRecord {
				converted++
				return browserDocumentRecordParquet(item)
			}, decodeBrowserDocumentRecord)
			if name == "valid" {
				if err != nil || converted != 1 {
					t.Fatalf("valid source refused: %v", err)
				}
				return
			}
			if err == nil || converted != 0 {
				t.Fatal("malformed sidecar became a typed Parquet row")
			}
		})
	}
}

func archivedBrowserDocumentEvidence() *protocol.BrowserDocumentEvidence {
	generation := "0123456789abcdef0123456789abcdef"
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
	value := 0.1
	observation := protocol.BrowserDocumentObservation{Version: 2, SourceGeneration: generation, DocumentID: generation + "-d1", LifecycleID: generation + "-l1", FrameID: generation + "-f1", NavigationID: generation + "-n1", MeasurementID: generation + "-d1/" + generation + "-l1/cls", Revision: 1, Metric: "cls", Algorithm: protocol.BrowserVitalsAlgorithm, AlgorithmSHA256: protocol.BrowserVitalsAlgorithmSHA256, Unit: "score", Engine: "chromium", Cohort: wireBrowserDocumentSummary().Metrics[0].Cohort, FrameScope: "main_frame", LifecyclePhase: "navigation", State: "final", Boundary: "graceful_end", WindowStart: now, WindowEnd: now.Add(time.Second), CapturedAt: now.Add(time.Second), Available: true, Qualified: true, Value: &value}
	return &protocol.BrowserDocumentEvidence{Version: 2, SourceGeneration: generation, Observations: []protocol.BrowserDocumentObservation{observation}}
}
func TestBrowserDocumentDedicatedPhysicalArchiveNeverCreatesTransactionPopulation(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	archive, err := newArchiveRecorder("browser-doc-generator", "browser-doc-run", "Browser documents")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Discard()
	archive.bindBrowserDocumentCapture(true)
	archive.bindBrowserDocumentCapture(false)
	evidence := archivedBrowserDocumentEvidence()
	sink := combineSinks(&transactionBuffer{}, archive)
	if err := sink.(engine.BrowserDocumentEvidenceSink).RecordBrowserDocumentEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	record, err := archive.Finalize("COMPLETE", time.Now().UTC(), "")
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest archiveManifest
	if json.Unmarshal(data, &manifest) != nil {
		t.Fatal("manifest decode")
	}
	if manifest.TransactionCount != 0 || manifest.BrowserDocumentRecordCount != 1 || !manifest.BrowserDocumentCaptureRequested {
		t.Fatalf("independent populations or immutable request intent lost: %+v", manifest)
	}
	for name, want := range map[string]string{manifest.BrowserDocumentsFile: manifest.BrowserDocumentsSHA256, manifest.BrowserDocumentsParquetFile: manifest.BrowserDocumentsParquetSHA256} {
		data, err := os.ReadFile(filepath.Join(record.ArchivePath, name))
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(data)
		if want != hex.EncodeToString(digest[:]) {
			t.Fatal("durable sidecar proof disagrees with actual file")
		}
	}
	data, err = os.ReadFile(filepath.Join(record.ArchivePath, manifest.BrowserDocumentsFile))
	if err != nil {
		t.Fatal(err)
	}
	jsonEvidence, err := protocol.DecodeBrowserDocumentEvidence(data)
	if err != nil || !reflect.DeepEqual(jsonEvidence, evidence) {
		t.Fatal("JSONL sidecar changed observation")
	}
	file, err := parquetlocal.NewLocalFileReader(filepath.Join(record.ArchivePath, manifest.BrowserDocumentsParquetFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	pr, err := reader.NewParquetReader(file, new(parquetBrowserDocumentRecord), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer pr.ReadStop()
	if pr.GetNumRows() != 1 {
		t.Fatal("physical Parquet document record count incorrect")
	}
	rows := make([]parquetBrowserDocumentRecord, 1)
	if err := pr.Read(&rows); err != nil {
		t.Fatal(err)
	}
	parquetEvidence, err := protocol.DecodeBrowserDocumentEvidence([]byte(rows[0].EvidenceJSON))
	if err != nil || !reflect.DeepEqual(parquetEvidence, evidence) || rows[0].SourceGeneration != evidence.SourceGeneration || rows[0].EvidenceVersion != 2 {
		t.Fatal("physical Parquet sidecar changed observation")
	}
	transactions, err := os.ReadFile(filepath.Join(record.ArchivePath, manifest.TransactionsFile))
	if err != nil || len(transactions) != 0 {
		t.Fatal("standalone document finalization created action rows")
	}
}
func TestBrowserDocumentCorruptSidecarCannotPublishManifest(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	archive, err := newArchiveRecorder("browser-doc-corrupt", "browser-doc-run", "Browser documents")
	if err != nil {
		t.Fatal(err)
	}
	defer archive.Discard()
	if err := archive.RecordBrowserDocumentEvidence(archivedBrowserDocumentEvidence()); err != nil {
		t.Fatal(err)
	}
	file, err := archive.runRoot.OpenFile("browser_documents.jsonl", os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString(`{"version":2,"source_generation":"invalid"}` + "\n"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if record, err := archive.Finalize("COMPLETE", time.Now().UTC(), ""); err == nil || record != nil {
		t.Fatal("corrupt sidecar published complete archive")
	}
}
