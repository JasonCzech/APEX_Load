package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestClientPackageWorkflowUsesSignedPackageScripts(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatalf("read desktop workflow: %v", err)
	}
	workflow := string(content)
	checks := []string{
		"Build Client Packages",
		"actions/checkout@v4",
		"actions/setup-go@v5",
		"actions/upload-artifact@v4",
		"./scripts/package-desktop.sh",
		"./scripts/package-recording-agent.sh",
		"APEXLOAD_MACOS_CERTIFICATE_P12_BASE64",
		"APEXLOAD_MACOS_CODESIGN_IDENTITY",
		"notarytool store-credentials",
		"APEXLOAD_WINDOWS_CODESIGN_PFX_BASE64",
		"APEXLOAD_WINDOWS_CODESIGN_TOOL=osslsigncode",
		"APEXLOAD_WINDOWS_CODESIGN_TOOL=signtool",
		"APEXLOAD_REQUIRE_TRUSTED_PACKAGES",
		"startsWith(github.ref, 'refs/tags/')",
		"APEXLOAD_GPG_PRIVATE_KEY",
		"APEXLOAD_CHECKSUM_SIGN=1",
	}
	for _, check := range checks {
		if !strings.Contains(workflow, check) {
			t.Fatalf("desktop workflow missing %q", check)
		}
	}
}

// TestClientPackageWorkflowValidatesOnPullRequests guards the CI broadening:
// desktop builds must run on PRs/main (not just tags) so they stop rotting
// between releases, with npm + Go caching and a pinned Wails CLI, while signing
// stays tag-gated.
func TestClientPackageWorkflowValidatesOnPullRequests(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatalf("read desktop workflow: %v", err)
	}
	workflow := string(content)
	checks := []string{
		"pull_request:",
		"branches: [main]",
		"cache: npm",
		"cache-dependency-path: dashboard/package-lock.json",
		"wails/v2/cmd/wails@v2.12.0", // pinned, not @latest
	}
	for _, check := range checks {
		if !strings.Contains(workflow, check) {
			t.Fatalf("desktop workflow missing %q", check)
		}
	}
	if strings.Contains(workflow, "cmd/wails@latest") {
		t.Fatal("desktop workflow should pin the Wails CLI version, not use @latest")
	}
}
