package engine

import (
	"context"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// replayLeafNames returns the action names of executed leaf events, excluding
// the synthetic teardown leaf so loop-body assertions stay focused.
func replayLeafNames(report *ReplayReport) []string {
	var names []string
	for _, ev := range report.Events {
		if ev.Phase == "teardown" {
			continue
		}
		names = append(names, ev.ActionName)
	}
	return names
}

func ifLoopIndexEqReplay(value string, steps ...dsl.Action) dsl.Action {
	return dsl.Action{
		Name: "Guard",
		Type: "control_if",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Left: "{{var.loop_index}}", Op: "eq", Right: value},
			Steps:     steps,
		},
	}
}

func TestReplay_ControlBreakStopsLoop(t *testing.T) {
	proto := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "BreakLoop",
		Protocol: proto,
		Actions: []dsl.Action{{
			Name: "Repeat",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Count: 5,
				Steps: []dsl.Action{
					{Name: "Step", Type: "GET", Method: "GET", Target: "/s"},
					ifLoopIndexEqReplay("2", dsl.Action{Name: "Break", Type: "control_break"}),
				},
			},
		}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	// Step x2 (passes 1 and 2), Break leaf on pass 2, then the loop exits.
	names := replayLeafNames(report)
	want := []string{"Step", "Step", "Break"}
	if !equalStrings(names, want) {
		t.Fatalf("replay break leaves = %v, want %v", names, want)
	}
}

func TestReplay_ControlContinueSkipsRest(t *testing.T) {
	proto := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "ContinueLoop",
		Protocol: proto,
		Actions: []dsl.Action{{
			Name: "Repeat",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Count: 3,
				Steps: []dsl.Action{
					{Name: "A", Type: "GET", Method: "GET", Target: "/a"},
					ifLoopIndexEqReplay("2", dsl.Action{Name: "Continue", Type: "control_continue"}),
					{Name: "B", Type: "GET", Method: "GET", Target: "/b"},
				},
			},
		}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	// Pass 2 emits the Continue leaf then skips B; A runs every pass, B on 1 and 3.
	names := replayLeafNames(report)
	want := []string{"A", "B", "A", "Continue", "A", "B"}
	if !equalStrings(names, want) {
		t.Fatalf("replay continue leaves = %v, want %v", names, want)
	}
}

func TestReplay_ForEachIteratesRows(t *testing.T) {
	proto := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "ForEach",
		Protocol: proto,
		DataSources: []dsl.DataSource{{
			Name:           "users",
			Type:           "csv",
			InlineData:     "email\na@x.com\nb@x.com",
			FirstRowHeader: true,
			Strategy:       "sequential",
		}},
		Actions: []dsl.Action{{
			Name: "Loop",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				ForEach: &dsl.ForEachLoop{Source: "users", Var: "row"},
				Steps:   []dsl.Action{{Name: "Row", Type: "GET", Method: "GET", Target: "/{{var.row.email}}"}},
			},
		}},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	var targets []string
	var iterations []int
	for _, ev := range report.Events {
		if ev.ActionName != "Row" {
			continue
		}
		targets = append(targets, ev.Target)
		if len(ev.IterationPath) > 0 {
			iterations = append(iterations, ev.IterationPath[len(ev.IterationPath)-1])
		}
	}
	if !equalStrings(targets, []string{"/a@x.com", "/b@x.com"}) {
		t.Fatalf("foreach replay targets = %v, want [/a@x.com /b@x.com]", targets)
	}
	if len(iterations) != 2 || iterations[0] != 1 || iterations[1] != 2 {
		t.Fatalf("foreach iteration path = %v, want [1 2]", iterations)
	}
}

func TestReplay_EmitsConditionEvidence(t *testing.T) {
	proto := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	var conditions []ConditionEvidence
	_, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "EvidenceIf",
		Protocol: proto,
		Actions: []dsl.Action{{
			Name: "Branch",
			Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{var.role}}", Op: "eq", Right: "admin"},
				Steps:     []dsl.Action{{Name: "Then", Type: "GET", Method: "GET", Target: "/then"}},
				Else:      []dsl.Action{{Name: "Else", Type: "GET", Method: "GET", Target: "/else"}},
			},
		}},
	}, ReplayOptions{
		IgnoreThinkTime: true,
		Variables:       map[string]string{"role": "admin"},
		OnProgress: func(ev ReplayProgressEvent) {
			if ev.Type == ReplayProgressCondition && ev.Condition != nil {
				conditions = append(conditions, *ev.Condition)
			}
		},
	})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if len(conditions) != 1 {
		t.Fatalf("condition evidence events = %d, want 1", len(conditions))
	}
	ev := conditions[0]
	if ev.Left != "admin" || ev.Op != "eq" || ev.Right != "admin" || !ev.Result || ev.Branch != "then" {
		t.Fatalf("condition evidence = %+v, want left=admin op=eq right=admin result=true branch=then", ev)
	}
}

func TestReplay_ControlSwitchMatchesLiveFirstMatchSemantics(t *testing.T) {
	proto := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	var conditions []ConditionEvidence
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "Switch",
		Protocol: proto,
		Actions: []dsl.Action{{
			Name: "RoleSwitch",
			Type: "control_switch",
			Control: &dsl.ControlFlow{
				Cases: []dsl.SwitchCase{
					{Condition: &dsl.Condition{Expr: `var.role == "admin"`}, Steps: []dsl.Action{{Name: "Admin", Type: "GET", Target: "/admin"}}},
					{Condition: &dsl.Condition{Expr: `var.role == "member"`}, Steps: []dsl.Action{{Name: "Member", Type: "GET", Target: "/member"}}},
				},
				Default: []dsl.Action{{Name: "Guest", Type: "GET", Target: "/guest"}},
			},
		}},
	}, ReplayOptions{
		IgnoreThinkTime: true,
		Variables:       map[string]string{"role": "member"},
		OnProgress: func(ev ReplayProgressEvent) {
			if ev.Type == ReplayProgressCondition && ev.Condition != nil {
				conditions = append(conditions, *ev.Condition)
			}
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := replayLeafNames(report); !equalStrings(got, []string{"Member"}) {
		t.Fatalf("replay leaves = %v, want [Member]", got)
	}
	if len(conditions) != 2 || conditions[0].Result || !conditions[1].Result || conditions[1].Branch != "case[1]" {
		t.Fatalf("condition evidence = %+v", conditions)
	}
}

func TestReplay_BreakOutsideLoopIsNoOp(t *testing.T) {
	proto := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	report, err := ReplayScript(context.Background(), &dsl.Script{
		Name:     "StrayBreak",
		Protocol: proto,
		Actions: []dsl.Action{
			{Name: "Break", Type: "control_break"},
			{Name: "After", Type: "GET", Method: "GET", Target: "/after"},
		},
	}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	// The stray break emits its leaf but does not stop the phase; After still runs.
	names := replayLeafNames(report)
	if !equalStrings(names, []string{"Break", "After"}) {
		t.Fatalf("stray break replay leaves = %v, want [Break After]", names)
	}
}

func TestReplay_DoWhileRunsBodyOnceWhenConditionInitiallyFalse(t *testing.T) {
	proto := registerReplayProtocol(t, func(action *protocol.Action) (*protocol.Result, error) {
		return &protocol.Result{ActionName: action.Name, Success: true, StatusCode: 200}, nil
	})
	loop := func(doWhile bool) dsl.Action {
		return dsl.Action{
			Name: "Loop",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				// Unset var → the predicate is false on the very first check.
				Condition:     &dsl.Condition{Left: "{{var.flag}}", Op: "eq", Right: "go"},
				DoWhile:       doWhile,
				MaxIterations: 3,
				Steps:         []dsl.Action{{Name: "Body", Type: "GET", Method: "GET", Target: "/b"}},
			},
		}
	}
	pre, err := ReplayScript(context.Background(), &dsl.Script{Name: "Pre", Protocol: proto, Actions: []dsl.Action{loop(false)}}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if names := replayLeafNames(pre); len(names) != 0 {
		t.Fatalf("pre-test loop replayed body %v, want none", names)
	}
	dw, err := ReplayScript(context.Background(), &dsl.Script{Name: "DoWhile", Protocol: proto, Actions: []dsl.Action{loop(true)}}, ReplayOptions{IgnoreThinkTime: true})
	if err != nil {
		t.Fatalf("ReplayScript: %v", err)
	}
	if names := replayLeafNames(dw); len(names) != 1 || names[0] != "Body" {
		t.Fatalf("do_while loop replayed body %v, want exactly [Body]", names)
	}
}
