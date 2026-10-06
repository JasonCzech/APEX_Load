//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// nativeExtraFixture writes another input file into the engine's root.
func nativeExtraFixture(t *testing.T, e *Engine, name, query string) string {
	t.Helper()
	path := filepath.Join(e.root, name)
	if err := e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		_, err := db.ExecContext(ctx, "COPY ("+query+") TO "+literal(path)+" (FORMAT PARQUET)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return path
}

func cumulativeLatency(bound Column) Mapping {
	return Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"requests"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "ms",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"100", "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramCumulative, BoundColumn: bound},
	}}}
}

// A cumulative series split across files keeps the window that spans the
// file boundary.
func TestNativeCumulativeHistogramDifferencesAcrossFiles(t *testing.T) {
	scrape := func(ts int, low, total float64) string {
		return "(" + itoa(ts) + ", '100', " + ftoa(low) + "), (" + itoa(ts) + ", '+Inf', " + ftoa(total) + ")"
	}
	// File b (later scrapes) is listed first: order of files must not matter.
	e, a := nativeFixture(t, "SELECT * FROM (VALUES "+scrape(1700000040, 1, 2)+", "+scrape(1700000100, 2, 4)+") t(ts, le, requests)")
	b := nativeExtraFixture(t, e, "b.parquet", "SELECT * FROM (VALUES "+scrape(1700000160, 4, 7)+", "+scrape(1700000220, 5, 9)+", "+scrape(1700000280, 6, 12)+") t(ts, le, requests)")
	result, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{b, a}, Mapping: cumulativeLatency(Column{"le"})})
	if err != nil {
		t.Fatal(err)
	}
	result = retainNativeFixtureOutputs(t, e, result)
	windows, sources := int64(0), int64(0)
	for _, partition := range result.Partitions {
		windows += partition.Stats.Rows
		sources += partition.Stats.SourceRows
	}
	// Five scrapes in two files: four windows, one of them across the files.
	if windows != 4 || sources != 10 || result.Accepted != 10 {
		t.Fatalf("windows %d sources %d %+v", windows, sources, result)
	}
	// Every observation from the first to the last scrape is counted once:
	// 12 total at the end minus 2 at the start.
	wantHistogramBuckets(t, "count/240", histogramSeries(t, e, result.Files, "latency", "count", summaryStart(), 1, 240), counted(10))
}

// An instant two files both report is one sample when they agree (an
// overlapping export) and a duplicate when they disagree: two series mixed
// under one identity must never be differenced against each other.
func TestNativeCumulativeHistogramInstantsReportedByTwoFiles(t *testing.T) {
	scrape := func(ts int, low, total float64) string {
		return "(" + itoa(ts) + ", '100', " + ftoa(low) + "), (" + itoa(ts) + ", '+Inf', " + ftoa(total) + ")"
	}
	// Overlap: both files report +60 with the same bins.
	e, a := nativeFixture(t, "SELECT * FROM (VALUES "+scrape(1700000040, 1, 2)+", "+scrape(1700000100, 2, 4)+") t(ts, le, requests)")
	b := nativeExtraFixture(t, e, "b.parquet", "SELECT * FROM (VALUES "+scrape(1700000100, 2, 4)+", "+scrape(1700000160, 4, 7)+") t(ts, le, requests)")
	result, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{a, b}, Mapping: cumulativeLatency(Column{"le"})})
	if err != nil {
		t.Fatal(err)
	}
	result = retainNativeFixtureOutputs(t, e, result)
	wantHistogramBuckets(t, "overlap count/120", histogramSeries(t, e, result.Files, "latency", "count", summaryStart(), 1, 120), counted(5))

	// Mixed: two pods' series land under one identity with different counts
	// at the same instants.
	e, a = nativeFixture(t, "SELECT * FROM (VALUES "+scrape(1700000040, 500, 1000)+", "+scrape(1700000100, 530, 1060)+") t(ts, le, requests)")
	b = nativeExtraFixture(t, e, "b.parquet", "SELECT * FROM (VALUES "+scrape(1700000040, 5, 10)+", "+scrape(1700000100, 35, 70)+") t(ts, le, requests)")
	result, err = e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{a, b}, Mapping: cumulativeLatency(Column{"le"})})
	if err != nil {
		t.Fatal(err)
	}
	result = retainNativeFixtureOutputs(t, e, result)
	wantHistogramBuckets(t, "mixed count/60", histogramSeries(t, e, result.Files, "latency", "count", summaryStart(), 1, 60), unscored(analysis.MissingDuplicateHistogramRow))
}

// The lead-in that makes a first bucket partial belongs to the queried
// series: another series' window ending just after From says nothing about
// it.
func TestNativeHistogramLeadInIsTheQueriedSeries(t *testing.T) {
	// Host b's only window starts 30 s before From and ends inside the first
	// bucket; host a's windows lie wholly inside the requested range.
	e, path := nativeFixture(t, "SELECT * FROM (VALUES ('b', 1700000010, 1.0, 1.0), ('a', 1700000040, 1.0, 2.0), ('a', 1700000100, 2.0, 1.0)) t(host, ts, b100, binf)")
	mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Identity: &analysis.ParquetIdentityMapping{Host: Column{"host"}}, Measures: []Measure{{
		Value: Column{"binf"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "ms",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"100", "+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []Column{{"b100"}}},
	}}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	from := summaryStart()
	points, err := e.Series(context.Background(), result.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "latency", IdentitySchemaVersion: 2, From: from, To: from.Add(120 * time.Second), BucketSeconds: 120, Aggregation: "count"})
	if err != nil {
		t.Fatal(err)
	}
	wantHistogramBuckets(t, "host a count/120", points, counted(6))
}

// A bucket is partial when a window from an earlier bucket ends inside it.
func TestNativeCumulativeHistogramWindowCrossingIntoABucketIsPartial(t *testing.T) {
	scrape := func(ts int, low, total float64) string {
		return "(" + itoa(ts) + ", '100', " + ftoa(low) + "), (" + itoa(ts) + ", '+Inf', " + ftoa(total) + ")"
	}
	// Samples at +0, +100, +130 and +240 seconds; buckets of 120 s.
	e, path := nativeFixture(t, "SELECT * FROM (VALUES "+scrape(1700000040, 1, 1)+", "+scrape(1700000140, 2, 3)+", "+scrape(1700000170, 3, 5)+", "+scrape(1700000280, 4, 8)+") t(ts, le, requests)")
	result := normalizeSummaryFixture(t, e, path, cumulativeLatency(Column{"le"}), false)
	// Bucket one holds the window +0..+100 whole but +100..+130 crosses its
	// end; bucket two holds +130..+240 but +100..+130 crossed into it.
	wantHistogramBuckets(t, "count/120", histogramSeries(t, e, result.Files, "latency", "count", summaryStart(), 2, 120),
		unscored(analysis.MissingPartialHistogramWindow), unscored(analysis.MissingPartialHistogramWindow))
}

// A FLOAT bound column matches the declared decimal bounds.
func TestNativeHistogramAcceptsAFloatBoundColumn(t *testing.T) {
	e, path := nativeFixture(t, "SELECT ts, CAST(le AS FLOAT) AS le, requests FROM (VALUES (1700000040, 0.1, 1.0), (1700000040, 'inf'::DOUBLE, 2.0), (1700000100, 0.1, 2.0), (1700000100, 'inf'::DOUBLE, 5.0)) t(ts, le, requests)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"requests"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "ms",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"0.1", "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramCumulative, BoundColumn: Column{"le"}},
	}}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	if result.Accepted != 4 || result.Rejected != 0 {
		t.Fatalf("FLOAT bounds rejected: %+v", result)
	}
}

// A duplicated long-table group, a wide le_cumulative histogram and the
// precise refusal of different bound sets.
func TestNativeHistogramGroupValidationAndPreciseRefusals(t *testing.T) {
	e, path := nativeFixture(t, `SELECT * FROM (VALUES
		(1700000040, '1', 1.0), (1700000040, '+Inf', 2.0),
		(1700000100, '1', 1.0), (1700000100, '1', 1.0), (1700000100, '+Inf', 2.0)
	) t(ts, le, n)`)
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"n"}, Name: "size", Kind: analysis.KindHistogram, Unit: "bytes",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"1", "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramDelta, WindowSeconds: 60, BoundColumn: Column{"le"}},
	}}}
	result := normalizeSummaryFixture(t, e, path, mapping, true)
	if result.Accepted != 2 || result.Rejected != 3 {
		t.Fatalf("a repeated bound in one group was admitted: %+v", result)
	}

	wide, widePath := nativeFixture(t, "SELECT 1700000040+i*60 AS ts, (1+i)::DOUBLE AS le1, (3+i)::DOUBLE AS total FROM range(2) t(i)")
	wideMapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"total"}, Name: "size", Kind: analysis.KindHistogram, Unit: "bytes",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"1", "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []Column{{"le1"}}},
	}}}
	wideResult := normalizeSummaryFixture(t, wide, widePath, wideMapping, false)
	// le-cumulative (1,3) and (2,4) become per-bin (1,2) and (2,2).
	wantHistogramBuckets(t, "wide le", histogramSeries(t, wide, wideResult.Files, "size", "count", summaryStart(), 1, 120), counted(7))

	bounds, boundsPath := nativeFixture(t, "SELECT * FROM (VALUES (1700000040, 'rt', '1', '2', 1.0, 1.0), (1700000040, 'rt', '+Inf', '+Inf', 2.0, 3.0)) t(ts, metric, le_a, le_b, a, b)")
	measure := func(value, le, low string) Measure {
		return Measure{Value: Column{value}, NameColumn: Column{"metric"}, Kind: analysis.KindHistogram, Unit: "ms", Histogram: &analysis.ParquetHistogram{Bounds: []string{low, "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramDelta, WindowSeconds: 60, BoundColumn: Column{le}}}
	}
	boundsResult := normalizeSummaryFixture(t, bounds, boundsPath, Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{measure("a", "le_a", "1"), measure("b", "le_b", "2")}}, false)
	_, err := bounds.Series(context.Background(), boundsResult.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "rt", From: summaryStart(), To: summaryStart().Add(time.Minute), BucketSeconds: 60, Aggregation: "count"})
	if !errors.Is(err, ErrIncompatibleHistogramBounds) {
		t.Fatalf("different bound sets: %v", err)
	}
}

// A coarse summary bucket is missing by the windows it spans, not by how
// many rows happen to be present, and sample-only faults have their reason.
func TestNativeSummarySparseCoarseBucketsAndSampleRejections(t *testing.T) {
	e, path := nativeFixture(t, "SELECT * FROM (VALUES (1700000040, 50.0, 10.0), (1700000340, 60.0, -1.0)) t(ts, p95, n)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{
		{Value: Column{"p95"}, Name: "latency", Kind: analysis.KindSummary, Unit: "ms", Statistic: "p95", WindowSeconds: 60, SampleCountColumn: Column{"n"}},
	}}
	result := normalizeSummaryFixture(t, e, path, mapping, true)
	if result.Accepted != 1 || result.Rejected != 1 || len(result.RejectionFiles) != 1 {
		t.Fatalf("accounting %+v", result)
	}
	var reason string
	if err := e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT reason FROM read_parquet("+literal(result.RejectionFiles[0])+")").Scan(&reason)
	}); err != nil || reason != analysis.RejectionInvalidSummarySample {
		t.Fatalf("sample-only rejection reason %q %v", reason, err)
	}
	// One p95 row in a 300 s bucket of 60 s windows is not that bucket's p95.
	points := summarySeries(t, e, result.Files, "latency", "p95", summaryStart(), 1, 300)
	if len(points) != 1 || points[0].Value != nil || points[0].MissingReason != analysis.MissingPercentileNotMergeable {
		t.Fatalf("sparse coarse p95: %+v", points)
	}
}

// A negative count is a bad value, not a bad sample: its samples are fine.
func TestNativeSummaryNegativeCountIsAValueRejection(t *testing.T) {
	e, path := nativeFixture(t, "SELECT * FROM (VALUES (1700000040, 5.0, 10.0), (1700000100, -1.0, 10.0)) t(ts, requests, n)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{
		{Value: Column{"requests"}, Name: "requests", Kind: analysis.KindSummary, Unit: "count", Statistic: "count", WindowSeconds: 60, SampleCountColumn: Column{"n"}},
	}}
	result := normalizeSummaryFixture(t, e, path, mapping, true)
	if result.Accepted != 1 || result.Rejected != 1 || len(result.RejectionFiles) != 1 {
		t.Fatalf("accounting %+v", result)
	}
	var reason string
	if err := e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, "SELECT reason FROM read_parquet("+literal(result.RejectionFiles[0])+")").Scan(&reason)
	}); err != nil || reason != "invalid_timestamp_name_or_value" {
		t.Fatalf("negative count rejection reason %q %v", reason, err)
	}
}

// An average spanning several windows needs sample counts, however few rows
// the bucket holds.
func TestNativeSummarySparseAverageWithoutCountsSpanningWindows(t *testing.T) {
	e, path := nativeFixture(t, "SELECT * FROM (VALUES (1700000040, 50.0)) t(ts, mean)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{
		{Value: Column{"mean"}, Name: "latency", Kind: analysis.KindSummary, Unit: "ms", Statistic: "avg", WindowSeconds: 60},
	}}
	result := normalizeSummaryFixture(t, e, path, mapping, false)
	points := summarySeries(t, e, result.Files, "latency", "avg", summaryStart(), 1, 300)
	if len(points) != 1 || points[0].Value != nil || points[0].MissingReason != analysis.MissingAverageWithoutCounts {
		t.Fatalf("sparse coarse average without counts: %+v", points)
	}
}

// Two delta windows of one series at one instant make that bucket a
// duplicate; a wide cumulative histogram refuses a repeated instant at import.
func TestNativeHistogramDuplicateWindowsAndWideCumulativeInstants(t *testing.T) {
	e, path := nativeFixture(t, "SELECT * FROM (VALUES (1700000040, 1.0, 2.0), (1700000040, 3.0, 4.0)) t(ts, low, high)")
	delta := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"high"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "ms",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"10", "+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []Column{{"low"}}},
	}}}
	result := normalizeSummaryFixture(t, e, path, delta, false)
	wantHistogramBuckets(t, "duplicate count/60", histogramSeries(t, e, result.Files, "latency", "count", summaryStart(), 1, 60), unscored(analysis.MissingDuplicateHistogramRow))

	e, path = nativeFixture(t, "SELECT * FROM (VALUES (1700000040, 1.0, 2.0), (1700000040, 3.0, 4.0), (1700000100, 5.0, 6.0)) t(ts, low, total)")
	cumulative := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{
		Value: Column{"total"}, Name: "latency", Kind: analysis.KindHistogram, Unit: "ms",
		Histogram: &analysis.ParquetHistogram{Bounds: []string{"10", "+Inf"}, BucketCounts: analysis.HistogramLeCumulative, Temporality: analysis.HistogramCumulative, CountColumns: []Column{{"low"}}},
	}}}
	refused := normalizeSummaryFixture(t, e, path, cumulative, true)
	if refused.Accepted != 1 || refused.Rejected != 2 {
		t.Fatalf("a repeated wide cumulative instant was admitted: %+v", refused)
	}
}

func itoa(v int) string     { return strconv.Itoa(v) }
func ftoa(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) }
