package generatorrpc

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"io"
)

func (w *archiveRecorder) RecordBrowserDocumentReceiptEvidence(evidence *protocol.BrowserDocumentReceiptEvidence) error {
	if w == nil {
		return fmt.Errorf("generator receipt archive unavailable")
	}
	evidence = evidence.Clone()
	if evidence == nil {
		return fmt.Errorf("browser receipt evidence malformed")
	}
	data, err := json.Marshal(evidence)
	if err != nil || len(data) > protocol.MaxBrowserDocumentReceiptBytes {
		return fmt.Errorf("browser receipt evidence exceeds record bounds")
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.runRoot == nil {
		return fmt.Errorf("generator receipt archive closed")
	}
	if w.recordErr != nil {
		return w.recordErr
	}
	if w.manifest.BrowserReceiptRecordCount >= protocol.MaxBrowserDocumentReceiptRecords {
		w.recordErr = fmt.Errorf("browser receipt record limit exceeded")
		return w.recordErr
	}
	if w.browserReceiptsFile == nil {
		file, err := replaceGeneratorArchiveFile(w.runRoot, "browser_receipts.jsonl", 0o644)
		if err != nil {
			w.recordErr = err
			return err
		}
		w.browserReceiptsFile = file
		w.manifest.BrowserReceiptsFile = "browser_receipts.jsonl"
	}
	record := append(data, '\n')
	written, err := (&generatorArchiveQuotaWriter{file: w.browserReceiptsFile, budget: w.byteBudget}).Write(record)
	if err == nil && written != len(record) {
		err = io.ErrShortWrite
	}
	if err != nil {
		w.recordErr = err
		return err
	}
	w.manifest.BrowserReceiptRecordCount++
	return nil
}

func (w *archiveRecorder) MarkBrowserDocumentReceiptIncomplete() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.BrowserReceiptIncomplete = true
}
