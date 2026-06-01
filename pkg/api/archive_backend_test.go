package api

import (
	"path/filepath"
	"testing"
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
