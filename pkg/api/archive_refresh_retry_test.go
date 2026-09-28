package api

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveRefreshRetryUsesCopiedCatalogTrustAnchor(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := &failingArchiveCatalogStore{SQLiteStore: sqlite}
	test, record := createArchiveFinalizationTest(t, store, "refresh-retry", root)
	runner := NewTestRunner(1, NewHub(), store)
	if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	refresh, err := store.BumpArchiveRefreshJob(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	store.failCatalog = true
	if err := runner.processArchiveRefreshJob(context.Background(), refresh); err == nil {
		t.Fatal("catalog failure ignored")
	}
	job, err := store.GetArchiveFinalizationJob(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if job.ArchiveRecord.ManifestSHA256 == record.ManifestSHA256 {
		t.Fatal("failed attempt did not persist a different adjunct digest")
	}
	store.failCatalog = false
	// Exercise the startup worker, which reuses the existing durable intent.
	if err := runner.processArchiveFinalizationJob(context.Background(), job); err != nil {
		t.Fatalf("refresh retry failed: %v", err)
	}
	catalog, err := store.GetRunArchive(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ArchivePath == record.ArchivePath {
		t.Fatal("refresh modified catalog source in place")
	}
	data, err := os.ReadFile(catalog.ManifestPath)
	if err != nil || sha256BytesHex(data) != catalog.ManifestSHA256 {
		t.Fatalf("refreshed manifest integrity: %v", err)
	}
	unchanged, err := os.ReadFile(record.ManifestPath)
	if err != nil || string(unchanged) != string(original) {
		t.Fatalf("catalog source was modified: %v", err)
	}
	if _, err := store.GetArchiveFinalizationJob(test.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending finalization: %v", err)
	}
	if _, err := store.GetArchiveRefreshJob(test.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending refresh: %v", err)
	}
}
