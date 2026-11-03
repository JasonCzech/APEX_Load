package http

import (
	"bytes"
	"encoding/json"
	"strings"
)

const (
	LLMDialectOpenAIChat        = "openai_chat"
	LLMDialectOpenAICompletions = "openai_completions"
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
	case "openai", "openai_chat", "openai_responses", "chat", "chat_completions", "vllm", "nim":
		return LLMDialectOpenAIChat
	case "openai_completions", "completions":
		return LLMDialectOpenAICompletions
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

// llmSlowChunkEnvelope is used only on the terminal/metadata chunk (1-2 per stream).
type llmSlowChunkEnvelope struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Text         string `json:"text"`
		Delta        struct {
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content"`
			Reasoning        string           `json:"reasoning"`
			ToolCalls        []map[string]any `json:"tool_calls"`
		} `json:"delta"`
		Message struct {
			Content          string           `json:"content"`
			ReasoningContent string           `json:"reasoning_content"`
			ToolCalls        []map[string]any `json:"tool_calls"`
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
		Prefill         []any  `json:"prefill"`
	} `json:"details"`
	// Ollama
	Done            bool   `json:"done"`
	DoneReason      string `json:"done_reason"`
	PromptEvalCount int    `json:"prompt_eval_count"`
	EvalCount       int    `json:"eval_count"`
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
