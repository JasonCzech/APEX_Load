package api

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// blockingReload returns a reload func that parks its first invocation until the
// returned release func is called, plus a channel signalling that entry.
func blockingReload(calls *atomic.Int32) (func() error, chan struct{}, func()) {
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	reload := func() error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return nil
	}
	return reload, entered, func() { once.Do(func() { close(release) }) }
}

// awaitRefreshParked blocks until want callers have parked on the in-flight
// reload of c, and fails the test if they never do.
//
// Every burst test needs this barrier, and it has to count parked callers
// rather than arrivals, because both directions are otherwise races:
//
//   - Releasing the blocked reload before the rest of the burst has committed
//     makes a CORRECT coalescer fail: a caller whose arrival timestamp lands
//     after the reload finished is entitled to start a second reload.
//   - Releasing early also lets a BROKEN coalescer pass: with in-flight sharing
//     removed, callers that reach the lock after the reload completed are still
//     suppressed by the staleness window (throttled paths) or by the
//     finished-at-or-after-arrival rule (forced paths), so "one reload" holds
//     for the wrong reason. Measured on the throttled burst, ~76% of callers
//     exited that way and ~1 run in 4 exercised no coalescing at all.
//
// Only the in-flight branch of refresh advances the counter, so a coalescer
// that stopped sharing reloads can never satisfy this barrier: it fails here
// with a clear message instead of passing by luck.
func awaitRefreshParked(t *testing.T, c *haRefreshCoalescer, want int) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for c.parked.Load() < int64(want) {
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d callers parked on the in-flight reload; the burst was not coalesced", c.parked.Load(), want)
		}
		time.Sleep(time.Millisecond)
	}
}

// TestHARefreshCoalescerThrottledBurstCollapsesToOneReload pins the core
// contract every HA read-refresh path now shares: a burst of concurrent callers
// performs one durable reload, not one per caller.
func TestHARefreshCoalescerThrottledBurstCollapsesToOneReload(t *testing.T) {
	var coalescer haRefreshCoalescer
	var calls atomic.Int32
	reload, entered, release := blockingReload(&calls)

	const callers = 25
	var group sync.WaitGroup
	// Unwinds in reverse: release the blocked reload first, then drain the burst,
	// so a failed barrier below cannot leave goroutines logging after the test
	// has finished.
	defer group.Wait()
	defer release()
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			if err := coalescer.refresh(time.Minute, reload); err != nil {
				t.Errorf("refresh: %v", err)
			}
		}()
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no caller entered the reload")
	}
	// Hold the reload until every other caller has parked on it, so the burst is
	// collapsed by coalescing rather than by the staleness window. Without this
	// barrier most callers reach the lock only after the reload has already
	// finished and are suppressed by the window, so the assertion below would
	// hold even with in-flight sharing removed.
	awaitRefreshParked(t, &coalescer, callers-1)
	release()
	group.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("reload calls = %d, want 1 coalesced reload", got)
	}
}

// TestHARefreshCoalescerForcedBurstSharesOneReload covers the zero-window
// (forced) path: it has no staleness window to hide behind, so coalescing alone
// has to collapse callers that arrived before the shared reload finished.
func TestHARefreshCoalescerForcedBurstSharesOneReload(t *testing.T) {
	var coalescer haRefreshCoalescer
	var calls atomic.Int32
	reload, entered, release := blockingReload(&calls)

	const callers = 25
	var group sync.WaitGroup
	// See the throttled burst above: release before draining on the failure path.
	defer group.Wait()
	defer release()
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			if err := coalescer.refresh(0, reload); err != nil {
				t.Errorf("refresh: %v", err)
			}
		}()
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no caller entered the reload")
	}
	// Hold the reload until every other caller has provably parked on it.
	awaitRefreshParked(t, &coalescer, callers-1)
	release()
	group.Wait()
	if got := calls.Load(); got != 1 {
		t.Fatalf("reload calls = %d, want 1 shared reload", got)
	}
}

// TestHARefreshCoalescerSequentialForcedReadReloads is the other half of the
// forced contract: a caller arriving after the previous reload finished must
// issue a new query so it can observe a cross-replica mutation immediately.
func TestHARefreshCoalescerSequentialForcedReadReloads(t *testing.T) {
	var coalescer haRefreshCoalescer
	var calls atomic.Int32
	reload := func() error {
		calls.Add(1)
		return nil
	}
	for attempt := 1; attempt <= 3; attempt++ {
		if err := coalescer.refresh(0, reload); err != nil {
			t.Fatalf("refresh %d: %v", attempt, err)
		}
		if got := calls.Load(); got != int32(attempt) {
			t.Fatalf("reload calls after %d sequential forced reads = %d", attempt, got)
		}
	}
}

func TestHARefreshCoalescerStalenessWindowSuppressesReload(t *testing.T) {
	var coalescer haRefreshCoalescer
	var calls atomic.Int32
	reload := func() error {
		calls.Add(1)
		return nil
	}
	if err := coalescer.refresh(time.Minute, reload); err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if err := coalescer.refresh(time.Minute, reload); err != nil {
		t.Fatalf("second refresh: %v", err)
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("reload calls inside the staleness window = %d, want 1", got)
	}
	// A forced (zero-window) caller ignores the window.
	if err := coalescer.refresh(0, reload); err != nil {
		t.Fatalf("forced refresh: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("reload calls after a forced refresh = %d, want 2", got)
	}
	coalescer.expire()
	if err := coalescer.refresh(time.Minute, reload); err != nil {
		t.Fatalf("refresh after expire: %v", err)
	}
	if got := calls.Load(); got != 3 {
		t.Fatalf("reload calls after expire = %d, want 3", got)
	}
}

// TestHARefreshCoalescerFailureDoesNotOpenStalenessWindow keeps the pre-existing
// semantics of the throttled refresh paths: a failed reload never records
// freshness, so the next read retries instead of serving stale state for the
// whole window.
func TestHARefreshCoalescerFailureDoesNotOpenStalenessWindow(t *testing.T) {
	var coalescer haRefreshCoalescer
	var calls atomic.Int32
	boom := errors.New("store unavailable")
	reload := func() error {
		calls.Add(1)
		return boom
	}
	if err := coalescer.refresh(time.Minute, reload); !errors.Is(err, boom) {
		t.Fatalf("first refresh error = %v, want %v", err, boom)
	}
	if err := coalescer.refresh(time.Minute, reload); !errors.Is(err, boom) {
		t.Fatalf("second refresh error = %v, want %v", err, boom)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("reload calls after failure = %d, want 2 (no window opened)", got)
	}
	coalescer.markFresh(time.Now())
	if err := coalescer.refresh(time.Minute, reload); err != nil {
		t.Fatalf("refresh inside a marked window = %v, want nil", err)
	}
	if got := calls.Load(); got != 2 {
		t.Fatalf("reload calls inside a marked window = %d, want 2", got)
	}
}

// TestHARefreshCoalescerFailureIsSharedByTheBurst proves a failing reload still
// terminates the burst it coalesced instead of letting every parked caller
// stampede the broken store.
func TestHARefreshCoalescerFailureIsSharedByTheBurst(t *testing.T) {
	var coalescer haRefreshCoalescer
	var calls atomic.Int32
	boom := errors.New("store unavailable")
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseReload := func() { releaseOnce.Do(func() { close(release) }) }
	defer releaseReload()
	reload := func() error {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
		}
		return boom
	}
	const callers = 10
	errs := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			errs <- coalescer.refresh(0, reload)
		}()
	}
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("no caller entered the reload")
	}
	awaitRefreshParked(t, &coalescer, callers-1)
	releaseReload()
	group.Wait()
	close(errs)
	if got := calls.Load(); got != 1 {
		t.Fatalf("reload calls = %d, want 1 shared failing reload", got)
	}
	for err := range errs {
		if !errors.Is(err, boom) {
			t.Fatalf("burst caller error = %v, want %v", err, boom)
		}
	}
}

// TestHARefreshCoalescerPanicDoesNotStrandInFlightMarker is the regression test
// for the hand-rolled singleflight this helper replaced: it published and
// cleared its in-flight channel inline, so a panic inside the reload left the
// channel non-nil and never closed, permanently deadlocking every later caller
// of the affected read path.
func TestHARefreshCoalescerPanicDoesNotStrandInFlightMarker(t *testing.T) {
	var coalescer haRefreshCoalescer
	func() {
		defer func() {
			if recovered := recover(); recovered == nil {
				t.Error("reload panic was not propagated to the caller")
			}
		}()
		_ = coalescer.refresh(0, func() error {
			panic("reload exploded")
		})
	}()

	coalescer.mu.Lock()
	inflight := coalescer.inflight
	recordedErr := coalescer.lastErr
	freshAt := coalescer.freshAt
	coalescer.mu.Unlock()
	if inflight != nil {
		t.Fatal("in-flight marker survived a panicking reload")
	}
	if !errors.Is(recordedErr, errHARefreshAborted) {
		t.Fatalf("recorded outcome = %v, want %v", recordedErr, errHARefreshAborted)
	}
	if !freshAt.IsZero() {
		t.Fatal("a panicking reload opened a staleness window")
	}

	done := make(chan error, 1)
	go func() {
		done <- coalescer.refresh(0, func() error { return nil })
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("refresh after a panicking reload = %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("refresh after a panicking reload blocked forever on a stranded in-flight marker")
	}
}
