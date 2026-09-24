# Monitoring integrations

Status: Current

Source note: consolidated 2026-09-24 from the former `DYNATRACE_SAAS_MONITORING.md`, `DYNATRACE_WEBHOOKS.md` and `ELK_MONITORING.md`. Prometheus, OpenTelemetry and Power BI have their own references: [Prometheus metrics](PROMETHEUS_METRICS.md), [OpenTelemetry](OPENTELEMETRY.md), [Power BI](POWERBI_INTEGRATION.md).

| Integration | Direction | Scope |
| --- | --- | --- |
| Dynatrace SaaS monitoring | APEX Load polls Dynatrace during a test | Project-scoped connection, opt-in per test |
| Dynatrace lifecycle webhooks | APEX Load pushes events to Dynatrace | Notification webhook (Settings) |
| Elastic (ELK) monitoring | APEX Load polls Elasticsearch during a test | Project-scoped connection, opt-in per test |

## Dynatrace SaaS in-flight monitoring

APEX Load can optionally poll Dynatrace SaaS while a test runs. Connections are reusable within one project, are inert until enabled and complete, and are never selected automatically. The controller performs all monitoring calls; VUsers and generators do not call Dynatrace.

### Supported APIs

- Environment API v2 metrics (`/api/v2/metrics/query`) for named numeric selectors.
- Environment API v2 problems (`/api/v2/problems`) for bounded problem summaries.
- Dynatrace Platform Grail query API (`/platform/storage/query/v1/query:execute`) for bounded log samples.

Environment endpoints must be HTTPS URLs beneath `*.live.dynatrace.com`; Platform endpoints must be HTTPS URLs beneath `*.apps.dynatrace.com`. Requests are restricted to the saved host and redirects are rejected. The integration does not configure dashboards, ingest data into Dynatrace, or change monitored entities.

### Authentication and least privilege

Credential values stay in the existing project-secret system. Connection writes accept logical secret references, but reads return only `has_*` flags and never return credential values or secret-reference names.

Environment API metrics/problems use an API token with only the read scopes required by the selected APIs (normally `metrics.read`, `entities.read`, and `problems.read`). Grail logs can use either a Platform token or an OAuth client. Grant only log/storage query access needed for the configured DQL filter. OAuth clients require client-ID and client-secret project secrets, a resource, and explicit scopes.

### Setup checklist

1. In **Settings → Connections → Dynatrace test capture**, add a project connection. Monitoring → Telemetry provides a summary and management link. Confirm the project; **Save draft** needs only a name.
2. Enable one or more capabilities: metrics, problems, or Grail logs.
3. Supply the corresponding SaaS endpoint and project-secret references.
4. For metrics, add up to ten stable names and metric selectors, optional entity selectors, and an `avg`, `min`, `max`, or `sum` reduction.
5. For logs, confirm the timestamp, status, message, test-ID, and trace-ID field mappings. Set an administrator-owned base DQL filter that limits capture to the intended service or environment.
6. Use **Test saved connection** before enabling it for new tests. Capability health is reported independently, so a partial result identifies which API needs attention. Configuration, enablement, and last-check status are separate. Blank replacement credential fields preserve existing secrets; values and reference names remain hidden.
7. Select the ready connection when launching a test. Leaving the selector off performs no Dynatrace calls.

### Correlation and retention

The capture snapshot is fixed at launch. Log queries use the run start-to-current time range plus the saved base filter. A configured test-ID field is preferred; otherwise observed W3C trace IDs are used. If neither is available, capture falls back to the bounded time window and base filter.

Live values are exposed through external resource metrics with the prefix `dynatrace.<connection-id>.`. Query failures are fail-open: they update capability health and retain last-good values but never affect test execution.

Archives contain only numeric aggregates plus at most 50 deduplicated, scrubbed log samples and 50 problem summaries. Problem summaries retain the latest observed state (including resolution), not an unbounded history of transitions. The separate limits prevent busy logs from crowding out problems. Raw Dynatrace documents, arbitrary fields, DQL responses, token values, and secret-reference names are not archived. Evidence is covered by the normal archive integrity inventory, export, and distributed-run finalization.

Monitoring uses cancellable background workers; live metrics and test controls only read cached capture results. Each poll has a 30-second overall budget. Grail queries explicitly fetch the full test window, including tests longer than two hours. Problem counts traverse at most ten pages; incomplete or failed pagination degrades capture health and preserves last-good counts instead of reporting a truncated count as complete.

### API

Project administrators manage and test connections under `/api/v1/projects/{id}/monitoring/dynatrace-connections`. Editors may list ready connections and attach one with `dynatrace_capture.connection_id` during test creation. Viewers see only the safe selected connection name and captured results. See the generated [OpenAPI specification](swagger.yaml) for the complete contract.

## Dynatrace lifecycle webhooks

APEX Load sends lifecycle and telemetry-health notifications to Dynatrace through the Dynatrace Events API v2. This is an event webhook integration, not an OTLP exporter.

### Setup

1. Create a Dynatrace API token with event-ingest permissions.
2. In APEX Load settings, add a notification webhook with format `dynatrace`.
3. Use the Dynatrace Events API endpoint, for example `https://<tenant>/api/v2/events/ingest`.
4. Paste the API token once. The settings API stores it server-side and returns only `has_api_token: true`; the raw token is write-only.
5. Optionally set `entity_selector` and `timeout_minutes`.

### Delivery Behavior

- APEX Load sends `Authorization: Api-Token <token>`.
- Retryable delivery failures (`408`, `429`, `5xx`, or transport errors) are retried with bounded exponential backoff and `Retry-After` support.
- Failed deliveries after retry increment `apexload_notification_delivery_failures_total`.
- Dynatrace payloads include an `apexload.idempotency_key` property so downstream workflows can dedupe repeated attempts.

### Events

Dynatrace webhooks can subscribe to the same event names as Slack and Teams, including `test_started`, `test_complete`, `test_failed`, `telemetry_degraded`, `telemetry_breached`, `telemetry_recovered`, and fleet alert events.

## Optional Elastic (ELK) in-flight monitoring

APEX Load can poll an Elasticsearch-compatible search endpoint while a test is running. The integration is project-scoped, inactive by default, and runs in the controller rather than in virtual-user goroutines. It does not configure Kibana and does not write to Logstash.

### Compatibility and privileges

The client uses the Elasticsearch 7/8-compatible JSON REST search API:

- `POST /{index-pattern}/_search`
- `track_total_hits: true`
- a timestamp `range` filter and optional `query_string` filters
- filter aggregations for error counts
- `avg`, `min`, `max`, and `sum` numeric metric aggregations

The Elasticsearch identity needs only read/search access to the configured index pattern and fields. Do not grant index administration, document write, cluster administration, or Kibana privileges.

### Setup

1. Create the authentication material as project secrets. API-key authentication uses one secret whose value is the Elasticsearch REST API key. Basic authentication uses separate username and password secrets.
2. Open **Settings → Connections → Elastic test capture**, or use **Manage Elastic connections** from Monitoring → Telemetry. Confirm the active project. **Save draft** needs only a name; the checklist identifies details to supply later.
3. Enter an HTTPS Elasticsearch endpoint, index pattern, field mappings, authentication type, and project-secret references. Secret-reference names are accepted as write-only input and are not returned by the API.
4. Use **Test saved connection** while it is disabled, then edit and select **Enable for new tests**. Configuration, enablement, and last-check status are separate. Only enabled, fully configured connections appear in the create-test selector. Blank replacement credential fields preserve existing secrets; saved values and reference names remain hidden.
5. Opt a test into capture with **Elastic log capture**. Existing tests and new tests without a selection make no ELK requests.

No environment-specific endpoint or credential belongs in source control.

### Field-mapping checklist

- Timestamp field: required; usually `@timestamp`.
- Level field: optional; for example `log.level`.
- Message field: required for a ready connection; usually `message`.
- Test ID field: optional. When configured, APEX Load adds an exact term filter for the current test ID.
- Trace ID field: optional. When no test-ID field is configured, observed run trace IDs are used for correlation as they become available and are retained in evidence samples for cross-tool navigation.
- Base filter: optional fixed Elasticsearch query-string expression applied to every poll.
- Error filter: optional query-string expression used to calculate the cumulative error count.
- Numeric aggregations: up to 16 named field aggregations using `avg`, `min`, `max`, or `sum`.

When no test-ID mapping is supplied, APEX Load uses observed trace IDs if a trace field is mapped. Before any trace is observed—or when neither correlation field is mapped—capture falls back to the test start-to-current time window plus the base filter. This fallback is deliberately broad; use a test identifier or a sufficiently restrictive base filter when multiple workloads share an index.

### Runtime and privacy behavior

The controller polls at a bounded cadence and queries the cumulative window from test start through the current poll. Failures are fail-open: they update capture health but do not stop, pause, or otherwise affect the load test. Cumulative values are published as external resource metrics under `elk.<connection-id>.<metric>`, including `logs_total`, `errors_total`, and configured aggregation names.

Polling runs in a cancellable background worker; live metric publication reads cached results without waiting for Elasticsearch. Timed-out searches, failed shards, early termination, and inexact hit counts are treated as incomplete capture rather than healthy results. Last-good values remain available with degraded health. Both local and distributed runs contribute observed trace IDs to the bounded correlation set.

Search responses are size-bounded. Only mapped fields are requested. A run retains at most 50 deduplicated samples, and individual messages are capped at 4 KiB. Built-in credential/PII scrubbing runs before samples reach live state and again at the archive boundary. The archive stores only cumulative metrics and bounded scrubbed samples in `elk_evidence.jsonl`; it never stores raw Elasticsearch documents, authentication values, or secret-reference names.

HTTPS is required. Requests are restricted by the outbound network policy to the one saved endpoint host, including for internal Elasticsearch deployments, and redirects cannot escape that exact origin. Credential-bearing, query-bearing, fragment-bearing, cleartext, and otherwise malformed endpoint URLs are rejected.

Connection edits do not mutate an active or archived run: APEX Load snapshots the operational capture definition into durable private test state at creation. Public run provenance and the manifest retain only the immutable connection ID and safe display name.
