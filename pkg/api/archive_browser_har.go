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

	harOmittedSourceUnavailable = "source_unavailable"
	harOmittedTransfer          = "transfer_failed"
	harOmittedInvalid           = "invalid_artifact"
	harOmittedReceipt           = "receipt_refused"
	harOmittedRunBytes          = "run_byte_limit"
	harOmittedDuplicate         = "duplicate"
	harOmittedArtifactLimit     = "artifact_limit"
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
func fetchRemoteHARArtifacts(ctx context.Context, client archiveArtifactClient, testID, sourceIdentity, dir string, declared []RunArchiveEvidenceArtifact, budget *remoteHARBudget) ([]stagedRemoteHAR, map[string]int64) {
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
		copied, err := fetchRemoteArchiveArtifactFileForSource(ctx, client, testID, entry.Name, sourceIdentity, path, entry.Bytes, nil)
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
// publication committed, then acknowledges the published HARs to their
// generators. A failed acknowledgement only leaves the generator's copy to
// its archive retention.
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
	clients := tr.evidenceClients(testID)
	for _, source := range slices.Sorted(maps.Keys(bySource)) {
		published := bySource[source]
		client, ok := clients[published[0].Target].(evidenceAcknowledgingClient)
		if !ok {
			continue
		}
		request := generatorrpc.EvidenceAcknowledgement{TestID: testID, SourceIdentity: source}
		for _, receipt := range published {
			request.Artifacts = append(request.Artifacts, generatorrpc.AcknowledgedEvidence{Name: receipt.Name, SHA256: receipt.SHA256})
		}
		ctx, cancel := context.WithTimeout(context.Background(), evidenceAcknowledgementTimeout)
		result, err := client.AcknowledgeEvidence(ctx, request)
		cancel()
		if err != nil || len(result.Retained) > 0 {
			slog.Warn("generator kept acknowledged evidence", "test_id", testID, "source", source, "retained", len(result.Retained), "err", err)
		}
	}
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
			reason = harOmittedInvalid
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
