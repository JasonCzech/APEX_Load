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
