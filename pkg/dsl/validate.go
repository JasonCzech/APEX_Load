package dsl

import (
	"fmt"
	"strings"
	"time"

	browserprotocol "github.com/jasonczech/stormrunner/pkg/protocol/browser"
)

// ValidateScriptIssues returns all validation issues for a script definition.
func ValidateScriptIssues(script *Script) []string {
	if script == nil {
		return []string{"script is required"}
	}

	if strings.HasPrefix(strings.TrimSpace(script.Protocol), "jms-") {
		return validateJMSScriptIssues(script)
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
	if script.Protocol != "" && script.Protocol != "browser" && strings.TrimSpace(script.Config.BaseURL) == "" {
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

		if script.Protocol == "browser" {
			issues = append(issues, validateBrowserActionIssues(i, action)...)
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

	issues = append(issues, validateParameterIssues(script)...)
	issues = append(issues, validateBrowserConfigIssues(script)...)
	issues = append(issues, validateWebRuntimeIssues(script)...)

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
	case "browser_screenshot":
		return nil
	case "browser_wait":
		if action.WaitFor == nil && action.Selector == "" && action.Target == "" {
			issues = append(issues, fmt.Sprintf("%s: browser_wait requires wait_for, selector, or target", label))
		}
		return issues
	case "browser_navigate", "browser_click", "browser_fill", "browser_tap", "browser_long_press", "browser_swipe":
		if action.Target == "" && action.Selector == "" {
			issues = append(issues, fmt.Sprintf("%s: missing target or selector", label))
		}
	default:
		issues = append(issues, fmt.Sprintf("%s: unsupported browser action type %q", label, action.Type))
		return issues
	}

	switch action.Type {
	case "browser_fill":
		if action.Value == "" {
			issues = append(issues, fmt.Sprintf("%s: browser_fill requires value", label))
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

func validateParameterIssues(script *Script) []string {
	if len(script.Parameters) == 0 {
		return nil
	}

	var issues []string
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

		paramType := strings.TrimSpace(param.Type)
		if paramType == "" {
			issues = append(issues, fmt.Sprintf("%s %q: type is required", label, name))
			continue
		}
		if !isAllowedValue(paramType, "file", "list", "random_number", "date_time", "timestamp", "counter", "uuid", "random_string") {
			issues = append(issues, fmt.Sprintf("%s %q: unsupported type %q", label, name, paramType))
		}

		if param.Update != "" && !isAllowedValue(param.Update, "once", "each_iteration", "each_occurrence") {
			issues = append(issues, fmt.Sprintf("%s %q: update must be once, each_iteration, or each_occurrence", label, name))
		}
		if param.Selection != "" && !isAllowedValue(param.Selection, "sequential", "random", "unique") {
			issues = append(issues, fmt.Sprintf("%s %q: selection must be sequential, random, or unique", label, name))
		}
		if param.OnExhaustion != "" && !isAllowedValue(param.OnExhaustion, "wrap", "empty", "error") {
			issues = append(issues, fmt.Sprintf("%s %q: on_exhaustion must be wrap, empty, or error", label, name))
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
		case "date_time":
			if strings.TrimSpace(param.Timezone) != "" {
				if _, err := time.LoadLocation(param.Timezone); err != nil {
					issues = append(issues, fmt.Sprintf("%s %q: invalid timezone %q", label, name, param.Timezone))
				}
			}
		case "timestamp":
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
	if network.Mode == "custom" && network.BandwidthKbps <= 0 {
		issues = append(issues, "config.web_runtime.network_simulation.bandwidth_kbps must be greater than zero when mode is custom")
	}
	if network.LatencyMs < 0 {
		issues = append(issues, "config.web_runtime.network_simulation.latency_ms must be zero or greater")
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

	for index, transform := range script.Config.WebRuntime.DataFormatting.Chain {
		if strings.TrimSpace(transform.Type) == "" {
			issues = append(issues, fmt.Sprintf("config.web_runtime.data_formatting.chain[%d].type is required", index))
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

func isAllowedValue(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
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
