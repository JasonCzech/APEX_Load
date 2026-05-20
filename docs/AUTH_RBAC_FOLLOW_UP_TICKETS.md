# Auth / RBAC Follow-Up Tickets

This document records the Workstream 4 Auth/RBAC follow-up decisions made after the native OIDC implementation pass. The planned W4 scope is now complete; future changes should be treated as release hardening or enterprise-directory expansion rather than missing foundational auth/RBAC work. The current codebase supports:

- `dev` bypass mode
- `trusted_headers` mode
- `oidc` mode with local login, callback, logout, signed session cookies, and RBAC claim mapping
- service-account API keys
- audit coverage for service-account lifecycle changes plus OIDC login, logout, and representative callback failure events
- filtered audit CSV export for operator evidence packs and incident review

The original follow-up backlog has been resolved for the planned parity phase. The durable product decisions are: trusted headers remain the recommended default when an upstream gateway owns sign-in, native OIDC is the direct-auth option, human identity lifecycle stays upstream-owned, native SAML is handled through trusted-header gateways, and APEX Load owns service-account principals for automation.

## Audit Snapshot

- Implemented claims for multi-tenant governance, notifications, schedules, provenance, analysis depth, and ecosystem integration still line up with code.
- Basic sign-in, RBAC enforcement, service-account governance, audit evidence, and the long-term identity ownership decision are complete for Workstream 4.
- Native OIDC logout now uses an explicit `POST /api/v1/auth/logout` flow with CSRF proof from the dashboard rather than a plain top-level `GET`, which closes the most obvious session-mutation gap from the first implementation pass.
- Native OIDC sessions now report active, missing, invalid, or expired state through `/api/v1/auth/me`, and the dashboard warns users before short-lived sessions expire instead of letting them discover expiry through abrupt protected-route failures.
- Audit evidence can now be exported from `GET /api/v1/auth/audit-events/export` or the dashboard settings audit panel with the same action, actor type, and status filters used by the recent event feed.

## Ticket 1: Harden OIDC Logout and Session CSRF Boundaries

- Priority: High
- Problem: Native OIDC logout currently clears the APEX Load session through `GET /api/v1/auth/logout`. The route is simple and functional, but it is still easier to trigger accidentally or cross-site than an explicit authenticated state-changing POST flow.
- Scope:
  - Change logout from GET to POST for interactive session termination.
  - Add CSRF protection for logout and any future session-mutating auth routes.
  - Keep the current post-logout redirect behavior after the local session is cleared.
  - Update dashboard sign-out UI to call the new logout flow instead of relying on a plain link.
- Acceptance Criteria:
  - Logout requires an explicit non-idempotent request.
  - Requests missing the CSRF proof fail cleanly.
  - Dashboard sign-out still clears the local APEX Load session and then redirects to the configured post-logout destination.
  - Regression tests cover success and rejection paths.
- Status: Implemented for logout. Future session-mutating auth endpoints should reuse the same CSRF posture rather than reintroducing link-triggered state changes.

## Ticket 2: Expand OIDC Runtime Health and Misconfiguration Diagnostics

- Priority: High
- Problem: APEX Load exposes auth diagnostics for trusted-header and native OIDC readiness. Future operator experience can improve with provider-specific runtime and post-login troubleshooting if pilot deployments reveal repeated IdP-specific failure patterns.
- Scope:
  - Build on the existing diagnostics surface that already checks environment-variable presence, URL validity, trusted-header mappings, issuer discovery reachability, and current request/session status without exposing secrets.
  - Add deeper post-login diagnostics for callback/session/cookie failures.
  - Surface OIDC readiness in system settings or health output.
  - Return actionable error messages for common runtime misconfiguration cases.
  - Keep dashboard visibility strong enough that operators can distinguish config errors from ordinary unauthenticated requests.
- Acceptance Criteria:
  - Missing OIDC env vars are detectable before first login attempt.
  - Discovery failures are surfaced as operator-readable diagnostics.
  - Dashboard settings clearly show whether OIDC is configured, reachable, and ready.
  - Tests cover missing env vars, discovery failure, and healthy configuration.
- Status: Implemented for configuration readiness, issuer discovery, current-session state, and expired/invalid local-session diagnostics. Remaining future depth is provider-specific callback troubleshooting beyond the generic safe audit details now captured for failures.

## Ticket 3: Decide and Implement Long-Term Role-Binding Ownership

- Priority: High
- Problem: APEX Load now supports role claims from trusted headers, service accounts, and OIDC tokens, but it still does not own a first-class user/group binding model. That keeps RBAC dependent on upstream claim shaping.
- Decision Required:
  - Option A: Keep user and group lifecycle upstream-owned and document claim contracts as the intended final architecture.
  - Option B: Add APEX Load-owned user/group-to-role bindings with persistence and admin workflows.
- Scope if Option A is chosen:
  - Document the supported claim contract and examples for Entra ID or other IdPs.
  - Add validation or diagnostics for required claims.
  - Treat APEX Load as a policy consumer, not an identity source.
- Scope if Option B is chosen:
  - Add persisted user/group/role binding tables.
  - Add admin APIs and dashboard workflows for mapping identities to RBAC roles.
  - Define merge/precedence rules between upstream claims, local bindings, and service-account roles.
- Acceptance Criteria:
  - The product has an explicit, documented ownership model for RBAC identity data.
  - If local bindings are implemented, they are persisted, auditable, and test-covered.
  - If upstream ownership is retained, the supported claim contract is documented and validated.
- Decision: Option A. APEX Load remains a policy consumer for human users. User and group lifecycle stays upstream-owned through trusted-header gateways or native OIDC claim shaping, while APEX Load owns service-account principals for automation. The supported claim contract is `roles` for global roles and `project_roles` for project-scoped role mappings unless configured otherwise in system settings.
- Status: Implemented as an explicit product decision. Native user/group binding tables and admin workflows are intentionally deferred until there is a concrete enterprise requirement that upstream IdP claim shaping cannot satisfy.

## Ticket 4: Add OIDC Session Refresh and Expiry UX

- Priority: Medium
- Problem: The current native OIDC session is intentionally simple. It signs a local session cookie from the verified ID token, but it does not support refresh tokens, silent renewal, or proactive expiry UX.
- Scope:
  - Decide whether APEX Load should support refresh-token-backed session renewal.
  - If refresh tokens remain out of scope, add clearer expiry handling and reauthentication prompts.
  - Surface imminent session expiry in the dashboard so users do not hit sudden 401s during active work.
- Acceptance Criteria:
  - Expired sessions fail predictably and guide the user back into sign-in.
  - Operators understand whether the platform supports renewal or requires explicit reauthentication.
  - Tests cover expired-session behavior and the chosen renewal strategy.
- Decision: Refresh-token-backed silent renewal is out of scope for the current control plane. APEX Load signs a bounded local session from the verified ID token and requires explicit reauthentication after expiry.
- Status: Implemented for explicit expiry reporting and dashboard warning UX. `/api/v1/auth/me` reports `session_status`, `session_expires_at`, and `session_ttl_seconds` for native OIDC sessions, and the dashboard shows imminent-expiry guidance.

## Ticket 5: Evaluate Native SAML Support vs. Explicit Non-Goal

- Priority: Medium
- Problem: The original gap plan grouped OIDC and SAML together. APEX Load now has native OIDC, but SAML remains unimplemented.
- Scope:
  - Decide whether SAML becomes a first-class runtime mode.
  - If not, document trusted-header gateway integration as the supported path for SAML-backed enterprises.
  - If yes, define metadata, ACS, certificate, logout, and claim-mapping requirements before implementation begins.
- Acceptance Criteria:
  - The roadmap explicitly says whether SAML is supported natively, indirectly through gateway integration, or intentionally out of scope.
  - The plan no longer implies parity without a product decision.
- Decision: Native SAML is not a first-class APEX Load runtime mode for this parity phase. Enterprises that require SAML should terminate SAML at an upstream gateway and forward identity through the supported trusted-header contract.
- Status: Implemented as a roadmap decision; no SAML ACS, metadata, certificate, or native logout endpoints are planned in Workstream 4.

## Ticket 6: Expand Auth-Audit Coverage for Session Lifecycle and Failures

- Priority: Medium
- Problem: OIDC login/logout success paths are now audited, but failure-mode coverage is still thin.
- Scope:
  - Audit rejected callback attempts, nonce/state mismatches, and token verification failures at an operator-useful level.
  - Avoid leaking secrets, raw tokens, or authorization codes into audit payloads.
  - Add filters or views that make session lifecycle events easy to inspect during incident review.
- Acceptance Criteria:
  - Failed interactive auth attempts produce safe, queryable audit records.
  - Sensitive auth material is never persisted in audit details.
  - Tests cover both success and representative failure paths.
- Status: Implemented for representative OIDC callback failures. The audit event `auth.oidc.callback` records safe failure reasons such as state mismatch without persisting authorization codes, raw tokens, client secrets, or signed cookie material.
