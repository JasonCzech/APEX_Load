package api

import (
	"errors"
	"sync"
	"sync/atomic"
	"time"
)

// errHARefreshAborted is recorded when a coalesced reload unwinds without
// returning (a panic). Callers sharing that reload learn it produced no
// evidence instead of silently treating it as a successful refresh.
var errHARefreshAborted = errors.New("HA read refresh aborted before completion")

// haRefreshCoalescer is the shared request-coalescing gate for HA read
// refreshes. A follower read has to reconcile process-local state against the
// shared database, but a burst of dashboard polls used to queue one complete
// durable scan per request even though the first scan had already made the rest
// redundant. Every refresh path routes through this type so they all get the
// same guarantees instead of each reinventing them:
//
//   - Concurrent callers share one in-flight reload rather than running their own.
//   - A caller only accepts a reload that FINISHED at or after the caller
//     arrived, so a later sequential read always issues a new query and can
//     immediately observe a mutation committed by another replica.
//   - An optional staleness window lets throttled (non-forced) read paths reuse
//     a recent successful refresh without touching the database at all. Only a
//     successful reload opens that window, so a failing store is retried by the
//     next read instead of serving stale state for the whole window.
//
// The in-flight marker is published from a deferred function, so a panic inside
// a reload can never strand it and permanently block every later caller.
//
// The zero value is ready to use.
type haRefreshCoalescer struct {
	mu sync.Mutex
	// inflight is closed when the current reload finishes; nil while idle.
	inflight chan struct{}
	// arrivals assigns each caller a process-local order before it contends for
	// mu. completedThrough is the greatest arrival included in the most recently
	// completed reload. Together they distinguish a caller that was concurrent
	// with that reload from a later sequential caller without relying on wall-clock
	// timestamps, whose resolution can make those two events appear equal.
	arrivals         atomic.Uint64
	completedThrough uint64
	// freshAt is when the most recent successful reload finished, and anchors
	// the staleness window.
	freshAt time.Time
	// lastErr is the outcome of the most recently completed reload.
	lastErr error
	// parked counts callers that have committed to sharing an in-flight reload
	// instead of running their own. It carries no behaviour; it exists so a burst
	// test can hold the reload open until the rest of the burst has provably
	// parked on it.
	//
	// Counting parked callers rather than arrivals is what makes such a test an
	// assertion instead of a race, in both directions. A counted caller has
	// already stamped its arrival timestamp and released the lock without
	// starting a reload, so releasing the reload afterwards cannot make a correct
	// coalescer perform a second one (no false failure). And the count can only
	// advance through the in-flight branch below, so a coalescer that stopped
	// sharing in-flight reloads can never satisfy the barrier — the burst would
	// otherwise collapse to one reload anyway via the staleness window or the
	// finished-at-or-after-arrival rule, and the test would pass for the wrong
	// reason (no false pass).
	parked atomic.Int64
}

// refresh runs reload at most once per concurrent burst. A window greater than
// zero additionally suppresses the reload entirely when a successful one
// finished less than window ago; pass zero for reads that must not be served
// out of that staleness window.
//
// Zero does not promise the caller a query of its own. A caller that parks on
// an in-flight reload accepts that reload under the finished-at-or-after-arrival
// rule above, and that reload may have queried the store before this caller
// arrived. So zero bounds staleness to one in-flight reload — a *sequential*
// caller always queries again and sees a mutation another replica committed
// before it arrived, while a concurrent one may not. A read that needs a
// snapshot strictly newer than its own arrival has to query the store directly
// instead of going through the coalescer.
func (c *haRefreshCoalescer) refresh(window time.Duration, reload func() error) error {
	if c == nil || reload == nil {
		return nil
	}
	arrival := c.arrivals.Add(1)
	arrivedAt := time.Now()
	for {
		c.mu.Lock()
		if window > 0 && !c.freshAt.IsZero() && arrivedAt.Sub(c.freshAt) < window {
			c.mu.Unlock()
			return nil
		}
		if c.completedThrough >= arrival {
			// A reload finished after this caller arrived, so its result is
			// evidence this caller can use.
			err := c.lastErr
			c.mu.Unlock()
			return err
		}
		if c.inflight != nil {
			wait := c.inflight
			// Counted under the lock, so a counted caller has provably given up
			// its own reload for this round. See the parked field comment.
			c.parked.Add(1)
			c.mu.Unlock()
			<-wait
			// Re-evaluate against the checks above rather than assuming the
			// reload that just finished satisfies this caller.
			continue
		}
		done := make(chan struct{})
		c.inflight = done
		c.mu.Unlock()
		return c.runOnce(done, reload)
	}
}

func (c *haRefreshCoalescer) runOnce(done chan struct{}, reload func() error) (err error) {
	completed := false
	defer func() {
		c.mu.Lock()
		c.inflight = nil
		c.completedThrough = c.arrivals.Load()
		if completed {
			c.lastErr = err
			if err == nil {
				c.freshAt = time.Now()
			}
		} else {
			c.lastErr = errHARefreshAborted
		}
		c.mu.Unlock()
		close(done)
	}()
	err = reload()
	completed = true
	return err
}

// markFresh anchors the staleness window at `at` without performing a reload.
func (c *haRefreshCoalescer) markFresh(at time.Time) {
	c.mu.Lock()
	c.freshAt = at
	c.mu.Unlock()
}

// expire drops the recorded refresh evidence so the next caller reloads.
func (c *haRefreshCoalescer) expire() {
	c.mu.Lock()
	c.freshAt = time.Time{}
	c.completedThrough = 0
	c.lastErr = nil
	c.mu.Unlock()
}
