# Dynatrace SaaS test monitoring

APEX Load reads Dynatrace SaaS monitoring data during explicitly selected managed tests. Classic Environment APIs supply metric buckets, entities and relationships, problems and events. Optional Platform/Grail queries supply numeric aggregates, bounded log, event, span and sampled trace summaries, and Smartscape identities. Operators must already instrument the system under test. Capture does not install OneAgent, configure Dynatrace, or ingest telemetry into Dynatrace. Outbound lifecycle notifications remain a separate integration.


Shared pipeline ownership and provider qualification are in [observability integrations](OBSERVABILITY_INTEGRATIONS.md#governed-provider-registry). [Lifecycle webhooks](#dynatrace-lifecycle-webhooks) publish notifications separately from read-side monitoring.

## Set up a connection

1. Store credentials in the project's secret provider. Use a read-only Environment API token for selected Classic capabilities. Metrics, entity discovery, problems and events need their corresponding read permissions. Platform/Grail needs separate authentication: a dedicated OAuth client with client ID, client secret, resource and approved read scopes, or an explicitly configured Platform token. Grant only selected dataset and bucket access. Availability also depends on tenant configuration, data retention and licensing.
2. Open **Settings → Connections → Dynatrace test capture**. **Monitoring → Telemetry** provides a management link. A disabled draft needs only a name. Select the capabilities to verify and enter the appropriate SaaS origins: `https://<tenant>.live.dynatrace.com` for Classic and `https://<tenant>.apps.dynatrace.com` for Platform. Managed/on-premises Dynatrace environments are outside this implementation.
3. Enter logical project-secret references, never token values. Public responses expose only credential-presence flags. Blank replacement inputs retain references only while endpoint/account/authentication identity remains compatible. Identity changes require replacement credentials and verification.
4. Choose **Direct from controller** or **Assigned collector**. A collector must belong to the project, advertise Dynatrace and the current batch contract, have an enrolled credential-sealing key, trust the shared signing configuration, and locally approve the exact SaaS origins. The load generators do not call Dynatrace. Both modes use the same adapter and normalization.
5. Set request and Grail scan budgets. Defaults are 60 requests/minute per Environment identity and 60 per Platform credential identity; actual upstream restrictions still apply. Grail defaults are 1 GB scanned per query and 10 GB per source/run, including a 2 GB final-collection reserve. Administrators may lower limits or explicitly raise the scan limits up to the bounded API maxima of 100 GB/query and 1 TB/source/run. Charges apply to repeated/retried/preview queries, not just returned rows. Unknown cost remains charged against its reservation.
6. Save the connection disabled, then use **Test saved connection**. Verification runs asynchronously from its saved execution location and reports each capability separately. An authenticated response, unsupported dataset, permission denial, successful query with no matching records, throttling and incomplete capture have different meanings. An enabled, fully configured connection is immediately available for per-test capture. Native monitor sources additionally require the saved revision to be verified. Changes to credentials, execution location, selected capabilities or budgets invalidate verification and outstanding preparations.
7. Use **Discover metrics and entities** for metric IDs/descriptors, entity types, selected Classic entities and Smartscape identities. Narrow selectors/search when results are truncated; continue using the supplied pagination cursor. Classic entity IDs and Smartscape IDs remain distinct namespaces. Discovery and preview make no requests to the SUT.

TLS uses hostname verification and approved public SaaS origins. Redirects, embedded credentials, non-HTTPS provider URLs, unexpected ports, URL queries/fragments, insecure TLS bypasses and private/metadata/link-local/loopback destinations are rejected. DNS results are validated when connecting. Controller and Platform credentials cannot be sent to one another's origin.

Select only the read permissions needed by the approved sources. Classic token scopes are documented in [Dynatrace access tokens](https://docs.dynatrace.com/docs/manage/identity-access-management/access-tokens-and-oauth-clients/access-tokens); Grail additionally needs access to the selected data buckets and tables under [Grail permissions](https://docs.dynatrace.com/docs/platform/grail/organize-data/assign-permissions-in-grail).

| Selected capability | Read scope / permission |
|---|---|
| Classic metric descriptors and buckets | `metrics.read` |
| Classic entities, types and relationships | `entities.read` |
| Classic problems / events | `problems.read` / `events.read` |
| Grail logs / event summaries | `storage:logs:read` / `storage:events:read` |
| Grail span and sampled trace summaries | `storage:spans:read` |
| Grail numeric metrics | `storage:metrics:read` |
| Grail entity functions / Smartscape | `storage:entities:read` / `storage:smartscape:read` |
| Queried Grail buckets | `storage:buckets:read`, constrained to approved datasets |

Smartscape node and edge queries use the [Smartscape DQL commands](https://docs.dynatrace.com/docs/platform/grail/dynatrace-query-language/commands/smartscape-commands). Permission requirements describe upstream contracts; connection verification and later real-tenant qualification establish which selected capabilities work in an APEX deployment.

## Select sources for tests

In **Monitoring → Telemetry → Run monitor sources**, choose `dynatrace` and a saved verified connection. The connection supplies its execution location and collector assignment. Give the source a stable name and choose its entity scope, Classic metrics, problems, events, topology and optional Grail definitions. Sources permit 20 Classic metric selections, five Grail definitions and 200 expanded series. Native polling defaults to 60 seconds.

Classic metrics retain descriptor units, dimensions, source aggregation, bucket boundaries and returned resolution. Use separate aliases for selected series. Query transforms that fold away time, fabricate missing data or shift time are rejected. Explicit zero remains a measurement; missing buckets remain gaps. Safe unit/rate transformations must match their validated descriptor semantics. Request averages cannot become whole-run averages without valid observation weights. Descriptor-approved gauge averages use elapsed-time weighting. Bucket percentiles never become whole-run percentiles.

The source's **Problems/events/topology entity selector** scopes Classic diagnostic and topology queries. Each Classic metric has its own independent entity selector; leaving it blank uses the metric selector's scope. Grail definitions use their own approved entity field and IDs. Do not assume that the diagnostic selector narrows metrics or Grail results.

Choose **Classic entities** or **Smartscape** topology when needed. Problems retain identity and latest observed status, including closure. Optional source problem/event selectors narrow these diagnostic records. Events retain bounded safe summaries. Grail definitions are administrator-owned, read-only DQL with explicit result kinds and fields. Numeric definitions specify a value field, unit, statistic and a bucket interval when returning time series. Set bucket seconds to zero for a declared whole-window aggregate; its terminal rule requires the exact completed load window and does not freeze the preview window resolution. Log/event/span/trace projections allow only safe summary fields; arbitrary properties, request bodies, SQL text and unrestricted traces are excluded. Entity filters require an approved field and valid provider IDs. Runtime filters are encoded by the adapter rather than inserted as raw query text. Use **Preview Dynatrace selection** to inspect capability coverage and actual observed metadata before selecting acceptance rules.

Attach sources to scenarios for repeatable preparations, schedules, distributed runs, desktop and controller-managed CLI/CI execution. Per-test `monitor_bindings` omitted means inherit scenario bindings; `[]` turns native monitoring off; a nonempty array replaces inherited bindings. Required sources block preparation when unavailable. Source/connection revisions and rules are frozen in the run. Edits affect future runs and invalidate unconsumed preparations. Runtime outages update monitoring health while load execution and controls continue.

The controller-managed CLI uses `apexctl dynatrace list|create|get|update|delete` for connections and `test|discover|preview|operation` for asynchronous inspection. Connection update JSON includes `expected_revision`; delete uses `--revision`. Editors can preview an approved saved source with `apexctl dynatrace preview --project ID --id CONNECTION --source-id SOURCE --wait`; the server derives its frozen selection. Arbitrary `--file` previews, verification and discovery remain administrator operations. `apexctl dynatrace sources|create-source|update-source|delete-source|bindings|bind|series|evidence` provides the shared monitoring workflows, also available under `apexctl monitor`. The MCP tools expose `dynatrace_connection_*` and `dynatrace_operation_*` plus provider-neutral monitor source, binding, series and evidence tools. An approved-source MCP preview supplies `kind: "preview"` and `source_id` without selection/options overrides. Use the generated API contract for the exact request fields.

The terminal criterion editor previews approved sources to select an exact provider series. Preview results are tied to the source and connection revisions; edits require another preview. Editors cannot change the frozen query through this operation, and viewers cannot initiate it.

## Correlation and evidence

Default evidence correlation uses the UTC load window and chosen entity scope. It can include concurrent production activity and does not prove causation. Optional `X-Apex-Test-Run` injection requires approved HTTP/SOAP target origins and operator-managed Dynatrace capture of the header into the configured Grail run ID field. Explicit conflicting headers fail preparation; redirects outside approved target origins do not receive the header. W3C trace IDs stay separate from the opaque run identifier. Evidence labels distinguish run-header, trace, entity/time and unmatched correlation.

Optional W3C correlation uses APEX's existing `traceparent` propagation. Include `trace.id` in the approved Grail span or sampled trace projection. APEX compares returned IDs with up to 64 durably retained run-observed trace identities; only matching evidence receives the trace correlation label. This bounded proof applies to sampled context and does not establish complete request or trace coverage.

Live views display source health, last success, watermark/lag, retry/coverage state, typed series against VUsers/TPS/errors/response times, and bounded diagnostic tables. Missing observations are gaps. Cached last-good values remain visibly aged and are not repeatedly persisted as fresh observations. Native archives, exports and comparisons consume the same canonical observations and latest diagnostic heads.

When load stops, its exact terminal time is recorded immediately. Dynatrace enters **collecting delayed data** for at most 300 seconds, querying only evidence overlapping the original load window. A persisted deadline survives restart/failover. Stable successful settled passes can close earlier; this is a bounded completion criterion, not a promise that upstream will never deliver more data. Archive closure freezes observations, diagnostics, coverage and acceptance. Published archives remain immutable. Partial coverage is distinct from corruption or missing integrity artifacts.

Terminal monitoring criteria select source/metric, unit, dimensions, source statistic, resolution and optional exact provider/catalog identity, plus an evaluation operation, comparator, threshold and fail/warning severity. Defaults require fully contained settled buckets, 95% coverage and at least one eligible bucket. Rules never enter early-stop/load-control logic. Missing required evidence makes the report Inconclusive; CI stays pending during reconciliation and fails an incomplete required-evidence check with an explanation.

## Deployment, migration and troubleshooting

The project run-monitoring gate and durable artifact storage must be ready. HA replicas must share monitoring artifacts and quota/signing state. Filesystem HA needs the same shared mount on each replica and the explicit `APEXLOAD_MONITOR_SHARED_FILESYSTEM=true` administrator attestation. Direct-only execution does not require an unused collector key. Collector mode additionally needs enrolled encryption keys and persistent signing configuration shared across replicas.

Existing connection IDs, references and old archives remain readable. The legacy `dynatrace_capture` input keeps its original per-test capture for projects outside the run-monitoring gate (`APEXLOAD_RUN_MONITOR_PROJECTS`). In gated projects it is translated to an immutable native binding for future runs and requires a verified connection revision; old scalar captures remain scalar evidence and are never relabeled as a bucket timeline. Migration does not automatically select monitoring. Review converted sources/unsupported selectors, verify their revision and explicitly select them. Do not submit overlapping legacy capture and native source bindings for the same provider.

- **Unverified or stale revision:** save disabled, verify the saved revision from the intended location, then enable. Check credential identity and selected capability permissions.
- **Collector pending:** check heartbeat, project/pack assignment, enrolled key, signing trust and local destination approval.
- **Grail unavailable:** check independent Platform authentication, dataset/bucket read permissions, licenses and retained data. Classic metrics do not depend on Grail.
- **Scan budget exhausted or cost unknown:** inspect coverage, reduce queries/window/scope or raise an approved limit. Finalization retains its reserved budget; incomplete evidence must not become a pass.
- **Metric rejected:** inspect descriptor, selector syntax, series/point limits and transformations. Split oversized queries instead of blindly retrying them.
- **Rule not evaluated:** inspect units, entity identities, source aggregation, resolution, settled coverage and observation weights. Short tests may have no fully contained eligible bucket.

## Qualification

| Environment/API | Fixture contract coverage | Real SaaS qualification |
|---|---|---|
| Classic metrics/entities/problems/events | Automated fixtures and regression scenarios | None yet |
| Platform token/OAuth and async Grail queries | Automated fixtures and cancellation/limit scenarios | None yet |
| Grail numeric/log/event/span/trace summaries and Smartscape | Fixture projections and typed ingestion scenarios | None yet |
| Direct/collector, durable ingestion and reconciliation | Fixture integration/race/database/browser scenarios | None yet |

Fixture success does not establish compatibility with a real licensed tenant. Publish exact tenant release/API capabilities, permissions and qualification dates after SaaS testing. See [the implementation contract](DYNATRACE_SPEC.md), [observability integrations](OBSERVABILITY_INTEGRATIONS.md), [generated OpenAPI](swagger.yaml), and [archive retention](ARCHIVE_SOURCE_RETENTION.md).

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

Dynatrace webhooks can subscribe to the same event names as Slack and Teams, including `test_started`, `test_complete`, `test_failed`, `telemetry_degraded`, `telemetry_breached`, `telemetry_recovered`, `telemetry_acceptance`, `rca_analysis_completed`, `run_validity`, `load_phase` and fleet alert events. `load_phase` events are sent as `CUSTOM_ANNOTATION` on the webhook's entity selector. A `telemetry_acceptance` event is an `ERROR_EVENT` when the verdict failed (severity `danger`) and a `CUSTOM_ALERT` when it was inconclusive (severity `warning`). A `run_validity` event is an `ERROR_EVENT` for an invalid run and a `CUSTOM_ALERT` for a suspect one; `rca_analysis_completed` is `CUSTOM_INFO`.

### Root-cause events

- `rca_analysis_completed`: a root-cause analysis completed for a project
  that presents root cause, sent once per analysis. Status is the outcome
  (`supported`, `no_supported_hypothesis`, `insufficient_evidence`); severity
  is `danger` for an invalid run, `warning` for a suspect run or a supported
  cause, otherwise `info`; the summary is the reports' one-line root cause;
  conditions name the analysis, its origin, the run's validity, the report
  route and up to three hypotheses.
- `run_validity`: a run whose load generators or telemetry could not carry
  the load it reports, sent once per run and verdict (`suspect` or
  `invalid`) with its reasons, after the run's archive is attached. It is
  sent whether or not the project presents root cause, because validity is
  judged for every archived run.

Webhooks set to *All events* receive both.
