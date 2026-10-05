# Kafka fresh-source LSO and transaction qualification — 2026-10-05

This bounded MSG-3 slice fixes runtime isolation admission and truthful teardown accounting and qualifies a separate genuine Apache Kafka 3.9.2 single-KRaft-broker TLS/SASL PLAIN profile. It does not close all Kafka semantics or the feature-completion program. All affected checks below pass and final independent source review is clean. The full composed repository gate is required before integration.

## Delivered behavior

- Direct runtime initialization rejects unsupported, non-string and null explicit isolation values before constructing any SDK/mock client. Empty isolation retains the existing default; supported values are normalized.
- Teardown always attempts physical client cleanup, retains both abort and cleanup errors, counts only confirmed rollback, clears buffered output and makes an uncertain transaction outcome terminal. Normal and real-lazy regressions cover admission, error joining, namespace release and repeated cleanup.
- Latest seek in `read_committed` uses the actual last-stable next position and does not commit group offsets. Owned/polled admission remains required; seek stays unavailable inside consumer-transaction mode.
- Consumer transactions use the existing pinned SDK transaction session, a single input topic, an approved group, `read_committed` and a fresh per-VUser transactional identity. Commit reconciles visible output with input committed-next; abort discards locally buffered output and the real SDK rewinds input for redelivery.
- A separate genuine producer initializes a newer epoch for the actual runtime-resolved ID after a successful warmup transaction. The stale runtime must fail with the pinned `PRODUCER_FENCED` or `INVALID_PRODUCER_EPOCH` signature, commit no new input, expose no failed output to `read_committed`, and refuse further execution.

## Genuine acceptance and independent evidence

The new explicit `kafka_real,kafka_transaction_integration` tag fails when its protected fixture settings are missing. The wrapper `scripts/test-kafka-transactions.sh --report <unused-report-path>` requires the parent and all three subtest PASS markers; missing/skipped cases cannot qualify.

| Case | Actual provider evidence |
| --- | --- |
| `read_committed_latest_lso` | Independent fixed ListOffsets reads observe high-watermark greater than LSO during a real open transaction. After production latest seek, committing that transaction delivers its record at the former LSO (offset 1). A second broker-aborted record at offset 3 is skipped to the following acknowledged record at offset 5. Group commits remain unchanged. |
| `consume_transform_commit_abort` | Production public Begin/Consume/Produce/Commit actions atomically reconcile output and committed input, with commit count 1. Explicit abort count 1 retains committed-next and actual SDK redelivery of the same input. Ordinary commit and seek are refused in this mode. Output is buffered in Go until commit: this abort case proves buffered-output discard plus broker-backed input rewind, rather than claiming the buffered output was sent then broker-aborted. |
| `stale_producer_fencing` | Actual independent producer epoch takeover fences the old runtime, with a fixed Kafka fencing error and unchanged committed input. Ordered `read_committed` output scans exclude stale failed output and the separately broker-aborted contender record. |

The Describe-only oracle never consumes, joins a group or mutates offsets. It uses exact approved topic/group/partition inventory, numeric TLS routes, fixed ListOffsets isolation 0/1 and stable OffsetFetch. The independently authenticated output reader has only output-topic Read/Describe, no group configuration or join, and scans from the start to a separately acknowledged final barrier. Absence is never inferred from an empty poll or a sleep. Fixture writers have only the approved Write/Describe topics and short transactional-ID prefix; execution/transaction grants do not broaden management authority.

The previously qualified [four-case owned-seek profile](../feature-completion-kafka-seek-2026-10-05/README.md) remains separate. Shared transport admits only fixed transaction APIs 24/25/26/28 when explicitly enabled by this new fixture; its default seek authority is unchanged and tested. Offline constructor, wire-role, scope, ordering, retry and race tests qualify harness controls; they do not replace genuine broker acceptance.

## Retained attempts and verification

- [Initial failed receipt](evidence/actual-broker-initial.json) and [raw sanitized test output](evidence/actual-broker-initial.tests.txt): LSO passed; transaction reconciliation failed because the new oracle did not request stable transactional offsets. Cleanup was verified.
- [First successful repeat](evidence/actual-broker-repeat-1.json) and [test output](evidence/actual-broker-repeat-1.tests.txt): all three cases passed. The broker returned actual `UNSTABLE_OFFSET_COMMIT` code 88 twice before stable offsets were available, establishing the oracle correction. SDK/kmsg semantics and bounded retry regressions retain immediate refusal for authentication, routing, unknown and cancellation failures.
- [Final genuine receipt after explicit observer membership defaults](evidence/actual-broker-final-repeat.json) and [test output](evidence/actual-broker-final-repeat.tests.txt) pass the strengthened fixed-code fencing assertion: actual `PRODUCER_FENCED` code 90, input-next 1 unchanged, commit/abort counts 1 and SDK redelivery advancing input-next to 2. [Original four-case owned-seek final rerun](evidence/owned-seek-regression-final.json) passes independently, with [outer command/source provenance](evidence/owned-seek-regression-final-run.json). Its preceding passed broker receipt remains retained alongside the [recorder invocation-error note](evidence/owned-seek-recorder-invocation-error.json): the outer recorder and inner harness were accidentally given the same JSON path; the recorder refused overwrite and the repeat uses distinct paths. Affected normal/race/Python/documentation receipts are recorded in `evidence/`. Receipts preserve raw bounded output, source hashes, command, toolchain/profile and cleanup. Failed attempts remain retained.

| Final check | Result / receipt |
| --- | --- |
| Default Kafka / DSL | PASS 15.548 s / 0.316 s — [receipt](evidence/final-default.json) |
| `kafka_real` Kafka / DSL | PASS 15.522 s / 0.356 s — [receipt](evidence/final-real.json) |
| Full affected default races | PASS 16.742 s / 1.918 s — [receipt](evidence/final-default-race.json) |
| Full affected `kafka_real` races | PASS 17.042 s / 1.711 s — [receipt](evidence/final-real-race.json) |
| Explicit fixture-control races on final source | PASS 1.906 s — [receipt](evidence/final-helpers-race-repeat.json) |
| Python fixture controls | PASS 22 tests — [receipt](evidence/final-python.json) |
| Documentation | PASS 129 active Markdown files — final receipt in `evidence/` |

The default/`kafka_real` checks do not compile the separately tagged fixture; its final tagged races and genuine repeat qualify the subsequent fixture-only assertion/default corrections. Runtime production source remained frozen throughout final checks. [Final source/evidence inventory](evidence/verification-provenance.json) distinguishes immutable actual receipts, recorder-control checks and the unresolved composed repository gate.

The fixture pins the Kafka registry/index/platform digest, one CPU, 1.5 GiB container memory, protected credentials, exact loopback listeners, two owned volumes/network/container, a 2 GiB data guard, two-minute actual-test context and bounded socket ownership and outer wall/log/disk limits. Tests do not use the anonymous setup listener. The wrapper owns/reaps the whole test process group and independently verifies container/volume/network removal and protected scratch deletion. No source, dependencies, deliverable images or shared build caches are pruned here.

## Explicit remaining gates

Fresh-source qualification does not establish durable logical-identity continuation, broker restart/retry or failover, transactional revocation/cancellation outcomes, multiple input topics, multi-broker clusters, other broker versions/authentication, hosted brokers or Event Hubs Kafka tiers. HTTP/database side effects are outside Kafka atomicity. Full repository composition remains required before integration; no unrelated provider, platform or analytics gate is certified by this record.

## Opt-in inventory registration follow-up

The composed repository gate found missing opt-in inventory/runner registrations.
Both `kafka_seek_integration` and `kafka_transaction_integration` now have explicit
rows in [TEST_PROGRAM](../../TEST_PROGRAM.md#build-tag-gates) and separate
`--all` runner entries calling the existing bounded wrappers. Each wrapper gets
a fresh private report directory, printed for receipt discovery. Default `SKIP`
and `--list` establish no qualification; an explicitly selected wrapper failure
remains a failed opt-in suite.

The [focused inventory/runner gate](evidence/optin-registration-go-final.json)
passes, and the new offline Bash regression proves no fixture invocation for
list/default runs, both wrapper invocations under `--all`, isolated retained
receipt paths and propagation of required-fixture failures without a provider
PASS. [Python controls](evidence/optin-registration-python.json) pass 22 tests;
[shell syntax](evidence/optin-registration-shell.json),
[actual inventory listing](evidence/optin-registration-list.json) and
documentation checks pass. Direct
[owned-seek](evidence/optin-registration-negative-seek.json) and
[transaction](evidence/optin-registration-negative-transactions.json) tagged
runs with their fixture variables unset deliberately return exit 1 and parent
FAIL, with no PASS/SKIP markers. These negative checks validate refusal before
broker access and do not replace the genuine passing receipts above. This small
registration follow-up changes no Kafka runtime, broker fixture or prior
qualification scope; the composed full repository gate remains required.
