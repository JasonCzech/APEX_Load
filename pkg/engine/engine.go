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

type Engine struct {
	script    *dsl.Script
	vusers    int
	duration  time.Duration
	rampUp    time.Duration
	metrics   *MetricsCollector
	cancel    context.CancelFunc
}

type Config struct {
	Script   *dsl.Script
	VUsers   int
	Duration time.Duration
	RampUp   time.Duration
}

type RunResult struct {
	Duration          time.Duration
	TotalTransactions int64
	TotalErrors       int64
	FinalSnapshot     MetricsSnapshot
}

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
