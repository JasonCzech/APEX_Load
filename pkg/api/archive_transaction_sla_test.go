package api

import (
	"encoding/json"
	"os"
	"testing"
	"time"
)

func TestApplyTransactionSLARulesUsesCompleteExactArchiveEvidence(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(root + "/transactions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	encoder := json.NewEncoder(file)
	events := []TransactionEvent{
		{ActionName: "Checkout", BusinessTransaction: true, DurationMs: 100, Success: true, BytesSent: 10, BytesReceived: 100},
		{ActionName: "Checkout", BusinessTransaction: true, DurationMs: 200, Success: false, BytesSent: 20, BytesReceived: 200},
		{ActionName: "Checkout", BusinessTransaction: false, DurationMs: 900, Success: false},
		{ActionName: "checkout", BusinessTransaction: true, DurationMs: 1, Success: true},
	}
	for _, event := range events {
		if err := encoder.Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}

	started := time.Date(2026, 8, 25, 12, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{
		TransactionsFile: "transactions.jsonl",
		CreatedAt:        started,
		CompletedAt:      started.Add(10 * time.Second),
		SLAEvaluated:     true,
		SLADetails:       []string{"existing global detail"},
		Provenance: RunArchiveProvenance{StartedAt: &started, GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{
			{Transaction: "Checkout", Metric: "average_duration_ms", Comparator: "lte", Threshold: 150, Severity: "fail"},
			{Transaction: "Checkout", Metric: "error_rate_pct", Comparator: "lt", Threshold: 10, Severity: "warning"},
			{Transaction: "Checkout", Metric: "minimum_count", Comparator: "gte", Threshold: 3, Severity: "fail"},
			{Transaction: "Checkout", Metric: "bytes_received", Comparator: "eq", Threshold: 300, Severity: "fail"},
			{Transaction: "Missing", Metric: "minimum_count", Comparator: "eq", Threshold: 0, Severity: "fail"},
			{Transaction: "Missing", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 100, Severity: "fail"},
			{Transaction: "Checkout", Metric: "tps", Comparator: "eq", Threshold: .1, Severity: "fail"},
		}}},
	}

	if err := applyTransactionSLARules(root, &manifest); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(manifest.TransactionRuleResults) != 7 {
		t.Fatalf("results = %d, want 7", len(manifest.TransactionRuleResults))
	}
	wantStatuses := []string{"pass", "warning", "fail", "pass", "pass", "not_evaluated", "pass"}
	for index, want := range wantStatuses {
		if got := manifest.TransactionRuleResults[index].Status; got != want {
			t.Errorf("result[%d].status = %q, want %q (%s)", index, got, want, manifest.TransactionRuleResults[index].Message)
		}
	}
	if !manifest.SLABreached {
		t.Fatal("fail-severity minimum-count breach did not set sla_breached")
	}
	if manifest.SLAEvaluated {
		t.Fatal("SLAEvaluated = true with a p95 rule that had insufficient evidence")
	}
	if len(manifest.SLADetails) != 3 {
		t.Fatalf("sla details = %#v, want existing + warning + fail", manifest.SLADetails)
	}
	if got := manifest.TransactionRuleResults[0].MatchedCount; got != 2 {
		t.Fatalf("matched count = %d, want exact-name business transaction count 2", got)
	}
}

func TestTransactionSLARulesPreserveIncompleteLiveSLAEvidence(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(root + "/transactions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.NewEncoder(file).Encode(TransactionEvent{ActionName: "Checkout", BusinessTransaction: true, DurationMs: 10, Success: true}); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	p95 := 100.0
	manifest := RunArchiveManifest{
		TransactionsFile: "transactions.jsonl",
		CreatedAt:        time.Now().Add(-time.Second), CompletedAt: time.Now(), SLAEvaluated: false,
		Provenance: RunArchiveProvenance{GoalConfig: &GoalConfig{
			P95LatencyMs:     &p95,
			TransactionRules: []TransactionSLARule{{Transaction: "Checkout", Metric: "minimum_count", Comparator: "gte", Threshold: 1, Severity: "fail"}},
		}},
	}
	if err := applyTransactionSLARules(root, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SLAEvaluated || len(manifest.TransactionRuleResults) != 1 || manifest.TransactionRuleResults[0].Status != "pass" {
		t.Fatalf("manifest = %#v", manifest)
	}
}

func TestValidateTransactionSLARulesBoundsContract(t *testing.T) {
	valid := TransactionSLARule{Transaction: "Checkout", Metric: "p95_duration_ms", Comparator: "lte", Threshold: 500, Severity: "fail"}
	if err := ValidateTransactionSLARules([]TransactionSLARule{valid}); err != nil {
		t.Fatalf("valid rule: %v", err)
	}
	tests := []struct {
		name string
		rule TransactionSLARule
	}{
		{name: "whitespace name", rule: TransactionSLARule{Transaction: " Checkout", Metric: "minimum_count", Comparator: "gte"}},
		{name: "unknown metric", rule: TransactionSLARule{Transaction: "Checkout", Metric: "median", Comparator: "lte"}},
		{name: "unknown comparator", rule: TransactionSLARule{Transaction: "Checkout", Metric: "minimum_count", Comparator: "approximately"}},
		{name: "negative threshold", rule: TransactionSLARule{Transaction: "Checkout", Metric: "minimum_count", Comparator: "gte", Threshold: -1}},
		{name: "unknown severity", rule: TransactionSLARule{Transaction: "Checkout", Metric: "minimum_count", Comparator: "gte", Severity: "critical"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := ValidateTransactionSLARules([]TransactionSLARule{test.rule}); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}

func TestScopedActionSLAUsesIntersectionAndRequiresCoverage(t *testing.T) {
	root := t.TempDir()
	file, err := os.Create(root + "/transactions.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	for _, event := range []TransactionEvent{{ActionName: "Receive", Population: "action", Group: "consumer", Success: true, DurationMs: 10}, {ActionName: "Receive", Population: "action", Group: "other", Success: true, DurationMs: 900}, {ActionName: "Receive", Population: "transaction", BusinessTransaction: true, Group: "consumer", Success: true, DurationMs: 500}} {
		if err := json.NewEncoder(file).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	rule := TransactionSLARule{Population: "action", Protocol: "kafka", Group: "consumer", Transaction: "Receive", Metric: "p99_duration_ms", Comparator: "lte", Threshold: 20}
	m := RunArchiveManifest{TransactionsFile: "transactions.jsonl", Provenance: RunArchiveProvenance{Groups: []TestGroup{{Name: "consumer", Protocol: "kafka"}, {Name: "other", Protocol: "http"}}, GoalConfig: &GoalConfig{TransactionRules: []TransactionSLARule{rule}}}}
	if err := applyTransactionSLARules(root, &m); err != nil {
		t.Fatal(err)
	}
	if r := m.TransactionRuleResults[0]; r.Status != "pass" || r.MatchedCount != 1 {
		t.Fatalf("pooled scopes: %+v", r)
	}
	m.SourceCoverage = &RunArchiveSourceCoverage{Sources: []RunArchiveSource{{Collected: false}}}
	if err := applyTransactionSLARules(root, &m); err != nil {
		t.Fatal(err)
	}
	if r := m.TransactionRuleResults[0]; r.Status != "not_evaluated" || r.Observed != nil {
		t.Fatalf("missing source scored: %+v", r)
	}
	m.SourceCoverage = nil
	m.Provenance.Groups = nil
	if err := applyTransactionSLARules(root, &m); err != nil {
		t.Fatal(err)
	}
	if r := m.TransactionRuleResults[0]; r.Status != "not_evaluated" {
		t.Fatalf("unknown protocol scored: %+v", r)
	}
}
