package main

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestEnsurePrivateDirectoryMigratesExistingMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by Unix mode bits")
	}
	dir := filepath.Join(t.TempDir(), "state")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := ensurePrivateDirectory(dir); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != privateDirectoryMode {
		t.Fatalf("directory mode = %#o, want %#o", got, privateDirectoryMode)
	}
}

func TestWritePrivateFileAtomicUsesOwnerOnlyMode(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "state.json")
	if err := writePrivateFileAtomic(path, []byte(`{"ok":true}`)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != `{"ok":true}` {
		t.Fatalf("content = %q", got)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != privateFileMode {
			t.Fatalf("file mode = %#o, want %#o", mode, privateFileMode)
		}
	}
}

func TestHardenPrivateStateFilesMigratesLegacyModes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows ACLs are not represented by Unix mode bits")
	}
	dir := t.TempDir()
	for _, name := range []string{windowStateFileName, recentsFileName} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := hardenPrivateStateFiles(dir); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{windowStateFileName, recentsFileName} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != privateFileMode {
			t.Errorf("%s mode = %#o, want %#o", name, got, privateFileMode)
		}
	}
}
