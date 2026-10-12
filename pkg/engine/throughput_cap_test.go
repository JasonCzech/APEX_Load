package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// runCappedWorkers drives workers that admit through the cap and record
// txPerIteration attempts per iteration, the way VUsers record transactions,
// and returns the attempts recorded in d.
func runCappedWorkers(t *testing.T, c *GroupThroughputCap, attempts, iterations *atomic.Int64, workers, txPerIteration int, d time.Duration) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), d)
	defer cancel()
	start := attempts.Load()
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c.Admit(ctx) {
				attempts.Add(int64(txPerIteration))
				iterations.Add(1)
			}
		}()
	}
	wg.Wait()
	return attempts.Load() - start
}

func newCountedCap() (*GroupThroughputCap, *atomic.Int64, *atomic.Int64) {
	var attempts, iterations atomic.Int64
	c := newGroupThroughputCap(attempts.Load, iterations.Load)
	return c, &attempts, &iterations
}

func TestGroupThroughputCapHoldsManyFastVUsersToTarget(t *testing.T) {
	c, attempts, iterations := newCountedCap()
	c.Set(40, true)
	got := runCappedWorkers(t, c, attempts, iterations, 20, 1, 1500*time.Millisecond)
	// 40 TPS for 1.5 s is 60; twenty unthrottled workers would do thousands.
	if got < 50 || got > 66 {
		t.Fatalf("attempts in 1.5s at 40 TPS = %d, want about 60", got)
	}
}

func TestGroupThroughputCapCountsTransactionsNotIterations(t *testing.T) {
	c, attempts, iterations := newCountedCap()
	c.Set(40, true)
	got := runCappedWorkers(t, c, attempts, iterations, 8, 4, 2*time.Second)
	// Four transactions per iteration must not quadruple the rate: the cap
	// learns the ratio and the excess correction catches the first iterations.
	if got < 64 || got > 100 {
		t.Fatalf("attempts in 2s at 40 TPS with 4 tx/iteration = %d, want about 80", got)
	}
}

func TestGroupThroughputCapDisabledAdmitsAtOnce(t *testing.T) {
	c, _, _ := newCountedCap()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	for range 1000 {
		if !c.Admit(ctx) {
			t.Fatal("a disabled cap must admit without waiting")
		}
	}
	var nilCap *GroupThroughputCap
	if !nilCap.Admit(ctx) {
		t.Fatal("a nil cap must admit")
	}
}

func TestGroupThroughputCapZeroSuspendsUntilRaised(t *testing.T) {
	c, _, _ := newCountedCap()
	c.Set(0, true)
	admitted := make(chan bool, 1)
	go func() { admitted <- c.Admit(context.Background()) }()
	select {
	case <-admitted:
		t.Fatal("a zero target must hold iterations")
	case <-time.After(100 * time.Millisecond):
	}
	c.Set(100, true)
	select {
	case ok := <-admitted:
		if !ok {
			t.Fatal("raising the target must admit the waiting VUser")
		}
	case <-time.After(time.Second):
		t.Fatal("raising the target did not wake the waiting VUser")
	}
}

func TestGroupThroughputCapCancelReleasesWaiter(t *testing.T) {
	c, _, _ := newCountedCap()
	c.Set(0, true)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan bool, 1)
	go func() { done <- c.Admit(ctx) }()
	cancel()
	select {
	case ok := <-done:
		if ok {
			t.Fatal("a cancelled wait must not admit")
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not release the waiter")
	}
}

func TestGroupThroughputCapPausedEarnsNoCredit(t *testing.T) {
	c, attempts, iterations := newCountedCap()
	c.Set(20, true)
	c.SetPaused(true)
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	if c.Admit(ctx) {
		t.Fatal("a paused cap must not admit")
	}
	c.SetPaused(false)
	// After a 300 ms pause the next half second still runs at 20 TPS, with no
	// burst for the paused time.
	got := runCappedWorkers(t, c, attempts, iterations, 10, 1, 500*time.Millisecond)
	if got > 13 {
		t.Fatalf("attempts in 0.5s after resume = %d, want about 10 (no paused-time burst)", got)
	}
}

func TestGroupThroughputCapRateChangeWakesWaiters(t *testing.T) {
	c, attempts, _ := newCountedCap()
	c.Set(0.5, true)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	// Take the first slot, then queue a second VUser two seconds out.
	if !c.Admit(ctx) {
		t.Fatal("first admission failed")
	}
	attempts.Add(1)
	done := make(chan time.Duration, 1)
	go func() {
		started := time.Now()
		c.Admit(ctx)
		done <- time.Since(started)
	}()
	time.Sleep(50 * time.Millisecond)
	c.Set(100, true)
	select {
	case waited := <-done:
		if waited > 500*time.Millisecond {
			t.Fatalf("waiter took %s after the target rose to 100 TPS", waited)
		}
	case <-time.After(time.Second):
		t.Fatal("raising the target did not shorten the queued slot")
	}
}

func TestThroughputCapSpacingBounded(t *testing.T) {
	if got := throughputCapSpacing(1, 1e-12); got != maxThroughputCapSpacing {
		t.Fatalf("spacing for a vanishing target = %s, want %s", got, maxThroughputCapSpacing)
	}
	if got := throughputCapSpacing(1, 4); got != 250*time.Millisecond {
		t.Fatalf("spacing for 1 attempt at 4 TPS = %s, want 250ms", got)
	}
	if !validThroughputCapTPS(0) || validThroughputCapTPS(-1) {
		t.Fatal("zero is a valid (suspending) target and negatives are not")
	}
}

// A traffic profile moves its target every few seconds. Each move must not
// queue the waiting VUsers behind the slots they held at the old rate: the
// review's replay then delivered almost nothing for whole minutes.
func TestGroupThroughputCapFollowsFrequentTargetChanges(t *testing.T) {
	c, attempts, iterations := newCountedCap()
	c.Set(10, true)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for c.Admit(ctx) {
				attempts.Add(1)
				iterations.Add(1)
			}
		}()
	}
	// Step 10 → 15 → 25 → 40 → 50 TPS every 400 ms.
	for _, tps := range []float64{15, 25, 40, 50} {
		time.Sleep(400 * time.Millisecond)
		c.Set(tps, true)
	}
	wg.Wait()
	// Expected about 0.4×(10+15+25+40) + 0.4×50 = 56.
	if got := attempts.Load(); got < 44 || got > 68 {
		t.Fatalf("attempts across rising targets = %d, want about 56", got)
	}
}
