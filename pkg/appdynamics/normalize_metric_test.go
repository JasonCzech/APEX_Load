package appdynamics

import (
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNormalizeMetricPrimaryStatistic(t *testing.T) {
	ptr := func(value float64) *float64 { return &value }
	now := time.Now().UTC()
	for _, tc := range []struct {
		name, selected, path, statistic string
		value                           metricValue
		want                            *float64
		eligible                        bool
	}{
		{name: "explicit sum uses sum", selected: "sum", statistic: "sum", value: metricValue{Value: ptr(42), Sum: ptr(120)}, want: ptr(120), eligible: true},
		{name: "explicit sum preserves zero without observations", selected: "sum", statistic: "sum", value: metricValue{Value: ptr(42), Sum: ptr(0), Count: ptr(0)}, want: ptr(0), eligible: true},
		{name: "explicit sum absent", selected: "sum", statistic: "sum", value: metricValue{Value: ptr(42)}},
		{name: "explicit sum NaN", selected: "sum", statistic: "sum", value: metricValue{Value: ptr(42), Sum: ptr(math.NaN())}},
		{name: "explicit sum infinite", selected: "sum", statistic: "sum", value: metricValue{Value: ptr(42), Sum: ptr(math.Inf(1))}},
		{name: "default keeps provider value", statistic: "value", value: metricValue{Value: ptr(42), Sum: ptr(120)}, want: ptr(42), eligible: true},
		{name: "explicit value keeps provider value", selected: "value", statistic: "value", value: metricValue{Value: ptr(42), Sum: ptr(120)}, want: ptr(42), eligible: true},
		{name: "current uses current", selected: "current", statistic: "current", value: metricValue{Value: ptr(42), Current: ptr(7), Sum: ptr(120)}, want: ptr(7), eligible: true},
		{name: "current does not require value", selected: "current", statistic: "current", value: metricValue{Current: ptr(7)}, want: ptr(7), eligible: true},
		{name: "current missing", selected: "current", statistic: "current", value: metricValue{Value: ptr(42)}},
		{name: "current infinite", selected: "current", statistic: "current", value: metricValue{Value: ptr(42), Current: ptr(math.Inf(-1))}},
		{name: "inferred count prefers sum", path: "Custom Metrics|Request Count", statistic: "sum", value: metricValue{Value: ptr(42), Sum: ptr(120)}, want: ptr(120), eligible: true},
		{name: "inferred count allows legacy value", path: "Custom Metrics|Request Count", statistic: "sum", value: metricValue{Value: ptr(42)}, want: ptr(42), eligible: true},
		{name: "inferred count ignores invalid sum", path: "Custom Metrics|Request Count", statistic: "sum", value: metricValue{Value: ptr(42), Sum: ptr(math.NaN())}, want: ptr(42), eligible: true},
		{name: "inferred count preserves sum zero", path: "Custom Metrics|Request Count", statistic: "sum", value: metricValue{Value: ptr(42), Sum: ptr(0), Count: ptr(0)}, want: ptr(0), eligible: true},
		{name: "average preserves provider rounding", selected: "average", statistic: "average", value: metricValue{Value: ptr(12.35), Sum: ptr(37.04), Count: ptr(3)}, want: ptr(12.35), eligible: true},
		{name: "percentile remains ineligible", selected: "p95", statistic: "p95", value: metricValue{Value: ptr(12.35), Sum: ptr(37.04), Count: ptr(3)}, want: ptr(12.35)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := tc.path
			if path == "" {
				path = "Custom Metrics|Metric"
			}
			tc.value.Start = now.Add(-5 * time.Minute).UnixMilli()
			point, err := NormalizeMetric(MetricSelection{ApplicationID: "1", Name: "metric", Statistic: tc.selected}, metricResponse{ID: "7", Path: path, Frequency: "ONE_MIN"}, tc.value, "project", "run", now, 120)
			if err != nil {
				t.Fatal(err)
			}
			quality := analysis.QualityValid
			if tc.want == nil {
				quality = analysis.QualityMissing
			}
			if (point.Value == nil) != (tc.want == nil) || point.Value != nil && *point.Value != *tc.want || point.Quality != quality {
				t.Fatalf("value=%v quality=%s; want value=%v quality=%s", point.Value, point.Quality, tc.want, quality)
			}
			if point.Provider.Statistic != tc.statistic || point.Dimensions["statistic"] != tc.statistic || point.Provider.AggregationEligible != tc.eligible {
				t.Fatalf("inconsistent statistic or eligibility: %+v", point.Provider)
			}
			if !finite(tc.value.Sum) && point.Provider.Sum != nil {
				t.Fatalf("retained invalid auxiliary sum: %v", point.Provider.Sum)
			}
		})
	}
}
