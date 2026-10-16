package api

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/har"
	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

const (
	syntheticCoveredGeneration   = "11111111111111111111111111111111"
	syntheticUncoveredGeneration = "22222222222222222222222222222222"
)

func syntheticHARSchemaValidate(t *testing.T, raw []byte) {
	t.Helper()
	schemaRaw, err := os.ReadFile(filepath.Join("..", "harsynth", "testdata", "har-1.2.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(schemaRaw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource("har.schema.json", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("har.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	instance, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("synthetic HAR is not JSON: %v", err)
	}
	if err := schema.Validate(instance); err != nil {
		t.Fatalf("HAR 1.2 schema: %v", err)
	}
}

func syntheticEvidence(generation, request string, start time.Time) *protocol.BrowserResourceEvidence {
	evidence := browserResourceV2Fixture()
	old := evidence.SourceGeneration
	for _, id := range []*string{&evidence.SourceGeneration, &evidence.RequestID, &evidence.FrameID, &evidence.DocumentID, &evidence.NavigationID, &evidence.RedirectChainID, &evidence.RedirectFromRequestID, &evidence.InitiatingActionID, &evidence.ObservationActionID} {
		*id = strings.Replace(*id, old, generation, 1)
	}
	evidence.RequestID = request
	ms := float64(start.UnixMilli())
	evidence.EpochStartMS = &ms
	evidence.ObservedAt = start.Add(time.Second)
	return evidence
}

// syntheticArchiveRows are a run's archived transactions: a context with a
// native HAR, a context without one (with a credential header and a token
// in its URL), an older peer's entry without evidence, and an error
// snapshot's entries.
func syntheticArchiveRows(t *testing.T) [][]byte {
	t.Helper()
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []TransactionEvent{
		{Timestamp: at, TestID: "run-1", ActionName: "Covered", PageURL: "https://shop.example/", NetworkEntries: []protocol.NetworkEntry{
			{BrowserEvidence: syntheticEvidence(syntheticCoveredGeneration, syntheticCoveredGeneration+"-request-7", at), URL: "https://shop.example/", Method: "GET", StatusCode: 200, ResourceType: "document"},
			{BrowserEvidence: syntheticEvidence(syntheticCoveredGeneration, syntheticCoveredGeneration+"-request-8", at), URL: "https://shop.example/a.js", Method: "GET", StatusCode: 200, ResourceType: "script"},
		}},
		{Timestamp: at.Add(time.Second), TestID: "run-1", ActionName: "Uncovered", PageURL: "https://shop.example/cart", NetworkEntries: []protocol.NetworkEntry{
			{BrowserEvidence: syntheticEvidence(syntheticUncoveredGeneration, syntheticUncoveredGeneration+"-request-7", at.Add(time.Second)), URL: "https://shop.example/cart?access_token=urlTokenQxZrTm", Method: "GET", StatusCode: 200, ResourceType: "document",
				RequestHeaders: map[string]string{"Authorization": "Bearer headerTokenQxZrTmWp", "Accept": "text/html"}, ResponseHeaders: map[string]string{"Content-Type": "text/html"}},
			{BrowserEvidence: syntheticEvidence(syntheticUncoveredGeneration, syntheticUncoveredGeneration+"-request-8", at.Add(time.Second)), URL: "https://api.shop.example/cart", Method: "POST", FailureKind: "transport", ResourceType: "fetch", RequestBody: `{"sku":"secretBodyQxZr"}`},
		}},
		{Timestamp: at.Add(2 * time.Second), TestID: "run-1", ActionName: "Legacy", NetworkEntries: []protocol.NetworkEntry{{URL: "https://shop.example/legacy", Method: "GET", StatusCode: 304, DurationMs: 12, FailureKind: "peer said privateDetailQxZr"}}},
		{Timestamp: at.Add(3 * time.Second), TestID: "run-1", ActionName: "Failed", Snapshot: &protocol.ErrorSnapshot{PageURL: "https://shop.example/error", NetworkEntries: []protocol.NetworkEntry{{URL: "https://shop.example/error", Method: "GET", StatusCode: 500, DurationMs: 30}}}},
		{Timestamp: at.Add(4 * time.Second), TestID: "run-1", ActionName: "No browser"},
	}
	for _, event := range events[:2] {
		for _, entry := range event.NetworkEntries {
			if entry.BrowserEvidence.Clone() == nil {
				t.Fatal("invalid fixture evidence")
			}
		}
	}
	rows := make([][]byte, 0, len(events))
	for _, event := range events {
		raw, err := json.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, raw)
	}
	return rows
}

func syntheticArchiveManifest() *RunArchiveManifest {
	return &RunArchiveManifest{TestID: "run-1", BrowserHARArtifacts: []RunArchiveEvidenceArtifact{{Name: "browser_har_" + strings.Repeat("a", 32) + ".har", Kind: "har_native", Fidelity: "native", SourceGeneration: strings.Repeat("a", 32), Sequence: 1, SHA256: strings.Repeat("b", 64), Bytes: 10, EvidenceGeneration: syntheticCoveredGeneration}}}
}

type syntheticHARDoc struct {
	Log struct {
		Entries []struct {
			Request struct {
				URL     string              `json:"url"`
				Headers []map[string]string `json:"headers"`
			} `json:"request"`
			Evidence map[string]any `json:"_apexload"`
		} `json:"entries"`
		Apexload map[string]any `json:"_apexload"`
	} `json:"log"`
}

func TestRunArchiveSyntheticHARLeavesNativeContextsOutAndScrubs(t *testing.T) {
	dir := t.TempDir()
	var jsonl bytes.Buffer
	for _, row := range syntheticArchiveRows(t) {
		jsonl.Write(row)
		jsonl.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(dir, "transactions.jsonl"), jsonl.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := syntheticArchiveManifest()
	manifest.TransactionsFile = "transactions.jsonl"
	var out bytes.Buffer
	summary, err := writeRunArchiveSyntheticHAR(context.Background(), dir, manifest, &out)
	if err != nil {
		t.Fatal(err)
	}
	syntheticHARSchemaValidate(t, out.Bytes())
	if summary.Entries != 4 || summary.Omitted[harsynth.OmittedCoveredByHAR] != 2 || summary.SourceFormat != "jsonl" || summary.Completeness != harsynth.CompletenessPartial {
		t.Fatalf("summary %+v", summary)
	}
	// An unclassified failure detail is the sanitizer's to drop, as in the
	// safe transaction download.
	for _, secret := range []string{"urlTokenQxZrTm", "headerTokenQxZrTmWp", "secretBodyQxZr", "privateDetailQxZr"} {
		if bytes.Contains(out.Bytes(), []byte(secret)) {
			t.Fatalf("synthetic HAR retains %q", secret)
		}
	}
	var doc syntheticHARDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, entry := range doc.Log.Entries {
		actions = append(actions, entry.Evidence["action_name"].(string))
	}
	if strings.Join(actions, ",") != "Uncovered,Uncovered,Legacy,Failed" {
		t.Fatalf("entries %v", actions)
	}
	if doc.Log.Entries[0].Evidence["started_source"] != harsynth.StartedRequestStart || doc.Log.Entries[2].Evidence["started_source"] != harsynth.StartedRecordTime || doc.Log.Entries[3].Evidence["page_url"] != "https://shop.example/error" {
		t.Fatalf("entry evidence %+v / %+v / %+v", doc.Log.Entries[0].Evidence, doc.Log.Entries[2].Evidence, doc.Log.Entries[3].Evidence)
	}
	if headers := doc.Log.Entries[0].Request.Headers; len(headers) != 2 {
		t.Fatalf("a JSONL source keeps its sanitized headers: %v", headers)
	}
	// The same archive always yields the same bytes.
	var again bytes.Buffer
	second, err := writeRunArchiveSyntheticHAR(context.Background(), dir, manifest, &again)
	if err != nil || second.SHA256 != summary.SHA256 || !bytes.Equal(again.Bytes(), out.Bytes()) {
		t.Fatalf("a second derivation differs: %v", err)
	}
	// The existing HAR importer accounts for every entry.
	include := true
	converted, err := har.Convert(out.Bytes(), har.ConvertOptions{ScriptName: "synthetic", IncludeStaticAssets: &include})
	if err != nil {
		t.Fatal(err)
	}
	if stats := converted.Stats; int64(stats.TotalRequests) != summary.Entries || int64(stats.ConvertedActions+stats.FilteredAssets+stats.SkippedRequests) != summary.Entries {
		t.Fatalf("import stats %+v for %d entries", stats, summary.Entries)
	}
	if doc.Log.Apexload["native_har_coverage_unknown"] != nil {
		t.Fatalf("coverage unknown with every native HAR naming its context: %v", doc.Log.Apexload)
	}
	// A native HAR that does not name its context makes the overlap unknown,
	// and the HAR says so.
	unnamed := manifest.BrowserHARArtifacts[0]
	unnamed.EvidenceGeneration = ""
	manifest.BrowserHARArtifacts = append(manifest.BrowserHARArtifacts, unnamed)
	var disclosed bytes.Buffer
	if _, err := writeRunArchiveSyntheticHAR(context.Background(), dir, manifest, &disclosed); err != nil {
		t.Fatal(err)
	}
	var disclosedDoc syntheticHARDoc
	if err := json.Unmarshal(disclosed.Bytes(), &disclosedDoc); err != nil {
		t.Fatal(err)
	}
	reasons, _ := disclosedDoc.Log.Apexload["completeness_reasons"].([]any)
	if disclosedDoc.Log.Apexload["native_har_coverage_unknown"] != float64(1) || !slices.Contains(reasons, any("native_har_coverage_unknown")) {
		t.Fatalf("unknown native coverage not disclosed: %v", disclosedDoc.Log.Apexload)
	}
	// Without a native HAR naming its context, every retained entry is kept.
	manifest.BrowserHARArtifacts = nil
	all, err := writeRunArchiveSyntheticHAR(context.Background(), dir, manifest, &bytes.Buffer{})
	if err != nil || all.Entries != 6 || all.Omitted[harsynth.OmittedCoveredByHAR] != 0 {
		t.Fatalf("uncovered summary %+v %v", all, err)
	}
}

func TestRunArchiveSyntheticHARReadsParquetWithoutHeaders(t *testing.T) {
	var fixtures []any
	for _, row := range syntheticArchiveRows(t) {
		fixtures = append(fixtures, resourcePhysicalProjectionFixture{Projection: string(protocol.BrowserResourceArchiveProjection(row))})
	}
	path := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), fixtures)
	manifest := syntheticArchiveManifest()
	manifest.TransactionsParquetFile = filepath.Base(path)
	var out bytes.Buffer
	summary, err := writeRunArchiveSyntheticHAR(context.Background(), filepath.Dir(path), manifest, &out)
	if err != nil {
		t.Fatal(err)
	}
	syntheticHARSchemaValidate(t, out.Bytes())
	if summary.Entries != 4 || summary.Omitted[harsynth.OmittedCoveredByHAR] != 2 || summary.SourceFormat != "parquet" {
		t.Fatalf("summary %+v", summary)
	}
	var doc syntheticHARDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	for _, entry := range doc.Log.Entries {
		if len(entry.Request.Headers) != 0 {
			t.Fatalf("a Parquet-derived entry carries headers: %v", entry.Request.Headers)
		}
	}
	fields, _ := doc.Log.Apexload["omitted_fields"].([]any)
	joined := ""
	for _, field := range fields {
		joined += field.(string) + ","
	}
	if !strings.Contains(joined, "request.headers,") || !strings.Contains(joined, "response.headers,") {
		t.Fatalf("omitted_fields %v", fields)
	}
}

func TestRunArchiveSyntheticHARNeedsATransactionSource(t *testing.T) {
	if _, err := writeRunArchiveSyntheticHAR(context.Background(), t.TempDir(), &RunArchiveManifest{TestID: "run-1"}, &bytes.Buffer{}); err != errSyntheticHARSourceUnavailable {
		t.Fatalf("no source: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "transactions.jsonl"), append(syntheticArchiveRows(t)[0], '\n'), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := writeRunArchiveSyntheticHAR(ctx, dir, &RunArchiveManifest{TestID: "run-1", TransactionsFile: "transactions.jsonl"}, &bytes.Buffer{}); err == nil {
		t.Fatal("a canceled derivation succeeded")
	}
}

func TestReplaySyntheticHARUsesEachEventsEntries(t *testing.T) {
	at := time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)
	events := []engine.ReplayActionEvent{
		{ActionName: "Open", StartedAt: at, PageURL: "https://shop.example/", Network: []protocol.NetworkEntry{{URL: "https://shop.example/", Method: "GET", StatusCode: 200, DurationMs: 20}}},
		{ActionName: "Fail", StartedAt: at.Add(time.Second), Snapshot: &protocol.ErrorSnapshot{PageURL: "https://shop.example/x", NetworkEntries: []protocol.NetworkEntry{{URL: "https://shop.example/x", Method: "GET", StatusCode: 500}}}},
		{ActionName: "Think", StartedAt: at.Add(2 * time.Second)},
	}
	var out bytes.Buffer
	summary, err := writeReplaySyntheticHAR(events, &out)
	if err != nil {
		t.Fatal(err)
	}
	syntheticHARSchemaValidate(t, out.Bytes())
	var doc syntheticHARDoc
	if err := json.Unmarshal(out.Bytes(), &doc); err != nil {
		t.Fatal(err)
	}
	if summary.Entries != 2 || doc.Log.Apexload["capture_mode"] != harsynth.CaptureReplayNetworkEntries || doc.Log.Entries[1].Evidence["page_url"] != "https://shop.example/x" {
		t.Fatalf("replay summary %+v %#v", summary, doc.Log.Apexload)
	}
}

// A native HAR names the browser evidence generation of the context it
// recorded, so the archive's synthetic HAR leaves that context out; a
// malformed one is recorded as unknown, and a declared one must be valid.
func TestNativeHARRecordsItsEvidenceGeneration(t *testing.T) {
	// Native HAR staging needs private-ownership checks that exist only on
	// darwin and linux (pkg/protocol/browser/har_working_security_*.go); other
	// platforms refuse it by design, so the archive omits the HAR there.
	if runtime.GOOS != "darwin" && runtime.GOOS != "linux" {
		t.Skip("native HAR staging is unavailable on " + runtime.GOOS)
	}
	t.Setenv("APEXLOAD_ARCHIVE_DIR", t.TempDir())
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	writer, err := newRunArchiveWriter(&ManagedTest{ID: "evidence-generation-run", Name: "local"}, RunArchiveProvenance{})
	if err != nil {
		t.Fatal(err)
	}
	harDir := t.TempDir()
	for k, evidence := range []string{syntheticCoveredGeneration, "NOT-A-GENERATION"} {
		generation := strings.Repeat(string(rune('3'+k)), 32)
		path := filepath.Join(harDir, "apexload_browser_"+generation+"_scrubbed.har")
		if err := os.WriteFile(path, []byte(`{"log":{"entries":[]}}`), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := writer.RecordBrowserHARArtifact(protocol.BrowserHARArtifact{Path: path, Generation: generation, EvidenceGeneration: evidence}); err != nil {
			t.Fatal(err)
		}
	}
	record, err := writer.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := loadRunArchiveManifestFromRecord(record)
	if err != nil || len(manifest.BrowserHARArtifacts) != 2 {
		t.Fatalf("HARs %+v %v", manifest.BrowserHARArtifacts, err)
	}
	if manifest.BrowserHARArtifacts[0].EvidenceGeneration != syntheticCoveredGeneration || manifest.BrowserHARArtifacts[1].EvidenceGeneration != "" {
		t.Fatalf("evidence generations %+v", manifest.BrowserHARArtifacts)
	}
	// The HAR whose generation was malformed covers an unknown context.
	if covered, unknown := nativeHARCoveredGenerations(manifest); len(covered) != 1 || !covered[syntheticCoveredGeneration] || unknown != 1 {
		t.Fatalf("covered %v unknown %d", covered, unknown)
	}
	declared := manifest.BrowserHARArtifacts[0]
	if !validDeclaredHAR(declared) {
		t.Fatal("a valid declaration was refused")
	}
	declared.EvidenceGeneration = "NOT-A-GENERATION"
	if validDeclaredHAR(declared) {
		t.Fatal("a malformed evidence generation was declared valid")
	}
}

// A legacy Parquet file keeps network entries with their headers; a
// Parquet-derived HAR still carries none, as it says.
func TestRunArchiveSyntheticHARDropsLegacyParquetHeaders(t *testing.T) {
	entries, _ := json.Marshal([]protocol.NetworkEntry{{URL: "https://shop.example/legacy", Method: "GET", StatusCode: 200, RequestHeaders: map[string]string{"X-Legacy": "kept-in-parquet"}}})
	path := resourcePhysicalWrite(t, new(resourcePhysicalLegacyFixture), []any{resourcePhysicalLegacyFixture{Timestamp: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC).UnixMilli(), Action: "Legacy", Entries: string(entries), Snapshot: "null"}})
	manifest := &RunArchiveManifest{TestID: "run-1", TransactionsParquetFile: filepath.Base(path)}
	var out bytes.Buffer
	summary, err := writeRunArchiveSyntheticHAR(context.Background(), filepath.Dir(path), manifest, &out)
	if err != nil {
		t.Fatal(err)
	}
	syntheticHARSchemaValidate(t, out.Bytes())
	if summary.Entries != 1 || bytes.Contains(out.Bytes(), []byte("kept-in-parquet")) {
		t.Fatalf("legacy Parquet entry kept its headers: %s", out.Bytes())
	}
}
