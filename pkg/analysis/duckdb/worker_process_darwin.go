//go:build darwin

package duckdb

import (
	"golang.org/x/sys/unix"
	"os"
	"os/exec"
	"syscall"
	"unsafe"
)

func workerConfigureUnix(cmd *exec.Cmd) {}
func workerDirectoryFDPath(root *os.File) (string, error) {
	// /dev/fd is not a pathname symlink on Darwin; use the owned descriptor's
	// kernel F_GETPATH value so DuckDB's locked directory policy gets the actual
	// private path, then the shared helper verifies it still names this inode.
	var path [1024]byte
	_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, root.Fd(), uintptr(unix.F_GETPATH), uintptr(unsafe.Pointer(&path[0])))
	if errno != 0 {
		return "", errWorkerProtocol
	}
	for i, value := range path {
		if value == 0 {
			if i == 0 {
				return "", errWorkerProtocol
			}
			return string(path[:i]), nil
		}
	}
	return "", errWorkerProtocol
}

func workerPrepareUnixContainment(a *workerAttempt, profile workerProcessProfile) (workerUnixContainment, error) {
	if profile.requireHard {
		return nil, ErrUnavailable
	}
	return nil, nil
}
