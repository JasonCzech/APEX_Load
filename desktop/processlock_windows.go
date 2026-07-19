//go:build windows

package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"

	"golang.org/x/sys/windows"
)

type desktopFileLock struct {
	file       *os.File
	overlapped windows.Overlapped
}

func acquireDesktopProcessLock(appDataPath string) (desktopProcessLock, error) {
	path := filepath.Join(appDataPath, desktopProcessLockFile)
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return nil, err
	}
	handle, err := windows.CreateFile(
		pathUTF16,
		windows.GENERIC_READ|windows.GENERIC_WRITE,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_ALWAYS,
		windows.FILE_ATTRIBUTE_HIDDEN,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open desktop process lock: %w", err)
	}
	file := os.NewFile(uintptr(handle), path)
	if file == nil {
		_ = windows.CloseHandle(handle)
		return nil, fmt.Errorf("open desktop process lock: invalid handle")
	}
	lock := &desktopFileLock{file: file}
	if err := windows.LockFileEx(handle, windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &lock.overlapped); err != nil {
		_ = file.Close()
		if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
			return nil, errDesktopProcessLockHeld
		}
		return nil, fmt.Errorf("lock desktop process state: %w", err)
	}
	if err := writeDesktopLockOwner(file); err != nil {
		_ = windows.UnlockFileEx(handle, 0, 1, 0, &lock.overlapped)
		_ = file.Close()
		return nil, err
	}
	return lock, nil
}

func writeDesktopLockOwner(file *os.File) error {
	if err := file.Truncate(0); err != nil {
		return fmt.Errorf("truncate desktop process lock: %w", err)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return fmt.Errorf("seek desktop process lock: %w", err)
	}
	if _, err := file.WriteString(strconv.Itoa(os.Getpid()) + "\r\n"); err != nil {
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
	return errors.Join(
		windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, &lock.overlapped),
		file.Close(),
	)
}
