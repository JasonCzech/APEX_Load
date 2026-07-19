//go:build linux && !cgo

package main

import "context"

// Production Linux Wails builds use cgo/GTK. Fail closed in non-cgo diagnostic
// builds instead of calling GTK-backed Wails getters from an arbitrary thread.
func captureNativeWindowGeometry(context.Context) (nativeWindowGeometry, bool) {
	return nativeWindowGeometry{}, false
}
