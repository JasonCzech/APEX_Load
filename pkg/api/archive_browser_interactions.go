package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Browser interaction evidence in run archives (E3). Each source's
// browser_interactions.jsonl travels like a HAR: fetched from the live
// generator, verified by size and SHA-256 and by strict decoding of every
// record, receipted, merged into the run archive's own sidecar and
// acknowledged after publication. A controller running the test itself
// writes the sidecar directly.
const (
	browserInteractionsFileName      = "browser_interactions.jsonl"
	browserInteractionsRecordType    = "browser_interactions_v1"
	maxRunArchiveInteractionsBytes   = int64(64 << 20)
	maxRunArchiveInteractionsRecords = int64(1 << 20)
	interactionsEvidenceSource       = "interactions"
	interactionsUnavailableSource    = "source_unavailable"
	interactionsUnavailableTransfer  = "transfer_failed"
	interactionsUnavailableReceipt   = "receipt_refused"
	interactionsUnavailableInvalid   = "invalid_artifact"
)

func validDeclaredInteractions(a *RunArchiveEvidenceArtifact) bool {
	return a != nil && a.Name == browserInteractionsFileName && a.Kind == storage.EvidenceKindInteraction && a.Fidelity == "native" && a.SourceGeneration == interactionsEvidenceSource && a.Sequence == 1 && a.Bytes > 0 && a.Bytes <= maxRunArchiveInteractionsBytes && validDeclaredSHA256(a.SHA256)
}

// fetchRemoteInteractions fetches and verifies a source's declared
// interactions sidecar from its live generator client.
func fetchRemoteInteractions(ctx context.Context, client archiveArtifactClient, testID, sourceIdentity, dir string, declared *RunArchiveEvidenceArtifact, records int64, transfer *remoteArchiveTransferBudget) (*stagedRemoteHAR, string) {
	if declared == nil {
		return nil, ""
	}
	if !validDeclaredInteractions(declared) || records <= 0 || records > maxRunArchiveInteractionsRecords {
		return nil, interactionsUnavailableInvalid
	}
	if client == nil {
		return nil, interactionsUnavailableSource
	}
	path := filepath.Join(dir, declared.Name)
	// The run-wide allowance is reserved for the whole declared sidecar
	// before it streams and returned if it is not kept.
	artifact, err := transfer.reserveOptional(declared.Bytes)
	if err != nil {
		return nil, interactionsUnavailableTransfer
	}
	copied, err := fetchRemoteArchiveArtifactFileForSource(ctx, client, testID, declared.Name, sourceIdentity, path, declared.Bytes, artifact)
	if err == nil && copied != declared.Bytes {
		err = io.ErrUnexpectedEOF
	}
	if err == nil {
		err = verifyHARFile(path, *declared)
	}
	if err == nil {
		var count int64
		count, err = forEachBrowserInteractionRecord(path, func(*protocol.BrowserInteractionEvidence) error { return nil })
		if err == nil && count != records {
			err = errors.New("interaction record count differs from its declaration")
		}
	}
	if err != nil {
		_ = os.Remove(path)
		transfer.release(declared.Bytes)
		return nil, interactionsUnavailableTransfer
	}
	entry := *declared
	entry.SourceIdentity = sourceIdentity
	return &stagedRemoteHAR{entry: entry, path: path}, ""
}

// forEachBrowserInteractionRecord strictly decodes every record of a
// sidecar and returns how many it read.
func forEachBrowserInteractionRecord(path string, visit func(*protocol.BrowserInteractionEvidence) error) (int64, error) {
	file, _, err := openArchiveArtifactForRead(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return 0, err
	}
	defer file.Close()
	scanner := bufio.NewScanner(io.LimitReader(file, maxRunArchiveInteractionsBytes+1))
	scanner.Buffer(make([]byte, 0, 64<<10), protocol.MaxBrowserInteractionEvidenceBytes+1)
	var count int64
	for scanner.Scan() {
		evidence, err := protocol.DecodeBrowserInteractionEvidence(scanner.Bytes())
		if err != nil {
			return count, err
		}
		if err := visit(evidence); err != nil {
			return count, err
		}
		count++
		if count > maxRunArchiveInteractionsRecords {
			return count, errors.New("interaction sidecar exceeds its record limit")
		}
	}
	return count, scanner.Err()
}

// mergeRemoteInteractions appends each receipted source's records to the
// run archive's sidecar and describes it in the manifest.
func mergeRemoteInteractions(workingDir string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle, accept harReceiptAcceptor) (err error) {
	var file *os.File
	var writer *bufio.Writer
	var records int64
	defer func() {
		if file != nil {
			err = errors.Join(err, file.Close())
		}
	}()
	for _, bundle := range bundles {
		manifest.BrowserInteractionsIncomplete = manifest.BrowserInteractionsIncomplete || bundle.manifest.BrowserInteractionsIncomplete
		if dropped := bundle.manifest.BrowserInteractionsDropped; dropped > 0 && dropped <= 1e12 {
			manifest.BrowserInteractionsDropped += dropped
		}
		if bundle.interactionsUnavailable != "" {
			manifest.BrowserInteractionsIncomplete = true
			continue
		}
		if bundle.interactions == nil {
			continue
		}
		if state, acceptErr := accept(bundle.interactions.entry); acceptErr != nil || state != storage.EvidenceReceiptAccepted {
			manifest.BrowserInteractionsIncomplete = true
			continue
		}
		if file == nil {
			if file, err = createArchiveArtifactExclusive(workingDir, browserInteractionsFileName, 0o644, false); err != nil {
				return err
			}
			writer = bufio.NewWriter(file)
		}
		_, err = forEachBrowserInteractionRecord(bundle.interactions.path, func(evidence *protocol.BrowserInteractionEvidence) error {
			data, err := json.Marshal(evidence)
			if err != nil {
				return err
			}
			if _, err := writer.Write(append(data, '\n')); err != nil {
				return err
			}
			records++
			return nil
		})
		if err != nil {
			return fmt.Errorf("merge interaction evidence: %w", err)
		}
	}
	if file == nil {
		return nil
	}
	if err := writer.Flush(); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	manifest.BrowserInteractionsFile, manifest.BrowserInteractionRecordCount = browserInteractionsFileName, records
	return syncArchiveDirectory(workingDir)
}

// RecordBrowserInteractionEvidence writes interaction evidence into a run
// archive this controller writes itself.
func (w *runArchiveWriter) RecordBrowserInteractionEvidence(evidence *protocol.BrowserInteractionEvidence) error {
	if w == nil {
		return errors.New("run archive is unavailable")
	}
	evidence = evidence.Clone()
	if evidence == nil {
		return errors.New("browser interaction evidence is malformed")
	}
	data, err := json.Marshal(evidence)
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.txFile == nil {
		return errors.New("run archive is closed")
	}
	drop := func(reason string) error {
		w.manifest.BrowserInteractionsIncomplete = true
		w.manifest.BrowserInteractionsDropped += int64(len(evidence.Interactions)) + max(evidence.Dropped, 0)
		return errors.New(reason)
	}
	if err != nil || len(data) > protocol.MaxBrowserInteractionEvidenceBytes {
		return drop("browser interaction evidence exceeds its record bounds")
	}
	record := append(data, '\n')
	if w.interactionsBroken || int64(len(record)) > maxRunArchiveInteractionsBytes-w.interactionBytes || w.manifest.BrowserInteractionRecordCount >= maxRunArchiveInteractionsRecords {
		return drop("browser interaction evidence exceeds the run's sidecar budget")
	}
	if w.interactionsFile == nil {
		file, err := createArchiveArtifactExclusive(w.rootDir, browserInteractionsFileName, 0o644, false)
		if err != nil {
			w.manifest.BrowserInteractionsIncomplete = true
			return err
		}
		w.interactionsFile = file
		w.manifest.BrowserInteractionsFile = browserInteractionsFileName
	}
	var destination io.Writer = w.interactionsFile
	if w.captureBudget != nil {
		destination = archiveCaptureFileWriter{file: w.interactionsFile, budget: w.captureBudget}
	}
	if written, err := destination.Write(record); err != nil || written != len(record) {
		if written == 0 {
			// Refused before any byte (capture quota, free space): only
			// this record is lost.
			return errors.Join(drop("browser interaction evidence was not written"), err)
		}
		// A torn record corrupts the sidecar, which is then removed: every
		// record in it is lost, and nothing more is written into it.
		w.manifest.BrowserInteractionsDropped += w.interactionCount
		_ = w.interactionsFile.Close()
		w.interactionsFile = nil
		w.interactionsBroken = true
		return errors.Join(drop("browser interaction evidence sidecar was torn"), err, io.ErrShortWrite)
	}
	w.interactionCount += int64(len(evidence.Interactions))
	w.interactionBytes += int64(len(record))
	w.manifest.BrowserInteractionRecordCount++
	w.manifest.BrowserInteractionsDropped += evidence.Dropped
	w.manifest.BrowserInteractionsIncomplete = w.manifest.BrowserInteractionsIncomplete || evidence.Incomplete
	return nil
}

// closeBrowserInteractionsLocked syncs and closes the local sidecar; w.mu
// is held. A torn sidecar is removed from the archive.
func (w *runArchiveWriter) closeBrowserInteractionsLocked() error {
	if w.interactionsFile != nil {
		file := w.interactionsFile
		w.interactionsFile = nil
		if err := errors.Join(file.Sync(), file.Close()); err != nil {
			return err
		}
	}
	if w.interactionsBroken && w.manifest.BrowserInteractionsFile != "" {
		w.manifest.BrowserInteractionsFile, w.manifest.BrowserInteractionRecordCount = "", 0
		if err := os.Remove(filepath.Join(w.rootDir, browserInteractionsFileName)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
