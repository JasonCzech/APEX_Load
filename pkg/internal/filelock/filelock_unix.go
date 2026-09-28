//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

// Package filelock provides cross-platform exclusive advisory file locking.
//
// It exists so durable-store implementations do not each grow their own
// flock/LockFileEx wrapper. Nothing here is specific to any import format.
package filelock

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

// Acquire takes an exclusive advisory lock on path, creating it with mode 0600
// if absent, and returns the release function. It blocks until the lock is
// available.
func Acquire(path string) (func() error, error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, err
	}
	return func() error {
		return errors.Join(
			unix.Flock(int(file.Fd()), unix.LOCK_UN),
			file.Close(),
		)
	}, nil
}

// tryLockFile never blocks and leaves descriptor ownership with the caller.
func tryLockFile(file *os.File) (func() error, bool, error) {
	err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) || errors.Is(err, unix.EINTR) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return func() error { return unix.Flock(int(file.Fd()), unix.LOCK_UN) }, true, nil
}
