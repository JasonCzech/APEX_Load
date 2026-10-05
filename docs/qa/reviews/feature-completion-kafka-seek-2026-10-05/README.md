# Kafka owned-seek review and qualification

The genuine Apache Kafka 3.9.2 single-broker fixture passed all four owned-seek cases on 2026-10-05. Final default/tagged and race suites and wrapper regressions pass; the documentation check is recorded below. This slice qualifies read-uncommitted seek with TLS and SASL PLAIN; it does not close MSG-3 or the feature-completion program.

## Implemented behavior

Real `kafka_seek` accepts earliest, latest and exact nonnegative offsets only for the configured consumer group and the complete current owned/previously-polled partition inventory. Polling proof is bounded and assignment-generation scoped, independent of whether the SDK's dirty cursor equals its committed cursor. Successful seek purges buffered delivery and explicit commit tracking; it writes no committed offsets. A real ownership-generation change during offset resolution refuses the seek before cursor mutation. Real `kafka_commit` refuses a different action group before broker I/O or commit telemetry; literal DSL checks and resolved template checks preserve this exact identity fence.

## Review, fixes and repeated verification

Independent review and regression coverage addressed:

- SDK `UncommittedOffsets` omits dirty-equals-committed cursors; independent delivered/polled proof preserves valid consecutive earliest/exact seeks while refusing unpolled or revoked partitions.
- Ordinary production acknowledgements do not set request/reply's private `sent` flag. Seed admission requires present finite nonnegative acknowledgement timing and exact topic/partition/offset/timestamp; consumed unique markers independently prove actual record contents.
- The oracle's 1 MiB broker-read limit requires a matching SDK fetch-size configuration even though it never configures a consumer. Only Kafka coordinator codes 14, 15 and 16 receive bounded retries; authentication, unknown and routing errors remain terminal. The final actual fixture observed one code-15 retry followed by a successful OffsetFetch.
- A serial proxy held the primary's shared ListOffsets/heartbeat connection and prevented genuine rebalance notification. The bounded pipeline now reads ahead, preserves exact response-write order, holds a genuine primary/group Heartbeat response with code 27, obtains the subsequent real ListOffsets response, and releases the heartbeat while the lookup remains blocked. Generation loss is proven during the hold; actual disjoint peer ownership is required after lookup release/refusal.
- Both holds require the exact primary client identity. Peer, malformed header, wrong topic, duplicate/missing partitions and wrong selector controls cannot consume the primary proof.
- Queue admission is limited to eight in-flight requests per connection; frame size, total bytes, connections and response age remain bounded. An already-expired release cannot extend the eight-second hold budget. Owned cancellation actively closes raw TLS sockets and explicitly joins the cancellation watcher, pipeline workers, peer and seek operations on failure paths.
- Real TLS lifecycle controls retain active trust failures and exclude owned-close cancellation from failure receipts. The independent Describe-only oracle never joins a group, consumes, produces, commits, or uses setup-control routes.

Offline synthetic wire tests prove only proxy ordering, admission, bounds and cancellation. They supply no provider offsets, assignments or message evidence to the genuine qualification test.

## Executed evidence

| Check | Result |
|---|---|
| Genuine Kafka owned-seek fixture | Pass: Go suite 2.487 seconds; complete harness 25.635 seconds |
| Complete default Kafka and DSL suites | Pass: Kafka 15.440 / DSL 0.325 seconds; [receipt](evidence/final-default-kafka-dsl-20261005.json) |
| Complete `kafka_real` Kafka and DSL suites | Pass: Kafka 15.509 / DSL 0.377 seconds; [receipt](evidence/final-real-kafka-dsl-20261005.json) |
| Complete default Kafka and DSL race suites | Pass: Kafka 16.751 / DSL 1.638 seconds; [receipt](evidence/final-default-race-kafka-dsl-20261005.json) |
| Complete `kafka_real` Kafka and DSL race suites | Pass: Kafka 16.797 / DSL 1.664 seconds; [receipt](evidence/final-real-race-kafka-dsl-20261005.json) |
| `kafka_real,kafka_seek_integration` owned helper race controls | Pass: 1.750 seconds; [receipt](evidence/final-owned-helpers-race-20261005.json) |
| Python fixture/wrapper regressions | Pass: 18 tests; [receipt](evidence/final-python-wrapper-20261005.json) |
| Documentation checks | Pass: 128 active Markdown files; [receipt](evidence/final-docs-20261005.json) |

The [final actual report](evidence/actual-broker-repeat-5.json) and [matching test log](evidence/actual-broker-repeat-5.tests.txt) record the pinned image/platform provenance, Linux arm64 single KRaft broker, 1 CPU/1,536 MiB container ceiling, guarded data directory, exited/reaped test group and verified removal of owned Docker resources and scratch. The execution proxy's peak in-flight request count was two. The independent oracle observed only permitted read-only metadata/coordinator/offset/authentication APIs.

The four genuine cases cover multi-partition earliest/latest/exact seek with following SDK-buffered and real adapter-pending records, wholly/partially unpolled and unowned refusal, configured versus other/denied action groups, and broker-driven revocation during held offset lookup. Initial committed next positions were 1 on every partition; later explicit commit baselines were 9 and 10. Seeks and refusals retained those actual broker positions. The final revocation changed generation 22 to 26, left partitions 0/1 with the primary and assigned partition 2 to the peer. No assignment callback or broker response was fabricated.

Earlier failed attempts are retained: [oracle construction](actual-broker-initial.json), [coordinator startup](actual-broker-repeat.json), [seed acknowledgement](actual-broker-repeat-2.json), [joint barrier](actual-broker-repeat-3.json), and [diagnostic heartbeat blocking](evidence/actual-broker-repeat-4.json), each with its matching `.tests.txt` log. Copied earlier tool outputs remain historical context and do not replace final raw gate receipts. Final gate logs are preserved byte-for-byte with SHA-256, command, source-file hashes, timing and pinned environment in `evidence/final-*.json`.

## Remaining qualification

Read-committed last-stable-offset seek, consume-transform-produce atomicity, producer fencing, transaction crash/restart, multi-broker failover, other broker versions, other authentication profiles, hosted providers and Event Hubs Kafka endpoints remain open. Prior Kafka management-reader PLAIN/SCRAM qualification is separate and does not qualify these execution cases. The full repository gate for the composed program remains a separate merge gate. Shared build caches remain available while dependent verification continues.
