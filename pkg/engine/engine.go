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
	"time"

	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
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
	cancel   context.CancelFunc
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
	e.cancel = cancel
	defer cancel()

	proto, err := protocol.Get(e.script.Protocol)
	if err != nil {
		return nil, fmt.Errorf("protocol init: %w", err)
	}

	protoConfig := buildProtoConfig(e.script)
	if err := proto.HealthCheck(ctx); err != nil {
		slog.Warn("health check failed, proceeding anyway", "err", err)
	}

	e.metrics.Start(time.Second)
	defer e.metrics.Stop()

	start := time.Now()
	var wg sync.WaitGroup

	rampDelay := e.rampDelay()

	for i := 0; i < e.vusers; i++ {
		if ctx.Err() != nil {
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
		if err := vuProto.Init(ctx, protoConfig); err != nil {
			slog.Error("failed to init protocol for vuser", "vuser", vuserID, "err", err)
			wg.Done()
			continue
		}

		e.metrics.SetActiveVUsers(int64(i + 1))

		go func() {
			defer wg.Done()
			defer vuProto.Teardown(context.Background())

			vu := &VUser{
				ID:      vuserID,
				Proto:   vuProto,
				Script:  e.script,
				Metrics: e.metrics,
			}
			vu.Run(ctx)
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
	if e.cancel != nil {
		e.cancel()
	}
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
