package llmfixture

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SimulatorConfig configures the continuous-batching vLLM simulator (WP0.2 / WP3.0).
type SimulatorConfig struct {
	MaxNumSeqs          int           // e.g. 16; requests beyond this wait in queue
	KVCacheCapacityToks int           // e.g. 4096; exceeding triggers preemption + decode penalty
	BasePrefill         time.Duration // base prefill latency per request
	PrefillPerToken     time.Duration // additional prefill latency per prompt token
	BaseDecodePerToken  time.Duration // base ITL at batch size 1
	DecodeBatchSlope    time.Duration // additional ITL per concurrent running sequence
}

// SimulatorSnapshot reports point-in-time simulator state for assertions and Prometheus export.
type SimulatorSnapshot struct {
	NumRequestsRunning int
	NumRequestsWaiting int
	KVCacheUsageRatio  float64 // 0..1
	GPUComputeUtilPct  float64 // 0..100
	GPUMemCopyUtilPct  float64 // 0..100
	PreemptionsTotal   int64
	PromptTokensTotal  int64
	GenTokensTotal     int64
}

// BatchSimulator models a continuous-batching LLM inference engine (like vLLM).
type BatchSimulator struct {
	cfg       SimulatorConfig
	slots     chan struct{}
	mu        sync.Mutex
	running   int
	waiting   int
	kvTokens  int
	preempts  atomic.Int64
	promptTok atomic.Int64
	genTok    atomic.Int64

	// Histogram / summary accumulators for Prometheus metrics
	ttftSumSec  float64
	ttftCount   int64
	tpotSumSec  float64
	tpotCount   int64
	e2eSumSec   float64
	e2eCount    int64
	queueSumSec float64
	queueCount  int64
}

// NewBatchSimulator constructs a continuous-batching simulator with sensible defaults.
func NewBatchSimulator(cfg SimulatorConfig) *BatchSimulator {
	if cfg.MaxNumSeqs <= 0 {
		cfg.MaxNumSeqs = 16
	}
	if cfg.KVCacheCapacityToks <= 0 {
		cfg.KVCacheCapacityToks = cfg.MaxNumSeqs * 256
	}
	if cfg.BasePrefill <= 0 {
		cfg.BasePrefill = 10 * time.Millisecond
	}
	if cfg.PrefillPerToken <= 0 {
		cfg.PrefillPerToken = 100 * time.Microsecond
	}
	if cfg.BaseDecodePerToken <= 0 {
		cfg.BaseDecodePerToken = 4 * time.Millisecond
	}
	if cfg.DecodeBatchSlope <= 0 {
		cfg.DecodeBatchSlope = 250 * time.Microsecond
	}
	return &BatchSimulator{
		cfg:   cfg,
		slots: make(chan struct{}, cfg.MaxNumSeqs),
	}
}

// AdmitAndSchedule waits for a batch slot, updates KV-cache occupancy and preemption counters,
// and returns the queue wait, prefill duration, per-token ITLs, whether preemption occurred,
// and a release function to call when the request completes.
func (b *BatchSimulator) AdmitAndSchedule(
	ctx context.Context,
	promptTokens int,
	outputTokens int,
) (queueWait time.Duration, prefill time.Duration, itls []time.Duration, preempted bool, release func(), err error) {
	enqueuedAt := time.Now()
	b.mu.Lock()
	b.waiting++
	b.mu.Unlock()

	select {
	case b.slots <- struct{}{}:
	case <-ctx.Done():
		b.mu.Lock()
		b.waiting--
		b.mu.Unlock()
		return 0, 0, nil, false, func() {}, ctx.Err()
	}

	queueWait = time.Since(enqueuedAt)
	seqTokens := max(1, promptTokens+outputTokens)

	b.mu.Lock()
	b.waiting--
	b.running++
	b.kvTokens += seqTokens
	activeBatch := b.running
	kvRatio := float64(b.kvTokens) / float64(max(1, b.cfg.KVCacheCapacityToks))
	if kvRatio > 1.0 || activeBatch >= b.cfg.MaxNumSeqs {
		// When KV demand exceeds capacity or batch is saturated at max_num_seqs,
		// record preemption pressure and apply KV swap penalty.
		if kvRatio > 1.0 {
			preempted = true
			b.preempts.Add(1)
		}
	}
	b.mu.Unlock()

	prefill = b.cfg.BasePrefill + time.Duration(promptTokens)*b.cfg.PrefillPerToken
	if preempted {
		prefill += 15 * time.Millisecond
	}

	if outputTokens > 1 {
		itls = make([]time.Duration, outputTokens-1)
		stepITL := b.cfg.BaseDecodePerToken + time.Duration(max(0, activeBatch-1))*b.cfg.DecodeBatchSlope
		if preempted {
			stepITL += 5 * time.Millisecond
		}
		for i := range itls {
			itls[i] = stepITL
		}
	}

	var once sync.Once
	release = func() {
		once.Do(func() {
			b.mu.Lock()
			b.running--
			b.kvTokens -= seqTokens
			if b.kvTokens < 0 {
				b.kvTokens = 0
			}
			b.mu.Unlock()
			<-b.slots
		})
	}
	return queueWait, prefill, itls, preempted, release, nil
}

// ObserveCompleted records completed request durations for Prometheus scrape output.
func (b *BatchSimulator) ObserveCompleted(queueWait, ttft, tpot, e2e time.Duration, promptToks, genToks int) {
	b.promptTok.Add(int64(promptToks))
	b.genTok.Add(int64(genToks))
	b.mu.Lock()
	defer b.mu.Unlock()
	b.queueSumSec += queueWait.Seconds()
	b.queueCount++
	b.ttftSumSec += ttft.Seconds()
	b.ttftCount++
	if tpot > 0 {
		b.tpotSumSec += tpot.Seconds()
		b.tpotCount++
	}
	b.e2eSumSec += e2e.Seconds()
	b.e2eCount++
}

// Snapshot returns the current simulator gauges and counters.
func (b *BatchSimulator) Snapshot() SimulatorSnapshot {
	b.mu.Lock()
	defer b.mu.Unlock()
	kvRatio := math.Min(1.0, float64(b.kvTokens)/float64(max(1, b.cfg.KVCacheCapacityToks)))
	batchRatio := math.Min(1.0, float64(b.running)/float64(max(1, b.cfg.MaxNumSeqs)))
	return SimulatorSnapshot{
		NumRequestsRunning: b.running,
		NumRequestsWaiting: b.waiting,
		KVCacheUsageRatio:  kvRatio,
		GPUComputeUtilPct:  math.Min(100.0, batchRatio*95.0),
		GPUMemCopyUtilPct:  math.Min(100.0, kvRatio*90.0),
		PreemptionsTotal:   b.preempts.Load(),
		PromptTokensTotal:  b.promptTok.Load(),
		GenTokensTotal:     b.genTok.Load(),
	}
}

// PrometheusMetrics formats the current simulator state using real vLLM (both v0.4+ and v0.6+ KV names)
// and DCGM Prometheus metric names.
func (b *BatchSimulator) PrometheusMetrics() string {
	snap := b.Snapshot()
	b.mu.Lock()
	ttftSum, ttftCnt := b.ttftSumSec, b.ttftCount
	tpotSum, tpotCnt := b.tpotSumSec, b.tpotCount
	e2eSum, e2eCnt := b.e2eSumSec, b.e2eCount
	qSum, qCnt := b.queueSumSec, b.queueCount
	b.mu.Unlock()

	var sb strings.Builder
	fmt.Fprintf(&sb, "# TYPE vllm:num_requests_running gauge\nvllm:num_requests_running %d\n", snap.NumRequestsRunning)
	fmt.Fprintf(&sb, "# TYPE vllm:num_requests_waiting gauge\nvllm:num_requests_waiting %d\n", snap.NumRequestsWaiting)
	fmt.Fprintf(&sb, "# TYPE vllm:gpu_cache_usage_perc gauge\nvllm:gpu_cache_usage_perc %.4f\n", snap.KVCacheUsageRatio)
	fmt.Fprintf(&sb, "# TYPE vllm:kv_cache_usage_perc gauge\nvllm:kv_cache_usage_perc %.4f\n", snap.KVCacheUsageRatio)
	fmt.Fprintf(&sb, "# TYPE vllm:num_preemptions_total counter\nvllm:num_preemptions_total %d\n", snap.PreemptionsTotal)
	fmt.Fprintf(&sb, "# TYPE vllm:prompt_tokens_total counter\nvllm:prompt_tokens_total %d\n", snap.PromptTokensTotal)
	fmt.Fprintf(&sb, "# TYPE vllm:generation_tokens_total counter\nvllm:generation_tokens_total %d\n", snap.GenTokensTotal)
	fmt.Fprintf(&sb, "# TYPE vllm:time_to_first_token_seconds summary\nvllm:time_to_first_token_seconds_sum %.6f\nvllm:time_to_first_token_seconds_count %d\n", ttftSum, ttftCnt)
	fmt.Fprintf(&sb, "# TYPE vllm:time_per_output_token_seconds summary\nvllm:time_per_output_token_seconds_sum %.6f\nvllm:time_per_output_token_seconds_count %d\n", tpotSum, tpotCnt)
	fmt.Fprintf(&sb, "# TYPE vllm:e2e_request_latency_seconds summary\nvllm:e2e_request_latency_seconds_sum %.6f\nvllm:e2e_request_latency_seconds_count %d\n", e2eSum, e2eCnt)
	fmt.Fprintf(&sb, "# TYPE vllm:request_queue_time_seconds summary\nvllm:request_queue_time_seconds_sum %.6f\nvllm:request_queue_time_seconds_count %d\n", qSum, qCnt)
	fmt.Fprintf(&sb, "# TYPE DCGM_FI_DEV_GPU_UTIL gauge\nDCGM_FI_DEV_GPU_UTIL{gpu=\"0\"} %.2f\n", snap.GPUComputeUtilPct)
	fmt.Fprintf(&sb, "# TYPE DCGM_FI_DEV_MEM_COPY_UTIL gauge\nDCGM_FI_DEV_MEM_COPY_UTIL{gpu=\"0\"} %.2f\n", snap.GPUMemCopyUtilPct)
	return sb.String()
}
