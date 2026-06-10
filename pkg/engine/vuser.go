package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
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
	SkipInit             bool
	SkipTeardown         bool
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
		applyThinkTime(thinkCfg)
	}

	// --- Main iteration loop ---
	iteration := 0
	for {
		// Check iteration limit
		if maxIter > 0 && iteration >= maxIter {
			break
		}
		if vu.ExecutionGoals != nil && !vu.ExecutionGoals.TryStartIteration() {
			break
		}
		if ctx.Err() != nil {
			break
		}
		if iterationTxMode && vu.ExecutionGoals != nil && !vu.ExecutionGoals.TryStartTransaction() {
			break
		}
		if pacingCfg.Type == "tps" && !pacingCfg.RateLimiter.WaitContext(ctx) {
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
				if err := vu.resetProtocolForIteration(ctx); err != nil {
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
				if err := vu.PauseControl.WaitIfPaused(ctx); err != nil {
					return flowTeardown
				}
			}
			if ctx.Err() != nil {
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
				for pass := 0; ; pass++ {
					if ctx.Err() != nil {
						return flowTeardown
					}
					if fixed {
						if pass >= loop.Count || pass >= maxPasses {
							break
						}
					} else if pass >= maxPasses || !vu.evalCondition(loop.Condition) {
						break
					}
					if runActions(loop.Steps, depth+1) == flowTeardown {
						return flowTeardown
					}
				}
				applyActionThinkTime(action)
				return flowNext
			}

			if handled, err := vu.handleEngineAction(ctx, action); handled {
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
				result, err := vu.executeHTTPSequenceAction(ctx, action, cfg.ErrorHandling)
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

			result, err := vu.executeWithRetry(ctx, resolved, cfg.ErrorHandling)
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

		// Apply pacing between iterations
		applyIterationPacing(pacingCfg, iterStart)
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
			for pass := 0; ; pass++ {
				if ctx.Err() != nil {
					return false
				}
				if fixed {
					if pass >= loop.Count || pass >= maxPasses {
						break
					}
				} else if pass >= maxPasses || !vu.evalCondition(loop.Condition) {
					break
				}
				if !runActions(loop.Steps, depth+1) {
					return false
				}
			}
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

func (vu *VUser) recordResult(result *protocol.Result, errCfg dsl.ErrorHandlingConfig) {
	if result == nil {
		return
	}
	result.Group = vu.Group
	if result.NetworkProfile == "" && vu.Script != nil {
		result.NetworkProfile = networkProfileLabel(vu.Script.Config.WebRuntime.NetworkSimulation)
	}
	vu.Metrics.Record(result)
	vu.mergeExtractions(result)
	scrubSensitiveResult(result, vu.RedactionConfig)
	vu.handleSnapshot(result, errCfg)
	if vu.ResultSink != nil {
		_ = vu.ResultSink.RecordResult(result)
	}
}

func (vu *VUser) recordTransaction(name string, duration time.Duration, success bool, source *protocol.Result) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "transaction"
	}
	if duration < 0 {
		duration = 0
	}
	tx := &protocol.Result{
		ActionName:          name,
		BusinessTransaction: true,
		Group:               vu.Group,
		StartTime:           time.Now().Add(-duration),
		Duration:            duration,
		Success:             success,
	}
	if source != nil {
		tx.StartTime = source.StartTime
		if tx.StartTime.IsZero() {
			tx.StartTime = time.Now().Add(-duration)
		}
		tx.Region = source.Region
		tx.CorrelationID = source.CorrelationID
		tx.NetworkProfile = source.NetworkProfile
		tx.DependencyType = source.DependencyType
		tx.DependencyTarget = source.DependencyTarget
		tx.DNSTime = source.DNSTime
		tx.ConnectTime = source.ConnectTime
		tx.TLSTime = source.TLSTime
		tx.TTFB = source.TTFB
		tx.BytesSent = source.BytesSent
		tx.BytesReceived = source.BytesReceived
		tx.StatusCode = source.StatusCode
		tx.ErrorMessage = source.ErrorMessage
		tx.Extractions = source.Extractions
		tx.ServerTelemetry = source.ServerTelemetry
		tx.SourceStepID = source.SourceStepID
		tx.SourceStepIndex = source.SourceStepIndex
		tx.SourceAction = source.SourceAction
		tx.SourceActionName = source.SourceActionName
		tx.SourceTransaction = source.SourceTransaction
		tx.SourceProfile = source.SourceProfile
	}
	if !success && tx.ErrorMessage == "" {
		tx.ErrorMessage = "transaction failed"
	}
	if vu.Metrics != nil {
		vu.Metrics.RecordTransactionResult(tx)
	}
	if sink, ok := vu.ResultSink.(TransactionSink); ok {
		_ = sink.RecordTransaction(tx)
	}
}

func (vu *VUser) iterationTransactionName() string {
	if vu != nil && vu.Script != nil {
		if name := strings.TrimSpace(vu.Script.Name); name != "" {
			return name
		}
	}
	return "script_iteration"
}

func normalizeAutoTransactionMode(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "none"
	}
	return value
}

func scriptHasExplicitTransactions(script *dsl.Script) bool {
	if script == nil {
		return false
	}
	for _, action := range script.Actions {
		switch dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) {
		case "source_transaction", "transaction":
			return true
		case "http_sequence":
			if sourceTransactionName(action.Metadata) != "" {
				return true
			}
		}
	}
	return false
}

// sourceTransactionName extracts the transaction name from action or result
// metadata, accepting the legacy "loadrunner_transaction" key so scripts
// persisted before the enterprise-archive rename keep their transaction
// grouping.
func sourceTransactionName(metadata map[string]any) string {
	if name := sourceMetadataString(metadata, "source_transaction"); name != "" {
		return name
	}
	return sourceMetadataString(metadata, "loadrunner_transaction")
}

func networkProfileLabel(config dsl.NetworkSimulationConfig) string {
	switch strings.TrimSpace(config.Mode) {
	case "preset":
		if config.Preset != "" {
			return "preset:" + config.Preset
		}
		return "preset"
	case "custom":
		parts := make([]string, 0, 4)
		if config.BandwidthKbps > 0 {
			parts = append(parts, fmt.Sprintf("bandwidth=%dkbps", config.BandwidthKbps))
		}
		if config.LatencyMs > 0 {
			parts = append(parts, fmt.Sprintf("latency=%dms", config.LatencyMs))
		}
		if config.JitterMs > 0 {
			parts = append(parts, fmt.Sprintf("jitter=%dms", config.JitterMs))
		}
		if config.PacketLossPct > 0 {
			parts = append(parts, fmt.Sprintf("loss=%.2g%%", config.PacketLossPct))
		}
		if len(parts) == 0 {
			return "custom"
		}
		return "custom:" + strings.Join(parts, ",")
	case "max":
		return "max"
	default:
		return ""
	}
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

func (vu *VUser) handleEngineAction(ctx context.Context, action dsl.Action) (bool, error) {
	switch dsl.CanonicalActionType(action.Type) {
	case "rendezvous":
		return true, vu.waitAtRendezvous(ctx, action)
	case "error_message":
		vu.logErrorMessageAction(ctx, action)
		return true, nil
	case "source_message":
		vu.handleSourceMessageAction(ctx, action)
		return true, nil
	case "source_runtime":
		return true, vu.handleSourceRuntimeAction(ctx, action)
	case "source_transaction":
		vu.handleSourceTransactionAction(action)
		return true, nil
	case "source_timer":
		vu.handleSourceTimerAction(action)
		return true, nil
	case "source_metric":
		vu.handleSourceMetricAction(ctx, action)
		return true, nil
	case "source_parameter", "source_json", "source_xml", "source_database", "source_secret":
		vu.handleSourceStateAction(ctx, action)
		return true, nil
	case "source_external", "source_review", "custom_call":
		vu.logSourceReviewAction(ctx, action)
		return true, nil
	default:
		return false, nil
	}
}

func (vu *VUser) handleSourceMessageAction(ctx context.Context, action dsl.Action) {
	message := vu.renderErrorMessage(action)
	level, _ := actionParamString(action.Params, "level")
	attrs := []any{
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"message", message,
	}
	if status, _ := actionParamBool(action.Params, "status_message"); status {
		vu.ensureVariables()
		vu.Variables["vuser_status"] = message
	}
	switch level {
	case "debug":
		slog.Default().DebugContext(ctx, "source message", attrs...)
	case "error":
		slog.Default().ErrorContext(ctx, "source message", attrs...)
	default:
		slog.Default().InfoContext(ctx, "source message", attrs...)
	}
}

func (vu *VUser) handleSourceRuntimeAction(ctx context.Context, action dsl.Action) error {
	function := sourceActionFunction(action)
	vu.ensureVariables()
	switch function {
	case "lr_abort":
		return fmt.Errorf("source runtime abort requested by %s", action.Name)
	case "lr_exit":
		vu.Variables["__source_exit_requested"] = strings.Join(sourceActionArgs(action), ",")
	case "lr_disable_ip_spoofing":
		vu.Variables["__lr_ip_spoofing"] = "disabled"
	case "lr_enable_ip_spoofing":
		vu.Variables["__lr_ip_spoofing"] = "enabled"
	case "lr_load_dll":
		vu.logSourceReviewAction(ctx, action)
	default:
		slog.Default().DebugContext(ctx, "source runtime action",
			"vuser", vu.ID,
			"group", vu.Group,
			"action", action.Name,
			"function", function,
		)
	}
	return nil
}

func (vu *VUser) handleSourceTransactionAction(action dsl.Action) {
	function := sourceActionFunction(action)
	args := sourceActionArgs(action)
	name := firstEngineValue(action.Name, args...)
	vu.ensureVariables()
	switch {
	case strings.Contains(function, "start"):
		vu.Variables[sourceTransactionStartKey(name)] = strconv.FormatInt(time.Now().UnixNano(), 10)
	case strings.Contains(function, "end"), strings.Contains(function, "stop"), function == "lr_fail_trans_with_error":
		success := !strings.Contains(function, "fail")
		if len(args) > 1 {
			status := strings.ToLower(args[len(args)-1])
			if strings.Contains(status, "fail") {
				success = false
			}
		}
		start := time.Now()
		if raw := vu.Variables[sourceTransactionStartKey(name)]; raw != "" {
			if nanos, err := strconv.ParseInt(raw, 10, 64); err == nil {
				start = time.Unix(0, nanos)
			}
		}
		vu.recordTransaction(name, time.Since(start), success, nil)
		vu.Variables[sourceTransactionStatusKey(name)] = strconv.FormatBool(success)
	case strings.Contains(function, "status"):
		if len(args) > 1 {
			vu.Variables[sourceTransactionStatusKey(args[0])] = args[1]
		}
	}
}

func (vu *VUser) handleSourceTimerAction(action dsl.Action) {
	function := sourceActionFunction(action)
	args := sourceActionArgs(action)
	name := firstEngineValue(action.Name, args...)
	vu.ensureVariables()
	switch function {
	case "lr_start_timer":
		vu.Variables[sourceTimerStartKey(name)] = strconv.FormatInt(time.Now().UnixNano(), 10)
	case "lr_end_timer":
		if raw := vu.Variables[sourceTimerStartKey(name)]; raw != "" {
			if nanos, err := strconv.ParseInt(raw, 10, 64); err == nil {
				durationMs := time.Since(time.Unix(0, nanos)).Milliseconds()
				vu.Variables[sourceTimerDurationKey(name)] = strconv.FormatInt(durationMs, 10)
			}
		}
	}
}

func (vu *VUser) handleSourceMetricAction(ctx context.Context, action dsl.Action) {
	args := sourceActionArgs(action)
	name := firstEngineValue(action.Name, args...)
	value := ""
	if len(args) > 1 {
		value = args[1]
	}
	slog.Default().InfoContext(ctx, "source metric",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"name", name,
		"value", value,
	)
}

func (vu *VUser) handleSourceStateAction(ctx context.Context, action dsl.Action) {
	function := sourceActionFunction(action)
	switch action.Type {
	case "source_parameter":
		vu.handleSourceParameterAction(function, action)
	case "source_json", "source_xml":
		vu.handleSourceDocumentAction(function, action)
	case "source_secret":
		vu.handleSourceSecretAction(function, action)
	default:
		vu.logSourceReviewAction(ctx, action)
	}
}

func (vu *VUser) handleSourceParameterAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	vu.ensureVariables()
	switch function {
	case "lr_save_string", "lr_save_int":
		if len(args) >= 2 {
			vu.setSourceVariable(args[1], args[0])
		}
	case "lr_save_var":
		if len(args) >= 4 {
			vu.setSourceVariable(args[3], args[0])
		}
	case "lr_param_sprintf":
		if len(args) >= 2 {
			values := make([]any, 0, len(args)-2)
			for _, arg := range args[2:] {
				values = append(values, arg)
			}
			vu.setSourceVariable(args[0], renderPrintfLikeMessage(args[1], values))
		}
	case "lr_param_increment":
		if len(args) >= 1 {
			delta := 1
			if len(args) > 1 {
				if parsed, err := strconv.Atoi(strings.TrimSpace(args[1])); err == nil {
					delta = parsed
				}
			}
			current, _ := strconv.Atoi(strings.TrimSpace(vu.Variables[strings.ToLower(args[0])]))
			vu.setSourceVariable(args[0], strconv.Itoa(current+delta))
		}
	case "lr_free_parameter":
		if len(args) >= 1 {
			delete(vu.Variables, args[0])
			delete(vu.Variables, strings.ToLower(args[0]))
		}
	case "lr_save_timestamp":
		name := firstEngineValue("timestamp", args...)
		vu.setSourceVariable(name, strconv.FormatInt(time.Now().Unix(), 10))
	case "lr_save_datetime":
		name := firstEngineValue("datetime", args...)
		format := defaultDateTimeLayout
		if len(args) > 1 {
			format = sourceDateTimeLayout(args[0])
			name = args[len(args)-1]
		}
		vu.setSourceVariable(name, time.Now().Format(format))
	case "lr_whoami":
		vu.setSourceVariable("vuser_id", strconv.Itoa(vu.ID))
		vu.setSourceVariable("vuser_group", vu.Group)
	case "lr_get_host_name", "lr_get_master_host_name":
		if host, err := os.Hostname(); err == nil {
			vu.setSourceVariable(firstEngineValue("host_name", args...), host)
		}
	case "lr_get_vuser_ip":
		vu.setSourceVariable(firstEngineValue("vuser_ip", args...), "127.0.0.1")
	default:
		if saveName := saveParamNameFromAction(action); saveName != "" {
			vu.setSourceVariable(saveName, firstEngineValue("", args...))
		}
	}
}

func (vu *VUser) handleSourceDocumentAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	vu.ensureVariables()
	saveName := saveParamNameFromAction(action)
	if saveName == "" && len(args) > 0 {
		saveName = args[len(args)-1]
	}
	if saveName == "" {
		return
	}
	switch {
	case strings.Contains(function, "find"):
		vu.setSourceVariable(saveName, "0")
	case strings.Contains(function, "get_values"), strings.Contains(function, "extract"), strings.Contains(function, "stringify"), strings.Contains(function, "eval"):
		vu.setSourceVariable(saveName, firstEngineValue("", args...))
	}
}

func (vu *VUser) handleSourceSecretAction(function string, action dsl.Action) {
	args := sourceActionArgs(action)
	saveName := saveParamNameFromAction(action)
	if saveName == "" && len(args) > 0 {
		saveName = args[len(args)-1]
	}
	if saveName == "" {
		saveName = strings.TrimPrefix(function, "lr_")
	}
	vu.setSourceVariable(saveName, "{{secret."+strings.ToLower(saveName)+"}}")
}

func (vu *VUser) logSourceReviewAction(ctx context.Context, action dsl.Action) {
	slog.Default().InfoContext(ctx, "source review action",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"function", sourceActionFunction(action),
		"support_mode", sourceActionSupportMode(action),
	)
}

func (vu *VUser) ensureVariables() {
	if vu.Variables == nil {
		vu.Variables = make(map[string]string)
	}
}

func (vu *VUser) setSourceVariable(name string, value string) {
	name = strings.TrimSpace(name)
	if name == "" {
		return
	}
	vu.ensureVariables()
	resolved := vu.interpolate(value)
	vu.Variables[name] = resolved
	vu.Variables[strings.ToLower(name)] = resolved
}

func sourceActionFunction(action dsl.Action) string {
	if value, ok := actionParamString(action.Params, "function"); ok {
		return value
	}
	if value, ok := action.Metadata["source_function"].(string); ok {
		return value
	}
	return action.Type
}

func sourceActionSupportMode(action dsl.Action) string {
	if value, ok := actionParamString(action.Params, "support_mode"); ok {
		return value
	}
	if value, ok := action.Metadata["support_mode"].(string); ok {
		return value
	}
	return ""
}

func sourceActionArgs(action dsl.Action) []string {
	if action.Params == nil {
		return nil
	}
	raw, ok := action.Params["args"]
	if !ok {
		return nil
	}
	values := anySlice(raw)
	args := make([]string, 0, len(values))
	for _, value := range values {
		args = append(args, strings.TrimSpace(fmt.Sprint(value)))
	}
	return args
}

func anySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return typed
	case []string:
		out := make([]any, len(typed))
		for i, value := range typed {
			out[i] = value
		}
		return out
	case nil:
		return nil
	default:
		return []any{typed}
	}
}

func firstEngineValue(fallback string, values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return fallback
}

func sourceTransactionStartKey(name string) string {
	return "__lr_transaction." + strings.ToLower(strings.TrimSpace(name)) + ".start"
}

func sourceTransactionStatusKey(name string) string {
	return "__lr_transaction." + strings.ToLower(strings.TrimSpace(name)) + ".status"
}

func sourceTimerStartKey(name string) string {
	return "__lr_timer." + strings.ToLower(strings.TrimSpace(name)) + ".start"
}

func sourceTimerDurationKey(name string) string {
	return "__lr_timer." + strings.ToLower(strings.TrimSpace(name)) + ".duration_ms"
}

func saveParamNameFromAction(action dsl.Action) string {
	for _, key := range []string{"save_param", "saveparam", "save_param_name", "param", "parameter", "name"} {
		if value, ok := actionParamString(action.Params, key); ok {
			return value
		}
	}
	for _, entry := range structuredCallArgs(action) {
		name, _ := entry["name"].(string)
		value, _ := entry["value"].(string)
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "saveparam", "save_param", "param", "parameter", "name":
			return value
		}
	}
	return ""
}

func structuredCallArgs(action dsl.Action) []map[string]any {
	if action.Params == nil {
		return nil
	}
	raw, ok := action.Params["call_args"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []map[string]any:
		return typed
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if entry, ok := item.(map[string]any); ok {
				out = append(out, entry)
			}
		}
		return out
	default:
		return nil
	}
}

func sourceDateTimeLayout(format string) string {
	switch strings.TrimSpace(format) {
	case "", "%Y-%m-%d %H:%M:%S":
		return defaultDateTimeLayout
	case "%H:%M:%S":
		return defaultTimeLayout
	default:
		layout := strings.TrimSpace(format)
		replacements := []struct {
			old string
			new string
		}{
			{"%Y", "2006"},
			{"%m", "01"},
			{"%d", "02"},
			{"%H", "15"},
			{"%M", "04"},
			{"%S", "05"},
		}
		for _, replacement := range replacements {
			layout = strings.ReplaceAll(layout, replacement.old, replacement.new)
		}
		return layout
	}
}

func (vu *VUser) logErrorMessageAction(ctx context.Context, action dsl.Action) {
	message := vu.renderErrorMessage(action)
	slog.Default().ErrorContext(ctx, "source error message",
		"vuser", vu.ID,
		"group", vu.Group,
		"action", action.Name,
		"message", message,
	)
}

func (vu *VUser) renderErrorMessage(action dsl.Action) string {
	format := action.Value
	if format == "" {
		if paramsFormat, ok := actionParamString(action.Params, "format"); ok {
			format = paramsFormat
		}
	}
	format = vu.interpolate(format)

	args := errorMessageArgs(action.Params)
	if len(args) == 0 {
		if format != "" {
			return format
		}
		return action.Name
	}
	values := make([]any, 0, len(args))
	for _, arg := range args {
		values = append(values, vu.interpolateAny(arg))
	}
	return renderPrintfLikeMessage(format, values)
}

func errorMessageArgs(params map[string]any) []any {
	if params == nil {
		return nil
	}
	raw, ok := params["args"]
	if !ok {
		return nil
	}
	switch typed := raw.(type) {
	case []any:
		return typed
	case []string:
		args := make([]any, len(typed))
		for i, value := range typed {
			args[i] = value
		}
		return args
	default:
		return []any{typed}
	}
}

func renderPrintfLikeMessage(format string, args []any) string {
	if format == "" {
		parts := make([]string, 0, len(args))
		for _, arg := range args {
			parts = append(parts, fmt.Sprint(arg))
		}
		return strings.Join(parts, " ")
	}

	var out strings.Builder
	argIndex := 0
	for i := 0; i < len(format); i++ {
		ch := format[i]
		if ch != '%' || i == len(format)-1 {
			out.WriteByte(ch)
			continue
		}
		next := format[i+1]
		if next == '%' {
			out.WriteByte('%')
			i++
			continue
		}
		specEnd := printfSpecEnd(format, i+1)
		if specEnd < 0 {
			out.WriteByte(ch)
			continue
		}
		if argIndex < len(args) {
			out.WriteString(formatPrintfArg(format[i:specEnd+1], args[argIndex]))
			argIndex++
			i = specEnd
			continue
		}
		out.WriteByte(ch)
	}
	if argIndex < len(args) {
		for _, arg := range args[argIndex:] {
			if out.Len() > 0 {
				out.WriteByte(' ')
			}
			out.WriteString(fmt.Sprint(arg))
		}
	}
	return out.String()
}

func printfSpecEnd(format string, start int) int {
	for i := start; i < len(format); i++ {
		ch := format[i]
		if isPrintfModifier(ch) {
			continue
		}
		if isPrintfVerb(ch) {
			return i
		}
		return -1
	}
	return -1
}

func isPrintfModifier(ch byte) bool {
	if ch >= '0' && ch <= '9' {
		return true
	}
	return strings.ContainsRune("#0+-.hlLjztI", rune(ch))
}

func formatPrintfArg(spec string, arg any) string {
	if spec == "" {
		return fmt.Sprint(arg)
	}
	verb := spec[len(spec)-1]
	goSpec := normalizePrintfSpec(spec)
	value, ok := normalizePrintfValue(verb, arg)
	if !ok {
		return fmt.Sprint(arg)
	}
	return fmt.Sprintf(goSpec, value)
}

func normalizePrintfSpec(spec string) string {
	if len(spec) < 2 {
		return spec
	}
	verb := spec[len(spec)-1]
	body := stripCPrintfLengthModifiers(spec[1 : len(spec)-1])
	switch verb {
	case 'i', 'u':
		verb = 'd'
	}
	return "%" + body + string(verb)
}

func stripCPrintfLengthModifiers(body string) string {
	for {
		switch {
		case strings.HasSuffix(body, "I64"):
			body = strings.TrimSuffix(body, "I64")
		case strings.HasSuffix(body, "ll"), strings.HasSuffix(body, "hh"):
			body = body[:len(body)-2]
		case body != "" && strings.ContainsRune("hlLjzt", rune(body[len(body)-1])):
			body = body[:len(body)-1]
		default:
			return body
		}
	}
}

func normalizePrintfValue(verb byte, arg any) (any, bool) {
	switch verb {
	case 'v', 'T':
		return arg, true
	case 's', 'q':
		return fmt.Sprint(arg), true
	case 't':
		return printfBoolValue(arg)
	case 'c':
		return printfRuneValue(arg)
	case 'd', 'i':
		return printfSignedValue(arg)
	case 'u', 'b', 'o', 'O', 'x', 'X', 'U':
		return printfUnsignedValue(arg)
	case 'e', 'E', 'f', 'F', 'g', 'G':
		return printfFloatValue(arg)
	case 'p':
		return fmt.Sprint(arg), false
	default:
		return fmt.Sprint(arg), false
	}
}

func printfBoolValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case bool:
		return value, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(value))
		return parsed, err == nil
	default:
		return arg, false
	}
}

func printfRuneValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case rune:
		return value, true
	case byte:
		return rune(value), true
	case string:
		if value == "" {
			return arg, false
		}
		return []rune(value)[0], true
	default:
		return printfSignedValue(arg)
	}
}

func printfSignedValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case int:
		return value, true
	case int8:
		return value, true
	case int16:
		return value, true
	case int32:
		return value, true
	case int64:
		return value, true
	case uint:
		return value, true
	case uint8:
		return value, true
	case uint16:
		return value, true
	case uint32:
		return value, true
	case uint64:
		return value, true
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		return parsed, err == nil
	default:
		return arg, false
	}
}

func printfUnsignedValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case uint:
		return value, true
	case uint8:
		return value, true
	case uint16:
		return value, true
	case uint32:
		return value, true
	case uint64:
		return value, true
	case int:
		return value, true
	case int8:
		return value, true
	case int16:
		return value, true
	case int32:
		return value, true
	case int64:
		return value, true
	case string:
		trimmed := strings.TrimSpace(value)
		parsed, err := strconv.ParseUint(trimmed, 10, 64)
		if err == nil {
			return parsed, true
		}
		signed, signedErr := strconv.ParseInt(trimmed, 10, 64)
		return signed, signedErr == nil
	default:
		return arg, false
	}
}

func printfFloatValue(arg any) (any, bool) {
	switch value := arg.(type) {
	case float32:
		return value, true
	case float64:
		return value, true
	case int:
		return float64(value), true
	case int8:
		return float64(value), true
	case int16:
		return float64(value), true
	case int32:
		return float64(value), true
	case int64:
		return float64(value), true
	case uint:
		return float64(value), true
	case uint8:
		return float64(value), true
	case uint16:
		return float64(value), true
	case uint32:
		return float64(value), true
	case uint64:
		return float64(value), true
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(value), 64)
		return parsed, err == nil
	default:
		return arg, false
	}
}

func isPrintfVerb(ch byte) bool {
	return strings.ContainsRune("vTtbcdiuoOqxXUeEfFgGsp", rune(ch))
}

func (vu *VUser) waitAtRendezvous(ctx context.Context, action dsl.Action) error {
	participants := vu.TotalVUsers
	if participants <= 0 {
		participants = 1
	}
	if count, ok := actionParamInt(action.Params, "count"); ok {
		participants = count
	}
	timeout := time.Duration(0)
	if timeoutMs, ok := actionParamInt(action.Params, "timeout_ms"); ok && timeoutMs > 0 {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}
	key := action.Name
	if configuredKey, ok := actionParamString(action.Params, "key"); ok {
		key = configuredKey
	}
	return vu.Rendezvous.Wait(ctx, key, participants, timeout)
}

func actionParamInt(params map[string]any, key string) (int, bool) {
	if params == nil {
		return 0, false
	}
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case float32:
		return int(typed), true
	default:
		return 0, false
	}
}

func actionParamString(params map[string]any, key string) (string, bool) {
	if params == nil {
		return "", false
	}
	value, ok := params[key]
	if !ok {
		return "", false
	}
	typed, ok := value.(string)
	if !ok {
		return "", false
	}
	trimmed := strings.TrimSpace(typed)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

func actionParamBool(params map[string]any, key string) (bool, bool) {
	if params == nil {
		return false, false
	}
	value, ok := params[key]
	if !ok {
		return false, false
	}
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}

// executeWithRetry wraps a single action execution with retry logic.
func (vu *VUser) executeWithRetry(ctx context.Context, action *protocol.Action, errCfg dsl.ErrorHandlingConfig) (*protocol.Result, error) {
	maxAttempts := 1 + errCfg.MaxRetries
	if maxAttempts < 1 {
		maxAttempts = 1
	}

	retryDelay := time.Duration(errCfg.RetryDelayMs) * time.Millisecond
	if retryDelay == 0 {
		retryDelay = time.Second
	}

	var lastResult *protocol.Result
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			// Apply backoff
			delay := retryDelay
			if errCfg.RetryBackoff == "exponential" {
				delay = retryDelay * time.Duration(1<<uint(attempt-1))
			}
			select {
			case <-ctx.Done():
				return lastResult, ctx.Err()
			case <-time.After(delay):
			}
		}

		result, err := vu.Proto.Execute(ctx, action)
		if err != nil {
			return nil, err
		}
		applyProtocolResultSourceMetadata(result, action.Metadata)
		lastResult = result

		if result.Success {
			return result, nil
		}

		// If no retries configured, return immediately
		if errCfg.MaxRetries == 0 {
			return result, nil
		}
	}

	return lastResult, nil
}

func (vu *VUser) executeHTTPSequenceAction(ctx context.Context, action dsl.Action, errCfg dsl.ErrorHandlingConfig) (*protocol.Result, error) {
	children, err := httpSequenceChildActions(action)
	start := time.Now()
	aggregate := &protocol.Result{
		ActionName:          action.Name,
		StartTime:           start,
		Success:             true,
		BusinessTransaction: true,
		Group:               vu.Group,
		Extractions:         make(map[string]string),
	}
	if len(action.Metadata) > 0 {
		applyProtocolResultSourceMetadata(aggregate, vu.interpolateAnyMap(action.Metadata))
	}
	if err != nil {
		aggregate.Success = false
		aggregate.ErrorMessage = err.Error()
		aggregate.Duration = time.Since(start)
		return aggregate, nil
	}
	for index, child := range children {
		if ctx.Err() != nil {
			aggregate.Success = false
			aggregate.ErrorMessage = ctx.Err().Error()
			aggregate.Duration = time.Since(start)
			return aggregate, ctx.Err()
		}
		resolved := vu.resolveAction(child)
		result, execErr := vu.executeWithRetry(ctx, resolved, errCfg)
		if execErr != nil {
			aggregate.Success = false
			aggregate.ErrorMessage = execErr.Error()
			aggregate.Duration = time.Since(start)
			return aggregate, nil
		}
		if result == nil {
			continue
		}
		vu.recordResult(result, errCfg)
		copySequenceSourceResult(aggregate, result)
		for key, value := range result.Extractions {
			aggregate.Extractions[key] = value
		}
		if !result.Success {
			aggregate.Success = false
			aggregate.ErrorMessage = firstEngineValue(result.ErrorMessage, fmt.Sprintf("http_sequence child %d failed", index+1))
			if !errCfg.ContinueOnError {
				break
			}
		}
	}
	aggregate.Duration = time.Since(start)
	return aggregate, nil
}

func httpSequenceChildActions(action dsl.Action) ([]dsl.Action, error) {
	if action.Params == nil {
		return nil, fmt.Errorf("http_sequence %q requires params.requests", action.Name)
	}
	rawRequests, ok := action.Params["requests"]
	if !ok {
		return nil, fmt.Errorf("http_sequence %q requires params.requests", action.Name)
	}
	var values []any
	switch typed := rawRequests.(type) {
	case []any:
		values = typed
	case []dsl.Action:
		values = make([]any, len(typed))
		for i := range typed {
			values[i] = typed[i]
		}
	case []map[string]any:
		values = make([]any, len(typed))
		for i := range typed {
			values[i] = typed[i]
		}
	default:
		return nil, fmt.Errorf("http_sequence %q params.requests must be an array", action.Name)
	}
	if len(values) == 0 {
		return nil, fmt.Errorf("http_sequence %q requires at least one child request", action.Name)
	}
	children := make([]dsl.Action, 0, len(values))
	for index, value := range values {
		child, err := httpSequenceChildAction(value)
		if err != nil {
			return nil, fmt.Errorf("http_sequence %q child %d: %w", action.Name, index+1, err)
		}
		if child.Name == "" {
			child.Name = fmt.Sprintf("%s_%d", action.Name, index+1)
		}
		if child.Type == "" {
			child.Type = child.Method
		}
		if child.Method == "" {
			child.Method = child.Type
		}
		if child.Metadata == nil {
			child.Metadata = map[string]any{}
		}
		for _, key := range []string{"source_transaction", "loadrunner_transaction"} {
			if child.Metadata[key] == nil && action.Metadata != nil && action.Metadata[key] != nil {
				child.Metadata[key] = action.Metadata[key]
			}
		}
		children = append(children, child)
	}
	return children, nil
}

func httpSequenceChildAction(value any) (dsl.Action, error) {
	if action, ok := value.(dsl.Action); ok {
		return action, nil
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return dsl.Action{}, err
	}
	var action dsl.Action
	if err := json.Unmarshal(payload, &action); err != nil {
		return dsl.Action{}, err
	}
	if strings.TrimSpace(action.Target) == "" {
		return dsl.Action{}, fmt.Errorf("missing target")
	}
	return action, nil
}

func copySequenceSourceResult(target *protocol.Result, source *protocol.Result) {
	if target == nil || source == nil {
		return
	}
	target.Region = source.Region
	target.CorrelationID = source.CorrelationID
	target.NetworkProfile = source.NetworkProfile
	target.DependencyType = source.DependencyType
	target.DependencyTarget = source.DependencyTarget
	target.DNSTime += source.DNSTime
	target.ConnectTime += source.ConnectTime
	target.TLSTime += source.TLSTime
	target.TTFB += source.TTFB
	target.BytesSent += source.BytesSent
	target.BytesReceived += source.BytesReceived
	target.StatusCode = source.StatusCode
	target.ServerTelemetry = source.ServerTelemetry
	target.SourceStepID = source.SourceStepID
	target.SourceStepIndex = source.SourceStepIndex
	target.SourceAction = source.SourceAction
	target.SourceActionName = source.SourceActionName
	target.SourceTransaction = firstEngineValue(target.SourceTransaction, source.SourceTransaction)
	target.SourceProfile = source.SourceProfile
	if source.ErrorMessage != "" {
		target.ErrorMessage = source.ErrorMessage
	}
}

func applyProtocolResultSourceMetadata(result *protocol.Result, metadata map[string]any) {
	if result == nil || len(metadata) == 0 {
		return
	}
	result.SourceStepID = sourceMetadataString(metadata, "source_step_id")
	result.SourceStepIndex = sourceMetadataInt(metadata, "source_step_index")
	result.SourceAction = sourceMetadataString(metadata, "source_action")
	result.SourceActionName = sourceMetadataString(metadata, "source_action_name")
	result.SourceTransaction = sourceTransactionName(metadata)
	result.SourceProfile = sourceMetadataString(metadata, "source_profile")
}

// loadDataRow populates VUser variables from configured data sources.
func (vu *VUser) loadDataRow() {
	if vu.DataProvider == nil || !vu.DataProvider.HasSources() {
		return
	}
	for _, name := range vu.DataProvider.SourceNames() {
		row := vu.DataProvider.GetRow(name, vu.ID)
		for k, v := range row {
			vu.Variables[k] = v
		}
	}
}

// handleSnapshot stores an error snapshot if configured and the result is a failure.
func (vu *VUser) handleSnapshot(result *protocol.Result, errCfg dsl.ErrorHandlingConfig) {
	if errCfg.SnapshotOnError && !result.Success && result.Snapshot != nil && vu.SnapshotStore != nil {
		vu.SnapshotStore.Add(result.Snapshot)
	}
}

// makeLogger creates a logger for this VUser based on the log config.
func (vu *VUser) makeLogger(cfg dsl.LogConfig) *slog.Logger {
	attrs := []any{"vuser", vu.ID, "group", vu.Group}

	switch cfg.Level {
	case "disabled":
		// Use a no-op handler by setting level extremely high
		return slog.New(slog.NewTextHandler(io.Discard, &slog.HandlerOptions{
			Level: slog.Level(100),
		}))
	default:
		return slog.With(attrs...)
	}
}

func actionLogAttrs(actionName string, resolved *protocol.Action, result *protocol.Result, cfg dsl.LogConfig, includeError bool) []any {
	attrs := []any{
		"action", actionName,
		"status", result.StatusCode,
		"duration_ms", result.Duration.Milliseconds(),
	}
	if !includeError {
		attrs = append(attrs, "bytes", result.BytesReceived)
	} else {
		attrs = append(attrs, "error", result.ErrorMessage)
	}

	options := make(map[string]struct{}, len(cfg.ExtendedOptions))
	for _, option := range cfg.ExtendedOptions {
		options[strings.TrimSpace(option)] = struct{}{}
	}

	if _, ok := options["parameter_substitution"]; ok {
		target := resolved.Target
		if resolved.RedactedTarget != "" {
			target = resolved.RedactedTarget
		}
		requestBody := string(resolved.Body)
		if len(resolved.RedactedBody) > 0 {
			requestBody = string(resolved.RedactedBody)
		}
		attrs = append(attrs,
			"target", target,
			"request_body", requestBody,
		)
	}
	if _, ok := options["data_returned"]; ok {
		attrs = append(attrs, "response_body", result.ResponseBody)
	}
	if _, ok := options["advanced_trace"]; ok {
		attrs = append(attrs,
			"dependency_target", result.DependencyTarget,
			"correlation_id", result.CorrelationID,
			"dns_ms", result.DNSTime.Milliseconds(),
			"connect_ms", result.ConnectTime.Milliseconds(),
			"tls_ms", result.TLSTime.Milliseconds(),
			"ttfb_ms", result.TTFB.Milliseconds(),
		)
	}

	return attrs
}

func (vu *VUser) resolveAction(action dsl.Action) *protocol.Action {
	target, redactedTarget := vu.interpolateWithSecrets(action.Target)
	selector, _ := vu.interpolateWithSecrets(action.Selector)
	value, _ := vu.interpolateWithSecrets(action.Value)
	headers, redactedHeaders := vu.interpolateMapWithSecrets(action.Headers)
	body, redactedBody := vu.interpolateWithSecrets(action.Body)
	soapAction, redactedSOAPAction := toProtocolSOAPActions(action.SOAP, vu)
	return &protocol.Action{
		Name:            action.Name,
		Type:            action.Type,
		Method:          action.Method,
		Target:          target,
		RedactedTarget:  redactedTarget,
		Selector:        selector,
		Value:           value,
		Headers:         headers,
		RedactedHeaders: redactedHeaders,
		Body:            []byte(body),
		BodyEncoding:    action.BodyEncoding,
		RedactedBody:    []byte(redactedBody),
		Params:          vu.interpolateAnyMap(action.Params),
		WaitFor:         toProtocolWaitCondition(action.WaitFor, vu),
		Sync:            toProtocolBrowserSyncPolicy(action.Sync, vu),
		Capture:         toProtocolCaptureOptions(action.Capture),
		Metadata:        vu.interpolateAnyMap(action.Metadata),
		Extractors:      toProtocolExtractors(action.Extractors),
		Assertions:      toProtocolAssertions(action.Assertions),
		SOAP:            soapAction,
		RedactedSOAP:    redactedSOAPAction,
	}
}

func toProtocolSOAPAction(action *dsl.SOAPActionConfig, vu *VUser) *protocol.SOAPActionConfig {
	resolved, _ := toProtocolSOAPActions(action, vu)
	return resolved
}

func toProtocolSOAPActions(action *dsl.SOAPActionConfig, vu *VUser) (*protocol.SOAPActionConfig, *protocol.SOAPActionConfig) {
	if action == nil {
		return nil, nil
	}
	resolvedAction, redactedAction := vu.interpolateWithSecrets(action.Action)
	resolvedOperation, redactedOperation := vu.interpolateWithSecrets(action.Operation)
	resolvedBodyMode, redactedBodyMode := vu.interpolateWithSecrets(action.BodyMode)
	return &protocol.SOAPActionConfig{
			Action:    resolvedAction,
			Operation: resolvedOperation,
			BodyMode:  resolvedBodyMode,
		}, &protocol.SOAPActionConfig{
			Action:    redactedAction,
			Operation: redactedOperation,
			BodyMode:  redactedBodyMode,
		}
}

func (vu *VUser) interpolate(s string) string {
	if s == "" {
		return s
	}
	resolved, _ := vu.interpolateWithSecrets(s)
	return resolved
}

func (vu *VUser) interpolateWithSecrets(s string) (string, string) {
	if s == "" {
		return s, s
	}
	hasSecret := false
	resolved := templatePattern.ReplaceAllStringFunc(s, func(token string) string {
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if expr == "" {
			return token
		}
		if value, ok := vu.resolveTemplateValue(expr); ok {
			return value
		}
		return token
	})
	redacted := templatePattern.ReplaceAllStringFunc(s, func(token string) string {
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if expr == "" {
			return token
		}
		if strings.HasPrefix(expr, "secret.") {
			hasSecret = true
			return "[REDACTED]"
		}
		if value, ok := vu.resolveTemplateValue(expr); ok {
			return value
		}
		return token
	})
	if !hasSecret {
		return resolved, resolved
	}
	return resolved, redacted
}

func (vu *VUser) interpolateMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	resolved, _ := vu.interpolateMapWithSecrets(m)
	return resolved
}

func (vu *VUser) interpolateMapWithSecrets(m map[string]string) (map[string]string, map[string]string) {
	if m == nil {
		return nil, nil
	}
	out := make(map[string]string, len(m))
	redacted := make(map[string]string, len(m))
	for k, v := range m {
		resolvedValue, redactedValue := vu.interpolateWithSecrets(v)
		out[k] = resolvedValue
		redacted[k] = redactedValue
	}
	return out, redacted
}

func (vu *VUser) interpolateAnyMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = vu.interpolateAny(v)
	}
	return out
}

func (vu *VUser) interpolateAny(v any) any {
	switch typed := v.(type) {
	case string:
		return vu.interpolate(typed)
	case []any:
		out := make([]any, len(typed))
		for i := range typed {
			out[i] = vu.interpolateAny(typed[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(typed))
		for k, nested := range typed {
			out[k] = vu.interpolateAny(nested)
		}
		return out
	default:
		return v
	}
}

func (vu *VUser) resolveTemplateValue(expr string) (string, bool) {
	switch {
	case strings.HasPrefix(expr, "var."):
		key := strings.TrimSpace(strings.TrimPrefix(expr, "var."))
		value, ok := vu.Variables[key]
		if !ok && vu.Parameters != nil {
			return vu.Parameters.ResolveNamed(key)
		}
		return value, ok
	case strings.HasPrefix(expr, "param."):
		if vu.Parameters == nil {
			return "", false
		}
		key := strings.TrimSpace(strings.TrimPrefix(expr, "param."))
		return vu.Parameters.ResolveNamed(key)
	case strings.HasPrefix(expr, "data."):
		// Data variables are stored as "source.column", so map data.source.column -> source.column.
		key := strings.TrimSpace(strings.TrimPrefix(expr, "data."))
		value, ok := vu.Variables[key]
		return value, ok
	case strings.HasPrefix(expr, "env."):
		key := strings.TrimSpace(strings.TrimPrefix(expr, "env."))
		value, ok := os.LookupEnv(key)
		return value, ok
	case strings.HasPrefix(expr, "secret."):
		key := strings.TrimSpace(strings.TrimPrefix(expr, "secret."))
		return resolveSecretTemplateValue(key, vu.secretResolver())
	case strings.HasPrefix(expr, "fn."), strings.HasPrefix(expr, "fake."):
		return resolveBuiltinFunction(expr, time.Now)
	default:
		return "", false
	}
}

func (vu *VUser) secretResolver() SecretResolver {
	if vu.SecretResolver == nil {
		vu.SecretResolver = newDefaultSecretResolver()
	}
	return vu.SecretResolver
}

func (vu *VUser) mergeExtractions(r *protocol.Result) {
	for k, v := range r.Extractions {
		vu.Variables[k] = v
	}
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i < 0 {
			return s
		}
		s = s[:i] + new + s[i+len(old):]
	}
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

func toProtocolExtractors(exts []dsl.Extractor) []protocol.Extractor {
	out := make([]protocol.Extractor, len(exts))
	for i, e := range exts {
		out[i] = protocol.Extractor{
			Name:       e.Name,
			Source:     e.Source,
			Type:       e.Type,
			Expression: e.Expression,
		}
	}
	return out
}

func toProtocolAssertions(asserts []dsl.Assertion) []protocol.Assertion {
	out := make([]protocol.Assertion, len(asserts))
	for i, a := range asserts {
		out[i] = protocol.Assertion{
			Type:     a.Type,
			Expected: a.Expected,
			Value:    a.Value,
			Max:      a.Max,
		}
	}
	return out
}

func toProtocolWaitCondition(wait *dsl.WaitCondition, vu *VUser) *protocol.WaitCondition {
	if wait == nil {
		return nil
	}
	return &protocol.WaitCondition{
		Type:      wait.Type,
		Value:     vu.interpolate(wait.Value),
		TimeoutMs: wait.TimeoutMs,
	}
}

func toProtocolBrowserSyncPolicy(sync *dsl.BrowserSyncPolicy, vu *VUser) *protocol.BrowserSyncPolicy {
	if sync == nil {
		return nil
	}
	return &protocol.BrowserSyncPolicy{
		Type:      sync.Type,
		Value:     vu.interpolate(sync.Value),
		TimeoutMs: sync.TimeoutMs,
		StableMs:  sync.StableMs,
	}
}

func toProtocolCaptureOptions(capture *dsl.BrowserCaptureOptions) *protocol.BrowserCaptureOptions {
	if capture == nil {
		return nil
	}
	return &protocol.BrowserCaptureOptions{
		Screenshot: capture.Screenshot,
		FullPage:   capture.FullPage,
		HTML:       capture.HTML,
	}
}
