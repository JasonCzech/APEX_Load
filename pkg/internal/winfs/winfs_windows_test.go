//go:build windows

package winfs

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestValidateVolume(t *testing.T) {
	for _, test := range []struct {
		name        string
		drive       uint32
		filesystem  string
		filesystems []string
		ok          bool
	}{
		{"NTFS by default", windows.DRIVE_FIXED, "NTFS", nil, true},
		{"lower-case NTFS", windows.DRIVE_FIXED, "ntfs", nil, true},
		{"ReFS refused by default", windows.DRIVE_FIXED, "ReFS", nil, false},
		{"ReFS when named", windows.DRIVE_FIXED, "ReFS", []string{"NTFS", "REFS"}, true},
		{"SMB", windows.DRIVE_REMOTE, "NTFS", nil, false},
		{"removable", windows.DRIVE_REMOVABLE, "NTFS", nil, false},
		{"RAM disk", windows.DRIVE_RAMDISK, "NTFS", nil, false},
		{"FAT32", windows.DRIVE_FIXED, "FAT32", nil, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := ValidateVolume(test.drive, test.filesystem, test.filesystems...)
			if test.ok != (err == nil) || err != nil && !errors.Is(err, ErrUnsupportedVolume) {
				t.Fatalf("ValidateVolume = %v", err)
			}
		})
	}
}

func TestValidateLocalVolume(t *testing.T) {
	if err := ValidateLocalVolume(t.TempDir()); err != nil {
		t.Fatalf("the test's temporary directory (local NTFS) was refused: %v", err)
	}
	if err := ValidateLocalVolume(ExtendedPath(t.TempDir())); err != nil {
		t.Fatalf("an extended local path was refused: %v", err)
	}
	for _, path := range []string{`\\localhost\C$\Windows`, `\\?\UNC\localhost\C$\Windows`, `\\.\C:\Windows`} {
		if err := ValidateLocalVolume(path); !errors.Is(err, ErrUnsupportedVolume) {
			t.Fatalf("ValidateLocalVolume(%s) = %v, want a network or device refusal", path, err)
		}
	}
}

func TestExtendedPath(t *testing.T) {
	for in, want := range map[string]string{
		`C:\work\file`:           `\\?\C:\work\file`,
		`\\?\C:\work\file`:       `\\?\C:\work\file`,
		`\\server\share\file`:    `\\?\UNC\server\share\file`,
		`C:\work\..\work\.\file`: `\\?\C:\work\file`,
	} {
		if got := ExtendedPath(in); got != want {
			t.Errorf("ExtendedPath(%s) = %s, want %s", in, got, want)
		}
	}
}

func TestMoveFileWriteThrough(t *testing.T) {
	dir := t.TempDir()
	source, target := filepath.Join(dir, "source"), filepath.Join(dir, "target")
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write(source, "new")
	write(target, "old")
	if err := MoveFileWriteThrough(source, target, false); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("create-only move onto an existing name = %v", err)
	}
	if err := MoveFileWriteThrough(source, target, true); err != nil {
		t.Fatalf("replacing move = %v", err)
	}
	if data, _ := os.ReadFile(target); string(data) != "new" {
		t.Fatalf("target holds %q", data)
	}
	if err := MoveFileWriteThrough(source, target, false); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("move of a missing source = %v", err)
	}
	long := dir
	for len(long) < 300 {
		long = filepath.Join(long, strings.Repeat("segment", 4))
	}
	if err := os.MkdirAll(ExtendedPath(long), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := MoveFileWriteThrough(target, filepath.Join(long, "moved"), false); err != nil {
		t.Fatalf("move into a long path = %v", err)
	}
}

func openDir(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

// openShareAll opens path for reading while sharing read, write and delete,
// as antivirus and indexing scans do (os.Open does not share delete).
func openShareAll(t *testing.T, path string) *os.File {
	t.Helper()
	p, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	h, err := windows.CreateFile(p, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, windows.FILE_FLAG_BACKUP_SEMANTICS, 0)
	if err != nil {
		t.Fatal(err)
	}
	return os.NewFile(uintptr(h), path)
}

func TestCreatePrivateDirAtIsPrivateAndInherited(t *testing.T) {
	user, err := CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	parentPath := t.TempDir()
	parent := openDir(t, parentPath)
	private, err := CreatePrivateDirAt(parent, ".private")
	if err != nil {
		t.Fatal(err)
	}
	defer private.Close()
	if err := CheckPrivate(private, false); err != nil {
		t.Fatalf("a created private directory is not private: %v", err)
	}
	if err := CheckPrivate(private, true); err == nil {
		t.Fatal("a directory passed as a regular file")
	}
	sd, err := windows.GetSecurityInfo(windows.Handle(private.Fd()), windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION)
	if err != nil {
		t.Fatal(err)
	}
	// Compare as Windows renders both: it prints a well-known account's SID
	// by its alias (LA for the built-in Administrator).
	want, err := windows.SecurityDescriptorFromString(PrivateDirectorySDDL(user))
	if err != nil {
		t.Fatal(err)
	}
	if sd.String() != want.String() {
		t.Fatalf("DACL %s, want %s", sd.String(), want.String())
	}
	if _, err := CreatePrivateDirAt(parent, ".private"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("a second create = %v, want fs.ErrExist", err)
	}
	// Everything created below inherits the private DACL.
	root, err := os.OpenRoot(filepath.Join(parentPath, ".private"))
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if err := root.Mkdir("generation", 0o700); err != nil {
		t.Fatal(err)
	}
	file, err := root.OpenFile("generation/working.har", os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := CheckPrivate(file, true); err != nil {
		t.Fatalf("an inherited file is not private: %v", err)
	}
	generation, err := root.Open("generation")
	if err != nil {
		t.Fatal(err)
	}
	defer generation.Close()
	if err := CheckPrivate(generation, false); err != nil {
		t.Fatalf("an inherited directory is not private: %v", err)
	}
	for _, name := range []string{"", ".", "..", `a\b`, "a/b", "a:stream", "nul\x00"} {
		if _, err := CreatePrivateDirAt(parent, name); err == nil || errors.Is(err, fs.ErrExist) {
			t.Fatalf("CreatePrivateDirAt(%q) = %v", name, err)
		}
	}
}

// A real directory whose DACL also grants Users (as a profile directory's
// inherited ACL grants Administrators) is not private.
func TestCheckPrivateRefusesAWiderDACL(t *testing.T) {
	user, err := CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	sd, err := windows.SecurityDescriptorFromString("D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;" + user + ")(A;OICI;FR;;;BU)")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "wider")
	name, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.CreateDirectory(name, &windows.SecurityAttributes{Length: uint32(unsafe.Sizeof(windows.SecurityAttributes{})), SecurityDescriptor: sd}); err != nil {
		t.Fatal(err)
	}
	if err := CheckPrivate(openDir(t, path), false); !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("a directory granting Users = %v", err)
	}
}

func TestCheckDescriptorMatrix(t *testing.T) {
	user, owner, err := currentToken()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, sddl string
		ok         bool
	}{
		{"private", "O:" + user + PrivateDirectorySDDL(user), true},
		{"token default owner", "O:" + owner + PrivateDirectorySDDL(user), true},
		{"inherited entries", "O:" + user + "D:(A;OICIID;FA;;;SY)(A;OICIID;FA;;;" + user + ")", true},
		{"user only", "O:" + user + "D:P(A;;FA;;;" + user + ")", true},
		{"foreign owner", "O:SYD:P(A;;FA;;;SY)(A;;FA;;;" + user + ")", false},
		{"Users entry", "O:" + user + "D:P(A;;FA;;;SY)(A;;FA;;;" + user + ")(A;;FR;;;BU)", false},
		{"Everyone entry", "O:" + user + "D:P(A;;FA;;;SY)(A;;FA;;;" + user + ")(A;;FR;;;WD)", false},
		{"Administrators entry", "O:" + user + "D:P(A;;FA;;;SY)(A;;FA;;;" + user + ")(A;;FA;;;BA)", false},
		{"deny entry", "O:" + user + "D:P(D;;FA;;;WD)(A;;FA;;;" + user + ")", false},
		{"NULL DACL", "O:" + user + "D:NO_ACCESS_CONTROL", false},
		{"no DACL", "O:" + user, false},
		{"empty DACL", "O:" + user + "D:P", false},
		{"user entry inherit-only", "O:" + user + "D:P(A;OICIIO;FA;;;" + user + ")(A;;FA;;;SY)", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			sd, err := windows.SecurityDescriptorFromString(test.sddl)
			if err != nil {
				t.Fatal(err)
			}
			err = checkDescriptor(sd, user, owner)
			if test.ok != (err == nil) || err != nil && !errors.Is(err, ErrNotPrivate) {
				t.Fatalf("checkDescriptor(%s) = %v", test.sddl, err)
			}
		})
	}
}

func TestCheckPrivateRefusesJunctionAndHardLink(t *testing.T) {
	parentPath := t.TempDir()
	parent := openDir(t, parentPath)
	private, err := CreatePrivateDirAt(parent, ".private")
	if err != nil {
		t.Fatal(err)
	}
	defer private.Close()
	work := filepath.Join(parentPath, ".private")
	if err := os.Mkdir(filepath.Join(work, "target"), 0o700); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("cmd", "/c", "mklink", "/J", filepath.Join(work, "junction"), filepath.Join(work, "target")).CombinedOutput(); err != nil {
		t.Fatalf("mklink /J: %v %s", err, out)
	}
	junction, err := OpenAt(private, "junction", windows.READ_CONTROL, false)
	if err != nil {
		t.Fatal(err)
	}
	defer junction.Close()
	if err := CheckPrivate(junction, false); !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("a junction = %v", err)
	}
	if err := os.WriteFile(filepath.Join(work, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(filepath.Join(work, "file"), filepath.Join(work, "second-link")); err != nil {
		t.Fatal(err)
	}
	linked, err := os.Open(filepath.Join(work, "file"))
	if err != nil {
		t.Fatal(err)
	}
	defer linked.Close()
	if err := CheckPrivate(linked, true); !errors.Is(err, ErrNotPrivate) {
		t.Fatalf("a hard-linked file = %v", err)
	}
}

func TestFileIDAndLstatAreHandleIdentities(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "a")
	if err := os.WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	before, err := FileID(f)
	_ = f.Close()
	if err != nil || before.IsZero() {
		t.Fatalf("FileID = %+v %v", before, err)
	}
	original, err := Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path, filepath.Join(dir, "b")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	moved, err := os.Open(filepath.Join(dir, "b"))
	if err != nil {
		t.Fatal(err)
	}
	after, err := FileID(moved)
	_ = moved.Close()
	if err != nil || after != before {
		t.Fatalf("the ID changed across a rename: %+v -> %+v (%v)", before, after, err)
	}
	replacement, err := Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	movedInfo, err := Lstat(filepath.Join(dir, "b"))
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(original, replacement) || !os.SameFile(original, movedInfo) {
		t.Fatal("Lstat's identity follows the path instead of the object it named")
	}
	if _, err := Lstat(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("Lstat of a missing path = %v", err)
	}
}

func TestOpenAt(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "child"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	parent := openDir(t, dir)
	child, err := OpenAt(parent, "child", windows.FILE_LIST_DIRECTORY, true)
	if err != nil {
		t.Fatal(err)
	}
	_ = child.Close()
	if _, err := OpenAt(parent, "file", 0, true); err == nil {
		t.Fatal("a file opened as a directory")
	}
	if _, err := OpenAt(parent, "missing", 0, false); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("OpenAt of a missing name = %v", err)
	}
	if _, err := OpenAt(parent, `child\..\file`, 0, false); err == nil {
		t.Fatal("a multi-component name was opened")
	}
}

func TestRenameAtNeverReplaces(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"source", "taken"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "generation"), 0o700); err != nil {
		t.Fatal(err)
	}
	parent := openDir(t, dir)
	if err := RenameAt(parent, "source", "taken"); !errors.Is(err, fs.ErrExist) {
		t.Fatalf("rename onto an existing name = %v", err)
	}
	if data, _ := os.ReadFile(filepath.Join(dir, "taken")); string(data) != "taken" {
		t.Fatal("a create-only rename replaced its target")
	}
	// A reader that does not share delete (os.Open) blocks the rename with a
	// transient sharing violation; a share-all reader (a scan) does not.
	blocking, err := os.Open(filepath.Join(dir, "source"))
	if err != nil {
		t.Fatal(err)
	}
	if err := RenameAt(parent, "source", "renamed"); !Transient(err) {
		t.Fatalf("rename with an os.Open reader = %v, want a transient sharing violation", err)
	}
	_ = blocking.Close()
	reader := openShareAll(t, filepath.Join(dir, "source"))
	defer reader.Close()
	if err := RenameAt(parent, "source", "renamed"); err != nil {
		t.Fatalf("rename with a share-all reader open = %v", err)
	}
	if err := RenameAt(parent, "generation", ".retired-generation"); err != nil {
		t.Fatalf("directory rename = %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, ".retired-generation")); err != nil {
		t.Fatal(err)
	}
	if err := RenameAt(parent, "missing", "x"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("rename of a missing name = %v", err)
	}
	if err := RenameAt(parent, "renamed", `sub\x`); err == nil {
		t.Fatal("a multi-component target was accepted")
	}
}

func TestRemoveIfSame(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	f := openShareAll(t, path)
	id, err := FileID(f)
	if err != nil {
		t.Fatal(err)
	}
	parent := openDir(t, dir)
	// A replacement under the same name is not removed.
	if err := os.Rename(path, filepath.Join(dir, "moved")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := RemoveIfSame(parent, "file", id); !errors.Is(err, ErrIdentityChanged) {
		t.Fatalf("removing a replacement = %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("the replacement was removed")
	}
	// The expected object is removed at once, with a reader still open.
	if err := RemoveIfSame(parent, "moved", id); err != nil {
		t.Fatalf("removing the expected object = %v", err)
	}
	if _, err := os.Lstat(filepath.Join(dir, "moved")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("the removed name is still visible: %v", err)
	}
	_ = f.Close()
	if err := RemoveIfSame(parent, "moved", id); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("removing a missing name = %v", err)
	}
	if err := os.Mkdir(filepath.Join(dir, "empty"), 0o700); err != nil {
		t.Fatal(err)
	}
	d, err := os.Open(filepath.Join(dir, "empty"))
	if err != nil {
		t.Fatal(err)
	}
	dirID, err := FileID(d)
	_ = d.Close()
	if err != nil {
		t.Fatal(err)
	}
	if err := RemoveIfSame(parent, "empty", dirID); err != nil {
		t.Fatalf("removing an empty directory = %v", err)
	}
}

func TestRetry(t *testing.T) {
	attempts := 0
	err := Retry(context.Background(), func() error {
		attempts++
		if attempts < 3 {
			return &os.PathError{Op: "rename", Path: "x", Err: windows.ERROR_SHARING_VIOLATION}
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("Retry = %v after %d attempts", err, attempts)
	}
	attempts = 0
	permanent := errors.New("permanent")
	if err := Retry(context.Background(), func() error { attempts++; return permanent }); !errors.Is(err, permanent) || attempts != 1 {
		t.Fatalf("a permanent error was retried: %v after %d", err, attempts)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
	defer cancel()
	err = Retry(ctx, func() error { return windows.ERROR_LOCK_VIOLATION })
	if !errors.Is(err, windows.ERROR_LOCK_VIOLATION) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Retry past its deadline = %v", err)
	}
	// Errors named in also are retried like transient ones; others are not.
	attempts = 0
	err = Retry(context.Background(), func() error {
		attempts++
		if attempts < 2 {
			return &os.PathError{Op: "remove", Path: "x", Err: windows.ERROR_DIR_NOT_EMPTY}
		}
		return nil
	}, windows.ERROR_DIR_NOT_EMPTY)
	if err != nil || attempts != 2 {
		t.Fatalf("Retry with also = %v after %d attempts", err, attempts)
	}
	attempts = 0
	if err := Retry(context.Background(), func() error { attempts++; return windows.ERROR_DIR_NOT_EMPTY }, windows.ERROR_ACCESS_DENIED); !errors.Is(err, windows.ERROR_DIR_NOT_EMPTY) || attempts != 1 {
		t.Fatalf("an error outside also was retried: %v after %d", err, attempts)
	}
	for _, transient := range []error{windows.ERROR_SHARING_VIOLATION, windows.ERROR_LOCK_VIOLATION, windows.ERROR_DELETE_PENDING, windows.STATUS_DELETE_PENDING} {
		if !Transient(transient) {
			t.Errorf("%v is not transient", transient)
		}
	}
	if Transient(windows.ERROR_FILE_NOT_FOUND) || Transient(nil) {
		t.Error("a permanent error is transient")
	}
}

func TestSyncDirAndOwner(t *testing.T) {
	dir := t.TempDir()
	parent := openDir(t, dir)
	if err := SyncDir(parent); err != nil {
		t.Fatalf("SyncDir of a directory handle = %v", err)
	}
	private, err := CreatePrivateDirAt(parent, "private")
	if err != nil {
		t.Fatal(err)
	}
	defer private.Close()
	if err := SyncDir(private); err != nil {
		t.Fatalf("SyncDir of a private directory = %v", err)
	}
	owner, err := Owner(private)
	if err != nil || !OwnedByCurrentToken(owner) {
		t.Fatalf("Owner = %q %v", owner, err)
	}
	if OwnedByCurrentToken("S-1-5-18") && owner != "S-1-5-18" {
		t.Fatal("SYSTEM counts as this token's owner")
	}
	if OwnedByCurrentToken("") {
		t.Fatal("an empty SID counts as this token's owner")
	}
}

// A name another handle left delete-pending (legacy delete semantics)
// counts as removed.
func TestRemoveAtCountsDeletePendingAsRemoved(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "pending")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	pathPtr, err := windows.UTF16PtrFromString(path)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := windows.CreateFile(pathPtr, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer windows.CloseHandle(holder)
	deleter, err := windows.CreateFile(pathPtr, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	deleteFile := byte(1)
	if err := windows.SetFileInformationByHandle(deleter, windows.FileDispositionInfo, &deleteFile, 1); err != nil {
		t.Fatal(err)
	}
	_ = windows.CloseHandle(deleter)
	parent := openDir(t, dir)
	if err := RemoveAt(parent, "pending"); err != nil {
		t.Fatalf("removing a delete-pending name = %v", err)
	}
}
