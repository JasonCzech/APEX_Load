package api

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

type failingArchiveCatalogStore struct {
	*storage.SQLiteStore
	failCatalog bool
}

func (s *failingArchiveCatalogStore) SaveRunArchive(record *storage.RunArchiveRecord) error {
	if s.failCatalog {
		return errors.New("injected archive catalog failure")
	}
	return s.SQLiteStore.SaveRunArchive(record)
}

type archiveIntentOrderStore struct {
	*storage.SQLiteStore
	adjunctPath string
	mu          sync.Mutex
	orderErr    error
}

func (s *archiveIntentOrderStore) SaveArchiveFinalizationJob(job *storage.ArchiveFinalizationJob) error {
	s.mu.Lock()
	if _, err := os.Stat(s.adjunctPath); err == nil {
		s.orderErr = errors.New("adjunct existed before archive finalization intent")
	} else if !os.IsNotExist(err) {
		s.orderErr = err
	}
	s.mu.Unlock()
	return s.SQLiteStore.SaveArchiveFinalizationJob(job)
}

func (s *archiveIntentOrderStore) observedOrderError() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.orderErr
}

func createArchiveFinalizationTest(t *testing.T, store storage.Store, testID string, archiveRoot string) (*ManagedTest, *storage.RunArchiveRecord) {
	t.Helper()
	now := time.Now().UTC()
	if err := store.SaveTest(&storage.TestRecord{
		ID: testID, Name: testID, Status: "COMPLETE", ProjectID: storage.DefaultProjectID,
		StartTime: now, EndTime: &now,
	}); err != nil {
		t.Fatal(err)
	}
	archivePath := filepath.Join(archiveRoot, testID)
	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion,
		TestID:        testID,
		TestName:      testID,
		Status:        "COMPLETE",
		CreatedAt:     now,
		CompletedAt:   now,
	}
	manifestPath := filepath.Join(archivePath, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(manifestPath, manifest); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{
		TestID:         testID,
		SchemaVersion:  archiveSchemaVersion,
		ArchiveBackend: archiveBackendFilesystem,
		ArchivePath:    archivePath,
		ManifestPath:   manifestPath,
		ManifestSHA256: sha256BytesHex(data),
		CreatedAt:      now,
		CompletedAt:    now,
	}
	return &ManagedTest{
		ID: testID, Name: testID, Status: "COMPLETE", ProjectID: storage.DefaultProjectID,
		CreatedAt: now, EndedAt: &now,
	}, record
}

func TestArchiveFinalizationPersistsIntentBeforeAdjuncts(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	sqliteStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteStore.Close()
	testID := "archive-intent-before-adjunct"
	store := &archiveIntentOrderStore{
		SQLiteStore: sqliteStore,
		adjunctPath: filepath.Join(archiveRoot, testID, testRunManifestFileName),
	}
	test, record := createArchiveFinalizationTest(t, store, testID, archiveRoot)
	runner := NewTestRunner(1, NewHub(), store)

	if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err != nil {
		t.Fatal(err)
	}
	if err := store.observedOrderError(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(store.adjunctPath); err != nil {
		t.Fatalf("adjunct was not generated after intent commit: %v", err)
	}
	if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("successful finalization left outbox job: %v", err)
	}
}

func TestArchiveFinalizationRecoversCatalogFailureAfterRestart(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	dbPath := filepath.Join(t.TempDir(), "apexload.db")
	sqliteStore, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	store := &failingArchiveCatalogStore{SQLiteStore: sqliteStore, failCatalog: true}
	testID := "archive-finalization-restart"
	test, record := createArchiveFinalizationTest(t, store, testID, archiveRoot)
	runner := NewTestRunner(1, NewHub(), store)

	err = runner.finalizeRunArchiveDurably(context.Background(), test, record, true)
	if err == nil || !strings.Contains(err.Error(), "catalog") {
		t.Fatalf("finalize error = %v, want catalog failure", err)
	}
	job, err := store.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatalf("durable finalization job missing after catalog failure: %v", err)
	}
	if job.Attempts != 1 || !strings.Contains(job.LastError, "catalog") {
		t.Fatalf("failed job = %+v, want one annotated attempt", job)
	}
	if err := sqliteStore.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := storage.NewSQLiteStore(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	restarted := NewServer(ServerConfig{Store: reopened, MaxConcurrent: 1})
	if err := restarted.retryArchiveFinalizationJobs(context.Background()); err != nil {
		t.Fatalf("restart finalization sweep: %v", err)
	}
	if _, err := reopened.GetRunArchive(testID); err != nil {
		t.Fatalf("archive catalog was not recovered: %v", err)
	}
	if _, err := reopened.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("recovered finalization left outbox job: %v", err)
	}
}

func TestProducerManifestBuildDoesNotMaterializeUnpublishedObjectArchive(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "not-yet-published")
	t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", "http://127.0.0.1:1")
	archivePath := filepath.Join(t.TempDir(), "producer-local")
	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	manifestPath := filepath.Join(archivePath, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{
		SchemaVersion: archiveSchemaVersion, TestID: "producer-local", Status: "COMPLETE",
		CreatedAt: now, CompletedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	record := &storage.RunArchiveRecord{
		TestID: "producer-local", ArchiveBackend: archiveBackendS3,
		ArchivePath: archivePath, ManifestPath: manifestPath,
		ArchiveURI:  "s3://not-yet-published/apexload/archives/producer-local",
		ManifestURI: "s3://not-yet-published/apexload/archives/producer-local/manifest.json",
	}
	runner := NewTestRunner(1, NewHub(), nil)
	built, err := runner.buildTestManifestForManagedTest(&ManagedTest{
		ID: "producer-local", Name: "producer-local", Status: "RUNNING", CreatedAt: now,
	}, "producer-local", record, false)
	if err != nil {
		t.Fatal(err)
	}
	if built.Status != "COMPLETE" || built.Source != "archive" {
		t.Fatalf("producer manifest status/source = %q/%q", built.Status, built.Source)
	}
	for _, warning := range built.Warnings {
		if strings.Contains(warning, "materializ") {
			t.Fatalf("producer manifest attempted remote materialization: %q", warning)
		}
	}
}

func TestArchiveFinalizationRejectsUntrustedPersistedPathsBeforeWrites(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	testID := "outside-archive-root"
	now := time.Now().UTC()
	if err := store.SaveTest(&storage.TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: storage.DefaultProjectID, StartTime: now, EndTime: &now}); err != nil {
		t.Fatal(err)
	}
	outsidePath := filepath.Join(t.TempDir(), testID)
	if err := os.MkdirAll(outsidePath, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(outsidePath, runArchiveManifestFileName)
	if err := writeIndentedJSONFile(manifestPath, RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: testID}); err != nil {
		t.Fatal(err)
	}
	job := &storage.ArchiveFinalizationJob{
		TestID: testID, ProjectID: storage.DefaultProjectID, PersistArtifacts: true,
		ArchiveRecord: storage.RunArchiveRecord{
			TestID: testID, ArchiveBackend: archiveBackendFilesystem,
			ArchivePath: outsidePath, ManifestPath: manifestPath,
		},
	}
	if err := store.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 1})
	err = server.retryArchiveFinalizationJobs(context.Background())
	if err == nil || !strings.Contains(err.Error(), "outside configured archive roots") {
		t.Fatalf("retry error = %v, want path rejection", err)
	}
	if _, err := os.Stat(filepath.Join(outsidePath, testRunManifestFileName)); !os.IsNotExist(err) {
		t.Fatalf("untrusted path was mutated before rejection: %v", err)
	}
	loaded, err := store.GetArchiveFinalizationJob(testID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Attempts != 1 {
		t.Fatalf("path failure attempts = %d, want 1", loaded.Attempts)
	}
}

func TestArchiveFinalizationRejectsSymlinkRunDirectory(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	testID := "linked-run"
	target := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(target, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(target, runArchiveManifestFileName), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(archiveRoot, testID)
	if err := os.Symlink(target, linked); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	err := validateArchivePublicationPaths(&storage.RunArchiveRecord{
		TestID: testID, ArchivePath: linked, ManifestPath: filepath.Join(linked, runArchiveManifestFileName),
	})
	if err == nil || !strings.Contains(err.Error(), "non-symlink") {
		t.Fatalf("symlink validation error = %v", err)
	}
}

func TestArchiveFinalizationTreatsDeletedTestAsCancellation(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	testID := "deleted-before-finalization-retry"
	_, record := createArchiveFinalizationTest(t, store, testID, archiveRoot)
	job := &storage.ArchiveFinalizationJob{
		TestID: testID, ProjectID: storage.DefaultProjectID,
		ArchiveRecord: *record, PersistArtifacts: true,
	}
	if err := store.SaveArchiveFinalizationJob(job); err != nil {
		t.Fatal(err)
	}
	runner := NewTestRunner(1, NewHub(), store)
	if err := store.DeleteTest(testID); err != nil {
		t.Fatal(err)
	}
	if err := runner.processArchiveFinalizationJob(context.Background(), job); err != nil {
		t.Fatalf("deleted test should cancel finalization: %v", err)
	}
	if _, err := store.GetRunArchive(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("deleted test archive was resurrected: %v", err)
	}
}
