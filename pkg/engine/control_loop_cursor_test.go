package engine

import (
	"context"
	"maps"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func loopTraceAction(name, target string) dsl.Action {
	return dsl.Action{Name: name, Type: "GET", Method: "GET", Target: target}
}

func loopTraceSources() []dsl.DataSource {
	return []dsl.DataSource{{Name: "rows", Type: "csv", InlineData: "id,extra\na,one\nb,two\nc,three", FirstRowHeader: true, Strategy: "sequential"}}
}

func TestControlLoopDifferentialTraces(t *testing.T) {
	falseCondition := &dsl.Condition{Left: "no", Right: "yes"}
	tests := []struct {
		name     string
		actions  []dsl.Action
		cancelAt string
		want     []string
	}{
		{
			name: "nested foreach and count restore break and continue",
			actions: []dsl.Action{{Name: "rows", Type: "control_loop", Control: &dsl.ControlFlow{
				ForEach: &dsl.ForEachLoop{Source: "rows", Var: "row"}, MaxIterations: 2,
				Steps: []dsl.Action{
					loopTraceAction("outer", "/outer/{{var.loop_index}}/{{var.row.id}}"),
					{Name: "inner", Type: "control_loop", Control: &dsl.ControlFlow{Count: 4, Steps: []dsl.Action{
						ifLoopIndexEqReplay("2", dsl.Action{Name: "skip", Type: "control_continue"}),
						loopTraceAction("inner", "/inner/{{var.loop_index}}/{{var.row.id}}"),
						ifLoopIndexEqReplay("3", dsl.Action{Name: "stop", Type: "control_break"}),
					}}},
					loopTraceAction("restored", "/restored/{{var.loop_index}}/{{var.row.id}}"),
				},
			}}, loopTraceAction("after", "/after/{{var.loop_index}}/{{var.row.id}}")},
			want: []string{"/outer/1/a", "/inner/1/a", "/inner/3/a", "/restored/1/a", "/outer/2/b", "/inner/1/b", "/inner/3/b", "/restored/2/b", "/after/outer/original"},
		},
		{
			name: "post test and pre test condition bounds",
			actions: []dsl.Action{
				{Name: "post", Type: "control_loop", Control: &dsl.ControlFlow{DoWhile: true, Condition: falseCondition, Steps: []dsl.Action{loopTraceAction("once", "/once/{{var.loop_index}}")}}},
				{Name: "pre", Type: "control_loop", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "{{var.loop_index}}", Op: "lte", Right: "2"}, Steps: []dsl.Action{loopTraceAction("pre", "/pre/{{var.loop_index}}")}}},
				{Name: "capped", Type: "control_loop", Control: &dsl.ControlFlow{Count: 8, MaxIterations: 2, Steps: []dsl.Action{loopTraceAction("cap", "/cap/{{var.loop_index}}")}}},
			},
			want: []string{"/once/1", "/pre/1", "/pre/2", "/cap/1", "/cap/2"},
		},
		{
			name:    "foreach predicate observes binding before body",
			actions: []dsl.Action{{Name: "rows", Type: "control_loop", Control: &dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "rows", Var: "row", Condition: &dsl.Condition{Left: "{{var.row.id}}", Op: "ne", Right: "b"}}, Steps: []dsl.Action{loopTraceAction("row", "/row/{{var.row.id}}")}}}},
			want:    []string{"/row/a"},
		},
		{
			name: "cancellation restores nested bindings",
			actions: []dsl.Action{{Name: "rows", Type: "control_loop", Control: &dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "rows", Var: "row"}, Steps: []dsl.Action{
				{Name: "nested", Type: "control_loop", Control: &dsl.ControlFlow{Count: 3, Steps: []dsl.Action{loopTraceAction("cancel", "/cancel/{{var.loop_index}}/{{var.row.id}}"), loopTraceAction("unreachable", "/unreachable")}}},
			}}}},
			cancelAt: "cancel", want: []string{"/cancel/1/a"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for _, mode := range []string{"main", "init", "teardown", "replay"} {
				t.Run(mode, func(t *testing.T) {
					ctx, cancel := context.WithCancel(context.Background())
					defer cancel()
					var trace []string
					execute := func(action *protocol.Action) (*protocol.Result, error) {
						trace = append(trace, action.Target)
						if action.Name == tt.cancelAt {
							cancel()
						}
						return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
					}
					variables := map[string]string{"loop_index": "outer", "row.id": "original"}
					var final map[string]string
					if mode == "replay" {
						name := registerReplayProtocol(t, execute)
						report, err := ReplayScript(ctx, &dsl.Script{Name: "loops", Protocol: name, Actions: tt.actions, DataSources: loopTraceSources()}, ReplayOptions{IgnoreThinkTime: true, Variables: variables})
						if err != nil {
							t.Fatal(err)
						}
						final = report.FinalVariables
					} else {
						vu := newControlFlowVUser(&replayTestProtocol{execute: execute}, maps.Clone(variables), tt.actions)
						var err error
						vu.DataProvider, err = NewDataProvider(loopTraceSources())
						if err != nil {
							t.Fatal(err)
						}
						if mode == "main" {
							vu.Run(ctx)
						} else {
							vu.RunLifecycleBlock(ctx, tt.actions, mode)
						}
						final = vu.Variables
					}
					if !reflect.DeepEqual(trace, tt.want) {
						t.Fatalf("trace = %v, want %v", trace, tt.want)
					}
					for key, want := range variables {
						if final[key] != want {
							t.Errorf("binding %s = %q, want %q", key, final[key], want)
						}
					}
					if _, exists := final["row.extra"]; exists {
						t.Errorf("new foreach binding leaked: %v", final)
					}
				})
			}
		})
	}
}

func TestControlLoopCursorPrecedenceAndSafetyCaps(t *testing.T) {
	falseCondition := &dsl.Condition{Left: "false", Right: "true"}
	for _, tt := range []struct {
		name            string
		control         dsl.ControlFlow
		want            int
		conditionChecks int
		capReached      bool
	}{
		{"foreach wins", dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "rows", Var: "row"}, Count: 1, Condition: falseCondition}, 3, 0, false},
		{"count wins", dsl.ControlFlow{Count: 2, Condition: falseCondition, DoWhile: true}, 2, 0, false},
		{"empty foreach", dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "missing", Var: "row"}, Count: 2}, 0, 0, false},
		{"implicit condition cap", dsl.ControlFlow{}, defaultControlLoopMaxIterations, defaultControlLoopMaxIterations, true},
		{"explicit condition cap", dsl.ControlFlow{MaxIterations: 2, DoWhile: true}, 2, 1, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			provider, err := NewDataProvider(loopTraceSources())
			if err != nil {
				t.Fatal(err)
			}
			vu := &VUser{Variables: map[string]string{}, DataProvider: provider}
			cursor := newControlLoopCursor(vu, &tt.control)
			count, checks := 0, 0
			for cursor.pending() {
				pass, ok := cursor.next()
				if !ok {
					break
				}
				count++
				if pass.index != count {
					t.Fatalf("index = %d, want %d", pass.index, count)
				}
				if pass.checkCondition {
					checks++
				}
			}
			if count != tt.want || checks != tt.conditionChecks || cursor.conditionCapReached != tt.capReached {
				t.Fatalf("passes/checks/cap = %d/%d/%v, want %d/%d/%v", count, checks, cursor.conditionCapReached, tt.want, tt.conditionChecks, tt.capReached)
			}
			cursor.restoreBindings()
			cursor.restoreBindings()
			if len(vu.Variables) != 0 {
				t.Fatalf("bindings leaked: %v", vu.Variables)
			}
		})
	}
}
