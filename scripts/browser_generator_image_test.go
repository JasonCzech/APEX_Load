package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Observer pools run the generator on glibc with Playwright's own Chromium,
// and the controller runs the same build, so recording, Verify and observers
// report one Chromium major (WP4.1, decision D1 option A).
func TestBrowserImagesRunPlaywrightChromium(t *testing.T) {
	read := func(rel string) string {
		t.Helper()
		data, err := os.ReadFile("../" + rel)
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}
	pinned := regexp.MustCompile(`@sha256:[a-f0-9]{64}( |$)`)
	for _, rel := range []string{"Dockerfile.browser-generator", "Dockerfile.controller"} {
		dockerfile := read(rel)
		for _, line := range strings.Split(dockerfile, "\n") {
			if strings.HasPrefix(line, "FROM ") && !pinned.MatchString(line) {
				t.Errorf("%s base image is not digest-pinned: %s", rel, line)
			}
		}
		for _, required := range []string{
			"FROM debian:bookworm-slim@sha256:",
			"/src/dashboard/node_modules/playwright-core-go/ /opt/playwright-driver/package/",
			"ENV PLAYWRIGHT_BROWSERS_PATH=/ms-playwright",
			"node /opt/playwright-driver/package/cli.js install --with-deps chromium",
			"chmod -R a+rX /ms-playwright",
			"ARG PLAYWRIGHT_DOWNLOAD_HOST",
			"USER 65532:65532",
		} {
			if !strings.Contains(dockerfile, required) {
				t.Errorf("%s missing %q", rel, required)
			}
		}
		if regexp.MustCompile(`APEXLOAD_BROWSER_EXECUTABLE=`).MatchString(dockerfile) || regexp.MustCompile(`install[^\n]*\bchromium\b[^\n]*\n[^\n]*google-chrome`).MatchString(dockerfile) ||
			regexp.MustCompile(`apt-get install[^\n]*\bchromium\b`).MatchString(dockerfile) {
			t.Errorf("%s still drives a distribution Chromium", rel)
		}
	}
	generator := read("Dockerfile.browser-generator")
	for _, required := range []string{"node:22.22.0-bookworm-slim@sha256:", "golang:1.26.5-bookworm@sha256:", "CGO_ENABLED=0", "./cmd/generator", `test "${GO_BUILD_TAGS}" = standard`} {
		if !strings.Contains(generator, required) {
			t.Errorf("Dockerfile.browser-generator missing %q", required)
		}
	}

	ci := read(".github/workflows/ci.yml")
	if !strings.Contains(ci, "dockerfile: Dockerfile.browser-generator") || !strings.Contains(ci, `scripts/ci/browser-generator-smoke.sh "${IMAGE}"`) {
		t.Error("CI must build and smoke-test the browser generator image")
	}
	smoke := read("scripts/ci/browser-generator-smoke.sh")
	for _, required := range []string{"--read-only", "--cap-drop ALL", "--user 65532:65532", "browsers.json", "chromium.launch"} {
		if !strings.Contains(smoke, required) {
			t.Errorf("browser generator smoke missing %q", required)
		}
	}
	release := read(".github/workflows/release.yml")
	if !strings.Contains(release, "- component: browser-generator\n            profile: standard\n            dockerfile: Dockerfile.browser-generator\n            binary: generator") {
		t.Error("release must build the standard browser generator image")
	}
	for _, rel := range []string{".github/workflows/release.yml", ".github/workflows/publish-onprem.yml"} {
		if strings.Count(read(rel), "java-vuser-runner:standard browser-generator:standard; do") != 2 {
			t.Errorf("%s must verify and promote the browser generator image", rel)
		}
	}
	if !strings.Contains(read(".github/workflows/publish-onprem.yml"), "- component: browser-generator\n            profile: standard") {
		t.Error("on-prem publishing must stage the browser generator image")
	}
	if !strings.Contains(read("scripts/release/create-manifest.py"), `("browser-generator", "standard")`) {
		t.Error("the release manifest must require the browser generator image")
	}
	verify := read("scripts/release/verify-release.sh")
	if !strings.Contains(verify, "(.images | length) <= 6") || !strings.Contains(verify, `[["browser-generator", "standard"], ["controller", "enterprise"]`) {
		t.Error("release verification must accept the six-image release with the browser generator")
	}
}

// Image evidence verification knows the browser generator, standard only.
func TestVerifyImageEvidenceKnowsTheBrowserGenerator(t *testing.T) {
	dir := t.TempDir()
	manifest, key := filepath.Join(dir, "release-manifest.json"), filepath.Join(dir, "cosign.pub")
	for _, path := range []string{manifest, key} {
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(profile string) string {
		output, _ := exec.Command("bash", "release/verify-image-evidence.sh", manifest, "browser-generator", profile, key).CombinedOutput()
		return string(output)
	}
	if got := run("enterprise"); !strings.Contains(got, "invalid browser generator profile") {
		t.Fatalf("enterprise browser generator = %q", got)
	}
	if got := run("standard"); strings.Contains(got, "invalid component") || strings.Contains(got, "profile") {
		t.Fatalf("standard browser generator was refused as a component: %q", got)
	}
}
