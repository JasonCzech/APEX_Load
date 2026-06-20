package enterprisearchive

import (
	"os"
	"testing"
)

const javaLRCoverageDocPath = "../../../docs/LOADRUNNER_JAVA_LR_FUNCTION_COVERAGE.md"

// TestJavaLRCoverageDocIsFresh enforces that the committed Java lr coverage doc
// matches the generator output. Run `go generate ./pkg/convert/enterprisearchive`.
func TestJavaLRCoverageDocIsFresh(t *testing.T) {
	committed, err := os.ReadFile(javaLRCoverageDocPath)
	if err != nil {
		t.Fatalf("read coverage doc: %v (run go generate ./pkg/convert/enterprisearchive)", err)
	}
	if got := RenderJavaLRFunctionCoverageMarkdown(); got != string(committed) {
		t.Fatalf("%s is stale — run `go generate ./pkg/convert/enterprisearchive` (cmd/webcoverage -set java)", javaLRCoverageDocPath)
	}
}

// TestJavaLRCoverageCoversEveryMethod guarantees every published Java lr method has
// a target/notes entry and resolves in a registry, so no row ships blank.
func TestJavaLRCoverageCoversEveryMethod(t *testing.T) {
	rows := JavaLRFunctionCoverage()
	if len(rows) != len(javaLRPublishedMethods) {
		t.Fatalf("coverage rows = %d, want %d", len(rows), len(javaLRPublishedMethods))
	}
	for _, row := range rows {
		if row.Family == "" || row.SupportMode == "" {
			t.Errorf("method %q did not resolve in a registry (family=%q mode=%q)", row.Function, row.Family, row.SupportMode)
		}
		if row.Target == "" || row.Notes == "" {
			t.Errorf("method %q has an incomplete coverage detail (target=%q notes=%q)", row.Function, row.Target, row.Notes)
		}
	}
}
