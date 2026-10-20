//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Four aligned 60-second windows starting at 1700000040 (a minute boundary).
const summaryFixture = "SELECT 1700000040+i*60 AS ts, (500+i)::DOUBLE AS p95, (100*(i+1))::DOUBLE AS mean, (10*(i+1))::DOUBLE AS n, (10*(i+1)*100*(i+1))::DOUBLE AS total, i::DOUBLE AS low, (1000+i)::DOUBLE AS high FROM range(4) t(i)"

func summaryFixtureMeasure(column, name, statistic, unit string) Measure {
	return Measure{Value: Column{column}, Name: name, Kind: analysis.KindSummary, Unit: unit, Statistic: statistic, WindowSeconds: 60}
}

func normalizeSummaryFixture(t *testing.T, e *Engine, path string, mapping Mapping, allowRejected bool) NormalizeResult {
	t.Helper()
	result, err := normalizeConsentedNativeFixture(t, e, context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping, AllowRejected: allowRejected})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	return retainNativeFixtureOutputs(t, e, result)
}

func summarySeries(t *testing.T, e *Engine, files []string, metric, aggregation string, from time.Time, buckets, bucketSeconds int) []analysis.MetricPoint {
	t.Helper()
	points, err := e.Series(context.Background(), files, SeriesQuery{IdentitySchemaVersion: nativeFixtureIdentityVersion(t, e, files), ProjectID: "p", DatasetID: "d", Metric: metric, From: from, To: from.Add(time.Duration(buckets*bucketSeconds) * time.Second), BucketSeconds: bucketSeconds, Aggregation: aggregation})
	if err != nil {
		t.Fatalf("%s %s/%ds: %v", metric, aggregation, bucketSeconds, err)
	}
	return points
}

type summaryBucket struct {
	value  *float64
	reason string
}

func wantSummaryBuckets(t *testing.T, label string, points []analysis.MetricPoint, want ...summaryBucket) {
	t.Helper()
	if len(points) != len(want) {
		t.Fatalf("%s: %d buckets, want %d: %+v", label, len(points), len(want), points)
	}
	for i, point := range points {
		if point.Kind != analysis.KindGauge || point.InputKind != analysis.KindSummary {
			t.Fatalf("%s[%d]: summary semantics lost: %+v", label, i, point)
		}
		if want[i].value == nil {
			if point.Quality != analysis.QualityMissing || point.Value != nil || point.MissingReason != want[i].reason {
				t.Fatalf("%s[%d]: want missing (%q), got %+v", label, i, want[i].reason, point)
			}
			continue
		}
		if point.Quality != analysis.QualityValid || point.Value == nil || math.Abs(*point.Value-*want[i].value) > 1e-9 || point.MissingReason != "" {
			t.Fatalf("%s[%d]: want %v, got %+v", label, i, *want[i].value, point)
		}
	}
}

func valued(v float64) summaryBucket   { return summaryBucket{value: &v} }
func missing(r string) summaryBucket   { return summaryBucket{reason: r} }
func summaryStart() time.Time          { return time.Unix(1700000040, 0).UTC() }
func avgOf(sum, count float64) float64 { return sum / count }

func TestNativeSummaryStatisticsReBucketOnlyWhereExact(t *testing.T) {
	e, path := nativeFixture(t, summaryFixture)
	mean := summaryFixtureMeasure("mean", "latency", "avg", "ms")
	mean.SampleCountColumn, mean.SampleSumColumn = Column{"n"}, Column{"total"}
	requests := summaryFixtureMeasure("n", "requests", "count", "count")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{
		summaryFixtureMeasure("p95", "latency", "p95", "ms"), mean, requests,
		summaryFixtureMeasure("low", "latency", "min", "ms"), summaryFixtureMeasure("high", "latency", "max", "ms"),
	}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	files, from := result.Files, summaryStart()

	// At the source resolution every statistic is shown as reported.
	p95 := summarySeries(t, e, files, "latency", "p95", from, 4, 60)
	wantSummaryBuckets(t, "p95/60", p95, valued(500), valued(501), valued(502), valued(503))
	if want, _ := analysis.SummaryBucketDerivation("p95", 60, 60); !slices.Equal(p95[0].Derivation, want) || p95[0].Unit != "ms" {
		t.Fatalf("p95 lineage/unit %+v", p95[0])
	}
	// A precomputed percentile never merges two windows.
	wantSummaryBuckets(t, "p95/120", summarySeries(t, e, files, "latency", "p95", from, 2, 120),
		missing(analysis.MissingPercentileNotMergeable), missing(analysis.MissingPercentileNotMergeable))
	// Averages weight by sample counts; counts, sums and extrema combine exactly.
	wantSummaryBuckets(t, "avg/120", summarySeries(t, e, files, "latency", "avg", from, 2, 120),
		valued(avgOf(10*100+20*200, 30)), valued(avgOf(30*300+40*400, 70)))
	wantSummaryBuckets(t, "count/120", summarySeries(t, e, files, "requests", "count", from, 2, 120), valued(30), valued(70))
	wantSummaryBuckets(t, "min/240", summarySeries(t, e, files, "latency", "min", from, 1, 240), valued(0))
	wantSummaryBuckets(t, "max/120", summarySeries(t, e, files, "latency", "max", from, 2, 120), valued(1001), valued(1003))

	// The aggregation selects a declared statistic and whole windows only.
	for _, bad := range []struct {
		aggregation string
		bucket      int
	}{{"p99", 60}, {"p95", 30}, {"avg", 90}, {"rate", 60}} {
		if _, err := e.Series(context.Background(), files, SeriesQuery{IdentitySchemaVersion: nativeFixtureIdentityVersion(t, e, files), ProjectID: "p", DatasetID: "d", Metric: "latency", From: from, To: from.Add(4 * time.Minute), BucketSeconds: bad.bucket, Aggregation: bad.aggregation}); err == nil {
			t.Errorf("summary answered %s over %ds", bad.aggregation, bad.bucket)
		}
	}
}

func TestNativeSummaryPartialWindowsDuplicatesNullsAndMissingCounts(t *testing.T) {
	// Windows start 30s after each minute, a duplicated window, a missing value
	// and averages without sample counts.
	e, path := nativeFixture(t, "SELECT * FROM (VALUES (1700000070, 10.0, 1.0), (1700000130, 20.0, 2.0), (1700000250, 30.0, 3.0), (1700000250, 31.0, 3.0), (1700000370, NULL, 4.0), (1700000430, 50.0, 5.0)) t(ts, mean, low)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{
		summaryFixtureMeasure("mean", "latency", "avg", "ms"), summaryFixtureMeasure("low", "latency", "min", "ms"),
	}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	files := result.Files
	// Buckets aligned to the windows: single windows are exact, a duplicated
	// window and a missing sample are not.
	aligned := time.Unix(1700000070, 0).UTC()
	wantSummaryBuckets(t, "avg aligned", summarySeries(t, e, files, "latency", "avg", aligned, 7, 60),
		valued(10), valued(20), missing(""), missing(analysis.MissingDuplicateSummaryWindow), missing(""), missing(""), valued(50))
	// Two windows per bucket: an average without sample counts is unscored.
	wantSummaryBuckets(t, "avg merged", summarySeries(t, e, files, "latency", "avg", aligned, 1, 120), missing(analysis.MissingAverageWithoutCounts))
	wantSummaryBuckets(t, "min merged", summarySeries(t, e, files, "latency", "min", aligned, 1, 120), valued(1))
	// Buckets on the minute: every window crosses its bucket's end.
	wantSummaryBuckets(t, "min straddling", summarySeries(t, e, files, "latency", "min", time.Unix(1700000040, 0).UTC(), 2, 60),
		missing(analysis.MissingPartialSummaryWindow), missing(analysis.MissingPartialSummaryWindow))
}

func TestNativeSummaryScalesSampleSumsAndRejectsInvalidSamples(t *testing.T) {
	e, path := nativeFixture(t, "SELECT * FROM (VALUES (1700000040, 0.1, 2.0, 0.2), (1700000100, 0.3, 2.0, 0.6), (1700000160, 0.5, -1.0, 1.0)) t(ts, mean, n, total)")
	mean := Measure{Value: Column{"mean"}, Name: "latency", Kind: analysis.KindSummary, Unit: "s", Statistic: "avg", WindowSeconds: 60, SampleCountColumn: Column{"n"}, SampleSumColumn: Column{"total"}}
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{mean}}
	if _, err := normalizeConsentedNativeFixture(t, e, context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping}); err == nil {
		t.Fatal("a negative sample count was accepted")
	}
	result := normalizeSummaryFixture(t, e, path, mapping, true)
	if result.Accepted != 2 || result.Rejected != 1 {
		t.Fatalf("rejections %+v", result)
	}
	// Sample sums are in the value unit: seconds become milliseconds for both.
	points := summarySeries(t, e, result.Files, "latency", "avg", summaryStart(), 1, 120)
	wantSummaryBuckets(t, "scaled avg", points, valued(avgOf(200+600, 4)))
	if points[0].Unit != "ms" {
		t.Fatalf("unit %q", points[0].Unit)
	}
}

func TestNativeSummaryWallTimeWindowsAcrossDaylightSaving(t *testing.T) {
	// US spring-forward, 2026-03-08: local 01:00 and 01:30 are EST, 03:00 and
	// 03:30 EDT. Four 30-minute windows are contiguous in UTC (06:00-08:00).
	e, path := nativeFixture(t, "SELECT * FROM (VALUES (TIMESTAMP '2026-03-08 01:00:00', 1.0), (TIMESTAMP '2026-03-08 01:30:00', 2.0), (TIMESTAMP '2026-03-08 03:00:00', 3.0), (TIMESTAMP '2026-03-08 03:30:00', 4.0)) t(ts, requests)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp", Timezone: "America/New_York", Measures: []Measure{
		{Value: Column{"requests"}, Name: "requests", Kind: analysis.KindSummary, Unit: "count", Statistic: "sum", WindowSeconds: 1800},
	}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	from := time.Date(2026, 3, 8, 6, 0, 0, 0, time.UTC)
	wantSummaryBuckets(t, "dst hourly", summarySeries(t, e, result.Files, "requests", "sum", from, 2, 3600), valued(3), valued(7))
}

func TestNativeSummaryArtifactsKeepRawArtifactsUnchanged(t *testing.T) {
	e, path := nativeFixture(t, summaryFixture)
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{
		{Value: Column{"mean"}, Name: "mean", Kind: analysis.KindGauge, Unit: "ms"},
		summaryFixtureMeasure("p95", "latency", "p95", "ms"),
	}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	if len(result.Files) != 2 {
		t.Fatalf("files %v", result.Files)
	}
	hasSummaryColumns := func(file string) bool {
		schema, err := analysis.InspectParquet(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, c := range schema.Columns {
			if c.Path == "summary_statistic" {
				return true
			}
		}
		return false
	}
	if hasSummaryColumns(result.Files[0]) || !hasSummaryColumns(result.Files[1]) {
		t.Fatal("only declared summary artifacts carry summary columns")
	}
	// Raw and summary artifacts read together; each series keeps its semantics.
	raw := summarySeries(t, e, result.Files, "mean", "avg", summaryStart(), 1, 240)
	if len(raw) != 1 || raw[0].InputKind != analysis.KindGauge || raw[0].Value == nil || *raw[0].Value != 250 {
		t.Fatalf("raw series %+v", raw)
	}
	wantSummaryBuckets(t, "mixed p95", summarySeries(t, e, result.Files, "latency", "p95", summaryStart(), 1, 60), valued(500))
	for _, partition := range result.Partitions {
		if partition.Stats.Validate() != nil {
			t.Fatalf("partition stats %+v", partition.Stats)
		}
	}
}
