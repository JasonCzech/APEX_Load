package engine

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestRetiredBrunoRuntimeRejectsEveryActionPhaseAndBranch(t *testing.T) {
	retired := dsl.Action{Name: "Legacy collection", Type: "bruno_run"}
	cases := map[string]*dsl.Script{
		"protocol": {Protocol: " BrUnO "},
		"init":     {Init: []dsl.Action{retired}},
		"actions":  {Actions: []dsl.Action{retired}},
		"teardown": {Teardown: []dsl.Action{retired}},
		"steps":    {Actions: []dsl.Action{{Control: &dsl.ControlFlow{Steps: []dsl.Action{retired}}}}},
		"else":     {Actions: []dsl.Action{{Control: &dsl.ControlFlow{Else: []dsl.Action{retired}}}}},
		"switch":   {Actions: []dsl.Action{{Control: &dsl.ControlFlow{Cases: []dsl.SwitchCase{{Steps: []dsl.Action{retired}}}}}}},
		"default":  {Actions: []dsl.Action{{Control: &dsl.ControlFlow{Default: []dsl.Action{retired}}}}},
		"parallel": {Actions: []dsl.Action{{Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Steps: []dsl.Action{retired}}}}}}},
		"sequence": {Actions: []dsl.Action{{Type: "http_sequence", Params: map[string]any{"requests": []dsl.Action{retired}}}}},
	}
	for name, script := range cases {
		t.Run(name, func(t *testing.T) {
			err := RetiredBrunoRuntimeError(script)
			if err == nil || !strings.Contains(err.Error(), dsl.BrunoRunnerRetiredMessage) {
				t.Fatalf("retirement error = %v", err)
			}
			if _, err := New(Config{Script: script, VUsers: 1}); err == nil || !strings.Contains(err.Error(), "Bruno Runner has been retired") {
				t.Fatalf("engine admission error = %v", err)
			}
		})
	}
}

func TestRetiredBrunoRuntimeAllowsNativeImportedScripts(t *testing.T) {
	script := &dsl.Script{Protocol: "http", Actions: []dsl.Action{{Type: "GET", Metadata: map[string]any{"source_profile": "bruno_native"}}}}
	if err := RetiredBrunoRuntimeError(script); err != nil {
		t.Fatal(err)
	}
	if err := RetiredBrunoRuntimeError(nil); err != nil {
		t.Fatal(err)
	}
}
