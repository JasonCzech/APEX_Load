package enterprisearchive

import (
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/dsl"
)

func convertJSONHelperSource(t *testing.T, body string) *dsl.Script {
	t.Helper()
	parsed, err := ParseActionFile("Action.c", "Action()\n{\n"+body+"\treturn 0;\n}\n")
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "json-helpers"})
	if err != nil {
		t.Fatal(err)
	}
	return result.Script
}

func helperAction(t *testing.T, script *dsl.Script, function string) dsl.Action {
	t.Helper()
	for _, action := range script.Actions {
		if name, _ := action.Params["function"].(string); name == function {
			return action
		}
	}
	t.Fatalf("%s not converted", function)
	return dsl.Action{}
}

// The iPad store check-in script reads an Aura return value with the three
// JSON helpers APEX evaluates; their parameters feed later requests.
func TestJSONHelpersImportAsExecutableProducers(t *testing.T) {
	script := convertJSONHelperSource(t, ""+
		"\tweb_reg_save_param_json(\"ParamName=c_jsonresponse\",\"QueryString=$.actions[0].returnValue.returnValue\",SEARCH_FILTERS,LAST);\n"+
		"\tweb_url(\"store\",\"URL=https://example.com/aura\",LAST);\n"+
		"\tlr_eval_json(\"Buffer={c_jsonresponse}\",\"JsonObject=JSON_param\", LAST);\n"+
		"\tlr_json_stringify(\"JsonObject=JSON_param\",\"Format=auto\",\"OutputParam=Result\",LAST);\n"+
		"\tlr_json_get_values(\"JsonObject=JSON_param\",\"ValueParam=c_StoreId\",\"QueryString=$.Id\",\"SelectAll=No\",LAST);\n"+
		"\tlr_json_get_values(\"JsonObject=JSON_param\",\"ValueParam=c_Queue\",\"QueryString=$.Queues[*].Id\",\"SelectAll=Yes\",\"NotFound=Continue\",LAST);\n"+
		"\tlr_xml_get_values(\"XML={c_xml}\",\"ValueParam=c_value\",\"Query=/a/b\",LAST);\n"+
		"\tweb_url(\"use\",\"URL=https://example.com/use?store={c_StoreId}&queues={c_Queue_count}&json={Result}\",LAST);\n")

	eval := helperAction(t, script, "lr_eval_json")
	if eval.Type != "source_json" || eval.Params["json_object"] != "JSON_param" || eval.Params["buffer"] != "{{var.c_jsonresponse}}" {
		t.Fatalf("lr_eval_json = %s %v", eval.Type, eval.Params)
	}
	get := helperAction(t, script, "lr_json_get_values")
	if get.Type != "source_json" || get.Params["value_param"] != "c_StoreId" || get.Params["query"] != "$.Id" || get.Params["save_param"] != "c_StoreId" {
		t.Fatalf("lr_json_get_values = %s %v", get.Type, get.Params)
	}
	stringify := helperAction(t, script, "lr_json_stringify")
	if stringify.Type != "source_json" || stringify.Params["output_param"] != "Result" || stringify.Params["save_param"] != "Result" {
		t.Fatalf("lr_json_stringify = %s %v", stringify.Type, stringify.Params)
	}
	if xml := helperAction(t, script, "lr_xml_get_values"); xml.Type != "source_review" {
		t.Fatalf("lr_xml_get_values = %s, want gated source_review", xml.Type)
	}

	findings, err := importreview.AnalyzeDependencies(script)
	if err != nil {
		t.Fatal(err)
	}
	for _, finding := range findings {
		if strings.HasPrefix(finding.Code, "missing_producer:") && finding.Element != "c_xml" {
			t.Errorf("JSON helper output reported as unproduced: %+v", finding)
		}
	}
}

// SelectAll=Yes publishes NAME_1..NAME_n and NAME_count; a bare NAME
// reference has no producer.
func TestJSONHelperSelectAllDoesNotProduceTheBareName(t *testing.T) {
	script := convertJSONHelperSource(t, ""+
		"\tlr_eval_json(\"Buffer={c_jsonresponse}\",\"JsonObject=J\", LAST);\n"+
		"\tlr_json_get_values(\"JsonObject=J\",\"ValueParam=c_Queue\",\"QueryString=$.Queues[*].Id\",\"SelectAll=Yes\",LAST);\n"+
		"\tweb_url(\"use\",\"URL=https://example.com/use?q={c_Queue}&n={c_Queue_count}\",LAST);\n")
	findings, err := importreview.AnalyzeDependencies(script)
	if err != nil {
		t.Fatal(err)
	}
	missing := map[string]bool{}
	for _, finding := range findings {
		if strings.HasPrefix(finding.Code, "missing_producer:") {
			missing[strings.ToLower(finding.Element)] = true
		}
	}
	if !missing["c_queue"] || missing["c_queue_count"] {
		t.Fatalf("missing producers = %v, want c_Queue but not c_Queue_count", missing)
	}
}

func TestJSONHelpersOutsideTheEvaluatedSubsetAreGated(t *testing.T) {
	for _, tc := range []struct {
		name, call, function, want string
	}{
		{"JSONPath filter", `lr_json_get_values("JsonObject=J","ValueParam=v","QueryString=$.a[?(@.b=='c')].d",LAST);`, "lr_json_get_values", "source_json_query_unsupported"},
		{"invalid JSONPath", `lr_json_get_values("JsonObject=J","ValueParam=v","QueryString=Id",LAST);`, "lr_json_get_values", "source_json_query_invalid"},
		{"file buffer", `lr_eval_json("Buffer/File=store.json","JsonObject=J",LAST);`, "lr_eval_json", "source_json_buffer_file_unsupported"},
		{"unknown argument", `lr_json_get_values("JsonObject=J","ValueParam=v","QueryString=$.a","Base64=Yes",LAST);`, "lr_json_get_values", "source_json_argument_unsupported"},
		{"missing argument", `lr_json_get_values("JsonObject=J","QueryString=$.a",LAST);`, "lr_json_get_values", "source_json_argument_missing"},
		{"bad format", `lr_json_stringify("JsonObject=J","Format=pretty","OutputParam=o",LAST);`, "lr_json_stringify", "source_json_option_invalid"},
		{"unevaluated helper", `lr_json_set_values("JsonObject=J","Value=1","QueryString=$.a",LAST);`, "lr_json_set_values", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			action := helperAction(t, convertJSONHelperSource(t, "\t"+tc.call+"\n"), tc.function)
			if action.Type != "source_review" {
				t.Fatalf("%s converted as %s, want gated source_review", tc.function, action.Type)
			}
			if tc.want == "" {
				return
			}
			findings, err := importreview.FindingsFromScript(&dsl.Script{Actions: []dsl.Action{action}})
			if err != nil {
				t.Fatal(err)
			}
			matched := false
			for _, finding := range findings {
				if finding.Gate == importreview.GateExecution && finding.Code == tc.want {
					matched = true
				}
			}
			if !matched {
				t.Fatalf("findings %+v lack execution gate %s", findings, tc.want)
			}
		})
	}
}
