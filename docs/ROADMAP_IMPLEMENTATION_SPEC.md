# Remaining roadmap implementation specification

Status: Proposed implementation plan. Baseline: `7c5df8acabfba296019010f9f32df19f96c68d4c` on `main`, reviewed 2026-10-19. This document schedules remaining implementation and qualification; it does not claim delivery or authorize live infrastructure changes.

## Purpose and scope

Finish every remaining [roadmap](ROADMAP.md) track except **Messaging** and **CloudTest outcome parity**. This is a delta against the current implementation, not a replacement for the technical contracts or a replay of the October 2 backlog. Existing requirement IDs remain the parent acceptance contract where assigned. Non-Messaging protocol acceptance uses the reviewed PROTO-B measurement matrix under the protocol diagnostics/support contracts; recovery uses the node-failure readiness ledger, storage qualification plan and current continuity runbook. The work-package IDs below are planning identifiers, not new API capabilities or acceptance-record kinds.

| Included track | Parent requirements and authoritative detail |
| --- | --- |
| Runtime telemetry SLA | TEL-1–TEL-4; [telemetry acceptance](qa/TELEMETRY_ACCEPTANCE.md), [policy receipts](guides/TELEMETRY_POLICY_RECEIPTS.md) |
| Protocol telemetry parity, excluding messaging providers | [Protocol diagnostics](PROTOCOL_DIAGNOSTICS.md), [protocol support](PROTOCOL_SUPPORT.md); Java HTTP, HTTP, WebSocket and gRPC measurement contracts |
| Script authoring consolidation | AUTH-1–AUTH-3; [lifecycle parity](qa/LIFECYCLE_PARITY_MATRIX.md), [adapter retirement](qa/ADAPTER_RETIREMENT.md) |
| PT Analysis native Parquet import | NA-1–NA-6; [native analytics](NATIVE_ANALYTICS.md), [analytics acceptance](qa/ANALYTICS_ACCEPTANCE.md) |
| Browser and mobile web | BE-1–BE-4 and BI-1–BI-4; [browser acceptance](qa/BROWSER_ACCEPTANCE.md), [intelligence acceptance](qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md) |
| Recovery and storage | [Node-failure readiness ledger](NODE_FAILURE_READINESS_PLAN.md), [storage recovery](NODE_STORAGE_RECOVERY.md), [HA operations](ENTERPRISE_PLATFORM_RUNBOOK.md#operating-run-continuity) |

Excluded work is EH-1–EH-3, MSG-1–MSG-6, broker acknowledgements/settlement/dead-letter discovery, native JMS/SMF/MQ transports, messaging-provider certification, and every CloudTest-specific parity, fixture, migration, media or provider-expansion gate. Shared components may benefit those tracks, but this program neither implements nor closes their requirements. Existing messaging behavior must remain compatible when a shared component changes.

Browser scope includes Chromium/WebKit and supported mobile-web emulation. Native mobile automation remains a non-goal. TruClient browser-import fidelity is included only for the currently claimed supported subset and only with real sanitized export fixtures; CloudTest import qualification is excluded. No new compute provider, arbitrary SQL/formulas, Java deserialization facility or external LLM integration is introduced.

## Baseline reconciliation

Use these corrections before assigning work. Older delivery paragraphs remain evidence of their dated scope, not the current implementation inventory.

| Area | Already implemented; preserve and extend | Remaining distinction |
| --- | --- | --- |
| Telemetry | Immutable receipts, source epochs, coverage, stage distributions, verdicts, exports and comparison re-evaluation | The [October 18 integrated local run](qa/reviews/peer-live-verification-2026-10-18/README.md#validation-scope) passed the 60-second sample-age gate. It does not establish capture-to-visible-render, separate-host reference hardware, collector/native-analysis load, overload or zero loss. |
| Protocol diagnostics | gRPC client-call and WebSocket summaries, rates/codec work and advisory statistical/dependency findings | Complete measured semantics and qualification; do not recreate all diagnostics from older roadmap prose. Java HTTP currently handles opaque codec bytes, not a measured Java object graph. |
| Authoring | Script/scenario revision projections, conditioned writes, editor conflict recovery, ordinary-create receipts, shared session/assembly owners and named retired adapters | Wire real client intent keys, close remaining lifecycle edges and contextual validation races, and prove property-level parity before further removal. |
| Analytics | Contained native worker, resumable imports, identities, partition manifests, typed operations, joint evidence and saved revisions | Remaining data semantics, lifecycle/resource failure cases, complete workflows and four-platform/reference-load qualification. Native worker isolation is not a new subsystem to build. |
| Browser | Document evidence/finality, protected native HAR ownership, transfer/receipts and exports, findings, interactions/journeys and report surfaces | Close named identity/selection/coverage gaps and qualify real managed runs and declared engine/platform cohorts. Do not rebuild the entire HAR path. |
| Recovery | Controller-only adoption, reservations/standbys, evacuation guards, scoped post-commit replacement adoption, process-fence cleanup, archive recovery and partial-result notices | Earlier interruption phases, missing replacement, bounded same-leader cleanup and deployed multi-host/node/zone qualification remain distinct gates. |

The [feature completion specification](FEATURE_COMPLETION_SPEC.md) retains parent requirements and historical deviations. Where its old status prose disagrees with the baseline above, the current code and linked acceptance/review records determine whether a package needs implementation, regression protection or external qualification. A passed narrow fixture never closes a broader gate.

## Shared implementation contract

1. Extend the existing owners: pure measurement semantics in `pkg/analysis` and `pkg/rca`; protocol capture in `pkg/protocol`; durable intent/CAS in `pkg/storage`; orchestration/publication in `pkg/api` and `pkg/distributed`; shared dashboard authoring and report models. Do not add a parallel scheduler, metadata database, artifact store or universal authoring endpoint.
2. Preserve project/run incarnation, execution/source generation, immutable script and policy revisions, exact input identity, source/receipt time, window, units, semantic kind, population, quality and provenance. Old archives stay readable. Absent, unsupported, censored, stale, reset, truncated and resource-limited evidence never becomes a measured zero or a healthy verdict.
3. Use monotonic time inside a process and qualified mappings across clocks. Preserve uncertainty and invalidation on clock steps. Merge duration sketches or compatible legacy distributions, never percentiles. An uncertain threshold crossing is unscored; a positive lower bound must remain visible without becoming an exact total.
4. Additive capabilities must be negotiated at validation, placement and staging. An explicitly requested unsupported combination fails with a typed reason before target traffic. New JSON/DSL/API fields belong to the existing catalog/schema owners; regenerate generated artifacts only when those contracts change.
5. Durable publication checks project/input deletion, object incarnation, revision and ownership generation after artifacts are durable. Ambiguous outcomes retain retry evidence; cancellation revokes publication. Late writers enter the existing cleanup path. Schema changes include SQLite/PostgreSQL migrations, indexes, retention, downgrade/read behavior and restart tests in the same package.
6. Keep current authorization, secret/egress boundaries and viewer/editor/admin distinctions. Recheck authorization at read, export and publication. Reports, HAR and diagnostic samples use the shared redaction boundaries. Analytical and evidence requests cannot select arbitrary SQL, filesystem paths, code or infrastructure cleanup; existing governed execution protocols retain their own contracts.
7. Every bounded queue, result set, artifact and job names its population/limit and exposes rejected or truncated work. Reuse current quotas; any changed default requires measured resource evidence and migration notes. Feature flags may stop new work without making existing evidence unreadable.

## Delivery order

| Stage | Work packages | Dependency and exit |
| --- | --- | --- |
| 0 — Reconcile and lock compatibility | All track owners | Confirm baseline, exact open cases, old/new fixtures, required environments and supported cohorts. Record newly discovered gaps against a work package, not a copied historical backlog. |
| 1 — Correctness foundations | TEL-A, PROTO-A, AUTH-A/B, NA-A/B, BE-A, REC-A/B | Durable identities, receipts, contextual fencing, measurement semantics and recovery safety established with failure injection. These tracks can proceed independently where their seams do not overlap. |
| 2 — Complete workflows | TEL-B, AUTH-C, NA-C, BE-B, BI-A/B, REC-C | API, dashboard, CLI, archive/report and recovery behavior agree on the same contracts. BI consumes qualified BE identities and measurements; joint-query consumers consume NA semantics. |
| 3 — Measured qualification | TEL-C, PROTO-B, NA-D, BE-C, BI-C, REC-D | Run declared hardware/provider/storage cohorts, retain failures and independent counters, and satisfy the track-specific gates below. A blocked environment leaves only its gate pending. |
| 4 — Integrated release | All included tracks | Run the integrated non-messaging scenario, review/fix to zero actionable findings, and promote only the exact qualified scope. |

Do not assign calendar dates until the reference hosts, native runners, sanitized browser-import fixtures and disposable recovery environment are available. Telemetry and analytics share the declared resource testbed; recovery qualification needs genuinely separate failure domains. These are scheduling dependencies, not reasons to weaken acceptance.

## Runtime telemetry SLA

**Owners:** `pkg/api/telemetry_sources.go`, `telemetry_coverage.go`, `telemetry_stages.go`, `telemetry_client_render.go`, `clock_qualification.go`, persistence/distributed ingestion, storage history, `dashboard/src/components/clientRenderReporter.ts`, comparison/report/CLI consumers. Preserve [existing fault coverage](qa/TELEMETRY_ACCEPTANCE.md); add the missing cases below.

### TEL-A — Durable windows and measured end-to-end age

The [coordinated-start clock correction](qa/reviews/roadmap-coordinated-published-clock-2026-10-19/README.md) converts heartbeat-derived published-clock deadlines into generator wall time without moving the common barrier or re-anchoring active telemetry. Broader clock/population qualification below remains open.

Persist bounded source observation windows and committed collection cursors under the existing run-incarnation/execution-generation fence. Record source epoch, collection/publication identity, exact window, policy/cadence revision, available counters and classification. Repeated or overlapping windows cannot add coverage or totals twice. Receipt-time windows and sampled history cannot reconstruct exact lost source windows after takeover. Preserve legacy recovery as unknown/lower-bound when its source ledger is absent.

The [durable controller cursor slice](qa/reviews/roadmap-durable-cursors-2026-10-19/README.md) adds lease/incarnation-fenced atomic checkpoints, recovery of counted effects and bounded source-window provenance, and conservative legacy fallback. Receipt coverage remains v3 and source-window classification remains unknown. Qualified source-clock coverage, in-run policy segmentation and the end-to-end/reference gates below remain open.

The [monotonic browser timing slice](qa/reviews/roadmap-monotonic-browser-timing-2026-10-19/README.md) fixes local receive-to-callback timing and visibility/session fencing while preserving the advisory report contract. It does not provide publication correlation, a complete eligible population or proof of visible presentation.

Carry a bounded publication correlation identity from source collection through controller publication and browser receipt to its presentation signal. Define eligible source/cohort output publications before render sampling/filtering, with unique collection/publication identities and one declared foreground reference client. Account for every candidate as rendered, superseded/coalesced, dropped or unqualified; delivery omissions are separate from accepted-record loss. Record oldest and newest included observation timestamps and declare the measured population: **rendered source/cohort publications**, not every individual action and not an average of stage measurements. For release qualification, use oldest-included-observation → first-visible-paint age; keep newest-observation age separately so a change in batching cannot disguise older data. Exclude replayed/lifecycle-only envelopes from new measurement counts. Retain capture/receive/publish/render uncertainty and the denominator of eligible, measured and excluded publications.

Use authenticated generator→controller→browser clock mappings, with propagated uncertainty and at most one timestamp exchange per 30 seconds per source/client, browser monotonic receive-to-presentation intervals and current report quotas. The existing animation-frame/timer callback measures a post-commit paint opportunity, not proof of a displayed frame: retain it as advisory unless a qualified presentation signal/harness proves visible presentation. A literal capture-to-visible-render release claim requires that proof, linked to the publication identity. Reject foreign-run, expired, duplicate, stale-generation and malformed reports. Hidden/disconnected viewers and unqualified clocks have explicit reasons; absent viewers never fail an unattended run. Add a real end-to-end distribution including controller-publication → browser-receipt. Never derive it by adding P99 values or maxima. Apply the existing exclusion-aware quantile bounds to the entire eligible population: an upper P99 bound must meet the target even accounting for unmeasured candidates. Missing/superseded observations cannot be discarded to pass a sparse rendered sample; if the bound cannot be established, qualification is inconclusive.

**Exit:** deterministic lost/duplicate/out-of-order collection tests, clock expiry/step tests and crash-before/after-write tests prove correct totals and wall-time coverage through pause, reconnect and HA adoption. Independent timestamp fixtures prove the end-to-end population, first-paint identity and exclusions. No exactness is inferred from legacy data. Preserve the [first-publication and terminal-replay regressions](../pkg/api/telemetry_collection_replay_test.go): a repeated lifecycle envelope cannot age a collection again, while a first/unseen or previously throttled collection must retain its actual delivery age. The October 18 review supersedes the older unexplained-final-publication suspicion; retain trace diagnostics for new outliers.

### TEL-B — Policy evolution and complete consumers

The [notification outbox storage slice](qa/reviews/roadmap-telemetry-notification-storage-2026-10-20/README.md) adds immutable versioned outcomes, bounded per-destination leases, legacy-claim compatibility and incarnation/deletion fences. The [atomic notification runtime slice](qa/reviews/roadmap-telemetry-notification-runtime-2026-10-20/README.md) subsequently connects the exact archive commit to bounded authorized delivery, status inspection and scrubbed envelopes. In-run policy segments and the full consumer/sanitization matrix below remain open.

Before TEL-C, resolve the observed generator stage-budget mismatch from measured collection/publication scheduling. The current collectors and default generator publisher run at one-second intervals; the reference run’s roughly half-second generator-processing age is snapshot-to-publication delay, not a configured half-second aggregation window. The default stage budget is 500 ms. Record cadence, phase delay and actual processing work, then qualify either scheduling/cadence alignment or an explicitly versioned budget revision. Do not silently widen defaults or reinterpret old receipts to obtain a pass. Stage-health budget compliance and the independent ≤2,000 ms end-to-end acceptance criterion remain separately reported; one is not automatically the other’s verdict.

Keep the admission receipt immutable. Implement explicitly requested, authorized in-run policy/cadence changes as append-only durable segments with exact effective boundary and revision. A settings edit alone does not change a running test. Retry creates one segment; failed persistence leaves the old segment active. A measurement spanning a boundary is assigned only using its actual observation times; otherwise it is unscored, never proportionally invented. Re-evaluation selects immutable segments without rewriting historical verdicts.

Extend existing comparison, CLI and report projections to expose policy identity, clock qualification, coverage denominators, exclusions, loss lower bounds and stage/end-to-end distributions. Add explicit non-default policy selection where required by TEL-3. Application and telemetry acceptance remain separate; advisory degradation does not stop load. Legacy unfrozen policy remains inconclusive.

Use the existing incident/notification persistence boundary for durable idempotent dispatch and bounded authorized evidence links. A successful claim followed by a dispatch crash must remain retryable. Outcome identity includes run incarnation, policy/assessment revision and verdict, so retries do not create duplicate incidents. Keep payloads scrubbed.

**Exit:** both dashboard auth profiles, ordinary CLI inspection and CI, baseline comparison, JSON/CSV and existing HTML/PDF/DOCX/JUnit report paths agree on the same pinned evidence. Exercise policy-change races, deletion, missing views, dispatch failure/retry and capability downgrade. Add telemetry-specific sanitization regressions with forbidden credential/payload sentinels in policy/source metadata, reasons, evidence links and retained legacy inputs. Cover report/comparison JSON/CSV/HTML/PDF/DOCX, CLI/JUnit and notification projections; assert secrets are absent while authorized identities, counts, availability and provenance survive. Generic archive-redaction coverage alone does not close this gate. Update the generated acceptance inventory from these actual tests.

### TEL-C — Reference and fault qualification

Extend the existing reference harness; keep its local regression lane. Run a separate release lane with a **4 vCPU / 16 GiB controller, two separate generator hosts, foreground browser, 2,000 accepted observations/second, one-second publications, collector traffic and concurrent admitted native DuckDB analysis**. Declare generator/browser resources, transport, collector source and all versions. Run the unchanged 60-second steady measurement window after declared warm-up, with three independent repetitions as this plan's release repeatability gate. Require TEL-B’s recorded cadence/budget decision first. Publish the full eligible/retained/excluded counts and traceable tail contributors for each distribution in every repetition; a small P99 tail is not a claim of statistical confidence. Investigate new tail anomalies without treating the already corrected terminal-replay defect as unimplemented work.

Pass the exclusion-aware qualified capture-to-visible-render P99 upper bound ≤2,000 ms for the declared foreground population, with presentation proof, and zero **unreported accepted-record loss**. Reconcile accepted request identities and collection sequences independently on live and archive paths; count accepted work aborted during shutdown explicitly instead of calling an in-flight allowance zero loss. A separate overload/fault trial must expose degraded/incomplete status and recover without controller failure. Record stage distributions, queue/byte volumes, CPU, combined relevant RSS, clock uncertainty, exclusions, coverage and cleanup. A slow/background client remains identified separately from controller health.

Retain the October 18 local pass as regression evidence. Live AWS/Azure collector qualification remains a separate environment-specific lane; a local collector fixture cannot certify it. If a required environment is absent, its acceptance stays pending.

## Non-Messaging protocol telemetry parity

**Owners:** `pkg/protocol/java_http`, `pkg/protocol/grpc`, `pkg/protocol/websocket`, shared protocol/result diagnostics, generator RPC, native evidence adapters, `pkg/api/report_protocol_diagnostics.go`, baseline findings and dashboard diagnostic models.

### PROTO-A — Finish truthful measurements and propagation

The [Java encoding admission slice](qa/reviews/roadmap-java-encoding-admission-2026-10-19/README.md) rejects unsupported encodings even for empty bodies before target traffic. Its [local protocol matrix](qa/PROTOCOL_TELEMETRY_MATRIX.md) records Java HTTP, gRPC and WebSocket fixtures and propagation checks; versioned Java representations, helper measurements and complete consumer qualification below remain open.

The [Java body-conversion prerequisite](qa/reviews/roadmap-java-body-conversion-2026-10-19/README.md) fixes double decoding of explicit base64 actions and preserves transformed-body replay provenance. New measurement contracts and qualification below remain open.

For Java HTTP, distinguish source encoded bytes, decoded serialized-representation bytes, HTTP body bytes and measured base64 conversion work. The current embedded adapter rejects `object_schema` and `request_mapping` and the unwired sidecar; preserve its supported `response_mapping`; its conversion timings are not Java serialization/deserialization measurements. Preserve old field meanings and add optional versioned values with codec, unit, availability and population. A ratio uses a declared outer interval that actually includes conversion work, not the wrapped HTTP duration alone.

Implement **serialized representation versus HTTP body** as one explicitly named byte comparison, with compression/encoding declared. It does not close the roadmap’s separate JVM object-footprint gap. Add an opt-in, versioned measurement capability using an operator-approved pinned helper and the existing Java containment/credential boundaries. The helper measures actual objects participating in an approved codec execution; it must not reconstruct arbitrary objects from captured wire bytes. Define shallow object size and any bounded reachable-graph estimate as separate measurements, with JVM/method version, shared-object counting, traversal limits and unavailable reasons; neither is retained heap size without a separately qualified method. Carry measured serialization/deserialization work only when that codec operation actually occurs. Bound IPC, memory, measurement time and cancellation, and verify it with known object/payload fixtures on each supported JVM profile. Embedded opaque-byte execution remains object-footprint-unavailable. No script-selected helper or arbitrary deserialization facility is added. The footprint gate remains open until this helper contract and its real measurements qualify; byte ratios alone cannot close it.

Keep gRPC bytes labelled uncompressed protobuf payloads, and its rates labelled client-call rates. Streaming codec overlap may exceed 100%; do not clamp it into a false wall-time percentage. Missing HTTP/2 transport, connection or server-processing phases stay unavailable until a supported hook measures them. WebSocket receive wait is not RTT; only matched authored ping/pong can measure RTT. Application pending-write counts are not transport queue depth. Do not add probe traffic to the target.

Carry each measurement's identity, cohort, denominator and availability through generator merge, live diagnostics, JSONL/Parquet, native queries, baseline comparison, reports and advisory findings. Sampled diagnostic counts cannot silently become authoritative throughput totals. Reuse the existing statistical/dependency findings path.

### PROTO-B — Controlled endpoint qualification

Publish a reviewed non-Messaging measurement matrix as this roadmap track’s acceptance inventory and closure authority under the protocol diagnostics/support contracts. It must identify each measurement and claimed protocol/runtime cohort, independent ground truth, exact tests/commands, artifact hashes and unresolved limits, linking acquisition, merge, live/archive/query, report/baseline and failure tests. Cover Java HTTP empty/invalid/truncated base64, binary preservation, compression, HTTP failures and zero denominators; gRPC unary/client-stream/server-stream/bidi, retries, locally capped streams, concurrent codec work, cancellation/deadline and partial sends; WebSocket close/reconnect, idle timeout, backpressure and matched/unmatched ping/pong.

Use independent endpoint counters and known payloads with exact library/transport versions. For the approved Java helper, add shared/cyclic graphs, traversal exhaustion, unavailable JVM instrumentation, helper death/cancellation and codec failure; verify the defined object-size method independently against fixed JVM fixtures. Verify legacy field absence, mixed peers, redaction and bounded measurement overhead. Close only the measured cohort. Broker settlement, management readers and DLQ discovery are excluded and cannot block this track.

## Script authoring consolidation

**Owners:** `scriptAuthoringSession.ts`, `useScriptAuthoringSession`, scenario assembly/checkpoint models, `useScriptApi.ts`, script/scenario handlers, `pkg/api/authoring_idempotency.go`, storage mutation receipts, import surfaces and navigation guards. Extend current owners and the [retirement ratchet](qa/ADAPTER_RETIREMENT.md); do not resurrect removed adapters.

### AUTH-A — Durable user intent and explicit receipt semantics

Give every script/scenario create and queued revise-copy a draft-owned intent key. Preserve it across lost response, retry and browser reload, bound to project/actor/purpose and resolved payload. Rotate it only for an explicit new intent or a changed payload after a definitive outcome. An expired receipt requires an explicit new action; a deleted original must not reappear. Queued revise-copy creates a new run and leaves the original execution unchanged.

The existing ordinary-create contract claims a receipt before creation and replays the object's **current** projection, with original revision metadata where available. Do not call this an atomic immutable-original response. To satisfy the stronger AUTH-1 contract, introduce a negotiated receipt version on the existing create paths: resource creation, bindings and committed receipt outcome share one storage transaction; replay returns the protected original canonical outcome/revision. The client separately reads the current authoring projection before editing a replayed object. Retain documented legacy replay semantics for old clients; unsupported peers refuse the new contract explicitly.

Reuse current receipts, 24-hour retry lifetime, deletion/expiry tombstones and existing import/approval identities. Protect credential-bearing request fingerprints and any stored canonical outcome with the project's established key-wrapping/disclosure policies; do not persist credentials in plaintext or expose them through receipt projections. Key rotation must preserve verification of retained receipts. Migrate safely around already claimed legacy receipts rather than reissuing their creates.

**Exit:** actual SQLite/PostgreSQL and both-profile browser tests cover lost response, concurrent clicks, reload, crash at each receipt/resource boundary, same-key/different-body conflict, expiry, deletion and queued copy. Exactly one object, binding set and approval session exists per successful intent. Replay cannot overwrite edits made since creation.

### AUTH-B — Contextual validation revisions

Extend the current content fingerprint and observed revision binding with bounded capability, policy, approval and secret-reference availability revisions. A changed context invalidates the corresponding validation result. Keep static findings distinct from contextual cause codes and from execution admission. Start always rechecks current authorization, approval, secrets and placement.

Preserve strong ETags, durable `If-Match`, strict project policy, foreign-resource 404 behavior and Load/Diff/Reapply. A late result cannot adopt a newer edit, project/resource switch or changed source session. Resolve pending cells/JSON once; validate, save, replay and debug use that exact canonical document and fingerprint.

**Exit:** policy revoke, capability change, secret rebinding, approval expiry, two-editor conflicts and edits during validate/save all preserve the local draft and invalidate stale qualification. Include secondary writers and summary-only save reloads; no silent overwrite or wildcard bypass.

### AUTH-C — Lifecycle edges and property-level parity

The [archive approval handoff slice](qa/reviews/roadmap-archive-approval-handoff-2026-10-19/README.md) returns a distinct durable session receipt to the legacy/plain archive review, keeps later editor changes, and retains shared source sessions while sibling lanes remain pending. Protected imports still require save-before-edit. The [scenario prerequisite handoff slice](qa/reviews/roadmap-scenario-prerequisite-2026-10-19/README.md) retains the embedded authoring workflow after source returns and later-edit saves. The complete property matrix and AUTH-A/B remain open.

Close the named remaining edges: scenario prerequisite-script saves must retain unsaved import siblings; Java approval queueing must return an explicit queued state to the review; edited-during-save recovery must not discard sibling drafts or protected source sessions. Retain Save one/all/subset, partial-failure results, configure-secret queues and the existing return destination.

Expand the lifecycle matrix from entry-point presence to every AUTH-2 property: source/ownership, warnings/provenance, protected references, context/approval gates, pending edits, dirty navigation, delayed replies, exact resolved payload and durable reload. Include script producers outside the ordinary import route, recorder conversion, components/clone and library imports. Session-backed imports must be tested through real HTTP requests, not only constructed DTOs.

Retire a seam only after all callers use its canonical owner and property parity passes. Keep versioned checkpoint/import readers, authorization/CAS adapters, legitimate run-only projections and routing state. Existing Messaging/CloudTest entry points receive compatibility regression coverage only; their excluded product requirements are not added here.

**Exit:** zero unaccounted caller/seam, no silent sibling loss, meaningful property tests in every supported lifecycle row, and the full dashboard cycle in both auth profiles for these shared workflow changes. Update the generated lifecycle inventory and retirement ratchet with the implementation.

## PT Analysis native Parquet import

**Owners:** `pkg/analysis` mapping/identity/partition contracts, `pkg/analysis/duckdb` native worker and typed queries, `pkg/api` analysis upload/reconciliation/publication and joint-evidence paths, `pkg/storage` attempts/writers/cleanup, dashboard analysis and panel models. The contained worker and RCA core already exist; extend their contracts.

### NA-A — Deterministic import semantics

The [mapping reassociation slice](qa/reviews/roadmap-mapping-association-2026-10-20/README.md) negotiates a distinct PATCH contract, refuses unsupported peers and validates the returned identity/mapping before client adoption. The minimum-controller/rolling-upgrade barrier remains mandatory; precision consent, advanced mapping authoring gates and broader qualification below remain open.

The [partition-precedence qualification](qa/reviews/roadmap-partition-precedence-2026-10-20/README.md) verifies that existing v1/v2 physical-column and explicit-name mappings ignore conflicting directory labels and reject simultaneous constant/name-column selection. It preserves both mapping versions; precision disclosure and the other semantic gates below remain open.

The [native timestamp source-offset slice](qa/reviews/roadmap-analytics-timestamp-offsets-2026-10-19/README.md) refuses inferred offsets and verifies per-row DST conversion for unambiguous/offset-qualified inputs. The [local-wall-time slice](qa/reviews/roadmap-analytics-local-wall-times-2026-10-19/README.md) rejects ambiguous/nonexistent times with row provenance; precision disclosure and other mapping requirements below remain open.

Resolve the remaining mapping ambiguities in the versioned mapping contract: partition column versus mapping constant precedence, per-file/column schema drift, DECIMAL precision loss and timestamps crossing a daylight-saving transition inside one file. Do not infer an entire file's time grid from its first window. Preview must disclose a lossy conversion and require the same explicit choice at commit; unsupported ambiguity fails with row/file provenance rather than silently changing units, precision or timezone.

Preserve source hashes and non-mutation, raw versus summary/histogram semantics, exact identities, summary windows and histogram boundaries through preview, commit, reassociation and query. Duplicate file content and legitimate overlapping rows are different: reject/deduplicate only under the declared import policy, never by coincidental timestamp/value equality. Gate summary/histogram reassociation in the client on the controller capability/version, with a documented minimum-controller and rolling-upgrade barrier; new capable servers reject unsupported contract versions. Existing older binaries can silently discard those fields and cannot be made safe by this new code alone, so the legacy risk stays explicit until the barrier is enforced. Derived operations require the negotiated identity contract and response-echo validation; omission cannot silently downgrade the mapping.

**Exit:** extend the existing independent goldens for long/wide/nested inputs, nulls, conflicting partitions, schema drift, DECIMAL beyond double precision, intra-file DST, counter resets, raw/summary/histogram populations and duplicate content. Counts, sums, distributions, rejections and provenance reconcile across preview → commit → query; old mappings stay readable. Source hashes are unchanged.

### NA-B — Bounded joint evidence across execution entry points

The [distribution and pending-queue slice](qa/reviews/roadmap-joint-bounds-2026-10-20/README.md) bounds retained copies of already-invalid distributions and refuses a publication larger than the entire pending queue before evicting earlier evidence. Joint protobuf admission additionally checks cardinality, canonical keys, distribution shapes and encoded bytes before copying, preserving explicit unavailability and independent action counters. Category, execution-entry-point and durable source-window coverage below remain open.

Carry the existing v4 joint contract through scenario, schedule, CI and plan execution plus dashboard selection; direct test creation is not complete workflow coverage. Promote only allowlisted bounded property categories. Validate category/cardinality, encoded-byte and distribution-state budgets at ingestion, before buffering or building sketches. Keep the current limits of three grouping dimensions, 20 returned series, 16 bounded filters and 512 admitted joint keys per source publication unless a separately measured change is approved.

Persist expected source identities and publication/window ownership. Recovery either reconstructs the exact joint population or exposes the lost held batches as incomplete/truncated. Never fabricate an intersection from marginal series. Rates require whole contributing windows and the required source-union coverage, with compatible denominators; partial samples cannot masquerade as exact throughput. Preserve distribution semantics when a source lacks a compatible sketch.

**Exit:** one fixture produces equivalent evidence through each execution entry point. Test delayed/missing/duplicate sources, changed generations, overflow, byte/distribution exhaustion, pause boundaries, restart and cold HA adoption. Archive and native grouped queries agree with independently counted tuples; rejected and truncated populations remain visible.

### NA-C — Complete workflows and durable lifecycle

Close the non-Messaging catalog execution gaps: give measured `bytes_sent`/`bytes_received` bounded query profiles and qualified byte rates, and expose byte/duration distributions only where actual compatible observations or sketches were retained. Interval byte totals cannot yield a per-request size distribution, transaction rate or estimated transport bytes. Add the corresponding typed operation, units, source coverage, zero/empty/reset rules, API/CLI/dashboard selection and independent sum/rate/distribution fixtures before advertising support. Unmeasured or legacy populations remain unavailable.

Extend saved analysis/panel revisions with capability-driven operation selection, UTC versus elapsed alignment, source/measurement selection, coverage policy and bounded drill-down. Pin immutable input artifacts, policy/catalog revisions and query semantics, including default selections made by the UI. Comparison → saved panel → report/CLI must replay those pins without reading a later project default. Keep explicit stale/missing input states and project authorization. Preserve the shared catalog’s unit/population, measured-zero, reset, summary/distribution and zero-denominator rules across API/CLI/dashboard; an advertised operation must have qualified retained evidence. Use the existing shared alignment/correlation implementation and its 10,000-bucket, 366-day and 121-lag-candidate bounds, with paired coverage and explicit exclusions. Complete equivalent streaming import/mapping/query/compare CLI workflows without whole-file buffering. Expand existing panel exports; do not introduce a second panel model or arbitrary SQL.

There is no general result cache to qualify today. If needed to meet the reference target, a cache must key project, immutable input hashes, normalized typed query and capability/catalog/policy versions, with bounded residency and deletion/revocation checks. Repeated-query timings alone are not evidence of a cache. Partition pruning reports selected and pruned objects/bytes separately from physical bytes scanned when the engine exposes them.

Finish durable physical-writer ownership across controller restart and remote backends. A process-local close callback or expired lease is not proof that a remote write has stopped. Use the existing attempt generation, worker proof, backend-pinned cleanup queue and publication fence. Unknown close/commit outcomes retain ownership and heavy-work reservations until authoritative completion/termination proof or the existing explicit intervention path resolves them. Cancellation/deletion cannot publish a late result or resurrect an input; never delete another generation's object namespace.

**Exit:** inject controller/worker failure before and after upload completion, normalization, artifact commit, publication and cleanup. Exercise local, S3, Azure Blob and GCS contracts, cold HA replicas, ambiguous close/commit, cancellation, deletion and late writes. A legacy artifact whose safe removal cannot be proved stays explicitly deletion-pending. Shared dashboard changes pass both auth profiles, including revision conflicts and input deletion while a view is open.

### NA-D — Reference workload and native release evidence

Qualify **1 GiB of compressed input** on a **4 vCPU / 16 GiB host with at least 32 GiB usable scratch**, plus admission for the measured output/spill requirement. Preserve the typed worker's two-thread, 2 GiB engine-memory and 8 GiB spill defaults unless an explicitly qualified profile changes them. A larger scratch filesystem is not permission to bypass per-job limits.

Extend `TestNativeAnalyticsReferenceBenchmark`: retain reproducible fixture/binary hashes, row counts, upload and normalization time; measure selected-window P95 ≤5 seconds, declared cold and repeated queries, partition pruning, combined controller/worker RSS, actual spill, cancellation and concurrent telemetry. Add disk exhaustion, worker crash, contention, cold replica, restart/HA and remote artifact lifecycle trials. Retain the existing 15-minute import deadline unless explicitly configured. Fail qualification on controller failure, unexplained data loss, breached headroom or sustained reference telemetry-SLA violation, even when the selected query is fast. Record failures and resource-limit outcomes. The existing 10 GB waiver stays **waived, not passed**, and does not waive this 1 GiB gate.

Publish separate native controller and packaged desktop evidence for `linux/amd64`, `linux/arm64`, `darwin/arm64` and `windows/amd64`: pinned binary/native-library hashes, fresh install/start/query/cancel/uninstall, no dynamic extension download, CGO-free unavailable behavior, and generator/collector builds excluding DuckDB. Supply a sufficiently sized runner for the macOS API-native suite currently omitted on the hosted lane; an unrun suite is pending, not portable correctness evidence.

**Exit:** all required native lanes and reference/lifecycle cohorts have reviewed artifacts and accepted records. Benchmark-generated pending requests are not automatically promoted to passed. NA-1–NA-6 close only after their mapped semantic, workflow, containment and qualification gates pass.

## Browser evidence, mobile web and intelligence

**Owners:** browser document/bootstrap/interaction/HAR capture under `pkg/protocol/browser`, engine finality and distributed transfer, archive readers and findings under `pkg/api`, `pkg/rca/browser_baseline.go`, versioned browser/journey policies and dashboard Browser/panel/report surfaces. Preserve the [document contract](guides/BROWSER_DOCUMENT_EVIDENCE.md), [HAR ownership contract](guides/BROWSER_HAR_WORKING_OWNERSHIP.md) and [intelligence workflows](guides/BROWSER_INTELLIGENCE_WORKFLOWS.md).

### BE-A — Stable authored identity and qualified document evidence

The [HAR finalization budget slice](qa/reviews/roadmap-har-finalization-budget-2026-10-19/README.md) enforces one 30-second close/flush/scrub/publication deadline, reports exhaustion and preserves unjoined ownership. Its local deterministic tests do not qualify native platform behavior. The [outstanding-request slice](qa/reviews/roadmap-har-inflight-2026-10-20/README.md) closes active HAR contexts on overflow beyond 256 outstanding issued requests, preserving censored evidence and physical ownership. Stable authored identity and the remaining lifecycle work below remain open.

Add versioned stable authored page/action/step identity and actual gesture dispatch time, separate from post-action wait/capture time. Join evidence by run/source generation and document lifecycle identity. Duplicate action names must remain distinct steps. Capture scroll/layout/frame provenance at the relevant observation; child-frame coordinates require an observed transform, otherwise explicitly retain an unmapped frame population. Old records remain identity-unavailable rather than receiving invented keys or dispatch times.

Preserve independent availability per metric, document finality/deduplication and standards-qualified document measurements. Do not infer INP from action duration, reset a page's lifetime for every action or count duplicate final publications as new documents. Validate the actual browser lifecycle: genuine BFCache restore, navigation, managed browser/renderer/page crashes, cancellation and late final evidence. Synthetic lifecycle events are unit fixtures, not proof of BFCache behavior.

Exercise at least 50 interaction groups including a known INP outlier, known layout shifts, missing/unsupported metrics and both engine support differences. Preserve redaction for resource URLs/headers/bodies, selectors and screenshots. Implement the [parent BE-3](FEATURE_COMPLETION_SPEC.md#be-3-native-and-synthetic-har-delivery) default of **256 concurrently in-flight requests** and its shared **30-second native close/flush/scrub finalization budget**, with explicit evidence-budget reporting. The two slices above implement the shared finalization deadline and overflow-triggered context closure for the outstanding-request bound; native platform qualification remains separate. The current [512-entry request-identity tracking bound](../pkg/protocol/browser/resource_evidence.go) is separate and does not cap actual in-flight work; the 256-frame identity limit is separate again. Dropping tracking rows alone cannot enforce capture admission. Preserve the parent’s explicit resource-failure/censoring behavior when overflow requires context closure, and retain ownership until writers are proven joined. The [size-scaled remote artifact-fetch timeout](../pkg/api/archive_browser_har.go) is a different transfer stage and remains independently bounded; it does not replace the native-finalization budget. Test finite but oversized bodies and cap/cancel races. Qualify Linux memory admission using measured processes; retain existing generator-wide RSS/HAR budgets.

**Exit:** independent fixture ground truth matches one final document measurement population, missingness and truncation. Reconcile the rendered dashboard waterfall against independent fixture clocks and native HAR, and assert per-response HTTP-cache provenance (including unavailable cases), beyond cache-disable/revalidation behavior. Capture adds no target interactions or probe traffic. Engine/driver/platform differences are explicit in capability and acceptance records.

### BE-B — HAR durability and real import/replay workflows

Complete the remaining native HAR ownership cases: real Linux crash measurement, replacement-container reclamation boundaries, mixed-version transfer capability and HA source-cache omissions. Foreign writer state remains retained until proof allows reclamation. Move replay HAR ownership/durability through the existing artifact/receipt path so a process-local cache is not its only lifetime owner. Preserve bounded scrub/ZIP generation, native-versus-synthetic disclosure and independent download authorization.

Run a real managed browser through authoring/import → replay/run → archive → resource → native HAR download. For TruClient, obtain sanitized real export fixtures for the claimed supported subset, record exporter version/options and dependencies, then verify readiness, no-clear behavior, close and pre-armed download semantics against the source. Unsupported content stays review-only with precise diagnostics. Synthetic exports cannot close migration fidelity, and CloudTest exports remain excluded.

Mobile web qualification identifies engine, emulated viewport/profile, host platform and actual device/session separately. Desktop WebKit is not an actual iPad test. Preserve the existing Appium device-observer runtime; its implementation does not establish actual-device qualification or broader native-mobile support. Any expansion in device/provider scope first needs a scoped feasibility and capability decision; this plan does not introduce broader native-app automation or an unselected device provider.

**Exit:** native HAR bytes survive qualified cancellation/crash/restart/HA paths or expose a precise unavailable state; no foreign artifact is adopted/deleted. Mixed peers refuse unsupported explicit capture requests before target traffic. Real source fixtures and managed replay evidence support only the claimed import subset.

### BE-C — Publish measured browser limits

The [benchmark units slice](qa/reviews/roadmap-browser-benchmark-units-2026-10-20/README.md) makes the existing decimal memory target explicit in bytes and adds versioned raw-byte benchmark artifacts with missing/partial-sample disclosure. Historical MiB-labeled artifacts remain unchanged; reference-host and per-VUser qualification below remain open.

Use `BenchmarkBrowserInitWithDeviceProfile`, `BenchmarkBrowserTouchOrClickActionLatency`, `BenchmarkBrowserRSSPerChromiumDeviceProfile` and `TestMobileBrowserBenchmarkProfilesResolve`, with `TestBrowserBenchmark` for browser-runtime/failure evidence and the existing [benchmark procedure](PERFORMANCE_BENCHMARKS.md#browser-benchmark-artifacts). Retain the **five raw benchmark repetitions per required profile** specified by `RequiredRawRunCount: 5` and the `-count 5` command in the [code-owned benchmark acceptance manifest](../pkg/api/browser_benchmark_evidence.go). This repetition requirement is independent of the five-concurrent-VUser density ceiling. Record generator image/commit, host shape, driver/browser versions, cold launch, CPU, action latency or touch-over-click-baseline overhead as defined per profile, process-attributed RSS, throttling, cleanup and failure outcomes. Browser-runtime absolute navigation and combined descendant-tree peak RSS cannot substitute for these action/per-VUser measurements. On the declared **4 vCPU / 8 GB** reference generator, evaluate the published design targets: launch P50 <3 seconds, Chromium action/touch overhead <10 ms and Chromium RSS <250 MB per VUser. The current MB labels and binary-unit calculations must be reconciled explicitly in the owning contract and artifact schema before qualification, retaining raw byte values; do not silently relax a published threshold by converting it to MiB. These remain targets until the exact cohort passes; where process attribution is unavailable, report that rather than relabel host RSS as browser RSS.

Retain recommended **1–2 browser VUsers and maximum 5 per generator**. A higher-density override requires attached benchmark evidence and a reviewed runtime profile; internal capacity ceilings never become advertised density. Qualify Chromium and WebKit/mobile-emulation cohorts independently, with unsupported metrics left unavailable.

**Exit:** repeatable artifacts, failure/cleanup bounds and accepted cohort records establish the supported density and performance scope. A local host run cannot certify another engine, image or platform.

### BI-A — Qualified baseline selection and findings

The [baseline qualification guard slice](qa/reviews/roadmap-browser-baseline-qualification-2026-10-19/README.md) prevents per-metric floors from lowering the effective timing/CLS thresholds and leaves cohorts with known document loss unscored. The v1 estimator is unchanged; configured policy editing, compatible baseline selection and real-run qualification below remain open.

Extend the existing RCA browser baseline with stable authored identity and actual `latest_compatible` selection. Qualify immutable script, engine/profile/runtime cohort, policy revision, comparable load/window, clocks and source coverage before selection. Exclude pause/shaping/loss/insufficient-window mismatches and show why; do not select merely the newest run. Add configured per-metric effect floors and immutable links from each threshold/baseline finding to its selected baseline and pinned policy.

Keep the current [browser_baseline.v1 estimator](../pkg/rca/browser_baseline.go) explicit: the run-side statistic is the median of its bucket p75 values, compared with the qualified baseline bucket values at the same load. This gives buckets equal weight; it is neither a pooled per-document percentile nor an independent per-document statistical test. Qualify unequal document counts per bucket, cadence changes, repeated document publications, sparse/short windows and the configured exclusions. Preserve existing versioned results; a future per-document inference method needs its own algorithm version and qualification rather than relabelling v1.

Retain at least 30 valid comparable baseline buckets and absolute z-score ≥3 for advisory three-sigma/effect-floor evaluation, with explicit insufficient evidence and zero-variance handling. Configured floors may only raise the existing timing default max(50 ms, 10% of baseline mean) and CLS default 0.02; other metrics require a positive catalog/configured floor. Baseline status in browser findings must reflect the evaluated result rather than remain an unconditional `not_evaluated` placeholder. Keep baseline anomalies distinct from absolute thresholds and do not treat missing evidence as a good score.

**Exit:** two real controlled browser runs, one with a seeded regression, reproduce the expected finding and evidence chain. Pauses, lost sources, changed scripts, incompatible cohorts and missing metrics refuse or exclude the comparison for the expected reason. Unit-seeded series remain useful but do not replace this gate. Release BI scoring only after the corresponding BE metric cohort qualifies.

### BI-B — Interaction maps and authored journey outcomes

Use BE-A's step identity and dispatch time for repeat/retry windows. Bind declared assertion, dialog, download, navigation or state outcomes to their authored observation deadlines. A delayed post-action read must not change the repeat window. Keep intended retries separate; unchanged DOM, navigation away or cancellation alone cannot prove a dead interaction.

Keep compatible scroll/layout/frame cohorts separate in maps. Observed child-frame transforms can enable a frame map; absent transforms must be counted as excluded. Unknown engine/profile records cannot merge incompatible known cohorts. Preserve the [interaction-analysis bounds](../pkg/api/browser_interaction_patterns.go): 100,000 interactions retained for pattern detection, 65,536 context generations for engine/profile attribution, 200 output patterns with at most 16 listed evidence interaction IDs each, 64 maps of 64×64 cells and 64 keyboard targets per map in output. Streaming archive coverage and heatmaps can include records beyond the retained pattern sample; report these populations separately, and preserve the actual pattern count when its listed evidence IDs are capped. Test each boundary and expose truncation/unknown attribution; unknown contexts cannot qualify a cohort or merge incompatible known cohorts. These are distinct input, attribution and output limits, not interchangeable population counts. Screenshot overlays remain optional and require authorized matching, safely redacted capture.

Preserve scripted/consented-recorded/unknown provenance. Synthetic repeats do not prove human frustration. Journey attempts count one explicit terminal outcome, with failed/aborted/skipped/censored and zero-denominator semantics; missing steps cannot become success. Weighted test-journey risk must not be labelled revenue or conversion loss without an explicit external model.

**Exit:** fixtures distinguish duplicate step names, intended retries, delayed waits, outcome deadlines, frame/scroll/layout mismatches, unknown cohorts and censored attempts. Live/archive/export representations preserve the same eligible populations and exclusions.

### BI-C — Complete policy, panel and evidence workflows

Implement a release/CI dependency gate before promoting BI support: the claimed binary, engine/profile/platform and metric/algorithm cohort must resolve to reviewed, accepted BE evidence for that same scope. Bind evidence digests in the promotion result. Missing, failed, revoked or stale/mismatched binary/capability evidence blocks promotion; suite execution order or a pending acceptance request is insufficient. Test each refusal and a valid matching dependency, including a change between checking evidence and promotion. Preserve authorized historical results and explicitly labelled preview behavior; the gate must not rewrite old findings.

Allow an editor to select a specific immutable browser/journey policy revision for a run while retaining the current default behavior. Use existing optimistic policy writes and authorization. Expose cohort digests and stable page/step identities, support the necessary bounded multi-tuple native panels, and implement comparison → saved panel without losing input/policy/cohort pins.

Finalized findings remain authoritative. Complete BI-4’s live finding, interaction and journey views as explicitly provisional results, bounded to retained evidence, never a final verdict, and replaced by the finalized result with visible coverage/revision. Finalized-only views cannot close BI-4; if live evidence is not qualified, that requirement remains pending. Do not imply the existing live comparison is already a live findings engine. Reports and CLI must preserve these distinctions and expose the same drill-down/policy provenance.

Prove the full chain with real managed runs: live sample → finalized finding → document → resource → authorized native HAR/ZIP, two-run baseline comparison, saved panel and supported report exports. Drive both browser and journey policy editors in both dashboard auth profiles. Test viewer inspection/download, denied writes, cross-project denial, policy conflicts/deletion and stale panel inputs. Keep fixture-backed UI tests, but label their limits.

**Exit:** BE-qualified real runs establish BI-1–BI-4 behavior for each supported cohort; all retained artifacts are linked from reviewed acceptance records. A missing engine/device/import environment leaves only its declared cohort pending.

## Recovery and storage

**Owners:** `pkg/api/run_continuity.go`, `run_adoption.go`, `run_evacuation_recovery.go`, distributed resilience and process fencing, `pkg/storage` recovery journals/capacity reservations, generator execution epochs and archive recovery. The [node-failure ledger](NODE_FAILURE_READINESS_PLAN.md) remains the detailed source of open cases. Reuse current KEDA demand, standby reservations, archive retry/backlog and partial-result notices. Preserve strict-fail defaults; missing-shard continuation requires explicit continue/tolerate-generator-loss opt-in and the current stateless-HTTP workload eligibility. Keep current placement, duration, data/parameter and iteration restrictions. This spec does not expand automatic recovery to browser, gRPC, WebSocket, Java or stateful workloads.

### REC-A — Recover every evacuation interruption boundary

Extend the durable evacuation journal through conceptual phases `prepared → staged → source_fenced → replacement_committed → completed`, mapped onto existing states rather than creating a second coordinator. Persist exact source/replacement identities, execution/owner generations, capacity and topology observations, script/capability pins, pause state and remaining active duration. Make each side effect idempotent under conditional ownership checks.

Current successor adoption covers a narrow committed-replacement case. Add earlier interruption phases and a missing replacement for the eligible HTTP workload. Prepared/staged/source-fenced recovery remains fail-closed until its new replay/fence path is qualified; old peers cannot silently enter the new recovery states. A successor first proves what was committed and which execution can still run; it cannot infer a safe restart from an absent heartbeat. Never run old and replacement generations concurrently, replay already completed initialization, resurrect a stopped run or extend its configured active duration. Missing safe capacity remains an explicit admission/recovery state.

**Exit:** inject controller failure at every side-effect/journal boundary, a second leader change, worker/node partition and late old-owner return. Exercise stop, pause, deadline expiry, capability mismatch and no spare capacity at each resumable phase. Independent target counters and execution-generation records prove no duplicate load or unsafe adoption. Test rolling-version capability refusal and rollback/read behavior for every new journal state.

### REC-B — Bound same-leader terminal cleanup without weakening fences

The [same-leader intervention slice](qa/reviews/roadmap-same-leader-intervention-2026-10-19/README.md) bounds stop retries for the current canceled runtime with a durable evacuation journal, retains STOPPING quarantine on exhaustion and accepts the existing explicit administrator attestation. Unjournaled and stale-runtime cleanup contracts remain unchanged; deployed failure-domain qualification stays pending.

Handle the currently stranded same-leader path when a replacement worker restarts, rejects the old generation and leaves the run stopping. Reuse all-participant process fences and durable termination proof; preserve the already fixed successor behavior that retains the failed state/error. Add bounded retries and extend/reuse the existing administrator-attestation/intervention path where the current contracts require one.

A lease timeout, Kubernetes NotReady status or pod-name reuse is not death proof. Move to terminal failed/cleaned only when the required participant proof is durable; otherwise expose quarantined resources and the exact unresolved owner/action. Do not free reusable capacity, delete artifacts or claim successful cleanup solely to meet an elapsed-time target.

**Exit:** the same leader and successive leaders converge after valid proofs, retain the original cause, refuse stale callbacks and never release an uncertain writer/executor. Unprovable termination has a visible bounded retry/intervention state rather than an invisible endless spinner.

### REC-C — Repeatable integrated local fault qualification

Extend existing fault tooling with at least three clean, fresh repetitions per claimed scenario: controller-only failover, standby-worker loss, active controller plus worker loss, no spare capacity, partition, second leader change and PostgreSQL/Redis/archive interruption. Use an independently authenticated target traffic counter; record exact workload, topology, KEDA/ordinal floor, quota freshness, reservation decisions and all injected boundaries.

Measure API outage, leadership acquisition, generator discovery, resumed traffic, evidence attachment and archive recovery separately. Preserve duration/pause semantics, failure reasons and partial-archive notices. The ledger's proposed 120-second local recovery target cannot shorten a safety fence; publish a failure or revise the target through an explicit scope decision. Local success does not certify node/zone/storage failure domains.

**Exit:** no uncontrolled duplicate traffic, stale generation adoption, reservation leak or false complete archive; each repeat reconciles independent traffic and retained evidence. Failures remain linked alongside successful reruns.

### REC-D — Deployed storage and failure-domain qualification

Run the [storage recovery plan](NODE_STORAGE_RECOVERY.md) on genuinely separate hosts with the actual CSI driver, StorageClass/reclaim policy, topology, PostgreSQL/Redis failover and shared asset/staging/object-backend configuration declared. Cover permanent node termination, detach/attach failure, zone loss, old writer return, missing raw evidence and interrupted rollback/journal recovery. Qualify every backend/topology advertised for this scope, explicitly excluding untested combinations.

Never reset an execution epoch, force an unfenced worker back into service or equate an attached persistent volume with restored goroutines. Separate control-plane recovery, continuation/restart eligibility, traffic interruption, accepted-record loss and archive RPO/RTO. Local filesystem fixtures cannot establish deployed zero-loss storage behavior.

**Exit:** reviewed artifacts show the supported recovery action, proof boundaries, measured RTO/RPO, remaining incomplete evidence and operator runbook for each failure domain. Any required environment not exercised stays pending; product status must not promote local fault trials to HA certification.

## Integrated acceptance and release

Environment status, 2026-10-19: separate reference hosts, Linux/Windows native runners, sanitized real TruClient exports and a disposable multi-host recovery environment are unavailable. Their qualification gates remain pending; local implementation and validation do not waive them.

Use one non-Messaging integration scenario to connect the seams: author/import and revise a script through the shared session; execute HTTP plus qualified browser/gRPC/WebSocket cohorts; retain policy/source identities; run the admitted native reference query while telemetry is collected; compare a seeded browser regression; save/export a pinned panel and report; then exercise recovery with a separate explicitly eligible stateless-HTTP workload in its disposable fault environment. The mixed protocol/browser workload does not qualify missing-shard continuation. Java HTTP codec qualification has its own known-payload fixture. Do not force protocol-specific measurements into a fabricated common denominator.

| Gate | Required evidence and decision |
| --- | --- |
| Contract compatibility | Old archive/read fixtures, old/new peers, generated schema freshness, SQLite/PostgreSQL migrations and cross-project/role denial. No unsupported request reaches target traffic. |
| Correctness and recovery | Independent ground truth, deterministic failures at durable boundaries, exact or explicitly incomplete populations, no late publication or unsafe resource release. |
| Dashboard and in-app help | Typecheck, lint, relevant unit tests and affected Playwright workflows in both auth profiles; full dashboard cycle for shared workflows. Real managed browser cases supplement fixture tests. Help catalog routes, heading anchors, source ownership and internal links resolve. |
| Backend regression | Affected package tests plus the required full Go suite for cross-package/shared changes; native/tagged/platform lanes remain separate explicit results. Integrate current `main` before builds/full API runs. Finish dashboard assets before embedding Go builds. |
| Performance and deployment | TEL-C, NA-D, BE-C and REC-D use their own declared hosts, populations and environments. No extrapolation across providers, engines, density or storage topology. |
| Release review | Review each change and fix every finding, including low severity, then repeat until clean. Publish only accepted scope; pending, failed, waived and unsupported remain distinct. |

Every evidence bundle records the applicable parent requirement ID, or its named roadmap track and acceptance-authority document where no parent ID exists, together with its work package, tested commit and binary/image/fixture hashes, environment and versions, capabilities/flags, exact commands, workload/warm-up/measurement windows, independent expected result, measured outputs, failures/exclusions and cleanup. Use current acceptance-record schemas and inventories; planning IDs are not new record kinds. Any new machine-readable acceptance registration is an explicit owning-contract change, not implied by a package name. Preserve dated raw artifacts and link replacement results rather than overwriting them.

Update the owning user guide when behavior changes, generated acceptance inventories from their tests, and the roadmap only after the declared gates are accepted. Keep this document as the implementation delta; do not copy contracts or accumulate execution logs here. Close browser sessions and disposable build caches after their dependent verification work is complete, preserving source, dependencies, outputs and application data.

## Requirement traceability and handoff

| Parent requirement or roadmap track | Implementation packages | Completion dependency |
| --- | --- | --- |
| TEL-1 / TEL-2 | TEL-A, TEL-B | Durable windows, qualified correlation, exactness/exclusions and policy segments |
| TEL-3 / TEL-4 | TEL-B, TEL-C | Complete consumers and reference/fault evidence |
| Non-Messaging protocol telemetry | PROTO-A, PROTO-B | Measured semantics, archive/report propagation and independent endpoint qualification |
| AUTH-1 | AUTH-A, AUTH-B | Versioned intent receipts, conditioned writes and context-bound validation |
| AUTH-2 / AUTH-3 | AUTH-C | Property-level lifecycle parity before remaining adapter retirement |
| NA-1 | NA-A, NA-C | Catalog and typed operations remain qualified across import/query/workflow changes |
| NA-2 | NA-A | Complete deterministic mapping/identity/partition semantics |
| NA-3 | NA-B, NA-C | Bounded joints, shared alignment and execution-entry-point parity |
| NA-4 | NA-C | Pinned workflows and durable cancellation/deletion/backend lifecycle |
| NA-5 | NA-C, NA-D | Resource containment, physical ownership and four native release lanes |
| NA-6 | NA-D, NA-A–C fixtures | Independent golden correctness and measured 1 GiB reference evidence |
| BE-1 / BE-2 | BE-A | Stable identities and engine-qualified document measurements |
| BE-3 | BE-B | Durable HAR ownership/import/replay |
| BE-4 | BE-A–C | Document/waterfall/cache acceptance and measured supported browser cohorts |
| BI-1 / BI-2 | BI-A | Qualified baseline selection, effect floors and immutable findings |
| BI-3 | BI-B | Dispatch/step identity from BE-A; truthful outcomes/maps/journeys |
| BI-4 | BI-C | Enforced BE evidence dependency plus policy, comparison, panel/report and real browser acceptance |
| Recovery/storage roadmap row | REC-A–D | Safe journal/cleanup convergence, repeated local faults and deployed failure domains |

Before starting each package, its owner records the exact remaining cases and fixtures against the baseline. Resolve new capability/receipt version names, mapping precedence and backend-specific termination proof in the owning contract before implementation; retain the behavior and acceptance constraints above. Release owners identify reference hosts, native runners, real TruClient fixtures and disposable storage/fault environments in stage 0. Missing access or evidence is a named dependency, never an implicit waiver.
