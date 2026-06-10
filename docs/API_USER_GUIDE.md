# APEX Load API User Guide

This guide is for engineers and operators who want to automate APEX Load from CI/CD, external release systems, or integration scripts. It focuses on the public REST and WebSocket API served by the controller.

Last reviewed against `pkg/api/server.go` on June 8, 2026 (updated for multi-comparison, goal-oriented controls, and live error evidence).

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

Dashboard operators with access management permission can onboard users, assign scoped roles, review user activity, and manage scoped API keys from **Access**. `manage_users` is the system-admin-only lifecycle operation for creating users, editing user metadata, and enabling or disabling access. `manage_user_bindings` allows delegated admins to view the local user directory and manage role bindings inside their permitted scope. APEX Load returns created or rotated service-account keys once and then retains only the hash.

Useful auth endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/auth/login` | Initiate OIDC login flow; redirects to the configured identity provider. |
| `GET` | `/api/v1/auth/callback` | OIDC callback; exchanges the authorization code for a signed session. |
| `POST` | `/api/v1/auth/logout` | Invalidate the current OIDC session and redirect to the identity-provider logout. |
| `GET` | `/api/v1/auth/me` | Current auth mode, local user status, locally resolved roles, and session metadata. |
| `GET` | `/api/v1/auth/effective-access?project_id=<id>` | Operations allowed for the current principal from local user bindings or service-account roles. |
| `GET` | `/api/v1/auth/access-matrix` | Full role-to-operation catalog. |
| `GET` | `/api/v1/auth/diagnostics` | OIDC/trusted-header readiness diagnostics. |
| `GET` | `/api/v1/auth/users` | Search local human users. Requires `manage_users` or `manage_user_bindings`; binding-only callers see manageable bindings only. |
| `POST` | `/api/v1/auth/users` | Onboard a human user subject. Requires `manage_users`. |
| `GET` | `/api/v1/auth/users/{id}` | Get one local user and role bindings. Requires `manage_users` or `manage_user_bindings`; binding-only callers see manageable bindings only. |
| `PATCH` | `/api/v1/auth/users/{id}` | Update local user metadata. Requires `manage_users`. |
| `POST` | `/api/v1/auth/users/{id}/bindings` | Grant a global, domain, project, or repository role binding. Requires `manage_users` or `manage_user_bindings`, with delegated scope checks. |
| `DELETE` | `/api/v1/auth/users/{id}/bindings/{binding_id}` | Remove one local role binding. Requires `manage_users` or `manage_user_bindings`, with delegated scope checks. |
| `POST` | `/api/v1/auth/users/{id}/disable` | Disable a local user while preserving audit history. Requires `manage_users`. |
| `POST` | `/api/v1/auth/users/{id}/enable` | Re-enable a local user. Requires `manage_users`. |
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

Supported roles remain `viewer`, `editor`, `project_admin`, and `system_admin`. Delegated administration is scoped: system admins can manage all users and bindings; global project admins can manage scoped bindings but cannot grant global roles; domain project admins can manage domain and child-project bindings inside their domain; project admins can manage bindings for their project only; repository project admins can manage bindings for their repository only. Delegated admins receive `manage_user_bindings`, not `manage_users`, so they cannot create users, edit user metadata, disable users, enable users, grant `system_admin`, or create bindings broader than their own scope.

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
| `POST` | `/api/v1/scripts/import/enterprise-script-archive` | Import an enterprise script archive ZIP with protocol auto-detection. |
| `POST` | `/api/v1/scripts/import/enterprise-script-archive/validate` | Validate an enterprise script archive without saving. |
| `GET` | `/api/v1/scripts/import/enterprise-script-archive/support-matrix` | Enterprise script archive protocol import support. |
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

### 3. Sync Scripts With GitHub Enterprise And CI

GitHub Enterprise integration is project-policy enforced. Automation resolves credentials in this order, unless the system automation mode narrows it: project-policy GitHub App installation, per-user OAuth, then configured PAT fallback. API responses may include `auth_mode`, `remote_blob_sha`, `remote_commit_sha`, `policy`, `rate_limit`, and `check_run` fields.

Project policy controls allowed repositories, branches, path templates, direct-commit versus PR behavior, import/push permissions, bulk sync defaults, webhook auto-sync, and check-run publishing. Mutating routes reject requests outside that policy even when the connected GitHub identity can access more.

Useful GitHub endpoints:

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/integrations/github/config` | Safe GitHub configuration and capability flags for dashboard and clients. |
| `GET` | `/api/v1/integrations/github/diagnostics` | GHES reachability, auth-mode, installation, webhook, PAT fallback, and Checks readiness. |
| `GET` | `/api/v1/integrations/github/installations` | GitHub App installations known to automation. |
| `GET` | `/api/v1/integrations/github/login` | Begin a per-user OAuth connect flow. |
| `GET` | `/api/v1/integrations/github/callback` | OAuth callback route. |
| `GET` / `DELETE` | `/api/v1/integrations/github/connection` | Read or remove the current user's OAuth connection. |
| `GET` | `/api/v1/integrations/github/repos` | List repositories visible to the resolved GitHub identity. |
| `GET` | `/api/v1/integrations/github/branches` | List repository branches. |
| `GET` | `/api/v1/integrations/github/contents` | Browse repository contents for import review. |
| `GET` / `PUT` | `/api/v1/projects/{id}/github/policy` | Read or update the authoritative project GitHub policy. |
| `POST` | `/api/v1/scripts/import/github` | Import one GitHub JSON DSL file through the review/save path. |
| `POST` | `/api/v1/scripts/{id}/github/push` | Push a script by direct commit or pull request. |
| `GET` | `/api/v1/scripts/{id}/github/status` | Script link, drift, blob SHA, commit SHA, policy, rate-limit, auth-mode, and check status. |
| `POST` / `GET` | `/api/v1/integrations/github/sync-jobs` | Create or list bulk import/push sync jobs. |
| `GET` | `/api/v1/integrations/github/sync-jobs/{id}` | Read sync-job progress, conflict decisions, and errors. |
| `POST` | `/api/v1/integrations/github/webhook` | GitHub webhook receiver; validates `X-Hub-Signature-256` and dedupes `X-GitHub-Delivery`. |
| `GET` | `/api/v1/integrations/github/webhooks` | Webhook delivery log. |
| `POST` | `/api/v1/tests/{id}/github/check-run` | Publish or retry an APEX Load run result as a GitHub Check Run. |
| `GET` | `/api/v1/integrations/github/check-links` | GitHub Check Run links associated with APEX Load runs. |

Example project policy update:

```bash
curl -X PUT http://localhost:8080/api/v1/projects/project-1/github/policy \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "enabled": true,
    "allowed_repos": ["my-org/perf-scripts"],
    "allowed_branches": ["main", "release/*"],
    "path_template": "apexload/scripts/{project}/{ref_key}.json",
    "require_pr": true,
    "allow_import": true,
    "allow_push": true,
    "bulk_sync_enabled": true,
    "webhook_auto_sync": true,
    "checks_enabled": true,
    "app_installation_id": 12345678
  }'
```

Create a bulk import job:

```bash
curl -X POST http://localhost:8080/api/v1/integrations/github/sync-jobs \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "kind": "bulk_import",
    "project_id": "project-1",
    "owner": "my-org",
    "repo": "perf-scripts",
    "branch": "main",
    "path": "apexload/scripts"
  }'
```

Publish a check run for an APEX Load result:

```bash
curl -X POST http://localhost:8080/api/v1/tests/test-123/github/check-run \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d '{
    "owner": "my-org",
    "repo": "perf-scripts",
    "script_id": "script-456",
    "commit_sha": "0123456789abcdef0123456789abcdef01234567",
    "conclusion": "success",
    "details_url": "https://apexload.example.com/#/test/test-123",
    "summary": "APEX Load SLA gates passed."
  }'
```

### 4. Assemble And Validate A Scenario

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
| `POST` | `/api/v1/scenarios/size-from-goal` | Reverse-calculate recommended VUser counts from a target throughput or latency goal. |
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
| `GET` | `/api/v1/run-calendar` | Calendar view of scheduled upcoming and recent completed runs, including scenario labels and release context. |

### 5. Run And Control Tests

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
| `POST` | `/api/v1/tests/preflight` | Pre-flight capacity and governance policy check for a proposed run without creating it. |
| `GET` | `/api/v1/tests/{id}` | Get run status and live summary. |
| `GET` | `/api/v1/tests/{id}/manifest` | Generator assignment manifest for a run (which generators host which groups). |
| `POST` | `/api/v1/tests/{id}/start` | Start a queued run. |
| `POST` | `/api/v1/tests/{id}/stop` | Gracefully stop a run. |
| `POST` | `/api/v1/tests/{id}/abort` | Abort immediately. |
| `POST` | `/api/v1/tests/{id}/pause` | Soft-pause between actions. |
| `POST` | `/api/v1/tests/{id}/resume` | Resume a paused run. |
| `PATCH` | `/api/v1/tests/{id}/vusers` | Change VUser count for a group. Send `action: "run"` to apply `target_vusers`, or `action: "pause"` / `"stop"` to drive that group to zero. Include optional `ramp_duration`, such as `"2m"`, to add or remove VUsers gradually. |
| `PATCH` | `/api/v1/tests/{id}/goal-targets` | Adjust per-group TPS targets for a `goal_oriented` run. Request: `{"group":"<name>","target_tps":<float>}`. Response includes updated `goal_targets` map and the new aggregate `target_average_tps`. Broadcasts `goal_target_changed` on the WebSocket. Returns `409` if the test is not `goal_oriented`. |
| `PATCH` | `/api/v1/tests/{id}/duration` | Extend or shorten duration. |
| `PATCH` | `/api/v1/tests/{id}/move` | Move a run into another folder. |
| `DELETE` | `/api/v1/tests/{id}` | Delete a run record. |
| `GET` | `/api/v1/tests/{id}/metrics` | Current metrics snapshot. |
| `GET` | `/api/v1/tests/{id}/errors/evidence` | Live error evidence for a running test. Query params: `signature` (required — error pattern key), `action` (optional filter), `limit` (1–100, default 20). Response: `{test_id, signature, evidence:[]}` where each item carries `id`, `signature`, `action`, `group`, `timestamp`, `duration_ms`, `status`, `status_code`, `error_message`, and optional `snapshot`. |
| `GET` | `/api/v1/tests/{id}/sla-status` | SLA gate status for a run. |
| `POST` | `/api/v1/tests/{id}/comparison-baseline/pin` | Pin a run as its release comparison baseline. |
| `DELETE` | `/api/v1/tests/{id}/comparison-baseline/pin` | Remove the pinned comparison baseline. |

For `goal_oriented` runs, `GET /api/v1/tests/{id}` and `GET /api/v1/tests/{id}/metrics` include a `goal_targets` map keyed by group name. Each entry carries `target_tps` and `updated_at` so clients can display the current per-group throughput target alongside actual metrics.

When a gradual VUser change is in progress, `GET /api/v1/tests/{id}` includes `vuser_ramps` keyed by group name. Each ramp reports `start_vusers`, `target_vusers`, `started_at`, `ramp_duration`, and `ramp_duration_seconds` so clients can project the active ramp until it finishes.

If a run has `script_shakeout: true`, `POST /api/v1/tests/{id}/start` first executes a one-pass canary/shakeout for each script group. A clean shakeout starts the full run automatically. A failing shakeout returns HTTP `409` with `code: "script_shakeout_failed"`, `status: "shakeout_review"`, a `script_shakeout` evidence report, and `override_available` / `cancel_available` flags. To continue anyway, repeat the start call with `{"override_script_shakeout_failure": true}`; to cancel, call `POST /api/v1/tests/{id}/abort`.

### 6. Stream Live Events

Use the global WebSocket for dashboard-style updates:

```text
ws://<controller-host>:8080/api/v1/ws
```

Messages are JSON and include a `type` field such as `metrics_update`, `test_created`, `test_started`, `test_complete`, `test_failed`, `test_paused`, `test_resumed`, `script_shakeout_started`, `script_shakeout_passed`, `script_shakeout_failed`, `vusers_ramp_started`, `vusers_changed`, `duration_changed`, `latency_update`, and `goal_target_changed`.

`goal_target_changed` fires whenever `PATCH /api/v1/tests/{id}/goal-targets` succeeds and carries `test_id`, `group`, `target_tps`, `target_average_tps`, and `goal_targets` (a map of group name → `{target_tps, updated_at}`).

### 7. Download Reports And Archives

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
| `GET` | `/api/v1/tests/{id}/archive/comparison` | Comparison export. **Single-run** (existing): add `baseline_id=<id>` to compare the addressed run against one baseline. **Multi-run** (new): add `comparison_ids=<id1>,<id2>,...` (up to 9 comparison IDs; addressed run is the baseline; max 10 runs total) plus `breakdown=overall\|platform\|engine\|device`. Multi-run only supports `format=csv`; result is ordered oldest→newest with a `Content-Disposition` attachment header. |
| `POST` | `/api/v1/archives/retention/plan` | Preview archive-retention pruning. |
| `POST` | `/api/v1/archives/retention/apply` | Apply archive-retention pruning. |
| `GET` | `/api/v1/releases/{id}/trend/export` | Release trend CSV. |

Some reporting endpoints accept `format` or `profile` query parameters where implemented, for example `format=html|json|csv|pdf|jsonl|parquet` and `profile=executive|engineer|audit`.

Usage accounting from `GET /api/v1/system/usage` may include a `by_user` array when run attribution exists. Each bucket follows the same usage bucket shape used by project and protocol summaries; historical runs without attribution are grouped as `unattributed`.

### 8. Federate Library Sources

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

### 9. Record, Replay, And Debug

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
| `POST` | `/api/v1/scripts/import/enterprise-script-archive` |
| `POST` | `/api/v1/scripts/import/enterprise-script-archive/validate` |
| `GET` | `/api/v1/scripts/import/enterprise-script-archive/support-matrix` |
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

### GitHub Enterprise Script Sync And CI

| Method | Path |
| --- | --- |
| `GET` | `/api/v1/integrations/github/config` |
| `GET` | `/api/v1/integrations/github/diagnostics` |
| `GET` | `/api/v1/integrations/github/installations` |
| `GET` | `/api/v1/integrations/github/login` |
| `GET` | `/api/v1/integrations/github/callback` |
| `GET` / `DELETE` | `/api/v1/integrations/github/connection` |
| `GET` | `/api/v1/integrations/github/repos` |
| `GET` | `/api/v1/integrations/github/branches` |
| `GET` | `/api/v1/integrations/github/contents` |
| `GET` / `PUT` | `/api/v1/projects/{id}/github/policy` |
| `POST` | `/api/v1/scripts/import/github` |
| `POST` | `/api/v1/scripts/{id}/github/push` |
| `GET` | `/api/v1/scripts/{id}/github/status` |
| `POST` / `GET` | `/api/v1/integrations/github/sync-jobs` |
| `GET` | `/api/v1/integrations/github/sync-jobs/{id}` |
| `POST` | `/api/v1/integrations/github/webhook` |
| `GET` | `/api/v1/integrations/github/webhooks` |
| `POST` | `/api/v1/tests/{id}/github/check-run` |
| `GET` | `/api/v1/integrations/github/check-links` |

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
| `POST` | `/api/v1/scenarios/size-from-goal` |
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
| `GET` | `/api/v1/run-calendar` |

### Tests, Runs, And Archives

| Method | Path |
| --- | --- |
| `GET` / `POST` | `/api/v1/tests` |
| `POST` | `/api/v1/tests/estimate` |
| `POST` | `/api/v1/tests/preflight` |
| `GET` / `DELETE` | `/api/v1/tests/{id}` |
| `GET` | `/api/v1/tests/{id}/manifest` |
| `GET` | `/api/v1/tests/{id}/sla-status` |
| `GET` | `/api/v1/tests/{id}/report` |
| `POST` | `/api/v1/tests/{id}/start` |
| `POST` | `/api/v1/tests/{id}/stop` |
| `POST` | `/api/v1/tests/{id}/abort` |
| `POST` | `/api/v1/tests/{id}/pause` |
| `POST` | `/api/v1/tests/{id}/resume` |
| `PATCH` | `/api/v1/tests/{id}/vusers` |
| `PATCH` | `/api/v1/tests/{id}/goal-targets` |
| `PATCH` | `/api/v1/tests/{id}/duration` |
| `PATCH` | `/api/v1/tests/{id}/move` |
| `POST` | `/api/v1/tests/{id}/comparison-baseline/pin` |
| `DELETE` | `/api/v1/tests/{id}/comparison-baseline/pin` |
| `GET` | `/api/v1/tests/{id}/metrics` |
| `GET` | `/api/v1/tests/{id}/errors/evidence` |
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
| `GET` | `/api/v1/system/version` |
| `GET` | `/api/v1/system/inventory` |
| `GET` | `/api/v1/system/services` |
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
| `GET` | `/api/v1/fleet/load-zones` |
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
- For GitHub automation, prefer GitHub App installation tokens; reserve PAT fallback for explicitly headless environments.
- Configure project GitHub policy before enabling bulk sync, webhook auto-sync, or check-run publishing.
- Archive run evidence after release gates and export audit events for governance changes.
- Use `GET /api/v1/system/parity-status` and support matrix endpoints when building external readiness checks.
