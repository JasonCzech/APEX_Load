package analysis

import (
	"testing"
	"time"
)

func TestBucketSeriesPreservesMissingAndCounterResets(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var points []MetricPoint
	for i, v := range []float64{10, 12, 2, 6} {
		points = append(points, MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "s", MetricName: "requests", Kind: KindCounter, Unit: "count", Value: &v, Quality: QualityValid, TimestampUTC: start.Add(time.Duration(i) * time.Second)})
	}
	got, err := BucketSeries(points, start, start.Add(5*time.Second), 1, "rate")
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || got[0].Value != nil || *got[1].Value != 2 || got[2].Value != nil || *got[3].Value != 4 || got[4].Value != nil || got[3].Unit != "count/s" {
		t.Fatalf("counter buckets: %+v", got)
	}
	if _, err = BucketSeries(points, start, start.Add(5*time.Second), 1, "avg"); err == nil {
		t.Fatal("averaged counter")
	}
	points[0].Kind = KindSummary
	if _, err = BucketSeries(points[:1], start, start.Add(time.Second), 1, "avg"); err == nil {
		t.Fatal("averaged summary")
	}
}

func TestBucketSeriesDuplicateObservationsAndMixedSeries(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v := 2.0
	p := MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "s", MetricName: "latency", Kind: KindObservation, Unit: "ms", Value: &v, Quality: QualityValid, TimestampUTC: start}
	points := []MetricPoint{p, p}
	got, err := BucketSeries(points, start, start.Add(2*time.Second), 1, "count")
	if err != nil || len(got) != 2 || *got[0].Value != 2 || got[1].Value != nil {
		t.Fatalf("observations: %+v %v", got, err)
	}
	points[1].Host = "another"
	if _, err = BucketSeries(points, start, start.Add(2*time.Second), 1, "avg"); err == nil {
		t.Fatal("merged hosts implicitly")
	}
}

func TestBucketCounterDuplicatesBreakBothAdjacentIntervals(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points := []MetricPoint{}
	for i, second := range []int{0, 1, 1, 2, 3} {
		value := float64(i * 2)
		points = append(points, MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "s", MetricName: "n", Kind: KindCounter, Unit: "count", Value: &value, Quality: QualityValid, TimestampUTC: start.Add(time.Duration(second) * time.Second)})
	}
	got, err := BucketSeries(points, start, start.Add(4*time.Second), 1, "rate")
	if err != nil {
		t.Fatal(err)
	}
	for i, p := range got {
		if i < 3 && p.Value != nil || i == 3 && (p.Value == nil || *p.Value != 2) {
			t.Errorf("bucket %d: %+v", i, p)
		}
	}
}

func TestBucketSeriesPercentilesRequireObservations(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	var points []MetricPoint
	for _, v := range []float64{1, 2, 3, 4, 5} {
		points = append(points, MetricPoint{SchemaVersion: SchemaVersion, ProjectID: "p", Source: "s", MetricName: "latency", Kind: KindObservation, Unit: "ms", Value: &v, TimestampUTC: start, Quality: QualityValid})
	}
	got, err := BucketSeries(points, start, start.Add(time.Second), 1, "p95")
	if err != nil || len(got) != 1 || got[0].Value == nil || *got[0].Value < 4.79 || *got[0].Value > 4.81 || got[0].InputKind != KindObservation {
		t.Fatalf("p95: %+v %v", got, err)
	}
	points[0].Kind = KindGauge
	if _, err = BucketSeries(points[:1], start, start.Add(time.Second), 1, "p95"); err == nil {
		t.Fatal("gauge percentile accepted")
	}
}

func TestBucketSeriesPreservesExplicitSemanticIdentity(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	input := qualifiedPoint(start, 3)
	got, err := BucketSeries([]MetricPoint{input}, start, start.Add(time.Second), 1, "avg")
	if err != nil || len(got) != 1 || got[0].Population != input.Population || got[0].MetricVersion != input.MetricVersion {
		t.Fatalf("bucket identity: %+v error %v", got, err)
	}
	mixed := input
	mixed.Population = PopulationTransaction
	if _, err := BucketSeries([]MetricPoint{input, mixed}, start, start.Add(time.Second), 1, "count"); err == nil {
		t.Fatal("merged different populations")
	}
}

func TestEmptyBucketSeriesStillValidatesOperation(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		operation string
		supported bool
	}{
		{"avg", true}, {"p95", true}, {"rate", true},
		{"p90", false}, {"summary_value", false}, {"arbitrary", false},
	} {
		t.Run(test.operation, func(t *testing.T) {
			points, err := BucketSeries(nil, start, start.Add(time.Second), 1, test.operation)
			if test.supported {
				if err != nil || points == nil || len(points) != 0 {
					t.Fatalf("valid empty selection: %v %v", points, err)
				}
			} else if err == nil {
				t.Fatal("unsupported operation accepted for empty input")
			}
		})
	}
}
