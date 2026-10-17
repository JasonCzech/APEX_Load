//go:build !windows

package api

import "os"

// syncArchiveDirectoryHandle makes an opened directory's entries durable.
func syncArchiveDirectoryHandle(dir *os.File) error { return dir.Sync() }
