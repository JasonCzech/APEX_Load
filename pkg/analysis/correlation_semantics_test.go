package analysis

import (
	"math"
	"testing"
	"time"
)

func seriesPoint(at time.Time, metric, unit, kind, inputKind string, value float64) MetricPoint {
	return MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "project", Source: "collector", TimestampUTC: at, MetricName: metric, Unit: unit, Kind: kind, InputKind: inputKind, Value: &value, Quality: QualityValid}
}

func bestLag(scores []CorrelationScore) (int, float64) {
	lag, best := 0, -2.0
	for _, score := range scores {
		if score.Coefficient != nil && *score.Coefficient > best {
			lag, best = score.LagSeconds, *score.Coefficient
		}
	}
	return lag, best
}

// CPU percent (a gauge) associates with transaction latency in ms (a
// summary's declared p95): both keep their own semantics, they are never
// one unit, and a positive lag means the right series follows the left.
func TestCorrelationKeepsEachSeriesSemantics(t *testing.T) {
	from := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	var cpu, latency []MetricPoint
	for i := 0; i < 60; i++ {
		at := from.Add(time.Duration(i) * 10 * time.Second)
		load := math.Sin(float64(i) / 4)
		cpu = append(cpu, seriesPoint(at, "cpu", "percent", KindGauge, KindGauge, 50+20*load))
		// Latency follows CPU by two buckets (20 seconds).
		lagged := math.Sin(float64(i-2) / 4)
		p := seriesPoint(at, "transaction.duration", "ms", KindGauge, KindSummary, 200+80*lagged)
		p.Population = "transaction"
		latency = append(latency, p)
	}
	to := from.Add(600 * time.Second)
	scores, err := LaggedCorrelation(cpu, latency, from, to, 10, 60)
	if err != nil {
		t.Fatal(err)
	}
	if lag, coefficient := bestLag(scores); lag != 20 || coefficient < 0.99 {
		t.Fatalf("best lag %d (%.3f), want +20: the right series follows the left", lag, coefficient)
	}
	semantics := DescribeCorrelation("cpu", "avg", cpu, "transaction.duration", "p95", latency)
	if semantics.SameUnits || semantics.LagConvention != CorrelationLagConvention {
		t.Fatalf("semantics %+v", semantics)
	}
	if left := semantics.Left; left.Unit != "percent" || left.Kind != KindGauge || left.Statistic != "avg" || left.Conversion != "" || left.Valid != 60 {
		t.Fatalf("left %+v", left)
	}
	if right := semantics.Right; right.Unit != "ms" || right.InputKind != KindSummary || right.Statistic != "p95" || right.Conversion != ConversionDeclaredSummaryStatistic || right.Population != "transaction" {
		t.Fatalf("right %+v", right)
	}
}

// Raw summary points (precomputed percentiles) are never correlated as if
// they were observations: only a declared statistic's scalar buckets are.
func TestCorrelationRefusesRawSummaryPoints(t *testing.T) {
	from := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	var summary, gauge []MetricPoint
	for i := 0; i < 5; i++ {
		at := from.Add(time.Duration(i) * 10 * time.Second)
		summary = append(summary, seriesPoint(at, "latency", "ms", KindSummary, "", float64(100+i)))
		gauge = append(gauge, seriesPoint(at, "cpu", "percent", KindGauge, "", float64(i)))
	}
	if _, err := LaggedCorrelation(gauge, summary, from, from.Add(50*time.Second), 10, 0); err == nil {
		t.Fatal("raw summary points were correlated")
	}
}

func TestCorrelationCountsExclusionsAndConversions(t *testing.T) {
	from := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	points := []MetricPoint{
		seriesPoint(from, "requests", "count/s", KindGauge, KindCounter, 5),
		{SchemaVersion: SchemaVersion, ProjectID: "project", Source: "collector", TimestampUTC: from.Add(10 * time.Second), MetricName: "requests", Unit: "count/s", Kind: KindGauge, InputKind: KindCounter, Quality: QualityMissing, MissingReason: MissingCounterReset},
		{SchemaVersion: SchemaVersion, ProjectID: "project", Source: "collector", TimestampUTC: from.Add(20 * time.Second), MetricName: "requests", Unit: "count/s", Kind: KindGauge, InputKind: KindCounter, Quality: QualityStale},
		{SchemaVersion: SchemaVersion, ProjectID: "project", Source: "collector", TimestampUTC: from.Add(30 * time.Second), MetricName: "requests", Unit: "count/s", Kind: KindGauge, InputKind: KindCounter, Quality: QualityMissing},
	}
	side := DescribeCorrelationSide("requests", "rate", points)
	if side.Conversion != ConversionCounterRate || side.Points != 4 || side.Valid != 1 || side.Excluded[MissingCounterReset] != 1 || side.Excluded[QualityStale] != 1 || side.Excluded[QualityMissing] != 1 {
		t.Fatalf("counter side %+v", side)
	}
	observation := DescribeCorrelationSide("latency", "p95", []MetricPoint{seriesPoint(from, "latency", "ms", KindGauge, KindObservation, 1)})
	if observation.Conversion != ConversionObservationPercentile {
		t.Fatalf("observation side %+v", observation)
	}
	if mean := DescribeCorrelationSide("latency", "avg", []MetricPoint{seriesPoint(from, "latency", "ms", KindGauge, KindObservation, 1)}); mean.Conversion != "" {
		t.Fatalf("a mean is not a conversion: %+v", mean)
	}
	same := DescribeCorrelation("a", "avg", []MetricPoint{seriesPoint(from, "a", "ms", KindGauge, KindObservation, 1)}, "b", "avg", []MetricPoint{seriesPoint(from, "b", "ms", KindGauge, KindObservation, 2)})
	if !same.SameUnits {
		t.Fatal("same unit and kind reported as different")
	}
	// The same unit from different source semantics (a summary's declared
	// p95 vs raw observations) is not directly comparable either.
	mixed := DescribeCorrelation("a", "p95", []MetricPoint{seriesPoint(from, "a", "ms", KindGauge, KindSummary, 1)}, "b", "p95", []MetricPoint{seriesPoint(from, "b", "ms", KindGauge, KindObservation, 2)})
	if mixed.SameUnits {
		t.Fatal("a summary statistic and observations were reported as directly comparable")
	}
	empty := DescribeCorrelation("a", "avg", nil, "b", "avg", nil)
	if empty.SameUnits || empty.Left.Points != 0 {
		t.Fatalf("empty sides %+v", empty)
	}
}

func TestCorrelationSaysWhyACoefficientIsUnavailable(t *testing.T) {
	from := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	var steady, varying, sparse []MetricPoint
	for i := 0; i < 10; i++ {
		at := from.Add(time.Duration(i) * 10 * time.Second)
		steady = append(steady, seriesPoint(at, "cpu", "percent", KindGauge, "", 42))
		varying = append(varying, seriesPoint(at, "latency", "ms", KindGauge, "", float64(i*i)))
		if i < 2 {
			sparse = append(sparse, seriesPoint(at, "latency", "ms", KindGauge, "", float64(i)))
		}
	}
	to := from.Add(100 * time.Second)
	scores, err := LaggedCorrelation(steady, varying, from, to, 10, 0)
	if err != nil || len(scores) != 1 || scores[0].Coefficient != nil || scores[0].Unavailable != CorrelationConstantSeries {
		t.Fatalf("constant series %+v %v", scores, err)
	}
	scores, err = LaggedCorrelation(varying, sparse, from, to, 10, 0)
	if err != nil || scores[0].Unavailable != CorrelationInsufficientPairs || scores[0].Pairs != 2 {
		t.Fatalf("insufficient pairs %+v %v", scores, err)
	}
	scores, err = LaggedCorrelation(varying, varying, from, to, 10, 0)
	if err != nil || scores[0].Coefficient == nil || scores[0].Unavailable != "" {
		t.Fatalf("a scored lag carries a reason: %+v %v", scores, err)
	}
}

// Bucketing reduces stale, skewed, dropped and reset samples to missing
// buckets; their reasons are counted from the raw samples first.
func TestRawSampleExclusionsKeepReasons(t *testing.T) {
	from := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	stale := seriesPoint(from, "cpu", "percent", KindGauge, "", 10)
	stale.Quality = QualityStale
	skewed := seriesPoint(from.Add(time.Second), "cpu", "percent", KindGauge, "", 11)
	skewed.Quality = QualityClockSkewed
	dropped := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "project", Source: "collector", TimestampUTC: from.Add(2 * time.Second), MetricName: "cpu", Quality: QualityDropped}
	// A counter that resets once per series, given out of order; another
	// series of the same metric keeps increasing.
	counter := func(at int, host string, value float64) MetricPoint {
		p := seriesPoint(from.Add(time.Duration(at)*time.Second), "requests", "count", KindCounter, "", value)
		p.Host = host
		return p
	}
	// A series with two readings at one instant counts it as a duplicate,
	// never as a reset; a gauge derived from a counter (per-bucket values)
	// falls without resetting.
	derived := func(at int, value float64) MetricPoint {
		p := seriesPoint(from.Add(time.Duration(at)*time.Second), "requests", "count", KindGauge, "", value)
		p.Host, p.InputKind = "c", KindCounter
		return p
	}
	points := []MetricPoint{stale, skewed, dropped, counter(3, "a", 5), counter(1, "a", 10), counter(2, "a", 20), counter(1, "b", 1), counter(2, "b", 2), counter(2, "d", 9), counter(2, "d", 4), derived(1, 120), derived(2, 95), derived(3, 130)}
	got := RawSampleExclusions(points)
	want := map[string]int{QualityStale: 1, QualityClockSkewed: 1, QualityDropped: 1, MissingCounterReset: 1, MissingDuplicateCounterInstant: 1}
	if len(got) != len(want) {
		t.Fatalf("exclusions %v, want %v", got, want)
	}
	for reason, count := range want {
		if got[reason] != count {
			t.Fatalf("exclusions %v, want %v", got, want)
		}
	}
	if RawSampleExclusions([]MetricPoint{seriesPoint(from, "cpu", "percent", KindGauge, "", 1)}) != nil {
		t.Fatal("valid samples report exclusions")
	}
}

func TestCorrelationSemanticsPopulationUnitsAndHistograms(t *testing.T) {
	from := time.Date(2026, 10, 8, 0, 0, 0, 0, time.UTC)
	transaction := seriesPoint(from, "transaction.duration", "ms", KindObservation, "", 120)
	transaction.Dimensions = map[string]string{"population": "transaction"}
	action := seriesPoint(from, "transaction.duration", "ms", KindObservation, "", 80)
	action.Population = "action"
	semantics := DescribeCorrelation("transaction.duration", "p95", []MetricPoint{transaction}, "transaction.duration", "p95", []MetricPoint{action})
	// Population comes from the dimension when the field is unset, and two
	// populations are never directly comparable.
	if semantics.Left.Population != "transaction" || semantics.Right.Population != "action" || semantics.SameUnits || semantics.UnitsUndeclared {
		t.Fatalf("populations %+v", semantics)
	}
	if same := DescribeCorrelation("a", "p95", []MetricPoint{transaction}, "b", "p95", []MetricPoint{transaction}); !same.SameUnits {
		t.Fatalf("same population %+v", same)
	}
	// A side with no evidence, or no declared unit, leaves the comparison
	// unknown rather than "different".
	undeclared := seriesPoint(from, "queue", "", KindGauge, "", 1)
	for _, right := range [][]MetricPoint{nil, {undeclared}} {
		if s := DescribeCorrelation("a", "avg", []MetricPoint{transaction}, "b", "avg", right); s.SameUnits || !s.UnitsUndeclared {
			t.Fatalf("undeclared %+v", s)
		}
	}
	// A histogram percentile is an interpolated estimate.
	histogram := seriesPoint(from, "latency", "ms", KindGauge, KindHistogram, 90)
	if side := DescribeCorrelationSide("latency", "p95", []MetricPoint{histogram}); side.Conversion != ConversionHistogramPercentileEstimate {
		t.Fatalf("histogram %+v", side)
	}
}
