//go:build darwin && !cgo

package main

import "context"

// Wails' Cocoa getters are not safe from the goroutine used by OnBeforeClose.
// Production Darwin builds require cgo; fail closed rather than read AppKit from
// an unsupported thread in a non-cgo diagnostic build.
func captureNativeWindowGeometry(context.Context) (nativeWindowGeometry, bool) {
	return nativeWindowGeometry{}, false
}
