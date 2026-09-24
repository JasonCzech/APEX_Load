package enterprisearchive

import (
	"os"
	"strings"
	"testing"
)

const javaSurfaceCoverageDocPath = "../../../docs/LOADRUNNER_COVERAGE.md"

func TestJavaSurfaceCoverageDocIsFresh(t *testing.T) {
	committed, err := os.ReadFile(javaSurfaceCoverageDocPath)
	if err != nil {
		t.Fatalf("read coverage doc: %v (run go generate ./pkg/convert/enterprisearchive)", err)
	}
	if got := RenderJavaImportSurfaceCoverageMarkdown(); !strings.Contains(string(committed), got) {
		t.Fatalf("%s is stale; run `go generate ./pkg/convert/enterprisearchive` (cmd/webcoverage)", javaSurfaceCoverageDocPath)
	}
}

func TestJavaImportSurfaceCoverageDocumentsCombinedRunnerFamilies(t *testing.T) {
	matrix := JavaImportSurfaceCoverage()
	if len(matrix) == 0 {
		t.Fatal("expected Java import surface matrix entries")
	}
	for _, tc := range []struct {
		id              string
		executionStatus string
	}{
		{"java-web-http", "external_runner_required"},
		{"java-jms", "external_runner_required"},
		{"java-jdbc", "external_runner_required"},
		{"java-soap", "external_runner_required"},
		{"java-no-runtime-surfaces", "external_runner_required"},
	} {
		entry, ok := javaSurfaceSupportByID(matrix, tc.id)
		if !ok {
			t.Fatalf("Java import surface matrix missing %s: %#v", tc.id, matrix)
		}
		if entry.ExecutionStatus != tc.executionStatus || !entry.ReviewRequired || entry.Area == "" || entry.Construct == "" || entry.Status == "" {
			t.Fatalf("Java import surface entry %s = %#v, want execution_status=%s and populated fields", tc.id, entry, tc.executionStatus)
		}
	}
}

func TestJavaIdiomCoverageDocumentsCombinedRunnerBuilders(t *testing.T) {
	for _, tc := range []struct {
		name   string
		matrix []JavaImportSurfaceSupportEntry
		id     string
	}{
		{"HTTP", JavaHTTPIdiomCoverage(), "java-web-custom-request"},
		{"JMS", JavaJMSIdiomCoverage(), "java-jms-publish-consume"},
		{"JDBC", JavaJDBCIdiomCoverage(), "java-jdbc-query"},
	} {
		entry, ok := javaSurfaceSupportByID(tc.matrix, tc.id)
		if !ok {
			t.Fatalf("%s idiom matrix missing %s: %#v", tc.name, tc.id, tc.matrix)
		}
		if entry.ExecutionStatus != "external_runner_required" || !entry.ReviewRequired || entry.Status == "" || entry.Construct == "" {
			t.Fatalf("%s idiom entry = %#v, want external runner review and populated fields", tc.name, entry)
		}
	}
}

func TestJavaIdiomCoverageDocumentsPersistentPerVUserState(t *testing.T) {
	for _, entry := range append(JavaJMSIdiomCoverage(), JavaJDBCIdiomCoverage()...) {
		lower := strings.ToLower(entry.Notes)
		for _, stale := range []string{
			"does not cross phase",
			"cross-phase java state is not retained",
			"state does not persist",
		} {
			if strings.Contains(lower, stale) {
				t.Fatalf("%s notes still describe one-shot lifecycle state: %q", entry.ID, entry.Notes)
			}
		}
	}
	for _, id := range []string{"java-jms-config", "java-jms-destination", "java-jms-message", "java-jms-transaction", "java-jdbc-connect", "java-jdbc-transactions"} {
		matrices := append(JavaJMSIdiomCoverage(), JavaJDBCIdiomCoverage()...)
		entry, ok := javaSurfaceSupportByID(matrices, id)
		if !ok {
			t.Fatalf("missing persistent-state coverage row %s", id)
		}
		if !strings.Contains(strings.ToLower(entry.Notes), "persist") {
			t.Fatalf("%s notes do not describe persistent per-VUser state: %q", id, entry.Notes)
		}
	}
}

func javaSurfaceSupportByID(entries []JavaImportSurfaceSupportEntry, id string) (JavaImportSurfaceSupportEntry, bool) {
	for _, entry := range entries {
		if entry.ID == id {
			return entry, true
		}
	}
	return JavaImportSurfaceSupportEntry{}, false
}
