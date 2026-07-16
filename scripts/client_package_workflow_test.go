package scripts_test

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestClientPackageWorkflowKeepsReleaseCredentialsIsolated(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatalf("read desktop workflow: %v", err)
	}
	workflow := string(content)
	checks := []string{
		"Build Client Packages",
		"./scripts/package-desktop.sh",
		"./scripts/package-recording-agent.sh",
		"APEXLOAD_REQUIRE_TRUSTED_PACKAGES",
		"uses: ./.github/actions/upload-artifact",
	}
	for _, check := range checks {
		if !strings.Contains(workflow, check) {
			t.Fatalf("desktop workflow missing %q", check)
		}
	}
	for _, prohibited := range []string{
		"tags:",
		"secrets.",
		"APEXLOAD_MACOS_CERTIFICATE_P12_BASE64",
		"APEXLOAD_MACOS_CODESIGN_IDENTITY",
		"APEXLOAD_WINDOWS_CODESIGN_PFX_BASE64",
		"APEXLOAD_GPG_PRIVATE_KEY",
		"notarytool",
	} {
		if strings.Contains(workflow, prohibited) {
			t.Fatalf("ordinary client CI must not contain release trigger/credential fragment %q", prohibited)
		}
	}

	releaseRaw, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatalf("read release workflow: %v", err)
	}
	release := string(releaseRaw)
	for _, required := range []string{
		"environment: client-release",
		"APEXLOAD_MACOS_CERTIFICATE_P12_BASE64",
		"APEXLOAD_MACOS_CODESIGN_IDENTITY",
		"APEXLOAD_MACOS_NOTARY_KEY_P8_BASE64",
		"APEXLOAD_MACOS_NOTARY_KEY_ID",
		"APEXLOAD_MACOS_NOTARY_ISSUER_ID",
		"APEXLOAD_WINDOWS_CODESIGN_PFX_BASE64",
		"Import-PfxCertificate",
		"APEXLOAD_WINDOWS_CODESIGN_SHA1",
	} {
		if !strings.Contains(release, required) {
			t.Fatalf("protected release workflow missing %q", required)
		}
	}
	for _, prohibited := range []string{
		"/p $env:WINDOWS_PFX_PASSWORD",
		"APEXLOAD_WINDOWS_CODESIGN_PASSWORD: ${{ secrets.APEXLOAD_WINDOWS_CODESIGN_PASSWORD }}",
		`security import "$certificate" -P "$CERTIFICATE_PASSWORD"`,
		`--password "$NOTARY_PASSWORD"`,
	} {
		if strings.Contains(release, prohibited) {
			t.Fatalf("protected release workflow exposes the Authenticode password through process arguments via %q", prohibited)
		}
	}
	for _, action := range []string{"actions/checkout", "actions/setup-go"} {
		pattern := regexp.MustCompile(regexp.QuoteMeta(action) + `@[0-9a-f]{40}`)
		if !pattern.MatchString(workflow) {
			t.Fatalf("desktop workflow must pin %s to a full commit SHA", action)
		}
	}

	uploadWrapper, err := os.ReadFile("../.github/actions/upload-artifact/action.yml")
	if err != nil {
		t.Fatalf("read platform-compatible artifact upload action: %v", err)
	}
	wrapper := string(uploadWrapper)
	for _, required := range []string{
		"github.server_url == 'https://github.com'",
		"github.server_url != 'https://github.com'",
	} {
		if !strings.Contains(wrapper, required) {
			t.Errorf("artifact upload wrapper missing %q", required)
		}
	}
	pattern := regexp.MustCompile(`actions/upload-artifact@[0-9a-f]{40}`)
	if got := len(pattern.FindAllString(wrapper, -1)); got != 2 {
		t.Fatalf("artifact upload wrapper must pin both GitHub.com and GHES clients, got %d", got)
	}
}

func TestDesktopWailsBuildUsesImmutableDashboardInstall(t *testing.T) {
	content, err := os.ReadFile("../desktop/wails.json")
	if err != nil {
		t.Fatalf("read Wails configuration: %v", err)
	}
	var configuration map[string]any
	if err := json.Unmarshal(content, &configuration); err != nil {
		t.Fatalf("parse Wails configuration: %v", err)
	}
	if got := configuration["frontend:install"]; got != "npm ci --ignore-scripts" {
		t.Fatalf("Wails frontend install = %v, want immutable lockfile install", got)
	}
}

// TestClientPackageWorkflowValidatesOnPullRequests guards the CI broadening:
// desktop builds must run on PRs/main (not just tags) so they stop rotting
// between releases, with a pinned Node toolchain and Wails CLI. Signing is
// isolated in the protected release workflow. Dependency caches remain disabled
// in this artifact-publishing workflow so pull-request-controlled caches cannot
// influence build validation.
func TestClientPackageWorkflowValidatesOnPullRequests(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatalf("read desktop workflow: %v", err)
	}
	workflow := string(content)
	checks := []string{
		"pull_request:",
		"branches: [main]",
		"node-version: 22.22.0",
		"cache: false",
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
	if strings.Contains(workflow, "cache: npm") {
		t.Fatal("desktop release workflow must not consume a pull-request-writable npm cache")
	}
}
