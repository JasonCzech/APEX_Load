package http

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

// prepareLLMRequestAction normalizes an llm_request action into an HTTP POST
// with a synthesized JSON request body (when action.Body is empty) and default
// streaming parameters.
func prepareLLMRequestAction(action *protocol.Action, resolvedTargetURL string) (*protocol.Action, error) {
	if action == nil || !strings.EqualFold(strings.TrimSpace(action.Type), "llm_request") {
		return action, nil
	}
	cp := *action
	if cp.Method == "" || strings.EqualFold(cp.Method, "llm_request") {
		cp.Method = "POST"
	}
	dialect := resolveActionLLMDialect(cp.Params, resolvedTargetURL)
	streamEnabled := true
	inferredStreamMode := ""
	if cp.Params != nil {
		if b, ok := boolParam(cp.Params["stream"]); ok {
			streamEnabled = b
		} else if s, ok := cp.Params["stream"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(s)) {
			case "none":
				streamEnabled = false
			case "ndjson", "jsonl", "application/x-ndjson", "application/ndjson", "application/jsonl":
				streamEnabled = true
				inferredStreamMode = "ndjson"
			}
		} else if mode, ok := cp.Params["stream_mode"].(string); ok {
			switch strings.ToLower(strings.TrimSpace(mode)) {
			case "none", "false", "0", "no":
				streamEnabled = false
			}
		}
	}
	params := make(map[string]any, len(cp.Params)+3)
	for k, v := range cp.Params {
		params[k] = v
	}
	if inferredStreamMode != "" && params["stream_mode"] == nil {
		params["stream_mode"] = inferredStreamMode
	}
	params["llm"] = true
	params["stream"] = streamEnabled
	cp.Params = params

	if len(cp.Body) == 0 {
		bodyBytes, err := buildLLMRequestBody(dialect, cp.Params, streamEnabled)
		if err != nil {
			return nil, err
		}
		cp.Body = bodyBytes
		if len(cp.RedactedParams) > 0 {
			if redBytes, redErr := buildLLMRequestBody(dialect, cp.RedactedParams, streamEnabled); redErr == nil {
				cp.RedactedBody = redBytes
			} else {
				cp.RedactedBody = bodyBytes
			}
		} else {
			cp.RedactedBody = bodyBytes
		}
	}
	return &cp, nil
}

func buildLLMRequestBody(dialect string, params map[string]any, stream bool) ([]byte, error) {
	payload := make(map[string]any, 8)
	if params != nil {
		if extra, ok := params["extra_body"].(map[string]any); ok {
			for k, v := range extra {
				payload[k] = v
			}
		}
	}
	model, _ := params["model"].(string)
	model = strings.TrimSpace(model)
	if model != "" && dialect != LLMDialectTGI && dialect != LLMDialectTriton {
		payload["model"] = model
	}

	prompt, _ := params["prompt"].(string)
	systemPrompt, _ := params["system"].(string)
	if strings.TrimSpace(systemPrompt) == "" {
		systemPrompt, _ = params["system_prompt"].(string)
	}

	switch dialect {
	case LLMDialectTGI:
		inputs := prompt
		if inputs == "" {
			inputs = extractPromptFromMessagesParam(params["messages"])
		}
		payload["inputs"] = inputs
		payload["stream"] = stream
		parameters := make(map[string]any)
		if prev, ok := payload["parameters"].(map[string]any); ok {
			for k, v := range prev {
				parameters[k] = v
			}
		}
		if maxToks, ok := intParam(params["max_tokens"]); ok && maxToks > 0 {
			parameters["max_new_tokens"] = maxToks
		}
		if temp, ok := floatParam(params["temperature"]); ok {
			parameters["temperature"] = temp
		}
		if topP, ok := floatParam(params["top_p"]); ok {
			parameters["top_p"] = topP
		}
		if seed, ok := intParam(params["seed"]); ok {
			parameters["seed"] = seed
		}
		if ignoreEOS, ok := boolParam(params["ignore_eos"]); ok && ignoreEOS {
			parameters["ignore_eos"] = true
		}
		// TGI reports the finish reason and generated token count only with
		// details; without them a stream has no finish to account.
		if _, set := parameters["details"]; !set {
			parameters["details"] = true
		}
		payload["parameters"] = parameters

	case LLMDialectTriton:
		textInput := prompt
		if textInput == "" {
			textInput = extractPromptFromMessagesParam(params["messages"])
		}
		payload["text_input"] = textInput
		payload["stream"] = stream
		if maxToks, ok := intParam(params["max_tokens"]); ok && maxToks > 0 {
			payload["max_tokens"] = maxToks
		}
		if temp, ok := floatParam(params["temperature"]); ok {
			payload["temperature"] = temp
		}

	case LLMDialectOpenAICompletions:
		p := prompt
		if p == "" {
			p = extractPromptFromMessagesParam(params["messages"])
		}
		payload["prompt"] = p
		payload["stream"] = stream
		applyCommonOpenAIFields(payload, params, stream)

	case LLMDialectOpenAIResponses:
		// The Responses API takes input (a string, or chat-style
		// {role, content} items), instructions for the system prompt,
		// max_output_tokens and text.format; it has no stream_options
		// (usage arrives on the terminal event) and no [DONE].
		msgs, err := buildMessagesField(params["messages"], "", "")
		if err != nil {
			return nil, err
		}
		if msgs != nil {
			payload["input"] = msgs
		} else {
			payload["input"] = prompt
		}
		if strings.TrimSpace(systemPrompt) != "" {
			payload["instructions"] = systemPrompt
		}
		payload["stream"] = stream
		if maxToks, ok := intParam(params["max_tokens"]); ok && maxToks > 0 {
			// OpenAI and Azure reject a max_output_tokens below 16.
			payload["max_output_tokens"] = max(maxToks, minResponsesOutputTokens)
		}
		if temp, ok := floatParam(params["temperature"]); ok {
			payload["temperature"] = temp
		}
		if topP, ok := floatParam(params["top_p"]); ok {
			payload["top_p"] = topP
		}
		if ignoreEOS, ok := boolParam(params["ignore_eos"]); ok && ignoreEOS {
			payload["ignore_eos"] = true
		}
		if _, exists := payload["text"]; !exists {
			if format := responsesTextFormat(params); format != nil {
				payload["text"] = map[string]any{"format": format}
			}
		}

	case LLMDialectOllama:
		msgs, err := buildMessagesField(params["messages"], systemPrompt, prompt)
		if err != nil {
			return nil, err
		}
		if msgs != nil {
			payload["messages"] = msgs
		} else if prompt != "" {
			payload["prompt"] = prompt
		}
		payload["stream"] = stream
		options := make(map[string]any)
		if maxToks, ok := intParam(params["max_tokens"]); ok && maxToks > 0 {
			options["num_predict"] = maxToks
		}
		if temp, ok := floatParam(params["temperature"]); ok {
			options["temperature"] = temp
		}
		if topP, ok := floatParam(params["top_p"]); ok {
			options["top_p"] = topP
		}
		if seed, ok := intParam(params["seed"]); ok {
			options["seed"] = seed
		}
		if len(options) > 0 {
			payload["options"] = options
		}

	default:
		// OpenAI Chat / Azure OpenAI / Vercel AI / LangServe
		msgs, err := buildMessagesField(params["messages"], systemPrompt, prompt)
		if err != nil {
			return nil, err
		}
		if msgs != nil {
			payload["messages"] = msgs
		}
		payload["stream"] = stream
		applyCommonOpenAIFields(payload, params, stream)
	}

	return json.Marshal(payload)
}

func applyCommonOpenAIFields(payload map[string]any, params map[string]any, stream bool) {
	if params == nil {
		return
	}
	if maxToks, ok := intParam(params["max_tokens"]); ok && maxToks > 0 {
		payload["max_tokens"] = maxToks
	}
	if temp, ok := floatParam(params["temperature"]); ok {
		payload["temperature"] = temp
	}
	if topP, ok := floatParam(params["top_p"]); ok {
		payload["top_p"] = topP
	}
	if seed, ok := intParam(params["seed"]); ok {
		payload["seed"] = seed
	}
	if stop, ok := params["stop"]; ok && stop != nil {
		payload["stop"] = stop
	}
	if ignoreEOS, ok := boolParam(params["ignore_eos"]); ok && ignoreEOS {
		payload["ignore_eos"] = true
	}
	if rf, ok := params["response_format"]; ok && rf != nil {
		payload["response_format"] = rf
	} else if schema, ok := params["json_schema"]; ok && schema != nil {
		payload["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "response",
				"schema": schema,
			},
		}
	}
	if stream {
		includeUsage := true
		if inc, ok := boolParam(params["include_usage"]); ok {
			includeUsage = inc
		}
		if includeUsage {
			if _, exists := payload["stream_options"]; !exists {
				payload["stream_options"] = map[string]any{"include_usage": true}
			}
		}
	}
}

// minResponsesOutputTokens is the smallest max_output_tokens the OpenAI and
// Azure Responses API accept.
const minResponsesOutputTokens = 16

// responsesTextFormat maps params.response_format (chat shape) or
// params.json_schema to a Responses API text.format object.
func responsesTextFormat(params map[string]any) map[string]any {
	if rf, ok := params["response_format"].(map[string]any); ok {
		if typ, _ := rf["type"].(string); typ == "json_schema" {
			if inner, ok := rf["json_schema"].(map[string]any); ok {
				format := make(map[string]any, len(inner)+1)
				for k, v := range inner {
					format[k] = v
				}
				format["type"] = "json_schema"
				return format
			}
		}
		return rf
	}
	schema, ok := params["json_schema"]
	if !ok || schema == nil {
		return nil
	}
	if raw, isString := schema.(string); isString {
		var parsed map[string]any
		if json.Unmarshal([]byte(raw), &parsed) != nil {
			return nil
		}
		schema = parsed
	}
	if m, isMap := schema.(map[string]any); isMap {
		// A {name, schema} wrapper, as params.json_schema may be authored.
		if inner, hasInner := m["schema"].(map[string]any); hasInner {
			name, _ := m["name"].(string)
			if strings.TrimSpace(name) == "" {
				name = "response"
			}
			return map[string]any{"type": "json_schema", "name": name, "schema": inner}
		}
	}
	return map[string]any{"type": "json_schema", "name": "response", "schema": schema}
}

func hasLeadingSystemMessageAny(msgs []any) bool {
	if len(msgs) == 0 {
		return false
	}
	m, ok := msgs[0].(map[string]any)
	if !ok {
		return false
	}
	role, _ := m["role"].(string)
	return strings.EqualFold(strings.TrimSpace(role), "system")
}

func hasLeadingSystemMessageMaps(msgs []map[string]any) bool {
	if len(msgs) == 0 {
		return false
	}
	role, _ := msgs[0]["role"].(string)
	return strings.EqualFold(strings.TrimSpace(role), "system")
}

func buildMessagesField(rawMessages any, systemPrompt, userPrompt string) (any, error) {
	switch typed := rawMessages.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if trimmed != "" {
			if !json.Valid([]byte(trimmed)) {
				return nil, fmt.Errorf("llm_request params.messages string is not valid JSON")
			}
			var arr []any
			if err := json.Unmarshal([]byte(trimmed), &arr); err != nil {
				return nil, fmt.Errorf("llm_request params.messages must be a JSON array: %w", err)
			}
			if strings.TrimSpace(systemPrompt) != "" && !hasLeadingSystemMessageAny(arr) {
				withSys := make([]any, 0, len(arr)+1)
				withSys = append(withSys, map[string]any{"role": "system", "content": systemPrompt})
				withSys = append(withSys, arr...)
				return withSys, nil
			}
			return json.RawMessage(bytes.TrimSpace([]byte(trimmed))), nil
		}
	case []any:
		if len(typed) > 0 {
			if strings.TrimSpace(systemPrompt) != "" && !hasLeadingSystemMessageAny(typed) {
				withSys := make([]any, 0, len(typed)+1)
				withSys = append(withSys, map[string]any{"role": "system", "content": systemPrompt})
				withSys = append(withSys, typed...)
				return withSys, nil
			}
			return typed, nil
		}
	case []map[string]any:
		if len(typed) > 0 {
			if strings.TrimSpace(systemPrompt) != "" && !hasLeadingSystemMessageMaps(typed) {
				withSys := make([]map[string]any, 0, len(typed)+1)
				withSys = append(withSys, map[string]any{"role": "system", "content": systemPrompt})
				withSys = append(withSys, typed...)
				return withSys, nil
			}
			return typed, nil
		}
	}
	var msgs []map[string]any
	if strings.TrimSpace(systemPrompt) != "" {
		msgs = append(msgs, map[string]any{"role": "system", "content": systemPrompt})
	}
	if strings.TrimSpace(userPrompt) != "" {
		msgs = append(msgs, map[string]any{"role": "user", "content": userPrompt})
	}
	if len(msgs) == 0 {
		return nil, nil
	}
	return msgs, nil
}

func extractPromptFromMessagesParam(rawMessages any) string {
	switch typed := rawMessages.(type) {
	case string:
		var msgs []struct {
			Content string `json:"content"`
		}
		if json.Unmarshal([]byte(typed), &msgs) == nil && len(msgs) > 0 {
			return msgs[len(msgs)-1].Content
		}
	case []any:
		for i := len(typed) - 1; i >= 0; i-- {
			if m, ok := typed[i].(map[string]any); ok {
				if c, ok := m["content"].(string); ok && c != "" {
					return c
				}
			}
		}
	case []map[string]any:
		for i := len(typed) - 1; i >= 0; i-- {
			if c, ok := typed[i]["content"].(string); ok && c != "" {
				return c
			}
		}
	}
	return ""
}

func floatParam(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}

func intParam(value any) (int, bool) {
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case float32:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	default:
		return 0, false
	}
}

func boolParam(value any) (bool, bool) {
	switch v := value.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes":
			return true, true
		case "false", "0", "no":
			return false, true
		}
	}
	return false, false
}
