package http

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"unsafe"
)

const (
	LLMDialectOpenAIChat        = "openai_chat"
	LLMDialectOpenAICompletions = "openai_completions"
	LLMDialectOpenAIResponses   = "openai_responses"
	LLMDialectAzureOpenAI       = "azure_openai"
	LLMDialectTGI               = "tgi"
	LLMDialectOllama            = "ollama"
	LLMDialectTriton            = "triton"
	LLMDialectVercelAI          = "vercel_ai"
	LLMDialectLangServe         = "langserve"
	LLMDialectRawText           = "raw_text"
)

func normalizeLLMDialect(raw string, targetURL string) string {
	d := strings.ToLower(strings.TrimSpace(raw))
	switch d {
	case "openai", "azure", "aoai", "foundry", "vllm", "nim":
		// A provider name, not a wire format: these providers serve the
		// Responses API too, which their /responses path selects.
		if isResponsesTarget(targetURL) {
			return LLMDialectOpenAIResponses
		}
	}
	switch d {
	case "openai", "openai_chat", "chat", "chat_completions", "vllm", "nim":
		return LLMDialectOpenAIChat
	case "openai_completions", "completions":
		return LLMDialectOpenAICompletions
	case "openai_responses", "responses", "azure_openai_responses":
		return LLMDialectOpenAIResponses
	case "azure", "azure_openai", "azure_openai_chat", "aoai", "foundry":
		return LLMDialectAzureOpenAI
	case "tgi", "tgi_generate":
		return LLMDialectTGI
	case "ollama", "ollama_chat", "ndjson":
		return LLMDialectOllama
	case "triton", "triton_generate", "tensorrt_llm":
		return LLMDialectTriton
	case "vercel_ai", "vercel", "vercel_ai_data_stream":
		return LLMDialectVercelAI
	case "langserve", "langserve_sse":
		return LLMDialectLangServe
	case "raw_text", "raw":
		return LLMDialectRawText
	}
	lowerTarget := strings.ToLower(targetURL)
	switch {
	case isResponsesTarget(targetURL):
		return LLMDialectOpenAIResponses
	case strings.Contains(lowerTarget, "/openai/deployments/"):
		return LLMDialectAzureOpenAI
	case strings.Contains(lowerTarget, "/api/chat") || strings.Contains(lowerTarget, "/api/generate"):
		return LLMDialectOllama
	case strings.Contains(lowerTarget, "/generate_stream") || strings.HasSuffix(lowerTarget, "/generate"):
		if strings.Contains(lowerTarget, "/v2/models/") {
			return LLMDialectTriton
		}
		return LLMDialectTGI
	case strings.HasSuffix(lowerTarget, "/v1/completions"):
		return LLMDialectOpenAICompletions
	default:
		return LLMDialectOpenAIChat
	}
}

var (
	keyContent          = []byte(`"content":"`)
	keyContentSpaced    = []byte(`"content": "`)
	keyReasoningContent = []byte(`"reasoning_content":"`)
	keyReasoningSpaced  = []byte(`"reasoning_content": "`)
	keyReasoningShort   = []byte(`"reasoning":"`)
	keyText             = []byte(`"text":"`)
	keyTextSpaced       = []byte(`"text": "`)
	keyResponse         = []byte(`"response":"`)
	keyResponseSpaced   = []byte(`"response": "`)
	keyTextOutput       = []byte(`"text_output":"`)
	keyTextOutputSpaced = []byte(`"text_output": "`)
	keyModel            = []byte(`"model":"`)
	keyModelSpaced      = []byte(`"model": "`)

	markerUsage        = []byte(`"usage"`)
	markerFinishReason = []byte(`"finish_reason"`)
	markerDoneTrue     = []byte(`"done":true`)
	markerDoneSpaced   = []byte(`"done": true`)
	markerDetails      = []byte(`"details"`)
	markerToolCalls    = []byte(`"tool_calls"`)
	markerError        = []byte(`"error"`)
	markerStopReason   = []byte(`"stop_reason"`)
	markerEvalCount    = []byte(`"eval_count"`)
)

// llmChunkScanResult holds zero-allocation extracted slices from a single chunk.
type llmChunkScanResult struct {
	hasVisibleToken   bool
	hasReasoningToken bool
	visibleRaw        []byte // JSON-escaped bytes inside quotes
	reasoningRaw      []byte
	modelRaw          []byte
	needsSlowParse    bool
}

// scanLLMChunkFast performs a zero-allocation byte scan of a single SSE/NDJSON
// data payload to detect visible content deltas, reasoning deltas, model name,
// and whether a slow-path json.Unmarshal is needed for usage/finish_reason/error.
func scanLLMChunkFast(dialect string, data []byte) llmChunkScanResult {
	var res llmChunkScanResult
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		return res
	}
	if dialect == LLMDialectRawText {
		res.hasVisibleToken = true
		res.visibleRaw = trimmed
		return res
	}
	if dialect == LLMDialectVercelAI {
		// Vercel AI data stream format: 0:"token" or e:{...} or d:{...}
		if len(trimmed) >= 4 && trimmed[0] == '0' && trimmed[1] == ':' && trimmed[2] == '"' {
			if val, ok := extractJSONQuotedBytes(trimmed[3:]); ok && len(val) > 0 {
				res.hasVisibleToken = true
				res.visibleRaw = val
			}
			return res
		}
		if len(trimmed) >= 2 && (trimmed[0] == 'e' || trimmed[0] == 'd' || trimmed[0] == '3') && trimmed[1] == ':' {
			res.needsSlowParse = true
		}
		return res
	}
	if dialect == LLMDialectLangServe && len(trimmed) >= 2 && trimmed[0] == '"' {
		if val, ok := extractJSONQuotedBytes(trimmed[1:]); ok && len(val) > 0 {
			res.hasVisibleToken = true
			res.visibleRaw = val
		}
		return res
	}

	if val, ok := findJSONStringField(trimmed, keyModel, keyModelSpaced); ok {
		res.modelRaw = val
	}

	if val, ok := findJSONStringField(trimmed, keyReasoningContent, keyReasoningSpaced); ok && len(val) > 0 {
		res.hasReasoningToken = true
		res.reasoningRaw = val
	} else if val, ok := findJSONStringField(trimmed, keyReasoningShort, nil); ok && len(val) > 0 {
		res.hasReasoningToken = true
		res.reasoningRaw = val
	}

	switch dialect {
	case LLMDialectOpenAICompletions:
		if val, ok := findJSONStringField(trimmed, keyText, keyTextSpaced); ok && len(val) > 0 {
			res.hasVisibleToken = true
			res.visibleRaw = val
		}
	case LLMDialectTGI:
		if val, ok := findJSONStringField(trimmed, keyText, keyTextSpaced); ok && len(val) > 0 {
			res.hasVisibleToken = true
			res.visibleRaw = val
		}
	case LLMDialectOllama:
		// A thinking model streams its reasoning in "thinking" (message.thinking
		// on /api/chat, thinking on /api/generate) beside an empty content.
		if val, ok := findJSONStringField(trimmed, keyThinking, keyThinkingSpaced); ok && len(val) > 0 {
			res.hasReasoningToken = true
			res.reasoningRaw = val
		}
		if val, ok := findJSONStringField(trimmed, keyContent, keyContentSpaced); ok && len(val) > 0 {
			res.hasVisibleToken = true
			res.visibleRaw = val
		} else if val, ok := findJSONStringField(trimmed, keyResponse, keyResponseSpaced); ok && len(val) > 0 {
			res.hasVisibleToken = true
			res.visibleRaw = val
		}
	case LLMDialectTriton:
		if val, ok := findJSONStringField(trimmed, keyTextOutput, keyTextOutputSpaced); ok && len(val) > 0 {
			res.hasVisibleToken = true
			res.visibleRaw = val
		}
	default:
		if val, ok := findJSONStringField(trimmed, keyContent, keyContentSpaced); ok && len(val) > 0 {
			res.hasVisibleToken = true
			res.visibleRaw = val
		} else if val, ok := findJSONStringField(trimmed, keyText, keyTextSpaced); ok && len(val) > 0 {
			res.hasVisibleToken = true
			res.visibleRaw = val
		}
	}

	// Check if slow-path JSON unmarshaling is needed.
	// Note: many OpenAI chunks have "finish_reason":null and "usage":null; skip slow parse when they are null!
	if hasNonNullField(trimmed, markerFinishReason) ||
		hasNonNullField(trimmed, markerUsage) ||
		hasNonNullField(trimmed, markerDetails) ||
		hasNonNullField(trimmed, markerToolCalls) ||
		hasNonNullField(trimmed, markerError) ||
		hasNonNullField(trimmed, markerStopReason) ||
		dialect == LLMDialectTGI && hasNonNullField(trimmed, markerGeneratedText) ||
		bytes.Contains(trimmed, markerDoneTrue) ||
		bytes.Contains(trimmed, markerDoneSpaced) ||
		bytes.Contains(trimmed, markerEvalCount) {
		res.needsSlowParse = true
	}
	return res
}

func findJSONStringField(data []byte, keyCompact, keySpaced []byte) ([]byte, bool) {
	if idx := bytes.Index(data, keyCompact); idx >= 0 {
		return extractJSONQuotedBytes(data[idx+len(keyCompact):])
	}
	if len(keySpaced) > 0 {
		if idx := bytes.Index(data, keySpaced); idx >= 0 {
			return extractJSONQuotedBytes(data[idx+len(keySpaced):])
		}
	}
	return nil, false
}

// extractJSONQuotedBytes returns the slice of data up to the closing unescaped '"'
// without allocating.
func extractJSONQuotedBytes(rest []byte) ([]byte, bool) {
	escaped := false
	for i := 0; i < len(rest); i++ {
		b := rest[i]
		if escaped {
			escaped = false
			continue
		}
		if b == '\\' {
			escaped = true
			continue
		}
		if b == '"' {
			return rest[:i], true
		}
	}
	return nil, false
}

// hasNonNullField returns true if marker appears in data followed by ':' and a
// non-null value.
func hasNonNullField(data []byte, marker []byte) bool {
	search := data
	for {
		idx := bytes.Index(search, marker)
		if idx < 0 {
			return false
		}
		after := search[idx+len(marker):]
		search = after
		// Skip whitespace
		i := 0
		for i < len(after) && (after[i] == ' ' || after[i] == '\t' || after[i] == '\n' || after[i] == '\r') {
			i++
		}
		if i >= len(after) || after[i] != ':' {
			continue
		}
		i++
		for i < len(after) && (after[i] == ' ' || after[i] == '\t' || after[i] == '\n' || after[i] == '\r') {
			i++
		}
		if i+4 <= len(after) && bytes.Equal(after[i:i+4], []byte("null")) {
			continue
		}
		if i+2 <= len(after) && bytes.Equal(after[i:i+2], []byte(`""`)) {
			continue
		}
		return true
	}
}

// appendUnescapedJSONBytes appends the unescaped UTF-8 content of raw (the interior
// of a JSON string literal) to dst without heap allocation when dst has capacity.
func appendUnescapedJSONBytes(dst []byte, raw []byte) []byte {
	if bytes.IndexByte(raw, '\\') < 0 {
		return append(dst, raw...)
	}
	for i := 0; i < len(raw); i++ {
		b := raw[i]
		if b != '\\' || i+1 >= len(raw) {
			dst = append(dst, b)
			continue
		}
		i++
		switch raw[i] {
		case '"', '\\', '/':
			dst = append(dst, raw[i])
		case 'n':
			dst = append(dst, '\n')
		case 'r':
			dst = append(dst, '\r')
		case 't':
			dst = append(dst, '\t')
		case 'b':
			dst = append(dst, '\b')
		case 'f':
			dst = append(dst, '\f')
		default:
			dst = append(dst, '\\', raw[i])
		}
	}
	return dst
}

// isResponsesTarget reports an OpenAI Responses API path: OpenAI
// /v1/responses, Azure /openai/v1/responses or /openai/responses?api-version=.
func isResponsesTarget(targetURL string) bool {
	targetPath := strings.ToLower(targetURL)
	if i := strings.IndexAny(targetPath, "?#"); i >= 0 {
		targetPath = targetPath[:i]
	}
	return strings.HasSuffix(targetPath, "/responses")
}

var (
	keyResponsesType       = []byte(`"type":"response.`)
	keyResponsesTypeSpaced = []byte(`"type": "response.`)
	keyErrorType           = []byte(`"type":"error"`)
	keyErrorTypeSpaced     = []byte(`"type": "error"`)
	keyErrorObject         = []byte(`"error":{`)
	keyErrorObjectSpaced   = []byte(`"error": {`)
	markerGeneratedText    = []byte(`"generated_text"`)
	keyThinking            = []byte(`"thinking":"`)
	keyThinkingSpaced      = []byte(`"thinking": "`)
	keyDelta               = []byte(`"delta":"`)
	keyDeltaSpaced         = []byte(`"delta": "`)
)

// responsesEventKind classifies one OpenAI Responses API stream event: by the
// SSE event name when the server sends one, otherwise by the payload's event
// type. Only event types begin with "response." (nested output items have
// types such as message or function_call), so the payload's field order does
// not matter. It does not allocate.
func responsesEventKind(eventType string, data []byte) string {
	// Only a Responses event name is trusted: a proxy may label every event
	// generically (the SSE default is "message").
	if strings.HasPrefix(eventType, "response.") || eventType == "error" {
		return eventType
	}
	for _, key := range [][]byte{keyResponsesType, keyResponsesTypeSpaced} {
		if idx := bytes.Index(data, key); idx >= 0 {
			start := idx + len(key) - len("response.")
			if val, ok := extractJSONQuotedBytes(data[start:]); ok && len(val) > 0 {
				return unsafe.String(&val[0], len(val))
			}
		}
	}
	// An error event, or a gateway's bare {"error": {...}} payload.
	if bytes.Contains(data, keyErrorType) || bytes.Contains(data, keyErrorTypeSpaced) ||
		bytes.HasPrefix(data, []byte(`{"error"`)) || bytes.HasPrefix(data, []byte(`{ "error"`)) ||
		(bytes.Contains(data, keyErrorObject) || bytes.Contains(data, keyErrorObjectSpaced)) && !bytes.Contains(data, []byte(`"response"`)) {
		return "error"
	}
	return ""
}

// responsesErrorCode renders an error event's code, which a gateway may send
// as a number.
func responsesErrorCode(code any) string {
	switch c := code.(type) {
	case string:
		return c
	case float64:
		return strconv.FormatFloat(c, 'f', -1, 64)
	default:
		return ""
	}
}

// llmResponsesObject is an OpenAI Responses API response: the body of a
// non-streaming call, and the "response" of a stream's terminal event.
type llmResponsesObject struct {
	Model             string                   `json:"model"`
	Status            string                   `json:"status"`
	Output            []llmResponsesOutputItem `json:"output"`
	IncompleteDetails *struct {
		Reason string `json:"reason"`
	} `json:"incomplete_details"`
	Error any `json:"error"`
	Usage *struct {
		InputTokens        int `json:"input_tokens"`
		OutputTokens       int `json:"output_tokens"`
		TotalTokens        int `json:"total_tokens"`
		InputTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"input_tokens_details"`
		OutputTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"output_tokens_details"`
	} `json:"usage"`
}

type llmResponsesOutputItem struct {
	Type    string `json:"type"`
	Content []struct {
		Type    string `json:"type"`
		Text    string `json:"text"`
		Refusal string `json:"refusal"`
	} `json:"content"`
	Summary []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	} `json:"summary"`
	CallID    string `json:"call_id,omitempty"`
	Name      string `json:"name,omitempty"`
	Arguments string `json:"arguments,omitempty"`
}

// llmResponsesEvent is a stream event that needs a full parse: a terminal
// response event or an error event.
type llmResponsesEvent struct {
	Type     string              `json:"type"`
	Code     any                 `json:"code"` // a string; some gateways send a number
	Message  string              `json:"message"`
	Error    any                 `json:"error"` // an error event may nest {code, message}
	Response *llmResponsesObject `json:"response"`
}

// failureCode names a terminal status that is not a usable completion: a
// failed or cancelled response, or one left incomplete for a reason other
// than its output limit or a content filter (which the finish reason already
// carries). It is empty for a completed response.
func (r *llmResponsesObject) failureCode() string {
	switch r.Status {
	case "failed":
		return "response_failed"
	case "cancelled":
		return "response_cancelled"
	case "incomplete":
		if r.IncompleteDetails != nil && (r.IncompleteDetails.Reason == "max_output_tokens" || r.IncompleteDetails.Reason == "content_filter") {
			return ""
		}
		return "response_incomplete"
	default:
		return ""
	}
}

// finishReason maps a response's status to the chat finish-reason vocabulary
// the rest of the LLM accounting uses.
func (r *llmResponsesObject) finishReason() string {
	switch r.Status {
	case "completed":
		for _, item := range r.Output {
			if item.Type == "function_call" {
				return "tool_calls"
			}
		}
		return "stop"
	case "incomplete":
		reason := ""
		if r.IncompleteDetails != nil {
			reason = r.IncompleteDetails.Reason
		}
		switch reason {
		case "max_output_tokens":
			return "length"
		case "":
			return "incomplete"
		default:
			return reason
		}
	case "":
		return ""
	default:
		return r.Status
	}
}

// toolCalls returns the response's function calls and their JSON.
func (r *llmResponsesObject) toolCalls() (int, string) {
	var calls []llmResponsesOutputItem
	for _, item := range r.Output {
		if item.Type == "function_call" {
			calls = append(calls, llmResponsesOutputItem{Type: item.Type, CallID: item.CallID, Name: item.Name, Arguments: item.Arguments})
		}
	}
	if len(calls) == 0 {
		return 0, ""
	}
	b, err := json.Marshal(calls)
	if err != nil || len(b) > maxSavedAssistantTextBytes {
		return len(calls), ""
	}
	return len(calls), string(b)
}

// reasoningText is the raw reasoning of the response's reasoning items: their
// content, typed reasoning_text (or output_text, as LiteLLM's chat bridge
// types it), not a summary, which is not the reasoning tokens.
func (r *llmResponsesObject) reasoningText() string {
	var sb strings.Builder
	for _, item := range r.Output {
		if item.Type != "reasoning" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "reasoning_text" || part.Type == "output_text" {
				sb.WriteString(part.Text)
			}
		}
	}
	return sb.String()
}

// reasoningSummaryText is the reasoning items' summary text. Ollama's
// /v1/responses returns a thinking model's whole reasoning this way, with a
// reasoning count of zero.
func (r *llmResponsesObject) reasoningSummaryText() string {
	var sb strings.Builder
	for _, item := range r.Output {
		if item.Type != "reasoning" {
			continue
		}
		for _, part := range item.Summary {
			if part.Type == "summary_text" {
				sb.WriteString(part.Text)
			}
		}
	}
	return sb.String()
}

// outputText concatenates the response's visible output text.
func (r *llmResponsesObject) outputText() string {
	var sb strings.Builder
	for _, item := range r.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			switch part.Type {
			case "output_text":
				sb.WriteString(part.Text)
			case "refusal":
				sb.WriteString(part.Refusal)
			}
		}
	}
	return sb.String()
}

// llmOllamaOutput is an Ollama response's assistant output: message.content
// or response, its thinking, and message.tool_calls.
type llmOllamaOutput struct {
	Content   string
	Thinking  string
	ToolCalls []map[string]any
}

// ollamaOutput decodes the envelope's Ollama output, if it has one.
func (env *llmSlowChunkEnvelope) ollamaOutput() (llmOllamaOutput, bool) {
	var out llmOllamaOutput
	found := false
	if raw := bytes.TrimSpace(env.OllamaMessage); len(raw) > 0 && raw[0] == '{' {
		var msg struct {
			Content   string          `json:"content"`
			Thinking  string          `json:"thinking"`
			ToolCalls llmToolCallList `json:"tool_calls"`
		}
		if json.Unmarshal(raw, &msg) == nil {
			out.Content, out.Thinking, out.ToolCalls = msg.Content, msg.Thinking, msg.ToolCalls
			found = true
		}
	}
	if raw := bytes.TrimSpace(env.OllamaResponse); len(raw) > 0 && raw[0] == '"' {
		var text string
		if json.Unmarshal(raw, &text) == nil {
			if out.Content == "" {
				out.Content = text
			}
			found = true
		}
	}
	if env.Thinking != "" && out.Thinking == "" {
		out.Thinking = env.Thinking
		found = true
	}
	return out, found
}

// llmToolCallList decodes tool_calls as an array, or as one object as older
// TGI streams send it; any other shape is ignored rather than failing the
// whole chunk.
type llmToolCallList []map[string]any

func (l *llmToolCallList) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	switch {
	case len(data) == 0:
	case data[0] == '[':
		var calls []map[string]any
		if json.Unmarshal(data, &calls) == nil {
			*l = calls
		}
	case data[0] == '{':
		var call map[string]any
		if json.Unmarshal(data, &call) == nil {
			*l = llmToolCallList{call}
		}
	}
	return nil
}

// llmSlowChunkEnvelope is used only on the terminal/metadata chunk (1-2 per stream).
type llmSlowChunkEnvelope struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Text         string `json:"text"`
		Delta        struct {
			Content          string          `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			Reasoning        string          `json:"reasoning"`
			ToolCalls        llmToolCallList `json:"tool_calls"`
		} `json:"delta"`
		Message struct {
			Content          string          `json:"content"`
			ReasoningContent string          `json:"reasoning_content"`
			Reasoning        string          `json:"reasoning"`
			ToolCalls        llmToolCallList `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage *struct {
		PromptTokens            int `json:"prompt_tokens"`
		CompletionTokens        int `json:"completion_tokens"`
		TotalTokens             int `json:"total_tokens"`
		CompletionTokensDetails *struct {
			ReasoningTokens int `json:"reasoning_tokens"`
		} `json:"completion_tokens_details"`
		PromptTokensDetails *struct {
			CachedTokens int `json:"cached_tokens"`
		} `json:"prompt_tokens_details"`
	} `json:"usage"`
	// TGI
	GeneratedText string `json:"generated_text"`
	Details       *struct {
		FinishReason    string `json:"finish_reason"`
		GeneratedTokens int    `json:"generated_tokens"`
		InputLength     int    `json:"input_length"`
		Prefill         []any  `json:"prefill"`
	} `json:"details"`
	// Ollama
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
	// Ollama's top-level message (/api/chat) and response and thinking
	// (/api/generate). Raw, so another server's string "message" or object
	// "response" cannot fail the whole parse.
	OllamaMessage  json.RawMessage `json:"message"`
	OllamaResponse json.RawMessage `json:"response"`
	Thinking       string          `json:"thinking"`
	// Triton
	ModelName    string `json:"model_name"`
	TextOutput   string `json:"text_output"`
	FinishReason string `json:"finish_reason"`
	StopReason   string `json:"stop_reason"`
	// Error
	Error any `json:"error"`
}

func extractLLMErrorDetails(raw any) (code string, msg string) {
	switch typed := raw.(type) {
	case string:
		trimmed := strings.TrimSpace(typed)
		if strings.Contains(strings.ToLower(trimmed), "context_length") || strings.Contains(strings.ToLower(trimmed), "maximum context length") {
			return "context_length_exceeded", trimmed
		}
		return "error", trimmed
	case map[string]any:
		c, _ := typed["code"].(string)
		t, _ := typed["type"].(string)
		m, _ := typed["message"].(string)
		if c == "" {
			c = t
		}
		lowerMsg := strings.ToLower(m)
		if c == "" && (strings.Contains(lowerMsg, "context_length") || strings.Contains(lowerMsg, "maximum context length")) {
			c = "context_length_exceeded"
		}
		if c == "" && strings.Contains(lowerMsg, "content_filter") {
			c = "content_filter"
		}
		if c == "" {
			c = "error"
		}
		return c, m
	default:
		if raw != nil {
			b, _ := json.Marshal(raw)
			return "error", string(b)
		}
		return "", ""
	}
}
