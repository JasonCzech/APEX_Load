# Auth and RBAC Operations Runbook

This runbook covers the self-hosted operational paths for APEX Load auth, RBAC, service accounts, audit evidence, and emergency access recovery.

## Supported Production Modes

APEX Load supports four auth modes:

- `dev`: local development bypass. Use only for local development or a controlled break-glass recovery window.
- `local`: fixed-credential, database-backed test personas for non-production browser and packaged desktop testing.
- `trusted_headers`: production mode for an upstream gateway that owns browser sign-in and forwards identity or role headers.
- `oidc`: native APEX Load OIDC login, callback, signed local session, logout, and claim mapping.

Recommended default:

- Use `trusted_headers` when Microsoft Entra ID, SAML, or another identity gateway already protects the ingress.
- Use `oidc` when APEX Load should own the browser login redirect and local dashboard session.
- Use `local` only on non-production controllers when persona switching and authorization testing are required. The controller rejects it whenever production posture is enabled.
- Do not run shared production controllers in `dev` mode except during an approved break-glass window.

## Local Test Login

Select **Local Test Login** in Settings, save, and restart the controller. The landing page then accepts these seeded identities:

| Credential | Initial access |
| --- | --- |
| `admin/admin` | Global `system_admin` |
| `domainadmin/domainadmin` | Global `viewer`; `project_admin` for Default Domain |
| `user/user` | Global `viewer`; viewer for the default domain, repository, and project |
| `dev/dev` | Global `viewer`; editor for the default domain, repository, and project |

Use Access to disable identities or alter their bindings; startup seeding does not overwrite those edits. Add more domain bindings to make `domainadmin` responsible for a domain list. Deleting a fixture recreates it on the next Local Test Login startup. Every controller startup while Local Test Login is active invalidates all of its sessions, so the next dashboard load returns to the persona login even if the browser still has an old cookie. Logout for any persona, including `dev`, requires the CSRF token returned by `/api/v1/auth/me`, deletes the persisted session, clears the browser cookie or desktop capability binding, and returns to the persona login.

## Native OIDC Bootstrap Checklist

Before switching `auth.mode` to `oidc`:

1. Register the APEX Load dashboard as an OIDC client in the identity provider.
2. Register the callback URL exactly as served by the controller, usually `https://apexload.example.com/api/v1/auth/callback`.
3. Set the client secret in the environment variable named by `auth.client_secret_env_var`, default `APEXLOAD_OIDC_CLIENT_SECRET`.
4. Set a strong session-signing secret in the environment variable named by `auth.session_secret_env_var`, default `APEXLOAD_AUTH_SESSION_SECRET`.
5. Configure `issuer_url`, `client_id`, `redirect_url`, scopes, and claim names in Settings.
6. Open Settings -> General -> Auth Diagnostics and confirm required checks pass.

The dashboard includes Microsoft Entra ID and Generic OIDC presets. Presets intentionally do not fill issuer URL, client ID, or redirect URL because those values are deployment-specific.

## Trusted Header Gateway Contract

For `trusted_headers`, the authenticating gateway must strip any inbound spoofed auth headers before adding its trusted values.

### Trusted-proxy gate (defense-in-depth)

Stripping at the gateway is necessary but not sufficient: if the controller port is ever reachable
directly (a port-forward, a NetworkPolicy gap, an SSRF pivot), a client can forge `X-Auth-Request-User`.
Configure a trusted-proxy gate so identity headers are honored only from the real ingress:

- `auth.trusted_proxy_cidrs`: list of CIDRs (or bare IPs) the request's immediate peer (`RemoteAddr`)
  must fall within — typically your ingress/proxy pod CIDR. `X-Forwarded-For` is intentionally NOT
  trusted for this check (it is client-controlled).
- `auth.trusted_proxy_secret_env_var`: name of an environment variable holding a shared secret; the
  gateway must inject it as the `X-APEXLoad-Proxy-Secret` header (compared in constant time).

Configure at least one. **In production (`APEXLOAD_ENV=production`) the controller refuses to start in
`trusted_headers` mode unless a gate is configured.** In non-production it starts with a loud warning.
Auth Diagnostics shows a `Trusted Proxy Gate` check (pass / warn / fail).

Default required headers:

- `X-Auth-Request-User`: stable subject identifier.
- `X-APEXLoad-Roles`: comma or pipe separated global roles. Used only for first-admin bootstrap while the local user table is empty.
- `X-APEXLoad-Project-Roles`: semicolon separated project assignments, for example `checkout=viewer|editor;governance=project_admin`. Used only for first-admin bootstrap while the local user table is empty.

Optional identity headers:

- `X-Auth-Request-Name`
- `X-Auth-Request-Email`

**RBAC ownership (important):** APEX Load owns identity and RBAC as the source of truth once any
user exists. The forwarded role headers (`X-APEXLoad-Roles`, `X-APEXLoad-Project-Roles`) are only
honored to bootstrap the very first administrator while the user table is empty. After that, each
request's subject (`X-Auth-Request-User`) is matched to a local APEX Load user record, and that
user's roles are derived from APEX Load's **local role bindings** — the role headers are no longer
consulted for onboarded users. In other words, the gateway authenticates *who* the user is; APEX
Load decides *what* they may do. A subject with no local user record (after bootstrap) is rejected
as "not onboarded", so provision users and bindings in Access & API Keys before relying on the
gateway. APEX Load also owns service-account principals for automation. Provide at least one role
header so first-admin bootstrap can succeed.

## Local User And Role Binding Model

After bootstrap, APEX Load's local users and role bindings are the authorization source of truth for human users in both `trusted_headers` and `oidc` modes. The identity provider or gateway authenticates the subject; APEX Load decides access from persisted local bindings.

Operational rules:

- Onboard users from the Access page or `POST /api/v1/auth/users/onboard`, which creates the user and initial bindings atomically.
- A user that authenticates successfully but has no local user record is blocked as not onboarded.
- Disabled users and revoked OIDC session epochs are rejected by `/api/v1/auth/me` and protected routes.
- Scoped admins can only see and manage users whose bindings fall inside their manageable scopes.
- Binding scopes must already exist; nonexistent project, domain, or application-repository scopes are rejected.

Groups, SCIM provisioning, time-bound grants, custom roles, and short-lived client-credentials tokens are roadmap items, not part of the current RBAC ownership model.

## First-Administrator Bootstrap

When the user table is empty there is no admin to onboard anyone, so the first authenticated subject
must be granted access to break the chicken-and-egg. How that works depends on the environment:

- **Non-production** (`APEXLOAD_ENV` unset/not `production`): the first authenticated subject on an
  empty store is admitted with the roles the gateway/IdP asserts (convenient for local/staging
  first-run).
- **Production** (`APEXLOAD_ENV=production`): empty-store bootstrap is **disabled by default** — a
  subject with no local user record is rejected as "not onboarded". This prevents anyone who can reach
  the controller directly (e.g. a header-spoofing client if the gateway is misconfigured) from
  asserting `system_admin` against a fresh store. To bootstrap the first admin in production:
  - Set `APEXLOAD_ALLOW_BOOTSTRAP_ADMIN=true` to enable it.
  - Optionally set `APEXLOAD_BOOTSTRAP_SUBJECT=<exact subject>` to pin who may bootstrap (strongly
    recommended — only that subject is admitted).
  - The grant is **deterministic** (a fixed global `system_admin`, *not* the asserted roles),
    **persisted** (the user + binding are written so the window closes after the first request), and
    **audited** (`auth.bootstrap.admin_granted`).
  - Once the first admin exists, **unset `APEXLOAD_ALLOW_BOOTSTRAP_ADMIN`** and manage all further
    users/roles in Access & API Keys. (Alternatively, seed the first admin directly in the database
    and never enable bootstrap.)

## Local Dev Admin/User Preview

When the controller reports `auth.mode = dev`, the dashboard sidebar shows a dev-only Admin/User segmented control:

- `Admin` is the default and preserves local development bypass behavior.
- `User` sends `X-APEX Load-Dev-View: user` and the active project id as `X-APEX Load-Dev-Project-ID`.
- User preview simulates subject `dev-user` with global `viewer` and active-project `editor` roles.
- User preview keeps script, scenario, run, replay, and application authoring controls available for the active project while hiding system administration and project-governance controls.
- Backend RBAC still enforces protected APIs in User preview, returning normal `403` responses for admin-only operations.

The preview headers are ignored when auth mode is `local`, `trusted_headers`, or `oidc`; identity and RBAC then come from the configured local session, gateway, or OIDC session.

## Break-Glass Recovery

Use break-glass only when normal admin access is unavailable because of IdP, gateway, claim, or secret misconfiguration.

1. Restrict controller network access to trusted operators before changing auth settings.
2. Temporarily set `auth.mode` to `dev` through the settings API, Helm values, or the controller settings store.
3. Restart or reload the controller if your deployment model only reads settings on startup.
4. Sign in through the isolated access path and fix the broken OIDC, trusted-header, or RBAC claim configuration.
5. Export audit events for the recovery window.
6. Restore `auth.mode` to `trusted_headers` or `oidc`.
7. Confirm `/api/v1/auth/diagnostics` reports the production mode as ready.
8. Remove temporary network exceptions and record the incident in the external change system.

Never leave shared environments in `dev` mode after recovery. Service-account API keys remain available in production auth modes and should be used for automation instead of keeping dev bypass enabled.

## Service Accounts

Service accounts are APEX Load-owned automation principals. Create one per automation workflow instead of sharing user credentials.

Operational guidance:

- Assign the narrowest global and project roles that the automation needs.
- Assign token scopes as a second boundary. Use `automation:*` only for broad CI controllers; prefer narrower scopes such as `results:read`, `tests:run`, `scripts:write`, `system:read`, `settings:manage`, `deployments:manage`, `service_accounts:manage`, or `audit:read` for single-purpose jobs. Ordinary deployment maintenance-lease keys use `deployments:manage`; they do not need `settings:manage`. The exceptional active-test override uses a separate, expiring service account with both `deployments:manage` and the exact `deployments:override-active-tests` scope; wildcard scopes do not satisfy that conditional override check.
- Set an expiration date for temporary or vendor-owned automation.
- Use the rotation interval to make key-age review visible in Settings.
- Copy the API key immediately; it is returned only at creation or rotation time.
- Rotate keys after staff changes, CI secret exposure, or periodic policy windows.
- Disable stale accounts before deletion when you need a reversible pause.
- Use `last_used_at`, `expires_at`, `key_rotated_at`, and `rotation_due_at` in Settings to identify inactive, expired, or overdue automation principals.

API-key requests must satisfy both RBAC roles and token scope. For example, a key with `system_admin` but only `results:read` is still rejected from system settings APIs because the token scope does not cover `system:read` or `settings:manage`.

`apexctl` can authenticate to secured controllers with either explicit flags or environment variables:

- `--api-key <key>` or `APEXLOAD_API_KEY=<key>` sends `X-APEXLoad-API-Key`.
- `--bearer-token <token>` or `APEXLOAD_BEARER_TOKEN=<token>` sends `Authorization: Bearer <token>`.
- `--api-key` takes precedence over bearer-token auth when both are set.

These flags are supported by `run`, `status`, `wait`, `record browser`, and `github` controller-backed commands.

## Audit Evidence

APEX Load records governance events for settings changes, project administration, service-account lifecycle actions, auth session lifecycle events, and core test control actions.

Useful API paths:

- `GET /api/v1/auth/audit-events`: filtered JSON feed.
- `GET /api/v1/auth/audit-events/export`: filtered CSV export.

Common filters:

- `action=auth.service_account.rotate`
- `actor_type=service_account`
- `status=failure`
- `project_id=<project-id>`
- `resource_type=system_settings`

Dashboard path:

1. Open Settings -> General.
2. Use the Recent Audit Events filters.
3. Click Export CSV to download the currently filtered evidence set.

Keep exported audit CSV files with the external incident, change, or release record when auth or RBAC configuration changes are part of the work.

## Common Recovery Checks

- Missing OIDC secret: confirm `APEXLOAD_OIDC_CLIENT_SECRET` or the configured secret env var is present on the controller pod.
- Invalid session secret: rotate `APEXLOAD_AUTH_SESSION_SECRET` only during a planned session invalidation window.
- Issuer discovery failure: confirm controller egress to the issuer and that `issuer_url` points at the provider metadata root.
- Missing trusted-header identity: open the dashboard through the protected ingress, not the raw controller origin.
- Missing RBAC roles: verify the subject has local APEX Load role bindings in Access. Upstream role claims/headers do not grant access after first-admin bootstrap.

## Open work / backlog

Source note: condensed 2026-09-24 from the former `AUTH_RBAC_BACKLOG.md`. Completed work (Phases 1-3, Phase-4 B1/B2/A17, the A16 utility extraction, A18 atomic user onboarding, C11 effective-access simulator, C12 service-account owner metadata, C4-MVP access-review report, A5 bootstrap admin, A15 API-key rate-limit fallback, B3 broad-scope deprecation) is not listed here. Everything below is deferred. Anchors are symbol names; line numbers drift, so search by symbol. Routes named here that do not exist yet are proposals, described in prose rather than as route references.

Effort: S (half a day or less), M (1-2 days), L (about a week), XL (multi-week). Status: TODO (do when prioritized), DEMAND-GATED (build when a deal or user needs it), WONT-DO (decided against; kept so it is not re-raised).

| ID | Item | Effort | Status | Depends on |
| --- | --- | --- | --- | --- |
| A16b | Split `AccessPage.tsx` into workspace components | L | TODO | Utilities already extracted |
| A14 | Explicit service-account scopes and freshness enforcement | S/M | TODO (low value) | Route-catalog scope foundation (shipped) |
| A20 | Usage-accounting ramp/abort accuracy and archive-bytes cache | M | TODO | ManagedTest schema |
| A19 | Unify audit views (Settings vs Access) | - | WONT-DO | - |
| A21 | Move logout CSRF token out of the status body | - | WONT-DO | - |
| A22 | Per-session GitHub identity in bypass mode | - | WONT-DO | - |
| C1 | Group/team RBAC (IdP-claim mapping, then native groups) | L | TODO | - |
| C7 | Bulk binding / CSV import | M | TODO | C1 (native groups optional) |
| C3b | Full session-management console | L | TODO | Phase-2 `session_epoch` MVP (shipped) |
| C2 | SCIM 2.0 provisioning and deprovisioning | XL | DEMAND-GATED | C1 |
| C4b | Access-recertification campaigns | M+ | TODO | C4-MVP report (shipped) |
| C5 | Time-bound / JIT grants and break-glass | M | TODO | - |
| C6 | Audit retention policy and SIEM/webhook streaming | M | TODO | Phase-2 retention (shipped) |
| C8 | Per-account rate limits and IP allowlist for service accounts | M | TODO | - |
| C9 | Custom / cloneable roles with editable permission matrix | XL | DEMAND-GATED | B1 (shipped) |
| C10 | Guided "Connect identity provider" wizard | M | TODO | - |

### Maintainability

**A16b - split `AccessPage.tsx`.** The safe half of A16 shipped (types, utilities and `SortHeader.tsx` extracted), but `AccessPage.tsx` has grown back to about 2,760 lines. The remaining split into `UsersWorkspace`, `ServiceAccountsWorkspace` and `ActivityWorkspace` is a deep state restructure with regression risk and should be its own reviewable change.

- Approach: parent owns shared data (`users`, `accounts`, `usersTotal`, `activityEvents`, `activityUsage`, `accessReview`), their loaders, the tab bar, role/scope filter options and the `accessReviewAttention` memo; each workspace owns only UI state and rendering. Extract one workspace at a time (Activity, then Service Accounts, then Users), keeping `AccessPage.test.tsx` green after each step, and reduce `AccessPage` to a thin tab shell plus permission gate.
- Cross-tab coupling: the attention badge derives from `accessReview` (keep it and its load/refresh effects in the parent); the simulator stays in the Activity workspace; the Service Accounts attention banner needs `accessReviewAttention` plus an open-review callback; tab counts need the parent to own the data arrays; `loadActivity` moves with `activityFilter` if that state moves, with the parent receiving events via a callback.
- Acceptance: `AccessPage.tsx` under about 450 lines, three workspace files, all AccessPage/Settings tests pass unchanged (they query `.access-tab`, `.access-workspace[aria-label=...]`, `.access-inspector`, `.access-attention-badge`, `.access-simulator`, so class names and aria-labels must be preserved), tsc and eslint clean, full vitest green.
- Anchors: `dashboard/src/components/AccessPage.tsx`, `accessUtils.ts`, `accessTypes.ts`, `AccessPage.test.tsx`.

### Low-severity polish

**A14 - explicit service-account scopes (low value).** The route-catalog scope foundation shipped (`ServiceAccountScope` on `accessRequirement`, `withRouteServiceAccountScope`, catalog-derived `requiredServiceAccountScope` before the legacy prefix fallback). Service-account scopes are a second gate after the RBAC role check, so a coarse default can only fail to narrow, never escalate. Remaining: many routes still inherit scopes from the legacy prefix table. Convert them to explicit declarations where useful, add a freshness test that every service-account-reachable route has an explicit scope or an intentional fallback, then decide whether the non-catalog catch-all should fail closed. Anchors: `pkg/api/route_catalog.go`, `pkg/api/auth_resolution.go` (`requiredServiceAccountScope`, `serviceAccountAllowsRequest`, `normalizeAuthenticatedServiceAccountScopes`), `pkg/api/middleware.go` (`grantIfScopeAllows`).

**A20 - usage-accounting accuracy and archive-bytes cache.** `usageAccountingFromTests` bills total VUsers times planned duration, ignoring ramp and early abort (a 1,000-VU, 10-minute test aborted at 2 minutes with 200 VUs bills 600k VU-seconds, not 24k). Fixing this needs the run record to capture provisioned VUs and actual duration, a schema change with historical-reconciliation implications. Separately, `usageArchiveBytes` walks the filesystem on every usage-accounting request. Approach: capture ramp/abort state and bill provisioned times actual; cache archive bytes in run metadata and recompute only on archive finalization (independent, safe to do first). Anchors: `pkg/api/usage_accounting.go`.

**Won't do (do not re-raise):**

- A19 unify audit views: Settings audit (global governance, action/actor/status filters, limit 20) and Access Activity (scoped user activity, actor/domain/project/time filters, limit 50) serve different audiences on purpose.
- A21 logout CSRF token in the status body: the token is HMAC-signed, user-bound, 10-minute TTL and returned in the authenticated status response for resubmission on logout, the standard SPA double-submit pattern. An `HttpOnly` cookie would be contradictory because frontend JS must read it. Anchors: `handleGetAuthStatus`, `issueOIDCLogoutCSRFToken`.
- A22 per-session GitHub identity in bypass mode: `githubUserKey` returns `"local"` only when no user is authenticated (dev-bypass, single operator); per-session keying would force GitHub re-auth every session. Production paths key by real subject/ID. Anchor: `pkg/api/github_oauth.go`.
- Refuted during the original audit: GitHub project-policy/sync mutations are audited; there is no project/domain owner model to transfer.

### Enterprise-governance roadmap (Phase 5)

Dependency-ordered; C1 unlocks the most.

- **C1 - group/team RBAC (L, foundation for C2 and C7).** `UserRoleBinding` is keyed only by `UserID`, so onboarding a team means N x M manual bindings. Form (a), recommended first: map OIDC/trusted-header group claims to roles during `applyLocalUserBindings`. Form (b): native admin-managed groups (`storage.Group` plus `GroupRoleBinding`) with effective access as the union of direct and group bindings. Acceptance: group grants resolve in effective access and enforcement; admin UI for form (b). Anchors: `pkg/api/auth_resolution.go` (`applyLocalUserBindings`, `authenticatedUserFromStoredUser`), `pkg/storage`.
- **C7 - bulk binding / CSV import (M).** Proposed: a bulk-binding endpoint under the auth API (not yet implemented, so not a catalogued route) that accepts a list or parsed CSV (`subject,scope_type,scope_id,role`), applies it transactionally with a per-row result, plus bulk-assign and CSV upload on the onboard sub-tab. Additive; pairs well with C1.
- **C3b - session-management console (L).** Phase-2 per-user `session_epoch` "revoke all sessions" shipped. Remaining: a storage-backed session registry (id, subject, issued/last-seen, ip, user agent, revoked_at) checked per request, a per-session revoke action plus revoke-all-for-user, and a Sessions sub-tab under Activity. Anchors: `pkg/api/auth_resolution.go`, `pkg/storage`.
- **C2 - SCIM 2.0 provisioning (XL, demand-gated, needs C1).** Unknown subjects are rejected today, but leavers keep a valid local identity until manually disabled (sharpest in trusted-header mode). Implement SCIM Users and Groups endpoints (token-auth, SystemAdmin-gated) onto `User` and `Group`; deprovision calls `UpdateUserActive(false)`.
- **C4b - recertification campaigns (M+).** Layer `AccessReviewCampaign` and `AccessReviewItem` (generated from current bindings) over the shipped report in `pkg/api/access_review.go`; reviewers keep or revoke (revoke reuses `DeleteUserRoleBinding`), decisions are audit events, plus a Reviews sub-tab.
- **C5 - time-bound / JIT grants and break-glass (M).** Add optional `ExpiresAt` to `UserRoleBinding` (mind the Postgres `rewriteSQL` bool/datetime gotcha) and filter expired bindings during effective-access resolution; add self-service elevation requests with auto-expiry and audit, and a pre-created expiring break-glass SystemAdmin grant.
- **C6 - audit retention and SIEM/webhook streaming (M).** Retention prune shipped (`APEXLOAD_AUDIT_RETENTION_DAYS`). Add an `AuditExportTarget` setting (webhook/syslog plus secret) and emit each `SaveAuditEvent` asynchronously, reusing the GitHub webhook delivery-log pattern for retry and visibility. Anchors: `pkg/api/audit_retention.go`, `pkg/api/github_enterprise.go`.
- **C8 - per-account rate limits and IP allowlist (M).** Add `RateLimitPerMinute` and `AllowedCIDRs []string` to `storage.ServiceAccount`; enforce per account (preferred over the global default) and the CIDR check in the auth path beside the scope check. Anchors: `pkg/storage` (`ServiceAccount`), `pkg/api/auth_resolution.go`, `pkg/api/middleware.go`.
- **C9 - custom / cloneable roles (XL, demand-gated).** Introduce `storage.Role` keyed to access-matrix resource/action IDs, have `authorizeAccessRequirement` consult role permissions instead of fixed strings, and seed the four built-ins as immutable cloneable roles. B1 (matrix-to-route consistency test plus `authAccessMatrix`) is the shipped prerequisite. Defer until a deal needs it.
- **C10 - guided "Connect identity provider" wizard (M).** Preset, then issuer/client/redirect (live URL validation exists), a pre-save diagnostics test against draft values, confirmation and copy-to-clipboard callback URL. Enhancement; the plumbing exists. Anchors: `dashboard/src/components/SettingsPage.tsx`, `pkg/api/handlers_auth.go` (`handleGetAuthDiagnostics`).

### Cross-cutting conventions for backlog work

- New routes: add to `pkg/api/route_catalog.go` (resolver plus `accessRequirement`) and `server.go`; run `go generate ./pkg/api` (`TestSwaggerYAMLIsFresh`); add the golden fingerprint to `accessRequirementGolden` in `route_catalog_characterization_test.go`. Public (no-auth) routes go in `intentionallyPublicAPIRoutes` in `route_coverage_test.go`, not the route catalog.
- New SQLite columns/tables: the Postgres schema is runtime-translated by `rewriteSQL()`. TEXT columns pass through; INTEGER-bool and DATETIME columns need coordinated `rewriteSQL` entries (CREATE, ALTER, comparisons) or Postgres breaks. New indexes go in `schemaIndexes`.
- New Store interface methods: update the test doubles (`metricsStoreSpy` in `runner_test.go`) or `pkg/api` will not build.
- Periodic/background jobs (retention, notifiers, prune) run only on the HA execution leader (`isExecutionLeader()`, reuse the `audit_retention.go` cadence pattern).
- WebSocket admin signals: the hub `Client` has no identity beyond `testID`/`projectID`. Use `broadcast_all` only for content-free global signals (it no-ops if the message carries a `test_id`/`project_id`); detailed admin payloads need per-client identity routing, which is not built.
- Frontend: lifecycle/admin API calls live in `dashboard/src/hooks/api/useSystemApi.ts`; UI gating reads the effective-access endpoint and `isEffectiveOperationAllowed` (default-deny). Run the full `vitest` suite when touching cross-cutting auth/permission defaults, and verify dashboard changes in a browser, not just `tsc`.
