package engine

import (
	"regexp"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestUUIDParameterFormat(t *testing.T) {
	lower := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	upper := regexp.MustCompile(`^[0-9A-F]{8}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{4}-[0-9A-F]{12}$`)
	for format, pattern := range map[string]*regexp.Regexp{"": lower, "lowercase": lower, "uppercase": upper} {
		runtime := NewParameterRuntime([]dsl.Parameter{{Name: "id", Type: "uuid", Format: format}}, nil, 0)
		runtime.Prime()
		if value, ok := runtime.ResolveNamed("id"); !ok || !pattern.MatchString(value) {
			t.Fatalf("format %q produced %q", format, value)
		}
	}
	// A format left over from another parameter type keeps the default, as
	// before uuid formats existed, rather than failing validation.
	runtime := NewParameterRuntime([]dsl.Parameter{{Name: "id", Type: "uuid", Format: "iso8601"}}, nil, 0)
	runtime.Prime()
	if value, ok := runtime.ResolveNamed("id"); !ok || !lower.MatchString(value) {
		t.Fatalf("stale format produced %q", value)
	}
	issues := dsl.ValidateScriptIssues(&dsl.Script{Name: "s", Version: "1", Protocol: "http", Parameters: []dsl.Parameter{{Name: "id", Type: "uuid", Format: "iso8601"}}, Actions: []dsl.Action{{Name: "a", Type: "GET", Target: "https://example.test/"}}})
	for _, issue := range issues {
		if regexp.MustCompile(`format`).MatchString(issue) {
			t.Fatalf("stale uuid format rejected: %v", issues)
		}
	}
}
