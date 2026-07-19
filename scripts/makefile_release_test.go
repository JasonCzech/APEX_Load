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
		"WAILS ?=",
		"WAILS_PLATFORM ?=",
		`WAILS="$(WAILS)" WAILS_PLATFORM="$(WAILS_PLATFORM)" ./scripts/build-desktop-local.sh`,
		`VERSION="$(VERSION)" COMMIT="$(COMMIT)" BUILD_TIME="$(BUILD_TIME)" APEXLOAD_DESKTOP_PACKAGE_PLATFORM="$(WAILS_PLATFORM)" ./scripts/package-desktop.sh`,
		"APEXLOAD_DESKTOP_PACKAGE_PLATFORM=\"$(WAILS_PLATFORM)\"",
	}
	for _, check := range checks {
		if !strings.Contains(makefile, check) {
			t.Fatalf("Makefile missing %q", check)
		}
	}
	if strings.Contains(makefile, "WAILS_PLATFORM_FLAG") {
		t.Fatal("Makefile retains the dead WAILS_PLATFORM_FLAG variable")
	}
}
