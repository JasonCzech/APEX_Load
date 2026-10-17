package main

import (
	"strings"
	"testing"
)

func TestExplainScriptFileCountsChecksAcrossScopes(t *testing.T) {
	signal := `{"name":"create","type":"request","method":"POST","target":"/orders","extractors":[{"name":"id","source":"body","type":"json_path","expression":"$.id"}],"assertions":[{"type":"status_code","expected":201}]}`
	plain := `{"name":"read","type":"request","method":"GET","target":"/orders"}`
	for _, tc := range []struct {
		name, sections      string
		checks, rootActions int
	}{
		{"no checks", `"actions":[` + plain + `]`, 0, 1},
		{"root actions", `"actions":[` + signal + `,` + signal + `]`, 2, 2},
		{"nested loop", `"actions":[{"name":"loop","type":"control_loop","control":{"count":10,"steps":[` + signal + `]}}]`, 1, 1},
		{"if and else", `"actions":[{"name":"if","type":"control_if","control":{"condition":{"expr":"true"},"steps":[` + signal + `],"else":[` + signal + `]}}]`, 2, 1},
		{"switch and default", `"actions":[{"name":"switch","type":"control_switch","control":{"cases":[{"condition":{"expr":"true"},"steps":[` + signal + `]}],"default":[` + signal + `]}}]`, 2, 1},
		{"parallel branches", `"actions":[{"name":"parallel","type":"control_parallel","control":{"branches":[{"name":"a","steps":[` + signal + `]},{"name":"b","steps":[` + signal + `] }]}}]`, 2, 1},
		{"init", `"init":[` + signal + `],"actions":[` + plain + `]`, 1, 1},
		{"teardown", `"actions":[` + plain + `],"teardown":[` + signal + `]`, 1, 1},
		{"sequence", `"actions":[{"name":"sequence","type":"http_sequence","params":{"requests":[` + signal + `,` + signal + `]}}]`, 2, 1},
		{"nested lifecycle sequence", `"init":[{"name":"loop","type":"control_loop","control":{"count":2,"steps":[{"name":"sequence","type":"http_sequence","params":{"requests":[` + signal + `]}}]}}],"actions":[` + plain + `]`, 1, 1},
		{"async request", `"actions":[{"name":"background","type":"http_async_start","params":{"id":"once","pattern":"once","request":` + signal + `}}]`, 1, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeTempScript(t, `{"name":"explain","protocol":"http","config":{"base_url":"https://example.com"},`+tc.sections+`}`)
			result, err := explainScriptFile(path)
			if err != nil {
				t.Fatalf("valid fixture did not parse: %v", err)
			}
			if result.Assertions != tc.checks || result.Extractors != tc.checks || result.Actions != tc.rootActions {
				t.Fatalf("counts = actions %d, assertions %d, extractors %d; want %d, %d, %d", result.Actions, result.Assertions, result.Extractors, tc.rootActions, tc.checks, tc.checks)
			}
			var recommendsAssertions, recommendsExtractors bool
			for _, recommendation := range result.NextActions {
				recommendsAssertions = recommendsAssertions || strings.HasPrefix(recommendation, "Add assertions")
				recommendsExtractors = recommendsExtractors || strings.HasPrefix(recommendation, "Review responses")
			}
			if recommendsAssertions != (tc.checks == 0) || recommendsExtractors != (tc.checks == 0) {
				t.Errorf("recommendations do not reflect authored checks: %v", result.NextActions)
			}
		})
	}
}
