package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A draft saved before LoadRunner JSON/XML helpers became review-only must not
// fabricate a variable from an argument literal and report success.
func TestSourceDocumentActionsFailInsteadOfStoringArgumentLiterals(t *testing.T) {
	for _, tc := range []struct {
		actionType, function string
		args                 []any
	}{
		{"source_json", "lr_json_get_values", []any{"JsonObject=JSON_param", "ValueParam=c_StoreId", "QueryString=$.Id", "SelectAll=No"}},
		{"source_json", "lr_eval_json", []any{"Buffer={{var.c_jsonresponse}}", "JsonObject=JSON_param"}},
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
