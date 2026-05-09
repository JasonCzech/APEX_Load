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

type GroupConfig struct {
	Name           string
	Script         *dsl.Script
	VUsers         int
	RampUp         time.Duration
	RampDown       time.Duration
	InitStrategy   string // "at_runtime", "at_once"
}

type MultiGroupConfig struct {
	Groups   []GroupConfig
	Duration time.Duration
}

type MultiGroupEngine struct {
	groups   []GroupConfig
	duration time.Duration
	metrics  *MetricsCollector
	cancel   context.CancelFunc
}

type GroupResult struct {
	Name              string
	TotalTransactions int64
	TotalErrors       int64
}

type MultiGroupRunResult struct {
	Duration          time.Duration
	TotalTransactions int64
	TotalErrors       int64
	FinalSnapshot     MetricsSnapshot
	Groups            []GroupResult
}

func NewMultiGroup(cfg MultiGroupConfig) (*MultiGroupEngine, error) {
	if len(cfg.Groups) == 0 {
		return nil, fmt.Errorf("at least one group is required")
	}
	if cfg.Duration <= 0 {
		return nil, fmt.Errorf("duration must be positive")
	}
	for i, g := range cfg.Groups {
		if g.Script == nil {
			return nil, fmt.Errorf("group[%d] %q: script is required", i, g.Name)
		}
		if g.VUsers <= 0 {
			return nil, fmt.Errorf("group[%d] %q: vusers must be positive", i, g.Name)
		}
		if g.Name == "" {
			cfg.Groups[i].Name = g.Script.Name
		}
	}
	return &MultiGroupEngine{
		groups:   cfg.Groups,
		duration: cfg.Duration,
		metrics:  NewMetricsCollector(10000),
	}, nil
}

func (mge *MultiGroupEngine) Run(ctx context.Context) (*MultiGroupRunResult, error) {
	mge.metrics.Start(time.Second)
	defer mge.metrics.Stop()

	// 1. Pre-initialization Phase (if strategy is "at_once")
	var initWg sync.WaitGroup
	for _, group := range mge.groups {
		if group.InitStrategy != "at_once" {
			continue
		}
		_ = buildProtoConfig(group.Script)
		for i := 0; i < group.VUsers; i++ {
			initWg.Add(1)
			go func(grp GroupConfig) {
				defer initWg.Done()
				// Pre-initializing protocol instances would go here in a pooled model.
			}(group)
		}
	}
	initWg.Wait()

	start := time.Now()
	var wg sync.WaitGroup
	groupResults := make([]GroupResult, len(mge.groups))

	// Determine max ramp down to extend the total duration
	maxRampDown := time.Duration(0)
	for _, g := range mge.groups {
		if g.RampDown > maxRampDown {
			maxRampDown = g.RampDown
		}
	}

	// totalCtx covers the entire execution (Duration + RampDown)
	totalCtx, totalCancel := context.WithTimeout(ctx, mge.duration+maxRampDown)
	mge.cancel = totalCancel
	defer totalCancel()

	// steadyStateCtx signals the end of the steady state period
	steadyStateCtx, steadyStateCancel := context.WithTimeout(totalCtx, mge.duration)
	defer steadyStateCancel()

	vuserOffset := 0
	for gi, group := range mge.groups {
		gi, group := gi, group
		groupResults[gi] = GroupResult{Name: group.Name}

		protoConfig := buildProtoConfig(group.Script)

		rampDelay := time.Duration(0)
		if group.RampUp > 0 && group.VUsers > 1 {
			rampDelay = group.RampUp / time.Duration(group.VUsers)
		}

		rampDownDelay := time.Duration(0)
		if group.RampDown > 0 && group.VUsers > 1 {
			rampDownDelay = group.RampDown / time.Duration(group.VUsers)
		}

		for i := 0; i < group.VUsers; i++ {
			if totalCtx.Err() != nil {
				break
			}

			if i > 0 && rampDelay > 0 {
				time.Sleep(rampDelay)
			}

			wg.Add(1)
			vuserID := vuserOffset + i

			// Create a per-VUser context that respects its individual ramp-down time
			vuCtx, vuCancel := context.WithCancel(totalCtx)
			
			// Handle Ramp-Down for this specific VUser
			go func(idx int, cancelFunc context.CancelFunc) {
				<-steadyStateCtx.Done()
				if rampDownDelay > 0 {
					time.Sleep(time.Duration(idx) * rampDownDelay)
				}
				cancelFunc()
			}(i, vuCancel)

			vuProto, err := protocol.Get(group.Script.Protocol)
			if err != nil {
				slog.Error("failed to create protocol", "group", group.Name, "vuser", vuserID, "err", err)
				vuCancel()
				wg.Done()
				continue
			}
			if err := vuProto.Init(vuCtx, protoConfig); err != nil {
				slog.Error("failed to init protocol", "group", group.Name, "vuser", vuserID, "err", err)
				vuCancel()
				wg.Done()
				continue
			}

			mge.metrics.SetActiveVUsers(int64(vuserOffset + i + 1))

			go func(grp GroupConfig, p protocol.Protocol, vCtx context.Context, vCancel context.CancelFunc) {
				defer wg.Done()
				defer vCancel()
				defer p.Teardown(context.Background())

				vu := &VUser{
					ID:      vuserID,
					Group:   grp.Name,
					Proto:   p,
					Script:  grp.Script,
					Metrics: mge.metrics,
				}
				vu.Run(vCtx)
			}(group, vuProto, vuCtx, vuCancel)
		}

		vuserOffset += group.VUsers
	}

	wg.Wait()
	elapsed := time.Since(start)

	result := &MultiGroupRunResult{
		Duration:          elapsed,
		TotalTransactions: mge.metrics.TotalTransactions(),
		TotalErrors:       mge.metrics.TotalErrors(),
		Groups:            groupResults,
	}
	if snap, ok := mge.metrics.LatestSnapshot(); ok {
		result.FinalSnapshot = snap
	}
	return result, nil
}

func (mge *MultiGroupEngine) Stop() {
	if mge.cancel != nil {
		mge.cancel()
	}
}

func (mge *MultiGroupEngine) LatestSnapshot() (MetricsSnapshot, bool) {
	return mge.metrics.LatestSnapshot()
}

func (mge *MultiGroupEngine) GroupNames() []string {
	names := make([]string, len(mge.groups))
	for i, g := range mge.groups {
		names[i] = g.Name
	}
	return names
}
