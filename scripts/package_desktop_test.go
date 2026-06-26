package scripts_test

import (
	"os"
	"strings"
	"testing"
)

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
		"codesign --force --deep --options runtime --timestamp",
		"notarytool submit",
		"stapler staple",
		"spctl -a -vvv -t open",
		"Apple could not verify",
		"xattr -dr com.apple.quarantine",
		"APEXLOAD_WINDOWS_CODESIGN",
		"APEXLOAD_WINDOWS_CODESIGN_CERT_PATH",
		"signtool",
		"osslsigncode",
		"Windows SmartScreen",
		"validate_desktop_artifacts",
		"APEXLOAD_CHECKSUM_SIGN",
		"APEXLOAD_REQUIRE_TRUSTED_PACKAGES",
		"--armor --detach-sign",
		"sha256sum",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("desktop package script missing %q", check)
		}
	}
}
