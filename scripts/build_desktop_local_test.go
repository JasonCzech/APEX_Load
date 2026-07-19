package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func writeFakeMakensis(t *testing.T, toolDir string) {
	t.Helper()
	writeExecutable(t, filepath.Join(toolDir, "makensis"), `#!/bin/sh
if [ "${FAKE_REQUIRE_MSYS_EXCLUSION:-0}" = "1" ] && [ "${MSYS2_ARG_CONV_EXCL:-}" != "*" ]; then
  echo "makensis slash switch was not protected from MSYS argument conversion" >&2
  exit 86
fi
if [ "${1:-}" = "/VERSION" ]; then
  printf '%s\n' "${FAKE_NSIS_VERSION:-v3.10}"
fi
`)
}

// TestBuildDesktopLocalScript guards the local host-platform build helper: it
// must document the cross-compile limitation honestly and preflight the desktop
// toolchain (wails CLI, node/npm, and the Linux gtk/webkit2gtk dev headers).
func TestBuildDesktopLocalScript(t *testing.T) {
	content, err := os.ReadFile("../scripts/build-desktop-local.sh")
	if err != nil {
		t.Fatalf("read build-desktop-local.sh: %v", err)
	}
	script := string(content)
	checks := []string{
		"set -euo pipefail",
		"CANNOT be cross-compiled",
		"--check",
		"--install-wails",
		"command -v wails",
		"command -v node",
		"node --version",
		"Node.js ^20.19.0 or >=22.12.0",
		"command -v npm",
		"command -v makensis",
		`NSIS_VERSION="3.10"`,
		`MSYS2_ARG_CONV_EXCL='*' makensis /VERSION`,
		"pkg-config --exists gtk+-3.0",
		"webkit2gtk-4.1",
		`WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS//[[:space:]]/,}"`,
		`WAILS_BUILD_TAGS="${WAILS_BUILD_TAGS},webkit2_41"`,
		`"${installed_wails}" != "${WAILS_VERSION}"`,
		"does not match host OS",
		"differs from pinned",
		"go install github.com/wailsapp/wails/v2/cmd/wails@",
		"scripts/resolve-version.sh",
		"-trimpath",
		"WAILS_CONFIG_LOCK",
		"cmp -s",
		"go env GOBIN",
		"npm ci --ignore-scripts",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("build-desktop-local.sh missing %q", check)
		}
	}
	if strings.Contains(script, "webkit2gtk-4.0") {
		t.Fatal("desktop build helper accepts WebKitGTK 4.0 even though the Linux package targets the 4.1 ABI")
	}
	if strings.Contains(script, "APEXLOAD_WAILS_VERSION") {
		t.Fatal("desktop build helper permits bypassing the shared Wails version pin")
	}
}

func TestBuildDesktopLocalPreservesConcurrentWailsConfigEdit(t *testing.T) {
	tempRoot := t.TempDir()
	scriptsDir := filepath.Join(tempRoot, "scripts")
	desktopDir := filepath.Join(tempRoot, "desktop")
	dashboardDir := filepath.Join(tempRoot, "dashboard")
	toolDir := filepath.Join(tempRoot, "tools")
	for _, directory := range []string{scriptsDir, desktopDir, dashboardDir, toolDir} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"build-desktop-local.sh", "stamp-desktop-version.mjs"} {
		contents, err := os.ReadFile(filepath.Join("..", "scripts", name))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if strings.HasSuffix(name, ".sh") {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(scriptsDir, name), contents, mode); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(desktopDir, ".wails-version"), []byte("v2.13.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	configPath := filepath.Join(desktopDir, "wails.json")
	original := `{"name":"apexload","info":{"productVersion":"0.1.0"}}` + "\n"
	newerEdit := `{"name":"user-edit-during-build","info":{"productVersion":"9.9.9"}}` + "\n"
	if err := os.WriteFile(configPath, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}

	fakeWails := filepath.Join(toolDir, "wails")
	writeExecutable(t, fakeWails, `#!/bin/sh
if [ "${1:-}" = "version" ]; then
  printf '%s\n' v2.13.0
  exit 0
fi
printf '%s\n' "$WAILS_CONCURRENT_EDIT" > "$WAILS_TEST_CONFIG"
`)
	for _, tool := range []string{"pkg-config"} {
		writeExecutable(t, filepath.Join(toolDir, tool), "#!/bin/sh\nexit 0\n")
	}
	writeFakeMakensis(t, toolDir)
	npmCallPath := filepath.Join(tempRoot, "npm-call.txt")
	writeExecutable(t, filepath.Join(toolDir, "npm"), `#!/bin/sh
printf '%s\n' "$*" > "$NPM_CALL_FILE"
`)
	hostOSRaw, err := exec.Command("go", "env", "GOOS").Output()
	if err != nil {
		t.Fatal(err)
	}
	hostArchRaw, err := exec.Command("go", "env", "GOARCH").Output()
	if err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", filepath.Join(scriptsDir, "build-desktop-local.sh"))
	cmd.Env = append(os.Environ(),
		"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VERSION=2.0.0",
		"COMMIT=test",
		"BUILD_TIME=2026-07-18T00:00:00Z",
		"APEXLOAD_WAILS_BIN="+fakeWails,
		"WAILS_PLATFORM="+strings.TrimSpace(string(hostOSRaw))+"/"+strings.TrimSpace(string(hostArchRaw)),
		"WAILS_TEST_CONFIG="+configPath,
		"WAILS_CONCURRENT_EDIT="+strings.TrimSpace(newerEdit),
		"NPM_CALL_FILE="+npmCallPath,
	)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("build with a concurrent wails.json edit unexpectedly succeeded:\n%s", output)
	}
	if !strings.Contains(string(output), "changed during the Wails build") {
		t.Fatalf("concurrent config edit failure was not actionable:\n%s", output)
	}
	current, readErr := os.ReadFile(configPath)
	if readErr != nil {
		t.Fatal(readErr)
	}
	if string(current) != newerEdit {
		t.Fatalf("concurrent user edit was overwritten:\ngot  %q\nwant %q", current, newerEdit)
	}
	info, statErr := os.Stat(configPath)
	if statErr != nil {
		t.Fatal(statErr)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("wails config mode changed to %o, want 0644", info.Mode().Perm())
	}
	if _, lockErr := os.Stat(configPath + ".apexload-build.lock"); !os.IsNotExist(lockErr) {
		t.Fatalf("desktop build lock was not cleaned up: %v", lockErr)
	}
	npmCall, readErr := os.ReadFile(npmCallPath)
	if readErr != nil {
		t.Fatalf("immutable frontend install was not invoked: %v", readErr)
	}
	if strings.TrimSpace(string(npmCall)) != "ci --ignore-scripts" {
		t.Fatalf("frontend install = %q, want immutable npm ci", npmCall)
	}
}

func TestBuildDesktopLocalFindsWailsInCustomGoBin(t *testing.T) {
	toolDir := t.TempDir()
	goBinDir := t.TempDir()
	writeExecutable(t, filepath.Join(goBinDir, "wails"), `#!/bin/sh
if [ "${1:-}" = "version" ]; then printf '%s\n' v2.13.0; fi
`)
	writeExecutable(t, filepath.Join(toolDir, "go"), `#!/bin/sh
if [ "${1:-}" != "env" ]; then exit 2; fi
case "${2:-}" in
  GOOS) printf '%s\n' "$FAKE_GOOS" ;;
  GOARCH) printf '%s\n' "$FAKE_GOARCH" ;;
  GOBIN) printf '%s\n' "$FAKE_GOBIN" ;;
  GOPATH) printf '%s\n' "$FAKE_GOPATH" ;;
  *) exit 2 ;;
esac
`)
	writeExecutable(t, filepath.Join(toolDir, "node"), "#!/bin/sh\nprintf '%s\\n' v22.12.0\n")
	for _, tool := range []string{"npm", "pkg-config"} {
		writeExecutable(t, filepath.Join(toolDir, tool), "#!/bin/sh\nexit 0\n")
	}
	writeFakeMakensis(t, toolDir)

	path := toolDir + string(os.PathListSeparator) + "/usr/bin:/bin"
	cmd := exec.Command("/bin/bash", "../scripts/build-desktop-local.sh", "--check")
	cmd.Env = append(os.Environ(),
		"PATH="+path,
		"FAKE_GOOS="+runtime.GOOS,
		"FAKE_GOARCH="+runtime.GOARCH,
		"FAKE_GOBIN="+goBinDir,
		"FAKE_GOPATH="+filepath.Join(t.TempDir(), "unused-gopath"),
		"WAILS_PLATFORM="+runtime.GOOS+"/"+runtime.GOARCH,
		"APEXLOAD_WAILS_BIN=",
		"WAILS=",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("custom GOBIN Wails was not discovered: %v\n%s", err, output)
	}
}

func TestBuildDesktopLocalInstallWailsRepairsMismatchedCLI(t *testing.T) {
	toolDir := t.TempDir()
	goBinDir := t.TempDir()
	installMarker := filepath.Join(t.TempDir(), "installed.txt")
	writeExecutable(t, filepath.Join(toolDir, "wails"), `#!/bin/sh
if [ "${1:-}" = "version" ]; then printf '%s\n' v2.12.0; fi
`)
	writeExecutable(t, filepath.Join(toolDir, "go"), `#!/bin/sh
set -eu
if [ "${1:-}" = "install" ]; then
  cat > "$FAKE_GOBIN/wails" <<'WAILS'
#!/bin/sh
if [ "${1:-}" = "version" ]; then printf '%s\n' v2.13.0; fi
WAILS
  chmod 0755 "$FAKE_GOBIN/wails"
  printf '%s\n' "$2" > "$FAKE_INSTALL_MARKER"
  exit 0
fi
if [ "${1:-}" != "env" ]; then exit 2; fi
case "${2:-}" in
  GOOS) printf '%s\n' "$FAKE_GOOS" ;;
  GOARCH) printf '%s\n' "$FAKE_GOARCH" ;;
  GOBIN) printf '%s\n' "$FAKE_GOBIN" ;;
  GOPATH) printf '%s\n' "$FAKE_GOPATH" ;;
  *) exit 2 ;;
esac
`)
	writeExecutable(t, filepath.Join(toolDir, "node"), "#!/bin/sh\nprintf '%s\\n' v22.12.0\n")
	for _, tool := range []string{"npm", "pkg-config"} {
		writeExecutable(t, filepath.Join(toolDir, tool), "#!/bin/sh\nexit 0\n")
	}
	writeFakeMakensis(t, toolDir)

	cmd := exec.Command("/bin/bash", "../scripts/build-desktop-local.sh", "--install-wails", "--check")
	cmd.Env = append(os.Environ(),
		"PATH="+toolDir+string(os.PathListSeparator)+"/usr/bin:/bin",
		"FAKE_GOOS="+runtime.GOOS,
		"FAKE_GOARCH="+runtime.GOARCH,
		"FAKE_GOBIN="+goBinDir,
		"FAKE_GOPATH="+filepath.Join(t.TempDir(), "unused-gopath"),
		"FAKE_INSTALL_MARKER="+installMarker,
		"WAILS_PLATFORM="+runtime.GOOS+"/"+runtime.GOARCH,
		"APEXLOAD_WAILS_BIN=",
		"WAILS=",
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("--install-wails did not repair mismatched CLI: %v\n%s", err, output)
	}
	installed, err := os.ReadFile(installMarker)
	if err != nil {
		t.Fatalf("fake go install was not called: %v", err)
	}
	if got, want := strings.TrimSpace(string(installed)), "github.com/wailsapp/wails/v2/cmd/wails@v2.13.0"; got != want {
		t.Fatalf("installed module = %q, want %q", got, want)
	}
}

func TestBuildDesktopLocalEnforcesViteNodeRange(t *testing.T) {
	hostOSRaw, err := exec.Command("go", "env", "GOOS").Output()
	if err != nil {
		t.Fatal(err)
	}
	hostArchRaw, err := exec.Command("go", "env", "GOARCH").Output()
	if err != nil {
		t.Fatal(err)
	}
	hostPlatform := strings.TrimSpace(string(hostOSRaw)) + "/" + strings.TrimSpace(string(hostArchRaw))
	toolDir := t.TempDir()
	writeExecutable(t, filepath.Join(toolDir, "node"), `#!/bin/sh
if [ "${1:-}" = "--version" ]; then
  printf '%s\n' "${FAKE_NODE_VERSION:-}"
fi
`)
	writeExecutable(t, filepath.Join(toolDir, "npm"), "#!/bin/sh\nexit 0\n")
	writeExecutable(t, filepath.Join(toolDir, "pkg-config"), "#!/bin/sh\nexit 0\n")
	writeFakeMakensis(t, toolDir)
	fakeWails := filepath.Join(toolDir, "wails")
	writeExecutable(t, fakeWails, `#!/bin/sh
if [ "${1:-}" = "version" ]; then
  printf '%s\n' v2.13.0
fi
`)

	tests := []struct {
		version string
		wantOK  bool
	}{
		{version: "v18.20.8"},
		{version: "v20.18.9"},
		{version: "v21.7.3"},
		{version: "v22.11.0"},
		{version: "not-a-version"},
		{version: "v20.19.0", wantOK: true},
		{version: "v20.20.1", wantOK: true},
		{version: "v22.12.0", wantOK: true},
		{version: "v23.0.0", wantOK: true},
		{version: "v24.0.0", wantOK: true},
	}
	for _, test := range tests {
		t.Run(strings.NewReplacer(".", "_", "-", "_").Replace(test.version), func(t *testing.T) {
			cmd := exec.Command("bash", "../scripts/build-desktop-local.sh", "--check")
			cmd.Env = append(os.Environ(),
				"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"APEXLOAD_WAILS_BIN="+fakeWails,
				"WAILS_PLATFORM="+hostPlatform,
				"FAKE_NODE_VERSION="+test.version,
			)
			output, runErr := cmd.CombinedOutput()
			if test.wantOK {
				if runErr != nil {
					t.Fatalf("supported Node version %q failed preflight: %v\n%s", test.version, runErr, output)
				}
				return
			}
			if runErr == nil {
				t.Fatalf("unsupported Node version %q passed preflight:\n%s", test.version, output)
			}
			if !strings.Contains(string(output), "Vite requires Node.js ^20.19.0 or >=22.12.0") {
				t.Fatalf("Node version rejection was not actionable:\n%s", output)
			}
		})
	}
}

func TestBuildDesktopLocalRequiresExactNSISVersion(t *testing.T) {
	toolDir := t.TempDir()
	writeExecutable(t, filepath.Join(toolDir, "go"), `#!/bin/sh
if [ "${1:-}" != "env" ]; then exit 2; fi
case "${2:-}" in
  GOOS) printf '%s\n' windows ;;
  GOARCH) printf '%s\n' amd64 ;;
  GOBIN) printf '%s\n' "$FAKE_GOBIN" ;;
  GOPATH) printf '%s\n' "$FAKE_GOPATH" ;;
  *) exit 2 ;;
esac
`)
	writeExecutable(t, filepath.Join(toolDir, "node"), "#!/bin/sh\nprintf '%s\\n' v22.12.0\n")
	writeExecutable(t, filepath.Join(toolDir, "npm"), "#!/bin/sh\nexit 0\n")
	fakeWails := filepath.Join(toolDir, "wails")
	writeExecutable(t, fakeWails, `#!/bin/sh
if [ "${1:-}" = "version" ]; then printf '%s\n' v2.13.0; fi
`)
	writeFakeMakensis(t, toolDir)

	tests := []struct {
		name    string
		version string
		wantOK  bool
	}{
		{name: "pinned", version: "v3.10", wantOK: true},
		{name: "older", version: "v3.09"},
		{name: "unparseable", version: "NSIS 3.10"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd := exec.Command("bash", "../scripts/build-desktop-local.sh", "--check")
			cmd.Env = append(os.Environ(),
				"PATH="+toolDir+string(os.PathListSeparator)+"/usr/bin:/bin",
				"APEXLOAD_WAILS_BIN="+fakeWails,
				"WAILS_PLATFORM=windows/amd64",
				"FAKE_GOBIN="+filepath.Join(t.TempDir(), "go-bin"),
				"FAKE_GOPATH="+filepath.Join(t.TempDir(), "go-path"),
				"FAKE_NSIS_VERSION="+test.version,
				"FAKE_REQUIRE_MSYS_EXCLUSION=1",
			)
			output, err := cmd.CombinedOutput()
			if test.wantOK {
				if err != nil {
					t.Fatalf("pinned NSIS failed preflight: %v\n%s", err, output)
				}
				return
			}
			if err == nil {
				t.Fatalf("NSIS %q unexpectedly passed preflight:\n%s", test.version, output)
			}
			if !strings.Contains(string(output), "differs from pinned v3.10") {
				t.Fatalf("NSIS mismatch was not actionable:\n%s", output)
			}
		})
	}
}

func TestBuildDesktopLocalRejectsCrossOSPlatform(t *testing.T) {
	hostRaw, err := exec.Command("go", "env", "GOOS").Output()
	if err != nil {
		t.Fatal(err)
	}
	host := strings.TrimSpace(string(hostRaw))
	target := "linux/amd64"
	if host == "linux" {
		target = "windows/amd64"
	}
	cmd := exec.Command("bash", "../scripts/build-desktop-local.sh", "--check")
	cmd.Env = append(os.Environ(), "WAILS_PLATFORM="+target)
	output, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("cross-OS Wails target unexpectedly passed:\n%s", output)
	}
	if !strings.Contains(string(output), "does not match host OS") {
		t.Fatalf("cross-OS rejection was not actionable:\n%s", output)
	}
}

func TestBuildDesktopLocalRequiresExactWailsVersion(t *testing.T) {
	hostOSRaw, err := exec.Command("go", "env", "GOOS").Output()
	if err != nil {
		t.Fatal(err)
	}
	hostArchRaw, err := exec.Command("go", "env", "GOARCH").Output()
	if err != nil {
		t.Fatal(err)
	}
	hostPlatform := strings.TrimSpace(string(hostOSRaw)) + "/" + strings.TrimSpace(string(hostArchRaw))
	toolDir := t.TempDir()
	for _, tool := range []string{"pkg-config"} {
		path := filepath.Join(toolDir, tool)
		if err := os.WriteFile(path, []byte("#!/usr/bin/env bash\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	writeFakeMakensis(t, toolDir)

	for _, reported := range []string{"v12.13.0", "v2.13.0-dev", ""} {
		t.Run("reported_"+strings.ReplaceAll(reported, ".", "_"), func(t *testing.T) {
			fakeWails := filepath.Join(toolDir, "wails-"+strings.NewReplacer(".", "_", "-", "_").Replace(reported))
			if reported == "" {
				fakeWails += "empty"
			}
			script := "#!/usr/bin/env bash\nif [[ ${1:-} == version ]]; then printf '%s\\n' '" + reported + "'; fi\n"
			if err := os.WriteFile(fakeWails, []byte(script), 0o755); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command("bash", "../scripts/build-desktop-local.sh", "--check")
			cmd.Env = append(os.Environ(),
				"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"WAILS="+fakeWails,
				"WAILS_PLATFORM="+hostPlatform,
			)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("non-exact Wails version %q unexpectedly passed:\n%s", reported, output)
			}
			if !strings.Contains(string(output), "differs from pinned v2.13.0") {
				t.Fatalf("Wails mismatch was not actionable:\n%s", output)
			}
		})
	}
}

// TestBuildDesktopLocalScriptIsExecutable ensures the helper the Makefile invokes
// is marked executable.
func TestBuildDesktopLocalScriptIsExecutable(t *testing.T) {
	info, err := os.Stat("../scripts/build-desktop-local.sh")
	if err != nil {
		t.Fatalf("stat build-desktop-local.sh: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Fatal("build-desktop-local.sh should be executable")
	}
}
