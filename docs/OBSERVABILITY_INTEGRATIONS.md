# Help Center

A guided collection of operator articles for the APEX Load dashboard.


## Prometheus scrape endpoint

APEX Load exposes controller metrics at `GET /metrics`. When `APEXLOAD_METRICS_ADDR` is set, `/metrics` is served only on that dedicated listener. When `APEXLOAD_METRICS_TOKEN` is set, scrapes must include `Authorization: Bearer <token>`.

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

APEX Load can optionally poll Dynatrace SaaS while a test runs. Connections are reusable within one project, are inert until enabled and complete, and are never selected automatically. The controller performs all monitoring calls; VUsers and generators do not call Dynatrace.

#### Supported APIs

- Environment API v2 metrics (`/api/v2/metrics/query`) for named numeric selectors.
- Environment API v2 problems (`/api/v2/problems`) for bounded problem summaries.
- Dynatrace Platform Grail query API (`/platform/storage/query/v1/query:execute`) for bounded log samples.

Environment endpoints must be HTTPS URLs beneath `*.live.dynatrace.com`; Platform endpoints must be HTTPS URLs beneath `*.apps.dynatrace.com`. Requests are restricted to the saved host and redirects are rejected. The integration does not configure dashboards, ingest data into Dynatrace, or change monitored entities.

#### Authentication and least privilege

Credential values stay in the existing project-secret system. Connection writes accept logical secret references, but reads return only `has_*` flags and never return credential values or secret-reference names.

Environment API metrics/problems use an API token with only the read scopes required by the selected APIs (normally `metrics.read`, `entities.read`, and `problems.read`). Grail logs can use either a Platform token or an OAuth client. Grant only log/storage query access needed for the configured DQL filter. OAuth clients require client-ID and client-secret project secrets, a resource, and explicit scopes.

#### Setup checklist

1. In **Settings → Connections → Dynatrace test capture**, add a project connection. Monitoring → Telemetry provides a summary and management link. Confirm the project; **Save draft** needs only a name.
2. Enable one or more capabilities: metrics, problems, or Grail logs.
3. Supply the corresponding SaaS endpoint and project-secret references.
4. For metrics, add up to ten stable names and metric selectors, optional entity selectors, and an `avg`, `min`, `max`, or `sum` reduction.
5. For logs, confirm the timestamp, status, message, test-ID, and trace-ID field mappings. Set an administrator-owned base DQL filter that limits capture to the intended service or environment.
6. Use **Test saved connection** before enabling it for new tests. Capability health is reported independently, so a partial result identifies which API needs attention. Configuration, enablement, and last-check status are separate. Blank replacement credential fields preserve existing secrets; values and reference names remain hidden.
7. Select the ready connection when launching a test. Leaving the selector off performs no Dynatrace calls.

#### Correlation and retention

The capture snapshot is fixed at launch. Log queries use the run start-to-current time range plus the saved base filter. A configured test-ID field is preferred; otherwise observed W3C trace IDs are used. If neither is available, capture falls back to the bounded time window and base filter.

Live values are exposed through external resource metrics with the prefix `dynatrace.<connection-id>.`. Query failures are fail-open: they update capability health and retain last-good values but never affect test execution.

Archives contain only numeric aggregates plus at most 50 deduplicated, scrubbed log samples and 50 problem summaries. Problem summaries retain the latest observed state (including resolution), not an unbounded history of transitions. The separate limits prevent busy logs from crowding out problems. Raw Dynatrace documents, arbitrary fields, DQL responses, token values, and secret-reference names are not archived. Evidence is covered by the normal archive integrity inventory, export, and distributed-run finalization.

Monitoring uses cancellable background workers; live metrics and test controls only read cached capture results. Each poll has a 30-second overall budget. Grail queries explicitly fetch the full test window, including tests longer than two hours. Problem counts traverse at most ten pages; incomplete or failed pagination degrades capture health and preserves last-good counts instead of reporting a truncated count as complete.

#### API

Project administrators manage and test connections under `/api/v1/projects/{id}/monitoring/dynatrace-connections`. Editors may list ready connections and attach one with `dynatrace_capture.connection_id` during test creation. Viewers see only the safe selected connection name and captured results. See the generated [OpenAPI specification](swagger.yaml) for the complete contract.

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

Dynatrace webhooks can subscribe to the same event names as Slack and Teams, including `test_started`, `test_complete`, `test_failed`, `telemetry_degraded`, `telemetry_breached`, `telemetry_recovered`, and fleet alert events.

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
