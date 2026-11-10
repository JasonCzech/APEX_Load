package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"
)

// The drill-down's late_requests applies the report's late test, dispatch
// half included: a callback request (async_dispatch_epoch > 0) written after
// a main-flow root is not late for it (the root could never claim it), but
// is late for a root opened in that same dispatch.
func TestTransactionWaterfallLateRequestsHonorDispatchEpoch(t *testing.T) {
	for _, test := range []struct {
		name      string
		rootEpoch int64
		wantLate  int
	}{
		{"main-flow root", 0, 1},
		{"same-dispatch root", 3, 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := taTransaction(1, 51, 1, 0, 100, "Login")
			if test.rootEpoch > 0 {
				root.ProtocolEvidence = map[string]any{timeAnatomyDispatchEpochKey: test.rootEpoch}
			}
			events := waterfallPersistIDs([]TransactionEvent{
				taRequest(1, 51, 1, 0, 10, "A", 1),
				root,
				taDispatch(taRequest(1, 51, 1, 20, 10, "LateCallback", 2), 3),
				taRequest(1, 51, 1, 30, 10, "LateMain", 3),
			})
			feeder := newTAFeeder()
			feeder.feed(slices.Clone(events)...)
			anatomy := feeder.finalize(t)
			if anatomy.Coverage.LateRequestRows != int64(test.wantLate) {
				t.Fatalf("report late rows %d, want %d", anatomy.Coverage.LateRequestRows, test.wantLate)
			}
			record, _ := waterfallRecordForTest(t, events)
			instance := waterfallInstances(t, waterfallAccumulatorFor(events), "Login")[0]
			waterfall := assertWaterfallIsReportInstance(t, record, instance, false)
			if waterfall.LateRequests != test.wantLate {
				t.Fatalf("drill-down late_requests %d, want the report's %d", waterfall.LateRequests, test.wantLate)
			}
		})
	}
}

// The every-budget parity sweep with a per-row allowance: with
// timeAnatomyWorkRowsPerFloor lowered, timeAnatomyWorkBudget(rows) grows by
// a non-zero amount per row, so the report's budget (a.rows) and the
// replay's (ordinal+1) must count rows the same way for the verdicts to
// agree at every floor.
func TestTransactionWaterfallWorkChargesMatchReportWithPerRowBudget(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		events []TransactionEvent
	}{
		{"iteration flushes", waterfallIterationFixture()},
		{"legacy restart", waterfallLegacyRestartFixture()},
	} {
		for _, rowsPerFloor := range []int64{1, 2, 4} {
			t.Run(fmt.Sprintf("%s/rows per floor %d", fixture.name, rowsPerFloor), func(t *testing.T) {
				setTimeAnatomyCap(t, &timeAnatomyWorkRowsPerFloor, rowsPerFloor)
				record, _ := waterfallRecordForTest(t, fixture.events)
				total := waterfallAccumulatorFor(fixture.events).work
				if total <= 0 {
					t.Fatal("fixture charged no work")
				}
				var sawRefused, sawJoined, sawPerRow bool
				for floor := int64(1); floor <= total+2; floor++ {
					setTimeAnatomyCap(t, &maxTimeAnatomyWork, floor)
					if timeAnatomyWorkBudget(1) > floor {
						sawPerRow = true
					}
					acc := waterfallAccumulatorFor(fixture.events)
					instance := waterfallInstances(t, acc, "Root")[0]
					partial := waterfallReportPartial(t, acc, "Root")
					assertWaterfallIsReportInstance(t, record, instance, partial)
					switch {
					case partial && instance.children == 0:
						sawRefused = true
					case instance.joined:
						sawJoined = true
					}
				}
				if !sawPerRow || !sawRefused || !sawJoined {
					t.Fatalf("rows per floor %d: sweep per-row %v refused %v joined %v", rowsPerFloor, sawPerRow, sawRefused, sawJoined)
				}
			})
		}
	}
}

// serveWaterfallAs serves a drill-down for user (nil: unauthenticated) from
// remoteAddr.
func serveWaterfallAs(server *Server, eventID, remoteAddr string, user *AuthenticatedUser) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tests/test-query/archive/transactions/"+eventID+"/waterfall", nil)
	req.RemoteAddr = remoteAddr
	if user != nil {
		req = req.WithContext(context.WithValue(req.Context(), authUserContextKey{}, user))
	}
	req.SetPathValue("id", "test-query")
	req.SetPathValue("event_id", eventID)
	rec := httptest.NewRecorder()
	server.handleGetRunArchiveTransactionWaterfall(rec, req)
	return rec
}

// With no authenticated principal (auth disabled), every viewer behind an
// ingress has the proxy's RemoteAddr, so the drill-down takes no per-caller
// share: a share held under the old peer key does not refuse anyone, and no
// peer key is ever taken.
func TestTransactionWaterfallUnauthenticatedCallersShareNoPeerCap(t *testing.T) {
	setTimeAnatomyCap(t, &archiveWaterfallCallerRequests, 1)
	setTimeAnatomyCap(t, &archiveWaterfallCallerShares, newArchiveWaterfallShares())
	server, report := waterfallHandlerServer(t, waterfallFixture(0))
	id := waterfallRepresentative(t, report, "Checkout").EventID
	const proxy = "10.0.0.9:443"
	hold, ok := archiveWaterfallCallerShares.tryAcquire("peer:10.0.0.9", 1)
	if !ok {
		t.Fatal("could not hold the peer share")
	}
	defer hold()
	if rec := serveWaterfallAs(server, id, proxy, nil); rec.Code != http.StatusOK {
		t.Fatalf("unauthenticated viewer behind the proxy got %d: %s", rec.Code, rec.Body.String())
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = proxy
	if key, ok := archiveWaterfallCaller(req); ok {
		t.Fatalf("unauthenticated request keyed as %q", key)
	}
}

// Unauthenticated callers share one coarse bound: past it a new drill-down is
// answered 429 at once instead of queueing a load that holds an archive lease.
func TestTransactionWaterfallUnauthenticatedCallersShareACoarseBound(t *testing.T) {
	setTimeAnatomyCap(t, &archiveWaterfallAnonymousRequests, 1)
	setTimeAnatomyCap(t, &archiveWaterfallCallerShares, newArchiveWaterfallShares())
	server, report := waterfallHandlerServer(t, waterfallFixture(0))
	id := waterfallRepresentative(t, report, "Checkout").EventID
	if rec := serveWaterfallAs(server, id, "10.0.0.9:443", nil); rec.Code != http.StatusOK {
		t.Fatalf("first unauthenticated drill-down got %d: %s", rec.Code, rec.Body.String())
	}
	hold, ok := archiveWaterfallCallerShares.tryAcquire(archiveWaterfallAnonymousCaller, 1)
	if !ok {
		t.Fatal("could not hold the anonymous share")
	}
	defer hold()
	rec := serveWaterfallAs(server, id, "10.0.0.10:443", nil)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("unauthenticated drill-down past the shared bound got %d (Retry-After %q), want 429", rec.Code, rec.Header().Get("Retry-After"))
	}
}
