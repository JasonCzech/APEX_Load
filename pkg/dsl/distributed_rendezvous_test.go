package dsl

import (
	"strings"
	"testing"
)

func distributedBarrier() Action {
	return Action{Type: "rendezvous", Params: map[string]any{"scope": "distributed_group", "key": "checkout", "timeout_ms": 1000, "on_timeout": "fail"}}
}
func TestDistributedRendezvousAuthoringContract(t *testing.T) {
	valid := distributedBarrier()
	if specs, err := ScriptDistributedRendezvous(&Script{Actions: []Action{valid}}); err != nil || len(specs) != 1 {
		t.Fatalf("valid %+v %v", specs, err)
	}
	for name, change := range map[string]func(*Action){"dynamic_key": func(a *Action) { a.Params["key"] = "{{var.name}}" }, "unbounded_timeout": func(a *Action) { a.Params["timeout_ms"] = 120001 }, "missing_policy": func(a *Action) { delete(a.Params, "on_timeout") }, "ambiguous_target": func(a *Action) { a.Params["count"] = 1; a.Params["percent"] = 50 }, "unknown": func(a *Action) { a.Params["surprise"] = true }, "fractional": func(a *Action) { a.Params["count"] = 1.5 }} {
		t.Run(name, func(t *testing.T) {
			a := distributedBarrier()
			change(&a)
			if _, err := ParseDistributedRendezvous(a); err == nil {
				t.Fatal("invalid contract accepted")
			}
		})
	}
	if _, err := ScriptDistributedRendezvous(&Script{Init: []Action{valid}}); err == nil {
		t.Fatal("init barrier accepted")
	}
	if _, err := ScriptDistributedRendezvous(&Script{Actions: []Action{valid, valid}}); err == nil || !strings.Contains(err.Error(), "more than once") {
		t.Fatal("duplicate key accepted")
	}
	if spec, err := ParseDistributedRendezvous(Action{Type: "rendezvous", Params: map[string]any{"function": "lr_rendezvous", "args": []any{"legacy"}}}); err != nil || spec != nil {
		t.Fatal("legacy local import changed")
	}
}
