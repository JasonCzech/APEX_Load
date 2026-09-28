package api

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

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
