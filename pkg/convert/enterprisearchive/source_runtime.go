package enterprisearchive

// source_runtime.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
)

func applySourceContinueOnError(result *ConvertResult, fn ParsedFunction) {
	value := firstSourceControlArg(fn)
	details := map[string]any{
		"value":     value,
		"mapped_to": "config.error_handling.continue_on_error",
	}
	if enabled, ok := parseSourceBool(value); ok {
		result.Script.Config.ErrorHandling.ContinueOnError = enabled
		details["enabled"] = enabled
	}
	preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, details)
}

func applySourceSocketOption(result *ConvertResult, fn ParsedFunction) map[string]any {
	details := applySourceSocketOptionConfig(result, fn)
	preserveSourceRuntimeControl(result.Script, "source_socket_options", fn, details)
	return details
}

func applySourceTimeout(result *ConvertResult, fn ParsedFunction) map[string]any {
	details := applySourceTimeoutConfig(result, fn)
	preserveSourceRuntimeControl(result.Script, "source_timeouts", fn, details)
	return details
}

func applySourceWebSetUser(result *ConvertResult, fn ParsedFunction) map[string]any {
	args := sourceControlArgs(fn)
	username := ""
	password := ""
	host := ""
	if len(args) > 0 {
		username = convertParamReferences(args[0])
	}
	if len(args) > 1 {
		password = convertParamReferences(args[1])
	}
	if len(args) > 2 {
		host = convertParamReferences(args[2])
	}
	details := map[string]any{
		"username":  username,
		"host":      host,
		"mapped_to": "config.http.auth",
	}
	if username != "" {
		result.Script.Config.HTTP.Auth = dsl.AuthConfig{
			Type:     "basic",
			Username: username,
			Password: password,
		}
	}
	if host != "" {
		setSourceWebRuntimeAttribute(result.Script, "web_set_user.host", host)
	}
	preserveSourceRuntimeControl(result.Script, "source_authentication", fn, details)
	return details
}

func convertLRErrorMessageAction(fn ParsedFunction) dsl.Action {
	args := sourceControlArgs(fn)
	format := ""
	if len(args) > 0 {
		format = convertSourceMessageExpression(args[0])
	}
	messageArgs := make([]any, 0, max(0, len(args)-1))
	for _, arg := range args[1:] {
		messageArgs = append(messageArgs, convertSourceMessageExpression(arg))
	}

	name := "LR_Error_Message"
	if fn.LineNumber > 0 {
		name = fmt.Sprintf("%s_%d", name, fn.LineNumber)
	}
	action := dsl.Action{
		Name:     name,
		Type:     "error_message",
		Value:    format,
		Metadata: sourceFunctionMetadata(fn),
		Params: map[string]any{
			"level":  "error",
			"format": format,
			"always": true,
		},
	}
	if len(messageArgs) > 0 {
		action.Params["args"] = messageArgs
	}
	return action
}

func convertSourceMessageExpression(value string) string {
	cleaned := strings.TrimSpace(cleanConfigValue(value))
	if cleaned == "" {
		return ""
	}
	if converted, ok := convertSourceParamArrayExpression(cleaned); ok {
		return converted
	}
	if param, ok := unwrapSourceEvalString(cleaned); ok {
		return convertParamReferences(param)
	}
	return convertParamReferences(cleaned)
}

func convertSourceParamArrayExpression(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	open := strings.Index(trimmed, "(")
	close := strings.LastIndex(trimmed, ")")
	if open <= 0 || close <= open {
		return "", false
	}
	name := strings.TrimSpace(trimmed[:open])
	if name != "lr_paramarr_random" && name != "lr_paramarr_len" && name != "lr_paramarr_idx" {
		return "", false
	}
	args := splitCFunctionArgs(trimmed[open+1 : close])
	if len(args) == 0 {
		return "", false
	}
	param := canonicalSourceParameterName(cleanConfigValue(args[0]))
	if param == "" {
		return "", false
	}
	switch name {
	case "lr_paramarr_len":
		return "{{var." + param + "_count}}", true
	case "lr_paramarr_idx":
		if len(args) >= 2 {
			return "{{var." + param + "_" + canonicalSourceParameterName(cleanConfigValue(args[1])) + "}}", true
		}
	}
	return "{{var." + param + "}}", true
}

func unwrapSourceEvalString(value string) (string, bool) {
	trimmed := strings.TrimSpace(value)
	if !strings.HasPrefix(trimmed, "lr_eval_string") {
		return "", false
	}
	open := strings.Index(trimmed, "(")
	close := strings.LastIndex(trimmed, ")")
	if open < 0 || close <= open {
		return "", false
	}
	args := splitCFunctionArgs(trimmed[open+1 : close])
	if len(args) == 0 {
		return "", false
	}
	return cleanConfigValue(args[0]), true
}

func applySourceTimeoutConfig(result *ConvertResult, fn ParsedFunction) map[string]any {
	args := sourceControlArgs(fn)
	timeoutType := ""
	value := ""
	if len(args) > 0 {
		timeoutType = args[0]
	}
	if len(args) > 1 {
		value = args[1]
	}

	attributeKey := "web_set_timeout"
	if timeoutType != "" {
		attributeKey += "." + timeoutType
	}
	setSourceWebRuntimeAttribute(result.Script, attributeKey, value)

	details := map[string]any{
		"timeout":   timeoutType,
		"value":     value,
		"mapped_to": "config.web_runtime.additional_attributes." + attributeKey,
	}
	timeoutMs, ok := parseSourceTimeoutMs(value)
	if !ok {
		return details
	}
	details["mapped_value"] = timeoutMs

	switch normalizeEnum(timeoutType) {
	case "connect":
		result.Script.Config.HTTP.ConnectTimeoutMs = timeoutMs
		details["mapped_to"] = "config.http.connect_timeout_ms"
	case "receive", "response", "download":
		result.Script.Config.HTTP.RequestTimeoutMs = timeoutMs
		details["mapped_to"] = "config.http.request_timeout_ms"
	case "step", "step_download", "stepdownload":
		if result.Script.Config.HTTP.RequestTimeoutMs == 0 {
			result.Script.Config.HTTP.RequestTimeoutMs = timeoutMs
			details["mapped_to"] = "config.http.request_timeout_ms"
		}
	case "ssl", "tls", "secure":
		result.Script.Config.HTTP.TLSTimeoutMs = timeoutMs
		details["mapped_to"] = "config.http.tls_timeout_ms"
	}

	return details
}

func applySourceSocketOptionConfig(result *ConvertResult, fn ParsedFunction) map[string]any {
	args := sourceControlArgs(fn)
	option := ""
	value := ""
	if len(args) > 0 {
		option = args[0]
	}
	if len(args) > 1 {
		value = args[1]
	}

	attributeKey := "web_set_sockets_option"
	if option != "" {
		attributeKey += "." + option
	}
	setSourceWebRuntimeAttribute(result.Script, attributeKey, value)

	details := map[string]any{
		"option":    option,
		"value":     value,
		"mapped_to": "config.web_runtime.additional_attributes." + attributeKey,
	}

	switch normalizeEnum(option) {
	case "ssl_version", "tls_version":
		mapped := mapSSLVersion(value)
		result.Script.Config.HTTP.TLS.MinVersion = mapped
		details["mapped_to"] = "config.http.tls.min_version"
		details["mapped_value"] = mapped
	case "close_keepalive_connections", "close_keep_alive_connections":
		if closeConnections, ok := parseSourceBool(value); ok && closeConnections {
			keepAlive := false
			result.Script.Config.HTTP.KeepAlive = &keepAlive
			details["mapped_to"] = "config.http.keep_alive"
			details["mapped_value"] = false
		}
	case "max_connections_per_host", "max_connections":
		if maxConnections := parseInt(value); maxConnections > 0 {
			result.Script.Config.HTTP.MaxConnsPerHost = maxConnections
			details["mapped_to"] = "config.http.max_conns_per_host"
			details["mapped_value"] = maxConnections
		}
	}

	return details
}

func reapplySourceRuntimeControlConfig(result *ConvertResult, fn ParsedFunction) {
	if result == nil || result.Script == nil {
		return
	}
	switch fn.Name {
	case "lr_continue_on_error":
		if enabled, ok := parseSourceBool(firstSourceControlArg(fn)); ok {
			result.Script.Config.ErrorHandling.ContinueOnError = enabled
		}
	case "web_cache_cleanup":
		setSourceWebRuntimeAttribute(result.Script, "web_cache_cleanup", "true")
	case "web_cleanup_cookies":
		setSourceWebRuntimeAttribute(result.Script, "web_cleanup_cookies", "true")
	case "web_set_sockets_option":
		applySourceSocketOptionConfig(result, fn)
	case "web_set_timeout":
		applySourceTimeoutConfig(result, fn)
	}
}

func setSourceWebRuntimeAttribute(script *dsl.Script, key, value string) {
	if script == nil {
		return
	}
	if script.Config.WebRuntime.AdditionalAttributes == nil {
		script.Config.WebRuntime.AdditionalAttributes = make(map[string]string)
	}
	script.Config.WebRuntime.AdditionalAttributes[key] = value
}

func sourceReviewContributorTargetPath(actionPath string, targetKind string, index int) string {
	switch targetKind {
	case "extractor":
		return fmt.Sprintf("%s.extractors[%d]", actionPath, index)
	case "assertion":
		return fmt.Sprintf("%s.assertions[%d]", actionPath, index)
	case "headers":
		return actionPath + ".headers"
	case "transaction":
		return actionPath + ".name"
	default:
		return actionPath
	}
}

func stringMapValue(values map[string]any, key string, fallback string) string {
	if values == nil {
		return fallback
	}
	if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func preserveSourceRuntimeControl(script *dsl.Script, key string, fn ParsedFunction, details map[string]any) {
	if script == nil || key == "" {
		return
	}
	entry := map[string]any{
		"function": fn.Name,
		"line":     fn.LineNumber,
	}
	if args := sourceControlArgs(fn); len(args) > 0 {
		entry["args"] = args
	}
	for detailKey, detailValue := range details {
		switch value := detailValue.(type) {
		case string:
			if strings.TrimSpace(value) == "" {
				continue
			}
		case nil:
			continue
		}
		entry[detailKey] = detailValue
	}

	preserved := ensureEnterpriseArchiveImportPreserved(script)
	switch existing := preserved[key].(type) {
	case []map[string]any:
		preserved[key] = append(existing, entry)
	case []any:
		preserved[key] = append(existing, entry)
	default:
		preserved[key] = []map[string]any{entry}
	}
}

func ensureEnterpriseArchiveImportPreserved(script *dsl.Script) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: "enterprise_script_archive"}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	return script.Metadata.Import.Preserved
}

func firstSourceControlArg(fn ParsedFunction) string {
	return sourceControlArgAt(fn, 0)
}

func sourceControlArgAt(fn ParsedFunction, index int) string {
	args := sourceControlArgs(fn)
	if index < 0 || index >= len(args) {
		return ""
	}
	return args[index]
}

func sourceControlArgs(fn ParsedFunction) []string {
	args := make([]string, 0, len(fn.Args))
	for index := 1; ; index++ {
		value, ok := fn.Args[fmt.Sprintf("arg%d", index)]
		if !ok {
			break
		}
		if strings.TrimSpace(value) != "" {
			args = append(args, value)
		}
	}
	if len(args) == 0 && len(fn.CallArgs) > 0 {
		for _, arg := range fn.CallArgs {
			if arg.Kind == "macro" {
				continue
			}
			value := arg.Value
			if arg.Kind == "key_value" && arg.Name != "" {
				value = arg.Name + "=" + arg.Value
			}
			if strings.TrimSpace(value) != "" {
				args = append(args, value)
			}
		}
	}
	if len(args) == 0 {
		args = append(args, fn.RawArgs...)
	}
	return args
}

func isSourceMetadataOnlyContributor(name string) bool {
	return false
}

func parseSourceBool(value string) (bool, bool) {
	switch normalizeEnum(cleanConfigValue(value)) {
	case "lr_on", "lr_true", "continue", "continue_on_error":
		return true, true
	case "lr_off", "lr_false", "abort", "stop":
		return false, true
	default:
		return parseBoolExplicit(value)
	}
}

func parseSourceTimeoutMs(value string) (int, bool) {
	seconds, err := strconv.ParseFloat(cleanConfigValue(value), 64)
	if err != nil || seconds <= 0 {
		return 0, false
	}
	return int(math.Round(seconds * 1000)), true
}
