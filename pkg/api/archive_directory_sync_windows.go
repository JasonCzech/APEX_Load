//go:build windows

package api

import (
	"os"

	"github.com/jasonczech/apexload/pkg/internal/winfs"
)

// syncArchiveDirectoryHandle makes an opened directory's entries durable.
// Windows refuses File.Sync on a directory handle; FlushFileBuffers on a
// write-access reopen of the same directory flushes them instead.
func syncArchiveDirectoryHandle(dir *os.File) error { return winfs.SyncDir(dir) }
