//go:build windows

package duckdb

import (
	"errors"
	"os"
	"time"

	"golang.org/x/sys/windows"
)

// workerCloseCommandChannel closes the child's inherited command pipe and joins
// its sole reader. The pipe is synchronous, so Close cancels a pending read once
// (CancelIoEx) and then waits for the reader. A reader that has taken its lock
// but not yet entered ReadFile misses that cancel and blocks, and Close with it,
// until the parent writes or closes its end, while the parent waits for this
// child's result. So, until Close returns, the cancel repeats. It runs only once
// Close has begun, so the reader still reports os.ErrClosed, and goes through a
// duplicate handle, which stays valid after Close destroys the original.
func workerCloseCommandChannel(commands *os.File) error {
	raw, err := commands.SyscallConn()
	if err != nil {
		return commands.Close()
	}
	current := windows.CurrentProcess()
	var duplicate windows.Handle
	duplicateErr := errWorkerProtocol
	if raw.Control(func(fd uintptr) {
		duplicateErr = windows.DuplicateHandle(current, windows.Handle(fd), current, &duplicate, 0, false, windows.DUPLICATE_SAME_ACCESS)
	}) != nil || duplicateErr != nil {
		return commands.Close()
	}
	stop, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				// Control fails once Close has marked the descriptor closing.
				if raw.Control(func(uintptr) {}) != nil {
					_ = windows.CancelIoEx(duplicate, nil)
				}
			}
		}
	}()
	err = commands.Close()
	close(stop)
	<-stopped
	return errors.Join(err, windows.CloseHandle(duplicate))
}
