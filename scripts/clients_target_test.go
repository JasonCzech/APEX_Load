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
		"clients: controller desktop-preflight desktop",
		"desktop-preflight:",
		"./scripts/build-desktop-local.sh --check",
		"clients-release: controller desktop-release",
		"ifeq ($(WITH_DESKTOP),1)",
		"build: clients",
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
}
