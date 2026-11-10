package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// blockFirstWaterfallDecode blocks the first line the drill-down decodes
// until release is called; started is closed when it blocks.
func blockFirstWaterfallDecode(t *testing.T) (started <-chan struct{}, release func()) {
	t.Helper()
	startedCh, releaseCh := make(chan struct{}), make(chan struct{})
	var once, releaseOnce sync.Once
	previous := archiveWaterfallDecodeHook
	archiveWaterfallDecodeHook = func(int64) {
		once.Do(func() {
			close(startedCh)
			<-releaseCh
		})
	}
	release = func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(func() {
		release()
		archiveWaterfallDecodeHook = previous
	})
	return startedCh, release
}

// Async-conversation responses and background requests are never part of a
// transaction (timeAnatomyRequestExcluded): the drill-down claims and counts
// as late exactly what the report does.
func TestTransactionWaterfallExcludesAsyncAndBackgroundRequests(t *testing.T) {
	poll := taRequest(1, 31, 1, 20, 500, "Poll", 2)
	poll.ProtocolEvidence = map[string]any{"async_conversation": map[string]any{"id": "c1"}}
	beacon := taRequest(1, 31, 1, 40, 10, "Beacon", 3)
	beacon.ProtocolEvidence = map[string]any{"background_request": true}
	lateBeacon := taRequest(1, 31, 1, 60, 10, "LateBeacon", 4)
	lateBeacon.ProtocolEvidence = map[string]any{"background_request": true}
	notBackground := taRequest(1, 31, 1, 70, 10, "Foreground", 5)
	notBackground.ProtocolEvidence = map[string]any{"background_request": false}
	events := waterfallPersistIDs([]TransactionEvent{
		taRequest(1, 31, 1, 0, 10, "A", 1),
		poll,
		beacon,
		notBackground,
		taTransaction(1, 31, 1, 0, 100, "Login"),
		lateBeacon,
	})
	acc := waterfallAccumulatorFor(events)
	record, _ := waterfallRecordForTest(t, events)
	instance := waterfallInstances(t, acc, "Login")[0]
	if !instance.joined || instance.children != 2 {
		t.Fatalf("report instance joined %v children %d, want 2 (A, Foreground)", instance.joined, instance.children)
	}
	waterfall := assertWaterfallIsReportInstance(t, record, instance, false)
	if names := waterfallNames(waterfall); !reflect.DeepEqual(names, []string{"A", "Foreground"}) {
		t.Fatalf("claimed %v, want [A Foreground]", names)
	}
	if waterfall.LateRequests != 0 {
		t.Fatalf("late_requests = %d, want 0 (the late row is a background request)", waterfall.LateRequests)
	}
}

// A reused legacy VUser ID: the report releases the previous lifetime's
// buffered requests (restartLegacyLifetime), so the new lifetime's
// transaction never claims them, and neither does the drill-down.
func TestTransactionWaterfallLegacyIDReuseMatchesReport(t *testing.T) {
	ended := func(event TransactionEvent) TransactionEvent {
		event.EndsIteration = true
		return event
	}
	events := waterfallPersistIDs([]TransactionEvent{
		taRequest(7, 0, 1, 1000, 10, "Old1", 1),
		ended(taTransaction(7, 0, 1, 1000, 20, "Iter")),
		taRequest(7, 0, 2, 2000, 10, "Old2", 1),
		ended(taTransaction(7, 0, 2, 2000, 20, "Iter")),
		// A new VUser reuses ID 7 at iteration 1.
		taRequest(7, 0, 1, 1990, 10, "New1", 1),
		taTransaction(7, 0, 1, 1985, 45, "New"),
	})
	acc := waterfallAccumulatorFor(events)
	record, _ := waterfallRecordForTest(t, events)
	instance := waterfallInstances(t, acc, "New")[0]
	if !instance.joined || instance.children != 1 {
		t.Fatalf("report instance joined %v children %d, want New1 only", instance.joined, instance.children)
	}
	waterfall := assertWaterfallIsReportInstance(t, record, instance, false)
	if names := waterfallNames(waterfall); !reflect.DeepEqual(names, []string{"New1"}) || waterfall.IdentityQuality != timeAnatomyIdentityLegacy {
		t.Fatalf("claimed %v (identity %s), want [New1]", names, waterfall.IdentityQuality)
	}
}

// The report's work budget: once it is spent (by requests, or by the root's
// own candidate scan) the root claims nothing and is partial. The drill-down
// charges the same work and says so.
func TestTransactionWaterfallWorkBudgetMatchesReport(t *testing.T) {
	const requests = 10
	var events []TransactionEvent
	for step := 0; step < requests; step++ {
		events = append(events, taRequest(1, 41, 1, float64(step*10), 10, fmt.Sprintf("R%02d", step), step+1))
	}
	events = append(events, taTransaction(1, 41, 1, 0, requests*10, "Session"))
	events = waterfallPersistIDs(events)
	for _, test := range []struct {
		name   string
		budget int64
	}{
		{"spent by requests", 5},
		// Each in-order request costs 1; the root's scan costs requests+1.
		{"spent by the root's scan", requests + requests},
	} {
		t.Run(test.name, func(t *testing.T) {
			setTimeAnatomyCap(t, &maxTimeAnatomyWork, test.budget)
			acc := waterfallAccumulatorFor(events)
			if _, ok := acc.reasons[timeAnatomyReasonWorkBudget]; !ok || !acc.workExhausted {
				t.Fatalf("fixture did not spend the work budget: %+v", acc.reasons)
			}
			record, _ := waterfallRecordForTest(t, events)
			instance := waterfallInstances(t, acc, "Session")[0]
			if instance.joined || instance.children != 0 {
				t.Fatalf("report instance joined %v children %d", instance.joined, instance.children)
			}
			waterfall := assertWaterfallIsReportInstance(t, record, instance, true)
			if !reflect.DeepEqual(waterfall.PartialReasons, []string{timeAnatomyReasonWorkBudget}) {
				t.Fatalf("partial reasons %v, want [work_budget]", waterfall.PartialReasons)
			}
		})
	}
	// Under the default budget the same archive joins.
	acc := waterfallAccumulatorFor(events)
	record, _ := waterfallRecordForTest(t, events)
	assertWaterfallIsReportInstance(t, record, waterfallInstances(t, acc, "Session")[0], false)
}

// A partial root has its claimed requests but no decomposition: the report
// keeps it out of its decomposition, and the requests it lost would show as
// time outside requests.
func TestTransactionWaterfallPartialHasNoDecomposition(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, 2)
	events := waterfallPersistIDs([]TransactionEvent{
		taRequest(1, 11, 1, 0, 10, "A", 1),
		taRequest(1, 11, 1, 10, 10, "B", 2),
		taRequest(1, 11, 1, 20, 10, "C", 3),
		taTransaction(1, 11, 1, 0, 30, "T"),
	})
	record, report := waterfallRecordForTest(t, events)
	representative := waterfallRepresentative(t, report, "T")
	waterfall := buildWaterfallForTest(t, record, representative.EventID, archiveWaterfallHints{}, 500)
	if !waterfall.Partial || waterfall.Decomposition != nil || waterfall.RequestsTotal != 2 {
		t.Fatalf("partial %v decomposition %+v requests_total %d", waterfall.Partial, waterfall.Decomposition, waterfall.RequestsTotal)
	}
	raw, err := json.Marshal(waterfall)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	if _, ok := payload["decomposition"]; ok || payload["partial"] != true {
		t.Fatalf("payload = %s", raw)
	}
}

// When the last caller stops waiting, the load it started is canceled and
// leaves the in-flight registry, so a later caller starts a fresh load.
func TestTransactionWaterfallLastWaiterCancelsLoad(t *testing.T) {
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	representative := waterfallRepresentative(t, report, "Checkout")
	cache := newArchiveWaterfallCache()
	key := archiveWaterfallCacheKey(record, representative.EventID)
	loadContexts := make(chan context.Context, 4)
	previous := archiveWaterfallLoadHook
	archiveWaterfallLoadHook = func(ctx context.Context) { loadContexts <- ctx }
	t.Cleanup(func() { archiveWaterfallLoadHook = previous })
	started, release := blockFirstWaterfallDecode(t)

	ctx, cancel := context.WithCancel(context.Background())
	caller := make(chan error, 1)
	go func() {
		_, err := cachedArchiveTransactionWaterfall(ctx, cache, record, representative.EventID, archiveWaterfallHints{}, 500)
		caller <- err
	}()
	<-started
	loadCtx := <-loadContexts
	cancel()
	if err := <-caller; !errors.Is(err, context.Canceled) {
		t.Fatalf("caller error = %v", err)
	}
	select {
	case <-loadCtx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("the load kept running after its last caller left")
	}
	if waiters := archiveWaterfallWaiters(cache, key); waiters != -1 {
		t.Fatalf("abandoned load still in flight (%d waiters)", waiters)
	}
	release()
	waterfall, err := cachedArchiveTransactionWaterfall(context.Background(), cache, record, representative.EventID, archiveWaterfallHints{}, 500)
	if err != nil || waterfall.RequestsTotal != 3 {
		t.Fatalf("fresh load = %+v, %v", waterfall, err)
	}
	if len(loadContexts) != 1 {
		t.Fatalf("%d further loads, want the one fresh load", len(loadContexts))
	}
}

// Drill-down loads wait for a process-wide slot: with one slot, a second
// event's load does not start reading while the first holds it, and a
// caller that gives up while queued never starts one.
func TestTransactionWaterfallLoadSlotsBoundConcurrentLoads(t *testing.T) {
	setTimeAnatomyCap(t, &archiveWaterfallLoadSlots, make(chan struct{}, 1))
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	checkout := waterfallRepresentative(t, report, "Checkout")
	inner := waterfallRepresentative(t, report, "Inner")
	cache := newArchiveWaterfallCache()
	loads := countWaterfallLoads(t)
	started, release := blockFirstWaterfallDecode(t)

	first := make(chan error, 1)
	go func() {
		_, err := cachedArchiveTransactionWaterfall(context.Background(), cache, record, checkout.EventID, archiveWaterfallHints{}, 500)
		first <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	second := make(chan error, 1)
	go func() {
		_, err := cachedArchiveTransactionWaterfall(ctx, cache, record, inner.EventID, archiveWaterfallHints{}, 500)
		second <- err
	}()
	waitForWaterfallWaiters(t, cache, archiveWaterfallCacheKey(record, inner.EventID), 1)
	time.Sleep(50 * time.Millisecond)
	if got := loads.Load(); got != 1 {
		t.Fatalf("%d loads reading at once, want 1", got)
	}
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued caller error = %v", err)
	}
	release()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("%d loads ran, want only the first (the queued one was abandoned)", got)
	}
	if len(archiveWaterfallLoadSlots) != 0 {
		t.Fatalf("%d load slots still held", len(archiveWaterfallLoadSlots))
	}
}

// max_requests does not split the cache: one load serves every value, and
// each caller gets its own cut without changing the cached result.
func TestTransactionWaterfallMaxRequestsShareOneLoad(t *testing.T) {
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	representative := waterfallRepresentative(t, report, "Checkout")
	cache := newArchiveWaterfallCache()
	loads := countWaterfallLoads(t)
	get := func(maxRequests int) *archiveWaterfallResult {
		waterfall, err := cachedArchiveTransactionWaterfall(context.Background(), cache, record, representative.EventID, archiveWaterfallHints{}, maxRequests)
		if err != nil {
			t.Fatal(err)
		}
		return waterfall
	}
	one := get(1)
	all := get(500)
	two := get(2)
	if loads.Load() != 1 {
		t.Fatalf("%d loads, want 1", loads.Load())
	}
	for _, test := range []struct {
		waterfall *archiveWaterfallResult
		rows      int
		truncated bool
	}{{one, 1, true}, {two, 2, true}, {all, 3, false}} {
		if len(test.waterfall.Requests) != test.rows || test.waterfall.Truncated != test.truncated || test.waterfall.RequestsTotal != 3 || test.waterfall.Decomposition == nil {
			t.Fatalf("rows %d truncated %v total %d, want %d %v 3", len(test.waterfall.Requests), test.waterfall.Truncated, test.waterfall.RequestsTotal, test.rows, test.truncated)
		}
	}
	if again := get(500); len(again.Requests) != 3 || again.Truncated {
		t.Fatalf("a cut changed the cached result: %d rows truncated %v", len(again.Requests), again.Truncated)
	}
}

// The cache-owned load holds its own archive lease for every line it reads,
// so it does not depend on a caller's lease.
func TestTransactionWaterfallLoadHoldsItsOwnLease(t *testing.T) {
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	representative := waterfallRepresentative(t, report, "Checkout")
	var held, acquired atomic.Int32
	setTimeAnatomyCap(t, &archiveWaterfallAcquireLease, func(context.Context, *storage.RunArchiveRecord) (func(), error) {
		held.Add(1)
		acquired.Add(1)
		return func() { held.Add(-1) }, nil
	})
	var unleased atomic.Int32
	previous := archiveWaterfallDecodeHook
	archiveWaterfallDecodeHook = func(int64) {
		if held.Load() < 1 {
			unleased.Add(1)
		}
	}
	t.Cleanup(func() { archiveWaterfallDecodeHook = previous })
	if _, err := cachedArchiveTransactionWaterfall(context.Background(), newArchiveWaterfallCache(), record, representative.EventID, archiveWaterfallHints{}, 500); err != nil {
		t.Fatal(err)
	}
	if acquired.Load() != 1 || unleased.Load() != 0 || held.Load() != 0 {
		t.Fatalf("leases acquired %d, lines read without one %d, still held %d", acquired.Load(), unleased.Load(), held.Load())
	}
}

// The report's run-wide state (here the global cap) cannot be replayed from
// one identity: the drill-down says its join may differ, with the reason.
// An archive the report joined without run-wide drops says nothing.
func TestTransactionWaterfallHandlerJoinMayDiffer(t *testing.T) {
	decode := func(t *testing.T, server *Server, eventID string) archiveWaterfallResult {
		t.Helper()
		rec := serveWaterfallForTest(server, eventID, "")
		if rec.Code != http.StatusOK {
			t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
		var waterfall archiveWaterfallResult
		if err := json.Unmarshal(rec.Body.Bytes(), &waterfall); err != nil {
			t.Fatal(err)
		}
		return waterfall
	}
	t.Run("global cap", func(t *testing.T) {
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
		waterfall := decode(t, server, outer.EventID)
		if !waterfall.JoinMayDiffer || !reflect.DeepEqual(waterfall.JoinMayDifferReasons, []string{timeAnatomyReasonPendingGlobalCap}) {
			t.Fatalf("join_may_differ %v reasons %v", waterfall.JoinMayDiffer, waterfall.JoinMayDifferReasons)
		}
		// Run-wide state the drill-down cannot replay is not a partial instance
		// of its own: the drill-down still has every request, so it keeps the
		// decomposition and only flags that the report's join may differ.
		if waterfall.Partial || len(waterfall.PartialReasons) != 0 || waterfall.Decomposition == nil {
			t.Fatalf("global-cap drill-down partial=%v reasons=%v decomposition=%v", waterfall.Partial, waterfall.PartialReasons, waterfall.Decomposition)
		}
	})
	t.Run("no run-wide drops", func(t *testing.T) {
		server, report := waterfallHandlerServer(t, waterfallFixture(0))
		waterfall := decode(t, server, waterfallRepresentative(t, report, "Checkout").EventID)
		if waterfall.JoinMayDiffer || len(waterfall.JoinMayDifferReasons) != 0 {
			t.Fatalf("join_may_differ %v reasons %v", waterfall.JoinMayDiffer, waterfall.JoinMayDifferReasons)
		}
	})
}
