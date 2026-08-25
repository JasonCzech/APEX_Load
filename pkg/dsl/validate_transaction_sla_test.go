package dsl

import "testing"

func TestValidateTransactionSLARules(t *testing.T) {
	script := &Script{
		Name: "rules", Protocol: "http", Version: "1.0",
		Config: ScriptConfig{BaseURL: "https://example.test"},
		GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{{
			Transaction: "Checkout", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 500, Severity: "fail",
		}}},
		Actions: []Action{{Name: "request", Type: "request", Method: "GET", Target: "/"}},
	}
	if issues := ValidateScriptIssues(script); len(issues) != 0 {
		t.Fatalf("valid rules produced issues: %#v", issues)
	}
	script.GoalConfig.TransactionRules[0].Metric = "median"
	if issues := ValidateScriptIssues(script); len(issues) == 0 {
		t.Fatal("unknown terminal transaction metric was accepted")
	}
}
