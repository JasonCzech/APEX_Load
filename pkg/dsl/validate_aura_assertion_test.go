package dsl

import (
	"strings"
	"testing"
)

func TestValidateAuraAssertionContract(t *testing.T) {
	for _, tc := range []struct {
		name      string
		assertion Assertion
		valid     bool
	}{
		{"structural assertion", Assertion{Type: "aura_actions_success"}, true},
		{"captured ids forbidden", Assertion{Type: "aura_actions_success", Value: "recorded-id"}, false},
		{"expected state override forbidden", Assertion{Type: "aura_actions_success", Expected: "ERROR"}, false},
		{"partial count override forbidden", Assertion{Type: "aura_actions_success", Min: 1}, false},
		{"legacy bypass forbidden", Assertion{Type: "aura_actions_success", LegacyHTMLCheck: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &Script{Name: "Aura validation", Protocol: "http", Config: ScriptConfig{BaseURL: "https://example.test"}, Actions: []Action{{Name: "Aura batch", Type: "request", Method: "POST", Target: "/aura", Body: `message=%7B%22actions%22%3A%5B%7B%22id%22%3A%221%22%7D%5D%7D`, Assertions: []Assertion{tc.assertion}}}}
			issues := ValidateScriptIssues(s)
			got := strings.Join(issues, "\n")
			if tc.valid && len(issues) > 0 {
				t.Fatalf("valid assertion rejected: %s", got)
			}
			if !tc.valid && !strings.Contains(got, "aura_actions_success") {
				t.Fatalf("invalid assertion accepted or missing diagnostic: %s", got)
			}
		})
	}
}
