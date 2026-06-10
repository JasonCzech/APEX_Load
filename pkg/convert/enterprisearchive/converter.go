package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/dsl"
	browserprotocol "github.com/jasonczech/apexload/pkg/protocol/browser"
)

// ConvertResult holds the output of an enterprise script archive to APEX Load DSL conversion.
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
	LogicalActions            int      `json:"logical_actions,omitempty"`
	RawRequestActions         int      `json:"raw_request_actions,omitempty"`
	CollapsedRequests         int      `json:"collapsed_requests,omitempty"`
	TransactionMarkers        int      `json:"transaction_markers,omitempty"`
	ContributorFunctions      int      `json:"contributor_functions,omitempty"`
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

	// HTTP runtime settings mapped from source runtime config.
	HTTPConfig      *dsl.HTTPConfig   // Pre-built HTTP config to embed in script
	RuntimeSettings *dsl.ScriptConfig // Broader runtime settings mapped from default.cfg
}

const regexOrdinalExpressionConfigPrefix = "__apexload_regex_ordinal__:"

type regexOrdinalExpressionConfig struct {
	Expression string `json:"expression"`
	Ordinal    int    `json:"ordinal"`
}

var sourceReplayOrdinalSuffix = regexp.MustCompile(`_\d+$`)

// Convert transforms parsed enterprise C/VUser action files into an APEX Load DSL script.
func Convert(actions []*ParsedAction, opts ConvertOptions) (*ConvertResult, error) {
	result := &ConvertResult{
		Warnings: make([]string, 0),
		Script: &dsl.Script{
			Version:       "1.0.0",
			Protocol:      "http",
			GeneratedBy:   "apexload-enterprise-script-archive-importer",
			GeneratedFrom: "enterprise_script_archive",
			Config:        dsl.ScriptConfig{},
			Actions:       make([]dsl.Action, 0),
		},
	}

	// Collect all functions across action files in order
	var allFunctions []ParsedFunction
	for _, a := range actions {
		for _, fn := range a.Functions {
			if fn.Phase == "" {
				fn.Phase = a.Phase
			}
			allFunctions = append(allFunctions, fn)
		}
		result.Stats.TotalFunctions += len(a.Functions)
	}

	if len(allFunctions) == 0 {
		return nil, fmt.Errorf("no source functions found in action files")
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
		result.Script.Name = "Converted_Enterprise_Script_Archive"
	}

	// Process functions — correlations/extractors come BEFORE the request they apply to
	var pendingExtractors []dsl.Extractor
	var pendingAssertions []dsl.Assertion
	var pendingHeaders map[string]string
	var autoHeaders map[string]string
	var persistentCookies []ParsedCookie
	var pendingSourceMappings []SourceReviewMapping
	var currentTransaction string
	var currentSubTransaction string
	var lastRequestURL string
	var thinkTimes []float64
	var sourceRuntimeControls []ParsedFunction

	actionNameCounts := make(map[string]int)

	for _, fn := range allFunctions {
		switch fn.Name {
		case "web_reg_save_param", "web_reg_save_param_regexp", "web_reg_save_param_json", "web_reg_save_param_xpath", "web_reg_save_param_ex":
			ext := convertExtractor(fn)
			if ext != nil {
				pendingExtractors = append(pendingExtractors, *ext)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", ext.Name, "extractor", "contributor", "Applied to the next converted request action.", SourceFunctionSupportExecutable))
				result.Stats.Extractors++
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			}

		case "web_reg_find":
			assertion, saveCountExtractor, warning := convertFindAssertion(fn)
			mapped := false
			if assertion != nil {
				pendingAssertions = append(pendingAssertions, *assertion)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", assertion.Type, "assertion", "contributor", "Applied to the next converted request action.", SourceFunctionSupportExecutable))
				result.Stats.ContributorFunctions++
				mapped = true
			}
			if saveCountExtractor != nil {
				pendingExtractors = append(pendingExtractors, *saveCountExtractor)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", saveCountExtractor.Name, "extractor", "contributor", "Counts matches for SaveCount and applies the value to the next converted request action.", SourceFunctionSupportExecutable))
				result.Stats.Extractors++
				result.Stats.ContributorFunctions++
				mapped = true
			}
			if mapped {
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			}
			if warning != "" {
				result.Warnings = append(result.Warnings, warning)
			}

		case "web_global_verification":
			if check, warning := convertGlobalVerification(fn); check != nil {
				result.Script.Config.WebRuntime.ContentChecks = append(result.Script.Config.WebRuntime.ContentChecks, *check)
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.content_checks", check.Text, "config", "converted", "Mapped to a global HTTP content check.", SourceFunctionSupportExecutable))
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			} else if warning != "" {
				result.Warnings = append(result.Warnings, warning)
			}

		case "web_add_header":
			if pendingHeaders == nil {
				pendingHeaders = make(map[string]string)
			}
			for k, v := range fn.Headers {
				pendingHeaders[k] = convertParamReferences(v)
			}
			pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", firstNonEmpty(fn.StepName, fn.Name), "headers", "contributor", "Applied to the next converted request action.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_add_auto_header":
			if autoHeaders == nil {
				autoHeaders = make(map[string]string)
			}
			for k, v := range fn.Headers {
				autoHeaders[k] = convertParamReferences(v)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers", firstNonEmpty(fn.StepName, fn.Name), "headers", "converted", "Applied as a persistent auto header until reverted.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_revert_auto_header":
			headerName := firstSourceControlArg(fn)
			if headerName != "" && autoHeaders != nil {
				deleteHeaderCaseInsensitive(autoHeaders, headerName)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers", firstNonEmpty(headerName, fn.Name), "headers", "converted", "Reverted a persistent source auto header.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_add_cookie":
			for _, cookie := range fn.Cookies {
				if cookie.Name == "" {
					continue
				}
				cookie.Value = convertParamReferences(cookie.Value)
				persistentCookies = append(persistentCookies, cookie)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers.Cookie", firstNonEmpty(fn.StepName, fn.Name), "headers", "converted", "Mapped to Cookie headers on matching downstream HTTP requests.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "lr_start_transaction":
			currentTransaction = fn.StepName
			currentSubTransaction = ""
			pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", fn.StepName, "transaction", "contributor", "Applied to generated action names until the transaction ends.", SourceFunctionSupportExecutable))
			result.Stats.Transactions++
			result.Stats.TransactionMarkers++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "lr_end_transaction":
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.source_transaction_groups", fn.StepName, "transaction", "contributor", "Closed a source transaction group.", SourceFunctionSupportExecutable))
			result.Stats.TransactionMarkers++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			currentTransaction = ""
			currentSubTransaction = ""

		case "lr_start_sub_transaction":
			currentSubTransaction = fn.StepName
			if currentTransaction == "" {
				if parent := sourceControlArgAt(fn, 1); parent != "" {
					currentTransaction = parent
				}
			}
			pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", firstNonEmpty(fn.StepName, currentTransaction), "transaction", "contributor", "Applied as a source subtransaction on generated child requests.", SourceFunctionSupportExecutable))
			result.Stats.TransactionMarkers++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "lr_end_sub_transaction":
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.source_transaction_groups", fn.StepName, "transaction", "contributor", "Closed a source subtransaction group.", SourceFunctionSupportExecutable))
			result.Stats.TransactionMarkers++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			currentSubTransaction = ""

		case "lr_think_time":
			sec, ok := ExtractThinkTimeSeconds(fn)
			if ok {
				thinkTimes = append(thinkTimes, sec)
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.think_time", "Think Time", "config", "converted", "Contributes to generated think-time distribution.", SourceFunctionSupportExecutable))
				result.Stats.ThinkTimes++
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			}

		case "lr_continue_on_error":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			applySourceContinueOnError(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.error_handling.continue_on_error", "Continue On Error", "config", "converted", "Mapped from source runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "lr_error_message":
			action := convertLRErrorMessageAction(fn)
			if def, ok := lookupSourceFunction(fn.Name); ok {
				annotateSourceAction(&action, fn, def)
			}
			appendSourceAction(result, fn, action, SourceFunctionSupportExecutable, "Converted to a native error-message action.")

		case "lr_exit":
			preserveSourceRuntimeControl(result.Script, "source_exit_controls", fn, nil)
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				appendSourceAction(result, fn, action, mode, "Converted to a native source runtime action.")
			}

		case "web_cache_cleanup":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			setSourceWebRuntimeAttribute(result.Script, "web_cache_cleanup", "true")
			preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, map[string]any{
				"mapped_to": "config.web_runtime.additional_attributes.web_cache_cleanup",
			})
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.additional_attributes.web_cache_cleanup", "Web Cache Cleanup", "config", "converted", "Mapped from source runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_cleanup_cookies":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			setSourceWebRuntimeAttribute(result.Script, "web_cleanup_cookies", "true")
			preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, map[string]any{
				"mapped_to": "config.web_runtime.additional_attributes.web_cleanup_cookies",
			})
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.additional_attributes.web_cleanup_cookies", "Web Cleanup Cookies", "config", "converted", "Mapped from source runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_set_sockets_option":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			details := applySourceSocketOption(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, stringMapValue(details, "mapped_to", "config.web_runtime.additional_attributes.web_set_sockets_option"), firstNonEmpty(stringMapValue(details, "option", ""), "Socket Option"), "config", "converted", "Mapped from source runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_set_timeout":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			details := applySourceTimeout(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, stringMapValue(details, "mapped_to", "config.web_runtime.additional_attributes.web_set_timeout"), firstNonEmpty(stringMapValue(details, "timeout", ""), "Timeout"), "config", "converted", "Mapped from source runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_set_user":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			details := applySourceWebSetUser(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.http.auth", firstNonEmpty(stringMapValue(details, "username", ""), fn.Name), "config", "converted", "Mapped from source web_set_user authentication.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_set_max_html_param_len":
			value := firstSourceControlArg(fn)
			if value == "" {
				value = fn.StepName
			}
			if value != "" {
				setSourceWebRuntimeAttribute(result.Script, "web_set_max_html_param_len", value)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.additional_attributes.web_set_max_html_param_len", firstNonEmpty(value, fn.Name), "config", "converted", "Preserved as a source HTTP/HTML runtime compatibility setting.", SourceFunctionSupportNativeNoop))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportNativeNoop)

		case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_rest", "web_service_call":
			action, warnings := convertAction(fn, baseURL, currentTransaction, currentSubTransaction, opts, lastRequestURL)
			if contextURL := sourceRequestContextURL(fn, lastRequestURL); contextURL != "" {
				lastRequestURL = contextURL
			}
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
				if len(autoHeaders) > 0 {
					if action.Headers == nil {
						action.Headers = make(map[string]string)
					}
					for k, v := range autoHeaders {
						if _, exists := headerValueCaseInsensitive(action.Headers, k); !exists {
							action.Headers[k] = v
						}
					}
				}
				applyPersistentCookies(action, persistentCookies, rawURLForCookieMatch(fn, lastRequestURL))

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

				actionIndex := actionCountForPhase(result.Script, fn.Phase)
				targetPath := sourceReviewActionPath(fn.Phase, actionIndex)
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, targetPath, action.Name, "action", "converted", "Converted to a generated APEX Load action.", SourceFunctionSupportExecutable))
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

				appendActionForPhase(result.Script, fn.Phase, *action)
				result.Stats.ConvertedActions++
				result.Stats.RawRequestActions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			}
			result.Warnings = append(result.Warnings, warnings...)

		default:
			if isSourceMetadataOnlyContributor(fn.Name) {
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.source_contributor_functions", firstNonEmpty(fn.StepName, fn.Name), "review", "review_only", "Preserved as source import metadata; it does not create a visible script action.", SourceFunctionSupportReviewOnly))
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
				continue
			}
			if def, ok := lookupSourceFunction(fn.Name); ok && def.Family == "transaction" {
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.source_transaction_groups", firstNonEmpty(fn.StepName, fn.Name), "transaction", "contributor", "Preserved as a source transaction marker; it does not create a visible script action.", def.SupportMode))
				result.Stats.TransactionMarkers++
				recordSourceFunctionSupport(result, def.SupportMode)
				continue
			}
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				appendSourceAction(result, fn, action, mode, sourceSupportNotes(fn, mode))
				continue
			}
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			appendSourceReviewMapping(result, sourceReviewMappingForFunction(fn, "metadata.import.preserved.unsupported_functions", fn.Name, "unsupported", "review_only", "No executable APEX Load mapping exists for this source function yet."))
		}
	}

	compactSourceHTTPActions(result)

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

	// Mark main actions with think_time_after (all except last)
	for i := range result.Script.Actions {
		if i < len(result.Script.Actions)-1 {
			result.Script.Actions[i].ThinkTimeAfter = true
		}
	}

	if len(result.Script.Init)+len(result.Script.Actions)+len(result.Script.Teardown) == 0 {
		return nil, fmt.Errorf("no request actions could be converted from the enterprise script archive")
	}

	// Apply HTTP runtime config if provided
	if opts.HTTPConfig != nil {
		result.Script.Config.HTTP = *opts.HTTPConfig
	}
	if opts.RuntimeSettings != nil {
		mergeScriptConfig(&result.Script.Config, opts.RuntimeSettings)
	}
	for _, fn := range sourceRuntimeControls {
		reapplySourceRuntimeControlConfig(result, fn)
	}

	return result, nil
}

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
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "web_convert_param":
		return true
	default:
		return false
	}
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
	case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_rest", "web_service_call":
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

func headerValueCaseInsensitive(headers map[string]string, key string) (string, bool) {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value, true
		}
	}
	return "", false
}

func deleteHeaderCaseInsensitive(headers map[string]string, key string) {
	for headerKey := range headers {
		if strings.EqualFold(headerKey, key) {
			delete(headers, headerKey)
		}
	}
}

func applySOAPImportHints(action *dsl.Action, fn ParsedFunction) {
	if action == nil || !isSOAPFunction(fn) {
		return
	}
	if action.Method == "" || strings.EqualFold(action.Method, "GET") {
		action.Method = "POST"
		action.Type = "POST"
	}
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	soapAction := strings.TrimSpace(firstNonEmptyArg(fn, "SOAPAction"))
	if soapAction == "" {
		soapAction = strings.TrimSpace(firstHeaderValue(action.Headers, "SOAPAction"))
	}
	soapMethod := strings.TrimSpace(firstNonEmptyArg(fn, "SOAPMethod"))
	service, binding, operation := splitSourceSOAPMethod(soapMethod)
	if soapMethod != "" {
		action.Metadata["soap_method"] = soapMethod
	}
	if operation == "" && soapAction != "" {
		operation = strings.Trim(soapAction, `"`)
	}
	if operation == "" {
		operation = strings.TrimSpace(firstNonEmptyArg(fn, "Operation"))
	}
	if operation == "" {
		operation = action.Name
	}
	if explicitService := strings.TrimSpace(firstNonEmptyArg(fn, "Service")); explicitService != "" {
		service = explicitService
	}
	if service != "" {
		action.Metadata["soap_service"] = service
	}
	if binding != "" {
		action.Metadata["soap_binding"] = binding
	}
	if responseParam := strings.TrimSpace(firstNonEmptyArg(fn, "ResponseParam")); responseParam != "" {
		action.Metadata["soap_response_param"] = responseParam
	}
	generatedBody := false
	if strings.TrimSpace(action.Body) == "" {
		if body := generateSourceSOAPBody(fn, operation); body != "" {
			action.Body = body
			generatedBody = true
			action.Metadata["soap_body_generated"] = true
		}
	}
	bodyMode := "generated"
	if strings.TrimSpace(action.Body) != "" && !generatedBody {
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
func convertAction(fn ParsedFunction, baseURL, transaction string, subTransaction string, opts ConvertOptions, previousRequestURL string) (*dsl.Action, []string) {
	var warnings []string

	action := &dsl.Action{
		Headers:  make(map[string]string),
		Metadata: sourceFunctionMetadata(fn),
	}

	// Determine name
	name := sanitizeName(fn.StepName)
	if transaction != "" {
		action.Metadata["source_transaction"] = transaction
		action.Metadata["source_transaction"] = transaction
		if opts.PreserveTransactions {
			name = sanitizeName(transaction) + "_" + name
		}
	}
	if subTransaction != "" {
		action.Metadata["source_subtransaction"] = subTransaction
		action.Metadata["source_subtransaction"] = subTransaction
		if _, exists := action.Metadata["source_action_name"]; !exists {
			action.Metadata["source_action_name"] = subTransaction
		}
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
	rawURL := sourceRequestContextURL(fn, previousRequestURL)
	generatedSOAPEndpoint := false
	if rawURL == "" && fn.Name == "web_service_call" {
		rawURL = sourceSOAPEndpointPlaceholder(fn)
		generatedSOAPEndpoint = rawURL != ""
	}
	if rawURL == "" {
		warnings = append(warnings, fmt.Sprintf("line %d: %s '%s' has no URL, skipping", fn.LineNumber, fn.Name, fn.StepName))
		return nil, warnings
	}
	if generatedSOAPEndpoint {
		action.Metadata["soap_endpoint_placeholder"] = rawURL
		action.Metadata["review_required"] = true
		warnings = append(warnings, fmt.Sprintf("line %d: web_service_call '%s' has no inline endpoint; using %s until WSDL/service settings are mapped", fn.LineNumber, fn.StepName, rawURL))
	}
	if fn.URL == "" && fn.Name == "web_submit_form" {
		action.Metadata["inferred_url_from"] = "source_form_context"
		action.Metadata["inferred_url_source"] = firstNonEmpty(firstNonEmptyArg(fn, "Referer"), previousRequestURL)
	}

	target := makeRelative(rawURL, baseURL)
	action.Target = target
	action.Metadata["original_url"] = rawURL

	// Apply headers
	for k, v := range fn.Headers {
		action.Headers[k] = convertParamReferences(v)
	}
	if contentType := requestContentTypeFromFunction(fn); contentType != "" {
		if _, exists := headerValueCaseInsensitive(action.Headers, "Content-Type"); !exists {
			action.Headers["Content-Type"] = convertParamReferences(contentType)
		}
	}

	// Apply body
	if fn.Body != "" {
		action.Body = unescapeSourceCString(fn.Body)
		// Convert source parameter syntax {ParamName} to template syntax.
		// Known .prm-backed parameters are rewritten from var to param after all archive files are parsed.
		action.Body = convertParamReferences(action.Body)
	}

	// Convert parameter references in URL/target too
	action.Target = convertParamReferences(action.Target)

	return action, warnings
}

func sourceRequestContextURL(fn ParsedFunction, previousRequestURL string) string {
	if strings.TrimSpace(fn.URL) != "" {
		return strings.TrimSpace(fn.URL)
	}
	if fn.Name != "web_submit_form" {
		return ""
	}
	return inferSourceSubmitFormURL(fn, previousRequestURL)
}

func sourceSOAPEndpointPlaceholder(fn ParsedFunction) string {
	service := strings.TrimSpace(firstNonEmptyArg(fn, "Service"))
	if service == "" {
		service, _, _ = splitSourceSOAPMethod(strings.TrimSpace(firstNonEmptyArg(fn, "SOAPMethod")))
	}
	if service == "" {
		service = firstNonEmpty(fn.StepName, "soap_service")
	}
	key := strings.Trim(sanitizeEnvToken(service), "_")
	if key == "" {
		key = "SOAP_SERVICE"
	}
	return "{{env." + key + "_URL}}"
}

func splitSourceSOAPMethod(value string) (service, binding, operation string) {
	parts := strings.Split(value, "|")
	cleaned := make([]string, 0, len(parts))
	for _, part := range parts {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			cleaned = append(cleaned, trimmed)
		}
	}
	if len(cleaned) > 0 {
		service = cleaned[0]
	}
	if len(cleaned) > 1 {
		binding = cleaned[1]
	}
	if len(cleaned) > 2 {
		operation = cleaned[len(cleaned)-1]
	} else if len(cleaned) == 1 {
		operation = cleaned[0]
	}
	return service, binding, operation
}

func generateSourceSOAPBody(fn ParsedFunction, operation string) string {
	operation = sanitizeXMLName(firstNonEmpty(operation, fn.StepName, "Operation"))
	if operation == "" {
		return ""
	}
	payload := webServicePayloadArgs(fn)
	if len(payload) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(`<soapenv:Envelope xmlns:soapenv="http://schemas.xmlsoap.org/soap/envelope/"><soapenv:Body><`)
	b.WriteString(operation)
	b.WriteString(">")
	for _, arg := range payload {
		name := sanitizeXMLName(arg.Name)
		value := convertParamReferences(unescapeSourceCString(arg.Value))
		if strings.HasPrefix(strings.ToLower(arg.Name), "xml:") {
			b.WriteString(value)
			continue
		}
		if name == "" {
			continue
		}
		b.WriteString("<")
		b.WriteString(name)
		b.WriteString(">")
		b.WriteString(escapeXMLText(value))
		b.WriteString("</")
		b.WriteString(name)
		b.WriteString(">")
	}
	b.WriteString("</")
	b.WriteString(operation)
	b.WriteString(`></soapenv:Body></soapenv:Envelope>`)
	return b.String()
}

type webServicePayloadArg struct {
	Name  string
	Value string
}

func webServicePayloadArgs(fn ParsedFunction) []webServicePayloadArg {
	skip := map[string]struct{}{
		"stepname":           {},
		"soapmethod":         {},
		"responseparam":      {},
		"service":            {},
		"expectedresponse":   {},
		"snapshot":           {},
		"soapaction":         {},
		"url":                {},
		"action":             {},
		"body":               {},
		"contenttype":        {},
		"content-type":       {},
		"method":             {},
		"reccontenttype":     {},
		"resource":           {},
		"referer":            {},
		"mode":               {},
		"enctype":            {},
		"targetframe":        {},
		"targetbrowser":      {},
		"ordinal":            {},
		"savecount":          {},
		"scope":              {},
		"requesturl":         {},
		"search":             {},
		"fail":               {},
		"ignoreredirections": {},
	}
	args := make([]webServicePayloadArg, 0)
	for _, arg := range fn.CallArgs {
		if arg.Kind != "key_value" || arg.Name == "" {
			continue
		}
		if _, ok := skip[strings.ToLower(arg.Name)]; ok {
			continue
		}
		args = append(args, webServicePayloadArg{Name: arg.Name, Value: arg.Value})
	}
	return args
}

func sanitizeEnvToken(value string) string {
	var b strings.Builder
	prevUnderscore := false
	for _, r := range strings.ToUpper(value) {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevUnderscore = false
			continue
		}
		if !prevUnderscore {
			b.WriteByte('_')
			prevUnderscore = true
		}
	}
	return b.String()
}

func sanitizeXMLName(value string) string {
	value = strings.TrimSpace(value)
	if strings.HasPrefix(strings.ToLower(value), "xml:") {
		value = value[4:]
	}
	var b strings.Builder
	for i, r := range value {
		valid := (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' || r == ':' || (i > 0 && ((r >= '0' && r <= '9') || r == '-' || r == '.'))
		if valid {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func escapeXMLText(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	value = strings.ReplaceAll(value, `"`, "&quot;")
	value = strings.ReplaceAll(value, "'", "&apos;")
	return value
}

func requestContentTypeFromFunction(fn ParsedFunction) string {
	for _, key := range []string{"ContentType", "Content-Type"} {
		if value := firstNonEmptyArg(fn, key); strings.TrimSpace(value) != "" {
			return unescapeSourceCString(value)
		}
	}
	if encType := firstNonEmptyArg(fn, "EncType"); strings.TrimSpace(encType) != "" && !strings.EqualFold(strings.TrimSpace(encType), "raw") {
		return unescapeSourceCString(encType)
	}
	return ""
}

func rawURLForCookieMatch(fn ParsedFunction, currentRequestURL string) string {
	if currentRequestURL != "" {
		return currentRequestURL
	}
	return sourceRequestContextURL(fn, "")
}

func applyPersistentCookies(action *dsl.Action, cookies []ParsedCookie, rawURL string) {
	if action == nil || len(cookies) == 0 {
		return
	}
	matches := make([]string, 0, len(cookies))
	for _, cookie := range cookies {
		if cookie.Name == "" || !cookieMatchesURL(cookie, rawURL) {
			continue
		}
		matches = append(matches, cookie.Name+"="+convertParamReferences(cookie.Value))
	}
	if len(matches) == 0 {
		return
	}
	if action.Headers == nil {
		action.Headers = make(map[string]string)
	}
	if existing, ok := headerValueCaseInsensitive(action.Headers, "Cookie"); ok && strings.TrimSpace(existing) != "" {
		matches = append([]string{existing}, matches...)
		deleteHeaderCaseInsensitive(action.Headers, "Cookie")
	}
	action.Headers["Cookie"] = strings.Join(matches, "; ")
}

func cookieMatchesURL(cookie ParsedCookie, rawURL string) bool {
	if strings.TrimSpace(cookie.Domain) == "" && strings.TrimSpace(cookie.Path) == "" {
		return true
	}
	parsed, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || parsed.Hostname() == "" {
		return true
	}
	domain := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(cookie.Domain)), ".")
	host := strings.ToLower(parsed.Hostname())
	if domain != "" && host != domain && !strings.HasSuffix(host, "."+domain) {
		return false
	}
	path := strings.TrimSpace(cookie.Path)
	if path == "" {
		return true
	}
	requestPath := parsed.EscapedPath()
	if requestPath == "" {
		requestPath = "/"
	}
	return strings.HasPrefix(requestPath, path)
}

func inferSourceSubmitFormURL(fn ParsedFunction, previousRequestURL string) string {
	candidate := cleanSourceSubmitFormStepName(fn.StepName)
	if candidate == "" {
		return ""
	}
	if isAbsoluteHTTPURL(candidate) {
		return candidate
	}
	base := firstNonEmpty(firstNonEmptyArg(fn, "Referer"), previousRequestURL)
	if base == "" {
		return candidate
	}
	return resolveSourceRelativeURL(candidate, base)
}

func cleanSourceSubmitFormStepName(stepName string) string {
	candidate := strings.TrimSpace(cleanConfigValue(stepName))
	if candidate == "" {
		return ""
	}
	withoutOrdinal := sourceReplayOrdinalSuffix.ReplaceAllString(candidate, "")
	if withoutOrdinal != candidate && strings.Contains(withoutOrdinal, ".") {
		candidate = withoutOrdinal
	}
	return candidate
}

func isAbsoluteHTTPURL(value string) bool {
	lower := strings.ToLower(strings.TrimSpace(value))
	return strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://")
}

func resolveSourceRelativeURL(candidate, base string) string {
	if !strings.ContainsAny(base, "{}") {
		if parsedBase, err := url.Parse(base); err == nil && parsedBase.Scheme != "" {
			if parsedRelative, relErr := url.Parse(candidate); relErr == nil {
				return parsedBase.ResolveReference(parsedRelative).String()
			}
		}
	}

	base = stripURLQueryAndFragment(strings.TrimSpace(base))
	if base == "" {
		return candidate
	}
	if strings.HasPrefix(candidate, "/") {
		if schemeIndex := strings.Index(base, "://"); schemeIndex >= 0 {
			hostStart := schemeIndex + len("://")
			if slashIndex := strings.Index(base[hostStart:], "/"); slashIndex >= 0 {
				return base[:hostStart+slashIndex] + candidate
			}
			return base + candidate
		}
		return candidate
	}
	if !strings.HasSuffix(base, "/") {
		if slashIndex := strings.LastIndex(base, "/"); slashIndex >= 0 {
			base = base[:slashIndex+1]
		} else {
			base += "/"
		}
	}
	return base + candidate
}

func stripURLQueryAndFragment(value string) string {
	if index := strings.IndexAny(value, "?#"); index >= 0 {
		return value[:index]
	}
	return value
}

// convertExtractor converts a web_reg_save_param to a DSL Extractor.
func convertExtractor(fn ParsedFunction) *dsl.Extractor {
	paramName := canonicalSourceParameterName(firstNonEmpty(firstNonEmptyArg(fn, "ParamName"), fn.StepName))
	if paramName == "" {
		return nil
	}

	if fn.Name == "web_reg_save_param_regexp" {
		expression := firstNonEmptyArg(fn, "RegExp")
		if expression == "" {
			return nil
		}
		ext := &dsl.Extractor{
			Name:       paramName,
			Source:     sourceExtractorSource(firstNonEmptyArg(fn, "Scope")),
			Type:       "regex",
			Expression: unescapeSourceCString(expression),
		}
		if ordinal := parsePositiveInt(firstNonEmptyArg(fn, "Ordinal")); ordinal > 1 {
			ext.Type = "regex_ordinal"
			ext.Expression = encodeRegexOrdinalExpression(ext.Expression, ordinal)
		}
		return ext
	}

	if fn.Name == "web_reg_save_param_json" {
		expression := firstNonEmptyArg(fn, "QueryString")
		if expression == "" {
			expression = firstNonEmptyArg(fn, "JSONPath")
		}
		if expression == "" {
			expression = firstNonEmptyArg(fn, "Query")
		}
		if expression == "" {
			return nil
		}
		return &dsl.Extractor{
			Name:       paramName,
			Source:     sourceExtractorSource(firstNonEmptyArg(fn, "Scope")),
			Type:       "jsonpath",
			Expression: unescapeSourceCString(expression),
		}
	}

	if fn.Name == "web_reg_save_param_xpath" {
		expression := firstNonEmptyArg(fn, "QueryString")
		if expression == "" {
			expression = firstNonEmptyArg(fn, "XPath")
		}
		if expression == "" {
			expression = firstNonEmptyArg(fn, "Query")
		}
		if expression == "" {
			return nil
		}
		return &dsl.Extractor{
			Name:       paramName,
			Source:     sourceExtractorSource(firstNonEmptyArg(fn, "Scope")),
			Type:       "xpath",
			Expression: unescapeSourceCString(expression),
		}
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
	lb = unescapeSourceCString(lb)
	rb = unescapeSourceCString(rb)

	ext := &dsl.Extractor{
		Name:   paramName,
		Source: sourceExtractorSource(firstNonEmptyArg(fn, "Scope")),
	}

	if fn.Name == "web_reg_save_param_ex" && (lb != "" || rb != "") {
		ext.Type = "boundary"
		ext.Expression = lb + "|||" + rb
	} else if strings.Contains(lb, "\"") && strings.Contains(rb, "\"") {
		ext.Type = "regex"
		ext.Expression = regexp.QuoteMeta(lb) + "(.+?)" + regexp.QuoteMeta(rb)
	} else if lb != "" || rb != "" {
		ext.Type = "boundary"
		ext.Expression = lb + "|||" + rb
	} else {
		ext.Type = "regex"
		ext.Expression = "(.+?)"
	}

	return ext
}

func convertGlobalVerification(fn ParsedFunction) (*dsl.ContentCheckConfig, string) {
	text := firstNonEmptyArg(fn, "Text")
	if text == "" {
		text = fn.StepName
	}
	text = unescapeSourceCString(text)
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Sprintf("line %d: web_global_verification has no Text argument", fn.LineNumber)
	}
	failIfFound := strings.EqualFold(strings.TrimSpace(firstNonEmptyArg(fn, "Fail")), "Found")
	if strings.TrimSpace(firstNonEmptyArg(fn, "Fail")) == "" {
		failIfFound = true
	}
	return &dsl.ContentCheckConfig{
		Text:        convertParamReferences(text),
		Scope:       normalizeSourceContentCheckScope(firstNonEmpty(firstNonEmptyArg(fn, "Search"), firstNonEmptyArg(fn, "Scope"), "any_response")),
		FailIfFound: &failIfFound,
	}, ""
}

func normalizeSourceContentCheckScope(value string) string {
	scope := normalizeEnum(value)
	switch scope {
	case "", "all", "any", "body", "header", "headers", "response", "global", "any_response":
		return "any_response"
	case "main", "page", "document", "html", "main_document":
		return "main_document"
	case "resource", "resources", "embedded_resource", "embedded_resources":
		return "resource"
	default:
		if strings.Contains(scope, "resource") {
			return "resource"
		}
		if strings.Contains(scope, "document") || strings.Contains(scope, "main") {
			return "main_document"
		}
		return "any_response"
	}
}

func convertFindAssertion(fn ParsedFunction) (*dsl.Assertion, *dsl.Extractor, string) {
	text := firstNonEmptyArg(fn, "Text")
	if text == "" {
		return nil, nil, fmt.Sprintf("line %d: web_reg_find '%s' — manual review recommended because no Text argument was found", fn.LineNumber, fn.StepName)
	}

	assertionType := "body_contains"
	failMode := strings.ToLower(firstNonEmptyArg(fn, "Fail"))
	if failMode == "found" {
		assertionType = "body_not_contains"
	}

	warnings := make([]string, 0, 2)
	var saveCountExtractor *dsl.Extractor
	if saveCount := firstNonEmptyArg(fn, "SaveCount"); saveCount != "" {
		if name := canonicalSourceParameterName(saveCount); name != "" {
			saveCountExtractor = &dsl.Extractor{
				Name:       name,
				Source:     sourceExtractorSource(firstNonEmptyArg(fn, "Search")),
				Type:       "regex_count",
				Expression: regexp.QuoteMeta(convertParamReferences(text)),
			}
		}
	}

	return &dsl.Assertion{
		Type:  assertionType,
		Value: convertParamReferences(text),
	}, saveCountExtractor, strings.Join(warnings, "; ")
}

func sourceExtractorSource(scope string) string {
	switch strings.ToLower(strings.TrimSpace(scope)) {
	case "headers", "header":
		return "header"
	default:
		return "body"
	}
}

func encodeRegexOrdinalExpression(expression string, ordinal int) string {
	payload, err := json.Marshal(regexOrdinalExpressionConfig{Expression: expression, Ordinal: ordinal})
	if err != nil {
		return expression
	}
	return regexOrdinalExpressionConfigPrefix + string(payload)
}

func unescapeSourceCString(value string) string {
	if !strings.Contains(value, `\`) {
		return value
	}
	decoded, err := strconv.Unquote(`"` + value + `"`)
	if err != nil {
		return value
	}
	return decoded
}

func parsePositiveInt(value string) int {
	parsed, err := strconv.Atoi(strings.TrimSpace(cleanConfigValue(value)))
	if err != nil || parsed <= 0 {
		return 0
	}
	return parsed
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
		return detectTemplatedBaseURL(urls[0])
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return detectTemplatedBaseURL(urls[0])
	}

	return fmt.Sprintf("%s://%s", parsed.Scheme, parsed.Host)
}

func detectTemplatedBaseURL(rawURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	if schemeIndex := strings.Index(rawURL, "://"); schemeIndex >= 0 {
		hostStart := schemeIndex + len("://")
		hostEnd := len(rawURL)
		if slashIndex := strings.Index(rawURL[hostStart:], "/"); slashIndex >= 0 {
			hostEnd = hostStart + slashIndex
		}
		if hostEnd > hostStart {
			return rawURL[:hostEnd]
		}
	}
	return rawURL
}

// makeRelative strips the base URL from an absolute URL to create a relative path.
func makeRelative(rawURL, baseURL string) string {
	rawURL = strings.TrimSpace(rawURL)
	baseURL = strings.TrimSpace(baseURL)
	if baseURL != "" {
		if strings.HasPrefix(rawURL, baseURL) {
			rel := strings.TrimPrefix(rawURL, baseURL)
			if rel == "" {
				rel = "/"
			}
			return rel
		}
		if rel, ok := sameOriginRelativeTarget(rawURL, baseURL); ok {
			return rel
		}
	}

	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return rawURL
	}
	return rawURL
}

func sameOriginRelativeTarget(rawURL, baseURL string) (string, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	base, err := url.Parse(baseURL)
	if err != nil || base.Scheme == "" || base.Host == "" {
		return "", false
	}
	if !strings.EqualFold(parsed.Scheme, base.Scheme) || !strings.EqualFold(parsed.Host, base.Host) {
		return "", false
	}
	path := parsed.Path
	if parsed.RawQuery != "" {
		path += "?" + parsed.RawQuery
	}
	if path == "" {
		path = "/"
	}
	return path, true
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

// sanitizeName converts a source step name to a valid DSL action name.
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

// convertParamReferences converts source {ParamName} references to APEX Load {{var.paramname}}.
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
		name := canonicalSourceParameterName(match[1])
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

// RuntimeConfig holds parsed source runtime settings from a .cfg file.
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

// ParseRuntimeCfg parses a source default.cfg / runtime settings .cfg file.
func ParseRuntimeCfg(content string) *RuntimeConfig {
	rc := &RuntimeConfig{
		KeepAlive:            true,
		HTTPVersion:          "1.1",
		MaxRedirects:         10,
		AdditionalAttributes: make(map[string]string),
	}

	sections := ParseConfigSections(content)
	httpSection := findSection(sections, "HTTP", "Internet Protocol", "Preferences", "Internet Protocol Preferences", "WEB", "Web")
	rc.ConnectTimeoutSec = parseInt(httpSection["ConnectTimeout"])
	rc.ReceiveTimeoutSec = parseInt(httpSection["ReceiveTimeout"])
	rc.StepTimeoutSec = parseInt(httpSection["StepDownloadTimeout"])
	if version := strings.TrimSpace(firstNonBlank(httpSection["HTTPVersion"], httpSection["HTTPVer"], httpSection["HttpVer"])); version != "" {
		rc.HTTPVersion = version
	}
	if keepAlive, ok := parseBoolExplicit(httpSection["KeepAlive"]); ok {
		rc.KeepAlive = keepAlive
	}
	if redirects := parseInt(httpSection["MaxRedirections"]); redirects > 0 {
		rc.MaxRedirects = redirects
	}
	if redirects := parseInt(httpSection["MaxRedirectionDepth"]); redirects > 0 {
		rc.MaxRedirects = redirects
	}
	rc.MaxConnsPerHost = firstNonZeroInt(httpSection["MaxConnectionsPerHost"], httpSection["MaxConnections"], httpSection["MaxConnectionsPerHostHTTP"])

	proxySection := findSection(sections, "Proxy", "WEB", "Web")
	rc.ProxyMode = normalizeEnum(strings.TrimSpace(firstNonBlank(proxySection["Mode"], proxySection["ProxyMode"], proxySection["Type"])))
	rc.ProxyURL = strings.TrimSpace(firstNonBlank(proxySection["ProxyServer"], proxySection["ProxyURL"], proxySection["Server"]))
	if rc.ProxyURL == "" {
		rc.ProxyURL = buildRuntimeProxyURL(proxySection)
	}
	rc.ProxyURL = normalizeRuntimeProxyURL(rc.ProxyURL)
	rc.ProxyUser = strings.TrimSpace(firstNonBlank(proxySection["ProxyUserName"], proxySection["UserName"], proxySection["ProxyUser"]))
	rc.ProxyPass = strings.TrimSpace(firstNonBlank(proxySection["ProxyPassword"], proxySection["Password"]))

	browserSection := findSection(sections, "Browser Emulation", "Browser", "WEB", "Web")
	rc.UserAgent = strings.TrimSpace(firstNonBlank(browserSection["CustomUserAgent"], browserSection["UserAgent"], browserSection["BrowserUserAgent"]))
	rc.AcceptLanguage = strings.TrimSpace(firstNonBlank(browserSection["AcceptLanguage"], browserSection["BrowserAcceptLanguage"], browserSection["NavigatorBrowserLanguage"], browserSection["NavigatorUserLanguage"]))
	acceptEncoding := firstNonBlank(browserSection["AcceptEncoding"], browserSection["BrowserAcceptEncoding"])
	rc.UseGzip = strings.Contains(strings.ToLower(acceptEncoding), "gzip")
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
	if strings.Contains(normalizeEnum(thinkTimeSection["Options"]), "nothink") {
		rc.ThinkTimeIgnore = true
	}
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
			Scope:       normalizeSourceContentCheckScope(firstNonBlank(contentCheckSection["Scope"], "any_response")),
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

var sourceBrowserDeviceAliases = map[string]string{
	"ipad_pro": "ipad_air",
}

func resolveImportedBrowserProfile(profile string) (string, string) {
	normalized := normalizeEnum(profile)
	if normalized == "" {
		return "", ""
	}
	if alias, ok := sourceBrowserDeviceAliases[normalized]; ok {
		normalized = alias
	}
	device, ok := browserprotocol.LookupDevice(normalized)
	if !ok {
		return normalized, ""
	}
	return normalized, device.BrowserFamily
}

// ToHTTPConfig converts parsed source runtime settings into an APEX Load HTTPConfig.
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

func buildRuntimeProxyURL(section map[string]string) string {
	host := strings.TrimSpace(firstNonBlank(section["ProxyHTTPSHost"], section["ProxyHTTPHost"]))
	if host == "" {
		return ""
	}
	port := firstNonZeroInt(section["ProxyHTTPSPort"], section["ProxyHTTPPort"])
	scheme := "http"
	if strings.TrimSpace(section["ProxyHTTPSHost"]) != "" {
		scheme = "https"
	}
	if strings.HasPrefix(host, "http://") || strings.HasPrefix(host, "https://") {
		if port > 0 && !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(host, "https://"), "http://"), ":") {
			return strings.TrimRight(host, "/") + ":" + strconv.Itoa(port)
		}
		return host
	}
	if port > 0 {
		return fmt.Sprintf("%s://%s:%d", scheme, host, port)
	}
	return scheme + "://" + host
}

func normalizeRuntimeProxyURL(rawURL string) string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return ""
	}
	parsed, err := url.Parse(trimmed)
	if err != nil || !strings.EqualFold(parsed.Scheme, "https") {
		return trimmed
	}
	switch parsed.Port() {
	case "3128", "8080", "8888":
		parsed.Scheme = "http"
		return parsed.String()
	default:
		return trimmed
	}
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

// mapSSLVersion converts source SSL version identifiers to standard version strings.
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
