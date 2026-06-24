package api

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"gocloud.dev/blob/fileblob"
)

func TestCurrentArchiveBackendDefaultsToFilesystem(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)

	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendFilesystem {
		t.Fatalf("backend = %q, want filesystem", backend.Backend)
	}
	if backend.LocalRoot != root {
		t.Fatalf("local root = %q, want %q", backend.LocalRoot, root)
	}
	if err := backend.validate(); err != nil {
		t.Fatalf("validate returned error: %v", err)
	}
}

func TestCurrentArchiveBackendBuildsObjectURIAndStagingRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "s3")
	t.Setenv("APEXLOAD_ARCHIVE_BUCKET", "apexload-archives")
	t.Setenv("APEXLOAD_ARCHIVE_PREFIX", "/prod/evidence/")

	backend := currentArchiveBackend()
	if backend.Backend != archiveBackendS3 {
		t.Fatalf("backend = %q, want s3", backend.Backend)
	}
	wantRoot := filepath.Join(root, "_object_staging", "s3")
	if backend.LocalRoot != wantRoot {
		t.Fatalf("local root = %q, want %q", backend.LocalRoot, wantRoot)
	}
	if got := backend.runURI("run-1"); got != "s3://apexload-archives/prod/evidence/run-1" {
		t.Fatalf("run uri = %q", got)
	}
	if got := backend.manifestURI("run-1"); got != "s3://apexload-archives/prod/evidence/run-1/manifest.json" {
		t.Fatalf("manifest uri = %q", got)
	}
}

func TestCurrentArchiveBackendValidatesRequiredObjectTarget(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", "gcs")

	backend := currentArchiveBackend()
	if err := backend.validate(); err == nil {
		t.Fatal("expected missing bucket validation error")
	}
}

func TestUploadArchiveObjectsUploadsOnlyNamedFiles(t *testing.T) {
	localDir := t.TempDir()
	bucketDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(localDir, runArchiveManifestFileName), []byte(`{"test_id":"run-1"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, testNotesArchiveFileName), []byte(`{"notes":[]}`), 0o644); err != nil {
		t.Fatalf("write notes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(localDir, "transactions.jsonl"), []byte(`{"transaction":1}`), 0o644); err != nil {
		t.Fatalf("write transactions: %v", err)
	}
	bucket, err := fileblob.OpenBucket(bucketDir, nil)
	if err != nil {
		t.Fatalf("OpenBucket returned error: %v", err)
	}
	defer bucket.Close()
	backend := archiveBackendRuntime{Prefix: "prod/evidence"}

	if err := uploadArchiveObjectsToBucket(context.Background(), bucket, backend, localDir, "run-1", testNotesArchiveFileName, runArchiveManifestFileName); err != nil {
		t.Fatalf("uploadArchiveObjectsToBucket returned error: %v", err)
	}

	for _, name := range []string{runArchiveManifestFileName, testNotesArchiveFileName} {
		if _, err := os.Stat(filepath.Join(bucketDir, "prod", "evidence", "run-1", name)); err != nil {
			t.Fatalf("expected uploaded object %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(bucketDir, "prod", "evidence", "run-1", "transactions.jsonl")); !os.IsNotExist(err) {
		t.Fatalf("transactions artifact upload err = %v, want not uploaded", err)
	}
}
