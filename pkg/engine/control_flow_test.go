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
		"role":   "admin",
		"count":  "3",
		"empty":  "",
		"status": "active",
	}}
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
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := vu.evalCondition(tt.cond); got != tt.want {
				t.Fatalf("evalCondition(%#v) = %v, want %v", tt.cond, got, tt.want)
			}
		})
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
