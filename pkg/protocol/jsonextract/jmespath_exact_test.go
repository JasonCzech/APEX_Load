package jsonextract

import (
	"errors"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol/jsonextract/jsonextracttest"
)

func TestNumberExtractionFixture(t *testing.T) {
	for _, tc := range jsonextracttest.NumberCases {
		t.Run(tc.Name, func(t *testing.T) {
			body := jsonextracttest.Body(tc.Raw)
			for _, expr := range []string{"$.id", "$.ids[0]"} {
				if got, err := JSONPath(expr, body); err != nil || got != tc.Raw {
					t.Errorf("JSONPath(%s) = %q, %v; want %q", expr, got, err, tc.Raw)
				}
			}
			for _, expr := range []string{"id", "ids[0]"} {
				if got, err := JMESPath(expr, body); err != nil || got != tc.Raw {
					t.Errorf("JMESPath(%s) = %q, %v; want %q", expr, got, err, tc.Raw)
				}
			}
		})
	}
}

func TestJMESPathComparesSafeFieldsBesideLargeIdentifiers(t *testing.T) {
	body := []byte(`{"items":[{"id":1234567890123456789,"price":12},{"id":2,"price":3}]}`)

	got, err := JMESPath("items[?price > `10`].id", body)

	if err != nil || got != "[1234567890123456789]" {
		t.Fatalf("got = %q, %v; want the large identifier unrounded", got, err)
	}
}

func TestJMESPathRefusesComparisonsOnUnsafeIntegers(t *testing.T) {
	body := []byte(`{"items":[{"id":1234567890123456789,"price":12}]}`)

	_, err := JMESPath("items[?id > `1`].price", body)

	var failure *JMESPathError
	if !errors.As(err, &failure) || failure.Kind != JMESPathEvaluation ||
		!strings.Contains(err.Error(), "numeric comparison is unsafe") {
		t.Fatalf("error = %v, want an unsafe-comparison evaluation error", err)
	}
}

func TestJMESPathErrorKinds(t *testing.T) {
	for _, tc := range []struct {
		name, expr, body string
		kind             JMESPathErrorKind
	}{
		{"invalid json", "id", `{"id":`, JMESPathInvalidJSON},
		{"trailing document", "id", `{"id":1}{"id":2}`, JMESPathInvalidJSON},
		{"invalid expression", "items[", `{"items":[]}`, JMESPathInvalidExpression},
		{"type error", "sum(name)", `{"name":"x"}`, JMESPathEvaluation},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := JMESPathValue(tc.expr, []byte(tc.body))
			var failure *JMESPathError
			if !errors.As(err, &failure) || failure.Kind != tc.kind {
				t.Fatalf("error = %#v, want kind %d", err, tc.kind)
			}
			if errors.Is(err, ErrNotFound) {
				t.Fatalf("error = %v, must not read as a miss", err)
			}
		})
	}
	if _, err := JMESPath("missing", []byte(`{"id":1}`)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing selection error = %v, want ErrNotFound", err)
	}
}

func TestFormatValueNeverUsesExponentFormForComputedNumbers(t *testing.T) {
	for value, want := range map[float64]string{1234567: "1234567", 6.5: "6.5", 0.0000001: "0.0000001"} {
		if got := FormatValue(value); got != want {
			t.Errorf("FormatValue(%s) = %q, want %q", strconv.FormatFloat(value, 'g', -1, 64), got, want)
		}
	}
}

// Every protocol must reach JMESPath through this package, so a private copy
// cannot reintroduce float64 rounding or exponent output. HTTP keeps its own
// implementation until its refactor lands; TestJMESPathMatchesSharedExtraction
// in pkg/protocol/http holds it to identical results meanwhile.
func TestProtocolsDoNotImportJMESPathDirectly(t *testing.T) {
	// Directories relative to pkg/protocol; packages nested below them are checked.
	allowed := map[string]bool{"jsonextract": true, "http": true}
	err := filepath.WalkDir("..", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "testdata" {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		dir, err := filepath.Rel("..", filepath.Dir(path))
		if err != nil {
			return err
		}
		if allowed[filepath.ToSlash(dir)] {
			return nil
		}
		parsed, err := parser.ParseFile(token.NewFileSet(), path, nil, parser.ImportsOnly)
		if err != nil {
			return err
		}
		for _, spec := range parsed.Imports {
			if strings.Trim(spec.Path.Value, `"`) == "github.com/jmespath/go-jmespath" {
				t.Errorf("%s imports go-jmespath directly; use jsonextract.JMESPathValue", path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
