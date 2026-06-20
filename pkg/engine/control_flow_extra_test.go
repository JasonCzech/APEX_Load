package engine

import (
	"context"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// recordingProtocol records both the action name and its resolved target so
// tests can observe template-expanded loop/foreach bindings.
type recordingProtocol struct {
	names   []string
	targets []string
}

func (p *recordingProtocol) Name() string                               { return "http" }
func (p *recordingProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *recordingProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.names = append(p.names, action.Name)
	p.targets = append(p.targets, action.Target)
	return &protocol.Result{ActionName: action.Name, Success: true, Duration: time.Millisecond}, nil
}
func (p *recordingProtocol) Teardown(context.Context) error    { return nil }
func (p *recordingProtocol) HealthCheck(context.Context) error { return nil }

func breakAction() dsl.Action    { return dsl.Action{Name: "Break", Type: "control_break"} }
func continueAction() dsl.Action { return dsl.Action{Name: "Continue", Type: "control_continue"} }

// ifThen wraps steps in a control_if whose condition compares loop_index.
func ifLoopIndexEq(value string, steps ...dsl.Action) dsl.Action {
	return dsl.Action{
		Name: "Guard",
		Type: "control_if",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Left: "{{var.loop_index}}", Op: "eq", Right: value},
			Steps:     steps,
		},
	}
}

func dataVUser(t *testing.T, proto protocol.Protocol, sources []dsl.DataSource, actions []dsl.Action) *VUser {
	t.Helper()
	dp, err := NewDataProvider(sources)
	if err != nil {
		t.Fatalf("NewDataProvider: %v", err)
	}
	vu := newControlFlowVUser(proto, nil, actions)
	vu.DataProvider = dp
	vu.Script.DataSources = sources
	return vu
}

func TestVUser_Run_ControlBreakStopsLoop(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{{
		Name: "Repeat",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Count: 5,
			Steps: []dsl.Action{
				click("Step"),
				ifLoopIndexEq("2", breakAction()),
			},
		},
	}})
	vu.Run(context.Background())
	// Pass 1: Step (guard false). Pass 2: Step, then break → loop exits.
	if len(proto.names) != 2 {
		t.Fatalf("break loop executed %v, want [Step Step]", proto.names)
	}
}

func TestVUser_Run_ControlContinueSkipsRestOfBody(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{{
		Name: "Repeat",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Count: 3,
			Steps: []dsl.Action{
				click("A"),
				ifLoopIndexEq("2", continueAction()),
				click("B"),
			},
		},
	}})
	vu.Run(context.Background())
	// Pass 2 hits continue after A, skipping B; A runs all 3 passes, B runs on 1 and 3.
	want := []string{"A", "B", "A", "A", "B"}
	if got := proto.names; !equalStrings(got, want) {
		t.Fatalf("continue loop executed %v, want %v", got, want)
	}
}

func TestVUser_Run_NestedBreakHitsOnlyInnerLoop(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{{
		Name: "Outer",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Count: 2,
			Steps: []dsl.Action{
				{
					Name: "Inner",
					Type: "control_loop",
					Control: &dsl.ControlFlow{
						Count: 3,
						Steps: []dsl.Action{
							click("In"),
							ifLoopIndexEq("1", breakAction()),
						},
					},
				},
				click("Out"),
			},
		},
	}})
	vu.Run(context.Background())
	// Inner breaks on its first pass each time; the outer loop keeps running, so
	// Out runs once per outer pass.
	want := []string{"In", "Out", "In", "Out"}
	if got := proto.names; !equalStrings(got, want) {
		t.Fatalf("nested break executed %v, want %v", got, want)
	}
}

func TestVUser_Run_BreakOutsideLoopIsNoOp(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{
		breakAction(),
		click("After"),
	})
	vu.Run(context.Background())
	// A break with no enclosing loop is ignored; the next top-level action runs.
	if got := proto.names; !equalStrings(got, []string{"After"}) {
		t.Fatalf("stray break executed %v, want [After]", got)
	}
}

func TestVUser_Run_ForEachIteratesRowsAndBinds(t *testing.T) {
	proto := &recordingProtocol{}
	sources := []dsl.DataSource{{
		Name:           "users",
		Type:           "csv",
		InlineData:     "email\na@x.com\nb@x.com\nc@x.com",
		FirstRowHeader: true,
		Strategy:       "sequential",
	}}
	vu := dataVUser(t, proto, sources, []dsl.Action{{
		Name: "Loop",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			ForEach: &dsl.ForEachLoop{Source: "users", Var: "row"},
			Steps:   []dsl.Action{{Name: "Row", Type: "GET", Method: "GET", Target: "/{{var.row.email}}/{{var.loop_index}}"}},
		},
	}})
	vu.Run(context.Background())
	want := []string{"/a@x.com/1", "/b@x.com/2", "/c@x.com/3"}
	if !equalStrings(proto.targets, want) {
		t.Fatalf("foreach targets = %v, want %v", proto.targets, want)
	}
	// Row binding must not leak past the loop.
	if _, ok := vu.Variables["row.email"]; ok {
		t.Error("foreach var binding row.email must be restored after the loop")
	}
}

func TestVUser_Run_ForEachEarlyExitCondition(t *testing.T) {
	proto := &recordingProtocol{}
	sources := []dsl.DataSource{{
		Name:           "nums",
		Type:           "csv",
		InlineData:     "v\n1\n2\n3\n4",
		FirstRowHeader: true,
		Strategy:       "sequential",
	}}
	vu := dataVUser(t, proto, sources, []dsl.Action{{
		Name: "Loop",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			ForEach: &dsl.ForEachLoop{
				Source:    "nums",
				Var:       "n",
				Condition: &dsl.Condition{Expr: "int(var.n.v) < 3"},
			},
			Steps: []dsl.Action{{Name: "Row", Type: "GET", Method: "GET", Target: "/{{var.n.v}}"}},
		},
	}})
	vu.Run(context.Background())
	// Rows 1 and 2 satisfy the predicate; row 3 fails it and stops iteration.
	if !equalStrings(proto.targets, []string{"/1", "/2"}) {
		t.Fatalf("foreach early-exit targets = %v, want [/1 /2]", proto.targets)
	}
}

func TestVUser_Run_ForEachUnknownSourceRunsZeroPasses(t *testing.T) {
	proto := &recordingProtocol{}
	sources := []dsl.DataSource{{
		Name:           "users",
		Type:           "csv",
		InlineData:     "email\na@x.com",
		FirstRowHeader: true,
		Strategy:       "sequential",
	}}
	vu := dataVUser(t, proto, sources, []dsl.Action{{
		Name: "Loop",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			ForEach: &dsl.ForEachLoop{Source: "missing", Var: "row"},
			Steps:   []dsl.Action{click("Body")},
		},
	}})
	vu.Run(context.Background())
	if len(proto.names) != 0 {
		t.Fatalf("foreach over unknown source executed %v, want none", proto.names)
	}
}

func TestVUser_EvalConditionEvidence(t *testing.T) {
	vu := &VUser{Variables: map[string]string{"role": "admin", "count": "3"}}
	t.Run("nil condition", func(t *testing.T) {
		ok, ev := vu.evalConditionEvidence(nil)
		if !ok || !ev.Result {
			t.Fatalf("nil condition = (%v, %+v), want true", ok, ev)
		}
	})
	t.Run("left/op/right operands", func(t *testing.T) {
		ok, ev := vu.evalConditionEvidence(&dsl.Condition{Left: "{{var.role}}", Op: "eq", Right: "admin"})
		if !ok {
			t.Fatal("expected true")
		}
		if ev.Left != "admin" || ev.Right != "admin" || ev.Op != "eq" || !ev.Result {
			t.Fatalf("evidence = %+v, want left=admin right=admin op=eq result=true", ev)
		}
	})
	t.Run("default op normalizes to eq", func(t *testing.T) {
		_, ev := vu.evalConditionEvidence(&dsl.Condition{Left: "a", Right: "b"})
		if ev.Op != "eq" {
			t.Fatalf("op = %q, want eq", ev.Op)
		}
	})
	t.Run("cel expression", func(t *testing.T) {
		ok, ev := vu.evalConditionEvidence(&dsl.Condition{Expr: "int(var.count) > 2"})
		if !ok || ev.Expr != "int(var.count) > 2" || !ev.Result {
			t.Fatalf("cel evidence = (%v, %+v)", ok, ev)
		}
	})
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
