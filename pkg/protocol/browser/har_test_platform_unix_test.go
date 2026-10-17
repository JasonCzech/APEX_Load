//go:build !windows

package browser

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"testing"
)

// harTestSleeper is a process that lives for seconds.
func harTestSleeper(seconds int) *exec.Cmd { return exec.Command("sleep", strconv.Itoa(seconds)) }

// harTestExiter is a process that exits at once.
func harTestExiter() *exec.Cmd { return exec.Command("true") }

// makeHARPrivateDir creates path private to this user.
func makeHARPrivateDir(t *testing.T, path string) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
}

// makeHARUnsafe grants group and other users access to path.
func makeHARUnsafe(t *testing.T, path string) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	mode := os.FileMode(0o644)
	if info.IsDir() {
		mode = 0o755
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

// makeHARReadOnlyDir leaves this user unable to create entries in path
// until restored; it skips where permissions do not bind (root).
func makeHARReadOnlyDir(t *testing.T, path string) {
	t.Helper()
	if os.Geteuid() == 0 {
		t.Skip("permissions do not bind root")
	}
	if err := os.Chmod(path, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
}

// harDirAliasKinds are the directory aliases this platform can make.
var harDirAliasKinds = []string{"symlink"}

// makeHARDirAlias makes alias name target.
func makeHARDirAlias(t *testing.T, kind, target, alias string) {
	t.Helper()
	if err := os.Symlink(target, alias); err != nil {
		t.Fatal(kind, err)
	}
}

// harPrivatePath reports why path is not private (0700 for a directory,
// 0600 for a file), or nil.
func harPrivatePath(path string, dir bool) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	want := os.FileMode(0o600)
	if dir {
		want = 0o700
	}
	if info.Mode().Perm() != want || info.IsDir() != dir {
		return fmt.Errorf("%s has mode %v", path, info.Mode())
	}
	return nil
}
