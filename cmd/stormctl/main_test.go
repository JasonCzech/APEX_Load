package main

import (
	"testing"

	"github.com/jasonczech/stormrunner/pkg/dsl"
)

func TestParseRunArgs_RuntimeOverrides(t *testing.T) {
	args := parseRunArgs([]string{
		"--script", "example.json",
		"-env", "target_namespace=pr-123",
		"--env", "api_host=preview.example.com",
		"--base-url", "https://preview.example.com",
	})

	if args.script != "example.json" {
		t.Fatalf("script = %q, want example.json", args.script)
	}
	if args.baseURL != "https://preview.example.com" {
		t.Fatalf("baseURL = %q, want https://preview.example.com", args.baseURL)
	}
	if args.env["target_namespace"] != "pr-123" {
		t.Fatalf("target_namespace = %q, want pr-123", args.env["target_namespace"])
	}
	if args.env["api_host"] != "preview.example.com" {
		t.Fatalf("api_host = %q, want preview.example.com", args.env["api_host"])
	}
}

func TestApplyRuntimeOverrides_ReplacesEnvPlaceholdersAndBaseURL(t *testing.T) {
	script := &dsl.Script{
		Name:     "preview",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://{{env.api_host}}",
			HTTP: dsl.HTTPConfig{
				DefaultHeaders: map[string]string{"X-Namespace": "{{env.target_namespace}}"},
			},
		},
		Actions: []dsl.Action{{
			Name:   "open",
			Type:   "request",
			Target: "/namespaces/{{env.target_namespace}}/health",
			Body:   `{"host":"{{env.api_host}}"}`,
		}},
	}

	overridden, err := applyRuntimeOverrides(script, runArgs{
		baseURL: "https://override.example.com",
		env: map[string]string{
			"target_namespace": "pr-123",
			"api_host":         "preview.example.com",
		},
	})
	if err != nil {
		t.Fatalf("applyRuntimeOverrides returned error: %v", err)
	}

	if overridden.Config.BaseURL != "https://override.example.com" {
		t.Fatalf("BaseURL = %q, want https://override.example.com", overridden.Config.BaseURL)
	}
	if overridden.Actions[0].Target != "/namespaces/pr-123/health" {
		t.Fatalf("Target = %q, want /namespaces/pr-123/health", overridden.Actions[0].Target)
	}
	if overridden.Actions[0].Body != `{"host":"preview.example.com"}` {
		t.Fatalf("Body = %q, want host replacement", overridden.Actions[0].Body)
	}
	if overridden.Config.HTTP.DefaultHeaders["X-Namespace"] != "pr-123" {
		t.Fatalf("X-Namespace = %q, want pr-123", overridden.Config.HTTP.DefaultHeaders["X-Namespace"])
	}
	if script.Actions[0].Target != "/namespaces/{{env.target_namespace}}/health" {
		t.Fatalf("original Target mutated to %q", script.Actions[0].Target)
	}
}

func TestParseEnvAssignment(t *testing.T) {
	key, value, ok := parseEnvAssignment("target_namespace=pr-123")
	if !ok {
		t.Fatal("parseEnvAssignment returned ok=false, want true")
	}
	if key != "target_namespace" || value != "pr-123" {
		t.Fatalf("parseEnvAssignment = (%q, %q), want (target_namespace, pr-123)", key, value)
	}
	if _, _, ok := parseEnvAssignment("missing-separator"); ok {
		t.Fatal("parseEnvAssignment accepted invalid input")
	}
}
