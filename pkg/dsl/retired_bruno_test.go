package dsl

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRetiredBrunoValidationExplainsLegacyScripts(t *testing.T) {
	for _, script := range []*Script{
		{Name: "Legacy", Protocol: "bruno", Actions: []Action{{Name: "run", Type: "bruno_run"}}},
		{Name: "Nested", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"}, Actions: []Action{{Name: "loop", Type: "control_loop", Control: &ControlFlow{Count: 1, Steps: []Action{{Name: "run", Type: "bruno_run"}}}}}},
		{Name: "Sequence", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"}, Actions: []Action{{Name: "requests", Type: "http_sequence", Params: map[string]any{"requests": []any{map[string]any{"name": "legacy", "type": "bruno_run", "method": "GET", "target": "/health"}}}}}},
	} {
		if issues := strings.Join(ValidateScriptIssues(script), "\n"); !strings.Contains(issues, BrunoRunnerRetiredMessage) {
			t.Fatalf("issues = %s", issues)
		}
	}
	if _, exists := SpecForActionType("bruno_run"); exists {
		t.Fatal("retired runner action remains in the authoring catalog")
	}
}

func TestRetiredBrunoConfigIsOmittedWhenUnusedAndPreservedWhenPopulated(t *testing.T) {
	for name, config := range map[string]ScriptConfig{
		"native": {BaseURL: "https://example.test"},
		"legacy": {Bruno: BrunoConfig{BundleRef: "artifact://retained", SecretBindings: map[string]string{"TOKEN": "saved-value"}}},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(config)
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &fields); err != nil {
				t.Fatal(err)
			}
			_, present := fields["bruno"]
			if present != (name == "legacy") {
				t.Fatalf("legacy config presence=%v in %s", present, encoded)
			}
			var decoded ScriptConfig
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatal(err)
			}
			if decoded.Bruno.BundleRef != config.Bruno.BundleRef || decoded.Bruno.SecretBindings["TOKEN"] != config.Bruno.SecretBindings["TOKEN"] {
				t.Fatal("saved Bruno config changed after JSON round trip")
			}
		})
	}
}
