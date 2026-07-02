//go:build darwin || linux

package main

import (
	"os"
	"os/exec"
	"runtime"
)

// startSleepInhibitor launches the OS sleep-inhibitor subprocess and returns a
// stop func that releases the lock by killing it. Best-effort: if the tool is
// unavailable the stop func is a no-op.
func startSleepInhibitor() func() {
	name, args := inhibitorCommand(runtime.GOOS, os.Getpid())
	if name == "" {
		return func() {}
	}
	cmd := exec.Command(name, args...)
	configureInhibitorCommand(cmd)
	if err := cmd.Start(); err != nil {
		return func() {}
	}
	return func() {
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}
}
