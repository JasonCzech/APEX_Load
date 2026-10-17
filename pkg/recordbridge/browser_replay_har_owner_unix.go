//go:build darwin || linux

package recordbridge

import (
	"errors"
	"os"
	"syscall"
)

// replayHAROwnedByProcess reports whether a directory belongs to this
// process's effective user (a root process must not adopt another user's).
func replayHAROwnedByProcess(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat != nil && stat.Uid == uint32(os.Geteuid())
}

// ensurePrivateReplayHARRoot makes the managed root (0700) when missing and
// proves it a real directory, mode 0700, this process's user owns.
func ensurePrivateReplayHARRoot(root string) error {
	if err := os.MkdirAll(root, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o700 || !replayHAROwnedByProcess(info) {
		return errors.New("the managed replay HAR root is not a private directory")
	}
	return nil
}

// privateReplayHARTempDir is a fresh 0700 directory under the system
// temporary directory, for a store that cannot use the managed root.
func privateReplayHARTempDir() (string, error) { return os.MkdirTemp("", "apexload-replay-har-") }
