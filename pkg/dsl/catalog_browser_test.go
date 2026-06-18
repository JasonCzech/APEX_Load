package dsl

import "testing"

func TestBrowserActionObjectRefSatisfiesLocator(t *testing.T) {
	// object_ref alone is a valid locator for an element-targeting action.
	bound := &Script{
		Name:     "bound",
		Protocol: "browser",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []Action{{Name: "click-login", Type: "browser_click", ObjectRef: "login"}},
	}
	if issues := ValidateScriptIssues(bound); len(issues) != 0 {
		t.Fatalf("object_ref-only action should validate, got issues: %v", issues)
	}

	// With no locator at all the action is still rejected.
	bare := &Script{
		Name:     "bare",
		Protocol: "browser",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []Action{{Name: "click-login", Type: "browser_click"}},
	}
	issues := ValidateScriptIssues(bare)
	assertIssue(t, issues, `actions[0] "click-login": missing target or selector`)
}

func TestBrowserNavigateRejectsObjectRefOnlyLocator(t *testing.T) {
	// browser_navigate targets a URL, not a repository element: object_ref must
	// not satisfy its locator requirement.
	script := &Script{
		Name:     "nav",
		Protocol: "browser",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions:  []Action{{Name: "go", Type: "browser_navigate", ObjectRef: "login"}},
	}
	issues := ValidateScriptIssues(script)
	assertIssue(t, issues, `actions[0] "go": missing target or selector`)
}
