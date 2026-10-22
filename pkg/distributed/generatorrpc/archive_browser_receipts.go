package generatorrpc

import (
	"encoding/json"
	"fmt"
	"github.com/jasonczech/apexload/pkg/protocol"
	"io"
	"os"
	"path/filepath"
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
	if err := w.browserReceiptsFile.Sync(); err != nil {
		w.recordErr = err
		return err
	}
	if w.manifest.BrowserReceiptRecordCount == 0 {
		if err := syncGeneratorArchiveDirectory(w.runRoot); err != nil {
			w.recordErr = err
			return err
		}
		parent, err := os.OpenRoot(filepath.Dir(w.rootDir))
		if err == nil {
			err = syncGeneratorArchiveDirectory(parent)
			closeErr := parent.Close()
			if err == nil {
				err = closeErr
			}
		}
		if err != nil {
			w.recordErr = err
			return err
		}
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

func (w *archiveRecorder) BrowserDocumentSourceJournal() (*protocol.BrowserSourceJournal, error) {
	if w == nil {
		return nil, fmt.Errorf("browser source archive unavailable")
	}
	w.browserProofMu.Lock()
	defer w.browserProofMu.Unlock()
	if w.browserProofSealed {
		return nil, fmt.Errorf("browser source archive sealed")
	}
	if w.browserProofJournal == nil {
		w.mu.Lock()
		runID, owner := w.manifest.TestID, w.manifest.SourceIdentity
		w.mu.Unlock()
		if owner == "" {
			owner = "generator"
		}
		journal, err := protocol.NewBrowserSourceJournal(runID, owner, w.RecordBrowserDocumentReceiptEvidence)
		if err != nil {
			return nil, err
		}
		w.browserProofJournal = journal
	}
	return w.browserProofJournal, nil
}
func (w *archiveRecorder) sealBrowserSourceJournal(success bool) error {
	w.browserProofMu.Lock()
	defer w.browserProofMu.Unlock()
	w.browserProofSealed = true
	return w.browserProofJournal.Seal(success)
}
