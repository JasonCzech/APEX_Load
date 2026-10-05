package analysis

import (
	"math"
	"testing"
	"time"
)

// exchange simulates one request and response between a source whose clock
// differs from the controller's by skew, with separate one-way delays.
func exchange(skew, forward, back, processing time.Duration) (sourceSent, controllerReceived, controllerSent, sourceReceived time.Time) {
	controllerSendStart := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	sourceSent = controllerSendStart.Add(skew)
	controllerReceived = controllerSendStart.Add(forward)
	controllerSent = controllerReceived.Add(processing)
	sourceReceived = controllerSent.Add(back).Add(skew)
	return
}

func TestEstimateClockOffsetBoundsTheErrorUnderSkewAndAsymmetry(t *testing.T) {
	for _, tc := range []struct {
		name          string
		skew          time.Duration
		forward, back time.Duration
	}{
		{"source two seconds ahead, symmetric", 2 * time.Second, 20 * time.Millisecond, 20 * time.Millisecond},
		{"source two seconds behind, symmetric", -2 * time.Second, 20 * time.Millisecond, 20 * time.Millisecond},
		{"ahead, slow request path", 2 * time.Second, 300 * time.Millisecond, 10 * time.Millisecond},
		{"behind, slow response path", -2 * time.Second, 5 * time.Millisecond, 400 * time.Millisecond},
		{"in sync, one-sided delay", 0, 0, 250 * time.Millisecond},
	} {
		t.Run(tc.name, func(t *testing.T) {
			estimate, ok := EstimateClockOffset(exchange(tc.skew, tc.forward, tc.back, 7*time.Millisecond))
			if !ok {
				t.Fatal("estimate rejected")
			}
			if estimate.RoundTrip != tc.forward+tc.back {
				t.Fatalf("round trip %s, want %s (processing excluded)", estimate.RoundTrip, tc.forward+tc.back)
			}
			if estimate.Uncertainty != estimate.RoundTrip/2 {
				t.Fatalf("uncertainty %s, want half the round trip", estimate.Uncertainty)
			}
			// The true skew always lies within the reported uncertainty.
			if err := estimate.Offset - tc.skew; err < -estimate.Uncertainty || err > estimate.Uncertainty {
				t.Fatalf("offset %s misses the true skew %s by more than %s", estimate.Offset, tc.skew, estimate.Uncertainty)
			}
			if tc.forward == tc.back && estimate.Offset != tc.skew {
				t.Fatalf("symmetric delay should be exact: %s vs %s", estimate.Offset, tc.skew)
			}
		})
	}
	sent, received, replied, back := exchange(0, 10*time.Millisecond, 10*time.Millisecond, 5*time.Millisecond)
	if _, ok := EstimateClockOffset(sent, replied, received, back); ok {
		t.Fatal("a reply before the request was accepted")
	}
	if _, ok := EstimateClockOffset(back, received, replied, sent); ok {
		t.Fatal("a negative round trip was accepted")
	}
	if _, ok := EstimateClockOffset(exchange(48*time.Hour, 0, 0, 0)); ok {
		t.Fatal("an offset beyond a day was accepted")
	}
}

func TestPointTimingValidation(t *testing.T) {
	estimate, _ := EstimateClockOffset(exchange(-2*time.Second, 40*time.Millisecond, 10*time.Millisecond, 0))
	measured := MeasuredTiming(ClockSourceCollector, estimate)
	if err := measured.Validate(); err != nil {
		t.Fatal(err)
	}
	if *measured.ClockOffsetMs > -1975 || *measured.ClockOffsetMs < -2025 || *measured.ClockUncertaintyMs != 25 {
		t.Fatalf("measured timing %v ± %v", *measured.ClockOffsetMs, *measured.ClockUncertaintyMs)
	}
	stale := 90 * time.Second
	provider := ProviderTiming(60, &stale)
	if err := provider.Validate(); err != nil || provider.OffsetStatus != OffsetUnknown || *provider.ClockUncertaintyMs != 30000 || *provider.FreshnessSeconds != 90 {
		t.Fatalf("provider timing %+v %v", provider, err)
	}
	nan, offset := math.NaN(), 5.0
	local := time.Now()
	for name, bad := range map[string]PointTiming{
		"unknown source":           {ClockSource: "sundial", OffsetStatus: OffsetUnknown},
		"unknown status":           {ClockSource: ClockSourceProvider, OffsetStatus: "guessed"},
		"measured without value":   {ClockSource: ClockSourceCollector, OffsetStatus: OffsetMeasured},
		"unknown with an offset":   {ClockSource: ClockSourceProvider, OffsetStatus: OffsetUnknown, ClockOffsetMs: &offset},
		"non-finite uncertainty":   {ClockSource: ClockSourceProvider, OffsetStatus: OffsetUnknown, ClockUncertaintyMs: &nan},
		"resolution beyond a day":  {ClockSource: ClockSourceProvider, OffsetStatus: OffsetUnknown, ResolutionSeconds: 86401},
		"local receive time":       {ClockSource: ClockSourceCollector, OffsetStatus: OffsetUnknown, ReceivedAt: &local},
		"non-finite freshness age": {ClockSource: ClockSourceProvider, OffsetStatus: OffsetUnknown, FreshnessSeconds: &nan},
	} {
		if bad.Validate() == nil {
			t.Fatalf("%s: accepted", name)
		}
	}
	value := 1.0
	point := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "s", MetricName: "m", Kind: KindGauge, Value: &value, Quality: QualityValid, TimestampUTC: time.Now().UTC(), Timing: &PointTiming{ClockSource: "sundial"}}
	if point.Validate() == nil {
		t.Fatal("a point carried invalid timing")
	}
}
