package api

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// orphanArchiveFinalizationStore provides the create-only transition used by
// startup recovery. The condition must be evaluated atomically with the insert:
// a recovery scan is never allowed to overwrite a newer finalization/refresh
// intent or recreate work after the archive catalog has committed.
type orphanArchiveFinalizationStore interface {
	SaveArchiveFinalizationJobIfOrphan(job *storage.ArchiveFinalizationJob) (bool, error)
}

type incarnationOrphanArchiveFinalizationStore interface {
	SaveArchiveFinalizationJobIfOrphanForRun(*storage.ArchiveFinalizationJob, time.Time, string, string, string) (bool, error)
}

type orphanArchiveCandidate struct {
	path     string
	identity os.FileInfo
}

const maxOrphanArchiveRecoveryCandidates = 10_000

// recoverOrphanedLocalRunArchives closes the crash window between committing a
// local manifest and committing the durable finalization outbox row. Only the
// execution leader performs recovery, and only a production store exposing an
// atomic create-if-orphan transition is eligible.
func (tr *TestRunner) recoverOrphanedLocalRunArchives(ctx context.Context) error {
	if tr == nil || tr.archiveStore == nil || tr.testStore == nil {
		return nil
	}
	tr.orphanRecoveryMu.Lock()
	defer tr.orphanRecoveryMu.Unlock()
	orphanStore, ok := tr.archiveStore.(incarnationOrphanArchiveFinalizationStore)
	if !ok {
		return nil
	}
	if !tr.isExecutionLeader() {
		tr.closeOrphanArchiveScanner()
		return errNotExecutionLeader
	}
	ctx, cancel := tr.archiveOperationContext(ctx)
	defer cancel()
	tr.orphanArchiveScanMu.Lock()
	root := resolveArchiveRoot()
	if tr.orphanArchiveScan != nil && tr.orphanArchiveScan.rootPath != root {
		tr.orphanArchiveScan.close()
		tr.orphanArchiveScan = nil
	}
	if tr.orphanArchiveScan == nil {
		scan, err := newOrphanArchiveScanner(root)
		if err != nil {
			tr.orphanArchiveScanMu.Unlock()
			return err
		}
		tr.orphanArchiveScan = scan
	}
	if tr.orphanArchiveScan == nil {
		tr.orphanArchiveScanMu.Unlock()
		return nil
	}
	scan := tr.orphanArchiveScan
	candidates, done, discoveryErr := scan.nextPage(ctx, maxOrphanArchiveRecoveryCandidates)
	tr.orphanArchiveScanMu.Unlock()
	failures := make([]error, 0)
	if discoveryErr != nil {
		failures = append(failures, discoveryErr)
	}
	for index, candidate := range candidates {
		if err := ctx.Err(); err != nil {
			tr.requeueOrphanArchiveCandidates(scan, candidates[index:])
			return errors.Join(append(failures, err)...)
		}
		if err := tr.recoverOrphanedLocalRunArchive(ctx, orphanStore, candidate); err != nil {
			failures = append(failures, err)
			if ctx.Err() != nil {
				// Rotate the slow candidate behind the untouched tail so a
				// repeatedly blocked archive cannot starve later orphans.
				tr.requeueOrphanArchiveCandidates(scan, append(append([]orphanArchiveCandidate(nil), candidates[index+1:]...), candidate))
				return errors.Join(failures...)
			}
		}
	}
	// A deadline may interrupt discovery after ReadDir has advanced its stream.
	// Keep its queued entries and candidates for the next sweep. Once all
	// returned candidates have been handled, EOF or a read error can reset it.
	if done || (discoveryErr != nil && !errors.Is(discoveryErr, context.Canceled) && !errors.Is(discoveryErr, context.DeadlineExceeded)) {
		tr.closeOrphanArchiveScannerIfCurrent(scan)
	}
	return errors.Join(failures...)
}

func (tr *TestRunner) requeueOrphanArchiveCandidates(scan *orphanArchiveScanner, candidates []orphanArchiveCandidate) {
	tr.orphanArchiveScanMu.Lock()
	if tr.orphanArchiveScan == scan {
		scan.retryCandidates = append(append([]orphanArchiveCandidate(nil), candidates...), scan.retryCandidates...)
	}
	tr.orphanArchiveScanMu.Unlock()
}

func (tr *TestRunner) closeOrphanArchiveScannerIfCurrent(scan *orphanArchiveScanner) {
	tr.orphanArchiveScanMu.Lock()
	if tr.orphanArchiveScan == scan {
		scan.close()
		tr.orphanArchiveScan = nil
	}
	tr.orphanArchiveScanMu.Unlock()
}

func (tr *TestRunner) closeOrphanArchiveScanner() {
	if tr == nil {
		return
	}
	tr.orphanArchiveScanMu.Lock()
	if tr.orphanArchiveScan != nil {
		tr.orphanArchiveScan.close()
		tr.orphanArchiveScan = nil
	}
	tr.orphanArchiveScanMu.Unlock()
}

func (tr *TestRunner) recoverOrphanedLocalRunArchive(ctx context.Context, store incarnationOrphanArchiveFinalizationStore, candidate orphanArchiveCandidate) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	release, err := lockArchivePublicationSource(ctx, filepath.Base(candidate.path))
	if err != nil {
		return err
	}
	defer release()
	return tr.recoverOrphanedLocalRunArchiveUnderSourceFence(ctx, store, candidate)
}

func (tr *TestRunner) recoverOrphanedLocalRunArchiveUnderSourceFence(ctx context.Context, store incarnationOrphanArchiveFinalizationStore, candidate orphanArchiveCandidate) error {
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	// Already cataloged, pending, deleted or resolved runs are not orphans. Check
	// durable ownership before reading payloads so startup never rehashes the
	// entire retained archive history.
	row, err := tr.testStore.GetTest(filepath.Base(candidate.path))
	if errors.Is(err, sql.ErrNoRows) || (err == nil && row == nil) {
		return nil
	}
	if err != nil {
		return err
	}
	if !isTerminalTestStatus(row.Status) {
		return nil
	}
	existing, err := tr.existingCollectedArchive(managedTestFromRecord(row))
	if errors.Is(err, storage.ErrArchiveCleanupPending) {
		return nil
	}
	if err != nil {
		return err
	}
	if existing != nil {
		return nil
	}
	record, manifest, err := orphanArchiveRecordFromDirectoryWithIdentity(ctx, candidate.path, candidate.identity)
	if err != nil {
		return fmt.Errorf("inspect orphan archive %q: %w", candidate.path, err)
	}
	if !isTerminalTestStatus(manifest.Status) {
		return nil
	}
	project := normalizeProjectID(row.ProjectID)
	if declared := strings.TrimSpace(manifest.Provenance.ProjectID); declared != "" && normalizeProjectID(declared) != project {
		return fmt.Errorf("orphan archive %s belongs to another project", record.TestID)
	}
	if row.EndTime == nil || manifest.Status != row.Status || !archiveRunTimestampMatches(archiveManifestRequestedAt(manifest), row.StartTime, tr.testStore) {
		return fmt.Errorf("orphan archive %s does not match the durable terminal incarnation", record.TestID)
	}
	job := &storage.ArchiveFinalizationJob{TestID: record.TestID, ProjectID: project, PersistArtifacts: true}
	if !archiveRunTimestampMatches(manifest.CompletedAt, *row.EndTime, tr.testStore) {
		// After a failover the executing term finalizes the archive at its own
		// time and the successor reconciles the row at another. Only the archive
		// of the term the durable row says executed the run is that run's
		// outcome; the durable end time then becomes its completion time. The
		// source stays untouched: the restamped manifest is committed with the
		// fenced job and restored by finalization.
		if row.ExecutionGeneration == 0 || manifest.Provenance.ExecutionGeneration != row.ExecutionGeneration {
			return fmt.Errorf("orphan archive %s does not match the durable terminal incarnation", record.TestID)
		}
		prepared, completedAt, err := restampedOrphanArchiveManifest(manifest, *row.EndTime)
		if err != nil {
			return fmt.Errorf("restamp failed-over orphan archive %s: %w", record.TestID, err)
		}
		record.CompletedAt, record.ManifestSHA256 = completedAt, sha256BytesHex(prepared)
		job.PreparedManifestJSON = string(prepared)
	}
	lease, owner := "", ""
	if tr.durableExecutionFence {
		_, holder, _, err := tr.generatorExecutionClaim()
		if err != nil {
			return err
		}
		lease, owner = executionLeaseName, holder
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	record.RefKey = strings.TrimSpace(row.RefKey)
	return tr.trackDurableWrite(func() error {
		job.ArchiveRecord = *record
		saved, err := store.SaveArchiveFinalizationJobIfOrphanForRun(job, archiveManifestRequestedAt(manifest), manifest.Status, lease, owner)
		if saved && job.PreparedManifestJSON != "" {
			slog.Info("queued failed-over run archive at the durable run end time", "test_id", record.TestID, "execution_generation", row.ExecutionGeneration, "executor_completed_at", manifest.CompletedAt, "completed_at", record.CompletedAt)
		}
		return err
	})
}

// restampedOrphanArchiveManifest returns the manifest bytes of a verified
// failed-over orphan with the durable run end as its completion time, keeping
// the first executor time. Every later exact end-time check then sees one
// terminal identity.
func restampedOrphanArchiveManifest(manifest *RunArchiveManifest, durableEnd time.Time) ([]byte, time.Time, error) {
	restamped := *manifest
	if restamped.ExecutorCompletedAt == nil {
		executor := manifest.CompletedAt
		restamped.ExecutorCompletedAt = &executor
	}
	restamped.CompletedAt = durableEnd.UTC()
	data, err := json.MarshalIndent(restamped, "", "  ")
	return data, restamped.CompletedAt, err
}

func discoverOrphanArchiveDirectories(ctx context.Context, root string) ([]orphanArchiveCandidate, error) {
	return discoverOrphanArchiveDirectoriesWithLimit(ctx, root, maxOrphanArchiveRecoveryCandidates)
}

func discoverOrphanArchiveDirectoriesWithLimit(ctx context.Context, root string, limit int) ([]orphanArchiveCandidate, error) {
	scan, err := newOrphanArchiveScanner(root)
	if err != nil || scan == nil {
		return nil, err
	}
	defer scan.close()
	candidates, done, err := scan.nextPage(ctx, limit)
	if !done && err == nil {
		// ReadDir may deliver exactly limit entries without returning EOF until
		// the next read. Probe once so an exact-sized directory is accepted.
		_, done, err = scan.nextPage(ctx, 1)
		if !done && err == nil {
			err = fmt.Errorf("archive orphan recovery entry limit %d exceeded", limit)
		}
	}
	return candidates, err
}

// orphanArchiveScanner holds directory streams between sweeps so every page
// reads at most its entry budget, regardless of how many earlier pages exist.
// It is owned by TestRunner.orphanArchiveScanMu and closed on EOF or shutdown.
type orphanArchiveScanner struct {
	rootPath        string
	absRoot         string
	root            *os.Root
	directory       *os.File
	remote          *os.Root
	remoteDirectory *os.File
	queuedEntries   []os.DirEntry
	queuedReadErr   error
	pending         []orphanArchiveCandidate
	retryCandidates []orphanArchiveCandidate
	phase           int // 0: root, 1: remote, 2: complete
}

func newOrphanArchiveScanner(root string) (*orphanArchiveScanner, error) {
	root = strings.TrimSpace(root)
	if root == "" {
		return nil, nil
	}
	absRoot, err := resolveArchiveDirectoryRoot(root, false)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("resolve archive recovery root: %w", err)
	}
	rootHandle, err := openArchiveDirectoryNoSymlink(absRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect archive recovery root: %w", err)
	}
	directory, err := rootHandle.Open(".")
	if err != nil {
		_ = rootHandle.Close()
		return nil, fmt.Errorf("read archive recovery root: %w", err)
	}
	return &orphanArchiveScanner{rootPath: root, absRoot: absRoot, root: rootHandle, directory: directory}, nil
}

func (scan *orphanArchiveScanner) close() {
	if scan == nil {
		return
	}
	if scan.directory != nil {
		_ = scan.directory.Close()
		scan.directory = nil
	}
	if scan.remoteDirectory != nil {
		_ = scan.remoteDirectory.Close()
		scan.remoteDirectory = nil
	}
	if scan.remote != nil {
		_ = scan.remote.Close()
		scan.remote = nil
	}
	if scan.root != nil {
		_ = scan.root.Close()
		scan.root = nil
	}
	scan.phase = 2
	scan.queuedEntries = nil
	scan.queuedReadErr = nil
	scan.pending = nil
	scan.retryCandidates = nil
}

func (scan *orphanArchiveScanner) advancePhase() error {
	if scan.phase == 0 {
		_ = scan.directory.Close()
		scan.directory = nil
		remote, err := openArchiveChildDirectoryAt(scan.root, "remote", false, 0)
		if os.IsNotExist(err) {
			scan.phase = 2
			return nil
		}
		if err != nil {
			return err
		}
		directory, err := remote.Open(".")
		if err != nil {
			_ = remote.Close()
			return err
		}
		scan.remote = remote
		scan.remoteDirectory = directory
		scan.phase = 1
		return nil
	}
	scan.phase = 2
	return nil
}

func (scan *orphanArchiveScanner) nextPage(ctx context.Context, limit int) ([]orphanArchiveCandidate, bool, error) {
	if scan == nil || limit <= 0 {
		return nil, true, nil
	}
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if len(scan.retryCandidates) > 0 {
		candidates := scan.retryCandidates
		scan.retryCandidates = nil
		return candidates, scan.phase == 2 && len(scan.queuedEntries) == 0 && len(scan.pending) == 0, nil
	}
	if len(scan.pending) > 0 {
		return scan.takePending(), scan.phase == 2 && len(scan.queuedEntries) == 0, nil
	}
	remaining := limit
	for remaining > 0 {
		if err := ctx.Err(); err != nil {
			return nil, false, err
		}
		if len(scan.queuedEntries) == 0 {
			if scan.queuedReadErr != nil {
				readErr := scan.queuedReadErr
				scan.queuedReadErr = nil
				if readErr != io.EOF {
					return scan.takePending(), false, readErr
				}
				if err := scan.advancePhase(); err != nil {
					return scan.takePending(), false, err
				}
			}
			if scan.phase == 2 {
				break
			}
		}
		parent, directory, parentPath := scan.root, scan.directory, scan.absRoot
		if scan.phase == 1 {
			parent, directory, parentPath = scan.remote, scan.remoteDirectory, filepath.Join(scan.absRoot, "remote")
		}
		if len(scan.queuedEntries) == 0 {
			entries, readErr := directory.ReadDir(min(256, remaining))
			if len(entries) == 0 && readErr == nil {
				return scan.takePending(), false, fmt.Errorf("archive recovery directory read made no progress")
			}
			scan.queuedEntries, scan.queuedReadErr = entries, readErr
			continue
		}
		entry := scan.queuedEntries[0]
		scan.queuedEntries = scan.queuedEntries[1:]
		remaining--
		if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
			continue
		}
		if err := validateArchiveRunID(entry.Name()); err != nil {
			continue
		}
		runRoot, err := openArchiveChildDirectoryAt(parent, entry.Name(), false, 0)
		if err != nil {
			continue
		}
		identity, identityErr := runRoot.Stat(".")
		manifest, _, manifestErr := openArchiveArtifactForReadAt(runRoot, runArchiveManifestFileName)
		if manifest != nil {
			_ = manifest.Close()
		}
		_ = runRoot.Close()
		if identityErr != nil || manifestErr != nil {
			continue
		}
		candidatePath := filepath.Join(parentPath, entry.Name())
		scan.pending = append(scan.pending, orphanArchiveCandidate{path: candidatePath, identity: identity})
	}
	return scan.takePending(), scan.phase == 2 && len(scan.queuedEntries) == 0, nil
}

func (scan *orphanArchiveScanner) takePending() []orphanArchiveCandidate {
	candidates := scan.pending
	scan.pending = nil
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].path < candidates[j].path })
	return candidates
}

func orphanArchiveRecordFromDirectory(ctx context.Context, archivePath string) (*storage.RunArchiveRecord, *RunArchiveManifest, error) {
	return orphanArchiveRecordFromDirectoryWithIdentity(ctx, archivePath, nil)
}

func orphanArchiveRecordFromDirectoryWithIdentity(ctx context.Context, archivePath string, expectedIdentity os.FileInfo) (*storage.RunArchiveRecord, *RunArchiveManifest, error) {
	archivePath, err := filepath.Abs(strings.TrimSpace(archivePath))
	if err != nil {
		return nil, nil, err
	}
	testID := filepath.Base(archivePath)
	if err := validateArchiveRunID(testID); err != nil {
		return nil, nil, err
	}
	parent, err := openArchiveDirectoryNoSymlink(filepath.Dir(archivePath))
	if err != nil {
		return nil, nil, err
	}
	defer parent.Close()
	runRoot, err := openArchiveChildDirectoryAt(parent, testID, false, 0)
	if err != nil {
		return nil, nil, err
	}
	defer runRoot.Close()
	runIdentity, err := runRoot.Stat(".")
	if err != nil {
		return nil, nil, err
	}
	if expectedIdentity != nil && !os.SameFile(expectedIdentity, runIdentity) {
		return nil, nil, fmt.Errorf("orphan archive directory %q was replaced after discovery", archivePath)
	}
	manifestPath := filepath.Join(archivePath, runArchiveManifestFileName)
	record := &storage.RunArchiveRecord{
		TestID:       testID,
		ArchivePath:  archivePath,
		ManifestPath: manifestPath,
	}
	if err := validateArchivePublicationPaths(record); err != nil {
		return nil, nil, err
	}
	data, _, err := readArchiveFileAtRootLimitedContext(ctx, runRoot, runArchiveManifestFileName, maxArchiveManifestDownloadBytes)
	if err != nil {
		return nil, nil, err
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(data, &manifest); err != nil {
		return nil, nil, fmt.Errorf("decode archive manifest: %w", err)
	}
	if strings.TrimSpace(manifest.SchemaVersion) == "" {
		manifest.SchemaVersion = archiveSchemaVersion
	}
	if manifest.TestID != testID {
		return nil, nil, fmt.Errorf("archive manifest test id %q does not match directory %q", manifest.TestID, testID)
	}
	if err := validateRunArchiveManifestArtifacts(&manifest); err != nil {
		return nil, nil, err
	}

	backend := currentArchiveBackend()
	record.SchemaVersion = manifest.SchemaVersion
	record.ArchiveBackend = backend.Backend
	record.ManifestSHA256 = sha256BytesHex(data)
	record.ArchiveURI = backend.runURI(testID)
	record.ManifestURI = backend.manifestURI(testID)
	record.TransactionCount = manifest.TransactionCount
	record.ResourceSampleCount = manifest.ResourceSampleCount
	record.IntervalSummaryCount = manifest.IntervalSummaryCount
	record.CreatedAt = manifest.CreatedAt
	record.CompletedAt = manifest.CompletedAt
	if err := validateOrphanArchiveArtifacts(ctx, runRoot, &manifest, int64(len(data))); err != nil {
		return nil, nil, err
	}
	return record, &manifest, nil
}

// Generator archive creation can differ from the controller request time.
// Legacy manifests without provenance can only use their creation timestamp.
func archiveManifestRequestedAt(manifest *RunArchiveManifest) time.Time {
	if !manifest.Provenance.RequestedAt.IsZero() {
		return manifest.Provenance.RequestedAt
	}
	return manifest.CreatedAt
}
