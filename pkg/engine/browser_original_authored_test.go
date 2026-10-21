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

func TestBrowserOriginalSourceSurvivesControllerRuntimeOverrides(t *testing.T) {
	source := authoredBrowserFixture()
	definition := BrowserOriginalAuthoredDefinition(source)
	if !definition.Valid() {
		t.Fatal("source descriptor unavailable")
	}
	raw, _ := json.Marshal(source)
	var runtime dsl.Script
	if err := json.Unmarshal(raw, &runtime); err != nil {
		t.Fatal(err)
	}
	runtime.Config.Browser.HARArtifactDir = t.TempDir()
	runtime.Config.RunLogic.Iterations = 1
	runtime.Config.Pacing = dsl.PacingConfig{Type: "tps", TPS: 2}
	if replayScriptHash(&runtime) == definition.ScriptSHA256 {
		t.Fatal("fixture lacks runtime override")
	}
	vu := &VUser{Script: &runtime, browserAuthoredDefinition: definition}
	for _, path := range []string{"init[0]", "actions[0]", "actions[1]", "actions[2].control.steps[0].control.steps[0]", "teardown[0]"} {
		index := vu.browserAuthoredIndex()
		action := index.byPath[path]
		got := vu.resolveBrowserAuthoredAction(*action, action).BrowserAuthoredStep
		want := protocol.NewBrowserOriginalAuthoredStep(definition, path)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("%s identity=%+v want=%+v", path, got, want)
		}
	}
	// A changed descriptor invalidates the cached index; malformed proof cannot
	// silently become legacy identity or retain the earlier valid proof.
	vu.browserAuthoredDefinition.StructureSHA256 = strings.Repeat("f", 64)
	if vu.browserAuthoredIndex() != nil {
		t.Fatal("stale source descriptor cache")
	}
	runtime.Actions[0].Type = "browser_fill"
	if newBrowserOriginalAuthoredIndex(&runtime, nil, definition) != nil {
		t.Fatal("changed structure qualified")
	}
	if BrowserOriginalAuthoredDefinition(nil).Valid() || BrowserOriginalAuthoredDefinition(&dsl.Script{Protocol: "http"}).Valid() {
		t.Fatal("non-browser source qualified")
	}
}

func TestBrowserOriginalSourceCannotBeInjectedThroughRuntimeOptionsJSON(t *testing.T) {
	body := []byte(`{"BrowserAuthoredDefinition":{"version":1,"script_sha256":"` + strings.Repeat("a", 64) + `","structure_sha256":"` + strings.Repeat("b", 64) + `"}}`)
	var single Config
	var group GroupConfig
	var replay ReplayOptions
	for _, dst := range []any{&single, &group, &replay} {
		if err := json.Unmarshal(body, dst); err != nil {
			t.Fatal(err)
		}
	}
	if single.BrowserAuthoredDefinition != (protocol.BrowserAuthoredDefinition{}) || group.BrowserAuthoredDefinition != (protocol.BrowserAuthoredDefinition{}) || replay.BrowserAuthoredDefinition != (protocol.BrowserAuthoredDefinition{}) {
		t.Fatal("JSON created original-source authority")
	}
}

func TestBrowserOriginalSourceReachesLifecycleAndReplayDispatch(t *testing.T) {
	source := &dsl.Script{Protocol: "browser", Init: []dsl.Action{{Name: "same", Type: "browser_click"}}, Actions: []dsl.Action{{Name: "same", Type: "browser_click"}}}
	definition := BrowserOriginalAuthoredDefinition(source)
	runtime := cloneSingleIterationScript(source)
	var got []*protocol.BrowserAuthoredStep
	spy := &replayTestProtocol{execute: func(action *protocol.Action) (*protocol.Result, error) {
		got = append(got, action.BrowserAuthoredStep.Clone())
		return &protocol.Result{Success: true, StatusCode: 200, StartTime: time.Now()}, nil
	}}
	vu := &VUser{Script: runtime, browserAuthoredDefinition: definition, Proto: spy, Variables: map[string]string{}, Metrics: NewMetricsCollector(32)}
	if !vu.RunLifecycleBlock(context.Background(), runtime.Init, "init") {
		t.Fatal("lifecycle failed")
	}
	report := &ReplayReport{ReplaySessionID: "original-source", Success: true}
	leaf := 0
	_, ok, abort := runReplayUnit(context.Background(), vu, replayUnit{action: runtime.Actions[0], phase: "action", actionIndex: 0}, &leaf, false, true, protocol.ThinkTimeConfig{}, report, nil, nil)
	if !ok || abort || len(got) != 2 {
		t.Fatal("dispatch failed")
	}
	for i, path := range []string{"init[0]", "actions[0]"} {
		if !reflect.DeepEqual(got[i], protocol.NewBrowserOriginalAuthoredStep(definition, path)) {
			t.Fatal("source proof lost at dispatch", got[i])
		}
	}
}

func TestBrowserOriginalSourceReplayEventClone(t *testing.T) {
	definition := BrowserOriginalAuthoredDefinition(authoredBrowserFixture())
	event := ReplayActionEvent{AuthoredStep: protocol.NewBrowserOriginalAuthoredStep(definition, "actions[1]")}
	copy := cloneReplayActionEvent(event)
	if copy.AuthoredStep == event.AuthoredStep || !reflect.DeepEqual(copy.AuthoredStep, event.AuthoredStep) {
		t.Fatal("replay clone lost or aliased original source")
	}
	copy.AuthoredStep.StepID = "changed"
	if !event.AuthoredStep.Valid() {
		t.Fatal("clone mutated original")
	}
	event.AuthoredStep.StepID = "invalid"
	if cloneReplayActionEvent(event).AuthoredStep != nil {
		t.Fatal("clone preserved invalid evidence")
	}
}

func TestBrowserOriginalSourcePreparedVUserRetainsBinding(t *testing.T) {
	source := authoredBrowserFixture()
	definition := BrowserOriginalAuthoredDefinition(source)
	prepared := &preparedVUser{runtimeID: 7, script: source, group: GroupConfig{Name: "browser", Script: source, RunID: "run", VUsers: 2, BrowserAuthoredDefinition: definition, InitStrategy: "at_once"}}
	mge := &MultiGroupEngine{}
	vu := mge.newPreparedVUser(prepared)
	if vu.ID != 7 || vu.Script != source || vu.browserAuthoredSource != source || vu.browserAuthoredDefinition != definition || vu.Group != "browser" || vu.RunID != "run" || !vu.SkipInit || !vu.SkipTeardown {
		t.Fatal("prepared constructor lost runtime or source binding")
	}
	prepared.group.BrowserAuthoredDefinition = protocol.BrowserAuthoredDefinition{}
	if !vu.browserAuthoredDefinition.Valid() {
		t.Fatal("prepared state mutated VUser source binding")
	}
}
