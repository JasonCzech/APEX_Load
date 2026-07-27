package api

import (
	"fmt"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestMaskScriptSecretsPortableTemplatePlaceholdersCannotCollide(t *testing.T) {
	tests := []struct {
		name   string
		body   string
		target string
	}{
		{
			name:   "free text template marker",
			body:   "literal apexbindingref0 before {{param.order_id}}",
			target: "/submit",
		},
		{
			name:   "query binding marker in form body",
			body:   "literal=apexsafe0=apexref0&authorization={{secret.API_TOKEN}}",
			target: "/submit",
		},
		{
			name:   "query binding marker in URL path",
			body:   "{{param.payload}}",
			target: "https://safe.example.test/path/apexsafe0=apexref0?authorization={{secret.API_TOKEN}}",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{
				Name:     "Placeholder collision",
				Protocol: "http",
				Actions: []dsl.Action{{
					Name:   "request",
					Type:   "request",
					Method: "POST",
					Target: test.target,
					Body:   test.body,
				}},
			}

			projected := maskScriptSecrets(script)
			if projected == nil || len(projected.Actions) != 1 {
				t.Fatalf("projected script = %#v, want one action", projected)
			}
			if got := projected.Actions[0].Body; got != test.body {
				t.Errorf("projected body = %q, want exact input %q", got, test.body)
			}
			if got := projected.Actions[0].Target; got != test.target {
				t.Errorf("projected target = %q, want exact input %q", got, test.target)
			}
			if requirements := discoverSecretBindingRequirements(projected, 0); len(requirements) != 0 {
				t.Fatalf("portable references produced requirements: %#v", requirements)
			}
		})
	}
}

func TestMaskScriptSecretsXMLPlaceholdersRemainDistinctPastTenBindings(t *testing.T) {
	var body strings.Builder
	body.WriteString(`<root marker="apexxmlref0">`)
	for index := 0; index < 12; index++ {
		fmt.Fprintf(&body, `<password>{{secret.TOKEN_%02d}}</password>`, index)
	}
	body.WriteString(`</root>`)
	input := body.String()

	protected, replacements := protectPortableXMLSecretBindings(input)
	if len(replacements) != 12 {
		t.Fatalf("protected XML bindings = %d, want 12; protected=%q", len(replacements), protected)
	}
	placeholders := make([]string, 0, len(replacements))
	for placeholder := range replacements {
		if strings.Contains(input, placeholder) {
			t.Fatalf("placeholder %q collides with literal input", placeholder)
		}
		placeholders = append(placeholders, placeholder)
	}
	for left := range placeholders {
		for right := range placeholders {
			if left != right && strings.HasPrefix(placeholders[left], placeholders[right]) {
				t.Fatalf("placeholder %q has ambiguous prefix %q", placeholders[left], placeholders[right])
			}
		}
	}

	script := &dsl.Script{
		Name:     "XML placeholder collision",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: "POST",
			Target: "/submit",
			Body:   input,
		}},
	}
	projected := maskScriptSecrets(script)
	if projected == nil || len(projected.Actions) != 1 {
		t.Fatalf("projected script = %#v, want one action", projected)
	}
	if got := projected.Actions[0].Body; got != input {
		t.Errorf("projected XML body = %q, want exact input %q", got, input)
	}
	if requirements := discoverSecretBindingRequirements(projected, 0); len(requirements) != 0 {
		t.Fatalf("portable XML secret references produced requirements: %#v", requirements)
	}
}

func TestMaskScriptSecretsRepeatedURLSeparatorsPreserveBindingsAndScrubCredentials(t *testing.T) {
	const target = "https://safe.example.test/path?first={{secret.FIRST}}?token=short#next={{param.order_id}}?password=short"
	script := &dsl.Script{
		Name:     "Repeated URL separators",
		Protocol: "http",
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "request",
			Method: "GET",
			Target: target,
		}},
	}

	projected := maskScriptSecrets(script)
	if projected == nil || len(projected.Actions) != 1 {
		t.Fatalf("projected script = %#v, want one action", projected)
	}
	const want = "https://safe.example.test/path?first={{secret.FIRST}}?token=[REDACTED]#next={{param.order_id}}?password=[REDACTED]"
	if got := projected.Actions[0].Target; got != want {
		t.Fatalf("projected target = %q, want %q", got, want)
	}
	if requirements := discoverSecretBindingRequirements(projected, 0); len(requirements) != 2 {
		t.Fatalf("credential binding requirements = %#v, want token and password only", requirements)
	}
}
