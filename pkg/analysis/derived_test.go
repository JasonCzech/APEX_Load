package analysis

import (
	"math"
	"testing"
	"time"
)

func assertDerived(t *testing.T, got DerivedValue, value *float64, reason, unit string) {
	t.Helper()
	if got.Unit != unit || got.Reason != reason || (value == nil) != (got.Value == nil) {
		t.Fatalf("derived=%+v want value=%v reason=%s unit=%s", got, value, reason, unit)
	}
	if value != nil && (got.Quality != QualityValid || math.Abs(*got.Value-*value) > 1e-10) {
		t.Fatalf("derived value=%v want=%v", got.Value, value)
	}
	if value == nil && got.Quality != QualityMissing {
		t.Fatalf("unavailable value has quality %s", got.Quality)
	}
}

func TestCountDerivationsPreservePopulationAndUnits(t *testing.T) {
	base := CountEvidence{Count: 10, Failures: 2, WindowSeconds: 4, Quality: QualityValid, Complete: true}
	for _, test := range []struct {
		metric, unit string
		value        float64
	}{
		{"observation_count", "count", 10}, {"error_rate", "percent", 20}, {"observation_rate", "count/s", 2.5},
		{"error_observation_rate", "count/s", .5}, {"success_observation_rate", "count/s", 2},
	} {
		got, err := EvaluateCountMetric(test.metric, PopulationAction, base)
		if err != nil {
			t.Fatal(err)
		}
		assertDerived(t, got, &test.value, "", test.unit)
	}
	zero := base
	zero.Failures = 0
	got, err := EvaluateCountMetric("error_observation_rate", PopulationTransaction, zero)
	if err != nil {
		t.Fatal(err)
	}
	v := 0.0
	assertDerived(t, got, &v, "", "count/s")
	for _, test := range []struct {
		name, reason string
		edit         func(*CountEvidence)
	}{
		{"missing", "incomplete_evidence", func(e *CountEvidence) { e.Quality = QualityMissing }},
		{"incomplete", "incomplete_evidence", func(e *CountEvidence) { e.Complete = false }},

		{"overflow", "invalid_population_counts", func(e *CountEvidence) { e.Count = maxExactCount + 1 }},
		{"negative", "invalid_population_counts", func(e *CountEvidence) { e.Failures = -1 }},
		{"not subset", "invalid_population_counts", func(e *CountEvidence) { e.Failures = e.Count + 1 }},
		{"zero duration", "invalid_elapsed_window", func(e *CountEvidence) { e.WindowSeconds = 0 }},
		{"infinite duration", "invalid_elapsed_window", func(e *CountEvidence) { e.WindowSeconds = math.Inf(1) }},
		{"nan duration", "invalid_elapsed_window", func(e *CountEvidence) { e.WindowSeconds = math.NaN() }},
		{"overflow rate", "numeric_range", func(e *CountEvidence) { e.WindowSeconds = math.SmallestNonzeroFloat64 }},
	} {
		t.Run(test.name, func(t *testing.T) {
			e := base
			test.edit(&e)
			got, err := EvaluateCountMetric("observation_rate", PopulationAction, e)
			if err != nil {
				t.Fatal(err)
			}
			assertDerived(t, got, nil, test.reason, "count/s")
		})
	}
	empty := base
	empty.Count, empty.Failures = 0, 0
	for _, metric := range []string{"observation_count", "observation_rate", "error_observation_rate", "success_observation_rate"} {
		got, err := EvaluateCountMetric(metric, PopulationAction, empty)
		if err != nil {
			t.Fatal(err)
		}
		unit := "count/s"
		if metric == "observation_count" {
			unit = "count"
		}
		assertDerived(t, got, &v, "", unit)
	}
	got, err = EvaluateCountMetric("error_rate", PopulationAction, empty)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "empty_population", "percent")
	for _, population := range []string{"", PopulationMessage, PopulationByte} {
		if _, err := EvaluateCountMetric("observation_rate", population, base); err == nil {
			t.Fatal("mixed observation population accepted")
		}
	}
}

func TestTypedRatiosRequireEligibleSubsetAndScope(t *testing.T) {
	base := RatioCount{Count: 10, Population: PopulationMessage, Scope: "run/source/window/received", MetricVersion: "messaging.v1", Quality: QualityValid, Complete: true}
	numerator := base
	numerator.Count = 2
	for _, operation := range []string{"acknowledgement_ratio", "settlement_ratio", "duplicate_ratio", "redelivery_ratio", "dead_letter_ratio"} {
		got, err := EvaluateRatio(operation, numerator, base)
		if err != nil {
			t.Fatal(err)
		}
		v := .2
		assertDerived(t, got, &v, "", "ratio")
	}
	for _, test := range []struct {
		reason string
		edit   func(*RatioCount)
	}{
		{"incomplete_evidence", func(e *RatioCount) { e.Complete = false }},
		{"unknown_population_scope", func(e *RatioCount) { e.Scope = "" }},
		{"incompatible_population_scope", func(e *RatioCount) { e.Scope = "another" }},
		{"incompatible_population_scope", func(e *RatioCount) { e.Population = PopulationByte }},
		{"incompatible_population_scope", func(e *RatioCount) { e.MetricVersion = "messaging.v2" }},
		{"numerator_not_subset", func(e *RatioCount) { e.Count = 11 }},
		{"invalid_population_counts", func(e *RatioCount) { e.Count = maxExactCount + 1 }},
	} {
		n := numerator
		test.edit(&n)
		got, err := EvaluateRatio("duplicate_ratio", n, base)
		if err != nil {
			t.Fatal(err)
		}
		assertDerived(t, got, nil, test.reason, "ratio")
	}
	base.Count = 0
	numerator.Count = 0
	got, err := EvaluateRatio("acknowledgement_ratio", numerator, base)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "zero_denominator", "ratio")
	if _, err := EvaluateRatio("error_ratio", numerator, base); err == nil {
		t.Fatal("message/action ratio accepted")
	}
	if _, err := EvaluateRatio("arbitrary_formula", numerator, base); err == nil {
		t.Fatal("arbitrary ratio accepted")
	}
}

func qualifiedPoint(at time.Time, value float64) MetricPoint {
	p := point(at, value)
	p.Unit = "ms"
	p.Population = PopulationAction
	p.MetricVersion = "response_time.v1"
	return p
}

func TestCounterDerivationsExcludeResetsEpochsMissingAndOverflow(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	p := qualifiedPoint(start, 10)
	p.Kind = KindCounter
	p.Unit = "count"
	q := p
	q.TimestampUTC = start.Add(2 * time.Second)
	v := 14.0
	q.Value = &v
	before, after := CounterSample{p, "epoch-1", true}, CounterSample{q, "epoch-1", true}
	for _, test := range []struct {
		operation, unit string
		value           float64
	}{{"delta", "count", 4}, {"rate", "count/s", 2}} {
		got, err := EvaluateCounter(test.operation, before, after)
		if err != nil {
			t.Fatal(err)
		}
		assertDerived(t, got, &test.value, "", test.unit)
	}
	for _, test := range []struct {
		reason string
		edit   func(*CounterSample)
	}{
		{"counter_reset", func(s *CounterSample) { v := 2.0; s.Point.Value = &v }},
		{"counter_epoch_changed_or_unknown", func(s *CounterSample) { s.Epoch = "epoch-2" }},
		{"incomplete_evidence", func(s *CounterSample) { s.Complete = false }},
		{"incomplete_evidence", func(s *CounterSample) { s.Point.Quality = QualityMissing; s.Point.Value = nil }},
		{"invalid_elapsed_window", func(s *CounterSample) { s.Point.TimestampUTC = before.Point.TimestampUTC }},
		{"incompatible_counter_series", func(s *CounterSample) { s.Point.Population = PopulationByte }},
		{"invalid_counter_range", func(s *CounterSample) { v := float64(maxExactCount) + 1; s.Point.Value = &v }},
	} {
		a := after
		test.edit(&a)
		got, err := EvaluateCounter("rate", before, a)
		if err != nil {
			t.Fatal(err)
		}
		assertDerived(t, got, nil, test.reason, "count/s")
	}
	fraction := after
	fractionalValue := 14.5
	fraction.Point.Value = &fractionalValue
	got, err := EvaluateCounter("rate", before, fraction)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "nonintegral_counter", "count/s")
	unsupportedBefore, unsupportedAfter := before, after
	unsupportedBefore.Point.Unit, unsupportedAfter.Point.Unit = "ratio", "ratio"
	got, err = EvaluateCounter("rate", unsupportedBefore, unsupportedAfter)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "unsupported_counter_rate_unit", "ratio")
	legacy := before
	legacy.Point.Population = ""
	legacy.Point.MetricVersion = ""
	legacyAfter := after
	legacyAfter.Point.Population = ""
	legacyAfter.Point.MetricVersion = ""
	got, err = EvaluateCounter("delta", legacy, legacyAfter)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "unknown_measurement_semantics", "count")
}

func TestBaselineDeltasRequireComparableExplicitSemantics(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	base, current := qualifiedPoint(start, 20), qualifiedPoint(start.Add(time.Second), 25)
	base.RunID = "baseline"
	for _, test := range []struct {
		percent bool
		value   float64
		unit    string
	}{{false, 5, "ms"}, {true, 25, "percent"}} {
		got, err := EvaluateBaselineDelta(current, base, test.percent)
		if err != nil {
			t.Fatal(err)
		}
		assertDerived(t, got, &test.value, "", test.unit)
	}
	for _, edit := range []func(*MetricPoint){func(p *MetricPoint) { p.Population = PopulationByte }, func(p *MetricPoint) { p.MetricVersion = "response_time.v2" }, func(p *MetricPoint) { p.Unit = "bytes" }, func(p *MetricPoint) { p.InputKind = KindObservation }} {
		other := base
		edit(&other)
		got, err := EvaluateBaselineDelta(current, other, false)
		if err != nil {
			t.Fatal(err)
		}
		assertDerived(t, got, nil, "incompatible_measurement", "ms")
	}
	v := 0.0
	base.Value = &v
	got, err := EvaluateBaselineDelta(current, base, true)
	if err != nil {
		t.Fatal(err)
	}
	assertDerived(t, got, nil, "zero_denominator", "percent")
	v = -20
	base.Value = &v
	c := -10.0
	current.Value = &c
	got, err = EvaluateBaselineDelta(current, base, true)
	if err != nil {
		t.Fatal(err)
	}
	want := 50.0
	assertDerived(t, got, &want, "", "percent")
}

func TestMovingStatisticsUseElapsedBucketsAndCoverage(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points := []MetricPoint{qualifiedPoint(start, 1), qualifiedPoint(start.Add(time.Second), 2), qualifiedPoint(start.Add(3*time.Second), 4), qualifiedPoint(start.Add(4*time.Second), 5)}
	original := points[0]
	got, err := MovingStatistics(points, 1, 3, 2.0/3)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[0].Reason != "insufficient_window" || got[2].ValidBuckets != 2 || got[2].Mean == nil || *got[2].Mean != 1.5 || got[2].StdDev == nil || math.Abs(*got[2].StdDev-.5) > 1e-12 || got[3].Mean == nil || *got[3].Mean != 3 || got[4].Mean == nil || *got[4].Mean != 4.5 {
		t.Fatalf("moving=%+v", got)
	}
	if points[0].TimestampUTC != original.TimestampUTC || points[0].Value != original.Value {
		t.Fatal("moving statistics mutated input")
	}
	strict, err := MovingStatistics(points, 1, 3, 1)
	if err != nil {
		t.Fatal(err)
	}
	if strict[2].Mean != nil || strict[2].Reason != "insufficient_coverage" {
		t.Fatalf("strict coverage=%+v", strict[2])
	}
	points[1].Quality = QualityStale
	got, err = MovingStatistics(points, 1, 3, .5)
	if err != nil {
		t.Fatal(err)
	}
	if got[2].Mean != nil || got[2].Reason != "insufficient_valid_buckets" {
		t.Fatalf("stale bucket qualified: %+v", got[2])
	}
}

func TestMovingStatisticsBoundsKindsAndNumericStability(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, scale := range []float64{1, 1e150, 1e-200, math.MaxFloat64 / 4} {
		points := []MetricPoint{qualifiedPoint(start, scale), qualifiedPoint(start.Add(time.Second), 2*scale), qualifiedPoint(start.Add(2*time.Second), 3*scale)}
		got, err := MovingStatistics(points, 1, 3, 1)
		if err != nil {
			t.Fatal(err)
		}
		if got[2].Mean == nil || math.Abs(*got[2].Mean/(2*scale)-1) > 1e-12 || got[2].StdDev == nil || math.Abs(*got[2].StdDev/(math.Sqrt(2.0/3)*scale)-1) > 1e-12 {
			t.Fatalf("scale %g moving=%+v", scale, got[2])
		}
	}
	for _, test := range []struct {
		bucket, window int
		coverage       float64
	}{{0, 3, 1}, {1, 1, 1}, {1, MaxMovingWindowBuckets + 1, 1}, {1, 3, 0}, {1, 3, math.NaN()}} {
		if _, err := MovingStatistics(nil, test.bucket, test.window, test.coverage); err == nil {
			t.Fatal("invalid moving limits accepted")
		}
	}
	for _, edit := range []func(*MetricPoint){func(p *MetricPoint) { p.Kind = KindSummary }, func(p *MetricPoint) { p.Population = "" }, func(p *MetricPoint) { p.MetricVersion = "" }, func(p *MetricPoint) { p.TimestampUTC = p.TimestampUTC.Add(500 * time.Millisecond) }} {
		points := []MetricPoint{qualifiedPoint(start, 1), qualifiedPoint(start.Add(time.Second), 2)}
		edit(&points[1])
		if _, err := MovingStatistics(points, 1, 3, 1); err == nil {
			t.Fatal("unqualified moving input accepted")
		}
	}
	points := []MetricPoint{qualifiedPoint(start, 1), qualifiedPoint(start.Add(MaxSeriesPoints*time.Second), 2)}
	if _, err := MovingStatistics(points, 1, 3, 1); err == nil {
		t.Fatal("excessive sparse moving window accepted")
	}
}
