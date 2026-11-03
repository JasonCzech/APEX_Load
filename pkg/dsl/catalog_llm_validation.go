package dsl

import (
	"fmt"
	"strings"
)

func validateLLMRequestActionIssues(index int, action Action) []string {
	label := fmt.Sprintf("actions[%d] %q", index, action.Name)
	var issues []string
	if strings.TrimSpace(action.Target) == "" {
		issues = append(issues, fmt.Sprintf("%s: missing target", label))
	}
	return append(issues, validateLLMRequestActionParamsIssues(label, action)...)
}

func validateLLMRequestActionParamsIssues(label string, action Action) []string {
	var issues []string

	hasInput := strings.TrimSpace(action.Body) != "" || strings.TrimSpace(action.BodyFile) != ""
	if action.Params != nil {
		if prompt, ok := action.Params["prompt"].(string); ok && strings.TrimSpace(prompt) != "" {
			hasInput = true
		}
		if convVar, ok := action.Params["conversation_var"].(string); ok && strings.TrimSpace(convVar) != "" {
			hasInput = true
		}
		if histVar, ok := action.Params["history_var"].(string); ok && strings.TrimSpace(histVar) != "" {
			hasInput = true
		}
		switch conv := action.Params["conversation"].(type) {
		case bool:
			if conv {
				hasInput = true
			}
		case map[string]any:
			hasInput = true
		}
		switch msgs := action.Params["messages"].(type) {
		case string:
			if strings.TrimSpace(msgs) != "" {
				hasInput = true
			}
		case []any:
			if len(msgs) > 0 {
				hasInput = true
			}
		case []map[string]any:
			if len(msgs) > 0 {
				hasInput = true
			}
		}

		for _, key := range []string{"dialect", "llm_dialect"} {
			if raw, exists := action.Params[key]; exists && raw != nil {
				s, ok := raw.(string)
				if !ok {
					issues = append(issues, fmt.Sprintf("%s: params.%s must be a string", label, key))
					continue
				}
				trimmed := strings.ToLower(strings.TrimSpace(s))
				if trimmed != "" && !strings.Contains(trimmed, "{{") && !isValidLLMDialectName(trimmed) {
					issues = append(issues, fmt.Sprintf("%s: unsupported params.%s %q", label, key, s))
				}
			}
		}

		if raw, exists := action.Params["stream_profile"]; exists && raw != nil {
			switch typed := raw.(type) {
			case string:
				trimmed := strings.ToLower(strings.TrimSpace(typed))
				if trimmed != "" && !strings.Contains(trimmed, "{{") && !isValidLLMDialectName(trimmed) {
					issues = append(issues, fmt.Sprintf("%s: unsupported params.stream_profile %q", label, typed))
				}
			case map[string]any:
				for _, field := range []string{"preset", "delta_path", "finish_path", "usage_path"} {
					val, present := typed[field]
					if !present || val == nil {
						continue
					}
					s, ok := val.(string)
					if !ok {
						issues = append(issues, fmt.Sprintf("%s: params.stream_profile.%s must be a string", label, field))
						continue
					}
					if field == "preset" {
						trimmed := strings.ToLower(strings.TrimSpace(s))
						if trimmed != "" && !strings.Contains(trimmed, "{{") && !isValidLLMDialectName(trimmed) {
							issues = append(issues, fmt.Sprintf("%s: unsupported params.stream_profile.preset %q", label, s))
						}
					}
				}
			default:
				issues = append(issues, fmt.Sprintf("%s: params.stream_profile must be a string or object", label))
			}
		}

		if maxToks, ok := actionParamFloat(action.Params, "max_tokens"); ok && maxToks < 0 {
			issues = append(issues, fmt.Sprintf("%s: params.max_tokens must be >= 0", label))
		}
		if temp, ok := actionParamFloat(action.Params, "temperature"); ok && (temp < 0 || temp > 2) {
			issues = append(issues, fmt.Sprintf("%s: params.temperature must be between 0 and 2", label))
		}
		if topP, ok := actionParamFloat(action.Params, "top_p"); ok && (topP < 0 || topP > 1) {
			issues = append(issues, fmt.Sprintf("%s: params.top_p must be between 0 and 1", label))
		}
		for _, k := range []string{"max_turns", "max_history_turns", "max_history_tokens", "max_context_tokens"} {
			if v, ok := actionParamFloat(action.Params, k); ok && v < 0 {
				issues = append(issues, fmt.Sprintf("%s: params.%s must be >= 0", label, k))
			}
		}
		if maxHistBytes, ok := actionParamFloat(action.Params, "max_history_bytes"); ok && (maxHistBytes < 0 || maxHistBytes > 64*1024) {
			issues = append(issues, fmt.Sprintf("%s: params.max_history_bytes must be between 0 and 65536", label))
		}
		if rawStrat, exists := action.Params["truncation_strategy"]; exists && rawStrat != nil {
			if s, ok := rawStrat.(string); !ok {
				issues = append(issues, fmt.Sprintf("%s: params.truncation_strategy must be a string", label))
			} else if trimmed := strings.ToLower(strings.TrimSpace(s)); trimmed != "" && !strings.Contains(trimmed, "{{") && trimmed != "sliding_window" && trimmed != "drop_oldest" {
				issues = append(issues, fmt.Sprintf("%s: unsupported params.truncation_strategy %q", label, s))
			}
		}
		issues = append(issues, validateLLMConversationObjectIssues(label, action.Params["conversation"])...)
	}

	if !hasInput {
		issues = append(issues, fmt.Sprintf("%s: llm_request requires body, params.prompt, params.messages, or params.conversation_var", label))
	}
	return issues
}

func validateLLMConversationObjectIssues(label string, raw any) []string {
	if raw == nil {
		return nil
	}
	switch typed := raw.(type) {
	case bool:
		return nil
	case map[string]any:
		var issues []string
		for _, k := range []string{"history_var", "conversation_var", "system", "system_prompt"} {
			if val, exists := typed[k]; exists && val != nil {
				if _, ok := val.(string); !ok {
					issues = append(issues, fmt.Sprintf("%s: params.conversation.%s must be a string", label, k))
				}
			}
		}
		for _, k := range []string{"max_turns", "max_history_turns", "max_history_tokens", "max_context_tokens"} {
			if v, ok := actionParamFloat(typed, k); ok && v < 0 {
				issues = append(issues, fmt.Sprintf("%s: params.conversation.%s must be >= 0", label, k))
			}
		}
		if maxHistBytes, ok := actionParamFloat(typed, "max_history_bytes"); ok && (maxHistBytes < 0 || maxHistBytes > 64*1024) {
			issues = append(issues, fmt.Sprintf("%s: params.conversation.max_history_bytes must be between 0 and 65536", label))
		}
		if rawStrat, exists := typed["truncation_strategy"]; exists && rawStrat != nil {
			if s, ok := rawStrat.(string); !ok {
				issues = append(issues, fmt.Sprintf("%s: params.conversation.truncation_strategy must be a string", label))
			} else if trimmed := strings.ToLower(strings.TrimSpace(s)); trimmed != "" && !strings.Contains(trimmed, "{{") && trimmed != "sliding_window" && trimmed != "drop_oldest" {
				issues = append(issues, fmt.Sprintf("%s: unsupported params.conversation.truncation_strategy %q", label, s))
			}
		}
		return issues
	default:
		return []string{fmt.Sprintf("%s: params.conversation must be a boolean or object", label)}
	}
}

func isValidLLMDialectName(name string) bool {
	for _, allowed := range LLMDialectNames {
		if name == allowed {
			return true
		}
	}
	for _, alias := range LLMDialectAliases {
		if name == alias {
			return true
		}
	}
	return false
}

func actionParamFloat(params map[string]any, key string) (float64, bool) {
	if params == nil {
		return 0, false
	}
	switch v := params[key].(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	default:
		return 0, false
	}
}
