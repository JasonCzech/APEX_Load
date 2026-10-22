package rca

import (
	"math"
	"testing"
)

func TestBrowserPinnedMetricFloorsAreIndependentAndCannotLowerGlobals(t *testing.T) {
	ms, relative := 200.0, 0.1
	policy := &BrowserBaselinePolicy{TimingMs: &ms, TimingRelative: &relative, MetricFloors: map[string]float64{"lcp": 350, "fcp": 50}}
	params := BrowserBaselineParamsFor(policy)
	policy.MetricFloors["lcp"] = 999
	for _, tc := range []struct {
		metric     string
		mean, want float64
	}{{"lcp", 1000, 350}, {"fcp", 1000, 200}, {"inp", 1000, 200}, {"lcp", 5000, 500}} {
		if value, ok := browserEffectFloor(tc.metric, "ms", tc.mean, params); !ok || value != tc.want {
			t.Fatalf("%s mean=%v: %v %t want=%v", tc.metric, tc.mean, value, ok, tc.want)
		}
	}
	if len(DefaultBrowserBaselineParams.MetricFloors) != 0 {
		t.Fatal("policy mutated default metric floors")
	}
	for _, value := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		params = BrowserBaselineParamsFor(&BrowserBaselinePolicy{MetricFloors: map[string]float64{"lcp": value}})
		if _, ok := browserEffectFloor("lcp", "ms", 1000, params); ok {
			t.Fatal("invalid pinned metric floor qualified", value)
		}
	}
	params = BrowserBaselineParamsFor(&BrowserBaselinePolicy{Unavailable: "missing revision", MetricFloors: map[string]float64{"lcp": 999}})
	if value, ok := browserEffectFloor("lcp", "ms", 1000, params); !ok || value != 100 {
		t.Fatal("unavailable policy affected defaults", value, ok)
	}
}
