package runcontract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestTransactionSLARulePolicy(t *testing.T) {
	valid := TransactionSLARule{Transaction: "Checkout", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 500}
	for _, metric := range []string{"average_duration_ms", "p90_duration_ms", "p95_duration_ms", "p99_duration_ms", "max_acknowledgement_ms", "min_duration_ms", "max_duration_ms", "error_count", "error_rate_pct", "minimum_count", "bytes_sent", "bytes_received", "tps", "max_consumer_lag_ms", "max_consumer_offset_lag"} {
		for _, comparator := range []string{"lt", "lte", "gt", "gte", "eq", "neq"} {
			rule := valid
			rule.Metric, rule.Comparator = metric, comparator
			if err := ValidateTransactionSLARules([]TransactionSLARule{rule}); err != nil {
				t.Fatalf("valid %s/%s: %v", metric, comparator, err)
			}
		}
	}
	for _, tc := range []struct {
		name string
		edit func(*TransactionSLARule)
	}{
		{"empty name", func(r *TransactionSLARule) { r.Transaction = "" }},
		{"whitespace", func(r *TransactionSLARule) { r.Transaction = " Checkout" }},
		{"long name", func(r *TransactionSLARule) { r.Transaction = strings.Repeat("x", 257) }},
		{"population", func(r *TransactionSLARule) { r.Population = "gauge" }},
		{"protocol", func(r *TransactionSLARule) { r.Protocol = "http\n" }},
		{"group", func(r *TransactionSLARule) { r.Group = " group" }},
		{"metric", func(r *TransactionSLARule) { r.Metric = "median" }},
		{"comparator", func(r *TransactionSLARule) { r.Comparator = "approximately" }},
		{"negative", func(r *TransactionSLARule) { r.Threshold = -1 }},
		{"nan", func(r *TransactionSLARule) { r.Threshold = math.NaN() }},
		{"infinity", func(r *TransactionSLARule) { r.Threshold = math.Inf(1) }},
		{"severity", func(r *TransactionSLARule) { r.Severity = "critical" }},
		{"iterations", func(r *TransactionSLARule) { r.Iterations = "first" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rule := valid
			tc.edit(&rule)
			if ValidateTransactionSLARules([]TransactionSLARule{rule}) == nil {
				t.Fatal("invalid rule accepted")
			}
		})
	}
	rules := make([]TransactionSLARule, 128)
	for i := range rules {
		rules[i] = valid
	}
	if err := ValidateTransactionSLARules(rules); err != nil {
		t.Fatal(err)
	}
	if ValidateTransactionSLARules(append(rules, valid)) == nil {
		t.Fatal("rule limit lost")
	}
	if err := ValidateTransactionSLARules(nil); err != nil {
		t.Fatal(err)
	}
}

func TestGoalAndVerdictWirePresence(t *testing.T) {
	zero := 0.0
	goal := GoalConfig{MaxErrorPct: &zero, EarlyStop: &EarlyStopConfig{}, WorkloadShapeConfig: &WorkloadShapeConfig{ClosedVUser: &ClosedVUserConfig{Scope: "scenario", Scenario: &ClosedVUserDefinition{}}}}
	raw, err := json.Marshal(goal)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"max_error_pct":0,"early_stop":{"enabled":false,"error_rate_pct":0,"breach_window":""},"workload_shape_config":{"closed_vuser":{"scope":"scenario","scenario":{"peak_vusers":0,"ramp_up":"","sustain":"","ramp_down_enabled":false,"ramp_down":""}}}}`
	if string(raw) != want {
		t.Fatalf("wire=%s", raw)
	}
	raw, err = json.Marshal(SLABreachReport{TestID: "run", Status: "COMPLETE"})
	if err != nil || string(raw) != `{"test_id":"run","status":"COMPLETE","sla_evaluated":false,"sla_breached":false}` {
		t.Fatalf("unevaluated wire=%s err=%v", raw, err)
	}
}

func TestTerminalVocabulary(t *testing.T) {
	for _, status := range []string{"COMPLETE", "failed", " ABORTED "} {
		if !IsTerminalTestStatus(status) {
			t.Errorf("terminal %q rejected", status)
		}
	}
	for _, status := range []string{"", "RUNNING", "STOPPING", "CANCELLED", "TIMED_OUT", "unknown"} {
		if IsTerminalTestStatus(status) {
			t.Errorf("nonterminal %q accepted", status)
		}
	}
}
