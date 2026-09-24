package scripts_test

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestRecordingSuiteIsIsolatedAndRequiredWhenInvoked(t *testing.T) {
	read := func(path string) string {
		t.Helper()
		b, e := os.ReadFile(path)
		if e != nil {
			t.Fatal(e)
		}
		return string(b)
	}
	normal := read("../dashboard/playwright.config.ts")
	dedicated := read("../dashboard/playwright.recording.config.ts")
	if !strings.Contains(normal, "testDir: './e2e'") || !strings.Contains(dedicated, "testDir: './e2e-recording'") {
		t.Fatal("heavy suite discovery is not isolated")
	}
	for _, required := range []string{"retries: 0", "workers: 1", "'dev-admin', 'local-auth'"} {
		if !strings.Contains(dedicated, required) {
			t.Fatalf("missing recording invariant %s", required)
		}
	}
	if !strings.Contains(read("../dashboard/vite.config.ts"), "'e2e-recording/**/*.spec.ts'") {
		t.Fatal("Vitest must not discover browser recording specs")
	}
	runner := read("run-recording-e2e.mjs") + read("recording-report.mjs")
	for _, required := range []string{"report.stats.expected", "report.stats.skipped", "report.stats.unexpected", "report.stats.flaky", "finally", "rm(scratch"} {
		if !strings.Contains(runner, required) {
			t.Fatalf("runner missing %s", required)
		}
	}
}

func TestRecordingCIChangeAndTrustBoundary(t *testing.T) {
	b, err := os.ReadFile("../.github/workflows/recording-e2e.yml")
	if err != nil {
		t.Fatal(err)
	}
	workflow := string(b)
	for _, path := range []string{"pkg/recorder/**", "pkg/recordagent/**", "pkg/protocol/browser/**", "pkg/protocol/http/**", "pkg/api/**", "pkg/engine/**", "pkg/controller/**", "pkg/protocol/*.go", "pkg/config/**", "pkg/dslwalk/**", "pkg/netpolicy/**", "pkg/fileasset/**", "pkg/dsl/**", "pkg/convert/**", "dashboard/**", "go.mod", "go.sum"} {
		if strings.Count(workflow, "'"+path+"'") != 2 {
			t.Errorf("%s must trigger both pull-request and main-branch coverage", path)
		}
	}
	for _, required := range []string{"workflow_dispatch:", "merge_group:", "github.event.pull_request.head.repo.id == github.event.repository.id", "persist-credentials: false", "scripts/run-recording-e2e.sh", "if: always()"} {
		if !strings.Contains(workflow, required) {
			t.Errorf("CI missing %s", required)
		}
	}
}

func TestRecordingScenarioInventoryHasCoverageAssertions(t *testing.T) {
	b, err := os.ReadFile("recording-scenarios.json")
	if err != nil {
		t.Fatal(err)
	}
	var titles []string
	if err := json.Unmarshal(b, &titles); err != nil {
		t.Fatal(err)
	}
	matrix, err := os.ReadFile("../docs/qa/E2E_SUITES.md")
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, title := range titles {
		if title == "" || seen[title] {
			t.Fatalf("invalid or duplicate scenario %q", title)
		}
		seen[title] = true
		if !strings.Contains(string(matrix), title) {
			t.Errorf("scenario absent from coverage matrix: %s", title)
		}
	}
	if len(titles) == 0 {
		t.Fatal("empty recording inventory")
	}
}
