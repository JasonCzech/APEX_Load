package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestIsExternalOpenScheme(t *testing.T) {
	cases := map[string]bool{
		"http":       true,
		"https":      true,
		"HTTPS":      true,
		"mailto":     true,
		"file":       false,
		"apexload":   false,
		"javascript": false,
		"":           false,
	}
	for scheme, want := range cases {
		if got := isExternalOpenScheme(scheme); got != want {
			t.Errorf("isExternalOpenScheme(%q) = %v, want %v", scheme, got, want)
		}
	}
}

func TestOpenURLRejectsNonExternalSchemes(t *testing.T) {
	app := NewApp()
	// ctx is nil → fails closed before scheme checks; still must not panic.
	if err := app.OpenURL("file:///etc/passwd"); err == nil {
		t.Error("expected error opening file:// without runtime")
	}
}

// TestValidateOpenURL exercises the scheme allow-list directly (the OpenURL
// path otherwise short-circuits on nil ctx in tests).
func TestValidateOpenURL(t *testing.T) {
	ok := []string{"https://example.com/a", "http://localhost:8080", "mailto:ops@example.com"}
	for _, u := range ok {
		if _, err := validateOpenURL(u); err != nil {
			t.Errorf("validateOpenURL(%q) unexpected error: %v", u, err)
		}
	}
	rejected := []string{"file:///etc/passwd", "javascript:alert(1)", "data:text/html,x", "apexload://run/1", "vbscript:x"}
	for _, u := range rejected {
		if _, err := validateOpenURL(u); err == nil {
			t.Errorf("validateOpenURL(%q) should be rejected", u)
		}
	}
}

func TestSystemSettingsURLIsNarrowlyPlatformScoped(t *testing.T) {
	tests := []struct {
		goos      string
		target    string
		osVersion string
		want      string
	}{
		{goos: "darwin", target: "network", osVersion: "12.7.6", want: "x-apple.systempreferences:com.apple.preference.network"},
		{goos: "darwin", target: "network", osVersion: "13.0", want: "x-apple.systempreferences:com.apple.Network-Settings.extension"},
		{goos: "darwin", target: "network", osVersion: "15.5", want: "x-apple.systempreferences:com.apple.Network-Settings.extension"},
		{goos: "darwin", target: "network", osVersion: "unknown", want: "x-apple.systempreferences:com.apple.preference.network"},
		{goos: "windows", target: "proxy", want: "ms-settings:network-proxy"},
	}
	for _, tc := range tests {
		got, err := systemSettingsURLForVersion(tc.goos, tc.target, tc.osVersion)
		if err != nil || got != tc.want {
			t.Errorf("systemSettingsURL(%q, %q) = %q, %v; want %q", tc.goos, tc.target, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ goos, target string }{
		{goos: "darwin", target: "proxy"},
		{goos: "windows", target: "network"},
		{goos: "linux", target: "proxy"},
		{goos: "windows", target: "../../control-panel"},
	} {
		if got, err := systemSettingsURLForVersion(tc.goos, tc.target, "15.0"); err == nil || got != "" {
			t.Errorf("systemSettingsURL(%q, %q) = %q, %v; want rejection", tc.goos, tc.target, got, err)
		}
	}
}

func TestMacOSMajorVersion(t *testing.T) {
	for raw, want := range map[string]int{
		"15.5.1":  15,
		" 13.0 ":  13,
		"12":      12,
		"":        0,
		"unknown": 0,
	} {
		if got := macOSMajorVersion(raw); got != want {
			t.Errorf("macOSMajorVersion(%q) = %d, want %d", raw, got, want)
		}
	}
}

func TestExportDiagnosticsIsValidJSONWithoutSecrets(t *testing.T) {
	app := NewApp()
	app.appDataPath = "/tmp/apex"
	app.logPath = "/tmp/apex/logs"
	out, err := app.ExportDiagnostics()
	if err != nil {
		t.Fatalf("ExportDiagnostics: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal([]byte(out), &payload); err != nil {
		t.Fatalf("diagnostics is not valid JSON: %v", err)
	}
	if payload["product"] != "APEX Load Desktop" {
		t.Errorf("unexpected product: %v", payload["product"])
	}
	if payload["app_data_path"] != "/tmp/apex" {
		t.Errorf("app_data_path = %v", payload["app_data_path"])
	}
	// Never leak credential-bearing keys.
	for _, banned := range []string{"token", "secret", "password", "credentials"} {
		if _, ok := payload[banned]; ok {
			t.Errorf("diagnostics must not contain %q", banned)
		}
	}
}

// TestExportDiagnosticsScrubsStartupError verifies a startup error carrying a
// connection string with embedded credentials is redacted in the bundle.
func TestExportDiagnosticsScrubsStartupError(t *testing.T) {
	app := NewApp()
	app.startupErr = "failed to initialize local storage: dial postgres://apex:s3cr3t@db.internal:5432/apex: timeout"
	out, err := app.ExportDiagnostics()
	if err != nil {
		t.Fatalf("ExportDiagnostics: %v", err)
	}
	if strings.Contains(out, "s3cr3t") {
		t.Errorf("startup_error must not leak the embedded password:\n%s", out)
	}
	if !strings.Contains(out, "***@") {
		t.Errorf("expected userinfo to be redacted to ***@:\n%s", out)
	}
}

func TestScrubTextAndHomeRelativize(t *testing.T) {
	if got := scrubText("connect to mysql://u:p@h/db failed"); strings.Contains(got, "u:p@") {
		t.Errorf("scrubText left credentials: %q", got)
	}
	// homeRelativize collapses the home prefix to ~ and leaves outside paths intact.
	if got := homeRelativize("/var/log/apex"); got != "/var/log/apex" {
		t.Errorf("non-home path should be unchanged, got %q", got)
	}
}

func TestScrubTextRedactsQueryDSNAndTokenForms(t *testing.T) {
	input := strings.Join([]string{
		"connect postgres://apex:urlpass@db/apex?token=querytoken&sslmode=require",
		"Password=dsnpass;Api_Key=apikey;client_secret=clientsecret",
		`{"refresh_token":"refreshtoken","safe":"visible"}`,
		"Authorization: Bearer eyJhbGciOiJIUzI1Ni.secret.signature",
	}, "\n")
	got := scrubText(input)
	for _, secret := range []string{"urlpass", "querytoken", "dsnpass", "apikey", "clientsecret", "refreshtoken", "eyJhbGciOiJIUzI1Ni.secret.signature"} {
		if strings.Contains(got, secret) {
			t.Errorf("scrubText leaked %q:\n%s", secret, got)
		}
	}
	if !strings.Contains(got, `"safe":"visible"`) {
		t.Errorf("scrubText should preserve non-sensitive diagnostics: %s", got)
	}
}

func TestApexloadWindowsMessagesBranded(t *testing.T) {
	m := apexloadWindowsMessages()
	if m.Webview2NotInstalled == "" || m.InstallationRequired == "" {
		t.Fatal("WebView2 messages should be populated")
	}
}

// On Windows the home folder is scrubbed however a message spells it: any case,
// either separator. Elsewhere only the exact path is.
func TestReplaceHomeDirCoversWindowsSpellings(t *testing.T) {
	const home = `C:\Users\Me`
	got := replaceHomeDir(`open C:/users/ME/AppData/Local/x: denied; log c:\USERS\me\logs\a.log`, home, true)
	if want := `open ~/AppData/Local/x: denied; log ~\logs\a.log`; got != want {
		t.Errorf("windows scrub = %q, want %q", got, want)
	}
	if got := replaceHomeDir("/Users/me/a and /users/me/b", "/Users/me", false); got != "~/a and /users/me/b" {
		t.Errorf("non-windows scrub = %q", got)
	}
}
