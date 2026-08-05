package enterprisearchive

// source_runtime.go — split from converter.go (same package, behavior-preserving).

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/credential"
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
	details := applySourceWebSetUserConfig(result, fn)
	preserveSourceRuntimeControl(result.Script, "source_authentication", fn, details)
	return details
}

func applySourceWebSetUserConfig(result *ConvertResult, fn ParsedFunction) map[string]any {
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
		if mapped != "" {
			result.Script.Config.HTTP.TLS.MinVersion = mapped
			result.Script.Config.HTTP.TLS.MaxVersion = mapped
			details["mapped_to"] = "config.http.tls"
			details["mapped_value"] = mapped
		} else {
			details["mapping_partial"] = true
			details["review_reason"] = fmt.Sprintf(
				"web_set_sockets_option %s=%q is unsupported; APEX TLS defaults remain unchanged and review is required",
				option, value,
			)
		}
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
	case "web_set_sockets_option":
		applySourceSocketOptionConfig(result, fn)
	case "web_set_timeout":
		applySourceTimeoutConfig(result, fn)
	case "web_set_user":
		applySourceWebSetUserConfig(result, fn)
	case "web_set_option", "web_set_max_retries", "web_disable_keep_alive", "web_enable_keep_alive",
		"web_set_certificate_ex", "web_set_proxy", "web_set_secure_proxy", "web_set_proxy_bypass", "web_set_proxy_bypass_local":
		applySourceWebRuntimeControlConfig(result, fn)
	}
}

// applySourceWebRuntimeControl maps a Web (HTTP/HTML) connection/session/TLS/proxy
// control onto config.http (with a web_runtime attribute for unmapped options) and
// preserves the original call for round-trip fidelity.
func applySourceWebRuntimeControl(result *ConvertResult, fn ParsedFunction) map[string]any {
	details := applySourceWebRuntimeControlConfig(result, fn)
	preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, details)
	return details
}

func applySourceWebRuntimeControlConfig(result *ConvertResult, fn ParsedFunction) map[string]any {
	args := sourceControlArgs(fn)
	details := map[string]any{}

	switch fn.Name {
	case "web_disable_keep_alive", "web_enable_keep_alive":
		keepAlive := fn.Name == "web_enable_keep_alive"
		result.Script.Config.HTTP.KeepAlive = &keepAlive
		details["summary"] = fn.Name
		details["mapped_to"] = "config.http.keep_alive"
		details["mapped_value"] = keepAlive

	case "web_set_max_retries":
		value := firstSourceControlArg(fn)
		setSourceWebRuntimeAttribute(result.Script, "web_set_max_retries", value)
		details["summary"] = "max_retries=" + value
		details["mapped_to"] = "config.web_runtime.additional_attributes.web_set_max_retries"
		if n := parseInt(value); n > 0 {
			result.Script.Config.ErrorHandling.MaxRetries = n
			details["mapped_to"] = "config.error_handling.max_retries"
			details["mapped_value"] = n
		}

	case "web_set_option":
		option, value := sourceOptionPair(args)
		key := "web_set_option"
		if option != "" {
			key += "." + option
		}
		setSourceWebRuntimeAttribute(result.Script, key, value)
		details["summary"] = strings.TrimSpace(option + "=" + value)
		details["mapped_to"] = "config.web_runtime.additional_attributes." + key
		switch normalizeEnum(option) {
		case "max_total_connections", "max_connections", "max_connections_per_host":
			if n := parseInt(value); n > 0 {
				result.Script.Config.HTTP.MaxConnsPerHost = n
				details["mapped_to"] = "config.http.max_conns_per_host"
				details["mapped_value"] = n
			}
		}

	case "web_set_certificate_ex":
		cert := convertParamReferences(firstNonEmptyArg(fn, "CertFilePath"))
		key := convertParamReferences(firstNonEmptyArg(fn, "KeyFilePath"))
		if cert != "" {
			result.Script.Config.HTTP.TLS.ClientCertFile = cert
			details["mapped_to"] = "config.http.tls.client_cert_file"
			details["summary"] = cert
		}
		if key != "" {
			result.Script.Config.HTTP.TLS.ClientKeyFile = key
		}
		if cert == "" && key == "" {
			setSourceWebRuntimeAttribute(result.Script, "web_set_certificate_ex", firstSourceControlArg(fn))
			details["mapped_to"] = "config.web_runtime.additional_attributes.web_set_certificate_ex"
		}

	case "web_set_proxy", "web_set_secure_proxy":
		proxy := sourceProxyURL(firstSourceControlArg(fn), fn.Name == "web_set_secure_proxy")
		if proxy != "" {
			result.Script.Config.HTTP.ProxyMode = "custom"
			result.Script.Config.HTTP.Proxy.URL = proxy
			details["mapped_to"] = "config.http.proxy.url"
			details["summary"] = proxy
		}

	case "web_set_proxy_bypass", "web_set_proxy_bypass_local":
		for _, host := range sourceProxyBypassHosts(args) {
			result.Script.Config.HTTP.Proxy.NoProxy = appendUniqueString(result.Script.Config.HTTP.Proxy.NoProxy, host)
		}
		if fn.Name == "web_set_proxy_bypass_local" {
			result.Script.Config.HTTP.Proxy.NoProxy = appendUniqueString(result.Script.Config.HTTP.Proxy.NoProxy, "localhost")
			result.Script.Config.HTTP.Proxy.NoProxy = appendUniqueString(result.Script.Config.HTTP.Proxy.NoProxy, "127.0.0.1")
		}
		details["mapped_to"] = "config.http.proxy.no_proxy"
		details["summary"] = strings.Join(result.Script.Config.HTTP.Proxy.NoProxy, ",")
	}

	return details
}

// sourceOptionPair resolves the (key, value) of a web_set_option("Key", "Value")
// call from its positional arguments.
func sourceOptionPair(args []string) (string, string) {
	option := ""
	value := ""
	if len(args) > 0 {
		option = cleanConfigValue(args[0])
	}
	if len(args) > 1 {
		value = cleanConfigValue(args[1])
	}
	return option, value
}

// sourceProxyURL normalizes a web_set_proxy("host:port") argument into a URL. A
// value that already carries a scheme is kept as-is.
func sourceProxyURL(raw string, secure bool) string {
	value := strings.TrimSpace(cleanConfigValue(raw))
	if value == "" {
		return ""
	}
	if strings.Contains(value, "://") {
		return convertParamReferences(value)
	}
	scheme := "http://"
	if secure {
		scheme = "https://"
	}
	return scheme + convertParamReferences(value)
}

func sourceProxyBypassHosts(args []string) []string {
	hosts := make([]string, 0, len(args))
	for _, arg := range args {
		for _, host := range strings.FieldsFunc(cleanConfigValue(arg), func(r rune) bool {
			return r == ',' || r == ';' || r == ' '
		}) {
			if trimmed := strings.TrimSpace(host); trimmed != "" {
				hosts = append(hosts, convertParamReferences(trimmed))
			}
		}
	}
	return hosts
}

func appendUniqueString(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// convertWebCleanupCookiesAction builds the executable cookie_clear action that
// an imported web_cleanup_cookies() call maps to. The engine dispatches it to
// the protocol's CookieController (HTTP) so cookies are actually cleared at this
// point in the flow, rather than being preserved as a non-executed attribute.
func convertWebCleanupCookiesAction(fn ParsedFunction) dsl.Action {
	action := dsl.Action{
		Name:     sourceActionName(fn),
		Type:     "cookie_clear",
		Metadata: sourceFunctionMetadata(fn),
		Params: map[string]any{
			"function":     "web_cleanup_cookies",
			"family":       "runtime",
			"support_mode": string(SourceFunctionSupportExecutable),
		},
	}
	action.Metadata["support_mode"] = string(SourceFunctionSupportExecutable)
	return action
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
	if args := disclosureSafeSourceRuntimeControlArgs(fn); len(args) > 0 {
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

func disclosureSafeSourceRuntimeControlArgs(fn ParsedFunction) []string {
	args := append([]string(nil), sourceControlArgs(fn)...)
	switch strings.ToLower(strings.TrimSpace(fn.Name)) {
	case "web_set_user":
		if len(args) > 1 {
			args[1] = credential.Redacted
		}
	case "web_set_proxy", "web_set_secure_proxy":
		if len(args) > 0 {
			args[0] = disclosureSafeProxyEndpoint(args[0])
		}
	case "web_set_option":
		if len(args) > 1 && credential.SensitiveName(cleanConfigValue(args[0])) {
			args[1] = credential.Redacted
		}
	}
	return args
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
	foundPositional := false
	for index := 1; ; index++ {
		value, ok := fn.Args[fmt.Sprintf("arg%d", index)]
		if !ok {
			break
		}
		foundPositional = true
		args = append(args, value)
	}
	if !foundPositional && len(fn.CallArgs) > 0 {
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
	if !foundPositional && len(args) == 0 {
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
