# Optional Elastic (ELK) in-flight monitoring

APEX Load can poll an Elasticsearch-compatible search endpoint while a test is running. The integration is project-scoped, inactive by default, and runs in the controller rather than in virtual-user goroutines. It does not configure Kibana and does not write to Logstash.

## Compatibility and privileges

The client uses the Elasticsearch 7/8-compatible JSON REST search API:

- `POST /{index-pattern}/_search`
- `track_total_hits: true`
- a timestamp `range` filter and optional `query_string` filters
- filter aggregations for error counts
- `avg`, `min`, `max`, and `sum` numeric metric aggregations

The Elasticsearch identity needs only read/search access to the configured index pattern and fields. Do not grant index administration, document write, cluster administration, or Kibana privileges.

## Setup

1. Create the authentication material as project secrets. API-key authentication uses one secret whose value is the Elasticsearch REST API key. Basic authentication uses separate username and password secrets.
2. Open **Settings → Connections → Elastic test capture**, or use **Manage Elastic connections** from Monitoring → Telemetry. Confirm the active project. **Save draft** needs only a name; the checklist identifies details to supply later.
3. Enter an HTTPS Elasticsearch endpoint, index pattern, field mappings, authentication type, and project-secret references. Secret-reference names are accepted as write-only input and are not returned by the API.
4. Use **Test saved connection** while it is disabled, then edit and select **Enable for new tests**. Configuration, enablement, and last-check status are separate. Only enabled, fully configured connections appear in the create-test selector. Blank replacement credential fields preserve existing secrets; saved values and reference names remain hidden.
5. Opt a test into capture with **Elastic log capture**. Existing tests and new tests without a selection make no ELK requests.

No environment-specific endpoint or credential belongs in source control.

## Field-mapping checklist

- Timestamp field: required; usually `@timestamp`.
- Level field: optional; for example `log.level`.
- Message field: required for a ready connection; usually `message`.
- Test ID field: optional. When configured, APEX Load adds an exact term filter for the current test ID.
- Trace ID field: optional. When no test-ID field is configured, observed run trace IDs are used for correlation as they become available and are retained in evidence samples for cross-tool navigation.
- Base filter: optional fixed Elasticsearch query-string expression applied to every poll.
- Error filter: optional query-string expression used to calculate the cumulative error count.
- Numeric aggregations: up to 16 named field aggregations using `avg`, `min`, `max`, or `sum`.

When no test-ID mapping is supplied, APEX Load uses observed trace IDs if a trace field is mapped. Before any trace is observed—or when neither correlation field is mapped—capture falls back to the test start-to-current time window plus the base filter. This fallback is deliberately broad; use a test identifier or a sufficiently restrictive base filter when multiple workloads share an index.

## Runtime and privacy behavior

The controller polls at a bounded cadence and queries the cumulative window from test start through the current poll. Failures are fail-open: they update capture health but do not stop, pause, or otherwise affect the load test. Cumulative values are published as external resource metrics under `elk.<connection-id>.<metric>`, including `logs_total`, `errors_total`, and configured aggregation names.

Polling runs in a cancellable background worker; live metric publication reads cached results without waiting for Elasticsearch. Timed-out searches, failed shards, early termination, and inexact hit counts are treated as incomplete capture rather than healthy results. Last-good values remain available with degraded health. Both local and distributed runs contribute observed trace IDs to the bounded correlation set.

Search responses are size-bounded. Only mapped fields are requested. A run retains at most 50 deduplicated samples, and individual messages are capped at 4 KiB. Built-in credential/PII scrubbing runs before samples reach live state and again at the archive boundary. The archive stores only cumulative metrics and bounded scrubbed samples in `elk_evidence.jsonl`; it never stores raw Elasticsearch documents, authentication values, or secret-reference names.

HTTPS is required. Requests are restricted by the outbound network policy to the one saved endpoint host, including for internal Elasticsearch deployments, and redirects cannot escape that exact origin. Credential-bearing, query-bearing, fragment-bearing, cleartext, and otherwise malformed endpoint URLs are rejected.

Connection edits do not mutate an active or archived run: APEX Load snapshots the operational capture definition into durable private test state at creation. Public run provenance and the manifest retain only the immutable connection ID and safe display name.
