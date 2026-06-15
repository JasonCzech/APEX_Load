package main

import "testing"

// TestWailsWebviewOriginsCoverPlatforms guards the allow-list passed to the
// embedded API server. The dashboard runs inside the Wails webview and makes
// cross-origin requests to the loopback API; if these origins drift the CORS
// preflight is rejected and the dashboard reports "Authentication check failed".
func TestWailsWebviewOriginsCoverPlatforms(t *testing.T) {
	want := map[string]bool{
		"wails://wails":          false, // macOS (WKWebView, Wails v2.12) — confirmed location.origin
		"http://wails.localhost": false, // Windows (WebView2) / Linux (WebKitGTK)
	}
	for _, origin := range wailsWebviewOrigins {
		if _, ok := want[origin]; ok {
			want[origin] = true
		}
	}
	for origin, seen := range want {
		if !seen {
			t.Errorf("wailsWebviewOrigins missing required origin %q", origin)
		}
	}
}

func TestGetAPIOriginWithoutServer(t *testing.T) {
	app := NewApp()
	if got := app.GetAPIOrigin(); got != "" {
		t.Fatalf("GetAPIOrigin with no server = %q, want empty", got)
	}
}
