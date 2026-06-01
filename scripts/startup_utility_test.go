package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestWindowsStartupUtilityIncludesLocalRunAndRecorderProxyLifecycle(t *testing.T) {
	content, err := os.ReadFile("start-local-windows.ps1")
	if err != nil {
		t.Fatalf("read startup utility: %v", err)
	}
	script := string(content)
	checks := []string{
		"[ValidateSet('Dev', 'Embedded')]",
		"APEXLOAD_ADDR",
		"APEXLOAD_RECORDER_PROXY_ADDR",
		"APEXLOAD_API_PROXY",
		"Start-ApexLoadWindow",
		"Set-ApexLoadWindowsRecorderProxy",
		"Restore-ApexLoadWindowsRecorderProxy",
		"Invoke-ProxySettingsChanged",
		"HKCU:\\Software\\Microsoft\\Windows\\CurrentVersion\\Internet Settings",
		"windows-11-wininet.json",
		"@('run', 'dev', '--', '--host'",
		"@('run', './cmd/controller')",
		"ConfigureRecorderProxy cannot be combined with -NoWait",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("startup utility missing %q", check)
		}
	}
}
