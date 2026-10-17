package browser

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// A record's identity is shaped for its platform: Unix ledgers keep the
// device, inode and UID alone (byte-identical to before the Windows fields
// existed), Windows records carry the high file ID half and the owner SID
// with UID 0, and each platform refuses the other's shape.
func TestHARPersistentIdentityShapes(t *testing.T) {
	unix := harPersistentIdentity{Device: 1, Inode: 2, UID: 501}
	data, err := json.Marshal(unix)
	if err != nil || string(data) != `{"device":1,"inode":2,"uid":501}` {
		t.Fatalf("a Unix identity encodes as %s (%v)", data, err)
	}
	windowsShaped := harPersistentIdentity{Device: 1, Inode: 2, FileIDHigh: 3, Owner: "S-1-5-21-1-2-3-1001"}
	data, err = json.Marshal(windowsShaped)
	if err != nil || string(data) != `{"device":1,"inode":2,"uid":0,"file_id_high":3,"owner":"S-1-5-21-1-2-3-1001"}` {
		t.Fatalf("a Windows identity encodes as %s (%v)", data, err)
	}
	if runtime.GOOS == "windows" {
		if validHARPersistentIdentity(harPersistentIdentity{Device: 1, Inode: 2, UID: 501}) {
			t.Fatal("Windows accepted a Unix-shaped identity")
		}
		if validHARPersistentIdentity(harPersistentIdentity{Device: 1, Inode: 2, Owner: "S-1-5-18"}) {
			t.Fatal("Windows accepted an identity another owner holds")
		}
		return
	}
	self := harPersistentIdentity{Device: 1, Inode: 2, UID: uint32(os.Geteuid())}
	if !validHARPersistentIdentity(self) {
		t.Fatal("this user's Unix identity was refused")
	}
	for _, shaped := range []harPersistentIdentity{
		{Device: 1, Inode: 2, UID: uint32(os.Geteuid()), Owner: "S-1-5-18"},
		{Device: 1, Inode: 2, UID: uint32(os.Geteuid()), FileIDHigh: 7},
		{Device: 1, Inode: 0, UID: uint32(os.Geteuid())},
		{Device: 1, Inode: 2, UID: uint32(os.Geteuid()) + 1},
	} {
		if validHARPersistentIdentity(shaped) {
			t.Fatalf("Unix accepted %+v", shaped)
		}
	}
}

// The identity a retirement record binds, read for a real generation, is
// valid on this platform, stable across the tombstone rename and refuses a
// FileInfo of another object.
func TestHARPersistentIdentityAtIsStableAcrossRename(t *testing.T) {
	dir := t.TempDir()
	n, err := lockHARNamespace(context.Background(), dir)
	if err != nil {
		t.Fatal(err)
	}
	defer n.close()
	generation, err := harRandomGeneration()
	if err != nil {
		t.Fatal(err)
	}
	if err := n.work.Mkdir(generation, 0o700); err != nil {
		t.Fatal(err)
	}
	info, err := n.work.Lstat(generation)
	if err != nil {
		t.Fatal(err)
	}
	id, err := persistentHARIdentityAt(n.work, generation, info)
	if err != nil || !validHARPersistentIdentity(id) {
		t.Fatalf("identity %+v (%v)", id, err)
	}
	if err := renameHARNoReplace(n.work, generation, ".retired-"+generation); err != nil {
		t.Fatal(err)
	}
	renamed, err := n.work.Lstat(".retired-" + generation)
	if err != nil {
		t.Fatal(err)
	}
	after, err := persistentHARIdentityAt(n.work, ".retired-"+generation, renamed)
	if err != nil || after != id {
		t.Fatalf("identity changed across the rename: %+v -> %+v (%v)", id, after, err)
	}
	if err := n.work.Mkdir("other", 0o700); err != nil {
		t.Fatal(err)
	}
	other, err := n.work.Lstat("other")
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		// Windows reads identity through a handle to the name, so a FileInfo
		// of another object is refused.
		if _, err := persistentHARIdentityAt(n.work, ".retired-"+generation, other); err == nil {
			t.Fatal("a FileInfo of another object was accepted")
		}
	}
	// A create-only rename never replaces.
	if err := renameHARNoReplace(n.work, "other", ".retired-"+generation); err == nil {
		t.Fatal("a create-only rename replaced its target")
	}
	if _, err := os.Stat(filepath.Join(dir, runtimeHARWorkingDirectory, "other")); err != nil {
		t.Fatal(err)
	}
}
