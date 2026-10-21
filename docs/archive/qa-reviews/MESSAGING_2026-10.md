# Completed messaging implementation reviews — October 2026

Status: Historical. Consolidated on 2026-10-19. These are completed, scoped implementation reviews, not closure of their parent requirements. Original dates, failed checks, pending acceptance and qualification limits remain below. Evidence files remain at their original paths. Use the [review index](../../qa/reviews/README.md) for outstanding work and other records.

## Records
- [Kafka management reader review and qualification](#feature-completion-kafka-management-2026-10-03)
- [Kafka owned-seek review and qualification](#feature-completion-kafka-seek-2026-10-05)
- [Kafka fresh-source LSO and transaction qualification — 2026-10-05](#feature-completion-kafka-transactions-2026-10-05)
- [Event Hubs processor capacity review — 2026-10-03](#feature-completion-eh-capacity-2026-10-03)
- [Event Hubs acknowledged pause review — 2026-10-03](#feature-completion-eh-pause-2026-10-03)
- [Event Hubs processor progress review — 2026-10-03](#feature-completion-eh-progress-2026-10-03)
- [Service Bus management review](#feature-completion-servicebus-management-2026-10-04)
- [Solace management review](#feature-completion-solace-management-2026-10-04)

<!-- Original record: docs/qa/reviews/feature-completion-kafka-management-2026-10-03/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-kafka-management-2026-10-03/) -->
<a id="feature-completion-kafka-management-2026-10-03"></a>

## Kafka management reader review and qualification

Status: implementation, independent review and repository/targeted acceptance complete; final documentation check recorded below. This slice does not close the feature-completion program.

The production `kafka-offsets` observer reads exact approved topic/partition/group inventories over explicitly pinned TLS and SASL PLAIN/SCRAM identities. Numeric routes and CA trust are loaded immutably without target traffic during construction/readiness. Fixed versioned metadata, coordinator and offset requests retain exact next positions, presence and the oldest component capture time. Request and physical-connection ownership stays charged until actual cleanup is proven. Capability-gated source authoring preserves saved approval separately from unsaved edits and revision conflicts; CLI source update/delete uses compare-and-swap.

<a id="feature-completion-kafka-management-2026-10-03--review-fixes-and-repeat-review"></a>

### Review, fixes and repeat review

Independent source reviews are clean after fixing:

- The signed authorization deadline can precede the scheduling deadline. Admission and actual I/O use the earliest signed, parent and operation deadline, including queue waiting.
- A context timer can be delivered after its deadline. Shared admission, Kafka I/O, observer qualification and scheduler completion now check the actual deadline, with delayed-timer queue/callback/publication and real TCP/peer-closure regressions.
- SASL session lifetime starts at the last authentication request, so a delayed response cannot extend authority. Close errors retain first-owner/proof semantics and unresolved physical charge.
- Saved approval cannot cross a different source, project, pack or evidence kind. Narrow layouts contain the immutable approval. Help text has separate accessible descriptions; the original browser label failure is retained and both corrected profiles pass.
- Highest-valid stored source revisions remain deletable; updates retain increment bounds. CLI regression counters are atomic.
- The genuine fixture counts sockets before dispatch, discharges only after physical closure, retains test process-group authority until reap and verifies each owned Docker resource and protected scratch independently. It copies only bounded protected required inputs and preserves pinned image/trust constraints.
- Cold setup probes use bounded numeric listener readiness and short provider deadlines without retrying an unproven timed-out remote command. Independent setup production uses finite per-record waits. Transaction setup retries only bounded control requests, with no production-reader retries or weakened oracle.
- Fixture reports and test logs cannot overwrite retained existing evidence, including dangling symlinks; rejected destinations perform no scratch/provider work.

<a id="feature-completion-kafka-management-2026-10-03--executed-checks"></a>

### Executed checks

| Check | Result |
|---|---|
| Complete collector normal suite after final deadline fix | Pass, 9.719 seconds |
| Targeted Management/Kafka race suite after final deadline fix | Pass, 11.379 seconds |
| Complete CLI suite; revision-boundary race | Pass, 15.595 / 2.813 seconds |
| Final dashboard typecheck, lint, embedded build | Pass |
| Final focused source units | 9 passed |
| Initial full dashboard cycle | 459 files / 4,774 units passed; dev-admin 196 passed, 4 conditional skips and one Kafka label failure; local-auth 116 passed, 1 conditional skip and the same Kafka failure |
| Corrected final source workflow | dev-admin 1 passed, 2.8 seconds; local-auth 1 passed, 9.8 seconds; no retries |
| Final fixture cleanup/readiness/provenance units | 11 passed |
| Genuine Apache Kafka 3.9.2 fixture | Pass, 1.391-second Go suite / 28.692-second harness |
| Full Go repository gate | Pass; API suite 1,280.330 seconds, collector 9.601 seconds |
| Final documentation check | Pass, 107 active Markdown documents |

The first full Go attempt was interrupted before completion. The final restart uses the same task cache and serialized `-p 1` lane with `GOMAXPROCS=2` and an 8 GiB Go memory setting; the earlier 4 GiB compile profile showed GC pressure above its setting. This build/test adjustment does not qualify analytics resource controls or a native release. The final complete run exited zero.

The full dashboard command remains recorded as failed at its original snapshot; subsequent final focused execution covers the correction. Source/approval viewport screenshots from both profiles are retained and visually inspected at desktop and 375 pixels. Browser authoring uses scoped API fixtures; it does not certify a broker. Copied text logs normalize tab indentation and trim trailing whitespace; SHA-256 hashes of the raw originals are in `raw-log-hashes-final.txt`.

<a id="feature-completion-kafka-management-2026-10-03--genuine-broker-result-and-limits"></a>

### Genuine broker result and limits

`actual-broker-final.json` and the matching UUID-specific test/harness logs identify the official pinned 3.9.2 index and Linux arm64 platform digest, single KRaft topology, 1 CPU / 1,536 MiB container ceiling, guarded data directory, physical test reap and verified removal of all owned resources/scratch. All three SASL profiles, denied group, wrong cluster, wrong CA, zero construction/readiness target traffic, fixed read-only frame audit and physical ownership checks pass.

The transaction visibility oracle passes: before the transaction HWM/LSO/committed-next are 4/4/1; while open they are 5/4/1; after abort they are 6/6/1. The reader never labels physical replica log end as HWM, invents an offset increment or measures lag by consuming. Failed setup/cleanup attempts remain retained; a reused-basename mismatched report/log pair is explicitly excluded from qualification, and final evidence uses a fresh UUID with overwrite protection.

This result does not certify full consume-transform-produce atomicity, producer fencing, broker restart/multi-broker failover, newer releases, hosted/protected brokers or Event Hubs Kafka endpoints. Those remain explicit program work. Service Bus/Solace readers and the full MSG-2 exit are separate slices. Only the 10 GB benchmark is waived; native 1 GiB/platform and protected provider gates remain required. Build-cache cleanup stays deferred while dependent program builds/tests still need the caches.

<!-- Original record: docs/qa/reviews/feature-completion-kafka-seek-2026-10-05/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-kafka-seek-2026-10-05/) -->
<a id="feature-completion-kafka-seek-2026-10-05"></a>

## Kafka owned-seek review and qualification

The genuine Apache Kafka 3.9.2 single-broker fixture passed all four owned-seek cases on 2026-10-05. Final default/tagged and race suites and wrapper regressions pass; the documentation check is recorded below. This slice qualifies read-uncommitted seek with TLS and SASL PLAIN; it does not close MSG-3 or the feature-completion program.

<a id="feature-completion-kafka-seek-2026-10-05--implemented-behavior"></a>

### Implemented behavior

Real `kafka_seek` accepts earliest, latest and exact nonnegative offsets only for the configured consumer group and the complete current owned/previously-polled partition inventory. Polling proof is bounded and assignment-generation scoped, independent of whether the SDK's dirty cursor equals its committed cursor. Successful seek purges buffered delivery and explicit commit tracking; it writes no committed offsets. A real ownership-generation change during offset resolution refuses the seek before cursor mutation. Real `kafka_commit` refuses a different action group before broker I/O or commit telemetry; literal DSL checks and resolved template checks preserve this exact identity fence.

<a id="feature-completion-kafka-seek-2026-10-05--review-fixes-and-repeated-verification"></a>

### Review, fixes and repeated verification

Independent review and regression coverage addressed:

- SDK `UncommittedOffsets` omits dirty-equals-committed cursors; independent delivered/polled proof preserves valid consecutive earliest/exact seeks while refusing unpolled or revoked partitions.
- Ordinary production acknowledgements do not set request/reply's private `sent` flag. Seed admission requires present finite nonnegative acknowledgement timing and exact topic/partition/offset/timestamp; consumed unique markers independently prove actual record contents.
- The oracle's 1 MiB broker-read limit requires a matching SDK fetch-size configuration even though it never configures a consumer. Only Kafka coordinator codes 14, 15 and 16 receive bounded retries; authentication, unknown and routing errors remain terminal. The final actual fixture observed one code-15 retry followed by a successful OffsetFetch.
- A serial proxy held the primary's shared ListOffsets/heartbeat connection and prevented genuine rebalance notification. The bounded pipeline now reads ahead, preserves exact response-write order, holds a genuine primary/group Heartbeat response with code 27, obtains the subsequent real ListOffsets response, and releases the heartbeat while the lookup remains blocked. Generation loss is proven during the hold; actual disjoint peer ownership is required after lookup release/refusal.
- Both holds require the exact primary client identity. Peer, malformed header, wrong topic, duplicate/missing partitions and wrong selector controls cannot consume the primary proof.
- Queue admission is limited to eight in-flight requests per connection; frame size, total bytes, connections and response age remain bounded. An already-expired release cannot extend the eight-second hold budget. Owned cancellation actively closes raw TLS sockets and explicitly joins the cancellation watcher, pipeline workers, peer and seek operations on failure paths.
- Real TLS lifecycle controls retain active trust failures and exclude owned-close cancellation from failure receipts. The independent Describe-only oracle never joins a group, consumes, produces, commits, or uses setup-control routes.

Offline synthetic wire tests prove only proxy ordering, admission, bounds and cancellation. They supply no provider offsets, assignments or message evidence to the genuine qualification test.

<a id="feature-completion-kafka-seek-2026-10-05--executed-evidence"></a>

### Executed evidence

| Check | Result |
|---|---|
| Genuine Kafka owned-seek fixture | Pass: Go suite 2.487 seconds; complete harness 25.635 seconds |
| Complete default Kafka and DSL suites | Pass: Kafka 15.440 / DSL 0.325 seconds; receipt (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/final-default-kafka-dsl-20261005.json`) |
| Complete `kafka_real` Kafka and DSL suites | Pass: Kafka 15.509 / DSL 0.377 seconds; receipt (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/final-real-kafka-dsl-20261005.json`) |
| Complete default Kafka and DSL race suites | Pass: Kafka 16.751 / DSL 1.638 seconds; receipt (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/final-default-race-kafka-dsl-20261005.json`) |
| Complete `kafka_real` Kafka and DSL race suites | Pass: Kafka 16.797 / DSL 1.664 seconds; receipt (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/final-real-race-kafka-dsl-20261005.json`) |
| `kafka_real,kafka_seek_integration` owned helper race controls | Pass: 1.750 seconds; receipt (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/final-owned-helpers-race-20261005.json`) |
| Python fixture/wrapper regressions | Pass: 18 tests; receipt (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/final-python-wrapper-20261005.json`) |
| Documentation checks | Pass: 128 active Markdown files; receipt (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/final-docs-20261005.json`) |

The final actual report (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/actual-broker-repeat-5.json`) and matching test log (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/actual-broker-repeat-5.tests.txt`) record the pinned image/platform provenance, Linux arm64 single KRaft broker, 1 CPU/1,536 MiB container ceiling, guarded data directory, exited/reaped test group and verified removal of owned Docker resources and scratch. The execution proxy's peak in-flight request count was two. The independent oracle observed only permitted read-only metadata/coordinator/offset/authentication APIs.

The four genuine cases cover multi-partition earliest/latest/exact seek with following SDK-buffered and real adapter-pending records, wholly/partially unpolled and unowned refusal, configured versus other/denied action groups, and broker-driven revocation during held offset lookup. Initial committed next positions were 1 on every partition; later explicit commit baselines were 9 and 10. Seeks and refusals retained those actual broker positions. The final revocation changed generation 22 to 26, left partitions 0/1 with the primary and assigned partition 2 to the peer. No assignment callback or broker response was fabricated.

Earlier failed attempts are retained: oracle construction (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/actual-broker-initial.json`), coordinator startup (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/actual-broker-repeat.json`), seed acknowledgement (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/actual-broker-repeat-2.json`), joint barrier (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/actual-broker-repeat-3.json`), and diagnostic heartbeat blocking (`../../qa/reviews/feature-completion-kafka-seek-2026-10-05/evidence/actual-broker-repeat-4.json`), each with its matching `.tests.txt` log. Copied earlier tool outputs remain historical context and do not replace final raw gate receipts. Final gate logs are preserved byte-for-byte with SHA-256, command, source-file hashes, timing and pinned environment in `evidence/final-*.json`.

<a id="feature-completion-kafka-seek-2026-10-05--remaining-qualification"></a>

### Remaining qualification

Read-committed last-stable-offset seek, consume-transform-produce atomicity, producer fencing, transaction crash/restart, multi-broker failover, other broker versions, other authentication profiles, hosted providers and Event Hubs Kafka endpoints remain open. Prior Kafka management-reader PLAIN/SCRAM qualification is separate and does not qualify these execution cases. The full repository gate for the composed program remains a separate merge gate. Shared build caches remain available while dependent verification continues.

<!-- Original record: docs/qa/reviews/feature-completion-kafka-transactions-2026-10-05/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-kafka-transactions-2026-10-05/) -->
<a id="feature-completion-kafka-transactions-2026-10-05"></a>

## Kafka fresh-source LSO and transaction qualification — 2026-10-05

This bounded MSG-3 slice fixes runtime isolation admission and truthful teardown accounting and qualifies a separate genuine Apache Kafka 3.9.2 single-KRaft-broker TLS/SASL PLAIN profile. It does not close all Kafka semantics or the feature-completion program. All affected checks below pass and final independent source review is clean. The full composed repository gate is required before integration.

<a id="feature-completion-kafka-transactions-2026-10-05--delivered-behavior"></a>

### Delivered behavior

- Direct runtime initialization rejects unsupported, non-string and null explicit isolation values before constructing any SDK/mock client. Empty isolation retains the existing default; supported values are normalized.
- Teardown always attempts physical client cleanup, retains both abort and cleanup errors, counts only confirmed rollback, clears buffered output and makes an uncertain transaction outcome terminal. Normal and real-lazy regressions cover admission, error joining, namespace release and repeated cleanup.
- Latest seek in `read_committed` uses the actual last-stable next position and does not commit group offsets. Owned/polled admission remains required; seek stays unavailable inside consumer-transaction mode.
- Consumer transactions use the existing pinned SDK transaction session, a single input topic, an approved group, `read_committed` and a fresh per-VUser transactional identity. Commit reconciles visible output with input committed-next; abort discards locally buffered output and the real SDK rewinds input for redelivery.
- A separate genuine producer initializes a newer epoch for the actual runtime-resolved ID after a successful warmup transaction. The stale runtime must fail with the pinned `PRODUCER_FENCED` or `INVALID_PRODUCER_EPOCH` signature, commit no new input, expose no failed output to `read_committed`, and refuse further execution.

<a id="feature-completion-kafka-transactions-2026-10-05--genuine-acceptance-and-independent-evidence"></a>

### Genuine acceptance and independent evidence

The new explicit `kafka_real,kafka_transaction_integration` tag fails when its protected fixture settings are missing. The wrapper `scripts/test-kafka-transactions.sh --report <unused-report-path>` requires the parent and all three subtest PASS markers; missing/skipped cases cannot qualify.

| Case | Actual provider evidence |
| --- | --- |
| `read_committed_latest_lso` | Independent fixed ListOffsets reads observe high-watermark greater than LSO during a real open transaction. After production latest seek, committing that transaction delivers its record at the former LSO (offset 1). A second broker-aborted record at offset 3 is skipped to the following acknowledged record at offset 5. Group commits remain unchanged. |
| `consume_transform_commit_abort` | Production public Begin/Consume/Produce/Commit actions atomically reconcile output and committed input, with commit count 1. Explicit abort count 1 retains committed-next and actual SDK redelivery of the same input. Ordinary commit and seek are refused in this mode. Output is buffered in Go until commit: this abort case proves buffered-output discard plus broker-backed input rewind, rather than claiming the buffered output was sent then broker-aborted. |
| `stale_producer_fencing` | Actual independent producer epoch takeover fences the old runtime, with a fixed Kafka fencing error and unchanged committed input. Ordered `read_committed` output scans exclude stale failed output and the separately broker-aborted contender record. |

The Describe-only oracle never consumes, joins a group or mutates offsets. It uses exact approved topic/group/partition inventory, numeric TLS routes, fixed ListOffsets isolation 0/1 and stable OffsetFetch. The independently authenticated output reader has only output-topic Read/Describe, no group configuration or join, and scans from the start to a separately acknowledged final barrier. Absence is never inferred from an empty poll or a sleep. Fixture writers have only the approved Write/Describe topics and short transactional-ID prefix; execution/transaction grants do not broaden management authority.

The previously qualified [four-case owned-seek profile](#feature-completion-kafka-seek-2026-10-05) remains separate. Shared transport admits only fixed transaction APIs 24/25/26/28 when explicitly enabled by this new fixture; its default seek authority is unchanged and tested. Offline constructor, wire-role, scope, ordering, retry and race tests qualify harness controls; they do not replace genuine broker acceptance.

<a id="feature-completion-kafka-transactions-2026-10-05--retained-attempts-and-verification"></a>

### Retained attempts and verification

- Initial failed receipt (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/actual-broker-initial.json`) and raw sanitized test output (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/actual-broker-initial.tests.txt`): LSO passed; transaction reconciliation failed because the new oracle did not request stable transactional offsets. Cleanup was verified.
- First successful repeat (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/actual-broker-repeat-1.json`) and test output (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/actual-broker-repeat-1.tests.txt`): all three cases passed. The broker returned actual `UNSTABLE_OFFSET_COMMIT` code 88 twice before stable offsets were available, establishing the oracle correction. SDK/kmsg semantics and bounded retry regressions retain immediate refusal for authentication, routing, unknown and cancellation failures.
- Final genuine receipt after explicit observer membership defaults (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/actual-broker-final-repeat.json`) and test output (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/actual-broker-final-repeat.tests.txt`) pass the strengthened fixed-code fencing assertion: actual `PRODUCER_FENCED` code 90, input-next 1 unchanged, commit/abort counts 1 and SDK redelivery advancing input-next to 2. Original four-case owned-seek final rerun (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/owned-seek-regression-final.json`) passes independently, with outer command/source provenance (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/owned-seek-regression-final-run.json`). Its preceding passed broker receipt remains retained alongside the recorder invocation-error note (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/owned-seek-recorder-invocation-error.json`): the outer recorder and inner harness were accidentally given the same JSON path; the recorder refused overwrite and the repeat uses distinct paths. Affected normal/race/Python/documentation receipts are recorded in `evidence/`. Receipts preserve raw bounded output, source hashes, command, toolchain/profile and cleanup. Failed attempts remain retained.

| Final check | Result / receipt |
| --- | --- |
| Default Kafka / DSL | PASS 15.548 s / 0.316 s — receipt (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/final-default.json`) |
| `kafka_real` Kafka / DSL | PASS 15.522 s / 0.356 s — receipt (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/final-real.json`) |
| Full affected default races | PASS 16.742 s / 1.918 s — receipt (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/final-default-race.json`) |
| Full affected `kafka_real` races | PASS 17.042 s / 1.711 s — receipt (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/final-real-race.json`) |
| Explicit fixture-control races on final source | PASS 1.906 s — receipt (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/final-helpers-race-repeat.json`) |
| Python fixture controls | PASS 22 tests — receipt (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/final-python.json`) |
| Documentation | PASS 129 active Markdown files — final receipt in `evidence/` |

The default/`kafka_real` checks do not compile the separately tagged fixture; its final tagged races and genuine repeat qualify the subsequent fixture-only assertion/default corrections. Runtime production source remained frozen throughout final checks. Final source/evidence inventory (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/verification-provenance.json`) distinguishes immutable actual receipts, recorder-control checks and the unresolved composed repository gate.

The fixture pins the Kafka registry/index/platform digest, one CPU, 1.5 GiB container memory, protected credentials, exact loopback listeners, two owned volumes/network/container, a 2 GiB data guard, two-minute actual-test context and bounded socket ownership and outer wall/log/disk limits. Tests do not use the anonymous setup listener. The wrapper owns/reaps the whole test process group and independently verifies container/volume/network removal and protected scratch deletion. No source, dependencies, deliverable images or shared build caches are pruned here.

<a id="feature-completion-kafka-transactions-2026-10-05--explicit-remaining-gates"></a>

### Explicit remaining gates

Fresh-source qualification does not establish durable logical-identity continuation, broker restart/retry or failover, transactional revocation/cancellation outcomes, multiple input topics, multi-broker clusters, other broker versions/authentication, hosted brokers or Event Hubs Kafka tiers. HTTP/database side effects are outside Kafka atomicity. Full repository composition remains required before integration; no unrelated provider, platform or analytics gate is certified by this record.

<a id="feature-completion-kafka-transactions-2026-10-05--opt-in-inventory-registration-follow-up"></a>

### Opt-in inventory registration follow-up

The composed repository gate found missing opt-in inventory/runner registrations.
Both `kafka_seek_integration` and `kafka_transaction_integration` now have explicit
rows in [TEST_PROGRAM](../../qa/TEST_PROGRAM.md#build-tag-gates) and separate
`--all` runner entries calling the existing bounded wrappers. Each wrapper gets
a fresh private report directory, printed for receipt discovery. Default `SKIP`
and `--list` establish no qualification; an explicitly selected wrapper failure
remains a failed opt-in suite.

The focused inventory/runner gate (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/optin-registration-go-final.json`)
passes, and the new offline Bash regression proves no fixture invocation for
list/default runs, both wrapper invocations under `--all`, isolated retained
receipt paths and propagation of required-fixture failures without a provider
PASS. Python controls (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/optin-registration-python.json`) pass 22 tests;
shell syntax (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/optin-registration-shell.json`),
actual inventory listing (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/optin-registration-list.json`) and
documentation checks pass. Direct
owned-seek (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/optin-registration-negative-seek.json`) and
transaction (`../../qa/reviews/feature-completion-kafka-transactions-2026-10-05/evidence/optin-registration-negative-transactions.json`) tagged
runs with their fixture variables unset deliberately return exit 1 and parent
FAIL, with no PASS/SKIP markers. These negative checks validate refusal before
broker access and do not replace the genuine passing receipts above. This small
registration follow-up changes no Kafka runtime, broker fixture or prior
qualification scope; the composed full repository gate remains required.

<!-- Original record: docs/qa/reviews/feature-completion-eh-capacity-2026-10-03/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-eh-capacity-2026-10-03/) -->
<a id="feature-completion-eh-capacity-2026-10-03"></a>

## Event Hubs processor capacity review — 2026-10-03

Scope: completion specification EH-1: process-wide typed processor/worker demand, target-free staging, immutable peak claims, local/distributed placement, preparation readiness, execution adoption, cleanup and visible requested/admitted evidence. This does not close EH-2 lag/recovery or EH-3 provider qualification.

The default real-adapter pool retains the established four processors, 32 workers and 32 partitions per processor. Lower-only startup limits and immutable per-processor ceilings are explicit. Producer-only and observer work have no processor demand. Scheduled/adaptive peaks and open-workload effective maximum concurrency determine demand independently of optional pod reservations. Phase-only consumers are accounted without averaging or inventing TPS-to-processor equivalence.

Independent review and regression fixes cover:

- Single-use adoption, duplicate-owner cleanup safety and atomic pending-only release; zero resize ends the parent instead of accumulating dormant registry entries.
- Actual partition inventory checked after execution commit; incompatible or tampered claims fail before traffic. Prepared zero-peak consumers cannot grow outside their frozen admission.
- Capacity-only and engine-backed staged commit/readiness checks, expired/fenced stage cleanup and same-process HA transfer retaining the exact reservation.
- SDK queued partition drain after cancellation, worker/transport close lifetime accounting and explicit retained draining capacity when cleanup is unproven.
- Preservation of immediate ordinary staged-engine shutdown while Java host cleanup keeps its existing asynchronous drain barrier.
- Local allocation distinguishes current VUsers from the frozen resource peak. Resource-bearing generator-loss evacuation remains unavailable until qualified ownership/checkpoint recovery exists.
- Dashboard handwritten/generated DSL parity and missing-versus-confirmed reservation display.
- Resource-free capacity weighting for HTTP, observer and producer groups. The first repository gate exposed a 90:10 placement regression; the corrected resource-fit upper bound retains the expected 8:2 allocation.
- Startup and teardown deadline coverage across lifecycle-gate wait, inventory requests and late consumer ownership transfer; cancelled startup cannot launch a processor.
- Asynchronous snapshot tests wait for completed publication with a bounded deadline instead of assuming a fixed sleep covers resource sampling. The ten raw samples are buffered before aggregation so their expected single window is independent of test-goroutine scheduling; idle negative and data assertions remain.

Final verification passed. Independent repeat reviews found no remaining issues in this slice.

Confirmed checks: full provider default/tagged tests; provider resource race tests (1.495s/1.505s); full tagged generator tests (40.248s); targeted generator resource/graceful-cleanup race tests (6.206s); documentation checks for 100 active files; dashboard typecheck, lint, 4,756 unit tests, build and full browser cycle (190 dev-admin passes/4 configured skips, 110 local-auth passes/1 configured skip).

The initial repository run exposed the fixed resource-free placement regression, reached the API package's cumulative 20-minute deadline during Swagger generation, and failed two asynchronous engine snapshot tests using fixed sleeps. The latter tests now use bounded publication waits and passed in the full engine rerun (35.512s). The second repository run uses a 40-minute package deadline and 4 GiB compile/runtime memory headroom. Its API package passed (563.661s), including the placement and generated-contract checks; its generator package passed (44.138s). It exited nonzero on a Java broker readiness regression: the readiness handler could expose internal `reserved` before asynchronous preparation began, which the client correctly rejected as an unsupported public state. The deterministic service/client regression failed before the fix and passed afterward. Ready now projects internal `reserved` to the established public `preparing` state without advancing preparation or weakening owner, expiry, inventory, commit or invocation fences. Thirty repetitions of the regression/original failure, both full affected package suites and focused race checks passed. Independent repeat review is clean. The third full repository run exited zero on the final source; unchanged package results were reused by Go's test cache, and broker/sidecar executed in 3.248s/27.496s. The first targeted API race run failed the existing HTTP supervision integration fixture with a two-second RPC deadline and finalizer timeout; the current-source serialized rerun passed (20.828s) with unchanged production and fixture deadlines and no race warnings. Its original timeout cause remains unproven. Reviewed snapshot fixtures passed default (0.604s) and race (1.765s). A redundant focused default build was interrupted while compiling and is superseded by the final full repository gate; it is not recorded as passing.

Out-of-slice evidence findings discovered while preparing EH-2: database interval-history reload omits messaging JSON, and distributed action messaging projection follows an early return for absent business aggregates. These remain explicit next-slice fixes; EH-1 closes processor admission and lifecycle semantics only. Local/tagged tests do not certify Azure rebalance, identity, service takeover or target-wide resource limits.

Remaining acceptance: approved Event Hubs emulator subset and live Azure processor/epoch/checkpoint matrix; full tail/checkpoint progress diagnostics; measured capacity increases. IBM MQ remains low priority. The operator-waived 10 GB analytics benchmark was not run.

Commands and evidence:

- Final repository: `GOCACHE=/private/tmp/apexload-feature-completion-go-cache GOMAXPROCS=2 GOMEMLIMIT=4GiB go test -p 1 -timeout 40m ./...` — exit 0; log (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/go-tests.log`).
- Provider: default and full `eventhubs_real` suites plus focused resource race; default (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/provider-default.log`), tagged (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/provider-real.log`), race (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/provider-race.log`).
- Generator: full tagged suite and focused resource/graceful-cleanup race; race transcript (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/generator-race-transcript.log`).
- API and snapshot gates: [review and logs](../../qa/reviews/feature-completion-eh-capacity-2026-10-03/engine-snapshot-review.md), including the earlier API timeout and unchanged-deadline successful serial rerun.
- Broker: deterministic failure (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/broker-ready-before-fix.log`), fixed case (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/broker-ready-after-fix.log`), 30 repeats (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/broker-ready-repeat.log`), full affected suites (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/broker-default.log`), race (`../../qa/reviews/feature-completion-eh-capacity-2026-10-03/broker-race.log`).
- Dashboard: Node 22.22; `npm run test:cycle` — exit 0, 4,756 unit tests, 190 dev-admin passes/4 configured skips and 110 local-auth passes/1 configured skip. The new preparation case also passed both targeted auth profiles, and the narrow-layout screenshot was inspected. Skips are not provider acceptance.
- Documentation and generated DSL/API/TypeScript freshness passed; `git diff --check` passed.

Dashboard full-cycle transcript SHA-256: `309117ac5572381d06574e3a71377b8ec5d4d3ddb08d94dd0dae219ca3688664`. Its temporary transcript is `/private/tmp/apexload-eh-capacity-dashboard-cycle.log`; counts and commands above are preserved here.

<!-- Original record: docs/qa/reviews/feature-completion-eh-pause-2026-10-03/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-eh-pause-2026-10-03/) -->
<a id="feature-completion-eh-pause-2026-10-03"></a>

## Event Hubs acknowledged pause review — 2026-10-03

Scope: the strict processor pause portion of EH-2. Managed processors close new action/receive/delivery/tail/checkpoint admission at the pause latch. Previously admitted actions finish their assertions and fenced checkpoints before acknowledgement. Lazy receivers, actual physical-connection cleanup and engine leases establish quiescence; requesting cancellation or reading a quiet metric does not establish proof. Ownership reads/renewals continue. Existing SDK control-plane keepalives and bound infrastructure monitors retain their separate lifecycles.

Both `eventhubs_processor_pause_v1` and `messaging_runtime_quiescence_v1` are required for distributed processor pause. Preflight precedes all mutations. Exact operation/sequence and explicit true proof are required; absent/false acknowledgements compensate, and unproved compensation persists STOPPING and drains the actual runtime. Start capability requirements and ordinary nonprocessor control compatibility are preserved.

This slice does not close processor recovery, live Azure/emulator acceptance, EH-2, R2 or the full feature program. Local/tagged fixtures do not certify service behavior. The operator-waived 10 GB import benchmark was not run.

<a id="feature-completion-eh-pause-2026-10-03--review-and-fixes"></a>

### Review and fixes

Repeated independent reviews corrected the following findings, with regressions:

- The pinned SDK Processor eagerly opens receive links, and cancelling a manual-credit read does not retract its credit. Managed execution now uses a governed lazy dispatcher and an exclusive physical SDK consumer per charged partition worker. Actual private transport closure remains inside the receive proof lease; failed closure retains proof and quarantines capacity.
- A fresh `latest` attach could skip pause-gap messages. One bounded initial partition-properties query freezes a private exact cursor before the first read. Actual received cursors remain separate from durable checkpoints and opt-in tail evidence. Geo-DR Earliest fallback persists through empty/cancelled attaches until an actual event establishes its new cursor; it does not reset the checkpoint store.
- Cancelled action tokens could be preserved with a cancellation-stripped context to mint new children. Child and reentrant action admission now require the registered parent context remains live. Cancellation never frees the original charge.
- Initial/teardown-only and zero-floor consuming scripts could evade pause admission, or mutable scripts could change their runtime profile after planning. Consuming phase plans and pure immutable preparation profiles now cover these cases before protocol initialization.
- Rollback omitted a failed but latched participant, and restoring PAUSED after a failed Resume used latch-only compensation. Every attempted participant is included; restoring processor PAUSED requires fresh proof. Unproved compensation detaches/stops the exact execution incarnation and retains bounded drain obligations.
- Paused terminal cleanup could strand bounded DSL teardown. Explicit terminal state closes ordinary admission and privately authorizes fresh bounded teardown; ordinary Pause still parks it. Foreign, expired and finally closed authorities cannot revive admission.
- Malformed claims and compacted legacy topology could bypass required-peer checks. Canonical immutable claims are validated before sequence mutation, and unknown consuming plans fail closed. Retained terminal/evacuated archive transports are excluded from control, including readiness, retries and compensation.
- Completion during readiness/control/compensation could falsely trigger uncertain STOPPING. Each stage rechecks trusted terminal state, preserving actual terminal outcomes without retrying removed peers.
- Retained SDK metadata/payload could exceed bounded pending evidence. The application copy has a combined 1 MiB bound, validated exact cursor, bounded key/properties and only minimal checkpoint cursor retained across ACK/CAS.
- Browser screenshot review exposed a stale RUNNING selector beside an acknowledged PAUSED card. The selected live option now uses the same resolved status as the controls; pending/rejected responses remain RUNNING and unrelated run options retain their own catalog status.

<a id="feature-completion-eh-pause-2026-10-03--verification"></a>

### Verification

The reviewed source passed the full Go suite (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/go-all.txt`), including API 622.068 seconds, engine 37.078 seconds and default generator 39.157 seconds. Real-adapter protocol/Event Hubs/generator (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/provider-generator-real.txt`) passed in 0.146/0.764/41.841 seconds; common/provider race (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/common-provider-race.txt`) passed in 2.020/2.219 seconds and targeted API race (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/api-race.txt`) passed in 3.811 seconds. Engine terminal/authority and generator-wire normal/race evidence is retained alongside these final gates. Independent repeat reviews (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/repeat-review.txt`) found no remaining actionable source findings.

The full dashboard cycle (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/dashboard-full-cycle.txt`) passed typecheck/lint, 4,760 unit tests, build and 192 dev-admin/112 local-auth browser tests. Four dev-admin and one local-auth conditional/protected-fixture skips remain unqualified. The final selector correction passed 75 component tests (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/dashboard-selector-unit.txt`), typecheck (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/dashboard-final-typecheck.txt`), lint (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/dashboard-final-lint.txt`) and build (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/dashboard-final-build.txt`). The final affected browser repeats passed all four tests in dev-admin (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/browser-final-dev-admin.txt`) and all four in local-auth (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/browser-final-local-auth.txt`), with no skips. The final acknowledged-pause screenshot (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/acknowledged-pause-local-auth.png`) was visually inspected; both selector and run card show PAUSED. Source hashes (`../../qa/reviews/feature-completion-eh-pause-2026-10-03/source-hashes.txt`) bind this review to the committed slice. A full dashboard cycle precedes the final small selected-status correction; final component, typecheck/lint/build and affected browser repeats verify that correction separately. Existing conditional/protected-fixture skips are not provider acceptance.

<!-- Original record: docs/qa/reviews/feature-completion-eh-progress-2026-10-03/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-eh-progress-2026-10-03/) -->
<a id="feature-completion-eh-progress-2026-10-03"></a>

## Event Hubs processor progress review — 2026-10-03

Scope: the progress evidence portion of EH-2. Standalone bounded generation snapshots preserve ownership, exact delivered/checkpoint/tail sequences, opaque provider offsets, checkpoint populations/distributions/outcomes and source-monotonic freshness. They survive zero-action live windows, trusted group/peer projection, JSONL/Parquet, SQLite/PostgreSQL reload, reports and bounded native API/CLI queries. Tail management traffic requires explicit opt-in and negotiated capabilities.

Strict processor pause acknowledgement, recovery qualification and real Azure/emulator acceptance remain separate open slices. This record does not close EH-2, R2 or full feature completion. The operator-waived 10 GB import benchmark was not run.

<a id="feature-completion-eh-progress-2026-10-03--review-and-fixes"></a>

### Review and fixes

Independent reviews and meaningful regressions corrected:

- Durable history omitted messaging JSON, and distributed live projection discarded messaging when business transaction aggregates were absent. Neither repair manufactures transactions or rates.
- Group performance selection cleared standalone progress for idle groups; trusted group projection now survives without completed transactions.
- A fresh archive review found admitted legacy processors that never emitted progress did not invalidate otherwise qualified peer backlog. Archive expectations now use controller final/private initial admission claims; remote legacy provenance cannot invent expectations. Mixed/all-legacy gaps explicitly remain unavailable, and released sources stop being expected.
- Unrelated HTTP intervals falsely implied missing processor evidence. Known nonprocessor sources are skipped; actual admitted missing/legacy processor peers remain explicit. Unknown uncollected archive sources conservatively mark coverage incomplete.
- Shared tail-query evidence could hide another client's permission failure. Only poll admission is shared; each identity queries and publishes its own frozen generations. Poll cancellation joins before consumer cleanup and suppresses late publication.
- A fresh provider review found acquired-but-unverified generations could still be evicted by terminal history, and an ownership loss followed by a fenced checkpoint could double-count loss. Provider retention now prioritizes both current ownership states and loss transitions occur once per acquired generation, while each fenced attempt remains counted.
- Historical rows could displace current ownership at row/byte limits. Current owned and unverified rows now precede history, with replay-stable lower-bound omission counts. New verified generations retire prior acquired-but-unverified owners.
- Numeric case aliases could overwrite validated populations with null. Canonical required numeric keys are enforced before decoding.
- Final engine sampling could miss concurrently accepted registrations. Registration closes before final inventory; unresolved cleanup retains incomplete evidence. Phase-only consumers no longer register unrelated producer-phase clients.
- Protocol reinitialization could race the pure metric snapshot reader. Atomic tracker publication removes reads of mutable runtime configuration and retains prior evidence during successful replacement.
- HA identity refresh could return stale local evidence, or a new project's evidence after RBAC resolved an older project. Getter identity validation and live/immutable archive-manifest project binding prohibit both cases before evidence publication.
- Dashboard/report parity now preserves exact provider offsets, nullable qualified backlog, checkpoint outcomes and duration populations. Measured zero remains distinct from zero observed samples; reports use actual mergeable histograms for approximate P95.

Per-row source capture timestamps remain authoritative provenance. Qualified backlog is as of that capture, with unqualified transit time and cross-host alignment. Missing, stale, empty, unconfirmed, fenced or incomplete evidence never becomes measured zero. These findings are advisory, not new stopping policies.

<a id="feature-completion-eh-progress-2026-10-03--verification"></a>

### Verification

Final gates passed on the reviewed source. The first full Go run exposed stale generated API contract and authorization snapshots for the new route; both fixtures were corrected and the full suite was repeated. Console logs retain outcomes with trailing whitespace removed. Verified gates:

- Full Go suite (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/go-all.txt`), including the API suite (433.764 seconds); real-adapter generator suite (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/generator-real.txt`) (43.655 seconds); targeted API race (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/api-race.txt`) (6.576 seconds); documentation (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/documentation.txt`) and staged diff checks.
- Fresh independent repeat reviews (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/repeat-review.txt`) found no additional actionable issues after the final fixes.
- Provider/shared review (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/provider-review.txt`), default (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/provider-progress-default.txt`), real-adapter build (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/provider-progress-real.txt`), race (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/provider-progress-race.txt`).
- [Engine review and tests](../../qa/reviews/feature-completion-eh-progress-2026-10-03/engine-review.md).
- API targeted (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/api-default.txt`), API generation (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/api-generation.txt`), dashboard typecheck/lint/unit/build (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/dashboard-quality.txt`) (4,760 tests), and affected Playwright workflows in dev-admin (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/browser-dev-admin.txt`) (four passed) and local-auth (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/browser-local-auth.txt`) (four passed). Captured browser evidence (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/eventhubs-progress.png`) was visually inspected.
- Wire/storage normal (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/wire-storage-default.txt`), race (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/wire-storage-race.txt`), disposable PostgreSQL 16 migration/round-trip (`../../qa/reviews/feature-completion-eh-progress-2026-10-03/postgres-history.txt`). The private fixture and credential file were removed.

Local/tagged tests do not certify Azure identity, rebalance, crash recovery, epoch takeover or production service behavior. Full program work continues after this commit.

<!-- Original record: docs/qa/reviews/feature-completion-servicebus-management-2026-10-04/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-servicebus-management-2026-10-04/) -->
<a id="feature-completion-servicebus-management-2026-10-04"></a>

## Service Bus management review

Status: final collector, durable SQLite/API, full Go, targeted API race and dashboard verification passed. No live Azure qualification is claimed.

<a id="feature-completion-servicebus-management-2026-10-04--behavior-and-boundaries"></a>

### Behavior and boundaries

The executable `servicebus-runtime` pack reads only fixed entity descriptions for a finite, explicitly approved namespace/inventory using a pinned shared-key Manage principal. Numeric routing, protected immutable CA trust, fixed port 443, no DNS/proxy/redirect/retry expansion, source-wide minimum 30 s cadence and independent socket/TLS/GET activity admission preserve the existing signed authority and physical ownership contract. Missing/deferred/unsupported populations remain explicit; exact observed decimal strings retain zero. This management pack does not enable data-plane transactions.

Source authoring separates saved approval from the unsaved draft, supports hierarchical queues/topic plus single-segment subscriptions, preserves revision conflicts, and retains historical ten-second source definitions for reads/deletion while requiring current cadence for a new revision. The shared Kafka reader rejects foreign Service Bus transport settings.

The adopted drafts received independent bounded source reviews of fixed configuration, SAS issuance, finite response decoding, physical transport ownership, scheduler authority, durable cadence and client workflows. The final readiness/cadence re-review is clean. Two final product findings were fixed and re-reviewed:

- Signed Service Bus jobs now enforce the same minimum thirty-second interval at the collector's first admission boundary; previously the later observer rejected an undersized interval. A signed malformed/old-issuer table tests all three management packs around their boundaries.
- The local-auth identity card and sign-out action escaped the CSS-narrowed sidebar. The narrow layout now contains both, with real browser geometry assertions and inspected screenshots.

The first two new API cohort attempts failed because the test fixture advertised an unkeyed destination and then used the legacy host-only readiness helper. The real collector already advertised its enrolled key. The corrected fixture exercises the actual HTTP readiness headers, rejects unkeyed/wrong-key approval, verifies the sealed key/principal, rotates the secret and verifies durable duplicate-batch/cohort receipts. These were fixture corrections, not a product readiness change.

<a id="feature-completion-servicebus-management-2026-10-04--validation"></a>

### Validation

Final collector normal passed (11.976 s) and race passed (14.132 s) after the admission fix. Full SQLite storage passed (97.543 s). The first storage race run exhausted temporary/database storage; the repeat passed (105.426 s) after exact task-owned superseded object cleanup. The final new actual API cohort passed (1.530 s).

The final full `npm run test:cycle` passed: typecheck, lint, 462 unit files/4,832 tests, a fresh embedded build, 198 dev-admin browser workflows (4 existing skips) and 118 local-auth workflows (1 existing skip). The six new desktop/narrow source/approval screenshots were inspected; no further finding in those views. This includes the added signed-in sidebar bounds assertion.

The first full cycle had two dev-admin archive-download timeouts; their traces showed HTTP 500, but the old controller log was not retained, so the precise cause is unproven. The independently repeated lifecycle/archive workflows passed (3 tests, 12.9 s) with full controller logs retained, and both downloads passed again in the final full cycle. An initial repeat selected no tests because the title regex used a slash instead of the existing space; it is not acceptance evidence. Original fixture/cycle failures are retained, rather than counted as passes.

Full `go test ./...` passed, including the API package (500.816 s), SQLite storage (44.113 s), generated-contract freshness and documentation tests. Final targeted API race passed; the final standalone documentation check passed. The completed logs and screenshot hashes are in the retained evidence manifest (`../../qa/reviews/feature-completion-servicebus-management-2026-10-04/retained-evidence-final.json`); compressed logs preserve exact raw hashes. The local-auth controller snapshot is an observation captured before fixture teardown, not a complete shutdown log.

Docker daemon availability timed out in a bounded five-second read-only check. Actual PostgreSQL 16 cadence integration is pending; source fixtures do not establish it. Live Azure counters, tiers and Entra identities remain unqualified.

Root uses a task-only package-specific API compiler profile (`-gcflags=github.com/jasonczech/apexload/pkg/api=-dwarf=false`, with stripped test linking where applicable) to reduce scratch demand. Independent pinned Go1.26.5 source inspection confirms this removes debugger metadata while preserving runtime PC/file/line/inline tables, panic/Callers and Go race instrumentation/symbolization. It does not qualify debugger/native-C symbolization or release packaging. No test assertions or production resource reserves are changed.

During this slice, nine exact task-owned obsolete DWARF API archives were pruned after independent profile/source and open-file checks, reclaiming 1,165,252,940 bytes; current API/dependency objects and another chat’s separate cache were preserved. Inspection (`../../qa/reviews/feature-completion-servicebus-management-2026-10-04/obsolete-api-cache-inspection.json`) and prune receipt (`../../qa/reviews/feature-completion-servicebus-management-2026-10-04/obsolete-api-cache-prune.json`) retain the exact identities. Final cache cleanup remains deferred while later program slices need the caches. The 10 GB benchmark waiver does not waive native 1 GiB, platform or live-provider acceptance.

<!-- Original record: docs/qa/reviews/feature-completion-solace-management-2026-10-04/README.md; evidence kept outside git (formerly tracked in docs/qa/reviews/feature-completion-solace-management-2026-10-04/) -->
<a id="feature-completion-solace-management-2026-10-04"></a>

## Solace management review

Status: implemented and verified within the controlled local reader/client scope. Independent review/fix/re-review is clean; full Go, targeted races, documentation and the full dashboard cycle pass. Live broker and restarted-source qualification remain pending.

The fixed `solace-semp` reader follows the pinned 10.26.7.12655 SEMP v2 individual queue/topic-endpoint schema. It retains exact depth, missing values, exact VPN/name identities, static numeric TLS routing and protected CA trust. One origin-owned Session cookie is separate from process/socket ownership. A bounded protected intent becomes durable before Basic bytes; fixed own-session PUT logout, validated provider metadata, physical close and durable removal are all needed for session release. Ambiguous cookies/logout/authority retain quarantine across restart and configuration identity changes. There is no ordinary/public provider-session reset.

Independent configuration/decoder, transport/session and client reads were clean before coherent adoption. Integrated review found response capture after physical GET cleanup; it now captures the complete read first and retains cleanup in row age, with a controlled delayed-close regression. It also found ignored lock-descriptor cleanup errors; the correction retains one exact constructor/release/Close owner, preserves first failure or panic, and joins management tasks before releasing the lock. Final independent integration review is clean; collector/filelock normal tests passed (18.763 s/0.305 s), and their race checks passed (22.019 s/3.329 s).

The first collector execution failed two fixture assumptions: its positive startup fixture used a public temporary directory, and its cancellation could occur before receiving the first session cookie. The corrected positive fixture explicitly uses 0700 and holds a later response after the cookie is established. The explicit public-directory rejection and ambiguous-session tests remain intact. The full collector repeat passed (17.507 s) and race passed (20.856 s). The actual API source/revision/keyed readiness/sealed Basic/two-run durable cohort test passed (1.912 s), retaining observed zero, 2^53+1 and literal Unicode/slash identities with duplicate receipt deduplication and secret-rotation fences.

The original 32 affected client checks passed, followed by a 48-test repeat after sixteen Unicode boundary cases were added. Typecheck passed. The first lint caught redundant C0 whitespace in a regular expression; removing it preserves rejection because the scalar validator already rejects all C0 codepoints everywhere. The lint repeat passed. Full dashboard unit verification passed 465 files/4,880 tests; the full dashboard cycle passed, including fresh embedded build, typecheck and lint, 199 administrator workflows/four existing skips and 119 authenticated workflows/one existing skip. The Solace workflow and six inspected desktop/narrow screenshots passed in both authentication profiles, using actual dashboard navigation with finite API fixtures. These qualify client request/revision/layout fidelity, not live SEMP interoperability.

The full `go test ./...` gate passed, including API (585.362 s), storage (51.800 s), historical/generated contracts and remaining packages. Final targeted management/run-monitoring API race passed (52.579 s), and documentation checks passed. The browser cycle built its controller before the startup-owner correction; the correction is covered separately by final Go and collector/filelock checks. The API/DSL public contracts were already optional; no new public endpoint or migration is introduced by this reader. PostgreSQL, live pinned broker/session cleanup, audited restarted-source recovery, alternate version/v1 XML, inventory pagination and complete presence-aware DMQ relationships remain mandatory separate program work. Default five-hundred-twelve operation and poll limits may leave an approved 256-entity inventory incomplete; no full-provider population is inferred.

Root retains the task-only pinned API no-DWARF compiler profile used by the prior Service Bus slice; it does not qualify release debugger/native-C packaging. No production reserve/assertion was relaxed. Final build-cache cleanup remains deferred while the program needs these caches. The 10 GB benchmark waiver does not waive native 1 GiB or platform/provider acceptance.
