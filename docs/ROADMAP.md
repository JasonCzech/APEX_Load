# Roadmap

Status: Roadmap. This page identifies remaining work and qualification gates. Implementation is not evidence of full feature or production qualification. Current usage belongs in the [documentation index](INDEX.md); requirement definitions belong in the [feature completion specification](FEATURE_COMPLETION_SPEC.md).

The [dated roadmap snapshot](archive/ROADMAP_2026-10-19.md) retains the former delivery narratives, design decisions and phase history. [Review records](qa/reviews/README.md) preserve exact checks, failed attempts, accepted limits and evidence. Older slice-level backlog statements can be superseded by later waves; use the linked acceptance inventories and requirement records before scheduling work.

The proposed [remaining-roadmap implementation specification](ROADMAP_IMPLEMENTATION_SPEC.md) reconciles current code with the remaining telemetry, non-Messaging protocol diagnostics, authoring, analytics, browser and recovery work. It defines dependencies and acceptance gates; Messaging and CloudTest outcome parity remain outside that plan.

## Track status

As of 2026-10-21, reconciled against main at `d639a646f` (PR #260), including merged [PR #256](https://github.com/JasonCzech/APEX_Load/pull/256) and the staged browser slices in draft PRs #261–#263. Completed slices below do not close their parent acceptance requirements. Work-package IDs refer to the [implementation specification](ROADMAP_IMPLEMENTATION_SPEC.md); its linked review records retain the precise scope and evidence.

| Track | Completed scope to preserve | Remaining work / authority |
| --- | --- | --- |
| Integration and release | Published peer fixes from PR #254 integrated; precision consent and browser workflow-store isolation committed in PR #256. [Local integration review](qa/reviews/roadmap-e2e-isolation-2026-10-20/README.md) retains regression, Help, private HAR and review evidence. | PR #256 merged as `0ae073d85`; the earlier budget-blocked CI attempts remain recorded. Hosted package results do not close the external qualification gates below. |
| [Runtime telemetry SLA](#runtime-telemetry-sla) | Durable controller cursors/source-window provenance, coordinated-start clock correction, monotonic browser timing and atomic notification outbox/runtime slices. | TEL-A/B: qualified source-clock coverage, publication-to-visible-render correlation, complete eligible populations, in-run policy segments and consumer/sanitization coverage. TEL-C: separate-host reference workload, collector/native-analysis load, overload and fault qualification. [Acceptance inventory](qa/TELEMETRY_ACCEPTANCE.md). |
| [Protocol telemetry parity](#protocol-telemetry-parity) | Java encoding/body conversion and versioned opaque-byte representations; controlled local gRPC and WebSocket measurements. | PROTO-A/B: approved JVM-helper object-footprint cohort, complete non-Messaging consumer coverage and controlled-endpoint/reference qualification beyond the retained local matrix. [Measurement matrix](qa/PROTOCOL_TELEMETRY_MATRIX.md). |
| [Script authoring consolidation](#script-authoring-consolidation) | Archive approval and scenario prerequisite handoff fixes; existing revision-conditioned writes and shared owners. | AUTH-A/B: durable client intent, negotiated atomic original-outcome receipts and context-bound validation. AUTH-C: complete property-level lifecycle coverage and prove all callers before further adapter retirement. [Lifecycle matrix](qa/LIFECYCLE_PARITY_MATRIX.md), [retirement inventory](qa/ADAPTER_RETIREMENT.md). |
| [PT Analysis Parquet import](#pt-analysis-native-parquet-import) | Timestamp/DST handling, partition-precedence qualification, mapping reassociation and joint-admission bounds; source-bound decimal precision consent is locally validated and merged through PR #256. | NA-A–C: remaining mapping/schema semantics, joint categories and execution-entry-point/source-window parity, typed byte queries, pinned panel/CLI workflows and durable remote-writer lifecycle. NA-D: 1 GiB reference workload, four native release lanes, restart/HA and remote-backend acceptance. [Acceptance inventory](qa/ANALYTICS_ACCEPTANCE.md). |
| [Browser and mobile](#browser-and-mobile) | HAR finalization deadline and outstanding-request bounds, benchmark byte-unit artifacts and baseline qualification guards; local [interaction map cohort separation](qa/reviews/roadmap-browser-map-cohorts-2026-10-20/README.md) and [native page-close/renderer-crash evidence](qa/reviews/roadmap-browser-native-failure-2026-10-20/README.md), plus [Chromium process loss and pool replacement](qa/reviews/roadmap-browser-process-loss-2026-10-20/README.md), the [engine authored-step identity foundation](qa/reviews/roadmap-browser-authored-identity-2026-10-20/README.md) and [original-source replay/run binding](qa/reviews/roadmap-browser-original-source-2026-10-21/README.md) and [observed Sync clocks](qa/reviews/roadmap-browser-outcome-clocks-2026-10-21/README.md), [negotiated native dispatch with qualified interaction analysis](qa/reviews/roadmap-browser-dispatch-v2-2026-10-20/README.md), and [WebKit loss / alternate Chromium BFCache fixtures](qa/reviews/roadmap-browser-native-lifecycle-2026-10-20/README.md), plus the [local managed author/replay/run/archive/HAR chain](qa/reviews/roadmap-browser-managed-chain-2026-10-20/README.md) and [capture omission retention](qa/reviews/roadmap-browser-har-omissions-2026-10-20/README.md), plus the [atomic BE-to-BI promotion gate](qa/reviews/roadmap-browser-promotion-2026-10-20/README.md) and [pinned per-metric policy floors](qa/reviews/roadmap-browser-metric-floors-2026-10-21/README.md) and [explicit run policy revisions](qa/reviews/roadmap-browser-policy-selection-2026-10-21/README.md) and [separate browser tuple panels](qa/reviews/roadmap-browser-tuple-panels-2026-10-21/README.md), plus native authored-navigation bindings, frozen evaluated panel inputs and [controller durable replay recovery](qa/reviews/roadmap-browser-replay-http-2026-10-21/README.md) (see the [implementation specification](ROADMAP_IMPLEMENTATION_SPEC.md)). | BE-A–C: remaining document/lifecycle qualification, native crash/multi-host HAR recovery qualification, real supported TruClient fixtures and measured engine/platform/density cohorts. BI-A–C: compatible baseline selection, broader authored interaction outcomes, provisional live findings and real-run qualification. Frozen evaluated panel inputs are implemented; full cohort qualification remains open. [Browser](qa/BROWSER_ACCEPTANCE.md) and [intelligence](qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md) acceptance inventories. |
| Recovery and storage | Narrow committed-replacement adoption and bounded same-leader intervention with retained quarantine/proof requirements. | REC-A–D: earlier evacuation phases and missing replacements, complete proof-based cleanup, repeated integrated local fault trials and deployed multi-host/node/zone/storage qualification. [Node-failure readiness](NODE_FAILURE_READINESS_PLAN.md), [storage recovery](NODE_STORAGE_RECOVERY.md), [HA continuity](HA_RUN_CONTINUITY_PLAN.md). |
| [Messaging](#messaging) | Existing scoped provider results remain in their own evidence records. | Outside this implementation plan: Event Hubs recovery/live Azure, broader Kafka/provider qualification and native transports. |
| [CloudTest outcome parity](#cloudtest-outcome-parity) | Existing capability ledger and scoped reviews remain authoritative. | Outside this implementation plan: sanitized export fixtures, target/provider acceptance and remaining media work. |

Separate reference hosts, Linux/Windows native qualification runners, sanitized real TruClient exports and a disposable multi-host recovery environment were reported unavailable. These dependencies leave the corresponding qualification gates pending; hosted package builds alone do not satisfy native install/query/cancel/uninstall or deployed fault acceptance. Implementation work in the table remains open independently of those environment dependencies.

## Runtime telemetry SLA

The under-two-second collection-to-dashboard objective remains a design target until measured qualification establishes it. Immutable policy receipts, stage evidence, comparison verdicts and reporting have implementation records; do not schedule those entire features again from an older slice narrative.

Remaining gates are defined by TEL-1–TEL-4 in the [completion specification](FEATURE_COMPLETION_SPEC.md#8-telemetry-reporting-completion) and the [telemetry acceptance inventory](qa/TELEMETRY_ACCEPTANCE.md). The current reference harness does not establish collector traffic, overload, separate-host generators, native DuckDB analysis, or the specified 4 vCPU/16 GiB controller qualification. Capture-to-visible-render cannot be proved by adding stage percentiles. Live AWS/Azure collector acceptance remains separate from local fixtures.

Current operation: [observability](OBSERVABILITY_INTEGRATIONS.md), [analysis workflows](ANALYSIS_WORKFLOWS.md), and [policy receipts](guides/TELEMETRY_POLICY_RECEIPTS.md).

## Protocol telemetry parity

Java HTTP now retains versioned opaque-byte representation measurements; measured JVM object footprint remains a separate helper cohort. Provider acknowledgement/settlement semantics and broker-managed dead-letter discovery remain in the excluded Messaging track. Do not fill unavailable gRPC transport phases with estimates or promote local protocol fixtures into broker certification.

Current semantics and gaps: [protocol diagnostics](PROTOCOL_DIAGNOSTICS.md), [protocol support](PROTOCOL_SUPPORT.md), and [messaging requirements](FEATURE_COMPLETION_SPEC.md#5-messaging-completion).

## Script authoring consolidation

Close AUTH-1–AUTH-3 using the [lifecycle parity matrix](qa/LIFECYCLE_PARITY_MATRIX.md) and [adapter retirement inventory](qa/ADAPTER_RETIREMENT.md). Shared owners, revision-conditioned saves and lifecycle improvements have landed in staged work; remaining callers and field coverage must be demonstrated before removing an adapter. Completed implementation slices do not close the parent requirements.

Current workflow: [script lifecycle](SCRIPT_LIFECYCLE.md) and [scenario fidelity](guides/AUTHORING_SCENARIO_FIDELITY.md). The completed native live-table plans are [historical design records](archive/README.md#completed-live-table-plans); live tables are not an outstanding VTS implementation gate.

## PT Analysis native Parquet import

Use NA-1–NA-6 and the [analytics acceptance inventory](qa/ANALYTICS_ACCEPTANCE.md) for remaining query/workflow, containment and release gates. Summary/histogram imports, partition manifests, identities, source-bound decimal precision consent and saved panel revisions have implementation records; distinguish their bounded supported contracts from broader analytical completeness.

Outstanding qualification includes the reproducible **1 GiB compressed-input** reference workload on the specified 4 vCPU/16 GiB host, all four native packaging lanes, controller restart/HA takeover and remote artifact-backend lifecycle. Correctness fixtures and local SQLite/PostgreSQL checks do not establish these outcomes. The **10 GB benchmark is waived, not passed**; that waiver does not waive the 1 GiB gate.

Current operation: [analysis workflows](ANALYSIS_WORKFLOWS.md), [imported identities](NATIVE_ANALYTICS.md#imported-measurement-identities), and [baseline comparison](BASELINE_COMPARISON.md).

## Messaging

Use EH-1–EH-3 and MSG-1–MSG-6 in the [completion specification](FEATURE_COMPLETION_SPEC.md) to distinguish delivered scopes from pending provider acceptance.

- Event Hubs: complete protected live Azure rebalance, crash recovery and epoch takeover qualification. Processor progress, checkpoints, capacity and strict-pause implementation do not establish that acceptance.
- Kafka: preserve the exact Apache Kafka 3.9.2 single-broker qualification scopes; multi-broker failover, restart continuity, transaction revocation/cancellation, other versions, hosted brokers and Event Hubs Kafka require additional evidence.
- Service Bus and Solace: qualify approved live provider environments and outstanding management/recovery semantics. Management readers do not enable native Solace SMF/JMS load execution.
- Native Solace SMF and IBM MQ remain separate transport work. **IBM MQ remains very low priority.** Default-versus-opt-in real-adapter packaging and native SDK choices remain explicit decisions.

Current support: [messaging protocols](PROTOCOL_SUPPORT.md#broker-and-messaging-support), [Event Hubs operation](EVENT_HUBS_HOWTO.md). Scoped results and remaining acceptance are preserved in [completed messaging reviews](archive/qa-reviews/MESSAGING_2026-10.md).

## Browser and mobile

Use BE-1–BE-4 and BI-1–BI-4 with the [browser acceptance inventory](qa/BROWSER_ACCEPTANCE.md) and [intelligence acceptance inventory](qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md). Capture, document finality, findings, baseline anomalies, interactions, journeys and report workflows have staged implementations; broader browser/platform acceptance and measured benchmark publication remain distinct gates. Native mobile automation remains a product-fit non-goal.

Preserve the diagnostic canary maximum of **5 browser VUsers per generator**, starting with 1–2. Higher density requires `APEXLOAD_BROWSER_DENSITY_OVERRIDE=true` and attached benchmark evidence; internal capacity ceilings are not published density claims. The [benchmark procedure](PERFORMANCE_BENCHMARKS.md#browser-benchmark-artifacts) and [historical capacity rationale](archive/ROADMAP_2026-10-19.md#capacity-ceiling-published-density-versus-internal-ceilings) retain the qualification details.

Current workflows: [browser support](PROTOCOL_SUPPORT.md#browser-execution-and-mobile-emulation), [document evidence](guides/BROWSER_DOCUMENT_EVIDENCE.md), [HAR ownership](guides/BROWSER_HAR_WORKING_OWNERSHIP.md), and [browser intelligence](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md). Real TruClient/CloudTest export fixtures and actual device/session qualification must not be inferred from synthetic fixtures.

Browser intelligence policy editing now supports pinned [per-metric baseline floors](qa/reviews/roadmap-browser-metric-floors-2026-10-21/README.md); compatible-run selection and external cohort qualification remain pending.

## CloudTest outcome parity

The [canonical capability ledger](../pkg/api/cloudtest_parity_catalog.json) owns capability status. The IDs below remain the navigation inventory required by the API documentation contract; this page does not duplicate their status values.

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

Remaining gates:

- Acquire three sanitized dependency-complete exports (basic HTTP Clip, multi-track Composition, complex repository export), including the CloudTest build/export options. Import mappings remain provisional/review-only until fixtures establish their wire semantics; parallel-repeat and Burst semantics need that evidence.
- Complete target/runtime/provider acceptance for zero-traffic preparation, takeover, reservation loss and cleanup; local fixtures do not establish live cloud readiness.
- Complete remaining media playback and analytics breadth against the canonical ledger. HTTP media streaming design and phase decisions remain in the [historical plan](archive/ROADMAP_2026-10-19.md#phase-5-media-automation-and-reporting).
- Preserve rollout gates, old-reader compatibility, explicit unsupported-capability results and project authorization. Existing report formats, CLI automation and chart exports must not be relisted as wholly missing.

Current comparison and migration guidance: [competitive comparison](COMPETITIVE_COMPARISON.md#akamai-cloudtest). Detailed historical decisions: [CloudTest phase record](archive/ROADMAP_2026-10-19.md#cloudtest-outcome-parity).
