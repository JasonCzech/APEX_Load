package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestStoredObjectArchiveCatalogDoesNotDownloadAtStartup(t *testing.T) {
	for _, mode := range []string{"available", "unavailable", "corrupt"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Startup-Test")
			t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
			t.Setenv(envArchiveMaterializeTimeout, "250ms")
			t.Setenv("AWS_ACCESS_KEY_ID", "test")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
			t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
			now := time.Now().UTC()
			manifest, err := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "startup-archive", CreatedAt: now, CompletedAt: now, Status: "COMPLETE"})
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int64
			var available atomic.Bool
			available.Store(mode != "unavailable")
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if !available.Load() {
					http.Error(w, "unavailable", http.StatusServiceUnavailable)
					return
				}
				if mode == "corrupt" {
					_, _ = w.Write([]byte(`{"test_id":"other-run"}`))
					return
				}
				_, _ = w.Write(manifest)
			}))
			defer server.Close()
			t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", server.URL)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.SaveTest(&storage.TestRecord{ID: "startup-archive", Name: "Startup", ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: now, EndTime: &now}); err != nil {
				t.Fatal(err)
			}
			backend := currentArchiveBackend()
			record := &storage.RunArchiveRecord{TestID: "startup-archive", ArchiveBackend: archiveBackendS3, ArchiveURI: backend.runURI("startup-archive"), ManifestURI: backend.manifestURI("startup-archive"), ManifestSHA256: sha256BytesHex(manifest), CreatedAt: now, CompletedAt: now}
			if err := store.SaveRunArchive(record); err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, NewHub(), store)
			if requests.Load() != 0 {
				t.Fatalf("startup made %d object requests", requests.Load())
			}
			if test := runner.tests[record.TestID]; test == nil || test.Archive == nil {
				t.Fatal("durable object catalog was lost at startup")
			}
			if _, err := os.Stat(archiveMaterializationCacheScanRoot("")); !os.IsNotExist(err) {
				t.Fatalf("startup created download staging: %v", err)
			}
			copy := *record
			if got := runArchiveRecordAvailableContext(context.Background(), &copy); got != (mode == "available") {
				t.Fatalf("read availability = %v for %s", got, mode)
			}
			if requests.Load() == 0 {
				t.Fatal("explicit archive read bypassed remote verification")
			}
			if mode == "unavailable" {
				available.Store(true)
				copy = *record
				if !runArchiveRecordAvailableContext(context.Background(), &copy) {
					t.Fatal("archive did not recover when storage returned")
				}
			}
		})
	}
}

func TestStoredArchiveCatalogRejectsMalformedObjectAndMissingFilesystem(t *testing.T) {
	for _, backend := range []string{archiveBackendS3, archiveBackendFilesystem} {
		t.Run(backend, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", backend)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			test, record := createArchiveFinalizationTest(t, store, "invalid-catalog", resolveArchiveRoot())
			record.ArchiveBackend = backend
			if backend == archiveBackendFilesystem {
				if err := os.Remove(record.ManifestPath); err != nil {
					t.Fatal(err)
				}
			} else {
				record.ArchiveURI = "https://untrusted.invalid/archive"
			}
			if err := store.SaveRunArchive(record); err != nil {
				t.Fatal(err)
			}
			runner := NewTestRunner(1, NewHub(), store)
			if runner.tests[test.ID].Archive != nil {
				t.Fatal("unusable catalog attached")
			}
		})
	}
}
