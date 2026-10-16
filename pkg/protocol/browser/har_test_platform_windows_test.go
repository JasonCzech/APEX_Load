//go:build windows

package browser

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/jasonczech/apexload/pkg/internal/winfs"
	"golang.org/x/sys/windows"
)

// harTestSleeper is a process that lives for seconds.
func harTestSleeper(seconds int) *exec.Cmd {
	return exec.Command("ping", "-n", strconv.Itoa(seconds+1), "127.0.0.1")
}

// harTestExiter is a process that exits at once.
func harTestExiter() *exec.Cmd { return exec.Command("cmd", "/c", "exit", "0") }

// makeHARPrivateDir creates path with the private DACL.
func makeHARPrivateDir(t *testing.T, path string) {
	t.Helper()
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	defer parent.Close()
	dir, err := winfs.CreatePrivateDirAt(parent, filepath.Base(path))
	if err != nil {
		t.Fatal(err)
	}
	_ = dir.Close()
}

func setHARTestDACL(t *testing.T, path, sddl string) {
	t.Helper()
	sd, err := windows.SecurityDescriptorFromString(sddl)
	if err != nil {
		t.Fatal(err)
	}
	dacl, _, err := sd.DACL()
	if err != nil {
		t.Fatal(err)
	}
	if err := windows.SetNamedSecurityInfo(path, windows.SE_FILE_OBJECT, windows.DACL_SECURITY_INFORMATION|windows.PROTECTED_DACL_SECURITY_INFORMATION, nil, nil, dacl, nil); err != nil {
		t.Fatal(err)
	}
}

func harTestUser(t *testing.T) string {
	t.Helper()
	user, err := winfs.CurrentUserSID()
	if err != nil {
		t.Fatal(err)
	}
	return user
}

// makeHARUnsafe adds an Everyone entry to path's DACL.
func makeHARUnsafe(t *testing.T, path string) {
	t.Helper()
	setHARTestDACL(t, path, winfs.PrivateDirectorySDDL(harTestUser(t))+"(A;OICI;FR;;;WD)")
}

// makeHARReadOnlyDir leaves this user able only to read and traverse path
// until restored (the DACL still names only the user and SYSTEM).
func makeHARReadOnlyDir(t *testing.T, path string) {
	t.Helper()
	user := harTestUser(t)
	setHARTestDACL(t, path, "D:P(A;OICI;FA;;;SY)(A;OICI;0x1200a9;;;"+user+")")
	t.Cleanup(func() { setHARTestDACL(t, path, winfs.PrivateDirectorySDDL(user)) })
}

// harDirAliasKinds are the directory aliases this platform can make.
var harDirAliasKinds = []string{"symlink", "junction"}

// makeHARDirAlias makes alias name target.
func makeHARDirAlias(t *testing.T, kind, target, alias string) {
	t.Helper()
	switch kind {
	case "junction":
		if out, err := exec.Command("cmd", "/c", "mklink", "/J", alias, target).CombinedOutput(); err != nil {
			t.Fatalf("mklink /J: %v %s", err, out)
		}
	default:
		if err := os.Symlink(target, alias); err != nil {
			t.Fatal(kind, err)
		}
	}
}

// harPrivatePath reports why path is not private to this user, or nil.
func harPrivatePath(path string, dir bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return winfs.CheckPrivate(f, !dir)
}
