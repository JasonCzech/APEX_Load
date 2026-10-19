# Roadmap

Status: Roadmap. This page identifies remaining work and qualification gates. Implementation is not evidence of full feature or production qualification. Current usage belongs in the [documentation index](INDEX.md); requirement definitions belong in the [feature completion specification](FEATURE_COMPLETION_SPEC.md).

The [dated roadmap snapshot](archive/ROADMAP_2026-10-19.md) retains the former delivery narratives, design decisions and phase history. [Review records](qa/reviews/README.md) preserve exact checks, failed attempts, accepted limits and evidence. Older slice-level backlog statements can be superseded by later waves; use the linked acceptance inventories and requirement records before scheduling work.

## Track status

| Track | Remaining work / authority |
| --- | --- |
| [Runtime telemetry SLA](#runtime-telemetry-sla) | Reference-workload qualification and remaining TEL acceptance; measured latency, not inferred stage-percentile sums |
| [Protocol telemetry parity](#protocol-telemetry-parity) | Additional protocol-specific measurements and real-provider qualification |
| [Script authoring consolidation](#script-authoring-consolidation) | Remaining lifecycle parity and proven adapter retirement |
| [PT Analysis Parquet import](#pt-analysis-native-parquet-import) | Native packaging, restart/HA/remote-backend and reference-load acceptance |
| [Messaging](#messaging) | Event Hubs recovery/live Azure, broader Kafka/provider qualification and native transports |
| [Browser and mobile](#browser-and-mobile) | Browser acceptance, density measurements and fixture-proven import fidelity |
| [CloudTest outcome parity](#cloudtest-outcome-parity) | Sanitized export fixtures, target/provider acceptance and remaining media work |
| Recovery and storage | [Node-failure readiness](NODE_FAILURE_READINESS_PLAN.md), [storage recovery](NODE_STORAGE_RECOVERY.md), and [HA continuity](HA_RUN_CONTINUITY_PLAN.md) retain their separate implementation and fault-qualification limits |

## Runtime telemetry SLA

The under-two-second collection-to-dashboard objective remains a design target until measured qualification establishes it. Immutable policy receipts, stage evidence, comparison verdicts and reporting have implementation records; do not schedule those entire features again from an older slice narrative.

Remaining gates are defined by TEL-1–TEL-4 in the [completion specification](FEATURE_COMPLETION_SPEC.md#8-telemetry-reporting-completion) and the [telemetry acceptance inventory](qa/TELEMETRY_ACCEPTANCE.md). The current reference harness does not establish collector traffic, overload, separate-host generators, native DuckDB analysis, or the specified 4 vCPU/16 GiB controller qualification. Capture-to-visible-render cannot be proved by adding stage percentiles. Live AWS/Azure collector acceptance remains separate from local fixtures.

Current operation: [observability](OBSERVABILITY_INTEGRATIONS.md), [analysis workflows](ANALYSIS_WORKFLOWS.md), and [policy receipts](guides/TELEMETRY_POLICY_RECEIPTS.md).

## Protocol telemetry parity

Extend measured protocol-specific intelligence where evidence is available: Java HTTP object-versus-wire deltas, provider acknowledgement/settlement semantics and broker-managed dead-letter discovery. Do not fill unavailable gRPC transport phases with estimates or promote local protocol fixtures into broker certification.

Current semantics and gaps: [protocol diagnostics](PROTOCOL_DIAGNOSTICS.md), [protocol support](PROTOCOL_SUPPORT.md), and [messaging requirements](FEATURE_COMPLETION_SPEC.md#5-messaging-completion).

## Script authoring consolidation

Close AUTH-1–AUTH-3 using the [lifecycle parity matrix](qa/LIFECYCLE_PARITY_MATRIX.md) and [adapter retirement inventory](qa/ADAPTER_RETIREMENT.md). Shared owners, revision-conditioned saves and lifecycle improvements have landed in staged work; remaining callers and field coverage must be demonstrated before removing an adapter. Completed implementation slices do not close the parent requirements.

Current workflow: [script lifecycle](SCRIPT_LIFECYCLE.md) and [scenario fidelity](guides/AUTHORING_SCENARIO_FIDELITY.md). The completed native live-table plans are [historical design records](archive/README.md#completed-live-table-plans); live tables are not an outstanding VTS implementation gate.

## PT Analysis native Parquet import

Use NA-1–NA-6 and the [analytics acceptance inventory](qa/ANALYTICS_ACCEPTANCE.md) for remaining query/workflow, containment and release gates. Summary/histogram imports, partition manifests, identities and saved panel revisions have implementation records; distinguish their bounded supported contracts from broader analytical completeness.

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
