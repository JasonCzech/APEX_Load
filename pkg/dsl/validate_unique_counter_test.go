package dsl

import "testing"

func TestValidateUniqueCounterDefinition(t *testing.T) {
	for _, test := range []struct {
		parameter Parameter
		invalid   bool
	}{
		{Parameter{Name: "id", Type: "counter", Selection: "unique", Step: 1, BlockSize: 999999, Start: 2}, false},
		{Parameter{Name: "id", Type: "counter", Selection: "unique", Step: 1}, true},
		{Parameter{Name: "id", Type: "counter", Selection: "unique", Step: 2, BlockSize: 1}, true},
		{Parameter{Name: "id", Type: "counter", Selection: "unique", Step: 1, BlockSize: 1, End: 10}, true},
		{Parameter{Name: "id", Type: "counter", Selection: "unique", Step: 1, BlockSize: 1, AutoAllocate: true}, true},
	} {
		s := &Script{Name: "unique", Version: "1", Protocol: "http/1.1", Config: ScriptConfig{BaseURL: "https://example.invalid"}, Parameters: []Parameter{test.parameter}, Actions: []Action{{Name: "request", Type: "GET", Target: "https://example.invalid"}}}
		if issues := ValidateScriptIssues(s); (len(issues) != 0) != test.invalid {
			t.Fatalf("parameter=%+v issues=%v invalid=%v", test.parameter, issues, test.invalid)
		}
	}
}
