//go:build !darwin

package main

// setDockBadge is a no-op on platforms without a wired taskbar-badge mechanism.
func setDockBadge(string) {}
