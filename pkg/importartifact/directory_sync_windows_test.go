//go:build windows

package importartifact

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/windows"
)

func TestWritableEncryptedStoreUsesWindowsDurableNamespaceOperations(t *testing.T) {
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{
		Key: bytes.Repeat([]byte{0x5a}, 32), KeyID: "windows-durability-test",
	})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewEncryptedStore(StoreOptions{
		RootDir: t.TempDir(),
		Wrapper: wrapper,
	})
	if err != nil {
		t.Fatalf("NewEncryptedStore: %v", err)
	}
	source := filepath.Join(store.root, "source.tmp")
	if err := os.WriteFile(source, []byte("durable"), 0o600); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(store.root, "published.dat")
	if err := store.linkDurably(source, target); err != nil {
		t.Fatalf("linkDurably: %v", err)
	}
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("published target: %v", err)
	}
	if _, err := os.Stat(source); !os.IsNotExist(err) {
		t.Fatalf("source should have been consumed: %v", err)
	}
	if err := store.removeDurably(target); err != nil {
		t.Fatalf("removeDurably: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("removed target remains visible: %v", err)
	}

	conflictSource := filepath.Join(store.root, "conflict.tmp")
	if err := os.WriteFile(conflictSource, []byte("new"), 0o600); err != nil {
		t.Fatal(err)
	}
	conflictTarget := filepath.Join(store.root, "conflict.dat")
	if err := os.WriteFile(conflictTarget, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.linkDurably(conflictSource, conflictTarget); !errors.Is(err, os.ErrExist) {
		t.Fatalf("create-only collision = %v, want os.ErrExist", err)
	}
}

func TestWindowsProtectedImportVolumePolicy(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		drive      uint32
		filesystem string
		wantErr    bool
	}{
		{name: "NTFS", drive: windows.DRIVE_FIXED, filesystem: "NTFS"},
		{name: "ReFS", drive: windows.DRIVE_FIXED, filesystem: "ReFS"},
		{name: "SMB", drive: windows.DRIVE_REMOTE, filesystem: "NTFS", wantErr: true},
		{name: "removable", drive: windows.DRIVE_REMOVABLE, filesystem: "NTFS", wantErr: true},
		{name: "FAT32", drive: windows.DRIVE_FIXED, filesystem: "FAT32", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := validateWindowsVolume(test.drive, test.filesystem)
			if test.wantErr != errors.Is(err, ErrUnsupportedFilesystem) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestWindowsWriteThroughPublicationSupportsExtendedPathsAndCleansTombstones(t *testing.T) {
	base := t.TempDir()
	root := base
	for len(root) < 300 {
		root = filepath.Join(root, strings.Repeat("segment", 4))
	}
	wrapper, err := NewLocalAESKeyWrapper(LocalKeyOptions{Key: bytes.Repeat([]byte{0x31}, 32), KeyID: "long-path"})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper})
	if err != nil {
		t.Fatal(err)
	}
	longDir := filepath.Join(store.root, "nested")
	if err := mkdirAllDurably(longDir, 0o700); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(longDir, "source.tmp")
	target := filepath.Join(longDir, "target.dat")
	if err := os.WriteFile(source, []byte("long"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := store.linkDurably(source, target); err != nil {
		t.Fatalf("long publish: %v", err)
	}
	if err := store.removeDurably(target); err != nil {
		t.Fatalf("long remove: %v", err)
	}

	tombstone := filepath.Join(root, durableTombstoneDirectory, "stale")
	if err := os.MkdirAll(filepath.Dir(tombstone), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tombstone, []byte("stale"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewEncryptedStore(StoreOptions{RootDir: root, Wrapper: wrapper}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(tombstone); !os.IsNotExist(err) {
		t.Fatalf("stale tombstone remains: %v", err)
	}
}

func TestWindowsDurableDirectoryCreationRecoversPendingPublication(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "protected", "sessions")
	pending := durableDirectoryPendingPath(target)
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAllDurably(target, 0o700); err != nil {
		t.Fatalf("recover pending directory publication: %v", err)
	}
	if info, err := os.Stat(target); err != nil || !info.IsDir() {
		t.Fatalf("published directory = %#v, %v", info, err)
	}
	if _, err := os.Stat(pending); !os.IsNotExist(err) {
		t.Fatalf("pending directory remains: %v", err)
	}
}

func TestWindowsDurableDirectoryCreationRejectsUnexpectedPendingContent(t *testing.T) {
	root := t.TempDir()
	target := filepath.Join(root, "protected")
	pending := durableDirectoryPendingPath(target)
	if err := os.Mkdir(pending, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pending, "unexpected"), []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mkdirAllDurably(target, 0o700); err == nil || !strings.Contains(err.Error(), "is not empty") {
		t.Fatalf("mkdirAllDurably error = %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("unexpected pending content was published: %v", err)
	}
}
