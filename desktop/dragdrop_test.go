package main

import "testing"

// Windows reads dropped paths from the page's drop event, so its webview must
// keep accepting external drops; elsewhere Wails reads drops natively and the
// webview's own drop handling stays off.
func TestDesktopDragAndDropKeepsWindowsDropsReachingThePage(t *testing.T) {
	for goos, wantDisabled := range map[string]bool{"darwin": true, "linux": true, "windows": false} {
		opts := desktopDragAndDrop(goos)
		if !opts.EnableFileDrop {
			t.Errorf("%s: file drop is off", goos)
		}
		if opts.DisableWebViewDrop != wantDisabled {
			t.Errorf("%s: DisableWebViewDrop = %v, want %v", goos, opts.DisableWebViewDrop, wantDisabled)
		}
	}
}
