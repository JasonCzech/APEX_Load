package dsl

import (
	"fmt"
	"strings"
	"testing"
)

func TestValidateScriptIssuesBoundsExtractorsPerAction(t *testing.T) {
	extractors := make([]Extractor, MaxActionExtractors+1)
	for index := range extractors {
		extractors[index] = Extractor{Name: fmt.Sprintf("value-%d", index), Type: "regex", Expression: "(.*)"}
	}
	script := &Script{Name: "bounded", Protocol: "http", Actions: []Action{{Name: "extract", Type: "http", Method: "GET", Target: "https://example.test", Extractors: extractors}}}
	issues := ValidateScriptIssues(script)
	found := false
	for _, issue := range issues {
		if strings.Contains(issue, fmt.Sprintf("extractors must not exceed %d", MaxActionExtractors)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %v, want extractor limit", issues)
	}
}

func TestValidateScriptIssuesBoundsHTTPAssertionsPerAction(t *testing.T) {
	assertions := make([]Assertion, MaxActionAssertions+1)
	for index := range assertions {
		assertions[index] = Assertion{Type: "status_code", Expected: 200}
	}
	script := &Script{
		Name:     "bounded",
		Protocol: "http",
		Actions: []Action{{
			Name:       "assert",
			Type:       "http",
			Method:     "GET",
			Target:     "https://example.test",
			Assertions: assertions,
		}},
	}
	issues := ValidateScriptIssues(script)
	found := false
	for _, issue := range issues {
		if strings.Contains(issue, fmt.Sprintf("assertions must not exceed %d", MaxActionAssertions)) {
			found = true
		}
	}
	if !found {
		t.Fatalf("issues = %v, want assertion limit", issues)
	}
}

func TestValidateScriptIssuesRejectsDeepControlTreeBeforeRecursiveValidation(t *testing.T) {
	nested := Action{Name: "leaf", Type: "jms_publish", Target: "orders"}
	for depth := 0; depth < MaxScriptActionDepth; depth++ {
		nested = Action{
			Name: "nested",
			Type: "control_if",
			Control: &ControlFlow{
				Condition: &Condition{Left: "true", Op: "eq", Right: "true"},
				Steps:     []Action{nested},
			},
		}
	}
	script := &Script{Name: "deep", Protocol: "jms-solace", Actions: []Action{nested}}
	issues := ValidateScriptIssues(script)
	if len(issues) != 1 || !strings.Contains(issues[0], "nesting depth") {
		t.Fatalf("expected one structural depth issue, got %v", issues)
	}
}

func TestValidateScriptIssuesBoundsDefinitionsAcrossSpecializedProtocols(t *testing.T) {
	protocols := []string{"jms-solace", "kafka", "eventhubs"}
	for _, protocol := range protocols {
		t.Run(protocol, func(t *testing.T) {
			actions := make([]Action, MaxScriptActionDefinitions+1)
			script := &Script{Name: "wide", Protocol: protocol, Actions: actions}
			issues := ValidateScriptIssues(script)
			if len(issues) != 1 || !strings.Contains(issues[0], "definitions") {
				t.Fatalf("expected one structural definition issue, got %v", issues)
			}
		})
	}
}

func TestValidateScriptIssuesCountsHTTPSequenceRequests(t *testing.T) {
	requests := make([]any, MaxScriptActionDefinitions)
	for i := range requests {
		requests[i] = map[string]any{"method": "GET", "target": "/"}
	}
	script := &Script{
		Name:     "sequence",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "requests",
			Type:   "http_sequence",
			Params: map[string]any{"requests": requests},
		}},
	}
	issues := ValidateScriptIssues(script)
	if len(issues) != 1 || !strings.Contains(issues[0], "definitions") {
		t.Fatalf("expected one structural definition issue, got %v", issues)
	}
}

func TestValidateScriptIssuesCountsNormalizedHTTPSequenceRequests(t *testing.T) {
	requests := make([]any, MaxScriptActionDefinitions)
	for i := range requests {
		requests[i] = map[string]any{"method": "GET", "target": "/"}
	}
	script := &Script{
		Name:     "sequence",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "requests",
			Type:   " HTTP_SEQUENCE ",
			Params: map[string]any{"requests": requests},
		}},
	}
	issues := ValidateScriptIssues(script)
	if len(issues) != 1 || !strings.Contains(issues[0], "definitions") {
		t.Fatalf("expected one structural definition issue, got %v", issues)
	}
}

func TestValidateScriptIssuesValidatesNormalizedHTTPSequenceShape(t *testing.T) {
	script := &Script{
		Name:     "sequence",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name:   "requests",
			Type:   " HTTP_SEQUENCE ",
			Target: "/would-pass-generic-request-validation",
			Params: map[string]any{"requests": "not-an-array"},
		}},
	}
	issues := ValidateScriptIssues(script)
	for _, issue := range issues {
		if strings.Contains(issue, "params.requests must be an array") {
			return
		}
	}
	t.Fatalf("issues = %v, want normalized http_sequence shape issue", issues)
}

func TestValidateScriptIssuesAcceptsTypedHTTPSequenceChildren(t *testing.T) {
	script := &Script{
		Name:     "typed sequence",
		Protocol: "http",
		Config:   ScriptConfig{BaseURL: "https://example.test"},
		Actions: []Action{{
			Name: "requests",
			Type: "http_sequence",
			Params: map[string]any{"requests": []Action{{
				Name: "child", Type: "GET", Method: "GET", Target: "/orders",
			}}},
		}},
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("typed http_sequence issues = %v, want none", issues)
	}

	script.Actions[0].Params["requests"] = []any{Action{
		Name: "child", Type: "GET", Method: "GET", Target: "/orders",
	}}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("typed interface child issues = %v, want none", issues)
	}
}
