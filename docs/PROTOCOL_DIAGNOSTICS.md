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

## Messaging dimensions and acknowledgements

Messaging action summaries carry optional bounded `messaging` evidence keyed by protocol family, execution group, source, observed partition and subscription. Execution groups keep independently configured brokers with equal source names separate; labels do not establish broker namespace coverage. At most 64 intersections per action and 128 action populations are retained. Invalid evidence, overflow and legacy peers mark coverage incomplete. Generator capability `messaging_rollup_v1` carries versioned mergeable sums/counts/maxima through RPC and live snapshots; JSONL and Parquet interval artifacts preserve these summaries. Reports aggregate retained raw observations separately, so report sample counts can differ when raw evidence was sampled or dropped.

Message age, offset backlog and acknowledgement duration each have independent observation counts. Observed zero remains zero; missing measurements remain unavailable. These moments do not produce percentiles. Successful real Kafka publish acknowledgements (except no-ack mode), Event Hubs batch sends and supported JMS settlement calls measure client elapsed time, including client queue/wait. Mock operations and unsupported adapters do not invent acknowledgement measurements. Subscription identity denotes an observed consumer group or subscription, rather than a broker inventory.

Partition-skew goals require both `messaging_rollup_v1` and `messaging_scope_v1` and complete known evidence. Legacy summaries without protocol scope remain readable but unscored. They score coefficient of variation over at least two observed producer partitions within one protocol/group/source; unknown partitions, mixed source populations, dropped windows and incomplete summaries are unscored. Distributed archive windows also remain incomplete when a known generator misses the alignment window, provides no intervals, or cannot be collected; surviving observations remain available. This does not establish broker-wide partition coverage. The existing legacy scalar display is explicitly unqualified. No provider management queue-depth polling or additional target probes are introduced. Live cloud acceptance and Event Hubs processor/checkpoint ownership remain open; IBM MQ remains low priority.

Synthetic business-transaction wrappers do not add broker operation counts to action reports. Combined request/reply observations retain measured counts and moments without attributing reply partitions or age to the request source; their combined dimensions are incomplete and unscored. Event Hubs sampled lists count actual sent messages rather than action samples. Batch partition attribution requires a pinned partition; unpinned batches retain volume with unavailable partition identity.

### Scoped archive SLA and baseline acceptance

Terminal `transaction_rules` may select `population: action` plus exact `protocol` and `group`; omitted population retains named-transaction behavior. `p99_duration_ms` and `max_acknowledgement_ms` join latency, errors, successful TPS and independent consumer-age/offset measurements. Scoped rules never borrow another group's observations. Missing collector archives or unavailable protocol declarations yield `not_evaluated`; acknowledgement/lag rules require every selected observation to be measured. For browser sessions that keep their context, `iterations` selects the iterations a rule judges: warm ones by default (`warm`), `all`, or `cold` (each session's first iteration in a browser context); a session that starts every iteration afresh is never tagged cold, so every value but `cold` judges it. The per-group transaction response-time SLA also judges warm iterations only. Percentile rules (`p90_duration_ms`, `p95_duration_ms`, `p99_duration_ms`) are exact through 20,000 matching observations and come from a compression-1,000 digest beyond that. The report's transaction and request tables use the same calculation within a 40 MiB budget per report; names past that budget are exact through 1,024 observations only.

Saved baseline comparisons include a versioned `verdict` separate from advisory findings. Default acceptance measures successful P95, successful throughput and error percentage points. `regression_metrics`, `regression_protocols`, `regression_groups` and `regression_identities` select a bounded explicit scope, including typed gRPC/WebSocket P95 measurements and messaging acknowledgement/age/offset means. Means merge observation sums and counts; they never imply a percentile. Latency needs 100 successful observations (1,000 for P99); other measures need 100 attempts and complete independent telemetry. Workload, version, window and source-coverage differences prevent an automatic pass. A zero baseline without an absolute threshold remains unavailable.

Use `apexctl analysis baseline --id RUN --file request.json --fail-on-regression` to print the saved assessment and gate CI: exit 1 for a regression, 3 for unavailable acceptance evidence, and 0 only for a fully evaluated pass. The dashboard preserves the same acceptance scope and shows the saved verdict, while reports and evidence exports retain it. This opt-in gate does not change advisory anomaly behavior or existing run SLA policies.

### Real Kafka replay and consumer transactions

The `kafka_real` adapter advertises `kafka_real_seek_v1` and `kafka_consumer_transactions_v1`. Real `kafka_seek` supports `earliest`, `latest`, and exact nonnegative integer offsets (use strings for offsets above JSON's exact integer range). It requires the configured consumer group, a closed transaction, and every currently owned partition to have been polled first. Offset lookup preserves `read_committed` isolation, fails on partial broker responses or changed ownership, and clears queued delivery/commit tracking after repositioning. It does not reposition unowned partitions or support a per-action partition selection.

Set `config.kafka.consumer_transactions: true` with a consumer group, transactional ID, and `isolation_level: read_committed` in a real build. Begin before consuming, transform/produce, then commit the transaction. One input topic is supported per VUser. Ordinary `kafka_commit` and seek are unavailable in this mode. franz-go's group transaction session commits the produced records and consumed offsets together; ownership loss aborts the transaction and reconciles offsets. Confirmed ownership aborts are recoverable on the next transaction; ambiguous broker outcomes require recreating the VUser. Construction and capability discovery remain traffic-free. Mock producer transaction behavior is unchanged and does not claim consumer-offset coordination.

The deterministic adapter and recovery tests are verified locally. Broker-backed seek, transactional visibility, committed offsets, restart and rebalance acceptance require an approved disposable Kafka environment and remain pending; these are not published production acceptance results. The implementation uses the pinned [franz-go transaction session](https://github.com/twmb/franz-go/blob/v1.21.5/docs/transactions.md).

## Client-call rates and codec work

Live and archived gRPC diagnostics report sent/received message and protobuf-byte rates over the sum of retained client-call durations, plus mean protobuf bytes per observed message. These are per-client-call service-time rates, not aggregate elapsed-run throughput or HTTP/2 wire sizes. A zero duration or incomplete call cohort leaves the rate unavailable; a measured zero message count remains zero. Message averages require a positive message denominator.

Codec elapsed work divided by call wall time uses summed encode/decode measurements only when both optional measurement cohorts cover every retained call. The calculation is weighted by call durations; it never averages per-call percentages. Streaming encode/decode can overlap, so the ratio may exceed 100% and is not an additive breakdown of server processing. Existing archive evidence supplies the same analysis in the live/archive UI and HTML/PDF/DOCX/CSV reports without inventing unavailable transport phases.

Saved baseline comparisons now retain whole-run telemetry health for each run alongside selected load-window analysis. The health table distinguishes retained, classified and metric-bearing intervals, shows classification coverage and breached classified-interval percentages, and links to each run's evidence. Missing classifications and sample-age measurements remain unavailable. Drop counters are cumulative observations: repeated publications retain the peak instead of summing the same counter repeatedly; decreases are qualified and exact loss totals are not inferred. Different sampling cadence and health policies prevent causal or acceptance claims; telemetry comparison never changes regression verdicts. HTML, CSV, DOCX and PDF exports retain the same evidence and qualifications.

Browser archive diagnostics expose all of the bounded top-50 retained resources with action and failure filtering and per-resource page, capture-action timestamp, method and device context. Network failure classification survives into these rows. Resource rows are diagnostic samples; their capture-action timestamp does not supply an individual request start time or measured transport phases.
