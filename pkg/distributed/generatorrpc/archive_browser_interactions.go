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
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.runRoot == nil {
		return fmt.Errorf("generator archive recorder is closed")
	}
	drop := func() error {
		w.manifest.BrowserInteractionsIncomplete = true
		w.manifest.BrowserInteractionsDropped += int64(len(evidence.Interactions)) + max(evidence.Dropped, 0)
		return fmt.Errorf("browser interaction evidence was not retained in the run's sidecar")
	}
	if err != nil || len(data) > protocol.MaxBrowserInteractionEvidenceBytes {
		return drop()
	}
	record := append(data, '\n')
	if w.interactionsBroken || int64(len(record)) > maxArchiveInteractionsRunBytes-w.interactionBytes || !w.byteBudget.admitOptional(int64(len(record))) {
		return drop()
	}
	if w.interactionsFile == nil {
		file, err := replaceGeneratorArchiveFile(w.runRoot, archiveInteractionsName, 0o644)
		if err != nil {
			w.byteBudget.releaseOptional(int64(len(record)))
			return drop()
		}
		w.interactionsFile = file
	}
	written, err := (&generatorArchiveQuotaWriter{file: w.interactionsFile, budget: w.byteBudget}).Write(record)
	if err == nil && written != len(record) {
		err = io.ErrShortWrite
	}
	switch {
	case err != nil && written == 0:
		// Refused before any byte was written (free space, backlog): only
		// this record is lost.
		w.byteBudget.releaseOptional(int64(len(record)))
		return drop()
	case err != nil:
		// A partial line corrupts the sidecar, which is then not
		// transferred: every record in it is lost, and nothing more is
		// written into it.
		w.manifest.BrowserInteractionsIncomplete = true
		w.manifest.BrowserInteractionsDropped += w.interactionCount
		w.interactionsBroken = true
		return drop()
	}
	w.interactionCount += int64(len(evidence.Interactions))
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
