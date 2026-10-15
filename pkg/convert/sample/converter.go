package sample

import (
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/convertresult"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/recordedshape"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
	"github.com/jasonczech/apexload/pkg/recordingprofile"
)

type ImportRequest struct {
	Name            string         `json:"name,omitempty"`
	ProjectID       string         `json:"project_id,omitempty"`
	Format          string         `json:"format"`
	Content         string         `json:"content"`
	ResponseContent string         `json:"response_content,omitempty"`
	Options         ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	BaseURL             string `json:"base_url,omitempty"`
	IncludeAssertions   *bool  `json:"include_assertions,omitempty"`
	InferExtractors     *bool  `json:"infer_extractors,omitempty"`
	SanitizeSensitive   *bool  `json:"sanitize_sensitive,omitempty"`
	IncludeStaticAssets *bool  `json:"include_static_assets,omitempty"`
	// PageTitleChecks adds a body_contains check on each recorded HTML page's
	// <title>, as VuGen does by default. Nil means on.
	PageTitleChecks *bool `json:"page_title_checks,omitempty"`
	// ResourceMode mirrors VuGen's resource recording choices for HAR input:
	// "runtime" (default) drops static resources for discovery at replay,
	// "in_step" attaches them to the page that requested them as explicit
	// resources, and "separate" keeps them as their own steps.
	ResourceMode string `json:"resource_mode,omitempty"`
	// HTMLSteps turns the requests of the given 1-based HAR entries into
	// HTML-mode steps (params.html_step) that resolve against the replayed
	// page. The browser recorder supplies them for an HTML-level HTTP script.
	HTMLSteps map[int]map[string]any `json:"-"`
	// Transactions (HAR input) tags each request with the transaction open
	// when it started (metadata.source_transaction). The browser recorder
	// supplies the transactions marked while recording.
	Transactions []TransactionWindow `json:"-"`
	// AsyncScan (HAR input; default on) collapses requests a page repeated in
	// the background (poll, long-poll, push) into background conversations.
	// Off keeps them as recorded steps.
	AsyncScan *bool `json:"async_scan,omitempty"`
	// Profile is the project recording profile (HAR input): its host and
	// content-type filters, header drop list, think-time threshold and
	// correlation settings apply.
	Profile *recordingprofile.Profile `json:"-"`
	// OmitSourceReview skips the source review, which embeds a scrubbed copy
	// of the input. The proxy recorder sets it: it keeps the recording itself.
	OmitSourceReview bool `json:"-"`
	// HighConfidenceOnly rewrites only high-confidence correlations
	// (correlate.HighConfidence); the rest stay review candidates. The proxy
	// recorder sets it.
	HighConfidenceOnly bool `json:"-"`
	// HTTPVersionMode (HAR input) maps captured HTTP versions to leaf
	// policies: "upgrade" (default; captured h3 becomes auto+h3), "exact"
	// (captured h3 becomes 3) or "inherit" (no leaf policy). Captured HTTP/2
	// and HTTP/1.1 stay exact except under inherit.
	HTTPVersionMode string `json:"http_version_mode,omitempty"`
}

type ConvertStats struct {
	Format               string `json:"format"`
	TotalRequests        int    `json:"total_requests"`
	ConvertedActions     int    `json:"converted_actions"`
	FilteredAssets       int    `json:"filtered_assets,omitempty"`
	SkippedRequests      int    `json:"skipped_requests,omitempty"`
	Assertions           int    `json:"assertions"`
	Extractors           int    `json:"extractors"`
	CorrelationsDetected int    `json:"correlations_detected"`
}

type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ExtractType string `json:"extract_type,omitempty"`
	Convert     string `json:"convert,omitempty"`
	Value       string `json:"-"`
	SampleValue string `json:"sample_value"`
	FoundInStep int    `json:"found_in_step"`
	UsedInSteps []int  `json:"used_in_steps"`
	// PreApplied is true only when conversion attached an executable extractor
	// and rewrote at least one supported consumer. Correlations whose only reuse
	// has an encoding the DSL cannot reproduce remain review candidates instead
	// of being mislabeled as already applied.
	PreApplied   bool   `json:"pre_applied,omitempty"`
	ReviewAction string `json:"review_action,omitempty"`

	// FoundInEntry and UsedInEntries are the 1-based HAR entries of
	// FoundInStep and UsedInSteps, and RuleID, Confidence and Reasons the
	// detector's ranking. They are for adapters (the proxy recorder maps
	// entries back to its request sequence) and are not serialized.
	FoundInEntry  int      `json:"-"`
	UsedInEntries []int    `json:"-"`
	RuleID        string   `json:"-"`
	Confidence    float64  `json:"-"`
	Reasons       []string `json:"-"`

	// reviewOnly keeps a correlation out of the automatic rewrite (see
	// ConvertOptions.HighConfidenceOnly) while it still takes part in
	// collision checks.
	reviewOnly bool
	// missDefault is the recorded value the extractor's variable replaced
	// (see correlationMissPolicy).
	missDefault string
}

type ConvertResult struct {
	Script       *dsl.Script        `json:"script"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	Correlations []Correlation      `json:"correlations,omitempty"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`

	// ProtectedRequirements is a one-shot adapter-to-API handoff for source
	// values needed to bind the generated {{secret.*}} references. It is never
	// serialized with the conversion result.
	ProtectedRequirements credentialpolicy.ProtectedRequirements `json:"-"`

	// Baseline holds the raw recorded observation of each converted request
	// (HAR input), with ID the index of its action in the final script.
	// Requests folded into a background conversation are left out. It holds
	// source values and is never serialized.
	Baseline []correlate.Observation `json:"-"`
}

type exchange struct {
	SourceEntryID int
	Method        string
	URL           string
	HTTPVersion   string
	Headers       map[string]string
	HeaderList    dsl.HeaderList
	Body          string
	BodyEncoding  string
	FileAssets    []dsl.DataSource
	Metadata      map[string]any
	Response      *response
	StartedAt     time.Time
	HasStartedAt  bool
	DurationMs    float64
	TimeoutMs     int
	// ReviewFindings attach to the action generated from this exchange (for
	// example the integrated-auth gate from normalizeRecordedExchanges).
	ReviewFindings []importreview.Finding
	// SourceSecrets retains source-only values that do not survive in a
	// projected request surface (for example cURL -u input or arguments of an
	// unsupported option). It is consumed only by source-review redaction.
	SourceSecrets []string
	// StepName names the generated action (the recorder's step name), and
	// RecordedThinkTimeMs is the pause the recorder measured, or the tester
	// edited, before this request (HasRecordedThinkTime).
	StepName             string
	RecordedThinkTimeMs  int64
	HasRecordedThinkTime bool
	// CacheValidatorsDropped marks a captured request whose browser cache
	// validators were removed (see dropHARCacheValidators): replay asks for
	// the full response.
	CacheValidatorsDropped bool
}

type response struct {
	ContentCapture requestimport.ResponseContentCapture
	Status         int
	Headers        map[string]string
	HeaderList     dsl.HeaderList
	Body           string
}

// Evidence redaction of sensitive headers is shared with the paramize package
// (paramize.SensitiveHeaderKey); credential review uses the narrower
// credential.ImportCredentialHeader, as every importer does.

const (
	rawHTTPRequestDelimiter  = "\n\n### APEXLOAD REQUEST ###\n\n"
	rawHTTPResponseDelimiter = "\n\n### APEXLOAD RESPONSE ###\n\n"
)

func Convert(req ImportRequest) (*ConvertResult, error) {
	format := normalizeFormat(req.Format)
	if format == "" {
		return nil, errors.New("format is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return nil, errors.New("content is required")
	}

	var (
		exchanges   []exchange
		warnings    []string
		err         error
		diagnostics diag.Collector
		filtered    int
		skipped     int
	)

	httpVersionMode, err := requestimport.NormalizeHTTPVersionMode(req.Options.HTTPVersionMode)
	if err != nil {
		return nil, err
	}
	var pageResources map[int][]any
	switch format {
	case "raw_http":
		exchanges, warnings, err = parseRawHTTP(req.Content, req.ResponseContent, req.Options.BaseURL)
	case "curl":
		exchanges, warnings, err = parseCurl(req.Content, req.ResponseContent, &diagnostics)
	case "rest_json":
		exchanges, warnings, err = parseRESTJSON(req.Content)
	case "har":
		includeStatic := convertutil.BoolOption(req.Options.IncludeStaticAssets, false) || strings.EqualFold(req.Options.ResourceMode, "separate")
		exchanges, warnings, filtered, skipped, err = parseHARWithMode(req.Content, includeStatic, httpVersionMode, &diagnostics)
		if err == nil && !includeStatic && strings.EqualFold(req.Options.ResourceMode, "in_step") {
			pageResources = harInStepResources(req.Content)
		}
	default:
		return nil, fmt.Errorf("unsupported format %q", req.Format)
	}
	if err != nil {
		return nil, err
	}
	if len(exchanges) == 0 {
		return nil, errors.New("no requests found in sample input")
	}
	if format == "har" && req.Options.Profile != nil {
		var profileFiltered int
		exchanges, profileFiltered = applyRecordingProfileFilters(exchanges, req.Options.Profile)
		filtered += profileFiltered
		if profileFiltered > 0 {
			warnings = append(warnings, fmt.Sprintf("recording profile %q left %d request(s) out by host or content type", req.Options.Profile.Name, profileFiltered))
		}
		if len(exchanges) == 0 {
			return nil, errors.New("the recording profile filtered out every request")
		}
	}
	exchanges, normalizationWarnings := normalizeRecordedExchanges(exchanges, format)
	if format == "har" && len(req.Options.HTMLSteps) > 0 {
		forgetHTMLStepRequestValues(exchanges, req.Options.HTMLSteps)
	}
	warnings = append(warnings, normalizationWarnings...)
	if len(exchanges) == 0 {
		return nil, errors.New("no requests found in sample input")
	}

	includeAssertions := convertutil.BoolOption(req.Options.IncludeAssertions, true)
	inferExtractors := convertutil.BoolOption(req.Options.InferExtractors, true)
	pageTitleChecks := includeAssertions && convertutil.BoolOption(req.Options.PageTitleChecks, true)
	baseURL := strings.TrimSpace(req.Options.BaseURL)
	if baseURL == "" {
		baseURL = detectBaseURL(exchanges)
	}
	if baseURL == "" {
		warnings = append(warnings, "base URL could not be derived automatically; absolute URLs will be preserved in actions")
	}

	correlations := []Correlation{}
	correlationVars := map[string]string{}
	correlationVarSources := map[string]int{}
	extraCorrelationExtractors := map[int][]dsl.Extractor{}
	if inferExtractors {
		var analysisWarnings []string
		if format == "har" {
			var settings *recordingprofile.CorrelationSettings
			if req.Options.Profile != nil {
				settings = &req.Options.Profile.Correlation
			}
			correlations, analysisWarnings, err = detectHARCorrelationsWithSettings(exchanges, settings)
		} else {
			correlations, err = detectCorrelationsChecked(exchanges)
		}
		if err != nil {
			if !errors.Is(err, correlate.ErrCorrelationWorkLimit) {
				return nil, fmt.Errorf("detect correlations: %w", err)
			}
			// A deterministic work limit must not fail the import: the script still
			// converts, just without automatic correlation rewrites.
			correlations = nil
			warnings = append(warnings, fmt.Sprintf("correlation analysis was not completed and no automatic correlation rewrites were applied: %v", err))
		}
		correlations = collapseSameSourceDuplicateCorrelations(exchanges, correlations)
		if req.Options.HighConfidenceOnly {
			for index := range correlations {
				correlations[index].reviewOnly = !correlate.HighConfidence(correlate.Correlation{Confidence: correlations[index].Confidence})
			}
		}
		if len(analysisWarnings) > 0 {
			// Incomplete analysis misses correlations; it does not invalidate the
			// ones found. Each detected correlation finished its reuse and
			// client-origin checks (a budget stop discards the candidate in
			// flight), so they are applied while the unanalyzed gap stays
			// visible. A value seen inside a longer identifier is held for review
			// because the skipped part may hold that longer value.
			detail := "correlation coverage is incomplete; detected correlations were fully checked and applied (except values that also occur inside a longer identifier, which are held for review), but values in the unanalyzed part keep their recorded literals and need review: " + strings.Join(analysisWarnings, "; ")
			warnings = append(warnings, detail)
			diagnostics.Add(diag.Warning, "HAR correlation analysis incomplete", "HAR import", detail)
			correlationVars, correlationVarSources, extraCorrelationExtractors = prepareIncompleteCorrelationApplication(exchanges, correlations)
		} else {
			correlationVars, correlationVarSources, extraCorrelationExtractors = prepareCorrelationApplication(exchanges, correlations)
		}
		if len(correlations) == 0 {
			warnings = append(warnings, "no reusable correlation values were detected across the provided samples")
		}
	}

	scriptName := strings.TrimSpace(req.Name)
	if scriptName == "" {
		scriptName = defaultScriptName(exchanges)
	}

	result := &ConvertResult{
		Script: &dsl.Script{
			Name:          scriptName,
			Version:       "1.0.0",
			Protocol:      "http",
			GeneratedBy:   "sample-import",
			GeneratedFrom: format,
			CreatedAt:     time.Now(),
			Config: dsl.ScriptConfig{
				BaseURL: baseURL,
				ThinkTime: dsl.ThinkTimeConfig{
					Type:   "fixed",
					MeanMs: 1000,
				},
				Pacing: dsl.PacingConfig{Type: "none"},
				RunLogic: dsl.RunLogicConfig{
					Iterations:       0,
					AutoTransactions: "none",
				},
				// continue_on_error is left unset for the scenario policy to decide.
				ErrorHandling: dsl.ErrorHandlingConfig{
					MaxRetries: 0,
				},
				Log: dsl.LogConfig{Level: "standard"},
			},
		},
		Warnings:     warnings,
		Correlations: correlations,
		Stats: ConvertStats{
			Format:          format,
			TotalRequests:   len(exchanges) + filtered + skipped,
			FilteredAssets:  filtered,
			SkippedRequests: skipped,
		},
	}
	if warning := requestimport.ApplyIntegratedAuth(result.Script, integratedAuthHosts(exchanges)); warning != "" {
		result.Warnings = append(result.Warnings, warning)
	}
	if format == "raw_http" && rawHTTP11Only(exchanges) {
		// Raw HTTP/1.1 connection-specific headers are executable only when the
		// script requires HTTP/1.1. Leaving the config in automatic negotiation
		// mode would make import-time validation and runtime replay disagree.
		result.Script.Config.HTTP.Version = "1.1"
	}
	if javaHTTPConfig, ok := detectJavaHTTPConfig(exchanges); ok {
		result.Script.Protocol = "java_http"
		result.Script.Config.JavaHTTP = javaHTTPConfig
		result.Warnings = append(result.Warnings, "Java-over-HTTP serialized-object traffic was imported in byte-preserving embedded mode; request/object mappings are not applied without a governed java_vuser sidecar flow")
	}
	var sourceFiles map[string]string
	var sourceLocations []sampleSourceLocation
	if !req.Options.OmitSourceReview {
		sourceFiles = sampleSourceFiles(format, req.Content, req.ResponseContent)
		sourceLocations = sampleSourceLocations(format, req.Content, req.ResponseContent, exchanges)
	}
	sourceMappings := make([]sourceview.Mapping, 0, len(exchanges))
	finalizationReports := make([]requestimport.Report, 0, len(exchanges))
	defer func() {
		clearSampleReportRequirementValues(finalizationReports)
	}()
	credentialNames := credentialpolicy.NewNameRegistry()

	for idx, ex := range exchanges {
		stepVars := correlationVarsAvailableAtStep(correlationVars, correlationVarSources, idx+1)
		mergeFileAssets(result.Script, ex.FileAssets)
		target := relativeTarget(ex.URL, baseURL)
		headers := cloneHeaders(ex.Headers)
		headerList := cloneHeaderList(ex.HeaderList)

		body := ex.Body
		if ex.BodyEncoding == "" {
			body = applyCorrelationVars(body, stepVars)
		}
		target = applyCorrelationVars(target, stepVars)
		headers = applyCorrelationVarsToHeaders(headers, stepVars)
		headerList = applyCorrelationVarsToHeaderList(headerList, stepVars)

		var actionParams map[string]any
		if resources := pageResources[ex.SourceEntryID]; len(resources) > 0 {
			actionParams = map[string]any{"resources": resources}
		}
		action := dsl.Action{
			Params:       actionParams,
			Name:         exchangeActionName(idx, ex),
			Type:         "request",
			Method:       strings.ToUpper(ex.Method),
			Target:       target,
			Headers:      headers,
			HeaderList:   headerList,
			Body:         body,
			BodyEncoding: ex.BodyEncoding,
			Metadata:     cloneMetadata(ex.Metadata),
		}
		if name := transactionAt(req.Options.Transactions, ex); name != "" && action.Metadata[harSourceTransactionMeta] == nil {
			if action.Metadata == nil {
				action.Metadata = map[string]any{}
			}
			action.Metadata[harSourceTransactionMeta] = name
		}
		if htmlStep := req.Options.HTMLSteps[ex.SourceEntryID]; htmlStep != nil && format == "har" {
			// The page decides the URL, body and encoding at replay.
			if action.Params == nil {
				action.Params = map[string]any{}
			}
			action.Params[httpprotocol.HTMLStepParam] = htmlStep
			action.Target = "#html-step"
			action.Body, action.BodyEncoding = "", ""
			// The live page is the Referer, as for any HTML-mode step; the
			// recorded one may carry the recorded page's values.
			for name := range action.Headers {
				if strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Content-Length") || strings.EqualFold(name, "Referer") {
					delete(action.Headers, name)
				}
			}
			action.HeaderList = withoutHeaders(action.HeaderList, "Content-Type", "Content-Length", "Referer")
		}
		if ex.TimeoutMs > 0 {
			action.TimeoutMs = ex.TimeoutMs
		}
		if isExplicitRedirectStep(ex) {
			zero := 0
			action.MaxRedirects = &zero
		}
		applyExchangeReviewFindings(&action, ex)
		if thinkTimeMs, ok := thinkTimeAfterThreshold(exchanges, idx, thinkThreshold(req.Options.Profile)); ok {
			action.ThinkTimeAfter = true
			action.ThinkTime = dsl.ThinkTimeConfig{
				Type:   "fixed",
				MeanMs: thinkTimeMs,
			}
		}

		if len(action.Headers) == 0 {
			action.Headers = nil
		}
		applySOAPSampleHints(&action)

		if ex.Response != nil {
			if includeAssertions && ex.Response.Status > 0 {
				action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code", Expected: replayedStatus(ex)})
				if location := responseSourceLocation(sourceLocations, idx); location.SourceFile != "" {
					sourceMappings = append(sourceMappings, sourceview.Mapping{
						SourceFile: location.SourceFile,
						StartLine:  location.StartLine,
						EndLine:    location.EndLine,
						Function:   "response_status",
						TargetPath: fmt.Sprintf("actions[%d].assertions[%d]", idx, len(action.Assertions)-1),
						TargetName: "status_code",
						TargetKind: "assertion",
						Status:     "contributor",
						Notes:      "Sample response status was converted into a generated assertion.",
					})
				}
				result.Stats.Assertions++
			}
			if pageTitleChecks {
				if title := requestimport.RecordedPageTitle(responseHeaderValue(ex.Response, "Content-Type"), ex.Response.Status, ex.Response.Body, correlationValues(correlations)); title != "" {
					action.Assertions = append(action.Assertions, dsl.Assertion{Type: "body_contains", Value: title})
					result.Stats.Assertions++
				}
			}
			if inferExtractors {
				for _, corr := range correlations {
					if corr.PreApplied && corr.FoundInStep == idx+1 {
						onMiss, defaultValue := correlationMissPolicy(corr, corr.missDefault)
						action.Extractors = append(action.Extractors, dsl.Extractor{
							Name:         corr.Name,
							Source:       corr.Source,
							Type:         extractorTypeForCorrelation(corr),
							Expression:   corr.Expression,
							Convert:      corr.Convert,
							OnMiss:       onMiss,
							DefaultValue: defaultValue,
						})
						if location := responseSourceLocation(sourceLocations, idx); location.SourceFile != "" {
							sourceMappings = append(sourceMappings, sourceview.Mapping{
								SourceFile: location.SourceFile,
								StartLine:  location.StartLine,
								EndLine:    location.EndLine,
								Function:   "response_correlation",
								TargetPath: fmt.Sprintf("actions[%d].extractors[%d]", idx, len(action.Extractors)-1),
								TargetName: corr.Name,
								TargetKind: "extractor",
								Status:     "contributor",
								Notes:      "Sample response value was converted into a generated extractor.",
							})
						}
						result.Stats.Extractors++
					}
				}
				for _, extractor := range extraCorrelationExtractors[idx+1] {
					action.Extractors = append(action.Extractors, extractor)
					if location := responseSourceLocation(sourceLocations, idx); location.SourceFile != "" {
						sourceMappings = append(sourceMappings, sourceview.Mapping{
							SourceFile: location.SourceFile,
							StartLine:  location.StartLine,
							EndLine:    location.EndLine,
							Function:   "response_correlation",
							TargetPath: fmt.Sprintf("actions[%d].extractors[%d]", idx, len(action.Extractors)-1),
							TargetName: extractor.Name,
							TargetKind: "extractor",
							Status:     "contributor",
							Notes:      "Encoded sample response value was converted into a generated extractor.",
						})
					}
					result.Stats.Extractors++
				}
			}
		}

		location := requestSourceLocation(sourceLocations, idx)
		actionPath := fmt.Sprintf("actions[%d]", idx)
		action, report, err := finalizeSampleRequestAction(
			action,
			ex,
			actionPath,
			location,
			format,
			credentialNames,
			sampleProvenVariables(stepVars),
		)
		if err != nil {
			return nil, fmt.Errorf("finalize sample request %d: %w", idx+1, err)
		}
		finalizationReports = append(finalizationReports, report)
		sourceMappings = append(sourceMappings, sourceview.Mapping{
			SourceFile: location.SourceFile,
			StartLine:  location.StartLine,
			EndLine:    location.EndLine,
			Function:   sampleSourceFunction(format),
			TargetPath: actionPath,
			TargetName: action.Name,
			TargetKind: "action",
			Status:     "converted",
			Notes:      "Source request was converted into a generated APEX Load action.",
		})
		result.Script.Actions = append(result.Script.Actions, action)
		result.Stats.ConvertedActions++
	}

	applySOAPSampleProtocol(result.Script, &result.Warnings)
	finalized, err := requestimport.FinalizeScript(result.Script, finalizationReports, requestimport.FinalizeOptions{})
	if err != nil {
		return nil, err
	}
	result.ProtectedRequirements.Store(finalized.Requirements)
	clearSampleRequirements(finalized.Requirements)
	result.Script = finalized.Script
	if format == "har" {
		structureMultipartBodies(result.Script)
	}

	if gap := result.Stats.TotalRequests - result.Stats.FilteredAssets - result.Stats.SkippedRequests - result.Stats.ConvertedActions; gap > 0 {
		diagnostics.Add(diag.Warning, "Sample completeness guard", format, fmt.Sprintf("%d request(s) were parsed but not converted into actions", gap))
	}
	var timestampsNote string
	if format == "har" {
		if timestampsNote = applyRecordedClientTimestamps(result.Script, req.Content); timestampsNote != "" {
			result.Warnings = append(result.Warnings, timestampsNote)
		}
		var shapeWarnings []string
		var newIndex []int
		var startedAt []time.Time
		sourceMappings, newIndex, startedAt, shapeWarnings = shapeRecordedHAR(result.Script, exchanges, req.Content, convertutil.BoolOption(req.Options.AsyncScan, true), sourceMappings)
		result.Warnings = append(result.Warnings, shapeWarnings...)
		if len(req.Options.Transactions) > 0 {
			if newIndex == nil {
				startedAt = exchangeStartTimes(exchanges, len(result.Script.Actions))
			}
			positions, note := insertTransactionMarkers(result.Script, startedAt, req.Options.Transactions)
			if note != "" {
				result.Warnings = append(result.Warnings, note)
			}
			if positions != nil {
				// The baseline and source mappings name actions by index.
				newIndex = composeActionIndex(newIndex, positions)
				moved := recordedshape.Result{NewIndex: positions}
				for index := range sourceMappings {
					sourceMappings[index].TargetPath, _ = moved.RemapPath(sourceMappings[index].TargetPath)
				}
			}
		}
		result.Baseline = recordedBaseline(exchanges, newIndex)
	}
	for index := range result.Correlations {
		corr := &result.Correlations[index]
		corr.FoundInEntry = exchangeEntry(exchanges, corr.FoundInStep)
		corr.UsedInEntries = make([]int, 0, len(corr.UsedInSteps))
		for _, step := range corr.UsedInSteps {
			corr.UsedInEntries = append(corr.UsedInEntries, exchangeEntry(exchanges, step))
		}
	}
	result.Unsupported = append(diagnostics.Items(), finalized.Diagnostics...)
	if timestampsNote != "" {
		result.Unsupported = append(result.Unsupported, diag.Diagnostic{Code: ClientTimestampsDiagnosticCode, Element: "Client-generated timestamps", Location: "HAR requests", Detail: timestampsNote, Severity: diag.Warning})
	}
	result.Stats.CorrelationsDetected = len(correlations)
	if !req.Options.OmitSourceReview {
		result.SourceReview = sourceview.Build(sourceFiles, sourceMappings)
	}
	return result, nil
}

// ClientTimestampsDiagnosticCode marks the note that recorded client
// timestamps get a fresh value at replay.
const ClientTimestampsDiagnosticCode = "client_generated_timestamps"

// exchangeStartTimes is when each action started in the recording, for a
// script that holds one action per exchange (zero when unknown); nil when it
// does not.
func exchangeStartTimes(exchanges []exchange, actions int) []time.Time {
	if len(exchanges) != actions {
		return nil
	}
	times := make([]time.Time, len(exchanges))
	for index, ex := range exchanges {
		if ex.HasStartedAt {
			times[index] = ex.StartedAt
		}
	}
	return times
}

// composeActionIndex follows each original action through a reshape
// (newIndex, nil when there was none) and then positions.
func composeActionIndex(newIndex, positions []int) []int {
	if newIndex == nil {
		return positions
	}
	composed := make([]int, len(newIndex))
	for index, shaped := range newIndex {
		composed[index] = -1
		if shaped >= 0 && shaped < len(positions) {
			composed[index] = positions[shaped]
		}
	}
	return composed
}

// exchangeEntry is the source entry of a 1-based step.
func exchangeEntry(exchanges []exchange, step int) int {
	if step < 1 || step > len(exchanges) {
		return 0
	}
	return exchanges[step-1].SourceEntryID
}

// recordedBaseline is the raw observation of each converted exchange, with
// ID its action index after shaping (newIndex, when the script was shaped);
// exchanges folded into a conversation are left out.
func recordedBaseline(exchanges []exchange, newIndex []int) []correlate.Observation {
	baseline := make([]correlate.Observation, 0, len(exchanges))
	for index, ex := range exchanges {
		id := index
		if newIndex != nil {
			if index >= len(newIndex) || newIndex[index] < 0 {
				continue
			}
			id = newIndex[index]
		}
		observation := correlate.Observation{
			ID:                     id,
			RequestURL:             ex.URL,
			RequestBody:            ex.Body,
			RequestBodyTransformed: ex.BodyEncoding != "",
			RequestHeaders:         correlationObservationHeaders(ex.Headers, ex.HeaderList),
		}
		if ex.Response != nil {
			observation.ResponseBody = ex.Response.Body
			observation.ResponseHeaders = requestimport.ResponseContentBaselineHeaders(correlationObservationHeaders(ex.Response.Headers, ex.Response.HeaderList), ex.Response.ContentCapture)
			observation.ResponseStatus = ex.Response.Status
		}
		baseline = append(baseline, observation)
	}
	return baseline
}

// exchangeActionName names an exchange's action: its recorded step name, or
// its method and path.
func exchangeActionName(index int, ex exchange) string {
	if name := strings.TrimSpace(ex.StepName); name != "" {
		return fmt.Sprintf("%02d - %s", index+1, name)
	}
	return actionName(index, ex.Method, ex.URL)
}

func isExplicitRedirectStep(ex exchange) bool {
	if ex.Response == nil || ex.Response.Status < 300 || ex.Response.Status > 399 {
		return false
	}
	return headerLookup(ex.Response.Headers, "Location") != ""
}

func mergeFileAssets(script *dsl.Script, assets []dsl.DataSource) {
	if script == nil || len(assets) == 0 {
		return
	}
	byName := make(map[string]int, len(script.DataSources))
	for i, source := range script.DataSources {
		byName[strings.TrimSpace(source.Name)] = i
	}
	for _, asset := range assets {
		asset.Name = strings.TrimSpace(asset.Name)
		if asset.Name == "" {
			continue
		}
		asset.Type = "file"
		if existingIndex, ok := byName[asset.Name]; ok {
			existing := script.DataSources[existingIndex]
			if existing.FileName == "" {
				existing.FileName = asset.FileName
			}
			if existing.ContentType == "" {
				existing.ContentType = asset.ContentType
			}
			if existing.Path == "" {
				existing.Path = asset.Path
			}
			if existing.StorageRef == "" {
				existing.StorageRef = asset.StorageRef
			}
			if existing.InlineData == "" {
				existing.InlineData = asset.InlineData
			}
			if existing.Encoding == "" {
				existing.Encoding = asset.Encoding
			}
			existing.Type = "file"
			script.DataSources[existingIndex] = existing
			continue
		}
		byName[asset.Name] = len(script.DataSources)
		script.DataSources = append(script.DataSources, asset)
	}
}

func thinkTimeAfter(exchanges []exchange, index int) (float64, bool) {
	return thinkTimeAfterThreshold(exchanges, index, 500*time.Millisecond)
}

func thinkThreshold(profile *recordingprofile.Profile) time.Duration {
	if profile == nil {
		return recordingprofile.DefaultThinkTimeMs * time.Millisecond
	}
	return time.Duration(profile.ThinkTimeThreshold()) * time.Millisecond
}

// thinkTimeAfterThreshold is the recorded pause after an exchange, when it
// exceeds threshold.
func thinkTimeAfterThreshold(exchanges []exchange, index int, threshold time.Duration) (float64, bool) {
	if index < 0 || index >= len(exchanges)-1 {
		return 0, false
	}
	current := exchanges[index]
	next := exchanges[index+1]
	if next.HasRecordedThinkTime {
		// The recorder measured the pause before the next request, or the
		// tester edited it.
		if time.Duration(next.RecordedThinkTimeMs)*time.Millisecond > threshold {
			return float64(next.RecordedThinkTimeMs), true
		}
		return 0, false
	}
	if current.HasStartedAt && next.HasStartedAt {
		gap := next.StartedAt.Sub(current.StartedAt) - time.Duration(current.DurationMs*float64(time.Millisecond))
		if gap > threshold {
			return float64(gap / time.Millisecond), true
		}
	}
	return 0, false
}

func detectBaseURL(exchanges []exchange) string {
	for _, ex := range exchanges {
		parsed, err := url.Parse(ex.URL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			continue
		}
		return parsed.Scheme + "://" + parsed.Host
	}
	return ""
}

func actionName(index int, method, rawURL string) string {
	parsed, err := url.Parse(rawURL)
	path := rawURL
	if err == nil && parsed.Path != "" {
		path = parsed.Path
	}
	path = strings.Trim(path, "/")
	if path == "" {
		path = "root"
	}
	path = strings.ReplaceAll(path, "/", " ")
	return fmt.Sprintf("%02d - %s %s", index+1, strings.ToUpper(method), path)
}

func relativeTarget(rawURL, baseURL string) string {
	if baseURL == "" {
		return rawURL
	}
	parsedRaw, rawErr := url.Parse(rawURL)
	parsedBase, baseErr := url.Parse(baseURL)
	if rawErr == nil && parsedRaw.User != nil {
		// Userinfo is a credential-bearing request surface. Relativizing would
		// silently discard it before the shared projector can externalize and
		// review-gate the value.
		return rawURL
	}
	if rawErr == nil && baseErr == nil && parsedRaw.Scheme != "" && parsedBase.Scheme != "" &&
		strings.EqualFold(parsedRaw.Scheme, parsedBase.Scheme) &&
		strings.EqualFold(parsedRaw.Hostname(), parsedBase.Hostname()) &&
		effectivePort(parsedRaw) == effectivePort(parsedBase) {
		target := parsedRaw.RequestURI()
		if target == "" {
			return "/"
		}
		return target
	}
	return rawURL
}

func effectivePort(parsed *url.URL) string {
	if port := parsed.Port(); port != "" {
		return port
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http":
		return "80"
	case "https":
		return "443"
	default:
		return ""
	}
}

func defaultScriptName(exchanges []exchange) string {
	if len(exchanges) == 0 {
		return "Sample_Import"
	}
	parsed, err := url.Parse(exchanges[0].URL)
	if err == nil && parsed.Path != "" {
		name := strings.Trim(parsed.Path, "/")
		name = strings.ReplaceAll(name, "/", "_")
		if name != "" {
			return strings.ToUpper(exchanges[0].Method) + "_" + sanitizeIdentifier(name)
		}
	}
	return "Sample_Import"
}

// Accounting reports this conversion's completeness in the terms every importer
// shares. See convertresult.Accounting.
func (result *ConvertResult) Accounting() convertresult.Accounting {
	if result == nil {
		return convertresult.Accounting{}
	}
	return convertresult.New(
		result.Stats.TotalRequests,
		result.Stats.ConvertedActions,
		result.Stats.FilteredAssets+result.Stats.SkippedRequests,
		result.SourceReview.ReviewOnlyActionCount(),
	)
}

// responseHeaderValue returns a recorded response header, ignoring case.
func responseHeaderValue(resp *response, name string) string {
	if resp == nil {
		return ""
	}
	for key, value := range resp.Headers {
		if strings.EqualFold(key, name) {
			return value
		}
	}
	for _, header := range resp.HeaderList {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	return ""
}

// correlationValues lists the recorded values the correlation pass found.
func correlationValues(correlations []Correlation) []string {
	values := make([]string, 0, len(correlations))
	for _, corr := range correlations {
		values = append(values, corr.Value)
	}
	return values
}

// replayedStatus is the status a converted request should get. A 304 the
// browser's cache revalidation earned becomes the full response once the
// validators are gone: 206 for a range request, 200 otherwise.
func replayedStatus(ex exchange) int {
	if !ex.CacheValidatorsDropped || ex.Response.Status != http.StatusNotModified {
		return ex.Response.Status
	}
	if headerLookup(ex.Headers, "Range") != "" {
		return http.StatusPartialContent
	}
	return http.StatusOK
}

func withoutHeaders(list dsl.HeaderList, names ...string) dsl.HeaderList {
	if len(list) == 0 {
		return list
	}
	kept := list[:0:0]
	for _, header := range list {
		drop := false
		for _, name := range names {
			drop = drop || strings.EqualFold(header.Name, name)
		}
		if !drop {
			kept = append(kept, header)
		}
	}
	return kept
}

// forgetHTMLStepRequestValues drops the recorded body and query of requests
// that become HTML-mode steps. The page supplies them at replay, so they must
// not make the correlation pass extract hidden fields or link parameters that
// nothing sends any more.
func forgetHTMLStepRequestValues(exchanges []exchange, steps map[int]map[string]any) {
	for i := range exchanges {
		if steps[exchanges[i].SourceEntryID] == nil {
			continue
		}
		exchanges[i].Body, exchanges[i].BodyEncoding = "", ""
		if parsed, err := url.Parse(exchanges[i].URL); err == nil {
			parsed.RawQuery = ""
			exchanges[i].URL = parsed.String()
		}
	}
}
