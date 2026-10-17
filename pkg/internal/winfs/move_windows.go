//go:build windows

package winfs

import (
	"errors"
	"fmt"
	"io/fs"

	"golang.org/x/sys/windows"
)

// MoveFileWriteThrough renames oldPath to newPath with MOVEFILE_WRITE_THROUGH,
// so the call returns only once the move is on disk. Without replace an
// existing newPath fails with fs.ErrExist; a missing source fails with
// fs.ErrNotExist.
func MoveFileWriteThrough(oldPath, newPath string, replace bool) error {
	oldPtr, err := windows.UTF16PtrFromString(ExtendedPath(oldPath))
	if err != nil {
		return err
	}
	newPtr, err := windows.UTF16PtrFromString(ExtendedPath(newPath))
	if err != nil {
		return err
	}
	flags := uint32(windows.MOVEFILE_WRITE_THROUGH)
	if replace {
		flags |= windows.MOVEFILE_REPLACE_EXISTING
	}
	if err := windows.MoveFileEx(oldPtr, newPtr, flags); err != nil {
		switch {
		case errors.Is(err, windows.ERROR_FILE_EXISTS), errors.Is(err, windows.ERROR_ALREADY_EXISTS):
			return fmt.Errorf("%w: %v", fs.ErrExist, err)
		case errors.Is(err, windows.ERROR_FILE_NOT_FOUND), errors.Is(err, windows.ERROR_PATH_NOT_FOUND):
			return fmt.Errorf("%w: %v", fs.ErrNotExist, err)
		default:
			return err
		}
	}
	return nil
}
