package scripts_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type fakeLinuxDesktopPackageResult struct {
	controlPath string
	distDir     string
	output      string
	err         error
}

type fakeLinuxDesktopPackageOptions struct {
	signChecksums bool
	relativeDist  bool
	setup         func(sourceDir, distDir string)
}

func runFakeLinuxDesktopPackage(t *testing.T, shlibdepsOutput string, signChecksums bool) fakeLinuxDesktopPackageResult {
	t.Helper()
	return runFakeLinuxDesktopPackageWithOptions(t, shlibdepsOutput, fakeLinuxDesktopPackageOptions{
		signChecksums: signChecksums,
	})
}

func runFakeLinuxDesktopPackageWithOptions(t *testing.T, shlibdepsOutput string, options fakeLinuxDesktopPackageOptions) fakeLinuxDesktopPackageResult {
	t.Helper()
	tempDir := t.TempDir()
	sourceDir := filepath.Join(tempDir, "build")
	distDir := filepath.Join(tempDir, "dist")
	toolDir := filepath.Join(tempDir, "tools")
	controlPath := filepath.Join(tempDir, "control")
	for _, dir := range []string{sourceDir, toolDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "apexload"), []byte("ELF fixture"), 0o755); err != nil {
		t.Fatal(err)
	}

	fakeTools := map[string]string{
		"file": `#!/bin/sh
printf '%s\n' 'ELF 64-bit LSB pie executable, x86-64'
`,
		"dpkg-shlibdeps": `#!/bin/sh
set -eu
if [ "$#" -ne 2 ] || [ "$1" != "-O" ] || [ ! -f "$2" ]; then
  echo "unexpected dpkg-shlibdeps invocation: $*" >&2
  exit 91
fi
if [ ! -f debian/control ]; then
  echo "dependency analysis did not provide debian/control" >&2
  exit 92
fi
case "$2" in
  */usr/bin/apexload) ;;
  *) echo "dependency analysis did not inspect the staged package binary: $2" >&2; exit 93 ;;
esac
printf '%s' "$FAKE_SHLIBDEPS_OUTPUT"
`,
		"dpkg-deb": `#!/bin/sh
set -eu
case "${1:-}" in
  --root-owner-group)
    [ "${2:-}" = "--build" ]
    cp "$3/DEBIAN/control" "$FAKE_CONTROL_CAPTURE"
    : > "$4"
    ;;
  --info)
    [ -f "$2" ]
    ;;
  *)
    echo "unexpected dpkg-deb invocation: $*" >&2
    exit 94
    ;;
esac
`,
	}
	if options.signChecksums {
		fakeTools["gpg"] = `#!/bin/sh
set -eu
manifest=""
for arg in "$@"; do manifest="$arg"; done
[ -f "$manifest" ]
printf '%s\n' 'fake detached signature' > "${manifest}.asc"
`
	}
	for name, body := range fakeTools {
		if err := os.WriteFile(filepath.Join(toolDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if options.setup != nil {
		options.setup(sourceDir, distDir)
	}

	cmd := exec.Command("bash", "package-desktop.sh")
	distEnv := distDir
	if options.relativeDist {
		workingDir, err := os.Getwd()
		if err != nil {
			t.Fatal(err)
		}
		distEnv, err = filepath.Rel(workingDir, distDir)
		if err != nil {
			t.Fatal(err)
		}
	}
	cmd.Env = append(os.Environ(),
		"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VERSION=1.2.3",
		"APEXLOAD_DESKTOP_PACKAGE_PLATFORM=linux/amd64",
		"SOURCE_DIR="+sourceDir,
		"DIST_DIR="+distEnv,
		"FAKE_SHLIBDEPS_OUTPUT="+shlibdepsOutput,
		"FAKE_CONTROL_CAPTURE="+controlPath,
	)
	if options.signChecksums {
		cmd.Env = append(cmd.Env, "APEXLOAD_CHECKSUM_SIGN=1")
	}
	output, err := cmd.CombinedOutput()
	return fakeLinuxDesktopPackageResult{
		controlPath: controlPath,
		distDir:     distDir,
		output:      string(output),
		err:         err,
	}
}

func TestDesktopPackageScriptDocumentsMacOSTrustPath(t *testing.T) {
	content, err := os.ReadFile("package-desktop.sh")
	if err != nil {
		t.Fatalf("read desktop package script: %v", err)
	}
	script := string(content)
	checks := []string{
		"APEXLOAD_MACOS_CODESIGN_IDENTITY",
		"APEXLOAD_DESKTOP_PACKAGE_PLATFORM",
		"APEXLOAD_MACOS_NOTARIZE",
		"APEXLOAD_MACOS_NOTARY_PROFILE",
		"APEXLOAD_MACOS_NOTARY_KEY_PATH",
		"APEXLOAD_MACOS_NOTARY_KEY_ID",
		"APEXLOAD_MACOS_NOTARY_ISSUER_ID",
		"codesign --force --deep --options runtime --timestamp",
		"notarytool submit",
		"stapler staple",
		"spctl -a -vvv -t exec",
		"Apple could not verify",
		"xattr -dr com.apple.quarantine",
		"APEXLOAD_WINDOWS_CODESIGN",
		"APEXLOAD_WINDOWS_CODESIGN_CERT_PATH",
		"signtool",
		"MSYS2_ARG_CONV_EXCL='*'",
		"cygpath -w",
		"osslsigncode",
		"Windows SmartScreen",
		"validate_desktop_artifacts",
		"APEXLOAD_CHECKSUM_SIGN",
		"APEXLOAD_REQUIRE_TRUSTED_PACKAGES",
		"--armor --detach-sign",
		"sha256sum",
		"clean_desktop_dist",
		"mktemp -d",
		"Existing downloadable artifacts are",
		"find \"$DIST_DIR\" -mindepth 1",
		`! -name "apexload-desktop-${VERSION}-*"`,
		"validate_binary_arch",
		"build_linux_deb",
		"dpkg-deb --root-owner-group --build",
		`dpkg-shlibdeps -O "$binary"`,
		"dpkg-shlibdeps (provided by dpkg-dev) is required",
		"normalize_linux_t64_dependencies",
		"shared-mime-info, desktop-file-utils, xdg-utils",
		"usr/share/applications/apexload.desktop",
		"usr/share/mime/packages/apexload-har.xml",
		"usr/share/pixmaps/apexload.png",
		"desktop-file-utils",
		`desktop_packages=(apexload-desktop-"${VERSION}"-*.tar.gz apexload-desktop-"${VERSION}"-*.deb)`,
		`rm -f -- "$DIST_DIR/SHA256SUMS.txt.asc"`,
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("desktop package script missing %q", check)
		}
	}
	if strings.Contains(script, "libwebkit2gtk-4.0-37") {
		t.Fatal("4.1-linked Linux desktop package must not declare WebKitGTK 4.0 as a compatible runtime alternative")
	}
	if strings.Contains(script, "Depends: libgtk") {
		t.Fatal("Linux ELF runtime dependencies must be derived instead of hard-coded")
	}
}

func TestLinuxDesktopPackageDerivesCanonicalRuntimeDependencies(t *testing.T) {
	result := runFakeLinuxDesktopPackage(t, "shlibs:Depends=libc6 (>= 2.34), libglib2.0-0t64 (>= 2.72.0), libgtk-3-0t64, libwebkit2gtk-4.1-0 (>= 2.40.0)\n", false)
	if result.err != nil {
		t.Fatalf("package with derived dependencies: %v\n%s", result.err, result.output)
	}
	control, err := os.ReadFile(result.controlPath)
	if err != nil {
		t.Fatalf("read captured Debian control: %v", err)
	}
	want := `Package: apexload-desktop
Version: 1.2.3
Section: devel
Priority: optional
Architecture: amd64
Maintainer: Project Stormrunner
Depends: libc6 (>= 2.34), libglib2.0-0t64 (>= 2.72.0) | libglib2.0-0 (>= 2.72.0), libgtk-3-0t64 | libgtk-3-0, libwebkit2gtk-4.1-0 (>= 2.40.0), shared-mime-info, desktop-file-utils, xdg-utils
Description: APEX Load desktop performance testing client
 Kubernetes-native performance testing authoring and local execution client.
`
	if string(control) != want {
		t.Fatalf("Debian control was not canonical:\n--- got ---\n%s--- want ---\n%s", control, want)
	}
}

func TestLinuxDesktopPackageRejectsInvalidShlibdepsOutput(t *testing.T) {
	tests := []struct {
		name   string
		output string
		want   string
	}{
		{name: "empty", output: "", want: "exactly one non-empty 'shlibs:Depends=' line"},
		{name: "empty dependency set", output: "shlibs:Depends=\n", want: "empty Linux desktop dependency set"},
		{name: "whitespace dependency set", output: "shlibs:Depends=   \n", want: "empty Linux desktop dependency set"},
		{name: "multiple lines", output: "shlibs:Depends=libc6 (>= 2.34)\nunexpected=true\n", want: "got 2 line(s)"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result := runFakeLinuxDesktopPackage(t, test.output, false)
			if result.err == nil {
				t.Fatalf("invalid dpkg-shlibdeps output unexpectedly packaged:\n%s", result.output)
			}
			if !strings.Contains(result.output, test.want) {
				t.Fatalf("rejection missing %q:\n%s", test.want, result.output)
			}
		})
	}
}

func TestDesktopPackagePublishesFakeChecksumSignatureOnce(t *testing.T) {
	result := runFakeLinuxDesktopPackage(t, "shlibs:Depends=libc6 (>= 2.34)\n", true)
	if result.err != nil {
		t.Fatalf("signed desktop package failed: %v\n%s", result.err, result.output)
	}
	signature, err := os.ReadFile(filepath.Join(result.distDir, "SHA256SUMS.txt.asc"))
	if err != nil {
		t.Fatalf("published detached checksum signature: %v", err)
	}
	if string(signature) != "fake detached signature\n" {
		t.Fatalf("published detached checksum signature = %q", signature)
	}
}

func TestDesktopPackageVerifiesRetainedPlatformBeforeReissuingManifest(t *testing.T) {
	const retainedName = "apexload-desktop-1.2.3-darwin-amd64.tar.gz"
	retainedBytes := []byte("known prior platform package")
	retainedHash := sha256.Sum256(retainedBytes)
	result := runFakeLinuxDesktopPackageWithOptions(t, "shlibs:Depends=libc6 (>= 2.34)\n", fakeLinuxDesktopPackageOptions{
		setup: func(_ string, distDir string) {
			if err := os.MkdirAll(distDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(distDir, retainedName), retainedBytes, 0o644); err != nil {
				t.Fatal(err)
			}
			manifest := fmt.Sprintf("%x  %s\n", retainedHash, retainedName)
			if err := os.WriteFile(filepath.Join(distDir, "SHA256SUMS.txt"), []byte(manifest), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	})
	if result.err != nil {
		t.Fatalf("package with verified retained platform failed: %v\n%s", result.err, result.output)
	}
	manifest, err := os.ReadFile(filepath.Join(result.distDir, "SHA256SUMS.txt"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		retainedName,
		"apexload-desktop-1.2.3-linux-amd64.tar.gz",
		"apexload-desktop-1.2.3-linux-amd64.deb",
	} {
		if !strings.Contains(string(manifest), "  "+name+"\n") {
			t.Errorf("reissued checksum manifest does not cover %s:\n%s", name, manifest)
		}
	}
}

func TestDesktopPackageRejectsChangedRetainedPlatform(t *testing.T) {
	const retainedName = "apexload-desktop-1.2.3-darwin-arm64.tar.gz"
	priorBytes := []byte("prior verified bytes")
	changedBytes := []byte("changed after manifest")
	priorHash := sha256.Sum256(priorBytes)
	priorManifest := fmt.Sprintf("%x  %s\n", priorHash, retainedName)
	result := runFakeLinuxDesktopPackageWithOptions(t, "shlibs:Depends=libc6 (>= 2.34)\n", fakeLinuxDesktopPackageOptions{
		setup: func(_ string, distDir string) {
			if err := os.MkdirAll(distDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(distDir, retainedName), changedBytes, 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(distDir, "SHA256SUMS.txt"), []byte(priorManifest), 0o644); err != nil {
				t.Fatal(err)
			}
		},
	})
	if result.err == nil {
		t.Fatalf("changed retained package was re-manifested:\n%s", result.output)
	}
	if !strings.Contains(result.output, "checksum does not match its prior manifest") {
		t.Fatalf("checksum rejection was not actionable:\n%s", result.output)
	}
	retained, err := os.ReadFile(filepath.Join(result.distDir, retainedName))
	if err != nil {
		t.Fatal(err)
	}
	if string(retained) != string(changedBytes) {
		t.Fatalf("rejected retained package was modified: %q", retained)
	}
	manifest, err := os.ReadFile(filepath.Join(result.distDir, "SHA256SUMS.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(manifest) != priorManifest {
		t.Fatalf("prior manifest changed after rejection:\n%s", manifest)
	}
	for _, name := range []string{
		"apexload-desktop-1.2.3-linux-amd64.tar.gz",
		"apexload-desktop-1.2.3-linux-amd64.deb",
	} {
		if _, statErr := os.Stat(filepath.Join(result.distDir, name)); !os.IsNotExist(statErr) {
			t.Errorf("rejected transaction published %s: %v", name, statErr)
		}
	}
}

func TestDesktopPackageSerializesSharedDistTransactions(t *testing.T) {
	result := runFakeLinuxDesktopPackageWithOptions(t, "shlibs:Depends=libc6 (>= 2.34)\n", fakeLinuxDesktopPackageOptions{
		setup: func(_ string, distDir string) {
			lockDir := filepath.Join(distDir, ".apexload-desktop-package.lock")
			if err := os.MkdirAll(lockDir, 0o755); err != nil {
				t.Fatal(err)
			}
		},
	})
	if result.err == nil {
		t.Fatalf("packaging unexpectedly entered an already locked dist directory:\n%s", result.output)
	}
	if !strings.Contains(result.output, "Another desktop packaging process holds") {
		t.Fatalf("lock rejection was not actionable:\n%s", result.output)
	}
}

func TestLinuxDesktopPackageSupportsRelativeDistDirectory(t *testing.T) {
	result := runFakeLinuxDesktopPackageWithOptions(t, "shlibs:Depends=libc6 (>= 2.34)\n", fakeLinuxDesktopPackageOptions{
		relativeDist: true,
	})
	if result.err != nil {
		t.Fatalf("relative DIST_DIR broke Linux dependency analysis: %v\n%s", result.err, result.output)
	}
	if _, err := os.Stat(filepath.Join(result.distDir, "apexload-desktop-1.2.3-linux-amd64.deb")); err != nil {
		t.Fatalf("relative DIST_DIR package output: %v", err)
	}
}

func TestDesktopPackageFailurePreservesExistingReleaseArtifacts(t *testing.T) {
	tempDir := t.TempDir()
	distDir := filepath.Join(tempDir, "dist")
	sourceDir := filepath.Join(tempDir, "invalid-build")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldArtifact := filepath.Join(distDir, "apexload-desktop-1.9.0-linux-amd64.tar.gz")
	if err := os.WriteFile(oldArtifact, []byte("known-good-release"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A text file is intentionally not a valid amd64 Wails executable, causing
	// validation to fail after staging has begun but before the commit point.
	if err := os.WriteFile(filepath.Join(sourceDir, "apexload"), []byte("not-a-native-binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "package-desktop.sh")
	cmd.Env = append(os.Environ(),
		"VERSION=2.0.0",
		"APEXLOAD_DESKTOP_PACKAGE_PLATFORM=linux/amd64",
		"DIST_DIR="+distDir,
		"SOURCE_DIR="+sourceDir,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("invalid desktop build unexpectedly packaged:\n%s", output)
	}
	contents, err := os.ReadFile(oldArtifact)
	if err != nil {
		t.Fatalf("existing release artifact was removed: %v", err)
	}
	if string(contents) != "known-good-release" {
		t.Fatalf("existing release artifact was modified: %q", contents)
	}
	staged, err := filepath.Glob(filepath.Join(distDir, ".apexload-desktop-stage.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 0 {
		t.Fatalf("failed packaging left staging directories: %v", staged)
	}
	if _, err := os.Stat(filepath.Join(distDir, ".apexload-desktop-package.lock")); !os.IsNotExist(err) {
		t.Fatalf("failed packaging left its dist lock behind: %v", err)
	}
}

func TestDesktopPackageRejectsInvalidVersionAndUnsupportedPlatform(t *testing.T) {
	sourceDir := filepath.Join(t.TempDir(), "build")
	if err := os.MkdirAll(sourceDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceDir, "apexload"), []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name     string
		version  string
		platform string
		want     string
	}{
		{name: "noncanonical version", version: "1.2.3junk", platform: "linux/amd64", want: "canonical SemVer"},
		{name: "unsupported platform", version: "1.2.3", platform: "freebsd/amd64", want: "Unsupported desktop package platform"},
		{name: "extra platform segment", version: "1.2.3", platform: "linux/amd64/extra", want: "Unsupported desktop package platform"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("bash", "package-desktop.sh")
			cmd.Env = append(os.Environ(),
				"VERSION="+test.version,
				"APEXLOAD_DESKTOP_PACKAGE_PLATFORM="+test.platform,
				"SOURCE_DIR="+sourceDir,
				"DIST_DIR="+filepath.Join(t.TempDir(), "dist"),
			)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("invalid package inputs unexpectedly succeeded:\n%s", output)
			}
			if !strings.Contains(string(output), test.want) {
				t.Fatalf("rejection was not actionable, want %q:\n%s", test.want, output)
			}
		})
	}
}

func TestDesktopPackageRejectsWrongOSAndUnexpectedArtifacts(t *testing.T) {
	makeFakeFile := func(t *testing.T, description string) string {
		t.Helper()
		toolDir := t.TempDir()
		toolPath := filepath.Join(toolDir, "file")
		script := "#!/bin/sh\nprintf '%s\\n' \"$FAKE_FILE_DESCRIPTION\"\n"
		if err := os.WriteFile(toolPath, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		t.Setenv("FAKE_FILE_DESCRIPTION", description)
		return toolDir
	}
	run := func(t *testing.T, description string, extra bool) string {
		t.Helper()
		sourceDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(sourceDir, "apexload"), []byte("fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
		if extra {
			if err := os.WriteFile(filepath.Join(sourceDir, "stale.txt"), []byte("stale"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		toolDir := makeFakeFile(t, description)
		cmd := exec.Command("bash", "package-desktop.sh")
		cmd.Env = append(os.Environ(),
			"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
			"VERSION=1.2.3",
			"APEXLOAD_DESKTOP_PACKAGE_PLATFORM=linux/amd64",
			"SOURCE_DIR="+sourceDir,
			"DIST_DIR="+filepath.Join(t.TempDir(), "dist"),
		)
		output, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("invalid artifacts unexpectedly packaged:\n%s", output)
		}
		return string(output)
	}

	if output := run(t, "Mach-O 64-bit executable x86_64", false); !strings.Contains(output, "not a Linux ELF binary") {
		t.Fatalf("wrong-OS same-architecture fixture was not rejected:\n%s", output)
	}
	if output := run(t, "ELF 64-bit LSB executable, x86-64", true); !strings.Contains(output, "unexpected artifacts") {
		t.Fatalf("stale build artifact was not rejected:\n%s", output)
	}
}

func TestStampDesktopVersionRequiresCompleteCanonicalSemVer(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "wails.json")
	if err := os.WriteFile(configPath, []byte(`{"info":{"productVersion":"0.0.0"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	valid := exec.Command("node", "stamp-desktop-version.mjs", configPath, "v1.2.3-rc.1+build.7")
	if output, err := valid.CombinedOutput(); err != nil {
		t.Fatalf("valid SemVer stamp failed: %v\n%s", err, output)
	}
	contents, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(contents), `"productVersion": "1.2.3"`) {
		t.Fatalf("numeric desktop product version was not stamped: %s", contents)
	}
	for _, invalid := range []string{"1.2", "1.2.3junk", "1.2.3-01"} {
		cmd := exec.Command("node", "stamp-desktop-version.mjs", configPath, invalid)
		if output, err := cmd.CombinedOutput(); err == nil {
			t.Fatalf("invalid version %q was accepted: %s", invalid, output)
		}
	}
}

func TestDesktopPackagePublishesIntoFreshDistWithSystemBash(t *testing.T) {
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("fresh-dist package regression uses a host-native Unix executable")
	}
	if runtime.GOOS == "linux" {
		if _, err := exec.LookPath("dpkg-deb"); err != nil {
			t.Skip("dpkg-deb is required for Linux desktop packaging")
		}
	}
	tempDir := t.TempDir()
	distDir := filepath.Join(tempDir, "fresh-dist")
	sourceDir := filepath.Join(tempDir, "build")
	if runtime.GOOS == "darwin" {
		binaryDir := filepath.Join(sourceDir, "apexload.app", "Contents", "MacOS")
		if err := os.MkdirAll(binaryDir, 0o755); err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		binaryPath := filepath.Join(binaryDir, "apexload")
		input, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(binaryPath, input, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(binaryPath, 0o755); err != nil {
			t.Fatal(err)
		}
	} else {
		if err := os.MkdirAll(sourceDir, 0o755); err != nil {
			t.Fatal(err)
		}
		executable, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		input, err := os.ReadFile(executable)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(sourceDir, "apexload"), input, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	platform := runtime.GOOS + "/" + runtime.GOARCH
	cmd := exec.Command("/bin/bash", "package-desktop.sh")
	cmd.Env = append(os.Environ(),
		"VERSION=2.0.0",
		"APEXLOAD_DESKTOP_PACKAGE_PLATFORM="+platform,
		"APEXLOAD_MACOS_NOTARIZE=0",
		"DIST_DIR="+distDir,
		"SOURCE_DIR="+sourceDir,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("fresh desktop package failed under system Bash: %v\n%s", err, output)
	}
	for _, name := range []string{
		"apexload-desktop-2.0.0-" + runtime.GOOS + "-" + runtime.GOARCH + ".tar.gz",
		"SHA256SUMS.txt",
	} {
		if _, err := os.Stat(filepath.Join(distDir, name)); err != nil {
			t.Fatalf("fresh package output %s: %v", name, err)
		}
	}
}

func TestSemverToDebianVersionPreservesUpgradeOrdering(t *testing.T) {
	tests := map[string]string{
		"1.2.3":                 "1.2.3",
		"1.2.3-rc.1":            "1.2.3~rc.1",
		"1.2.3-alpha.2+build.7": "1.2.3~alpha.2+build.7",
		"1.2.3+build.7":         "1.2.3+build.7",
	}
	for input, want := range tests {
		output, err := exec.Command("bash", "semver-to-debian-version.sh", input).CombinedOutput()
		if err != nil {
			t.Fatalf("convert %q: %v\n%s", input, err, output)
		}
		if got := strings.TrimSpace(string(output)); got != want {
			t.Errorf("convert %q = %q, want %q", input, got, want)
		}
	}
	for _, invalid := range []string{
		"1.2", "01.2.3", "1.02.3", "1.2.03", "1.2.3-", "1.2.3+",
		"1.2.3-01", "1.2.3-alpha..1", "1.2.3+build..1", "1.2.3junk",
	} {
		if output, err := exec.Command("bash", "semver-to-debian-version.sh", invalid).CombinedOutput(); err == nil {
			t.Fatalf("invalid version %q was accepted: %s", invalid, output)
		}
	}
	if _, err := exec.LookPath("dpkg"); err == nil {
		if output, compareErr := exec.Command("dpkg", "--compare-versions", "1.2.3~rc.1", "lt", "1.2.3").CombinedOutput(); compareErr != nil {
			t.Fatalf("Debian prerelease does not sort before stable: %v\n%s", compareErr, output)
		}
	}
}

func TestDesktopPlatformIntegrationMetadata(t *testing.T) {
	workflow, err := os.ReadFile("../.github/workflows/desktop.yml")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(workflow), `grep -Fq '<glob pattern="*.har" weight="80"/>' /usr/share/mime/packages/apexload-har.xml`) {
		t.Fatal("desktop workflow must verify the installed HAR MIME glob registration")
	}
	if !strings.Contains(string(workflow), `$notificationGuid = [guid]::NewGuid().ToString()`) {
		t.Fatal("desktop workflow must deterministically seed a canonical toast activator when Wails startup has not registered one")
	}

	desktopEntry, err := os.ReadFile("../desktop/build/linux/apexload.desktop")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(desktopEntry), "MimeType=x-scheme-handler/apexload;application/x-apexload-har;") {
		t.Fatal("Linux desktop entry does not register both deep links and HAR files")
	}
	if !strings.Contains(string(desktopEntry), "Categories=Development;") {
		t.Fatal("Linux desktop entry must declare one main freedesktop category")
	}
	mimeDefinition, err := os.ReadFile("../desktop/build/linux/apexload-har.xml")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{`type="application/x-apexload-har"`, `pattern="*.har"`} {
		if !strings.Contains(string(mimeDefinition), required) {
			t.Errorf("Linux MIME definition missing %q", required)
		}
	}

	installer, err := os.ReadFile("../desktop/build/windows/installer/project.nsi")
	if err != nil {
		t.Fatal(err)
	}
	installerText := string(installer)
	for _, required := range []string{
		"!uninstfinalize",
		"sign-windows-binary.sh",
		"APEXLoad.HTTPArchive",
		"APEXLoadBackupCaptured",
		"APEXLoadBackupProgID",
		"apexload_refresh_registration",
		"apexload_preserve_user_association",
		"APEXLOAD_PROTOCOL_KEY",
		"APEXLoadOwnedCommand",
		"APEXLoadOwnedExecutable",
		"APEXLOAD_FILE_CHAIN_KEY",
		"APEXLOAD_PROTOCOL_CHAIN_KEY",
		"apexload_find_previous_file_owner",
		"apexload_protocol_find_previous_owner",
		"apexload_skip_missing_file_owner",
		"apexload_protocol_skip_missing_owner",
		"IntCmpU $6 64",
		"EnumRegKey",
		"EnumRegValue",
		"apexload_protocol_preserve_existing",
		"apexload_protocol_preserve_current",
		"APEXLOAD_INSTALL_METADATA_KEY",
		"APEXLOAD_INSTALL_CURRENT_EXECUTABLE",
		"APEXLOAD_INSTALL_SHORTCUT_OWNER",
		"Function apexload.ensureSingleInstallLocation",
		"apexload.inspectExistingInstall HKLM",
		"apexload.inspectExistingInstall HKCU",
		"apexload.inspectExistingInstall HKCU $R0 user64 1 1",
		"apexload.inspectExistingInstall HKLM $R1 machine64 0 1",
		"apexload.inspectExistingInstall HKLM $R2 machine32 0 0",
		`Software\Classes\APEXLoad.HTTPArchive" "APEXLoadOwnedExecutable`,
		`"${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_OWNED_EXECUTABLE}"`,
		"InstallLocation",
		"SetErrorLevel 68",
		"stale ownership record in a different Windows registry scope",
		"apexload.writeInstallerOwnership",
		"apexload.removeOwnedInstallerState",
		`$\"$INSTDIR\${PRODUCT_EXECUTABLE}$\" $\"%1$\"`,
		"!insertmacro apexload.associateFiles",
		"Function apexload.ensureWebView2Runtime",
		`ExecWait '"$pluginsdir\webview2bootstrapper\MicrosoftEdgeWebview2Setup.exe" /silent /install' $1`,
		"bootstrapper exit code $1",
		"SetErrorLevel 66",
		"Call apexload.ensureSingleInstallLocation",
		"Call apexload.ensureWebView2Runtime",
		"apexload.unassociateNotifications",
		"StrCmp $1 36 apexload_notifications_validate_start",
		"StrCmp $1 38 apexload_notifications_validate_start apexload_notifications_done",
		`DeleteRegKey HKCU "Software\Classes\CLSID\$0"`,
		`DeleteRegKey HKCU "Software\Classes\AppUserModelId\${PRODUCT_EXECUTABLE}"`,
		`Software\Classes\AppUserModelId\${PRODUCT_EXECUTABLE}`,
		`Software\Classes\CLSID\$0\LocalServer32`,
		`$TEMP\${PRODUCT_EXECUTABLE}$0.png`,
		"apexload_uninstall_running",
		"SetErrorLevel 67",
		`Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"`,
		`RMDir "$INSTDIR"`,
	} {
		if !strings.Contains(installerText, required) {
			t.Errorf("reviewed NSIS template missing %q", required)
		}
	}
	if strings.Contains(installerText, "!insertmacro wails.associateFiles") {
		t.Fatal("reviewed NSIS template uses Wails' unquoted association macro")
	}
	if strings.Contains(installerText, "!insertmacro wails.webview2runtime") {
		t.Fatal("reviewed NSIS template uses Wails' unchecked WebView2 bootstrapper macro")
	}
	webViewRegistryKey := `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	if count := strings.Count(installerText, webViewRegistryKey); count != 4 {
		t.Fatalf("reviewed NSIS template checks the WebView2 registry %d times, want machine/user checks before and after bootstrapper", count)
	}
	ensureIndex := strings.Index(installerText, "Call apexload.ensureWebView2Runtime")
	locationGuardIndex := strings.Index(installerText, "Call apexload.ensureSingleInstallLocation")
	filesIndex := strings.Index(installerText, "!insertmacro wails.files")
	if locationGuardIndex < 0 || ensureIndex < 0 || filesIndex < 0 || locationGuardIndex > ensureIndex || ensureIndex > filesIndex {
		t.Fatal("reviewed NSIS template must enforce one install location before WebView2 setup and file copy")
	}
	if strings.Contains(installerText, `!insertmacro APP_ASSOCIATE`) || strings.Contains(installerText, `!insertmacro APP_UNASSOCIATE`) {
		t.Fatal("reviewed NSIS template uses Wails' reinstall-unsafe association macros")
	}
	if strings.Contains(installerText, `!insertmacro CUSTOM_PROTOCOL_ASSOCIATE`) || strings.Contains(installerText, `!insertmacro CUSTOM_PROTOCOL_UNASSOCIATE`) {
		t.Fatal("reviewed NSIS template uses Wails' ownership-destructive protocol macros")
	}
	if strings.Contains(installerText, `!insertmacro wails.deleteUninstaller`) {
		t.Fatal("reviewed NSIS template blindly deletes the shared Add/Remove Programs key")
	}
	if strings.Contains(installerText, "apexload.inspectExistingInstall HKCU $R3 user32") {
		t.Fatal("reviewed NSIS template redundantly scans shared HKCU\\Software through the 32-bit view")
	}
	for _, forbiddenForeignCleanup := range []string{
		"deleteStaleInstallRegistration HKLM $R1 machine64_cleanup",
		"deleteStaleInstallRegistration HKCU $R1 user64_cleanup",
		"deleteStaleInstallRegistration HKLM $R2 machine32_cleanup",
	} {
		if strings.Contains(installerText, forbiddenForeignCleanup) {
			t.Fatalf("reviewed NSIS template attempts unchecked foreign-scope cleanup: %s", forbiddenForeignCleanup)
		}
	}
	uninstallIndex := strings.Index(installerText, `Section "uninstall"`)
	if uninstallIndex < 0 {
		t.Fatal("reviewed NSIS template is missing its uninstall section")
	}
	uninstallSection := installerText[uninstallIndex:]
	uninstallShellContextIndex := strings.Index(uninstallSection, "!insertmacro wails.setShellContext")
	uninstallRegViewIndex := strings.Index(uninstallSection, "SetRegView 64")
	uninstallDeleteIndex := strings.Index(uninstallSection, `Delete "$INSTDIR\${PRODUCT_EXECUTABLE}"`)
	if uninstallShellContextIndex < 0 || uninstallRegViewIndex < 0 || uninstallDeleteIndex < 0 || uninstallShellContextIndex > uninstallRegViewIndex || uninstallRegViewIndex > uninstallDeleteIndex {
		t.Fatal("reviewed NSIS uninstaller must select the 64-bit registry view before ownership checks and cleanup")
	}
	for _, forbidden := range []string{
		`WriteRegStr SHCTX "Software\Classes\APEXLoad.HTTPArchive" "APEXLoadPreviousOwnedExecutable"`,
		`WriteRegStr SHCTX "${APEXLOAD_PROTOCOL_KEY}" "${APEXLOAD_PROTOCOL_PREVIOUS_EXECUTABLE}"`,
	} {
		if strings.Contains(installerText, forbidden) {
			t.Fatalf("reviewed NSIS template still stores side-by-side ownership in one shared predecessor slot: %s", forbidden)
		}
	}
	if strings.Contains(installerText, `RMDir /r $INSTDIR`) || strings.Contains(installerText, `RMDir /r "$INSTDIR"`) {
		t.Fatal("reviewed NSIS template recursively deletes a user-selected install directory")
	}

	signingHook, err := os.ReadFile("sign-windows-binary.sh")
	if err != nil {
		t.Fatal(err)
	}
	for _, required := range []string{"signtool", "osslsigncode", "APEXLOAD_WINDOWS_CODESIGN_CERT_PEM", "verify /pa /all"} {
		if !strings.Contains(string(signingHook), required) {
			t.Errorf("pre-installer signing hook missing %q", required)
		}
	}
}

func TestDesktopPackageNeverPassesWindowsSigningPasswordsInProcessArguments(t *testing.T) {
	for _, path := range []string{"package-desktop.sh", "package-recording-agent.sh"} {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		script := string(content)
		for _, forbidden := range []string{
			`/p "$WINDOWS_CODESIGN_PASSWORD"`,
			`-pass "$WINDOWS_CODESIGN_PASSWORD"`,
			`--password "$MACOS_NOTARY_PASSWORD"`,
		} {
			if strings.Contains(script, forbidden) {
				t.Fatalf("%s exposes a signing password through %q", path, forbidden)
			}
		}
		for _, required := range []string{
			"Import the certificate into the CurrentUser certificate store",
			"password arguments are disabled because they expose credentials in the process list",
			`sign_args+=(/s "$WINDOWS_CODESIGN_STORE" /sha1 "$WINDOWS_CODESIGN_SHA1")`,
			`sign_args+=(/s "$WINDOWS_CODESIGN_STORE" /n "$WINDOWS_CODESIGN_SUBJECT")`,
		} {
			if !strings.Contains(script, required) {
				t.Errorf("%s missing password-safe contract %q", path, required)
			}
		}
	}
}

func TestWindowsSigningHookUsesCustomStoreWithSubjectSelection(t *testing.T) {
	tempDir := t.TempDir()
	toolDir := filepath.Join(tempDir, "fake tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(tempDir, "signtool.log")
	fakeSigntool := filepath.Join(toolDir, "signtool")
	writeExecutable(t, fakeSigntool, `#!/bin/sh
for arg in "$@"; do printf 'ARG=%s\n' "$arg"; done >> "$SIGNTOOL_LOG"
printf 'END\n' >> "$SIGNTOOL_LOG"
`)
	binaryPath := filepath.Join(tempDir, "apexload.exe")
	if err := os.WriteFile(binaryPath, []byte("test binary"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "sign-windows-binary.sh", binaryPath)
	cmd.Env = append(os.Environ(),
		"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"APEXLOAD_WINDOWS_CODESIGN=1",
		"APEXLOAD_WINDOWS_CODESIGN_TOOL=signtool",
		"APEXLOAD_WINDOWS_SIGNTOOL="+fakeSigntool,
		"APEXLOAD_WINDOWS_CODESIGN_SHA1=",
		"APEXLOAD_WINDOWS_CODESIGN_SUBJECT=CN=APEX Load Test",
		"APEXLOAD_WINDOWS_CODESIGN_STORE=TrustedPeople",
		"SIGNTOOL_LOG="+logPath,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subject-selected signing hook failed: %v\n%s", err, output)
	}
	logged, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ARG=/s\nARG=TrustedPeople\nARG=/n\nARG=CN=APEX Load Test\n", "ARG=verify\nARG=/pa\nARG=/all\n"} {
		if !strings.Contains(string(logged), want) {
			t.Fatalf("signtool log missing custom-store subject selection %q:\n%s", want, logged)
		}
	}
}
