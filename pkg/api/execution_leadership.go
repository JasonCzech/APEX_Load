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

type leadershipCallbackKind uint8

const (
	leadershipCallbackAcquire leadershipCallbackKind = iota + 1
	leadershipCallbackLoss
)

type leadershipCallbackEvent struct {
	kind       leadershipCallbackKind
	generation uint64
	fn         func()
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

	leader      atomic.Bool
	reconciling atomic.Bool

	// callbackMu guards the ordered leadership-transition callback worker.
	// Loss cleanup and acquire-time reconciliation share this queue so a loss
	// callback from an older generation always finishes before reconciliation
	// for a newer generation. Consecutive acquire requests are coalesced while a
	// reconcile is in flight, preserving the original bounded-work behavior.
	callbackMu      sync.Mutex
	callbackRunning bool
	callbackQueue   []leadershipCallbackEvent

	mu              sync.Mutex
	onAcquire       func()
	onLoss          func()
	lastConfirmed   time.Time // last time we positively held the lease
	generation      uint64    // incremented on every leader/follower transition
	pendingLoss     uint64    // queued or running loss callbacks blocking admission
	operationCtx    context.Context
	operationCancel context.CancelFunc
}

func newExecutionLeadership(store executionLeaseStore, ownerID string) *executionLeadership {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return &executionLeadership{
		store:           store,
		ownerID:         ownerID,
		ttl:             defaultExecutionLeaseTTL,
		renew:           defaultExecutionLeaseRenew,
		now:             time.Now,
		operationCtx:    ctx,
		operationCancel: cancel,
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

// setOnLoss registers a non-blocking cleanup callback for lease loss and
// controller shutdown. The old leader still owns the live generator clients,
// so it is the only replica that can reliably stop work before takeover.
func (l *executionLeadership) setOnLoss(fn func()) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.onLoss = fn
	l.mu.Unlock()
}

// IsLeader reports whether this replica currently holds the execution lease. A
// nil receiver is always the leader (single-node default).
func (l *executionLeadership) IsLeader() bool {
	if l == nil {
		return true
	}
	now := l.now().UTC()
	l.mu.Lock()
	if !l.leader.Load() || l.pendingLoss > 0 {
		l.mu.Unlock()
		return false
	}
	// Defense in depth: if renewals have not positively confirmed the lease for
	// longer than the TTL — e.g. the renewal goroutine is wedged on a blocking
	// store call and can never reach the step-down path in tick() — fail closed
	// rather than continue mutating shared execution state after the DB lease
	// could already have been granted to another replica. The freshness read and
	// step-down decision stay under the same lock used by successful renewals;
	// this prevents a stale snapshot from stepping down after a concurrent tick
	// has already renewed the lease.
	if !l.lastConfirmed.IsZero() && now.Sub(l.lastConfirmed) >= l.ttl {
		l.transitionToFollowerLocked()
		l.mu.Unlock()
		slog.Warn("execution leadership expired without a successful renewal", "owner", l.ownerID)
		return false
	}
	l.mu.Unlock()
	return true
}

// IsAdmitted reports whether this replica may mutate execution state. A newly
// acquired lease is not admitted until durable recovery has completed.
func (l *executionLeadership) IsAdmitted() bool {
	return l != nil && l.IsLeader() && !l.reconciling.Load()
}

// Run renews the lease until ctx is cancelled. It acquires immediately, then
// renews on the configured interval.
func (l *executionLeadership) Run(ctx context.Context) {
	if l == nil {
		return
	}
	defer l.relinquish()
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
		steppedDown := stale && l.transitionToFollowerLocked()
		l.mu.Unlock()
		if steppedDown {
			slog.Warn("execution leadership lost after lease renewal errors", "owner", l.ownerID)
		}
		slog.Warn("execution lease renewal failed", "owner", l.ownerID, "err", err)
		return
	}
	if !acquired {
		// Another replica holds an unexpired lease. We are not the leader.
		l.mu.Lock()
		steppedDown := l.transitionToFollowerLocked()
		l.mu.Unlock()
		if steppedDown {
			slog.Info("execution leadership relinquished to another replica", "owner", l.ownerID)
		}
		return
	}
	l.mu.Lock()
	l.lastConfirmed = now
	onAcquire := l.onAcquire
	acquiredLeadership := false
	if !l.leader.Load() {
		if l.operationCancel != nil {
			l.operationCancel()
		}
		l.operationCtx, l.operationCancel = context.WithCancel(context.Background())
		l.reconciling.Store(onAcquire != nil)
		l.leader.Store(true)
		l.generation++
		acquireGeneration := l.generation
		acquiredLeadership = true
		if onAcquire != nil {
			acquireCallback := onAcquire
			onAcquire = func() {
				defer func() {
					l.mu.Lock()
					if l.generation == acquireGeneration && l.leader.Load() {
						l.reconciling.Store(false)
					}
					l.mu.Unlock()
				}()
				acquireCallback()
			}
		}
		// The ordered worker keeps the potentially long reconcile off the lease
		// renewal goroutine while preserving transition order.
		l.enqueueLeadershipCallback(leadershipCallbackEvent{
			kind:       leadershipCallbackAcquire,
			generation: l.generation,
			fn:         onAcquire,
		})
	}
	l.mu.Unlock()
	if acquiredLeadership {
		slog.Info("execution leadership acquired", "owner", l.ownerID)
	}
}

func (l *executionLeadership) relinquish() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.transitionToFollowerLocked()
	l.mu.Unlock()
}

// transitionToFollowerLocked records a leadership loss and enqueues its
// cleanup callback in generation order. l.mu must be held by the caller.
func (l *executionLeadership) transitionToFollowerLocked() bool {
	if !l.leader.Load() {
		return false
	}
	l.leader.Store(false)
	l.reconciling.Store(false)
	if l.operationCancel != nil {
		// Cancel object-store publication/deletion synchronously with admission
		// closing. A stale leader must not finish an in-flight catalog or outbox
		// commit after another replica can acquire the lease.
		l.operationCancel()
	}
	l.generation++
	l.pendingLoss++
	l.enqueueLeadershipCallback(leadershipCallbackEvent{
		kind:       leadershipCallbackLoss,
		generation: l.generation,
		fn:         l.onLoss,
	})
	return true
}

// operationContext combines caller cancellation with the current leadership
// epoch. The epoch is cancelled synchronously on lease loss and never reused on
// reacquisition, so an old operation cannot resume under a newer lease.
func (l *executionLeadership) operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if l == nil {
		return context.WithCancel(parent)
	}
	l.mu.Lock()
	epoch := l.operationCtx
	active := l.leader.Load() && l.pendingLoss == 0 && epoch != nil
	l.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	if !active {
		cancel()
		return ctx, cancel
	}
	stop := context.AfterFunc(epoch, cancel)
	return ctx, func() {
		stop()
		cancel()
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
	l.enqueueLeadershipCallback(leadershipCallbackEvent{
		kind: leadershipCallbackAcquire,
		fn:   onAcquire,
	})
}

// enqueueLeadershipCallback appends a transition callback to the single
// ordered worker. State transitions invoke this while holding l.mu, which makes
// queue insertion follow generation assignment exactly; the worker never holds
// callbackMu while invoking callbacks or consulting leadership state.
func (l *executionLeadership) enqueueLeadershipCallback(event leadershipCallbackEvent) {
	// Loss events must be queued even without a registered callback: their
	// completion releases the admission gate established by the transition.
	if event.fn == nil && event.kind != leadershipCallbackLoss {
		return
	}
	l.callbackMu.Lock()
	if event.kind == leadershipCallbackAcquire && len(l.callbackQueue) > 0 && l.callbackQueue[len(l.callbackQueue)-1].kind == leadershipCallbackAcquire {
		// Keep only the newest of consecutive pending acquire callbacks. A loss
		// event is an ordering barrier and is never crossed by coalescing.
		l.callbackQueue[len(l.callbackQueue)-1] = event
		l.callbackMu.Unlock()
		return
	}
	l.callbackQueue = append(l.callbackQueue, event)
	if l.callbackRunning {
		l.callbackMu.Unlock()
		return
	}
	l.callbackRunning = true
	l.callbackMu.Unlock()

	go func() {
		for {
			l.callbackMu.Lock()
			if len(l.callbackQueue) == 0 {
				l.callbackRunning = false
				l.callbackMu.Unlock()
				return
			}
			event := l.callbackQueue[0]
			l.callbackQueue = l.callbackQueue[1:]
			l.callbackMu.Unlock()

			if event.kind == leadershipCallbackLoss {
				if event.fn != nil {
					event.fn()
				}
				l.completeLossCallback()
				continue
			}
			if l.shouldRunLeadershipCallback(event) {
				event.fn()
			}
		}
	}()
}

// shouldRunLeadershipCallback drops acquire callbacks superseded before they
// begin. Loss cleanup is never dropped: even after reacquisition it must run
// before the newer acquire callback can reconcile live work safely. Generation
// zero is reserved for direct triggerReconcile calls in tests; those bypass the
// leader-generation check but still respect the pending-loss admission gate.
func (l *executionLeadership) shouldRunLeadershipCallback(event leadershipCallbackEvent) bool {
	if event.kind != leadershipCallbackAcquire {
		return true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.pendingLoss > 0 {
		return false
	}
	if event.generation == 0 {
		return true
	}
	return l.leader.Load() && l.generation == event.generation
}

// completeLossCallback opens admission only after one queued/running cleanup
// has fully returned. The internal leader bit may already be true because tick
// must keep renewing a reacquired lease while cleanup drains.
func (l *executionLeadership) completeLossCallback() {
	l.mu.Lock()
	if l.pendingLoss > 0 {
		l.pendingLoss--
	}
	l.mu.Unlock()
}
