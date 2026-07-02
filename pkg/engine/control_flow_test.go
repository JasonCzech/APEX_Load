package engine

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// captureProtocol records the names of executed actions and can optionally emit an
// incrementing counter extraction to drive condition-based control_loop tests.
type captureProtocol struct {
	names      []string
	counterKey string
	counter    int
}

func (p *captureProtocol) Name() string                               { return "http" }
func (p *captureProtocol) Init(context.Context, map[string]any) error { return nil }
func (p *captureProtocol) Execute(_ context.Context, action *protocol.Action) (*protocol.Result, error) {
	p.names = append(p.names, action.Name)
	result := &protocol.Result{
		ActionName:  action.Name,
		Success:     true,
		Duration:    time.Millisecond,
		Extractions: map[string]string{},
	}
	if p.counterKey != "" {
		p.counter++
		result.Extractions[p.counterKey] = strconv.Itoa(p.counter)
	}
	return result, nil
}
func (p *captureProtocol) Teardown(context.Context) error    { return nil }
func (p *captureProtocol) HealthCheck(context.Context) error { return nil }

func newControlFlowVUser(proto protocol.Protocol, variables map[string]string, actions []dsl.Action) *VUser {
	if variables == nil {
		variables = map[string]string{}
	}
	return &VUser{
		ID:        1,
		Group:     "ControlFlow",
		Proto:     proto,
		Variables: variables,
		Metrics:   NewMetricsCollector(16),
		Script: &dsl.Script{
			Name:     "ControlFlow",
			Protocol: "http",
			Config: dsl.ScriptConfig{
				RunLogic: dsl.RunLogicConfig{Iterations: 1},
			},
			Actions: actions,
		},
	}
}

func click(name string) dsl.Action {
	return dsl.Action{Name: name, Type: "GET", Method: "GET", Target: "/" + name}
}

func TestVUser_EvalCondition(t *testing.T) {
	vu := &VUser{Variables: map[string]string{
		"role":     "admin",
		"count":    "3",
		"empty":    "",
		"status":   "active",
		"users.id": "42",
	}}
	t.Setenv("APEXLOAD_CEL_CONDITION", "enabled")
	tests := []struct {
		name string
		cond *dsl.Condition
		want bool
	}{
		{"nil condition is true", nil, true},
		{"eq match", &dsl.Condition{Left: "{{var.role}}", Op: "eq", Right: "admin"}, true},
		{"eq mismatch", &dsl.Condition{Left: "{{var.role}}", Op: "eq", Right: "guest"}, false},
		{"ne match", &dsl.Condition{Left: "{{var.role}}", Op: "ne", Right: "guest"}, true},
		{"contains", &dsl.Condition{Left: "{{var.status}}", Op: "contains", Right: "act"}, true},
		{"not_contains", &dsl.Condition{Left: "{{var.status}}", Op: "not_contains", Right: "xyz"}, true},
		{"gt true", &dsl.Condition{Left: "{{var.count}}", Op: "gt", Right: "2"}, true},
		{"gt false", &dsl.Condition{Left: "{{var.count}}", Op: "gt", Right: "5"}, false},
		{"lte true", &dsl.Condition{Left: "{{var.count}}", Op: "lte", Right: "3"}, true},
		{"exists true", &dsl.Condition{Left: "{{var.role}}", Op: "exists"}, true},
		{"exists false on empty", &dsl.Condition{Left: "{{var.empty}}", Op: "exists"}, false},
		{"not_exists on empty", &dsl.Condition{Left: "{{var.empty}}", Op: "not_exists"}, true},
		{"numeric op non-numeric operand is false", &dsl.Condition{Left: "{{var.role}}", Op: "gt", Right: "2"}, false},
		{"unknown op is false", &dsl.Condition{Left: "a", Op: "wat", Right: "b"}, false},
		{"cel string comparison", &dsl.Condition{Expr: `var.role == "admin"`}, true},
		{"cel numeric comparison", &dsl.Condition{Expr: `int(var.count) > 2`}, true},
		{"cel membership", &dsl.Condition{Expr: `var.role in ["admin", "owner"]`}, true},
		{"cel data namespace", &dsl.Condition{Expr: `data.users.id == "42"`}, true},
		{"cel env namespace", &dsl.Condition{Expr: `env.APEXLOAD_CEL_CONDITION == "enabled"`}, true},
		{"cel false branch", &dsl.Condition{Expr: `var.role == "guest"`}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vu.evalCondition(tt.cond); got != tt.want {
				t.Fatalf("evalCondition(%#v) = %v, want %v", tt.cond, got, tt.want)
			}
		})
	}
}

func TestVUser_Run_ControlIfUsesCELExpression(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, map[string]string{"role": "admin"}, []dsl.Action{
		{
			Name: "RoleBranch",
			Type: "control_if",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Expr: `var.role == "admin"`},
				Steps:     []dsl.Action{click("AdminPanel")},
				Else:      []dsl.Action{click("GuestHome")},
			},
		},
	})
	vu.Run(context.Background())
	if len(proto.names) != 1 || proto.names[0] != "AdminPanel" {
		t.Fatalf("executed %v, want [AdminPanel]", proto.names)
	}
}

func TestVUser_Run_ControlLoopUsesCELExpression(t *testing.T) {
	proto := &captureProtocol{counterKey: "counter"}
	vu := newControlFlowVUser(proto, map[string]string{"counter": "0"}, []dsl.Action{
		{
			Name: "While",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Expr: `int(var.counter) < 3`},
				Steps:     []dsl.Action{click("Step")},
			},
		},
	})
	vu.Run(context.Background())
	if len(proto.names) != 3 {
		t.Fatalf("condition loop executed %d actions, want 3: %v", len(proto.names), proto.names)
	}
}

func TestVUser_Run_ControlIfChoosesBranch(t *testing.T) {
	for _, tc := range []struct {
		role string
		want string
	}{
		{"admin", "AdminPanel"},
		{"guest", "GuestHome"},
	} {
		proto := &captureProtocol{}
		vu := newControlFlowVUser(proto, map[string]string{"role": tc.role}, []dsl.Action{
			{
				Name: "RoleBranch",
				Type: "control_if",
				Control: &dsl.ControlFlow{
					Condition: &dsl.Condition{Left: "{{var.role}}", Op: "eq", Right: "admin"},
					Steps:     []dsl.Action{click("AdminPanel")},
					Else:      []dsl.Action{click("GuestHome")},
				},
			},
		})
		vu.Run(context.Background())
		if len(proto.names) != 1 || proto.names[0] != tc.want {
			t.Fatalf("role %q executed %v, want [%s]", tc.role, proto.names, tc.want)
		}
	}
}

func TestVUser_Run_ControlLoopFixedCount(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{
		{
			Name: "Repeat",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Count: 3,
				Steps: []dsl.Action{click("Tick")},
			},
		},
	})
	vu.Run(context.Background())
	if len(proto.names) != 3 {
		t.Fatalf("fixed loop executed %d actions, want 3: %v", len(proto.names), proto.names)
	}
}

func TestVUser_Run_ControlLoopConditionDriven(t *testing.T) {
	proto := &captureProtocol{counterKey: "counter"}
	vu := newControlFlowVUser(proto, map[string]string{"counter": "0"}, []dsl.Action{
		{
			Name: "While",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Condition: &dsl.Condition{Left: "{{var.counter}}", Op: "lt", Right: "3"},
				Steps:     []dsl.Action{click("Step")},
			},
		},
	})
	vu.Run(context.Background())
	if len(proto.names) != 3 {
		t.Fatalf("condition loop executed %d actions, want 3: %v", len(proto.names), proto.names)
	}
}

func TestVUser_Run_ControlLoopRespectsMaxIterations(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, map[string]string{"flag": "true"}, []dsl.Action{
		{
			Name: "Runaway",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				// Always-true predicate; only MaxIterations stops it.
				Condition:     &dsl.Condition{Left: "{{var.flag}}", Op: "eq", Right: "true"},
				MaxIterations: 5,
				Steps:         []dsl.Action{click("Spin")},
			},
		},
	})
	vu.Run(context.Background())
	if len(proto.names) != 5 {
		t.Fatalf("runaway loop executed %d actions, want 5 (capped): %v", len(proto.names), proto.names)
	}
}

func TestVUser_Run_NestedControlFlow(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, map[string]string{"enabled": "yes"}, []dsl.Action{
		{
			Name: "Outer",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Count: 2,
				Steps: []dsl.Action{{
					Name: "Inner",
					Type: "control_if",
					Control: &dsl.ControlFlow{
						Condition: &dsl.Condition{Left: "{{var.enabled}}", Op: "eq", Right: "yes"},
						Steps:     []dsl.Action{click("Body")},
					},
				}},
			},
		},
	})
	vu.Run(context.Background())
	if len(proto.names) != 2 {
		t.Fatalf("nested control flow executed %d actions, want 2: %v", len(proto.names), proto.names)
	}
}

func TestVUser_RunLifecycleBlock_ControlFlow(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, map[string]string{"phase": "init"}, nil)
	ok := vu.RunLifecycleBlock(context.Background(), []dsl.Action{{
		Name: "LifecycleBranch",
		Type: "control_if",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Left: "{{var.phase}}", Op: "eq", Right: "init"},
			Steps: []dsl.Action{{
				Name: "RepeatLifecycle",
				Type: "control_loop",
				Control: &dsl.ControlFlow{
					Count: 2,
					Steps: []dsl.Action{click("LifecycleStep")},
				},
			}},
			Else: []dsl.Action{click("Skipped")},
		},
	}}, "init")
	if !ok {
		t.Fatal("RunLifecycleBlock returned false, want true")
	}
	if len(proto.names) != 2 || proto.names[0] != "LifecycleStep" || proto.names[1] != "LifecycleStep" {
		t.Fatalf("lifecycle control flow executed %v, want two LifecycleStep actions", proto.names)
	}
}

func BenchmarkVUser_EvalConditionCELCached(b *testing.B) {
	vu := &VUser{Variables: map[string]string{"count": "42", "role": "admin"}}
	cond := &dsl.Condition{Expr: `int(var.count) > 10 && var.role in ["admin", "owner"]`}
	if !vu.evalCondition(cond) {
		b.Fatal("condition should be true")
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if !vu.evalCondition(cond) {
			b.Fatal("condition should stay true")
		}
	}
}

func TestVUser_ControlLoopExposesLoopIndex(t *testing.T) {
	// Each pass records {{var.loop_index}} into the request target so the
	// 1-based per-pass value is observable.
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{{
		Name: "Repeat",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Count: 3,
			Steps: []dsl.Action{{Name: "Step", Type: "GET", Method: "GET", Target: "/pass/{{var.loop_index}}"}},
		},
	}})
	vu.Run(context.Background())
	if len(proto.names) != 3 {
		t.Fatalf("executed %d steps, want 3", len(proto.names))
	}
	if _, ok := vu.Variables["loop_index"]; ok {
		t.Error("loop_index must be cleaned up after the loop")
	}
}

func TestVUser_ControlLoopConditionOverLoopIndex(t *testing.T) {
	// A condition-driven loop bounded by a runtime variable: passes while
	// loop_index <= total, evaluating the bound every pass.
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, map[string]string{"total": "4"}, []dsl.Action{{
		Name: "RepeatVar",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Expr: "int(var.loop_index) <= int(var.total)"},
			Steps:     []dsl.Action{click("VarStep")},
		},
	}})
	vu.Run(context.Background())
	if len(proto.names) != 4 {
		t.Fatalf("executed %d steps, want 4", len(proto.names))
	}
}

func TestVUser_ControlLoopConditionZeroPasses(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, map[string]string{"total": "0"}, []dsl.Action{{
		Name: "RepeatNone",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Expr: "int(var.loop_index) <= int(var.total)"},
			Steps:     []dsl.Action{click("NeverStep")},
		},
	}})
	vu.Run(context.Background())
	if len(proto.names) != 0 {
		t.Fatalf("executed %d steps, want 0", len(proto.names))
	}
}

func TestVUser_NestedControlLoopRestoresOuterLoopIndex(t *testing.T) {
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, nil, []dsl.Action{{
		Name: "Outer",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			// Outer runs while loop_index <= 2; the inner fixed loop clobbers
			// loop_index during its passes and must restore it afterwards.
			Condition: &dsl.Condition{Expr: "int(var.loop_index) <= 2"},
			Steps: []dsl.Action{{
				Name: "Inner",
				Type: "control_loop",
				Control: &dsl.ControlFlow{
					Count: 3,
					Steps: []dsl.Action{click("InnerStep")},
				},
			}},
		},
	}})
	vu.Run(context.Background())
	if len(proto.names) != 6 {
		t.Fatalf("executed %d inner steps, want 6 (2 outer x 3 inner)", len(proto.names))
	}
}

func TestVUser_LifecycleControlLoopExposesLoopIndex(t *testing.T) {
	// The init/teardown runner has its own control_loop implementation; an
	// imported param-bounded loop must iterate there too.
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, map[string]string{"total": "2"}, nil)
	ok := vu.RunLifecycleBlock(context.Background(), []dsl.Action{{
		Name: "InitLoop",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Condition: &dsl.Condition{Expr: "int(var.loop_index) <= int(var.total)"},
			Steps:     []dsl.Action{click("InitStep")},
		},
	}}, "init")
	if !ok {
		t.Fatal("RunLifecycleBlock returned false, want true")
	}
	if len(proto.names) != 2 {
		t.Fatalf("lifecycle loop executed %d passes, want 2", len(proto.names))
	}
	if _, exists := vu.Variables["loop_index"]; exists {
		t.Error("loop_index must be cleaned up after the lifecycle loop")
	}
}

func TestVUser_EvalConditionSafeAtoiUnsetParameter(t *testing.T) {
	// The importer's hardened atoi translation: an unset parameter evaluates
	// to 0 like C atoi, so a negated guard stays TRUE instead of being forced
	// false by a CEL evaluation error.
	vu := &VUser{Variables: map[string]string{}}
	expr := `!((has(vars.success_count) && var.success_count.matches("^-?[0-9]+$") ? int(var.success_count) : 0) > 0)`
	if !vu.evalCondition(&dsl.Condition{Expr: expr}) {
		t.Error("negated guard over an unset parameter must evaluate true (atoi semantics)")
	}
	vu.Variables["success_count"] = "5"
	if vu.evalCondition(&dsl.Condition{Expr: expr}) {
		t.Error("guard must flip false once the parameter holds a positive count")
	}
	vu.Variables["success_count"] = "not-a-number"
	if !vu.evalCondition(&dsl.Condition{Expr: expr}) {
		t.Error("non-numeric parameter must behave like atoi()==0")
	}
}

func TestControlLoopDoWhileRunsBodyAtLeastOnce(t *testing.T) {
	// Condition is false on the very first check (done already == "yes").
	loop := func(doWhile bool) dsl.Action {
		return dsl.Action{
			Name: "Loop",
			Type: "control_loop",
			Control: &dsl.ControlFlow{
				Condition:     &dsl.Condition{Left: "{{var.done}}", Op: "ne", Right: "yes"},
				DoWhile:       doWhile,
				MaxIterations: 5,
				Steps:         []dsl.Action{click("body")},
			},
		}
	}
	// Default pre-test loop: predicate false up front → body never runs.
	pre := &captureProtocol{}
	newControlFlowVUser(pre, map[string]string{"done": "yes"}, []dsl.Action{loop(false)}).Run(context.Background())
	if len(pre.names) != 0 {
		t.Fatalf("pre-test loop ran body %d time(s), want 0", len(pre.names))
	}
	// do_while: body runs exactly once before the predicate is first evaluated.
	dw := &captureProtocol{}
	newControlFlowVUser(dw, map[string]string{"done": "yes"}, []dsl.Action{loop(true)}).Run(context.Background())
	if len(dw.names) != 1 {
		t.Fatalf("do_while loop ran body %d time(s), want exactly 1", len(dw.names))
	}
}

func TestVUser_LifecycleControlLoopDoWhileRunsBodyAtLeastOnce(t *testing.T) {
	loop := dsl.Action{
		Name: "InitLoop",
		Type: "control_loop",
		Control: &dsl.ControlFlow{
			Condition:     &dsl.Condition{Left: "{{var.done}}", Op: "ne", Right: "yes"},
			DoWhile:       true,
			MaxIterations: 5,
			Steps:         []dsl.Action{click("InitBody")},
		},
	}
	proto := &captureProtocol{}
	vu := newControlFlowVUser(proto, map[string]string{"done": "yes"}, nil)
	if !vu.RunLifecycleBlock(context.Background(), []dsl.Action{loop}, "init") {
		t.Fatal("RunLifecycleBlock returned false, want true")
	}
	if len(proto.names) != 1 {
		t.Fatalf("lifecycle do_while loop ran body %d time(s), want exactly 1", len(proto.names))
	}
}
