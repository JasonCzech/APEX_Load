package contentmutation

import (
	"go/parser"
	"go/token"
	"os"
	"strconv"
	"strings"
	"testing"
)

// This is deliberately stronger than a direct api/storage denylist: both the
// coordinator and its tests stay standard-library-only, so transitive domain
// dependencies cannot silently turn the small race gate into a server build.
func TestCoordinatorAndRegressionTestsRemainDependencyIndependent(t *testing.T) {
	entries, err := os.ReadDir(".")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") {
			continue
		}
		file, err := parser.ParseFile(token.NewFileSet(), entry.Name(), nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				t.Fatal(err)
			}
			// Standard-library import paths never have a dotted first component.
			if strings.Contains(strings.Split(path, "/")[0], ".") {
				t.Errorf("%s imports %s; keep content ownership and race tests independent of domain/server/storage packages", entry.Name(), path)
			}
		}
	}
}
