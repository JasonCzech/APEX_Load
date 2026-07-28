package engine

// vuser_execute.go — split from vuser.go (same package, behavior-preserving).

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime/debug"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/protocol"
	apptelemetry "github.com/jasonczech/apexload/pkg/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

func (vu *VUser) recordResult(result *protocol.Result, errCfg dsl.ErrorHandlingConfig, logCfg dsl.LogConfig) {
	if result == nil {
		return
	}
	vu.consumeRuntimeCredentialResultValues(result)
	// CorrelationEvidence is a replay-only, pre-scrub side channel. No metrics or
	// ResultSink implementation may retain it, even accidentally in-process.
	result.CorrelationEvidence = nil
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
	if vu.runtimeCredentialRedactionError() != nil {
		clearUnredactableResultEvidence(result)
	}
	retainResponseBody := shouldRetainResultResponseBody(result, logCfg)
	scrubSensitiveStructuredResult(result, vu.RedactionConfig)
	scrubSensitiveResultWithOptions(result, vu.RedactionConfig, retainResponseBody)
	boundResultEvidence(result)
	if result.BusinessTransaction {
		// Streamed adapters (notably java_vuser) emit completed business
		// transactions as child results. Route those through the same cumulative
		// transaction accounting as synthetic transactions while retaining the
		// ordinary ResultSink call below exactly once. Some sinks already interpret
		// BusinessTransaction in RecordResult, so also calling TransactionSink here
		// would duplicate their transaction records.
		vu.Metrics.RecordTransactionResult(result)
	} else {
		vu.Metrics.Record(result)
	}
	vu.handleSnapshot(result, errCfg)
	if vu.ResultSink != nil {
		_ = vu.ResultSink.RecordResult(result)
	}
}

// consumeRuntimeCredentialResultValues moves protocol-only credential evidence
// into the runtime matcher before a result is retained or an unsuccessful retry
// result is discarded. Consuming every attempt means a cookie rotated during a
// retry is protected on the next attempt without growing an append-only global
// matcher.
func (vu *VUser) consumeRuntimeCredentialResultValues(result *protocol.Result) {
	if vu == nil || result == nil {
		return
	}
	runtimeCredentialValues := result.RuntimeCredentialValues
	runtimeCredentialValuesSnapshot := result.RuntimeCredentialValuesSnapshot
	result.RuntimeCredentialValues = nil
	result.RuntimeCredentialValuesSnapshot = false
	if runtimeCredentialValuesSnapshot {
		_ = vu.stageRuntimeCredentialSessionSnapshot(runtimeCredentialValues)
	} else {
		_ = vu.registerRuntimeCredentialValues(runtimeCredentialValues...)
	}
}

func clearUnredactableResultEvidence(result *protocol.Result) {
	if result == nil {
		return
	}
	result.Success = false
	result.ErrorMessage = "runtime credential redaction failed"
	result.ResponseBody = ""
	result.CorrelationEvidence = nil
	result.Extractions = nil
	result.CorrelationID = ""
	result.DependencyTarget = ""
	result.PageURL = ""
	result.PageTitle = ""
	result.HTML = ""
	result.ScreenshotBase64 = ""
	result.ConsoleEntries = nil
	result.NetworkEntries = nil
	result.DOMSummary = nil
	result.ClientMetrics = nil
	result.ServerTelemetry = nil
	result.ProtocolEvidence = nil
	result.Snapshot = nil
	result.SourceAction = ""
	result.SourceActionName = ""
	result.SourceTransaction = ""
	result.SourcePath = ""
	result.SourceUID = ""
	result.OperationKind = ""
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

func (vu *VUser) recordTransaction(name string, duration time.Duration, success bool, source *protocol.Result) *protocol.Result {
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
	// Transaction-only execution paths (notably http_sequence aggregates) do
	// not pass through recordResult. Apply the same detach, exact-secret
	// redaction, and evidence bounds before either metrics or archive sinks can
	// retain adapter-controlled metadata or error text.
	scrubSensitiveStructuredResult(tx, vu.RedactionConfig)
	scrubSensitiveResultWithOptions(tx, vu.RedactionConfig, false)
	boundResultEvidence(tx)
	if vu.Metrics != nil {
		vu.Metrics.RecordTransactionResult(tx)
	}
	if sink, ok := vu.ResultSink.(TransactionSink); ok {
		_ = sink.RecordTransaction(tx)
	}
	return tx
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
	return actionsHaveExplicitTransactions(script.Init) ||
		actionsHaveExplicitTransactions(script.Actions) ||
		actionsHaveExplicitTransactions(script.Teardown)
}

// actionsHaveExplicitTransactions recurses into control-flow bodies
// (control_if/control_loop steps and else branches) so a transaction-emitting
// action wrapped in a loop still suppresses the automatic whole-iteration
// transaction, matching the sibling persistent-lifecycle detectors.
func actionsHaveExplicitTransactions(actions []dsl.Action) bool {
	for _, action := range actions {
		switch dsl.CanonicalActionType(strings.ToLower(strings.TrimSpace(action.Type))) {
		case "source_transaction", "transaction":
			return true
		case "java_vuser_run":
			// Java Vuser imports execute LoadRunner transaction calls inside the
			// governed JVM runner. The importer marks actions whose source contains
			// those calls so their streamed business-transaction samples do not also
			// acquire an automatic whole-iteration transaction.
			if emits, _ := action.Metadata["emits_business_transactions"].(bool); emits {
				return true
			}
		case "http_sequence":
			// Every sequence emits a logical aggregate transaction unless an
			// actually-open source transaction owns it at runtime. Treating only
			// metadata-bearing sequences as explicit lets the default automatic
			// iteration transaction consume the same goal slot first and either
			// double-count or suppress the sequence aggregate.
			return true
		}
		if action.Control != nil && (actionsHaveExplicitTransactions(action.Control.Steps) || actionsHaveExplicitTransactions(action.Control.Else)) {
			return true
		}
	}
	return false
}

// actionsMayEmitBusinessTransactions reports whether an action can stream
// adapter-owned business transactions. The first emitted transaction reserves
// capacity before pacing, just like action/source/sequence transactions, so an
// exact goal cannot leave a VUser sleeping before work it is already entitled
// to finish.
func actionsMayEmitBusinessTransactions(actions []dsl.Action) bool {
	for _, action := range actions {
		if actionEmitsBusinessTransactions(action) {
			return true
		}
		if action.Control != nil &&
			(actionsMayEmitBusinessTransactions(action.Control.Steps) ||
				actionsMayEmitBusinessTransactions(action.Control.Else)) {
			return true
		}
	}
	return false
}

func actionEmitsBusinessTransactions(action dsl.Action) bool {
	emits, _ := action.Metadata["emits_business_transactions"].(bool)
	return emits
}

// actionsMayRecordActionTransaction reports whether an iteration can reach an
// action that acquires a per-action transaction slot. It is used only to decide
// whether the next iteration can safely carry one atomic reservation across
// pacing; engine-only actions never consume such a reservation.
func actionsMayRecordActionTransaction(actions []dsl.Action) bool {
	for _, action := range actions {
		actionType := strings.TrimSpace(action.Type)
		switch strings.ToLower(actionType) {
		case "control_if", "control_loop":
			if action.Control != nil &&
				(actionsMayRecordActionTransaction(action.Control.Steps) ||
					actionsMayRecordActionTransaction(action.Control.Else)) {
				return true
			}
			continue
		case "http_sequence":
			return true
		}
		if spec, ok := dsl.SpecForActionType(dsl.CanonicalActionType(actionType)); ok && spec.EngineAction() {
			continue
		}
		return true
	}
	return false
}

// actionsMayRecordHTTPSequenceTransaction reports whether an iteration can
// reach a logical HTTP sequence. A sequence records its own aggregate
// transaction unless a matching source transaction is actually open at
// runtime, so it must reserve goal capacity before TPS/final-iteration pacing
// even when auto_transactions is "none".
func actionsMayRecordHTTPSequenceTransaction(actions []dsl.Action) bool {
	for _, action := range actions {
		if strings.EqualFold(strings.TrimSpace(action.Type), "http_sequence") {
			return true
		}
		if action.Control != nil &&
			(actionsMayRecordHTTPSequenceTransaction(action.Control.Steps) ||
				actionsMayRecordHTTPSequenceTransaction(action.Control.Else)) {
			return true
		}
	}
	return false
}

// actionsMayRecordSourceTransaction reports whether an iteration can reach an
// explicit LoadRunner transaction boundary or an immediate lr_set_transaction.
// Starts normally claim the slot; ends also need a reservation when their
// matching start ran in init.
func actionsMayRecordSourceTransaction(actions []dsl.Action) bool {
	for _, action := range actions {
		if sourceTransactionStarts(action) || sourceTransactionEnds(action) ||
			sourceTransactionRecordsImmediate(action) {
			return true
		}
		if action.Control != nil &&
			(actionsMayRecordSourceTransaction(action.Control.Steps) ||
				actionsMayRecordSourceTransaction(action.Control.Else)) {
			return true
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
	if action.Params != nil {
		if timeoutValue, present := action.Params["timeout_ms"]; present {
			var err error
			timeout, err = protocol.BoundedMilliseconds(timeoutValue, 0, dsl.MaxConfiguredProtocolTimeout)
			if err != nil {
				return fmt.Errorf("rendezvous timeout_ms %w", err)
			}
		}
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
	return vu.safeProtoExecuteStream(ctx, action, nil)
}

func (vu *VUser) safeProtoExecuteStream(ctx context.Context, action *protocol.Action, emit func(*protocol.Result) error) (result *protocol.Result, err error) {
	if len(vu.unresolvedParameterRefs) > 0 || actionContainsUnresolvedParameter(action) {
		// Name the offending reference when resolveAction recorded one, so the
		// diagnostic points at the specific binding instead of only the action.
		if refs := dedupeTemplateRefs(vu.unresolvedParameterRefs); len(refs) > 0 {
			return nil, fmt.Errorf("refusing to execute action %q with an unresolved {{param.*}} reference: %s (check the parameter data source and column binding)",
				action.Name, strings.Join(refs, ", "))
		}
		return nil, fmt.Errorf("refusing to execute action %q with an unresolved {{param.*}} reference", action.Name)
	}
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
	if streaming, ok := vu.Proto.(protocol.StreamingProtocol); ok {
		if emit == nil {
			emit = func(*protocol.Result) error { return nil }
		}
		return streaming.ExecuteStream(ctx, action, emit)
	}
	return vu.Proto.Execute(ctx, action)
}

const unresolvedParamToken = "{{param."

// containsUnresolvedParamToken reports whether s contains "{{param." in any
// letter case. Allocation-free by design: this runs on every action of every
// iteration of every VUser, so the obvious strings.Contains(strings.ToLower(s))
// would copy each multi-KB request body on the hot path.
func containsUnresolvedParamToken(s string) bool {
	for i := 0; i+len(unresolvedParamToken) <= len(s); {
		next := strings.Index(s[i:], "{{")
		if next < 0 {
			return false
		}
		i += next
		if i+len(unresolvedParamToken) <= len(s) && strings.EqualFold(s[i:i+len(unresolvedParamToken)], unresolvedParamToken) {
			return true
		}
		i += 2
	}
	return false
}

var (
	unresolvedParamOpenBytes  = []byte("{{")
	unresolvedParamTokenBytes = []byte(unresolvedParamToken)
)

// bytesContainUnresolvedParamToken is the []byte twin of the scan above, used
// for request bodies so the hot path never converts a multi-KB body to string.
func bytesContainUnresolvedParamToken(b []byte) bool {
	for i := 0; i+len(unresolvedParamTokenBytes) <= len(b); {
		next := bytes.Index(b[i:], unresolvedParamOpenBytes)
		if next < 0 {
			return false
		}
		i += next
		if i+len(unresolvedParamTokenBytes) <= len(b) && bytes.EqualFold(b[i:i+len(unresolvedParamTokenBytes)], unresolvedParamTokenBytes) {
			return true
		}
		i += 2
	}
	return false
}

func anyStringContainsUnresolvedParam(values ...string) bool {
	for _, value := range values {
		if containsUnresolvedParamToken(value) {
			return true
		}
	}
	return false
}

func stringMapContainsUnresolvedParam(values map[string]string) bool {
	for key, value := range values {
		if containsUnresolvedParamToken(key) || containsUnresolvedParamToken(value) {
			return true
		}
	}
	return false
}

// anyValueContainsUnresolvedParam walks the dynamically-typed params/metadata
// containers. Depth is bounded by the DSL's own nesting limits.
func anyValueContainsUnresolvedParam(value any) bool {
	switch typed := value.(type) {
	case nil:
		return false
	case string:
		return containsUnresolvedParamToken(typed)
	case []byte:
		return containsUnresolvedParamToken(string(typed))
	case map[string]any:
		for key, item := range typed {
			if containsUnresolvedParamToken(key) || anyValueContainsUnresolvedParam(item) {
				return true
			}
		}
	case map[string]string:
		return stringMapContainsUnresolvedParam(typed)
	case []any:
		for _, item := range typed {
			if anyValueContainsUnresolvedParam(item) {
				return true
			}
		}
	case []string:
		return anyStringContainsUnresolvedParam(typed...)
	}
	return false
}

// actionContainsUnresolvedParameter fails an action closed when interpolation
// left a literal {{param.*}} reference behind, rather than sending the template
// text to the target.
//
// Every string-bearing field is walked explicitly. The previous json.Marshal
// catch-all was both slower (a full encode plus two whole-body lowercase copies
// per action — measured at ~15.7us and 21KB allocated for a 3KB body) and
// blind to the nine `json:"-"` fields, so unresolved refs in RedactedHeaders /
// RedactedParams / RedactedAuth / RedactedSOAP slipped through. Coverage is
// held to the struct by TestActionUnresolvedParameterScanCoversEveryStringField,
// which fails if protocol.Action grows a string-bearing field this misses.
func actionContainsUnresolvedParameter(action *protocol.Action) bool {
	if action == nil {
		return false
	}
	if anyStringContainsUnresolvedParam(
		action.Name, action.Type, action.Method, action.BodyEncoding,
		action.Target, action.RedactedTarget,
		action.Selector, action.RedactedSelector,
		action.Value, action.RedactedValue,
		action.ThinkTime.Type,
	) {
		return true
	}
	if bytesContainUnresolvedParamToken(action.Body) || bytesContainUnresolvedParamToken(action.RedactedBody) {
		return true
	}
	if stringMapContainsUnresolvedParam(action.Headers) || stringMapContainsUnresolvedParam(action.RedactedHeaders) {
		return true
	}
	for _, list := range []protocol.HeaderList{action.HeaderList, action.RedactedHeaderList} {
		for _, entry := range list {
			if anyStringContainsUnresolvedParam(entry.Name, entry.Value) {
				return true
			}
		}
	}
	for _, values := range []map[string]any{action.Params, action.RedactedParams, action.Metadata} {
		for key, item := range values {
			if containsUnresolvedParamToken(key) || anyValueContainsUnresolvedParam(item) {
				return true
			}
		}
	}
	if action.WaitFor != nil && anyStringContainsUnresolvedParam(action.WaitFor.Type, action.WaitFor.Value) {
		return true
	}
	if action.Sync != nil && anyStringContainsUnresolvedParam(action.Sync.Type, action.Sync.Value) {
		return true
	}
	for _, auth := range []*protocol.AuthConfig{action.Auth, action.RedactedAuth} {
		if auth != nil && anyStringContainsUnresolvedParam(auth.Type, auth.Username, auth.Password, auth.Token, auth.Domain) {
			return true
		}
	}
	for _, soap := range []*protocol.SOAPActionConfig{action.SOAP, action.RedactedSOAP} {
		if soap != nil && anyStringContainsUnresolvedParam(soap.Action, soap.Operation, soap.BodyMode) {
			return true
		}
	}
	for _, extractor := range action.Extractors {
		if anyStringContainsUnresolvedParam(extractor.Name, extractor.Source, extractor.Type,
			extractor.Expression, extractor.OnMiss, extractor.Mode, extractor.Convert) {
			return true
		}
	}
	for _, assertion := range action.Assertions {
		if anyStringContainsUnresolvedParam(assertion.Type, assertion.Value) || anyValueContainsUnresolvedParam(assertion.Expected) {
			return true
		}
	}
	// BrowserCaptureOptions is all bools — nothing to scan.
	return false
}

// executeWithRetry wraps a single action execution with retry logic.
func (vu *VUser) executeWithRetry(ctx context.Context, action *protocol.Action, errCfg dsl.ErrorHandlingConfig) (*protocol.Result, error) {
	return vu.executeWithRetryStream(ctx, action, errCfg, nil)
}

func (vu *VUser) executeWithRetryStream(
	ctx context.Context,
	action *protocol.Action,
	errCfg dsl.ErrorHandlingConfig,
	emit func(*protocol.Result) error,
) (finalResult *protocol.Result, finalErr error) {
	var actionSpan trace.Span
	attemptCount := 0
	if apptelemetry.ShouldStartActionSpan(ctx) {
		attributes := []attribute.KeyValue{
			attribute.String("apexload.action.type", apptelemetry.BoundedIdentifier(actionTypeForTrace(action))),
		}
		if vu.Script != nil {
			attributes = append(attributes,
				attribute.String("apexload.protocol", apptelemetry.BoundedIdentifier(vu.Script.Protocol)),
			)
		}
		ctx, actionSpan = otel.Tracer("github.com/jasonczech/apexload/pkg/engine").Start(
			ctx,
			"apexload.action",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attributes...),
		)
		defer func() {
			actionSpan.SetAttributes(attribute.Int("apexload.action.attempts", attemptCount))
			if finalErr != nil {
				actionSpan.SetAttributes(attribute.String("error.type", apptelemetry.BoundedIdentifier(fmt.Sprintf("%T", finalErr))))
				actionSpan.SetStatus(codes.Error, "protocol action failed")
			} else if finalResult == nil || !finalResult.Success {
				actionSpan.SetAttributes(attribute.Bool("apexload.action.success", false))
				if finalResult != nil && finalResult.StatusCode > 0 {
					actionSpan.SetAttributes(attribute.Int("apexload.action.status_code", finalResult.StatusCode))
				}
				actionSpan.SetStatus(codes.Error, "protocol action was unsuccessful")
			} else {
				actionSpan.SetAttributes(attribute.Bool("apexload.action.success", true))
				if finalResult.StatusCode > 0 {
					actionSpan.SetAttributes(attribute.Int("apexload.action.status_code", finalResult.StatusCode))
				}
				actionSpan.SetStatus(codes.Ok, "")
			}
			actionSpan.End()
		}()
	}
	if err := vu.checkResolvedSecrets(); err != nil {
		return nil, err
	}
	if err := vu.runtimeCredentialRedactionError(); err != nil {
		return nil, err
	}
	if err := vu.extendRuntimeRedactionForResolvedAction(action); err != nil {
		return nil, err
	}
	if errCfg.MaxRetries < 0 || errCfg.MaxRetries > dsl.MaxErrorRetries {
		return nil, fmt.Errorf("max_retries must be between 0 and %d", dsl.MaxErrorRetries)
	}
	if errCfg.RetryDelayMs < 0 || errCfg.RetryDelayMs > dsl.MaxConfiguredProtocolTimeoutMS {
		return nil, fmt.Errorf("retry_delay_ms must be between 0 and %d", dsl.MaxConfiguredProtocolTimeoutMS)
	}
	retryBackoff := strings.ToLower(strings.TrimSpace(errCfg.RetryBackoff))
	if retryBackoff != "" && retryBackoff != "fixed" && retryBackoff != "exponential" {
		return nil, fmt.Errorf("retry_backoff must be fixed or exponential")
	}
	maxAttempts := 1 + errCfg.MaxRetries
	// Persistent Java Vuser lifecycle calls are stateful boundaries, not
	// idempotent protocol requests. Re-running a failed init/action/end on the
	// same Actions object can execute HTTP, JDBC, JMS, or SOAP side effects
	// twice and violates LoadRunner's once-per-lifecycle/iteration semantics.
	// Keep the legacy full_lifecycle one-shot retry contract unchanged because
	// every such attempt owns a fresh JVM and Actions instance.
	if javaVuserPersistentAction(action) {
		maxAttempts = 1
	}

	baseRetryDelay := time.Duration(errCfg.RetryDelayMs) * time.Millisecond
	if baseRetryDelay == 0 {
		baseRetryDelay = time.Second
	}

	var lastResult *protocol.Result
	for attempt := 0; attempt < maxAttempts; attempt++ {
		attemptCount = attempt + 1
		if attempt > 0 {
			// Apply backoff
			delay := baseRetryDelay
			if retryBackoff == "exponential" {
				delay = boundedRetryBackoff(baseRetryDelay, attempt)
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				// Since Go 1.23 timer channels are synchronous and a receive after
				// Stop returns is guaranteed to block. The legacy Stop-then-drain
				// pattern can therefore deadlock when cancellation wins at expiry.
				timer.Stop()
				return lastResult, ctx.Err()
			case <-timer.C:
			}
			// The failed attempt will never be returned once its backoff completes.
			// Zero the raw replay-only exchange before the next protocol call can
			// overwrite lastResult and leave the discarded evidence reachable.
			discardCorrelationEvidence(lastResult)
		}

		streaming := false
		if _, ok := vu.Proto.(protocol.StreamingProtocol); ok {
			streaming = true
		}
		childEmitter := emit
		if streaming {
			childEmitter = func(child *protocol.Result) error {
				if child == nil {
					return nil
				}
				if child.SampleKind == "" {
					child.SampleKind = "child"
				}
				if child.ParentActionName == "" && action != nil {
					child.ParentActionName = action.Name
				}
				child.Attempt = attempt + 1
				applyProtocolResultSourceMetadata(child, action.Metadata)
				if emit == nil {
					return nil
				}
				return emit(child)
			}
		}
		result, err := vu.safeProtoExecuteStream(ctx, action, childEmitter)
		vu.consumeRuntimeCredentialResultValues(result)
		if err != nil {
			// Adapters are permitted to return diagnostic Result data alongside an
			// error. The caller intentionally discards that result, so detach and zero
			// the replay-only raw exchange before an adapter-retained pointer can outlive
			// this execution boundary.
			discardCorrelationEvidence(result)
			return nil, err
		}
		if result == nil {
			actionName := ""
			if action != nil {
				actionName = action.Name
			}
			return nil, fmt.Errorf("protocol returned no result for action %q", actionName)
		}
		if streaming && result != nil {
			if result.SampleKind == "" {
				result.SampleKind = "aggregate"
			}
			result.Attempt = attempt + 1
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

func javaVuserPersistentAction(action *protocol.Action) bool {
	if action == nil || !strings.EqualFold(strings.TrimSpace(action.Type), "java_vuser_run") {
		return false
	}
	method := ""
	if action.Params != nil {
		if value, exists := action.Params["method"]; exists && value != nil {
			method = strings.TrimSpace(fmt.Sprint(value))
		}
	}
	return method != "" && !strings.EqualFold(method, "full_lifecycle")
}

func discardCorrelationEvidence(result *protocol.Result) {
	if result == nil || result.CorrelationEvidence == nil {
		return
	}
	*result.CorrelationEvidence = protocol.CorrelationEvidence{}
	result.CorrelationEvidence = nil
}

func actionTypeForTrace(action *protocol.Action) string {
	if action == nil {
		return ""
	}
	if action.Type != "" {
		return strings.ToLower(strings.TrimSpace(action.Type))
	}
	return strings.ToLower(strings.TrimSpace(action.Method))
}

func boundedRetryBackoff(base time.Duration, attempt int) time.Duration {
	maximum := dsl.MaxConfiguredProtocolTimeout
	if base <= 0 {
		base = time.Second
	}
	if base >= maximum {
		return maximum
	}
	delay := base
	for step := 1; step < attempt; step++ {
		if delay >= maximum/2 {
			return maximum
		}
		delay *= 2
	}
	return min(delay, maximum)
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
	var measuredDuration time.Duration
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
			aggregate.Duration = sequenceAggregateDuration(measuredDuration, start)
			return aggregate, ctx.Err()
		}
		resolved := vu.resolveAction(child)
		result, execErr := vu.executeWithRetry(ctx, resolved, errCfg)
		if execErr != nil {
			if ctx.Err() != nil {
				aggregate.Success = false
				aggregate.ErrorMessage = ctx.Err().Error()
				aggregate.Duration = sequenceAggregateDuration(measuredDuration, start)
				return aggregate, ctx.Err()
			}
			aggregate.Success = false
			aggregate.ErrorMessage = execErr.Error()
			if result != nil {
				vu.recordResult(result, errCfg, dsl.LogConfig{})
				measuredDuration += result.Duration
				copySequenceSourceResult(aggregate, result)
			}
			aggregate.Duration = sequenceAggregateDuration(measuredDuration, start)
			if !errCfg.ContinueOnError {
				return aggregate, nil
			}
			if !vu.applyActionThinkTime(ctx, child) {
				interruptErr := actionThinkTimeInterruptedError(ctx)
				aggregate.Success = false
				aggregate.ErrorMessage = interruptErr.Error()
				aggregate.Duration = sequenceAggregateDuration(measuredDuration, start)
				return aggregate, interruptErr
			}
			continue
		}
		if result == nil {
			continue
		}
		vu.recordResult(result, errCfg, dsl.LogConfig{})
		measuredDuration += result.Duration
		copySequenceSourceResult(aggregate, result)
		for key, value := range result.Extractions {
			if err := protocol.StoreExtraction(aggregate, key, value); err != nil {
				aggregate.Success = false
				aggregate.Extractions = nil
				aggregate.ErrorMessage = fmt.Sprintf("http_sequence extraction retention failed: %v", err)
				aggregate.Duration = sequenceAggregateDuration(measuredDuration, start)
				return aggregate, nil
			}
		}
		if !result.Success {
			aggregate.Success = false
			aggregate.ErrorMessage = firstEngineValue(result.ErrorMessage, fmt.Sprintf("http_sequence child %d failed", index+1))
			if !errCfg.ContinueOnError {
				break
			}
		}
		if !vu.applyActionThinkTime(ctx, child) {
			interruptErr := actionThinkTimeInterruptedError(ctx)
			aggregate.Success = false
			aggregate.ErrorMessage = interruptErr.Error()
			aggregate.Duration = sequenceAggregateDuration(measuredDuration, start)
			return aggregate, interruptErr
		}
	}
	aggregate.Duration = measuredDuration
	return aggregate, nil
}

// sequenceAggregateDuration reports the duration of an http_sequence aggregate
// that is being returned as a failure. The success path reports the sum of the
// children's measured durations so think time is excluded from response time,
// but a sequence that fails before any child produced a result has no
// measurement to sum. Reporting zero there is not "instant": it pins
// ActionMetrics.MinDuration to 0 and feeds a 0 sample into the latency digest,
// silently skewing P50/P90 for the whole action. Fall back to elapsed wall time,
// which is what the pre-measurement implementation reported and what the
// parse-failure path above still reports.
func sequenceAggregateDuration(measured time.Duration, start time.Time) time.Duration {
	if measured > 0 {
		return measured
	}
	return time.Since(start)
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
		childMetadata := make(map[string]any, len(child.Metadata)+2)
		for key, metadataValue := range child.Metadata {
			childMetadata[key] = metadataValue
		}
		child.Metadata = childMetadata
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
		return validateHTTPSequenceChildAction(action)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return dsl.Action{}, err
	}
	var action dsl.Action
	if err := json.Unmarshal(payload, &action); err != nil {
		return dsl.Action{}, err
	}
	return validateHTTPSequenceChildAction(action)
}

func validateHTTPSequenceChildAction(action dsl.Action) (dsl.Action, error) {
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
	if result.SourceStepID == "" {
		result.SourceStepID = sourceMetadataString(metadata, "source_step_id")
	}
	if result.SourceStepIndex == 0 {
		result.SourceStepIndex = sourceMetadataInt(metadata, "source_step_index")
	}
	if result.SourceAction == "" {
		result.SourceAction = sourceMetadataString(metadata, "source_action")
	}
	if result.SourceActionName == "" {
		result.SourceActionName = sourceMetadataString(metadata, "source_action_name")
	}
	if result.SourceTransaction == "" {
		result.SourceTransaction = sourceTransactionName(metadata)
	}
	if result.SourceProfile == "" {
		result.SourceProfile = sourceMetadataString(metadata, "source_profile")
	}
}

// handleSnapshot stores an error snapshot if configured and the result is a failure.
func (vu *VUser) handleSnapshot(result *protocol.Result, errCfg dsl.ErrorHandlingConfig) {
	if errCfg.SnapshotOnError && !result.Success && result.Snapshot != nil && vu.SnapshotStore != nil {
		vu.SnapshotStore.Add(result.Snapshot)
	}
}
