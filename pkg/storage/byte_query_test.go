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
}
