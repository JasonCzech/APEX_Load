//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

type histogramBucket struct {
	value    *float64
	reason   string
	low, sup float64
}

func estimated(v, low, sup float64) histogramBucket {
	return histogramBucket{value: &v, low: low, sup: sup}
}
func counted(v float64) histogramBucket      { return histogramBucket{value: &v} }
func unscored(reason string) histogramBucket { return histogramBucket{reason: reason} }

func histogramSeries(t *testing.T, e *Engine, files []string, metric, aggregation string, from time.Time, buckets, bucketSeconds int) []analysis.MetricPoint {
	t.Helper()
	points, err := e.Series(context.Background(), files, SeriesQuery{IdentitySchemaVersion: nativeFixtureIdentityVersion(t, e, files), ProjectID: "p", DatasetID: "d", Metric: metric, From: from, To: from.Add(time.Duration(buckets*bucketSeconds) * time.Second), BucketSeconds: bucketSeconds, Aggregation: aggregation})
	if err != nil {
		t.Fatalf("%s %s/%ds: %v", metric, aggregation, bucketSeconds, err)
	}
	return points
}

func wantHistogramBuckets(t *testing.T, label string, points []analysis.MetricPoint, want ...histogramBucket) {
	t.Helper()
	if len(points) != len(want) {
		t.Fatalf("%s: %d buckets, want %d: %+v", label, len(points), len(want), points)
	}
	for i, p := range points {
		w := want[i]
		if p.Kind != analysis.KindGauge || p.InputKind != analysis.KindHistogram || len(p.Derivation) != 2 {
			t.Fatalf("%s[%d]: histogram semantics lost: %+v", label, i, p)
		}
		if w.value == nil {
			if p.Quality != analysis.QualityMissing || p.Value != nil || p.MissingReason != w.reason {
				t.Fatalf("%s[%d]: want missing (%q), got %+v", label, i, w.reason, p)
			}
			continue
		}
		if p.Quality != analysis.QualityValid || p.Value == nil || math.Abs(*p.Value-*w.value) > 1e-9 {
			t.Fatalf("%s[%d]: want %v, got %+v", label, i, *w.value, p)
		}
		if w.sup != 0 && (p.EstimateBin == nil || p.EstimateBin.Lower != w.low || p.EstimateBin.Upper != w.sup || p.Derivation[1].Algorithm != "histogram_interpolated.v1") {
			t.Fatalf("%s[%d]: estimate bin %+v, want [%v,%v]", label, i, p.EstimateBin, w.low, w.sup)
		}
	}
}

func TestNativeWideDeltaHistogramMergesBinsAndInterpolates(t *testing.T) {
	// Four one-minute windows; bins <=10ms, <=100ms and overflow.
	e, path := nativeFixture(t, "SELECT 1700000040+i*60 AS ts, (4+i)::DOUBLE AS fast, (4-i)::DOUBLE AS slow, CASE WHEN i=3 THEN 10.0 ELSE 0.0 END AS overflow FROM range(4) t(i)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"overflow"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "s",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"0.01", "0.1", "+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []Column{{"fast"}, {"slow"}}},
	}}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	if result.Accepted != 4 || len(result.Partitions) != 1 || result.Partitions[0].Stats.Kind != analysis.KindHistogram || result.Partitions[0].Stats.Rows != 4 || result.Partitions[0].Stats.SourceRows != 4 {
		t.Fatalf("normalized %+v", result)
	}
	from := summaryStart()
	// Counts add across windows: (4+4)+(5+3) and (6+2)+(7+1+10).
	wantHistogramBuckets(t, "count/120", histogramSeries(t, e, result.Files, "latency", "count", from, 2, 120), counted(16), counted(26))
	// Bounds are in milliseconds after unit conversion. Bucket one holds 9
	// fast and 7 slow observations; the median (rank 8) is in the first bin:
	// 0 + 10*8/9.
	wantHistogramBuckets(t, "p50/120", histogramSeries(t, e, result.Files, "latency", "p50", from, 2, 120), estimated(10*8.0/9, 0, 10), estimated(10*13.0/13, 0, 10))
	// p90 of the second bucket (rank 23.4 of 26) falls in the overflow bin.
	wantHistogramBuckets(t, "p90/120", histogramSeries(t, e, result.Files, "latency", "p90", from, 2, 120), estimated(10+90*(14.4-9)/7, 10, 100), unscored(analysis.MissingHistogramOverflowBin))
	for _, bad := range []struct {
		aggregation string
		bucket      int
	}{{"avg", 60}, {"p95", 90}, {"rate", 60}} {
		if _, err := e.Series(context.Background(), result.Files, SeriesQuery{IdentitySchemaVersion: nativeFixtureIdentityVersion(t, e, result.Files), ProjectID: "p", DatasetID: "d", Metric: "latency", From: from, To: from.Add(4 * time.Minute), BucketSeconds: bad.bucket, Aggregation: bad.aggregation}); err == nil {
			t.Errorf("histogram answered %s over %ds", bad.aggregation, bad.bucket)
		}
	}
}

func TestNativeLongCumulativeHistogramDetectsResets(t *testing.T) {
	// Prometheus-style scrapes: le-cumulative buckets of a counter, scraped
	// each minute, with a process restart before the fourth scrape.
	e, path := nativeFixture(t, `SELECT * FROM (VALUES
		(1700000040, '0.1', 1.0), (1700000040, '0.5', 3.0), (1700000040, '+Inf', 4.0),
		(1700000100, '0.1', 2.0), (1700000100, '0.5', 6.0), (1700000100, '+Inf', 9.0),
		(1700000160, '0.1', 4.0), (1700000160, '0.5', 9.0), (1700000160, '+Inf', 13.0),
		(1700000220, '0.1', 0.0), (1700000220, '0.5', 1.0), (1700000220, '+Inf', 1.0),
		(1700000280, '0.1', 1.0), (1700000280, '0.5', 3.0), (1700000280, '+Inf', 3.0)
	) t(ts, le, requests)`)
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"requests"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "s",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"0.1", "0.5", "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramCumulative, BoundColumn: Column{"le"}},
	}}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	stats := result.Partitions[0].Stats
	// 15 source rows form five scrapes and four windows between them.
	if result.Accepted != 15 || stats.SourceRows != 15 || stats.Rows != 4 || stats.Missing != 1 {
		t.Fatalf("normalized %+v stats %+v", result, stats)
	}
	from := summaryStart()
	// Windows: [40,100) per-bin delta (1,2,2); [100,160) (2,1,1); reset;
	// [220,280) (1,1,0).
	wantHistogramBuckets(t, "count/60", histogramSeries(t, e, result.Files, "latency", "count", from, 5, 60),
		counted(5), counted(4), unscored(analysis.MissingHistogramCounterReset), counted(2), unscored(""))
	wantHistogramBuckets(t, "count/120", histogramSeries(t, e, result.Files, "latency", "count", from, 2, 120), counted(9), unscored(analysis.MissingHistogramCounterReset))
	// Merged (3,3,3): the median (rank 4.5) lies in the 100-500ms bin.
	wantHistogramBuckets(t, "p50/120", histogramSeries(t, e, result.Files, "latency", "p50", from, 1, 120), estimated(100+400*(4.5-3)/3, 100, 500))
	// Cumulative windows follow the samples, so a bucket that ends inside a
	// window cannot hold it whole.
	wantHistogramBuckets(t, "count/90", histogramSeries(t, e, result.Files, "latency", "count", from, 1, 90), unscored(analysis.MissingPartialHistogramWindow))
}

func TestNativeHistogramRejectsMalformedRowsWithProvenance(t *testing.T) {
	e, path := nativeFixture(t, `SELECT * FROM (VALUES
		(1700000040, '1', 1.0), (1700000040, '+Inf', 2.0),
		(1700000100, '1', 1.0),
		(1700000160, '1', 3.0), (1700000160, '+Inf', 2.0),
		(1700000220, '1', -1.0), (1700000220, '+Inf', 2.0),
		(1700000280, '2', 1.0), (1700000280, '+Inf', 2.0),
		(1700000340, '1', 1.0), (1700000340, '+Inf', 4.0)
	) t(ts, le, n)`)
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"n"}, Name: "size", Kind: analysis.KindHistogram, Unit: "bytes",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"1", "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramDelta, WindowSeconds: 60, BoundColumn: Column{"le"}},
	}}}
	if _, err := normalizeConsentedNativeFixture(t, e, context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping}); err == nil {
		t.Fatal("malformed histograms accepted without explicit acceptance")
	}
	result := normalizeSummaryFixture(t, e, path, mapping, true)
	// Kept: the first and last histograms. Rejected: a missing bound, a
	// decreasing le count, a negative count and an undeclared bound.
	if result.Accepted != 4 || result.Rejected != 7 || len(result.Rejections) != 1 {
		t.Fatalf("accounting %+v", result)
	}
	if got := result.Rejections[0].Rows; len(got) != 7 || got[0] != 2 || got[6] != 8 {
		t.Fatalf("rejected rows %v", got)
	}
	wantHistogramBuckets(t, "count", histogramSeries(t, e, result.Files, "size", "count", summaryStart(), 6, 60),
		counted(2), unscored(""), unscored(""), unscored(""), unscored(""), counted(4))
}

func TestNativeHistogramRefusesDifferentBoundSets(t *testing.T) {
	// Two long-table measures name the same metric with different bounds.
	e, path := nativeFixture(t, "SELECT * FROM (VALUES (1700000040, 'rt', '1', '2', 1.0, 1.0), (1700000040, 'rt', '+Inf', '+Inf', 2.0, 3.0)) t(ts, metric, le_a, le_b, a, b)")
	measure := func(value, le, low string) Measure {
		return Measure{Value: Column{value}, NameColumn: Column{"metric"}, Kind: analysis.KindHistogram, Unit: "ms", Histogram: &analysis.ParquetHistogram{Bounds: []string{low, "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramDelta, WindowSeconds: 60, BoundColumn: Column{le}}}
	}
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{measure("a", "le_a", "1"), measure("b", "le_b", "2")}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	_, err := e.Series(context.Background(), result.Files, SeriesQuery{IdentitySchemaVersion: nativeFixtureIdentityVersion(t, e, result.Files), ProjectID: "p", DatasetID: "d", Metric: "rt", From: summaryStart(), To: summaryStart().Add(time.Minute), BucketSeconds: 60, Aggregation: "count"})
	if err == nil {
		t.Fatal("histograms with different bounds merged")
	}
}

func TestNativeHistogramAndScalarArtifactsQueryTogether(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000040+i*60 AS ts, 1.0 AS low, 1.0 AS high, (i+1)::DOUBLE AS cpu FROM range(2) t(i)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{
		{Value: Column{"cpu"}, Name: "cpu", Kind: analysis.KindGauge, Unit: "percent"},
		{Value: Column{"high"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "ms", Histogram: &analysis.ParquetHistogram{Bounds: []string{"10", "+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []Column{{"low"}}}},
	}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	if result.Accepted != 4 {
		t.Fatalf("accepted %d", result.Accepted)
	}
	cpu := histogramSeries(t, e, result.Files, "cpu", "avg", summaryStart(), 1, 120)
	if len(cpu) != 1 || cpu[0].Value == nil || *cpu[0].Value != 1.5 || cpu[0].InputKind != analysis.KindGauge {
		t.Fatalf("scalar series %+v", cpu)
	}
	wantHistogramBuckets(t, "latency", histogramSeries(t, e, result.Files, "latency", "count", summaryStart(), 1, 120), counted(4))
}
