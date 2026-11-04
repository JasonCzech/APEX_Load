package api

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/jasonczech/apexload/pkg/engine"
	"github.com/jasonczech/apexload/pkg/llmperf/econ"
	"github.com/jasonczech/apexload/pkg/protocol"
)

// RunArchiveLLMLatencyQuantiles holds exact or bounded-reservoir percentiles
// for an LLM phase duration (TTFT, TPOT, or E2E) over an archived run.
type RunArchiveLLMLatencyQuantiles struct {
	Samples           int64   `json:"samples"`
	MeanMs            float64 `json:"mean_ms,omitempty"`
	MinMs             float64 `json:"min_ms,omitempty"`
	MaxMs             float64 `json:"max_ms,omitempty"`
	P50Ms             float64 `json:"p50_ms,omitempty"`
	P90Ms             float64 `json:"p90_ms,omitempty"`
	P95Ms             float64 `json:"p95_ms,omitempty"`
	P99Ms             float64 `json:"p99_ms,omitempty"`
	PercentileQuality string  `json:"percentile_quality,omitempty"`
}

// RunArchiveLLMITLSummary summarizes inter-token latency from merged log-linear
// histograms across all streamed chunks in the run.
type RunArchiveLLMITLSummary struct {
	Samples           int64                            `json:"samples"`
	MeanMs            float64                          `json:"mean_ms,omitempty"`
	MaxMs             float64                          `json:"max_ms,omitempty"`
	P50Ms             float64                          `json:"p50_ms,omitempty"`
	P90Ms             float64                          `json:"p90_ms,omitempty"`
	P95Ms             float64                          `json:"p95_ms,omitempty"`
	P99Ms             float64                          `json:"p99_ms,omitempty"`
	PercentileQuality string                           `json:"percentile_quality,omitempty"`
	StallCount        int64                            `json:"stall_count,omitempty"`
	MaxStallMs        float64                          `json:"max_stall_ms,omitempty"`
	Distribution      *protocol.DiagnosticDistribution `json:"distribution,omitempty"`
}

// RunArchiveLLMLengthBandStats groups LLM performance by prompt token length band
// so prefill cost (TTFT) is isolated from decode cost (TPOT) across context sizes.
type RunArchiveLLMLengthBandStats struct {
	Band                 string                        `json:"band"`
	Label                string                        `json:"label"`
	Requests             int64                         `json:"requests"`
	StreamedRequests     int64                         `json:"streamed_requests,omitempty"`
	PromptTokens         int64                         `json:"prompt_tokens,omitempty"`
	CompletionTokens     int64                         `json:"completion_tokens,omitempty"`
	MeanPromptTokens     float64                       `json:"mean_prompt_tokens,omitempty"`
	MeanCompletionTokens float64                       `json:"mean_completion_tokens,omitempty"`
	TTFT                 RunArchiveLLMLatencyQuantiles `json:"ttft"`
	TPOT                 RunArchiveLLMLatencyQuantiles `json:"tpot"`
	E2E                  RunArchiveLLMLatencyQuantiles `json:"e2e"`
}

// RunArchiveActionLLMStats summarizes LLM metrics for one action or model.
type RunArchiveActionLLMStats struct {
	Name                 string                        `json:"name,omitempty"`
	Requests             int64                         `json:"requests"`
	StreamedRequests     int64                         `json:"streamed_requests,omitempty"`
	SuccessfulRequests   int64                         `json:"successful_requests,omitempty"`
	ThrottledRequests    int64                         `json:"throttled_requests,omitempty"`
	TruncatedRequests    int64                         `json:"truncated_requests,omitempty"`
	EmptyRequests        int64                         `json:"empty_requests,omitempty"`
	RefusedRequests      int64                         `json:"refused_requests,omitempty"`
	ToolCallRequests     int64                         `json:"tool_call_requests,omitempty"`
	GoodRequests         int64                         `json:"good_requests,omitempty"`
	GoodputPct           float64                       `json:"goodput_pct,omitempty"`
	ThrottleRatePct      float64                       `json:"throttle_rate_pct,omitempty"`
	TruncationRatePct    float64                       `json:"truncation_rate_pct,omitempty"`
	PromptTokens         int64                         `json:"prompt_tokens,omitempty"`
	CompletionTokens     int64                         `json:"completion_tokens,omitempty"`
	CachedTokens         int64                         `json:"cached_tokens,omitempty"`
	ReasoningTokens      int64                         `json:"reasoning_tokens,omitempty"`
	MeanPromptTokens     float64                       `json:"mean_prompt_tokens,omitempty"`
	MeanCompletionTokens float64                       `json:"mean_completion_tokens,omitempty"`
	CacheHitRatePct      float64                       `json:"cache_hit_rate_pct,omitempty"`
	OutputTokensPerSec   float64                       `json:"output_tokens_per_sec,omitempty"`
	TTFT                 RunArchiveLLMLatencyQuantiles `json:"ttft"`
	TPOT                 RunArchiveLLMLatencyQuantiles `json:"tpot"`
	E2E                  RunArchiveLLMLatencyQuantiles `json:"e2e"`
	ITL                  RunArchiveLLMITLSummary       `json:"itl"`
}

// RunArchiveLLMGoldenPromptDrift reports output stability for deterministic
// golden prompts across load levels.
type RunArchiveLLMGoldenPromptDrift struct {
	CheckedPrompts int                             `json:"checked_prompts"`
	DriftedPrompts int                             `json:"drifted_prompts"`
	Status         string                          `json:"status"`
	Items          []RunArchiveLLMGoldenPromptItem `json:"items,omitempty"`
}

// RunArchiveLLMGoldenPromptItem summarizes one golden prompt's observed outputs.
type RunArchiveLLMGoldenPromptItem struct {
	Name                string  `json:"name"`
	Samples             int64   `json:"samples"`
	BaselineHash        string  `json:"baseline_hash,omitempty"`
	ObservedHashes      int     `json:"observed_hashes"`
	MinCompletionTokens int     `json:"min_completion_tokens,omitempty"`
	MaxCompletionTokens int     `json:"max_completion_tokens,omitempty"`
	LengthDriftRatio    float64 `json:"length_drift_ratio,omitempty"`
	Drifted             bool    `json:"drifted,omitempty"`
	Detail              string  `json:"detail,omitempty"`
}

// RunArchiveLLMSummary is the complete archive-level LLM inference performance
// summary framed around prefill (TTFT) vs decode (TPOT/ITL), token throughput,
// SLO goodput, rate-limit behavior, and prompt length bands.
type RunArchiveLLMSummary struct {
	TotalRequests           int64                                `json:"total_requests"`
	StreamedRequests        int64                                `json:"streamed_requests,omitempty"`
	SuccessfulRequests      int64                                `json:"successful_requests,omitempty"`
	ErrorRequests           int64                                `json:"error_requests,omitempty"`
	ThrottledRequests       int64                                `json:"throttled_requests,omitempty"`
	TruncatedRequests       int64                                `json:"truncated_requests,omitempty"`
	EmptyRequests           int64                                `json:"empty_requests,omitempty"`
	RefusedRequests         int64                                `json:"refused_requests,omitempty"`
	ToolCallRequests        int64                                `json:"tool_call_requests,omitempty"`
	FallbackRequests        int64                                `json:"fallback_requests,omitempty"`
	TotalRetries            int64                                `json:"total_retries,omitempty"`
	TotalThrottledAttempts  int64                                `json:"total_throttled_attempts,omitempty"`
	TotalRetryWaitMs        float64                              `json:"total_retry_wait_ms,omitempty"`
	TokensEstimatedRequests int64                                `json:"tokens_estimated_requests,omitempty"`
	EstimatedTokenSharePct  float64                              `json:"estimated_token_share_pct,omitempty"`
	GoodRequests            int64                                `json:"good_requests,omitempty"`
	GoodputPct              float64                              `json:"goodput_pct,omitempty"`
	GoodOutputTokensPerSec  float64                              `json:"good_output_tokens_per_sec,omitempty"`
	SLOTTFTMs               *float64                             `json:"slo_ttft_ms,omitempty"`
	SLOTPOTMs               *float64                             `json:"slo_tpot_ms,omitempty"`
	SLOE2EMs                *float64                             `json:"slo_e2e_ms,omitempty"`
	ThrottleRatePct         float64                              `json:"throttle_rate_pct,omitempty"`
	TruncationRatePct       float64                              `json:"truncation_rate_pct,omitempty"`
	EmptyRatePct            float64                              `json:"empty_rate_pct,omitempty"`
	RefusalRatePct          float64                              `json:"refusal_rate_pct,omitempty"`
	ErrorRatePct            float64                              `json:"error_rate_pct,omitempty"`
	PromptTokens            int64                                `json:"prompt_tokens,omitempty"`
	CompletionTokens        int64                                `json:"completion_tokens,omitempty"`
	TotalTokens             int64                                `json:"total_tokens,omitempty"`
	CachedTokens            int64                                `json:"cached_tokens,omitempty"`
	ReasoningTokens         int64                                `json:"reasoning_tokens,omitempty"`
	MeanPromptTokens        float64                              `json:"mean_prompt_tokens,omitempty"`
	MeanCompletionTokens    float64                              `json:"mean_completion_tokens,omitempty"`
	CacheHitRatePct         float64                              `json:"cache_hit_rate_pct,omitempty"`
	RequestsPerSec          float64                              `json:"requests_per_sec,omitempty"`
	OutputTokensPerSec      float64                              `json:"output_tokens_per_sec,omitempty"`
	InputTokensPerSec       float64                              `json:"input_tokens_per_sec,omitempty"`
	TotalTokensPerSec       float64                              `json:"total_tokens_per_sec,omitempty"`
	TTFT                    RunArchiveLLMLatencyQuantiles        `json:"ttft"`
	FirstContent            RunArchiveLLMLatencyQuantiles        `json:"first_content,omitempty"`
	TPOT                    RunArchiveLLMLatencyQuantiles        `json:"tpot"`
	E2E                     RunArchiveLLMLatencyQuantiles        `json:"e2e"`
	ITL                     RunArchiveLLMITLSummary              `json:"itl"`
	FinishReasons           map[string]int64                     `json:"finish_reasons,omitempty"`
	ByLengthBand            []RunArchiveLLMLengthBandStats       `json:"by_length_band,omitempty"`
	ByModel                 map[string]*RunArchiveActionLLMStats `json:"by_model,omitempty"`
	ByAction                map[string]*RunArchiveActionLLMStats `json:"by_action,omitempty"`
	GoldenPromptDrift       *RunArchiveLLMGoldenPromptDrift      `json:"golden_prompt_drift,omitempty"`
	Economics               *econ.Summary                        `json:"economics,omitempty"`
	LatencyDecomposition    *LLMLatencyDecomposition             `json:"latency_decomposition,omitempty"`
	CapacitySweep           *CapacitySweepReport                 `json:"capacity_sweep,omitempty"`
}

type archiveLLMLatencyTracker struct {
	samples   int64
	sum       float64
	min       float64
	max       float64
	quantiles *archiveQuantileAccumulator
}

func newArchiveLLMLatencyTracker(budget *archiveQuantileBudget) *archiveLLMLatencyTracker {
	return &archiveLLMLatencyTracker{
		min:       math.Inf(1),
		quantiles: newArchiveGateQuantileAccumulator(budget),
	}
}

func (t *archiveLLMLatencyTracker) add(ms float64) {
	if t == nil || ms <= 0 || math.IsNaN(ms) || math.IsInf(ms, 0) {
		return
	}
	t.samples++
	t.sum += ms
	if ms < t.min {
		t.min = ms
	}
	if ms > t.max {
		t.max = ms
	}
	t.quantiles.Add(ms)
}

func (t *archiveLLMLatencyTracker) finish() RunArchiveLLMLatencyQuantiles {
	if t == nil || t.samples == 0 {
		return RunArchiveLLMLatencyQuantiles{PercentileQuality: archivePercentileQualityUnavailable}
	}
	return RunArchiveLLMLatencyQuantiles{
		Samples:           t.samples,
		MeanMs:            t.sum / float64(t.samples),
		MinMs:             t.min,
		MaxMs:             t.max,
		P50Ms:             t.quantiles.Quantile(0.50),
		P90Ms:             t.quantiles.Quantile(0.90),
		P95Ms:             t.quantiles.Quantile(0.95),
		P99Ms:             t.quantiles.Quantile(0.99),
		PercentileQuality: t.quantiles.PercentileQuality(),
	}
}

type archiveActionLLMAccumulator struct {
	name             string
	requests         int64
	streamed         int64
	successful       int64
	throttled        int64
	truncated        int64
	empty            int64
	refused          int64
	toolCalls        int64
	good             int64
	promptTokens     int64
	completionTokens int64
	cachedTokens     int64
	reasoningTokens  int64
	ttft             *archiveLLMLatencyTracker
	tpot             *archiveLLMLatencyTracker
	e2e              *archiveLLMLatencyTracker
	itl              protocol.DiagnosticDistribution
	stallCount       int64
	maxStallMs       float64
}

func newArchiveActionLLMAccumulator(name string, budget *archiveQuantileBudget) *archiveActionLLMAccumulator {
	return &archiveActionLLMAccumulator{
		name: name,
		ttft: newArchiveLLMLatencyTracker(budget),
		tpot: newArchiveLLMLatencyTracker(budget),
		e2e:  newArchiveLLMLatencyTracker(budget),
	}
}

func (a *archiveActionLLMAccumulator) add(tx TransactionEvent, d *protocol.LLMDiagnostics, good bool) {
	if a == nil || d == nil {
		return
	}
	a.requests++
	throttled := d.Throttled || tx.StatusCode == 429
	if throttled {
		a.throttled++
	}
	if d.Truncated {
		a.truncated++
	}
	if d.EmptyOutput {
		a.empty++
	}
	if d.FinishReason == "content_filter" {
		a.refused++
	}
	if d.ToolCallsCount > 0 {
		a.toolCalls++
	}
	if d.PromptTokens > 0 {
		a.promptTokens += int64(d.PromptTokens)
	}
	if d.CompletionTokens > 0 {
		a.completionTokens += int64(d.CompletionTokens)
	}
	if d.CachedTokens > 0 {
		a.cachedTokens += int64(d.CachedTokens)
	}
	if d.ReasoningTokens > 0 {
		a.reasoningTokens += int64(d.ReasoningTokens)
	}
	if tx.Success && !throttled {
		a.successful++
		if good {
			a.good++
		}
		e2e := d.E2EMs
		if e2e <= 0 {
			e2e = tx.DurationMs
		}
		a.e2e.add(e2e)
		if d.Stream {
			a.streamed++
			a.ttft.add(d.TTFTMs)
			a.tpot.add(d.TPOTMs)
			if d.ITLDistribution != nil && d.ITLDistribution.Count > 0 {
				a.itl.Merge(*d.ITLDistribution)
				if d.ITLDistribution.Max > 1000 {
					a.stallCount++
				}
				if d.ITLDistribution.Max > a.maxStallMs {
					a.maxStallMs = d.ITLDistribution.Max
				}
			}
		}
	}
}

func (a *archiveActionLLMAccumulator) finish(windowSeconds float64) *RunArchiveActionLLMStats {
	if a == nil || a.requests == 0 {
		return nil
	}
	reqF := float64(a.requests)
	stats := &RunArchiveActionLLMStats{
		Name:                 a.name,
		Requests:             a.requests,
		StreamedRequests:     a.streamed,
		SuccessfulRequests:   a.successful,
		ThrottledRequests:    a.throttled,
		TruncatedRequests:    a.truncated,
		EmptyRequests:        a.empty,
		RefusedRequests:      a.refused,
		ToolCallRequests:     a.toolCalls,
		GoodRequests:         a.good,
		GoodputPct:           float64(a.good) / reqF * 100.0,
		ThrottleRatePct:      float64(a.throttled) / reqF * 100.0,
		TruncationRatePct:    float64(a.truncated) / reqF * 100.0,
		PromptTokens:         a.promptTokens,
		CompletionTokens:     a.completionTokens,
		CachedTokens:         a.cachedTokens,
		ReasoningTokens:      a.reasoningTokens,
		MeanPromptTokens:     float64(a.promptTokens) / reqF,
		MeanCompletionTokens: float64(a.completionTokens) / reqF,
		TTFT:                 a.ttft.finish(),
		TPOT:                 a.tpot.finish(),
		E2E:                  a.e2e.finish(),
		ITL:                  finishArchiveITLSummary(a.itl, a.stallCount, a.maxStallMs),
	}
	if a.promptTokens > 0 {
		stats.CacheHitRatePct = float64(a.cachedTokens) / float64(a.promptTokens) * 100.0
	}
	if windowSeconds > 0 {
		stats.OutputTokensPerSec = float64(a.completionTokens) / windowSeconds
	}
	return stats
}

func finishArchiveITLSummary(dist protocol.DiagnosticDistribution, stallCount int64, maxStallMs float64) RunArchiveLLMITLSummary {
	if dist.Count == 0 {
		return RunArchiveLLMITLSummary{
			PercentileQuality: archivePercentileQualityUnavailable,
			StallCount:        stallCount,
			MaxStallMs:        maxStallMs,
		}
	}
	p50, _ := dist.Quantile(0.50)
	p90, _ := dist.Quantile(0.90)
	p95, _ := dist.Quantile(0.95)
	p99, _ := dist.Quantile(0.99)
	distCopy := dist
	return RunArchiveLLMITLSummary{
		Samples:           dist.Count,
		MeanMs:            dist.Sum / float64(dist.Count),
		MaxMs:             dist.Max,
		P50Ms:             p50,
		P90Ms:             p90,
		P95Ms:             p95,
		P99Ms:             p99,
		PercentileQuality: "approximate_log_histogram_10pct",
		StallCount:        stallCount,
		MaxStallMs:        maxStallMs,
		Distribution:      &distCopy,
	}
}

type archiveLLMBandAccumulator struct {
	band             string
	label            string
	requests         int64
	streamed         int64
	promptTokens     int64
	completionTokens int64
	ttft             *archiveLLMLatencyTracker
	tpot             *archiveLLMLatencyTracker
	e2e              *archiveLLMLatencyTracker
}

type archiveLLMGoldenAccumulator struct {
	name          string
	samples       int64
	baselineHash  string
	hashes        map[string]struct{}
	minTokens     int
	maxTokens     int
	invalidSchema bool
}

type archiveLLMAccumulator struct {
	budget               *archiveQuantileBudget
	sloTTFTMs            *float64
	sloTPOTMs            *float64
	sloE2EMs             *float64
	totalRequests        int64
	streamedRequests     int64
	successful           int64
	errors               int64
	throttled            int64
	truncated            int64
	empty                int64
	refused              int64
	toolCalls            int64
	fallback             int64
	retries              int64
	throttledTries       int64
	retryWaitMs          float64
	estimatedReqs        int64
	goodRequests         int64
	goodOutputTokens     int64
	promptTokens         int64
	completionTokens     int64
	totalTokens          int64
	cachedTokens         int64
	reasoningTokens      int64
	ttft                 *archiveLLMLatencyTracker
	firstContent         *archiveLLMLatencyTracker
	tpot                 *archiveLLMLatencyTracker
	e2e                  *archiveLLMLatencyTracker
	itl                  protocol.DiagnosticDistribution
	stallCount           int64
	maxStallMs           float64
	testID               string
	serverQueueSamples   int64
	serverQueueSum       float64
	serverPrefillSamples int64
	serverPrefillSum     float64
	serverDecodeSamples  int64
	serverDecodeSum      float64
	finishReasons        map[string]int64
	byAction             map[string]*archiveActionLLMAccumulator
	byModel              map[string]*archiveActionLLMAccumulator
	bands                map[string]*archiveLLMBandAccumulator
	goldenPrompts        map[string]*archiveLLMGoldenAccumulator
	sweepTxs             []TransactionEvent
}

const maxArchiveLLMSweepTransactions = 25000

var llmLengthBandOrder = []struct {
	band  string
	label string
}{
	{"short", "Short (<= 256 prompt tokens)"},
	{"medium", "Medium (257-1,024 prompt tokens)"},
	{"long", "Long (1,025-4,096 prompt tokens)"},
	{"very_long", "Very long (> 4,096 prompt tokens)"},
}

func classifyLLMPromptLengthBand(promptTokens int) string {
	switch {
	case promptTokens <= 256:
		return "short"
	case promptTokens <= 1024:
		return "medium"
	case promptTokens <= 4096:
		return "long"
	default:
		return "very_long"
	}
}

func newArchiveLLMAccumulator(manifest *RunArchiveManifest, budget *archiveQuantileBudget) *archiveLLMAccumulator {
	testID := ""
	if manifest != nil {
		testID = manifest.TestID
	}
	acc := &archiveLLMAccumulator{
		budget:        budget,
		testID:        testID,
		ttft:          newArchiveLLMLatencyTracker(budget),
		firstContent:  newArchiveLLMLatencyTracker(budget),
		tpot:          newArchiveLLMLatencyTracker(budget),
		e2e:           newArchiveLLMLatencyTracker(budget),
		finishReasons: make(map[string]int64),
		byAction:      make(map[string]*archiveActionLLMAccumulator),
		byModel:       make(map[string]*archiveActionLLMAccumulator),
		bands:         make(map[string]*archiveLLMBandAccumulator, len(llmLengthBandOrder)),
		goldenPrompts: make(map[string]*archiveLLMGoldenAccumulator),
	}
	for _, b := range llmLengthBandOrder {
		acc.bands[b.band] = &archiveLLMBandAccumulator{
			band:  b.band,
			label: b.label,
			ttft:  newArchiveLLMLatencyTracker(budget),
			tpot:  newArchiveLLMLatencyTracker(budget),
			e2e:   newArchiveLLMLatencyTracker(budget),
		}
	}
	if manifest != nil && manifest.Provenance.GoalConfig != nil {
		gc := manifest.Provenance.GoalConfig
		if gc.LLMSLOTTFTTargetMs != nil && *gc.LLMSLOTTFTTargetMs > 0 {
			v := *gc.LLMSLOTTFTTargetMs
			acc.sloTTFTMs = &v
		} else if gc.MaxLLMP95TTFTMs != nil && *gc.MaxLLMP95TTFTMs > 0 {
			v := *gc.MaxLLMP95TTFTMs
			acc.sloTTFTMs = &v
		}
		if gc.LLMSLOTPOTTargetMs != nil && *gc.LLMSLOTPOTTargetMs > 0 {
			v := *gc.LLMSLOTPOTTargetMs
			acc.sloTPOTMs = &v
		} else if gc.MaxLLMP95TPOTMs != nil && *gc.MaxLLMP95TPOTMs > 0 {
			v := *gc.MaxLLMP95TPOTMs
			acc.sloTPOTMs = &v
		}
		if gc.LLMSLOE2ETargetMs != nil && *gc.LLMSLOE2ETargetMs > 0 {
			v := *gc.LLMSLOE2ETargetMs
			acc.sloE2EMs = &v
		} else if gc.MaxEndToEndLatencyMs != nil && *gc.MaxEndToEndLatencyMs > 0 {
			v := *gc.MaxEndToEndLatencyMs
			acc.sloE2EMs = &v
		}
	}
	return acc
}

func (a *archiveLLMAccumulator) isGoodRequest(tx TransactionEvent, d *protocol.LLMDiagnostics) bool {
	if !tx.Success || d == nil || d.Throttled || tx.StatusCode == 429 || d.EmptyOutput || d.FinishReason == "content_filter" || d.HTTP200Error || d.SchemaFailed {
		return false
	}
	if a.sloTTFTMs != nil && *a.sloTTFTMs > 0 && d.Stream && (d.TTFTMs <= 0 || d.TTFTMs > *a.sloTTFTMs) {
		return false
	}
	if a.sloTPOTMs != nil && *a.sloTPOTMs > 0 && d.Stream && d.CompletionTokens > 1 && (d.TPOTMs <= 0 || d.TPOTMs > *a.sloTPOTMs) {
		return false
	}
	e2e := d.E2EMs
	if e2e <= 0 {
		e2e = tx.DurationMs
	}
	if a.sloE2EMs != nil && *a.sloE2EMs > 0 && (e2e <= 0 || e2e > *a.sloE2EMs) {
		return false
	}
	return true
}

func (a *archiveLLMAccumulator) merge(tx TransactionEvent) {
	if a == nil || tx.ServerTelemetry == nil || tx.ServerTelemetry.LLM == nil {
		return
	}
	if archiveEventPopulation(tx) != "action" || tx.BusinessTransaction {
		return
	}
	d := tx.ServerTelemetry.LLM
	a.totalRequests++
	if len(a.sweepTxs) < maxArchiveLLMSweepTransactions {
		a.sweepTxs = append(a.sweepTxs, tx)
	}
	throttled := d.Throttled || tx.StatusCode == 429
	if throttled {
		a.throttled++
	} else if !tx.Success {
		a.errors++
	}
	if d.Truncated {
		a.truncated++
	}
	if d.EmptyOutput {
		a.empty++
	}
	if d.FinishReason == "content_filter" {
		a.refused++
	}
	if d.ToolCallsCount > 0 {
		a.toolCalls++
	}
	if d.TokensEstimated {
		a.estimatedReqs++
	}
	if tx.Attempts > 1 {
		a.retries += int64(tx.Attempts - 1)
	}
	if tx.ThrottledAttempts > 0 {
		a.throttledTries += int64(tx.ThrottledAttempts)
	}
	if tx.RetryWaitMs > 0 {
		a.retryWaitMs += tx.RetryWaitMs
	}
	if d.PromptTokens > 0 {
		a.promptTokens += int64(d.PromptTokens)
	}
	if d.CompletionTokens > 0 {
		a.completionTokens += int64(d.CompletionTokens)
	}
	totalTok := d.TotalTokens
	if totalTok <= 0 && (d.PromptTokens > 0 || d.CompletionTokens > 0) {
		totalTok = d.PromptTokens + d.CompletionTokens
	}
	if totalTok > 0 {
		a.totalTokens += int64(totalTok)
	}
	if d.CachedTokens > 0 {
		a.cachedTokens += int64(d.CachedTokens)
	}
	if d.ReasoningTokens > 0 {
		a.reasoningTokens += int64(d.ReasoningTokens)
	}
	if reason := strings.TrimSpace(d.FinishReason); reason != "" && len(a.finishReasons) < 64 {
		a.finishReasons[reason]++
	}

	good := a.isGoodRequest(tx, d)
	// Success already fails a cut-off stream and, unless the script accepts
	// it, a length finish; truncation itself is only counted above.
	if tx.Success && !throttled {
		a.successful++
		if good {
			a.goodRequests++
			if d.CompletionTokens > 0 {
				a.goodOutputTokens += int64(d.CompletionTokens)
			}
		}
		e2e := d.E2EMs
		if e2e <= 0 {
			e2e = tx.DurationMs
		}
		a.e2e.add(e2e)
		if d.ServerQueueMs != nil && *d.ServerQueueMs >= 0 && !math.IsNaN(*d.ServerQueueMs) {
			a.serverQueueSamples++
			a.serverQueueSum += *d.ServerQueueMs
		}
		if d.ServerPrefillMs != nil && *d.ServerPrefillMs >= 0 && !math.IsNaN(*d.ServerPrefillMs) {
			a.serverPrefillSamples++
			a.serverPrefillSum += *d.ServerPrefillMs
		}
		if d.ServerDecodeMs != nil && *d.ServerDecodeMs >= 0 && !math.IsNaN(*d.ServerDecodeMs) {
			a.serverDecodeSamples++
			a.serverDecodeSum += *d.ServerDecodeMs
		}
		if d.Stream {
			a.streamedRequests++
			a.ttft.add(d.TTFTMs)
			a.firstContent.add(d.FirstVisibleTokenMs)
			a.tpot.add(d.TPOTMs)
			if d.ITLDistribution != nil && d.ITLDistribution.Count > 0 {
				a.itl.Merge(*d.ITLDistribution)
				if d.ITLDistribution.Max > 1000 {
					a.stallCount++
				}
				if d.ITLDistribution.Max > a.maxStallMs {
					a.maxStallMs = d.ITLDistribution.Max
				}
			}
		}
	}

	// Length band accumulation
	bandKey := classifyLLMPromptLengthBand(d.PromptTokens)
	if band := a.bands[bandKey]; band != nil {
		band.requests++
		if d.PromptTokens > 0 {
			band.promptTokens += int64(d.PromptTokens)
		}
		if d.CompletionTokens > 0 {
			band.completionTokens += int64(d.CompletionTokens)
		}
		if tx.Success && !throttled {
			e2e := d.E2EMs
			if e2e <= 0 {
				e2e = tx.DurationMs
			}
			band.e2e.add(e2e)
			if d.Stream {
				band.streamed++
				band.ttft.add(d.TTFTMs)
				band.tpot.add(d.TPOTMs)
			}
		}
	}

	// Per-action accumulation
	if tx.ActionName != "" && len(a.byAction) < maxArchiveAnalysisDimensions {
		actAcc := a.byAction[tx.ActionName]
		if actAcc == nil {
			actAcc = newArchiveActionLLMAccumulator(tx.ActionName, a.budget)
			a.byAction[tx.ActionName] = actAcc
		}
		actAcc.add(tx, d, good)
	}

	// Per-model accumulation
	modelName := strings.TrimSpace(d.Model)
	if modelName != "" && len(a.byModel) < maxArchiveAnalysisDimensions {
		modAcc := a.byModel[modelName]
		if modAcc == nil {
			modAcc = newArchiveActionLLMAccumulator(modelName, a.budget)
			a.byModel[modelName] = modAcc
		}
		modAcc.add(tx, d, good)
	}

	// Golden-prompt drift tracking
	if goldenKey := goldenPromptKey(tx); goldenKey != "" && tx.Success && !throttled && len(a.goldenPrompts) < 64 {
		g := a.goldenPrompts[goldenKey]
		if g == nil {
			g = &archiveLLMGoldenAccumulator{
				name:      goldenKey,
				hashes:    make(map[string]struct{}),
				minTokens: d.CompletionTokens,
				maxTokens: d.CompletionTokens,
			}
			a.goldenPrompts[goldenKey] = g
		}
		g.samples++
		if d.CompletionTokens < g.minTokens {
			g.minTokens = d.CompletionTokens
		}
		if d.CompletionTokens > g.maxTokens {
			g.maxTokens = d.CompletionTokens
		}
		hash := goldenPromptOutputHash(tx, d)
		if hash != "" {
			if g.baselineHash == "" {
				g.baselineHash = hash
			}
			g.hashes[hash] = struct{}{}
		}
		if d.EmptyOutput || d.Truncated || d.FinishReason == "content_filter" || d.SchemaFailed {
			g.invalidSchema = true
		}
	}
}

func goldenPromptKey(tx TransactionEvent) string {
	if len(tx.Extractions) > 0 {
		for _, k := range []string{"golden_prompt", "llm_golden_prompt", "golden_prompt_id"} {
			v := strings.TrimSpace(tx.Extractions[k])
			if v == "" || strings.EqualFold(v, "false") {
				continue
			}
			if strings.EqualFold(v, "true") {
				return tx.ActionName
			}
			return v
		}
	}
	if len(tx.PropertyDimensions) > 0 {
		v := strings.TrimSpace(tx.PropertyDimensions["golden_prompt"])
		if strings.EqualFold(v, "true") {
			return tx.ActionName
		}
		if v != "" && !strings.EqualFold(v, "false") {
			return v
		}
	}
	if tx.ServerTelemetry != nil && tx.ServerTelemetry.LLM != nil {
		if gp := strings.TrimSpace(tx.ServerTelemetry.LLM.GoldenPromptID); gp != "" && !strings.EqualFold(gp, "false") {
			if strings.EqualFold(gp, "true") {
				return tx.ActionName
			}
			return gp
		}
	}
	if strings.Contains(strings.ToLower(tx.ActionName), "golden") {
		return tx.ActionName
	}
	return ""
}

func goldenPromptOutputHash(tx TransactionEvent, d *protocol.LLMDiagnostics) string {
	if d != nil && d.OutputHash != 0 {
		return fmt.Sprintf("%016x", d.OutputHash)
	}
	if len(tx.Extractions) > 0 {
		for _, k := range []string{"output_hash", "llm_output_hash", "response", "reply", "answer"} {
			if v := strings.TrimSpace(tx.Extractions[k]); v != "" {
				sum := sha256.Sum256([]byte(v))
				return hex.EncodeToString(sum[:8])
			}
		}
	}
	return ""
}

func (a *archiveLLMAccumulator) finalize(windowSeconds float64) *RunArchiveLLMSummary {
	if a == nil || a.totalRequests == 0 {
		return nil
	}
	reqF := float64(a.totalRequests)
	summary := &RunArchiveLLMSummary{
		TotalRequests:           a.totalRequests,
		StreamedRequests:        a.streamedRequests,
		SuccessfulRequests:      a.successful,
		ErrorRequests:           a.errors,
		ThrottledRequests:       a.throttled,
		TruncatedRequests:       a.truncated,
		EmptyRequests:           a.empty,
		RefusedRequests:         a.refused,
		ToolCallRequests:        a.toolCalls,
		FallbackRequests:        a.fallback,
		TotalRetries:            a.retries,
		TotalThrottledAttempts:  a.throttledTries,
		TotalRetryWaitMs:        a.retryWaitMs,
		TokensEstimatedRequests: a.estimatedReqs,
		EstimatedTokenSharePct:  float64(a.estimatedReqs) / reqF * 100.0,
		GoodRequests:            a.goodRequests,
		GoodputPct:              float64(a.goodRequests) / reqF * 100.0,
		SLOTTFTMs:               a.sloTTFTMs,
		SLOTPOTMs:               a.sloTPOTMs,
		SLOE2EMs:                a.sloE2EMs,
		ThrottleRatePct:         float64(a.throttled) / reqF * 100.0,
		TruncationRatePct:       float64(a.truncated) / reqF * 100.0,
		EmptyRatePct:            float64(a.empty) / reqF * 100.0,
		RefusalRatePct:          float64(a.refused) / reqF * 100.0,
		ErrorRatePct:            float64(a.errors) / reqF * 100.0,
		PromptTokens:            a.promptTokens,
		CompletionTokens:        a.completionTokens,
		TotalTokens:             a.totalTokens,
		CachedTokens:            a.cachedTokens,
		ReasoningTokens:         a.reasoningTokens,
		MeanPromptTokens:        float64(a.promptTokens) / reqF,
		MeanCompletionTokens:    float64(a.completionTokens) / reqF,
		TTFT:                    a.ttft.finish(),
		FirstContent:            a.firstContent.finish(),
		TPOT:                    a.tpot.finish(),
		E2E:                     a.e2e.finish(),
		ITL:                     finishArchiveITLSummary(a.itl, a.stallCount, a.maxStallMs),
	}
	if a.promptTokens > 0 {
		summary.CacheHitRatePct = float64(a.cachedTokens) / float64(a.promptTokens) * 100.0
	}
	if windowSeconds > 0 {
		summary.RequestsPerSec = reqF / windowSeconds
		summary.OutputTokensPerSec = float64(a.completionTokens) / windowSeconds
		summary.InputTokensPerSec = float64(a.promptTokens) / windowSeconds
		summary.TotalTokensPerSec = float64(a.totalTokens) / windowSeconds
		summary.GoodOutputTokensPerSec = float64(a.goodOutputTokens) / windowSeconds
	}
	if len(a.finishReasons) > 0 {
		summary.FinishReasons = a.finishReasons
	}
	for _, b := range llmLengthBandOrder {
		bandAcc := a.bands[b.band]
		if bandAcc == nil || bandAcc.requests == 0 {
			continue
		}
		bReqF := float64(bandAcc.requests)
		summary.ByLengthBand = append(summary.ByLengthBand, RunArchiveLLMLengthBandStats{
			Band:                 bandAcc.band,
			Label:                bandAcc.label,
			Requests:             bandAcc.requests,
			StreamedRequests:     bandAcc.streamed,
			PromptTokens:         bandAcc.promptTokens,
			CompletionTokens:     bandAcc.completionTokens,
			MeanPromptTokens:     float64(bandAcc.promptTokens) / bReqF,
			MeanCompletionTokens: float64(bandAcc.completionTokens) / bReqF,
			TTFT:                 bandAcc.ttft.finish(),
			TPOT:                 bandAcc.tpot.finish(),
			E2E:                  bandAcc.e2e.finish(),
		})
	}
	if len(a.byAction) > 0 {
		summary.ByAction = make(map[string]*RunArchiveActionLLMStats, len(a.byAction))
		for k, acc := range a.byAction {
			if finished := acc.finish(windowSeconds); finished != nil {
				summary.ByAction[k] = finished
			}
		}
	}
	if len(a.byModel) > 0 {
		summary.ByModel = make(map[string]*RunArchiveActionLLMStats, len(a.byModel))
		for k, acc := range a.byModel {
			if finished := acc.finish(windowSeconds); finished != nil {
				summary.ByModel[k] = finished
			}
		}
	}
	if len(a.goldenPrompts) > 0 {
		drift := &RunArchiveLLMGoldenPromptDrift{
			CheckedPrompts: len(a.goldenPrompts),
			Status:         "pass",
		}
		names := make([]string, 0, len(a.goldenPrompts))
		for name := range a.goldenPrompts {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			g := a.goldenPrompts[name]
			ratio := 0.0
			if g.minTokens > 0 && g.maxTokens > g.minTokens {
				ratio = float64(g.maxTokens-g.minTokens) / float64(g.minTokens)
			}
			drifted := len(g.hashes) > 1 || ratio > 0.20 || g.invalidSchema
			detail := "Deterministic output remained stable across load levels."
			if drifted {
				drift.DriftedPrompts++
				drift.Status = "drift_detected"
				switch {
				case g.invalidSchema:
					detail = "Truncation, refusal, or empty output observed under load."
				case len(g.hashes) > 1:
					detail = fmt.Sprintf("Output hash varied across %d distinct responses.", len(g.hashes))
				default:
					detail = fmt.Sprintf("Completion token length drifted by %.1f%% (%d to %d tokens).", ratio*100, g.minTokens, g.maxTokens)
				}
			}
			drift.Items = append(drift.Items, RunArchiveLLMGoldenPromptItem{
				Name:                g.name,
				Samples:             g.samples,
				BaselineHash:        g.baselineHash,
				ObservedHashes:      len(g.hashes),
				MinCompletionTokens: g.minTokens,
				MaxCompletionTokens: g.maxTokens,
				LengthDriftRatio:    ratio,
				Drifted:             drifted,
				Detail:              detail,
			})
		}
		summary.GoldenPromptDrift = drift
	}
	decompSummary := &engine.LLMSummary{
		Requests:             a.totalRequests,
		StreamedRequests:     a.streamedRequests,
		AvgTTFTMs:            summary.TTFT.MeanMs,
		AvgTPOTMs:            summary.TPOT.MeanMs,
		AvgE2EMs:             summary.E2E.MeanMs,
		ServerQueueSamples:   a.serverQueueSamples,
		ServerQueueSumMs:     a.serverQueueSum,
		ServerPrefillSamples: a.serverPrefillSamples,
		ServerPrefillSumMs:   a.serverPrefillSum,
		ServerDecodeSamples:  a.serverDecodeSamples,
		ServerDecodeSumMs:    a.serverDecodeSum,
	}
	summary.LatencyDecomposition = BuildLLMLatencyDecomposition(a.testID, decompSummary, summary.RequestsPerSec, nil)
	return summary
}

func pdfLLMPerformanceLines(d *runArchiveHTMLReportData, lines *[]string) {
	pdfHeading(lines, reportSectionTitle("llm_performance"))
	if d == nil || d.Report == nil || d.Report.LLM == nil || d.Report.LLM.TotalRequests == 0 {
		*lines = append(*lines, "No LLM inference telemetry was captured for this run.", "")
		return
	}
	llm := d.Report.LLM
	*lines = append(*lines,
		fmt.Sprintf("Prefill vs decode summary: %d LLM requests (%d streamed, %d successful, %d throttled, %d truncated).",
			llm.TotalRequests, llm.StreamedRequests, llm.SuccessfulRequests, llm.ThrottledRequests, llm.TruncatedRequests),
		fmt.Sprintf("Prefill (TTFT): mean %.1f ms, p50 %.1f ms, p90 %.1f ms, p95 %.1f ms, p99 %.1f ms.",
			llm.TTFT.MeanMs, llm.TTFT.P50Ms, llm.TTFT.P90Ms, llm.TTFT.P95Ms, llm.TTFT.P99Ms),
		fmt.Sprintf("Decode (TPOT / ITL): TPOT p50 %.1f ms, p95 %.1f ms, p99 %.1f ms; ITL approx p95 %.1f ms, p99 %.1f ms (%d stalls).",
			llm.TPOT.P50Ms, llm.TPOT.P95Ms, llm.TPOT.P99Ms, llm.ITL.P95Ms, llm.ITL.P99Ms, llm.ITL.StallCount),
		fmt.Sprintf("Token throughput & SLO goodput: %.1f output tok/s, %.1f input tok/s, goodput %.2f%% (throttle %.2f%%, truncation %.2f%%).",
			llm.OutputTokensPerSec, llm.InputTokensPerSec, llm.GoodputPct, llm.ThrottleRatePct, llm.TruncationRatePct),
	)
	if len(llm.ByLengthBand) > 0 {
		*lines = append(*lines, "|Prompt length band|Requests|Mean prompt tok|Mean output tok|TTFT p50 ms|TTFT p95 ms|TPOT p95 ms|E2E p95 ms|")
		for _, band := range llm.ByLengthBand {
			*lines = append(*lines, fmt.Sprintf("|%s|%d|%.0f|%.0f|%.1f|%.1f|%.1f|%.1f|",
				band.Label, band.Requests, band.MeanPromptTokens, band.MeanCompletionTokens,
				band.TTFT.P50Ms, band.TTFT.P95Ms, band.TPOT.P95Ms, band.E2E.P95Ms))
		}
	}
	if llm.GoldenPromptDrift != nil && len(llm.GoldenPromptDrift.Items) > 0 {
		*lines = append(*lines, fmt.Sprintf("Golden-prompt drift check: %s (%d/%d drifted).",
			llm.GoldenPromptDrift.Status, llm.GoldenPromptDrift.DriftedPrompts, llm.GoldenPromptDrift.CheckedPrompts))
	}
	*lines = append(*lines, "")
}

func formatPDFFloat(v float64) string {
	return fmt.Sprintf("%.2f", v)
}

func csvLLMPerformanceRows(d *runArchiveHTMLReportData, write reportCSVWriteFunc) error {
	if d == nil || d.Report == nil || d.Report.LLM == nil || d.Report.LLM.TotalRequests == 0 {
		return write("llm_performance", "", "status", "not_captured")
	}
	llm := d.Report.LLM
	pairs := [][2]string{
		{"total_requests", formatPDFInt(llm.TotalRequests)},
		{"streamed_requests", formatPDFInt(llm.StreamedRequests)},
		{"successful_requests", formatPDFInt(llm.SuccessfulRequests)},
		{"throttled_requests", formatPDFInt(llm.ThrottledRequests)},
		{"truncated_requests", formatPDFInt(llm.TruncatedRequests)},
		{"goodput_pct", formatPDFFloat(llm.GoodputPct)},
		{"throttle_rate_pct", formatPDFFloat(llm.ThrottleRatePct)},
		{"truncation_rate_pct", formatPDFFloat(llm.TruncationRatePct)},
		{"output_tokens_per_sec", formatPDFFloat(llm.OutputTokensPerSec)},
		{"input_tokens_per_sec", formatPDFFloat(llm.InputTokensPerSec)},
		{"ttft_p50_ms", formatPDFFloat(llm.TTFT.P50Ms)},
		{"ttft_p90_ms", formatPDFFloat(llm.TTFT.P90Ms)},
		{"ttft_p95_ms", formatPDFFloat(llm.TTFT.P95Ms)},
		{"ttft_p99_ms", formatPDFFloat(llm.TTFT.P99Ms)},
		{"tpot_p50_ms", formatPDFFloat(llm.TPOT.P50Ms)},
		{"tpot_p90_ms", formatPDFFloat(llm.TPOT.P90Ms)},
		{"tpot_p95_ms", formatPDFFloat(llm.TPOT.P95Ms)},
		{"tpot_p99_ms", formatPDFFloat(llm.TPOT.P99Ms)},
		{"itl_p95_ms", formatPDFFloat(llm.ITL.P95Ms)},
		{"itl_p99_ms", formatPDFFloat(llm.ITL.P99Ms)},
		{"e2e_p95_ms", formatPDFFloat(llm.E2E.P95Ms)},
	}
	for _, pair := range pairs {
		if err := write("llm_performance", "overall", pair[0], pair[1]); err != nil {
			return err
		}
	}
	for _, band := range llm.ByLengthBand {
		if err := write("llm_performance", "band:"+band.Band, "requests", formatPDFInt(band.Requests)); err != nil {
			return err
		}
		if err := write("llm_performance", "band:"+band.Band, "ttft_p95_ms", formatPDFFloat(band.TTFT.P95Ms)); err != nil {
			return err
		}
		if err := write("llm_performance", "band:"+band.Band, "tpot_p95_ms", formatPDFFloat(band.TPOT.P95Ms)); err != nil {
			return err
		}
	}
	return nil
}
