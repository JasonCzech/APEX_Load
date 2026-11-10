package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/protocol"
	"github.com/jasonczech/apexload/pkg/storage"
)

const waterfallTestLifetime = uint64(7349851234567890123)

// waterfallFixture is one VUser's Checkout (A, B, C) with an Inner
// transaction around B, a request written after Checkout inside its window,
// and noise from other VUsers, after `noise` unrelated leading rows.
func waterfallFixture(noise int) []TransactionEvent {
	var events []TransactionEvent
	for index := 0; index < noise; index++ {
		events = append(events, taRequest(3, 99, 1, float64(index), 1, "noise", 1))
	}
	a := taRequest(1, waterfallTestLifetime, 1, 0, 30, "A", 1)
	a.Snapshot = &protocol.ErrorSnapshot{RequestMethod: "GET", RequestURL: "https://example.test/a"}
	a.NetworkEntries = []protocol.NetworkEntry{{URL: "https://example.test/a"}}
	b := taRequest(1, waterfallTestLifetime, 1, 30, 40, "B", 2)
	b.DNSMs, b.ConnectMs, b.TTFBMs = 5, 10, 35
	c := taRequest(1, waterfallTestLifetime, 1, 70, 20, "C", 3)
	c.Success, c.StatusCode, c.ErrorMessage = false, 503, "upstream unavailable"
	checkout := taTransaction(1, waterfallTestLifetime, 1, 0, 100, "Checkout")
	events = append(events,
		a,
		taRequest(2, 42, 1, 5, 10, "X", 1),
		b,
		taTransaction(1, waterfallTestLifetime, 1, 30, 40, "Inner"),
		c,
		checkout,
		taRequest(1, waterfallTestLifetime, 1, 50, 5, "late", 4),
		taTransaction(2, 42, 1, 0, 20, "Other"),
	)
	return events
}

func waterfallRecordForTest(t *testing.T, events []TransactionEvent) (*storage.RunArchiveRecord, *RunArchiveReport) {
	t.Helper()
	record := writeArchiveTransactionsForTest(t, events)
	for _, name := range []string{"resources.jsonl", "interval_summaries.jsonl"} {
		if err := os.WriteFile(filepath.Join(record.ArchivePath, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	report, err := buildRunArchiveReport(record)
	if err != nil {
		t.Fatalf("buildRunArchiveReport: %v", err)
	}
	return record, report
}

func waterfallRepresentative(t *testing.T, report *RunArchiveReport, name string) RunArchiveRepresentativeSample {
	t.Helper()
	entry := taEntry(t, report.TimeAnatomy, name)
	if len(entry.Representatives) == 0 {
		t.Fatalf("%s has no representatives", name)
	}
	return entry.Representatives[0]
}

// countWaterfallDecodes counts the lines the drill-down decodes during fn.
func countWaterfallDecodes(t *testing.T, fn func()) []int64 {
	t.Helper()
	var decoded []int64
	previous := archiveWaterfallDecodeHook
	archiveWaterfallDecodeHook = func(ordinal int64) { decoded = append(decoded, ordinal) }
	defer func() { archiveWaterfallDecodeHook = previous }()
	fn()
	return decoded
}

func buildWaterfallForTest(t *testing.T, record *storage.RunArchiveRecord, eventID string, hints archiveWaterfallHints, maxRequests int) *archiveWaterfallResult {
	t.Helper()
	waterfall, err := buildArchiveTransactionWaterfall(context.Background(), record, eventID, hints, maxRequests)
	if err != nil {
		t.Fatalf("buildArchiveTransactionWaterfall: %v", err)
	}
	return waterfall
}

func TestTransactionWaterfallChildrenAndDecompositionMatchReport(t *testing.T) {
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	entry := taEntry(t, report.TimeAnatomy, "Checkout")
	representative := waterfallRepresentative(t, report, "Checkout")
	waterfall := buildWaterfallForTest(t, record, representative.EventID, archiveWaterfallHints{}, 500)

	if waterfall.Schema != transactionWaterfallSchema || waterfall.JoinRule != timeAnatomyJoinRule || waterfall.PhaseModel != timeAnatomyPhaseModel || waterfall.IdentityQuality != timeAnatomyIdentityLifetime {
		t.Fatalf("header = %+v", waterfall)
	}
	if waterfall.Root.EventID != representative.EventID || waterfall.Root.Ordinal != representative.Ordinal || waterfall.Root.Population != "transaction" || waterfall.Root.Phases.UnsplitMs != 100 {
		t.Fatalf("root = %+v (representative %+v)", waterfall.Root, representative)
	}
	var names []string
	var offsets []float64
	for _, row := range waterfall.Requests {
		names = append(names, row.ActionName)
		offsets = append(offsets, row.OffsetMs)
		if sum := taPhaseSum(row.Phases); sum != row.DurationMs {
			t.Errorf("%s phases sum %v, duration %v", row.ActionName, sum, row.DurationMs)
		}
	}
	if !reflect.DeepEqual(names, []string{"A", "B", "C"}) || !reflect.DeepEqual(offsets, []float64{0, 30, 70}) {
		t.Fatalf("requests = %v offsets %v", names, offsets)
	}
	if waterfall.RequestsTotal != 3 || waterfall.Truncated || waterfall.LateRequests != 1 {
		t.Fatalf("total %d truncated %v late %d", waterfall.RequestsTotal, waterfall.Truncated, waterfall.LateRequests)
	}
	b := waterfall.Requests[1]
	if b.Phases.DNSMs != 5 || b.Phases.ConnectMs != 10 || b.Phases.ServerMs != 20 || b.Phases.DownloadMs != 5 || b.Connection != "new" {
		t.Fatalf("B row = %+v", b)
	}
	c := waterfall.Requests[2]
	if c.Success || c.StatusCode != 503 || c.ErrorClass == "" || c.ErrorMessage != "upstream unavailable" {
		t.Fatalf("C row = %+v", c)
	}
	if waterfall.Decomposition == nil {
		t.Fatal("decomposition missing")
	}
	if !reflect.DeepEqual(*waterfall.Decomposition, entry.Decomposition) {
		t.Fatalf("decomposition %+v, report instance %+v", *waterfall.Decomposition, entry.Decomposition)
	}
	if waterfall.Decomposition.Instances != 1 || waterfall.Decomposition.NonRequestMs != 10 || waterfall.Decomposition.OverlapMs != 0 {
		t.Fatalf("decomposition = %+v", *waterfall.Decomposition)
	}
	assertDecompositionIdentity(t, *waterfall.Decomposition)
	if waterfall.EnclosingTransactions != nil {
		t.Fatalf("transaction root has enclosing transactions: %+v", waterfall.EnclosingTransactions)
	}

	raw, err := json.Marshal(waterfall)
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{`"snapshot"`, `"network_entries"`, `"extractions"`, `"protocol_evidence"`, `"console_entries"`, `"dom_summary"`} {
		if strings.Contains(string(raw), key) {
			t.Errorf("drill-down carries %s: %s", key, raw)
		}
	}
	if !strings.Contains(string(raw), `"vuser_lifetime":"`+strconv.FormatUint(waterfallTestLifetime, 10)+`"`) {
		t.Errorf("vuser_lifetime is not a JSON string: %s", raw)
	}
}

func TestTransactionWaterfallInnerDecompositionMatchesReport(t *testing.T) {
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	entry := taEntry(t, report.TimeAnatomy, "Inner")
	representative := waterfallRepresentative(t, report, "Inner")
	waterfall := buildWaterfallForTest(t, record, representative.EventID, archiveWaterfallHints{ordinal: representative.Ordinal, ok: true}, 500)
	if len(waterfall.Requests) != 1 || waterfall.Requests[0].ActionName != "B" || waterfall.Decomposition == nil || !reflect.DeepEqual(*waterfall.Decomposition, entry.Decomposition) {
		t.Fatalf("Inner waterfall = %+v, report %+v", waterfall, entry.Decomposition)
	}
}

func TestTransactionWaterfallHints(t *testing.T) {
	const noise = 200
	for _, persisted := range []bool{false, true} {
		t.Run(fmt.Sprintf("persisted_ids=%v", persisted), func(t *testing.T) {
			events := waterfallFixture(noise)
			if persisted {
				for index := range events {
					events[index].EventID = fmt.Sprintf("tx_%032x", index+1)
				}
			}
			record, report := waterfallRecordForTest(t, events)
			representative := waterfallRepresentative(t, report, "Checkout")
			if representative.FirstOrdinal != noise || representative.Ordinal != noise+5 {
				t.Fatalf("representative ordinals %d/%d", representative.FirstOrdinal, representative.Ordinal)
			}
			var absent *archiveWaterfallResult
			absentDecodes := countWaterfallDecodes(t, func() {
				absent = buildWaterfallForTest(t, record, representative.EventID, archiveWaterfallHints{}, 500)
			})

			var hinted *archiveWaterfallResult
			hintedDecodes := countWaterfallDecodes(t, func() {
				hinted = buildWaterfallForTest(t, record, representative.EventID, archiveWaterfallHints{ordinal: representative.Ordinal, ok: true}, 500)
			})
			// One locate decode, then the root VUser's six rows.
			if len(hintedDecodes) != 7 {
				t.Fatalf("hinted decodes = %v", hintedDecodes)
			}
			for _, ordinal := range hintedDecodes {
				if ordinal < representative.FirstOrdinal {
					t.Fatalf("hinted scan decoded row %d before first_ordinal %d", ordinal, representative.FirstOrdinal)
				}
			}
			if !persisted && len(absentDecodes) <= len(hintedDecodes) {
				t.Fatalf("legacy ids without hints decoded %d rows, hinted %d", len(absentDecodes), len(hintedDecodes))
			}

			var wrong *archiveWaterfallResult
			wrongDecodes := countWaterfallDecodes(t, func() {
				wrong = buildWaterfallForTest(t, record, representative.EventID, archiveWaterfallHints{ordinal: representative.Ordinal - 1, ok: true}, 500)
			})
			if len(wrongDecodes) <= 1 {
				t.Fatalf("wrong hint did not fall back: %v", wrongDecodes)
			}
			if !reflect.DeepEqual(absent, hinted) || !reflect.DeepEqual(absent, wrong) {
				t.Fatalf("hints changed the result:\nabsent %+v\nhinted %+v\nwrong  %+v", absent, hinted, wrong)
			}
			if len(absent.Requests) != 3 {
				t.Fatalf("requests = %+v", absent.Requests)
			}
		})
	}
}

func TestTransactionWaterfallTruncatesRequests(t *testing.T) {
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	entry := taEntry(t, report.TimeAnatomy, "Checkout")
	representative := waterfallRepresentative(t, report, "Checkout")
	waterfall := buildWaterfallForTest(t, record, representative.EventID, archiveWaterfallHints{}, 2)
	if len(waterfall.Requests) != 2 || !waterfall.Truncated || waterfall.RequestsTotal != 3 {
		t.Fatalf("truncation = %d rows, truncated %v, total %d", len(waterfall.Requests), waterfall.Truncated, waterfall.RequestsTotal)
	}
	if !reflect.DeepEqual(*waterfall.Decomposition, entry.Decomposition) {
		t.Fatalf("truncated decomposition %+v, report %+v", *waterfall.Decomposition, entry.Decomposition)
	}
}

func TestTransactionWaterfallRequestRootListsEnclosingInnermostFirst(t *testing.T) {
	events := waterfallFixture(0)
	record, _ := waterfallRecordForTest(t, events)
	// Row 2 is request B.
	items, _, err := loadArchiveTransactions(record, transactionEventFilter{}, archivePage{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	var bID string
	for _, item := range items {
		if item.ActionName == "B" {
			bID = item.EventID
		}
	}
	if bID == "" {
		t.Fatal("request B not found through paging")
	}
	waterfall := buildWaterfallForTest(t, record, bID, archiveWaterfallHints{}, 500)
	if waterfall.Root.Population != "action" || waterfall.Root.Ordinal != 2 || waterfall.Root.Phases.ServerMs != 20 || len(waterfall.Requests) != 0 || waterfall.Decomposition != nil {
		t.Fatalf("request root = %+v", waterfall)
	}
	var names []string
	for _, ref := range waterfall.EnclosingTransactions {
		names = append(names, ref.ActionName)
	}
	if !reflect.DeepEqual(names, []string{"Inner", "Checkout"}) || waterfall.EnclosingTransactions[0].SpanMs != 40 || waterfall.EnclosingTransactions[1].SpanMs != 100 {
		t.Fatalf("enclosing = %+v", waterfall.EnclosingTransactions)
	}
}

func TestTransactionWaterfallUnjoinableRoot(t *testing.T) {
	orphan := taTransaction(0, 0, 0, 0, 50, "Orphan")
	record, report := waterfallRecordForTest(t, []TransactionEvent{taRequest(0, 0, 0, 0, 10, "A", 1), orphan})
	representative := waterfallRepresentative(t, report, "Orphan")
	waterfall := buildWaterfallForTest(t, record, representative.EventID, archiveWaterfallHints{}, 500)
	if waterfall.IdentityQuality != timeAnatomyIdentityNone || len(waterfall.Requests) != 0 || waterfall.Requests == nil || waterfall.Decomposition != nil {
		t.Fatalf("unjoinable root = %+v", waterfall)
	}
}

func waterfallHandlerServer(t *testing.T, events []TransactionEvent) (*Server, *RunArchiveReport) {
	t.Helper()
	server, store := newTimeslotServer(t)
	if err := store.SaveTest(&storage.TestRecord{ID: "test-query", Name: "test-query", ProjectID: "proj-a", Status: "COMPLETE"}); err != nil {
		t.Fatal(err)
	}
	record, report := waterfallRecordForTest(t, events)
	record.TestID = "test-query"
	record.ArchiveBackend = archiveBackendFilesystem
	if raw, err := os.ReadFile(record.ManifestPath); err == nil {
		record.ManifestSHA256 = sha256BytesHex(raw)
	}
	admitArchiveForTest(t, record)
	if err := store.SaveRunArchive(record); err != nil {
		t.Fatal(err)
	}
	server.runner.mu.Lock()
	server.runner.archives["test-query"] = record
	server.runner.mu.Unlock()
	return server, report
}

func serveWaterfallForTest(server *Server, eventID, query string) *httptest.ResponseRecorder {
	target := "/api/v1/tests/test-query/archive/transactions/" + eventID + "/waterfall"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.SetPathValue("id", "test-query")
	req.SetPathValue("event_id", eventID)
	rec := httptest.NewRecorder()
	server.handleGetRunArchiveTransactionWaterfall(rec, req)
	return rec
}

func TestTransactionWaterfallHandlerStatusesAndCache(t *testing.T) {
	server, report := waterfallHandlerServer(t, waterfallFixture(0))
	representative := waterfallRepresentative(t, report, "Checkout")
	id := representative.EventID

	for _, tc := range []struct {
		name, eventID, query string
		want                 int
	}{
		{"malformed id", "tx_XYZ", "", http.StatusBadRequest},
		{"short id", "tx_0123", "", http.StatusBadRequest},
		{"negative ordinal", id, "ordinal=-1", http.StatusBadRequest},
		{"non-numeric ordinal", id, "ordinal=abc", http.StatusBadRequest},
		{"first without ordinal", id, "first_ordinal=0", http.StatusBadRequest},
		{"first after ordinal", id, "ordinal=1&first_ordinal=2", http.StatusBadRequest},
		{"max_requests zero", id, "max_requests=0", http.StatusBadRequest},
		{"max_requests too large", id, "max_requests=501", http.StatusBadRequest},
		{"unknown event", "tx_ffffffffffffffffffffffffffffffff", "", http.StatusNotFound},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if rec := serveWaterfallForTest(server, tc.eventID, tc.query); rec.Code != tc.want {
				t.Fatalf("status = %d, want %d: %s", rec.Code, tc.want, rec.Body.String())
			}
		})
	}

	query := fmt.Sprintf("ordinal=%d&first_ordinal=%d", representative.Ordinal, representative.FirstOrdinal)
	var first *httptest.ResponseRecorder
	firstDecodes := countWaterfallDecodes(t, func() { first = serveWaterfallForTest(server, id, query) })
	if first.Code != http.StatusOK || len(firstDecodes) == 0 {
		t.Fatalf("first status = %d decodes %v: %s", first.Code, firstDecodes, first.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(first.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["schema"] != transactionWaterfallSchema || payload["requests_total"] != float64(3) {
		t.Fatalf("payload = %s", first.Body.String())
	}
	var second *httptest.ResponseRecorder
	secondDecodes := countWaterfallDecodes(t, func() { second = serveWaterfallForTest(server, id, query) })
	if second.Code != http.StatusOK || len(secondDecodes) != 0 || second.Body.String() != first.Body.String() {
		t.Fatalf("repeat request was not served from the cache: status %d decodes %v", second.Code, secondDecodes)
	}
}

func TestTransactionWaterfallHandlerMapsScanBudget(t *testing.T) {
	server, report := waterfallHandlerServer(t, waterfallFixture(20))
	representative := waterfallRepresentative(t, report, "Checkout")
	setTimeAnatomyCap(t, &archiveWaterfallScanRecords, 5)
	rec := serveWaterfallForTest(server, representative.EventID, "")
	if rec.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413: %s", rec.Code, rec.Body.String())
	}
}

func TestTransactionWaterfallHandlerMissingArchive(t *testing.T) {
	server, _ := newTimeslotServer(t)
	rec := serveWaterfallForTest(server, "tx_0123456789abcdef0123456789abcdef", "")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}
