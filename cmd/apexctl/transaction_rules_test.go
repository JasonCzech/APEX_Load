package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseTransactionSLARulePipeAndJSON(t *testing.T) {
	for _, raw := range []string{
		"Checkout|p95_duration_ms|lte|800|fail",
		`{"transaction":"Checkout","metric":"p95_duration_ms","comparator":"lte","threshold":800}`,
	} {
		rule, err := parseTransactionSLARule(raw)
		if err != nil {
			t.Fatalf("parse %q: %v", raw, err)
		}
		if rule.Transaction != "Checkout" || rule.Metric != "p95_duration_ms" || rule.Comparator != "lte" || rule.Threshold != 800 || rule.Severity != "fail" {
			t.Fatalf("unexpected rule: %#v", rule)
		}
	}
}

func TestParseRunArgsMergesBoundedTransactionRuleFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "rules.json")
	if err := os.WriteFile(path, []byte(`{"transaction_rules":[{"transaction":"Search","metric":"minimum_count","comparator":"gte","threshold":10,"severity":"warning"}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	args := parseRunArgs([]string{
		"--script", "test.json",
		"--transaction-rule", "Checkout|error_rate_pct|lte|1|fail",
		"--transaction-rules-file", path,
	})
	if args.err != nil {
		t.Fatalf("parse args: %v", args.err)
	}
	if len(args.transactionRules) != 2 || args.transactionRules[1].Transaction != "Search" {
		t.Fatalf("rules = %#v", args.transactionRules)
	}
}
