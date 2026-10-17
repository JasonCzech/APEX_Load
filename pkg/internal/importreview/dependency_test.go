package importreview

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const moduleImportPrefix = "github.com/jasonczech/apexload/"

// Follow transitive local imports so a helper cannot quietly pull controller
// storage, assets, or recorder ownership back into draft review.
func TestImportReviewDependencyGraphExcludesControllerAndStorage(t *testing.T) {
	root := filepath.Join("..", "..", "..")
	visited := map[string]bool{}
	var visit func(string, []string)
	visit = func(pkg string, chain []string) {
		if visited[pkg] {
			return
		}
		visited[pkg] = true
		if pkg == "pkg/api" || pkg == "pkg/storage" || pkg == "pkg/recordbridge" || pkg == "pkg/recordagent" {
			t.Errorf("import review imports an ownership adapter: %s", strings.Join(append(chain, pkg), " -> "))
			return
		}
		entries, err := os.ReadDir(filepath.Join(root, pkg))
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
				continue
			}
			path := filepath.Join(root, pkg, entry.Name())
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range file.Imports {
				value, err := strconv.Unquote(imp.Path.Value)
				if err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(value, moduleImportPrefix) {
					visit(strings.TrimPrefix(value, moduleImportPrefix), append(chain, pkg))
				}
			}
		}
	}
	for _, pkg := range []string{"pkg/internal/importreview"} {
		visit(pkg, nil)
	}
}
