# APEX Load API User Guide

This guide is for engineers and operators who want to automate APEX Load from CI/CD, external release systems, or integration scripts. It focuses on workflow guidance for the public REST and WebSocket API served by the controller.

For the exhaustive, current route inventory, use the dashboard **Swagger** page or **API Guide → Endpoint Map**. Those views are generated from the controller route catalog; this repository guide is hand-maintained prose for common automation flows and operational context.

Last reviewed against `pkg/api/server.go`, `pkg/api/route_catalog.go`, and the generated endpoint catalog on July 19, 2026.

### Reference Coverage

The generated OpenAPI document is authoritative for registered HTTP methods, paths, path/query/header parameter names and basic types, deprecation flags, authentication alternatives, route-level access requirements, request media/top-level serializable fields, and handler-declared response statuses/media. Semantic validation rules, conditional requirements, and some dynamic representations remain defined by handlers and workflow-specific guides. The Endpoint Map is a compact route inventory and access-oriented navigation aid; it is not an authorization source. The controller's runtime RBAC and service-account scope checks always decide access.

Use the references together:

- **Swagger**: every registered REST route plus inferred query/header, request-body, response, media, and access contracts; explicit maintained schemas override inference.
- **Endpoint Map**: compact grouping and a best-effort filter based on the current principal's effective operations.
- **Repository Guide**: request examples, behavioral contracts, limitations, and operational workflows.
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
- `local`: non-production Local Test Login with database-backed personas and 12-hour sessions.
- `trusted_headers`: an upstream gateway authenticates the user and forwards trusted identity headers.
- `oidc`: APEX Load handles OIDC login, callback, signed session, and logout.

For human users, OIDC, trusted headers, or Local Test Login prove identity, but APEX Load access is controlled by local user records and local role bindings. After bootstrap, an upstream-authenticated human principal must have an active local user whose `subject` matches the authenticated subject. Human effective roles come from `UserRoleBinding` rows, not from upstream role headers. Service accounts keep their existing key-based behavior.

Local Test Login is selected in Settings and takes effect after restart. It seeds `admin/admin` (system administrator), `domainadmin/domainadmin` (Default Domain administrator), `user/user` (read-only default workspace), and `dev/dev` (default workspace editor). Changes to fixture active state and role bindings in Access are preserved. Passwords are stored only as bcrypt hashes, and browser session tokens are stored only as SHA-256 hashes. These fixed credentials are deliberately rejected under production posture.

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

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/auth/login` | Initiate OIDC login flow; redirects to the configured identity provider. |
| `POST` | `/api/v1/auth/login` | Start a Local Test Login session from a username and password. |
| `GET` | `/api/v1/auth/callback` | OIDC callback; exchanges the authorization code for a signed session. |
| `POST` | `/api/v1/auth/logout` | CSRF-protected logout for the current OIDC or Local Test Login session. |
| `GET` | `/api/v1/auth/me` | Current auth mode, local user status, locally resolved roles, and session metadata. |
| `GET` | `/api/v1/auth/effective-access?project_id=<id>` | Operations allowed for the current principal from local user bindings or service-account roles. |
| `GET` | `/api/v1/auth/access-matrix` | Full role-to-operation catalog. |
| `GET` | `/api/v1/auth/roles` | Role catalog, scope rules, and assignable-role metadata. |
| `GET` | `/api/v1/auth/policy` | Current authentication and authorization policy projection. |
| `GET` | `/api/v1/auth/diagnostics` | OIDC/trusted-header readiness diagnostics. Requires system-admin access outside dev/disabled bypass modes. |
| `GET` | `/api/v1/auth/access-review` | Access-review evidence for the current authorized scope. |
| `GET` | `/api/v1/auth/effective-access/principal` | System-admin lookup of effective access for one user or service account. |
| `GET` | `/api/v1/auth/users` | Search local human users. Requires `manage_users` or `manage_user_bindings`; binding-only callers see manageable bindings only. |
| `POST` | `/api/v1/auth/users` | Onboard a human user subject. Requires `manage_users`. |
| `POST` | `/api/v1/auth/users/onboard` | Explicit onboarding workflow for a human user subject. Requires `manage_users`. |
| `GET` | `/api/v1/auth/users/{id}` | Get one local user and role bindings. Requires `manage_users` or `manage_user_bindings`; binding-only callers see manageable bindings only. |
| `PATCH` | `/api/v1/auth/users/{id}` | Update local user metadata. Requires `manage_users`. |
| `POST` | `/api/v1/auth/users/{id}/bindings` | Grant a global, domain, project, or repository role binding. Requires `manage_users` or `manage_user_bindings`, with delegated scope checks. |
| `DELETE` | `/api/v1/auth/users/{id}/bindings/{binding_id}` | Remove one local role binding. Requires `manage_users` or `manage_user_bindings`, with delegated scope checks. |
| `POST` | `/api/v1/auth/users/{id}/disable` | Disable a local user while preserving audit history. Requires `manage_users`. |
| `POST` | `/api/v1/auth/users/{id}/enable` | Re-enable a local user. Requires `manage_users`. |
| `POST` | `/api/v1/auth/users/{id}/revoke-sessions` | Increment the user's session epoch so existing OIDC and local sessions can no longer authenticate. |
| `GET` | `/api/v1/auth/user-activity` | Audit events plus attributed usage filtered by user, timeframe, domain, project, action, or status. |
| `GET` | `/api/v1/auth/user-activity/export` | Export user activity as CSV. |
| `GET` | `/api/v1/auth/service-accounts` | List service accounts. |
| `POST` | `/api/v1/auth/service-accounts` | Create a service account and return its one-time API key. |
| `PATCH` | `/api/v1/auth/service-accounts/{id}` | Update service-account metadata, roles, scopes, or active state. |
| `POST` | `/api/v1/auth/service-accounts/{id}/rotate` | Rotate a service-account API key. |
| `DELETE` | `/api/v1/auth/service-accounts/{id}` | Delete a service account. |
| `GET` | `/api/v1/auth/audit-events` | Query audit events. |
| `GET` | `/api/v1/auth/audit-events/export` | Export audit events as CSV. |
| `POST` | `/api/v1/auth/ci/github/exchange` | Exchange a policy-validated GitHub Actions OIDC token for a short-lived APEX Load CI bearer token. |

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

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/v1/scripts/import/enterprise-script-archive` | Import an enterprise script archive ZIP with protocol auto-detection. Returns executable scripts, review artifacts, or external-runner-required drafts depending on source protocol. |
| `POST` | `/api/v1/scripts/import/enterprise-script-archive/validate` | Validate an enterprise script archive without saving. Uses the same detection, fidelity, and review envelope as conversion. |
| `GET` | `/api/v1/scripts/import/enterprise-script-archive/support-matrix` | Enterprise script archive protocol import support, including TruClient 2 Web, Java-family, SAP GUI, and external-runner routes. |
| `POST` | `/api/v1/scripts/import/jmeter` | Import a JMeter test plan. |
| `POST` | `/api/v1/scripts/import/jmeter/save` | Save a reviewed JMeter import draft. |
| `POST` | `/api/v1/scripts/import/jmeter/validate` | Validate a JMeter archive or plan without saving. |
| `GET` | `/api/v1/scripts/import/jmeter/support-matrix` | JMeter import support matrix. |
| `POST` | `/api/v1/scripts/import/k6` | Import the constrained k6 HTTP subset into APEX Load DSL. |
| `POST` | `/api/v1/scripts/export/k6` | Export an APEX HTTP script to a reviewable k6 HTTP subset script. |
| `GET` | `/api/v1/scripts/import/k6/support-matrix` | k6 import/export support matrix. |
| `POST` | `/api/v1/scripts/import/har` | Import a HAR archive. |
| `POST` | `/api/v1/scripts/import/openapi` | Generate HTTP/API drafts from OpenAPI or Swagger. |
| `POST` | `/api/v1/scripts/import/asyncapi` | Inspect an AsyncAPI contract and return a reviewable messaging import. |
| `POST` | `/api/v1/scripts/import/asyncapi/commit` | Commit an approved AsyncAPI import draft. |
| `GET` | `/api/v1/scripts/import/asyncapi/support-matrix` | AsyncAPI protocol and operation coverage. |
| `POST` | `/api/v1/scripts/import/bruno/inspect` | Inspect a Bruno collection without saving scripts. |
| `POST` | `/api/v1/scripts/import/bruno/commit` | Commit an approved Bruno import. |
| `GET` | `/api/v1/scripts/import/bruno/support-matrix` | Bruno import coverage and limits. |
| `POST` | `/api/v1/scripts/import/broker-config` | Import supported broker configuration into a reviewable messaging draft. |
| `GET` | `/api/v1/scripts/import/broker-config/support-matrix` | Broker configuration import coverage. |
| `POST` | `/api/v1/scripts/import/postman` | Generate HTTP/API drafts from a Postman collection JSON file. |
| `POST` | `/api/v1/scripts/import/wsdl` | Generate SOAP drafts from WSDL. |
| `POST` | `/api/v1/scripts/import/devweb` | Import DevWeb-style assets. |
| `GET` | `/api/v1/scripts/import/devweb/support-matrix` | DevWeb import support matrix. |
| `GET` | `/api/v1/scripts/import/oracle-ebusiness/support-matrix` | Oracle E-Business/Siebel support matrix. |
| `POST` | `/api/v1/scripts/import/oracle-ebusiness/review` | Review Oracle E-Business, Siebel, or Oracle NCA source for supported routes. |
| `POST` | `/api/v1/scripts/import/sample` | Import raw HTTP, cURL, REST sample requests. |
| `GET` | `/api/v1/scripts/web-api/support-matrix` | Web API execution support matrix. |
| `GET` | `/api/v1/protocols/native-support-matrix` | Runtime-native protocol implementation status. |
| `GET` | `/api/v1/protocol-bundles/support-matrix` | Protocol bundle, governance, and entitlement projection. |

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

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/tests` | List runs. |
| `POST` | `/api/v1/tests/estimate` | Estimate capacity for a proposed run. |
| `POST` | `/api/v1/tests/preflight` | Pre-flight capacity and governance policy check for a proposed run without creating it. Blocks external-runner-required drafts until the matching runner capability is enabled. |
| `GET` | `/api/v1/tests/{id}` | Get run status and live summary. |
| `GET` | `/api/v1/tests/{id}/manifest` | Generator assignment manifest for a run (which generators host which groups). |
| `POST` | `/api/v1/tests/{id}/start` | Start a queued or reserved run. |
| `POST` | `/api/v1/tests/{id}/stop` | Gracefully stop a run. |
| `POST` | `/api/v1/tests/{id}/abort` | Abort immediately. |
| `POST` | `/api/v1/tests/{id}/pause` | Soft-pause between actions. |
| `POST` | `/api/v1/tests/{id}/resume` | Resume a paused run. |
| `PATCH` | `/api/v1/tests/{id}/vusers` | Change VUser count for a group. Send `action: "run"` to apply `target_vusers`, or `action: "pause"` / `"stop"` to drive that group to zero. Include optional `ramp_duration`, such as `"2m"`, to add or remove VUsers gradually. |

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
| `GET` | `/api/v1/applications/{id}/load-comparison?scope=transaction\|group&key=<name>&test_id=<run>` | Compare transaction or group TPS/ART under load across releases for an application. Add repeated `test_id` params or comma-separated `test_ids` to limit the archived runs considered. |
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

Unified replay sessions use `/api/v1/replay/sessions/...` for draft and stored script replay. The older `/api/v1/script-debug/sessions/...` routes are deprecated compatibility aliases. Browser replay canary sessions still use `/api/v1/browser-replay/sessions/...`. The recorder routes manage proxy-captured HTTP traffic and conversion previews.

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

### 10. Govern Assisted Authoring

The assisted-authoring surface is policy-gated and defaults to the deterministic local provider. External LLM integration is not wired by default. AI draft output cannot directly become a reusable script: drafts enter an authoring session and must pass structural validation, diff review, replay evidence, and explicit approval.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/ai/authoring/contract` | Published source types, evidence requirements, promotion gates, and audit contract. |
| `GET` | `/api/v1/ai/policy?project_id=<id>` | Effective provider allow-list, enabled state, redaction posture, retention, and project override. |
| `POST` | `/api/v1/ai/drafts` | Create a deterministic draft and, by default, an authoring review session. |
| `POST` | `/api/v1/ai/repairs` | Return deterministic repair suggestions and a preview without mutating the stored script. |
| `POST` | `/api/v1/ai/explain` | Explain a supported script, scenario, runtime, or report target with citations. |
| `GET` | `/api/v1/ai/review-queue?project_id=<id>` | List draft sessions awaiting review or approval. |
| `GET` | `/api/v1/scripts/authoring/capabilities` | Authoring workflow capabilities. |
| `GET` / `POST` | `/api/v1/scripts/authoring/sessions` | List or create authoring sessions. |
| `GET` / `PUT` / `DELETE` | `/api/v1/scripts/authoring/sessions/{id}` | Read, update, or discard one authoring session. |
| `POST` | `/api/v1/scripts/authoring/sessions/{id}/approve` | Promote an approval-ready authoring session. |

### 11. Manage Data Pools And Project Secrets

Data pools are project-scoped reusable row sets. They can be bound to scenarios and overridden per execution-plan entry. Test execution currently supports only `access_mode: read_only`: rows are snapshotted into the run and the durable pool is not mutated. `read_write` and `consume` requests are rejected until durable, HA-safe row coordination is available. Project secrets back the `{{secret.name}}` template namespace and expose metadata/status without returning secret values.

Row `status` is free text; rows whose status is `consumed`, `used`, `disabled`, `inactive`, or `exhausted` are excluded from run snapshots, exports with `?status=active`, and load-model row counts — every other status (including blank) executes. Archiving a pool (`archived_at`) blocks new bindings, imports, and run creation until it is restored; on `PUT /api/v1/data-pools/{id}` an omitted `archived_at` leaves the archive state unchanged, an explicit `null` restores, and a timestamp archives. `PUT` requests must echo the previously read `updated_at` (pools) or `version` (rows) as an optimistic-concurrency token; a mismatch returns `409`.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` / `POST` | `/api/v1/data-pools` | List or create data pools. |
| `GET` / `PUT` / `DELETE` | `/api/v1/data-pools/{id}` | Read, replace, or delete a pool. |
| `GET` / `POST` | `/api/v1/data-pools/{id}/rows` | List or append pool rows. |
| `POST` | `/api/v1/data-pools/{id}/rows/import` | Bulk-load rows from a CSV/TSV upload (`mode=append\|replace`, `delimiter=auto\|comma\|tab`, `header`, `columns`, `dry_run`). |
| `GET` | `/api/v1/data-pools/{id}/rows/export` | Stream the pool as an `.apexload.csv` round-trip file (`?status=active\|all`); the default `all` form preserves row status. Leading formula characters are apostrophe-guarded, and the APEX preamble remains recognizable after a file is renamed, while ordinary CSV files preserve literal apostrophes. |
| `PUT` / `DELETE` | `/api/v1/data-pools/{id}/rows/{row_id}` | Replace or delete one row. |
| `GET` | `/api/v1/projects/{id}/secrets` | List secret metadata and provider capabilities; values are never returned. |
| `POST` | `/api/v1/projects/{id}/secrets/status` | Check whether named secret requirements are configured. |
| `POST` | `/api/v1/projects/{id}/secrets` | Create one or more secrets with create-only semantics. Human authorization and a writable provider are required. |
| `PUT` | `/api/v1/projects/{id}/secrets/{name}` | Rotate an existing secret. Human authorization and a writable provider are required. |

Secret writes are intentionally unavailable to service accounts even when the account has broad roles; use an authorized human workflow and audit the result. A partial batch failure can report already-created entries, the failed entry, and unattempted entries, so clients should read the response and refresh status before retrying.

### 12. Orchestrate Execution Plans, Reservations, And CI Runs

Execution plans group ordered scenario entries with per-entry runtime/data-pool overrides. `run_mode` accepts `parallel` or `sequential`. In the current implementation, materialization creates the enabled run entries eagerly; `sequential` records the intended ordering and partial-failure behavior but does not yet wait for run N to finish before starting run N+1.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` / `POST` | `/api/v1/execution-plans` | List or create execution plans. |
| `GET` / `PUT` / `DELETE` | `/api/v1/execution-plans/{id}` | Read, revision-checked update, or delete a plan. |
| `POST` | `/api/v1/execution-plans/{id}/runs` | Materialize enabled entries and create their test runs. |
| `GET` | `/api/v1/execution-plans/{id}/runs` | List materialized plan runs. |
| `GET` | `/api/v1/execution-plans/{id}/runs/{runId}` | Read one plan-run record. |
| `GET` | `/api/v1/execution-plans/{id}/approvals` | List governance approvals for a plan. |
| `POST` | `/api/v1/execution-plans/{id}/approvals/{approval_id}/approve` | Approve a plan gate. |
| `POST` | `/api/v1/execution-plans/{id}/approvals/{approval_id}/reject` | Reject a plan gate. |
| `GET` / `POST` | `/api/v1/execution-plans/{id}/schedules` | List or create plan schedules. |
| `PUT` / `DELETE` | `/api/v1/execution-plans/{id}/schedules/{schedule_id}` | Update or delete a plan schedule. |

Capacity reservations protect a project/time window and can optionally target a fleet pool. New reservations always start `pending`; approval and run binding use dedicated endpoints.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` / `POST` | `/api/v1/capacity/timeslots` | List or create capacity reservations. |
| `GET` / `PUT` / `DELETE` | `/api/v1/capacity/timeslots/{id}` | Read, update, or delete a reservation. |
| `POST` | `/api/v1/capacity/timeslots/availability` | Check a proposed window against available/reserved capacity. |
| `POST` | `/api/v1/capacity/timeslots/{id}/approve` | Approve a pending reservation. |
| `POST` | `/api/v1/capacity/timeslots/{id}/reject` | Reject a pending reservation. |
| `POST` | `/api/v1/capacity/timeslots/{id}/bind` | Bind an approved reservation to a test. |

Controller-backed GitHub Actions runs require project policy plus either the GitHub OIDC exchange or an approved `ci_runner` service account. Use an `Idempotency-Key` on create so retries recover the same accepted attempt.

| Method | Path | Purpose |
| --- | --- | --- |
| `POST` | `/api/v1/auth/ci/github/exchange` | Validate GitHub Actions OIDC claims and return a short-lived bearer token. |
| `POST` | `/api/v1/ci/runs` | Validate policy/source identity and create an idempotent CI run. |
| `GET` | `/api/v1/ci/runs/{id}` | Read normalized CI run, SLA, result, and publication state. |
| `POST` | `/api/v1/ci/runs/{id}/abort` | Abort a CI run with an optional reason. |

### 13. Operate Fleet And Monitoring Integrations

Fleet APIs expose live capacity, load zones, alerts, persistent pool/metadata records, lifecycle controls, and provisioning requests. Cordon excludes new placement; drain marks a generator as draining; evacuation supports `{"dry_run":true}` before moving/handling affected distributed runs. Provisioning requests are recorded honestly as `not_configured` when no live cloud/Kubernetes provisioner is installed.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/fleet/capacity` | Aggregated placement capacity and generator health. |
| `GET` | `/api/v1/fleet/load-zones` | Region/load-zone projection. |
| `GET` | `/api/v1/fleet/alerts` | Fleet alerts with acknowledgement state. |
| `PATCH` | `/api/v1/fleet/alerts/{id}/ack` | Acknowledge an active fleet alert. |
| `GET` / `POST` | `/api/v1/fleet/pools` | List or create fleet pools. |
| `GET` / `PUT` / `DELETE` | `/api/v1/fleet/pools/{id}` | Read, update, or delete a fleet pool. |
| `GET` | `/api/v1/fleet/generators` | Persistent generator metadata. |
| `PATCH` | `/api/v1/fleet/generators/{id}` | Update target, region, pool, provider, cordon, or tags. |
| `POST` | `/api/v1/fleet/generators/{id}/cordon` | Exclude a generator from new placement. |
| `POST` | `/api/v1/fleet/generators/{id}/drain` | Mark a generator as draining. |
| `POST` | `/api/v1/fleet/generators/{id}/evacuate` | Preview or apply evacuation for active distributed runs. |
| `POST` | `/api/v1/fleet/generators/{id}/uncordon` | Return a generator to active placement. |
| `GET` / `POST` | `/api/v1/fleet/provision-requests` | List or create provisioning requests. |

Monitoring collectors use one-time registration/rotation tokens. The heartbeat route is authenticated in-handler with that collector token (Bearer or `X-APEXLoad-API-Key`), not ordinary human RBAC middleware. Imported Grafana dashboards follow inspect/preview/commit-style workflows and do not silently create unsupported live integrations.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` / `POST` | `/api/v1/monitoring/collectors` | List or register collectors. |
| `GET` / `PATCH` / `DELETE` | `/api/v1/monitoring/collectors/{id}` | Read, update, or remove a collector. |
| `POST` | `/api/v1/monitoring/collectors/{id}/rotate-token` | Rotate the one-time collector token. |
| `POST` | `/api/v1/monitoring/collectors/{id}/heartbeat` | Collector heartbeat ingestion. |
| `GET` | `/api/v1/monitoring/collectors/{id}/metrics` | Collector metrics projection. |
| `GET` | `/api/v1/monitoring/dashboards` | List imported Grafana dashboards. |
| `GET` | `/api/v1/monitoring/dashboards/graphs` | Query-ready graphs derived from imported dashboards. |
| `POST` | `/api/v1/monitoring/dashboards/import/grafana/preview` | Preview dashboard mapping. |
| `POST` | `/api/v1/monitoring/dashboards/import/grafana/commit` | Commit a reviewed dashboard import. |
| `GET` | `/api/v1/monitoring/dashboards/import/grafana/support-matrix` | Supported Grafana constructs and limits. |

### 14. Inspect Entitlements And Import External Results

Entitlements are a read-only self-hosted projection of enabled bundles, configured limits, and current usage. They do not implement a licensing engine or enforce limits.

| Method | Path | Purpose |
| --- | --- | --- |
| `GET` | `/api/v1/system/entitlements` | Edition, protocol bundles, and configured limits. |
| `GET` | `/api/v1/system/entitlements/usage` | Deployment-wide usage against the projection. |
| `GET` | `/api/v1/projects/{id}/entitlements/usage` | Project-scoped usage projection. |
| `POST` | `/api/v1/results/import` | Import a supported external result envelope. |
| `GET` | `/api/v1/results/imports` | List external-result import records in scope. |
| `GET` | `/api/v1/reports/profiles` | Supported report profiles and output capabilities. |
| `GET` | `/api/v1/runtime-settings/support-matrix` | Runtime-setting implementation and protocol applicability. |

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
