package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestMakefilePassesWailsPlatformToDesktopPackager(t *testing.T) {
	content, err := os.ReadFile("../Makefile")
	if err != nil {
		t.Fatalf("read Makefile: %v", err)
	}
	makefile := string(content)
	checks := []string{
		"WAILS_PLATFORM ?=",
		"WAILS_PLATFORM_FLAG",
		"-platform $(WAILS_PLATFORM)",
		"APEXLOAD_DESKTOP_PACKAGE_PLATFORM=\"$(WAILS_PLATFORM)\"",
	}
	for _, check := range checks {
		if !strings.Contains(makefile, check) {
			t.Fatalf("Makefile missing %q", check)
		}
	}
}
