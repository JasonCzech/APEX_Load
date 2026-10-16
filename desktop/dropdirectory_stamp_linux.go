//go:build linux

package main

import (
	"os"
	"time"

	"golang.org/x/sys/unix"
)

// directoryStamp tells a dropped folder from a replacement that reuses its
// inode number, which Linux hands out again as soon as a directory is deleted.
type directoryStamp struct {
	birth    time.Time
	hasBirth bool
	modified time.Time
}

func directoryStampOf(path string, info os.FileInfo) directoryStamp {
	stamp := directoryStamp{modified: info.ModTime()}
	var status unix.Statx_t
	if unix.Statx(unix.AT_FDCWD, path, unix.AT_SYMLINK_NOFOLLOW, unix.STATX_BTIME, &status) == nil && status.Mask&unix.STATX_BTIME != 0 {
		stamp.birth = time.Unix(status.Btime.Sec, int64(status.Btime.Nsec))
		stamp.hasBirth = true
	}
	return stamp
}

// sameDirectoryStamp compares creation times; where the filesystem reports
// none, the modification time stands in, so a folder whose entries changed
// after the drop must be dropped again.
func sameDirectoryStamp(granted, current directoryStamp) bool {
	if granted.hasBirth && current.hasBirth {
		return granted.birth.Equal(current.birth)
	}
	return granted.modified.Equal(current.modified)
}
