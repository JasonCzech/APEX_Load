//go:build linux || darwin

package duckdb

import (
	"os"
	"syscall"
	"time"
)

// os.StartProcess calls Fd on inherited endpoints and therefore clears the
// parent's os.Pipe O_NONBLOCK flag. NewFile alone would admit a blocking read
// which Close cannot evict. Restore kernel nonblocking mode BEFORE NewFile, then
// require actual Go poller registration before any command read/native work.
// This wrapper is used only by the child owning that inherited descriptor.
func workerPollableInheritedCommandFile(descriptor uintptr) (*os.File, error) {
	if _, err := workerInspectRawInheritedDescriptor(descriptor, workerRawReadPipe, false); err != nil {
		return nil, errWorkerProtocol
	}
	if syscall.SetNonblock(int(descriptor), true) != nil {
		if syscall.Close(int(descriptor)) != nil {
			return nil, errWorkerOwnership
		}
		return nil, errWorkerProtocol
	}
	file := os.NewFile(descriptor, "native-commands")
	if file == nil {
		if syscall.Close(int(descriptor)) != nil {
			return nil, errWorkerOwnership
		}
		return nil, errWorkerProtocol
	}
	info, statErr := file.Stat()
	if statErr != nil || info.Mode()&os.ModeNamedPipe == 0 || file.SetReadDeadline(time.Time{}) != nil {
		if file.Close() != nil {
			return nil, errWorkerOwnership
		}
		return nil, errWorkerProtocol
	}
	// No Fd call or second wrapper may change this open-file description back to
	// blocking after admission. Close must join the sole read before result send.
	return file, nil
}
