package api

import (
	"encoding/json"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Apply-to-groups from a partial source converts a v1 target row (no
// override_fields) to partial semantics. The converted row must keep applying
// every field that was not copied exactly as the v1 row did: v1 lets a section
// win over its compact alias (log over log_level, run_logic over
// iterations/auto_transactions, browser_config over the compact browser
// override), so those shadowed aliases must not start applying.
func TestReplaceRuntimeFieldsV1TargetFromPartialSourceKeepsV1Semantics(t *testing.T) {
	seven := 7
	headless := true
	prior := func() *RuntimeSettingsOverride {
		return &RuntimeSettingsOverride{
			Log:              &dsl.LogConfig{OnErrorOnly: true},
			LogLevel:         "debug",
			RunLogic:         &dsl.RunLogicConfig{WorkloadModel: "closed"},
			Iterations:       &seven,
			AutoTransactions: "per_action",
			BrowserConfig:    &dsl.BrowserConfig{DeviceProfile: "desktop"},
			Browser:          &RuntimeBrowserOverride{Headless: &headless, Orientation: "landscape"},
		}
	}
	base := func() *dsl.Script {
		s := httpScript("alias", "alias")
		s.Config.Log.Level = "info"
		s.Config.RunLogic.Iterations = 3
		s.Config.RunLogic.AutoTransactions = "none"
		s.Config.Browser.Orientation = "portrait"
		return s
	}
	for _, tc := range []struct {
		name  string
		field string
		src   *RuntimeSettingsOverride
		clear func(*dsl.ScriptConfig)
	}{
		{
			name:  "pacing",
			field: "pacing",
			src:   &RuntimeSettingsOverride{OverrideFields: []string{"pacing"}, Pacing: &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100}},
			clear: func(c *dsl.ScriptConfig) { c.Pacing = dsl.PacingConfig{} },
		},
		{
			name:  "think_time",
			field: "think_time",
			src:   &RuntimeSettingsOverride{OverrideFields: []string{"think_time"}, ThinkTime: &dsl.ThinkTimeConfig{Type: "ignore"}},
			clear: func(c *dsl.ScriptConfig) { c.ThinkTime = dsl.ThinkTimeConfig{} },
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := prior()
			next := replaceRuntimeFields(target, tc.src, []string{tc.field})
			if next == nil || len(next.OverrideFields) == 0 {
				t.Fatalf("precondition: expected conversion to partial, got %#v", next)
			}
			if target.LogLevel != "debug" || target.Iterations == nil || target.Browser == nil {
				t.Fatalf("the caller's target row must not be modified: %#v", target)
			}
			legacy, converted := base(), base()
			if err := applyRuntimeOverrides(legacy, prior()); err != nil {
				t.Fatal(err)
			}
			if err := applyRuntimeOverrides(converted, next); err != nil {
				t.Fatal(err)
			}
			tc.clear(&legacy.Config)
			tc.clear(&converted.Config)
			want, _ := json.Marshal(legacy.Config)
			got, _ := json.Marshal(converted.Config)
			if string(want) != string(got) {
				t.Fatalf("fields=%v\n v1:        %s\n converted: %s", next.OverrideFields, want, got)
			}
		})
	}
}
