package enterprisearchive

import (
	"fmt"
	"math"
	"regexp"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/uniquify"
	"github.com/jasonczech/apexload/pkg/dsl"
)

// ConvertResult holds the output of an enterprise script archive to APEX Load DSL conversion.
type ConvertResult struct {
	Script                       *dsl.Script           `json:"script"`
	Warnings                     []string              `json:"warnings,omitempty"`
	Stats                        ConvertStats          `json:"stats"`
	DetectedProtocol             *DetectedProtocol     `json:"detected_protocol,omitempty"`
	FidelityReport               *ImportFidelityReport `json:"fidelity_report,omitempty"`
	ReviewArtifact               *ReviewArtifact       `json:"review_artifact,omitempty"`
	SourceReview                 *SourceReview         `json:"source_review,omitempty"`
	RecordedResourceURLs         []string              `json:"recorded_resource_urls,omitempty"`
	RecordedResourceURLsCaptured bool                  `json:"recorded_resource_urls_captured,omitempty"`

	// flowWarned deduplicates control-flow translation warnings per distinct
	// condition/loop source text during one conversion.
	flowWarned map[string]bool
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
	// Render lr_unmask(...) request bodies as governed secret references. Archive
	// imports enable this so payload bytes never leak while operators get a
	// re-provisioning handle; lower-level conversions keep opaque bodies empty.
	UseMaskedBodySecretReferences bool
	// AllowJavaVuserRunner emits a governed java_vuser sidecar-runner draft for
	// unmappable Java archives. It is intentionally opt-in because Java replay
	// executes arbitrary source code inside a generator-java runner boundary.
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

		case "web_revert_auto_header":
			headerName := firstSourceControlArg(fn)
			if headerName != "" && autoHeaders != nil {
				deleteHeaderCaseInsensitive(autoHeaders, headerName)
			}
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers", convertutil.FirstNonEmpty(headerName, fn.Name), "headers", "converted", "Reverted a persistent source auto header.", SourceFunctionSupportExecutable))
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
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "actions[].headers.Cookie", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "headers", "converted", "Mapped to Cookie headers on matching downstream HTTP requests.", SourceFunctionSupportExecutable))
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
			pendingSourceMappings = append(pendingSourceMappings, sourceReviewMappingWithSupportMode(fn, "", convertutil.FirstNonEmpty(fn.StepName, currentTransaction), "transaction", "contributor", "Applied as a source subtransaction on generated child requests.", SourceFunctionSupportExecutable))
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
			// APEX Load's HTTP runtime keeps no response cache, so a cache-cleanup
			// control is a guaranteed native no-op (every fetch is already fresh).
			// Preserved as a compatibility attribute for round-trip fidelity but
			// classified native so it never counts as a runtime-parity gap.
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			setSourceWebRuntimeAttribute(result.Script, "web_cache_cleanup", "true")
			preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, map[string]any{
				"mapped_to": "config.web_runtime.additional_attributes.web_cache_cleanup",
			})
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "config.web_runtime.additional_attributes.web_cache_cleanup", "Web Cache Cleanup", "config", "converted", "APEX Load keeps no HTTP response cache; cache cleanup is a native no-op.", SourceFunctionSupportNativeNoop))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportNativeNoop)

		case "web_cleanup_cookies":
			// Cookie cleanup is behavioral: emit a positional, executable
			// cookie_clear action so the clear happens exactly where the source
			// called it, instead of flattening it into a non-executed attribute.
			action := convertWebCleanupCookiesAction(fn)
			preserveSourceRuntimeControl(result.Script, "source_runtime_controls", fn, map[string]any{
				"mapped_to": "actions[].cookie_clear",
			})
			appendSourceAction(result, fn, action, SourceFunctionSupportExecutable, "Converted to an executable cookie_clear action.")

		case "web_set_sockets_option":
			sourceRuntimeControls = append(sourceRuntimeControls, fn)
			details := applySourceSocketOption(result, fn)
			appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, stringMapValue(details, "mapped_to", "config.web_runtime.additional_attributes.web_set_sockets_option"), convertutil.FirstNonEmpty(stringMapValue(details, "option", ""), "Socket Option"), "config", "converted", "Mapped from source runtime control.", SourceFunctionSupportExecutable))
			result.Stats.ContributorFunctions++
			recordSourceFunctionSupport(result, SourceFunctionSupportExecutable)

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

		case "web_convert_param":
			action := convertWebConvertParamAction(fn)
			appendSourceAction(result, fn, action, SourceFunctionSupportExecutable, "Converted to an executable source parameter transform.")

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
				result.Stats.ConvertedActions++
				result.Stats.RawRequestActions++
				recordSourceFunctionSupport(result, mappingMode)
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
				appendSourceReviewMapping(result, sourceReviewMappingWithSupportMode(fn, "metadata.import.preserved.source_transaction_groups", convertutil.FirstNonEmpty(fn.StepName, fn.Name), "transaction", "contributor", "Preserved as a source transaction marker; it does not create a visible script action.", def.SupportMode))
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

	return result, nil
}
