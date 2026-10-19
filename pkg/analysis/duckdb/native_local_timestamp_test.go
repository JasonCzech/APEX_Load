//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"crypto/sha256"
	"database/sql/driver"
	"errors"
	"os"
	"reflect"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func TestNativeLocalWallTimeRejectsAmbiguityWithRowProvenance(t *testing.T) {
	for _, tc := range []struct{ name, zone, before, bad, after string }{
		{"spring gap", "America/New_York", "2026-03-08 01:59:59.999999", "2026-03-08 02:30:00", "2026-03-08 03:00:00"},
		{"fall fold", "America/New_York", "2026-11-01 00:59:59", "2026-11-01 01:30:00", "2026-11-01 02:00:00"},
		{"half hour fold", "Australia/Lord_Howe", "2026-04-05 01:29:59", "2026-04-05 01:45:00", "2026-04-05 02:00:00"},
		{"half hour gap", "Australia/Lord_Howe", "2026-10-04 01:59:59", "2026-10-04 02:15:00", "2026-10-04 02:30:00"},
		{"repeated date", "Pacific/Kwajalein", "1969-09-29 12:00:00", "1969-09-30 12:00:00", "1969-10-01 12:00:00"},
		{"skipped date", "Pacific/Apia", "2011-12-29 12:00:00", "2011-12-30 12:00:00", "2011-12-31 12:00:00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e, path := nativeFixture(t, "SELECT * FROM (VALUES ("+literal(tc.before)+",1),("+literal(tc.bad)+",2),("+literal(tc.after)+",3)) t(ts,value)")
			original, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			mapping := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp", Timezone: tc.zone, Measures: []Measure{{Name: "v", Value: Column{"value"}, Kind: analysis.KindGauge, Unit: "count"}}}
			request := NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping}
			refused, err := e.Normalize(t.Context(), request)
			if err == nil || len(refused.Files) != 0 {
				t.Fatalf("ambiguous input published: %+v %v", refused, err)
			}
			request.AllowRejected = true
			result, err := e.Normalize(t.Context(), request)
			if err != nil || result.Accepted != 2 || result.Rejected != 1 || len(result.RejectionFiles) != 1 || len(result.Rejections) != 1 || !reflect.DeepEqual(result.Rejections[0].Rows, []int64{1}) {
				t.Fatalf("row provenance: %+v %v", result, err)
			}
			current, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if sha256.Sum256(current) != sha256.Sum256(original) {
				t.Fatal("source bytes changed")
			}
		})
	}
}

func TestNativeLocalTimestampQualifierRequiresAgreementAndCancellation(t *testing.T) {
	q := &nativeLocalTimestampQualifier{location: time.UTC}
	execute := q.Executor().RowContextExecutor
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		args []driver.Value
		want bool
	}{
		{"unique", []driver.Value{at, at}, true},
		{"timezone database disagreement", []driver.Value{at, at.Add(time.Hour)}, false},
		{"missing source", []driver.Value{nil, at}, false},
		{"missing conversion", []driver.Value{at, nil}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := execute(t.Context(), tc.args)
			if err != nil || got != tc.want {
				t.Fatalf("%v %v", got, err)
			}
		})
	}
	if _, err := execute(t.Context(), []driver.Value{at}); !errors.Is(err, errWorkerProtocol) {
		t.Fatalf("malformed args: %v", err)
	}
	if _, err := execute(t.Context(), []driver.Value{"untrusted", at}); !errors.Is(err, errWorkerProtocol) {
		t.Fatalf("malformed type: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := execute(ctx, []driver.Value{at, at}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled: %v", err)
	}
}

func TestNativeLocalTimestampGuardCoversSummaryAndHistogram(t *testing.T) {
	for _, kind := range []string{analysis.KindSummary, analysis.KindHistogram} {
		t.Run(kind, func(t *testing.T) {
			e, path := nativeFixture(t, "SELECT ts,10.0 AS value,2.0 AS fast,1.0 AS overflow FROM (VALUES ('2026-03-08 01:30:00'),('2026-03-08 02:30:00'),('2026-03-08 03:30:00')) t(ts)")
			measure := Measure{Name: "latency", Value: Column{"value"}, Kind: kind, Unit: "ms"}
			if kind == analysis.KindSummary {
				measure.Statistic = "p95"
				measure.WindowSeconds = 60
			} else {
				measure.Value = Column{"overflow"}
				measure.Histogram = &analysis.ParquetHistogram{Bounds: []string{"100", "+Inf"}, BucketCounts: analysis.HistogramPerBin, Temporality: analysis.HistogramDelta, WindowSeconds: 60, CountColumns: []Column{{"fast"}}}
			}
			m := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "timestamp", Timezone: "America/New_York", Measures: []Measure{measure}}
			result, err := e.Normalize(t.Context(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: m, AllowRejected: true})
			if err != nil || result.Accepted != 2 || result.Rejected != 1 || len(result.Rejections) != 1 || !reflect.DeepEqual(result.Rejections[0].Rows, []int64{1}) {
				t.Fatalf("%s provenance: %+v %v", kind, result, err)
			}
		})
	}
}
