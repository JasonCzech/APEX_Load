//go:build !darwin && !linux

package api

import "os"

// replayHAROwnedByProcess cannot prove ownership here, so the managed root is
// never adopted (native replay HARs need the browser's HAR ownership, which
// only darwin and linux provide).
func replayHAROwnedByProcess(os.FileInfo) bool { return false }
