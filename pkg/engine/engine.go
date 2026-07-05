// Package engine implements the core VUser (virtual user) execution engine.
//
// The engine manages a pool of goroutines (one per VUser), schedules their execution
// via a pacing algorithm (ramp-up, steady-state), and aggregates metrics in a
// zero-lock hot path using buffered channels.
//
// Key design:
//   - 1 VUser = 1 goroutine (~2–4 KB stack); supports 10k+ VUsers per pod
//   - Metrics use buffered channels (cap 10,000) to avoid lock contention
//   - sync.Pool reuses request/response buffers to reduce GC pressure
//   - T-digest for streaming percentile (p50/p95/p99) computation
//
// Example usage:
//
//	cfg := engine.Config{
//	    Script:   script,
//	    VUsers:   100,
//	    Duration: 5 * time.Minute,
//	    RampUp:   30 * time.Second,
//	}
//	eng, err := engine.New(cfg)
//	if err != nil {
//	    log.Fatal(err)
//	}
//	result, err := eng.Run(ctx)
package engine

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// Engine orchestrates VUser goroutines and metrics collection.
//
// The engine manages the full lifecycle of a load test:
//   - Init phase: Protocol initialization and setup actions
//   - Ramp-up phase: Gradual increase of active VUsers per pacing config
//   - Steady-state phase: All VUsers running; actions repeated until duration expires
//   - Teardown phase: Cleanup actions, graceful shutdown
//
// Metrics are collected asynchronously via buffered channels to minimize
// contention. Call MetricsSnapshot() periodically (e.g., every second) to
// extract aggregated metrics during or after a run.
type Engine struct {
	script   *dsl.Script
	vusers   int
	duration time.Duration
	rampUp   time.Duration
	metrics  *MetricsCollector
	mu       sync.Mutex // guards cancel
	cancel   context.CancelFunc
	softStop context.CancelFunc
}

// Config contains configuration for Engine creation.
type Config struct {
	Script   *dsl.Script   // Test script (required)
	VUsers   int           // Number of virtual users (required, > 0)
	Duration time.Duration // Total test duration (required, > 0)
	RampUp   time.Duration // Ramp-up period (can be 0 for immediate start)
}

// RunResult contains final test results after execution completes.
type RunResult struct {
	Duration          time.Duration   // Actual test duration
	TotalTransactions int64           // Total completed transactions across all VUsers
	TotalErrors       int64           // Total failed transactions
	FinalSnapshot     MetricsSnapshot // Aggregated metrics at end of run
}

// New creates a new Engine with the given config.
//
// Validation:
//   - script must not be nil
//   - vusers must be > 0
//   - duration must be > 0
//
// The returned engine is not yet running; call Run() to start it.
func New(cfg Config) (*Engine, error) {
	if cfg.Script == nil {
		return nil, fmt.Errorf("script is required")
	}
	if cfg.VUsers <= 0 {
		return nil, fmt.Errorf("vusers must be positive")
	}
	if cfg.Duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}
	return &Engine{
		script:   cfg.Script,
		vusers:   cfg.VUsers,
		duration: cfg.Duration,
		rampUp:   cfg.RampUp,
		metrics:  NewMetricsCollector(10000),
	}, nil
}

func (e *Engine) Run(ctx context.Context) (*RunResult, error) {
	ctx, cancel := context.WithTimeout(ctx, e.duration)
	e.mu.Lock()
	e.cancel = cancel
	e.mu.Unlock()
	defer cancel()

	proto, err := protocol.Get(e.script.Protocol)
	if err != nil {
		return nil, fmt.Errorf("protocol init: %w", err)
	}

	protoConfig := buildProtoConfigForRunMode(e.script, "load")
	if err := proto.HealthCheck(ctx); err != nil {
		slog.Warn("health check failed, proceeding anyway", "err", err)
	}

	e.metrics.Start(time.Second)
	defer e.metrics.Stop()

	iterationRateLimiter := newIterationRateLimiter(e.script)
	if iterationRateLimiter != nil {
		e.metrics.SetTargetTPS(e.script.Config.Pacing.TPS)
		defer iterationRateLimiter.Stop()
	}
	if isOpenWorkload(e.script) {
		loopCtx, loopCancel := context.WithCancel(ctx)
		e.mu.Lock()
		e.softStop = loopCancel
		e.mu.Unlock()
		defer func() {
			loopCancel()
			e.mu.Lock()
			e.softStop = nil
			e.mu.Unlock()
		}()
		return e.runOpenWorkload(loopCtx, protoConfig)
	}

	// Execution goals cancel only loopCtx so VUsers stop claiming new
	// iterations; iterations already holding a reservation finish under ctx
	// (hard stop: duration timeout or Stop) and record their transactions.
	loopCtx, loopCancel := context.WithCancel(ctx)
	defer loopCancel()
	var executionGoals *ExecutionGoalLimiter
	if hasExecutionGoals(e.script.Config.RunLogic) {
		executionGoals = NewExecutionGoalLimiter(e.script.Config.RunLogic, loopCancel)
	}
	e.mu.Lock()
	e.softStop = loopCancel
	e.mu.Unlock()
	defer func() {
		e.mu.Lock()
		e.softStop = nil
		e.mu.Unlock()
	}()

	start := time.Now()
	var wg sync.WaitGroup
	rendezvous := NewRendezvousCoordinator()

	rampDelay := e.rampDelay()

	for i := 0; i < e.vusers; i++ {
		if loopCtx.Err() != nil {
			break
		}

		if i > 0 && rampDelay > 0 {
			time.Sleep(rampDelay)
		}

		wg.Add(1)
		vuserID := i

		vuProto, err := protocol.Get(e.script.Protocol)
		if err != nil {
			slog.Error("failed to create protocol for vuser", "vuser", vuserID, "err", err)
			wg.Done()
			continue
		}
		if err := vuProto.Init(ctx, vuserProtoConfig(protoConfig, vuserID)); err != nil {
			slog.Error("failed to init protocol for vuser", "vuser", vuserID, "err", err)
			wg.Done()
			continue
		}

		e.metrics.SetActiveVUsers(int64(i + 1))

		go func() {
			defer recoverVUserGoroutine(vuserID)
			defer wg.Done()
			defer runProtocolTeardown(vuProto)

			vu := &VUser{
				ID:                   vuserID,
				TotalVUsers:          e.vusers,
				Proto:                vuProto,
				Script:               e.script,
				Metrics:              e.metrics,
				IterationRateLimiter: iterationRateLimiter,
				Rendezvous:           rendezvous,
				ExecutionGoals:       executionGoals,
				HardStopCtx:          ctx,
			}
			vu.Run(loopCtx)
		}()
	}

	wg.Wait()
	elapsed := time.Since(start)

	result := &RunResult{
		Duration:          elapsed,
		TotalTransactions: e.metrics.TotalTransactions(),
		TotalErrors:       e.metrics.TotalErrors(),
	}
	if snap, ok := e.metrics.LatestSnapshot(); ok {
		result.FinalSnapshot = snap
	}
	return result, nil
}

func (e *Engine) Stop() {
	e.mu.Lock()
	cancel := e.cancel
	e.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (e *Engine) GracefulStop() {
	e.mu.Lock()
	cancel := e.softStop
	e.mu.Unlock()
	if cancel != nil {
		cancel()
		return
	}
	e.Stop()
}

func (e *Engine) LatestSnapshot() (MetricsSnapshot, bool) {
	return e.metrics.LatestSnapshot()
}

func (e *Engine) rampDelay() time.Duration {
	if e.rampUp <= 0 || e.vusers <= 1 {
		return 0
	}
	return e.rampUp / time.Duration(e.vusers)
}

func (e *Engine) runOpenWorkload(ctx context.Context, protoConfig map[string]any) (*RunResult, error) {
	start := time.Now()
	iterationScript := cloneSingleIterationScript(e.script)
	arrivals, bounded := boundedArrivalLimit(e.script, e.vusers)
	loopCtx := ctx
	var executionGoals *ExecutionGoalLimiter
	if hasExecutionGoals(e.script.Config.RunLogic) {
		var goalCancel context.CancelFunc
		loopCtx, goalCancel = context.WithCancel(ctx)
		defer goalCancel()
		executionGoals = NewExecutionGoalLimiter(e.script.Config.RunLogic, goalCancel)
	}

	var wg sync.WaitGroup
	var activeWorkers atomic.Int64
	setActive := func(delta int64) {
		e.metrics.SetActiveVUsers(activeWorkers.Add(delta))
	}

	inFlightLimit := openWorkloadInFlightLimit(e.script)
	limiter := newInFlightLimiter(inFlightLimit)

	runArrivalScheduler(loopCtx, e.script.Config.Pacing.TPS, func() int {
		if bounded {
			return arrivals
		}
		return 0
	}(), nil, func(arrival int) {
		// Shed the arrival when the in-flight set is saturated: the SUT cannot
		// keep up, so dropping it protects the generator from unbounded
		// goroutine growth instead of silently throttling to a closed model.
		if !limiter.tryAcquire() {
			e.metrics.RecordSheddedArrival()
			if limiter.shouldWarn() {
				slog.Warn("open workload shedding arrivals: system under test cannot keep up; capping in-flight workers to protect the generator", "max_in_flight", inFlightLimit, "tps", e.script.Config.Pacing.TPS)
			}
			return
		}
		wg.Add(1)
		setActive(1)

		go func(workerID int) {
			defer recoverVUserGoroutine(workerID)
			defer wg.Done()
			defer limiter.release()
			defer func() { setActive(-1) }()

			vuProto, err := protocol.Get(e.script.Protocol)
			if err != nil {
				slog.Error("failed to create protocol for arrival worker", "arrival", workerID, "err", err)
				return
			}
			if err := vuProto.Init(ctx, vuserProtoConfig(protoConfig, workerID)); err != nil {
				slog.Error("failed to init protocol for arrival worker", "arrival", workerID, "err", err)
				return
			}
			defer runProtocolTeardown(vuProto)

			vu := &VUser{
				ID:             workerID,
				Proto:          vuProto,
				Script:         iterationScript,
				Metrics:        e.metrics,
				ExecutionGoals: executionGoals,
				HardStopCtx:    ctx,
			}
			vu.Run(loopCtx)
		}(arrival)
	})

	if shed := limiter.Shed(); shed > 0 {
		slog.Warn("open workload shed arrivals over the run (offered TPS exceeded achievable throughput)", "shed", shed, "max_in_flight", inFlightLimit)
	}

	wg.Wait()
	elapsed := time.Since(start)
	result := &RunResult{
		Duration:          elapsed,
		TotalTransactions: e.metrics.TotalTransactions(),
		TotalErrors:       e.metrics.TotalErrors(),
	}
	if snap, ok := e.metrics.LatestSnapshot(); ok {
		result.FinalSnapshot = snap
	}
	return result, nil
}

func newIterationRateLimiter(script *dsl.Script) *TokenBucket {
	if script == nil || script.Config.Pacing.Type != "tps" || script.Config.Pacing.TPS <= 0 {
		return nil
	}
	return NewTokenBucket(script.Config.Pacing.TPS)
}
