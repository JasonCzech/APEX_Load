package enterprisearchive

import (
	"reflect"
	"testing"
)

func TestStepModeAndResourceAttributes(t *testing.T) {
	result := convertSingleWebSource(t, `
	web_url("a", "URL=https://x.test/a", "Mode=HTTP", "Resource=1", LAST);
	web_url("b", "URL=https://x.test/b", "Mode=HTML", "Resource=0", LAST);
	web_url("c", "URL=https://x.test/c", LAST);`)
	want := []map[string]any{{"html_mode": false, "resource_step": true}, {"html_mode": true}, nil}
	for i, action := range result.Script.Actions {
		got := map[string]any{}
		for _, key := range []string{"html_mode", "resource_step"} {
			if value, ok := action.Params[key]; ok {
				got[key] = value
			}
		}
		if len(got) == 0 {
			got = nil
		}
		if !reflect.DeepEqual(got, want[i]) {
			t.Errorf("action %d params = %v, want %v", i, got, want[i])
		}
	}
}

// A form submitted with no fields is an empty list, which validates and
// saves, rather than null.
func TestSourceHTMLStepWritesAnEmptyFormFieldList(t *testing.T) {
	step, reason := sourceHTMLStep(ParsedFunction{Name: "web_submit_form", Args: map[string]string{}})
	if reason != "" {
		t.Fatalf("form step refused: %s", reason)
	}
	fields, ok := step["fields"].([]any)
	if !ok || fields == nil || len(fields) != 0 {
		t.Fatalf("fields = %#v, want an empty list", step["fields"])
	}
}
