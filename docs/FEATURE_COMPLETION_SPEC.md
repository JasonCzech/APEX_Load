# Feature completion specification

Status: Roadmap — approved implementation program, authorized and evaluated 2026-10-02 against published commit `2314ab698c805f5b75ac8b984143a6389deef03b`.

The evaluation below is the **2026-10-02 baseline**, not a current gap inventory. Requirements remain here; dated implementation contracts, delivery narratives and their qualification limits are preserved in the [delivery snapshot](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md). Retained deviation lists and the linked delivery records must be read together; moving a record does not close its gates. Use [ROADMAP.md](ROADMAP.md) for remaining priorities.

## 1. Objective and completion contract

Complete the remaining Event Hubs, messaging, browser evidence, browser intelligence, telemetry reporting, native analytics and authoring cleanup tracks. This is a specification, not a claim that the features or acceptance tests below have shipped.

“In full” means that each supported operation has executable runtime semantics, capability admission, project authorization, dashboard and CLI workflows, durable evidence, archive/report/comparison representation, documentation and passing acceptance tests. A mock, schema field, CI job definition or clean source review alone cannot close a provider or performance gate. Unsupported combinations remain explicit. Full support is bounded by the provider/platform matrix and resource limits defined here; it does not mean arbitrary protocols, SQL, code, labels or unlimited evidence.

Reuse the existing engine, collectors, DSL catalog, protobuf, archive manifests, storage interfaces, dashboard definitions, baseline comparison, incident delivery and authoring workflows. Keep the seven areas independently releasable. IBM MQ remains a very low priority final release and does not delay the other tracks. Its requirements remain part of this specification. The operator's 10 GB import benchmark waiver remains in force; smaller measured acceptance does not certify 10 GB operation.

Streaming media, native mobile/desktop automation, new compute providers, arbitrary Java ObjectMessage deserialization, arbitrary SQL/formulas and unapproved external LLMs are separate scope. CloudTest and TruClient source fidelity requires fixtures before exact migration support can be claimed; authoring cleanup preserves those gates.

## 2. Evaluation of the current implementation

| Area | Verified foundation to retain | Actual completion gap |
| --- | --- | --- |
| Event Hubs | Native producer/observer, AAD processor with fenced Blob ownership/checkpoints, fixed-partition epoch-exclusive receiver, batching and scoped evidence | Processor tail/checkpoint lag, visible process-capacity admission, lifecycle diagnostics and real Azure rebalance/recovery/takeover qualification |
| Messaging | Kafka real seek and consumer-offset transactions; provider AMQP and Service Bus execution; bounded scoped moments, observed-zero availability and goals | Complete provider semantics, management observations, mergeable distributions, genuine generic JMS, Solace SMF, native MQ and broker-backed certification |
| Browser evidence | Network entries, Chromium enrichment, top-50 inspection, failure classification, screenshots and local native runtime HAR capture | Stable request/document clocks and identities; end-to-end HAR publication, replay/download linkage, full resource inspection and capability-aware fidelity |
| Browser intelligence | Core browser measurements, device breakdowns, threshold issues and health scores | Correct/versioned document metric semantics and availability before baseline scoring; qualified anomalies, observed interaction patterns, heatmaps and configured journey impact |
| Telemetry reporting | Lag stages, drop presence, health transitions, whole-run baseline evidence and report exports | Immutable policy/cadence provenance, time-weighted coverage, reset/fence-aware losses, cross-clock qualification and separately governed telemetry verdicts |
| Native analytics | Embedded DuckDB, resumable imports/mappings, saved comparisons/panels, rates, bounded dimensions and joint v3 live evidence | Preserved imported dimensions, typed derived operations, broader measured joints, resource isolation, partition pruning and verified native release/scale outcomes |
| Authoring | DSL validator, typed import envelopes, shared authoring shell, scenario reducers/payload helpers, canonical save/reopen and complete data bindings | One owner for mutable session/assembly state, client-observed revision fencing, exhaustive lifecycle parity and evidence-driven adapter retirement |

Important corrections to older planning prose:

- Event Hubs has an official emulator. Use it for its documented AMQP/Kafka scope; Entra, Capture, persistence and real service behavior still require separate evidence. [Microsoft emulator scope](https://learn.microsoft.com/en-us/azure/event-hubs/overview-emulator)
- The current Go Service Bus SDK has no transaction context/API. Atomic transactions require a different governed implementation; sequential send/settle calls are insufficient. [Go SDK changelog](https://github.com/Azure/azure-sdk-for-go/blob/main/sdk/messaging/azservicebus/CHANGELOG.md)
- Solace's Go SDK wraps its C API through CGO. Native images and platform certification are required. [Official SDK](https://github.com/SolaceProducts/solace-messaging-go-client)
- `pkg/protocol/browser/har_capture.go` already configures native runtime recording. Its local teardown path is not an end-to-end run/replay artifact contract. The roadmap's synthetic replay HAR paragraph describes a design, not a verified delivered writer/export.
- Legacy browser CLS/interaction-duration values and missing-value health scoring need qualification changes. They must not be silently relabeled as standards-compliant document metrics.
- Scenario assembly, live lag correlation, branch-local repeat control, observation rates and chart exports have shipped slices. Reconcile stale phase/ledger text from verified acceptance; do not rebuild them as missing features.

Current pins include `playwright-go v0.6100.0`, `duckdb-go/v2 v2.10506.0`, DuckDB native bindings `v0.10506.0` and `azservicebus v1.10.0`. Dependency upgrades require their own conformance evidence; they are not implied by this document.

## 3. Shared contracts

### 3.1 Evidence and clocks

Extend the existing `pkg/analysis` model and native publication contracts with optional versioned fields. Every new measurement carries project/run/dataset identity, immutable execution/script revision, source and source generation, stable entity/action/document identity, UTC source and receipt times, observation window, measurement name, semantic kind, unit, population, quality and provenance.

Distinguish observations, gauges, counters, histograms/distributions and precomputed statistics. Retain counts, sums and mergeable distributions only when actually measured. A scalar P95 cannot be merged into a fleet P95. Missing, unsupported, suppressed, stale, skewed, incomplete, invalid and resource-limited are distinct states; none means zero. Counter sums use checked arithmetic; positive lower bounds survive invalid contributors but cannot qualify exact totals.

Use monotonic clocks for durations inside one process. Cross-process/browser wall-clock deltas require a recorded offset/uncertainty assessment. Use authenticated control-plane timestamp exchanges at no more than one per 30 seconds per source/client, retain round-trip uncertainty and invalidate the mapping on clock jumps or expired calibration. These exchanges never contact the SUT. A budget decision whose uncertainty interval crosses the threshold is unscored. Unqualified negative/implausible deltas remain unavailable. Infrastructure and telemetry alignment use elapsed wall time through pauses; load/iteration schedules retain active time separately. Browser resource clocks additionally use document-relative time origins. The same identity and time window must reach live, archives, reports and native queries.

New evidence artifacts are immutable, hashed and versioned, referenced by the existing manifest. Keep SQLite/PostgreSQL authoritative for identities, revisions, policies, jobs, leases and publication state; use existing artifact backends for analytical/event files. HA replicas have independent analytical caches. Publication occurs only after objects are durable and the job's ownership/revision fence still matches. Cancellation, late writers and deletion use the existing durable cleanup queue.

### 3.2 Capabilities and support evidence

Add versioned capabilities for messaging evidence/operations, browser document/resource metrics, native joint evidence and helper/worker contracts. Reject explicitly requested unsupported features at validation, placement and staging. Old peers still execute existing supported scripts and old archives remain readable; optional fields do not manufacture capabilities.

Maintain separate records for implementation availability and acceptance: provider, transport, broker/service tier, SDK/helper version, platform, image hash, commit, test suite, time and sanitized artifact hashes. Supported means the required real-provider matrix passed. Preview means local/component correctness is established but provider evidence is incomplete. A skipped protected acceptance job remains pending; it does not become a pass.

Helpers use packaged, pinned code with typed commands and operator-approved providers/drivers. Extend existing JMS publish/consume/browse/ack/commit/rollback action contracts with provider-qualified transport/session options. A transacted session lazily establishes its transaction after execution commit; commit/rollback complete that context and the next transactional action starts a new one. New fields are catalog-owned and generated. Operations and delivery handles are scoped to run/group/VUser/helper generation, not transferable across sessions. No script-selected shell, classpath, arbitrary Java or dynamic SDK installation. Use credential-minimized IPC, project secret/identity policies and contained egress enforcement. A JVM/native process that cannot enforce the approved destination boundary is unavailable. Capability/Prepare checks inspect local prerequisites and approved control-plane state without opening broker/browser/SUT connections or polling sources; initialization begins after execution commit.

### 3.3 Authorization and compatibility

Viewers read and run bounded analyses over authorized evidence. Editors configure scenario/run settings, author/import scripts and mappings, save panels/comparisons and explicitly configure CI verdict policies. Project administrators approve monitor sources, management identities and helper trust; system administrators install platform runtime profiles. Preserve existing service-account scopes and audit conventions.

Artifact views/downloads, findings, comparisons, caches and deletion must enforce project isolation independently of possession of a resource ID. Redact credentials, URL userinfo/query secrets, headers/bodies, selectors and source metadata before publication. Protected preview/report data cannot expose raw rejected values or helper errors. Existing irreversible disclosure projections remain authoritative.

Roll out through explicit project/runtime capabilities. Disabling a feature prevents new work but retains authorized audit/read/cancel/delete operations and old definition compatibility. New SQL migrations are nullable/additive with SQLite and PostgreSQL tests. Never backfill absent measurements as measured zero. Generate DSL/OpenAPI/TypeScript references from their code owners.

## 4. Event Hubs completion

### EH-1: Explicit mode and resource contracts

Preserve observer, processor and epoch-exclusive modes as distinct contracts. Observer probes use explicitly isolated groups. Processor mode owns a bounded partition set and durable checkpoint namespace. Exclusive mode pins partition/group/JSON-safe epoch; ownership loss is terminal and cannot silently fall back to ordinary consumption.

Make the existing process limits—four processors, 32 workers and 32 partitions per processor—visible to capability/placement/reservation checks. Reserve demand atomically across overlapping runs before Start. Configuration may lower limits; increasing them requires resource evidence. Report requested versus admitted demand and release reservations on every terminal/failed staging path.

### EH-2: Progress, lag and recovery

Retain acquired/lost ownership, ownership generation, delivered position, durable checkpoint position, tail observation, checkpoint timing/retries, redelivery and incomplete scope. Measure delivered-position and checkpoint-position lag separately; enqueue age remains a time measurement. Tail queries are bounded/cached and cannot consume messages to estimate backlog.

Checkpoint only after the successful extraction/assertion boundary. Conditional ownership/checkpoint updates prevent stale or ABA owners advancing progress. An assertion failure, failed checkpoint, lost ownership or conditional-write fence, or cancelled receive has an explicit retry/terminal state. Crash before checkpoint permits redelivery: the processor guarantee is at-least-once, with no loss claim established only by accepted event-ID reconciliation. Do not advertise exactly-once processing or automatically replay/reset source state after generator loss.

Soft Pause prevents new event delivery/application work while allowing an already-started action, assertions and its fenced checkpoint to finish before pause acknowledgement. Once paused, bounded ownership reads and renewals continue while queued events remain bounded and pending. Existing SDK control-plane keepalives and run-bound infrastructure monitors retain their separate lifecycles; pause confirmation proves the application receive/delivery/tail/checkpoint boundary. Start, stop, drain, expiry, failed extraction, identity expiry, controller takeover and storage failure must preserve fencing and cleanup.

Delivery status, 2026-10-03: EH-1 process-wide processor/worker admission and traffic-free preparation are implemented. The EH-2 evidence slice adds bounded standalone progress, exact decimal-string positions and nullable delivered/checkpoint sequence backlog, opt-in tail polls (10 seconds to 5 minutes; default 10 seconds), cumulative generation counters/checkpoint distributions with latest-revision merge, and retained live/history/archive/report/API/CLI reads. Source-monotonic ownership/cursor ages qualify only within 30 seconds of per-row capture; transit and cross-host clock alignment remain unqualified. Nullable SQLite/PostgreSQL history columns also preserve existing messaging summaries without inventing older measurements. Strict processor pause now requires actual receive/tail/checkpoint quiescence and explicit generator proof, with lazy SDK receiver admission and fenced fail-closed compensation. Processor recovery and the protected EH-3 Azure matrix below remain open; local/default/tagged verification does not close provider acceptance. Current operator guidance is in the [Event Hubs workflow](EVENT_HUBS_HOWTO.md#processor-progress-and-checkpoint-evidence).

### EH-3: Acceptance

Local emulator cases verify supported produce/consume/auth-by-connection-string behavior. Protected Azure cases separately verify AAD/workload identity, Blob ownership/checkpoints, 2→3→1 worker rebalance, abrupt worker death/restart, failed assertion/checkpoint retry, stale-owner CAS/ABA, higher/equal/lower epoch competition, revocation, cancellation and tail/checkpoint-lag accuracy. Capture and Kafka-endpoint tier-dependent features get separate cases.

Keep Event Hubs Kafka transactions independent of Apache Kafka certification and require the exact supported service tier/feature state. [Microsoft Kafka transaction scope](https://learn.microsoft.com/en-us/azure/event-hubs/apache-kafka-transactions)

## 5. Messaging completion

### MSG-1: Provider capability and evidence v2

Extend `pkg/protocol/messaging_diagnostics.go`, provider adapter capabilities, engine rollups and distributed RPC rather than adding another reporting pipeline. Capabilities enumerate real settlement modes, session transactions, seek, subscriptions, browse, management observation, ownership, replay, message types and platform/resource limits.

Retain distinct measures: producer acknowledgement, consumer settlement, checkpoint duration, local admission/queue wait, total action duration, enqueue age, authored producer-to-consumer latency, delivered-position lag, committed/checkpoint lag, active/scheduled/deferred/transfer/DLQ depth, retry/throttle/lock loss and delivery/redelivery/duplicate counts. End-to-end latency requires an authored correlation/send timestamp plus qualified clocks. Client acknowledgement never means isolated broker processing.

Add mergeable distributions for percentile-bearing durations; existing count/sum/max moments are insufficient. Every reliability counter states its eligible population and observation scope. Local duplicate detection is bounded, window-scoped and explicitly sampled if applicable. Counter reset, overflow, missing peers, overlapping retries and scope omissions preserve availability flags and lower bounds.

### MSG-2: Management observations

Reuse signed collector jobs and monitor artifacts for allowlisted, paginated management packs: Kafka high-watermark/last-stable next positions and group committed-next offsets (physical replica log end remains separately unavailable unless proven); Event Hubs partition tails/checkpoints; Service Bus runtime properties; Solace SEMP depths/DMQ metadata; eventual MQ PCF depth. Scope the management identity separately from send/receive credentials. Default interval is 10 seconds, at most 256 approved entities per source, at most 16 pages and a five-second request deadline; provider rate-limit/backoff rules may require a slower declared cadence. Allow one active poll per source, at most two concurrent requests and a default eight-second whole-poll deadline. Coalesce missed ticks and record cadence gaps instead of overlapping polls. Timeout/backoff/page-budget exhaustion marks remaining inventory incomplete; late responses cannot publish into a cancelled or newer collection generation.

Record expected inventory, observation time/freshness, permissions and missing entities. Failed probes never yield zero. Discover only approved entities and relationships; do not create queues, change subscriptions, trigger replay, consume to measure depth or claim unobserved provider-wide maxima. Credentials and destination policy apply to every request and redirect.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#msg-2-management-observations).

### MSG-3: Kafka semantics and certification

Certify real earliest/latest/explicit seek only for owned, fully polled partitions. Revocation invalidates in-flight resolution; buffered delivery and commit state follow the new cursor. Seek cannot silently commit offsets.

Certify atomic consume-transform-produce-plus-offset transactions using the existing transaction session: commit/abort, `read_committed`, producer fencing, broker retry/restart, revocation and cancellation. External HTTP/database side effects are outside Kafka atomicity. Preserve the existing bounded input-topic contract until an explicitly tested extension exists.

Separate fresh-run transactional identity from any supported continuation. Persist a logical run/group/VUser identity and fence it if restart continuation is offered; the current random suffix alone does not establish that promise. Otherwise terminate the old source session and publish explicit recovery limits. Real broker tests reconcile committed output/input offsets and prove aborted/failed messages invisible to `read_committed`.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#msg-3-kafka-semantics-and-certification).

### MSG-4: Generic JMS and Service Bus

Use a packaged restricted Java/Jakarta Messaging helper for genuine JMS semantics, initially certified against ActiveMQ Artemis with approved factories/drivers. Reuse containment primitives where applicable, but give the helper its own typed, versioned messaging contract; the existing arbitrary-command Solace sidecar is not a complete governed runner.

Support provider-qualified auto/client/dups-ok/individual settlement, local transacted session commit/rollback, selectors, Text/Bytes/Map/Stream types, TTL/priority/persistence, non-destructive browse, durable/shared subscriptions and explicit client/subscription identity. Serialize access to session-owned state. Do not deserialize arbitrary ObjectMessages. AMQP 1.0 transport support alone cannot claim all JMS semantics; AMQP 0-9-1 remains a separate protocol.

Preserve the current Go Service Bus adapter for its supported operations. Complete settlement, lock renewal/loss, reconnect, sessions/state, scheduled/deferred messages, DLQ operations and management depth tests. For atomic same-namespace send/complete and cross-entity send-via transactions, use a pinned restricted Java Service Bus helper with explicit begin/commit/rollback and transaction-context lifetime. Reject cross-namespace/distributed transaction requests and unsupported service tiers. All transactional receive/send/settlement operations and delivery tokens belong to one helper-owned client/connection scope. Go-adapter delivery tokens or transaction contexts from another helper cannot be enlisted. Validate namespace, receiver/session ownership and transaction expiry before each operation; expired or disconnected contexts fail explicitly. Lost commit acknowledgement produces an ambiguous outcome; retries cannot silently claim committed success. [Java transaction API](https://learn.microsoft.com/en-us/java/api/com.azure.messaging.servicebus.servicebustransactioncontext?view=azure-java-stable)

Helper packaging includes cancellation/drain, session/connection limits, memory/process quotas, credential identity checks and cleanup evidence. A required helper capability blocks Start if unavailable. Emulator cases cover their documented subset; cloud identity, production locks/recovery and service control-plane behavior require live acceptance.

### MSG-5: Imports, strict source execution and user workflows

Make AsyncAPI 2.x/3.x mappings binding-version-aware. Separate JMS, AMQP 1.0, AMQP 0-9-1, Solace SMF, MQ and Kafka/Event Hubs targets. Infer a provider only from sufficient explicit evidence; otherwise require operator resolution. Preserve security alternatives/server variables, destination semantics, typed headers/properties, TTL/selectors, reply/correlation, partition keys, supported serde and source disposition. Unsupported bindings such as MQTT remain inspectable review drafts. Runtime availability and mapping fidelity are separate gates. [Official JMS binding](https://www.asyncapi.com/docs/reference/bindings/jms)

Finish strict JMeter messaging with one run-owned source session spanning colocated groups: coordinated start, shared parameters/sampler admission, producer lifetime, active pause clock, drain and cancellation fence. Complete pinned Java buffer/batch/timer/lifecycle traces and protected atomic multi-script/scenario repair. Do not advertise its compatibility capability until conformance, distributed placement, repair/save/reopen and controlled-service acceptance pass. New native features never implicitly clear imported execution blockers.

Live/archive/report/CLI/native panels expose the same provider-qualified throughput, acknowledgement/settlement distributions, lag/depth type, ownership transitions, reliability populations and coverage. Extend existing scoped SLA machinery with ack/settlement duration, backlog/depth, duplicate/redelivery/DLQ rates and sustained throttle rules. Missing denominator or stale/incomplete required scope yields unscored, not pass. Advisory findings cannot stop tests; explicit policies may.

### MSG-6: Native Solace SMF and low-priority MQ

Implement `jms_solace_smf` behind existing JMS dispatch with CGO-enabled glibc Linux amd64 generator packaging first. Support direct/guaranteed publication, publisher ack windows, bounded receiver flow/backpressure, persistent settlement, wildcard subscriptions, topic/queue mapping, partition keys, access mode, DMQ/redelivery, replay, VPN/TLS/compression. Operator/harness provisioning supplies queues and replay/DMQ configuration. Reject incompatible combinations, including partitioned queues with replay. Direct publication measures only the actual exposed local SDK completion boundary; broker acknowledgement remains unavailable. Publisher acknowledgement windows and persistent settlement apply to guaranteed mode. Direct-message counts cannot establish confirmed broker receipt. Additional platforms are unavailable until SDK/platform and release evidence pass. [Solace queue restrictions](https://docs.solace.com/Messaging/Guaranteed-Msg/Queues.htm)

Implement IBM MQ last via `ibmmq_real` and pinned MQI/native-client packaging. Cover MQCONNX/open/put/get/browse/subscription, MsgId/CorrelId, MQMD/RFH2, persistence/priority/expiry, syncpoint commit/backout, CCDT/TLS credentials and backout/DLQ evidence. Use the existing detailed roadmap design and MQ container acceptance; mock support cannot promote native availability. [IBM-maintained MQI binding](https://github.com/ibm-messaging/mq-golang)

## 6. Browser evidence completion

### BE-1: Resource/document identity and timing

Introduce optional browser document/resource evidence v2 with document/navigation ID, frame ID, request ID, redirect chain/hop, initiating action ID, completion-observation action ID, source origin, request start epoch/document offset, actual available phases, method/status/fixed failure classification, byte semantics, cache/service-worker provenance and timing availability/reasons. Use captured initiator information only when exposed and sanitized. Do not infer a dependency solely from URL similarity.

Playwright request start is an epoch measurement while the other request timings are relative to it; preserve that distinction and sentinel/unavailable phases. CDP enrichment is Chromium-specific. Maintain redirect hops, concurrent requests, failed/no-response requests and requests spanning actions. The initiating action and later capture action are not interchangeable. Reused connections, cache and restricted cross-origin timing may omit phases; omit them rather than manufacture DNS/TLS/server time. [Playwright request timing](https://playwright.dev/docs/api/class-request#request-timing)

Retain bounded resource rows outside the compact live stream. The live card keeps its current top-50 summary but offers authorized pagination/drill-down over retained resources. Filters include exact action/document, fixed failure class, resource type, sanitized origin and device/engine. Sorting and counts describe the retained population; an omitted/truncated sample cannot become whole-page totals. Waterfalls use true request start, with gaps/phase availability shown explicitly.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#be-1-resourcedocument-identity-and-timing).

### BE-2: Document metrics and capture policy

Add per-metric availability, algorithm version, document lifecycle window and qualification. Use a pinned reviewed measurement library/implementation to compute session-window CLS and interaction-ranked document INP where supported. Keep the legacy summed-shift/max-event values under their original version/labels. Finalize LCP at its lifecycle boundary; FCP/LCP/CLS/INP, long tasks, layout/heap and engine-specific process metrics have separate support and completeness. Standards acceptance explicitly covers maximum CLS session windows and INP interaction grouping/outlier handling with at least 50 interactions. [CLS methodology](https://web.dev/articles/cls), [INP methodology](https://web.dev/articles/inp), [measurement implementation/support](https://github.com/GoogleChrome/web-vitals). Soft navigation metrics require an explicit separate contract; do not silently treat them as ordinary navigation metrics.

Assign stable measurement IDs and provisional-revision upserts; publish exactly one eligible final observation per document/lifecycle metric. Censored documents and no-interaction documents have unavailable final INP. Independent per-metric eligibility counts and mergeable final-document distributions replace the common browser `Samples` denominator; ten progress snapshots of one document cannot become ten samples. Keep document metrics separate from action durations and from progress snapshots. Missing metrics cannot contribute healthy zero or a score of 100. Archive aggregation/comparison must match metric version, document phase, engine/profile and population. Native page measurements and Chromium-only process/CDP measurements have independent capability labels. WebKit CPU/RSS remains unavailable until stable platform-specific attribution is proven.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#be-2-document-metrics-and-capture-policy).

### BE-3: Native and synthetic HAR delivery

Connect the existing native runtime recorder to typed artifact receipts, generator transfer, manifest publication, authorized replay/run downloads and ZIP exports. Add native recording to replay; implement a synthetic HAR 1.2 fallback from retained network evidence for old peers/capture-disabled contexts. Label `native` versus `synthetic`, capture mode, completeness, entry count, omitted fields, algorithm/schema version and body policy. A synthetic writer cannot invent absent phase timings or response content.

Native recording uses the pinned Playwright full timing mode with content omitted by default; content retention requires the existing explicit protected capture policy. Close the context gracefully to finalize native output, await bounded flush, scrub into a separate immutable artifact, then publish. A raw working file must never be downloadable. Failed redaction or partial invalid HAR is withheld with a typed reason; secrets-present capture remains disabled until the new omission/redaction path proves equivalent protection. Existing credential suppression is not removed merely because bodies are omitted.

Use private per-run/worker-generation staging with safe regular files and local ACLs, not a globally reusable temp namespace. Retain the existing 100 MiB/100,000-entry per-HAR and 128-artifact/1 GiB limits as default ceilings, additionally enforcing aggregate run quotas. Admit bounded recording against scratch reserve and driver/browser/generator RSS. Count issued requests and in-flight work, not only completed entries: starting defaults are 256 concurrently in-flight requests, 2,048 in-memory metadata rows and the existing 256-task collection queue, with explicit byte caps. Retain overflow/drop counters before discarding evidence. Native recording and sanitization share a reserved output/RSS budget and a 30-second finalization deadline; watchdogs observe buffering even before a file grows. Record truncation/resource-limit reasons. Quota exhaustion that can stop only recording marks evidence incomplete. Recorder overflow that requires closing a context is an explicit affected-VUser resource failure and censors unfinished document/journey observations; it must not appear as successful unaffected workload. Required-evidence policy may additionally fail the run explicitly; optional evidence failure cannot silently become an application pass. Publication waits for accepted artifact receipts to drain or records incomplete capture. Crash cleanup, late writers, project/run deletion and remote storage retention are fenced and durable.

The native HAR working-file follow-up provides private generation staging, exact descriptor leases, joined native close via a pinned licensed SDK subset, protected scrubbed publication and identity-bound terminal-retirement recovery. Retained browser resource pages provide bounded authorized JSONL/Parquet inspection, exact filters/cursors and dashboard/CLI readers. Review corrected native document finality, close/retirement races and late read ownership. [The composed review](archive/qa-reviews/INTEGRATION_2026-10.md#feature-completion-composed-review-2026-10-05) records completed scoped source/test qualification, including the default full Go suite and both dashboard authentication profiles. General crash cleanup, aggregate active-writer/RSS quotas, managed generator transfer, native/synthetic archive exports and full provider/platform/overhead acceptance remain open; these changes do not close BE-1–BE-4.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#be-3-native-and-synthetic-har-delivery).

### BE-4: Browser acceptance

Use controlled Chromium and WebKit pages covering cache/service workers, redirects, frames, cross-origin timing restrictions, long-running/background requests, transport close, HTTP errors, secret-bearing URLs/headers/bodies, context close/crash, never-finishing requests, huge/chunked bodies, metadata floods, flush-time quota exhaustion, recorder limits and cancellation. Reconcile UI waterfall timing with independent fixture clocks and native HAR; test old archives and synthetic fallback. Both auth profiles exercise live sample→archive resource→HAR/ZIP download, viewer/editor separation, project denial and retained missing values.

Publish benchmark artifacts for supported engine/profile/platform combinations: host/browser/driver versions, launch time, CPU/RSS, action/metric overhead, capture overhead, throttled timing separation, cleanup and stability. The current five-browser-VUser published ceiling remains until measured evidence authorizes an increase. Synthetic shaping is calibrated approximation, not a real-device equivalence claim.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#be-4-browser-acceptance).

Frames, never-finishing requests, never-ending chunked bodies, killed native recordings, failed subresources, cache disabling and revalidation, and runtime-secret route redaction ran in Chromium only and now run in both engines.

The opt-in `TestBrowserBenchmark` (gate `APEXLOAD_BROWSER_BENCHMARKS`) measures each engine with a desktop and a mobile profile it accepts. It records host, driver and browser versions; cold launch and session start; navigation time, capture overhead and throttled separation; peak RSS and CPU (with every other pooled browser closed first); teardown time and leftover processes; and failures, including sessions that fail to start. Each engine is a subtest, so a host without WebKit still produces Chromium's results. It writes `browser-benchmark.json` and, given the measured commit, F4 acceptance requests (`BE-4`, `browser-benchmark`) that pass the endpoint's validation: `pending` for a reviewer to judge against the published targets, or `failed` when unstable, never `passed` by itself. `scripts/run-optin-suites.sh --all` runs the browser package with both engines required and the acceptance benchmark as the "browser acceptance" suite, keeping the results at a printed path and naming the commit only for an unchanged tree. A local run (darwin-arm64, Chromium 149, WebKit 26.5) had no failures and no leftover processes; throttled navigations were 194–207 ms slower for 200 ms of latency.

Deviations:
- No benchmark results are published or recorded; an operator records them per engine, profile and platform, and the five-browser-VUser ceiling stands. The benchmark's profile names (`chromium-desktop-1080p`) are not the browser benchmark evidence's profile IDs (`desktop_chromium_1080p`), and navigation time is absolute, not an overhead against a baseline.
- Metadata floods, flush-time quota exhaustion, recorder limits, old-archive fallback, and secret-bearing headers and bodies are tested without a browser (a secret in the URL path is tested in both engines).
- Viewer/editor separation and project denial run in the local-auth profile only: the default profile has no second principal.
- The end-to-end walk starts from an archived run, not a live sample, and downloads a synthetic HAR (D7); the dashboard's waterfall rendering is not reconciled against a fixture, only the recorded phases.
- No test crashes a browser, renderer or page under a live generator (context close and a killed recording owner are covered), and the huge-body case is a never-ending stream with stubbed memory, not a large finite body.
- HTTP cache provenance is not asserted per response; cache disabling and revalidation are.
- WebKit has no desktop device profile, so its desktop benchmark sets a 1920x1080 viewport.
- Cold launch is timed against a closed browser pool, not a cold host.

## 7. Browser intelligence completion

### BI-1: Explainable findings and applicability

Introduce versioned browser finding records with rule/model version, exact measurement/population/window, applicability, observed value/threshold, quality/coverage, matching baseline criteria and immutable evidence links. Reuse incident/root-cause/report delivery with deduplication and resolved transitions. A finding may be suppressed or unscored with a visible reason. Application outcomes, collection health and automation/selector failures remain distinct.

Threshold scoring uses documented nonnegative weights and only qualified metrics. A required missing metric prevents an overall qualified score; show observed component contributions and coverage instead. Preserve the legacy scoring version for historical reports. Avoid a healthy badge when collection is absent.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#bi-1-explainable-findings-and-applicability).

### BI-2: Baseline anomalies

Match browser metric/algorithm version, actual engine/browser version, platform/profile/viewport, applied shaping, cache/new-user policy, stable authored script/action/route/document-kind identity and compatible load window. Concrete document/navigation IDs are only evidence lineage/deduplication identities; they are never cross-run matching keys. Mark pauses, source loss, shaping changes and insufficient/unrepresentative windows. Use the existing minimum 30 valid comparable buckets and absolute z-score ≥3 as an advisory three-sigma heuristic, not a calibrated p-value. Require a practical effect floor: timing changes at least max(50 ms, 10% of baseline mean), CLS changes at least 0.02, and other metrics a positive catalog-defined/configured floor. Zero variance and insufficient baselines remain unscored. Group repeated observations from one browser/document before qualification so publication repeats cannot manufacture independent samples. Publish direction, expected range, sample/bucket count, baseline provenance and excluded reasons. Statistical findings remain advisory; explicit application SLAs own CI/stop outcomes.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#bi-2-baseline-anomalies).

A scored result also gives the expected range: the baseline mean ± the larger of 3σ and the effect floor, so a value inside it is within range.

Buckets outside steady load (ramp, pause, settle) are counted as excluded. The RCA panel lists the results under **Browser baseline**; they stay advisory and never feed hypotheses or CI.

Deviations:
- Authored action and route identity are not part of the cohort key, because document observations do not record them; the scenario group stands in, and only while the baseline ran the same script and configuration.
- Of the excluded reasons, only `outside_steady_load` is counted per bucket; source loss is a per-run caveat, and there is no check for an unrepresentative or short window.
- No catalog-defined effect floors exist yet, so metrics other than timings and CLS are unscored (`no_effect_floor`); the metrics RCA reads all have floors.
- Browser findings (E1) still report their baseline criteria as `not_evaluated`, and do not cite these results.
- The run's side is the median of its bucket p75 values, not an independent per-document test.
- The pinned `browser_policy` sets the effect floors (raising the defaults only) and the baseline mode, but the analysis names its baseline run: `latest_compatible` uses that run rather than selecting one, and `pinned_run` compares only when it is the pinned run.

### BI-3: Interaction patterns, heatmaps and journey impact

Report measured repeated click/tap patterns, failed declared outcomes, retries, unexpected navigation and incomplete journeys with explicit windows and thresholds. Dead-interaction findings require an authored expected assertion/navigation/dialog/download/state outcome and observation deadline; an unchanged DOM, navigation away or cancellation alone cannot establish abandonment or a dead interaction. The starting repeated-pattern rule is at least three matching gestures within two seconds and 32 CSS pixels, excluding explicitly intended repetitions/retries. Synthetic scripts can generate repeated gestures but cannot prove human frustration or intent. Record `scripted`, explicitly consented `recorded`, or `unknown` provenance; unknown is ineligible for human-frustration labels; `isTrusted` is not sufficient to determine that distinction. Do not inject extra interactions or probe traffic.

Heatmaps aggregate observed coordinates into at most a 64×64 grid for the same sanitized route, document/layout revision, viewport/orientation, engine and profile. Define CSS coordinate space, viewport dimensions, scroll offsets, frame identity and layout revision. Map frame coordinates only through an observed transform; otherwise keep a separate frame map. Keyboard interactions use a target table, not fabricated positions. An abstract grid uses a safe matching layout reference; screenshot-backed overlays are optional and require authorized matching capture. Incompatible layouts remain separate. Exclude secret/form input content and unsafe DOM labels, mask sensitive screenshot regions or suppress capture, and retain sampling/coverage. Heatmaps describe observed scripted/opt-in recorded interactions, not production population behavior.

Define one journey attempt by an explicit start and terminal outcome, with nonnegative versioned journey weights. Report completed/failed/aborted/skipped/censored attempts separately from step failures. The default weighted failure index is `sum(weight_j * failed_eligible_journey_attempts_j) / sum(weight_j * eligible_journey_attempts_j)`; one failed journey counts once regardless of its step count. Zero denominator is unscored. A separate step-risk index requires explicit configuration. Missing steps and censored sessions remain incomplete. Revenue/conversion impact requires an explicit authorized external model and its provenance; it is never inferred from synthetic latency. Default results are weighted test-journey risk, not lost revenue.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#bi-3-interaction-patterns-heatmaps-and-journey-impact).

Heatmaps place each pointer gesture's first point on an abstract 64×64 viewport grid. There is one map per sanitized route, layout revision, viewport, device pixel ratio and orientation, engine and profile (joined from the run's document evidence by source generation, else `unknown`). A run that captured interactions but kept none in its archive answers `unavailable`, and coverage counts the archive's own losses. Keyboard targets go to a per-map role and hashed-label table. Child-frame, navigated, synthetic, page-state-unavailable, viewport-less and out-of-viewport gestures are counted, not mapped. The run report shows the patterns and grids under **Browser evidence**.

Deviations:
- No screenshot overlays.
- No child-frame maps, because E3 records no child-frame points.
- Incomplete journeys stay with E5's journey accounting.
- Patterns are found among the first 100,000 interactions.
- No CLI command.
- Engine and device profile come from document evidence only: without it (none kept for a context, unreadable, or a context past the first 65,536) they are `unknown` and disclosed with the reason (`engine_profile_unknown`, plus `document_evidence_unreadable` or `document_evidence_truncated`), and gestures of different engines with one layout can share a map.
- Cells are viewport positions at dispatch; gestures at different scroll offsets share a map, which states its scroll range, and the layout revision is the one read after the gesture.
- Steps are told apart by action name and gesture kind (capture records no stable step key), so distinct steps that share a name are one step for retries and for repeats without dispatched points.
- Dead interactions come only from post-action `sync` waits; authored assertion, dialog and download outcomes are not used.
- The repeat window is measured on capture time, taken after each gesture's wait and page reads, because E3 records no dispatch time.

### BI-4: User workflows and acceptance

Provide device/engine/page comparison, finding list and evidence drill-down, baseline qualification/exclusions, interaction map and journey summary in live/archive views, saved native panels and reports. Editors configure threshold/journey definitions and pin baseline policies; viewers inspect versioned results. Definitions use optimistic revisions and cannot retroactively rewrite archived findings.

Golden browser fixtures reproduce known layout shifts, interactions and regressions, missing/unsupported metrics, repeated publications, synthetic repeated taps, layout mismatch and failed journey steps. Assert measured ground truth and scoring/exclusion rules; test no additional target traffic. Verify both engines' support differences and both auth profiles' configuration, finding→resource navigation, comparison and exports. Baseline intelligence cannot release before BE document-metric qualification passes.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#bi-4-user-workflows-and-acceptance).

  Diagnostics' browser evidence points to it. The live execution page shows the comparison over the documents published so far.
- **Drill-down:** finding → document → resources → HAR entry. A finding's worst document opens the inspector filtered to that document. A request opens its HAR entry through `GET /api/v1/tests/{id}/archive/browser/har-entry?request_id=` (HAR download permission): a synthetic HAR 1.2 entry scrubbed and bounded as the downloadable synthetic HAR is, or, for a context that recorded a native HAR, that HAR's name.
- **Policy editor:** **Settings → Workspaces → Browser & journey policies** lists, creates, edits and deletes a project's browser and journey policies. It presents the revision it read; a save refused because the policy's revision moved offers the current revision (other refusals, such as a second default, show their reason); and any past revision can be shown read-only. The list, in the editor and `apexctl`, reads every page. Viewers read them. Run pins and finding revisions are unchanged by edits.
- **Saved panels:** the project panel editor offers the **Archived browser documents** population (`document_*` metrics at p50–p99), which reads one measurement tuple.
- **Reports:** the engineer profile adds Browser Findings, Interaction Patterns and Heatmaps, and Journey Risk sections (HTML, PDF, CSV and DOCX through HTML). Their results are read at export, and a section without a result says why. The browser baseline is reported in the root-cause section, with its analysis, for projects that present root-cause v2 (`APEXLOAD_RCA_V2_PRESENT_PROJECTS`); the v1 root-cause section has no baseline, while the Browser area shows it for every project.
- **CLI and MCP:** `apexctl analysis interactions`, `har-entry` and `policies list|get|save|delete` (a save of an existing policy needs the revision it was read at), and MCP tools for findings, interactions, journeys, the HAR entry and the policies.

Deviations:
- Pages are compared by scenario group, because documents record no page or route.
- The comparison shows p75 and its document count only.
- Findings, the browser baseline, interaction patterns and journeys read the finalized archive, so the live page shows only the comparison.
- A run pins its project's default policy of each kind at Start; the plan's choice of a different policy revision for one run is not offered, in the dashboard or the API.
- A saved panel reads one tuple, so the comparison table offers no "save as panel"; panels are authored in the panel editor.
- When one engine, device profile and group ran under several runtime cohorts, a saved panel must name the cohort by its digest, which no dashboard view shows (the comparison numbers cohorts "1 of N"), so such a group cannot be made a panel from the dashboard.
- The report sections are in the engineer profile only.
- The drill-down workflow test runs a real HTTP run (its archive and policy pin are real, and the real HAR-entry API is probed only for a 404), but its document summary, findings result, resource page and the HAR entry it shows are transport fixtures; no live browser run proves the drill-down end to end.
- The journey-policy editor has component tests only; no end-to-end test drives it (the journey workflow creates its policy through the API).

Deviations:
- Baseline regressions are accepted by unit tests over seeded series; no two real runs are compared.
- The golden drives the browser runtime directly, not the engine, so it writes the iteration-end row the engine would record.
- INP is not part of the golden.
- Layout-mismatch and exclusion rules are accepted by unit tests only; the golden page exercises neither.
- The plan's rule that baseline intelligence is not released before BE qualification passes is a release step: nothing in the product or CI enforces it.
- The blank document a browser context starts with records an unavailable FCP. It carries no value, and findings count it as an unavailable final observation.
- No acceptance record is published; an operator posts the requests.

## 8. Telemetry reporting completion

### TEL-1: Immutable policy and time coverage

Snapshot the resolved telemetry budget policy at Start: policy ID/version/hash, per-stage and end-to-end thresholds, nil/default versus zero/disabled semantics, sustained window, expected publication cadence, source generations, coverage rules and classification algorithm version. An intentional policy change during a run creates a new timestamped policy segment; it cannot reinterpret earlier classifications.

Persist observation start/end plus classification and availability per required source. Reports show both retained interval counts and union-based wall-time coverage. Overlapping/retried publications count once. Time gaps and controller takeover periods remain unknown, including during pause. Report healthy/degraded/breached/unknown durations and breached-known-time fraction with explicit denominators; never imply missing intervals were healthy.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#tel-1-immutable-policy-and-time-coverage).

### TEL-2: Loss and stage distributions

Retain source/generation/sequence-specific cumulative counter observations. Compute exact loss increments only within a valid monotonic source epoch with a known initial origin and coverage. Reset/new generation, missing origins, malformed negatives and overflow yield observed lower bounds or unavailable totals. Fleet and run totals cannot be inferred by adding repeated cumulative publications. Preserve the newly added explicit availability column and legacy unknown history.

Measure generator capture/aggregation/publish, transport, controller receive/queue/fan-out, browser receive/render and end-to-end age with independent availability. Add bounded mergeable duration distributions and P50/P95/P99/max only where sampled. Do not add marginal stage percentiles into an end-to-end percentile. Browser-local receive→render is measurable without cross-host clocks; end-to-end browser age requires clock qualification and clearly separates visible, background and disconnected clients. Client render evidence is opt-in, authenticated, rate-limited and advisory; absence cannot fail an unattended CLI run.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#tel-2-loss-and-stage-distributions).

### TEL-3: Comparison, reports and explicit verdicts

Retain whole-run results separately from selected steady-load windows. Compare policies/cadence/source capability/workload versions; incompatible policies show side-by-side evidence with an incompatibility reason. Allow explicit re-evaluation under a named common policy only when retained raw lag/window evidence can answer it; it creates a new derived result and keeps original classifications.

Add a separate telemetry acceptance policy with `advisory` default and explicit opt-in `fail_ci`. It can require minimum wall-time coverage, maximum loss, maximum known-time breach fraction and specified lag percentiles. Outcomes are pass/fail/inconclusive; unknown required evidence cannot pass and CI maps inconclusive through an explicit caller policy. Application regression verdicts and telemetry verdicts are separate fields. This specification adds no automatic run stopping from advisory telemetry.

Live cards, saved baseline comparisons and HTML/PDF/DOCX/CSV/JSON/JUnit exports show policy revisions, exact/observed-lower-bound loss, clock quality, coverage, denominators, stage distributions and exclusions. CLI supports policy selection and verdict retrieval through the existing run/wait/comparison commands. Incidents/webhooks reuse durable deduplication with policy/run/source evidence links; no secret content in messages.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#tel-3-comparison-reports-and-explicit-verdicts).

The manifest now records `telemetry_loss` at finalize. A distributed run finalized without its source ledger has every loss counter unavailable (`ledger_lost`).

Where the verdict appears:
- **CI:** `ciRunResponse.telemetry_verdict`, beside the application outcome. Only `fail_ci` changes the outcome, and only for an otherwise passed run: `telemetry_failed` (exit 1), or `telemetry_inconclusive` (exit 2) when the caller gave no mapping. `telemetry_inconclusive` (`apexctl --telemetry-inconclusive=pass|fail`, action input `telemetry-inconclusive`) has no default. The controller refuses a CI run without it when the project's default acceptance policy is `fail_ci`. Advisory and provisional verdicts never change the outcome.
- **CI evidence:** a separate `telemetry_acceptance` JUnit test case (an advisory non-pass is skipped), a separate section in the GitHub check, job summary rows, and the `telemetry-verdict` output.
- **Live:** while the run executes, the Telemetry card shows a provisional verdict (none for a finished run or for health rebuilt from stored history).
- **Run report:** a **Telemetry Acceptance** section in every report profile (HTML, PDF, CSV and, through HTML, DOCX). The report JSON adds `telemetry_acceptance`. The section shows:
  - policy revisions;
  - criteria with their observed values, bounds and denominators;
  - exact or lower-bound loss;
  - coverage by source and classified time;
  - clock quality;
  - stage distributions with their exclusions;
  - what the verdict does not count.
- **Saved comparisons:** each run's verdict and dropped results, side by side.
- **Webhooks:** a `telemetry_acceptance` event for a failed or inconclusive finalized run, sent once per run and outcome: a durable claim (deleted with the run) keeps a later attach of the archive (an adjunct refresh such as a new note, a recovered publication, another leader) from announcing the same outcome again, while a verdict a refresh found changed is announced. It names the run, the policy revision, the deciding criteria that did not pass (never advisory ones) and the sources with unknown time. A run in mode `unknown` is not announced.

Deviations:
- No incident record is created; the webhook is the notification, and it carries no evidence links (such as a report URL). A dispatch lost after its claim is not retried.
- A run that pinned a policy before definitions were frozen is inconclusive in mode `unknown`, which no gate enforces.
- Only `apexctl ci run` carries the verdict; `apexctl run`, `wait`, `status` and the comparison commands do not show it, and no CLI selects a non-default acceptance policy.
- The JUnit case is apexctl's CI JUnit; there is no server-side JUnit export.
- Saved comparisons show each run's verdict and dropped results only, not its coverage, clock quality, denominators, stage distributions or exclusions (those are in the run report).
- The live verdict leaves breached time inconclusive until finalize.
- A local run's live coverage is recorded only at finalize.

### TEL-4: Acceptance and measured latency

Complete the final published malformed-counter API regressions and refreshed API race gate, plus live PostgreSQL release-26 migration verification, before this program's new behavior merges. Test legacy/nil/zero/positive/negative/overflow/reset counters, cold/missing peers, out-of-order/retried publications, ownership epochs, cadence/policy changes, clock skew, browser reconnect/background/render stalls, HA adoption gaps and sanitized exports.

The under-two-second pipeline figure remains a design target. Certify a declared reference workload, not universal density: on a 4 vCPU/16 GiB controller with two declared generator hosts and a foreground dashboard, exercise 2,000 accepted observations/s, one-second publications, collector traffic and concurrent admitted analytics. Proposed release targets are P99 qualified capture→visible-render age ≤2,000 ms and zero unreported accepted-record loss. Record stage P50/P95/P99, CPU/RSS, queue/byte volumes, clock uncertainty and coverage. A slow browser/unsupported clock is classified separately; overloaded/fault trials must surface degraded/incomplete status and recover without control-plane failure. Density/host upgrades need new evidence.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#tel-4-acceptance-and-measured-latency).

The opt-in `TestTelemetryReferenceWorkload` runs the reference workload in one process:
- two declared generators (real generator services over in-memory gRPC), each running half of 2,000 accepted observations a second for 60 s against a local target, publishing every second;
- a foreground dashboard: headless Chromium on the run's live page, reporting its render evidence;
- archive analyses of an earlier run, read every second.

It records each stage's P50/P95/P99, coverage, CPU, and peak RSS sampled for the process, its child processes (the browser and its driver) and both together. Loss is accounted per path against the target's own count of served requests: the archive's samples, and separately the run's live totals (successes and recorded errors) plus its dropped-results counter, must account for every served request but those aborted in flight at the stop (at most one per VUser). Lost publications and dropped subscriber batches are counted in publications, and a dropped batch is also a lost sequence: each lost publication may account for at most the largest window one generator's group published, and a shortfall within that is reported loss of unknown size, classified `live_publications_lost`. A path holding more results than were served (duplicates) also fails. A run also fails without browser-render evidence (the suite requires the browser). An unqualified clock is classified `clock_unqualified` and fails the run, since no sample age can be judged; a slow browser is classified `slow_browser` without failing it. With a commit it writes one TEL-4 F4 request, `pending` without failures and `failed` otherwise, including a run that stopped before it was measured.

A local run (darwin-arm64, 10 CPUs) **failed the target**:
- **Loss:** 123,146 requests served and 123,146 archived (1,887 a second over the run's whole window, start lead and drain included; the harness now takes the rate from the first archived sample to the last), with zero unreported archive loss (the live path was not yet accounted); every loss counter was exact at 0, except the WebSocket drops, which are controller-wide.
- **Stage P50/P95/P99:**
  - generator processing: 502 / 503 / 506 ms;
  - transport: 8 / 17 / 5,181 ms;
  - controller fan-out: 10 / 16 / 990 ms;
  - sample age: 525 / 1,512 / 5,199 ms, above the 2,000 ms target;
  - browser render: 141 / 239 / 307 ms.
- **Sample-age and browser-render maxima:** 5.5 s together. This leaves out delivery from the controller's publication to the browser's receipt, so it does not bound capture to visible render.
- **Analyses:** 94 concurrent analyses, none failed.
- **Coverage:** 61 of 65 s observed; under the default budgets, 13 s degraded, 46 s breached and none healthy.

After the review's per-path loss accounting, 20-second local runs showed the archive path exact (for example 43,059 served and 43,059 archived) while the run's live totals were short by 1,900 to 5,900, with no recorded errors and every live loss counter exact at 0. A whole live window could go missing, and the final window was counted again by each terminal publication. The reference workload therefore failed on the live path. This was a defect in the live run totals that the harness exposed, and it was fixed after Wave 5. Before the fix, the run totals summed published samples, which are throttled fleet-wide and hold only each generator's latest window. A distributed run now counts each generator's window where its batch is accepted:
- **Collections are numbered.** A generator numbers its metric collections within its process (`collection_sequence`). A batch that re-sends the last window repeats the number and is not counted again; that covers stopping, paused and terminal batches and a subscription replay.
- **A stop drain is a new window.** It is counted. On a generator running several groups it replaces every group's published window, so a group that ended earlier is not carried into the terminal batch again.
- **During a stop requested on the controller, the controller keeps reading each generator's metrics.** That covers an operator, a threshold or goal rule, a CI abort and a shutdown. The run is set STOPPING before the generators stop, so a window collected before then can still arrive ahead of the terminal batch. The controller counts that window without publishing it, then reads the terminal batch with the final drain.
  - Before, it stopped reading at that window. For a graceful stop both were lost, and the run ended only when the stop grace period forced it.
  - A forced abort (Abort, or a CI abort) ends once every generator confirms. A window that arrives after that is not counted.
- **Older generators** send no collection number. Their re-sends are matched by the collection's publication time. A batch with neither is counted unless it is terminal; a terminal one is taken to re-send.
- **Counting rules.** A window is counted by the same rules as a published sample, and a parity test holds the two together.

Limits:
- **A controller that takes over a run** starts the totals from the stored history, not from a copy it kept. If the stored history cannot be read, it starts from the history it holds.
  - Stored history keeps no run totals, so the start is the sum of the stored windows, which is bounded and approximate as before the fix.
  - Stored windows keep no groups either. A run with one group starts its group's totals from the same sum; with several groups, the group totals restart at zero.
  - Each generator's last window, replayed when the stream reconnects, may be counted a second time.
- **An older generator** is matched by publication time only. Two of its collections published in the same millisecond count once. With several groups, its stop drain carries forward the last window of any group with nothing left to drain, and that window is counted again.

Four 20-second runs of the reference workload afterwards, each completing on its own, showed the live totals equal to the archived samples: 43,036 of 43,037 served (one in flight at the stop) and 43,056 of 43,056; after the review fixes, 43,122 of 43,122; and with the stop fix, 43,052 of 43,052. The 20-second test passed each time. The declared 60-second target was not rerun.

Deviations:
- The transport and sample-age P99s come from one or two publications per run, and they persist without the concurrent analyses; a generator's final publication is suspected and is not yet explained.
- Generator processing sits at the generator's half-second aggregation window, so the default budgets read the run as breached.
- The workload runs in one process: transport is in-memory gRPC, and CPU and the process's RSS cover the controller, both generators and the target together. It is not the 4 vCPU / 16 GiB reference controller.
- Collector traffic, an overload trial and native (DuckDB) analytics are not exercised, and queue and byte volumes are not recorded.
- No test changes the cadence or policy within a run (each run has one policy segment).
- Exports are covered by the archive's export redaction, not by a telemetry-specific sanitization test.
- No acceptance record is published.

## 9. Native analytics completion

Implementation coordination: reuse the in-progress RCA engine in `claude/rca-correlation-start` for analysis, detection, graph construction and workload/baseline qualification, together with its immutable evidence revisions and fenced durable jobs. Native ingestion and query work supply adapters to that engine rather than a second analytical core or job lifecycle. Adopt the authoritative engine changes before wiring those adapters. Cross-run series identity must preserve unit, kind, population/version, derivation, provider provenance and observation-grid semantics; matching an identity does not establish qualified coverage or permission to compare measurements. Existing archived engine revisions remain readable, and new identity semantics require an explicit engine revision.

The [portable series-identity prerequisite](qa/reviews/feature-completion-native-rca-identity-2026-10-05/README.md) is implemented and independently reviewed, with package normal/race checks. Its bounded structured encoding preserves existing comparison semantics and declared observation grids. It does not wire runtime adapters, adopt an engine revision or establish native scale/platform acceptance.

The current RCA `detect.v1` defaults use robust CUSUM with ten baseline buckets. Browser advice specified in BI-2 requires thirty qualified buckets, its three-sigma rule and practical effect floors. Engine adoption must retain explicit versioned detector/policy semantics and satisfy those browser requirements; existing onset findings cannot be relabeled as that advisory qualification. Extend the authoritative engine contracts rather than adding a competing detector.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#9-native-analytics-completion).

### NA-1: Metric and query catalog

Create one typed metric catalog in `pkg/analysis`, consumed by storage validation, API, CLI and dashboard authoring. Each entry declares input population/kind, unit, allowed aggregation/derivation/grouping/filtering, required evidence/capability, missing/reset rules and whether it is eligible for baseline/correlation/SLA use. Existing names and saved definitions keep their semantics. A catalog option does not imply a peer has captured it.

Complete operations over qualified evidence: counts; observation/error/success rates; latency and message/byte/duration distributions; byte/message rates where measured; same-population error/ack/settlement/reliability ratios; typed gauge aggregations; reset-aware counter deltas/rates; compatible baseline absolute/percentage deltas; and qualified moving mean/standard deviation over bounded windows. Division by zero, missing denominator, empty statistical populations, incompatible units and insufficient coverage remain unscored. A complete observed window with zero events retains measured zero for counts and elapsed event rates; an absent or incomplete window remains missing. Bytes/messages are not interchangeable with transaction observations. Rolling percentiles use raw observations or mergeable distributions, never percentile scalars.

Summary imports explicitly declare statistic/window/sample count. They can display their own statistic; only sufficient count/sum or genuine distribution inputs permit specific reaggregation. Precomputed P95 remains a precomputed series. Histogram imports use an explicit typed mapping of observation window, unit, bin bounds, counts and cumulative-versus-delta convention, with at most 128 bins, ordered compatible boundaries and checked nonnegative counts. Merge only compatible declared distributions; percentile estimates retain algorithm/bucket-resolution labels and never claim exact raw-observation quantiles. Ordinary repeated/nested fields are not automatically expanded into histogram measurements. No arbitrary SQL, expression evaluator, external extension or user-provided function.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#na-1-metric-and-query-catalog).

### NA-2: Imported schema and partitions v2

Extend saved mappings/normalized artifacts to retain selected nested scalar host/service/source/run/scenario/action/transaction/protocol/group/region/device/network/phase/hierarchy and approved dimensions, plus status/error classification, window/sample counts and optional source timestamp quality. Values use allowlisted keys and bounded/sanitized categories. Preserve provenance and explicit project-authorized managed-run association; imported run-like labels do not become managed run IDs.

Current v1 normalized artifacts contain metric/time/kind/unit/value but not the broader selected dimension contract. Do not reconstruct discarded dimensions from filenames or arbitrary tags. Re-normalization requires immutable originals, a new mapping/artifact revision and atomic publication; old datasets remain readable. Duplicate-file detection uses hashes and explicit duplicate policy without deduplicating legitimate repeated transaction rows.

Build a durable partition manifest containing hashes, row/measurement counts, time ranges, metric/kind/unit/schema versions and approved partition categories. Use it to prune objects before download, then projection/filter pushdown within selected Parquet files. Validate relative paths and conflicts between file columns, mapping constants and partition values; precedence must be chosen explicitly. Directory upload alone is not evidence of optimized partition pruning. [DuckDB Parquet pushdown](https://duckdb.org/docs/current/data/parquet/overview)

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#na-2-imported-schema-and-partitions-v2).

### NA-3: Live/archive dimensions and alignment

Add capability-gated native evidence v4 for actual measured tuples needed by the query catalog. Cover action or transaction population, protocol, group, region, dependency, page/hierarchy/phase, network and browser profile/engine/platform, and approved promoted property categories where captured. Keep host/service/source dimensions on their applicable monitoring/import populations. Never join independent marginal summaries into an invented intersection.

Default bounds: at most three grouping dimensions, 20 returned series, 16 bounded filters and 512 admitted joint keys per source publication. Configure a capture projection before Start so generators retain only approved tuples; excess cardinality marks the affected selection incomplete. Optional explicit top-N/other results must disclose omitted count/population and cannot qualify fleet totals or exact maxima. No silent truncation. Labels, histograms and joint payloads have byte and distribution-size caps independent of key count.

Retain source publication identity, expected peer inventory and exact window boundaries. Observation rates require wholly contained nonoverlapping source windows and full union coverage; boundary-crossing counts cannot be prorated. Archive raw observations use their actual timestamps. Missing source/window/metric support stays a gap. Do not average percentiles or infer transaction metrics from action totals.

Use one alignment/correlation implementation for runs, monitors and imports. Bound to 10,000 buckets, 366 days and at most 121 lag candidates; report paired count, common coverage, stale/reset exclusions, lag sign and constant-series unavailability. Direct comparable overlays, baseline deltas and reaggregation require compatible semantic kinds, units and populations. Typed ratios follow the catalog’s declared numerator/denominator units and eligible populations. Lagged correlation may associate different qualified series, such as CPU-percent gauges and transaction latency in milliseconds: each series retains its own semantics, units, population and coverage, with explicit conversion of counters and statistic selection for summaries. Correlation never reinterprets summary percentiles as raw observations and remains association. Separate labelled axes are permissible for investigation, with incompatible values never presented as directly comparable.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#na-3-livearchive-dimensions-and-alignment).

### NA-4: Durable definitions and complete workflows

Extend existing versioned panel/comparison/mapping stores; avoid a parallel dashboard service. Editors choose population, metric, derivation, filters/grouping, source(s), UTC or elapsed alignment, coverage, baseline and lag settings; viewers reopen the exact saved revision. Offer capability-driven options, precise unavailable reasons, bucket drill-down and redacted CSV/SVG plus existing report exports. Definition changes use optimistic revisions and saved results preserve input artifact/policy versions.

Complete equivalent `apexctl analysis` catalog/query/compare/import/mapping operations without whole-file memory loading. Permit reusable definitions and cross-scenario selections only within authorized project ownership. Cancellation/deletion revokes worker publication, releases cache leases and reclaims originals/normalized data according to retention, including late outputs and project deletion. Hash-bound cache identity includes project, artifact revision, query semantics and capability/catalog version.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#na-4-durable-definitions-and-complete-workflows).

Deviations:
- No analytical result cache exists, so there is no cache identity to key. Every query recomputes from the archive and its artifacts.
- Archive queries align by elapsed time from each run's start; there is no UTC alignment option.
- Coverage thresholds are not a saved query setting.
- Pins are opt-in, and the dashboard does not set them yet.
- A saved revision records its query version, not the policy or catalog versions it was saved under.
- The analytics query has no source list and no baseline setting beyond the existing comparison run.
- The panel editor's options are not driven by capability responses with precise unavailable reasons, and there is no bucket drill-down or report export of a panel.
- Cancellation and retention of imports are unchanged.
- No CSV/SVG changes beyond the existing chart exports.

### NA-5: Resource isolation and native packaging

Retain the embedded pinned DuckDB driver in controller/desktop builds, with CGO-free unavailable profiles and no generator/collector dependency. Execute native import/query/export pipelines through a contained child mode of the same versioned binary, so cancellation/resource exhaustion can terminate analytics without terminating the controller or Wails UI. This is a new isolation requirement, not existing behavior. Child IPC accepts typed operations and approved artifact handles, never SQL or arbitrary file paths; credentials/unrelated environment are removed. Durable leases/publication remain controller-owned.

One heavy job per controller process remains the default. Two DuckDB threads, 2 GiB engine memory and 8 GiB spill are maximum default settings subject to admission. Admission reserves controller headroom and evaluates process/container memory, free disk, projected output and spill; smaller profiles lower the engine budget explicitly. Track combined controller/worker RSS and disk. Cancel at the reserved headroom boundary, terminate the worker after two seconds without cancellation acknowledgement and force termination after five seconds, with cleanup fenced. Use OS containment where available and describe the remaining watchdog uncertainty on each platform. DuckDB's engine setting is not a total native-memory limit. [DuckDB memory behavior](https://duckdb.org/docs/current/guides/performance/oom)

Keep extension auto-install/load disabled and file access restricted to private resolved staging paths. Lock configuration, limit worker execution/output, guard symlinks/path races and record resource failures explicitly. Each HA replica has its own disposable worker/cache; never share a writable DuckDB file.

Native release evidence must execute controller and packaged desktop workflows on Linux amd64/arm64, macOS arm64 and Windows amd64, with pinned driver/library/image hashes, fresh-install/uninstall and no runtime extension downloads. Verify CGO-free capability absence and generator/collector dependency exclusion. These platforms are documented prebuilt targets, but the repository's lanes alone do not prove release success. [DuckDB Go packaging](https://duckdb.org/docs/current/clients/go/overview)

### NA-6: Analytical acceptance

Golden fixtures cover long/wide/nested mappings, decimals, timezones/DST, nulls, malformed files, schema drift, raw versus summary/distribution inputs, units, duplicate files, partition conflicts, resets and exact rejection provenance. Verify preview→commit→query reconciliation, source nonmutation, cross-project denial, cancellation/recovery/HA fences and deletion. Independently reconcile raw count/sum/percentiles and v4 joint/rate coverage.

Use a reproducible 1 GiB compressed, multi-file partition fixture on 4 vCPU/16 GiB RAM with at least 32 GiB scratch plus admission-verified output/spill reserve. Record hashes, rows, import/query durations, scan/pruned object/byte counts, peak combined RSS, spill, cancellation and simultaneous live telemetry lag. Test selective projection/partition pruning, repeated cached query, cold replica, worker crash, disk exhaustion and contention. Proposed bounded interactive-query P95 target is five seconds for the documented selected-window workload; imports have the existing 15-minute per-job deadline unless explicitly configured. Fail on controller failure, unexplained data loss, breached headroom or sustained reference telemetry-SLA violation. No result from this fixture closes the waived 10 GB benchmark.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#na-6-analytical-acceptance).

Deviations:
- Measurements are doubles: a DECIMAL beyond about 15 significant digits is rounded to the nearest double (12345678901234567.89 becomes 1.2345678901234568e16), not refused.
- A file whose schema drifted (a mapped column missing) fails the import closed, with the fixed stored message; the message names neither the file nor the column.
- Conflicting files keep every observation (two values at one instant average), and identical content chunked differently is not refused at commit; normalization still fails closed on it.
- Partition values are not read: sources are read with `hive_partitioning=false`, so a partition value that disagrees with a column (the plan's partition conflict) is neither resolved nor tested.
- The selected-window time is about 2.5 s at 64 MiB and at 1 GiB: it is dominated by starting a worker per query, not by scanning.
- The benchmark does not measure spill bytes, live telemetry lag under a concurrent run, a cold replica, a worker crash, disk exhaustion or contention, and it neither checks nor records scratch capacity.
- The platform lanes are CI evidence that this repository cannot produce locally. They run the native checks only: NA-5's install and uninstall checks and pinned artifact hashes are not produced. No acceptance record is published, and the 10 GB waiver stands.

## 10. Authoring cleanup completion

### AUTH-1: Two shared owners and revision safety

Consolidate around a script session owner and a scenario assembly owner, keeping their distinct entry surfaces. Reuse `useScriptsAuthoringState`, editor controllers, `ScenarioDraftState` and existing payload/effective runtime helpers.

The script owner tracks project/folder/source session, canonical/baseline document and observed revision, pending JSON/form edits, dirty state, edit/operation generation, validation fingerprint, save state, siblings and return target. `resolveDraft()` flushes pending cells and parses once; validate/save/replay/debug receive that exact resolved document/fingerprint. Bind validation to that fingerprint, observed content revision and applicable capability/policy/approval/secret-reference status revisions, with validity metadata. Edits or contextual revision changes invalidate older evidence; late validation cannot qualify a newer draft. Start rechecks current authorization, secret resolution, approval and placement without trusting a stale authoring result. Late responses cannot overwrite newer edits or a different project/resource. Summary-only saves GET canonical content before reopening; normalization is adopted only for the matching operation generation. Navigation/discard/cleanup use one dirty calculation and retain pending import siblings/approval drafts.

Expose server-minted strong opaque content revisions/ETags from existing script/scenario reads and writes, covering all executable/approval-relevant fields, bindings and authored metadata but excluding transient read/disclosure projections. Every writer advances the persisted revision atomically. New dashboard saves send one exact `If-Match` token; existing durable guarded methods compare it inside the same mutation CAS. A valid stale token returns `412 Precondition Failed`; malformed, weak, multi-token or wildcard input returns 400. Resolve project/resource ownership before evaluating a token so foreign resources remain 404. Secret reconciliation uses the matching canonical revision and current secret-reference authorization, never a stale masked projection. Conflicts retain the local draft and offer reload/diff/reapply, never automatic overwrite. User-authored script version is not a concurrency token. Older callers retain a documented compatibility window with no false stale-edit-protection claim; support an explicit strict project policy after migration, returning 428 for a missing precondition. New clients never use wildcard bypass. Add two-editor, concurrent secret-binding and legacy-unconditioned-write audit fixtures. Ordinary script/scenario creation and queued revise-copy add bounded project/actor/purpose-scoped idempotency. The same key and protected request fingerprint return the original canonical object/revision; changed input returns 409. Reuse existing import/approval receipts where present. Receipt creation and object mutation commit atomically; concurrent retries cannot duplicate groups/bindings or approval sessions. Guarantee retries for 24 hours, retain expiry/deletion tombstones under project retention and refuse automatic retry after expiry. A deleted original is not recreated by a receipt. Lost-response, two-request and browser-reload tests prove this behavior. Reuse existing endpoints, approval sessions and durable CAS rather than introducing a universal authoring execution endpoint.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#auth-1-two-shared-owners-and-revision-safety).

The scenario revision foundation supplies migration 38, database-issued authored-content nonces and coherent internal snapshots with conditioned storage operations. Affected SQLite/PostgreSQL and API normal/race checks plus generated-contract freshness pass in the [foundation record](qa/reviews/feature-completion-scenario-revision-2026-10-05/README.md). Explicit HTTP conditions and strict scenario policy configuration remain refused while the public authoring vertical is unavailable. Private publication/source dependency admission is delivered by the composed follow-up in the [delivery record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#auth-2-complete-lifecycle-parity-matrix); public ETags/projections and editor recovery remain open.

The private source-admission follow-up binds exact script incarnation, authored nonce/content, current publication presence and raw-byte claims inside the same SQLite/PostgreSQL transaction. Authoring fidelity preserves complete settings/bindings, historical portable defaults, ordered group/source identities and queued-copy provenance across the implemented entry points. [The composed review](archive/qa-reviews/INTEGRATION_2026-10.md#feature-completion-composed-review-2026-10-05) records focused and composed implementation qualification; outstanding work includes public scenario ETags/projections, editor recovery, ordinary-create idempotency, exhaustive lifecycle parity and adapter retirement. Public scenario precondition support remains explicitly unavailable; AUTH-1–AUTH-3 remain open.

The scenario owner contains root settings, ordered stable-identity groups, scripts, complete binding arrays, assignments, runtime overrides, goals/scoped transaction SLA, offsets/stages/traffic profile/observer roles and checkpoint metadata. Preserve absent/inherit, explicit false/zero/clear and legacy unknown. Effective values shown in UI do not become authored overrides. Rename/reorder/remove/add cannot misassign source group identity. Wizard/modal/new-run/queued-copy/preview projections share payload, timeline and estimate semantics. Execution revision remains a new queued copy; cleanup cannot mutate a running execution.

The [import-publication prerequisite](qa/reviews/feature-completion-import-publication-2026-10-05/README.md) now supplies a durable publication fence, exact raw-byte hash plus row presence, bounded reads and claims CAS reused by blank-ID repair. Real SQLite/PostgreSQL and affected API normal/race checks pass. The composed private admission slice now binds captured source identities and current publication facts within the same transaction; public scenario precondition support remains unavailable until its complete vertical is qualified.

### AUTH-2: Complete lifecycle parity matrix

For every row, prove ownership/destination, warnings/provenance, secret references, capability/policy gates, pending edits, dirty/navigation behavior, delayed-response protection, exact resolved payload and durable reopen.

| Entry point | Required proof |
| --- | --- |
| Direct JSON and structured editor | Invalid text stays editable; pending cells flush once; advanced fields, undo/selection and canonical normalization survive validate/save/replay/reopen |
| Template/component/clone | Independent snapshots, accurate capabilities and preserved references/provenance; protected credential migration explicit |
| Every import launcher | Acquisition-specific inspect → shared review → canonical edit → return → commit → stored reopen; source-specific warnings/blockers retained |
| Multi-script imports | Save one/all/subset and partial failure preserve committed IDs, siblings, source sessions and scenario association without silent loss |
| Recording | Preview-first review, selectors/runtime/assets, project/folder handoff, protected cleanup and save/reopen; agent/API identity changes cannot reset active edits |
| Classic replay/debug/browser replay | Same applicable static findings for equivalent draft/stored content; contextual runtime/install/policy/approval differences are separately classified; stop/pause ownership preserved |
| Scenario library wizard and compact modal | Template/reseed/clone, every binding, root/group overrides, goals/SLA, offsets/stages and inheritance round-trip through full reload |
| New run | Asynchronous metadata preserves operator edits; explicit scenario changes reseed; run-only overrides remain separate; estimate/timeline/create agree |
| Queued revision and execution overview | Complete immutable snapshot, correct group/source identity and revise-copy provenance; original run unchanged; preview matches effective workload |

Enumerate supported launchers from actual routes: Enterprise Archive, JMeter, Bruno/native requests, HAR, recorder, OpenAPI/RAML/Postman/WSDL/AsyncAPI, DevWeb/k6/CloudTest, sample/Java HTTP/Java-family, broker configuration, Event Hubs Capture and supported YAML selection. Exact source-runtime certification is outside cleanup; unsupported imports remain governed drafts. Expand parity fixtures across valid/invalid HTTP, browser engine/profile, gRPC modes, Kafka and Event Hubs modes, each JMS provider, nested control flow, selectors, templates/secrets and governed runtimes. Static findings match; contextual authorization/security/runtime findings retain distinct causes.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#auth-2-complete-lifecycle-parity-matrix).

The matrix found and fixed two parity gaps. `dsl.Parse`/`ParseFile` reported only a script's first static finding, while the validation routes report all of them; the parser now reports every finding (as a `dsl.ValidationError`, so `apexctl script validate` lists one issue per finding). Debug and replay of a draft judged the masked copy a session executes, so a credential-bearing JVM argument read as `must_start_with_dash` there and `sensitive_property` everywhere else; they now judge the copy they execute with the submitted JVM arguments in place of their sentinels, so the finding matches while a recorded resource URL whose credential that copy masks is still refused as unexecutable (a contextual finding, covered by `TestTransientReplayRefusesAMaskedRecordedResourceURL`).

Deviations:
- Contextual runtime, install, policy and approval outcomes are left to the existing replay and browser tests, not given new cause-code fixtures; replay merges its review-gate issues into the same list without a cause code.
- The single-script import commits (`openapi/commit`, `postman/save`, `asyncapi/commit`) validate the finalized copy with the DSL validator (`400`) and `bruno/commit` with its native check (`422`); neither is held to the persisted-copy check.
- The import saves' persisted-copy validation is exercised directly, not through each save's HTTP request (which needs a live import session); each handler is held to it by a source check. A credential-bearing Java Vuser import is stored only through the authoring-session approval of its import, so the stored routes are not driven for it; that approval stores the copy `preparePersistentImportedLibraryScript` prepares, and the stored routes judge it with the same stored validator the import-save check runs.
- One fixture each for gRPC (unary) and Kafka (produce); no governed-runtime fixture.
- The AUTH-2 Playwright proofs show that each entry point opens, saves and reopens what it should; they are not mapped to every AUTH-2 property (ownership, provenance, secret references, policy gates, pending edits, dirty navigation, delayed responses, exact payloads). The recording proof mocks the recorder service, and only a starter template is covered under Template/component/clone (no component, clone or protected-credential migration).
- Script-producing launchers outside `/api/v1/scripts/import/` (browser recorder and recorder conversions, library import) are not inventoried.


### AUTH-3: Adapter retirement inventory

| Current seam | Decision and removal gate |
| --- | --- |
| `aliasImportState` / route selection | Canonical launcher descriptors internally; retain tested old deep-link translation at the route boundary |
| `TestHub` flat `ScenarioFormState` / local normalization | Replace with scenario-owner projections after save/estimate/timeline round-trip tests |
| `scenarioFormModel` runtime reconstruction | Remove duplicate reconstruction after all dialogs edit canonical overrides; preserve advanced fields and tri-state semantics |
| `newTestRunModel` effective/override composition | Keep legitimate run-only projection; share workload semantics and remove only proven duplicated calculations |
| `testExecutionScenarioModel` preview calculation | Replace with shared workload/timeline projection after offset/stage/goal/baseline parity |
| `CompatibleDebugStartRequest` | Remove after generated/domain request parity and serialization freshness tests |
| Versioned draft/import metadata readers | Retain supported user-data read migrations; these are not disposable response adapters |
| Domain service adapters preserving authorization/CAS | Retain purposeful boundaries; naming alone does not justify removal |

No broad deletion precedes caller inventory, fixtures and passing parity. Library filters/selection and project refresh remain page concerns; mutable session orchestration moves to its shared owner. Cleanup ends when one canonical owner resolves each mutation/validation/payload and every supported route/old deep link has a tested destination.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#auth-3-adapter-retirement-inventory).

  In every leave prompt, Save changes now goes ahead without saving when the work was saved, or its editor closed, while the prompt was open. In the script editor, Save changes also waits for a save that is still running. If edits were made after that save started, the prompt stays open and says so. After a create, Save is off until the page opens the saved script, so a second create cannot happen. Edits made while the save ran open with the saved script as a draft to review; edits made after it finished are not kept.

  The Save button follows one rule, `scriptSaveAvailability`, beside the session. The ratchet fails if `ScriptEditor` keeps its own baseline, initial-save flag, guard, dirty comparison or save rule again.

  Leaving for a converted archive review changed in three ways:
  - **The return button now belongs to the editor.** Leaving an edited converted draft for its archive review asks first; before, the edits were discarded without a prompt.
  - **The review's own guard is kept.** The review stays open behind the editor, so going back never prompts for it.
  - **Saving a converted draft goes back to the review.** The review records the draft as saved and moves on to the next draft. A validation review becomes a conversion review once a draft is saved, as with the review's own saves, so its own save cannot save that draft again. Its remaining drafts are converted again first; the options cannot be changed until that is done. The editor cannot be opened from the review while a validate, convert or save batch runs, while a Java dependency uploads, or while an earlier save of the archive is unconfirmed; its buttons stay, disabled, and say why. Before, the saved script opened and the review closed, dropping its other unsaved drafts without a prompt.
    - A request to set up the script's secrets is kept: the secrets dialog opens in the script library, as for any new script. When several saved scripts asked for it, their dialogs open one after another.
    - If the draft was edited while the save was in flight, the saved script opens instead, as before. The review still records the save, but it closes, and its other unsaved drafts are dropped without a prompt.

Deviations:
- TestHub keeps a form as its editing view (its groups, name and other scalar fields), now holding only canonical values; it does not hold a `ScenarioDraft` itself.
- The wizard's model chart keeps its own chart builder (series metadata, TPS stages, draft placeholders); it shares stage semantics but draws neither baselines nor start offsets.
- A queued run's summary carries no traffic profile, so its preview draws profile-mapped groups from their ramps.
- `NewTestPage`'s group edit draft remains a run-only projection.
- The Scripts page keeps the routing state in `useScriptsAuthoringState`: the open sub-view and the return target it reopens. An archive import's pending sibling drafts live in the importer, which stays mounted behind the editor. The script session decides only whether the editor may leave.
- Inside the scenario editor's "Create prerequisite script", saving a converted draft returns the script to the scenario group and closes the embedded Scripts page, as any save from the script editor there does. The archive review closes with it, and its other unsaved drafts are dropped without a prompt.
- A converted Java Vuser draft is queued for approval, not saved to the library. Save changes on the way back queues it and returns to the review. The review still lists the draft as unsaved, and no notice says it was queued.
- Validation is not bound to capability, policy or approval revisions, which have no client-visible revision yet.

## 11. Proposed public contracts and persistence

Extend existing generated API contracts rather than copying endpoint catalogs into prose. Proposed additions must be marked as new until implemented:

| Surface | Contract change |
| --- | --- |
| System/analysis/protocol capabilities | Versioned operations, metrics/dimensions, limits, helper/native prerequisites and independent acceptance status |
| Existing native run/dataset series and comparison operations | Optional query/catalog version, typed derivation, bounded tuple grouping/filtering, quality/coverage and immutable input revisions |
| Existing run/archive/detail/report resources | Versioned messaging, browser document/resource/artifact/finding and telemetry policy/window projections |
| Script/scenario GET/create/update | Opaque content revision/ETag and conditional existing PUT support |
| Existing baseline comparisons | Telemetry policy compatibility, time-weighted evidence and separate optional acceptance verdict |
| Existing project configuration | Revisioned browser intelligence/journey settings and telemetry acceptance policy, with existing project authorization conventions |
| New bounded evaluation jobs | Proposed `POST/GET /api/v1/analysis/evaluations`, `GET /api/v1/analysis/evaluations/{id}` and `DELETE /api/v1/analysis/evaluations/{id}` for typed create/list/progress/cancel with retained audit. Kinds are browser intelligence, telemetry acceptance, native derived analysis and bounded export; no SQL/helper execution |

Authoritative metadata ownership is fixed as follows; implementation may factor code internally but cannot create competing sources of truth:

| Resource | Owner, identity and lifecycle |
| --- | --- |
| Browser/journey/telemetry policy definitions | Extend append-only `analysis_definitions` kinds with browser policy, journey policy, telemetry policy and derived-operation definition. Project + ID + revision is immutable; scenario/run snapshots pin revisions. Existing system telemetry defaults resolve into the run snapshot. Soft deletion hides latest policy while retained referenced revisions remain auditable. |
| Panels, mappings and comparisons | Existing dashboard/analysis definition stores remain authoritative; add typed versioned fields and CAS, not another widget store. |
| Script/scenario content revision | Add persisted opaque revision fields to existing script/scenario records; all writers use their guarded storage mutation. User-authored version and timestamps are not sufficient concurrency identity. |
| Authoring create/revise receipts | Existing protected import/approval receipts for those flows; new `authoring_mutation_receipts` for ordinary creates/revise, uniquely keyed by project/actor/purpose/key with protected keyed fingerprint, original resource/revision, expiry and deletion tombstone. No raw credentials in receipt payloads. |
| Findings/evaluation results | New `feature_evaluation_jobs` metadata and immutable versioned result artifacts. Findings are artifact records keyed by job/rule/cohort/window/input fingerprint; live projections are bounded caches, not an independent durable finding database. Incident state references finding identity. |
| Acceptance records | New append-only `feature_acceptance_records` containing profile/provider/platform/feature, commit and binary hashes, suite result and sanitized artifact hashes. Release claim promotion resolves these records; evidence deletion cannot silently retain a supported claim. |
| Runtime reservations | Existing preparation/assignment reservations and ownership fences extended with admitted processor/worker/browser/helper/native demand. Release exactly once through fenced cleanup, retaining audit. |
| Source artifact receipts/publication | Existing archive finalization/import job owners plus new durable `run_evidence_receipts` for browser/helper evidence (run/execution/source generation + sequence + hash). Acknowledge after object and receipt durability; accepted unpublished receipts drain at finalization. Existing durable cleanup handles late writers/tombstones. |

Evaluation jobs persist project/actor, kind, idempotency key, typed operation/definition revision, immutable input artifact/run-watermark/policy/catalog versions, state/revision, lease owner/generation/expiry, progress, result hashes and typed failure. States are queued → running → publishing → ready, with failed/cancelled/expired alternatives. Publication CAS checks job revision, ownership generation and project/input deletion fences after artifacts are durable. Cancellation revokes the generation; late results are invisible and queued for cleanup. Viewer `results:read` may create/read authorized read-only evaluations within quota and cancel their own jobs. Cancelling another actor’s job or hiding/deleting shared saved results requires editor permission; system-owned evaluation control uses the existing trusted controller identity. Private completed results may be hidden by their creator. Archived/pinned finding artifacts remain retained under their reference/retention policy. Source/project authorization is rechecked on every operation; possession of a job ID is insufficient. Saving/changing policies needs editor permission. Default bounds are one running heavy job/controller, 20 queued jobs/project, 100 listed jobs/page and existing bounded query bodies/deadlines. DELETE cancels an unfinished job and `DELETE ?hide=true` hides a finished one; both are idempotent, retain audit and never delete input runs/datasets. Referenced result retention follows the run/project artifact policy. Query/read/cancel/delete behavior remains available when new-job feature flags are disabled.

Dated delivery evidence: [implementation record](archive/plans/FEATURE_COMPLETION_DELIVERY_2026-10-19.md#11-proposed-public-contracts-and-persistence).

Define migrations, indices, retention and deletion effects together. Explicitly project old data into `legacy/unavailable` semantics; no guessed backfills. Hash normalized source and result provenance so reports can be reproduced after policy/library upgrades.

## 12. Delivery sequence and exit gates

The engineering assessment is an additive multi-release program. The largest dependency risks are real provider semantics/helper containment, corrected browser metric finality, live tuple cardinality and native memory isolation; authoring cleanup also crosses shared workflows and is not a cosmetic refactor. Source fixtures, controlled broker/cloud identities, approved native dependencies and a benchmark host with adequate scratch are execution prerequisites. Existing implementation reduces foundation work, but does not reduce those qualification requirements. Estimate releases from the named requirement/test inventory after staffing is chosen; this document makes no unsupported delivery-date promise.

Every release includes runtime/API/dashboard/CLI/archive/report/docs/tests for its scope and an independent review/fix cycle. Commit release work only after required checks pass; external acceptance not yet available remains visibly pending. A feature is not complete merely because this specification schedules its missing gate.

| Release | Deliverable | Dependencies and exit evidence |
| --- | --- | --- |
| R0 | Truth/compatibility inventory and outstanding verification | Final API counter/race and PostgreSQL availability gates; reconcile roadmap/support/ledger; golden old/new evidence contracts |
| R1 | Shared policy, metric/capability and publication contracts | EH/MSG/BE/TEL/NA semantics, bounded sizes and mixed-peer rejection; nullable migrations and restart/HA/deletion tests |
| R2 | Event Hubs ownership diagnostics/admission and common messaging management/evidence | Local/emulator cases plus protected Azure recovery/epoch matrix; live/archive/report/CLI/native parity |
| R3 | Kafka, genuine JMS, Service Bus semantics and binding fidelity | Controlled Kafka/Artemis plus Service Bus emulator/live tests; governed helper, transaction/settlement and secret/egress/zero-traffic evidence |
| R4 | Browser document/resource metrics and HAR publication | Corrected metric fixtures, Chromium/WebKit fidelity, local/distributed artifact lifecycle and both-profile resource/HAR workflows |
| R5 | Complete telemetry reporting/verdicts and native analytics | TEL/NA policy/coverage/derived/joint/partition requirements; child resource controls, platform packaging and measured reference load |
| R6 | Authoring owners/revision fences and cleanup | Exhaustive lifecycle matrix, stale editor/async conflict tests, full browser cycle and verified caller retirement |
| R7 | Browser intelligence and journey/interaction views | Qualified R4 metrics and R5 analysis; fixture-grounded scores/anomalies/heatmaps, evidence drill-down and exports |
| R8 | Solace SMF and strict JMeter qualification | Native image/broker evidence; pinned source-session conformance, atomic repair and capability promotion only after all gates |
| R9 | Integrated qualification and support promotion | Combined scenario below, complete native/platform/provider matrix and final source/test review |
| R10 | IBM MQ native, very low priority last release | MQ native image/client licensing and broker conformance; does not delay R0–R9 |

R3, R4 and the contract-backed authoring work can develop independently after R1. Their release gates remain independent. R7 cannot use legacy unqualified metrics as a shortcut. R8 has separate SMF and strict-JMeter sub-gates; passing one does not certify the other. R0–R9 close the primary requested program; native MQ remains scheduled last under the explicit low-priority instruction and must be shown open until R10 passes.

## 13. Validation and final acceptance

| Gate | Required evidence |
| --- | --- |
| Security/authorization | Cross-project read/write/download/cache isolation, redacted previews/reports/HAR/findings, arbitrary path/SQL/helper rejection, approved identities/destinations, expiry and quota attacks |
| Compatibility | Old archives/clients/panels/mappings, mixed versions, missing/zero/negative/overflow evidence, preserved policy/history, denied unsupported helpers/runtime combinations |
| Messaging | Controlled real broker matrix, independent accepted-ID/offset/queue counters, rollback/commit/rebalance/ownership/checkpoint failure, stale management coverage and scoped SLA verdicts |
| Browser | Independent page/resource/interaction fixtures, standards-correct document metrics, both engines' differences, HAR close/crash/redaction/limits, layout-version isolation and no extra target interactions |
| Telemetry/analytics | Time unions, counter epoch/reset/lower bounds, skew/cadence/policy differences, percentile distribution reconciliation, typed derivations, partition/projection proof, resource cancellation and HA publication fences |
| Authoring | Full fixture/entry-point matrix, two-editor conflicts, pending JSON/forms, async races, siblings/approval sessions, bindings/SLA/offset provenance and route/legacy checkpoint compatibility |
| Persistence | SQLite and actual PostgreSQL migrations/integration, job/definition CAS, concurrent deletion/publication and controller restart/takeover |
| Repository | Affected Go tests during development; final `go test ./...`; affected race and real-adapter build-tag suites; generated DSL/API/TypeScript freshness; documentation checks |
| Dashboard | Typecheck, lint and meaningful units; affected Playwright flows in dev-admin/local-auth; full `npm run test:cycle` for shared workflow changes with stable source/embedded assets |
| Native release/performance | Actual fresh packaged binaries on four native platform targets; reproducible declared 1 GiB and browser/telemetry hardware matrices with immutable resource/latency reports |

Protected provider jobs use approved disposable resources with declared test identities and automated cleanup. Missing required credentials/infrastructure fails or records pending qualification; ordinary developer suites may skip opt-in tests but cannot promote support. Record provider versions/tier, dependency hashes, commit, workload, timings, CPU/RSS/spill, accepted/rejected/missing counts and redacted evidence. Existing archive disk-headroom guards remain enforced; insufficient build/scratch storage is an environment blocker, not a reason to weaken safety or claim a pass. Shared build-cache cleanup requires its existing authorization boundary.

Integrated completion: prepare an admitted messaging/browser run without target traffic; Start with bound management observations; exercise controlled lag, settlement and browser slowdown; inspect qualified live findings; terminate/recover a worker and reconcile retained evidence; reopen archives/HAR/resources; import an external partitioned dataset and preserve its dimensions; compare/correlate it with the explicitly associated managed run; export both application and telemetry verdicts; revise the authored scenario through another entry point without field loss; delete the project/dataset and prove late writers cannot resurrect artifacts.

Completion is recorded per requirement ID and provider/platform, with no unresolved actionable review findings and no required gate represented by a skip. Remaining MQ, source-fixture, live-provider or operator-waived scale items stay visible in the roadmap/capability ledger. The 10 GB waiver is never converted into a passing result.

## 14. Implementation map and source references

| Area | Existing owners and extension seams |
| --- | --- |
| Event Hubs | `pkg/protocol/eventhubs/{processor_settings,processor_real,processor_blob_real,exclusive_settings,exclusive_real}.go`; DSL/engine config and generator admission |
| Messaging | `pkg/protocol/messaging_diagnostics.go`, `pkg/protocol/messaging/`, JMS adapter capabilities/provider adapters, Kafka real client, AsyncAPI converter, JMeter compatibility, existing integration workflow |
| Browser | `pkg/protocol/browser/{runtime_context,har_capture}.go`, browser client metric collectors, protocol evidence/result types, engine/archive ingestion, `BrowserResourceEvidence` and browser breakdown models |
| Telemetry | `pkg/api/{telemetry,baseline_telemetry,distributed_stream,distributed_metrics,distributed_archive,persistence}.go`, storage history and report section registry |
| Native analytics | `pkg/analysis/{points,parquet_mapping,correlation,buckets}.go`, `pkg/analysis/duckdb`, `pkg/storage/{native_evidence,analysis_definitions,dashboard_definitions}.go`, native/analysis API handlers and desktop packaging |
| Authoring | Shared script hooks/shell/surface/editor, scenario reducer/payload/form/run/preview models, script/scenario handlers and durable guarded stores |

Repository baseline sources: [roadmap](ROADMAP.md), [analysis workflows](ANALYSIS_WORKFLOWS.md), [protocol support](PROTOCOL_SUPPORT.md), [Event Hubs guide](EVENT_HUBS_HOWTO.md), [import fidelity](IMPORT_FIDELITY_AND_REPAIRS.md), [latest implementation/review record](qa/reviews/evidence-expansion-2026-10-02/REVIEW.md) and [canonical CloudTest ledger](../pkg/api/cloudtest_parity_catalog.json). Those sources describe existing behavior; requirements above describe planned completion. Update stale statements as each accepted release lands, keeping implementation availability separate from qualification.
