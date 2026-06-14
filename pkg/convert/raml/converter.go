package raml

import (
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"

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
	Title       string   `json:"title,omitempty"`
	Version     string   `json:"version,omitempty"`
	BaseURL     string   `json:"base_url,omitempty"`
	Resources   []string `json:"resources,omitempty"`
	AuthSchemes []string `json:"auth_schemes,omitempty"`
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
	var doc map[string]any
	if err := yaml.Unmarshal([]byte(req.Content), &doc); err != nil {
		return nil, fmt.Errorf("parse RAML: %w", err)
	}
	if len(doc) == 0 {
		return nil, fmt.Errorf("RAML document is empty")
	}
	baseURL := firstNonEmpty(req.Options.BaseURL, stringValue(doc["baseUri"]), "{{env.BASE_URL}}")
	name := firstNonEmpty(req.Options.ScriptName, req.Name, stringValue(doc["title"]), "RAML Import")
	result := &ConvertResult{
		Stats: ConvertStats{Format: "raml"},
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

	resources := sortedResourceKeys(doc)
	for _, resource := range resources {
		node, _ := doc[resource].(map[string]any)
		walkResource(resource, node, result, script, len(script.Init) > 0)
	}
	if len(script.Actions) == 0 {
		return nil, fmt.Errorf("RAML document did not contain HTTP resources or methods")
	}
	result.Script = script
	result.Stats.ConvertedActions = len(script.Actions)
	return result, nil
}

func walkResource(resourcePath string, node map[string]any, result *ConvertResult, script *dsl.Script, withOAuth bool) {
	if node == nil {
		return
	}
	result.Stats.TotalResources++
	result.Summary.Resources = append(result.Summary.Resources, resourcePath)
	keys := sortedMapKeys(node)
	for _, key := range keys {
		lower := strings.ToLower(key)
		if !ramlMethods[lower] {
			continue
		}
		child, _ := node[key].(map[string]any)
		result.Stats.TotalMethods++
		action := dsl.Action{
			Name:   firstNonEmpty(stringValue(child["displayName"]), stringValue(child["description"]), strings.ToUpper(lower)+" "+resourcePath),
			Type:   strings.ToUpper(lower),
			Method: strings.ToUpper(lower),
			Target: resourcePath,
			Headers: map[string]string{
				"Accept": "application/json",
			},
			Assertions: []dsl.Assertion{{Type: "status_code", Expected: 200}},
		}
		if withOAuth {
			action.Headers["Authorization"] = "Bearer {{var.access_token}}"
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
		walkResource(joinRAMLPath(resourcePath, key), child, result, script, withOAuth)
	}
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

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func jsonBody(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return "{}"
	}
	return string(raw)
}
