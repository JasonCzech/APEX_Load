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
		"osslsigncode",
		"Windows SmartScreen",
		"validate_desktop_artifacts",
		"APEXLOAD_CHECKSUM_SIGN",
		"APEXLOAD_REQUIRE_TRUSTED_PACKAGES",
		"--armor --detach-sign",
		"sha256sum",
		"clean_desktop_dist",
		"find \"$DIST_DIR\" -mindepth 1",
		"write_sha256_manifest apexload-desktop-*.tar.gz > SHA256SUMS.txt",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("desktop package script missing %q", check)
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
			`sign_args+=(/s My /sha1 "$WINDOWS_CODESIGN_SHA1")`,
		} {
			if !strings.Contains(script, required) {
				t.Errorf("%s missing password-safe contract %q", path, required)
			}
		}
	}
}
