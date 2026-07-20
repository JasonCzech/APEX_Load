//go:build linux

package main

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSecureSavePreservesLinuxExtendedAttributes(t *testing.T) {
	target := filepath.Join(t.TempDir(), "recording.har")
	if err := os.WriteFile(target, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	const attributeName = "user.apexload.metadata-test"
	const attributeValue = "preserve-this-label"
	if err := unix.Setxattr(target, attributeName, []byte(attributeValue), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("test filesystem does not support extended attributes: %v", err)
		}
		t.Fatalf("set test extended attribute: %v", err)
	}
	flagsPreserved := false
	file, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	flags, flagsSupported, flagsErr := readLinuxFileFlags(int(file.Fd()))
	if flagsErr == nil && flagsSupported {
		flagsErr = unix.IoctlSetPointerInt(int(file.Fd()), unix.FS_IOC_SETFLAGS, flags|linuxFSNoDumpFlag)
		flagsPreserved = flagsErr == nil
	}
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if flagsErr != nil && !errors.Is(flagsErr, unix.EPERM) && !errors.Is(flagsErr, unix.EACCES) {
		t.Fatalf("set test no-dump flag: %v", flagsErr)
	}

	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFile(grant, []byte("new HAR payload")); err != nil {
		t.Fatal(err)
	}

	attribute := make([]byte, 256)
	read, err := unix.Getxattr(target, attributeName, attribute)
	if err != nil {
		t.Fatalf("read preserved extended attribute: %v", err)
	}
	if got := string(attribute[:read]); got != attributeValue {
		t.Fatalf("preserved extended attribute = %q, want %q", got, attributeValue)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("saved file mode = %o, want 640", got)
	}
	if flagsPreserved {
		saved, openErr := os.Open(target)
		if openErr != nil {
			t.Fatal(openErr)
		}
		savedFlags, supported, readErr := readLinuxFileFlags(int(saved.Fd()))
		_ = saved.Close()
		if readErr != nil || !supported || savedFlags&linuxFSNoDumpFlag == 0 {
			t.Fatalf("saved file no-dump flag = %#x, supported=%t, error=%v", savedFlags, supported, readErr)
		}
	}
}

func TestSecureSavePreservesLinuxSpecialPermissionBits(t *testing.T) {
	target := filepath.Join(t.TempDir(), "set-id.har")
	if err := os.WriteFile(target, []byte("old"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(target, 0o750|os.ModeSetuid|os.ModeSetgid); err != nil {
		t.Fatal(err)
	}
	var before unix.Stat_t
	if err := unix.Stat(target, &before); err != nil {
		t.Fatal(err)
	}
	if before.Mode&0o6000 != 0o6000 {
		t.Skipf("test filesystem did not retain set-id bits: mode=%#o", before.Mode&0o7777)
	}

	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFile(grant, []byte("new payload")); err != nil {
		t.Fatal(err)
	}

	var after unix.Stat_t
	if err := unix.Stat(target, &after); err != nil {
		t.Fatal(err)
	}
	if got, want := after.Mode&0o7777, before.Mode&0o7777; got != want {
		t.Fatalf("saved file mode = %#o, want %#o", got, want)
	}
}

func TestSecureSaveScrubsLinuxTombstoneMetadataBeforeReuse(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "recording.har")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	const attributeName = "user.apexload.tombstone-test"
	if err := unix.Setxattr(target, attributeName, []byte("must-not-survive"), 0); err != nil {
		if errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.EOPNOTSUPP) {
			t.Skipf("test filesystem does not support extended attributes: %v", err)
		}
		t.Fatalf("set test extended attribute: %v", err)
	}
	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFile(grant, []byte("replacement")); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var tombstonePath string
	for _, entry := range entries {
		if isUnixSaveRecoveryName(entry.Name()) {
			tombstonePath = filepath.Join(dir, entry.Name())
			break
		}
	}
	if tombstonePath == "" {
		t.Fatal("secure save did not retain a recovery tombstone")
	}
	tombstoneIdentity, err := os.Stat(tombstonePath)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(tombstonePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	attributes, supported, metadataErr := readLinuxFDXattrs(int(file.Fd()))
	flags, flagsSupported, flagsErr := readLinuxFileFlags(int(file.Fd()))
	if closeErr := file.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if metadataErr != nil || (supported && len(attributes) != 0) {
		t.Fatalf("scrubbed tombstone xattrs=%v supported=%t err=%v", attributes, supported, metadataErr)
	}
	if flagsErr != nil || (flagsSupported && flags&linuxFSUserModifiableFlags != 0) {
		t.Fatalf("scrubbed tombstone flags=%#x supported=%t err=%v", flags, flagsSupported, flagsErr)
	}
	if tombstoneIdentity.Size() != 0 || tombstoneIdentity.Mode().Perm() != 0o600 {
		t.Fatalf("scrubbed tombstone size=%d mode=%o", tombstoneIdentity.Size(), tombstoneIdentity.Mode().Perm())
	}

	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	_, newGrant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFile(newGrant, []byte("new file")); err != nil {
		t.Fatalf("reuse scrubbed tombstone: %v", err)
	}
	publishedIdentity, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(tombstoneIdentity, publishedIdentity) {
		t.Fatal("new-file save did not reuse the verified scrubbed tombstone")
	}
	if _, err := unix.Getxattr(target, attributeName, make([]byte, 64)); !errors.Is(err, unix.ENODATA) {
		t.Fatalf("reused file retained stale extended attribute: %v", err)
	}
}
