# LoadRunner Parity Execution Plan

## Objective
Bring Stormrunner to practical parity with OpenText LoadRunner Enterprise in four target areas:

- Browser Testing
- Script Authoring UX
- Runtime Settings Parity
- Auth / RBAC

This plan is intentionally execution-oriented. It translates the current repo state into epics, stories, dependencies, acceptance criteria, and a staged delivery sequence that can be turned into backlog work without another round of strategy rewriting.

## Scope And Positioning

### In Scope
- Browser testing parity for web applications, including replay, diagnostics, mobile web emulation, and migration from LoadRunner browser assets where feasible
- Script-authoring lifecycle parity across import, editing, validation, replay, and save flows
- Runtime-settings parity for the highest-value Web HTTP/HTML migration surfaces first, with visibility into partial and preserved-only settings
- Enterprise-ready authentication, RBAC, service-account, and audit behavior for a shared Stormrunner control plane

### Explicitly Out Of Scope
- Native Windows desktop UI automation parity with legacy TruClient desktop-style expectations
- Full parity with every historical LoadRunner protocol family
- Rebuilding VuGen's exact UX; the target is equal or better workflow productivity, not interface mimicry

## Current Baseline In The Repo

Stormrunner already has meaningful footing in each target area:

- Browser testing plans and partial implementation exist in `docs/BROWSER_CLIENT_METRICS_PLAN.md` and `docs/MOBILE_BROWSER_EMULATION_PLAN.md`
- Script-authoring consolidation is active in `docs/SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md`
- Runtime-setting support and gaps are explicitly tracked in `docs/LOADRUNNER_RTS_REFERENCE.md`
- Auth and RBAC already include trusted-header mode, service accounts, audit events, and a native OIDC implementation path in `pkg/api/auth_oidc.go`

That means this plan is not a greenfield roadmap. It is a parity acceleration plan.

## Delivery Principles

1. Ship migration confidence before chasing long-tail checkbox parity.
2. Make partial support visible in product surfaces instead of burying it in docs.
3. Reuse the existing browser protocol, dashboard shell, DSL validator, and API surfaces rather than creating second systems.
4. Treat enterprise auth and RBAC as platform-blocking work, not polish.
5. Benchmark and document browser-scale limits instead of implying HTTP-engine density for real browsers.

## Program Structure

The work is organized into four workstreams and four delivery waves.

### Workstreams
- WS1: Browser Testing
- WS2: Script Authoring UX
- WS3: Runtime Settings Parity
- WS4: Auth / RBAC

### Delivery Waves

| Wave | Duration | Goal |
| --- | --- | --- |
| Wave 1 | 4-6 weeks | Foundations, architecture decisions, shared contracts, parity inventory |
| Wave 2 | 6-8 weeks | Daily-user workflow parity for browser and authoring flows |
| Wave 3 | 6-8 weeks | Migration confidence: runtime execution parity and import-quality upgrades |
| Wave 4 | 4-6 weeks | Enterprise hardening, benchmarks, rollout readiness, cleanup |

## Sizing Model

Story sizes use rough engineering sizing:

- `S`: 1-2 days
- `M`: 3-5 days
- `L`: 1-2 weeks
- `XL`: 2-3 weeks

These are implementation sizes, not calendar commitments.

## Workstream 1: Browser Testing

### Desired Outcome
Stormrunner becomes a strong practical alternative to LoadRunner TruClient for browser-based web testing, with a clearer and more modern workflow, while staying explicit about non-goals around native desktop automation.

### Epic B1: Browser Diagnostics And Evidence Parity
Goal: make browser failures and performance regressions explorable from one place.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| B1.1 | Add browser-process CPU and OS-memory sampling for Chromium and WebKit runtimes where practical | L | none |
| B1.2 | Add richer network waterfall and resource-dependency views to archive and replay surfaces | L | B1.1 not required |
| B1.3 | Unify screenshot, HTML, console, network, and timeline drilldown into one browser evidence panel | L | B1.2 |
| B1.4 | Add side-by-side browser run comparison views for device, platform, engine, and action-level metrics | L | B1.2 |
| B1.5 | Add browser-diagnostics export bundles for support and migration review | M | B1.3 |

Status:
- B1.1 has a first Chromium-backed implementation. Browser client metrics now carry `browser_process_cpu_pct` and `browser_process_mem_mb`, and live/archive/report summaries aggregate them as `avg_browser_process_cpu_pct` and `avg_browser_process_mem_mb`. WebKit process attribution remains open until the Playwright runtime exposes stable process IDs.
- B1.5 has a first backend slice implemented through `GET /api/v1/tests/{id}/archive/browser-diagnostics`. The endpoint exports `browser_diagnostics.json` plus screenshot and HTML artifacts captured from archived browser evidence. The dashboard now exposes archived browser-diagnostics downloads, and the script editor can export both the current browser replay session JSON and a richer replay ZIP bundle with manifest, session/report JSON, per-event request/response snapshots, console and network logs, HTML captures, DOM summaries, and screenshots for support review.

Acceptance criteria:
- A browser action can be inspected with timing, screenshots, console logs, network evidence, and page context from one UI path
- Browser archive views include waterfall-style resource diagnostics
- Comparison UX surfaces browser-specific regressions clearly across runs

### Epic B2: Mobile Web And Device Fidelity
Goal: make device-aware browser testing first-class beyond the script editor.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| B2.1 | Finish scenario-level device profile selection and validation across create/edit/run flows | M | none |
| B2.2 | Add browser-engine/profile compatibility matrix coverage in validation and replay APIs | M | none |
| B2.3 | Expand mobile UX presentation across dashboard run summaries and archive views | M | B2.1 |
| B2.4 | Build a validation matrix of real sample apps across Chromium and WebKit device profiles | L | B2.2 |
| B2.5 | Publish supported mobile-browser execution envelope and benchmark guidance | M | B2.4 |

Status:
- B2.1 is implemented across scenario authoring, payload serialization, and backend runtime override application for `headless`, `device_profile`, `touch_mode`, and `orientation`.
- B2.2 now has explicit compatibility-matrix coverage across stored validation, draft replay, stored replay, draft browser replay, and stored browser replay APIs, and scenario compilation rejects incompatible browser runtime overrides before they can be persisted.
- B2.3 now surfaces effective mobile browser runtime context across dashboard run summaries and archive provenance views so device profile, orientation, touch mode, and headless or headed execution are visible after launch.
- B2.4 now has representative protocol-level matrix coverage for a local sample browser flow across desktop Chromium, mobile Chromium, and mobile WebKit device profiles, with assertions on emitted device profile, platform, tier, and browser engine metrics.
- B2.5 now publishes the shipped browser engine and device profile support envelope plus conservative benchmark guidance in `docs/MOBILE_BROWSER_EMULATION_PLAN.md`, with discoverability links from the README and quick reference.

Acceptance criteria:
- Device profiles are available across all browser scenario authoring paths
- Browser validation calls out unsupported engine/profile combinations before execution
- The project has a documented support envelope for mobile-browser use

### Epic B3: LoadRunner Browser Migration
Goal: reduce the work required to move browser-centric LoadRunner assets into Stormrunner.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| B3.1 | Define TrueClient Mobile/Web import scope and support matrix | M | none |
| B3.2 | Implement TrueClient import MVP for common navigation, click, fill, select, wait, assertion, and transaction flows | XL | B3.1 |
| B3.3 | Surface unsupported browser steps and assumptions in standardized import review sections | M | B3.2 |
| B3.4 | Add post-import repair suggestions for selectors, waits, timing, and unsupported constructs | L | B3.2 |
| B3.5 | Add imported-browser-script replay validation path before save | M | B3.2 |

Status:
- B3.1 now has a code-owned scope definition in `pkg/convert/trueclient/support.go`, including the initial TrueClient Mobile Web versus legacy TruClient script-type matrix and the first step support matrix for planned, review-only, and out-of-scope constructs.
- B3.2 now has an MVP implementation for `TruClientMobileWeb` LoadRunner ZIP archives. The importer routes supported Mobile Web archives through `pkg/convert/trueclient`, reads UTF-16 `default.xml`, maps navigate/click/fill/select/wait plus conservative verify assertions and simple decomposed JavaScript fill/click into browser DSL actions, preserves transaction names when requested, applies mobile browser runtime settings, and keeps legacy desktop `TruClient` explicitly out of scope.
- B3.3 now has a shared `review_sections` response shape on import envelopes. LoadRunner and TruClient imports surface warnings, unsupported elements, preserved source metadata, and browser-import assumptions as machine-readable sections with stable keys, severity, summary, and item lists.
- B3.4 now has deterministic `repair_suggestions` on LoadRunner and TruClient import envelopes. Suggestions call out unsupported constructs, browser selector validation, wait refinement, runtime-setting review, and browser replay validation with stable IDs and categories that the dashboard can render.
- B3.5 now has a dashboard validation path for imported browser scripts. Converted `browser` protocol imports show a draft replay validation action before save, call the draft browser replay API with the converted script and mobile browser settings, and surface the returned session status, ID, group, warnings, and errors in the import review.

Acceptance criteria:
- Imported browser scripts land in the same review/edit/save flow as other authoring paths
- Unsupported steps are explicit and machine-readable
- Teams can replay an imported browser draft before persisting it

### Browser Workstream Notes
- Existing seams: `pkg/protocol/browser`, `pkg/api/archive.go`, `pkg/api/handlers_browser_replay.go`, `dashboard/src/components/BrowserReplayDebugger.tsx`
- Key risk: browser scale expectations drifting beyond what benchmark data supports

## Workstream 2: Script Authoring UX

### Desired Outcome
A user can acquire or create a script from any source, review its health, repair it, validate it, replay it, and save it through one consistent lifecycle.

### Epic A1: Shared Authoring Lifecycle
Goal: finish the shared authoring shell already outlined in the repo roadmap.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| A1.1 | Finish route-level authoring-shell consolidation in `ScriptsPage` and related surfaces | L | none |
| A1.2 | Standardize replay handoff so recorder, browser replay, imports, and drafts use the same review-to-edit contract | L | A1.1 |
| A1.3 | Ensure one validation API is called by parser, save, replay, import, and execute paths | M | none |
| A1.4 | Add shared draft state transitions for acquire -> review -> edit -> validate -> save | L | A1.1, A1.3 |
| A1.5 | Remove remaining temporary normalization adapters after parity verification | M | A1.2, A1.4 |

Status:
- A1.1 now has the remaining scripts-page route branching moved into `ScriptsAuthoringShell`, so `ScriptsPage` owns library data/actions while the shell owns list, create-options, and active authoring-surface selection.
- A1.2 now has a shared dashboard replay handoff helper for editor replay and imported-browser validation. Drafts, saved scripts, and imported browser scripts now share project binding, group naming, variable parsing, browser defaults, viewport fallback, and screenshot-capture normalization before they call replay APIs.
- A1.3 now has a draft validation endpoint at `POST /api/v1/scripts/validate`, giving unsaved editor drafts the same validator and warning payload shape used by saved script validation. The dashboard editor now exposes one validation action for saved and unsaved scripts, with parity coverage across draft validation, stored validation, replay, and browser replay paths.
- A1.4 has started with a typed save handoff from `ScriptEditor` through the shared authoring surface. Create and update saves now pass the stored script returned by the API into the shell callback, so downstream authoring state can react to the actual persisted script rather than a generic completion signal.
- A1.4 now keeps the authoring lifecycle inside the editor after save. Saving a draft transitions the shared authoring state into persisted edit mode for the returned script, preserving the draft body when the API response omits an embedded script payload and keeping the library list refreshed in the background.
- A1.4 now applies that same persisted-script handoff to import and recorder saves. LoadRunner, JMeter, sample-import, Java-over-HTTP sample-import, and recorder save actions now return stored scripts into the shared authoring shell so saved imports reopen in persisted edit mode instead of dropping back to the library.
- A1.4 now preserves follow-on context for multi-script imports. When an import saves multiple scripts and/or a scenario draft, the shared authoring shell reopens the first persisted script in edit mode and shows a notice about the additional saved artifacts instead of silently dropping that context.
- A1.4 now also hydrates thin import save responses with the reviewed draft payload. If an import save returns persisted ids and metadata without embedding the full script body, the shared authoring handoff still reopens the editor with the reviewed script content instead of a blank persisted shell.
- A1.4 now aligns import navigation around the create-options shell. LoadRunner, JMeter, sample, Java-over-HTTP, and recorder authoring surfaces all expose the same `Back to Create Script` return path, and import components' own close actions now land there instead of jumping back to the library.
- A1.5 has started by removing the frontend JMeter import-result normalizer. The API already guarantees non-nil import envelope arrays plus populated `stats` and `summary`, so `JMeterImport` now consumes the typed backend response directly and the adapter has been deleted instead of maintained.
- A1.5 now also tightens the imported-script contract on the dashboard side. Import flows treat returned scripts as full `ScriptDefinition` payloads, which removes cast helpers from JMeter, LoadRunner, and sample-import handoffs and makes the remaining save/edit plumbing rely on one real DSL shape.
- A1.5 now removes the last `ScriptDefinition | ImportedScript` split from the shared dashboard surfaces. The script API hook, import review surface, and Java-over-HTTP wrapper now speak in the single canonical script type, trimming another leftover migration seam.
- A2 now has a first editor-side migration assistant pass. Imported drafts expose correlation inspection, imported parameter/data-source mapping review, guided fixups for naming/think time/pacing/transactions, and audit-friendly change summaries directly inside `ScriptEditor` instead of leaving migration cleanup buried in preserved metadata.
- A3 now has a first production-grade authoring utilities pass in `ScriptEditor`: outline navigation, local authoring-signal lint badges, reusable starter blocks, imported-baseline review/diff summary, and reusable parameter/data-source/extractor inspector panels.

Acceptance criteria:
- Every script acquisition path resolves into the same lifecycle model
- Validation results are identical across entry points
- The UI no longer duplicates unrelated import/review/save orchestration

### Epic A2: Migration Assistant UX
Goal: make imported scripts understandable and repairable without deep repo knowledge.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| A2.1 | Standardize import review sections for warnings, unsupported elements, assumptions, stats, and parity notes | M | A1.1 |
| A2.2 | Add correlation inspector with source/target mapping visualization | L | A1.4 |
| A2.3 | Add parameterization and data-source assistant panels for common migration cleanup | L | A1.4 |
| A2.4 | Add guided fixups for pacing, think time, transactions, and naming normalization | M | A2.1 |
| A2.5 | Add "apply suggestion" actions that mutate drafts predictably and generate audit-friendly change summaries | L | A2.2, A2.3, A2.4 |

Acceptance criteria:
- Imported scripts explain what translated, what did not, and what the user should fix next
- Correlation and parameterization workflows are inspectable rather than hidden in metadata
- Suggested fixes are explicit and reversible at the draft level

### Epic A3: Production-Grade Editor Ergonomics
Goal: close the day-to-day UX gap with mature authoring tools without copying VuGen mechanically.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| A3.1 | Add outline/tree navigation for large scripts and grouped actions | L | A1.4 |
| A3.2 | Add inline protocol-aware linting and validation badges | M | A1.3 |
| A3.3 | Add reusable action templates and protocol starter blocks | M | none |
| A3.4 | Add diff/review mode for imported draft versus edited draft | L | A1.4 |
| A3.5 | Add reusable parameter, variable, and extractor inspector panels | L | A2.2 |

Acceptance criteria:
- Large imported scripts remain navigable and reviewable
- Validation feedback is present where the user edits
- Migration cleanup no longer requires jumping among multiple disconnected surfaces

### Authoring Workstream Notes
- Existing seams: `dashboard/src/components/ScriptsPage.tsx`, `ScriptEditor.tsx`, import components, replay handlers, `pkg/dsl`
- Key risk: rebuilding UI state before backend contract stability is finished

## Workstream 3: Runtime Settings Parity

### Desired Outcome
Stormrunner should tell users exactly which imported LoadRunner runtime settings are executed, partially emulated, preserved only, or non-applicable, and it should close the highest-value execution gaps first.

### Epic R1: Runtime Settings Inventory And Classification
Goal: turn the current reference doc into an executable backlog and user-visible status system.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| R1.1 | Convert the RTS reference into a machine-readable support matrix owned by code and tests | L | none |
| R1.2 | Add support classification metadata to import results and validation responses | M | R1.1 |
| R1.3 | Surface executed/partial/preserved/non-applicable status in import review and script editor | M | R1.2 |
| R1.4 | Add migration readiness scoring with configurable block conditions for critical unsupported settings | L | R1.2 |

Acceptance criteria:
- Runtime-setting support is not doc-only; it is available through APIs and UI
- Imported scripts receive a machine-readable parity report
- Teams can block runs or saves on unsupported critical settings

Recent progress:
- Stormrunner now owns a code-backed LoadRunner RTS support matrix with tested examples and import-envelope parity summaries.
- Draft and stored script validation now return `runtime_settings_parity`, `runtime_settings_status_counts`, and `migration_readiness` so validation, import review, and replay surfaces share one machine-readable runtime classification contract.
- The shared script editor now refreshes imported runtime parity metadata from validation results, keeping migration-readiness cards aligned with current draft runtime settings instead of stale import-time snapshots.

### Epic R2: Highest-Value Web HTTP/HTML Execution Gaps
Goal: close the runtime behaviors most likely to break real migrations.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| R2.1 | Improve extended logging fidelity for imported Web HTTP/HTML runtime settings | L | R1.1 |
| R2.2 | Expand snapshot-on-error parity beyond current HTTP/browser coverage where execution paths already support artifacts | L | R1.1 |
| R2.3 | Complete `download_resources = recorded` behavior for imported and manually-authored browser flows with preserved metadata | L | B3.2 helpful, not required |
| R2.4 | Expand DFE/data-formatting chain execution beyond current transforms and document remaining intentional exclusions | L | R1.1 |
| R2.5 | Add stronger replay semantics and warnings for preserved-only streaming/media settings | M | R1.2 |

Acceptance criteria:
- Common migrated Web HTTP/HTML settings execute consistently
- Partial support is explicit where full execution is not practical
- High-value imported scripts do not silently degrade at runtime

### Epic R3: Parity Verification Harness
Goal: make parity claims testable and regression-resistant.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| R3.1 | Build golden import/runtime fixtures for representative LoadRunner Web HTTP/HTML scripts | XL | R1.1 |
| R3.2 | Add side-by-side expected support assertions for converter output and runtime execution behavior | L | R3.1 |
| R3.3 | Add parity-focused regression tests around import review envelopes and validation responses | M | R1.2 |
| R3.4 | Publish a maintained support matrix with tested examples and known caveats | M | R3.1 |

Acceptance criteria:
- The repo contains parity fixtures that prove support claims
- Changes to importers or runtime behavior can be regression-tested against migration expectations
- Support claims in docs are backed by executable coverage

### Runtime Parity Workstream Notes
- Existing seams: `docs/LOADRUNNER_RTS_REFERENCE.md`, converter packages, `pkg/protocol/http`, browser protocol, validation APIs
- Key risk: trying to chase every historical setting before closing the few that dominate real migrations

## Workstream 4: Auth / RBAC

### Desired Outcome
Stormrunner becomes safe to operate as a shared enterprise platform with clear identity modes, least-privilege access, service-account support, and auditable control-plane actions.

### Epic G1: Auth Architecture Decision And Mode Completion
Goal: remove ambiguity around trusted headers versus native OIDC.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| G1.1 | Decide and document supported production auth modes: native OIDC, trusted headers, or both with recommendation | S | none |
| G1.2 | Complete native OIDC configuration model, validation, and operator docs | L | G1.1 |
| G1.3 | Add provider presets for Entra ID and generic OIDC in settings UX | M | G1.2 |
| G1.4 | Add auth-mode diagnostics page elements for login URL, claim mapping, cookie/session health, and callback validation | M | G1.2 |
| G1.5 | Add break-glass local/admin bootstrap guidance for self-hosted operators | S | G1.1 |

Acceptance criteria:
- Operators can configure native OIDC end to end without reading code
- Auth mode choice is explicit and documented
- Production diagnostics make auth misconfiguration visible quickly

Progress:
- G1.1/G1.2 are implemented with support for trusted-header gateway auth and native OIDC, with trusted headers still recommended when an upstream identity gateway already owns browser sign-in.
- G1.4 now includes backend-owned auth diagnostics for trusted-header and OIDC readiness, current-session state, issuer discovery, and local session expiry/invalid-cookie states.
- Native SAML is intentionally out of scope for this wave; SAML-backed deployments should terminate at an upstream gateway and use the trusted-header contract.

### Epic G2: RBAC Coverage And Enforcement
Goal: ensure roles mean the same thing across API and dashboard behavior.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| G2.1 | Audit every API route for RBAC coverage and close remaining gaps | L | none |
| G2.2 | Align dashboard action gating with backend route permissions for all major surfaces | L | G2.1 |
| G2.3 | Publish a backend-owned permission matrix by resource and action | M | G2.1 |
| G2.4 | Add project-admin ownership and delegated admin workflows where missing | L | G2.1 |
| G2.5 | Add RBAC regression tests for project isolation and service-account scopes | L | G2.1 |

Acceptance criteria:
- Every mutating and sensitive read route has explicit permission enforcement
- The dashboard does not offer actions the backend will deny without explanation
- Project isolation is provable in tests

### Epic G3: Service Accounts, Audit, And Governance Hardening
Goal: finish the parts enterprise platform teams expect before broad rollout.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| G3.1 | Add service-account expiration, rotation policy, and last-use visibility polish | M | none |
| G3.2 | Add audit search/export workflows and stronger admin-event coverage verification | M | none |
| G3.3 | Add token-scope modeling for CI/CD and automation use cases | L | G2.1 |
| G3.4 | Add auth and audit operational runbooks for self-hosted deployment | M | G1.2, G3.2 |
| G3.5 | Perform cookie/session/security review for native OIDC paths and secret handling | M | G1.2 |

Acceptance criteria:
- Service accounts are production-manageable
- Audit evidence is queryable and exportable
- Security-sensitive auth flows are explicitly reviewed and documented

Progress:
- G3.5 has a first hardening pass: native OIDC logout is a CSRF-protected `POST`, `/auth/me` reports session expiry metadata, the dashboard warns on imminent local-session expiry, and representative OIDC callback failures are audited without persisting authorization codes, raw tokens, client secrets, or signed cookie material.

### Epic G4: Directory And Lifecycle Extensions
Goal: handle the remaining parity gap between Stormrunner's current platform auth and a mature enterprise identity model.

| ID | Story | Size | Dependencies |
| --- | --- | --- | --- |
| G4.1 | Decide whether user/group persistence stays upstream-owned or becomes Stormrunner-owned | M | G1.1 |
| G4.2 | If upstream-owned, add clearer group/role mapping documentation and diagnostics | M | G4.1 |
| G4.3 | If Stormrunner-owned, design user/group persistence and assignment APIs | XL | G4.1 |
| G4.4 | Evaluate SCIM or directory-sync compatibility for future enterprise rollout | M | G4.1 |

Acceptance criteria:
- The remaining identity-ownership question is resolved rather than deferred indefinitely
- Operators understand where identity lifecycle lives

### Auth / RBAC Workstream Notes
- Existing seams: `pkg/api/middleware.go`, `pkg/api/auth_oidc.go`, settings UI, service-account and audit handlers
- Key risk: supporting both trusted-header and native OIDC without a clear recommendation and diagnostics story

## Cross-Workstream Dependencies

| Dependency | Why It Matters |
| --- | --- |
| Shared validation contract | Authoring, browser import, and runtime parity all depend on one validation surface |
| Import review envelope | Browser migration UX and runtime parity status both need one shared import contract |
| Native auth decision | RBAC rollout and enterprise operator docs depend on a stable auth architecture |
| Parity matrix in code | Runtime-setting UX, import review, and migration scoring all depend on machine-readable support data |
| Browser benchmark data | Product positioning and scenario UX should not promise unsupported scale |

## Recommended Delivery Sequence

### Wave 1: Foundations
- G1.1 auth architecture decision
- A1.3 validation API parity verification
- R1.1 machine-readable RTS matrix
- G2.1 RBAC route audit
- B3.1 TrueClient import scope definition

### Wave 2: User Workflow Parity
- A1.1, A1.2, A1.4 shared authoring lifecycle completion
- A2.1 import review standardization
- B1.2, B1.3 browser diagnostics unification
- B2.1 scenario-level device profile UX
- G1.2, G1.3 native OIDC completion and presets

### Wave 3: Migration Confidence
- B3.2, B3.3, B3.5 TrueClient import MVP and replay validation
- R2.1-R2.4 highest-value runtime execution gaps
- R1.2-R1.4 parity reporting and migration scoring
- A2.2-A2.5 migration assistant UX
- G2.2-G2.5 RBAC UI parity and isolation regression coverage

### Wave 4: Hardening And Rollout
- B1.4, B1.5 browser comparison and export polish
- B2.4, B2.5 browser validation matrix and benchmark publication
- R3.1-R3.4 parity verification harness and public support matrix
- G3.* governance hardening
- G4.* identity lifecycle decision closure
- A1.5 cleanup and adapter removal

## Suggested Team Split

| Team Slice | Primary Ownership |
| --- | --- |
| Browser parity | `pkg/protocol/browser`, browser replay handlers, archive/report surfaces, browser dashboard components |
| Authoring UX | `ScriptsPage`, import components, `ScriptEditor`, replay/debugger UI, authoring hooks |
| Runtime settings parity | converter packages, `pkg/protocol/http`, browser runtime, `pkg/dsl`, validation and import APIs |
| Auth / RBAC | auth middleware, `auth_oidc.go`, settings UI, audit and service-account APIs |

## Release Gates

Before claiming practical parity in these target areas, all of the following should be true:

1. A representative LoadRunner browser or Web HTTP/HTML script can be imported and reviewed through one shared flow.
2. The user can see which runtime settings are executed, partial, preserved-only, or unsupported before running the script.
3. A browser replay/debug cycle exists for imported browser drafts before save.
4. Native OIDC or the chosen production auth mode is fully documented, validated, and test-covered.
5. RBAC enforcement and dashboard gating are consistent across major resources.
6. Browser-scale and mobile-browser claims are backed by published benchmark guidance.

### Workstream 5 Progress
- Multi-region execution is now observable as well as configurable: `/metrics` exports region-labeled generator count, available VUser capacity, and active VUsers from live generator inventory, so regional placement can be tracked in Grafana/APM workflows.
- APM cross-reference is now explicit in archives: W3C `traceparent` correlation IDs are decomposed into `trace_id` and `span_id` fields in JSONL and Parquet transaction results, and archive transaction queries can filter directly by `trace_id`.
- Lifecycle notification webhooks now support Dynatrace Events API v2 payloads with API-token auth, optional entity selectors, event timeouts, and failure events mapped to Dynatrace `ERROR_EVENT` annotations.
- Scripts can import target-side Prometheus text metrics through `config.observability.external_metrics`, carrying selected CPU, memory, queue-depth, or application gauges into live resource snapshots and archived resource samples.
- Network emulation now has attribution as well as shaping: transaction results include a normalized `network_profile`, live metrics expose `network_profiles`, and archive reports aggregate response/error stats under `by_network_profile`.
- Browser/mobile capacity advice now classifies scenarios as `http`, `browser`, or `mixed`, applies conservative device-tier density caps for browser VUsers, and warns users that browser runtimes are diagnostic canaries rather than HTTP-scale goroutine VUsers.
- TrueClient MobileWeb import now converts simple JavaScript scroll intent (`window.scrollTo` / `window.scrollBy`) into `browser_swipe` actions, reducing blocker repair items for common mobile review-only steps.
- TrueClient MobileWeb import now also maps JavaScript `location` navigation assignments and `assign` / `replace` calls into `browser_navigate`, shrinking the unsupported JavaScript envelope for common page-transition scripts.
- TrueClient MobileWeb direct `Scroll` steps now map to `browser_swipe` actions with selector, direction, and distance handling, bringing the support matrix in line with executable converter behavior.
- Scenario-level browser UX now surfaces saved runtime device overrides in queued run previews, so users can verify device profile, orientation, touch mode, and headed/headless state before start or revision.

## Suggested Milestones

| Milestone | Exit Criteria |
| --- | --- |
| M1: Foundations Locked | Auth mode decision made, RTS matrix codified, validation and RBAC audits complete |
| M2: Unified Authoring | Shared authoring lifecycle ships across imports, drafts, replay, and save |
| M3: Migration Confidence | TrueClient import MVP, runtime parity reporting, and high-value RTS execution gaps ship |
| M4: Enterprise Rollout Ready | OIDC and RBAC hardening complete, audit/service-account polish shipped, browser benchmarks published |

## Success Metrics

Track these metrics during rollout:

- Import-to-first-successful-run time for representative migrated scripts
- Percentage of imported scripts with machine-readable parity reports
- Number of unsupported or preserved-only settings per migrated script
- Browser replay pass rate after import
- RBAC defect count from integration testing
- OIDC configuration support incidents during pilot rollout

## Recommended First Backlog Cut

If the team wants the highest leverage first slice, start here:

1. G1.1 Decide production auth posture and document it
2. R1.1 Move RTS support matrix into code-owned metadata
3. A1.1/A1.3 Finish shared authoring and validation contracts
4. B3.1/B3.2 Define and build TrueClient import MVP
5. G2.1 Audit all API routes for RBAC coverage

That set establishes the control plane, the migration truth model, and the core workflow that the rest of the parity effort depends on.
