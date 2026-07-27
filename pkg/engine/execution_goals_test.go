package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func TestExecutionGoalTransactionReservationReleaseDoesNotConsumeCapacity(t *testing.T) {
	var cancelCalls atomic.Int32
	limiter := NewExecutionGoalLimiter(dsl.RunLogicConfig{MaxTransactions: 1}, func() {
		cancelCalls.Add(1)
	})
	if !limiter.TryReserveTransaction() {
		t.Fatal("initial transaction reservation failed")
	}
	if limiter.TryStartTransaction() {
		t.Fatal("transaction started while the sole slot was reserved")
	}
	if cancelCalls.Load() != 0 {
		t.Fatal("pending reservation prematurely cancelled the shared execution goal")
	}

	limiter.ReleaseTransactionReservation()
	if !limiter.TryStartTransaction() {
		t.Fatal("released transaction reservation did not restore capacity")
	}
	if limiter.TryStartTransaction() {
		t.Fatal("transaction limit admitted a second transaction")
	}
	if cancelCalls.Load() != 1 {
		t.Fatalf("goal cancellation calls = %d, want 1", cancelCalls.Load())
	}
}

func TestExecutionGoalIterationReservationReleaseDoesNotConsumeCapacity(t *testing.T) {
	var cancelCalls atomic.Int32
	limiter := NewExecutionGoalLimiter(dsl.RunLogicConfig{GlobalIterations: 1}, func() {
		cancelCalls.Add(1)
	})
	if !limiter.TryReserveIteration() {
		t.Fatal("initial iteration reservation failed")
	}
	if limiter.TryStartIteration() {
		t.Fatal("iteration started while the sole slot was reserved")
	}
	if cancelCalls.Load() != 0 {
		t.Fatal("pending iteration reservation prematurely cancelled the shared execution goal")
	}

	limiter.ReleaseIterationReservation()
	if !limiter.TryStartIteration() {
		t.Fatal("released iteration reservation did not restore capacity")
	}
	if limiter.TryStartIteration() {
		t.Fatal("iteration limit admitted a second committed iteration")
	}
	if cancelCalls.Load() != 1 {
		t.Fatalf("goal cancellation calls = %d, want 1", cancelCalls.Load())
	}
}

func TestExecutionGoalWaitReservationHandoff(t *testing.T) {
	tests := []struct {
		name    string
		limit   dsl.RunLogicConfig
		reserve func(*ExecutionGoalLimiter) bool
		wait    func(*ExecutionGoalLimiter, context.Context) bool
		release func(*ExecutionGoalLimiter)
	}{
		{
			name:    "iteration",
			limit:   dsl.RunLogicConfig{GlobalIterations: 1},
			reserve: (*ExecutionGoalLimiter).TryReserveIteration,
			wait:    (*ExecutionGoalLimiter).WaitReserveIteration,
			release: (*ExecutionGoalLimiter).ReleaseIterationReservation,
		},
		{
			name:    "transaction",
			limit:   dsl.RunLogicConfig{MaxTransactions: 1},
			reserve: (*ExecutionGoalLimiter).TryReserveTransaction,
			wait:    (*ExecutionGoalLimiter).WaitReserveTransaction,
			release: (*ExecutionGoalLimiter).ReleaseTransactionReservation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			limiter := NewExecutionGoalLimiter(tc.limit, func() {})
			if !tc.reserve(limiter) {
				t.Fatal("owner failed to reserve the sole slot")
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			waiting := make(chan struct{})
			acquired := make(chan bool, 1)
			go func() {
				close(waiting)
				acquired <- tc.wait(limiter, ctx)
			}()
			<-waiting

			select {
			case got := <-acquired:
				t.Fatalf("contender returned %v while the owner still held a pending reservation", got)
			case <-time.After(20 * time.Millisecond):
			}

			tc.release(limiter)
			select {
			case got := <-acquired:
				if !got {
					t.Fatal("released reservation was not handed to the waiting contender")
				}
				tc.release(limiter)
			case <-ctx.Done():
				t.Fatal("waiting contender was not woken by reservation release")
			}
		})
	}
}

func TestExecutionGoalCommitWakesWaitersToObserveExhaustion(t *testing.T) {
	tests := []struct {
		name    string
		limit   dsl.RunLogicConfig
		reserve func(*ExecutionGoalLimiter) bool
		wait    func(*ExecutionGoalLimiter, context.Context) bool
		commit  func(*ExecutionGoalLimiter)
	}{
		{
			name:    "iteration",
			limit:   dsl.RunLogicConfig{GlobalIterations: 1},
			reserve: (*ExecutionGoalLimiter).TryReserveIteration,
			wait:    (*ExecutionGoalLimiter).WaitReserveIteration,
			commit:  (*ExecutionGoalLimiter).CommitIterationReservation,
		},
		{
			name:    "transaction",
			limit:   dsl.RunLogicConfig{MaxTransactions: 1},
			reserve: (*ExecutionGoalLimiter).TryReserveTransaction,
			wait:    (*ExecutionGoalLimiter).WaitReserveTransaction,
			commit:  (*ExecutionGoalLimiter).CommitTransactionReservation,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var cancelCalls atomic.Int32
			limiter := NewExecutionGoalLimiter(tc.limit, func() { cancelCalls.Add(1) })
			if !tc.reserve(limiter) {
				t.Fatal("owner failed to reserve the sole slot")
			}

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			exhausted := make(chan bool, 1)
			go func() {
				exhausted <- tc.wait(limiter, ctx)
			}()

			tc.commit(limiter)
			select {
			case got := <-exhausted:
				if got {
					t.Fatal("committed capacity was incorrectly handed to a waiter")
				}
			case <-ctx.Done():
				t.Fatal("waiter was not woken by reservation commit")
			}
			if cancelCalls.Load() != 1 {
				t.Fatalf("goal cancellation calls = %d, want 1", cancelCalls.Load())
			}
		})
	}
}

func TestExecutionGoalExactStartCancelsImmediately(t *testing.T) {
	tests := []struct {
		name  string
		limit dsl.RunLogicConfig
		start func(*ExecutionGoalLimiter) bool
	}{
		{
			name:  "iteration",
			limit: dsl.RunLogicConfig{GlobalIterations: 1},
			start: (*ExecutionGoalLimiter).TryStartIteration,
		},
		{
			name:  "transaction",
			limit: dsl.RunLogicConfig{MaxTransactions: 1},
			start: (*ExecutionGoalLimiter).TryStartTransaction,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var cancelCalls atomic.Int32
			limiter := NewExecutionGoalLimiter(tc.limit, func() { cancelCalls.Add(1) })
			if !tc.start(limiter) {
				t.Fatal("exact final slot was not admitted")
			}
			if cancelCalls.Load() != 1 {
				t.Fatalf("goal cancellation calls = %d, want immediate cancellation at the exact limit", cancelCalls.Load())
			}
			if tc.start(limiter) {
				t.Fatal("goal admitted work beyond its exact limit")
			}
			if cancelCalls.Load() != 1 {
				t.Fatalf("goal cancellation calls = %d, want once-only cancellation", cancelCalls.Load())
			}
		})
	}
}

func TestExecutionGoalExactReservationCommitCancelsButReleaseDoesNot(t *testing.T) {
	tests := []struct {
		name    string
		limit   dsl.RunLogicConfig
		reserve func(*ExecutionGoalLimiter) bool
		commit  func(*ExecutionGoalLimiter)
		release func(*ExecutionGoalLimiter)
	}{
		{
			name:    "iteration",
			limit:   dsl.RunLogicConfig{GlobalIterations: 1},
			reserve: (*ExecutionGoalLimiter).TryReserveIteration,
			commit:  (*ExecutionGoalLimiter).CommitIterationReservation,
			release: (*ExecutionGoalLimiter).ReleaseIterationReservation,
		},
		{
			name:    "transaction",
			limit:   dsl.RunLogicConfig{MaxTransactions: 1},
			reserve: (*ExecutionGoalLimiter).TryReserveTransaction,
			commit:  (*ExecutionGoalLimiter).CommitTransactionReservation,
			release: (*ExecutionGoalLimiter).ReleaseTransactionReservation,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var releaseCancelCalls atomic.Int32
			released := NewExecutionGoalLimiter(tc.limit, func() { releaseCancelCalls.Add(1) })
			if !tc.reserve(released) {
				t.Fatal("release case failed to reserve")
			}
			tc.release(released)
			if releaseCancelCalls.Load() != 0 {
				t.Fatal("releasing pending capacity prematurely canceled the goal")
			}

			var commitCancelCalls atomic.Int32
			committed := NewExecutionGoalLimiter(tc.limit, func() { commitCancelCalls.Add(1) })
			if !tc.reserve(committed) {
				t.Fatal("commit case failed to reserve")
			}
			if commitCancelCalls.Load() != 0 {
				t.Fatal("pending reservation prematurely canceled the goal")
			}
			tc.commit(committed)
			if commitCancelCalls.Load() != 1 {
				t.Fatalf("goal cancellation calls = %d, want cancellation when the final pending slot commits", commitCancelCalls.Load())
			}
		})
	}
}

func TestExecutionGoalWaitStartTransactionUsesReleasedPendingCapacity(t *testing.T) {
	var cancelCalls atomic.Int32
	limiter := NewExecutionGoalLimiter(dsl.RunLogicConfig{MaxTransactions: 1}, func() {
		cancelCalls.Add(1)
	})
	if !limiter.TryReserveTransaction() {
		t.Fatal("owner failed to reserve the sole transaction slot")
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	started := make(chan bool, 1)
	go func() {
		started <- limiter.WaitStartTransaction(ctx)
	}()
	select {
	case got := <-started:
		t.Fatalf("wait-start returned %v while capacity was only temporarily pending", got)
	case <-time.After(20 * time.Millisecond):
	}

	limiter.ReleaseTransactionReservation()
	select {
	case got := <-started:
		if !got {
			t.Fatal("released pending capacity was abandoned instead of started")
		}
	case <-ctx.Done():
		t.Fatal("wait-start was not woken by released capacity")
	}
	if cancelCalls.Load() != 1 {
		t.Fatalf("goal cancellation calls = %d, want exact-limit cancellation by the started owner", cancelCalls.Load())
	}
}
