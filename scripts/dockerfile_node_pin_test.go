package scripts_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The dashboard-builder stage runs `npm --prefix dashboard run check:node`, whose
// preinstall gate resolves the Node pin from a path RELATIVE TO THE SCRIPT. The
// script is copied into the image on its own, so the file it reads has to be
// copied too — otherwise the gate throws ENOENT and the image build fails before
// `npm ci` ever runs. That break is invisible to every local gate (go build,
// tsc, vitest all pass), so it needs a guard here.
//
// These tests derive the required path FROM THE SCRIPT rather than hardcoding
// ".nvmrc", so repointing the script at a different file fails this test unless
// the Dockerfile is updated to match.

func repoRoot(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}
	return root
}

func readRepoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot(t), rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

// nodePinPathReadByCheckScript returns the repo-relative path that
// dashboard/scripts/check-node-version.mjs reads its pin from, resolved from the
// `new URL(<relative>, import.meta.url)` declaration in the script itself.
func nodePinPathReadByCheckScript(t *testing.T) string {
	t.Helper()
	const scriptRel = "dashboard/scripts/check-node-version.mjs"
	script := readRepoFile(t, scriptRel)

	matches := regexp.MustCompile(`new URL\(\s*'([^']+)'\s*,\s*import\.meta\.url\s*\)`).FindStringSubmatch(script)
	if len(matches) != 2 {
		t.Fatalf("%s no longer declares its pin file as new URL('<path>', import.meta.url); "+
			"update this test so the Dockerfile guard keeps tracking the real path", scriptRel)
	}

	resolved := filepath.Clean(filepath.Join(filepath.Dir(scriptRel), matches[1]))
	if strings.HasPrefix(resolved, "..") {
		t.Fatalf("%s reads %q, which resolves outside the repository (%s)", scriptRel, matches[1], resolved)
	}
	return resolved
}

// pinnedNodeVersion returns the Node.js version recorded in the file that
// check-node-version.mjs reads (today ".nvmrc"), normalised the way nvm accepts
// it. Guards in this package derive their expectations from this instead of
// embedding a version literal: every literal is one more copy of the pin that a
// correct bump has to chase, and a stale copy turns CI red inside the very test
// that is supposed to police the bump.
func pinnedNodeVersion(t *testing.T) string {
	t.Helper()
	pinPath := nodePinPathReadByCheckScript(t)
	pinned := strings.TrimPrefix(strings.TrimSpace(readRepoFile(t, pinPath)), "v")
	if !regexp.MustCompile(`^\d+\.\d+\.\d+$`).MatchString(pinned) {
		t.Fatalf("%s must contain an exact Node.js version such as 22.22.0; found %q", pinPath, pinned)
	}
	return pinned
}

func TestNodePinFileReadByThePreinstallGateExists(t *testing.T) {
	pin := nodePinPathReadByCheckScript(t)
	if _, err := os.Stat(filepath.Join(repoRoot(t), pin)); err != nil {
		t.Fatalf("check-node-version.mjs reads %s, which does not exist: %v", pin, err)
	}
}

func TestDockerfileCopiesTheNodePinBeforeThePreinstallGate(t *testing.T) {
	pin := nodePinPathReadByCheckScript(t)
	lines := strings.Split(readRepoFile(t, "Dockerfile"), "\n")

	gate := -1
	for i, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "RUN") && strings.Contains(line, "run check:node") {
			gate = i
			break
		}
	}
	if gate < 0 {
		t.Fatal("Dockerfile no longer runs the check:node preinstall gate; " +
			"if that is intentional, delete this test rather than leaving it passing vacuously")
	}

	// Only COPY instructions BEFORE the gate can satisfy it.
	copied := false
	for _, line := range lines[:gate] {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "COPY") {
			continue
		}
		// Ignore the destination argument: `COPY .nvmrc ./.nvmrc` must match on the source.
		fields := strings.Fields(trimmed)
		for _, field := range fields[1 : len(fields)-1] {
			if field == pin || field == "./"+pin {
				copied = true
			}
		}
	}
	if !copied {
		t.Fatalf("Dockerfile runs the check:node gate at line %d but never COPYs %s, which the gate reads; "+
			"the image build fails with ENOENT before npm ci runs", gate+1, pin)
	}
}

func TestDockerignoreDoesNotExcludeTheNodePin(t *testing.T) {
	pin := nodePinPathReadByCheckScript(t)
	base := filepath.Base(pin)

	for _, raw := range strings.Split(readRepoFile(t, ".dockerignore"), "\n") {
		pattern := strings.TrimSpace(raw)
		if pattern == "" || strings.HasPrefix(pattern, "#") || strings.HasPrefix(pattern, "!") {
			continue
		}
		matched, err := filepath.Match(pattern, pin)
		if err == nil && matched {
			t.Fatalf(".dockerignore pattern %q excludes %s, so the Dockerfile COPY of it is a no-op", pattern, pin)
		}
		if matched, err := filepath.Match(pattern, base); err == nil && matched {
			t.Fatalf(".dockerignore pattern %q excludes %s, so the Dockerfile COPY of it is a no-op", pattern, pin)
		}
	}
}
