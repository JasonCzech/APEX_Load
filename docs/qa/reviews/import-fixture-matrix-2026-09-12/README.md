# Import conversion and replay fixture matrix

Date: 2026-09-12. This follows the [Import From Source usability audit](../import-source-audit-2026-09-12/README.md). The matrix validates observable source behavior and records unavailable evidence separately.

## Result

**16 of 20 cases passed; 4 are blocked; no executed case failed.** The final matrix includes real Kafka and Solace round trips, three attached canonical Bruno SOAP assets, source preservation, mock messaging, and enterprise execution constraints. It does not certify every customer application or full original-tool parity.

The source-backed [manifest](../../import-fixture-matrix.json), [machine-readable results](evidence/results.json), per-case logs, source hashes, and [reproduction runbook](RUNBOOK.md) make each claim inspectable.

## Matrix

| Case | Result | Provenance | Evidence scope |
| --- | --- | --- | --- |
| bruno-attached-preservation | [PASSED](evidence/bruno-attached-preservation.log) | repository-attached | Byte preservation, variables, headers, body and scripts across three attached SOAP sources |
| bruno-canonical-replay | [PASSED](evidence/bruno-canonical-replay.log) | repository-attached-with-synthetic-environment | Attached source → encrypted bundle → Go replay → pinned Node runner → loopback SOAP target |
| bruno-runner-conformance | [PASSED](evidence/bruno-runner-conformance.log) | synthetic | Pinned runtime behavior and egress/lease/cleanup contracts against local test servers |
| bruno-commit-revisions | [PASSED](evidence/bruno-commit-revisions.log) | synthetic | Canonical inspection, persisted source revision, synchronization and rollback |
| asyncapi-mock-replay | [PASSED](evidence/asyncapi-mock-replay.log) | synthetic | Kafka JSON/Avro/request-reply, JMS publish/consume and mixed-provider split; in-memory brokers |
| broker-topology-mock-replay | [PASSED](evidence/broker-topology-mock-replay.log) | synthetic | MQSC and JNDI topology drafts replay through mock IBM MQ/Solace adapters |
| capture-mock-replay | [PASSED](evidence/capture-mock-replay.log) | synthetic | Capture Avro → persisted/reloaded DSL → mock Event Hubs produce/observe |
| provider-adapter-contracts | [PASSED](evidence/provider-adapter-contracts.log) | synthetic | Real adapter builds and local/unit transport contracts; no live provider certification |
| enterprise-persisted-gates | [PASSED](evidence/enterprise-persisted-gates.log) | synthetic | CloudTest, SAP GUI and disabled Java runner reject execution after draft persistence |
| cloudtest-commit-attestation | [PASSED](evidence/cloudtest-commit-attestation.log) | synthetic | CloudTest commit cannot replace unproven mappings with unattested executable actions |
| enterprise-runtime-fidelity | [PASSED](evidence/enterprise-runtime-fidelity.log) | synthetic | Source runtime options that are not implemented cannot claim execution parity |
| truclient-conversion | [PASSED](evidence/truclient-conversion.log) | synthetic | TruClient archive inventory and preservation; supported browser subset is not universally gated |
| java-conversion-lifecycle | [PASSED](evidence/java-conversion-lifecycle.log) | synthetic | Java VUser and JMS-family sources retain combined lifecycle, manifests and runner constraints |
| pinned-public-corpus | [PASSED](evidence/pinned-public-corpus.log) | public-pinned-corpus | Pinned public LoadRunner/DevWeb/JMeter corpus conversion and protocol posture; no customer replay parity |
| kafka-live-import-replay | [PASSED](evidence/kafka-live-import-replay.log) | synthetic-live-broker | Imported AsyncAPI order event → real Kafka broker → exact consumed JSON, isolated topic and cleanup |
| solace-live | [PASSED](evidence/solace-live.log) | synthetic-live-provider | Imported JNDI topology → live Solace AMQP queue → exact consumed payload |
| servicebus-live | BLOCKED | synthetic-live-provider | Live Azure Service Bus queue publish/consume; topic/session cases remain separate |
| eventhubs-live | BLOCKED | synthetic-live-provider | Existing live Event Hubs produce/observe smoke; does not assert exact marker identity |
| ibmmq-live | BLOCKED | customer-provider-needed | IBM MQ provider certification |
| enterprise-customer-replay | BLOCKED | customer-fixtures-needed | Customer-authenticated enterprise replay parity |

## What the replay evidence establishes

- **Canonical Bruno:** all three attached SOAP sources are imported without rewriting their source files, saved as encrypted immutable bundles, resolved through the real artifact store, and executed through the Go replay engine and pinned Bruno 3.5.2 runner. Synthetic environment values direct requests to a loopback target. Exact request paths, SOAP bodies, headers and basic-auth values are checked. Original source assertions pass for the expected HTTP 500 SOAP fault and fail when its fault code is altered. The literal attached session identifier is replaced with a synthetic binding for execution.
- **Kafka:** a synthetic AsyncAPI order contract converts to publish/consume actions. A real Kafka 4.2.0 broker returns the exact unique order marker, numeric amount, currency and item array, with an actual offset. Each run creates and deletes its own topic.
- **Solace:** a JNDI queue definition converts to a draft, which is configured for a real Solace 10.26.0.8827 AMQP broker. The replay engine publishes and consumes the exact unique JSON payload using synthetic credentials and a dedicated queue. This is queue round-trip evidence, not certification of all selector, transaction, topic or HA behavior.
- **Mock messaging:** AsyncAPI exercises Kafka JSON/Avro/request-reply, JMS flows and mixed-provider splitting. MQSC/JNDI topology drafts preserve each destination's own payload. Capture Avro fixtures retain Unicode/binary payload content and application properties after DSL JSON persistence. These are explicitly in-memory results.
- **Enterprise:** persisted CloudTest drafts still fail on `cloudtest_fixture_mapping_unproven`; SAP GUI still has no registered runtime; Java lifecycle execution is rejected when its runner is disabled. Generic CloudTest commit cannot substitute unattested executable actions. Separate Java source tests retain lifecycle and protocol-family behavior for the governed runner.
- **Public corpus:** all 26 source directories at pinned LR_Scripts revision `cc010f01ea6f0120d1e22bc1acb41522bf98d03d` pass conversion smoke checks across 11 source families. Public examples are not customer fixtures, and conversion is not replay equivalence.

## Defects and corrections

### Fixed: Solace AMQP login identity changed when a VPN was set

The live test failed with a configured VPN and succeeded with the identical broker, queue and credentials when that field was omitted. The adapter appended `@vpn` to the SASL username. Removed this rewrite; the exact-payload test then passed with `SOLACE_VPN=default`. A local SASL peer regression test checks the transmitted identity for ordinary usernames, names containing `@`, and surrounding whitespace.

This aligns with Solace's documented [per-VPN AMQP listen ports](https://docs.solace.com/Services/AMQP-on-VPN-Management.htm) and [TLS virtual-hostname routing on global ports](https://docs.solace.com/Services/Managing-Services.htm). No broader authentication policy or production broker configuration changed.

### Fixed: public corpus test demanded source families absent from its pin

The existing corpus test converted all 26 directories, then failed because it demanded Java/JMS examples that do not exist anywhere in the pinned repository tree. The required corpus families now match that immutable source set. Java coverage remains a separate explicit matrix case; it was not relabeled as public-corpus evidence.

### Clarified: TruClient is not universally execution-blocked

The repository's simple TruClient navigation fixture converts into supported browser actions. Its source-tool branding alone does not imply a durable execution blocker. The matrix therefore records archive inventory/preservation and limits, without claiming complete browser replay parity or weakening any unsupported-feature gates.

## Remaining gaps and completion criteria

| Blocked case | Needed to complete it |
| --- | --- |
| Service Bus | Dedicated test queue and connection configuration; execute exact-payload queue round trip. Topic, session and request/reply fidelity require additional fixtures. |
| Event Hubs | Dedicated namespace/hub and credentials. Strengthen the current live smoke to identify the just-published unique payload before calling it fidelity evidence. |
| IBM MQ | Supported live client/sidecar, dedicated queue, and baseline behavior for acknowledgment, selectors and transactions. Mock topology replay is insufficient. |
| Customer enterprise replay | Dependency-complete customer CloudTest/TruClient/Java/Oracle exports, source-tool versions, original-tool expected results, approved runner configuration and controlled target applications. |

No new customer fixture paths or cloud-provider configuration were supplied during this run. The three repository-attached SOAP files retain their source provenance; their controlled synthetic responses establish assertion behavior, not the behavior of a customer backend.

## Validation and cleanup

- Full Go suite passed: **105 packages**, **10,812 top-level tests passed**, 4 top-level environment-dependent skips and 11 packages without tests. See [validation record](VALIDATION.json).
- The matrix passed 16 cases without skipped tests inside those passing cases. It separately records four unavailable cases as blocked. Exit 0 never implies those gaps are covered.
- Seven Python harness tests passed, covering empty/missing tests, skips, failures, live prerequisites, bounded timeouts, output capture and manifest references. The pinned Compose configuration validates.
- The first full Go attempt ran out of disk space. About 8 GB of unused Docker build cache older than 24 hours was reclaimed; the full suite was rerun successfully. No source files or application data were removed.
- An initial Node reporter mismatch was corrected by requesting explicit TAP output. Broker setup was corrected to explicitly provision Kafka's test topic and enable internal authentication on the disposable Solace VPN. These harness/setup failures are not product conversion defects.
- Both task-owned broker containers and their anonymous volumes were removed after the final matrix. The local fixture runner, HTTP target and temporary artifact store are cleaned up by their tests. No customer service was used as a successful replay target.
- No dashboard code changed in this follow-up. The earlier UI audit's browser evidence remains separate from these conversion/runtime checks.
