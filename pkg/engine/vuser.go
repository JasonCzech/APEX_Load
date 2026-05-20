package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
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
		if vu.ExecutionGoals != nil && !vu.ExecutionGoals.TryStartIteration() {
			break
		}
		if ctx.Err() != nil {
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
			if handled, err := vu.handleEngineAction(ctx, action); handled {
				if err != nil {
					logger.Error("action error", "action", action.Name, "err", err)
					iterSuccess = false
					if !continueOnError {
						goto teardown
					}
				}
				continue
			}

			resolved := vu.resolveAction(action)
			if vu.ExecutionGoals != nil && !vu.ExecutionGoals.TryStartTransaction() {
				goto teardown
			}

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
					logger.Error("action failed", actionLogAttrs(action.Name, resolved, result, logCfg, true)...)
				}
				if !continueOnError {
					goto teardown
				}
			} else if logCfg.Level == "extended" {
				logger.Info("action ok", actionLogAttrs(action.Name, resolved, result, logCfg, false)...)
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
	for _, action := range actions {
		if vu.PauseControl != nil {
			if err := vu.PauseControl.WaitIfPaused(ctx); err != nil {
				return false
			}
		}
		if ctx.Err() != nil {
			return false
		}
		if handled, err := vu.handleEngineAction(ctx, action); handled {
			if err != nil {
				logger.Error(phase+" action failed", "action", action.Name, "err", err)
				if !ignoreErrors {
					return false
				}
			}
			continue
		}
		resolved := vu.resolveAction(action)
		result, err := vu.executeWithRetry(ctx, resolved, errCfg)
		if err != nil {
			logger.Error(phase+" action failed", "action", action.Name, "err", err)
			if !ignoreErrors {
				return false
			}
			continue
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
	}
	return true
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
	switch action.Type {
	case "rendezvous":
		return true, vu.waitAtRendezvous(ctx, action)
	default:
		return false, nil
	}
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

func applyProtocolResultSourceMetadata(result *protocol.Result, metadata map[string]any) {
	if result == nil || len(metadata) == 0 {
		return
	}
	result.SourceStepID = sourceMetadataString(metadata, "source_step_id")
	result.SourceStepIndex = sourceMetadataInt(metadata, "source_step_index")
	result.SourceAction = sourceMetadataString(metadata, "source_action")
	result.SourceActionName = sourceMetadataString(metadata, "source_action_name")
	result.SourceTransaction = sourceMetadataString(metadata, "source_transaction")
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
		RedactedBody:    []byte(redactedBody),
		Params:          vu.interpolateAnyMap(action.Params),
		WaitFor:         toProtocolWaitCondition(action.WaitFor, vu),
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
