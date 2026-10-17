package dsl

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

// Follow module-local imports instead of checking only DSL's direct imports:
// a descriptor helper must never quietly reintroduce transport registration.
func TestAuthoringDependencyGraphExcludesProtocolImplementations(t *testing.T) {
	root := filepath.Join("..", "..")
	visited := map[string]bool{}
	var visit func(string, []string)
	visit = func(pkg string, chain []string) {
		if visited[pkg] {
			return
		}
		visited[pkg] = true
		if pkg == "pkg/protocol/http" || pkg == "pkg/protocol/browser" || pkg == "pkg/protocol/builtin" {
			t.Errorf("authoring imports runtime implementation: %s", strings.Join(append(chain, pkg), " -> "))
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
	for _, pkg := range []string{"pkg/dsl", "pkg/dsl/schemagen", "pkg/protocol/httpcontract", "pkg/protocol/browsercontract", "pkg/protocol/kafkacontract", "pkg/convert/requestimport", "pkg/convert/sample", "pkg/recorder/browser"} {
		visit(pkg, nil)
	}
}

func TestImportersUseProtocolContractsDirectly(t *testing.T) {
	err := filepath.WalkDir(filepath.Join("..", "convert"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, imp := range file.Imports {
			value, err := strconv.Unquote(imp.Path.Value)
			if err != nil {
				return err
			}
			if value == moduleImportPrefix+"pkg/protocol/http" || value == moduleImportPrefix+"pkg/protocol/browser" {
				t.Errorf("%s imports %s; source contracts belong below runtime implementations", path, value)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
