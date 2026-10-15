package main

import "github.com/wailsapp/wails/v2/pkg/options"

// desktopDragAndDrop returns the file-drop options for goos. Dropped absolute
// paths reach the dashboard through OnFileDrop, and drop targets opt in with
// the `--wails-drop-target: drop` CSS marker.
//
// DisableWebViewDrop stops the webview from opening a file dropped outside a
// registered target. That is only safe where Wails reads drops natively
// (macOS, Linux). On Windows Wails takes the dropped paths from the page's own
// drop event, which WebView2 delivers only while it accepts external drops, so
// disabling them there turns every file and folder drop away. Instead the
// dashboard, as soon as its page loads, registers OnFileDrop, which installs
// Wails' page listeners that cancel a file drop's default, and cancels the
// other external drops (a link or text) that would navigate the window away
// (installDesktopDropHandling in dashboard/src/desktop/useDesktopFileDrop.ts).
func desktopDragAndDrop(goos string) *options.DragAndDrop {
	return &options.DragAndDrop{
		EnableFileDrop:     true,
		DisableWebViewDrop: goos != "windows",
	}
}
