# Protocol diagnostics

The first gRPC diagnostics slice adds optional `server_telemetry.grpc` evidence
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
- Codec CPU time, server processing, connection phases, and HTTP2 stream timing
  are not inferred from total duration and are not exposed by this slice.

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

Per-method hotspot views, qualified baseline comparison, codec instrumentation,
deadline findings, and WebSocket diagnostic parity remain roadmap work. This
slice does not complete the protocol diagnostics milestone.
