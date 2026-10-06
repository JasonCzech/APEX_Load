package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const businessKeyTestMasterKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY"

// businessKeyServer is a test server whose controller can wrap keys, in a
// project that uses business keys.
func businessKeyServer(t *testing.T) (*Server, *storage.SQLiteStore) {
	t.Helper()
	t.Setenv("APEXLOAD_WRAPPING_MASTER_KEY", businessKeyTestMasterKey)
	t.Setenv("APEXLOAD_BUSINESS_KEY_PROJECTS", storage.DefaultProjectID)
	server, store := baselineTestServer(t)
	if server.runner.continuityKeyWrapper == nil {
		t.Fatal("the test controller has no key wrapper")
	}
	return server, store
}

func TestBusinessKeyDigestsAreProjectKeyedAndStable(t *testing.T) {
	server, store := businessKeyServer(t)
	now := time.Now().UTC()
	if err := store.SaveProject(&storage.Project{ID: "other", Name: "Other", CreatedAt: now, UpdatedAt: now}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	d1, err := server.runner.businessKeyDigester(ctx, storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	// A fresh runner reads the stored key back: digests stay joinable.
	server.runner.businessKeys = sync.Map{}
	d2, err := server.runner.businessKeyDigester(ctx, storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := server.runner.businessKeyDigester(ctx, "other")
	if err != nil {
		t.Fatal(err)
	}
	a := d1.digest("order_id", "ORD-1")
	if !strings.HasPrefix(a, businessKeyDigestPrefix) || a != d2.digest("order_id", "ORD-1") || a == d1.digest("account", "ORD-1") || a == other.digest("order_id", "ORD-1") {
		t.Fatalf("digests: %s %s %s", a, d2.digest("order_id", "ORD-1"), other.digest("order_id", "ORD-1"))
	}
	server.runner.continuityKeyWrapper = nil
	server.runner.businessKeys = sync.Map{}
	if _, err := server.runner.businessKeyDigester(ctx, storage.DefaultProjectID); err == nil {
		t.Fatal("digests without a key wrapper")
	}
}

func TestBusinessKeyPolicyAndArchiver(t *testing.T) {
	server, store := businessKeyServer(t)
	approveBusinessKeyMapping(t, server, store)
	redact := true
	specs := []engine.GroupConfig{
		{Name: "shop", Script: &dsl.Script{}},
		{Name: "secure", Script: &dsl.Script{Config: dsl.ScriptConfig{Log: dsl.LogConfig{RedactRunEvidence: &redact}}}},
	}
	policy := server.runner.runArchiveBusinessKeys(storage.DefaultProjectID, []TestGroup{{Name: "shop"}, {Name: "secure"}, {Name: "unknown"}}, specs)
	if policy == nil || strings.Join(policy.Extractions, ",") != "order_id" || strings.Join(policy.DigestOnlyGroups, ",") != "secure,unknown" {
		t.Fatalf("policy: %+v", policy)
	}
	if server.runner.runArchiveBusinessKeys("other", nil, nil) != nil {
		t.Fatal("a project without business keys got a policy")
	}

	archiver := server.runner.newBusinessKeyArchiver(context.Background(), storage.DefaultProjectID, policy)
	shared := map[string]string{"order_id": "ORD-1", "cart": "c1"}
	kept := TransactionEvent{Group: "shop", Extractions: shared}
	archiver.apply(&kept)
	if kept.Extractions["order_id"] != "ORD-1" || !strings.HasPrefix(kept.ExtractionDigests["order_id"], businessKeyDigestPrefix) {
		t.Fatalf("kept: %+v", kept)
	}
	dropped := TransactionEvent{Group: "secure", Extractions: shared}
	archiver.apply(&dropped)
	if dropped.Extractions["order_id"] != scrub.Redacted || dropped.Extractions["cart"] != "c1" || dropped.ExtractionDigests["order_id"] != kept.ExtractionDigests["order_id"] || shared["order_id"] != "ORD-1" {
		t.Fatalf("dropped: %+v (shared %v)", dropped, shared)
	}
	if archiver.unavailable {
		t.Fatal("digests were available")
	}

	// Without a wrapper a digest-only group still drops its values, and the
	// archive says they cannot be matched.
	server.runner.continuityKeyWrapper = nil
	server.runner.businessKeys = sync.Map{}
	blind := server.runner.newBusinessKeyArchiver(context.Background(), storage.DefaultProjectID, policy)
	row := TransactionEvent{Group: "secure", Extractions: map[string]string{"order_id": "ORD-2"}}
	blind.apply(&row)
	if row.Extractions["order_id"] != scrub.Redacted || row.ExtractionDigests != nil || !blind.unavailable {
		t.Fatalf("blind: %+v", row)
	}
}

func TestRunArchiveWriterDigestsBusinessKeysBeforeScrubbing(t *testing.T) {
	server, store := businessKeyServer(t)
	approveBusinessKeyMapping(t, server, store)
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	policy := &RunArchiveBusinessKeys{Extractions: []string{"order_id"}, DigestOnlyGroups: []string{"secure"}}
	w, err := newRunArchiveWriter(&ManagedTest{ID: "writer-run"}, RunArchiveProvenance{BusinessKeys: policy})
	if err != nil {
		t.Fatal(err)
	}
	w.businessKeys = server.runner.newBusinessKeyArchiver(context.Background(), storage.DefaultProjectID, policy)
	result := &protocol.Result{Group: "secure", ActionName: "pay", StartTime: time.Now().UTC(), Success: true, Extractions: map[string]string{"order_id": "ORD-9"}}
	if err := w.RecordResult(result); err != nil {
		t.Fatal(err)
	}
	if result.Extractions["order_id"] != "ORD-9" {
		t.Fatal("the live result was modified")
	}
	record, err := w.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	rows := readArchivedTransactions(t, record)
	digester, _ := server.runner.businessKeyDigester(context.Background(), storage.DefaultProjectID)
	if len(rows) != 1 || rows[0].Extractions["order_id"] != scrub.Redacted || rows[0].ExtractionDigests["order_id"] != digester.digest("order_id", "ORD-9") {
		t.Fatalf("rows: %+v", rows)
	}
}

// readArchivedTransactions reads a run archive's transaction rows.
func readArchivedTransactions(t *testing.T, record *storage.RunArchiveRecord) []TransactionEvent {
	t.Helper()
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil {
		t.Fatal(err)
	}
	var rows []TransactionEvent
	if err := forEachArchiveRecordContext(context.Background(), record, func(m *RunArchiveManifest) string { return m.TransactionsFile }, func(row *TransactionEvent) error {
		rows = append(rows, *row)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = manifest
	return rows
}

// approveBusinessKeyMapping saves an elastic-query source and approves the
// order_id → order.id mapping on it.
func approveBusinessKeyMapping(t *testing.T, server *Server, store *storage.SQLiteStore) string {
	t.Helper()
	connection := &storage.ELKConnection{ID: "elk-bk", ProjectID: storage.DefaultProjectID, Name: "Logs", Enabled: true, Endpoint: "https://elk.example", IndexPattern: "logs-*", TimestampField: "@timestamp", TraceIDField: "trace.id", AuthType: storage.ELKAuthAPIKey, APIKeySecretName: "elk-token"}
	if err := store.CreateELKConnection(connection); err != nil {
		t.Fatal(err)
	}
	source := storage.MonitorSource{ID: "elastic-bk", ProjectID: storage.DefaultProjectID, Name: "Order logs", Enabled: true, Pack: "elastic-query", ExecutorMode: "direct", ConnectionID: connection.ID, IntervalSeconds: 30, ElasticQuery: &storage.ElasticQuerySelection{ProjectID: storage.DefaultProjectID}}
	if _, err := store.SaveMonitorSource(source, 0); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/analysis/business-key-mappings?project_id=" + storage.DefaultProjectID
	bad := timeslotRequest(t, server, http.MethodPost, path, map[string]any{"name": "orders", "definition": map[string]any{"extraction": "order_id", "source_id": "missing", "field": "order.id"}})
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("unknown source: %d %s", bad.Code, bad.Body.String())
	}
	reply := timeslotRequest(t, server, http.MethodPost, path, map[string]any{"name": "orders", "definition": map[string]any{"extraction": "order_id", "source_id": source.ID, "field": "order.id", "label": "Order ID"}})
	var saved storage.AnalysisDefinition
	if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &saved) != nil || saved.Kind != storage.AnalysisDefinitionKindBusinessKeyMapping {
		t.Fatalf("approve: %d %s", reply.Code, reply.Body.String())
	}
	return saved.ID
}

// businessKeyLogs is an Elasticsearch fake that answers one document
// search per order.id value.
type businessKeyLogs struct {
	mu       sync.Mutex
	requests []map[string]any
	hits     []map[string]any
	fail     bool
}

func (f *businessKeyLogs) RoundTrip(r *http.Request) (*http.Response, error) {
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || r.Header.Get("Authorization") != "ApiKey fixture-token" {
		return &http.Response{StatusCode: 400, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, body)
	if f.fail {
		return &http.Response{StatusCode: 503, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	wanted := ""
	for _, filter := range body["query"].(map[string]any)["bool"].(map[string]any)["filter"].([]any) {
		if term, ok := filter.(map[string]any)["term"].(map[string]any); ok {
			wanted = term["order.id"].(string)
		}
	}
	size := int(body["size"].(float64))
	var hits []any
	total := 0
	for _, hit := range f.hits {
		if fmt.Sprint(hit["order"].(map[string]any)["id"]) == wanted {
			total++
			if len(hits) < size {
				hits = append(hits, map[string]any{"_source": hit})
			}
		}
	}
	out, _ := json.Marshal(map[string]any{"timed_out": false, "hits": map[string]any{"total": map[string]any{"value": total, "relation": "eq"}, "hits": hits}})
	return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(out)))}, nil
}

func TestBusinessKeyLookupJoinsRequestsAndLogsWithoutKeepingValues(t *testing.T) {
	server, store := businessKeyServer(t)
	if server.appDynamicsCancel != nil {
		server.appDynamicsCancel()
	}
	mappingID := approveBusinessKeyMapping(t, server, store)
	digester, err := server.runner.businessKeyDigester(context.Background(), storage.DefaultProjectID)
	if err != nil {
		t.Fatal(err)
	}
	// Orders ORD-0..ORD-4 repeat; the secure group's rows kept only digests.
	record := exemplarArchiveFixture(t, server, store, "bk-run", func(i int, tx *TransactionEvent) {
		value := fmt.Sprintf("ORD-%d", i%5)
		tx.Extractions = map[string]string{"order_id": value}
		if i%2 == 1 {
			tx.Group = "secure"
			tx.Extractions["order_id"] = scrub.Redacted
			tx.ExtractionDigests = map[string]string{"order_id": digester.digest("order_id", value)}
		}
	})
	writeArchiveSidecar(t, record, "resources.jsonl", []ResourceSample{}, func(m *RunArchiveManifest) {
		m.Provenance.BusinessKeys = &RunArchiveBusinessKeys{Extractions: []string{"order_id"}, DigestOnlyGroups: []string{"secure"}}
	})
	// The run was monitored by the mapped source.
	t.Setenv("APEXLOAD_RUN_MONITOR_PROJECTS", storage.DefaultProjectID)
	run, err := store.GetTest("bk-run")
	if err != nil {
		t.Fatal(err)
	}
	run.MonitorBindings = &storage.MonitorBindings{Sources: []storage.MonitorBinding{{SourceID: "elastic-bk"}}}
	if err := store.SaveTest(run); err != nil {
		t.Fatal(err)
	}
	if jobs, err := store.StartRunMonitors(storage.DefaultProjectID, "bk-run", "", exemplarFixtureStart, exemplarFixtureStart.Add(time.Hour)); err != nil || len(jobs) != 1 {
		t.Fatal(jobs, err)
	}
	// ORD-3 is busy in the logs; it must not crowd ORD-1 out.
	logs := &businessKeyLogs{}
	for i := 0; i < 80; i++ {
		logs.hits = append(logs.hits, map[string]any{"@timestamp": exemplarFixtureStart.Add(time.Duration(i) * time.Second).Format(time.RFC3339Nano), "order": map[string]any{"id": "ORD-3"}, "message": "busy order"})
	}
	logs.hits = append(logs.hits, map[string]any{"@timestamp": exemplarFixtureStart.Add(90 * time.Second).Format(time.RFC3339Nano), "order": map[string]any{"id": "ORD-1"}, "trace": map[string]any{"id": "abc123"}, "message": "payment captured for ORD-1"})
	server.elasticHTTPClient = &http.Client{Transport: logs}
	server.runner.projectSecretStore = &devSecretStore{value: "fixture-token"}

	path := "/api/v1/tests/bk-run/archive/business-keys/lookup"
	reply := timeslotRequest(t, server, http.MethodPost, path, map[string]any{"mapping_id": mappingID, "values": []string{"ORD-1", "ORD-404", "ORD-3"}})
	var out BusinessKeyLookupResponse
	if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &out) != nil {
		t.Fatalf("lookup: %d %s", reply.Code, reply.Body.String())
	}
	if strings.Contains(reply.Body.String(), "ORD-404") || len(out.Results) != 3 || len(out.Limitations) != 0 || out.Mapping.Label != "Order ID" {
		t.Fatalf("response: %s", reply.Body.String())
	}
	found, missing := out.Results[0], out.Results[1]
	// ORD-1 is every fifth row from row 1: rows 1, 11, 21, ... are secure
	// (digest) and rows 6, 16, 26, ... kept the value.
	byDigest, byValue := 0, 0
	for _, m := range found.Requests.Matches {
		switch m.MatchedBy {
		case "digest":
			byDigest++
		case "value":
			byValue++
		}
	}
	if found.Requests.Total != 72 || byDigest == 0 || byValue == 0 || found.Logs.Status != businessKeyLogsFound || found.Logs.Total != 1 ||
		found.Logs.Entries[0].TraceID != "abc123" || found.Logs.Entries[0].Message != "payment captured for ORD-1" {
		t.Fatalf("found: %+v", found)
	}
	if missing.Requests.Total != 0 || missing.Logs.Status != businessKeyLogsNotFound {
		t.Fatalf("missing: %+v", missing)
	}
	if busy := out.Results[2]; busy.Logs.Total != 80 || len(busy.Logs.Entries) != maxBusinessKeyLogEntries {
		t.Fatalf("busy: total %d, %d entries", busy.Logs.Total, len(busy.Logs.Entries))
	}
	if len(logs.requests) != 3 {
		t.Fatalf("%d searches, want one per value", len(logs.requests))
	}
	// A failed search is reported per value, without the values.
	logs.fail = true
	reply = timeslotRequest(t, server, http.MethodPost, path, map[string]any{"mapping_id": mappingID, "values": []string{"ORD-1"}})
	if json.Unmarshal(reply.Body.Bytes(), &out) != nil || out.Results[0].Logs.Status != businessKeyLogsError || out.Results[0].Requests.Total != 72 || strings.Contains(reply.Body.String(), "ORD-1\"") {
		t.Fatalf("failed search: %s", reply.Body.String())
	}

	for name, body := range map[string]map[string]any{
		"no values":  {"mapping_id": mappingID, "values": []string{}},
		"too many":   {"mapping_id": mappingID, "values": strings.Split(strings.Repeat("v,", 21), ",")[:21]},
		"blank":      {"mapping_id": mappingID, "values": []string{" "}},
		"oversized":  {"mapping_id": mappingID, "values": []string{strings.Repeat("x", 257)}},
		"no mapping": {"mapping_id": "missing", "values": []string{"ORD-1"}},
	} {
		if reply := timeslotRequest(t, server, http.MethodPost, path, body); reply.Code != http.StatusBadRequest && reply.Code != http.StatusNotFound {
			t.Errorf("%s: %d %s", name, reply.Code, reply.Body.String())
		}
	}
	t.Setenv("APEXLOAD_BUSINESS_KEY_PROJECTS", "")
	if reply := timeslotRequest(t, server, http.MethodPost, path, map[string]any{"mapping_id": mappingID, "values": []string{"ORD-1"}}); reply.Code != http.StatusConflict {
		t.Fatalf("gate off: %d", reply.Code)
	}
	if reply := timeslotRequest(t, server, http.MethodGet, "/api/v1/analysis/business-key-mappings?project_id="+storage.DefaultProjectID, nil); reply.Code != http.StatusConflict {
		t.Fatalf("mappings with the gate off: %d", reply.Code)
	}
}

func TestBusinessKeyLookupReportsWhatItCannotMatch(t *testing.T) {
	server, store := businessKeyServer(t)
	if server.appDynamicsCancel != nil {
		server.appDynamicsCancel()
	}
	mappingID := approveBusinessKeyMapping(t, server, store)
	record := exemplarArchiveFixture(t, server, store, "bk-old-run", func(i int, tx *TransactionEvent) {
		tx.Extractions = map[string]string{"order_id": fmt.Sprintf("ORD-%d", i%5)}
	})
	writeArchiveSidecar(t, record, "resources.jsonl", []ResourceSample{}, func(m *RunArchiveManifest) { m.BusinessKeyDigestsUnavailable = true })
	reply := timeslotRequest(t, server, http.MethodPost, "/api/v1/tests/bk-old-run/archive/business-keys/lookup", map[string]any{"mapping_id": mappingID, "values": []string{"ORD-2"}})
	var out BusinessKeyLookupResponse
	if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &out) != nil {
		t.Fatalf("lookup: %d %s", reply.Code, reply.Body.String())
	}
	// Archived before the mapping, and partly without digests; matched by
	// value; the source never monitored the run.
	if len(out.Limitations) != 2 || out.Results[0].Requests.Total != 72 || out.Results[0].Requests.Matches[0].MatchedBy != "value" ||
		out.Results[0].Logs.Status != businessKeyLogsUnavailable || !strings.Contains(out.Results[0].Logs.Detail, "did not monitor") {
		t.Fatalf("response: %s", reply.Body.String())
	}
}

func TestRemoteArchiveMergeDigestsOnTheControllerOnly(t *testing.T) {
	server, _ := businessKeyServer(t)
	policy := &RunArchiveBusinessKeys{Extractions: []string{"order_id"}, DigestOnlyGroups: []string{"secure"}}
	root := t.TempDir()
	source := filepath.Join(root, "source.jsonl")
	var lines []byte
	for _, event := range []TransactionEvent{
		{Timestamp: time.Now().UTC(), TestID: "remote", Group: "secure", ActionName: "pay", Success: true, Extractions: map[string]string{"order_id": "ORD-7"}, ExtractionDigests: map[string]string{"order_id": "bk1:forged"}},
		{Timestamp: time.Now().UTC(), TestID: "remote", Group: "shop", ActionName: "browse", Success: true, ExtractionDigests: map[string]string{"order_id": "bk1:forged"}},
	} {
		line, _ := json.Marshal(event)
		lines = append(append(lines, line...), '\n')
	}
	if err := os.WriteFile(source, lines, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := RunArchiveManifest{businessKeys: server.runner.newBusinessKeyArchiver(context.Background(), storage.DefaultProjectID, policy)}
	destination := filepath.Join(root, "merged.jsonl")
	bundles := []remoteArchiveBundle{{target: "generator-a", region: "us-east", transactionsPath: source, manifest: RunArchiveManifest{TransactionCount: 2}}}
	if err := mergeRemoteArchiveTransactions(destination, &manifest, bundles, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	var merged []TransactionEvent
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var event TransactionEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		merged = append(merged, event)
	}
	digester, _ := server.runner.businessKeyDigester(context.Background(), storage.DefaultProjectID)
	if merged[0].Extractions["order_id"] != scrub.Redacted || merged[0].ExtractionDigests["order_id"] != digester.digest("order_id", "ORD-7") || merged[1].ExtractionDigests != nil || manifest.BusinessKeyDigestsUnavailable {
		t.Fatalf("merged: %+v", merged)
	}
}

func TestSanitizeBusinessKeyDigests(t *testing.T) {
	got := sanitizeBusinessKeyDigests(map[string]string{"order_id": "bk1:abc", "bad name!": "bk1:abc", "other": "plain", "long": "bk1:" + strings.Repeat("a", 80)})
	if len(got) != 1 || got["order_id"] != "bk1:abc" || sanitizeBusinessKeyDigests(map[string]string{"x": "y"}) != nil {
		t.Fatalf("%v", got)
	}
}
