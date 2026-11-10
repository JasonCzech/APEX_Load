package dsl

import (
	"strings"
	"testing"
)

func TestGoalConfigTransactionRulePhaseValidation(t *testing.T) {
	rule := func(phase, metric string) *GoalConfig {
		return &GoalConfig{TransactionRules: []TransactionSLARule{{Transaction: "Checkout", Phase: phase, Metric: metric, Comparator: "lt", Threshold: 50}}}
	}
	if issues := validateGoalConfigIssues(rule("connect", "p95_duration_ms")); len(issues) != 0 {
		t.Fatalf("valid phase rule: %v", issues)
	}
	if issues := strings.Join(validateGoalConfigIssues(rule("ttfb", "p95_duration_ms")), "; "); !strings.Contains(issues, ".phase must be") {
		t.Fatalf("unknown phase: %q", issues)
	}
	if issues := strings.Join(validateGoalConfigIssues(rule("tls", "error_count")), "; "); !strings.Contains(issues, ".phase applies only") {
		t.Fatalf("phase on a count metric: %q", issues)
	}
}
