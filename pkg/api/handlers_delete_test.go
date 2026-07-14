package api

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/refkey"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHandleDeleteTestByRefKeyRemovesPhysicalArchive(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatalf("NewSQLiteStore returned error: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()
	testID := "2d332bd8-1bd1-47f9-a255-6ef6be6f1d13"
	refKey := refkey.Run("DEV", now, 1)
	archivePath := filepath.Join(archiveRoot, testID)
	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		t.Fatalf("MkdirAll archive: %v", err)
	}
	if err := os.WriteFile(filepath.Join(archivePath, "transactions.jsonl"), []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile archive: %v", err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: testID, RefKey: refKey, Name: "ref-key-delete", Status: "COMPLETE", ProjectID: storage.DefaultProjectID, StartTime: now}); err != nil {
		t.Fatalf("SaveTest returned error: %v", err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{TestID: testID, RefKey: refKey, SchemaVersion: archiveSchemaVersion, ArchiveBackend: archiveBackendFilesystem, ArchivePath: archivePath, CreatedAt: now, CompletedAt: now}); err != nil {
		t.Fatalf("SaveRunArchive returned error: %v", err)
	}

	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 1})
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/"+refKey, nil)
	resp := httptest.NewRecorder()
	server.mux.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive directory still exists after ref-key delete: %v", err)
	}
	if _, err := store.GetTest(testID); err == nil {
		t.Fatal("test row still exists after ref-key delete")
	}
	if _, err := store.GetArchiveCleanupJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("completed deletion left cleanup job: %v", err)
	}
}

func TestRetryArchiveCleanupJobsCompletesDeletionAfterMetadataCommit(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	now := time.Now().UTC()
	testID := "retry-archive-cleanup"
	archivePath := filepath.Join(archiveRoot, testID)
	if err := os.MkdirAll(archivePath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(archivePath, "manifest.json"), []byte(`{}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveTest(&storage.TestRecord{ID: testID, Name: testID, Status: "COMPLETE", ProjectID: storage.DefaultProjectID, StartTime: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(&storage.RunArchiveRecord{
		TestID: testID, SchemaVersion: archiveSchemaVersion, ArchiveBackend: archiveBackendFilesystem,
		ArchivePath: archivePath, ManifestPath: filepath.Join(archivePath, "manifest.json"), CreatedAt: now, CompletedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate a process stopping after the durable metadata transaction and
	// before physical cleanup. The serialized outbox record is all the next
	// controller instance needs to finish the operation.
	if err := store.DeleteTest(testID); err != nil {
		t.Fatal(err)
	}
	if _, err := store.GetArchiveCleanupJob(testID); err != nil {
		t.Fatalf("cleanup job not persisted: %v", err)
	}

	server := NewServer(ServerConfig{Store: store})
	if err := server.retryArchiveCleanupJobs(context.Background()); err != nil {
		t.Fatalf("retryArchiveCleanupJobs returned error: %v", err)
	}
	if _, err := os.Stat(archivePath); !os.IsNotExist(err) {
		t.Fatalf("archive remains after retry: %v", err)
	}
	if _, err := store.GetArchiveCleanupJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("cleanup job remains after retry: %v", err)
	}
}

func TestHandleDeleteTestWaitsForArchiveFinalizationFence(t *testing.T) {
	archiveRoot := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", archiveRoot)
	sqliteStore, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "apexload.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqliteStore.Close()
	store := &failingArchiveCatalogStore{SQLiteStore: sqliteStore, failCatalog: true}
	testID := "delete-races-finalization"
	test, record := createArchiveFinalizationTest(t, store, testID, archiveRoot)
	server := NewServer(ServerConfig{Store: store, MaxConcurrent: 1})

	// Simulate the producer's existing fence spanning local finalization and the
	// terminal transition. The HTTP handler must wait rather than deleting the
	// test between outbox creation and publication/catalog commit.
	unlock := server.runner.lockArchiveCommitFence(testID)
	requestStarted := make(chan struct{})
	requestDone := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		close(requestStarted)
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/tests/"+testID, nil)
		resp := httptest.NewRecorder()
		server.mux.ServeHTTP(resp, req)
		requestDone <- resp
	}()
	<-requestStarted
	select {
	case resp := <-requestDone:
		unlock()
		t.Fatalf("delete bypassed archive finalization fence: status=%d body=%s", resp.Code, resp.Body.String())
	case <-time.After(50 * time.Millisecond):
	}
	if err := server.runner.finalizeRunArchiveDurablyUnderFence(context.Background(), test, record, false); err == nil {
		unlock()
		t.Fatal("expected injected catalog failure")
	}
	unlock()

	var resp *httptest.ResponseRecorder
	select {
	case resp = <-requestDone:
	case <-time.After(5 * time.Second):
		t.Fatal("delete did not resume after archive finalization fence release")
	}
	if resp.Code != http.StatusOK {
		t.Fatalf("delete status = %d, want 200 body=%s", resp.Code, resp.Body.String())
	}
	if _, err := store.GetTest(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("test row remains after fenced delete: %v", err)
	}
	if _, err := store.GetArchiveFinalizationJob(testID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("finalization job remains after fenced delete: %v", err)
	}
	if _, err := store.GetArchiveCleanupJob(testID); err != nil {
		t.Fatalf("finalization job was not converted to durable cleanup: %v", err)
	}
	if err := server.retryArchiveCleanupJobs(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(record.ArchivePath); !os.IsNotExist(err) {
		t.Fatalf("archive remains after durable cleanup: %v", err)
	}
}
