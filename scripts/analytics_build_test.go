package scripts_test

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

func TestNativeAnalyticsBuildProfiles(t *testing.T) {
	data, err := os.ReadFile("../Dockerfile.controller")
	if err != nil {
		t.Fatal(err)
	}
	s := string(data)
	for _, fragment := range []string{"CGO_ENABLED=1", "analytics_duckdb", "golang:1.26.9-bookworm@sha256:", "FROM debian:trixie-slim@sha256:", "libstdc++6", "node:22.22.0-bookworm-slim@sha256:", "COPY --from=dashboard-builder /usr/local/bin/node /usr/bin/node", "test \"${BINARY}\" = controller"} {
		if !strings.Contains(s, fragment) {
			t.Fatalf("controller native profile missing %q", fragment)
		}
	}
	for _, line := range strings.Split(s, "\n") {
		if strings.HasPrefix(line, "FROM ") && !regexp.MustCompile(`@sha256:[a-f0-9]{64}( |$)`).MatchString(line) {
			t.Fatalf("un-pinned native base image: %s", line)
		}
	}
	data, err = os.ReadFile("../.github/workflows/release.yml")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "dockerfile: Dockerfile.controller") != 2 {
		t.Fatal("both controller release profiles must enable native analytics")
	}
	data, err = os.ReadFile("../scripts/build-desktop-local.sh")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "analytics_duckdb") || !strings.Contains(string(data), "export CGO_ENABLED=1") {
		t.Fatal("desktop must enable native analytics")
	}
}
