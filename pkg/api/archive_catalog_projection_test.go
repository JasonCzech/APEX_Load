package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestHATerminalReadDiscoversPublishedCatalogWithoutArtifactDownloads(t *testing.T) {
	for _, mode := range []string{"valid", "wrong project", "wrong creation", "wrong digest", "wrong test", "recreated", "recreated timestamp", "replaced catalog", "slow manifest", "slow legacy manifest", "new on reload"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Catalog-Test")
			t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
			t.Setenv("AWS_ACCESS_KEY_ID", "test")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
			t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
			now := time.Now().UTC().Truncate(time.Microsecond)
			manifest := RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: "late-catalog", Status: "COMPLETE", CreatedAt: now, CompletedAt: now, Provenance: RunArchiveProvenance{ProjectID: storage.DefaultProjectID, RequestedAt: now}, TransactionsFile: "transactions.jsonl", TransactionCount: 10}
			if mode == "wrong project" {
				manifest.Provenance.ProjectID = "another-project"
			}
			if mode == "wrong creation" {
				manifest.Provenance.RequestedAt = now.Add(-time.Hour)
			}
			if mode == "wrong test" {
				manifest.TestID = "another-run"
			}
			data, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			var manifests, artifacts atomic.Int32
			var onRead func()
			var once sync.Once
			fixtureReady := make(chan struct{})
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if filepath.Base(r.URL.Path) != "manifest.json" {
					artifacts.Add(1)
					http.Error(w, "artifact reads forbidden for test summaries", http.StatusServiceUnavailable)
					return
				}
				manifests.Add(1)
				<-fixtureReady
				if mode == "slow manifest" || mode == "slow legacy manifest" {
					<-r.Context().Done()
					return
				}
				once.Do(func() {
					if onRead != nil {
						onRead()
					}
				})
				_, _ = w.Write(data)
			}))
			defer server.Close()
			t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", server.URL)
			store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := store.SaveProject(&storage.Project{ID: "replacement-project", Name: "Replacement", CreatedAt: now, UpdatedAt: now}); err != nil {
				t.Fatal(err)
			}
			row := &storage.TestRecord{ID: "late-catalog", Name: "late catalog", ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: now, EndTime: &now}
			if err := store.SaveTest(row); err != nil {
				t.Fatal(err)
			}
			leadership := newExecutionLeadership(store, "catalog-follower")
			runner := NewTestRunner(1, NewHub(), store, WithExecutionLeadership(leadership))
			if runner.tests[row.ID].Archive != nil {
				t.Fatal("fixture already has a catalog")
			}
			backend := currentArchiveBackend()
			record := &storage.RunArchiveRecord{TestID: row.ID, ArchiveBackend: archiveBackendS3, ArchiveURI: backend.runURI(row.ID), ManifestURI: backend.manifestURI(row.ID), ManifestSHA256: sha256BytesHex(data), CreatedAt: now, CompletedAt: now, TransactionCount: 10}
			if mode == "slow legacy manifest" {
				record.ManifestSHA256 = ""
			}
			if mode == "wrong digest" {
				record.ManifestSHA256 = sha256BytesHex([]byte("different"))
			}
			if err := store.SaveRunArchive(record); err != nil {
				t.Fatal(err)
			}
			if mode == "recreated" || mode == "recreated timestamp" {
				onRead = func() {
					replacement := *row
					replacement.ProjectID = "replacement-project"
					if mode == "recreated timestamp" {
						replacement.ProjectID = row.ProjectID
						replacement.StartTime = now.Add(time.Microsecond)
					}
					if err := store.DeleteTest(row.ID); err != nil {
						t.Error(err)
						return
					}
					if err := store.SaveTest(&replacement); err != nil {
						t.Error(err)
					}
				}
			}
			if mode == "replaced catalog" {
				onRead = func() {
					replacement := *record
					replacement.ManifestSHA256 = sha256BytesHex([]byte("replaced"))
					if err := store.SaveRunArchive(&replacement); err != nil {
						t.Error(err)
					}
				}
			}
			close(fixtureReady)
			if mode == "new on reload" {
				runner.mu.Lock()
				delete(runner.tests, row.ID)
				runner.mu.Unlock()
				if err := runner.reloadStoredTests(); err != nil {
					t.Fatal(err)
				}
				if manifests.Load() != 0 || artifacts.Load() != 0 {
					t.Fatal("reload downloaded historical archive objects")
				}
			}
			for i := 0; i < 2; i++ {
				started := time.Now()
				snapshot, ok := runner.GetTestSummary(row.ID)
				if (mode == "slow manifest" || mode == "slow legacy manifest") && time.Since(started) > 1500*time.Millisecond {
					t.Fatal("summary waited beyond its metadata budget")
				}
				if !ok || (snapshot.Archive != nil) != (mode == "valid" || mode == "new on reload") {
					t.Fatalf("catalog visibility incorrect for %s: found=%v manifests=%d", mode, ok, manifests.Load())
				}
			}
			if artifacts.Load() != 0 {
				t.Fatal("test summary downloaded result artifacts")
			}
			if (mode == "valid" || mode == "new on reload") && manifests.Load() != 1 {
				t.Fatalf("immutable catalog identity was not coalesced/cached: %d reads", manifests.Load())
			}
		})
	}
}
