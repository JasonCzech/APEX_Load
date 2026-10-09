package dsl

import (
	"strings"
	"testing"
)

func TestCEscapedBodyValidation(t *testing.T) {
	script := func(body string) *Script {
		return &Script{Name: "s", Protocol: "http", Config: ScriptConfig{BaseURL: "https://x.test"}, Actions: []Action{{Type: "http", Name: "a", Method: "POST", Target: "/", Body: body, BodyEncoding: "c_escaped"}}}
	}
	if issues := ValidateScriptIssues(script(`\x00{{var.path}}\\`)); len(issues) != 0 {
		t.Fatalf("valid escapes with a template were rejected: %v", issues)
	}
	// A template's value is sent verbatim, so a backslash inside the
	// expression is not an escape.
	if issues := ValidateScriptIssues(script(`{{var.a\b}}`)); hasIssue(issues, "invalid escape") {
		t.Fatalf("template contents were validated as escapes: %v", issues)
	}
	if issues := ValidateScriptIssues(script(`\q`)); !hasIssue(issues, "body_encoding c_escaped body has an invalid escape") {
		t.Fatalf("an invalid escape was accepted: %v", issues)
	}
}

func hasIssue(issues []string, text string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, text) {
			return true
		}
	}
	return false
}
