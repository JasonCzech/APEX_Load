package dsl

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"

	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// ValidateScriptIssues returns all validation issues for a script definition.
func ValidateScriptIssues(script *Script) []string {
	if script == nil {
		return []string{"script is required"}
	}

	if strings.HasPrefix(strings.TrimSpace(script.Protocol), "jms-") {
		return validateJMSScriptIssues(script)
	}

	if strings.TrimSpace(script.Protocol) == "kafka" {
		return validateKafkaScriptIssues(script)
	}

	if strings.TrimSpace(script.Protocol) == "eventhubs" {
		return validateEventHubsScriptIssues(script)
	}

	var issues []string
	if strings.TrimSpace(script.Name) == "" {
		issues = append(issues, "script name is required")
	}
	if strings.TrimSpace(script.Protocol) == "" {
		issues = append(issues, "script protocol is required")
	}
	if len(script.Actions) == 0 {
		issues = append(issues, "script must have at least one action")
	}
	if script.Protocol != "" && !protocolAllowsEmptyBaseURL(script.Protocol) && strings.TrimSpace(script.Config.BaseURL) == "" {
		issues = append(issues, "config.base_url is empty")
	}

	for i, action := range script.Init {
		issues = append(issues, validateActionForProtocolAtPath(script.Protocol, fmt.Sprintf("init[%d]", i), i, action)...)
	}
	for i, action := range script.Actions {
		issues = append(issues, validateActionForProtocol(script.Protocol, i, action)...)
	}
	for i, action := range script.Teardown {
		issues = append(issues, validateActionForProtocolAtPath(script.Protocol, fmt.Sprintf("teardown[%d]", i), i, action)...)
	}

	if script.Protocol == "soap" {
		issues = append(issues, validateSOAPScriptIssues(script)...)
	}
	if script.Protocol == "java_http" {
		issues = append(issues, validateJavaHTTPScriptIssues(script)...)
	}
	if script.Protocol == "java_vuser" {
		issues = append(issues, validateJavaVuserScriptIssues(script)...)
	}
	if script.Protocol == "database" {
		issues = append(issues, validateDatabaseScriptIssues(script)...)
	}
	if script.Protocol == "network" {
		issues = append(issues, validateNetworkScriptIssues(script)...)
	}
	if script.Protocol == "sap_gui" {
		issues = append(issues, validateSAPGUIScriptIssues(script)...)
	}

	issues = append(issues, validateParameterIssues(script)...)
	issues = append(issues, validateBrowserConfigIssues(script)...)
	issues = append(issues, validateWebSocketConfigIssues(script)...)
	issues = append(issues, validateGRPCConfigIssues(script)...)
	issues = append(issues, validateWebRuntimeIssues(script)...)
	issues = append(issues, validateRunLogicIssues(script.Config.RunLogic)...)
	issues = append(issues, validateGoalConfigIssues(script.GoalConfig)...)

	return issues
}

func validateHTTPSequenceActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	requests, ok := action.Params["requests"]
	if !ok {
		return []string{fmt.Sprintf("%s: http_sequence requires params.requests", label)}
	}
	switch typed := requests.(type) {
	case []any:
		if len(typed) == 0 {
			issues = append(issues, fmt.Sprintf("%s: http_sequence requires at least one child request", label))
		}
		for requestIndex, value := range typed {
			request, ok := value.(map[string]any)
			if !ok {
				issues = append(issues, fmt.Sprintf("%s.params.requests[%d]: must be an object", label, requestIndex))
				continue
			}
			issues = append(issues, validateHTTPSequenceRequestIssues(label, requestIndex, request)...)
		}
	case []map[string]any:
		// In-memory form produced by importers before JSON serialization.
		if len(typed) == 0 {
			issues = append(issues, fmt.Sprintf("%s: http_sequence requires at least one child request", label))
		}
		for requestIndex, request := range typed {
			issues = append(issues, validateHTTPSequenceRequestIssues(label, requestIndex, request)...)
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: http_sequence params.requests must be an array", label))
	}
	return issues
}

func validateHTTPSequenceRequestIssues(label string, requestIndex int, request map[string]any) []string {
	var issues []string
	name := stringAnyValue(request["name"])
	method := strings.ToUpper(strings.TrimSpace(firstNonEmptyString(stringAnyValue(request["method"]), stringAnyValue(request["type"]))))
	target := stringAnyValue(request["target"])
	childLabel := fmt.Sprintf("%s.params.requests[%d] %q", label, requestIndex, name)
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD", "OPTIONS":
	default:
		issues = append(issues, fmt.Sprintf("%s: unsupported HTTP method/type %q", childLabel, method))
	}
	if strings.TrimSpace(target) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing target", childLabel))
	}
	// Child requests support body_file the same way top-level actions do; the
	// engine decodes each child into a dsl.Action whose HTTP protocol uses only
	// the inline body, so an unresolved body_file would silently send an empty
	// body. Apply the same conflict + load-context checks as validateBodyFileIssues.
	issues = append(issues, validateSequenceChildBodyFileIssues(childLabel, request)...)
	// Child requests carry full extractor objects (the importer's primary
	// output shape); run the same field checks as top-level actions so typos
	// fail validation instead of silently misbehaving at runtime. The JSON
	// round-trip uniformly handles the wire form ([]any of maps) and the
	// importer's in-memory form ([]Extractor).
	if raw, ok := request["extractors"]; ok && raw != nil {
		if payload, err := json.Marshal(raw); err == nil {
			var extractors []Extractor
			if json.Unmarshal(payload, &extractors) == nil {
				issues = append(issues, validateExtractorIssues(childLabel, Action{Extractors: extractors})...)
			}
		}
	}
	return issues
}

// validateSequenceChildBodyFileIssues mirrors validateBodyFileIssues for the
// untyped http_sequence child-request map form. body_file is only resolvable
// when loading scripts from a local file (see ResolveBodyRefs); API uploads
// must inline the body. Rejecting it here prevents the engine from silently
// sending an empty child body at runtime.
func validateSequenceChildBodyFileIssues(childLabel string, request map[string]any) []string {
	bodyFile := strings.TrimSpace(stringAnyValue(request["body_file"]))
	if bodyFile == "" {
		return nil
	}
	issues := make([]string, 0, 2)
	if strings.TrimSpace(stringAnyValue(request["body"])) != "" {
		issues = append(issues, fmt.Sprintf("%s: body and body_file cannot both be set", childLabel))
	}
	issues = append(issues, fmt.Sprintf("%s: body_file is only supported when loading scripts from a local file; inline body is required for API uploads", childLabel))
	return issues
}

func stringAnyValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	default:
		return ""
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func protocolAllowsEmptyBaseURL(protocolName string) bool {
	switch strings.TrimSpace(protocolName) {
	case "browser", "database", "network", "kafka", "eventhubs", "sap_gui", "java_vuser":
		return true
	default:
		return false
	}
}

func validateRunLogicIssues(runLogic RunLogicConfig) []string {
	var issues []string
	if runLogic.GlobalIterations < 0 {
		issues = append(issues, "config.run_logic.global_iterations must be zero or greater")
	}
	if runLogic.MaxTransactions < 0 {
		issues = append(issues, "config.run_logic.max_transactions must be zero or greater")
	}
	return issues
}

func validateGRPCConfigIssues(script *Script) []string {
	if script == nil || strings.TrimSpace(script.Protocol) != "grpc" {
		return nil
	}
	cfg := script.Config.GRPC
	var issues []string
	if cfg.MaxRecvMsgSize < 0 {
		issues = append(issues, "config.grpc.max_recv_msg_size must be zero or greater")
	}
	if cfg.TimeoutMs < 0 {
		issues = append(issues, "config.grpc.timeout_ms must be zero or greater")
	}
	for index, protoFile := range cfg.ProtoFiles {
		if strings.TrimSpace(protoFile) == "" {
			issues = append(issues, fmt.Sprintf("config.grpc.proto_files[%d] must be non-empty", index))
		}
	}
	return issues
}

// legacyActionTypePrefix is the pre-rename prefix for enterprise-source engine
// action types. Scripts persisted before the enterprise-archive rename use it
// (e.g. "loadrunner_transaction").
const legacyActionTypePrefix = "loadrunner_"

// CanonicalActionType normalizes a DSL action type, mapping legacy pre-rename
// "loadrunner_*" engine action types to their current "source_*" equivalents so
// scripts stored before the enterprise-archive rename continue to validate and
// execute. Types without the legacy prefix are returned unchanged.
func CanonicalActionType(actionType string) string {
	if strings.HasPrefix(actionType, legacyActionTypePrefix) {
		return "source_" + strings.TrimPrefix(actionType, legacyActionTypePrefix)
	}
	return actionType
}

func isEngineActionType(actionType string) bool {
	spec, ok := actionCatalogIndex()[CanonicalActionType(actionType)]
	return ok && spec.EngineAction()
}

// validateActionForProtocol dispatches a single action to the engine or the
// protocol-specific validator. It is used for top-level actions and, recursively,
// for the nested bodies of control_if / control_loop blocks.
func validateActionForProtocol(protocol string, index int, action Action) []string {
	return validateActionForProtocolAtPath(protocol, fmt.Sprintf("actions[%d]", index), index, action)
}

func validateActionForProtocolAtPath(protocol string, path string, index int, action Action) []string {
	var issues []string
	if strings.TrimSpace(action.Name) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing name", path))
	}
	issues = append(issues, validateBodyFileIssues(path, action)...)
	issues = append(issues, validateExtractorIssues(path, action)...)
	if strings.TrimSpace(action.Type) == "" {
		return append(issues, fmt.Sprintf("%s: missing type", actionLabel(path, action)))
	}
	if isEngineActionType(action.Type) {
		return append(issues, validateEngineActionIssues(protocol, path, index, action)...)
	}
	rebased := func(nestedIssues []string) []string {
		return rebaseActionIssues(fmt.Sprintf("actions[%d]", index), path, nestedIssues)
	}
	switch protocol {
	case "browser":
		return append(issues, rebased(validateBrowserActionIssues(index, action))...)
	case "websocket":
		return append(issues, rebased(validateWebSocketActionIssues(index, action))...)
	case "grpc":
		return append(issues, rebased(validateGRPCActionIssues(index, action))...)
	case "database":
		return append(issues, rebased(validateDatabaseActionIssues(index, action))...)
	case "network":
		return append(issues, rebased(validateNetworkActionIssues(index, action))...)
	case "sap_gui":
		return append(issues, rebased(validateSAPGUIActionIssues(index, action))...)
	case "java_vuser":
		return append(issues, rebased(validateJavaVuserActionIssues(index, action))...)
	}
	if action.Type == "http_sequence" {
		return append(issues, rebased(validateHTTPSequenceActionIssues(index, action))...)
	}
	if CanonicalActionType(action.Type) == "http_oauth2_token" {
		return issues
	}
	// JMS and Kafka actions resolve their destination/topic from script config
	// (config.jms.destination / config.kafka.topic) when target is omitted, so the
	// generic "missing target" rule must not apply to them — otherwise a JMS/Kafka
	// action nested in a control_if/control_loop body is falsely flagged as
	// "missing target" even though the config-level destination is set. The
	// destination-aware checks live in the top-level JMS/Kafka validators.
	if ProtocolFamily(protocol) == "jms" || protocol == "kafka" {
		return issues
	}
	if action.Target == "" {
		issues = append(issues, fmt.Sprintf("%s: missing target", actionLabel(path, action)))
	}
	return issues
}

func validateBodyFileIssues(path string, action Action) []string {
	if strings.TrimSpace(action.BodyFile) == "" {
		return nil
	}
	label := actionLabel(path, action)
	issues := make([]string, 0, 2)
	if strings.TrimSpace(action.Body) != "" {
		issues = append(issues, fmt.Sprintf("%s: body and body_file cannot both be set", label))
	}
	issues = append(issues, fmt.Sprintf("%s: body_file is only supported when loading scripts from a local file; inline body is required for API uploads", label))
	return issues
}

// validateExtractorIssues checks the optional extractor behavior fields so
// typos fail validation instead of silently misbehaving at runtime.
func validateExtractorIssues(path string, action Action) []string {
	var issues []string
	label := actionLabel(path, action)
	for i, ext := range action.Extractors {
		switch ext.OnMiss {
		case "", "ignore", "fail", "empty":
		default:
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] on_miss must be \"\", \"ignore\", \"empty\", or \"fail\" (got %q)", label, i, ext.OnMiss))
		}
		switch ext.Mode {
		case "", "all":
		default:
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] mode must be \"\" or \"all\" (got %q)", label, i, ext.Mode))
		}
		switch ext.Convert {
		case "", "url_encode", "html_decode", "html_url_encode":
		default:
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] convert must be \"\", \"url_encode\", \"html_decode\", or \"html_url_encode\" (got %q)", label, i, ext.Convert))
		}
		if ext.SaveOffset < 0 {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] save_offset must be zero or greater", label, i))
		}
		if ext.SaveLength < 0 {
			issues = append(issues, fmt.Sprintf("%s: extractors[%d] save_length must be zero or greater", label, i))
		}
	}
	return issues
}

func validateEngineActionIssues(protocol string, path string, index int, action Action) []string {
	switch action.Type {
	case "rendezvous":
		return rebaseActionIssues(fmt.Sprintf("actions[%d]", index), path, validateRendezvousActionIssues(index, action))
	case "control_if", "control_loop":
		return validateControlFlowIssues(protocol, path, action)
	case "error_message":
		label := actionLabel(path, action)
		if strings.TrimSpace(action.Value) == "" {
			if format, ok := actionParamStringValue(action.Params, "format"); ok && format != "" {
				return nil
			}
			return []string{fmt.Sprintf("%s: error_message requires value or params.format", label)}
		}
	}
	return nil
}

// validateControlFlowIssues validates a control_if / control_loop block and
// recursively validates its nested step bodies against the script protocol.
func validateControlFlowIssues(protocol string, path string, action Action) []string {
	label := actionLabel(path, action)
	if action.Control == nil {
		return []string{fmt.Sprintf("%s: %s requires a control block", label, action.Type)}
	}
	var issues []string
	if action.Control.Count < 0 {
		issues = append(issues, fmt.Sprintf("%s: control count must be zero or greater", label))
	}
	if action.Control.MaxIterations < 0 {
		issues = append(issues, fmt.Sprintf("%s: control max_iterations must be zero or greater", label))
	}
	issues = append(issues, validateConditionIssues(label, action.Control.Condition)...)
	switch action.Type {
	case "control_loop":
		// Loop mode precedence: for_each > count > condition.
		if action.Control.ForEach != nil {
			if strings.TrimSpace(action.Control.ForEach.Source) == "" {
				issues = append(issues, fmt.Sprintf("%s: control_loop for_each requires a source", label))
			}
			issues = append(issues, validateConditionIssues(label, action.Control.ForEach.Condition)...)
		} else if action.Control.Count <= 0 && action.Control.Condition == nil {
			issues = append(issues, fmt.Sprintf("%s: control_loop requires a for_each source, a positive count, or a condition", label))
		}
	case "control_if":
		if len(action.Control.Steps) == 0 && len(action.Control.Else) == 0 {
			issues = append(issues, fmt.Sprintf("%s: control_if requires steps or else", label))
		}
	}
	for j, nested := range action.Control.Steps {
		issues = append(issues, validateActionForProtocolAtPath(protocol, fmt.Sprintf("%s.control.steps[%d]", path, j), j, nested)...)
	}
	for j, nested := range action.Control.Else {
		issues = append(issues, validateActionForProtocolAtPath(protocol, fmt.Sprintf("%s.control.else[%d]", path, j), j, nested)...)
	}
	return issues
}

func validateConditionIssues(label string, condition *Condition) []string {
	if condition == nil {
		return nil
	}
	if expr := strings.TrimSpace(condition.Expr); expr != "" {
		if err := ValidateConditionExpression(expr); err != nil {
			return []string{fmt.Sprintf("%s: condition expr is invalid: %v", label, err)}
		}
		return nil
	}
	op := strings.ToLower(strings.TrimSpace(condition.Op))
	if op == "" {
		op = "eq"
	}
	if !isAllowedValue(op, "eq", "==", "equals", "ne", "!=", "not_equals", "contains", "not_contains", "gt", "lt", "gte", "lte", ">", "<", ">=", "<=", "exists", "truthy", "not_exists", "falsy", "empty") {
		return []string{fmt.Sprintf("%s: condition op %q is not supported", label, condition.Op)}
	}
	if strings.TrimSpace(condition.Left) == "" {
		return []string{fmt.Sprintf("%s: condition left operand is required", label)}
	}
	switch op {
	case "exists", "truthy", "not_exists", "falsy", "empty":
		return nil
	default:
		if strings.TrimSpace(condition.Right) == "" {
			return []string{fmt.Sprintf("%s: condition right operand is required for op %q", label, condition.Op)}
		}
	}
	return nil
}

func actionLabel(path string, action Action) string {
	if strings.TrimSpace(action.Name) == "" {
		return path
	}
	return fmt.Sprintf("%s %q", path, action.Name)
}

func rebaseActionIssues(oldPath, newPath string, issues []string) []string {
	if len(issues) == 0 {
		return nil
	}
	if oldPath == newPath {
		return issues
	}
	rebased := make([]string, len(issues))
	for i, issue := range issues {
		if strings.HasPrefix(issue, oldPath) {
			rebased[i] = newPath + strings.TrimPrefix(issue, oldPath)
		} else {
			rebased[i] = newPath + ": " + issue
		}
	}
	return rebased
}

func validateGRPCActionIssues(index int, action Action) []string {
	spec, ok := protocolActionSpec("grpc", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported grpc action type %q", index, action.Name, action.Type)}
	}
	if spec.Validate == nil {
		return nil
	}
	return spec.Validate(index, action)
}

func validateWebSocketConfigIssues(script *Script) []string {
	if script == nil || strings.TrimSpace(script.Protocol) != "websocket" {
		return nil
	}
	cfg := script.Config.WebSocket
	var issues []string
	if cfg.ReadTimeoutMs < 0 {
		issues = append(issues, "config.websocket.read_timeout_ms must be zero or greater")
	}
	if cfg.WriteTimeoutMs < 0 {
		issues = append(issues, "config.websocket.write_timeout_ms must be zero or greater")
	}
	if cfg.MaxMessageSize < 0 {
		issues = append(issues, "config.websocket.max_message_size must be zero or greater")
	}
	return issues
}

func validateWebSocketActionIssues(index int, action Action) []string {
	spec, ok := protocolActionSpec("websocket", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported websocket action type %q", index, action.Name, action.Type)}
	}
	if spec.Validate == nil {
		return nil
	}
	return spec.Validate(index, action)
}

func validateGoalConfigIssues(goal *GoalConfig) []string {
	if goal == nil {
		return nil
	}

	var issues []string
	if goal.P95LatencyMs != nil && *goal.P95LatencyMs <= 0 {
		issues = append(issues, "goal_config.p95_latency_ms must be greater than zero")
	}
	if goal.MaxErrorPct != nil && (*goal.MaxErrorPct < 0 || *goal.MaxErrorPct > 100) {
		issues = append(issues, "goal_config.max_error_pct must be between 0 and 100")
	}
	if goal.MaxAvgServerProcessingMs != nil && *goal.MaxAvgServerProcessingMs <= 0 {
		issues = append(issues, "goal_config.max_avg_server_processing_ms must be greater than zero")
	}
	if goal.MaxAvgTransferMs != nil && *goal.MaxAvgTransferMs <= 0 {
		issues = append(issues, "goal_config.max_avg_transfer_ms must be greater than zero")
	}
	if goal.MinVUsers != nil && *goal.MinVUsers < 1 {
		issues = append(issues, "goal_config.min_vusers must be at least 1")
	}
	if goal.MaxVUsers != nil && *goal.MaxVUsers < 1 {
		issues = append(issues, "goal_config.max_vusers must be at least 1")
	}
	if goal.MinVUsers != nil && goal.MaxVUsers != nil && *goal.MinVUsers > *goal.MaxVUsers {
		issues = append(issues, "goal_config.min_vusers cannot exceed goal_config.max_vusers")
	}
	if goal.MaxErrorCount != nil && *goal.MaxErrorCount < 1 {
		issues = append(issues, "goal_config.max_error_count must be at least 1")
	}
	if goal.SLABreachWindow != "" {
		if _, err := time.ParseDuration(goal.SLABreachWindow); err != nil {
			issues = append(issues, fmt.Sprintf("goal_config.sla_breach_window is invalid: %v", err))
		}
	}
	if goal.StopOnSLABreach && goal.P95LatencyMs == nil && goal.MaxErrorPct == nil && goal.MaxAvgServerProcessingMs == nil && goal.MaxAvgTransferMs == nil {
		issues = append(issues, "goal_config.stop_on_sla_breach requires a latency, error, server processing, or transfer threshold")
	}
	if goal.StopOnMaxErrors && goal.MaxErrorCount == nil {
		issues = append(issues, "goal_config.stop_on_max_errors requires goal_config.max_error_count")
	}

	return issues
}

func validateBrowserConfigIssues(script *Script) []string {
	if script == nil || strings.TrimSpace(script.Protocol) != "browser" {
		return nil
	}

	browser := script.Config.Browser
	var issues []string
	trimmedEngine := strings.TrimSpace(browser.Engine)
	trimmedDeviceProfile := strings.TrimSpace(browser.DeviceProfile)
	if trimmedEngine != "" && !isAllowedValue(trimmedEngine, "chromium", "webkit") {
		issues = append(issues, "config.browser.engine must be chromium or webkit")
	}
	if trimmedDeviceProfile != "" {
		profile, ok := browserprotocol.LookupDevice(trimmedDeviceProfile)
		if !ok {
			issues = append(issues, fmt.Sprintf("config.browser.device_profile %q is not recognized", trimmedDeviceProfile))
		} else if trimmedEngine != "" && profile.BrowserFamily != "" && trimmedEngine != profile.BrowserFamily {
			issues = append(issues, fmt.Sprintf("config.browser.engine %q is incompatible with device_profile %q (requires %s)", trimmedEngine, trimmedDeviceProfile, profile.BrowserFamily))
		}
	}
	if browser.Orientation != "" && !isAllowedValue(browser.Orientation, "portrait", "landscape") {
		issues = append(issues, "config.browser.orientation must be portrait or landscape")
	}
	if browser.Viewport.Width < 0 {
		issues = append(issues, "config.browser.viewport.width must be zero or greater")
	}
	if browser.Viewport.Height < 0 {
		issues = append(issues, "config.browser.viewport.height must be zero or greater")
	}
	return issues
}

func validateBrowserActionIssues(index int, action Action) []string {
	if action.Type == "rendezvous" {
		return validateRendezvousActionIssues(index, action)
	}
	spec, ok := protocolActionSpec("browser", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported browser action type %q", index, action.Name, action.Type)}
	}
	if spec.Validate == nil {
		return nil
	}
	return spec.Validate(index, action)
}

func validateBrowserSyncPolicyIssues(label string, sync *BrowserSyncPolicy) []string {
	if sync == nil {
		return nil
	}
	var issues []string
	syncType := strings.ToLower(strings.TrimSpace(sync.Type))
	if syncType == "" {
		syncType = "network_idle"
	}
	if !isAllowedValue(syncType, "network_idle", "networkidle", "dom_stable", "element_visible", "visible", "duration", "sleep", "load", "domcontentloaded") {
		issues = append(issues, fmt.Sprintf("%s: sync.type %q is not supported", label, sync.Type))
	}
	if sync.TimeoutMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: sync.timeout_ms must be zero or greater", label))
	}
	if sync.StableMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: sync.stable_ms must be zero or greater", label))
	}
	if (syncType == "element_visible" || syncType == "visible") && strings.TrimSpace(sync.Value) == "" {
		issues = append(issues, fmt.Sprintf("%s: sync.value is required for element_visible", label))
	}
	if (syncType == "duration" || syncType == "sleep") && strings.TrimSpace(sync.Value) == "" {
		issues = append(issues, fmt.Sprintf("%s: sync.value is required for duration", label))
	}
	return issues
}

func validateRendezvousActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	if count, ok := actionParamIntValue(action.Params, "count"); ok && count <= 0 {
		issues = append(issues, fmt.Sprintf("%s: rendezvous count must be greater than zero", label))
	}
	if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && timeoutMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: rendezvous timeout_ms must be zero or greater", label))
	}
	return issues
}

func actionParamIntValue(params map[string]any, key string) (int, bool) {
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

func actionParamStringValue(params map[string]any, key string) (string, bool) {
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

// ValidateScriptError returns the first validation issue as an error.
func ValidateScriptError(script *Script) error {
	issues := ValidateScriptIssues(script)
	if len(issues) == 0 {
		return nil
	}
	return fmt.Errorf("%s", issues[0])
}

func validateJMSScriptIssues(script *Script) []string {
	var issues []string
	if strings.TrimSpace(script.Name) == "" {
		issues = append(issues, "script name is required")
	}
	if strings.TrimSpace(script.Protocol) == "" {
		issues = append(issues, "script protocol is required")
	}
	if len(script.Actions) == 0 {
		issues = append(issues, "script must have at least one action")
	}

	jms := script.Config.JMS
	serviceBus := isServiceBusJMSScript(script)
	if !jms.Mock {
		if jms.BrokerURL == "" {
			issues = append(issues, "config.jms.broker_url is required when mock mode is disabled")
		}
		if jms.MessageVPN == "" && !serviceBus {
			issues = append(issues, "config.jms.message_vpn is recommended when mock mode is disabled")
		}
		if strings.TrimSpace(script.Protocol) == "jms-solace" && !SolaceJMSRealAdapterAvailable() {
			issues = append(issues, "real Solace JMS adapter is not available in this build; enable config.jms.mock for replay or build with -tags jms_solace_real")
		}
		if serviceBus && !ServiceBusJMSRealAdapterAvailable() {
			issues = append(issues, "real Azure Service Bus JMS adapter is not available in this build; enable config.jms.mock for replay or build with -tags jms_servicebus_real")
		}
	}
	if jms.DeliveryMode != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(jms.DeliveryMode)), "persistent", "non_persistent") {
		issues = append(issues, fmt.Sprintf("config.jms.delivery_mode %q is not supported", jms.DeliveryMode))
	}
	if jms.Acknowledgement != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(jms.Acknowledgement)), "auto", "client", "dups_ok", "transacted") {
		issues = append(issues, fmt.Sprintf("config.jms.acknowledgement %q is not supported", jms.Acknowledgement))
	}
	if jms.DestinationType != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(jms.DestinationType)), "queue", "topic") {
		issues = append(issues, fmt.Sprintf("config.jms.destination_type %q is not supported", jms.DestinationType))
	}
	if jms.Destination == "" {
		usesActionDestinations := true
		for _, action := range script.Actions {
			switch action.Type {
			case "jms_publish", "jms_consume", "jms_request_reply":
				if action.Target == "" {
					usesActionDestinations = false
				}
			}
		}
		if !usesActionDestinations {
			issues = append(issues, "config.jms.destination or per-action target is required")
		}
	}

	for i, action := range script.Actions {
		if strings.TrimSpace(action.Name) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		issues = append(issues, validateBodyFileIssues(fmt.Sprintf("actions[%d]", i), action)...)
		if action.Type == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
		} else if isEngineActionType(action.Type) {
			issues = append(issues, validateEngineActionIssues(script.Protocol, fmt.Sprintf("actions[%d]", i), i, action)...)
		} else if _, ok := protocolActionSpec("jms", action.Type); !ok {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: unsupported JMS action type %q", i, action.Name, action.Type))
		}
		if (action.Type == "jms_publish" || action.Type == "jms_consume" || action.Type == "jms_request_reply" || action.Type == "jms_browse") && action.Target == "" && jms.Destination == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing destination", i, action.Name))
		}
		if action.Type == "jms_request_reply" && jms.ReplyTo == "" {
			replyTo, _ := action.Params["reply_to"].(string)
			if strings.TrimSpace(replyTo) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: missing reply_to destination", i, action.Name))
			}
		}
		if serviceBus && (action.Type == "jms_consume" || action.Type == "jms_request_reply") && jmsActionUsesServiceBusTopicWithoutSubscription(jms, action) {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: subscription required to consume from a Service Bus topic", i, action.Name))
		}
		// JMS message-type model + delivery controls (P2.3) on send actions.
		if action.Type == "jms_publish" || action.Type == "jms_request_reply" {
			if mt, ok := actionParamStringValue(action.Params, "message_type"); ok &&
				!isAllowedValue(strings.ToLower(strings.TrimSpace(mt)), "text", "bytes", "map", "stream", "object") {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: message_type %q is not supported", i, action.Name, mt))
			}
			if dm, ok := actionParamStringValue(action.Params, "delivery_mode"); ok &&
				!isAllowedValue(strings.ToLower(strings.TrimSpace(dm)), "persistent", "non_persistent") {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: delivery_mode %q is not supported", i, action.Name, dm))
			}
			if _, present := action.Params["priority"]; present {
				if p, ok := actionParamIntValue(action.Params, "priority"); !ok || p < 0 || p > 9 {
					issues = append(issues, fmt.Sprintf("actions[%d] %q: priority must be an integer 0-9", i, action.Name))
				}
			}
		}
		// Service Bus advanced verbs (P2.4): validate enums + required tokens.
		if action.Type == "jms_consume" {
			if sq, ok := actionParamStringValue(action.Params, "sub_queue"); ok && strings.TrimSpace(sq) != "" &&
				!isAllowedValue(strings.ToLower(strings.TrimSpace(sq)), "none", "dead_letter", "transfer_dead_letter") {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: sub_queue %q is not supported", i, action.Name, sq))
			}
			// A selector (Solace/AMQP data-plane filter) is mutually exclusive with
			// the Service Bus advanced-consume controls (P2.3).
			if sel, ok := actionParamStringValue(action.Params, "selector"); ok && strings.TrimSpace(sel) != "" {
				_, hasSubQueue := actionParamStringValue(action.Params, "sub_queue")
				_, hasSession := actionParamStringValue(action.Params, "session_token")
				peekLock, _ := action.Params["peek_lock"].(bool)
				if hasSubQueue || hasSession || peekLock {
					issues = append(issues, fmt.Sprintf("actions[%d] %q: selector cannot be combined with peek_lock, sub_queue, or session_token", i, action.Name))
				}
			}
		}
		if action.Type == "jms_settle" {
			if disp, ok := actionParamStringValue(action.Params, "disposition"); ok && strings.TrimSpace(disp) != "" &&
				!isAllowedValue(strings.ToLower(strings.TrimSpace(disp)), "complete", "abandon", "dead_letter", "defer") {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: disposition %q is not supported", i, action.Name, disp))
			}
			if tok, _ := actionParamStringValue(action.Params, "settlement_token"); strings.TrimSpace(tok) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: settlement_token is required", i, action.Name))
			}
		}
		if action.Type == "jms_renew_lock" {
			if tok, _ := actionParamStringValue(action.Params, "settlement_token"); strings.TrimSpace(tok) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: settlement_token is required", i, action.Name))
			}
		}
		if action.Type == "jms_session_state" {
			if tok, _ := actionParamStringValue(action.Params, "session_token"); strings.TrimSpace(tok) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: session_token is required", i, action.Name))
			}
		}
		if action.Type == "jms_receive_deferred" || action.Type == "jms_cancel_scheduled" {
			if _, ok := action.Params["sequence_numbers"]; !ok {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: sequence_numbers is required", i, action.Name))
			}
		}
	}

	return issues
}

func isServiceBusJMSScript(script *Script) bool {
	if strings.EqualFold(strings.TrimSpace(script.Protocol), "jms-servicebus") {
		return true
	}
	return strings.EqualFold(strings.TrimSpace(script.Config.JMS.Provider), "servicebus")
}

func jmsActionUsesServiceBusTopicWithoutSubscription(jms JMSConfig, action Action) bool {
	destinationType, ok := actionParamStringValue(action.Params, "destination_type")
	if !ok {
		destinationType = jms.DestinationType
	}
	if !strings.EqualFold(strings.TrimSpace(destinationType), "topic") {
		return false
	}
	destination := jmsActionConsumeDestination(jms, action)
	if jmsDestinationIncludesServiceBusSubscription(destination) {
		return false
	}
	subscription, ok := actionParamStringValue(action.Params, "subscription")
	if !ok {
		subscription = jms.Subscription
	}
	return strings.TrimSpace(subscription) == ""
}

func jmsActionConsumeDestination(jms JMSConfig, action Action) string {
	if action.Type == "jms_request_reply" {
		if replyTo, ok := actionParamStringValue(action.Params, "reply_to"); ok {
			return replyTo
		}
		return jms.ReplyTo
	}
	if destination, ok := actionParamStringValue(action.Params, "destination"); ok {
		return destination
	}
	if strings.TrimSpace(action.Target) != "" {
		return action.Target
	}
	return jms.Destination
}

func jmsDestinationIncludesServiceBusSubscription(destination string) bool {
	trimmed := strings.Trim(strings.TrimSpace(destination), "/")
	lower := strings.ToLower(trimmed)
	const marker = "/subscriptions/"
	idx := strings.Index(lower, marker)
	if idx < 0 {
		return false
	}
	return strings.Trim(trimmed[:idx], "/") != "" && strings.Trim(trimmed[idx+len(marker):], "/") != ""
}

func validateSAPGUIScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.SAPGUI
	if cfg.SyncTimeoutMs < 0 {
		issues = append(issues, "config.sapgui.sync_timeout_ms must be zero or greater")
	}
	if cfg.SessionBehavior != "" && !isAllowedValue(cfg.SessionBehavior, "attach", "new_session", "reuse") {
		issues = append(issues, "config.sapgui.session_behavior must be attach, new_session, or reuse")
	}
	if cfg.Password != "" && !strings.HasPrefix(cfg.Password, "{{secret.") {
		issues = append(issues, "config.sapgui.password must be a secret template")
	}
	return issues
}

func validateSAPGUIActionIssues(index int, action Action) []string {
	spec, ok := protocolActionSpec("sap_gui", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported SAP GUI action type %q", index, action.Name, action.Type)}
	}
	if spec.Validate != nil {
		return spec.Validate(index, action)
	}
	return nil
}

func validateKafkaScriptIssues(script *Script) []string {
	var issues []string
	if strings.TrimSpace(script.Name) == "" {
		issues = append(issues, "script name is required")
	}
	if strings.TrimSpace(script.Protocol) == "" {
		issues = append(issues, "script protocol is required")
	}
	if len(script.Actions) == 0 {
		issues = append(issues, "script must have at least one action")
	}

	kafka := script.Config.Kafka
	if !kafka.Mock {
		if len(kafka.Brokers) == 0 {
			issues = append(issues, "config.kafka.brokers is required when mock mode is disabled")
		}
		if !KafkaRealAdapterAvailable() {
			issues = append(issues, "real Kafka network adapter is not available in this build; enable config.kafka.mock for replay or build with -tags kafka_real")
		}
	}
	profile := strings.ToLower(strings.TrimSpace(kafka.Profile))
	if profile != "" && profile != "eventhubs" {
		issues = append(issues, fmt.Sprintf("config.kafka.profile %q is not supported (use \"eventhubs\")", kafka.Profile))
	}
	if profile == "eventhubs" && !kafka.Mock {
		// Azure Event Hubs requires TLS (sasl_ssl) and rejects plaintext transport.
		if secProto := strings.ToLower(strings.TrimSpace(kafka.SecurityProtocol)); secProto != "" && secProto != "sasl_ssl" {
			issues = append(issues, fmt.Sprintf("config.kafka.profile eventhubs requires security_protocol sasl_ssl, got %q", kafka.SecurityProtocol))
		}
		switch strings.ToLower(strings.TrimSpace(kafka.SASLMechanism)) {
		case "", "plain":
			if kafka.SASLPassword == "" && kafka.SASLPasswordSecret == "" {
				issues = append(issues, "config.kafka.profile eventhubs with PLAIN auth requires config.kafka.sasl_password or sasl_password_secret (the namespace connection string)")
			}
		case "oauthbearer":
			// Microsoft Entra ID / OAUTHBEARER auth (EH.2): credentials come from
			// the Azure environment (DefaultAzureCredential), so no SAS password
			// is required here.
		default:
			issues = append(issues, fmt.Sprintf("config.kafka.profile eventhubs supports sasl_mechanism plain or oauthbearer, got %q", kafka.SASLMechanism))
		}
	}
	if strings.EqualFold(strings.TrimSpace(kafka.SASLMechanism), "oauthbearer") {
		// OAUTHBEARER must run over TLS. The eventhubs profile forces sasl_ssl at
		// runtime, so accept it even when security_protocol is left unset.
		eventHubsProfile := strings.EqualFold(strings.TrimSpace(kafka.Profile), "eventhubs")
		if !eventHubsProfile && !strings.Contains(strings.ToLower(kafka.SecurityProtocol), "ssl") {
			issues = append(issues, "config.kafka.sasl_mechanism oauthbearer requires a TLS security_protocol (e.g. sasl_ssl)")
		}
	}
	if kafka.SecurityProtocol != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.SecurityProtocol)), "plaintext", "ssl", "sasl_ssl", "sasl_plaintext") {
		issues = append(issues, fmt.Sprintf("config.kafka.security_protocol %q is not supported", kafka.SecurityProtocol))
	}
	if kafka.SASLMechanism != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.SASLMechanism)), "plain", "scram-sha-256", "scram-sha-512", "oauthbearer") {
		issues = append(issues, fmt.Sprintf("config.kafka.sasl_mechanism %q is not supported", kafka.SASLMechanism))
	}
	if kafka.Acks != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.Acks)), "none", "leader", "all") {
		issues = append(issues, fmt.Sprintf("config.kafka.acks %q is not supported", kafka.Acks))
	}
	if kafka.Compression != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.Compression)), "none", "gzip", "snappy", "lz4", "zstd") {
		issues = append(issues, fmt.Sprintf("config.kafka.compression %q is not supported", kafka.Compression))
	}
	if kafka.PartitionStrategy != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.PartitionStrategy)), "key-hash", "round-robin", "explicit") {
		issues = append(issues, fmt.Sprintf("config.kafka.partition_strategy %q is not supported", kafka.PartitionStrategy))
	}
	if kafka.AutoOffsetReset != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.AutoOffsetReset)), "earliest", "latest") {
		issues = append(issues, fmt.Sprintf("config.kafka.auto_offset_reset %q is not supported", kafka.AutoOffsetReset))
	}
	if kafka.Idempotence != "" {
		idem := strings.ToLower(strings.TrimSpace(kafka.Idempotence))
		if !isAllowedValue(idem, "enabled", "disabled") {
			issues = append(issues, fmt.Sprintf("config.kafka.idempotence %q is not supported (use enabled or disabled)", kafka.Idempotence))
		} else if idem == "enabled" {
			// Idempotent writes require acks=all (the default when acks is unset).
			if acks := strings.ToLower(strings.TrimSpace(kafka.Acks)); acks != "" && acks != "all" {
				issues = append(issues, fmt.Sprintf("config.kafka.idempotence enabled requires acks=all, got %q", kafka.Acks))
			}
		}
	}
	if kafka.IsolationLevel != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(kafka.IsolationLevel)), "read_uncommitted", "read_committed") {
		issues = append(issues, fmt.Sprintf("config.kafka.isolation_level %q is not supported", kafka.IsolationLevel))
	}
	if strings.TrimSpace(kafka.TransactionalID) != "" {
		// Transactional (EOS) producers require acks=all and idempotence.
		if acks := strings.ToLower(strings.TrimSpace(kafka.Acks)); acks != "" && acks != "all" {
			issues = append(issues, fmt.Sprintf("config.kafka.transactional_id requires acks=all, got %q", kafka.Acks))
		}
		if strings.ToLower(strings.TrimSpace(kafka.Idempotence)) == "disabled" {
			issues = append(issues, "config.kafka.transactional_id requires idempotence (remove idempotence: disabled)")
		}
	}
	if kafka.Serialization != "" {
		switch strings.ToLower(strings.TrimSpace(kafka.Serialization)) {
		case "raw":
		case "json_schema":
			if strings.TrimSpace(kafka.SchemaRegistryURL) == "" && kafka.SchemaID <= 0 {
				issues = append(issues, "config.kafka.serialization json_schema requires config.kafka.schema_registry_url or schema_id")
			}
		case "avro":
			if strings.TrimSpace(kafka.SchemaRegistryURL) == "" && kafka.SchemaID <= 0 {
				issues = append(issues, "config.kafka.serialization avro requires config.kafka.schema_registry_url or schema_id")
			}
			if strings.TrimSpace(kafka.Schema) == "" {
				issues = append(issues, "config.kafka.serialization avro requires config.kafka.schema (the Avro schema)")
			}
		default:
			issues = append(issues, fmt.Sprintf("config.kafka.serialization %q is not supported (raw, json_schema, or avro; protobuf in a later release)", kafka.Serialization))
		}
	}
	if kafka.TimeoutMs < 0 {
		issues = append(issues, "config.kafka.timeout_ms must be zero or greater")
	}
	if kafka.Partitions < 0 {
		issues = append(issues, "config.kafka.partitions must be zero or greater")
	}
	if kafka.MaxMessageBytes < 0 {
		issues = append(issues, "config.kafka.max_message_bytes must be zero or greater")
	}

	for i, action := range script.Actions {
		if strings.TrimSpace(action.Name) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		issues = append(issues, validateBodyFileIssues(fmt.Sprintf("actions[%d]", i), action)...)
		if action.Type == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
		} else if _, ok := protocolActionSpec("kafka", action.Type); !ok {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: unsupported Kafka action type %q", i, action.Name, action.Type))
		}
		if (action.Type == "kafka_produce" || action.Type == "kafka_consume" || action.Type == "kafka_request_reply") && action.Target == "" && kafka.Topic == "" {
			if topic, _ := action.Params["topic"].(string); strings.TrimSpace(topic) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: missing topic", i, action.Name))
			}
		}
		if action.Type == "kafka_request_reply" {
			replyTo, _ := action.Params["reply_to"].(string)
			if strings.TrimSpace(replyTo) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: missing reply_to topic", i, action.Name))
			}
		}
	}

	return issues
}

// validateEventHubsScriptIssues validates the native Azure Event Hubs protocol
// (config.eventhubs + eventhubs_* actions). It is producer-first: eventhubs_produce
// is the primary action, eventhubs_consume is a read-only observer probe.
func validateEventHubsScriptIssues(script *Script) []string {
	var issues []string
	if strings.TrimSpace(script.Name) == "" {
		issues = append(issues, "script name is required")
	}
	if strings.TrimSpace(script.Protocol) == "" {
		issues = append(issues, "script protocol is required")
	}
	if len(script.Actions) == 0 {
		issues = append(issues, "script must have at least one action")
	}

	eh := script.Config.EventHubs
	if !eh.Mock {
		// A non-mock run needs a reachable namespace and a real adapter. The
		// connection string may be supplied inline or via a secret reference.
		hasNamespace := strings.TrimSpace(eh.Namespace) != ""
		hasConnString := strings.TrimSpace(eh.ConnectionString) != "" || strings.TrimSpace(eh.ConnectionStringSecret) != ""
		if !hasNamespace && !hasConnString {
			issues = append(issues, "config.eventhubs requires namespace (AAD) or connection_string/connection_string_secret (SAS) when mock mode is disabled")
		}
		if !EventHubsRealAdapterAvailable() {
			issues = append(issues, "real Azure Event Hubs adapter is not available in this build; enable config.eventhubs.mock for shakeout or build with -tags eventhubs_real")
		}
	}
	if eh.AuthType != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(eh.AuthType)), "connection_string", "aad") {
		issues = append(issues, fmt.Sprintf("config.eventhubs.auth_type %q is not supported (use connection_string or aad)", eh.AuthType))
	}
	if eh.TimeoutMs < 0 {
		issues = append(issues, "config.eventhubs.timeout_ms must be zero or greater")
	}

	for i, action := range script.Actions {
		if strings.TrimSpace(action.Name) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		issues = append(issues, validateBodyFileIssues(fmt.Sprintf("actions[%d]", i), action)...)
		if action.Type == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
		} else if _, ok := protocolActionSpec("eventhubs", action.Type); !ok {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: unsupported Event Hubs action type %q", i, action.Name, action.Type))
		}
		if action.Type == "eventhubs_produce" || action.Type == "eventhubs_consume" {
			eventHub, _ := action.Params["event_hub"].(string)
			if strings.TrimSpace(action.Target) == "" && strings.TrimSpace(eventHub) == "" && strings.TrimSpace(eh.EventHub) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: missing event hub (set target, params.event_hub, or config.eventhubs.event_hub)", i, action.Name))
			}
		}
		// partition_key and partition_id are mutually exclusive routing controls.
		if pk, _ := action.Params["partition_key"].(string); strings.TrimSpace(pk) != "" {
			if pid, _ := action.Params["partition_id"].(string); strings.TrimSpace(pid) != "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: partition_key and partition_id are mutually exclusive", i, action.Name))
			}
		}
	}

	return issues
}

func validateJavaVuserScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.JavaVuser
	if cfg.MaxProcesses < 0 {
		issues = append(issues, "config.java_vuser.max_processes must be zero or greater")
	}
	if cfg.StartupTimeoutMs < 0 {
		issues = append(issues, "config.java_vuser.startup_timeout_ms must be zero or greater")
	}
	if cfg.ActionTimeoutMs < 0 {
		issues = append(issues, "config.java_vuser.action_timeout_ms must be zero or greater")
	}
	return issues
}

func validateJavaVuserActionIssues(index int, action Action) []string {
	spec, ok := protocolActionSpec("java_vuser", action.Type)
	if !ok {
		return []string{fmt.Sprintf("actions[%d] %q: unsupported Java Vuser action type %q", index, action.Name, action.Type)}
	}
	if spec.Validate != nil {
		return spec.Validate(index, action)
	}
	return nil
}

func validateParameterIssues(script *Script) []string {
	if len(script.Parameters) == 0 {
		return nil
	}

	var issues []string
	parametersByName := make(map[string]Parameter, len(script.Parameters))
	for _, param := range script.Parameters {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		normalized := strings.ToLower(name)
		if _, exists := parametersByName[normalized]; !exists {
			parametersByName[normalized] = param
		}
	}

	seen := make(map[string]struct{}, len(script.Parameters))
	for i, param := range script.Parameters {
		label := fmt.Sprintf("parameters[%d]", i)
		name := strings.TrimSpace(param.Name)
		if name == "" {
			issues = append(issues, fmt.Sprintf("%s: name is required", label))
		} else {
			if _, ok := seen[name]; ok {
				issues = append(issues, fmt.Sprintf("%s %q: duplicate name", label, name))
			}
			seen[name] = struct{}{}
		}

		paramType := CanonicalParameterType(param.Type)
		if paramType == "" {
			issues = append(issues, fmt.Sprintf("%s %q: type is required", label, name))
			continue
		}
		if !isAllowedValue(paramType, "file", "list", "random_number", "date_time", "timestamp", "counter", "current_iteration", "vuser_id", "host", "time", "uuid", "random_string") {
			issues = append(issues, fmt.Sprintf("%s %q: unsupported type %q", label, name, param.Type))
		}

		if param.Update != "" && !isAllowedValue(param.Update, "once", "each_iteration", "each_occurrence") {
			issues = append(issues, fmt.Sprintf("%s %q: update must be once, each_iteration, or each_occurrence", label, name))
		}
		if param.Selection != "" && !isAllowedValue(param.Selection, "sequential", "random", "unique") {
			issues = append(issues, fmt.Sprintf("%s %q: selection must be sequential, random, or unique", label, name))
		}
		if param.OnExhaustion != "" && !isAllowedValue(param.OnExhaustion, "wrap", "empty", "error", "last") {
			issues = append(issues, fmt.Sprintf("%s %q: on_exhaustion must be wrap, empty, error, or last", label, name))
		}
		if sameLineAs := strings.TrimSpace(param.SameLineAs); sameLineAs != "" {
			reference, ok := parametersByName[strings.ToLower(sameLineAs)]
			if !ok {
				issues = append(issues, fmt.Sprintf("%s %q: same_line_as references unknown parameter %q", label, name, sameLineAs))
			} else if strings.EqualFold(strings.TrimSpace(reference.Name), name) {
				issues = append(issues, fmt.Sprintf("%s %q: same_line_as cannot reference itself", label, name))
			} else if paramType != "file" && paramType != "list" {
				issues = append(issues, fmt.Sprintf("%s %q: same_line_as requires a file or list parameter", label, name))
			} else if strings.TrimSpace(param.DataSource) == "" || strings.TrimSpace(reference.DataSource) == "" || !strings.EqualFold(strings.TrimSpace(param.DataSource), strings.TrimSpace(reference.DataSource)) {
				issues = append(issues, fmt.Sprintf("%s %q: same_line_as must reference a parameter in the same data_source", label, name))
			}
		}

		switch paramType {
		case "file":
			if strings.TrimSpace(param.DataSource) == "" {
				issues = append(issues, fmt.Sprintf("%s %q: data_source is required for file parameters", label, name))
			}
			if strings.TrimSpace(param.Column) == "" {
				issues = append(issues, fmt.Sprintf("%s %q: column is required for file parameters", label, name))
			}
		case "list":
			if len(param.Values) == 0 && strings.TrimSpace(param.DataSource) == "" {
				issues = append(issues, fmt.Sprintf("%s %q: values or data_source is required for list parameters", label, name))
			}
			if strings.TrimSpace(param.DataSource) != "" && strings.TrimSpace(param.Column) == "" {
				issues = append(issues, fmt.Sprintf("%s %q: column is required when data_source is set", label, name))
			}
		case "random_number":
			if param.Min > param.Max {
				issues = append(issues, fmt.Sprintf("%s %q: min must be less than or equal to max", label, name))
			}
		case "date_time", "time":
			validateParameterTimezone(&issues, label, name, param.Timezone)
		case "timestamp":
			validateParameterTimezone(&issues, label, name, param.Timezone)
			if param.Format != "" && !isAllowedValue(param.Format, "unix", "unix_ms", "iso8601", "rfc3339", "rfc3339_nano") {
				issues = append(issues, fmt.Sprintf("%s %q: format must be unix, unix_ms, iso8601, rfc3339, or rfc3339_nano", label, name))
			}
		case "counter":
			if param.Step == 0 {
				issues = append(issues, fmt.Sprintf("%s %q: step must be non-zero for counter parameters", label, name))
			}
		case "random_string":
			if param.Length <= 0 {
				issues = append(issues, fmt.Sprintf("%s %q: length must be greater than zero for random_string parameters", label, name))
			}
		}
	}

	return issues
}

func validateParameterTimezone(issues *[]string, label, name, timezone string) {
	if strings.TrimSpace(timezone) == "" {
		return
	}
	if _, err := time.LoadLocation(timezone); err != nil {
		*issues = append(*issues, fmt.Sprintf("%s %q: invalid timezone %q", label, name, timezone))
	}
}

func validateWebRuntimeIssues(script *Script) []string {
	var issues []string

	runLogic := script.Config.RunLogic
	if runLogic.BlockScheduling != "" && !isAllowedValue(runLogic.BlockScheduling, "sequential", "random", "percentage") {
		issues = append(issues, "config.run_logic.block_scheduling must be sequential, random, or percentage")
	}
	for index, step := range runLogic.ActionSequence {
		if !isAllowedValue(step, "init", "action", "teardown", "vuser_init", "vuser_end") {
			issues = append(issues, fmt.Sprintf("config.run_logic.action_sequence[%d] must be init, action, teardown, vuser_init, or vuser_end", index))
		}
	}

	for key := range script.Config.WebRuntime.AdditionalAttributes {
		if strings.TrimSpace(key) == "" {
			issues = append(issues, "config.web_runtime.additional_attributes keys must be non-empty")
			break
		}
	}

	// Source-IP pool (LR-style "IP spoofing" / multiple client IPs). Each entry binds
	// VUser connections to that local address, so it must parse as a real IP — an
	// invalid address would otherwise be silently dropped and the spoofing skipped.
	for index, raw := range script.Config.WebRuntime.SourceIPs {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			issues = append(issues, fmt.Sprintf("config.web_runtime.source_ips[%d] must not be empty", index))
			continue
		}
		if net.ParseIP(trimmed) == nil {
			issues = append(issues, fmt.Sprintf("config.web_runtime.source_ips[%d] %q is not a valid IP address", index, raw))
		}
	}

	logCfg := script.Config.Log
	for index, option := range logCfg.ExtendedOptions {
		if !isAllowedValue(option, "parameter_substitution", "data_returned", "advanced_trace") {
			issues = append(issues, fmt.Sprintf("config.log.extended_options[%d] must be parameter_substitution, data_returned, or advanced_trace", index))
		}
	}

	httpCfg := script.Config.HTTP
	if httpCfg.ProxyMode != "" && !isAllowedValue(httpCfg.ProxyMode, "none", "system", "custom") {
		issues = append(issues, "config.http.proxy_mode must be none, system, or custom")
	}
	if httpCfg.ProxyMode == "custom" && strings.TrimSpace(httpCfg.Proxy.URL) == "" {
		issues = append(issues, "config.http.proxy.url is required when config.http.proxy_mode is custom")
	}

	browserCfg := script.Config.Browser
	if browserCfg.DownloadResources != "" && !isAllowedValue(browserCfg.DownloadResources, "none", "recorded", "all") {
		issues = append(issues, "config.browser.download_resources must be none, recorded, or all")
	}

	filters := script.Config.WebRuntime.DownloadFilters
	if filters.Mode != "" && !isAllowedValue(filters.Mode, "none", "allow_list", "block_list") {
		issues = append(issues, "config.web_runtime.download_filters.mode must be none, allow_list, or block_list")
	}
	if (filters.Mode == "allow_list" || filters.Mode == "block_list") && len(filters.Hosts) == 0 {
		issues = append(issues, "config.web_runtime.download_filters.hosts is required when download filter mode is allow_list or block_list")
	}

	for index, check := range script.Config.WebRuntime.ContentChecks {
		if strings.TrimSpace(check.Text) == "" {
			issues = append(issues, fmt.Sprintf("config.web_runtime.content_checks[%d].text is required", index))
		}
		if check.Scope != "" && !isAllowedValue(check.Scope, "any_response", "main_document", "resource") {
			issues = append(issues, fmt.Sprintf("config.web_runtime.content_checks[%d].scope must be any_response, main_document, or resource", index))
		}
	}

	network := script.Config.WebRuntime.NetworkSimulation
	if network.Mode != "" && !isAllowedValue(network.Mode, "max", "preset", "custom") {
		issues = append(issues, "config.web_runtime.network_simulation.mode must be max, preset, or custom")
	}
	if network.Mode == "preset" && !isAllowedValue(network.Preset, "56k", "dsl", "t1", "10mbps") {
		issues = append(issues, "config.web_runtime.network_simulation.preset must be 56k, dsl, t1, or 10mbps when mode is preset")
	}
	if network.Mode == "custom" && network.BandwidthKbps <= 0 && network.LatencyMs <= 0 && network.JitterMs <= 0 && network.PacketLossPct <= 0 {
		issues = append(issues, "config.web_runtime.network_simulation custom mode requires bandwidth_kbps, latency_ms, jitter_ms, or packet_loss_pct")
	}
	if network.LatencyMs < 0 {
		issues = append(issues, "config.web_runtime.network_simulation.latency_ms must be zero or greater")
	}
	if network.JitterMs < 0 {
		issues = append(issues, "config.web_runtime.network_simulation.jitter_ms must be zero or greater")
	}
	if network.PacketLossPct < 0 || network.PacketLossPct > 100 {
		issues = append(issues, "config.web_runtime.network_simulation.packet_loss_pct must be between 0 and 100")
	}

	streaming := script.Config.WebRuntime.Streaming
	if streaming.Mode != "" && !isAllowedValue(streaming.Mode, "adaptive", "fixed") {
		issues = append(issues, "config.web_runtime.streaming.mode must be adaptive or fixed")
	}
	if streaming.BufferSizeSeconds < 0 {
		issues = append(issues, "config.web_runtime.streaming.buffer_size_seconds must be zero or greater")
	}
	if streaming.Mode == "fixed" && streaming.FixedBitrateKbps <= 0 {
		issues = append(issues, "config.web_runtime.streaming.fixed_bitrate_kbps must be greater than zero when mode is fixed")
	}
	if streaming.BandwidthEstimation != "" && !isAllowedValue(streaming.BandwidthEstimation, "rolling", "instant") {
		issues = append(issues, "config.web_runtime.streaming.bandwidth_estimation must be rolling or instant")
	}

	for index, metric := range script.Config.Observability.ExternalMetrics {
		prefix := fmt.Sprintf("config.observability.external_metrics[%d]", index)
		if strings.TrimSpace(metric.Name) == "" {
			issues = append(issues, prefix+".name is required")
		}
		if strings.TrimSpace(metric.URL) == "" {
			issues = append(issues, prefix+".url is required")
		} else if parsed, err := url.ParseRequestURI(metric.URL); err != nil || parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
			issues = append(issues, prefix+".url must be a valid http or https URL")
		}
		format := strings.TrimSpace(metric.Format)
		if format != "" && !isAllowedValue(format, "prometheus") {
			issues = append(issues, prefix+".format must be prometheus")
		}
		if strings.TrimSpace(metric.MetricName) == "" {
			issues = append(issues, prefix+".metric_name is required")
		}
	}

	for index, transform := range script.Config.WebRuntime.DataFormatting.Chain {
		transformType := strings.TrimSpace(transform.Type)
		if transformType == "" {
			issues = append(issues, fmt.Sprintf("config.web_runtime.data_formatting.chain[%d].type is required", index))
			continue
		}
		if !isAllowedDataFormattingTransform(transformType) {
			issues = append(issues, fmt.Sprintf("config.web_runtime.data_formatting.chain[%d].type %q is not supported", index, transform.Type))
		}
	}

	return issues
}

func validateJavaHTTPScriptIssues(script *Script) []string {
	var issues []string
	javaCfg := script.Config.JavaHTTP
	if strings.TrimSpace(javaCfg.Codec) == "" {
		issues = append(issues, "config.java_http.codec is required")
	} else if javaCfg.Codec != "java_serialized_object_base64" {
		issues = append(issues, fmt.Sprintf("config.java_http.codec %q is not supported", javaCfg.Codec))
	}
	if javaCfg.HelperMode != "" && !isAllowedValue(javaCfg.HelperMode, "embedded", "sidecar") {
		issues = append(issues, "config.java_http.helper_mode must be embedded or sidecar")
	}
	if javaCfg.BodyEncoding != "" && !isAllowedValue(javaCfg.BodyEncoding, "base64") {
		issues = append(issues, "config.java_http.body_encoding must be base64")
	}
	return issues
}

func validateDatabaseScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.Database
	if strings.TrimSpace(cfg.Driver) == "" {
		issues = append(issues, "config.database.driver is required")
	} else if !isAllowedValue(strings.ToLower(strings.TrimSpace(cfg.Driver)), "sqlite", "sqlite3", "postgres", "mysql", "odbc", "oracle") {
		issues = append(issues, fmt.Sprintf("config.database.driver %q is not supported", cfg.Driver))
	}
	if strings.TrimSpace(cfg.DSN) == "" {
		issues = append(issues, "config.database.dsn is required")
	}
	if cfg.MaxOpenConns < 0 {
		issues = append(issues, "config.database.max_open_conns must be zero or greater")
	}
	if cfg.MaxIdleConns < 0 {
		issues = append(issues, "config.database.max_idle_conns must be zero or greater")
	}
	if cfg.ConnMaxLifetimeMs < 0 {
		issues = append(issues, "config.database.conn_max_lifetime_ms must be zero or greater")
	}
	if cfg.QueryTimeoutMs < 0 {
		issues = append(issues, "config.database.query_timeout_ms must be zero or greater")
	}
	if cfg.MaxRows < 0 {
		issues = append(issues, "config.database.max_rows must be zero or greater")
	}
	return issues
}

func validateDatabaseActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	actionType := strings.ToLower(strings.TrimSpace(action.Type))
	spec, ok := protocolActionSpec("database", actionType)
	if !ok {
		issues = append(issues, fmt.Sprintf("%s: unsupported database action type %q", label, action.Type))
	} else if spec.Validate != nil {
		issues = append(issues, spec.Validate(index, action)...)
	}
	if maxRows, ok := actionParamIntValue(action.Params, "max_rows"); ok && maxRows < 0 {
		issues = append(issues, fmt.Sprintf("%s: max_rows must be zero or greater", label))
	}
	return issues
}

func validateNetworkScriptIssues(script *Script) []string {
	var issues []string
	cfg := script.Config.Network
	if cfg.Subprotocol != "" && !isAllowedValue(strings.ToLower(strings.TrimSpace(cfg.Subprotocol)), "dns", "ftp", "smtp", "pop3", "imap", "ldap", "winsock", "tcp", "udp") {
		issues = append(issues, fmt.Sprintf("config.network.subprotocol %q is not supported", cfg.Subprotocol))
	}
	if cfg.Port < 0 {
		issues = append(issues, "config.network.port must be zero or greater")
	}
	if cfg.TimeoutMs < 0 {
		issues = append(issues, "config.network.timeout_ms must be zero or greater")
	}
	for index, recipient := range cfg.To {
		if strings.TrimSpace(recipient) == "" {
			issues = append(issues, fmt.Sprintf("config.network.to[%d] must be non-empty", index))
		}
	}
	return issues
}

func validateNetworkActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	actionType := strings.ToLower(strings.TrimSpace(action.Type))
	spec, ok := protocolActionSpec("network", actionType)
	if !ok {
		issues = append(issues, fmt.Sprintf("%s: unsupported network action type %q", label, action.Type))
	} else if spec.Validate != nil {
		issues = append(issues, spec.Validate(index, action)...)
	}
	if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && timeoutMs < 0 {
		issues = append(issues, fmt.Sprintf("%s: timeout_ms must be zero or greater", label))
	}
	if readBytes, ok := actionParamIntValue(action.Params, "read_bytes"); ok && readBytes < 0 {
		issues = append(issues, fmt.Sprintf("%s: read_bytes must be zero or greater", label))
	}
	if maxEntries, ok := actionParamIntValue(action.Params, "max_entries"); ok && maxEntries < 0 {
		issues = append(issues, fmt.Sprintf("%s: max_entries must be zero or greater", label))
	}
	return issues
}

func isAllowedValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}

func isAllowedDataFormattingTransform(value string) bool {
	return isAllowedValue(value,
		"base64",
		"gzip",
		"json_to_xml",
		"xml_to_json",
		"url_decode",
		"url_encode",
		"html_unescape",
		"html_escape",
	)
}

func validateSOAPScriptIssues(script *Script) []string {
	var issues []string

	if version := strings.TrimSpace(script.Config.SOAP.Version); version != "" && version != "1.1" && version != "1.2" {
		issues = append(issues, "config.soap.version must be 1.1 or 1.2")
	}

	if ws := script.Config.SOAP.WSSecurity; ws != nil {
		if token := ws.UsernameToken; token != nil {
			if strings.TrimSpace(token.Username) == "" {
				issues = append(issues, "config.soap.ws_security.username_token.username is required")
			}
			if strings.TrimSpace(token.Password) == "" {
				issues = append(issues, "config.soap.ws_security.username_token.password is required")
			}
			if passwordType := strings.TrimSpace(token.PasswordType); passwordType != "" && passwordType != "text" && passwordType != "digest" {
				issues = append(issues, "config.soap.ws_security.username_token.password_type must be text or digest")
			}
		}
		if timestamp := ws.Timestamp; timestamp != nil && timestamp.TTLSeconds < 0 {
			issues = append(issues, "config.soap.ws_security.timestamp.ttl_seconds must be zero or greater")
		}
	}

	for i, action := range script.Actions {
		if action.SOAP == nil {
			continue
		}
		if bodyMode := strings.TrimSpace(action.SOAP.BodyMode); bodyMode != "" && bodyMode != "raw_xml" && bodyMode != "generated" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: soap.body_mode must be raw_xml or generated", i, action.Name))
		}
	}

	return issues
}
