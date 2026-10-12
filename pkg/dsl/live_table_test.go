package dsl

import (
	"reflect"
	"strings"
	"testing"
)

func TestLiveTableActionStrictContract(t *testing.T) {
	for _, test := range []struct {
		name   string
		params map[string]any
		valid  bool
	}{
		{"empty producer", map[string]any{"alias": "work", "operation": "push", "values": map[string]string{"id": ""}}, true},
		{"template version", map[string]any{"alias": "work", "operation": "update", "record_id": "{{var.row.record_id}}", "expected_version": "{{var.row.record_version}}", "values": map[string]string{"id": "new"}}, true},
		{"lease", map[string]any{"alias": "work", "operation": "lease", "result_prefix": "claim", "wait_ms": 1000}, true},
		{"unknown operation", map[string]any{"alias": "work", "operation": "destroy"}, false},
		{"unknown field", map[string]any{"alias": "work", "operation": "next", "token": "credential"}, false},
		{"irrelevant field", map[string]any{"alias": "work", "operation": "pop", "position": 0}, false},
		{"null", map[string]any{"alias": "work", "operation": "pop", "wait_ms": nil}, false},
		{"wrong type", map[string]any{"alias": "work", "operation": "pop", "wait_ms": "1000"}, false},
		{"unbounded wait", map[string]any{"alias": "work", "operation": "pop", "wait_ms": 60001}, false},
		{"dynamic alias", map[string]any{"alias": "{{var.pool}}", "operation": "pop"}, false},
		{"ambiguous read", map[string]any{"alias": "work", "operation": "read", "position": 1, "record_id": "x"}, false},
		{"unconditioned update", map[string]any{"alias": "work", "operation": "update", "record_id": "x", "values": map[string]string{"id": "new"}}, false},
		{"numeric version", map[string]any{"alias": "work", "operation": "update", "record_id": "x", "expected_version": 1, "values": map[string]string{"id": "new"}}, false},
		{"lease token injection", map[string]any{"alias": "work", "operation": "ack", "lease_ref": "claim", "lease_token": "stolen"}, false},
		{"duplicate columns", map[string]any{"alias": "work", "operation": "pop", "columns": []string{"id", "id"}}, false},
		{"managed secret", map[string]any{"alias": "work", "operation": "push", "values": map[string]string{"id": "{{secret.customer}}"}}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ParseLiveTableAction(test.params)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%v error=%v", test.valid, err)
			}
		})
	}
}

func TestLiveTableAliasesVisitEveryLifecycleAndNestedBranch(t *testing.T) {
	action := func(alias string) Action {
		return Action{Type: "live_table", Params: map[string]any{"alias": alias, "operation": "next"}}
	}
	script := &Script{Init: []Action{action("init")}, Actions: []Action{{Type: "control_if", Control: &ControlFlow{Steps: []Action{action("work")}, Else: []Action{action("other")}}}}, Teardown: []Action{action("cleanup")}}
	aliases, err := ScriptLiveTableAliases(script)
	if err != nil || !reflect.DeepEqual(aliases, []string{"cleanup", "init", "other", "work"}) {
		t.Fatalf("aliases=%v error=%v", aliases, err)
	}
	script.Actions[0].Control.Else[0].Params["wait_ms"] = -1
	if _, err = ScriptLiveTableAliases(script); err == nil {
		t.Fatal("invalid nested action accepted")
	}
}

func TestLiveTableValidationRebasesNestedActionPath(t *testing.T) {
	issues := validateActionForProtocolAtPath("http", "init[0]", 0, Action{Name: "ack", Type: "live_table", Params: map[string]any{"alias": "work", "operation": "ack"}})
	if len(issues) != 1 || !strings.HasPrefix(issues[0], "init[0]:") {
		t.Fatalf("issues=%v", issues)
	}
}
