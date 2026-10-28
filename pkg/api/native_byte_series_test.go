package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
	"github.com/jasonczech/apexload/pkg/archivecontract"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

// byteRequest is one archived request of the byte fixture.
type byteRequest struct {
	at             time.Duration
	action, kind   string // kind: http (evidenced), legacy (HTTP without evidence), none (no request), grpc, transaction
	sent, received int64
}

// byteSeriesFixture archives requests for a run that started at start and
// completed 20 s later; coverage is its source coverage, if any.
func byteSeriesFixture(t *testing.T, coverage *RunArchiveSourceCoverage, requests ...byteRequest) *storage.RunArchiveRecord {
	t.Helper()
	root := t.TempDir()
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	manifest := RunArchiveManifest{TestID: "run", CompletedAt: start.Add(20 * time.Second), TransactionsFile: "transactions.jsonl", Provenance: RunArchiveProvenance{StartedAt: &start}, SourceCoverage: coverage}
	body, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(root, "manifest.json"), body, 0o600); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(root, "transactions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for _, r := range requests {
		event := TransactionEvent{Timestamp: start.Add(r.at), ActionName: r.action, Population: "action", DependencyType: "http", BytesSent: r.sent, BytesReceived: r.received, Success: true, ByteEvidence: protocol.ByteEvidenceHTTPBody}
		switch r.kind {
		case "legacy":
			event.ByteEvidence = ""
		case "none":
			event.ByteEvidence = protocol.ByteEvidenceNone
		case "grpc":
			event.DependencyType, event.ByteEvidence = "grpc", ""
		case "grpc_evidenced":
			event.DependencyType, event.ByteEvidence = "grpc", protocol.ByteEvidenceGRPCUncompressedPayload
		case "ws_evidenced":
			event.DependencyType, event.ByteEvidence = "websocket", protocol.ByteEvidenceWebSocketApplicationPayload
		case "ws_control":
			event.DependencyType, event.ByteEvidence = "websocket", protocol.ByteEvidenceNone
		case "ws_legacy":
			event.DependencyType, event.ByteEvidence = "websocket", ""
		case "transaction":
			event.Population, event.BusinessTransaction, event.ByteEvidence = "transaction", true, ""
		}
		if err := json.NewEncoder(f).Encode(event); err != nil {
			t.Fatal(err)
		}
	}
	return &storage.RunArchiveRecord{TestID: "run", ArchivePath: root, ManifestPath: filepath.Join(root, "manifest.json")}
}

// The values below are worked by hand from the fixture, not by the code
// under test: bucket 0 holds three evidenced login requests (one sending no
// body: a measured zero) beside a same-named gRPC action, a business
// transaction and a check that dispatched no request, none of them in the
// population; bucket 1 holds an evidenced and a legacy request.
var byteFixtureRequests = []byteRequest{
	{1 * time.Second, "login", "http", 100, 1000},
	{2 * time.Second, "login", "http", 0, 2000},
	{3 * time.Second, "login", "http", 300, 500},
	{4 * time.Second, "login", "grpc", 7000, 7000},
	{5 * time.Second, "login", "transaction", 9000, 9000},
	{5 * time.Second, "login", "none", 0, 0},
	{6 * time.Second, "logout", "http", 40, 60},
	{11 * time.Second, "login", "http", 50, 50},
	{12 * time.Second, "login", "legacy", 70, 70},
}

func TestNativeByteSeriesSumsRatesAndDistributions(t *testing.T) {
	record := byteSeriesFixture(t, nil, byteFixtureRequests...)
	query := func(metric, aggregation string, filters map[string]string) NativeArchiveSeries {
		t.Helper()
		q := storage.AnalyticsQuery{Population: analysis.PopulationByte, Metric: metric, Aggregation: aggregation, EndSeconds: 20, BucketSeconds: 10, Filters: filters}
		series, err := buildNativeArchiveSeries(context.Background(), record, q)
		if err != nil {
			t.Fatal(err)
		}
		return series
	}
	login := map[string]string{"action": "login"}
	for _, c := range []struct {
		metric, aggregation string
		filters             map[string]string
		want                float64
		unit                string
	}{
		{"bytes_sent", "sum", login, 400, "bytes"},
		{"bytes_received", "sum", login, 3500, "bytes"},
		{"bytes_sent", "sum", nil, 440, "bytes"},
		{"bytes_sent_rate", "rate", login, 40, "bytes/s"},
		{"bytes_received_rate", "rate", nil, 356, "bytes/s"},
		{"bytes_sent", "min", login, 0, "bytes"},
		{"bytes_received", "max", login, 2000, "bytes"},
		{"bytes_received", "avg", login, 3500.0 / 3, "bytes"},
		{"bytes_received", "p50", login, 1000, "bytes"},
		{"bytes_received", "p99", login, 2000, "bytes"},
	} {
		series := query(c.metric, c.aggregation, c.filters)
		first := series.Buckets[0]
		if first.Value == nil || *first.Value != c.want || series.Unit != c.unit {
			t.Fatalf("%s %s %v: bucket %+v unit %q, want %v %s", c.metric, c.aggregation, c.filters, first, series.Unit, c.want, c.unit)
		}
		// A bucket holding a request archived without byte evidence is
		// unscored, whatever the aggregation, and the exclusion counted.
		if second := series.Buckets[1]; second.Value != nil || second.Reason != "byte_evidence_unavailable" || series.Excluded["byte_evidence_unavailable"] != 1 {
			t.Fatalf("%s %s: a legacy request scored its bucket: %+v %v", c.metric, c.aggregation, second, series.Excluded)
		}
	}
	if series := query("bytes_sent", "sum", login); series.Buckets[0].Count != 3 || series.Excluded["no_request"] != 1 {
		t.Fatalf("bucket 0 counted %d requests (excluded %v), want the 3 evidenced HTTP logins and one step without a request", series.Buckets[0].Count, series.Excluded)
	}
	if warnings := strings.Join(query("bytes_sent", "sum", login).Warnings, " "); !strings.Contains(warnings, "without byte evidence") {
		t.Fatalf("warnings %q", warnings)
	}
	// No request in a bucket is no value, not a measured zero.
	if series := query("bytes_sent", "sum", map[string]string{"action": "logout"}); series.Buckets[0].Value == nil || series.Buckets[1].Value != nil || series.Buckets[1].Count != 0 {
		t.Fatalf("empty bucket %+v", series.Buckets)
	}
}

// An invalid count unscores its bucket with its own reason, and a sum past
// exact double precision is unscored rather than rounded.
func TestNativeByteSeriesInvalidCountsAndExactSums(t *testing.T) {
	record := byteSeriesFixture(t, nil, byteRequest{1 * time.Second, "login", "http", -1, 0}, byteRequest{11 * time.Second, "login", "http", 1 << 52, 0}, byteRequest{12 * time.Second, "login", "http", 1 << 52, 0})
	q := storage.AnalyticsQuery{Population: analysis.PopulationByte, Metric: "bytes_sent", Aggregation: "sum", EndSeconds: 20, BucketSeconds: 10}
	series, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil || series.Buckets[0].Value != nil || series.Buckets[0].Reason != "invalid_byte_count" || series.Excluded["invalid_byte_count"] != 1 {
		t.Fatalf("invalid count: %+v %v", series, err)
	}
	if series.Buckets[1].Value != nil || series.Buckets[1].Reason != "exact_sum_limit" {
		t.Fatalf("a sum past 2^53 - 1 scored: %+v", series.Buckets[1])
	}
	q.Aggregation = "max"
	if series, err = buildNativeArchiveSeries(context.Background(), record, q); err != nil || series.Buckets[1].Value == nil || *series.Buckets[1].Value != 1<<52 {
		t.Fatalf("a distribution of large counts: %+v %v", series, err)
	}
}

// Totals need every source's requests and a bucket that ended by the run's
// completion; a distribution still describes the collected requests.
func TestNativeByteSeriesTotalsNeedCompleteBuckets(t *testing.T) {
	evidenced := byteRequest{1 * time.Second, "login", "http", 100, 1000}
	for _, coverage := range []*RunArchiveSourceCoverage{
		{Mode: "required", Sources: []archivecontract.RunArchiveSource{{Target: "a", Collected: true}, {Target: "b"}}},
		{Mode: "best_effort", Sources: []archivecontract.RunArchiveSource{{Target: "a", Collected: true}}},
	} {
		record := byteSeriesFixture(t, coverage, evidenced)
		for aggregation, scored := range map[string]bool{"sum": false, "avg": true} {
			q := storage.AnalyticsQuery{Population: analysis.PopulationByte, Metric: "bytes_sent", Aggregation: aggregation, EndSeconds: 20, BucketSeconds: 10}
			series, err := buildNativeArchiveSeries(context.Background(), record, q)
			if err != nil || (series.Buckets[0].Value != nil) != scored || !scored && series.Buckets[0].Reason != "incomplete_bucket" || !strings.Contains(strings.Join(series.Warnings, " "), "does not hold every source") {
				t.Fatalf("%s coverage %s: %+v %v", coverage.Mode, aggregation, series, err)
			}
		}
	}
	record := byteSeriesFixture(t, &RunArchiveSourceCoverage{Mode: "required", Sources: []archivecontract.RunArchiveSource{{Target: "a", Collected: true}}}, evidenced, byteRequest{21 * time.Second, "login", "http", 5, 5})
	q := storage.AnalyticsQuery{Population: analysis.PopulationByte, Metric: "bytes_sent_rate", Aggregation: "rate", EndSeconds: 30, BucketSeconds: 10}
	series, err := buildNativeArchiveSeries(context.Background(), record, q)
	if err != nil || series.Buckets[0].Value == nil || *series.Buckets[0].Value != 10 || series.Buckets[2].Value != nil || series.Buckets[2].Reason != "incomplete_bucket" {
		t.Fatalf("a bucket past completion scored a rate: %+v %v", series.Buckets, err)
	}
}

// The archive panel route admits byte queries, compares a run with another,
// and refuses what the population cannot answer.
func TestNativeByteSeriesRoute(t *testing.T) {
	server, store := baselineTestServer(t)
	current := comparisonArchiveFixture(t, server, store, "current", 200, 150)
	baseline := comparisonArchiveFixture(t, server, store, "baseline", 100, 100)
	start := *current.StartedAt
	for id, sent := range map[string]int64{current.ID: 300, baseline.ID: 200} {
		f, err := os.Create(filepath.Join(server.runner.archives[id].ArchivePath, "transactions.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		event := TransactionEvent{Timestamp: start.Add(30 * time.Second), ActionName: "login", Population: "action", DependencyType: "http", BytesSent: sent, Success: true, ByteEvidence: protocol.ByteEvidenceHTTPBody}
		if err := json.NewEncoder(f).Encode(event); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
	}
	call := func(q storage.AnalyticsQuery) *httptest.ResponseRecorder {
		body, _ := json.Marshal(q)
		request := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+current.ID+"/archive/native-series", bytes.NewReader(body))
		request.SetPathValue("id", current.ID)
		recorder := httptest.NewRecorder()
		server.handleNativeArchiveSeries(recorder, request)
		return recorder
	}
	q := storage.AnalyticsQuery{Population: analysis.PopulationByte, Metric: "bytes_sent", Aggregation: "sum", EndSeconds: 60, BucketSeconds: 60, ComparisonRunID: baseline.ID}
	recorder := call(q)
	var response NativeArchiveResponse
	if recorder.Code != http.StatusOK || json.Unmarshal(recorder.Body.Bytes(), &response) != nil {
		t.Fatalf("status %d: %s", recorder.Code, recorder.Body)
	}
	if response.Current.Buckets[0].Value == nil || *response.Current.Buckets[0].Value != 300 || response.Baseline == nil || response.Baseline.Buckets[0].Value == nil || *response.Baseline.Buckets[0].Value != 200 || response.Current.Unit != "bytes" {
		t.Fatalf("byte comparison %+v", response)
	}
	q.Filters = map[string]string{"network_profile": "3g"}
	if recorder := call(q); recorder.Code != http.StatusBadRequest {
		t.Fatalf("a byte query with an unsupported filter = %d", recorder.Code)
	}
}

// The controller's archive writer keeps the byte evidence its HTTP adapter
// set, but not on a business transaction; the merge of generator archives
// and the Parquet copy keep it.
func TestRunArchiveWriterStampsHTTPByteEvidence(t *testing.T) {
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "byte-evidence", Name: "Byte evidence"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	for _, result := range []*protocol.Result{
		{ActionName: "http", DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, Success: true, StartTime: time.Now().UTC(), BytesReceived: 512},
		{ActionName: "check", DependencyType: "http", ByteEvidence: protocol.ByteEvidenceNone, Success: true, StartTime: time.Now().UTC()},
		{ActionName: "transaction", DependencyType: "http", ByteEvidence: protocol.ByteEvidenceHTTPBody, BusinessTransaction: true, Success: true, StartTime: time.Now().UTC(), BytesReceived: 512},
		{ActionName: "websocket", DependencyType: "websocket", Success: true, StartTime: time.Now().UTC(), BytesSent: 9},
	} {
		if err := writer.RecordResult(result); err != nil {
			t.Fatal(err)
		}
	}
	record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	evidence := map[string]string{}
	if err := forEachArchiveTransactionContext(context.Background(), record, func(tx *TransactionEvent) error {
		evidence[tx.ActionName] = tx.ByteEvidence
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if evidence["http"] != protocol.ByteEvidenceHTTPBody || evidence["check"] != protocol.ByteEvidenceNone || evidence["transaction"] != "" || evidence["websocket"] != "" {
		t.Fatalf("byte evidence %v", evidence)
	}
	destination := filepath.Join(t.TempDir(), "merged.jsonl")
	if err := mergeRemoteArchiveTransactions(destination, &RunArchiveManifest{}, []remoteArchiveBundle{{
		target: "a", transactionsPath: filepath.Join(record.ArchivePath, "transactions.jsonl"), manifest: RunArchiveManifest{TransactionCount: 4},
	}}, nil); err != nil {
		t.Fatal(err)
	}
	merged := map[string]string{}
	for _, row := range readTransactionRows(t, destination) {
		merged[row.ActionName] = row.ByteEvidence
	}
	if merged["http"] != protocol.ByteEvidenceHTTPBody || merged["check"] != protocol.ByteEvidenceNone {
		t.Fatalf("the merge lost the byte evidence: %v", merged)
	}
	if rows := toParquetTransactions([]TransactionEvent{{ByteEvidence: protocol.ByteEvidenceHTTPBody}}); rows[0].ByteEvidence != protocol.ByteEvidenceHTTPBody {
		t.Fatal("the Parquet copy lost the byte evidence")
	}
}

func TestNativeGRPCAndWebSocketArchiveAndLiveByteQueries(t *testing.T) {
	record := byteSeriesFixture(t, nil,
		byteRequest{1 * time.Second, "Greeter/SayHello", "grpc_evidenced", 120, 240},
		byteRequest{2 * time.Second, "Greeter/SayHello", "grpc_evidenced", 180, 360},
		byteRequest{3 * time.Second, "ws_send", "ws_evidenced", 64, 0},
		byteRequest{4 * time.Second, "ws_receive", "ws_evidenced", 0, 128},
		byteRequest{5 * time.Second, "ws_ping", "ws_control", 0, 0},
		byteRequest{11 * time.Second, "Greeter/SayHello", "grpc", 100, 200},
		byteRequest{12 * time.Second, "ws_send", "ws_legacy", 50, 0},
	)

	// Archive gRPC byte queries: sum, rate, p95, and unmeasured bucket 1 -> byte_evidence_unavailable.
	// Unfiltered byte query on an archive with multiple byte-measured protocols (gRPC + WebSocket, or HTTP + gRPC) is rejected as ambiguous.
	if _, err := buildNativeArchiveSeries(context.Background(), record, storage.AnalyticsQuery{
		Population: analysis.PopulationByte, Metric: "bytes_sent", Aggregation: "sum", EndSeconds: 20, BucketSeconds: 10,
	}); err == nil || !strings.Contains(err.Error(), "multiple byte-measured protocols present") {
		t.Fatalf("expected multi-protocol ambiguity error for grpc+websocket without filters.protocol, got %v", err)
	}
	httpAndGRPCRecord := byteSeriesFixture(t, nil,
		byteRequest{1 * time.Second, "http_get", "http", 100, 200},
		byteRequest{2 * time.Second, "Greeter/SayHello", "grpc_evidenced", 120, 240},
	)
	if _, err := buildNativeArchiveSeries(context.Background(), httpAndGRPCRecord, storage.AnalyticsQuery{
		Population: analysis.PopulationByte, Metric: "bytes_sent", Aggregation: "sum", EndSeconds: 20, BucketSeconds: 10,
	}); err == nil || !strings.Contains(err.Error(), "multiple byte-measured protocols present") {
		t.Fatalf("expected multi-protocol ambiguity error for http+grpc without filters.protocol, got %v", err)
	}

	grpcFilter := map[string]string{"protocol": "grpc", "action": "Greeter/SayHello"}
	for _, tc := range []struct {
		metric, agg string
		want        float64
	}{
		{"bytes_sent", "sum", 300},
		{"bytes_received", "sum", 600},
		{"bytes_sent_rate", "rate", 30},
		{"bytes_received", "p95", 360},
	} {
		q := storage.AnalyticsQuery{Population: analysis.PopulationByte, Metric: tc.metric, Aggregation: tc.agg, EndSeconds: 20, BucketSeconds: 10, Filters: grpcFilter}
		series, err := buildNativeArchiveSeries(context.Background(), record, q)
		if err != nil {
			t.Fatalf("grpc %s %s: %v", tc.metric, tc.agg, err)
		}
		if series.Buckets[0].Value == nil || *series.Buckets[0].Value != tc.want {
			t.Fatalf("grpc %s %s bucket 0 = %+v, want %v", tc.metric, tc.agg, series.Buckets[0], tc.want)
		}
		if series.Buckets[1].Value != nil || series.Buckets[1].Reason != "byte_evidence_unavailable" {
			t.Fatalf("grpc legacy bucket 1 = %+v, want byte_evidence_unavailable", series.Buckets[1])
		}
	}

	// Archive WebSocket byte queries: application payload counted, ws_ping control frame excluded as no_request, legacy unmeasured -> byte_evidence_unavailable.
	wsFilter := map[string]string{"protocol": "websocket"}
	wsSeries, err := buildNativeArchiveSeries(context.Background(), record, storage.AnalyticsQuery{
		Population: analysis.PopulationByte, Metric: "bytes_received", Aggregation: "sum", EndSeconds: 20, BucketSeconds: 10, Filters: wsFilter,
	})
	if err != nil {
		t.Fatalf("websocket archive sum: %v", err)
	}
	if wsSeries.Buckets[0].Value == nil || *wsSeries.Buckets[0].Value != 128 || wsSeries.Buckets[0].Count != 2 || wsSeries.Excluded["no_request"] != 1 {
		t.Fatalf("unexpected websocket bucket 0: %+v excluded=%v", wsSeries.Buckets[0], wsSeries.Excluded)
	}
	if wsSeries.Buckets[1].Value != nil || wsSeries.Buckets[1].Reason != "byte_evidence_unavailable" {
		t.Fatalf("expected websocket legacy bucket 1 byte_evidence_unavailable, got %+v", wsSeries.Buckets[1])
	}

	// Live HTTP, gRPC, and WebSocket interval byte queries over active run buckets.
	server, store := baselineTestServer(t)
	start := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	runID := "live-byte-run"
	if err := store.SaveTest(&storage.TestRecord{ID: runID, Name: "Live Bytes", Status: "RUNNING", StartTime: start}); err != nil {
		t.Fatal(err)
	}
	pt1 := &LiveMetrics{
		Timestamp: start.Add(10 * time.Second),
		NativeEvidence: &storage.NativeIntervalEvidence{
			Version:       3,
			WindowSeconds: 10,
			Dimensions: map[string]map[string]storage.NativeIntervalMetric{
				"action": {
					"http_get":         {Count: 2, BytesSent: 200, BytesReceived: 800, ByteEvidence: protocol.ByteEvidenceHTTPBody, ByteSamples: 2},
					"Greeter/SayHello": {Count: 3, BytesSent: 300, BytesReceived: 900, ByteEvidence: protocol.ByteEvidenceGRPCUncompressedPayload, ByteSamples: 3},
					"ws_send":          {Count: 4, BytesSent: 400, BytesReceived: 0, ByteEvidence: protocol.ByteEvidenceWebSocketApplicationPayload, ByteSamples: 4},
					"legacy_http":      {Count: 1, BytesSent: 100, BytesReceived: 100, ByteEvidence: protocol.ByteEvidenceUnmeasured},
				},
			},
		},
	}
	pt2Incomplete := &LiveMetrics{
		Timestamp: start.Add(20 * time.Second),
		NativeEvidence: &storage.NativeIntervalEvidence{
			Version:       3,
			WindowSeconds: 10,
			Incomplete:    true,
			Dimensions: map[string]map[string]storage.NativeIntervalMetric{
				"action": {
					"Greeter/SayHello": {Count: 1, BytesSent: 100, BytesReceived: 300, ByteEvidence: protocol.ByteEvidenceGRPCUncompressedPayload, ByteSamples: 1},
				},
			},
		},
	}
	server.runner.mu.Lock()
	server.runner.tests[runID] = &ManagedTest{
		ID:        runID,
		Name:      "Live Bytes",
		Status:    "RUNNING",
		StartedAt: &start,
		History:   []*LiveMetrics{pt1, pt2Incomplete},
	}
	server.runner.mu.Unlock()

	callLive := func(q storage.AnalyticsQuery) *httptest.ResponseRecorder {
		body, _ := json.Marshal(q)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/tests/"+runID+"/native-evidence", bytes.NewReader(body))
		req.SetPathValue("id", runID)
		rec := httptest.NewRecorder()
		server.handleNativeEvidenceSeries(rec, req)
		return rec
	}

	for _, tc := range []struct {
		proto, action, metric, agg string
		want                       float64
	}{
		{"http", "http_get", "bytes_received_rate", "rate", 80},
		{"grpc", "Greeter/SayHello", "bytes_sent", "sum", 300},
		{"grpc", "Greeter/SayHello", "bytes_received_rate", "rate", 90},
		{"websocket", "ws_send", "bytes_sent_rate", "rate", 40},
	} {
		rec := callLive(storage.AnalyticsQuery{
			QueryVersion:   storage.AnalyticsQueryVersion,
			EvidenceMode:   "live",
			LiveBucketMode: "merged",
			Population:     analysis.PopulationByte,
			Metric:         tc.metric,
			Aggregation:    tc.agg,
			EndSeconds:     60,
			BucketSeconds:  10,
			MaxSeries:      1,
			Filters:        map[string]string{"protocol": tc.proto, "action": tc.action},
		})
		var resp NativeEvidenceResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || len(resp.Series) != 1 {
			t.Fatalf("live %s %s: status=%d body=%s", tc.proto, tc.metric, rec.Code, rec.Body.String())
		}
		points := resp.Series[0].Points
		if len(points) < 1 || points[0].Value == nil || *points[0].Value != tc.want {
			t.Fatalf("live %s %s point 0 = %+v, want %v", tc.proto, tc.metric, points, tc.want)
		}
		if tc.proto == "grpc" && (len(points) < 2 || points[1].Value != nil || points[1].Reason != "incomplete_bucket") {
			t.Fatalf("expected incomplete second bucket for grpc live query, got %+v", points)
		}
	}

	// Live byte queries filtered only by protocol (without filters.action) are not poisoned by unmeasured actions from other protocols, in both merged and source bucket modes, ungrouped and grouped by action.
	for _, bucketMode := range []string{"merged", "source"} {
		for _, groupBy := range [][]string{nil, {"action"}} {
			maxSeries := 1
			if len(groupBy) > 0 {
				maxSeries = 5
			}
			rec := callLive(storage.AnalyticsQuery{
				QueryVersion:   storage.AnalyticsQueryVersion,
				EvidenceMode:   "live",
				LiveBucketMode: bucketMode,
				Population:     analysis.PopulationByte,
				Metric:         "bytes_sent",
				Aggregation:    "sum",
				Dimensions:     groupBy,
				EndSeconds:     60,
				BucketSeconds:  10,
				MaxSeries:      maxSeries,
				Filters:        map[string]string{"protocol": "grpc"},
			})
			var resp NativeEvidenceResponse
			if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || len(resp.Series) != 1 {
				t.Fatalf("live protocol-only grpc (%s group=%v): status=%d body=%s", bucketMode, groupBy, rec.Code, rec.Body.String())
			}
			if len(groupBy) > 0 && resp.Series[0].Name != "Greeter/SayHello" {
				t.Fatalf("expected only Greeter/SayHello series when grouped by action, got %q", resp.Series[0].Name)
			}
			if len(resp.Series[0].Points) < 1 || resp.Series[0].Points[0].Value == nil || *resp.Series[0].Points[0].Value != 300 {
				t.Fatalf("live protocol-only grpc (%s group=%v) point 0 = %+v, want 300", bucketMode, groupBy, resp.Series[0].Points)
			}
		}
	}

	// Archive grouped byte evidence resolves single evidenced protocol symmetrically regardless of arrival order beside unevidenced observations of another protocol.
	orderA := byteSeriesFixture(t, nil,
		byteRequest{1 * time.Second, "shared_step", "grpc_evidenced", 120, 240},
		byteRequest{2 * time.Second, "shared_step", "legacy", 100, 200},
	)
	orderB := byteSeriesFixture(t, nil,
		byteRequest{1 * time.Second, "shared_step", "legacy", 100, 200},
		byteRequest{2 * time.Second, "shared_step", "grpc_evidenced", 120, 240},
	)
	for label, recFixture := range map[string]*storage.RunArchiveRecord{"grpc_then_legacy_http": orderA, "legacy_http_then_grpc": orderB} {
		var mf RunArchiveManifest
		rawMf, err := os.ReadFile(recFixture.ManifestPath)
		if err != nil || json.Unmarshal(rawMf, &mf) != nil {
			t.Fatalf("%s: read manifest: %v", label, err)
		}
		groupedResp, err := buildNativeGroupedByteArchive(context.Background(), recFixture, &mf, storage.AnalyticsQuery{
			EvidenceMode:  "archive",
			Population:    analysis.PopulationByte,
			Metric:        "bytes_sent",
			Aggregation:   "sum",
			Dimensions:    []string{"action"},
			EndSeconds:    20,
			BucketSeconds: 10,
			MaxSeries:     5,
		})
		if err != nil {
			t.Fatalf("%s: unexpected error from buildNativeGroupedByteArchive: %v", label, err)
		}
		if len(groupedResp.Series) != 1 || len(groupedResp.Series[0].Points) < 1 || groupedResp.Series[0].Points[0].Value == nil || *groupedResp.Series[0].Points[0].Value != 120 {
			t.Fatalf("%s: expected scored gRPC sum 120, got %+v", label, groupedResp.Series)
		}
	}

	// Unmeasured live action returns byte_evidence_unavailable, both with and without filters.protocol.
	for _, filters := range []map[string]string{
		{"action": "legacy_http"},
		{"protocol": "http", "action": "legacy_http"},
	} {
		for _, bucketMode := range []string{"merged", "source"} {
			unmeasuredRec := callLive(storage.AnalyticsQuery{
				QueryVersion:   storage.AnalyticsQueryVersion,
				EvidenceMode:   "live",
				LiveBucketMode: bucketMode,
				Population:     analysis.PopulationByte,
				Metric:         "bytes_sent",
				Aggregation:    "sum",
				EndSeconds:     60,
				BucketSeconds:  10,
				MaxSeries:      1,
				Filters:        filters,
			})
			var unmeasuredResp NativeEvidenceResponse
			if unmeasuredRec.Code != http.StatusOK || json.Unmarshal(unmeasuredRec.Body.Bytes(), &unmeasuredResp) != nil {
				t.Fatalf("live unmeasured (%s %v) status=%d body=%s", bucketMode, filters, unmeasuredRec.Code, unmeasuredRec.Body.String())
			}
			if len(unmeasuredResp.Series) != 1 || unmeasuredResp.Series[0].Points[0].Value != nil || unmeasuredResp.Series[0].Points[0].Reason != "byte_evidence_unavailable" {
				t.Fatalf("expected byte_evidence_unavailable for unmeasured live action (%s %v), got %+v", bucketMode, filters, unmeasuredResp.Series)
			}
		}
	}

	// Same gRPC action transitioning from measured in interval 1 to unmeasured in interval 3 surfaces byte_evidence_unavailable under filters: {"protocol": "grpc"}.
	pt3UnmeasuredGRPC := &LiveMetrics{
		Timestamp: start.Add(30 * time.Second),
		NativeEvidence: &storage.NativeIntervalEvidence{
			Version:       3,
			WindowSeconds: 10,
			Dimensions: map[string]map[string]storage.NativeIntervalMetric{
				"action": {
					"Greeter/SayHello": {Count: 1, BytesSent: 150, BytesReceived: 300, ByteEvidence: protocol.ByteEvidenceUnmeasured},
				},
			},
		},
	}
	server.runner.mu.Lock()
	server.runner.tests[runID].History = []*LiveMetrics{pt1, pt2Incomplete, pt3UnmeasuredGRPC}
	server.runner.mu.Unlock()
	for _, bucketMode := range []string{"merged", "source"} {
		rec := callLive(storage.AnalyticsQuery{
			QueryVersion:   storage.AnalyticsQueryVersion,
			EvidenceMode:   "live",
			LiveBucketMode: bucketMode,
			Population:     analysis.PopulationByte,
			Metric:         "bytes_sent",
			Aggregation:    "sum",
			EndSeconds:     60,
			BucketSeconds:  10,
			MaxSeries:      1,
			Filters:        map[string]string{"protocol": "grpc"},
		})
		var resp NativeEvidenceResponse
		if rec.Code != http.StatusOK || json.Unmarshal(rec.Body.Bytes(), &resp) != nil || len(resp.Series) != 1 || len(resp.Series[0].Points) < 3 {
			t.Fatalf("live grpc transition (%s): status=%d body=%s", bucketMode, rec.Code, rec.Body.String())
		}
		if resp.Series[0].Points[2].Value != nil || resp.Series[0].Points[2].Reason != "byte_evidence_unavailable" {
			t.Fatalf("expected bucket 2 Reason=byte_evidence_unavailable for same-action unmeasured interval (%s), got %+v", bucketMode, resp.Series[0].Points[2])
		}
	}

	// Live byte distribution query is rejected with 400.
	distRec := callLive(storage.AnalyticsQuery{
		QueryVersion:   storage.AnalyticsQueryVersion,
		EvidenceMode:   "live",
		LiveBucketMode: "merged",
		Population:     analysis.PopulationByte,
		Metric:         "bytes_sent",
		Aggregation:    "p95",
		EndSeconds:     60,
		BucketSeconds:  10,
		MaxSeries:      1,
		Filters:        map[string]string{"protocol": "grpc", "action": "Greeter/SayHello"},
	})
	if distRec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for live byte p95 distribution query, got %d: %s", distRec.Code, distRec.Body.String())
	}
}
