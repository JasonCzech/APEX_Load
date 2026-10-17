// Package extractiontest supplies test-only dialect fixtures. Runtime parsers
// intentionally retain different path syntax, result formatting, and budgets.
package extractiontest

import (
	_ "embed"
	"encoding/json"
	"slices"
	"testing"
)

//go:embed corpus.json
var corpus []byte

type expectation struct {
	Values []string
	Error  bool
}
type fixture struct {
	Name, Body, Path string
	Expect           map[string]expectation
}

// Run checks common inputs against a dialect's own accepted syntax and result
// shape. A missing field can be an error or an empty selection, not a universal
// parser policy. This helper is imported only by tests.
func Run(t *testing.T, dialect string, extract func(string, []byte) ([]string, error)) {
	t.Helper()
	var fixtures []fixture
	if err := json.Unmarshal(corpus, &fixtures); err != nil {
		t.Fatal(err)
	}
	for _, test := range fixtures {
		t.Run(test.Name, func(t *testing.T) {
			want, exists := test.Expect[dialect]
			if !exists {
				t.Fatalf("missing %s expectation", dialect)
			}
			got, err := extract(test.Path, []byte(test.Body))
			if (err != nil) != want.Error {
				t.Fatalf("path %q: error=%v wantError=%v", test.Path, err, want.Error)
			}
			if err == nil && !slices.Equal(got, want.Values) {
				t.Fatalf("path %q: values=%q want=%q", test.Path, got, want.Values)
			}
		})
	}
}

func Scalar(extract func(string, []byte) (string, error)) func(string, []byte) ([]string, error) {
	return func(path string, body []byte) ([]string, error) {
		value, err := extract(path, body)
		return []string{value}, err
	}
}
