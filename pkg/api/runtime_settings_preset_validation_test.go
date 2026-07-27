package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestRuntimeSettingsProfilePresetsProduceValidScripts pins every shipped
// runtime-settings preset against the DSL validator.
//
// Pacing, run-logic, and think-time resource validation now runs for every
// protocol rather than only the messaging families, and validateScript is a
// hard 400 on script create/update as well as on the stored-script
// revalidation that happens when a managed test is launched. A preset whose
// pacing or think-time vocabulary drifts out of the allowlist would therefore
// break saving any script that applied it — including presets that use
// think_time.type "recorded", which only recently joined the allowlist.
func TestRuntimeSettingsProfilePresetsProduceValidScripts(t *testing.T) {
	presets := runtimeSettingsProfilePresets()
	if len(presets) == 0 {
		t.Fatal("runtimeSettingsProfilePresets returned no presets")
	}

	for _, preset := range presets {
		t.Run(preset.ID, func(t *testing.T) {
			overrides := preset.RuntimeSettings
			if overrides == nil {
				return
			}
			script := presetValidationScript()
			if overrides.Pacing != nil {
				script.Config.Pacing = *overrides.Pacing
			}
			if overrides.ThinkTime != nil {
				script.Config.ThinkTime = *overrides.ThinkTime
			}
			if overrides.RunLogic != nil {
				script.Config.RunLogic = *overrides.RunLogic
			}
			if overrides.ErrorHandling != nil {
				script.Config.ErrorHandling = *overrides.ErrorHandling
			}
			if overrides.Log != nil {
				script.Config.Log = *overrides.Log
			}
			if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
				t.Fatalf("preset %q produced an invalid script: %v", preset.ID, issues)
			}
		})
	}
}

// TestAuthoringTemplateThinkTimeVocabularyIsValid covers the other producer of
// think_time.type outside the importers: the authoring template catalog.
func TestAuthoringTemplateThinkTimeVocabularyIsValid(t *testing.T) {
	seen := map[string]struct{}{}
	for _, template := range authoringTemplateCatalog() {
		thinkTime := template.Script.Config.ThinkTime
		if thinkTime.Type == "" {
			continue
		}
		if _, done := seen[thinkTime.Type]; done {
			continue
		}
		seen[thinkTime.Type] = struct{}{}
		script := presetValidationScript()
		script.Config.ThinkTime = thinkTime
		if issues := dsl.ValidateScriptIssues(script); len(issues) != 0 {
			t.Fatalf("authoring template think_time.type %q is rejected by the validator: %v", thinkTime.Type, issues)
		}
	}
	if len(seen) == 0 {
		t.Fatal("no authoring template declared a think_time type")
	}
}

func presetValidationScript() *dsl.Script {
	return &dsl.Script{
		Name:     "runtime settings preset",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: "https://api.example.test",
		},
		Actions: []dsl.Action{{
			Name:   "request",
			Type:   "GET",
			Method: "GET",
			Target: "/orders",
		}},
	}
}
