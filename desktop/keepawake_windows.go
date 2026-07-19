//go:build windows

package main

import (
	"fmt"
	"log/slog"
	"runtime"
	"sync"
	"syscall"
)

// SetThreadExecutionState flags (winbase.h).
const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// startSleepInhibitor owns a locked OS thread for both calls to
// SetThreadExecutionState. The assertion is thread-scoped; calling enable and
// reset from arbitrary Go goroutines can land on different threads and leave
// the original assertion active until process exit.
func startSleepInhibitor() func() {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	started := make(chan bool, 1)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		runtime.LockOSThread()
		defer runtime.UnlockOSThread()
		if err := proc.Find(); err != nil {
			slog.Warn("Windows sleep inhibitor is unavailable", "err", err)
			started <- false
			return
		}
		if err := setThreadExecutionState(proc, esContinuous|esSystemRequired); err != nil {
			slog.Warn("failed to enable Windows sleep inhibitor", "err", err)
			started <- false
			return
		}
		started <- true
		<-stop
		if err := setThreadExecutionState(proc, esContinuous); err != nil {
			slog.Warn("failed to release Windows sleep inhibitor", "err", err)
		}
	}()
	if !<-started {
		<-done
		return func() {}
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			close(stop)
			<-done
		})
	}
}

func setThreadExecutionState(proc *syscall.LazyProc, state uintptr) error {
	result, _, callErr := proc.Call(state)
	if result != 0 {
		return nil
	}
	if errno, ok := callErr.(syscall.Errno); ok && errno == 0 {
		return fmt.Errorf("SetThreadExecutionState(%#x) returned zero", state)
	}
	return fmt.Errorf("SetThreadExecutionState(%#x): %w", state, callErr)
}
