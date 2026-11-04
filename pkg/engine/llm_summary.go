package engine

import (
	"math"
	"strings"

	"github.com/jasonczech/apexload/pkg/protocol"
)

const maxLLMSummaryMapEntries = 16

// LLMBandSummary holds aggregated LLM latency and token metrics for one
// input-length band ("short", "medium", "long", "xlarge").
type LLMBandSummary struct {
	Band             string                  `json:"band"`
	Requests         int64                   `json:"requests"`
	StreamedRequests int64                   `json:"streamed_requests,omitempty"`
	PromptTokens     int64                   `json:"prompt_tokens,omitempty"`
	CompletionTokens int64                   `json:"completion_tokens,omitempty"`
	TTFTSketch       *DurationSketchSnapshot `json:"ttft_sketch,omitempty"`
	TPOTSketch       *DurationSketchSnapshot `json:"tpot_sketch,omitempty"`
	E2ESketch        *DurationSketchSnapshot `json:"e2e_sketch,omitempty"`
	AvgTTFTMs        float64                 `json:"avg_ttft_ms,omitempty"`
	P50TTFTMs        float64                 `json:"p50_ttft_ms,omitempty"`
	P95TTFTMs        float64                 `json:"p95_ttft_ms,omitempty"`
	P99TTFTMs        float64                 `json:"p99_ttft_ms,omitempty"`
	AvgTPOTMs        float64                 `json:"avg_tpot_ms,omitempty"`
	P50TPOTMs        float64                 `json:"p50_tpot_ms,omitempty"`
	P95TPOTMs        float64                 `json:"p95_tpot_ms,omitempty"`
	P99TPOTMs        float64                 `json:"p99_tpot_ms,omitempty"`
	AvgE2EMs         float64                 `json:"avg_e2e_ms,omitempty"`
	P95E2EMs         float64                 `json:"p95_e2e_ms,omitempty"`
}

// LLMSummary aggregates per-action LLM inference telemetry across a collection
// window or an entire run. Latency sketches (TTFT, FirstVisibleToken, TPOT)
// include only successful, complete, streamed requests (matching vLLM and
// GenAI-Perf conventions), whereas counters include all requests.
type LLMSummary struct {
	Requests                int64   `json:"requests"`
	SuccessfulRequests      int64   `json:"successful_requests,omitempty"`
	StreamedRequests        int64   `json:"streamed_requests,omitempty"`
	FailedRequests          int64   `json:"failed_requests,omitempty"`
	ThrottledRequests       int64   `json:"throttled_requests,omitempty"`
	ThrottledAttempts       int64   `json:"throttled_attempts,omitempty"`
	TotalRetryWaitMs        float64 `json:"total_retry_wait_ms,omitempty"`
	TruncatedRequests       int64   `json:"truncated_requests,omitempty"`
	EmptyOutputRequests     int64   `json:"empty_output_requests,omitempty"`
	ContentFilterRequests   int64   `json:"content_filter_requests,omitempty"`
	HTTP200ErrorRequests    int64   `json:"http_200_error_requests,omitempty"`
	SchemaFailedRequests    int64   `json:"schema_failed_requests,omitempty"`
	TokensEstimatedRequests int64   `json:"tokens_estimated_requests,omitempty"`

	PromptTokens     int64 `json:"prompt_tokens,omitempty"`
	CompletionTokens int64 `json:"completion_tokens,omitempty"`
	ReasoningTokens  int64 `json:"reasoning_tokens,omitempty"`
	CachedTokens     int64 `json:"cached_tokens,omitempty"`
	TotalTokens      int64 `json:"total_tokens,omitempty"`
	ToolCallsCount   int64 `json:"tool_calls_count,omitempty"`

	FinishReasons map[string]int64 `json:"finish_reasons,omitempty"`
	Models        map[string]int64 `json:"models,omitempty"`
	Dialects      map[string]int64 `json:"dialects,omitempty"`

	TTFTSketch              *DurationSketchSnapshot          `json:"ttft_sketch,omitempty"`
	FirstVisibleTokenSketch *DurationSketchSnapshot          `json:"first_visible_token_sketch,omitempty"`
	TPOTSketch              *DurationSketchSnapshot          `json:"tpot_sketch,omitempty"`
	E2ESketch               *DurationSketchSnapshot          `json:"e2e_sketch,omitempty"`
	ITLDistribution         *protocol.DiagnosticDistribution `json:"itl_ms,omitempty"`

	AvgTTFTMs              float64 `json:"avg_ttft_ms,omitempty"`
	P50TTFTMs              float64 `json:"p50_ttft_ms,omitempty"`
	P90TTFTMs              float64 `json:"p90_ttft_ms,omitempty"`
	P95TTFTMs              float64 `json:"p95_ttft_ms,omitempty"`
	P99TTFTMs              float64 `json:"p99_ttft_ms,omitempty"`
	AvgFirstVisibleTokenMs float64 `json:"avg_first_visible_token_ms,omitempty"`
	P50FirstVisibleTokenMs float64 `json:"p50_first_visible_token_ms,omitempty"`
	P95FirstVisibleTokenMs float64 `json:"p95_first_visible_token_ms,omitempty"`
	P99FirstVisibleTokenMs float64 `json:"p99_first_visible_token_ms,omitempty"`
	AvgTPOTMs              float64 `json:"avg_tpot_ms,omitempty"`
	P50TPOTMs              float64 `json:"p50_tpot_ms,omitempty"`
	P90TPOTMs              float64 `json:"p90_tpot_ms,omitempty"`
	P95TPOTMs              float64 `json:"p95_tpot_ms,omitempty"`
	P99TPOTMs              float64 `json:"p99_tpot_ms,omitempty"`
	AvgITLMs               float64 `json:"avg_itl_ms,omitempty"`
	P50ITLMs               float64 `json:"p50_itl_ms,omitempty"`
	P90ITLMs               float64 `json:"p90_itl_ms,omitempty"`
	P95ITLMs               float64 `json:"p95_itl_ms,omitempty"`
	P99ITLMs               float64 `json:"p99_itl_ms,omitempty"`
	AvgE2EMs               float64 `json:"avg_e2e_ms,omitempty"`
	P50E2EMs               float64 `json:"p50_e2e_ms,omitempty"`
	P90E2EMs               float64 `json:"p90_e2e_ms,omitempty"`
	P95E2EMs               float64 `json:"p95_e2e_ms,omitempty"`
	P99E2EMs               float64 `json:"p99_e2e_ms,omitempty"`

	ServerQueueSamples   int64   `json:"server_queue_samples,omitempty"`
	ServerQueueSumMs     float64 `json:"server_queue_sum_ms,omitempty"`
	AvgServerQueueMs     float64 `json:"avg_server_queue_ms,omitempty"`
	ServerPrefillSamples int64   `json:"server_prefill_samples,omitempty"`
	ServerPrefillSumMs   float64 `json:"server_prefill_sum_ms,omitempty"`
	AvgServerPrefillMs   float64 `json:"avg_server_prefill_ms,omitempty"`
	ServerDecodeSamples  int64   `json:"server_decode_samples,omitempty"`
	ServerDecodeSumMs    float64 `json:"server_decode_sum_ms,omitempty"`
	AvgServerDecodeMs    float64 `json:"avg_server_decode_ms,omitempty"`

	MinRateLimitRemainingTokens   *int64 `json:"min_ratelimit_remaining_tokens,omitempty"`
	MinRateLimitRemainingRequests *int64 `json:"min_ratelimit_remaining_requests,omitempty"`

	ByInputBand map[string]*LLMBandSummary `json:"by_input_band,omitempty"`
	Incomplete  bool                       `json:"incomplete,omitempty"`
}

// ObserveLLMAction records a single non-business-transaction protocol.Result into
// dst, allocating *dst on first observation.
func ObserveLLMAction(dst **LLMSummary, result *protocol.Result) {
	if dst == nil || result == nil || result.BusinessTransaction {
		return
	}
	if result.ServerTelemetry == nil || result.ServerTelemetry.LLM == nil {
		return
	}
	d := result.ServerTelemetry.LLM
	if *dst == nil {
		*dst = &LLMSummary{}
	}
	s := *dst
	s.Requests++
	throttled := d.Throttled || result.StatusCode == 429
	if throttled {
		s.ThrottledRequests++
	}
	if result.ThrottledAttempts > 0 {
		s.ThrottledAttempts += int64(result.ThrottledAttempts)
	} else if throttled {
		s.ThrottledAttempts++
	}
	if result.RetryWaitMs > 0 {
		s.TotalRetryWaitMs += result.RetryWaitMs
	}
	if d.Truncated {
		s.TruncatedRequests++
	}
	if d.EmptyOutput {
		s.EmptyOutputRequests++
	}
	if strings.EqualFold(d.FinishReason, "content_filter") || strings.EqualFold(d.ErrorCode, "content_filter") {
		s.ContentFilterRequests++
	}
	if d.HTTP200Error {
		s.HTTP200ErrorRequests++
	}
	if d.SchemaFailed {
		s.SchemaFailedRequests++
	}
	if d.TokensEstimated {
		s.TokensEstimatedRequests++
	}

	// Success already fails a cut-off stream and, unless the script accepts
	// it, a length finish; truncation itself is only counted above.
	if result.Success && !throttled {
		s.SuccessfulRequests++
	} else if !throttled {
		s.FailedRequests++
	}

	s.PromptTokens += int64(max(0, d.PromptTokens))
	s.CompletionTokens += int64(max(0, d.CompletionTokens))
	s.ReasoningTokens += int64(max(0, d.ReasoningTokens))
	s.CachedTokens += int64(max(0, d.CachedTokens))
	totalTok := d.TotalTokens
	if totalTok <= 0 && (d.PromptTokens > 0 || d.CompletionTokens > 0) {
		totalTok = d.PromptTokens + d.CompletionTokens
	}
	s.TotalTokens += int64(max(0, totalTok))
	s.ToolCallsCount += int64(max(0, d.ToolCallsCount))

	if fr := strings.TrimSpace(d.FinishReason); fr != "" {
		incrementBoundedLLMMap(&s.FinishReasons, fr, 1)
	}
	if m := strings.TrimSpace(d.Model); m != "" {
		incrementBoundedLLMMap(&s.Models, m, 1)
	}
	if dl := strings.TrimSpace(d.Dialect); dl != "" {
		incrementBoundedLLMMap(&s.Dialects, dl, 1)
	}

	if d.ServerQueueMs != nil && *d.ServerQueueMs >= 0 && !math.IsNaN(*d.ServerQueueMs) {
		s.ServerQueueSamples++
		s.ServerQueueSumMs += *d.ServerQueueMs
	}
	if d.ServerPrefillMs != nil && *d.ServerPrefillMs >= 0 && !math.IsNaN(*d.ServerPrefillMs) {
		s.ServerPrefillSamples++
		s.ServerPrefillSumMs += *d.ServerPrefillMs
	}
	if d.ServerDecodeMs != nil && *d.ServerDecodeMs >= 0 && !math.IsNaN(*d.ServerDecodeMs) {
		s.ServerDecodeSamples++
		s.ServerDecodeSumMs += *d.ServerDecodeMs
	}

	if d.RateLimitRemainingTokens != nil && *d.RateLimitRemainingTokens >= 0 {
		v := *d.RateLimitRemainingTokens
		if s.MinRateLimitRemainingTokens == nil || v < *s.MinRateLimitRemainingTokens {
			s.MinRateLimitRemainingTokens = &v
		}
	}
	if d.RateLimitRemainingRequests != nil && *d.RateLimitRemainingRequests >= 0 {
		v := *d.RateLimitRemainingRequests
		if s.MinRateLimitRemainingRequests == nil || v < *s.MinRateLimitRemainingRequests {
			s.MinRateLimitRemainingRequests = &v
		}
	}

	bandName := strings.TrimSpace(d.InputLengthBand)
	if bandName == "" {
		bandName = protocol.ClassifyLLMInputBand(d.PromptTokens)
	}
	if s.ByInputBand == nil {
		s.ByInputBand = make(map[string]*LLMBandSummary, 4)
	}
	band := s.ByInputBand[bandName]
	if band == nil && len(s.ByInputBand) < 8 {
		band = &LLMBandSummary{Band: bandName}
		s.ByInputBand[bandName] = band
	}
	if band != nil {
		band.Requests++
		band.PromptTokens += int64(max(0, d.PromptTokens))
		band.CompletionTokens += int64(max(0, d.CompletionTokens))
	}

	// Latency distributions use only successful, complete, streamed requests
	// for TTFT / FirstVisibleToken / TPOT / ITL, and successful requests for E2E.
	if result.Success && !throttled {
		e2eMs := d.E2EMs
		if e2eMs <= 0 && result.Duration > 0 {
			e2eMs = float64(result.Duration) / 1e6
		}
		if e2eMs > 0 {
			addValueToSketchSnapshot(&s.E2ESketch, e2eMs)
			if band != nil {
				addValueToSketchSnapshot(&band.E2ESketch, e2eMs)
			}
		}
		if d.Stream {
			s.StreamedRequests++
			if band != nil {
				band.StreamedRequests++
			}
			if d.TTFTMs > 0 {
				addValueToSketchSnapshot(&s.TTFTSketch, d.TTFTMs)
				if band != nil {
					addValueToSketchSnapshot(&band.TTFTSketch, d.TTFTMs)
				}
			}
			if d.FirstVisibleTokenMs > 0 {
				addValueToSketchSnapshot(&s.FirstVisibleTokenSketch, d.FirstVisibleTokenMs)
			}
			if d.TPOTMs > 0 {
				addValueToSketchSnapshot(&s.TPOTSketch, d.TPOTMs)
				if band != nil {
					addValueToSketchSnapshot(&band.TPOTSketch, d.TPOTMs)
				}
			}
			if d.ITLDistribution != nil && d.ITLDistribution.Count > 0 {
				if s.ITLDistribution == nil {
					s.ITLDistribution = &protocol.DiagnosticDistribution{}
				}
				s.ITLDistribution.Merge(*d.ITLDistribution)
			}
		}
	}
}

// FinalizeLLMSummary computes derived averages and percentiles from the
// underlying sketches and histograms when present, preserving scalar fields on
// numbers-only summaries. It is idempotent.
func FinalizeLLMSummary(s *LLMSummary) {
	if s == nil {
		return
	}
	if s.TTFTSketch != nil && s.TTFTSketch.Count > 0 {
		s.AvgTTFTMs, s.P50TTFTMs, s.P90TTFTMs, s.P95TTFTMs, s.P99TTFTMs = quantilesFromSketchSnapshot(s.TTFTSketch)
	}
	if s.FirstVisibleTokenSketch != nil && s.FirstVisibleTokenSketch.Count > 0 {
		s.AvgFirstVisibleTokenMs, s.P50FirstVisibleTokenMs, _, s.P95FirstVisibleTokenMs, s.P99FirstVisibleTokenMs = quantilesFromSketchSnapshot(s.FirstVisibleTokenSketch)
	}
	if s.TPOTSketch != nil && s.TPOTSketch.Count > 0 {
		s.AvgTPOTMs, s.P50TPOTMs, s.P90TPOTMs, s.P95TPOTMs, s.P99TPOTMs = quantilesFromSketchSnapshot(s.TPOTSketch)
	}
	if s.E2ESketch != nil && s.E2ESketch.Count > 0 {
		s.AvgE2EMs, s.P50E2EMs, s.P90E2EMs, s.P95E2EMs, s.P99E2EMs = quantilesFromSketchSnapshot(s.E2ESketch)
	}

	if s.ITLDistribution != nil && s.ITLDistribution.Count > 0 {
		s.AvgITLMs = s.ITLDistribution.Sum / float64(s.ITLDistribution.Count)
		s.P50ITLMs, _ = s.ITLDistribution.Quantile(0.50)
		s.P90ITLMs, _ = s.ITLDistribution.Quantile(0.90)
		s.P95ITLMs, _ = s.ITLDistribution.Quantile(0.95)
		s.P99ITLMs, _ = s.ITLDistribution.Quantile(0.99)
	}
	if s.ServerQueueSamples > 0 {
		s.AvgServerQueueMs = s.ServerQueueSumMs / float64(s.ServerQueueSamples)
	}
	if s.ServerPrefillSamples > 0 {
		s.AvgServerPrefillMs = s.ServerPrefillSumMs / float64(s.ServerPrefillSamples)
	}
	if s.ServerDecodeSamples > 0 {
		s.AvgServerDecodeMs = s.ServerDecodeSumMs / float64(s.ServerDecodeSamples)
	}
	for _, band := range s.ByInputBand {
		if band == nil {
			continue
		}
		if band.TTFTSketch != nil && band.TTFTSketch.Count > 0 {
			band.AvgTTFTMs, band.P50TTFTMs, _, band.P95TTFTMs, band.P99TTFTMs = quantilesFromSketchSnapshot(band.TTFTSketch)
		}
		if band.TPOTSketch != nil && band.TPOTSketch.Count > 0 {
			band.AvgTPOTMs, band.P50TPOTMs, _, band.P95TPOTMs, band.P99TPOTMs = quantilesFromSketchSnapshot(band.TPOTSketch)
		}
		if band.E2ESketch != nil && band.E2ESketch.Count > 0 {
			band.AvgE2EMs, _, _, band.P95E2EMs, _ = quantilesFromSketchSnapshot(band.E2ESketch)
		}
	}
}

func llmStreamWeight(s *LLMSummary) float64 {
	if s == nil {
		return 0
	}
	if s.StreamedRequests > 0 {
		return float64(s.StreamedRequests)
	}
	if s.SuccessfulRequests > 0 {
		return float64(s.SuccessfulRequests)
	}
	return float64(max(0, s.Requests))
}

func llmSuccessWeight(s *LLMSummary) float64 {
	if s == nil {
		return 0
	}
	if s.SuccessfulRequests > 0 {
		return float64(s.SuccessfulRequests)
	}
	return float64(max(0, s.Requests))
}

func weightedLLMValue(a, wa, b, wb float64) float64 {
	if wa <= 0 || a <= 0 {
		return b
	}
	if wb <= 0 || b <= 0 {
		return a
	}
	return (a*wa + b*wb) / (wa + wb)
}

// MergeLLMSummary merges src into *dst. The merge is commutative and
// associative across both counters and DurationSketch / DiagnosticDistribution
// distributions, and falls back to weighted averaging when merging numbers-only
// summaries.
func MergeLLMSummary(dst **LLMSummary, src *LLMSummary) {
	if dst == nil || src == nil || src.Requests <= 0 {
		return
	}
	if *dst == nil {
		*dst = src.Clone()
		FinalizeLLMSummary(*dst)
		return
	}
	d := *dst
	wDstStream := llmStreamWeight(d)
	wSrcStream := llmStreamWeight(src)
	wDstSuccess := llmSuccessWeight(d)
	wSrcSuccess := llmSuccessWeight(src)

	if d.TTFTSketch == nil && src.TTFTSketch == nil {
		d.AvgTTFTMs = weightedLLMValue(d.AvgTTFTMs, wDstStream, src.AvgTTFTMs, wSrcStream)
		d.P50TTFTMs = weightedLLMValue(d.P50TTFTMs, wDstStream, src.P50TTFTMs, wSrcStream)
		d.P90TTFTMs = weightedLLMValue(d.P90TTFTMs, wDstStream, src.P90TTFTMs, wSrcStream)
		d.P95TTFTMs = weightedLLMValue(d.P95TTFTMs, wDstStream, src.P95TTFTMs, wSrcStream)
		d.P99TTFTMs = weightedLLMValue(d.P99TTFTMs, wDstStream, src.P99TTFTMs, wSrcStream)
	}
	if d.FirstVisibleTokenSketch == nil && src.FirstVisibleTokenSketch == nil {
		d.AvgFirstVisibleTokenMs = weightedLLMValue(d.AvgFirstVisibleTokenMs, wDstStream, src.AvgFirstVisibleTokenMs, wSrcStream)
		d.P50FirstVisibleTokenMs = weightedLLMValue(d.P50FirstVisibleTokenMs, wDstStream, src.P50FirstVisibleTokenMs, wSrcStream)
		d.P95FirstVisibleTokenMs = weightedLLMValue(d.P95FirstVisibleTokenMs, wDstStream, src.P95FirstVisibleTokenMs, wSrcStream)
		d.P99FirstVisibleTokenMs = weightedLLMValue(d.P99FirstVisibleTokenMs, wDstStream, src.P99FirstVisibleTokenMs, wSrcStream)
	}
	if d.TPOTSketch == nil && src.TPOTSketch == nil {
		d.AvgTPOTMs = weightedLLMValue(d.AvgTPOTMs, wDstStream, src.AvgTPOTMs, wSrcStream)
		d.P50TPOTMs = weightedLLMValue(d.P50TPOTMs, wDstStream, src.P50TPOTMs, wSrcStream)
		d.P90TPOTMs = weightedLLMValue(d.P90TPOTMs, wDstStream, src.P90TPOTMs, wSrcStream)
		d.P95TPOTMs = weightedLLMValue(d.P95TPOTMs, wDstStream, src.P95TPOTMs, wSrcStream)
		d.P99TPOTMs = weightedLLMValue(d.P99TPOTMs, wDstStream, src.P99TPOTMs, wSrcStream)
	}
	if d.ITLDistribution == nil && src.ITLDistribution == nil {
		d.AvgITLMs = weightedLLMValue(d.AvgITLMs, wDstStream, src.AvgITLMs, wSrcStream)
		d.P50ITLMs = weightedLLMValue(d.P50ITLMs, wDstStream, src.P50ITLMs, wSrcStream)
		d.P90ITLMs = weightedLLMValue(d.P90ITLMs, wDstStream, src.P90ITLMs, wSrcStream)
		d.P95ITLMs = weightedLLMValue(d.P95ITLMs, wDstStream, src.P95ITLMs, wSrcStream)
		d.P99ITLMs = weightedLLMValue(d.P99ITLMs, wDstStream, src.P99ITLMs, wSrcStream)
	}
	if d.E2ESketch == nil && src.E2ESketch == nil {
		d.AvgE2EMs = weightedLLMValue(d.AvgE2EMs, wDstSuccess, src.AvgE2EMs, wSrcSuccess)
		d.P50E2EMs = weightedLLMValue(d.P50E2EMs, wDstSuccess, src.P50E2EMs, wSrcSuccess)
		d.P90E2EMs = weightedLLMValue(d.P90E2EMs, wDstSuccess, src.P90E2EMs, wSrcSuccess)
		d.P95E2EMs = weightedLLMValue(d.P95E2EMs, wDstSuccess, src.P95E2EMs, wSrcSuccess)
		d.P99E2EMs = weightedLLMValue(d.P99E2EMs, wDstSuccess, src.P99E2EMs, wSrcSuccess)
	}

	d.Incomplete = d.Incomplete || src.Incomplete
	d.Requests += src.Requests
	d.SuccessfulRequests += src.SuccessfulRequests
	d.StreamedRequests += src.StreamedRequests
	d.FailedRequests += src.FailedRequests
	d.ThrottledRequests += src.ThrottledRequests
	d.ThrottledAttempts += src.ThrottledAttempts
	d.TotalRetryWaitMs += src.TotalRetryWaitMs
	d.TruncatedRequests += src.TruncatedRequests
	d.EmptyOutputRequests += src.EmptyOutputRequests
	d.ContentFilterRequests += src.ContentFilterRequests
	d.HTTP200ErrorRequests += src.HTTP200ErrorRequests
	d.SchemaFailedRequests += src.SchemaFailedRequests
	d.TokensEstimatedRequests += src.TokensEstimatedRequests

	d.PromptTokens += src.PromptTokens
	d.CompletionTokens += src.CompletionTokens
	d.ReasoningTokens += src.ReasoningTokens
	d.CachedTokens += src.CachedTokens
	d.TotalTokens += src.TotalTokens
	d.ToolCallsCount += src.ToolCallsCount

	for k, v := range src.FinishReasons {
		incrementBoundedLLMMap(&d.FinishReasons, k, v)
	}
	for k, v := range src.Models {
		incrementBoundedLLMMap(&d.Models, k, v)
	}
	for k, v := range src.Dialects {
		incrementBoundedLLMMap(&d.Dialects, k, v)
	}

	mergeSketchSnapshots(&d.TTFTSketch, src.TTFTSketch)
	mergeSketchSnapshots(&d.FirstVisibleTokenSketch, src.FirstVisibleTokenSketch)
	mergeSketchSnapshots(&d.TPOTSketch, src.TPOTSketch)
	mergeSketchSnapshots(&d.E2ESketch, src.E2ESketch)

	if src.ITLDistribution != nil && src.ITLDistribution.Count > 0 {
		if d.ITLDistribution == nil {
			d.ITLDistribution = &protocol.DiagnosticDistribution{}
		}
		d.ITLDistribution.Merge(*src.ITLDistribution)
	}

	d.ServerQueueSamples += src.ServerQueueSamples
	d.ServerQueueSumMs += src.ServerQueueSumMs
	d.ServerPrefillSamples += src.ServerPrefillSamples
	d.ServerPrefillSumMs += src.ServerPrefillSumMs
	d.ServerDecodeSamples += src.ServerDecodeSamples
	d.ServerDecodeSumMs += src.ServerDecodeSumMs

	if src.MinRateLimitRemainingTokens != nil {
		v := *src.MinRateLimitRemainingTokens
		if d.MinRateLimitRemainingTokens == nil || v < *d.MinRateLimitRemainingTokens {
			d.MinRateLimitRemainingTokens = &v
		}
	}
	if src.MinRateLimitRemainingRequests != nil {
		v := *src.MinRateLimitRemainingRequests
		if d.MinRateLimitRemainingRequests == nil || v < *d.MinRateLimitRemainingRequests {
			d.MinRateLimitRemainingRequests = &v
		}
	}

	if len(src.ByInputBand) > 0 {
		if d.ByInputBand == nil {
			d.ByInputBand = make(map[string]*LLMBandSummary, len(src.ByInputBand))
		}
		for bandName, sb := range src.ByInputBand {
			if sb == nil {
				continue
			}
			db := d.ByInputBand[bandName]
			if db == nil && len(d.ByInputBand) < 8 {
				db = &LLMBandSummary{Band: bandName}
				d.ByInputBand[bandName] = db
			}
			if db != nil {
				wbDst := float64(max(db.StreamedRequests, db.Requests))
				wbSrc := float64(max(sb.StreamedRequests, sb.Requests))
				if db.TTFTSketch == nil && sb.TTFTSketch == nil {
					db.AvgTTFTMs = weightedLLMValue(db.AvgTTFTMs, wbDst, sb.AvgTTFTMs, wbSrc)
					db.P50TTFTMs = weightedLLMValue(db.P50TTFTMs, wbDst, sb.P50TTFTMs, wbSrc)
					db.P95TTFTMs = weightedLLMValue(db.P95TTFTMs, wbDst, sb.P95TTFTMs, wbSrc)
					db.P99TTFTMs = weightedLLMValue(db.P99TTFTMs, wbDst, sb.P99TTFTMs, wbSrc)
				}
				if db.TPOTSketch == nil && sb.TPOTSketch == nil {
					db.AvgTPOTMs = weightedLLMValue(db.AvgTPOTMs, wbDst, sb.AvgTPOTMs, wbSrc)
					db.P50TPOTMs = weightedLLMValue(db.P50TPOTMs, wbDst, sb.P50TPOTMs, wbSrc)
					db.P95TPOTMs = weightedLLMValue(db.P95TPOTMs, wbDst, sb.P95TPOTMs, wbSrc)
					db.P99TPOTMs = weightedLLMValue(db.P99TPOTMs, wbDst, sb.P99TPOTMs, wbSrc)
				}
				if db.E2ESketch == nil && sb.E2ESketch == nil {
					db.AvgE2EMs = weightedLLMValue(db.AvgE2EMs, wbDst, sb.AvgE2EMs, wbSrc)
					db.P95E2EMs = weightedLLMValue(db.P95E2EMs, wbDst, sb.P95E2EMs, wbSrc)
				}
				db.Requests += sb.Requests
				db.StreamedRequests += sb.StreamedRequests
				db.PromptTokens += sb.PromptTokens
				db.CompletionTokens += sb.CompletionTokens
				mergeSketchSnapshots(&db.TTFTSketch, sb.TTFTSketch)
				mergeSketchSnapshots(&db.TPOTSketch, sb.TPOTSketch)
				mergeSketchSnapshots(&db.E2ESketch, sb.E2ESketch)
			}
		}
	}

	FinalizeLLMSummary(d)
}

// NumbersOnly returns a deep copy of s with derived quantiles finalized and raw
// duration sketches / ITL bucket distributions stripped so JSON serialization
// emits numbers only.
func (s *LLMSummary) NumbersOnly() *LLMSummary {
	if s == nil {
		return nil
	}
	cp := s.Clone()
	FinalizeLLMSummary(cp)
	cp.TTFTSketch = nil
	cp.FirstVisibleTokenSketch = nil
	cp.TPOTSketch = nil
	cp.E2ESketch = nil
	cp.ITLDistribution = nil
	for _, band := range cp.ByInputBand {
		if band == nil {
			continue
		}
		band.TTFTSketch = nil
		band.TPOTSketch = nil
		band.E2ESketch = nil
	}
	return cp
}

// Clone returns a deep copy of s.
func (s *LLMSummary) Clone() *LLMSummary {
	if s == nil {
		return nil
	}
	cp := *s
	if len(s.FinishReasons) > 0 {
		cp.FinishReasons = make(map[string]int64, len(s.FinishReasons))
		for k, v := range s.FinishReasons {
			cp.FinishReasons[k] = v
		}
	}
	if len(s.Models) > 0 {
		cp.Models = make(map[string]int64, len(s.Models))
		for k, v := range s.Models {
			cp.Models[k] = v
		}
	}
	if len(s.Dialects) > 0 {
		cp.Dialects = make(map[string]int64, len(s.Dialects))
		for k, v := range s.Dialects {
			cp.Dialects[k] = v
		}
	}
	cp.TTFTSketch = CloneDurationSketchSnapshot(s.TTFTSketch)
	cp.FirstVisibleTokenSketch = CloneDurationSketchSnapshot(s.FirstVisibleTokenSketch)
	cp.TPOTSketch = CloneDurationSketchSnapshot(s.TPOTSketch)
	cp.E2ESketch = CloneDurationSketchSnapshot(s.E2ESketch)
	if s.ITLDistribution != nil {
		dist := *s.ITLDistribution
		cp.ITLDistribution = &dist
	}
	if s.MinRateLimitRemainingTokens != nil {
		v := *s.MinRateLimitRemainingTokens
		cp.MinRateLimitRemainingTokens = &v
	}
	if s.MinRateLimitRemainingRequests != nil {
		v := *s.MinRateLimitRemainingRequests
		cp.MinRateLimitRemainingRequests = &v
	}
	if len(s.ByInputBand) > 0 {
		cp.ByInputBand = make(map[string]*LLMBandSummary, len(s.ByInputBand))
		for k, b := range s.ByInputBand {
			if b == nil {
				continue
			}
			bcp := *b
			bcp.TTFTSketch = CloneDurationSketchSnapshot(b.TTFTSketch)
			bcp.TPOTSketch = CloneDurationSketchSnapshot(b.TPOTSketch)
			bcp.E2ESketch = CloneDurationSketchSnapshot(b.E2ESketch)
			cp.ByInputBand[k] = &bcp
		}
	}
	return &cp
}

// ThrottleRatePct returns the percentage of total requests that were throttled (429).
func (s *LLMSummary) ThrottleRatePct() float64 {
	if s == nil || s.Requests <= 0 || s.ThrottledRequests <= 0 {
		return 0
	}
	return float64(s.ThrottledRequests) / float64(s.Requests) * 100
}

// TruncationRatePct returns the percentage of total requests whose output was truncated.
func (s *LLMSummary) TruncationRatePct() float64 {
	if s == nil || s.Requests <= 0 || s.TruncatedRequests <= 0 {
		return 0
	}
	return float64(s.TruncatedRequests) / float64(s.Requests) * 100
}

// GoodputPct estimates the percentage of all requests (including throttled and
// failed requests in the denominator) that succeeded (a length finish counts
// when fail_on_truncation: false accepts it) and met all positive latency SLO
// thresholds (TTFT, TPOT, E2E in ms).
func (s *LLMSummary) GoodputPct(sloTTFTMs, sloTPOTMs, sloE2EMs float64) float64 {
	if s == nil || s.Requests <= 0 || s.SuccessfulRequests <= 0 {
		return 0
	}
	baseRatio := float64(s.SuccessfulRequests) / float64(s.Requests)
	passFraction := 1.0
	if sloTTFTMs > 0 {
		if s.TTFTSketch != nil && s.TTFTSketch.Count > 0 {
			if f := sketchPassFraction(s.TTFTSketch, sloTTFTMs); f < passFraction {
				passFraction = f
			}
		} else if f := scalarQuantilePassFraction(sloTTFTMs, s.P50TTFTMs, s.P90TTFTMs, s.P95TTFTMs, s.P99TTFTMs); f < passFraction {
			passFraction = f
		}
	}
	if sloTPOTMs > 0 {
		if s.TPOTSketch != nil && s.TPOTSketch.Count > 0 {
			if f := sketchPassFraction(s.TPOTSketch, sloTPOTMs); f < passFraction {
				passFraction = f
			}
		} else if f := scalarQuantilePassFraction(sloTPOTMs, s.P50TPOTMs, s.P90TPOTMs, s.P95TPOTMs, s.P99TPOTMs); f < passFraction {
			passFraction = f
		}
	}
	if sloE2EMs > 0 {
		if s.E2ESketch != nil && s.E2ESketch.Count > 0 {
			if f := sketchPassFraction(s.E2ESketch, sloE2EMs); f < passFraction {
				passFraction = f
			}
		} else if f := scalarQuantilePassFraction(sloE2EMs, s.P50E2EMs, s.P90E2EMs, s.P95E2EMs, s.P99E2EMs); f < passFraction {
			passFraction = f
		}
	}
	return baseRatio * passFraction * 100
}

func scalarQuantilePassFraction(threshold, p50, p90, p95, p99 float64) float64 {
	if threshold <= 0 || (p50 <= 0 && p90 <= 0 && p95 <= 0 && p99 <= 0) {
		return 1.0
	}
	switch {
	case p99 > 0 && threshold >= p99:
		return 1.0
	case p95 > 0 && threshold >= p95:
		return 0.95
	case p90 > 0 && threshold >= p90:
		return 0.90
	case p50 > 0 && threshold >= p50:
		return 0.50
	default:
		return 0.0
	}
}

func sketchPassFraction(snap *DurationSketchSnapshot, thresholdMs float64) float64 {
	if snap == nil || snap.Count == 0 || thresholdMs <= 0 {
		return 1.0
	}
	if thresholdMs >= snap.Max {
		return 1.0
	}
	if thresholdMs < snap.Min {
		return 0.0
	}
	maxIdx := durationSketchIndex(thresholdMs)
	passed := snap.ZeroCount
	for _, bin := range snap.Bins {
		if bin.Index <= maxIdx {
			passed += bin.Count
		}
	}
	if passed > snap.Count {
		passed = snap.Count
	}
	return float64(passed) / float64(snap.Count)
}

func addValueToSketchSnapshot(dst **DurationSketchSnapshot, v float64) {
	if dst == nil || v < 0 || math.IsNaN(v) || math.IsInf(v, 0) {
		return
	}
	var sketch *DurationSketch
	if *dst != nil {
		if restored, err := DurationSketchFromSnapshot(**dst); err == nil {
			sketch = restored
		}
	}
	if sketch == nil {
		sketch = NewDurationSketch()
	}
	sketch.Add(v)
	snap := sketch.Snapshot()
	*dst = &snap
}

func mergeSketchSnapshots(dst **DurationSketchSnapshot, src *DurationSketchSnapshot) {
	if dst == nil || src == nil || src.Count == 0 {
		return
	}
	srcSketch, err := DurationSketchFromSnapshot(*src)
	if err != nil {
		return
	}
	if *dst == nil || (*dst).Count == 0 {
		snap := srcSketch.Snapshot()
		*dst = &snap
		return
	}
	dstSketch, err := DurationSketchFromSnapshot(**dst)
	if err != nil {
		snap := srcSketch.Snapshot()
		*dst = &snap
		return
	}
	dstSketch.Merge(srcSketch)
	snap := dstSketch.Snapshot()
	*dst = &snap
}

func quantilesFromSketchSnapshot(snap *DurationSketchSnapshot) (avg, p50, p90, p95, p99 float64) {
	if snap == nil || snap.Count == 0 {
		return 0, 0, 0, 0, 0
	}
	sketch, err := DurationSketchFromSnapshot(*snap)
	if err != nil || sketch.Count() == 0 {
		return 0, 0, 0, 0, 0
	}
	avg = snap.Sum / float64(snap.Count)
	p50 = sketch.Quantile(0.50)
	p90 = sketch.Quantile(0.90)
	p95 = sketch.Quantile(0.95)
	p99 = sketch.Quantile(0.99)
	return avg, p50, p90, p95, p99
}

func incrementBoundedLLMMap(m *map[string]int64, key string, delta int64) {
	if m == nil || key == "" || delta <= 0 {
		return
	}
	if *m == nil {
		*m = make(map[string]int64, 4)
	}
	if _, exists := (*m)[key]; !exists && len(*m) >= maxLLMSummaryMapEntries {
		key = metricOverflowLabel
	}
	(*m)[key] += delta
}
