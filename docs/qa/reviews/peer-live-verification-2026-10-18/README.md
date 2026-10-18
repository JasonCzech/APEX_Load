# Peer live-verification fixes

Base: `ec42f607f4942c268c1527951b84af70636a5556`. Local verification uses
Go 1.26.9 and Node 22.23.3 on macOS arm64. Dashboard workflows use the pinned
Playwright 1.64 Chromium build; the telemetry fixture uses the repository's Go
browser runtime. The evidence below applies to the changes accompanying this
report.

## Changes

| Findings | Change |
|---|---|
| LV1 | Server-owned archive caches and deterministic per-run invalidation assertions. |
| LV2, LV5 | Skip unused Salesforce analysis; reuse parsed HAR evidence; apply safe literal/syntax prefilters before expensive correlation regexes. |
| LV3, LV10 | Smaller parked VUser frames, immutable shared configuration, lazy HTTP version transports; paced network footprint admission and failed-sample rejection. |
| LV4, LV6 | Opt-in canonical import responses with shared draft references, bounded grouped diagnostics and compatible frontend alias restoration. Legacy clients retain their response shape. |
| LV7 | Separate archive quota refusals from malformed ZIP errors; explain invalid Avro container headers. |
| LV8 | Preserve actual archive, HAR and recording provenance in Salesforce native previews. |
| LV9 | Check the pinned browser by launching it before building the E2E controller. |
| LV11 | [Manual pinned local broker runner](../../LOCAL_BROKER_CHECKS.md), dated execution status separate from static coverage, and full tagged PostgreSQL CI suites instead of a test-name allowlist. |
| LV12 | Explicit immutable run incarnation in admission/archive provenance; recover verified HA archives without equating independently assigned completion timestamps. Fence follower history, archive and CI evidence against prior incarnations. |
| Telemetry reference | Distinguish metric collection delivery from later lifecycle envelopes; preserve first-publication latency and actual counters. The 2,000 ms acceptance threshold is unchanged. |

## Validation scope

The user restricted remaining validation to targeted checks after free disk fell
below 3 GB while other agents were building. No full Go, full PostgreSQL,
full race or full browser-suite pass is claimed for this change. A full tagged
PostgreSQL storage run was stopped when that scope changed; partial results do
not count as a full pass. Cloud-provider, private-corpus and platform-specific
lanes without their required environments remain unverified.

The whole dashboard unit suite had already passed: 7,906 tests. After the final
response-normalization correction, the affected two files passed 73 tests.
TypeScript/build, ESLint and bundle budgets pass. Accessibility lint reports
zero blocking errors and the same two pre-existing warnings.

Both pinned local broker packages passed with no skips: Kafka 240 test results
and JMS/Solace 318 (including subtests). The required live Kafka produce/consume
and Solace publish tests ran, and fixture teardown succeeded. See the
[broker receipt](broker-receipt.json). Raw logs are retained with the local task
evidence; basic interoperability does not qualify the separate transaction/seek
or cloud-provider lanes. No recurring task was created.

Targeted PostgreSQL archive-collection durability and SQLite run-incarnation
regressions passed, including atomic completion-time adoption and stale-token
rejection. The PostgreSQL fixture was stopped after validation; its data was
preserved. The three selected CI contract tests, seven Python runner/QA tests
and three browser-preflight tests passed.

Targeted generator lifecycle checks passed, including finalization/reconnect,
exact stopping-generation ownership, archive success/failure, terminal ordering
and graceful-stop regressions.

The archive/cache pair passed all five reported shuffle seeds. The HA soak
passed 25 deterministic controller handoffs using the SQLite transactional
simulation; this is not production AKS or PostgreSQL HA qualification.

The affected browser workflows passed 43 tests in ten files under dev-admin
on pinned Chromium. The private LoadRunner acceptance archive test skipped
because its fixture is unavailable. Under local-auth, 42 tests passed across
the nine applicable files, with the same private-archive fixture skipped. The
profile excludes the dev-admin-only import-labels file. Both runs used the
pinned Chromium and verified edit/save/reopen behavior against a fresh controller.

The final targeted API run passed 95 tests covering archive ownership, recovery,
cache isolation, import responses, distributed publication and lifecycle replay.
A separate 18-test pass covered clock qualification, drop-counter availability
and silent-shard observation. Both use the final compiled API test binary.

The final telemetry reference passed the unchanged 60-second, 2,000/s workload
and 2,000 ms sample-age gate: P99 was 1,532.8 ms and maximum 1,535.0 ms.
Served, archived and live transactions all equal 123,134, with zero unreported
loss and zero failures across 100 concurrent archive-analysis reads. Browser
render P99 was 194.4 ms. See the [raw telemetry receipt](telemetry-reference.json);
its explicit measurement limitations still apply.

## Performance evidence

Controlled idle VUser footprints use the same Go toolchain, GOMAXPROCS=2 and
250/1,000/2,000 VUsers, with independent processes and external targets. All
twelve samples before and after have zero action errors. Network admission after
the fix is paced at 500/s to avoid saturating the macOS listener backlog.

| Protocol | Before Go KB/VUser | After Go KB/VUser | Before stack KB/VUser | After stack KB/VUser |
|---|---:|---:|---:|---:|
| Engine only | 24.18 | 19.58 | 16.04 | 16.02 |
| HTTP | 80.69 | 53.67 | 45.25 | 28.72 |
| WebSocket | 50.35 | 29.78 | 31.98 | 16.00 |
| Network | 41.10 | 20.52 | 32.00 | 16.02 |

Raw measurements: [before](footprint-before.json), [after](footprint-after.json).
These are local idle footprints, not throughput or production capacity claims.

Bounded synthetic correlation benchmarks (three 300 ms runs, median) changed
from 120.047 to 2.488 ms for plain text, 119.132 to 14.143 ms for JSON, and
197.226 to 9.964 ms for script bodies. Shared HAR analysis changed from 61.579 to
48.857 ms and 12.83 to 5.56 MB/op. These do not substitute for end-to-end timings
on the peer's private captures.

## Review record

Independent review covered importer compatibility, response consumers, transport
ownership, VUser lifecycle, cache isolation, archive recovery and telemetry
publication. Follow-up regressions address typed-JSON dialect compatibility,
bounded diagnostic locations, native-preview aliasing, repeated footprint sample
counts, stale follower/CI evidence and first-publication telemetry ownership.
The archive follow-up also fences group-filtered history, collection source
writes and recovery-state reads by run incarnation; retained-worker replacements
hydrate their own terminal metrics and preserve the historical observation state.
Area reviews and the final independent cross-cutting source review completed
with zero actionable findings after corrections. The last review covered
archive identity/cache interactions, telemetry publication fencing and
runtime/importer/UI/CI integration. The saved receipts and validation claims
were independently checked for consistency. Security was excluded from the
review criteria as requested.
