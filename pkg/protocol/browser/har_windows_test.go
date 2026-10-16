//go:build windows

package browser

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unsafe"

	"github.com/jasonczech/apexload/pkg/internal/winfs"
	"golang.org/x/sys/windows"
)

func harTestDescriptor(t *testing.T, path string, info windows.SECURITY_INFORMATION) *windows.SECURITY_DESCRIPTOR {
	t.Helper()
	sd, err := windows.GetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, info)
	if err != nil {
		t.Fatal(err)
	}
	return sd
}

// The working directory is created with a DACL granting only this user and
// SYSTEM; its generation and working file inherit it.
func TestWindowsHARWorkingDirectoryDACLIsPrivate(t *testing.T) {
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	work := filepath.Join(dir, runtimeHARWorkingDirectory)
	user := harTestUser(t)
	if got, want := harTestDescriptor(t, work, windows.DACL_SECURITY_INFORMATION).String(), winfs.PrivateDirectorySDDL(user); got != want {
		t.Fatalf("%s DACL %s, want %s", runtimeHARWorkingDirectory, got, want)
	}
	for _, path := range []string{work, filepath.Dir(o.path)} {
		if err := harPrivatePath(path, true); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"working.har", "owner.lock", "manifest.json"} {
		if err := harPrivatePath(filepath.Join(filepath.Dir(o.path), name), false); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := finishFixtureHAR(t, o, false); err != nil {
		t.Fatal(err)
	}
}

// enableHARTestPrivilege enables a privilege in this process token for the
// test, or skips when the token does not hold it.
func enableHARTestPrivilege(t *testing.T, name string) {
	t.Helper()
	var token windows.Token
	if err := windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token); err != nil {
		t.Fatal(err)
	}
	defer token.Close()
	var luid windows.LUID
	namePtr, _ := windows.UTF16PtrFromString(name)
	if err := windows.LookupPrivilegeValue(nil, namePtr, &luid); err != nil {
		t.Fatal(err)
	}
	set := func(attributes uint32) error {
		privileges := windows.Tokenprivileges{PrivilegeCount: 1}
		privileges.Privileges[0] = windows.LUIDAndAttributes{Luid: luid, Attributes: attributes}
		return windows.AdjustTokenPrivileges(token, false, &privileges, uint32(unsafe.Sizeof(privileges)), nil, nil)
	}
	if err := set(windows.SE_PRIVILEGE_ENABLED); err != nil || errors.Is(windows.GetLastError(), windows.ERROR_NOT_ALL_ASSIGNED) {
		t.Skipf("%s is not held by this token: %v", name, err)
	}
	t.Cleanup(func() {
		var token windows.Token
		if windows.OpenProcessToken(windows.CurrentProcess(), windows.TOKEN_ADJUST_PRIVILEGES|windows.TOKEN_QUERY, &token) == nil {
			privileges := windows.Tokenprivileges{PrivilegeCount: 1}
			privileges.Privileges[0] = windows.LUIDAndAttributes{Luid: luid}
			_ = windows.AdjustTokenPrivileges(token, false, &privileges, uint32(unsafe.Sizeof(privileges)), nil, nil)
			_ = token.Close()
		}
	})
}

// Every unsafe working directory is refused before anything is created in
// it: an ACL inherited from a parent granting Users, an Everyone entry, a
// NULL DACL and a foreign owner.
func TestWindowsHARRefusesUnsafeWorkingDirectoryACLs(t *testing.T) {
	user := harTestUser(t)
	for _, kind := range []string{"inherited_users", "everyone", "null_dacl", "foreign_owner"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			work := filepath.Join(dir, runtimeHARWorkingDirectory)
			switch kind {
			case "inherited_users":
				setHARTestDACL(t, dir, "D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;"+user+")(A;OICI;FR;;;BU)")
				if err := os.Mkdir(work, 0o700); err != nil {
					t.Fatal(err)
				}
			case "everyone":
				makeHARPrivateDir(t, work)
				makeHARUnsafe(t, work)
			case "null_dacl":
				makeHARPrivateDir(t, work)
				if err := windows.SetNamedSecurityInfo(work, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, nil, nil); err != nil {
					t.Fatal(err)
				}
			case "foreign_owner":
				makeHARPrivateDir(t, work)
				enableHARTestPrivilege(t, "SeRestorePrivilege")
				system, err := windows.CreateWellKnownSid(windows.WinLocalSystemSid)
				if err != nil {
					t.Fatal(err)
				}
				if err := windows.SetNamedSecurityInfo(work, windows.SE_FILE_OBJECT, windows.OWNER_SECURITY_INFORMATION, system, nil, nil, nil); err != nil {
					t.Skipf("cannot give the directory a foreign owner: %v", err)
				}
			}
			if o, err := newHARCaptureOwner(context.Background(), dir); err == nil {
				_, _ = finishFixtureHAR(t, o, false)
				t.Fatalf("%s working directory admitted", kind)
			}
			entries, err := os.ReadDir(work)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("a refused working directory gained %v", entries)
			}
		})
	}
}

// An elevated token's default owner (Administrators) owns what this process
// creates, and that owner is accepted.
func TestWindowsHARAcceptsElevatedTokenOwner(t *testing.T) {
	if !windows.GetCurrentProcessToken().IsElevated() {
		t.Skip("the token is not elevated")
	}
	dir := t.TempDir()
	o := fixtureHAROwner(t, dir)
	owner, _, err := harTestDescriptor(t, filepath.Join(dir, runtimeHARWorkingDirectory), windows.OWNER_SECURITY_INFORMATION).Owner()
	if err != nil || !winfs.OwnedByCurrentToken(owner.String()) {
		t.Fatalf("owner %v (%v)", owner, err)
	}
	t.Logf("working directory owner %s, token user %s", owner, harTestUser(t))
	if _, err := finishFixtureHAR(t, o, false); err != nil {
		t.Fatal(err)
	}
}

// A UNC HAR directory is refused (SMB and network paths are never owned).
func TestWindowsHARRefusesUNCDirectory(t *testing.T) {
	for _, path := range []string{`\\localhost\C$\Windows\Temp`, `\\?\UNC\localhost\C$\Windows\Temp`} {
		if err := validateHARVolume(path); !errors.Is(err, winfs.ErrUnsupportedVolume) {
			t.Fatalf("validateHARVolume(%s) = %v", path, err)
		}
		if o, err := newHARCaptureOwner(context.Background(), path); err == nil {
			_, _ = finishFixtureHAR(t, o, false)
			t.Fatalf("a HAR directory at %s was admitted", path)
		}
	}
}

// A name another handle left delete-pending counts as removed.
func TestWindowsHARRemoveCountsDeletePendingAsRemoved(t *testing.T) {
	dir := t.TempDir()
	makeHARPrivateDir(t, filepath.Join(dir, "private"))
	root, err := os.OpenRoot(filepath.Join(dir, "private"))
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "private", "pending")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	pathPtr, _ := windows.UTF16PtrFromString(path)
	holder, err := windows.CreateFile(pathPtr, windows.GENERIC_READ, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	deleter, err := windows.CreateFile(pathPtr, windows.DELETE, windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE, nil, windows.OPEN_EXISTING, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	deleteFile := byte(1)
	if err := windows.SetFileInformationByHandle(deleter, windows.FileDispositionInfo, &deleteFile, 1); err != nil {
		t.Fatal(err)
	}
	_ = windows.CloseHandle(deleter)
	if err := removeHAR(root, "pending"); err != nil {
		t.Fatalf("removing a delete-pending name = %v", err)
	}
	if err := root.Close(); err != nil {
		t.Fatal(err)
	}
	// The directory empties once the last handle closes, and its removal
	// waits for that.
	go func() { _ = windows.CloseHandle(holder) }()
	parent, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	if err := removeHAR(parent, "private"); err != nil {
		t.Fatalf("removing a directory whose entry was delete-pending = %v", err)
	}
}

// 200 owners in a row create a generation, publish a scrubbed HAR and
// retire both, leaving only the namespace lease behind.
func TestWindowsHARCreatePublishRetireStress(t *testing.T) {
	if testing.Short() {
		t.Skip("stress loop")
	}
	dir := t.TempDir()
	for cycle := range 200 {
		o, err := newHARCaptureOwner(context.Background(), dir)
		if err != nil {
			t.Fatalf("cycle %d: owner: %v", cycle, err)
		}
		writeOwnedHARFixture(t, o)
		artifact, err := finishFixtureHAR(t, o, true)
		if err != nil {
			_ = o.closeDescriptors()
			t.Fatalf("cycle %d: finish: %v", cycle, err)
		}
		if err := TakeRuntimeHARArtifact(context.Background(), artifact, func(r io.Reader, _ int64) error {
			_, err := io.Copy(io.Discard, r)
			return err
		}); err != nil {
			t.Fatalf("cycle %d: take: %v", cycle, err)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.Name() != runtimeHARWorkingDirectory {
			t.Fatalf("the HAR directory kept %s", entry.Name())
		}
	}
	work, err := os.ReadDir(filepath.Join(dir, runtimeHARWorkingDirectory))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range work {
		if entry.Name() != "namespace.lock" && entry.Name() != harRetirementLedgerName {
			names := []string{}
			for _, e := range work {
				names = append(names, e.Name())
			}
			t.Fatalf("staging kept %s", strings.Join(names, ","))
		}
	}
}
