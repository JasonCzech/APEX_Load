# APEX Load API User Guide

This guide is for engineers and operators who want to automate APEX Load from CI/CD, external release systems, or integration scripts. It focuses on workflow guidance for the public REST and WebSocket API served by the controller.

For the exhaustive, current route inventory, use the dashboard [API Reference](#/swagger). Those views are generated from the controller route catalog; this repository guide is hand-maintained prose for common automation flows and operational context.

Last reviewed against `pkg/api/server.go`, `pkg/api/route_catalog.go`, and the generated endpoint catalog for the September 12, 2026 documentation update. Behavioral corrections also reference the HTTP protocol and execution-plan orchestrator.

### Reference Coverage

The generated OpenAPI document is authoritative for registered HTTP methods, paths, path/query/header parameter names and basic types, deprecation flags, authentication alternatives, route-level access requirements, request media/top-level serializable fields, and handler-declared response statuses/media. Semantic validation rules, conditional requirements, and some dynamic representations remain defined by handlers and workflow-specific guides. API Reference filters are navigation aids, not authorization sources. The controller's runtime RBAC and service-account scope checks always decide access.

Use the references together:

- **Swagger**: every registered REST route plus inferred query/header, request-body, response, media, and access contracts; explicit maintained schemas override inference.
- **API Reference filters**: category, search, and optional filtering by the current principal's effective operations.
- **API Workflows**: request examples, behavioral contracts, limitations, and operational workflows.
- **DSL references**: [DSL action reference](DSL_ACTION_REFERENCE.md) and [JSON Schema](dsl.schema.json) for script payloads.

## Base URL

The controller serves API endpoints under:

```text
http://<controller-host>:8080/api/v1
```

Local dashboard development normally proxies `/api` from `http://localhost:4000` to the controller. Direct API clients should use the controller origin.

Health checks:

```bash
curl http://localhost:8080/api/v1/system/health
curl http://localhost:8080/api/v1/system/readiness
```

## Controller Startup Compatibility

Some enterprise TLS intermediaries still present certificate chains that require compatibility behavior in newer Go runtimes.

When needed, enable controller TLS compatibility defaults explicitly:

```bash
APEXLOAD_ENABLE_TLS_COMPAT=true go run ./cmd/controller
```

This appends missing `GODEBUG` tokens:

- `x509negativeserial=1`
- `tlsmlkem=0`
- `tlssecpmlkem=0`

To force-disable automatic TLS compatibility defaults in any environment:

```bash
APEXLOAD_DISABLE_TLS_COMPAT=true go run ./cmd/controller
```

If `APEXLOAD_ENV=production|prod` or `APEXLOAD_REQUIRE_AUTH` enables production posture, the persisted `auth.mode` setting must not be `dev`, `disabled`, or `local`; the controller rejects that combination at startup. Use `oidc` or `trusted_headers` in production environments. (There is no `APEXLOAD_AUTH_MODE` environment variable — the mode is a stored system setting.)

## Authentication

APEX Load supports four auth modes:

- `dev`: local development bypass. Do not use for shared environments.
- `local`: non-production Local Test Login with database-backed personas and sessions lasting up to 12 hours within the current controller process.
- `trusted_headers`: an upstream gateway authenticates the user and forwards trusted identity headers.
- `oidc`: APEX Load handles OIDC login, callback, signed session, and logout.

For human users, OIDC, trusted headers, or Local Test Login prove identity, but APEX Load access is controlled by local user records and local role bindings. After bootstrap, an upstream-authenticated human principal must have an active local user whose `subject` matches the authenticated subject. Human effective roles come from `UserRoleBinding` rows, not from upstream role headers. Service accounts keep their existing key-based behavior.

Local Test Login is selected in [Access → Authentication](#/access?setting=authentication) and takes effect after restart. It seeds `admin/admin` (system administrator), `domainadmin/domainadmin` (Default Domain administrator), `user/user` (read-only default workspace), and `dev/dev` (default workspace editor). Changes to fixture active state and role bindings in Access are preserved. Passwords are stored only as bcrypt hashes, and browser session tokens are stored only as SHA-256 hashes. Every controller startup while Local Test Login is active invalidates existing local sessions, so a restarted application opens at the persona login instead of silently restoring the previous persona. Logging out any persona, including `dev`, clears the current session and returns to the same login. These fixed credentials are deliberately rejected under production posture.

Service-account automation can authenticate with either header:

```bash
Authorization: Bearer <api-key>
X-APEXLoad-API-Key: <api-key>
```

Service-account requests must satisfy both RBAC role checks and token scopes. For example, a service account with `system_admin` still needs a compatible scope such as `settings:manage` before it can update system settings.

The OpenAPI security alternatives also describe the signed `apexload_auth_session` cookie used by hosted OIDC sessions. Trusted identity headers are deployment-specific and therefore remain documented in the auth runbook instead of being modeled as a portable client-side OpenAPI credential. Mutating OIDC-session requests may additionally require the CSRF token surfaced by `/api/v1/auth/me`.

The MCP server under `mcp/` also uses service-account authentication by default. Configure `APEXLOAD_API_KEY` or `APEXLOAD_BEARER_TOKEN` for AI-agent and CI/CD automation rather than relying on local dev preview headers.

Dashboard operators with access management permission can onboard users, assign scoped roles, review user activity, and manage scoped API keys from **Access**. `manage_users` is the system-admin-only lifecycle operation for creating users, editing user metadata, and enabling or disabling access. `manage_user_bindings` allows delegated admins to view the local user directory and manage role bindings inside their permitted scope. APEX Load returns created or rotated service-account keys once and then retains only the hash.

Useful auth endpoints:

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

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
- Generated Swagger entries use exact handler-declared statuses where they can be derived and broad response classes only as a fallback for dynamic paths.
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

These headers are honored only when the controller auth mode is `dev`. They are ignored in `local`, `trusted_headers`, and `oidc` modes.
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

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

#### Shared HTTP import finalization

Postman, JMeter HTTP samplers, OpenAPI, WSDL-generated SOAP requests, the constrained k6 HTTP subset, and sample/HAR request families now converge on a shared effective-request, credential, review, and DSL-finalization pipeline. Source-specific parsing and inheritance remain in each importer. Ordered headers, runtime capability bounds, structured body projection, credential requirements, recursive binding readiness, durable gates, and final validation use the shared contract.

The four conversion dispositions are:

| Disposition | API behavior |
| --- | --- |
| `executable_exact` | Supported wire behavior is preserved. Runtime preflight still verifies deployed values for valid secret/file/data references. |
| `executable_bounded` | A native runtime ceiling was applied with a stable diagnostic; replay remains safe. |
| `blocked_draft` | The valid sanitized draft may be reviewed and saved, but replay, debug, scenario materialization, and launch fail before transport until the affected behavior or binding is repaired and its durable gate is removed. |
| `rejected` | No valid disclosure-safe draft exists, so conversion fails without returning a runnable script. |

These names are shared review semantics, not new required fields on every legacy response. Existing routes, request fields, response compatibility fields, merge behavior, and status-code contracts remain in place; older envelopes may surface the result through `preflight`, diagnostics, review sections, and action metadata. Finding acknowledgement can permit commit where that workflow already supports acknowledgement, but cannot make an execution-blocked draft runnable.

Credential-bearing URL components, ordered headers, typed auth, JSON, XML, forms, multipart, and GraphQL are projected without embedding source literals in scripts or public review data. When an inspection response supplies optional `import_session_id` and `expires_at`, clients should commit through that protected server-side candidate; legacy script-posting remains supported and still passes through finalization and stored-gate enforcement. Missing configured project-secret values are checked dynamically at execution rather than written as permanent import findings.

Archive-backed WSDL, DevWeb, and Enterprise Script Archive imports accept regular files only and enforce bounded entry count, per-entry and total expanded size, path depth, and compression ratio. Traversal, absolute/drive paths, links/devices, invalid UTF-8 or control characters, names with leading or trailing whitespace, case/NFC/separator collisions, size mismatches, excessive compression ratios, and decompression bombs are rejected before source-root selection. See the [Web/API Execution Support Matrix](WEB_API_EXECUTION_SUPPORT_MATRIX.md#shared-request-import-pipeline) for the per-importer tested disposition matrix.

Enterprise Script Archive requests are multipart form uploads. Common fields are `file` (`.zip`), `name`, `base_url`, `include_assertions`, `preserve_transactions`, `migration_block_statuses`, and `salesforce_native`. The deprecated `allow_java_vuser_runner` field is still accepted for compatibility but has no effect. Every LoadRunner Java archive produces one combined `java_vuser` draft: `script` and the sole item in `scripts` describe the same draft, and HTTP, JDBC, JMS, SOAP, LoadRunner utility calls, and custom Java are not split into native lanes. Java responses use `import_mode: "external_runner_required"` with `runner_requirement: "java_vuser_jvm_runner"`; launch requires an enabled runner advertising both `java_vuser_persistent_lifecycle_v1` and `java_vuser_session_frame_token_v1`. A qualified local or loopback-sidecar runner that advertises both capabilities keeps one JVM and one `Actions` instance alive per VUser across `init`, repeated `action` iterations, and `end`, preserving Java fields and saved LoadRunner parameters for that lifecycle. Windows Job Object containment or a narrowly delegated Linux cgroup can qualify; the stock non-privileged Helm, Kustomize, and Compose sidecars remain legacy `full_lifecycle` only.

A complete Java external-runner draft is review-required, not fidelity-loss blocked. Save it as one authoring session and approve it through the normal authoring workflow. Approval may place the draft in the script library, but test launch remains blocked until a governed Java runner advertises the required capability and capacity. The importer does not compile or distribute Java source; operators provision the compiled `Actions` class and its reviewed third-party dependencies on the runner classpath.

The import response uses the shared import review envelope: `script`/`scripts`, `warnings`, `stats`, `review_sections`, `repair_suggestions`, `source_review`, `fidelity_report`, `detected_protocol`, `protocol_compat_report`, `review_artifact`, `trueclient2_compat_report`, and `parameterization`. `detected_protocol.mode` and `protocol_compat_report.import_mode` may be `review_then_executable`, `review_only`, or `external_runner_required`. SAP GUI archives are preserved as `sap_gui` external-runner drafts for a Windows SAP GUI runner. TruClient 2 Web archives now produce executable browser drafts with replay, object-identification, runtime, and optional browser-to-HTTP review sections.

### 3. Sync Scripts With GitHub Enterprise And CI

GitHub Enterprise integration is project-policy enforced. Automation resolves credentials in this order, unless the system automation mode narrows it: project-policy GitHub App installation, per-user OAuth, then configured PAT fallback. API responses may include `auth_mode`, `remote_blob_sha`, `remote_commit_sha`, `policy`, `rate_limit`, and `check_run` fields.

Project policy controls allowed repositories, branches, path templates, direct-commit versus PR behavior, import/push permissions, bulk sync defaults, webhook auto-sync, and check-run publishing. Mutating routes reject requests outside that policy even when the connected GitHub identity can access more.

Useful GitHub endpoints:

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

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

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

#### IP Spoofing (Source-IP Pool)

Set a pool of local source IP addresses and APEX Load binds each virtual user to one of them
(round-robin, stable per VUser), so the run presents many distinct client source addresses to the
target — the LoadRunner "Multiple IP Addresses" capability, useful to defeat server-side IP
throttling or sticky load balancing. Configure it either in the script (`config.web_runtime.source_ips`)
or as a test-level runtime override:

```json
{
  "runtime_settings": {
    "web_runtime": { "source_ips": ["10.0.0.1", "10.0.0.2", "10.0.0.3"] }
  }
}
```

In the dashboard this is exposed in the Scenario Setup wizard under **Runtime Settings → Enable IP spoofing**.

- **HTTP/HTTPS only.** Binding applies to the HTTP protocol (HTTP/1.1 and HTTP/2); other protocols ignore the pool.
- **Generators must own the addresses.** Each generator host must already have these IPs configured on its network interfaces, or the connection fails at dial time (`bind: cannot assign requested address`). In distributed runs every generator uses the full pool — IPs are not partitioned per host.
- **Validated on create.** Each entry must parse as an IPv4/IPv6 address; an invalid value is rejected when the scenario or run is validated, rather than failing silently at runtime. The runtime override merges field-by-field, so supplying `source_ips` does not clear other `web_runtime` settings (e.g. `network_simulation`).
- The LoadRunner `lr_enable_ip_spoofing` / `lr_disable_ip_spoofing` calls are accepted for import compatibility but are no-ops — binding is governed entirely by the `source_ips` pool.

### 5. Run And Control Tests

Create a run directly:

```bash
curl -X POST http://localhost:8080/api/v1/tests \
  -H "Authorization: Bearer $APEXLOAD_API_KEY" \
  -H "Content-Type: application/json" \
  -d @test-run.json
```

Add `reserve_launch_window: true` to create a manual-start reservation. The API stores the rounded reservation start in `scheduled_start_at`, sets status to `RESERVED`, and waits for `POST /api/v1/tests/{id}/start` before execution begins.

Control endpoints:

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

`stop` lets each VUser finish its currently executing action, skips remaining think time/actions, and then runs teardown. `abort` hard-cancels active local work and sends `graceful: false` to distributed generators. Active requests remain durably `STOPPING` until teardown/archive finalization publishes a terminal `ABORTED` state; the control response therefore reports the current durable status plus `stop_requested` or `abort_requested`. Queued and other pre-start runs transition directly to `ABORTED`.
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

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

Some reporting endpoints accept `format` or `profile` query parameters where implemented, for example `format=html|json|csv|pdf|jsonl|parquet` and `profile=executive|engineer|audit`.

Usage accounting from `GET /api/v1/system/usage` may include a `by_user` array when run attribution exists. Each bucket follows the same usage bucket shape used by project and protocol summaries; historical runs without attribution are grouped as `unattributed`.

### 8. Federate Library Sources

Library APIs move reusable scripts, scenarios, results, and archives between this controller and a hosted team library. The dashboard surfaces these controls in the **Library Sources** boxes on Scripts and Scenarios. Remote definitions are system-admin managed; asset pull/push/import operations are scoped by project and the kind being moved.

Useful library endpoints:

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

### 9. Record, Replay, And Debug

Unified replay sessions use `/api/v1/replay/sessions/...` for draft and stored script replay. The older `/api/v1/script-debug/sessions/...` routes are deprecated compatibility aliases. Browser replay canary sessions still use `/api/v1/browser-replay/sessions/...`. The recorder routes manage proxy-captured HTTP traffic and conversion previews.

Useful recorder and replay endpoints:

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

### 10. Govern Assisted Authoring

The assisted-authoring surface is policy-gated and defaults to the deterministic local provider. External LLM integration is not wired by default. AI draft output cannot directly become a reusable script: drafts enter an authoring session and must pass structural validation, diff review, replay evidence, and explicit approval.

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

### 11. Manage Data Pools And Project Secrets

Data pools are project-scoped reusable row sets. They can be bound to scenarios and overridden per execution-plan entry. Test execution currently supports only `access_mode: read_only`: rows are snapshotted into the run and the durable pool is not mutated. `read_write` and `consume` requests are rejected until durable, HA-safe row coordination is available. Project secrets back the `{{secret.name}}` template namespace and expose metadata/status without returning secret values.

Row `status` is free text; rows whose status is `consumed`, `used`, `disabled`, `inactive`, or `exhausted` are excluded from run snapshots, exports with `?status=active`, and load-model row counts — every other status (including blank) executes. Archiving a pool (`archived_at`) blocks new bindings, imports, and run creation until it is restored; on `PUT /api/v1/data-pools/{id}` an omitted `archived_at` leaves the archive state unchanged, an explicit `null` restores, and a timestamp archives. `PUT` requests must echo the previously read `updated_at` (pools) or `version` (rows) as an optimistic-concurrency token; a mismatch returns `409`.

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

Secret writes are intentionally unavailable to service accounts even when the account has broad roles; use an authorized human workflow and audit the result. A partial batch failure can report already-created entries, the failed entry, and unattempted entries, so clients should read the response and refresh status before retrying.

### 12. Orchestrate Execution Plans, Reservations, And CI Runs

Execution plans group ordered scenario entries with per-entry runtime/data-pool overrides. `run_mode` accepts `parallel` or `sequential`. Sequential execution is dependency-gated: each enabled entry starts only after the previous run reaches a terminal state. The entry failure policy determines whether a failure halts the sequence or skips onward. Parallel plans create their enabled entries eagerly. Persisted plan-run state supports recovery after controller restart or HA takeover.

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

Capacity reservations protect a project/time window and can optionally target a fleet pool. New reservations always start `pending`; approval and run binding use dedicated endpoints.

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

Controller-backed GitHub Actions runs require project policy plus either the GitHub OIDC exchange or an approved `ci_runner` service account. Use an `Idempotency-Key` on create so retries recover the same accepted attempt.

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

### 13. Operate Fleet And Monitoring Integrations

Fleet APIs expose live capacity, load zones, alerts, persistent pool/metadata records, lifecycle controls, and provisioning requests. Cordon excludes new placement; drain marks a generator as draining; evacuation supports `{"dry_run":true}` before moving/handling affected distributed runs. Provisioning requests are recorded honestly as `not_configured` when no live cloud/Kubernetes provisioner is installed.

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

Monitoring collectors use one-time registration/rotation tokens. The heartbeat route is authenticated in-handler with that collector token (Bearer or `X-APEXLoad-API-Key`), not ordinary human RBAC middleware. Imported Grafana dashboards follow inspect/preview/commit-style workflows and do not silently create unsupported live integrations.

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

### 14. Inspect Entitlements And Import External Results

Entitlements are a read-only self-hosted projection of enabled bundles, configured limits, and current usage. They do not implement a licensing engine or enforce limits.

Use the [API Reference](#/swagger) for current methods, paths, permissions and response contracts for this workflow.

## Generated Endpoint Map

The exhaustive route inventory and inferred portable contract are generated from the controller route/handler code. Use the dashboard Swagger page, [swagger.yaml](swagger.yaml), or [api-endpoint-catalog.json](api-endpoint-catalog.json) when automation needs the complete current route surface. Use this guide and focused runbooks for semantic validation, conditional behavior, and operational examples.

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
