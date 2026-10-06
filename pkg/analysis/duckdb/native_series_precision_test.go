//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativeSeriesSubmicrosecondBoundsPreserveAuthoritativeBuckets(t *testing.T) {
	for _, at := range []time.Time{time.Date(2026, 1, 1, 0, 0, 0, 123456000, time.UTC), time.Unix(-4, 123456000).UTC()} {
		t.Run(at.Format(time.RFC3339Nano), func(t *testing.T) {
			selectSQL := "SELECT " + literal(at.Format(time.RFC3339Nano)) + " AS ts, 7::DOUBLE AS value UNION ALL SELECT " + literal(at.Add(time.Second).Format(time.RFC3339Nano)) + ", NULL::DOUBLE UNION ALL SELECT " + literal(at.Add(2*time.Second).Format(time.RFC3339Nano)) + ", 11::DOUBLE"
			e, path := nativeFixture(t, selectSQL)
			mapping := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp_offset", Timezone: "UTC", Measures: []Measure{{Name: "value", Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}}}
			result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
			if err != nil {
				t.Fatal(err)
			}
			result = retainNativeFixtureOutputs(t, e, result)
			for _, tc := range []struct {
				name               string
				fromDelta, toDelta time.Duration
			}{
				{"from-plus-1ns", time.Nanosecond, 0}, {"from-minus-1ns", -time.Nanosecond, 0},
				{"to-plus-1ns", 0, time.Nanosecond}, {"to-minus-1ns", 0, -time.Nanosecond},
				{"both-plus-1ns", time.Nanosecond, time.Nanosecond}, {"both-minus-1ns", -time.Nanosecond, -time.Nanosecond},
				{"offset-zone-plus-1ns", time.Nanosecond, time.Nanosecond},
			} {
				t.Run(tc.name, func(t *testing.T) {
					q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "value", From: at.Add(tc.fromDelta), To: at.Add(3*time.Second + tc.toDelta), BucketSeconds: 1, Aggregation: "avg"}
					if tc.name == "offset-zone-plus-1ns" {
						q.From, q.To = q.From.In(time.FixedZone("query", 7200)), q.To.In(time.FixedZone("query", 7200))
					}
					var canonicalFrom, canonicalTo time.Time
					if err = e.withDB(t.Context(), func(ctx context.Context, db *sql.DB) error {
						return db.QueryRowContext(ctx, "SELECT TIMESTAMPTZ "+literal(q.From.Format(time.RFC3339Nano))+",TIMESTAMPTZ "+literal(q.To.Format(time.RFC3339Nano))).Scan(&canonicalFrom, &canonicalTo)
					}); err != nil {
						t.Fatal(err)
					}
					canonicalFrom, canonicalTo = canonicalFrom.UTC(), canonicalTo.UTC()
					if !canonicalFrom.Equal(q.From.UTC().Truncate(time.Microsecond)) || !canonicalTo.Equal(q.To.UTC().Truncate(time.Microsecond)) {
						t.Fatal("Go canonical endpoints differ from actual pinned native casts", canonicalFrom, canonicalTo)
					}
					expectedTimes := []time.Time{}
					for bucket := canonicalFrom; bucket.Before(canonicalTo); bucket = bucket.Add(time.Second) {
						expectedTimes = append(expectedTimes, bucket)
					}
					points, err := e.Series(t.Context(), result.Files, q)
					if err != nil {
						t.Fatal(err)
					}
					if len(points) != len(expectedTimes) {
						t.Fatalf("raw nanosecond bounds invented/lost terminal buckets: actual=%d canonical=%d", len(points), len(expectedTimes))
					}
					for i, p := range points {
						if !p.TimestampUTC.Equal(expectedTimes[i]) || p.TimestampUTC.Location() != time.UTC || p.TimestampUTC.Nanosecond()%1000 != 0 {
							t.Fatal("returned noncanonical bucket origin", i, p.TimestampUTC, expectedTimes[i])
						}
						if i == 0 || i == 2 {
							expected := 7.0
							if i == 2 {
								expected = 11
							}
							if p.Value == nil || *p.Value != expected || p.Quality != analysis.QualityValid {
								t.Fatalf("physical observed sample erased by nanosecond gap fill: bucket=%d point=%+v", i, p)
							}
						} else if p.Value != nil || p.Quality != analysis.QualityMissing {
							t.Fatal("physical/terminal missing sample invented a value", i, p)
						}
					}
				})
			}
			t.Run("collapsed-native-window", func(t *testing.T) {
				q := SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "value", From: at.Add(time.Nanosecond), To: at.Add(2 * time.Nanosecond), BucketSeconds: 1, Aggregation: "avg"}
				points, err := e.Series(t.Context(), result.Files, q)
				if err != nil || len(points) != 0 {
					t.Fatal("empty canonical native window invented evidence", points, err)
				}
			})
		})
	}
}
