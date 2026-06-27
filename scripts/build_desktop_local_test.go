package scripts_test

import (
	"os"
	"strings"
	"testing"
)

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
		"command -v npm",
		"pkg-config --exists gtk+-3.0",
		"webkit2gtk-4.0",
		"webkit2gtk-4.1",
		"differs from the pinned",
		"go install github.com/wailsapp/wails/v2/cmd/wails@",
		"scripts/resolve-version.sh",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("build-desktop-local.sh missing %q", check)
		}
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
