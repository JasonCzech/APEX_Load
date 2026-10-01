# Protocol diagnostics

gRPC diagnostics add optional `server_telemetry.grpc` evidence
to transaction records and live summaries. Generators advertise
`grpc_diagnostics_v1` when the gRPC runtime is registered. Older peers and
archives can omit this field; omission means unavailable.

## Measurement semantics

- All four call modes are measured: unary, server streaming, client streaming,
  and bidirectional streaming. Reflection is excluded from authored RPC counts.
- Method names are canonical `/package.Service/Method` identities, validated and
  bounded to 256 bytes. Request bodies, headers, and credentials are not labels.
- `call_ms` is a client-observed call window after method resolution, ending at
  the transport's terminal event when available. For a locally bounded stream,
  it ends when observation stops. It is not server processing time.
- `first_message_ms` measures time to the first received protobuf message.
  No response means unavailable, including on deadline and cancellation errors.
  `deadline_budget_ms` records the budget remaining when the call starts.
- Counts and bytes come from gRPC's payload hooks, including partial transfers
  and transport retries. Bytes describe uncompressed protobuf payloads and exclude
  gRPC/HTTP2 framing. These are client transport observations, not proof of server
  application receipt. Legacy transaction byte fields retain their JSON meaning.
- `status_code` is present only after a terminal RPC status is observed. A
  `max_messages` cap or streamed JSON response byte cap sets
  `receive_limit_reached`; neither establishes successful remote completion.
  Assertions can fail an action with an OK RPC.
- Bidirectional calls send and receive concurrently. Hitting a receive limit
  cancels outstanding I/O and joins the sender. If requests remain unsent, the
  action fails explicitly rather than claiming all authored messages were sent.
- `encode_ms` and `decode_ms` measure protobuf codec wall time accumulated per call. They exclude DSL JSON conversion, compression and transport framing; an unavailable codec measurement is omitted. Server processing, connection phases and HTTP/2 stream timings are not inferred from total duration.

## Analysis and compatibility

The dashboard shows client call means/maxima, first-response means with their
own measurement counts, status distributions, and receive-limit counts. Archive
transaction details show method, mode, deadline budget, sizes, and availability.
The optional evidence is retained in existing JSONL and Parquet transaction
artifacts; Parquet uses the existing `server_telemetry_json` column.

Typed timing summaries retain count/sum/min/max and 144 bounded logarithmic
buckets with upper bounds `0.001 * 1.2^i` milliseconds (last bucket is overflow).
Buckets can be pooled; transaction percentiles are never averaged. Quantile
estimates use bucket upper bounds, capped by the observed maximum. Each optional
timing has its own denominator. Diagnostic summaries describe retained samples;
they must not be interpreted as complete traffic counts when raw telemetry has
been sampled or dropped. Existing authoritative transaction counters remain the
source for throughput and total error rates.

Method hotspot and deadline/status findings are advisory and appear in live cards,
archives, reports and root-cause evidence. Qualified baseline comparisons match
protocol and stable transaction/method identity, retain workload qualification,
and compare mergeable timing distributions. Explicit SLA policies remain responsible
for stopping tests and failing CI.

## WebSocket diagnostics

Optional `server_telemetry.websocket` evidence records handshake/reconnect,
application send and receive wait, write-lock wait, pending application writes,
message counts and payload sizes, and bounded disconnect classifications. Authored
local closes are distinguished from abnormal peer closes. The callback loop retains
read ownership. Receive wait is not round-trip latency; only matched authored
ping/pong messages produce RTT samples. No health-probe traffic is added.

Distributions merge through generator RPC, live summaries, JSONL/Parquet archives,
HTML/PDF/CSV reports and qualified baseline comparisons. Text and binary receive
waits retain separate distributions. Findings identify observed disconnects,
timeouts and write pressure without asserting that an idle connection is dead.
Statistical anomaly delivery and complete dependency intelligence remain open.
