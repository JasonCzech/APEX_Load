package engine

// vuser_execute.go — split from vuser.go (same package, behavior-preserving).

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
)

func (vu *VUser) recordResult(result *protocol.Result, errCfg dsl.ErrorHandlingConfig, logCfg dsl.LogConfig) {
	if result == nil {
		return
	}
	result.Group = vu.Group
	if result.NetworkProfile == "" && vu.Script != nil {
		result.NetworkProfile = networkProfileLabel(vu.Script.Config.WebRuntime.NetworkSimulation)
	}
	if err := vu.mergeExtractions(result); err != nil {
		result.Success = false
		result.Extractions = nil
		if result.ErrorMessage == "" {
			result.ErrorMessage = "extraction retention failed: " + err.Error()
		} else {
			result.ErrorMessage += "; extraction retention failed: " + err.Error()
		}
	}
	retainResponseBody := shouldRetainResultResponseBody(result, logCfg)
	scrubSensitiveStructuredResult(result, vu.RedactionConfig)
	boundResultEvidence(result)
	scrubSensitiveResultWithOptions(result, vu.RedactionConfig, retainResponseBody)
	boundResultEvidence(result)
	vu.Metrics.Record(result)
	vu.handleSnapshot(result, errCfg)
	if vu.ResultSink != nil {
		_ = vu.ResultSink.RecordResult(result)
	}
}

func shouldRetainResultResponseBody(result *protocol.Result, logCfg dsl.LogConfig) bool {
	if result == nil || result.ResponseBody == "" {
		return false
	}
	if !result.Success || result.Snapshot != nil {
		return true
	}
	return logConfigRetainsSuccessfulResponseBody(logCfg)
}

func logConfigRetainsSuccessfulResponseBody(logCfg dsl.LogConfig) bool {
	if strings.ToLower(strings.TrimSpace(logCfg.Level)) != "extended" {
		return false
	}
	for _, option := range logCfg.ExtendedOptions {
		if strings.TrimSpace(option) == "data_returned" {
			return true
		}
	}
	return false
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

// safeProtoExecute invokes the protocol adapter's Execute, converting a panic
// into an error. A buggy adapter (nil dereference, index out of range, …) thus
// fails the action — flowing through the normal error / continue-on-error path,
// at worst aborting the current iteration to teardown — instead of unwinding the
// goroutine and crashing the whole generator process along with every other
// concurrent VUser and test on the pod.
func (vu *VUser) safeProtoExecute(ctx context.Context, action *protocol.Action) (result *protocol.Result, err error) {
	defer func() {
		if r := recover(); r != nil {
			result = nil
			actionName := ""
			if action != nil {
				actionName = action.Name
			}
			protoName := ""
			if vu.Script != nil {
				protoName = vu.Script.Protocol
			}
			err = fmt.Errorf("protocol %q panicked executing action %q: %v\n%s",
				protoName, actionName, r, debug.Stack())
		}
	}()
	return vu.Proto.Execute(ctx, action)
}

// executeWithRetry wraps a single action execution with retry logic.
func (vu *VUser) executeWithRetry(ctx context.Context, action *protocol.Action, errCfg dsl.ErrorHandlingConfig) (*protocol.Result, error) {
	if err := vu.checkResolvedSecrets(); err != nil {
		return nil, err
	}
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

		result, err := vu.safeProtoExecute(ctx, action)
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
		vu.recordResult(result, errCfg, dsl.LogConfig{})
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

// handleSnapshot stores an error snapshot if configured and the result is a failure.
func (vu *VUser) handleSnapshot(result *protocol.Result, errCfg dsl.ErrorHandlingConfig) {
	if errCfg.SnapshotOnError && !result.Success && result.Snapshot != nil && vu.SnapshotStore != nil {
		vu.SnapshotStore.Add(result.Snapshot)
	}
}
