package engine

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// TestWarnStrayControlFlowDedupes verifies the stray break/continue warning is
// emitted once per (action, phase) per process, so it cannot flood the logs at a
// high VUser/iteration count.
func TestWarnStrayControlFlowDedupes(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	for i := 0; i < 5; i++ {
		warnStrayControlFlow(logger, "StrayDedupeProbe", "actions")
	}
	if got := strings.Count(buf.String(), "StrayDedupeProbe"); got != 1 {
		t.Fatalf("stray-control-flow warning emitted %d times, want 1 (deduped)", got)
	}
}

// TestConditionExprNestedBindingWinsDeterministically verifies that when a plain
// variable and a dotted (foreach-style) binding share a prefix, the nested map
// wins regardless of Go's randomized map iteration order — so a CEL condition is
// deterministic across identical runs/replays.
func TestConditionExprNestedBindingWinsDeterministically(t *testing.T) {
	for i := 0; i < 200; i++ {
		vu := &VUser{Variables: map[string]string{"row": "scalar", "row.email": "a@b.com"}}
		if !vu.evalCondition(&dsl.Condition{Expr: `var.row.email == "a@b.com"`}) {
			t.Fatalf("iteration %d: var.row.email unreachable (a flat var clobbered the nested map)", i)
		}
	}
}

// TestLifecycleControlLoopHonorsBreak verifies control_break inside an
// init/teardown control_loop breaks the loop (previously it was silently ignored
// in lifecycle phases, diverging from the main and replay paths).
func TestLifecycleControlLoopHonorsBreak(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, nil)
	ok := vu.RunLifecycleBlock(context.Background(), []dsl.Action{{
		Name: "InitLoop",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Count: 5,
			Steps: []dsl.Action{
				click("InitStep"),
				{
					Name: "MaybeBreak",
					Type: "control_if",
					Control: &dsl.ControlFlow{
						Condition: &dsl.Condition{Expr: "int(var.loop_index) >= 2"},
						Steps:     []dsl.Action{breakAction()},
					},
				},
			},
		},
	}}, "init")
	if !ok {
		t.Fatal("RunLifecycleBlock returned false, want true")
	}
	// Pass 1 runs InitStep; pass 2 runs InitStep then breaks -> 2 steps, not 5.
	if len(proto.names) != 2 {
		t.Fatalf("lifecycle loop executed %d steps, want 2 (break honored at pass 2)", len(proto.names))
	}
}

// TestLifecycleStrayBreakIsNoOp verifies a control_break with no enclosing loop
// in a lifecycle phase is a no-op (does not abort the phase), matching the main
// and replay paths.
func TestLifecycleStrayBreakIsNoOp(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, nil)
	ok := vu.RunLifecycleBlock(context.Background(), []dsl.Action{
		click("First"),
		breakAction(),
		click("Second"),
	}, "init")
	if !ok {
		t.Fatal("stray break must not abort the lifecycle phase")
	}
	if len(proto.names) != 2 {
		t.Fatalf("executed %d steps, want 2 (stray break is a no-op, Second still runs)", len(proto.names))
	}
}
