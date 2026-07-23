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

func TestJavaLRCoverageDoesNotOverclaimExitContinuationSemantics(t *testing.T) {
	for _, row := range JavaLRFunctionCoverage() {
		if row.Function != "lr_exit" {
			continue
		}
		if row.SupportMode != string(SourceFunctionSupportReviewOnly) {
			t.Fatalf("lr_exit support mode = %q, want review_only until engine continuation semantics have integration coverage", row.SupportMode)
		}
		if row.Target != "authoring/fidelity review" {
			t.Fatalf("lr_exit target = %q, want authoring/fidelity review", row.Target)
		}
		return
	}
	t.Fatal("lr_exit missing from Java LR coverage")
}

func TestJavaLRExecutableCoverageHasVerifiedBytecodeDescriptors(t *testing.T) {
	for _, row := range JavaLRFunctionCoverage() {
		if row.SupportMode == string(SourceFunctionSupportReviewOnly) {
			continue
		}
		descriptor, ok := javaLRRunnerABIDescriptors[row.Function]
		if !ok || descriptor == "" {
			t.Errorf("%s is advertised as %s without a verified JVM descriptor", row.Function, row.SupportMode)
		}
	}
	for function, descriptor := range javaLRRunnerABIDescriptors {
		if def, _, ok := javaLRRunnerDisposition(function); !ok {
			t.Errorf("verified descriptor %s %s has no runner disposition", function, descriptor)
		} else if def.SupportMode != SourceFunctionSupportExecutable {
			t.Errorf("verified descriptor %s %s resolves as %s", function, descriptor, def.SupportMode)
		}
	}
}
