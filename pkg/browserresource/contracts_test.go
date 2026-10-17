package browserresource

import (
	"bytes"
	"encoding/json"
	"github.com/jasonczech/apexload/pkg/protocol"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"
)

func resourcePageTestEvidence() *protocol.BrowserResourceEvidence {
	generation := "0123456789abcdef0123456789abcdef"
	zero := 0.0
	body := int64(0)
	no := false
	return &protocol.BrowserResourceEvidence{Version: 2, SourceGeneration: generation, RequestID: generation + "-request-1", RedirectHop: 0,
		InitiatingActionID: generation + "-action-1", ObservationActionID: generation + "-action-2", Origin: "https://example.test", ObservedAt: time.Unix(1700000000, 123456789).UTC(), TimingSource: "playwright", State: "finished", EpochStartMS: &zero,
		Phases: []protocol.BrowserResourcePhase{{Name: "response", StartMS: &zero, EndMS: &zero}}, Bytes: protocol.BrowserResourceBytes{ResponseEncodedBody: &body}, ServiceWorker: &no}
}
func TestBrowserResourcePageClosedWirePresence(t *testing.T) {
	count := int64(1)
	zero := 0.0
	page := BrowserResourcePage{Version: 1, TestID: "run-1", ProjectID: "project-1", SourceFormat: "jsonl", SourceRevision: strings.Repeat("a", 64), SourceIntegrity: "legacy_scan_fingerprint", SourceTimestampPrecision: "unavailable", Population: BrowserResourcePagePopulation{Availability: "retained_observations", TransactionRecords: 1, RetainedEntryObservations: &count, MatchedRetainedObservations: &count}, CaptureCoverage: BrowserResourcePageCoverage{Availability: "unavailable", Reason: "retained_observations_are_not_capture_coverage"}, Items: []BrowserResourcePageItem{{Locator: BrowserResourcePageLocator{TransactionOrdinal: 1, ResourceOrdinal: 1, Branch: "network_entries"}, TimestampPrecision: "unavailable", EvidenceState: "available", BrowserEvidence: resourcePageTestEvidence(), DurationMs: &zero}}}
	raw, err := json.Marshal(page)
	if err != nil {
		t.Fatal(err)
	}
	got, err := DecodeBrowserResourcePage(raw)
	if err != nil || !reflect.DeepEqual(got, &page) {
		t.Fatalf("valid closed projection drifted: %v", err)
	}
	for _, field := range []string{"version", "test_id", "project_id", "source_format", "source_revision", "source_integrity", "source_timestamp_precision", "population", "capture_coverage", "items"} {
		t.Run("missing_"+field, func(t *testing.T) {
			var fields map[string]json.RawMessage
			_ = json.Unmarshal(raw, &fields)
			delete(fields, field)
			forged, _ := json.Marshal(fields)
			if _, err := DecodeBrowserResourcePage(forged); err == nil {
				t.Fatal("required page field absent")
			}
		})
	}
	nilItemsPage := page
	nilItemsPage.Items = nil
	nullItems, err := json.Marshal(nilItemsPage)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DecodeBrowserResourcePage(nullItems); err == nil {
		t.Fatal("null items admitted as an empty observed array")
	}
	emptyCursor := append(append([]byte{}, raw[:len(raw)-1]...), []byte(`,"next_cursor":""}`)...)
	if _, err := DecodeBrowserResourcePage(emptyCursor); err == nil {
		t.Fatal("explicit empty cursor manufactured a completed page")
	}
	malformed := bytes.Replace(raw, []byte(`"redirect_hop":0,`), nil, 1)
	if _, err := DecodeBrowserResourcePage(malformed); err == nil {
		t.Fatal("nested missing observed zero admitted")
	}
	duplicate := bytes.Replace(raw, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1)
	if _, err := DecodeBrowserResourcePage(duplicate); err == nil {
		t.Fatal("duplicate page field admitted")
	}
}
func TestBrowserResourcePageQueryRefusesBroadening(t *testing.T) {
	for name, values := range map[string]url.Values{"unknown": {"offset": {"50"}}, "duplicate": {"limit": {"10", "20"}}, "empty": {"action_name": {""}}, "bad_sort": {"sort_by": {"sql"}}, "bad_generation": {"source_generation": {"wrong"}}, "mixed_generation": {"source_generation": {"0123456789abcdef0123456789abcdef"}, "request_id": {"fedcba9876543210fedcba9876543210-request-1"}}, "bad_origin": {"origin": {"https://example.test/path"}}} {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseBrowserResourcePageQuery(values); err == nil {
				t.Fatal("invalid query silently broadened")
			}
		})
	}
}
