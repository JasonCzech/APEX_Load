package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func iterationCappedScript(id, name string, iterations int) *dsl.Script {
	script := httpScript(id, name)
	script.Config.RunLogic = dsl.RunLogicConfig{Iterations: iterations}
	return script
}

// A duration-driven scenario must keep every group loading for the whole
// schedule. A script-sourced per-VUser iteration cap (what the LoadRunner
// archive importer writes from the RTS "Number of Iterations", defaulting to 1)
// would otherwise retire that group's VUsers while its peers keep running.
func TestScenarioScheduleOverridesScriptIterationCap(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	capped := iterationCappedScript("lr-capped-script", "LR imported", 1)
	uncapped := httpScript("duration-driven-script", "Duration driven")
	for _, script := range []*dsl.Script{capped, uncapped} {
		if _, err := server.runner.StoreScriptDurable(script); err != nil {
			t.Fatalf("StoreScriptDurable(%s): %v", script.ID, err)
		}
	}

	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Mixed groups", Duration: "10m",
		Groups: []CreateGroupEntry{
			{ScriptID: capped.ID, VUsers: 5, RampUp: "1m"},
			{ScriptID: uncapped.ID, VUsers: 5, RampUp: "1m"},
		},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	for index, spec := range test.groupSpecs {
		if got := spec.Script.Config.RunLogic.Iterations; got != 0 {
			t.Fatalf("group[%d] compiled iterations = %d, want 0 (duration-driven)", index, got)
		}
	}
	if test.Groups[0].Iterations != nil {
		t.Fatalf("group[0] effective iterations = %v, want nil (no operator intent)", test.Groups[0].Iterations)
	}
	if got := test.Groups[0].scheduleOverriddenScriptIterations; got != 1 {
		t.Fatalf("group[0] recorded overridden cap = %d, want 1", got)
	}
	if got := test.Groups[1].scheduleOverriddenScriptIterations; got != 0 {
		t.Fatalf("group[1] recorded overridden cap = %d, want 0 (script had no cap)", got)
	}

	// The stored script keeps its imported cap: only the compiled run overrides it.
	stored, ok := server.runner.GetScript(capped.ID)
	if !ok || stored.Script.Config.RunLogic.Iterations != 1 {
		t.Fatalf("stored script iterations changed: %+v", stored)
	}

	review := test.RuntimeSettingsReview
	if review == nil {
		t.Fatal("runtime settings review = nil, want the schedule-override finding")
	}
	found := false
	for _, finding := range review.Findings {
		if finding.ID == "run_logic_iterations_schedule_override" {
			found = true
			if finding.Severity != "info" {
				t.Fatalf("override finding severity = %q, want info", finding.Severity)
			}
			if finding.ScriptID != capped.ID {
				t.Fatalf("override finding script id = %q, want %q", finding.ScriptID, capped.ID)
			}
		}
	}
	if !found {
		t.Fatalf("review findings lack the schedule override: %+v", review.Findings)
	}
	if review.Status == "blocked" {
		t.Fatalf("review status = %q, want a non-blocking review", review.Status)
	}
}

// Explicit operator intent still wins: a group- or scenario-level iteration
// count is LoadRunner's "run until iterations complete" equivalent.
func TestExplicitIterationIntentSurvivesScheduleAuthority(t *testing.T) {
	for _, tc := range []struct {
		name            string
		groupIterations *int
		rootRunLogic    *dsl.RunLogicConfig
		rootIterations  *int
		want            int
	}{
		{name: "group override", groupIterations: iterationIntPointer(3), want: 3},
		{name: "root run_logic", rootRunLogic: &dsl.RunLogicConfig{Iterations: 5}, want: 5},
		{name: "root legacy iterations alias", rootIterations: iterationIntPointer(7), want: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server, _ := newRuntimeOverrideTestServer(t)
			script := iterationCappedScript("pinned-iteration-script", "Pinned", 1)
			if _, err := server.runner.StoreScriptDurable(script); err != nil {
				t.Fatalf("StoreScriptDurable: %v", err)
			}
			var runtimeSettings *RuntimeSettingsOverride
			if tc.rootRunLogic != nil || tc.rootIterations != nil {
				runtimeSettings = &RuntimeSettingsOverride{RunLogic: tc.rootRunLogic, Iterations: tc.rootIterations}
			}
			test, err := server.runner.CreateTest(CreateTestRequest{
				Name: "Pinned iterations", Duration: "10m",
				RuntimeSettings: runtimeSettings,
				Groups: []CreateGroupEntry{{
					ScriptID: script.ID, VUsers: 2, Iterations: cloneIntPtr(tc.groupIterations),
				}},
			})
			if err != nil {
				t.Fatalf("CreateTest: %v", err)
			}
			if got := test.groupSpecs[0].Script.Config.RunLogic.Iterations; got != tc.want {
				t.Fatalf("compiled iterations = %d, want %d", got, tc.want)
			}
			if got := test.Groups[0].scheduleOverriddenScriptIterations; got != 0 {
				t.Fatalf("recorded overridden cap = %d, want 0 (explicit intent)", got)
			}
		})
	}
}

// In the open model run_logic.iterations is the per-worker arrival budget that
// bounds total offered arrivals, not a per-VUser loop cap, so the scheduler
// authority must leave it alone.
func TestOpenWorkloadArrivalBudgetIsNotOverridden(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := httpScript("open-model-script", "Open model")
	script.Config.RunLogic = dsl.RunLogicConfig{Iterations: 4, WorkloadModel: "open"}
	script.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: 5}
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Open workload", Duration: "10m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 2}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	if got := test.groupSpecs[0].Script.Config.RunLogic.Iterations; got != 4 {
		t.Fatalf("compiled open-model iterations = %d, want 4 (arrival budget preserved)", got)
	}
	if got := test.Groups[0].scheduleOverriddenScriptIterations; got != 0 {
		t.Fatalf("recorded overridden cap = %d, want 0", got)
	}
}

// Shared run goals are authored stop conditions, not per-VUser caps: the
// schedule authority must not silently remove them.
func TestSharedRunGoalsSurviveScheduleAuthority(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := iterationCappedScript("goal-script", "Goal script", 2)
	script.Config.RunLogic.GlobalIterations = 50
	script.Config.RunLogic.MaxTransactions = 500
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Goal driven", Duration: "10m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 2}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	runLogic := test.groupSpecs[0].Script.Config.RunLogic
	if runLogic.Iterations != 0 {
		t.Fatalf("compiled iterations = %d, want 0", runLogic.Iterations)
	}
	if runLogic.GlobalIterations != 50 || runLogic.MaxTransactions != 500 {
		t.Fatalf("shared goals changed: global=%d max_tx=%d", runLogic.GlobalIterations, runLogic.MaxTransactions)
	}
	if test.Groups[0].RunLogicGoals == nil || test.Groups[0].RunLogicGoals.GlobalIterations != 50 {
		t.Fatalf("run logic goal snapshot = %+v, want global_iterations 50", test.Groups[0].RunLogicGoals)
	}
}

// The scenario runtime-settings report is a pre-launch advisory, so it must show
// the same precedence a run applies rather than reporting the script's cap as
// though it still governed the run.
func TestScenarioRuntimeSettingsReportShowsScheduleOverride(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	capped := iterationCappedScript("report-capped-script", "Report capped", 1)
	pinned := iterationCappedScript("report-pinned-script", "Report pinned", 1)
	for _, script := range []*dsl.Script{capped, pinned} {
		if _, err := server.runner.StoreScriptDurable(script); err != nil {
			t.Fatalf("StoreScriptDurable(%s): %v", script.ID, err)
		}
	}
	scenario, err := server.runner.StoreScenario(CreateScenarioRequest{
		Name: "Report scenario", Duration: "10m",
		Groups: []CreateGroupEntry{
			{ScriptID: capped.ID, Name: "overridden", VUsers: 2},
			{ScriptID: pinned.ID, Name: "pinned", VUsers: 2, Iterations: iterationIntPointer(2)},
		},
	})
	if err != nil {
		t.Fatalf("StoreScenario: %v", err)
	}
	scripts := map[string]*StoredScript{}
	for _, ref := range scenario.Scripts {
		stored, ok := server.runner.GetScript(ref.ScriptID)
		if !ok {
			t.Fatalf("GetScript(%s) missing", ref.ScriptID)
		}
		scripts[ref.ScriptID] = stored
	}

	report := runtimeSettingsReportForScenario(scenario.ID, scenario, scripts)
	overridden := 0
	for _, finding := range report.Findings {
		if finding.ID != "run_logic_iterations_schedule_override" {
			continue
		}
		overridden++
		if finding.ScriptID != capped.ID {
			t.Fatalf("override finding script id = %q, want %q", finding.ScriptID, capped.ID)
		}
	}
	if overridden != 1 {
		t.Fatalf("schedule override findings = %d, want 1 (only the unpinned group): %+v", overridden, report.Findings)
	}
	if report.Status == "blocked" {
		t.Fatalf("report status = %q, want a non-blocking report", report.Status)
	}
}

// A run queued before this rule existed still holds a capped compiled snapshot.
// Restoring its engine specs must apply the same scheduler authority so a
// resumed or failed-over run does not drain its groups early.
func TestRestoredGroupSpecsApplyScheduleIterationAuthority(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := iterationCappedScript("restored-script", "Restored", 1)
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Restored run", Duration: "10m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 2}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	// Simulate the legacy snapshot: the compiled script still carries the cap.
	test.Groups[0].script.Config.RunLogic.Iterations = 1

	specs, err := restoreStoredEngineGroupSpecs(test)
	if err != nil {
		t.Fatalf("restoreStoredEngineGroupSpecs: %v", err)
	}
	if got := specs[0].Script.Config.RunLogic.Iterations; got != 0 {
		t.Fatalf("restored iterations = %d, want 0 (duration-driven)", got)
	}
	if got := test.Groups[0].scheduleOverriddenScriptIterations; got != 1 {
		t.Fatalf("restored recorded overridden cap = %d, want 1", got)
	}
}

// The pinned-count case must round-trip through restore unchanged.
func TestRestoredGroupSpecsKeepExplicitIterationIntent(t *testing.T) {
	server, _ := newRuntimeOverrideTestServer(t)
	script := iterationCappedScript("restored-pinned-script", "Restored pinned", 1)
	if _, err := server.runner.StoreScriptDurable(script); err != nil {
		t.Fatalf("StoreScriptDurable: %v", err)
	}
	test, err := server.runner.CreateTest(CreateTestRequest{
		Name: "Restored pinned run", Duration: "10m",
		Groups: []CreateGroupEntry{{ScriptID: script.ID, VUsers: 2, Iterations: iterationIntPointer(4)}},
	})
	if err != nil {
		t.Fatalf("CreateTest: %v", err)
	}
	specs, err := restoreStoredEngineGroupSpecs(test)
	if err != nil {
		t.Fatalf("restoreStoredEngineGroupSpecs: %v", err)
	}
	if got := specs[0].Script.Config.RunLogic.Iterations; got != 4 {
		t.Fatalf("restored iterations = %d, want 4", got)
	}
}
