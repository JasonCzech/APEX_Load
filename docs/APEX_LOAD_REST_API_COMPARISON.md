# APEX Load REST API vs LoadRunner Enterprise 26.x — API-Layer Comparison

**Audience:** internal engineering, product, evaluators, and integration partners.

This document compares the **REST API / integration layer** of APEX Load against **OpenText
LoadRunner Enterprise (LRE) 26.x**. It is deliberately scoped to the *programmable surface* — how each
platform is driven as-code and wired into automation — and answers "where does each fall short of the
other." Non-API competitive dimensions (VuGen correlation depth, desktop-protocol breadth, offline
Analysis depth, proven scale) live in [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md#loadrunner)
and are only referenced here.

This doc is also the **living scorecard** for the API-parity program: its "▼ APEX trails" rows map to
dimensions in `GET /api/v1/system/parity-status` (see [Parity program linkage](#parity-program-linkage)),
so the doc and the endpoint do not drift.

**As-of:** APEX Load `main` (evidence rechecked 2026-06-15); baseline OpenText LoadRunner Enterprise
26.x. LRE's REST surface varies by patch level and some admin resources are version-/deployment-specific;
rows note this where it matters.

---

## Legend

Verdict (APEX-relative): **▲ APEX leads** · **= parity** · **▼ APEX trails** · **▼▼ APEX trails badly**

Status vocabulary (shared with the competitive matrix): **Supported** · **Partial** · **Gap** ·
**Evidence required** · **Non-goal**.

---

## TL;DR verdict

- **APEX leads on modern API ergonomics and test-as-code.** Stateless OIDC + scoped API-key auth,
  explicit `/api/v1` versioning, generated OpenAPI, JSON throughout, WebSocket live-metrics streaming,
  full script/test CRUD via REST + open JSON DSL, JSON KPI/transaction/trend extraction, and native
  GitHub/GitOps with PR check-runs.
- **LRE leads on enterprise resource governance exposed at the admin layer** — first-class timeslot
  reservations, domain/project administration, test-sets/test-instances, host pools, AUT registry, and
  ALM/Quality Center traceability — backed by a maintained CI-marketplace plugin ecosystem (Jenkins,
  Azure DevOps).
- **The headline asymmetry:** LRE's REST API **cannot create or modify test/script definitions** (VuGen/
  UI only) and **cannot stream live metrics** (poll-only; KPIs locked in proprietary `.lrr`/`.lra`
  result archives). APEX's REST API **lacks a forward-booking reservation resource** and a managed
  fleet/host-pool admin surface. Those two gaps define most of the build-out below.

---

## Chart A — API ergonomics & protocol design

| Dimension | LRE 26.x | APEX Load | Verdict |
| --- | --- | --- | --- |
| Resource model / base path | Domains → Projects → Tests → Runs; `/LoadTest/rest/domains/{domain}/projects/{project}/...` | Flat `/api/v1/...` with project/domain scoping via params + RBAC | **=** |
| Authentication | LWSSO cookie (stateful); HTTP Basic → `Set-Cookie: LWSSO_COOKIE_KEY`; re-auth on mid-flight 401 | Stateless: OIDC sessions, `Authorization: Bearer`/`X-APEXLoad-API-Key` (SHA-256-hashed), trusted-header mode (`pkg/api/middleware.go`) | **▲** |
| Authorization / RBAC | Domain/project roles, ALM-linked permissions | 4 roles (`system_admin`/`project_admin`/`editor`/`viewer`) × global/project/domain/repo scopes, single-source policy (`pkg/api/access_policy.go`) | **=** |
| Multi-tenancy | Domains + Projects (strong, mature) | Projects (primary) + Domains (app-repo sharing) | **=** |
| API versioning | None in path; implicit cross-version compatibility | Explicit `/api/v1` prefix | **▲** |
| Machine-readable contract | No published OpenAPI/Swagger | Generated OpenAPI 3.0.3 (`docs/swagger.yaml` via `cmd/swaggergen`, freshness test-enforced) | **▲** |
| Response schemas in contract | n/a (no spec) | Present but mostly generic `200 Successful operation` (concrete schemas in progress) | **▲** (caveated) |
| Payload format | JSON (`Accept: application/json`) | JSON; multipart for uploads; CSV export on some endpoints | **=** |
| Error envelope | `{ExceptionMessage, ErrorMessage, Message, ErrorCode}` | `{error, ref}`; opaque `ref` + server log on 5xx (`writeError`, `pkg/api/handlers.go`) | **=** |
| Live metrics over API | Not REST-exposed; online stats behind the run dashboard | `GET /api/v1/tests/{id}/metrics` snapshot + WebSocket streams | **▲** |
| Async / streaming | Polling only (no webhooks/SSE/WebSocket) | Polling + WebSocket (`/api/v1/ws`, `/api/v1/tests/{id}/stream`); async jobs poll (GitHub sync-jobs) | **▲** |
| Inbound webhooks | None documented | GitHub receiver with HMAC-SHA256 (`POST /api/v1/integrations/github/webhook`) | **▲** |
| Outbound completion webhooks | None | None (Dynatrace is dry-run only) — shared gap | **=** |
| Idempotency | Convention only (`RunComment` marker) | `Idempotency-Key` header, actor-scoped, 409 on conflict (coordination store) | **▲** |
| Rate limiting | Not documented | Per-service-account `X-RateLimit-*` headers (env-configurable) | **▲** |
| Pagination / filtering | `?query={...}` server filter; no cursor/limit/offset | Offset (`limit`/`offset`) on some lists, not yet standardized; no cursor | **=** |
| CORS / browser security | n/a (server-rendered) | Allow-list CORS, CSP/HSTS/`nosniff`/`DENY` headers (`pkg/api/middleware.go`) | **▲** |
| Request size limits | Not documented | Global 128 MiB backstop + per-route `MaxBytesReader` overrides | **▲** |
| Agent-tool surface (MCP/LLM) | None | MCP server exposes the REST API as tools with confirm/reason gating (`mcp/apexload_mcp/`) | **▲** |

---

## Chart B — Capability surface (what each API can actually do)

| Capability | LRE 26.x | APEX Load | Verdict |
| --- | --- | --- | --- |
| Test/script definition as code | **Cannot** create/modify test or script definitions via REST (VuGen/UI only) | Full CRUD + validate + replay + export over JSON DSL (`/api/v1/scripts*`) | **▲▲** |
| Run lifecycle control | Create+start, stop, abort; duration/VUsers fixed at creation | Start/stop/abort/pause/resume **plus** mid-run scale-VUsers, adjust duration, adjust goal targets | **▲** |
| Capacity model | First-class **timeslot reservation** (forward booking, calendar, conflict prevention) | Real-time admission control + estimate/preflight + scenario schedules/approvals, **plus a forward-booking timeslot API** (`/api/v1/capacity/timeslots`: CRUD, availability/conflict, approval state, atomic bind — `pkg/api/handlers_timeslots.go`); scheduler admission control not yet reservation-aware | **▼** (narrowing — reservation primitive landed) |
| Results & KPI extraction | Download result zip; parse proprietary `.lrr`/`.lra`/Analysis; only `RunSLAStatus` on the run entity | JSON metrics/transactions/trend/comparison endpoints; Parquet/JSONL archives; error evidence | **▲** |
| Trend / regression analysis | Analysis tool (offline); limited REST | `GET /api/v1/releases/{id}/trend` (+ export), pinned-baseline comparison, SLA governance | **▲** |
| Scheduling / run calendar | Timeslot calendar (reservation-centric) | Cron schedules, occurrences, approvals, governance windows, `/api/v1/run-calendar` | **=** |
| Test-set / ordered execution | Test Sets / Test Instances (ordered collections) | **Execution plans** (`/api/v1/execution-plans`): ordered parallel/sequential scenario entries that materialize one run each, with per-entry failure policy; dependency-gated sequential *execution* still pending | **▼** (narrowing — ordered plan landed) |
| Imports / interop | LR-native; one-way archive import | 11 formats (LR archive, JMeter, k6, HAR, OpenAPI, RAML, AsyncAPI, Postman, WSDL, Salesforce, Oracle EBS) | **▲** |
| Fleet / host administration | Host pools, load generators, monitor-over-firewall, SiteScope (admin layer) | Read-only fleet + generator drain, **plus managed pools, persistent generator metadata/labels/cordon, and provisioning requests** (`/api/v1/fleet/pools\|generators\|provision-requests`); provisioning is `not_configured` until a provider is wired | **▼** (narrowing — management surface landed) |
| Monitoring integration | SiteScope + monitor-over-firewall agents (registered collectors) | Inbound monitoring **plus a collector control plane** (`/api/v1/monitoring/collectors`: register, SHA-256 scoped token + rotation, metric packs, health); agent runtime/heartbeat pending | **▼** (narrowing — control plane landed) |
| Entitlements / consumption | License-tier model + usage | **Read-only entitlements** (`/api/v1/system/entitlements[/usage]`): edition, enabled bundles, limits, and live consumption in one view (surfaced, not enforced) | **=** (read parity; APEX doesn't enforce by design) |
| Correlation as-code | Per-protocol rules engine + design studio (persistent, learned) | detect/diagnose/diff engine (`pkg/convert/correlate/`) **plus persisted per-script recorded baselines** (`/api/v1/scripts/{id}/recording/status`, `/api/v1/scripts/{id}/diagnose-replay`, redacted to preserve correlation candidates); learned reusable rulesets still pending | **▼** (narrowing) |
| Browser object repository | VuGen/TruClient object repository | Selector model + **persisted project-scoped object repositories** (`/api/v1/object-repositories`, seedable `from-script`); script↔repository binding + repair-on-replay still pending | **▼** (narrowing) |
| CI/CD integration | Maintained Jenkins + Azure DevOps marketplace plugins | GitHub-native (push/pull, PR check-runs, webhooks, sync-jobs) + CI templates; **no marketplace plugins** | **=** |
| Git / GitOps | No native Git script sync | Native GitHub script push/pull, drift status, project policy | **▲** |
| ALM / requirements traceability | Quality Center / ALM integration | None (out of scope) | **▼** |

---

## Where APEX falls short of LRE (and what closes it)

These are the API-layer gaps the parity program addresses. Each maps to a build family.

1. **Forward-booking capacity reservation — *landed (partial)*.** APEX now exposes timeslot
   reservations (`/api/v1/capacity/timeslots`: CRUD, availability/conflict checks, an approval state,
   and atomic single-test binding — `pkg/api/handlers_timeslots.go`, `pkg/storage/sqlite_timeslots.go`).
   Remaining for full LRE parity: make `pkg/api/capacity.go` admission control reservation-aware
   (protect an approved, bound window from other tenants) and wire `timeslot_id` binding into test
   creation. Tracked as the `capacity_reservation` dimension (now `partial`) in `parity-status`.
2. **Fleet pool & provisioning management — *landed (partial)*.** APEX now exposes managed generator
   pools, persistent generator metadata (labels/region/cordon/pool membership), and provisioning
   requests (`/api/v1/fleet/pools|generators|provision-requests`; `pkg/api/handlers_fleet_management.go`,
   `pkg/storage/sqlite_fleet.go`). Provisioning returns `not_configured` until a live cloud/Kubernetes
   provider backend is wired. Tracked as `fleet_management` (now `partial`) in `parity-status`.
3. **Ordered cross-run execution plan — *landed (partial)*.** APEX now exposes execution plans
   (`/api/v1/execution-plans`: CRUD + `POST /{id}/runs` materializing one run per enabled entry, with
   per-entry parallel/sequential failure handling — `pkg/api/handlers_execution_plans.go`). Remaining:
   dependency-gated sequential execution. Tracked as `execution_planning` (now `partial`).
4. **Monitoring collectors — *control plane landed (partial)*.** Restricted-network collectors now
   register via `/api/v1/monitoring/collectors` with SHA-256 scoped tokens (one-time issue + rotation),
   allowed metric packs, and health (`pkg/api/handlers_monitoring_collectors.go`). Remaining: the agent
   runtime + collector-token-authenticated heartbeat/health ingestion. Tracked as `monitoring_collectors`.
5. **Entitlements read model — *landed (partial)*.** `/api/v1/system/entitlements[/usage]` projects
   edition, enabled bundles, limits, and live consumption in one read-only view
   (`pkg/api/handlers_entitlements.go`) — surfaced, not enforced (no licensing engine by design).
   Tracked as `entitlements`.
6. **Correlation baselines and object repositories now persist (partial).** Per-script recorded baselines and project-scoped object repositories are stored; learned correlation rulesets and script↔repository binding remain.
   → **Persistent correlation rulesets** + per-script discover/apply, and a **persisted object
   repository**.
7. **No ALM/Quality Center traceability and no CI-marketplace plugins.** Out of scope for this program;
   GitOps/PR-check-runs are the deliberate alternative. ALM remains a documented non-goal.

Note: LRE's most cited advantages — VuGen correlation, desktop protocols (SAP GUI/Citrix/Oracle
Forms), offline Analysis depth, and decade-proven scale — are **not REST-API features**. They are not
addressed here; see [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md#loadrunner).

## Where LRE falls short of APEX (deliberately not "fixed" — APEX advantages to keep)

1. **No test-as-code.** The REST API cannot create/modify test or script definitions; everything must
   pre-exist in VuGen/UI. APEX is API-first over an open JSON DSL.
2. **Stateful, token-less auth.** LWSSO cookie + Basic, no API keys/JWT/scoped service accounts.
3. **No API versioning and no OpenAPI spec.** Clients reverse-engineer; no codegen, no contract tests.
4. **No live-metrics REST surface and no streaming.** Poll-only; no WebSocket/SSE. KPIs require
   downloading and parsing proprietary `.lrr`/`.lra` Analysis archives — there are no JSON
   KPI/transaction/trend endpoints.
5. **No native Git / GitOps / PR check integration.**
6. **No idempotency keys** (only the `RunComment` marker convention) and **no documented rate-limit
   contract**.
7. **No agent-tool (MCP) surface.**

The build program explicitly **does not** chase these — they are documented as APEX-ahead so effort
isn't spent regressing toward LRE's model.

## Shared gaps / parity

- Neither offers cursor pagination (LRE has none; APEX has offset on some lists, not standardized).
- Neither emits outbound test-completion webhooks.
- Neither publishes rate-limit SLOs (APEX at least emits `X-RateLimit-*`).
- Both are JSON-over-HTTP at the wire level.

---

## Parity program linkage

The "▼ APEX trails" rows above are tracked as dimensions in `GET /api/v1/system/parity-status`
(`pkg/api/parity_status.go`): `capacity_reservation`, `fleet_management`, `execution_planning`,
`monitoring_collectors`, `entitlements`, `correlation_persistence`, `object_repository`. As each family
lands, its dimension flips `gap → partial → supported`, and the corresponding row here is updated in the
same change so this scorecard never drifts from the endpoint.

## Honesty caveats

- **LRE's admin REST surface is broader than a minimal run-orchestration client exercises.** Timeslots,
  hosts, test-sets, AUT, and monitor management exist at the administration layer in LRE; exact
  availability is patch-/deployment-specific. This doc represents them as present without claiming an
  exhaustive 26.x endpoint inventory.
- **APEX claims are code-grounded.** Every APEX row cites `pkg/...` or `/api/v1/...`. Where a capability
  is partial (response schemas, entitlements, pagination standardization), it is marked partial, not
  supported.
- **API-scope only.** Verdicts here rate the *programmable surface*, not the underlying engine,
  analysis depth, or proven scale.

## Evidence & sources

- **APEX Load (code-owned):** `pkg/api/server.go`, `pkg/api/access_policy.go`, `pkg/api/middleware.go`,
  `pkg/api/handlers.go`, `pkg/api/capacity.go`, `pkg/api/parity_status.go`, `docs/swagger.yaml`,
  `docs/API_USER_GUIDE.md`, `mcp/apexload_mcp/`.
- **LoadRunner Enterprise 26.x:** OpenText LoadRunner Enterprise REST API Reference
  (admhelp.microfocus.com / OpenText documentation portal); LWSSO/run/results wire behavior corroborated
  against an internal LRE orchestration adapter.
- **Companion:** [COMPETITIVE_COMPARISON.md](COMPETITIVE_COMPARISON.md#loadrunner) (non-API dimensions).
