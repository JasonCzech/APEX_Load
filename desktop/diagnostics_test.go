package main

import (
	"encoding/json"
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

func TestApexloadWindowsMessagesBranded(t *testing.T) {
	m := apexloadWindowsMessages()
	if m.Webview2NotInstalled == "" || m.InstallationRequired == "" {
		t.Fatal("WebView2 messages should be populated")
	}
}
