package main

import (
	"errors"
	"strings"
	"testing"
)

var errTest = errors.New("boom")

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

func TestGetStartupErrorDefaultEmpty(t *testing.T) {
	app := NewApp()
	if got := app.GetStartupError(); got != "" {
		t.Fatalf("GetStartupError on fresh app = %q, want empty", got)
	}
}

func TestFailStartupRecordsMessage(t *testing.T) {
	app := NewApp()
	app.failStartup("failed to initialize local storage", errTest)
	got := app.GetStartupError()
	if !strings.Contains(got, "failed to initialize local storage") {
		t.Errorf("startup error %q should contain the failure context", got)
	}
}

func TestLastRouteRoundTrip(t *testing.T) {
	app := NewApp()
	if got := app.GetLastRoute(); got != "" {
		t.Fatalf("GetLastRoute on fresh app = %q, want empty", got)
	}
	app.SetLastRoute("  #/scripts  ")
	if got := app.GetLastRoute(); got != "#/scripts" {
		t.Fatalf("GetLastRoute = %q, want trimmed %q", got, "#/scripts")
	}
}
