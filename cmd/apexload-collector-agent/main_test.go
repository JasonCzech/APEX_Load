package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestReadProtectedFileBoundsAndPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permission contract")
	}
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte("test-key"), 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := readProtectedFile(path); err != nil || string(value) != "test-key" {
		t.Fatalf("protected read: %q %v", value, err)
	}
	if err := os.Chmod(path, 0640); err != nil {
		t.Fatal(err)
	}
	if _, err := readProtectedFile(path); err == nil {
		t.Fatal("group-readable key accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, (64<<10)+1), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := readProtectedFile(path); err == nil {
		t.Fatal("oversized key accepted")
	}
	if _, err := readProtectedFile(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}
