package enterprisearchive

import (
	"testing"
)

// LoadRunner JSON/XML document helpers are not evaluated by the runtime, so the
// importer must not label them executable or publish their outputs.
func TestDocumentHelpersImportAsGatedSourceReview(t *testing.T) {
	src := "Action()\n{\n" +
		"\tlr_eval_json(\"Buffer={c_jsonresponse}\",\"JsonObject=JSON_param\", LAST);\n" +
		"\tlr_json_get_values(\"JsonObject=JSON_param\",\"ValueParam=c_StoreId\",\"QueryString=$.Id\",\"SelectAll=No\",LAST);\n" +
		"\tlr_json_stringify(\"JsonObject=JSON_param\",\"Format=auto\",\"OutputParam=Result\",LAST);\n" +
		"\tlr_xml_get_values(\"XML={c_xml}\",\"ValueParam=c_value\",\"Query=/a/b\",LAST);\n" +
		"\treturn 0;\n}\n"
	parsed, err := ParseActionFile("Action.c", src)
	if err != nil {
		t.Fatal(err)
	}
	result, err := Convert([]*ParsedAction{parsed}, ConvertOptions{ScriptName: "document-helpers"})
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, action := range result.Script.Actions {
		function, _ := action.Params["function"].(string)
		switch function {
		case "lr_eval_json", "lr_json_get_values", "lr_json_stringify", "lr_xml_get_values":
			seen++
			if action.Type != "source_review" || action.Params["support_mode"] != string(SourceFunctionSupportReviewOnly) {
				t.Fatalf("%s converted as %s/%v, want gated source_review", function, action.Type, action.Params["support_mode"])
			}
		}
	}
	if seen != 4 {
		t.Fatalf("converted helper steps=%d, want 4", seen)
	}
}
