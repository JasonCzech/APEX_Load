# Roadmap

Status: Roadmap. This page identifies remaining work and qualification gates. Implementation is not evidence of full feature or production qualification. Current usage belongs in the [documentation index](INDEX.md); requirement definitions belong in the [feature completion specification](FEATURE_COMPLETION_SPEC.md).

The [dated roadmap snapshot](archive/ROADMAP_2026-10-19.md) retains the former delivery narratives, design decisions and phase history. [Review records](qa/reviews/README.md) preserve exact checks, failed attempts, accepted limits and evidence. Older slice-level backlog statements can be superseded by later waves; use the linked acceptance inventories and requirement records before scheduling work.

The [remaining-roadmap implementation specification](archive/plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md) defines the October 28 non-Messaging wave dependencies and acceptance gates; Messaging and CloudTest outcome parity remain outside that plan.

## Track status

As of 2026-10-29, reconciled against `main` through `3b9494318`, including merged non-Messaging roadmap completion (PR #300), HTTP authoring unification (PR #302), post-merge CI/bundle/race/Windows path fixes (PR #303), distributed resource metrics across gRPC and archives (PR #305), run-results and telemetry-policy UX polish (PR #306), and debugger/Source Mapping fixes. Completed implementation slices and local integration gates do not close external hardware, multi-node cloud, native release-lane, or real-customer fixture acceptance requirements. Work-package IDs refer to the [implementation specification](archive/plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md); its linked review records retain the precise scope and evidence.

| Track | Completed scope to preserve | Remaining work / authority |
| --- | --- | --- |
| Integration and release | Wave 4 `INT-1` end-to-end non-Messaging release gate (`TestRoadmapIntegratedNonMessagingReleaseScenario`, [review record](qa/reviews/roadmap-int-1-2026-10-28/README.md)), HTTP authoring unification (`U1–U6`), post-merge bundle/race/Windows path fixes, and per-file browser workflow-store isolation ([review](archive/qa-reviews/INTEGRATION_AND_CODEBASE_REVIEWS_2026-10.md#roadmap-e2e-isolation-2026-10-20)). | Hosted package builds and local integration gates do not close the external reference-host, multi-node cloud, live provider, or customer-fixture qualification gates below. |
| [Runtime telemetry SLA](#runtime-telemetry-sla) | `TEL-A1/A2` and `TEL-B1/B2`: calibrated publication-window start bounds, foreground presentation proofs, durable `RenderPopulation` checkpointing (migration v86), qualified source-window coverage v4, append-only multi-segment in-run policies, `telemetry_evidence.json` archive sidecars, and 10-consumer scrubbed SLA projection parity. | `TEL-C`: separate-host 4 vCPU/16 GiB controller reference workload with two generator hosts, live cloud collector and native DuckDB load, and overload/fault qualification. [Acceptance inventory](qa/TELEMETRY_ACCEPTANCE.md). |
| [Protocol telemetry parity](#protocol-telemetry-parity) | `PROTO-A1/A2` and `PROTO-B1`: Java HTTP wire-versus-representation diagnostics (`serialized_representation_vs_http_body`), contained JVM object-footprint helper (`contained_jvm_helper_v1`), propagation across native analytics/baseline comparison/archive reports, and the controlled-endpoint gRPC, WebSocket, and Java HTTP telemetry matrix. | External multi-host/reference endpoint qualification beyond the retained controlled matrix. [Measurement matrix](qa/PROTOCOL_TELEMETRY_MATRIX.md). |
| [Script authoring consolidation](#script-authoring-consolidation) | `AUTH-A1/B1/C1` and `U1–U6` HTTP authoring unification: Contract v2 durable authoring receipts with live/offline credential-key rotation, `ScenarioValidationContext` composite digest over constituent scripts/policies/approvals/secret bindings, real-HTTP session-backed import persisted-copy parity, unified HTTP inspect/review/commit across single-script launchers, and zero open implementation cells in the [lifecycle matrix](qa/LIFECYCLE_PARITY_MATRIX.md) and [retirement inventory](qa/ADAPTER_RETIREMENT.md). | Real-customer migration corpus qualification and ongoing editor/debugger hardening below. [Lifecycle matrix](qa/LIFECYCLE_PARITY_MATRIX.md), [retirement inventory](qa/ADAPTER_RETIREMENT.md). |
| [PT Analysis Parquet import](#pt-analysis-native-parquet-import) | `NA-A1`, `NA-B1`, `NA-C1/C2/C3`, and `NA-D1` (`darwin/arm64`): timestamp/DST handling, partition-precedence qualification, source-bound decimal precision consent, multi-chunk duplicate provenance, durable `JointStreams` failover sequence ledger, admitted `http`/`grpc`/`websocket` byte query catalog, frozen `PanelInputs` v2 (`query_version: 5`) with `SavedAnalysisPanels` staleness tracking, mint-time `_analysis/source.v1` volume binding with worker staging reclamation and `POST /api/v1/analysis/imports/{id}/attest-writer-stopped`, plus [macOS native reference fault trials](qa/reviews/roadmap-na-d1-local-macos-2026-10-28/README.md). | `NA-D`: reproducible 1 GiB compressed-input reference workload on the 4 vCPU/16 GiB host, fresh native packaged install/query/cancel/uninstall qualification across all four release lanes (`linux/amd64`, `linux/arm64`, `darwin/arm64`, `windows/amd64`), and deployed multi-replica HA/remote-backend acceptance. [Acceptance inventory](qa/ANALYTICS_ACCEPTANCE.md). |
| [Browser and mobile](#browser-and-mobile) | `BE-A–C` and `BI-A–C` codebase and local qualification: Chromium and WebKit managed author/replay/run/archive/HAR chain, capture omission retention, v3 inherited-lease crash and replacement-container HAR reclamation, structural `StepPath` authored identity, observed Sync and navigation clocks, negotiated native dispatch, 30s provisional live intelligence, pinned per-metric baseline floors, explicit run policy revisions, frozen compatible baseline selection, separate browser tuple panels, and frozen `PanelInputs` v2 (see [completed browser reviews](archive/qa-reviews/BROWSER_SLICES_2026-10.md) and [analytics reviews](archive/qa-reviews/ANALYTICS_SLICES_2026-10.md)). | Sanitized real customer TruClient exports, real device/Appium session qualification, and multi-platform benchmark publication beyond the 5-browser-VUser canary ceiling. [Browser](qa/BROWSER_ACCEPTANCE.md) and [intelligence](qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md) acceptance inventories. |
| Recovery and storage | `REC-A1/A2`, `REC-B1`, and `REC-C1`: opt-in v4–v6 successor evacuation rollback/roll-forward/stateless-HTTP replacement restart, asymmetric network-partition and successive-leader quarantine convergence, operator intervention UI, and the 21/21 `-race` 3-repetition integrated local fault qualification harness ([review record](qa/reviews/roadmap-rec-c-local-faults-2026-10-28/README.md)). | `REC-D`: deployed multi-node cloud cluster soak, physical CSI/RWX storage interruption, and live cloud node/zone drain qualification. [Node-failure readiness](NODE_FAILURE_READINESS_PLAN.md), [storage recovery](ARCHIVE_SOURCE_RETENTION.md#node-storage-recovery), [HA controller behavior](ENTERPRISE_PLATFORM_RUNBOOK.md#ha-controller-behavior). |
| [Messaging](#messaging) | Existing scoped provider results remain in their own evidence records. | Outside this implementation plan: Event Hubs recovery/live Azure, broader Kafka/provider qualification and native transports. |
| [CloudTest outcome parity](#cloudtest-outcome-parity) | Existing capability ledger and scoped reviews remain authoritative. | Outside this implementation plan: sanitized export fixtures, target/provider acceptance and remaining media work. |

Separate 4 vCPU/16 GiB reference hosts, Linux/Windows native qualification runners, sanitized real TruClient exports, and a disposable multi-node cloud recovery cluster remain external prerequisites. Hosted package builds and single-host integration harnesses do not satisfy native packaged install/query/cancel/uninstall or deployed multi-node fault acceptance.

## Runtime telemetry SLA

The under-two-second collection-to-dashboard objective remains a design target until measured separate-host qualification establishes it. Immutable multi-segment policy receipts, calibrated publication-window bounds, foreground presentation proofs, source-window coverage v4, `telemetry_evidence.json` archive sidecars, and 10-consumer scrubbed projection parity are implemented; do not schedule those features again from an older slice narrative.

Remaining gates are defined by TEL-1–TEL-4 in the [completion specification](FEATURE_COMPLETION_SPEC.md#8-telemetry-reporting-completion) and `TEL-C` in the [telemetry acceptance inventory](qa/TELEMETRY_ACCEPTANCE.md). The in-process reference harness does not establish live cloud collector traffic, sustained overload, separate-host generators, concurrent native DuckDB analysis on the declared 4 vCPU/16 GiB controller hardware, or live AWS/Azure collector acceptance. Capture-to-visible-render cannot be proved by adding marginal stage percentiles.

Current operation: [observability](OBSERVABILITY_INTEGRATIONS.md), [native analytics](NATIVE_ANALYTICS.md), and [policy receipts](guides/TELEMETRY_POLICY_RECEIPTS.md).

## Protocol telemetry parity

Java HTTP retains versioned wire-versus-representation diagnostics and the contained JVM object-footprint helper cohort (`contained_jvm_helper_v1`), propagated across native analytics, baseline comparison, and archive reports alongside the local controlled-endpoint gRPC and WebSocket matrix. Provider acknowledgement/settlement semantics and broker-managed dead-letter discovery remain in the excluded Messaging track. Do not fill unavailable gRPC transport phases with estimates or promote local protocol fixtures into broker or external multi-host certification.

Current semantics and gaps: [protocol diagnostics](PROTOCOL_DIAGNOSTICS.md), [protocol support](PROTOCOL_SUPPORT.md), and [messaging requirements](FEATURE_COMPLETION_SPEC.md#5-messaging-completion).

## Script authoring consolidation

AUTH-1–AUTH-3 implementation and local lifecycle parity are tracked in the [lifecycle parity matrix](qa/LIFECYCLE_PARITY_MATRIX.md) and [adapter retirement inventory](qa/ADAPTER_RETIREMENT.md). Shared script and scenario owners, `If-Match` revision fencing, Contract v2 authoring receipts with credential-key rotation, `ScenarioValidationContext` composite digests, and unified session-backed HTTP import review/commit have landed; real-customer corpus migration qualification remains governed by fixture availability.

### Debugger disclosure and editor review backlog

Debugger evidence/resizable editor panel improvements (`bd4241a68`) and independent Source Mapping scrollports (`3b9494318`) have landed. Continue hardening debugger disclosure and editor edge cases as follow-up work: examine the exported gRPC header flattener's handling of invalid UTF-8 in a maximum-length binary metadata name for direct callers (production callers normalize names first), review live replay event selection when WebSocket delivery begins mid-run with absolute event indices against a dense client list, and add live receipts for compacted extraction evidence and reconstructed parameter-inspector history.

Review future fixes under the repository's independent review and verification-proof protocol, and validate embedded dashboard assets, affected browser workflows in both authentication profiles, and cross-package behavior as appropriate.

Current workflow: [script lifecycle](SCRIPT_LIFECYCLE.md) and [scenario fidelity](guides/AUTHORING_SCENARIO_FIDELITY.md). The completed native live-table plans are [historical design records](archive/README.md#completed-live-table-plans); live tables are not an outstanding VTS implementation gate.

## PT Analysis native Parquet import

Use NA-1–NA-6 and the [analytics acceptance inventory](qa/ANALYTICS_ACCEPTANCE.md) for remaining external scale and release gates. Summary/histogram imports, partition manifests, multi-chunk duplicate provenance, durable `JointStreams` failover sequences, admitted `http`/`grpc`/`websocket` byte queries, source-bound decimal precision consent, frozen `PanelInputs` v2 (`query_version: 5`), mint-time `_analysis/source.v1` writer ownership, and [macOS native reference fault trials](qa/reviews/roadmap-na-d1-local-macos-2026-10-28/README.md) are implemented; distinguish their bounded supported contracts from broader analytical completeness.

Outstanding qualification includes the reproducible **1 GiB compressed-input** reference workload on the specified 4 vCPU/16 GiB host, fresh native packaged install/query/cancel/uninstall checks across all four release packaging lanes, deployed multi-replica controller HA takeover, and live remote artifact-backend lifecycle. The **10 GB benchmark is waived, not passed**; that waiver does not waive the 1 GiB gate.

Current operation: [native analytics](NATIVE_ANALYTICS.md), [imported identities](NATIVE_ANALYTICS.md#imported-measurement-identities), and [baseline comparison](ROOT_CAUSE_ANALYSIS.md#baseline-comparison).

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

Current workflows: [browser support](PROTOCOL_SUPPORT.md#browser-execution-and-mobile-emulation), [browser evidence architecture](BROWSER_EVIDENCE_ARCHITECTURE.md), [document evidence](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#document-and-navigation-evidence), [HAR ownership](BROWSER_EVIDENCE_ARCHITECTURE.md), and [browser intelligence](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md). Real TruClient/CloudTest export fixtures and actual device/session qualification must not be inferred from synthetic fixtures.

Browser intelligence policy editing supports pinned [per-metric baseline floors](archive/qa-reviews/BROWSER_SLICES_2026-10.md#roadmap-browser-metric-floors-2026-10-21), explicit run policy revisions, 30s provisional live intelligence, frozen compatible-run selection, and frozen `PanelInputs` v2 (`query_version: 5`); external real-customer and multi-platform cohort qualification remains pending.

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
