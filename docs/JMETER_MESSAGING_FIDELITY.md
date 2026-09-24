# JMeter Kafka / Event Hubs import fidelity

**Current status: native draft preservation is implemented; strict replay is blocked. The complete runtime/repair plan is not implemented.**

The importer recognizes DI-KafkaMeter producer/configuration classes and PNOP Event Hubs samplers with string messages. A plan containing the three producers in the synthetic fixture becomes three native drafts and one concurrent scenario. The disabled CSV and disabled producers remain disabled.

The fixture preserves 100/50/50 threads, one-second thread ramps, 300-second linear ramps from 1 to 84/42/42 samples per second, and 3,600-second plateaus. The scenario lasts 3,900 seconds. Integrating the schedules gives 630,450 planned samples, assuming one message per sampler and sufficient available threads. This is not a guarantee of successful sends and does not rewrite the workload to 600,000.

## Implemented

- Source identities use the original plan digest plus XML byte offsets. Samplers on the same line remain distinct. Scenario groups bind by source identity rather than filtered array position.
- Kafka configuration resolves by source scope and producer variable. `extraConfigs` override base settings; both views are retained. String payload bytes, explicit partitions, and authored empty keys are retained. Native Kafka distinguishes empty from null keys. In the pinned DI compatibility mode an authored empty string becomes a null wire key, matching the plugin; whitespace-only keys remain nonempty. Import provenance is excluded from Kafka wire headers.
- Event Hubs message lists remain one sampler draft. Multiple messages are retained together and explicitly blocked pending batch runtime support. HTTP Header Manager entries are marked ineffective and never converted into messaging properties automatically.
- Counter/random declarations retain source scope, iteration update, bounds, wrap, and seed. A session-owned shared sequence pool and Java 48-bit generator are implemented behind the execution gate. Tests cover concurrent wrapping, per-user/plan/run isolation and 600 JDK 17 oracle values; full lifecycle iteration timing remains unverified.
- Sampler schedules retain segment rates, duration, scope identities, and action references. They are distinct from thread ramps and iteration pacing. They are persisted with scripts; the saved scenario retains source-engine affinity and schedule duration. Schedule/identity mismatches are rejected on import save.
- Bounded Java date formatting handles single-`S` minimum-width milliseconds, quoted literals, UTC offsets and explicit timezones. Unsupported patterns are rejected. The supplied two-evaluation timestamp remains two evaluations unless a supported explicit repair changes it.
- Accounting separates enabled/disabled workload samplers, converted/blocked workloads, configuration contributors, and reporting elements. Unknown sampler variants retain blocked drafts instead of disappearing with their groups.
- JAAS parsing accepts a bounded set of options and never loads uploaded Java classes. Nonstandard option names, malformed/unknown options, unsupported serializers/callbacks/configuration, unresolved variables and missing credentials retain source-linked blockers.
- East/west Event Hubs bindings are distinct. Messaging source redaction checks decoded XML attributes as well as text, including credentials duplicated in message element names.
- Preview shows three workload curves, planned samples, affinity, preserved Kafka overrides and explicit converted-versus-runnable status. Warnings include static data, unused counters, literal-Z assumptions, partition availability, View Results Tree and missing downstream verification.
- The pinned reference installation starts and loads the synthetic JMX. See [reference evidence](../scripts/jmeter-reference/README.md). This is startup/formatter and deterministic parameter evidence only.

## Execution boundary

`config.jmeter.profile = "jmeter-messaging-v1"`, `config.sampler_schedules`, parameter scope/algorithm fields and `action.pacing_ids` are versioned preservation contracts. All imported actions carry durable execution blockers. Validation permits the structurally valid blocked drafts to be saved; removing their blockers fails validation. Engine admission independently rejects the pending compatibility contract even if metadata is stripped. Single-generator affinity is persisted and admission rejects it until the allocator/runtime supports it; it cannot silently fall back to distributed placement or restart after loss.

Existing scripts with none of these compatibility settings retain their existing defaults. Empty-versus-null Kafka key preservation is a targeted protocol correction.

## Remaining implementation

1. Complete end-to-end iteration-update conformance for the implemented shared parameter pool; full virtual-clock throughput gating, scope sharing, schedule completion and pause behavior. The pinned timer uses per-second admission accounting and inclusive segment endpoints, so a generic token bucket is not an equivalent replacement.
2. Kafka shared producer lifecycle and PNOP per-sample producer lifecycle, sampled latency versus worker occupancy, batch sends, and failure cleanup traces.
3. Typed effective Kafka producer tuning, admission/buffering versus delivery timeouts, TLS bounds, generic explicit client-credentials OAuth and complete credential/disclosure integration. Effective-settings metadata does not imply runtime support.
4. Single-generator allocation, capacity/capability negotiation and older-generator rejection, without restart or counter replay after loss.
5. Atomic protected multi-script/scenario repair preview/apply: OAuth option normalization, credential binding, UTC timestamp replacement, trace-property repair and selected payload parameterization. The `messaging_repaired.jmx` fixture currently changes only OAuth option names; it deliberately retains masked credentials and other source behavior.
6. Reference lifecycle/rate/OAuth conformance, authorized live Kafka/Event Hubs validation, and full browser repair/secret-binding workflows. Strict parity stays unverified until these pass.

Do not clear compatibility blockers merely because credentials are bound or a warning was acknowledged. Do not enable the disabled CSV, add business assertions, transmit the ineffective Header Manager, or change message volume without explicit review.

## Validation evidence for this implementation

- Conversion of the supplied file: 3/3 enabled workload samplers mapped to Kafka/Event Hubs/Event Hubs; all three remain blocked drafts. Threads 100/50/50, one-second ramps, 3,900-second duration and single-generator affinity are preserved.
- Focused converter, import-contract, DSL, Kafka, Event Hubs and engine suites pass. API tests cover attested save/reload, blocked launch, source/schedule tampering and affinity/loss rejection.
- Shared parameter tests pass under the race detector, including 100 concurrent users, exact counter wrap counts, Java oracle vectors and run/plan isolation. Real-adapter build-tag suites pass; these are not live-service conformance results.
- The messaging browser workflow passes in both dev-admin and local-auth profiles, including workload display, effective settings, scenario persistence and blocked launch. A fresh dashboard build and visual inspection of the three-curve preview passed.
- The full dashboard cycle did not pass: review/credential tests hit standard timeouts. Their 106-test diagnostic rerun passed with a 30-second timeout. A separate local-auth HTTP JMeter repair workflow also hit a click timeout. These diagnostic passes do not replace the required normal full cycle.
- Full repository Go validation has not yet produced a clean result. The initial run timed out in the large API suite; Git-dependent QA checks in the isolated validation copy were subsequently corrected and passed. Do not report the entire repository or the full plan as verified from focused checks alone.

## Two-pass code review follow-up

Both passes included regression tests for their findings. Fixes:

- Reject retained sampler pacing and per-user source counter identities even after the profile/metadata is removed, including direct engine callers.
- Bound standalone compatibility validation and include parallel branches in structural validation and blocker traversal.
- Require complete source identities and schedules on messaging scenario save; reject duplicate or omitted source groups.
- Redact credential-valued Kafka extra properties from source review. Omit source review when unsupported JAAS escaping cannot be decoded safely.
- Normalize numeric Kafka acknowledgement aliases while retaining their authored values in effective-settings evidence.
- Detect structured Event Hubs message options instead of silently ignoring nonempty collection properties.
- Preserve each sampler's native configuration, retain the first script configuration, and explicitly block unsupported differing producer configurations. Credential requirement identities use source-node identities rather than display names.
- Reject unsupported multi-JMX messaging scenario assembly instead of inheriting the first document's timing and affinity.
- Use the longest messaging schedule for scenario duration; do not invent a five-minute duration when no source schedule exists.
- Keep shared parameter generation bound to its session definition and reject unnamed sessions.
- Require one complete XML document and bound depth/node count before recursive conversion and source review.

These fixes do not enable the unfinished compatibility runtime or establish live parity.

Review validation: focused converter/import-contract, DSL, engine and API regressions pass, as do shared-state race tests and Kafka/Event Hubs build-tag tests. The full dashboard cycle passed type checking and lint, then reported 4,464 passing tests and one standard-timeout failure in `ScriptImportReview`; all 40 tests in that file passed on a normal-timeout rerun. A fresh dashboard build passed. The frozen full Go run stopped during linking with `no space left on device`, so repository-wide Go validation remains incomplete. These results do not establish live-service parity.

The fresh full browser run completed: dev-admin 136 passed / 4 skipped / 1 failed; local-auth 56 passed / 1 skipped / 1 failed. Both failures are the LoadRunner review scroll-position assertion at `dashboard/e2e/workflow-loadrunner-review.spec.ts:789` (outside these messaging changes). All three JMeter workflows passed in both profiles, including messaging save/reopen and blocked launch. The messaging screenshot was visually inspected. Full browser logs: `/tmp/apex-messaging-implementation/review-browser.log`; full Go environmental failure: `/tmp/apex-messaging-implementation/review-go-frozen.log`.

## Throughput calculation continuation

Implemented a session-owned, concurrency-safe admission calculation state using the pinned TST 2.6 per-second behavior. Definitions are copied, conflicting shared identities are rejected, separate source plans/runs cannot share permits, and unused capacity does not carry into later seconds. Java rate and delay oracle vectors match exactly (7,804 rate values and 448 delay values); full-duration virtual-clock tests cover both workload curves. The reference final endpoint is inclusive: schedule exhaustion is first observed at second 3,901. The nominal authored duration remains 3,900 seconds.

This is calculation conformance only. A component wait loop now applies multiple timers in source order, notifies registered waiters, tracks contention, and cleans up on cancellation; these behaviors have focused concurrent tests. The state was subsequently wired to pre-sampler execution and the engine's active pause clock (see the continuation below). Reference stop escalation and end-to-end timing still require verification. No compatibility blocker was removed.

Continuation validation: `go test -p 1 -timeout 30m ./...` passed, the full engine suite passed after the final component changes, and `go test -race ./pkg/engine -run TestSamplerSchedule -count=1` passed. Oracle regeneration and altered-classpath rejection passed. These results supersede the earlier incomplete full Go attempts for this checkout, but do not establish live messaging parity. No dashboard behavior or public DSL contract changed in this continuation.


## Runtime continuation — not certified for strict replay

Implemented component contracts and behavior:

- Pre-sampler schedule admission before payload interpolation, shared within a multi-group source runtime. Group completion stops new actions while draining in-flight sends. Pause freezes the active schedule clock.
- Explicit producer lifecycle and sample-timing settings. Kafka producers are borrowed from a run-owned session and flushed/closed once; iteration resets and parallel branches retain that ownership. Event Hubs creates/sends/closes per sample, supports one sampled message list, and reports worker occupancy separately from sampled latency. Cleanup errors and panics have regression coverage.
- Kafka OAuth client credentials through the existing bounded OAuth implementation, with standard POST/Basic and the pinned Kafka 3.3.1 Basic wire format. Secret references participate in resolution, redaction, rebinding, protected import validation, and outbound token-endpoint discovery. Missing references never become literal credentials.
- Typed retries, linger, buffer, admission, delivery/request timeouts, and TLS bounds. Metadata/buffer admission is separate from acknowledgement waiting. Raw invalid source settings remain in a blocked draft instead of making the draft unsavable.
- All-or-nothing single-generator placement and a versioned distributed capability requirement. The requirement is intentionally **not advertised** as available. Generator-loss recovery refuses to reset/replay source state.
- Worker occupancy is carried in gRPC transaction metrics and JSON/Parquet archives.

A review found that Java Kafka `batch.size` is an allocation target while franz-go's batch maximum rejects oversized singleton records. The importer no longer translates these as equivalent; it preserves the source setting and adds `jmeter_kafka_batch_allocation_parity_unverified`.

Remaining delivery work:

1. Distributed assignments currently create independent engines per group. They need one shared source-session lifetime, cancellation boundary and coordinated start before affinity can enable strict execution.
2. Complete producer lifecycle/timing reference traces, Java batch/buffer allocation behavior, zero-admission-timeout semantics, and timer stop escalation. Local component tests do not certify these behaviors.
3. Extend protected repair preview/apply to the atomic three-script/scenario candidate, including context confirmation and the requested source corrections, with browser workflows.
4. Complete compatibility capability reporting, pause comparison marking, and fixed-population controls.
5. Finish repository/race/build-tag/browser validation on the final tree and add controlled-service conformance evidence.

The operator has no authorized Kafka/OAuth or Event Hubs endpoints available. Continue local implementation and reference tests; live-service conformance remains **unverified**, and no mock result clears that status.


Additional pinned evidence: the actual Kafka 3.3.1 callback matches seven HTTP wire-format vectors, including Java whitespace and form-encoding behavior. The actual DI sampler record builder matches four key/partition vectors without opening a broker connection. It converts an empty string to a null Kafka key and retains whitespace-only keys. The source's authored empty value remains visible in the draft.

Latest browser validation: 4,472 unit tests passed. Dev-admin completed with 136 passed, four skipped and one failure in the general scenario-builder chart/legend placement assertion (three-pixel overlap); local-auth completed with 57 passed and one skipped. All three JMeter workflows passed in both profiles. The messaging preview screenshot was visually inspected. The full dashboard cycle is therefore not a clean pass.

Latest stable validation: the complete `go test -p 1 -timeout 30m ./...` run passed on the final Go tree. Full race suites passed for HTTP OAuth, Kafka, Event Hubs and engine, and both real-adapter build-tag suites passed. These supersede the earlier interrupted Go runs. The dashboard cycle result above remains unchanged: one general scenario chart/legend overlap failure, with all JMeter workflows passing.

Offline PNOP lifecycle evidence now executes the actual pinned sampler while substituting SDK transport methods and forbidding network access. Five cases cover repeated successful three-message samples, creation failure, send failure and close failure. They confirm per-sample producer ownership, success timing before close and zero completed sample time on creation/send failure. They also expose an outstanding difference: PNOP lets a close exception escape; APEX records a failed protocol result. Strict replay stays blocked. See `scripts/jmeter-reference/eventhubs-lifecycle-evidence.json` and its accompanying vectors; deterministic regeneration and changed-classpath rejection passed. This evidence does not certify actual Azure SDK or live-service behavior.

## Repeated code review of runtime continuation

Two corrective passes and a subsequent review covered the uncommitted importer, runtime, credential, affinity, metric and reference-harness changes. Regression tests now cover:

- Independent Kafka source configurations cannot inherit OAuth/TLS fields from the preceding producer. Distinct configuration identities remain distinct even with identical effective settings; unsupported multiple producer lifetimes stay blocked.
- Positive metadata/buffer admission deadlines cannot be bypassed by a late metadata response. Zero-wait cached-metadata admission remains supported.
- Parallel branches inherit the parent iteration's shared parameter values without consuming new sequence values. Pending sampler gates observe group cancellation even outside the ordinary VUser run wrapper.
- Event Hubs restores the VUser client after a cleanup panic and rejects a nil sample factory result.
- Native Kafka batch settings cannot override a stricter protocol size cap.
- Shared Kafka owner cleanup preserves cancellation classification without exposing adapter error text. Already-cancelled OAuth callers cannot obtain cached tokens.

The subsequent code review found no additional actionable defects in these changes. This does not remove the separately documented implementation gaps or certify strict replay.

A further edge-case pass reproduced an unsaveable blocked draft when an Event Hubs sampler contained no messages. The importer now retains that sampler, its zero-message source evidence and its specific execution blocker without emitting an invalid native message-list parameter. The regression fails before the fix and passes after it; a subsequent review found no further actionable issues. Final validation was restarted after this correction.

Final review validation: the complete Go suite passed on the final production tree. Full engine/HTTP OAuth/Kafka/Event Hubs race suites and both messaging real-adapter tag suites passed; cancellation/shared-parameter regressions also passed 20 repeated race runs. All four pinned offline oracle vector/evidence sets reproduced exactly. The full dashboard cycle passed type checking, lint, 4,472 unit tests, the fresh embedded build, and dev-admin workflows (137 passed, four skipped). Local-auth reported 56 passed, one skipped, and one Author editor validation-message click timeout at `workflow-author-experience.spec.ts:51`, outside the messaging changes. That exact workflow passed three consecutive reruns at the normal timeout. This diagnostic pass does not make the full dashboard cycle a clean pass. No frontend behavior was modified during this review, and strict/live conformance remains unverified.
