package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRegistryReferenceStateClassifiesInspectionResults(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tool       string
		mode       string
		wantOutput string
		wantError  string
	}{
		{name: "docker present", tool: "docker-buildx", mode: "present", wantOutput: "present"},
		{name: "docker manifest missing", tool: "docker-buildx", mode: "manifest-missing", wantOutput: "absent"},
		{name: "docker reference missing", tool: "docker-buildx", mode: "reference-missing", wantOutput: "absent"},
		{name: "skopeo present", tool: "skopeo", mode: "present", wantOutput: "present"},
		{name: "skopeo repository missing", tool: "skopeo", mode: "name-missing", wantOutput: "absent"},
		{name: "authentication failure", tool: "docker-buildx", mode: "authentication-failure", wantError: "without a confirmed not-found response"},
		{name: "transient failure", tool: "skopeo", mode: "transient-failure", wantError: "without a confirmed not-found response"},
		{name: "unqualified 404 fails closed", tool: "docker-buildx", mode: "generic-404", wantError: "without a confirmed not-found response"},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			toolDir := t.TempDir()
			writeRegistryInspectionFake(t, toolDir, "docker")
			writeRegistryInspectionFake(t, toolDir, "skopeo")

			cmd := exec.Command("../scripts/release/registry-reference-state.sh", test.tool, "registry.example/apexload-controller:v1.2.3-standard")
			cmd.Env = append(os.Environ(),
				"PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"),
				"FAKE_REGISTRY_INSPECT_MODE="+test.mode,
			)
			output, err := cmd.CombinedOutput()
			if test.wantError != "" {
				if err == nil {
					t.Fatalf("inspection unexpectedly succeeded:\n%s", output)
				}
				if !strings.Contains(string(output), test.wantError) {
					t.Fatalf("error output %q does not contain %q", output, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("inspection failed: %v\n%s", err, output)
			}
			if got := strings.TrimSpace(string(output)); got != test.wantOutput {
				t.Fatalf("state = %q, want %q", got, test.wantOutput)
			}
		})
	}
}

func writeRegistryInspectionFake(t *testing.T, directory, name string) {
	t.Helper()
	path := filepath.Join(directory, name)
	content := `#!/usr/bin/env bash
set -euo pipefail
mode="${FAKE_REGISTRY_INSPECT_MODE:?}"
reference="${*: -1}"
case "$mode" in
  present)
    printf '{"digest":"sha256:present"}\n'
    ;;
  manifest-missing)
    printf 'manifest unknown: requested manifest does not exist\n' >&2
    exit 1
    ;;
  name-missing)
    printf 'NAME_UNKNOWN: repository name not known to registry\n' >&2
    exit 1
    ;;
  reference-missing)
    printf '%s: not found\n' "$reference" >&2
    exit 1
    ;;
  authentication-failure)
    printf 'unauthorized: authentication required\n' >&2
    exit 1
    ;;
  transient-failure)
    printf 'dial tcp: connection reset by peer\n' >&2
    exit 1
    ;;
  generic-404)
    printf 'unexpected status from proxy: 404 Not Found\n' >&2
    exit 1
    ;;
  *)
    exit 99
    ;;
esac
`
	if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
		t.Fatal(err)
	}
}
