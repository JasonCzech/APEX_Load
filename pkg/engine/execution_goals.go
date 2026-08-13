package engine

import (
	"context"
	"sync"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type ExecutionGoalLimiter struct {
	maxIterations   int64
	maxTransactions int64
	cancel          context.CancelFunc
	cancelOnce      sync.Once

	iterationMu       sync.Mutex
	iterations        int64
	pendingIterations int64
	iterationChanged  chan struct{}

	transactionMu       sync.Mutex
	transactions        int64
	pendingTransactions int64
	transactionChanged  chan struct{}
}

// hasExecutionGoals reports whether the run logic configures a shared
// execution goal that requires an ExecutionGoalLimiter.
func hasExecutionGoals(runLogic dsl.RunLogicConfig) bool {
	return runLogic.GlobalIterations > 0 || runLogic.MaxTransactions > 0
}

// hasExactSingleExecutionGoal identifies the narrow case where one admitted
// VUser can mathematically finish the shared run before any peer is entitled to
// start. Only this case needs lazy peer initialization; larger goals retain the
// all-user startup barrier and its fail-fast guarantee.
func hasExactSingleExecutionGoal(runLogic dsl.RunLogicConfig) bool {
	return runLogic.GlobalIterations == 1 || runLogic.MaxTransactions == 1
}

// NewExecutionGoalLimiter builds a limiter that stops the run once a shared
// goal is reached. The cancel function must only stop VUsers from *starting*
// new iterations/transactions: a VUser that already claimed a reservation via
// TryStartIteration/TryStartTransaction is entitled to finish and record it,
// so callers must not abort in-flight iterations with this cancel (see the
// loop/hard context split in Engine.Run and VUser.Run).
func NewExecutionGoalLimiter(runLogic dsl.RunLogicConfig, cancel context.CancelFunc) *ExecutionGoalLimiter {
	if !hasExecutionGoals(runLogic) {
		return nil
	}
	return &ExecutionGoalLimiter{
		maxIterations:      int64(runLogic.GlobalIterations),
		maxTransactions:    runLogic.MaxTransactions,
		cancel:             cancel,
		iterationChanged:   make(chan struct{}),
		transactionChanged: make(chan struct{}),
	}
}

func (l *ExecutionGoalLimiter) TryStartIteration() bool {
	if l == nil || l.maxIterations <= 0 {
		return true
	}
	l.iterationMu.Lock()
	if l.iterations < l.maxIterations {
		l.iterations++
		shouldCancel := l.iterations >= l.maxIterations && l.pendingIterations == 0
		l.iterationMu.Unlock()
		if shouldCancel {
			l.cancelIfConfigured()
		}
		return true
	}
	shouldCancel := l.pendingIterations == 0
	l.iterationMu.Unlock()
	if shouldCancel {
		l.cancelIfConfigured()
	}
	return false
}

// TryReserveIteration atomically claims one global-iteration slot without
// committing it. VUsers carry this reservation through interruptible
// between-iteration pacing, then either commit it immediately before starting
// the iteration or release it when pacing/pausing is cancelled. Pending
// reservations suppress goal cancellation because they may still restore
// capacity.
func (l *ExecutionGoalLimiter) TryReserveIteration() bool {
	if l == nil || l.maxIterations <= 0 {
		return true
	}
	l.iterationMu.Lock()
	if l.iterations < l.maxIterations {
		l.iterations++
		l.pendingIterations++
		l.iterationMu.Unlock()
		return true
	}
	shouldCancel := l.pendingIterations == 0
	l.iterationMu.Unlock()
	if shouldCancel {
		l.cancelIfConfigured()
	}
	return false
}

// WaitReserveIteration claims an iteration reservation, waiting when all
// capacity is temporarily held by other pending reservations. A commit wakes
// waiters so they can observe definitive exhaustion; a release wakes them so
// exactly one contender can claim the restored slot.
func (l *ExecutionGoalLimiter) WaitReserveIteration(ctx context.Context) bool {
	if l == nil || l.maxIterations <= 0 {
		return true
	}
	for {
		l.iterationMu.Lock()
		if ctx != nil && ctx.Err() != nil {
			l.iterationMu.Unlock()
			return false
		}
		if l.iterations < l.maxIterations {
			l.iterations++
			l.pendingIterations++
			l.iterationMu.Unlock()
			return true
		}
		if l.pendingIterations == 0 {
			l.iterationMu.Unlock()
			l.cancelIfConfigured()
			return false
		}
		changed := l.iterationChanged
		l.iterationMu.Unlock()

		if !waitForGoalReservationChange(ctx, changed) {
			return false
		}
	}
}

func (l *ExecutionGoalLimiter) CommitIterationReservation() {
	if l == nil || l.maxIterations <= 0 {
		return
	}
	l.iterationMu.Lock()
	shouldCancel := false
	if l.pendingIterations > 0 {
		l.pendingIterations--
		l.signalIterationChangeLocked()
		shouldCancel = l.iterations >= l.maxIterations && l.pendingIterations == 0
	}
	l.iterationMu.Unlock()
	if shouldCancel {
		l.cancelIfConfigured()
	}
}

func (l *ExecutionGoalLimiter) ReleaseIterationReservation() {
	if l == nil || l.maxIterations <= 0 {
		return
	}
	l.iterationMu.Lock()
	if l.pendingIterations > 0 {
		l.pendingIterations--
		if l.iterations > 0 {
			l.iterations--
		}
		l.signalIterationChangeLocked()
	}
	l.iterationMu.Unlock()
}

func (l *ExecutionGoalLimiter) TryStartTransaction() bool {
	if l == nil || l.maxTransactions <= 0 {
		return true
	}
	l.transactionMu.Lock()
	if l.transactions < l.maxTransactions {
		l.transactions++
		shouldCancel := l.transactions >= l.maxTransactions && l.pendingTransactions == 0
		l.transactionMu.Unlock()
		if shouldCancel {
			l.cancelIfConfigured()
		}
		return true
	}
	// A pending reservation belongs to a VUser that is pacing before its first
	// per-action transaction. Do not stop the shared loop while that reservation
	// can still either be committed or released.
	shouldCancel := l.pendingTransactions == 0
	l.transactionMu.Unlock()
	if shouldCancel {
		l.cancelIfConfigured()
	}
	return false
}

// TryReserveTransaction atomically claims one transaction slot without yet
// counting it as the first per-action transaction of an iteration. The VUser
// carries this reservation across between-iteration pacing and commits it at
// the first transaction-bearing action. This avoids a racy "remaining" check
// and prevents the same transaction from being counted twice.
func (l *ExecutionGoalLimiter) TryReserveTransaction() bool {
	if l == nil || l.maxTransactions <= 0 {
		return true
	}
	l.transactionMu.Lock()
	if l.transactions < l.maxTransactions {
		l.transactions++
		l.pendingTransactions++
		l.transactionMu.Unlock()
		return true
	}
	shouldCancel := l.pendingTransactions == 0
	l.transactionMu.Unlock()
	if shouldCancel {
		l.cancelIfConfigured()
	}
	return false
}

// WaitReserveTransaction is the transaction counterpart to
// WaitReserveIteration. It waits only while the full capacity is pending and
// therefore still capable of being restored.
func (l *ExecutionGoalLimiter) WaitReserveTransaction(ctx context.Context) bool {
	if l == nil || l.maxTransactions <= 0 {
		return true
	}
	for {
		l.transactionMu.Lock()
		if ctx != nil && ctx.Err() != nil {
			l.transactionMu.Unlock()
			return false
		}
		if l.transactions < l.maxTransactions {
			l.transactions++
			l.pendingTransactions++
			l.transactionMu.Unlock()
			return true
		}
		if l.pendingTransactions == 0 {
			l.transactionMu.Unlock()
			l.cancelIfConfigured()
			return false
		}
		changed := l.transactionChanged
		l.transactionMu.Unlock()

		if !waitForGoalReservationChange(ctx, changed) {
			return false
		}
	}
}

// WaitStartTransaction waits for temporarily reserved capacity to be released,
// then atomically starts a transaction without exposing another pending state.
// This is used at an action boundary where abandoning the iteration merely
// because another VUser is still pacing would strand otherwise reusable goal
// capacity.
func (l *ExecutionGoalLimiter) WaitStartTransaction(ctx context.Context) bool {
	if l == nil || l.maxTransactions <= 0 {
		return true
	}
	for {
		l.transactionMu.Lock()
		if ctx != nil && ctx.Err() != nil {
			l.transactionMu.Unlock()
			return false
		}
		if l.transactions < l.maxTransactions {
			l.transactions++
			shouldCancel := l.transactions >= l.maxTransactions && l.pendingTransactions == 0
			l.transactionMu.Unlock()
			if shouldCancel {
				l.cancelIfConfigured()
			}
			return true
		}
		if l.pendingTransactions == 0 {
			l.transactionMu.Unlock()
			l.cancelIfConfigured()
			return false
		}
		changed := l.transactionChanged
		l.transactionMu.Unlock()

		if !waitForGoalReservationChange(ctx, changed) {
			return false
		}
	}
}

func (l *ExecutionGoalLimiter) CommitTransactionReservation() {
	if l == nil || l.maxTransactions <= 0 {
		return
	}
	l.transactionMu.Lock()
	shouldCancel := false
	if l.pendingTransactions > 0 {
		l.pendingTransactions--
		l.signalTransactionChangeLocked()
		shouldCancel = l.transactions >= l.maxTransactions && l.pendingTransactions == 0
	}
	l.transactionMu.Unlock()
	if shouldCancel {
		l.cancelIfConfigured()
	}
}

func (l *ExecutionGoalLimiter) ReleaseTransactionReservation() {
	if l == nil || l.maxTransactions <= 0 {
		return
	}
	l.transactionMu.Lock()
	if l.pendingTransactions > 0 {
		l.pendingTransactions--
		if l.transactions > 0 {
			l.transactions--
		}
		l.signalTransactionChangeLocked()
	}
	l.transactionMu.Unlock()
}

func (l *ExecutionGoalLimiter) signalIterationChangeLocked() {
	close(l.iterationChanged)
	l.iterationChanged = make(chan struct{})
}

func (l *ExecutionGoalLimiter) signalTransactionChangeLocked() {
	close(l.transactionChanged)
	l.transactionChanged = make(chan struct{})
}

func waitForGoalReservationChange(ctx context.Context, changed <-chan struct{}) bool {
	if ctx == nil {
		<-changed
		return true
	}
	select {
	case <-ctx.Done():
		return false
	case <-changed:
		return true
	}
}

func (l *ExecutionGoalLimiter) hasTransactionLimit() bool {
	return l != nil && l.maxTransactions > 0
}

func (l *ExecutionGoalLimiter) hasIterationLimit() bool {
	return l != nil && l.maxIterations > 0
}

func (l *ExecutionGoalLimiter) cancelIfConfigured() {
	if l != nil && l.cancel != nil {
		l.cancelOnce.Do(l.cancel)
	}
}

func boundedArrivalLimit(script *dsl.Script, workers int) (int, bool) {
	if script == nil {
		return 0, false
	}
	arrivals, bounded := openWorkloadArrivalLimit(script.Config.RunLogic.Iterations, workers)
	if script.Config.RunLogic.GlobalIterations <= 0 {
		return arrivals, bounded
	}
	global := script.Config.RunLogic.GlobalIterations
	if !bounded || global < arrivals {
		return global, true
	}
	return arrivals, true
}
