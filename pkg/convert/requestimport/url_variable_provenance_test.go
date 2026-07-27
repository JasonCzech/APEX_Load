package requestimport

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestFinalizeScriptPreservesSensitiveURLVariableOnlyAfterExtractor(t *testing.T) {
	t.Parallel()

	script := &dsl.Script{
		Name:          "correlated-url",
		Version:       "1.0.0",
		Protocol:      "http",
		GeneratedFrom: "har",
		Config:        dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{
				Name:   "bootstrap",
				Type:   "GET",
				Method: "GET",
				Target: "/bootstrap",
				Extractors: []dsl.Extractor{{
					Name:       "access_token",
					Type:       "jsonpath",
					Expression: "$.access_token",
				}},
			},
			{
				Name:   "use token",
				Type:   "GET",
				Method: "GET",
				Target: "/orders?access_token={{var.access_token}}",
			},
		},
	}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if got := result.Script.Actions[1].Target; got != "/orders?access_token={{var.access_token}}" {
		t.Fatalf("correlated target = %q, want proven response variable preserved", got)
	}
	for _, requirement := range result.Requirements {
		if strings.Contains(requirement.Replacement, "ACCESS_TOKEN") {
			t.Fatalf("proven response variable created credential requirement: %#v", requirement)
		}
	}
}

func TestFinalizeScriptGovernsSensitiveURLVariableBeforeExtractor(t *testing.T) {
	t.Parallel()

	script := &dsl.Script{
		Name:          "unproven-url",
		Version:       "1.0.0",
		Protocol:      "http",
		GeneratedFrom: "har",
		Config:        dsl.ScriptConfig{BaseURL: "https://example.test"},
		Actions: []dsl.Action{
			{
				Name:   "use token too early",
				Type:   "GET",
				Method: "GET",
				Target: "/orders?access_token={{var.access_token}}",
			},
			{
				Name:   "bootstrap",
				Type:   "GET",
				Method: "GET",
				Target: "/bootstrap",
				Extractors: []dsl.Extractor{{
					Name:       "access_token",
					Type:       "jsonpath",
					Expression: "$.access_token",
				}},
			},
		},
	}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if got := result.Script.Actions[0].Target; !strings.Contains(got, "{{secret.") {
		t.Fatalf("unproven target = %q, want governed secret reference", got)
	}
	if len(result.Requirements) == 0 {
		t.Fatal("unproven credential variable created no requirement")
	}
}

func TestFinalizeScriptDoesNotTrustStaticParameterAsURLCredentialProvenance(t *testing.T) {
	t.Parallel()

	script := &dsl.Script{
		Name:          "static-parameter-url",
		Version:       "1.0.0",
		Protocol:      "http",
		GeneratedFrom: "har",
		Config:        dsl.ScriptConfig{BaseURL: "https://example.test"},
		Parameters: []dsl.Parameter{{
			Name:   "access_token",
			Type:   "list",
			Values: []string{"static-token-value"},
		}},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "GET",
			Method: "GET",
			Target: "/orders?access_token={{var.access_token}}",
		}},
	}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if got := result.Script.Actions[0].Target; !strings.Contains(got, "{{secret.") {
		t.Fatalf("static parameter target = %q, want governed secret reference", got)
	}
	if len(result.Requirements) != 1 {
		t.Fatalf("requirements = %#v, want static parameter credential binding", result.Requirements)
	}
}

func TestFinalizeScriptPreservesPriorOAuthOutputAsURLCredentialProvenance(t *testing.T) {
	t.Parallel()

	script := &dsl.Script{
		Name:          "oauth-output-url",
		Version:       "1.0.0",
		Protocol:      "http",
		GeneratedFrom: "openapi",
		Config:        dsl.ScriptConfig{BaseURL: "https://example.test"},
		Parameters: []dsl.Parameter{{
			Name:   "access_token",
			Type:   "list",
			Values: []string{"shadowed-static-value"},
		}},
		Init: []dsl.Action{{
			Name: "token",
			Type: "http_oauth2_token",
			Params: map[string]any{
				"token_url":     "https://auth.example.test/token",
				"grant_type":    "client_credentials",
				"client_id":     "{{env.CLIENT_ID}}",
				"client_secret": "{{secret.CLIENT_SECRET}}",
				"save_as":       "access_token",
			},
		}},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "GET",
			Method: "GET",
			Target: "/orders?access_token={{var.access_token}}",
		}},
	}

	result, err := FinalizeScript(script, nil, FinalizeOptions{})
	if err != nil {
		t.Fatalf("FinalizeScript: %v", err)
	}
	if got := result.Script.Actions[0].Target; got != "/orders?access_token={{var.access_token}}" {
		t.Fatalf("OAuth-derived target = %q, want runtime variable preserved", got)
	}
	for _, requirement := range result.Requirements {
		if strings.Contains(requirement.Replacement, "ACCESS_TOKEN") {
			t.Fatalf("OAuth-derived variable created credential requirement: %#v", requirement)
		}
	}
}
