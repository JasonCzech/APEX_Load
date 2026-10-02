package storage

import (
	"math"
	"testing"
)

func TestNativeObservationRates(t *testing.T) {
	for _, tc := range []struct {
		metric          string
		count, failures int64
		seconds         float64
		want            *float64
	}{
		{"observation_rate", 3, 1, 10, rateValue(.3)}, {"error_observation_rate", 3, 1, 10, rateValue(.1)}, {"success_observation_rate", 3, 1, 10, rateValue(.2)},
		{"error_observation_rate", 3, 0, 5, rateValue(0)}, {"observation_rate", 0, 0, 5, nil}, {"observation_rate", 3, 4, 5, nil}, {"observation_rate", 3, 1, 0, nil}, {"observation_rate", 3, 1, math.NaN(), nil}, {"response_time", 3, 1, 5, nil},
	} {
		got := NativeObservationRate(tc.metric, tc.count, tc.failures, tc.seconds)
		if (got == nil) != (tc.want == nil) || got != nil && *got != *tc.want {
			t.Fatalf("%+v got %v", tc, got)
		}
	}
	for _, metric := range []string{"observation_rate", "error_observation_rate", "success_observation_rate"} {
		q := AnalyticsQuery{Population: "action", Metric: metric, Aggregation: "rate", EndSeconds: 15, BucketSeconds: 10}
		if err := ValidateNativeArchiveQuery(q); err != nil {
			t.Fatal(err)
		}
		q.Aggregation = "avg"
		if ValidateNativeArchiveQuery(q) == nil {
			t.Fatal("invalid aggregation accepted")
		}
	}
}
func rateValue(v float64) *float64 { return &v }

func TestNativeRatesRequirePopulation(t *testing.T) {
	d := sampleDashboardDefinition("rates")
	d.Tabs[0].Panels[0].Query = &AnalyticsQuery{Metric: "observation_rate", Aggregation: "rate"}
	if validateDashboardDefinition(d) == nil {
		t.Fatal("retained count rate admitted as fleet totals")
	}
}
