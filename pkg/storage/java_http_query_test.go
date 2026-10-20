package storage

import (
	"testing"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestJavaHTTPQueryClosedProfile(t *testing.T) {
	q := AnalyticsQuery{Population: analysis.PopulationJavaHTTPAction, Metric: "java_http_decoded_request_bytes", Aggregation: "avg", Filters: map[string]string{"action": "request"}, EndSeconds: 20, BucketSeconds: 10}
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*AnalyticsQuery){
		func(q *AnalyticsQuery) { q.Aggregation = "rate" }, func(q *AnalyticsQuery) { q.Population = "action" }, func(q *AnalyticsQuery) { q.Metric = "response_time" },
		func(q *AnalyticsQuery) { q.Filters = nil }, func(q *AnalyticsQuery) { q.EvidenceMode = "live" }, func(q *AnalyticsQuery) { q.Dimensions = []string{"action"} },
		func(q *AnalyticsQuery) { q.EndSeconds = 86401 }, func(q *AnalyticsQuery) { q.Metric = "java_http_prepared_per_decoded" },
	} {
		copy := q
		change(&copy)
		if err := ValidateNativeArchiveQuery(copy); err == nil {
			t.Fatal("unsupported query admitted", copy)
		}
	}
	q.Metric = "java_http_prepared_per_decoded"
	q.Aggregation = "ratio_of_sums"
	if err := ValidateNativeArchiveQuery(q); err != nil {
		t.Fatal(err)
	}
}
