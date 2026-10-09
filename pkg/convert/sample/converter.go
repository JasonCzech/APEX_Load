package sample

import (
	"errors"
	"fmt"
	"github.com/jasonczech/apexload/pkg/convert/convertresult"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/correlate"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
	httpprotocol "github.com/jasonczech/apexload/pkg/protocol/http"
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
}

type response struct {
	ContentCapture requestimport.ResponseContentCapture
	Status         int
	Headers        map[string]string
	HeaderList     dsl.HeaderList
	Body           string
}

// Sensitive header detection is shared with the paramize package so every
// importer agrees on which headers carry credentials (see paramize.SensitiveHeaderKey).

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
		exchanges, warnings, filtered, skipped, err = parseHAR(req.Content, includeStatic, &diagnostics)
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
			correlations, analysisWarnings, err = detectHARCorrelationsForReview(exchanges)
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
	sourceFiles := sampleSourceFiles(format, req.Content, req.ResponseContent)
	sourceLocations := sampleSourceLocations(format, req.Content, req.ResponseContent, exchanges)
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
			Name:         actionName(idx, ex.Method, ex.URL),
			Type:         "request",
			Method:       strings.ToUpper(ex.Method),
			Target:       target,
			Headers:      headers,
			HeaderList:   headerList,
			Body:         body,
			BodyEncoding: ex.BodyEncoding,
			Metadata:     cloneMetadata(ex.Metadata),
		}
		if htmlStep := req.Options.HTMLSteps[ex.SourceEntryID]; htmlStep != nil && format == "har" {
			// The page decides the URL, body and encoding at replay.
			if action.Params == nil {
				action.Params = map[string]any{}
			}
			action.Params[httpprotocol.HTMLStepParam] = htmlStep
			action.Target = "#html-step"
			action.Body, action.BodyEncoding = "", ""
			for name := range action.Headers {
				if strings.EqualFold(name, "Content-Type") || strings.EqualFold(name, "Content-Length") {
					delete(action.Headers, name)
				}
			}
			action.HeaderList = withoutHeaders(action.HeaderList, "Content-Type", "Content-Length")
		}
		if ex.TimeoutMs > 0 {
			action.TimeoutMs = ex.TimeoutMs
		}
		if isExplicitRedirectStep(ex) {
			zero := 0
			action.MaxRedirects = &zero
		}
		applyExchangeReviewFindings(&action, ex)
		if thinkTimeMs, ok := thinkTimeAfter(exchanges, idx); ok {
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
				action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code", Expected: ex.Response.Status})
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
						action.Extractors = append(action.Extractors, dsl.Extractor{
							Name:       corr.Name,
							Source:     corr.Source,
							Type:       extractorTypeForCorrelation(corr),
							Expression: corr.Expression,
							Convert:    corr.Convert,
							OnMiss:     "fail",
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

	if gap := result.Stats.TotalRequests - result.Stats.FilteredAssets - result.Stats.SkippedRequests - result.Stats.ConvertedActions; gap > 0 {
		diagnostics.Add(diag.Warning, "Sample completeness guard", format, fmt.Sprintf("%d request(s) were parsed but not converted into actions", gap))
	}
	result.Unsupported = append(diagnostics.Items(), finalized.Diagnostics...)
	result.Stats.CorrelationsDetected = len(correlations)
	result.SourceReview = sourceview.Build(sourceFiles, sourceMappings)
	return result, nil
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
	if index < 0 || index >= len(exchanges)-1 {
		return 0, false
	}
	current := exchanges[index]
	next := exchanges[index+1]
	if current.HasStartedAt && next.HasStartedAt {
		gap := next.StartedAt.Sub(current.StartedAt) - time.Duration(current.DurationMs*float64(time.Millisecond))
		if gap > 500*time.Millisecond {
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
