# APEX Load API User Guide

This guide is for engineers and operators who want to automate APEX Load from CI/CD, external release systems, or integration scripts. It focuses on workflow guidance for the public REST and WebSocket API served by the controller.

For the exhaustive, current route inventory, use the dashboard [API Reference](#/swagger). Those views are generated from the controller route catalog; this repository guide is hand-maintained prose for common automation flows and operational context.

Last reviewed against `pkg/api/server.go`, `pkg/api/route_catalog.go`, and the generated endpoint catalog for the September 24, 2026 documentation update. Behavioral corrections also reference the HTTP protocol and execution-plan orchestrator.

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

The controller applies TLS compatibility defaults automatically outside production. In production (`APEXLOAD_ENV=production` or `prod`) they are skipped unless you opt in:

```bash
APEXLOAD_ENV=production APEXLOAD_ENABLE_TLS_COMPAT=true go run ./cmd/controller
```

`APEXLOAD_ENABLE_TLS_COMPAT` therefore only matters when `APEXLOAD_ENV` is `production` or `prod`. When applied, the controller appends any missing `GODEBUG` tokens:

- `x509negativeserial=1`
- `tlsmlkem=0`
- `tlssecpmlkem=0`

To disable the defaults in any environment:

```bash
APEXLOAD_DISABLE_TLS_COMPAT=true go run ./cmd/controller
```

`APEXLOAD_DISABLE_DEV_TLS_COMPAT` is accepted as an alias for `APEXLOAD_DISABLE_TLS_COMPAT`.

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

- `GET /api/v1/auth/me` — Current principal, effective roles, and CSRF token for OIDC sessions
- `GET /api/v1/auth/effective-access` — Effective access for the caller
- `GET /api/v1/auth/diagnostics` — Authentication diagnostics
- `GET /api/v1/auth/users` — List local users
- `POST /api/v1/auth/users` — Create a local user
- `POST /api/v1/auth/users/onboard` — Onboard a user with a role binding
- `POST /api/v1/auth/users/{id}/bindings` — Create a role binding
- `POST /api/v1/auth/users/{id}/disable` — Disable a user
- `GET /api/v1/auth/service-accounts` — List service accounts
- `POST /api/v1/auth/service-accounts` — Create a service account (key returned once)
- `POST /api/v1/auth/service-accounts/{id}/rotate` — Rotate a service-account key
- `POST /api/v1/auth/ci/github/exchange` — GitHub Actions OIDC token exchange
- `GET /api/v1/auth/audit-events` — List audit events
- `GET /api/v1/auth/user-activity` — User activity

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

Stored scripts use `POST /api/v1/scripts/{id}/replay/sessions` with the same payload minus `script` unless overriding the draft definition. The response includes `preflight` readiness checks and a `session` object with `mode`, `runtime_profile`, lifecycle state, logs, events, and final report. `POST /api/v1/scripts/debug/sessions` (and `POST /api/v1/scripts/{id}/debug/sessions`) are also registered and behave as compatibility wrappers over the same session engine; prefer the `replay/sessions` start routes. Browser replay start routes (`/api/v1/scripts/browser-replay/sessions`) remain for browser replay canary sessions. Replay sessions are protocol-agnostic; browser scripts include screenshots, DOM, console, network, Core Web Vitals, and HTML evidence when captured.

Replay control endpoints (`/api/v1/replay/sessions/...` is canonical; the identical `/api/v1/script-debug/sessions/...` routes are registered but marked deprecated in `swagger.yaml`):

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

Import endpoints (per-importer `support-matrix` GET routes and further importers are listed in [api-endpoint-catalog.json](api-endpoint-catalog.json)):

- `POST /api/v1/scripts/import/openapi` — OpenAPI / Swagger (also `/commit`)
- `POST /api/v1/scripts/import/postman` — Postman collection (also `/validate`, `/save`)
- `POST /api/v1/scripts/import/bruno/inspect` — Inspect a Bruno bundle, then `POST .../bruno/commit`
- `POST /api/v1/scripts/import/har` — HAR traffic (Salesforce traffic is auto-detected)
- `POST /api/v1/scripts/import/wsdl` — WSDL / SOAP
- `POST /api/v1/scripts/import/asyncapi` — AsyncAPI (also `/commit`, `/commit-batch`)
- `POST /api/v1/scripts/import/jmeter` — JMeter test plan (also `/validate`, `/save`)
- `POST /api/v1/scripts/import/k6` — k6 script
- `POST /api/v1/scripts/import/cloudtest` — Akamai CloudTest export
- `POST /api/v1/scripts/import/enterprise-script-archive` — LoadRunner-style script archive (also `/validate`, `/save`)
- `POST /api/v1/scripts/import/recorder` — Recorder export
- `POST /api/v1/scripts/import/github` — Import a script from GitHub

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

Archive-backed WSDL, DevWeb, and Enterprise Script Archive imports accept regular files only and enforce bounded entry count, per-entry and total expanded size, path depth, and compression ratio. Traversal, absolute/drive paths, links/devices, invalid UTF-8 or control characters, names with leading or trailing whitespace, case/NFC/separator collisions, size mismatches, excessive compression ratios, and decompression bombs are rejected before source-root selection. See the [Web/API Execution Support Matrix](PROTOCOL_SUPPORT.md#shared-request-import-pipeline) for the per-importer tested disposition matrix.

Enterprise Script Archive requests are multipart form uploads. Choose a `.zip` in the dashboard, or choose or drop an unpacked VuGen script folder containing exactly one `.usr` project file in the drop zone. A folder upload sends each file under a `file:<percent-encoded-relative-path>` multipart field (for example, `file:Demo%2FAction.c`); omit the `file` ZIP field when sending a folder. The server validates paths and sizes, builds a temporary ZIP, and runs the same converter. Common option fields are `name`, `base_url`, `include_assertions`, `preserve_transactions`, `migration_block_statuses`, `expire_draft`, and `salesforce_native`. Draft expiration is disabled by default; set `expire_draft=true` to apply the 30-minute save-capability lifetime and receive `import_draft_expires_at` in the response. The deprecated `allow_java_vuser_runner` field is still accepted for compatibility but has no effect. Every LoadRunner Java archive produces one combined `java_vuser` draft: `script` and the sole item in `scripts` describe the same draft, and HTTP, JDBC, JMS, SOAP, LoadRunner utility calls, and custom Java are not split into native lanes. Java responses use `import_mode: "external_runner_required"` with `runner_requirement: "java_vuser_jvm_runner"`; launch requires an enabled runner advertising both `java_vuser_persistent_lifecycle_v1` and `java_vuser_session_frame_token_v1`. A qualified local or loopback-sidecar runner that advertises both capabilities keeps one JVM and one `Actions` instance alive per VUser across `init`, repeated `action` iterations, and `end`, preserving Java fields and saved LoadRunner parameters for that lifecycle. Windows Job Object containment or a narrowly delegated Linux cgroup can qualify; the stock non-privileged Helm, Kustomize, and Compose sidecars remain legacy `full_lifecycle` only.

A complete Java external-runner draft is review-required, not fidelity-loss blocked. Save it as one authoring session and approve it through the normal authoring workflow. Approval may place the draft in the script library, but test launch remains blocked until a governed Java runner advertises the required capability and capacity. The importer does not compile or distribute Java source; operators provision the compiled `Actions` class and its reviewed third-party dependencies on the runner classpath.

The import response uses the shared import review envelope: `script`/`scripts`, `warnings`, `stats`, `review_sections`, `repair_suggestions`, `source_review`, `fidelity_report`, `detected_protocol`, `protocol_compat_report`, `review_artifact`, `trueclient2_compat_report`, and `parameterization`. `detected_protocol.mode` and `protocol_compat_report.import_mode` may be `review_then_executable`, `review_only`, or `external_runner_required`. SAP GUI archives are preserved as `sap_gui` external-runner drafts for a Windows SAP GUI runner. TruClient 2 Web archives now produce executable browser drafts with replay, object-identification, runtime, and optional browser-to-HTTP review sections.

### 3. Sync Scripts With GitHub Enterprise And CI

GitHub Enterprise integration is project-policy enforced. Automation resolves credentials in this order, unless the system automation mode narrows it: project-policy GitHub App installation, per-user OAuth, then configured PAT fallback. API responses may include `auth_mode`, `remote_blob_sha`, `remote_commit_sha`, `policy`, `rate_limit`, and `check_run` fields.

Project policy controls allowed repositories, branches, path templates, direct-commit versus PR behavior, import/push permissions, bulk sync defaults, webhook auto-sync, and check-run publishing. Mutating routes reject requests outside that policy even when the connected GitHub identity can access more.

Useful GitHub endpoints:

- `GET /api/v1/integrations/github/connection` — Connection and auth mode for the caller
- `GET /api/v1/integrations/github/repos` — Repositories visible to the connection
- `GET /api/v1/integrations/github/branches` — Branches of a repository
- `GET /api/v1/projects/{id}/github/policy` — Read project GitHub policy
- `PUT /api/v1/projects/{id}/github/policy` — Update project GitHub policy
- `POST /api/v1/integrations/github/sync-jobs` — Create a bulk import/push job
- `GET /api/v1/integrations/github/sync-jobs/{id}` — Read a sync job
- `POST /api/v1/scripts/{id}/github/push` — Push a script
- `GET /api/v1/scripts/{id}/github/status` — Script sync status
- `POST /api/v1/tests/{id}/github/check-run` — Publish a check run for a result

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
    "require_pull_request": true,
    "allow_import": true,
    "allow_push": true,
    "bulk_sync_enabled": true,
    "auto_sync_enabled": true,
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

- `GET /api/v1/scenarios` — List scenarios
- `POST /api/v1/scenarios` — Create a scenario
- `POST /api/v1/scenarios/validate-load-model` — Validate a load model
- `POST /api/v1/scenarios/size-from-goal` — Size a scenario from a goal
- `GET /api/v1/scenarios/load-models/templates` — Load-model templates
- `PUT /api/v1/scenarios/{id}/data-pools` — Bind data pools
- `POST /api/v1/scenarios/{id}/schedules` — Create a schedule
- `GET /api/v1/scenarios/{id}/runtime-settings/report` — Effective runtime-settings report

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

In the dashboard this is the **Source IPs (IP spoofing)** field (one IP per line) in the Script Editor's runtime settings, and the **Source IPs** field on the **Web/HTTP** tab of the scenario runtime-settings group editor.

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

- `GET /api/v1/tests/{id}` — Run detail
- `POST /api/v1/tests/preflight` — Preflight a run before creating it
- `POST /api/v1/tests/{id}/start` — Start a queued or reserved run
- `POST /api/v1/tests/{id}/pause` — Pause
- `POST /api/v1/tests/{id}/resume` — Resume
- `POST /api/v1/tests/{id}/stop` — Graceful stop
- `POST /api/v1/tests/{id}/abort` — Hard abort
- `PATCH /api/v1/tests/{id}/vusers` — Scale VUsers
- `PATCH /api/v1/tests/{id}/goal-targets` — Adjust per-group TPS targets for a `goal_oriented` run. Request: `{"group":"<name>","target_tps":<float>}`. Response includes updated `goal_targets` map and the new aggregate `target_average_tps`. Broadcasts `goal_target_changed` on the WebSocket. Returns `409` if the test is not `goal_oriented`.
- `PATCH /api/v1/tests/{id}/duration` — Extend or shorten duration.
- `PATCH /api/v1/tests/{id}/move` — Move a run into another folder.
- `DELETE /api/v1/tests/{id}` — Delete a run record.
- `GET /api/v1/tests/{id}/metrics` — Current metrics snapshot.
- `GET /api/v1/tests/{id}/errors/evidence` — Live error evidence for a running test. Query params: `signature` (required — error pattern key), `action` (optional filter), `limit` (1–100, default 20). Response: `{test_id, signature, evidence:[]}` where each item carries `id`, `signature`, `action`, `group`, `timestamp`, `duration_ms`, `status`, `status_code`, `error_message`, and optional `snapshot`.
- `GET /api/v1/tests/{id}/sla-status` — SLA gate status for a run.
- `POST /api/v1/tests/{id}/comparison-baseline/pin` — Pin a run as its release comparison baseline.
- `DELETE /api/v1/tests/{id}/comparison-baseline/pin` — Remove the pinned comparison baseline.

`stop` lets each VUser finish its currently executing action, skips remaining think time/actions, and then runs teardown. `abort` hard-cancels active local work and sends `graceful: false` to distributed generators. Active requests remain durably `STOPPING` until teardown/archive finalization publishes a terminal `ABORTED` state; the control response therefore reports the current durable status plus `stop_requested` or `abort_requested`. Queued and other pre-start runs transition directly to `ABORTED`.

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

- `GET /api/v1/tests/{id}/report` — HTML report for a run
- `GET /api/v1/tests/{id}/archive/report` — Archive report (`format`, `profile` query parameters)
- `GET /api/v1/tests/{id}/archive` — Download the run archive
- `GET /api/v1/tests/{id}/archive/manifest` — Archive manifest
- `GET /api/v1/tests/{id}/archive/verify` — Verify archive artifact integrity
- `GET /api/v1/tests/{id}/archive/comparison` — Baseline comparison
- `GET /api/v1/tests/{id}/archive/files/{name}` — Download one archive artifact

The archive report's `overview`, `by_action` and `by_group` count every retained
event together, requests and the transactions that wrap them: this is the mixed
legacy population. A script with per-action auto transactions therefore counts each
request twice, once as the request and once as its transaction. For counts that
match what the target received, read `metric_populations` (`action` for requests,
`transaction` for business transactions, `unknown` for older events that recorded
neither) or the `by_request` and `by_transaction` maps; the dashboard's report pages
read these.

Some reporting endpoints accept `format` or `profile` query parameters where implemented, for example `format=html|json|csv|analysis_zip|pdf|docx|jsonl|parquet` and `profile=executive|engineer|audit`. `analysis_zip` on the archive report endpoint downloads row-based CSV tables plus a data guide while the original four-column report CSV remains available.

Usage accounting from `GET /api/v1/system/usage` may include a `by_user` array when run attribution exists. Each bucket follows the same usage bucket shape used by project and protocol summaries; historical runs without attribution are grouped as `unattributed`.

### 8. Federate Library Sources

Library APIs move reusable scripts, scenarios, results, and archives between this controller and a hosted team library. The dashboard surfaces these controls in the **Library Sources** boxes on Scripts and Scenarios. Remote definitions are system-admin managed; asset pull/push/import operations are scoped by project and the kind being moved.

Useful library endpoints:

- `GET /api/v1/library/catalog` — Local library catalog
- `POST /api/v1/library/export` — Export a script, scenario, or result bundle
- `POST /api/v1/library/import` — Import a bundle
- `GET /api/v1/library/remotes` — List remote library sources
- `POST /api/v1/library/remotes` — Create a remote (system admin)
- `GET /api/v1/library/remotes/{id}/catalog` — Remote catalog
- `POST /api/v1/library/remotes/{id}/pull` — Pull assets from a remote
- `POST /api/v1/library/remotes/{id}/push` — Push assets to a remote

### 9. Record, Replay, And Debug

Unified replay sessions use `/api/v1/replay/sessions/...` for draft and stored script replay. The older `/api/v1/script-debug/sessions/...` routes are deprecated compatibility aliases. Browser replay canary sessions still use `/api/v1/browser-replay/sessions/...`. The recorder routes manage proxy-captured HTTP traffic and conversion previews.

Useful recorder and replay endpoints:

- `POST /api/v1/recorder/start` — Start a proxy recording
- `POST /api/v1/recorder/{id}/stop` — Stop a recording
- `POST /api/v1/recorder/{id}/convert/preview` — Preview conversion to a script
- `POST /api/v1/recorder/{id}/convert` — Convert a recording
- `POST /api/v1/scripts/replay/sessions` — Start a replay session for a draft script
- `POST /api/v1/scripts/{id}/replay/sessions` — Start a replay session for a stored script
- `GET /api/v1/replay/sessions/{session_id}` — Read a session (canonical)
- `POST /api/v1/replay/sessions/{session_id}/step` — Step a paused session
- `GET /api/v1/replay/sessions/{session_id}/export` — Export a session
- `POST /api/v1/scripts/browser-replay/sessions` — Start a browser replay canary for a draft

### 10. Govern Assisted Authoring

The assisted-authoring surface is policy-gated and defaults to the deterministic local provider. External LLM integration is not wired by default. AI draft output cannot directly become a reusable script: drafts enter an authoring session and must pass structural validation, diff review, replay evidence, and explicit approval.

- `GET /api/v1/ai/policy` — AI policy
- `GET /api/v1/ai/authoring/contract` — Authoring contract
- `POST /api/v1/ai/drafts` — Generate a draft into an authoring session
- `POST /api/v1/scripts/authoring/sessions` — Create an authoring session
- `POST /api/v1/scripts/authoring/sessions/{id}/approve` — Approve a session into the script library
- `GET /api/v1/ai/review-queue` — Review queue

### 11. Manage Data Pools And Secrets

Data pools are project-scoped reusable row sets. They can be bound to scenarios and overridden per execution-plan entry. The default `access_mode: read_only` snapshots rows into the run without mutating the durable pool. Explicit `access_mode: live` enables [run-scoped tables](LIVE_DATA_TABLES_RUNTIME.md), or a [project table reference](PROJECT_LIVE_TABLES.md) with shared lifetime across admitted runs. The legacy `read_write` and `consume` access modes remain rejected; use the explicit live-table contract for runtime coordination. Application and project secrets back the `{{secret.name}}` template namespace and expose metadata/status without returning secret values. For an application, use `/api/v1/applications/{id}/secrets` to list or create, `/secrets/status` to resolve effective status, `/secrets/{name}/usage` to inspect assigned script consumers, and `PUT /secrets/{name}` to rotate. Application values override project values for managed runs targeting that application; see the generated [API reference](#/swagger) for request and response contracts.

Row `status` is free text; rows whose status is `consumed`, `used`, `disabled`, `inactive`, or `exhausted` are excluded from run snapshots, exports with `?status=active`, and load-model row counts — every other status (including blank) executes. Archiving a pool (`archived_at`) blocks new bindings, imports, and run creation until it is restored; on `PUT /api/v1/data-pools/{id}` an omitted `archived_at` leaves the archive state unchanged, an explicit `null` restores, and a timestamp archives. `PUT` requests must echo the previously read `updated_at` (pools) or `version` (rows) as an optimistic-concurrency token; a mismatch returns `409`.

- `GET /api/v1/data-pools` — List data pools
- `POST /api/v1/data-pools` — Create a data pool
- `PUT /api/v1/data-pools/{id}` — Update or archive a pool
- `POST /api/v1/data-pools/{id}/rows/import` — Import rows
- `GET /api/v1/data-pools/{id}/rows/export` — Export rows
- `GET /api/v1/projects/{id}/secrets` — List secret metadata
- `POST /api/v1/projects/{id}/secrets` — Create secrets
- `POST /api/v1/projects/{id}/secrets/status` — Check secret status
- `PUT /api/v1/projects/{id}/secrets/{name}` — Rotate a secret
- `GET /api/v1/projects/{id}/secrets/{name}/usage` — Where a secret is used

Secret writes are intentionally unavailable to service accounts even when the account has broad roles; use an authorized human workflow and audit the result. A partial batch failure can report already-created entries, the failed entry, and unattempted entries, so clients should read the response and refresh status before retrying.

### 12. Orchestrate Execution Plans, Reservations, And CI Runs

Execution plans group ordered scenario entries with per-entry runtime/data-pool overrides. `run_mode` accepts `parallel` or `sequential`. Sequential execution is dependency-gated: each enabled entry starts only after the previous run reaches a terminal state. The entry failure policy determines whether a failure halts the sequence or skips onward. Parallel plans create their enabled entries eagerly. Persisted plan-run state supports recovery after controller restart or HA takeover.

- `POST /api/v1/execution-plans` — Create a plan
- `PUT /api/v1/execution-plans/{id}` — Update a plan
- `POST /api/v1/execution-plans/{id}/runs` — Materialize and run a plan
- `GET /api/v1/execution-plans/{id}/runs/{runId}` — Read a plan run
- `POST /api/v1/execution-plans/{id}/schedules` — Create a plan schedule

Capacity reservations protect a project/time window and can optionally target a fleet pool. New reservations always start `pending`; approval and run binding use dedicated endpoints.

- `POST /api/v1/capacity/timeslots` — Create a reservation (starts `pending`)
- `POST /api/v1/capacity/timeslots/availability` — Check availability
- `POST /api/v1/capacity/timeslots/{id}/approve` — Approve
- `POST /api/v1/capacity/timeslots/{id}/bind` — Bind to a run

Controller-backed GitHub Actions runs require project policy plus either the GitHub OIDC exchange or an approved `ci_runner` service account. Use an `Idempotency-Key` on create so retries recover the same accepted attempt.

- `POST /api/v1/ci/runs` — Create a CI run
- `GET /api/v1/ci/runs/{id}` — Read a CI run
- `POST /api/v1/ci/runs/{id}/abort` — Abort a CI run

### 13. Operate Fleet And Monitoring Integrations

Fleet APIs expose live capacity, load zones, alerts, persistent pool/metadata records, lifecycle controls, and provisioning requests. Cordon excludes new placement; drain marks a generator as draining; evacuation supports `{"dry_run":true}` before moving/handling affected distributed runs. Provisioning requests are recorded honestly as `not_configured` when no live cloud/Kubernetes provisioner is installed.

- `GET /api/v1/fleet/capacity` — Live capacity
- `GET /api/v1/fleet/generators` — Generator metadata
- `POST /api/v1/fleet/generators/{id}/cordon` — Exclude from new placement
- `POST /api/v1/fleet/generators/{id}/drain` — Mark draining
- `POST /api/v1/fleet/generators/{id}/evacuate` — Evacuate (supports `dry_run`)
- `POST /api/v1/fleet/provision-requests` — Record a provisioning request

Monitoring collectors use one-time registration/rotation tokens. The heartbeat route is authenticated in-handler with that collector token (Bearer or `X-APEXLoad-API-Key`), not ordinary human RBAC middleware. Imported Grafana dashboards follow inspect/preview/commit-style workflows and do not silently create unsupported live integrations.

- `POST /api/v1/monitoring/collectors` — Register a collector
- `POST /api/v1/monitoring/collectors/{id}/heartbeat` — Collector heartbeat
- `POST /api/v1/monitoring/collectors/{id}/rotate-token` — Rotate a collector token
- `POST /api/v1/monitoring/dashboards/import/grafana/preview` — Preview a Grafana import
- `POST /api/v1/monitoring/dashboards/import/grafana/commit` — Commit a Grafana import

### 14. Inspect Entitlements And Import External Results

Entitlements are a read-only self-hosted projection of enabled bundles, configured limits, and current usage. They do not implement a licensing engine or enforce limits.

- `GET /api/v1/system/entitlements` — Entitlements projection
- `GET /api/v1/system/entitlements/usage` — Usage against configured limits
- `GET /api/v1/projects/{id}/entitlements/usage` — Project-level usage
- `POST /api/v1/results/import` — Import external results
- `GET /api/v1/results/imports` — List external result imports

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

## Start with the REST API

Use the REST API for integrations, the CLI for pipelines, or MCP for agent-driven workflows. They operate on the same project-scoped resources. The [API Reference](#/swagger) is generated from controller routes; the [workflow guide](API_USER_GUIDE.md#core-workflows) describes conditional behavior and multi-step operations.

### Connect and verify access

Use the controller's reachable origin plus `/api/v1`. Hosted deployments usually use HTTPS through their configured ingress. `http://localhost:8080/api/v1` is a local example, not a universal production address. The dashboard development server normally proxies `/api` from localhost:4000.

Ask an authorized administrator for a service-account key with the project roles and token scopes needed for your task. Set `APEXLOAD_API_KEY` in your shell or CI secret store. A key is shown only once when created or rotated. Never paste it into a shared script or report. See [API authentication](API_USER_GUIDE.md#authentication).

```bash
export APEXLOAD_URL=http://localhost:8080/api/v1
curl --fail-with-body "$APEXLOAD_URL/system/health"
curl --fail-with-body -H "Authorization: Bearer $APEXLOAD_API_KEY" "$APEXLOAD_URL/auth/me"
curl --fail-with-body -H "Authorization: Bearer $APEXLOAD_API_KEY" "$APEXLOAD_URL/projects"
```

These are read-only checks. Expect HTTP 200, controller health information, the authenticated identity, and projects visible to that account. An empty project list can be a scope issue. A 401 means authentication failed; a 403 means an authenticated request was denied. Follow [authentication troubleshooting](HELP_CENTER.md#authentication-or-permission-denied).

### Create, validate and run

Save the complete JSON example from [Your first test](HELP_CENTER.md#create-and-prove-the-script) as script.json and replace its base URL with a target reachable from the execution environment. Use [API workflows](API_USER_GUIDE.md#core-workflows) to create the script in your project, validate it, assemble a scenario, and start a run. Those operations create state and the run sends traffic; use the required project and release identifiers rather than copying someone else's IDs.

For a local CLI exercise, the same script can be validated and executed without creating controller resources:

```bash
apexctl script validate --script script.json --format json
apexctl run --script script.json --vusers 1 --duration 30s
```

Expect validation to succeed before running. Inspect errors and the report rather than treating process completion as a performance verdict. Use [CLI & CI/CD](APPLICATION_USER_GUIDE.md#automating-apex-load) for exit codes and [CI pipeline examples](CICD_INTEGRATION.md#copy-ready-github-actions-gates) for controller-backed pipelines.

### Responses and recovery

| Status | Meaning | Next step |
| --- | --- | --- |
| 200 / 201 | Read or creation succeeded | Read the returned identity and resource IDs. |
| 202 | Work accepted asynchronously | Poll the documented status resource. |
| 400 | Invalid input | Read the validation message and correct the request. |
| 401 | Missing or invalid identity | Check the key, session and controller URL. |
| 403 | Permission or scope denied | Check both roles and token scopes. |
| 404 | Resource unavailable | Verify IDs and project scope. |
| 409 | Conflict or admission gate | Inspect the response; resolve revision, capacity or approval requirements before retrying. |
| 429 / 5xx | Throttling or service failure where declared | Preserve request context, inspect service health, and retry only when the operation is safe or idempotent. |

Error bodies normally include `error`; some workflows include structured evidence or partial results. Download endpoints may return CSV, HTML, ZIP or binary artifacts. The operation's [generated reference](#/swagger) describes declared response contracts. Do not blindly retry a mutation after an ambiguous network failure.

### Advanced local development

In local dev auth mode, `X-APEXLoad-Dev-View: admin|user` and `X-APEXLoad-Dev-Project-ID: <project-id>` simulate dashboard access. Production auth modes ignore these headers. Use service-account identity for automation; see [identity operations](AUTH_RBAC_OPERATIONS_RUNBOOK.md) for deployment configuration.
