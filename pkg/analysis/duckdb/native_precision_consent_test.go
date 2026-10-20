//go:build analytics_duckdb && cgo

package duckdb

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

func nativePrecisionReview(t *testing.T, mapping Mapping, paths ...string) *analysis.ParquetPrecisionReview {
	t.Helper()
	inputs := make([]analysis.ParquetPrecisionInput, len(paths))
	for i, path := range paths {
		schema, err := analysis.InspectParquet(path)
		if err != nil {
			t.Fatal(err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		h := sha256.New()
		_, err = io.Copy(h, f)
		closeErr := f.Close()
		if err != nil || closeErr != nil {
			t.Fatalf("hash: %v / %v", err, closeErr)
		}
		inputs[i] = analysis.ParquetPrecisionInput{SHA256: hex.EncodeToString(h.Sum(nil)), Schema: schema}
	}
	review, err := analysis.ReviewParquetPrecision(mapping, inputs)
	if err != nil {
		t.Fatal(err)
	}
	return review
}

func TestNativePrecisionConsentBindsRealDecimalInputs(t *testing.T) {
	e, path := nativeFixture(t, "SELECT 1700000040::BIGINT AS ts, 12345678901234567.89::DECIMAL(38,2) AS amount")
	legacy := Mapping{Version: 2, Timestamp: Column{"ts"}, TimestampEncoding: "unix_s", Timezone: "UTC", Measures: []Measure{{Value: Column{"amount"}, Name: "amount", Kind: analysis.KindGauge, Unit: "count"}}}
	mapping := legacy
	mapping.Version = 3
	mapping.PrecisionPolicy = analysis.PrecisionAllowFloat64Rounding
	review := nativePrecisionReview(t, mapping, path)
	if len(review.Risks) != 1 || review.Risks[0].Precision != 38 || review.Risks[0].Scale != 2 {
		t.Fatalf("missing schema-level risk: %+v", review)
	}
	for _, tc := range []struct {
		name    string
		mapping Mapping
		ack     string
	}{
		{"legacy-new-lossy-work", legacy, ""},
		{"missing-choice", mapping, ""},
		{"foreign-review", mapping, strings.Repeat("a", 64)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: tc.mapping, PrecisionAcknowledgement: tc.ack})
			if err == nil || len(r.Files) != 0 {
				t.Fatalf("unconsented normalization: %+v %v", r, err)
			}
		})
	}
	rejected := mapping
	rejected.PrecisionPolicy = analysis.PrecisionRejectPotentialLoss
	rejectedReview := nativePrecisionReview(t, rejected, path)
	if _, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: rejected, PrecisionAcknowledgement: rejectedReview.SHA256}); err == nil {
		t.Fatal("reject policy accepted lossy decimal")
	}
	r, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{path}, Mapping: mapping, PrecisionAcknowledgement: review.SHA256})
	if err != nil || r.Accepted != 1 || len(r.Partitions) != 1 || r.Partitions[0].Stats.IdentityVersion != 2 {
		t.Fatalf("explicit consent changed identity contract: %+v %v", r, err)
	}
	r = retainNativeFixtureOutputs(t, e, r)
	points, err := e.Series(context.Background(), r.Files, SeriesQuery{ProjectID: "p", DatasetID: "d", Metric: "amount", IdentitySchemaVersion: 2, From: time.Unix(1700000040, 0), To: time.Unix(1700000100, 0), BucketSeconds: 60, Aggregation: "max"})
	if err != nil || len(points) != 1 || points[0].Value == nil || *points[0].Value != float64(12345678901234567.89) {
		t.Fatalf("consented nearest-double result: %+v %v", points, err)
	}
	if got := nativePrecisionReview(t, mapping, path).SHA256; got != review.SHA256 {
		t.Fatal("normalization mutated source")
	}
	changed := nativeAcceptanceFile(t, e, "changed.parquet", "SELECT 1700000040::BIGINT AS ts, 12345678901234568.89::DECIMAL(38,2) AS amount")
	if _, err := e.Normalize(context.Background(), NormalizeRequest{ProjectID: "p", DatasetID: "d", Files: []string{changed}, Mapping: mapping, PrecisionAcknowledgement: review.SHA256}); err == nil {
		t.Fatal("old acknowledgement accepted changed source")
	}
}
