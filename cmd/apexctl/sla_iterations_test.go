package main

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/dsl"
)

// A script's transaction rule keeps the browser iterations it judges.
func TestScriptTransactionRulesKeepTheirIterations(t *testing.T) {
	converted := convertDSLTransactionSLARules([]dsl.TransactionSLARule{{Transaction: "Open account", Metric: "p90_duration_ms", Comparator: "lte", Threshold: 2000, Iterations: "all"}})
	if len(converted) != 1 || converted[0].Iterations != "all" {
		t.Fatalf("converted = %+v", converted)
	}
}
