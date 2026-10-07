package generatorrpc

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// Browser interaction evidence in a generator run archive (E3). Records go
// to their own browser_interactions.jsonl sidecar under their own byte
// budget; at Finalize the sidecar is declared as a transferable evidence
// artifact, which the controller fetches, verifies and acknowledges like a
// HAR. A record over the budget is counted as dropped, never an error that
// fails the run.
const (
	archiveInteractionsName        = "browser_interactions.jsonl"
	archiveInteractionsKind        = "interaction_evidence"
	archiveInteractionsSource      = "interactions"
	maxArchiveInteractionsRunBytes = int64(64 << 20)
)

// RecordBrowserInteractionEvidence appends one evidence record.
func (w *archiveRecorder) RecordBrowserInteractionEvidence(evidence *protocol.BrowserInteractionEvidence) error {
	if w == nil {
		return fmt.Errorf("generator archive recorder is unavailable")
	}
	evidence = evidence.Clone()
	if evidence == nil {
		return fmt.Errorf("browser interaction evidence is malformed")
	}
	data, err := json.Marshal(evidence)
	if err != nil || len(data) > protocol.MaxBrowserInteractionEvidenceBytes {
		return fmt.Errorf("browser interaction evidence exceeds its record bounds")
	}
	record := append(data, '\n')
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.runRoot == nil {
		return fmt.Errorf("generator archive recorder is closed")
	}
	drop := func() error {
		w.manifest.BrowserInteractionsIncomplete = true
		w.manifest.BrowserInteractionsDropped += int64(len(evidence.Interactions))
		return fmt.Errorf("browser interaction evidence exceeds the run's sidecar budget")
	}
	if int64(len(record)) > maxArchiveInteractionsRunBytes-w.interactionBytes || !w.byteBudget.admitOptional(int64(len(record))) {
		return drop()
	}
	if w.interactionsFile == nil {
		file, err := replaceGeneratorArchiveFile(w.runRoot, archiveInteractionsName, 0o644)
		if err != nil {
			return drop()
		}
		w.interactionsFile = file
	}
	written, err := (&generatorArchiveQuotaWriter{file: w.interactionsFile, budget: w.byteBudget}).Write(record)
	if err == nil && written != len(record) {
		err = io.ErrShortWrite
	}
	if err != nil {
		// A partial line would corrupt the sidecar: stop recording into it.
		w.manifest.BrowserInteractionsIncomplete = true
		w.interactionsBroken = true
		return drop()
	}
	w.interactionBytes += int64(len(record))
	w.manifest.BrowserInteractionRecordCount++
	w.manifest.BrowserInteractionsDropped += evidence.Dropped
	w.manifest.BrowserInteractionsIncomplete = w.manifest.BrowserInteractionsIncomplete || evidence.Incomplete
	return nil
}

// finalizeBrowserInteractionsLocked closes the sidecar and declares it; w.mu
// is held.
func (w *archiveRecorder) finalizeBrowserInteractionsLocked() error {
	if w.interactionsFile == nil {
		return nil
	}
	file := w.interactionsFile
	w.interactionsFile = nil
	if err := joinArchiveCloseError(file.Sync(), file.Close()); err != nil {
		return err
	}
	if w.interactionsBroken {
		// A torn sidecar is not transferred.
		w.manifest.BrowserInteractionRecordCount = 0
		return w.runRoot.Remove(archiveInteractionsName)
	}
	info, err := w.runRoot.Lstat(archiveInteractionsName)
	if err != nil {
		return err
	}
	sum, err := archiveBrowserDocumentSHA256(w.runRoot, archiveInteractionsName)
	if err != nil {
		return err
	}
	w.manifest.BrowserInteractionsArtifact = &archiveEvidenceArtifact{Name: archiveInteractionsName, Kind: archiveInteractionsKind, Fidelity: "native", SourceGeneration: archiveInteractionsSource, Sequence: 1, SHA256: sum, Bytes: info.Size()}
	return nil
}
