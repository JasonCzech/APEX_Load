package signatures

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"unicode"
)

// Queue kinds Little's law reads at a broker hop.
const (
	KindQueueDepth    = "queue_depth"
	KindArrivalRate   = "arrival_rate"
	KindResidenceTime = "residence_time"
)

// classifiers are tried in order, the most specific first, so a JVM GC
// metric is gc rather than memory and a consumer-lag metric is not a queue
// depth. Each matches whole name tokens or a phrase of joined tokens.
var classifiers = []struct {
	kind    string
	tokens  []string
	phrases []string
}{
	{KindDeadLetters, []string{"dlq", "deadletter", "deadletters"}, []string{"dead_letter"}},
	{KindRebalance, []string{"rebalance", "rebalances", "rebalancing"}, nil},
	{KindConsumerLag, []string{"lag"}, []string{"consumer_lag", "records_lag", "offset_lag"}},
	{KindRetries, []string{"retry", "retries", "retried", "redelivered", "redeliveries"}, nil},
	{KindPreemption, []string{"preemption", "preemptions"}, []string{"num_preemptions"}},
	{KindKVCache, nil, []string{"kv_cache", "gpu_cache_usage", "cpu_cache_usage", "cache_config", "block_table"}},
	{KindGPUCompute, []string{"hbm"}, []string{"dcgm_fi_dev_gpu_util", "dcgm_fi_dev_mem_copy_util", "mem_copy_util", "sm_active", "tensor_active", "dram_active", "gpu_utilization", "gpu_util"}},
	{KindInferenceQueue, nil, []string{"num_requests_waiting", "request_queue_time", "inference_queue"}},
	{KindRateLimit, []string{"ratelimit", "throttled", "429"}, []string{"rate_limit", "ptu_utilization", "provisioned_managed_utilization"}},
	{KindGC, []string{"gc", "garbage"}, []string{"suspension_time", "collection_time"}},
	{KindScaling, []string{"replica", "replicas", "restart", "restarts", "oom", "coldstart", "scaling"}, []string{"cold_start", "oom_kill"}},
	{KindResidenceTime, []string{"residence", "sojourn"}, []string{"time_in_queue", "message_age", "queue_time"}},
	{KindArrivalRate, []string{"enqueue", "enqueued", "arrival", "arrivals", "incoming"}, []string{"publish_rate", "produce_rate"}},
	{KindQueueDepth, []string{"depth", "backlog", "queued"}, []string{"queue_size", "messages_ready", "queue_length"}},
	{KindPool, []string{"pool"}, []string{"active_connections", "connections_active", "pending_acquire", "threads_busy"}},
	{KindDBWait, []string{"deadlock", "deadlocks"}, []string{"lock_wait", "db_wait", "row_lock", "buffer_wait", "query_wait"}},
	{KindCPU, []string{"cpu", "processor"}, nil},
	{KindMemory, []string{"memory", "mem", "heap", "rss"}, nil},
}

// classifyInference disambiguates LLM serving, GPU and rate-limit metric
// families before the general classifier table, preventing tokens such as
// "mem" in DCGM_FI_DEV_MEM_COPY_UTIL, "cpu" in vllm:cpu_cache_usage_perc or
// "queue_time" in request_queue_time from falling into host memory, host CPU
// or broker residence-time kinds.
func classifyInference(set map[string]bool, joined string) string {
	switch {
	case set["preemption"] || set["preemptions"] || strings.Contains(joined, "_num_preemptions_"):
		return KindPreemption
	case strings.Contains(joined, "_kv_cache_") || strings.Contains(joined, "_gpu_cache_usage_") ||
		strings.Contains(joined, "_cpu_cache_usage_") || strings.Contains(joined, "_cache_config_") ||
		strings.Contains(joined, "_block_table_"):
		return KindKVCache
	case strings.Contains(joined, "_dcgm_fi_dev_gpu_util_") || strings.Contains(joined, "_dcgm_fi_dev_mem_copy_util_") ||
		strings.Contains(joined, "_mem_copy_util_") || strings.Contains(joined, "_sm_active_") ||
		strings.Contains(joined, "_tensor_active_") || strings.Contains(joined, "_dram_active_") ||
		strings.Contains(joined, "_gpu_utilization_") || strings.Contains(joined, "_gpu_util_") || set["hbm"]:
		return KindGPUCompute
	case strings.Contains(joined, "_num_requests_waiting_") || strings.Contains(joined, "_request_queue_time_") ||
		strings.Contains(joined, "_inference_queue_"):
		return KindInferenceQueue
	case set["ratelimit"] || set["throttled"] || set["429"] || strings.Contains(joined, "_rate_limit_") ||
		strings.Contains(joined, "_ptu_utilization_") || strings.Contains(joined, "_provisioned_managed_utilization_"):
		return KindRateLimit
	case set["ttft"] || strings.Contains(joined, "_time_to_first_token_"):
		return KindTTFT
	case set["tpot"] || strings.Contains(joined, "_time_per_output_token_") || strings.Contains(joined, "_inter_token_latency_"):
		return KindTPOT
	}
	inferenceFamily := set["vllm"] || set["tgi"] || set["nim"] || set["triton"] || set["trt"] ||
		strings.Contains(joined, "_nv_trt_llm_") || strings.Contains(joined, "_nv_inference_") || strings.Contains(joined, "_gen_ai_")
	if inferenceFamily {
		if strings.Contains(joined, "_queue_size_") || strings.Contains(joined, "_time_in_queue_") ||
			strings.Contains(joined, "_queue_time_") || strings.Contains(joined, "_queue_duration_") || set["waiting"] {
			return KindInferenceQueue
		}
		if strings.Contains(joined, "_cache_usage_") || strings.Contains(joined, "_kv_blocks_") {
			return KindKVCache
		}
	}
	return ""
}

// Classify names the evidence kind a provider series measures from its
// metric name, or "" when no kind applies. It is a deterministic keyword
// match; the evaluation corpus checks it with the signatures.
func Classify(name string) string {
	tokens := strings.FieldsFunc(strings.ToLower(name), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	joined := "_" + strings.Join(tokens, "_") + "_"
	set := map[string]bool{}
	for _, t := range tokens {
		set[t] = true
	}
	if kind := classifyInference(set, joined); kind != "" {
		return kind
	}
	for _, c := range classifiers {
		for _, t := range c.tokens {
			if set[t] {
				return c.kind
			}
		}
		for _, p := range c.phrases {
			if strings.Contains(joined, "_"+p+"_") {
				return c.kind
			}
		}
	}
	return ""
}

// Fingerprint identifies the signature set, its scoring weights and the
// classifier exactly, so a change to any value changes every analysis's
// frozen inputs even when Version is not bumped. A change to the judging
// logic itself still bumps Version.
func Fingerprint() string {
	type described struct {
		Name, Falsification string
		Required, Competing []string
		Indicators          []Indicator
		Symptoms            []string
		Phases              []PhaseIndicator
	}
	var set []described
	for _, s := range All() {
		set = append(set, described{s.Name, s.Falsification, s.Required, s.Competing, s.Indicators, s.Symptoms, s.Phases})
	}
	// The classifier table's fields are unexported, which JSON would skip.
	type keywords struct {
		Kind            string
		Tokens, Phrases []string
	}
	var table []keywords
	for _, c := range classifiers {
		table = append(table, keywords{c.kind, c.tokens, c.phrases})
	}
	weights := []float64{weightUnconnected, weightUnordered, penaltyFollows, penaltyFlat, penaltyFlatLoose, bonusLoadStep, bonusPhase, penaltyPhase}
	body, _ := json.Marshal(struct {
		Version     string
		Signatures  []described
		Classifiers []keywords
		Weights     []float64
	}{Version, set, table, weights})
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:6])
}
