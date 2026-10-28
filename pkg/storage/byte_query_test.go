package storage

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// A byte query selects HTTP actions' body bytes: an exact action filter at
// most, no grouping, property or derivation, a bounded window.
func TestNativeByteQueryValidation(t *testing.T) {
	base := AnalyticsQuery{QueryVersion: AnalyticsQueryVersion, Population: analysis.PopulationByte, Metric: "bytes_sent", Aggregation: "sum", EndSeconds: 60, BucketSeconds: 10}
	accept := []func(*AnalyticsQuery){
		func(*AnalyticsQuery) {},
		func(q *AnalyticsQuery) { q.Filters = map[string]string{"action": "login"} },
		func(q *AnalyticsQuery) { q.Filters = map[string]string{"protocol": "http"} },
		func(q *AnalyticsQuery) { q.Filters = map[string]string{"protocol": "grpc", "action": "Greeter/SayHello"} },
		func(q *AnalyticsQuery) { q.Filters = map[string]string{"protocol": "websocket", "action": "ws_send"} },
		func(q *AnalyticsQuery) { q.Metric, q.Aggregation = "bytes_received", "p95" },
		func(q *AnalyticsQuery) { q.Metric, q.Aggregation = "bytes_received_rate", "rate" },
		func(q *AnalyticsQuery) { q.ComparisonRunID = "baseline" },
	}
	for i, edit := range accept {
		q := base
		edit(&q)
		if err := ValidateNativeArchiveQuery(q); err != nil {
			t.Fatalf("accept %d: %v", i, err)
		}
	}
	refuse := []func(*AnalyticsQuery){
		func(q *AnalyticsQuery) { q.Filters = map[string]string{"network_profile": "3g"} },
		func(q *AnalyticsQuery) { q.Filters = map[string]string{"protocol": "kafka"} },
		func(q *AnalyticsQuery) { q.Filters = map[string]string{"action": ""} },
		func(q *AnalyticsQuery) { q.Dimensions = []string{"action"} },
		func(q *AnalyticsQuery) { q.PropertyName, q.PropertyValue = "kind", "paid" },
		func(q *AnalyticsQuery) { q.Derivation = &analysis.QueryDerivation{Operation: "moving_mean"} },
		func(q *AnalyticsQuery) { q.Aggregation = "rate" },
		func(q *AnalyticsQuery) { q.Metric = "bytes_sent_rate" },
		func(q *AnalyticsQuery) { q.Metric = "response_time" },
		func(q *AnalyticsQuery) { q.EndSeconds = 100000 },
		func(q *AnalyticsQuery) { q.EvidenceMode = "archive" },
	}
	for i, edit := range refuse {
		q := base
		edit(&q)
		if err := ValidateNativeArchiveQuery(q); err == nil {
			t.Fatalf("refuse %d: accepted %+v", i, q)
		}
	}

	// Live byte evidence queries support sum and rate, and reject per-request size distributions.
	liveSum := AnalyticsQuery{QueryVersion: AnalyticsQueryVersion, EvidenceMode: "live", LiveBucketMode: "merged", Population: analysis.PopulationByte, Metric: "bytes_sent", Aggregation: "sum", EndSeconds: 60, BucketSeconds: 10, MaxSeries: 1}
	if err := ValidateNativeEvidenceQuery(liveSum); err != nil {
		t.Fatalf("expected live byte sum query to be valid, got %v", err)
	}
	liveRate := liveSum
	liveRate.Metric, liveRate.Aggregation = "bytes_sent_rate", "rate"
	liveRate.Filters = map[string]string{"protocol": "grpc"}
	if err := ValidateNativeEvidenceQuery(liveRate); err != nil {
		t.Fatalf("expected live byte rate query to be valid, got %v", err)
	}
	for _, badAgg := range []string{"p50", "p90", "p95", "p99", "avg", "min", "max"} {
		bad := liveSum
		bad.Aggregation = badAgg
		if err := ValidateNativeEvidenceQuery(bad); err == nil {
			t.Fatalf("expected live byte %s query to be rejected", badAgg)
		}
	}
}
