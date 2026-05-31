package loadrunner

import (
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func ConvertJMS(actions []*ParsedAction, opts ConvertOptions, detected DetectedProtocol) (*ConvertResult, error) {
	result := newMessagingConvertResult(opts, detected, "Converted_LR_JMS_Script", "jms-solace")
	result.Script.Config.JMS = dsl.JMSConfig{
		Provider:        "solace",
		BrokerURL:       "{{secret.jms_broker_url}}",
		Destination:     "{{jms.destination}}",
		DestinationType: "queue",
		TimeoutMs:       5000,
		Mock:            true,
	}
	return convertMessagingFunctions(result, actions, func(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
		return convertJMSFunction(fn)
	}, "JMS")
}

func ConvertWebSocket(actions []*ParsedAction, opts ConvertOptions, detected DetectedProtocol) (*ConvertResult, error) {
	result := newMessagingConvertResult(opts, detected, "Converted_LR_WebSocket_Script", "websocket")
	result.Script.Config.BaseURL = firstNonEmpty(opts.BaseURL, "ws://{{websocket.host}}")
	result.Script.Config.WebSocket = dsl.WebSocketConfig{
		ReadTimeoutMs:  30000,
		WriteTimeoutMs: 10000,
		Reconnect:      true,
	}
	return convertMessagingFunctions(result, actions, func(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
		return convertWebSocketFunction(fn)
	}, "WebSocket")
}

func ConvertGRPC(actions []*ParsedAction, opts ConvertOptions, detected DetectedProtocol) (*ConvertResult, error) {
	result := newMessagingConvertResult(opts, detected, "Converted_LR_GRPC_Script", "grpc")
	result.Script.Config.BaseURL = firstNonEmpty(opts.BaseURL, "{{grpc.target}}")
	result.Script.Config.GRPC = dsl.GRPCConfig{
		UseReflection: true,
		PlainText:     true,
		TimeoutMs:     30000,
	}
	return convertMessagingFunctions(result, actions, func(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
		return convertGRPCFunction(fn)
	}, "gRPC")
}

func newMessagingConvertResult(opts ConvertOptions, detected DetectedProtocol, defaultName string, protocolName string) *ConvertResult {
	return &ConvertResult{
		DetectedProtocol: &detected,
		Warnings:         append([]string(nil), detected.Warnings...),
		Script: &dsl.Script{
			Name:          firstNonEmpty(opts.ScriptName, defaultName),
			Version:       "1.0.0",
			Protocol:      protocolName,
			GeneratedBy:   "apexload-loadrunner-converter",
			GeneratedFrom: "loadrunner",
			Config:        dsl.ScriptConfig{},
			Actions:       make([]dsl.Action, 0),
		},
	}
}

func convertMessagingFunctions(result *ConvertResult, actions []*ParsedAction, converter func(ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool), display string) (*ConvertResult, error) {
	functions := flattenParsedFunctions(actions)
	result.Stats.TotalFunctions = len(functions)
	if len(functions) == 0 {
		return nil, fmt.Errorf("no LoadRunner functions found in action files")
	}

	for _, fn := range functions {
		action, mode, ok := converter(fn)
		if !ok {
			if fallback, mode, fallbackOK := convertLoadRunnerFunctionAction(fn); fallbackOK {
				appendLoadRunnerAction(result, fn, fallback, mode, loadRunnerSupportNotes(fn, mode))
				continue
			}
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			appendSourceReviewMapping(result, sourceReviewMappingForFunction(fn, "metadata.import.preserved.unsupported_functions", fn.Name, "unsupported", "review_only", "No executable "+display+" mapping exists for this LoadRunner function yet."))
			continue
		}
		appendConvertedProtocolAction(result, fn, action, mode, "Converted to a generated APEX Load "+display+" action.")
	}

	if len(result.Script.Actions) == 0 {
		detected := markDetectedReviewOnly(*result.DetectedProtocol, "No executable "+display+" actions were found.")
		result.DetectedProtocol = &detected
		result.Script = nil
		result.ReviewArtifact = NewReviewArtifact(detected, nil, actions, map[string]any{"reason": "no_messaging_actions"})
		result.Warnings = append(result.Warnings, "No executable "+display+" actions were found; import was preserved as a review artifact.")
		return result, nil
	}
	result.Warnings = append(result.Warnings, display+" endpoints were imported with governed placeholders; replace them before replay.")
	return result, nil
}

func convertJMSFunction(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	if !strings.HasPrefix(name, "jms_") {
		return dsl.Action{}, "", false
	}
	if strings.Contains(name, "connect") || strings.Contains(name, "disconnect") || strings.Contains(name, "create") || strings.Contains(name, "init") || strings.Contains(name, "close") {
		return messagingNativeAction(fn), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
	}
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "jms_action")),
		Target:   firstNonEmpty(messagingDestination(fn), "{{jms.destination}}"),
		Body:     messagingBody(fn),
		Params:   map[string]any{"timeout_ms": 5000},
		Metadata: sourceFunctionMetadata(fn),
	}
	if replyTo := messagingArg(fn, "ReplyTo"); replyTo != "" {
		action.Params["reply_to"] = replyTo
	}
	switch {
	case strings.Contains(name, "request") && strings.Contains(name, "reply"):
		action.Type = "jms_request_reply"
		if _, ok := action.Params["reply_to"]; !ok {
			action.Params["reply_to"] = "{{jms.reply_to}}"
		}
	case strings.Contains(name, "receive") || strings.Contains(name, "consume") || strings.Contains(name, "read") || strings.Contains(name, "get"):
		action.Type = "jms_consume"
		action.Body = ""
	case strings.Contains(name, "ack"):
		action.Type = "jms_ack"
		action.Body = ""
	case strings.Contains(name, "commit"):
		action.Type = "jms_commit"
		action.Body = ""
	case strings.Contains(name, "rollback"):
		action.Type = "jms_rollback"
		action.Body = ""
	case strings.Contains(name, "send") || strings.Contains(name, "publish") || strings.Contains(name, "produce") || strings.Contains(name, "write"):
		action.Type = "jms_publish"
	default:
		return messagingNativeAction(fn), supportModeForFunction(fn, LoadRunnerSupportReviewOnly), true
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action, LoadRunnerSupportExecutable, true
}

func convertWebSocketFunction(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	if !strings.HasPrefix(name, "ws_") && !strings.HasPrefix(name, "websocket_") {
		return dsl.Action{}, "", false
	}
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "websocket_action")),
		Target:   firstNonEmpty(messagingTarget(fn), "/"),
		Body:     messagingBody(fn),
		Params:   map[string]any{},
		Metadata: sourceFunctionMetadata(fn),
	}
	switch {
	case strings.Contains(name, "connect") || strings.Contains(name, "open"):
		action.Type = "ws_connect"
	case strings.Contains(name, "send") || strings.Contains(name, "write"):
		action.Type = "ws_send"
		action.Params["type"] = firstNonEmpty(messagingArg(fn, "Type"), "text")
	case strings.Contains(name, "receive") || strings.Contains(name, "read"):
		action.Type = "ws_receive"
		action.Body = ""
		action.Params["timeout_ms"] = 30000
	case strings.Contains(name, "ping"):
		action.Type = "ws_ping"
	case strings.Contains(name, "close") || strings.Contains(name, "disconnect"):
		action.Type = "ws_close"
		action.Target = ""
		action.Body = ""
	default:
		return messagingNativeAction(fn), supportModeForFunction(fn, LoadRunnerSupportReviewOnly), true
	}
	if len(action.Params) == 0 {
		action.Params = nil
	}
	return action, LoadRunnerSupportExecutable, true
}

func convertGRPCFunction(fn ParsedFunction) (dsl.Action, LoadRunnerSupportMode, bool) {
	name := strings.ToLower(strings.TrimSpace(fn.Name))
	if !strings.HasPrefix(name, "grpc_") {
		return dsl.Action{}, "", false
	}
	if strings.Contains(name, "connect") || strings.Contains(name, "close") || strings.Contains(name, "disconnect") || strings.Contains(name, "init") {
		return messagingNativeAction(fn), supportModeForFunction(fn, LoadRunnerSupportNativeNoop), true
	}
	action := dsl.Action{
		Name:     sanitizeName(firstNonEmpty(fn.StepName, fn.Name, "grpc_action")),
		Target:   grpcMethodTarget(fn),
		Body:     firstNonEmpty(messagingBody(fn), "{}"),
		Params:   map[string]any{"timeout_ms": 30000},
		Metadata: sourceFunctionMetadata(fn),
	}
	switch {
	case strings.Contains(name, "server_stream"):
		action.Type = "grpc_server_stream"
	case strings.Contains(name, "client_stream"):
		action.Type = "grpc_client_stream"
	case strings.Contains(name, "bidi"):
		action.Type = "grpc_bidi_stream"
	case strings.Contains(name, "invoke") || strings.Contains(name, "call") || strings.Contains(name, "unary") || strings.Contains(name, "request"):
		action.Type = "grpc_unary"
	default:
		return messagingNativeAction(fn), supportModeForFunction(fn, LoadRunnerSupportReviewOnly), true
	}
	return action, LoadRunnerSupportExecutable, true
}

func messagingNativeAction(fn ParsedFunction) dsl.Action {
	def, ok := lookupLoadRunnerFunction(fn.Name)
	if !ok {
		def = LoadRunnerFunctionDefinition{Name: strings.ToLower(fn.Name), Family: "messaging", SupportMode: LoadRunnerSupportReviewOnly, Reason: defaultLoadRunnerReason("messaging", LoadRunnerSupportReviewOnly)}
	}
	return convertGenericLoadRunnerAction(fn, "loadrunner_messaging", def)
}

func messagingArg(fn ParsedFunction, key string) string {
	for argKey, value := range fn.Args {
		if strings.EqualFold(argKey, key) {
			return strings.TrimSpace(value)
		}
	}
	for _, arg := range fn.CallArgs {
		if strings.EqualFold(arg.Name, key) {
			return strings.TrimSpace(arg.Value)
		}
	}
	prefix := strings.ToLower(key) + "="
	for _, raw := range fn.RawArgs {
		if strings.HasPrefix(strings.ToLower(raw), prefix) {
			return strings.TrimSpace(raw[len(prefix):])
		}
	}
	return ""
}

func messagingDestination(fn ParsedFunction) string {
	for _, key := range []string{"Destination", "Queue", "Topic", "Channel", "Subject"} {
		if value := messagingArg(fn, key); value != "" {
			return convertLoadRunnerMessageExpression(value)
		}
	}
	args := loadRunnerControlArgs(fn)
	for _, arg := range args {
		trimmed := strings.TrimSpace(arg)
		if trimmed == "" || strings.Contains(trimmed, "=") || strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
			continue
		}
		return convertLoadRunnerMessageExpression(trimmed)
	}
	return ""
}

func messagingTarget(fn ParsedFunction) string {
	for _, key := range []string{"URL", "Target", "Endpoint", "Path"} {
		if value := messagingArg(fn, key); value != "" {
			return convertLoadRunnerMessageExpression(value)
		}
	}
	return messagingDestination(fn)
}

func messagingBody(fn ParsedFunction) string {
	for _, key := range []string{"Body", "Message", "Payload", "Data", "Text", "Request"} {
		if value := messagingArg(fn, key); value != "" {
			return convertLoadRunnerMessageExpression(value)
		}
	}
	args := loadRunnerControlArgs(fn)
	for i := 1; i < len(args); i++ {
		arg := strings.TrimSpace(args[i])
		if arg == "" || strings.Contains(arg, "=") {
			continue
		}
		return convertLoadRunnerMessageExpression(arg)
	}
	return ""
}

func grpcMethodTarget(fn ParsedFunction) string {
	if service := firstNonEmpty(messagingArg(fn, "Service"), messagingArg(fn, "Package")); service != "" {
		if method := firstNonEmpty(messagingArg(fn, "Method"), messagingArg(fn, "MethodName"), messagingArg(fn, "RPC")); method != "" {
			return strings.Trim(service, "/") + "/" + strings.Trim(method, "/")
		}
	}
	for _, key := range []string{"FullMethod", "Target", "Method", "RPC"} {
		if value := messagingArg(fn, key); value != "" && strings.Contains(strings.Trim(value, "/"), "/") {
			return strings.Trim(value, "/")
		}
	}
	args := loadRunnerControlArgs(fn)
	for _, arg := range args {
		arg = strings.Trim(arg, "/")
		if strings.Contains(arg, "/") {
			return arg
		}
	}
	return "package.Service/Method"
}
