package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	local "github.com/xitongsys/parquet-go-source/local"
	"github.com/xitongsys/parquet-go/reader"
)

func TestBrowserDocumentArchiveSidecarNeverCreatesTransactions(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	run := &ManagedTest{ID: "document-sidecar", Name: "Documents"}
	archive, err := newRunArchiveWriter(run, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	evidence := browserDocumentV2ArchiveFixture()
	if err := archive.RecordBrowserDocumentEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	if err := archive.RecordBrowserDocumentEvidence(evidence); err != nil {
		t.Fatal(err)
	}
	if err := archive.RecordResult(&protocol.Result{SampleKind: "browser_document_evidence", ClientMetrics: &protocol.ClientMetrics{DocumentEvidence: evidence}}); err == nil {
		t.Fatal("document became action record")
	}
	malformed := evidence.Clone()
	malformed.Observations[0].MeasurementID = "foreign"
	if err := archive.RecordBrowserDocumentEvidence(malformed); err == nil {
		t.Fatal("malformed record accepted")
	}
	evidence.Observations[0].UnavailableReason = "unsupported"
	record, err := archive.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := archive.RecordBrowserDocumentEvidence(browserDocumentV2ArchiveFixture()); err == nil {
		t.Fatal("closed writer accepted publication")
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.TransactionCount != 0 || manifest.BrowserDocumentRecordCount != 2 || !validArchiveSHA256(manifest.BrowserDocumentsSHA256) || !validArchiveSHA256(manifest.BrowserDocumentsParquetSHA256) {
		t.Fatalf("bad populations/proofs: %+v", manifest)
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	if report.Overview.Transactions != 0 || len(report.ByAction) != 0 || len(report.BrowserByEngine) != 0 {
		t.Fatal("sidecar inflated action report")
	}
	if report.BrowserDocuments == nil || len(report.BrowserDocuments.Metrics) != 1 || report.BrowserDocuments.Metrics[0].UnavailableFinal != 1 || report.BrowserDocuments.Metrics[0].Distribution.Count != 0 || len(report.BrowserDocumentEvidence) != 1 || report.BrowserDocumentEvidence[0].UnavailableReason != "no_interaction" {
		t.Fatalf("lifecycle identity/copy lost: %+v", report.BrowserDocuments)
	}
	file, err := local.NewLocalFileReader(filepath.Join(record.ArchivePath, manifest.BrowserDocumentsParquetFile))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	parquet, err := reader.NewParquetReader(file, new(parquetBrowserDocumentRecord), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer parquet.ReadStop()
	rows := make([]parquetBrowserDocumentRecord, parquet.GetNumRows())
	if err := parquet.Read(&rows); err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Fatal("sidecar parquet count", len(rows))
	}
	for _, row := range rows {
		var restored protocol.BrowserDocumentEvidence
		if err := json.Unmarshal([]byte(row.EvidenceJSON), &restored); err != nil {
			t.Fatal(err)
		}
		if row.EvidenceVersion != protocol.BrowserEvidenceVersion || row.SourceGeneration != restored.SourceGeneration || !reflect.DeepEqual(restored.Observations, browserDocumentV2ArchiveFixture().Observations) {
			t.Fatal("typed parquet lost document evidence")
		}
	}
	data, err := os.ReadFile(filepath.Join(record.ArchivePath, manifest.BrowserDocumentsFile))
	if err != nil {
		t.Fatal(err)
	}
	// Same-size corruption of immutable evidence cannot turn unavailable into a
	// measured zero or disappear through a legacy transaction-only report path.
	data = bytes.ReplaceAll(data, []byte("no_interaction"), []byte("no_observation"))
	if err := os.WriteFile(filepath.Join(record.ArchivePath, manifest.BrowserDocumentsFile), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := buildRunArchiveReport(record); err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatal("corrupt sidecar was ignored", err)
	}
}

func TestBrowserDocumentManifestFailsClosedAndLegacyRemainsOptional(t *testing.T) {
	if err := validateBrowserDocumentManifest(&RunArchiveManifest{}, true); err != nil {
		t.Fatal("legacy archive rejected", err)
	}
	valid := RunArchiveManifest{BrowserDocumentsFile: browserDocumentsFileName, BrowserDocumentsParquetFile: browserDocumentsParquetFileName, BrowserDocumentRecordCount: 1, BrowserDocumentsSHA256: strings.Repeat("a", 64), BrowserDocumentsParquetSHA256: strings.Repeat("b", 64)}
	for name, mutate := range map[string]func(*RunArchiveManifest){
		"missing source":  func(m *RunArchiveManifest) { m.BrowserDocumentsFile = "" },
		"nested source":   func(m *RunArchiveManifest) { m.BrowserDocumentsFile = "nested/browser_documents.jsonl" },
		"unsafe target":   func(m *RunArchiveManifest) { m.BrowserDocumentsParquetFile = "../browser_documents.parquet" },
		"missing proof":   func(m *RunArchiveManifest) { m.BrowserDocumentsSHA256 = "" },
		"uppercase proof": func(m *RunArchiveManifest) { m.BrowserDocumentsSHA256 = strings.Repeat("A", 64) },
		"empty":           func(m *RunArchiveManifest) { m.BrowserDocumentRecordCount = 0 },
		"negative":        func(m *RunArchiveManifest) { m.BrowserDocumentRecordCount = -1 },
		"oversize":        func(m *RunArchiveManifest) { m.BrowserDocumentRecordCount = maxArchiveQueryRecords + 1 },
	} {
		t.Run(name, func(t *testing.T) {
			m := valid
			mutate(&m)
			if err := validateBrowserDocumentManifest(&m, true); err == nil {
				t.Fatal("bad manifest accepted")
			}
		})
	}
}

func TestBrowserDocumentRevisionsAndReportDisplayBound(t *testing.T) {
	var accumulator archiveBrowserDocumentAccumulator
	provisional := browserDocumentV2ArchiveFixture()
	provisional.Observations[0].State = "provisional"
	provisional.Observations[0].Boundary = "progress"
	accumulator.add(provisional)
	final := provisional.Clone()
	final.Observations[0].Revision = 2
	final.Observations[0].State = "final"
	final.Observations[0].Boundary = "graceful_end"
	final.DroppedObservations = 3
	accumulator.add(final)
	accumulator.add(final)
	accumulator.add(provisional)
	summary := accumulator.summary()
	if summary == nil || len(summary.Metrics) != 1 || summary.Metrics[0].UnavailableFinal != 1 || summary.Metrics[0].Provisional != 0 || summary.DroppedObservations != 3 || !summary.Incomplete {
		t.Fatalf("revisions/retries create populations: %+v", summary)
	}
	changed := final.Clone()
	changed.Observations[0].Revision = 3
	changed.Observations[0].UnavailableReason = "unsupported"
	accumulator.add(changed)
	summary = accumulator.summary()
	if !summary.Incomplete || summary.Metrics[0].UnavailableFinal != 1 || summary.Metrics[0].ExcludedReasons["no_interaction"] != 1 {
		t.Fatal("terminal mutation silently replaced valid evidence")
	}
}

func TestBrowserDocumentParquetRejectsMalformedTypedRecord(t *testing.T) {
	root := t.TempDir()
	m := &RunArchiveManifest{BrowserDocumentsFile: browserDocumentsFileName, BrowserDocumentsParquetFile: browserDocumentsParquetFileName, BrowserDocumentRecordCount: 1}
	if err := os.WriteFile(filepath.Join(root, m.BrowserDocumentsFile), []byte("{\"version\":2}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	budget, err := newArchiveConversionBudget(root)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	if err := writeBrowserDocumentParquet(root, m, budget); err == nil {
		t.Fatal("malformed evidence became zero parquet row")
	}
}

func TestBrowserDocumentCapturePlacementRequiresExplicitFeature(t *testing.T) {
	capture := true
	script := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{CaptureDocumentMetrics: &capture}}}
	if !strings.Contains(groupRequiredRuntimeFeature(TestGroup{Protocol: "browser", script: script}), dsl.FeatureBrowserDocumentEvidenceV2) {
		t.Fatal("older peer admitted explicit document capture")
	}
	capture = false
	if strings.Contains(groupRequiredRuntimeFeature(TestGroup{Protocol: "browser", script: script}), dsl.FeatureBrowserDocumentEvidenceV2) {
		t.Fatal("default action capture forced newer capability")
	}
}

func attachBrowserDocumentSource(t *testing.T, client *archiveStreamingFakeClient, evidence *protocol.BrowserDocumentEvidence) {
	t.Helper()
	path := filepath.Join(filepath.Dir(client.artifacts[runArchiveManifestFileName]), browserDocumentsFileName)
	raw, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(client.artifacts[runArchiveManifestFileName])
	if err != nil {
		t.Fatal(err)
	}
	var manifest map[string]any
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest["browser_documents_file"] = browserDocumentsFileName
	manifest["browser_documents_parquet_file"] = browserDocumentsParquetFileName
	manifest["browser_document_record_count"] = 1
	manifest["browser_documents_sha256"] = sha256BytesHex(raw)
	manifest["browser_documents_parquet_sha256"] = strings.Repeat("a", 64)
	data, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client.artifacts[runArchiveManifestFileName], data, 0600); err != nil {
		t.Fatal(err)
	}
	client.artifacts[browserDocumentsFileName] = path
}

func TestBrowserDocumentCollectionCacheReplaysTypedSourceAfterRestart(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a", "b")
	client := run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)
	attachBrowserDocumentSource(t, client, browserDocumentV2ArchiveFixture())
	missing := run.remoteClientsByTarget["b"]
	delete(run.remoteClientsByTarget, "b")
	if record, err := backend.stageRemoteArchive(tr, run, "FAILED", "", time.Now().UTC(), true); err == nil || record != nil {
		t.Fatal("missing peer accepted")
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	root := collectionCachedDirectory(resolveArchiveRoot(), intent, "a")
	data, err := os.ReadFile(filepath.Join(root, "receipt.json"))
	if err != nil {
		t.Fatal(err)
	}
	var receipt collectionSourceReceipt
	if err := json.Unmarshal(data, &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.Files) != 4 || receipt.Files[3].Name != browserDocumentsFileName {
		t.Fatal("typed source omitted from durable cache")
	}
	row, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewTestRunner(1, nil, nil)
	restarted.testStore, restarted.archiveStore = store, store
	restored := managedTestFromRecord(row)
	restored.remoteClientsByTarget = map[string]generatorClient{"b": missing}
	record, err := backend.stageRemoteArchive(restarted, restored, "FAILED", "", time.Now().UTC(), true)
	if err != nil || record == nil {
		t.Fatal("typed replay failed", err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.BrowserDocumentRecordCount != 1 {
		t.Fatal("typed source lost or inflated", manifest.BrowserDocumentRecordCount)
	}
	summary, observations, _, err := readArchiveBrowserDocumentSummary(record, manifest)
	if err != nil || summary == nil || len(observations) != 1 || summary.Metrics[0].UnavailableFinal != 1 {
		t.Fatal("typed replay lost document population", err)
	}
	cache, err := restarted.openCollectionSourceCache(context.Background(), restored)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	source, err := cache.load(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	if err := verifyCollectionCacheForCleanup(context.Background(), source); err != nil {
		t.Fatal("four-artifact cache cannot be safely retired", err)
	}
	// Cache proof is checked again during copy, even after receipt admission.
	if err := os.WriteFile(filepath.Join(root, browserDocumentsFileName), []byte("changed"), 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if _, err := source.StreamArchiveArtifact(context.Background(), generatorrpc.ArchiveArtifactRequest{TestID: run.ID, SourceIdentity: source.receipt.Identity, Name: browserDocumentsFileName}, &output, 1<<20); err == nil {
		t.Fatal("corrupt typed cache replay succeeded")
	}
	if err := verifyCollectionCacheForCleanup(context.Background(), source); err == nil {
		t.Fatal("corrupt typed cache cleanup succeeded")
	}
}

func TestBrowserDocumentArtifactContextCancellation(t *testing.T) {
	root := t.TempDir()
	evidence := browserDocumentV2ArchiveFixture()
	data, err := json.Marshal(evidence)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(filepath.Join(root, browserDocumentsFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	err = forEachBrowserDocumentArtifact(ctx, root, browserDocumentsFileName, sha256BytesHex(data), 1, func(*protocol.BrowserDocumentEvidence) error { calls++; return nil })
	if !errors.Is(err, context.Canceled) || calls != 0 {
		t.Fatal("cancelled scan accepted evidence", err, calls)
	}
}

func TestBrowserDocumentArchiveRejectsOversizeBeforeCreatingSidecar(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	archive, err := newRunArchiveWriter(&ManagedTest{ID: "document-byte-limit"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	evidence := browserDocumentV2ArchiveFixture()
	o := evidence.Observations[0]
	o.Group = strings.Repeat("<", 256)
	o.Region = strings.Repeat(">", 256)
	o.DeviceProfile = strings.Repeat("&", 128)
	evidence.Observations = make([]protocol.BrowserDocumentObservation, protocol.MaxBrowserDocumentObservations)
	for i := range evidence.Observations {
		evidence.Observations[i] = o
		evidence.Observations[i].DocumentID = fmt.Sprintf("%s-document-%d", evidence.SourceGeneration, i)
		evidence.Observations[i].MeasurementID = evidence.Observations[i].DocumentID + "/" + o.LifecycleID + "/" + o.Metric
	}
	raw, err := json.Marshal(evidence)
	if err != nil || len(raw) <= protocol.MaxBrowserDocumentEvidenceBytes || evidence.Clone() == nil {
		t.Fatal("fixture does not exercise valid typed oversize", len(raw), err)
	}
	if err := archive.RecordBrowserDocumentEvidence(evidence); err == nil {
		t.Fatal("oversize accepted")
	}
	if archive.manifest.BrowserDocumentsFile != "" || archive.manifest.BrowserDocumentRecordCount != 0 {
		t.Fatal("rejected evidence changed archive metadata")
	}
	if _, err := os.Stat(filepath.Join(archive.rootDir, browserDocumentsFileName)); !os.IsNotExist(err) {
		t.Fatal("oversize created physical sidecar", err)
	}
	record, err := archive.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil || record == nil {
		t.Fatal("independent transaction archive was poisoned by rejected optional evidence", err)
	}
}

func TestBrowserDocumentParquetRejectsUnknownEvidenceFields(t *testing.T) {
	root := t.TempDir()
	raw, err := json.Marshal(browserDocumentV2ArchiveFixture())
	if err != nil {
		t.Fatal(err)
	}
	raw = append(append(raw[:len(raw)-1], []byte(",\"unapproved_field\":\"discarded?\"}")...), '\n')
	path := filepath.Join(root, browserDocumentsFileName)
	if err := os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	m := &RunArchiveManifest{BrowserDocumentsFile: browserDocumentsFileName, BrowserDocumentsParquetFile: browserDocumentsParquetFileName, BrowserDocumentRecordCount: 1}
	budget, err := newArchiveConversionBudget(root)
	if err != nil {
		t.Fatal(err)
	}
	defer budget.close()
	if err := writeBrowserDocumentParquet(root, m, budget); err == nil {
		t.Fatal("unknown fields silently discarded during conversion")
	}
	if err := forEachBrowserDocumentArtifact(context.Background(), root, browserDocumentsFileName, sha256BytesHex(raw), 1, func(*protocol.BrowserDocumentEvidence) error { return nil }); err == nil {
		t.Fatal("unknown fields silently discarded during transfer/report")
	}
}

type browserDocumentFanoutSink struct {
	documents []*protocol.BrowserDocumentEvidence
	actions   int
	mutate    bool
	failure   error
}

func (s *browserDocumentFanoutSink) RecordResult(*protocol.Result) error { s.actions++; return nil }
func (s *browserDocumentFanoutSink) RecordBrowserDocumentEvidence(evidence *protocol.BrowserDocumentEvidence) error {
	s.documents = append(s.documents, evidence)
	if s.mutate {
		evidence.Observations[0].UnavailableReason = "unsupported"
	}
	return s.failure
}
func TestBrowserDocumentCombinedSinkForwardsOnlyTypedOwnedEvidence(t *testing.T) {
	first := &browserDocumentFanoutSink{mutate: true}
	second := &browserDocumentFanoutSink{}
	sink := combinedResultSink{first, elkTraceEvidenceSink{}, second}
	original := browserDocumentV2ArchiveFixture()
	if err := sink.RecordBrowserDocumentEvidence(original); err != nil {
		t.Fatal(err)
	}
	if first.actions != 0 || second.actions != 0 || len(first.documents) != 1 || len(second.documents) != 1 || second.documents[0].Observations[0].UnavailableReason != "no_interaction" || original.Observations[0].UnavailableReason != "no_interaction" {
		t.Fatal("document fanout fabricated action or leaked mutable alias")
	}
	if err := (combinedResultSink{elkTraceEvidenceSink{}}).RecordBrowserDocumentEvidence(original); err == nil {
		t.Fatal("unsupported sink silently acknowledged lifecycle evidence")
	}
	failure := errors.New("typed evidence unavailable")
	second.failure = failure
	if err := sink.RecordBrowserDocumentEvidence(original); !errors.Is(err, failure) {
		t.Fatal("typed publication failure was acknowledged", err)
	}
}

func TestBrowserDocumentCaptureIntentSurvivesCompactionAndMissingSidecar(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	yes := true
	script := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{Browser: dsl.BrowserConfig{CaptureDocumentMetrics: &yes}}}
	run := &ManagedTest{ID: "document-capture-empty", Status: "COMPLETE", Groups: []TestGroup{{Name: "Browser", Protocol: "browser", script: script}}}
	compact := cloneManagedTestCapacitySnapshotLocked(run)
	if !groupsRequestBrowserDocumentCapture(compact.Groups) || compact.Groups[0].script != nil || !strings.Contains(groupRequiredRuntimeFeature(compact.Groups[0]), dsl.FeatureBrowserDocumentEvidenceV2) {
		t.Fatal("compact capacity snapshot lost capture admission")
	}
	frozen := cloneArchiveTestGroups(run.Groups)
	if !frozen[0].BrowserDocumentCaptureRequested {
		t.Fatal("frozen archive group lost explicit capture intent")
	}
	trimTerminalManagedTestRuntime(run)
	if run.Groups[0].script != nil || !groupsRequestBrowserDocumentCapture(run.Groups) {
		t.Fatal("terminal compaction erased capture intent")
	}
	raw, err := json.Marshal(run.Groups)
	if err != nil {
		t.Fatal(err)
	}
	var restored []TestGroup
	if err := json.Unmarshal(raw, &restored); err != nil {
		t.Fatal(err)
	}
	if !groupsRequestBrowserDocumentCapture(restored) {
		t.Fatal("durable group snapshot erased capture intent")
	}
	writer, err := newRunArchiveWriter(run, RunArchiveProvenance{Groups: frozen})
	if err != nil {
		t.Fatal(err)
	}
	record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatal(err)
	}
	if !report.Manifest.BrowserDocumentCaptureRequested || report.BrowserDocuments == nil || !report.BrowserDocuments.Incomplete || len(report.BrowserDocuments.Metrics) != 0 || report.Overview.Transactions != 0 {
		t.Fatal("missing requested evidence became zero/absent/complete")
	}
}

func TestBrowserDocumentCoverageUsesFrozenExpectedSources(t *testing.T) {
	source := RunArchiveManifest{BrowserDocumentsFile: browserDocumentsFileName}
	for _, tc := range []struct {
		name       string
		bundles    []remoteArchiveBundle
		coverage   *RunArchiveSourceCoverage
		incomplete bool
	}{
		{name: "all requested", bundles: []remoteArchiveBundle{{target: "a", manifest: source}, {target: "b", manifest: source}, {target: "http"}}},
		{name: "missing requested", bundles: []remoteArchiveBundle{{target: "a", manifest: source}, {target: "b"}, {target: "http"}}, incomplete: true},
		{name: "lost requested", bundles: []remoteArchiveBundle{{target: "a", manifest: source}, {target: "http"}}, incomplete: true},
		{name: "contradictory coverage", bundles: []remoteArchiveBundle{{target: "a", manifest: source}, {target: "b", manifest: source}}, coverage: &RunArchiveSourceCoverage{Sources: []RunArchiveSource{{Target: "b", Collected: false}}}, incomplete: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := &RunArchiveManifest{BrowserDocumentCaptureRequested: true, SourceCoverage: tc.coverage, Provenance: RunArchiveProvenance{Groups: []TestGroup{{Name: "browser", BrowserDocumentCaptureRequested: true}, {Name: "http"}}, GeneratorShards: []GeneratorShardSummary{{Group: "browser", Target: "a"}, {Group: "browser", Target: "b"}, {Group: "http", Target: "http"}}}}
			qualifyBrowserDocumentSourceCoverage(m, tc.bundles)
			if m.BrowserDocumentCoverageIncomplete != tc.incomplete {
				t.Fatal("browser expected source coverage", m.BrowserDocumentCoverageIncomplete)
			}
		})
	}
	unknown := &RunArchiveManifest{BrowserDocumentCaptureRequested: true}
	qualifyBrowserDocumentSourceCoverage(unknown, []remoteArchiveBundle{{target: "a", manifest: source}})
	if !unknown.BrowserDocumentCoverageIncomplete {
		t.Fatal("unknown frozen source scope reported complete")
	}
}

func TestBrowserDocumentsArchiveIntervalsQualifyMixedPeersAndReport(t *testing.T) {
	for _, legacyBrowser := range []bool{true, false} {
		t.Run(fmt.Sprintf("legacy-browser-%t", legacyBrowser), func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			writer, err := newRunArchiveWriter(&ManagedTest{ID: "browser-interval-mixed"}, RunArchiveProvenance{})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 5, 0, 0, 0, 0, time.UTC)
			records := []IntervalSummaryRecord{{Timestamp: now, TestID: "browser-interval-mixed", BrowserDocuments: unavailableDocumentMetricFixture(1)}, {Timestamp: now, TestID: "browser-interval-mixed"}}
			if legacyBrowser {
				records[1].Browser = &BrowserLiveMetrics{Samples: 100}
			}
			var bundles []remoteArchiveBundle
			for i, row := range records {
				dir := t.TempDir()
				summaries := filepath.Join(dir, "interval_summaries.jsonl")
				file, err := os.Create(summaries)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.NewEncoder(file).Encode(row); err != nil {
					t.Fatal(err)
				}
				if err := file.Close(); err != nil {
					t.Fatal(err)
				}
				tx := filepath.Join(dir, "transactions.jsonl")
				if err := os.WriteFile(tx, nil, 0600); err != nil {
					t.Fatal(err)
				}
				bundles = append(bundles, remoteArchiveBundle{target: fmt.Sprintf("source-%d", i), summariesPath: summaries, transactionsPath: tx, manifest: RunArchiveManifest{IntervalSummaryCount: 1}})
			}
			destination := filepath.Join(t.TempDir(), "summaries.jsonl")
			count, err := mergeRemoteArchiveIntervalSummariesWithCoverage(destination, bundles, nil, nil)
			if err != nil || count != 1 {
				t.Fatal("merge intervals", count, err)
			}
			file, err := os.Open(destination)
			if err != nil {
				t.Fatal(err)
			}
			var merged IntervalSummaryRecord
			if err := json.NewDecoder(file).Decode(&merged); err != nil {
				t.Fatal(err)
			}
			file.Close()
			if merged.BrowserDocuments == nil || merged.BrowserDocuments.Incomplete != legacyBrowser || len(merged.BrowserDocuments.Metrics) != 1 || merged.BrowserDocuments.Metrics[0].UnavailableFinal != 1 || merged.BrowserDocuments.Metrics[0].EligibleFinal != 0 {
				t.Fatal("archive merge fabricated complete coverage/document population")
			}
			if err := writer.RecordIntervalSummary(&LiveMetrics{Timestamp: merged.Timestamp, BrowserDocuments: merged.BrowserDocuments}); err != nil {
				t.Fatal(err)
			}
			record, err := writer.Finalize("COMPLETE", now.Add(time.Second), "", false, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			report, err := buildRunArchiveReport(record)
			if err != nil {
				t.Fatal(err)
			}
			if len(report.SummaryTimeline) != 1 || report.SummaryTimeline[0].BrowserDocuments == nil || report.SummaryTimeline[0].BrowserDocuments.Incomplete != legacyBrowser || report.Overview.Transactions != 0 {
				t.Fatal("archived report lost mixed-peer coverage or inflated actions")
			}
		})
	}
}

func TestBrowserDocumentArchiveLossUnitsAndImmutableSource(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		observations, requests int64
		incomplete             bool
	}{
		{"request_only", 0, 5, false}, {"request_only_flagged", 0, 5, true},
		{"observation_only", 3, 0, false}, {"distinct_losses", 3, 5, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
			archive, err := newRunArchiveWriter(&ManagedTest{ID: "loss-units", Name: "Loss units"}, RunArchiveProvenance{})
			if err != nil {
				t.Fatal(err)
			}
			evidence := browserDocumentV2ArchiveFixture()
			evidence.Observations = nil
			evidence.IssuedRequests, evidence.DroppedRequestIdentities = 7, tc.requests
			evidence.DroppedObservations, evidence.Incomplete = tc.observations, tc.incomplete
			for i := 0; i < 2; i++ {
				if err := archive.RecordBrowserDocumentEvidence(evidence); err != nil {
					t.Fatal(err)
				}
			}
			record, err := archive.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
			if err != nil {
				t.Fatal(err)
			}
			manifest, err := loadRunArchiveManifestFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			report, err := buildRunArchiveReport(record)
			if err != nil {
				t.Fatal(err)
			}
			if summary := report.BrowserDocuments; summary == nil || !summary.Incomplete || summary.DroppedObservations != tc.observations || len(summary.Metrics) != 0 || report.Overview.Transactions != 0 {
				t.Fatalf("archive confused loss units: %+v", summary)
			}
			seen := 0
			if err := forEachBrowserDocumentArtifact(context.Background(), record.ArchivePath, manifest.BrowserDocumentsFile, manifest.BrowserDocumentsSHA256, 2, func(raw *protocol.BrowserDocumentEvidence) error {
				seen++
				if raw.IssuedRequests != 7 || raw.DroppedRequestIdentities != tc.requests || raw.DroppedObservations != tc.observations || raw.Incomplete != tc.incomplete {
					t.Fatal("original typed loss evidence was rewritten")
				}
				return nil
			}); err != nil || seen != 2 {
				t.Fatalf("sidecar proof failed: %v, %d", err, seen)
			}
		})
	}
}

func TestBrowserDocumentArchiveObservationLossDeltasIgnoreRequestLoss(t *testing.T) {
	var accumulator archiveBrowserDocumentAccumulator
	evidence := browserDocumentV2ArchiveFixture()
	evidence.Observations = nil
	evidence.IssuedRequests, evidence.DroppedRequestIdentities = 10, 9
	for _, step := range []struct {
		generation    string
		dropped, want int64
	}{
		{strings.Repeat("a", 32), 3, 3}, {strings.Repeat("a", 32), 3, 3},
		{strings.Repeat("a", 32), 5, 5}, {strings.Repeat("b", 32), 1, 6},
	} {
		evidence.SourceGeneration, evidence.DroppedObservations = step.generation, step.dropped
		accumulator.add(evidence)
		summary := accumulator.summary()
		if summary == nil || !summary.Incomplete || summary.DroppedObservations != step.want {
			t.Fatalf("source delta=%+v, want %d", summary, step.want)
		}
	}
}

func TestBrowserDocumentArchiveMeasurementCapacityRetriesDoNotInventLoss(t *testing.T) {
	evidence := browserDocumentV2ArchiveFixture()
	var accumulator archiveBrowserDocumentAccumulator
	for i := 0; i < maxArchiveBrowserDocumentMeasurements; i++ {
		copy := evidence.Clone()
		observation := &copy.Observations[0]
		observation.DocumentID = fmt.Sprintf("%s-d%d", observation.SourceGeneration, i+2)
		observation.LifecycleID = fmt.Sprintf("%s-l%d", observation.SourceGeneration, i+2)
		observation.MeasurementID = observation.DocumentID + "/" + observation.LifecycleID + "/" + observation.Metric
		if !observation.Valid() {
			t.Fatal("invalid capacity fixture")
		}
		accumulator.add(copy)
	}
	for i := 0; i < 3; i++ {
		accumulator.add(evidence)
		summary := accumulator.summary()
		if summary == nil || !summary.Incomplete || summary.DroppedObservations != 0 || len(summary.Metrics) != 1 || summary.Metrics[0].UnavailableFinal != maxArchiveBrowserDocumentMeasurements {
			t.Fatalf("unknown rejected retry inflated loss/population: %+v", summary)
		}
	}
}
