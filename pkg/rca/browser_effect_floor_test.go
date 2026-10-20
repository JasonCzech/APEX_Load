package rca

import (
	"fmt"
	"math"
	"testing"
)

func TestBrowserEffectFloorOnlyRaisesTimingAndCLSDefaults(t *testing.T) {
	for _, tc := range []struct {
		name, metric, unit     string
		mean, configured, want float64
	}{
		{"timing absolute minimum", "lcp", "ms", 100, 1, 50},
		{"timing relative minimum", "lcp", "ms", 1000, 1, 100},
		{"raised metric floor", "lcp", "ms", 1000, 250, 250},
		{"layout minimum", "cls", "score", 0.1, 0.001, 0.02},
		{"raised layout floor", "cls", "score", 0.1, 0.1, 0.1},
		{"other metric needs catalog floor", "custom", "count", 100, 2, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			params := DefaultBrowserBaselineParams
			params.MetricFloors = map[string]float64{tc.metric: tc.configured}
			got, ok := browserEffectFloor(tc.metric, tc.unit, tc.mean, params)
			if !ok || !browserNear(got, tc.want) {
				t.Fatalf("floor=%v supported=%v want=%v", got, ok, tc.want)
			}
		})
	}
}

func TestBrowserEffectFloorRejectsInvalidConfiguredValues(t *testing.T) {
	for _, metric := range []string{"lcp", "cls", "other"} {
		for _, value := range []float64{0, -1, math.NaN(), math.Inf(1), math.Inf(-1)} {
			params := DefaultBrowserBaselineParams
			params.MetricFloors = map[string]float64{metric: value}
			if got, ok := browserEffectFloor(metric, "ms", 1000, params); ok {
				t.Fatalf("%s %v admitted floor %v", metric, value, got)
			}
		}
	}
	if _, ok := browserEffectFloor("other", "count", 100, DefaultBrowserBaselineParams); ok {
		t.Fatal("unknown metric scored without a floor")
	}
}

func TestBrowserBaselineMetricFloorCannotCreateSmallEffectAnomaly(t *testing.T) {
	baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 1), 1)
	current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, 1030, 0), 1)
	params := DefaultBrowserBaselineParams
	params.MetricFloors = map[string]float64{"lcp": 1}
	got := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), params).Results[0]
	if got.State != BrowserBaselineWithinRange || !browserNear(got.EffectFloor, 100) || math.Abs(got.Z) < 3 {
		t.Fatalf("small effect: %+v", got)
	}
}

func TestBrowserEffectFloorPreservesRaisedGlobalFloors(t *testing.T) {
	params := DefaultBrowserBaselineParams
	params.TimingFloorFraction = 0.2
	params.TimingFloorMs = 200
	params.MetricFloors = map[string]float64{"lcp": 150}
	if got, ok := browserEffectFloor("lcp", "ms", 2000, params); !ok || got != 400 {
		t.Fatalf("raised global floor: %v %v", got, ok)
	}
}

func TestBrowserEffectFloorCannotQualifyNonfiniteFloor(t *testing.T) {
	for _, value := range []float64{math.NaN(), math.Inf(1), math.Inf(-1)} {
		params := DefaultBrowserBaselineParams
		params.TimingFloorMs = value
		if _, ok := browserEffectFloor("lcp", "ms", 1000, params); ok {
			t.Fatal("nonfinite absolute floor")
		}
		params = DefaultBrowserBaselineParams
		params.TimingFloorFraction = value
		if _, ok := browserEffectFloor("lcp", "ms", 1000, params); ok {
			t.Fatal("nonfinite relative floor")
		}
		params = DefaultBrowserBaselineParams
		params.CLSFloor = value
		if _, ok := browserEffectFloor("cls", "score", 0.1, params); ok {
			t.Fatal("nonfinite CLS floor")
		}
	}
	params := DefaultBrowserBaselineParams
	params.TimingFloorFraction = math.MaxFloat64
	if _, ok := browserEffectFloor("lcp", "ms", 1000, params); ok {
		t.Fatal("overflow floor")
	}
}

// A large observed delta must not turn a known incomplete population into
// a scored regression; a small delta must not claim a healthy comparison.
func TestBrowserBaselineRefusesKnownSourceLoss(t *testing.T) {
	for _, tc := range []struct {
		name              string
		current, baseline bool
		reason            string
	}{
		{"current", true, false, BrowserBaselineSourceLoss},
		{"baseline", false, true, BrowserBaselineBaselineSourceLoss},
		{"both", true, true, BrowserBaselineSourceLoss},
	} {
		for _, value := range []float64{1000, 1500} {
			t.Run(tc.name+fmt.Sprint(value), func(t *testing.T) {
				baseline := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(40, 1000, 20), 1)
				current := documentSeries("lcp", "lcp", "ms", "f", chromium141, browserNoisy(10, value, 0), 1)
				baseline.Document.Incomplete, current.Document.Incomplete = tc.baseline, tc.current
				out := DetectBrowserBaseline(browserBaselineInput([]Series{current}, []Series{baseline}, BaselineQualified), DefaultBrowserBaselineParams)
				got := out.Results[0]
				if out.Status != DetectionPartial || got.State != BrowserBaselineUnscored || got.Reason != tc.reason {
					t.Fatalf("known source loss qualified: %+v %+v", out, got)
				}
				if got.CurrentBuckets != 10 || got.BaselineBuckets != 40 || got.CurrentSamples != 10 || got.BaselineSamples != 40 {
					t.Fatalf("retained population lost: %+v", got)
				}
				if got.ExpectedHigh != 0 || got.ExpectedLow != 0 || got.Effect != 0 || got.EffectFloor != 0 || got.Z != 0 || got.Direction != "" || len(got.Evidence) != 1 || got.Baseline.TestID == "" {
					t.Fatalf("unscored comparison leaked a verdict or lost its baseline identity: %+v", got)
				}
			})
		}
	}
}
