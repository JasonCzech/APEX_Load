package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Native HAR artifacts in run archives (D5). A generator moves each
// published, scrubbed session HAR into its run archive. At finalization the
// controller fetches the declared HARs from the generator, verifies their
// size and SHA-256, places them in the run archive and records a durable
// accepted receipt, all before the manifest is written; receipts are
// published when the archive publication commits, and only then are the
// generators acknowledged so they delete their copies. HAR is optional
// evidence: a HAR that cannot be transferred is counted under an omission
// reason and the archive records browser_har_incomplete.

// RunArchiveEvidenceArtifact describes one transferable evidence artifact of
// a run archive: a native HAR or the browser interactions sidecar.
type RunArchiveEvidenceArtifact struct {
	Name     string `json:"name"`
	Kind     string `json:"kind"`
	Fidelity string `json:"fidelity"`
	// SourceIdentity is the generator archive source the HAR came from.
	SourceIdentity   string `json:"source_identity,omitempty"`
	SourceGeneration string `json:"source_generation"`
	Sequence         int64  `json:"sequence"`
	SHA256           string `json:"sha256"`
	Bytes            int64  `json:"bytes"`
}

const (
	maxRunArchiveHARBytes    = int64(100 << 20)
	maxRunArchiveHARRunBytes = int64(1 << 30)
	runArchiveHARRecordType  = "browser_har_native"

	harOmittedSourceUnavailable = protocol.BrowserHAROmittedSourceUnavailable
	harOmittedTransfer          = protocol.BrowserHAROmittedTransfer
	harOmittedInvalid           = protocol.BrowserHAROmittedInvalid
	harOmittedReceipt           = protocol.BrowserHAROmittedReceipt
	harOmittedRunBytes          = protocol.BrowserHAROmittedRunBytes
	harOmittedDuplicate         = protocol.BrowserHAROmittedDuplicate
	harOmittedArtifactLimit     = protocol.BrowserHAROmittedArtifactLimit
	harOmittedTooLarge          = protocol.BrowserHAROmittedTooLarge
	harOmittedArchiveQuota      = protocol.BrowserHAROmittedArchiveQuota
)

// runArchiveHARGeneration parses a run archive HAR name.
func runArchiveHARGeneration(name string) (string, bool) {
	generation, found := strings.CutPrefix(name, "browser_har_")
	generation, suffixed := strings.CutSuffix(generation, ".har")
	if !found || !suffixed || len(generation) != 32 {
		return "", false
	}
	if _, err := hex.DecodeString(generation); err != nil || strings.ToLower(generation) != generation {
		return "", false
	}
	return generation, true
}

// validDeclaredHAR checks a generator manifest's HAR entry.
func validDeclaredHAR(a RunArchiveEvidenceArtifact) bool {
	generation, ok := runArchiveHARGeneration(a.Name)
	return ok && generation == a.SourceGeneration && a.Kind == storage.EvidenceKindHARNative && a.Fidelity == "native" && validDeclaredSHA256(a.SHA256) && a.Bytes >= 0 && a.Bytes <= maxRunArchiveHARBytes && a.Sequence > 0
}

func validDeclaredSHA256(value string) bool {
	sum, err := hex.DecodeString(value)
	return err == nil && len(sum) == sha256.Size && strings.ToLower(value) == value
}

// remoteHARBudget bounds one run's transferred HAR bytes across sources.
type remoteHARBudget struct {
	mu        sync.Mutex
	remaining int64
	artifacts int
}

func newRemoteHARBudget() *remoteHARBudget {
	return &remoteHARBudget{remaining: maxRunArchiveHARRunBytes, artifacts: protocol.MaxBrowserHARArtifactsPerRun}
}

func (b *remoteHARBudget) reserve(bytes int64) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	switch {
	case b.artifacts <= 0:
		return harOmittedArtifactLimit
	case bytes > b.remaining:
		return harOmittedRunBytes
	}
	b.artifacts--
	b.remaining -= bytes
	return ""
}

// stagedRemoteHAR is a fetched, verified HAR awaiting the archive merge.
type stagedRemoteHAR struct {
	entry RunArchiveEvidenceArtifact
	path  string
}

// fetchRemoteHARArtifacts fetches a source's declared HARs from its live
// generator client into dir. The collection cache never holds HARs, so a
// source served from it, or without a client, has its HARs unavailable.
//
// transfer charges the bytes to the run's transfer reservation, whose writer
// refuses to go below the controller's free-space floor, so optional evidence
// can never exhaust the disk the required archive needs; a HAR it refuses is
// omitted.
func fetchRemoteHARArtifacts(ctx context.Context, client archiveArtifactClient, testID, sourceIdentity, dir string, declared []RunArchiveEvidenceArtifact, budget *remoteHARBudget, transfer *remoteArchiveTransferBudget) ([]stagedRemoteHAR, map[string]int64) {
	omitted := map[string]int64{}
	var staged []stagedRemoteHAR
	for _, entry := range declared {
		if !validDeclaredHAR(entry) {
			omitted[harOmittedInvalid]++
			continue
		}
		if client == nil {
			omitted[harOmittedSourceUnavailable]++
			continue
		}
		if reason := budget.reserve(entry.Bytes); reason != "" {
			omitted[reason]++
			continue
		}
		path := filepath.Join(dir, entry.Name)
		copied, err := fetchRemoteArchiveArtifactFileForSource(ctx, client, testID, entry.Name, sourceIdentity, path, entry.Bytes, transfer)
		if err == nil && copied != entry.Bytes {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			err = verifyHARFile(path, entry)
		}
		if err != nil {
			_ = os.Remove(path)
			omitted[harOmittedTransfer]++
			continue
		}
		entry.SourceIdentity = sourceIdentity
		staged = append(staged, stagedRemoteHAR{entry: entry, path: path})
	}
	return staged, omitted
}

// verifyHARFile checks a file's size and SHA-256 against its entry.
func verifyHARFile(path string, entry RunArchiveEvidenceArtifact) error {
	file, info, err := openArchiveArtifactForRead(filepath.Dir(path), filepath.Base(path))
	if err != nil {
		return err
	}
	defer file.Close()
	if info.Size() != entry.Bytes {
		return errors.New("HAR size differs from its declaration")
	}
	sum, err := sha256Reader(io.LimitReader(file, entry.Bytes+1))
	if err != nil {
		return err
	}
	if sum != entry.SHA256 {
		return errors.New("HAR SHA-256 differs from its declaration")
	}
	return nil
}

// harReceiptAcceptor records the controller's receipt of a HAR and returns
// its state.
type harReceiptAcceptor func(RunArchiveEvidenceArtifact) (string, error)

// mergeRemoteHARArtifacts places the sources' staged HARs in the run archive
// working directory and lists them in the manifest. Each is durably written
// before its receipt is accepted; a refused receipt removes it.
func mergeRemoteHARArtifacts(workingDir string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle, accept harReceiptAcceptor) error {
	omitted := map[string]int64{}
	seen := map[string]bool{}
	incomplete := false
	for _, bundle := range bundles {
		incomplete = incomplete || bundle.manifest.BrowserHARIncomplete
		for reason, count := range bundle.manifest.BrowserHAROmitted {
			if reason != "" && len(reason) <= 64 && count > 0 && count <= int64(protocol.MaxBrowserHARArtifactsPerRun)*1024 {
				omitted[reason] += count
			}
		}
		for reason, count := range bundle.harOmitted {
			omitted[reason] += count
		}
		for _, staged := range bundle.hars {
			if seen[staged.entry.Name] {
				omitted[harOmittedDuplicate]++
				continue
			}
			seen[staged.entry.Name] = true
			if err := copyStagedHAR(staged, workingDir); err != nil {
				omitted[harOmittedTransfer]++
				continue
			}
			state, err := accept(staged.entry)
			if err != nil || state != storage.EvidenceReceiptAccepted {
				if removeErr := os.Remove(filepath.Join(workingDir, staged.entry.Name)); removeErr != nil && !os.IsNotExist(removeErr) {
					return removeErr
				}
				omitted[harOmittedReceipt]++
				continue
			}
			manifest.BrowserHARArtifacts = append(manifest.BrowserHARArtifacts, staged.entry)
		}
	}
	if len(omitted) > 0 || incomplete {
		manifest.BrowserHARIncomplete = true
		manifest.BrowserHAROmitted = omitted
	}
	slices.SortFunc(manifest.BrowserHARArtifacts, func(a, b RunArchiveEvidenceArtifact) int {
		return strings.Compare(a.SourceIdentity+"\x00"+fmt.Sprintf("%020d", a.Sequence), b.SourceIdentity+"\x00"+fmt.Sprintf("%020d", b.Sequence))
	})
	return syncArchiveDirectory(workingDir)
}

// copyStagedHAR durably copies a verified staged HAR into the archive.
func copyStagedHAR(staged stagedRemoteHAR, workingDir string) (err error) {
	// The staged file was verified as fetched and is private to this
	// staging attempt: move it rather than hold two copies on disk. Only
	// across filesystems is it copied (and verified again).
	destination := filepath.Join(workingDir, staged.entry.Name)
	if _, statErr := os.Lstat(destination); errors.Is(statErr, os.ErrNotExist) {
		if os.Rename(staged.path, destination) == nil {
			return syncArchiveDirectory(workingDir)
		}
	}
	return copyStagedHARBytes(staged, workingDir)
}

// copyStagedHARBytes copies a staged HAR into the archive, verifying its
// size and SHA-256 as it goes.
func copyStagedHARBytes(staged stagedRemoteHAR, workingDir string) (err error) {
	source, _, err := openArchiveArtifactForRead(filepath.Dir(staged.path), filepath.Base(staged.path))
	if err != nil {
		return err
	}
	defer source.Close()
	destination, err := createArchiveArtifactExclusive(workingDir, staged.entry.Name, 0o644, false)
	if err != nil {
		return err
	}
	keep := false
	defer func() {
		err = errors.Join(err, destination.Close())
		if !keep || err != nil {
			_ = os.Remove(filepath.Join(workingDir, staged.entry.Name))
		}
	}()
	digest := sha256.New()
	copied, err := io.Copy(io.MultiWriter(destination, digest), io.LimitReader(source, staged.entry.Bytes+1))
	if err != nil {
		return err
	}
	if copied != staged.entry.Bytes || hex.EncodeToString(digest.Sum(nil)) != staged.entry.SHA256 {
		return errors.New("staged HAR changed before the archive merge")
	}
	if err := destination.Sync(); err != nil {
		return err
	}
	keep = true
	return nil
}

// acceptRemoteHARReceipt records the controller's durable receipt of a HAR
// under the run's execution generation. Without a receipt store the HAR is
// kept unacknowledged: its generator keeps its copy until retention.
func (tr *TestRunner) acceptRemoteHARReceipt(test *ManagedTest, targets map[string]string) harReceiptAcceptor {
	store, ok := tr.receiptStore()
	if !ok {
		return func(RunArchiveEvidenceArtifact) (string, error) { return storage.EvidenceReceiptAccepted, nil }
	}
	tr.mu.RLock()
	generation := test.executionGeneration
	tr.mu.RUnlock()
	return func(entry RunArchiveEvidenceArtifact) (string, error) {
		return store.AcceptRunEvidenceReceipt(storage.RunEvidenceReceipt{TestID: test.ID, ExecutionGeneration: generation, SourceIdentity: entry.SourceIdentity, Target: targets[entry.SourceIdentity], SourceGeneration: entry.SourceGeneration, Sequence: entry.Sequence, Kind: entry.Kind, Name: entry.Name, SHA256: entry.SHA256, Bytes: entry.Bytes})
	}
}

func (tr *TestRunner) receiptStore() (storage.RunEvidenceReceiptStore, bool) {
	if tr == nil {
		return nil, false
	}
	store, ok := tr.testStore.(storage.RunEvidenceReceiptStore)
	return store, ok
}

// evidenceAcknowledgingClient is a generator client that deletes its copies
// of acknowledged evidence.
type evidenceAcknowledgingClient interface {
	AcknowledgeEvidence(context.Context, generatorrpc.EvidenceAcknowledgement) (generatorrpc.EvidenceAckResult, error)
}

const evidenceAcknowledgementTimeout = 30 * time.Second

// publishRunEvidence publishes a run's accepted receipts once its archive
// publication committed, then acknowledges the published evidence to its
// generators in the background, outside the commit's fences: by then the
// run's generator clients are closed, so a target without a retained client
// is dialed afresh. A failed acknowledgement only leaves the generator's copy
// to its archive retention.
func (tr *TestRunner) publishRunEvidence(testID string) {
	store, ok := tr.receiptStore()
	if !ok {
		return
	}
	if _, err := store.PublishRunEvidenceReceipts(testID); err != nil {
		slog.Warn("run evidence receipts were not published", "test_id", testID, "err", err)
		return
	}
	receipts, err := store.ListRunEvidenceReceipts(testID)
	if err != nil {
		slog.Warn("run evidence receipts were not read for acknowledgement", "test_id", testID, "err", err)
		return
	}
	bySource := map[string][]storage.RunEvidenceReceipt{}
	for _, receipt := range receipts {
		if receipt.State == storage.EvidenceReceiptPublished {
			bySource[receipt.SourceIdentity] = append(bySource[receipt.SourceIdentity], receipt)
		}
	}
	if len(bySource) == 0 {
		return
	}
	var acks []evidenceAcknowledgementTarget
	for _, source := range slices.Sorted(maps.Keys(bySource)) {
		published := bySource[source]
		request := generatorrpc.EvidenceAcknowledgement{TestID: testID, SourceIdentity: source}
		for _, receipt := range published {
			request.Artifacts = append(request.Artifacts, generatorrpc.AcknowledgedEvidence{Name: receipt.Name, SHA256: receipt.SHA256})
		}
		acks = append(acks, evidenceAcknowledgementTarget{target: published[0].Target, request: request})
	}
	clients := tr.evidenceClients(testID)
	tr.mu.RLock()
	backend, _ := tr.execution.(*distributedExecutionBackend)
	tr.mu.RUnlock()
	tr.evidenceAcknowledgements.Add(1)
	go func() {
		defer tr.evidenceAcknowledgements.Done()
		acknowledgeRunEvidence(testID, acks, clients, backend)
	}()
}

// evidenceAcknowledgementTarget is one source's published evidence and the
// generator target holding it.
type evidenceAcknowledgementTarget struct {
	target  string
	request generatorrpc.EvidenceAcknowledgement
}

// acknowledgeRunEvidence tells each generator which of its copies the
// published archive now holds, through a retained client or a fresh dial.
func acknowledgeRunEvidence(testID string, acks []evidenceAcknowledgementTarget, clients map[string]generatorClient, backend *distributedExecutionBackend) {
	for _, ack := range acks {
		ctx, cancel := context.WithTimeout(context.Background(), evidenceAcknowledgementTimeout)
		client, ok := clients[ack.target].(evidenceAcknowledgingClient)
		var dialed generatorClient
		if !ok && backend != nil && backend.dial != nil && ack.target != "" {
			if fresh, err := backend.dial(ctx, ack.target); err == nil && fresh != nil {
				dialed = fresh
				client, ok = fresh.(evidenceAcknowledgingClient)
			}
		}
		if !ok {
			slog.Warn("no generator client to acknowledge published evidence; its copy stays until retention", "test_id", testID, "target", ack.target, "source", ack.request.SourceIdentity)
		} else if result, err := client.AcknowledgeEvidence(ctx, ack.request); err != nil || len(result.Retained) > 0 {
			slog.Warn("generator kept acknowledged evidence", "test_id", testID, "source", ack.request.SourceIdentity, "retained", len(result.Retained), "err", err)
		}
		if dialed != nil {
			_ = dialed.Close()
		}
		cancel()
	}
}

// waitEvidenceAcknowledgements waits for the background acknowledgements
// started so far (tests and shutdown).
func (tr *TestRunner) waitEvidenceAcknowledgements() {
	tr.evidenceAcknowledgements.Wait()
}

// evidenceClients are the run's generator clients by target, live or
// retained for archive collection.
func (tr *TestRunner) evidenceClients(testID string) map[string]generatorClient {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	test := tr.tests[testID]
	if test == nil {
		return nil
	}
	clients := cloneClientMap(test.remoteArchiveClientsByTarget)
	if clients == nil {
		clients = map[string]generatorClient{}
	}
	for target, client := range test.remoteClientsByTarget {
		if client != nil {
			clients[target] = client
		}
	}
	if test.remoteClient != nil && test.remoteTarget != "" && clients[test.remoteTarget] == nil {
		clients[test.remoteTarget] = test.remoteClient
	}
	return clients
}

// runArchiveHARMatches reports whether an archive file is the HAR its
// manifest entry describes.
func runArchiveHARMatches(manifest *RunArchiveManifest, artifact RunArchiveArtifact) bool {
	for _, har := range manifest.BrowserHARArtifacts {
		if har.Name == artifact.Name {
			return har.SHA256 == artifact.SHA256 && har.Bytes == artifact.SizeBytes
		}
	}
	return false
}

// RecordBrowserHARArtifact moves a session's published HAR into a run
// archive this controller writes itself. It needs no receipt: the archive
// and the HAR directory are local. The copy runs outside w.mu.
func (w *runArchiveWriter) RecordBrowserHARArtifact(artifact protocol.BrowserHARArtifact) error {
	if w == nil {
		return errors.New("run archive is unavailable")
	}
	w.harMu.Lock()
	defer w.harMu.Unlock()
	name := "browser_har_" + artifact.Generation + ".har"
	w.mu.Lock()
	closed, rootDir, count, runBytes := w.txFile == nil, w.rootDir, len(w.manifest.BrowserHARArtifacts), w.harBytes
	duplicate := false
	for _, existing := range w.manifest.BrowserHARArtifacts {
		duplicate = duplicate || existing.Name == name
	}
	w.mu.Unlock()
	omit := func(reason string) error {
		w.mu.Lock()
		defer w.mu.Unlock()
		w.manifest.BrowserHARIncomplete = true
		if w.manifest.BrowserHAROmitted == nil {
			w.manifest.BrowserHAROmitted = map[string]int64{}
		}
		w.manifest.BrowserHAROmitted[reason]++
		return fmt.Errorf("browser HAR omitted from the run archive: %s", reason)
	}
	switch {
	case closed:
		return errors.New("run archive is closed")
	case duplicate:
		return omit(harOmittedDuplicate)
	case count >= protocol.MaxBrowserHARArtifactsPerRun:
		return omit(harOmittedArtifactLimit)
	}
	if _, ok := runArchiveHARGeneration(name); !ok {
		return omit(harOmittedInvalid)
	}
	reason := harOmittedTransfer
	var sum string
	var size int64
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := browser.TakeRuntimeHARArtifact(ctx, artifact.Path, func(r io.Reader, length int64) (err error) {
		switch {
		case length > maxRunArchiveHARBytes:
			reason = harOmittedTooLarge
			return errors.New("HAR exceeds its size limit")
		case length > maxRunArchiveHARRunBytes-runBytes:
			reason = harOmittedRunBytes
			return errors.New("run HAR byte limit reached")
		}
		file, err := createArchiveArtifactExclusive(rootDir, name, 0o644, false)
		if err != nil {
			return err
		}
		keep := false
		defer func() {
			err = errors.Join(err, file.Close())
			if !keep || err != nil {
				_ = os.Remove(filepath.Join(rootDir, name))
			}
		}()
		digest := sha256.New()
		var destination io.Writer = file
		if w.captureBudget != nil {
			destination = archiveCaptureFileWriter{file: file, budget: w.captureBudget}
		}
		copied, err := io.Copy(io.MultiWriter(destination, digest), io.LimitReader(r, length+1))
		if err == nil && copied != length {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			err = file.Sync()
		}
		if err != nil {
			if errors.Is(err, errArchiveCaptureLimit) || errors.Is(err, errArchiveCaptureFreeSpace) {
				reason = harOmittedArchiveQuota
			}
			return err
		}
		sum, size, keep = hex.EncodeToString(digest.Sum(nil)), length, true
		return nil
	})
	if err != nil {
		return errors.Join(omit(reason), err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.harBytes += size
	w.manifest.BrowserHARArtifacts = append(w.manifest.BrowserHARArtifacts, RunArchiveEvidenceArtifact{Name: name, Kind: storage.EvidenceKindHARNative, Fidelity: "native", SourceGeneration: artifact.Generation, Sequence: int64(len(w.manifest.BrowserHARArtifacts) + 1), SHA256: sum, Bytes: size})
	return nil
}
