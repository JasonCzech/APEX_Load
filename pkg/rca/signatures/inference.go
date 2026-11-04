package signatures

// KVCacheSaturation: the inference engine behind the affected dependencies ran
// out of KV-cache blocks or began preempting sequences before the symptom.
var KVCacheSaturation = Signature{
	Name:          "kv_cache_saturation",
	Required:      []string{KindKVCache, KindPreemption},
	Indicators:    []Indicator{{KindKVCache, Increase}, {KindPreemption, Increase}},
	Symptoms:      symptomsOfInference,
	Competing:     []string{"gpu_compute_saturation", "inference_queueing", "resource_pressure"},
	Falsification: "KV-cache usage and sequence preemptions on the inference engine stay within their baselines through the window",
}

// GPUComputeSaturation: GPU compute or HBM memory bandwidth saturated on the
// inference accelerator before the symptom.
var GPUComputeSaturation = Signature{
	Name:          "gpu_compute_saturation",
	Required:      []string{KindGPUCompute},
	Indicators:    []Indicator{{KindGPUCompute, Increase}},
	Symptoms:      symptomsOfInference,
	Competing:     []string{"kv_cache_saturation", "inference_queueing", "resource_pressure"},
	Falsification: "GPU compute and memory-bandwidth utilization on the inference hosts stay within their baselines through the window",
}

// InferenceQueueing: requests accumulated in the inference scheduler waiting
// queue before the symptom.
var InferenceQueueing = Signature{
	Name:          "inference_queueing",
	Required:      []string{KindInferenceQueue},
	Indicators:    []Indicator{{KindInferenceQueue, Increase}},
	Symptoms:      symptomsOfInference,
	Competing:     []string{"kv_cache_saturation", "gpu_compute_saturation", "rate_limited"},
	Falsification: "inference scheduler waiting queue depth and queue residence time stay within their baselines through the window",
}

// RateLimited: the LLM endpoint or managed deployment throttled requests (429,
// token-rate limit, or PTU utilization saturation) before or at the symptom.
var RateLimited = Signature{
	Name:          "rate_limited",
	Required:      []string{KindRateLimit},
	Indicators:    []Indicator{{KindRateLimit, Increase}},
	Symptoms:      symptomsOfInference,
	Competing:     []string{"inference_queueing", "pool_exhaustion", "retries"},
	Falsification: "provider rate-limit, throttle and PTU utilization indicators stay within their baselines through the window",
}
