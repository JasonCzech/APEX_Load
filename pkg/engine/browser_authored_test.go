package engine

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func authoredBrowserFixture() *dsl.Script {
	click := func() dsl.Action {
		return dsl.Action{Name: "duplicate", Type: "browser_click", Selector: "{{var.selector}}"}
	}
	return &dsl.Script{Protocol: "browser", Init: []dsl.Action{click()}, Actions: []dsl.Action{click(), click(), {Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{{Name: "if", Type: "control_if", Control: &dsl.ControlFlow{Steps: []dsl.Action{click()}, Else: []dsl.Action{click()}}}}}}, {Name: "switch", Type: "control_switch", Control: &dsl.ControlFlow{Cases: []dsl.SwitchCase{{Steps: []dsl.Action{click()}}}, Default: []dsl.Action{click()}}}, {Name: "parallel", Type: "control_parallel", Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Steps: []dsl.Action{click()}}}}}}, Teardown: []dsl.Action{click()}}
}

func TestBrowserAuthoredIdentityMatchesLoadAndReplayLocations(t *testing.T) {
	script := authoredBrowserFixture()
	before, _ := json.Marshal(script)
	vu := &VUser{Script: script, Variables: map[string]string{"selector": "#first"}}
	index := vu.browserAuthoredIndex()
	if index == nil {
		t.Fatal("missing index")
	}
	seen := map[string]bool{}
	for _, tc := range []struct {
		phase     string
		top       int
		path      []string
		canonical string
	}{
		{"init", 0, nil, "init[0]"}, {"action", 0, nil, "actions[0]"}, {"action", 1, nil, "actions[1]"},
		{"action", 2, []string{"loop[0]", "then[0]"}, "actions[2].control.steps[0].control.steps[0]"},
		{"action", 2, []string{"loop[0]", "else[0]"}, "actions[2].control.steps[0].control.else[0]"},
		{"action", 3, []string{"case[0][0]"}, "actions[3].control.cases[0].steps[0]"},
		{"action", 3, []string{"default[0]"}, "actions[3].control.default[0]"},
		{"action", 4, []string{"branches[0].steps[0]"}, "actions[4].control.branches[0].steps[0]"},
		{"teardown", 0, nil, "teardown[0]"},
	} {
		t.Run(tc.canonical, func(t *testing.T) {
			source := index.byPath[tc.canonical]
			if source == nil {
				t.Fatal("missing source")
			}
			load := vu.resolveBrowserAuthoredAction(*source, source)
			replay := vu.browserReplayAuthoredStep(replayUnit{action: *source, phase: tc.phase, actionIndex: tc.top, stepPath: tc.path, iterationPath: []int{7, 11}, leafOrdinal: 92})
			if !load.BrowserAuthoredStep.Valid() || !reflect.DeepEqual(load.BrowserAuthoredStep, replay) {
				t.Fatalf("identities disagree: load=%+v replay=%+v", load.BrowserAuthoredStep, replay)
			}
			if seen[replay.StepID] {
				t.Fatal("distinct duplicate-named steps collapsed")
			}
			seen[replay.StepID] = true
			vu.Variables["selector"] = "#second"
			repeated := vu.resolveBrowserAuthoredAction(*source, source)
			if !reflect.DeepEqual(repeated.BrowserAuthoredStep, replay) {
				t.Fatal("interpolation or repeated execution changed identity")
			}
		})
	}
	after, _ := json.Marshal(script)
	if string(before) != string(after) {
		t.Fatal("identity construction changed authored script")
	}
	cloned := &dsl.Script{}
	if err := json.Unmarshal(before, cloned); err != nil {
		t.Fatal(err)
	}
	rebuilt := newBrowserAuthoredIndex(cloned)
	if rebuilt.byAction[&cloned.Actions[0]].StepID != index.byAction[&script.Actions[0]].StepID {
		t.Fatal("wire round trip changed authored identity")
	}
	cloned.Actions[0].Selector = "#changed"
	changed := newBrowserAuthoredIndex(cloned)
	if changed.byAction[&cloned.Actions[1]].StepID == index.byAction[&script.Actions[1]].StepID {
		t.Fatal("script revision did not invalidate dependent step identity")
	}
}

func TestBrowserAuthoredIdentityLeavesUnknownSourcesUnavailable(t *testing.T) {
	script := authoredBrowserFixture()
	vu := &VUser{Script: script}
	foreign := script.Actions[0]
	if vu.resolveBrowserAuthoredAction(foreign, &foreign).BrowserAuthoredStep != nil {
		t.Fatal("equal-looking detached action adopted a location")
	}
	for _, unit := range []replayUnit{
		{action: foreign, phase: "action", actionIndex: -1},
		{action: foreign, phase: "action", actionIndex: 99},
		{action: foreign, phase: "unknown", actionIndex: 0},
		{action: foreign, phase: "action", actionIndex: 0, stepPath: []string{"requests[0]"}},
		{action: foreign, phase: "action", actionIndex: 2, stepPath: []string{"loop[0]", "then[01]"}},
		{action: foreign, phase: "action", actionIndex: 0, stepPath: []string{strings.Repeat("x", 65)}},
		{action: dsl.Action{Type: "browser_fill"}, phase: "action", actionIndex: 0},
	} {
		if vu.browserReplayAuthoredStep(unit) != nil {
			t.Fatalf("unknown replay location got identity: %+v", unit)
		}
	}
	script2 := authoredBrowserFixture()
	script2.Actions[0].Target = "/different"
	vu.Script = script2
	if got := vu.resolveBrowserAuthoredAction(script2.Actions[0], &script2.Actions[0]).BrowserAuthoredStep; got == nil || got.ScriptSHA256 != replayScriptHash(script2) {
		t.Fatal("replaced script retained the previous index")
	}
	for _, bad := range []*dsl.Script{nil, {Protocol: "http"}, {Protocol: "browser", Init: script.Actions, Actions: script.Actions}, {Protocol: "browser", Actions: []dsl.Action{{Params: map[string]any{"unsupported": func() {}}}}}} {
		if newBrowserAuthoredIndex(bad) != nil {
			t.Fatal("invalid/non-browser script acquired an index")
		}
	}
	cyclic := &dsl.Script{Protocol: "browser", Actions: []dsl.Action{{Type: "control_loop"}}}
	cyclic.Actions[0].Control = &dsl.ControlFlow{Steps: cyclic.Actions}
	if newBrowserAuthoredIndex(cyclic) != nil {
		t.Fatal("cyclic action graph acquired a partial index")
	}
}

func TestBrowserAuthoredIdentityReachesLifecycleAndReplayProtocol(t *testing.T) {
	script := &dsl.Script{Protocol: "browser", Init: []dsl.Action{{Name: "same", Type: "browser_click"}}, Actions: []dsl.Action{{Name: "same", Type: "browser_click"}}}
	var got []*protocol.BrowserAuthoredStep
	spy := &replayTestProtocol{execute: func(action *protocol.Action) (*protocol.Result, error) {
		got = append(got, action.BrowserAuthoredStep.Clone())
		return &protocol.Result{Success: true, StatusCode: 200, StartTime: time.Now()}, nil
	}}
	vu := &VUser{Script: script, Proto: spy, Variables: map[string]string{}, Metrics: NewMetricsCollector(32)}
	if !vu.RunLifecycleBlock(context.Background(), script.Init, "init") {
		t.Fatal("lifecycle failed")
	}
	report := &ReplayReport{ReplaySessionID: "authored", Success: true}
	leaf := 0
	_, ok, abort := runReplayUnit(context.Background(), vu, replayUnit{action: script.Actions[0], phase: "action", actionIndex: 0}, &leaf, false, true, protocol.ThinkTimeConfig{}, report, nil, nil)
	if !ok || abort || len(got) != 2 || !got[0].Valid() || !got[1].Valid() {
		t.Fatalf("identities did not reach execution: %+v ok=%t abort=%t", got, ok, abort)
	}
	if got[0].StepPath != "init[0]" || got[1].StepPath != "actions[0]" || got[0].StepID == got[1].StepID {
		t.Fatal("phase identity collapsed")
	}
}

func TestBrowserAuthoredIdentitySurvivesLoadRetriesAndReplayOrder(t *testing.T) {
	script := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{
		RunLogic: dsl.RunLogicConfig{Iterations: 2}, ErrorHandling: dsl.ErrorHandlingConfig{MaxRetries: 1, RetryDelayMs: 1},
	}, Actions: []dsl.Action{
		{Name: "same", Type: "browser_click"},
		{Name: "loop", Type: "control_loop", Control: &dsl.ControlFlow{Count: 2, Steps: []dsl.Action{{Name: "same", Type: "browser_click"}}}},
	}}
	var got []*protocol.BrowserAuthoredStep
	spy := &replayTestProtocol{execute: func(action *protocol.Action) (*protocol.Result, error) {
		got = append(got, action.BrowserAuthoredStep.Clone())
		return &protocol.Result{Success: len(got) != 1, StatusCode: 200, StartTime: time.Now()}, nil
	}}
	vu := &VUser{Script: script, Proto: spy, Variables: map[string]string{}, Metrics: NewMetricsCollector(32)}
	vu.Run(context.Background())
	want := []string{"actions[0]", "actions[0]", "actions[1].control.steps[0]", "actions[1].control.steps[0]", "actions[0]", "actions[1].control.steps[0]", "actions[1].control.steps[0]"}
	check := func(want []string) {
		t.Helper()
		if len(got) != len(want) {
			t.Fatalf("dispatches=%d want=%d", len(got), len(want))
		}
		for i, path := range want {
			expected := protocol.NewBrowserAuthoredStep(replayScriptHash(script), path)
			if !reflect.DeepEqual(got[i], expected) {
				t.Fatalf("dispatch %d identity=%+v want=%+v", i, got[i], expected)
			}
		}
	}
	check(want)
	got = nil
	spy.execute = func(action *protocol.Action) (*protocol.Result, error) {
		got = append(got, action.BrowserAuthoredStep.Clone())
		return &protocol.Result{Success: true, StatusCode: 200, StartTime: time.Now()}, nil
	}
	report := &ReplayReport{ReplaySessionID: "reordered", Success: true}
	if !runReplayPhaseInOrder(context.Background(), vu, script.Actions, []int{1, 0, 1}, "action", false, true, protocol.ThinkTimeConfig{}, report, nil, nil) {
		t.Fatal("reordered replay failed")
	}
	check([]string{"actions[1].control.steps[0]", "actions[1].control.steps[0]", "actions[0]", "actions[1].control.steps[0]", "actions[1].control.steps[0]"})
}

func TestBrowserAuthoredIdentityStructureBounds(t *testing.T) {
	for _, depth := range []int{dsl.MaxScriptActionDepth, dsl.MaxScriptActionDepth + 1} {
		action := dsl.Action{Type: "browser_click"}
		for i := 1; i < depth; i++ {
			action = dsl.Action{Type: "control_loop", Control: &dsl.ControlFlow{Steps: []dsl.Action{action}}}
		}
		index := newBrowserAuthoredIndex(&dsl.Script{Protocol: "browser", Actions: []dsl.Action{action}})
		if (index != nil) != (depth == dsl.MaxScriptActionDepth) {
			t.Fatalf("depth %d availability=%t", depth, index != nil)
		}
	}
	for _, count := range []int{dsl.MaxScriptActionDefinitions, dsl.MaxScriptActionDefinitions + 1} {
		index := newBrowserAuthoredIndex(&dsl.Script{Protocol: "browser", Actions: make([]dsl.Action, count)})
		if (index != nil) != (count == dsl.MaxScriptActionDefinitions) {
			t.Fatalf("count %d availability=%t", count, index != nil)
		}
	}
}

func TestBrowserAuthoredIdentityPreservesSourceAcrossRuntimeCopies(t *testing.T) {
	source := &dsl.Script{Protocol: "browser", Config: dsl.ScriptConfig{RunLogic: dsl.RunLogicConfig{Iterations: 7}, Pacing: dsl.PacingConfig{Type: "tps", TPS: 3}}, Actions: []dsl.Action{{Name: "observer {{identity.username}}", Type: "browser_fill", Selector: "#name", Value: "{{identity.username}}"}}}
	observer, err := source.WithBrowserIdentity(dsl.BrowserIdentity{Username: "observer@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	want := protocol.NewBrowserAuthoredStep(replayScriptHash(source), "actions[0]")
	for _, runtime := range []*dsl.Script{cloneSingleIterationScript(source), observer, cloneSingleIterationScript(observer)} {
		if replayScriptHash(runtime) == replayScriptHash(source) {
			t.Fatal("fixture did not change runtime serialization")
		}
		vu := &VUser{Script: runtime, browserAuthoredSource: source}
		got := vu.resolveBrowserAuthoredAction(runtime.Actions[0], &runtime.Actions[0]).BrowserAuthoredStep
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("runtime copy identity=%+v want=%+v", got, want)
		}
	}
	changed := authoredBrowserFixture()
	if newBrowserAuthoredIndex(changed, source) != nil {
		t.Fatal("structurally changed runtime acquired a source identity")
	}
	copy := *source
	copy.Actions = append([]dsl.Action(nil), source.Actions...)
	copy.Actions[0].Type = "browser_click"
	if newBrowserAuthoredIndex(&copy, source) != nil {
		t.Fatal("changed action type acquired a source identity")
	}
}
