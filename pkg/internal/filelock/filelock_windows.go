//go:build windows

package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// Acquire takes an exclusive advisory lock on path, creating it with mode 0600
// if absent, and returns the release function. It blocks until the lock is
// available.
func Acquire(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var overlapped windows.Overlapped
	handle := windows.Handle(file.Fd())
	if err := windows.LockFileEx(
		handle,
		windows.LOCKFILE_EXCLUSIVE_LOCK,
		0,
		1,
		0,
		&overlapped,
	); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() error {
		return errors.Join(
			windows.UnlockFileEx(handle, 0, 1, 0, &overlapped),
			file.Close(),
		)
	}, nil
}
