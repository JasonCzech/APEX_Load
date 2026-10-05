package rca

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The engine must stay pure: it may use the standard library (without network,
// database or process access) and the shared analysis primitives, nothing else.
func TestRCAImportsNoStorageProviderOrHTTPPackage(t *testing.T) {
	allowedModule := map[string]bool{"github.com/jasonczech/apexload/pkg/analysis": true}
	forbiddenStd := []string{"net", "database", "os/exec", "syscall"}
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(token.NewFileSet(), name, source, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				if !allowedModule[path] {
					t.Errorf("%s imports %s; pkg/rca may only import the standard library and pkg/analysis", name, path)
				}
				continue
			}
			for _, prefix := range forbiddenStd {
				if path == prefix || strings.HasPrefix(path, prefix+"/") {
					t.Errorf("%s imports %s; pkg/rca must not reach the network, databases or processes", name, path)
				}
			}
		}
	}
}
