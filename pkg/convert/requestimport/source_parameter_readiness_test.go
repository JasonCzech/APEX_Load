package requestimport

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestVariableReadinessSourceParameterOutputsAndExecutableInputs(t *testing.T) {
	parameter := func(function string, args ...string) dsl.Action {
		return dsl.Action{Type: "source_parameter", Params: map[string]any{"function": function, "args": args}}
	}
	convert := parameter("web_convert_param", "Token_URL", "SourceString={{var.token}}")
	convert.Params["save_param"] = "Token_URL"
	convert.Params["source_string"] = "{{var.token}}"
	convert.Params["call_args"] = []map[string]any{{"name": "source_snapshot", "value": "{{var.evidence_only}}"}}
	convert.Metadata = map[string]any{"source_snippet": "{{var.evidence_only}}"}
	array := parameter("lr_save_string", "{{var.ids}}", "Picked")
	array.Params["source_array"] = map[string]any{"name": "ids", "selection": "random"}
	array.Params["source_string"] = "{{var.ids_count}}"
	clock := parameter("web_convert_param", "Clock_URL", "SourceString={{param.clock}}")
	clock.Params["source_string"] = "{{param.clock}}"
	script := &dsl.Script{
		Parameters: []dsl.Parameter{{Name: "clock", Type: "date_time"}},
		Init: []dsl.Action{
			{Type: "http_request", Target: "/bootstrap", Extractors: []dsl.Extractor{{Name: "token"}, {Name: "ids", Mode: "all"}}},
			convert,
		},
		Actions: []dsl.Action{
			parameter("lr_save_timestamp", "Tstamp"),
			array,
			clock,
			{Type: "http_request", Target: "/{{var.token_url}}/{{var.Token_URL}}/{{var.picked}}/{{var.tstamp}}/{{var.clock_url}}"},
		},
	}
	// API decoding produces []any args and source_array maps. Both decoded and
	// directly authored forms must have the same readiness result.
	for _, decoded := range []bool{false, true} {
		candidate := script
		if decoded {
			payload, err := json.Marshal(script)
			if err != nil {
				t.Fatal(err)
			}
			candidate = new(dsl.Script)
			if err := json.Unmarshal(payload, candidate); err != nil {
				t.Fatal(err)
			}
		}
		if got := AnalyzeVariableReadiness(candidate, nil); len(got.Missing) != 0 {
			t.Fatalf("decoded=%v readiness=%#v", decoded, got)
		}
	}
}

func TestVariableReadinessSourceParameterKeepsRealMissingInputs(t *testing.T) {
	tests := []struct {
		name   string
		action dsl.Action
		want   []string
	}{
		{"encoded source", dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "web_convert_param", "save_param": "Encoded", "source_string": "{{var.input}}"}}, []string{"input"}},
		{"blank encoded source uses args", dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "web_convert_param", "args": []string{"Encoded", "SourceString={{var.input}}"}, "source_string": "  "}}, []string{"input"}},
		{"string input", dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []string{"{{var.input}}", "Copy"}}}, []string{"input"}},
		{"whitespace function reads input", dsl.Action{Type: "source_parameter", Params: map[string]any{"function": " lr_save_string ", "args": []string{"{{var.input}}", "Copy"}}}, []string{"input"}},
		{"buffer input", dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "lr_save_var", "args": []string{"{{var.input}}", "1", "0", "Copy"}}}, []string{"input"}},
		{"format inputs", dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "lr_param_sprintf", "args": []string{"Formatted", "%s", "{{var.input}}"}}}, []string{"input"}},
		{"date format input", dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "lr_save_datetime", "args": []string{"{{var.input}}", "Formatted"}}}, []string{"input"}},
		{"array count", dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []string{"{{var.ids}}", "Picked"}, "source_array": map[string]any{"name": "ids", "selection": "random"}}}, []string{"ids_count"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := AnalyzeVariableReadiness(&dsl.Script{Actions: []dsl.Action{test.action}}, nil)
			if !reflect.DeepEqual(got.Missing, test.want) || !reflect.DeepEqual(got.ByAction["actions[0]"], test.want) {
				t.Fatalf("readiness=%#v want=%v", got, test.want)
			}
		})
	}
}

func TestVariableReadinessSourceParameterOrderingAndRemoval(t *testing.T) {
	assignment := dsl.Action{Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []string{"ready", "Value"}}}
	read := dsl.Action{Type: "http_request", Target: "/{{var.value}}"}
	tests := []struct {
		name    string
		actions []dsl.Action
		want    []string
	}{
		{"preceding assignment", []dsl.Action{assignment, read}, nil},
		{"late assignment", []dsl.Action{read, assignment}, []string{"value"}},
		{"malformed assignment", []dsl.Action{{Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []string{"ready"}, "save_param": "Value"}}, read}, []string{"value"}},
		{"empty date time destination", []dsl.Action{{Type: "source_parameter", Params: map[string]any{"function": "lr_save_datetime", "args": []string{"%Y-%m-%d", ""}}}, {Type: "http_request", Target: "/{{var.datetime}}"}}, []string{"datetime"}},
		{"freed parameter", []dsl.Action{assignment, {Type: "source_parameter", Params: map[string]any{"function": "lr_free_parameter", "args": []string{"Value"}}}, read}, []string{"value"}},
		{"optional branch", []dsl.Action{{Type: "control_if", Control: &dsl.ControlFlow{Condition: &dsl.Condition{Left: "yes", Op: "eq", Right: "yes"}, Steps: []dsl.Action{assignment}}}, read}, []string{"value"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := MissingVariables(&dsl.Script{Actions: test.actions}, nil); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("missing=%v want=%v", got, test.want)
			}
		})
	}
}

func TestVariableReadinessSourceParameterNativeOutputPolicies(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		target string
	}{
		{"saved integer", map[string]any{"function": "lr_save_int", "args": []any{1, "Result"}}, "/{{var.result}}"},
		{"whitespace function writes value", map[string]any{"function": " lr_save_string ", "args": []string{"ready", "Result"}}, "/{{var.result}}"},
		{"saved buffer", map[string]any{"function": "lr_save_var", "args": []string{"ready", "5", "0", "Result"}}, "/{{var.result}}"},
		{"formatted value", map[string]any{"function": "lr_param_sprintf", "args": []string{"Result", "%s", "ready"}}, "/{{var.result}}"},
		{"initialized increment", map[string]any{"function": "lr_param_increment", "args": []string{"Result"}}, "/{{var.result}}"},
		{"default timestamp", map[string]any{"function": "lr_save_timestamp"}, "/{{var.timestamp}}"},
		{"first nonempty timestamp name", map[string]any{"function": "lr_save_timestamp", "args": []string{"  ", "Result"}}, "/{{var.result}}"},
		{"scalar timestamp arg", map[string]any{"function": "lr_save_timestamp", "args": "Result"}, "/{{var.result}}"},
		{"date time positional name", map[string]any{"function": "lr_save_datetime", "args": []string{"%Y-%m-%d", "Result"}}, "/{{var.result}}"},
		{"default date time", map[string]any{"function": "lr_save_datetime"}, "/{{var.datetime}}"},
		{"host identity", map[string]any{"function": "lr_get_host_name"}, "/{{var.host_name}}"},
		{"master identity", map[string]any{"function": "lr_get_master_host_name"}, "/{{var.host_name}}"},
		{"vuser identity", map[string]any{"function": "lr_whoami"}, "/{{var.vuser_id}}/{{var.vuser_group}}"},
		{"vuser ip", map[string]any{"function": "lr_get_vuser_ip"}, "/{{var.vuser_ip}}"},
		{"named native fallback", map[string]any{"function": "legacy_parameter", "save_param": "Result", "args": []string{"ready"}}, "/{{var.result}}"},
		{"blank named native fallback", map[string]any{"function": "legacy_parameter", "save_param": "  ", "parameter": "Result", "args": []string{"ready"}}, "/{{var.result}}"},
		{"structured native fallback", map[string]any{"function": "legacy_parameter", "call_args": []map[string]any{{"name": "SaveParam", "value": "Result"}}, "args": []string{"ready"}}, "/{{var.result}}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			script := &dsl.Script{Actions: []dsl.Action{
				{Type: "source_parameter", Params: test.params},
				{Type: "http_request", Target: test.target},
			}}
			if got := MissingVariables(script, nil); len(got) != 0 {
				t.Fatalf("missing=%v", got)
			}
		})
	}
}
