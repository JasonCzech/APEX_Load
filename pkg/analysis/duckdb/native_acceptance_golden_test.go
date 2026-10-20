//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Analytical acceptance goldens (NA-6, B8) for the mapping layouts, decimal
// values, schema drift across the files of one import and conflicting files.
// Expected series values come from DuckDB's own SQL over the source files, an
// oracle independent of the normalization and series code under test; the
// counts, the conflicting-file aggregates and the DECIMAL beyond a double's
// precision are fixed expectations derived from the fixtures.

// nativeAcceptanceFile writes another Parquet fixture file beside the
// engine's input.
func nativeAcceptanceFile(t *testing.T, e *Engine, name, query string) string {
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

// nativeAcceptanceOracle evaluates one scalar over source files with DuckDB.
func nativeAcceptanceOracle(t *testing.T, e *Engine, query string) float64 {
	t.Helper()
	var value float64
	if err := e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		return db.QueryRowContext(ctx, query).Scan(&value)
	}); err != nil {
		t.Fatal(err)
	}
	return value
}

// nativeAcceptanceRetain copies a normalization's outputs under a prefix
// (several normalizations share one engine) and releases them.
func nativeAcceptanceRetain(t *testing.T, e *Engine, result NormalizeResult, prefix string) NormalizeResult {
	t.Helper()
	copySet := func(paths []string) []string {
		out := make([]string, 0, len(paths))
		for _, path := range paths {
			reader, err := e.OpenOutput(path)
			if err != nil {
				t.Fatal(err)
			}
			destination := filepath.Join(e.root, prefix+"-"+filepath.Base(path))
			file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
			if err != nil {
				reader.Close()
				t.Fatal(err)
			}
			_, copyErr := io.Copy(file, reader)
			fileCloseErr, readerCloseErr := file.Close(), reader.Close()
			if copyErr != nil || fileCloseErr != nil || readerCloseErr != nil {
				t.Fatal("fixture output copy", copyErr, fileCloseErr, readerCloseErr)
			}
			out = append(out, destination)
		}
		return out
	}
	result.Files = copySet(result.Files)
	result.RejectionFiles = copySet(result.RejectionFiles)
	if err := e.ReleaseOutputs(); err != nil {
		t.Fatal(err)
	}
	return result
}

func nativeAcceptanceDigest(t *testing.T, path string) [32]byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return sha256.Sum256(data)
}

func nativeAcceptanceSeries(t *testing.T, e *Engine, r NormalizeResult, metric, aggregation string, from time.Time, buckets int) []analysis.MetricPoint {
	t.Helper()
	points, err := e.Series(context.Background(), r.Files, SeriesQuery{IdentitySchemaVersion: nativeFixtureIdentityVersion(t, e, r.Files), ProjectID: "p", DatasetID: "d", Metric: metric, From: from, To: from.Add(time.Duration(buckets) * time.Minute), BucketSeconds: 60, Aggregation: aggregation})
	if err != nil || len(points) != buckets {
		t.Fatalf("%s %s: %+v %v", metric, aggregation, points, err)
	}
	return points
}

func nativeAcceptanceValues(points []analysis.MetricPoint) []any {
	out := make([]any, len(points))
	for i, point := range points {
		if point.Value != nil {
			out[i] = *point.Value
		}
	}
	return out
}

// The same measurements as a wide file (a column per metric), a long file (a
// metric-name column) and a nested file (struct fields) normalize into the
// same series, which equal DuckDB's own per-minute aggregates of the source,
// and none of the sources changes.
func TestNativeAcceptanceMappingLayoutsAgree(t *testing.T) {
	const rows = "FROM range(360) t(i)"
	e, wide := nativeFixture(t, "SELECT 1700000040+i AS ts, (100+i%17)::DOUBLE AS latency, (i%5)::DOUBLE AS errors "+rows)
	long := nativeAcceptanceFile(t, e, "long.parquet", "SELECT ts, metric, value FROM (SELECT 1700000040+i AS ts, 'latency' AS metric, (100+i%17)::DOUBLE AS value "+rows+" UNION ALL SELECT 1700000040+i, 'errors', (i%5)::DOUBLE "+rows+")")
	nested := nativeAcceptanceFile(t, e, "nested.parquet", "SELECT 1700000040+i AS ts, {'latency': (100+i%17)::DOUBLE, 'errors': (i%5)::DOUBLE} AS m "+rows)
	mapping := func(measures ...Measure) Mapping {
		return Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: measures}
	}
	layouts := map[string]NormalizeRequest{
		"wide": {Files: []string{wide}, Mapping: mapping(
			Measure{Value: Column{"latency"}, Name: "latency", Kind: analysis.KindGauge, Unit: "ms"},
			Measure{Value: Column{"errors"}, Name: "errors", Kind: analysis.KindGauge, Unit: "ms"})},
		"long": {Files: []string{long}, Mapping: mapping(
			Measure{Value: Column{"value"}, NameColumn: Column{"metric"}, Kind: analysis.KindGauge, Unit: "ms"})},
		"nested": {Files: []string{nested}, Mapping: mapping(
			Measure{Value: Column{"m", "latency"}, Name: "latency", Kind: analysis.KindGauge, Unit: "ms"},
			Measure{Value: Column{"m", "errors"}, Name: "errors", Kind: analysis.KindGauge, Unit: "ms"})},
	}
	// Normalization never writes to its sources.
	sources := map[string][32]byte{}
	for _, path := range []string{wide, long, nested} {
		sources[path] = nativeAcceptanceDigest(t, path)
	}
	t.Cleanup(func() {
		for path, digest := range sources {
			if nativeAcceptanceDigest(t, path) != digest {
				t.Errorf("normalization changed its source %s", filepath.Base(path))
			}
		}
	})
	from := time.Unix(1700000040, 0).UTC()
	aggregations := map[string]string{"avg": "AVG", "sum": "SUM", "count": "COUNT", "max": "MAX"}
	for name, request := range layouts {
		request.ProjectID, request.DatasetID = "p", "d"
		r, err := e.Normalize(context.Background(), request)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if r.Accepted != 720 || r.Rejected != 0 {
			t.Fatalf("%s: accepted %d rejected %d, want 720 and 0", name, r.Accepted, r.Rejected)
		}
		r = nativeAcceptanceRetain(t, e, r, name)
		for _, metric := range []string{"latency", "errors"} {
			for aggregation, function := range aggregations {
				points := nativeAcceptanceSeries(t, e, r, metric, aggregation, from, 6)
				for minute, point := range points {
					want := nativeAcceptanceOracle(t, e, fmt.Sprintf("SELECT %s(%s)::DOUBLE FROM read_parquet(%s) WHERE ts >= %d AND ts < %d", function, metric, literal(wide), 1700000040+60*minute, 1700000100+60*minute))
					if point.Value == nil || math.Abs(*point.Value-want) > 1e-9*math.Max(1, math.Abs(want)) {
						t.Errorf("%s %s %s minute %d = %v, the source gives %v", name, metric, aggregation, minute, nativeAcceptanceValues([]analysis.MetricPoint{point})[0], want)
					}
				}
			}
		}
	}
}

// DECIMAL values become double-precision measurements: per-minute sums equal
// DuckDB's exact DECIMAL sums to double precision, and a value beyond about
// 15 significant digits is rounded to the nearest double (stated, not hidden:
// measurements are doubles). New normalization explicitly acknowledges this
// potential rounding; historical normalized artifacts retain their reader.
func TestNativeAcceptanceDecimalValues(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000040+i AS ts, (i*0.001 + 0.1)::DECIMAL(18,3) AS amount FROM range(360) t(i)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"amount"}, Name: "amount", Kind: analysis.KindGauge, Unit: "count"}}}
	r, err := normalizeConsentedNativeFixture(t, e, context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil || r.Accepted != 360 || r.Rejected != 0 {
		t.Fatalf("decimal normalize: accepted %d rejected %d %v", r.Accepted, r.Rejected, err)
	}
	r = nativeAcceptanceRetain(t, e, r, "decimal")
	from := time.Unix(1700000040, 0).UTC()
	for minute, point := range nativeAcceptanceSeries(t, e, r, "amount", "sum", from, 6) {
		want := nativeAcceptanceOracle(t, e, fmt.Sprintf("SELECT SUM(amount)::DOUBLE FROM read_parquet(%s) WHERE ts >= %d AND ts < %d", literal(path), 1700000040+60*minute, 1700000100+60*minute))
		if point.Value == nil || math.Abs(*point.Value-want) > 1e-9 {
			t.Errorf("minute %d sum = %v, the exact DECIMAL sum is %v", minute, nativeAcceptanceValues([]analysis.MetricPoint{point})[0], want)
		}
	}
	big := nativeAcceptanceFile(t, e, "big.parquet", "SELECT 1700000040 AS ts, 12345678901234567.89::DECIMAL(38,2) AS amount")
	r, err = normalizeConsentedNativeFixture(t, e, context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{big}, Mapping: mapping})
	if err != nil || r.Accepted != 1 {
		t.Fatalf("big decimal: accepted %d %v", r.Accepted, err)
	}
	r = nativeAcceptanceRetain(t, e, r, "big")
	if point := nativeAcceptanceSeries(t, e, r, "amount", "max", from, 1)[0]; point.Value == nil || *point.Value != 12345678901234567.89 {
		t.Fatalf("a DECIMAL beyond double precision = %v, want the nearest double %v", nativeAcceptanceValues([]analysis.MetricPoint{point})[0], float64(12345678901234567.89))
	}
}

// A second file whose schema drifted from the mapping fails the
// normalization closed (nothing is accepted), with or without rejections
// allowed; a text column whose values are numbers is read as numbers.
func TestNativeAcceptanceSchemaDriftAcrossFiles(t *testing.T) {
	e, first := nativeFixture(t, "SELECT 1700000040+i AS ts, (100+i)::DOUBLE AS latency FROM range(60) t(i)")
	missing := nativeAcceptanceFile(t, e, "missing.parquet", "SELECT 1700000100+i AS ts, (200+i)::DOUBLE AS latency_ms FROM range(60) t(i)")
	text := nativeAcceptanceFile(t, e, "text.parquet", "SELECT 1700000100+i AS ts, (200+i)::VARCHAR AS latency FROM range(60) t(i)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"latency"}, Name: "latency", Kind: analysis.KindGauge, Unit: "ms"}}}
	for _, allow := range []bool{false, true} {
		r, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{first, missing}, Mapping: mapping, AllowRejected: allow})
		if err == nil || r.Accepted != 0 || len(r.Files) != 0 {
			t.Fatalf("a file without the mapped column (allow rejected %v) was normalized: %+v %v", allow, r, err)
		}
		if err := e.ReleaseOutputs(); err != nil {
			t.Fatal(err)
		}
	}
	r, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{first, text}, Mapping: mapping})
	if err != nil || r.Accepted != 120 || r.Rejected != 0 {
		t.Fatalf("numeric text: accepted %d rejected %d %v", r.Accepted, r.Rejected, err)
	}
	r = nativeAcceptanceRetain(t, e, r, "text")
	from := time.Unix(1700000040, 0).UTC()
	sums := nativeAcceptanceValues(nativeAcceptanceSeries(t, e, r, "latency", "sum", from, 2))
	want := []any{nativeAcceptanceOracle(t, e, "SELECT SUM(latency)::DOUBLE FROM read_parquet("+literal(first)+")"), nativeAcceptanceOracle(t, e, "SELECT SUM(latency::DOUBLE) FROM read_parquet("+literal(text)+")")}
	if !reflect.DeepEqual(sums, want) {
		t.Fatalf("numeric text sums %v, the sources give %v", sums, want)
	}
}

// Two files with different values for the same series and instants keep
// every observation: nothing is overwritten or dropped, so each minute counts
// both files and averages their values.
func TestNativeAcceptanceConflictingFilesKeepEveryObservation(t *testing.T) {
	e, first := nativeFixture(t, "SELECT 1700000040+i AS ts, 100::DOUBLE AS latency FROM range(60) t(i)")
	conflicting := nativeAcceptanceFile(t, e, "conflicting.parquet", "SELECT 1700000040+i AS ts, 300::DOUBLE AS latency FROM range(60) t(i)")
	mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"latency"}, Name: "latency", Kind: analysis.KindGauge, Unit: "ms"}}}
	r, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{first, conflicting}, Mapping: mapping})
	if err != nil || r.Accepted != 120 {
		t.Fatalf("conflicting files: accepted %d %v", r.Accepted, err)
	}
	r = nativeAcceptanceRetain(t, e, r, "conflicting")
	from := time.Unix(1700000040, 0).UTC()
	for aggregation, want := range map[string]float64{"count": 120, "avg": 200, "sum": 24000, "max": 300} {
		if got := nativeAcceptanceSeries(t, e, r, "latency", aggregation, from, 1)[0]; got.Value == nil || *got.Value != want {
			t.Errorf("%s = %v, want %v", aggregation, nativeAcceptanceValues([]analysis.MetricPoint{got})[0], want)
		}
	}
}
