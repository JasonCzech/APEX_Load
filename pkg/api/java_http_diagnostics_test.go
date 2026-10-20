package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	javaproto "github.com/jasonczech/apexload/pkg/protocol/java_http"
	"github.com/jasonczech/apexload/pkg/storage"
	localParquet "github.com/xitongsys/parquet-go-source/local"
	parquetReader "github.com/xitongsys/parquet-go/reader"
)

func javaAPIDiagnostic() *protocol.JavaHTTPDiagnostics {
	zero, size := 0.0, int64(0)
	return &protocol.JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "empty", RequestDecodedBytes: &size, PreparedRequestBodyBytes: &size, AppliedRequestCompression: "none", ResponseState: "complete", ResponseRetainedBytes: &size, ResponseBase64Bytes: &size, Base64EncodeMs: &zero, EnclosingMs: 1, ObjectFootprint: "unavailable_embedded_opaque_bytes"}
}

func TestJavaHTTPActionSummaryLocalDistributedAndCloneAgree(t *testing.T) {
	var s *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&s, javaAPIDiagnostic())
	local := cloneEngineServerTelemetrySummary(&engine.ServerTelemetrySummary{JavaHTTP: s})
	remote := txMetricsFromAggregate(generatorrpc.MetricAggregate{Name: "java-call", Count: 1, JavaHTTP: s})
	if remote == nil || remote.ServerTelemetry == nil || !reflect.DeepEqual(local.JavaHTTP, remote.ServerTelemetry.JavaHTTP) {
		t.Fatal("distributed action summary lost observations", remote)
	}
	cloned := cloneTxMetricsMap(map[string]*TxMetrics{"java-call": remote})
	remote.ServerTelemetry.JavaHTTP.Base64Encode.Buckets[0]++
	if cloned["java-call"].ServerTelemetry.JavaHTTP.Base64Encode.Buckets[0] != 1 {
		t.Fatal("API snapshot aliases histogram")
	}
	mixed := txMetricsFromAggregate(generatorrpc.MetricAggregate{Name: "java-call", Count: 2, JavaHTTP: s})
	if mixed.ServerTelemetry.JavaHTTP.Samples != 1 || !mixed.ServerTelemetry.JavaHTTP.Incomplete || mixed.ServerTelemetry.JavaHTTP.RequestDecoded.Count != 1 {
		t.Fatal("old peer became zero/complete", mixed)
	}
}

func TestJavaHTTPAcquisitionSurvivesJSONLParquetAndReport(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Equal(body, []byte{0, 255, 1, 2}) {
			t.Error("independent endpoint request mismatch", err)
		}
		_, _ = w.Write([]byte("java-object-content"))
	}))
	defer server.Close()
	adapter := &javaproto.JavaHTTP{}
	if err := adapter.Init(context.Background(), map[string]any{"java_http_preserve_binary_result": true}); err != nil {
		t.Fatal(err)
	}
	defer adapter.Teardown(context.Background())
	result, err := adapter.Execute(context.Background(), &protocol.Action{Name: "java-call", Method: "POST", Target: server.URL, Body: []byte("AP8BAg==")})
	if err != nil || result == nil || result.ServerTelemetry == nil || !result.ServerTelemetry.JavaHTTP.Valid() {
		t.Fatal("acquisition failed", err)
	}
	d := result.ServerTelemetry.JavaHTTP
	if *d.RequestDecodedBytes != 4 || *d.PreparedRequestBodyBytes != 4 || *d.ResponseRetainedBytes != 19 {
		t.Fatal("byte populations changed", d)
	}
	events := []TransactionEvent{
		{ActionName: "java-call", Population: "action", ServerTelemetry: result.ServerTelemetry},
		{ActionName: "empty-call", Population: "action", ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: javaAPIDiagnostic()}},
		{ActionName: "unknown", Population: "action", ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: &protocol.JavaHTTPDiagnostics{Version: 9, Mode: "forbidden-peer-content"}}},
		{ActionName: "legacy", Population: "action"},
		{ActionName: "wrapper", Population: "transaction", BusinessTransaction: true, ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: d.Clone()}},
	}
	var raw bytes.Buffer
	for i := range events {
		sanitizeArchivedTransaction(&events[i])
		if err := json.NewEncoder(&raw).Encode(events[i]); err != nil {
			t.Fatal(err)
		}
	}
	if strings.Contains(raw.String(), "forbidden-peer-content") || strings.Contains(raw.String(), "java-object-content") {
		t.Fatal("diagnostic archive leaked representation content")
	}
	if events[4].ServerTelemetry.JavaHTTP != nil || result.ServerTelemetry.JavaHTTP == nil {
		t.Fatal("wrapper duplicated action or changed original")
	}
	dir := t.TempDir()
	input, output := filepath.Join(dir, "transactions.jsonl"), filepath.Join(dir, "transactions.parquet")
	if err := os.WriteFile(input, raw.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	count, err := streamJSONLToParquet(input, output, func(e TransactionEvent) parquetTransactionEvent {
		return toParquetTransactions([]TransactionEvent{e})[0]
	})
	if err != nil || count != int64(len(events)) {
		t.Fatal("conversion failed", count, err)
	}
	f, err := localParquet.NewLocalFileReader(output)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	reader, err := parquetReader.NewParquetReader(f, new(parquetTransactionEvent), 1)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.ReadStop()
	rows := make([]parquetTransactionEvent, len(events))
	if err := reader.Read(&rows); err != nil {
		t.Fatal(err)
	}
	for i, row := range rows {
		var restored *protocol.ServerTelemetry
		if err := json.Unmarshal([]byte(row.ServerTelemetryJSON), &restored); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(restored, events[i].ServerTelemetry) {
			t.Fatal("Parquet changed diagnostic availability", i)
		}
	}
	var summary *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&summary, d)
	lines := strings.Join(javaHTTPReportLines("java-call", summary, 1), "\n")
	for _, want := range []string{"1 observed / 1 action samples", "mean 4.000 bytes", "JVM object footprint is unavailable", "Base64 conversion work / enclosing wrapper time"} {
		if !strings.Contains(lines, want) {
			t.Fatal("report lost declared measurement", want, lines)
		}
	}
	partial := strings.Join(javaHTTPReportLines("java-call", summary, 2), "\n")
	if !strings.Contains(partial, "coverage is incomplete") {
		t.Fatal("mixed population qualified", partial)
	}
}

func TestJavaHTTPControllerMergePreservesActionPopulation(t *testing.T) {
	var summary *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&summary, &protocol.JavaHTTPDiagnostics{Version: 1, Mode: "embedded_opaque_bytes", InputState: "empty", RequestEncodedBytes: 0, RequestDecodedBytes: javaHTTPAPIInt(0), ResponseState: "unavailable", EnclosingMs: 1, ObjectFootprint: "unavailable_embedded_opaque_bytes"})
	if !summary.Valid() {
		t.Fatal("invalid fixture", summary)
	}
	current := generatorrpc.MetricAggregate{Name: "java", Count: 1, JavaHTTP: summary}
	old := generatorrpc.MetricAggregate{Name: "java", Count: 1}
	for _, values := range [][]generatorrpc.MetricAggregate{{current}, {current, current}, {current, old}, {old, current}, {old}} {
		got := mergeGeneratorMetricAggregates(values)
		if got == nil || got.Count != int64(len(values)) {
			t.Fatal("population", got)
		}
		var observed int64
		for _, v := range values {
			if v.JavaHTTP != nil {
				observed++
			}
		}
		if observed == 0 {
			if got.JavaHTTP != nil {
				t.Fatal("legacy absence became measurement")
			}
			continue
		}
		if got.JavaHTTP == nil || !got.JavaHTTP.Valid() || got.JavaHTTP.Samples != observed || got.JavaHTTP.Incomplete != (observed < got.Count) {
			t.Fatal("coverage", got.JavaHTTP)
		}
		got.JavaHTTP.Samples = 123
		if summary.Samples != 1 {
			t.Fatal("controller merge aliased source")
		}
	}
	invalid := current
	invalid.JavaHTTP = &protocol.JavaHTTPSummary{Version: 99}
	if got := mergeGeneratorMetricAggregates([]generatorrpc.MetricAggregate{invalid, current}); got.JavaHTTP == nil || !got.JavaHTTP.Incomplete || got.JavaHTTP.Samples != 1 {
		t.Fatal("invalid peer", got)
	}
	// A counted population survives another protocol's same-named peer, but an
	// older generator's uncounted java_http peer makes it unknown.
	var counted *protocol.JavaHTTPSummary
	protocol.ObserveJavaHTTPAction(&counted, javaAPIDiagnostic())
	java := generatorrpc.MetricAggregate{Name: "java", Count: 1, DependencyType: "java_http", JavaHTTP: counted}
	for dependency, known := range map[string]bool{"http": true, "java_http": false} {
		peer := generatorrpc.MetricAggregate{Name: "java", Count: 2, DependencyType: dependency}
		for _, values := range [][]generatorrpc.MetricAggregate{{java, peer}, {peer, java}} {
			if got := mergeGeneratorMetricAggregates(values).JavaHTTP; got == nil || got.Samples != 1 || got.Incomplete == known || (got.Population == 1) != known {
				t.Fatal("peer without a Java summary", dependency, got)
			}
		}
	}
}
func javaHTTPAPIInt(v int64) *int64 { return &v }

func TestJavaHTTPActualActionLiveHistoryAndParquet(t *testing.T) {
	var s *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&s, javaAPIDiagnostic())
	snapshot := engine.MetricsSnapshot{ByAction: map[string]*engine.ActionMetrics{"request": {Name: "request", Count: 1, ServerTelemetry: &engine.ServerTelemetrySummary{JavaHTTP: s}}}}
	live := buildLiveMetricsFromSnapshot(snapshot, nil)
	if live.JavaHTTP == nil || live.JavaHTTP.Incomplete || live.JavaHTTP.Actions["request"].Population != 1 {
		t.Fatal("action without transaction missing", live.JavaHTTP)
	}
	var remote LiveMetrics
	applyDistributedAggregateMetrics(nil, &remote, generatorrpc.MetricsBatch{ActionMetrics: []generatorrpc.MetricAggregate{{Name: "request", Count: 1, JavaHTTP: s}}}, 0, nil)
	if !reflect.DeepEqual(live.JavaHTTP, remote.JavaHTTP) {
		t.Fatal("standalone distributed action missing", remote.JavaHTTP)
	}
	clone := cloneLiveMetrics(live)
	clone.JavaHTTP.Actions["request"].Diagnostics.Samples = 99
	if live.JavaHTTP.Actions["request"].Diagnostics.Samples != 1 {
		t.Fatal("live clone aliases")
	}
	if filterLiveMetricsForGroups(live, groupFilterQuery{Explicit: true}).JavaHTTP != nil {
		t.Fatal("global diagnostics promoted into group selection")
	}
	restored := intervalSummaryRecordToLiveMetrics(storage.IntervalSummaryRecord{JavaHTTP: live.JavaHTTP})
	if !reflect.DeepEqual(live.JavaHTTP, restored.JavaHTTP) {
		t.Fatal("restart projection dropped action diagnostics")
	}
	rows := toParquetSummaries([]IntervalSummaryRecord{{JavaHTTP: live.JavaHTTP}})
	if len(rows) != 1 || !reflect.DeepEqual(protocol.ParseJavaHTTPActionsJSON(rows[0].JavaHTTPJSON), live.JavaHTTP) {
		t.Fatal("parquet projection dropped diagnostics")
	}
	before := managedTestHistoryPointWeight(&LiveMetrics{}, 1<<30)
	after := managedTestHistoryPointWeight(live, 1<<30)
	if after <= before+1000 {
		t.Fatal("history byte budget ignored evidence", before, after)
	}
}
func TestJavaHTTPArchiveMergeDisclosesLegacyCoverage(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		var s *protocol.JavaHTTPSummary
		protocol.MergeJavaHTTPDiagnostic(&s, javaAPIDiagnostic())
		var a *protocol.JavaHTTPActions
		protocol.AddJavaHTTPAction(&a, "request", 1, s)
		values := []*protocol.JavaHTTPActions{a, nil}
		if reverse {
			values[0], values[1] = values[1], values[0]
		}
		root := t.TempDir()
		var bundles []remoteArchiveBundle
		for i, v := range values {
			path := filepath.Join(root, fmt.Sprintf("%d.jsonl", i))
			raw, err := json.Marshal(IntervalSummaryRecord{Timestamp: time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), TestID: "run", JavaHTTP: v})
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, append(raw, '\n'), 0600); err != nil {
				t.Fatal(err)
			}
			bundles = append(bundles, remoteArchiveBundle{target: fmt.Sprint(i), summariesPath: path, manifest: RunArchiveManifest{IntervalSummaryCount: 1}})
		}
		dest := filepath.Join(root, "merged.jsonl")
		if _, err := mergeRemoteArchiveIntervalSummaries(dest, bundles); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(dest)
		if err != nil {
			t.Fatal(err)
		}
		var record IntervalSummaryRecord
		if err = json.Unmarshal(data, &record); err != nil {
			t.Fatal(err)
		}
		if record.JavaHTTP == nil || !record.JavaHTTP.Incomplete || record.JavaHTTP.Actions["request"].Diagnostics.Samples != 1 {
			t.Fatal("legacy source claimed full coverage", reverse, record.JavaHTTP)
		}
	}
}

func TestJavaHTTPTextReportsPreserveResponseAvailabilityAndTiming(t *testing.T) {
	var summary *protocol.JavaHTTPSummary
	for _, state := range []string{"complete", "collection_window", "discarded", "unavailable"} {
		d := javaAPIDiagnostic()
		d.ResponseState = state
		if state == "unavailable" {
			d.ResponseRetainedBytes, d.ResponseBase64Bytes, d.Base64EncodeMs = nil, nil, nil
		}
		if !d.Valid() {
			t.Fatalf("invalid %s fixture", state)
		}
		protocol.MergeJavaHTTPDiagnostic(&summary, d)
	}
	lines := strings.Join(javaHTTPReportLines("request", summary, 4), "\n")
	for _, want := range []string{"Base64 decoding:", "Base64 encoding:", "Enclosing action:", "ms mean", "1 unavailable; 1 limited to a collection window; 1 explicitly discarded"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("report lost %q: %s", want, lines)
		}
	}
	var absent *protocol.JavaHTTPSummary
	d := javaAPIDiagnostic()
	d.InputState, d.RequestEncodedBytes, d.RequestDecodedBytes, d.PreparedRequestBodyBytes = "unsupported_encoding", 0, nil, nil
	d.AppliedRequestCompression, d.ResponseState = "", "unavailable"
	d.Base64DecodeMs, d.Base64EncodeMs, d.ResponseRetainedBytes, d.ResponseBase64Bytes = nil, nil, nil, nil
	protocol.MergeJavaHTTPDiagnostic(&absent, d)
	lines = strings.Join(javaHTTPReportLines("request", absent, 1), "\n")
	for _, want := range []string{"Base64 decoding: unavailable", "Base64 encoding: unavailable"} {
		if !strings.Contains(lines, want) {
			t.Fatalf("report invented timing %q: %s", want, lines)
		}
	}
}

// Neither a same-name wrapper nor a same-named action of another protocol
// enters the Java denominator.
func TestJavaHTTPArchiveSameNameWrapperDoesNotInflatePopulation(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		events := []TransactionEvent{
			{ActionName: "same", Population: "action", DependencyType: "java_http", ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: javaAPIDiagnostic()}},
			{ActionName: "same", Population: "action", DependencyType: "java_http"},
			{ActionName: "same", Population: "action", DependencyType: "http"},
			{ActionName: "same", Population: "transaction", BusinessTransaction: true, ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: javaAPIDiagnostic()}},
			{ActionName: "same", ServerTelemetry: &protocol.ServerTelemetry{JavaHTTP: javaAPIDiagnostic()}},
		}
		if reverse {
			for i, j := 0, len(events)-1; i < j; i, j = i+1, j-1 {
				events[i], events[j] = events[j], events[i]
			}
		}
		var stats RunArchiveActionStats
		var q archiveQuantileAccumulator
		for _, e := range events {
			stats = mergeRunArchiveActionStats(stats, &q, e)
		}
		if stats.Transactions != 5 || stats.JavaHTTP == nil || stats.JavaHTTP.Population != 2 || stats.JavaHTTP.Samples != 1 {
			t.Fatalf("mixed action population: %+v", stats)
		}
		lines := strings.Join(protocolReportLines(&RunArchiveReport{ByAction: map[string]RunArchiveActionStats{"same": stats}}), "\n")
		if !strings.Contains(lines, "1 observed / 2 action samples") || !strings.Contains(lines, "coverage is incomplete") {
			t.Fatal(lines)
		}
	}
}

func TestJavaHTTPReportDoesNotInferMissingActualActionPopulation(t *testing.T) {
	var summary *protocol.JavaHTTPSummary
	protocol.MergeJavaHTTPDiagnostic(&summary, javaAPIDiagnostic())
	lines := strings.Join(protocolReportLines(&RunArchiveReport{ByAction: map[string]RunArchiveActionStats{"legacy": {Transactions: 9, JavaHTTP: summary}}}), "\n")
	if !strings.Contains(lines, "actual-action population is absent or inconsistent") || strings.Contains(lines, "0 observed") || strings.Contains(lines, "9 action samples") {
		t.Fatal(lines)
	}
}
