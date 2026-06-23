package api

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// executionLeaseName is the well-known scheduler-lease key used to elect the
// single controller replica that is allowed to mutate live test-execution
// state. It rides on the same scheduler_leases table that already backs the
// scenario scheduler, so it works identically on SQLite and PostgreSQL.
const executionLeaseName = "controller-execution-leader"

const (
	defaultExecutionLeaseTTL   = 30 * time.Second
	defaultExecutionLeaseRenew = 10 * time.Second
)

// executionLeaseStore is the subset of storage.Store used for leader election.
// Reusing the scenario-scheduler lease primitive means no new storage method,
// schema migration, or test double is required.
type executionLeaseStore interface {
	TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error)
}

// executionLeadership coordinates which controller replica performs test
// execution when more than one replica runs against a shared database (HA).
//
// Only the lease holder ("leader") promotes queued tests, reconciles
// interrupted runs, and starts new runs; every other replica serves the
// read-only API and refuses execution-mutating calls. On leader failure the
// lease expires (TTL) and a standby takes over, reloading state from the
// database and reconciling before it begins executing.
//
// A nil *executionLeadership means "always leader" — that is the single-node /
// test default, so behavior is unchanged unless HA election is explicitly
// enabled.
type executionLeadership struct {
	store   executionLeaseStore
	ownerID string
	ttl     time.Duration
	renew   time.Duration
	now     func() time.Time

	leader atomic.Bool

	// reconcileMu guards the acquire-time reconcile lifecycle. At most one
	// reconcile runs at a time; if leadership flaps (false->true->false->true)
	// while one is in flight, the new request is COALESCED via reconcilePending
	// and re-run when the in-flight one finishes — rather than dropped, which
	// would leave the final takeover unreconciled.
	reconcileMu      sync.Mutex
	reconcileRunning bool
	reconcilePending bool

	mu            sync.Mutex
	onAcquire     func()
	lastConfirmed time.Time // last time we positively held the lease
}

func newExecutionLeadership(store executionLeaseStore, ownerID string) *executionLeadership {
	return &executionLeadership{
		store:   store,
		ownerID: ownerID,
		ttl:     defaultExecutionLeaseTTL,
		renew:   defaultExecutionLeaseRenew,
		now:     time.Now,
	}
}

// setOnAcquire registers the callback invoked once each time leadership is
// gained (initial acquisition at boot or takeover after a failover). It must be
// set before Run starts.
func (l *executionLeadership) setOnAcquire(fn func()) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.onAcquire = fn
	l.mu.Unlock()
}

// IsLeader reports whether this replica currently holds the execution lease. A
// nil receiver is always the leader (single-node default).
func (l *executionLeadership) IsLeader() bool {
	if l == nil {
		return true
	}
	if !l.leader.Load() {
		return false
	}
	// Defense in depth: if renewals have not positively confirmed the lease for
	// longer than the TTL — e.g. the renewal goroutine is wedged on a blocking
	// store call and can never reach the step-down path in tick() — fail closed
	// rather than continue mutating shared execution state after the DB lease
	// could already have been granted to another replica.
	l.mu.Lock()
	last := l.lastConfirmed
	l.mu.Unlock()
	if !last.IsZero() && l.now().UTC().Sub(last) >= l.ttl {
		return false
	}
	return true
}

// Run renews the lease until ctx is cancelled. It acquires immediately, then
// renews on the configured interval.
func (l *executionLeadership) Run(ctx context.Context) {
	if l == nil {
		return
	}
	l.tick()
	ticker := time.NewTicker(l.renew)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.tick()
		}
	}
}

// tick performs a single acquire/renew attempt and updates leadership state.
func (l *executionLeadership) tick() {
	if l == nil || l.store == nil {
		return
	}
	now := l.now().UTC()
	acquired, err := l.store.TryAcquireSchedulerLease(executionLeaseName, l.ownerID, now, l.ttl)
	if err != nil {
		// A transient store error must not immediately surrender leadership
		// (that would flap), but we must step down before the lease could be
		// granted elsewhere. Step down only once our last confirmed hold is
		// older than the TTL — bounding the split-brain window to the TTL.
		l.mu.Lock()
		stale := !l.lastConfirmed.IsZero() && now.Sub(l.lastConfirmed) >= l.ttl
		l.mu.Unlock()
		if stale && l.leader.CompareAndSwap(true, false) {
			slog.Warn("execution leadership lost after lease renewal errors", "owner", l.ownerID)
		}
		slog.Warn("execution lease renewal failed", "owner", l.ownerID, "err", err)
		return
	}
	if !acquired {
		// Another replica holds an unexpired lease. We are not the leader.
		if l.leader.CompareAndSwap(true, false) {
			slog.Info("execution leadership relinquished to another replica", "owner", l.ownerID)
		}
		return
	}
	l.mu.Lock()
	l.lastConfirmed = now
	onAcquire := l.onAcquire
	l.mu.Unlock()
	if l.leader.CompareAndSwap(false, true) {
		slog.Info("execution leadership acquired", "owner", l.ownerID)
		// Run the acquire-time reconcile OFF the renewal goroutine. It can reload
		// the full test set and run synchronous per-group script shakeouts (up to
		// ~30s each), which is longer than the lease TTL; if it ran inline it would
		// block lease renewal and let the lease this replica just acquired expire
		// mid-takeover, admitting a second leader (split-brain).
		l.triggerReconcile(onAcquire)
	}
}

// triggerReconcile schedules the acquire-time reconcile, ensuring at most one
// runs at a time. If one is already in flight, the request is coalesced: the
// running worker re-runs onAcquire once it finishes, so a takeover that happens
// while a prior reconcile is still executing is never left unreconciled.
func (l *executionLeadership) triggerReconcile(onAcquire func()) {
	if onAcquire == nil {
		return
	}
	l.reconcileMu.Lock()
	l.reconcilePending = true
	if l.reconcileRunning {
		// A worker is already running; it will observe reconcilePending and re-run.
		l.reconcileMu.Unlock()
		return
	}
	l.reconcileRunning = true
	l.reconcileMu.Unlock()

	go func() {
		for {
			l.reconcileMu.Lock()
			if !l.reconcilePending {
				// No pending request: clear the running flag under the lock so a
				// concurrent triggerReconcile either sees running=true (and sets
				// pending, which we'd miss) — avoided because we re-check pending
				// here before releasing — or sees running=false and starts fresh.
				l.reconcileRunning = false
				l.reconcileMu.Unlock()
				return
			}
			l.reconcilePending = false
			l.reconcileMu.Unlock()
			onAcquire()
		}
	}()
}
