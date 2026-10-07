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
	"github.com/jasonczech/apexload/pkg/internal/archivequota"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

// release returns a reservation whose HAR was not kept.
func (b *remoteHARBudget) release(bytes int64) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.artifacts++
	b.remaining += bytes
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
func fetchRemoteHARArtifacts(ctx context.Context, source *optionalEvidenceSource, testID, sourceIdentity, dir string, declared []RunArchiveEvidenceArtifact, budget *remoteHARBudget, transfer *remoteArchiveTransferBudget) ([]stagedRemoteHAR, map[string]int64) {
	omitted := map[string]int64{}
	var staged []stagedRemoteHAR
	for _, entry := range declared {
		if !validDeclaredHAR(entry) {
			omitted[harOmittedInvalid]++
			continue
		}
		if source == nil || source.client == nil {
			omitted[harOmittedSourceUnavailable]++
			continue
		}
		if source.stalled {
			omitted[harOmittedTransfer]++
			continue
		}
		if reason := budget.reserve(entry.Bytes); reason != "" {
			omitted[reason]++
			continue
		}
		path := filepath.Join(dir, entry.Name)
		artifact, err := transfer.reserveOptional(entry.Bytes)
		if err != nil {
			budget.release(entry.Bytes)
			omitted[harOmittedTransfer]++
			continue
		}
		copied, err := source.fetch(ctx, testID, entry.Name, sourceIdentity, path, entry.Bytes, artifact)
		if err == nil && copied != entry.Bytes {
			err = io.ErrUnexpectedEOF
		}
		if err == nil {
			err = verifyHARFile(path, entry)
		}
		if err != nil {
			_ = os.Remove(path)
			transfer.release(entry.Bytes)
			budget.release(entry.Bytes)
			omitted[harOmittedTransfer]++
			continue
		}
		entry.SourceIdentity = sourceIdentity
		staged = append(staged, stagedRemoteHAR{entry: entry, path: path})
	}
	return staged, omitted
}

// optionalEvidenceFetchTimeout bounds one optional artifact's fetch by its
// declared size (30 s plus 1 MiB/s), so a stalled stream is omitted rather
// than holding a fetch slot until the attempt's deadline.
var optionalEvidenceFetchTimeout = func(bytes int64) time.Duration {
	return 30*time.Second + time.Duration(max(bytes, 0)>>20)*time.Second
}

// optionalEvidenceSource is one generator's live client for its optional
// evidence (HARs, then the interactions sidecar). Once a fetch from it runs
// out its own deadline, the source's remaining optional artifacts are not
// fetched (each is transfer_failed): every one would wait out another
// deadline, and one stalled source must not use up the staging attempt.
type optionalEvidenceSource struct {
	client  archiveArtifactClient
	stalled bool
}

func (s *optionalEvidenceSource) fetch(ctx context.Context, testID, name, sourceIdentity, path string, size int64, budget *remoteArchiveTransferBudget) (int64, error) {
	fetchCtx, cancel := context.WithTimeout(ctx, optionalEvidenceFetchTimeout(size))
	defer cancel()
	copied, err := fetchRemoteArchiveArtifactFileForSource(fetchCtx, s.client, testID, name, sourceIdentity, path, size, budget)
	if err != nil && ctx.Err() == nil && errors.Is(fetchCtx.Err(), context.DeadlineExceeded) {
		s.stalled = true
	}
	return copied, err
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
func mergeRemoteHARArtifacts(workingDir string, manifest *RunArchiveManifest, bundles []remoteArchiveBundle, accept harReceiptAcceptor, budget *archiveOutputBudget) error {
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
			if err := copyStagedHAR(staged, workingDir, budget); err != nil {
				if errors.Is(err, errHARArchiveQuota) {
					omitted[harOmittedArchiveQuota]++
				} else {
					omitted[harOmittedTransfer]++
				}
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

// renameStagedHAR moves a staged HAR into the archive (tests replace it to
// cross filesystems).
var renameStagedHAR = os.Rename

// errHARArchiveQuota: the archive's merge budget left no room for an
// optional HAR copy.
var errHARArchiveQuota = errors.New("archive merge budget has no room for the HAR")

// runArchiveHARSource is a HAR-directory copy of a HAR a local run archive
// holds.
type runArchiveHARSource struct {
	path     string
	identity os.FileInfo
}

// retireArchivedHARSourcesLocked retires the HAR-directory copies once the
// local archive is finalized; one that cannot be retired stays under its
// directory's retention. The caller holds harMu.
func (w *runArchiveWriter) retireArchivedHARSourcesLocked() {
	for _, source := range w.harSources {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		if err := browser.RetireRuntimeHARArtifact(ctx, source.path, source.identity); err != nil && !errors.Is(err, os.ErrNotExist) {
			slog.Warn("archived HAR left in its HAR directory until retention", "path", source.path, "error", err)
		}
		cancel()
	}
	w.harSources = nil
}

// copyStagedHAR durably copies a verified staged HAR into the archive.
func copyStagedHAR(staged stagedRemoteHAR, workingDir string, budget *archiveOutputBudget) (err error) {
	// The staged file was verified as fetched and is private to this
	// staging attempt: move it rather than hold two copies on disk. Only
	// across filesystems (the shipped layouts keep transfers on another
	// volume) is it copied, within the merge budget, and verified again.
	destination := filepath.Join(workingDir, staged.entry.Name)
	if _, statErr := os.Lstat(destination); errors.Is(statErr, os.ErrNotExist) {
		if renameStagedHAR(staged.path, destination) == nil {
			if err := syncArchiveDirectory(workingDir); err != nil {
				// Not durably in the archive: keep it out of it.
				_ = os.Remove(destination)
				return err
			}
			return nil
		}
	}
	if !budget.admitOptional(staged.entry.Bytes) {
		return errHARArchiveQuota
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
	generation, owner := test.executionGeneration, test.executionFence
	tr.mu.RUnlock()
	// Under the durable execution fence each accept proves, in its own
	// transaction, that this controller still holds the run's lease term.
	leaseName := ""
	if tr.durableExecutionFence && owner != "" && generation > 0 {
		leaseName = executionLeaseName
	} else {
		owner = ""
	}
	return func(entry RunArchiveEvidenceArtifact) (state string, err error) {
		err = tr.trackDurableWrite(func() error {
			state, err = store.AcceptRunEvidenceReceipt(storage.RunEvidenceReceipt{TestID: test.ID, ExecutionGeneration: generation, SourceIdentity: entry.SourceIdentity, Target: targets[entry.SourceIdentity], SourceGeneration: entry.SourceGeneration, Sequence: entry.Sequence, Kind: entry.Kind, Name: entry.Name, SHA256: entry.SHA256, Bytes: entry.Bytes, LeaseName: leaseName, LeaseOwner: owner})
			return err
		})
		return state, err
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
	if err := tr.trackDurableWrite(func() error {
		_, err := store.PublishRunEvidenceReceipts(testID)
		return err
	}); err != nil {
		slog.Warn("run evidence receipts were not published", "test_id", testID, "err", err)
		return
	}
	receipts, err := store.ListRunEvidenceReceipts(testID)
	if err != nil {
		slog.Warn("run evidence receipts were not read for acknowledgement", "test_id", testID, "err", err)
		return
	}
	// Only evidence no generator confirmed yet in this process, and none
	// whose last attempt failed within the retry interval: a later commit of
	// the run (a refresh, a note edit) acknowledges nothing again, while
	// after a restart it re-drives what was never confirmed.
	now := time.Now()
	bySource := map[string][]storage.RunEvidenceReceipt{}
	for _, receipt := range receipts {
		if receipt.State == storage.EvidenceReceiptPublished && tr.acknowledgedEvidence.due(acknowledgedEvidenceKey(testID, receipt.SourceIdentity, receipt.Name, receipt.SHA256), now) {
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
		acknowledgeRunEvidence(testID, acks, clients, backend, &tr.acknowledgedEvidence)
	}()
}

// acknowledgedEvidenceKey names one published artifact of one source.
func acknowledgedEvidenceKey(testID, source, name, sha string) string {
	return testID + "\x00" + source + "\x00" + name + "\x00" + sha
}

// maxAcknowledgedEvidence bounds the remembered confirmations and retry
// times; the oldest are forgotten first (a forgotten one is at most
// acknowledged again).
const maxAcknowledgedEvidence = 1 << 16

// evidenceAcknowledgementRetry is how long an acknowledgement that failed
// (no reachable generator, an error, a retained copy) waits before a later
// commit of the run tries it again.
var evidenceAcknowledgementRetry = 15 * time.Minute

// acknowledgedEvidenceSet remembers, per artifact, a confirmed
// acknowledgement (a zero time) or the earliest retry of a failed one.
type acknowledgedEvidenceSet struct {
	mu    sync.Mutex
	keys  map[string]time.Time
	order []string
}

// due reports whether an artifact should be acknowledged at now.
func (a *acknowledgedEvidenceSet) due(key string, now time.Time) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	retry, ok := a.keys[key]
	return !ok || !retry.IsZero() && !now.Before(retry)
}

// has reports a confirmed acknowledgement.
func (a *acknowledgedEvidenceSet) has(key string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	retry, ok := a.keys[key]
	return ok && retry.IsZero()
}

func (a *acknowledgedEvidenceSet) add(key string) { a.set(key, time.Time{}) }

// retryAfter defers a failed acknowledgement; it never undoes a confirmation.
func (a *acknowledgedEvidenceSet) retryAfter(key string, at time.Time) { a.set(key, at) }

// set records a confirmation (a zero time) or a retry time, in one locked
// step so a confirmation made meanwhile is never replaced by a retry.
func (a *acknowledgedEvidenceSet) set(key string, at time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.keys == nil {
		a.keys = map[string]time.Time{}
	}
	current, ok := a.keys[key]
	if ok && current.IsZero() {
		return
	}
	if !ok {
		a.order = append(a.order, key)
	}
	a.keys[key] = at
	if len(a.order) > maxAcknowledgedEvidence {
		delete(a.keys, a.order[0])
		a.order = a.order[1:]
	}
}

// evidenceAcknowledgementTarget is one source's published evidence and the
// generator target holding it.
type evidenceAcknowledgementTarget struct {
	target  string
	request generatorrpc.EvidenceAcknowledgement
}

// acknowledgeRunEvidence tells each generator which of its copies the
// published archive now holds, through a retained client or a fresh dial.
func acknowledgeRunEvidence(testID string, acks []evidenceAcknowledgementTarget, clients map[string]generatorClient, backend *distributedExecutionBackend, confirmed *acknowledgedEvidenceSet) {
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
			deferAcknowledgement(confirmed, testID, ack.request)
		} else if result, err := client.AcknowledgeEvidence(ctx, ack.request); status.Code(err) == codes.NotFound {
			// The generator no longer has the run's archive (its retention
			// swept it): nothing is left to delete.
			confirmAcknowledged(confirmed, testID, ack.request, acknowledgedNames(ack.request))
		} else if err != nil || len(result.Retained) > 0 {
			slog.Warn("generator kept acknowledged evidence", "test_id", testID, "source", ack.request.SourceIdentity, "retained", len(result.Retained), "err", err)
			if err == nil {
				confirmAcknowledged(confirmed, testID, ack.request, result.Deleted)
			}
			deferAcknowledgement(confirmed, testID, ack.request)
		} else {
			confirmAcknowledged(confirmed, testID, ack.request, result.Deleted)
		}
		if dialed != nil {
			_ = dialed.Close()
		}
		cancel()
	}
}

// confirmAcknowledged remembers the artifacts a generator deleted (or no
// longer had).
func confirmAcknowledged(confirmed *acknowledgedEvidenceSet, testID string, request generatorrpc.EvidenceAcknowledgement, deleted []string) {
	if confirmed == nil {
		return
	}
	gone := map[string]bool{}
	for _, name := range deleted {
		gone[name] = true
	}
	for _, artifact := range request.Artifacts {
		if gone[artifact.Name] {
			confirmed.add(acknowledgedEvidenceKey(testID, request.SourceIdentity, artifact.Name, artifact.SHA256))
		}
	}
}

// deferAcknowledgement schedules the retry of a request's unconfirmed
// artifacts.
func deferAcknowledgement(confirmed *acknowledgedEvidenceSet, testID string, request generatorrpc.EvidenceAcknowledgement) {
	if confirmed == nil {
		return
	}
	retry := time.Now().Add(evidenceAcknowledgementRetry)
	for _, artifact := range request.Artifacts {
		confirmed.retryAfter(acknowledgedEvidenceKey(testID, request.SourceIdentity, artifact.Name, artifact.SHA256), retry)
	}
}

func acknowledgedNames(request generatorrpc.EvidenceAcknowledgement) []string {
	names := make([]string, 0, len(request.Artifacts))
	for _, artifact := range request.Artifacts {
		names = append(names, artifact.Name)
	}
	return names
}

// waitEvidenceAcknowledgements waits for the background acknowledgements
// started so far (tests).
func (tr *TestRunner) waitEvidenceAcknowledgements() {
	tr.evidenceAcknowledgements.Wait()
}

// waitEvidenceAcknowledgementsUntil waits for the background
// acknowledgements until ctx ends, at shutdown; one not finished leaves its
// generator's copy to retention.
func (tr *TestRunner) waitEvidenceAcknowledgementsUntil(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		tr.evidenceAcknowledgements.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
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
	// Read, then retire only after the archive is final: a failure after the
	// copy (retirement, a namespace error) must neither lose the HAR nor
	// leave an undeclared copy in the archive.
	created := false
	admitted := int64(0)
	identity, err := browser.ReadRuntimeHARArtifact(ctx, artifact.Path, func(r io.Reader, length int64) (err error) {
		switch {
		case length > maxRunArchiveHARBytes:
			reason = harOmittedTooLarge
			return errors.New("HAR exceeds its size limit")
		case length > maxRunArchiveHARRunBytes-runBytes:
			reason = harOmittedRunBytes
			return errors.New("run HAR byte limit reached")
		case !w.captureBudget.admitOptional(length):
			// Optional evidence keeps within its share of the capture
			// budget and the volume, so required capture keeps the rest.
			reason = harOmittedArchiveQuota
			return errors.New("archive capture has no room for optional evidence")
		}
		admitted = length
		file, err := createArchiveArtifactExclusive(rootDir, name, 0o644, false)
		if err != nil {
			return err
		}
		created = true
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
			if errors.Is(err, errArchiveCaptureLimit) || errors.Is(err, errArchiveCaptureFreeSpace) || errors.Is(err, archivequota.ErrCapacity) {
				reason = harOmittedArchiveQuota
			}
			return err
		}
		sum, size, keep = hex.EncodeToString(digest.Sum(nil)), length, true
		return nil
	})
	if err != nil {
		if created {
			_ = os.Remove(filepath.Join(rootDir, name))
		}
		w.captureBudget.releaseOptional(admitted)
		return errors.Join(omit(reason), err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.harSources = append(w.harSources, runArchiveHARSource{path: artifact.Path, identity: identity})
	w.harBytes += size
	w.manifest.BrowserHARArtifacts = append(w.manifest.BrowserHARArtifacts, RunArchiveEvidenceArtifact{Name: name, Kind: storage.EvidenceKindHARNative, Fidelity: "native", SourceGeneration: artifact.Generation, Sequence: int64(len(w.manifest.BrowserHARArtifacts) + 1), SHA256: sum, Bytes: size})
	return nil
}
