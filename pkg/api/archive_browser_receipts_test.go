package api

import (
	"context"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func receiptTransportFixture() *protocol.BrowserDocumentReceiptEvidence {
	gen := strings.Repeat("a", 32)
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	return &protocol.BrowserDocumentReceiptEvidence{Version: 1, SourceGeneration: gen, StartedAt: at, Receipts: []protocol.BrowserDocumentReceipt{{DocumentID: gen + "-d1", LifecycleID: gen + "-l1", FrameID: gen + "-f1", Kind: "begin", BrowserTimeOriginMS: 1792627200000, ReceivedAt: at}}}
}

func TestBrowserReceiptArchiveRoundTripAndIntegrity(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	w, err := newRunArchiveWriter(&ManagedTest{ID: "receipt-sidecar"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	original := receiptTransportFixture()
	if err = w.RecordBrowserDocumentReceiptEvidence(original); err != nil {
		t.Fatal(err)
	}
	record, err := w.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if m.TransactionCount != 0 || m.BrowserDocumentRecordCount != 0 || m.BrowserReceiptRecordCount != 1 || !validArchiveSHA256(m.BrowserReceiptsSHA256) {
		t.Fatal("receipt became measurement or lost integrity", m)
	}
	var restored *protocol.BrowserDocumentReceiptEvidence
	if err = forEachBrowserReceiptArtifact(context.Background(), record.ArchivePath, m.BrowserReceiptsFile, m.BrowserReceiptsSHA256, 1, func(e *protocol.BrowserDocumentReceiptEvidence) error { restored = e; return nil }); err != nil || !reflect.DeepEqual(restored, original) {
		t.Fatal("roundtrip", err)
	}
	before, err := rcaEvidenceDigest(m)
	if err != nil {
		t.Fatal(err)
	}
	m.BrowserReceiptsSHA256 = strings.Repeat("b", 64)
	after, err := rcaEvidenceDigest(m)
	if err != nil || before == after {
		t.Fatal("receipt proof omitted from analysis freshness")
	}
	if err = w.RecordBrowserDocumentReceiptEvidence(original); err == nil {
		t.Fatal("closed writer accepted receipt")
	}
	if err = os.WriteFile(filepath.Join(record.ArchivePath, m.BrowserReceiptsFile), []byte("corrupt\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = forEachBrowserReceiptArtifact(context.Background(), record.ArchivePath, m.BrowserReceiptsFile, m.BrowserReceiptsSHA256, 1, func(*protocol.BrowserDocumentReceiptEvidence) error { return nil }); err == nil {
		t.Fatal("corrupt receipt admitted")
	}
}
func attachBrowserReceiptSource(t *testing.T, client *archiveStreamingFakeClient) {
	t.Helper()
	path := filepath.Join(filepath.Dir(client.artifacts[runArchiveManifestFileName]), browserReceiptsFileName)
	raw, err := json.Marshal(receiptTransportFixture())
	if err != nil {
		t.Fatal(err)
	}
	raw = append(raw, '\n')
	if err = os.WriteFile(path, raw, 0600); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(client.artifacts[runArchiveManifestFileName])
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["browser_receipt_incomplete"] = true
	m["browser_receipts_file"] = browserReceiptsFileName
	m["browser_receipt_record_count"] = 1
	m["browser_receipts_sha256"] = sha256BytesHex(raw)
	data, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(client.artifacts[runArchiveManifestFileName], data, 0600); err != nil {
		t.Fatal(err)
	}
	client.artifacts[browserReceiptsFileName] = path
}
func TestBrowserReceiptCollectionCacheRestoresWithoutOriginalPeer(t *testing.T) {
	for _, documents := range []bool{false, true} {
		t.Run(map[bool]string{false: "receipt only", true: "with documents"}[documents], func(t *testing.T) {
			tr, store, run, backend := collectionCacheFixture(t, "a", "b")
			client := run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)
			if documents {
				attachBrowserDocumentSource(t, client, browserDocumentV2ArchiveFixture())
			}
			attachBrowserReceiptSource(t, client)
			missing := run.remoteClientsByTarget["b"]
			delete(run.remoteClientsByTarget, "b")
			if record, err := backend.stageRemoteArchive(tr, run, "FAILED", "", time.Now().UTC(), true); err == nil || record != nil {
				t.Fatal("missing peer accepted")
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
				t.Fatal("receipt recovery", err)
			}
			m, err := loadRunArchiveManifestFromRecord(record)
			if err != nil {
				t.Fatal(err)
			}
			if !m.BrowserReceiptIncomplete || m.BrowserReceiptRecordCount != 1 {
				t.Fatal("receipt source lost or inflated", m.BrowserReceiptRecordCount)
			}
			if err = forEachBrowserReceiptArtifact(context.Background(), record.ArchivePath, m.BrowserReceiptsFile, m.BrowserReceiptsSHA256, 1, func(e *protocol.BrowserDocumentReceiptEvidence) error {
				if !reflect.DeepEqual(e, receiptTransportFixture()) {
					t.Fatal("receipt changed across recovery")
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
		})
	}
}
func TestBrowserReceiptManifestRefusesInvalidDeclarations(t *testing.T) {
	for _, m := range []RunArchiveManifest{{BrowserReceiptRecordCount: 1}, {BrowserReceiptsSHA256: strings.Repeat("a", 64)}, {BrowserReceiptsFile: "../browser_receipts.jsonl", BrowserReceiptRecordCount: 1}, {BrowserReceiptsFile: browserReceiptsFileName, BrowserReceiptRecordCount: 0}, {BrowserReceiptsFile: browserReceiptsFileName, BrowserReceiptRecordCount: 1}} {
		if validateBrowserReceiptManifest(&m, true) == nil {
			t.Fatal("invalid receipt declaration admitted", m)
		}
	}
}

func TestBrowserReceiptRemoteTransferRejectsMissingOrCorrupt(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "corrupt", true: "missing"}[missing], func(t *testing.T) {
			tr, _, run, backend := collectionCacheFixture(t, "a")
			client := run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)
			attachBrowserReceiptSource(t, client)
			if missing {
				delete(client.artifacts, browserReceiptsFileName)
			} else if err := os.WriteFile(client.artifacts[browserReceiptsFileName], []byte("{}\n"), 0600); err != nil {
				t.Fatal(err)
			}
			if record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "", time.Now().UTC(), true); err == nil || record != nil {
				t.Fatal("invalid receipt transfer accepted")
			}
		})
	}
}
func TestBrowserReceiptLossPersistsWithoutArtifact(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	w, err := newRunArchiveWriter(&ManagedTest{ID: "receipt-loss"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	combinedResultSink{w}.MarkBrowserDocumentReceiptIncomplete()
	record, err := w.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if !m.BrowserReceiptIncomplete || m.BrowserReceiptsFile != "" || m.BrowserReceiptRecordCount != 0 {
		t.Fatal("receipt loss became missing or fabricated data")
	}
	before, err := rcaEvidenceDigest(m)
	if err != nil {
		t.Fatal(err)
	}
	m.BrowserReceiptIncomplete = false
	after, err := rcaEvidenceDigest(m)
	if err != nil || before == after {
		t.Fatal("receipt loss omitted from digest")
	}
	if err := validateBrowserReceiptManifest(m, true); err != nil {
		t.Fatal(err)
	}
}

func TestBrowserReceiptLossWithoutPayloadSurvivesCacheRestart(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a", "b")
	client := run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)
	name := client.artifacts[runArchiveManifestFileName]
	data, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["browser_receipt_incomplete"] = true
	data, err = json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(name, data, 0600); err != nil {
		t.Fatal(err)
	}
	missing := run.remoteClientsByTarget["b"]
	delete(run.remoteClientsByTarget, "b")
	if record, err := backend.stageRemoteArchive(tr, run, "FAILED", "", time.Now().UTC(), true); err == nil || record != nil {
		t.Fatal("missing peer accepted")
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
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	if !manifest.BrowserReceiptIncomplete || manifest.BrowserReceiptsFile != "" {
		t.Fatal("lost empty source obligation")
	}
}
