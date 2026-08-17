package api

import (
	"fmt"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// applyScheduleIterationAuthority makes the scenario schedule — not the script —
// the authority on how long a group's VUsers keep running.
//
// Every managed run is duration-driven: MultiGroupEngine requires a positive
// duration and stops the run when the schedule (plus ramp-down and baseline
// windows) expires. A per-VUser cap in config.run_logic.iterations retires each
// VUser as soon as it completes that many iterations, so a group whose SCRIPT
// carries a cap drains and shuts down mid-run while the other groups keep
// loading. Imported LoadRunner scripts hit this constantly: the enterprise
// archive importer maps the RTS "Number of Iterations" into
// config.run_logic.iterations and falls back to 1 whenever the run-logic
// profile is missing or ambiguous, which is also LoadRunner's own default.
// LoadRunner Controller resolves the same conflict in favor of the schedule:
// with a scenario duration configured, the RTS iteration count is ignored and
// VUsers iterate until the schedule ends.
//
// The operator can still pin a count, because an explicit scenario- or
// group-level iteration value (CreateGroupEntry.iterations, or
// runtime_settings[.run_logic].iterations at either scope) wins over this
// override — that is the "run until iterations complete" equivalent, and it is
// what explicitIterations carries. Shared goals (run_logic.global_iterations,
// run_logic.max_transactions) are deliberately untouched: they are authored run
// goals that the archive report surfaces as configured goals, and ending the run
// when they are met is their whole purpose.
//
// Open-model scripts are also left alone. There run_logic.iterations is not a
// per-VUser loop cap but the per-worker arrival budget that bounds total
// arrivals (see engine.boundedArrivalLimit), so clearing it would change the
// offered-load contract rather than remove an early exit.
//
// It returns the cap that was overridden (0 when nothing changed) so callers can
// log it and surface it in the pre-launch runtime-settings review instead of
// silently changing what the operator asked for.
func applyScheduleIterationAuthority(script *dsl.Script, explicitIterations *int) int {
	if script == nil || explicitIterations != nil {
		return 0
	}
	scriptCap := script.Config.RunLogic.Iterations
	if scriptCap <= 0 {
		return 0
	}
	if dsl.CanonicalWorkloadModel(script.Config.RunLogic.WorkloadModel) == "open" {
		return 0
	}
	script.Config.RunLogic.Iterations = 0
	return scriptCap
}

// scenarioGroupIterationIntent resolves the explicit iteration count a scenario
// group carries, mirroring how buildTestGroup merges root and group runtime
// settings with the compact groups[].iterations field. It returns nil when the
// scenario leaves run length to the schedule.
func scenarioGroupIterationIntent(root, group *RuntimeSettingsOverride, compact *int) *int {
	merged := mergeRuntimeSettings(root, group)
	if compact != nil {
		merged = setRuntimeSettingsIteration(merged, *compact)
	}
	if iterations, present := runtimeSettingsIterationValue(merged); present && iterations > 0 {
		return &iterations
	}
	return nil
}

// scheduleIterationOverrideFinding surfaces an applied override in the
// pre-launch runtime-settings review. It is informational: the run is
// executable and the override is the intended default, but the operator sees
// which group's script cap stopped applying and how to pin it back.
func scheduleIterationOverrideFinding(scope string, group TestGroup, scriptCap int) RuntimeSettingsFinding {
	unit := "iterations"
	if scriptCap == 1 {
		unit = "iteration"
	}
	return RuntimeSettingsFinding{
		ID:         "run_logic_iterations_schedule_override",
		Scope:      scope,
		Group:      group.Name,
		ScriptID:   group.ScriptID,
		ScriptName: group.ScriptName,
		Protocol:   group.Protocol,
		Area:       "Run Logic",
		Concept:    "Iterations",
		Status:     "executed",
		Severity:   "info",
		Summary: fmt.Sprintf(
			"Script iteration cap of %d %s per VUser is not applied: the scenario schedule owns run length, so this group's VUsers iterate until the schedule ends.",
			scriptCap, unit,
		),
		RecommendedAction: "Set iterations on this group (or in scenario runtime settings) to stop its VUsers after a fixed iteration count.",
		Evidence:          []string{"config.run_logic.iterations"},
	}
}
