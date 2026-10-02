package api

import "testing"

func TestMessagingTransactionSLARulesRequireCompleteObservedEvidence(t *testing.T) {
	for _, metric := range []string{"max_consumer_lag_ms", "max_consumer_offset_lag"} {
		rule := TransactionSLARule{Transaction: "Consume", Metric: metric, Comparator: "lte", Threshold: 5}
		if err := ValidateTransactionSLARules([]TransactionSLARule{rule}); err != nil {
			t.Fatal(err)
		}
		for _, stats := range []*transactionSLAAccumulator{nil, {count: 1}, {count: 2, lagSamples: 1, offsetSamples: 1}} {
			if got := evaluateTransactionSLARule(rule, stats, 1); got.Status != "not_evaluated" {
				t.Fatalf("missing telemetry scored: %+v", got)
			}
		}
		stats := &transactionSLAAccumulator{count: 1, lagSamples: 1, offsetSamples: 1}
		if got := evaluateTransactionSLARule(rule, stats, 1); got.Status != "pass" || got.Observed == nil || *got.Observed != 0 {
			t.Fatalf("observed zero lost: %+v", got)
		}
		stats.maxLag = 10
		stats.maxOffset = 10
		if got := evaluateTransactionSLARule(rule, stats, 1); got.Status != "fail" {
			t.Fatalf("lag did not fail: %+v", got)
		}
	}
}

func TestAcknowledgementSLARequiresFullCoverage(t *testing.T) {
	rule := TransactionSLARule{Population: "action", Protocol: "kafka", Group: "producer", Transaction: "Send", Metric: "max_acknowledgement_ms", Comparator: "lte", Threshold: 5}
	if err := ValidateTransactionSLARules([]TransactionSLARule{rule}); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		stats  *transactionSLAAccumulator
		status string
	}{{nil, "not_evaluated"}, {&transactionSLAAccumulator{count: 2, ackSamples: 1}, "not_evaluated"}, {&transactionSLAAccumulator{count: 2, ackSamples: 2}, "pass"}, {&transactionSLAAccumulator{count: 2, ackSamples: 2, maxAck: 10}, "fail"}} {
		if r := evaluateTransactionSLARule(rule, tc.stats, 1); r.Status != tc.status {
			t.Fatalf("%+v", r)
		}
	}
	for _, bad := range []TransactionSLARule{{Population: "gauge", Transaction: "Send", Metric: "minimum_count", Comparator: "gte"}, {Protocol: " kafka", Transaction: "Send", Metric: "minimum_count", Comparator: "gte"}} {
		if ValidateTransactionSLARules([]TransactionSLARule{bad}) == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}
