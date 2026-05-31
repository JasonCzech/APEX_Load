package openapi

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/dsl"
	"gopkg.in/yaml.v3"
)

type ImportRequest struct {
	Name      string         `json:"name,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Content   string         `json:"content"`
	Options   ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	ScriptName         string   `json:"script_name,omitempty"`
	BaseURL            string   `json:"base_url,omitempty"`
	IncludeAssertions  *bool    `json:"include_assertions,omitempty"`
	OperationIDs       []string `json:"operation_ids,omitempty"`
	Tags               []string `json:"tags,omitempty"`
	DefaultThinkTimeMs int      `json:"default_think_time_ms,omitempty"`
}

type ConvertStats struct {
	Format              string   `json:"format"`
	OpenAPIVersion      string   `json:"openapi_version,omitempty"`
	TotalPaths          int      `json:"total_paths"`
	TotalOperations     int      `json:"total_operations"`
	ConvertedActions    int      `json:"converted_actions"`
	RequestBodies       int      `json:"request_bodies"`
	Assertions          int      `json:"assertions"`
	ParametersDetected  int      `json:"parameters_detected"`
	AuthSchemesDetected []string `json:"auth_schemes_detected,omitempty"`
	SkippedOperations   []string `json:"skipped_operations,omitempty"`
}

type ReviewSummary struct {
	Title          string   `json:"title,omitempty"`
	Version        string   `json:"version,omitempty"`
	BaseURL        string   `json:"base_url,omitempty"`
	Servers        []string `json:"servers,omitempty"`
	AuthSchemes    []string `json:"auth_schemes,omitempty"`
	DataColumns    []string `json:"data_columns,omitempty"`
	OperationNames []string `json:"operation_names,omitempty"`
}

type ConvertResult struct {
	Script      *dsl.Script      `json:"script"`
	Warnings    []string         `json:"warnings,omitempty"`
	Unsupported diag.Diagnostics `json:"unsupported,omitempty"`
	Stats       ConvertStats     `json:"stats"`
	Summary     ReviewSummary    `json:"summary"`
}

type document struct {
	OpenAPI     string                `json:"openapi,omitempty" yaml:"openapi,omitempty"`
	Swagger     string                `json:"swagger,omitempty" yaml:"swagger,omitempty"`
	Info        info                  `json:"info,omitempty" yaml:"info,omitempty"`
	Servers     []server              `json:"servers,omitempty" yaml:"servers,omitempty"`
	Schemes     []string              `json:"schemes,omitempty" yaml:"schemes,omitempty"`
	Host        string                `json:"host,omitempty" yaml:"host,omitempty"`
	BasePath    string                `json:"basePath,omitempty" yaml:"basePath,omitempty"`
	Paths       map[string]pathItem   `json:"paths,omitempty" yaml:"paths,omitempty"`
	Components  components            `json:"components,omitempty" yaml:"components,omitempty"`
	Security    []map[string][]string `json:"security,omitempty" yaml:"security,omitempty"`
	Definitions map[string]schema     `json:"definitions,omitempty" yaml:"definitions,omitempty"`
}

type info struct {
	Title   string `json:"title,omitempty" yaml:"title,omitempty"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

type server struct {
	URL string `json:"url,omitempty" yaml:"url,omitempty"`
}

type components struct {
	SecuritySchemes map[string]securityScheme `json:"securitySchemes,omitempty" yaml:"securitySchemes,omitempty"`
	Schemas         map[string]schema         `json:"schemas,omitempty" yaml:"schemas,omitempty"`
	Parameters      map[string]parameter      `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RequestBodies   map[string]requestBody    `json:"requestBodies,omitempty" yaml:"requestBodies,omitempty"`
}

type securityScheme struct {
	Type         string `json:"type,omitempty" yaml:"type,omitempty"`
	Scheme       string `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Name         string `json:"name,omitempty" yaml:"name,omitempty"`
	In           string `json:"in,omitempty" yaml:"in,omitempty"`
	BearerFormat string `json:"bearerFormat,omitempty" yaml:"bearerFormat,omitempty"`
}

type pathItem struct {
	Parameters []parameter `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	Get        *operation  `json:"get,omitempty" yaml:"get,omitempty"`
	Post       *operation  `json:"post,omitempty" yaml:"post,omitempty"`
	Put        *operation  `json:"put,omitempty" yaml:"put,omitempty"`
	Patch      *operation  `json:"patch,omitempty" yaml:"patch,omitempty"`
	Delete     *operation  `json:"delete,omitempty" yaml:"delete,omitempty"`
	Options    *operation  `json:"options,omitempty" yaml:"options,omitempty"`
	Head       *operation  `json:"head,omitempty" yaml:"head,omitempty"`
	Trace      *operation  `json:"trace,omitempty" yaml:"trace,omitempty"`
}

type operation struct {
	OperationID string                `json:"operationId,omitempty" yaml:"operationId,omitempty"`
	Summary     string                `json:"summary,omitempty" yaml:"summary,omitempty"`
	Description string                `json:"description,omitempty" yaml:"description,omitempty"`
	Tags        []string              `json:"tags,omitempty" yaml:"tags,omitempty"`
	Parameters  []parameter           `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RequestBody *requestBody          `json:"requestBody,omitempty" yaml:"requestBody,omitempty"`
	Responses   map[string]any        `json:"responses,omitempty" yaml:"responses,omitempty"`
	Security    []map[string][]string `json:"security,omitempty" yaml:"security,omitempty"`
}

type parameter struct {
	Ref      string `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	In       string `json:"in,omitempty" yaml:"in,omitempty"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
	Schema   schema `json:"schema,omitempty" yaml:"schema,omitempty"`
	Example  any    `json:"example,omitempty" yaml:"example,omitempty"`
}

type requestBody struct {
	Ref      string               `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Required bool                 `json:"required,omitempty" yaml:"required,omitempty"`
	Content  map[string]mediaType `json:"content,omitempty" yaml:"content,omitempty"`
}

type mediaType struct {
	Schema  schema `json:"schema,omitempty" yaml:"schema,omitempty"`
	Example any    `json:"example,omitempty" yaml:"example,omitempty"`
}

type schema struct {
	Ref           string            `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Type          string            `json:"type,omitempty" yaml:"type,omitempty"`
	Format        string            `json:"format,omitempty" yaml:"format,omitempty"`
	Properties    map[string]schema `json:"properties,omitempty" yaml:"properties,omitempty"`
	Items         *schema           `json:"items,omitempty" yaml:"items,omitempty"`
	Example       any               `json:"example,omitempty" yaml:"example,omitempty"`
	Enum          []any             `json:"enum,omitempty" yaml:"enum,omitempty"`
	OneOf         []schema          `json:"oneOf,omitempty" yaml:"oneOf,omitempty"`
	AnyOf         []schema          `json:"anyOf,omitempty" yaml:"anyOf,omitempty"`
	AllOf         []schema          `json:"allOf,omitempty" yaml:"allOf,omitempty"`
	Discriminator map[string]any    `json:"discriminator,omitempty" yaml:"discriminator,omitempty"`
}

var supportedMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true,
}

func Convert(req ImportRequest) (*ConvertResult, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errors.New("content is required")
	}
	doc, err := decodeDocument([]byte(content))
	if err != nil {
		return nil, err
	}
	if len(doc.Paths) == 0 {
		return nil, errors.New("OpenAPI document contains no paths")
	}

	var diagnostics diag.Collector
	res := resolver{doc: doc, diagnostics: &diagnostics}
	baseURL, warnings := deriveBaseURL(doc, req.Options.BaseURL)
	scriptName := firstNonEmpty(req.Options.ScriptName, req.Name, doc.Info.Title, "Imported_OpenAPI_Script")
	result := &ConvertResult{
		Script: &dsl.Script{
			Name:          sanitizeName(scriptName),
			Version:       "1.0.0",
			Protocol:      "http",
			GeneratedBy:   "apexload-openapi-converter",
			GeneratedFrom: "openapi",
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
				HTTP: dsl.HTTPConfig{
					DefaultHeaders: map[string]string{"Accept": "application/json"},
				},
			},
		},
		Warnings: warnings,
		Stats: ConvertStats{
			Format:         "openapi",
			OpenAPIVersion: firstNonEmpty(doc.OpenAPI, doc.Swagger),
			TotalPaths:     len(doc.Paths),
		},
		Summary: ReviewSummary{
			Title:   doc.Info.Title,
			Version: doc.Info.Version,
			BaseURL: baseURL,
			Servers: serverURLs(doc),
		},
	}

	authSchemes := sortedAuthSchemeNames(doc.Components.SecuritySchemes)
	result.Stats.AuthSchemesDetected = authSchemes
	result.Summary.AuthSchemes = authSchemes
	applyAuthDefaults(result.Script, doc.Components.SecuritySchemes, &result.Warnings)

	columns := map[string]struct{}{}
	operationNames := []string{}
	for _, path := range sortedPathKeys(doc.Paths) {
		item := doc.Paths[path]
		for _, candidate := range operationsForPath(item) {
			result.Stats.TotalOperations++
			if !supportedMethods[candidate.method] || !operationAllowed(candidate.op, req.Options) {
				skipped := strings.ToUpper(candidate.method) + " " + path
				result.Stats.SkippedOperations = append(result.Stats.SkippedOperations, skipped)
				diagnostics.Add(diag.Warning, "OpenAPI operation skipped", skipped, "operation was not converted because it was filtered or uses an unsupported HTTP method")
				continue
			}
			action, bodyColumns, assertionAdded := buildAction(path, candidate.method, item.Parameters, candidate.op, boolOption(req.Options.IncludeAssertions, true), res)
			result.Script.Actions = append(result.Script.Actions, action)
			result.Stats.ConvertedActions++
			result.Stats.ParametersDetected += len(item.Parameters) + len(candidate.op.Parameters)
			if action.Body != "" {
				result.Stats.RequestBodies++
			}
			if assertionAdded {
				result.Stats.Assertions++
			}
			operationNames = append(operationNames, action.Name)
			for _, name := range bodyColumns {
				columns[name] = struct{}{}
			}
			for _, param := range append(item.Parameters, candidate.op.Parameters...) {
				if param.Name != "" {
					columns[sanitizeColumnName(param.Name)] = struct{}{}
				}
			}
		}
	}

	if len(result.Script.Actions) == 0 {
		return nil, errors.New("no supported OpenAPI operations found")
	}
	result.Summary.OperationNames = operationNames
	dataColumns := sortedMapKeys(columns)
	result.Summary.DataColumns = dataColumns
	if len(dataColumns) > 0 {
		result.Script.DataSources = []dsl.DataSource{openAPIDataSource(dataColumns)}
	}
	if result.Script.Config.BaseURL == "" {
		result.Warnings = append(result.Warnings, "base URL could not be derived; set options.base_url before saving the script")
	}
	if gap := result.Stats.TotalOperations - result.Stats.ConvertedActions - len(result.Stats.SkippedOperations); gap > 0 {
		diagnostics.Add(diag.Warning, "OpenAPI completeness guard", "document", fmt.Sprintf("%d operation(s) were present but not converted or explicitly skipped", gap))
	}
	result.Unsupported = diagnostics.Items()
	return result, nil
}

func decodeDocument(data []byte) (*document, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err == nil && (doc.OpenAPI != "" || doc.Swagger != "" || len(doc.Paths) > 0) {
		return &doc, nil
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("invalid OpenAPI document: %w", err)
	}
	if doc.OpenAPI == "" && doc.Swagger == "" && len(doc.Paths) == 0 {
		return nil, errors.New("invalid OpenAPI document: missing openapi/swagger version or paths")
	}
	return &doc, nil
}

func deriveBaseURL(doc *document, override string) (string, []string) {
	if value := strings.TrimSpace(override); value != "" {
		return strings.TrimRight(value, "/"), nil
	}
	for _, srv := range doc.Servers {
		if value := strings.TrimSpace(srv.URL); value != "" {
			if strings.Contains(value, "{") {
				value = regexp.MustCompile(`\{[^}]+\}`).ReplaceAllString(value, "example")
			}
			return strings.TrimRight(value, "/"), nil
		}
	}
	if doc.Host != "" {
		scheme := "https"
		if len(doc.Schemes) > 0 && doc.Schemes[0] != "" {
			scheme = doc.Schemes[0]
		}
		return strings.TrimRight(scheme+"://"+doc.Host+"/"+strings.Trim(doc.BasePath, "/"), "/"), nil
	}
	return "", []string{"OpenAPI server URL was not provided; imported targets remain relative"}
}

type operationCandidate struct {
	method string
	op     *operation
}

func operationsForPath(item pathItem) []operationCandidate {
	candidates := []operationCandidate{
		{method: "get", op: item.Get},
		{method: "post", op: item.Post},
		{method: "put", op: item.Put},
		{method: "patch", op: item.Patch},
		{method: "delete", op: item.Delete},
		{method: "options", op: item.Options},
		{method: "head", op: item.Head},
		{method: "trace", op: item.Trace},
	}
	result := make([]operationCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.op != nil {
			result = append(result, candidate)
		}
	}
	return result
}

func operationAllowed(op *operation, opts ConvertOptions) bool {
	if len(opts.OperationIDs) > 0 {
		allowed := stringSet(opts.OperationIDs)
		return allowed[op.OperationID]
	}
	if len(opts.Tags) > 0 {
		allowed := stringSet(opts.Tags)
		for _, tag := range op.Tags {
			if allowed[tag] {
				return true
			}
		}
		return false
	}
	return true
}

type resolver struct {
	doc         *document
	diagnostics *diag.Collector
}

func (r resolver) add(sev diag.Severity, element, location, detail string) {
	if r.diagnostics != nil {
		r.diagnostics.Add(sev, element, location, detail)
	}
}

func (r resolver) resolveParameter(param parameter, location string) parameter {
	ref := strings.TrimSpace(param.Ref)
	if ref == "" {
		return param
	}
	if value, ok := r.localParameter(ref); ok {
		return value
	}
	r.add(diag.Warning, "OpenAPI parameter $ref", location, "parameter reference "+ref+" could not be resolved; parameter was not expanded")
	return parameter{}
}

func (r resolver) resolveRequestBody(body *requestBody, location string) *requestBody {
	if body == nil {
		return nil
	}
	ref := strings.TrimSpace(body.Ref)
	if ref == "" {
		return body
	}
	if value, ok := r.localRequestBody(ref); ok {
		return &value
	}
	r.add(diag.Warning, "OpenAPI requestBody $ref", location, "requestBody reference "+ref+" could not be resolved; request payload was left empty")
	return nil
}

func (r resolver) resolveSchema(s schema, location string, seen map[string]bool) schema {
	if seen == nil {
		seen = map[string]bool{}
	}
	if ref := strings.TrimSpace(s.Ref); ref != "" {
		if seen[ref] {
			r.add(diag.Warning, "OpenAPI schema $ref", location, "schema reference cycle detected at "+ref+"; using an empty object placeholder")
			return schema{Type: "object"}
		}
		seen[ref] = true
		resolved, ok := r.localSchema(ref)
		if !ok {
			r.add(diag.Warning, "OpenAPI schema $ref", location, "schema reference "+ref+" could not be resolved; external refs are not inlined")
			return schema{Type: "object"}
		}
		return r.resolveSchema(resolved, location, seen)
	}
	if len(s.Discriminator) > 0 {
		r.add(diag.Warning, "OpenAPI discriminator", location, "schema discriminator is not modeled; generated sample uses the statically selected shape")
	}
	if len(s.AllOf) > 0 {
		r.add(diag.Warning, "OpenAPI allOf", location, "allOf composition was merged where possible; review generated payload fields")
		merged := s
		merged.AllOf = nil
		if merged.Properties == nil {
			merged.Properties = map[string]schema{}
		}
		for _, member := range s.AllOf {
			resolved := r.resolveSchema(member, location, copySeen(seen))
			for name, prop := range resolved.Properties {
				merged.Properties[name] = prop
			}
			if merged.Type == "" {
				merged.Type = resolved.Type
			}
		}
		return merged
	}
	if len(s.OneOf) > 0 {
		r.add(diag.Warning, "OpenAPI oneOf", location, "oneOf composition was reduced to the first variant for sample generation")
		return r.resolveSchema(s.OneOf[0], location, copySeen(seen))
	}
	if len(s.AnyOf) > 0 {
		r.add(diag.Warning, "OpenAPI anyOf", location, "anyOf composition was reduced to the first variant for sample generation")
		return r.resolveSchema(s.AnyOf[0], location, copySeen(seen))
	}
	if s.Items != nil {
		item := r.resolveSchema(*s.Items, location+"[]", copySeen(seen))
		s.Items = &item
	}
	if len(s.Properties) > 0 {
		props := make(map[string]schema, len(s.Properties))
		for name, prop := range s.Properties {
			props[name] = r.resolveSchema(prop, location+"."+name, copySeen(seen))
		}
		s.Properties = props
	}
	return s
}

func (r resolver) localSchema(ref string) (schema, bool) {
	if r.doc == nil {
		return schema{}, false
	}
	switch {
	case strings.HasPrefix(ref, "#/components/schemas/"):
		name := refName(ref, "#/components/schemas/")
		value, ok := r.doc.Components.Schemas[name]
		return value, ok
	case strings.HasPrefix(ref, "#/definitions/"):
		name := refName(ref, "#/definitions/")
		value, ok := r.doc.Definitions[name]
		return value, ok
	default:
		return schema{}, false
	}
}

func (r resolver) localParameter(ref string) (parameter, bool) {
	if r.doc == nil || !strings.HasPrefix(ref, "#/components/parameters/") {
		return parameter{}, false
	}
	value, ok := r.doc.Components.Parameters[refName(ref, "#/components/parameters/")]
	return value, ok
}

func (r resolver) localRequestBody(ref string) (requestBody, bool) {
	if r.doc == nil || !strings.HasPrefix(ref, "#/components/requestBodies/") {
		return requestBody{}, false
	}
	value, ok := r.doc.Components.RequestBodies[refName(ref, "#/components/requestBodies/")]
	return value, ok
}

func refName(ref, prefix string) string {
	name := strings.TrimPrefix(ref, prefix)
	name = strings.ReplaceAll(name, "~1", "/")
	name = strings.ReplaceAll(name, "~0", "~")
	return name
}

func copySeen(input map[string]bool) map[string]bool {
	out := make(map[string]bool, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}

func buildAction(path, method string, pathParams []parameter, op *operation, includeAssertion bool, res resolver) (dsl.Action, []string, bool) {
	params := append([]parameter{}, pathParams...)
	params = append(params, op.Parameters...)
	location := strings.ToUpper(method) + " " + path
	for i := range params {
		params[i] = res.resolveParameter(params[i], location)
	}
	target := templatePath(path, params)
	headers := map[string]string{"Accept": "application/json"}
	applyHeaderAndCookieParams(headers, params)
	body, contentType, bodyColumns := requestBodyExample(res.resolveRequestBody(op.RequestBody, location), res, location)
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	action := dsl.Action{
		Name:    sanitizeName(firstNonEmpty(op.OperationID, op.Summary, strings.ToUpper(method)+"_"+path)),
		Type:    "request",
		Method:  strings.ToUpper(method),
		Target:  target,
		Headers: headers,
		Body:    body,
		Metadata: map[string]any{
			"openapi_operation_id": op.OperationID,
			"openapi_tags":         op.Tags,
		},
	}
	if includeAssertion {
		if status, ok := firstSuccessStatus(op.Responses); ok {
			action.Assertions = []dsl.Assertion{{Type: "status_code", Expected: status}}
			return action, bodyColumns, true
		}
	}
	return action, bodyColumns, false
}

func requestBodyExample(body *requestBody, res resolver, location string) (string, string, []string) {
	if body == nil || len(body.Content) == 0 {
		return "", "", nil
	}
	contentTypes := sortedMapKeys(body.Content)
	preferred := preferredContentType(contentTypes)
	media := body.Content[preferred]
	if media.Example != nil {
		return renderExample(media.Example, preferred), preferred, nil
	}
	if media.Schema.Example != nil {
		return renderExample(media.Schema.Example, preferred), preferred, nil
	}
	if strings.Contains(preferred, "application/x-www-form-urlencoded") {
		return formURLEncodedExample(media.Schema, res, location), preferred, formColumns(media.Schema, res, location)
	}
	if strings.Contains(preferred, "multipart/form-data") {
		body, columns := multipartFormExample(media.Schema, res, location)
		contentType := preferred
		if !strings.Contains(strings.ToLower(contentType), "boundary=") {
			contentType += "; boundary=----apexload-openapi-boundary"
		}
		return body, contentType, columns
	}
	payload, columns := sampleForSchema(media.Schema, res, location, map[string]bool{})
	if payload == nil {
		return "", preferred, nil
	}
	if strings.Contains(preferred, "xml") {
		return "<request/>", preferred, columns
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", preferred, columns
	}
	return string(encoded), preferred, columns
}

func sampleForSchema(s schema, res resolver, location string, seen map[string]bool) (any, []string) {
	s = res.resolveSchema(s, location, seen)
	if len(s.Properties) > 0 || s.Type == "object" {
		result := map[string]any{}
		columns := []string{}
		for _, name := range sortedSchemaPropertyKeys(s.Properties) {
			prop := res.resolveSchema(s.Properties[name], location+"."+name, copySeen(seen))
			value, nested := sampleForSchema(prop, res, location+"."+name, copySeen(seen))
			column := sanitizeColumnName(name)
			if scalarSchema(prop) {
				value = "{{data.openapi." + column + "}}"
				columns = append(columns, column)
			}
			result[name] = value
			columns = append(columns, nested...)
		}
		return result, uniqueSorted(columns)
	}
	if s.Items != nil || s.Type == "array" {
		value, columns := sampleForSchema(valueOrEmptySchema(s.Items), res, location+"[]", copySeen(seen))
		return []any{value}, columns
	}
	switch strings.ToLower(s.Type) {
	case "integer", "number":
		return 1, nil
	case "boolean":
		return true, nil
	case "string":
		if len(s.Enum) > 0 {
			return s.Enum[0], nil
		}
		if s.Format == "date-time" {
			return "{{fn.now()}}", nil
		}
		return "value", nil
	default:
		return map[string]any{}, nil
	}
}

func formURLEncodedExample(s schema, res resolver, location string) string {
	values := url.Values{}
	for _, field := range schemaFieldNames(s, res, location) {
		values.Set(field, "{{data.openapi."+sanitizeColumnName(field)+"}}")
	}
	return values.Encode()
}

func formColumns(s schema, res resolver, location string) []string {
	fields := schemaFieldNames(s, res, location)
	columns := make([]string, 0, len(fields))
	for _, field := range fields {
		columns = append(columns, sanitizeColumnName(field))
	}
	return uniqueSorted(columns)
}

func multipartFormExample(s schema, res resolver, location string) (string, []string) {
	const boundary = "----apexload-openapi-boundary"
	fields := res.resolveSchema(s, location, map[string]bool{})
	var b strings.Builder
	columns := []string{}
	for _, name := range sortedSchemaPropertyKeys(fields.Properties) {
		prop := res.resolveSchema(fields.Properties[name], location+"."+name, map[string]bool{})
		column := sanitizeColumnName(name)
		columns = append(columns, column)
		b.WriteString("--" + boundary + "\r\n")
		if strings.EqualFold(prop.Format, "binary") {
			b.WriteString(`Content-Disposition: form-data; name="` + name + `"; filename="{{data.openapi.` + column + `}}"` + "\r\n")
			b.WriteString("Content-Type: application/octet-stream\r\n\r\n")
			b.WriteString("{{file." + column + "}}\r\n")
			res.add(diag.Warning, "OpenAPI multipart file", location, "property "+name+" is format: binary; file bytes cannot be generated from the spec and must be supplied before replay")
			continue
		}
		b.WriteString(`Content-Disposition: form-data; name="` + name + `"` + "\r\n\r\n")
		b.WriteString("{{data.openapi." + column + "}}\r\n")
	}
	if len(columns) == 0 {
		return "", nil
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String(), uniqueSorted(columns)
}

func schemaFieldNames(s schema, res resolver, location string) []string {
	s = res.resolveSchema(s, location, map[string]bool{})
	if len(s.Properties) == 0 {
		return nil
	}
	return sortedSchemaPropertyKeys(s.Properties)
}

func renderExample(value any, contentType string) string {
	if strings.Contains(contentType, "json") {
		encoded, err := json.MarshalIndent(value, "", "  ")
		if err == nil {
			return string(encoded)
		}
	}
	return fmt.Sprint(value)
}

func preferredContentType(contentTypes []string) string {
	for _, candidate := range []string{"application/json", "application/*+json", "application/x-www-form-urlencoded", "multipart/form-data", "text/xml", "application/xml"} {
		for _, contentType := range contentTypes {
			if contentType == candidate || (candidate == "application/*+json" && strings.HasSuffix(contentType, "+json")) {
				return contentType
			}
		}
	}
	if len(contentTypes) == 0 {
		return ""
	}
	return contentTypes[0]
}

func firstSuccessStatus(responses map[string]any) (int, bool) {
	if len(responses) == 0 {
		return 0, false
	}
	keys := sortedMapKeys(responses)
	for _, key := range keys {
		if len(key) == 3 && key[0] == '2' {
			var status int
			if _, err := fmt.Sscanf(key, "%d", &status); err == nil {
				return status, true
			}
		}
	}
	if _, ok := responses["default"]; ok {
		return 200, true
	}
	return 0, false
}

func templatePath(path string, params []parameter) string {
	target := path
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		template := "{{data.openapi." + sanitizeColumnName(name) + "}}"
		if strings.EqualFold(param.In, "path") {
			target = strings.ReplaceAll(target, "{"+name+"}", template)
			continue
		}
		if strings.EqualFold(param.In, "query") {
			separator := "?"
			if strings.Contains(target, "?") {
				separator = "&"
			}
			target += separator + url.QueryEscape(name) + "=" + template
		}
	}
	return target
}

func applyHeaderAndCookieParams(headers map[string]string, params []parameter) {
	var cookies []string
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		template := "{{data.openapi." + sanitizeColumnName(name) + "}}"
		switch strings.ToLower(strings.TrimSpace(param.In)) {
		case "header":
			headers[name] = template
		case "cookie":
			cookies = append(cookies, name+"="+template)
		}
	}
	if len(cookies) == 0 {
		return
	}
	if existing := headerValue(headers, "Cookie"); existing != "" {
		cookies = append([]string{existing}, cookies...)
	}
	headers["Cookie"] = strings.Join(cookies, "; ")
}

func headerValue(headers map[string]string, key string) string {
	for existingKey, value := range headers {
		if strings.EqualFold(existingKey, key) {
			return value
		}
	}
	return ""
}

func applyAuthDefaults(script *dsl.Script, schemes map[string]securityScheme, warnings *[]string) {
	if len(schemes) == 0 {
		return
	}
	keys := sortedMapKeys(schemes)
	for _, key := range keys {
		scheme := schemes[key]
		switch strings.ToLower(scheme.Type) {
		case "http":
			switch strings.ToLower(scheme.Scheme) {
			case "bearer":
				script.Config.HTTP.Auth = dsl.AuthConfig{Type: "bearer", Token: "{{env.API_TOKEN}}"}
				return
			case "basic":
				script.Config.HTTP.Auth = dsl.AuthConfig{Type: "basic", Username: "{{env.API_USERNAME}}", Password: "{{env.API_PASSWORD}}"}
				return
			}
		case "apikey":
			if strings.EqualFold(scheme.In, "header") && scheme.Name != "" {
				if script.Config.HTTP.DefaultHeaders == nil {
					script.Config.HTTP.DefaultHeaders = map[string]string{}
				}
				script.Config.HTTP.DefaultHeaders[scheme.Name] = "{{env.API_KEY}}"
				return
			}
		case "oauth2", "openidconnect":
			*warnings = append(*warnings, "OAuth/OpenID Connect security schemes require environment-provided bearer tokens before execution")
		}
	}
}

func openAPIDataSource(columns []string) dsl.DataSource {
	values := make([]string, len(columns))
	for i, column := range columns {
		values[i] = sampleColumnValue(column)
	}
	return dsl.DataSource{
		Name:           "openapi",
		Type:           "csv",
		InlineData:     strings.Join(columns, ",") + "\n" + strings.Join(values, ",") + "\n",
		Strategy:       "sequential",
		Delimiter:      ",",
		FirstRowHeader: true,
		Columns:        columns,
	}
}

func sampleColumnValue(column string) string {
	lower := strings.ToLower(column)
	switch {
	case strings.Contains(lower, "id"):
		return "sample-id"
	case strings.Contains(lower, "limit") || strings.Contains(lower, "count"):
		return "10"
	case strings.Contains(lower, "email"):
		return "user@example.test"
	default:
		return "sample-" + column
	}
}

func serverURLs(doc *document) []string {
	values := make([]string, 0, len(doc.Servers))
	for _, server := range doc.Servers {
		if strings.TrimSpace(server.URL) != "" {
			values = append(values, strings.TrimSpace(server.URL))
		}
	}
	if doc.Host != "" {
		values = append(values, strings.TrimRight(firstNonEmpty(firstScheme(doc.Schemes), "https")+"://"+doc.Host+"/"+strings.Trim(doc.BasePath, "/"), "/"))
	}
	return values
}

func firstScheme(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func sortedPathKeys(paths map[string]pathItem) []string {
	keys := make([]string, 0, len(paths))
	for key := range paths {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedSchemaPropertyKeys(properties map[string]schema) []string {
	keys := make([]string, 0, len(properties))
	for key := range properties {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys[V any](values map[string]V) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedAuthSchemeNames(schemes map[string]securityScheme) []string {
	keys := sortedMapKeys(schemes)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		scheme := schemes[key]
		label := key
		if scheme.Type != "" {
			label += ":" + scheme.Type
		}
		if scheme.Scheme != "" {
			label += "/" + scheme.Scheme
		}
		result = append(result, label)
	}
	return result
}

func uniqueSorted(values []string) []string {
	seen := map[string]struct{}{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			seen[value] = struct{}{}
		}
	}
	return sortedMapKeys(seen)
}

func stringSet(values []string) map[string]bool {
	result := map[string]bool{}
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			result[strings.TrimSpace(value)] = true
		}
	}
	return result
}

func scalarSchema(s schema) bool {
	return len(s.Properties) == 0 && s.Items == nil
}

func valueOrEmptySchema(value *schema) schema {
	if value == nil {
		return schema{}
	}
	return *value
}

func sanitizeColumnName(value string) string {
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(`[^A-Za-z0-9_]+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "value"
	}
	return strings.ToLower(value)
}

func sanitizeName(value string) string {
	value = strings.TrimSpace(value)
	value = regexp.MustCompile(`[^A-Za-z0-9_]+`).ReplaceAllString(value, "_")
	value = strings.Trim(value, "_")
	if value == "" {
		return "Imported_OpenAPI_Script"
	}
	return value
}

func boolOption(value *bool, fallback bool) bool {
	if value == nil {
		return fallback
	}
	return *value
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
