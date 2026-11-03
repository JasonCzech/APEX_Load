package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"
	"unsafe"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const (
	defaultLLMStreamTimeout    = 300 * time.Second
	maxLLMRetainedRawBodyBytes = 64 * 1024
	maxSavedAssistantTextBytes = 16 * 1024

	fnvOffset64 = uint64(14695981039346656037)
	fnvPrime64  = uint64(1099511628211)
)

var llmITLDistributionPool = sync.Pool{
	New: func() any {
		return &protocol.DiagnosticDistribution{}
	},
}

func acquireITLDistribution() *protocol.DiagnosticDistribution {
	dist := llmITLDistributionPool.Get().(*protocol.DiagnosticDistribution)
	*dist = protocol.DiagnosticDistribution{}
	return dist
}

func releaseITLDistribution(dist *protocol.DiagnosticDistribution) {
	if dist == nil {
		return
	}
	llmITLDistributionPool.Put(dist)
}

func isLLMAction(action *protocol.Action) bool {
	if action == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(action.Type), "llm_request") {
		return true
	}
	if action.Params == nil {
		return false
	}
	if enabled, ok := boolParam(action.Params["llm"]); ok && enabled {
		return true
	}
	if _, hasDialect := action.Params["llm_dialect"]; hasDialect {
		return true
	}
	if _, hasProfile := action.Params["stream_profile"]; hasProfile {
		return true
	}
	return false
}

type llmStreamObserver struct {
	dialect      string
	requestStart time.Time
	deltaPath    string
	finishPath   string
	usagePath    string

	firstTokenAt        time.Time
	firstVisibleTokenAt time.Time
	lastTokenAt         time.Time
	tokenChunkCount     int
	visibleChunkCount   int
	reasoningChunkCount int
	itlDist             *protocol.DiagnosticDistribution

	outputHash  uint64
	outputChars int
	captureText bool
	savedText   []byte
	scratchBuf  [256]byte

	model            string
	finishReason     string
	promptTokens     int
	completionTokens int
	reasoningTokens  int
	cachedTokens     int
	totalTokens      int
	hasServerUsage   bool
	toolCallsCount   int
	toolCallsJSON    string
	errorCode        string
	errorMessage     string
	http200Error     bool
}

func newLLMStreamObserver(dialect string, requestStart time.Time, captureText bool) *llmStreamObserver {
	obs := &llmStreamObserver{
		dialect:      dialect,
		requestStart: requestStart,
		itlDist:      acquireITLDistribution(),
		outputHash:   fnvOffset64,
		captureText:  captureText,
	}
	if captureText {
		obs.savedText = make([]byte, 0, 1024)
	}
	return obs
}

func (o *llmStreamObserver) close() {
	if o != nil && o.itlDist != nil {
		releaseITLDistribution(o.itlDist)
		o.itlDist = nil
	}
}

func (o *llmStreamObserver) observeTokenBytes(raw []byte, visible bool, now time.Time) {
	if len(raw) == 0 {
		return
	}
	if o.tokenChunkCount == 0 {
		o.firstTokenAt = now
	} else if !o.lastTokenAt.IsZero() && o.itlDist != nil {
		deltaMs := float64(now.Sub(o.lastTokenAt)) / float64(time.Millisecond)
		if deltaMs >= 0 {
			o.itlDist.Add(deltaMs)
		}
	}
	o.lastTokenAt = now
	o.tokenChunkCount++

	if visible {
		if o.visibleChunkCount == 0 {
			o.firstVisibleTokenAt = now
		}
		o.visibleChunkCount++
		decoded := appendUnescapedJSONBytes(o.scratchBuf[:0], raw)
		for i := 0; i < len(decoded); i++ {
			o.outputHash ^= uint64(decoded[i])
			o.outputHash *= fnvPrime64
		}
		o.outputChars += len(decoded)
		if o.captureText && len(o.savedText) < maxSavedAssistantTextBytes {
			rem := maxSavedAssistantTextBytes - len(o.savedText)
			if len(decoded) > rem {
				decoded = decoded[:rem]
			}
			o.savedText = append(o.savedText, decoded...)
		}
	} else {
		o.reasoningChunkCount++
	}
}

// observeChunk processes a single SSE or NDJSON data payload.
// Normal token chunks take the zero-allocation fast path.
func (o *llmStreamObserver) observeChunk(eventType string, data []byte, now time.Time) {
	if o == nil || len(data) == 0 {
		return
	}
	if o.deltaPath != "" || o.finishPath != "" || o.usagePath != "" {
		o.observeCustomProfileChunk(data, now)
		return
	}
	scan := scanLLMChunkFast(o.dialect, data)
	if o.model == "" && len(scan.modelRaw) > 0 && len(scan.modelRaw) <= 128 {
		o.model = string(scan.modelRaw)
	}
	if scan.hasReasoningToken {
		o.observeTokenBytes(scan.reasoningRaw, false, now)
	}
	if scan.hasVisibleToken {
		o.observeTokenBytes(scan.visibleRaw, true, now)
	}
	if scan.needsSlowParse {
		o.parseSlowChunk(data)
	}
}

func (o *llmStreamObserver) parseSlowChunk(data []byte) {
	trimmed := bytes.TrimSpace(data)
	if o.dialect == LLMDialectVercelAI && len(trimmed) >= 2 && trimmed[1] == ':' {
		prefix := trimmed[0]
		payload := trimmed[2:]
		if prefix == '3' {
			o.http200Error = true
			o.errorCode = "stream_error"
			o.errorMessage = strings.Trim(string(payload), `"`)
			return
		}
		var vMeta struct {
			FinishReason string `json:"finishReason"`
			Usage        *struct {
				PromptTokens     int `json:"promptTokens"`
				CompletionTokens int `json:"completionTokens"`
			} `json:"usage"`
		}
		if json.Unmarshal(payload, &vMeta) == nil {
			if vMeta.FinishReason != "" {
				o.finishReason = vMeta.FinishReason
			}
			if vMeta.Usage != nil && (vMeta.Usage.PromptTokens > 0 || vMeta.Usage.CompletionTokens > 0) {
				o.promptTokens = vMeta.Usage.PromptTokens
				o.completionTokens = vMeta.Usage.CompletionTokens
				o.totalTokens = vMeta.Usage.PromptTokens + vMeta.Usage.CompletionTokens
				o.hasServerUsage = true
			}
		}
		return
	}

	var env llmSlowChunkEnvelope
	if err := json.Unmarshal(trimmed, &env); err != nil {
		return
	}
	if o.model == "" {
		if env.Model != "" {
			o.model = env.Model
		} else if env.ModelName != "" {
			o.model = env.ModelName
		}
	}
	if env.Error != nil {
		code, msg := extractLLMErrorDetails(env.Error)
		if code != "" {
			o.http200Error = true
			o.errorCode = code
			o.errorMessage = msg
		}
	}
	if len(env.Choices) > 0 {
		ch := env.Choices[0]
		if ch.FinishReason != "" {
			o.finishReason = ch.FinishReason
		}
		tc := ch.Delta.ToolCalls
		if len(tc) == 0 {
			tc = ch.Message.ToolCalls
		}
		if len(tc) > 0 {
			o.toolCallsCount += len(tc)
			if o.captureText && o.toolCallsJSON == "" {
				if b, err := json.Marshal(tc); err == nil && len(b) <= maxSavedAssistantTextBytes {
					o.toolCallsJSON = string(b)
				}
			}
		}
	}
	if env.Usage != nil && (env.Usage.PromptTokens > 0 || env.Usage.CompletionTokens > 0 || env.Usage.TotalTokens > 0) {
		o.promptTokens = env.Usage.PromptTokens
		o.completionTokens = env.Usage.CompletionTokens
		o.totalTokens = env.Usage.TotalTokens
		if o.totalTokens == 0 {
			o.totalTokens = o.promptTokens + o.completionTokens
		}
		if env.Usage.CompletionTokensDetails != nil && env.Usage.CompletionTokensDetails.ReasoningTokens > 0 {
			o.reasoningTokens = env.Usage.CompletionTokensDetails.ReasoningTokens
		}
		if env.Usage.PromptTokensDetails != nil && env.Usage.PromptTokensDetails.CachedTokens > 0 {
			o.cachedTokens = env.Usage.PromptTokensDetails.CachedTokens
		}
		o.hasServerUsage = true
	}
	if env.Details != nil {
		if env.Details.FinishReason != "" {
			o.finishReason = env.Details.FinishReason
		}
		if env.Details.GeneratedTokens > 0 {
			o.completionTokens = env.Details.GeneratedTokens
			if len(env.Details.Prefill) > 0 {
				o.promptTokens = len(env.Details.Prefill)
			}
			o.totalTokens = o.promptTokens + o.completionTokens
			o.hasServerUsage = true
		}
	}
	if env.Done {
		if env.DoneReason != "" && o.finishReason == "" {
			o.finishReason = env.DoneReason
		} else if o.finishReason == "" {
			o.finishReason = "stop"
		}
		if env.PromptEvalCount > 0 || env.EvalCount > 0 {
			o.promptTokens = env.PromptEvalCount
			o.completionTokens = env.EvalCount
			o.totalTokens = o.promptTokens + o.completionTokens
			o.hasServerUsage = true
		}
	}
	if env.FinishReason != "" && o.finishReason == "" {
		o.finishReason = env.FinishReason
	}
	if env.StopReason != "" && o.finishReason == "" {
		o.finishReason = env.StopReason
	}
}

func (o *llmStreamObserver) observeCustomProfileChunk(data []byte, now time.Time) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("[DONE]")) {
		return
	}
	var root map[string]any
	if err := json.Unmarshal(trimmed, &root); err != nil {
		return
	}
	if o.deltaPath != "" {
		if val := lookupDottedPathString(root, o.deltaPath); val != "" {
			o.observeTokenBytes([]byte(val), true, now)
		}
	}
	if o.finishPath != "" {
		if val := lookupDottedPathString(root, o.finishPath); val != "" {
			o.finishReason = val
		}
	}
	if o.usagePath != "" {
		if uMap, ok := lookupDottedPathValue(root, o.usagePath).(map[string]any); ok {
			pToks, _ := intParam(uMap["prompt_tokens"])
			if pToks == 0 {
				pToks, _ = intParam(uMap["input_tokens"])
			}
			cToks, _ := intParam(uMap["completion_tokens"])
			if cToks == 0 {
				cToks, _ = intParam(uMap["output_tokens"])
			}
			tToks, _ := intParam(uMap["total_tokens"])
			if pToks > 0 || cToks > 0 || tToks > 0 {
				o.promptTokens = pToks
				o.completionTokens = cToks
				if tToks > 0 {
					o.totalTokens = tToks
				} else {
					o.totalTokens = pToks + cToks
				}
				o.hasServerUsage = true
			}
		}
	}
}

func lookupDottedPathValue(root map[string]any, path string) any {
	clean := strings.TrimSpace(path)
	clean = strings.TrimPrefix(clean, "$.")
	clean = strings.TrimPrefix(clean, "$")
	clean = strings.TrimPrefix(clean, ".")
	if clean == "" {
		return nil
	}
	parts := strings.Split(clean, ".")
	var cur any = root
	for _, p := range parts {
		m, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur, ok = m[p]
		if !ok {
			return nil
		}
	}
	return cur
}

func lookupDottedPathString(root map[string]any, path string) string {
	val, _ := lookupDottedPathValue(root, path).(string)
	return val
}

func (o *llmStreamObserver) onSSEEvent(event sseEvent, now time.Time) (bool, error) {
	if len(event.Data) == 0 {
		return false, nil
	}
	data := unsafe.Slice(unsafe.StringData(event.Data), len(event.Data))
	o.observeChunk(event.Event, data, now)
	return false, nil
}

func readLLMStreamResponse(
	ctx context.Context,
	body io.ReadCloser,
	contentType string,
	params map[string]any,
	targetURL string,
	requestStart time.Time,
	lease *responseCaptureLease,
) (sseReadResult, *llmStreamObserver, error) {
	dialect := resolveActionLLMDialect(params, targetURL)
	needText := shouldCaptureLLMText(params)
	obs := newLLMStreamObserver(dialect, requestStart, needText)
	if params != nil {
		if profMap, ok := params["stream_profile"].(map[string]any); ok {
			obs.deltaPath, _ = profMap["delta_path"].(string)
			obs.finishPath, _ = profMap["finish_path"].(string)
			obs.usagePath, _ = profMap["usage_path"].(string)
		}
	}

	// Default stop_on_data to [DONE] for OpenAI/Azure dialects unless caller specified otherwise.
	if params == nil {
		params = map[string]any{}
	}
	effectiveParams := params
	if _, hasStopData := params["stop_on_data"]; !hasStopData {
		if _, hasStopEvent := params["stop_on_event"]; !hasStopEvent {
			if dialect == LLMDialectOpenAIChat || dialect == LLMDialectOpenAICompletions || dialect == LLMDialectAzureOpenAI {
				cloned := make(map[string]any, len(params)+1)
				for k, v := range params {
					cloned[k] = v
				}
				cloned["stop_on_data"] = "[DONE]"
				effectiveParams = cloned
			}
		}
	}

	opts, err := resolveSSEReadOptions(effectiveParams, defaultLLMStreamTimeout)
	if err != nil {
		return sseReadResult{EndReason: EndReasonError}, obs, err
	}
	opts.MaxRetainedBytes = maxLLMRetainedRawBodyBytes
	opts.OnEvent = obs.onSSEEvent

	if shouldReadNDJSON(contentType, effectiveParams) || dialect == LLMDialectOllama {
		res, readErr := readNDJSONResponseWithOptions(ctx, body, opts, requestStart, lease)
		return res, obs, readErr
	}
	res, readErr := readSSEResponseWithOptions(ctx, body, opts, requestStart, lease)
	return res, obs, readErr
}

func resolveActionLLMDialect(params map[string]any, targetURL string) string {
	if params != nil {
		if profile, ok := params["stream_profile"].(string); ok && strings.TrimSpace(profile) != "" {
			return normalizeLLMDialect(profile, targetURL)
		}
		if profMap, ok := params["stream_profile"].(map[string]any); ok {
			if preset, ok := profMap["preset"].(string); ok && strings.TrimSpace(preset) != "" {
				return normalizeLLMDialect(preset, targetURL)
			}
			return "custom_profile"
		}
		if d, ok := params["llm_dialect"].(string); ok && strings.TrimSpace(d) != "" {
			return normalizeLLMDialect(d, targetURL)
		}
		if d, ok := params["dialect"].(string); ok && strings.TrimSpace(d) != "" {
			return normalizeLLMDialect(d, targetURL)
		}
	}
	return normalizeLLMDialect("", targetURL)
}

func shouldCaptureLLMText(params map[string]any) bool {
	if params == nil {
		return false
	}
	for _, key := range []string{"save_as", "save_text_as", "save_assistant_text", "save_response_as", "response_var", "save_tool_calls_as", "save_tool_calls_var", "tool_calls_var", "conversation", "conversation_var", "history_var", "json_schema"} {
		if v, ok := params[key]; ok && v != nil {
			if b, isBool := v.(bool); isBool {
				if b {
					return true
				}
				continue
			}
			if s, isStr := v.(string); !isStr || strings.TrimSpace(s) != "" {
				return true
			}
		}
	}
	return false
}

func finalizeStreamLLMDiagnostics(
	obs *llmStreamObserver,
	sseRes sseReadResult,
	streamErr error,
	requestBody []byte,
	respHeaders http.Header,
	statusCode int,
	duration time.Duration,
	params map[string]any,
	result *protocol.Result,
) *protocol.LLMDiagnostics {
	if obs == nil {
		return nil
	}
	e2eMs := durationToNonNegativeMilliseconds(duration)
	diag := &protocol.LLMDiagnostics{
		Dialect:        obs.dialect,
		Model:          obs.model,
		Stream:         true,
		E2EMs:          e2eMs,
		FinishReason:   obs.finishReason,
		ErrorCode:      obs.errorCode,
		HTTP200Error:   obs.http200Error,
		ToolCallsCount: obs.toolCallsCount,
		OutputHash:     obs.outputHash,
		OutputChars:    obs.outputChars,
		Throttled:      statusCode == http.StatusTooManyRequests,
	}
	if obs.outputChars == 0 {
		diag.OutputHash = 0
	}
	if !obs.firstTokenAt.IsZero() {
		diag.TTFTMs = durationToNonNegativeMilliseconds(obs.firstTokenAt.Sub(obs.requestStart))
	}
	if !obs.firstVisibleTokenAt.IsZero() {
		diag.FirstVisibleTokenMs = durationToNonNegativeMilliseconds(obs.firstVisibleTokenAt.Sub(obs.requestStart))
	}
	if obs.itlDist != nil && obs.itlDist.Count > 0 {
		distCopy := *obs.itlDist
		diag.ITLDistribution = &distCopy
	}
	if diag.Model == "" {
		diag.Model = extractModelFromRequest(requestBody, params)
	}

	if obs.hasServerUsage {
		diag.PromptTokens = obs.promptTokens
		diag.CompletionTokens = obs.completionTokens
		diag.ReasoningTokens = obs.reasoningTokens
		diag.CachedTokens = obs.cachedTokens
		diag.TotalTokens = obs.totalTokens
	} else {
		diag.PromptTokens = protocol.EstimateTokenCount(string(requestBody))
		if obs.outputChars > 0 {
			diag.CompletionTokens = max(1, (obs.outputChars+3)/4)
		} else if obs.visibleChunkCount > 0 {
			diag.CompletionTokens = obs.visibleChunkCount
		}
		if obs.reasoningChunkCount > 0 && diag.ReasoningTokens == 0 {
			diag.ReasoningTokens = obs.reasoningChunkCount
			diag.CompletionTokens += obs.reasoningChunkCount
		}
		diag.TotalTokens = diag.PromptTokens + diag.CompletionTokens
		diag.TokensEstimated = true
	}

	if diag.CompletionTokens > 1 {
		if !obs.firstTokenAt.IsZero() && !obs.lastTokenAt.IsZero() && obs.lastTokenAt.After(obs.firstTokenAt) {
			decodeSpanMs := durationToNonNegativeMilliseconds(obs.lastTokenAt.Sub(obs.firstTokenAt))
			diag.TPOTMs = decodeSpanMs / float64(diag.CompletionTokens-1)
		} else if diag.E2EMs > diag.TTFTMs && diag.TTFTMs > 0 {
			diag.TPOTMs = (diag.E2EMs - diag.TTFTMs) / float64(diag.CompletionTokens-1)
		}
	}

	diag.InputLengthBand = protocol.ClassifyLLMInputBand(diag.PromptTokens)
	if isTruncatedFinishReason(diag.FinishReason) {
		diag.Truncated = true
	} else if sseRes.EndReason == EndReasonTimeout {
		diag.Truncated = true
		if diag.ErrorCode == "" {
			diag.ErrorCode = "stream_timeout"
		}
	} else if sseRes.EndReason == EndReasonEOF && diag.FinishReason == "" && statusCode >= 200 && statusCode < 300 && dialectRequiresTerminalMarker(obs.dialect) {
		diag.Truncated = true
		if diag.ErrorCode == "" {
			diag.ErrorCode = "stream_truncated"
		}
	}
	if streamErr != nil && !diag.Truncated {
		if strings.Contains(streamErr.Error(), "stream_truncated") {
			diag.Truncated = true
			if diag.ErrorCode == "" {
				diag.ErrorCode = "stream_truncated"
			}
		} else if strings.Contains(streamErr.Error(), "stream_timeout") {
			diag.Truncated = true
			if diag.ErrorCode == "" {
				diag.ErrorCode = "stream_timeout"
			}
		}
	}

	applyLLMResponseHeaders(diag, respHeaders)
	stampLLMResultDimensionsAndExtractions(diag, string(obs.savedText), obs.toolCallsJSON, obs.errorMessage, statusCode, streamErr == nil, params, result)
	return diag
}

func dialectRequiresTerminalMarker(dialect string) bool {
	switch dialect {
	case LLMDialectOpenAIChat, LLMDialectOpenAICompletions, LLMDialectAzureOpenAI, LLMDialectTGI, LLMDialectOllama, LLMDialectTriton, LLMDialectVercelAI:
		return true
	default:
		return false
	}
}

func analyzeNonStreamingLLMResponse(
	dialect string,
	requestBody []byte,
	respBody []byte,
	respHeaders http.Header,
	statusCode int,
	duration time.Duration,
	params map[string]any,
	result *protocol.Result,
) *protocol.LLMDiagnostics {
	e2eMs := durationToNonNegativeMilliseconds(duration)
	diag := &protocol.LLMDiagnostics{
		Dialect:   dialect,
		Stream:    false,
		TTFTMs:    e2eMs,
		E2EMs:     e2eMs,
		Throttled: statusCode == http.StatusTooManyRequests,
	}

	var env llmSlowChunkEnvelope
	var assistantText string
	var toolCallsJSON string
	var errMsg string
	if len(bytes.TrimSpace(respBody)) > 0 && json.Unmarshal(respBody, &env) == nil {
		diag.Model = env.Model
		if env.Error != nil {
			code, msg := extractLLMErrorDetails(env.Error)
			diag.ErrorCode = code
			errMsg = msg
			if statusCode >= 200 && statusCode < 300 {
				diag.HTTP200Error = true
			}
		}
		if len(env.Choices) > 0 {
			ch := env.Choices[0]
			diag.FinishReason = ch.FinishReason
			assistantText = ch.Message.Content
			if assistantText == "" {
				assistantText = ch.Text
			}
			if len(ch.Message.ToolCalls) > 0 {
				diag.ToolCallsCount = len(ch.Message.ToolCalls)
				if b, err := json.Marshal(ch.Message.ToolCalls); err == nil && len(b) <= maxSavedAssistantTextBytes {
					toolCallsJSON = string(b)
				}
			}
		}
		if assistantText == "" {
			if env.GeneratedText != "" {
				assistantText = env.GeneratedText
			} else if env.TextOutput != "" {
				assistantText = env.TextOutput
			}
		}
		if env.Details != nil {
			if diag.FinishReason == "" {
				diag.FinishReason = env.Details.FinishReason
			}
			if env.Details.GeneratedTokens > 0 {
				diag.CompletionTokens = env.Details.GeneratedTokens
				diag.PromptTokens = len(env.Details.Prefill)
				diag.TotalTokens = diag.PromptTokens + diag.CompletionTokens
			}
		}
		if env.Usage != nil && (env.Usage.PromptTokens > 0 || env.Usage.CompletionTokens > 0 || env.Usage.TotalTokens > 0) {
			diag.PromptTokens = env.Usage.PromptTokens
			diag.CompletionTokens = env.Usage.CompletionTokens
			diag.TotalTokens = env.Usage.TotalTokens
			if diag.TotalTokens == 0 {
				diag.TotalTokens = diag.PromptTokens + diag.CompletionTokens
			}
			if env.Usage.CompletionTokensDetails != nil {
				diag.ReasoningTokens = env.Usage.CompletionTokensDetails.ReasoningTokens
			}
			if env.Usage.PromptTokensDetails != nil {
				diag.CachedTokens = env.Usage.PromptTokensDetails.CachedTokens
			}
		} else if env.PromptEvalCount > 0 || env.EvalCount > 0 {
			diag.PromptTokens = env.PromptEvalCount
			diag.CompletionTokens = env.EvalCount
			diag.TotalTokens = diag.PromptTokens + diag.CompletionTokens
		}
		if env.FinishReason != "" && diag.FinishReason == "" {
			diag.FinishReason = env.FinishReason
		}
		if env.StopReason != "" && diag.FinishReason == "" {
			diag.FinishReason = env.StopReason
		}
	}

	if diag.Model == "" {
		diag.Model = extractModelFromRequest(requestBody, params)
	}
	if len(assistantText) > 0 {
		diag.FirstVisibleTokenMs = e2eMs
		diag.OutputChars = len(assistantText)
		h := fnvOffset64
		for i := 0; i < len(assistantText); i++ {
			h ^= uint64(assistantText[i])
			h *= fnvPrime64
		}
		diag.OutputHash = h
	}
	if diag.PromptTokens == 0 && diag.CompletionTokens == 0 {
		diag.PromptTokens = protocol.EstimateTokenCount(string(requestBody))
		diag.CompletionTokens = protocol.EstimateTokenCount(assistantText)
		diag.TotalTokens = diag.PromptTokens + diag.CompletionTokens
		diag.TokensEstimated = true
	}
	diag.InputLengthBand = protocol.ClassifyLLMInputBand(diag.PromptTokens)
	if isTruncatedFinishReason(diag.FinishReason) {
		diag.Truncated = true
	}
	applyLLMResponseHeaders(diag, respHeaders)
	if len(assistantText) > maxSavedAssistantTextBytes {
		assistantText = assistantText[:maxSavedAssistantTextBytes]
	}
	stampLLMResultDimensionsAndExtractions(diag, assistantText, toolCallsJSON, errMsg, statusCode, true, params, result)
	return diag
}

func isTruncatedFinishReason(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "model_length":
		return true
	default:
		return false
	}
}

func extractModelFromRequest(requestBody []byte, params map[string]any) string {
	if params != nil {
		if m, ok := params["model"].(string); ok && strings.TrimSpace(m) != "" && len(m) <= 128 {
			return strings.TrimSpace(m)
		}
	}
	if val, ok := findJSONStringField(requestBody, keyModel, keyModelSpaced); ok && len(val) > 0 && len(val) <= 128 {
		return string(val)
	}
	return ""
}

func stampLLMResultDimensionsAndExtractions(
	diag *protocol.LLMDiagnostics,
	assistantText string,
	toolCallsJSON string,
	errorMsg string,
	statusCode int,
	transportOK bool,
	params map[string]any,
	result *protocol.Result,
) {
	if result == nil || diag == nil {
		return
	}
	if diag.InputLengthBand != "" {
		if result.PropertyDimensions == nil {
			result.PropertyDimensions = make(map[string]string, 1)
		}
		if len(result.PropertyDimensions) < 4 {
			result.PropertyDimensions["llm_input_band"] = diag.InputLengthBand
		}
	}
	if params != nil {
		var goldenID string
		for _, k := range []string{"golden_prompt_id", "golden_prompt"} {
			switch raw := params[k].(type) {
			case string:
				if s := strings.TrimSpace(raw); s != "" && !strings.EqualFold(s, "false") {
					goldenID = s
				}
			case bool:
				if raw {
					goldenID = "true"
				}
			}
			if goldenID != "" {
				break
			}
		}
		if goldenID != "" {
			if len(goldenID) > 128 {
				goldenID = goldenID[:128]
			}
			if diag.GoldenPromptID == "" {
				diag.GoldenPromptID = goldenID
			}
			if result.PropertyDimensions == nil {
				result.PropertyDimensions = make(map[string]string, 2)
			}
			if len(result.PropertyDimensions) < 4 {
				result.PropertyDimensions["golden_prompt"] = goldenID
			}
		}
		var saveVar string
		for _, k := range []string{"save_as", "save_text_as", "save_assistant_text", "save_response_as", "response_var"} {
			if s, ok := params[k].(string); ok && strings.TrimSpace(s) != "" {
				saveVar = strings.TrimSpace(s)
				break
			}
		}
		if saveVar != "" {
			if result.Extractions == nil {
				result.Extractions = make(map[string]string)
			}
			result.Extractions[saveVar] = assistantText
		}
		var saveReasonVar string
		for _, k := range []string{"save_finish_reason_var", "save_finish_reason_as", "finish_reason_var"} {
			if s, ok := params[k].(string); ok && strings.TrimSpace(s) != "" {
				saveReasonVar = strings.TrimSpace(s)
				break
			}
		}
		if saveReasonVar != "" {
			if result.Extractions == nil {
				result.Extractions = make(map[string]string)
			}
			result.Extractions[saveReasonVar] = diag.FinishReason
		}
		var saveToolsVar string
		for _, k := range []string{"save_tool_calls_as", "save_tool_calls_var", "tool_calls_var"} {
			if s, ok := params[k].(string); ok && strings.TrimSpace(s) != "" {
				saveToolsVar = strings.TrimSpace(s)
				break
			}
		}
		if saveToolsVar != "" && toolCallsJSON != "" {
			if result.Extractions == nil {
				result.Extractions = make(map[string]string)
			}
			result.Extractions[saveToolsVar] = toolCallsJSON
		}
		if (params["conversation"] != nil && params["conversation"] != false) || params["conversation_var"] != nil || params["history_var"] != nil {
			if result.ProtocolEvidence == nil {
				result.ProtocolEvidence = make(map[string]any, 1)
			}
			result.ProtocolEvidence["__llm_assistant_text"] = assistantText
		}
	}

	// Deterministic quality checks on HTTP 2xx responses:
	if statusCode >= 200 && statusCode < 300 && transportOK {
		if diag.HTTP200Error {
			result.Success = false
			if errorMsg != "" {
				result.ErrorMessage = fmt.Sprintf("llm_stream_error: %s", errorMsg)
			} else {
				result.ErrorMessage = "llm_stream_error"
			}
			return
		}
		if strings.EqualFold(diag.FinishReason, "content_filter") {
			diag.ErrorCode = "content_filter"
			result.Success = false
			result.ErrorMessage = "llm_content_filter"
			return
		}
		if diag.CompletionTokens == 0 && diag.OutputChars == 0 && diag.ToolCallsCount == 0 {
			diag.EmptyOutput = true
			allowEmpty, _ := boolParam(params["allow_empty_output"])
			if !allowEmpty {
				if diag.ErrorCode == "" {
					diag.ErrorCode = "empty_output"
				}
				result.Success = false
				result.ErrorMessage = "llm_empty_output"
				return
			}
		}
		if diag.Truncated {
			failTrunc := true
			if params != nil {
				if v, ok := boolParam(params["fail_on_truncation"]); ok {
					failTrunc = v
				}
				if ignoreEOS, ok := boolParam(params["ignore_eos"]); ok && ignoreEOS {
					failTrunc = false
				}
			}
			if failTrunc {
				if diag.ErrorCode == "" {
					diag.ErrorCode = "truncated"
				}
				result.Success = false
				if diag.ErrorCode == "stream_timeout" {
					result.ErrorMessage = "llm_stream_timeout"
				} else {
					result.ErrorMessage = "llm_output_truncated"
				}
				return
			}
		}
		if params != nil {
			if schemaRaw, ok := params["json_schema"]; ok && schemaRaw != nil {
				if err := validateLLMJSONSchema(assistantText, schemaRaw); err != nil {
					diag.SchemaFailed = true
					if diag.ErrorCode == "" {
						diag.ErrorCode = "schema_failed"
					}
					result.Success = false
					result.ErrorMessage = fmt.Sprintf("llm_json_schema_failed: %v", err)
					return
				}
			}
		}
	} else if statusCode == http.StatusTooManyRequests {
		diag.Throttled = true
		if diag.ErrorCode == "" {
			diag.ErrorCode = "rate_limit_exceeded"
		}
	}
}

func validateLLMJSONSchema(assistantText string, schemaRaw any) error {
	trimmed := strings.TrimSpace(assistantText)
	if trimmed == "" {
		return fmt.Errorf("empty assistant output")
	}
	var parsed any
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return fmt.Errorf("invalid JSON output: %w", err)
	}
	var schema map[string]any
	switch typed := schemaRaw.(type) {
	case map[string]any:
		schema = typed
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		if err := json.Unmarshal([]byte(typed), &schema); err != nil {
			return fmt.Errorf("invalid json_schema definition: %w", err)
		}
	default:
		return nil
	}
	if inner, ok := schema["schema"].(map[string]any); ok {
		schema = inner
	}
	if reqRaw, ok := schema["required"]; ok {
		obj, isObj := parsed.(map[string]any)
		if !isObj {
			return fmt.Errorf("expected JSON object for required properties")
		}
		switch reqList := reqRaw.(type) {
		case []any:
			for _, item := range reqList {
				if key, ok := item.(string); ok {
					if _, exists := obj[key]; !exists {
						return fmt.Errorf("missing required property %q", key)
					}
				}
			}
		case []string:
			for _, key := range reqList {
				if _, exists := obj[key]; !exists {
					return fmt.Errorf("missing required property %q", key)
				}
			}
		}
	}
	return nil
}
