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
	events := make([]TransactionEvent, len(values))
	for i, d := range values {
		events[i] = TransactionEvent{DependencyType: "java_http", ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: d}}
	}
	return nativeJavaEventsFixture(t, events...)
}
func nativeJavaEventsFixture(t *testing.T, events ...TransactionEvent) *storage.RunArchiveRecord {
	t.Helper()
	record, start := nativeSeriesFixture(t)
	f, err := os.Create(filepath.Join(record.ArchivePath, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i, tx := range events {
		tx.Timestamp, tx.ActionName, tx.Population, tx.Success = start.Add(time.Duration(i)*time.Second), "request", "action", true
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

// A same-named action of another protocol is outside the Java population, so
// it neither dilutes nor refuses the Java bucket.
func TestJavaHTTPNativeArchiveIgnoresSameNamedOtherProtocolActions(t *testing.T) {
	q := storage.AnalyticsQuery{Population: analysis.PopulationJavaHTTPAction, Metric: "java_http_decoded_per_encoded", Aggregation: "ratio_of_sums", EndSeconds: 20, BucketSeconds: 10, Filters: map[string]string{"action": "request"}}
	java := TransactionEvent{DependencyType: "java_http", ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: nativeJavaDiagnostic(8, 4)}}
	for _, other := range []TransactionEvent{{DependencyType: "http"}, {DependencyType: "http", ServerTelemetry: &protocol.ServerTelemetry{}}} {
		series, err := buildNativeArchiveSeries(context.Background(), nativeJavaEventsFixture(t, java, other), q)
		if err != nil || series.Buckets[0].Value == nil || *series.Buckets[0].Value != 0.5 || series.Buckets[0].Count != 1 || len(series.Excluded) != 0 {
			t.Fatal("another protocol's action entered the Java population", series, err)
		}
	}
	series, err := buildNativeArchiveSeries(context.Background(), nativeJavaEventsFixture(t, java, TransactionEvent{DependencyType: "java_http"}), q)
	if err != nil || series.Buckets[0].Value != nil || series.Buckets[0].Reason != "incomplete_representation_evidence" {
		t.Fatal("a Java action without diagnostics qualified the bucket", series, err)
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
	// A discarded body's zero retained size is logging policy, not a measurement.
	d.ResponseState = "discarded"
	for _, metric := range []string{"java_http_retained_response_bytes", "java_http_response_base64_bytes"} {
		q.Metric = metric
		series, err = buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, d), q)
		if err != nil || series.Buckets[0].Value != nil || series.Excluded["response_discarded"] != 1 {
			t.Fatal("discarded response became a measured zero", metric, series, err)
		}
	}
	q.Metric = "java_http_decoded_request_bytes"
	if series, err = buildNativeArchiveSeries(context.Background(), nativeJavaFixture(t, d), q); err != nil || series.Buckets[0].Value == nil {
		t.Fatal("discarded response refused request evidence", series, err)
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
	shallowA, shallowB, graphA, graphB, serBytesA, serBytesB := int64(32), int64(48), int64(128), int64(256), int64(64), int64(96)
	serMsA, serMsB, deserMsA, deserMsB := 1.5, 2.5, 0.5, 1.5
	a.PreparedRequestBodyBytes, b.PreparedRequestBodyBytes = &preparedA, &preparedB
	a.AppliedRequestCompression, b.AppliedRequestCompression = "gzip", "gzip"
	a.ResponseRetainedBytes, b.ResponseRetainedBytes = &retainedA, &retainedB
	a.ResponseBase64Bytes, b.ResponseBase64Bytes = &base64A, &base64B
	a.Base64DecodeMs, b.Base64DecodeMs, a.Base64EncodeMs, b.Base64EncodeMs = &decodeA, &decodeB, &encodeA, &encodeB
	a.EnclosingMs, b.EnclosingMs = 10, 20
	a.ContainedObjectFootprint = &protocol.JavaHTTPObjectFootprintEvidence{
		FixtureKind: "approved_serializable_record",
		JVMVersion:  "21",
		ShallowObject: protocol.JavaHTTPShallowObjectFootprint{
			Metric:           "java_shallow_object_bytes",
			Method:           protocol.JavaHTTPShallowObjectMethodV1,
			JVMVersion:       "21",
			Available:        true,
			Bytes:            &shallowA,
			Unit:             "bytes",
			RetainedHeapNote: protocol.JavaHTTPObjectFootprintRetainedNote,
		},
		ReachableGraph: protocol.JavaHTTPReachableGraphFootprint{
			Metric:           "java_reachable_graph_bytes",
			Method:           protocol.JavaHTTPReachableGraphMethodV1,
			JVMVersion:       "21",
			Available:        true,
			Bytes:            &graphA,
			Unit:             "bytes",
			UniqueObjects:    5,
			MaxNodes:         4096,
			MaxDepth:         32,
			RetainedHeapNote: protocol.JavaHTTPObjectFootprintRetainedNote,
		},
		CodecExecuted:         true,
		SerializedBytes:       &serBytesA,
		JavaSerializationMs:   &serMsA,
		JavaDeserializationMs: &deserMsA,
	}
	b.ContainedObjectFootprint = &protocol.JavaHTTPObjectFootprintEvidence{
		FixtureKind: "approved_serializable_record",
		JVMVersion:  "21",
		ShallowObject: protocol.JavaHTTPShallowObjectFootprint{
			Metric:           "java_shallow_object_bytes",
			Method:           protocol.JavaHTTPShallowObjectMethodV1,
			JVMVersion:       "21",
			Available:        true,
			Bytes:            &shallowB,
			Unit:             "bytes",
			RetainedHeapNote: protocol.JavaHTTPObjectFootprintRetainedNote,
		},
		ReachableGraph: protocol.JavaHTTPReachableGraphFootprint{
			Metric:           "java_reachable_graph_bytes",
			Method:           protocol.JavaHTTPReachableGraphMethodV1,
			JVMVersion:       "21",
			Available:        true,
			Bytes:            &graphB,
			Unit:             "bytes",
			UniqueObjects:    5,
			MaxNodes:         4096,
			MaxDepth:         32,
			RetainedHeapNote: protocol.JavaHTTPObjectFootprintRetainedNote,
		},
		CodecExecuted:         true,
		SerializedBytes:       &serBytesB,
		JavaSerializationMs:   &serMsB,
		JavaDeserializationMs: &deserMsB,
	}
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
		{"serialized_representation_vs_http_body", "ratio", 9.0 / 7, 0, 0, true},
		{"java_http_base64_per_retained", "ratio", 12.0 / 9, 0, 0, true},
		{"java_http_base64_work_share", "ratio", .5, 0, 0, true},
		{"java_shallow_object_bytes", "bytes", 40, 32, 48, false},
		{"java_reachable_graph_bytes", "bytes", 192, 128, 256, false},
		{"java_serialization_ms", "ms", 2, 1.5, 2.5, false},
		{"java_deserialization_ms", "ms", 1, 0.5, 1.5, false},
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
