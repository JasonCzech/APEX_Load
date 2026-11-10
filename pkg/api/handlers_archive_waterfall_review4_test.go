package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// waterfallReportPartial reports whether the report marked name's (single)
// instance partial.
func waterfallReportPartial(t *testing.T, acc *archiveTimeAnatomyAccumulator, name string) bool {
	t.Helper()
	entry := acc.names[name]
	if entry == nil {
		t.Fatalf("no %s instances", name)
	}
	return entry.partial > 0
}

// waterfallIterationFixture is one identity running iterations of three
// requests; odd iterations close an Iter transaction around theirs, and the
// last one closes Root. Every iteration change past the second walks (and
// charges) the buffer, and every Iter transaction charges its fold.
func waterfallIterationFixture() []TransactionEvent {
	const iterations = 12
	var events []TransactionEvent
	for iteration := int64(1); iteration <= iterations; iteration++ {
		start := float64(iteration) * 100
		for step := 0; step < 3; step++ {
			events = append(events, taRequest(1, 51, iteration, start+float64(step*10), 10, fmt.Sprintf("I%02dR%d", iteration, step), step+1))
		}
		switch {
		case iteration == iterations:
			events = append(events, taTransaction(1, 51, iteration, start, 30, "Root"))
		case iteration%2 == 1:
			events = append(events, taTransaction(1, 51, iteration, start, 30, "Iter"))
		}
	}
	return waterfallPersistIDs(events)
}

// waterfallLegacyRestartFixture is a legacy identity (no lifetime) whose ID a
// new VUser reuses: the report's restart walk is charged, then Root joins
// the new lifetime's requests.
func waterfallLegacyRestartFixture() []TransactionEvent {
	ended := func(event TransactionEvent) TransactionEvent {
		event.EndsIteration = true
		return event
	}
	return waterfallPersistIDs([]TransactionEvent{
		taRequest(7, 0, 1, 1000, 10, "Old1a", 1),
		taRequest(7, 0, 1, 1010, 10, "Old1b", 2),
		ended(taTransaction(7, 0, 1, 1000, 20, "Iter")),
		taRequest(7, 0, 2, 2000, 10, "Old2a", 1),
		taRequest(7, 0, 2, 2010, 10, "Old2b", 2),
		ended(taTransaction(7, 0, 2, 2000, 20, "Iter")),
		// A new VUser reuses ID 7 at iteration 1.
		taRequest(7, 0, 1, 3000, 10, "New1", 1),
		taRequest(7, 0, 1, 3010, 10, "New2", 2),
		taTransaction(7, 0, 1, 3000, 20, "Root"),
	})
}

// For every work budget from 1 to past what the archive needs, the
// drill-down's verdict for Root is the report's: the same claimed requests
// (none when the budget ran out before Root's scan, all of them when only
// its fold was refused) and the same partial verdict. The replay charges
// every walk the report charges: iteration flushes, the legacy restart
// walk, earlier transactions' scans and folds, and Root's own scan and fold.
func TestTransactionWaterfallWorkChargesMatchReportAtEveryBudget(t *testing.T) {
	for _, fixture := range []struct {
		name   string
		events []TransactionEvent
	}{
		{"iteration flushes", waterfallIterationFixture()},
		{"legacy restart", waterfallLegacyRestartFixture()},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			record, _ := waterfallRecordForTest(t, fixture.events)
			total := waterfallAccumulatorFor(fixture.events).work
			if total <= 0 {
				t.Fatal("fixture charged no work")
			}
			var sawRefused, sawFoldRefused, sawJoined bool
			for budget := int64(1); budget <= total+2; budget++ {
				setTimeAnatomyCap(t, &maxTimeAnatomyWork, budget)
				acc := waterfallAccumulatorFor(fixture.events)
				instance := waterfallInstances(t, acc, "Root")[0]
				partial := waterfallReportPartial(t, acc, "Root")
				waterfall := assertWaterfallIsReportInstance(t, record, instance, partial)
				switch {
				case partial && instance.children == 0:
					sawRefused = true
				case partial:
					sawFoldRefused = true
					if !reflect.DeepEqual(waterfall.PartialReasons, []string{timeAnatomyReasonWorkBudget}) {
						t.Fatalf("budget %d: partial reasons %v, want [work_budget]", budget, waterfall.PartialReasons)
					}
				case instance.joined:
					sawJoined = true
				}
			}
			if !sawRefused || !sawFoldRefused || !sawJoined {
				t.Fatalf("sweep did not reach every verdict: refused %v fold refused %v joined %v", sawRefused, sawFoldRefused, sawJoined)
			}
		})
	}
}

// One unit under the report's total need, the report refuses its last
// charge, Root's fold: Root keeps its three claimed children and is partial
// (work_budget). The drill-down must reach the same verdict, and it can only
// do so if its replay charges every walk the report charged before that fold
// (the iteration flushes included): a replay that skipped any of them would
// still have room for the fold at this budget and return Root joined.
func TestTransactionWaterfallIterationFlushesSpendTheBudgetLikeTheReport(t *testing.T) {
	events := waterfallIterationFixture()
	record, _ := waterfallRecordForTest(t, events)
	total := waterfallAccumulatorFor(events).work
	setTimeAnatomyCap(t, &maxTimeAnatomyWork, total-1)
	acc := waterfallAccumulatorFor(events)
	instance := waterfallInstances(t, acc, "Root")[0]
	if !waterfallReportPartial(t, acc, "Root") {
		t.Fatalf("report Root is not partial one unit under its need %d", total)
	}
	if instance.children != 3 {
		t.Fatalf("report Root claimed %d children, want 3 (only its fold is refused)", instance.children)
	}
	waterfall := assertWaterfallIsReportInstance(t, record, instance, true)
	if !reflect.DeepEqual(waterfall.PartialReasons, []string{timeAnatomyReasonWorkBudget}) {
		t.Fatalf("partial reasons %v", waterfall.PartialReasons)
	}
}

// Shared decision 1: a dispatch-recorded request is claimed only by the
// transaction opened in the same dispatch (same async_dispatch_epoch). The
// drill-down decodes the epoch and mirrors the report through the archive's
// JSON round trip, for a transaction root and for a request root's
// enclosing list.
func TestTransactionWaterfallDispatchEpochMatchesReport(t *testing.T) {
	dispatch := func(event TransactionEvent, epoch int64, phase string) TransactionEvent {
		event.Phase = phase
		event.ProtocolEvidence = map[string]any{"async_dispatch": true}
		if epoch > 0 {
			event.ProtocolEvidence[timeAnatomyDispatchEpochKey] = epoch
		}
		return event
	}
	opened := func(event TransactionEvent, epoch int64) TransactionEvent {
		event.ProtocolEvidence = map[string]any{timeAnatomyDispatchEpochKey: epoch}
		return event
	}
	conversation := taRequest(2, 61, 1, 50, 5, "Conversation", 7)
	conversation.ProtocolEvidence = map[string]any{"async_conversation": true, timeAnatomyDispatchEpochKey: int64(5)}
	events := waterfallPersistIDs([]TransactionEvent{
		taRequest(2, 61, 1, 10, 10, "A", 1),
		dispatch(taRequest(2, 61, 1, 20, 10, "PollReq", 2), 5, "async on_response"),
		dispatch(taRequest(2, 61, 1, 30, 10, "PollBranch", 3), 5, ""),
		dispatch(taRequest(2, 61, 1, 40, 5, "OtherDispatch", 4), 6, "async on_response"),
		dispatch(taRequest(2, 61, 1, 45, 5, "Unstamped", 5), 0, "async on_response"),
		conversation,
		opened(taTransaction(2, 61, 1, 15, 45, "poll"), 5),
		taTransaction(2, 61, 1, 0, 100, "Login"),
	})
	acc := waterfallAccumulatorFor(events)
	record, _ := waterfallRecordForTest(t, events)
	for _, want := range []struct {
		name   string
		claims []string
	}{
		{"poll", []string{"PollReq", "PollBranch"}},
		{"Login", []string{"A"}},
	} {
		instance := waterfallInstances(t, acc, want.name)[0]
		if !instance.joined || int(instance.children) != len(want.claims) {
			t.Fatalf("report %s joined %v children %d, want %v", want.name, instance.joined, instance.children, want.claims)
		}
		waterfall := assertWaterfallIsReportInstance(t, record, instance, false)
		if names := waterfallNames(waterfall); !reflect.DeepEqual(names, want.claims) {
			t.Fatalf("%s claimed %v, want %v", want.name, names, want.claims)
		}
	}
	for _, want := range []struct {
		request   string
		enclosing []string
	}{
		{"PollReq", []string{"poll"}},
		{"A", []string{"Login"}},
		{"OtherDispatch", nil},
		{"Unstamped", nil},
		{"Conversation", nil},
	} {
		waterfall := buildWaterfallForTest(t, record, waterfallEventID(t, events, want.request), archiveWaterfallHints{}, 500)
		var names []string
		for _, ref := range waterfall.EnclosingTransactions {
			names = append(names, ref.ActionName)
		}
		if !reflect.DeepEqual(names, want.enclosing) {
			t.Fatalf("%s enclosing %v, want %v", want.request, names, want.enclosing)
		}
	}
}

// An event the archive does not hold is remembered for the TTL: asking
// again does not reload (rescan) the archive until it expires.
func TestTransactionWaterfallNotFoundIsRememberedBriefly(t *testing.T) {
	setTimeAnatomyCap(t, &archiveWaterfallMisses, newArchiveWaterfallMissCache())
	now := time.Unix(1_700_000_000, 0)
	setTimeAnatomyCap(t, &archiveWaterfallNow, func() time.Time { return now })
	record, _ := waterfallRecordForTest(t, waterfallFixture(0))
	cache := newArchiveWaterfallCache()
	loads := countWaterfallLoads(t)
	missing := fmt.Sprintf("tx_%032x", 0xdead)
	ask := func() {
		t.Helper()
		if _, err := cachedArchiveTransactionWaterfall(context.Background(), cache, record, missing, archiveWaterfallHints{}, 500); !errors.Is(err, errArchiveWaterfallNotFound) {
			t.Fatalf("error = %v, want not found", err)
		}
	}
	ask()
	ask()
	if got := loads.Load(); got != 1 {
		t.Fatalf("%d loads for a repeated unknown event, want 1", got)
	}
	now = now.Add(archiveWaterfallNotFoundTTL)
	ask()
	if got := loads.Load(); got != 2 {
		t.Fatalf("%d loads after the TTL, want 2", got)
	}
}

func TestTransactionWaterfallMissCacheIsBounded(t *testing.T) {
	setTimeAnatomyCap(t, &archiveWaterfallNotFoundLimit, 2)
	misses := newArchiveWaterfallMissCache()
	for index := 0; index < 5; index++ {
		misses.add(fmt.Sprintf("key-%d", index))
	}
	if got := misses.len(); got != 2 || !misses.has("key-4") {
		t.Fatalf("miss cache holds %d entries (latest kept %v), want 2", got, misses.has("key-4"))
	}
}

// One test's loads hold at most archiveWaterfallTestSlots of the load slots:
// a second load of the same run waits, while a load of another run reads at
// once.
func TestTransactionWaterfallOneTestHoldsAtMostItsShareOfSlots(t *testing.T) {
	setTimeAnatomyCap(t, &archiveWaterfallTestSlots, 1)
	setTimeAnatomyCap(t, &archiveWaterfallTestShares, newArchiveWaterfallShares())
	recordA, reportA := waterfallRecordForTest(t, waterfallFixture(0))
	recordA.TestID = "test-a"
	recordB, reportB := waterfallRecordForTest(t, waterfallFixture(1))
	recordB.TestID = "test-b"
	checkoutA := waterfallRepresentative(t, reportA, "Checkout")
	innerA := waterfallRepresentative(t, reportA, "Inner")
	checkoutB := waterfallRepresentative(t, reportB, "Checkout")
	cache := newArchiveWaterfallCache()
	loads := countWaterfallLoads(t)
	// Block only the first decode (blockFirstWaterfallDecode's Once would
	// hold every other decode behind it too).
	startedCh, releaseCh := make(chan struct{}), make(chan struct{})
	var decodes atomic.Int32
	previous := archiveWaterfallDecodeHook
	archiveWaterfallDecodeHook = func(int64) {
		if decodes.Add(1) == 1 {
			close(startedCh)
			<-releaseCh
		}
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { close(releaseCh) }) }
	t.Cleanup(func() {
		release()
		archiveWaterfallDecodeHook = previous
	})
	started := (<-chan struct{})(startedCh)

	first := make(chan error, 1)
	go func() {
		_, err := cachedArchiveTransactionWaterfall(context.Background(), cache, recordA, checkoutA.EventID, archiveWaterfallHints{}, 500)
		first <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	second := make(chan error, 1)
	go func() {
		_, err := cachedArchiveTransactionWaterfall(ctx, cache, recordA, innerA.EventID, archiveWaterfallHints{}, 500)
		second <- err
	}()
	waitForWaterfallWaiters(t, cache, archiveWaterfallCacheKey(recordA, innerA.EventID), 1)
	// Another run's load is not held behind test-a's.
	if _, err := cachedArchiveTransactionWaterfall(context.Background(), cache, recordB, checkoutB.EventID, archiveWaterfallHints{}, 500); err != nil {
		t.Fatalf("other run's load: %v", err)
	}
	if got := loads.Load(); got != 2 {
		t.Fatalf("%d loads read, want test-a's first and test-b's (test-a's second waits)", got)
	}
	cancel()
	if err := <-second; !errors.Is(err, context.Canceled) {
		t.Fatalf("queued same-run caller error = %v", err)
	}
	release()
	if err := <-first; err != nil {
		t.Fatal(err)
	}
	if held := archiveWaterfallTestShares.count(archiveWaterfallTestShareKey(recordA)); held != 0 {
		t.Fatalf("test-a still holds %d shares", held)
	}
}

// One caller has at most archiveWaterfallCallerRequests drill-downs in
// flight: past that it gets 429 with Retry-After at once, another caller is
// admitted, and the share is returned when the request ends. The caller is
// the authenticated principal, so two users behind one proxy address are
// two callers.
func TestTransactionWaterfallHandlerCapsInFlightPerCaller(t *testing.T) {
	setTimeAnatomyCap(t, &archiveWaterfallCallerRequests, 1)
	setTimeAnatomyCap(t, &archiveWaterfallCallerShares, newArchiveWaterfallShares())
	server, report := waterfallHandlerServer(t, waterfallFixture(0))
	id := waterfallRepresentative(t, report, "Checkout").EventID
	const proxy = "10.0.0.9:443"
	alice := &AuthenticatedUser{ID: "u-alice", Subject: "alice"}
	bob := &AuthenticatedUser{ID: "u-bob", Subject: "bob"}
	probe := serveWaterfallAs(server, id, proxy, alice)
	if probe.Code != http.StatusOK {
		t.Fatalf("status %d: %s", probe.Code, probe.Body.String())
	}
	caller := "user:u-alice"
	hold, ok := archiveWaterfallCallerShares.tryAcquire(caller, 1)
	if !ok {
		t.Fatal("caller share already held after the probe")
	}
	busy := serveWaterfallAs(server, id, proxy, alice)
	if busy.Code != http.StatusTooManyRequests || busy.Header().Get("Retry-After") == "" {
		t.Fatalf("status %d Retry-After %q, want 429 with Retry-After", busy.Code, busy.Header().Get("Retry-After"))
	}
	if other := serveWaterfallAs(server, id, proxy, bob); other.Code != http.StatusOK {
		t.Fatalf("another caller got %d", other.Code)
	}
	hold()
	if again := serveWaterfallAs(server, id, proxy, alice); again.Code != http.StatusOK {
		t.Fatalf("status %d after the share was returned", again.Code)
	}
	if held := archiveWaterfallCallerShares.count(caller); held != 0 {
		t.Fatalf("caller still holds %d shares", held)
	}
}
