//go:build !darwin && !linux && !windows

package main

// startSleepInhibitor is a no-op on platforms without a supported mechanism.
func startSleepInhibitor() func() {
	return func() {}
}
