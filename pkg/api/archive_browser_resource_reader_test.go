package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
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
func resourcePageTestJSONL(t *testing.T, entries []map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]any{"test_id": "run-1", "timestamp": "2023-11-14T22:13:20.123456789Z", "action_name": "observed", "group": "group-1", "region": "east", "network_entries": entries})
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}
func resourcePageTestQuery(t *testing.T, values url.Values) BrowserResourcePageQuery {
	t.Helper()
	q, err := ParseBrowserResourcePageQuery(values)
	if err != nil {
		t.Fatal(err)
	}
	return q
}

func TestBrowserResourceReaderPagesMoreThanFiftyWithMissingSortTies(t *testing.T) {
	entries := make([]map[string]any, 80)
	for i := range entries {
		entries[i] = map[string]any{"url": "https://example.test/same", "method": "GET"}
		if i < 60 {
			entries[i]["duration_ms"] = float64(0)
		}
	}
	raw := resourcePageTestJSONL(t, entries)
	hash := sha256.Sum256(raw)
	for _, direction := range []string{"asc", "desc"} {
		t.Run(direction, func(t *testing.T) {
			q := resourcePageTestQuery(t, url.Values{"sort_by": {"duration_ms"}, "sort_dir": {direction}})
			first, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(raw), "run-1", q, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(first.Items) != 50 || !first.HasNext || first.Retained != 80 || first.Matched != 80 || first.Transactions != 1 || first.SHA256 != hex.EncodeToString(hash[:]) || first.SizeBytes != int64(len(raw)) {
				t.Fatalf("incorrect completed first page: %+v", first)
			}
			second, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(raw), "run-1", q, &first.Last)
			if err != nil {
				t.Fatal(err)
			}
			if len(second.Items) != 30 || second.HasNext || second.Retained != 80 || second.Matched != 80 {
				t.Fatalf("incorrect second page: %+v", second)
			}
			all := append(first.Items, second.Items...)
			for i, item := range all {
				if item.Locator.ResourceOrdinal != int64(i+1) {
					t.Fatalf("gap or duplicate at%d: %+v", i, item.Locator)
				}
				if i < 60 && (item.DurationMs == nil || *item.DurationMs != 0) || i >= 60 && item.DurationMs != nil {
					t.Fatal("missing value sorted as measured zero")
				}
			}
		})
	}
}
func TestBrowserResourceReaderFiltersBeforePaginationAndSeparatesActions(t *testing.T) {
	entries := make([]map[string]any, 80)
	for i := range entries {
		e := resourcePageTestEvidence()
		e.RequestID = e.SourceGeneration + "-request-" + strings.Repeat("a", 1+i%5)
		if i%2 == 0 {
			e.InitiatingActionID = e.SourceGeneration + "-action-3"
		}
		entries[i] = map[string]any{"url": "https://example.test/same", "browser_evidence": e}
	}
	raw := resourcePageTestJSONL(t, entries)
	generation := resourcePageTestEvidence().SourceGeneration
	for _, tc := range []struct {
		field, value string
		count        int
	}{{"initiating_action_id", generation + "-action-1", 40}, {"observation_action_id", generation + "-action-2", 80}, {"action_name", "observed", 80}, {"action_name", generation + "-action-2", 0}} {
		t.Run(tc.field+tc.value, func(t *testing.T) {
			q := resourcePageTestQuery(t, url.Values{tc.field: {tc.value}})
			got, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(raw), "run-1", q, nil)
			if err != nil {
				t.Fatal(err)
			}
			if got.Matched != int64(tc.count) || len(got.Items) != min(tc.count, 50) {
				t.Fatalf("filter not applied over full retained source: matched%d items%d", got.Matched, len(got.Items))
			}
		})
	}
}
func TestBrowserResourceReaderFallbackPresenceAndDisclosure(t *testing.T) {
	e := resourcePageTestEvidence()
	validRaw, _ := json.Marshal(e)
	cases := []struct {
		name, raw string
		wantRows  int
		branch    string
		present   bool
		state     string
	}{{"no_capture", `{"test_id":"run-1"}`, 0, "", false, ""}, {"null_capture", `{"network_entries":null}`, 0, "", false, ""}, {"both_null", `{"network_entries":null,"snapshot":{"network_entries":null}}`, 0, "", false, ""}, {"empty_null_fallback", `{"network_entries":[],"snapshot":{"network_entries":null}}`, 0, "", true, ""}, {"null_empty_fallback", `{"network_entries":null,"snapshot":{"network_entries":[]}}`, 0, "", true, ""}, {"null_nonempty_fallback", `{"network_entries":null,"snapshot":{"network_entries":[{}]}}`, 1, "snapshot_network_entries", true, "not_captured"}, {"explicit_empty", `{"network_entries":[]}`, 0, "", true, ""}, {"fallback", `{"snapshot":{"page_url":"https://user:secret@example.test/?token=private","network_entries":[{"url":"https://example.test/a"}]}}`, 1, "snapshot_network_entries", true, "not_captured"}, {"ordinary_preferred", `{"network_entries":[{"url":"https://example.test/a"}],"snapshot":{"network_entries":[{"url":"https://example.test/b"}]}}`, 1, "network_entries", true, "not_captured"}, {"empty_url_retained", `{"network_entries":[{}]}`, 1, "network_entries", true, "not_captured"}, {"malformed_v2", `{"network_entries":[{"browser_evidence":{"version":2}}]}`, 1, "network_entries", true, "malformed"}, {"future_v2", `{"network_entries":[{"browser_evidence":{"version":3}}]}`, 1, "network_entries", true, "unsupported_version"}, {"valid_zero_false", `{"network_entries":[{"duration_ms":0,"status_code":0,"bytes_received":0,"browser_evidence":` + string(validRaw) + `}]}`, 1, "network_entries", true, "available"}}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rows, present, err := browserResourceTransactionRows([]byte(tc.raw), 1, "run-1")
			if err != nil {
				t.Fatal(err)
			}
			if len(rows) != tc.wantRows || present != tc.present {
				t.Fatalf("capture presence changed: %+v %v", rows, present)
			}
			if len(rows) > 0 {
				if rows[0].Locator.Branch != tc.branch || rows[0].EvidenceState != tc.state {
					t.Fatalf("wrong projection: %+v", rows[0])
				}
				raw, _ := json.Marshal(rows)
				if strings.Contains(string(raw), "private") || strings.Contains(string(raw), "secret") {
					t.Fatal("unsafe source disclosed")
				}
				if tc.name == "valid_zero_false" && (rows[0].DurationMs == nil || *rows[0].DurationMs != 0 || rows[0].BrowserEvidence.ServiceWorker == nil || *rows[0].BrowserEvidence.ServiceWorker) {
					t.Fatal("lost observed zero/false")
				}
			}
		})
	}
}
func TestBrowserResourceReaderRefusesMalformedAndPartialScans(t *testing.T) {
	q := resourcePageTestQuery(t, nil)
	valid := resourcePageTestJSONL(t, []map[string]any{{"url": "https://example.test/a"}})
	for name, bad := range map[string]string{"duplicate": `{"network_entries":[],"network_entries":[]}`, "case_alias": `{"Network_Entries":[]}`, "negative": `{"network_entries":[{"duration_ms":-1}]}`, "foreign_run": `{"test_id":"other","network_entries":[]}`, "trailing": `{} {}`, "wrong_shape": `{"network_entries":{}}`, "partial": `{"network_entries":[`, "subnanosecond": `{"timestamp":"2023-11-14T22:13:20.1234567891Z"}`} {
		t.Run(name, func(t *testing.T) {
			source := append(append([]byte{}, valid...), []byte(bad)...)
			got, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(source), "run-1", q, nil)
			if err == nil || got.Transactions != 0 || len(got.Items) != 0 || got.SHA256 != "" {
				t.Fatalf("malformed source became successful partial page: %+v %v", got, err)
			}
		})
	}
	source := strings.NewReader(strings.Repeat("x", maxBrowserResourceReadRecordBytes+1))
	if got, err := scanBrowserResourceJSONL(context.Background(), source, "run-1", q, nil); !errors.Is(err, errBrowserResourceReadLimit) || len(got.Items) != 0 {
		t.Fatalf("oversize source was not explicitly refused: %v", err)
	}
}
func TestBrowserResourceReaderCancellationPhysicallyClosesAndJoins(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader, writer := io.Pipe()
	defer writer.Close()
	stop := browserResourceCloseOnCancel(ctx, reader)
	done := make(chan error, 1)
	go func() {
		_, err := scanBrowserResourceJSONL(ctx, reader, "run-1", resourcePageTestQuery(t, nil), nil)
		done <- err
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("wrong cancel cause: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("owned source read did not join")
	}
	stop()
	if _, err := writer.Write([]byte("{}\n")); err == nil {
		t.Fatal("canceled reader remains open")
	}
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

func TestBrowserResourceReaderSourceTimestampOffsetsNormalizeOnlyProjection(t *testing.T) {
	cases := []struct{ source, utc string }{
		{"2026-10-05T11:30:45.123456789-04:00", "2026-10-05T15:30:45.123456789Z"},
		{"2026-10-05T11:30:45.000000001+05:30", "2026-10-05T06:00:45.000000001Z"},
		{"1970-01-01T00:00:00.000000001+00:30", "1969-12-31T23:30:00.000000001Z"},
		{"2026-10-05T11:30:45+00:00", "2026-10-05T11:30:45Z"},
		{"2026-10-05T11:30:45.123456789Z", "2026-10-05T11:30:45.123456789Z"},
	}
	for _, tc := range cases {
		t.Run(tc.source, func(t *testing.T) {
			raw, err := json.Marshal(map[string]any{"test_id": "run-1", "timestamp": tc.source, "network_entries": []map[string]any{{"duration_ms": 0}}})
			if err != nil {
				t.Fatal(err)
			}
			jsonl := append(append([]byte(nil), raw...), '\n')
			original := append([]byte(nil), jsonl...)
			q := resourcePageTestQuery(t, nil)
			observed, err := scanBrowserResourceJSONL(context.Background(), bytes.NewReader(jsonl), "run-1", q, nil)
			if err != nil || len(observed.Items) != 1 || observed.Items[0].Timestamp == nil || observed.Items[0].Timestamp.Location() != time.UTC || observed.Items[0].Timestamp.Format(time.RFC3339Nano) != tc.utc || observed.Items[0].TimestampPrecision != "rfc3339_nanosecond" || observed.SHA256 != sha256BytesHex(original) || !bytes.Equal(jsonl, original) {
				t.Fatalf("source timestamp normalization changed the source instant or original bytes: %v", err)
			}
			path := resourcePhysicalWrite(t, new(resourcePhysicalProjectionFixture), []any{resourcePhysicalProjectionFixture{Projection: string(protocol.BrowserResourceArchiveProjection(raw))}})
			physical, err := resourcePhysicalScan(t, path, q, nil)
			if err != nil || !reflect.DeepEqual(physical.Items, observed.Items) {
				t.Fatalf("physical source timestamp normalization differs from exact JSONL: %v", err)
			}
			count := int64(1)
			page := &BrowserResourcePage{Version: 1, TestID: "run-1", ProjectID: "project-1", SourceFormat: "jsonl", SourceRevision: observed.SHA256, SourceIntegrity: "legacy_scan_fingerprint", SourceTimestampPrecision: observed.TimestampPrecision, Population: BrowserResourcePagePopulation{Availability: "retained_observations", TransactionRecords: 1, RetainedEntryObservations: &count, MatchedRetainedObservations: &count}, CaptureCoverage: BrowserResourcePageCoverage{Availability: "unavailable", Reason: "retained_observations_are_not_capture_coverage"}, Items: observed.Items}
			encoded, err := json.Marshal(page)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := DecodeBrowserResourcePage(encoded); err != nil {
				t.Fatal("normalized source timestamp no longer obeys the strict UTC output contract")
			}
		})
	}
	for _, source := range []string{"2026-10-05T11:30:45+24:00", "2026-10-05T11:30:45+00:60", "2026-10-05T11:30:45.1234567891-04:00", "2026-02-30T11:30:45-04:00", "2026-10-05T11:30:45", "2026-10-05T11:30:45-04:00trailing", "9999-12-31T23:59:59-23:59"} {
		t.Run("refuse_"+source, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]any{"timestamp": source, "network_entries": []map[string]any{{"duration_ms": 0}}})
			if _, _, err := browserResourceTransactionRows(raw, 1, "run-1"); !errors.Is(err, errBrowserResourceReadMalformed) {
				t.Fatal("invalid or unrepresentable source timestamp became a successful page")
			}
		})
	}
}
