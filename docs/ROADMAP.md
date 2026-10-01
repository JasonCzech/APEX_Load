# Roadmap

Status: Roadmap. Planned and in-progress engineering work with the current state of each track. Landed behavior is described in the guides linked from [INDEX.md](INDEX.md); this file records only what is decided, what has landed toward it, and what remains.

Source note: consolidated 2026-09-24 from the former `RUNTIME_TELEMETRY_SLA_PLAN.md`, `PROTOCOL_TELEMETRY_PARITY_PLAN.md`, `SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md`, `PT_ANALYSIS_PARQUET_IMPORT_PLAN.md`, `MESSAGING_PLAN.md`, `BROWSER_ROADMAP.md` and `CLOUDTEST_PARITY_PROGRAM.md`. Older design sketches can be recovered from git history. Current support guidance lives in [Browser and mobile support](PROTOCOL_SUPPORT.md#browser-execution-and-mobile-emulation) and [Messaging support](PROTOCOL_SUPPORT.md#broker-and-messaging-support); benchmark procedure lives in [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-runbook).

## Track status

| Track | Status | Open work |
| --- | --- | --- |
| [Runtime telemetry SLA](#runtime-telemetry-sla) | Shared evidence, signed collector jobs, archives, overlays, lag correlation, live dependency maps and qualified findings implemented behind project flags; cloud adapters are previews | Live CloudWatch/Azure Monitor acceptance, broader provider coverage and cross-run/SLA reporting |
| [Protocol telemetry parity](#protocol-telemetry-parity) | gRPC codec/call diagnostics and WebSocket transport diagnostics propagate through live/archive/report/baseline paths | Cross-protocol intelligence, Java HTTP object-vs-wire delta, JMS ack latency and broker-managed DLQ discovery |
| [Script authoring consolidation](#script-authoring-consolidation) | Phases 1-2 done; 3-5 in progress | Scenario-assembly tightening, cross-entry-point lifecycle parity and adapter removal; canonical save/reopen and SLA reseeding fixes landed |
| [PT Analysis Parquet import](#pt-analysis-native-parquet-import) | Native import, saved mappings, bounded queries and comparisons implemented behind a project flag | 10 GB acceptance skipped at user direction; cross-platform release and broader scale acceptance remain unverified |
| [Messaging](#messaging) | Most of the program landed | Partition/subscription rollups, Event Hubs consumer ownership, provider depth, IBM MQ native (P2.5) and Solace SMF (P2.6) |
| [Browser and mobile](#browser-and-mobile) | Device emulation, client metrics, import MVP and UX health slice landed | Measured benchmark publication, WebKit process attribution, TrueClient fixtures, HAR V2, UX intelligence beyond thresholds |
| [CloudTest outcome parity](#cloudtest-outcome-parity) | Active; initial vertical slices implemented | Fixture-proven import mappings, additional analytics dimensions/live intersections, live provider acceptance, media and remaining chart exports |

## Completed implementation and remaining acceptance — 2026-10-01

Reconciled against published commits `0a480cee`, `7bf711ea` and `10e9f08e`. These are completed implementation slices, not closure of every parent track. Workflow contracts and configuration are in [Analysis workflows](ANALYSIS_WORKFLOWS.md) and [Protocol diagnostics](PROTOCOL_DIAGNOSTICS.md); the latest [review and validation record](qa/reviews/authoring-analytics-cloud-2026-10-01/REVIEW.md) records exact test outcomes and limits.

| Area | Completed implementation | Remaining backlog or acceptance |
| --- | --- | --- |
| Run monitoring | Shared metric evidence; revisioned bindings; signed jobs and sealed credentials; durable retry/acknowledgement; Linux/Prometheus, SSH, authenticated OTLP and restricted Java packs; archived evidence, overlays and lag correlation | Live acceptance for remote providers and broader customer-backed integrations |
| Diagnostics | Typed gRPC/WebSocket measurements, codec timing, dependency/error cards, baseline comparisons and qualified advisory anomaly delivery; WebSocket evidence preserved through telemetry cloning | Broader protocol-specific depth and live acceptance; unavailable transport phases remain unavailable |
| Preparation | Durable fenced lifecycle, immutable revisions, reservation/provisioning reuse, cancellation/expiry and capacity policies; ECS spot ceiling/on-demand floor; common zero-traffic regression coverage | Live zero-traffic, takeover and cleanup acceptance for every supported target/runtime combination |
| Parquet analysis | Embedded DuckDB build profile; resumable multi-file/directory imports, nested scalar mappings, immutable originals, saved mapping revisions, normalization, rejection provenance, bounded queries/rates, saved comparisons and lag correlation | Cross-platform release outcomes and broader scale evidence; 10 GB benchmark explicitly skipped, not passed |
| Authoring | Canonical stored-script reload after summary-only saves; scenario reseeding preserves group transaction SLA overrides | Shared scenario-assembly completion, lifecycle/validation parity and removal of remaining temporary adapters |
| Native panels | Project editors save run-metric and filtered archive panels; viewers reopen them; bounded raw timelines, same-project comparisons, bucket inspection and CSV/SVG as applicable | Live/archive grouping and overlays implemented in the current slice; acceptance and review in progress |
| Response properties | Explicit finite extraction categories, redaction-first promotion, capability negotiation, action metrics, distributed merges, JSONL/Parquet retention, archive filters/reports, bounded archived timelines, saved comparisons and bucket drill-down | Bounded live property history and single-dimension grouping implemented; live dimension intersections remain unavailable |
| Cloud collector previews | Identity-scoped CloudWatch/Azure Monitor queries, fixed approved destinations, bounded retries/responses, durable numeric-sample deduplication; delayed missing buckets can fill and equivalent timestamps are rejected | Approved live AWS/Azure acceptance before production-readiness claims |
| Messaging | Observed-zero versus unavailable lag in Kafka/Event Hubs; terminal maximum age/offset gates require complete evidence; Event Hubs auth/probe-group defaults and HOWTO | Partition/subscription views, processor/checkpoint ownership and provider-wide acknowledgement/depth telemetry |

The latest implementation passed full Go tests, affected race tests, dashboard typecheck/lint/unit tests and the repaired browser workflows in both auth profiles. The review record distinguishes the initial browser failures from successful post-fix reruns. Cloud readiness, release-platform coverage and scale results require their own evidence; local tests do not close those gates.

## Current slice: filtered native analytics and archive drill-down

Status: implemented and reviewed; no remaining actionable findings in this slice. The saved-panel workflow now lets a project user investigate a selected action, business transaction or approved response-property category instead of only run-wide totals. This builds directly on existing property capture, archive filters and versioned dashboard definitions without requiring new cloud infrastructure or proprietary fixtures.

- Implemented explicit population selection (action or business transaction), stable identity filters and approved property-category selection. Property filtering applies only to action observations, matching the shipped property metrics/report contract.
- Implemented bounded archived time-series queries for count, error rate and latency, reusing archive/query authorization and limits. Percentiles use raw observations, never averaged interval percentiles. Missing buckets remain gaps and unsupported query operations fail explicitly.
- Implemented saved query settings and selected-bucket drill-down into the existing filtered transaction inspector, preserving old run-total panel definitions and project viewer/editor permissions.
- Implemented a saved two-run comparison for the same population/filter, showing added/removed identities, missing evidence and workload/version compatibility warnings alongside separate series; automatic regression verdicts remain out of scope.
- Acceptance coverage: real JSONL fixtures reconcile bucket and inspector counts without action/business double counting and verify raw percentiles, second-viewer access, cross-project denial and legacy/missing-property warnings. Browser fixtures verify saved definitions, comparison warnings and inspection in both auth profiles. A full live mixed-category run/report reconciliation is not claimed. See the [review and validation record](qa/reviews/filtered-native-analytics-2026-10-01/REVIEW.md).

This completed slice is archive-first. The follow-on below adds bounded live selections and overlays; arbitrary dimensions, cross-provider messaging dashboards and live cloud acceptance remain separate. See [workflow semantics and limits](ANALYSIS_WORKFLOWS.md#filtered-archive-panels-and-comparisons).

## Follow-on slice: live/grouped panels and analytical overlays

Status: implemented and reviewed; affected checks and failure reruns passed (see the review record for broad-run limitations). Items 1–3 add durable bounded live interval evidence, 1–20 series grouped by supported identities, and project-authorized monitor/imported overlays with labelled unit axes and archive lag-correlation coverage. Independent live summaries reject intersections they cannot answer; live percentiles are never averaged. Old peers and source-plan runtimes without summaries report unavailable evidence. Fixed elapsed windows and bounded retained history remain explicit.

Remaining analytics breadth: live cross-dimension intersections, live percentile re-bucketing from mergeable distributions, additional dimensions, and live lag correlation. The existing saved two-run archive comparison remains available. See [workflow semantics](ANALYSIS_WORKFLOWS.md#live-and-grouped-evidence-panels) and the [review record](qa/reviews/live-grouped-overlays-2026-10-01/REVIEW.md).

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
- `protocol.Result` carries `correlation_id`, `dependency_type`, `dependency_target`; HTTP, JMS, gRPC, WebSocket, database, network, Kafka and Event Hubs classify them; engine rollups, distributed batches, archives and Parquet retain them, and archive queries filter on them.
- Configurable SLA budgets in system settings (defaults: 2000 ms end-to-end sample age, 500 ms per stage), applied to diagnostics; run-level `telemetry_health` moves `degraded` then `breached` after the sustained window (data loss breaches immediately) and recovers with a healthy transition.
- Webhooks can subscribe to `telemetry_degraded`, `telemetry_breached`, `telemetry_recovered`; Slack, Teams and Dynatrace payloads carry severity/summary/conditions; websocket consumers get `telemetry_health_changed`.
- Archive reports aggregate telemetry status, healthy/degraded/breached interval counts, sustained breaches, dropped samples, websocket drops, max lag per stage, breach timestamps and condition counts; HTML reports show a Telemetry Health overview metric.

Live dependency maps and error-pattern cards are implemented. Project-gated diagnostics now associate explicitly configured monitor service identities with retained dependency intervals and link to bounded monitor overlays. Broader cross-run baseline and sustained SLA reporting remain ongoing.

Foundation since the last consolidation: `pkg/analysis` defines validated metric points, wall-clock alignment, correlation scoring, and advisory 30-bucket/three-sigma anomaly scoring with explicit missing/stale quality; collector snapshots can expose the metric-point projection. The collector agent now differentiates Linux host, Go runtime, and bounded Prometheus scalar packs. Statistical inputs are bounded to 10,000 points per series and require unique timestamps. Counters and aggregate summaries require explicit conversion before scoring; anomaly baselines require comparable gauge buckets. Run bindings, signed jobs, durable monitor artifacts and lagged comparison queries now use this contract. Qualified 30-bucket anomaly findings now reach live summaries, archived reports, advisory incidents/webhooks and root-cause evidence. Pauses, load changes, stale samples and dropped evidence invalidate qualification; these findings cannot stop a run. See [workflow configuration and acceptance limits](ANALYSIS_WORKFLOWS.md).

---

## Protocol telemetry parity

Goal: bring every protocol to the three-layer depth that [Browser and mobile](#browser-and-mobile) defines for the browser, so each protocol answers "why was it slow, where did time go, how does it compare to baseline".

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
- gRPC diagnostic capture now covers unary, server/client streaming, and bidirectional calls: bounded method identity, client call and first-response timing, remaining deadline budget, observed terminal status, and protobuf message counts/sizes (including partial failures). Optional typed evidence survives distributed RPC and JSONL/Parquet transaction artifacts; live summaries and archive details distinguish missing observations from zero. Bidirectional calls read while sending. Measured protobuf encode/decode timing, qualified baseline comparisons, method hotspot/deadline findings and WebSocket diagnostics are implemented. HTTP/2 phases remain unavailable where client hooks do not expose them; see [protocol diagnostic semantics](PROTOCOL_DIAGNOSTICS.md).
- Previous-run comparison baselines on `GetTest`, used by the execution summary and server telemetry panel.

Open work (effort estimates from the original plan):

| Protocol | Item | Layer | Location | Effort |
| :--- | :--- | :--- | :--- | :--- |
| Java HTTP | Helper-reported object footprint vs serialized wire size | L2 | `java_http/java_http.go`, helper contract | 1 day |
| Java HTTP | Codec overhead chart (serialization share of total time) | L3 | `dashboard/` | 1 day |
| WebSocket | Captured handshake/reconnect, send/receive wait, pending application writes, write wait and matched authored ping RTT; transport-internal queue depth remains unavailable | L2 | `protocol.go`, `websocket/websocket.go` | Implemented measurement slice |
| WebSocket | Advisory connection-health findings, message distributions, report cards and qualified throughput comparisons implemented; no silent active health probes or inferred message RTT | L3 | `engine/metrics.go`, `dashboard/`, `api/` | Broader acceptance open |
| gRPC | Client call/first-response timing, counts, sizes, statuses and deadline budgets landed. Measured protobuf codec timing added. Remaining: exposed transport phases and broader throughput analysis (no inferred server-processing or HTTP/2 phase timings) | L2 | `grpc/grpc.go` | ~3 days |
| gRPC | Method hotspots, deadline/status findings, dashboard/report cards and qualified baseline comparisons implemented; project-gated statistical/dependency finding delivery implemented; broader live acceptance remains | L3 | `engine/metrics.go`, `dashboard/`, `api/` | Partial |
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

Related: [Browser and mobile](#browser-and-mobile) is the most complete existing L2 example; [Web/API execution matrix](PROTOCOL_SUPPORT.md#webapi-execution-support-matrix) records the shipped L1 SOAP and Java HTTP support.

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

Additional fixes: scenario reseeding retains group SLA overrides, and summary-only script-save responses reload the canonical stored script instead of reopening stale draft content.

Remaining high-value work: finish the scenario-assembly path around `TestExecution` and shared execution/result models; verify all remaining save/reopen entry points against the canonical behavior; run the end-to-end parity pass and then delete remaining temporary adapters.

Verification checklist still open: validation matrix proving identical outcomes across parser, create, update, draft/stored validation, replay, browser replay and import save; snapshot of pre-standardization import payloads versus the shared envelope; same lifecycle (acquire, review, edit, save, reopen) on every authoring path; scenario editing from both new-test creation and execution-time editing; final check that no route or option references deleted files or adapters.

Decisions: keep multiple entry points; main script editor is the default canonical edit destination; the richer JMeter result model is the baseline for the shared envelope; keep authoring and scenario assembly separate; do not force browser replay into the shared model unless its lifecycle aligns. Risks: adapter sprawl, frontend churn before backend contracts stabilize, and cleanup regressions if adapters are deleted before parity verification. Validator unification was the recommended first slice.

Key code: `pkg/dsl/parser.go` and `types.go`; `pkg/api/handlers_scripts.go`, `handlers_recorder.go`, `handlers_replay.go`, `handlers_browser_replay.go`; converters under `pkg/convert/{enterprisearchive,jmeter,sample}` and `pkg/recorder`; dashboard `ScriptsPage`, `ScriptEditor`, importer/recorder components, `CreateTestModal`, `TestExecutionScenarioEditor`. See also [ENTERPRISE_CONTRACTS.md](ENTERPRISE_CONTRACTS.md).

---

## PT Analysis native Parquet import

Status: Project-gated implementation. Controller/desktop builds embed the pinned `github.com/duckdb/duckdb-go/v2` driver under `analytics_duckdb`; a CGO-free profile reports the capability unavailable. Resumable multi-file imports, schema preview, explicit mappings, immutable originals, normalization, saved definitions and bounded series/correlation are implemented. See [Analysis workflows](ANALYSIS_WORKFLOWS.md). The 10 GB benchmark was skipped at the user's direction and is not an achieved scale claim.

Decision: import Parquet natively, preserve the original file, and normalize its meaning into the dashboard's canonical analysis model. Do not analyze arbitrary Parquet schemas directly, and do not convert to CSV/JSON as the primary path (that loses schema, types, compression and efficient column scans); conversion is only for exports, debugging or an optimized cache.

Why Parquet: columnar for analytical queries, strongly typed (timestamps, metrics, dimensions), compressed, widely supported (DuckDB, Arrow, Polars, Spark, Python, cloud analytics), and suited to multi-file partitioned datasets.

Architecture: Parquet files, native reader (DuckDB/Arrow/Polars), schema detection and field mapping, canonical analysis tables, correlation engine and dashboard queries. Steps: accept files in the import workflow; keep the original as an artifact; infer schema and preview columns; map columns to canonical fields; validate required fields (time and scenario/run identity); load normalized data; query through DuckDB/Arrow; materialize correlation views only when needed.

Two storage layers: a raw artifact layer (original files unchanged, for reprocessing, auditing, debugging mappings) and a normalized analysis layer (mapped, validated records for fast queries, reliable joins, cross-scenario comparison, system-metric correlation). DuckDB fits as a direct query layer over files, a staging engine during import, or a local analytical cache.

Canonical fields: `run_id`, `scenario_id`, `scenario_name`, `timestamp_utc`, `relative_time_ms`, `source`, `host`, `service`, `metric_name`, `metric_value`, `unit`, `transaction_name`, `status`, `error_type`, `tags`, `labels`. Source-column aliases map to shared concepts, for example `elapsed`, `response_time`, `duration_ms`, `latency` to `response_time_ms`, and `cpu_pct`, `processor_time` to `cpu_percent`.

Correlation requirement: every dataset supports both absolute time (`timestamp_utc`) and test-relative time (`relative_time_ms`), enabling questions such as whether CPU saturation preceded a checkout latency spike, whether scenario 2 failed after scenario 1 raised load, or whether network latency tracked response time. Correlation dimensions: time window, run, scenario, transaction, host, service, region/environment, metric type, error category. Derived views: scenario response time and transaction percentiles by time bucket, error rate by scenario/transaction, host CPU/memory/network by bucket, scenario latency versus resource use, cross-scenario contention.

Import workflow: user selects Parquet files; dashboard inspects schema and previews columns; auto-detects common fields; user confirms or edits mappings; validation; import and normalize; expose for comparison and correlation.

Minimum viable scope: native reading, schema inspection, mapping configuration, required-timestamp validation, a normalized `metric_points` table (`id`, `run_id`, `scenario_id`, `timestamp_utc`, `relative_time_ms`, `source`, `host`, `service`, `transaction_name`, `metric_name`, `metric_value`, `unit`, `status`, `tags_json`), scenario and system metric import, and time-bucketed correlation queries.

Landed follow-ons: saved mapping revisions, unit normalization, directory uploads with validated partition paths, explicit counter-rate conversion, saved comparisons and lagged correlation. Remaining: broader derived-metric and dashboard overlay authoring, measured partition/scale performance, and verified release outcomes across supported native platforms. The skipped 10 GB benchmark remains an unverified acceptance item.

---

## Messaging

Scope: importing, running and evaluating results for JMS, Apache Kafka, Solace PubSub+, IBM MQ, Azure Service Bus and Azure Event Hubs. Current behavior is authoritative in [Messaging support](PROTOCOL_SUPPORT.md#broker-and-messaging-support). Most of this program has landed; this section keeps the open work, a compact record of what landed, the coverage picture and the design decisions that still constrain new adapters. It was condensed from the former 1,551-line coverage plan and the Kafka/AsyncAPI plan; per-item baselines superseded by landed code were dropped (recover from git history).

### Open work (reconciled 2026-10-01)

| Item | State | Notes |
| --- | --- | --- |
| P2.5 IBM MQ native adapter | Not built | Registered for authoring and mock replay only. `IbmMqJMSRealAdapterAvailable()` is false, validation rejects `mock:false`, runtime init fails fast with guidance. No `ibmmq_real` build-tag pair, no MQI dependency, no MQMD fields. Spec below. |
| P2.6 Solace native SMF transport | Not built | Solace is JMS-over-AMQP 1.0 only (`jms_solace_real`). No direct vs guaranteed, wildcards, partitioned queues, DMQ/max-redelivery, replay. Spec below. |
| Event Hubs deployment env vars and HOWTO | Implemented | Optional auth/probe-group defaults preserve explicit script settings; [deployment guide](EVENT_HUBS_HOWTO.md) documents identity, real-adapter builds, consumer isolation and terminal lag gates. Live Azure acceptance remains open. |
| Event Hubs consumer-side parity | Deferred past day-1 | Full Processor, Blob checkpoint store and epoch-exclusive consumer; only the read-only observer probe exists. `checkpoint` and request/reply are cataloged but secondary. |
| Messaging depth gaps | Partial | Kafka transactions/EOS and complete `read_committed`, `kafka_seek` on the real adapter, unified offset/queue-depth lag across providers, not every adapter populates the reliability counters, no Event Hubs GA emulator so live coverage needs a namespace, many JMS/AMQP/Solace/MQ/MQTT AsyncAPI bindings still downgrade to review-only. |

IBM MQ is very low priority at user direction (2026-10-01); do not include it in the current analytics slice.

Recommended order: per-partition/subscription rollups and remaining goal signals (P4.1 remainder), then Event Hubs consumer ownership by customer need, then P2.5 and P2.6 (off the critical path; schedule by business priority). Open decisions: which real adapters ship in the default generator image versus opt-in images (the default must keep failing closed on non-mock messaging); IBM MQ native MQI (cgo) versus a pure-Go REST/AMQP fallback; Solace SMF via a cgo SDK versus staying on AMQP.

### Landed work record

Original items were checked against the tree on 2026-09-24; Event Hubs defaults and observed-lag gates were reconciled on 2026-10-01.

| Item | Scope | State |
| --- | --- | --- |
| P0.0 | Build-tag-aware Kafka non-mock validator (`KafkaRealAdapterAvailable()`) | Landed (CI `kafka` profile of the `protocol-tags` job in `.github/workflows/ci.yml` and a `kafka_real,kafka_integration` lane in `protocol-integration.yml`) |
| P1.1 | Unified reliability counters on `protocol.ServerTelemetry` (send failure, redelivery, dead-lettered, duplicate, out-of-order, commit/abort, throttle/ServerBusy/quota) | Landed; percentiles ship as raw values (no t-digest snapshot), so watch payload volume at high TPS |
| P1.2 | Shared ack / transaction / session state machine | Landed as `pkg/protocol/messaging/session.go` |
| P1.3 | Request-reply correlation with safe requeue | Landed as `pkg/protocol/messaging/correlation.go` |
| P1.4 | DSL catalog enrichment and catalog/runtime parity | Landed for JMS; regenerate with `go generate ./pkg/dsl` |
| P1.5 | Consumer-lag / queue-depth / DLQ telemetry and dashboard | Largely landed; lag is not yet a single offset/queue-depth model across providers |
| P2.1 | Kafka depth: idempotence, transactions, isolation, batching, seek | Partly landed; full EOS and `read_committed` open |
| P2.2 | Schema Registry / Avro / Protobuf / JSON-Schema serde | Largely landed in `pkg/protocol/kafka`; Protobuf/JSON-Schema not re-audited |
| P2.3 | JMS semantics: message types, selectors, priority, TTL, delivery mode, browse, topic fan-out | Landed |
| P2.4 | Azure Service Bus: settlement, lock renewal, sessions, scheduled/deferred, DLQ receive | Landed in `realadapter_servicebus.go`; cross-entity transactions and backlog probes shallow |
| EH.1-EH.2 | Event Hubs Kafka-endpoint profile (`config.kafka.profile: eventhubs`), OAUTHBEARER / Entra ID SASL | Landed |
| EH.3 | Native Event Hubs AMQP adapter (`pkg/protocol/eventhubs`, `eventhubs_real`) | Landed producer-first with observer probe and mock default |
| EH.4 | Event Hubs results (per-partition skew, ServerBusy/throttle, batch fill, E2E latency) | Landed in telemetry fields and Prometheus; dashboard panels and goal signals uneven |
| EH.5 | Event Hubs Capture (Avro) import and AsyncAPI EH binding | Landed (`POST /api/v1/scripts/import/eventhubs-capture`) |
| EH.6 | Prometheus exposition, terraform, CI tag matrix | Landed, including optional auth/probe-group env defaults and the deployment HOWTO; `deployments/azure/terraform/eventhubs.tf` exists |
| P3.1 | Messaging import breadth (AsyncAPI JMS/AMQP/Solace/MQ/MQTT bindings) | Partly landed: AMQP/JMS servers target `jms-solace`; other bindings review-only |
| P4.1 | Messaging-aware goals and dashboard panels | Partly landed: redelivery/dead-letter thresholds and live breach flags; terminal maximum consumer age/offset rules require complete observed evidence, including zero; partition/subscription rollups remain open |
| Kafka plugin + AsyncAPI converter | Direct Kafka protocol, AsyncAPI 2.x/3.x importer and wizard, franz-go real adapter | Landed (design record below) |

### Coverage assessment

Ratings: None (not implemented or mock-only), Thin/Partial, Basic (core ops over a real adapter behind a build tag), Broad (spec-level).

| Platform | Import | Run | Results | Remaining gaps |
| --- | --- | --- | --- | --- |
| JMS (generic) | Partial (AMQP server drafts) | Thin | Partial | Durable/shared subscription breadth, provider breadth |
| Apache Kafka | Partial (AsyncAPI) | Basic (`kafka_real`) | Partial | Full transactions/EOS, complete `read_committed`, real-adapter `kafka_seek`, offset-lag in every view |
| Solace PubSub+ | Partial (AsyncAPI to `jms-solace`) | AMQP-only (`jms_solace_real`) | Partial | Native SMF (P2.6) |
| IBM MQ | None | Mock only | Partial | Entire native MQI adapter (P2.5) |
| Azure Service Bus | None | Basic (`jms_servicebus_real`) | Partial | Cross-entity transactions, backlog probes, emulator/live breadth |
| Azure Event Hubs | Partial (Kafka endpoint, Capture, EH binding) | Basic (`eventhubs_real`, `kafka_real`) | Partial | Consumer checkpoint ownership, live Azure coverage, dashboard/goal signals |

Structural facts: no real broker transport ships in the default build (mock or fail-closed stub; real adapters are opt-in tags `kafka_real`, `jms_solace_real`, `jms_servicebus_real`, `eventhubs_real`); results are messaging-aware for throughput, latency and the newer reliability fields but shallow compared with broker-native consoles; messaging SLAs in `GoalConfig` are partial.

### Architecture constraints and design decisions

- A new protocol lives in `pkg/protocol/<name>/`, implements `pkg/protocol.Protocol`, self-registers via `init() -> protocol.Register(name, factory)`, and is blank-imported by `cmd/generator`, `cmd/controller` and (a subset) `cmd/apexctl`. Real execution runs on generator pods.
- Real adapters follow a build-tag seam: a `//go:build <tag>_real` file plus a fail-closed default stub, and a `pkg/dsl/capabilities_<x>_{real,default}.go` availability helper the validator consults so non-mock scripts validate only when the adapter is compiled in.
- The results pipeline is protocol-neutral (`protocol.ServerTelemetry` to `ServerTelemetrySummary`/`ProtocolSummary` to proto/distributed merge to dashboard); an adapter that fills `ServerTelemetry` inherits throughput, latency, consumer lag, payload sizing, throughput-gap SLA and the broker-pressure analyzer.
- Build once, reuse everywhere: unified reliability counters, request-reply correlation, a generic AMQP 1.0 layer factored from Solace, schema-registry serde, one lag/depth/backlog surface, real ack/transaction state machine, catalog enums, messaging goals, a higher-fidelity mock broker.
- Refresh generated artifacts after catalog or route changes: `go generate ./pkg/dsl` (`docs/dsl.schema.json`, `docs/DSL_ACTION_REFERENCE.md`) and `go generate ./pkg/api` (`docs/swagger.yaml`, `TestSwaggerYAMLIsFresh`).

### P2.5 IBM MQ native adapter (open — very low priority)

Goal: a native-MQI transport (`MQCONNX`, `MQOPEN`, `MQPUT`/`MQPUT1`/`MQGET`, `MQCMIT`/`MQBACK`, `MQSUB`, `MQDISC`) opt-in behind `-tags ibmmq_real`, fail-closed by default, with MQMD control (persistence, priority, expiry, MsgId/CorrelId as `MQBYTE24`), RFH2-vs-MQMD property split, SVRCONN/CCDT/CipherSpec/MQCSP connectivity, backout/DLQ taxonomy, and validate-gate parity with Solace and Service Bus.

Design: mirror the real/default pair with `newIbmMqAdapter(cfg) brokerAdapter` in `//go:build ibmmq_real` and `!ibmmq_real` files (real uses `github.com/ibm-messaging/mq-golang/v5/ibmmq`, cgo against the MQ C client redistributable). Map the five `brokerAdapter` methods: Publish to `MQPUT1`/`MQPUT`, Consume to `MQGET` with `MQGMO` (wait interval, browse, syncpoint plus commit/backout), RequestReply to `MQPUT` with `ReplyToQ` then a correlated `MQGET`. Surface `MQMD.PutDate/PutTime`, backout count and resolved queue manager in `result.Extractions`. Rejected: a sidecar JMS bridge and a pure-Go MQ reimplementation (no supported library).

DSL and catalog: no new action types; add a `jms-ibmmq` protocol alias next to `jms-solace` in `catalog.go`, MQMD fields `mq_persistence` (`persistent|non_persistent|as_queue`), `mq_priority` (0-9), `mq_expiry_ms`, `mq_msg_id`, `mq_correl_id`, `mq_format`, `mq_reply_to_qmgr`, and config keys `mq_queue_manager`, `mq_channel`, `mq_conn_name`, `mq_ccdt_url`, `mq_cipher_spec`, `mq_backout_queue`, `mq_backout_threshold`, `mq_property_style` (`rfh2|mqmd`). Extend `config`/`message` in `pkg/protocol/jms/jms.go` (an `mqMessageDescriptor`); add `IbmMqJMSRealAdapterAvailable()` in `pkg/dsl/capabilities_ibmmq_{real,default}.go`; add `isIbmMqJMSScript()` and an availability issue in `pkg/dsl/validate.go` parallel to the Service Bus check; plumb keys in `pkg/engine/config_protocols.go`. Real files: `pkg/protocol/jms/realadapter_ibmmq.go` and `realadapter_ibmmq_default.go`.

Build and CI: an `ibmmq_real` lane with the MQ client in the runner image; the cgo-free default lane must keep compiling. Integration via testcontainers `icr.io/ibm-messaging/mq` (`DEV.QUEUE.1`, `DEV.APP.SVRCONN`) covering put/get, persistence/priority/expiry, MsgId/CorrelId, syncpoint commit/backout, browse and DLQ/backout routing.

Acceptance: `-tags ibmmq_real` builds while default stays fail-closed; `mock:false` validates only when the adapter is available; MQPUT/MQGET round-trip honors MQMD fields; syncpoint and backout-threshold routing verified; `go generate ./pkg/dsl` clean; `go test ./...` and `go test -tags ibmmq_real ./...` green. Effort XL; depends on P1.2, P1.4, P2.3 (all landed). Risks: cgo and the MQ C client complicate CI, images and cross-compilation (linux/amd64 generator pods only); `MQBYTE24` semantics and RFH2 vs MQMD property placement are interop foot-guns; image licensing and startup time; `jms_ack`/`jms_commit`/`jms_rollback` must be made real for MQ or documented as not wired.

### P2.6 Solace native SMF transport (open)

Goal: a native SMF transport alongside the AMQP adapter: direct vs guaranteed messaging with a publisher-ack window and bounded consumer flow; `*`/`>` wildcard subscriptions and topic-to-queue mapping; partitioned queues by partition key; exclusive vs non-exclusive access; DMQ plus max-redelivery (0-255); replay (all, msg-id, timestamp); Message-VPN, TLS and compression. Build-tagged, fail-closed by default.

Design: add `pkg/protocol/jms/solace_smf.go` (`//go:build jms_solace_smf`) implementing `brokerAdapter` with `solace.dev/go/messaging`, plus `solace_smf_default.go` with an `unavailableSolaceSMFAdapter` and `SolaceSMFRealAdapterAvailable()`. Stay inside the JMS package. Extend `selectAdapter` so `provider==solace` with `properties["transport"]=="smf"` returns `newSolaceSMFAdapter(cfg)`; AMQP stays the default and `normalizedBrokerURL` stays AMQP-only while the SMF path accepts `tcp(s)://` and `smf(s)://`. Publisher acks satisfy `jms_publish`; persistent-mode consumer ack makes `jms_ack` real for this path. Rejected: a native SMF C wrapper (cgo in generator pods) and a new protocol name (breaks catalog, import and dashboard reuse).

DSL and catalog (`pkg/dsl/catalog_jms.go`): on `jms_publish` add `params.delivery_mode` (`direct|persistent`), `params.partition_key`, `params.dmq_eligible`, `params.ttl_ms`; on `jms_consume` add `params.access_type` (`exclusive|nonexclusive`), `params.max_redelivery` (0-255), `params.flow_window`, `params.replay` (`none|all|msg-id|timestamp`), `params.replay_from`. Config keys: `jms_transport`, `jms_publisher_ack_window`, `jms_compression` (1-9), `jms_replay`, `jms_replay_from`. Validation in `validateJMSScriptIssues`: redelivery range, `replay_from` required for msg-id/timestamp, SMF availability gate. Telemetry: `RedeliveryCount` from SMF delivery count, extractions `jms_redelivered`, `solace_partition_key`, `solace_replay_offset`.

Build and CI: add `solace.dev/go/messaging` to `go.mod`; tags `jms_solace_smf` and `jms_solace_smf_integration`; a lane in the `protocol-tags` job; integration against a `solace/solace-pubsub-standard` testcontainer. Acceptance mirrors the goal list plus a clear fail-closed error ("build with -tags jms_solace_smf") in the default build. Effort XL. Risks: confirm the SDK is CGO-free for the linux/amd64 generator image, otherwise fall back to the sidecar adapter path; replay and DMQ need broker-side provisioning in the harness; partitioned queues need a recent broker; ack/flow-window tuning interacts with VUser density targets.

### Azure Event Hubs design record

Two faces, both landed producer-first:

- Kafka endpoint (`{namespace}.servicebus.windows.net:9093`, TLS mandatory, SASL `PLAIN` with username `$ConnectionString` or `OAUTHBEARER` with an AAD token, 1 MB message cap) is `profile: "eventhubs"` on the existing `kafka` protocol, reusing the whole metrics/import/dashboard pipeline.
- Native AMQP is a dedicated `eventhubs` protocol behind `eventhubs_real`, cloning the JMS provider-adapter pattern (`selectAdapter()`, pooling as in `realadapter_servicebus.go`, paired real/default files). It hosts partition keys, AMQP credit metrics and Capture parity.

Locked decisions: auth by connection string (SAS) and AAD/workload identity (`pkg/engine/azure_token.go`; OAUTHBEARER for the Kafka endpoint via EH.2); build-tag opt-in and mock by default; import from the AsyncAPI Event Hubs binding and Capture files (ARM/Bicep dropped); producer-first metrics (aggregate plus per-partition throughput/bytes, skew and hot-partition detection); role is producer plus a read-only observer probe measuring E2E produce-to-consume latency and backlog. Kafka EOS on Event Hubs is Premium/Dedicated preview-only, so transactions are broker-capability-gated.

Deployment: `deployments/azure/terraform/eventhubs.tf` exists; the AKS configmap env vars, Helm values/templates examples and `docs/EVENT_HUBS_HOWTO.md` are the unbuilt remainder. Dependencies: `azeventhubs` and `github.com/hamba/avro/v2`. Reuse points: `pkg/protocol/kafka/realclient_{default,franz}.go`, `pkg/protocol/jms/jms.go` `selectAdapter()`, `pkg/engine/azure_token.go`, `pkg/dsl/capabilities_servicebus_{real,default}.go`, `pkg/api/distributed_metrics.go`, `pkg/api/import_paramize.go`.

### Kafka plugin and AsyncAPI design record (implemented)

Two additive deliverables: a Kafka protocol plugin (`pkg/protocol/kafka`, direct-execution, modeled on JMS) and an AsyncAPI converter (`pkg/convert/asyncapi`, the only importer that emits Kafka DSL). Kafka is authored directly, not recorded.

- Opt-in real adapter (`github.com/twmb/franz-go`, pure Go): `go build -tags kafka_real ./cmd/generator ./cmd/controller ./cmd/apexctl`. With the tag, `config.kafka.mock: false` uses franz (SASL PLAIN/SCRAM-256/SCRAM-512/OAUTHBEARER, TLS, acks, compression, consumer groups). Seam: `newRealClient(config)` in `realclient_default.go` versus `realclient_franz.go`. Live test: `KAFKA_BROKERS=localhost:9092 go test -tags "kafka_real kafka_integration" ./pkg/protocol/kafka/...`. Default `go test ./...` uses only the in-memory mock.
- Actions `kafka_produce`, `kafka_consume`, `kafka_request_reply`, `kafka_commit`, `kafka_seek`. `KafkaConfig` keys (brokers, security_protocol, sasl_mechanism, sasl credentials/`sasl_password_secret`, tls, client_id, consumer_group, topic, acks, compression, partition_strategy, auto_offset_reset, timeout_ms, properties, mock) flatten to `kafka_*` in `pkg/engine/config.go`. Extractors: body, header, key, partition, offset, correlation_id. Assertions: `message_received`, `body_contains`, `header_equals`, `key_equals`, `partition_equals`, `offset_advanced`, `correlation_match`, `consumer_lag_below`, `response_time_ms`. Results reuse `DependencyType` `kafka_topic`/`kafka_consumer_group` and extractions `kafka_partition|offset|key|timestamp|consumer_lag`. Validation is `validateKafkaScriptIssues`; `kafka` is exempt from `base_url`.
- AsyncAPI converter: parses 2.x/3.x (JSON/YAML); servers to brokers and `security_protocol`; channels to topics; send/publish to `kafka_produce`, receive/subscribe to `kafka_consume`, reply/correlationId to `kafka_request_reply`; payload schemas sampled to bodies. Options: `script_name`, `brokers`, `operation_ids`/`channels`, `include_assertions`, `default_think_time_ms`. Routes: `POST /api/v1/scripts/import/asyncapi` and `GET .../asyncapi/support-matrix`. Kafka binding supported; Avro/Protobuf partial; MQTT/AMQP/WebSocket out of scope or review-only/`jms-solace`.
- Open risks: (1) confirm the SSRF/egress allow-list intercepts raw `host:port` broker dials in `APEXLOAD_ENV=production`; (2) consumer-group semantics under load (shared groups cause rebalances); (3) Schema Registry payloads in AsyncAPI remain partial; (4) franz-go effect on generator image size; (5) redact broker URLs with inline credentials in evidence (`BuildProtocolEvidence`).

### Testing, CI and verification

- Every change: table-driven unit tests, `go test ./...`, browser verification for dashboard changes.
- Build-tag matrix proving both gating directions: default, `kafka_real`, `jms_solace_real`, `jms_servicebus_real`, `eventhubs_real`, plus `ibmmq_real` and `jms_solace_smf` when built; each tagged job runs `go test -tags <tag> ./pkg/dsl ./pkg/protocol/...` (`ci.yml` `protocol-tags`, enterprise Dockerfile profile).
- Integration via testcontainers behind the build tag and an opt-in env: Kafka (Redpanda/Apache) plus schema registry, IBM MQ, Solace, ActiveMQ Artemis. Service Bus has an official emulator; Event Hubs has no GA emulator, so live coverage needs a namespace, otherwise mock and contract tests only.
- Event Hubs mock end-to-end: `apexctl run` with an `eventhubs` producer script (`mock:true`) plus observer probe, and a `kafka` script with `profile:eventhubs`; confirm throughput, ack latency, simulated throttling, per-partition distribution and probe E2E latency in `GET /api/v1/tests/{id}/metrics`. Import checks: AsyncAPI with an EH binding yields `kafka.profile=eventhubs`; Capture Avro yields produce DSL. Also `terraform validate` in `deployments/azure/terraform` and `helm lint`.

---

## Browser and mobile

Scope: mobile device emulation, client-side metrics, the UX-platform strategy tying them together, TrueClient MobileWeb import and browser replay HAR capture. Current support guidance: [Browser and mobile support](PROTOCOL_SUPPORT.md#browser-execution-and-mobile-emulation). Benchmark procedure: [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-runbook).

### Vision and decisions

APEX Load is positioned as a synthetic digital-experience platform: emulate modern mobile devices (iOS, iPadOS, Android) and measure client-side experience under controlled load before production. It is deliberately not a TrueClient clone and not native desktop or native mobile app automation.

- **One protocol.** DSL `protocol` stays `browser` (no `browser_mobile`/`browser_desktop`/`browser_metrics`); device behaviour is configuration read in `Init()` of `pkg/protocol/browser`.
- **Explicit DSL fields** on `BrowserConfig`: `device_profile`, `touch_mode`, `orientation`, `client_metrics`, engine/headless settings.
- **Opt-in touch semantics.** With touch mode active `browser_click` maps to touch events; desktop scripts are unaffected.
- **Three separable layers.** L1 device emulation and interaction; L2 client telemetry (Core Web Vitals, navigation/resource timing, JS heap, layout/paint, long tasks); L3 UX intelligence. Each is usable without the layer above.
- **Four fidelity dimensions**: visual, interaction, performance (CPU throttle, network preset, advisory JS heap) and measurement.
- **Device profiles carry performance characteristics** (`CPUThrottleRate`, `NetworkPreset`, `DeviceTier`), applied once at `Init()` for the VUser lifetime.
- **Metrics are per action**, attached to `protocol.Result` as optional `ClientMetrics` (nil when disabled, zero cost) and tagged with device profile/tier.
- **Two collection sources**: CDP `Performance.getMetrics` plus injected PerformanceObserver/Performance API scripts, with network events from existing capture. Everything runs through Playwright (`playwright-go`) with CDP sessions for Chromium-only telemetry; `chromedp` is not a dependency.
- **Benchmark before scale claims.** Browser VUsers cost roughly 150-300 MB each; they are diagnostic canaries paired with high-density protocol load.
- **Out of scope**: native app testing, sensors, multi-touch beyond pinch-zoom, offline mode, visual regression, video recording.

Seams: `pkg/dsl/types.go`, `pkg/protocol/protocol.go`, `pkg/protocol/browser/` (`browser.go`, `devices.go`), `pkg/engine/config.go`/`metrics.go`, `pkg/api/runner.go`/`archive.go`, `dashboard/src/types.ts`.

### Status (rechecked 2026-09-24)

Implemented: named device profiles; DSL/engine wiring and validation (including profile/engine compatibility); touch actions `browser_tap`, `browser_long_press`, `browser_swipe`, `browser_set_orientation` plus click-to-touch mapping; Chromium and WebKit both on Playwright with WebKit prerequisite warnings in validation, replay APIs and the editor; editor/scenario/library/queued-preview/archive preservation of browser defaults; per-action `ClientMetrics` (FCP, LCP, CLS, INP, DOM timing, blocking resources, long tasks, JS heap, layout/style/task duration, Chromium process CPU/RSS), live `browser`/`browser_by_device` summaries, archive persistence, `browser_by_engine`/`browser_by_platform`/`browser_by_device` rollups, LCP/INP/CLS comparison, per-action drilldown and a capped resource waterfall; TrueClient MobileWeb import MVP; explainable UX slice (health score, weakest-device callout, threshold-backed issue cards); opt-in benchmark harness (`APEXLOAD_BROWSER_BENCHMARKS=1`).

Still open:

- Measured benchmark publication from a controlled hardware matrix; nothing measured is checked in, so capacity guidance is conservative and code-backed.
- Evidence that CPU/network profile differences produce meaningful timing separation; decide on memory-pressure simulation and publish how approximate simulated timings are.
- WebKit process CPU/RSS attribution (no stable process IDs); WebKit stays scoped to page/runtime metrics.
- Broader manual validation on real applications, especially WebKit hosts; representative TrueClient MobileWeb archives; per-resource waterfall rows beyond the capped entries.
- UX intelligence beyond thresholds: frustration/rage-tap detection, heatmaps, business-impact scoring, anomaly detection against baselines.

### Device profiles

Defined in `pkg/protocol/browser/devices.go`, enforced by DSL and replay validation.

| Profile | Engine | Platform | Tier | Viewport | Scale | Default orientation |
|:---|:---|:---|:---|:---|---:|:---|
| `desktop_1080p` | `chromium` | desktop | high | 1920x1080 | 1.0 | landscape |
| `galaxy_s24` | `chromium` | android | high | 360x780 | 3.0 | portrait |
| `pixel_8` | `chromium` | android | high | 412x915 | 2.625 | portrait |
| `ipad_air` | `webkit` | ipados | mid | 820x1180 | 2.0 | portrait |
| `iphone_15_pro` | `webkit` | ios | high | 393x852 | 3.0 | portrait |
| `iphone_se` | `webkit` | ios | low | 375x667 | 2.0 | portrait |

Rules: `iphone_*` and `ipad_*` require `webkit`; `desktop_1080p`, `galaxy_s24`, `pixel_8` require `chromium`. `touch_mode` and `orientation` overrides are valid on top of a profile, which remains the source of truth for platform, tier and default viewport. Headed and headless are supported for both engines; headed is a diagnostic workflow, not a scaling target.

DSL shape: `{"protocol":"browser","config":{"base_url":"https://m.example.com","browser":{"device_profile":"iphone_15_pro","orientation":"portrait","touch_mode":true,"headless":true,"client_metrics":true,"capture_screenshots":"on_error"}}}`. `buildProtoConfig` in `pkg/engine/config.go` forwards `browser_device_profile`, `browser_touch_mode`, `browser_orientation`; Init applies viewport, scale, mobile flag and UA (unless overridden) and enables touch emulation.

Baseline browser protocol: Playwright execution for both engines; custom UA/viewport; network throttling presets (56K, DSL, T1, 10Mbps) and custom bandwidth/latency; download allow/block lists; cache control; new-user-per-iteration reset; actions `browser_navigate`, `browser_click`, `browser_fill`, `browser_select`, `browser_wait`, `browser_screenshot`, `browser_tap`, `browser_long_press`, `browser_swipe`; full network/console capture, DOM summaries and error snapshots. Opt-in benchmarks (`pkg/protocol/browser/mobile_benchmark_test.go`): launch with device profile p50 < 3 s, touch overhead < 10 ms, Chromium RSS < 250 MB per VUser.

### Capacity ceiling: published density versus internal ceilings

Capacity estimates classify scenarios as `http`, `browser` or `mixed`. The **published density** is the diagnostic canary maximum of **5 browser VUsers per generator** (`publishedBrowserDensityPerGenerator`, `pkg/api/browser_benchmark_evidence.go`), recommended start 1-2. The per-tier figures below are **internal engineering ceilings** (`browserCapacityProfileForTier` in `pkg/api/capacity_planning.go`) used for capacity planning math, not published density. Runs above the published cap flip the release gate to `benchmark_evidence_and_override_required` and require `APEXLOAD_BROWSER_DENSITY_OVERRIDE=true` plus attached benchmark evidence. See [PERFORMANCE_BENCHMARKS.md](PERFORMANCE_BENCHMARKS.md#browser-benchmark-artifacts).

| Device tier | Internal ceiling (VUsers / generator; not published) | Memory / VUser | CPU / VUser |
|:---|---:|---:|---:|
| low | 40 | 180 MB | 0.055 cores |
| mid | 32 | 220 MB | 0.070 cores |
| high | 25 | 260 MB | 0.085 cores |
| unknown | 20 | 280 MB | 0.090 cores |

The original per-profile starting ranges (desktop Chromium 1-15, mobile Chromium 1-10, mobile WebKit 1-5) are engineering starting points for local investigation only. Raise concurrency only after stable host CPU, memory and browser-process telemetry; enable `browser.client_metrics` for benchmark runs; treat cross-engine comparisons as diagnostic; re-run matrix tests and a local benchmark after Playwright/WebKit upgrades. Recommended model is hybrid: high-scale HTTP/JMS load plus a few browser canaries; device comparison uses VUser groups (about 5 browser VUsers per profile) with per-group `device_profile` overrides.

### Client metrics phases

Goal: the practical equivalent of TrueClient's browser-side timing and artifact capture, extending the `browser` protocol rather than adding a runtime. Alternatives not pursued: application RUM/OpenTelemetry instrumentation (needs app changes) and a separate browser worker/sidecar (second execution model).

| Phase | Scope | Status |
|:---|:---|:---|
| 0 | Product boundaries, v1 metric contract, non-goals | Complete |
| 1 | DSL `browser.client_metrics` and optional `protocol.Result.ClientMetrics` | Complete |
| 2 | Runtime collection: Performance API + CDP, screenshots/HTML/console/network | Mostly complete; WebKit process attribution open |
| 3 | Live and archive aggregation, bounded payloads | Complete for the current metric contract |
| 4 | Dashboard cards, replay drilldown, comparison charts, authoring/validation | Mostly complete; per-resource waterfall rows open |
| 5 | Benchmark and positioning as a low-concurrency diagnostic mode | Implementation complete, evidence pending |

Tradeoffs: browser concurrency is far below HTTP density; process CPU/RSS attribution is noisy and OS-specific; live streams carry summaries while heavy artifacts stay in replay/archive drilldown. Avoid "TrueClient equivalent" naming.

### TrueClient MobileWeb import

Code-owned scope: `pkg/convert/trueclient/support.go`; `pkg/convert/enterprisearchive/zip.go` checks the parsed `.usr` type first. `TruClient - Mobile Web` is the only MVP browser-import target (`default.xml` steps convert to browser DSL actions; unsupported constructs stay visible as warnings); `TruClient 2.0 - Web` is planned separately (see [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md#web-20-browser-profile)); legacy desktop `TruClient` is out of scope with an explicit error.

Step mapping: Navigate to `browser_navigate`; Click to `browser_click`; Set/Type to `browser_fill`; Select to `browser_select`; Wait to `browser_wait`; Verify to action assertions; Start/End Transaction to transaction metadata; Scroll to `browser_swipe` (including `window.scrollTo`/`scrollBy`); Evaluate JavaScript is review-only, decomposed when simple (jQuery `.val`/`.attr('value', ...)`/`.click()`/`.trigger('click')`, `document.querySelector`, first-match DOM lookups, `document.forms[].elements[]`, window scroll, `location` navigation), other shapes stay in guided review because no public browser-eval action exists.

Import responses include `review_sections` and deterministic `repair_suggestions`; the dashboard offers draft browser replay validation before save; device profile is extracted from `.usr` and `default.cfg`. Open: sample-backed hardening (needs 3-5 representative archives as fixtures; the step format is proprietary).

### Dashboard and UX intelligence

Scenario authoring carries device preset, orientation, touch and headed/headless into queued-run payloads, capacity estimates, previews and archived comparisons. The script editor has the profile selector, orientation toggle, aspect-ratio viewport preview, touch actions and replay/config warnings. The archive has device/engine/platform rollups, LCP/INP/CLS comparison, drilldown, waterfall and explainable UX health scoring. Long-term goal: answer "will this release degrade checkout on iPhone SE over 4G?" before deploying, with anomaly detection (Layer 3, to start only once stages 1-4 produce real data).

### Browser replay HAR capture

HAR is evidence for debugging, migration review and browser-to-HTTP conversion, not a density claim.

- **V1: synthetic HAR from replay evidence** (no new required endpoint): a backend HAR 1.2 writer converts replay `network_entries` into `log.entries`; `replay.har` is in the replay ZIP export whenever network evidence exists; the Browser Replay cockpit gains `Download HAR` with artifact metadata (availability, entry count, fidelity label). Chromium CDP enrichment correlates `RequestWillBeSent`/`ResponseReceived`/`LoadingFinished`/`LoadingFailed`, keeps redirects as distinct entries and tolerates partial streams. Heavy HAR stays in artifact/export flows, never on the live WebSocket path.
- **V2: native Playwright HAR recording** (`RecordHarPath`, `RecordHarMode`, `RecordHarContent`, `RecordHarOmitContent`, URL filters) saved on context close and bundled in exports, fidelity labelled `native` vs `synthetic`, with size guardrails. Adopting it for both engines remains a V2 decision.
- Compatibility: only optional fields are added to replay entry types and manifests; existing replay endpoints and ZIP exports are unchanged.
- Tests: Go unit tests for CDP enrichment, HAR 1.2 structure, empty evidence, manifest metadata and (V2) context options; dashboard tests that `Download HAR` shows only with network evidence and fidelity labels render; `go test ./...`, `npm run build` in `dashboard`, and an in-app smoke of Script Editor Debug replay.

### Stage map and risks

| Stage | Scope | Status |
|:---|:---|:---|
| 1 Foundation | Device registry, viewport/touch emulation, touch actions, orientation | Complete |
| 2 Performance simulation | `CPUThrottleRate`/`NetworkPreset` per profile; opt-in benchmark harness and capacity advice | Implementation complete, evidence pending |
| 3 Client metrics collection | `ClientMetrics` on `protocol.Result`, `browser.client_metrics` | Complete for the current contract |
| 4 Aggregation and dashboard | Device-segmented aggregation, comparison and waterfall views | Complete for the current contract |
| 5 TrueClient import | `pkg/convert/trueclient`, device profile extraction, replay validation | Partial; sample-backed hardening open |
| 6 UX intelligence | Explainable scoring slice | First slice complete; frustration, heatmap, business-impact, anomaly work future |

| Risk | Mitigation |
|:---|:---|
| Touch events do not trigger every mobile JS handler | Test against real responsive apps; document limits |
| Device presets go stale | Data-driven, extensible profiles |
| Playwright/CDP behaviour changes | Pin the driver (`PLAYWRIGHT_DRIVER_VERSION` in the Dockerfile); test stable Chromium |
| CPU throttling distorts timing non-linearly; PerformanceObserver gaps in headless | Calibrate and document as approximation; fall back to Navigation Timing |
| Memory per browser VUser exceeds expectations | Keep canary positioning and the hybrid HTTP + browser model |
| Layer 3 scope creep | Defer until stages 1-4 produce real data |

External requirements: Chromium/Chrome on generators, Playwright WebKit assets on WebKit hosts, controlled benchmark hardware for density claims, TrueClient sample archives. Related: [Web/API execution matrix](PROTOCOL_SUPPORT.md#webapi-execution-support-matrix) and the [enterprise archive runtime settings reference](COMPETITIVE_COMPARISON.md#enterprise-archive-runtime-settings-reference).

---

## CloudTest outcome parity

Status: Active; initial vertical slices implemented.

Objective: reproduce the valuable outcomes of current Akamai CloudTest workflows without copying CloudTest repository objects or its internal Band, Maestro, alpha-grid or TouchTest architecture. A capability is complete only when its runtime/API behavior, dashboard workflow, archive/report representation and tests are all present.

The canonical machine-readable ledger is [`pkg/api/cloudtest_parity_catalog.json`](../pkg/api/cloudtest_parity_catalog.json), the only editable capability list. `GET /api/v1/system/parity-status` publishes the same rows in `cloudtest_capabilities`; the in-product view is **Settings → Integrations → Dashboards → CloudTest outcome parity**. `TestCloudTestProgramDocumentsEveryCanonicalCapability` in `pkg/api/parity_status_test.go` requires every capability ID from the catalog to appear in backticks in this document, so the ledger table below must list them all. There is no separately published Artifact/Sites deployment of the dashboard, so external publication is not counted complete.

### Evidence policy

| Confidence | Meaning |
| --- | --- |
| `current_official` | Current Akamai product or documentation page. |
| `current_api` | Current Akamai API reference. |
| `legacy_official` | Akamai/Soasta reference material whose current wire shape must be fixture-proven. |
| `inferred` | Behavior inferred from evidence and not safe to claim as exact parity. |

Starting evidence: the [CloudTest product page](https://www.akamai.com/products/cloudtest), [composition model](https://techdocs.akamai.com/cloudtest/docs/test-composition), [grid guidance](https://techdocs.akamai.com/cloudtest/docs/cloudtest-grid), [Preview Mode](https://techdocs.akamai.com/cloudtest/docs/navigate-the-user-interface), [analytics](https://techdocs.akamai.com/cloudtest/docs/analytics), [HTTP/2 targets](https://techdocs.akamai.com/cloudtest/docs/edit-target-information), [SCommand reference](https://techdocs.akamai.com/cloudtest/docs/scommand-reference-1) and [reporting](https://techdocs.akamai.com/cloudtest/docs/report-generation-analysis).

### Corrected baseline

| Capability | Verified APEX position | Program decision |
| --- | --- | --- |
| HTTP/2 | Supported by APEX and CloudTest. | Parity; no competitive gap claim. |
| Tentative live ramp preview | Existing runtime-adjustment preview is supported. | Reuse it on the composition timeline. |
| Transaction drill-down | Existing archive filters and redacted request/response inspector are partial foundations. | Extend the same archive and inspector; no `/archive/messages`. |
| Page resources | Existing HTTP resource loader and network entries are partial foundations. | Add page/hierarchy attribution; no duplicate page transport. |
| Result evidence | Failure evidence and `snapshot_on_error` are separate concerns. | Add an optional bounded message-detail policy without changing legacy behavior. |
| Streaming response pacing | Already implemented for HTTP responses. | Build media playback and metrics on it. |
| Shared dashboards | Project-scoped Grafana imports, private local drafts and saved versioned native run-metric panels exist. | Preserve existing definitions; extend native filtering, dimensions and overlays. |
| Reporting | HTML, PDF, DOCX, CSV, JSON, JSONL and Parquet exist; report charts export CSV/SVG. | Extend the shared chart export wrapper to remaining quantitative charts; do not list PDF or DOCX as missing. |
| Scale | Density numbers are design targets without published evidence. | Never present them as achieved. |
| Native mobile | TouchTest remains in current terminology. | APEX native-mobile automation is a product-fit non-goal, not a competitor-discontinuation claim. |

### Capability ledger

Stable IDs grouped for navigation; statuses live only in the JSON ledger. The API test suite validates row count, ID uniqueness, evidence/status vocabularies, required acceptance tests and outcome decisions; the competitive comparison documents the API-level CloudTest dimensions and is checked against the endpoint names.

| Group | Capability IDs |
| --- | --- |
| Authoring and load model | `timed_composition`, `reusable_clips`, `sequenced_parallel_containers`, `page_resource_model`, `repeat_models`, `conditional_switch`, `visual_timeline` |
| Capacity | `elastic_grid`, `cloud_breadth`, `prepare_mode` |
| Analytics | `regional_analytics`, `message_drilldown`, `waterfall`, `hierarchy_phase_filters`, `network_profile_dimension`, `error_analysis`, `comparison_baselines` |
| Dashboards | `composable_analytics`, `shared_dashboards`, `property_analytics` |
| Monitoring | `infrastructure_monitors`, `external_data_sources`, `monitor_correlation` |
| Execution and protocols | `live_ramp_preview`, `database_load`, `websocket`, `jms`, `streaming_media`, `extension_model` |
| Automation/reporting | `cli_automation`, `word_widget_export` |
| Migration | `cloudtest_import` |
| Explicit decisions | `native_mobile`, `rum_pairing` |

### Phase status

| Phase | Scope | State |
| --- | --- | --- |
| 0 Evidence and fixtures | Evidence-linked ledger; three sanitized CloudTest exports | Ledger, comparison view and dashboard live; fixtures not supplied, so fixture-dependent mappings stay provisional |
| 1 Live regional analytics and safe detail | `by_region` aggregation; `config.results.message_detail` modes; archive filters and waterfall | Implemented; formal run annotations and the browser bucket-to-waterfall exit test in progress |
| 2 Composition authoring and shared analytics | `start_offset`, `control_switch`, `control_parallel`, native dashboards, property analytics | Largely implemented; fixture-proven parallel-repeat mapping and richer native panel authoring in progress |
| 3 CloudTest import | `POST /api/v1/scripts/import/cloudtest` and support matrix | Implemented review-only (`blocked_draft`, `fixture_proven: false`) pending fixtures |
| 4 Run-bound monitoring and elastic capacity | Monitor sources, signed collector jobs, ephemeral capacity, prepare mode | In progress; first monitoring/preparation workflows implemented |
| 5 Media, automation, reporting | `media_stream`, named-transaction SLA gates, DOCX, chart export | SLA gates, `apexctl library`, DOCX and report chart CSV/SVG landed; media and remaining chart export open |

### Phase 0: evidence and migration fixtures

Acquire three sanitized, dependency-complete XML or ZIP exports (basic HTTP Clip, multi-track Composition, complex repository export) and record the CloudTest build and export options beside each immutable fixture. All jsDoc and CloudLink-only mappings remain provisional until a fixture proves the wire shape. Exit gate: every claim has a URL and confidence; each mapping is fixture-proven or provisional; no source claims CloudTest lacks HTTP/2 or that TouchTest is discontinued. The ledger, comparison view, generated API contract and filtered dashboard are live; the fixtures and external Artifact publication remain open gates.

### Phase 1: live regional analytics and safe detail

Bounded `by_region` aggregation flows through engine snapshots, distributed protobuf summaries, WebSocket messages, the dashboard and archive reconciliation; regions are controller-approved load-zone labels, not unbounded script values. Optional `config.results.message_detail` modes `off`, `errors`, `sampled`, `adaptive` use deterministic sampling and item, item-byte and run-byte caps partitioned across generators and re-enforced at archive admission (failures outrank outliers, which outrank representative successes; snapshot bounds and secret redaction still apply). The transaction archive gains region, network profile, source transaction, hierarchy, phase and detail-presence filters, stable event/trace IDs, metadata-only WebSocket notifications, a bounded authorized live-detail endpoint and DNS/connect/TLS/TTFB waterfall timings. Timestamped annotations and named load-stage phase ranges are distinct from report notes. Exit gate: a two-region run reconciles live and archive totals; caps hold under load; redaction proves no leakage; a browser test walks bucket to transaction to waterfall. In progress: formal run annotations and that browser test.

### Phase 2: composition authoring and shared analytics

- Active-time `start_offset` on scenario groups flows through storage, revisions, scheduling, capacity validation, snapshots, provenance, archives and reports; paused time does not consume an offset; launch admission evaluates overlap-aware group windows; the timeline and form/JSON editors share one scenario model (scripts referenced by groups already satisfy reusable Clip outcomes).
- CEL-backed ordered first-match `control_switch` is cataloged, validated, authorable, replayed, redacted and executed. Project-gated `control_parallel` provides named branches, bounded concurrency, isolated variable/protocol instances, deterministic `var.parallel.<branch>` extraction merge, container metrics and runtime safety admission; nested parallel, rendezvous, loop control, database transactions, persistent WebSocket, browser and Java Vuser actions remain rejected until advertised safe. Count, foreach, while and scenario iteration models are implemented; fixture-proven parallel-repeat mapping is partial. Typed hierarchy/page attribution reaches execution and archives, reusing the HTTP document resource loader for Page semantics.
- Project-scoped versioned `DashboardDefinition` with optimistic revision checks and a code-owned bounded query catalog; private localStorage drafts and Grafana imports keep their contracts; richer native panel authoring in progress. Bounded property analytics now require explicit promotion of non-sensitive extraction keys and finite approved categories; generators fold other values into `other` and absent values into `missing`. Versioned capability negotiation, live distributions and archive action-population reports are implemented. Native panel creation and saved-panel rendering support bounded run measurements plus archived action/transaction and property filters, saved two-run comparisons and bucket drill-down. Bounded single-dimension grouping, filtered live publication histories and authorized monitor/imported overlays are implemented in the follow-on slice. Live intersections, additional dimensions and live lag correlation remain open.
- Exit gate: offset groups execute at active-time 0/60/120 through pause/resume; switch and permitted parallel replay match load execution; a second project user reads a shared dashboard while private drafts stay private.

### Phase 3: first-class CloudTest import

CloudTest is registered in the import registry: bounded XML or dependency-closure ZIP uploads through the standard inspection, encryption, attestation, parameterization, validate, commit and batch-commit paths; one script per Clip and one scenario draft per Composition plus target, environment, data-pool, monitor, dashboard and review artifacts. Every recognized element is `executable_exact`, `executable_bounded`, `blocked_draft` or `rejected`; unknown elements retain path/type evidence. No runtime `call_script` (groups already reuse scripts); irreducible nested launch semantics, if fixtures prove them, would use an immutable controller-resolved `script_ref` with cycle/depth checks. Implemented: `POST /api/v1/scripts/import/cloudtest`, `GET .../cloudtest/support-matrix`, the **Akamai CloudTest Export** dashboard flow and atomic batch save of Clip drafts; the upload is held in an encrypted short-lived session and becomes a durable encrypted source revision on commit. All mappings stay `blocked_draft` with `fixture_proven: false`. Exit gate: golden fixtures have zero unclassified known elements and one imported composition batch-commits and executes.

### Phase 4: run-bound monitoring and elastic capacity (in progress)

Implemented behind project flags: durable Linux/Prometheus source bindings and signed jobs, collector retry spooling, immutable run artifacts, common lagged correlation, and a fenced preparing/ready/consumed lifecycle with revision checks, renewal, expiry and cancellation. Existing Kubernetes, Azure Container Apps/Jobs and ECS asynchronous provisioning is reused; provisioning is not a new dependency. Local HTTP/TCP zero-traffic and generator preflight regression tests cover the common boundary. Preparation now accepts immutable existing/ephemeral capacity policies, with ECS spot ceilings and on-demand floors. Unsupported required policies fail explicitly. Live acceptance for every target/runtime/provider combination remains required; local deterministic tests do not establish cloud production readiness.

Collector artifacts, project sources/bindings, signed assigned jobs, sealed expiring credentials, destination approvals, SSH host-key pins and restricted command/query packs are implemented. Prometheus, OTLP, SSH/Linux and restricted JMX/JDBC have deterministic/local coverage; CloudWatch and Azure Monitor are implemented previews pending approved live acceptance. SNMP, WMI/PerfMon, New Relic, AppDynamics and GCP remain customer-evidence-dependent work; CA Introscope and unrestricted commands remain non-goals. Existing/ephemeral capacity policies and capability-gated spot policies are implemented, including ECS percentage ceilings and an on-demand floor; unsupported required policies fail explicitly. Remaining exit gate: verify zero target traffic, takeover, reservation loss and cleanup for each target/runtime/provider combination, with capacity retained until archive terminality and no silent cross-region/cloud fallback.

### Phase 5: media, automation and reporting

Add a bounded HTTP `media_stream` action for HLS or DASH (live/VOD refresh, adaptive/fixed rendition, alternate CDN targets, player/network presets); CMAF is a container format, not a separate manifest protocol; report manifest/startup/segment latency, bitrate switches, playback ratio, estimated buffer, rebuffer risk/duration, errors, CDN and region; HDS stays review-only. Landed: exact named-transaction SLA rules evaluated over the closed transaction archive with bounded CLI flags/rule files, structured API/archive/report evidence, JSON/JUnit and fail exit codes; `apexctl library export|import` over the library federation bundle APIs; Executive/Engineer/Audit DOCX generated as OOXML from the shared section registry; definition-derived CSV and accessible SVG for report charts. Monitor source/binding, preparation and analysis CLI commands are implemented. Open: the export wrapper on remaining live quantitative charts and media. Exit gate: local media fixtures, named-transaction CI gates, DOCX round-trip inspection and chart export browser tests pass.

### Cross-phase compatibility and rollout

New fields are optional: offsets default to zero, local dashboard drafts and Grafana imports remain usable, absent capture policy keeps legacy evidence behavior, archive and protobuf schemas keep old readers, and admission fails closed if a generator lacks required capabilities. Each phase requires Go unit/distributed tests, SQLite and PostgreSQL migrations, API authorization/bounds/pagination checks, dashboard tests and browser verification, generated DSL/API freshness, `go test ./...` and `python3 scripts/check-docs.py`. Risky features start disabled, then progress internal/local, single-project preview, enterprise opt-in, and default-on only after compatibility, security and browser acceptance gates pass.
