package postman

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/convert/tmpl"
	"github.com/jasonczech/apexload/pkg/dsl"
)

type ImportRequest struct {
	Name               string         `json:"name,omitempty"`
	ProjectID          string         `json:"project_id,omitempty"`
	Content            string         `json:"content"`
	EnvironmentContent string         `json:"environment_content,omitempty"`
	Options            ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	ScriptName          string `json:"script_name,omitempty"`
	BaseURL             string `json:"base_url,omitempty"`
	IncludeAssertions   *bool  `json:"include_assertions,omitempty"`
	SanitizeSensitive   *bool  `json:"sanitize_sensitive,omitempty"`
	UseParameterDataSet *bool  `json:"use_parameter_data_source,omitempty"`
	DefaultThinkTimeMs  int    `json:"default_think_time_ms,omitempty"`
}

type ConvertStats struct {
	Format            string   `json:"format"`
	CollectionVersion string   `json:"collection_version,omitempty"`
	Folders           int      `json:"folders"`
	TotalRequests     int      `json:"total_requests"`
	ConvertedActions  int      `json:"converted_actions"`
	VariablesDetected int      `json:"variables_detected"`
	EnvironmentValues int      `json:"environment_values,omitempty"`
	AuthSchemes       []string `json:"auth_schemes,omitempty"`
	UnsupportedBodies int      `json:"unsupported_bodies"`
	Assertions        int      `json:"assertions"`
	Extractors        int      `json:"extractors,omitempty"`
	Correlations      int      `json:"correlations,omitempty"`
	SkippedItems      []string `json:"skipped_items,omitempty"`
}

// Correlation records a response→request chain detected from a Postman test
// script (pm.<scope>.set("var", pm.response...)). It mirrors the OpenAPI
// importer's Correlation so the API layer can feed both through paramize.
type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ExtractType string `json:"extract_type"`
	SampleValue string `json:"sample_value,omitempty"`
	FoundInStep int    `json:"found_in_step"`
	UsedInSteps []int  `json:"used_in_steps"`
}

type ReviewSummary struct {
	CollectionName string   `json:"collection_name,omitempty"`
	BaseURL        string   `json:"base_url,omitempty"`
	Variables      []string `json:"variables,omitempty"`
	Requests       []string `json:"requests,omitempty"`
	AuthSchemes    []string `json:"auth_schemes,omitempty"`
}

type ConvertResult struct {
	Script       *dsl.Script        `json:"script"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Correlations []Correlation      `json:"correlations,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	Summary      ReviewSummary      `json:"summary"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`
}

type collection struct {
	Info     collectionInfo `json:"info"`
	Item     []item         `json:"item"`
	Variable []variable     `json:"variable"`
	Auth     *auth          `json:"auth"`
	Event    []event        `json:"event"`
}

type collectionInfo struct {
	Name   string `json:"name"`
	Schema string `json:"schema"`
}

type item struct {
	Name    string     `json:"name"`
	Item    []item     `json:"item"`
	Request requestRaw `json:"request"`
	Event   []event    `json:"event"`
}

type event struct {
	Listen string      `json:"listen"`
	Script eventScript `json:"script"`
}

type eventScript struct {
	Type string   `json:"type"`
	Exec []string `json:"exec"`
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

type environmentFile struct {
	Name   string                `json:"name"`
	Values []environmentVariable `json:"values"`
}

type environmentVariable struct {
	Key      string `json:"key"`
	Value    any    `json:"value"`
	Enabled  *bool  `json:"enabled"`
	Disabled bool   `json:"disabled"`
}

type auth struct {
	Type   string          `json:"type"`
	Bearer []keyValueEntry `json:"bearer"`
	Basic  []keyValueEntry `json:"basic"`
	Digest []keyValueEntry `json:"digest"`
	NTLM   []keyValueEntry `json:"ntlm"`
	APIKey []keyValueEntry `json:"apikey"`
	OAuth2 []keyValueEntry `json:"oauth2"`
	AWSV4  []keyValueEntry `json:"awsv4"`
	Hawk   []keyValueEntry `json:"hawk"`
}

type postmanURL struct {
	Raw      string          `json:"raw"`
	Protocol string          `json:"protocol"`
	Host     []string        `json:"host"`
	Path     []string        `json:"path"`
	Query    []keyValueEntry `json:"query"`
	Variable []keyValueEntry `json:"variable"`
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
	Events  []event
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
	collectItems(col.Item, "", col.Event, &requests, &folders)
	if len(requests) == 0 {
		return nil, errors.New("Postman collection contains no requests")
	}

	var diagnostics diag.Collector
	sourceFileName := "postman_collection.json"
	sources := map[string]string{sourceFileName: content}
	sourceMappings := make([]sourceview.Mapping, 0)
	vars := collectionVariables(col.Variable)
	envVars := environmentVariables(req.EnvironmentContent, &diagnostics)
	for key, value := range envVars {
		vars[key] = value
	}
	applyPostmanEventVariables(vars, requests, &diagnostics)
	baseURL, warnings := deriveBaseURL(requests, vars, req.Options.BaseURL)
	scriptName := sanitizeName(convertutil.FirstNonEmpty(req.Options.ScriptName, req.Name, col.Info.Name, "Imported_Postman_Collection"))
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
					"collection_schema":  col.Info.Schema,
					"environment_values": len(envVars),
				},
			}},
		},
		Warnings: warnings,
		Stats: ConvertStats{
			Format:            "postman",
			CollectionVersion: strings.TrimPrefix(col.Info.Schema, "https://schema.getpostman.com/json/collection/"),
			Folders:           folders,
			TotalRequests:     len(requests),
			EnvironmentValues: len(envVars),
			AuthSchemes:       authSchemes,
		},
		Summary: ReviewSummary{
			CollectionName: col.Info.Name,
			BaseURL:        baseURL,
			AuthSchemes:    authSchemes,
		},
	}

	result.Script.Parameters = buildParameters(vars, baseURL)
	result.Script.Parameters = mergeParameters(result.Script.Parameters, pathVariableParameters(requests))
	if convertutil.BoolOption(req.Options.UseParameterDataSet, true) {
		result.Script.Parameters, result.Script.DataSources = postmanParameterDataSource(result.Script.Parameters)
	}
	result.Stats.VariablesDetected = len(result.Script.Parameters)
	result.Summary.Variables = parameterNames(result.Script.Parameters)
	reportUnsupportedPostmanAuth(col.Auth, &diagnostics, "collection")

	sanitizeSensitive := convertutil.BoolOption(req.Options.SanitizeSensitive, true)
	includeAssertions := convertutil.BoolOption(req.Options.IncludeAssertions, true)
	chainItems := make([]postmanChainItem, 0, len(requests))
	for index, item := range requests {
		reportPostmanEvents(item, &diagnostics)

		// Extract JS scripts to virtual review files
		for i, ev := range item.Events {
			if len(ev.Script.Exec) == 0 {
				continue
			}
			scriptContent := strings.Join(ev.Script.Exec, "\n")
			listenType := convertutil.FirstNonEmpty(ev.Listen, "script")
			filename := fmt.Sprintf("%s_%s_%d.js", sanitizeName(item.Name), listenType, i)
			sources[filename] = scriptContent
			sourceMappings = append(sourceMappings, sourceview.Mapping{
				SourceFile: filename,
				StartLine:  1,
				EndLine:    len(ev.Script.Exec),
				Function:   postmanItemLocation(item) + " / " + listenType,
				TargetKind: "review",
				Status:     "review_only",
				Notes:      fmt.Sprintf("Postman %s script preserved for manual review and translation.", listenType),
			})
		}

		action, actionWarnings := buildAction(index, item, col.Auth, baseURL, vars, sanitizeSensitive, includeAssertions, &diagnostics)
		if action.Name == "" {
			result.Stats.SkippedItems = append(result.Stats.SkippedItems, item.Name)
			diagnostics.Add(diag.Warning, "Postman item skipped", postmanItemLocation(item), "request item could not be converted into an executable action")
			sourceMappings = append(sourceMappings, postmanSourceMapping(sourceFileName, content, item, "", postmanItemLocation(item), "request", "review_only", "Postman item could not be converted into an executable action."))
			continue
		}
		result.Warnings = append(result.Warnings, actionWarnings...)
		if action.Body == "" && item.Request.Body != nil && unsupportedBodyMode(item.Request.Body.Mode) {
			result.Stats.UnsupportedBodies++
		}
		if len(action.Assertions) > 0 {
			result.Stats.Assertions += len(action.Assertions)
		}
		actionIndex := len(result.Script.Actions)
		result.Script.Actions = append(result.Script.Actions, action)
		chainItems = append(chainItems, postmanChainItem{ActionIndex: actionIndex, Item: item})
		sourceMappings = append(sourceMappings, postmanSourceMapping(sourceFileName, content, item, fmt.Sprintf("actions[%d]", actionIndex), action.Name, "action", "converted", "Postman request was converted into a generated APEX Load action."))
		result.Summary.Requests = append(result.Summary.Requests, action.Method+" "+action.Target)
		result.Stats.ConvertedActions++
	}
	if len(result.Script.Actions) == 0 {
		return nil, errors.New("Postman collection contains no supported requests")
	}
	// Turn pm.<scope>.set("var", pm.response...) test-script assignments into
	// response extractors on the producing action so later {{var.X}} references
	// resolve at replay (Postman's response→request chaining idiom).
	result.Correlations = applyPostmanResponseChaining(result.Script, chainItems, &diagnostics)
	result.Stats.Correlations = len(result.Correlations)
	for i := range result.Script.Actions {
		result.Stats.Extractors += len(result.Script.Actions[i].Extractors)
	}
	if gap := result.Stats.TotalRequests - result.Stats.ConvertedActions - len(result.Stats.SkippedItems); gap > 0 {
		diagnostics.Add(diag.Warning, "Postman completeness guard", "collection", fmt.Sprintf("%d request item(s) were present but not converted or explicitly skipped", gap))
	}
	result.Unsupported = diagnostics.Items()
	result.SourceReview = sourceview.Build(sources, sourceMappings)
	return result, nil
}

func postmanSourceMapping(sourceFile string, content string, item requestItem, targetPath string, targetName string, targetKind string, status string, notes string) sourceview.Mapping {
	line := postmanItemLine(content, item)
	return sourceview.Mapping{
		SourceFile: sourceFile,
		StartLine:  line,
		EndLine:    line,
		Function:   postmanItemLocation(item),
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     status,
		Notes:      notes,
	}
}

func postmanItemLine(content string, item requestItem) int {
	for _, needle := range []string{`"` + item.Name + `"`, item.Request.URL.Raw, item.Request.Method} {
		if strings.TrimSpace(needle) == "" {
			continue
		}
		if offset := strings.Index(content, needle); offset >= 0 {
			return sourceview.LineForOffset(content, offset)
		}
	}
	return 1
}

func collectItems(items []item, folder string, inheritedEvents []event, requests *[]requestItem, folders *int) {
	for _, item := range items {
		events := append([]event{}, inheritedEvents...)
		events = append(events, item.Event...)
		if len(item.Item) > 0 {
			nextFolder := strings.TrimSpace(item.Name)
			if folder != "" && nextFolder != "" {
				nextFolder = folder + " / " + nextFolder
			} else if nextFolder == "" {
				nextFolder = folder
			}
			(*folders)++
			collectItems(item.Item, nextFolder, events, requests, folders)
			continue
		}
		if item.Request.URL.Raw == "" && item.Request.raw == "" && item.Request.Method == "" {
			continue
		}
		*requests = append(*requests, requestItem{Name: item.Name, Folder: folder, Request: item.Request, Events: events})
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

func environmentVariables(content string, diagnostics *diag.Collector) map[string]string {
	out := map[string]string{}
	content = strings.TrimSpace(content)
	if content == "" {
		return out
	}
	var env environmentFile
	if err := json.Unmarshal([]byte(content), &env); err != nil {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman environment", "environment", "environment file could not be parsed: "+err.Error())
		}
		return out
	}
	for _, v := range env.Values {
		if v.Disabled || strings.TrimSpace(v.Key) == "" {
			continue
		}
		if v.Enabled != nil && !*v.Enabled {
			continue
		}
		out[v.Key] = strings.TrimSpace(fmt.Sprint(v.Value))
	}
	if len(out) > 0 && diagnostics != nil {
		diagnostics.Add(diag.Info, "Postman environment", convertutil.FirstNonEmpty(env.Name, "environment"), fmt.Sprintf("imported %d environment value(s)", len(out)))
	}
	return out
}

func postmanParameterDataSource(params []dsl.Parameter) ([]dsl.Parameter, []dsl.DataSource) {
	if len(params) == 0 {
		return params, nil
	}
	columns := make([]string, 0, len(params))
	values := make([]string, 0, len(params))
	seenColumns := map[string]struct{}{}
	for _, param := range params {
		if dsl.CanonicalParameterType(param.Type) != "list" || len(param.Values) == 0 {
			continue
		}
		column := normalizeVariableName(param.Name)
		if strings.TrimSpace(column) == "" || strings.Contains(param.Values[0], "{{") {
			continue
		}
		if _, seen := seenColumns[column]; seen {
			continue
		}
		seenColumns[column] = struct{}{}
		columns = append(columns, column)
		values = append(values, param.Values[0])
	}
	if len(columns) == 0 {
		return params, nil
	}

	var buf bytes.Buffer
	writer := csv.NewWriter(&buf)
	_ = writer.Write(columns)
	_ = writer.Write(values)
	writer.Flush()

	next := make([]dsl.Parameter, len(params))
	for i, param := range params {
		next[i] = param
		column := normalizeVariableName(param.Name)
		if containsString(columns, column) {
			next[i].Values = nil
			next[i].DataSource = "postman"
			next[i].Column = column
			next[i].Selection = "sequential"
		}
	}
	return next, []dsl.DataSource{{
		Name:           "postman",
		Type:           "csv",
		InlineData:     buf.String(),
		Strategy:       "sequential",
		Delimiter:      ",",
		FirstRowHeader: true,
		Columns:        columns,
	}}
}

func containsString(values []string, candidate string) bool {
	for _, value := range values {
		if value == candidate {
			return true
		}
	}
	return false
}

var postmanSetPattern = regexp.MustCompile(`pm\.(?:environment|collectionVariables)\.set\s*\(\s*["']([^"']+)["']\s*,\s*["']([^"']*)["']\s*\)`)

func applyPostmanEventVariables(vars map[string]string, requests []requestItem, diagnostics *diag.Collector) {
	for _, req := range requests {
		location := postmanItemLocation(req)
		for _, ev := range req.Events {
			for _, line := range ev.Script.Exec {
				for _, match := range postmanSetPattern.FindAllStringSubmatch(line, -1) {
					if len(match) < 3 {
						continue
					}
					key := strings.TrimSpace(match[1])
					if key == "" {
						continue
					}
					vars[key] = match[2]
					diagnostics.Add(diag.Info, "Postman script variable", location, "translated deterministic pm variable assignment for "+key)
				}
			}
		}
	}
}

func reportPostmanEvents(item requestItem, diagnostics *diag.Collector) {
	location := postmanItemLocation(item)
	for _, ev := range item.Events {
		listen := strings.ToLower(strings.TrimSpace(ev.Listen))
		if listen == "" {
			continue
		}
		switch listen {
		case "prerequest":
			diagnostics.Add(diag.Warning, "Postman pre-request script", location, "pre-request script is not executed during import; review auth signing, variable mutation, and correlation logic before replay")
		case "test":
			diagnostics.Add(diag.Warning, "Postman test script", location, "test script is not executed during import; translate assertions or side effects before replay")
		default:
			diagnostics.Add(diag.Info, "Postman event script", location, "event script "+listen+" is not executed during import")
		}
	}
}

func postmanItemLocation(item requestItem) string {
	if item.Folder != "" && item.Name != "" {
		return item.Folder + " / " + item.Name
	}
	return convertutil.FirstNonEmpty(item.Name, item.Folder, "Postman request")
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
			Values: []string{tmpl.TranslateDynamic(value, nil, "collection variable "+key)},
		})
	}
	return params
}

func pathVariableParameters(requests []requestItem) []dsl.Parameter {
	values := map[string]string{}
	for _, item := range requests {
		for _, variable := range item.Request.URL.Variable {
			if variable.Disabled || strings.TrimSpace(variable.Key) == "" {
				continue
			}
			values[normalizeVariableName(variable.Key)] = convertutil.FirstNonEmpty(variable.Value, "sample-"+normalizeVariableName(variable.Key))
		}
		for _, name := range pathVariableNames(rawURL(item.Request.URL)) {
			if _, ok := values[normalizeVariableName(name)]; !ok {
				values[normalizeVariableName(name)] = "sample-" + normalizeVariableName(name)
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	params := make([]dsl.Parameter, 0, len(keys))
	for _, key := range keys {
		params = append(params, dsl.Parameter{Name: key, Type: "list", Update: "once", Values: []string{values[key]}})
	}
	return params
}

func mergeParameters(existing []dsl.Parameter, extra []dsl.Parameter) []dsl.Parameter {
	if len(extra) == 0 {
		return existing
	}
	seen := map[string]struct{}{}
	out := append([]dsl.Parameter{}, existing...)
	for _, param := range existing {
		seen[strings.ToLower(param.Name)] = struct{}{}
	}
	for _, param := range extra {
		if _, ok := seen[strings.ToLower(param.Name)]; ok {
			continue
		}
		out = append(out, param)
		seen[strings.ToLower(param.Name)] = struct{}{}
	}
	return out
}

func buildAction(index int, item requestItem, collectionAuth *auth, baseURL string, vars map[string]string, sanitizeSensitive bool, includeAssertions bool, diagnostics *diag.Collector) (dsl.Action, []string) {
	req := item.Request
	location := postmanItemLocation(item)
	method := strings.ToUpper(convertutil.FirstNonEmpty(req.Method, "GET"))
	raw := rawURL(req.URL)
	target := toTarget(raw, baseURL, vars, diagnostics, location)
	headers := map[string]string{}
	for _, h := range req.Header {
		if h.Disabled || strings.TrimSpace(h.Key) == "" {
			continue
		}
		if sanitizeSensitive && sensitiveHeader(h.Key) && !containsTemplateToken(h.Value) {
			// Only redact hard-coded secrets; a value that references a variable
			// (e.g. "Bearer {{token}}", incl. a chained/extracted token) is not a
			// literal secret and must be preserved so the chain resolves at replay.
			headers[h.Key] = "{{env." + sensitiveEnvName(h.Key) + "}}"
			continue
		}
		headers[h.Key] = rewriteVariablesWithContext(h.Value, vars, diagnostics, location)
	}

	bodyText, contentType, warnings := requestBody(req.Body, diagnostics, location)
	if contentType != "" && headerValue(headers, "Content-Type") == "" {
		headers["Content-Type"] = contentType
	}
	if sanitizeSensitive {
		bodyText = sanitizeBody(bodyText)
	}
	bodyText = rewriteVariablesWithContext(bodyText, vars, diagnostics, location)

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
	applyPostmanAuth(&action, req.Auth, collectionAuth, vars, sanitizeSensitive, diagnostics, location)
	if len(action.Headers) == 0 {
		action.Headers = nil
	}
	if includeAssertions {
		action.Assertions = append(action.Assertions, dsl.Assertion{Type: "status_code_range", Min: 200, Max: 299})
	}
	return action, warnings
}

func rawURL(u postmanURL) string {
	if strings.TrimSpace(u.Raw) != "" {
		raw := strings.TrimSpace(u.Raw)
		if len(u.Query) > 0 {
			return reconcileRawQuery(raw, u.Query)
		}
		return raw
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
	if encoded := postmanQueryString(u.Query); encoded != "" {
		raw += "?" + encoded
	}
	return raw
}

func reconcileRawQuery(raw string, entries []keyValueEntry) string {
	query := postmanQueryString(entries)
	if query == "" {
		if before, _, ok := strings.Cut(raw, "?"); ok {
			return before
		}
		return raw
	}
	before, _, ok := strings.Cut(raw, "?")
	if !ok {
		return raw + "?" + query
	}
	return before + "?" + query
}

func postmanQueryString(entries []keyValueEntry) string {
	parts := make([]string, 0, len(entries))
	for _, q := range entries {
		key := strings.TrimSpace(q.Key)
		if q.Disabled || key == "" {
			continue
		}
		if q.Value == "" {
			parts = append(parts, key)
			continue
		}
		parts = append(parts, key+"="+q.Value)
	}
	return strings.Join(parts, "&")
}

func toTarget(raw string, baseURL string, vars map[string]string, diagnostics *diag.Collector, location string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "/"
	}
	expanded := expandBaseURLVariables(raw, vars, baseURL)
	if strings.TrimSpace(baseURL) != "" {
		base := strings.TrimRight(baseURL, "/")
		if strings.HasPrefix(expanded, base+"/") {
			return rewriteVariablesWithContext(rewritePathVariables(strings.TrimPrefix(expanded, base)), vars, diagnostics, location)
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
		return rewriteVariablesWithContext(rewritePathVariables(target), vars, diagnostics, location)
	}
	if strings.HasPrefix(raw, "{{") {
		return rewriteVariablesWithContext(raw, vars, diagnostics, location)
	}
	if !strings.HasPrefix(raw, "/") {
		raw = "/" + raw
	}
	return rewriteVariablesWithContext(rewritePathVariables(raw), vars, diagnostics, location)
}

func requestBody(b *body, diagnostics *diag.Collector, location string) (string, string, []string) {
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
			values.Add(entry.Key, tmpl.TranslateDynamic(entry.Value, diagnostics, location))
		}
		return values.Encode(), "application/x-www-form-urlencoded", nil
	case "formdata":
		values := map[string]string{}
		diagnostics.Add(diag.Warning, "Postman form-data", location, "form-data body was flattened to JSON for review; multipart replay and file upload parts require manual validation")
		for _, entry := range b.FormData {
			if entry.Disabled || entry.Key == "" {
				continue
			}
			if strings.EqualFold(entry.Type, "file") {
				values[entry.Key] = "{{var." + normalizeVariableName(entry.Key) + "_file}}"
				diagnostics.Add(diag.Warning, "Postman form-data file", location, "file part "+entry.Key+" cannot be seeded from the collection JSON; provide a governed file parameter before replay")
				continue
			}
			values[entry.Key] = tmpl.TranslateDynamic(entry.Value, diagnostics, location)
		}
		data, _ := json.MarshalIndent(values, "", "  ")
		return string(data), "application/json", nil
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
		diagnostics.Add(diag.Warning, "Postman body mode", location, fmt.Sprintf("body mode %q is not natively converted; add the payload manually before replay", b.Mode))
		return "", "", nil
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

func reportUnsupportedPostmanAuth(value *auth, diagnostics *diag.Collector, location string) {
	if value == nil || diagnostics == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(value.Type)) {
	case "digest", "ntlm":
		diagnostics.Add(diag.Warning, "Postman auth", location, value.Type+" auth is not replayable by the HTTP runtime; it was preserved as review-only metadata and no runnable auth override was emitted")
	case "awsv4", "hawk":
		diagnostics.Add(diag.Warning, "Postman auth", location, value.Type+" request signing cannot be executed during import; translate the signing flow before replay")
	}
}

func applyPostmanAuth(action *dsl.Action, requestAuth *auth, collectionAuth *auth, vars map[string]string, sanitizeSensitive bool, diagnostics *diag.Collector, location string) {
	explicitRequestAuth := requestAuth != nil && strings.TrimSpace(requestAuth.Type) != ""
	if explicitRequestAuth && strings.EqualFold(strings.TrimSpace(requestAuth.Type), "noauth") {
		action.Auth = &dsl.AuthConfig{Type: "noauth"}
		return
	}

	effective := requestAuth
	inherited := false
	if effective == nil || strings.TrimSpace(effective.Type) == "" || strings.EqualFold(strings.TrimSpace(effective.Type), "inherit") {
		effective = collectionAuth
		inherited = true
	}
	if effective == nil {
		return
	}

	switch strings.ToLower(strings.TrimSpace(effective.Type)) {
	case "", "inherit", "noauth":
		return
	case "apikey":
		applyPostmanAPIKey(action, effective, vars, sanitizeSensitive, diagnostics, location)
	case "bearer", "basic", "oauth2":
		cfg := postmanAuthConfig(effective, vars, sanitizeSensitive, diagnostics, location)
		if strings.TrimSpace(cfg.Type) != "" {
			action.Auth = &cfg
		}
	case "digest", "ntlm", "awsv4", "hawk":
		if !inherited {
			reportUnsupportedPostmanAuth(effective, diagnostics, location)
		}
		if action.Metadata != nil {
			action.Metadata["postman_auth_review_only"] = effective.Type
		}
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "auth type "+effective.Type+" was not converted")
		}
		if action.Metadata != nil {
			action.Metadata["postman_auth_review_only"] = effective.Type
		}
	}
}

func applyPostmanAPIKey(action *dsl.Action, value *auth, vars map[string]string, sanitizeSensitive bool, diagnostics *diag.Collector, location string) {
	key := authValue(value.APIKey, "key", vars, diagnostics, location)
	secret := authCredentialValue(value.APIKey, "value", vars, diagnostics, location, sanitizeSensitive, "POSTMAN_API_KEY")
	placement := strings.ToLower(strings.TrimSpace(authValue(value.APIKey, "in", vars, diagnostics, location)))
	if strings.TrimSpace(key) == "" || strings.TrimSpace(secret) == "" {
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "API key auth is missing key, value, or placement and was not converted")
		}
		return
	}
	switch placement {
	case "query", "queryparam", "queryparams":
		action.Target = appendQueryParam(action.Target, key, secret)
	case "", "header":
		if action.Headers == nil {
			action.Headers = map[string]string{}
		}
		action.Headers[key] = secret
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "API key placement "+placement+" is not supported; converted as a header")
		}
		if action.Headers == nil {
			action.Headers = map[string]string{}
		}
		action.Headers[key] = secret
	}
}

func appendQueryParam(target string, key string, value string) string {
	sep := "?"
	if strings.Contains(target, "?") {
		sep = "&"
	}
	return target + sep + key + "=" + value
}

func postmanAuthConfig(value *auth, vars map[string]string, sanitizeSensitive bool, diagnostics *diag.Collector, location string) dsl.AuthConfig {
	if value == nil {
		return dsl.AuthConfig{}
	}
	switch strings.ToLower(strings.TrimSpace(value.Type)) {
	case "", "noauth", "inherit":
		return dsl.AuthConfig{}
	case "bearer":
		return dsl.AuthConfig{Type: "bearer", Token: convertutil.FirstNonEmpty(authCredentialValue(value.Bearer, "token", vars, diagnostics, location, sanitizeSensitive, "POSTMAN_BEARER_TOKEN"), "{{env.API_TOKEN}}")}
	case "basic":
		return dsl.AuthConfig{
			Type:     "basic",
			Username: convertutil.FirstNonEmpty(authValue(value.Basic, "username", vars, diagnostics, location), "{{env.API_USERNAME}}"),
			Password: convertutil.FirstNonEmpty(authCredentialValue(value.Basic, "password", vars, diagnostics, location, sanitizeSensitive, "POSTMAN_BASIC_PASSWORD"), "{{env.API_PASSWORD}}"),
		}
	case "oauth2":
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "OAuth2 auth was represented as a bearer token placeholder; review token acquisition before replay")
		}
		return dsl.AuthConfig{Type: "bearer", Token: convertutil.FirstNonEmpty(authCredentialValue(value.OAuth2, "accessToken", vars, diagnostics, location, sanitizeSensitive, "POSTMAN_OAUTH2_ACCESS_TOKEN"), authCredentialValue(value.OAuth2, "access_token", vars, diagnostics, location, sanitizeSensitive, "POSTMAN_OAUTH2_ACCESS_TOKEN"), "{{env.API_TOKEN}}")}
	default:
		if diagnostics != nil {
			diagnostics.Add(diag.Warning, "Postman auth", location, "auth type "+value.Type+" was not converted")
		}
		return dsl.AuthConfig{}
	}
}

func authValue(entries []keyValueEntry, key string, vars map[string]string, diagnostics *diag.Collector, location string) string {
	for _, entry := range entries {
		if entry.Disabled {
			continue
		}
		if strings.EqualFold(entry.Key, key) {
			return rewriteVariablesWithContext(entry.Value, vars, diagnostics, location)
		}
	}
	return ""
}

func authCredentialValue(entries []keyValueEntry, key string, vars map[string]string, diagnostics *diag.Collector, location string, sanitizeSensitive bool, envName string) string {
	value := authValue(entries, key, vars, diagnostics, location)
	if value == "" {
		return ""
	}
	if sanitizeSensitive && !strings.Contains(value, "{{") {
		return "{{env." + envName + "}}"
	}
	return value
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
	return rewriteVariablesWithDiagnostics(value, nil, "")
}

func rewriteVariablesWithDiagnostics(value string, diagnostics *diag.Collector, location string) string {
	return rewriteVariablesWithContext(value, nil, diagnostics, location)
}

func rewriteVariablesWithContext(value string, vars map[string]string, diagnostics *diag.Collector, location string) string {
	value = tmpl.TranslateDynamic(value, diagnostics, location)
	return variablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := variablePattern.FindStringSubmatch(token)
		if len(matches) != 2 {
			return token
		}
		name := strings.TrimSpace(matches[1])
		if isApexExpressionName(name) {
			return "{{" + name + "}}"
		}
		if replacement, ok := inlineRuntimeVariable(name, vars, diagnostics, location); ok {
			return replacement
		}
		return "{{var." + normalizeVariableName(name) + "}}"
	})
}

func inlineRuntimeVariable(name string, vars map[string]string, diagnostics *diag.Collector, location string) (string, bool) {
	if len(vars) == 0 {
		return "", false
	}
	value, ok := vars[name]
	if !ok {
		return "", false
	}
	translated := strings.TrimSpace(tmpl.TranslateDynamic(value, diagnostics, location))
	if !singleRuntimeExpression(translated) {
		return "", false
	}
	return translated, true
}

func singleRuntimeExpression(value string) bool {
	if !strings.HasPrefix(value, "{{") || !strings.HasSuffix(value, "}}") {
		return false
	}
	expr := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, "{{"), "}}"))
	return strings.HasPrefix(expr, "fn.") || strings.HasPrefix(expr, "fake.")
}

var pathVariablePattern = regexp.MustCompile(`(^|/):([A-Za-z][A-Za-z0-9_-]*)`)

func rewritePathVariables(value string) string {
	return pathVariablePattern.ReplaceAllStringFunc(value, func(token string) string {
		matches := pathVariablePattern.FindStringSubmatch(token)
		if len(matches) != 3 {
			return token
		}
		return matches[1] + "{{var." + normalizeVariableName(matches[2]) + "}}"
	})
}

func pathVariableNames(value string) []string {
	matches := pathVariablePattern.FindAllStringSubmatch(value, -1)
	if len(matches) == 0 {
		return nil
	}
	names := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 3 {
			names = append(names, match[2])
		}
	}
	return names
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

// containsTemplateToken reports whether a value references a template variable
// (e.g. "Bearer {{token}}"). Such values are not hard-coded secrets, so
// sanitization must preserve them rather than masking the whole value.
func containsTemplateToken(value string) bool {
	return strings.Contains(value, "{{") && strings.Contains(value, "}}")
}

func sensitiveEnvName(key string) string {
	normalized := strings.ToUpper(strings.TrimSpace(key))
	var builder strings.Builder
	lastUnderscore := false
	for _, r := range normalized {
		if (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			builder.WriteRune(r)
			lastUnderscore = false
			continue
		}
		if !lastUnderscore {
			builder.WriteByte('_')
			lastUnderscore = true
		}
	}
	out := strings.Trim(builder.String(), "_")
	if out == "" {
		out = "SECRET"
	}
	return "POSTMAN_" + out
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
	name := convertutil.FirstNonEmpty(item.Name, fmt.Sprintf("Request %d", index+1))
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

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
