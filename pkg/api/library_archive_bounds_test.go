package api

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeLibraryArchiveZip(t *testing.T, names []string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bundle.zip")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, name := range names {
		entry, err := writer.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = entry.Write([]byte("payload"))
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestUnzipArchiveFilePreflightsNamesAndRejectsDuplicates(t *testing.T) {
	longName := strings.Repeat("x", maxLibraryArchiveEntryNameBytes+1)
	if err := unzipArchiveFile(writeLibraryArchiveZip(t, []string{longName}), t.TempDir(), 1<<20); err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("long-name error = %v", err)
	}
	if err := unzipArchiveFile(writeLibraryArchiveZip(t, []string{"same.json", "same.json"}), t.TempDir(), 1<<20); err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("duplicate error = %v", err)
	}
}

func TestUnzipArchiveFileCreatesPrivateArtifacts(t *testing.T) {
	destination := filepath.Join(t.TempDir(), "unpacked")
	if err := unzipArchiveFile(writeLibraryArchiveZip(t, []string{"nested/manifest.json"}), destination, 1<<20); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(filepath.Join(destination, "nested", "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("artifact mode = %#o, want 0600", got)
	}
}
