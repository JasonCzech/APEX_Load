# Auth and RBAC Operations Runbook

This runbook covers the self-hosted operational paths for Stormrunner auth, RBAC, service accounts, audit evidence, and emergency access recovery.

## Supported Production Modes

Stormrunner supports three auth modes:

- `dev`: local development bypass. Use only for local development or a controlled break-glass recovery window.
- `trusted_headers`: production mode for an upstream gateway that owns browser sign-in and forwards identity or role headers.
- `oidc`: native Stormrunner OIDC login, callback, signed local session, logout, and claim mapping.

Recommended default:

- Use `trusted_headers` when Microsoft Entra ID, SAML, or another identity gateway already protects the ingress.
- Use `oidc` when Stormrunner should own the browser login redirect and local dashboard session.
- Do not run shared production controllers in `dev` mode except during an approved break-glass window.

## Native OIDC Bootstrap Checklist

Before switching `auth.mode` to `oidc`:

1. Register the Stormrunner dashboard as an OIDC client in the identity provider.
2. Register the callback URL exactly as served by the controller, usually `https://stormrunner.example.com/api/v1/auth/callback`.
3. Set the client secret in the environment variable named by `auth.client_secret_env_var`, default `STORMRUNNER_OIDC_CLIENT_SECRET`.
4. Set a strong session-signing secret in the environment variable named by `auth.session_secret_env_var`, default `STORMRUNNER_AUTH_SESSION_SECRET`.
5. Configure `issuer_url`, `client_id`, `redirect_url`, scopes, and claim names in Settings.
6. Open Settings -> General -> Auth Diagnostics and confirm required checks pass.

The dashboard includes Microsoft Entra ID and Generic OIDC presets. Presets intentionally do not fill issuer URL, client ID, or redirect URL because those values are deployment-specific.

## Trusted Header Gateway Contract

For `trusted_headers`, the authenticating gateway must strip any inbound spoofed auth headers before adding its trusted values.

Default required headers:

- `X-Auth-Request-User`: stable subject identifier.
- `X-Stormrunner-Roles`: comma or pipe separated global roles.
- `X-Stormrunner-Project-Roles`: semicolon separated project assignments, for example `checkout=viewer|editor;governance=project_admin`.

Optional identity headers:

- `X-Auth-Request-Name`
- `X-Auth-Request-Email`

At least one role header is required. Human user and group lifecycle remains upstream-owned; Stormrunner consumes the resulting claims and owns service-account principals for automation.

## Local Dev Admin/User Preview

When the controller reports `auth.mode = dev`, the dashboard sidebar shows a dev-only Admin/User segmented control:

- `Admin` is the default and preserves local development bypass behavior.
- `User` sends `X-Stormrunner-Dev-View: user` and the active project id as `X-Stormrunner-Dev-Project-ID`.
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

Service accounts are Stormrunner-owned automation principals. Create one per automation workflow instead of sharing user credentials.

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

## Audit Evidence

Stormrunner records governance events for settings changes, project administration, service-account lifecycle actions, auth session lifecycle events, and core test control actions.

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

- Missing OIDC secret: confirm `STORMRUNNER_OIDC_CLIENT_SECRET` or the configured secret env var is present on the controller pod.
- Invalid session secret: rotate `STORMRUNNER_AUTH_SESSION_SECRET` only during a planned session invalidation window.
- Issuer discovery failure: confirm controller egress to the issuer and that `issuer_url` points at the provider metadata root.
- Missing trusted-header identity: open the dashboard through the protected ingress, not the raw controller origin.
- Missing RBAC roles: verify the IdP or gateway emits `roles` or `project_roles`, or the configured claim/header alternatives.
