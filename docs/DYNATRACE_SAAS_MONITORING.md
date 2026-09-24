# Dynatrace SaaS in-flight monitoring

APEX Load can optionally poll Dynatrace SaaS while a test runs. Connections are reusable within one project, are inert until enabled and complete, and are never selected automatically. The controller performs all monitoring calls; VUsers and generators do not call Dynatrace.

## Supported APIs

- Environment API v2 metrics (`/api/v2/metrics/query`) for named numeric selectors.
- Environment API v2 problems (`/api/v2/problems`) for bounded problem summaries.
- Dynatrace Platform Grail query API (`/platform/storage/query/v1/query:execute`) for bounded log samples.

Environment endpoints must be HTTPS URLs beneath `*.live.dynatrace.com`; Platform endpoints must be HTTPS URLs beneath `*.apps.dynatrace.com`. Requests are restricted to the saved host and redirects are rejected. The integration does not configure dashboards, ingest data into Dynatrace, or change monitored entities.

## Authentication and least privilege

Credential values stay in the existing project-secret system. Connection writes accept logical secret references, but reads return only `has_*` flags and never return credential values or secret-reference names.

Environment API metrics/problems use an API token with only the read scopes required by the selected APIs (normally `metrics.read`, `entities.read`, and `problems.read`). Grail logs can use either a Platform token or an OAuth client. Grant only log/storage query access needed for the configured DQL filter. OAuth clients require client-ID and client-secret project secrets, a resource, and explicit scopes.

## Setup checklist

1. In **Settings → Connections → Dynatrace test capture**, add a project connection. Monitoring → Telemetry provides a summary and management link. Confirm the project; **Save draft** needs only a name.
2. Enable one or more capabilities: metrics, problems, or Grail logs.
3. Supply the corresponding SaaS endpoint and project-secret references.
4. For metrics, add up to ten stable names and metric selectors, optional entity selectors, and an `avg`, `min`, `max`, or `sum` reduction.
5. For logs, confirm the timestamp, status, message, test-ID, and trace-ID field mappings. Set an administrator-owned base DQL filter that limits capture to the intended service or environment.
6. Use **Test saved connection** before enabling it for new tests. Capability health is reported independently, so a partial result identifies which API needs attention. Configuration, enablement, and last-check status are separate. Blank replacement credential fields preserve existing secrets; values and reference names remain hidden.
7. Select the ready connection when launching a test. Leaving the selector off performs no Dynatrace calls.

## Correlation and retention

The capture snapshot is fixed at launch. Log queries use the run start-to-current time range plus the saved base filter. A configured test-ID field is preferred; otherwise observed W3C trace IDs are used. If neither is available, capture falls back to the bounded time window and base filter.

Live values are exposed through external resource metrics with the prefix `dynatrace.<connection-id>.`. Query failures are fail-open: they update capability health and retain last-good values but never affect test execution.

Archives contain only numeric aggregates plus at most 50 deduplicated, scrubbed log samples and 50 problem summaries. Problem summaries retain the latest observed state (including resolution), not an unbounded history of transitions. The separate limits prevent busy logs from crowding out problems. Raw Dynatrace documents, arbitrary fields, DQL responses, token values, and secret-reference names are not archived. Evidence is covered by the normal archive integrity inventory, export, and distributed-run finalization.

Monitoring uses cancellable background workers; live metrics and test controls only read cached capture results. Each poll has a 30-second overall budget. Grail queries explicitly fetch the full test window, including tests longer than two hours. Problem counts traverse at most ten pages; incomplete or failed pagination degrades capture health and preserves last-good counts instead of reporting a truncated count as complete.

## API

Project administrators manage and test connections under `/api/v1/projects/{id}/monitoring/dynatrace-connections`. Editors may list ready connections and attach one with `dynatrace_capture.connection_id` during test creation. Viewers see only the safe selected connection name and captured results. See the generated [OpenAPI specification](swagger.yaml) for the complete contract.
