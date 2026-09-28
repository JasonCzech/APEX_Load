package api

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCollectionCacheRefetchesOnDifferentControllerRoot(t *testing.T) {
	for _, mode := range []string{"same", "changed", "missing"} {
		t.Run(mode, func(t *testing.T) {
			tr, store, run, backend := collectionCacheFixture(t, "a", "lost")
			delete(run.remoteClientsByTarget, "lost")
			client := run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)
			if _, err := backend.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
				t.Fatal("missing source accepted")
			}
			intent, err := store.GetRunArchiveCollection(run.ID)
			if err != nil {
				t.Fatal(err)
			}
			anchor := intent.CachedSources["a"]
			if anchor.Bytes == 0 {
				t.Fatal("source was not cached")
			}
			firstRoot := resolveArchiveRoot()
			relative := filepath.Join("_collection", run.ID, intent.IntentToken, sha256BytesHex([]byte("a")), anchor.ReceiptSHA256)
			before, err := os.ReadFile(filepath.Join(firstRoot, relative, "receipt.json"))
			if err != nil {
				t.Fatal(err)
			}
			secondRoot := t.TempDir()
			t.Setenv("APEXLOAD_ARCHIVE_DIR", secondRoot)
			if mode == "changed" {
				if err := os.WriteFile(client.artifacts["transactions.jsonl"], []byte("changed source\n"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if mode == "missing" {
				run.remoteClientsByTarget = nil
			}
			cache, err := tr.openCollectionSourceCache(context.Background(), run)
			if err != nil {
				t.Fatal(err)
			}
			defer cache.close()
			// Exercise an exact-fit logical ceiling: an already anchored source must not
			// spend the run allowance a second time when materialized in another root.
			cache.intent.CacheLimitBytes = anchor.Bytes
			release, err := lockArchivePublicationSource(context.Background(), run.ID)
			if err != nil {
				t.Fatal(err)
			}
			defer release()
			bundles, coverage, err := backend.fetchRemoteArchiveBundlesWithCache(context.Background(), tr, run, false, cache)
			defer cleanupRemoteArchiveBundles(bundles)
			switch mode {
			case "same":
				if err != nil || len(bundles) != 1 {
					t.Fatalf("verified refetch failed: %d %v", len(bundles), err)
				}
				receipt, err := os.ReadFile(filepath.Join(secondRoot, relative, "receipt.json"))
				if err != nil || string(receipt) != string(before) {
					t.Fatal("refetch changed receipt", err)
				}
			case "changed":
				if err == nil || !strings.Contains(err.Error(), "differs from its durable receipt") {
					t.Fatalf("changed source trusted: %v", err)
				}
			case "missing":
				if err != nil || len(bundles) != 0 || coverage == nil || len(coverage.Sources) != 2 {
					t.Fatalf("missing local/remote coverage: %+v %v", coverage, err)
				}
				for _, source := range coverage.Sources {
					if source.Collected {
						t.Fatal("unavailable source counted as collected")
					}
				}
			}
			after, err := os.ReadFile(filepath.Join(firstRoot, relative, "receipt.json"))
			if err != nil || string(after) != string(before) {
				t.Fatal("another controller changed original cache", err)
			}
			latest, err := store.GetRunArchiveCollection(run.ID)
			if err != nil || latest.CachedSources["a"] != anchor || latest.CacheLimitBytes != intent.CacheLimitBytes {
				t.Fatal("refetch rebound SQL receipt or ceiling", err)
			}
		})
	}
}

func TestCollectionCacheDoesNotRefetchIncompleteAnchoredDirectory(t *testing.T) {
	tr, store, run, backend := collectionCacheFixture(t, "a", "lost")
	delete(run.remoteClientsByTarget, "lost")
	client := run.remoteClientsByTarget["a"].(*archiveStreamingFakeClient)
	if _, err := backend.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), true); err == nil {
		t.Fatal("missing source accepted")
	}
	intent, err := store.GetRunArchiveCollection(run.ID)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(resolveArchiveRoot(), "_collection", run.ID, intent.IntentToken, sha256BytesHex([]byte("a")), intent.CachedSources["a"].ReceiptSHA256, "receipt.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	client.mu.Lock()
	calls := client.streamCalls
	client.mu.Unlock()
	if _, err := backend.stageRemoteArchive(tr, run, "FAILED", "", time.Now(), false); err == nil {
		t.Fatal("incomplete directory silently repaired")
	}
	client.mu.Lock()
	after := client.streamCalls
	client.mu.Unlock()
	if after != calls {
		t.Fatal("corrupt cache caused remote replacement")
	}
}
