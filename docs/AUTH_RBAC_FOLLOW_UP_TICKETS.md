# Auth / RBAC Follow-Up Tickets

This document turns the remaining 3.1 platform-gap work into issue-ready tickets after the native OIDC implementation pass. The current codebase now supports:

- `dev` bypass mode
- `trusted_headers` mode
- `oidc` mode with local login, callback, logout, signed session cookies, and RBAC claim mapping
- service-account API keys
- audit coverage for service-account lifecycle changes and OIDC login/logout events

The remaining backlog is mostly about hardening, lifecycle ownership, and architectural decisions rather than missing foundational RBAC primitives.

## Audit Snapshot

- Implemented claims for multi-tenant governance, notifications, schedules, provenance, analysis depth, and ecosystem integration still line up with code.
- The material remaining auth gap is not basic sign-in anymore; it is long-term identity ownership and operational hardening.
- Native OIDC logout now uses an explicit `POST /api/v1/auth/logout` flow with CSRF proof from the dashboard rather than a plain top-level `GET`, which closes the most obvious session-mutation gap from the first implementation pass.

## Ticket 1: Harden OIDC Logout and Session CSRF Boundaries

- Priority: High
- Problem: Native OIDC logout currently clears the Stormrunner session through `GET /api/v1/auth/logout`. The route is simple and functional, but it is still easier to trigger accidentally or cross-site than an explicit authenticated state-changing POST flow.
- Scope:
  - Change logout from GET to POST for interactive session termination.
  - Add CSRF protection for logout and any future session-mutating auth routes.
  - Keep the current post-logout redirect behavior after the local session is cleared.
  - Update dashboard sign-out UI to call the new logout flow instead of relying on a plain link.
- Acceptance Criteria:
  - Logout requires an explicit non-idempotent request.
  - Requests missing the CSRF proof fail cleanly.
  - Dashboard sign-out still clears the local Stormrunner session and then redirects to the configured post-logout destination.
  - Regression tests cover success and rejection paths.
- Status: Implemented for logout. Future session-mutating auth endpoints should reuse the same CSRF posture rather than reintroducing link-triggered state changes.

## Ticket 2: Expand OIDC Runtime Health and Misconfiguration Diagnostics

- Priority: High
- Problem: Stormrunner now exposes a first-pass auth diagnostics surface for trusted-header and native OIDC readiness, but operators still need broader runtime and post-login diagnostics before the auth lane feels fully production-hardened.
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

## Ticket 3: Decide and Implement Long-Term Role-Binding Ownership

- Priority: High
- Problem: Stormrunner now supports role claims from trusted headers, service accounts, and OIDC tokens, but it still does not own a first-class user/group binding model. That keeps RBAC dependent on upstream claim shaping.
- Decision Required:
  - Option A: Keep user and group lifecycle upstream-owned and document claim contracts as the intended final architecture.
  - Option B: Add Stormrunner-owned user/group-to-role bindings with persistence and admin workflows.
- Scope if Option A is chosen:
  - Document the supported claim contract and examples for Entra ID or other IdPs.
  - Add validation or diagnostics for required claims.
  - Treat Stormrunner as a policy consumer, not an identity source.
- Scope if Option B is chosen:
  - Add persisted user/group/role binding tables.
  - Add admin APIs and dashboard workflows for mapping identities to RBAC roles.
  - Define merge/precedence rules between upstream claims, local bindings, and service-account roles.
- Acceptance Criteria:
  - The product has an explicit, documented ownership model for RBAC identity data.
  - If local bindings are implemented, they are persisted, auditable, and test-covered.
  - If upstream ownership is retained, the supported claim contract is documented and validated.

## Ticket 4: Add OIDC Session Refresh and Expiry UX

- Priority: Medium
- Problem: The current native OIDC session is intentionally simple. It signs a local session cookie from the verified ID token, but it does not support refresh tokens, silent renewal, or proactive expiry UX.
- Scope:
  - Decide whether Stormrunner should support refresh-token-backed session renewal.
  - If refresh tokens remain out of scope, add clearer expiry handling and reauthentication prompts.
  - Surface imminent session expiry in the dashboard so users do not hit sudden 401s during active work.
- Acceptance Criteria:
  - Expired sessions fail predictably and guide the user back into sign-in.
  - Operators understand whether the platform supports renewal or requires explicit reauthentication.
  - Tests cover expired-session behavior and the chosen renewal strategy.

## Ticket 5: Evaluate Native SAML Support vs. Explicit Non-Goal

- Priority: Medium
- Problem: The original gap plan grouped OIDC and SAML together. Stormrunner now has native OIDC, but SAML remains unimplemented.
- Scope:
  - Decide whether SAML becomes a first-class runtime mode.
  - If not, document trusted-header gateway integration as the supported path for SAML-backed enterprises.
  - If yes, define metadata, ACS, certificate, logout, and claim-mapping requirements before implementation begins.
- Acceptance Criteria:
  - The roadmap explicitly says whether SAML is supported natively, indirectly through gateway integration, or intentionally out of scope.
  - The plan no longer implies parity without a product decision.

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
