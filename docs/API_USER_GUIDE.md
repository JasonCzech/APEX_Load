# APEX Load API User Guide

This guide is for engineers and operators who want to automate APEX Load from CI/CD, external release systems, or integration scripts. It focuses on the public REST and WebSocket API served by the controller.

Last reviewed against `pkg/api/server.go` on June 2, 2026.

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
- `trusted_headers`: an upstream gateway authenticates the user and forwards trusted identity headers.
- `oidc`: APEX Load handles OIDC login, callback, signed session, and logout.

For human users, OIDC or trusted headers prove identity, but APEX Load access is controlled by local user records and local role bindings. After bootstrap, an upstream-authenticated human principal must have an active local user whose `subject` matches the authenticated subject. Human effective roles come from `UserRoleBinding` rows, not from upstream role headers. Service accounts keep their existing key-based behavior.

Service-account automation can authenticate with either header:

```bash
Authorization: Bearer <api-key>
X-APEXLoad-API-Key: <api-key>
```

Service-account requests must satisfy both RBAC role checks and token scopes. For example, a service account with `system_admin` still needs a compatible scope such as `settings:manage` before it can update system settings.

The MCP server under `mcp/` also uses service-account authentication by default. Configure `APEXLOAD_API_KEY` or `APEXLOAD_BEARER_TOKEN` for AI-agent and CI/CD automation rather than relying on local dev preview headers.

Dashboard operators with access management permission can onboard users, assign scoped roles, review user activity, and manage scoped API keys from **Access**. APEX Load returns created or rotated service-account keys once and then retains only the hash.

Useful auth endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/auth/me` | Current auth mode, local user status, locally resolved roles, and session metadata. |
| `GET` | `/api/v1/auth/effective-access?project_id=<id>` | Operations allowed for the current principal from local user bindings or service-account roles. |
| `GET` | `/api/v1/auth/access-matrix` | Full role-to-operation catalog. |
| `GET` | `/api/v1/auth/diagnostics` | OIDC/trusted-header readiness diagnostics. |
| `GET` | `/api/v1/auth/users` | Search local human users. |
| `POST` | `/api/v1/auth/users` | Onboard a human user subject. |
| `GET` | `/api/v1/auth/users/{id}` | Get one local user and role bindings. |
| `PATCH` | `/api/v1/auth/users/{id}` | Update local user metadata or active state. |
| `POST` | `/api/v1/auth/users/{id}/bindings` | Grant a global, domain, project, or repository role binding. |
| `DELETE` | `/api/v1/auth/users/{id}/bindings/{binding_id}` | Remove one local role binding. |
| `POST` | `/api/v1/auth/users/{id}/disable` | Disable a local user while preserving audit history. |
| `POST` | `/api/v1/auth/users/{id}/enable` | Re-enable a local user. |
| `GET` | `/api/v1/auth/user-activity` | Audit events plus attributed usage filtered by user, timeframe, domain, project, action, or status. |
| `GET` | `/api/v1/auth/user-activity/export` | Export user activity as CSV. |
| `GET` | `/api/v1/auth/service-accounts` | List service accounts. |
| `POST` | `/api/v1/auth/service-accounts` | Create a service account and return its one-time API key. |
| `PATCH` | `/api/v1/auth/service-accounts/{id}` | Update service-account metadata, roles, scopes, or active state. |
| `POST` | `/api/v1/auth/service-accounts/{id}/rotate` | Rotate a service-account API key. |
| `DELETE` | `/api/v1/auth/service-accounts/{id}` | Delete a service account. |
| `GET` | `/api/v1/auth/audit-events` | Query audit events. |
| `GET` | `/api/v1/auth/audit-events/export` | Export audit events as CSV. |

### Human User Administration

Create a local user record for the subject emitted by OIDC or the trusted-header gateway:

```bash
curl -X POST http://localhost:8080/api/v1/auth/users \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "subject": "oidc|00u123",
    "email": "maya@example.com",
    "display_name": "Maya Chen",
    "active": true
  }'
```

Assign roles with explicit scopes. Omit `scope_id` for `global`; provide a domain, project, or application repository id for narrower scopes.

```bash
curl -X POST http://localhost:8080/api/v1/auth/users/usr_123/bindings \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "scope_type": "project",
    "scope_id": "project-1",
    "role": "project_admin"
  }'
```

Supported roles remain `viewer`, `editor`, `project_admin`, and `system_admin`. Delegated administration is scoped: system admins can manage all users and bindings; domain project admins can manage domain and child-project bindings inside their domain; project admins can manage bindings for their project only. Delegated admins cannot grant `system_admin` or create bindings broader than their own scope.

Disable users instead of deleting them:

```bash
curl -X POST http://localhost:8080/api/v1/auth/users/usr_123/disable \
  -H "Authorization: Bearer $APEXLOAD_API_KEY"
```

Disabling preserves user records, role history, and audit events. Re-enable with `POST /api/v1/auth/users/{id}/enable`.

### User Activity And Audit Filters

User activity combines audit events with per-user run usage attribution for newly attributed runs. Older runs without attribution are reported under an `unattributed` usage bucket.

```bash
curl "http://localhost:8080/api/v1/auth/user-activity?actor_subject=oidc%7C00u123&created_after=2026-05-01T00:00:00Z&domain_id=default&project_id=project-1" \
  -H "Authorization: Bearer $APEXLOAD_API_KEY"
```

`/api/v1/auth/user-activity` and `/api/v1/auth/audit-events` support `actor_subject`, `created_after`, `created_before`, `domain_id`, `project_id`, `action`, `actor_type`, `resource_type`, `resource_id`, `status`, `limit`, and `offset`. Use the matching `/export` endpoints for CSV output.

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
X-APEXLoad-Dev-View: admin|user
X-APEXLoad-Dev-Project-ID: <project-id>
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

Start a unified replay session for a draft script:

```bash
curl -X POST http://localhost:8080/api/v1/scripts/replay/sessions \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "script": {
      "name": "Checkout Debug",
      "version": "1.0.0",
      "protocol": "http",
      "config": { "base_url": "https://example.com" },
      "actions": [{ "name": "Open", "type": "GET", "method": "GET", "target": "/" }]
    },
    "start_paused": true,
    "log_level": "debug",
    "breakpoints": [{ "phase": "action", "action_index": 0 }],
    "ignore_think_time": true
  }'
```

Stored scripts use `POST /api/v1/scripts/{id}/replay/sessions` with the same payload minus `script` unless overriding the draft definition. The response includes `preflight` readiness checks and a `session` object with `mode`, `runtime_profile`, lifecycle state, logs, events, and final report. Legacy `POST /api/v1/scripts/debug/sessions` and browser replay endpoints remain available as compatibility wrappers. Replay sessions are protocol-agnostic; browser scripts include screenshots, DOM, console, network, Core Web Vitals, and HTML evidence when captured.

Replay control endpoints:

```bash
curl -X POST http://localhost:8080/api/v1/replay/sessions/$SESSION_ID/step \
  -H "Authorization: Bearer $APEXLOAD_API_KEY"

curl -X PATCH http://localhost:8080/api/v1/replay/sessions/$SESSION_ID/breakpoints \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{"breakpoints":[{"phase":"action","action_index":2}]}'

curl "http://localhost:8080/api/v1/replay/sessions/$SESSION_ID/export?format=log" \
  -H "Authorization: Bearer $APEXLOAD_API_KEY"
```

`view_scripts` can read and export replay sessions. `manage_scripts` is required to start sessions, pause, resume, step, stop, or update breakpoints. WebSocket messages include stable `event_id` values and use session lifecycle, action lifecycle (`action_started`, `action_completed`, `action_failed`), compatibility `replay_event`, and final report events with `replay_session_id`, `session`, and optional `event`, `action`, or `report` fields.

Import endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/v1/scripts/import/loadrunner` | Import a LoadRunner/VuGen ZIP with protocol auto-detection. |
| `POST` | `/api/v1/scripts/import/loadrunner/validate` | Validate a LoadRunner archive without saving. |
| `GET` | `/api/v1/scripts/import/loadrunner/support-matrix` | LoadRunner protocol import support. |
| `POST` | `/api/v1/scripts/import/jmeter` | Import a JMeter test plan. |
| `POST` | `/api/v1/scripts/import/jmeter/save` | Save a reviewed JMeter import draft. |
| `POST` | `/api/v1/scripts/import/jmeter/validate` | Validate a JMeter archive or plan without saving. |
| `GET` | `/api/v1/scripts/import/jmeter/support-matrix` | JMeter import support matrix. |
| `POST` | `/api/v1/scripts/import/k6` | Import the constrained k6 HTTP subset into APEX Load DSL. |
| `POST` | `/api/v1/scripts/export/k6` | Export an APEX HTTP script to a reviewable k6 HTTP subset script. |
| `GET` | `/api/v1/scripts/import/k6/support-matrix` | k6 import/export support matrix. |
| `POST` | `/api/v1/scripts/import/har` | Import a HAR archive. |
| `POST` | `/api/v1/scripts/import/openapi` | Generate HTTP/API drafts from OpenAPI or Swagger. |
| `POST` | `/api/v1/scripts/import/postman` | Generate HTTP/API drafts from a Postman collection JSON file. |
| `POST` | `/api/v1/scripts/import/wsdl` | Generate SOAP drafts from WSDL. |
| `POST` | `/api/v1/scripts/import/devweb` | Import DevWeb-style assets. |
| `GET` | `/api/v1/scripts/import/devweb/support-matrix` | DevWeb import support matrix. |
| `GET` | `/api/v1/scripts/import/oracle-ebusiness/support-matrix` | Oracle E-Business/Siebel support matrix. |
| `POST` | `/api/v1/scripts/import/oracle-ebusiness/review` | Review Oracle E-Business, Siebel, or Oracle NCA source for supported routes. |
| `POST` | `/api/v1/scripts/import/sample` | Import raw HTTP, cURL, REST sample requests. |
| `GET` | `/api/v1/scripts/web-api/support-matrix` | Web API execution support matrix. |

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
| `GET` | `/api/v1/scenarios/load-models/support-matrix` | Supported load model features, including chaos-resilience workloads. |
| `GET` | `/api/v1/scenarios/load-models/templates` | Starter load model templates, including `chaos-resilience-shakeout`. |
| `GET` | `/api/v1/scenarios/{id}/results` | Runs linked to one scenario. |
| `GET` | `/api/v1/scenarios/{id}/runtime-settings/report` | Runtime settings compatibility report. |
| `GET` | `/api/v1/scenarios/{id}/data-pools` | Scenario data-pool bindings. |
| `PUT` | `/api/v1/scenarios/{id}/data-pools` | Update scenario data-pool bindings. |
| `GET` | `/api/v1/scenarios/{id}/schedules` | Scenario schedules. |
| `POST` | `/api/v1/scenarios/{id}/schedules` | Create a schedule for a scenario. |
| `PUT` | `/api/v1/scenarios/{id}/schedules/{schedule_id}` | Update a schedule. |
| `DELETE` | `/api/v1/scenarios/{id}/schedules/{schedule_id}` | Delete a schedule. |
| `GET` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences` | Occurrence and approval history for a schedule. |
| `POST` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/approve` | Approve a pending schedule occurrence. |
| `POST` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/reject` | Reject a pending schedule occurrence. |
| `GET` | `/api/v1/schedules/health` | Scheduling health, queue, approval, and conflict signals. |

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
| `POST` | `/api/v1/tests/estimate` | Estimate capacity for a proposed run. |
| `GET` | `/api/v1/tests/{id}` | Get run status and live summary. |
| `POST` | `/api/v1/tests/{id}/start` | Start a queued run. |
| `POST` | `/api/v1/tests/{id}/stop` | Gracefully stop a run. |
| `POST` | `/api/v1/tests/{id}/abort` | Abort immediately. |
| `POST` | `/api/v1/tests/{id}/pause` | Soft-pause between actions. |
| `POST` | `/api/v1/tests/{id}/resume` | Resume a paused run. |
| `PATCH` | `/api/v1/tests/{id}/vusers` | Change VUser count for a group. Send `action: "run"` to apply `target_vusers`, or `action: "pause"` / `"stop"` to drive that group to zero. Include optional `ramp_duration`, such as `"2m"`, to add or remove VUsers gradually. |
| `PATCH` | `/api/v1/tests/{id}/duration` | Extend or shorten duration. |
| `PATCH` | `/api/v1/tests/{id}/move` | Move a run into another folder. |
| `DELETE` | `/api/v1/tests/{id}` | Delete a run record. |
| `GET` | `/api/v1/tests/{id}/metrics` | Current metrics snapshot. |
| `GET` | `/api/v1/tests/{id}/sla-status` | SLA gate status for a run. |
| `POST` | `/api/v1/tests/{id}/comparison-baseline/pin` | Pin a run as its release comparison baseline. |
| `DELETE` | `/api/v1/tests/{id}/comparison-baseline/pin` | Remove the pinned comparison baseline. |

When a gradual VUser change is in progress, `GET /api/v1/tests/{id}` includes `vuser_ramps` keyed by group name. Each ramp reports `start_vusers`, `target_vusers`, `started_at`, `ramp_duration`, and `ramp_duration_seconds` so clients can project the active ramp until it finishes.

If a run has `script_shakeout: true`, `POST /api/v1/tests/{id}/start` first executes a one-pass canary/shakeout for each script group. A clean shakeout starts the full run automatically. A failing shakeout returns HTTP `409` with `code: "script_shakeout_failed"`, `status: "shakeout_review"`, a `script_shakeout` evidence report, and `override_available` / `cancel_available` flags. To continue anyway, repeat the start call with `{"override_script_shakeout_failure": true}`; to cancel, call `POST /api/v1/tests/{id}/abort`.

### 5. Stream Live Events

Use the global WebSocket for dashboard-style updates:

```text
ws://<controller-host>:8080/api/v1/ws
```

Messages are JSON and include a `type` field such as `metrics_update`, `test_created`, `test_started`, `test_complete`, `test_failed`, `test_paused`, `test_resumed`, `script_shakeout_started`, `script_shakeout_passed`, `script_shakeout_failed`, `vusers_ramp_started`, `vusers_changed`, `duration_changed`, and `latency_update`.

### 6. Download Reports And Archives

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/tests/{id}/report` | HTML report. |
| `GET` | `/api/v1/tests/{id}/archive` | Full run archive. |
| `GET` | `/api/v1/tests/{id}/archive/manifest` | Archive manifest. |
| `GET` | `/api/v1/tests/{id}/archive/report` | Structured run report. |
| `GET` | `/api/v1/tests/{id}/archive/files/{name}` | One named archive artifact. |
| `GET` | `/api/v1/tests/{id}/archive/browser-diagnostics` | Browser replay diagnostics artifact. |
| `GET` | `/api/v1/tests/{id}/archive/transactions` | Transaction data. |
| `GET` | `/api/v1/tests/{id}/archive/resources` | Resource metrics. |
| `GET` | `/api/v1/tests/{id}/archive/summaries` | Interval summaries. |
| `GET` | `/api/v1/tests/{id}/archive/analysis` | Merged action/dependency graph plus paged transaction drilldown. |
| `GET` | `/api/v1/tests/{id}/archive/root-cause` | Deterministic root-cause analysis. |
| `GET` | `/api/v1/tests/{id}/archive/ai-root-cause` | Governed AI root-cause assistance when policy allows it. |
| `GET` | `/api/v1/tests/{id}/archive/comparison` | Baseline comparison. |
| `POST` | `/api/v1/archives/retention/plan` | Preview archive-retention pruning. |
| `POST` | `/api/v1/archives/retention/apply` | Apply archive-retention pruning. |
| `GET` | `/api/v1/releases/{id}/trend/export` | Release trend CSV. |

Some reporting endpoints accept `format` or `profile` query parameters where implemented, for example `format=html|json|csv|jsonl|parquet` and `profile=executive|engineer|audit`.

Usage accounting from `GET /api/v1/system/usage` may include a `by_user` array when run attribution exists. Each bucket follows the same usage bucket shape used by project and protocol summaries; historical runs without attribution are grouped as `unattributed`.

### 7. Federate Library Sources

Library APIs move reusable scripts, scenarios, results, and archives between this controller and a hosted team library. The dashboard surfaces these controls in the **Library Sources** boxes on Scripts and Scenarios. Remote definitions are system-admin managed; asset pull/push/import operations are scoped by project and the kind being moved.

Useful library endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/library/capabilities` | Library feature and operation support advertised by this controller. |
| `GET` | `/api/v1/library/catalog?kind=script|scenario|result&project_id=<id>` | Local asset catalog. |
| `POST` | `/api/v1/library/export` | Export one local asset bundle for transfer. |
| `POST` | `/api/v1/library/import` | Import a JSON library bundle. |
| `POST` | `/api/v1/library/import/archive` | Import an uploaded archive bundle. |
| `GET` | `/api/v1/library/results/{id}/archive` | Download a result archive from the library surface. |
| `GET` / `POST` | `/api/v1/library/remotes` | List or create remote library hosts. |
| `PUT` / `DELETE` | `/api/v1/library/remotes/{id}` | Update or delete a remote host. |
| `POST` | `/api/v1/library/remotes/{id}/probe` | Probe remote health and credentials. |
| `GET` | `/api/v1/library/remotes/{id}/catalog?kind=script|scenario|result&project_id=<id>` | Catalog assets on a remote host. |
| `POST` | `/api/v1/library/remotes/{id}/pull` | Pull a remote asset into the local project. |
| `POST` | `/api/v1/library/remotes/{id}/push` | Push a local asset to the remote host. |
| `POST` | `/api/v1/library/remotes/{id}/diff` | Compare local and remote versions. |

### 8. Record, Replay, And Debug

Unified replay sessions use `/api/v1/replay/sessions/...` for draft and stored script replay. The older `/api/v1/script-debug/sessions/...` and `/api/v1/browser-replay/sessions/...` routes remain as compatibility/debug surfaces. The recorder routes manage proxy-captured HTTP traffic and conversion previews.

Useful recorder and replay endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/replay/sessions/{session_id}` | Read a replay/debug session. |
| `GET` | `/api/v1/replay/sessions/{session_id}/export?format=json|log` | Export replay evidence. |
| `POST` | `/api/v1/replay/sessions/{session_id}/pause` | Pause replay. |
| `POST` | `/api/v1/replay/sessions/{session_id}/resume` | Resume replay. |
| `POST` | `/api/v1/replay/sessions/{session_id}/step` | Step replay at a breakpoint. |
| `POST` | `/api/v1/replay/sessions/{session_id}/stop` | Stop replay. |
| `PATCH` | `/api/v1/replay/sessions/{session_id}/breakpoints` | Replace replay breakpoints. |
| `GET` | `/api/v1/browser-replay/sessions/{session_id}` | Read a browser replay session. |
| `POST` | `/api/v1/browser-replay/sessions/{session_id}/pause` | Pause browser replay. |
| `POST` | `/api/v1/browser-replay/sessions/{session_id}/resume` | Resume browser replay. |
| `POST` | `/api/v1/browser-replay/sessions/{session_id}/stop` | Stop browser replay. |
| `GET` | `/api/v1/recorder` | List recordings. |
| `POST` | `/api/v1/recorder/start` | Start a recording proxy session. |
| `GET` / `DELETE` | `/api/v1/recorder/{id}` | Read or delete one recording. |
| `POST` | `/api/v1/recorder/{id}/pause` | Pause a recording. |
| `POST` | `/api/v1/recorder/{id}/resume` | Resume a recording. |
| `POST` | `/api/v1/recorder/{id}/stop` | Stop a recording. |
| `PATCH` | `/api/v1/recorder/{id}/requests/{seq}` | Edit one recorded request. |
| `POST` | `/api/v1/recorder/{id}/convert/preview` | Preview conversion to script DSL. |
| `POST` | `/api/v1/recorder/{id}/convert` | Convert a recording to script DSL. |
| `GET` | `/api/v1/recorder/{id}/export` | Export recording evidence. |
| `GET` | `/api/v1/recorder/status` | Recorder proxy status. |
| `GET` | `/api/v1/recorder/ca.pem` | Recorder CA certificate. |

## Endpoint Catalogue

### Auth, Access, And Activity

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/auth/me` |
| `GET` | `/api/v1/auth/effective-access` |
| `GET` | `/api/v1/auth/access-matrix` |
| `GET` | `/api/v1/auth/roles` |
| `GET` | `/api/v1/auth/diagnostics` |
| `GET` / `POST` | `/api/v1/auth/users` |
| `GET` / `PATCH` | `/api/v1/auth/users/{id}` |
| `POST` | `/api/v1/auth/users/{id}/bindings` |
| `DELETE` | `/api/v1/auth/users/{id}/bindings/{binding_id}` |
| `POST` | `/api/v1/auth/users/{id}/disable` |
| `POST` | `/api/v1/auth/users/{id}/enable` |
| `GET` | `/api/v1/auth/user-activity` |
| `GET` | `/api/v1/auth/user-activity/export` |
| `GET` / `POST` | `/api/v1/auth/service-accounts` |
| `PATCH` / `DELETE` | `/api/v1/auth/service-accounts/{id}` |
| `POST` | `/api/v1/auth/service-accounts/{id}/rotate` |
| `GET` | `/api/v1/auth/audit-events` |
| `GET` | `/api/v1/auth/audit-events/export` |

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
| `GET` | `/api/v1/releases/{id}/trend` |
| `GET` | `/api/v1/releases/{id}/trend/export` |
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
| `POST` | `/api/v1/scripts/replay/sessions` |
| `POST` | `/api/v1/scripts/debug/sessions` |
| `POST` | `/api/v1/scripts/browser-replay/sessions` |
| `POST` | `/api/v1/scripts/browser-to-http/preview` |
| `GET` | `/api/v1/scripts/authoring/capabilities` |
| `GET` | `/api/v1/scripts/authoring/templates` |
| `GET` | `/api/v1/scripts/authoring/components` |
| `GET` / `POST` | `/api/v1/scripts/authoring/sessions` |
| `GET` / `PUT` / `DELETE` | `/api/v1/scripts/authoring/sessions/{id}` |
| `POST` | `/api/v1/scripts/authoring/sessions/{id}/approve` |
| `POST` | `/api/v1/scripts/authoring/sessions/{id}/correlations/preview` |
| `GET` | `/api/v1/correlations/rules` |
| `POST` | `/api/v1/correlations/diagnose` |
| `POST` | `/api/v1/scripts/{id}/validate` |
| `POST` | `/api/v1/scripts/{id}/replay` |
| `POST` | `/api/v1/scripts/{id}/replay/sessions` |
| `POST` | `/api/v1/scripts/{id}/debug/sessions` |
| `POST` | `/api/v1/scripts/{id}/browser-replay/sessions` |
| `POST` | `/api/v1/scripts/{id}/export` |
| `GET` | `/api/v1/scripts/{id}/download` |
| `GET` | `/api/v1/scripts/{id}/runtime-settings/report` |
| `GET` | `/api/v1/script-debug/sessions/{session_id}` |
| `POST` | `/api/v1/script-debug/sessions/{session_id}/pause` |
| `POST` | `/api/v1/script-debug/sessions/{session_id}/resume` |
| `POST` | `/api/v1/script-debug/sessions/{session_id}/step` |
| `POST` | `/api/v1/script-debug/sessions/{session_id}/stop` |
| `PATCH` | `/api/v1/script-debug/sessions/{session_id}/breakpoints` |
| `GET` | `/api/v1/script-debug/sessions/{session_id}/export?format=json|log` |
| `GET` | `/api/v1/replay/sessions/{session_id}` |
| `POST` | `/api/v1/replay/sessions/{session_id}/pause` |
| `POST` | `/api/v1/replay/sessions/{session_id}/resume` |
| `POST` | `/api/v1/replay/sessions/{session_id}/step` |
| `POST` | `/api/v1/replay/sessions/{session_id}/stop` |
| `PATCH` | `/api/v1/replay/sessions/{session_id}/breakpoints` |
| `GET` | `/api/v1/replay/sessions/{session_id}/export?format=json|log` |

### Script Imports And Protocol Matrices

| Method | Path |
| --- | --- |
| `POST` | `/api/v1/scripts/import/loadrunner` |
| `POST` | `/api/v1/scripts/import/loadrunner/validate` |
| `GET` | `/api/v1/scripts/import/loadrunner/support-matrix` |
| `POST` | `/api/v1/scripts/import/jmeter` |
| `POST` | `/api/v1/scripts/import/jmeter/save` |
| `POST` | `/api/v1/scripts/import/jmeter/validate` |
| `GET` | `/api/v1/scripts/import/jmeter/support-matrix` |
| `POST` | `/api/v1/scripts/import/k6` |
| `POST` | `/api/v1/scripts/export/k6` |
| `GET` | `/api/v1/scripts/import/k6/support-matrix` |
| `POST` | `/api/v1/scripts/import/sample` |
| `POST` | `/api/v1/scripts/import/devweb` |
| `GET` | `/api/v1/scripts/import/devweb/support-matrix` |
| `GET` | `/api/v1/scripts/import/oracle-ebusiness/support-matrix` |
| `POST` | `/api/v1/scripts/import/oracle-ebusiness/review` |
| `POST` | `/api/v1/scripts/import/har` |
| `POST` | `/api/v1/scripts/import/openapi` |
| `POST` | `/api/v1/scripts/import/postman` |
| `POST` | `/api/v1/scripts/import/wsdl` |
| `GET` | `/api/v1/scripts/web-api/support-matrix` |

### Replay, Browser Replay, And Recorder

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/browser-replay/sessions/{session_id}` |
| `POST` | `/api/v1/browser-replay/sessions/{session_id}/pause` |
| `POST` | `/api/v1/browser-replay/sessions/{session_id}/resume` |
| `POST` | `/api/v1/browser-replay/sessions/{session_id}/stop` |
| `GET` | `/api/v1/recorder` |
| `POST` | `/api/v1/recorder/start` |
| `GET` / `DELETE` | `/api/v1/recorder/{id}` |
| `POST` | `/api/v1/recorder/{id}/pause` |
| `POST` | `/api/v1/recorder/{id}/resume` |
| `POST` | `/api/v1/recorder/{id}/stop` |
| `PATCH` | `/api/v1/recorder/{id}/requests/{seq}` |
| `POST` | `/api/v1/recorder/{id}/convert/preview` |
| `POST` | `/api/v1/recorder/{id}/convert` |
| `GET` | `/api/v1/recorder/{id}/export` |
| `GET` | `/api/v1/recorder/status` |
| `GET` | `/api/v1/recorder/ca.pem` |

### Data Pools

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/data-pools` |
| `GET` / `PUT` / `DELETE` | `/api/v1/data-pools/{id}` |
| `GET` / `POST` | `/api/v1/data-pools/{id}/rows` |
| `PUT` / `DELETE` | `/api/v1/data-pools/{id}/rows/{row_id}` |

### Scenarios And Load Models

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/scenarios` |
| `POST` | `/api/v1/scenarios/validate-load-model` |
| `GET` / `PUT` / `DELETE` | `/api/v1/scenarios/{id}` |
| `GET` | `/api/v1/scenarios/load-models/support-matrix` |
| `GET` | `/api/v1/scenarios/load-models/templates` |
| `GET` | `/api/v1/scenarios/{id}/results` |
| `GET` | `/api/v1/scenarios/{id}/runtime-settings/report` |
| `GET` / `PUT` | `/api/v1/scenarios/{id}/data-pools` |

### Scheduling

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/scenarios/{id}/schedules` |
| `PUT` / `DELETE` | `/api/v1/scenarios/{id}/schedules/{schedule_id}` |
| `GET` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/occurrences` |
| `POST` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/approve` |
| `POST` | `/api/v1/scenarios/{id}/schedules/{schedule_id}/approvals/{approval_id}/reject` |
| `GET` | `/api/v1/schedules/health` |

### Tests, Runs, And Archives

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/tests` |
| `POST` | `/api/v1/tests/estimate` |
| `GET` / `DELETE` | `/api/v1/tests/{id}` |
| `GET` | `/api/v1/tests/{id}/sla-status` |
| `GET` | `/api/v1/tests/{id}/report` |
| `POST` | `/api/v1/tests/{id}/start` |
| `POST` | `/api/v1/tests/{id}/stop` |
| `POST` | `/api/v1/tests/{id}/abort` |
| `POST` | `/api/v1/tests/{id}/pause` |
| `POST` | `/api/v1/tests/{id}/resume` |
| `PATCH` | `/api/v1/tests/{id}/vusers` |
| `PATCH` | `/api/v1/tests/{id}/duration` |
| `PATCH` | `/api/v1/tests/{id}/move` |
| `POST` | `/api/v1/tests/{id}/comparison-baseline/pin` |
| `DELETE` | `/api/v1/tests/{id}/comparison-baseline/pin` |
| `GET` | `/api/v1/tests/{id}/metrics` |
| `GET` | `/api/v1/tests/{id}/archive` |
| `GET` | `/api/v1/tests/{id}/archive/manifest` |
| `GET` | `/api/v1/tests/{id}/archive/files/{name}` |
| `GET` | `/api/v1/tests/{id}/archive/browser-diagnostics` |
| `GET` | `/api/v1/tests/{id}/archive/transactions` |
| `GET` | `/api/v1/tests/{id}/archive/resources` |
| `GET` | `/api/v1/tests/{id}/archive/summaries` |
| `GET` | `/api/v1/tests/{id}/archive/report` |
| `GET` | `/api/v1/tests/{id}/archive/analysis` |
| `GET` | `/api/v1/tests/{id}/archive/root-cause` |
| `GET` | `/api/v1/tests/{id}/archive/ai-root-cause` |
| `GET` | `/api/v1/tests/{id}/archive/comparison` |
| `GET` | `/api/v1/tests/{id}/stream` |

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
| `GET` | `/api/v1/desktop/status` |
| `GET` | `/api/v1/runtime-settings/support-matrix` |
| `GET` | `/api/v1/protocol-bundles/support-matrix` |
| `GET` | `/api/v1/protocols/native-support-matrix` |
| `GET` | `/api/v1/integrations/apm/profiles` |
| `GET` | `/api/v1/monitoring/packs` |
| `GET` | `/api/v1/reports/profiles` |
| `GET` | `/api/v1/fleet/alerts` |
| `GET` | `/api/v1/fleet/capacity` |
| `GET` | `/api/v1/fleet/provisioner` |
| `PATCH` | `/api/v1/system/generators/drain` |
| `GET` / `PUT` | `/api/v1/system/settings` |
| `GET` | `/api/v1/ws` |
| `GET` | `/metrics` |

### Library Federation

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/library/capabilities` |
| `GET` | `/api/v1/library/catalog` |
| `POST` | `/api/v1/library/export` |
| `POST` | `/api/v1/library/import` |
| `POST` | `/api/v1/library/import/archive` |
| `GET` | `/api/v1/library/results/{id}/archive` |
| `GET` / `POST` | `/api/v1/library/remotes` |
| `PUT` / `DELETE` | `/api/v1/library/remotes/{id}` |
| `POST` | `/api/v1/library/remotes/{id}/probe` |
| `GET` | `/api/v1/library/remotes/{id}/catalog` |
| `POST` | `/api/v1/library/remotes/{id}/pull` |
| `POST` | `/api/v1/library/remotes/{id}/push` |
| `POST` | `/api/v1/library/remotes/{id}/diff` |

### AI Assistance And Integrations

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/ai/authoring/contract` |
| `GET` | `/api/v1/ai/policy` |
| `GET` | `/api/v1/ai/review-queue` |
| `POST` | `/api/v1/ai/drafts` |
| `POST` | `/api/v1/ai/repairs` |
| `POST` | `/api/v1/ai/explain` |
| `POST` | `/api/v1/results/import` |
| `GET` | `/api/v1/results/imports` |
| `POST` | `/api/v1/archives/retention/plan` |
| `POST` | `/api/v1/archives/retention/apply` |

## Operational Tips

- Prefer service accounts for CI/CD and external automations.
- Onboard local users before enabling production OIDC or trusted-header access for a team.
- Use `GET /api/v1/auth/effective-access` before enabling UI or pipeline actions for a principal.
- Use user activity filters when investigating changes across timeframe, domain, project, action, or status.
- Validate scripts and load models before creating or running tests.
- Scope list calls with `project_id`, `application_id`, and `release_id` to keep automation deterministic.
- Archive run evidence after release gates and export audit events for governance changes.
- Use `GET /api/v1/system/parity-status` and support matrix endpoints when building external readiness checks.
