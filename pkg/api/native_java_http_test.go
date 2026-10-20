package api

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

func nativeJavaFixture(t *testing.T, values ...*protocol.JavaHTTPDiagnostics) *storage.RunArchiveRecord {
	t.Helper()
	record, start := nativeSeriesFixture(t)
	f, err := os.Create(filepath.Join(record.ArchivePath, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i, d := range values {
		tx := TransactionEvent{Timestamp: start.Add(time.Duration(i) * time.Second), ActionName: "request", Population: "action", Success: true, ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: d}}
		if err = json.NewEncoder(f).Encode(tx); err != nil {
			t.Fatal(err)
		}
	}
	return record
}
func nativeJavaDiagnostic(encoded, decoded int64) *protocol.JavaHTTPDiagnostics {
	d := javaAPIDiagnostic()
	zero := 0.0
	d.InputState = "decoded"
	d.RequestEncodedBytes = encoded
	d.RequestDecodedBytes = &decoded
	d.PreparedRequestBodyBytes = &decoded
	d.Base64DecodeMs = &zero
	return d
}
func TestJavaHTTPNativeArchiveValuesPairsMissingAndLegacy(t *testing.T) {
	q := storage.AnalyticsQuery{Population: analysis.PopulationJavaHTTPAction, Metric: "java_http_decoded_per_encoded", Aggregation: "ratio_of_sums", EndSeconds: 20, BucketSeconds: 10, Filters: map[string]string{"action": "request"}}
	a, b := nativeJavaDiagnostic(8, 4), nativeJavaDiagnostic(4, 3)
	if !a.Valid() || !b.Valid() {
		t.Fatal("fixture invalid")
	}
	series, err := buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, a, b), q)
	if err != nil || series.Buckets[0].Value == nil || math.Abs(*series.Buckets[0].Value-7.0/12) > 1e-12 || series.Buckets[0].Count != 2 {
		t.Fatal("ratio must use paired sums", series, err)
	}
	q.Metric = "java_http_retained_response_bytes"
	q.Aggregation = "avg"
	series, err = buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, a, b), q)
	if err != nil || series.Buckets[0].Value == nil || *series.Buckets[0].Value != 0 || series.Buckets[1].Value != nil {
		t.Fatal("observed zero or gap changed", series, err)
	}
	for _, missing := range []*protocol.JavaHTTPDiagnostics{nil, {Version: 99}} {
		series, err = buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, a, missing), q)
		if err != nil || series.Buckets[0].Value != nil || series.Buckets[0].Count != 1 || series.Buckets[0].Reason != "incomplete_representation_evidence" || len(series.Warnings) == 0 {
			t.Fatal("missing peer diluted or scored", series, err)
		}
	}
	q.Metric = "java_http_prepared_body_bytes"
	b.AppliedRequestCompression = "gzip"
	series, err = buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, a, b), q)
	if err != nil || series.Buckets[0].Value != nil || len(series.Warnings) == 0 {
		t.Fatal("mixed compression scored", series, err)
	}
	series, err = buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, b), q)
	if err != nil || series.JavaHTTPCompression != "gzip" || series.Unit != "bytes" {
		t.Fatal("cohort or unit missing", series, err)
	}
	q.Metric = "java_http_base64_per_retained"
	q.Aggregation = "ratio_of_sums"
	series, err = buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, a), q)
	if err != nil || series.Buckets[0].Value != nil || series.Buckets[0].Reason != "zero_denominator" {
		t.Fatal("empty ratio became zero", series, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = buildNativeArchiveSeries(ctx, nativeJavaFixture(t, a), q); err == nil {
		t.Fatal("cancel ignored")
	}
}
func TestJavaHTTPNativeArchiveMeasurementAvailability(t *testing.T) {
	d := javaAPIDiagnostic()
	for _, metric := range []string{"java_http_decoded_request_bytes", "java_http_prepared_body_bytes", "java_http_retained_response_bytes", "java_http_response_base64_bytes", "java_http_base64_encode_ms"} {
		n, _, ok := nativeJavaHTTPValue(d, metric)
		if !ok || n != 0 {
			t.Fatal("measured zero lost", metric, n, ok)
		}
	}
	if _, _, ok := nativeJavaHTTPValue(d, "java_http_base64_decode_ms"); ok {
		t.Fatal("absent empty-body decode became measurement")
	}
	if _, _, ok := nativeJavaHTTPValue(d, "java_http_base64_work_share"); ok {
		t.Fatal("unpaired codec work scored")
	}
	q := storage.AnalyticsQuery{Population: analysis.PopulationJavaHTTPAction, Metric: "java_http_retained_response_bytes", Aggregation: "avg", EndSeconds: 20, BucketSeconds: 10, Filters: map[string]string{"action": "request"}}
	d.ResponseState = "collection_window"
	series, err := buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, d), q)
	if err != nil || series.Buckets[0].Value != nil || series.Excluded["response_collection_window"] != 1 {
		t.Fatal("collection-window response qualified", series, err)
	}
}

func TestJavaHTTPNativeSourceCoverageRemainsUnqualified(t *testing.T) {
	record := nativeJavaFixture(t, javaAPIDiagnostic())
	body, err := os.ReadFile(record.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(body, &manifest); err != nil {
		t.Fatal(err)
	}
	manifest.SourceCoverage = &RunArchiveSourceCoverage{Mode: "required", Sources: []RunArchiveSource{{Target: "synthetic-generator", Collected: true}}}
	body, err = json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record.ManifestPath, body, 0600); err != nil {
		t.Fatal(err)
	}
	q := storage.AnalyticsQuery{Population: analysis.PopulationJavaHTTPAction, Metric: "java_http_retained_response_bytes", Aggregation: "avg", EndSeconds: 20, BucketSeconds: 10, Filters: map[string]string{"action": "request"}}
	series, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil {
		t.Fatal(err)
	}
	if series.Buckets[0].Value != nil || series.Buckets[0].Count != 1 || series.Buckets[0].Reason != "incomplete_representation_evidence" {
		t.Fatal("collection presence was treated as source-window qualification", series)
	}
}

func TestJavaHTTPNativeAllCatalogMeasurementsUseTheirOwnValuesAndUnits(t *testing.T) {
	a, b := nativeJavaDiagnostic(8, 4), nativeJavaDiagnostic(4, 3)
	preparedA, preparedB, retainedA, retainedB, base64A, base64B := int64(6), int64(3), int64(3), int64(6), int64(4), int64(8)
	decodeA, decodeB, encodeA, encodeB := 2.0, 4.0, 3.0, 6.0
	a.PreparedRequestBodyBytes, b.PreparedRequestBodyBytes = &preparedA, &preparedB
	a.AppliedRequestCompression, b.AppliedRequestCompression = "gzip", "gzip"
	a.ResponseRetainedBytes, b.ResponseRetainedBytes = &retainedA, &retainedB
	a.ResponseBase64Bytes, b.ResponseBase64Bytes = &base64A, &base64B
	a.Base64DecodeMs, b.Base64DecodeMs, a.Base64EncodeMs, b.Base64EncodeMs = &decodeA, &decodeB, &encodeA, &encodeB
	a.EnclosingMs, b.EnclosingMs = 10, 20
	if !a.Valid() || !b.Valid() {
		t.Fatal("invalid numeric fixture")
	}
	record := nativeJavaFixture(t, a, b)
	for _, tc := range []struct {
		metric, unit    string
		mean, low, high float64
		paired          bool
	}{
		{"java_http_input_bytes", "bytes", 6, 4, 8, false},
		{"java_http_decoded_request_bytes", "bytes", 3.5, 3, 4, false},
		{"java_http_prepared_body_bytes", "bytes", 4.5, 3, 6, false},
		{"java_http_retained_response_bytes", "bytes", 4.5, 3, 6, false},
		{"java_http_response_base64_bytes", "bytes", 6, 4, 8, false},
		{"java_http_base64_decode_ms", "ms", 3, 2, 4, false},
		{"java_http_base64_encode_ms", "ms", 4.5, 3, 6, false},
		{"java_http_enclosing_ms", "ms", 15, 10, 20, false},
		{"java_http_decoded_per_encoded", "ratio", 7.0 / 12, 0, 0, true},
		{"java_http_prepared_per_decoded", "ratio", 9.0 / 7, 0, 0, true},
		{"java_http_base64_per_retained", "ratio", 12.0 / 9, 0, 0, true},
		{"java_http_base64_work_share", "ratio", .5, 0, 0, true},
	} {
		aggregations := map[string]float64{"avg": tc.mean, "min": tc.low, "max": tc.high, "p50": tc.low, "p90": tc.high, "p95": tc.high, "p99": tc.high}
		if tc.paired {
			aggregations = map[string]float64{"ratio_of_sums": tc.mean}
		}
		for aggregation, want := range aggregations {
			t.Run(tc.metric+"/"+aggregation, func(t *testing.T) {
				q := storage.AnalyticsQuery{Population: analysis.PopulationJavaHTTPAction, Metric: tc.metric, Aggregation: aggregation, EndSeconds: 20, BucketSeconds: 10, Filters: map[string]string{"action": "request"}}
				series, err := buildNativeArchiveSeries(context.Background(), record, q)
				if err != nil {
					t.Fatal(err)
				}
				if series.Unit != tc.unit || series.Buckets[0].Value == nil || math.Abs(*series.Buckets[0].Value-want) > 1e-12 || series.Buckets[0].Count != 2 || series.Buckets[1].Value != nil || series.Buckets[1].Count != 0 {
					t.Fatalf("wrong measurement population/value/unit: %+v want %g %s", series, want, tc.unit)
				}
			})
		}
	}
}
