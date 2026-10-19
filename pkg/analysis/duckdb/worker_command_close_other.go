//go:build !windows

package duckdb

import "os"

// workerCloseCommandChannel closes the child's inherited command pipe and joins
// its sole reader. On Linux and macOS the pipe is registered with the Go poller
// (workerPollableInheritedCommandFile), so Close evicts a pending read.
func workerCloseCommandChannel(commands *os.File) error { return commands.Close() }
