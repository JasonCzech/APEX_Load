package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log/slog"
	"reflect"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
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

// executionLeaseStore is the compatibility subset of storage.Store used for
// leader election. Production stores also implement executionLeaseTermStore so
// the scheduler lease's durable generation can fence generator execution.
type executionLeaseStore interface {
	TryAcquireSchedulerLease(name string, ownerID string, now time.Time, ttl time.Duration) (bool, error)
}

type executionLeaseReleaseStore interface {
	ReleaseSchedulerLease(name string, ownerID string) error
}

type executionLeaseTermStore interface {
	TryAcquireSchedulerLeaseTerm(name string, ownerID string, now time.Time, ttl time.Duration) (bool, *storage.SchedulerLease, error)
}

type leadershipCallbackKind uint8

const (
	leadershipCallbackAcquire leadershipCallbackKind = iota + 1
	leadershipCallbackLoss
	// leadershipCallbackRelease releases one exact durable owner token after
	// the loss cleanup queued before it has finished.
	leadershipCallbackRelease
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
	// now must keep Go's monotonic clock reading (do not call UTC/In/Round on
	// its results): every local lease deadline is compared on the monotonic
	// clock, so a wall-clock step can neither extend nor shorten a lease.
	now func() time.Time

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

	mu                 sync.Mutex
	onAcquire          func()
	onAdmit            func()
	onLoss             func()
	lastConfirmed      time.Time // last time we positively held the lease
	leaseExpiresAt     time.Time // database-authored expiry retained as term metadata
	localLeaseDeadline time.Time // conservative deadline anchored before the DB call
	durableGeneration  uint64    // database-authored monotonic fencing generation
	generation         uint64    // incremented on every leader/follower transition
	pendingLoss        uint64    // queued or running loss callbacks blocking admission
	lossDone           *sync.Cond
	operationCtx       context.Context
	operationCancel    context.CancelFunc

	// leaseOwnerID identifies one acquisition epoch, not merely one process.
	// Durable execution writes carry this token and the store verifies it against
	// scheduler_leases in the same transaction. Rotating it before every
	// acquisition prevents a callback from an older epoch being mistaken for the
	// same process after it reacquires leadership.
	leaseAttempt uint64
	leaseOwnerID string
	// processNonce makes every owner token unique to this process. ownerID can
	// be operator-configured (APEXLOAD_CONTROLLER_ID) to the same value on every
	// replica or survive a container restart, and the lease treats an identical
	// owner token as a renewal rather than a contest.
	processNonce string
	// acquireNotBefore holds off contention after a voluntary step-down so a
	// healthy replica can take over before this one retries.
	acquireNotBefore time.Time
	tickMu           sync.Mutex
}

func newExecutionLeadership(store executionLeaseStore, ownerID string) *executionLeadership {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	l := &executionLeadership{
		store:           store,
		ownerID:         ownerID,
		ttl:             defaultExecutionLeaseTTL,
		renew:           defaultExecutionLeaseRenew,
		now:             time.Now,
		operationCtx:    ctx,
		operationCancel: cancel,
		processNonce:    newExecutionLeaseProcessNonce(),
	}
	l.lossDone = sync.NewCond(&l.mu)
	return l
}

func newExecutionLeaseProcessNonce() string {
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		// crypto/rand does not fail on supported platforms; keep a distinct
		// per-process value even if it ever does.
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(nonce[:])
}

// nextLeaseOwnerIDLocked starts a new acquisition epoch and returns its owner
// token. l.mu must be held.
func (l *executionLeadership) nextLeaseOwnerIDLocked() string {
	if l.processNonce == "" {
		l.processNonce = newExecutionLeaseProcessNonce()
	}
	l.leaseAttempt++
	l.leaseOwnerID = fmt.Sprintf("%s/%s/%d", l.ownerID, l.processNonce, l.leaseAttempt)
	return l.leaseOwnerID
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

func (l *executionLeadership) setOnAdmit(fn func()) {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.onAdmit = fn
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
	now := l.now()
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
	if l.leaseExpiredLocked(now) {
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
	watchdogStop := make(chan struct{})
	defer close(watchdogStop)
	watchdogInterval := l.ttl / 4
	if watchdogInterval <= 0 || watchdogInterval > time.Second {
		watchdogInterval = time.Second
	}
	go func() {
		ticker := time.NewTicker(watchdogInterval)
		defer ticker.Stop()
		for {
			select {
			case <-watchdogStop:
				return
			case <-ticker.C:
				now := l.now()
				l.mu.Lock()
				stale := l.leader.Load() && l.leaseExpiredLocked(now)
				steppedDown := stale && l.transitionToFollowerLocked()
				l.mu.Unlock()
				if steppedDown {
					slog.Warn("execution leadership watchdog expired stale lease", "owner", l.ownerID)
				}
			}
		}
	}()
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
	l.tickMu.Lock()
	defer l.tickMu.Unlock()

	requestStartedAt := l.now()
	l.mu.Lock()
	if !l.leader.Load() && requestStartedAt.Before(l.acquireNotBefore) {
		l.mu.Unlock()
		return
	}
	if !l.leader.Load() {
		l.nextLeaseOwnerIDLocked()
	} else if l.leaseOwnerID == "" {
		// Compatibility for tests/bootstrap paths that install leader state
		// directly. Normal acquisitions always take the branch above.
		l.nextLeaseOwnerIDLocked()
	}
	leaseOwnerID := l.leaseOwnerID
	transitionGeneration := l.generation
	wasLeader := l.leader.Load()
	l.mu.Unlock()

	var leaseTerm *storage.SchedulerLease
	var acquired bool
	var err error
	if termStore, ok := l.store.(executionLeaseTermStore); ok {
		acquired, leaseTerm, err = termStore.TryAcquireSchedulerLeaseTerm(executionLeaseName, leaseOwnerID, requestStartedAt, l.ttl)
	} else {
		acquired, err = l.store.TryAcquireSchedulerLease(executionLeaseName, leaseOwnerID, requestStartedAt, l.ttl)
		if acquired {
			leaseTerm = &storage.SchedulerLease{
				Name:      executionLeaseName,
				OwnerID:   leaseOwnerID,
				ExpiresAt: requestStartedAt.Add(l.ttl),
				UpdatedAt: requestStartedAt,
			}
		}
	}

	// The lease call can outlive the local watchdog. Never let a delayed response
	// re-admit an epoch that was already canceled while the database call was in
	// flight. If that stale call did acquire the durable row, release only its
	// exact owner token; the next tick will rotate to a fresh acquisition token.
	l.mu.Lock()
	staleResponse := l.generation != transitionGeneration || l.leaseOwnerID != leaseOwnerID || (wasLeader && !l.leader.Load())
	l.mu.Unlock()
	if staleResponse {
		if acquired {
			if store, ok := l.store.(executionLeaseReleaseStore); ok {
				if releaseErr := store.ReleaseSchedulerLease(executionLeaseName, leaseOwnerID); releaseErr != nil {
					slog.Warn("failed to release stale execution lease response", "owner", leaseOwnerID, "err", releaseErr)
				}
			}
		}
		return
	}
	if err != nil {
		// A transient store error must not immediately surrender leadership
		// (that would flap), but we must step down before the lease could be
		// granted elsewhere. Step down only once our last confirmed hold is
		// older than the TTL — bounding the split-brain window to the TTL.
		l.mu.Lock()
		stale := l.leaseExpiredLocked(l.now())
		steppedDown := stale && l.transitionToFollowerLocked()
		l.mu.Unlock()
		if steppedDown {
			slog.Warn("execution leadership lost after lease renewal errors", "owner", leaseOwnerID)
		}
		slog.Warn("execution lease renewal failed", "owner", leaseOwnerID, "err", err)
		return
	}
	if !acquired {
		// Another replica holds an unexpired lease. We are not the leader.
		l.mu.Lock()
		steppedDown := l.transitionToFollowerLocked()
		l.mu.Unlock()
		if steppedDown {
			slog.Info("execution leadership relinquished to another replica", "owner", leaseOwnerID)
		}
		return
	}
	localLeaseDeadline := requestStartedAt.Add(l.ttl)
	_, generationAwareStore := l.store.(executionLeaseTermStore)
	invalidGeneration := generationAwareStore && leaseTerm != nil && leaseTerm.Generation == 0
	if leaseTerm == nil || leaseTerm.OwnerID != leaseOwnerID || leaseTerm.ExpiresAt.IsZero() || invalidGeneration || !l.now().Before(localLeaseDeadline) {
		if store, ok := l.store.(executionLeaseReleaseStore); ok {
			_ = store.ReleaseSchedulerLease(executionLeaseName, leaseOwnerID)
		}
		l.mu.Lock()
		steppedDown := l.transitionToFollowerLocked()
		l.mu.Unlock()
		if steppedDown {
			slog.Warn("execution leadership response arrived after the conservative local deadline", "owner", leaseOwnerID)
		}
		return
	}
	l.mu.Lock()
	// Never derive freshness from response arrival. The returned term was read in
	// the acquisition transaction, but its database-clock timestamps are metadata
	// only; delayed delivery cannot extend the request-anchored local deadline.
	l.lastConfirmed = leaseTerm.UpdatedAt
	l.leaseExpiresAt = leaseTerm.ExpiresAt
	l.localLeaseDeadline = localLeaseDeadline
	l.durableGeneration = leaseTerm.Generation
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
				acquireCallback()
				var onAdmit func()
				l.mu.Lock()
				if l.generation == acquireGeneration && l.leader.Load() {
					l.reconciling.Store(false)
					onAdmit = l.onAdmit
				}
				l.mu.Unlock()
				if onAdmit != nil {
					onAdmit()
				}
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
		slog.Info("execution leadership acquired", "owner", leaseOwnerID)
	}
}

func (l *executionLeadership) relinquish() {
	if l == nil {
		return
	}
	l.mu.Lock()
	l.transitionToFollowerLocked()
	for l.pendingLoss > 0 {
		if l.lossDone == nil {
			l.lossDone = sync.NewCond(&l.mu)
		}
		l.lossDone.Wait()
	}
	leaseOwnerID := l.leaseOwnerID
	l.mu.Unlock()
	if store, ok := l.store.(executionLeaseReleaseStore); ok {
		if err := store.ReleaseSchedulerLease(executionLeaseName, leaseOwnerID); err != nil {
			slog.Warn("failed to release execution leadership lease", "owner", leaseOwnerID, "err", err)
		}
	}
}

// executionFenceToken returns the durable lease token for the current epoch.
// It deliberately includes the acquire-time reconciliation window: recovery
// owns the lease and may perform fenced repairs before public admission opens.
func (l *executionLeadership) executionFenceToken() string {
	token, _ := l.executionLeaseTerm()
	return token
}

// executionLeaseTerm snapshots the exact durable owner token and monotonic
// generation under one lock so callers cannot combine values from two terms.
func (l *executionLeadership) executionLeaseTerm() (string, uint64) {
	if l == nil {
		return "", 0
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.leader.Load() || l.pendingLoss > 0 || l.leaseOwnerID == "" {
		return "", 0
	}
	if l.leaseExpiredLocked(l.now()) {
		l.transitionToFollowerLocked()
		return "", 0
	}
	return l.leaseOwnerID, l.durableGeneration
}

func (l *executionLeadership) executionGeneratorClaim() (uint64, string, time.Time, bool) {
	if l == nil {
		return 0, "", time.Time{}, true
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	if !l.leader.Load() || l.pendingLoss > 0 || l.durableGeneration == 0 || l.leaseExpiredLocked(now) {
		return 0, "", time.Time{}, false
	}
	if !now.Before(l.localLeaseDeadline) {
		return 0, "", time.Time{}, false
	}
	return l.durableGeneration, l.leaseOwnerID, l.localLeaseDeadline, true
}

func (l *executionLeadership) leaseExpiredLocked(now time.Time) bool {
	if !l.localLeaseDeadline.IsZero() {
		return !now.Before(l.localLeaseDeadline)
	}
	return !l.lastConfirmed.IsZero() && now.Sub(l.lastConfirmed) >= l.ttl
}

// sameStoreInstance reports whether two persistence interfaces wrap the same
// concrete pointer. Durable fencing is enabled only in that configuration so a
// lease check and the protected write share one database transaction. This is
// the production server shape; tests may intentionally use an independent fake
// lease store.
func sameStoreInstance(left, right any) bool {
	if left == nil || right == nil {
		return false
	}
	lv := reflect.ValueOf(left)
	rv := reflect.ValueOf(right)
	return lv.Kind() == reflect.Pointer && rv.Kind() == reflect.Pointer && lv.Type() == rv.Type() && lv.Pointer() == rv.Pointer()
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
// epoch and snapshots its durable lease term into the returned context. The
// epoch is cancelled synchronously on lease loss and never reused on
// reacquisition, so an old operation cannot resume under a newer lease.
func (l *executionLeadership) operationContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	if l == nil {
		return context.WithCancel(parent)
	}
	l.mu.Lock()
	if l.leader.Load() && l.pendingLoss == 0 && (l.operationCtx == nil || l.operationCtx.Err() != nil) {
		// Some recovery/bootstrap paths install an already-confirmed leadership
		// state before Run begins. Materialize the epoch lazily so those operations
		// still receive the same lease-loss cancellation semantics.
		l.operationCtx, l.operationCancel = context.WithCancel(context.Background())
	}
	epoch := l.operationCtx
	active := l.leader.Load() && l.pendingLoss == 0 && epoch != nil
	term := executionLeaseTerm{ownerID: l.leaseOwnerID, generation: l.durableGeneration}
	l.mu.Unlock()
	ctx, cancel := context.WithCancel(parent)
	// Bind the exact durable term captured with this leadership epoch. Replay
	// completion hooks may run long after their request returned and must never
	// adopt a newer term if this process loses and later reacquires leadership.
	ctx = context.WithValue(ctx, executionLeaseTermContextKey{}, term)
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
				l.invokeLossCallback(event)
				continue
			}
			if event.kind == leadershipCallbackRelease {
				if panicValue := invokeLeadershipCallback(event.fn); panicValue != nil {
					slog.Error("execution leadership release callback panicked", "panic", panicValue)
				}
				continue
			}
			if l.shouldRunLeadershipCallback(event) {
				if panicValue := invokeLeadershipCallback(event.fn); panicValue != nil {
					slog.Error("execution leadership acquire callback panicked", "generation", event.generation, "panic", panicValue)
					l.failAcquireCallback(event)
				}
			}
		}
	}()
}

// invokeLossCallback guarantees that the admission gate is released even when
// cleanup code panics. Panics in an asynchronous callback must not terminate the
// controller process or wedge relinquish forever.
func (l *executionLeadership) invokeLossCallback(event leadershipCallbackEvent) {
	defer l.completeLossCallback()
	if panicValue := invokeLeadershipCallback(event.fn); panicValue != nil {
		slog.Error("execution leadership loss callback panicked", "generation", event.generation, "panic", panicValue)
	}
}

func invokeLeadershipCallback(fn func()) (panicValue any) {
	if fn == nil {
		return nil
	}
	defer func() {
		panicValue = recover()
	}()
	fn()
	return nil
}

// failAcquireCallback keeps public admission closed after failed recovery,
// cancels the epoch, and relinquishes the exact durable token so a later tick
// can retry recovery under a new acquisition generation.
func (l *executionLeadership) failAcquireCallback(event leadershipCallbackEvent) {
	if l == nil {
		return
	}
	l.mu.Lock()
	leaseOwnerID := ""
	if l.leader.Load() && (event.generation == 0 || l.generation == event.generation) {
		leaseOwnerID = l.leaseOwnerID
		l.transitionToFollowerLocked()
	}
	l.mu.Unlock()
	if leaseOwnerID == "" {
		return
	}
	if store, ok := l.store.(executionLeaseReleaseStore); ok {
		if err := store.ReleaseSchedulerLease(executionLeaseName, leaseOwnerID); err != nil {
			slog.Warn("failed to release execution lease after recovery callback panic", "owner", leaseOwnerID, "err", err)
		}
	}
}

// relinquishAfterRecoveryFailure steps down voluntarily when acquire-time
// recovery cannot complete, so another replica can try. Loss cleanup runs
// first (it decides which runs are handed off rather than stopped), then the
// exact durable token is released for an immediate takeover. This replica does
// not contend again until cooldown has passed. It reports whether it stepped
// down. It is safe to call from the acquire callback: both follow-up events
// run on the ordered worker after the callback returns.
func (l *executionLeadership) relinquishAfterRecoveryFailure(cooldown time.Duration) bool {
	if l == nil {
		return false
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.leader.Load() {
		return false
	}
	leaseOwnerID := l.leaseOwnerID
	l.acquireNotBefore = l.now().Add(cooldown)
	l.transitionToFollowerLocked()
	if store, ok := l.store.(executionLeaseReleaseStore); ok && leaseOwnerID != "" {
		l.enqueueLeadershipCallback(leadershipCallbackEvent{
			kind: leadershipCallbackRelease,
			fn: func() {
				if err := store.ReleaseSchedulerLease(executionLeaseName, leaseOwnerID); err != nil {
					slog.Warn("failed to release execution lease after recovery failure", "owner", leaseOwnerID, "err", err)
				}
			},
		})
	}
	return true
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
	if l.lossDone != nil {
		l.lossDone.Broadcast()
	}
	l.mu.Unlock()
}
