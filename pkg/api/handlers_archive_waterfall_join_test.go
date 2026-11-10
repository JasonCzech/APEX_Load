package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// waterfallPersistIDs gives every row a persisted event_id, so the report
// accumulator fed directly and the archived report agree on ids.
func waterfallPersistIDs(events []TransactionEvent) []TransactionEvent {
	for index := range events {
		events[index].EventID = fmt.Sprintf("tx_%032x", index+1)
	}
	return events
}

// waterfallAccumulatorFor feeds events to a fresh accumulator at their line
// ordinals, exactly as the report's pass does.
func waterfallAccumulatorFor(events []TransactionEvent) *archiveTimeAnatomyAccumulator {
	feeder := newTAFeeder()
	feeder.feed(slices.Clone(events)...)
	return feeder.acc
}

// waterfallInstances returns the accumulator's sampled instances of name.
func waterfallInstances(t *testing.T, acc *archiveTimeAnatomyAccumulator, name string) []timeAnatomyInstance {
	t.Helper()
	entry := acc.names[name]
	if entry == nil || len(entry.sample) == 0 {
		t.Fatalf("no sampled %s instances", name)
	}
	return slices.Clone(entry.sample)
}

type waterfallHintCase struct {
	name  string
	hints func(timeAnatomyInstance) archiveWaterfallHints
}

var waterfallHintCases = []waterfallHintCase{
	{"no hints", func(timeAnatomyInstance) archiveWaterfallHints { return archiveWaterfallHints{} }},
	{"ordinal only", func(i timeAnatomyInstance) archiveWaterfallHints {
		return archiveWaterfallHints{ordinal: i.ordinal, ok: true}
	}},
	// A stale hint naming another row, and one past the archive's end: the
	// locate step falls back to the id scan, and the join must not change.
	{"wrong ordinal", func(i timeAnatomyInstance) archiveWaterfallHints {
		if i.ordinal > 0 {
			return archiveWaterfallHints{ordinal: i.ordinal - 1, ok: true}
		}
		return archiveWaterfallHints{ordinal: i.ordinal + 1, ok: true}
	}},
	{"out-of-range ordinal", func(timeAnatomyInstance) archiveWaterfallHints {
		return archiveWaterfallHints{ordinal: 1 << 40, ok: true}
	}},
}

// assertWaterfallIsReportInstance checks that the drill-down rebuilds the
// report's instance: the same claimed requests, the same partial verdict
// (a partial one has reasons and no decomposition) and, for a joined
// instance, the same decomposition bit for bit.
func assertWaterfallIsReportInstance(t *testing.T, record *storage.RunArchiveRecord, instance timeAnatomyInstance, wantPartial bool) *archiveWaterfallResult {
	t.Helper()
	var first *archiveWaterfallResult
	for _, hintCase := range waterfallHintCases {
		waterfall := buildWaterfallForTest(t, record, instance.eventID, hintCase.hints(instance), 500)
		if waterfall.RequestsTotal != int(instance.children) {
			t.Fatalf("%s %s (%s): requests_total %d, report claimed %d", instance.eventID, hintCase.name, waterfallNames(waterfall), waterfall.RequestsTotal, instance.children)
		}
		if waterfall.Partial != wantPartial {
			t.Fatalf("%s %s: partial %v reasons %v, want %v", instance.eventID, hintCase.name, waterfall.Partial, waterfall.PartialReasons, wantPartial)
		}
		if wantPartial && (len(waterfall.PartialReasons) == 0 || waterfall.Decomposition != nil) {
			t.Fatalf("%s %s: partial reasons %v, decomposition %+v (a partial instance has none)", instance.eventID, hintCase.name, waterfall.PartialReasons, waterfall.Decomposition)
		}
		if instance.joined {
			want := timeAnatomyInstanceVector{Phases: instance.phases, NonRequest: instance.nonRequest, Overlap: instance.overlap, Children: int(instance.children)}.decomposition(timeAnatomyPos(instance.durationMs))
			if waterfall.Decomposition == nil || !reflect.DeepEqual(*waterfall.Decomposition, want) {
				t.Fatalf("%s %s: decomposition %+v, report instance %+v", instance.eventID, hintCase.name, waterfall.Decomposition, want)
			}
		}
		if first == nil {
			first = waterfall
		} else if !reflect.DeepEqual(first, waterfall) {
			t.Fatalf("%s: hints changed the result:\n%+v\n%+v", instance.eventID, first, waterfall)
		}
	}
	return first
}

func waterfallNames(waterfall *archiveWaterfallResult) []string {
	var names []string
	for _, row := range waterfall.Requests {
		names = append(names, row.ActionName)
	}
	return names
}

// An open workload under a lowered global cap and identity cap: the report
// evicts ended VUser lifetimes, which costs no instance a child, so every
// drill-down equals its report instance and none is partial.
func TestTransactionWaterfallMatchesReportOpenWorkload(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingTotal, 32)
	setTimeAnatomyCap(t, &maxTimeAnatomyIdentities, 8)
	const arrivals, requests = 60, 4
	var events []TransactionEvent
	for pair := 0; pair < arrivals; pair += 2 {
		var rows [2][]TransactionEvent
		var txs []TransactionEvent
		for slot, arrival := range []int{pair, pair + 1} {
			vu, life := 1+arrival%7, uint64(arrival+1)*2654435761
			start := float64(arrival) * 20
			for step := 0; step < requests; step++ {
				row := taRequest(vu, life, 1, start+float64(step*10), 8, fmt.Sprintf("R%d", step), step+1)
				row.DNSMs, row.TTFBMs = 1, 6
				rows[slot] = append(rows[slot], row)
			}
			txs = append(txs, taTransaction(vu, life, 1, start, float64(requests*10), "Arrival"))
		}
		for step := 0; step < requests; step++ {
			events = append(events, rows[0][step], rows[1][step])
		}
		events = append(events, txs...)
	}
	events = waterfallPersistIDs(events)
	acc := waterfallAccumulatorFor(events)
	// Every evicted request here was already claimed, so it counts in
	// evictedClaimed rather than PendingRequestsEvicted.
	if acc.truncation.PendingRequestsEvicted+acc.evictedClaimed == 0 || acc.truncation.IdentitiesEvicted == 0 {
		t.Fatalf("fixture did not exercise eviction: %+v", acc.truncation)
	}
	record, _ := waterfallRecordForTest(t, events)
	instances := waterfallInstances(t, acc, "Arrival")
	if len(instances) != arrivals {
		t.Fatalf("sampled %d of %d arrivals", len(instances), arrivals)
	}
	for _, instance := range instances {
		if !instance.joined || instance.children != requests {
			t.Fatalf("report instance %s joined %v children %d", instance.eventID, instance.joined, instance.children)
		}
		assertWaterfallIsReportInstance(t, record, instance, false)
	}
}

// The per-identity cap made the report drop a request: the drill-down
// replays the same cap rule, claims what the report claimed and says the
// instance is partial. The later instance past the drop stays joined.
func TestTransactionWaterfallMatchesReportUnderKeyCap(t *testing.T) {
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, 2)
	for _, test := range []struct {
		name      string
		events    []TransactionEvent
		partial   []string
		joined    []string
		firstName string
	}{
		{
			name: "in order",
			events: []TransactionEvent{
				taRequest(1, 11, 1, 0, 10, "A", 1),
				taRequest(1, 11, 1, 10, 10, "B", 2),
				taRequest(1, 11, 1, 20, 10, "C", 3),
				taTransaction(1, 11, 1, 0, 30, "T"),
				taRequest(1, 11, 1, 100, 10, "D", 4),
				taTransaction(1, 11, 1, 100, 10, "T"),
			},
			partial: []string{"B", "C"},
			joined:  []string{"D"},
		},
		{
			// A finishes last but started first: a full buffer keeps the
			// latest-starting requests, so A itself is the one dropped.
			name: "earliest start written last",
			events: []TransactionEvent{
				taRequest(1, 11, 1, 10, 10, "B", 2),
				taRequest(1, 11, 1, 20, 10, "C", 3),
				taRequest(1, 11, 1, 0, 40, "A", 1),
				taTransaction(1, 11, 1, 0, 40, "T"),
				taRequest(1, 11, 1, 100, 10, "D", 4),
				taTransaction(1, 11, 1, 100, 10, "T"),
			},
			partial: []string{"B", "C"},
			joined:  []string{"D"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			events := waterfallPersistIDs(test.events)
			acc := waterfallAccumulatorFor(events)
			record, _ := waterfallRecordForTest(t, events)
			var sawPartial, sawJoined bool
			for _, instance := range waterfallInstances(t, acc, "T") {
				partial := !instance.joined
				waterfall := assertWaterfallIsReportInstance(t, record, instance, partial)
				if partial && !reflect.DeepEqual(waterfall.PartialReasons, []string{timeAnatomyReasonPendingKeyCap}) {
					t.Fatalf("%s: partial reasons %v", instance.eventID, waterfall.PartialReasons)
				}
				want := test.joined
				if partial {
					want, sawPartial = test.partial, true
				} else {
					sawJoined = true
				}
				if names := waterfallNames(waterfall); !reflect.DeepEqual(names, want) {
					t.Fatalf("%s claimed %v, report claimed %v", instance.eventID, names, want)
				}
			}
			if !sawPartial || !sawJoined {
				t.Fatalf("fixture lacks a partial (%v) or joined (%v) instance", sawPartial, sawJoined)
			}
		})
	}
}

// A long transaction around many requests: the report keeps only the
// latest maxTimeAnatomyPendingPerVUser of them and marks the instance
// partial. The replay holds no more decoded rows than that cap.
func TestTransactionWaterfallLongWindowHoldsOnlyBufferedRequests(t *testing.T) {
	const limit, requests = 8, 300
	setTimeAnatomyCap(t, &maxTimeAnatomyPendingPerVUser, limit)
	var events []TransactionEvent
	for step := 0; step < requests; step++ {
		events = append(events, taRequest(1, 21, 1, float64(step*10), 10, fmt.Sprintf("R%03d", step), step+1))
	}
	events = append(events, taTransaction(1, 21, 1, 0, requests*10, "Session"))
	events = waterfallPersistIDs(events)
	acc := waterfallAccumulatorFor(events)
	record, _ := waterfallRecordForTest(t, events)
	instances := waterfallInstances(t, acc, "Session")
	waterfall := assertWaterfallIsReportInstance(t, record, instances[0], true)
	if waterfall.RequestsTotal != limit || waterfall.Requests[0].ActionName != fmt.Sprintf("R%03d", requests-limit) {
		t.Fatalf("claimed %v", waterfallNames(waterfall))
	}

	replay := newArchiveWaterfallReplay(timeAnatomyClaimWindow{StartNs: taAt(0).UnixNano(), EndNs: taAt(requests * 10).UnixNano()})
	for index := range events[:requests] {
		startNs, _, _ := timeAnatomyWindow(events[index].Timestamp, events[index].DurationMs, 0)
		replay.offer(int64(index), &events[index], startNs)
		if len(replay.payloads) > limit || replay.pending.len() > limit {
			t.Fatalf("after %d requests the replay holds %d rows (%d buffered), cap %d", index+1, len(replay.payloads), replay.pending.len(), limit)
		}
	}
}

// A live identity the report evicted under the global cap cannot be replayed
// from one identity's rows: the drill-down claims the requests the report
// lost, and requests_total exceeding the representative's children is what
// tells the drawer that the report's instance is incomplete.
func TestTransactionWaterfallGlobalEvictionShowsInChildren(t *testing.T) {
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
	events = waterfallPersistIDs(events)
	record, report := waterfallRecordForTest(t, events)
	outer := waterfallRepresentative(t, report, "Outer")
	waterfall := buildWaterfallForTest(t, record, outer.EventID, archiveWaterfallHints{ordinal: outer.Ordinal, ok: true}, 500)
	if outer.Children != 0 || waterfall.RequestsTotal != 3 || waterfall.RequestsTotal == outer.Children {
		t.Fatalf("report children %d, drill-down requests_total %d", outer.Children, waterfall.RequestsTotal)
	}
	if entry := taEntry(t, report.TimeAnatomy, "Outer"); entry.PartialInstances != 1 {
		t.Fatalf("report did not mark Outer partial: %+v", entry)
	}
}

// first_ordinal never bounds the join: missing, too late, past the first
// child, or further from ordinal than any span limit, the drill-down still
// claims every child.
func TestTransactionWaterfallHandlerIgnoresFirstOrdinalForTheJoin(t *testing.T) {
	const noise = 50
	server, report := waterfallHandlerServer(t, waterfallFixture(noise))
	representative := waterfallRepresentative(t, report, "Checkout")
	// Rows: A at noise, X (another VUser) at noise+1, B at noise+2.
	for _, query := range []string{
		fmt.Sprintf("ordinal=%d", representative.Ordinal),
		fmt.Sprintf("ordinal=%d&first_ordinal=%d", representative.Ordinal, representative.Ordinal),
		fmt.Sprintf("ordinal=%d&first_ordinal=%d", representative.Ordinal, noise+1),
		fmt.Sprintf("ordinal=%d&first_ordinal=%d", representative.Ordinal, noise+2),
		"ordinal=2000001&first_ordinal=0",
	} {
		rec := serveWaterfallForTest(server, representative.EventID, query)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", query, rec.Code, rec.Body.String())
		}
		var waterfall archiveWaterfallResult
		if err := json.Unmarshal(rec.Body.Bytes(), &waterfall); err != nil {
			t.Fatal(err)
		}
		if names := waterfallNames(&waterfall); !reflect.DeepEqual(names, []string{"A", "B", "C"}) || waterfall.RequestsTotal != 3 {
			t.Fatalf("%s: claimed %v (total %d)", query, names, waterfall.RequestsTotal)
		}
	}
}

// archiveWaterfallWaiters is how many callers wait on the in-flight load of
// key (-1: none in flight).
func archiveWaterfallWaiters(cache *boundedArchiveCache[archiveWaterfallResult], key string) int {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if call := cache.inFlight[key]; call != nil {
		return call.waiters
	}
	return -1
}

// waitForWaterfallWaiters polls until the in-flight load of key has want
// waiters.
func waitForWaterfallWaiters(t *testing.T, cache *boundedArchiveCache[archiveWaterfallResult], key string, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for archiveWaterfallWaiters(cache, key) != want {
		if time.Now().After(deadline) {
			t.Fatalf("in-flight load has %d waiters, want %d", archiveWaterfallWaiters(cache, key), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// countWaterfallLoads counts the drill-down loads that start reading.
func countWaterfallLoads(t *testing.T) *atomic.Int32 {
	t.Helper()
	var loads atomic.Int32
	previous := archiveWaterfallLoadHook
	archiveWaterfallLoadHook = func(context.Context) { loads.Add(1) }
	t.Cleanup(func() { archiveWaterfallLoadHook = previous })
	return &loads
}

// A caller that gives up stops waiting at once, and the load it started
// keeps running for the callers still waiting: one load serves both.
func TestTransactionWaterfallSharedLoadSurvivesCanceledCaller(t *testing.T) {
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	representative := waterfallRepresentative(t, report, "Checkout")
	cache := newArchiveWaterfallCache()
	key := archiveWaterfallCacheKey(record, representative.EventID)
	loads := countWaterfallLoads(t)
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	previous := archiveWaterfallDecodeHook
	archiveWaterfallDecodeHook = func(int64) {
		once.Do(func() {
			close(started)
			<-release
		})
	}
	var releaseOnce sync.Once
	releaseLoad := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(func() {
		releaseLoad()
		archiveWaterfallDecodeHook = previous
	})
	load := func(ctx context.Context) (*archiveWaterfallResult, error) {
		return cachedArchiveTransactionWaterfall(ctx, cache, record, representative.EventID, archiveWaterfallHints{}, 500)
	}

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	leader := make(chan error, 1)
	go func() {
		_, err := load(leaderCtx)
		leader <- err
	}()
	<-started
	type outcome struct {
		waterfall *archiveWaterfallResult
		err       error
	}
	waiter := make(chan outcome, 1)
	go func() {
		waterfall, err := load(context.Background())
		waiter <- outcome{waterfall, err}
	}()
	// The waiter has joined the in-flight load before the leader leaves.
	waitForWaterfallWaiters(t, cache, key, 2)
	cancelLeader()
	select {
	case err := <-leader:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled caller error = %v", err)
		}
	case <-time.After(5 * time.Second):
		releaseLoad()
		t.Fatal("a canceled caller kept waiting for the shared load")
	}
	waitForWaterfallWaiters(t, cache, key, 1)
	releaseLoad()
	select {
	case got := <-waiter:
		if got.err != nil || got.waterfall == nil || got.waterfall.RequestsTotal != 3 {
			t.Fatalf("waiter = %+v, %v", got.waterfall, got.err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("waiter never completed")
	}
	if got := loads.Load(); got != 1 {
		t.Fatalf("%d loads ran, want the one shared load", got)
	}
}

// Deleting or pruning a run forgets its drill-downs; another controller's
// caches are separate.
func TestTransactionWaterfallCacheForgottenWithRun(t *testing.T) {
	record, report := waterfallRecordForTest(t, waterfallFixture(0))
	record.TestID = "test-query"
	representative := waterfallRepresentative(t, report, "Checkout")
	caches, other := newArchiveReadCaches(), newArchiveReadCaches()
	load := func(cache *boundedArchiveCache[archiveWaterfallResult]) int {
		return len(countWaterfallDecodes(t, func() {
			if _, err := cachedArchiveTransactionWaterfall(context.Background(), cache, record, representative.EventID, archiveWaterfallHints{}, 500); err != nil {
				t.Fatal(err)
			}
		}))
	}
	if load(caches.waterfalls) == 0 {
		t.Fatal("first load decoded nothing")
	}
	if decodes := load(caches.waterfalls); decodes != 0 {
		t.Fatalf("repeat load decoded %d rows, want a cache hit", decodes)
	}
	if load(other.waterfalls) == 0 {
		t.Fatal("another controller's cache served this controller's entry")
	}
	caches.forgetRunArchiveCache(record.TestID)
	if load(caches.waterfalls) == 0 {
		t.Fatal("forgetRunArchiveCache kept the run's drill-down")
	}
}
