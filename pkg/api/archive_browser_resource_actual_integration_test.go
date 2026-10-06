package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/protocol/browser"
	"github.com/jasonczech/apexload/pkg/storage"
)

// Qualification requires the pinned installed browser runtime. An explicitly
// requested runtime failure is a failure, never an acceptance skip. This proves
// acquisition of retained observations, not a complete browser request census.
func TestBrowserResourceActualChromiumAcquisitionArchiveAndPagination(t *testing.T) {
	switch strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_TESTS")) {
	case "require":
	case "", "auto", "skip":
		t.Skip("archive acquisition qualification requires APEXLOAD_BROWSER_TESTS=require")
	default:
		t.Fatal("invalid APEXLOAD_BROWSER_TESTS value; use require, auto or skip")
	}
	const assetCount = 80
	var mu sync.Mutex
	requests := make(map[string]int)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests[r.URL.Path]++
		mu.Unlock()
		w.Header().Set("Cache-Control", "no-store")
		switch {
		case r.URL.Path == "/":
			w.Header().Set("Content-Type", "text/html")
			_, _ = fmt.Fprint(w, `<!doctype html><html><head><title>owned retained resources</title><link rel="icon" href="data:,">`)
			for i := 0; i < assetCount; i++ {
				_, _ = fmt.Fprintf(w, `<link rel="stylesheet" href="/asset/%03d.css">`, i)
			}
			_, _ = fmt.Fprint(w, `</head><body>controlled retained resource fixture</body></html>`)
		case strings.HasPrefix(r.URL.Path, "/asset/"):
			w.Header().Set("Content-Type", "text/css")
			// A successful empty response gives a genuine measured zero body size.
			w.WriteHeader(http.StatusOK)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(fixture.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	runtime := &browser.Browser{}
	if err := runtime.Init(ctx, map[string]any{
		"base_url": fixture.URL, "browser_engine": "chromium", "browser_headless": true,
		"browser_capture_document_metrics": true, "browser_runtime_new_user_policy": "persistent_context",
		"browser_viewport_width": 1280, "browser_viewport_height": 900,
	}); err != nil {
		t.Fatalf("explicit actual Chromium runtime initialization failed: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanupCancel()
		if err := runtime.Teardown(cleanupCtx); err != nil {
			t.Errorf("owned Chromium cleanup failed: %v", err)
		}
	})
	result, err := runtime.Execute(ctx, &protocol.Action{Name: "load controlled resources", Type: "browser_navigate", Target: "/", Params: map[string]any{"wait_until": "load"}})
	if err != nil || result == nil || !result.Success {
		t.Fatalf("actual controlled navigation failed: %v", err)
	}
	t.Logf("actual action source_timestamp=%s", result.StartTime.Format(time.RFC3339Nano))
	if len(result.NetworkEntries) <= 50 {
		t.Fatalf("actual navigation retained only%d entries; >50 acquisition acceptance was not reached", len(result.NetworkEntries))
	}
	result.Group = "owned-chromium"
	result.Region = "owned-local"
	// Read the actual launched engine/version through the local evidence provider.
	// This read does not generate target traffic or execute a health probe.
	var documentEvidence *protocol.BrowserDocumentEvidence
	var browserVersion string
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		documentEvidence = runtime.BrowserDocumentEvidence()
		if documentEvidence != nil {
			for _, observation := range documentEvidence.Observations {
				if observation.Engine == "chromium" && observation.Cohort != nil {
					browserVersion = observation.Cohort.BrowserVersion
				}
			}
		}
		if browserVersion != "" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if browserVersion == "" || documentEvidence == nil || documentEvidence.Clone() == nil {
		t.Fatal("actual browser version/evidence provider was unavailable")
	}
	mu.Lock()
	counts := make(map[string]int, len(requests))
	for path, count := range requests {
		counts[path] = count
	}
	mu.Unlock()
	if counts["/"] != 1 || len(counts) != assetCount+1 {
		t.Fatalf("controlled actual request count changed: unique%d root%d", len(counts), counts["/"])
	}
	for i := 0; i < assetCount; i++ {
		if counts[fmt.Sprintf("/asset/%03d.css", i)] != 1 {
			t.Fatal("a controlled resource was not requested exactly once")
		}
	}
	byRequestID := make(map[string]*protocol.BrowserResourceEvidence)
	zeroBytes, measuredFalse := 0, 0
	for _, entry := range result.NetworkEntries {
		evidence := entry.BrowserEvidence.Clone()
		if evidence == nil || evidence.RequestID == "" || byRequestID[evidence.RequestID] != nil {
			t.Fatal("actual retained resource identity/evidence was missing, malformed or duplicated")
		}
		byRequestID[evidence.RequestID] = evidence
		if evidence.Bytes.ResponseEncodedBody != nil && *evidence.Bytes.ResponseEncodedBody == 0 {
			zeroBytes++
		}
		if evidence.ServiceWorker != nil && !*evidence.ServiceWorker {
			measuredFalse++
		}
	}
	if zeroBytes == 0 || measuredFalse == 0 {
		t.Fatal("actual native zero-body/measured-false observations were unavailable")
	}
	t.Logf("actual acquisition engine=chromium browser_version=%s viewport=1280x900 fixture_unique_requests=%d retained_entries=%d measured_zero_body=%d measured_service_worker_false=%d", browserVersion, len(counts), len(result.NetworkEntries), zeroBytes, measuredFalse)

	root := t.TempDir()
	t.Setenv("APEXLOAD_ARCHIVE_DIR", root)
	t.Setenv("APEXLOAD_ARCHIVE_BACKEND", archiveBackendFilesystem)
	const runID = "owned-browser-resource-run"
	managed := &ManagedTest{ID: runID, Name: "Owned Chromium resource acceptance", ProjectID: storage.DefaultProjectID, Groups: []TestGroup{{BrowserDocumentCaptureRequested: true}}}
	archiveWriter, err := newRunArchiveWriter(managed, RunArchiveProvenance{ProjectID: storage.DefaultProjectID, RequestedAt: result.StartTime})
	if err != nil {
		t.Fatal(err)
	}
	finalized := false
	t.Cleanup(func() {
		if !finalized {
			_, _ = archiveWriter.Finalize("FAILED", time.Now().UTC(), "owned fixture stopped before publication", false, false, nil)
		}
	})
	if err := archiveWriter.RecordResult(result); err != nil {
		t.Fatal(err)
	}
	if err := archiveWriter.RecordBrowserDocumentEvidence(documentEvidence); err != nil {
		t.Fatal(err)
	}
	archive, err := archiveWriter.Finalize("COMPLETE", time.Now().UTC(), "", false, false, nil)
	finalized = true // Finalize always joins its worker and closes owned files.
	if err != nil {
		t.Fatal(err)
	}
	if archive.TransactionCount != 1 {
		t.Fatal("independent document evidence became an action transaction")
	}
	server, store := resourceAuthTestServer(t, authModeDev)
	if err := store.SaveTest(&storage.TestRecord{ID: runID, Name: managed.Name, ProjectID: storage.DefaultProjectID, Status: "COMPLETE", StartTime: result.StartTime, MaterializationKey: "owned-real-browser-incarnation"}); err != nil {
		t.Fatal(err)
	}
	if err := store.SaveRunArchive(archive); err != nil {
		t.Fatal(err)
	}
	jsonl := resourceActualReadAllPages(t, server, runID, "jsonl", nil)
	if len(jsonl) != len(result.NetworkEntries) {
		t.Fatal("actual archive/reader lost retained observations")
	}
	for _, item := range jsonl {
		if item.EvidenceState != "available" || item.BrowserEvidence == nil || !reflect.DeepEqual(item.BrowserEvidence, byRequestID[item.BrowserEvidence.RequestID]) || item.Timestamp == nil || !item.Timestamp.Equal(result.StartTime) || item.TimestampPrecision != "rfc3339_nanosecond" {
			t.Fatal("archive reading changed actual native identity, zero/false or source timestamp")
		}
	}
	filtered := resourceActualReadAllPages(t, server, runID, "jsonl", url.Values{"request_id": {jsonl[0].BrowserEvidence.RequestID}})
	if len(filtered) != 1 || filtered[0].Locator != jsonl[0].Locator {
		t.Fatal("exact actual native identity filter changed retained selection")
	}
	for _, sortBy := range []string{"native_start_ms", "response_encoded_body"} {
		q := resourcePageTestQuery(t, url.Values{"sort_by": {sortBy}})
		expected := append([]BrowserResourcePageItem(nil), jsonl...)
		sort.Slice(expected, func(i, j int) bool {
			return browserResourceKeyBefore(browserResourceItemSortKey(expected[i], q), browserResourceItemSortKey(expected[j], q), q)
		})
		actual := resourceActualReadAllPages(t, server, runID, "jsonl", url.Values{"sort_by": {sortBy}})
		resourceActualAssertNativeOrder(t, actual, sortBy)
		if !reflect.DeepEqual(actual, expected) {
			t.Fatal("actual native sort/continuation changed stable retained selection")
		}
	}

	// Select the actual production Parquet artifact through a separate fixture
	// publication. The original JSONL and both producer artifacts stay unchanged.
	manifestRaw, err := os.ReadFile(archive.ManifestPath)
	if err != nil {
		t.Fatal(err)
	}
	var manifest RunArchiveManifest
	if err := json.Unmarshal(manifestRaw, &manifest); err != nil || manifest.TransactionsParquetFile == "" {
		t.Fatal("production physical transaction artifact was not published")
	}
	manifest.TransactionsFile = ""
	parquetManifestRaw, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(archive.ManifestPath, parquetManifestRaw, 0600); err != nil {
		t.Fatal(err)
	}
	archive.ManifestSHA256 = sha256BytesHex(parquetManifestRaw)
	if err := store.SaveRunArchive(archive); err != nil {
		t.Fatal(err)
	}
	physical := resourceActualReadAllPages(t, server, runID, "parquet", nil)
	if !reflect.DeepEqual(physical, jsonl) {
		t.Fatal("actual production JSONL/Parquet projections changed retained native presence, precision or paging")
	}
	mu.Lock()
	unchangedTargetTraffic := reflect.DeepEqual(counts, requests)
	mu.Unlock()
	if !unchangedTargetTraffic {
		t.Fatal("archived resource reads changed controlled target traffic")
	}
	t.Logf("actual acquisition archive proof transaction_records=1 jsonl_and_physical_parquet_retained=%d first_page=50 continuation=%d producer_hashes_verified=true", len(jsonl), len(jsonl)-50)
}

func resourceActualReadAllPages(t *testing.T, server *Server, runID, expectedFormat string, filters url.Values) []BrowserResourcePageItem {
	t.Helper()
	values := make(url.Values, len(filters))
	for key, entries := range filters {
		values[key] = append([]string(nil), entries...)
	}
	var items []BrowserResourcePageItem
	seen := make(map[BrowserResourcePageLocator]bool)
	var sourceRevision string
	for pageNumber := 0; pageNumber < 4; pageNumber++ {
		request := resourceHandlerRequest(runID)
		request.URL.RawQuery = values.Encode()
		response := httptest.NewRecorder()
		server.handleGetRunArchiveBrowserResources(response, request)
		if response.Code != http.StatusOK {
			t.Fatalf("actual archived resource page status%d: %s", response.Code, response.Body.String())
		}
		page, err := DecodeBrowserResourcePage(response.Body.Bytes())
		if err != nil || page.SourceIntegrity != "verified_manifest_and_artifact" || page.SourceFormat != expectedFormat || page.SourceTimestampPrecision != "rfc3339_nanosecond" || page.Population.Availability != "retained_observations" || page.Population.TransactionRecords != 1 || page.Population.RetainedEntryObservations == nil || page.Population.MatchedRetainedObservations == nil {
			t.Fatal("actual published resource page contract/integrity was unavailable")
		}
		if sourceRevision != "" && sourceRevision != page.SourceRevision {
			t.Fatal("actual continuation changed source revision")
		}
		sourceRevision = page.SourceRevision
		for _, item := range page.Items {
			if seen[item.Locator] {
				t.Fatal("actual continuation duplicated a retained observation")
			}
			seen[item.Locator] = true
			items = append(items, item)
		}
		if page.NextCursor == "" {
			if int64(len(items)) != *page.Population.MatchedRetainedObservations {
				t.Fatal("actual continuation did not exhaust matched retained observations")
			}
			return items
		}
		if len(page.Items) != 50 {
			t.Fatal("actual continuation page did not honor the admitted default limit")
		}
		values.Set("cursor", page.NextCursor)
	}
	t.Fatal("bounded actual resource fixture exceeded four pages")
	return nil
}

// This checks native values directly, independently from the production sort
// key builder, so substituting legacy action values cannot certify this oracle.
func resourceActualAssertNativeOrder(t *testing.T, items []BrowserResourcePageItem, sortBy string) {
	t.Helper()
	missingSeen := false
	var previousValue *float64
	var previousOrdinal int64
	for _, item := range items {
		var value *float64
		if evidence := item.BrowserEvidence; evidence != nil {
			if sortBy == "native_start_ms" {
				value = evidence.EpochStartMS
			} else if evidence.Bytes.ResponseEncodedBody != nil {
				observed := float64(*evidence.Bytes.ResponseEncodedBody)
				value = &observed
			}
		}
		if value == nil {
			missingSeen = true
			continue
		}
		if missingSeen || previousValue != nil && (*previousValue > *value || *previousValue == *value && previousOrdinal >= item.Locator.ResourceOrdinal) {
			t.Fatal("actual native values are not ascending with missing last and stable capture ties")
		}
		observed := *value
		previousValue = &observed
		previousOrdinal = item.Locator.ResourceOrdinal
	}
}
