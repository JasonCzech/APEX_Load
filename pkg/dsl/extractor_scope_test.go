package dsl

import (
	"strings"
	"testing"

	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
)

func TestExtractorScopeValidation(t *testing.T) {
	script := func(ext Extractor) *Script {
		return &Script{Name: "s", Protocol: "http", Config: ScriptConfig{BaseURL: "https://x.test"}, Actions: []Action{{Type: "http", Name: "a", Method: "GET", Target: "/", Extractors: []Extractor{ext}}}}
	}
	valid := Extractor{Name: "v", Source: "header", Type: "boundary", Expression: "a|||b", Scope: &ExtractorScope{Redirects: "include", Resources: "exclude", RequestURL: "*token*", ContentType: "text/*", HeaderNames: []string{"Location"}}}
	if issues := ValidateScriptIssues(script(valid)); len(issues) != 0 {
		t.Fatalf("valid scope rejected: %v", issues)
	}
	invalid := Extractor{Name: "v", Source: "body", Type: "regex", Expression: "(x)", Scope: &ExtractorScope{Redirects: "always", RequestURL: strings.Repeat("*", httpprotocol.MaxExtractorExpressionBytes+1), HeaderNames: []string{"Bad Name"}}}
	issues := ValidateScriptIssues(script(invalid))
	for _, want := range []string{"scope.redirects must be", "scope.request_url exceeds", "scope.header_names applies only to the header, all or cookies sources", `scope.header_names has an invalid header name "Bad Name"`} {
		if !hasIssue(issues, want) {
			t.Errorf("missing %q in %v", want, issues)
		}
	}
}

func TestExtractorDFEValidation(t *testing.T) {
	script := &Script{Name: "s", Protocol: "http", Config: ScriptConfig{BaseURL: "https://x.test"}, Actions: []Action{{Type: "http", Name: "a", Method: "GET", Target: "/", Extractors: []Extractor{
		{Name: "ok", Source: "body", Type: "regex", Expression: "(x)", DFEs: []string{"base64", "JSON_TO_XML"}},
		{Name: "bad", Source: "body", Type: "regex", Expression: "(x)", DFEs: []string{"gwt"}},
	}}}}
	issues := ValidateScriptIssues(script)
	if !hasIssue(issues, `extractors[1] dfes has unsupported transform "gwt"`) || hasIssue(issues, "extractors[0]") {
		t.Fatalf("issues = %v", issues)
	}
}
