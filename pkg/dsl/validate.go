package dsl

import (
	"fmt"
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

	for i, action := range script.Actions {
		if strings.TrimSpace(action.Name) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		if strings.TrimSpace(action.Type) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
			continue
		}
		if isEngineActionType(action.Type) {
			issues = append(issues, validateEngineActionIssues(i, action)...)
			continue
		}

		if script.Protocol == "browser" {
			issues = append(issues, validateBrowserActionIssues(i, action)...)
			continue
		}

		if script.Protocol == "websocket" {
			issues = append(issues, validateWebSocketActionIssues(i, action)...)
			continue
		}
		if script.Protocol == "grpc" {
			issues = append(issues, validateGRPCActionIssues(i, action)...)
			continue
		}

		if script.Protocol == "database" {
			issues = append(issues, validateDatabaseActionIssues(i, action)...)
			continue
		}

		if script.Protocol == "network" {
			issues = append(issues, validateNetworkActionIssues(i, action)...)
			continue
		}

		if action.Type == "http_sequence" {
			issues = append(issues, validateHTTPSequenceActionIssues(i, action)...)
			continue
		}

		if action.Target == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing target", i, action.Name))
		}
	}

	if script.Protocol == "soap" {
		issues = append(issues, validateSOAPScriptIssues(script)...)
	}
	if script.Protocol == "java_http" {
		issues = append(issues, validateJavaHTTPScriptIssues(script)...)
	}
	if script.Protocol == "database" {
		issues = append(issues, validateDatabaseScriptIssues(script)...)
	}
	if script.Protocol == "network" {
		issues = append(issues, validateNetworkScriptIssues(script)...)
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
	case "browser", "database", "network", "kafka":
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

func isEngineActionType(actionType string) bool {
	switch actionType {
	case "rendezvous", "error_message",
		"loadrunner_message", "loadrunner_runtime", "loadrunner_transaction", "loadrunner_timer",
		"loadrunner_metric", "loadrunner_parameter", "loadrunner_json", "loadrunner_xml",
		"loadrunner_database", "loadrunner_secret", "loadrunner_external", "loadrunner_review",
		"custom_call", "review_required":
		return true
	default:
		return false
	}
}

func validateEngineActionIssues(index int, action Action) []string {
	switch action.Type {
	case "rendezvous":
		return validateRendezvousActionIssues(index, action)
	case "error_message":
		label := fmt.Sprintf("actions[%d] %q", index, action.Name)
		if strings.TrimSpace(action.Value) == "" {
			if format, ok := actionParamStringValue(action.Params, "format"); ok && format != "" {
				return nil
			}
			return []string{fmt.Sprintf("%s: error_message requires value or params.format", label)}
		}
	}
	return nil
}

func validateGRPCActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	switch action.Type {
	case "grpc_unary", "grpc_server_stream", "grpc_client_stream", "grpc_bidi_stream":
		if strings.TrimSpace(action.Target) == "" {
			issues = append(issues, fmt.Sprintf("%s: missing target", label))
		} else if !strings.Contains(strings.Trim(action.Target, "/"), "/") {
			issues = append(issues, fmt.Sprintf("%s: %s target must be service/method", label, action.Type))
		}
		if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && timeoutMs < 0 {
			issues = append(issues, fmt.Sprintf("%s: %s timeout_ms must be zero or greater", label, action.Type))
		}
		if maxMessages, ok := actionParamIntValue(action.Params, "max_messages"); ok && maxMessages < 0 {
			issues = append(issues, fmt.Sprintf("%s: %s max_messages must be zero or greater", label, action.Type))
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: unsupported grpc action type %q", label, action.Type))
	}
	return issues
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
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	switch action.Type {
	case "ws_connect":
		if strings.TrimSpace(action.Target) == "" {
			issues = append(issues, fmt.Sprintf("%s: missing target", label))
		}
	case "ws_send":
		if action.Body == "" {
			issues = append(issues, fmt.Sprintf("%s: ws_send requires body", label))
		}
		if messageType, ok := actionParamStringValue(action.Params, "type"); ok && !isAllowedValue(messageType, "text", "binary") {
			issues = append(issues, fmt.Sprintf("%s: ws_send type must be text or binary", label))
		}
	case "ws_receive":
		if timeoutMs, ok := actionParamIntValue(action.Params, "timeout_ms"); ok && timeoutMs < 0 {
			issues = append(issues, fmt.Sprintf("%s: ws_receive timeout_ms must be zero or greater", label))
		}
	case "ws_close":
	case "ws_ping":
	default:
		issues = append(issues, fmt.Sprintf("%s: unsupported websocket action type %q", label, action.Type))
	}
	return issues
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
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	switch action.Type {
	case "rendezvous":
		return validateRendezvousActionIssues(index, action)
	case "browser_screenshot":
		return nil
	case "browser_wait":
		if action.WaitFor == nil && action.Selector == "" && action.Target == "" {
			issues = append(issues, fmt.Sprintf("%s: browser_wait requires wait_for, selector, or target", label))
		}
		return issues
	case "browser_navigate", "browser_click", "browser_fill", "browser_select", "browser_tap", "browser_long_press", "browser_swipe":
		if action.Target == "" && action.Selector == "" {
			issues = append(issues, fmt.Sprintf("%s: missing target or selector", label))
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: unsupported browser action type %q", label, action.Type))
		return issues
	}

	switch action.Type {
	case "browser_fill", "browser_select":
		if action.Value == "" {
			issues = append(issues, fmt.Sprintf("%s: %s requires value", label, action.Type))
		}
	case "browser_long_press":
		if durationMs, ok := actionParamIntValue(action.Params, "duration_ms"); ok && durationMs <= 0 {
			issues = append(issues, fmt.Sprintf("%s: browser_long_press duration_ms must be greater than zero", label))
		}
	case "browser_swipe":
		if direction, ok := actionParamStringValue(action.Params, "direction"); ok && !isAllowedValue(direction, "left", "right", "up", "down") {
			issues = append(issues, fmt.Sprintf("%s: browser_swipe direction must be left, right, up, or down", label))
		}
		if distance, ok := actionParamIntValue(action.Params, "distance"); ok && distance <= 0 {
			issues = append(issues, fmt.Sprintf("%s: browser_swipe distance must be greater than zero", label))
		}
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
	if !jms.Mock {
		if jms.BrokerURL == "" {
			issues = append(issues, "config.jms.broker_url is required when mock mode is disabled")
		}
		if jms.MessageVPN == "" {
			issues = append(issues, "config.jms.message_vpn is recommended when mock mode is disabled")
		}
		issues = append(issues, "real Solace JMS adapter is not available in this build; enable config.jms.mock for replay")
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
		switch action.Type {
		case "jms_publish", "jms_consume", "jms_request_reply", "jms_ack", "jms_commit", "jms_rollback":
		case "":
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
		default:
			issues = append(issues, fmt.Sprintf("actions[%d] %q: unsupported JMS action type %q", i, action.Name, action.Type))
		}
		if (action.Type == "jms_publish" || action.Type == "jms_consume" || action.Type == "jms_request_reply") && action.Target == "" && jms.Destination == "" {
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing destination", i, action.Name))
		}
		if action.Type == "jms_request_reply" && jms.ReplyTo == "" {
			replyTo, _ := action.Params["reply_to"].(string)
			if strings.TrimSpace(replyTo) == "" {
				issues = append(issues, fmt.Sprintf("actions[%d] %q: missing reply_to destination", i, action.Name))
			}
		}
	}

	return issues
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
		issues = append(issues, "real Kafka network adapter is not available in this build; enable config.kafka.mock for replay")
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
	if kafka.TimeoutMs < 0 {
		issues = append(issues, "config.kafka.timeout_ms must be zero or greater")
	}
	if kafka.Partitions < 0 {
		issues = append(issues, "config.kafka.partitions must be zero or greater")
	}

	for i, action := range script.Actions {
		if strings.TrimSpace(action.Name) == "" {
			issues = append(issues, fmt.Sprintf("actions[%d]: missing name", i))
		}
		switch action.Type {
		case "kafka_produce", "kafka_consume", "kafka_request_reply", "kafka_commit", "kafka_seek":
		case "":
			issues = append(issues, fmt.Sprintf("actions[%d] %q: missing type", i, action.Name))
		default:
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
		if !isAllowedValue(paramType, "file", "list", "random_number", "date_time", "timestamp", "counter", "current_iteration", "host", "time", "uuid", "random_string") {
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
	switch actionType {
	case "db_begin", "database_begin", "begin", "db_commit", "database_commit", "commit", "db_rollback", "database_rollback", "rollback", "db_health", "database_health", "ping":
	case "db_query", "database_query", "query", "select", "db_exec", "database_exec", "exec", "execute", "insert", "update", "delete", "db_procedure", "stored_procedure":
		if strings.TrimSpace(action.Target) == "" && strings.TrimSpace(action.Body) == "" {
			issues = append(issues, fmt.Sprintf("%s: database action requires target or body SQL", label))
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: unsupported database action type %q", label, action.Type))
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
	switch actionType {
	case "network_health":
	case "dns_query", "dns_lookup":
		if strings.TrimSpace(action.Target) == "" {
			issues = append(issues, fmt.Sprintf("%s: dns action requires target host", label))
		}
		if recordType, ok := actionParamStringValue(action.Params, "record_type"); ok && !isAllowedValue(strings.ToUpper(recordType), "A", "AAAA", "CNAME", "MX", "NS", "TXT") {
			issues = append(issues, fmt.Sprintf("%s: dns record_type must be A, AAAA, CNAME, MX, NS, or TXT", label))
		}
	case "tcp_request", "tcp_send", "winsock_tcp", "udp_request", "udp_send", "winsock_udp":
		if strings.TrimSpace(action.Target) == "" {
			issues = append(issues, fmt.Sprintf("%s: socket action requires target address", label))
		}
	case "ftp_command", "ftp_login", "ftp_noop", "smtp_send", "pop3_command", "imap_command":
		if strings.TrimSpace(action.Target) == "" && strings.TrimSpace(action.Value) == "" && strings.TrimSpace(action.Body) == "" {
			issues = append(issues, fmt.Sprintf("%s: %s requires target, value, or body", label, actionType))
		}
	case "ldap_bind":
	case "ldap_search":
		if strings.TrimSpace(action.Target) == "" {
			issues = append(issues, fmt.Sprintf("%s: ldap_search requires target base DN", label))
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: unsupported network action type %q", label, action.Type))
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
