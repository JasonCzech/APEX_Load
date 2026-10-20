# Non-Messaging protocol telemetry matrix

This is the measurement inventory for PROTO-A/PROTO-B in the
[implementation spec](../ROADMAP_IMPLEMENTATION_SPEC.md#proto-b--controlled-endpoint-qualification),
under the [diagnostics](../PROTOCOL_DIAGNOSTICS.md) and
[support](../PROTOCOL_SUPPORT.md) contracts. It records the exact local cohort
verified by the [Java admission and protocol review](reviews/roadmap-java-encoding-admission-2026-10-19/README.md) and the [Java representation review](reviews/roadmap-java-representations-2026-10-20/README.md).
It does not close the protocol roadmap row. Messaging and CloudTest outcome
parity remain excluded.

## Local measurement and failure inventory

All endpoint fixtures below run on one darwin/arm64 host with pinned Go modules.
They use synthetic loopback endpoints; no customer capture is replayed. Exact
commands, runtime/module versions, source hashes, passing test names and failed
before-fix evidence are retained in the review. The tests establish functional
semantics for those fixtures, not resource overhead or deployed performance.

| Measurement / boundary | Independent fixture and tests | Retained conclusion and limit |
| --- | --- | --- |
| Java HTTP opaque request bytes and HTTP body bytes | [Body conversion](../../pkg/protocol/java_http/body_conversion_test.go): `TestJavaHTTPBodyIsDecodedExactlyOnce` sends fixed binary, base64-looking and empty payloads; the endpoint independently reads and decompresses gzip bytes and checks the adapter byte count. | Exactly one base64 decode and actual HTTP body-byte accounting. Does not measure a JVM object, serialized object construction, transport framing or a separate retained encoded/decoded byte comparison. |
| Java HTTP input rejection and failed HTTP outcome | [Conversion failures](../../pkg/protocol/java_http/body_conversion_test.go): malformed/truncated base64 has zero endpoint calls; HTTP 500 preserves failure and a redacted snapshot. [Encoding admission](../../pkg/protocol/java_http/encoding_admission_test.go) checks nil, empty, whitespace and nonempty bodies against unsupported encodings, plus supported empty-body requests. | Unsupported explicit encodings fail before target traffic even with an empty body. Caller input and preflight diagnostics survive. Empty/default base64 remains supported. |
| Java HTTP legacy conversion timing | [Codec tests](../../pkg/protocol/java_http/java_http_test.go): `TestJavaHTTPTimingDistinguishesCompletedAndSkippedOperations`, request decode/response encode and response mapping cases. | Legacy fields describe the embedded adapter's base64 work; they are not JVM serialization/deserialization measurements. No new wall-time ratio or footprint qualification. |
| gRPC client-call counts, status and optional timing | [Diagnostic endpoint](../../pkg/protocol/grpc/diagnostics_test.go): unary, client/server streaming and bidi with known message counts/statuses; deadline/cancellation, local receive/byte caps, early rejection, delayed statistics and invalid later messages. | Client-call windows and available codec/first-message observations, including partial outcomes. Missing terminal status stays missing after a local cap. Does not establish HTTP/2 wire bytes, server phases, retry-attempt qualification or measured overhead. |
| WebSocket matched ping/pong and application work | [Diagnostic endpoint](../../pkg/protocol/websocket/diagnostics_test.go): sends an unmatched pong, then the authored matching pong, followed by ordinary data; fixed seven-byte message. [Lifecycle fixtures](../../pkg/protocol/websocket/websocket_test.go) and [callback fixtures](../../pkg/protocol/websocket/callbacks_test.go) cover reconnect, explicit/peer close, timeout, cancellation and queue overflow. | Only matched authored pong produces RTT; ordinary receive wait stays separate. Application queue pressure is not transport queue depth. No probe traffic is introduced. |
| gRPC generator and archive propagation | [RPC round trip](../../pkg/distributed/generatorrpc/grpc_diagnostics_test.go), [local/distributed and JSONL-to-Parquet agreement](../../pkg/api/grpc_diagnostics_test.go). | Optional zero, absent first response, client summaries and archive JSON/Parquet evidence survive the tested paths. Does not qualify every report, baseline or native-query consumer. |
| WebSocket generator propagation | [Wire evidence](../../pkg/distributed/generatorrpc/websocket_diagnostics_test.go), [clone ownership](../../pkg/distributed/generatorrpc/clone_test.go). | Tested optional evidence survives the DTO boundary without mutable aliasing. Full archive/query/report/baseline qualification is still required. |

## Versioned Java opaque representations

The representation review records the applied source and generated artifact hashes,
commands, failures and repeat results. These are synthetic local functional fixtures;
the browser tests stub diagnostic responses while exercising real authenticated
navigation and panel persistence. They do not execute a Java target.

| Boundary | Independent fixture / consumer tests | Scope and limits |
| --- | --- | --- |
| Acquisition and body preparation | [Representation observations](../../pkg/protocol/java_http/representation_diagnostics_test.go), [HTTP body observer](../../pkg/protocol/http/body_observation_test.go) | Known binary, empty, malformed and unsupported inputs; endpoint-read body and compression; response retention, failed calls and monotonic conversion work. First preparation excludes refresh/retry traffic and never proves delivery. |
| Action summaries and typed RPC | [Actual engine and metrics stream](../../pkg/distributed/generatorrpc/java_http_summary_test.go), [summary bounds](../../pkg/protocol/java_http_summary_test.go), [action envelope](../../pkg/protocol/java_http_actions_test.go) | Independent action counts, optional zeros, old/invalid peers, fixed histograms, exact-sum limits and bounded refusal. Synthetic business wrappers do not duplicate actions. |
| Archives, history and reports | [JSONL/Parquet/live/report agreement](../../pkg/api/java_http_diagnostics_test.go), [SQLite migration/restart](../../pkg/storage/java_http_history_test.go), [PostgreSQL migration/round trip](../../pkg/storage/java_http_history_postgres_integration_test.go) | Versioned raw/summary evidence, compression, timing and response availability survive their named paths. Old history stays absent; invalid strings and payload sentinels are not retained. |
| Native paired and scalar queries | [Native archive fixtures](../../pkg/api/native_java_http_test.go), [paired evaluator](../../pkg/analysis/java_http_pairs_test.go) | One exact action; independent scalar observations and same-sample paired sums. Missing, collection-window, incomplete coverage, mixed compression and zero denominators remain unscored. No generic scalar ratio, grouping, live rate, drill-down or SLA claim. |
| Baselines and exports | [Archive reader, advisory findings and exports](../../pkg/api/baseline_java_http_test.go) | Own per-run populations, actual-action filtering, compatible compression/window and at least 100 complete samples for findings. HTML escaping, bounded text disclosure and structured CSV; no Java acceptance verdict. |
| Dashboard consumers | [Java workflows](../../dashboard/e2e/workflow-java-representation.spec.ts), [summary formatting](../../dashboard/src/utils/javaHttpDiagnostics.test.ts) | Live absence/partial/measured-zero, dedicated archive report, saved native panel and CSV, baseline detail and CSV under both authentication profiles. Help remains independently checked. |

## Remaining closure gates

| Gate | Status and required evidence |
| --- | --- |
| Java versioned encoded/decoded/body comparison and outer interval | Version 1 opaque representations are implemented; applied-source validation is recorded by the [representation review](reviews/roadmap-java-representations-2026-10-20/README.md). The operator-approved JVM helper and external runtime qualification remain separate open gates. |
| Java object footprint and actual codec work | Pending an operator-approved pinned helper, bounded IPC/containment and independently known objects on each supported JVM. Shared/cyclic graphs, traversal exhaustion, missing instrumentation and helper death/cancellation remain required. Embedded opaque-byte execution is footprint-unavailable. |
| HTTP and complete non-Messaging consumer parity | Pending a measurement-by-measurement inventory for acquisition, merge, live/archive, native query, baseline, report and findings, including old-field absence and mixed peers. Passing the rows above does not fill missing paths. |
| gRPC retries, concurrency and byte ground truth | Existing local streaming tests are regression evidence. Explicit independent encoded-payload counts, retries/attempt populations and overlapping codec work still need reviewed qualification. Do not clamp overlap to 100% or call payload bytes wire bytes. |
| WebSocket backpressure and lifecycle qualification | Local callback/close fixtures are regression evidence. Bounded write-pressure ground truth and the full retained consumer chain remain pending. |
| Redaction and overhead | The Java failure snapshot and existing endpoint tests cover their named cases only. Telemetry-specific forbidden sentinels across all projections and bounded measurement overhead are separate pending gates. |
| Runtime/platform cohorts | No separate-host, native-platform or production performance claim. Each advertised JVM/engine/runtime cohort needs its own evidence and limits. |

A later qualification appends a dated review and updates only its measured row.
Absent measurements remain unavailable; sampled diagnostic counts never become
execution throughput. Existing field meanings and old archives remain readable.
