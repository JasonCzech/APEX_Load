package api

import (
	"sort"
	"strings"
)

const gpuInferenceMetricPrefix = "llm.gpu."

func gpuInferenceMetricContract() []MonitoringMetricSpec {
	labels := gpuInferenceExpectedLabels()
	return []MonitoringMetricSpec{
		{
			CanonicalName:    "llm.gpu.kv_cache_usage_pct",
			PrometheusMetric: "vllm:kv_cache_usage_perc",
			Unit:             "percent",
			Required:         true,
			Description:      "KV-cache block utilization percentage used to correlate prefill/decode queuing and sequence preemption.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "llm.gpu.compute_utilization_pct",
			PrometheusMetric: "DCGM_FI_DEV_GPU_UTIL",
			Unit:             "percent",
			Required:         true,
			Description:      "Accelerator compute utilization percentage used to identify prefill or batch-decode compute saturation.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "llm.gpu.memory_bandwidth_pct",
			PrometheusMetric: "DCGM_FI_DEV_MEM_COPY_UTIL",
			Unit:             "percent",
			Required:         true,
			Description:      "GPU HBM memory-bandwidth utilization percentage used to diagnose memory-bandwidth-bound token decode (TPOT/ITL).",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "llm.gpu.requests_waiting",
			PrometheusMetric: "vllm:num_requests_waiting",
			Unit:             "requests",
			Required:         true,
			Description:      "Requests queued in the inference scheduler waiting for KV-cache or batch slots.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "llm.gpu.preemptions_per_sec",
			PrometheusMetric: "vllm:num_preemptions_total",
			Unit:             "events_per_second",
			Required:         true,
			Description:      "Sequence preemption rate caused by KV-cache pressure during continuous batching.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "llm.gpu.queue_time_ms",
			PrometheusMetric: "vllm:request_queue_time_seconds",
			Unit:             "milliseconds",
			Required:         false,
			Description:      "Server-reported scheduler queue residence time before prefill begins.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "llm.gpu.prefill_time_ms",
			PrometheusMetric: "vllm:request_prefill_time_seconds",
			Unit:             "milliseconds",
			Required:         false,
			Description:      "Server-reported prompt prefill duration used for client vs. server TTFT decomposition.",
			ExpectedLabels:   labels,
		},
		{
			CanonicalName:    "llm.gpu.decode_time_ms",
			PrometheusMetric: "vllm:request_decode_time_seconds",
			Unit:             "milliseconds",
			Required:         false,
			Description:      "Server-reported token decode duration used for client vs. server TPOT decomposition.",
			ExpectedLabels:   labels,
		},
	}
}

func gpuInferenceMonitoringProfile() MonitoringReadinessProfile {
	required := gpuInferenceCanonicalMetricNames(true)
	return MonitoringReadinessProfile{
		ID:              "gpu-inference",
		Name:            "GPU Inference Serving (vLLM / TGI / NIM / Triton / DCGM)",
		Provider:        "prometheus",
		Status:          "awaiting_metric_source",
		ResourceType:    "gpu_inference",
		Summary:         "Canonical llm.gpu.* and curated Prometheus pack contract for correlating KV-cache pressure, sequence preemptions, scheduler queueing, and GPU compute/HBM saturation with TTFT and TPOT.",
		RequiredMetrics: required,
		MissingMetrics:  append([]string(nil), required...),
		MetricContract:  gpuInferenceMetricContract(),
		ExpectedLabels:  gpuInferenceExpectedLabels(),
		NamingConventions: []string{
			"Use curated Prometheus packs (vllm, nim, tgi, triton-trtllm, dcgm-basic, dcgm-profiling, otel-genai) or publish llm.gpu.* canonical metrics.",
			"Keep Prometheus labels bounded to model_name, deployment, instance, and GPU index.",
			"Never place raw prompt text, completion text, or user identifiers in Prometheus labels.",
		},
		SuggestedDashboards: []string{
			"KV-Cache Usage & Preemptions vs TTFT / TPOT P95",
			"GPU Compute vs HBM Memory Bandwidth Saturation",
			"Client vs Server Latency Decomposition (Queue, Prefill, Network, Decode)",
		},
		ExampleExternalMetrics: gpuInferenceExternalMetricExamples("http://127.0.0.1:8000/metrics"),
	}
}

func gpuInferenceExpectedLabels() []string {
	return []string{"model_name", "deployment", "instance", "gpu"}
}

func gpuInferenceExternalMetricExamples(url string) []MonitoringExternalMetricExample {
	labels := map[string]string{"model_name": "llama-3.1-8b-instruct"}
	contract := gpuInferenceMetricContract()
	examples := make([]MonitoringExternalMetricExample, 0, len(contract))
	for _, metric := range contract {
		examples = append(examples, MonitoringExternalMetricExample{
			Name:       metric.CanonicalName,
			URL:        url,
			Format:     "prometheus",
			MetricName: metric.PrometheusMetric,
			Labels:     cloneStringMap(labels),
		})
	}
	return examples
}

func gpuInferenceCanonicalMetricNames(requiredOnly bool) []string {
	metrics := gpuInferenceMetricContract()
	names := make([]string, 0, len(metrics))
	for _, metric := range metrics {
		if requiredOnly && !metric.Required {
			continue
		}
		names = append(names, metric.CanonicalName)
	}
	sort.Strings(names)
	return names
}

func isGPUInferenceMetricName(name string) bool {
	return strings.HasPrefix(strings.TrimSpace(name), gpuInferenceMetricPrefix)
}
