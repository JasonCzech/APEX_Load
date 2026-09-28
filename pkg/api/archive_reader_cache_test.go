package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/storage"
)

func TestArchiveReaderCacheRuntime(t *testing.T) {
	backend := archiveBackendRuntime{Backend: archiveBackendS3, Bucket: "bucket", Prefix: "version", LocalRoot: t.TempDir()}
	for _, value := range []string{"", "relative/cache", t.TempDir()} {
		t.Run(value, func(t *testing.T) {
			t.Setenv(envArchiveReaderCacheDir, value)
			got, err := archiveReaderRuntime(backend)
			if value == "relative/cache" {
				if err == nil {
					t.Fatal("relative cache root accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if value == "" {
				if got != backend {
					t.Fatal("default runtime changed")
				}
				return
			}
			canonical, err := archiveReaderCacheCanonicalRoot(value)
			if err != nil {
				t.Fatal(err)
			}
			if got.LocalRoot == backend.LocalRoot || !strings.HasPrefix(got.LocalRoot, canonical+string(filepath.Separator)) {
				t.Fatal("reader root not isolated")
			}
			if archiveMaterializationCacheScanRoot(backend.LocalRoot) != canonical {
				t.Fatal("retention scans shared staging")
			}
			got.LocalRoot = backend.LocalRoot
			if got != backend {
				t.Fatal("reader override changed object identity")
			}
		})
	}
}

func TestArchiveReaderCacheRejectsSourceOverlapWithoutCreatingFiles(t *testing.T) {
	parent := t.TempDir()
	shared := filepath.Join(parent, "shared")
	if err := os.Mkdir(shared, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(parent, "alias")
	if err := os.Symlink(shared, alias); err != nil {
		t.Fatal(err)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", shared)
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", "  "+filepath.Join(shared, "objects")+"  ")
	backend := archiveBackendRuntime{Backend: archiveBackendS3, Bucket: "test", LocalRoot: filepath.Join(shared, "objects", "namespace")}
	for _, tc := range []struct {
		name, path string
		valid      bool
	}{
		{"same", shared, false}, {"child", filepath.Join(shared, "new-cache"), false},
		{"parent", parent, false}, {"alias", filepath.Join(alias, "new-cache"), false},
		{"sibling", filepath.Join(parent, "shared-reader"), true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(envArchiveReaderCacheDir, tc.path)
			_, err := archiveReaderRuntime(backend)
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t error=%v", tc.valid, err)
			}
		})
	}
	for _, path := range []string{filepath.Join(shared, "new-cache"), filepath.Join(parent, "shared-reader"), backend.LocalRoot} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("configuration validation created files: %v", err)
		}
	}
}

func TestArchivePrivateReaderCachesPreserveSharedStaging(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_STAGING_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendS3)
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "Reader-Test")
	t.Setenv("APEXLOAD_ARCHIVE_REGION", "us-east-1")
	t.Setenv("AWS_ACCESS_KEY_ID", "test")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "test")
	t.Setenv("AWS_EC2_METADATA_DISABLED", "true")
	t.Setenv("AWS_REQUEST_CHECKSUM_CALCULATION", "when_required")
	t.Setenv("AWS_RESPONSE_CHECKSUM_VALIDATION", "when_required")
	const id = "private-reader-shared-source"
	data, err := json.Marshal(RunArchiveManifest{SchemaVersion: archiveSchemaVersion, TestID: id, TestName: "shared evidence"})
	if err != nil {
		t.Fatal(err)
	}
	var expectedPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != expectedPath {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		_, _ = w.Write(data)
	}))
	defer server.Close()
	t.Setenv("APEXLOAD_ARCHIVE_ENDPOINT", server.URL)
	backend := currentArchiveBackend()
	expectedPath = "/Reader-Test/" + backend.Prefix + "/" + id + "/manifest.json"
	original := storage.RunArchiveRecord{TestID: id, ArchiveBackend: archiveBackendS3, ArchiveURI: backend.runURI(id), ManifestURI: backend.manifestURI(id), ManifestSHA256: sha256BytesHex(data)}
	sharedRoot := resolveArchiveRoot()
	sharedSource := filepath.Join(sharedRoot, id)
	if err := os.MkdirAll(sharedSource, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sharedSource, runArchiveManifestFileName), data, 0600); err != nil {
		t.Fatal(err)
	}
	// Even a legacy reader marker in the shared tree must remain outside the
	// new private cache's eviction scan. It may still be used by an older pod.
	if err := writeArchiveMaterializationCacheMarker(sharedSource, int64(len(data)), time.Now().Add(-24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	privateA, privateB := t.TempDir(), t.TempDir()
	t.Setenv(envArchiveReaderCacheDir, privateA)
	a := original
	leaseA, err := acquireArchiveMaterialized(context.Background(), &a)
	if err != nil {
		t.Fatal(err)
	}
	defer leaseA.Release()
	t.Setenv(envArchiveReaderCacheDir, privateB)
	b := original
	leaseB, err := acquireArchiveMaterialized(context.Background(), &b)
	if err != nil {
		t.Fatal(err)
	}
	defer leaseB.Release()
	if a.ArchivePath == b.ArchivePath || a.ArchivePath == sharedSource || b.ArchivePath == sharedSource {
		t.Fatal("controllers share reader files")
	}
	if a.ArchiveURI != original.ArchiveURI || b.ManifestSHA256 != original.ManifestSHA256 {
		t.Fatal("reader override changed durable proof")
	}
	// Refreshes copy from the private cache back into durable shared staging.
	publication, _, err := stageMaterializedArchiveForPublication(context.Background(), &b, uuid.NewString())
	if err != nil {
		t.Fatal(err)
	}
	canonicalShared, err := resolveArchiveDirectoryRoot(sharedRoot, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(publication, canonicalShared+string(filepath.Separator)) {
		t.Fatal("publication escaped shared staging")
	}
	leaseB.Release()
	t.Setenv(envArchiveMaterializationCacheMaxBytes, "1")
	archiveMaterializations.enforceRetention(archiveMaterializationCacheScanRoot(""))
	if _, err := os.Stat(b.ManifestPath); !os.IsNotExist(err) {
		t.Fatalf("private B cache not evicted: %v", err)
	}
	for _, path := range []string{a.ManifestPath, filepath.Join(sharedSource, runArchiveManifestFileName), filepath.Join(publication, runArchiveManifestFileName)} {
		got, err := os.ReadFile(path)
		if err != nil || sha256BytesHex(got) != original.ManifestSHA256 {
			t.Fatalf("private eviction damaged surviving source: %s %v", path, err)
		}
	}
}
