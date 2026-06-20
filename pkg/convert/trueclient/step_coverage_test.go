package trueclient

import (
	"os"
	"testing"
)

const stepCoverageDocPath = "../../../docs/LOADRUNNER_TRUCLIENT_STEP_COVERAGE.md"

// TestTrueClientStepCoverageDocIsFresh enforces that the committed coverage doc
// matches the generator output. Run `go generate ./pkg/convert/trueclient`.
func TestTrueClientStepCoverageDocIsFresh(t *testing.T) {
	committed, err := os.ReadFile(stepCoverageDocPath)
	if err != nil {
		t.Fatalf("read coverage doc: %v (run go generate ./pkg/convert/trueclient)", err)
	}
	if got := RenderStepCoverageMarkdown(); got != string(committed) {
		t.Fatalf("%s is stale — run `go generate ./pkg/convert/trueclient` (cmd/webcoverage -set trueclient)", stepCoverageDocPath)
	}
}

// TestTrueClientStepCoverageCoversEveryStep guarantees every published step
// resolves to a scope (no blank family/status/shape) and that the doc inventory
// matches the completeness test's authoritative list.
func TestTrueClientStepCoverageCoversEveryStep(t *testing.T) {
	rows := StepCoverage()
	if len(rows) != len(publishedTruClientStepNames) {
		t.Fatalf("coverage rows = %d, want %d", len(rows), len(publishedTruClientStepNames))
	}
	for _, row := range rows {
		if row.Family == "" || row.Status == "" || row.APEXLoadShape == "" || row.Notes == "" {
			t.Errorf("step %q has an incomplete coverage row: %#v", row.Step, row)
		}
		if _, ok := publishedTruClientSteps[row.Step]; !ok {
			t.Errorf("coverage doc lists %q, which is not in the authoritative published-steps table", row.Step)
		}
	}
}
