package main

import "testing"

// TestSetDockBadgeNoPanic exercises the bound method on the host platform. On
// macOS the dock-tile mutation is dispatched to the main queue (which never runs
// in a test binary, and NSApp is nil there), so it is safe; elsewhere it is a
// no-op. We only assert it doesn't panic.
func TestSetDockBadgeNoPanic(t *testing.T) {
	app := NewApp()
	app.SetDockBadge("3")
	app.SetDockBadge("")
}
