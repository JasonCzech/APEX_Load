package loadrunner

import (
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// ConvertResult holds the output of a LoadRunner to APEX Load DSL conversion.
type ConvertResult struct {
	Script                       *dsl.Script       `json:"script"`
	Warnings                     []string          `json:"warnings,omitempty"`
	Stats                        ConvertStats      `json:"stats"`
	DetectedProtocol             *DetectedProtocol `json:"detected_protocol,omitempty"`
	ReviewArtifact               *ReviewArtifact   `json:"review_artifact,omitempty"`
	SourceReview                 *SourceReview     `json:"source_review,omitempty"`
	RecordedResourceURLs         []string          `json:"recorded_resource_urls,omitempty"`
	RecordedResourceURLsCaptured bool              `json:"recorded_resource_urls_captured,omitempty"`
}

// ConvertStats provides conversion statistics for reporting.
type ConvertStats struct {
	TotalFunctions            int      `json:"total_functions"`
	ConvertedActions          int      `json:"converted_actions"`
	Extractors                int      `json:"extractors"`
	ThinkTimes                int      `json:"think_times"`
	Transactions              int      `json:"transactions"`
	NativeFunctions           int      `json:"native_functions,omitempty"`
	ReviewOnlyFunctions       int      `json:"review_only_functions,omitempty"`
	ExternalRequiredFunctions int      `json:"external_required_functions,omitempty"`
	CustomCallFunctions       int      `json:"custom_call_functions,omitempty"`
	Skipped                   int      `json:"skipped"`
	UnsupportedFuncs          []string `json:"unsupported_funcs,omitempty"`
}

// ConvertOptions controls the conversion behavior.
type ConvertOptions struct {
	ScriptName           string  // Override the generated script name
	BaseURL              string  // Override base URL (otherwise auto-detected from first URL)
	DefaultThinkTimeMs   float64 // Default think time if none in script (0 = no default)
	IncludeAssertions    bool    // Add default status_code assertions
	PreserveTransactions bool    // Wrap actions in transaction markers (as comments in name)

	// HTTP runtime settings (mapped from VuGen runtime config)
	HTTPConfig      *dsl.HTTPConfig   // Pre-built HTTP config to embed in script
	RuntimeSettings *dsl.ScriptConfig // Broader runtime settings mapped from default.cfg
}

// Convert transforms parsed LoadRunner action files into a APEX Load DSL Script.
func Convert(actions []*ParsedAction, opts ConvertOptions) (*ConvertResult, error) {
	result := &ConvertResult{
		Warnings: make([]string, 0),
		Script: &dsl.Script{
			Version:       "1.0.0",
			Protocol:      "http",
			GeneratedBy:   "apexload-loadrunner-converter",
			GeneratedFrom: "loadrunner",
			Config:        dsl.ScriptConfig{},
			Actions:       make([]dsl.Action, 0),
		},
	}

	// Collect all functions across action files in order
	var allFunctions []ParsedFunction
	for _, a := range actions {
		allFunctions = append(allFunctions, a.Functions...)
		result.Stats.TotalFunctions += len(a.Functions)
	}

	if len(allFunctions) == 0 {
		return nil, fmt.Errorf("no LoadRunner functions found in action files")
	}

	protocolName, soapConfig, protocolWarning := detectImportProtocol(allFunctions)
	result.Script.Protocol = protocolName
	if soapConfig != nil {
		result.Script.Config.SOAP = *soapConfig
	}
	if protocolWarning != "" {
		result.Warnings = append(result.Warnings, protocolWarning)
	}

	// Auto-detect base URL from first web_ call
	baseURL := opts.BaseURL
	if baseURL == "" {
		baseURL = detectBaseURL(allFunctions)
	}
	result.Script.Config.BaseURL = baseURL
	result.RecordedResourceURLs = collectRecordedResourceURLs(allFunctions)
	result.RecordedResourceURLsCaptured = true

	// Set script name
	if opts.ScriptName != "" {
		result.Script.Name = opts.ScriptName
	} else {
		result.Script.Name = "Converted_LR_Script"
	}

	// Process functions — correlations/extractors come BEFORE the request they apply to
	var pendingExtractors []dsl.Extractor
	var pendingAssertions []dsl.Assertion
	var pendingHeaders map[string]string
	var pendingSourceMappings []SourceReviewMapping
	var currentTransaction string
	var thinkTimes []float64
	var loadRunnerRuntimeControls []ParsedFunction

	actionNameCounts := make(map[string]int)

	for _, fn := range allFunctions {
		switch fn.Name {
		case "web_reg_save_param", "web_reg_save_param_ex", "web_reg_save_param_regexp", "web_reg_save_param_json", "web_reg_save_param_xpath":
			ext := convertExtractor(fn)
			if ext != nil {
				pendingExtractors = append(pendingExtractors, *ext)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", ext.Name, "extractor", "contributor", "Applied to the next converted request action.", LoadRunnerSupportExecutable))
				result.Stats.Extractors++
				recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)
			}

		case "web_reg_find":
			assertion, warning := convertFindAssertion(fn)
			if assertion != nil {
				pendingAssertions = append(pendingAssertions, *assertion)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", assertion.Type, "assertion", "contributor", "Applied to the next converted request action.", LoadRunnerSupportExecutable))
				recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)
			}
			if warning != "" {
				result.Warnings = append(result.Warnings, warning)
			}

		case "web_add_header", "web_add_auto_header":
			if pendingHeaders == nil {
				pendingHeaders = make(map[string]string)
			}
			for k, v := range fn.Headers {
				pendingHeaders[k] = v
			}
			pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", firstNonEmpty(fn.StepName, fn.Name), "headers", "contributor", "Applied to the next converted request action.", LoadRunnerSupportExecutable))
			recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)

		case "lr_start_transaction":
			if action, mode, ok := convertLoadRunnerFunctionAction(fn); ok {
				appendLoadRunnerAction(result, fn, action, mode, "Converted to a native LoadRunner transaction action.")
			}
			currentTransaction = fn.StepName
			pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", fn.StepName, "transaction", "contributor", "Applied to generated action names until the transaction ends.", LoadRunnerSupportExecutable))
			result.Stats.Transactions++

		case "lr_end_transaction":
			if action, mode, ok := convertLoadRunnerFunctionAction(fn); ok {
				appendLoadRunnerAction(result, fn, action, mode, "Converted to a native LoadRunner transaction action.")
			}
			currentTransaction = ""

		case "lr_think_time":
			sec, ok := ExtractThinkTimeSeconds(fn)
			if ok {
				thinkTimes = append(thinkTimes, sec)
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.think_time", "Think Time", "config", "converted", "Contributes to generated think-time distribution.", LoadRunnerSupportExecutable))
				result.Stats.ThinkTimes++
				recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)
			}

		case "lr_continue_on_error":
			loadRunnerRuntimeControls = append(loadRunnerRuntimeControls, fn)
			applyLoadRunnerContinueOnError(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.error_handling.continue_on_error", "Continue On Error", "config", "converted", "Mapped from LoadRunner runtime control.", LoadRunnerSupportExecutable))
			recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)

		case "lr_error_message":
			action := convertLRErrorMessageAction(fn)
			if def, ok := lookupLoadRunnerFunction(fn.Name); ok {
				annotateLoadRunnerAction(&action, fn, def)
			}
			appendLoadRunnerAction(result, fn, action, LoadRunnerSupportExecutable, "Converted to a native error-message action.")

		case "lr_exit":
			preserveLoadRunnerRuntimeControl(result.Script, "loadrunner_exit_controls", fn, nil)
			if action, mode, ok := convertLoadRunnerFunctionAction(fn); ok {
				appendLoadRunnerAction(result, fn, action, mode, "Converted to a native LoadRunner runtime action.")
			}

		case "web_cache_cleanup":
			loadRunnerRuntimeControls = append(loadRunnerRuntimeControls, fn)
			setLoadRunnerWebRuntimeAttribute(result.Script, "web_cache_cleanup", "true")
			preserveLoadRunnerRuntimeControl(result.Script, "loadrunner_runtime_controls", fn, map[string]any{
				"mapped_to": "config.web_runtime.additional_attributes.web_cache_cleanup",
			})
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.additional_attributes.web_cache_cleanup", "Web Cache Cleanup", "config", "converted", "Mapped from LoadRunner runtime control.", LoadRunnerSupportExecutable))
			recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)

		case "web_cleanup_cookies":
			loadRunnerRuntimeControls = append(loadRunnerRuntimeControls, fn)
			setLoadRunnerWebRuntimeAttribute(result.Script, "web_cleanup_cookies", "true")
			preserveLoadRunnerRuntimeControl(result.Script, "loadrunner_runtime_controls", fn, map[string]any{
				"mapped_to": "config.web_runtime.additional_attributes.web_cleanup_cookies",
			})
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.additional_attributes.web_cleanup_cookies", "Web Cleanup Cookies", "config", "converted", "Mapped from LoadRunner runtime control.", LoadRunnerSupportExecutable))
			recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)

		case "web_set_sockets_option":
			loadRunnerRuntimeControls = append(loadRunnerRuntimeControls, fn)
			details := applyLoadRunnerSocketOption(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, stringMapValue(details, "mapped_to", "config.web_runtime.additional_attributes.web_set_sockets_option"), firstNonEmpty(stringMapValue(details, "option", ""), "Socket Option"), "config", "converted", "Mapped from LoadRunner runtime control.", LoadRunnerSupportExecutable))
			recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)

		case "web_set_timeout":
			loadRunnerRuntimeControls = append(loadRunnerRuntimeControls, fn)
			details := applyLoadRunnerTimeout(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, stringMapValue(details, "mapped_to", "config.web_runtime.additional_attributes.web_set_timeout"), firstNonEmpty(stringMapValue(details, "timeout", ""), "Timeout"), "config", "converted", "Mapped from LoadRunner runtime control.", LoadRunnerSupportExecutable))
			recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)

		case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_service_call":
			action, warnings := convertAction(fn, baseURL, currentTransaction, opts)
			if action != nil {
				// Capture pre-attach counts so contributor mappings can point at the
				// exact extractor/assertion index they land on in the generated action.
				baseExtractorCount := len(action.Extractors)
				baseAssertionCount := len(action.Assertions)

				if len(pendingAssertions) > 0 {
					action.Assertions = append(action.Assertions, pendingAssertions...)
					pendingAssertions = nil
				}

				// Attach pending extractors
				if len(pendingExtractors) > 0 {
					action.Extractors = append(action.Extractors, pendingExtractors...)
					pendingExtractors = nil
				}

				// Attach pending headers
				if len(pendingHeaders) > 0 {
					if action.Headers == nil {
						action.Headers = make(map[string]string)
					}
					for k, v := range pendingHeaders {
						if _, exists := action.Headers[k]; !exists {
							action.Headers[k] = v
						}
					}
					pendingHeaders = nil
				}

				applySOAPImportHints(action, fn)

				// Default assertions
				if opts.IncludeAssertions {
					action.Assertions = append(action.Assertions, dsl.Assertion{
						Type:     "status_code",
						Expected: 200,
					})
				}

				// Deduplicate action names
				actionNameCounts[action.Name]++
				if actionNameCounts[action.Name] > 1 {
					action.Name = fmt.Sprintf("%s_%d", action.Name, actionNameCounts[action.Name])
				}

				actionIndex := len(result.Script.Actions)
				targetPath := fmt.Sprintf("actions[%d]", actionIndex)
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, targetPath, action.Name, "action", "converted", "Converted to a generated APEX Load action.", LoadRunnerSupportExecutable))
				// Contributors (extractors, assertions, headers) were collected in source
				// order ahead of this request, so the Nth extractor/assertion mapping lines
				// up with the Nth attached extractor/assertion.
				extractorIndex := baseExtractorCount
				assertionIndex := baseAssertionCount
				for _, pendingMapping := range pendingSourceMappings {
					contributorIndex := -1
					switch pendingMapping.TargetKind {
					case "extractor":
						contributorIndex = extractorIndex
						extractorIndex++
					case "assertion":
						contributorIndex = assertionIndex
						assertionIndex++
					}
					pendingMapping.TargetPath = sourceReviewContributorTargetPath(targetPath, pendingMapping.TargetKind, contributorIndex)
					if pendingMapping.TargetName == "" {
						pendingMapping.TargetName = action.Name
					}
					appendSourceReviewMapping(result, pendingMapping)
				}
				pendingSourceMappings = nil

				result.Script.Actions = append(result.Script.Actions, *action)
				result.Stats.ConvertedActions++
				recordLoadRunnerFunctionSupport(result, LoadRunnerSupportExecutable)
			}
			result.Warnings = append(result.Warnings, warnings...)

		default:
			if action, mode, ok := convertLoadRunnerFunctionAction(fn); ok {
				appendLoadRunnerAction(result, fn, action, mode, loadRunnerSupportNotes(fn, mode))
				continue
			}
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			appendSourceReviewMapping(result, sourceReviewMappingForFunction(fn, "metadata.import.preserved.unsupported_functions", fn.Name, "unsupported", "review_only", "No executable APEX Load mapping exists for this LoadRunner function yet."))
		}
	}

	// Configure think time from collected lr_think_time values
	if len(thinkTimes) > 0 {
		mean, std := computeThinkTimeStats(thinkTimes)
		result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{
			Type:   "normal",
			MeanMs: mean * 1000,
			StdMs:  std * 1000,
			MinMs:  math.Max(0, (mean-2*std)*1000),
			MaxMs:  (mean + 2*std) * 1000,
		}
	} else if opts.DefaultThinkTimeMs > 0 {
		result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{
			Type:   "fixed",
			MeanMs: opts.DefaultThinkTimeMs,
		}
	}

	// Mark actions with think_time_after (all except last)
	for i := range result.Script.Actions {
		if i < len(result.Script.Actions)-1 {
			result.Script.Actions[i].ThinkTimeAfter = true
		}
	}

	if len(result.Script.Actions) == 0 {
		return nil, fmt.Errorf("no request actions could be converted from the LoadRunner script")
	}

	// Apply HTTP runtime config if provided
	if opts.HTTPConfig != nil {
		result.Script.Config.HTTP = *opts.HTTPConfig
	}
	if opts.RuntimeSettings != nil {
		mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
	}
	for _, fn := range loadRunnerRuntimeControls {
		reapplyLoadRunnerRuntimeControlConfig(result, fn)
	}

	return result, nil
}

func applyLoadRunnerContinueOnError(result *ConvertResult, fn ParsedFunction) {
	value := firstLoadRunnerControlArg(fn)
	details := map[string]any{
		"value":     value,
		"mapped_to": "config.error_handling.continue_on_error",
	}
	if enabled, ok := parseLoadRunnerBool(value); ok {
		result.Script.Config.ErrorHandling.ContinueOnError = enabled
		details["enabled"] = enabled
	}
	preserveLoadRunnerRuntimeControl(result.Script, "loadrunner_runtime_controls", fn, details)
}

func applyLoadRunnerSocketOption(result *ConvertResult, fn ParsedFunction) map[string]any {
	details := applyLoadRunnerSocketOptionConfig(result, fn)
	preserveLoadRunnerRuntimeControl(result.Script, "loadrunner_socket_options", fn, details)
	return details
}

func applyLoadRunnerTimeout(result *ConvertResult, fn ParsedFunction) map[string]any {
	details := applyLoadRunnerTimeoutConfig(result, fn)
	preserveLoadRunnerRuntimeControl(result.Script, "loadrunner_timeouts", fn, details)
	return details
}

func convertLRErrorMessageAction(fn ParsedFunction) dsl.Action {
	args := loadRunnerControlArgs(fn)
	format := ""
	if len(args) > 0 {
		format = convertLoadRunnerMessageExpression(args[0])
	}
	messageArgs := make([]any, 0, max(0, len(args)-1))
	for _, arg := range args[1:] {
		messageArgs = append(messageArgs, convertLoadRunnerMessageExpression(arg))
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

func convertLoadRunnerMessageExpression(value string) string {
	cleaned := strings.TrimSpace(cleanConfigValue(value))
	if cleaned == "" {
		return ""
	}
	if param, ok := unwrapLoadRunnerEvalString(cleaned); ok {
		return convertParamReferences(param)
	}
	return convertParamReferences(cleaned)
}

func unwrapLoadRunnerEvalString(value string) (string, bool) {
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

func applyLoadRunnerTimeoutConfig(result *ConvertResult, fn ParsedFunction) map[string]any {
	args := loadRunnerControlArgs(fn)
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
	setLoadRunnerWebRuntimeAttribute(result.Script, attributeKey, value)

	details := map[string]any{
		"timeout":   timeoutType,
		"value":     value,
		"mapped_to": "config.web_runtime.additional_attributes." + attributeKey,
	}
	timeoutMs, ok := parseLoadRunnerTimeoutMs(value)
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

func applyLoadRunnerSocketOptionConfig(result *ConvertResult, fn ParsedFunction) map[string]any {
	args := loadRunnerControlArgs(fn)
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
	setLoadRunnerWebRuntimeAttribute(result.Script, attributeKey, value)

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
		if closeConnections, ok := parseLoadRunnerBool(value); ok && closeConnections {
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

func reapplyLoadRunnerRuntimeControlConfig(result *ConvertResult, fn ParsedFunction) {
	if result == nil || result.Script == nil {
		return
	}
	switch fn.Name {
	case "lr_continue_on_error":
		if enabled, ok := parseLoadRunnerBool(firstLoadRunnerControlArg(fn)); ok {
			result.Script.Config.ErrorHandling.ContinueOnError = enabled
		}
	case "web_cache_cleanup":
		setLoadRunnerWebRuntimeAttribute(result.Script, "web_cache_cleanup", "true")
	case "web_cleanup_cookies":
		setLoadRunnerWebRuntimeAttribute(result.Script, "web_cleanup_cookies", "true")
	case "web_set_sockets_option":
		applyLoadRunnerSocketOptionConfig(result, fn)
	case "web_set_timeout":
		applyLoadRunnerTimeoutConfig(result, fn)
	}
}

func setLoadRunnerWebRuntimeAttribute(script *dsl.Script, key, value string) {
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

func preserveLoadRunnerRuntimeControl(script *dsl.Script, key string, fn ParsedFunction, details map[string]any) {
	if script == nil || key == "" {
		return
	}
	entry := map[string]any{
		"function": fn.Name,
		"line":     fn.LineNumber,
	}
	if args := loadRunnerControlArgs(fn); len(args) > 0 {
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

	preserved := ensureLoadRunnerImportPreserved(script)
	switch existing := preserved[key].(type) {
	case []map[string]any:
		preserved[key] = append(existing, entry)
	case []any:
		preserved[key] = append(existing, entry)
	default:
		preserved[key] = []map[string]any{entry}
	}
}

func ensureLoadRunnerImportPreserved(script *dsl.Script) map[string]any {
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{Source: "loadrunner"}
	}
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = make(map[string]any)
	}
	return script.Metadata.Import.Preserved
}

func firstLoadRunnerControlArg(fn ParsedFunction) string {
	args := loadRunnerControlArgs(fn)
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func loadRunnerControlArgs(fn ParsedFunction) []string {
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

func parseLoadRunnerBool(value string) (bool, bool) {
	switch normalizeEnum(cleanConfigValue(value)) {
	case "lr_on", "lr_true", "continue", "continue_on_error":
		return true, true
	case "lr_off", "lr_false", "abort", "stop":
		return false, true
	default:
		return parseBoolExplicit(value)
	}
}

func parseLoadRunnerTimeoutMs(value string) (int, bool) {
	seconds, err := strconv.ParseFloat(cleanConfigValue(value), 64)
	if err != nil || seconds <= 0 {
		return 0, false
	}
	return int(math.Round(seconds * 1000)), true
}

func mergeScriptConfig(dst *dsl.ScriptConfig, src *dsl.ScriptConfig) {
	if dst == nil || src == nil {
		return
	}
	if src.ThinkTime.Type != "" || src.ThinkTime.MeanMs != 0 || src.ThinkTime.StdMs != 0 || src.ThinkTime.MinMs != 0 || src.ThinkTime.MaxMs != 0 || src.ThinkTime.Multiplier != 0 || src.ThinkTime.LimitMs != 0 || src.ThinkTime.Ignore {
		if src.ThinkTime.Type != "" {
			dst.ThinkTime.Type = src.ThinkTime.Type
		}
		if src.ThinkTime.MeanMs != 0 {
			dst.ThinkTime.MeanMs = src.ThinkTime.MeanMs
		}
		if src.ThinkTime.StdMs != 0 {
			dst.ThinkTime.StdMs = src.ThinkTime.StdMs
		}
		if src.ThinkTime.MinMs != 0 {
			dst.ThinkTime.MinMs = src.ThinkTime.MinMs
		}
		if src.ThinkTime.MaxMs != 0 {
			dst.ThinkTime.MaxMs = src.ThinkTime.MaxMs
		}
		if src.ThinkTime.Multiplier != 0 {
			dst.ThinkTime.Multiplier = src.ThinkTime.Multiplier
		}
		if src.ThinkTime.LimitMs != 0 {
			dst.ThinkTime.LimitMs = src.ThinkTime.LimitMs
		}
		if src.ThinkTime.Ignore {
			dst.ThinkTime.Ignore = true
		}
	}
	if !isEmptyPacingConfig(src.Pacing) {
		dst.Pacing = src.Pacing
	}
	if !isEmptyRunLogicConfig(src.RunLogic) {
		dst.RunLogic = src.RunLogic
	}
	if !isEmptyLogConfig(src.Log) {
		dst.Log = src.Log
	}
	if !isEmptyHTTPConfig(src.HTTP) {
		dst.HTTP = src.HTTP
	}
	if !isEmptyBrowserConfig(src.Browser) {
		dst.Browser = src.Browser
	}
	if !isEmptyErrorHandlingConfig(src.ErrorHandling) {
		dst.ErrorHandling = src.ErrorHandling
	}
	if !isEmptyWebRuntimeConfig(src.WebRuntime) {
		dst.WebRuntime = src.WebRuntime
	}
}

func isEmptyPacingConfig(cfg dsl.PacingConfig) bool {
	return cfg.Type == "" && cfg.DelayMs == 0 && cfg.MinDelayMs == 0 && cfg.MaxDelayMs == 0 && cfg.IntervalMs == 0 && cfg.MinIntervalMs == 0 && cfg.MaxIntervalMs == 0 && cfg.TPS == 0
}

func isEmptyRunLogicConfig(cfg dsl.RunLogicConfig) bool {
	return cfg.Iterations == 0 && cfg.AutoTransactions == "" && cfg.BlockScheduling == "" && len(cfg.ActionSequence) == 0
}

func isEmptyLogConfig(cfg dsl.LogConfig) bool {
	return cfg.Level == "" && !cfg.OnErrorOnly && len(cfg.ExtendedOptions) == 0
}

func isEmptyHTTPConfig(cfg dsl.HTTPConfig) bool {
	return cfg.ConnectTimeoutMs == 0 && cfg.RequestTimeoutMs == 0 && cfg.TLSTimeoutMs == 0 && cfg.IdleTimeoutMs == 0 && cfg.Version == "" && cfg.MaxConnsPerHost == 0 && cfg.MaxIdleConns == 0 && cfg.KeepAlive == nil && cfg.ProxyMode == "" && cfg.UserAgent == "" && cfg.AcceptEncoding == "" && cfg.AcceptLanguage == "" && cfg.MaxRedirects == nil && len(cfg.DefaultHeaders) == 0 && cfg.Proxy.URL == "" && cfg.Proxy.Username == "" && cfg.Proxy.Password == "" && len(cfg.Proxy.NoProxy) == 0 && cfg.Auth.Type == "" && cfg.Auth.Username == "" && cfg.Auth.Password == "" && cfg.Auth.Token == "" && cfg.Auth.Domain == "" && cfg.TLS.MinVersion == "" && cfg.TLS.MaxVersion == "" && !cfg.TLS.InsecureSkipVerify && cfg.TLS.ClientCertFile == "" && cfg.TLS.ClientKeyFile == "" && cfg.TLS.CACertFile == ""
}

func isEmptyBrowserConfig(cfg dsl.BrowserConfig) bool {
	return cfg.Engine == "" && cfg.Headless == nil && cfg.SlowMoMs == 0 && cfg.UserAgent == "" && cfg.DeviceProfile == "" && cfg.TouchMode == nil && cfg.Orientation == "" && cfg.ClientMetrics == nil && cfg.CaptureScreenshots == "" && cfg.CacheEnabled == nil && cfg.CacheContentURLs == nil && cfg.CheckNewerVersions == nil && cfg.NewUserPerIter == nil && cfg.DownloadResources == "" && cfg.Viewport.Width == 0 && cfg.Viewport.Height == 0
}

func isEmptyErrorHandlingConfig(cfg dsl.ErrorHandlingConfig) bool {
	return !cfg.ContinueOnError && cfg.MaxRetries == 0 && cfg.RetryDelayMs == 0 && cfg.RetryBackoff == "" && !cfg.SnapshotOnError
}

func isEmptyWebRuntimeConfig(cfg dsl.WebRuntimeConfig) bool {
	return len(cfg.AdditionalAttributes) == 0 && cfg.DownloadFilters.Mode == "" && len(cfg.DownloadFilters.Hosts) == 0 && len(cfg.ContentChecks) == 0 && cfg.NetworkSimulation.Mode == "" && cfg.NetworkSimulation.Preset == "" && cfg.NetworkSimulation.BandwidthKbps == 0 && cfg.NetworkSimulation.LatencyMs == 0 && cfg.Streaming.Mode == "" && cfg.Streaming.BufferSizeSeconds == 0 && cfg.Streaming.FixedBitrateKbps == 0 && cfg.Streaming.BandwidthEstimation == "" && !cfg.DataFormatting.AutoDetect && len(cfg.DataFormatting.Chain) == 0
}

func detectImportProtocol(functions []ParsedFunction) (string, *dsl.SOAPConfig, string) {
	requestCount := 0
	soapCount := 0
	soapVersion := ""

	for _, fn := range functions {
		if !isRequestFunction(fn.Name) {
			continue
		}
		requestCount++
		if !isSOAPFunction(fn) {
			continue
		}
		soapCount++
		if version := detectSOAPVersion(fn); version == "1.2" {
			soapVersion = "1.2"
		} else if soapVersion == "" {
			soapVersion = "1.1"
		}
	}

	if requestCount == 0 || soapCount == 0 {
		return "http", nil, ""
	}
	if soapCount != requestCount {
		return "http", nil, fmt.Sprintf("detected %d SOAP-style requests and %d non-SOAP requests; keeping HTTP protocol because the imported script is mixed", soapCount, requestCount-soapCount)
	}
	if soapVersion == "" {
		soapVersion = "1.1"
	}
	return "soap", &dsl.SOAPConfig{
		Version: soapVersion,
		FaultHandling: dsl.SOAPFaultHandlingConfig{
			FailOnFault: true,
		},
	}, ""
}

func isRequestFunction(name string) bool {
	switch name {
	case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_service_call":
		return true
	default:
		return false
	}
}

func isSOAPFunction(fn ParsedFunction) bool {
	if fn.Name == "web_service_call" {
		return true
	}
	if strings.TrimSpace(firstNonEmptyArg(fn, "SOAPAction")) != "" {
		return true
	}
	for key := range fn.Headers {
		if strings.EqualFold(key, "SOAPAction") {
			return true
		}
	}
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(fn.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return true
	}
	body := strings.ToLower(strings.TrimSpace(fn.Body))
	if strings.Contains(body, ":envelope") || strings.Contains(body, "<envelope") {
		return true
	}
	if strings.Contains(contentType, "text/xml") && body != "" {
		return strings.Contains(body, "soap") || strings.Contains(body, "<envelope")
	}
	return false
}

func detectSOAPVersion(fn ParsedFunction) string {
	contentType := strings.ToLower(strings.TrimSpace(firstHeaderValue(fn.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return "1.2"
	}
	body := strings.ToLower(strings.TrimSpace(fn.Body))
	if strings.Contains(body, "http://www.w3.org/2003/05/soap-envelope") {
		return "1.2"
	}
	if isSOAPFunction(fn) {
		return "1.1"
	}
	return ""
}

func firstHeaderValue(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}

func applySOAPImportHints(action *dsl.Action, fn ParsedFunction) {
	if action == nil || !isSOAPFunction(fn) {
		return
	}
	if action.Method == "" || strings.EqualFold(action.Method, "GET") {
		action.Method = "POST"
		action.Type = "POST"
	}
	soapAction := strings.TrimSpace(firstNonEmptyArg(fn, "SOAPAction"))
	if soapAction == "" {
		soapAction = strings.TrimSpace(firstHeaderValue(action.Headers, "SOAPAction"))
	}
	operation := strings.TrimSpace(firstNonEmptyArg(fn, "SOAPMethod"))
	if operation == "" {
		operation = strings.TrimSpace(firstNonEmptyArg(fn, "Operation"))
	}
	if operation == "" && soapAction != "" {
		operation = strings.Trim(soapAction, `"`)
	}
	if operation == "" {
		operation = action.Name
	}
	bodyMode := "generated"
	if strings.TrimSpace(action.Body) != "" {
		bodyMode = "raw_xml"
	}
	action.SOAP = &dsl.SOAPActionConfig{
		Action:    strings.Trim(soapAction, `"`),
		Operation: operation,
		BodyMode:  bodyMode,
	}
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}
	if contentType := firstHeaderValue(action.Headers, "Content-Type"); contentType == "" {
		if detectSOAPVersion(fn) == "1.2" {
			action.Headers["Content-Type"] = "application/soap+xml"
		} else {
			action.Headers["Content-Type"] = "text/xml"
		}
	}
}

// convertAction converts a single web_ function to a DSL Action.
func convertAction(fn ParsedFunction, baseURL, transaction string, opts ConvertOptions) (*dsl.Action, []string) {
	var warnings []string

	action := &dsl.Action{
		Headers:  make(map[string]string),
		Metadata: sourceFunctionMetadata(fn),
	}

	// Determine name
	name := sanitizeName(fn.StepName)
	if opts.PreserveTransactions && transaction != "" {
		name = sanitizeName(transaction) + "_" + name
	}
	action.Name = name

	// Set method and type
	action.Method = fn.Method
	action.Type = fn.Method
	if action.Type == "" {
		action.Type = "GET"
		action.Method = "GET"
	}

	// Determine target URL (relative to base)
	rawURL := fn.URL
	if rawURL == "" {
		warnings = append(warnings, fmt.Sprintf("line %d: %s '%s' has no URL, skipping", fn.LineNumber, fn.Name, fn.StepName))
		return nil, warnings
	}

	target := makeRelative(rawURL, baseURL)
	action.Target = target

	// Apply headers
	for k, v := range fn.Headers {
		action.Headers[k] = v
	}

	// Apply body
	if fn.Body != "" {
		action.Body = fn.Body
		// Convert LoadRunner parameter syntax {ParamName} to template syntax.
		// Known .prm-backed parameters are rewritten from var to param after all archive files are parsed.
		action.Body = convertParamReferences(action.Body)
	}

	// Convert parameter references in URL/target too
	action.Target = convertParamReferences(action.Target)

	return action, warnings
}

// convertExtractor converts a web_reg_save_param to a DSL Extractor.
func convertExtractor(fn ParsedFunction) *dsl.Extractor {
	// The name is the first positional arg (web_reg_save_param) or the ParamName= named
	// arg (web_reg_save_param_ex / _regexp / _json / _xpath).
	paramName := firstNonEmpty(fn.StepName, fn.Args["ParamName"])
	if paramName == "" {
		return nil
	}

	ext := &dsl.Extractor{
		Name:   paramName,
		Source: "body",
	}

	// web_reg_save_param_regexp("ParamName=...", "RegExp=...", ...) -> regex extractor.
	if regExp := fn.Args["RegExp"]; regExp != "" {
		ext.Type = "regex"
		ext.Expression = regExp
		return ext
	}
	// web_reg_save_param_json("ParamName=...", "QueryString=$.path", ...) -> jsonpath extractor.
	if jsonQuery := fn.Args["QueryString"]; jsonQuery != "" {
		ext.Type = "jsonpath"
		ext.Expression = jsonQuery
		return ext
	}
	// web_reg_save_param_xpath("ParamName=...", "Query=//node", ...) -> xpath extractor.
	if xpathQuery := fn.Args["Query"]; xpathQuery != "" {
		ext.Type = "xpath"
		ext.Expression = xpathQuery
		return ext
	}

	lb := fn.Args["LB"]
	rb := fn.Args["RB"]

	if lb == "" && rb == "" {
		// Try positional args: web_reg_save_param("Name", "LB=...", "RB=...", ...)
		for _, raw := range fn.RawArgs {
			if strings.HasPrefix(raw, "LB=") {
				lb = strings.TrimPrefix(raw, "LB=")
			}
			if strings.HasPrefix(raw, "RB=") {
				rb = strings.TrimPrefix(raw, "RB=")
			}
		}
	}

	// If boundaries look like JSON path markers, use jsonpath
	if strings.Contains(lb, "\"") && strings.Contains(rb, "\"") {
		ext.Type = "regex"
		ext.Expression = regexp.QuoteMeta(lb) + "(.+?)" + regexp.QuoteMeta(rb)
	} else if lb != "" || rb != "" {
		ext.Type = "boundary"
		ext.Expression = fmt.Sprintf("LB=%s|RB=%s", lb, rb)
	} else {
		ext.Type = "regex"
		ext.Expression = "(.+?)"
	}

	return ext
}

func convertFindAssertion(fn ParsedFunction) (*dsl.Assertion, string) {
	text := firstNonEmptyArg(fn, "Text")
	if text == "" {
		return nil, fmt.Sprintf("line %d: web_reg_find '%s' — manual review recommended because no Text argument was found", fn.LineNumber, fn.StepName)
	}

	assertionType := "body_contains"
	failMode := strings.ToLower(firstNonEmptyArg(fn, "Fail"))
	if failMode == "found" {
		assertionType = "body_not_contains"
	}

	warnings := make([]string, 0, 2)
	if saveCount := firstNonEmptyArg(fn, "SaveCount"); saveCount != "" {
		warnings = append(warnings,
			fmt.Sprintf("line %d: web_reg_find '%s' SaveCount=%s was not preserved; mapped to %s only", fn.LineNumber, fn.StepName, saveCount, assertionType))
	}

	return &dsl.Assertion{
		Type:  assertionType,
		Value: convertParamReferences(text),
	}, strings.Join(warnings, "; ")
}

func firstNonEmptyArg(fn ParsedFunction, key string) string {
	if value := fn.Args[key]; value != "" {
		return value
	}

	prefix := key + "="
	for _, raw := range fn.RawArgs {
		if strings.HasPrefix(raw, prefix) {
			return strings.TrimPrefix(raw, prefix)
		}
	}

	return ""
}

// detectBaseURL finds the common base URL from all web_ function calls.
func detectBaseURL(functions []ParsedFunction) string {
	var urls []string
	for _, fn := range functions {
		if fn.URL != "" && strings.HasPrefix(fn.URL, "http") {
			urls = append(urls, fn.URL)
		}
	}

	if len(urls) == 0 {
		return "https://example.com"
	}

	// Parse first URL to get base
	parsed, err := url.Parse(urls[0])
	if err != nil {
		return urls[0]
	}

	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

// makeRelative strips the base URL from an absolute URL to create a relative path.
func makeRelative(rawURL, baseURL string) string {
	if baseURL != "" && strings.HasPrefix(rawURL, baseURL) {
		rel := strings.TrimPrefix(rawURL, baseURL)
		if rel == "" {
			rel = "/"
		}
		return rel
	}

	// Try to extract just the path
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}

	path := parsed.Path
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if path == "" {
		path = "/"
	}
	return path
}

func collectRecordedResourceURLs(functions []ParsedFunction) []string {
	urls := make([]string, 0)
	seen := make(map[string]struct{})
	for _, fn := range functions {
		if !isRequestFunction(fn.Name) || firstNonEmptyArg(fn, "Resource") != "1" {
			continue
		}
		rawURL := strings.TrimSpace(fn.URL)
		if rawURL == "" {
			rawURL = strings.TrimSpace(fn.Action)
		}
		if rawURL == "" {
			continue
		}
		if _, ok := seen[rawURL]; ok {
			continue
		}
		seen[rawURL] = struct{}{}
		urls = append(urls, rawURL)
	}
	return urls
}

// sanitizeName converts a LoadRunner step name to a valid DSL action name.
func sanitizeName(name string) string {
	if name == "" {
		return "unnamed_action"
	}

	// Replace spaces and special chars with underscores
	replacer := strings.NewReplacer(
		" ", "_",
		"-", "_",
		".", "_",
		"/", "_",
		"\\", "_",
		"(", "",
		")", "",
		"[", "",
		"]", "",
		"'", "",
		"\"", "",
		",", "",
		":", "_",
	)

	result := replacer.Replace(name)

	// Remove consecutive underscores
	for strings.Contains(result, "__") {
		result = strings.ReplaceAll(result, "__", "_")
	}

	// Trim leading/trailing underscores
	result = strings.Trim(result, "_")

	if result == "" {
		return "unnamed_action"
	}

	return result
}

// convertParamReferences converts LoadRunner {ParamName} to APEX Load {{var.paramname}}.
// Archive-level post-processing rewrites known .prm-backed parameters to {{param.name}},
// leaving web_reg_save_param and other dynamic values as {{var.name}}.
func convertParamReferences(s string) string {
	// Match {ParamName} but not {{already.converted}}
	re := regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_]*)\}`)
	return re.ReplaceAllStringFunc(s, func(token string) string {
		match := re.FindStringSubmatch(token)
		if len(match) != 2 {
			return token
		}
		name := canonicalLoadRunnerParameterName(match[1])
		if name == "" {
			return token
		}
		return "{{var." + name + "}}"
	})
}

// computeThinkTimeStats calculates mean and standard deviation for think times.
func computeThinkTimeStats(times []float64) (mean, std float64) {
	if len(times) == 0 {
		return 0, 0
	}

	sum := 0.0
	for _, t := range times {
		sum += t
	}
	mean = sum / float64(len(times))

	if len(times) == 1 {
		return mean, 0
	}

	variance := 0.0
	for _, t := range times {
		diff := t - mean
		variance += diff * diff
	}
	variance /= float64(len(times) - 1)
	std = math.Sqrt(variance)

	return mean, std
}

// RuntimeConfig holds parsed VuGen runtime settings from a .cfg file.
type RuntimeConfig struct {
	ConnectTimeoutSec            int
	ReceiveTimeoutSec            int
	StepTimeoutSec               int
	HTTPVersion                  string // "1.0" or "1.1"
	KeepAlive                    bool
	MaxConnsPerHost              int
	ProxyMode                    string
	ProxyURL                     string
	ProxyUser                    string
	ProxyPass                    string
	UserAgent                    string
	AcceptLanguage               string
	UseGzip                      bool
	SSLVersion                   string
	AuthUser                     string
	AuthPass                     string
	MaxRedirects                 int
	Iterations                   int
	AutoTransactions             string
	BlockScheduling              string
	ActionSequence               []string
	PacingType                   string
	DelayMs                      int
	MinDelayMs                   int
	MaxDelayMs                   int
	IntervalMs                   int
	MinIntervalMs                int
	MaxIntervalMs                int
	ThinkTimeIgnore              bool
	ThinkTimeMultiplier          float64
	ThinkTimeLimitMs             float64
	LogLevel                     string
	OnErrorOnly                  bool
	LogExtendedOptions           []string
	ContinueOnError              bool
	SnapshotOnError              bool
	CacheEnabled                 bool
	CacheContentURLs             bool
	CheckNewerVersions           bool
	NewUserPerIteration          bool
	BrowserEngine                string
	BrowserDeviceProfile         string
	BrowserTouchMode             bool
	BrowserOrientation           string
	BrowserClientMetrics         bool
	BrowserSlowMoMs              int
	BrowserViewportWidth         int
	BrowserViewportHeight        int
	DownloadResources            string
	AdditionalAttributes         map[string]string
	DownloadFilterMode           string
	DownloadFilterHosts          []string
	ContentChecks                []dsl.ContentCheckConfig
	NetworkSimulationMode        string
	NetworkSimulationPreset      string
	NetworkBandwidthKbps         int
	NetworkLatencyMs             int
	StreamingMode                string
	StreamingBufferSizeSeconds   int
	StreamingFixedBitrateKbps    int
	StreamingBandwidthEstimation string
	DataFormattingAutoDetect     bool
	DataFormattingChain          []dsl.DFETransform
}

// ParseRuntimeCfg parses a VuGen default.cfg / runtime settings .cfg file.
func ParseRuntimeCfg(content string) *RuntimeConfig {
	rc := &RuntimeConfig{
		KeepAlive:            true,
		HTTPVersion:          "1.1",
		MaxRedirects:         10,
		AdditionalAttributes: make(map[string]string),
	}

	sections := ParseConfigSections(content)
	httpSection := findSection(sections, "HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences")
	rc.ConnectTimeoutSec = parseInt(httpSection["ConnectTimeout"])
	rc.ReceiveTimeoutSec = parseInt(httpSection["ReceiveTimeout"])
	rc.StepTimeoutSec = parseInt(httpSection["StepDownloadTimeout"])
	if version := strings.TrimSpace(httpSection["HTTPVersion"]); version != "" {
		rc.HTTPVersion = version
	}
	if keepAlive, ok := parseBoolExplicit(httpSection["KeepAlive"]); ok {
		rc.KeepAlive = keepAlive
	}
	if redirects := parseInt(httpSection["MaxRedirections"]); redirects > 0 {
		rc.MaxRedirects = redirects
	}
	rc.MaxConnsPerHost = firstNonZeroInt(httpSection["MaxConnectionsPerHost"], httpSection["MaxConnections"], httpSection["MaxConnectionsPerHostHTTP"])

	proxySection := findSection(sections, "Proxy")
	rc.ProxyMode = normalizeEnum(strings.TrimSpace(firstNonBlank(proxySection["Mode"], proxySection["ProxyMode"], proxySection["Type"])))
	rc.ProxyURL = strings.TrimSpace(firstNonBlank(proxySection["ProxyServer"], proxySection["ProxyURL"], proxySection["Server"]))
	rc.ProxyUser = strings.TrimSpace(firstNonBlank(proxySection["ProxyUserName"], proxySection["UserName"]))
	rc.ProxyPass = strings.TrimSpace(firstNonBlank(proxySection["ProxyPassword"], proxySection["Password"]))

	browserSection := findSection(sections, "Browser Emulation", "Browser")
	rc.UserAgent = strings.TrimSpace(browserSection["UserAgent"])
	rc.AcceptLanguage = strings.TrimSpace(browserSection["AcceptLanguage"])
	rc.UseGzip = strings.Contains(strings.ToLower(browserSection["AcceptEncoding"]), "gzip")
	rc.CacheEnabled = parseBool(browserSection["CacheEnabled"])
	rc.CacheContentURLs = parseBool(firstNonBlank(browserSection["CacheContentURLs"], browserSection["CacheURLsRequiringContent"]))
	rc.CheckNewerVersions = parseBool(firstNonBlank(browserSection["CheckNewerVersions"], browserSection["CheckForNewerPages"]))
	rc.NewUserPerIteration = parseBool(firstNonBlank(browserSection["NewUserPerIteration"], browserSection["SimulateNewUser"]))
	rc.BrowserEngine = normalizeEnum(strings.TrimSpace(firstNonBlank(browserSection["Engine"], browserSection["BrowserEngine"])))
	rc.BrowserDeviceProfile = normalizeEnum(strings.TrimSpace(firstNonBlank(browserSection["DeviceProfile"], browserSection["MobileProfile"], browserSection["EmulatedDevice"])))
	rc.BrowserTouchMode = parseBool(firstNonBlank(browserSection["TouchMode"], browserSection["TouchEnabled"]))
	rc.BrowserOrientation = normalizeEnum(strings.TrimSpace(browserSection["Orientation"]))
	rc.BrowserClientMetrics = parseBool(firstNonBlank(browserSection["ClientMetrics"], browserSection["CollectClientMetrics"]))
	rc.BrowserSlowMoMs = firstNonZeroInt(browserSection["SlowMoMs"], browserSection["SlowMotionMs"])
	rc.BrowserViewportWidth = firstNonZeroInt(browserSection["ViewportWidth"], browserSection["ScreenWidth"])
	rc.BrowserViewportHeight = firstNonZeroInt(browserSection["ViewportHeight"], browserSection["ScreenHeight"])
	rc.DownloadResources = normalizeEnum(strings.TrimSpace(firstNonBlank(browserSection["DownloadResources"], browserSection["DownloadNonHtmlResources"])))

	sslSection := findSection(sections, "SSL", "Security")
	rc.SSLVersion = strings.TrimSpace(firstNonBlank(sslSection["SSLVersion"], sslSection["TLSVersion"]))

	authSection := findSection(sections, "Authentication")
	rc.AuthUser = strings.TrimSpace(firstNonBlank(authSection["UserName"], authSection["Username"]))
	rc.AuthPass = strings.TrimSpace(authSection["Password"])

	runLogicSection := findSection(sections, "Run Logic", "RunLogic")
	rc.Iterations = parseInt(firstNonBlank(runLogicSection["Iterations"], runLogicSection["NumberOfIterations"]))
	rc.AutoTransactions = normalizeEnum(runLogicSection["AutoTransactions"])
	rc.BlockScheduling = normalizeEnum(runLogicSection["BlockScheduling"])
	rc.ActionSequence = splitList(firstNonBlank(runLogicSection["ActionSequence"], runLogicSection["Sequence"]))

	pacingSection := findSection(sections, "Pacing")
	rc.PacingType = mapPacingType(firstNonBlank(pacingSection["Mode"], pacingSection["PacingMode"]))
	rc.DelayMs = firstNonZeroInt(pacingSection["DelayMs"], pacingSection["Delay"])
	rc.MinDelayMs = firstNonZeroInt(pacingSection["MinDelayMs"], pacingSection["MinDelay"])
	rc.MaxDelayMs = firstNonZeroInt(pacingSection["MaxDelayMs"], pacingSection["MaxDelay"])
	rc.IntervalMs = firstNonZeroInt(pacingSection["IntervalMs"], pacingSection["Interval"])
	rc.MinIntervalMs = firstNonZeroInt(pacingSection["MinIntervalMs"], pacingSection["MinInterval"])
	rc.MaxIntervalMs = firstNonZeroInt(pacingSection["MaxIntervalMs"], pacingSection["MaxInterval"])

	thinkTimeSection := findSection(sections, "Think Time", "ThinkTime")
	rc.ThinkTimeIgnore = parseBool(firstNonBlank(thinkTimeSection["Ignore"], thinkTimeSection["IgnoreThinkTime"]))
	rc.ThinkTimeMultiplier = parseFloat(firstNonBlank(thinkTimeSection["Multiplier"], thinkTimeSection["MultiplyRecordedThinkTimeBy"]))
	rc.ThinkTimeLimitMs = parseFloat(firstNonBlank(thinkTimeSection["LimitMs"], thinkTimeSection["LimitThinkTimeMs"]))

	logSection := findSection(sections, "Log", "Logging")
	rc.LogLevel = mapLogLevel(firstNonBlank(logSection["Level"], logSection["LogLevel"]))
	rc.OnErrorOnly = parseBool(firstNonBlank(logSection["OnErrorOnly"], logSection["LogOnErrorOnly"]))
	rc.LogExtendedOptions = splitList(firstNonBlank(logSection["ExtendedOptions"], logSection["ExtendedLogOptions"]))
	if parseBool(logSection["ParameterSubstitution"]) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "parameter_substitution")
	}
	if parseBool(logSection["DataReturned"]) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "data_returned")
	}
	if parseBool(logSection["AdvancedTrace"]) {
		rc.LogExtendedOptions = appendIfMissing(rc.LogExtendedOptions, "advanced_trace")
	}

	miscSection := findSection(sections, "Misc", "Miscellaneous")
	rc.ContinueOnError = parseBool(firstNonBlank(miscSection["ContinueOnError"], miscSection["ErrorContinue"]))
	rc.SnapshotOnError = parseBool(firstNonBlank(miscSection["SnapshotOnError"], miscSection["GenerateSnapshotOnError"]))

	attrsSection := findSection(sections, "Additional Attributes", "Attributes")
	for key, value := range attrsSection {
		trimmedKey := strings.TrimSpace(key)
		if trimmedKey == "" {
			continue
		}
		rc.AdditionalAttributes[trimmedKey] = strings.TrimSpace(value)
	}

	filtersSection := findSection(sections, "Download Filters", "DownloadFilters")
	rc.DownloadFilterMode = normalizeEnum(firstNonBlank(filtersSection["Mode"], filtersSection["FilterMode"]))
	rc.DownloadFilterHosts = splitList(firstNonBlank(filtersSection["Hosts"], filtersSection["IncludeHosts"], filtersSection["ExcludeHosts"]))

	contentCheckSection := findSection(sections, "Content Check", "ContentCheck")
	if text := strings.TrimSpace(firstNonBlank(contentCheckSection["Text"], contentCheckSection["SearchText"])); text != "" {
		failIfFound := !hasExplicitFalse(contentCheckSection["FailIfFound"])
		rc.ContentChecks = append(rc.ContentChecks, dsl.ContentCheckConfig{
			Text:        text,
			Scope:       normalizeEnum(firstNonBlank(contentCheckSection["Scope"], "any_response")),
			FailIfFound: &failIfFound,
		})
	}

	networkSection := findSection(sections, "Network Speed Simulation", "NetworkSimulation")
	rc.NetworkSimulationMode = normalizeEnum(firstNonBlank(networkSection["Mode"], networkSection["SimulationMode"]))
	rc.NetworkSimulationPreset = normalizeEnum(networkSection["Preset"])
	rc.NetworkBandwidthKbps = firstNonZeroInt(networkSection["BandwidthKbps"], networkSection["Bandwidth"])
	rc.NetworkLatencyMs = parseInt(networkSection["LatencyMs"])

	streamingSection := findSection(sections, "Streaming")
	rc.StreamingMode = normalizeEnum(streamingSection["Mode"])
	rc.StreamingBufferSizeSeconds = firstNonZeroInt(streamingSection["BufferSizeSeconds"], streamingSection["BufferSize"])
	rc.StreamingFixedBitrateKbps = firstNonZeroInt(streamingSection["FixedBitrateKbps"], streamingSection["FixedBitrate"])
	rc.StreamingBandwidthEstimation = normalizeEnum(streamingSection["BandwidthEstimation"])

	dataFormattingSection := findSection(sections, "Data Formatting", "DataFormatting")
	rc.DataFormattingAutoDetect = parseBool(firstNonBlank(dataFormattingSection["AutoDetect"], dataFormattingSection["DFEAutoDetect"]))
	for _, item := range splitDelimitedItems(firstNonBlank(dataFormattingSection["Chain"], dataFormattingSection["DFEChain"])) {
		transform := parseDFETransform(item)
		if transform.Type == "" {
			continue
		}
		rc.DataFormattingChain = append(rc.DataFormattingChain, transform)
	}

	return rc
}

func (rc *RuntimeConfig) ToScriptConfig() *dsl.ScriptConfig {
	if rc == nil {
		return nil
	}
	config := &dsl.ScriptConfig{}
	if httpCfg := rc.ToHTTPConfig(); httpCfg != nil {
		config.HTTP = *httpCfg
	}
	if rc.Iterations > 0 || rc.AutoTransactions != "" || rc.BlockScheduling != "" || len(rc.ActionSequence) > 0 {
		config.RunLogic = dsl.RunLogicConfig{
			Iterations:       rc.Iterations,
			AutoTransactions: rc.AutoTransactions,
			BlockScheduling:  rc.BlockScheduling,
			ActionSequence:   append([]string(nil), rc.ActionSequence...),
		}
	}
	if rc.PacingType != "" {
		config.Pacing = dsl.PacingConfig{
			Type:          rc.PacingType,
			DelayMs:       rc.DelayMs,
			MinDelayMs:    rc.MinDelayMs,
			MaxDelayMs:    rc.MaxDelayMs,
			IntervalMs:    rc.IntervalMs,
			MinIntervalMs: rc.MinIntervalMs,
			MaxIntervalMs: rc.MaxIntervalMs,
		}
	}
	if rc.ThinkTimeIgnore || rc.ThinkTimeMultiplier != 0 || rc.ThinkTimeLimitMs != 0 {
		config.ThinkTime = dsl.ThinkTimeConfig{
			Ignore:     rc.ThinkTimeIgnore,
			Multiplier: rc.ThinkTimeMultiplier,
			LimitMs:    rc.ThinkTimeLimitMs,
		}
	}
	if rc.LogLevel != "" || rc.OnErrorOnly || len(rc.LogExtendedOptions) > 0 {
		config.Log = dsl.LogConfig{
			Level:           rc.LogLevel,
			OnErrorOnly:     rc.OnErrorOnly,
			ExtendedOptions: append([]string(nil), rc.LogExtendedOptions...),
		}
	}
	if rc.ContinueOnError || rc.SnapshotOnError {
		config.ErrorHandling = dsl.ErrorHandlingConfig{
			ContinueOnError: rc.ContinueOnError,
			SnapshotOnError: rc.SnapshotOnError,
		}
	}
	if rc.CacheEnabled || rc.CacheContentURLs || rc.CheckNewerVersions || rc.NewUserPerIteration || rc.BrowserEngine != "" || rc.BrowserDeviceProfile != "" || rc.BrowserTouchMode || rc.BrowserOrientation != "" || rc.BrowserClientMetrics || rc.BrowserSlowMoMs != 0 || rc.BrowserViewportWidth != 0 || rc.BrowserViewportHeight != 0 || rc.DownloadResources != "" {
		cacheEnabled := rc.CacheEnabled
		cacheContentURLs := rc.CacheContentURLs
		checkNewerVersions := rc.CheckNewerVersions
		newUserPerIter := rc.NewUserPerIteration
		touchMode := rc.BrowserTouchMode
		clientMetrics := rc.BrowserClientMetrics
		resolvedProfile, profileEngine := resolveImportedBrowserProfile(rc.BrowserDeviceProfile)
		resolvedEngine := rc.BrowserEngine
		if profileEngine != "" {
			resolvedEngine = profileEngine
		}
		config.Browser = dsl.BrowserConfig{
			Engine:             resolvedEngine,
			SlowMoMs:           rc.BrowserSlowMoMs,
			DeviceProfile:      resolvedProfile,
			TouchMode:          &touchMode,
			Orientation:        rc.BrowserOrientation,
			ClientMetrics:      &clientMetrics,
			CacheEnabled:       &cacheEnabled,
			CacheContentURLs:   &cacheContentURLs,
			CheckNewerVersions: &checkNewerVersions,
			NewUserPerIter:     &newUserPerIter,
			DownloadResources:  rc.DownloadResources,
			Viewport: dsl.BrowserViewport{
				Width:  rc.BrowserViewportWidth,
				Height: rc.BrowserViewportHeight,
			},
		}
	}
	if len(rc.AdditionalAttributes) > 0 || rc.DownloadFilterMode != "" || len(rc.DownloadFilterHosts) > 0 || len(rc.ContentChecks) > 0 || rc.NetworkSimulationMode != "" || rc.NetworkSimulationPreset != "" || rc.NetworkBandwidthKbps != 0 || rc.NetworkLatencyMs != 0 || rc.StreamingMode != "" || rc.StreamingBufferSizeSeconds != 0 || rc.StreamingFixedBitrateKbps != 0 || rc.StreamingBandwidthEstimation != "" || rc.DataFormattingAutoDetect || len(rc.DataFormattingChain) > 0 {
		config.WebRuntime = dsl.WebRuntimeConfig{
			AdditionalAttributes: cloneStringMap(rc.AdditionalAttributes),
			DownloadFilters: dsl.DownloadFiltersConfig{
				Mode:  rc.DownloadFilterMode,
				Hosts: append([]string(nil), rc.DownloadFilterHosts...),
			},
			ContentChecks: append([]dsl.ContentCheckConfig(nil), rc.ContentChecks...),
			NetworkSimulation: dsl.NetworkSimulationConfig{
				Mode:          rc.NetworkSimulationMode,
				Preset:        rc.NetworkSimulationPreset,
				BandwidthKbps: rc.NetworkBandwidthKbps,
				LatencyMs:     rc.NetworkLatencyMs,
			},
			Streaming: dsl.StreamingConfig{
				Mode:                rc.StreamingMode,
				BufferSizeSeconds:   rc.StreamingBufferSizeSeconds,
				FixedBitrateKbps:    rc.StreamingFixedBitrateKbps,
				BandwidthEstimation: rc.StreamingBandwidthEstimation,
			},
			DataFormatting: dsl.DataFormattingConfig{
				AutoDetect: rc.DataFormattingAutoDetect,
				Chain:      append([]dsl.DFETransform(nil), rc.DataFormattingChain...),
			},
		}
	}
	return config
}

var loadRunnerBrowserDeviceAliases = map[string]string{
	"ipad_pro": "ipad_air",
}

func resolveImportedBrowserProfile(profile string) (string, string) {
	normalized := normalizeEnum(profile)
	if normalized == "" {
		return "", ""
	}
	if alias, ok := loadRunnerBrowserDeviceAliases[normalized]; ok {
		normalized = alias
	}
	device, ok := browserprotocol.LookupDevice(normalized)
	if !ok {
		return normalized, ""
	}
	return normalized, device.BrowserFamily
}

// ToHTTPConfig converts parsed VuGen runtime settings into a APEX Load HTTPConfig.
func (rc *RuntimeConfig) ToHTTPConfig() *dsl.HTTPConfig {
	cfg := &dsl.HTTPConfig{}

	if rc.ConnectTimeoutSec > 0 {
		cfg.ConnectTimeoutMs = rc.ConnectTimeoutSec * 1000
	}
	if rc.ReceiveTimeoutSec > 0 {
		cfg.RequestTimeoutMs = rc.ReceiveTimeoutSec * 1000
	}
	if rc.StepTimeoutSec > 0 && rc.ReceiveTimeoutSec == 0 {
		cfg.RequestTimeoutMs = rc.StepTimeoutSec * 1000
	}

	cfg.Version = rc.HTTPVersion
	if rc.MaxConnsPerHost > 0 {
		cfg.MaxConnsPerHost = rc.MaxConnsPerHost
	}

	ka := rc.KeepAlive
	cfg.KeepAlive = &ka

	if rc.MaxRedirects != 10 {
		mr := rc.MaxRedirects
		cfg.MaxRedirects = &mr
	}

	if rc.ProxyURL != "" {
		if rc.ProxyMode == "" {
			cfg.ProxyMode = "custom"
		} else {
			cfg.ProxyMode = rc.ProxyMode
		}
		cfg.Proxy = dsl.ProxyConfig{
			URL:      rc.ProxyURL,
			Username: rc.ProxyUser,
			Password: rc.ProxyPass,
		}
	} else if rc.ProxyMode != "" {
		cfg.ProxyMode = rc.ProxyMode
	}

	if rc.UserAgent != "" {
		cfg.UserAgent = rc.UserAgent
	}
	if rc.AcceptLanguage != "" {
		cfg.AcceptLanguage = rc.AcceptLanguage
	}
	if rc.UseGzip {
		cfg.AcceptEncoding = "gzip, deflate"
	}

	if rc.SSLVersion != "" {
		cfg.TLS = dsl.TLSConfig{
			MinVersion: mapSSLVersion(rc.SSLVersion),
		}
	}

	if rc.AuthUser != "" {
		cfg.Auth = dsl.AuthConfig{
			Type:     "basic",
			Username: rc.AuthUser,
			Password: rc.AuthPass,
		}
	}

	return cfg
}

func findSection(sections map[string]map[string]string, aliases ...string) map[string]string {
	for _, alias := range aliases {
		for name, values := range sections {
			if strings.EqualFold(name, alias) {
				return values
			}
		}
	}
	return map[string]string{}
}

func parseInt(value string) int {
	parsed, _ := strconv.Atoi(strings.TrimSpace(value))
	return parsed
}

func parseFloat(value string) float64 {
	parsed, _ := strconv.ParseFloat(strings.TrimSpace(value), 64)
	return parsed
}

func firstNonZeroInt(values ...string) int {
	for _, value := range values {
		if parsed := parseInt(value); parsed != 0 {
			return parsed
		}
	}
	return 0
}

func parseBool(value string) bool {
	parsed, ok := parseBoolExplicit(value)
	return ok && parsed
}

func parseBoolExplicit(value string) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "1", "true", "yes", "on", "enabled":
		return true, true
	case "0", "false", "no", "off", "disabled":
		return false, true
	default:
		return false, false
	}
}

func hasExplicitFalse(value string) bool {
	parsed, ok := parseBoolExplicit(value)
	return ok && !parsed
}

func splitList(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := normalizeEnum(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func splitDelimitedItems(value string) []string {
	parts := strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == '\n' || r == '\r'
	})
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

func parseDFETransform(value string) dsl.DFETransform {
	tokens := strings.Fields(strings.TrimSpace(value))
	if len(tokens) == 0 {
		return dsl.DFETransform{}
	}

	transform := dsl.DFETransform{Type: normalizeEnum(tokens[0])}
	options := make(map[string]string)
	for _, token := range tokens[1:] {
		key, optionValue, found := strings.Cut(token, "=")
		if !found {
			continue
		}
		key = normalizeEnum(key)
		optionValue = strings.TrimSpace(optionValue)
		if key == "" || optionValue == "" {
			continue
		}
		options[key] = optionValue
	}
	if len(options) > 0 {
		transform.Options = options
	}
	return transform
}

func normalizeEnum(value string) string {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	trimmed = strings.ReplaceAll(trimmed, " ", "_")
	trimmed = strings.ReplaceAll(trimmed, "-", "_")
	return trimmed
}

func appendIfMissing(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func mapPacingType(value string) string {
	switch normalizeEnum(value) {
	case "", "none", "immediate", "start_new_iteration_immediately":
		return "none"
	case "fixed_delay", "after_previous_iteration_ends", "delay":
		return "fixed_delay"
	case "random_delay", "after_previous_iteration_ends_random":
		return "random_delay"
	case "fixed_interval", "interval", "start_new_iteration_at_fixed_intervals":
		return "fixed_interval"
	case "random_interval", "start_new_iteration_at_random_intervals":
		return "random_interval"
	case "tps":
		return "tps"
	default:
		return normalizeEnum(value)
	}
}

func mapLogLevel(value string) string {
	switch normalizeEnum(value) {
	case "", "standard":
		return strings.TrimSpace(strings.ToLower(firstNonBlank(value, "")))
	case "disable", "disabled":
		return "disabled"
	case "extended", "verbose":
		return "extended"
	default:
		return normalizeEnum(value)
	}
}

func cloneStringMap(values map[string]string) map[string]string {
	if len(values) == 0 {
		return nil
	}
	clone := make(map[string]string, len(values))
	for key, value := range values {
		clone[key] = value
	}
	return clone
}

// mapSSLVersion converts VuGen SSL version identifiers to standard version strings.
func mapSSLVersion(v string) string {
	switch strings.ToLower(v) {
	case "tls1.0", "tls 1.0", "tls10":
		return "1.0"
	case "tls1.1", "tls 1.1", "tls11":
		return "1.1"
	case "tls1.2", "tls 1.2", "tls12", "tls":
		return "1.2"
	case "tls1.3", "tls 1.3", "tls13":
		return "1.3"
	default:
		return "1.2" // safe default
	}
}
