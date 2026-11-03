package prometheus

import (
	"fmt"
	"strings"
)

// MetricPackQueryTemplate describes a curated PromQL query template with optional
// fallback metric names for older/newer engine releases.
type MetricPackQueryTemplate struct {
	ID              string   `json:"id"`
	Name            string   `json:"name"`
	Description     string   `json:"description,omitempty"`
	Expression      string   `json:"expression"`
	FallbackMetrics []string `json:"fallback_metrics,omitempty"`
	PrimaryMetric   string   `json:"primary_metric,omitempty"`
	Unit            string   `json:"unit,omitempty"`
	Semantics       string   `json:"semantics,omitempty"`
	AllowedLabels   []string `json:"allowed_labels,omitempty"`
}

// MetricPack defines a curated Prometheus monitoring pack for an LLM serving
// stack or GPU telemetry exporter. Each pack has at most 20 queries.
type MetricPack struct {
	ID          string                    `json:"id"`
	Name        string                    `json:"name"`
	Category    string                    `json:"category"`
	Description string                    `json:"description"`
	Queries     []MetricPackQueryTemplate `json:"queries"`
}

// PackRenderOptions controls metric variant selection (from discovered metric
// names) and label overrides (such as pod -> exported_pod).
type PackRenderOptions struct {
	DiscoveredMetrics []string          `json:"discovered_metrics,omitempty"`
	LabelOverrides    map[string]string `json:"label_overrides,omitempty"`
	PodLabel          string            `json:"pod_label,omitempty"`
}

// InferenceMetricPacks returns the built-in catalog of LLM and GPU Prometheus packs.
func InferenceMetricPacks() []MetricPack {
	return []MetricPack{
		vllmMetricPack(),
		nimMetricPack(),
		tgiMetricPack(),
		tritonTRTLLMMetricPack(),
		dcgmBasicMetricPack(),
		dcgmProfilingMetricPack(),
		otelGenAIMetricPack(),
	}
}

// GetMetricPack looks up a curated pack by ID.
func GetMetricPack(id string) (MetricPack, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	for _, p := range InferenceMetricPacks() {
		if p.ID == key {
			return p, true
		}
	}
	return MetricPack{}, false
}

// BuildQueries materializes a MetricPack into validated Prometheus Query
// definitions, choosing metric variants based on discovered metrics and
// applying label overrides (e.g. pod -> exported_pod).
func (p MetricPack) BuildQueries(opts PackRenderOptions) ([]Query, error) {
	if len(p.Queries) > 20 {
		return nil, fmt.Errorf("pack %s exceeds the 20-query limit (%d)", p.ID, len(p.Queries))
	}
	discovered := make(map[string]bool, len(opts.DiscoveredMetrics))
	for _, m := range opts.DiscoveredMetrics {
		discovered[strings.TrimSpace(m)] = true
	}
	podLabel := strings.TrimSpace(opts.PodLabel)
	if podLabel == "" && opts.LabelOverrides != nil {
		podLabel = strings.TrimSpace(opts.LabelOverrides["pod"])
	}
	if podLabel != "" && (!labelRE.MatchString(podLabel) || sensitiveLabel(podLabel)) {
		return nil, fmt.Errorf("invalid pod label override %q", podLabel)
	}

	out := make([]Query, 0, len(p.Queries))
	for _, tmpl := range p.Queries {
		expr := tmpl.Expression
		if tmpl.PrimaryMetric != "" && len(tmpl.FallbackMetrics) > 0 && len(discovered) > 0 {
			if !discovered[tmpl.PrimaryMetric] {
				for _, fb := range tmpl.FallbackMetrics {
					if discovered[fb] {
						expr = strings.ReplaceAll(expr, tmpl.PrimaryMetric, fb)
						break
					}
				}
			}
		}
		allowed := append([]string(nil), tmpl.AllowedLabels...)
		if podLabel != "" && podLabel != "pod" {
			expr = strings.ReplaceAll(expr, "by (pod", "by ("+podLabel)
			expr = strings.ReplaceAll(expr, ", pod)", ", "+podLabel+")")
			for i, l := range allowed {
				if l == "pod" {
					allowed[i] = podLabel
				}
			}
		}
		for src, dst := range opts.LabelOverrides {
			src = strings.TrimSpace(src)
			dst = strings.TrimSpace(dst)
			if src == "" || dst == "" || src == "pod" {
				continue
			}
			if !labelRE.MatchString(dst) || sensitiveLabel(dst) {
				return nil, fmt.Errorf("invalid label override %q", dst)
			}
			for i, l := range allowed {
				if l == src {
					allowed[i] = dst
				}
			}
		}
		out = append(out, Query{
			ID:            tmpl.ID,
			Name:          tmpl.Name,
			Expression:    expr,
			Unit:          tmpl.Unit,
			Semantics:     tmpl.Semantics,
			AllowedLabels: allowed,
		})
	}
	return out, nil
}

func vllmMetricPack() MetricPack {
	labels := []string{"model_name", "pod", "instance"}
	return MetricPack{
		ID:          "vllm",
		Name:        "vLLM Inference Server",
		Category:    "llm_inference",
		Description: "KV-cache usage (supporting both vllm:kv_cache_usage_perc and vllm:gpu_cache_usage_perc), request queue, preemptions, TTFT/TPOT histograms, and token throughput.",
		Queries: []MetricPackQueryTemplate{
			{
				ID:              "vllm_kv_cache_usage",
				Name:            "vllm.kv_cache_usage_perc",
				PrimaryMetric:   "vllm:kv_cache_usage_perc",
				FallbackMetrics: []string{"vllm:gpu_cache_usage_perc"},
				Expression:      "avg by (model_name, pod) (vllm:kv_cache_usage_perc) * 100",
				Unit:            "percent",
				Semantics:       "gauge",
				AllowedLabels:   labels,
			},
			{
				ID:            "vllm_requests_running",
				Name:          "vllm.num_requests_running",
				PrimaryMetric: "vllm:num_requests_running",
				Expression:    "sum by (model_name, pod) (vllm:num_requests_running)",
				Unit:          "requests",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_requests_waiting",
				Name:          "vllm.num_requests_waiting",
				PrimaryMetric: "vllm:num_requests_waiting",
				Expression:    "sum by (model_name, pod) (vllm:num_requests_waiting)",
				Unit:          "requests",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_preemptions_rate",
				Name:          "vllm.num_preemptions_rate",
				PrimaryMetric: "vllm:num_preemptions_total",
				Expression:    "sum by (model_name, pod) (rate(vllm:num_preemptions_total[1m]))",
				Unit:          "preemptions/s",
				Semantics:     "rate",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_ttft_p95_ms",
				Name:          "vllm.time_to_first_token_p95_ms",
				PrimaryMetric: "vllm:time_to_first_token_seconds_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, model_name, pod) (rate(vllm:time_to_first_token_seconds_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_tpot_p95_ms",
				Name:          "vllm.time_per_output_token_p95_ms",
				PrimaryMetric: "vllm:time_per_output_token_seconds_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, model_name, pod) (rate(vllm:time_per_output_token_seconds_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_queue_time_avg_ms",
				Name:          "vllm.request_queue_time_avg_ms",
				PrimaryMetric: "vllm:request_queue_time_seconds_sum",
				Expression:    "(sum by (model_name, pod) (rate(vllm:request_queue_time_seconds_sum[1m])) / clamp_min(sum by (model_name, pod) (rate(vllm:request_queue_time_seconds_count[1m])), 1e-9)) * 1000",
				Unit:          "ms",
				Semantics:     "average",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_prefill_time_avg_ms",
				Name:          "vllm.request_prefill_time_avg_ms",
				PrimaryMetric: "vllm:request_prefill_time_seconds_sum",
				Expression:    "(sum by (model_name, pod) (rate(vllm:request_prefill_time_seconds_sum[1m])) / clamp_min(sum by (model_name, pod) (rate(vllm:request_prefill_time_seconds_count[1m])), 1e-9)) * 1000",
				Unit:          "ms",
				Semantics:     "average",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_decode_time_avg_ms",
				Name:          "vllm.request_decode_time_avg_ms",
				PrimaryMetric: "vllm:request_decode_time_seconds_sum",
				Expression:    "(sum by (model_name, pod) (rate(vllm:request_decode_time_seconds_sum[1m])) / clamp_min(sum by (model_name, pod) (rate(vllm:request_decode_time_seconds_count[1m])), 1e-9)) * 1000",
				Unit:          "ms",
				Semantics:     "average",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_generation_tokens_rate",
				Name:          "vllm.generation_tokens_per_sec",
				PrimaryMetric: "vllm:generation_tokens_total",
				Expression:    "sum by (model_name, pod) (rate(vllm:generation_tokens_total[1m]))",
				Unit:          "tokens/s",
				Semantics:     "rate",
				AllowedLabels: labels,
			},
			{
				ID:            "vllm_prompt_tokens_rate",
				Name:          "vllm.prompt_tokens_per_sec",
				PrimaryMetric: "vllm:prompt_tokens_total",
				Expression:    "sum by (model_name, pod) (rate(vllm:prompt_tokens_total[1m]))",
				Unit:          "tokens/s",
				Semantics:     "rate",
				AllowedLabels: labels,
			},
			{
				ID:              "vllm_prefix_cache_hit_rate",
				Name:            "vllm.prefix_cache_hit_pct",
				PrimaryMetric:   "vllm:prefix_cache_hits_total",
				FallbackMetrics: []string{"vllm:gpu_prefix_cache_hits_total"},
				Expression:      "(sum by (model_name, pod) (rate(vllm:prefix_cache_hits_total[1m])) / clamp_min(sum by (model_name, pod) (rate(vllm:prefix_cache_queries_total[1m])), 1e-9)) * 100",
				Unit:            "percent",
				Semantics:       "gauge",
				AllowedLabels:   labels,
			},
		},
	}
}

func nimMetricPack() MetricPack {
	labels := []string{"model_name", "pod", "instance"}
	return MetricPack{
		ID:          "nim",
		Name:        "NVIDIA NIM for LLMs",
		Category:    "llm_inference",
		Description: "NVIDIA NIM KV-cache usage, active/waiting requests, TTFT and TPOT p95 latencies, and token generation rate.",
		Queries: []MetricPackQueryTemplate{
			{
				ID:              "nim_kv_cache_usage",
				Name:            "nim.kv_cache_usage_perc",
				PrimaryMetric:   "gpu_cache_usage_perc",
				FallbackMetrics: []string{"vllm:kv_cache_usage_perc"},
				Expression:      "avg by (model_name, pod) (gpu_cache_usage_perc) * 100",
				Unit:            "percent",
				Semantics:       "gauge",
				AllowedLabels:   labels,
			},
			{
				ID:            "nim_requests_running",
				Name:          "nim.num_requests_running",
				PrimaryMetric: "num_requests_running",
				Expression:    "sum by (model_name, pod) (num_requests_running)",
				Unit:          "requests",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "nim_requests_waiting",
				Name:          "nim.num_requests_waiting",
				PrimaryMetric: "num_requests_waiting",
				Expression:    "sum by (model_name, pod) (num_requests_waiting)",
				Unit:          "requests",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "nim_ttft_p95_ms",
				Name:          "nim.time_to_first_token_p95_ms",
				PrimaryMetric: "time_to_first_token_seconds_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, model_name, pod) (rate(time_to_first_token_seconds_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "nim_tpot_p95_ms",
				Name:          "nim.time_per_output_token_p95_ms",
				PrimaryMetric: "time_per_output_token_seconds_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, model_name, pod) (rate(time_per_output_token_seconds_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "nim_generation_tokens_rate",
				Name:          "nim.generation_tokens_per_sec",
				PrimaryMetric: "generation_tokens_total",
				Expression:    "sum by (model_name, pod) (rate(generation_tokens_total[1m]))",
				Unit:          "tokens/s",
				Semantics:     "rate",
				AllowedLabels: labels,
			},
		},
	}
}

func tgiMetricPack() MetricPack {
	labels := []string{"pod", "instance"}
	return MetricPack{
		ID:          "tgi",
		Name:        "Hugging Face Text Generation Inference (TGI)",
		Category:    "llm_inference",
		Description: "TGI queue size, batch size, queue duration, prefill/decode token latency, and generated tokens.",
		Queries: []MetricPackQueryTemplate{
			{
				ID:            "tgi_queue_size",
				Name:          "tgi.queue_size",
				PrimaryMetric: "tgi_queue_size",
				Expression:    "sum by (pod) (tgi_queue_size)",
				Unit:          "requests",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "tgi_batch_current_size",
				Name:          "tgi.batch_current_size",
				PrimaryMetric: "tgi_batch_current_size",
				Expression:    "avg by (pod) (tgi_batch_current_size)",
				Unit:          "requests",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "tgi_queue_duration_p95_ms",
				Name:          "tgi.request_queue_duration_p95_ms",
				PrimaryMetric: "tgi_request_queue_duration_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, pod) (rate(tgi_request_queue_duration_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "tgi_time_per_token_p95_ms",
				Name:          "tgi.time_per_token_p95_ms",
				PrimaryMetric: "tgi_request_mean_time_per_token_duration_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, pod) (rate(tgi_request_mean_time_per_token_duration_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "tgi_generated_tokens_rate",
				Name:          "tgi.generated_tokens_per_sec",
				PrimaryMetric: "tgi_request_generated_tokens_sum",
				Expression:    "sum by (pod) (rate(tgi_request_generated_tokens_sum[1m]))",
				Unit:          "tokens/s",
				Semantics:     "rate",
				AllowedLabels: labels,
			},
		},
	}
}

func tritonTRTLLMMetricPack() MetricPack {
	labels := []string{"model", "pod", "instance"}
	return MetricPack{
		ID:          "triton_trtllm",
		Name:        "Triton / TensorRT-LLM",
		Category:    "llm_inference",
		Description: "Triton and TensorRT-LLM KV-cache utilization, queued requests, queue/compute duration, and inference rate.",
		Queries: []MetricPackQueryTemplate{
			{
				ID:            "trtllm_kv_cache_usage",
				Name:          "triton.kv_cache_block_utilization_pct",
				PrimaryMetric: "nv_trt_llm_kv_cache_block_metrics",
				Expression:    "avg by (model, pod) (nv_trt_llm_kv_cache_block_metrics) * 100",
				Unit:          "percent",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "triton_pending_requests",
				Name:          "triton.pending_request_count",
				PrimaryMetric: "nv_inference_pending_request_count",
				Expression:    "sum by (model, pod) (nv_inference_pending_request_count)",
				Unit:          "requests",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "triton_queue_duration_ms",
				Name:          "triton.request_queue_duration_avg_ms",
				PrimaryMetric: "nv_inference_queue_duration_us",
				Expression:    "(sum by (model, pod) (rate(nv_inference_queue_duration_us[1m])) / clamp_min(sum by (model, pod) (rate(nv_inference_request_success[1m])), 1e-9)) / 1000",
				Unit:          "ms",
				Semantics:     "average",
				AllowedLabels: labels,
			},
			{
				ID:            "triton_compute_infer_ms",
				Name:          "triton.compute_infer_duration_avg_ms",
				PrimaryMetric: "nv_inference_compute_infer_duration_us",
				Expression:    "(sum by (model, pod) (rate(nv_inference_compute_infer_duration_us[1m])) / clamp_min(sum by (model, pod) (rate(nv_inference_request_success[1m])), 1e-9)) / 1000",
				Unit:          "ms",
				Semantics:     "average",
				AllowedLabels: labels,
			},
			{
				ID:            "triton_inference_rate",
				Name:          "triton.inference_count_per_sec",
				PrimaryMetric: "nv_inference_count",
				Expression:    "sum by (model, pod) (rate(nv_inference_count[1m]))",
				Unit:          "inferences/s",
				Semantics:     "rate",
				AllowedLabels: labels,
			},
		},
	}
}

func dcgmBasicMetricPack() MetricPack {
	labels := []string{"gpu", "UUID", "pod", "Hostname"}
	return MetricPack{
		ID:          "dcgm_basic",
		Name:        "NVIDIA DCGM GPU Basic Telemetry",
		Category:    "gpu_hardware",
		Description: "GPU compute utilization, memory copy utilization, HBM used/free, power usage, temperature, and throttle reasons.",
		Queries: []MetricPackQueryTemplate{
			{
				ID:            "dcgm_gpu_util",
				Name:          "dcgm.gpu_utilization_pct",
				PrimaryMetric: "DCGM_FI_DEV_GPU_UTIL",
				Expression:    "avg by (gpu, pod) (DCGM_FI_DEV_GPU_UTIL)",
				Unit:          "percent",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "dcgm_mem_copy_util",
				Name:          "dcgm.mem_copy_utilization_pct",
				PrimaryMetric: "DCGM_FI_DEV_MEM_COPY_UTIL",
				Expression:    "avg by (gpu, pod) (DCGM_FI_DEV_MEM_COPY_UTIL)",
				Unit:          "percent",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "dcgm_fb_used_mib",
				Name:          "dcgm.fb_used_mib",
				PrimaryMetric: "DCGM_FI_DEV_FB_USED",
				Expression:    "max by (gpu, pod) (DCGM_FI_DEV_FB_USED)",
				Unit:          "MiB",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "dcgm_fb_free_mib",
				Name:          "dcgm.fb_free_mib",
				PrimaryMetric: "DCGM_FI_DEV_FB_FREE",
				Expression:    "min by (gpu, pod) (DCGM_FI_DEV_FB_FREE)",
				Unit:          "MiB",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "dcgm_power_usage_watts",
				Name:          "dcgm.power_usage_watts",
				PrimaryMetric: "DCGM_FI_DEV_POWER_USAGE",
				Expression:    "avg by (gpu, pod) (DCGM_FI_DEV_POWER_USAGE)",
				Unit:          "W",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "dcgm_gpu_temp_c",
				Name:          "dcgm.gpu_temp_celsius",
				PrimaryMetric: "DCGM_FI_DEV_GPU_TEMP",
				Expression:    "max by (gpu, pod) (DCGM_FI_DEV_GPU_TEMP)",
				Unit:          "C",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
		},
	}
}

func dcgmProfilingMetricPack() MetricPack {
	labels := []string{"gpu", "UUID", "pod", "Hostname"}
	return MetricPack{
		ID:          "dcgm_profiling",
		Name:        "NVIDIA DCGM GPU Profiling Metrics",
		Category:    "gpu_hardware",
		Description: "SM active ratio, SM occupancy, tensor core pipe utilization, DRAM bandwidth activity, and NVLink/PCIe throughput.",
		Queries: []MetricPackQueryTemplate{
			{
				ID:            "dcgm_sm_active",
				Name:          "dcgm.prof_sm_active_pct",
				PrimaryMetric: "DCGM_FI_PROF_SM_ACTIVE",
				Expression:    "avg by (gpu, pod) (DCGM_FI_PROF_SM_ACTIVE) * 100",
				Unit:          "percent",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "dcgm_sm_occupancy",
				Name:          "dcgm.prof_sm_occupancy_pct",
				PrimaryMetric: "DCGM_FI_PROF_SM_OCCUPANCY",
				Expression:    "avg by (gpu, pod) (DCGM_FI_PROF_SM_OCCUPANCY) * 100",
				Unit:          "percent",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "dcgm_tensor_active",
				Name:          "dcgm.prof_pipe_tensor_active_pct",
				PrimaryMetric: "DCGM_FI_PROF_PIPE_TENSOR_ACTIVE",
				Expression:    "avg by (gpu, pod) (DCGM_FI_PROF_PIPE_TENSOR_ACTIVE) * 100",
				Unit:          "percent",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
			{
				ID:            "dcgm_dram_active",
				Name:          "dcgm.prof_dram_active_pct",
				PrimaryMetric: "DCGM_FI_PROF_DRAM_ACTIVE",
				Expression:    "avg by (gpu, pod) (DCGM_FI_PROF_DRAM_ACTIVE) * 100",
				Unit:          "percent",
				Semantics:     "gauge",
				AllowedLabels: labels,
			},
		},
	}
}

func otelGenAIMetricPack() MetricPack {
	labels := []string{"gen_ai_request_model", "gen_ai_system", "pod"}
	return MetricPack{
		ID:          "otel_genai",
		Name:        "OpenTelemetry GenAI Semantic Conventions",
		Category:    "llm_inference",
		Description: "Standard OpenTelemetry gen_ai.server.time_to_first_token, time_per_output_token, request.duration, and token usage metrics.",
		Queries: []MetricPackQueryTemplate{
			{
				ID:            "otel_genai_ttft_p95_ms",
				Name:          "otel.gen_ai_server_ttft_p95_ms",
				PrimaryMetric: "gen_ai_server_time_to_first_token_seconds_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, gen_ai_request_model, pod) (rate(gen_ai_server_time_to_first_token_seconds_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "otel_genai_tpot_p95_ms",
				Name:          "otel.gen_ai_server_tpot_p95_ms",
				PrimaryMetric: "gen_ai_server_time_per_output_token_seconds_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, gen_ai_request_model, pod) (rate(gen_ai_server_time_per_output_token_seconds_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "otel_genai_duration_p95_ms",
				Name:          "otel.gen_ai_server_request_duration_p95_ms",
				PrimaryMetric: "gen_ai_server_request_duration_seconds_bucket",
				Expression:    "histogram_quantile(0.95, sum by (le, gen_ai_request_model, pod) (rate(gen_ai_server_request_duration_seconds_bucket[1m]))) * 1000",
				Unit:          "ms",
				Semantics:     "p95",
				AllowedLabels: labels,
			},
			{
				ID:            "otel_genai_token_usage_rate",
				Name:          "otel.gen_ai_client_token_usage_per_sec",
				PrimaryMetric: "gen_ai_client_token_usage_sum",
				Expression:    "sum by (gen_ai_request_model, pod) (rate(gen_ai_client_token_usage_sum[1m]))",
				Unit:          "tokens/s",
				Semantics:     "rate",
				AllowedLabels: labels,
			},
		},
	}
}
