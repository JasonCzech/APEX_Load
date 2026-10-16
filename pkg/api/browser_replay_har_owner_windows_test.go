//go:build windows

package api

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/winfs"
	"golang.org/x/sys/windows"
)

func checkPrivateReplayDir(t *testing.T, path string) error {
	t.Helper()
	dir, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	return winfs.CheckPrivate(dir, false)
}

// The managed replay HAR root is created private (user and SYSTEM only)
// and adopted again; a directory with the temporary directory's inherited
// ACL is not adopted, and the fallback directory is private too.
func TestReplayHARRootIsPrivateOnWindows(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, replayHARRootName)
	if err := ensurePrivateReplayHARRoot(root); err != nil {
		t.Fatalf("a new managed root: %v", err)
	}
	if err := checkPrivateReplayDir(t, root); err != nil {
		t.Fatalf("the managed root is not private: %v", err)
	}
	if err := ensurePrivateReplayHARRoot(root); err != nil {
		t.Fatalf("an existing private root was not adopted: %v", err)
	}
	manager, err := os.MkdirTemp(root, "manager-")
	if err != nil {
		t.Fatal(err)
	}
	if err := checkPrivateReplayDir(t, manager); err != nil {
		t.Fatalf("a manager directory did not inherit the private DACL: %v", err)
	}
	fallback, err := privateReplayHARTempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(fallback)
	if err := checkPrivateReplayDir(t, fallback); err != nil {
		t.Fatalf("the fallback directory is not private: %v", err)
	}
	inherited := filepath.Join(parent, "inherited")
	if err := os.Mkdir(inherited, 0o700); err != nil {
		t.Fatal(err)
	}
	if checkPrivateReplayDir(t, inherited) == nil {
		t.Skip("this host's temporary directory ACL is already private; nothing to refuse")
	}
	if err := ensurePrivateReplayHARRoot(inherited); err == nil {
		t.Fatal("a directory with an inherited, wider ACL was adopted")
	}
}

// makeReplayHARRootPrivate creates root as the managed root must be: with
// the private DACL.
func makeReplayHARRootPrivate(t *testing.T, root string) {
	t.Helper()
	if err := ensurePrivateReplayHARRoot(root); err != nil {
		t.Fatal(err)
	}
}

func setReplayHARRootDACL(t *testing.T, root, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(root, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

// makeReplayHARRootShared adds an Everyone entry to root's DACL.
func makeReplayHARRootShared(t *testing.T, root string) {
	t.Helper()
	user, err := winfs.CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	setReplayHARRootDACL(t, root, winfs.PrivateDirectorySDDL(user)+"(A;OICI;FR;;;WD)")
}

// makeReplayHARRootPrivateAgain restores the private DACL.
func makeReplayHARRootPrivateAgain(t *testing.T, root string) {
	t.Helper()
	user, err := winfs.CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	setReplayHARRootDACL(t, root, winfs.PrivateDirectorySDDL(user))
}
