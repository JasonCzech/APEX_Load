# Observability Integrations

Configuration and operating guidance for Prometheus, OpenTelemetry, Dynatrace, AppDynamics, Power BI, Elastic, and Confluence integrations.

## AppDynamics test monitoring

Classic AppDynamics SaaS and on-premises Controllers can be configured as project connections and attached to tests through revisioned monitor sources. A source selects direct controller collection or an assigned collector, application/entity scope, metric paths, events, health-rule violations, bounded snapshot summaries, and optional licensed Analytics queries. Credentials use project-secret references; values and reference names are excluded from public connection and job projections.

Configure **Settings → Integrations → AppDynamics test capture**, test the saved connection, discover applications and metric paths, then add an AppDynamics source in **Monitoring → Telemetry → Run monitor sources**. Save reusable scenario bindings or override them for one run in Run Setup. Collection continues through pauses and drains delayed source data after load ends. Terminal performance criteria require settled, compatible measurements with sufficient coverage.

See [AppDynamics setup and operation](APPDYNAMICS.md) and [implementation specification](APPDYNAMICS_SPEC.md) for transport policy, authentication, selection, evidence semantics, rollout, and acceptance criteria. The generated [OpenAPI specification](swagger.yaml) is the authority for route contracts.


## Prometheus target-system test monitoring

Project administrators configure **Settings → Integrations → Prometheus test monitoring**, verify the saved revision at its selected direct or collector location, and create `prometheus-query` sources in **Monitoring → Telemetry**. Typed PromQL queries retain approved labels, units, semantics, query identity, optional freshness companions and scoped target/alert summaries. Tests and scenarios select sources explicitly; preparation freezes revisions and acceptance rules without SUT traffic.

Live timelines, five-minute maximum delayed-data reconciliation, canonical archives, analysis and terminal criteria use the common durable provider pipeline. Missing required evidence remains Inconclusive and cannot pass CI. Existing APEX platform graphs, `/metrics`, imported dashboard behavior and the legacy collector `prometheus` pack remain separate. Explicit dashboard adoption resolves a reviewed disabled source draft without copying credentials or thresholds.

See [setup and operation](PROMETHEUS_MONITORING.md) and [specification/qualification matrix](PROMETHEUS_MONITORING_SPEC.md). Real backend qualification follows fixture verification; no server version or Azure identity mode is certified solely by fixtures.

## Governed provider registry

AppDynamics, Dynatrace and Prometheus query sources share the durable provider pipeline. Each one's contract is a descriptor in `pkg/monitorprovider/registry.go`, and the controller adds its ingestion, direct-collection, grant and submission hooks in `pkg/api/provider_monitoring.go`. Code dispatches through the registry; a test fails if any other Go source compares a provider name.

| Provider | Source pack | Scheduling | Observation | Default / minimum interval | Collector key |
| --- | --- | --- | --- | --- | --- |
| AppDynamics | `appdynamics` | Windowed: closed windows behind the clock | Interval buckets | 60 s / 60 s | Required |
| Dynatrace | `dynatrace` | Cursor: resumable queries under scan budgets | Interval buckets | 60 s / 60 s | Required |
| Prometheus query | `prometheus-query` | Polled at a fixed interval | Snapshot evaluations | 30 s / 15 s | Not used |
| Elasticsearch query | `elastic-query` | Windowed, re-reading a five-minute overlap | Interval buckets | 60 s / 30 s | Not used (controller only) |

Intervals may go up to 3,600 seconds. Dynatrace defaults to direct execution. The registry marks Dynatrace and Elasticsearch query as trace-lookup providers: Dynatrace sources look up exemplar trace IDs, while Elasticsearch lookups run in the legacy ELK capture. An `elastic-query` search reserves room for a trace lookup but does not yet send exemplar trace IDs. Collectors must advertise the provider's capability (`appdynamics.v1`, `dynatrace.v1` or `prometheus-query.v1`); a job whose collector lacks it records that provider's `collector_*_contract_unavailable` coverage reason.

### Provider qualification

Each governed provider has a record in `docs/qa/qualification/<provider>.json`
(`apexload.provider_qualification.v1`): its required permissions, any real
environments it was checked against (product and API versions, non-production
tenant type, permissions, observed limits and date) and an outcome per
capability with its evidence. **Qualified** means checked against such an
environment; **fixture-tested** means exercised only against fixtures that
model the provider's documented contract, which does not establish
compatibility with a licensed tenant. A `docs/qa` test requires a record for
every provider in the registry, covering the capabilities the registry
declares, with evidence that names existing test functions, and keeps this
table in step with the records. Gate C needs a record for every provider in
the RCA evaluation corpus; when that corpus's manifest lands (WP3.9), the test
must also read its providers. Update a record when a
qualification succeeds, then regenerate the table with
`APEXLOAD_UPDATE_QUALIFICATION_TABLE=1 go test ./docs/qa -run TestQualificationTableMatchesRecords`.

<!-- provider-qualification:begin -->
| Provider | Capability | Status | Qualified on |
| --- | --- | --- | --- |
| AppDynamics | metrics | fixture-tested | — |
| AppDynamics | diagnostics | fixture-tested | — |
| AppDynamics | run header | fixture-tested | — |
| AppDynamics | analytics | fixture-tested | — |
| AppDynamics | private collector | fixture-tested | — |
| Dynatrace | metrics | fixture-tested | — |
| Dynatrace | diagnostics | fixture-tested | — |
| Dynatrace | grail queries | fixture-tested | — |
| Dynatrace | trace lookup | fixture-tested | — |
| Dynatrace | run header | fixture-tested | — |
| Dynatrace | deep links | fixture-tested | — |
| Dynatrace | load phase annotations | fixture-tested | — |
| Elasticsearch query | metrics | fixture-tested | — |
| Elasticsearch query | trace lookup | fixture-tested | — |
| Elasticsearch query | opensearch profile | fixture-tested | — |
| Elasticsearch query | deep links | fixture-tested | — |
| Elasticsearch query | legacy capture | fixture-tested | — |
| Prometheus | metrics | qualified | Prometheus 2.55.1 (2026-10-04); Prometheus 3.13.0 (2026-10-04) |
| Prometheus | diagnostics | qualified | Prometheus 2.55.1 (2026-10-04); Prometheus 3.13.0 (2026-10-04) |
| Prometheus | discovery | qualified | Prometheus 2.55.1 (2026-10-04); Prometheus 3.13.0 (2026-10-04) |
| Prometheus | private collector | fixture-tested | — |
| Prometheus | azure managed prometheus | fixture-tested | — |
| Prometheus | kubernetes presets | fixture-tested | — |
<!-- provider-qualification:end -->

### Bucketed Elasticsearch capture

An `elastic-query` monitor source reads an existing ELK connection (its
endpoint, index pattern, timestamp field, filters and API-key or basic
credentials from project secrets, through the same outbound host allowlist)
and collects interval buckets instead of the legacy capture's run totals:

```json
{"pack": "elastic-query", "executor_mode": "direct", "connection_id": "<elk connection>", "interval_seconds": 60,
 "elastic_query": {"project_id": "<project>", "bucket_seconds": 60, "terms_fields": ["http.method"], "terms_size": 10}}
```

- **Buckets:** each poll runs a `date_histogram` with a fixed interval of
  30–300 seconds (default 60) aligned to UTC. Each bucket yields `logs.count`,
  `logs.errors` (from the connection's error filter) and
  `logs.terms.<field>.<value>` for the top `terms_size` values of each field.
- **Truncation:** at most 16 terms fields are queried, each at most 128
  bytes. Extra fields, values beyond the top ones, and values too long to name
  a series within the 256-byte metric name are flagged as coverage warnings.
  Values are named as the server displays them (`key_as_string`, or a
  number's exact literal).
- **Late data:** each poll re-reads from five minutes (`overlap_seconds`)
  before the last watermark and upserts buckets by start, so a late document
  updates its bucket. A bucket is marked settled once it falls behind the
  overlap; ingestion rejects earlier settled claims.
- **Execution:** sources run in the controller only (`executor_mode: direct`).
  One poll reads at most 120 buckets, fewer when terms are heavy, so a search
  stays under 10,000 aggregation buckets (`search.max_buckets` before
  Elasticsearch 7.9). A selection whose overlap re-read plus one new bucket
  does not fit one search is rejected, so every poll can advance.
- **OpenSearch:** set `"profile": "opensearch"` in `elastic_query`. The
  search is the same (`date_histogram` with `fixed_interval`, terms and
  filters exist in OpenSearch 1.x and 2.x), and points record
  `backend: opensearch`. OpenSearch has no Elasticsearch API keys, so the
  connection must use basic authentication. Saving an OpenSearch source on an
  API-key connection is refused, and a run whose connection has since switched
  to an API key records `connection_source_mismatch` for that source. AWS
  SigV4 signing is not supported; use fine-grained access control's internal
  users. The ELK connection's default trace link is Kibana's legacy Discover
  URL; newer OpenSearch Dashboards Discover pages use a different URL, so if
  the default does not open the trace, save a trace template for your
  version.

The legacy ELK capture keeps running unchanged. Its status and the archive
manifest label it `observation: cumulative` (`elk_observation`), and its run
totals are never reinterpreted as intervals. Fixture-tested only; not yet
qualified against a live cluster.

## Confluence Cloud result publishing

Project administrators configure **Settings → Connections → Confluence** with an HTTPS Cloud site URL, Atlassian account email, numeric space and parent page IDs, and the name of an API token held in the project's secret provider. The token value is never saved in the connection or returned by the API. Save the connection, then use **Test saved connection** to check that the account can read the parent page in the selected space. The account also needs permission to create and update pages there. Confluence Cloud's REST API supports page creation beneath a parent using `spaceId` and `parentId` ([Atlassian API reference](https://developer.atlassian.com/cloud/confluence/rest/v2/api-group-page/)); API-token authentication uses the account email and token ([Atlassian authentication guide](https://developer.atlassian.com/cloud/confluence/basic-auth-for-rest-apis/)).

Editors can open an archived run's report, expand **Publish to Confluence**, choose an enabled connection, and publish. Each run has one page per connection. The page contains the execution outcome, verdict, overview metrics, up to 100 named transaction rows, and a link to the full APEX Load report. Publishing again updates the same page. If a page creation response is uncertain, APEX Load looks for the run's stable title beneath the configured parent before attempting another create.

Automatic publishing is off by default. A project administrator can enable it on one connection per project after setting `APEXLOAD_PUBLIC_URL` on the controller. Every finalized archived run in that project, including failed and aborted runs with an archive, is queued durably. Delivery is retried up to three times; Confluence failure does not change the test outcome. The run report displays delivery status and the Confluence page link. Deleting a connection leaves already published Confluence pages and recorded page links in place.


## Prometheus scrape endpoint

APEX Load exposes controller metrics at `GET /metrics`. When `APEXLOAD_METRICS_ADDR` is set, `/metrics` is served only on that dedicated listener. When `APEXLOAD_METRICS_TOKEN` is set, scrapes must include `Authorization: Bearer <token>`.

The workstation collector agent has separate source-specific packs. `linux-hosts` reads Linux `/proc` host metrics, `go-runtime` reads the agent process, and `prometheus` scrapes an explicitly configured `-prometheus-url` (or `APEXLOAD_COLLECTOR_PROMETHEUS_URL`). The collector must be registered with each selected pack in its allow-list. The initial Prometheus pack accepts text exposition 0.0.4 with up to 512 finite, unlabeled numeric samples from a 1 MiB response. It preserves declared metric kinds and explicit millisecond timestamps; undeclared kinds remain unknown. Labeled and non-finite samples are counted as skipped and degrade the heartbeat. Duplicate samples and unsupported formats fail the scrape. Scrapes have a 10-second deadline and do not follow redirects; the URL cannot contain credentials, query parameters, or a fragment. An unavailable source degrades the heartbeat and is not reported under another pack name. The collector metrics API also returns a bounded `metric_points` projection with collector identity, source timestamps, and current freshness. `metric_points_coverage` counts returned, omitted, invalid, and source-skipped samples. Collector heartbeat snapshots are separate from test evidence. When a legacy scrape source is explicitly selected through run-monitor bindings, accepted typed point batches can be retained in the run archive; use `prometheus-query` for the project-governed labelled PromQL workflow.

The controller is scrape-only. Remote write is configured on the external Prometheus server; see `deployments/k8s/monitoring/prometheus-remote-write-example.yaml`.

### Controller

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `apexload_up` | gauge | none | Controller scrape health. |
| `apexload_websocket_clients` | gauge | none | Connected WebSocket clients. |
| `apexload_test_count` | gauge | none | Tests tracked in controller memory. |
| `apexload_tests_by_status` | gauge | `status` | Tests by lifecycle status. |
| `apexload_max_concurrent_tests` | gauge | none | Configured concurrent-test limit. |
| `apexload_metrics_interval_seconds` | gauge | none | Configured live metrics publish interval. |
| `apexload_notification_delivery_failures_total` | counter | none | Webhook deliveries that failed after retry. |

### Scaling And Capacity

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `apexload_scaling_vusers` | gauge | `state` | Queued and active VUsers. |
| `apexload_scaling_tests` | gauge | `state` | Queued and active tests. |
| `apexload_scaling_protocol_vusers` | gauge | `state`, `protocol` | Queued and active VUsers by protocol. |
| `apexload_capacity_available_generators` | gauge | `scope` | Configured/effective/reserved/shared/live generator capacity. |
| `apexload_generator_region_count` | gauge | `region` | Configured generator inventory entries by region. |
| `apexload_generator_region_available_capacity` | gauge | `region` | Available VUser capacity by region. |
| `apexload_generator_region_active_vusers` | gauge | `region` | Active VUsers by region. |
| `apexload_generator_capacity_state_count` | gauge | `region`, `state` | Generator capacity states by region. |
| `apexload_generator_drain_state_count` | gauge | `region`, `state` | Generator drain states by region. |
| `apexload_generator_compatibility_state_count` | gauge | `region`, `state` | Generator compatibility states by region. |

### Telemetry Pipeline

These series are emitted only for non-terminal tests with live telemetry.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `apexload_telemetry_dropped_results_total` | counter | `test_id` | Collector samples dropped by the engine. |
| `apexload_telemetry_shedded_arrivals_total` | counter | `test_id` | Open-workload arrivals shed by the engine. |
| `apexload_telemetry_dropped_subscriber_batches_total` | counter | `test_id` | Generator metrics batches dropped because stream subscribers were full. |
| `apexload_websocket_dropped_messages_total` | counter | `test_id` | WebSocket client messages dropped by the controller. |
| `apexload_websocket_dropped_events_total` | counter | `test_id` | WebSocket broadcast events dropped by the controller. |
| `apexload_telemetry_sample_age_ms` | gauge | `test_id` | Age of the latest telemetry sample. |
| `apexload_telemetry_generator_processing_lag_ms` | gauge | `test_id` | Generator-side collection-to-publish lag. |
| `apexload_telemetry_transport_lag_ms` | gauge | `test_id` | Generator-to-controller transport lag. |
| `apexload_telemetry_controller_fanout_lag_ms` | gauge | `test_id` | Controller-to-dashboard fan-out lag. |

### Messaging

Messaging metrics are emitted only for non-terminal tests whose dependency telemetry includes JMS, Kafka, or Event Hubs targets.

| Metric | Type | Labels | Meaning |
| --- | --- | --- | --- |
| `apexload_messaging_consumer_lag_ms` | gauge | `test_id`, `protocol`, `target` | Average consumer lag. |
| `apexload_messaging_consumer_offset_lag` | gauge | `test_id`, `protocol`, `target` | Average offset or sequence backlog. |
| `apexload_messaging_e2e_latency_ms` | gauge | `test_id`, `protocol`, `target` | Average producer-to-observed-consume latency. |
| `apexload_messaging_partition_skew_pct` | gauge | `test_id`, `protocol`, `target` | Produce skew across partitions. |
| `apexload_messaging_throttled_total` | gauge | `test_id`, `protocol`, `target` | Throttle, server-busy, and quota responses. |
| `apexload_messaging_dead_lettered_total` | gauge | `test_id`, `protocol`, `target` | Dead-lettered message count. |
| `apexload_messaging_messages_sent_per_sec` | gauge | `test_id`, `protocol`, `target` | Average send rate. |
| `apexload_messaging_messages_received_per_sec` | gauge | `test_id`, `protocol`, `target` | Average receive rate. |

### Troubleshooting

- If `apexload_up` is absent, verify the ServiceMonitor points at the `metrics` service port and that NetworkPolicy allows Prometheus to reach it.
- If telemetry lag or drops alert, compare `apexload_telemetry_*` series with dashboard telemetry-health banners and controller logs.
- `APEXLOAD_PPROF_ADDR` can expose unauthenticated Go pprof for CPU/memory investigation when enabled by the operator. Production startup rejects non-loopback pprof binds; use an explicit loopback address plus port forwarding.
- `APEXLOAD_PROMETHEUS_URL` configures the controller's Prometheus query client for dashboard graph overlays; it is separate from the `/metrics` scrape endpoint.


## OpenTelemetry tracing

Status: Current

APEX Load can export vendor-neutral OTLP traces from the standalone controller
and generator binaries. Export is strictly opt-in: the default process installs
no SDK provider, creates no exporter, and opens no collector connection. The
desktop application remains correlation-only so local operator activity is not
exported implicitly.

### Enable tracing

Set the following on both controller and generator processes:

```bash
APEXLOAD_OTEL_ENABLED=true
OTEL_EXPORTER_OTLP_ENDPOINT=https://otel-collector.example:4318
OTEL_EXPORTER_OTLP_PROTOCOL=http/protobuf
OTEL_SERVICE_NAME=apexload-controller # use apexload-generator on generators
OTEL_TRACES_SAMPLER=parentbased_traceidratio
OTEL_TRACES_SAMPLER_ARG=0.10
APEXLOAD_OTEL_ACTION_SAMPLE_RATIO=0.01
```

`OTEL_EXPORTER_OTLP_TRACES_ENDPOINT` and
`OTEL_EXPORTER_OTLP_TRACES_PROTOCOL` take precedence over their common OTLP
counterparts. `http/protobuf` and `grpc` are supported. The official exporters
also honor the standard trace-specific/common headers, TLS certificate, client
certificate/key, timeout, compression, `OTEL_RESOURCE_ATTRIBUTES`, and
`OTEL_SERVICE_NAME` variables. Put authentication in
`OTEL_EXPORTER_OTLP_HEADERS`; endpoint URLs containing credentials or query
strings are rejected.

For Helm installs, set `tracing.enabled`, `tracing.endpoint`, and the sampling
values in the release configuration. If the collector requires headers, put
the complete `OTEL_EXPORTER_OTLP_HEADERS` value in an existing Kubernetes
Secret and set `tracing.headersSecretName` plus, when needed,
`tracing.headersSecretKey`. The chart injects the Secret key into both
controller and generator processes without copying the credential into the
release values or ConfigMap.

An enabled process fails startup when its endpoint, protocol, sampler, or ratio
is invalid. Export uses the SDK batch processor and shutdown is given a bounded
flush window. Integration-profile status is `configured` only after the SDK and
exporter initialize; this is startup readiness, not a live collector health
probe.

### Span coverage

- inbound controller HTTP requests, named by method and matched route pattern;
- test-start orchestration and complete local test execution;
- distributed test launch orchestration through generator commit (the durable
  terminal state remains the source of truth for distributed completion);
- sampled protocol action execution, including retry count and safe outcome;
- controller/client and generator/server unary and streaming gRPC boundaries;
- generator engine work linked to the detached `CommitWork` server context; and
- W3C context injection into HTTP protocol requests when script traceparent
  propagation is enabled.

Action span names are static (`apexload.action`). Attributes contain durable
test/project/scenario identifiers, controlled action-type/protocol values,
counts, and outcomes. Free-form test, action, and group names plus user-supplied
script IDs are excluded. Identifier length limits are defense in depth, not
redaction: operators should still treat exported identifiers as telemetry.
Targets, URLs, query strings, request/response bodies, headers, raw error
messages, and arbitrary baggage are not exported. Explicit script `traceparent`
headers remain authoritative; setting `traceparent: false` continues to
suppress automatic context injection.

The test-run sampler follows standard `OTEL_TRACES_SAMPLER` semantics. Its
default is `parentbased_traceidratio` at `0.10`. High-volume action spans have a
second deterministic sampler controlled by
`APEXLOAD_OTEL_ACTION_SAMPLE_RATIO` (default `0.01`); skipped actions retain the
active parent context, so HTTP propagation remains continuous without exporting
every action.

### Run-level trace context

A run can opt into correlation headers for operator-approved origins with the
`trace_context` run setting (Run Setup → Trace context, or `trace_context` on
`POST /api/v1/tests`): `{"enabled": true, "transaction_marker": true,
"origins": ["https://shop.example.com"]}`. Origins are `scheme://host[:port]`
only, at most 32, shared with any AppDynamics run-header origins bound to the
run.

- `enabled` sends generated `traceparent` headers to the approved origins,
  even when an imported script persisted `traceparent: false`, and strips
  generated context on every other hop, including redirects. Each top-level
  source transaction instance gets its own trace (actions outside a
  transaction get one each; nested transactions, `http_sequence` children and
  concurrent groups share the enclosing trace), and every request gets its own
  span ID. This works with OTel export off. With export on, a transaction root
  is exported only when `APEXLOAD_OTEL_ACTION_SAMPLE_RATIO` admits it, linked
  to the run span rather than parented by it.
- `transaction_marker` adds `X-Dynatrace-Test: VU=…;SI=APEXLoad;TSN=…;LSN=…;LTN=…`
  with the VUser number and bounded transaction, script and test names. It
  never carries parameter values.
- Script-authored `traceparent` and `X-Dynatrace-Test` headers are never
  changed or stripped.

Distributed runs that use either option require generators advertising
`correlation_policy_v2`; older generators fail before any traffic starts. Runs
without the setting behave as before.

Archive rows record the context each request actually sent: `trace_sent`,
`trace_source` (`authored`, `propagated` or `generated`), `sent_trace_id` and
`sent_span_id`. `trace_id` and `span_id` come from that context, so a gateway's
`X-Request-Id` no longer replaces them. `trace_sent` means a send was
attempted: a fresh HTTP cache hit, a request whose connection failed before
any headers were written, a message rejected before reaching its client and a
Kafka record still buffered in an open transaction all archive
`trace_sent: false` with no `trace_id`. Rows written before this change, or by
protocols that do not record it, omit `trace_sent`.

These behaviours are covered by unit and engine tests against local HTTP
servers; they have not yet been qualified against a live APM tenant.

### Trace exemplars and provider lookups

Only requests whose trace context was actually sent (`trace_sent: true` with a
valid trace ID) become exemplars.

- **Live:** each transaction keeps, per metrics window, its slowest success,
  its first error and one uniformly sampled request, carried as `exemplars`
  (trace ID, span ID, duration, success, status, reason) on live transaction
  metrics. Generators advertise `exemplars_v1`; the controller falls back to
  correlation IDs for generators without it.
- **Provider lookups:** ELK, Dynatrace log capture and native Dynatrace trace
  attribution no longer stop at the first 64 trace IDs of the run. Each poll
  takes the best 64 exemplars since its previous poll: errors first, then slow
  requests, then representative samples. ELK and Dynatrace log capture only
  filter by trace ID when no test ID field is configured, so only those polls
  record lookups. Native Dynatrace still proves at most 64 trace IDs per job;
  they are now the run's errors and slowest requests rather than its first.
- **Lookup outcomes:** each looked-up trace ID is archived in
  `provider_lookups.jsonl` (`apexload.provider_lookup.v1`) as `found`,
  `not_found` or `error`. `result_limited` marks a miss when the provider
  returned its row limit (20 for ELK, 100 for Dynatrace logs), so the miss is
  not conclusive. `not_sampled`, `not_retained` and `expired` are reserved for
  when providers report sampling and retention. At most 20,000 outcomes are
  kept (`provider_lookups_truncated`). Distributed runs keep them with the
  controller's capture state, like the capture evidence.
- **Post-run exemplars:** after finalization, `exemplars.jsonl`
  (`apexload.exemplar.v1`) holds, per transaction (population, group and
  name) and interval, up to two slowest successes, two errors of distinct
  error classes and two requests nearest the interval median. The median
  comes from `interval_distributions.jsonl`. Caps are 2,000 per transaction
  and 20,000 per run, taken round-robin across intervals and then across
  transactions so a cap thins the whole run instead of dropping its end.
  Ties break by duration, then timestamp, then trace ID. Selection is a pure
  function of the archive, so it is byte-identical when repeated.
  `exemplars_truncated` marks any cap.

### Timing provenance and clock observations

Metric points may carry an optional `timing` block (additive within
`apexload.metric_point.v1`): `clock_source` (`generator`, `collector`,
`provider` or `controller`), `offset_status` (`measured` or `unknown`),
`clock_offset_ms` (the source's clock minus the controller's) and
`clock_uncertainty_ms`, plus `resolution_seconds`, `freshness_seconds` and
`received_at` where they apply. Offsets are evidence for ordering, never a
silent correction: a point more than 30 s ahead of its receipt keeps its
`clock_skewed` quality.

- **Collectors** time each heartbeat. The controller stamps its replies with
  `X-Apex-Clock-Received` and `X-Apex-Clock-Sent` (Unix nanoseconds), and the
  agent derives its offset, round trip and uncertainty (half the round trip)
  from the four timestamps. Points it timed itself for a run carry that
  measurement, stamped once so a retried batch keeps its identity; without a
  measurement in the last five minutes they say `unknown`. Adapter points
  (CloudWatch, Azure Monitor, OTLP) and samples that bring their own
  timestamp were timed by another clock and carry no collector timing. Older controllers
  send no stamps and older agents send no timing; both keep working.
- **Generators** are re-measured once a minute while a distributed run is
  live, by the same heartbeat exchange the start barrier uses, including after
  HA adoption.
- **Providers** (AppDynamics, Dynatrace, Prometheus queries, Elasticsearch
  query buckets) never exchange clocks with the controller, so their points say
  `unknown` with an uncertainty of half the bucket or evaluation step;
  Prometheus points add `freshness_seconds` when the sample time is known. The
  timing depends only on the point, so re-reading a bucket persists identical
  content.

Measured offsets are archived after finalization in `clock_observations.jsonl`
(`apexload.clock_observation.v1`): source, clock source, observed time, offset,
round trip and uncertainty, at most one per source per minute and 10,000 per
run (`clock_observations_truncated`). The file is written only when the run
measured a clock. Observations live with the controller holding the run, so a
collector batch accepted by another replica, or a probe lost to a controller
restart, is not archived. With HA, offsets are against the replica that
measured them: generator observations name the execution owner in
`controller`, but a collector's offset is against whichever replica answered
its heartbeat. The uncertainty does not include skew between replicas, so keep
controller replicas NTP-synchronized.

### Deep links

Archived exemplars carry links that open their trace in the run's providers:
`GET /api/v1/tests/{id}/archive/exemplars` (viewer; `limit` up to 1000,
`offset`, and optional `group`, transaction `name`, `population` and an
inclusive RFC 3339 `from`/`to` window) returns each exemplar with
`links`, one per trace-capable connection the run used: its legacy ELK and
Dynatrace captures and its trace-lookup monitor sources (Dynatrace and
Elasticsearch query). `total` counts the matching exemplars across pages;
`capture_truncated` means the archive kept fewer exemplars than the run
selected. When link settings cannot be read, exemplars are still returned,
without links and with `links_unavailable`.

- **Templates.** Each Dynatrace or ELK connection can hold a trace link
  template, saved with `PUT /api/v1/projects/{id}/monitoring/connection-links/{kind}/{connection_id}`
  (`kind` is `dynatrace` or `elk`; project admins, with `GET` for editors) as a
  revisioned definition. Templates may use only `{trace_id}`, `{entity_id}`,
  `{from_utc}` and `{to_utc}` (RFC 3339 UTC seconds). Validation requires
  HTTPS and one of the connection's own hosts, and an ELK connection may add
  a Kibana `ui_url` host because Kibana is not the search endpoint. Scheme and
  host must be literal. Trace and entity IDs must be plain identifiers (RFC
  3986 unreserved characters, not only dots), and every value is
  percent-encoded, so a value can never add a path, parameter, fragment or
  host, or change Kibana's state after it decodes the link. Save validates
  the template in effect, including a default built from `ui_url`.
- **Defaults.** Dynatrace links open the distributed-tracing app on the
  platform (or environment) URL with `traceId`; they carry no timeframe, so
  for traces older than the app's default window, save a template with your
  tenant's timeframe parameter. ELK links open Kibana Discover around the
  exemplar's time once `ui_url` names Kibana, querying the connection's trace
  ID field when it has a plain name (free text otherwise); Discover opens on
  Kibana's default data view, so save a template naming the data view when
  that is not the logs index. With no Kibana URL there is no ELK link.
  AppDynamics snapshot and Dynatrace problem links remain on
  `ProviderDiagnostic.DeepLink`, validated against their connection's origin;
  Dynatrace and AppDynamics ingestion share the template target check.
- **Budget.** Link settings are one definition per connection and do not count
  toward the project's 1000 mappings and comparisons. They outlive a deleted
  connection unused, since connection IDs are never reused.
- **Not yet.** Grafana Explore links need a Grafana or Tempo connection, which
  APEX Load does not have; Grafana is currently an annotation sink only.

### Provider breadth

- **OpenSearch** collects through the `elastic-query` source's `opensearch`
  profile (see Bucketed Elasticsearch capture).
- **Kubernetes restarts, OOM kills and scaling** come from Prometheus query
  presets: pod restarts and Deployment and HorizontalPodAutoscaler replicas
  from kube-state-metrics, and OOM kills from the kubelet's cAdvisor
  `container_oom_events_total` (see [Prometheus monitoring](PROMETHEUS_MONITORING.md)).
- **Not yet built**, each needing its own short spec first:
  - a read-only Kubernetes namespace events watch with a namespace allow-list;
  - bounded Azure Log Analytics queries;
  - an IBM MQ PCF queue-depth reader on the messaging management pipeline.

### Messaging trace carriers

Messaging scripts opt in per script with `config.observability.trace_carrier:
"w3c"` (default `none`). Kafka, JMS on Solace, IBM MQ and Service Bus, and
Event Hubs then write the carrier their consumers' instrumentation reads.

| Protocol | Carrier |
| --- | --- |
| Kafka | `traceparent` and `tracestate` record headers |
| JMS (Solace, IBM MQ) | `traceparent` and `tracestate` message properties |
| Service Bus | Those properties plus `Diagnostic-Id` for Azure SDK consumers |
| Event Hubs | `traceparent`, `tracestate` and `Diagnostic-Id` event properties |

- **Producers:** each message carries a child of the transaction root (or of
  the active span) with its own span ID and the parent's flags and state.
  Without a root it starts a new unsampled trace. A `traceparent` or
  `Diagnostic-Id` the script sets wins and is recorded as authored. The
  producer row archives what was sent like an HTTP request (`trace_sent`,
  `sent_trace_id`), so producers can be exemplars.
- **Batches:** each Event Hubs batch member gets its own span, and the
  producer row links every member (`batch_member`).
- **Consumers:** a consumed message is not a child of the consumer's trace.
  The consumer row archives `trace_links` naming the producer's context,
  `consumed` or, for a redelivery, `redelivery` with the broker's delivery
  count when reported (JMS rollbacks and delivery counts, Kafka and Event
  Hubs duplicate offsets or sequence numbers). Earlier deliveries have no
  span of their own to link, so a redelivery links the producer context.
  Rows keep at most eight links (`trace_links_truncated`).

Distributed runs whose scripts opt in require generators advertising
`messaging_trace_carrier_v1`; older generators fail before any traffic starts.
These behaviours are covered by the protocols' in-memory brokers; they have
not yet been qualified against live brokers or APM tenants.

### Signal boundary

This implementation exports traces only. Prometheus remains the metrics
surface, structured process logs remain the logging surface, archived result
telemetry remains in APEX Load storage, and Dynatrace lifecycle notifications
remain event webhooks. Native OpenTelemetry metrics/log export is not currently
implemented.


## Power BI publishing workflow

APEX Load can publish a completed run's results to a Microsoft Power BI **push dataset** so release managers and executives can track performance trends in the BI platform they already use. Publishing is **manual and per-run**: an editor opens a completed run's report page and clicks **Publish to Power BI**.

What gets published:

- one **run summary row** per run (verdict, SLA rollup, throughput, percentiles), and
- one **transaction row** per action and per group (stats + per-transaction SLA outcome).

Full time-series data is *not* pushed; the run row carries a deep link (`apexload_url`) back to the APEX Load report for engineers who need per-interval detail.

### Prerequisites (platform team)

1. **Azure AD app registration** (service principal) with a client secret.
2. Power BI tenant setting **“Allow service principals to use Power BI APIs”** enabled (Admin portal → Tenant settings → Developer settings), scoped to a security group containing the principal.
3. The service principal added as **Member** (or Admin) of the target Power BI **workspace**.
4. No API permissions/consent are required beyond workspace membership for push-dataset calls; the token is requested with the `https://analysis.windows.net/powerbi/api/.default` scope.

### APEX Load configuration (system admin)

Settings → Integrations → **Power BI**:

| Field | Notes |
|---|---|
| Enabled | Gates the publish action on run reports. |
| Azure AD tenant ID | Directory (tenant) GUID. |
| Client ID | Application (client) ID of the service principal. |
| Client secret environment variable | Name of a controller env var (e.g. `APEXLOAD_POWERBI_CLIENT_SECRET`) holding the secret. The secret value is never stored or returned by the API. |
| Workspace ID | GUID of the workspace (from its URL). |
| Dataset name | Default `APEX Load Performance Runs`. Created automatically on first publish. |
| Authority URL / API base URL | Defaults target the public cloud (`https://login.microsoftonline.com`, `https://api.powerbi.com/v1.0/myorg`). Point these at sovereign or internal endpoints when applicable — both hosts are added to the outbound allow-list and may resolve to private address space; all other egress remains SSRF-guarded. HTTPS is required in production (`APEXLOAD_POWERBI_ALLOW_INSECURE_HTTP=true` is the explicit lab-only escape hatch). |
| Dashboard base URL (optional) | Absolute base for the `apexload_url` deep links pushed into the dataset (e.g. `https://apexload.example.com`). Recommended: when unset, links fall back to the publish request's own host (strictly validated), which may be wrong behind path-rewriting proxies. |

Use **Test connection** after saving to verify token acquisition and workspace access.

### Dataset schema (v1)

Two tables, related on `run_id` (relationship is created with the dataset, cross-filtering both directions).

#### `runs` — one row per published run

| Column | Type | Source |
|---|---|---|
| `run_id` | String | Test ID |
| `publish_id` | String | Unique per publish; use for dedupe |
| `test_name` | String | |
| `project_id`, `release_id`, `application_id` | String | Run provenance |
| `status` | String | Final run status (COMPLETED/FAILED/…) |
| `verdict` | String | Report verdict (Pass / Conditional Pass / Fail) |
| `started_at`, `completed_at` | DateTime | Run wall-clock bounds |
| `duration_seconds` | Double | |
| `avg_tps` | Double | total transactions / duration |
| `total_vusers`, `peak_active_vusers` | Int64 | |
| `total_transactions`, `total_errors` | Int64 | |
| `error_pct` | Double | |
| `avg_ms`, `min_ms`, `max_ms` | Double | Response times |
| `p50_ms`, `p90_ms`, `p95_ms`, `p99_ms` | Double | Percentiles |
| `peak_tps`, `peak_target_tps` | Double | |
| `sla_evaluated`, `sla_passed`, `sla_warned`, `sla_failed` | Int64 | SLA rollup counts |
| `apexload_url` | String | Deep link to the APEX Load run report |
| `published_at` | DateTime | Publish timestamp |

#### `transactions` — one row per action + one per group, per run

| Column | Type | Notes |
|---|---|---|
| `run_id`, `publish_id`, `published_at` | | Join/dedupe keys |
| `kind` | String | `action` or `group` |
| `name` | String | Transaction/group name |
| `count`, `errors` | Int64 | |
| `error_pct` | Double | |
| `avg_ms`, `min_ms`, `max_ms`, `p50_ms`, `p90_ms`, `p95_ms`, `p99_ms` | Double | |
| `peak_tps` | Double | |
| `has_sla` | Bool | Actions only |
| `sla_ms` | Double | Configured p95 SLA |
| `sla_result` | String | `pass` / `warn` / `fail` / `na` |

### Re-publish and dedupe

Power BI push datasets cannot delete individual rows, so re-publishing a run **appends** a second copy of its rows (a new `publish_id`). APEX Load blocks accidental duplicates — a publish is refused with a conflict when a previous attempt already pushed rows for the run into the same dataset (whether it completed or failed partway), unless the user confirms **force**. A `partial` publish leaves transaction rows without a matching runs row; because the table relationship joins on `run_id`, those orphaned rows would double-count in transaction-level visuals after a re-publish — which is why **reports must filter both tables to the latest `publish_id` per run**, not just the `runs` table.

The Power BI push API has no idempotency key, so APEX Load does not retry ambiguous row mutations. A timeout, throttle response, or server error stops the publish as `partial`; only an explicit authentication rejection is retried after refreshing the token. This prevents a client retry from duplicating rows within one `publish_id`. The service can still apply a chunk before returning an ambiguous error, so an operator-forced re-publish may append rows under a new `publish_id`; the latest-publish filter above remains required.

Sample DAX filter measure:

```dax
Is Latest Publish =
VAR latest =
    CALCULATE ( MAX ( runs[published_at] ), ALLEXCEPT ( runs, runs[run_id] ) )
RETURN IF ( SELECTEDVALUE ( runs[published_at] ) = latest, 1, 0 )
```

Apply `Is Latest Publish = 1` as a report/page filter (equivalent measure on `transactions` using `publish_id`).

### Limits and operations

- Push API limits: 10,000 rows per POST (APEX Load chunks automatically), 1M rows/hour, 5M rows per table. Manual per-run publishes stay far below these.
- 429/5xx responses are retried with `Retry-After` honored; a failure mid-publish is recorded as `partial` and surfaced on the run report — recover by force re-publishing.
- The run row is pushed **last**, so a run never appears in trend visuals until all of its transaction rows landed.
- **Schema drift**: if the dataset predates a schema change (or its tables were altered), publishing fails with a drift error. Drift detection is dataset-scoped — it compares against the newest publish into that dataset across all tests. Settings → Power BI → **Recreate dataset** deletes and recreates it with the current schema — this erases all previously pushed rows in Power BI (APEX Load's publish history is retained). The recreated dataset has a new ID, so previously published runs can be re-published into it without `force`.
- The published `verdict` honors an analyst's verdict override saved via report annotations (executive profile wins, then engineer, then audit), matching what the rendered reports display.
- Every publish is recorded in the `powerbi_publishes` table and in the audit log (`powerbi.publish`).

### Recommended report layout (built by the BI team)

**Page 1 — Release Scorecard.** KPI cards (latest verdict, SLA pass %, error rate, p95, peak TPS); a verdict strip of runs over time colored pass/conditional/fail; slicers for project, release, date range.

**Page 2 — Release Trends.** Line charts across runs: p90/p95/p99 response times, peak TPS, error rate, SLA pass % (`sla_passed / sla_evaluated`). Use `release_id` as a categorical axis to compare release-to-release.

**Page 3 — Transaction Drill-down.** Matrix of `transactions[name]` × runs with `p95_ms` conditional formatting (heatmap) to spot which transaction regressed in which release; a top-regressions table ranked by p95 delta vs the previous run; drill-through with `apexload_url` linking back to the full APEX Load report.

**Page 4 (optional) — SLA Compliance.** Matrix of `sla_result` per transaction per run; compliance % trend over time.


## External monitoring integrations

Status: Current

Source note: consolidated 2026-09-24 from the former `DYNATRACE_SAAS_MONITORING.md`, `DYNATRACE_WEBHOOKS.md` and `ELK_MONITORING.md`. Prometheus, OpenTelemetry and Power BI have their own references: [Prometheus metrics](OBSERVABILITY_INTEGRATIONS.md#prometheus-scrape-endpoint), [OpenTelemetry](OBSERVABILITY_INTEGRATIONS.md#opentelemetry-tracing), [Power BI](OBSERVABILITY_INTEGRATIONS.md).

| Integration | Direction | Scope |
| --- | --- | --- |
| Dynatrace SaaS monitoring | APEX Load polls Dynatrace during a test | Project-scoped connection, opt-in per test |
| Dynatrace lifecycle webhooks | APEX Load pushes events to Dynatrace | Notification webhook (Settings) |
| Elastic (ELK) monitoring | APEX Load polls Elasticsearch during a test | Project-scoped connection, opt-in per test |

### Dynatrace SaaS in-flight monitoring

Native Dynatrace monitoring uses revisioned project connections, approved reusable sources and immutable scenario/per-test bindings. Classic Environment metrics, entities, problems and events are supported alongside optional independently authenticated Grail numeric/log/event/span and sampled trace queries and Smartscape discovery. Select direct controller or assigned-collector execution. Verification, discovery and previews are bounded asynchronous operations; disabled drafts remain saveable with a name.

Typed observations preserve source bucket times, dimensions, descriptor units and resolution. Live overlays, canonical archive exports, comparisons and terminal rules use those observations. Delayed collection continues for at most five minutes after load stops. Required incomplete evidence makes performance acceptance Inconclusive and cannot pass CI. Grail scan defaults are 1 GB/query and 10 GB/source-run including a 2 GB final-collection reserve; limits and missing cost metadata are reflected in coverage.

Follow [Dynatrace setup, permissions and troubleshooting](DYNATRACE.md) and [the native monitoring contract](DYNATRACE_SPEC.md). The generated [OpenAPI specification](swagger.yaml) defines connection/operation/source/binding/evidence interfaces. Qualification is fixture-first; real SaaS tenant, licensed Grail and Smartscape compatibility remains unqualified until exact environments are tested.

Existing `dynatrace_capture` selection and archived scalar/evidence readers remain compatibility surfaces. Projects outside the run-monitoring gate keep legacy per-test capture; in gated projects, future legacy selections resolve to reviewed, verified native sources without automatic opt-in. Historical scalar reductions remain historical aggregates rather than fabricated bucket timelines. Outbound lifecycle notifications below remain separate from read-side monitoring.

### Dynatrace lifecycle webhooks

APEX Load sends lifecycle and telemetry-health notifications to Dynatrace through the Dynatrace Events API v2. This is an event webhook integration, not an OTLP exporter.

#### Setup

1. Create a Dynatrace API token with event-ingest permissions.
2. In APEX Load settings, add a notification webhook with format `dynatrace`.
3. Use the Dynatrace Events API endpoint, for example `https://<tenant>/api/v2/events/ingest`.
4. Paste the API token once. The settings API stores it server-side and returns only `has_api_token: true`; the raw token is write-only.
5. Optionally set `entity_selector` and `timeout_minutes`.

#### Delivery Behavior

- APEX Load sends `Authorization: Api-Token <token>`.
- Retryable delivery failures (`408`, `429`, `5xx`, or transport errors) are retried with bounded exponential backoff and `Retry-After` support.
- Failed deliveries after retry increment `apexload_notification_delivery_failures_total`.
- Dynatrace payloads include an `apexload.idempotency_key` property so downstream workflows can dedupe repeated attempts.

#### Events

Dynatrace webhooks can subscribe to the same event names as Slack and Teams, including `test_started`, `test_complete`, `test_failed`, `telemetry_degraded`, `telemetry_breached`, `telemetry_recovered`, `load_phase` and fleet alert events. `load_phase` events are sent as `CUSTOM_ANNOTATION` on the webhook's entity selector.

### Load-phase events and Grafana annotations

Load-level transitions become phase events, so analysis and the monitored
systems can tell a load step from an incident:

- **Kinds:** `ramp_start` and `level_reached` for each ramp stage,
  `level_change` for a hold whose target steps the load, and `pause`, `resume`
  and `stop` from the run's lifecycle. Each level event carries the group
  (`target`) and its target VUsers (`level`).
- **Bounds:** events are deduplicated by kind, group, level and second (one
  `stop` per run) and capped at 500 per run.
- **Delivery:** level events go to notification webhooks as `load_phase`, the
  same way pause, resume and stop already go out as `test_paused`,
  `test_resumed` and `test_stopping`/`test_complete`/`test_failed`. Webhooks
  set to *All events* receive `load_phase` too. Delivery fails open:
  bounded retries, never affecting the run.
- **Grafana:** a webhook with format `grafana` posts annotations to the URL
  given (for example `https://grafana.example.com/api/annotations`) with a
  service-account token sent as `Authorization: Bearer`. Every annotation is
  tagged `apexload`, `run:<test id>` and `event:<type>`, plus `phase:<kind>` for
  load phases, so dashboards can overlay a run by tag. Like Dynatrace, the
  token is write-only.
- **Archive:** after finalization, `phase_events.jsonl`
  (`apexload.phase_event.v1`) holds each phase event with every sink's outcome
  (`pending`, `delivered`, `failed` or `dropped`, named by the webhook's name,
  never its URL). It is kept apart from analyst-authored run annotations. A run
  with no recorded stop gets one at its end time. Phase events live with the
  controller that recorded them, so after an HA adoption the archive holds only
  the transitions the adopting controller saw; delivered notifications are not
  affected.

### Optional Elastic (ELK) in-flight monitoring

APEX Load can poll an Elasticsearch-compatible search endpoint while a test is running. The integration is project-scoped, inactive by default, and runs in the controller rather than in virtual-user goroutines. It does not configure Kibana and does not write to Logstash.

#### Compatibility and privileges

The client uses the Elasticsearch 7/8-compatible JSON REST search API:

- `POST /{index-pattern}/_search`
- `track_total_hits: true`
- a timestamp `range` filter and optional `query_string` filters
- filter aggregations for error counts
- `avg`, `min`, `max`, and `sum` numeric metric aggregations

The Elasticsearch identity needs only read/search access to the configured index pattern and fields. Do not grant index administration, document write, cluster administration, or Kibana privileges.

#### Setup

1. Create the authentication material as project secrets. API-key authentication uses one secret whose value is the Elasticsearch REST API key. Basic authentication uses separate username and password secrets.
2. Open **Settings → Connections → Elastic test capture**, or use **Manage Elastic connections** from Monitoring → Telemetry. Confirm the active project. **Save draft** needs only a name; the checklist identifies details to supply later.
3. Enter an HTTPS Elasticsearch endpoint, index pattern, field mappings, authentication type, and project-secret references. Secret-reference names are accepted as write-only input and are not returned by the API.
4. Use **Test saved connection** while it is disabled, then edit and select **Enable for new tests**. Configuration, enablement, and last-check status are separate. Only enabled, fully configured connections appear in the create-test selector. Blank replacement credential fields preserve existing secrets; saved values and reference names remain hidden.
5. Opt a test into capture with **Elastic log capture**. Existing tests and new tests without a selection make no ELK requests.

No environment-specific endpoint or credential belongs in source control.

#### Field-mapping checklist

- Timestamp field: required; usually `@timestamp`.
- Level field: optional; for example `log.level`.
- Message field: required for a ready connection; usually `message`.
- Test ID field: optional. When configured, APEX Load adds an exact term filter for the current test ID.
- Trace ID field: optional. When no test-ID field is configured, observed run trace IDs are used for correlation as they become available and are retained in evidence samples for cross-tool navigation.
- Base filter: optional fixed Elasticsearch query-string expression applied to every poll.
- Error filter: optional query-string expression used to calculate the cumulative error count.
- Numeric aggregations: up to 16 named field aggregations using `avg`, `min`, `max`, or `sum`.

When no test-ID mapping is supplied, APEX Load uses observed trace IDs if a trace field is mapped. Before any trace is observed—or when neither correlation field is mapped—capture falls back to the test start-to-current time window plus the base filter. This fallback is deliberately broad; use a test identifier or a sufficiently restrictive base filter when multiple workloads share an index.

#### Runtime and privacy behavior

The controller polls at a bounded cadence and queries the cumulative window from test start through the current poll. Failures are fail-open: they update capture health but do not stop, pause, or otherwise affect the load test. Cumulative values are published as external resource metrics under `elk.<connection-id>.<metric>`, including `logs_total`, `errors_total`, and configured aggregation names.

Polling runs in a cancellable background worker; live metric publication reads cached results without waiting for Elasticsearch. Timed-out searches, failed shards, early termination, and inexact hit counts are treated as incomplete capture rather than healthy results. Last-good values remain available with degraded health. Both local and distributed runs contribute observed trace IDs to the bounded correlation set.

Search responses are size-bounded. Only mapped fields are requested. A run retains at most 50 deduplicated samples, and individual messages are capped at 4 KiB. Built-in credential/PII scrubbing runs before samples reach live state and again at the archive boundary. The archive stores only cumulative metrics and bounded scrubbed samples in `elk_evidence.jsonl`; it never stores raw Elasticsearch documents, authentication values, or secret-reference names.

HTTPS is required. Requests are restricted by the outbound network policy to the one saved endpoint host, including for internal Elasticsearch deployments, and redirects cannot escape that exact origin. Credential-bearing, query-bearing, fragment-bearing, cleartext, and otherwise malformed endpoint URLs are rejected.

Connection edits do not mutate an active or archived run: APEX Load snapshots the operational capture definition into durable private test state at creation. Public run provenance and the manifest retain only the immutable connection ID and safe display name.
