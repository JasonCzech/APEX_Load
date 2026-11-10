package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/jasonczech/apexload/pkg/storage"
)

// waterfallEventID returns the persisted event_id of the row named action.
func waterfallEventID(t *testing.T, events []TransactionEvent, action string) string {
	t.Helper()
	for _, event := range events {
		if event.ActionName == action {
			return event.EventID
		}
	}
	t.Fatalf("no row %s", action)
	return ""
}

// The drill-down decodes the fields timeAnatomyRequestExcluded reads: the
// async_dispatch evidence key the engine stamps on callback rows, and the
// Phase older archives label callback steps with. A callback row inside
// Login's window is outside every transaction in the report, so the
// drill-down claims neither it nor a callback-started branch.
func TestTransactionWaterfallExcludesAsyncDispatchRows(t *testing.T) {
	callback := taRequest(1, 51, 1, 20, 10, "Callback", 2)
	callback.Phase = "async on_response"
	branch := taRequest(1, 51, 1, 40, 10, "Branch", 3)
	branch.ProtocolEvidence = map[string]any{"async_dispatch": true}
	lateCallback := taRequest(1, 51, 1, 60, 10, "LateCallback", 4)
	lateCallback.ProtocolEvidence = map[string]any{"async_dispatch": true}
	events := waterfallPersistIDs([]TransactionEvent{
		taRequest(1, 51, 1, 0, 10, "A", 1),
		callback,
		branch,
		taTransaction(1, 51, 1, 0, 100, "Login"),
		lateCallback,
	})
	acc := waterfallAccumulatorFor(events)
	record, _ := waterfallRecordForTest(t, events)
	instance := waterfallInstances(t, acc, "Login")[0]
	if !instance.joined || instance.children != 1 {
		t.Fatalf("report instance joined %v children %d, want A only", instance.joined, instance.children)
	}
	waterfall := assertWaterfallIsReportInstance(t, record, instance, false)
	if names := waterfallNames(waterfall); !reflect.DeepEqual(names, []string{"A"}) {
		t.Fatalf("claimed %v, want [A]", names)
	}
	if waterfall.LateRequests != 0 {
		t.Fatalf("late_requests = %d, want 0 (the late row is an async dispatch)", waterfall.LateRequests)
	}
	// The decoded projection carries both signals.
	for _, test := range []struct {
		name string
		row  TransactionEvent
	}{{"phase", events[1]}, {"async_dispatch", events[2]}} {
		line, err := json.Marshal(test.row)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeArchiveWaterfallLine(line, 0)
		if err != nil {
			t.Fatal(err)
		}
		if !timeAnatomyRequestExcluded(&decoded) {
			t.Fatalf("%s: decoded row %+v is not excluded", test.name, decoded)
		}
	}
}

// A request the report counts as outside every transaction (async,
// background, or async dispatch) has no enclosing transactions in an
// action-root drill-down, even when a later transaction's window covers it.
func TestTransactionWaterfallExcludedRequestRootHasNoEnclosing(t *testing.T) {
	for _, test := range []struct {
		name  string
		apply func(*TransactionEvent)
	}{
		{"background_request", func(tx *TransactionEvent) { tx.ProtocolEvidence = map[string]any{"background_request": true} }},
		{"async_conversation", func(tx *TransactionEvent) {
			tx.ProtocolEvidence = map[string]any{"async_conversation": map[string]any{"id": "c1"}}
		}},
		{"async_dispatch", func(tx *TransactionEvent) { tx.ProtocolEvidence = map[string]any{"async_dispatch": true} }},
		{"async phase", func(tx *TransactionEvent) { tx.Phase = "Async on_message" }},
	} {
		t.Run(test.name, func(t *testing.T) {
			excluded := taRequest(1, 61, 1, 20, 10, "Excluded", 2)
			test.apply(&excluded)
			events := waterfallPersistIDs([]TransactionEvent{
				taRequest(1, 61, 1, 10, 10, "Ordinary", 1),
				excluded,
				taTransaction(1, 61, 1, 0, 100, "Checkout"),
			})
			record, _ := waterfallRecordForTest(t, events)
			waterfall := buildWaterfallForTest(t, record, waterfallEventID(t, events, "Excluded"), archiveWaterfallHints{}, 500)
			if waterfall.Root.Population != "action" || len(waterfall.EnclosingTransactions) != 0 {
				t.Fatalf("excluded root %s enclosing %+v, want none", waterfall.Root.Population, waterfall.EnclosingTransactions)
			}
			// The ordinary request in the same window is still enclosed.
			ordinary := buildWaterfallForTest(t, record, waterfallEventID(t, events, "Ordinary"), archiveWaterfallHints{}, 500)
			if len(ordinary.EnclosingTransactions) != 1 || ordinary.EnclosingTransactions[0].ActionName != "Checkout" {
				t.Fatalf("ordinary root enclosing %+v, want [Checkout]", ordinary.EnclosingTransactions)
			}
		})
	}
}

// A load that times out (a saturated slot queue, the lease wait, or a long
// build) while the viewer is still waiting is a busy server, not a cancel:
// 503 with Retry-After and copy that says so. Only a request whose own
// context ended reads as canceled.
func TestTransactionWaterfallHandlerTimeoutIsBusyNotCanceled(t *testing.T) {
	t.Run("load timed out", func(t *testing.T) {
		server, report := waterfallHandlerServer(t, waterfallFixture(0))
		id := waterfallRepresentative(t, report, "Checkout").EventID
		setTimeAnatomyCap(t, &archiveWaterfallAcquireLease, func(context.Context, *storage.RunArchiveRecord) (func(), error) {
			return nil, fmt.Errorf("wait to materialize archive %q: %w", "x", context.DeadlineExceeded)
		})
		rec := serveWaterfallForTest(server, id, "")
		if rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		if body := rec.Body.String(); strings.Contains(body, "canceled") || !strings.Contains(body, "busy") {
			t.Fatalf("body %q, want busy copy", body)
		}
		if rec.Header().Get("Retry-After") == "" {
			t.Fatal("no Retry-After on a busy 503")
		}
	})
	t.Run("viewer canceled", func(t *testing.T) {
		server, report := waterfallHandlerServer(t, waterfallFixture(0))
		id := waterfallRepresentative(t, report, "Checkout").EventID
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		setTimeAnatomyCap(t, &archiveWaterfallAcquireLease, func(context.Context, *storage.RunArchiveRecord) (func(), error) {
			cancel()
			return nil, context.Canceled
		})
		req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/test-query/archive/transactions/"+id+"/waterfall", nil).WithContext(ctx)
		req.SetPathValue("id", "test-query")
		req.SetPathValue("event_id", id)
		rec := httptest.NewRecorder()
		server.handleGetRunArchiveTransactionWaterfall(rec, req)
		if rec.Code != http.StatusServiceUnavailable || !strings.Contains(rec.Body.String(), "canceled") {
			t.Fatalf("status %d body %q, want 503 canceled", rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Retry-After") != "" {
			t.Fatal("a canceled request got Retry-After")
		}
	})
}

// What the drawer receives when the report's join hit run-wide state: the
// one-identity replay claims every request, so partial is false and a
// decomposition is present. join_may_differ (with its reasons) is the only
// signal that the report may count this instance as partial, so it is the
// field the drawer must read.
func TestTransactionWaterfallJoinMayDifferPayload(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 16)
	events := []TransactionEvent{
		taRequest(1, 1, 1, 0, 10, "A", 1),
		taRequest(1, 1, 1, 10, 10, "B", 2),
		taRequest(1, 1, 1, 20, 10, "C", 3),
	}
	for arrival := 0; arrival < 20; arrival++ {
		life := uint64(arrival+2) * 2654435761
		start := float64(1000 + arrival*40)
		for step := 0; step < 3; step++ {
			events = append(events, taRequest(2+arrival%5, life, 1, start+float64(step*10), 8, "R", step+1))
		}
		events = append(events, taTransaction(2+arrival%5, life, 1, start, 30, "Arrival"))
	}
	events = append(events, taTransaction(1, 1, 1, 0, 100, "Outer"))
	server, report := waterfallHandlerServer(t, waterfallPersistIDs(events))
	outer := waterfallRepresentative(t, report, "Outer")
	rec := serveWaterfallForTest(server, outer.EventID, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var payload map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if string(payload["join_may_differ"]) != "true" || string(payload["join_may_differ_reasons"]) != `["`+timeAnatomyReasonPendingGlobalCap+`"]` {
		t.Fatalf("join_may_differ %s reasons %s", payload["join_may_differ"], payload["join_may_differ_reasons"])
	}
	if _, ok := payload["partial"]; ok {
		t.Fatalf("partial = %s, want omitted (the identity's own state dropped nothing)", payload["partial"])
	}
	if _, ok := payload["decomposition"]; !ok {
		t.Fatal("no decomposition: the drill-down claimed every request")
	}
	if string(payload["requests_total"]) != "3" || outer.Children >= 3 {
		t.Fatalf("requests_total %s, representative children %d: want 3 > children", payload["requests_total"], outer.Children)
	}
	// The backend never sends a decomposition_partial field.
	if _, ok := payload["decomposition_partial"]; ok {
		t.Fatal("unexpected decomposition_partial field")
	}
}
