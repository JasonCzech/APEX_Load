package dsl

import "testing"

func TestHTMLStepValidation(t *testing.T) {
	script := func(protocol string, step any) *Script {
		return &Script{Name: "s", Protocol: protocol, Config: ScriptConfig{BaseURL: "https://x.test"}, Actions: []Action{{Type: "GET", Name: "a", Params: map[string]any{"html_step": step}}}}
	}
	if issues := ValidateScriptIssues(script("http", map[string]any{"kind": "link", "text": "Next", "ordinal": float64(2)})); len(issues) != 0 {
		t.Fatalf("a link step needs no target: %v", issues)
	}
	// An import that wrote a form with no fields stored null: an empty list.
	if issues := ValidateScriptIssues(script("http", map[string]any{"kind": "form", "fields": nil, "hidden": nil})); len(issues) != 0 {
		t.Fatalf("a form with null field lists: %v", issues)
	}
	for _, tt := range []struct {
		step any
		want string
	}{
		{map[string]any{"kind": "link"}, "link requires text"},
		{map[string]any{"kind": "image"}, "image requires alt or src"},
		{map[string]any{"kind": "form", "fields": "user=ada"}, "fields must be a list"},
		{map[string]any{"kind": "form", "fields": []any{map[string]any{"value": "x"}}}, "fields entries need a name"},
		{map[string]any{"kind": "click"}, "kind must be link, image, image_check or form"},
		{map[string]any{"kind": "link", "text": "x", "ordinal": 1.5}, "ordinal must be a whole number"},
		{map[string]any{"kind": "link", "text": "x", "ordinal": -1}, "ordinal must be a whole number"},
		{map[string]any{"kind": "link", "text": "x", "ordinal": float64(-2)}, "ordinal must be a whole number"},
		{"link", "html_step must be an object"},
	} {
		if issues := ValidateScriptIssues(script("http", tt.step)); !hasIssue(issues, tt.want) {
			t.Errorf("%v: issues = %v, want %q", tt.step, issues, tt.want)
		}
	}
	concurrent := &Script{Name: "s", Protocol: "http", Config: ScriptConfig{BaseURL: "https://x.test"}, Actions: []Action{{Type: "http_sequence", Name: "g", Params: map[string]any{
		"execution_mode": HTTPSequenceConcurrent,
		"requests":       []any{map[string]any{"name": "c", "type": "GET", "target": "#html-step", "params": map[string]any{"html_step": map[string]any{"kind": "link", "text": "x"}}}},
	}}}}
	if issues := ValidateScriptIssues(concurrent); !hasIssue(issues, "cannot run in a concurrent http_sequence") {
		t.Fatalf("concurrent html steps = %v", issues)
	}
}
