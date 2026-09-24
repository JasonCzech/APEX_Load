# Roadmap

Status: Roadmap. Planned and in-progress engineering work with the current state of each track. Landed behavior is described in the guides linked from [INDEX.md](INDEX.md); this file records only what is decided, what has landed toward it, and what remains.

Source note: consolidated 2026-09-24 from the former `RUNTIME_TELEMETRY_SLA_PLAN.md`, `PROTOCOL_TELEMETRY_PARITY_PLAN.md`, `SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md` and `PT_ANALYSIS_PARQUET_IMPORT_PLAN.md`. The CloudTest program stays in its own document because a Go test validates it against the machine-readable ledger (see [CloudTest outcome parity](#cloudtest-outcome-parity)). Messaging roadmap items are in [MESSAGING_PLAN.md](MESSAGING_PLAN.md).

## Track status

| Track | Status | Open work |
| --- | --- | --- |
| [Runtime telemetry SLA](#runtime-telemetry-sla) | Twelve implementation slices landed | Correlation/dependency metadata for remaining protocols; dependency maps and error-pattern cards |
| [Protocol telemetry parity](#protocol-telemetry-parity) | Shared `ServerTelemetry` baseline landed; no protocol has full Layer 3 | WebSocket and gRPC L2/L3, Java HTTP object-vs-wire delta, JMS ack latency and broker-managed DLQ discovery, intelligence layer |
| [Script authoring consolidation](#script-authoring-consolidation) | Phases 1-2 done; 3-5 in progress | Scenario-assembly tightening, reopen-after-save consistency, parity pass and adapter removal |
| [PT Analysis Parquet import](#pt-analysis-native-parquet-import) | Proposed; no DuckDB/Arrow dependency in `go.mod` | Everything |
| [CloudTest outcome parity](#cloudtest-outcome-parity) | Active; initial vertical slices implemented | Fixture-proven import mappings, run-bound monitoring, media, chart export coverage |

---

## Runtime telemetry SLA

Goal: meet the documented under-2-second collection-to-dashboard target while improving the data retained for post-run analysis. Design rule: summary metrics travel the hot path, diagnostic samples a bounded near-real-time path, and full-fidelity records go to durable archives.

Baseline already captured: transaction timings/bytes/status/errors, HTTP timing phases, aggregated TPS/error rate/percentiles, per-action and per-group summaries, system CPU/memory/network/disk, goal-state and threshold evaluation, network latency probes, JSONL/Parquet transaction, resource and interval archives, and browser snapshots.

Phases: 0 lock the SLA budget as composed stage budgets (capture, aggregation, serialization, delivery, render); 1 pipeline hardening (drop counts, bounded in-memory history, health in payloads); 2 split live summary from diagnostic detail; 3 typed retention with schema versions; 4 application-performance depth (correlation IDs, dependency classification, freshness, baseline comparison).

Landed so far:

- Collector and websocket drop counters plus bounded in-memory test history; telemetry health surfaced in live payloads and the execution Telemetry Health card.
- Freshness stamps: `collected_at`, `published_at`, `sample_age_ms` server-side, with dashboard `client_received_at`, `delivery_lag_ms` and `end_to_end_age_ms`. Distributed batches preserve generator collection/publish timestamps; the controller computes generator processing, transport and fan-out lag stages.
- `metrics_update` stays compact; sampled `telemetry_diagnostic` events fire on staleness, drops and recovery, and carry slow/error transaction hotspots (name, dependency metadata, bounded correlation IDs). The dashboard keeps a bounded diagnostic list in Monitoring.
- Typed SQLite interval summaries with explicit `schema_version` and migration/backfill, carrying the lag-stage columns and run-level telemetry health.
- `protocol.Result` carries `correlation_id`, `dependency_type`, `dependency_target`; HTTP and JMS classify them; engine rollups, distributed batches, archives and Parquet retain them, and archive queries filter on them.
- Configurable SLA budgets in system settings (defaults: 2000 ms end-to-end sample age, 500 ms per stage), applied to diagnostics; run-level `telemetry_health` moves `degraded` then `breached` after the sustained window (data loss breaches immediately) and recovers with a healthy transition.
- Webhooks can subscribe to `telemetry_degraded`, `telemetry_breached`, `telemetry_recovered`; Slack, Teams and Dynatrace payloads carry severity/summary/conditions; websocket consumers get `telemetry_health_changed`.
- Archive reports aggregate telemetry status, healthy/degraded/breached interval counts, sustained breaches, dropped samples, websocket drops, max lag per stage, breach timestamps and condition counts; HTML reports show a Telemetry Health overview metric.

Open: expand correlation/dependency metadata to protocols beyond HTTP and JMS; add dependency maps and error-pattern cards to live monitoring; sustained SLA-breach and baseline-versus-current reporting remain limited.

---

## Protocol telemetry parity

Goal: bring every protocol to the three-layer depth that [BROWSER_ROADMAP.md](BROWSER_ROADMAP.md) defines for the browser, so each protocol answers "why was it slow, where did time go, how does it compare to baseline".

| Layer | Browser | HTTP / SOAP / Java HTTP | WebSocket | gRPC | JMS |
| :--- | :--- | :--- | :--- | :--- | :--- |
| L1 Execution | Device emulation + touch | Request/response cycle | Connection lifecycle | RPC invocation | Message pub/sub |
| L2 Deep telemetry | Core Web Vitals, JS heap, layout counts | Timing phases, response decomposition, dependency chain | Message latency, throughput, backpressure | Per-method timing, stream throughput, protobuf size | Queue depth, broker latency, consumer lag |
| L3 Intelligence | Frustration detection, device comparison | SLA breach detection, dependency health, decomposition | Connection stability, throughput regression | Method hotspots, error classification | Broker health correlation, dead-letter analysis |

Question classes each protocol must answer: where did time go (DNS/TCP/TLS/TTFB/server processing/transfer), what is degrading (TTFB regression, error spikes, dependency slowdown), which variant is worst (endpoint/operation/broker region), app versus infrastructure (DNS spikes, TLS renegotiation, pool exhaustion), and SLA compliance (P95, error rate, throughput).

Anchors: `pkg/protocol/protocol.go` (`protocol.Result`, `ClientMetrics`, `ServerTelemetry`), `pkg/engine/metrics.go` (rollups, comparisons, SLA evaluation), `pkg/api/runner.go`, `archive.go`, `archive_parquet.go`, `pkg/distributed/generatorrpc/` plus `distributed_execution_backend.go`, and `dashboard/src/types.ts` with the execution/archive/comparison components. New telemetry extends these surfaces rather than a parallel stack.

Landed:

- Protocol-neutral `ServerTelemetry` on `protocol.Result` (pointer, nil when unused), propagated through engine rollups, controller/generator RPC, archives and dashboard contracts. Per-transaction and per-dependency SLA status derives from goal-config thresholds, including server-processing and transfer thresholds with stop-on-breach.
- HTTP: timing decomposition, payload size classes, connection reuse, redirect-hop timing, live response-decomposition and stacked phase views, dependency-health aggregation with P95/P99 and error rate, action/dependency baseline deltas. No further HTTP gaps are tracked for captured telemetry.
- SOAP: operation tagging, fault categorization by code, namespace-conflict detection, envelope-overhead tracking, live per-operation aggregation and cards, archive grouping/drill-down/fault distribution and minute timelines. SOAP has the full analytical surface the captured telemetry supports.
- Java HTTP: dependency tagging, helper/codec metadata, raw codec timing retention.
- JMS: payload sizing and size-class distributions, request/reply timing, message counts and throughput, message latency, consumer-lag retention and live trend, messaging cards, sampled broker-pressure diagnostics, topic fan-out analysis, dead-letter diagnostics for explicitly classified DLQ destinations.
- Previous-run comparison baselines on `GetTest`, used by the execution summary and server telemetry panel.

Open work (effort estimates from the original plan):

| Protocol | Item | Layer | Location | Effort |
| :--- | :--- | :--- | :--- | :--- |
| Java HTTP | Helper-reported object footprint vs serialized wire size | L2 | `java_http/java_http.go`, helper contract | 1 day |
| Java HTTP | Codec overhead chart (serialization share of total time) | L3 | `dashboard/` | 1 day |
| WebSocket | `WebSocketTelemetry`/shared fields, handshake timing, per-message send/receive timestamps, write-buffer depth and backpressure flag, ping/pong latency | L2 | `protocol.go`, `websocket/websocket.go` | ~3.5 days |
| WebSocket | Connection health score, dashboard cards, throughput regression alerts, P50/P90/P99 per message type, dead-connection detection | L3 | `engine/metrics.go`, `dashboard/`, `api/` | ~5 days |
| gRPC | Per-method timing via interceptors, stream message counter and throughput, protobuf marshal timing, status distribution, HTTP/2 stream and metadata timing | L2 | `grpc/grpc.go` | ~3 days |
| gRPC | Method hotspot detection, error classification, deadline-budget tracking and alerts, dashboard cards | L3 | `engine/metrics.go`, `dashboard/`, `api/` | ~4.5 days |
| JMS | Consumer acknowledgement latency; broker-side latency isolation; queue-depth sampling (provider management API) | L2 | `jms/jms.go` | 0.5 day + provider-specific |
| JMS | Automatic broker-managed dead-letter discovery / queue-depth correlation | L3 | `jms/jms.go`, `api/` | 1.5 days |

Shared infrastructure still to generalize: per-transaction SLA thresholds in the DSL evaluated protocol-agnostically (P90/P95/P99, max error rate, min throughput, max TTFB, consumer lag), and run-over-run baseline comparison (archive stores per-transaction P50/P90/P99; new runs load the latest baseline of the same script; `baseline_comparison` websocket event on threshold crossing). Baselines match by transaction name plus protocol and warn on added/removed transactions.

Variant comparison and card layout: each protocol's dashboard card follows hero metric (LCP / P95 / consumer lag), decomposition, trend sparkline, and SLA health indicator. Comparison dimensions: browser device profiles, HTTP/SOAP endpoints or operations, gRPC service methods, WebSocket connection scenarios, JMS queues/topics, and current run versus baseline for all.

| Stage | Scope | Effort | Dependencies |
| :--- | :--- | :--- | :--- |
| 1 | Shared foundation (`ServerTelemetry`, SLA, baseline) | 5-7 days | None (landed for HTTP) |
| 2 | HTTP, SOAP, Java HTTP deep telemetry | 5-7 days | Stage 1 (largely landed) |
| 3 | WebSocket and gRPC deep telemetry | 6-8 days | Stage 1 and the DevWeb L1 work in [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md#devweb-import) |
| 4 | JMS deep telemetry | 4-5 days | Stage 1 (largely landed) |
| 5 | Dashboard parity for all protocols | 7-10 days | Stages 2-4 |
| 6 | Intelligence layer (Z-score anomalies, dependency health, connection stability, broker correlation, cross-protocol correlation) | Ongoing | Stages 1-5 |

Stages 2-4 are independent after Stage 1. Risks: `ServerTelemetry` growth (keep a nil pointer, benchmark); SLA config friction (optional, sensible defaults); baseline mismatches across script versions; UI surface multiplication (shared card layout, protocol selector); JMS broker metrics needing provider APIs (start client-observable, broker APIs are L3).

Related: [BROWSER_ROADMAP.md](BROWSER_ROADMAP.md) is the most complete existing L2 example; [WEB_API_EXECUTION_SUPPORT_MATRIX.md](WEB_API_EXECUTION_SUPPORT_MATRIX.md) records the shipped L1 SOAP and Java HTTP support.

---

## Script authoring consolidation

Goal: consolidate script building around shared contracts and lifecycle without collapsing distinct entry points (direct editing, import, recording, replay/debugging) into one UI. Test and scenario assembly is a separate, adjacent track.

Principles: consolidate contracts, not inputs; validation has one owner (same result from disk, API, replay or import); importers differ only at acquisition and converge on one review/edit/save contract; script authoring and scenario assembly stay separate.

| Phase | Scope | Status |
| --- | --- | --- |
| 0 Lock boundaries | Classify surfaces (authoring, replay/debug, scenario assembly); confirm live route owners | Done; duplicate page copies and patch/temp artifacts removed |
| 1 Unify validation | One DSL-owned validator used by parser, create/update/validate, draft and stored replay, browser replay, import save; JMS rules exposed through it | Done (shared DSL validator); the cross-entry-point validation-parity matrix is still being completed |
| 2 Standardize import contracts | Shared envelope (scripts, warnings, stats, correlations, assumptions, unsupported elements, scenario draft) across Enterprise Script Archive, JMeter, sample and recorder; lifecycle acquire, validate, review, edit, save | Done backend and shared frontend model; typed envelopes consumed directly, generic normalizer and JMeter response adapter removed |
| 3 Frontend orchestration | Script hub delegates to a shared authoring shell; shared review/warnings/stats/edit-handoff/save; main script editor is the canonical edit surface | In progress: authoring-state hook, `ScriptsAuthoringShell`/surface, shared review/save panel, recorder preview-first flow, persisted-script handoff reopening the editor (including multi-script import follow-on notices); route-level selection still in `ScriptsPage` |
| 4 Scenario assembly | Reusable assembly state shared by new-test creation and execution-time scenario editing | In progress: one `ScenarioDraftState` reducer in `CreateTestModal`, shared payload normalization with `TestHub`, queued-run revise-copy flow, and `TestExecution` decomposed into hooks/helpers (polling, archive, live controls, metrics stream, chart model, action bridge, phase model) |
| 5 Cleanup | Remove stale duplicates and temporary normalization/migration adapters after parity checks | Started; adapter removal pending the parity pass |

Other landed items: Enterprise Script Archive import exposes `preserve_transactions` and a validate-only review path and promotes unsupported functions/runtime review data into standard `unsupported_elements` and `summary`; a first-class draft-validation endpoint backs one validation action in the editor; editor replay and archive browser-replay validation share one request builder; the JMeter review uses the shared review shell.

Remaining high-value work: finish the scenario-assembly path around `TestExecution` and shared execution/result models; tighten remaining reopen-after-save inconsistencies; run the end-to-end parity pass and then delete remaining temporary adapters.

Verification checklist still open: validation matrix proving identical outcomes across parser, create, update, draft/stored validation, replay, browser replay and import save; snapshot of pre-standardization import payloads versus the shared envelope; same lifecycle (acquire, review, edit, save, reopen) on every authoring path; scenario editing from both new-test creation and execution-time editing; final check that no route or option references deleted files or adapters.

Decisions: keep multiple entry points; main script editor is the default canonical edit destination; the richer JMeter result model is the baseline for the shared envelope; keep authoring and scenario assembly separate; do not force browser replay into the shared model unless its lifecycle aligns. Risks: adapter sprawl, frontend churn before backend contracts stabilize, and cleanup regressions if adapters are deleted before parity verification. Validator unification was the recommended first slice.

Key code: `pkg/dsl/parser.go` and `types.go`; `pkg/api/handlers_scripts.go`, `handlers_recorder.go`, `handlers_replay.go`, `handlers_browser_replay.go`; converters under `pkg/convert/{enterprisearchive,jmeter,sample}` and `pkg/recorder`; dashboard `ScriptsPage`, `ScriptEditor`, importer/recorder components, `CreateTestModal`, `TestExecutionScenarioEditor`. See also [ENTERPRISE_CONTRACTS.md](ENTERPRISE_CONTRACTS.md).

---

## PT Analysis native Parquet import

Status: Proposed. No DuckDB/Arrow dependency exists in `go.mod` yet.

Decision: import Parquet natively, preserve the original file, and normalize its meaning into the dashboard's canonical analysis model. Do not analyze arbitrary Parquet schemas directly, and do not convert to CSV/JSON as the primary path (that loses schema, types, compression and efficient column scans); conversion is only for exports, debugging or an optimized cache.

Why Parquet: columnar for analytical queries, strongly typed (timestamps, metrics, dimensions), compressed, widely supported (DuckDB, Arrow, Polars, Spark, Python, cloud analytics), and suited to multi-file partitioned datasets.

Architecture: Parquet files, native reader (DuckDB/Arrow/Polars), schema detection and field mapping, canonical analysis tables, correlation engine and dashboard queries. Steps: accept files in the import workflow; keep the original as an artifact; infer schema and preview columns; map columns to canonical fields; validate required fields (time and scenario/run identity); load normalized data; query through DuckDB/Arrow; materialize correlation views only when needed.

Two storage layers: a raw artifact layer (original files unchanged, for reprocessing, auditing, debugging mappings) and a normalized analysis layer (mapped, validated records for fast queries, reliable joins, cross-scenario comparison, system-metric correlation). DuckDB fits as a direct query layer over files, a staging engine during import, or a local analytical cache.

Canonical fields: `run_id`, `scenario_id`, `scenario_name`, `timestamp_utc`, `relative_time_ms`, `source`, `host`, `service`, `metric_name`, `metric_value`, `unit`, `transaction_name`, `status`, `error_type`, `tags`, `labels`. Source-column aliases map to shared concepts, for example `elapsed`, `response_time`, `duration_ms`, `latency` to `response_time_ms`, and `cpu_pct`, `processor_time` to `cpu_percent`.

Correlation requirement: every dataset supports both absolute time (`timestamp_utc`) and test-relative time (`relative_time_ms`), enabling questions such as whether CPU saturation preceded a checkout latency spike, whether scenario 2 failed after scenario 1 raised load, or whether network latency tracked response time. Correlation dimensions: time window, run, scenario, transaction, host, service, region/environment, metric type, error category. Derived views: scenario response time and transaction percentiles by time bucket, error rate by scenario/transaction, host CPU/memory/network by bucket, scenario latency versus resource use, cross-scenario contention.

Import workflow: user selects Parquet files; dashboard inspects schema and previews columns; auto-detects common fields; user confirms or edits mappings; validation; import and normalize; expose for comparison and correlation.

Minimum viable scope: native reading, schema inspection, mapping configuration, required-timestamp validation, a normalized `metric_points` table (`id`, `run_id`, `scenario_id`, `timestamp_utc`, `relative_time_ms`, `source`, `host`, `service`, `transaction_name`, `metric_name`, `metric_value`, `unit`, `status`, `tags_json`), scenario and system metric import, and time-bucketed correlation queries.

Follow-on: saved mappings per source/tool, automatic unit normalization, partition-aware and directory batch imports (partitioned by `run_id`, `scenario_id`, date or metric type), correlation scoring across series, lag analysis (do system metrics lead or trail degradation), and derived metrics (error rate, throughput, p95, saturation).

---

## CloudTest outcome parity

Status: Active; initial vertical slices implemented. The full program (evidence policy, corrected baseline, phases 0-5, rollout gates) is [CLOUDTEST_PARITY_PROGRAM.md](CLOUDTEST_PARITY_PROGRAM.md). It stays a separate document because `TestCloudTestProgramDocumentsEveryCanonicalCapability` in `pkg/api/parity_status_test.go` requires every capability ID from `pkg/api/cloudtest_parity_catalog.json` to appear in it. The JSON ledger remains the only editable capability list and is published at `GET /api/v1/system/parity-status` (`cloudtest_capabilities`).

| Phase | Scope | State |
| --- | --- | --- |
| 0 Evidence and fixtures | Evidence-linked ledger; three sanitized CloudTest exports | Ledger, comparison view and dashboard live; fixtures not supplied, so fixture-dependent mappings stay provisional |
| 1 Live regional analytics and safe detail | `by_region` aggregation; `config.results.message_detail` modes; archive filters and waterfall | Implemented; formal run annotations and the browser bucket-to-waterfall exit test in progress |
| 2 Composition authoring and shared analytics | `start_offset`, `control_switch`, `control_parallel`, native dashboards, property analytics | Largely implemented; fixture-proven parallel-repeat mapping and richer native panel authoring in progress |
| 3 CloudTest import | `POST /api/v1/scripts/import/cloudtest` and support matrix | Implemented review-only (`blocked_draft`, `fixture_proven: false`) pending fixtures |
| 4 Run-bound monitoring and elastic capacity | Monitor sources, signed collector jobs, ephemeral capacity, prepare mode | Not started |
| 5 Media, automation, reporting | `media_stream`, named-transaction SLA gates, DOCX, chart export | SLA gates, `apexctl library`, DOCX and report chart CSV/SVG landed; media and remaining chart export open |
