package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// ConvertResult holds the output of an enterprise script archive to APEX Load DSL conversion.
type ConvertResult struct {
	Script                       *dsl.Script           `json:"script"`
	Scripts                      []*dsl.Script         `json:"scripts,omitempty"`
	Warnings                     []string              `json:"warnings,omitempty"`
	Stats                        ConvertStats          `json:"stats"`
	DetectedProtocol             *DetectedProtocol     `json:"detected_protocol,omitempty"`
	FidelityReport               *ImportFidelityReport `json:"fidelity_report,omitempty"`
	ReviewArtifact               *ReviewArtifact       `json:"review_artifact,omitempty"`
	SourceReview                 *SourceReview         `json:"source_review,omitempty"`
	RecordedResourceURLs         []string              `json:"recorded_resource_urls,omitempty"`
	RecordedResourceURLsCaptured bool                  `json:"recorded_resource_urls_captured,omitempty"`
	// ProtectedRequirements is a one-shot, in-memory handoff for source runtime
	// credential values. It is deliberately excluded from converter JSON.
	ProtectedRequirements credentialpolicy.ProtectedRequirements `json:"-"`

	// flowWarned deduplicates control-flow translation warnings per distinct
	// condition/loop source text during one conversion.
	flowWarned map[string]bool
}

func (result *ConvertResult) effectiveScripts() []*dsl.Script {
	if result == nil {
		return nil
	}
	if len(result.Scripts) > 0 {
		return result.Scripts
	}
	if result.Script != nil {
		return []*dsl.Script{result.Script}
	}
	return nil
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
	// deferArchiveFinalization is set by ConvertZip so request credential
	// projection runs only after parameter files have been attached and known
	// source parameters have been rewritten to {{param.*}} references.
	deferArchiveFinalization bool
	// Render lr_unmask(...) request bodies as governed secret references. Archive
	// imports enable this so payload bytes never leak while operators get a
	// re-provisioning handle; lower-level conversions keep opaque bodies empty.
	UseMaskedBodySecretReferences bool
	// AllowJavaVuserRunner is retained for API compatibility. Java archives now
	// always produce a governed java_vuser draft, so this option is deprecated
	// and has no effect.
	AllowJavaVuserRunner bool

	// HTTP runtime settings mapped from source runtime config.
	HTTPConfig      *dsl.HTTPConfig   // Pre-built HTTP config to embed in script
	RuntimeSettings *dsl.ScriptConfig // Broader runtime settings mapped from default.cfg
}

const regexOrdinalExpressionConfigPrefix = "__apexload_regex_ordinal__:"

type regexOrdinalExpressionConfig struct {
	Expression string `json:"expression"`
	Ordinal    int    `json:"ordinal"`
}

const boundaryExpressionConfigPrefix = "__apexload_boundary_config__:"

type boundaryExpressionConfig struct {
	Left            string `json:"left"`
	Right           string `json:"right"`
	Ordinal         int    `json:"ordinal,omitempty"`
	CaseInsensitive bool   `json:"case_insensitive,omitempty"`
}

var sourceReplayOrdinalSuffix = regexp.MustCompile(`_\d+$`)

func disclosureSafeSourceProjectionFunctions(functions []ParsedFunction) []ParsedFunction {
	safe := make([]ParsedFunction, 0, len(functions))
	for _, fn := range functions {
		if sourceFunctionHasUnsupportedTransactionDependency(fn) {
			continue
		}
		safe = append(safe, fn)
	}
	return safe
}

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
	hasCSourceParseFailures := false
	for _, a := range actions {
		if a == nil {
			continue
		}
		hasCSourceParseFailures = hasCSourceParseFailures || len(a.ParseFailures) > 0
		for _, fn := range a.Functions {
			if fn.Phase == "" {
				fn.Phase = a.Phase
			}
			allFunctions = append(allFunctions, fn)
		}
		result.Stats.TotalFunctions += len(a.Functions)
	}

	if len(allFunctions) == 0 && !hasCSourceParseFailures {
		return nil, fmt.Errorf("no source functions found in action files")
	}

	projectionFunctions := disclosureSafeSourceProjectionFunctions(allFunctions)
	protocolName, soapConfig, protocolWarning := detectImportProtocol(projectionFunctions)
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
		baseURL = detectBaseURL(projectionFunctions)
	}
	result.Script.Config.BaseURL = baseURL
	result.RecordedResourceURLs = collectRecordedResourceURLs(projectionFunctions)
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
	// Track the last emitted request action (phase + index) so a deprecated
	// web_find — which checks the PREVIOUS response — can attach its assertion to
	// that request rather than forward onto the next one.
	var lastRequestPhase string
	lastRequestIndex := -1
	// Literal lr_think_time calls belong after the immediately preceding
	// executable action, which may be a protocol request or a source/engine
	// action such as lr_end_transaction. Keeping this separate from
	// lastRequest* preserves web_find's previous-response behavior.
	var lastExecutablePhase string
	lastExecutableIndex := -1
	lastExecutableActionCount := -1
	var lastExecutableConditionIDs []string
	var lastExecutableLoopIDs []string
	var sourceRuntimeControls []ParsedFunction

	actionNameCounts := make(map[string]int)
	trackLastExecutable := func(fn ParsedFunction) {
		lastExecutablePhase = fn.Phase
		lastExecutableIndex = actionCountForPhase(result.Script, fn.Phase) - 1
		lastExecutableActionCount = len(result.Script.Init) + len(result.Script.Actions) + len(result.Script.Teardown)
		lastExecutableConditionIDs = append(lastExecutableConditionIDs[:0], fn.ConditionIDs...)
		lastExecutableLoopIDs = append(lastExecutableLoopIDs[:0], fn.LoopIDs...)
	}
	appendTrackedSourceAction := func(fn ParsedFunction, action dsl.Action, mode SourceFunctionSupportMode, notes string) {
		appendSourceAction(result, fn, action, mode, notes)
		if !sourceFunctionHasUnsupportedTransactionDependency(fn) &&
			(mode == SourceFunctionSupportExecutable || mode == SourceFunctionSupportNativeNoop) {
			trackLastExecutable(fn)
		}
	}

	for _, fn := range allFunctions {
		if sourceFunctionHasUnsupportedTransactionDependency(fn) &&
			!nativeSourceTransactionReviewUnsupported(fn.Name) {
			notes := "Execution depends on an unsupported native transaction return value; preserved as a disclosure-safe review action and not executed."
			appendSourceAction(result, fn, dsl.Action{
				Name: disclosureSafeSourceReviewActionName(fn),
				Type: "source_review",
				Params: map[string]any{
					"function": fn.Name,
				},
			}, SourceFunctionSupportReviewOnly, notes)
			result.Warnings = append(result.Warnings, fmt.Sprintf(
				"%s:%d %s requires review because its transaction-dependent expression cannot execute natively",
				fn.Filename,
				fn.LineNumber,
				fn.Name,
			))
			continue
		}
		switch fn.Name {
		case "web_reg_save_param", "web_reg_save_param_regexp", "web_reg_save_param_json", "web_reg_save_param_xpath", "web_reg_save_param_ex", "web_reg_save_param_attrib":
			ext, extractorWarnings := convertExtractor(fn)
			result.Warnings = append(result.Warnings, extractorWarnings...)
			if ext != nil {
				pendingExtractors = append(pendingExtractors, *ext)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", ext.Name, "extractor", "contributor", "Applied to the next converted request action.", SourceFunctionSupportExecutable))
				result.Stats.Extractors++
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			}

		case "web_reg_find":
			assertion, saveCountExtractor, supportMode, warning := convertFindAssertion(fn)
			mapped := false
			if assertion != nil {
				pendingAssertions = append(pendingAssertions, *assertion)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", assertion.Type, "assertion", "contributor", "Applied to the next converted request action.", supportMode))
				result.Stats.ContributorFunctions++
				mapped = true
			}
			if saveCountExtractor != nil {
				pendingExtractors = append(pendingExtractors, *saveCountExtractor)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", saveCountExtractor.Name, "extractor", "contributor", "Counts matches for SaveCount and applies the value to the next converted request action.", supportMode))
				result.Stats.Extractors++
				result.Stats.ContributorFunctions++
				mapped = true
			}
			if mapped {
				recordSourceFunctionSupport(result, supportMode)
			}
			if warning != "" {
				result.Warnings = append(result.Warnings, warning)
			}

		case "web_find":
			// Deprecated inline verification (predecessor of web_reg_find). It checks
			// the *previous* response; the source argument is "What=" rather than
			// web_reg_find's "Text=", so alias it before reusing the same mapping.
			if fn.Args != nil && fn.Args["Text"] == "" && fn.Args["What"] != "" {
				fn.Args["Text"] = fn.Args["What"]
			}
			assertion, saveCountExtractor, supportMode, warning := convertFindAssertion(fn)
			// web_find validates the previous response, so attach to the LAST emitted
			// request action. pendingAssertions/pendingExtractors would flush forward
			// onto the NEXT request, validating the wrong response.
			prev := actionAtPhaseIndex(result.Script, lastRequestPhase, lastRequestIndex)
			prevPath := sourceReviewActionPath(lastRequestPhase, lastRequestIndex)
			mapped := false
			if assertion != nil {
				if prev != nil {
					prev.Assertions = append(prev.Assertions, *assertion)
					appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, prevPath, assertion.Type, "assertion", "contributor", "Mapped from web_find; applied to the preceding request's response (web_find checks the previous page).", supportMode))
				} else {
					// No preceding request: preserve the check by attaching forward, and
					// warn so the relocation is not silent.
					pendingAssertions = append(pendingAssertions, *assertion)
					pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", assertion.Type, "assertion", "contributor", "Mapped from web_find with no preceding request; applied to the next converted request action.", supportMode))
					result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: web_find has no preceding request; its content check was attached to the following request", fn.LineNumber))
				}
				result.Stats.ContributorFunctions++
				mapped = true
			}
			if saveCountExtractor != nil {
				if prev != nil {
					prev.Extractors = append(prev.Extractors, *saveCountExtractor)
					appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, prevPath, saveCountExtractor.Name, "extractor", "contributor", "Counts web_find matches against the preceding request's response.", supportMode))
				} else {
					pendingExtractors = append(pendingExtractors, *saveCountExtractor)
					pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", saveCountExtractor.Name, "extractor", "contributor", "Counts web_find matches and applies the value to the next converted request action.", supportMode))
				}
				result.Stats.Extractors++
				result.Stats.ContributorFunctions++
				mapped = true
			}
			if mapped {
				recordSourceFunctionSupport(result, supportMode)
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
			pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "headers", "contributor", "Applied to the next converted request action.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_add_auto_header":
			if autoHeaders == nil {
				autoHeaders = make(map[string]string)
			}
			for k, v := range fn.Headers {
				autoHeaders[k] = convertParamReferences(v)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "headers", "converted", "Applied as a persistent auto header until reverted.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_revert_auto_header", "web_remove_auto_header":
			headerName := firstSourceControlArg(fn)
			if headerName != "" && autoHeaders != nil {
				deleteHeaderCaseInsensitive(autoHeaders, headerName)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers", convertutil.FirstNonEmpty(headerName, fn.Name), "headers", "converted", "Removed a persistent source auto header.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_cleanup_auto_headers":
			// Drop every persistent auto header so downstream requests stop carrying them.
			autoHeaders = nil
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers", fn.Name, "headers", "converted", "Cleared all persistent source auto headers.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_add_cookie", "web_add_cookie_ex", "web_reg_add_cookie":
			for _, cookie := range fn.Cookies {
				if cookie.Name == "" {
					continue
				}
				cookie.Value = convertParamReferences(cookie.Value)
				persistentCookies = append(persistentCookies, cookie)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers.Cookie", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "headers", "converted", "Mapped to Cookie headers on matching downstream HTTP requests.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_remove_cookie":
			// Drop a previously-added cookie (or ALL) from the persistent set so it
			// stops attaching to downstream requests.
			removed := removePersistentCookie(&persistentCookies, fn)
			targetPath := "actions[].headers.Cookie"
			notes := "Removed a source cookie from downstream HTTP requests."
			if !removed {
				targetPath = "metadata.import.preserved.source_runtime_controls"
				notes = "web_remove_cookie referenced a cookie not added earlier in this script; preserved for review."
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, targetPath, convertutil.FirstNonEmpty(fn.StepName, fn.Name), "headers", "converted", notes, SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "lr_start_transaction":
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				action = labelSourceTransactionMarker(action, fn, "Start")
				appendTrackedSourceAction(fn, action, mode, "Started an executable source transaction at the original source position.")
			}
			currentTransaction = fn.StepName
			currentSubTransaction = ""
			result.Stats.Transactions++
			result.Stats.TransactionMarkers++

		case "lr_end_transaction":
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				action = labelSourceTransactionMarker(action, fn, "End")
				appendTrackedSourceAction(fn, action, mode, "Ended the executable source transaction with the source PASS/FAIL status.")
			}
			result.Stats.TransactionMarkers++
			currentTransaction = ""
			currentSubTransaction = ""

		case "lr_start_sub_transaction":
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				action = labelSourceTransactionMarker(action, fn, "Start")
				appendTrackedSourceAction(fn, action, mode, "Started an executable source subtransaction at the original source position.")
			}
			currentSubTransaction = fn.StepName
			if currentTransaction == "" {
				if parent := sourceControlArgAt(fn, 1); parent != "" {
					currentTransaction = parent
				}
			}
			result.Stats.TransactionMarkers++

		case "lr_end_sub_transaction", "lr_end_sub_trans":
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				action = labelSourceTransactionMarker(action, fn, "End")
				appendTrackedSourceAction(fn, action, mode, "Ended the executable source subtransaction with the source PASS/FAIL status.")
			}
			result.Stats.TransactionMarkers++
			currentSubTransaction = ""

		case "lr_set_transaction_status", "lr_set_transaction_status_by_name",
			"lr_fail_trans_with_error", "lr_stop_transaction", "lr_resume_transaction",
			"lr_set_transaction":
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				appendTrackedSourceAction(fn, action, mode, "Preserved as an executable source transaction operation at the original source position.")
			}
			result.Stats.TransactionMarkers++
			if fn.Name == "lr_set_transaction" {
				result.Stats.Transactions++
			}

		case "lr_think_time":
			sec, ok := ExtractThinkTimeSeconds(fn)
			prev := actionAtPhaseIndex(result.Script, lastExecutablePhase, lastExecutableIndex)
			adjacent := prev != nil &&
				strings.EqualFold(strings.TrimSpace(fn.Phase), strings.TrimSpace(lastExecutablePhase)) &&
				len(result.Script.Init)+len(result.Script.Actions)+len(result.Script.Teardown) == lastExecutableActionCount &&
				stringSlicesEqual(fn.ConditionIDs, lastExecutableConditionIDs) &&
				stringSlicesEqual(fn.LoopIDs, lastExecutableLoopIDs)
			waitMs := sec * 1000
			if ok && sec >= 0 && !math.IsNaN(waitMs) && !math.IsInf(waitMs, 0) &&
				waitMs <= dsl.MaxConfiguredProtocolTimeoutMS && adjacent {
				if prev.ThinkTimeAfter && strings.EqualFold(prev.ThinkTime.Type, "fixed") {
					if opts.RuntimeSettings != nil && opts.RuntimeSettings.ThinkTime.LimitMs > 0 {
						result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d consecutive lr_think_time calls cannot be combined exactly when the source think-time cap is enabled; preserving the preceding wait and requiring review", fn.Filename, fn.LineNumber))
						appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.unplaced_think_times", "Think Time", "config", "review_only", "Consecutive literal wait cannot be combined without changing per-call cap semantics; the preceding wait is preserved and this wait uses the zero-delay fallback.", SourceFunctionSupportReviewOnly))
						recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
						break
					}
					waitMs += prev.ThinkTime.MeanMs
				}
				if waitMs > dsl.MaxConfiguredProtocolTimeoutMS {
					result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d consecutive lr_think_time calls exceed the destination wait limit; preserving the preceding wait and requiring review", fn.Filename, fn.LineNumber))
					appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.unplaced_think_times", "Think Time", "config", "review_only", "Combined literal think time exceeds the destination limit; the preceding wait is preserved and this wait uses the zero-delay fallback.", SourceFunctionSupportReviewOnly))
					recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
					break
				}
				prev.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: waitMs}
				prev.ThinkTimeAfter = true
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, sourceReviewActionPath(lastExecutablePhase, lastExecutableIndex)+".think_time", "Think Time", "config", "converted", "Preserved as the preceding executable action's exact recorded wait.", SourceFunctionSupportExecutable))
				result.Stats.ThinkTimes++
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			} else {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d lr_think_time could not be placed as an exact wait; using zero delay and requiring review", fn.Filename, fn.LineNumber))
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.unplaced_think_times", "Think Time", "config", "review_only", "Dynamic, malformed, or unplaceable think time; zero-delay fallback requires review.", SourceFunctionSupportReviewOnly))
				recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
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
			appendTrackedSourceAction(fn, action, SourceFunctionSupportExecutable, "Converted to a native error-message action.")

		case "lr_exit":
			preserveSourceRuntimeControl(result.Script, "source_exit_controls", fn, nil)
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				appendTrackedSourceAction(fn, action, mode, "Converted to a native source runtime action.")
			}

		case "web_cache_cleanup":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			setSourceWebRuntimeAttribute(result.Script, "web_cache_cleanup", "true")
			preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, map[string]any{
				"mapped_to": "actions[].source_runtime",
			})
			def, _ := lookupSourceFunction(fn.Name)
			action := convertGenericSourceAction(fn, "source_runtime", def)
			appendTrackedSourceAction(fn, action, SourceFunctionSupportExecutable, "Clears the active protocol's simulated HTTP response cache at the original source position.")

		case "web_cleanup_cookies":
			// Cookie cleanup is behavioral: emit a positional, executable
			// cookie_clear action so the clear happens exactly where the source
			// called it, instead of flattening it into a non-executed attribute.
			action := convertWebCleanupCookiesAction(fn)
			// web_cleanup_cookies removes ALL cookies, so also drop the importer's
			// persistent set. Otherwise web_add_cookie cookies (stamped as literal
			// Cookie headers on later requests by applyPersistentCookies) would
			// keep attaching after the cleanup, replaying cleared session/auth state.
			persistentCookies = nil
			preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, map[string]any{
				"mapped_to": "actions[].cookie_clear",
			})
			appendTrackedSourceAction(fn, action, SourceFunctionSupportExecutable, "Converted to an executable cookie_clear action.")

		case "web_set_sockets_option":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			details := applySourceSocketOption(result, fn)
			supportMode := SourceFunctionSupportExecutable
			mappingStatus := "converted"
			mappingNotes := "Mapped from source runtime control."
			if partial, _ := details["mapping_partial"].(bool); partial {
				supportMode = SourceFunctionSupportReviewOnly
				mappingStatus = "review_only"
				mappingNotes = stringMapValue(details, "review_reason", "Unsupported source socket option; APEX defaults remain unchanged and review is required.")
				result.Warnings = append(result.Warnings, mappingNotes)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, stringMapValue(details, "mapped_to", "config.web_runtime.additional_attributes.web_set_sockets_option"), convertutil.FirstNonEmpty(stringMapValue(details, "option", ""), "Socket Option"), "config", mappingStatus, mappingNotes, supportMode))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, supportMode)

		case "web_set_timeout":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			details := applySourceTimeout(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, stringMapValue(details, "mapped_to", "config.web_runtime.additional_attributes.web_set_timeout"), convertutil.FirstNonEmpty(stringMapValue(details, "timeout", ""), "Timeout"), "config", "converted", "Mapped from source runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_set_user":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			details := applySourceWebSetUser(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.http.auth", convertutil.FirstNonEmpty(stringMapValue(details, "username", ""), fn.Name), "config", "converted", "Mapped from source web_set_user authentication.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_set_option", "web_set_max_retries", "web_disable_keep_alive", "web_enable_keep_alive",
			"web_set_certificate_ex", "web_set_proxy", "web_set_secure_proxy", "web_set_proxy_bypass", "web_set_proxy_bypass_local":
			// Connection/session/TLS/proxy controls → config.http (and web_runtime
			// attributes for unmapped options). Reapplied after opts.HTTPConfig so the
			// source-declared setting wins, mirroring web_set_timeout/web_set_sockets_option.
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			details := applySourceWebRuntimeControl(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, stringMapValue(details, "mapped_to", "config.web_runtime.additional_attributes."+fn.Name), convertutil.FirstNonEmpty(stringMapValue(details, "summary", ""), fn.Name), "config", "converted", "Mapped from source HTTP runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_add_filter", "web_add_auto_filter", "web_remove_auto_filter", "web_load_cache", "web_dump_cache":
			// APEX Load fetches only the scripted requests (no auto-resource download)
			// and keeps no client-side response cache, so download filters and cache
			// load/dump are guaranteed native no-ops. Preserve as a compatibility
			// attribute for round-trip fidelity.
			value := firstSourceControlArg(fn)
			if value == "" {
				value = "true"
			}
			setSourceWebRuntimeAttribute(result.Script, fn.Name, value)
			preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, map[string]any{
				"mapped_to": "config.web_runtime.additional_attributes." + fn.Name,
			})
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.additional_attributes."+fn.Name, fn.Name, "config", "converted", "APEX Load fetches only scripted requests and keeps no client cache; this control is a native no-op.", SourceFunctionSupportNativeNoop))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportNativeNoop)

		case "web_concurrent_start", "web_concurrent_end":
			// APEX Load has no concurrent-request primitive; requests inside a
			// web_concurrent_start/web_concurrent_end block are emitted and replayed
			// sequentially. Preserve the markers and warn so the serialization is
			// explicit rather than silently lost.
			preserveSourceRuntimeControl(result.Script, "source_concurrent_groups", fn, map[string]any{"marker": fn.Name})
			if fn.Name == "web_concurrent_start" {
				result.Warnings = append(result.Warnings, fmt.Sprintf("line %d: web_concurrent_start group is replayed sequentially; APEX Load has no concurrent-request primitive, so the parallel timing of the grouped requests is not preserved", fn.LineNumber))
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.source_concurrent_groups", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "review", "converted", "Concurrent-request block marker preserved; the inner requests are replayed sequentially.", SourceFunctionSupportNativeNoop))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportNativeNoop)

		case "web_convert_param":
			action := convertWebConvertParamAction(fn)
			appendTrackedSourceAction(fn, action, SourceFunctionSupportExecutable, "Converted to an executable source parameter transform.")

		case "web_set_max_html_param_len":
			value := firstSourceControlArg(fn)
			if value == "" {
				value = fn.StepName
			}
			if value != "" {
				setSourceWebRuntimeAttribute(result.Script, "web_set_max_html_param_len", value)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.additional_attributes.web_set_max_html_param_len", convertutil.FirstNonEmpty(value, fn.Name), "config", "converted", "Preserved as a source HTTP/HTML runtime compatibility setting.", SourceFunctionSupportNativeNoop))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportNativeNoop)

		case "web_url", "web_submit_data", "web_submit_form", "web_custom_request", "web_rest", "web_service_call",
			"web_link", "web_image", "web_image_check":
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
				mappingStatus := "converted"
				mappingNotes := "Converted to a generated APEX Load action."
				mappingMode := SourceFunctionSupportExecutable
				if metadataBool(action.Metadata, "masked_body") {
					mappingStatus = "review_only"
					mappingNotes = fmt.Sprintf("Request argument is produced by %s(...); the masked payload is bound to the recording machine's key and cannot be recovered at import time — the action requires manual review.", metadataString(action.Metadata, "masked_body_function"))
					mappingMode = SourceFunctionSupportReviewOnly
				}
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, targetPath, action.Name, "action", mappingStatus, mappingNotes, mappingMode))
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

				tagSourceFlowMetadata(result, fn, action)
				appendActionForPhase(result.Script, fn.Phase, *action)
				lastRequestPhase = fn.Phase
				lastRequestIndex = actionCountForPhase(result.Script, fn.Phase) - 1
				trackLastExecutable(fn)
				result.Stats.ConvertedActions++
				result.Stats.RawRequestActions++
				recordSourceFunctionSupport(result, mappingMode)
			} else if isHTMLModeNavigationFunction(fn.Name) {
				// HTML-mode web_link/web_image/web_image_check whose absolute target was
				// resolved from page context at record time (no URL=/Src= in the static
				// source). Preserve honestly as review_only instead of dropping it or
				// inventing a URL. convertAction already appended the "no URL" warning.
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.unresolved_html_navigation", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "review", "review_only", "HTML-mode navigation whose absolute URL is resolved from page context at replay; re-record in URL mode or add an explicit URL/Src to execute it. Preserved for manual review.", SourceFunctionSupportReviewOnly))
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
			}
			result.Warnings = append(result.Warnings, warnings...)

		default:
			if isSourceMetadataOnlyContributor(fn.Name) {
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.source_contributor_functions", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "review", "review_only", "Preserved as source import metadata; it does not create a visible script action.", SourceFunctionSupportReviewOnly))
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
				continue
			}
			if def, ok := lookupSourceFunction(fn.Name); ok && def.Family == "transaction" {
				if nativeSourceTransactionOwnershipUnsupported(fn.Name) {
					notes := "Transaction instance/cross-VUser ownership cannot be reproduced by the native source runtime; preserved for review and not executed."
					action, _, _ := convertSourceFunctionAction(fn)
					gateUnsupportedSourceTransactionOwnership(&action, fn, notes)
					appendTrackedSourceAction(fn, action, SourceFunctionSupportReviewOnly, notes)
					result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d %s requires review: %s", fn.Filename, fn.LineNumber, fn.Name, notes))
					result.Stats.TransactionMarkers++
					continue
				}

				// Every transaction call with native behavior is handled explicitly
				// above. A cataloged transaction that reaches this fallback must not
				// silently disappear into provenance: preserve a visible, durably
				// gated review action instead.
				notes := "No native source-transaction runtime behavior is implemented for this call; preserved for review and not executed."
				action, _, _ := convertSourceFunctionAction(fn)
				if nativeSourceTransactionValueUnsupported(fn.Name) {
					notes = "Native C transaction getters cannot return values to source code, and lr_wasted_time bookkeeping is not implemented; preserved for review and not executed."
					gateUnsupportedNativeSourceTransactionValue(&action, fn, notes)
				} else {
					gateUnsupportedNativeSourceTransactionCall(&action, fn, notes)
				}
				appendTrackedSourceAction(fn, action, SourceFunctionSupportReviewOnly, notes)
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d %s requires review: %s", fn.Filename, fn.LineNumber, fn.Name, notes))
				result.Stats.TransactionMarkers++
				continue
			}
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				appendTrackedSourceAction(fn, action, mode, sourceSupportNotes(fn, mode))
				continue
			}
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			appendSourceReviewMapping(result, sourceReviewMappingForFunction(fn, "metadata.import.preserved.unsupported_functions", fn.Name, "unsupported", "review_only", "No executable APEX Load mapping exists for this source function yet."))
		}
	}

	appendCSourceParseFailureGates(result, actions)
	structureSourceControlFlow(result)
	compactSourceHTTPActions(result)

	// Guarantee globally case-insensitive-unique action names on the final tree
	// (top-level requests + http_sequence wrappers + nested control bodies). The
	// per-branch dedup above is case-sensitive and never sees compaction-created
	// wrapper names, so a repeated transaction name or a case-variant step name
	// would otherwise surface as a false duplicate in the dashboard. Reconcile
	// any rename into the source-review mappings so the import-review table keeps
	// pointing at the right action.
	for _, rename := range uniquify.DeduplicateActionNames(result.Script) {
		reconcileSourceReviewName(result, rename.OldName, rename.NewName)
	}

	if result.Script.Config.ThinkTime.Type == "" && opts.DefaultThinkTimeMs > 0 {
		result.Script.Config.ThinkTime = dsl.ThinkTimeConfig{
			Type:   "fixed",
			MeanMs: opts.DefaultThinkTimeMs,
		}
	}

	for i := range result.Script.Actions {
		applyControlFlowThinkTime(&result.Script.Actions[i])
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
	result.Stats.Extractors -= len(pruneUnusedWholeBodyExtractors(result.Script))
	if !opts.deferArchiveFinalization {
		applyEnterpriseRuntimeBindings(
			result,
			[]*dsl.Script{result.Script},
			archiveFinalizationContext{},
		)
		// Direct Convert callers can supply HTTPConfig/RuntimeSettings without the
		// ZIP orchestration layer. Run the same shared safety pass here so an
		// unsupported version or header shape never escapes as an invalid/runnable
		// script. ConvertZip performs it during finalization with richer runtime
		// and parameter-file provenance.
		applyEnterpriseRuntimeFidelityReview(
			result,
			[]*dsl.Script{result.Script},
			archiveFinalizationContext{},
		)
	}

	return result, nil
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

// enterpriseTemplatePattern mirrors pkg/engine's templatePattern. The engine
// matches {{ ... }} and TrimSpaces the inner expression, so "{{ var.token }}"
// is a live reference that resolves at replay. Detecting references by the bare
// literal "{{var.token}}" would therefore prune an extractor the script really
// uses, silently breaking correlation at import time.
var enterpriseTemplatePattern = regexp.MustCompile(`\{\{([^{}]+)\}\}`)

// referencedVariableNames collects the {{var.NAME}} names referenced anywhere in
// text, applying the engine's whitespace-tolerant parsing.
func referencedVariableNames(text string) map[string]struct{} {
	names := map[string]struct{}{}
	for _, match := range enterpriseTemplatePattern.FindAllStringSubmatch(text, -1) {
		expr := strings.TrimSpace(match[1])
		rest, ok := strings.CutPrefix(expr, "var.")
		if !ok {
			continue
		}
		if name := strings.TrimSpace(rest); name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

// Whole-response captures are expensive and can exceed the engine's bounded
// extraction budget. Importers may emit them defensively even when no later
// executable field consumes the value, so retain only structurally referenced
// captures. Targeted extractors are intentionally unaffected.
func pruneUnusedWholeBodyExtractors(script *dsl.Script) []string {
	if script == nil {
		return nil
	}
	clone := *script
	clone.Init = append([]dsl.Action(nil), script.Init...)
	clone.Actions = append([]dsl.Action(nil), script.Actions...)
	clone.Teardown = append([]dsl.Action(nil), script.Teardown...)
	for _, actions := range [][]dsl.Action{clone.Init, clone.Actions, clone.Teardown} {
		for i := range actions {
			// Keep extractor expressions in the reference text — a later
			// extractor may consume an earlier capture — but blank the Names,
			// which are definitions rather than uses and would otherwise make
			// every extractor look self-referencing.
			stripped := append([]dsl.Extractor(nil), actions[i].Extractors...)
			for j := range stripped {
				stripped[j].Name = ""
			}
			actions[i].Extractors = stripped
		}
	}
	references, err := json.Marshal(&clone)
	if err != nil {
		return nil
	}
	referenced := referencedVariableNames(string(references))
	var removed []string
	prune := func(actions []dsl.Action) {
		for i := range actions {
			kept := actions[i].Extractors[:0]
			for _, extractor := range actions[i].Extractors {
				wholeBody := extractor.Type == "regex" && extractor.Expression == "(?s)^(.*)$"
				_, used := referenced[extractor.Name]
				if wholeBody && !used {
					removed = append(removed, extractor.Name)
					continue
				}
				kept = append(kept, extractor)
			}
			actions[i].Extractors = kept
		}
	}
	prune(script.Init)
	prune(script.Actions)
	prune(script.Teardown)
	return removed
}
