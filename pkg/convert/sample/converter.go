package sample

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/dsl"
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
}

type ConvertStats struct {
	Format               string `json:"format"`
	TotalRequests        int    `json:"total_requests"`
	ConvertedActions     int    `json:"converted_actions"`
	FilteredAssets       int    `json:"filtered_assets,omitempty"`
	Assertions           int    `json:"assertions"`
	Extractors           int    `json:"extractors"`
	CorrelationsDetected int    `json:"correlations_detected"`
}

type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ExtractType string `json:"extract_type,omitempty"`
	Value       string `json:"-"`
	SampleValue string `json:"sample_value"`
	FoundInStep int    `json:"found_in_step"`
	UsedInSteps []int  `json:"used_in_steps"`
}

type ConvertResult struct {
	Script       *dsl.Script        `json:"script"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	Correlations []Correlation      `json:"correlations,omitempty"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`
}

type exchange struct {
	Method       string
	URL          string
	Headers      map[string]string
	Body         string
	BodyEncoding string
	Response     *response
	StartedAt    time.Time
	HasStartedAt bool
	DurationMs   float64
	TimeoutMs    int
}

type response struct {
	Status  int
	Headers map[string]string
	Body    string
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
	)

	switch format {
	case "raw_http":
		exchanges, warnings, err = parseRawHTTP(req.Content, req.ResponseContent, req.Options.BaseURL)
	case "curl":
		exchanges, warnings, err = parseCurl(req.Content, req.ResponseContent, &diagnostics)
	case "rest_json":
		exchanges, warnings, err = parseRESTJSON(req.Content)
	case "har":
		exchanges, warnings, filtered, err = parseHAR(req.Content, boolOption(req.Options.IncludeStaticAssets, false), &diagnostics)
	default:
		return nil, fmt.Errorf("unsupported format %q", req.Format)
	}
	if err != nil {
		return nil, err
	}
	if len(exchanges) == 0 {
		return nil, errors.New("no requests found in sample input")
	}

	includeAssertions := boolOption(req.Options.IncludeAssertions, true)
	inferExtractors := boolOption(req.Options.InferExtractors, true)
	sanitizeSensitive := boolOption(req.Options.SanitizeSensitive, true)

	baseURL := strings.TrimSpace(req.Options.BaseURL)
	if baseURL == "" {
		baseURL = detectBaseURL(exchanges)
	}
	if baseURL == "" {
		warnings = append(warnings, "base URL could not be derived automatically; absolute URLs will be preserved in actions")
	}

	correlations := []Correlation{}
	correlationVars := map[string]string{}
	if inferExtractors {
		correlations = detectCorrelations(exchanges)
		for _, corr := range correlations {
			value := corr.Value
			if value == "" {
				value = corr.SampleValue
			}
			correlationVars[value] = "{{var." + corr.Name + "}}"
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
				ErrorHandling: dsl.ErrorHandlingConfig{
					ContinueOnError: false,
					MaxRetries:      0,
				},
				Log: dsl.LogConfig{Level: "standard"},
			},
		},
		Warnings:     warnings,
		Correlations: correlations,
		Stats: ConvertStats{
			Format:         format,
			TotalRequests:  len(exchanges) + filtered,
			FilteredAssets: filtered,
		},
	}
	sourceFiles := sampleSourceFiles(format, req.Content, req.ResponseContent)
	redactedSourceFiles := redactSourceReviewSecrets(sourceFiles, exchanges)
	sourceLocations := sampleSourceLocations(format, req.Content, req.ResponseContent, exchanges)
	sourceMappings := make([]sourceview.Mapping, 0, len(exchanges))

	for idx, ex := range exchanges {
		target := relativeTarget(ex.URL, baseURL)
		headers := cloneHeaders(ex.Headers)
		if sanitizeSensitive {
			headers = sanitizeHeaders(headers)
		}

		body := ex.Body
		if ex.BodyEncoding == "" {
			body = applyCorrelationVars(body, correlationVars)
			if sanitizeSensitive {
				body = sanitizeBody(body)
			}
		}
		target = applyCorrelationVars(target, correlationVars)
		headers = applyCorrelationVarsToHeaders(headers, correlationVars)

		action := dsl.Action{
			Name:         actionName(idx, ex.Method, ex.URL),
			Type:         "request",
			Method:       strings.ToUpper(ex.Method),
			Target:       target,
			Headers:      headers,
			Body:         body,
			BodyEncoding: ex.BodyEncoding,
		}
		if ex.TimeoutMs > 0 {
			action.TimeoutMs = ex.TimeoutMs
		}
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
		if strings.TrimSpace(action.Body) == "" {
			action.Body = ""
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
			if inferExtractors {
				for _, corr := range correlations {
					if corr.FoundInStep == idx+1 {
						action.Extractors = append(action.Extractors, dsl.Extractor{
							Name:       corr.Name,
							Source:     corr.Source,
							Type:       extractorTypeForCorrelation(corr),
							Expression: corr.Expression,
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
			}
		}

		location := requestSourceLocation(sourceLocations, idx)
		sourceMappings = append(sourceMappings, sourceview.Mapping{
			SourceFile: location.SourceFile,
			StartLine:  location.StartLine,
			EndLine:    location.EndLine,
			Function:   sampleSourceFunction(format),
			TargetPath: fmt.Sprintf("actions[%d]", idx),
			TargetName: action.Name,
			TargetKind: "action",
			Status:     "converted",
			Notes:      "Source request was converted into a generated APEX Load action.",
		})
		result.Script.Actions = append(result.Script.Actions, action)
		result.Stats.ConvertedActions++
	}

	applySOAPSampleProtocol(result.Script, &result.Warnings)

	if gap := result.Stats.TotalRequests - result.Stats.ConvertedActions; gap > 0 {
		diagnostics.Add(diag.Warning, "Sample completeness guard", format, fmt.Sprintf("%d request(s) were parsed but not converted into actions", gap))
	}
	result.Unsupported = diagnostics.Items()
	result.Stats.CorrelationsDetected = len(correlations)
	result.SourceReview = sourceview.Build(sourceFiles, sourceMappings)
	if result.SourceReview != nil {
		for index := range result.SourceReview.SourceFiles {
			if redactedSourceFiles[result.SourceReview.SourceFiles[index].Path] {
				result.SourceReview.SourceFiles[index].Redacted = true
			}
		}
	}
	return result, nil
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
	if current.DurationMs > 500 {
		return current.DurationMs, true
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
	if strings.HasPrefix(rawURL, baseURL) {
		target := strings.TrimPrefix(rawURL, baseURL)
		if target == "" {
			return "/"
		}
		if !strings.HasPrefix(target, "/") {
			target = "/" + target
		}
		return target
	}
	return rawURL
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
