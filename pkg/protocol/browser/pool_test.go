package browser

import (
	"errors"
	"slices"
	"strings"
	"testing"
)

func TestChromiumLaunchOptions_Defaults(t *testing.T) {
	t.Setenv("APEXLOAD_BROWSER_EXECUTABLE", "")
	t.Setenv("APEXLOAD_BROWSER_SANDBOX", "")

	options := chromiumLaunchOptions(true, "")
	if !slices.Contains(options.Args, "--no-sandbox") {
		t.Fatal("expected --no-sandbox by default (hardened pods cannot start the Chromium sandbox)")
	}
	if !slices.Contains(options.Args, "--disable-dev-shm-usage") {
		t.Fatal("expected --disable-dev-shm-usage always (64MB container /dev/shm)")
	}
	if options.ExecutablePath != nil {
		t.Fatalf("expected no executable override, got %q", *options.ExecutablePath)
	}
	if options.Channel != nil {
		t.Fatalf("expected no channel for the chromium engine, got %q", *options.Channel)
	}
	if options.Headless == nil || !*options.Headless {
		t.Fatal("expected headless to propagate")
	}
}

func TestChromiumLaunchOptions_SandboxOptIn(t *testing.T) {
	for _, value := range []string{"true", "1", "YES", "on"} {
		t.Setenv("APEXLOAD_BROWSER_SANDBOX", value)
		options := chromiumLaunchOptions(true, "")
		if slices.Contains(options.Args, "--no-sandbox") {
			t.Fatalf("APEXLOAD_BROWSER_SANDBOX=%s must drop --no-sandbox", value)
		}
		if !slices.Contains(options.Args, "--disable-dev-shm-usage") {
			t.Fatalf("--disable-dev-shm-usage must remain with APEXLOAD_BROWSER_SANDBOX=%s", value)
		}
	}

	t.Setenv("APEXLOAD_BROWSER_SANDBOX", "false")
	if options := chromiumLaunchOptions(true, ""); !slices.Contains(options.Args, "--no-sandbox") {
		t.Fatal("APEXLOAD_BROWSER_SANDBOX=false must keep --no-sandbox")
	}
}

func TestChromiumLaunchOptions_ExecutableOverride(t *testing.T) {
	t.Setenv("APEXLOAD_BROWSER_EXECUTABLE", "/usr/bin/chromium-browser")

	// The chromium engine drives the system executable directly.
	options := chromiumLaunchOptions(false, "")
	if options.ExecutablePath == nil || *options.ExecutablePath != "/usr/bin/chromium-browser" {
		t.Fatalf("expected executable override, got %+v", options.ExecutablePath)
	}

	// The chrome engine must drop its channel when overridden: channel
	// resolution looks for /opt/google/chrome/chrome, which container images
	// with a system chromium do not have.
	options = chromiumLaunchOptions(false, "chrome")
	if options.Channel != nil {
		t.Fatalf("expected channel dropped under executable override, got %q", *options.Channel)
	}
	if options.ExecutablePath == nil || *options.ExecutablePath != "/usr/bin/chromium-browser" {
		t.Fatal("expected chrome engine to use the executable override")
	}
}

func TestChromiumLaunchOptions_ChromeChannelWithoutOverride(t *testing.T) {
	t.Setenv("APEXLOAD_BROWSER_EXECUTABLE", "")
	options := chromiumLaunchOptions(true, "chrome")
	if options.Channel == nil || *options.Channel != "chrome" {
		t.Fatal("expected chrome channel when no executable override is set")
	}
}

func TestBrowserPoolKeySeparatesLaunchSettings(t *testing.T) {
	t.Setenv("APEXLOAD_BROWSER_EXECUTABLE", "")
	t.Setenv("APEXLOAD_BROWSER_SANDBOX", "")

	headless := browserPoolKey("chromium", true, browserQUICAuto)
	headed := browserPoolKey("chromium", false, browserQUICAuto)
	if headless == headed {
		t.Fatal("browser pool key did not include headless mode")
	}

	t.Setenv("APEXLOAD_BROWSER_EXECUTABLE", "/usr/bin/chromium")
	withExecutable := browserPoolKey("chromium", true, browserQUICAuto)
	if withExecutable == headless {
		t.Fatal("browser pool key did not include executable override")
	}
}

func TestRuntimeInstallWarning(t *testing.T) {
	tests := []struct {
		name        string
		engine      string
		probeError  error
		wantWarning bool
		wantProbe   bool
	}{
		{name: "default chromium", engine: "", wantWarning: false, wantProbe: false},
		{name: "chrome channel", engine: "chrome", wantWarning: false, wantProbe: false},
		{name: "installed webkit", engine: " WebKit ", wantWarning: false, wantProbe: true},
		{name: "missing webkit", engine: "WebKit", probeError: errors.New("runtime missing"), wantWarning: true, wantProbe: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			probed := false
			warning := runtimeInstallWarning(tc.engine, func(engine string) error {
				probed = true
				if engine != "webkit" {
					t.Fatalf("probe engine = %q, want webkit", engine)
				}
				return tc.probeError
			})
			if probed != tc.wantProbe {
				t.Fatalf("probe called = %t, want %t", probed, tc.wantProbe)
			}
			if (warning != "") != tc.wantWarning {
				t.Fatalf("warning = %q, want warning=%t", warning, tc.wantWarning)
			}
			if tc.wantWarning && (!strings.Contains(warning, "Playwright webkit") || !strings.Contains(warning, "runtime missing")) {
				t.Fatalf("warning = %q, want engine and probe error", warning)
			}
		})
	}
}
