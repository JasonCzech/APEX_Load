package dsl

import "testing"

func TestExtractorScopeValidation(t *testing.T) {
	script := func(ext Extractor) *Script {
		return &Script{Name: "s", Protocol: "http", Config: ScriptConfig{BaseURL: "https://x.test"}, Actions: []Action{{Type: "http", Name: "a", Method: "GET", Target: "/", Extractors: []Extractor{ext}}}}
	}
	valid := Extractor{Name: "v", Source: "header", Type: "boundary", Expression: "a|||b", Scope: &ExtractorScope{Redirects: "include", Resources: "exclude", RequestURL: "*token*", ContentType: "text/*", HeaderNames: []string{"Location"}}}
	if issues := ValidateScriptIssues(script(valid)); len(issues) != 0 {
		t.Fatalf("valid scope rejected: %v", issues)
	}
	invalid := Extractor{Name: "v", Source: "body", Type: "regex", Expression: "(x)", Scope: &ExtractorScope{Redirects: "always", RequestURL: "[bad", HeaderNames: []string{"Bad Name"}}}
	issues := ValidateScriptIssues(script(invalid))
	for _, want := range []string{"scope.redirects must be", "scope.request_url is not a valid wildcard pattern", "scope.header_names applies only to the header, all or cookies sources", `scope.header_names has an invalid header name "Bad Name"`} {
		if !hasIssue(issues, want) {
			t.Errorf("missing %q in %v", want, issues)
		}
	}
}
