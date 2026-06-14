package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDefaultArchiveDirIsAbsolute(t *testing.T) {
	if got := DefaultArchiveDir(); !filepath.IsAbs(got) {
		t.Fatalf("DefaultArchiveDir() = %q, want an absolute path", got)
	}
}

// TestDefaultArchiveDirOutsideModuleTree guards the property that motivated the
// default: per-run archive directories must never land inside the repository,
// or `go list all` / `wails build` (which walk the module tree) trip over the
// thousands of UUID-named directories the archive subsystem creates.
func TestDefaultArchiveDirOutsideModuleTree(t *testing.T) {
	moduleRoot := moduleRootDir(t)
	def := DefaultArchiveDir()
	if isWithin(moduleRoot, def) {
		t.Fatalf("DefaultArchiveDir() = %q must not be inside the module tree %q", def, moduleRoot)
	}
}

func TestArchiveDirHonorsOverride(t *testing.T) {
	const override = "/custom/apexload/archives"
	t.Setenv(EnvArchiveDir, override)
	if got := ArchiveDir(); got != override {
		t.Fatalf("ArchiveDir() = %q, want override %q", got, override)
	}
}

func TestArchiveDirFallsBackToDefaultWhenBlank(t *testing.T) {
	t.Setenv(EnvArchiveDir, "   ")
	if got, want := ArchiveDir(), DefaultArchiveDir(); got != want {
		t.Fatalf("ArchiveDir() = %q, want DefaultArchiveDir() %q", got, want)
	}
}

// isWithin reports whether child is parent itself or nested under it.
func isWithin(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		// Different volumes have no relative path: definitively not nested.
		return false
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return false
	}
	return true
}

// moduleRootDir walks up from this test file until it finds the go.mod that
// marks the module root.
func moduleRootDir(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed to locate this test file")
	}
	dir := filepath.Dir(file)
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not locate module root (no go.mod found)")
		}
		dir = parent
	}
}
