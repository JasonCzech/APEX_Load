# Elasticsearch, ES|QL, and OpenSearch monitoring

APEX Load reads existing indexed telemetry during an opted-in test. Elasticsearch and OpenSearch use separate adapters over a verified HTTPS transport. This connector works with controller-reachable public services or approved private services through an assigned collector, including Azure Native Elastic, Elastic Cloud, AKS, and on-premises installations. Installing ELK, agents, index templates, or ingest pipelines is outside this connector.


For shared storage/signing, provider dispatch and the qualification inventory, see [observability integrations](OBSERVABILITY_INTEGRATIONS.md#governed-provider-registry). The native source contract below is distinct from the [bucketed legacy-connection adapter](#bucketed-elasticsearch-capture) and [cumulative legacy capture](#optional-elastic-elk-in-flight-monitoring); their different coverage and failure semantics remain explicit.

## Setup

1. Enable the project in `APEXLOAD_RUN_MONITOR_PROJECTS` (comma-separated exact project IDs). Configure persistent monitoring signing keys and durable monitoring artifact storage as described in [monitoring storage and signing](OBSERVABILITY_INTEGRATIONS.md#run-bound-monitoring-storage-and-signing). HA controllers must share the artifact backend and database. The feature remains opt-in.
2. Create logical project-secret references containing the upstream credentials. Use a dedicated identity with read/search permission on the selected indices and field/discovery metadata access. Elasticsearch accepts API key, Basic, Bearer, or mTLS; OpenSearch accepts configured Basic, Bearer, or mTLS. AWS IAM/SigV4 authentication is outside this release. Azure portal sign-in does not supply an Elasticsearch API credential.
3. In the existing Elastic integration settings, save a **disabled** connection with its explicit product, HTTPS endpoint, execution location, and credential references. An optional base path is supported. Redirects, ambient proxies, node sniffing, and credentials in endpoint URLs are prohibited.
4. Private destinations require explicit system-administrator approval of private network CIDRs. Supply the issuing CA PEM when system trust is insufficient; hostname verification remains enabled. Loopback, link-local, multicast, and cloud metadata addresses stay prohibited. Kibana and Logstash have separate endpoints, credentials, CA trust, and network approval. An optional saved `dashboards_url` approves browser navigation to Kibana or OpenSearch Dashboards; it must be an HTTPS URL without credentials, query parameters or fragments. The connector never sends API requests or credentials to that navigation URL.
5. Verify the saved revision, selecting indices when checking document pagination. Discover indices/aliases/data streams and fields, and preview the transformed query output. The asynchronous operation has a 15-minute lifetime. Preview is bounded and scrubbed, and its continuation handles never become public artifacts.
6. Enable the verified connection, then create a reusable `elastic` monitor source under Monitoring → Telemetry. The connection determines Elasticsearch versus OpenSearch and direct versus assigned collector execution. Review field capabilities instead of relying on preset names alone.
7. Bind several sources to a scenario or test. Omitted `monitor_bindings` inherits scenario bindings, `[]` turns monitoring off, and a nonempty list replaces inherited bindings. Sources are optional unless explicitly marked required. Selecting legacy `elk_capture` and a canonical Elastic source for the same connection is rejected.

Connection identity, authentication, trust, and executor changes invalidate verification and clear retained credential references on identity changes unless replacements are supplied. Reference fields are write-only. Blank replacement inputs retain the current reference when identity is unchanged. A prepared or active run retains immutable snapshots; later source edits affect future runs.

## Query and schema mapping

Presets cover ECS logs/errors, Elastic APM transactions/spans/errors, OpenSearch Data Prepper/OTel spans, and custom fields. Explicitly map event timestamp, optional ingestion timestamp, service, run ID, trace/span/parent IDs, status, and duration. ECS `event.duration` is nanoseconds; Elastic APM duration fields commonly use microseconds. Canonical durations are milliseconds. Raw transactions remain separate from histogram or summary metrics.

Sources support up to twenty mapped metrics in total, including advanced metric queries, and five advanced query definitions. Advanced record queries do not count toward the metric limit. Metric units, statistic, dimensions, and native bucket duration are explicit. Source averages require weights; sum/count fields are supported for weighted values. Missing/null stays missing. A complete successful count query can establish zero. Percentiles/cardinality retain approximation metadata. Health API snapshots are instantaneous, without fabricated native buckets.

Query DSL templates are query clauses, rather than arbitrary `_search` request bodies. APEX constructs the endpoint and injects mandatory half-open UTC event ranges, run correlation and service filters. It controls projection, timeouts, pagination and partial results. Scripts, runtime mappings, `global` aggregations, arbitrary endpoints, unrestricted documents, and cross-cluster sources are rejected. `post_filter` cannot bypass aggregation scope.

Elasticsearch ES|QL uses fixed `POST /_query`, named columns, request `filter`, and supported parameters. Use `exact_window` for an unbucketed aggregate; otherwise map timestamp/bucket columns. Weighted averages require sum/count output columns. Advanced pipelines are constrained to approved sources and supported filtering, scalar expressions, projection, grouping, sorting and limits. Joins, external datasets, subqueries and cross-project sources are rejected. OpenSearch rejects ES|QL rather than translating it. ES|QL output is a bounded table and never an exhaustive document capture mechanism.

Logs, errors, spans and transactions use product-specific PIT plus `search_after`. Older OpenSearch versions require an approved unique sortable field when native `_shard_doc` is unavailable. Equal timestamps and identical document IDs in different concrete indices remain separate records. PIT state is private and advances after durable acceptance. Expired PITs restart the same fixed window with canonical deduplication. Index/data-stream rollover is resolved across polling windows.

## Collectors

Register a monitoring collector allowed to use the `elastic` pack. Enroll its X25519 public key and persistent controller Ed25519 signing trust. The collector advertises `elastic.v1`; older collectors fail readiness visibly. Configure local adapter approval, for example `examples/monitoring/elastic-collector-targets.json`. Exact selected wildcard patterns must be locally approved; a signed grant cannot broaden them. `elastic_esql` and `elastic_health` are separate local approvals. Component target trust remains independent.

Sealed grants are assigned, expiring, and tied to immutable run/source revisions. Existing bounded spool persistence and replay handle interrupted uploads. Records are projected and scrubbed before spool storage, controller ingestion, live publication and archive export. Full raw documents and credential values are never evidence.

## Coverage and outcomes

Collection follows elapsed wall time, including soft pauses. It runs outside VUser goroutines. Live windows overlap so delayed records and revised buckets can be reconciled. Event time, ingestion time and receipt/observation time remain distinct. When an ingestion field is mapped, document queries retain the full approved event interval and apply an overlapping ingestion cursor; documents missing that field use the event-window fallback. The cursor advances only after the entire fixed query pass is durably accepted. Final reconciliation clears the ingestion restriction and re-reads the complete final event interval.

For exact run correlation, enable the opt-in `X-Apex-Test-Run` header only for approved HTTP/SOAP origins and arrange for your application or APM pipeline to index it, for example as `labels.apex_test_run`. Set the source's run-ID field to that indexed field. The connector does not install an ingest pipeline. Authored-header conflicts and redirect stripping retain their existing protections. The first 64 observed W3C trace IDs can classify matching retained records as `trace_id`; reaching that bound is disclosed in coverage. The approved query remains unchanged. Service/time matches retain `service_time`, and records without a usable association retain `unmatched`.

After load completion the event interval is fixed at actual load end. Collection re-reads that interval after settlement, continues bounded pagination, and freezes canonical evidence after stable reconciliation or the deadline. Complete coverage means complete for the approved query and final read horizon; it does not promise telemetry that arrives indefinitely later. Upstream sampling or missing span ancestors can produce a partial trace even with complete source-query pagination.

Component health retains its actual observation time between 30-second polls. Final reconciliation requires a successful post-settlement snapshot and reuses it during stability checks, so health-only sources can complete without inventing another instantaneous measurement. Failed or partial health reads retain their status and retry within the bounded cadence.

Owned PIT and async handles are explicitly released after collection, preview, cancellation, final-window replacement and shutdown. Private retired handles remain durable until cleanup is acknowledged. Assigned collectors receive separate signed cleanup leases and can release only the exact handle for their frozen connection and assignment; disabling new capture still permits cleanup. An unavailable endpoint may require retry until the upstream keep-alive expires.

The UI shows per-source/per-query status, watermarks, stale data, partial reads and truncation. Optional-source failures do not stop load traffic. Required-source incompleteness is an acceptance failure separate from threshold failure. Final monitoring rules evaluate retained canonical data only after freeze. They support `avg|min|max|sum`, or `value` for one exact terminal query-window metric. Percentiles are re-read for that interval, never averaged across buckets or fed into load-engine percentile digests. Missing/incompatible inputs produce `not_evaluated`; a failure-severity rule that cannot evaluate prevents successful acceptance. Metric rules can remain valid when an independently captured log signal is truncated.

Live and archived panels show metric overlays, projected logs/errors, spans with missing-parent indicators, and retained component health. Captured content is rendered as text; captured URLs are never executed. Download scrubbed canonical monitoring JSONL for analysis. Legacy ELK and AppDynamics archives remain readable.

## Operating limits

| Resource | Default / ceiling |
|---|---|
| Indexed polling / health-only polling | 10 s / 30 s |
| Bucket / settlement / overlap | 10 s / 60 s / 120 s |
| Final deadline | 300 s after load end |
| Request timeout | 10 s, maximum 30 s |
| Decompressed response | 4 MiB |
| Poll work | 30 s and 20 upstream requests |
| Endpoint concurrency / rate | Two durable executors, 60 requests/minute shared |
| Document page | 100 projected records |
| Points per collection | 10,000 |
| Durable batch | 1 MiB hard cap, target 768 KiB; 512 points / 100 records |
| Projected record | 8 KiB and 16 extra scalar fields |
| Series | 200/source, 1,000/run |
| Retained records | 10,000/source, 50,000/run |
| Canonical Elastic bytes | 8 MiB/source, 32 MiB/run |
| Accepted immutable Elastic artifacts | 64 MiB/run including revisions |

Sources may lower retention ceilings. Request ceilings belong to deployment policy: administrators can explicitly configure `APEXLOAD_ELASTIC_MAX_REQUESTS_PER_MINUTE` (default 60, hard maximum 600), `APEXLOAD_ELASTIC_MAX_REQUESTS_PER_POLL` (default 20, hard maximum 100), and `APEXLOAD_ELASTIC_MAX_POLL_SECONDS` (default 30, hard maximum 60). Connection budgets cannot exceed these ceilings. Invalid deployment values retain the defaults. Quotas commit with canonical heads and receipts; identical retries do not consume quota twice. Exhaustion stops the affected query and records truncation. Archive adjunct metadata shares the existing 64 MiB allowance with other metadata and preserves its finalization reserve; this is not the whole performance archive limit.

## CLI, API and compatibility evidence

`apexctl elastic` provides `list|create|get|update|delete|test|inspect|operation|cancel`. For example, `apexctl elastic inspect --project PROJECT --id CONNECTION --revision 2 --kind verify --wait`. Query preview and field discovery use `--file` with the typed request. Existing `apexctl monitor` commands manage sources, bindings, run series and evidence. MCP has equivalent `elastic_connection_*` and `elastic_inspection_*` tools plus the generic monitoring tools.

Use the generated [OpenAPI](swagger.yaml) and [endpoint catalog](api-endpoint-catalog.json) for request shapes and current routes. [Implementation specification](ELASTIC_SPEC.md) records contracts and qualification gates. Local TLS/auth, query and pagination trials pass for Elasticsearch 8.14.3/9.1.0 and OpenSearch 2.19.0/3.3.0, including scoped readers, filtered aliases and both OpenSearch tie paths. Hosted/Serverless, Azure Native and actual collector-to-vendor deployments remain unqualified. These local results do not extend to every patch version or deployment. Serverless capabilities are probed rather than inferred from reported version.

## Bucketed Elasticsearch capture

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
2. Open **Settings → Elastic test capture** (`#/settings?setting=elk-capture`), or use **Manage Elastic connections** from Monitoring → Telemetry. Confirm the active project. **Save draft** needs only a name; the checklist identifies details to supply later.
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
