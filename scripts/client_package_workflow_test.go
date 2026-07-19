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
		"dpkg-dev",
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
	macImport := strings.Index(release, "- name: Import macOS signing certificate")
	desktopBuild := strings.Index(release, "- name: Build native desktop client")
	apexctlBuild := strings.Index(release, "- name: Build platform apexctl")
	macSign := strings.Index(release, "- name: Sign and notarize macOS apexctl")
	if macImport < 0 || desktopBuild < 0 || apexctlBuild < 0 || macSign < 0 || macImport < desktopBuild || macImport < apexctlBuild || macImport > macSign {
		t.Fatal("macOS signing/notary credentials must be imported only after unsigned desktop and apexctl builds, immediately before signing")
	}
	for _, required := range []string{
		"environment: client-release",
		"WAILS_BUILD_TAGS",
		"webkit2_41",
		"APEXLOAD_MACOS_CERTIFICATE_P12_BASE64",
		"APEXLOAD_MACOS_CODESIGN_IDENTITY",
		"APEXLOAD_MACOS_NOTARY_KEY_P8_BASE64",
		"APEXLOAD_MACOS_NOTARY_KEY_ID",
		"APEXLOAD_MACOS_NOTARY_ISSUER_ID",
		"APEXLOAD_WINDOWS_CODESIGN_PFX_BASE64",
		"Import-PfxCertificate",
		"APEXLOAD_WINDOWS_CODESIGN_SHA1",
		"apexload-windows-cert-store.txt",
		"APEXLoadRelease-${{ github.run_id }}-${{ github.run_attempt }}-${{ matrix.name }}",
		"APEXLOAD_WINDOWS_CODESIGN_STORE",
		"-DeleteKey",
		"dpkg-dev",
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
		`Cert:\CurrentUser\My\$env:APEXLOAD_WINDOWS_CODESIGN_SHA1`,
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
	hooks, ok := configuration["postBuildHooks"].(map[string]any)
	hook, _ := hooks["windows/*"].(string)
	if !ok || !strings.Contains(hook, "sign-windows-binary.sh") {
		t.Fatalf("Wails Windows post-build hook does not sign the app before NSIS packaging: %#v", configuration["postBuildHooks"])
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
		"- '**/*.go'",
		"node-version: 22.22.0",
		"cache: false",
		"desktop/.wails-version",
		"scripts/sign-windows-binary.sh",
		"scripts/stamp-desktop-version.mjs",
		"scripts/semver-to-debian-version.sh",
		"scripts/ci/install-nsis.ps1",
		"Makefile",
		"- name: Install Wails CLI\n        shell: bash",
		"name: Install checksum-pinned NSIS",
		"scripts/ci/install-nsis.ps1",
		"wails/v2/cmd/wails@${WAILS_VERSION}",
		"WAILS_BUILD_TAGS",
		"webkit2_41",
		"./scripts/build-desktop-local.sh",
		"name: Smoke-test Windows backend",
		"name: Smoke-test Windows installer registration",
		"Previous.HAR.Handler",
		"User.Selected.Handler",
		"Previous.Protocol.Owner",
		"User Selected\\handler.exe",
		"unrelated-sentinel.txt",
		"running-app uninstall exited",
		"_?=$installDir",
		"AppUserModelId\\apexload.exe",
		"apexload-single-install-policy",
		"legacy-marker second-location installer",
		"foreign-scope same-path installer",
		"foreign-scope stale same-path installer",
		"foreign association-only stale installer",
		"second-location installer exited",
		"want 68",
		"same-location repair",
		"must survive rejected second install",
		"owned Add/Remove Programs registration survived uninstall",
		"name: Smoke-test Linux package registration",
		"go test -count=1 ./pkg/internal/diskspace",
		"TestStageArchiveDownload",
		"TestGeneratorArchiveWriterEnforcesByteAndFreeSpaceBounds",
		"go build -trimpath ./cmd/...",
		"/api/v1/system/health",
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
	if strings.Contains(workflow, "github.event.pull_request.head.repo") {
		t.Fatal("credential-free desktop validation must not skip external pull requests")
	}
	if strings.Contains(workflow, "side-by-side installer") {
		t.Fatal("desktop workflow must enforce the single-location installation contract")
	}
}

func TestDesktopWailsVersionMatchesGoModule(t *testing.T) {
	pinnedRaw, err := os.ReadFile("../desktop/.wails-version")
	if err != nil {
		t.Fatal(err)
	}
	pinned := strings.TrimSpace(string(pinnedRaw))
	goMod, err := os.ReadFile("../go.mod")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(goMod), "github.com/wailsapp/wails/v2 "+pinned) {
		t.Fatalf("go.mod does not use pinned Wails %s", pinned)
	}
	for _, path := range []string{"../.github/workflows/desktop.yml", "../.github/workflows/release.yml", "build-desktop-local.sh"} {
		content, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if !strings.Contains(string(content), ".wails-version") {
			t.Errorf("%s does not read the shared Wails version pin", path)
		}
	}
	plist, err := os.ReadFile("../desktop/build/darwin/Info.plist")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(plist), "<key>NSHighResolutionCapable</key>") ||
		!strings.Contains(string(plist), "<true/>") ||
		strings.Contains(string(plist), "<string>true</string>") {
		t.Fatal("macOS high-resolution capability must be encoded as a plist Boolean")
	}
}

func TestDesktopBuildProducesPlatformInstallMetadata(t *testing.T) {
	buildScriptRaw, err := os.ReadFile("build-desktop-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	buildScript := string(buildScriptRaw)
	for _, required := range []string{
		`WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS},webkit2_41"`,
		`build_args+=(-tags "${WAILS_BUILD_TAGS}")`,
		`build_args+=(-nsis -installscope user)`,
		`MACOS_MIN_VERSION="12.0"`,
		`CGO_CFLAGS="${CGO_CFLAGS:+${CGO_CFLAGS} }-mmacosx-version-min=${MACOS_MIN_VERSION}"`,
		`CGO_LDFLAGS="${CGO_LDFLAGS:+${CGO_LDFLAGS} }-mmacosx-version-min=${MACOS_MIN_VERSION}"`,
		"stamp-desktop-version.mjs",
		"APEXLOAD_UX_VERSION",
	} {
		if !strings.Contains(buildScript, required) {
			t.Errorf("desktop build script missing %q", required)
		}
	}

	releaseRaw, err := os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	release := string(releaseRaw)
	for _, required := range []string{
		"Verify macOS desktop metadata",
		"Verify Windows desktop metadata and installer",
		"- name: Install Wails\n        shell: bash",
		"apexload-desktop-*.deb",
		"Get-AuthenticodeSignature",
		"ConvertTo-FourPartVersion",
		"actualInstallerVersion",
		"does not equal",
	} {
		if !strings.Contains(release, required) {
			t.Errorf("release workflow missing %q", required)
		}
	}
	if strings.Contains(release, ".ProductVersion.StartsWith(") {
		t.Fatal("Windows release metadata check accepts longer version prefixes")
	}

	manifestRaw, err := os.ReadFile("release/create-manifest.py")
	if err != nil {
		t.Fatal(err)
	}
	manifest := string(manifestRaw)
	for _, required := range []string{"DESKTOP_INSTALLER", `"schema_version": 2`, `"desktop_installers"`, `("linux", "amd64", "deb")`, `("linux", "arm64", "deb")`} {
		if !strings.Contains(manifest, required) {
			t.Errorf("release manifest does not enforce installable desktop package %q", required)
		}
	}
	verifierRaw, err := os.ReadFile("release/verify-release.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(verifierRaw), ".desktop_installers | length == 2") {
		t.Fatal("release verifier does not require both Linux desktop installers")
	}
	for _, required := range []string{
		`"apexload-desktop-" + $root.version + "-linux-" + .architecture + ".deb"`,
		`.asset == $installer.asset and .sha256 == $installer.sha256`,
	} {
		if !strings.Contains(string(verifierRaw), required) {
			t.Fatalf("release verifier does not bind desktop installers to inventory: missing %q", required)
		}
	}
	if !strings.Contains(string(verifierRaw), `manifest_schema" == 1 || "$manifest_schema" == 2`) {
		t.Fatal("release verifier cannot consume legacy schema-v1 releases")
	}
}
