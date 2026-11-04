# LLM Inference Performance Testing

APEX Load provides first-class performance testing, capacity planning, and root-cause analysis for large language model (LLM) inference endpoints and AI applications.

---

## 1. Supported Targets

APEX Load's `llm_request` action (`pkg/dsl/catalog_llm.go`) and streaming parser (`pkg/protocol/http/llm_stream.go`) support three primary target families:

1. **Self-hosted OpenAI-compatible servers (`openai_chat`, `openai_completions`, `openai_responses`, `tgi_generate`, `ollama_chat`, `triton_generate`)**:
   - vLLM, HuggingFace Text Generation Inference (TGI), NVIDIA NIM / TensorRT-LLM (Triton), and Ollama over SSE (`text/event-stream`) or NDJSON (`application/x-ndjson`).
   - Automatically injects `"stream_options": {"include_usage": true}` when streaming OpenAI-compatible chat completions so server-reported prompt, completion, reasoning, and cached token counts are captured (`pkg/protocol/http/llm_request.go`).
   - `openai_responses` (also chosen for a target path ending in `/responses`, including Azure `/openai/v1/responses`, when the dialect is unset or a provider name such as `openai`, `azure`, `foundry` or `vllm`) builds a Responses API body (`input`, `instructions`, `max_output_tokens` raised to the API's minimum of 16, `text.format`) and parses its typed events: `response.output_text.delta` and reasoning deltas are timed, and the stream ends at the terminal `response.completed` / `response.incomplete` / `response.failed` event, which carries usage and the finish (`incomplete` with `max_output_tokens` is a `length` finish). A failed, cancelled, or otherwise incomplete response fails the request.
   - Triton `generate_stream` sends no terminal event: a stream that ends cleanly is complete. A TGI request asks for `details`, without which TGI reports no finish reason or token count; a TGI stream without them ends complete at its final `generated_text` event.
2. **Azure OpenAI and Azure AI Foundry (`azure_openai_chat`)**:
   - Supports `api-key` and `Authorization: Bearer` headers, captures `x-ratelimit-remaining-tokens` / `x-ratelimit-remaining-requests` and `x-ms-region`, and honors `retry-after-ms` / `Retry-After` on HTTP 429 responses (`pkg/protocol/http/llm_headers.go`).
   - Supports client-side TPM/RPM dual-bucket quota pacing and AIMD 429 congestion control (`pkg/engine/quota_pacer.go`). The quota belongs to the deployment: one pacer per group on each generator, shared by its closed, open-model, parallel-branch and async users, paces that generator's share (1/N of the quota across N generators) and only LLM actions (`llm_request`, or an HTTP step with `params.llm`, `params.llm_dialect` or `params.stream_profile`; a plain HTTP call to a model is not paced). Each group has its own pacer, so two groups calling one deployment each pace the full quota. A `Retry-After` on a 429 or 503 starts a cooldown capped by `retry_after_max_ms` (default 60 s). With `adaptive_aimd`, the rate probes above the configured quota (up to 4×) by +5% per second free of throttles and low `x-ratelimit-remaining-tokens`, and backs off by 0.7× at most once per second however many requests are throttled at once; it climbs only while the pacer is holding requests back, and holds through a Retry-After cooldown. A pause holds requests already queued for quota. The time an LLM action spends queued for quota is client-side pacing, so transaction times leave it out, as they leave out think time. Each generator paces an equal share of the quota; a closed-model group whose users are spread unevenly across generators (capacity-weighted placement, a sweep's first levels) under-delivers the quota, so run quota soaks on one generator or with `adaptive_aimd`.
3. **AI applications, RAG pipelines, and agents (`vercel_ai_data_stream`, `langserve_sse`, `raw_text`, `custom`)**:
   - Supports custom `stream_profile` JSONPath extraction (`pkg/protocol/http/llm_dialects.go`) and multi-turn conversational state (`conversation_var`, `max_history_turns`, `max_context_tokens` in `pkg/engine/llm_conversation.go`) with per-hop LLM roll-ups inside `source_transaction` and `http_sequence`.

> **Scope note**: AWS Bedrock binary event-stream and SigV4 signing are out of scope. Token counts use server-reported `usage` when available and fall back to a deterministic `~4 bytes/token` estimate (`tokens_estimated = true`) without external tokenizer dependencies.

---

## 2. Core Metrics & Diagnostics

Every `llm_request` action records `protocol.LLMDiagnostics` (`pkg/protocol/llm_diagnostics.go`) on `ServerTelemetry.LLM` and aggregates into `engine.LLMSummary` (`pkg/engine/llm_summary.go`):

- **Time to First Token (`ttft_ms`)**: Elapsed milliseconds from the request's start (including connection setup) to the first non-empty content or reasoning chunk (excludes role-only preamble chunks and SSE keepalive comments).
- **Time to First Visible Token (`first_visible_token_ms`)**: Elapsed milliseconds to the first user-visible content token. When a model streams reasoning before its answer, this comes after `ttft_ms`, and the gap between them is the time the model spent reasoning.
- **Time per Output Token (`tpot_ms`)**: `(last_token - first_token) / (output_tokens - 1)` when `output_tokens >= 2`, capturing pure decode cadence independent of prefill latency. Which tokens it spans depends on whether the model streams its raw reasoning:
  - It streams raw reasoning (chat `reasoning_content`, Responses `reasoning_text`, Ollama `thinking`): TPOT runs from the first token of either kind to the last, over all output tokens, reasoning included. A Responses reasoning summary counts as raw reasoning only when the server reports no reasoning tokens, as Ollama's `/v1/responses` does with a thinking model's whole reasoning.
  - It does not (OpenAI and Azure o-series and gpt-5 send no reasoning, or only a summary): TPOT runs from the first visible token to the last, over the output tokens less the reported `reasoning_tokens`, so it is the visible answer's cadence.
- **Inter-Token Latency (`itl_ms`)**: Mergeable 144-bucket `DiagnosticDistribution` (`pkg/protocol/grpc_diagnostics.go`, `pkg/protocol/llm_diagnostics.go`) of inter-chunk gaps; its `max` is the request's longest gap. The run report adds `stall_count`, the number of streamed requests whose longest gap exceeded a fixed 1000 ms, and `max_stall_ms`, the longest gap in the run.
- **Goodput (`goodput_pct`; a capacity sweep's per-level `meets_slo`)**: Percentage of requests that complete without error, a failing truncation (a `length` finish, unless `fail_on_truncation: false` accepts it or `ignore_eos` requested it), refusal (`finish_reason = "content_filter"`), or schema failure while meeting configured `slo_ttft_ms`, `slo_tpot_ms`, and `slo_e2e_ms` targets.
- **Which requests count**: Latency distributions and goodput use successful, unthrottled requests; counters (requests, errors, throttles, truncations) count every LLM request, including one that failed before any response (`error_code: transport`). A stream the client cut off (a stream timeout, even after a `length` finish, or a stream that ends before the server reports a finish) always fails. A `length` finish fails by default; `fail_on_truncation: false` accepts it (still counted as truncated), and under `ignore_eos` it is the requested stop and not a truncation.

---

## 3. Workloads, Datasets, and Capacity Sweeps

- **Arrival Processes**: Open-model groups (`pkg/engine/open_workload.go`, `pkg/engine/arrival_process.go`) support `"arrival_process": "uniform" | "poisson" | "gamma"` (with `"burstiness"` as the gamma shape k, so the gaps' squared coefficient of variation is $c_v^2 = 1/k$, the vLLM `--burstiness` convention) and per-stage `target_tps` plus `warmup` exclusion (`pkg/dsl/types.go`). Arrivals follow the integrated rate: a ramp from 0 to 10 TPS over an hour offers about 18,000 arrivals, and each stage keeps its own. Stages compile to rate stages only when at least one sets `target_tps`; stages that set only `target_vusers` (a goal run, a concurrency sweep) leave the script's own `tps` running. Among rate stages, a stage without `target_tps` holds the current rate (the script's `tps` before any rate stage), and a `ramp_down` that sets neither `target_tps` nor `target_vusers` ramps it to zero. The controller's stage schedule only labels each level, because the generators' arrival scheduler runs the rate stages. A rate stage runs at most an hour, so a longer stage compiles to consecutive pieces along the same line; a group's stages may need at most 1,024 such pieces (about 42 days).
- **Prompt Datasets (`data_jsonl_v1`)**: Import ShareGPT, OpenAI chat JSONL, or plain prompt JSONL via `POST /api/v1/llm/prompt-datasets/import` (`pkg/llmperf/dataset/dataset.go`; `scrub_pii`, on by default, redacts credentials in the text, not general PII), synthesize token-length distributions via `POST /api/v1/llm/prompt-datasets/synthesize` (`pkg/llmperf/synth/synth.go`), or calibrate `bytes_per_token` via `POST /api/v1/llm/prompt-datasets/calibrate` (`pkg/api/handlers_llm_perf.go`). Calibration from a run (`test_id`) divides the visible answer's characters by the server's completion tokens less its reasoning tokens. It skips requests whose completion count was estimated, whose reasoning share was estimated (reasoning text returned without a count of it, as vLLM, SGLang and Ollama do), or that made tool calls; a request whose prompt count alone was estimated is kept. An import keeps at most 50,000 records and 32 MiB of normalized JSONL; a multi-turn conversation repeats its history in every turn, so a larger result is cut and reported (`truncated`, `truncation_reason`: `max_records` or `max_bytes`), and `max_turns` keeps conversations short. A synthesized dataset over the same 32 MiB is refused. The compare and calibrate routes read only tests in the route's `project_id`.
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
| **Author** | Prompt dataset loader: ShareGPT, OpenAI chat JSONL, custom JSONL (`data_jsonl_v1`) | P0 | M | Implemented | `pkg/llmperf/dataset/dataset.go`, `pkg/engine/data.go`, `pkg/dsl/validate.go`, `pkg/api/handlers_llm_perf.go` |
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
2. **Exact metric definitions**: as in section 2, `TTFT` measures from the request's start (including connection setup) to the first non-empty content or reasoning chunk; `TPOT = (last_token - first_token) / (output_tokens - 1)` for `output_tokens >= 2`, over all output tokens when the model streams its raw reasoning, and otherwise over the visible answer, leaving out the reasoning tokens it did not stream; `ITL` uses a mergeable 144-bucket `DiagnosticDistribution` (`pkg/protocol/grpc_diagnostics.go`, `pkg/protocol/llm_diagnostics.go`).
3. **Server-reported `usage` first, calibrated byte heuristic fallback**: No Cgo or multi-megabyte BPE dictionaries in VUser goroutines.
4. **Target scope**: OpenAI-compatible SSE/NDJSON, Azure OpenAI, TGI, Ollama, Triton/TRT-LLM, Vercel AI Data Stream, and LangServe; AWS Bedrock binary event-stream is out of scope.
5. **Dual-layer quantile storage**: `engine.DurationSketch` (0.5% relative error) for TTFT, TPOT, and E2E; `DiagnosticDistribution` (144 log-spaced buckets) for per-token ITL.
6. **Goodput as the north-star metric**: Requests meeting TTFT, TPOT, and E2E SLOs simultaneously without a failing truncation, refusal, error, or schema failure.
7. **Mixed-version generator fleet safety**: `MetricsBatch.llm_summary_version = 45` (`CurrentLLMSummaryVersion = 1`) and `LLMSummary.Incomplete` when a generator predates LLM summaries (decided by the batch version; every LLM request, including a transport failure, carries diagnostics), plus `LLMSummary.NumbersOnly()` on live WebSocket/REST payloads (`pkg/api/llm_live.go`).
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
