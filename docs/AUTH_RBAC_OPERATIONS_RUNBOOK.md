# Auth and RBAC Operations Runbook

This runbook covers the self-hosted operational paths for APEX Load auth, RBAC, service accounts, audit evidence, and emergency access recovery.

## Supported Production Modes

APEX Load supports three auth modes:

- `dev`: local development bypass. Use only for local development or a controlled break-glass recovery window.
- `trusted_headers`: production mode for an upstream gateway that owns browser sign-in and forwards identity or role headers.
- `oidc`: native APEX Load OIDC login, callback, signed local session, logout, and claim mapping.

Recommended default:

- Use `trusted_headers` when Microsoft Entra ID, SAML, or another identity gateway already protects the ingress.
- Use `oidc` when APEX Load should own the browser login redirect and local dashboard session.
- Do not run shared production controllers in `dev` mode except during an approved break-glass window.

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

The preview headers are ignored when auth mode is `trusted_headers` or `oidc`; production identity and RBAC always come from the configured gateway or OIDC session.

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
- Assign token scopes as a second boundary. Use `automation:*` only for broad CI controllers; prefer narrower scopes such as `results:read`, `tests:run`, `scripts:write`, `system:read`, `settings:manage`, `service_accounts:manage`, or `audit:read` for single-purpose jobs.
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
