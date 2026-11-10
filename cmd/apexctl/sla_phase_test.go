package main

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestConvertDSLTransactionSLARulesKeepsPhase(t *testing.T) {
	got := convertDSLTransactionSLARules([]dsl.TransactionSLARule{{Transaction: "Checkout", Phase: "connect", Metric: "p95_duration_ms", Comparator: "lt", Threshold: 50}})
	if len(got) != 1 || got[0].Phase != "connect" {
		t.Fatalf("converted = %+v", got)
	}
	if ruleMetricLabel("connect", "p95_duration_ms") != "connect p95_duration_ms" || ruleMetricLabel("", "tps") != "tps" {
		t.Fatal("rule metric label")
	}
}
