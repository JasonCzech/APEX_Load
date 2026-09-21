# Optional Dynatrace SaaS In-Flight Monitoring

## Summary

Add a project-scoped Dynatrace SaaS monitoring connection that is inert until configured. Editors can opt an individual test into capture; the controller then reads selected Dynatrace metrics, active problems, and optional Grail log evidence during the run without adding work to the VUser path.

This is an inbound evidence integration. It complements the existing Dynatrace lifecycle-event webhook and trace propagation rather than replacing them: APEX Load continues to send lifecycle events when that integration is enabled, while this connection reads bounded monitoring evidence back into the live and archived test record.

Dynatrace currently exposes two relevant API surfaces, so the connection must model them explicitly instead of pretending one credential always covers both:

- The Environment API v2 at `https://{environment-id}.live.dynatrace.com/api/v2/...` provides metric queries and problems. Metric reads require `metrics.read`; problem reads require `problems.read`. Environment API tokens use the `Api-Token` authorization scheme. See the official [Metrics API v2](https://docs.dynatrace.com/docs/dynatrace-api/environment-api/metric-v2), [metric data-points endpoint](https://docs.dynatrace.com/docs/dynatrace-api/environment-api/metric-v2/get-data-points), [Problems API v2](https://docs.dynatrace.com/docs/dynatrace-api/environment-api/problems-v2/problems/get-problems-list), and [API authentication](https://docs.dynatrace.com/docs/dynatrace-api/basics/dynatrace-api-authentication) documentation.
- Grail DQL queries use the Platform API at `https://{environment-id}.apps.dynatrace.com/platform/storage/query/v1/query:execute`, followed by `query:poll`. They use Bearer authentication through a platform token or OAuth client. Log access requires table and bucket permissions such as `storage:logs:read` and `storage:buckets:read`; span correlation additionally requires `storage:spans:read`. See the official [DQL Query API workflow](https://docs.dynatrace.com/docs/observe/business-observability/bo-events-capturing/bo-events-capturing-external-sources), [OAuth client](https://docs.dynatrace.com/docs/manage/identity-access-management/access-tokens-and-oauth-clients/oauth-clients), [platform token](https://docs.dynatrace.com/docs/manage/identity-access-management/access-tokens-and-oauth-clients/platform-tokens), and [Grail permissions](https://docs.dynatrace.com/docs/platform/grail/organize-data/assign-permissions-in-grail) documentation.

## Key Changes

### Connection model and secrets

- Add persisted, project-owned Dynatrace SaaS connections with `unconfigured`, `ready`, and `disabled` configuration states plus capability-level last-check health. A connection is `ready` only when every enabled capture capability has its required URL, query definition, and authentication reference.
- Store a safe display name, description, enablement, SaaS environment URL, optional Platform URL, enabled capabilities, query definitions, correlation mappings, polling cadence, and retention limits.
- Support these authentication profiles through existing project-secret references:
  - Environment API token: one write-only token reference, sent as `Authorization: Api-Token ...`.
  - Platform token: one write-only token reference, sent as `Authorization: Bearer ...`.
  - OAuth client credentials: write-only client-ID and client-secret references plus the configured account/resource identifier and an allowlisted scope set. Cache access tokens in memory only, refresh before expiry, and single-flight concurrent refreshes.
- Never return credential values or secret-reference names. Safe responses expose only flags such as `has_environment_token`, `has_platform_token`, and `has_oauth_client`.
- Require HTTPS, reject embedded credentials/query fragments, pin each request to the saved Environment or Platform host, reject cross-origin redirects and scheme downgrades, and reuse the shared outbound-network/SSRF policy. Do not accept arbitrary token or API URLs from launch requests.
- Keep all connections inactive by default. Do not add environment-specific URLs, selectors, tenant identifiers, or credentials to source control.

### Capture definition

- Configure named Environment API metric queries as `{name, metric_selector, entity_selector?, aggregation}`. Allow at most 10 selectors per request, matching the Metrics API limit, and validate names for the stable external-metric namespace.
- Query the Metrics API over the test start-to-current window with an aggregate resolution/fold so each configured metric produces a bounded scalar. If Dynatrace returns multiple dimension tuples, reduce them with the configured `avg`, `min`, `max`, or `sum` rule and record the tuple count for diagnostics.
- Configure optional problem capture with a fixed administrator-owned problem selector and/or entity selector. Retain only cumulative counts by severity/status and bounded safe summaries containing problem ID, display name, severity, status, start/end time, and affected-entity display names.
- Configure optional Grail log capture with an administrator-owned base filter and fixed field projection. Build the DQL around `fetch logs`, an explicit time window, correlation predicates, `fields`, `sort`, and `limit`; do not let editors submit DQL at launch time.
- Default Grail fields to the Dynatrace semantic log model (`timestamp`, `status`, `content`, and `trace_id`) while allowing project admins to map an explicit test-ID field and safe display fields. Dynatrace documents `trace_id` as the log trace identifier and supports log/trace correlation through trace IDs. See the [log semantic model](https://docs.dynatrace.com/docs/semantic-dictionary/model/log) and [log enrichment guidance](https://docs.dynatrace.com/docs/analyze-explore-automate/logs/lma-log-enrichment).
- If span evidence is enabled later, treat it as a distinct capability with its own `storage:spans:read` permission, query template, sample bounds, and archive record type; do not silently broaden a log-only connection.

### API and authorization

- Add project-scoped CRUD and connection-test endpoints under `/api/v1/projects/{id}/monitoring/dynatrace-connections` and regenerate OpenAPI artifacts and dashboard contract types.
- Project admins create, update, delete, and test connections. Editors can list only safe, ready choices and attach one to a test. Viewers see only the selected connection name, capability health, live values, and archived evidence.
- Reuse optimistic update checks, project-isolation enforcement, project-deletion cleanup, audit events, and request-size limits.
- Make connection testing capability-aware:
  - Validate Environment API authentication and execute a bounded one-minute metric/problem query for enabled Environment capabilities.
  - Validate Platform authentication, token acquisition/refresh, and execute/poll a minimal bounded DQL query for enabled Grail capabilities.
  - Return per-capability diagnostics without response bodies, tokens, selectors containing secret values, or backend stack details.

### Test lifecycle and correlation

- Extend test creation with `dynatrace_capture: { connection_id }`. Selection remains opt-in for every run and is rejected when the connection is disabled, incomplete, or belongs to another project.
- Snapshot the selected connection's safe capture definition and the credential references needed for restart recovery at launch. Public test state, manifests, exports, and reports serialize only the connection ID/name and non-sensitive capture provenance.
- Poll from the controller on a bounded cadence with a per-run in-progress guard. Never perform Dynatrace I/O in load generators or VUser result sinks.
- Use the run start-to-current interval for cumulative queries. Correlate in this order:
  1. A configured exact test-ID attribute, when applications already attach the APEX test ID to Dynatrace telemetry.
  2. Observed W3C trace IDs from controller results, mapped to Grail `trace_id`/span `trace.id` as appropriate.
  3. The fixed entity/service filter plus the run time window when no correlation identifier exists.
- Cap observed trace IDs, DQL execution time, DQL poll attempts, response size, returned records, metric tuples, and problems. Treat pending/partial/expired DQL results as capture-health conditions rather than test failures.
- Query failures are fail-open: preserve the last good values, publish capability-specific freshness/error status, audit repeated authorization failures at a throttled rate, and never change test execution status or SLA results solely because Dynatrace is unavailable.

### Live metrics and evidence

- Publish cumulative values through external resource metrics with the stable namespace `dynatrace.<connection-id>.<metric>`, including configured metric names, `problems.open`, `problems.total`, `logs.total`, and `logs.errors` when those capabilities are enabled.
- Add a dedicated Dynatrace section to the live test view showing connection/capability status, last refresh, metric values, open-problem summaries, and scrubbed log samples.
- Redact every retained string at the decode and archive boundaries. Bound identifiers, titles, entity names, and messages by UTF-8 bytes. Deduplicate logs by a stable record identifier or timestamp/message/trace fingerprint and problems by problem ID.
- Never retain raw Dynatrace responses, arbitrary record fields, OAuth access tokens, API tokens, client credentials, full DQL result metadata, or fields outside the configured projection.

### Archive and reporting

- Archive bounded evidence in `dynatrace_evidence.jsonl` using typed records such as `log_sample` and `problem_summary`. Metrics continue through resource samples so existing timeline and report aggregation can consume them.
- Include the evidence file and counts in manifest integrity, sanitized export, distributed archive merging, object-storage publication, archive verification, and orphan recovery.
- Add a Dynatrace evidence tier and archive report section with the selected connection's safe provenance, final capability health, final metric values, bounded problem summaries, and bounded redacted log samples.
- Keep live/archive display useful when only one capability is enabled; absence of Grail credentials must not hide valid Environment API metrics or problems.

### Dashboard and documentation

- Add Dynatrace connection management to Monitoring → Telemetry beside the existing Dynatrace event integration, while clearly labeling inbound monitoring capture versus outbound lifecycle events.
- Provide an empty-state setup path, capability toggles, write-only credential inputs, readiness/last-check status, a per-capability Test action, and least-privilege guidance.
- Add a launch-time `Dynatrace SaaS capture` selector to both create-test flows, defaulting to Off and listing only ready connections.
- Document tenant URL discovery, Environment API token creation, platform-token and OAuth-client setup, exact scopes/permissions, metric selector validation, problem/entity selector behavior, DQL correlation, token refresh, response/query limits, privacy controls, and troubleshooting.

## Test Plan

- Storage migration, SQLite/PostgreSQL parity, optimistic updates, connection-state derivation, project isolation, unique names, project-deletion cleanup, and secret non-disclosure tests.
- API/RBAC tests for admin CRUD/test access, editor safe-list access, viewer restrictions, incomplete/disabled connections, cross-project attachment rejection, audit events, and write-only credential replacement/preservation.
- URL/security tests for HTTPS enforcement, saved-host pinning, private-host policy, DNS rebinding defense, embedded credentials, redirect downgrade/cross-origin rejection, response limits, and error-message scrubbing.
- Environment API client fixtures covering `Api-Token` headers, metric selector/time/entity parameters, multi-tuple reduction, missing/late data, pagination/limits, problem selectors, problem deduplication, 401/403/429/5xx responses, and fail-open polling.
- Platform client fixtures covering Bearer platform tokens, OAuth client-credentials exchange, cached-token reuse, expiry refresh, refresh single-flight, allowed scopes/resource, DQL execute/poll success, pending/timeout/partial/failure states, and response bounds.
- Correlation tests for exact test ID, observed trace IDs, time/entity fallback, Dynatrace trace field formats, escaping/injection resistance, and administrator-owned query templates.
- Redaction and privacy tests proving secrets and sensitive fields cannot enter live payloads, manifests, reports, logs, or exports; include multibyte UTF-8 boundary cases and malicious raw field content.
- Test lifecycle, live external metrics, fail-open execution, restart recovery, immutable launch snapshot, archive report/evidence, manifest integrity, sanitized export, distributed-run merge, remote archive publication, and orphan-recovery tests.
- Dashboard unit and browser workflow coverage for empty-state setup, all authentication modes, capability readiness/testing, both launch selectors, live health/evidence, and archive evidence.
- Run `go generate ./pkg/api`, `go test ./...`, and `cd dashboard && npm run test:cycle`; also run the focused dev-admin and local-auth launch/monitoring Playwright workflows when diagnosing suite failures.

## Assumptions and boundaries

- “Dynatrace SaaS monitoring” means read-only capture from a Dynatrace SaaS environment. Managed/ActiveGate URL shapes can be a later extension rather than weakening SaaS URL validation now.
- The initial implementation supports Environment API metrics/problems and optional Grail logs. It does not configure OneAgent, create dashboards, change alerting profiles, ingest metrics/logs, or mutate Dynatrace problems.
- Existing Dynatrace event webhooks remain a separate outbound integration. A project may use either or both.
- Connections are reusable per project but never active by default; every test explicitly opts in.
- The initial retention policy is numeric aggregates plus bounded, deduplicated, redacted problem/log evidence. Raw API and DQL responses are never archived.
- Metric selectors, entity/problem selectors, and DQL base filters are supplied later by a project admin. No real tenant, credential, account UUID, service name, or environment-specific selector is committed with the plumbing.
