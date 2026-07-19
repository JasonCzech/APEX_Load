//go:build linux && !cgo

package main

// Production Linux Wails builds use cgo/GTK. Fail closed in non-cgo diagnostic
// builds rather than executing a native UI mutation from an arbitrary thread.
func dispatchNativeUI(func()) bool {
	return false
}
