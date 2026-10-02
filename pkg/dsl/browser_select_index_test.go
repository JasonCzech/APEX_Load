package dsl

import "testing"

func TestBrowserSelectIndexValidation(t *testing.T) {
	for _, test := range []struct {
		name    string
		index   any
		value   string
		values  any
		invalid bool
	}{
		{name: "first", index: 0}, {name: "last bounded", index: 999999}, {name: "decoded", index: float64(2)},
		{name: "negative", index: -1, invalid: true}, {name: "outside", index: 1000000, invalid: true}, {name: "fraction", index: 1.5, invalid: true}, {name: "null", index: nil, invalid: true}, {name: "value conflict", index: 0, value: "Label", invalid: true}, {name: "values conflict", index: 0, values: []string{"Label"}, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			action := Action{Name: "Choose option", Type: "browser_select", Selector: "#choice", Value: test.value, Params: map[string]any{"option_index": test.index}}
			if test.values != nil {
				action.Params["values"] = test.values
			}
			issues := ValidateScriptIssues(&Script{Name: "Selection", Protocol: "browser", Actions: []Action{action}})
			if (len(issues) > 0) != test.invalid {
				t.Fatalf("issues=%v invalid=%v", issues, test.invalid)
			}
		})
	}
}
