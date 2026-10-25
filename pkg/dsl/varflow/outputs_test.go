package varflow

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestStepOutputsMatchesRuntimeProducers(t *testing.T) {
	t.Parallel()
	parameter := func(function string, args any) dsl.Action {
		params := map[string]any{"function": function}
		if args != nil {
			params["args"] = args
		}
		return dsl.Action{Type: "source_parameter", Params: params}
	}
	with := func(action dsl.Action, key string, value any) dsl.Action {
		params := make(map[string]any, len(action.Params)+1)
		for k, v := range action.Params {
			params[k] = v
		}
		params[key] = value
		action.Params = params
		return action
	}
	sourceJSON := func(params map[string]any) dsl.Action {
		return dsl.Action{Type: "source_json", Params: params}
	}
	x := func(name string) Write { return Write{Name: name, Lower: true} }
	known := func(reads []string, writes ...Write) Outputs {
		return Outputs{Known: true, ReadTexts: reads, Writes: writes}
	}

	tests := []struct {
		name   string
		action dsl.Action
		want   Outputs
	}{
		// source_parameter rows 1-15.
		{"lr_save_string reads its value and writes args[1]", parameter("lr_save_string", []string{"{{var.input}}", "Copy"}), known([]string{"{{var.input}}"}, x("Copy"))},
		{"lr_save_int formats scalar args", parameter("lr_save_int", []any{1, "Result"}), known([]string{"1"}, x("Result"))},
		{"lr_save_string needs two args", parameter("lr_save_string", []string{"only"}), Outputs{Known: true}},
		{"random source array reads only the count", with(parameter("lr_save_string", []string{"{{var.ids}}", "Picked"}), "source_array", map[string]any{"name": " IDs ", "selection": "random"}), known([]string{"{{var.ids_count}}"}, x("Picked"))},
		{"non-random source array reads nothing", with(parameter("lr_save_string", []string{"{{var.ids}}", "Picked"}), "source_array", map[string]any{"name": "ids", "selection": "sequential"}), known(nil, x("Picked"))},
		{"lr_save_var reads the buffer and writes args[3]", parameter("lr_save_var", []string{"{{var.buffer}}", "5", "0", "Part"}), known([]string{"{{var.buffer}}"}, x("Part"))},
		{"lr_save_var needs four args", parameter("lr_save_var", []string{"{{var.buffer}}", "5", "Part"}), Outputs{Known: true}},
		{"lr_param_sprintf reads the format and values", parameter("lr_param_sprintf", []string{"Formatted", "%s-%s", "{{var.a}}", "{{var.b}}"}), known([]string{"%s-%s", "{{var.a}}", "{{var.b}}"}, x("Formatted"))},
		{"lr_param_sprintf needs two args", parameter("lr_param_sprintf", []string{"Formatted"}), Outputs{Known: true}},
		{"lr_param_increment writes args[0]", parameter("lr_param_increment", []string{"Counter", "2"}), known(nil, x("Counter"))},
		{"lr_param_increment without args", parameter("lr_param_increment", nil), Outputs{Known: true}},
		{"lr_free_parameter removes args[0]", parameter("lr_free_parameter", []string{"Value"}), Outputs{Known: true, Removes: []string{"Value"}}},
		{"lr_save_timestamp default name", parameter("lr_save_timestamp", nil), known(nil, x("timestamp"))},
		{"lr_save_timestamp first non-empty arg", parameter("lr_save_timestamp", []string{"  ", "Stamp", "DIGITS=10"}), known(nil, x("Stamp"))},
		{"lr_save_timestamp scalar arg", parameter("lr_save_timestamp", "Stamp"), known(nil, x("Stamp"))},
		{"lr_save_datetime one arg", parameter("lr_save_datetime", []string{"Today"}), known(nil, x("Today"))},
		{"lr_save_datetime last arg and format read", parameter("lr_save_datetime", []string{"{{var.format}}", "DATE_NOW", "Out"}), known([]string{"{{var.format}}"}, x("Out"))},
		{"lr_save_datetime blank destination", parameter("lr_save_datetime", []string{"%Y", ""}), known([]string{"%Y"})},
		{"lr_save_datetime default name", parameter("lr_save_datetime", nil), known(nil, x("datetime"))},
		{"lr_whoami", parameter("lr_whoami", nil), known(nil, x("vuser_id"), x("vuser_group"))},
		{"lr_get_host_name default", parameter("lr_get_host_name", nil), known(nil, x("host_name"))},
		{"lr_get_master_host_name named", parameter("lr_get_master_host_name", []string{"Master"}), known(nil, x("Master"))},
		{"lr_get_vuser_ip default", parameter("lr_get_vuser_ip", nil), known(nil, x("vuser_ip"))},
		{"web_convert_param save_param and source_string", with(with(parameter("web_convert_param", []string{"Ignored", "SourceString={{var.other}}"}), "save_param", " c_Token_URL "), "source_string", "{{var.token}}"), known([]string{"{{var.token}}"}, x("c_Token_URL"))},
		{"web_convert_param args fallback", parameter("web_convert_param", []string{"Encoded", "SourceString={{var.token}}", "TargetEncoding=URL"}), known([]string{"{{var.token}}"}, x("Encoded"))},
		{"web_convert_param blank name reads nothing", parameter("web_convert_param", []string{" ", "SourceString={{var.token}}"}), Outputs{Known: true}},
		{"web_save_timestamp_param", parameter("web_save_timestamp_param", []string{"Tstamp"}), known(nil, x("Tstamp"))},
		{"web_save_timestamp_param without args", parameter("web_save_timestamp_param", nil), Outputs{Known: true}},
		{"web_save_param_length", parameter("web_save_param_length", []string{" C_Body ", "Base=Hexadecimal"}), known(nil, x("C_Body_Length"))},
		{"web_save_param_length without args", parameter("web_save_param_length", nil), Outputs{Known: true}},
		{"web_convert_date_param rewrites in place", parameter("web_convert_date_param", []string{"Date", "SourceFormat=%Y", "TargetFormat=%y"}), Outputs{Known: true}},
		{"named fallback", with(parameter("legacy_parameter", []string{" ", "{{var.input}}"}), "save_param", "Result"), known([]string{"{{var.input}}"}, x("Result"))},
		{"blank named fallback key is skipped", with(with(parameter("legacy_parameter", []string{"ready"}), "save_param", "  "), "parameter", "Result"), known([]string{"ready"}, x("Result"))},
		{"call_args SaveParam fallback", with(parameter("lr_save_param_regexp", []string{"{{var.body}}"}), "call_args", []any{map[string]any{"name": "SaveParam", "value": "Matched"}}), known([]string{"{{var.body}}"}, x("Matched"))},
		{"blank call_args SaveParam writes nothing", with(parameter("lr_save_param_regexp", []string{"{{var.body}}"}), "call_args", []map[string]any{{"name": "SaveParam", "value": " "}, {"name": "param", "value": "Later"}}), Outputs{Known: true}},
		{"positional lr_save_searched_string publishes nothing", parameter("lr_save_searched_string", []string{"{{var.body}}", "LB=a", "Found"}), Outputs{Known: true}},
		{"function from metadata", dsl.Action{Type: "source_parameter", Params: map[string]any{"args": []string{"ready", "Copy"}}, Metadata: map[string]any{"source_function": "lr_save_string"}}, known([]string{"ready"}, x("Copy"))},
		{"whitespace function", parameter(" lr_save_string ", []string{"ready", "Copy"}), known([]string{"ready"}, x("Copy"))},
		{"legacy loadrunner_parameter type", dsl.Action{Type: "loadrunner_parameter", Params: map[string]any{"function": "lr_save_string", "args": []string{"ready", "Copy"}}}, known([]string{"ready"}, x("Copy"))},

		// source_json rows 16-19.
		{"lr_eval_json reads the buffer", sourceJSON(map[string]any{"function": "lr_eval_json", "json_object": "doc", "buffer": "{{var.body}}"}), known([]string{"{{var.body}}"})},
		{"lr_json_get_values SelectAll array", sourceJSON(map[string]any{"function": "lr_json_get_values", "json_object": "doc", "value_param": " C_OfferIds ", "query": "$.ids[*]", "select_all": " yes "}), known([]string{"$.ids[*]"}, Write{Name: "C_OfferIds", Lower: true, Array: true})},
		{"lr_json_get_values scalar despite save_all", sourceJSON(map[string]any{"function": "lr_json_get_values", "json_object": "doc", "value_param": "C_Name", "query": "$.name", "save_all": true, "select_all": ""}), known([]string{"$.name"}, x("C_Name"))},
		{"lr_json_get_values legacy args", sourceJSON(map[string]any{"function": "lr_json_get_values", "args": []string{"JsonObject=doc", "ValueParam=Picked", "QueryString=$.items[{{var.index}}]", "SelectAll=Yes"}}), known([]string{"$.items[{{var.index}}]"}, Write{Name: "Picked", Lower: true, Array: true})},
		{"lr_json_get_values without a value param writes nothing", sourceJSON(map[string]any{"function": "lr_json_get_values", "json_object": "doc", "query": "$.name"}), known([]string{"$.name"})},
		{"lr_json_stringify", sourceJSON(map[string]any{"function": "lr_json_stringify", "json_object": "doc", "output_param": "Text"}), known(nil, x("Text"))},
		{"lr_json_stringify without a document writes nothing", sourceJSON(map[string]any{"function": "lr_json_stringify", "output_param": "Text"}), Outputs{Known: true}},
		{"other JSON functions fail and stay unknown", sourceJSON(map[string]any{"function": "lr_json_find", "json_object": "doc"}), Outputs{}},

		// Other producers.
		{"source_secret save param", dsl.Action{Type: "source_secret", Params: map[string]any{"function": "lr_get_secret", "save_param": "Token", "args": []string{"{{var.ignored}}", "Last"}}}, known(nil, x("Token"))},
		{"source_secret last arg", dsl.Action{Type: "source_secret", Params: map[string]any{"function": "lr_get_secret", "args": []string{"vault", "Last"}}}, known(nil, x("Last"))},
		{"source_secret function name", dsl.Action{Type: "loadrunner_secret", Params: map[string]any{"function": "lr_unmask"}}, known(nil, x("unmask"))},
		{"source_message status", dsl.Action{Type: "source_message", Params: map[string]any{"format": "done", "status_message": "true"}}, Outputs{Writes: []Write{{Name: "vuser_status"}}}},
		{"source_message without status", dsl.Action{Type: "source_message", Params: map[string]any{"format": "done", "status_message": false}}, Outputs{}},
		{"oauth default", dsl.Action{Type: "http_oauth2_token"}, Outputs{Writes: []Write{x("access_token"), x("access_token_token_type"), x("access_token_instance_url"), x("access_token_expires_at")}}},
		{"oauth save_as", dsl.Action{Type: "http_oauth2_token", Params: map[string]any{"save_as": " Orders "}}, Outputs{Writes: []Write{x("Orders"), x("Orders_token_type"), x("Orders_instance_url"), x("Orders_expires_at")}}},
		{"browser evaluate result", dsl.Action{Type: "browser_evaluate_js", Target: "document.title"}, Outputs{Writes: []Write{{Name: "result"}}}},
		{"native javascript reads inputs only", dsl.Action{Type: "native_javascript", Params: map[string]any{"source": "{{var.not_read}}", "inputs": map[string]any{"order": "{{var.order}}"}, "result_prefix": " calc "}}, Outputs{Known: true, ReadTexts: []string{`{"order":"{{var.order}}"}`}, Prefixes: []string{"calc"}}},
		{"live table prefix", dsl.Action{Type: "live_table", Params: map[string]any{"result_prefix": " job "}}, Outputs{Prefixes: []string{"job"}}},
		{"live table without prefix", dsl.Action{Type: "live_table", Params: map[string]any{"result_prefix": " "}}, Outputs{}},
		{"source_review is log only", dsl.Action{Type: "source_review", Params: map[string]any{"args": []string{"{{var.x}}"}}}, Outputs{NoReads: true}},
		{"source_external is log only", dsl.Action{Type: "source_external"}, Outputs{NoReads: true}},
		{"custom_call is log only", dsl.Action{Type: "custom_call"}, Outputs{NoReads: true}},
		{"source_database is log only", dsl.Action{Type: "loadrunner_database"}, Outputs{NoReads: true}},
		{"source_transaction uses its args literally", dsl.Action{Type: "source_transaction", Params: map[string]any{"function": "lr_start_transaction", "args": []string{"{{var.x}}"}}}, Outputs{NoReads: true}},
		{"source_timer uses its args literally", dsl.Action{Type: "loadrunner_timer", Params: map[string]any{"args": []string{"{{var.x}}"}}}, Outputs{NoReads: true}},
		{"source_metric only logs", dsl.Action{Type: "source_metric", Params: map[string]any{"args": []string{"m", "{{var.x}}"}}}, Outputs{NoReads: true}},
		{"source_runtime uses its args literally", dsl.Action{Type: "source_runtime", Params: map[string]any{"function": "lr_exit", "args": []string{"{{var.x}}"}}}, Outputs{NoReads: true}},
		{"rendezvous uses its key literally", dsl.Action{Type: "rendezvous", Params: map[string]any{"key": "{{var.x}}"}}, Outputs{NoReads: true}},
		{"requests are not modeled", dsl.Action{Type: "request", Target: "/{{var.x}}"}, Outputs{}},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := StepOutputs(test.action); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("StepOutputs() = %#v\nwant %#v", got, test.want)
			}
		})
	}
}

// API decoding produces []any args, []any call_args and map[string]any
// arrays. Decoded and directly authored actions must have the same outputs.
func TestStepOutputsIgnoresJSONDecoding(t *testing.T) {
	t.Parallel()
	actions := []dsl.Action{
		{Type: "source_parameter", Params: map[string]any{"function": "lr_save_string", "args": []string{"{{var.ids}}", "Picked"}, "source_array": map[string]any{"name": "ids", "selection": "random"}}},
		{Type: "source_parameter", Params: map[string]any{"function": "lr_save_param_regexp", "args": []string{"{{var.body}}"}, "call_args": []map[string]any{{"name": "SaveParam", "value": "Matched"}}}},
		{Type: "source_json", Params: map[string]any{"function": "lr_json_get_values", "args": []string{"JsonObject=doc", "ValueParam=Picked", "SelectAll=Yes"}}},
		{Type: "source_message", Params: map[string]any{"status_message": true}},
	}
	for _, action := range actions {
		payload, err := json.Marshal(action)
		if err != nil {
			t.Fatal(err)
		}
		var decoded dsl.Action
		if err := json.Unmarshal(payload, &decoded); err != nil {
			t.Fatal(err)
		}
		if direct, roundTrip := StepOutputs(action), StepOutputs(decoded); !reflect.DeepEqual(direct, roundTrip) {
			t.Fatalf("decoded outputs differ:\n direct %#v\ndecoded %#v", direct, roundTrip)
		}
	}
}
