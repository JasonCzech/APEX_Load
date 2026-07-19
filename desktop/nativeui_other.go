//go:build !linux

package main

// Wails' macOS and Windows frontends retain their existing synchronous runtime
// behavior. Only GTK requires the explicit main-loop handoff maintained here.
func dispatchNativeUI(operation func()) bool {
	if operation != nil {
		operation()
	}
	return true
}
