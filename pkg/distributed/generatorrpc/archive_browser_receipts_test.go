package generatorrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func receiptTransportFixture() *protocol.BrowserDocumentReceiptEvidence {
	gen := strings.Repeat("a", 32)
	at := time.Date(2026, 10, 22, 0, 0, 0, 0, time.UTC)
	return &protocol.BrowserDocumentReceiptEvidence{Version: 1, SourceGeneration: gen, StartedAt: at, Receipts: []protocol.BrowserDocumentReceipt{{DocumentID: gen + "-d1", LifecycleID: gen + "-l1", FrameID: gen + "-f1", Kind: "begin", BrowserTimeOriginMS: 1792627200000, ReceivedAt: at}}}
}

func TestBrowserReceiptGeneratorArchive(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv(envGeneratorArchiveMinFree, "1")
	w, err := newArchiveRecorder("receipt-generator", "receipt-run", "Receipt")
	if err != nil {
		t.Fatal(err)
	}
	defer w.Discard()
	if err = w.RecordBrowserDocumentReceiptEvidence(receiptTransportFixture()); err != nil {
		t.Fatal(err)
	}
	w.MarkBrowserDocumentReceiptIncomplete()
	record, err := w.Finalize("COMPLETE", time.Now().UTC(), "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(record.ArchivePath, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var m archiveManifest
	if err = json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !m.BrowserReceiptIncomplete || m.TransactionCount != 0 || m.BrowserDocumentRecordCount != 0 || m.BrowserReceiptRecordCount != 1 {
		t.Fatal("receipt changed action population")
	}
	raw, err = os.ReadFile(filepath.Join(record.ArchivePath, m.BrowserReceiptsFile))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if m.BrowserReceiptsSHA256 != hex.EncodeToString(sum[:]) {
		t.Fatal("receipt digest mismatch")
	}
	found := false
	for _, name := range archiveArtifactNames(&m) {
		found = found || name == m.BrowserReceiptsFile
	}
	if !found {
		t.Fatal("receipt not transferable")
	}
	if err = w.RecordBrowserDocumentReceiptEvidence(receiptTransportFixture()); err == nil {
		t.Fatal("closed generator archive accepted receipt")
	}
}
