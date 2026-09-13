package api

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/engine"
)

func TestDebugBreakpointConditions(t *testing.T) {
	zero, one := 0, 1
	tests := []struct {
		name   string
		bp     ScriptDebugBreakpoint
		action engine.ReplayDebugAction
		wants  []bool
	}{
		{"disabled", ScriptDebugBreakpoint{Phase: "action", Disabled: true}, engine.ReplayDebugAction{Phase: "action"}, []bool{false, false}},
		{"second eligible hit", ScriptDebugBreakpoint{Phase: "action", HitCount: 2}, engine.ReplayDebugAction{Phase: "action"}, []bool{false, true, false}},
		{"iteration zero", ScriptDebugBreakpoint{Phase: "action", Iteration: &zero, StepPath: []string{"loop[0]"}}, engine.ReplayDebugAction{Phase: "action", Iteration: &zero, StepPath: []string{"loop[0]"}}, []bool{true, true}},
		{"wrong iteration", ScriptDebugBreakpoint{Phase: "action", Iteration: &one}, engine.ReplayDebugAction{Phase: "action", Iteration: &zero}, []bool{false}},
		{"no iteration", ScriptDebugBreakpoint{Phase: "action", Iteration: &zero}, engine.ReplayDebugAction{Phase: "action"}, []bool{false}},
		{"one shot", ScriptDebugBreakpoint{Phase: "action", Once: true}, engine.ReplayDebugAction{Phase: "action"}, []bool{true, false}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			session := &ScriptDebugSession{Breakpoints: []ScriptDebugBreakpoint{tt.bp}}
			for index, want := range tt.wants {
				if got := session.hasBreakpointLocked(tt.action); got != want {
					t.Fatalf("visit %d: got %v, want %v", index, got, want)
				}
			}
		})
	}
}
func TestDebugBreakpointNormalizationCopiesConditions(t *testing.T) {
	iteration := 0
	source := []ScriptDebugBreakpoint{{Phase: "action", Iteration: &iteration, HitCount: 2, Disabled: true}, {Phase: "action", Once: true}, {Phase: "action", ActionIndex: 1, HitCount: -1}}
	actual := normalizeDebugBreakpoints(source)
	if len(actual) != 2 || !actual[0].Disabled || actual[0].HitCount != 2 || !actual[1].Once {
		t.Fatalf("normalized: %#v", actual)
	}
	iteration = 5
	if *actual[0].Iteration != 0 {
		t.Fatal("normalization aliases caller-owned iteration")
	}
	session := &ScriptDebugSession{Breakpoints: actual}
	clone := session.snapshotLocked()
	*clone.Breakpoints[0].Iteration = 9
	if *session.Breakpoints[0].Iteration != 0 {
		t.Fatal("snapshot aliases live iteration")
	}
}
func TestRunToPreservesPermanentBreakpoint(t *testing.T) {
	session := &ScriptDebugSession{Breakpoints: normalizeDebugBreakpoints([]ScriptDebugBreakpoint{{Phase: "action", Once: true}, {Phase: "action", HitCount: 2}})}
	action := engine.ReplayDebugAction{Phase: "action"}
	if !session.hasBreakpointLocked(action) || len(session.Breakpoints) != 1 {
		t.Fatal("run-to target was not consumed")
	}
	if !session.hasBreakpointLocked(action) || session.hasBreakpointLocked(action) {
		t.Fatal("permanent hit-count breakpoint was not preserved")
	}
}

func TestOverlappingBreakpointsCountEveryEligibleVisit(t *testing.T) {
	session := &ScriptDebugSession{Breakpoints: []ScriptDebugBreakpoint{
		{Phase: "action"},
		{Phase: "action", StepPath: []string{"loop[0]"}, HitCount: 2},
	}}
	action := engine.ReplayDebugAction{Phase: "action", StepPath: []string{"loop[0]"}}
	if !session.hasBreakpointLocked(action) {
		t.Fatal("root breakpoint did not match")
	}
	action.LeafOrdinal = 1
	if !session.hasBreakpointLocked(action) {
		t.Fatal("overlapping leaf did not count its first visit")
	}
	if session.hasBreakpointLocked(action) {
		t.Fatal("exact hit condition repeated")
	}
}
