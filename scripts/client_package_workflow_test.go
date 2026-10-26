package scripts_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
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
	if got := configuration["frontend:install"]; got != "npm run check:node && npm ci --ignore-scripts" {
		t.Fatalf("Wails frontend install = %v, want pinned Node validation plus immutable lockfile install", got)
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
		// Derived from the repository pin rather than hardcoded. A correct pin
		// bump is *forced* to edit this workflow's setup-node line, so a literal
		// here failed on exactly the change it was meant to bless. The
		// cross-file equality (every setup-node pin under .github == .nvmrc) is
		// owned by dashboard/scripts/check-node-version.node-test.mjs; this
		// entry only asserts the desktop workflow provisions a pinned Node
		// toolchain at all.
		"node-version: " + pinnedNodeVersion(t),
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
		"go test -count=1 -v ./pkg/internal/winfs\n          go test -count=1 -v ./pkg/internal/procidentity ./pkg/internal/localfs",
		// The native step, unconditional on every leg, from its name through the
		// process identity and local-filesystem tests.
		"- name: Native analytics contracts and controller build\n        shell: bash\n        env:\n          CGO_ENABLED: '1'\n        run: |\n          set -euo pipefail\n          # The native worker suite outlasts go test's 10-minute default on slower\n          # hosted runners. It refuses work when memory runs low, so pkg/api (a\n          # multi-gigabyte compile) gets its own run instead of sharing the machine,\n          # and none on a runner too small to compile it.\n          go test -timeout 30m -tags analytics_duckdb ./pkg/analysis/... -run 'Test(Analysis|Native|Worker|Parquet|Lagged|BucketSeries|RCAImported)'\n          # The process identity/exit proof and local-filesystem probe behind\n          # native writer ownership, on every desktop platform (darwin has no\n          # other lane for them).\n          go test -count=1 -v ./pkg/internal/procidentity ./pkg/internal/localfs\n",
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

// The desktop matrix job's native-analytics step runs the process identity
// and local-filesystem tests on every leg, so neither the job nor the step
// may carry a condition or a failure allowance, wherever in the step the key
// is written (a text pin cannot see one after the step's last run line).
func TestDesktopNativeAnalyticsStepRunsUnconditionallyOnEveryLeg(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatalf("read desktop workflow: %v", err)
	}
	if err = checkUnconditionalDesktopNativeStep(content); err != nil {
		t.Fatal(err)
	}
}

// desktopNativeLegs are the desktop matrix's operating systems; the native
// step must run on each of them.
var desktopNativeLegs = []string{"macos-latest", "ubuntu-24.04", "ubuntu-24.04-arm", "windows-latest"}

const desktopNativeIdentityTests = "go test -count=1 -v ./pkg/internal/procidentity ./pkg/internal/localfs"

func checkUnconditionalDesktopNativeStep(content []byte) error {
	var workflow struct {
		Jobs map[string]map[string]any `yaml:"jobs"`
	}
	if err := yaml.Unmarshal(content, &workflow); err != nil {
		return fmt.Errorf("parse desktop workflow: %w", err)
	}
	job, ok := workflow.Jobs["desktop"]
	if !ok {
		return errors.New("desktop workflow has no desktop job")
	}
	for _, key := range []string{"if", "continue-on-error"} {
		if _, present := job[key]; present {
			return fmt.Errorf("desktop job carries %q", key)
		}
	}
	// Each leg runs on its own matrix OS, so the legs below are the runners.
	if runsOn, _ := job["runs-on"].(string); runsOn != "${{ matrix.os }}" {
		return fmt.Errorf("desktop job runs-on %q, want ${{ matrix.os }}", job["runs-on"])
	}
	strategy, _ := job["strategy"].(map[string]any)
	matrix, _ := strategy["matrix"].(map[string]any)
	// Only an include list: an exclude (or a cross-product axis) could drop a
	// leg the include list still names.
	for key := range matrix {
		if key != "include" {
			return fmt.Errorf("desktop matrix carries %q", key)
		}
	}
	include, _ := matrix["include"].([]any)
	legs := []string{}
	for _, raw := range include {
		leg, _ := raw.(map[string]any)
		name, _ := leg["os"].(string)
		legs = append(legs, name)
	}
	slices.Sort(legs)
	if !slices.Equal(legs, desktopNativeLegs) {
		return fmt.Errorf("desktop matrix legs %v, want %v", legs, desktopNativeLegs)
	}
	steps, _ := job["steps"].([]any)
	found := 0
	for _, raw := range steps {
		step, _ := raw.(map[string]any)
		if step["name"] != "Native analytics contracts and controller build" {
			continue
		}
		found++
		for _, key := range []string{"if", "continue-on-error"} {
			if _, present := step[key]; present {
				return fmt.Errorf("native analytics step carries %q", key)
			}
		}
		run, _ := step["run"].(string)
		if err := checkDesktopNativeIdentityCommand(run); err != nil {
			return err
		}
	}
	if found != 1 {
		return fmt.Errorf("desktop job has %d native analytics steps, want 1", found)
	}
	return nil
}

// checkDesktopNativeIdentityCommand requires the step's script to start in
// strict mode and to reach the exact identity command line (not commented
// out, not followed by "|| true"). Before that line it allows only comments
// and plain go test commands, so nothing (an exit 0, a set +e, a trap, an if)
// can skip the command or swallow its failure.
func checkDesktopNativeIdentityCommand(run string) error {
	strict := false
	for _, raw := range strings.Split(run, "\n") {
		line := strings.TrimSpace(raw)
		switch {
		case line == desktopNativeIdentityTests:
			if !strict {
				return errors.New("native analytics step does not start in strict mode (set -euo pipefail)")
			}
			return nil
		case line == "" || strings.HasPrefix(line, "#"):
		case !strict:
			if line != "set -euo pipefail" {
				return errors.New("native analytics step does not start in strict mode (set -euo pipefail)")
			}
			strict = true
		case !plainGoTest(line):
			return fmt.Errorf("native analytics step runs %q before the identity tests", line)
		}
	}
	return errors.New("native analytics step does not run the identity and filesystem tests exactly")
}

// plainGoTest reports whether a script line is one go test command: outside
// its single-quoted arguments (a -run pattern) it has no shell operator,
// redirection, substitution, double quote or backslash (a trailing one would
// join the next line, the identity command, onto it). With those banned a
// separate exit or set command cannot follow on the same line.
func plainGoTest(line string) bool {
	if !strings.HasPrefix(line, "go test ") {
		return false
	}
	parts := strings.Split(line, "'")
	if len(parts)%2 == 0 {
		return false // an unterminated quote
	}
	for i := 0; i < len(parts); i += 2 {
		if strings.ContainsAny(parts[i], "|&;`$<>()\"\\") {
			return false
		}
	}
	return true
}

// The parser check catches a condition or failure allowance on the job or on
// the step (after its name, or after the step's last key, before the next
// step), a missing or excluded matrix leg, a duplicated step, a weakened
// command line and anything before it that could skip it; each refusal
// names what it caught, so a parse failure cannot pass for a catch.
func TestDesktopNativeAnalyticsStepCheckCatchesConditions(t *testing.T) {
	content, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatalf("read desktop workflow: %v", err)
	}
	workflow := string(content)
	if err = checkUnconditionalDesktopNativeStep(content); err != nil {
		t.Fatal(err)
	}
	// The desktop matrix job's step is the first of the two steps with this
	// header; the recording-agent job has the other.
	header := "      - name: Native analytics contracts and controller build\n        shell: bash\n"
	headerAt := strings.Index(workflow, header)
	if headerAt < 0 {
		t.Fatal("unexpected desktop workflow layout for the mutation check")
	}
	nextStep := strings.Index(workflow[headerAt+len(header):], "\n      - name: ")
	jobAt := strings.Index(workflow, "\n  desktop:\n")
	runLine := "          " + desktopNativeIdentityTests + "\n"
	if nextStep < 0 || jobAt < 0 || !strings.Contains(workflow[headerAt:], runLine) {
		t.Fatal("unexpected desktop workflow layout for the mutation check")
	}
	afterStep := headerAt + len(header) + nextStep + 1
	afterName := func(line string) string {
		return workflow[:headerAt] + "      - name: Native analytics contracts and controller build\n" + line + "        shell: bash\n" + workflow[headerAt+len(header):]
	}
	jobLine := jobAt + len("\n  desktop:\n")
	// inStep replaces the command line inside the native step only (the
	// Windows HAR step runs the same packages earlier in the file).
	inStep := func(replacement string) string {
		return workflow[:headerAt] + strings.Replace(workflow[headerAt:], runLine, replacement, 1)
	}
	for name, c := range map[string]struct{ mutated, want string }{
		"if after the name":                {afterName("        if: runner.os == 'Linux'\n"), `"if"`},
		"if after the step":                {workflow[:afterStep] + "        if: runner.os == 'Linux'\n" + workflow[afterStep:], `"if"`},
		"continue-on-error after the name": {afterName("        continue-on-error: true\n"), `"continue-on-error"`},
		"continue-on-error after the step": {workflow[:afterStep] + "        continue-on-error: true\n" + workflow[afterStep:], `"continue-on-error"`},
		"if on the job":                    {workflow[:jobLine] + "    if: false\n" + workflow[jobLine:], `desktop job carries "if"`},
		"continue-on-error on the job":     {workflow[:jobLine] + "    continue-on-error: true\n" + workflow[jobLine:], `desktop job carries "continue-on-error"`},
		"Windows leg dropped":              {strings.Replace(workflow, "            os: windows-latest\n", "            os: ubuntu-22.04\n", 1), "desktop matrix legs"},
		"command commented out":            {inStep("          # " + desktopNativeIdentityTests + "\n"), "before the identity tests"},
		"command made infallible":          {inStep("          " + desktopNativeIdentityTests + " || true\n"), `|| true" before the identity tests`},
		"exit 0 before the command":        {inStep("          exit 0\n" + runLine), `"exit 0" before the identity tests`},
		"set +e before the command":        {inStep("          set +e\n" + runLine), `"set +e" before the identity tests`},
		"strict mode dropped":              {workflow[:headerAt] + strings.Replace(workflow[headerAt:], "          set -euo pipefail\n", "", 1), "strict mode"},
		"matrix exclude":                   {strings.Replace(workflow, "        include:\n", "        exclude:\n          - os: windows-latest\n        include:\n", 1), `desktop matrix carries "exclude"`},
		"set +e after a go test":           {inStep("          go test ./pkg/analysis; set +e\n" + runLine), `"go test ./pkg/analysis; set +e" before the identity tests`},
		"set +e after a go test with &&":   {inStep("          go test ./pkg/analysis && set +e\n" + runLine), `"go test ./pkg/analysis && set +e" before the identity tests`},
		"unterminated quote":               {inStep("          go test ./pkg/analysis -run 'Test\n" + runLine), `"go test ./pkg/analysis -run 'Test" before the identity tests`},
		"line continuation":                {inStep("          go test ./pkg/analysis -args \\\n" + runLine), `-args \\" before the identity tests`},
		"command before strict mode":       {workflow[:headerAt] + strings.Replace(workflow[headerAt:], "          set -euo pipefail\n", runLine+"          set -euo pipefail\n", 1), "strict mode"},
		"runs-on changed":                  {strings.Replace(workflow, "    runs-on: ${{ matrix.os }}\n", "    runs-on: ubuntu-24.04\n", 1), "desktop job runs-on"},
		"step duplicated":                  {workflow[:afterStep] + workflow[headerAt:afterStep] + workflow[afterStep:], "2 native analytics steps"},
	} {
		if c.mutated == workflow {
			t.Errorf("%s: the mutation did not apply", name)
			continue
		}
		if err := checkUnconditionalDesktopNativeStep([]byte(c.mutated)); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s was not caught by name: %v", name, err)
		}
	}
}
