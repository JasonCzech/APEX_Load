package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func sourceRetentionFixture(t *testing.T, publication bool) (*TestRunner, *storage.SQLiteStore, *ManagedTest, *storage.RunArchiveRecord, orphanArchiveCandidate) {
	t.Helper()
	t.Setenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL", "1m")
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "source-test")
	t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", "")
	t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "retention")
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	store, err := storage.NewSQLiteStore(filepath.Join(t.TempDir(), "db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	parent := filepath.Join(resolveArchiveRoot(), "remote")
	if publication {
		parent = filepath.Join(resolveArchiveRoot(), "_publication", uuid.NewString())
	}
	test, record := createArchiveFinalizationTest(t, store, "source-run", parent)
	backend := currentArchiveBackend()
	record.ArchiveBackend = archiveBackendS3
	record.ArchiveURI = backend.runURI(test.ID)
	record.ManifestURI = backend.manifestURI(test.ID)
	if err := bindArchiveRecordToFinalizationVersion(record, uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	info, err := os.Lstat(record.ArchivePath)
	if err != nil {
		t.Fatal(err)
	}
	return NewTestRunner(1, NewHub(), store), store, test, record, orphanArchiveCandidate{path: mustCanonicalArchiveSource(t, record.ArchivePath), identity: info}
}

func TestPublishedArchiveSourceRetirementGuards(t *testing.T) {
	for _, mode := range []string{"confirmed", "publication", "unconfirmed", "young", "future receipt", "pending", "changed manifest", "changed directory", "bad receipt", "symlink receipt", "different endpoint", "disabled", "filesystem"} {
		t.Run(mode, func(t *testing.T) {
			runner, store, test, record, candidate := sourceRetentionFixture(t, mode == "publication")
			if mode != "unconfirmed" {
				if err := markPublishedArchiveSource(record); err != nil {
					t.Fatal(err)
				}
			}
			now := time.Now().Add(2 * time.Minute)
			expectError := false
			switch mode {
			case "young":
				now = time.Now()
			case "future receipt":
				now = time.Now().Add(-time.Hour)
				expectError = true
			case "pending":
				if err := store.SaveArchiveFinalizationJob(&storage.ArchiveFinalizationJob{TestID: test.ID, ProjectID: test.ProjectID, ArchiveRecord: *record}); err != nil {
					t.Fatal(err)
				}
			case "changed manifest":
				if err := os.WriteFile(record.ManifestPath, []byte(`{"test_id":"replacement"}`), 0o600); err != nil {
					t.Fatal(err)
				}
				expectError = true
			case "changed directory":
				if err := os.Rename(record.ArchivePath, record.ArchivePath+"-old"); err != nil {
					t.Fatal(err)
				}
				if err := os.Mkdir(record.ArchivePath, 0o700); err != nil {
					t.Fatal(err)
				}
				expectError = true
			case "bad receipt":
				if err := os.WriteFile(filepath.Join(record.ArchivePath, archiveSourceReceiptName), []byte(`{}`), 0o600); err != nil {
					t.Fatal(err)
				}
				expectError = true
			case "symlink receipt":
				path := filepath.Join(record.ArchivePath, archiveSourceReceiptName)
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				outside := filepath.Join(t.TempDir(), "receipt")
				if err := os.WriteFile(outside, []byte("keep"), 0o600); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(outside, path); err != nil {
					t.Fatal(err)
				}
				expectError = true
			case "different endpoint":
				t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", "http://127.0.0.1:12345")
				expectError = true
			case "disabled":
				t.Setenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL", "")
			case "filesystem":
				path := filepath.Join(record.ArchivePath, archiveSourceReceiptName)
				data, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				var receipt publishedArchiveSourceReceipt
				if err := json.Unmarshal(data, &receipt); err != nil {
					t.Fatal(err)
				}
				receipt.Backend = archiveBackendFilesystem
				data, _ = json.Marshal(receipt)
				if err := os.WriteFile(path, data, 0o600); err != nil {
					t.Fatal(err)
				}
				expectError = true
			}
			err := runner.retirePublishedArchiveSource(context.Background(), candidate, now, time.Minute)
			if (err != nil) != expectError {
				t.Fatalf("error=%v want error=%v", err, expectError)
			}
			_, statErr := os.Lstat(record.ArchivePath)
			shouldRemove := mode == "confirmed" || mode == "publication"
			if os.IsNotExist(statErr) != shouldRemove {
				t.Fatalf("source removal=%v want=%v (stat %v)", os.IsNotExist(statErr), shouldRemove, statErr)
			}
		})
	}
}

func TestPublishedArchiveSourceFenceAndReceiptReplacement(t *testing.T) {
	runner, _, _, record, candidate := sourceRetentionFixture(t, false)
	if err := markPublishedArchiveSource(record); err != nil {
		t.Fatal(err)
	}
	// Repeated confirmation replaces the receipt, rather than retaining stale
	// metadata or following a previously planted receipt symlink.
	if err := markPublishedArchiveSource(record); err != nil {
		t.Fatal(err)
	}
	release, err := lockArchivePublicationSource(context.Background(), record.TestID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	err = runner.retirePublishedArchiveSource(ctx, candidate, time.Now().Add(time.Hour), time.Minute)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("active writer was not protected: %v", err)
	}
	release()
	if err := runner.retirePublishedArchiveSource(context.Background(), candidate, time.Now().Add(time.Hour), time.Minute); err != nil {
		t.Fatal(err)
	}
	locks, err := os.ReadDir(filepath.Join(resolveArchiveRoot(), "_source_locks"))
	if err != nil || len(locks) != 1 {
		t.Fatalf("lock inode was removed: %v %v", locks, err)
	}
}

func TestPublishedArchiveSourceScanExcludesReaderCachesAndSymlinks(t *testing.T) {
	_, _, _, record, _ := sourceRetentionFixture(t, false)
	outside := t.TempDir()
	for _, path := range []string{filepath.Join(resolveArchiveRoot(), "reader-cache"), filepath.Join(resolveArchiveRoot(), "_publication", "not-a-token", "source-run")} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(outside, filepath.Join(resolveArchiveRoot(), "remote", "outside")); err != nil {
		t.Fatal(err)
	}
	candidates, err := discoverPublishedArchiveSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 || !os.SameFile(candidates[0].identity, mustArchiveSourceInfo(t, record.ArchivePath)) {
		t.Fatalf("unexpected sources: %+v", candidates)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := discoverPublishedArchiveSources(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("scan cancellation: %v", err)
	}
}

func TestPublishedArchiveSourceTTLAndImmutableVersion(t *testing.T) {
	for _, v := range []string{"", "bad", "0", "-1m", "59s", "721h"} {
		t.Setenv("APEXLOAD_ARCHIVE_SOURCE_RETENTION_TTL", v)
		if archiveSourceRetentionTTL() != 0 {
			t.Fatalf("accepted %q", v)
		}
	}
	_, _, _, record, _ := sourceRetentionFixture(t, false)
	for _, mode := range []string{"mutable URI", "invalid hash", "cache path"} {
		copy := *record
		switch mode {
		case "mutable URI":
			copy.ArchiveURI = currentArchiveBackend().runURI(record.TestID)
			copy.ManifestURI = currentArchiveBackend().manifestURI(record.TestID)
		case "invalid hash":
			copy.ManifestSHA256 = strings.Repeat("z", 64)
		case "cache path":
			copy.ArchivePath = filepath.Join(resolveArchiveRoot(), copy.TestID)
		}
		err := markPublishedArchiveSource(&copy)
		if mode == "cache path" {
			if err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatalf("accepted %s", mode)
		}
	}
}

func TestPublishedArchiveSourceReceiptRequiresUploadAndCatalogCommit(t *testing.T) {
	for _, mode := range []string{"success", "upload failure", "catalog failure"} {
		t.Run(mode, func(t *testing.T) {
			_, sqlite, test, record, _ := sourceRetentionFixture(t, false)
			var mu sync.Mutex
			objects := map[string][]byte{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if mode == "upload failure" {
					http.Error(w, "rejected", http.StatusForbidden)
					return
				}
				mu.Lock()
				defer mu.Unlock()
				if r.Method == http.MethodPut {
					data, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
					if err != nil {
						http.Error(w, "read", 500)
						return
					}
					objects[r.URL.Path] = data
					w.Header().Set("ETag", `"test"`)
					w.WriteHeader(200)
					return
				}
				if r.Method == http.MethodGet {
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
			defer server.Close()
			// A legacy mixed-case bucket forces the SDK's path-style addressing
			// so the real HTTP adapter can reach this loopback test service.
			t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Source-Test")
			t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", server.URL)
			t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
			t.Setenv("AWS_ACCESS_KEY_ID", "test")
			t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
			t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
			t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
			t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
			// Endpoint identity changes the staging namespace. Move this fixture to the
			// same root the configured publisher and retirement worker will use.
			next := filepath.Join(resolveArchiveRoot(), "remote", test.ID)
			if err := os.MkdirAll(filepath.Dir(next), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(record.ArchivePath, next); err != nil {
				t.Fatal(err)
			}
			record.ArchivePath = next
			record.ManifestPath = filepath.Join(next, runArchiveManifestFileName)
			backend := currentArchiveBackend()
			record.ArchiveURI = backend.runURI(test.ID)
			record.ManifestURI = backend.manifestURI(test.ID)
			store := &failingArchiveCatalogStore{SQLiteStore: sqlite, failCatalog: mode == "catalog failure"}
			runner := NewTestRunner(1, NewHub(), sqliteOverlay(store))
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err := runner.finalizeRunArchiveDurably(ctx, test, record, false)
			if (err == nil) != (mode == "success") {
				t.Fatalf("finalize: %v", err)
			}
			_, markerErr := os.Stat(filepath.Join(record.ArchivePath, archiveSourceReceiptName))
			if (markerErr == nil) != (mode == "success") {
				t.Fatalf("receipt after %s: %v", mode, markerErr)
			}
			if mode != "success" {
				if _, err := os.Stat(record.ManifestPath); err != nil {
					t.Fatalf("failed source lost: %v", err)
				}
				return
			}
			catalog, err := sqlite.GetRunArchive(test.ID)
			if err != nil {
				t.Fatal(err)
			}
			// Simulate a crash after catalog commit but before the cleanup
			// receipt reached disk. Confirmation must consult the remote manifest.
			if err := os.Remove(filepath.Join(record.ArchivePath, archiveSourceReceiptName)); err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(record.ArchivePath)
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			var remoteKey string
			var remoteManifest []byte
			for key, value := range objects {
				if strings.HasSuffix(key, "/manifest.json") {
					remoteKey = key
					remoteManifest = append([]byte(nil), value...)
					objects[key] = []byte("corrupt remote manifest")
				}
			}
			mu.Unlock()
			if remoteKey == "" {
				t.Fatal("manifest was not uploaded")
			}
			candidate := orphanArchiveCandidate{path: mustCanonicalArchiveSource(t, record.ArchivePath), identity: info}
			if err := runner.retirePublishedArchiveSource(context.Background(), candidate, time.Now().Add(time.Hour), time.Minute); err == nil {
				t.Fatal("unconfirmed remote manifest was trusted")
			}
			if _, err := os.Stat(filepath.Join(record.ArchivePath, archiveSourceReceiptName)); !os.IsNotExist(err) {
				t.Fatalf("unsafe receipt: %v", err)
			}
			mu.Lock()
			objects[remoteKey] = remoteManifest
			mu.Unlock()
			if err := runner.retirePublishedArchiveSource(context.Background(), orphanArchiveCandidate{path: mustCanonicalArchiveSource(t, record.ArchivePath), identity: info}, time.Now().Add(time.Hour), time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(record.ManifestPath); err != nil {
				t.Fatalf("recovered receipt skipped TTL: %v", err)
			}
			if err := runner.retirePublishedArchiveSource(context.Background(), orphanArchiveCandidate{path: mustCanonicalArchiveSource(t, record.ArchivePath), identity: info}, time.Now().Add(time.Hour), time.Minute); err != nil {
				t.Fatal(err)
			}
			if _, err := sqlite.GetRunArchive(test.ID); err != nil {
				t.Fatalf("catalog removed: %v", err)
			}
			lease, err := acquireArchiveMaterialized(context.Background(), catalog)
			if err != nil {
				t.Fatalf("published archive unreadable after source retirement: %v", err)
			}
			lease.Release()
		})
	}
}

func mustArchiveSourceInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}

func mustCanonicalArchiveSource(t *testing.T, path string) string {
	t.Helper()
	p, err := resolveArchiveDirectoryRoot(path, false)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestPublishedArchiveSourceScanBoundsNonCandidates(t *testing.T) {
	_, _, _, _, _ = sourceRetentionFixture(t, false)
	directory := filepath.Join(resolveArchiveRoot(), "remote")
	for i := 0; i < 10001; i++ {
		if err := os.WriteFile(filepath.Join(directory, fmt.Sprintf("not-directory-%d", i)), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := discoverPublishedArchiveSources(context.Background()); err == nil || !strings.Contains(err.Error(), "entry limit") {
		t.Fatalf("unbounded scan: %v", err)
	}
}

func TestPublishedArchiveSourceLockRejectsPlantedSymlink(t *testing.T) {
	_, _, _, record, _ := sourceRetentionFixture(t, false)
	locks := filepath.Join(resolveArchiveRoot(), "_source_locks")
	if err := os.MkdirAll(locks, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "lock")
	if err := os.WriteFile(outside, []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(locks, sha256BytesHex([]byte(record.TestID))+".lock")); err != nil {
		t.Fatal(err)
	}
	if release, err := lockArchivePublicationSource(context.Background(), record.TestID); err == nil {
		release()
		t.Fatal("symlink lock accepted")
	}
	data, err := os.ReadFile(outside)
	if err != nil || string(data) != "preserve" {
		t.Fatalf("outside file changed: %q %v", data, err)
	}
}
