//go:build darwin || linux

package recordbridge

import (
	"os"
	"syscall"
	"testing"
)

type replayHAROwnerInfo struct {
	os.FileInfo
	stat *syscall.Stat_t
}

func (i replayHAROwnerInfo) Sys() any { return i.stat }

// Only a directory this process's user owns is adopted as the managed root.
func TestReplayHAROwnedByProcess(t *testing.T) {
	own, err := os.Lstat(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !replayHAROwnedByProcess(own) {
		t.Fatal("this process's own directory was not owned")
	}
	other := replayHAROwnerInfo{FileInfo: own, stat: &syscall.Stat_t{Uid: uint32(os.Geteuid()) + 1}}
	if replayHAROwnedByProcess(other) {
		t.Fatal("another user's directory was taken for this process's")
	}
	if replayHAROwnedByProcess(replayHAROwnerInfo{FileInfo: own}) {
		t.Fatal("a directory without ownership information was adopted")
	}
}

// makeReplayHARRootPrivate creates root as the managed root must be: 0700.
func makeReplayHARRootPrivate(t *testing.T, root string) {
	t.Helper()
	if err := os.Mkdir(root, 0o700); err != nil {
		t.Fatal(err)
	}
}

// makeReplayHARRootShared grants other users access to root.
func makeReplayHARRootShared(t *testing.T, root string) {
	t.Helper()
	if err := os.Chmod(root, 0o755); err != nil {
		t.Fatal(err)
	}
}

// makeReplayHARRootPrivateAgain withdraws that access.
func makeReplayHARRootPrivateAgain(t *testing.T, root string) {
	t.Helper()
	if err := os.Chmod(root, 0o700); err != nil {
		t.Fatal(err)
	}
}
