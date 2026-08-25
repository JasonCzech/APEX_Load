package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
)

func TestTransactionMatchesArchiveFilterDimensions(t *testing.T) {
	item := &TransactionEvent{
		Region: "us-east", NetworkProfile: "preset:mobile", SourceTransaction: "Checkout",
		Hierarchy: "Store/Cart/Submit", Phase: "action", Snapshot: &protocol.ErrorSnapshot{},
	}
	finalizeTransactionEventDerivedFields(item, 7)
	if item.EventID == "" || !item.HasDetail {
		t.Fatalf("derived event fields = %#v", item)
	}
	wantDetail := true
	matching := transactionEventFilter{
		Region: "US-EAST", NetworkProfile: "PRESET:MOBILE", SourceTransaction: "checkout",
		Hierarchy: "store/cart/submit", Phase: "ACTION", HasDetail: &wantDetail,
	}
	if !transactionMatchesArchiveFilter(item, matching) {
		t.Fatal("dimension-complete filter did not match case-insensitively")
	}
	for name, mutate := range map[string]func(*transactionEventFilter){
		"region":             func(f *transactionEventFilter) { f.Region = "eu-west" },
		"network profile":    func(f *transactionEventFilter) { f.NetworkProfile = "wifi" },
		"source transaction": func(f *transactionEventFilter) { f.SourceTransaction = "Browse" },
		"hierarchy":          func(f *transactionEventFilter) { f.Hierarchy = "Store/Search" },
		"phase":              func(f *transactionEventFilter) { f.Phase = "teardown" },
	} {
		t.Run(name, func(t *testing.T) {
			filter := matching
			mutate(&filter)
			if transactionMatchesArchiveFilter(item, filter) {
				t.Fatalf("mismatched %s unexpectedly matched", name)
			}
		})
	}
}

func TestLegacyTransactionDerivedEventIDIsStable(t *testing.T) {
	first := &TransactionEvent{Timestamp: time.Unix(10, 20), TestID: "run", ActionName: "checkout", DurationMs: 12.5, Success: true}
	second := *first
	finalizeTransactionEventDerivedFields(first, 0)
	finalizeTransactionEventDerivedFields(&second, 0)
	if first.EventID == "" || first.EventID != second.EventID {
		t.Fatalf("legacy IDs = %q and %q", first.EventID, second.EventID)
	}
}

func TestLegacyTransactionPageIDsAreStableAndUniquePerSourceRow(t *testing.T) {
	event := TransactionEvent{Timestamp: time.Unix(10, 20), TestID: "run", ActionName: "checkout", DurationMs: 12.5, Success: true}
	var source bytes.Buffer
	encoder := json.NewEncoder(&source)
	if err := encoder.Encode(event); err != nil {
		t.Fatal(err)
	}
	if err := encoder.Encode(event); err != nil {
		t.Fatal(err)
	}
	read := func() []TransactionEvent {
		items, total, err := readJSONLPageFrom[TransactionEvent](t.Context(), bytes.NewReader(source.Bytes()), archivePage{Limit: 10}, nil)
		if err != nil {
			t.Fatal(err)
		}
		if total != 2 || len(items) != 2 {
			t.Fatalf("page total/items = %d/%d, want 2/2", total, len(items))
		}
		return items
	}
	first := read()
	second := read()
	if first[0].EventID == first[1].EventID {
		t.Fatalf("identical legacy rows share event ID %q", first[0].EventID)
	}
	if first[0].EventID != second[0].EventID || first[1].EventID != second[1].EventID {
		t.Fatalf("legacy row IDs changed across scans: %#v then %#v", first, second)
	}
}

func TestParseTransactionEventFilterRejectsInvalidHasDetail(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/?has_detail=sometimes", nil)
	response := httptest.NewRecorder()
	if _, ok := parseTransactionEventFilter(response, req); ok || response.Code != http.StatusBadRequest {
		t.Fatalf("parse ok=%v status=%d, want false/400", ok, response.Code)
	}
}
