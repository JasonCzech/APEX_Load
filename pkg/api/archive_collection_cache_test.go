package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/storage"
)

func collectionCacheFixture(t *testing.T, targets ...string) (*TestRunner, *storage.SQLiteStore, *ManagedTest, *distributedExecutionBackend) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES", "1048576")
	t.Setenv("APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES", "2097152")
	tr, store, run := collectionIntentFixture(t)
	token := bytes.Repeat([]byte{1}, 32)
	if err := tr.prepareRunArchiveSources(run, targets, 0, token); err != nil {
		t.Fatal(err)
	}
	run.remoteClientsByTarget = make(map[string]generatorClient)
	for _, target := range targets {
		client := coverageArchiveClient(t, run.ID)
		setSourceIdentityManifest(t, client, generatorrpc.ArchiveSourceIdentity(run.ID, token, target))
		run.remoteClientsByTarget[target] = client
	}
	return tr, store, run, newDistributedExecutionBackend(targets, time.Second, nil)
}
func TestCollectionCacheRetainsPartialSourcesAndReplaysAfterRestart(t *testing.T) {
	tr, store, run, b := collectionCacheFixture(t, "a", "b")
	second := run.remoteClientsByTarget["b"]
	delete(run.remoteClientsByTarget, "b")
	if record, err := b.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil || record != nil {
		t.Fatal("missing required source accepted")
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || len(intent.CachedSources) != 1 || intent.CachedSources["a"].Bytes == 0 {
		t.Fatalf("partial cache lost: %+v %v", intent, err)
	}
	row, err := store.GetTest(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewTestRunner(1, nil, nil)
	restarted.testStore, restarted.archiveStore = store, store
	restored := managedTestFromRecord(row)
	restored.remoteClientsByTarget = map[string]generatorClient{"b": second}
	record, err := b.stageRemoteArchive(restarted, restored, "FAILED", "", time.Now(), true)
	if err != nil || record == nil {
		t.Fatalf("cached source unavailable after restart: %v", err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil || manifest.SourceCoverage == nil || len(manifest.SourceCoverage.Sources) != 2 {
		t.Fatalf("coverage: %+v %v", manifest, err)
	}
	for _, source := range manifest.SourceCoverage.Sources {
		if !source.Collected {
			t.Fatal("cached source marked missing")
		}
	}
}
func TestCollectionCacheRejectsCorruptionAndSymlinks(t *testing.T) {
	for _, mode := range []string{"receipt", "payload", "symlink", "target-symlink"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, b := collectionCacheFixture(t, "a", "lost")
			delete(run.remoteClientsByTarget, "lost")
			if _, err := b.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
				t.Fatal("missing source accepted")
			}
			intent, err := store.GetRunArchiveCollection(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			target := filepath.Join(resolveArchiveRoot(), "_collection", run.ID, intent.IntentToken, sha256BytesHex([]byte("a")))
			dir := filepath.Join(target, intent.CachedSources["a"].ReceiptSHA256)
			switch mode {
			case "receipt":
				if err := os.WriteFile(filepath.Join(dir, "receipt.json"), []byte("{}"), 0600); err != nil {
					t.Fatal(err)
				}
			case "payload":
				if err := os.WriteFile(filepath.Join(dir, "transactions.jsonl"), []byte("changed"), 0600); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				path := filepath.Join(dir, "transactions.jsonl")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(t.TempDir(), "elsewhere"), path); err != nil {
					t.Fatal(err)
				}
			case "target-symlink":
				moved := target + "-moved"
				if err := os.Rename(target, moved); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(moved, target); err != nil {
					t.Fatal(err)
				}
			}
			run.remoteClientsByTarget = nil
			if record, err := b.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), false); err == nil || record != nil {
				t.Fatal("corrupt source silently reused")
			}
		})
	}
}
func TestCollectionCacheBoundsAndCountsOrphans(t *testing.T) {
	for _, mode := range []string{"run", "root", "orphan", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, b := collectionCacheFixture(t, "a")
			if mode == "run" {
				t.Setenv("APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES", "1")
			}
			if mode == "root" {
				t.Setenv("APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES", "1")
			}
			if mode == "orphan" || mode == "symlink" {
				root := filepath.Join(resolveArchiveRoot(), "_collection")
				if err := os.Mkdir(root, 0700); err != nil {
					t.Fatal(err)
				}
				if mode == "orphan" {
					if err := os.WriteFile(filepath.Join(root, "unanchored"), make([]byte, 2<<20), 0600); err != nil {
						t.Fatal(err)
					}
				} else {
					if err := os.Symlink(t.TempDir(), filepath.Join(root, "untrusted")); err != nil {
						t.Fatal(err)
					}
				}
			}
			if record, err := b.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil || record != nil {
				t.Fatal("unbounded collection succeeded")
			}
			intent, err := store.GetRunArchiveCollection(run.ID)
			if err != nil || len(intent.CachedSources) != 0 {
				t.Fatal("rejected collection anchored", err)
			}
		})
	}
}
func TestCollectionCacheRevalidatesBytesWhileCopying(t *testing.T) {
	tr, _, run, b := collectionCacheFixture(t, "a", "lost")
	delete(run.remoteClientsByTarget, "lost")
	if _, err := b.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
		t.Fatal("missing source accepted")
	}
	cache, err := tr.openCollectionSourceCache(context.Background(), run)
	if err != nil {
		t.Fatal(err)
	}
	defer cache.close()
	source, err := cache.load(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	defer source.close()
	// Tamper after receipt loading. Copy-time hashing must still detect it.
	file, err := source.root.OpenFile("transactions.jsonl", os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatal(err)
	}
	originalSize := source.receipt.Files[0].Bytes
	if _, err := file.Write(bytes.Repeat([]byte("x"), int(originalSize))); err != nil {
		t.Fatal(err)
	}
	file.Close()
	_, err = source.StreamArchiveArtifact(context.Background(), generatorrpc.ArchiveArtifactRequest{TestID: run.ID, SourceIdentity: source.receipt.Identity, Name: source.names[0]}, io.Discard, 1<<20)
	if err == nil || !strings.Contains(err.Error(), "hash mismatch") {
		t.Fatalf("late corruption missed: %v", err)
	}
}
func TestCollectionCacheUsageCancellationAndDepth(t *testing.T) {
	path := t.TempDir()
	root, err := os.OpenRoot(path)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := collectionCacheUsage(ctx, root, 100); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(path, "a/b/c/d/e/f/g"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := collectionCacheUsage(context.Background(), root, 100); err == nil {
		t.Fatal("unbounded traversal")
	}
}

type fenceCollectionCacheStore struct {
	SQLiteStore *storage.SQLiteStore
	reject      bool
}

func (s *fenceCollectionCacheStore) SaveRunArchiveCollectionSource(id, token, target string, source storage.RunArchiveCollectionSource, lease, owner string) (bool, error) {
	if s.reject {
		return false, nil
	}
	return s.SQLiteStore.SaveRunArchiveCollectionSource(id, token, target, source, lease, owner)
}
func TestCollectionCacheRecoversSyncedButUnanchoredSource(t *testing.T) {
	tr, store, run, b := collectionCacheFixture(t, "a", "lost")
	delete(run.remoteClientsByTarget, "lost")
	fenced := &fenceCollectionCacheStore{SQLiteStore: store, reject: true}
	tr.testStore = sqliteOverlay(fenced)
	if _, err := b.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
		t.Fatal("fenced anchor accepted")
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || len(intent.CachedSources) != 0 {
		t.Fatal("fenced anchor persisted")
	}
	root, err := os.OpenRoot(filepath.Join(resolveArchiveRoot(), "_collection"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	before, err := collectionCacheUsage(context.Background(), root, 2<<20)
	if err != nil || before == 0 {
		t.Fatal("unanchored data lost", err)
	}
	fenced.reject = false
	if _, err := b.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
		t.Fatal("missing source accepted")
	}
	intent, err = store.GetRunArchiveCollection(run.ID)
	if err != nil || len(intent.CachedSources) != 1 {
		t.Fatal("synced source did not recover", err)
	}
	after, err := collectionCacheUsage(context.Background(), root, 2<<20)
	if err != nil || after != before {
		t.Fatal("recovery duplicated source", err)
	}
}

type collectionPeerFailure struct {
	startupLifecycleFakeClient
	failed chan struct{}
}

func (c *collectionPeerFailure) StreamArchiveArtifact(context.Context, generatorrpc.ArchiveArtifactRequest, io.Writer, int64) (int64, error) {
	close(c.failed)
	return 0, errors.New("source disconnected")
}

type collectionDelayedSource struct {
	*archiveStreamingFakeClient
	failed <-chan struct{}
}

func (c *collectionDelayedSource) StreamArchiveArtifact(ctx context.Context, r generatorrpc.ArchiveArtifactRequest, w io.Writer, max int64) (int64, error) {
	if r.Name == runArchiveManifestFileName {
		select {
		case <-c.failed:
		case <-ctx.Done():
			return 0, ctx.Err()
		}
		select {
		case <-time.After(20 * time.Millisecond):
		case <-ctx.Done():
			return 0, ctx.Err()
		}
	}
	return c.archiveStreamingFakeClient.StreamArchiveArtifact(ctx, r, w, max)
}
func TestCollectionCachePeerErrorDoesNotDiscardSurvivor(t *testing.T) {
	tr, store, run, b := collectionCacheFixture(t, "a", "broken")
	failed := make(chan struct{})
	run.remoteClientsByTarget["broken"] = &collectionPeerFailure{failed: failed}
	run.remoteClientsByTarget["a"] = &collectionDelayedSource{archiveStreamingFakeClient: run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient), failed: failed}
	if _, err := b.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
		t.Fatal("failed source ignored")
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil || len(intent.CachedSources) != 1 || intent.CachedSources["a"].Bytes == 0 {
		t.Fatalf("peer failure discarded survivor: %+v %v", intent, err)
	}
}
