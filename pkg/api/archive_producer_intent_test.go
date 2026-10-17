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

func TestProducerArchiveRetryPreservesNewerDurableManifest(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	defer sqlite.Close()
	store := &failingArchiveCatalogStore{SQLiteStore: sqlite, failCatalog: true}
	test, record := createArchiveFinalizationTest(t, sqliteOverlay(store), "producer-retry", root)
	runner := NewTestRunner(1, NewHub(), sqliteOverlay(store))
	if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err == nil {
		t.Fatal("catalog failure ignored")
	}
	stale := *record
	job, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	token := job.IntentToken
	// The background worker changes adjunct timestamps and the manifest digest,
	// persisting that new trust anchor before another unsuccessful publication.
	if err := runner.processArchiveFinalizationJob(context.Background(), job); err == nil {
		t.Fatal("catalog failure ignored")
	}
	newer, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	if newer.ArchiveRecord.ManifestSHA256 == stale.ManifestSHA256 {
		t.Fatal("regression did not create stale producer metadata")
	}
	store.failCatalog = false
	if err := runner.finalizeRunArchiveDurably(context.Background(), test, &stale, true); err != nil {
		t.Fatalf("stale producer broke durable retry: %v", err)
	}
	if newer.IntentToken != token {
		t.Fatal("background retry changed intent")
	}
	catalog, err := store.SQLiteStore.GetRunArchive(test.ID)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := os.ReadFile(stale.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.ManifestSHA256 != sha256BytesHex(manifest) || stale.ManifestSHA256 != catalog.ManifestSHA256 {
		t.Fatal("current trust anchor was lost")
	}
	if _, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("pending job after success: %v", err)
	}
	// A still older producer retry after publication must return the committed
	// record without recreating an outbox or modifying the local manifest again.
	before := catalog.ManifestSHA256
	if err := runner.finalizeRunArchiveDurably(context.Background(), test, record, true); err != nil {
		t.Fatal(err)
	}
	if record.ManifestSHA256 != before {
		t.Fatal("completed catalog was overwritten")
	}
	if _, err := store.SQLiteStore.GetArchiveFinalizationJob(test.ID); !errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("completed intent recreated: %v", err)
	}
}

func TestProducerArchiveIntentRejectsDifferentIncarnation(t *testing.T) {
	for _, published := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "catalog"}[published], func(t *testing.T) {
			root := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			test, record := createArchiveFinalizationTest(t, store, "reused-run", root)
			runner := NewTestRunner(1, NewHub(), store)
			original := &storage.ArchiveFinalizationJob{TestID: test.ID, ProjectID: test.ProjectID, ArchiveRecord: *record}
			if published {
				err = store.SaveRunArchive(record)
			} else {
				err = store.SaveArchiveFinalizationJob(original)
			}
			if err != nil {
				t.Fatal(err)
			}
			different := *record
			different.CreatedAt = different.CreatedAt.Add(time.Second)
			if err := runner.finalizeRunArchiveDurably(context.Background(), test, &different, true); err == nil {
				t.Fatal("different incarnation accepted")
			}
			if !published {
				current, err := store.GetArchiveFinalizationJob(test.ID)
				if err != nil || current.IntentToken != original.IntentToken {
					t.Fatalf("existing intent overwritten: %v", err)
				}
			}
		})
	}
}
