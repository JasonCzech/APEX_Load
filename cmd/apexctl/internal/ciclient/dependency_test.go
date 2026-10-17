package ciclient

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestCoordinatorDependsOnlyOnStandardLibrary(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range files {
		file, err := parser.ParseFile(token.NewFileSet(), name, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, spec := range file.Imports {
			path, _ := strconv.Unquote(spec.Path.Value)
			if strings.Contains(strings.SplitN(path, "/", 2)[0], ".") {
				t.Errorf("%s imports %s; coordinator and its tests must remain independent of server, storage and command packages", name, path)
			}
		}
	}
}
