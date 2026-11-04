package protocol

import "strings"

// LLMDiagnostics carries per-request LLM inference telemetry.
// Only token counts, hashes, lengths, finish reason, and timing distributions
// are retained here — never raw prompt or completion text.
type LLMDiagnostics struct {
	Dialect             string                  `json:"dialect,omitempty"`
	Model               string                  `json:"model,omitempty"`
	Stream              bool                    `json:"stream,omitempty"`
	TTFTMs              float64                 `json:"ttft_ms,omitempty"`
	FirstVisibleTokenMs float64                 `json:"first_visible_token_ms,omitempty"`
	TPOTMs              float64                 `json:"tpot_ms,omitempty"`
	E2EMs               float64                 `json:"e2e_ms,omitempty"`
	ITLDistribution     *DiagnosticDistribution `json:"itl_ms,omitempty"`
	PromptTokens        int                     `json:"prompt_tokens,omitempty"`
	CompletionTokens    int                     `json:"completion_tokens,omitempty"`
	ReasoningTokens     int                     `json:"reasoning_tokens,omitempty"`
	CachedTokens        int                     `json:"cached_tokens,omitempty"`
	TotalTokens         int                     `json:"total_tokens,omitempty"`
	TokensEstimated     bool                    `json:"tokens_estimated,omitempty"`
	// PromptTokensEstimated: only the prompt count is estimated, the
	// completion count being the server's (TokensEstimated is also set).
	PromptTokensEstimated bool `json:"prompt_tokens_estimated,omitempty"`
	// ReasoningTokensEstimated: the reasoning share of the completion is the
	// client's estimate, the server having returned reasoning text without a
	// count of it.
	ReasoningTokensEstimated   bool     `json:"reasoning_tokens_estimated,omitempty"`
	FinishReason               string   `json:"finish_reason,omitempty"`
	Truncated                  bool     `json:"truncated,omitempty"`
	Throttled                  bool     `json:"throttled,omitempty"`
	EmptyOutput                bool     `json:"empty_output,omitempty"`
	ErrorCode                  string   `json:"error_code,omitempty"`
	HTTP200Error               bool     `json:"http_200_error,omitempty"`
	SchemaFailed               bool     `json:"schema_failed,omitempty"`
	ToolCallsCount             int      `json:"tool_calls_count,omitempty"`
	InputLengthBand            string   `json:"input_length_band,omitempty"`
	RateLimitRemainingRequests *int64   `json:"ratelimit_remaining_requests,omitempty"`
	RateLimitRemainingTokens   *int64   `json:"ratelimit_remaining_tokens,omitempty"`
	ServerQueueMs              *float64 `json:"server_queue_ms,omitempty"`
	ServerPrefillMs            *float64 `json:"server_prefill_ms,omitempty"`
	ServerDecodeMs             *float64 `json:"server_decode_ms,omitempty"`
	OutputHash                 uint64   `json:"output_hash,omitempty"`
	OutputChars                int      `json:"output_chars,omitempty"`
	GoldenPromptID             string   `json:"golden_prompt_id,omitempty"`
	Region                     string   `json:"region,omitempty"`
	LLMHopCount                int      `json:"llm_hop_count,omitempty"`
}

func (d *LLMDiagnostics) Valid() bool {
	if d == nil {
		return false
	}
	if len(d.Dialect) > 64 || len(d.Model) > 128 || len(d.FinishReason) > 64 || len(d.ErrorCode) > 64 || len(d.InputLengthBand) > 32 || len(d.GoldenPromptID) > 128 || len(d.Region) > 64 {
		return false
	}
	if d.PromptTokens < 0 || d.CompletionTokens < 0 || d.ReasoningTokens < 0 || d.CachedTokens < 0 || d.TotalTokens < 0 || d.ToolCallsCount < 0 || d.OutputChars < 0 || d.LLMHopCount < 0 {
		return false
	}
	return true
}

func (d *LLMDiagnostics) Clone() *LLMDiagnostics {
	if d == nil {
		return nil
	}
	cp := *d
	if d.ITLDistribution != nil {
		dist := *d.ITLDistribution
		cp.ITLDistribution = &dist
	}
	if d.RateLimitRemainingRequests != nil {
		v := *d.RateLimitRemainingRequests
		cp.RateLimitRemainingRequests = &v
	}
	if d.RateLimitRemainingTokens != nil {
		v := *d.RateLimitRemainingTokens
		cp.RateLimitRemainingTokens = &v
	}
	if d.ServerQueueMs != nil {
		v := *d.ServerQueueMs
		cp.ServerQueueMs = &v
	}
	if d.ServerPrefillMs != nil {
		v := *d.ServerPrefillMs
		cp.ServerPrefillMs = &v
	}
	if d.ServerDecodeMs != nil {
		v := *d.ServerDecodeMs
		cp.ServerDecodeMs = &v
	}
	return &cp
}

// MergeLLMHopDiagnostics rolls up a child LLM hop into an enclosing business transaction's
// LLMDiagnostics (summing token counts, tool calls, output chars, and hop count, retaining
// first-hop TTFT, and computing completion-token-weighted TPOT).
func MergeLLMHopDiagnostics(dst **LLMDiagnostics, hop *LLMDiagnostics) {
	if dst == nil || hop == nil {
		return
	}
	if *dst == nil {
		cp := hop.Clone()
		if cp.LLMHopCount <= 0 {
			cp.LLMHopCount = 1
		}
		*dst = cp
		return
	}
	cur := *dst
	if cur.LLMHopCount <= 0 {
		cur.LLMHopCount = 1
	}
	hopCount := hop.LLMHopCount
	if hopCount <= 0 {
		hopCount = 1
	}
	prevComp := cur.CompletionTokens
	cur.LLMHopCount += hopCount
	cur.PromptTokens += hop.PromptTokens
	cur.CompletionTokens += hop.CompletionTokens
	cur.ReasoningTokens += hop.ReasoningTokens
	cur.CachedTokens += hop.CachedTokens
	cur.TotalTokens += hop.TotalTokens
	cur.ToolCallsCount += hop.ToolCallsCount
	cur.OutputChars += hop.OutputChars
	cur.E2EMs += hop.E2EMs
	if cur.TTFTMs <= 0 && hop.TTFTMs > 0 {
		cur.TTFTMs = hop.TTFTMs
	}
	if cur.FirstVisibleTokenMs <= 0 && hop.FirstVisibleTokenMs > 0 {
		cur.FirstVisibleTokenMs = hop.FirstVisibleTokenMs
	}
	if cur.CompletionTokens > 0 && (cur.TPOTMs > 0 || hop.TPOTMs > 0) {
		cur.TPOTMs = (cur.TPOTMs*float64(prevComp) + hop.TPOTMs*float64(hop.CompletionTokens)) / float64(cur.CompletionTokens)
	}
	if hop.ITLDistribution != nil {
		if cur.ITLDistribution == nil {
			dist := *hop.ITLDistribution
			cur.ITLDistribution = &dist
		} else {
			cur.ITLDistribution.Merge(*hop.ITLDistribution)
		}
	}
	cur.Truncated = cur.Truncated || hop.Truncated
	cur.Throttled = cur.Throttled || hop.Throttled
	cur.EmptyOutput = cur.EmptyOutput && hop.EmptyOutput
	cur.HTTP200Error = cur.HTTP200Error || hop.HTTP200Error
	cur.SchemaFailed = cur.SchemaFailed || hop.SchemaFailed
	// The roll-up's estimate is prompt-only when every estimated hop's was.
	cur.PromptTokensEstimated = (cur.PromptTokensEstimated || !cur.TokensEstimated) && (hop.PromptTokensEstimated || !hop.TokensEstimated) && (cur.TokensEstimated || hop.TokensEstimated)
	cur.TokensEstimated = cur.TokensEstimated || hop.TokensEstimated
	cur.ReasoningTokensEstimated = cur.ReasoningTokensEstimated || hop.ReasoningTokensEstimated
	if hop.FinishReason != "" {
		cur.FinishReason = hop.FinishReason
	}
	if cur.Model == "" {
		cur.Model = hop.Model
	}
	cur.InputLengthBand = ClassifyLLMInputBand(cur.PromptTokens)
}

func (d *DiagnosticDistribution) Merge(other DiagnosticDistribution) {
	if d == nil || other.Count <= 0 {
		return
	}
	if d.Count == 0 || other.Min < d.Min {
		d.Min = other.Min
	}
	if other.Max > d.Max {
		d.Max = other.Max
	}
	d.Count += other.Count
	d.Sum += other.Sum
	for i, n := range other.Buckets {
		d.Buckets[i] += n
	}
}

// IsLLMAction reports whether an action runs with LLM stream accounting: an
// llm_request step, or an HTTP step that opts in with params.llm,
// params.llm_dialect or params.stream_profile.
func IsLLMAction(action *Action) bool {
	if action == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(action.Type), "llm_request") {
		return true
	}
	if action.Params == nil {
		return false
	}
	switch v := action.Params["llm"].(type) {
	case bool:
		if v {
			return true
		}
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes":
			return true
		}
	}
	if _, hasDialect := action.Params["llm_dialect"]; hasDialect {
		return true
	}
	_, hasProfile := action.Params["stream_profile"]
	return hasProfile
}

// IsLLMLengthFinish reports a finish reason that means the generation stopped
// at its output-length limit. The server finished the response; it is not a
// stream the client cut off.
func IsLLMLengthFinish(reason string) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "length", "max_tokens", "max_output_tokens", "model_length":
		return true
	default:
		return false
	}
}

// ClassifyLLMInputBand classifies a request's prompt token count into one of the
// four standard input-length bands: short (<512), medium (512–2047),
// long (2048–8191), or xlarge (>=8192).
func ClassifyLLMInputBand(promptTokens int) string {
	switch {
	case promptTokens <= 0:
		return "short"
	case promptTokens < 512:
		return "short"
	case promptTokens < 2048:
		return "medium"
	case promptTokens < 8192:
		return "long"
	default:
		return "xlarge"
	}
}

// EstimateTokenCount provides a deterministic dependency-free token estimate
// (~4 UTF-8 bytes per token, minimum 1 when text is non-empty).
func EstimateTokenCount(text string) int {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return 0
	}
	n := (len(trimmed) + 3) / 4
	if n < 1 {
		return 1
	}
	return n
}
