//go:build darwin || linux

package api

import (
	"os"
	"syscall"
)

// replayHAROwnedByProcess reports whether a directory belongs to this
// process's effective user (a root process must not adopt another user's).
func replayHAROwnedByProcess(info os.FileInfo) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat != nil && stat.Uid == uint32(os.Geteuid())
}
