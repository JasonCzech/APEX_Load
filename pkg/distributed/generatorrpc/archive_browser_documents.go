package generatorrpc

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	"github.com/jasonczech/apexload/pkg/protocol"
)

type parquetBrowserDocumentRecord struct {
	SourceGeneration string `parquet:"name=source_generation, type=BYTE_ARRAY, convertedtype=UTF8"`
	EvidenceVersion  int32  `parquet:"name=evidence_version, type=INT32"`
	EvidenceJSON     string `parquet:"name=evidence_json, type=BYTE_ARRAY, convertedtype=UTF8"`
}

func (w *archiveRecorder) RecordBrowserDocumentEvidence(evidence *protocol.BrowserDocumentEvidence) error {
	if w == nil {
		return fmt.Errorf("generator archive recorder is unavailable")
	}
	evidence = evidence.Clone()
	if evidence == nil {
		return fmt.Errorf("browser document evidence is malformed")
	}
	data, err := json.Marshal(evidence)
	if err != nil || len(data) > protocol.MaxBrowserDocumentEvidenceBytes {
		return fmt.Errorf("browser document evidence exceeds its record bounds")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.runRoot == nil {
		return fmt.Errorf("generator archive recorder is closed")
	}
	if w.recordErr != nil {
		return w.recordErr
	}
	if w.browserDocumentsEncoder == nil {
		file, err := replaceGeneratorArchiveFile(w.runRoot, "browser_documents.jsonl", 0o644)
		if err != nil {
			w.recordErr = fmt.Errorf("open browser document archive: %w", err)
			return w.recordErr
		}
		w.browserDocumentsFile = file
		w.browserDocumentsEncoder = json.NewEncoder(&generatorArchiveQuotaWriter{file: file, budget: w.byteBudget})
		w.manifest.BrowserDocumentsFile = "browser_documents.jsonl"
		w.manifest.BrowserDocumentsParquetFile = "browser_documents.parquet"
	}
	record := append(data, '\n')
	written, writeError := (&generatorArchiveQuotaWriter{file: w.browserDocumentsFile, budget: w.byteBudget}).Write(record)
	if writeError == nil && written != len(record) {
		writeError = io.ErrShortWrite
	}
	if err := writeError; err != nil {
		w.recordErr = fmt.Errorf("record browser document evidence: %w", err)
		return w.recordErr
	}
	w.manifest.BrowserDocumentRecordCount++
	return nil
}
func archiveBrowserDocumentSHA256(root *os.Root, name string) (digest string, err error) {
	file, err := root.Open(name)
	if err != nil {
		return "", err
	}
	defer func() { err = joinArchiveCloseError(err, file.Close()) }()
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
func joinArchiveCloseError(prior, closeError error) error {
	if prior != nil {
		return prior
	}
	return closeError
}
func decodeBrowserDocumentRecord(raw []byte, evidence *protocol.BrowserDocumentEvidence) error {
	owned, err := protocol.DecodeBrowserDocumentEvidence(raw)
	if err != nil {
		return err
	}
	*evidence = *owned
	return nil
}
func browserDocumentRecordParquet(evidence protocol.BrowserDocumentEvidence) parquetBrowserDocumentRecord {
	// The private streaming converter validates this typed record before calling
	// conversion; invalid input must fail publication, never become a zero row.
	data, _ := json.Marshal(evidence)
	return parquetBrowserDocumentRecord{SourceGeneration: evidence.SourceGeneration, EvidenceVersion: int32(evidence.Version), EvidenceJSON: string(data)}
}

func (w *archiveRecorder) bindBrowserDocumentCapture(requested bool) {
	if w == nil || !requested {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed {
		w.manifest.BrowserDocumentCaptureRequested = true
	}
}

func (w *archiveRecorder) bindBrowserDocumentProofVersion(version int) {
	if w == nil || version <= 0 {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if !w.closed && version > w.browserDocumentProofVersion {
		w.browserDocumentProofVersion = version
	}
}

func (w *archiveRecorder) browserDocumentProofEnabled() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.manifest.BrowserDocumentCaptureRequested && w.browserDocumentProofVersion >= 3
}
