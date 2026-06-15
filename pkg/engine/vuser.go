package engine

import (
	"context"
	"log/slog"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

type VUser struct {
	ID                   int
	TotalVUsers          int
	Group                string
	Proto                protocol.Protocol
	Script               *dsl.Script
	Metrics              *MetricsCollector
	ResultSink           ResultSink
	Variables            map[string]string
	DataProvider         *DataProvider
	Parameters           *ParameterRuntime
	SecretResolver       SecretResolver
	RedactionConfig      *RedactionConfig
	SnapshotStore        *SnapshotStore
	PauseControl         PauseController
	IterationRateLimiter *TokenBucket
	Rendezvous           *RendezvousCoordinator
	ExecutionGoals       *ExecutionGoalLimiter
	// HardStopCtx, when set, is cancelled only on hard stops (duration
	// timeout, explicit stop/abort) — not when ExecutionGoals exhausts a
	// shared goal. An iteration that already claimed a goal reservation runs
	// under this context so its transaction completes and is recorded instead
	// of being lost when another VUser trips the goal-exhaustion cancel.
	// When nil, the context passed to Run governs everything.
	HardStopCtx  context.Context
	SkipInit     bool
	SkipTeardown bool
}

type PauseController interface {
	WaitIfPaused(ctx context.Context) error
}

var templatePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// SnapshotStore collects error snapshots for post-test analysis.
type SnapshotStore struct {
	snapshots []*protocol.ErrorSnapshot
	maxSize   int
}

func NewSnapshotStore(maxSize int) *SnapshotStore {
	return &SnapshotStore{
		snapshots: make([]*protocol.ErrorSnapshot, 0),
		maxSize:   maxSize,
	}
}

func (ss *SnapshotStore) Add(snap *protocol.ErrorSnapshot) {
	if ss == nil || snap == nil {
		return
	}
	if len(ss.snapshots) < ss.maxSize {
		ss.snapshots = append(ss.snapshots, snap)
	}
}

func (ss *SnapshotStore) Snapshots() []*protocol.ErrorSnapshot {
	if ss == nil {
		return nil
	}
	return ss.snapshots
}

func (vu *VUser) Run(ctx context.Context) {
	cfg := vu.Script.Config
	logCfg := cfg.Log
	logger := vu.makeLogger(logCfg)

	vu.prepareRuntimeState()

	// --- Init phase ---
	if !vu.SkipInit {
		if ok := vu.runLifecycleActions(ctx, vu.Script.Init, "init", false, logger, logCfg, cfg.ErrorHandling); !ok {
			return
		}
	}

	// --- Build pacing config ---
	pacingCfg := PacingRuntimeConfig{
		Type:          cfg.Pacing.Type,
		DelayMs:       cfg.Pacing.DelayMs,
		MinDelayMs:    cfg.Pacing.MinDelayMs,
		MaxDelayMs:    cfg.Pacing.MaxDelayMs,
		IntervalMs:    cfg.Pacing.IntervalMs,
		MinIntervalMs: cfg.Pacing.MinIntervalMs,
		MaxIntervalMs: cfg.Pacing.MaxIntervalMs,
		TPS:           cfg.Pacing.TPS,
		RateLimiter:   vu.IterationRateLimiter,
	}

	// --- Build think time config ---
	globalThinkCfg := protocol.ThinkTimeConfig{
		Type:       cfg.ThinkTime.Type,
		MeanMs:     cfg.ThinkTime.MeanMs,
		StdMs:      cfg.ThinkTime.StdMs,
		MinMs:      cfg.ThinkTime.MinMs,
		MaxMs:      cfg.ThinkTime.MaxMs,
		Multiplier: cfg.ThinkTime.Multiplier,
		LimitMs:    cfg.ThinkTime.LimitMs,
		Ignore:     cfg.ThinkTime.Ignore,
	}

	autoTx := normalizeAutoTransactionMode(cfg.RunLogic.AutoTransactions)
	actionTxMode := autoTx == "per_action"
	iterationTxMode := autoTx == "per_iteration" || (autoTx == "none" && !scriptHasExplicitTransactions(vu.Script))
	maxIter := cfg.RunLogic.Iterations
	continueOnError := cfg.ErrorHandling.ContinueOnError

	// ctx (the value passed to Run) stops the loop from starting new iterations;
	// it is cancelled when a shared execution goal exhausts (soft stop) and on
	// per-VUser scale/ramp-down. iterCtx governs the body of an iteration once it
	// holds a goal reservation: it is set to HardStopCtx so a claimed reservation
	// always completes and is recorded on a goal-exhaustion soft stop, while
	// still being interrupted on scale/ramp-down and hard stops. In the
	// MultiGroupEngine, HardStopCtx is a per-VUser "shed" context that is a child
	// of the engine base context (so hard stops cancel it) and is cancelled
	// explicitly on scale/ramp-down — but NOT by goal exhaustion.
	iterCtx := ctx
	if vu.HardStopCtx != nil {
		iterCtx = vu.HardStopCtx
	}

	applyActionThinkTime := func(action dsl.Action) {
		if !action.ThinkTimeAfter {
			return
		}
		thinkCfg := globalThinkCfg
		if action.ThinkTime.Type != "" {
			thinkCfg = protocol.ThinkTimeConfig{
				Type:       action.ThinkTime.Type,
				MeanMs:     action.ThinkTime.MeanMs,
				StdMs:      action.ThinkTime.StdMs,
				MinMs:      action.ThinkTime.MinMs,
				MaxMs:      action.ThinkTime.MaxMs,
				Multiplier: action.ThinkTime.Multiplier,
				LimitMs:    action.ThinkTime.LimitMs,
				Ignore:     action.ThinkTime.Ignore,
			}
			// Inherit global multiplier/limit/ignore if not set per-action.
			if thinkCfg.Multiplier == 0 {
				thinkCfg.Multiplier = globalThinkCfg.Multiplier
			}
			if thinkCfg.LimitMs == 0 {
				thinkCfg.LimitMs = globalThinkCfg.LimitMs
			}
			if globalThinkCfg.Ignore {
				thinkCfg.Ignore = true
			}
		}
		applyThinkTimeCtx(iterCtx, thinkCfg)
	}

	// --- Main iteration loop ---
	iteration := 0
	for {
		// Check iteration limit
		if maxIter > 0 && iteration >= maxIter {
			break
		}
		// Must precede the goal reservations below: once a reservation is
		// claimed, this iteration is entitled to run and record.
		if ctx.Err() != nil {
			break
		}
		if vu.ExecutionGoals != nil && !vu.ExecutionGoals.TryStartIteration() {
			break
		}
		if iterationTxMode && vu.ExecutionGoals != nil && !vu.ExecutionGoals.TryStartTransaction() {
			break
		}
		if pacingCfg.Type == "tps" && !pacingCfg.RateLimiter.WaitContext(iterCtx) {
			break
		}

		iterStart := time.Now()

		// Reload data row at the start of each iteration
		if iteration > 0 {
			vu.loadDataRow()
			if vu.Parameters != nil {
				vu.Parameters.RefreshIteration()
			}
			if vu.Script.Protocol == "browser" && cfg.Browser.NewUserPerIter != nil && *cfg.Browser.NewUserPerIter {
				if err := vu.resetProtocolForIteration(iterCtx); err != nil {
					logger.Error("iteration protocol reset failed", "iteration", iteration+1, "err", err)
					goto teardown
				}
			}
		}

		// Track iteration-level transaction
		iterTxStart := time.Now()
		iterTxDuration := time.Duration(0)
		var iterTransactionSource *protocol.Result
		iterTxRecorded := false
		recordIterationTransaction := func(success bool) {
			if !iterationTxMode || iterTxRecorded {
				return
			}
			duration := iterTxDuration
			if duration <= 0 {
				duration = time.Since(iterTxStart)
			}
			var source *protocol.Result
			if !success {
				source = iterTransactionSource
			}
			vu.recordTransaction(vu.iterationTransactionName(), duration, success, source)
			iterTxRecorded = true
		}

		iterSuccess := true

		// runOne executes a single action and reports whether the iteration should
		// continue or abort to teardown. runActions drives a slice of actions and is
		// reused recursively for control_if / control_loop block bodies.
		var runActions func(actions []dsl.Action, depth int) actionFlow
		runOne := func(action dsl.Action, depth int) actionFlow {
			if vu.PauseControl != nil {
				if err := vu.PauseControl.WaitIfPaused(iterCtx); err != nil {
					return flowTeardown
				}
			}
			if iterCtx.Err() != nil {
				return flowTeardown
			}

			switch strings.ToLower(strings.TrimSpace(action.Type)) {
			case "control_if":
				if action.Control == nil || depth >= maxControlDepth {
					if depth >= maxControlDepth {
						logger.Error("control flow nesting exceeded", "action", action.Name)
					}
					return flowNext
				}
				body := action.Control.Else
				if vu.evalCondition(action.Control.Condition) {
					body = action.Control.Steps
				}
				if runActions(body, depth+1) == flowTeardown {
					return flowTeardown
				}
				applyActionThinkTime(action)
				return flowNext
			case "control_loop":
				if action.Control == nil || depth >= maxControlDepth {
					if depth >= maxControlDepth {
						logger.Error("control flow nesting exceeded", "action", action.Name)
					}
					return flowNext
				}
				loop := action.Control
				maxPasses := controlLoopMaxPasses(loop)
				fixed := loop.Count > 0
				// Expose a 1-based {{var.loop_index}} to the loop body and the
				// condition (set before evaluation so per-pass bounds like
				// "int(var.loop_index) <= int(var.total)" work). The outer
				// value is restored afterwards so nested loops compose.
				outerLoopIndex, hadOuterLoopIndex := vu.Variables["loop_index"]
				restoreLoopIndex := func() {
					if hadOuterLoopIndex {
						vu.Variables["loop_index"] = outerLoopIndex
					} else {
						delete(vu.Variables, "loop_index")
					}
				}
				for pass := 0; ; pass++ {
					if iterCtx.Err() != nil {
						restoreLoopIndex()
						return flowTeardown
					}
					vu.Variables["loop_index"] = strconv.Itoa(pass + 1)
					if fixed {
						if pass >= loop.Count || pass >= maxPasses {
							break
						}
					} else if pass >= maxPasses || !vu.evalCondition(loop.Condition) {
						break
					}
					if runActions(loop.Steps, depth+1) == flowTeardown {
						restoreLoopIndex()
						return flowTeardown
					}
				}
				restoreLoopIndex()
				applyActionThinkTime(action)
				return flowNext
			}

			if handled, err := vu.handleEngineAction(iterCtx, action); handled {
				if err != nil {
					logger.Error("action error", "action", action.Name, "err", err)
					iterSuccess = false
					if !continueOnError {
						recordIterationTransaction(false)
						return flowTeardown
					}
				}
				return flowNext
			}

			if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
				if vu.ExecutionGoals != nil && !vu.ExecutionGoals.TryStartTransaction() {
					return flowTeardown
				}
				result, err := vu.executeHTTPSequenceAction(iterCtx, action, cfg.ErrorHandling)
				if err != nil {
					logger.Error("action error", "action", action.Name, "err", err)
					iterSuccess = false
					if !continueOnError {
						recordIterationTransaction(false)
						return flowTeardown
					}
					return flowNext
				}
				iterTxDuration += result.Duration
				vu.recordTransaction(action.Name, result.Duration, result.Success, result)
				if !result.Success {
					iterSuccess = false
					iterTransactionSource = result
					if logCfg.OnErrorOnly || logCfg.Level == "extended" {
						logger.Error("action failed", actionLogAttrs(action.Name, vu.resolveAction(action), result, logCfg, true)...)
					}
					if !continueOnError {
						recordIterationTransaction(false)
						return flowTeardown
					}
				} else if logCfg.Level == "extended" {
					logger.Info("action ok", actionLogAttrs(action.Name, vu.resolveAction(action), result, logCfg, false)...)
				}
				applyActionThinkTime(action)
				return flowNext
			}

			resolved := vu.resolveAction(action)
			if actionTxMode && vu.ExecutionGoals != nil && !vu.ExecutionGoals.TryStartTransaction() {
				return flowTeardown
			}

			// Track action-level transaction
			var actionTxStart time.Time
			if actionTxMode {
				actionTxStart = time.Now()
			}

			result, err := vu.executeWithRetry(iterCtx, resolved, cfg.ErrorHandling)
			if err != nil {
				logger.Error("action error", "action", action.Name, "err", err)
				iterSuccess = false
				if actionTxMode {
					vu.recordTransaction(action.Name, time.Since(actionTxStart), false, nil)
				}
				if !continueOnError {
					recordIterationTransaction(false)
					return flowTeardown
				}
				return flowNext
			}

			vu.recordResult(result, cfg.ErrorHandling)
			iterTxDuration += result.Duration

			// Record auto-transaction for this action
			if actionTxMode {
				vu.recordTransaction(action.Name, time.Since(actionTxStart), result.Success, result)
			}

			if !result.Success {
				iterSuccess = false
				iterTransactionSource = result
				if logCfg.OnErrorOnly || logCfg.Level == "extended" {
					logger.Error("action failed", actionLogAttrs(action.Name, resolved, result, logCfg, true)...)
				}
				if !continueOnError {
					recordIterationTransaction(false)
					return flowTeardown
				}
			} else if logCfg.Level == "extended" {
				logger.Info("action ok", actionLogAttrs(action.Name, resolved, result, logCfg, false)...)
			}

			applyActionThinkTime(action)
			return flowNext
		}
		runActions = func(actions []dsl.Action, depth int) actionFlow {
			for _, action := range actions {
				if runOne(action, depth) == flowTeardown {
					return flowTeardown
				}
			}
			return flowNext
		}

		if runActions(vu.Script.Actions, 0) == flowTeardown {
			goto teardown
		}

		// Record iteration-level transaction
		recordIterationTransaction(iterSuccess)

		iteration++

		// Apply pacing between iterations (interruptible: a scale/ramp-down or
		// hard stop cuts the pacing sleep short instead of holding load).
		applyIterationPacingCtx(iterCtx, pacingCfg, iterStart)
	}

teardown:
	if !vu.SkipTeardown {
		vu.runLifecycleActions(context.Background(), vu.Script.Teardown, "teardown", true, logger, logCfg, cfg.ErrorHandling)
	}
}

func (vu *VUser) RunLifecycleBlock(ctx context.Context, actions []dsl.Action, phase string) bool {
	if vu == nil || vu.Script == nil {
		return true
	}
	cfg := vu.Script.Config
	logCfg := cfg.Log
	vu.prepareRuntimeState()
	return vu.runLifecycleActions(ctx, actions, phase, false, vu.makeLogger(logCfg), logCfg, cfg.ErrorHandling)
}

func (vu *VUser) prepareRuntimeState() {
	if vu.Variables == nil {
		vu.Variables = make(map[string]string)
	}
	vu.Parameters = NewParameterRuntime(vu.Script.Parameters, vu.DataProvider, vu.ID)
	if vu.Parameters != nil {
		vu.Parameters.Prime()
	}
	vu.loadDataRow()
}

func (vu *VUser) runLifecycleActions(ctx context.Context, actions []dsl.Action, phase string, ignoreErrors bool, logger *slog.Logger, logCfg dsl.LogConfig, errCfg dsl.ErrorHandlingConfig) bool {
	var runActions func(actions []dsl.Action, depth int) bool
	runOne := func(action dsl.Action, depth int) bool {
		if vu.PauseControl != nil {
			if err := vu.PauseControl.WaitIfPaused(ctx); err != nil {
				return false
			}
		}
		if ctx.Err() != nil {
			return false
		}
		switch strings.ToLower(strings.TrimSpace(action.Type)) {
		case "control_if":
			if action.Control == nil {
				return true
			}
			if depth >= maxControlDepth {
				logger.Error(phase+" control flow nesting exceeded", "action", action.Name)
				return true
			}
			body := action.Control.Else
			if vu.evalCondition(action.Control.Condition) {
				body = action.Control.Steps
			}
			return runActions(body, depth+1)
		case "control_loop":
			if action.Control == nil {
				return true
			}
			if depth >= maxControlDepth {
				logger.Error(phase+" control flow nesting exceeded", "action", action.Name)
				return true
			}
			loop := action.Control
			maxPasses := controlLoopMaxPasses(loop)
			fixed := loop.Count > 0
			// Mirror the main iteration loop: expose a 1-based
			// {{var.loop_index}} to the body and the per-pass condition,
			// restoring any outer value afterwards.
			outerLoopIndex, hadOuterLoopIndex := vu.Variables["loop_index"]
			restoreLoopIndex := func() {
				if hadOuterLoopIndex {
					vu.Variables["loop_index"] = outerLoopIndex
				} else {
					delete(vu.Variables, "loop_index")
				}
			}
			for pass := 0; ; pass++ {
				if ctx.Err() != nil {
					restoreLoopIndex()
					return false
				}
				vu.Variables["loop_index"] = strconv.Itoa(pass + 1)
				if fixed {
					if pass >= loop.Count || pass >= maxPasses {
						break
					}
				} else if pass >= maxPasses || !vu.evalCondition(loop.Condition) {
					break
				}
				if !runActions(loop.Steps, depth+1) {
					restoreLoopIndex()
					return false
				}
			}
			restoreLoopIndex()
			return true
		}
		if handled, err := vu.handleEngineAction(ctx, action); handled {
			if err != nil {
				logger.Error(phase+" action failed", "action", action.Name, "err", err)
				if !ignoreErrors {
					return false
				}
			}
			return true
		}
		resolved := vu.resolveAction(action)
		result, err := vu.executeWithRetry(ctx, resolved, errCfg)
		if err != nil {
			logger.Error(phase+" action failed", "action", action.Name, "err", err)
			if !ignoreErrors {
				return false
			}
			return true
		}
		vu.recordResult(result, errCfg)
		if result != nil && !result.Success {
			if logCfg.OnErrorOnly || logCfg.Level == "extended" {
				logger.Error(phase+" action unsuccessful", "action", action.Name, "status", result.StatusCode, "error", result.ErrorMessage)
			} else {
				logger.Warn(phase+" action unsuccessful", "action", action.Name, "error", result.ErrorMessage)
			}
			if !ignoreErrors {
				return false
			}
		}
		return true
	}
	runActions = func(actions []dsl.Action, depth int) bool {
		for _, action := range actions {
			if !runOne(action, depth) {
				return false
			}
		}
		return true
	}
	return runActions(actions, 0)
}

func (vu *VUser) resetProtocolForIteration(ctx context.Context) error {
	if vu == nil || vu.Proto == nil || vu.Script == nil {
		return nil
	}
	if err := vu.Proto.Teardown(context.Background()); err != nil {
		return err
	}
	return vu.Proto.Init(ctx, buildProtoConfig(vu.Script))
}
