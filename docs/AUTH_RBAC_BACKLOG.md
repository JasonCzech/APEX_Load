# Admin / User / Account / RBAC — Backlog

> Consolidated backlog of **deferred** work from the Admin/User/Account/RBAC hardening program
> (the 5-phase evaluation that produced Phases 1–4). Phases 1–3, plus Phase-4 B1/B2/A17 and the
> A16 utility extraction, have shipped — see [AUTH_RBAC_OPERATIONS_RUNBOOK.md](AUTH_RBAC_OPERATIONS_RUNBOOK.md)
> and the program history. This file is what remains: one maintainability refactor, a few
> low-severity polish items, several explicit *won't-do*s (kept here so they aren't re-raised),
> and the full enterprise-governance roadmap (Phase 5).
>
> **How to use:** pick an item, read its *Approach* + *Acceptance*, follow the
> [Cross-cutting conventions](#cross-cutting-conventions) at the bottom (route catalog, swagger
> regen, Postgres `rewriteSQL`, HA-leader gating, FULL vitest). File anchors are accurate as of
> 2026-06-26 but line numbers drift — search by symbol.

## Status legend & effort

- **Effort:** S (≤½ day) · M (1–2 days) · L (~a week) · XL (multi-week)
- **Status:** `TODO` (do when prioritized) · `WONT-DO` (decided against, with reason) · `DEMAND-GATED` (build when a deal/user needs it)

## At a glance

| ID | Item | Effort | Status | Depends on |
|----|------|--------|--------|-----------|
| **A16b** | Split `AccessPage.tsx` into workspace components | L | TODO | — (utils already extracted) |
| **A14** | Explicit service-account scopes + freshness enforcement | S/M | TODO (low value) | route-catalog scope foundation |
| **A18** | Atomic user onboarding (user + bindings) | M | DONE | — |
| **A20** | Usage-accounting ramp/abort accuracy + archive-bytes cache | M | TODO | ManagedTest schema |
| **A19** | Unify audit views (Settings vs Access) | — | WONT-DO | — |
| **A21** | Move logout CSRF token out of status body | — | WONT-DO | — |
| **A22** | Per-session GitHub identity in bypass mode | — | WONT-DO | — |
| **C1** | Group/team RBAC (IdP-claim mapping → native groups) | L | TODO | — |
| **C7** | Bulk binding / CSV import | M | TODO | C1 (native groups optional) |
| **C3b** | Full session-management console | L | TODO | Phase-2 `session_epoch` MVP |
| **C2** | SCIM 2.0 provisioning + deprovisioning | XL | DEMAND-GATED | C1 |
| **C4b** | Access-recertification campaigns | M+ | TODO | C4-MVP report (shipped) |
| **C5** | Time-bound / JIT grants + break-glass | M | TODO | — |
| **C6** | Audit retention policy + SIEM/webhook streaming | M | TODO | Phase-2 retention (shipped) |
| **C8** | Per-account rate limits + IP allowlist for service accounts | M | TODO | — |
| **C9** | Custom / cloneable roles with editable permission matrix | XL | DEMAND-GATED | B1 (shipped) |
| **C10** | Guided "Connect identity provider" wizard | M | TODO | — |

---

## 1. Maintainability

### A16b — Split `AccessPage.tsx` into workspace components (L, TODO)

**Why deferred:** the safe half of A16 shipped — 683 lines (25%) of pure code were extracted into
`dashboard/src/components/accessTypes.ts`, `accessUtils.ts`, and `SortHeader.tsx`, dropping
`AccessPage.tsx` from 2745 → ~2090 lines. The remaining split into `UsersWorkspace` /
`ServiceAccountsWorkspace` / `ActivityWorkspace` is a deep state restructure (~1500 lines of
prop-threading) with real regression risk, best done as its own focused, reviewable change rather
than bundled with feature work.

**Approach (incremental, keep `AccessPage.test.tsx` green at every step):**
1. Decide the data-ownership model up front — the cleanest is **parent-owns-data**: `AccessPage`
   keeps the shared data state (`users`, `accounts`, `usersTotal`, `activityEvents`,
   `activityUsage`, `accessReview`) + their loaders, the tab bar (+ topbar contribution),
   `roleFilterOptions`/`scopeFilterOptions`, and the `accessReviewAttention` memo; each workspace
   owns only its **UI** state (filters, sort, selection, forms, inspector-collapse, action
   messages) and render.
2. Extract one workspace at a time (suggested order: `ActivityWorkspace` → `ServiceAccountsWorkspace`
   → `UsersWorkspace`), integrating + running tests after each.
3. Reduce `AccessPage` to a thin tab-shell + permission gate that renders the three children.

**Cross-tab coupling to handle (the reason it's not trivial):**
- The **attention badge** lives in the tab bar (parent) but derives from `accessReview` →
  keep `accessReview` + `loadAccessReview` + the eager-load/refresh-signal effects in the parent,
  pass `accessReview`/`accessReviewAttention` down.
- The **simulator** stays under Activity → Access Review (its state stays in `ActivityWorkspace`).
- The **attention banner** in the Service Accounts workspace needs `accessReviewAttention` (prop) +
  an `onOpenReview` callback (parent owns the tab switch).
- **Tab-bar counts** (`Users N`, `Service Accounts N`, `Activity N`) need each workspace's data
  length → parent owning the data arrays avoids lifting counts back up.
- `loadActivity` depends on `activityFilter` (UI state) — if `activityFilter` moves into
  `ActivityWorkspace`, `loadActivity` moves with it; then the parent gets `activityEvents` via a
  callback for the tab count.

**Acceptance:** `AccessPage.tsx` < ~450 lines; three workspace files; all 43 AccessPage/Settings
tests pass **unchanged** (they query `.access-tab`, `.access-workspace[aria-label=...]`,
`.access-inspector`, `.access-attention-badge`, `.access-simulator`, etc. — class names and
aria-labels MUST be preserved); tsc + eslint clean; FULL vitest green.

**Anchors:** `dashboard/src/components/AccessPage.tsx`, `accessUtils.ts`, `accessTypes.ts`,
`AccessPage.test.tsx`.

---

## 2. Low-severity polish

### A14 — Explicit service-account scopes + freshness enforcement (S/M, TODO — low value)

**Status:** The route-catalog scope foundation has shipped. `accessRequirement` carries
`ServiceAccountScope` / `ServiceAccountScopeLookup`, `routeAuth` applies
`withRouteServiceAccountScope`, and `requiredServiceAccountScope` prefers catalog-derived values
before the legacy prefix fallback.

**Why deferred:** SA scopes are a *second* authorization gate applied **after** the RBAC role check
(`serviceAccountAllowsRequest`), so a coarse default can only fail to *narrow*, never escalate.
What remains is the smaller drift-prevention tail: many routes still inherit scopes from the legacy
prefix table at catalog-build time instead of declaring explicit per-route scopes.

**Approach:** convert inherited scope defaults into explicit route declarations where useful, add a
freshness test that every SA-reachable route has either an explicit scope or an intentional fallback,
then decide whether the non-catalog catch-all should fail closed. **Acceptance:** scope requirements
are catalog-derived + test-enforced, no SA regressions. **Anchors:** `pkg/api/route_catalog.go`
(`withRouteServiceAccountScope`), `pkg/api/auth_resolution.go` (`requiredServiceAccountScope`,
`serviceAccountAllowsRequest`, `normalizeAuthenticatedServiceAccountScopes`),
`pkg/api/middleware.go` (`grantIfScopeAllows`).

### A18 — Atomic user onboarding (M, DONE)

**Status:** Implemented. `POST /api/v1/auth/users/onboard` creates the user and initial bindings in
one storage transaction, and the Access page uses that endpoint instead of a client-side binding loop.

**Acceptance:** a failed binding mid-onboard leaves no orphaned half-configured user; invalid
binding scopes return 400; duplicate subjects return 409. **Anchors:** `dashboard/src/components/AccessPage.tsx` (`createUser`),
`pkg/api/handlers_auth.go` (user + binding handlers), `pkg/storage` (transaction support).

### A20 — Usage-accounting accuracy + archive-bytes cache (M, TODO)

**Why deferred:** `usageAccountingFromTests` bills `TotalVUsers × planned DurationSeconds`, ignoring
ramp and early-abort (e.g. a 1000-VU/10-min test aborted at 2 min with 200 VUs bills 600k VU-s, not
24k). Fixing this needs the `ManagedTest`/run record to capture **provisioned** VUs + **actual**
duration — a schema/data change with historical-reconciliation implications. Separately,
`usageArchiveBytes` `filepath.WalkDir`s on every `getUsageAccounting` request.

**Approach:** (a) accuracy — capture ramp/abort state on the run record and bill provisioned×actual;
(b) perf — cache archive bytes in run metadata, recompute only on archive finalization (decoupled,
safe to do first). **Acceptance:** billed VU-seconds match observed load for ramped/aborted runs;
`getUsageAccounting` no longer walks the filesystem per request. **Anchors:**
`pkg/api/usage_accounting.go` (`usageAccountingFromTests`, `usageArchiveBytes`).

### A19 — Unify audit views — WONT-DO (by design)

SettingsPage audit (`listAuthAuditEvents`, action/actor/status filters, limit 20) is the **global
governance** surface; AccessPage Activity (`getAuthUserActivity`, actor/domain/project/time filters,
limit 50) is the **scoped user-activity** surface. Different audiences and filter sets — the
divergence is intentional. Revisit only if product direction unifies them.

### A21 — Logout CSRF token in the status body — WONT-DO

The token is HMAC-signed, user-bound, 10-min TTL, and returned in the *authenticated* `/auth/status`
response for the same client to resubmit on logout (`validateOIDCLogoutCSRFToken`) — the standard
SPA double-submit pattern. Moving it to an `HttpOnly` cookie (the original suggestion) is
**contradictory**: the frontend JS must read it to submit it. No change. **Anchors:**
`pkg/api/handlers_auth.go` (`handleGetAuthStatus`), `pkg/api/auth_oidc.go`
(`issueOIDCLogoutCSRFToken`).

### A22 — Per-session GitHub identity in bypass mode — WONT-DO

`githubUserKey` returns `"local"` only when there is no authenticated user, i.e. **dev-bypass mode**
(non-production, single-operator), where a shared GitHub connection is convenient. Per-session
keying would force GitHub re-auth every session — a net UX regression for an edge case (a shared
controller running in bypass). Production paths already key by real subject/ID. **Anchors:**
`pkg/api/github_oauth.go` (`githubUserKey`).

---

## 3. Enterprise-governance roadmap (Phase 5)

Build dependency-ordered. C1 unlocks the most. C11 (effective-access simulator) and C12 (SA owner
metadata) and C4-MVP (access-review report) already shipped in Phase 3 and are **not** in this
backlog.

### C1 — Group/team RBAC (L, TODO) — foundation

`UserRoleBinding` is keyed only by `UserID`; onboarding a team is N×M manual bindings. Two
complementary forms:
- **(a) IdP-group-claim → role mapping** (lighter, recommended first): OIDC/trusted-header modes
  already carry group claims — map a claim value to role(s) and fold them in during
  `applyLocalUserBindings`.
- **(b) native admin-managed groups**: add `storage.Group` + `GroupRoleBinding`; resolve effective
  access as the **union of direct + group bindings** (resolution already runs per request).

**Foundation for C2 (SCIM) and C7 (bulk binding).** **Acceptance:** group→role grants resolve in
effective access + enforcement; admin UI to manage groups (form b). **Anchors:**
`pkg/api/auth_resolution.go` (`applyLocalUserBindings`, `authenticatedUserFromStoredUser`),
`pkg/storage` (new types).

### C7 — Bulk binding / CSV import (M, TODO)

`POST /api/v1/auth/bindings/bulk` accepting a list or parsed CSV (`subject,scope_type,scope_id,role`)
applied transactionally with a per-row result; Bulk-assign + CSV upload on the onboard sub-tab.
Largely additive. Pairs well with C1.

### C3b — Full session-management console (L, TODO)

The Phase-2 per-user `session_epoch` "revoke all sessions" MVP shipped. The full feature: a
storage-backed session registry (id, subject, issued/last-seen, ip, ua, revoked_at) checked per
request (piggyback the existing per-request store hit), `POST /auth/sessions/{id}/revoke` +
"revoke all for user", and a Sessions sub-tab under Activity. **Anchors:**
`pkg/api/auth_resolution.go`, `pkg/storage` (new session table).

### C2 — SCIM 2.0 provisioning + deprovisioning (XL, DEMAND-GATED) — needs C1

Today unknown subjects are *rejected* (good) but leavers keep a valid local identity until manually
disabled (sharpest in trusted_headers mode). Implement `/scim/v2/Users` + `/Groups` (token-auth,
SystemAdmin-gated) onto `User` + `Group`; deprovision ⇒ `UpdateUserActive(false)`.

### C4b — Access-recertification campaigns (M+, TODO)

The read-only access-review **report** shipped (C4-MVP, `pkg/api/access_review.go`). The campaign
layer: `AccessReviewCampaign` + `AccessReviewItem` generated from current bindings; reviewer
keeps/revokes (revoke calls existing `DeleteUserRoleBinding`); decisions recorded as audit events;
a Reviews sub-tab.

### C5 — Time-bound / JIT grants + break-glass (M, TODO)

Add optional `ExpiresAt` to `UserRoleBinding` (mirror the SA expiry column work — mind the Postgres
`rewriteSQL` gotcha if it's a bool/datetime); filter expired bindings during effective-access
resolution. Self-service "request elevation" + auto-expiry + audit; a pre-created expiring
break-glass SystemAdmin grant.

### C6 — Audit retention policy + SIEM/webhook streaming (M, TODO)

Configurable retention prune shipped in Phase 2 (`APEXLOAD_AUDIT_RETENTION_DAYS`). Add an
`AuditExportTarget` setting (webhook/syslog + secret); emit each `SaveAuditEvent` async (reuse the
GitHub webhook delivery-log pattern for retry/visibility). **Anchors:** `pkg/api/audit_retention.go`,
the GitHub webhook delivery-log code in `pkg/api/github_enterprise.go`.

### C8 — Per-account rate limits + IP allowlist for service accounts (M, TODO)

Add `RateLimitPerMinute` and `AllowedCIDRs []string` to `storage.ServiceAccount`; enforce
per-account (preferring it over the global default) + the CIDR check in the auth path beside the
existing scope check. **Anchors:** `pkg/storage` (`ServiceAccount` — note the owner/contact column
work in Phase 3 as the pattern), `pkg/api/auth_resolution.go` / `middleware.go` (API-key auth path).

### C9 — Custom / cloneable roles with editable permission matrix (XL, DEMAND-GATED) — needs B1

Introduce `storage.Role` with a permission set keyed to the access-matrix resource/action IDs;
`authorizeAccessRequirement` consults role permissions instead of fixed strings; seed the 4 built-ins
as immutable cloneable roles. **B1 shipped** (the matrix↔route consistency test +
`authAccessMatrix`), which is the prerequisite for a single policy representation. Defer until a deal
needs custom roles.

### C10 — Guided "Connect identity provider" wizard (M, TODO)

Preset → fill issuer/client/redirect (live URL validation already exists) → **pre-save**
`/auth/diagnostics` test against draft values → confirm; copy-to-clipboard callback URL. Enhancement,
not a defect — preset/diagnostics/validation plumbing already exists. **Anchors:**
`dashboard/src/components/SettingsPage.tsx` (auth config), `pkg/api/handlers_auth.go`
(`handleGetAuthDiagnostics`).

---

## Cross-cutting conventions

Apply to every backend/route change here (these are the gotchas that bit during Phases 1–4):

- **New routes:** add to `pkg/api/route_catalog.go` (resolver + `accessRequirement`) **and**
  `server.go`. Run `go generate ./pkg/api` (`TestSwaggerYAMLIsFresh`). Add the golden fingerprint to
  `accessRequirementGolden` in `route_catalog_characterization_test.go`. **Public (no-auth) routes**
  go in `intentionallyPublicAPIRoutes` in `route_coverage_test.go`, not the route catalog.
- **New SQLite columns/tables:** the Postgres schema is runtime-translated by `rewriteSQL()`. **TEXT**
  columns pass through unchanged; **INTEGER-bool / DATETIME** columns need coordinated `rewriteSQL`
  entries (CREATE + ALTER + comparisons) or Postgres breaks. New indexes go in `schemaIndexes`.
- **New Store interface methods:** update the test doubles (`metricsStoreSpy` in `runner_test.go`)
  or `pkg/api` won't build.
- **Periodic/background jobs (retention, notifiers, prune):** run only on the **HA execution leader**
  (`isExecutionLeader()`, reuse the `audit_retention.go` cadence pattern) so replicas > 1 don't
  double-run.
- **WebSocket admin signals:** the hub `Client` has no identity — only `testID`/`projectID`. Use the
  `broadcast_all` flag **only** for content-free global signals (it is guarded to no-op if the
  message carries a `test_id`/`project_id`). Detailed admin payloads need real per-client identity
  routing (not yet built).
- **Frontend:** lifecycle/admin API calls live in `dashboard/src/hooks/api/useSystemApi.ts`; UI
  gating reads `/auth/effective-access` and `isEffectiveOperationAllowed` (default-DENY). Run the
  **FULL** `vitest` suite when touching cross-cutting auth/permission defaults (the #47 lesson), not
  a subset. Verify dashboard changes in a browser, not just `tsc`.
- **Git:** the maintainer drives git via GitHub Desktop and commits the whole working tree at once;
  do not run CLI checkout/reset/stash, and never `git add -A`.

---

## Appendix — resolved / refuted (do not re-add)

- **A5** bootstrap-admin — DONE (default-OFF + pinned subject, production-scoped).
- **A15** API-key rate-limit fallback — already satisfied: the single-node in-memory cache backend
  implements `AllowRateLimit`, so per-key limiting works when
  `APEXLOAD_API_KEY_RATE_LIMIT_PER_MINUTE` is set. Only HA-without-Redis uses per-replica counters
  (low value).
- **B3** deprecate broad SA scopes — already satisfied: the SA scope picker never offers
  `*`/`read`/`write`; the access-review report flags broad scopes.
- **C11** effective-access simulator, **C12** SA owner/contact metadata, **C4-MVP** access-review
  report — DONE in Phase 3.
- **B1** RBAC-model consistency, **B2** `/auth/policy` fold, **A17** issued-key modal a11y — DONE in
  Phase 4.
- Refuted during the original audit (false premises): GitHub project-policy/sync mutations *are*
  audited; there is no project/domain *owner* model to "transfer".
