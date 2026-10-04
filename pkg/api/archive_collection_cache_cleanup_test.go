package api

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

func collectionCleanupFixture(t *testing.T, targets ...string) (*TestRunner, *storage.SQLiteStore, *ManagedTest, *storage.RunArchiveRecord, *storage.RunArchiveCollectionIntent) {
	t.Helper()
	tr, store, run, backend := collectionCacheFixture(t, targets...)
	ended := time.Now().UTC()
	record, err := backend.stageRemoteArchive(tr, run, "COMPLETE", "", ended, true)
	if err != nil || record == nil {
		t.Fatalf("collect: %+v %v", record, err)
	}
	if _, err := store.UpdateTestStatusIfActive(run.ID, "COMPLETE", &ended, ""); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || !intent.Resolved {
		t.Fatal("collection not resolved", err)
	}
	return tr, store, run, record, intent
}
func collectionCachedDirectory(root string, intent *storage.RunArchiveCollectionIntent, target string) string {
	return filepath.Join(root, "_collection", intent.TestID, intent.IntentToken, sha256BytesHex([]byte(target)), intent.CachedSources[target].ReceiptSHA256)
}
func TestCollectionCacheCleanupRequiresConfirmedUntamperedCopies(t *testing.T) {
	for _, mode := range []string{"confirmed", "no catalog", "pending", "changed primary", "changed cache", "extra file", "no coverage", "partial coverage", "different run"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, record, intent := collectionCleanupFixture(t, "a", "b")
			pathA := collectionCachedDirectory(resolveArchiveRoot(), intent, "a")
			pathB := collectionCachedDirectory(resolveArchiveRoot(), intent, "b")
			wantsError := false
			switch mode {
			case "no catalog":
				if err := store.DeleteRunArchive(run.ID); err != nil {
					t.Fatal(err)
				}
			case "pending":
				if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: run.ID, ProjectID: run.ProjectID, ArchiveRecord: *record}); err != nil {
					t.Fatal(err)
				}
			case "changed primary":
				if err := os.WriteFile(record.ManifestPath, []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
				wantsError = true
			case "changed cache":
				if err := os.WriteFile(filepath.Join(pathA, "transactions.jsonl"), []byte("corrupt"), 0600); err != nil {
					t.Fatal(err)
				}
				wantsError = true
			case "extra file":
				if err := os.WriteFile(filepath.Join(pathA, "unconfirmed"), []byte("keep"), 0600); err != nil {
					t.Fatal(err)
				}
				wantsError = true
			case "no coverage", "partial coverage":
				data, err := os.ReadFile(record.ManifestPath)
				if err != nil {
					t.Fatal(err)
				}
				var manifest RunArchiveManifest
				if err := json.Unmarshal(data, &manifest); err != nil {
					t.Fatal(err)
				}
				if mode == "no coverage" {
					manifest.SourceCoverage = nil
					wantsError = true
				} else {
					for i := range manifest.SourceCoverage.Sources {
						if manifest.SourceCoverage.Sources[i].Target == "a" {
							manifest.SourceCoverage.Sources[i].Collected = false
						}
					}
				}
				if err := writeIndentedJSONFile(record.ManifestPath, manifest); err != nil {
					t.Fatal(err)
				}
				data, err = os.ReadFile(record.ManifestPath)
				if err != nil {
					t.Fatal(err)
				}
				record.ManifestSHA256 = sha256BytesHex(data)
				if err := store.SaveRunArchive(record); err != nil {
					t.Fatal(err)
				}
			case "different run":
				row, err := store.GetTest(run.ID)
				if err != nil {
					t.Fatal(err)
				}
				row.StartTime = row.StartTime.Add(time.Second)
				if err := store.SaveTest(row); err != nil {
					t.Fatal(err)
				}
			}
			err := tr.retirePublishedCollectionCaches(context.Background())
			if (err != nil) != wantsError {
				t.Fatalf("cleanup %s: %v", mode, err)
			}
			for target, path := range map[string]string{"a": pathA, "b": pathB} {
				_, err := os.Stat(path)
				shouldRemove := mode == "confirmed" || (mode == "partial coverage" && target == "b")
				if os.IsNotExist(err) != shouldRemove {
					t.Fatalf("%s removal=%v want=%v (%v)", target, os.IsNotExist(err), shouldRemove, err)
				}
			}
			if mode != "different run" {
				after, err := store.GetRunArchiveCollection(run.ID)
				if err != nil || !reflect.DeepEqual(after.CachedSources, intent.CachedSources) {
					t.Fatal("local cleanup changed shared SQL anchors", err)
				}
			}
			if mode == "confirmed" {
				if err := tr.retirePublishedCollectionCaches(context.Background()); err != nil {
					t.Fatal("cleanup not idempotent", err)
				}
			}
		})
	}
}

func TestCollectionCacheCleanupPreservesUnanchoredSiblings(t *testing.T) {
	tr, store, _, _, intent := collectionCleanupFixture(t, "a")
	dir := collectionCachedDirectory(resolveArchiveRoot(), intent, "a")
	unanchored := filepath.Join(filepath.Dir(dir), "unanchored")
	if err := os.Mkdir(unanchored, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(unanchored, "evidence"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := tr.retirePublishedCollectionCaches(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("confirmed cache retained", err)
	}
	if data, err := os.ReadFile(filepath.Join(unanchored, "evidence")); err != nil || string(data) != "keep" {
		t.Fatal("unanchored evidence deleted", err)
	}
	// With no anchored local copy left, a later scan must not rehash the primary.
	record, err := store.GetRunArchive(intent.TestID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(record.ManifestPath); err != nil {
		t.Fatal(err)
	}
	if err := tr.retirePublishedCollectionCaches(context.Background()); err != nil {
		t.Fatal("empty local cleanup rechecked primary", err)
	}
}

func TestCollectionCacheCleanupLeavesOtherControllerRootsAnchored(t *testing.T) {
	tr, store, _, _, intent := collectionCleanupFixture(t, "a")
	originalRoot := resolveArchiveRoot()
	source := collectionCachedDirectory(originalRoot, intent, "a")
	otherRoot := t.TempDir()
	destination := collectionCachedDirectory(otherRoot, intent, "a")
	if err := os.MkdirAll(destination, 0700); err != nil {
		t.Fatal(err)
	}
	for _, name := range append([]string{"receipt.json"}, collectionPayloadNames...) {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(destination, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := tr.retirePublishedCollectionCaches(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(destination); err != nil {
		t.Fatal("another local root was removed", err)
	}
	after, err := store.GetRunArchiveCollection(intent.TestID)
	if err != nil || !reflect.DeepEqual(after.CachedSources, intent.CachedSources) {
		t.Fatal("another controller lost anchors", err)
	}
}

func TestCollectionCacheCleanupWorkerCancellation(t *testing.T) {
	tr, _, _, _, intent := collectionCleanupFixture(t, "a")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { tr.runCollectionCacheCleanup(ctx); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("cache cleanup did not stop")
	}
	if _, err := os.Stat(collectionCachedDirectory(resolveArchiveRoot(), intent, "a")); err != nil {
		t.Fatal("canceled cleanup removed evidence", err)
	}
}

func TestCollectionCacheCleanupConfirmsOnlyImmutableObjectManifest(t *testing.T) {
	for _, mode := range []string{"confirmed", "missing", "changed"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, _, record, intent := collectionCleanupFixture(t, "a")
			originalRoot := resolveArchiveRoot()
			data, err := os.ReadFile(record.ManifestPath)
			if err != nil {
				t.Fatal(err)
			}
			var requests atomic.Int32
			var expectedPath string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.Path != expectedPath {
					t.Errorf("unexpected object request %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 400)
					return
				}
				if mode == "missing" {
					http.NotFound(w, r)
					return
				}
				if mode == "changed" {
					_, _ = w.Write([]byte("{}"))
					return
				}
				_, _ = w.Write(data)
			}))
			defer server.Close()
			t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Cleanup-Test")
			t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", server.URL)
			t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
			t.Setenv("AWS_ACCESS_KEY_ID", "test")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
			t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
			if err := os.MkdirAll(resolveArchiveRoot(), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(filepath.Join(originalRoot, "_collection"), filepath.Join(resolveArchiveRoot(), "_collection")); err != nil {
				t.Fatal(err)
			}
			backend := currentArchiveBackend()
			record.ArchiveBackend = archiveBackendS3
			record.ArchiveURI, record.ManifestURI = backend.runURI(record.TestID), backend.manifestURI(record.TestID)
			if err := bindArchiveRecordToFinalizationVersion(record, uuid.NewString()); err != nil {
				t.Fatal(err)
			}
			runtime, err := archiveMaterializationRuntime(backend, record)
			if err != nil {
				t.Fatal(err)
			}
			expectedPath = "/Cleanup-Test/" + runtime.objectKey(record.TestID, runArchiveManifestFileName)
			if err := store.SaveRunArchive(record); err != nil {
				t.Fatal(err)
			}
			// An object publication must remain sufficient after its merged staging source is gone.
			if err := os.RemoveAll(record.ArchivePath); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = tr.retirePublishedCollectionCaches(ctx)
			if (err != nil) != (mode != "confirmed") {
				t.Fatalf("cleanup %s: %v", mode, err)
			}
			if requests.Load() != 1 {
				t.Fatalf("object requests=%d", requests.Load())
			}
			_, err = os.Stat(collectionCachedDirectory(resolveArchiveRoot(), intent, "a"))
			if os.IsNotExist(err) != (mode == "confirmed") {
				t.Fatal("unexpected cache removal", err)
			}
		})
	}
}

func TestCollectionCacheCleanupWaitsForSourceAndCapacityFences(t *testing.T) {
	for _, namespace := range []string{"source", "capacity"} {
		t.Run(namespace, func(t *testing.T) {
			tr, _, run, _, intent := collectionCleanupFixture(t, "a")
			var release func()
			var err error
			if namespace == "source" {
				release, err = lockArchivePublicationSource(context.Background(), run.ID)
			} else {
				release, err = lockArchiveNamespace(context.Background(), "_collection_locks", "capacity.lock")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if err := tr.retirePublishedCollectionCaches(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("cleanup did not respect held fence", err)
			}
			if _, err := os.Stat(collectionCachedDirectory(resolveArchiveRoot(), intent, "a")); err != nil {
				t.Fatal("busy cleanup removed source", err)
			}
		})
	}
}

func TestCollectionCacheCleanupWorkerJoinedWithPublicationWorker(t *testing.T) {
	tr, _, run, _, intent := collectionCleanupFixture(t, "a")
	release, err := lockArchivePublicationSource(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	server := &Server{runner: tr, archiveFinalizationWake: make(chan struct{}, 1)}
	server.startArchiveFinalizationRetry()
	if server.archiveFinalizationCancel == nil {
		t.Fatal("worker not started")
	}
	server.archiveFinalizationCancel()
	select {
	case <-server.archiveFinalizationDone:
	case <-time.After(5 * time.Second):
		t.Fatal("publication/cache workers did not join")
	}
	if _, err := os.Stat(collectionCachedDirectory(resolveArchiveRoot(), intent, "a")); err != nil {
		t.Fatal("canceled worker removed busy source", err)
	}
}

func TestCollectionCacheCleanupReleasesPhysicalByteAdmission(t *testing.T) {
	tr, _, _, _, _ := collectionCleanupFixture(t, "a")
	root, err := os.OpenRoot(filepath.Join(resolveArchiveRoot(), "_collection"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before, err := collectionCacheUsage(context.Background(), root, 2<<20)
	if err != nil || before <= 0 {
		t.Fatal("fixture has no cache usage", err)
	}
	if err := tr.retirePublishedCollectionCaches(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := collectionCacheUsage(context.Background(), root, 2<<20)
	if err != nil || after != 0 {
		t.Fatalf("cache quota not released: %d %v", after, err)
	}
}

func TestCollectionCacheCleanupRejectsReplacedDiscoveryDirectory(t *testing.T) {
	tr, _, _, _, intent := collectionCleanupFixture(t, "a")
	rootPath := filepath.Join(resolveArchiveRoot(), "_collection")
	root, err := os.OpenRoot(rootPath)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	candidates, err := discoverCollectionCacheCleanupCandidates(context.Background(), root)
	if err != nil || len(candidates) != 1 {
		t.Fatal("discover", err)
	}
	tokenPath := filepath.Join(rootPath, intent.TestID, intent.IntentToken)
	if err := os.Rename(tokenPath, tokenPath+".preserved"); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(tokenPath, 0700); err != nil {
		t.Fatal(err)
	}
	if err := tr.retirePublishedCollectionCache(context.Background(), root, candidates[0]); err == nil {
		t.Fatal("replaced discovery directory accepted")
	}
	if _, err := os.Stat(tokenPath + ".preserved"); err != nil {
		t.Fatal("original evidence removed", err)
	}
}

type collectionCleanupDetachedDeadline struct {
	context.Context
	at time.Time
}

func (ctx collectionCleanupDetachedDeadline) Deadline() (time.Time, bool) { return ctx.at, true }
func (ctx collectionCleanupDetachedDeadline) Done() <-chan struct{}       { return nil }
func (ctx collectionCleanupDetachedDeadline) Err() error                  { return nil }

func TestCollectionCacheCleanupUsesActualCallerDeadlineAtHeldFences(t *testing.T) {
	for _, namespace := range []string{"source", "capacity"} {
		t.Run(namespace, func(t *testing.T) {
			tr, _, run, _, intent := collectionCleanupFixture(t, "a")
			var release func()
			var err error
			if namespace == "source" {
				release, err = lockArchivePublicationSource(context.Background(), run.ID)
			} else {
				release, err = lockArchiveNamespace(context.Background(), "_collection_locks", "capacity.lock")
			}
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			// The actual deadline expires while Err/Done deliberately remain
			// unpublished. This also represents timer-publication scheduling lag.
			ctx := collectionCleanupDetachedDeadline{Context: context.Background(), at: time.Now().Add(25 * time.Millisecond)}
			if err := tr.retirePublishedCollectionCaches(ctx); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("expired caller deadline became a successful busy-source skip", err)
			}
			if _, err := os.Stat(collectionCachedDirectory(resolveArchiveRoot(), intent, "a")); err != nil {
				t.Fatal("expired cleanup removed held source", err)
			}
		})
	}
}

func TestCollectionCacheCleanupBusySourceSkipsOnlyWithLiveCaller(t *testing.T) {
	tr, _, run, _, intent := collectionCleanupFixture(t, "a")
	release, err := lockArchivePublicationSource(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	if err := tr.retirePublishedCollectionCaches(context.Background()); err != nil {
		t.Fatal("cleanup's own bounded busy-source retry became a caller failure", err)
	}
	if _, err := os.Stat(collectionCachedDirectory(resolveArchiveRoot(), intent, "a")); err != nil {
		t.Fatal("busy cleanup removed held source", err)
	}
}

func TestCollectionCacheCleanupContextAuthority(t *testing.T) {
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	for _, tc := range []struct {
		name string
		ctx  context.Context
		want error
	}{
		{"live", context.Background(), nil},
		{"future detached deadline", collectionCleanupDetachedDeadline{Context: context.Background(), at: time.Now().Add(time.Minute)}, nil},
		{"expired detached deadline", collectionCleanupDetachedDeadline{Context: context.Background(), at: time.Now().Add(-time.Second)}, context.DeadlineExceeded},
		{"cancelled", cancelled, context.Canceled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := collectionCacheCleanupContextError(tc.ctx); !errors.Is(err, tc.want) {
				t.Fatalf("cleanup authority = %v, want %v", err, tc.want)
			}
		})
	}
}
