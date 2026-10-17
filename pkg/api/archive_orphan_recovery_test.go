package api

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type blockingFinalizationListingStore struct {
	SQLiteStore *storage.SQLiteStore
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
}

type cancelFirstOrphanReadStore struct {
	SQLiteStore *storage.SQLiteStore
	cancel      context.CancelFunc
	once        sync.Once
}

type cancelRepeatedSlowOrphanStore struct {
	SQLiteStore *storage.SQLiteStore
	cancel      context.CancelFunc
}

func TestDiscoverOrphanArchiveDirectoriesExactEntryLimit(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"a", "b"} {
		if err := os.WriteFile(filepath.Join(root, name), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := discoverOrphanArchiveDirectoriesWithLimit(context.Background(), root, 2); err != nil {
		t.Fatalf("exact entry limit was rejected: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "c"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := discoverOrphanArchiveDirectoriesWithLimit(context.Background(), root, 2); err == nil {
		t.Fatal("entry beyond the limit was accepted")
	}
}

func (s *cancelRepeatedSlowOrphanStore) GetTest(id string) (*storage.TestRecord, error) {
	row, err := s.SQLiteStore.GetTest(id)
	if id == "orphan-a" && s.cancel != nil {
		s.cancel()
	}
	return row, err
}

func TestOrphanRecoveryRotatesRepeatedlySlowCandidate(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for _, id := range []string{"orphan-a", "orphan-b"} {
		saveTerminalOrphanTest(t, base, id, storage.DefaultProjectID)
		writeOrphanArchiveFixture(t, root, id, id, storage.DefaultProjectID)
	}
	store := &cancelRepeatedSlowOrphanStore{SQLiteStore: base}
	runner := NewTestRunner(1, nil, sqliteOverlay(store))
	for sweep := 0; sweep < 2; sweep++ {
		ctx, cancel := context.WithCancel(context.Background())
		store.cancel = cancel
		if err := runner.recoverOrphanedLocalRunArchives(ctx); !errors.Is(err, context.Canceled) {
			t.Fatalf("sweep %d error = %v, want canceled", sweep, err)
		}
	}
	if _, err := base.GetArchiveFinalizationJob("orphan-b"); err != nil {
		t.Fatalf("later orphan was starved by repeated slow first candidate: %v", err)
	}
	if _, err := base.GetArchiveFinalizationJob("orphan-a"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("slow candidate unexpectedly finalized: %v", err)
	}
}

func (s *cancelFirstOrphanReadStore) GetTest(id string) (*storage.TestRecord, error) {
	row, err := s.SQLiteStore.GetTest(id)
	s.once.Do(s.cancel)
	return row, err
}

func TestOrphanRecoveryRetainsUnprocessedCandidatesAfterDeadline(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	for _, id := range []string{"orphan-a", "orphan-b"} {
		saveTerminalOrphanTest(t, base, id, storage.DefaultProjectID)
		writeOrphanArchiveFixture(t, root, id, id, storage.DefaultProjectID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	store := &cancelFirstOrphanReadStore{SQLiteStore: base, cancel: cancel}
	runner := NewTestRunner(1, nil, sqliteOverlay(store))
	if err := runner.recoverOrphanedLocalRunArchives(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("first sweep error = %v, want canceled", err)
	}
	if runner.orphanArchiveScan == nil || len(runner.orphanArchiveScan.retryCandidates) != 2 || filepath.Base(runner.orphanArchiveScan.retryCandidates[0].path) != "orphan-b" {
		t.Fatalf("unprocessed candidates lost: scan=%+v", runner.orphanArchiveScan)
	}
	if err := runner.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"orphan-a", "orphan-b"} {
		if _, err := base.GetArchiveFinalizationJob(id); err != nil {
			t.Fatalf("orphan %s was starved after deadline: %v", id, err)
		}
	}
}

func (s *blockingFinalizationListingStore) ListArchiveFinalizationJobs(limit int) ([]*storage.ArchiveFinalizationJob, error) {
	s.once.Do(func() { close(s.entered) })
	<-s.release
	return s.SQLiteStore.ListArchiveFinalizationJobs(limit)
}

func TestOrphanRecoveryRunsWhileKnownFinalizationListingIsBlocked(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	base, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	store := &blockingFinalizationListingStore{SQLiteStore: base, entered: make(chan struct{}), release: make(chan struct{})}
	saveTerminalOrphanTest(t, sqliteOverlay(store), "independent-orphan", storage.DefaultProjectID)
	writeOrphanArchiveFixture(t, root, "independent-orphan", "independent-orphan", storage.DefaultProjectID)
	runner := NewTestRunner(1, nil, sqliteOverlay(store))
	server := &Server{runner: runner, archiveFinalizationWake: make(chan struct{}, 1), archiveOrphanWake: make(chan struct{}, 1)}
	server.startArchiveFinalizationRetry()
	defer func() {
		close(store.release)
		server.archiveFinalizationCancel()
		select {
		case <-server.archiveFinalizationDone:
		case <-time.After(2 * time.Second):
			t.Error("archive workers did not stop")
		}
	}()
	select {
	case <-store.entered:
	case <-time.After(2 * time.Second):
		t.Fatal("known finalization worker did not reach the blocking list")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := store.SQLiteStore.GetArchiveFinalizationJob("independent-orphan"); err == nil {
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			t.Fatal(err)
		}
		if time.Now().After(deadline) {
			t.Fatal("blocked known jobs starved independent orphan recovery")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func writeOrphanArchiveFixture(t *testing.T, root, testID, manifestTestID, projectID string) *storage.RunArchiveRecord {
	t.Helper()
	archivePath := filepath.Join(root, testID)
	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion,
		TestID:        manifestTestID,
		TestName:      testID,
		CreatedAt:     now.Add(-time.Minute),
		CompletedAt:   now,
		Status:        "COMPLETE",
		Provenance:    RunArchiveProvenance{ProjectID: projectID},
	}
	manifestPath := filepath.Join(archivePath, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	return &storage.RunArchiveRecord{TestID: testID, ArchivePath: archivePath, ManifestPath: manifestPath}
}

func saveTerminalOrphanTest(t *testing.T, store storage.Store, testID, projectID string) {
	t.Helper()
	now := time.Date(2026, 9, 28, 0, 0, 0, 0, time.UTC)
	if err := store.SaveTest(&storage.TestRecord{
		ID:        testID,
		RefKey:    "RUN-" + testID,
		Name:      testID,
		Status:    "COMPLETE",
		ProjectID: projectID,
		StartTime: now.Add(-time.Minute),
		EndTime:   &now,
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverOrphanedLocalRunArchivesQueuesAndFinalizes(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const testID = "orphan-run"
	saveTerminalOrphanTest(t, store, testID, storage.DefaultProjectID)
	writeOrphanArchiveFixture(t, archiveRoot, testID, testID, storage.DefaultProjectID)
	runner := NewTestRunner(1, NewHub(), store)

	if err := runner.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	job, err := store.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if job.Generation != 1 || !job.PersistArtifacts || job.ArchiveRecord.RefKey != "RUN-"+testID {
		t.Fatalf("recovered job = %+v", job)
	}

	server := &Server{runner: runner, store: store}
	if err := server.retryArchiveFinalizationJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	record, err := store.GetRunArchive(testID)
	if err != nil {
		t.Fatal(err)
	}
	if record.TestID != testID || record.ManifestSHA256 == "" {
		t.Fatalf("catalog record = %+v", record)
	}
	if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("finalization job remained after success: %v", err)
	}
}

func TestRecoverOrphanedLocalRunArchivesDoesNotOverwriteOrResurrect(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const pendingID = "pending-refresh"
	saveTerminalOrphanTest(t, store, pendingID, storage.DefaultProjectID)
	pendingRecord := writeOrphanArchiveFixture(t, archiveRoot, pendingID, pendingID, storage.DefaultProjectID)
	if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{
		TestID: pendingID, ProjectID: storage.DefaultProjectID,
		ArchiveRecord:    storage.RunArchiveRecord{TestID: pendingID, RefKey: "newer-refresh"},
		PersistArtifacts: true,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := store.GetArchiveFinalizationJob(pendingID)
	if err != nil {
		t.Fatal(err)
	}

	const deletedID = "deleted-orphan"
	writeOrphanArchiveFixture(t, archiveRoot, deletedID, deletedID, storage.DefaultProjectID)
	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.recoverOrphanedLocalRunArchives(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := store.GetArchiveFinalizationJob(pendingID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Generation != before.Generation || after.ArchiveRecord.RefKey != "newer-refresh" {
		t.Fatalf("recovery overwrote newer job: before=%+v after=%+v candidate=%+v", before, after, pendingRecord)
	}
	if _, err := store.GetArchiveFinalizationJob(deletedID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted test was resurrected into outbox: %v", err)
	}
}

func TestRecoverOrphanedLocalRunArchivesRejectsMismatchedAndSymlinkedCandidates(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	const mismatchedID = "mismatched-orphan"
	saveTerminalOrphanTest(t, store, mismatchedID, storage.DefaultProjectID)
	writeOrphanArchiveFixture(t, archiveRoot, mismatchedID, "different-run", storage.DefaultProjectID)

	outside := t.TempDir()
	writeOrphanArchiveFixture(t, outside, "symlinked-orphan", "symlinked-orphan", storage.DefaultProjectID)
	if err := os.Symlink(filepath.Join(outside, "symlinked-orphan"), filepath.Join(archiveRoot, "symlinked-orphan")); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.recoverOrphanedLocalRunArchives(context.Background()); err == nil {
		t.Fatal("expected mismatched manifest recovery error")
	}
	if _, err := store.GetArchiveFinalizationJob(mismatchedID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("mismatched archive was queued: %v", err)
	}
	if _, err := store.GetArchiveFinalizationJob("symlinked-orphan"); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("symlinked archive was queued: %v", err)
	}
}
