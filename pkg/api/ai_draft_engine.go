package api

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

func (s *Server) buildAIDraftScript(req AIDraftRequest, provider string, projectID string) (*dsl.Script, string, []AICitation, error) {
	sourceType := normalizeAISourceType(req.SourceType)
	if sourceType == "" {
		return nil, "", nil, fmt.Errorf("source_type is required")
	}
	switch sourceType {
	case "openapi", "har", "sample_request", "user_story", "existing_script":
	default:
		return nil, "", nil, fmt.Errorf("unsupported AI source_type %q", req.SourceType)
	}

	if sourceType == "existing_script" {
		if strings.TrimSpace(req.ExistingScriptID) == "" {
			return nil, "", nil, fmt.Errorf("existing_script_id is required for existing_script source")
		}
		stored, ok := s.runner.GetScript(strings.TrimSpace(req.ExistingScriptID))
		if !ok || stored == nil || stored.Script == nil {
			return nil, "", nil, fmt.Errorf("existing script not found")
		}
		draft := cloneScriptDefinition(stored.Script)
		draft.ID = uuid.New().String()
		draft.Name = firstNonEmpty(req.Name, stored.Name+" AI Draft")
		draft.ProjectID = normalizeProjectID(firstNonEmpty(projectID, stored.ProjectID))
		attachAIProvenance(draft, provider, sourceType, firstNonEmpty(req.SourceName, stored.Name))
		return draft, sourceType, []AICitation{{Label: "Existing script", Path: "/api/v1/scripts/" + stored.ID}}, nil
	}

	baseURL, method, target, actionName := aiDraftEndpointFromRequest(sourceType, req)
	if req.Name == "" {
		req.Name = "AI " + strings.ReplaceAll(sourceType, "_", " ") + " draft"
	}
	draftAction := dsl.SkeletonAction(method)
	draftAction.Name = actionName
	draftAction.Method = method
	draftAction.Target = target
	draftAction.Headers = map[string]string{
		"Accept": "application/json",
	}
	draftAction.Assertions = []dsl.Assertion{{Type: "status_code", Expected: 200}}
	draftAction.Metadata = map[string]any{
		"ai_source_type": sourceType,
		"review_hint":    "Verify path, auth, body examples, and data source bindings before replay.",
	}
	draft := &dsl.Script{
		ID:            uuid.New().String(),
		Name:          strings.TrimSpace(req.Name),
		Version:       "1.0.0",
		Protocol:      "http",
		ProjectID:     projectID,
		GeneratedBy:   "apexload-ai-" + provider,
		GeneratedFrom: "ai_generated",
		Config: dsl.ScriptConfig{
			BaseURL: baseURL,
		},
		Actions: []dsl.Action{draftAction},
		DataSources: []dsl.DataSource{
			{
				Name:           "ai_seed_data",
				Type:           "csv",
				InlineData:     "id,name\n1,example\n",
				Strategy:       "sequential",
				FirstRowHeader: true,
			},
		},
		Parameters: []dsl.Parameter{
			{Name: "sample_id", Type: "file", DataSource: "ai_seed_data", Column: "id", Selection: "sequential", OnExhaustion: "wrap"},
		},
	}
	attachAIProvenance(draft, provider, sourceType, firstNonEmpty(req.SourceName, sourceType))
	return draft, sourceType, []AICitation{
		{Label: "AI authoring contract", Path: "/api/v1/ai/authoring/contract"},
		{Label: "Draft validation", Path: "/api/v1/scripts/validate"},
	}, nil
}

func aiDraftEndpointFromRequest(sourceType string, req AIDraftRequest) (string, string, string, string) {
	baseURL := "https://example.test"
	method := "GET"
	target := "/"
	actionName := "AI generated request"
	if len(req.SourcePayload) > 0 {
		switch sourceType {
		case "openapi":
			baseURL, method, target, actionName = endpointFromOpenAPIPayload(req.SourcePayload, baseURL, method, target, "OpenAPI generated request")
		case "har":
			baseURL, method, target, actionName = endpointFromHARPayload(req.SourcePayload, baseURL, method, target, "HAR generated request")
		case "sample_request":
			baseURL, method, target, actionName = endpointFromSamplePayload(req.SourcePayload, baseURL, method, target, "Sample generated request")
		}
	}
	text := strings.TrimSpace(firstNonEmpty(req.Text, req.Prompt))
	if text != "" {
		if parsedMethod, parsedTarget, parsedBaseURL := endpointFromText(text); parsedTarget != "" {
			method = parsedMethod
			target = parsedTarget
			if parsedBaseURL != "" {
				baseURL = parsedBaseURL
			}
			actionName = strings.ToUpper(method) + " " + target
		} else if sourceType == "user_story" {
			target = "/user-story-flow"
			actionName = "User story acceptance flow"
		}
	}
	return baseURL, method, target, actionName
}

func endpointFromOpenAPIPayload(raw json.RawMessage, fallbackBaseURL string, fallbackMethod string, fallbackTarget string, fallbackName string) (string, string, string, string) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fallbackBaseURL, fallbackMethod, fallbackTarget, fallbackName
	}
	baseURL := fallbackBaseURL
	if servers, ok := payload["servers"].([]any); ok && len(servers) > 0 {
		if server, ok := servers[0].(map[string]any); ok {
			if rawURL, ok := server["url"].(string); ok && strings.TrimSpace(rawURL) != "" {
				baseURL = strings.TrimSpace(rawURL)
			}
		}
	}
	paths, _ := payload["paths"].(map[string]any)
	keys := make([]string, 0, len(paths))
	for path := range paths {
		keys = append(keys, path)
	}
	sort.Strings(keys)
	for _, path := range keys {
		methods, _ := paths[path].(map[string]any)
		for _, candidate := range []string{"get", "post", "put", "patch", "delete"} {
			if _, ok := methods[candidate]; ok {
				return baseURL, strings.ToUpper(candidate), path, "OpenAPI " + strings.ToUpper(candidate) + " " + path
			}
		}
	}
	return baseURL, fallbackMethod, fallbackTarget, fallbackName
}

func endpointFromHARPayload(raw json.RawMessage, fallbackBaseURL string, fallbackMethod string, fallbackTarget string, fallbackName string) (string, string, string, string) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fallbackBaseURL, fallbackMethod, fallbackTarget, fallbackName
	}
	logPayload, _ := payload["log"].(map[string]any)
	entries, _ := logPayload["entries"].([]any)
	if len(entries) == 0 {
		return fallbackBaseURL, fallbackMethod, fallbackTarget, fallbackName
	}
	entry, _ := entries[0].(map[string]any)
	request, _ := entry["request"].(map[string]any)
	method, _ := request["method"].(string)
	rawURL, _ := request["url"].(string)
	baseURL, target := splitAbsoluteURL(rawURL)
	if method == "" {
		method = fallbackMethod
	}
	if baseURL == "" {
		baseURL = fallbackBaseURL
	}
	if target == "" {
		target = fallbackTarget
	}
	return baseURL, strings.ToUpper(method), target, "HAR " + strings.ToUpper(method) + " " + target
}

func endpointFromSamplePayload(raw json.RawMessage, fallbackBaseURL string, fallbackMethod string, fallbackTarget string, fallbackName string) (string, string, string, string) {
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fallbackBaseURL, fallbackMethod, fallbackTarget, fallbackName
	}
	method, _ := payload["method"].(string)
	target, _ := payload["target"].(string)
	rawURL, _ := payload["url"].(string)
	baseURL, parsedTarget := splitAbsoluteURL(rawURL)
	if method == "" {
		method = fallbackMethod
	}
	if target == "" {
		target = parsedTarget
	}
	if target == "" {
		target = fallbackTarget
	}
	if baseURL == "" {
		baseURL = fallbackBaseURL
	}
	return baseURL, strings.ToUpper(method), target, "Sample " + strings.ToUpper(method) + " " + target
}

func endpointFromText(text string) (string, string, string) {
	fields := strings.Fields(text)
	for i, field := range fields {
		method := strings.ToUpper(strings.Trim(field, " \t\r\n:"))
		if !isHTTPMethod(method) {
			continue
		}
		for _, next := range fields[i+1:] {
			candidate := strings.Trim(next, "'\"`")
			if strings.HasPrefix(candidate, "http://") || strings.HasPrefix(candidate, "https://") {
				baseURL, target := splitAbsoluteURL(candidate)
				return method, target, baseURL
			}
			if strings.HasPrefix(candidate, "/") {
				return method, candidate, ""
			}
		}
		return method, "", ""
	}
	return "GET", "", ""
}

func splitAbsoluteURL(raw string) (string, string) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", ""
	}
	target := parsed.EscapedPath()
	if target == "" {
		target = "/"
	}
	if parsed.RawQuery != "" {
		target += "?" + parsed.RawQuery
	}
	return parsed.Scheme + "://" + parsed.Host, target
}

func attachAIProvenance(script *dsl.Script, provider string, sourceType string, sourceName string) {
	if script == nil {
		return
	}
	script.GeneratedBy = "apexload-ai-" + normalizeAIProvider(provider)
	script.GeneratedFrom = "ai_generated"
	if script.Metadata == nil {
		script.Metadata = &dsl.ScriptMetadata{}
	}
	if script.Metadata.Import == nil {
		script.Metadata.Import = &dsl.ImportMetadata{}
	}
	script.Metadata.Import.Source = "ai_generated"
	script.Metadata.Import.SourceScriptName = strings.TrimSpace(sourceName)
	if script.Metadata.Import.Preserved == nil {
		script.Metadata.Import.Preserved = map[string]any{}
	}
	script.Metadata.Import.Preserved["ai_contract_version"] = aiAuthoringContractSchemaVersion
	script.Metadata.Import.Preserved["ai_source_type"] = sourceType
	script.Metadata.Import.Preserved["ai_provider"] = normalizeAIProvider(provider)
	script.Metadata.Import.Preserved["diff_reviewed"] = false
	script.Metadata.Import.Preserved["replay_status"] = "not_run"
	script.Metadata.Import.Preserved["validation_status"] = "pending"
}

func newAIDraftAuthoringSession(draft *dsl.Script, sourceType string, provider string, health AuthoringHealth) *storage.AuthoringSession {
	now := time.Now()
	reviewState := map[string]any{
		"ai_contract_version": aiAuthoringContractSchemaVersion,
		"ai_source_type":      sourceType,
		"ai_provider":         normalizeAIProvider(provider),
		"diff_reviewed":       false,
		"replay_status":       "not_run",
		"validation_status":   health.Status,
	}
	session := &storage.AuthoringSession{
		ID:             uuid.New().String(),
		ProjectID:      normalizeProjectID(draft.ProjectID),
		Source:         "ai_generated",
		Name:           authoringSessionName(draft.Name, draft),
		Status:         "blocked",
		Draft:          cloneScriptDefinition(draft),
		ReviewState:    reviewState,
		CreatedAt:      now,
		UpdatedAt:      now,
		LastAccessedAt: now,
	}
	updateAuthoringSessionReviewReadiness(session, health)
	return session
}
