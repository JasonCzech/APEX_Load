package scripts_test

import (
	"os"
	"strings"
	"testing"
)

// TestMakefileClientsTarget guards the opt-in combined controller+desktop build
// wiring: the `clients` target (host platform only), the toolchain preflight,
// the WITH_DESKTOP guard, and the release variant — without touching the fast
// default build.
func TestMakefileClientsTarget(t *testing.T) {
	content, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(content)
	checks := []string{
		"clients: desktop\n\t$(MAKE) --no-print-directory controller VERSION=\"$(VERSION)\" COMMIT=\"$(COMMIT)\" BUILD_TIME=\"$(BUILD_TIME)\"",
		"desktop-preflight:",
		"./scripts/build-desktop-local.sh --check",
		"clients-release: desktop-release\n\t$(MAKE) --no-print-directory controller VERSION=\"$(VERSION)\" COMMIT=\"$(COMMIT)\" BUILD_TIME=\"$(BUILD_TIME)\"",
		"ifeq ($(WITH_DESKTOP),1)",
		"build: generator collector-agent desktop-api-binaries",
		"desktop-api-binaries: desktop\n\t$(MAKE) --no-print-directory controller apexctl recording-agent VERSION=\"$(VERSION)\" COMMIT=\"$(COMMIT)\" BUILD_TIME=\"$(BUILD_TIME)\"",
	}
	for _, check := range checks {
		if !strings.Contains(makefile, check) {
			t.Fatalf("Makefile missing %q", check)
		}
	}

	// The default build target must stay pure-Go (no unconditional desktop).
	if !strings.Contains(makefile, "build: generator controller apexctl recording-agent collector-agent") {
		t.Fatal("default build target should remain the pure-Go binary set")
	}
	if strings.Contains(makefile, "echo $(HOME)/go/bin/wails") {
		t.Fatal("Makefile hard-codes HOME/go/bin and bypasses custom GOBIN/GOPATH discovery")
	}

	// Controller compilation embeds pkg/api/static. It must run only after the
	// desktop/Vite build finishes replacing that directory, including under
	// parallel make and the WITH_DESKTOP aggregate target.
	for _, unordered := range []string{
		"clients: controller",
		"clients-release: controller",
		"build: generator controller apexctl recording-agent collector-agent clients",
		"build: generator apexctl recording-agent collector-agent clients",
	} {
		if strings.Contains(makefile, unordered) {
			t.Fatalf("controller can race the desktop asset build through %q", unordered)
		}
	}
}
