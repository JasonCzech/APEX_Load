package sharedvalue

import (
	"errors"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// gatedScript references subscriberNumber, which the source never supplied,
// in a header, the query and the body, with the gate an importer stamps.
func gatedScript(baseURL string) *dsl.Script {
	return &dsl.Script{
		Name: "Inquire", Version: "1.0", Protocol: "http",
		Config: dsl.ScriptConfig{BaseURL: baseURL},
		Actions: []dsl.Action{{
			Name: "inquire", Type: "http_request", Method: "POST",
			Target:     "/accounts?subscriber={{var.subscriberNumber}}",
			HeaderList: dsl.HeaderList{{Name: "X-Subscriber", Value: "{{ var.subscriberNumber }}"}},
			Body:       "<subscriber>{{var.subscriberNumber}}</subscriber>",
			Metadata: map[string]any{
				"missing_variables": []any{"subscriberNumber"},
				"execution_blocker": "bruno_missing_variables",
			},
		}},
	}
}

func subscriberCandidate(t *testing.T, script *dsl.Script) Candidate {
	t.Helper()
	result := Detect([]*dsl.Script{script})
	for _, candidate := range result.Candidates {
		if candidate.Name == "subscriberNumber" {
			return candidate
		}
	}
	t.Fatalf("no candidate for subscriberNumber: %+v", result.Candidates)
	return Candidate{}
}

func TestBindReadsADataPoolColumnPerVirtualUser(t *testing.T) {
	script := gatedScript("https://example.test")
	candidate := subscriberCandidate(t, script)
	if candidate.Kind != KindSourceVariable {
		t.Fatalf("candidate kind = %q", candidate.Kind)
	}
	if _, err := Bind(script, candidate, Binding{Mode: BindingData, Alias: "subscribers", Column: "number"}); err != nil {
		t.Fatal(err)
	}
	action := script.Actions[0]
	for name, value := range map[string]string{"target": action.Target, "header": action.HeaderList[0].Value, "body": action.Body} {
		if !strings.Contains(value, "{{data.subscribers.number}}") || strings.Contains(value, "var.subscriberNumber") {
			t.Errorf("%s = %q, want the data column", name, value)
		}
	}
	if missing := requestimport.MissingVariables(script, nil); len(missing) != 0 {
		t.Errorf("still missing: %v", missing)
	}
	if _, gated := action.Metadata["missing_variables"]; gated {
		t.Errorf("the missing-variable gate stayed: %+v", action.Metadata)
	}
	if len(script.Parameters) != 0 {
		t.Errorf("a data binding defined parameters: %+v", script.Parameters)
	}
}

func TestBindGeneratesAValueEachIteration(t *testing.T) {
	for generator, want := range map[string]dsl.Parameter{
		GeneratorUUID:      {Name: "subscriberNumber", Type: "uuid", Update: "each_iteration"},
		GeneratorTimestamp: {Name: "subscriberNumber", Type: "timestamp", Format: "iso8601", Update: "each_iteration"},
		GeneratorDigits:    {Name: "subscriberNumber", Type: "random_string", Length: 10, Charset: "0123456789", Update: "each_iteration"},
	} {
		script := gatedScript("https://example.test")
		candidate := subscriberCandidate(t, script)
		if _, err := Bind(script, candidate, Binding{Mode: BindingGenerated, Generator: generator, Digits: 10}); err != nil {
			t.Fatalf("%s: %v", generator, err)
		}
		if len(script.Parameters) != 1 {
			t.Fatalf("%s parameters = %+v", generator, script.Parameters)
		}
		got := script.Parameters[0]
		if got.Name != want.Name || got.Type != want.Type || got.Format != want.Format || got.Update != want.Update || got.Length != want.Length || got.Charset != want.Charset {
			t.Errorf("%s parameter = %+v, want %+v", generator, got, want)
		}
		if !strings.Contains(script.Actions[0].Body, "{{var.subscriberNumber}}") {
			t.Errorf("%s rewrote the reference: %s", generator, script.Actions[0].Body)
		}
		if missing := requestimport.MissingVariables(script, nil); len(missing) != 0 {
			t.Errorf("%s still missing: %v", generator, missing)
		}
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Errorf("%s script invalid: %v", generator, issues)
		}
	}
}

// The base URL is resolved once before any virtual user exists, so neither a
// data column nor a per-iteration value can go there.
func TestBindRefusesAVariableInTheBaseURL(t *testing.T) {
	for _, binding := range []Binding{
		{Mode: BindingData, Alias: "subscribers", Column: "number"},
		{Mode: BindingGenerated, Generator: GeneratorUUID},
	} {
		script := gatedScript("https://{{var.subscriberNumber}}.example.test")
		candidate := subscriberCandidate(t, script)
		before := script.Actions[0].Body
		if _, err := Bind(script, candidate, binding); !errors.Is(err, ErrBindingUnsupported) {
			t.Fatalf("%s: err = %v, want ErrBindingUnsupported", binding.Mode, err)
		}
		if script.Actions[0].Body != before || len(script.Parameters) != 0 {
			t.Errorf("%s: a refused binding changed the script", binding.Mode)
		}
	}
}

func TestBindingValidation(t *testing.T) {
	for name, binding := range map[string]Binding{
		"no mode":           {},
		"unknown mode":      {Mode: "value"},
		"data no column":    {Mode: BindingData, Alias: "subscribers"},
		"data bad alias":    {Mode: BindingData, Alias: "sub scribers", Column: "number"},
		"unknown generator": {Mode: BindingGenerated, Generator: "fake_name"},
		"no digits":         {Mode: BindingGenerated, Generator: GeneratorDigits},
		"too many digits":   {Mode: BindingGenerated, Generator: GeneratorDigits, Digits: 65},
	} {
		if err := binding.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, binding)
		}
	}
	if err := (Binding{Mode: BindingData, Alias: "sub-scribers_1", Column: "number"}).Validate(); err != nil {
		t.Errorf("valid data binding refused: %v", err)
	}
}
