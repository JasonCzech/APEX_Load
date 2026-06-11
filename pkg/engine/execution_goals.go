package engine

import (
	"context"
	"sync/atomic"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type ExecutionGoalLimiter struct {
	maxIterations   int64
	maxTransactions int64
	cancel          context.CancelFunc
	iterations      atomic.Int64
	transactions    atomic.Int64
}

// hasExecutionGoals reports whether the run logic configures a shared
// execution goal that requires an ExecutionGoalLimiter.
func hasExecutionGoals(runLogic dsl.RunLogicConfig) bool {
	return runLogic.GlobalIterations > 0 || runLogic.MaxTransactions > 0
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
		maxIterations:   int64(runLogic.GlobalIterations),
		maxTransactions: runLogic.MaxTransactions,
		cancel:          cancel,
	}
}

func (l *ExecutionGoalLimiter) TryStartIteration() bool {
	if l == nil || l.maxIterations <= 0 {
		return true
	}
	next := l.iterations.Add(1)
	if next <= l.maxIterations {
		return true
	}
	l.cancelIfConfigured()
	return false
}

func (l *ExecutionGoalLimiter) TryStartTransaction() bool {
	if l == nil || l.maxTransactions <= 0 {
		return true
	}
	next := l.transactions.Add(1)
	if next <= l.maxTransactions {
		return true
	}
	l.cancelIfConfigured()
	return false
}

func (l *ExecutionGoalLimiter) cancelIfConfigured() {
	if l != nil && l.cancel != nil {
		l.cancel()
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
