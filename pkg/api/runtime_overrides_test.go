package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
)

func TestApplyRuntimeOverridesAppliesExtendedOverrideFields(t *testing.T) {
	headless := false
	touchMode := true
	defaultHeadless := true
	defaultTouchMode := false
	iterations := 4
	script := &dsl.Script{
		Config: dsl.ScriptConfig{
			BaseURL:       "https://default.example.com",
			Browser:       dsl.BrowserConfig{Headless: &defaultHeadless, DeviceProfile: "desktop_1080p", TouchMode: &defaultTouchMode, Orientation: "landscape"},
			RunLogic:      dsl.RunLogicConfig{Iterations: 1, AutoTransactions: "none"},
			Pacing:        dsl.PacingConfig{Type: "none"},
			ThinkTime:     dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 1000},
			ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false, SnapshotOnError: false},
			Log:           dsl.LogConfig{Level: "standard"},
		},
		Actions: []dsl.Action{{
			Name:    "runtime-step",
			Type:    "GET",
			Target:  "/namespaces/{{env.target_namespace}}/orders",
			Body:    "namespace={{env.target_namespace}}",
			Headers: map[string]string{"X-Namespace": "{{env.target_namespace}}"},
		}},
	}

	err := applyRuntimeOverrides(script, &RuntimeSettingsOverride{
		Iterations:       &iterations,
		Pacing:           &dsl.PacingConfig{Type: "fixed_interval", IntervalMs: 2500},
		ThinkTime:        &dsl.ThinkTimeConfig{Type: "fixed", MeanMs: 750},
		Browser:          &RuntimeBrowserOverride{Headless: &headless, DeviceProfile: "iphone_se", TouchMode: &touchMode, Orientation: "portrait"},
		LogLevel:         "extended",
		AutoTransactions: "per_iteration",
		BaseURL:          "https://pr-123.example.com",
		Env:              map[string]string{"target_namespace": "pr-123"},
		ErrorHandling:    &dsl.ErrorHandlingConfig{ContinueOnError: true, SnapshotOnError: true},
	})
	if err != nil {
		t.Fatalf("applyRuntimeOverrides returned error: %v", err)
	}

	if script.Config.RunLogic.Iterations != 4 {
		t.Fatalf("iterations = %d, want 4", script.Config.RunLogic.Iterations)
	}
	if script.Config.BaseURL != "https://pr-123.example.com" {
		t.Fatalf("base_url = %q, want https://pr-123.example.com", script.Config.BaseURL)
	}
	if script.Config.Pacing.Type != "fixed_interval" || script.Config.Pacing.IntervalMs != 2500 {
		t.Fatalf("pacing = %#v, want fixed_interval 2500ms", script.Config.Pacing)
	}
	if script.Config.ThinkTime.Type != "fixed" || script.Config.ThinkTime.MeanMs != 750 {
		t.Fatalf("think_time = %#v, want fixed 750ms", script.Config.ThinkTime)
	}
	if script.Config.Browser.Headless == nil || *script.Config.Browser.Headless != false {
		t.Fatalf("browser.headless = %#v, want false", script.Config.Browser.Headless)
	}
	if script.Config.Browser.DeviceProfile != "iphone_se" {
		t.Fatalf("browser.device_profile = %q, want iphone_se", script.Config.Browser.DeviceProfile)
	}
	if script.Config.Browser.TouchMode == nil || *script.Config.Browser.TouchMode != true {
		t.Fatalf("browser.touch_mode = %#v, want true", script.Config.Browser.TouchMode)
	}
	if script.Config.Browser.Orientation != "portrait" {
		t.Fatalf("browser.orientation = %q, want portrait", script.Config.Browser.Orientation)
	}
	if script.Config.Log.Level != "extended" {
		t.Fatalf("log level = %q, want extended", script.Config.Log.Level)
	}
	if script.Config.RunLogic.AutoTransactions != "per_iteration" {
		t.Fatalf("auto transactions = %q, want per_iteration", script.Config.RunLogic.AutoTransactions)
	}
	if !script.Config.ErrorHandling.ContinueOnError || !script.Config.ErrorHandling.SnapshotOnError {
		t.Fatalf("error handling = %#v, want continue_on_error and snapshot_on_error", script.Config.ErrorHandling)
	}
	if len(script.Actions) != 1 {
		t.Fatalf("expected 1 action, got %d", len(script.Actions))
	}
	if script.Actions[0].Target != "/namespaces/pr-123/orders" {
		t.Fatalf("target = %q, want /namespaces/pr-123/orders", script.Actions[0].Target)
	}
	if script.Actions[0].Body != "namespace=pr-123" {
		t.Fatalf("body = %q, want namespace=pr-123", script.Actions[0].Body)
	}
	if script.Actions[0].Headers["X-Namespace"] != "pr-123" {
		t.Fatalf("header X-Namespace = %q, want pr-123", script.Actions[0].Headers["X-Namespace"])
	}
}

func TestCloneRuntimeSettingsDoesNotAliasMutableFields(t *testing.T) {
	iterations := 3
	headless := true
	touch := false
	source := &RuntimeSettingsOverride{
		Iterations: &iterations,
		Pacing:     &dsl.PacingConfig{Type: "fixed_delay", DelayMs: 100},
		Browser:    &RuntimeBrowserOverride{Headless: &headless, TouchMode: &touch, DeviceProfile: "desktop_1080p"},
		Env:        map[string]string{"namespace": "blue"},
		RedactionRules: []engine.RedactionRule{
			{Name: "token", Pattern: `token-[0-9]+`},
		},
	}

	clone := cloneRuntimeSettings(source)
	*source.Iterations = 9
	source.Pacing.DelayMs = 999
	*source.Browser.Headless = false
	*source.Browser.TouchMode = true
	source.Env["namespace"] = "green"
	source.RedactionRules[0].Pattern = `changed`

	if clone.Iterations == nil || *clone.Iterations != 3 {
		t.Fatalf("cloned iterations = %#v, want 3", clone.Iterations)
	}
	if clone.Pacing == nil || clone.Pacing.DelayMs != 100 {
		t.Fatalf("cloned pacing = %#v, want delay 100", clone.Pacing)
	}
	if clone.Browser == nil || clone.Browser.Headless == nil || *clone.Browser.Headless != true {
		t.Fatalf("cloned browser headless = %#v, want true", clone.Browser)
	}
	if clone.Browser.TouchMode == nil || *clone.Browser.TouchMode != false {
		t.Fatalf("cloned browser touch = %#v, want false", clone.Browser.TouchMode)
	}
	if clone.Env["namespace"] != "blue" {
		t.Fatalf("cloned env = %#v, want original value", clone.Env)
	}
	if clone.RedactionRules[0].Pattern != `token-[0-9]+` {
		t.Fatalf("cloned redaction pattern = %q, want original", clone.RedactionRules[0].Pattern)
	}
}

func TestMergeRuntimeSettingsPreservesBaseAndAppliesGroupPrecedence(t *testing.T) {
	baseIterations := 2
	groupIterations := 5
	baseHeadless := true
	groupTouch := true

	base := &RuntimeSettingsOverride{
		Iterations:       &baseIterations,
		BaseURL:          "https://base.example",
		LogLevel:         "standard",
		AutoTransactions: "per_iteration",
		Browser:          &RuntimeBrowserOverride{Headless: &baseHeadless, DeviceProfile: "desktop_1080p", Orientation: "landscape"},
		Env:              map[string]string{"region": "east", "namespace": "base"},
		RedactionRules:   []engine.RedactionRule{{Name: "base", Pattern: `base-[0-9]+`}},
	}
	group := &RuntimeSettingsOverride{
		Iterations:     &groupIterations,
		BaseURL:        "https://group.example",
		LogLevel:       "extended",
		Browser:        &RuntimeBrowserOverride{TouchMode: &groupTouch, DeviceProfile: "iphone_15_pro"},
		Env:            map[string]string{"namespace": "group"},
		RedactionRules: []engine.RedactionRule{{Name: "group", Pattern: `group-[0-9]+`}},
	}

	merged := mergeRuntimeSettings(base, group)
	if merged.Iterations == nil || *merged.Iterations != 5 {
		t.Fatalf("iterations = %#v, want group override 5", merged.Iterations)
	}
	if merged.BaseURL != "https://group.example" || merged.LogLevel != "extended" {
		t.Fatalf("merged URL/log = %q/%q, want group overrides", merged.BaseURL, merged.LogLevel)
	}
	if merged.AutoTransactions != "per_iteration" {
		t.Fatalf("auto transactions = %q, want base fallback", merged.AutoTransactions)
	}
	if merged.Browser.Headless == nil || *merged.Browser.Headless != true {
		t.Fatalf("headless = %#v, want base fallback true", merged.Browser.Headless)
	}
	if merged.Browser.TouchMode == nil || *merged.Browser.TouchMode != true {
		t.Fatalf("touch = %#v, want group override true", merged.Browser.TouchMode)
	}
	if merged.Browser.DeviceProfile != "iphone_15_pro" || merged.Browser.Orientation != "landscape" {
		t.Fatalf("browser = %#v, want group profile and base orientation", merged.Browser)
	}
	if merged.Env["region"] != "east" || merged.Env["namespace"] != "group" {
		t.Fatalf("env = %#v, want base plus group override", merged.Env)
	}
	if len(merged.RedactionRules) != 1 || merged.RedactionRules[0].Name != "group" {
		t.Fatalf("redaction rules = %#v, want group replacement", merged.RedactionRules)
	}

	*base.Iterations = 11
	base.Env["namespace"] = "mutated"
	group.RedactionRules[0].Pattern = "mutated"
	if *merged.Iterations != 5 || merged.Env["namespace"] != "group" || merged.RedactionRules[0].Pattern != `group-[0-9]+` {
		t.Fatalf("merged settings aliased source fields: %#v", merged)
	}
}
