# LLM Inference Performance Testing

APEX Load provides first-class performance testing, capacity planning, and root-cause analysis for large language model (LLM) inference endpoints and AI applications.

---

## 1. Supported Targets

APEX Load's `llm_request` action (`pkg/dsl/catalog_llm.go`) and streaming parser (`pkg/protocol/http/llm_stream.go`) support three primary target families:

1. **Self-hosted OpenAI-compatible servers (`openai_chat`, `openai_completions`, `openai_responses`, `tgi_generate`, `ollama_chat`, `triton_generate`)**:
   - vLLM, HuggingFace Text Generation Inference (TGI), NVIDIA NIM / TensorRT-LLM (Triton), and Ollama over SSE (`text/event-stream`) or NDJSON (`application/x-ndjson`).
   - Automatically injects `"stream_options": {"include_usage": true}` when streaming OpenAI-compatible chat completions so server-reported prompt, completion, reasoning, and cached token counts are captured (`pkg/protocol/http/llm_request.go`).
2. **Azure OpenAI and Azure AI Foundry (`azure_openai_chat`)**:
   - Supports `api-key` and `Authorization: Bearer` headers, captures `x-ratelimit-remaining-tokens` / `x-ratelimit-remaining-requests` and `x-ms-region`, and honors `retry-after-ms` / `Retry-After` on HTTP 429 responses (`pkg/protocol/http/llm_headers.go`).
   - Supports client-side TPM/RPM dual-bucket quota pacing and AIMD 429 congestion control (`pkg/engine/quota_pacer.go`).
3. **AI applications, RAG pipelines, and agents (`vercel_ai_data_stream`, `langserve_sse`, `raw_text`, `custom`)**:
   - Supports custom `stream_profile` JSONPath extraction (`pkg/protocol/http/llm_dialects.go`) and multi-turn conversational state (`conversation_var`, `max_history_turns`, `max_context_tokens` in `pkg/engine/llm_conversation.go`) with per-hop LLM roll-ups inside `source_transaction` and `http_sequence`.

> **Scope note**: AWS Bedrock binary event-stream and SigV4 signing are out of scope. Token counts use server-reported `usage` when available and fall back to a deterministic `~4 bytes/token` estimate (`tokens_estimated = true`) without external tokenizer dependencies.

---

## 2. Core Metrics & Diagnostics

Every `llm_request` action records `protocol.LLMDiagnostics` (`pkg/protocol/llm_diagnostics.go`) on `ServerTelemetry.LLM` and aggregates into `engine.LLMSummary` (`pkg/engine/llm_summary.go`):

- **Time to First Token (`ttft_ms`)**: Elapsed milliseconds from request write completion to the first non-empty content, reasoning, or tool-call token chunk (excludes role-only preamble chunks and SSE keepalive comments).
- **Time to First Visible Token (`first_visible_token_ms`)**: Elapsed milliseconds to the first user-visible content token when reasoning tokens precede visible output (`reasoning_duration_ms`).
- **Time per Output Token (`tpot_ms`)**: `(last_token - first_token) / (output_tokens - 1)` when `output_tokens >= 2`, capturing pure decode cadence independent of prefill latency.
- **Inter-Token Latency (`itl_ms`, `max_itl_ms`, `stall_count`)**: Mergeable 144-bucket `DiagnosticDistribution` (`pkg/protocol/grpc_diagnostics.go`, `pkg/protocol/llm_diagnostics.go`) of inter-chunk gaps plus generation stall count (gaps exceeding `stall_threshold_ms`, default `500 ms`).
- **Goodput (`slo_compliant`, `goodput_pct`)**: Percentage of requests that complete without error, truncation (`finish_reason = "length"`), refusal (`finish_reason = "content_filter"`), or schema failure while meeting configured `slo_ttft_ms`, `slo_tpot_ms`, and `slo_e2e_ms` targets.

---

## 3. Workloads, Datasets, and Capacity Sweeps

- **Arrival Processes**: Open-model groups (`pkg/engine/open_workload.go`, `pkg/engine/arrival_process.go`) support `"arrival_process": "uniform" | "poisson" | "gamma"` (with `"burstiness"` for squared coefficient of variation $c_v^2$) and per-stage `target_tps` plus `warmup` exclusion (`pkg/dsl/types.go`).
- **Prompt Datasets (`data_jsonl_v1`)**: Import ShareGPT, OpenAI JSONL, Alpaca, or LMSYS traces via `POST /api/v1/llm/prompt-datasets/import` (with automatic PII scrubbing in `pkg/llmperf/dataset/dataset.go`), synthesize token-length distributions via `POST /api/v1/llm/prompt-datasets/synthesize` (`pkg/llmperf/synth/synth.go`), or calibrate `bytes_per_token` via `POST /api/v1/llm/prompt-datasets/calibrate` (`pkg/api/handlers_llm_perf.go`).
- **Capacity Sweeps**: Configure `capacity_sweep` on a test's workload shape (`pkg/api/capacity_sweep.go`) to step through concurrency or RPS levels with automatic warmup stages. Query `GET /api/v1/tests/{id}/archive/capacity-sweep` (`pkg/api/capacity_sweep_report.go`) for the knee-of-curve fit, maximum sustainable rate, and token economics (`$/1M output tokens`).
- **End-to-End Latency Decomposition**: Query `GET /api/v1/tests/{id}/archive/llm-latency-decomposition` (`pkg/api/llm_latency_decomposition.go`) to break E2E latency into network/gateway, server queue, prefill, and decode time, alongside Little's Law in-flight concurrency estimates ($L = \lambda \cdot W$).
- **A/B Model & Engine Comparison**: Compare two runs or two concurrent groups via `POST /api/v1/llm/compare` (`pkg/llmperf/compare/compare.go`) with paired bootstrap 95% confidence intervals on TTFT and TPOT.
- **OpenTelemetry GenAI Span Lookup**: Correlate archived LLM requests by W3C `trace_id` against Elastic/OpenSearch and Dynatrace Grail `gen_ai.*` spans via `POST /api/v1/tests/{id}/archive/genai-spans/lookup` (`pkg/api/genai_span_lookup.go`).

---

## 4. Telemetry Packs & Root-Cause Analysis (`signatures.v2`)

- **Prometheus Metric Packs**: Query `GET /api/v1/monitoring/prometheus-packs` (`pkg/prometheus/packs.go`, `pkg/api/handlers_llm_perf.go`, `pkg/api/route_registry_prometheus.go`) for curated PromQL packs covering `vllm`, `nim`, `tgi`, `triton-trtllm`, `dcgm-basic`, `dcgm-profiling`, and `otel-genai`.
- **Collector Cloud & GPU Packs**: The monitoring collector agent includes cloud inference query templates (`pkg/collectoragent/cloud.go`) alongside Prometheus/DCGM packs (`pkg/api/monitoring_readiness_gpu.go`).
- **RCA `signatures.v2`**: Automatically correlates TTFT/TPOT degradation and 429 throttling against KV-cache saturation (`kv_cache_saturation`), GPU compute saturation (`gpu_compute_saturation`), inference queueing/preemption (`inference_queueing`), and rate limiting (`rate_limited`) in `pkg/rca/signatures/inference.go` and `pkg/rca/hypotheses.go`.

---

## 5. Idea Catalog

| Area | Capability | Priority | Effort | Status | Primary Code Locations |
|---|---|---|---|---|---|
| **Measure** | First-class TTFT, TPOT, ITL, output tok/s, input tok/s, total generation time | P0 | M | Implemented | `pkg/protocol/http/llm_stream.go`, `pkg/protocol/llm_diagnostics.go`, `pkg/engine/llm_summary.go` |
| **Measure** | Goodput (% requests meeting TTFT + TPOT + E2E SLO simultaneously) | P0 | S | Implemented | `pkg/engine/llm_summary.go`, `pkg/api/report_llm.go`, `pkg/runcontract/goals.go` |
| **Measure** | Token accounting: prompt, completion, cached-prefix, reasoning tokens | P0 | S | Implemented | `pkg/protocol/http/llm_stream.go`, `pkg/protocol/http/llm_dialects.go` |
| **Measure** | Generation stall & stream anomaly detection (max inter-chunk gap, mid-stream abort, truncation) | P1 | S | Implemented | `pkg/protocol/http/llm_stream.go`, `pkg/protocol/grpc_diagnostics.go`, `pkg/protocol/llm_diagnostics.go` |
| **Measure** | Reasoning-model split: thinking duration vs. visible-answer TTFT/TPOT | P1 | S | Implemented | `pkg/protocol/http/llm_stream.go`, `pkg/protocol/llm_diagnostics.go` |
| **Author** | `llm_request` DSL action (or `http_request` `stream_profile`) with dialect presets | P0 | M | Implemented | `pkg/dsl/catalog_llm.go`, `pkg/protocol/http/llm_request.go`, `pkg/protocol/http/llm_dialects.go` |
| **Author** | Prompt dataset loader: ShareGPT, OpenAI JSONL, LMSYS, custom JSONL (`data_jsonl_v1`) | P0 | M | Implemented | `pkg/llmperf/dataset/dataset.go`, `pkg/engine/data.go`, `pkg/dsl/validate.go`, `pkg/api/handlers_llm_perf.go` |
| **Author** | Synthetic prompt generator with exact/approximate target input/output token distributions & prefix-cache hit control | P1 | M | Implemented | `pkg/llmperf/synth/synth.go`, `pkg/api/handlers_llm_perf.go` |
| **Author** | Multi-turn conversation state helper (append assistant reply, sliding window / token budget) | P1 | S | Implemented | `pkg/engine/llm_conversation.go`, `pkg/engine/vuser.go` |
| **Author** | Agentic & RAG workflow tracing (tool-call loop, retrieval step + LLM step rolled into one `source_transaction`) | P2 | M | Implemented | `pkg/protocol/http/llm_request.go`, `pkg/engine/vuser.go`, `pkg/engine/vuser_source_actions.go` |
| **Workload** | Poisson & bursty (Gamma/Weibull) open-loop request arrival processes | P1 | S | Implemented | `pkg/engine/open_workload.go`, `pkg/engine/arrival_process.go`, `pkg/dsl/types.go` |
| **Workload** | Token-budget / TPM & RPM rate-limit-aware pacing + 429 `Retry-After` AIMD | P1 | M | Implemented | `pkg/engine/quota_pacer.go`, `pkg/protocol/http/llm_headers.go`, `pkg/protocol/http/llm_request.go` |
| **Workload** | Automated concurrency/RPS capacity sweep with warmup exclusion | P1 | M | Implemented | `pkg/api/capacity_sweep.go`, `pkg/api/capacity_sweep_report.go`, `pkg/api/testcreation.go` |
| **Observe** | Curated Prometheus metric packs: vLLM, TGI, NIM, Triton/TRT-LLM, NVIDIA DCGM | P0 | S | Implemented | `pkg/prometheus/packs.go`, `pkg/api/handlers_llm_perf.go`, `pkg/api/route_registry_prometheus.go` |
| **Observe** | Cloud & collector telemetry packs for managed/host LLM deployments | P2 | S | Implemented | `pkg/collectoragent/cloud.go`, `pkg/api/monitoring_readiness_gpu.go` |
| **Observe** | OpenTelemetry `gen_ai.*` semantic-convention span attribute correlation | P1 | S | Implemented | `pkg/engine/vuser_execute.go`, `pkg/api/genai_span_lookup.go` |
| **Diagnose** | RCA signatures for KV-cache saturation, prefill compute bottleneck, decode memory-bandwidth bottleneck, preemption/swapping, gateway rate-limiting | P1 | M | Implemented | `pkg/rca/signatures/inference.go`, `pkg/rca/hypotheses.go`, `pkg/api/rca_detection.go` |
| **Diagnose** | End-to-end latency decomposition (Network/Gateway -> Server Queue -> Prefill -> Decode) | P1 | M | Implemented | `pkg/api/llm_latency_decomposition.go`, `pkg/api/report_llm.go` |
| **Quality under load** | Deterministic structural/semantic guards (JSON schema, refusal detector, finish-reason check, length bounds, golden-prompt drift) | P1 | S | Implemented | `pkg/protocol/http/llm_request.go`, `pkg/api/report_llm.go` |
| **Quality under load** | Sampled async post-run evaluation export (JSONL bundle for external evals) | P2 | S | Implemented | `pkg/api/handlers_llm_perf.go` |
| **Decide** | Token unit economics (`$/1M output tokens`, `$/1k goodput requests`, GPU-hour vs. API/PTU comparison) | P1 | S | Implemented | `pkg/llmperf/econ/econ.go`, `pkg/api/handlers_llm_perf.go`, `pkg/api/report_llm.go`, `pkg/storage/sqlite_schema.go` |
| **Decide** | Capacity & replica planner (knee-of-curve + max RPS/concurrency at SLO) | P1 | M | Implemented | `pkg/llmperf/plan/plan.go`, `pkg/api/capacity_sweep_report.go`, `pkg/api/handlers_llm_perf.go` |
| **Decide** | A/B model, quantization, or engine comparison view | P2 | M | Implemented | `pkg/llmperf/compare/compare.go`, `pkg/api/handlers_llm_perf.go`, `dashboard/src/components/LLMComparisonPanel.tsx` |

---

## 6. Implementation Roadmap

- **Prerequisite P0 — Local Mock LLM Inference Target (`cmd/apexllm-target`, `pkg/internal/llmfixture`)**:
  - Disposable HTTP/1.1 + HTTP/2 server speaking OpenAI Chat/Completions/Responses SSE, Azure OpenAI headers, TGI SSE, Ollama NDJSON, and Vercel AI Data Stream, with continuous-batching simulation (`max_num_seqs`, KV-cache capacity, prefill/decode physics, 429 injection, and `/metrics`).
- **Phase 0 — Streaming Protocol & Client-Side Token Timing (WP0.1–WP0.5)**:
  - **WP0.1**: Streaming SSE (`text/event-stream`) and NDJSON (`application/x-ndjson`) reader (`pkg/protocol/http/llm_stream.go`, `pkg/protocol/http/ndjson.go`) with per-chunk timestamping and `stream_options.include_usage` injection.
  - **WP0.2**: Dialect registry (`pkg/protocol/http/llm_dialects.go`) and custom `stream_profile` JSONPath extraction.
  - **WP0.3**: Token counting from server `usage` with calibrated `~4 bytes/token` fallback (`pkg/llmperf/dataset/dataset.go`).
  - **WP0.4**: Rate-limit and provider response header capture (`pkg/protocol/http/llm_headers.go`) and `honor_retry_after` waits.
  - **WP0.5**: `llm_request` DSL action catalog (`pkg/dsl/catalog_llm.go`, `pkg/protocol/http/llm_request.go`).
- **Phase 1 — Workload Realism, Datasets, and Multi-Turn State (WP1.1–WP1.6)**:
  - **WP1.1**: Prompt dataset importer (`pkg/llmperf/dataset/dataset.go`) and `jsonl` data source (`pkg/engine/data.go`, `pkg/dsl/validate.go`).
  - **WP1.2**: Synthetic prompt & prefix-cache generator (`pkg/llmperf/synth/synth.go`).
  - **WP1.3**: Multi-turn conversation state (`pkg/engine/llm_conversation.go`) and workflow roll-up (`pkg/engine/vuser.go`, `pkg/engine/vuser_source_actions.go`).
  - **WP1.4**: Open-loop Poisson and Gamma arrival processes (`pkg/engine/open_workload.go`, `pkg/engine/arrival_process.go`) and stage `target_tps` + `warmup` (`pkg/dsl/types.go`).
  - **WP1.5**: Client-side TPM/RPM dual-bucket pacing and AIMD 429 rate control (`pkg/engine/quota_pacer.go`).
  - **WP1.6**: Automated concurrency / RPS capacity sweep (`pkg/api/capacity_sweep.go`, `pkg/api/testcreation.go`).
- **Phase 2 — Engine Aggregation, Distributed Wire, SLAs, and Live Dashboard (WP2.1–WP2.4)**:
  - **WP2.1**: `engine.LLMSummary` with mergeable `DurationSketch` (TTFT/TPOT/E2E) and 144-bucket `DiagnosticDistribution` (ITL), plus gRPC protobuf `LLMSummary` and `MetricsBatch.llm_summary_version = 45` (`pkg/distributed/generatorrpc/llm_summary.go`, `api/proto/generator.proto`).
  - **WP2.2**: Goodput & LLM SLA gates (`pkg/runcontract/transaction_sla.go`, `pkg/runcontract/goals.go`, `pkg/api/archive_transaction_sla.go`) and persisted `test_interval_summaries.llm_json` (`pkg/storage/sqlite_schema.go`, `pkg/storage/schema_migrations.go`).
  - **WP2.3**: Run Report LLM panel (`dashboard/src/components/RunReportLLMPanel.tsx`), live LLM chart tab (`dashboard/src/components/TestExecutionCharts.tsx`), and generator self-check / incomplete peer banner (`dashboard/src/components/TestExecutionStatusPanels.tsx`).
  - **WP2.4**: Dashboard goal forms (`dashboard/src/components/goalCeilings.ts`, `dashboard/src/components/CreateTestModal.tsx`).
- **Phase 3 — Quality Under Load & Capacity / Economics Reporting (WP3.1–WP3.6)**:
  - **WP3.1**: Inline structural & quality guards (`json_schema`, refusal/truncation classification, golden-prompt drift in `pkg/protocol/http/llm_request.go` and `pkg/api/report_llm.go`).
  - **WP3.2**: Redacted evaluation sample export (`GET /api/v1/tests/{id}/archive/llm-samples` in `pkg/api/handlers_llm_perf.go`).
  - **WP3.3**: Capacity sweep report (`pkg/api/capacity_sweep_report.go`) with knee-of-curve detection and max sustainable rate.
  - **WP3.4**: Token unit economics (`pkg/llmperf/econ/econ.go`, `pkg/api/handlers_llm_perf.go`) and `ProjectSettings.LLMDeployments`.
  - **WP3.5**: Replica & PTU capacity planner (`pkg/llmperf/plan/plan.go`).
  - **WP3.6**: Capacity sweep & economics dashboard panel (`dashboard/src/components/CapacitySweepPanel.tsx`).
- **Phase 4 — Server Telemetry Correlation & Inference RCA (WP4.1–WP4.3)**:
  - **WP4.1**: Curated Prometheus metric packs (`pkg/prometheus/packs.go`) and collector cloud telemetry queries (`pkg/collectoragent/cloud.go`).
  - **WP4.2**: Inference RCA signatures (`signatures.v2` in `pkg/rca/signatures/inference.go` and `pkg/rca/hypotheses.go`).
  - **WP4.3**: End-to-end latency decomposition & Little's Law in-flight concurrency (`pkg/api/llm_latency_decomposition.go`, `dashboard/src/components/LLMLatencyBreakdown.tsx`).
- **Phase 5 — Advanced Workflows & Polish (WP5.5–WP5.7)**:
  - **WP5.5**: Paired bootstrap A/B run and group comparison (`pkg/llmperf/compare/compare.go`, `dashboard/src/components/LLMComparisonPanel.tsx`).
  - **WP5.6**: OpenTelemetry `gen_ai.*` span emission (`pkg/engine/vuser_execute.go`) and archive span lookup (`pkg/api/genai_span_lookup.go`).
  - **WP5.7**: MCP server tools and workflow schemas (`mcp/apexload_mcp/tools.py`, `mcp/generate_schemas.py`, `mcp/apexload_mcp/input_schemas.json`).

---

## 7. Key Design Decisions & Defaults (D1–D10)

### Core Architecture Decisions (1–12)

1. **Both a dedicated `llm_request` DSL action and a composable `stream_profile` on `http_request`**: `llm_request` provides zero-boilerplate authoring for standard inference endpoints while `http_request` with `stream_profile` covers custom RAG/agent gateways.
2. **Exact metric definitions**: `TTFT` measures request write completion to the first non-empty content/reasoning/tool-call token chunk; `TPOT = (last_token - first_token) / (output_tokens - 1)` for `output_tokens >= 2`; `ITL` uses a mergeable 144-bucket `DiagnosticDistribution` (`pkg/protocol/grpc_diagnostics.go`, `pkg/protocol/llm_diagnostics.go`).
3. **Server-reported `usage` first, calibrated byte heuristic fallback**: No Cgo or multi-megabyte BPE dictionaries in VUser goroutines.
4. **Target scope**: OpenAI-compatible SSE/NDJSON, Azure OpenAI, TGI, Ollama, Triton/TRT-LLM, Vercel AI Data Stream, and LangServe; AWS Bedrock binary event-stream is out of scope.
5. **Dual-layer quantile storage**: `engine.DurationSketch` (0.5% relative error) for TTFT, TPOT, and E2E; `DiagnosticDistribution` (144 log-spaced buckets) for per-token ITL.
6. **Goodput as the north-star metric**: Requests meeting TTFT, TPOT, and E2E SLOs simultaneously without truncation, refusal, error, or schema failure.
7. **Mixed-version generator fleet safety**: `MetricsBatch.llm_summary_version = 45` (`CurrentLLMSummaryVersion = 1`) and `LLMSummary.Incomplete` when a peer is older or silent, plus `LLMSummary.NumbersOnly()` on live WebSocket/REST payloads (`pkg/api/llm_live.go`).
8. **Data-source and memory safety**: Bounded JSONL offsets (`pkg/engine/data.go`, `pkg/dsl/validate.go`), bounded `conversation_var` history (`pkg/engine/llm_conversation.go`), and 64 KiB stream text tail ring buffer.
9. **Observability integration**: Curated PromQL metric packs (`pkg/prometheus/packs.go`) and W3C `traceparent` / `gen_ai.*` span lookup (`pkg/api/genai_span_lookup.go`).
10. **RCA `signatures.v2`**: Causal graph signatures for `kv_cache_saturation`, `gpu_compute_saturation`, `inference_queueing`, and `rate_limited` (`pkg/rca/signatures/inference.go`).
11. **Disposable local simulator (`cmd/apexllm-target`)**: Deterministic continuous-batching and fault injection for CI and local verification (`pkg/internal/llmfixture`).
12. **PII-redacted datasets and samples**: `pkg/scrub` redaction on dataset import and evaluation sample export.

### Decisions with Defaults (D1–D10)

| # | Question | Default used by this plan |
|---|---|---|
| **D1** | **429 classification**: When `honor_retry_after: true` succeeds on retry, should the parent transaction be `Success: true` with `ThrottledAttempts > 0`, or a separate outcome? | `Success: true`, `Attempts: k`, `ThrottledAttempts: k-1`, `RetryWaitMs: sum`; unrecovered 429s are `Success: false, Throttled: true`. Both feed `throttle_rate_pct`. |
| **D2** | **SSE `data:` whitespace fix**: Change `strings.TrimSpace` in existing `pkg/protocol/http/sse.go` or only in `pkg/protocol/http/llm_stream.go`? | Keep `pkg/protocol/http/sse.go` behaviour if existing tests rely on it; `pkg/protocol/http/llm_stream.go` strips at most one leading space after `data:` per the WHATWG SSE spec. |
| **D3** | **Reasoning models**: Do we want `first_visible_token_ms` as an SLA metric in Phase 2, or diagnostic-only first? | Diagnostic + archive report column in Phase 2; SLA metric in a follow-up if needed. |
| **D4** | **SLM/LLM judge**: Should APEX Load ever make outbound judge calls during or after a run? | **No** by default (aligns with `AGENTS.md` policy-gated external LLM stance). Inline deterministic guards + redacted JSONL export cover quality evaluation without external LLM calls. |
| **D5** | **Agentic prompt generation**: Should the deterministic assisted-authoring engine (`pkg/convert` / MCP) generate `llm_request` drafts from OpenAPI specs that contain `/v1/chat/completions`? | Yes, as a small rule in `pkg/convert/llmapi/transform.go` (`pkg/internal/importreview/handlers_web_api_imports.go`) when path/schema matches OpenAI chat completions; purely deterministic. |
| **D6** | **Prompt and response retention**: May raw prompt or completion text ever be stored in run archives? | Off by default (`store_Hashes_only`). Opt-in sampled JSONL export (`llm_samples.jsonl`) runs through `pkg/scrub` before disk write. |
| **D7** | **Prices**: Ship a static price table or require operator entry? | Operator-entered in `ProjectSettings.LLMDeployments` (with 3 clearly-labelled example presets in the UI Marked "example — verify current pricing"). |
| **D8** | **A/B default**: Multi-group single run or two-run comparison first? | Two-run comparison first (works when testing two configurations of the *same* GPU cluster sequentially); multi-group comparison reuses the same bootstrap math. |
| **D9** | **Dataset import**: Upload files into project assets or reference local paths? | Upload through `pkg/fileasset` into project data assets (`data_jsonl_v1`), respecting existing archive and asset quotas. |
| **D10** | **Azure outbound workload identity in `http_oauth2_token`**: Include federated token exchange in this program or keep Bearer/API-key for Phase 0? | Phase 0 uses `api-key` and standard `http_oauth2_token` `client_credentials` against `https://cognitiveservices.azure.com/.default`; federated workload identity assertion can be added as an independent enhancement to `pkg/protocol/http/oauth.go`. |
