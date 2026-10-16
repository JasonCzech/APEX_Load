//go:build windows

package winfs

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// SyncDir flushes an opened directory's metadata (its entries) to disk:
// FlushFileBuffers on a handle to the same directory opened for write,
// since os.File.Sync on a read-only directory handle is refused.
func SyncDir(dir *os.File) error {
	if dir == nil {
		return errors.New("directory handle is required")
	}
	// An empty name relative to the directory reopens that directory itself.
	handle, err := ntCreateAt(dir, "", nil, windows.FILE_WRITE_DATA|windows.FILE_APPEND_DATA|windows.SYNCHRONIZE,
		windows.FILE_OPEN, windows.FILE_DIRECTORY_FILE|windows.FILE_OPEN_REPARSE_POINT)
	if err != nil {
		return &os.PathError{Op: "syncdir", Path: dir.Name(), Err: ntErrno(err)}
	}
	defer windows.CloseHandle(handle)
	if err := windows.FlushFileBuffers(handle); err != nil {
		return &os.PathError{Op: "syncdir", Path: dir.Name(), Err: err}
	}
	return nil
}
