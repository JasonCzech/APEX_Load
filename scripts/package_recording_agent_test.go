package scripts_test

import (
	"os"
	"strings"
	"testing"
)

func TestRecordingAgentPackageScriptDocumentsMacOSTrustPath(t *testing.T) {
	content, err := os.ReadFile("package-recording-agent.sh")
	if err != nil {
		t.Fatalf("read package script: %v", err)
	}
	script := string(content)
	checks := []string{
		"APEXLOAD_MACOS_CODESIGN_IDENTITY",
		"APEXLOAD_MACOS_NOTARIZE",
		"APEXLOAD_MACOS_NOTARY_PROFILE",
		"codesign --force --options runtime --timestamp",
		"notarytool submit",
		"Apple could not verify",
		"xattr -dr com.apple.quarantine",
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("package script missing %q", check)
		}
	}
}
