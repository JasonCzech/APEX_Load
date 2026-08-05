package raml

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/convertutil"
	"github.com/jasonczech/apexload/pkg/convert/diag"
	"github.com/jasonczech/apexload/pkg/convert/srcparse"
	"github.com/jasonczech/apexload/pkg/dsl"
	"gopkg.in/yaml.v3"
)

type ImportRequest struct {
	Name      string `json:"name,omitempty"`
	ProjectID string `json:"project_id,omitempty"`
	Content   string `json:"content"`
	// Files holds additional spec files (keyed by their in-archive path) used to
	// resolve !include directives, e.g. the entries of an uploaded RAML zip.
	Files   map[string][]byte `json:"-"`
	Options ConvertOptions    `json:"options,omitempty"`
}

type ConvertOptions struct {
	ScriptName string `json:"script_name,omitempty"`
	BaseURL    string `json:"base_url,omitempty"`
}

type ConvertStats struct {
	Format              string   `json:"format"`
	TotalResources      int      `json:"total_resources"`
	TotalMethods        int      `json:"total_methods"`
	ConvertedActions    int      `json:"converted_actions"`
	RequestBodies       int      `json:"request_bodies"`
	AuthSchemesDetected []string `json:"auth_schemes_detected,omitempty"`
}

type ReviewSummary struct {
	Title       string       `json:"title,omitempty"`
	Version     string       `json:"version,omitempty"`
	BaseURL     string       `json:"base_url,omitempty"`
	Resources   []string     `json:"resources,omitempty"`
	AuthSchemes []string     `json:"auth_schemes,omitempty"`
	Policies    []PolicyHint `json:"policies,omitempty"`
}

// PolicyHint records an Anypoint API-gateway policy inferred from the RAML spec.
type PolicyHint struct {
	Policy string `json:"policy"`
	Detail string `json:"detail"`
	Scheme string `json:"scheme,omitempty"`
}

// applyOptions controls per-action auth/policy decoration during resource walk.
type applyOptions struct {
	oauth    bool
	clientID bool
}

type ConvertResult struct {
	Script      *dsl.Script      `json:"script"`
	Warnings    []string         `json:"warnings,omitempty"`
	Unsupported diag.Diagnostics `json:"unsupported,omitempty"`
	Stats       ConvertStats     `json:"stats"`
	Summary     ReviewSummary    `json:"summary"`
}

var ramlMethods = map[string]bool{
	"get": true, "post": true, "put": true, "patch": true, "delete": true, "head": true, "options": true,
}

func Convert(req ImportRequest) (*ConvertResult, error) {
	doc, err := parseRAMLDocument(req.Content, req.Files)
	if err != nil {
		return nil, err
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("RAML document is empty")
	}
	authoredBaseURL := stringValue(doc["baseUri"])
	baseURL := convertutil.FirstNonEmpty(req.Options.BaseURL, authoredBaseURL, "{{env.BASE_URL}}")
	var baseURLWarnings []string
	if strings.TrimSpace(req.Options.BaseURL) == "" && strings.TrimSpace(authoredBaseURL) != "" {
		baseURL, baseURLWarnings = bindRAMLBaseURI(authoredBaseURL, doc)
	}
	name := convertutil.FirstNonEmpty(req.Options.ScriptName, req.Name, stringValue(doc["title"]), "RAML Import")
	result := &ConvertResult{
		Warnings: baseURLWarnings,
		Stats:    ConvertStats{Format: "raml"},
		Summary: ReviewSummary{
			Title:   stringValue(doc["title"]),
			Version: stringValue(doc["version"]),
			BaseURL: baseURL,
		},
	}
	script := &dsl.Script{
		Name:     name,
		Version:  "1.0.0",
		Protocol: "http",
		Config: dsl.ScriptConfig{
			BaseURL: baseURL,
			RunLogic: dsl.RunLogicConfig{
				Iterations:       1,
				AutoTransactions: "per_action",
			},
			ErrorHandling: dsl.ErrorHandlingConfig{ContinueOnError: false},
		},
	}

	if tokenURL, schemes := oauth2TokenURL(doc); tokenURL != "" {
		result.Stats.AuthSchemesDetected = schemes
		result.Summary.AuthSchemes = schemes
		script.Init = append(script.Init, dsl.Action{
			Name: "Get OAuth2 Token",
			Type: "http_oauth2_token",
			Params: map[string]any{
				"token_url":     tokenURL,
				"grant_type":    "client_credentials",
				"client_id":     "{{env.RAML_CLIENT_ID}}",
				"client_secret": "{{secret.RAML_CLIENT_SECRET}}",
				"scope":         "{{env.RAML_SCOPE}}",
				"save_as":       "access_token",
			},
		})
	}

	clientIDEnforced, policySchemes := detectClientIDEnforcement(doc)
	if clientIDEnforced {
		result.Summary.Policies = append(result.Summary.Policies, PolicyHint{
			Policy: "client_id_enforcement",
			Detail: "Anypoint Client ID Enforcement detected; client_id/client_secret mapped to env/secret placeholders on each request.",
			Scheme: strings.Join(policySchemes, ", "),
		})
		result.Warnings = append(result.Warnings, "Anypoint Client ID Enforcement: supply client_id via {{env.RAML_CLIENT_ID}} and client_secret via {{secret.RAML_CLIENT_SECRET}} before running; do not embed live credentials.")
	}

	apply := applyOptions{oauth: len(script.Init) > 0, clientID: clientIDEnforced}
	resources := sortedResourceKeys(doc)
	for _, resource := range resources {
		node, _ := doc[resource].(map[string]any)
		walkResource(resource, node, result, script, apply, nil)
	}
	if len(script.Actions) == 0 {
		return nil, fmt.Errorf("RAML document did not contain HTTP resources or methods")
	}
	result.Script = script
	result.Stats.ConvertedActions = len(script.Actions)
	return result, nil
}

var ramlBaseURIParameterPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_.-]*)\}`)

func bindRAMLBaseURI(baseURI string, doc map[string]any) (string, []string) {
	if !ramlBaseURIParameterPattern.MatchString(baseURI) {
		return baseURI, nil
	}
	declarations := mapValue(doc["baseUriParameters"])
	version := stringValue(doc["version"])
	var warnings []string
	bound := ramlBaseURIParameterPattern.ReplaceAllStringFunc(baseURI, func(token string) string {
		matches := ramlBaseURIParameterPattern.FindStringSubmatch(token)
		name := matches[1]
		value := ""
		if name == "version" {
			value = version
		}
		if value == "" {
			declaration := mapValue(declarations[name])
			value = convertutil.FirstNonEmpty(stringValue(declaration["example"]), stringValue(declaration["default"]))
			if value == "" {
				if values, ok := declaration["enum"].([]any); ok && len(values) > 0 {
					value = fmt.Sprint(values[0])
				}
			}
		}
		if value != "" {
			return value
		}
		envName := strings.ToUpper(strings.Map(func(r rune) rune {
			if r >= 'A' && r <= 'Z' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
				return r
			}
			return '_'
		}, name))
		warnings = append(warnings, fmt.Sprintf("RAML baseUri parameter %q has no version/example/default binding; supply {{env.RAML_BASE_URI_PARAM_%s}} before replay", name, envName))
		return "{{env.RAML_BASE_URI_PARAM_" + envName + "}}"
	})
	return bound, warnings
}

func walkResource(resourcePath string, node map[string]any, result *ConvertResult, script *dsl.Script, apply applyOptions, inheritedURIParameters map[string]any) {
	if node == nil {
		return
	}
	result.Stats.TotalResources++
	result.Summary.Resources = append(result.Summary.Resources, resourcePath)
	uriParameters := mergeRAMLURIParameters(inheritedURIParameters, mapValue(node["uriParameters"]))
	keys := sortedMapKeys(node)
	for _, key := range keys {
		lower := strings.ToLower(key)
		if !ramlMethods[lower] {
			continue
		}
		child, _ := node[key].(map[string]any)
		result.Stats.TotalMethods++
		action := dsl.Action{
			Name:   convertutil.FirstNonEmpty(stringValue(child["displayName"]), stringValue(child["description"]), strings.ToUpper(lower)+" "+resourcePath),
			Type:   strings.ToUpper(lower),
			Method: strings.ToUpper(lower),
			Target: resourcePath,
			Headers: map[string]string{
				"Accept": "application/json",
			},
			Assertions: ramlResponseAssertions(child),
		}
		methodURIParameters := mergeRAMLURIParameters(uriParameters, mapValue(child["uriParameters"]))
		bindRAMLURIParameters(&action, script, methodURIParameters, result)
		if apply.oauth {
			action.Headers["Authorization"] = "Bearer {{var.access_token}}"
		}
		if apply.clientID {
			action.Headers["client_id"] = "{{env.RAML_CLIENT_ID}}"
			action.Headers["client_secret"] = "{{secret.RAML_CLIENT_SECRET}}"
		}
		if body, contentType, ok := ramlMethodBody(child); ok {
			action.Body = body
			action.Headers["Content-Type"] = contentType
			result.Stats.RequestBodies++
		}
		script.Actions = append(script.Actions, action)
	}
	for _, key := range keys {
		if !strings.HasPrefix(key, "/") {
			continue
		}
		child, _ := node[key].(map[string]any)
		walkResource(joinRAMLPath(resourcePath, key), child, result, script, apply, uriParameters)
	}
}

var ramlURIParameterPattern = regexp.MustCompile(`\{([A-Za-z_][A-Za-z0-9_.-]*)\}`)
var ramlParameterScopePattern = regexp.MustCompile(`[^a-z0-9]+`)

func mergeRAMLURIParameters(parent, local map[string]any) map[string]any {
	if len(parent) == 0 && len(local) == 0 {
		return nil
	}
	merged := make(map[string]any, len(parent)+len(local))
	for name, value := range parent {
		merged[name] = value
	}
	for name, value := range local {
		merged[name] = value
	}
	return merged
}

func bindRAMLURIParameters(action *dsl.Action, script *dsl.Script, declarations map[string]any, result *ConvertResult) {
	if action == nil || script == nil {
		return
	}
	matches := ramlURIParameterPattern.FindAllStringSubmatch(action.Target, -1)
	for _, match := range matches {
		name := match[1]
		declaration := mapValue(declarations[name])
		value := convertutil.FirstNonEmpty(stringValue(declaration["example"]), stringValue(declaration["default"]))
		if value == "" {
			if values, ok := declaration["enum"].([]any); ok && len(values) > 0 {
				value = fmt.Sprint(values[0])
			}
		}
		if value == "" {
			if action.Metadata == nil {
				action.Metadata = make(map[string]any)
			}
			action.Metadata["execution_blocker"] = "import_review_required"
			action.Metadata["unresolved_path_parameter"] = name
			result.Unsupported = append(result.Unsupported, diag.Diagnostic{
				Severity: diag.Warning, Element: "RAML path parameter", Location: action.Target,
				Detail: fmt.Sprintf("path parameter %q has no example/default binding and requires review", name),
			})
			continue
		}
		bindingName := name
		found := false
		for _, parameter := range script.Parameters {
			if parameter.Name != bindingName {
				continue
			}
			if len(parameter.Values) > 0 && parameter.Values[0] == value {
				found = true
				break
			}
			// The same placeholder name can legitimately mean different things
			// on different resources. Scope only the conflicting binding so one
			// resource's example cannot silently leak into another action.
			bindingName = ramlScopedParameterName(action, name)
			break
		}
		if !found {
			bindingName, found = collisionSafeRAMLParameterName(script.Parameters, action, name, bindingName, value)
		}
		if !found {
			script.Parameters = append(script.Parameters, dsl.Parameter{Name: bindingName, Type: "list", Update: "once", Values: []string{value}})
		}
		action.Target = strings.ReplaceAll(action.Target, match[0], "{{param."+bindingName+"}}")
	}
}

func collisionSafeRAMLParameterName(parameters []dsl.Parameter, action *dsl.Action, sourceName, candidate, value string) (string, bool) {
	if exists, matching := ramlParameterNameState(parameters, candidate, value); !exists || matching {
		return candidate, matching
	}

	// Normalization deliberately removes path punctuation and the readable scope
	// is capped, so distinct resources can share the same candidate. Add a stable
	// operation/value fingerprint only on collision, then retain a deterministic
	// numeric escape hatch for a pre-existing parameter with that exact hash.
	scope := ""
	if action != nil {
		scope = action.Method + "\x00" + action.Target
	}
	fingerprint := sha256.Sum256([]byte(scope + "\x00" + sourceName + "\x00" + value))
	hashed := candidate + "_" + fmt.Sprintf("%x", fingerprint[:6])
	for suffix := 0; ; suffix++ {
		unique := hashed
		if suffix > 0 {
			unique = fmt.Sprintf("%s_%d", hashed, suffix+1)
		}
		exists, matching := ramlParameterNameState(parameters, unique, value)
		if !exists || matching {
			return unique, matching
		}
	}
}

func ramlParameterNameState(parameters []dsl.Parameter, name, value string) (exists bool, matching bool) {
	for _, parameter := range parameters {
		if parameter.Name != name {
			continue
		}
		exists = true
		if len(parameter.Values) > 0 && parameter.Values[0] == value {
			return true, true
		}
	}
	return exists, false
}

func ramlScopedParameterName(action *dsl.Action, name string) string {
	scope := strings.ToLower(strings.TrimSpace(action.Method + "_" + action.Target))
	scope = ramlParameterScopePattern.ReplaceAllString(scope, "_")
	scope = strings.Trim(scope, "_")
	if len(scope) > 64 {
		scope = scope[:64]
	}
	if scope == "" {
		scope = "resource"
	}
	return name + "_" + scope
}

func ramlResponseAssertions(method map[string]any) []dsl.Assertion {
	responses := mapValue(method["responses"])
	exactSuccesses := 0
	onlySuccess := 0
	for _, raw := range sortedMapKeys(responses) {
		status := strings.ToUpper(strings.TrimSpace(raw))
		if status == "2XX" {
			return []dsl.Assertion{{Type: "status_code_range", Min: 200, Max: 299}}
		}
		code, err := strconv.Atoi(status)
		if err == nil && code >= 200 && code <= 299 {
			exactSuccesses++
			onlySuccess = code
		}
	}
	if exactSuccesses > 1 {
		return []dsl.Assertion{{Type: "status_code_range", Min: 200, Max: 299}}
	}
	if exactSuccesses == 1 {
		return []dsl.Assertion{{Type: "status_code", Expected: onlySuccess}}
	}
	return []dsl.Assertion{{Type: "status_code", Expected: 200}}
}

func mapValue(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	if mapped, ok := value.(map[any]any); ok {
		converted := make(map[string]any, len(mapped))
		for key, item := range mapped {
			converted[fmt.Sprint(key)] = item
		}
		return converted
	}
	return nil
}

func ramlMethodBody(method map[string]any) (string, string, bool) {
	bodyNode, _ := method["body"].(map[string]any)
	if len(bodyNode) == 0 {
		return "", "", false
	}
	contentTypes := sortedMapKeys(bodyNode)
	for _, contentType := range contentTypes {
		media, _ := bodyNode[contentType].(map[string]any)
		if len(media) == 0 {
			continue
		}
		if example, ok := bodyExampleValue(media["example"]); ok {
			return example, contentType, true
		}
		if examples, ok := media["examples"].(map[string]any); ok {
			for _, key := range sortedMapKeys(examples) {
				switch value := examples[key].(type) {
				case string:
					return value, contentType, true
				case map[string]any:
					if text, ok := bodyExampleValue(value["value"]); ok {
						return text, contentType, true
					}
				}
			}
		}
		if contentType == "application/json" {
			return "{}", contentType, true
		}
	}
	return "", "", false
}

// parseRAMLDocument parses RAML content into a generic map, resolving any
// !include directives against the supplied file set (e.g. a zip upload).
func parseRAMLDocument(content string, files map[string][]byte) (map[string]any, error) {
	// Reject the mapping ambiguities that let the converter and a reviewer read
	// the same spec differently. Merge keys stay tolerated: RAML resource types
	// and traits are anchor-heavy by design.
	if _, err := srcparse.ValidateYAMLMappingKeys([]byte(content), srcparse.SpecYAMLPolicy(), srcparse.Limits{}); err != nil {
		return nil, fmt.Errorf("parse RAML: %w", err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return nil, fmt.Errorf("parse RAML: %w", err)
	}
	if err := resolveIncludes(&root, files, 0); err != nil {
		return nil, err
	}
	var doc map[string]any
	if err := root.Decode(&doc); err != nil {
		return nil, fmt.Errorf("parse RAML: %w", err)
	}
	return doc, nil
}

// resolveIncludes walks a YAML node tree and replaces !include scalars with the
// parsed content of the referenced file. Included files may themselves contain
// !include directives (bounded recursion).
func resolveIncludes(node *yaml.Node, files map[string][]byte, depth int) error {
	if node == nil || depth > 20 {
		return nil
	}
	if node.Tag == "!include" {
		name := strings.TrimSpace(node.Value)
		content, ok := lookupInclude(files, name)
		if !ok {
			return fmt.Errorf("RAML !include %q could not be resolved; upload the referenced files (zip) to import this spec", name)
		}
		// Only structured fragments (RAML/YAML/JSON) are parsed as a node tree;
		// arbitrary text (e.g. an XML/text body example) is kept verbatim as a
		// string scalar. Parsing arbitrary text as YAML would silently retype,
		// reorder, requote, or drop it (most text is valid YAML).
		if !includeIsStructured(name) {
			node.Kind = yaml.ScalarNode
			node.Tag = "!!str"
			node.Value = string(content)
			return nil
		}
		var included yaml.Node
		if err := yaml.Unmarshal(content, &included); err != nil {
			node.Kind = yaml.ScalarNode
			node.Tag = "!!str"
			node.Value = string(content)
			return nil
		}
		resolved := &included
		if included.Kind == yaml.DocumentNode && len(included.Content) > 0 {
			resolved = included.Content[0]
		}
		if err := resolveIncludes(resolved, files, depth+1); err != nil {
			return err
		}
		*node = *resolved
		return nil
	}
	for _, child := range node.Content {
		if err := resolveIncludes(child, files, depth+1); err != nil {
			return err
		}
	}
	return nil
}

func lookupInclude(files map[string][]byte, name string) ([]byte, bool) {
	if len(files) == 0 || name == "" {
		return nil, false
	}
	clean := strings.TrimPrefix(name, "./")
	if content, ok := files[name]; ok {
		return content, true
	}
	if content, ok := files[clean]; ok {
		return content, true
	}
	// Fall back to a basename match, chosen deterministically (lexicographically
	// smallest key) so that colliding basenames across directories never resolve
	// to an arbitrary, run-dependent file.
	base := path.Base(clean)
	bestKey := ""
	for key := range files {
		if path.Base(key) == base && (bestKey == "" || key < bestKey) {
			bestKey = key
		}
	}
	if bestKey != "" {
		return files[bestKey], true
	}
	return nil, false
}

// includeIsStructured reports whether an !include target should be parsed as a
// structured (RAML/YAML/JSON) fragment rather than kept as raw text.
func includeIsStructured(name string) bool {
	lower := strings.ToLower(name)
	return strings.HasSuffix(lower, ".raml") ||
		strings.HasSuffix(lower, ".yaml") ||
		strings.HasSuffix(lower, ".yml") ||
		strings.HasSuffix(lower, ".json")
}

// detectClientIDEnforcement reports whether the spec applies Anypoint Client ID
// Enforcement, inferred from securitySchemes that require client_id/client_secret
// credentials or are explicitly named/typed for client-id enforcement.
func detectClientIDEnforcement(doc map[string]any) (bool, []string) {
	securitySchemes, _ := doc["securitySchemes"].(map[string]any)
	schemes := make([]string, 0)
	for name, raw := range securitySchemes {
		scheme, _ := raw.(map[string]any)
		if len(scheme) == 0 {
			continue
		}
		typeLower := strings.ToLower(stringValue(scheme["type"]))
		nameLower := strings.ToLower(name)
		isClientScheme := schemeRequiresClientCredentials(scheme) ||
			strings.Contains(typeLower, "client") ||
			(strings.Contains(nameLower, "client") && strings.Contains(nameLower, "id"))
		if isClientScheme {
			schemes = append(schemes, name)
		}
	}
	sort.Strings(schemes)
	return len(schemes) > 0, schemes
}

func schemeRequiresClientCredentials(scheme map[string]any) bool {
	describedBy, _ := scheme["describedBy"].(map[string]any)
	if len(describedBy) == 0 {
		return false
	}
	for _, key := range []string{"headers", "queryParameters"} {
		params, _ := describedBy[key].(map[string]any)
		for paramName := range params {
			normalized := strings.ToLower(strings.NewReplacer("-", "", "_", "").Replace(paramName))
			// Only client_id/client_secret indicate Anypoint Client ID
			// Enforcement. A bare api-key credential is a different scheme and must
			// not be mislabeled (nor injected with client_id/client_secret headers).
			if normalized == "clientid" || normalized == "clientsecret" {
				return true
			}
		}
	}
	return false
}

func oauth2TokenURL(doc map[string]any) (string, []string) {
	securitySchemes, _ := doc["securitySchemes"].(map[string]any)
	schemes := make([]string, 0)
	for name, raw := range securitySchemes {
		scheme, _ := raw.(map[string]any)
		if len(scheme) == 0 {
			continue
		}
		if !strings.Contains(strings.ToLower(stringValue(scheme["type"])), "oauth") {
			continue
		}
		schemes = append(schemes, name)
		settings, _ := scheme["settings"].(map[string]any)
		if tokenURL := stringValue(settings["accessTokenUri"]); tokenURL != "" {
			sort.Strings(schemes)
			return tokenURL, schemes
		}
	}
	sort.Strings(schemes)
	return "", schemes
}

func sortedResourceKeys(values map[string]any) []string {
	keys := make([]string, 0)
	for key := range values {
		if strings.HasPrefix(key, "/") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys
}

func sortedMapKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func joinRAMLPath(base, child string) string {
	if base == "" || base == "/" {
		return path.Clean("/" + strings.TrimPrefix(child, "/"))
	}
	return path.Clean(strings.TrimRight(base, "/") + "/" + strings.TrimPrefix(child, "/"))
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case nil:
		return ""
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func bodyExampleValue(value any) (string, bool) {
	switch typed := value.(type) {
	case nil:
		return "", false
	case string:
		trimmed := strings.TrimSpace(typed)
		return trimmed, trimmed != ""
	default:
		return jsonBody(typed), true
	}
}

func jsonBody(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
