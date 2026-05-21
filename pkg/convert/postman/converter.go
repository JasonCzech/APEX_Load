package postman

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/dsl"
)

type ImportRequest struct {
	Name      string         `json:"name,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Content   string         `json:"content"`
	Options   ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	ScriptName         string `json:"script_name,omitempty"`
	BaseURL            string `json:"base_url,omitempty"`
	IncludeAssertions  *bool  `json:"include_assertions,omitempty"`
	SanitizeSensitive  *bool  `json:"sanitize_sensitive,omitempty"`
	DefaultThinkTimeMs int    `json:"default_think_time_ms,omitempty"`
}

type ConvertStats struct {
	Format            string   `json:"format"`
	CollectionVersion string   `json:"collection_version,omitempty"`
	Folders           int      `json:"folders"`
	TotalRequests     int      `json:"total_requests"`
	ConvertedActions  int      `json:"converted_actions"`
	VariablesDetected int      `json:"variables_detected"`
	AuthSchemes       []string `json:"auth_schemes,omitempty"`
	UnsupportedBodies int      `json:"unsupported_bodies"`
	Assertions        int      `json:"assertions"`
	SkippedItems      []string `json:"skipped_items,omitempty"`
}

type ReviewSummary struct {
	CollectionName string   `json:"collection_name,omitempty"`
	BaseURL        string   `json:"base_url,omitempty"`
	Variables      []string `json:"variables,omitempty"`
	Requests       []string `json:"requests,omitempty"`
	AuthSchemes    []string `json:"auth_schemes,omitempty"`
}

type ConvertResult struct {
	Script   *dsl.Script   `json:"script"`
	Warnings []string      `json:"warnings,omitempty"`
	Stats    ConvertStats  `json:"stats"`
	Summary  ReviewSummary `json:"summary"`
}

type collection struct {
	Info     collectionInfo `json:"info"`
	Item     []item         `json:"item"`
	Variable []variable     `json:"variable"`
	Auth     *auth          `json:"auth"`
}

type collectionInfo struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type item struct {
	Name    string     `json:"name"`
	Item    []item     `json:"item"`
	Request requestRaw `json:"request"`
}

type requestRaw struct {
	Method string     `json:"method"`
	Header []header   `json:"header"`
	URL    postmanURL `json:"url"`
	Body   *body      `json:"body"`
	Auth   *auth      `json:"auth"`
	raw    string
}

type header struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Disabled bool   `json:"disabled"`
}

type body struct {
	Mode       string          `json:"mode"`
	Raw        string          `json:"raw"`
	URLEncoded []keyValueEntry `json:"urlencoded"`
	FormData   []keyValueEntry `json:"formdata"`
	GraphQL    *graphqlBody    `json:"graphql"`
}

type keyValueEntry struct {
	Key      string `json:"key"`
	Value    string `json:"value"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled"`
}

type graphqlBody struct {
	Query     string `json:"query"`
	Variables string `json:"variables"`
}

type variable struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Type     string `json:"type"`
	Disabled bool   `json:"disabled"`
}

type auth struct {
	Type   string          `json:"type"`
	Bearer []keyValueEntry `json:"bearer"`
	Basic  []keyValueEntry `json:"basic"`
	APIKey []keyValueEntry `json:"apikey"`
}

type postmanURL struct {
	Raw      string          `json:"raw"`
	Protocol string          `json:"protocol"`
	Host     []string        `json:"host"`
	Path     []string        `json:"path"`
	Query    []keyValueEntry `json:"query"`
}

func (u *postmanURL) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		u.Raw = raw
		return nil
	}
	type alias postmanURL
	var parsed alias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*u = postmanURL(parsed)
	return nil
}

func (r *requestRaw) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err == nil {
		r.Method = "GET"
		r.URL = postmanURL{Raw: raw}
		r.raw = raw
		return nil
	}
	type alias requestRaw
	var parsed alias
	if err := json.Unmarshal(data, &parsed); err != nil {
		return err
	}
	*r = requestRaw(parsed)
	return nil
}

type requestItem struct {
	Name    string
	Folder  string
	Request requestRaw
}

var variablePattern = regexp.MustCompile(`\{\{\s*([A-Za-z0-9_.-]+)\s*\}\}`)

func Convert(req ImportRequest) (*ConvertResult, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errors.New("content is required")
	}

	var col collection
	if err := json.Unmarshal([]byte(content), &col); err != nil {
		return nil, fmt.Errorf("invalid Postman collection: %w", err)
	}
	if len(col.Item) == 0 {
		return nil, errors.New("Postman collection contains no items")
	}

	requests := []requestItem{}
	folders := 0
	collectItems(col.Item, "", &requests, &folders)
	if len(requests) == 0 {
		return nil, errors.New("Postman collection contains no requests")
	}

	vars := collectionVariables(col.Variable)
	baseURL, warnings := deriveBaseURL(requests, vars, req.Options.BaseURL)
	scriptName := sanitizeName(firstNonEmpty(req.Options.ScriptName, req.Name, col.Info.Name, "Imported_Postman_Collection"))
	authSchemes := sortedAuthSchemes(requests, col.Auth)

	result := &ConvertResult{
		Script: &dsl.Script{
			Name:          scriptName,
			Version:       "1.0.0",
			Protocol:      "http",
			GeneratedBy:   "apexload-postman-converter",
			GeneratedFrom: "postman",
			CreatedAt:     time.Now(),
			Config: dsl.ScriptConfig{
				BaseURL: baseURL,
				ThinkTime: dsl.ThinkTimeConfig{
					Type:   "fixed",
					MeanMs: float64(firstPositive(req.Options.DefaultThinkTimeMs, 1000)),
				},
				Pacing:        dsl.PacingConfig{Type: "none"},
				RunLogic:      dsl.RunLogicConfig{Iterations: 0, AutoTransactions: "none"},
				ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false},
				Log:           dsl.LogConfig{Level: "standard"},
				HTTP:          dsl.HTTPConfig{DefaultHeaders: map[string]string{"Accept": "application/json"}},
			},
			Metadata: &dsl.ScriptMetadata{Import: &dsl.ImportMetadata{
				Source:           "postman",
				SourceScriptName: col.Info.Name,
				Preserved: map[string]any{
					"collection_schema": col.Info.Schema,
				},
			}},
		},
		Warnings: warnings,
		Stats: ConvertStats{
			Format:            "postman",
			CollectionVersion: strings.TrimPrefix(col.Info.Schema, "https://schema.getpostman.com/json/collection/"),
			Folders:           folders,
			TotalRequests:     len(requests),
			AuthSchemes:       authSchemes,
		},
		Summary: ReviewSummary{
			CollectionName: col.Info.Name,
			BaseURL:        baseURL,
			AuthSchemes:    authSchemes,
		},
	}

	result.Script.Parameters = buildParameters(vars, baseURL)
	result.Stats.VariablesDetected = len(result.Script.Parameters)
	result.Summary.Variables = parameterNames(result.Script.Parameters)

	sanitizeSensitive := boolOption(req.Options.SanitizeSensitive, true)
	includeAssertions := boolOption(req.Options.IncludeAssertions, true)
	for index, item := range requests {
		action, actionWarnings := buildAction(index, item, baseURL, vars, sanitizeSensitive, includeAssertions)
		if action.Name == "" {
			result.Stats.SkippedItems = append(result.Stats.SkippedItems, item.Name)
			continue
		}
		result.Warnings = append(result.Warnings, actionWarnings...)
		if action.Body == "" && item.Request.Body != nil && unsupportedBodyMode(item.Request.Body.Mode) {
			result.Stats.UnsupportedBodies++
		}
		if len(action.Assertions) > 0 {
			result.Stats.Assertions += len(action.Assertions)
		}
		result.Script.Actions = append(result.Script.Actions, action)
		result.Summary.Requests = append(result.Summary.Requests, action.Method+" "+action.Target)
		result.Stats.ConvertedActions++
	}
	if len(result.Script.Actions) == 0 {
		return nil, errors.New("Postman collection contains no supported requests")
	}
	return result, nil
}

func collectItems(items []item, folder string, requests *[]requestItem, folders *int) {
	for _, item := range items {
		if len(item.Item) > 0 {
			nextFolder := strings.TrimSpace(item.Name)
			if folder != "" && nextFolder != "" {
				nextFolder = folder + " / " + nextFolder
			} else if nextFolder == "" {
				nextFolder = folder
			}
			(*folders)++
			collectItems(item.Item, nextFolder, requests, folders)
			continue
		}
		if item.Request.URL.Raw == "" && item.Request.raw == "" && item.Request.Method == "" {
			continue
		}
		*requests = append(*requests, requestItem{Name: item.Name, Folder: folder, Request: item.Request})
	}
}

func collectionVariables(vars []variable) map[string]string {
	out := map[string]string{}
	for _, v := range vars {
		if v.Disabled || strings.TrimSpace(v.Key) == "" {
			continue
		}
		out[v.Key] = strings.TrimSpace(fmt.Sprint(v.Value))
	}
	return out
}

func deriveBaseURL(requests []requestItem, vars map[string]string, override string) (string, []string) {
	if strings.TrimSpace(override) != "" {
		return strings.TrimRight(strings.TrimSpace(override), "/"), nil
	}
	for _, key := range []string{"baseUrl", "base_url", "host", "url"} {
		if value := strings.TrimSpace(vars[key]); looksAbsoluteURL(value) {
			return strings.TrimRight(value, "/"), nil
		}
	}
	for _, item := range requests {
		raw := expandVariables(rawURL(item.Request.URL), vars)
		parsed, err := url.Parse(raw)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			return parsed.Scheme + "://" + parsed.Host, nil
		}
	}
	return "", []string{"base URL could not be derived automatically; imported request targets may remain absolute or variable-based"}
}

func buildParameters(vars map[string]string, baseURL string) []dsl.Parameter {
	keys := make([]string, 0, len(vars))
	for key := range vars {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	params := make([]dsl.Parameter, 0, len(keys))
	for _, key := range keys {
		value := vars[key]
		if value == "" || (looksAbsoluteURL(value) && strings.TrimRight(value, "/") == strings.TrimRight(baseURL, "/")) {
			continue
		}
		params = append(params, dsl.Parameter{
			Name:   normalizeVariableName(key),
			Type:   "list",
			Update: "once",
			Values: []string{value},
		})
	}
	return params
}

func buildAction(index int, item requestItem, baseURL string, vars map[string]string, sanitizeSensitive bool, includeAssertions bool) (dsl.Action, []string) {
	req := item.Request
	method := strings.ToUpper(firstNonEmpty(req.Method, "GET"))
	raw := rawURL(req.URL)
	target := toTarget(raw, baseURL, vars)
	headers := map[string]string{}
	for _, h := range req.Header {
		if h.Disabled || strings.TrimSpace(h.Key) == "" {
			continue
		}
		if sanitizeSensitive && sensitiveHeader(h.Key) {
			headers[h.Key] = "{{var." + normalizeVariableName(h.Key) + "}}"
			continue
		}
		headers[h.Key] = rewriteVariables(h.Value)
	}

	bodyText, contentType, warnings := requestBody(req.Body)
	if contentType != "" && headerValue(headers, "Content-Type") == "" {
		headers["Content-Type"] = contentType
	}
	if sanitizeSensitive {
		bodyText = sanitizeBody(bodyText)
	}
	bodyText = rewriteVariables(bodyText)

	action := dsl.Action{
		Name:    actionName(index, item),
		Type:    "request",
		Method:  method,
		Target:  target,
		Headers: headers,
		Body:    bodyText,
		Metadata: map[string]any{
			"postman_item": item.Name,
		},
	}
	if item.Folder != "" {
		action.Metadata["postman_folder"] = item.Folder
	}
	if len(action.Headers) == 0 {
		action.Headers = nil
	}
	if includeAssertions {
		action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code", Expected: 200})
	}
	return action, warnings
}

func rawURL(u postmanURL) string {
	if strings.TrimSpace(u.Raw) != "" {
		return strings.TrimSpace(u.Raw)
	}
	host := strings.Join(u.Host, ".")
	path := strings.Join(u.Path, "/")
	if host == "" && path == "" {
		return ""
	}
	prefix := ""
	if u.Protocol != "" && host != "" {
		prefix = u.Protocol + "://"
	}
	raw := prefix + host
	if path != "" {
		raw += "/" + path
	}
	query := url.Values{}
	for _, q := range u.Query {
		if q.Disabled || q.Key == "" {
			continue
		}
		query.Add(q.Key, q.Value)
	}
	if encoded := query.Encode(); encoded != "" {
		raw += "?" + encoded
	}
	return raw
}

func toTarget(raw string, baseURL string, vars map[string]string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	expanded := expandBaseURLVariables(raw, vars, baseURL)
	if strings.TrimSpace(baseURL) != "" {
		base := strings.TrimRight(baseURL, "/")
		if strings.HasPrefix(expanded, base+"/") {
			return rewriteVariables(strings.TrimPrefix(expanded, base))
		}
		if expanded == base {
			return "/"
		}
	}
	if parsed, err := url.Parse(expanded); err == nil && parsed.Scheme != "" && parsed.Host != "" {
		target := parsed.EscapedPath()
		if target == "" {
			target = "/"
		}
		if parsed.RawQuery != "" {
			target += "?" + parsed.RawQuery
		}
		return rewriteVariables(target)
	}
	if strings.HasPrefix(raw, "{{") {
		return rewriteVariables(raw)
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return rewriteVariables(raw)
}

func requestBody(b *body) (string, string, []string) {
	if b == nil {
		return "", "", nil
	}
	switch strings.ToLower(strings.TrimSpace(b.Mode)) {
	case "", "raw":
		return b.Raw, detectContentType(b.Raw), nil
	case "urlencoded":
		values := url.Values{}
		for _, entry := range b.URLEncoded {
			if entry.Disabled || entry.Key == "" {
				continue
			}
			values.Add(entry.Key, entry.Value)
		}
		return values.Encode(), "application/x-www-form-urlencoded", nil
	case "formdata":
		values := map[string]string{}
		warnings := []string{"Postman form-data body was flattened to JSON for review; file upload parts require manual validation before replay"}
		for _, entry := range b.FormData {
			if entry.Disabled || entry.Key == "" {
				continue
			}
			if strings.EqualFold(entry.Type, "file") {
				values[entry.Key] = "{{var." + normalizeVariableName(entry.Key) + "_file}}"
				continue
			}
			values[entry.Key] = entry.Value
		}
		data, _ := json.MarshalIndent(values, "", "  ")
		return string(data), "application/json", warnings
	case "graphql":
		payload := map[string]any{}
		if b.GraphQL != nil {
			payload["query"] = b.GraphQL.Query
			if strings.TrimSpace(b.GraphQL.Variables) != "" {
				var decoded any
				if err := json.Unmarshal([]byte(b.GraphQL.Variables), &decoded); err == nil {
					payload["variables"] = decoded
				} else {
					payload["variables"] = b.GraphQL.Variables
				}
			}
		}
		data, _ := json.MarshalIndent(payload, "", "  ")
		return string(data), "application/json", nil
	default:
		return "", "", []string{fmt.Sprintf("Postman body mode %q is not natively converted; add the payload manually before replay", b.Mode)}
	}
}

func unsupportedBodyMode(mode string) bool {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case "", "raw", "urlencoded", "formdata", "graphql":
		return false
	default:
		return true
	}
}

func sortedAuthSchemes(requests []requestItem, collectionAuth *auth) []string {
	seen := map[string]struct{}{}
	if collectionAuth != nil && collectionAuth.Type != "" {
		seen[collectionAuth.Type] = struct{}{}
	}
	for _, item := range requests {
		if item.Request.Auth != nil && item.Request.Auth.Type != "" {
			seen[item.Request.Auth.Type] = struct{}{}
		}
	}
	out := make([]string, 0, len(seen))
	for key := range seen {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func parameterNames(params []dsl.Parameter) []string {
	out := make([]string, 0, len(params))
	for _, param := range params {
		out = append(out, param.Name)
	}
	return out
}

func expandVariables(value string, vars map[string]string) string {
	return variablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := variablePattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		if replacement, ok := vars[matches[1]]; ok && replacement != "" {
			return replacement
		}
		return token
	})
}

func expandBaseURLVariables(value string, vars map[string]string, baseURL string) string {
	base := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	return variablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := variablePattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		name := matches[1]
		replacement := strings.TrimSpace(vars[name])
		if replacement == "" {
			return token
		}
		if looksAbsoluteURL(replacement) && (base == "" || strings.TrimRight(replacement, "/") == base) {
			return strings.TrimRight(replacement, "/")
		}
		if base != "" && isBaseURLVariableName(name) {
			return base
		}
		return token
	})
}

func rewriteVariables(value string) string {
	return variablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := variablePattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		name := strings.TrimSpace(matches[1])
		if isApexExpressionName(name) {
			return "{{" + name + "}}"
		}
		return "{{var." + normalizeVariableName(name) + "}}"
	})
}

func normalizeVariableName(value string) string {
	value = strings.TrimSpace(value)
	value = strings.Trim(value, "{}")
	value = strings.ReplaceAll(value, "-", "_")
	value = strings.ReplaceAll(value, ".", "_")
	value = strings.ReplaceAll(value, " ", "_")
	return value
}

func headerValue(headers map[string]string, key string) string {
	for existingKey, value := range headers {
		if strings.EqualFold(existingKey, key) {
			return value
		}
	}
	return ""
}

func sensitiveHeader(key string) bool {
	normalized := strings.ToLower(strings.TrimSpace(key))
	return normalized == "authorization" || normalized == "proxy-authorization" || normalized == "cookie" || normalized == "x-api-key" || normalized == "api-key"
}

func sanitizeBody(value string) string {
	if strings.TrimSpace(value) == "" {
		return value
	}
	var decoded any
	if err := json.Unmarshal([]byte(value), &decoded); err != nil {
		return value
	}
	changed := sanitizeJSONValue(decoded)
	if !changed {
		return value
	}
	data, err := json.MarshalIndent(decoded, "", "  ")
	if err != nil {
		return value
	}
	return string(data)
}

func sanitizeJSONValue(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		changed := false
		for key, nested := range typed {
			if sensitiveJSONKey(key) {
				typed[key] = "{{var." + normalizeVariableName(key) + "}}"
				changed = true
				continue
			}
			if sanitizeJSONValue(nested) {
				changed = true
			}
		}
		return changed
	case []any:
		changed := false
		for _, nested := range typed {
			if sanitizeJSONValue(nested) {
				changed = true
			}
		}
		return changed
	default:
		return false
	}
}

func sensitiveJSONKey(key string) bool {
	key = strings.ToLower(key)
	return strings.Contains(key, "password") || strings.Contains(key, "token") || strings.Contains(key, "secret") || strings.Contains(key, "apikey") || strings.Contains(key, "api_key")
}

func detectContentType(body string) string {
	trimmed := strings.TrimSpace(body)
	if trimmed == "" {
		return ""
	}
	if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
		return "application/json"
	}
	if strings.HasPrefix(trimmed, "<") {
		return "application/xml"
	}
	return ""
}

func actionName(index int, item requestItem) string {
	name := firstNonEmpty(item.Name, fmt.Sprintf("Request %d", index+1))
	if item.Folder != "" {
		name = item.Folder + " " + name
	}
	return sanitizeName(name)
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "Postman_Request"
	}
	replacer := strings.NewReplacer("/", " ", "\\", " ", ":", " ", "\t", " ", "\n", " ")
	value = replacer.Replace(value)
	fields := strings.Fields(value)
	if len(fields) == 0 {
		return "Postman_Request"
	}
	return strings.Join(fields, "_")
}

func looksAbsoluteURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}

func isBaseURLVariableName(value string) bool {
	switch strings.ToLower(strings.ReplaceAll(strings.TrimSpace(value), "_", "")) {
	case "baseurl", "host", "url":
		return true
	default:
		return false
	}
}

func isApexExpressionName(value string) bool {
	for _, prefix := range []string{"var.", "env.", "data.", "fn."} {
		if strings.HasPrefix(value, prefix) {
			return true
		}
	}
	return false
}

func boolOption(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
