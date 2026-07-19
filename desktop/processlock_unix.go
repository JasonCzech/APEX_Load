//go:build darwin || linux || freebsd || openbsd || netbsd || dragonfly

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/unix"
)

type desktopFileLock struct {
	file *os.File
}

func acquireDesktopProcessLock(appDataPath string) (desktopProcessLock, error) {
	path := filepath.Join(appDataPath, desktopProcessLockFile)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, privateFileMode)
	if err != nil {
		return nil, fmt.Errorf("open desktop process lock: %w", err)
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("open desktop process lock: invalid descriptor")
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return nil, errDesktopProcessLockHeld
		}
		return nil, fmt.Errorf("lock desktop process state: %w", err)
	}
	if err := writeDesktopLockOwner(file); err != nil {
		_ = unix.Flock(fd, unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	return &desktopFileLock{file: file}, nil
}

func writeDesktopLockOwner(file *os.File) error {
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate desktop process lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek desktop process lock: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\n"); err != nil {
		return fmt.Errorf("write desktop process lock: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync desktop process lock: %w", err)
	}
	return nil
}

func (lock *desktopFileLock) Close() error {
	if lock == nil || lock.file == nil {
		return nil
	}
	file := lock.file
	lock.file = nil
	return errors.Join(unix.Flock(int(file.Fd()), unix.LOCK_UN), file.Close())
}
