//go:build windows

package main

import "syscall"

// SetThreadExecutionState flags (winbase.h).
const (
	esContinuous     = 0x80000000
	esSystemRequired = 0x00000001
)

// startSleepInhibitor asks Windows to keep the system awake via
// SetThreadExecutionState(ES_CONTINUOUS|ES_SYSTEM_REQUIRED). The stop func
// clears the assertion (ES_CONTINUOUS). Best-effort: the OS also clears the
// assertion automatically when the process exits, so a missed reset cannot
// keep the machine awake beyond the app's lifetime.
func startSleepInhibitor() func() {
	proc := syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
	_, _, _ = proc.Call(uintptr(esContinuous | esSystemRequired))
	return func() {
		_, _, _ = proc.Call(uintptr(esContinuous))
	}
}
