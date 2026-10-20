//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativeAbsoluteTimestampRequiresSourceOffset(t *testing.T) {
	e, path := nativeFixture(t, `SELECT * FROM (VALUES
 ('2026-11-01T01:30:00-04:00',1),
 ('2026-11-01T01:30:00-05:00',2),
 ('2026-11-01T01:30:00',3),
 ('2026-11-01',4),
 ('2026-11-01T05:30:00Z',5),
 ('2026-11-01 01:30:00-04',6),
 ('2026-11-01T01:30:00-0400',7),
 (NULL,8),
 ('2026-11-01 01:30:00 -04:00',9)) t(ts,value)`)
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp_offset", Timezone: "America/Los_Angeles", Measures: []Measure{{Name: "v", Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}}}
	request := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping}
	refused, err := e.Normalize(context.Background(), request)
	if err == nil || len(refused.Files) != 0 {
		t.Fatalf("absolute timestamp refusal: %+v %v", refused, err)
	}
	// The pinned parser also rejects the spaced offset text (row 8);
	// classification must not turn a parser refusal into a guessed instant.
	request.AllowRejected = true
	result, err := e.Normalize(context.Background(), request)
	if err != nil || result.Accepted != 5 || result.Rejected != 4 || len(result.RejectionFiles) != 1 || len(result.Rejections) != 1 || !reflect.DeepEqual(result.Rejections[0].Rows, []int64{2, 3, 7, 8}) {
		t.Fatalf("explicit rejection acceptance: %+v %v", result, err)
	}
	result = retainNativeFixtureOutputs(t, e, result)
	// The repeated local clock time has two distinct absolute instants in the
	// same source file. The mapping timezone cannot override either offset.
	err = e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, "SELECT timestamp_utc FROM read_parquet("+literal(result.Files[0])+") ORDER BY value")
		if err != nil {
			return err
		}
		defer rows.Close()
		want := []string{"2026-11-01T05:30:00Z", "2026-11-01T06:30:00Z", "2026-11-01T05:30:00Z", "2026-11-01T05:30:00Z", "2026-11-01T05:30:00Z"}
		i := 0
		for rows.Next() {
			var got time.Time
			if err := rows.Scan(&got); err != nil {
				return err
			}
			if i >= len(want) || got.UTC().Format(time.RFC3339) != want[i] {
				t.Errorf("row %d time %s", i, got)
			}
			i++
		}
		if i != len(want) {
			t.Errorf("got %d rows", i)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if sha256.Sum256(original) != sha256.Sum256(current) {
		t.Fatal("source bytes changed")
	}
}

func TestNativeTimestampUsesEachRowsTimezoneOffset(t *testing.T) {
	for _, tc := range []struct {
		name, encoding, sql string
		want                []string
	}{
		{"spring local", "timestamp", `SELECT * FROM (VALUES (TIMESTAMP '2026-03-08 01:30:00',1),(TIMESTAMP '2026-03-08 03:30:00',2)) t(ts,value)`, []string{"2026-03-08T06:30:00Z", "2026-03-08T07:30:00Z"}},
		{"fall local", "timestamp", `SELECT * FROM (VALUES (TIMESTAMP '2026-11-01 00:30:00',1),(TIMESTAMP '2026-11-01 02:30:00',2)) t(ts,value)`, []string{"2026-11-01T04:30:00Z", "2026-11-01T07:30:00Z"}},
		{"typed absolute", "timestamp_offset", `SELECT TIMESTAMPTZ '2026-11-01 01:30:00-04' AS ts,1 AS value`, []string{"2026-11-01T05:30:00Z"}},
		{"typed absolute local mapping", "timestamp", `SELECT TIMESTAMPTZ '2026-11-01 01:30:00-04' AS ts,1 AS value`, []string{"2026-11-01T05:30:00Z"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, path := nativeFixture(t, tc.sql)
			m := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: tc.encoding, Timezone: "America/New_York", Measures: []Measure{{Name: "v", Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}}}
			result, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m})
			if err != nil {
				t.Fatal(err)
			}
			result = retainNativeFixtureOutputs(t, e, result)
			err = e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
				rows, err := db.QueryContext(ctx, "SELECT timestamp_utc FROM read_parquet("+literal(result.Files[0])+") ORDER BY value")
				if err != nil {
					return err
				}
				defer rows.Close()
				i := 0
				for rows.Next() {
					var got time.Time
					if err := rows.Scan(&got); err != nil {
						return err
					}
					if i >= len(tc.want) || got.UTC().Format(time.RFC3339) != tc.want[i] {
						t.Errorf("row %d time %s", i, got)
					}
					i++
				}
				if i != len(tc.want) {
					t.Errorf("got %d rows", i)
				}
				return rows.Err()
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// One-digit clock fields and a UTC suffix are explicit source offsets the
// pinned parser accepts; an offset mapping admits each at its stated instant.
func TestNativeAbsoluteTimestampAcceptsShortClockFieldsAndUTCSuffix(t *testing.T) {
	e, path := nativeFixture(t, `SELECT * FROM (VALUES
 ('2026-03-10T9:30:00Z',1),
 ('2026-03-10 9:30:00-04:00',2),
 ('2026-03-10 09:5:7+0530',3),
 ('2026-03-10 09:30:00 utc',4),
 ('2026-03-10T09:30:00+05:00' || chr(13),5),
 ('2026-03-10' || chr(9) || '09:30:00+05:00',6),
 ('2026-03-10 09:30:00+05:',7),
 ('2026-03-10 09:30:00 UTC' || chr(10),8),
 ('2026-03-10T009:30:00Z',9),
 ('2026-03-10 09:30:00.+05:00',10)) t(ts,value)`)
	mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp_offset", Timezone: "America/Los_Angeles", Measures: []Measure{{Name: "v", Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}}}
	result, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping})
	if err != nil || result.Accepted != 10 {
		t.Fatalf("explicit offsets refused: %+v %v", result, err)
	}
	result = retainNativeFixtureOutputs(t, e, result)
	err = e.withDB(context.Background(), func(ctx context.Context, db *sql.DB) error {
		rows, err := db.QueryContext(ctx, "SELECT timestamp_utc FROM read_parquet("+literal(result.Files[0])+") ORDER BY value")
		if err != nil {
			return err
		}
		defer rows.Close()
		want := []string{"2026-03-10T09:30:00Z", "2026-03-10T13:30:00Z", "2026-03-10T03:35:07Z", "2026-03-10T09:30:00Z", "2026-03-10T04:30:00Z", "2026-03-10T04:30:00Z", "2026-03-10T04:30:00Z", "2026-03-10T09:30:00Z", "2026-03-10T09:30:00Z", "2026-03-10T04:30:00Z"}
		i := 0
		for rows.Next() {
			var got time.Time
			if err := rows.Scan(&got); err != nil {
				return err
			}
			if i >= len(want) || got.UTC().Format(time.RFC3339) != want[i] {
				t.Errorf("row %d time %s", i, got)
			}
			i++
		}
		if i != len(want) {
			t.Errorf("got %d rows", i)
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNativeLocalTimestampRejectsShortOffsetText(t *testing.T) {
	// DuckDB also accepts one-digit clock fields before an offset.
	// Trailing tabs and line breaks, a tab separator and a dangling colon after
	// the offset hours are offsets too, not offset-free wall text.
	for _, text := range []string{"2026-11-01 01:30:00-04", "2026-11-01 01:30:00 -04:00", "2026-03-10T9:30:00Z", "2026-03-10 9:30:00-04:00", "2026-03-10T9:30Z", "2026-03-10 09:5:7+0530", "2026-03-10T09:30:00.5+5", "2026-03-10 09:30:00+05:30:00", "2026-03-10 09:30:00 UTC", "2026-03-10 09:30:00z",
		"2026-03-10T09:30:00+05:00\t", "2026-03-10T09:30:00+05:00\r", "2026-03-10T09:30:00+05:00\n", "2026-03-10\t09:30:00+05:00", "2026-03-10 09:30:00+05:", "2026-03-10 09:30:00 UTC\r",
		"2026-03-10T009:30:00Z", "2026-03-10 09:30:00.+05:00", "2026-03-10 09:30:00. UTC"} {
		t.Run(text, func(t *testing.T) {
			e, path := nativeFixture(t, "SELECT "+literal(text)+" AS ts,1 AS value")
			m := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp", Timezone: "UTC", Measures: []Measure{{Name: "v", Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}}}
			result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m})
			if err == nil || len(result.Files) != 0 {
				t.Fatalf("offset silently discarded: %+v %v", result, err)
			}
		})
	}
}

func TestNativeAbsoluteTimestampRejectsNaiveTypedColumns(t *testing.T) {
	for _, expression := range []string{"TIMESTAMP '2026-11-01 01:30:00'", "DATE '2026-11-01'"} {
		t.Run(expression, func(t *testing.T) {
			e, path := nativeFixture(t, "SELECT "+expression+" AS ts,1 AS value")
			m := Mapping{Version: 1, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp_offset", Timezone: "America/New_York", Measures: []Measure{{Name: "v", Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}}}
			result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m, AllowRejected: true})
			if err != nil || result.Accepted != 0 || result.Rejected != 1 || len(result.Rejections) != 1 || !reflect.DeepEqual(result.Rejections[0].Rows, []int64{0}) {
				t.Fatalf("naive typed absolute timestamp: %+v %v", result, err)
			}
		})
	}
}
