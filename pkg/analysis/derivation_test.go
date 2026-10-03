package analysis

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestBaselineRejectsDifferentBucketStatisticsAndNestedParents(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := qualifiedPoint(start, 1)
	p.Kind = KindObservation
	q := p
	v := 101.0
	q.Value = &v
	averages, err := BucketSeries([]MetricPoint{p, q}, start, start.Add(time.Second), 1, "avg")
	if err != nil {
		t.Fatal(err)
	}
	percentiles, err := BucketSeries([]MetricPoint{p, q}, start, start.Add(time.Second), 1, "p95")
	if err != nil {
		t.Fatal(err)
	}
	if err := CompatibleMeasurements(semanticsOf(averages[0]), semanticsOf(percentiles[0])); err == nil {
		t.Fatal("avg compared to p95")
	}
	got, err := EvaluateBaselineDelta(averages[0], percentiles[0], false)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "incompatible_measurement", "ms")
	left, err := BucketSeries(averages, start, start.Add(2*time.Second), 2, "avg")
	if err != nil {
		t.Fatal(err)
	}
	right, err := BucketSeries(percentiles, start, start.Add(2*time.Second), 2, "avg")
	if err != nil {
		t.Fatal(err)
	}
	if len(left[0].Derivation) != 2 || left[0].Derivation[1] != right[0].Derivation[1] {
		t.Fatalf("nested final operations: %+v %+v", left[0].Derivation, right[0].Derivation)
	}
	got, err = EvaluateBaselineDelta(left[0], right[0], false)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "incompatible_measurement", "ms")
	baseline := left[0]
	baseline.RunID = "previous-run"
	got, err = EvaluateBaselineDelta(left[0], baseline, false)
	if err != nil {
		t.Fatal(err)
	}
	zero := 0.0
	assertDerived(t, got, &zero, "", "ms")
}

func TestLegacyDerivedInputsStayReadableButUnqualifiedAcrossReaggregation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	legacy := qualifiedPoint(start, 5)
	legacy.InputKind = KindObservation
	if err := legacy.Validate(); err != nil {
		t.Fatal("legacy archive became unreadable", err)
	}
	got, err := EvaluateBaselineDelta(legacy, legacy, false)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "unknown_measurement_semantics", "ms")
	newer, err := BucketSeries([]MetricPoint{legacy}, start, start.Add(time.Second), 1, "avg")
	if err != nil {
		t.Fatal(err)
	}
	if len(newer[0].Derivation) != 2 || newer[0].Derivation[0].Operation != legacyDerivedOperation {
		t.Fatalf("legacy history upgraded into known raw data: %+v", newer[0])
	}
	got, err = EvaluateBaselineDelta(newer[0], newer[0], false)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "unknown_measurement_semantics", "ms")
}

func TestDerivationBoundsAndIndependentCopies(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := qualifiedPoint(start, 3)
	p.Derivation = []DerivationStep{{Operation: "avg", Algorithm: "scalar_bucket.v1", BucketSeconds: 1}}
	p.InputKind = KindObservation
	before := append([]DerivationStep(nil), p.Derivation...)
	got, err := BucketSeries([]MetricPoint{p}, start, start.Add(2*time.Second), 1, "avg")
	if err != nil {
		t.Fatal(err)
	}
	got[0].Derivation[0].Operation = "max"
	if !reflect.DeepEqual(p.Derivation, before) || got[1].Derivation[0].Operation != "avg" {
		t.Fatal("lineage aliases an input or another output")
	}
	for _, edit := range []func(*MetricPoint){
		func(p *MetricPoint) { p.Derivation[0].Operation = "SQL" },
		func(p *MetricPoint) { p.InputKind = "" },
		func(p *MetricPoint) { p.Kind = KindObservation; p.InputKind = "" },
		func(p *MetricPoint) { p.Derivation[0].Algorithm = strings.Repeat("x", maxMetricIdentityBytes+1) },
		func(p *MetricPoint) { p.Derivation[0].BucketSeconds = 86401 },
		func(p *MetricPoint) { p.Derivation = make([]DerivationStep, MaxDerivationSteps+1) },
	} {
		bad := p
		bad.Derivation = append([]DerivationStep(nil), p.Derivation...)
		edit(&bad)
		if err := bad.Validate(); err == nil {
			t.Fatal("unbounded or arbitrary derivation accepted")
		}
	}
	p.Derivation = nil
	for i := 0; i < MaxDerivationSteps; i++ {
		p.Derivation = append(p.Derivation, DerivationStep{Operation: "avg", Algorithm: "scalar_bucket.v1", BucketSeconds: 1})
	}
	if _, err := BucketSeries([]MetricPoint{p}, start, start.Add(time.Second), 1, "avg"); err == nil {
		t.Fatal("unbounded derivation chain accepted")
	}
}

func TestRawCounterRateRemainsUnqualifiedAcrossHigherRestartAndNestedBuckets(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points := []MetricPoint{qualifiedPoint(start, 10), qualifiedPoint(start.Add(time.Second), 100), qualifiedPoint(start.Add(2*time.Second), 105)}
	for i := range points {
		points[i].Kind, points[i].Unit = KindCounter, "count"
		points[i].Population, points[i].MetricVersion = PopulationInfrastructure, "requests.v1"
	}
	// A restarted counter can exceed its predecessor. Legacy rate display is
	// retained, but population/version declarations cannot supply a missing epoch.
	rates, err := BucketSeries(points, start, start.Add(3*time.Second), 1, "rate")
	if err != nil || len(rates) != 3 || rates[1].Value == nil || *rates[1].Value != 90 {
		t.Fatalf("legacy raw rate changed: %+v error=%v", rates, err)
	}
	for _, point := range rates {
		if len(point.Derivation) != 2 || point.Derivation[0] != (DerivationStep{Operation: legacyDerivedOperation, Algorithm: "unknown_counter_epoch"}) {
			t.Fatalf("raw counter epoch implicitly qualified: %+v", point.Derivation)
		}
		if err := point.Validate(); err != nil {
			t.Fatalf("legacy raw rate became unreadable: %v", err)
		}
	}
	for _, series := range [][]MetricPoint{rates, mustRebucketRawRates(t, rates, start)} {
		got, err := EvaluateBaselineDelta(series[1], series[1], false)
		if err != nil {
			t.Fatal(err)
		}
		assertDerived(t, got, nil, "unknown_measurement_semantics", "count/s")
		if err := CompatibleMeasurements(semanticsOf(series[1]), semanticsOf(series[1])); err == nil {
			t.Fatal("epochless raw rate qualified for an overlay")
		}
		if _, err := MovingStatistics(series, 1, 2, .5); err == nil {
			t.Fatal("epochless raw rate qualified for moving statistics")
		}
	}
	got, err := EvaluateCounter("rate", CounterSample{points[0], "before-restart", true}, CounterSample{points[1], "after-restart", true})
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "counter_epoch_changed_or_unknown", "count/s")
}

func mustRebucketRawRates(t *testing.T, points []MetricPoint, start time.Time) []MetricPoint {
	t.Helper()
	got, err := BucketSeries(points, start, start.Add(3*time.Second), 1, "avg")
	if err != nil || len(got) != 3 || len(got[1].Derivation) != 3 {
		t.Fatalf("nested raw rate=%+v error=%v", got, err)
	}
	return got
}
