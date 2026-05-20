package sample

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

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
	BaseURL           string `json:"base_url,omitempty"`
	IncludeAssertions *bool  `json:"include_assertions,omitempty"`
	InferExtractors   *bool  `json:"infer_extractors,omitempty"`
	SanitizeSensitive *bool  `json:"sanitize_sensitive,omitempty"`
}

type ConvertStats struct {
	Format               string `json:"format"`
	TotalRequests        int    `json:"total_requests"`
	ConvertedActions     int    `json:"converted_actions"`
	Assertions           int    `json:"assertions"`
	Extractors           int    `json:"extractors"`
	CorrelationsDetected int    `json:"correlations_detected"`
}

type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	SampleValue string `json:"sample_value"`
	FoundInStep int    `json:"found_in_step"`
	UsedInSteps []int  `json:"used_in_steps"`
}

type ConvertResult struct {
	Script       *dsl.Script   `json:"script"`
	Warnings     []string      `json:"warnings,omitempty"`
	Stats        ConvertStats  `json:"stats"`
	Correlations []Correlation `json:"correlations,omitempty"`
}

type exchange struct {
	Method       string
	URL          string
	Headers      map[string]string
	Body         string
	Response     *response
	StartedAt    time.Time
	HasStartedAt bool
	DurationMs   float64
}

type response struct {
	Status  int
	Headers map[string]string
	Body    string
}

type candidate struct {
	name       string
	value      string
	source     string
	expression string
}

var jsonCandidatePatterns = []struct {
	namePattern string
	regex       *regexp.Regexp
}{
	{namePattern: "corr_%s", regex: regexp.MustCompile(`(?i)"([A-Za-z0-9_]*(?:token|session|csrf|xsrf|auth|id))"\s*:\s*"([^\"]{6,})"`)},
	{namePattern: "corr_%s", regex: regexp.MustCompile(`(?i)"([A-Za-z0-9_]*(?:id))"\s*:\s*(\d{4,})`)},
}

var sensitiveHeaders = map[string]bool{
	"authorization":       true,
	"proxy-authorization": true,
	"cookie":              true,
	"set-cookie":          true,
	"x-api-key":           true,
	"api-key":             true,
}

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
		exchanges []exchange
		warnings  []string
		err       error
	)

	switch format {
	case "raw_http":
		exchanges, warnings, err = parseRawHTTP(req.Content, req.ResponseContent, req.Options.BaseURL)
	case "curl":
		exchanges, warnings, err = parseCurl(req.Content, req.ResponseContent)
	case "rest_json":
		exchanges, warnings, err = parseRESTJSON(req.Content)
	case "har":
		exchanges, warnings, err = parseHAR(req.Content)
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
			correlationVars[corr.SampleValue] = "{{var." + corr.Name + "}}"
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
			Format:        format,
			TotalRequests: len(exchanges),
		},
	}

	for idx, ex := range exchanges {
		target := relativeTarget(ex.URL, baseURL)
		headers := cloneHeaders(ex.Headers)
		if sanitizeSensitive {
			headers = sanitizeHeaders(headers)
		}

		body := ex.Body
		if sanitizeSensitive {
			body = sanitizeBody(body)
		}
		body = applyCorrelationVars(body, correlationVars)
		target = applyCorrelationVars(target, correlationVars)
		headers = applyCorrelationVarsToHeaders(headers, correlationVars)

		action := dsl.Action{
			Name:    actionName(idx, ex.Method, ex.URL),
			Type:    "request",
			Method:  strings.ToUpper(ex.Method),
			Target:  target,
			Headers: headers,
			Body:    body,
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
				result.Stats.Assertions++
			}
			if inferExtractors {
				for _, corr := range correlations {
					if corr.FoundInStep == idx+1 {
						action.Extractors = append(action.Extractors, dsl.Extractor{
							Name:       corr.Name,
							Source:     corr.Source,
							Type:       extractorTypeForSource(corr.Source),
							Expression: corr.Expression,
						})
						result.Stats.Extractors++
					}
				}
			}
		}

		result.Script.Actions = append(result.Script.Actions, action)
		result.Stats.ConvertedActions++
	}

	applySOAPSampleProtocol(result.Script, &result.Warnings)

	result.Stats.CorrelationsDetected = len(correlations)
	return result, nil
}

func applySOAPSampleProtocol(script *dsl.Script, warnings *[]string) {
	if script == nil || len(script.Actions) == 0 {
		return
	}
	soapCount := 0
	soapVersion := ""
	for _, action := range script.Actions {
		if action.SOAP == nil {
			continue
		}
		soapCount++
		if version := detectSOAPVersionFromSampleAction(action); version == "1.2" {
			soapVersion = "1.2"
		} else if soapVersion == "" {
			soapVersion = "1.1"
		}
	}
	if soapCount == 0 {
		return
	}
	if soapCount != len(script.Actions) {
		*warnings = append(*warnings, fmt.Sprintf("detected %d SOAP-style samples and %d non-SOAP samples; keeping HTTP protocol because the imported sample set is mixed", soapCount, len(script.Actions)-soapCount))
		return
	}
	if soapVersion == "" {
		soapVersion = "1.1"
	}
	script.Protocol = "soap"
	script.Config.SOAP = dsl.SOAPConfig{
		Version: soapVersion,
		FaultHandling: dsl.SOAPFaultHandlingConfig{
			FailOnFault: true,
		},
	}
}

func applySOAPSampleHints(action *dsl.Action) {
	if action == nil || !isSOAPSampleAction(action) {
		return
	}
	if action.Method == "" || strings.EqualFold(action.Method, "GET") {
		action.Method = "POST"
		action.Type = "POST"
	}
	soapAction := strings.TrimSpace(sampleHeaderValue(action.Headers, "SOAPAction"))
	operation := action.Name
	if soapAction != "" {
		operation = strings.Trim(soapAction, `"`)
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
		action.Headers = map[string]string{}
	}
	if sampleHeaderValue(action.Headers, "Content-Type") == "" {
		if detectSOAPVersionFromSampleAction(*action) == "1.2" {
			action.Headers["Content-Type"] = "application/soap+xml"
		} else {
			action.Headers["Content-Type"] = "text/xml"
		}
	}
}

func isSOAPSampleAction(action *dsl.Action) bool {
	if action == nil {
		return false
	}
	if strings.TrimSpace(sampleHeaderValue(action.Headers, "SOAPAction")) != "" {
		return true
	}
	contentType := strings.ToLower(strings.TrimSpace(sampleHeaderValue(action.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return true
	}
	body := strings.ToLower(strings.TrimSpace(action.Body))
	if strings.Contains(body, ":envelope") || strings.Contains(body, "<envelope") {
		return true
	}
	if strings.Contains(contentType, "text/xml") && body != "" {
		return strings.Contains(body, "soap") || strings.Contains(body, "<envelope")
	}
	return false
}

func detectSOAPVersionFromSampleAction(action dsl.Action) string {
	contentType := strings.ToLower(strings.TrimSpace(sampleHeaderValue(action.Headers, "Content-Type")))
	if strings.Contains(contentType, "application/soap+xml") {
		return "1.2"
	}
	body := strings.ToLower(strings.TrimSpace(action.Body))
	if strings.Contains(body, "http://www.w3.org/2003/05/soap-envelope") {
		return "1.2"
	}
	if isSOAPSampleAction(&action) {
		return "1.1"
	}
	return ""
}

func sampleHeaderValue(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}

func normalizeFormat(format string) string {
	value := strings.ToLower(strings.TrimSpace(format))
	switch value {
	case "http", "raw", "raw_http", "raw-http":
		return "raw_http"
	case "curl", "c_url":
		return "curl"
	case "json", "rest_json", "rest-message", "rest_message":
		return "rest_json"
	case "har", "har_entry", "har-entry":
		return "har"
	default:
		return value
	}
}

func boolOption(v *bool, defaultValue bool) bool {
	if v == nil {
		return defaultValue
	}
	return *v
}

func parseRawHTTP(content, responseContent, baseURL string) ([]exchange, []string, error) {
	requestSamples := splitBatchSamples(content, rawHTTPRequestDelimiter)
	if len(requestSamples) == 0 {
		return nil, nil, errors.New("raw HTTP request is empty")
	}
	responseSamples := splitBatchSamples(responseContent, rawHTTPResponseDelimiter)
	if len(responseSamples) > 0 && len(responseSamples) != len(requestSamples) {
		return nil, nil, fmt.Errorf("raw HTTP request count (%d) does not match response count (%d)", len(requestSamples), len(responseSamples))
	}

	warnings := []string{}
	exchanges := make([]exchange, 0, len(requestSamples))
	for index, sample := range requestSamples {
		req, err := parseRawRequest(sample, baseURL)
		if err != nil {
			return nil, nil, err
		}
		if len(responseSamples) > 0 {
			resp, respWarnings, err := parseRawResponse(responseSamples[index])
			if err != nil {
				return nil, nil, err
			}
			req.Response = resp
			warnings = append(warnings, respWarnings...)
		}
		exchanges = append(exchanges, req)
	}
	return exchanges, warnings, nil
}

func splitBatchSamples(content, delimiter string) []string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil
	}
	if !strings.Contains(content, delimiter) {
		return []string{content}
	}
	parts := strings.Split(content, delimiter)
	filtered := make([]string, 0, len(parts))
	for _, part := range parts {
		if strings.TrimSpace(part) != "" {
			filtered = append(filtered, part)
		}
	}
	return filtered
}

func parseRawRequest(content, baseURL string) (exchange, error) {
	head, body := splitHTTPMessage(content)
	lines := splitLines(head)
	if len(lines) == 0 {
		return exchange{}, errors.New("raw HTTP request is empty")
	}
	parts := strings.Fields(lines[0])
	if len(parts) < 2 {
		return exchange{}, errors.New("raw HTTP request line must contain method and target")
	}
	ex := exchange{
		Method:  strings.ToUpper(parts[0]),
		URL:     parts[1],
		Headers: parseHeaderLines(lines[1:]),
		Body:    body,
	}
	if !strings.HasPrefix(ex.URL, "http://") && !strings.HasPrefix(ex.URL, "https://") {
		host := headerLookup(ex.Headers, "host")
		if baseURL != "" {
			ex.URL = strings.TrimRight(baseURL, "/") + ensureLeadingSlash(ex.URL)
		} else if host != "" {
			scheme := "https"
			if strings.Contains(host, "localhost") || strings.Contains(host, "127.0.0.1") {
				scheme = "http"
			}
			ex.URL = scheme + "://" + host + ensureLeadingSlash(ex.URL)
		} else {
			return exchange{}, errors.New("raw HTTP request must include an absolute URL, Host header, or base_url option")
		}
	}
	return ex, nil
}

func parseRawResponse(content string) (*response, []string, error) {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return nil, nil, nil
	}
	if !strings.HasPrefix(strings.ToUpper(trimmed), "HTTP/") {
		return &response{Status: 200, Body: content}, []string{"response content did not include an HTTP status line; defaulted assertion status to 200"}, nil
	}
	head, body := splitHTTPMessage(content)
	lines := splitLines(head)
	if len(lines) == 0 {
		return nil, nil, errors.New("raw HTTP response is empty")
	}
	parts := strings.Fields(lines[0])
	if len(parts) < 2 {
		return nil, nil, errors.New("raw HTTP response status line is invalid")
	}
	status, err := strconv.Atoi(parts[1])
	if err != nil {
		return nil, nil, fmt.Errorf("invalid HTTP response status %q", parts[1])
	}
	return &response{Status: status, Headers: parseHeaderLines(lines[1:]), Body: body}, nil, nil
}

func parseCurl(content, responseContent string) ([]exchange, []string, error) {
	tokens, err := shellSplit(content)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse curl command: %w", err)
	}
	if len(tokens) == 0 || tokens[0] != "curl" {
		return nil, nil, errors.New("curl sample must start with 'curl'")
	}
	ex := exchange{Method: "GET", Headers: map[string]string{}}
	var pendingURL bool
	for index := 1; index < len(tokens); index++ {
		token := tokens[index]
		switch token {
		case "-X", "--request":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a method after -X/--request")
			}
			ex.Method = strings.ToUpper(tokens[index])
		case "-H", "--header":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a header value after -H/--header")
			}
			key, value, ok := splitHeader(tokens[index])
			if ok {
				ex.Headers[key] = value
			}
		case "-d", "--data", "--data-raw", "--data-binary", "--data-ascii":
			index++
			if index >= len(tokens) {
				return nil, nil, errors.New("curl sample is missing a body value after data flag")
			}
			if ex.Body == "" {
				ex.Body = tokens[index]
			} else {
				ex.Body += "&" + tokens[index]
			}
			if ex.Method == "GET" {
				ex.Method = "POST"
			}
		case "--url":
			pendingURL = true
		case "-A", "--user-agent":
			index++
			if index < len(tokens) {
				ex.Headers["User-Agent"] = tokens[index]
			}
		case "-b", "--cookie":
			index++
			if index < len(tokens) {
				ex.Headers["Cookie"] = tokens[index]
			}
		case "-u", "--user":
			index++
			if index < len(tokens) {
				ex.Headers["Authorization"] = "Basic " + tokens[index]
			}
		default:
			if pendingURL {
				ex.URL = token
				pendingURL = false
				continue
			}
			if !strings.HasPrefix(token, "-") && ex.URL == "" {
				ex.URL = token
			}
		}
	}
	if ex.URL == "" {
		return nil, nil, errors.New("curl sample did not include a URL")
	}
	var warnings []string
	if strings.TrimSpace(responseContent) != "" {
		resp, respWarnings, err := parseRawResponse(responseContent)
		if err != nil {
			return nil, nil, err
		}
		ex.Response = resp
		warnings = append(warnings, respWarnings...)
	}
	return []exchange{ex}, warnings, nil
}

func parseRESTJSON(content string) ([]exchange, []string, error) {
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil, nil, fmt.Errorf("failed to parse JSON sample: %w", err)
	}
	items, err := extractRESTItems(payload)
	if err != nil {
		return nil, nil, err
	}
	exchanges := make([]exchange, 0, len(items))
	for _, item := range items {
		ex, err := parseRESTItem(item)
		if err != nil {
			return nil, nil, err
		}
		exchanges = append(exchanges, ex)
	}
	return exchanges, nil, nil
}

func parseHAR(content string) ([]exchange, []string, error) {
	var payload any
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil, nil, fmt.Errorf("failed to parse HAR JSON: %w", err)
	}
	items, err := extractHARItems(payload)
	if err != nil {
		return nil, nil, err
	}
	exchanges := make([]exchange, 0, len(items))
	for _, item := range items {
		ex, err := parseHARItem(item)
		if err != nil {
			return nil, nil, err
		}
		exchanges = append(exchanges, ex)
	}
	warnings := []string{}
	if len(exchanges) > 1 {
		warnings = append(warnings, "HAR import preserves inter-request think time when entry timestamps are available, but browser-specific asset metadata is not preserved")
	}
	return exchanges, warnings, nil
}

func extractRESTItems(payload any) ([]map[string]any, error) {
	switch value := payload.(type) {
	case []any:
		items := make([]map[string]any, 0, len(value))
		for _, item := range value {
			mapped, ok := item.(map[string]any)
			if !ok {
				return nil, errors.New("REST JSON array entries must be objects")
			}
			items = append(items, mapped)
		}
		return items, nil
	case map[string]any:
		if requests, ok := value["requests"].([]any); ok {
			items := make([]map[string]any, 0, len(requests))
			for _, item := range requests {
				mapped, ok := item.(map[string]any)
				if !ok {
					return nil, errors.New("REST JSON requests entries must be objects")
				}
				items = append(items, mapped)
			}
			return items, nil
		}
		return []map[string]any{value}, nil
	default:
		return nil, errors.New("REST JSON must be an object or array")
	}
}

func parseRESTItem(item map[string]any) (exchange, error) {
	requestData := item
	if nested, ok := item["request"].(map[string]any); ok {
		requestData = nested
	}
	ex := exchange{
		Method:  strings.ToUpper(stringValue(requestData["method"])),
		URL:     stringValue(requestData["url"]),
		Headers: mapValueToStringMap(requestData["headers"]),
		Body:    bodyValue(requestData["body"]),
	}
	if ex.Method == "" || ex.URL == "" {
		return exchange{}, errors.New("REST JSON request entries must include method and url")
	}
	if responseData, ok := item["response"].(map[string]any); ok {
		ex.Response = &response{
			Status:  intValue(responseData["status"]),
			Headers: mapValueToStringMap(responseData["headers"]),
			Body:    bodyValue(responseData["body"]),
		}
	}
	return ex, nil
}

func extractHARItems(payload any) ([]map[string]any, error) {
	root, ok := payload.(map[string]any)
	if !ok {
		return nil, errors.New("HAR content must be a JSON object")
	}
	if logObj, ok := root["log"].(map[string]any); ok {
		root = logObj
	}
	if entries, ok := root["entries"].([]any); ok {
		items := make([]map[string]any, 0, len(entries))
		for _, entry := range entries {
			mapped, ok := entry.(map[string]any)
			if !ok {
				return nil, errors.New("HAR entries must be objects")
			}
			items = append(items, mapped)
		}
		return items, nil
	}
	if _, hasRequest := root["request"]; hasRequest {
		return []map[string]any{root}, nil
	}
	return nil, errors.New("HAR content must include log.entries or a single entry with request/response")
}

func parseHARItem(item map[string]any) (exchange, error) {
	requestData, ok := item["request"].(map[string]any)
	if !ok {
		return exchange{}, errors.New("HAR entry is missing request")
	}
	ex := exchange{
		Method:     strings.ToUpper(stringValue(requestData["method"])),
		URL:        stringValue(requestData["url"]),
		Body:       harBody(requestData),
		Headers:    headerListToMap(requestData["headers"]),
		DurationMs: floatValue(item["time"]),
	}
	if startedAt, ok := parseHARStartedAt(item["startedDateTime"]); ok {
		ex.StartedAt = startedAt
		ex.HasStartedAt = true
	}
	if ex.Method == "" || ex.URL == "" {
		return exchange{}, errors.New("HAR request entries must include method and url")
	}
	if responseData, ok := item["response"].(map[string]any); ok {
		responseBody := ""
		if content, ok := responseData["content"].(map[string]any); ok {
			responseBody = bodyValue(content["text"])
		}
		ex.Response = &response{
			Status:  intValue(responseData["status"]),
			Headers: headerListToMap(responseData["headers"]),
			Body:    responseBody,
		}
	}
	return ex, nil
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

func detectCorrelations(exchanges []exchange) []Correlation {
	seen := map[string]bool{}
	correlations := []Correlation{}
	for index, ex := range exchanges {
		if ex.Response == nil {
			continue
		}
		candidates := responseCandidates(*ex.Response)
		for _, candidate := range candidates {
			if candidate.value == "" || seen[candidate.value] {
				continue
			}
			usedIn := []int{}
			for later := index + 1; later < len(exchanges); later++ {
				if exchangeContains(exchanges[later], candidate.value) {
					usedIn = append(usedIn, later+1)
				}
			}
			if len(usedIn) == 0 {
				continue
			}
			seen[candidate.value] = true
			correlations = append(correlations, Correlation{
				Name:        candidate.name,
				Source:      candidate.source,
				Expression:  candidate.expression,
				SampleValue: candidate.value,
				FoundInStep: index + 1,
				UsedInSteps: usedIn,
			})
		}
	}
	sort.Slice(correlations, func(i, j int) bool {
		if correlations[i].FoundInStep == correlations[j].FoundInStep {
			return correlations[i].Name < correlations[j].Name
		}
		return correlations[i].FoundInStep < correlations[j].FoundInStep
	})
	return correlations
}

func responseCandidates(resp response) []candidate {
	results := []candidate{}
	for _, pattern := range jsonCandidatePatterns {
		matches := pattern.regex.FindAllStringSubmatch(resp.Body, -1)
		for _, match := range matches {
			if len(match) < 3 {
				continue
			}
			key := sanitizeIdentifier(match[1])
			results = append(results, candidate{
				name:       fmt.Sprintf(pattern.namePattern, key),
				value:      match[2],
				source:     "body",
				expression: "$." + match[1],
			})
		}
	}
	for key, value := range resp.Headers {
		lowerKey := strings.ToLower(key)
		if !strings.Contains(lowerKey, "token") && !strings.Contains(lowerKey, "cookie") && !strings.Contains(lowerKey, "location") && !strings.Contains(lowerKey, "id") {
			continue
		}
		if len(value) < 4 {
			continue
		}
		results = append(results, candidate{
			name:       "corr_" + sanitizeIdentifier(lowerKey),
			value:      value,
			source:     "header",
			expression: key,
		})
	}
	return results
}

func exchangeContains(ex exchange, value string) bool {
	if strings.Contains(ex.URL, value) || strings.Contains(ex.Body, value) {
		return true
	}
	for _, headerValue := range ex.Headers {
		if strings.Contains(headerValue, value) {
			return true
		}
	}
	return false
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

func splitHTTPMessage(content string) (string, string) {
	if strings.Contains(content, "\r\n\r\n") {
		parts := strings.SplitN(content, "\r\n\r\n", 2)
		return parts[0], parts[1]
	}
	parts := strings.SplitN(content, "\n\n", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return content, ""
}

func splitLines(content string) []string {
	content = strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(content, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimRight(line, "\r")
		if trimmed != "" {
			filtered = append(filtered, trimmed)
		}
	}
	return filtered
}

func parseHeaderLines(lines []string) map[string]string {
	headers := map[string]string{}
	for _, line := range lines {
		key, value, ok := splitHeader(line)
		if ok {
			headers[key] = value
		}
	}
	return headers
}

func splitHeader(value string) (string, string, bool) {
	parts := strings.SplitN(value, ":", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func headerLookup(headers map[string]string, key string) string {
	for headerKey, value := range headers {
		if strings.EqualFold(headerKey, key) {
			return value
		}
	}
	return ""
}

func shellSplit(input string) ([]string, error) {
	var tokens []string
	var current strings.Builder
	var quote rune
	escaped := false
	flush := func() {
		if current.Len() > 0 {
			tokens = append(tokens, current.String())
			current.Reset()
		}
	}
	for _, char := range input {
		if escaped {
			current.WriteRune(char)
			escaped = false
			continue
		}
		switch char {
		case '\\':
			escaped = true
		case '\'', '"':
			if quote == 0 {
				quote = char
				continue
			}
			if quote == char {
				quote = 0
				continue
			}
			current.WriteRune(char)
		case ' ', '\n', '\t':
			if quote != 0 {
				current.WriteRune(char)
				continue
			}
			flush()
		default:
			current.WriteRune(char)
		}
	}
	if escaped || quote != 0 {
		return nil, errors.New("unterminated quoted string in curl sample")
	}
	flush()
	return tokens, nil
}

func ensureLeadingSlash(value string) string {
	if strings.HasPrefix(value, "/") {
		return value
	}
	return "/" + value
}

func cloneHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(input))
	for key, value := range input {
		cloned[key] = value
	}
	return cloned
}

func sanitizeHeaders(input map[string]string) map[string]string {
	if len(input) == 0 {
		return input
	}
	for key := range input {
		if sensitiveHeaders[strings.ToLower(key)] {
			input[key] = "{{secret." + sanitizeIdentifier(strings.ToLower(key)) + "}}"
		}
	}
	return input
}

func sanitizeBody(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return body
	}
	replacements := []*regexp.Regexp{
		regexp.MustCompile(`(?i)("(?:password|token|access_token|refresh_token|api_key)"\s*:\s*")([^"]+)(")`),
		regexp.MustCompile(`(?i)((?:password|token|access_token|refresh_token|api_key)=)([^&\s]+)`),
	}
	result := body
	for _, pattern := range replacements {
		result = pattern.ReplaceAllString(result, `${1}{{secret.redacted}}${3}`)
	}
	return result
}

func applyCorrelationVars(value string, vars map[string]string) string {
	for raw, replacement := range vars {
		if raw == "" {
			continue
		}
		value = strings.ReplaceAll(value, raw, replacement)
	}
	return value
}

func applyCorrelationVarsToHeaders(headers map[string]string, vars map[string]string) map[string]string {
	if len(headers) == 0 {
		return headers
	}
	for key, value := range headers {
		headers[key] = applyCorrelationVars(value, vars)
	}
	return headers
}

func extractorTypeForSource(source string) string {
	if source == "header" {
		return "header"
	}
	return "jsonpath"
}

func sanitizeIdentifier(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "value"
	}
	replacer := regexp.MustCompile(`[^A-Za-z0-9_]+`)
	value = replacer.ReplaceAllString(strings.ToLower(value), "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "value"
	}
	return value
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	default:
		return ""
	}
}

func intValue(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return int(parsed)
	case string:
		parsed, _ := strconv.Atoi(typed)
		return parsed
	default:
		return 0
	}
}

func floatValue(value any) float64 {
	switch typed := value.(type) {
	case float64:
		return typed
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	case json.Number:
		parsed, _ := typed.Float64()
		return parsed
	case string:
		parsed, _ := strconv.ParseFloat(typed, 64)
		return parsed
	default:
		return 0
	}
}

func parseHARStartedAt(value any) (time.Time, bool) {
	raw := stringValue(value)
	if raw == "" {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		return time.Time{}, false
	}
	return parsed, true
}

func bodyValue(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(data)
	}
}

func mapValueToStringMap(value any) map[string]string {
	if value == nil {
		return nil
	}
	mapped, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(mapped))
	for key, item := range mapped {
		result[key] = bodyValue(item)
	}
	return result
}

func headerListToMap(value any) map[string]string {
	items, ok := value.([]any)
	if !ok {
		return nil
	}
	result := make(map[string]string, len(items))
	for _, item := range items {
		mapped, ok := item.(map[string]any)
		if !ok {
			continue
		}
		name := stringValue(mapped["name"])
		if name == "" {
			continue
		}
		result[name] = stringValue(mapped["value"])
	}
	return result
}

func harBody(requestData map[string]any) string {
	if postData, ok := requestData["postData"].(map[string]any); ok {
		if text := bodyValue(postData["text"]); text != "" {
			return text
		}
	}
	return ""
}
