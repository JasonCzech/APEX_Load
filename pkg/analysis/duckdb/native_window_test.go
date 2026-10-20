//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// Run the same interval cases through source normalization and both query
// implementations. Each whole window contributes 60 observations.
func TestNativeWindowAdmissionAcrossRepresentations(t *testing.T) {
	for _, representation := range []string{analysis.KindSummary, analysis.KindHistogram} {
		for _, tc := range []struct {
			name       string
			offsets    []int
			to, bucket int
			crossFile  bool
			otherHost  bool
			want       []histogramBucket
		}{
			{name: "adjacent windows", offsets: []int{0, 60}, to: 120, bucket: 120, want: []histogramBucket{counted(120)}},
			{name: "overlapping windows", offsets: []int{0, 30}, to: 120, bucket: 120, want: []histogramBucket{unscored("partial")}},
			{name: "overlap across files", offsets: []int{0, 30}, to: 120, bucket: 120, crossFile: true, want: []histogramBucket{unscored("partial")}},
			{name: "clipped query end", offsets: []int{0}, to: 30, bucket: 60, want: []histogramBucket{unscored("partial")}},
			{name: "exact query end", offsets: []int{0}, to: 60, bucket: 120, want: []histogramBucket{counted(60)}},
			{name: "partial final bucket", offsets: []int{0, 60}, to: 90, bucket: 60, want: []histogramBucket{counted(60), unscored("partial")}},
			{name: "lead in ends before first row", offsets: []int{-30, 30}, to: 120, bucket: 120, want: []histogramBucket{unscored("partial")}},
			{name: "unrelated lead in", offsets: []int{-30, 0, 60}, to: 120, bucket: 120, otherHost: true, want: []histogramBucket{counted(120)}},
			{name: "duplicate windows", offsets: []int{0, 0}, to: 60, bucket: 60, want: []histogramBucket{unscored("duplicate")}},
			{name: "sparse whole windows", offsets: []int{0, 120}, to: 240, bucket: 120, want: []histogramBucket{counted(60), counted(60)}},
		} {
			t.Run(representation+"/"+tc.name, func(t *testing.T) {
				var values []string
				for i, offset := range tc.offsets {
					host := "a"
					if tc.otherHost && i == 0 {
						host = "b"
					}
					values = append(values, fmt.Sprintf("('%s',%d,60.0,0.0)", host, summaryStart().Unix()+int64(offset)))
				}
				fixture := func(rows []string) string {
					return "SELECT * FROM (VALUES " + strings.Join(rows, ",") + ") t(host, ts, n, overflow)"
				}
				first := values
				if tc.crossFile {
					first = values[:1]
				}
				e, path := nativeFixture(t, fixture(first))
				files := []string{path}
				if tc.crossFile {
					files = append([]string{nativeExtraFixture(t, e, "later.parquet", fixture(values[1:]))}, files...)
				}
				measure := summaryFixtureMeasure("n", "requests", "count", "count")
				partial, duplicate := analysis.MissingPartialSummaryWindow, analysis.MissingDuplicateSummaryWindow
				if representation == analysis.KindHistogram {
					measure = Measure{Value: Column{"overflow"}, Name: "requests", Kind: analysis.KindHistogram, Unit: "ms", Histogram: &analysis.ParquetHistogram{
						Bounds: []string{"100", "+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []Column{{"n"}},
					}}
					partial, duplicate = analysis.MissingPartialHistogramWindow, analysis.MissingDuplicateHistogramRow
				}
				mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Identity: &analysis.ParquetIdentityMapping{Host: Column{"host"}}, Measures: []Measure{measure}}
				result, err := normalizeConsentedNativeFixture(t, e, context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: files, Mapping: mapping})
				if err != nil {
					t.Fatal(err)
				}
				result = retainNativeFixtureOutputs(t, e, result)
				from := summaryStart()
				points, err := e.Series(context.Background(), result.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "requests", IdentitySchemaVersion: 2, From: from, To: from.Add(time.Duration(tc.to) * time.Second), BucketSeconds: tc.bucket, Aggregation: "count"})
				if err != nil || len(points) != len(tc.want) {
					t.Fatalf("points=%+v err=%v", points, err)
				}
				for i, want := range tc.want {
					point := points[i]
					if point.InputKind != representation {
						t.Fatalf("source representation lost: %+v", point)
					}
					if want.value != nil {
						if point.Quality != analysis.QualityValid || point.Value == nil || *point.Value != *want.value {
							t.Fatalf("bucket %d: want %v, got %+v", i, *want.value, point)
						}
						continue
					}
					reason := partial
					if want.reason == "duplicate" {
						reason = duplicate
					}
					if point.Quality != analysis.QualityMissing || point.Value != nil || point.MissingReason != reason {
						t.Fatalf("bucket %d: want missing %s, got %+v", i, reason, point)
					}
				}
			})
		}
	}
}

func TestNativeWindowAdmissionKeepsLongestEarlierEnd(t *testing.T) {
	e, _ := nativeFixture(t, "SELECT 1 AS fixture")
	for _, tc := range []struct {
		name, windows string
		want          []bool
	}{
		{name: "nested windows", windows: "(0,240),(60,60),(180,60),(240,60)", want: []bool{true, true, true, false}},
		{name: "long lead in", windows: "(-60,300),(0,60),(60,60),(180,60),(240,60)", want: []bool{true, true, true, false}},
		{name: "carryover without row overlap", windows: "(0,100),(100,30),(130,110)", want: []bool{false, true, true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "m", From: summaryStart(), To: summaryStart().Add(360 * time.Second), BucketSeconds: 120, Aggregation: "count"}
			table := "(SELECT 'p' AS project_id, 'd' AS dataset_id, 'm' AS metric_name, to_timestamp(1700000040+start_seconds) AS timestamp_utc, duration_seconds FROM (VALUES " + tc.windows + ") t(start_seconds,duration_seconds))"
			query := nativeWindowAdmissionSQL(table, q, nativeSeriesFilter(q), nativeSeriesLeadInFilter(q), "timestamp_utc+duration_seconds*INTERVAL '1 second'", "1 AS value")
			err := e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
				rows, err := db.QueryContext(ctx, "SELECT partial_window FROM ("+query+") ORDER BY timestamp_utc")
				if err != nil {
					return err
				}
				defer rows.Close()
				var got []bool
				for rows.Next() {
					var partial bool
					if err := rows.Scan(&partial); err != nil {
						return err
					}
					got = append(got, partial)
				}
				if !slices.Equal(got, tc.want) {
					t.Errorf("partial windows=%v, want %v", got, tc.want)
				}
				return rows.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
