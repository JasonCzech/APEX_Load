package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// RN-3: the note-sync trust-anchor refresh must publish a new archive record
// pointer (copy-and-swap) rather than mutating the shared record in place, so a
// concurrent verify/report reader holding the record pointer never sees a torn
// ManifestSHA256 write.
func TestReplaceRunArchiveManifestTrustSwapsWithoutMutating(t *testing.T) {
	dir := t.TempDir()
	manifestPath := filepath.Join(dir, "manifest.json")
	if err := os.WriteFile(manifestPath, []byte(`{"schema":"x"}`), 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	original := &storage.RunArchiveRecord{TestID: "run-1", ManifestPath: manifestPath, ManifestSHA256: "stale"}
	tr := &TestRunner{archives: map[string]*storage.RunArchiveRecord{"run-1": original}}

	if err := tr.replaceRunArchiveManifestTrust("run-1"); err != nil {
		t.Fatalf("replaceRunArchiveManifestTrust returned error: %v", err)
	}
	if original.ManifestSHA256 != "stale" {
		t.Fatalf("original record mutated in place (ManifestSHA256=%q); that is the data race", original.ManifestSHA256)
	}
	updated := tr.archives["run-1"]
	if updated == original {
		t.Fatalf("archive record pointer was not swapped")
	}
	want, err := sha256File(manifestPath)
	if err != nil {
		t.Fatalf("sha256File returned error: %v", err)
	}
	if updated.ManifestSHA256 != want {
		t.Fatalf("updated ManifestSHA256 = %q, want %q", updated.ManifestSHA256, want)
	}
}
