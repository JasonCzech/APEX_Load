package api

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func TestRecoverUploadedArchiveAfterSourceLoss(t *testing.T) {
	for _, mode := range []string{"recovered", "missing marker", "changed marker", "changed start", "changed end", "changed status", "canceled", "stale lease", "new intent", "new checksum", "wrong namespace", "new default bucket", "wrong token", "unsafe local", "unknown local file"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
			var mu sync.Mutex
			objects := map[string][]byte{}
			var requests []string
			var intercept func()
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests = append(requests, r.Method+" "+r.URL.Path)
				if r.Method == http.MethodPut {
					data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
					if err != nil {
						t.Error(err)
						http.Error(w, "read failed", 400)
						return
					}
					objects[r.URL.Path] = data
					w.Header().Set("ETag", `"test"`)
					w.WriteHeader(200)
					return
				}
				if r.Method == http.MethodGet {
					if intercept != nil {
						intercept()
						intercept = nil
					}
					data, ok := objects[r.URL.Path]
					if !ok {
						http.NotFound(w, r)
						return
					}
					_, _ = w.Write(data)
					return
				}
				http.Error(w, "unexpected method", 405)
			}))
			defer remote.Close()
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Recovery-Test")
			t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", remote.URL)
			t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
			t.Setenv("AWS_ACCESS_KEY_ID", "test")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
			t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
			sqlite, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
			if err != nil {
				t.Fatal(err)
			}
			defer sqlite.Close()
			store := &failingArchiveCatalogStore{SQLiteStore: sqlite, failCatalog: true}
			run, record := createArchiveFinalizationTest(t, store, "uploaded-source-loss", filepath.Join(resolveArchiveRoot(), "remote"))
			record.ArchiveBackend = archiveBackendS3
			record.ArchiveURI = currentArchiveBackend().runURI(run.ID)
			record.ManifestURI = currentArchiveBackend().manifestURI(run.ID)
			producer := NewTestRunner(1, NewHub(), store)
			if err := producer.finalizeRunArchiveDurably(context.Background(), run, record, true); err == nil || !strings.Contains(err.Error(), "injected archive catalog failure") {
				t.Fatalf("upload/catalog crash=%v", err)
			}
			pending, err := store.GetArchiveFinalizationJob(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			runtime, err := archiveMaterializationRuntime(currentArchiveBackend(), &pending.ArchiveRecord)
			if err != nil {
				t.Fatal(err)
			}
			marker := "/Recovery-Test/" + runtime.objectKey(run.ID, runArchiveManifestFileName)
			mu.Lock()
			if sha256BytesHex(objects[marker]) != pending.ArchiveRecord.ManifestSHA256 {
				t.Error("uploaded commit marker differs from outbox")
			}
			requests = nil
			mu.Unlock()
			if mode != "unsafe local" {
				if err := os.RemoveAll(record.ArchivePath); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := os.Remove(record.ManifestPath); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(record.ManifestPath, 0700); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "unknown local file" {
				if err := os.MkdirAll(record.ArchivePath, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(record.ArchivePath, "unconfirmed"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			row, err := store.GetTest(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			switch mode {
			case "missing marker":
				mu.Lock()
				delete(objects, marker)
				mu.Unlock()
			case "changed marker":
				mu.Lock()
				objects[marker] = []byte("{}")
				mu.Unlock()
			case "changed start":
				row.StartTime = row.StartTime.Add(time.Second)
				if err := store.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			case "changed end":
				end := row.EndTime.Add(time.Second)
				row.EndTime = &end
				if err := store.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			case "changed status":
				row.Status = "FAILED"
				if err := store.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			case "canceled":
				mu.Lock()
				intercept = cancel
				mu.Unlock()
			case "new intent":
				mu.Lock()
				intercept = func() {
					copy := *pending
					if err := store.SaveArchiveFinalizationJob(&copy); err != nil {
						t.Error(err)
					}
				}
				mu.Unlock()
			case "new checksum":
				mu.Lock()
				intercept = func() {
					copy := pending.ArchiveRecord
					// Preparing is the legitimate way to atomically replace the
					// token's checksum and bytes. Use a changed but valid manifest.
					changed := strings.Replace(pending.PreparedManifestJSON, `"COMPLETE"`, `"FAILED"`, 1)
					copy.ManifestSHA256 = sha256BytesHex([]byte(changed))
					if ok, err := store.PrepareArchiveFinalizationManifestIfToken(pending.TestID, pending.IntentToken, &copy, changed); err != nil || !ok {
						t.Error("replace pending checksum", ok, err)
					}
				}
				mu.Unlock()
			case "wrong namespace":
				t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", remote.URL+"/different-account")
			case "new default bucket":
				t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Different-Test")
			case "wrong token":
				copy := *pending
				if err := store.SaveArchiveFinalizationJob(&copy); err != nil {
					t.Fatal(err)
				}
				pending = &copy
			}
			store.failCatalog = false
			restarted := NewTestRunner(1, NewHub(), store)
			if mode == "stale lease" {
				leader := newExecutionLeadership(store, "uploaded-recovery")
				leader.tick()
				defer leader.relinquish()
				restarted.leadership, restarted.durableExecutionFence = leader, true
				_, owner, _, err := restarted.generatorExecutionClaim()
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				intercept = func() {
					if err := store.ReleaseSchedulerLease(executionLeaseName, owner); err != nil {
						t.Error(err)
					}
				}
				mu.Unlock()
			}
			err = restarted.processArchiveFinalizationJob(ctx, pending)
			want := mode == "recovered" || mode == "unknown local file" || mode == "new default bucket"
			if want && err != nil {
				t.Fatal(err)
			}
			if !want && mode != "new intent" && err == nil {
				t.Fatal("unproven upload accepted")
			}
			catalog, catalogErr := store.GetRunArchive(run.ID)
			if want {
				if catalogErr != nil || catalog.ManifestSHA256 != pending.ArchiveRecord.ManifestSHA256 || catalog.TrendPointJSON != pending.ArchiveRecord.TrendPointJSON {
					t.Fatal("catalog recovery differs", catalogErr)
				}
				if _, err := store.GetArchiveFinalizationJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("completed intent retained", err)
				}
				if _, err := store.GetArchiveRefreshJob(run.ID); err != nil {
					t.Fatal("adjunct reconciliation lost", err)
				}
			} else {
				if cached := restarted.archives[run.ID]; cached != nil {
					t.Fatal("uncommitted archive attached")
				}
				if !errors.Is(catalogErr, sql.ErrNoRows) {
					t.Fatal("unproven catalog published", catalogErr)
				}
				if _, err := store.GetArchiveFinalizationJob(run.ID); err != nil {
					t.Fatal("unconfirmed pending work lost", err)
				}
				if _, err := store.GetArchiveRefreshJob(run.ID); !errors.Is(err, sql.ErrNoRows) {
					t.Fatal("rejected recovery dirtied refresh", err)
				}
			}
			if mode == "unknown local file" {
				data, err := os.ReadFile(filepath.Join(record.ArchivePath, "unconfirmed"))
				if err != nil || string(data) != "keep" {
					t.Fatal("unknown evidence removed", err)
				}
			}
			mu.Lock()
			defer mu.Unlock()
			expectedRequests := 1
			if mode == "wrong namespace" || mode == "wrong token" || mode == "unsafe local" {
				expectedRequests = 0
			}
			if len(requests) != expectedRequests {
				t.Fatalf("recovery requests=%v", requests)
			}
			for _, request := range requests {
				if request != "GET "+marker {
					t.Fatalf("recovery touched other objects: %s", request)
				}
			}
		})
	}
}

func TestFinalizedArchiveAttachmentRejectsReplacedLiveIdentity(t *testing.T) {
	for _, mode := range []string{"valid", "project", "start", "end", "status", "missing end", "record ID"} {
		t.Run(mode, func(t *testing.T) {
			store, run := archiveFailureFixture(t)
			runner := NewTestRunner(1, NewHub(), store)
			runner.tests[run.ID] = run
			identity := storage.ArchiveFinalizationRunIdentity{ProjectID: run.ProjectID, StartTime: run.CreatedAt, EndTime: *run.EndedAt, Status: run.Status}
			record := &storage.RunArchiveRecord{TestID: run.ID, CreatedAt: run.CreatedAt.Add(time.Second), CompletedAt: *run.EndedAt}
			switch mode {
			case "project":
				run.ProjectID = "replacement-project"
			case "start":
				run.CreatedAt = run.CreatedAt.Add(time.Second)
			case "end":
				end := run.EndedAt.Add(time.Second)
				run.EndedAt = &end
			case "status":
				run.Status = "COMPLETE"
			case "missing end":
				run.EndedAt = nil
			case "record ID":
				record.TestID = "other-run"
			}
			runner.attachFinalizedRunArchive(run.ID, record, &identity)
			if (run.Archive != nil) != (mode == "valid") || (runner.archives[run.ID] != nil) != (mode == "valid") {
				t.Fatal("late archive attached to wrong live incarnation")
			}
		})
	}
}
