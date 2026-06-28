package enterprisearchive

import (
	"os"
	"testing"
)

const javaSurfaceCoverageDocPath = "../../../docs/LOADRUNNER_JAVA_VUSER_IMPORT_COVERAGE.md"

func TestJavaSurfaceCoverageDocIsFresh(t *testing.T) {
	committed, err := os.ReadFile(javaSurfaceCoverageDocPath)
	if err != nil {
		t.Fatalf("read coverage doc: %v (run go generate ./pkg/convert/enterprisearchive)", err)
	}
	if got := RenderJavaImportSurfaceCoverageMarkdown(); got != string(committed) {
		t.Fatalf("%s is stale; run `go generate ./pkg/convert/enterprisearchive` (cmd/webcoverage -set java-surfaces)", javaSurfaceCoverageDocPath)
	}
}

func TestJavaImportSurfaceCoverageDocumentsExecutableFamilies(t *testing.T) {
	matrix := JavaImportSurfaceCoverage()
	if len(matrix) == 0 {
		t.Fatal("expected Java import surface matrix entries")
	}
	for _, tc := range []struct {
		id              string
		executionStatus string
	}{
		{"java-web-http", "executable"},
		{"java-jms", "executable"},
		{"java-jdbc", "executable"},
		{"java-soap", "scoped_executable"},
		{"java-no-runtime-surfaces", "not_executable"},
	} {
		entry, ok := javaSurfaceSupportByID(matrix, tc.id)
		if !ok {
			t.Fatalf("Java import surface matrix missing %s: %#v", tc.id, matrix)
		}
		if entry.ExecutionStatus != tc.executionStatus || entry.Area == "" || entry.Construct == "" || entry.Status == "" {
			t.Fatalf("Java import surface entry %s = %#v, want execution_status=%s and populated fields", tc.id, entry, tc.executionStatus)
		}
	}
}

func TestJavaIdiomCoverageDocumentsExecutableBuilders(t *testing.T) {
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
		if entry.ExecutionStatus != "executable" || entry.Status == "" || entry.Construct == "" {
			t.Fatalf("%s idiom entry = %#v, want executable and populated fields", tc.name, entry)
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
