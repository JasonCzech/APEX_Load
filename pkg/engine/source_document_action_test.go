package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// Helpers APEX does not evaluate must fail the step instead of fabricating a
// variable from an argument literal and reporting success.
func TestUnimplementedSourceDocumentActionsFail(t *testing.T) {
	for _, tc := range []struct {
		actionType, function string
		args                 []any
	}{
		{"source_json", "lr_json_set_values", []any{"JsonObject=JSON_param", "ValueParam=c_value", "QueryString=$.Id"}},
		{"source_xml", "lr_xml_get_values", []any{"XML={{var.c_xml}}", "ValueParam=c_value", "Query=/a/b"}},
		{"source_xml", "lr_xml_find", []any{"XML={{var.c_xml}}", "Query=/a/b", "SaveParam=c_found"}},
	} {
		t.Run(tc.function, func(t *testing.T) {
			vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Variables: map[string]string{"c_jsonresponse": `{"Id":"a"}`, "c_xml": "<a><b>x</b></a>"}}
			action := dsl.Action{Name: "helper", Type: tc.actionType, Params: map[string]any{"function": tc.function, "support_mode": "executable", "args": tc.args}}
			handled, err := vu.handleEngineAction(context.Background(), action)
			if !handled || err == nil || !strings.Contains(err.Error(), tc.function) {
				t.Fatalf("handled=%v err=%v, want explicit %s failure", handled, err, tc.function)
			}
			if len(vu.Variables) != 2 {
				t.Fatalf("helper wrote variables: %v", vu.Variables)
			}
		})
	}
}

func runSourceJSON(t *testing.T, vu *VUser, function string, params map[string]any) error {
	t.Helper()
	all := map[string]any{"function": function, "support_mode": "executable"}
	for key, value := range params {
		all[key] = value
	}
	handled, err := vu.handleEngineAction(context.Background(), dsl.Action{Name: function, Type: "source_json", Params: all})
	if !handled {
		t.Fatalf("%s not dispatched", function)
	}
	return err
}

// The iPad store check-in script parses an Aura return value and reads the
// store Id and Name into parameters that later requests send.
func TestSourceJSONHelpersReadAuraReturnValues(t *testing.T) {
	vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Variables: map[string]string{
		"c_jsonresponse": `{"Id":"a1k4M000004XZFrQAO","Name":"Store & Café","Queues":[{"Id":"q1"},{"Id":"q2"},{"Id":"q3"}],"Score":2.50}`,
	}}
	if err := runSourceJSON(t, vu, "lr_eval_json", map[string]any{"json_object": "JSON_param", "buffer": "{{var.c_jsonresponse}}"}); err != nil {
		t.Fatal(err)
	}
	if err := runSourceJSON(t, vu, "lr_json_get_values", map[string]any{"json_object": "json_PARAM", "value_param": "c_StoreId", "query": "$.Id", "select_all": "no"}); err != nil {
		t.Fatal(err)
	}
	if err := runSourceJSON(t, vu, "lr_json_get_values", map[string]any{"json_object": "JSON_param", "value_param": "c_Score", "query": "$.Score"}); err != nil {
		t.Fatal(err)
	}
	if vu.Variables["c_StoreId"] != "a1k4M000004XZFrQAO" || vu.Variables["c_storeid"] != "a1k4M000004XZFrQAO" || vu.Variables["c_Score"] != "2.50" {
		t.Fatalf("saved values: %v", vu.Variables)
	}
	if err := runSourceJSON(t, vu, "lr_json_stringify", map[string]any{"json_object": "JSON_param", "format": "auto", "output_param": "Result"}); err != nil {
		t.Fatal(err)
	}
	if vu.Variables["Result"] != vu.Variables["c_jsonresponse"] {
		t.Fatalf("stringify = %s, want the original document", vu.Variables["Result"])
	}

	// SelectAll=Yes publishes an array and clears entries from a longer result.
	if err := runSourceJSON(t, vu, "lr_json_get_values", map[string]any{"json_object": "JSON_param", "value_param": "c_Queue", "query": "$.Queues[*].Id", "select_all": "yes"}); err != nil {
		t.Fatal(err)
	}
	if vu.Variables["c_Queue_count"] != "3" || vu.Variables["c_Queue_3"] != "q3" {
		t.Fatalf("array: %v", vu.Variables)
	}
	if err := runSourceJSON(t, vu, "lr_json_get_values", map[string]any{"json_object": "JSON_param", "value_param": "c_Queue", "query": "$.Queues[0].Id", "select_all": "yes"}); err != nil {
		t.Fatal(err)
	}
	if _, stale := vu.Variables["c_Queue_2"]; stale || vu.Variables["c_Queue_count"] != "1" {
		t.Fatalf("stale array entries survived: %v", vu.Variables)
	}

	// A miss fails by default; NotFound=Continue leaves the parameter unset.
	if err := runSourceJSON(t, vu, "lr_json_get_values", map[string]any{"json_object": "JSON_param", "value_param": "c_Missing", "query": "$.Missing"}); err == nil || !strings.Contains(err.Error(), "matched nothing") {
		t.Fatalf("miss error = %v", err)
	}
	if err := runSourceJSON(t, vu, "lr_json_get_values", map[string]any{"json_object": "JSON_param", "value_param": "c_Missing", "query": "$.Missing", "not_found": "continue"}); err != nil {
		t.Fatal(err)
	}
	if _, set := vu.Variables["c_Missing"]; set {
		t.Fatal("NotFound=Continue set the parameter")
	}
}

func TestSourceJSONHelpersAcceptLegacyArguments(t *testing.T) {
	vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Variables: map[string]string{"c_jsonresponse": `{"Id":"a"}`}}
	for _, step := range []struct {
		function string
		args     []any
	}{
		{"lr_eval_json", []any{"Buffer={{var.c_jsonresponse}}", "JsonObject=JSON_param"}},
		{"lr_json_get_values", []any{"JsonObject=JSON_param", "ValueParam=c_StoreId", "QueryString=$.Id", "SelectAll=No"}},
	} {
		if err := runSourceJSON(t, vu, step.function, map[string]any{"args": step.args}); err != nil {
			t.Fatalf("%s: %v", step.function, err)
		}
	}
	if vu.Variables["c_StoreId"] != "a" {
		t.Fatalf("legacy arguments: %v", vu.Variables)
	}
}

func TestSourceJSONHelpersFailClearly(t *testing.T) {
	vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Variables: map[string]string{"bad": `{"Id":`}}
	for _, tc := range []struct {
		function string
		params   map[string]any
		want     string
	}{
		{"lr_eval_json", map[string]any{"json_object": "J", "buffer": "{{var.unset}}"}, "{{var.unset}} has no value"},
		{"lr_eval_json", map[string]any{"json_object": "J", "buffer": "{{var.bad}}"}, "invalid JSON"},
		{"lr_json_get_values", map[string]any{"json_object": "Never", "value_param": "v", "query": "$.Id"}, "not created by a preceding lr_eval_json"},
		{"lr_json_stringify", map[string]any{"json_object": "Never", "output_param": "v"}, "not created by a preceding lr_eval_json"},
	} {
		if err := runSourceJSON(t, vu, tc.function, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s error = %v, want %q", tc.function, err, tc.want)
		}
	}
}

// A resolved document may contain template-looking text; it is data.
func TestSourceJSONEvalAcceptsResolvedTemplateText(t *testing.T) {
	vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Variables: map[string]string{"doc": `{"label":"Hello {{name}}","Id":"a1"}`}}
	if err := runSourceJSON(t, vu, "lr_eval_json", map[string]any{"json_object": "J", "buffer": "{{var.doc}}"}); err != nil {
		t.Fatalf("resolved document with template text rejected: %v", err)
	}
	if err := runSourceJSON(t, vu, "lr_json_get_values", map[string]any{"json_object": "J", "value_param": "label", "query": "$.label"}); err != nil {
		t.Fatal(err)
	}
	if vu.Variables["label"] != "Hello {{name}}" {
		t.Fatalf("label = %q", vu.Variables["label"])
	}
}

// An each-occurrence parameter in Buffer advances once per lr_eval_json, as it
// would for any other single reference.
func TestSourceJSONEvalResolvesEachOccurrenceParameterOnce(t *testing.T) {
	params := NewParameterRuntime([]dsl.Parameter{{Name: "doc_id", Type: "counter", Update: "each_occurrence", Start: 30, Step: 2}}, nil, 1)
	vu := &VUser{ID: 1, Group: "g", Script: &dsl.Script{Name: "s"}, Variables: map[string]string{}, Parameters: params}
	if err := runSourceJSON(t, vu, "lr_eval_json", map[string]any{"json_object": "J", "buffer": `{"Id":{{param.doc_id}}}`}); err != nil {
		t.Fatal(err)
	}
	if err := runSourceJSON(t, vu, "lr_json_get_values", map[string]any{"json_object": "J", "value_param": "id", "query": "$.Id"}); err != nil {
		t.Fatal(err)
	}
	if vu.Variables["id"] != "30" {
		t.Fatalf("Buffer saw counter value %q, want the first value 30", vu.Variables["id"])
	}
}
