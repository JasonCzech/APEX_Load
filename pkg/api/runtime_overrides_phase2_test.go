package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Phase 2 increment 1: full Log + RunLogic override sections, with section-wins
// resolution over the legacy LogLevel/Iterations/AutoTransactions aliases.

func TestApplyRuntimeOverridesLogSection(t *testing.T) {
	script := &dsl.Script{}
	script.Config.Log = dsl.LogConfig{Level: "standard"}
	o := &RuntimeSettingsOverride{
		Log: &dsl.LogConfig{Level: "extended", OnErrorOnly: true, ExtendedOptions: []string{"data_returned"}},
	}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if script.Config.Log.Level != "extended" || !script.Config.Log.OnErrorOnly || len(script.Config.Log.ExtendedOptions) != 1 {
		t.Fatalf("log section not applied as whole replace: %#v", script.Config.Log)
	}
}

func TestApplyRuntimeOverridesLogSectionWinsOverLegacyLevel(t *testing.T) {
	script := &dsl.Script{}
	o := &RuntimeSettingsOverride{
		Log:      &dsl.LogConfig{Level: "extended"},
		LogLevel: "disabled", // legacy alias must be ignored when the section is present
	}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if script.Config.Log.Level != "extended" {
		t.Fatalf("section should win over legacy log_level, got %q", script.Config.Log.Level)
	}
}

func TestApplyRuntimeOverridesLegacyLogLevelWhenNoSection(t *testing.T) {
	script := &dsl.Script{}
	o := &RuntimeSettingsOverride{LogLevel: "extended"}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if script.Config.Log.Level != "extended" {
		t.Fatalf("legacy log_level not applied, got %q", script.Config.Log.Level)
	}
}

func TestApplyRuntimeOverridesRunLogicSectionWinsOverLegacy(t *testing.T) {
	script := &dsl.Script{}
	iter := 7
	o := &RuntimeSettingsOverride{
		RunLogic:         &dsl.RunLogicConfig{Iterations: 3, WorkloadModel: "open", AutoTransactions: "per_iteration"},
		Iterations:       &iter,        // legacy — ignored when the section is present
		AutoTransactions: "per_action", // legacy — ignored when the section is present
	}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := script.Config.RunLogic
	if got.Iterations != 3 || got.WorkloadModel != "open" || got.AutoTransactions != "per_iteration" {
		t.Fatalf("run_logic section should win over legacy fields, got %#v", got)
	}
}

func TestApplyRuntimeOverridesLegacyRunLogicFieldsWhenNoSection(t *testing.T) {
	script := &dsl.Script{}
	iter := 9
	o := &RuntimeSettingsOverride{Iterations: &iter, AutoTransactions: "per_action"}
	if err := applyRuntimeOverrides(script, o); err != nil {
		t.Fatalf("apply: %v", err)
	}
	got := script.Config.RunLogic
	if got.Iterations != 9 || got.AutoTransactions != "per_action" {
		t.Fatalf("legacy run-logic fields not applied: %#v", got)
	}
}

func TestMergeRuntimeSettingsReplacesLogAndRunLogicSections(t *testing.T) {
	base := &RuntimeSettingsOverride{
		Log:      &dsl.LogConfig{Level: "standard"},
		RunLogic: &dsl.RunLogicConfig{Iterations: 1, WorkloadModel: "closed"},
	}
	group := &RuntimeSettingsOverride{
		Log:      &dsl.LogConfig{Level: "extended", ExtendedOptions: []string{"advanced_trace"}},
		RunLogic: &dsl.RunLogicConfig{Iterations: 5, WorkloadModel: "open"},
	}
	merged := mergeRuntimeSettings(base, group)
	if merged.Log.Level != "extended" || len(merged.Log.ExtendedOptions) != 1 {
		t.Fatalf("merged log not group's: %#v", merged.Log)
	}
	if merged.RunLogic.Iterations != 5 || merged.RunLogic.WorkloadModel != "open" {
		t.Fatalf("merged run_logic not group's: %#v", merged.RunLogic)
	}
	// base must be untouched (no aliasing through merge).
	if base.Log.Level != "standard" || base.RunLogic.Iterations != 1 {
		t.Fatalf("merge mutated base: %#v %#v", base.Log, base.RunLogic)
	}
}

func TestMergeRuntimeSettingsPromotesGroupLegacyLogLevelOverBaseSection(t *testing.T) {
	base := &RuntimeSettingsOverride{Log: &dsl.LogConfig{
		Level: "extended", OnErrorOnly: true, ExtendedOptions: []string{"advanced_trace"},
	}}
	group := &RuntimeSettingsOverride{LogLevel: "disabled"}
	merged := mergeRuntimeSettings(base, group)
	if merged.Log == nil || merged.Log.Level != "disabled" || !merged.Log.OnErrorOnly || len(merged.Log.ExtendedOptions) != 1 {
		t.Fatalf("merged group alias should replace the level and retain base section siblings: %#v", merged.Log)
	}
	if base.Log.Level != "extended" || !base.Log.OnErrorOnly || len(base.Log.ExtendedOptions) != 1 {
		t.Fatalf("merge mutated base log section: %#v", base.Log)
	}
}

func TestCloneRuntimeSettingsDeepClonesLogAndRunLogicSlices(t *testing.T) {
	o := &RuntimeSettingsOverride{
		Log:      &dsl.LogConfig{ExtendedOptions: []string{"a"}},
		RunLogic: &dsl.RunLogicConfig{ActionSequence: []string{"init"}},
	}
	clone := cloneRuntimeSettings(o)
	clone.Log.ExtendedOptions[0] = "MUT"
	clone.RunLogic.ActionSequence[0] = "MUT"
	if o.Log.ExtendedOptions[0] != "a" || o.RunLogic.ActionSequence[0] != "init" {
		t.Fatalf("clone aliases the original slices: %#v %#v", o.Log, o.RunLogic)
	}
}
