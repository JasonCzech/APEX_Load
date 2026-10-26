package enterprisearchive

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

const trailingRequestContributorsMetadataKey = "trailing_request_contributors"

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
	// JavaSourceTexts holds a Java Vuser's complete .java files keyed by their
	// source manifest path, for the API to keep so the reviewed source can be
	// compiled later. It is excluded from converter JSON.
	JavaSourceTexts map[string]string `json:"-"`

	// flowWarned deduplicates control-flow translation warnings per distinct
	// condition/loop source text during one conversion.
	flowWarned map[string]bool

	// sourceTransactionNames collects transaction names started or published
	// anywhere in the converted source so archive declarations can be checked.
	sourceTransactionNames map[string]struct{}
}

// recordSourceTransactionName remembers a source transaction name for the
// project-level declared-transaction conservation check.
func recordSourceTransactionName(result *ConvertResult, name string) {
	name = strings.ToLower(strings.TrimSpace(name))
	if result == nil || name == "" {
		return
	}
	if result.sourceTransactionNames == nil {
		result.sourceTransactionNames = make(map[string]struct{})
	}
	result.sourceTransactionNames[name] = struct{}{}
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
	sourceEncoding string
	// archiveFiles holds the archive's files by archive-relative name, so
	// request steps can read the files they reference (BodyFilePath).
	archiveFiles         map[string]string
	ScriptName           string  // Override the generated script name
	BaseURL              string  // Override base URL (otherwise auto-detected from first URL)
	DefaultThinkTimeMs   float64 // Default think time if none in script (0 = no default)
	IncludeAssertions    bool    // Add default status_code assertions
	PreserveTransactions bool    // Wrap actions in transaction markers (as comments in name)
	// deferArchiveFinalization is set by ConvertZip so request credential
	// projection runs only after parameter files have been attached and known
	// source parameters have been rewritten to {{param.*}} references.
	deferArchiveFinalization bool
	// runLogicActionNames maps action files to their run-logic action names
	// (see tagRunLogicActions).
	runLogicActionNames map[string]string
	// Render lr_unmask(...) request bodies as governed secret references. Archive
	// imports enable this so payload bytes never leak while operators get a
	// re-provisioning handle; lower-level conversions keep opaque bodies empty.
	UseMaskedBodySecretReferences bool
	// AllowJavaVuserRunner is retained for API compatibility. Java archives now
	// always produce a governed java_vuser draft, so this option is deprecated
	// and has no effect.
	AllowJavaVuserRunner bool
	// BackgroundRequests are the URL path patterns of steps sent as background
	// requests (http_async_start pattern once), outside transaction times: an
	// app's logging and analytics calls that it does not wait for. Nil means
	// recordingprofile.DefaultBackgroundRequests; an empty list keeps every
	// step in line.
	BackgroundRequests *[]string

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
		Warnings:               make([]string, 0),
		sourceTransactionNames: make(map[string]struct{}),
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
		hasCSourceParseFailures = hasCSourceParseFailures || len(a.ParseFailures) > 0 || len(a.vtsReferences) > 0
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

	concurrentPlan := planSourceConcurrentGroups(allFunctions, actions)
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
	// A Base URL override never repoints recorded traffic to another host, so
	// count the requests it cannot apply to and say so instead of staying silent.
	overrideMissedTargets := 0
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
	addedCookies := map[string]ParsedCookie{}
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
	unmodelledRequestArgs := make(map[string]int)
	var pendingFindings []importreview.Finding
	var pendingFilters []*pendingRegistrationFilter
	// pendingAsync is a web_reg_async_attributes waiting for its step;
	// startedAsync holds the conversation IDs that became http_async_start.
	var pendingAsync *sourceAsync
	startedAsync := map[string]bool{}
	// Steps matching a background request pattern, counted by pattern; a step
	// inside web_concurrent_start/end stays in its group.
	backgroundPatterns := (&recordingprofile.Profile{BackgroundRequests: opts.BackgroundRequests}).BackgroundRequestPatterns()
	backgroundCounts := map[string]int{}
	var backgroundOrder []string
	backgroundSteps := 0
	inConcurrentGroup := false
	var webSockets sourceWebSockets
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

	for functionIndex, fn := range allFunctions {
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
		reviewAsync := func(async *sourceAsync, reason string) {
			notes := "The async conversation is preserved and not executed: " + reason + "."
			appendTrackedSourceAction(async.fn, asyncReviewAction(async.fn, notes), SourceFunctionSupportReviewOnly, notes)
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d web_reg_async_attributes requires review: %s", async.fn.Filename, async.fn.LineNumber, reason))
		}
		switch fn.Name {
		case "web_reg_async_attributes":
			if pendingAsync != nil {
				reviewAsync(pendingAsync, "another web_reg_async_attributes replaced it before a step")
			}
			async, reason := parseSourceAsync(fn)
			if async == nil {
				pendingAsync = nil
				reviewAsync(&sourceAsync{fn: fn}, reason)
				continue
			}
			pendingAsync = async

		case "web_stop_async":
			id := sourceStopAsyncID(fn)
			if !startedAsync[id] {
				notes := fmt.Sprintf("No background conversation %q was converted, so there is nothing to stop; preserved and not executed.", id)
				appendTrackedSourceAction(fn, asyncReviewAction(fn, notes), SourceFunctionSupportReviewOnly, notes)
				continue
			}
			appendTrackedSourceAction(fn, dsl.Action{Name: "Stop " + id, Type: "http_async_stop", Params: map[string]any{"id": id}, Metadata: sourceFunctionMetadata(fn)}, SourceFunctionSupportExecutable,
				"Converted to http_async_stop, which ends the background conversation and applies its last responses.")

		case "web_websocket_connect":
			actions, warnings, reason := convertWebSocketConnect(fn, &webSockets, opts.archiveFiles)
			if reason != "" {
				notes := "The WebSocket connection is preserved and not executed: " + reason + "."
				appendTrackedSourceAction(fn, webSocketReviewAction(fn, notes), SourceFunctionSupportReviewOnly, notes)
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d web_websocket_connect requires review: %s", fn.Filename, fn.LineNumber, reason))
				continue
			}
			result.Warnings = append(result.Warnings, warnings...)
			appendTrackedSourceAction(fn, actions[0], SourceFunctionSupportExecutable, "Converted to ws_connect; the socket shares the HTTP session's cookies and TLS settings.")
			if len(actions) > 1 {
				tagSourceFlowMetadata(result, fn, &actions[1])
				appendActionForPhase(result.Script, fn.Phase, actions[1])
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, sourceReviewActionPath(fn.Phase, actionCountForPhase(result.Script, fn.Phase)-1), actions[1].Name, "action", "contributor",
					"OnMessageCB translated to ws_on_message steps run for each received message.", SourceFunctionSupportExecutable))
			}

		case "web_websocket_send", "web_websocket_close":
			var action dsl.Action
			var reason string
			if fn.Name == "web_websocket_send" {
				action, reason = convertWebSocketSend(fn, &webSockets)
			} else {
				action, reason = convertWebSocketClose(fn, &webSockets)
			}
			if reason != "" {
				notes := "Preserved and not executed: " + reason + "."
				appendTrackedSourceAction(fn, webSocketReviewAction(fn, notes), SourceFunctionSupportReviewOnly, notes)
				continue
			}
			appendTrackedSourceAction(fn, action, SourceFunctionSupportExecutable, "Converted to "+action.Type+" on the script's WebSocket.")

		case "web_sync":
			if action, ok := sourceWebSyncAction(fn); ok {
				appendTrackedSourceAction(fn, action, SourceFunctionSupportExecutable, "Converted to http_sync, which waits for a background conversation to set the parameter.")
				continue
			}
			notes := "Only web_sync ParamExists= and ParamValueEquals= (with Timeout=) are converted; preserved and not executed."
			appendTrackedSourceAction(fn, asyncReviewAction(fn, notes), SourceFunctionSupportReviewOnly, notes)

		case "web_reg_save_param", "web_reg_save_param_regexp", "web_reg_save_param_json", "web_reg_save_param_xpath", "web_reg_save_param_ex", "web_reg_save_param_attrib":
			registration := convertRegistration(fn)
			ext := registration.extractor
			result.Warnings = append(result.Warnings, registration.warnings...)
			if ext != nil {
				pendingFindings = append(pendingFindings, registration.findings...)
				if registration.filter != nil {
					pendingFilters = append(pendingFilters, registration.filter)
				}

				pendingExtractors = append(pendingExtractors, *ext)
				pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", ext.Name, "extractor", "contributor", "Applied to the next converted request action.", SourceFunctionSupportExecutable))
				result.Stats.Extractors++
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			}

		case "web_reg_find":
			assertion, saveCountExtractor, supportMode, warning, findFindings := convertFindAssertionWithFindings(fn)
			pendingFindings = append(pendingFindings, findFindings...)
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
			assertion, saveCountExtractor, supportMode, warning, findFindings := convertFindAssertionWithFindings(fn)
			if assertion != nil {
				assertion.LegacyHTMLCheck = true
			}
			if len(findFindings) > 0 {
				if prev := actionAtPhaseIndex(result.Script, lastRequestPhase, lastRequestIndex); prev != nil {
					importreview.ApplyToAction(prev, findFindings)
				} else {
					pendingFindings = append(pendingFindings, findFindings...)
				}
			}
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

		case "web_global_verification", "web_global_verification_pause", "web_global_verification_resume":
			for _, a := range convertSourceVerifications(fn) {
				appendTrackedSourceAction(fn, a, SourceFunctionSupportExecutable, "Positional per-VUser HTTP verification.")
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

		case "web_add_cookie", "web_add_cookie_ex":
			// The cookie goes into the VUser's cookie jar at this point, as
			// LoadRunner adds it to its cookie cache: a later Set-Cookie from the
			// server replaces it instead of both being sent.
			for _, cookie := range fn.Cookies {
				if cookie.Name == "" {
					continue
				}
				appendTrackedSourceAction(fn, cookieSetAction(fn, cookie, false), SourceFunctionSupportExecutable, "Converted to a positional cookie_set action on the VUser's cookie jar.")
				addedCookies[strings.ToLower(cookie.Name)] = cookie
			}

		case "web_reg_add_cookie":
			// Registered (conditional) cookies keep their downstream Cookie-header
			// approximation.
			for _, cookie := range fn.Cookies {
				if cookie.Name == "" {
					continue
				}
				cookie.Value = convertParamReferences(cookie.Value)
				persistentCookies = upsertPersistentCookie(persistentCookies, cookie)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers.Cookie", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "headers", "converted", "Mapped to Cookie headers on matching downstream HTTP requests.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "web_remove_cookie":
			// Drop a previously-added cookie (or ALL) from the persistent set so it
			// stops attaching to downstream requests.
			removed := removePersistentCookie(&persistentCookies, fn)
			if cookie, ok := sourceCookieRemoval(fn); ok {
				// LoadRunner removes the cookie from its cookie cache, so it leaves
				// the VUser's jar whether web_add_cookie or the server set it.
				notes := "Converted to a positional cookie_set action that expires the cookie in the VUser's cookie jar."
				if added, found := addedCookies[strings.ToLower(cookie.Name)]; found {
					cookie = added
					delete(addedCookies, strings.ToLower(cookie.Name))
					notes = "Converted to a positional cookie_set action that expires the cookie added earlier."
				}
				appendTrackedSourceAction(fn, cookieSetAction(fn, cookie, true), SourceFunctionSupportExecutable, notes)
				break
			}
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
				tagSourceConcurrentTransaction(result, fn, functionIndex, concurrentPlan)
			}
			recordSourceTransactionName(result, convertutil.FirstNonEmpty(sourceControlArgAt(fn, 0), fn.StepName))
			currentTransaction = fn.StepName
			currentSubTransaction = ""
			result.Stats.Transactions++
			result.Stats.TransactionMarkers++

		case "lr_end_transaction":
			if action, mode, ok := convertSourceFunctionAction(fn); ok {
				action = labelSourceTransactionMarker(action, fn, "End")
				appendTrackedSourceAction(fn, action, mode, "Ended the executable source transaction with the source PASS/FAIL status.")
				tagSourceConcurrentTransaction(result, fn, functionIndex, concurrentPlan)
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
				recordSourceTransactionName(result, convertutil.FirstNonEmpty(sourceControlArgAt(fn, 0), fn.StepName))
				result.Stats.Transactions++
			}

		case "lr_think_time", "lr_force_think_time":
			// lr_force_think_time waits whatever the think-time settings say,
			// so it always keeps its own action with a forced delay.
			force := fn.Name == "lr_force_think_time"
			sec, ok := ExtractThinkTimeSeconds(fn)
			prev := actionAtPhaseIndex(result.Script, lastExecutablePhase, lastExecutableIndex)
			adjacent := prev != nil &&
				strings.EqualFold(strings.TrimSpace(fn.Phase), strings.TrimSpace(lastExecutablePhase)) &&
				len(result.Script.Init)+len(result.Script.Actions)+len(result.Script.Teardown) == lastExecutableActionCount &&
				stringSlicesEqual(fn.ConditionIDs, lastExecutableConditionIDs) &&
				stringSlicesEqual(fn.LoopIDs, lastExecutableLoopIDs)
			waitMs := sec * 1000
			// Preserve each call independently even when percentage mode or a cap
			// is selected only after import. A single adjacent wait can stay on its
			// predecessor; leading and consecutive waits need their own markers.
			if ok && sec >= 0 && !math.IsNaN(waitMs) && !math.IsInf(waitMs, 0) && waitMs <= dsl.MaxConfiguredProtocolTimeoutMS &&
				(force || !adjacent || prev.ThinkTimeAfter || (opts.RuntimeSettings != nil && opts.RuntimeSettings.ThinkTime.Type == "random_percentage")) {
				action, mode, _ := convertSourceFunctionAction(fn)
				action.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: waitMs, Force: force}
				action.ThinkTimeAfter = true
				appendTrackedSourceAction(fn, action, mode, "Preserved this recorded delay separately so percentage sampling and caps remain independent after runtime settings change.")
				result.Stats.ThinkTimes++
				break
			}
			if ok && sec >= 0 && !math.IsNaN(waitMs) && !math.IsInf(waitMs, 0) &&
				waitMs <= dsl.MaxConfiguredProtocolTimeoutMS && adjacent {
				prev.ThinkTime = dsl.ThinkTimeConfig{Type: "fixed", MeanMs: waitMs}
				prev.ThinkTimeAfter = true
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, sourceReviewActionPath(lastExecutablePhase, lastExecutableIndex)+".think_time", "Think Time", "config", "converted", "Preserved as the preceding executable action's exact recorded wait.", SourceFunctionSupportExecutable))
				result.Stats.ThinkTimes++
				result.Stats.ContributorFunctions++
				recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
			} else {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d %s could not be placed as an exact wait; using zero delay and requiring review", fn.Filename, fn.LineNumber, fn.Name))
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.unplaced_think_times", "Think Time", "config", "review_only", "Dynamic, malformed, or unplaceable think time; zero-delay fallback requires review.", SourceFunctionSupportReviewOnly))
				recordSourceFunctionSupport(result, SourceFunctionSupportReviewOnly)
			}

		case "lr_continue_on_error":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			applySourceContinueOnError(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.error_handling.continue_on_error", "Continue On Error", "config", "converted", "Mapped from source runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

		case "fprintf":
			// Generators do not write files on the load generator; the line
			// goes to the run's output messages, as lr_output_message would.
			action := convertSourceFileOutputAction(fn)
			appendTrackedSourceAction(fn, action, SourceFunctionSupportExecutable, "A line written to a load-generator file is replayed as an output message tagged with its stream.")
			noteSourceFileOutput(result)

		case "fopen", "fclose", "fflush":
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "", fn.Name, "config", "converted", "Load-generator file handles are not opened; fprintf lines go to output messages instead.", SourceFunctionSupportNativeNoop))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportNativeNoop)
			noteSourceFileOutput(result)

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
			clear(addedCookies)
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
			if warning := stringMapValue(details, "migration_warning", ""); warning != "" {
				mappingNotes = warning
				result.Warnings = append(result.Warnings, warning)
			}
			if partial, _ := details["mapping_partial"].(bool); partial {
				supportMode = SourceFunctionSupportReviewOnly
				mappingStatus = "review_only"
				mappingNotes = stringMapValue(details, "review_reason", "Unsupported source socket option; APEX defaults remain unchanged and review is required.")
				result.Warnings = append(result.Warnings, mappingNotes)
				if strings.EqualFold(stringMapValue(details, "option", ""), "SSL_VERSION") || strings.EqualFold(stringMapValue(details, "option", ""), "TLS_VERSION") {
					gate := dsl.Action{Name: "Select_supported_TLS", Type: "source_review", Metadata: sourceFunctionMetadata(fn)}
					// The gate is the actionable form of the warning above, so
					// a review that lists both can show it once. It is plain
					// metadata: the review-only rewrite keeps it with the gate's
					// codes, and clearing those codes leaves it in place.
					gate.Metadata["covered_warnings"] = []string{mappingNotes}
					importreview.ApplyToAction(&gate, []importreview.Finding{{Code: "legacy_tls_replacement", ReasonCode: "legacy_tls_replacement", Severity: diag.Blocker, Gate: importreview.GateExecution, Source: "enterprise_script_archive", SourceRef: fmt.Sprintf("%s:%d", fn.Filename, fn.LineNumber), Resolution: "select_tls", Detail: "Choose a supported TLS replacement for legacy SSL; this is an intentional migration."}})
					appendTrackedSourceAction(fn, gate, SourceFunctionSupportReviewOnly, "Legacy TLS requires intentional replacement.")
					result.Stats.ReviewOnlyFunctions--
				}

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
			if conflict, _ := details["conflict"].(bool); conflict {
				// APEX keeps one script-level credential set; a second set for
				// another host cannot be expressed yet, so execution is gated
				// rather than sending the wrong credentials.
				appendTrackedSourceAction(fn, webSetUserConflictGate(fn, stringMapValue(details, "host", "")), SourceFunctionSupportReviewOnly,
					"A second web_set_user credential set cannot be expressed; execution is gated.")
				break
			}
			if unscoped, _ := details["host_unscoped"].(bool); unscoped {
				result.Warnings = append(result.Warnings, fmt.Sprintf("%s:%d web_set_user host %q is parameterized or not a plain host, so its credentials apply to every host", fn.Filename, fn.LineNumber, stringMapValue(details, "host", "")))
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.http.auth", convertutil.FirstNonEmpty(stringMapValue(details, "username", ""), fn.Name), "config", "converted", "Mapped from source web_set_user authentication, scoped to its host.", SourceFunctionSupportExecutable))
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
			inConcurrentGroup = fn.Name == "web_concurrent_start"
			appendSourceConcurrentMarker(result, fn, functionIndex, concurrentPlan)

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
			var action *dsl.Action
			var warnings []string
			if isHTMLStepFunction(fn) {
				action, warnings = convertHTMLStepAction(fn, currentTransaction, currentSubTransaction, opts)
			} else {
				action, warnings = convertAction(fn, baseURL, currentTransaction, currentSubTransaction, opts, lastRequestURL)
			}
			argumentGated := false
			if action != nil {
				var unknownArgs []string
				unknownArgs, argumentGated = applyRequestArgumentReview(action, fn, opts)
				for _, name := range unknownArgs {
					unmodelledRequestArgs[name]++
				}
			}
			if opts.BaseURL != "" && action != nil && (strings.HasPrefix(action.Target, "http://") || strings.HasPrefix(action.Target, "https://")) {
				overrideMissedTargets++
			}
			if contextURL := sourceRequestContextURL(fn, lastRequestURL); contextURL != "" && fn.Name != "web_image_check" && !isHTMLStepFunction(fn) {
				lastRequestURL = contextURL
			}
			if action != nil {
				if fn.Name == "web_image_check" {
					if action.Params == nil {
						action.Params = map[string]any{}
					}
					action.Params["legacy_html_check"] = true
				}
				// Capture pre-attach counts so contributor mappings can point at the
				// exact extractor/assertion index they land on in the generated action.
				baseExtractorCount := len(action.Extractors)
				baseAssertionCount := len(action.Assertions)

				if len(pendingAssertions) > 0 && fn.Name != "web_image_check" {
					action.Assertions = append(action.Assertions, pendingAssertions...)
					pendingAssertions = nil
				}

				// Attach pending extractors
				if len(pendingExtractors) > 0 && fn.Name != "web_image_check" {
					action.Extractors = append(action.Extractors, pendingExtractors...)
					pendingExtractors = nil
				}
				// Review findings and search filters of the registrations that
				// apply to this request.
				if fn.Name != "web_image_check" {
					for _, filter := range pendingFilters {
						pendingFindings = append(pendingFindings, filter.findingsForAttachedRequest(action, firstNonEmptyArg(fn, "RecContentType"))...)
					}
					pendingFilters = nil
					if len(pendingFindings) > 0 {
						importreview.ApplyToAction(action, pendingFindings)
						pendingFindings = nil
					}
				}

				// Attach pending headers
				if len(pendingHeaders) > 0 && fn.Name != "web_image_check" {
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
				applyDefaultCustomRequestContentType(action, fn)

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

				stepGated := argumentGated || metadataBool(action.Metadata, "masked_body") || metadataString(action.Metadata, "source_itemdata_encoding") == "review_required"
				var asyncWrap, asyncResource *sourceAsync
				var asyncResourceRequest dsl.Action
				if pendingAsync != nil && fn.Name != "web_image_check" {
					async := pendingAsync
					pendingAsync = nil
					appliesToStep := async.appliesTo(fn, action)
					var resourceRequest dsl.Action
					resource := false
					if !appliesToStep {
						resourceRequest, resource = async.resourceConversation(action, baseURL, autoHeaders)
					}
					switch {
					case resource:
						// The conversation is the step's resource, which runs
						// apart from the step whether or not the step is gated.
						asyncResource, asyncResourceRequest = async, resourceRequest
					case stepGated:
						reviewAsync(async, "the step it applies to is gated for review")
					case !appliesToStep:
						reviewAsync(async, "its URL names a request inside the step, which cannot run apart from the step")
					default:
						asyncWrap = async
					}
				}
				// A step the app did not wait for (its logging or analytics)
				// becomes a background request, unless it is gated, in a
				// concurrent group, or extracts a value later steps may need.
				background := ""
				if asyncWrap == nil && !stepGated && !inConcurrentGroup && len(action.Extractors) == 0 && fn.Name != "web_image_check" && !isHTMLStepFunction(fn) {
					originalURL, _ := action.Metadata["original_url"].(string)
					background = recordingprofile.MatchBackgroundRequest(backgroundPatterns, convertutil.FirstNonEmpty(originalURL, action.Target))
				}

				actionIndex := actionCountForPhase(result.Script, fn.Phase)
				targetPath := sourceReviewActionPath(fn.Phase, actionIndex)
				contributorPath := targetPath
				if asyncWrap != nil || background != "" {
					contributorPath = targetPath + ".params.request"
				}
				mappingStatus := "converted"
				mappingNotes := "Converted to a generated APEX Load action."
				if background != "" {
					mappingNotes = fmt.Sprintf("Converted to a background request (http_async_start pattern once), sent without waiting and outside transaction times: its URL path matches background request pattern %q.", background)
				}
				mappingMode := SourceFunctionSupportExecutable
				if argumentGated {
					mappingStatus = "review_only"
					mappingNotes = "The step uses request attributes the importer cannot reproduce yet; execution is gated until the request is rebuilt (see the action's import review)."
					mappingMode = SourceFunctionSupportReviewOnly
				} else if metadataBool(action.Metadata, "masked_body") {
					mappingStatus = "review_only"
					mappingNotes = fmt.Sprintf("Request argument is produced by %s(...); APEX has no verified decoder for this payload; supply a restored body through protected import repair — the action requires manual review.", metadataString(action.Metadata, "masked_body_function"))
					mappingMode = SourceFunctionSupportReviewOnly
				} else if metadataString(action.Metadata, "source_itemdata_encoding") == "review_required" {
					mappingStatus = "review_only"
					mappingNotes = "ITEMDATA uses an encoding or field shape that cannot be serialized faithfully; the source is preserved and execution is blocked until the request body is rebuilt."
					mappingMode = SourceFunctionSupportReviewOnly
				}
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, targetPath, action.Name, "action", mappingStatus, mappingNotes, mappingMode))
				// Contributors (extractors, assertions, headers) were collected in source
				// order ahead of this request, so the Nth extractor/assertion mapping lines
				// up with the Nth attached extractor/assertion.
				extractorIndex := baseExtractorCount
				assertionIndex := baseAssertionCount
				retainedMappings := make([]SourceReviewMapping, 0)
				for _, pendingMapping := range pendingSourceMappings {
					if fn.Name == "web_image_check" {
						retainedMappings = append(retainedMappings, pendingMapping)
						continue
					}
					contributorIndex := -1
					switch pendingMapping.TargetKind {
					case "extractor":
						contributorIndex = extractorIndex
						extractorIndex++
					case "assertion":
						contributorIndex = assertionIndex
						assertionIndex++
					}
					pendingMapping.TargetPath = sourceReviewContributorTargetPath(contributorPath, pendingMapping.TargetKind, contributorIndex)
					if pendingMapping.TargetName == "" {
						pendingMapping.TargetName = action.Name
					}
					appendSourceReviewMapping(result, pendingMapping)
				}
				pendingSourceMappings = retainedMappings

				tagSourceFlowMetadata(result, fn, action)
				if asyncWrap != nil {
					callbacks := translateAsyncCallbacks(asyncWrap, opts.archiveFiles)
					wrapped, err := wrapAsyncConversation(asyncWrap, *action, callbacks)
					if err != nil {
						return nil, fmt.Errorf("wrap async conversation %q: %w", asyncWrap.id, err)
					}
					result.Warnings = append(result.Warnings, droppedCallbackLogsWarnings(asyncWrap.fn, callbacks)...)
					*action = wrapped
					startedAsync[asyncWrap.id] = true
					appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(asyncWrap.fn, targetPath, action.Name, "action", "contributor",
						"Runs this step as a background conversation (http_async_start) until web_stop_async or the end of the iteration.", SourceFunctionSupportExecutable))
					result.Stats.ContributorFunctions++
					recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
				}
				if background != "" {
					backgroundSteps++
					wrapped, err := wrapBackgroundRequest(fmt.Sprintf("background_%d", backgroundSteps), background, fn, *action)
					if err != nil {
						return nil, fmt.Errorf("wrap background request %q: %w", action.Name, err)
					}
					*action = wrapped
					if backgroundCounts[background] == 0 {
						backgroundOrder = append(backgroundOrder, background)
					}
					backgroundCounts[background]++
				}
				appendActionForPhase(result.Script, fn.Phase, *action)
				if fn.Name != "web_image_check" {
					lastRequestPhase = fn.Phase
					lastRequestIndex = actionCountForPhase(result.Script, fn.Phase) - 1
				}
				if asyncResource != nil {
					callbacks := translateAsyncCallbacks(asyncResource, opts.archiveFiles)
					start, err := wrapAsyncConversation(asyncResource, asyncResourceRequest, callbacks)
					if err != nil {
						return nil, fmt.Errorf("wrap async conversation %q: %w", asyncResource.id, err)
					}
					result.Warnings = append(result.Warnings, droppedCallbackLogsWarnings(asyncResource.fn, callbacks)...)
					startedAsync[asyncResource.id] = true
					appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(asyncResource.fn, sourceReviewActionPath(fn.Phase, actionCountForPhase(result.Script, fn.Phase)), start.Name, "action", "converted",
						"Runs the step's resource it names as a background conversation (http_async_start) until web_stop_async or the end of the iteration; the step no longer fetches it as a resource.", SourceFunctionSupportExecutable))
					appendActionForPhase(result.Script, fn.Phase, start)
					result.Stats.ConvertedActions++
					recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)
				}
				trackLastExecutable(fn)
				result.Stats.ConvertedActions++
				result.Stats.RawRequestActions++
				recordSourceFunctionSupport(result, mappingMode)
			} else if isHTMLModeNavigationFunction(fn.Name) {
				// HTML-mode web_link/web_image/web_image_check whose target VuGen
				// resolves from the previous page at replay. Dropping the click would
				// replay a flow the server never sees, and inventing a URL would be
				// wrong, so an execution-gated review action holds its position.
				// convertAction already appended the "no URL" warning.
				appendTrackedSourceAction(fn, htmlNavigationGateAction(fn), SourceFunctionSupportReviewOnly,
					"HTML-mode click resolved from the previous page at replay; execution is gated until it is replaced with an explicit request or the script is re-recorded in URL mode.")
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
				if fn.Name == "web_save_param_length" && action.Type != "source_review" && strings.EqualFold(opts.sourceEncoding, "windows-1252") {
					// LoadRunner counts the value's code-page bytes.
					if action.Params == nil {
						action.Params = map[string]any{}
					}
					action.Params["code_page"] = "windows-1252"
				}
				appendTrackedSourceAction(fn, action, mode, sourceSupportNotes(fn, mode))
				continue
			}
			result.Stats.Skipped++
			result.Stats.UnsupportedFuncs = append(result.Stats.UnsupportedFuncs, fn.Name)
			appendSourceReviewMapping(result, sourceReviewMappingForFunction(fn, "metadata.import.preserved.unsupported_functions", fn.Name, "unsupported", "review_only", "No executable APEX Load mapping exists for this source function yet."))
		}
	}
	for _, pattern := range backgroundOrder {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d step(s) matching background request pattern %q are sent as background requests, outside transaction times", backgroundCounts[pattern], pattern))
	}
	if pendingAsync != nil {
		notes := "The async conversation is preserved and not executed: no step follows it."
		appendTrackedSourceAction(pendingAsync.fn, asyncReviewAction(pendingAsync.fn, notes), SourceFunctionSupportReviewOnly, notes)
	}
	if warning := unmodelledRequestArgumentsWarning(unmodelledRequestArgs); warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}

	preserveTrailingRequestContributors(
		result,
		pendingExtractors,
		pendingAssertions,
		pendingHeaders,
		pendingSourceMappings,
	)

	appendCSourceParseFailureGates(result, actions)
	appendVTSSourceGates(result, actions, nil)
	if !opts.deferArchiveFinalization {
		appendSplicedCommentWarnings(result, actions) // ConvertZip reports them for every protocol
	}
	structureSourceConcurrentGroups(result, concurrentPlan)
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
	reconcileSourceConcurrentMarkerNames(result)

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
	tagRunLogicActions(result.Script, opts.runLogicActionNames)
	for _, note := range pruneEmptyRunLogicChildren(result.Script) {
		result.Warnings = append(result.Warnings, "Run logic: "+note+".")
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
	if overrideMissedTargets > 0 {
		result.Warnings = append(result.Warnings, fmt.Sprintf("%d recorded request(s) use absolute URLs on another host; the Base URL override applies only to relative targets, so these still go to the recorded host. Edit their targets to repoint them.", overrideMissedTargets))
	}

	return result, nil
}

func preserveTrailingRequestContributors(
	result *ConvertResult,
	extractors []dsl.Extractor,
	assertions []dsl.Assertion,
	headers map[string]string,
	mappings []SourceReviewMapping,
) {
	if result == nil || len(extractors)+len(assertions)+len(headers) == 0 {
		return
	}
	preserved := ensureEnterpriseArchiveImportPreserved(result.Script)
	preserved[trailingRequestContributorsMetadataKey] = map[string]int{
		"extractors": len(extractors),
		"assertions": len(assertions),
		"headers":    len(headers),
	}
	result.Warnings = append(result.Warnings,
		"source response contributors or one-shot headers appear after the final request; they were preserved for review and block promotion because there is no response/request to bind them to")

	if result.Stats.Extractors >= len(extractors) {
		result.Stats.Extractors -= len(extractors)
	}
	seenFunctions := make(map[string]struct{}, len(mappings))
	for _, mapping := range mappings {
		originalSupportMode := SourceFunctionSupportMode(mapping.SupportMode)
		mapping.TargetPath = "metadata.import.preserved." + trailingRequestContributorsMetadataKey
		mapping.Status = "review_only"
		mapping.SupportMode = string(SourceFunctionSupportReviewOnly)
		mapping.Notes = "Preserved for review: this contributor appeared after the final request and cannot be bound without changing source semantics."
		appendSourceReviewMapping(result, mapping)

		key := strings.ToLower(strings.TrimSpace(mapping.SourceFile)) + ":" + strconv.Itoa(mapping.StartLine) + ":" + strings.ToLower(strings.TrimSpace(mapping.Function))
		if _, seen := seenFunctions[key]; seen {
			continue
		}
		seenFunctions[key] = struct{}{}
		moveSourceFunctionSupportToReview(&result.Stats, originalSupportMode)
	}
}

func moveSourceFunctionSupportToReview(stats *ConvertStats, original SourceFunctionSupportMode) {
	if stats == nil || original == SourceFunctionSupportReviewOnly {
		return
	}
	switch original {
	case SourceFunctionSupportExecutable, SourceFunctionSupportNativeNoop:
		if stats.NativeFunctions == 0 {
			return
		}
		stats.NativeFunctions--
	case SourceFunctionSupportExternalRequired:
		if stats.ExternalRequiredFunctions == 0 {
			return
		}
		stats.ExternalRequiredFunctions--
	case SourceFunctionSupportCustomReview:
		if stats.CustomCallFunctions == 0 {
			return
		}
		stats.CustomCallFunctions--
	default:
		return
	}
	stats.ReviewOnlyFunctions++
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
