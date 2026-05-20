# APEX Load API User Guide

This guide is for engineers and operators who want to automate APEX Load from CI/CD, external release systems, or integration scripts. It focuses on the public REST and WebSocket API served by the controller.

## Base URL

The controller serves API endpoints under:

```text
http://<controller-host>:8080/api/v1
```

Local dashboard development normally proxies `/api` from `http://localhost:3000` to the controller. Direct API clients should use the controller origin.

Health checks:

```bash
curl http://localhost:8080/api/v1/system/health
curl http://localhost:8080/api/v1/system/readiness
```

## Authentication

APEX Load supports three auth modes:

- `dev`: local development bypass. Do not use for shared environments.
- `trusted_headers`: an upstream gateway authenticates the user and forwards trusted identity and role headers.
- `oidc`: APEX Load handles OIDC login, callback, signed session, and logout.

Service-account automation can authenticate with either header:

```bash
Authorization: Bearer <api-key>
X-APEX Load-API-Key: <api-key>
```

Service-account requests must satisfy both RBAC role checks and token scopes. For example, a service account with `system_admin` still needs a compatible scope such as `settings:manage` before it can update system settings.

The MCP server under `mcp/` also uses service-account authentication by default. Configure `APEXLOAD_API_KEY` or `APEXLOAD_BEARER_TOKEN` for AI-agent and CI/CD automation rather than relying on local dev preview headers.

Dashboard operators with service-account management permission can generate, rotate, disable, and delete scoped API keys from **Access & API Keys**. APEX Load returns created or rotated keys once and then retains only the hash.

Useful auth endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/auth/me` | Current auth mode, user, roles, and session metadata. |
| `GET` | `/api/v1/auth/effective-access?project_id=<id>` | Operations the current principal can perform. |
| `GET` | `/api/v1/auth/access-matrix` | Full role-to-operation catalog. |
| `GET` | `/api/v1/auth/diagnostics` | OIDC/trusted-header readiness diagnostics. |
| `GET` | `/api/v1/auth/service-accounts` | List service accounts. |
| `POST` | `/api/v1/auth/service-accounts` | Create a service account and return its one-time API key. |
| `POST` | `/api/v1/auth/service-accounts/{id}/rotate` | Rotate a service-account API key. |
| `GET` | `/api/v1/auth/audit-events` | Query audit events. |
| `GET` | `/api/v1/auth/audit-events/export` | Export audit events as CSV. |

## Request Conventions

- JSON requests should send `Content-Type: application/json`.
- JSON responses use `Content-Type: application/json`.
- Error responses use the shape `{"error":"message"}` and standard HTTP status codes.
- Most list endpoints accept scoping query parameters such as `project_id`, `application_id`, and `release_id`.
- Download/export endpoints may return JSON, CSV, HTML, ZIP, or binary artifacts depending on the endpoint and query parameters.

Common status codes:

| Status | Meaning |
| --- | --- |
| `200` | Request succeeded. |
| `201` | Resource created. |
| `202` | Long-running operation accepted. |
| `400` | Invalid request body or query parameter. |
| `401` | Authentication required or token invalid. |
| `403` | Authenticated but not allowed by RBAC or token scope. |
| `404` | Resource not found. |
| `409` | Conflict, such as scheduling or governance conflicts. |
| `500` | Controller-side failure. |

## Dev Preview Headers

In local `dev` auth mode, the dashboard can simulate Admin or project editor User views. API clients can use the same preview headers for local checks:

```text
X-APEX Load-Dev-View: admin|user
X-APEX Load-Dev-Project-ID: <project-id>
```

These headers are honored only when the controller auth mode is `dev`. They are ignored in `trusted_headers` and `oidc` modes.
`user` mode uses global viewer access plus active-project editor access, so local checks match the common script/scenario authoring persona without granting system administration.

## Core Workflows

### 1. Discover Domain, Repository, Project, And Release Scope

```bash
curl -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  http://localhost:8080/api/v1/domains

curl -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  http://localhost:8080/api/v1/domains/default/application-repository/categories

curl -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  "http://localhost:8080/api/v1/projects?domain_id=default&category_id=uncategorized"

curl -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  "http://localhost:8080/api/v1/projects/project-1/releases"
```

APEX Load now organizes work as `Domain > Application Repository > Category > Project > Release`. Projects remain the execution and governance boundary, and the older application-scoped endpoints continue to work as compatibility aliases for clients that still organize releases by application.

### 2. Create Or Import A Script

Create a JSON DSL script:

```bash
curl -X POST http://localhost:8080/api/v1/scripts \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d @script.json
```

Validate or replay before saving:

```bash
curl -X POST http://localhost:8080/api/v1/scripts/validate \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d @script.json
```

Import endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/v1/scripts/import/loadrunner` | Import a LoadRunner/VuGen ZIP with protocol auto-detection. |
| `POST` | `/api/v1/scripts/import/loadrunner/validate` | Validate a LoadRunner archive without saving. |
| `GET` | `/api/v1/scripts/import/loadrunner/support-matrix` | LoadRunner protocol import support. |
| `POST` | `/api/v1/scripts/import/jmeter` | Import a JMeter test plan. |
| `POST` | `/api/v1/scripts/import/har` | Import a HAR archive. |
| `POST` | `/api/v1/scripts/import/openapi` | Generate HTTP/API drafts from OpenAPI or Swagger. |
| `POST` | `/api/v1/scripts/import/wsdl` | Generate SOAP drafts from WSDL. |
| `POST` | `/api/v1/scripts/import/devweb` | Import DevWeb-style assets. |
| `POST` | `/api/v1/scripts/import/sample` | Import raw HTTP, cURL, REST sample requests. |

### 3. Assemble And Validate A Scenario

Scenarios combine scripts, load model, runtime settings, data pools, release context, and scheduling metadata.

```bash
curl -X POST http://localhost:8080/api/v1/scenarios \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d @scenario.json

curl -X POST http://localhost:8080/api/v1/scenarios/validate-load-model \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d @scenario.json
```

Useful scenario endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/scenarios` | List scenarios. |
| `GET` | `/api/v1/scenarios/{id}` | Get one scenario. |
| `PUT` | `/api/v1/scenarios/{id}` | Update a scenario. |
| `DELETE` | `/api/v1/scenarios/{id}` | Delete a scenario. |
| `GET` | `/api/v1/scenarios/load-models/support-matrix` | Supported load model features. |
| `GET` | `/api/v1/scenarios/load-models/templates` | Starter load model templates. |
| `GET` | `/api/v1/scenarios/{id}/runtime-settings/report` | Runtime settings compatibility report. |
| `GET` | `/api/v1/scenarios/{id}/data-pools` | Scenario data-pool bindings. |
| `PUT` | `/api/v1/scenarios/{id}/data-pools` | Update scenario data-pool bindings. |

### 4. Run And Control Tests

Create a run directly:

```bash
curl -X POST http://localhost:8080/api/v1/tests \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d @test-run.json
```

Control endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/tests` | List runs. |
| `GET` | `/api/v1/tests/{id}` | Get run status and live summary. |
| `POST` | `/api/v1/tests/{id}/start` | Start a queued run. |
| `POST` | `/api/v1/tests/{id}/stop` | Gracefully stop a run. |
| `POST` | `/api/v1/tests/{id}/abort` | Abort immediately. |
| `POST` | `/api/v1/tests/{id}/pause` | Soft-pause between actions. |
| `POST` | `/api/v1/tests/{id}/resume` | Resume a paused run. |
| `PATCH` | `/api/v1/tests/{id}/vusers` | Change VUser count for a group. |
| `PATCH` | `/api/v1/tests/{id}/duration` | Extend or shorten duration. |
| `GET` | `/api/v1/tests/{id}/metrics` | Current metrics snapshot. |

### 5. Stream Live Events

Use the global WebSocket for dashboard-style updates:

```text
ws://<controller-host>:8080/api/v1/ws
```

Messages are JSON and include a `type` field such as `metrics_update`, `test_created`, `test_started`, `test_complete`, `test_failed`, `test_paused`, `test_resumed`, `vusers_changed`, `duration_changed`, and `latency_update`.

### 6. Download Reports And Archives

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/tests/{id}/report` | HTML report. |
| `GET` | `/api/v1/tests/{id}/archive` | Full run archive. |
| `GET` | `/api/v1/tests/{id}/archive/manifest` | Archive manifest. |
| `GET` | `/api/v1/tests/{id}/archive/report` | Structured run report. |
| `GET` | `/api/v1/tests/{id}/archive/transactions` | Transaction data. |
| `GET` | `/api/v1/tests/{id}/archive/resources` | Resource metrics. |
| `GET` | `/api/v1/tests/{id}/archive/comparison` | Baseline comparison. |
| `GET` | `/api/v1/releases/{id}/trend/export` | Release trend CSV. |

Some reporting endpoints accept `format` or `profile` query parameters where implemented, for example `format=html|json|csv|jsonl|parquet` and `profile=executive|engineer|audit`.

## Endpoint Catalogue

### Domain, Repository, Project, And Release Scope

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/domains` |
| `GET` / `PUT` / `DELETE` | `/api/v1/domains/{id}` |
| `GET` / `PUT` | `/api/v1/domains/{id}/application-repository` |
| `GET` / `POST` | `/api/v1/domains/{id}/application-repository/categories` |
| `PUT` / `DELETE` | `/api/v1/application-repository/categories/{id}` |
| `GET` / `POST` | `/api/v1/projects` |
| `GET` / `PUT` / `DELETE` | `/api/v1/projects/{id}` |
| `GET` / `POST` | `/api/v1/projects/{id}/releases` |
| `GET` / `POST` | `/api/v1/projects/{pid}/folders` |
| `PUT` / `DELETE` | `/api/v1/folders/{id}` |
| `GET` / `POST` | `/api/v1/applications` |
| `GET` / `PUT` / `DELETE` | `/api/v1/applications/{id}` |
| `GET` / `POST` | `/api/v1/applications/{id}/releases` |
| `GET` / `PUT` / `DELETE` | `/api/v1/releases/{id}` |
| `POST` | `/api/v1/releases/{id}/archive` |
| `GET` | `/api/v1/releases/{id}/sla-governance` |
| `POST` | `/api/v1/releases/{id}/sla-governance/waivers` |

### Scripts And Authoring

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/scripts` |
| `GET` / `PUT` / `DELETE` | `/api/v1/scripts/{id}` |
| `PATCH` | `/api/v1/scripts/{id}/move` |
| `POST` | `/api/v1/scripts/validate` |
| `POST` | `/api/v1/scripts/replay` |
| `POST` | `/api/v1/scripts/browser-replay/sessions` |
| `POST` | `/api/v1/scripts/browser-to-http/preview` |
| `GET` | `/api/v1/scripts/authoring/capabilities` |
| `GET` | `/api/v1/scripts/authoring/templates` |
| `GET` | `/api/v1/scripts/authoring/components` |
| `GET` / `POST` | `/api/v1/scripts/authoring/sessions` |
| `GET` / `PUT` / `DELETE` | `/api/v1/scripts/authoring/sessions/{id}` |
| `POST` | `/api/v1/scripts/authoring/sessions/{id}/approve` |
| `POST` | `/api/v1/scripts/authoring/sessions/{id}/correlations/preview` |
| `POST` | `/api/v1/scripts/{id}/validate` |
| `POST` | `/api/v1/scripts/{id}/replay` |
| `POST` | `/api/v1/scripts/{id}/browser-replay/sessions` |
| `POST` | `/api/v1/scripts/{id}/export` |
| `GET` | `/api/v1/scripts/{id}/download` |
| `GET` | `/api/v1/scripts/{id}/runtime-settings/report` |

### Data Pools

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/data-pools` |
| `GET` / `PUT` / `DELETE` | `/api/v1/data-pools/{id}` |
| `GET` / `POST` | `/api/v1/data-pools/{id}/rows` |
| `PUT` / `DELETE` | `/api/v1/data-pools/{id}/rows/{row_id}` |

### Scheduling

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/scenarios/{id}/schedules` |
| `PUT` / `DELETE` | `/api/v1/scenarios/{id}/schedules/{schedule_id}` |
| `GET` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences` |
| `POST` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/approve` |
| `POST` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/reject` |
| `GET` | `/api/v1/schedules/health` |

### System, Fleet, Usage, And Parity

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/system/health` |
| `GET` | `/api/v1/system/readiness` |
| `GET` | `/api/v1/system/inventory` |
| `GET` | `/api/v1/system/usage` |
| `GET` | `/api/v1/system/usage/export` |
| `GET` / `POST` | `/api/v1/system/usage/snapshots` |
| `GET` | `/api/v1/system/parity-status` |
| `GET` | `/api/v1/system/browser-benchmarks` |
| `GET` | `/api/v1/runtime-settings/support-matrix` |
| `GET` | `/api/v1/protocol-bundles/support-matrix` |
| `GET` | `/api/v1/fleet/alerts` |
| `GET` | `/api/v1/fleet/capacity` |
| `PATCH` | `/api/v1/system/generators/drain` |
| `GET` / `PUT` | `/api/v1/system/settings` |
| `GET` | `/metrics` |

### AI Assistance And Integrations

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/ai/authoring/contract` |
| `GET` | `/api/v1/ai/policy` |
| `GET` | `/api/v1/ai/review-queue` |
| `POST` | `/api/v1/ai/drafts` |
| `POST` | `/api/v1/ai/repairs` |
| `POST` | `/api/v1/ai/explain` |
| `GET` | `/api/v1/integrations/apm/profiles` |
| `GET` | `/api/v1/monitoring/packs` |
| `GET` | `/api/v1/reports/profiles` |
| `POST` | `/api/v1/results/import` |
| `GET` | `/api/v1/results/imports` |

## Operational Tips

- Prefer service accounts for CI/CD and external automations.
- Use `GET /api/v1/auth/effective-access` before enabling UI or pipeline actions for a principal.
- Validate scripts and load models before creating or running tests.
- Scope list calls with `project_id`, `application_id`, and `release_id` to keep automation deterministic.
- Archive run evidence after release gates and export audit events for governance changes.
- Use `GET /api/v1/system/parity-status` and support matrix endpoints when building external readiness checks.
