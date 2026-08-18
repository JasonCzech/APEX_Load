package api

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func errorHandlingOverride(continueOnError *bool) *RuntimeSettingsOverride {
	return &RuntimeSettingsOverride{ErrorHandling: &dsl.ErrorHandlingConfig{ContinueOnError: continueOnError}}
}

func scriptWithContinueOnError(continueOnError *bool) *dsl.Script {
	script := &dsl.Script{}
	script.Config.ErrorHandling.ContinueOnError = continueOnError
	return script
}

func TestResolveGroupContinueOnErrorDefaultsToEnabled(t *testing.T) {
	// A scenario that says nothing, a group that says nothing, and a script that
	// says nothing must still run with continue-on-error enabled.
	resolution := resolveGroupContinueOnError(nil, nil, scriptWithContinueOnError(nil))
	if !resolution.Effective {
		t.Fatalf("effective = false, want true for an unset scenario")
	}
	if resolution.Source != continueOnErrorSourceScenarioDefault {
		t.Fatalf("source = %q, want %q", resolution.Source, continueOnErrorSourceScenarioDefault)
	}
	if resolution.ScenarioExplicit {
		t.Fatal("scenario_explicit = true, want false for a defaulted scenario")
	}
	if resolution.Conflict {
		t.Fatal("conflict = true, want false when every layer is silent")
	}
}

func TestResolveGroupContinueOnErrorScenarioOptOutIsHonored(t *testing.T) {
	// The scenario is the one place an operator can turn the policy off wholesale.
	resolution := resolveGroupContinueOnError(errorHandlingOverride(dsl.ContinueOnErrorValue(false)), nil, scriptWithContinueOnError(nil))
	if resolution.Effective {
		t.Fatal("effective = true, want false when the scenario explicitly disables it")
	}
	if resolution.Source != continueOnErrorSourceScenario {
		t.Fatalf("source = %q, want %q", resolution.Source, continueOnErrorSourceScenario)
	}
	if !resolution.ScenarioExplicit {
		t.Fatal("scenario_explicit = false, want true")
	}
	if resolution.Conflict {
		t.Fatal("conflict = true, want false when the scenario itself opted out")
	}
}

func TestResolveGroupContinueOnErrorGroupOptOutWinsAndConflicts(t *testing.T) {
	// The group is more specific, so it wins at execution time — but the
	// divergence from the scenario policy has to be recorded.
	resolution := resolveGroupContinueOnError(nil, errorHandlingOverride(dsl.ContinueOnErrorValue(false)), scriptWithContinueOnError(nil))
	if resolution.Effective {
		t.Fatal("effective = true, want false when the group explicitly disables it")
	}
	if resolution.Source != continueOnErrorSourceGroup {
		t.Fatalf("source = %q, want %q", resolution.Source, continueOnErrorSourceGroup)
	}
	if !resolution.Conflict {
		t.Fatal("conflict = false, want true when the group diverges from the scenario policy")
	}
}

func TestResolveGroupContinueOnErrorGroupSectionWithoutFieldStaysSilent(t *testing.T) {
	// A group that sets only snapshot_on_error states nothing about
	// continue-on-error; the scenario default must still come through. This is the
	// case a plain bool could not express.
	group := &RuntimeSettingsOverride{ErrorHandling: &dsl.ErrorHandlingConfig{SnapshotOnError: true}}
	resolution := resolveGroupContinueOnError(nil, group, scriptWithContinueOnError(nil))
	if !resolution.Effective {
		t.Fatal("effective = false, want true when the group's error_handling omits continue_on_error")
	}
	if resolution.Source != continueOnErrorSourceScenarioDefault {
		t.Fatalf("source = %q, want %q", resolution.Source, continueOnErrorSourceScenarioDefault)
	}
}

func TestResolveGroupContinueOnErrorScriptOptOutSurvivesTheDefault(t *testing.T) {
	// An imported script that carried ContinueOnError=0 across from its source
	// runtime settings is an explicit opt-out, so the default does not overturn it.
	resolution := resolveGroupContinueOnError(nil, nil, scriptWithContinueOnError(dsl.ContinueOnErrorValue(false)))
	if resolution.Effective {
		t.Fatal("effective = true, want false when the script explicitly disables it")
	}
	if resolution.Source != continueOnErrorSourceScript {
		t.Fatalf("source = %q, want %q", resolution.Source, continueOnErrorSourceScript)
	}
	if !resolution.Conflict {
		t.Fatal("conflict = false, want true when the script diverges from the scenario policy")
	}
}

func TestResolveGroupContinueOnErrorScenarioBeatsScript(t *testing.T) {
	// An explicit scenario value outranks the script, and that is not a conflict.
	resolution := resolveGroupContinueOnError(
		errorHandlingOverride(dsl.ContinueOnErrorValue(true)),
		nil,
		scriptWithContinueOnError(dsl.ContinueOnErrorValue(false)),
	)
	if !resolution.Effective {
		t.Fatal("effective = false, want true when the scenario explicitly enables it")
	}
	if resolution.Source != continueOnErrorSourceScenario {
		t.Fatalf("source = %q, want %q", resolution.Source, continueOnErrorSourceScenario)
	}
	if resolution.Conflict {
		t.Fatal("conflict = true, want false when the scenario layer itself decided")
	}
}

func TestApplyContinueOnErrorResolutionLeavesTheRestOfTheSection(t *testing.T) {
	script := scriptWithContinueOnError(nil)
	script.Config.ErrorHandling.MaxRetries = 3
	script.Config.ErrorHandling.RetryBackoff = "exponential"
	script.Config.ErrorHandling.SnapshotOnError = true

	applyContinueOnErrorResolution(script, ContinueOnErrorResolution{Effective: true})

	if !script.Config.ErrorHandling.ContinueOnErrorEnabled() {
		t.Fatal("continue_on_error was not applied to the compiled script")
	}
	if script.Config.ErrorHandling.MaxRetries != 3 ||
		script.Config.ErrorHandling.RetryBackoff != "exponential" ||
		!script.Config.ErrorHandling.SnapshotOnError {
		t.Fatalf("applying the policy disturbed the rest of error_handling: %#v", script.Config.ErrorHandling)
	}
}

func TestManifestContinueOnErrorFromGroupsReportsConflicts(t *testing.T) {
	groups := []TestGroup{
		{
			Name:       "checkout",
			ScriptID:   "script-1",
			ScriptName: "Checkout",
			ContinueOnError: &ContinueOnErrorResolution{
				Effective: true, Source: continueOnErrorSourceScenarioDefault, ScenarioValue: true,
			},
		},
		{
			Name:       "search",
			ScriptID:   "script-2",
			ScriptName: "Search",
			ContinueOnError: &ContinueOnErrorResolution{
				Effective: false, Source: continueOnErrorSourceGroup, ScenarioValue: true, Conflict: true,
			},
		},
	}

	summary := manifestContinueOnErrorFromGroups(groups)
	if summary == nil {
		t.Fatal("manifest continue-on-error summary is nil")
	}
	if !summary.ScenarioValue || summary.ScenarioExplicit {
		t.Fatalf("scenario policy = %#v, want the enabled default", summary)
	}
	if len(summary.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want 1", len(summary.Conflicts))
	}
	conflict := summary.Conflicts[0]
	if conflict.GroupName != "search" || conflict.Source != continueOnErrorSourceGroup || conflict.Effective {
		t.Fatalf("unexpected conflict entry: %#v", conflict)
	}
	if !strings.Contains(conflict.Detail, "search") || !strings.Contains(conflict.Detail, "continue-on-error conflict") {
		t.Fatalf("conflict detail does not name the conflict: %q", conflict.Detail)
	}
}

func TestManifestContinueOnErrorFromGroupsSkipsUnresolvedGroups(t *testing.T) {
	// Groups compiled before this policy existed carry no resolution; the manifest
	// reports no policy block rather than inventing one.
	if summary := manifestContinueOnErrorFromGroups([]TestGroup{{Name: "legacy"}}); summary != nil {
		t.Fatalf("summary = %#v, want nil for groups with no recorded resolution", summary)
	}
}

// storeContinueOnErrorScript registers a minimal HTTP script whose own
// error_handling states whatever the caller passes.
func storeContinueOnErrorScript(runner *TestRunner, id, name string, continueOnError *bool) {
	script := &dsl.Script{
		ID:       id,
		Name:     name,
		Protocol: "http",
		Version:  "1.0.0",
		Config: dsl.ScriptConfig{
			BaseURL:  "https://example.com",
			RunLogic: dsl.RunLogicConfig{Iterations: 1},
			Log:      dsl.LogConfig{Level: "standard"},
		},
		Actions: []dsl.Action{{Name: "Home", Type: "GET", Target: "/"}},
	}
	script.Config.ErrorHandling.ContinueOnError = continueOnError
	runner.StoreScript(script)
}

func TestCreateTestEnablesContinueOnErrorByDefault(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	storeContinueOnErrorScript(runner, "script-1", "Checkout", nil)

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Checkout Replay",
		Duration: "60s",
		Groups:   []CreateGroupEntry{{Name: "checkout", ScriptID: "script-1", VUsers: 5}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if len(test.Groups) != 1 {
		t.Fatalf("groups = %d, want 1", len(test.Groups))
	}

	group := test.Groups[0]
	if group.ContinueOnError == nil {
		t.Fatal("group carries no continue-on-error resolution")
	}
	if !group.ContinueOnError.Effective || group.ContinueOnError.Conflict {
		t.Fatalf("resolution = %#v, want the enabled default with no conflict", group.ContinueOnError)
	}
	if group.script == nil {
		t.Fatal("compiled group has no executable script")
	}
	if !group.script.Config.ErrorHandling.ContinueOnErrorEnabled() {
		t.Fatal("the compiled script does not run with continue-on-error enabled")
	}
}

func TestCreateTestGroupOptOutWinsAndIsRecorded(t *testing.T) {
	// A group that explicitly disables continue-on-error still runs with it off,
	// and the divergence from the scenario policy is recorded on the group so the
	// manifest can surface it.
	runner := NewTestRunner(2, NewHub(), nil)
	storeContinueOnErrorScript(runner, "script-1", "Checkout", nil)
	storeContinueOnErrorScript(runner, "script-2", "Search", nil)

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Mixed Policy",
		Duration: "60s",
		Groups: []CreateGroupEntry{
			{Name: "checkout", ScriptID: "script-1", VUsers: 5},
			{
				Name:            "search",
				ScriptID:        "script-2",
				VUsers:          5,
				RuntimeSettings: errorHandlingOverride(dsl.ContinueOnErrorValue(false)),
			},
		},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	checkout, search := test.Groups[0], test.Groups[1]
	if !checkout.script.Config.ErrorHandling.ContinueOnErrorEnabled() {
		t.Fatal("the group without an override should inherit the enabled default")
	}
	if search.script.Config.ErrorHandling.ContinueOnErrorEnabled() {
		t.Fatal("the group's explicit opt-out was overturned")
	}
	if search.ContinueOnError == nil || !search.ContinueOnError.Conflict {
		t.Fatalf("resolution = %#v, want a recorded conflict", search.ContinueOnError)
	}

	summary := manifestContinueOnErrorFromGroups(test.Groups)
	if summary == nil || len(summary.Conflicts) != 1 || summary.Conflicts[0].GroupName != "search" {
		t.Fatalf("manifest summary = %#v, want one conflict naming the search group", summary)
	}
}

func TestCreateTestGroupErrorHandlingOverrideKeepsTheDefault(t *testing.T) {
	// A group override that only sets retries must not silently drop
	// continue-on-error back to disabled, even though the group's error_handling
	// section replaces the script's section wholesale.
	runner := NewTestRunner(2, NewHub(), nil)
	storeContinueOnErrorScript(runner, "script-1", "Checkout", nil)

	test, err := runner.CreateTest(CreateTestRequest{
		Name:     "Retries Only",
		Duration: "60s",
		Groups: []CreateGroupEntry{{
			Name:     "checkout",
			ScriptID: "script-1",
			VUsers:   5,
			RuntimeSettings: &RuntimeSettingsOverride{
				ErrorHandling: &dsl.ErrorHandlingConfig{MaxRetries: 2, RetryBackoff: "exponential"},
			},
		}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	errorHandling := test.Groups[0].script.Config.ErrorHandling
	if !errorHandling.ContinueOnErrorEnabled() {
		t.Fatal("a retries-only override dropped continue-on-error back to disabled")
	}
	if errorHandling.MaxRetries != 2 || errorHandling.RetryBackoff != "exponential" {
		t.Fatalf("the override's own fields were lost: %#v", errorHandling)
	}
}

func TestCreateTestScenarioOptOutDisablesEveryGroup(t *testing.T) {
	runner := NewTestRunner(2, NewHub(), nil)
	storeContinueOnErrorScript(runner, "script-1", "Checkout", nil)

	test, err := runner.CreateTest(CreateTestRequest{
		Name:            "Strict",
		Duration:        "60s",
		Groups:          []CreateGroupEntry{{Name: "checkout", ScriptID: "script-1", VUsers: 5}},
		RuntimeSettings: errorHandlingOverride(dsl.ContinueOnErrorValue(false)),
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}

	group := test.Groups[0]
	if group.script.Config.ErrorHandling.ContinueOnErrorEnabled() {
		t.Fatal("the scenario's explicit opt-out was not applied")
	}
	if group.ContinueOnError == nil || group.ContinueOnError.Conflict {
		t.Fatalf("resolution = %#v, want no conflict when the scenario itself opted out", group.ContinueOnError)
	}
	if summary := manifestContinueOnErrorFromGroups(test.Groups); summary == nil ||
		summary.ScenarioValue || !summary.ScenarioExplicit || len(summary.Conflicts) != 0 {
		t.Fatalf("manifest summary = %#v, want an explicit disabled policy with no conflicts", summary)
	}
}

func TestApplyRuntimeToGroupsDoesNotPinContinueOnError(t *testing.T) {
	// "Apply runtime settings to groups" materializes engine defaults -> script ->
	// scenario -> source group and copies the result onto the targets. The engine
	// layer must not state a continue-on-error intent, or copying error_handling
	// between two groups that never opted out would pin an explicit false on the
	// target and silently defeat the scenario policy.
	effective := runtimeSettingsFromScriptConfig(dsl.DefaultScriptConfig())
	effective = mergeRuntimeSettings(effective, runtimeSettingsFromScriptConfig(dsl.ScriptConfig{}))

	if effective == nil || effective.ErrorHandling == nil {
		t.Fatal("engine defaults no longer project an error_handling section")
	}
	if effective.ErrorHandling.ContinueOnError != nil {
		t.Fatalf("engine defaults state continue_on_error = %v; the layer must stay silent",
			*effective.ErrorHandling.ContinueOnError)
	}
	// The rest of the section still has to survive the copy.
	if effective.ErrorHandling.RetryDelayMs != 1000 || effective.ErrorHandling.RetryBackoff != "fixed" {
		t.Fatalf("engine retry defaults were lost: %#v", effective.ErrorHandling)
	}

	target := replaceRuntimeFields(&RuntimeSettingsOverride{}, effective, []string{"error_handling"})
	resolution := resolveGroupContinueOnError(nil, target, &dsl.Script{})
	if !resolution.Effective || resolution.Conflict {
		t.Fatalf("resolution after apply-to-groups = %#v, want the enabled default with no conflict", resolution)
	}
}

func TestContinueOnErrorConflictDetailNamesTheWinningLayer(t *testing.T) {
	groupConflict := continueOnErrorConflictDetail("search", ContinueOnErrorResolution{
		Effective: false, Source: continueOnErrorSourceGroup, ScenarioValue: true, Conflict: true,
	})
	if !strings.Contains(groupConflict, "the script group's runtime settings") ||
		!strings.Contains(groupConflict, "the group setting wins") {
		t.Fatalf("group conflict detail = %q", groupConflict)
	}

	// A script-sourced conflict must not tell the operator to change a group
	// override that does not exist.
	scriptConflict := continueOnErrorConflictDetail("checkout", ContinueOnErrorResolution{
		Effective: false, Source: continueOnErrorSourceScript, ScenarioValue: true, Conflict: true,
	})
	if !strings.Contains(scriptConflict, "the script's own runtime settings") ||
		!strings.Contains(scriptConflict, "the script setting wins") {
		t.Fatalf("script conflict detail = %q", scriptConflict)
	}
	if strings.Contains(scriptConflict, "the group setting wins") {
		t.Fatalf("script conflict detail blames a group override: %q", scriptConflict)
	}
}

func TestReviseTestKeepsScenarioDefaultProvenance(t *testing.T) {
	// A revision that reuses its already-compiled script must not read the value a
	// previous compile stamped into that script as the script's own intent. The
	// effective value is the same either way; what regresses is the manifest's
	// account of which layer decided it.
	runner := NewTestRunner(2, NewHub(), nil)
	storeContinueOnErrorScript(runner, "script-1", "Checkout", nil)

	original, err := runner.CreateTest(CreateTestRequest{
		Name:     "Checkout Replay",
		Duration: "60s",
		Groups:   []CreateGroupEntry{{Name: "checkout", ScriptID: "script-1", VUsers: 5}},
	})
	if err != nil {
		t.Fatalf("CreateTest returned error: %v", err)
	}
	if source := original.Groups[0].ContinueOnError.Source; source != continueOnErrorSourceScenarioDefault {
		t.Fatalf("original source = %q, want %q", source, continueOnErrorSourceScenarioDefault)
	}
	// The compile stamps the resolved value onto the executable script; that is
	// the write-back this test guards against being read back as script intent.
	if !original.Groups[0].script.Config.ErrorHandling.ContinueOnErrorSet() {
		t.Fatal("expected the compiled script to carry the resolved value")
	}

	sourceGroupIndex := 0
	revision, err := runner.CreateTest(CreateTestRequest{
		Name:                 "Checkout Replay (revised)",
		Duration:             "60s",
		RevisionSourceTestID: original.ID,
		Groups: []CreateGroupEntry{{
			Name:                     "checkout",
			ScriptID:                 "script-1",
			VUsers:                   9, // only the workload changes; runtime settings are untouched
			RevisionSourceGroupIndex: &sourceGroupIndex,
		}},
	})
	if err != nil {
		t.Fatalf("revision CreateTest returned error: %v", err)
	}

	resolution := revision.Groups[0].ContinueOnError
	if resolution == nil {
		t.Fatal("revised group carries no continue-on-error resolution")
	}
	if !resolution.Effective || resolution.Conflict {
		t.Fatalf("revised resolution = %#v, want the enabled default with no conflict", resolution)
	}
	if resolution.Source != continueOnErrorSourceScenarioDefault {
		t.Fatalf("revised source = %q, want %q — the previous compile's write-back was read as script intent",
			resolution.Source, continueOnErrorSourceScenarioDefault)
	}
}
