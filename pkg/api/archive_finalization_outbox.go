package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

const (
	archiveFinalizationAttemptTimeout = 30 * time.Minute
	archiveFinalizationRetryInterval  = time.Minute
)

// archiveFinalizationOutbox is intentionally an optional storage extension so
// lightweight test/custom stores continue to work. The production SQLite store
// implements it; whenever it is available, publication cannot begin until the
// durable intent has committed.
type archiveFinalizationOutbox interface {
	SaveArchiveFinalizationJob(job *storage.ArchiveFinalizationJob) error
	GetArchiveFinalizationJob(testID string) (*storage.ArchiveFinalizationJob, error)
	ListArchiveFinalizationJobs(limit int) ([]*storage.ArchiveFinalizationJob, error)
	UpdateArchiveFinalizationRecordIfToken(testID string, intentToken string, record *storage.RunArchiveRecord) (bool, error)
	CompleteArchiveFinalization(testID string, intentToken string, refreshToken string, record *storage.RunArchiveRecord) (bool, error)
	MarkArchiveFinalizationJobFailedIfToken(testID string, intentToken string, finalizationErr error, updatedAt time.Time) error
	DeleteArchiveFinalizationJob(testID string) error
}

type archiveRefreshOutbox interface {
	BumpArchiveRefreshJob(testID string) (*storage.ArchiveRefreshJob, error)
	GetArchiveRefreshJob(testID string) (*storage.ArchiveRefreshJob, error)
	ListArchiveRefreshJobs(limit int) ([]*storage.ArchiveRefreshJob, error)
	DeleteArchiveRefreshJob(testID string) error
	DeleteArchiveRefreshJobIfToken(testID string, intentToken string) (bool, error)
	RebindArchiveRefreshToFinalization(testID string, refreshToken string) (*storage.ArchiveFinalizationJob, bool, error)
}

func (tr *TestRunner) archiveFinalizationOutbox() archiveFinalizationOutbox {
	if tr == nil || tr.archiveStore == nil {
		return nil
	}
	outbox, _ := tr.archiveStore.(archiveFinalizationOutbox)
	return outbox
}

func (tr *TestRunner) archiveRefreshOutbox() archiveRefreshOutbox {
	if tr == nil || tr.archiveStore == nil {
		return nil
	}
	outbox, _ := tr.archiveStore.(archiveRefreshOutbox)
	return outbox
}

// finalizeRunArchiveDurably publishes an already committed local archive and
// catalogs it. Publication is fenced against deletion, and the production
// store records the durable intent before adjunct generation or object I/O.
func (tr *TestRunner) finalizeRunArchiveDurably(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, persistArtifacts bool) error {
	if tr == nil || test == nil || record == nil {
		return nil
	}
	unlock := tr.lockArchiveCommitFence(test.ID)
	defer unlock()
	return tr.finalizeRunArchiveDurablyUnderFence(ctx, test, record, persistArtifacts)
}

func (tr *TestRunner) finalizeRunArchiveDurablyUnderFence(ctx context.Context, test *ManagedTest, record *storage.RunArchiveRecord, persistArtifacts bool) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	ctx, cancelOperation := tr.archiveOperationContext(ctx)
	defer cancelOperation()
	if err := ctx.Err(); err != nil {
		return err
	}
	releaseSource, err := lockArchivePublicationSource(ctx, test.ID)
	if err != nil {
		return err
	}
	defer releaseSource()

	recordCopy := *record
	job := &storage.ArchiveFinalizationJob{
		TestID:           strings.TrimSpace(test.ID),
		ProjectID:        normalizeProjectID(test.ProjectID),
		ArchiveRecord:    recordCopy,
		PersistArtifacts: persistArtifacts,
	}
	if job.TestID == "" {
		return fmt.Errorf("archive finalization test id is required")
	}
	if job.ArchiveRecord.TestID == "" {
		job.ArchiveRecord.TestID = job.TestID
	}
	if job.ArchiveRecord.TestID != job.TestID {
		return fmt.Errorf("archive record test id %q does not match test %q", job.ArchiveRecord.TestID, job.TestID)
	}

	outbox := tr.archiveFinalizationOutbox()
	if outbox != nil {
		completed, err := tr.prepareProducerArchiveIntent(outbox, job)
		if err != nil {
			return fmt.Errorf("save archive finalization intent: %w", err)
		}
		if completed {
			*record = job.ArchiveRecord
			tr.attachFinalizedRunArchive(job.TestID, record)
			return nil
		}
	}
	defer func() { *record = job.ArchiveRecord }()
	if err := tr.processArchiveFinalizationJobUnderSourceFence(ctx, job, test); err != nil {
		tr.markArchiveFinalizationFailed(job.TestID, job.IntentToken, err)
		return err
	}
	return nil
}

// processArchiveFinalizationJob is used by the startup/leader retry worker.
// The durable test row is checked under the same local fence as deletion before
// any publication side effects. A test deleted while the controller was down is
// cancellation, not a permanently failing job.
func (tr *TestRunner) processArchiveFinalizationJob(ctx context.Context, job *storage.ArchiveFinalizationJob) error {
	if tr == nil || job == nil || strings.TrimSpace(job.TestID) == "" {
		return nil
	}
	unlock := tr.lockArchiveCommitFence(job.TestID)
	defer unlock()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	ctx, cancelOperation := tr.archiveOperationContext(ctx)
	defer cancelOperation()
	outbox := tr.archiveFinalizationOutbox()
	if outbox != nil {
		current, err := outbox.GetArchiveFinalizationJob(job.TestID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reload archive finalization intent: %w", err)
		}
		if current == nil || current.IntentToken != job.IntentToken {
			return nil
		}
		// Keep the caller-owned job synchronized. Producer finalization copies its
		// ArchiveRecord back to the live run after this method returns.
		*job = *current
	}
	return tr.processArchiveFinalizationJobUnderFence(ctx, job, nil)
}

func (tr *TestRunner) processArchiveFinalizationJobUnderFence(ctx context.Context, job *storage.ArchiveFinalizationJob, test *ManagedTest) error {
	if job == nil {
		return nil
	}
	release, err := lockArchivePublicationSource(ctx, job.TestID)
	if err != nil {
		return err
	}
	defer release()
	return tr.processArchiveFinalizationJobUnderSourceFence(ctx, job, test)
}

func (tr *TestRunner) processArchiveFinalizationJobUnderSourceFence(ctx context.Context, job *storage.ArchiveFinalizationJob, test *ManagedTest) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if job == nil {
		return nil
	}
	job.TestID = strings.TrimSpace(job.TestID)
	if job.TestID == "" {
		return fmt.Errorf("archive finalization test id is required")
	}
	outbox := tr.archiveFinalizationOutbox()
	if outbox != nil {
		if strings.TrimSpace(job.IntentToken) == "" {
			return fmt.Errorf("archive finalization intent %s has no opaque token", job.TestID)
		}
		current, err := outbox.GetArchiveFinalizationJob(job.TestID)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("reload archive finalization intent under fence: %w", err)
		}
		if current == nil || current.IntentToken != job.IntentToken {
			return nil
		}
		// Keep the producer-owned job synchronized so its finalized record (trust
		// anchor and immutable object URI) is copied back to the live run.
		*job = *current
	}

	// The database is authoritative even if this replica still has a stale
	// ManagedTest pointer. This check also lets a deletion transaction convert
	// the job to cleanup without a retry worker resurrecting the catalog row.
	var durableTest *storage.TestRecord
	if tr.testStore != nil {
		var err error
		durableTest, err = tr.testStore.GetTest(job.TestID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && durableTest == nil) {
			return tr.cancelArchiveFinalizationJob(job.TestID)
		}
		if err != nil {
			return fmt.Errorf("load test for archive finalization: %w", err)
		}
		if normalizeProjectID(durableTest.ProjectID) != normalizeProjectID(job.ProjectID) {
			return fmt.Errorf("archive finalization project %q does not match test project %q", job.ProjectID, durableTest.ProjectID)
		}
		if !isTerminalTestStatus(durableTest.Status) {
			return fmt.Errorf("test %s is not durably terminal yet", job.TestID)
		}
		// Use the durable row as a fallback, then prefer a deep in-memory snapshot
		// captured under tr.mu. Producer finalization passes the live ManagedTest
		// pointer; reading its maps/slices while pause/stop/metrics mutate it races.
		test = managedTestFromRecord(durableTest)
	}
	test = tr.preferredArchiveFinalizationTestSnapshot(job.TestID, durableTest, test)
	if test == nil {
		return fmt.Errorf("test %s is unavailable for archive finalization", job.TestID)
	}

	snapshotRetryNeeded := false
	snapshotCtx, cancelSnapshot := context.WithTimeout(ctx, importedDashboardSnapshotTimeout)
	snapshotErr := tr.captureImportedDashboardSnapshotAttempt(snapshotCtx, test, job.ArchiveRecord.CompletedAt, strings.TrimSpace(job.RefreshToken) != "" && job.Attempts >= 2)
	cancelSnapshot()
	if snapshotErr != nil {
		if strings.TrimSpace(job.RefreshToken) != "" {
			return fmt.Errorf("capture imported dashboard snapshot: %w", snapshotErr)
		}
		snapshotRetryNeeded = true
		slog.Warn("archive finalization: imported dashboard snapshot deferred to durable backfill", "test", test.ID, "err", snapshotErr)
	} else if err := tr.applyImportedDashboardThresholdVerdict(test); err != nil {
		return fmt.Errorf("apply imported dashboard threshold verdict: %w", err)
	}
	if strings.TrimSpace(job.RefreshToken) != "" && job.Attempts >= 2 {
		slog.Info("archive finalization: imported dashboard snapshot retry budget exhausted; persisted fail-soft evidence", "test", test.ID, "attempts", job.Attempts+1)
	}

	record := &job.ArchiveRecord
	if strings.TrimSpace(record.TestID) == "" {
		record.TestID = job.TestID
	}
	if record.TestID != job.TestID {
		return fmt.Errorf("archive record test id %q does not match job %q", record.TestID, job.TestID)
	}
	// A refresh can change manifest-derived SLA/completeness evidence while
	// retaining the same run ID. Never carry the prior publication's compact
	// trend projection into the refreshed manifest; regenerate it after adjuncts
	// have been persisted below.
	if strings.TrimSpace(job.RefreshToken) != "" {
		record.TrendPointJSON = ""
	}
	var materializationLease *archiveMaterializationLease
	// Refreshes always operate on a token-private copy. This is required for
	// filesystem archives too: adjunct generation writes manifest/report files,
	// and doing that in the currently catalogued directory would expose partial
	// refresh output before the durable token CAS commits.
	if strings.TrimSpace(job.RefreshToken) != "" {
		if tr.archiveStore != nil {
			catalogRecord, catalogErr := tr.archiveStore.GetRunArchive(job.TestID)
			if catalogErr == nil && catalogRecord != nil {
				lease, err := acquireArchiveMaterialized(ctx, catalogRecord)
				if err != nil {
					return fmt.Errorf("materialize archive for adjunct refresh: %w", err)
				}
				materializationLease = lease
				defer materializationLease.Release()
				stagedRoot, stagedManifest, err := stageMaterializedArchiveForPublication(ctx, catalogRecord, job.IntentToken)
				if err != nil {
					return fmt.Errorf("stage materialized archive for adjunct refresh: %w", err)
				}
				record.ArchivePath = stagedRoot
				record.ManifestPath = stagedManifest
				// Each refresh attempt starts from the verified catalog version.
				// A prior failed attempt may have durably saved an adjunct digest
				// for this private path; it does not describe the freshly copied
				// catalog manifest. Preserve the new intent's URI, but use the
				// source's trust anchor until adjunct generation refreshes it.
				record.ManifestSHA256 = catalogRecord.ManifestSHA256
			} else if catalogErr != nil && !errors.Is(catalogErr, sql.ErrNoRows) {
				return fmt.Errorf("load archive catalog for adjunct refresh: %w", catalogErr)
			}
		}
	}
	if err := validateArchivePublicationPaths(record); err != nil {
		return err
	}
	if outbox != nil {
		if err := bindArchiveRecordToFinalizationVersion(record, job.IntentToken); err != nil {
			return fmt.Errorf("bind immutable archive publication version: %w", err)
		}
	}
	if job.PersistArtifacts {
		if err := tr.persistTestManifestArtifact(test, record); err != nil {
			return fmt.Errorf("persist archive adjunct artifacts: %w", err)
		}
	}
	if outbox != nil {
		tr.populateRunArchiveTrendPoint(record)
		current, err := outbox.UpdateArchiveFinalizationRecordIfToken(job.TestID, job.IntentToken, record)
		if err != nil {
			return fmt.Errorf("persist archive publication version: %w", err)
		}
		if !current {
			return tr.enqueueArchiveVersionCleanup(job.ProjectID, record)
		}
	}
	if err := publishRunArchive(ctx, record); err != nil {
		return fmt.Errorf("publish archive objects: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	if snapshotRetryNeeded {
		refreshOutbox := tr.archiveRefreshOutbox()
		if refreshOutbox == nil {
			slog.Warn("archive finalization: imported dashboard snapshot backfill is not durable with the configured store", "test", test.ID)
		} else if _, err := refreshOutbox.BumpArchiveRefreshJob(job.TestID); err != nil {
			return fmt.Errorf("enqueue imported dashboard snapshot backfill: %w", err)
		}
	}
	if outbox != nil {
		committed, err := outbox.CompleteArchiveFinalization(job.TestID, job.IntentToken, job.RefreshToken, record)
		if err != nil {
			return fmt.Errorf("commit archive finalization: %w", err)
		}
		if !committed {
			return tr.enqueueArchiveVersionCleanup(job.ProjectID, record)
		}
	} else {
		if err := tr.saveRunArchiveRecord(record); err != nil {
			return fmt.Errorf("commit archive catalog record: %w", err)
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if !tr.isExecutionLeader() {
			return errNotExecutionLeader
		}
	}
	if err := markPublishedArchiveSource(record); err != nil {
		slog.Warn("confirmed archive source retained without cleanup receipt", "test_id", job.TestID, "error", err)
	}
	tr.attachFinalizedRunArchive(job.TestID, record)
	return nil
}

func (tr *TestRunner) preferredArchiveFinalizationTestSnapshot(testID string, durable *storage.TestRecord, fallback *ManagedTest) *ManagedTest {
	if tr == nil {
		return fallback
	}
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	current := tr.tests[testID]
	if current == nil {
		return fallback
	}
	// The durable row and outbox establish the tenant/identity boundary. A
	// follower may still retain an object for a deleted/recreated ID; never let
	// that stale object's groups, actors, or runtime provenance cross into the
	// current tenant's archive.
	if durable != nil && !sameStoredTestIdentity(current, durable) {
		return fallback
	}
	return cloneManagedTestForReadLocked(current)
}

// processArchiveRefreshJob resolves a record-independent dirty intent under the
// publication fence. A terminal test with neither catalog nor pending archive
// was retention-pruned, so the dirty token is acknowledged without recreation.
func (tr *TestRunner) processArchiveRefreshJob(ctx context.Context, snapshot *storage.ArchiveRefreshJob) error {
	if tr == nil || snapshot == nil || strings.TrimSpace(snapshot.TestID) == "" {
		return nil
	}
	refreshOutbox := tr.archiveRefreshOutbox()
	if refreshOutbox == nil {
		return nil
	}
	unlock := tr.lockArchiveCommitFence(snapshot.TestID)
	defer unlock()
	if !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	ctx, cancelOperation := tr.archiveOperationContext(ctx)
	defer cancelOperation()
	if err := ctx.Err(); err != nil {
		return err
	}
	current, err := refreshOutbox.GetArchiveRefreshJob(snapshot.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("reload archive refresh intent: %w", err)
	}
	if current == nil || current.IntentToken != snapshot.IntentToken {
		return nil
	}

	var durableTest *storage.TestRecord
	if tr.testStore != nil {
		durableTest, err = tr.testStore.GetTest(current.TestID)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && durableTest == nil) {
			_, deleteErr := refreshOutbox.DeleteArchiveRefreshJobIfToken(current.TestID, current.IntentToken)
			return deleteErr
		}
		if err != nil {
			return fmt.Errorf("load test for archive refresh: %w", err)
		}
		if normalizeProjectID(durableTest.ProjectID) != normalizeProjectID(current.ProjectID) {
			return fmt.Errorf("archive refresh project %q does not match test project %q", current.ProjectID, durableTest.ProjectID)
		}
	}

	var record *storage.RunArchiveRecord
	if finalization := tr.archiveFinalizationOutbox(); finalization != nil {
		pending, pendingErr := finalization.GetArchiveFinalizationJob(current.TestID)
		if pendingErr == nil && pending != nil {
			copy := pending.ArchiveRecord
			record = &copy
		} else if pendingErr != nil && !errors.Is(pendingErr, sql.ErrNoRows) {
			return fmt.Errorf("load pending archive finalization for refresh: %w", pendingErr)
		}
	}
	if record == nil && tr.archiveStore != nil {
		catalogRecord, catalogErr := tr.archiveStore.GetRunArchive(current.TestID)
		if catalogErr == nil {
			record = catalogRecord
		} else if !errors.Is(catalogErr, sql.ErrNoRows) {
			return fmt.Errorf("load archive catalog for refresh: %w", catalogErr)
		}
	}
	if record == nil {
		if durableTest != nil && isTerminalTestStatus(durableTest.Status) {
			_, err := refreshOutbox.DeleteArchiveRefreshJobIfToken(current.TestID, current.IntentToken)
			return err
		}
		return nil
	}
	if durableTest != nil && !isTerminalTestStatus(durableTest.Status) {
		return nil
	}
	job, rebound, err := refreshOutbox.RebindArchiveRefreshToFinalization(current.TestID, current.IntentToken)
	if err != nil {
		return fmt.Errorf("bind archive refresh to finalization: %w", err)
	}
	if !rebound || job == nil {
		return nil
	}
	var managed *ManagedTest
	if durableTest != nil {
		managed = managedTestFromRecord(durableTest)
	}
	err = tr.processArchiveFinalizationJobUnderFence(ctx, job, managed)
	if err != nil {
		// Refresh retries bind to a finalization intent but do not pass through the
		// normal finalization worker. Annotate that intent here so bounded backfill
		// retries can eventually persist fail-soft evidence instead of looping forever.
		tr.markArchiveFinalizationFailed(job.TestID, job.IntentToken, err)
	}
	return err
}

func (tr *TestRunner) archiveOperationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if tr == nil || tr.leadership == nil {
		if parent == nil {
			parent = context.Background()
		}
		return context.WithCancel(parent)
	}
	return tr.leadership.operationContext(parent)
}

func (tr *TestRunner) cancelArchiveFinalizationJob(testID string) error {
	outbox := tr.archiveFinalizationOutbox()
	if outbox == nil {
		return nil
	}
	if err := outbox.DeleteArchiveFinalizationJob(testID); err != nil {
		return fmt.Errorf("cancel archive finalization for deleted test: %w", err)
	}
	return nil
}

func (tr *TestRunner) markArchiveFinalizationFailed(testID string, intentToken string, finalizationErr error) {
	outbox := tr.archiveFinalizationOutbox()
	if outbox == nil {
		return
	}
	if err := outbox.MarkArchiveFinalizationJobFailedIfToken(testID, intentToken, finalizationErr, time.Now().UTC()); err != nil && !errors.Is(err, sql.ErrNoRows) {
		slog.Warn("failed to annotate archive finalization outbox", "test_id", testID, "finalization_error", finalizationErr, "error", err)
	}
}

func (tr *TestRunner) attachFinalizedRunArchive(testID string, record *storage.RunArchiveRecord) {
	if tr == nil || record == nil {
		return
	}
	archived := *record
	tr.mu.Lock()
	defer tr.mu.Unlock()
	current := tr.tests[testID]
	if current == nil {
		return
	}
	tr.archives[testID] = &archived
	attached := archived
	current.Archive = &attached
}

// publishRunArchive uploads all object-backed artifacts before manifest.json.
// The persisted URI selects the original namespace on retry; current settings
// contribute credentials and endpoint configuration but cannot redirect it.
func publishRunArchive(ctx context.Context, record *storage.RunArchiveRecord) error {
	if record == nil {
		return nil
	}
	if err := validateArchivePublicationPaths(record); err != nil {
		return err
	}
	if archiveRecordBackend(record) == archiveBackendFilesystem {
		if _, err := os.Stat(record.ManifestPath); err != nil {
			return fmt.Errorf("stat local archive manifest: %w", err)
		}
		return nil
	}
	backend, err := archiveMaterializationRuntime(currentArchiveBackend(), record)
	if err != nil {
		return err
	}
	names, err := archivePublicationArtifactNames(ctx, record)
	if err != nil {
		return err
	}
	bucket, err := backend.openBucket(ctx)
	if err != nil {
		return err
	}
	defer bucket.Close()

	return uploadArchiveObjectsToBucket(ctx, bucket, backend, record.ArchivePath, record.TestID, names...)
}

func archivePublicationArtifactNames(ctx context.Context, record *storage.RunArchiveRecord) ([]string, error) {
	if record == nil {
		return nil, fmt.Errorf("archive record is required")
	}
	manifestData, err := readLocalFileLimitedContext(ctx, record.ManifestPath, maxArchiveManifestDownloadBytes)
	if err != nil {
		return nil, fmt.Errorf("read archive publication manifest: %w", err)
	}
	if expected := strings.TrimSpace(record.ManifestSHA256); expected != "" && !strings.EqualFold(sha256BytesHex(manifestData), expected) {
		return nil, fmt.Errorf("local archive manifest SHA-256 does not match persisted finalization metadata")
	}
	_, names, err := decodeMaterializationManifest(record, manifestData)
	if err != nil {
		return nil, fmt.Errorf("validate archive publication manifest: %w", err)
	}
	names = append(names, runArchiveManifestFileName)
	return names, nil
}

// stageMaterializedArchiveForPublication makes a token-private writable copy of
// a downloaded archive. Materialization cache entries are immutable and may
// have concurrent readers; an adjunct refresh must never edit that shared cache
// in place.
func stageMaterializedArchiveForPublication(ctx context.Context, source *storage.RunArchiveRecord, intentToken string) (string, string, error) {
	if source == nil {
		return "", "", fmt.Errorf("source archive record is required")
	}
	if err := validateArchiveIntentToken(intentToken); err != nil {
		return "", "", err
	}
	names, err := archivePublicationArtifactNames(ctx, source)
	if err != nil {
		return "", "", err
	}
	publicationRoot := filepath.Join(resolveArchiveRoot(), "_publication", intentToken, source.TestID)
	targetRoot, err := resolveArchiveDirectoryRoot(publicationRoot, true)
	if err != nil {
		return "", "", fmt.Errorf("prepare private publication directory: %w", err)
	}
	targetHandle, err := openArchiveDirectoryNoSymlink(targetRoot)
	if err != nil {
		return "", "", err
	}
	if err := targetHandle.Remove(runArchiveManifestFileName); err != nil && !os.IsNotExist(err) {
		_ = targetHandle.Close()
		return "", "", fmt.Errorf("clear prior publication commit marker: %w", err)
	}
	if err := targetHandle.Close(); err != nil {
		return "", "", err
	}

	for _, name := range names {
		if err := ctx.Err(); err != nil {
			return "", "", err
		}
		sourceFile, _, err := openArchiveArtifactForRead(source.ArchivePath, name)
		if err != nil {
			return "", "", fmt.Errorf("open source archive artifact %s: %w", name, err)
		}
		destination, err := replaceArchiveArtifactForWrite(targetRoot, name, 0o644, true)
		if err != nil {
			_ = sourceFile.Close()
			return "", "", fmt.Errorf("create private archive artifact %s: %w", name, err)
		}
		_, copyErr := copyArchiveArtifactContext(ctx, destination, sourceFile)
		sourceCloseErr := sourceFile.Close()
		if copyErr == nil {
			copyErr = destination.Sync()
		}
		destinationCloseErr := destination.Close()
		if copyErr != nil {
			return "", "", fmt.Errorf("copy private archive artifact %s: %w", name, copyErr)
		}
		if sourceCloseErr != nil {
			return "", "", sourceCloseErr
		}
		if destinationCloseErr != nil {
			return "", "", destinationCloseErr
		}
	}
	if err := syncArchiveDirectory(targetRoot); err != nil {
		return "", "", err
	}
	return targetRoot, filepath.Join(targetRoot, runArchiveManifestFileName), nil
}

func copyArchiveArtifactContext(ctx context.Context, destination io.Writer, source io.Reader) (int64, error) {
	buffer := make([]byte, 64<<10)
	var copied int64
	for {
		if err := ctx.Err(); err != nil {
			return copied, err
		}
		read, readErr := source.Read(buffer)
		if read > 0 {
			written, writeErr := destination.Write(buffer[:read])
			copied += int64(written)
			if writeErr != nil {
				return copied, writeErr
			}
			if written != read {
				return copied, io.ErrShortWrite
			}
		}
		if readErr == io.EOF {
			return copied, nil
		}
		if readErr != nil {
			return copied, readErr
		}
	}
}

func validateArchivePublicationPaths(record *storage.RunArchiveRecord) error {
	if record == nil {
		return fmt.Errorf("archive record is required")
	}
	testID := strings.TrimSpace(record.TestID)
	if err := validateArchiveRunID(testID); err != nil {
		return err
	}
	archivePath := strings.TrimSpace(record.ArchivePath)
	manifestPath := strings.TrimSpace(record.ManifestPath)
	if archivePath == "" || manifestPath == "" {
		return fmt.Errorf("archive and manifest paths are required")
	}
	absArchive, err := filepath.Abs(archivePath)
	if err != nil {
		return fmt.Errorf("resolve archive path: %w", err)
	}
	absManifest, err := filepath.Abs(manifestPath)
	if err != nil {
		return fmt.Errorf("resolve archive manifest path: %w", err)
	}
	if filepath.Clean(absManifest) != filepath.Join(filepath.Clean(absArchive), runArchiveManifestFileName) {
		return fmt.Errorf("archive manifest path is not the run manifest in archive root")
	}
	if filepath.Base(filepath.Clean(absArchive)) != testID {
		return fmt.Errorf("archive path is not scoped to test %q", testID)
	}
	archiveInfo, err := os.Lstat(absArchive)
	if err != nil {
		return fmt.Errorf("inspect archive path: %w", err)
	}
	if !archiveInfo.IsDir() || archiveInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archive path is not a non-symlink directory")
	}
	manifestInfo, err := os.Lstat(absManifest)
	if err != nil {
		return fmt.Errorf("inspect archive manifest: %w", err)
	}
	if !manifestInfo.Mode().IsRegular() || manifestInfo.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("archive manifest is not a regular file")
	}
	resolvedArchive, err := filepath.EvalSymlinks(absArchive)
	if err != nil {
		return fmt.Errorf("resolve archive directory links: %w", err)
	}
	allowedRoots := []string{resolveFilesystemArchiveRoot(), resolveArchiveRoot()}
	if stagingRoot := strings.TrimSpace(os.Getenv("APEXLOAD_ARCHIVE_STAGING_DIR")); stagingRoot != "" {
		allowedRoots = append(allowedRoots, stagingRoot)
	}
	for _, root := range allowedRoots {
		absRoot, err := filepath.Abs(strings.TrimSpace(root))
		if err != nil {
			continue
		}
		resolvedRoot := absRoot
		if candidate, err := filepath.EvalSymlinks(absRoot); err == nil {
			resolvedRoot = candidate
		}
		relative, err := filepath.Rel(resolvedRoot, resolvedArchive)
		if err == nil && relative != "." && relative != "" && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return nil
		}
	}
	return fmt.Errorf("archive path %q is outside configured archive roots", archivePath)
}

func (s *Server) retryArchiveFinalizationJobs(ctx context.Context) error {
	if s == nil || s.runner == nil {
		return nil
	}
	outbox := s.runner.archiveFinalizationOutbox()
	if outbox == nil {
		return nil
	}
	jobs, err := outbox.ListArchiveFinalizationJobs(1000)
	if err != nil {
		return fmt.Errorf("list archive finalization jobs: %w", err)
	}
	failures := make([]error, 0)
	for _, job := range jobs {
		if job == nil || strings.TrimSpace(job.TestID) == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, archiveFinalizationAttemptTimeout)
		err := s.runner.processArchiveFinalizationJob(attemptCtx, job)
		cancel()
		if err != nil {
			s.runner.markArchiveFinalizationFailed(job.TestID, job.IntentToken, err)
			failures = append(failures, fmt.Errorf("%s: %w", job.TestID, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Server) retryArchiveRefreshJobs(ctx context.Context) error {
	if s == nil || s.runner == nil {
		return nil
	}
	outbox := s.runner.archiveRefreshOutbox()
	if outbox == nil {
		return nil
	}
	jobs, err := outbox.ListArchiveRefreshJobs(1000)
	if err != nil {
		return fmt.Errorf("list archive refresh jobs: %w", err)
	}
	failures := make([]error, 0)
	for _, job := range jobs {
		if job == nil || strings.TrimSpace(job.TestID) == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return errors.Join(append(failures, err)...)
		}
		attemptCtx, cancel := context.WithTimeout(ctx, archiveFinalizationAttemptTimeout)
		err := s.runner.processArchiveRefreshJob(attemptCtx, job)
		cancel()
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", job.TestID, err))
		}
	}
	return errors.Join(failures...)
}

func (s *Server) startArchiveFinalizationRetry() {
	if s == nil || s.runner == nil || s.runner.archiveFinalizationOutbox() == nil || s.archiveFinalizationCancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.archiveFinalizationCancel = cancel
	s.archiveFinalizationDone = make(chan struct{})
	go func() {
		defer close(s.archiveFinalizationDone)
		ticker := time.NewTicker(archiveFinalizationRetryInterval)
		defer ticker.Stop()
		runSweep := func(recoverOrphans bool) {
			// Confirmed local sources can be retired on followers too. The file fence
			// protects shared staging writers independently of execution leadership.
			sourceCtx, sourceCancel := context.WithTimeout(ctx, 20*time.Second)
			sourceErr := s.runner.retirePublishedArchiveSources(sourceCtx)
			sourceCancel()
			if sourceErr != nil && ctx.Err() == nil {
				slog.Warn("archive source retirement incomplete", "error", sourceErr)
			}
			if !s.isExecutionLeader() {
				return
			}
			sweepCtx, sweepCancel := context.WithTimeout(ctx, archiveFinalizationAttemptTimeout)
			defer sweepCancel()
			// Durable intents are latency-sensitive. Process them before the bounded
			// filesystem recovery scan so a large archive root cannot delay known jobs.
			err := errors.Join(s.retryArchiveFinalizationJobs(sweepCtx), s.retryArchiveRefreshJobs(sweepCtx), s.retryConfluencePublications(sweepCtx))
			if recoverOrphans && sweepCtx.Err() == nil {
				err = errors.Join(err, s.runner.recoverOrphanedLocalRunArchives(sweepCtx))
			}
			if err != nil && ctx.Err() == nil {
				slog.Warn("archive finalization outbox retry incomplete", "error", err)
			}
		}
		runSweep(true)
		for {
			select {
			case <-ctx.Done():
				return
			case <-s.archiveFinalizationWake:
				runSweep(true)
			case <-ticker.C:
				runSweep(false)
			}
		}
	}()
}

func (s *Server) wakeArchiveFinalizationRetry() {
	if s == nil || s.archiveFinalizationWake == nil {
		return
	}
	select {
	case s.archiveFinalizationWake <- struct{}{}:
	default:
	}
}
