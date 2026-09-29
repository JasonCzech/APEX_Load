package dsl

import (
	"reflect"
	"strings"
	"testing"
)

func TestValidateRunIdentityTemplateNamespace(t *testing.T) {
	base := func(value string) *Script {
		return &Script{
			Name: "run identity", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{Name: "request", Type: "request", Method: "GET", Target: "/",
				Headers: map[string]string{"X-Test": value}}},
		}
	}
	for _, value := range []string{"{{run.id}}", "{{ run.id }}"} {
		if issues := ValidateScriptIssues(base(value)); len(issues) != 0 {
			t.Fatalf("valid template %q: %v", value, issues)
		}
	}
	for _, value := range []string{"{{run.name}}", "{{run.}}", "{{ run.ID }}"} {
		if issues := ValidateScriptIssues(base(value)); !strings.Contains(strings.Join(issues, "; "), "only {{run.id}}") {
			t.Fatalf("unknown template %q accepted: %v", value, issues)
		}
	}
	// The validator walks nested controls and protocol-specific parameter maps.
	nested := base("literal")
	nested.Actions[0].Params = map[string]any{"nested": []any{map[string]any{"value": "{{run.unknown}}"}}}
	if issues := ValidateScriptIssues(nested); !strings.Contains(strings.Join(issues, "; "), "only {{run.id}}") {
		t.Fatalf("nested template accepted: %v", issues)
	}
	cyclic := map[string]any{}
	cyclic["self"] = cyclic
	if got := inspectRunIdentityTemplates(reflect.ValueOf(cyclic), make(map[runTemplateVisit]struct{}), 0); got != runTemplateValid {
		t.Fatal("cycle without an invalid run template was rejected")
	}
	cyclic["value"] = "{{run.unknown}}"
	if got := inspectRunIdentityTemplates(reflect.ValueOf(cyclic), make(map[runTemplateVisit]struct{}), 0); got != runTemplateUnsupported {
		t.Fatal("invalid run template behind a cycle was accepted")
	}
	deep := any("literal")
	for range 130 {
		deep = []any{deep}
	}
	if got := inspectRunIdentityTemplates(reflect.ValueOf(deep), make(map[runTemplateVisit]struct{}), 0); got != runTemplateDepthExceeded {
		t.Fatalf("deep template container = %v, want depth limit", got)
	}
}

func TestRunIdentityTemplateOnlyAllowedWhereRuntimeInterpolates(t *testing.T) {
	base := func() *Script {
		return &Script{
			Name: "run identity", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
		}
	}
	for _, tc := range []struct {
		name   string
		change func(*Script)
	}{
		{"base URL", func(s *Script) { s.Config.BaseURL = "https://example.test/{{run.id}}" }},
		{"typed user agent", func(s *Script) { s.Config.HTTP.UserAgent = "agent-{{run.id}}" }},
		{"websocket origin", func(s *Script) { s.Config.WebSocket.Origin = "https://{{run.id}}.example.test" }},
		{"grpc metadata", func(s *Script) { s.Config.GRPC.Metadata = map[string]string{"x-test": "{{run.id}}"} }},
		{"script metadata", func(s *Script) { s.Name = "run-{{run.id}}" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.change(s)
			issues := ValidateScriptIssues(s)
			if !strings.Contains(strings.Join(issues, "; "), "runtime-interpolated action values") {
				t.Fatalf("unresolved config placeholder accepted: %v", issues)
			}
		})
	}
	for _, tc := range []struct {
		name   string
		change func(*Script)
	}{
		{"default header map", func(s *Script) { s.Config.HTTP.DefaultHeaders = map[string]string{"X-Test": "{{run.id}}"} }},
		{"default header list", func(s *Script) { s.Config.HTTP.DefaultHeaderList = HeaderList{{Name: "X-Test", Value: "{{run.id}}"}} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.change(s)
			if issues := ValidateScriptIssues(s); len(issues) != 0 {
				t.Fatalf("interpolated default header rejected: %v", issues)
			}
		})
	}
	implicit := base()
	implicit.Protocol = "" // full script validation rejects this, but the VUser resolver treats it as HTTP.
	implicit.Config.HTTP.DefaultHeaders = map[string]string{"X-Test": "{{run.id}}"}
	if issues := validateRunIdentityTemplateIssues(implicit); len(issues) != 0 {
		t.Fatalf("implicit HTTP resolver header scope was rejected: %v", issues)
	}
}

func TestRunIdentityTemplateRejectsInertActionFieldsAndKeys(t *testing.T) {
	base := func() *Script {
		return &Script{Name: "run identity", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"},
			Actions: []Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}}}
	}
	for _, tc := range []struct {
		name   string
		change func(*Script)
	}{
		{"action name", func(s *Script) { s.Actions[0].Name = "request-{{run.id}}" }},
		{"action type", func(s *Script) { s.Actions[0].Type = "{{run.id}}" }},
		{"action method", func(s *Script) { s.Actions[0].Method = "{{run.id}}" }},
		{"object reference", func(s *Script) { s.Actions[0].ObjectRef = "object-{{run.id}}" }},
		{"body file", func(s *Script) { s.Actions[0].BodyFile = "{{run.id}}.json" }},
		{"action header map key", func(s *Script) { s.Actions[0].Headers = map[string]string{"X-{{run.id}}": "literal"} }},
		{"action header list name", func(s *Script) { s.Actions[0].HeaderList = HeaderList{{Name: "X-{{run.id}}", Value: "literal"}} }},
		{"default header map key", func(s *Script) { s.Config.HTTP.DefaultHeaders = map[string]string{"X-{{run.id}}": "literal"} }},
		{"default header list name", func(s *Script) {
			s.Config.HTTP.DefaultHeaderList = HeaderList{{Name: "X-{{run.id}}", Value: "literal"}}
		}},
		{"params map key", func(s *Script) { s.Actions[0].Params = map[string]any{"{{run.id}}": "literal"} }},
		{"nested params map key", func(s *Script) {
			s.Actions[0].Params = map[string]any{"outer": map[string]any{"{{run.id}}": "literal"}}
		}},
		{"typed params value not interpolated", func(s *Script) {
			s.Actions[0].Params = map[string]any{"headers": map[string]string{"X-Test": "{{run.id}}"}}
		}},
		{"metadata map key", func(s *Script) { s.Actions[0].Metadata = map[string]any{"{{run.id}}": "literal"} }},
		{"auth type", func(s *Script) { s.Actions[0].Auth = &AuthConfig{Type: "{{run.id}}"} }},
		{"extractor identity", func(s *Script) {
			s.Actions[0].Extractors = []Extractor{{Name: "{{run.id}}", Source: "body", Type: "regex", Expression: "x"}}
		}},
		{"condition expression", func(s *Script) {
			s.Actions = []Action{{Name: "branch", Type: "control_if", Control: &ControlFlow{Condition: &Condition{Expr: "{{run.id}} == 'x'"}, Steps: s.Actions}}}
		}},
		{"condition operand ignored by expression", func(s *Script) {
			s.Actions = []Action{{Name: "branch", Type: "control_if", Control: &ControlFlow{Condition: &Condition{Expr: "true", Left: "{{run.id}}"}, Steps: s.Actions}}}
		}},
		{"nested action identity", func(s *Script) {
			s.Actions = []Action{{Name: "branch", Type: "control_if", Control: &ControlFlow{
				Condition: &Condition{Left: "x", Right: "x"},
				Steps:     []Action{{Name: "{{run.id}}", Type: "request", Method: "GET", Target: "/"}},
			}}}
		}},
		{"sequence child identity", func(s *Script) {
			s.Actions = []Action{{Name: "sequence", Type: "http_sequence", Params: map[string]any{
				"requests": []any{map[string]any{"name": "{{run.id}}", "type": "request", "method": "GET", "target": "/"}},
			}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := base()
			tc.change(s)
			if issues := ValidateScriptIssues(s); !strings.Contains(strings.Join(issues, "; "), "runtime-interpolated action values") {
				t.Fatalf("unresolved run identity accepted: %v", issues)
			}
		})
	}
}

func TestRunIdentityTemplateAcceptsOnlyInterpolatedNestedValues(t *testing.T) {
	s := &Script{Name: "run identity", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{Name: "branch", Type: "control_if", Control: &ControlFlow{
			Condition: &Condition{Left: "{{run.id}}", Op: "eq", Right: "expected"},
			Steps: []Action{{Name: "request", Type: "request", Method: "GET", Target: "/{{run.id}}",
				Headers: map[string]string{"X-Run": "{{run.id}}"}, Params: map[string]any{"nested": []any{map[string]any{"value": "{{run.id}}"}}}}},
		}}}}
	if issues := ValidateScriptIssues(s); len(issues) != 0 {
		t.Fatalf("interpolated nested values were rejected: %v", issues)
	}
	s.Actions = []Action{{Name: "sequence", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{
		"name": "child", "type": "request", "method": "GET", "target": "/{{run.id}}", "headers": map[string]any{"X-Run": "{{run.id}}"},
	}}}}}
	if issues := validateRunIdentityTemplateIssues(s); len(issues) != 0 {
		t.Fatalf("interpolated HTTP sequence child values were rejected: %v", issues)
	}
}
