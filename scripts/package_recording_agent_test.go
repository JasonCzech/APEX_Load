package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
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
		"APEXLOAD_WINDOWS_CODESIGN",
		"APEXLOAD_WINDOWS_CODESIGN_CERT_PATH",
		"signtool",
		"MSYS2_ARG_CONV_EXCL='*'",
		"cygpath -w",
		"osslsigncode",
		"Windows SmartScreen",
		"APEXLOAD_CHECKSUM_SIGN",
		"APEXLOAD_REQUIRE_TRUSTED_PACKAGES",
		"--armor --detach-sign",
		"sha256sum",
		"mktemp -d",
		"every fallible build",
		`recording_agent_packages=(*.tar.gz)`,
		`write_sha256_manifest "${recording_agent_packages[@]}"`,
	}
	for _, check := range checks {
		if !strings.Contains(script, check) {
			t.Fatalf("package script missing %q", check)
		}
	}
	if strings.Contains(script, "./*.tar.gz") {
		t.Fatal("recording-agent checksum manifest prefixes filenames with ./, which the download API cannot bind to package basenames")
	}
}

func TestRecordingAgentSigntoolUsesNativePathAndPreservesSwitches(t *testing.T) {
	tempDir := t.TempDir()
	toolDir := filepath.Join(tempDir, "fake tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}

	fakeGo := `#!/bin/sh
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    output="$1"
    break
  fi
  shift
done
[ -n "$output" ] || exit 2
mkdir -p "$(dirname "$output")"
printf 'fake Windows recording agent' > "$output"
`
	writeExecutable(t, filepath.Join(toolDir, "go"), fakeGo)
	writeExecutable(t, filepath.Join(toolDir, "cygpath"), `#!/bin/sh
printf '%s\n' "$*" >> "$CYGPATH_LOG"
printf '%s\n' 'C:\Program Files\APEX Load\apexload-record-agent.exe'
`)
	fakeSigntool := filepath.Join(toolDir, "signtool")
	writeExecutable(t, fakeSigntool, `#!/bin/sh
{
  printf 'ENV=%s\n' "${MSYS2_ARG_CONV_EXCL:-}"
  for arg in "$@"; do printf 'ARG=%s\n' "$arg"; done
  printf 'END\n'
} >> "$SIGNTOOL_LOG"
`)

	signtoolLog := filepath.Join(tempDir, "signtool.log")
	cygpathLog := filepath.Join(tempDir, "cygpath.log")
	cmd := exec.Command("bash", "package-recording-agent.sh")
	cmd.Env = append(os.Environ(),
		"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VERSION=2.0.0",
		"PLATFORMS=windows/amd64",
		"DIST_DIR="+filepath.Join(tempDir, "dist"),
		"APEXLOAD_WINDOWS_CODESIGN=1",
		"APEXLOAD_WINDOWS_CODESIGN_TOOL=signtool",
		"APEXLOAD_WINDOWS_SIGNTOOL="+fakeSigntool,
		"APEXLOAD_WINDOWS_CODESIGN_SHA1=DEADBEEF",
		"APEXLOAD_WINDOWS_CODESIGN_STORE=My",
		"APEXLOAD_WINDOWS_CODESIGN_TIMESTAMP_URL=https://timestamp.example.test",
		"SIGNTOOL_LOG="+signtoolLog,
		"CYGPATH_LOG="+cygpathLog,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("recording-agent signtool package failed: %v\n%s", err, output)
	}

	logged, err := os.ReadFile(signtoolLog)
	if err != nil {
		t.Fatal(err)
	}
	want := `ENV=*
ARG=sign
ARG=/fd
ARG=sha256
ARG=/td
ARG=sha256
ARG=/tr
ARG=https://timestamp.example.test
ARG=/s
ARG=My
ARG=/sha1
ARG=DEADBEEF
ARG=C:\Program Files\APEX Load\apexload-record-agent.exe
END
ENV=*
ARG=verify
ARG=/pa
ARG=/all
ARG=C:\Program Files\APEX Load\apexload-record-agent.exe
END
`
	if string(logged) != want {
		t.Fatalf("signtool received rewritten or incorrectly quoted arguments:\n%s\nwant:\n%s", logged, want)
	}
	cygpathArgs, err := os.ReadFile(cygpathLog)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(cygpathArgs), "-w ") || !strings.Contains(string(cygpathArgs), "apexload-record-agent.exe") {
		t.Fatalf("cygpath did not receive the staged Windows binary: %q", cygpathArgs)
	}
}

func TestRecordingAgentPackageRejectsInvalidVersionBeforeStaging(t *testing.T) {
	for _, version := range []string{
		"../../../../outside",
		"1.2.3/../../outside",
		"1.2.3junk",
		"1.2.3-01",
	} {
		t.Run(strings.ReplaceAll(version, "/", "_"), func(t *testing.T) {
			distDir := filepath.Join(t.TempDir(), "dist")
			cmd := exec.Command("bash", "package-recording-agent.sh")
			cmd.Env = append(os.Environ(),
				"VERSION="+version,
				"PLATFORMS=linux/amd64",
				"DIST_DIR="+distDir,
			)
			output, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("invalid recording-agent version %q was accepted:\n%s", version, output)
			}
			if !strings.Contains(string(output), "canonical SemVer") {
				t.Fatalf("invalid version rejection was not actionable:\n%s", output)
			}
			if entries, readErr := os.ReadDir(distDir); readErr == nil && len(entries) != 0 {
				t.Fatalf("invalid version created staging artifacts: %v", entries)
			} else if readErr != nil && !os.IsNotExist(readErr) {
				t.Fatal(readErr)
			}
		})
	}
}

func TestRecordingAgentPackageFailurePreservesExistingRelease(t *testing.T) {
	tempDir := t.TempDir()
	distDir := filepath.Join(tempDir, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	oldName := "apexload-recording-agent-1.9.0-linux-amd64.tar.gz"
	oldPath := filepath.Join(distDir, oldName)
	if err := os.WriteFile(oldPath, []byte("known-good-agent"), 0o644); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(distDir, "SHA256SUMS.txt")
	if err := os.WriteFile(manifestPath, []byte("known-good-manifest\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolDir := filepath.Join(tempDir, "tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(toolDir, "go"), []byte("#!/bin/sh\nexit 42\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "package-recording-agent.sh")
	cmd.Env = append(os.Environ(),
		"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VERSION=2.0.0",
		"PLATFORMS=linux/amd64",
		"DIST_DIR="+distDir,
	)
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("forced recording-agent build failure unexpectedly succeeded:\n%s", output)
	}
	for path, want := range map[string]string{
		oldPath:      "known-good-agent",
		manifestPath: "known-good-manifest\n",
	} {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("existing release file %s was removed: %v", path, err)
		}
		if string(contents) != want {
			t.Fatalf("existing release file %s changed to %q", path, contents)
		}
	}
	staged, err := filepath.Glob(filepath.Join(distDir, ".apexload-recording-agent-stage.*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(staged) != 0 {
		t.Fatalf("failed recording-agent package left staging directories: %v", staged)
	}
}

func TestRecordingAgentPackageRemovesSameVersionPlatformsOmittedFromManifest(t *testing.T) {
	tempDir := t.TempDir()
	distDir := filepath.Join(tempDir, "dist")
	if err := os.MkdirAll(distDir, 0o755); err != nil {
		t.Fatal(err)
	}
	omittedPath := filepath.Join(distDir, "apexload-recording-agent-2.0.0-linux-arm64.tar.gz")
	if err := os.WriteFile(omittedPath, []byte("stale same-version platform"), 0o644); err != nil {
		t.Fatal(err)
	}
	toolDir := filepath.Join(tempDir, "tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	fakeGo := `#!/bin/sh
output=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "-o" ]; then
    shift
    output="$1"
    break
  fi
  shift
done
[ -n "$output" ] || exit 2
mkdir -p "$(dirname "$output")"
printf 'fake recording agent' > "$output"
chmod 755 "$output"
`
	if err := os.WriteFile(filepath.Join(toolDir, "go"), []byte(fakeGo), 0o755); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command("bash", "package-recording-agent.sh")
	cmd.Env = append(os.Environ(),
		"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"VERSION=2.0.0",
		"PLATFORMS=linux/amd64",
		"DIST_DIR="+distDir,
	)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("subset recording-agent package failed: %v\n%s", err, output)
	}
	if _, err := os.Stat(omittedPath); !os.IsNotExist(err) {
		t.Fatalf("same-version package omitted from the new manifest survived: %v", err)
	}
	for _, name := range []string{
		"apexload-recording-agent-2.0.0-linux-amd64.tar.gz",
		"SHA256SUMS.txt",
	} {
		if _, err := os.Stat(filepath.Join(distDir, name)); err != nil {
			t.Fatalf("published subset artifact %s: %v", name, err)
		}
	}
}
