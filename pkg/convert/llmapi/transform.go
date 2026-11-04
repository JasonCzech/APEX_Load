// Package llmapi detects and transforms captured LLM inference HTTP requests
// (OpenAI Chat/Completions/Responses, Azure OpenAI, HuggingFace TGI, Ollama,
// Triton/NIM) inside an imported DSL script into first-class llm_request
// actions with dialect metadata, streaming config, and governed secret
// references.
package llmapi

import (
	"encoding/json"
	"net/url"
	"strings"

	"github.com/jasonczech/apexload/pkg/convert/credentialpolicy"
	"github.com/jasonczech/apexload/pkg/dsl"
)

const llmSecretRef = "{{secret.llm_api_key}}"

// Review summarizes LLM API calls detected in an imported script.
type Review struct {
	Detected  bool           `json:"detected"`
	Streaming bool           `json:"streaming"`
	Calls     []DetectedCall `json:"calls,omitempty"`
	Dialects  []string       `json:"dialects,omitempty"`
	Models    []string       `json:"models,omitempty"`
}

// DetectedCall describes one LLM inference request found in the script.
type DetectedCall struct {
	ActionName string `json:"action_name"`
	Target     string `json:"target"`
	Dialect    string `json:"dialect"`
	Model      string `json:"model,omitempty"`
	Stream     bool   `json:"stream"`
}

// Change records one mutation applied by Transform.
type Change struct {
	Step       string `json:"step"`
	ActionName string `json:"action_name,omitempty"`
	Detail     string `json:"detail"`
}

// Analyze inspects a converted script for LLM inference HTTP calls.
func Analyze(script *dsl.Script) *Review {
	rev := &Review{}
	if script == nil {
		return rev
	}
	dialectSet := map[string]bool{}
	modelSet := map[string]bool{}
	for _, action := range allActionPtrs(script) {
		info, ok := detectLLMCall(*action, script.Config.BaseURL)
		if !ok {
			continue
		}
		rev.Detected = true
		if info.Stream {
			rev.Streaming = true
		}
		rev.Calls = append(rev.Calls, info)
		if info.Dialect != "" && !dialectSet[info.Dialect] {
			dialectSet[info.Dialect] = true
			rev.Dialects = append(rev.Dialects, info.Dialect)
		}
		if info.Model != "" && !modelSet[info.Model] {
			modelSet[info.Model] = true
			rev.Models = append(rev.Models, info.Model)
		}
	}
	return rev
}

// Transform upgrades detected http_request LLM calls in script to llm_request
// actions in place and replaces captured API keys or Bearer tokens with
// {{secret.llm_api_key}}. It is idempotent.
func Transform(script *dsl.Script) []Change {
	var changes []Change
	if script == nil {
		return changes
	}
	for _, action := range allActionPtrs(script) {
		info, ok := detectLLMCall(*action, script.Config.BaseURL)
		if !ok {
			continue
		}
		if action.Type != "llm_request" {
			if strings.TrimSpace(action.Method) == "" {
				action.Method = "POST"
			}
			action.Type = "llm_request"
			if action.Params == nil {
				action.Params = map[string]interface{}{}
			}
			action.Params["dialect"] = info.Dialect
			action.Params["stream"] = info.Stream
			if info.Model != "" {
				action.Params["model"] = info.Model
			}
			if strings.Contains(action.Body, "{{") {
				action.BodyTemplateEncoding = "json"
				action.Params["body_template_encoding"] = "json"
			}
			changes = append(changes, Change{
				Step:       "upgrade_llm_request",
				ActionName: action.Name,
				Detail:     "Upgraded http_request to llm_request (dialect=" + info.Dialect + ").",
			})
		}
		if referenceProviderKeys(action) {
			changes = append(changes, Change{
				Step:       "scrub_llm_auth",
				ActionName: action.Name,
				Detail:     "Replaced captured LLM API credential with " + llmSecretRef + ".",
			})
		}
	}
	return changes
}

func detectLLMCall(a dsl.Action, baseURL string) (DetectedCall, bool) {
	switch strings.ToLower(strings.TrimSpace(a.Type)) {
	case "http_request", "request", "post", "llm_request":
	default:
		return DetectedCall{}, false
	}
	method := strings.ToUpper(strings.TrimSpace(a.Method))
	if method != "" && method != "POST" {
		return DetectedCall{}, false
	}
	path := extractPath(a.Target, baseURL)
	lowerPath := strings.ToLower(path)

	var bodyObj map[string]any
	if strings.TrimSpace(a.Body) != "" {
		_ = json.Unmarshal([]byte(a.Body), &bodyObj)
	}

	dialect := ""
	model := extractString(bodyObj, "model")
	stream := extractBool(bodyObj, "stream")

	switch {
	case strings.Contains(lowerPath, "/openai/deployments/") && strings.HasSuffix(lowerPath, "/chat/completions"):
		dialect = "azure_openai_chat"
		if model == "" {
			model = extractSegmentAfter(path, "deployments")
		}
	case strings.HasSuffix(lowerPath, "/v1/chat/completions") || strings.HasSuffix(lowerPath, "/chat/completions"):
		dialect = "openai_chat"
	case strings.HasSuffix(lowerPath, "/v1/completions"):
		dialect = "openai_completions"
	case strings.HasSuffix(lowerPath, "/v1/responses") || strings.HasSuffix(lowerPath, "/openai/responses"):
		// OpenAI /v1/responses, Azure /openai/v1/responses and
		// /openai/responses?api-version=...
		dialect = "openai_responses"
	case strings.HasSuffix(lowerPath, "/generate_stream") && strings.Contains(lowerPath, "/v2/models/"):
		dialect = "triton_generate"
		stream = true
		if model == "" {
			model = extractSegmentAfter(path, "models")
		}
	case strings.HasSuffix(lowerPath, "/generate_stream") || strings.HasSuffix(lowerPath, "/generate"):
		if _, hasInputs := bodyObj["inputs"]; hasInputs || strings.HasSuffix(lowerPath, "/generate_stream") {
			dialect = "tgi_generate"
			if strings.HasSuffix(lowerPath, "/generate_stream") {
				stream = true
			}
		}
	case strings.HasSuffix(lowerPath, "/api/chat") || strings.HasSuffix(lowerPath, "/api/generate"):
		dialect = "ollama_chat"
		if _, explicitStream := bodyObj["stream"]; !explicitStream {
			stream = true
		}
	}

	if dialect == "" {
		return DetectedCall{}, false
	}
	for k, v := range a.Headers {
		if strings.EqualFold(k, "Accept") && strings.Contains(strings.ToLower(v), "text/event-stream") {
			stream = true
		}
	}
	for _, h := range a.HeaderList {
		if strings.EqualFold(h.Name, "Accept") && strings.Contains(strings.ToLower(h.Value), "text/event-stream") {
			stream = true
		}
	}
	if a.Type == "llm_request" && a.Params != nil {
		if d, ok := a.Params["dialect"].(string); ok && d != "" {
			dialect = d
		}
		if s, ok := a.Params["stream"].(bool); ok {
			stream = s
		}
		if m, ok := a.Params["model"].(string); ok && m != "" {
			model = m
		}
	}
	return DetectedCall{
		ActionName: a.Name,
		Target:     a.Target,
		Dialect:    dialect,
		Model:      model,
		Stream:     stream,
	}, true
}

// referenceProviderKeys points the action's model-provider key headers at
// llmSecretRef (the replacement rule is credentialpolicy's).
func referenceProviderKeys(a *dsl.Action) bool {
	if a == nil {
		return false
	}
	changed := false
	for k, v := range a.Headers {
		if replacement, ok := credentialpolicy.ProviderKeyHeaderReference(k, v, llmSecretRef); ok {
			a.Headers[k] = replacement
			changed = true
		}
	}
	for i := range a.HeaderList {
		if replacement, ok := credentialpolicy.ProviderKeyHeaderReference(a.HeaderList[i].Name, a.HeaderList[i].Value, llmSecretRef); ok {
			a.HeaderList[i].Value = replacement
			changed = true
		}
	}
	return changed
}

func extractPath(target, baseURL string) string {
	if u, err := url.Parse(target); err == nil && u.Path != "" {
		return u.Path
	}
	if idx := strings.IndexByte(target, '?'); idx >= 0 {
		target = target[:idx]
	}
	if strings.HasPrefix(target, "/") {
		return target
	}
	if u, err := url.Parse(baseURL); err == nil && u.Path != "" {
		return strings.TrimRight(u.Path, "/") + "/" + strings.TrimLeft(target, "/")
	}
	return "/" + strings.TrimLeft(target, "/")
}

func extractSegmentAfter(path, marker string) string {
	parts := strings.Split(path, "/")
	for i := 0; i+1 < len(parts); i++ {
		if strings.EqualFold(parts[i], marker) && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return ""
}

func extractString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if v, ok := m[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func extractBool(m map[string]any, key string) bool {
	if m == nil {
		return false
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

func allActionPtrs(script *dsl.Script) []*dsl.Action {
	var out []*dsl.Action
	var walk func(slice []dsl.Action)
	walk = func(slice []dsl.Action) {
		for i := range slice {
			a := &slice[i]
			out = append(out, a)
			if a.Control != nil {
				walk(a.Control.Steps)
				walk(a.Control.Else)
				walk(a.Control.Default)
				for ci := range a.Control.Cases {
					walk(a.Control.Cases[ci].Steps)
				}
				for bi := range a.Control.Branches {
					walk(a.Control.Branches[bi].Steps)
				}
			}
		}
	}
	walk(script.Init)
	walk(script.Actions)
	walk(script.Teardown)
	return out
}
