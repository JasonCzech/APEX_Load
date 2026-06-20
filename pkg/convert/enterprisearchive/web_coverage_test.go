package enterprisearchive

import (
	"os"
	"testing"
)

const webCoverageDocPath = "../../../docs/LOADRUNNER_WEB_FUNCTION_COVERAGE.md"

// TestWebCoverageDocIsFresh enforces that the committed coverage document matches
// what the generator renders from the registry — mirroring the dsl.schema.json
// freshness discipline. Run `go generate ./pkg/convert/enterprisearchive` to refresh.
func TestWebCoverageDocIsFresh(t *testing.T) {
	committed, err := os.ReadFile(webCoverageDocPath)
	if err != nil {
		t.Fatalf("read coverage doc: %v (run go generate ./pkg/convert/enterprisearchive)", err)
	}
	if got := RenderWebHTTPFunctionCoverageMarkdown(); got != string(committed) {
		t.Fatalf("%s is stale — run `go generate ./pkg/convert/enterprisearchive` (cmd/webcoverage)", webCoverageDocPath)
	}
}

// TestWebCoverageDetailCoversEveryFunction guarantees every published function has
// a target/notes entry, so the coverage doc never ships a blank cell.
func TestWebCoverageDetailCoversEveryFunction(t *testing.T) {
	for _, row := range WebHTTPFunctionCoverage() {
		if row.Target == "" || row.Notes == "" {
			t.Errorf("function %q has an incomplete coverage detail (target=%q notes=%q)", row.Function, row.Target, row.Notes)
		}
	}
}
