package api

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Continue-on-error is enabled for every scenario, existing or new, unless it is
// explicitly disabled. "Explicitly" is what the tri-state
// dsl.ErrorHandlingConfig.ContinueOnError buys: a nil value is silence at that
// layer and lets the scenario default through, while a non-nil false is an
// operator decision that this policy will not overturn on its own.
//
// The layers, most specific first:
//
//  1. the script group's runtime settings,
//  2. the scenario's runtime settings,
//  3. the script's own config (typically a LoadRunner import that carried
//     ContinueOnError=0 across from its runtime settings),
//  4. the scenario default, which is enabled.
//
// A more specific layer still wins at execution time — that is what makes a
// per-group opt-out meaningful — but whenever the winning layer disagrees with
// the scenario-level value the divergence is recorded and surfaced in the test
// run manifest instead of being applied silently.
const (
	continueOnErrorSourceGroup           = "group"
	continueOnErrorSourceScenario        = "scenario"
	continueOnErrorSourceScript          = "script"
	continueOnErrorSourceScenarioDefault = "scenario_default"
)

// continueOnErrorScenarioDefault is the scenario-level default applied when the
// scenario settings say nothing about continue-on-error.
const continueOnErrorScenarioDefault = true

// ContinueOnErrorResolution records both the value a group runs with and how it
// was reached, so the manifest can explain a divergence rather than just report
// a boolean.
type ContinueOnErrorResolution struct {
	// Effective is the value the group actually executes with.
	Effective bool `json:"effective"`
	// Source names the layer that supplied Effective.
	Source string `json:"source"`
	// ScenarioValue is the scenario-level policy value, whether it was set
	// explicitly or defaulted.
	ScenarioValue bool `json:"scenario_value"`
	// ScenarioExplicit distinguishes a scenario that chose its value from one
	// that inherited the enabled-by-default policy.
	ScenarioExplicit bool `json:"scenario_explicit"`
	// Conflict is set when Effective diverges from ScenarioValue.
	Conflict bool `json:"conflict,omitempty"`
}

// runtimeSettingsContinueOnError reads a runtime-settings layer's stated
// continue-on-error intent. A layer with no error_handling section, or one that
// omits continue_on_error, states nothing.
func runtimeSettingsContinueOnError(settings *RuntimeSettingsOverride) (bool, bool) {
	if settings == nil || settings.ErrorHandling == nil || settings.ErrorHandling.ContinueOnError == nil {
		return false, false
	}
	return *settings.ErrorHandling.ContinueOnError, true
}

// scenarioContinueOnErrorPolicy returns the scenario-level value and whether the
// scenario settings chose it explicitly.
func scenarioContinueOnErrorPolicy(scenario *RuntimeSettingsOverride) (bool, bool) {
	if value, explicit := runtimeSettingsContinueOnError(scenario); explicit {
		return value, true
	}
	return continueOnErrorScenarioDefault, false
}

// resolveGroupContinueOnError applies the layering above for one script group.
// script must be the group's script before runtime overrides are applied, so
// that a group override cannot be mistaken for the script's own intent.
func resolveGroupContinueOnError(scenario, group *RuntimeSettingsOverride, script *dsl.Script) ContinueOnErrorResolution {
	scenarioValue, scenarioExplicit := scenarioContinueOnErrorPolicy(scenario)
	resolution := ContinueOnErrorResolution{
		ScenarioValue:    scenarioValue,
		ScenarioExplicit: scenarioExplicit,
	}

	switch {
	case hasExplicitGroupContinueOnError(group):
		resolution.Effective, _ = runtimeSettingsContinueOnError(group)
		resolution.Source = continueOnErrorSourceGroup
	case scenarioExplicit:
		resolution.Effective = scenarioValue
		resolution.Source = continueOnErrorSourceScenario
	case script != nil && script.Config.ErrorHandling.ContinueOnErrorSet():
		resolution.Effective = script.Config.ErrorHandling.ContinueOnErrorEnabled()
		resolution.Source = continueOnErrorSourceScript
	default:
		resolution.Effective = scenarioValue
		resolution.Source = continueOnErrorSourceScenarioDefault
	}

	resolution.Conflict = resolution.Effective != resolution.ScenarioValue
	return resolution
}

func hasExplicitGroupContinueOnError(group *RuntimeSettingsOverride) bool {
	_, explicit := runtimeSettingsContinueOnError(group)
	return explicit
}

// applyContinueOnErrorResolution writes the resolved value onto the compiled
// script. It deliberately touches only continue_on_error: the surrounding
// error_handling fields (retries, snapshot-on-error) keep whatever the script
// and the runtime overrides already agreed on.
func applyContinueOnErrorResolution(script *dsl.Script, resolution ContinueOnErrorResolution) {
	if script == nil {
		return
	}
	script.Config.ErrorHandling.ContinueOnError = dsl.ContinueOnErrorValue(resolution.Effective)
}

// continueOnErrorConflictDetail renders a conflict for the manifest warning
// list. It names the layer that won, so an operator can see which setting to
// change rather than only that the two disagree.
func continueOnErrorConflictDetail(groupName string, resolution ContinueOnErrorResolution) string {
	scenarioPhrase := fmt.Sprintf("%s by default", continueOnErrorWord(resolution.ScenarioValue))
	if resolution.ScenarioExplicit {
		scenarioPhrase = fmt.Sprintf("explicitly %s", continueOnErrorWord(resolution.ScenarioValue))
	}
	sourcePhrase := "the script group's runtime settings"
	winnerPhrase := "the group setting wins"
	if resolution.Source == continueOnErrorSourceScript {
		sourcePhrase = "the script's own runtime settings"
		winnerPhrase = "the script setting wins"
	}
	name := strings.TrimSpace(groupName)
	if name == "" {
		name = "(unnamed group)"
	}
	return fmt.Sprintf(
		"continue-on-error conflict: group %q runs %s because %s sets it, while the scenario has it %s; %s",
		name,
		continueOnErrorWord(resolution.Effective),
		sourcePhrase,
		scenarioPhrase,
		winnerPhrase,
	)
}

func continueOnErrorWord(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}
