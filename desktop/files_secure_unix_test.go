//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPublishExistingFileAtomicallyRestoresChangedDestination(t *testing.T) {
	dirPath := t.TempDir()
	targetPath := filepath.Join(dirPath, "result.json")
	if err := os.WriteFile(targetPath, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(targetPath, filepath.Join(dirPath, "selected.old")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "staging"), []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging, err := os.Lstat(filepath.Join(dirPath, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)

	preserve, err := publishExistingFileIfMatches(dirFD, "staging", "result.json", expected, staging)
	if err == nil || !strings.Contains(err.Error(), "changed during publication") {
		t.Fatalf("changed destination error = %v", err)
	}
	if preserve {
		t.Fatal("successful rollback incorrectly requested displaced-file retention")
	}
	got, err := os.ReadFile(targetPath)
	if err != nil || string(got) != "replacement" {
		t.Fatalf("changed destination was not restored: content=%q err=%v", got, err)
	}
	got, err = os.ReadFile(filepath.Join(dirPath, "staging"))
	if err != nil || string(got) != "saved" {
		t.Fatalf("staging file was not restored: content=%q err=%v", got, err)
	}
}

func TestPublishExistingFileRetainsUnknownFileAfterRacedRollback(t *testing.T) {
	dirPath := t.TempDir()
	targetPath := filepath.Join(dirPath, "result.json")
	if err := os.WriteFile(targetPath, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(targetPath, filepath.Join(dirPath, "selected.old")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(targetPath, []byte("first-racer"), 0o600); err != nil {
		t.Fatal(err)
	}
	stagingPath := filepath.Join(dirPath, "staging")
	if err := os.WriteFile(stagingPath, []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging, err := os.Lstat(stagingPath)
	if err != nil {
		t.Fatal(err)
	}
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)

	exchanges := 0
	exchange := func(oldDirFD int, oldName string, newDirFD int, newName string) error {
		exchanges++
		if exchanges == 2 {
			if err := os.Rename(targetPath, filepath.Join(dirPath, "staging-moved")); err != nil {
				return err
			}
			if err := os.WriteFile(targetPath, []byte("second-racer"), 0o600); err != nil {
				return err
			}
		}
		return nativeRenameExchange(oldDirFD, oldName, newDirFD, newName)
	}
	preserve, err := publishExistingFileIfMatchesWith(dirFD, "staging", "result.json", expected, staging, exchange)
	if err == nil || !strings.Contains(err.Error(), "unverified file retained") {
		t.Fatalf("second rollback race error = %v", err)
	}
	if !preserve {
		t.Fatal("unverified rollback file was not marked for retention")
	}
	got, err := os.ReadFile(stagingPath)
	if err != nil || string(got) != "second-racer" {
		t.Fatalf("raced rollback file was not retained: content=%q err=%v", got, err)
	}
}

func TestPublishExistingFileRejectsStagingPathReplacement(t *testing.T) {
	dirPath := t.TempDir()
	targetPath := filepath.Join(dirPath, "result.json")
	stagingPath := filepath.Join(dirPath, "staging")
	if err := os.WriteFile(targetPath, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected := mustFileInfo(t, targetPath)
	if err := os.WriteFile(stagingPath, []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging := mustFileInfo(t, stagingPath)
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)

	exchanges := 0
	exchange := func(oldDirFD int, oldName string, newDirFD int, newName string) error {
		exchanges++
		if exchanges == 1 {
			if err := os.Rename(stagingPath, filepath.Join(dirPath, "original-staging")); err != nil {
				return err
			}
			if err := os.WriteFile(stagingPath, []byte("foreign"), 0o600); err != nil {
				return err
			}
		}
		return nativeRenameExchange(oldDirFD, oldName, newDirFD, newName)
	}
	published, err := publishExistingFileIfMatchesWith(dirFD, "staging", "result.json", expected, staging, exchange)
	if err == nil || !strings.Contains(err.Error(), "source") {
		t.Fatalf("staging replacement error = %v", err)
	}
	if !published {
		t.Fatal("uncertain raced publication should prevent descriptor scrubbing")
	}
	if got, readErr := os.ReadFile(targetPath); readErr != nil || string(got) != "selected" {
		t.Fatalf("selected destination was not restored: content=%q err=%v", got, readErr)
	}
	if got, readErr := os.ReadFile(stagingPath); readErr != nil || string(got) != "foreign" {
		t.Fatalf("foreign staging entry was not retained: content=%q err=%v", got, readErr)
	}
}

func TestSecureWriteExistingFIFOReplacementDoesNotBlock(t *testing.T) {
	dirPath := t.TempDir()
	targetPath := filepath.Join(dirPath, "result.json")
	if err := os.WriteFile(targetPath, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, grant, err := newWritePathGrant(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(targetPath); err != nil {
		t.Fatal(err)
	}
	if err := unix.Mkfifo(targetPath, 0o600); err != nil {
		t.Skipf("FIFO unavailable: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		done <- secureWriteGrantedFile(grant, []byte("saved"))
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "refusing to overwrite") {
			t.Fatalf("FIFO replacement error = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("save blocked while opening a FIFO replacement")
	}
}

func TestPublishExistingFileReplacesOnlyExpectedDestination(t *testing.T) {
	dirPath := t.TempDir()
	targetPath := filepath.Join(dirPath, "result.json")
	if err := os.WriteFile(targetPath, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	expected, err := os.Lstat(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dirPath, "staging"), []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging, err := os.Lstat(filepath.Join(dirPath, "staging"))
	if err != nil {
		t.Fatal(err)
	}
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)

	published, err := publishExistingFileIfMatches(dirFD, "staging", "result.json", expected, staging)
	if err != nil {
		t.Fatal(err)
	}
	if !published {
		t.Fatal("successful publication did not report the staging inode as published")
	}
	got, err := os.ReadFile(targetPath)
	if err != nil || string(got) != "saved" {
		t.Fatalf("published content=%q err=%v", got, err)
	}
	if recovery, readErr := os.ReadFile(filepath.Join(dirPath, "staging")); readErr != nil || len(recovery) != 0 {
		t.Fatalf("verified displaced file was not retained as a scrubbed recovery tombstone: content=%q err=%v", recovery, readErr)
	}
}

func TestPublishExistingFileDoesNotScrubMultiplyLinkedDestination(t *testing.T) {
	dirPath := t.TempDir()
	targetPath := filepath.Join(dirPath, "result.json")
	aliasPath := filepath.Join(dirPath, "result-alias.json")
	stagingPath := filepath.Join(dirPath, "staging")
	if err := os.WriteFile(targetPath, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(targetPath, aliasPath); err != nil {
		t.Skipf("hard links unavailable: %v", err)
	}
	expected := mustFileInfo(t, targetPath)
	if err := os.WriteFile(stagingPath, []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging := mustFileInfo(t, stagingPath)
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)

	preserve, err := publishExistingFileIfMatches(dirFD, "staging", "result.json", expected, staging)
	if err == nil || !strings.Contains(err.Error(), "multiple hard links") {
		t.Fatalf("multiply-linked publication error = %v", err)
	}
	if !preserve {
		t.Fatal("multiply-linked displaced file was not retained for recovery")
	}
	if got, readErr := os.ReadFile(targetPath); readErr != nil || string(got) != "saved" {
		t.Fatalf("published target content=%q err=%v", got, readErr)
	}
	if got, readErr := os.ReadFile(aliasPath); readErr != nil || string(got) != "selected" {
		t.Fatalf("hard-linked prior content was destroyed: content=%q err=%v", got, readErr)
	}
	if got, readErr := os.ReadFile(stagingPath); readErr != nil || string(got) != "selected" {
		t.Fatalf("displaced recovery content=%q err=%v", got, readErr)
	}
}

func TestSecureWriteReusesScrubbedRecoveryTombstoneAtCap(t *testing.T) {
	dirPath := t.TempDir()
	targetPath := filepath.Join(dirPath, "result.json")
	if err := os.WriteFile(targetPath, []byte("selected"), 0o600); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxRetainedUnixSaveRecoveryFiles; index++ {
		name := filepath.Join(dirPath, fmt.Sprintf(".apexload-save-%032x.tmp", index))
		if err := os.WriteFile(name, nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	probe, err := os.OpenFile(filepath.Join(dirPath, fmt.Sprintf(".apexload-save-%032x.tmp", 0)), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	reuseErr := unixSaveTombstoneMetadataClean(int(probe.Fd()))
	if closeErr := probe.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if reuseErr != nil {
		t.Skipf("verified tombstone reuse is unavailable on this platform: %v", reuseErr)
	}
	_, grant, err := newWritePathGrant(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFile(grant, []byte("saved")); err != nil {
		t.Fatalf("save with reusable recovery tombstone: %v", err)
	}
	if got, err := os.ReadFile(targetPath); err != nil || string(got) != "saved" {
		t.Fatalf("published content=%q err=%v", got, err)
	}
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		t.Fatal(err)
	}
	retained := 0
	for _, entry := range entries {
		if isUnixSaveRecoveryName(entry.Name()) {
			retained++
		}
	}
	if retained != maxRetainedUnixSaveRecoveryFiles {
		t.Fatalf("retained tombstones = %d, want stable count %d", retained, maxRetainedUnixSaveRecoveryFiles)
	}
}

func TestSecureWriteCapsNonReusableRecoveryFiles(t *testing.T) {
	dirPath := t.TempDir()
	targetPath := filepath.Join(dirPath, "result.json")
	for index := 0; index < maxRetainedUnixSaveRecoveryFiles; index++ {
		name := filepath.Join(dirPath, fmt.Sprintf(".apexload-save-%032x.tmp", index))
		if err := os.WriteFile(name, []byte("retained recovery data"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	_, grant, err := newWritePathGrant(targetPath)
	if err != nil {
		t.Fatal(err)
	}
	err = secureWriteGrantedFile(grant, []byte("saved"))
	if err == nil || !strings.Contains(err.Error(), "non-reusable recovery files") {
		t.Fatalf("recovery file cap error = %v", err)
	}
	if _, err := os.Lstat(targetPath); !os.IsNotExist(err) {
		t.Fatalf("capped save published a destination: %v", err)
	}
}

func TestPublishNewFileUsesNativeRenameWhenHardLinksAreUnsupported(t *testing.T) {
	dirPath := t.TempDir()
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)
	if err := os.WriteFile(filepath.Join(dirPath, "staging"), []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}

	published, err := publishNewFileNoReplaceWith(
		dirFD,
		"staging",
		"result.json",
		mustFileInfo(t, filepath.Join(dirPath, "staging")),
		func(oldDirFD int, oldName string, newDirFD int, newName string) error {
			// Renameat models a successful native rename. The helper's no-clobber
			// behavior itself is covered against the real platform primitive below.
			return unix.Renameat(oldDirFD, oldName, newDirFD, newName)
		},
	)
	if err != nil {
		t.Fatalf("publish through native rename: %v", err)
	}
	if !published {
		t.Fatal("native rename success did not report the staging inode as published")
	}
	got, err := os.ReadFile(filepath.Join(dirPath, "result.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "saved" {
		t.Fatalf("published content = %q, want saved", got)
	}
	if _, err := os.Stat(filepath.Join(dirPath, "staging")); !os.IsNotExist(err) {
		t.Fatalf("native rename left staging name behind: %v", err)
	}
}

func TestPublishNewFileRejectsStagingPathReplacement(t *testing.T) {
	dirPath := t.TempDir()
	stagingPath := filepath.Join(dirPath, "staging")
	if err := os.WriteFile(stagingPath, []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}
	staging := mustFileInfo(t, stagingPath)
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)

	published, err := publishNewFileNoReplaceWith(
		dirFD,
		"staging",
		"result.json",
		staging,
		func(oldDirFD int, oldName string, newDirFD int, newName string) error {
			if err := os.Rename(stagingPath, filepath.Join(dirPath, "original-staging")); err != nil {
				return err
			}
			if err := os.WriteFile(stagingPath, []byte("foreign"), 0o600); err != nil {
				return err
			}
			return nativeRenameNoReplace(oldDirFD, oldName, newDirFD, newName)
		},
	)
	if err == nil || !strings.Contains(err.Error(), "identity does not match") {
		t.Fatalf("staging replacement error = %v", err)
	}
	if published {
		t.Fatal("foreign file was reported as the published staging inode")
	}
	if got, readErr := os.ReadFile(filepath.Join(dirPath, "result.json")); readErr != nil || string(got) != "foreign" {
		t.Fatalf("unverified published entry was not retained: content=%q err=%v", got, readErr)
	}
	if got, readErr := os.ReadFile(filepath.Join(dirPath, "original-staging")); readErr != nil || string(got) != "saved" {
		t.Fatalf("original staging inode was not preserved: content=%q err=%v", got, readErr)
	}
}

func TestPublishNewFileFailsClosedWhenNativePrimitiveIsUnavailable(t *testing.T) {
	dirPath := t.TempDir()
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)
	if err := os.WriteFile(filepath.Join(dirPath, "staging"), []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = publishNewFileNoReplaceWith(
		dirFD,
		"staging",
		"result.json",
		mustFileInfo(t, filepath.Join(dirPath, "staging")),
		func(_ int, _ string, _ int, _ string) error { return unix.ENOSYS },
	)
	if !errors.Is(err, unix.ENOSYS) {
		t.Fatalf("unsupported native publication error = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirPath, "result.json")); !os.IsNotExist(err) {
		t.Fatalf("unsupported publication created destination: %v", err)
	}
	got, readErr := os.ReadFile(filepath.Join(dirPath, "staging"))
	if readErr != nil || string(got) != "saved" {
		t.Fatalf("unsupported publication did not retain staging file: content=%q err=%v", got, readErr)
	}
}

func TestPublishNewFilePropagatesNativePublicationError(t *testing.T) {
	dirPath := t.TempDir()
	dirFD, err := unix.Open(dirPath, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer unix.Close(dirFD)
	if err := os.WriteFile(filepath.Join(dirPath, "staging"), []byte("saved"), 0o600); err != nil {
		t.Fatal(err)
	}

	_, err = publishNewFileNoReplaceWith(
		dirFD,
		"staging",
		"result.json",
		mustFileInfo(t, filepath.Join(dirPath, "staging")),
		func(_ int, _ string, _ int, _ string) error { return unix.EOPNOTSUPP },
	)
	if !errors.Is(err, unix.EOPNOTSUPP) {
		t.Fatalf("expected native publication error, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(dirPath, "result.json")); !os.IsNotExist(err) {
		t.Fatalf("failed publication created destination: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dirPath, "staging"))
	if err != nil || string(got) != "saved" {
		t.Fatalf("failed publication damaged staging file: content=%q err=%v", got, err)
	}
}

func mustFileInfo(t *testing.T, path string) os.FileInfo {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info
}
