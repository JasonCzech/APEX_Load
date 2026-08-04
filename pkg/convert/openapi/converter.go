package openapi

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/importreview"
	"github.com/jasonczech/apexload/pkg/convert/requestimport"
	"github.com/jasonczech/apexload/pkg/convert/sourceview"
	"github.com/jasonczech/apexload/pkg/convert/srcparse"
	"github.com/jasonczech/apexload/pkg/credential"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/scrub"
	"gopkg.in/yaml.v3"
)

type ImportRequest struct {
	Name      string         `json:"name,omitempty"`
	ProjectID string         `json:"project_id,omitempty"`
	Content   string         `json:"content"`
	Options   ConvertOptions `json:"options,omitempty"`
}

type ConvertOptions struct {
	ScriptName                   string   `json:"script_name,omitempty"`
	BaseURL                      string   `json:"base_url,omitempty"`
	IncludeAssertions            *bool    `json:"include_assertions,omitempty"`
	OperationIDs                 []string `json:"operation_ids,omitempty"`
	Tags                         []string `json:"tags,omitempty"`
	DefaultThinkTimeMs           int      `json:"default_think_time_ms,omitempty"`
	SynthesizeOAuthTokenInit     *bool    `json:"synthesize_oauth_token_init,omitempty"`
	SynthesizeOAuthPasswordGrant *bool    `json:"synthesize_oauth_password_grant,omitempty"`
}

type ConvertStats struct {
	Format              string   `json:"format"`
	OpenAPIVersion      string   `json:"openapi_version,omitempty"`
	TotalPaths          int      `json:"total_paths"`
	TotalOperations     int      `json:"total_operations"`
	ConvertedActions    int      `json:"converted_actions"`
	RequestBodies       int      `json:"request_bodies"`
	Assertions          int      `json:"assertions"`
	Extractors          int      `json:"extractors,omitempty"`
	Correlations        int      `json:"correlations,omitempty"`
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
	Script       *dsl.Script        `json:"script"`
	Warnings     []string           `json:"warnings,omitempty"`
	Unsupported  diag.Diagnostics   `json:"unsupported,omitempty"`
	Correlations []Correlation      `json:"correlations,omitempty"`
	Stats        ConvertStats       `json:"stats"`
	Summary      ReviewSummary      `json:"summary"`
	SourceReview *sourceview.Review `json:"source_review,omitempty"`

	// ProtectedRequirements is an in-memory, one-shot adapter-to-API handoff.
	// Its contents are never part of the JSON conversion result.
	ProtectedRequirements credentialpolicy.ProtectedRequirements `json:"-"`
}

const openAPIMissingBaseURL = "https://missing-base-url.apexload.invalid"

const openAPISourceReviewCredentialOmission = "# [OPENAPI SOURCE OMITTED: ENCODED CREDENTIAL VALUE REQUIRES PROTECTED BINDING]\n"

type Correlation struct {
	Name        string `json:"name"`
	Source      string `json:"source"`
	Expression  string `json:"expression"`
	ExtractType string `json:"extract_type"`
	SampleValue string `json:"sample_value,omitempty"`
	FoundInStep int    `json:"found_in_step"`
	UsedInSteps []int  `json:"used_in_steps"`
}

type document struct {
	OpenAPI             string                    `json:"openapi,omitempty" yaml:"openapi,omitempty"`
	Swagger             string                    `json:"swagger,omitempty" yaml:"swagger,omitempty"`
	Info                info                      `json:"info,omitempty" yaml:"info,omitempty"`
	Servers             []server                  `json:"servers,omitempty" yaml:"servers,omitempty"`
	Schemes             []string                  `json:"schemes,omitempty" yaml:"schemes,omitempty"`
	Host                string                    `json:"host,omitempty" yaml:"host,omitempty"`
	BasePath            string                    `json:"basePath,omitempty" yaml:"basePath,omitempty"`
	Consumes            []string                  `json:"consumes,omitempty" yaml:"consumes,omitempty"`
	Paths               map[string]pathItem       `json:"paths,omitempty" yaml:"paths,omitempty"`
	Components          components                `json:"components,omitempty" yaml:"components,omitempty"`
	SecurityDefinitions map[string]securityScheme `json:"securityDefinitions,omitempty" yaml:"securityDefinitions,omitempty"`
	Security            []map[string][]string     `json:"security,omitempty" yaml:"security,omitempty"`
	Definitions         map[string]schema         `json:"definitions,omitempty" yaml:"definitions,omitempty"`
	Parameters          map[string]parameter      `json:"parameters,omitempty" yaml:"parameters,omitempty"`
}

type info struct {
	Title   string `json:"title,omitempty" yaml:"title,omitempty"`
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
}

type server struct {
	URL       string                    `json:"url,omitempty" yaml:"url,omitempty"`
	Variables map[string]serverVariable `json:"variables,omitempty" yaml:"variables,omitempty"`
}

type serverVariable struct {
	Default string `json:"default,omitempty" yaml:"default,omitempty"`
	Enum    []any  `json:"enum,omitempty" yaml:"enum,omitempty"`
}

type components struct {
	SecuritySchemes map[string]securityScheme `json:"securitySchemes,omitempty" yaml:"securitySchemes,omitempty"`
	Schemas         map[string]schema         `json:"schemas,omitempty" yaml:"schemas,omitempty"`
	Parameters      map[string]parameter      `json:"parameters,omitempty" yaml:"parameters,omitempty"`
	RequestBodies   map[string]requestBody    `json:"requestBodies,omitempty" yaml:"requestBodies,omitempty"`
}

type securityScheme struct {
	Type             string            `json:"type,omitempty" yaml:"type,omitempty"`
	Scheme           string            `json:"scheme,omitempty" yaml:"scheme,omitempty"`
	Name             string            `json:"name,omitempty" yaml:"name,omitempty"`
	In               string            `json:"in,omitempty" yaml:"in,omitempty"`
	BearerFormat     string            `json:"bearerFormat,omitempty" yaml:"bearerFormat,omitempty"`
	OpenIDConnectURL string            `json:"openIdConnectUrl,omitempty" yaml:"openIdConnectUrl,omitempty"`
	Flow             string            `json:"flow,omitempty" yaml:"flow,omitempty"`
	TokenURL         string            `json:"tokenUrl,omitempty" yaml:"tokenUrl,omitempty"`
	Scopes           map[string]string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
	Flows            oauthFlows        `json:"flows,omitempty" yaml:"flows,omitempty"`
}

type oauthFlows struct {
	ClientCredentials oauthFlow `json:"clientCredentials,omitempty" yaml:"clientCredentials,omitempty"`
	Password          oauthFlow `json:"password,omitempty" yaml:"password,omitempty"`
	AuthorizationCode oauthFlow `json:"authorizationCode,omitempty" yaml:"authorizationCode,omitempty"`
	Implicit          oauthFlow `json:"implicit,omitempty" yaml:"implicit,omitempty"`
}

type oauthFlow struct {
	TokenURL string            `json:"tokenUrl,omitempty" yaml:"tokenUrl,omitempty"`
	Scopes   map[string]string `json:"scopes,omitempty" yaml:"scopes,omitempty"`
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
	Consumes    []string              `json:"consumes,omitempty" yaml:"consumes,omitempty"`
	Responses   map[string]any        `json:"responses,omitempty" yaml:"responses,omitempty"`
	Security    []map[string][]string `json:"security,omitempty" yaml:"security,omitempty"`
}

type parameter struct {
	Ref      string `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Name     string `json:"name,omitempty" yaml:"name,omitempty"`
	In       string `json:"in,omitempty" yaml:"in,omitempty"`
	Required bool   `json:"required,omitempty" yaml:"required,omitempty"`
	Style    string `json:"style,omitempty" yaml:"style,omitempty"`
	Explode  *bool  `json:"explode,omitempty" yaml:"explode,omitempty"`
	Schema   schema `json:"schema,omitempty" yaml:"schema,omitempty"`
	Type     string `json:"type,omitempty" yaml:"type,omitempty"`
	Format   string `json:"format,omitempty" yaml:"format,omitempty"`
	Example  any    `json:"example,omitempty" yaml:"example,omitempty"`
	Default  any    `json:"default,omitempty" yaml:"default,omitempty"`
	Enum     []any  `json:"enum,omitempty" yaml:"enum,omitempty"`
}

type requestBody struct {
	Ref      string               `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Required bool                 `json:"required,omitempty" yaml:"required,omitempty"`
	Content  map[string]mediaType `json:"content,omitempty" yaml:"content,omitempty"`
}

type mediaType struct {
	Schema   schema               `json:"schema,omitempty" yaml:"schema,omitempty"`
	Example  any                  `json:"example,omitempty" yaml:"example,omitempty"`
	Examples orderedMediaExamples `json:"examples,omitempty" yaml:"examples,omitempty"`
	Encoding map[string]any       `json:"encoding,omitempty" yaml:"encoding,omitempty"`
}

type namedMediaExample struct {
	Name   string
	Object any
}

// orderedMediaExamples retains the source order of an OpenAPI Media Type
// Object's examples map. Go map iteration cannot be used here: examples are
// alternatives, so the adapter deterministically selects the first authored
// entry when the singular example field is absent.
type orderedMediaExamples []namedMediaExample

func (examples *orderedMediaExamples) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || bytes.Equal(data, []byte("null")) {
		*examples = nil
		return nil
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	if delimiter, ok := token.(json.Delim); !ok || delimiter != '{' {
		return errors.New("OpenAPI media type examples must be an object")
	}
	ordered := make(orderedMediaExamples, 0)
	for decoder.More() {
		nameToken, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := nameToken.(string)
		if !ok {
			return errors.New("OpenAPI media type example name must be a string")
		}
		var object any
		if err := decoder.Decode(&object); err != nil {
			return err
		}
		ordered = append(ordered, namedMediaExample{Name: name, Object: object})
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	*examples = ordered
	return nil
}

func (examples *orderedMediaExamples) UnmarshalYAML(value *yaml.Node) error {
	if value == nil || value.Tag == "!!null" {
		*examples = nil
		return nil
	}
	if value.Kind == yaml.AliasNode {
		value = value.Alias
	}
	if value == nil || value.Kind != yaml.MappingNode {
		return errors.New("OpenAPI media type examples must be a mapping")
	}
	ordered := make(orderedMediaExamples, 0, len(value.Content)/2)
	for index := 0; index+1 < len(value.Content); index += 2 {
		nameNode := value.Content[index]
		var name string
		if err := nameNode.Decode(&name); err != nil {
			return fmt.Errorf("decode OpenAPI media type example name: %w", err)
		}
		var object any
		if err := value.Content[index+1].Decode(&object); err != nil {
			return fmt.Errorf("decode OpenAPI media type example %q: %w", name, err)
		}
		ordered = append(ordered, namedMediaExample{Name: name, Object: object})
	}
	*examples = ordered
	return nil
}

type schema struct {
	Ref                  string            `json:"$ref,omitempty" yaml:"$ref,omitempty"`
	Type                 string            `json:"type,omitempty" yaml:"type,omitempty"`
	Format               string            `json:"format,omitempty" yaml:"format,omitempty"`
	Properties           map[string]schema `json:"properties,omitempty" yaml:"properties,omitempty"`
	Required             []string          `json:"required,omitempty" yaml:"required,omitempty"`
	Items                *schema           `json:"items,omitempty" yaml:"items,omitempty"`
	Example              any               `json:"example,omitempty" yaml:"example,omitempty"`
	Default              any               `json:"default,omitempty" yaml:"default,omitempty"`
	Enum                 []any             `json:"enum,omitempty" yaml:"enum,omitempty"`
	Minimum              *float64          `json:"minimum,omitempty" yaml:"minimum,omitempty"`
	Maximum              *float64          `json:"maximum,omitempty" yaml:"maximum,omitempty"`
	MinLength            *int              `json:"minLength,omitempty" yaml:"minLength,omitempty"`
	MaxLength            *int              `json:"maxLength,omitempty" yaml:"maxLength,omitempty"`
	Pattern              string            `json:"pattern,omitempty" yaml:"pattern,omitempty"`
	Nullable             bool              `json:"nullable,omitempty" yaml:"nullable,omitempty"`
	AdditionalProperties any               `json:"additionalProperties,omitempty" yaml:"additionalProperties,omitempty"`
	OneOf                []schema          `json:"oneOf,omitempty" yaml:"oneOf,omitempty"`
	AnyOf                []schema          `json:"anyOf,omitempty" yaml:"anyOf,omitempty"`
	AllOf                []schema          `json:"allOf,omitempty" yaml:"allOf,omitempty"`
	Discriminator        map[string]any    `json:"discriminator,omitempty" yaml:"discriminator,omitempty"`
	XML                  *xmlObject        `json:"xml,omitempty" yaml:"xml,omitempty"`
}

type xmlObject struct {
	Name      string `json:"name,omitempty" yaml:"name,omitempty"`
	Namespace string `json:"namespace,omitempty" yaml:"namespace,omitempty"`
	Prefix    string `json:"prefix,omitempty" yaml:"prefix,omitempty"`
	Attribute bool   `json:"attribute,omitempty" yaml:"attribute,omitempty"`
	Wrapped   bool   `json:"wrapped,omitempty" yaml:"wrapped,omitempty"`
}

func (s *schema) UnmarshalJSON(data []byte) error {
	type alias schema
	aux := struct {
		Type any `json:"type,omitempty"`
		*alias
	}{alias: (*alias)(s)}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := decoder.Decode(&aux); err != nil {
		return err
	}
	s.Type = normalizeSchemaType(aux.Type, s.Type)
	return nil
}

func (s *schema) UnmarshalYAML(value *yaml.Node) error {
	type alias schema
	if value == nil {
		return nil
	}
	if value.Kind == yaml.AliasNode {
		value = value.Alias
	}
	if value == nil || value.Kind != yaml.MappingNode {
		return errors.New("OpenAPI schema must be a mapping")
	}
	withoutType := *value
	withoutType.Content = make([]*yaml.Node, 0, len(value.Content))
	var typeNode *yaml.Node
	for index := 0; index+1 < len(value.Content); index += 2 {
		key := value.Content[index]
		child := value.Content[index+1]
		if key.Value == "type" {
			typeNode = child
			continue
		}
		withoutType.Content = append(withoutType.Content, key, child)
	}
	*s = schema{}
	if err := withoutType.Decode((*alias)(s)); err != nil {
		return err
	}
	var rawType any
	if typeNode != nil {
		if err := typeNode.Decode(&rawType); err != nil {
			return err
		}
	}
	s.Type = normalizeSchemaType(rawType, s.Type)
	return nil
}

func normalizeSchemaType(raw any, fallback string) string {
	switch typed := raw.(type) {
	case string:
		if strings.TrimSpace(typed) != "" && !strings.EqualFold(strings.TrimSpace(typed), "null") {
			return strings.TrimSpace(typed)
		}
	case []any:
		for _, item := range typed {
			if value, ok := item.(string); ok && strings.TrimSpace(value) != "" && !strings.EqualFold(strings.TrimSpace(value), "null") {
				return strings.TrimSpace(value)
			}
		}
	case []string:
		for _, value := range typed {
			if strings.TrimSpace(value) != "" && !strings.EqualFold(strings.TrimSpace(value), "null") {
				return strings.TrimSpace(value)
			}
		}
	}
	return fallback
}

// responseObject is the subset of an OpenAPI response we inspect when deriving
// body assertions. Swagger 2 puts the schema at the top level; OpenAPI 3 nests
// it under content -> media type.
type responseObject struct {
	Content map[string]mediaType      `json:"content,omitempty" yaml:"content,omitempty"`
	Schema  *schema                   `json:"schema,omitempty" yaml:"schema,omitempty"`
	Headers map[string]responseHeader `json:"headers,omitempty" yaml:"headers,omitempty"`
}

type responseHeader struct {
	Schema schema `json:"schema,omitempty" yaml:"schema,omitempty"`
}

var supportedMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true,
}

type builtOperation struct {
	Index                  int
	Method                 string
	RawPath                string
	Operation              *operation
	PathParams             []parameter
	ActionName             string
	SuccessStatus          int
	SuccessSchema          *schema
	LocationHeader         bool
	Produced               []producedValue
	SensitiveRequestFields map[string]string
	SensitiveWholeBody     bool
}

type producedValue struct {
	VarName     string
	Resource    string
	FieldName   string
	Role        string
	Source      string
	Expression  string
	ExtractType string
	ActionIndex int
}

type columnRegistry map[string]string

func (r columnRegistry) add(name string, s schema) {
	name = sanitizeColumnName(name)
	if name == "" {
		return
	}
	if _, ok := r[name]; ok {
		return
	}
	r[name] = sampleValueForSchema(s, name)
}

func (r columnRegistry) sample(name string) string {
	name = sanitizeColumnName(name)
	if value := r[name]; value != "" {
		return value
	}
	return sampleColumnValue(name)
}

func Convert(req ImportRequest) (*ConvertResult, error) {
	content := strings.TrimSpace(req.Content)
	if content == "" {
		return nil, errors.New("content is required")
	}
	doc, sourceFindings, err := decodeDocument([]byte(content))
	if err != nil {
		return nil, err
	}
	if len(doc.Paths) == 0 {
		return nil, errors.New("OpenAPI document contains no paths")
	}

	var diagnostics diag.Collector
	for _, finding := range sourceFindings {
		diagnostics.Add(
			diag.Warning,
			"source document",
			finding.Location,
			finding.Detail,
		)
	}
	sourceFileName := openAPISourceFileName(content)
	sensitiveSourceValues, sensitiveTypedSourceValue := sensitiveOpenAPISourceValues(doc)
	reviewContent := openAPISourceReviewCredentialOmission
	if !sensitiveTypedSourceValue {
		reviewContent = scrub.Value(content, scrub.Policy{})
		for _, sensitiveValue := range sensitiveSourceValues {
			reviewContent = strings.ReplaceAll(reviewContent, sensitiveValue, scrub.Redacted)
			if encoded, err := json.Marshal(sensitiveValue); err == nil && len(encoded) >= 2 {
				reviewContent = strings.ReplaceAll(reviewContent, string(encoded[1:len(encoded)-1]), scrub.Redacted)
			}
		}
		reviewContent, _ = scrub.RedactQuotedValues(reviewContent, sensitiveSourceValues)
	}
	sourceMappings := make([]sourceview.Mapping, 0)
	res := resolver{doc: doc, diagnostics: &diagnostics}
	baseURL, warnings := deriveBaseURL(doc, req.Options.BaseURL)
	scriptName := convertutil.FirstNonEmpty(req.Options.ScriptName, req.Name, doc.Info.Title, "Imported_OpenAPI_Script")
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
			OpenAPIVersion: convertutil.FirstNonEmpty(doc.OpenAPI, doc.Swagger),
			TotalPaths:     len(doc.Paths),
		},
		Summary: ReviewSummary{
			Title:   doc.Info.Title,
			Version: doc.Info.Version,
			BaseURL: baseURL,
			Servers: serverURLs(doc),
		},
	}
	if result.Script.Config.BaseURL == "" {
		// HTTP scripts require a syntactically valid base URL. Preserve the
		// source omission in Summary and diagnostics, but use a non-resolving
		// .invalid placeholder in the blocked draft so it remains persistable.
		result.Script.Config.BaseURL = openAPIMissingBaseURL
	}

	schemes := normalizedSecuritySchemes(doc)
	authSchemes := sortedAuthSchemeNames(schemes)
	result.Stats.AuthSchemesDetected = authSchemes
	result.Summary.AuthSchemes = authSchemes

	columns := columnRegistry{}
	operationNames := []string{}
	built := make([]builtOperation, 0)
	finalizeReports := make([]requestimport.Report, 0)
	credentialNames := credentialpolicy.NewNameRegistry()
	auth := newAuthContext(result.Script, schemes, baseURL, req.Options, &result.Warnings, res)
	sourceLineIndex := sourceview.NewLineIndex(reviewContent)
	for _, path := range sortedPathKeys(doc.Paths) {
		item := doc.Paths[path]
		for _, candidate := range operationsForPath(item) {
			result.Stats.TotalOperations++
			if !supportedMethods[candidate.method] || !operationAllowed(candidate.op, req.Options) {
				skipped := strings.ToUpper(candidate.method) + " " + path
				result.Stats.SkippedOperations = append(result.Stats.SkippedOperations, skipped)
				diagnostics.Add(diag.Warning, "OpenAPI operation skipped", skipped, "operation was not converted because it was filtered or uses an unsupported HTTP method")
				sourceMappings = append(sourceMappings, openAPISourceMapping(sourceFileName, reviewContent, sourceLineIndex, path, candidate.method, "", skipped, "operation", "review_only", "OpenAPI operation was filtered or used an unsupported HTTP method."))
				continue
			}
			action, bodyColumns, assertionAdded, opInfo := buildAction(path, candidate.method, item.Parameters, candidate.op, convertutil.BoolOption(req.Options.IncludeAssertions, true), res, columns, auth)
			if baseURL == "" {
				applyOpenAPIReviewGate(&action, []string{"missing_base_url"}, strings.ToUpper(candidate.method)+" "+path)
			}
			actionIndex := len(result.Script.Actions)
			actionPath := fmt.Sprintf("actions[%d]", actionIndex)
			action, report, err := finalizeOpenAPIRequestAction(
				action,
				path,
				candidate.method,
				sourceFileName,
				actionPath,
				credentialNames,
				openAPIProvenVariables(result.Script.Init),
				opInfo.SensitiveRequestFields,
				opInfo.SensitiveWholeBody,
			)
			if err != nil {
				return nil, fmt.Errorf("finalize OpenAPI operation %s %s: %w", strings.ToUpper(candidate.method), path, err)
			}
			finalizeReports = append(finalizeReports, report)
			opInfo.Index = actionIndex
			opInfo.ActionName = action.Name
			result.Script.Actions = append(result.Script.Actions, action)
			built = append(built, opInfo)
			sourceMappings = append(sourceMappings, openAPISourceMapping(sourceFileName, reviewContent, sourceLineIndex, path, candidate.method, actionPath, action.Name, "action", "converted", "OpenAPI operation was converted into a generated APEX Load action."))
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
				if _, ok := columns[name]; !ok {
					columns[name] = sampleColumnValue(name)
				}
			}
			for _, param := range append(item.Parameters, candidate.op.Parameters...) {
				param = res.resolveParameter(param, strings.ToUpper(candidate.method)+" "+path)
				if param.Name != "" && !strings.EqualFold(param.In, "body") {
					columns.add(param.Name, parameterSchema(param))
				}
			}
		}
	}

	if len(result.Script.Actions) == 0 {
		return nil, errors.New("no supported OpenAPI operations found")
	}
	result.Stats.Extractors += applyResponseExtractors(result.Script, built, res)
	result.Correlations = applyOperationChaining(result.Script, built, res)
	result.Stats.Correlations = len(result.Correlations)
	result.Summary.OperationNames = operationNames
	dataColumns := openAPIDataColumnsFromScript(result.Script)
	result.Summary.DataColumns = dataColumns
	if len(dataColumns) > 0 {
		result.Script.DataSources = append([]dsl.DataSource{openAPIDataSource(dataColumns, columns)}, openAPIFileDataSources(result.Script)...)
	} else {
		result.Script.DataSources = openAPIFileDataSources(result.Script)
	}
	finalized, err := requestimport.FinalizeScript(result.Script, finalizeReports, requestimport.FinalizeOptions{})
	if err != nil {
		return nil, fmt.Errorf("finalize OpenAPI script: %w", err)
	}
	protectedSourceValues := make([]string, 0, len(finalized.Requirements))
	for _, requirement := range finalized.Requirements {
		if requirement.Value != "" {
			protectedSourceValues = append(protectedSourceValues, requirement.Value)
			reviewContent = strings.ReplaceAll(reviewContent, requirement.Value, scrub.Redacted)
		}
	}
	reviewContent, _ = scrub.RedactQuotedValues(reviewContent, protectedSourceValues)
	sourceReviewOmitted := sensitiveTypedSourceValue
	if !sourceReviewOmitted {
		reviewContent, sourceReviewOmitted = disclosureSafeOpenAPISourceReview(
			reviewContent,
			append(append([]string(nil), sensitiveSourceValues...), protectedSourceValues...),
		)
	}
	result.ProtectedRequirements.Store(finalized.Requirements)
	for index := range finalized.Requirements {
		finalized.Requirements[index].Value = ""
	}
	result.Script = finalized.Script
	result.Summary.BaseURL = result.Script.Config.BaseURL
	for index := range result.Summary.Servers {
		result.Summary.Servers[index] = scrub.ValueURL(result.Summary.Servers[index], scrub.Policy{})
	}
	for _, item := range finalized.Diagnostics {
		diagnostics.Add(item.Severity, item.Element, item.Location, item.Detail)
	}
	if baseURL == "" {
		result.Warnings = append(result.Warnings, "base URL could not be derived; set options.base_url before saving the script")
	}
	if gap := result.Stats.TotalOperations - result.Stats.ConvertedActions - len(result.Stats.SkippedOperations); gap > 0 {
		diagnostics.Add(diag.Warning, "OpenAPI completeness guard", "document", fmt.Sprintf("%d operation(s) were present but not converted or explicitly skipped", gap))
	}
	result.Unsupported = diagnostics.Items()
	result.SourceReview = sourceview.Build(map[string]string{sourceFileName: reviewContent}, sourceMappings)
	if sourceReviewOmitted && result.SourceReview != nil {
		for index := range result.SourceReview.SourceFiles {
			result.SourceReview.SourceFiles[index].Redacted = true
		}
	}
	sanitizeOpenAPIResultReviewText(result)
	return result, nil
}

func disclosureSafeOpenAPISourceReview(content string, protected []string) (string, bool) {
	seen := make(map[string]struct{}, len(protected))
	normalized := make([]string, 0, len(protected))
	for _, value := range protected {
		if value == "" {
			continue
		}
		if _, duplicate := seen[value]; duplicate {
			continue
		}
		seen[value] = struct{}{}
		normalized = append(normalized, value)
	}
	protected = normalized
	if content == "" || len(protected) == 0 {
		return content, false
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		// The original document parsed successfully. If redaction made the
		// review copy ambiguous, retaining it would make it impossible to prove
		// that an alternate scalar encoding does not still disclose a secret.
		return openAPISourceReviewCredentialOmission, true
	}
	seenNodes := make(map[*yaml.Node]struct{})
	remainingNodes := len(content) + 1
	remainingComparisons := (16 * int64(len(content))) + (1 << 20)
	var containsProtected func(*yaml.Node) (bool, bool)
	containsProtected = func(node *yaml.Node) (bool, bool) {
		if node == nil {
			return false, false
		}
		if _, duplicate := seenNodes[node]; duplicate {
			return false, false
		}
		seenNodes[node] = struct{}{}
		remainingNodes--
		if remainingNodes < 0 {
			return false, true
		}
		if node.Kind == yaml.ScalarNode {
			for _, value := range protected {
				if int64(len(node.Value)) > remainingComparisons {
					return false, true
				}
				remainingComparisons -= int64(len(node.Value))
				if strings.Contains(node.Value, value) {
					return true, false
				}
			}
		}
		if node.Alias != nil {
			if found, exhausted := containsProtected(node.Alias); found || exhausted {
				return found, exhausted
			}
		}
		for _, child := range node.Content {
			if found, exhausted := containsProtected(child); found || exhausted {
				return found, exhausted
			}
		}
		return false, false
	}
	if found, exhausted := containsProtected(&root); found || exhausted {
		return openAPISourceReviewCredentialOmission, true
	}
	return content, false
}

func openAPISourceFileName(content string) string {
	if strings.HasPrefix(strings.TrimSpace(content), "{") {
		return "openapi.json"
	}
	return "openapi.yaml"
}

func sensitiveOpenAPISourceValues(doc *document) ([]string, bool) {
	if doc == nil {
		return nil, false
	}
	schemaResolver := resolver{doc: doc}
	values := map[string]struct{}{}
	hasTypedValue := false
	var add func(any)
	add = func(value any) {
		switch typed := value.(type) {
		case string:
			if typed != "" {
				values[typed] = struct{}{}
			}
		case bool, float32, float64, int, int8, int16, int32, int64,
			uint, uint8, uint16, uint32, uint64, json.Number:
			// JSON's ordinary decoder and YAML's typed decoder do not retain
			// the original numeric lexeme. A large integer can be rounded and a
			// scientific value can be normalized before this semantic walk, so
			// string replacement cannot prove that the source-review copy no
			// longer contains the authored credential. Keep executable
			// conversion unchanged and conservatively omit only the review copy.
			hasTypedValue = true
			if text, ok := authoredWireValue(typed); ok {
				values[text] = struct{}{}
			}
		case map[string]any:
			for _, child := range typed {
				add(child)
			}
		case map[any]any:
			for _, child := range typed {
				add(child)
			}
		case []any:
			for _, child := range typed {
				add(child)
			}
		}
	}
	var walkExample func(any)
	walkExample = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				if credential.SensitiveName(key) {
					add(child)
					continue
				}
				walkExample(child)
			}
		case []any:
			for _, child := range typed {
				walkExample(child)
			}
		}
	}
	var walkSchemaValue func(any, schema, string, map[string]bool)
	walkSchemaValue = func(value any, current schema, field string, seen map[string]bool) {
		current = schemaResolver.resolveSchema(current, "source review", seen)
		if schemaCredentialField(field, current) {
			add(value)
			return
		}
		switch typed := value.(type) {
		case map[string]any:
			for key, child := range typed {
				childSchema, declared := current.Properties[key]
				if !declared {
					childSchema, _ = additionalPropertiesSchema(current)
				}
				if schemaCredentialField(key, childSchema) {
					add(child)
					continue
				}
				walkSchemaValue(child, childSchema, key, copySeen(seen))
			}
		case []any:
			itemSchema := valueOrEmptySchema(current.Items)
			for _, child := range typed {
				walkSchemaValue(child, itemSchema, field, copySeen(seen))
			}
		default:
			walkExample(value)
		}
	}
	var walkSchema func(schema, string)
	walkSchema = func(current schema, field string) {
		if schemaCredentialField(field, current) {
			add(current.Example)
			add(current.Default)
			for _, enum := range current.Enum {
				add(enum)
			}
		}
		walkSchemaValue(current.Example, current, field, map[string]bool{})
		walkSchemaValue(current.Default, current, field, map[string]bool{})
		walkExample(current.Example)
		walkExample(current.Default)
		for name, child := range current.Properties {
			walkSchema(child, name)
		}
		if current.Items != nil {
			walkSchema(*current.Items, field)
		}
		if additional, ok := additionalPropertiesSchema(current); ok {
			walkSchema(additional, field)
		}
		for _, child := range append(append([]schema(nil), current.OneOf...), append(current.AnyOf, current.AllOf...)...) {
			walkSchema(child, field)
		}
	}
	walkParameter := func(param parameter) {
		if schemaCredentialField(param.Name, parameterSchema(param)) || (strings.EqualFold(param.In, "header") && credential.SensitiveHeader(param.Name)) {
			add(param.Example)
			add(param.Default)
			add(param.Schema.Example)
			add(param.Schema.Default)
			for _, enum := range param.Enum {
				add(enum)
			}
		}
		walkSchema(param.Schema, param.Name)
	}
	walkBody := func(body *requestBody) {
		if body == nil {
			return
		}
		for _, media := range body.Content {
			walkExample(media.Example)
			walkSchemaValue(media.Example, media.Schema, "", map[string]bool{})
			for _, named := range media.Examples {
				// Walk the complete Example Object so credential-shaped
				// fields in every alternative are disclosure-redacted, even
				// though only the first alternative is projected.
				walkExample(named.Object)
				if value, present, _ := mediaExampleObjectValue(named.Object); present {
					walkSchemaValue(value, media.Schema, "", map[string]bool{})
				}
			}
			walkSchema(media.Schema, "")
		}
	}
	for _, item := range doc.Paths {
		for _, param := range item.Parameters {
			walkParameter(param)
		}
		for _, candidate := range operationsForPath(item) {
			if candidate.op == nil {
				continue
			}
			for _, param := range candidate.op.Parameters {
				walkParameter(param)
			}
			walkBody(candidate.op.RequestBody)
		}
	}
	for _, param := range doc.Components.Parameters {
		walkParameter(param)
	}
	for _, param := range doc.Parameters {
		walkParameter(param)
	}
	for _, body := range doc.Components.RequestBodies {
		bodyCopy := body
		walkBody(&bodyCopy)
	}
	for name, current := range doc.Components.Schemas {
		walkSchema(current, name)
	}
	for name, current := range doc.Definitions {
		walkSchema(current, name)
	}
	out := make([]string, 0, len(values))
	for value := range values {
		out = append(out, value)
	}
	sort.Strings(out)
	return out, hasTypedValue
}

func openAPISourceMapping(sourceFile string, content string, lineIndex sourceview.LineIndex, path string, method string, targetPath string, targetName string, targetKind string, status string, notes string) sourceview.Mapping {
	line := openAPIOperationLine(content, lineIndex, path, method)
	return sourceview.Mapping{
		SourceFile: sourceFile,
		StartLine:  line,
		EndLine:    line,
		Function:   strings.ToUpper(method) + " " + path,
		TargetPath: targetPath,
		TargetName: targetName,
		TargetKind: targetKind,
		Status:     status,
		Notes:      notes,
	}
}

func openAPIOperationLine(content string, lineIndex sourceview.LineIndex, path string, method string) int {
	pathLine := lineForNeedle(content, lineIndex, `"`+path+`"`)
	if pathLine == 1 {
		pathLine = lineForNeedle(content, lineIndex, path+":")
	}
	if pathLine < 1 {
		pathLine = 1
	}
	methodNeedles := []string{`"` + strings.ToLower(method) + `"`, strings.ToLower(method) + ":"}
	lines := strings.Split(content, "\n")
	for index := pathLine - 1; index < len(lines) && index < pathLine+80; index++ {
		trimmed := strings.TrimSpace(lines[index])
		for _, needle := range methodNeedles {
			if strings.HasPrefix(trimmed, needle) {
				return index + 1
			}
		}
	}
	return pathLine
}

func lineForNeedle(content string, lineIndex sourceview.LineIndex, needle string) int {
	if needle == "" {
		return 1
	}
	offset := strings.Index(content, needle)
	if offset < 0 {
		return 1
	}
	return lineIndex.LineForOffset(offset)
}

// decodeDocument parses an OpenAPI/Swagger document and reports structural
// ambiguities alongside it. A spec whose objects carry duplicate or non-scalar
// keys means different things to different readers, so the converter and a
// human reviewer can disagree about what was imported; those are rejected.
// Merge keys are tolerated — anchors plus "<<" are idiomatic in hand-authored
// specs — but returned so the caller can surface them for review.
func decodeDocument(data []byte) (*document, []srcparse.Finding, error) {
	var doc document
	jsonDecoder := json.NewDecoder(bytes.NewReader(data))
	jsonDecoder.UseNumber()
	if err := jsonDecoder.Decode(&doc); err == nil && (doc.OpenAPI != "" || doc.Swagger != "" || len(doc.Paths) > 0) {
		if validateErr := srcparse.ValidateJSON(data, srcparse.Limits{}); validateErr != nil {
			return nil, nil, fmt.Errorf("invalid OpenAPI document: %w", validateErr)
		}
		return &doc, nil, nil
	}
	findings, err := srcparse.ValidateYAMLMappingKeys(data, srcparse.SpecYAMLPolicy(), srcparse.Limits{})
	if err != nil {
		return nil, nil, fmt.Errorf("invalid OpenAPI document: %w", err)
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return nil, nil, fmt.Errorf("invalid OpenAPI document: %w", err)
	}
	if doc.OpenAPI == "" && doc.Swagger == "" && len(doc.Paths) == 0 {
		return nil, nil, errors.New("invalid OpenAPI document: missing openapi/swagger version or paths")
	}
	return &doc, findings, nil
}

func deriveBaseURL(doc *document, override string) (string, []string) {
	if value := strings.TrimSpace(override); value != "" {
		return strings.TrimRight(value, "/"), nil
	}
	warnings := []string{}
	for _, srv := range doc.Servers {
		if value := strings.TrimSpace(srv.URL); value != "" {
			if strings.Contains(value, "{") {
				var serverWarnings []string
				value, serverWarnings = expandServerVariables(value, srv.Variables)
				warnings = append(warnings, serverWarnings...)
			}
			return strings.TrimRight(value, "/"), warnings
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

func expandServerVariables(raw string, variables map[string]serverVariable) (string, []string) {
	var warnings []string
	value := regexp.MustCompile(`\{([^}]+)\}`).ReplaceAllStringFunc(raw, func(token string) string {
		name := strings.Trim(token, "{}")
		variable, ok := variables[name]
		if !ok || strings.TrimSpace(variable.Default) == "" {
			warnings = append(warnings, "OpenAPI server variable "+name+" did not define a default; substituted example")
			return "example"
		}
		if len(variable.Enum) > 0 {
			warnings = append(warnings, "OpenAPI server variable "+name+" used default "+variable.Default+" from an enum-constrained server URL")
		}
		return variable.Default
	})
	return value, warnings
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
	return r.resolveParameterWithSeen(param, location, nil)
}

func (r resolver) resolveParameterWithSeen(param parameter, location string, seen map[string]bool) parameter {
	ref := strings.TrimSpace(param.Ref)
	if ref == "" {
		return param
	}
	if seen == nil {
		seen = map[string]bool{}
	}
	if seen[ref] {
		r.add(diag.Warning, "OpenAPI parameter $ref", location, "parameter reference cycle detected at "+ref+"; parameter was not expanded")
		return parameter{}
	}
	seen[ref] = true
	if value, ok := r.localParameter(ref); ok {
		return r.resolveParameterWithSeen(value, location, seen)
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
			merged.Required = uniqueSorted(append(merged.Required, resolved.Required...))
			if merged.Format == "" {
				merged.Format = resolved.Format
			}
			if merged.Minimum == nil {
				merged.Minimum = resolved.Minimum
			}
			if merged.Maximum == nil {
				merged.Maximum = resolved.Maximum
			}
			if merged.MinLength == nil {
				merged.MinLength = resolved.MinLength
			}
			if merged.MaxLength == nil {
				merged.MaxLength = resolved.MaxLength
			}
			if merged.Pattern == "" {
				merged.Pattern = resolved.Pattern
			}
			if len(merged.Enum) == 0 {
				merged.Enum = append([]any(nil), resolved.Enum...)
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
	if r.doc == nil {
		return parameter{}, false
	}
	switch {
	case strings.HasPrefix(ref, "#/components/parameters/"):
		value, ok := r.doc.Components.Parameters[refName(ref, "#/components/parameters/")]
		return value, ok
	case strings.HasPrefix(ref, "#/parameters/"):
		value, ok := r.doc.Parameters[refName(ref, "#/parameters/")]
		return value, ok
	default:
		return parameter{}, false
	}
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

func buildAction(path, method string, pathParams []parameter, op *operation, includeAssertion bool, res resolver, columns columnRegistry, auth *authContext) (dsl.Action, []string, bool, builtOperation) {
	params := append([]parameter{}, pathParams...)
	params = append(params, op.Parameters...)
	location := strings.ToUpper(method) + " " + path
	for i := range params {
		params[i] = res.resolveParameter(params[i], location)
	}
	target, paramColumns := templatePath(path, params, res, location, columns)
	headers := map[string]string{"Accept": "application/json"}
	applyHeaderAndCookieParams(headers, params, columns)
	bodySource := res.resolveRequestBody(op.RequestBody, location)
	if bodySource == nil {
		bodySource = swagger2RequestBody(params, op, res, location)
	}
	body, contentType, bodyColumns, bodyReviewCodes, sensitiveRequestFields := requestBodyExample(bodySource, res, location, columns)
	sensitiveWholeBody := openAPIRequestBodyIsWholeCredential(
		bodySource,
		res,
		location,
	) || openAPIParametersDeclareWholeBodyCredential(params)
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" || credential.SensitiveName(name) ||
			!schemaCredentialField(name, parameterSchema(param)) {
			continue
		}
		if sensitiveRequestFields == nil {
			sensitiveRequestFields = map[string]string{}
		}
		sensitiveRequestFields[strings.ToLower(name)] = name
	}
	if contentType != "" {
		headers["Content-Type"] = contentType
	}
	operationKey := strings.ToUpper(method) + " " + path
	action := dsl.Action{
		Name:    sanitizeName(convertutil.FirstNonEmpty(op.OperationID, op.Summary, strings.ToUpper(method)+"_"+path)),
		Type:    "request",
		Method:  strings.ToUpper(method),
		Target:  target,
		Headers: headers,
		Body:    body,
		Metadata: map[string]any{
			"openapi_operation_id": op.OperationID,
			"openapi_tags":         op.Tags,
			"openapi": map[string]any{
				"operation_id":  op.OperationID,
				"method":        strings.ToUpper(method),
				"path":          path,
				"operation_key": operationKey,
			},
		},
	}
	pathCredentialTargets, pathCredentialLocatorUnresolved :=
		openAPIPathCredentialTargets(path, target, params, operationKey)
	action.Metadata = requestimport.MergePreservedCredentialTargetsMetadata(
		action.Metadata,
		pathCredentialTargets,
	)
	if pathCredentialLocatorUnresolved {
		bodyReviewCodes = append(
			bodyReviewCodes,
			"path_credential_locator_unresolved",
		)
	}
	securityRequirements := op.Security
	if securityRequirements == nil {
		// Operations without their own security requirement inherit the
		// document-level requirement (OpenAPI semantics).
		securityRequirements = res.doc.Security
	}
	applyOperationSecurity(&action, securityRequirements, auth, res, location)
	bodyReviewCodes = append(bodyReviewCodes, sensitiveParameterReviewCodes(params)...)
	if len(bodyReviewCodes) > 0 {
		applyOpenAPIReviewGate(&action, bodyReviewCodes, location)
	}
	status, responseKey, hasStatus := firstSuccessStatus(op.Responses)
	successSchema, responseObject := successResponseSchema(op.Responses, responseKey, res, location)
	opInfo := builtOperation{
		Method:                 method,
		RawPath:                path,
		Operation:              op,
		PathParams:             params,
		SuccessStatus:          status,
		SuccessSchema:          successSchema,
		LocationHeader:         responseDeclaresLocation(status, responseObject),
		SensitiveRequestFields: sensitiveRequestFields,
		SensitiveWholeBody:     sensitiveWholeBody,
	}
	if includeAssertion {
		if hasStatus {
			action.Assertions = openAPISuccessStatusAssertions(op.Responses, status)
			if openAPISuccessResponseSchemasConflict(op.Responses, res, location) {
				applyOpenAPIReviewGate(&action, []string{"multiple_success_response_schemas"}, location)
				res.add(diag.Warning, "OpenAPI success responses", location, "multiple successful responses declare incompatible body schemas; response-body replay assertions require review")
			}
			action.Assertions = append(action.Assertions, responseBodyAssertions(successSchema)...)
			return action, uniqueSorted(append(bodyColumns, paramColumns...)), true, opInfo
		}
	}
	return action, uniqueSorted(append(bodyColumns, paramColumns...)), false, opInfo
}

func openAPISuccessStatusAssertions(responses map[string]any, fallback int) []dsl.Assertion {
	exact := 0
	for _, key := range sortedMapKeys(responses) {
		status := strings.ToUpper(strings.TrimSpace(key))
		if status == "2XX" {
			return []dsl.Assertion{{Type: "status_code_range", Min: 200, Max: 299}}
		}
		code, err := strconv.Atoi(status)
		if err == nil && code >= 200 && code <= 299 {
			exact++
			fallback = code
		}
	}
	if exact > 1 {
		return []dsl.Assertion{{Type: "status_code_range", Min: 200, Max: 299}}
	}
	return []dsl.Assertion{{Type: "status_code", Expected: fallback}}
}

func openAPISuccessResponseSchemasConflict(responses map[string]any, res resolver, location string) bool {
	seen := ""
	seenResponse := false
	for _, key := range sortedMapKeys(responses) {
		upper := strings.ToUpper(strings.TrimSpace(key))
		code, err := strconv.Atoi(upper)
		if upper != "2XX" && (err != nil || code < 200 || code > 299) {
			continue
		}
		schema, _ := successResponseSchema(responses, key, res, location)
		current := "<no-body-schema>"
		if schema != nil {
			normalized := normalizeOpenAPIResponseSchemaForComparison(*schema)
			encoded, err := json.Marshal(normalized)
			if err != nil {
				continue
			}
			current = string(encoded)
		}
		if !seenResponse {
			seen = current
			seenResponse = true
			continue
		}
		if current != seen {
			return true
		}
	}
	return false
}

func normalizeOpenAPIResponseSchemaForComparison(current schema) schema {
	// Examples and defaults are sample values, and XML annotations control wire
	// presentation rather than the JSON response shape consumed by the generated
	// assertions and extractors. Including them makes two otherwise identical
	// success schemas look incompatible and needlessly blocks execution.
	current.Example = nil
	current.Default = nil
	current.XML = nil
	current.Required = uniqueSorted(current.Required)
	current.Enum = normalizeOpenAPIEnumForComparison(current.Enum)
	if len(current.Properties) > 0 {
		properties := make(map[string]schema, len(current.Properties))
		for name, property := range current.Properties {
			properties[name] = normalizeOpenAPIResponseSchemaForComparison(property)
		}
		current.Properties = properties
	}
	if current.Items != nil {
		item := normalizeOpenAPIResponseSchemaForComparison(*current.Items)
		current.Items = &item
	}
	for index := range current.OneOf {
		current.OneOf[index] = normalizeOpenAPIResponseSchemaForComparison(current.OneOf[index])
	}
	current.OneOf = sortOpenAPISchemaAlternativesForComparison(current.OneOf)
	for index := range current.AnyOf {
		current.AnyOf[index] = normalizeOpenAPIResponseSchemaForComparison(current.AnyOf[index])
	}
	current.AnyOf = sortOpenAPISchemaAlternativesForComparison(current.AnyOf)
	for index := range current.AllOf {
		current.AllOf[index] = normalizeOpenAPIResponseSchemaForComparison(current.AllOf[index])
	}
	current.AllOf = sortOpenAPISchemaAlternativesForComparison(current.AllOf)
	return current
}

func normalizeOpenAPIEnumForComparison(values []any) []any {
	if len(values) < 2 {
		return values
	}
	type encodedValue struct {
		key   string
		value any
	}
	encoded := make([]encodedValue, 0, len(values))
	for _, value := range values {
		data, err := json.Marshal(value)
		if err != nil {
			// Parsed OpenAPI enum values should always be JSON-compatible. Retain
			// authored order if a future decoder introduces an unsupported value;
			// conservatively review-gating that case is safer than conflating it.
			return values
		}
		encoded = append(encoded, encodedValue{key: string(data), value: value})
	}
	sort.Slice(encoded, func(i, j int) bool { return encoded[i].key < encoded[j].key })
	result := make([]any, 0, len(encoded))
	previous := ""
	for index, candidate := range encoded {
		if index > 0 && candidate.key == previous {
			continue
		}
		result = append(result, candidate.value)
		previous = candidate.key
	}
	return result
}

func sortOpenAPISchemaAlternativesForComparison(values []schema) []schema {
	if len(values) < 2 {
		return values
	}
	result := append([]schema(nil), values...)
	sort.SliceStable(result, func(i, j int) bool {
		left, leftErr := json.Marshal(result[i])
		right, rightErr := json.Marshal(result[j])
		if leftErr != nil || rightErr != nil {
			return false
		}
		return bytes.Compare(left, right) < 0
	})
	return result
}

// finalizeOpenAPIRequestAction is the adapter boundary between OpenAPI
// resolution and the source-neutral request import pipeline. Everything above
// this point is source work (refs, parameter styles, security alternatives,
// examples); everything below is the common wire projection and readiness
// contract.
func finalizeOpenAPIRequestAction(action dsl.Action, path, method, sourceFile, actionPath string, names *credentialpolicy.NameRegistry, provenVars map[string]struct{}, sensitiveNames map[string]string, sensitiveWholeBody bool) (dsl.Action, requestimport.Report, error) {
	source := credentialpolicy.SourceRef{
		Format:  "openapi",
		Path:    sourceFile,
		Pointer: openAPIOperationPointer(path, method),
		ItemID:  openAPIOperationKey(path, method),
	}
	headers := requestimport.HeaderListFromMap(action.Headers)
	for index := range headers {
		headers[index].Source = source
	}
	var findings []importreview.Finding
	if len(headers) != 0 {
		location := sourceFile + openAPIOperationPointer(path, method)
		findings = append(findings, importreview.Finding{
			Code:       "source_order_unavailable",
			Severity:   diag.Info,
			Element:    action.Name,
			Detail:     "source_order_unavailable: the source adapter exposed effective headers as a map; deterministic name order was synthesized",
			Location:   location,
			Source:     "openapi",
			ReasonCode: "source_order_unavailable",
			SourceRef:  location,
		})
	}
	body, err := openAPIEffectiveBody(action, source)
	if err != nil {
		return dsl.Action{}, requestimport.Report{
			RequestKey:  openAPIOperationKey(path, method),
			Disposition: requestimport.Rejected,
		}, err
	}
	effective := requestimport.EffectiveRequest{
		Key:       openAPIOperationKey(path, method),
		Name:      action.Name,
		Method:    action.Method,
		Target:    action.Target,
		Headers:   headers,
		Body:      body,
		Auth:      openAPIEffectiveAuth(action.Auth, source),
		Timeout:   openAPIEffectiveTimeout(action.TimeoutMs),
		Redirects: openAPIEffectiveRedirects(action.MaxRedirects),
		Metadata:  action.Metadata,
		Source:    source,
	}
	options := requestimport.FinalizeOptions{
		ActionPath: actionPath,
		Findings:   findings,
		Overlay: requestimport.ActionOverlay{
			Type:           action.Type,
			Params:         action.Params,
			Metadata:       action.Metadata,
			Extractors:     action.Extractors,
			Assertions:     action.Assertions,
			ThinkTime:      action.ThinkTime,
			ThinkTimeAfter: action.ThinkTimeAfter,
			SOAP:           action.SOAP,
		},
	}
	// OpenAPI already resolves source-generated composed Cookie values and
	// disclosure-safe typed placeholders. Run the shared projector whenever it
	// can operate on complete scalar values without re-governing those composed
	// constructs or turning an intentionally blocked typed draft into a hard
	// rejection.
	if openAPISharedCredentialProjectionSafe(action) {
		options.Credentials = &requestimport.CredentialProjectionOptions{
			Mode:                         credentialpolicy.Preserve,
			Registry:                     names,
			NamePrefix:                   "OPENAPI",
			SensitiveNames:               sensitiveNames,
			SensitiveWholeBody:           sensitiveWholeBody,
			ProvenVars:                   provenVars,
			PreGovernedHeaderOccurrences: openAPIPreGovernedHeaderOccurrences(headers),
		}
	}
	return requestimport.FinalizeAction(effective, options)
}

func openAPIProvenVariables(init []dsl.Action) map[string]struct{} {
	proven := make(map[string]struct{})
	for _, action := range init {
		if dsl.CanonicalActionType(action.Type) != "http_oauth2_token" {
			continue
		}
		name, _ := action.Params["save_as"].(string)
		name = strings.TrimSpace(name)
		if name == "" {
			name = "access_token"
		}
		proven[name] = struct{}{}
	}
	if len(proven) == 0 {
		return nil
	}
	return proven
}

func openAPIOperationKey(path, method string) string {
	return strings.ToUpper(strings.TrimSpace(method)) + " " + path
}

func openAPIOperationPointer(path, method string) string {
	escapedPath := strings.ReplaceAll(strings.ReplaceAll(path, "~", "~0"), "/", "~1")
	return "#/paths/" + escapedPath + "/" + strings.ToLower(strings.TrimSpace(method))
}

func openAPIEffectiveBody(action dsl.Action, source credentialpolicy.SourceRef) (requestimport.Body, error) {
	mediaType := headerValue(action.Headers, "Content-Type")
	kind := requestimport.BodyNone
	if action.Body != "" || mediaType != "" {
		switch {
		case strings.Contains(strings.ToLower(mediaType), "json"):
			kind = requestimport.BodyJSON
		case strings.Contains(strings.ToLower(mediaType), "xml"):
			kind = requestimport.BodyXML
		case strings.Contains(strings.ToLower(mediaType), "application/x-www-form-urlencoded"):
			kind = requestimport.BodyFormURLEncoded
		case strings.Contains(strings.ToLower(mediaType), "multipart/"):
			kind = requestimport.BodyMultipart
		case strings.Contains(strings.ToLower(mediaType), "graphql"):
			kind = requestimport.BodyGraphQL
		default:
			kind = requestimport.BodyRaw
		}
	}
	encoding := requestimport.BodyEncodingText
	if action.BodyEncoding == "" {
		encoding = requestimport.BodyEncodingAuto
	} else if strings.EqualFold(action.BodyEncoding, string(requestimport.BodyEncodingBase64)) {
		encoding = requestimport.BodyEncodingBase64
	}
	payload := []byte(action.Body)
	if encoding == requestimport.BodyEncodingBase64 {
		decoded, _, err := dsl.DecodeBodyEncoding(action.Body, action.BodyEncoding, 0)
		if err != nil {
			return requestimport.Body{}, fmt.Errorf("decode OpenAPI action body: %w", err)
		}
		payload = decoded
	}
	return requestimport.Body{
		Kind:      kind,
		MediaType: mediaType,
		Payload:   payload,
		Encoding:  encoding,
		Source:    source,
	}, nil
}

func openAPIEffectiveAuth(auth *dsl.AuthConfig, source credentialpolicy.SourceRef) requestimport.Auth {
	if auth == nil {
		return requestimport.Auth{Mode: requestimport.AuthInherit, Source: source}
	}
	projected := requestimport.Auth{
		Username: auth.Username,
		Password: auth.Password,
		Token:    auth.Token,
		Domain:   auth.Domain,
		Source:   source,
	}
	switch strings.ToLower(strings.TrimSpace(auth.Type)) {
	case "", "inherit":
		projected.Mode = requestimport.AuthInherit
	case "none":
		projected.Mode = requestimport.AuthNone
	case "basic":
		projected.Mode = requestimport.AuthBasic
	case "bearer":
		projected.Mode = requestimport.AuthBearer
	case "digest":
		projected.Mode = requestimport.AuthDigest
	default:
		projected.Mode = requestimport.AuthUnsupported
		projected.SourceMode = strings.ToLower(strings.TrimSpace(auth.Type))
	}
	return projected
}

func openAPIEffectiveTimeout(milliseconds int) requestimport.Timeout {
	if milliseconds <= 0 {
		return requestimport.Timeout{Mode: requestimport.TimeoutInherit}
	}
	return requestimport.Timeout{Mode: requestimport.TimeoutBounded, Milliseconds: milliseconds}
}

func openAPIEffectiveRedirects(maximum *int) requestimport.Redirects {
	if maximum == nil {
		return requestimport.Redirects{Mode: requestimport.RedirectInherit}
	}
	if *maximum == 0 {
		return requestimport.Redirects{Mode: requestimport.RedirectDisabled}
	}
	if *maximum < 0 {
		return requestimport.Redirects{Mode: requestimport.RedirectUnlimited}
	}
	return requestimport.Redirects{Mode: requestimport.RedirectLimited, Max: *maximum}
}

func openAPISharedCredentialProjectionSafe(action dsl.Action) bool {
	if review, ok := action.Metadata["import_review"].(map[string]any); ok {
		for _, code := range stringSlice(review["reason_codes"]) {
			switch code {
			case "sensitive_non_string_example", "sensitive_non_string_schema",
				"unsafe_xml_credential_projection", "unsafe_structured_credential_projection",
				"malformed_authored_example":
				return false
			}
		}
	}
	return true
}

// openAPIPreGovernedHeaderOccurrences isolates the OpenAPI adapter's existing
// composed-header handling from the rest of shared projection. OpenAPI builds
// values such as a Cookie header from individually governed parameter and
// security-scheme references. Preserving those exact occurrences must not
// disable projection of independent URL, auth, or body credentials.
func openAPIPreGovernedHeaderOccurrences(headers requestimport.HeaderList) map[int]struct{} {
	var governed map[int]struct{}
	wholeRuntimeValue := func(value string) bool {
		kind, expression := credentialpolicy.ClassifyWholeTemplate(value, nil)
		switch kind {
		case credentialpolicy.TemplateSecret, credentialpolicy.TemplateEnv, credentialpolicy.TemplateDynamic:
			return true
		case credentialpolicy.TemplateOther:
			namespace, _, found := strings.Cut(strings.TrimSpace(expression), ".")
			if !found {
				return false
			}
			switch strings.ToLower(strings.TrimSpace(namespace)) {
			case "data", "param", "fn", "fake", "file":
				return true
			}
		}
		return false
	}
	for index, header := range headers {
		if !credential.SensitiveHeader(header.Name) || !strings.Contains(header.Value, "{{") {
			continue
		}
		kind, _ := credentialpolicy.ClassifyWholeTemplate(header.Value, nil)
		if kind != credentialpolicy.TemplateNone {
			continue
		}
		safeComposition := false
		switch strings.ToLower(strings.TrimSpace(header.Name)) {
		case "authorization", "proxy-authorization":
			scheme, value, found := strings.Cut(strings.TrimSpace(header.Value), " ")
			switch strings.ToLower(strings.TrimSpace(scheme)) {
			case "basic", "bearer", "digest":
				authKind, _ := credentialpolicy.ClassifyWholeTemplate(value, nil)
				safeComposition = found &&
					(authKind == credentialpolicy.TemplateSecret || authKind == credentialpolicy.TemplateEnv)
			}
		case "cookie":
			safeComposition = true
			for _, pair := range strings.Split(header.Value, ";") {
				name, value, found := strings.Cut(strings.TrimSpace(pair), "=")
				if !found || strings.TrimSpace(name) == "" || !wholeRuntimeValue(value) {
					safeComposition = false
					break
				}
			}
		}
		if !safeComposition {
			continue
		}
		if governed == nil {
			governed = make(map[int]struct{})
		}
		governed[index] = struct{}{}
	}
	return governed
}

func stringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		return typed
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

// responseBodyAssertions derives body_contains assertions from the declared
// success-response schema: each required top-level property of a JSON response
// must appear (as a quoted key) in the body. Capped to keep assertion noise low.
func responseBodyAssertions(resolved *schema) []dsl.Assertion {
	const maxBodyAssertions = 3
	if resolved == nil {
		return nil
	}
	required := resolved.Required
	if len(required) == 0 {
		return nil
	}
	sort.Strings(required)
	assertions := make([]dsl.Assertion, 0, maxBodyAssertions)
	for _, property := range required {
		property = strings.TrimSpace(property)
		if property == "" {
			continue
		}
		if _, declared := resolved.Properties[property]; !declared {
			continue
		}
		assertions = append(assertions, dsl.Assertion{Type: "body_contains", Value: `"` + property + `"`})
		if len(assertions) == maxBodyAssertions {
			break
		}
	}
	return assertions
}

type rawJSONTemplate string

func firstMediaTypeExample(examples orderedMediaExamples) (any, bool, []string) {
	if len(examples) == 0 {
		return nil, false, nil
	}
	reviewCodes := duplicateMediaExampleReviewCodes(examples)
	value, present, objectReviewCodes := mediaExampleObjectValue(examples[0].Object)
	reviewCodes = append(reviewCodes, objectReviewCodes...)
	return value, present, uniqueSorted(reviewCodes)
}

func duplicateMediaExampleReviewCodes(examples orderedMediaExamples) []string {
	seen := make(map[string]struct{}, len(examples))
	for _, example := range examples {
		if _, duplicate := seen[example.Name]; duplicate {
			return []string{"duplicate_media_example_name"}
		}
		seen[example.Name] = struct{}{}
	}
	return nil
}

func mediaExampleObjectValue(raw any) (any, bool, []string) {
	object, ok := mediaExampleObjectMap(raw)
	if !ok {
		return nil, false, []string{"invalid_media_example_object"}
	}
	value, hasValue := object["value"]
	_, hasRef := object["$ref"]
	_, hasExternalValue := object["externalValue"]
	if hasValue {
		if hasRef || hasExternalValue {
			return value, true, []string{"conflicting_media_example_sources"}
		}
		return value, true, nil
	}
	switch {
	case hasRef:
		return nil, false, []string{"unsupported_media_example_reference"}
	case hasExternalValue:
		return nil, false, []string{"unsupported_media_example_external_value"}
	default:
		return nil, false, []string{"media_example_value_missing"}
	}
}

func mediaExampleObjectMap(raw any) (map[string]any, bool) {
	switch typed := raw.(type) {
	case map[string]any:
		return typed, true
	case map[any]any:
		object := make(map[string]any, len(typed))
		for key, value := range typed {
			name, ok := key.(string)
			if !ok {
				return nil, false
			}
			object[name] = value
		}
		return object, true
	default:
		return nil, false
	}
}

func requestBodyExample(body *requestBody, res resolver, location string, columns columnRegistry) (string, string, []string, []string, map[string]string) {
	if body == nil || len(body.Content) == 0 {
		return "", "", nil, nil, nil
	}
	contentTypes := sortedMapKeys(body.Content)
	preferred := preferredContentType(contentTypes)
	media := body.Content[preferred]
	resolvedMediaSchema := res.resolveSchema(media.Schema, location+" request body", map[string]bool{})
	sensitiveNames := openAPISchemaCredentialNames(media.Schema, res, location)
	encodingReviewCodes := openAPIEncodingReviewCodes(preferred, media.Encoding)
	if media.Example != nil {
		if strings.Contains(strings.ToLower(preferred), "xml") {
			rendered, reviewCodes := renderStructuredXMLExample(media.Example, resolvedMediaSchema)
			return rendered, preferred, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
		}
		if strings.Contains(strings.ToLower(preferred), "multipart/") {
			rendered, contentType, reviewCodes := renderAuthoredMultipartExample(media.Example, preferred, media.Schema, res, location)
			return rendered, contentType, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
		}
		rendered, reviewCodes := renderGovernedExample(media.Example, preferred)
		return rendered, preferred, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
	}
	if len(media.Examples) > 0 {
		example, present, exampleReviewCodes := firstMediaTypeExample(media.Examples)
		if !present {
			return "", preferred, nil, append(exampleReviewCodes, encodingReviewCodes...), sensitiveNames
		}
		if strings.Contains(strings.ToLower(preferred), "xml") {
			rendered, reviewCodes := renderStructuredXMLExample(example, resolvedMediaSchema)
			reviewCodes = append(reviewCodes, exampleReviewCodes...)
			return rendered, preferred, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
		}
		if strings.Contains(strings.ToLower(preferred), "multipart/") {
			rendered, contentType, reviewCodes := renderAuthoredMultipartExample(example, preferred, media.Schema, res, location)
			reviewCodes = append(reviewCodes, exampleReviewCodes...)
			return rendered, contentType, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
		}
		rendered, reviewCodes := renderGovernedExample(example, preferred)
		reviewCodes = append(reviewCodes, exampleReviewCodes...)
		return rendered, preferred, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
	}
	if resolvedMediaSchema.Example != nil {
		if strings.Contains(strings.ToLower(preferred), "xml") {
			rendered, reviewCodes := renderStructuredXMLExample(resolvedMediaSchema.Example, resolvedMediaSchema)
			return rendered, preferred, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
		}
		if strings.Contains(strings.ToLower(preferred), "multipart/") {
			rendered, contentType, reviewCodes := renderAuthoredMultipartExample(resolvedMediaSchema.Example, preferred, media.Schema, res, location)
			return rendered, contentType, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
		}
		rendered, reviewCodes := renderGovernedExample(resolvedMediaSchema.Example, preferred)
		return rendered, preferred, nil, append(reviewCodes, encodingReviewCodes...), sensitiveNames
	}
	if strings.Contains(preferred, "application/x-www-form-urlencoded") {
		reviewCodes := append(sensitiveNonStringSchemaCodes(media.Schema, res, location), encodingReviewCodes...)
		return formURLEncodedExample(media.Schema, res, location, columns), preferred, formColumns(media.Schema, res, location, columns), reviewCodes, sensitiveNames
	}
	if strings.Contains(preferred, "multipart/form-data") {
		body, cols, multipartReviewCodes := multipartFormExample(media.Schema, res, location, columns)
		contentType := preferred
		if !strings.Contains(strings.ToLower(contentType), "boundary=") {
			contentType += "; boundary=----apexload-openapi-boundary"
		}
		reviewCodes := append(sensitiveNonStringSchemaCodes(media.Schema, res, location), multipartReviewCodes...)
		reviewCodes = append(reviewCodes, encodingReviewCodes...)
		return body, contentType, cols, reviewCodes, sensitiveNames
	}
	payload, cols := sampleForSchema(media.Schema, res, location, map[string]bool{}, columns)
	if payload == nil {
		return "", preferred, nil, encodingReviewCodes, sensitiveNames
	}
	if strings.Contains(preferred, "xml") {
		body := xmlForSchema(media.Schema, res, location, columns)
		reviewCodes := append(sensitiveNonStringSchemaCodes(media.Schema, res, location), encodingReviewCodes...)
		return body, preferred, cols, reviewCodes, sensitiveNames
	}
	encoded, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", preferred, cols, encodingReviewCodes, sensitiveNames
	}
	reviewCodes := append(sensitiveNonStringSchemaCodes(media.Schema, res, location), encodingReviewCodes...)
	return expandRawJSONTemplates(string(encoded)), preferred, cols, reviewCodes, sensitiveNames
}

var openAPISafeXMLName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)

func renderStructuredXMLExample(value any, root schema) (string, []string) {
	if text, ok := value.(string); ok {
		return text, nil
	}
	rootName := ""
	if root.XML != nil {
		rootName = strings.TrimSpace(root.XML.Name)
	}
	// A structured top-level value has no property name to supply OpenAPI's
	// ordinary XML element fallback. Inventing <root> changes the authored wire
	// body, so only serialize when the schema declares the document element.
	if rootName == "" {
		return "", []string{"unsupported_structured_xml_example"}
	}
	var output strings.Builder
	if values, ok := value.([]any); ok {
		// A top-level array needs an authored wrapper to form one valid XML
		// document. Rendering an unwrapped array as sibling roots, or routing a
		// wrapped array through the ordinary element writer, would respectively
		// produce invalid XML or a duplicate wrapper.
		if !root.XML.Wrapped || root.Items == nil || root.Items.XML == nil || strings.TrimSpace(root.Items.XML.Name) == "" {
			return "", []string{"unsupported_structured_xml_example"}
		}
		if err := writeOpenAPIStructuredXMLArray(&output, rootName, values, root, true); err != nil {
			return "", []string{"unsupported_structured_xml_example"}
		}
		return output.String(), nil
	}
	if err := writeOpenAPIStructuredXML(&output, rootName, value, root); err != nil {
		return "", []string{"unsupported_structured_xml_example"}
	}
	return output.String(), nil
}

const openAPIPredefinedXMLNamespace = "http://www.w3.org/XML/1998/namespace"

type openAPIXMLNamespaceContext map[string]string

func newOpenAPIXMLNamespaceContext() openAPIXMLNamespaceContext {
	return openAPIXMLNamespaceContext{"xml": openAPIPredefinedXMLNamespace}
}

func cloneOpenAPIXMLNamespaceContext(parent openAPIXMLNamespaceContext) openAPIXMLNamespaceContext {
	clone := make(openAPIXMLNamespaceContext, len(parent)+1)
	for prefix, namespace := range parent {
		clone[prefix] = namespace
	}
	if _, ok := clone["xml"]; !ok {
		clone["xml"] = openAPIPredefinedXMLNamespace
	}
	return clone
}

func writeOpenAPIStructuredXML(output *strings.Builder, fallbackName string, value any, current schema) error {
	return writeOpenAPIStructuredXMLWithNamespaces(output, fallbackName, value, current, newOpenAPIXMLNamespaceContext())
}

func writeOpenAPIStructuredXMLWithNamespaces(
	output *strings.Builder,
	fallbackName string,
	value any,
	current schema,
	parentNamespaces openAPIXMLNamespaceContext,
) error {
	name := fallbackName
	metadata := current.XML
	if current.XML != nil {
		name = convertutil.FirstNonEmpty(strings.TrimSpace(current.XML.Name), name)
	}
	qualified, namespaces, err := writeOpenAPIStructuredXMLElementStart(output, name, metadata, parentNamespaces)
	if err != nil {
		return err
	}
	object, isObject := openAPIStructuredXMLObject(value)
	if isObject {
		attributeNames := make(map[string]struct{})
		for _, propertyName := range sortedSchemaPropertyKeys(current.Properties) {
			property := current.Properties[propertyName]
			if property.XML == nil || !property.XML.Attribute {
				continue
			}
			attributeValue, present := object[propertyName]
			if !present || attributeValue == nil {
				continue
			}
			if _, complex := openAPIStructuredXMLObject(attributeValue); complex {
				return fmt.Errorf("complex XML attribute")
			}
			if _, complex := attributeValue.([]any); complex {
				return fmt.Errorf("array XML attribute")
			}
			attributeName := convertutil.FirstNonEmpty(strings.TrimSpace(property.XML.Name), propertyName)
			attributePrefix := strings.TrimSpace(property.XML.Prefix)
			if !openAPISafeXMLName.MatchString(attributeName) || attributeName == "xmlns" ||
				(attributePrefix != "" && (!openAPISafeXMLName.MatchString(attributePrefix) || attributePrefix == "xmlns")) {
				return fmt.Errorf("invalid XML attribute name")
			}
			attributeNamespace := strings.TrimSpace(property.XML.Namespace)
			if attributeNamespace != "" && attributePrefix == "" {
				return fmt.Errorf("namespaced XML attribute requires a prefix")
			}
			resolvedNamespace, err := ensureOpenAPIXMLNamespaceBinding(output, namespaces, attributePrefix, attributeNamespace, true)
			if err != nil {
				return err
			}
			attributeKey := resolvedNamespace + "\x00" + attributeName
			if _, duplicate := attributeNames[attributeKey]; duplicate {
				return fmt.Errorf("duplicate XML attribute name")
			}
			attributeNames[attributeKey] = struct{}{}
			output.WriteByte(' ')
			if attributePrefix != "" {
				output.WriteString(attributePrefix + ":")
			}
			output.WriteString(attributeName)
			output.WriteString(`="`)
			if err := xml.EscapeText(output, []byte(fmt.Sprint(attributeValue))); err != nil {
				return err
			}
			output.WriteByte('"')
		}
	}
	output.WriteByte('>')
	if isObject {
		for _, childName := range sortedMapKeys(object) {
			child := object[childName]
			property, declared := current.Properties[childName]
			if declared && property.XML != nil && property.XML.Attribute {
				continue
			}
			if values, ok := child.([]any); ok {
				if err := writeOpenAPIStructuredXMLArrayWithNamespaces(output, childName, values, property, declared, namespaces); err != nil {
					return err
				}
				continue
			}
			if err := writeOpenAPIStructuredXMLWithNamespaces(output, childName, child, property, namespaces); err != nil {
				return err
			}
		}
	} else if values, ok := value.([]any); ok {
		if err := writeOpenAPIStructuredXMLArrayWithNamespaces(output, fallbackName, values, current, true, namespaces); err != nil {
			return err
		}
	} else if value != nil {
		if err := xml.EscapeText(output, []byte(fmt.Sprint(value))); err != nil {
			return err
		}
	}
	output.WriteString("</")
	output.WriteString(qualified)
	output.WriteByte('>')
	return nil
}

func writeOpenAPIStructuredXMLArray(output *strings.Builder, fallbackName string, values []any, arraySchema schema, declared bool) error {
	return writeOpenAPIStructuredXMLArrayWithNamespaces(output, fallbackName, values, arraySchema, declared, newOpenAPIXMLNamespaceContext())
}

func writeOpenAPIStructuredXMLArrayWithNamespaces(
	output *strings.Builder,
	fallbackName string,
	values []any,
	arraySchema schema,
	declared bool,
	parentNamespaces openAPIXMLNamespaceContext,
) error {
	itemSchema := schema{}
	if arraySchema.Items != nil {
		itemSchema = *arraySchema.Items
	}
	if declared && arraySchema.XML != nil && arraySchema.XML.Wrapped {
		wrapper := arraySchema
		wrapper.Type = "object"
		wrapper.Items = nil
		wrapper.Properties = nil
		name := fallbackName
		if wrapper.XML != nil {
			name = convertutil.FirstNonEmpty(strings.TrimSpace(wrapper.XML.Name), name)
		}
		qualified, namespaces, err := writeOpenAPIStructuredXMLElementStart(output, name, wrapper.XML, parentNamespaces)
		if err != nil {
			return err
		}
		output.WriteByte('>')
		itemName := fallbackName
		if itemSchema.XML != nil {
			itemName = convertutil.FirstNonEmpty(strings.TrimSpace(itemSchema.XML.Name), itemName)
		}
		for _, item := range values {
			if err := writeOpenAPIStructuredXMLWithNamespaces(output, itemName, item, itemSchema, namespaces); err != nil {
				return err
			}
		}
		output.WriteString("</" + qualified + ">")
		return nil
	}
	itemName := fallbackName
	if itemSchema.XML != nil {
		itemName = convertutil.FirstNonEmpty(strings.TrimSpace(itemSchema.XML.Name), itemName)
	}
	for _, item := range values {
		if err := writeOpenAPIStructuredXMLWithNamespaces(output, itemName, item, itemSchema, parentNamespaces); err != nil {
			return err
		}
	}
	return nil
}

func writeOpenAPIStructuredXMLElementStart(
	output *strings.Builder,
	name string,
	metadata *xmlObject,
	parentNamespaces openAPIXMLNamespaceContext,
) (string, openAPIXMLNamespaceContext, error) {
	prefix := ""
	namespace := ""
	if metadata != nil {
		prefix = strings.TrimSpace(metadata.Prefix)
		namespace = strings.TrimSpace(metadata.Namespace)
	}
	if !openAPISafeXMLName.MatchString(name) || name == "xmlns" ||
		(prefix != "" && (!openAPISafeXMLName.MatchString(prefix) || prefix == "xmlns")) {
		return "", nil, fmt.Errorf("invalid XML name")
	}
	qualified := name
	if prefix != "" {
		qualified = prefix + ":" + name
	}
	namespaces := cloneOpenAPIXMLNamespaceContext(parentNamespaces)
	output.WriteByte('<')
	output.WriteString(qualified)
	if _, err := ensureOpenAPIXMLNamespaceBinding(output, namespaces, prefix, namespace, false); err != nil {
		return "", nil, err
	}
	return qualified, namespaces, nil
}

func ensureOpenAPIXMLNamespaceBinding(
	output *strings.Builder,
	namespaces openAPIXMLNamespaceContext,
	prefix string,
	namespace string,
	attribute bool,
) (string, error) {
	prefix = strings.TrimSpace(prefix)
	namespace = strings.TrimSpace(namespace)
	if prefix == "xmlns" {
		return "", fmt.Errorf("reserved XML namespace prefix")
	}
	if prefix == "xml" {
		if namespace != "" && namespace != openAPIPredefinedXMLNamespace {
			return "", fmt.Errorf("conflicting XML namespace prefix")
		}
		return openAPIPredefinedXMLNamespace, nil
	}
	if prefix != "" {
		bound, exists := namespaces[prefix]
		if namespace == "" {
			if !exists || bound == "" {
				return "", fmt.Errorf("unbound XML namespace prefix")
			}
			return bound, nil
		}
		if exists {
			if bound != namespace {
				return "", fmt.Errorf("conflicting XML namespace prefix")
			}
			return bound, nil
		}
		output.WriteString(` xmlns:` + prefix + `="`)
		if err := xml.EscapeText(output, []byte(namespace)); err != nil {
			return "", err
		}
		output.WriteByte('"')
		namespaces[prefix] = namespace
		return namespace, nil
	}
	if attribute {
		if namespace != "" {
			return "", fmt.Errorf("namespaced XML attribute requires a prefix")
		}
		return "", nil
	}
	if namespace == "" {
		return namespaces[""], nil
	}
	if namespaces[""] == namespace {
		return namespace, nil
	}
	output.WriteString(` xmlns="`)
	if err := xml.EscapeText(output, []byte(namespace)); err != nil {
		return "", err
	}
	output.WriteByte('"')
	namespaces[""] = namespace
	return namespace, nil
}

func openAPIStructuredXMLObject(value any) (map[string]any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, true
	case map[any]any:
		out := make(map[string]any, len(typed))
		for key, item := range typed {
			name, ok := key.(string)
			if !ok {
				return nil, false
			}
			out[name] = item
		}
		return out, true
	default:
		return nil, false
	}
}

func openAPIEncodingReviewCodes(contentType string, encoding map[string]any) []string {
	if len(encoding) == 0 {
		return nil
	}
	if strings.Contains(strings.ToLower(contentType), "multipart/") {
		return []string{"unsupported_multipart_encoding"}
	}
	return []string{"unsupported_request_body_encoding"}
}

func openAPISchemaCredentialNames(root schema, res resolver, location string) map[string]string {
	root = res.resolveSchema(root, location, map[string]bool{})
	names := map[string]string{}
	var walk func(schema)
	walk = func(current schema) {
		for _, name := range sortedSchemaPropertyKeys(current.Properties) {
			child := current.Properties[name]
			if schemaCredentialField(name, child) && !credential.SensitiveName(name) {
				names[strings.ToLower(strings.TrimSpace(name))] = name
			}
			walk(child)
		}
		if current.Items != nil {
			walk(*current.Items)
		}
	}
	walk(root)
	if len(names) == 0 {
		return nil
	}
	return names
}

func openAPIRequestBodyIsWholeCredential(
	body *requestBody,
	res resolver,
	location string,
) bool {
	if body == nil || len(body.Content) == 0 {
		return false
	}
	contentType := preferredContentType(sortedMapKeys(body.Content))
	media := body.Content[contentType]
	root := res.resolveSchema(media.Schema, location+" request body", map[string]bool{})
	return schemaCredentialField("", root)
}

func openAPIParametersDeclareWholeBodyCredential(params []parameter) bool {
	for _, param := range params {
		if !strings.EqualFold(strings.TrimSpace(param.In), "body") {
			continue
		}
		if schemaCredentialField(strings.TrimSpace(param.Name), parameterSchema(param)) {
			return true
		}
	}
	return false
}

func schemaCredentialField(name string, current schema) bool {
	return credential.SensitiveName(name) ||
		strings.EqualFold(strings.TrimSpace(current.Format), "password")
}

func sampleForSchema(s schema, res resolver, location string, seen map[string]bool, columns columnRegistry) (any, []string) {
	s = res.resolveSchema(s, location, seen)
	if len(s.Properties) > 0 || s.Type == "object" {
		result := map[string]any{}
		out := []string{}
		for _, name := range sortedSchemaPropertyKeys(s.Properties) {
			prop := res.resolveSchema(s.Properties[name], location+"."+name, copySeen(seen))
			value, nested := sampleForSchema(prop, res, location+"."+name, copySeen(seen), columns)
			column := sanitizeColumnName(name)
			if scalarSchema(prop) {
				if schemaCredentialField(name, prop) {
					if authored, ok := authoredSchemaValue(prop); ok {
						value = authored
					} else if schemaNeedsRawJSONPlaceholder(prop) {
						value = zeroValueForSensitiveSchema(prop)
					} else {
						value = openAPICredentialReference(name)
					}
				} else {
					columns.add(column, prop)
					placeholder := "{{data.openapi." + column + "}}"
					if schemaNeedsRawJSONPlaceholder(prop) {
						value = rawJSONTemplate(rawJSONTemplateMarker(column))
					} else {
						value = placeholder
					}
					out = append(out, column)
				}
			}
			result[name] = value
			out = append(out, nested...)
		}
		if sub, ok := additionalPropertiesSchema(s); ok {
			column := "additional_value"
			result["additionalProperty"] = "{{data.openapi." + column + "}}"
			colSchema := sub
			if strings.TrimSpace(colSchema.Type) == "" {
				colSchema = schema{Type: "string"}
			}
			columns.add(column, colSchema)
			out = append(out, column)
		}
		return result, uniqueSorted(out)
	}
	if s.Items != nil || s.Type == "array" {
		value, columns := sampleForSchema(valueOrEmptySchema(s.Items), res, location+"[]", copySeen(seen), columns)
		return []any{value}, columns
	}
	switch strings.ToLower(s.Type) {
	case "integer":
		return 42, nil
	case "number":
		return 42.5, nil
	case "boolean":
		return true, nil
	case "string":
		if len(s.Enum) > 0 {
			return s.Enum[0], nil
		}
		switch strings.ToLower(s.Format) {
		case "date-time":
			// fn.now is not an engine builtin; fn.timestamp.iso8601 is. Emitting a
			// non-existent function ships the literal "{{fn.now()}}" in the body.
			return "{{fn.timestamp.iso8601}}", nil
		case "date":
			// The engine has no date-only formatter; use a static placeholder.
			return "2025-01-01", nil
		case "uuid":
			return "{{fn.uuid()}}", nil
		case "email":
			return "user@example.com", nil
		case "uri", "url":
			return "https://example.com/resource", nil
		case "hostname":
			return "example.com", nil
		case "ipv4":
			return "192.168.1.1", nil
		case "ipv6":
			return "2001:db8::1", nil
		case "password":
			return "SecretPass123!", nil
		default:
			return "sample_string", nil
		}
	default:
		return map[string]any{}, nil
	}
}

func formURLEncodedExample(s schema, res resolver, location string, columns columnRegistry) string {
	resolved := res.resolveSchema(s, location, map[string]bool{})
	fields := schemaFieldNames(s, res, location)
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		column := sanitizeColumnName(field)
		if schemaCredentialField(field, resolved.Properties[field]) {
			value := openAPICredentialReference(field)
			if authored, ok := authoredSchemaWireValue(resolved.Properties[field]); ok {
				value = url.QueryEscape(authored)
			}
			parts = append(parts, escapeQueryKey(field)+"="+value)
			continue
		}
		columns.add(column, resolved.Properties[field])
		parts = append(parts, escapeQueryKey(field)+"={{data.openapi."+column+"}}")
	}
	return strings.Join(parts, "&")
}

func formColumns(s schema, res resolver, location string, columns columnRegistry) []string {
	fields := schemaFieldNames(s, res, location)
	out := make([]string, 0, len(fields))
	resolved := res.resolveSchema(s, location, map[string]bool{})
	for _, field := range fields {
		if schemaCredentialField(field, resolved.Properties[field]) {
			continue
		}
		column := sanitizeColumnName(field)
		columns.add(column, resolved.Properties[field])
		out = append(out, column)
	}
	return uniqueSorted(out)
}

func multipartFormExample(s schema, res resolver, location string, columns columnRegistry) (string, []string, []string) {
	const boundary = "----apexload-openapi-boundary"
	fields := res.resolveSchema(s, location, map[string]bool{})
	var b strings.Builder
	out := []string{}
	var reviewCodes []string
	for index, name := range sortedSchemaPropertyKeys(fields.Properties) {
		prop := res.resolveSchema(fields.Properties[name], location+"."+name, map[string]bool{})
		column := sanitizeColumnName(name)
		sensitive := schemaCredentialField(name, prop)
		wireName := name
		if validMultipartExampleFieldName(name) {
			wireName = escapeMultipartExampleFieldName(name)
		} else {
			wireName = fmt.Sprintf("blocked_field_%d", index+1)
			reviewCodes = append(reviewCodes, "invalid_multipart_schema_field_name")
			res.add(diag.Warning, "OpenAPI multipart field", location, "invalid multipart schema field metadata was replaced in a blocked draft")
		}
		if !sensitive {
			columns.add(column, prop)
			out = append(out, column)
		}
		b.WriteString("--" + boundary + "\r\n")
		if strings.EqualFold(prop.Format, "binary") {
			b.WriteString(`Content-Disposition: form-data; name="` + wireName + `"; filename="{{data.openapi.` + column + `}}"` + "\r\n")
			b.WriteString("Content-Type: application/octet-stream\r\n\r\n")
			b.WriteString("{{file." + column + "}}\r\n")
			reviewCodes = append(reviewCodes, "multipart_binary_binding_required")
			res.add(diag.Warning, "OpenAPI multipart file", location, "property "+name+" is format: binary; file bytes cannot be generated from the spec and must be supplied before replay")
			continue
		}
		if multipartComplexSchema(prop) {
			// A data-column string is a disclosure-safe placeholder, but it
			// cannot express OpenAPI's style/explode rules for object or array
			// parts without an explicit, supported encoder.
			reviewCodes = append(reviewCodes, "unsupported_multipart_complex_encoding")
		}
		b.WriteString(`Content-Disposition: form-data; name="` + wireName + `"` + "\r\n\r\n")
		if sensitive {
			value := openAPICredentialReference(name)
			if authored, ok := authoredSchemaWireValue(prop); ok {
				value = authored
			}
			b.WriteString(value + "\r\n")
		} else {
			b.WriteString("{{data.openapi." + column + "}}\r\n")
		}
	}
	if len(fields.Properties) == 0 {
		return "", nil, nil
	}
	b.WriteString("--" + boundary + "--\r\n")
	return b.String(), uniqueSorted(out), uniqueSorted(reviewCodes)
}

func multipartComplexSchema(s schema) bool {
	typeName := strings.ToLower(strings.TrimSpace(s.Type))
	if typeName == "object" || typeName == "array" || len(s.Properties) > 0 || s.Items != nil {
		return true
	}
	if _, ok := additionalPropertiesSchema(s); ok {
		return true
	}
	return len(s.OneOf) > 0 || len(s.AnyOf) > 0 || len(s.AllOf) > 0
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

func renderGovernedExample(value any, contentType string) (string, []string) {
	lowerContentType := strings.ToLower(contentType)
	if strings.Contains(lowerContentType, "application/x-www-form-urlencoded") {
		fields, ok := value.(map[string]any)
		if !ok {
			return "", []string{"unsupported_form_example_shape"}
		}
		parts := make([]string, 0, len(fields))
		for _, name := range sortedMapKeys(fields) {
			switch fields[name].(type) {
			case nil, bool, float32, float64, int, int8, int16, int32, int64,
				uint, uint8, uint16, uint32, uint64, json.Number, string:
				parts = append(parts, escapeQueryKey(name)+"="+url.QueryEscape(fmt.Sprint(fields[name])))
			default:
				return "", []string{"unsupported_form_example_shape"}
			}
		}
		return strings.Join(parts, "&"), nil
	}
	if strings.Contains(lowerContentType, "json") {
		projected, unsafe := projectJSONExampleCredentials(value, nil)
		encoded, err := json.MarshalIndent(projected, "", "  ")
		if err != nil {
			return scrub.Value(renderExample(value, contentType), scrub.Policy{}), []string{"malformed_authored_example"}
		}
		if unsafe {
			return string(encoded), []string{"sensitive_non_string_example"}
		}
		return string(encoded), nil
	}
	if strings.Contains(lowerContentType, "xml") {
		// The shared XML projector performs byte-offset replacement after the
		// effective request is assembled, preserving namespaces and all
		// nonsensitive envelope bytes.
		if rendered, ok := value.(string); ok {
			return rendered, nil
		}
	}
	rendered := renderExample(value, contentType)
	// The shared Preserve-mode projector classifies raw text after the effective
	// request is assembled. It retains the exact source bytes, records a review
	// finding and a runtime-redaction locator, and leaves disclosure projection
	// responsible for masking API/source-review output. Gating this complete
	// scalar here would make an otherwise faithful import unnecessarily
	// non-executable.
	return rendered, nil
}

type authoredMultipartPart struct {
	name        string
	filename    string
	body        string
	contentType string
}

func renderAuthoredMultipartExample(value any, contentType string, root schema, res resolver, location string) (string, string, []string) {
	fields, ok := value.(map[string]any)
	if !ok {
		return "", multipartContentTypeWithBoundary(contentType, "----apexload-openapi-boundary"), []string{"unsupported_multipart_example_shape"}
	}
	resolvedRoot := res.resolveSchema(root, location, map[string]bool{})
	parts := make([]authoredMultipartPart, 0, len(fields))
	var reviewCodes []string
	for _, name := range sortedMapKeys(fields) {
		if !validMultipartExampleFieldName(name) {
			return "", multipartContentTypeWithBoundary(contentType, "----apexload-openapi-boundary"), []string{"unsupported_multipart_example_field_name"}
		}
		property, declared := resolvedRoot.Properties[name]
		if declared {
			property = res.resolveSchema(property, location+"."+name, map[string]bool{})
		}
		body, partContentType, filename, valueReviewCodes, supported := authoredMultipartValue(fields[name], property, declared, name)
		reviewCodes = append(reviewCodes, valueReviewCodes...)
		if !supported {
			reviewCodes = append(reviewCodes, "unsupported_multipart_example_value")
			return "", multipartContentTypeWithBoundary(contentType, "----apexload-openapi-boundary"), uniqueSorted(reviewCodes)
		}
		parts = append(parts, authoredMultipartPart{
			name:        name,
			filename:    filename,
			body:        body,
			contentType: partContentType,
		})
	}
	boundary := "----apexload-openapi-boundary"
	for attempt := 1; multipartExampleBoundaryOccurs(boundary, parts); attempt++ {
		boundary = fmt.Sprintf("----apexload-openapi-boundary-%d", attempt)
	}
	var body strings.Builder
	for _, part := range parts {
		body.WriteString("--" + boundary + "\r\n")
		body.WriteString(`Content-Disposition: form-data; name="` + escapeMultipartExampleFieldName(part.name) + `"`)
		if part.filename != "" {
			body.WriteString(`; filename="` + escapeMultipartExampleFieldName(part.filename) + `"`)
		}
		body.WriteString("\r\n")
		if part.contentType != "" {
			body.WriteString("Content-Type: " + part.contentType + "\r\n")
		}
		body.WriteString("\r\n")
		body.WriteString(part.body)
		body.WriteString("\r\n")
	}
	body.WriteString("--" + boundary + "--\r\n")
	return body.String(), multipartContentTypeWithBoundary(contentType, boundary), uniqueSorted(reviewCodes)
}

func authoredMultipartValue(value any, property schema, declared bool, fieldName string) (string, string, string, []string, bool) {
	if declared && strings.EqualFold(property.Format, "binary") {
		assetName := sanitizeColumnName(fieldName)
		return "{{file." + assetName + "}}", "application/octet-stream", assetName + ".bin",
			[]string{"multipart_binary_binding_required"}, true
	}
	var reviewCodes []string
	if (declared && multipartComplexSchema(property)) || multipartComplexValue(value) {
		reviewCodes = append(reviewCodes, "unsupported_multipart_complex_encoding")
	}
	switch typed := value.(type) {
	case nil:
		return "", "", "", reviewCodes, true
	case string:
		return typed, "", "", reviewCodes, true
	case bool:
		return fmt.Sprint(typed), "", "", reviewCodes, true
	case float32, float64, int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64, json.Number:
		return fmt.Sprint(typed), "", "", reviewCodes, true
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return "", "", "", reviewCodes, false
		}
		return string(encoded), "application/json", "", reviewCodes, true
	default:
		return "", "", "", reviewCodes, false
	}
}

func multipartComplexValue(value any) bool {
	switch value.(type) {
	case map[string]any, map[any]any, []any:
		return true
	default:
		return false
	}
}

func validMultipartExampleFieldName(name string) bool {
	if name == "" {
		return false
	}
	for _, current := range name {
		if current < 0x20 || current == 0x7f {
			return false
		}
	}
	return true
}

func escapeMultipartExampleFieldName(name string) string {
	return strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(name)
}

func multipartExampleBoundaryOccurs(boundary string, parts []authoredMultipartPart) bool {
	for _, part := range parts {
		if strings.Contains(part.body, "--"+boundary) {
			return true
		}
	}
	return false
}

func multipartContentTypeWithBoundary(contentType, boundary string) string {
	mediaType := strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0])
	if mediaType == "" {
		mediaType = "multipart/form-data"
	}
	return mediaType + "; boundary=" + boundary
}

func projectJSONExampleCredentials(value any, path []string) (any, bool) {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		unsafe := false
		for key, child := range typed {
			childPath := append(append([]string(nil), path...), key)
			if credential.SensitiveName(key) {
				// Authored examples are executable source data. Preserve their
				// exact JSON type and value; the shared projector records the
				// review finding without converting a number, boolean, object,
				// or array to a placeholder the operator must reconstruct.
				out[key] = child
				continue
			}
			projected, childUnsafe := projectJSONExampleCredentials(child, childPath)
			out[key] = projected
			unsafe = unsafe || childUnsafe
		}
		return out, unsafe
	case []any:
		out := make([]any, len(typed))
		unsafe := false
		for index, child := range typed {
			projected, childUnsafe := projectJSONExampleCredentials(child, path)
			out[index] = projected
			unsafe = unsafe || childUnsafe
		}
		return out, unsafe
	default:
		return value, false
	}
}

func sensitiveNonStringSchemaCodes(root schema, res resolver, location string) []string {
	var unsafe bool
	var walk func(schema, string, map[string]bool)
	walk = func(current schema, field string, seen map[string]bool) {
		if unsafe {
			return
		}
		current = res.resolveSchema(current, location, seen)
		if schemaCredentialField(field, current) && schemaNeedsRawJSONPlaceholder(current) {
			// An authored example/default/enum supplies the exact typed wire
			// value. Preserve-mode JSON projection records a pointer locator
			// without converting it to a string. A typed credential with no
			// authored value still needs a blocked zero-value draft because
			// there is no safe executable value to synthesize.
			if _, authored := authoredSchemaValue(current); !authored {
				unsafe = true
				return
			}
		}
		for _, name := range sortedSchemaPropertyKeys(current.Properties) {
			walk(current.Properties[name], name, copySeen(seen))
		}
		if current.Items != nil {
			walk(*current.Items, field, copySeen(seen))
		}
	}
	walk(root, "", map[string]bool{})
	if unsafe {
		return []string{"sensitive_non_string_schema"}
	}
	return nil
}

func zeroValueForSensitiveSchema(s schema) any {
	switch strings.ToLower(strings.TrimSpace(s.Type)) {
	case "boolean":
		return false
	case "integer":
		return 0
	case "number":
		return 0.0
	default:
		return nil
	}
}

func openAPICredentialReference(parts ...string) string {
	var tokens []string
	for _, part := range parts {
		part = strings.ToUpper(sanitizeColumnName(part))
		part = strings.Trim(part, "_")
		if part != "" {
			tokens = append(tokens, part)
		}
	}
	if len(tokens) == 0 {
		tokens = []string{"IMPORTED_SECRET"}
	}
	return "{{secret.OPENAPI_" + strings.Join(tokens, "_") + "}}"
}

func applyOpenAPIReviewGate(action *dsl.Action, reasonCodes []string, sourceRef string) {
	if action == nil || len(reasonCodes) == 0 {
		return
	}
	sourceRefs := []string{sourceRef}
	if action.Metadata != nil {
		if existing, ok := action.Metadata["import_review"].(map[string]any); ok {
			reasonCodes = append(reasonCodes, stringSlice(existing["reason_codes"])...)
			sourceRefs = append(sourceRefs, stringSlice(existing["source_refs"])...)
		}
	}
	reasonCodes = uniqueSorted(reasonCodes)
	sourceRefs = uniqueSorted(sourceRefs)
	findings := make([]importreview.Finding, 0, len(reasonCodes))
	for _, code := range reasonCodes {
		findings = append(findings, importreview.Finding{
			Code:       code,
			Severity:   diag.Blocker,
			Element:    action.Name,
			Detail:     "OpenAPI source behavior requires translation before execution",
			Location:   sourceRef,
			Gate:       importreview.GateExecution,
			ActionPath: "",
		})
	}
	importreview.ApplyToAction(action, findings)
	if action.Metadata == nil {
		action.Metadata = map[string]any{}
	}
	action.Metadata["execution_blocker"] = "import_review_required"
	blockers, _ := action.Metadata["execution_blockers"].([]string)
	action.Metadata["execution_blockers"] = uniqueSorted(append(blockers, "import_review_required"))
	action.Metadata["import_review"] = map[string]any{
		"version":      1,
		"source":       "openapi",
		"reason_codes": reasonCodes,
		"source_refs":  sourceRefs,
		"resolution":   "replace_behavior_and_remove_gate",
	}
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

func firstSuccessStatus(responses map[string]any) (int, string, bool) {
	if len(responses) == 0 {
		return 0, "", false
	}
	keys := sortedMapKeys(responses)
	for _, key := range keys {
		if len(key) == 3 && key[0] == '2' {
			var status int
			if _, err := fmt.Sscanf(key, "%d", &status); err == nil {
				return status, key, true
			}
			if strings.EqualFold(key, "2XX") {
				return 200, key, true
			}
		}
	}
	if _, ok := responses["default"]; ok {
		return 200, "default", true
	}
	return 0, "", false
}

func templatePath(path string, params []parameter, res resolver, location string, columns columnRegistry) (string, []string) {
	target := path
	out := []string{}
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		template, governed := openAPIParameterTemplate(param)
		if strings.EqualFold(param.In, "path") {
			if !governed {
				columns.add(name, parameterSchema(param))
			}
			target = strings.ReplaceAll(target, "{"+name+"}", template)
			if !governed {
				out = append(out, sanitizeColumnName(name))
			}
			continue
		}
		if strings.EqualFold(param.In, "query") {
			for _, pair := range queryParamTemplates(param, res, location, columns) {
				separator := "?"
				if strings.Contains(target, "?") {
					separator = "&"
				}
				target += separator + escapeQueryKey(pair.key) + "=" + pair.value
				out = append(out, pair.columns...)
			}
		}
	}
	return target, uniqueSorted(out)
}

func openAPIPathCredentialTargets(
	path string,
	target string,
	params []parameter,
	requestKey string,
) ([]credentialpolicy.TargetRef, bool) {
	return buildOpenAPIPathCredentialTargets(path, target, params, requestKey)
}

type queryTemplatePair struct {
	key     string
	value   string
	columns []string
}

func queryParamTemplates(param parameter, res resolver, location string, columns columnRegistry) []queryTemplatePair {
	name := strings.TrimSpace(param.Name)
	if name == "" {
		return nil
	}
	style := strings.TrimSpace(param.Style)
	if style == "" {
		style = "form"
	}
	explode := parameterExplode(param)
	s := res.resolveSchema(parameterSchema(param), location+" query "+name, nil)
	if (s.Type == "object" || len(s.Properties) > 0) && len(s.Properties) > 0 {
		properties := sortedSchemaPropertyKeys(s.Properties)
		propertyColumns := make([]string, 0, len(properties))
		pairs := make([]queryTemplatePair, 0, len(properties))
		switch style {
		case "deepObject":
			for _, propName := range properties {
				column := sanitizeColumnName(name + "_" + propName)
				if schemaCredentialField(propName, s.Properties[propName]) || credential.SensitiveName(name+"."+propName) {
					value := openAPICredentialReference(name, propName)
					if authored, ok := authoredSchemaWireValue(s.Properties[propName]); ok {
						value = url.QueryEscape(authored)
					}
					pairs = append(pairs, queryTemplatePair{key: name + "[" + propName + "]", value: value})
					continue
				}
				columns.add(column, s.Properties[propName])
				pairs = append(pairs, queryTemplatePair{key: name + "[" + propName + "]", value: "{{data.openapi." + column + "}}", columns: []string{column}})
			}
			return pairs
		case "form":
			if explode {
				for _, propName := range properties {
					column := sanitizeColumnName(name + "_" + propName)
					if schemaCredentialField(propName, s.Properties[propName]) || credential.SensitiveName(name+"."+propName) {
						value := openAPICredentialReference(name, propName)
						if authored, ok := authoredSchemaWireValue(s.Properties[propName]); ok {
							value = url.QueryEscape(authored)
						}
						pairs = append(pairs, queryTemplatePair{key: propName, value: value})
						continue
					}
					columns.add(column, s.Properties[propName])
					pairs = append(pairs, queryTemplatePair{key: propName, value: "{{data.openapi." + column + "}}", columns: []string{column}})
				}
				return pairs
			}
			parts := make([]string, 0, len(properties)*2)
			for _, propName := range properties {
				column := sanitizeColumnName(name + "_" + propName)
				if schemaCredentialField(propName, s.Properties[propName]) || credential.SensitiveName(name+"."+propName) {
					value := openAPICredentialReference(name, propName)
					if authored, ok := authoredSchemaWireValue(s.Properties[propName]); ok {
						value = url.QueryEscape(authored)
					}
					parts = append(parts, propName, value)
					continue
				}
				columns.add(column, s.Properties[propName])
				propertyColumns = append(propertyColumns, column)
				parts = append(parts, propName, "{{data.openapi."+column+"}}")
			}
			return []queryTemplatePair{{key: name, value: strings.Join(parts, ","), columns: propertyColumns}}
		}
	}
	column := sanitizeColumnName(name)
	if schemaCredentialField(name, s) {
		if literal, ok := authoredParameterWireValue(param); ok {
			return []queryTemplatePair{{key: name, value: url.QueryEscape(literal)}}
		}
		return []queryTemplatePair{{key: name, value: openAPICredentialReference(name)}}
	}
	columns.add(column, s)
	return []queryTemplatePair{{key: name, value: "{{data.openapi." + column + "}}", columns: []string{column}}}
}

func parameterExplode(param parameter) bool {
	if param.Explode != nil {
		return *param.Explode
	}
	return strings.EqualFold(convertutil.FirstNonEmpty(param.Style, "form"), "form")
}

func escapeQueryKey(key string) string {
	escaped := url.QueryEscape(key)
	escaped = strings.ReplaceAll(escaped, "%5B", "[")
	escaped = strings.ReplaceAll(escaped, "%5D", "]")
	return escaped
}

func applyHeaderAndCookieParams(headers map[string]string, params []parameter, columns columnRegistry) {
	var cookies []string
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		template, governed := openAPIParameterTemplate(param)
		switch strings.ToLower(strings.TrimSpace(param.In)) {
		case "header":
			if credential.SensitiveHeader(name) && !governed {
				template = openAPICredentialReference(name)
				governed = true
			}
			if !governed {
				columns.add(name, parameterSchema(param))
			}
			headers[name] = template
		case "cookie":
			if !governed {
				columns.add(name, parameterSchema(param))
			}
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

func swagger2RequestBody(params []parameter, op *operation, res resolver, location string) *requestBody {
	if op == nil {
		return nil
	}
	var bodyParams []parameter
	var formParams []parameter
	for _, param := range params {
		switch strings.ToLower(strings.TrimSpace(param.In)) {
		case "body":
			bodyParams = append(bodyParams, param)
		case "formdata":
			formParams = append(formParams, param)
		}
	}
	if len(bodyParams) > 0 {
		if len(bodyParams) > 1 {
			res.add(diag.Warning, "Swagger 2 body parameters", location, "multiple body parameters were reduced to the first schema")
		}
		contentType := convertutil.FirstNonEmpty(firstContentType(op.Consumes), firstContentType(res.doc.Consumes), "application/json")
		return &requestBody{Content: map[string]mediaType{
			contentType: {Schema: parameterSchema(bodyParams[0]), Example: bodyParams[0].Example},
		}}
	}
	if len(formParams) == 0 {
		return nil
	}
	contentType := convertutil.FirstNonEmpty(firstContentType(op.Consumes), firstContentType(res.doc.Consumes), "application/x-www-form-urlencoded")
	if contentType == "" || strings.Contains(strings.ToLower(contentType), "json") {
		contentType = "application/x-www-form-urlencoded"
	}
	props := map[string]schema{}
	for _, param := range formParams {
		if param.Name == "" {
			continue
		}
		props[param.Name] = parameterSchema(param)
	}
	if len(props) == 0 {
		return nil
	}
	res.add(diag.Info, "Swagger 2 formData parameters", location, "formData parameters were modeled as a request body; validate encoding and file fields before replay")
	return &requestBody{Content: map[string]mediaType{
		contentType: {Schema: schema{Type: "object", Properties: props}},
	}}
}

func parameterSchema(param parameter) schema {
	if param.Schema.Ref != "" || param.Schema.Type != "" || len(param.Schema.Properties) > 0 || param.Schema.Items != nil || param.Schema.Format != "" {
		s := param.Schema
		if s.Example == nil {
			s.Example = param.Example
		}
		if s.Default == nil {
			s.Default = param.Default
		}
		if len(s.Enum) == 0 && len(param.Enum) > 0 {
			s.Enum = append([]any(nil), param.Enum...)
		}
		return s
	}
	return schema{
		Type:    param.Type,
		Format:  param.Format,
		Example: param.Example,
		Default: param.Default,
		Enum:    append([]any(nil), param.Enum...),
	}
}

func openAPIParameterTemplate(param parameter) (string, bool) {
	name := strings.TrimSpace(param.Name)
	if schemaCredentialField(name, parameterSchema(param)) {
		if literal, ok := authoredParameterWireValue(param); ok {
			switch strings.ToLower(strings.TrimSpace(param.In)) {
			case "path":
				literal = url.PathEscape(literal)
			case "query":
				literal = url.QueryEscape(literal)
			}
			// This literal exists only in the effective request. The shared
			// Preserve-mode projector marks it for review while retaining
			// it in the durable destination.
			return literal, true
		}
		return openAPICredentialReference(name), true
	}
	return "{{data.openapi." + sanitizeColumnName(name) + "}}", false
}

func authoredSchemaValue(current schema) (any, bool) {
	switch {
	case current.Example != nil:
		return current.Example, true
	case current.Default != nil:
		return current.Default, true
	case len(current.Enum) > 0:
		return current.Enum[0], true
	default:
		return nil, false
	}
}

func authoredSchemaWireValue(current schema) (string, bool) {
	value, ok := authoredSchemaValue(current)
	if !ok {
		return "", false
	}
	return authoredWireValue(value)
}

func authoredWireValue(value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		return typed, true
	case bool:
		return strconv.FormatBool(typed), true
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 32), true
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64), true
	case json.Number:
		return typed.String(), true
	case int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64:
		return fmt.Sprint(typed), true
	default:
		return "", false
	}
}

func authoredParameterValue(param parameter) (any, bool) {
	switch {
	case param.Example != nil:
		return param.Example, true
	case param.Schema.Example != nil:
		return param.Schema.Example, true
	case param.Default != nil:
		return param.Default, true
	case param.Schema.Default != nil:
		return param.Schema.Default, true
	default:
		return nil, false
	}
}

func authoredParameterSourceValue(param parameter) (any, bool) {
	if value, ok := authoredParameterValue(param); ok {
		return value, true
	}
	return authoredSchemaValue(parameterSchema(param))
}

func authoredParameterWireValue(param parameter) (string, bool) {
	value, ok := authoredParameterSourceValue(param)
	if !ok {
		return "", false
	}
	return authoredWireValue(value)
}

func sensitiveParameterReviewCodes(params []parameter) []string {
	for _, param := range params {
		name := strings.TrimSpace(param.Name)
		if name == "" {
			continue
		}
		sensitive := schemaCredentialField(name, parameterSchema(param)) ||
			(strings.EqualFold(strings.TrimSpace(param.In), "header") && credential.SensitiveHeader(name))
		if !sensitive {
			continue
		}
		if value, ok := authoredParameterSourceValue(param); ok {
			if _, representable := authoredWireValue(value); !representable {
				return []string{"sensitive_non_string_parameter"}
			}
		}
	}
	return nil
}

func firstContentType(values []string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func headerValue(headers map[string]string, key string) string {
	for existingKey, value := range headers {
		if strings.EqualFold(existingKey, key) {
			return value
		}
	}
	return ""
}

type authContext struct {
	script             *dsl.Script
	schemes            map[string]securityScheme
	baseURL            string
	opts               ConvertOptions
	warnings           *[]string
	resolver           resolver
	tokenVars          map[string]string
	tokenScopes        map[string][]string
	tokenActionIndexes map[string]int
}

func newAuthContext(script *dsl.Script, schemes map[string]securityScheme, baseURL string, opts ConvertOptions, warnings *[]string, res resolver) *authContext {
	return &authContext{
		script:             script,
		schemes:            schemes,
		baseURL:            baseURL,
		opts:               opts,
		warnings:           warnings,
		resolver:           res,
		tokenVars:          map[string]string{},
		tokenScopes:        map[string][]string{},
		tokenActionIndexes: map[string]int{},
	}
}

func normalizedSecuritySchemes(doc *document) map[string]securityScheme {
	out := map[string]securityScheme{}
	if doc == nil {
		return out
	}
	for name, scheme := range doc.Components.SecuritySchemes {
		out[name] = scheme
	}
	for name, scheme := range doc.SecurityDefinitions {
		out[name] = scheme
	}
	return out
}

func applyOperationSecurity(action *dsl.Action, requirements []map[string][]string, auth *authContext, res resolver, location string) {
	if action == nil || requirements == nil || auth == nil {
		return
	}
	if len(requirements) == 0 {
		// An explicit empty security requirement ("security": []) means the
		// operation opts out of the document-level security scheme. Emit an
		// explicit anonymous auth override so the runtime suppresses any global
		// Authorization header (a nil Auth would instead inherit it). See
		// pkg/protocol/http buildActionAuthHeader and
		// TestConvertOpenAPIAnonymousSecurityOverrideSuppressesGlobalAuth.
		action.Auth = &dsl.AuthConfig{Type: "none"}
		return
	}
	selected, rejectionCodes := selectOpenAPISecurityAlternative(requirements, auth.schemes)
	if selected == nil {
		if len(rejectionCodes) == 0 {
			rejectionCodes = []string{"unsupported_security_requirement"}
		}
		res.add(diag.Blocker, "OpenAPI security", location, "no fully supported OpenAPI security alternative could be projected; the generated request is blocked pending review")
		applyOpenAPIReviewGate(action, rejectionCodes, location)
		return
	}
	if len(selected.names) == 0 {
		action.Auth = &dsl.AuthConfig{Type: "none"}
		return
	}
	for _, name := range selected.names {
		applyOpenAPISecurityScheme(action, name, requirements[selected.index][name], auth, res, location)
	}
}

type selectedSecurityAlternative struct {
	index int
	names []string
}

func selectOpenAPISecurityAlternative(requirements []map[string][]string, schemes map[string]securityScheme) (*selectedSecurityAlternative, []string) {
	var rejectionCodes []string
	for index, requirement := range requirements {
		names := sortedMapKeys(requirement)
		if len(names) == 0 {
			return &selectedSecurityAlternative{index: index}, nil
		}
		authSchemes := 0
		destinations := map[string]string{}
		supported := true
		for _, name := range names {
			scheme, ok := schemes[name]
			if !ok {
				rejectionCodes = append(rejectionCodes, "unresolved_security_scheme")
				supported = false
				continue
			}
			switch strings.ToLower(strings.TrimSpace(scheme.Type)) {
			case "http":
				switch strings.ToLower(strings.TrimSpace(scheme.Scheme)) {
				case "basic", "bearer", "digest":
					authSchemes++
				default:
					rejectionCodes = append(rejectionCodes, "unsupported_http_auth_scheme")
					supported = false
				}
			case "oauth2", "openidconnect":
				authSchemes++
			case "basic":
				// Swagger 2.0 represents HTTP Basic as type: basic rather than
				// the OpenAPI 3 type: http / scheme: basic pair.
				authSchemes++
			case "apikey":
				in := strings.ToLower(strings.TrimSpace(scheme.In))
				if scheme.Name == "" || (in != "header" && in != "query" && in != "cookie") {
					rejectionCodes = append(rejectionCodes, "unsupported_api_key_location")
					supported = false
					continue
				}
				destination := in + "\x00" + strings.ToLower(scheme.Name)
				if previous := destinations[destination]; previous != "" && previous != name {
					rejectionCodes = append(rejectionCodes, "conflicting_security_schemes")
					supported = false
				}
				destinations[destination] = name
				if in == "header" && strings.EqualFold(scheme.Name, "Authorization") {
					destinations["authorization"] = name
				}
			default:
				rejectionCodes = append(rejectionCodes, "unsupported_security_scheme")
				supported = false
			}
		}
		if authSchemes > 1 || (authSchemes > 0 && destinations["authorization"] != "") {
			rejectionCodes = append(rejectionCodes, "conflicting_security_schemes")
			supported = false
		}
		if supported {
			return &selectedSecurityAlternative{index: index, names: names}, nil
		}
	}
	return nil, uniqueSorted(rejectionCodes)
}

func applyOpenAPISecurityScheme(action *dsl.Action, name string, scopes []string, auth *authContext, res resolver, location string) {
	scheme := auth.schemes[name]
	switch strings.ToLower(strings.TrimSpace(scheme.Type)) {
	case "http":
		switch strings.ToLower(strings.TrimSpace(scheme.Scheme)) {
		case "bearer":
			action.Auth = &dsl.AuthConfig{Type: "bearer", Token: "{{secret." + openAPISecretName(name, "BEARER_TOKEN") + "}}"}
		case "basic", "digest":
			action.Auth = &dsl.AuthConfig{
				Type:     strings.ToLower(strings.TrimSpace(scheme.Scheme)),
				Username: "{{env." + openAPISecretName(name, "USERNAME") + "}}",
				Password: "{{secret." + openAPISecretName(name, "PASSWORD") + "}}",
			}
		}
	case "basic":
		action.Auth = &dsl.AuthConfig{
			Type:     "basic",
			Username: "{{env." + openAPISecretName(name, "USERNAME") + "}}",
			Password: "{{secret." + openAPISecretName(name, "PASSWORD") + "}}",
		}
	case "apikey":
		secret := "{{secret." + openAPISecretName(name, "API_KEY") + "}}"
		switch strings.ToLower(strings.TrimSpace(scheme.In)) {
		case "header":
			if action.Headers == nil {
				action.Headers = map[string]string{}
			}
			action.Headers[scheme.Name] = secret
		case "query":
			separator := "?"
			if strings.Contains(action.Target, "?") {
				separator = "&"
			}
			action.Target += separator + escapeQueryKey(scheme.Name) + "=" + secret
		case "cookie":
			if action.Headers == nil {
				action.Headers = map[string]string{}
			}
			cookie := scheme.Name + "=" + secret
			if existing := headerValue(action.Headers, "Cookie"); existing != "" {
				cookie = existing + "; " + cookie
			}
			action.Headers["Cookie"] = cookie
			res.add(diag.Info, "OpenAPI security", location, "cookie API key "+scheme.Name+" was represented as a Cookie header placeholder")
		}
	case "oauth2", "openidconnect":
		if tokenVar := auth.ensureOAuthToken(name, scheme, scopes, location); tokenVar != "" {
			action.Auth = &dsl.AuthConfig{Type: "bearer", Token: "{{var." + tokenVar + "}}"}
			return
		}
		action.Auth = &dsl.AuthConfig{Type: "bearer", Token: "{{secret." + openAPISecretName(name, "BEARER_TOKEN") + "}}"}
		res.add(diag.Warning, "OpenAPI security", location, "OAuth/OpenID Connect operation security was represented as a bearer token placeholder")
	}
}

func (a *authContext) ensureOAuthToken(name string, scheme securityScheme, scopes []string, location string) string {
	if a == nil || a.script == nil {
		return ""
	}
	tokenURL, grantType, flowScopes := oauthTokenFlow(scheme, a.opts)
	if tokenURL == "" {
		return ""
	}
	if !convertutil.BoolOption(a.opts.SynthesizeOAuthTokenInit, true) && grantType == "client_credentials" {
		return ""
	}
	requestedScopes := normalizedOAuthScopes(scopes)
	if len(requestedScopes) == 0 {
		requestedScopes = sortedMapKeys(flowScopes)
	}
	if existing := a.tokenVars[name]; existing != "" {
		merged := appendUniqueOAuthScopes(a.tokenScopes[name], requestedScopes)
		a.tokenScopes[name] = merged
		if actionIndex, ok := a.tokenActionIndexes[name]; ok &&
			actionIndex >= 0 && actionIndex < len(a.script.Init) &&
			a.script.Init[actionIndex].Params != nil {
			a.script.Init[actionIndex].Params["scope"] = strings.Join(merged, " ")
		}
		return existing
	}
	tokenURL = resolveURLAgainstBase(a.baseURL, tokenURL)
	saveAs := "access_token"
	if len(a.tokenVars) > 0 || sanitizeColumnName(name) != "oauth2" {
		saveAs = sanitizeColumnName("access_token_" + name)
	}
	scope := strings.Join(requestedScopes, " ")
	action := dsl.Action{
		Name:   sanitizeName("OAuth2_Token_" + name),
		Type:   "http_oauth2_token",
		Target: tokenURL,
		Params: map[string]any{
			"token_url":     tokenURL,
			"grant_type":    grantType,
			"client_id":     "{{env." + openAPISecretName(name, "CLIENT_ID") + "}}",
			"client_secret": "{{secret." + openAPISecretName(name, "CLIENT_SECRET") + "}}",
			"scope":         scope,
			"save_as":       saveAs,
		},
		Metadata: map[string]any{
			"openapi": map[string]any{
				"security_scheme": name,
				"grant_type":      grantType,
			},
		},
	}
	if grantType == "password" {
		action.Params["username"] = "{{env." + openAPISecretName(name, "USERNAME") + "}}"
		action.Params["password"] = "{{secret." + openAPISecretName(name, "PASSWORD") + "}}"
	}
	actionIndex := len(a.script.Init)
	a.script.Init = append(a.script.Init, action)
	a.tokenVars[name] = saveAs
	a.tokenScopes[name] = append([]string(nil), requestedScopes...)
	a.tokenActionIndexes[name] = actionIndex
	a.resolver.add(diag.Info, "OpenAPI OAuth2", location, "generated "+grantType+" token init action for scheme "+name)
	return saveAs
}

func normalizedOAuthScopes(scopes []string) []string {
	return appendUniqueOAuthScopes(nil, scopes)
}

func appendUniqueOAuthScopes(existing, additional []string) []string {
	out := append([]string(nil), existing...)
	seen := make(map[string]struct{}, len(out)+len(additional))
	for _, scope := range out {
		seen[scope] = struct{}{}
	}
	for _, scope := range additional {
		scope = strings.TrimSpace(scope)
		if scope == "" {
			continue
		}
		if _, duplicate := seen[scope]; duplicate {
			continue
		}
		seen[scope] = struct{}{}
		out = append(out, scope)
	}
	return out
}

func oauthTokenFlow(scheme securityScheme, opts ConvertOptions) (string, string, map[string]string) {
	if scheme.Flows.ClientCredentials.TokenURL != "" {
		return scheme.Flows.ClientCredentials.TokenURL, "client_credentials", scheme.Flows.ClientCredentials.Scopes
	}
	if strings.EqualFold(scheme.Flow, "application") && scheme.TokenURL != "" {
		return scheme.TokenURL, "client_credentials", scheme.Scopes
	}
	if convertutil.BoolOption(opts.SynthesizeOAuthPasswordGrant, false) {
		if scheme.Flows.Password.TokenURL != "" {
			return scheme.Flows.Password.TokenURL, "password", scheme.Flows.Password.Scopes
		}
		if strings.EqualFold(scheme.Flow, "password") && scheme.TokenURL != "" {
			return scheme.TokenURL, "password", scheme.Scopes
		}
	}
	return "", "", nil
}

func resolveURLAgainstBase(baseURL string, value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.IsAbs() || strings.TrimSpace(baseURL) == "" {
		return strings.TrimSpace(value)
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil {
		return strings.TrimSpace(value)
	}
	return base.ResolveReference(parsed).String()
}

func openAPISecretName(schemeName string, suffix string) string {
	base := strings.ToUpper(sanitizeColumnName(schemeName))
	if base == "" {
		base = "AUTH"
	}
	base = strings.ReplaceAll(base, "_", "")
	suffix = strings.ToUpper(strings.TrimSpace(suffix))
	if suffix == "" {
		return "OPENAPI_" + base
	}
	return "OPENAPI_" + base + "_" + suffix
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
		values = append(values, strings.TrimRight(convertutil.FirstNonEmpty(firstScheme(doc.Schemes), "https")+"://"+doc.Host+"/"+strings.Trim(doc.BasePath, "/"), "/"))
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
	// Objects (including map-style schemas declared only via additionalProperties)
	// are not scalars — they must recurse so their fields/representative entries
	// render, rather than collapsing into a single placeholder. Only the schema
	// (map) form of additionalProperties counts; a bool form (false/true) does not
	// make an otherwise-scalar schema non-scalar.
	if _, hasMapAP := additionalPropertiesSchema(s); strings.EqualFold(s.Type, "object") || hasMapAP {
		return false
	}
	return len(s.Properties) == 0 && s.Items == nil
}

// additionalPropertiesSchema returns the value schema and true only when
// `additionalProperties` is declared as a schema (the map form). In OpenAPI the
// field is `bool | schema`; JSON/YAML box a bool `false`/`true` into a non-nil
// any, so a plain `s.AdditionalProperties != nil` check wrongly treats a
// closed/open schema (additionalProperties: false / true) as a map and would
// synthesize a spurious `additionalProperty` field and data column.
func additionalPropertiesSchema(s schema) (schema, bool) {
	var raw map[string]any
	switch v := s.AdditionalProperties.(type) {
	case map[string]any:
		raw = v
	case map[any]any:
		raw = make(map[string]any, len(v))
		for k, val := range v {
			raw[fmt.Sprint(k)] = val
		}
	default:
		return schema{}, false
	}
	var sub schema
	if b, err := json.Marshal(raw); err == nil {
		decoder := json.NewDecoder(bytes.NewReader(b))
		decoder.UseNumber()
		_ = decoder.Decode(&sub)
	}
	return sub, true
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

func firstPositive(values ...int) int {
	for _, value := range values {
		if value > 0 {
			return value
		}
	}
	return 0
}
