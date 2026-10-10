package api

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/harsynth"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// The last step of the browser intelligence drill-down (BI-4): one retained
// request as its HAR entry, for HAR downloaders only.
func TestRunArchiveBrowserHAREntryDrillDown(t *testing.T) {
	server, _, _ := harDownloadFixture(t)
	path := func(requestID string) string {
		return "/api/v1/tests/har-download-run/archive/browser/har-entry?request_id=" + requestID
	}
	uncovered := syntheticUncoveredGeneration + "-request-7"
	for _, roles := range []string{harViewer, harOtherEditor} {
		if reply := harDownloadRequest(t, server, path(uncovered), "har-user", roles); reply.Code != http.StatusForbidden {
			t.Fatalf("%s read a HAR entry: %d", roles, reply.Code)
		}
	}

	// A request whose context recorded no native HAR is derived as the
	// synthetic HAR derives it, scrubbed the same way.
	reply := harDownloadRequest(t, server, path(uncovered), "har-editor", harEditor)
	if reply.Code != http.StatusOK || reply.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("synthetic entry: %d %v %s", reply.Code, reply.Header(), reply.Body.String())
	}
	var synthetic BrowserHAREntryResponse
	if err := json.Unmarshal(reply.Body.Bytes(), &synthetic); err != nil {
		t.Fatal(err)
	}
	if synthetic.Fidelity != harsynth.Fidelity || synthetic.SourceGeneration != syntheticUncoveredGeneration || synthetic.Artifact != "" || len(synthetic.Entry) == 0 || synthetic.HeadersOmitted {
		t.Fatalf("synthetic response %+v", synthetic)
	}
	var entry struct {
		Request struct {
			URL    string `json:"url"`
			Method string `json:"method"`
		} `json:"request"`
		Evidence struct {
			RequestID string `json:"request_id"`
		} `json:"_apexload"`
	}
	if err := json.Unmarshal(synthetic.Entry, &entry); err != nil {
		t.Fatal(err)
	}
	if entry.Request.Method != "GET" || !strings.HasPrefix(entry.Request.URL, "https://shop.example/cart") || entry.Evidence.RequestID != uncovered {
		t.Fatalf("entry %+v", entry)
	}
	for _, secret := range []string{"urlTokenQxZrTm", "headerTokenQxZrTmWp"} {
		if strings.Contains(string(synthetic.Entry), secret) {
			t.Fatalf("entry retains %q: %s", secret, synthetic.Entry)
		}
	}

	// A request in a context with a native HAR is in that HAR, not the
	// synthetic one.
	reply = harDownloadRequest(t, server, path(syntheticCoveredGeneration+"-request-8"), "har-editor", harEditor)
	var native BrowserHAREntryResponse
	if reply.Code != http.StatusOK || json.Unmarshal(reply.Body.Bytes(), &native) != nil {
		t.Fatalf("native: %d %s", reply.Code, reply.Body.String())
	}
	if native.Fidelity != "native" || native.Artifact != harNativeName || len(native.Entry) != 0 {
		t.Fatalf("native response %+v", native)
	}

	if reply := harDownloadRequest(t, server, path("no-such-request"), "har-editor", harEditor); reply.Code != http.StatusNotFound {
		t.Fatalf("unknown request: %d", reply.Code)
	}
	if reply := harDownloadRequest(t, server, "/api/v1/tests/har-download-run/archive/browser/har-entry", "har-editor", harEditor); reply.Code != http.StatusBadRequest {
		t.Fatalf("missing request_id: %d", reply.Code)
	}
}

// A Parquet transactions file keeps no headers, and its entry says so; a
// request in a context whose native HAR is declared but not servable is
// still the native HAR's, naming no artifact (the synthetic HAR leaves it
// out too).
func TestBrowserHAREntryFromParquetAndAnUnservableNativeHAR(t *testing.T) {
	var fixtures []any
	for _, row := range syntheticArchiveRows(t) {
		fixtures = append(fixtures, resourcePhysicalProjectionFixture{Projection: string(protocol.BrowserResourceArchiveProjection(row))})
	}
	path := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), fixtures)
	manifest := syntheticArchiveManifest()
	manifest.TransactionsParquetFile = filepath.Base(path)
	found, err := findRunArchiveBrowserHAREntry(context.Background(), filepath.Dir(path), manifest, syntheticUncoveredGeneration+"-request-7")
	if err != nil || found.Fidelity != harsynth.Fidelity || !found.HeadersOmitted || len(found.Entry) == 0 {
		t.Fatalf("Parquet entry %+v %v", found, err)
	}
	var entry struct {
		Request struct {
			URL     string `json:"url"`
			Headers []any  `json:"headers"`
		} `json:"request"`
		Response struct {
			Headers []any `json:"headers"`
		} `json:"response"`
	}
	if err := json.Unmarshal(found.Entry, &entry); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(entry.Request.URL, "https://shop.example/cart") || len(entry.Request.Headers) != 0 || len(entry.Response.Headers) != 0 || strings.Contains(string(found.Entry), "headerTokenQxZrTmWp") {
		t.Fatalf("Parquet entry carries headers: %s", found.Entry)
	}

	manifest.BrowserHARArtifacts[0].SHA256 = "not-a-digest"
	found, err = findRunArchiveBrowserHAREntry(context.Background(), filepath.Dir(path), manifest, syntheticCoveredGeneration+"-request-8")
	if err != nil || found.Fidelity != "native" || found.Artifact != "" || len(found.Entry) != 0 {
		t.Fatalf("unservable native HAR %+v %v", found, err)
	}
}

// A request the synthetic HAR leaves out says why, never "not found"; an
// entry names native HARs that may hold it; and a second concurrent archive
// scan is refused while one runs.
func TestBrowserHAREntryOmissionsCoverageAndAdmission(t *testing.T) {
	raw, omitted, err := syntheticHAREntryJSON(harsynth.Entry{ActionName: "Blob", Network: protocol.NetworkEntry{URL: "blob:opaque", Method: "GET", StatusCode: 200}}, true)
	if err != nil || raw != nil || omitted == "" {
		t.Fatalf("an omitted entry: %s %q %v", raw, omitted, err)
	}

	server, _, _ := harDownloadFixture(t)
	path := "/api/v1/tests/har-download-run/archive/browser/har-entry?request_id=" + syntheticUncoveredGeneration + "-request-7"
	browserResourceReadPermit <- struct{}{}
	reply := harDownloadRequest(t, server, path, "har-editor", harEditor)
	<-browserResourceReadPermit
	if reply.Code != http.StatusServiceUnavailable || reply.Header().Get("Retry-After") == "" {
		t.Fatalf("a scan while another runs: %d %s", reply.Code, reply.Body.String())
	}

	// A retained request whose URL has no host is one the synthetic HAR
	// leaves out.
	at := time.Date(2026, 10, 7, 12, 0, 5, 0, time.UTC)
	blob, err := json.Marshal(TransactionEvent{Timestamp: at, TestID: "run-1", ActionName: "Blob", PageURL: "https://shop.example/", NetworkEntries: []protocol.NetworkEntry{
		{BrowserEvidence: syntheticEvidence(syntheticUncoveredGeneration, syntheticUncoveredGeneration+"-request-9", at), URL: "blob:opaque", Method: "GET", StatusCode: 200, ResourceType: "fetch"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	var fixtures []any
	for _, row := range append(syntheticArchiveRows(t), blob) {
		fixtures = append(fixtures, resourcePhysicalProjectionFixture{Projection: string(protocol.BrowserResourceArchiveProjection(row))})
	}
	file := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), fixtures)
	manifest := syntheticArchiveManifest()
	manifest.TransactionsParquetFile = filepath.Base(file)
	left, err := findRunArchiveBrowserHAREntry(context.Background(), filepath.Dir(file), manifest, syntheticUncoveredGeneration+"-request-9")
	if err != nil || left.Fidelity != "omitted" || left.OmittedReason == "" || len(left.Entry) != 0 {
		t.Fatalf("an omitted request: %+v %v", left, err)
	}
	unnamed := manifest.BrowserHARArtifacts[0]
	unnamed.EvidenceGeneration = ""
	manifest.BrowserHARArtifacts = append(manifest.BrowserHARArtifacts, unnamed)
	found, err := findRunArchiveBrowserHAREntry(context.Background(), filepath.Dir(file), manifest, syntheticUncoveredGeneration+"-request-7")
	if err != nil || found.Fidelity != harsynth.Fidelity || found.NativeHARCoverageUnknown != 1 {
		t.Fatalf("coverage unknown: %+v %v", found, err)
	}
}
