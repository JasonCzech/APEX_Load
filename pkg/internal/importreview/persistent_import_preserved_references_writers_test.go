package importreview

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func stringPointer(value string) *string { return &value }

// varHostCredentialScript keeps its credential on actions[0], whose target
// reads {{var.host}}. Nothing writes the variable, so the "host" parameter
// answers: the fallback a variable written in any phase overrides.
func varHostCredentialScript() *dsl.Script {
	script := referencedCredentialScript()
	script.Actions[0].Target = "https://{{var.host}}/orders"
	return script
}

func saveHostStep(name string) dsl.Action {
	return dsl.Action{
		Name: name, Type: "source_parameter",
		Params: map[string]any{"function": "lr_save_string", "args": []any{"attacker.test", "host"}},
	}
}

func hostExtractorStep(name string) dsl.Action {
	return dsl.Action{
		Name: name, Type: "GET", Method: "GET", Target: "https://origin.example.test/seed",
		Extractors: []dsl.Extractor{{Name: "host", Source: "status", Type: "regex", Expression: "(.*)", DefaultValue: stringPointer("attacker.test")}},
	}
}

// A step that writes the variable a kept-credential request reads is a
// producer wherever it sits: in any control container of any phase, in an async
// conversation's callback, or on its nested request.
func TestValidatePreservedImportCredentialEditsPinsWritersInEveryContainer(t *testing.T) {
	prior := varHostCredentialScript()
	cond := &dsl.Condition{Left: "a", Op: "eq", Right: "b"}

	refusals := []struct {
		name string
		step dsl.Action
	}{
		{"top level", saveHostStep("save")},
		{"switch default", dsl.Action{Name: "pick", Type: "control_switch", Control: &dsl.ControlFlow{
			Cases:   []dsl.SwitchCase{{Condition: cond}},
			Default: []dsl.Action{saveHostStep("save")},
		}}},
		{"switch case", dsl.Action{Name: "pick", Type: "control_switch", Control: &dsl.ControlFlow{
			Cases: []dsl.SwitchCase{{Condition: cond, Steps: []dsl.Action{hostExtractorStep("seed")}}},
		}}},
		{"if else", dsl.Action{Name: "branch", Type: "control_if", Control: &dsl.ControlFlow{
			Condition: cond, Steps: []dsl.Action{hostExtractorStep("seed")}, Else: []dsl.Action{saveHostStep("save")},
		}}},
		{"parallel branch", dsl.Action{Name: "fork", Type: "control_parallel", Control: &dsl.ControlFlow{
			Branches: []dsl.ParallelBranch{{Name: "b1", Steps: []dsl.Action{saveHostStep("save")}}},
		}}},
		{"async callback", dsl.Action{
			Name: "bg", Type: "http_async_start",
			Params: map[string]any{"id": "bg", "pattern": "once", "request": map[string]any{
				"name": "ping", "type": "GET", "method": "GET", "target": "https://origin.example.test/ping",
			}},
			Control: &dsl.ControlFlow{Branches: []dsl.ParallelBranch{{Name: "on_request", Steps: []dsl.Action{saveHostStep("save")}}}},
		}},
		{"async request extractor", dsl.Action{
			Name: "bg", Type: "http_async_start",
			Params: map[string]any{"id": "bg", "pattern": "once", "request": map[string]any{
				"name": "ping", "type": "GET", "method": "GET", "target": "https://origin.example.test/ping",
				"extractors": []any{map[string]any{"name": "host", "source": "status", "type": "regex", "expression": "(x)", "default_value": "attacker.test"}},
			}},
		}},
		{"HTTP sequence child", dsl.Action{
			Name: "seq", Type: "http_sequence",
			Params: map[string]any{"requests": []any{map[string]any{
				"name": "seed", "type": "GET", "method": "GET", "target": "https://origin.example.test/seed",
				"extractors": []any{map[string]any{"name": "host", "source": "status", "type": "regex", "expression": "(x)", "default_value": "attacker.test"}},
			}}},
		}},
		{"date conversion that rewrites the name", dsl.Action{
			Name: "conv", Type: "source_parameter",
			Params: map[string]any{"function": "web_convert_date_param", "args": []any{"host", "SourceFormat=", "TargetFormat=attacker.test"}},
		}},
		{"free that lets the parameter answer", dsl.Action{
			Name: "free", Type: "source_parameter",
			Params: map[string]any{"function": "lr_free_parameter", "args": []any{"host"}},
		}},
	}
	for _, phase := range []string{"init", "teardown"} {
		for _, refusal := range refusals {
			t.Run("refuses "+refusal.name+" in "+phase, func(t *testing.T) {
				candidate := CloneScriptDefinition(prior)
				if phase == "init" {
					candidate.Init = append(candidate.Init, refusal.step)
				} else {
					candidate.Teardown = append(candidate.Teardown, refusal.step)
				}
				err := ValidatePreservedImportCredentialEdits(candidate, prior)
				if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
					t.Fatalf("%v, want the refusal at actions[0]", err)
				}
			})
		}
	}

	t.Run("accepts the same steps for another name", func(t *testing.T) {
		step := saveHostStep("save")
		step.Params = map[string]any{"function": "lr_save_string", "args": []any{"east", "region_name"}}
		convert := dsl.Action{Name: "conv", Type: "source_parameter", Params: map[string]any{"function": "web_convert_date_param", "args": []any{"stamp", "SourceFormat=%Y", "TargetFormat=%y"}}}
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, step, convert, dsl.Action{Name: "pick", Type: "control_switch", Control: &dsl.ControlFlow{
			Cases:   []dsl.SwitchCase{{Condition: cond, Steps: []dsl.Action{step}}},
			Default: []dsl.Action{convert},
		}})
		if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
			t.Fatalf("%v, want the unrelated steps accepted", err)
		}
	})
}

// A data-driven loop publishes the columns of its source as var.<var>.<column>
// for its body, so the source and the loop block are the value the body reads.
func TestValidatePreservedImportCredentialEditsPinsForEachRows(t *testing.T) {
	prior := sourceReviewCredentialScript()
	prior.DataSources = []dsl.DataSource{
		{Name: "hosts", Type: "csv", InlineData: "host\norigin.example.test\n", Strategy: "sequential", FirstRowHeader: true},
		{Name: "regions", Type: "csv", InlineData: "region\nwest\n", Strategy: "sequential", FirstRowHeader: true},
	}
	orders := prior.Actions[0]
	orders.Target = "https://{{var.row.host}}/orders"
	prior.Actions[0] = dsl.Action{
		Name: "each host", Type: "control_loop",
		Control: &dsl.ControlFlow{ForEach: &dsl.ForEachLoop{Source: "hosts", Var: "row"}, Steps: []dsl.Action{orders}},
	}

	for name, edit := range map[string]func(*dsl.Script){
		"rows of the source": func(script *dsl.Script) { script.DataSources[0].InlineData = "host\nattacker.test\n" },
		"loop source": func(script *dsl.Script) {
			script.DataSources = append(script.DataSources, dsl.DataSource{Name: "evil", Type: "csv", InlineData: "host\nattacker.test\n", Strategy: "sequential", FirstRowHeader: true})
			script.Actions[0].Control.ForEach.Source = "evil"
		},
		"loop variable": func(script *dsl.Script) { script.Actions[0].Control.ForEach.Var = "other" },
		"request in the body": func(script *dsl.Script) {
			script.Actions[0].Control.Steps[0].Target = "https://attacker.test/orders"
		},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			candidate := CloneScriptDefinition(prior)
			edit(candidate)
			err := ValidatePreservedImportCredentialEdits(candidate, prior)
			if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0].control.steps[0]") {
				t.Fatalf("%v, want the refusal at the request in the loop body", err)
			}
		})
	}
	t.Run("accepts a source the loop does not read", func(t *testing.T) {
		candidate := CloneScriptDefinition(prior)
		candidate.DataSources[1].InlineData = "region\neast\n"
		if err := ValidatePreservedImportCredentialEdits(candidate, prior); err != nil {
			t.Fatalf("%v, want the edit accepted", err)
		}
	})
}

// A kept-credential request in a switch case or a parallel branch has a
// context of its own; before, it fell back to the enclosing step's, which has
// no request fields.
func TestValidatePreservedImportCredentialEditsPinsRequestsInEveryContainer(t *testing.T) {
	cond := &dsl.Condition{Left: "a", Op: "eq", Right: "a"}
	containers := map[string]func(dsl.Action) dsl.Action{
		"switch case": func(request dsl.Action) dsl.Action {
			return dsl.Action{Name: "sw", Type: "control_switch", Control: &dsl.ControlFlow{
				Cases: []dsl.SwitchCase{{Condition: cond, Steps: []dsl.Action{request}}},
			}}
		},
		"switch default": func(request dsl.Action) dsl.Action {
			return dsl.Action{Name: "sw", Type: "control_switch", Control: &dsl.ControlFlow{
				Cases: []dsl.SwitchCase{{Condition: cond}}, Default: []dsl.Action{request},
			}}
		},
		"parallel branch": func(request dsl.Action) dsl.Action {
			return dsl.Action{Name: "par", Type: "control_parallel", Control: &dsl.ControlFlow{
				Branches: []dsl.ParallelBranch{{Name: "b1", Steps: []dsl.Action{request}}},
			}}
		},
	}
	for name, wrap := range containers {
		t.Run(name, func(t *testing.T) {
			prior := sourceReviewCredentialScript()
			prior.Actions[0] = wrap(prior.Actions[0])
			retargeted := CloneScriptDefinition(prior)
			var request *dsl.Action
			switch {
			case len(retargeted.Actions[0].Control.Cases) > 0 && len(retargeted.Actions[0].Control.Cases[0].Steps) > 0:
				request = &retargeted.Actions[0].Control.Cases[0].Steps[0]
			case len(retargeted.Actions[0].Control.Default) > 0:
				request = &retargeted.Actions[0].Control.Default[0]
			default:
				request = &retargeted.Actions[0].Control.Branches[0].Steps[0]
			}
			request.Target = "https://attacker.test/orders"
			err := ValidatePreservedImportCredentialEdits(retargeted, prior)
			if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0].control.") {
				t.Fatalf("%v, want the refusal at the nested request", err)
			}

			sibling := CloneScriptDefinition(prior)
			sibling.Actions[1].Target = "https://origin.example.test/cart/summary"
			if err := ValidatePreservedImportCredentialEdits(sibling, prior); err != nil {
				t.Fatalf("%v, want an unrelated request accepted", err)
			}
		})
	}
}

// Everything else the runtime interpolates into a kept-credential request:
// a base64-stored body is decoded first, and the HTTP configuration's default
// headers are added to every request.
func TestValidatePreservedImportCredentialEditsPinsOtherInterpolatedFields(t *testing.T) {
	t.Run("refuses a base64 body's parameter", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0].Target = "https://origin.example.test/orders"
		prior.Actions[0].Method = "POST"
		// {"callback":"https://{{param.host}}/hook"}
		prior.Actions[0].Body = "eyJjYWxsYmFjayI6Imh0dHBzOi8ve3twYXJhbS5ob3N0fX0vaG9vayJ9"
		prior.Actions[0].BodyEncoding = "base64"
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[0].Values = []string{"attacker.test"}
		err := ValidatePreservedImportCredentialEdits(candidate, prior)
		if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
			t.Fatalf("%v, want the refusal at actions[0]", err)
		}
	})
	t.Run("refuses a default header's parameter", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0].Target = "https://origin.example.test/orders"
		prior.Config.HTTP.DefaultHeaders = map[string]string{"Host": "{{param.region}}.example.test"}
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[1].Values = []string{"attacker-on-the-same-edge"}
		err := ValidatePreservedImportCredentialEdits(candidate, prior)
		if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
			t.Fatalf("%v, want the refusal at actions[0]", err)
		}
	})
	t.Run("matches a parameter name the way the runtime keys it", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Parameters[0].Name = " Host "
		candidate := CloneScriptDefinition(prior)
		candidate.Parameters[0].Values = []string{"attacker.test"}
		err := ValidatePreservedImportCredentialEdits(candidate, prior)
		if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
			t.Fatalf("%v, want the refusal at actions[0]", err)
		}
	})
	t.Run("refuses a fixed-name variable's new writer", func(t *testing.T) {
		prior := referencedCredentialScript()
		prior.Actions[0].Target = "https://{{var.async_response_body}}/orders"
		candidate := CloneScriptDefinition(prior)
		candidate.Init = append(candidate.Init, dsl.Action{
			Name: "bg", Type: "http_async_start",
			Params: map[string]any{"id": "bg", "pattern": "once", "request": map[string]any{
				"name": "ping", "type": "GET", "method": "GET", "target": "https://attacker.test/ping",
			}},
		})
		err := ValidatePreservedImportCredentialEdits(candidate, prior)
		if err == nil || !strings.Contains(err.Error(), "retained imported credentials at actions[0]") {
			t.Fatalf("%v, want the refusal at actions[0]", err)
		}
	})
}

// walkImportActions has to visit every list of steps a control block holds.
// dsl.ControlFlow.ActionBranches is the DSL's own list of them, written so a
// new container cannot go unvisited; this fills each container ControlFlow
// declares by reflection and holds the walk to it.
func TestWalkImportActionsVisitsEveryControlContainer(t *testing.T) {
	actionType := reflect.TypeOf(dsl.Action{})
	holdsActions := func(typ reflect.Type) bool {
		var visit func(reflect.Type, map[reflect.Type]bool) bool
		visit = func(typ reflect.Type, seen map[reflect.Type]bool) bool {
			for typ.Kind() == reflect.Slice || typ.Kind() == reflect.Pointer {
				typ = typ.Elem()
			}
			if typ == actionType {
				return true
			}
			if typ.Kind() != reflect.Struct || seen[typ] {
				return false
			}
			seen[typ] = true
			for i := 0; i < typ.NumField(); i++ {
				if visit(typ.Field(i).Type, seen) {
					return true
				}
			}
			return false
		}
		return visit(typ, map[reflect.Type]bool{})
	}
	step := func(name string) dsl.Action {
		return dsl.Action{Name: name, Type: "GET", Method: "GET", Target: "https://origin.example.test/" + name}
	}

	control := &dsl.ControlFlow{}
	value := reflect.ValueOf(control).Elem()
	populated := 0
	for i := 0; i < value.NumField(); i++ {
		field, fieldType := value.Field(i), value.Type().Field(i)
		if !holdsActions(fieldType.Type) {
			continue
		}
		switch fieldType.Type {
		case reflect.TypeOf([]dsl.Action{}):
			field.Set(reflect.ValueOf([]dsl.Action{step(fieldType.Name)}))
		case reflect.TypeOf([]dsl.SwitchCase{}):
			field.Set(reflect.ValueOf([]dsl.SwitchCase{{Condition: &dsl.Condition{Left: "a", Op: "eq", Right: "a"}, Steps: []dsl.Action{step(fieldType.Name)}}}))
		case reflect.TypeOf([]dsl.ParallelBranch{}):
			field.Set(reflect.ValueOf([]dsl.ParallelBranch{{Name: "b", Steps: []dsl.Action{step(fieldType.Name)}}}))
		default:
			t.Fatalf("ControlFlow.%s (%s) holds actions; teach walkImportActions and this test about it", fieldType.Name, fieldType.Type)
		}
		populated++
	}
	if branches := control.ActionBranches(); len(branches) != populated {
		t.Fatalf("ActionBranches lists %d containers for %d populated ControlFlow fields; it must list every one", len(branches), populated)
	}

	script := &dsl.Script{Name: "containers", Version: "1", Protocol: "http", Actions: []dsl.Action{{Name: "root", Type: "control_if", Control: control}}}
	value2, err := ScriptAsJSONValue(script)
	if err != nil {
		t.Fatal(err)
	}
	projected, _ := value2.(map[string]any)
	names := map[string]bool{}
	walkImportActions(projected, func(path string, action map[string]any) {
		names[StringJSONValue(action["name"])] = true
	})
	if want := populated + 1; len(names) != want {
		t.Fatalf("walkImportActions visited %d steps, want %d (root and one per container): %v", len(names), want, names)
	}
}
