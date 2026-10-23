package engine

import (
	"sync"
	"testing"
	"time"
)

// fakeCollectionTicks hands out a channel that tick closes, as
// MetricsCollector.CollectionTicked does.
type fakeCollectionTicks struct {
	mu sync.Mutex
	ch chan struct{}
}

func (f *fakeCollectionTicks) ticked() <-chan struct{} {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ch == nil {
		f.ch = make(chan struct{})
	}
	return f.ch
}

func (f *fakeCollectionTicks) tick() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ch != nil {
		close(f.ch)
		f.ch = nil
	}
}

func runPublishOnCollection(t *testing.T, interval time.Duration, ticks *fakeCollectionTicks) (chan time.Time, func()) {
	t.Helper()
	published := make(chan time.Time, 64)
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		PublishOnCollection(stop, interval, ticks.ticked, func() { published <- time.Now() })
	}()
	return published, func() { close(stop); <-done }
}

func waitPublication(t *testing.T, published <-chan time.Time, within time.Duration) time.Time {
	t.Helper()
	select {
	case at := <-published:
		return at
	case <-time.After(within):
		t.Fatalf("no publication within %s", within)
	}
	return time.Time{}
}

func noPublication(t *testing.T, published <-chan time.Time, within time.Duration) {
	t.Helper()
	select {
	case <-published:
		t.Fatal("unexpected publication")
	case <-time.After(within):
	}
}

// Each collection tick publishes at once; a tick arriving well before the
// cadence is due is skipped, so a cadence longer than the collection interval
// publishes on every n-th collection.
func TestPublishOnCollectionFollowsTicksAtTheCadence(t *testing.T) {
	ticks := &fakeCollectionTicks{}
	published, stop := runPublishOnCollection(t, 400*time.Millisecond, ticks)
	defer stop()
	time.Sleep(20 * time.Millisecond)
	ticks.tick()
	first := waitPublication(t, published, 200*time.Millisecond)
	// Too soon after the first publication: skipped.
	time.Sleep(100 * time.Millisecond)
	ticks.tick()
	noPublication(t, published, 100*time.Millisecond)
	// Due (within the tolerance of the cadence): published on the tick.
	time.Sleep(time.Until(first.Add(330 * time.Millisecond)))
	ticks.tick()
	if second := waitPublication(t, published, 200*time.Millisecond); second.Sub(first) < 300*time.Millisecond {
		t.Fatalf("second publication %s after the first", second.Sub(first))
	}
}

// Without collection ticks, a watchdog publishes every interval and a half.
func TestPublishOnCollectionWatchdogWithoutTicks(t *testing.T) {
	ticks := &fakeCollectionTicks{}
	published, stop := runPublishOnCollection(t, 100*time.Millisecond, ticks)
	defer stop()
	start := time.Now()
	first := waitPublication(t, published, time.Second)
	if elapsed := first.Sub(start); elapsed < 140*time.Millisecond {
		t.Fatalf("watchdog published after %s, before an interval and a half", elapsed)
	}
	waitPublication(t, published, time.Second)
}

// Stop ends the loop with no further publication.
func TestPublishOnCollectionStops(t *testing.T) {
	ticks := &fakeCollectionTicks{}
	published, stop := runPublishOnCollection(t, 50*time.Millisecond, ticks)
	stop()
	ticks.tick()
	noPublication(t, published, 150*time.Millisecond)
}

// A publisher started half a collection interval after its collector reads
// each snapshot right after it is collected: the published age measures the
// work between collection and publication, not the publisher's phase.
func TestPublishOnCollectionPublishesFreshSnapshots(t *testing.T) {
	const interval = 400 * time.Millisecond
	collector := NewMetricsCollector(16)
	collector.EnableIdleSnapshots(true)
	collector.Start(interval)
	defer collector.Stop()
	time.Sleep(interval / 2)
	var mu sync.Mutex
	var ages []time.Duration
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		PublishOnCollection(stop, interval, collector.CollectionTicked, func() {
			snapshot, ok := collector.LatestSnapshot()
			if !ok {
				return
			}
			mu.Lock()
			ages = append(ages, time.Since(snapshot.Timestamp))
			mu.Unlock()
		})
	}()
	time.Sleep(5*interval + interval/4)
	close(stop)
	<-done
	mu.Lock()
	defer mu.Unlock()
	if len(ages) < 4 {
		t.Fatalf("published %d times, want at least 4", len(ages))
	}
	for _, age := range ages {
		// A publisher on its own ticker would read snapshots about half an
		// interval (200 ms) old here.
		if age > interval/4 {
			t.Fatalf("published snapshot ages %v: want each within %s of its collection", ages, interval/4)
		}
	}
}

// CollectionTicked wakes every waiter once the tick's snapshot is the latest.
func TestCollectionTickedWakesEveryWaiterAfterTheSnapshot(t *testing.T) {
	collector := NewMetricsCollector(16)
	collector.EnableIdleSnapshots(true)
	first, second := collector.CollectionTicked(), collector.CollectionTicked()
	if first != second {
		t.Fatal("waiters for the same tick got different channels")
	}
	started := time.Now()
	collector.Start(50 * time.Millisecond)
	defer collector.Stop()
	for _, ch := range []<-chan struct{}{first, second} {
		select {
		case <-ch:
		case <-time.After(time.Second):
			t.Fatal("a waiter was not woken by the tick")
		}
	}
	snapshot, ok := collector.LatestSnapshot()
	if !ok || snapshot.Timestamp.Before(started) {
		t.Fatalf("the tick woke waiters before its snapshot was stored: %v %v", ok, snapshot.Timestamp)
	}
	if next := collector.CollectionTicked(); next == first {
		t.Fatal("a closed tick channel was handed out again")
	}
}
