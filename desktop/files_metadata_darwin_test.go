//go:build darwin && cgo

package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

func TestSecureSavePreservesMacOSACLAndExtendedAttributes(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "recording.har")
	if err := os.WriteFile(target, []byte("old"), 0o640); err != nil {
		t.Fatal(err)
	}
	const attributeName = "com.apexload.metadata-test"
	const attributeValue = "preserve-this-label"
	if err := unix.Setxattr(target, attributeName, []byte(attributeValue), 0); err != nil {
		t.Fatalf("set test extended attribute: %v", err)
	}
	if output, err := exec.Command("chmod", "+a", "everyone allow read", target).CombinedOutput(); err != nil {
		t.Fatalf("set test ACL: %v: %s", err, output)
	}

	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := secureWriteGrantedFile(grant, []byte("new HAR payload")); err != nil {
		t.Fatal(err)
	}

	data, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new HAR payload" {
		t.Fatalf("saved content = %q", data)
	}
	attribute := make([]byte, 256)
	read, err := unix.Getxattr(target, attributeName, attribute)
	if err != nil {
		t.Fatalf("read preserved extended attribute: %v", err)
	}
	if got := string(attribute[:read]); got != attributeValue {
		t.Fatalf("preserved extended attribute = %q, want %q", got, attributeValue)
	}
	aclOutput, err := exec.Command("ls", "-led", target).CombinedOutput()
	if err != nil {
		t.Fatalf("inspect preserved ACL: %v: %s", err, aclOutput)
	}
	if !strings.Contains(string(aclOutput), "group:everyone allow read") {
		t.Fatalf("saved file lost its ACL:\n%s", aclOutput)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if got := info.Mode().Perm(); got != 0o640 {
		t.Fatalf("saved file mode = %o, want 640", got)
	}
}

func TestSecureSavePreservesMacOSSpecialPermissionBits(t *testing.T) {
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
	if uint32(before.Mode)&0o6000 != 0o6000 {
		t.Skipf("test filesystem did not retain set-id bits: mode=%#o", uint32(before.Mode)&0o7777)
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
	if got, want := uint32(after.Mode)&0o7777, uint32(before.Mode)&0o7777; got != want {
		t.Fatalf("saved file mode = %#o, want %#o", got, want)
	}
}

func TestSecureSaveFailsClosedForImmutableMacOSMetadata(t *testing.T) {
	target := filepath.Join(t.TempDir(), "immutable.har")
	if err := os.WriteFile(target, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := unix.Chflags(target, unix.UF_IMMUTABLE); err != nil {
		t.Fatalf("mark test file immutable: %v", err)
	}
	t.Cleanup(func() {
		if err := unix.Chflags(target, 0); err != nil && !os.IsNotExist(err) {
			t.Errorf("clear immutable test flag: %v", err)
		}
	})

	_, grant, err := newWritePathGrant(target)
	if err != nil {
		t.Fatal(err)
	}
	err = secureWriteGrantedFile(grant, []byte("replacement"))
	if err == nil || !strings.Contains(err.Error(), "immutable flags") {
		t.Fatalf("secureWriteGrantedFile error = %v, want immutable metadata refusal", err)
	}
	data, readErr := os.ReadFile(target)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(data) != "original" {
		t.Fatalf("failed replacement changed immutable file to %q", data)
	}
}

func TestSecureSaveScrubsMacOSTombstoneMetadataBeforeReuse(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "recording.har")
	if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	const attributeName = "com.apexload.tombstone-test"
	if err := unix.Setxattr(target, attributeName, []byte("must-not-survive"), 0); err != nil {
		t.Fatal(err)
	}
	if output, err := exec.Command("chmod", "+a", "everyone allow read", target).CombinedOutput(); err != nil {
		t.Fatalf("set test ACL: %v: %s", err, output)
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
	tombstone, err := os.OpenFile(tombstonePath, os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	metadataErr := unixSaveTombstoneMetadataClean(int(tombstone.Fd()))
	if closeErr := tombstone.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if metadataErr != nil {
		t.Fatalf("scrubbed tombstone metadata: %v", metadataErr)
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
	if _, err := unix.Getxattr(target, attributeName, make([]byte, 64)); !errors.Is(err, unix.ENOATTR) {
		t.Fatalf("reused file retained stale extended attribute: %v", err)
	}
	published, err := os.Open(target)
	if err != nil {
		t.Fatal(err)
	}
	metadataErr = unixSaveTombstoneMetadataClean(int(published.Fd()))
	if closeErr := published.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	if metadataErr != nil {
		t.Fatalf("reused published file metadata: %v", metadataErr)
	}
}
