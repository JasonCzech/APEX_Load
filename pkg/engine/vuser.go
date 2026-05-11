package engine

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/stormrunner/pkg/dsl"
	"github.com/jasonczech/stormrunner/pkg/protocol"
)

type VUser struct {
	ID            int
	Group         string
	Proto         protocol.Protocol
	Script        *dsl.Script
	Metrics       *MetricsCollector
	ResultSink    ResultSink
	Variables     map[string]string
	DataProvider  *DataProvider
	SnapshotStore *SnapshotStore
	PauseControl  PauseController
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

	vu.Variables = make(map[string]string)

	// Load initial data source row
	vu.loadDataRow()

	// --- Init phase ---
	for _, action := range vu.Script.Init {
		if vu.PauseControl != nil {
			if err := vu.PauseControl.WaitIfPaused(ctx); err != nil {
				return
			}
		}
		if ctx.Err() != nil {
			return
		}
		resolved := vu.resolveAction(action)
		result, err := vu.executeWithRetry(ctx, resolved, cfg.ErrorHandling)
		if err != nil {
			logger.Error("init action failed", "action", action.Name, "err", err)
			return
		}
		vu.recordResult(result, cfg.ErrorHandling)

		if !result.Success {
			if logCfg.OnErrorOnly || logCfg.Level == "extended" {
				logger.Error("init action unsuccessful", "action", action.Name, "status", result.StatusCode, "error", result.ErrorMessage)
			} else {
				logger.Warn("init action unsuccessful", "action", action.Name, "error", result.ErrorMessage)
			}
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

	autoTx := cfg.RunLogic.AutoTransactions
	maxIter := cfg.RunLogic.Iterations
	continueOnError := cfg.ErrorHandling.ContinueOnError

	// --- Main iteration loop ---
	iteration := 0
	for {
		// Check iteration limit
		if maxIter > 0 && iteration >= maxIter {
			break
		}
		if ctx.Err() != nil {
			break
		}

		iterStart := time.Now()

		// Reload data row at the start of each iteration
		if iteration > 0 {
			vu.loadDataRow()
		}

		// Track iteration-level transaction
		var iterTxStart time.Time
		if autoTx == "per_iteration" {
			iterTxStart = time.Now()
		}

		iterSuccess := true

		for _, action := range vu.Script.Actions {
			if vu.PauseControl != nil {
				if err := vu.PauseControl.WaitIfPaused(ctx); err != nil {
					goto teardown
				}
			}
			if ctx.Err() != nil {
				goto teardown
			}

			resolved := vu.resolveAction(action)

			// Track action-level transaction
			var actionTxStart time.Time
			if autoTx == "per_action" {
				actionTxStart = time.Now()
			}

			result, err := vu.executeWithRetry(ctx, resolved, cfg.ErrorHandling)
			if err != nil {
				logger.Error("action error", "action", action.Name, "err", err)
				iterSuccess = false
				if !continueOnError {
					goto teardown
				}
				continue
			}

			vu.recordResult(result, cfg.ErrorHandling)

			// Record auto-transaction for this action
			if autoTx == "per_action" {
				vu.Metrics.RecordTransaction(action.Name, time.Since(actionTxStart), result.Success, vu.Group)
			}

			if !result.Success {
				iterSuccess = false
				if logCfg.OnErrorOnly || logCfg.Level == "extended" {
					logger.Error("action failed", "action", action.Name, "status", result.StatusCode,
						"duration_ms", result.Duration.Milliseconds(), "error", result.ErrorMessage)
				}
				if !continueOnError {
					goto teardown
				}
			} else if logCfg.Level == "extended" {
				logger.Info("action ok", "action", action.Name, "status", result.StatusCode,
					"duration_ms", result.Duration.Milliseconds(), "bytes", result.BytesReceived)
			}

			// Apply think time
			if action.ThinkTimeAfter {
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
					// Inherit global multiplier/limit/ignore if not set per-action
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
		}

		// Record iteration-level transaction
		if autoTx == "per_iteration" {
			txName := fmt.Sprintf("iteration_%d", iteration+1)
			vu.Metrics.RecordTransaction(txName, time.Since(iterTxStart), iterSuccess, vu.Group)
		}

		iteration++

		// Apply pacing between iterations
		applyIterationPacing(pacingCfg, iterStart)
	}

teardown:
	for _, action := range vu.Script.Teardown {
		resolved := vu.resolveAction(action)
		result, _ := vu.Proto.Execute(context.Background(), resolved)
		if result != nil {
			vu.recordResult(result, cfg.ErrorHandling)
		}
	}
}

func (vu *VUser) recordResult(result *protocol.Result, errCfg dsl.ErrorHandlingConfig) {
	if result == nil {
		return
	}
	result.Group = vu.Group
	vu.Metrics.Record(result)
	vu.mergeExtractions(result)
	vu.handleSnapshot(result, errCfg)
	if vu.ResultSink != nil {
		_ = vu.ResultSink.RecordResult(result)
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
		return slog.New(slog.NewTextHandler(nil, &slog.HandlerOptions{
			Level: slog.Level(100),
		}))
	default:
		return slog.With(attrs...)
	}
}

func (vu *VUser) resolveAction(action dsl.Action) *protocol.Action {
	return &protocol.Action{
		Name:       action.Name,
		Type:       action.Type,
		Method:     action.Method,
		Target:     vu.interpolate(action.Target),
		Selector:   vu.interpolate(action.Selector),
		Value:      vu.interpolate(action.Value),
		Headers:    vu.interpolateMap(action.Headers),
		Body:       []byte(vu.interpolate(action.Body)),
		Params:     vu.interpolateAnyMap(action.Params),
		WaitFor:    toProtocolWaitCondition(action.WaitFor, vu),
		Capture:    toProtocolCaptureOptions(action.Capture),
		Metadata:   vu.interpolateAnyMap(action.Metadata),
		Extractors: toProtocolExtractors(action.Extractors),
		Assertions: toProtocolAssertions(action.Assertions),
	}
}

func (vu *VUser) interpolate(s string) string {
	if s == "" {
		return s
	}
	return templatePattern.ReplaceAllStringFunc(s, func(token string) string {
		// token is always {{...}} from the regex.
		expr := strings.TrimSpace(token[2 : len(token)-2])
		if expr == "" {
			return token
		}
		if value, ok := vu.resolveTemplateValue(expr); ok {
			return value
		}
		return token
	})
}

func (vu *VUser) interpolateMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = vu.interpolate(v)
	}
	return out
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
		return value, ok
	case strings.HasPrefix(expr, "data."):
		// Data variables are stored as "source.column", so map data.source.column -> source.column.
		key := strings.TrimSpace(strings.TrimPrefix(expr, "data."))
		value, ok := vu.Variables[key]
		return value, ok
	case strings.HasPrefix(expr, "env."):
		key := strings.TrimSpace(strings.TrimPrefix(expr, "env."))
		value, ok := os.LookupEnv(key)
		return value, ok
	case expr == "fn.uuid()":
		return uuid.NewString(), true
	default:
		return "", false
	}
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
