# Enterprise Platform Parity Execution Plan

## Objective
Bring Stormrunner from a strong LoadRunner migration target to a mature enterprise performance testing platform across seven platform areas:

- Platform architecture
- Scale-out model
- Script authoring UX
- Load modeling
- Real-time monitoring
- Historical reporting
- Scheduling

This plan extends the completed `docs/LOADRUNNER_PARITY_EXECUTION_PLAN.md`. That plan closed the LoadRunner migration-facing workstreams around browser testing, script authoring, runtime settings, and Auth/RBAC. This plan is broader: it targets the control-plane, execution, analytics, and operations capabilities enterprise teams expect from a shared performance testing service.

## Positioning

Stormrunner should not mimic LoadRunner Enterprise screen-for-screen. The target is practical enterprise parity:

- Kubernetes-native controller and generator topology instead of Windows controller/host assumptions.
- Go goroutine VUsers for high-density protocol load, plus browser canaries for rendered UX diagnostics.
- Vendor-neutral telemetry and archives instead of proprietary result stores.
- Explicit support envelopes and migration diagnostics instead of hidden partial compatibility.

## Current Baseline

Stormrunner already has substantial platform foundations:

- Controller API, dashboard, scenario runs, recurring schedules, and persisted metadata.
- Distributed generator RPC, regional placement, generator inventory, and capacity advisory surfaces.
- Closed VUser and open arrival-rate workload modes.
- Scenario-level runtime overrides, goals, SLA status, and CI/CD-friendly CLI outputs.
- Live WebSocket metrics, telemetry health, diagnostic events, and typed interval history.
- Run archives, standalone HTML reports, comparison baselines, provenance manifests, JSONL and Parquet exports.
- LoadRunner, TrueClient MobileWeb, JMeter, DevWeb, HAR, sample, SOAP, Java-over-HTTP, WebSocket, and gRPC authoring/import paths.
- Auth/RBAC, service accounts, token scopes, audit feeds, and operational runbooks.

The remaining parity work is not a single missing subsystem. It is about tightening platform contracts, making scale and scheduling governance explicit, and turning monitoring/reporting data into operator-grade decisions.

## Delivery Principles

1. Treat the controller as the durable system of record and generators as elastic, replaceable workers.
2. Separate hot-path telemetry from diagnostic samples and full-fidelity archives.
3. Make capacity, quotas, placement, and scheduling decisions explainable before execution starts.
4. Keep authoring workflows unified across native drafts, imports, recorders, and generated scripts.
5. Prefer code-owned support matrices and machine-readable readiness scores over doc-only claims.
6. Publish scale envelopes with benchmark evidence instead of implying all protocols share HTTP density.

## Workstreams

| Workstream | Area | Goal |
| --- | --- | --- |
| P1 | Platform Architecture | Harden the shared control-plane architecture, persistence boundaries, tenant model, and operator contracts. |
| P2 | Scale-Out Model | Make distributed execution, regional placement, capacity governance, and generator elasticity production-grade. |
| P3 | Script Authoring UX | Extend the completed LoadRunner authoring lifecycle into a broader source-agnostic authoring platform. |
| P4 | Load Modeling | Close advanced workload-shaping gaps such as rendezvous, global iteration goals, and mixed open/closed profiles. |
| P5 | Real-Time Monitoring | Enforce the under-2-second live telemetry target and expose operator-grade pipeline health. |
| P6 | Historical Reporting | Make archive, comparison, provenance, and trend reporting suitable for release governance. |
| P7 | Scheduling | Turn recurring execution into a governed scheduling service with calendars, approvals, blackout windows, and fair-share policy. |

## Workstream P1: Platform Architecture

### Desired Outcome
Stormrunner has a clear enterprise architecture: stateless API/controller replicas where possible, explicit persistence ownership, resilient generator registration, tenant boundaries, and operator-friendly deployment contracts.

### Epic P1.1: Control Plane Topology

Reference artifact: `docs/ENTERPRISE_PLATFORM_ARCHITECTURE_RUNBOOK.md`.

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P1.1 | Document controller, generator, dashboard, storage, and telemetry boundaries as the production reference architecture | M | none | Complete |
| P1.2 | Define HA controller behavior: singleton scheduler responsibilities, leader election, idempotent reconciler loops, and safe restart contracts | L | P1.1 | Complete for architecture contract; implementation continues in P7.5 |
| P1.3 | Add production readiness checks for storage, generator RPC, archive writer, telemetry fan-out, auth mode, and scheduler state | M | P1.1 | Complete for initial API surface via `GET /api/v1/system/readiness`; extend as new dependencies land |
| P1.4 | Define upgrade and migration policy for SQLite/PostgreSQL metadata, archive schema versions, and dashboard static assets | M | P1.1 | Complete |
| P1.5 | Publish an operator architecture runbook with failure modes and recovery flows | M | P1.2, P1.3 | Complete |

Acceptance criteria:
- Operators can explain which component owns each state family.
- Controller restarts do not lose queued work, schedules, or running-run reconciliation.
- Readiness diagnostics distinguish degraded dependencies from ordinary no-traffic states.

### Epic P1.2: Tenant And Governance Model

Reference artifact: `docs/ENTERPRISE_GOVERNANCE_MODEL.md`.

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P1.6 | Normalize project, release, scenario, script, data pool, run, and archive ownership semantics | M | none | Complete |
| P1.7 | Decide whether first-class teams are required beyond projects for quotas and chargeback | M | P1.6 | Complete; project-first model remains primary |
| P1.8 | Add usage accounting primitives by project, protocol, generator region, VUsers, duration, and archive size | L | P1.6 | Partial completion: live rollups, CSV export, durable snapshot capture/list APIs, terminal-run automatic snapshots, and snapshot audit events are implemented; wall-clock cadence-based capture remains future work |
| P1.9 | Extend audit events for governance-policy changes, schedule decisions, run promotions, and capacity rejections | M | P1.8 | Defined; implementation continues with policy engines |

Acceptance criteria:
- Every governed resource has an owner, project scope, audit scope, and retention behavior.
- Usage accounting supports chargeback and capacity planning without replaying raw archives.

## Workstream P2: Scale-Out Model

### Desired Outcome
Stormrunner can scale out predictably across generator pools and regions while making capacity, placement, and saturation visible before and during tests.

### Epic P2.1: Generator Fleet Management

Reference artifact: `docs/ENTERPRISE_GENERATOR_FLEET_MODEL.md`.

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P2.1 | Convert generator inventory into a formal fleet model with health, capacity, protocol support, region, version, and drain state | L | P1.1 | In progress; inventory fields added |
| P2.2 | Add generator drain/cordon workflows for maintenance without interrupting unrelated runs | M | P2.1 | Complete for controller-local placement control; persistence remains future work |
| P2.3 | Add generator version compatibility checks between controller, DSL features, protocol plugins, and generator binaries | M | P2.1 | Complete for target-declared protocol/version compatibility; generator-side negotiation remains future work |
| P2.4 | Add fleet-level saturation alerts and placement rejection reasons to API, dashboard, and metrics | M | P2.1 | Complete for first-pass API/dashboard parity - `GET /api/v1/fleet/alerts` summarizes unreachable generators, cordon/drain state, saturation/constrained capacity, compatibility issues, and controller/generator version spread, and the Monitoring page renders the same alert classes from live generator inventory |

Acceptance criteria:
- Placement failures explain whether capacity, protocol support, region, version, or drain state blocked the run.
- Operators can remove a generator from scheduling without losing existing evidence.

### Epic P2.2: Elastic Scale And Regional Placement

Reference artifact: `docs/ENTERPRISE_AUTOSCALING_SIGNALS.md`.

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P2.5 | Define HPA/KEDA scaling signals for queued VUsers, active runs, protocol mix, and generator saturation | L | P2.1 | Complete for Prometheus signal contract; cluster manifests remain future work |
| P2.6 | Add capacity reservations and reserved pools for critical projects or environments | L | P1.8, P2.1 | Complete for first-pass API/settings/advisory/metrics primitives - capacity reservations feed advisory behavior, metrics expose fleet state, and `GET /api/v1/fleet/capacity?project_id=&region=&protocol=` reports filtered active, available, reserved, and rejected capacity; fair-share placement remains future work |
| P2.7 | Add fair-share placement when multiple projects contend for distributed capacity | L | P1.8, P2.1 | Complete for controller pending-queue promotion; distributed slot accounting remains future work |
| P2.8 | Publish protocol-specific density envelopes for HTTP, JMS, WebSocket, gRPC, SOAP, Java-over-HTTP, and browser canaries | M | P2.1 | Complete for code-owned advisory envelopes; benchmark calibration remains future hardening |

Acceptance criteria:
- Scale-out behavior is driven by observable queue/capacity signals.
- Capacity advice accounts for protocol density rather than treating every VUser as equivalent.
- Regional placement decisions are reproducible from stored metadata.

## Workstream P3: Script Authoring UX

### Desired Outcome
Authoring feels like one product across manual drafts, imports, recording, AI-assisted generation, validation, replay, repair, and save.

### Epic P3.1: Source-Agnostic Authoring Lifecycle

Reference artifact: `docs/ENTERPRISE_AUTHORING_CONTRACT.md`.

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P3.1 | Promote the completed LoadRunner authoring lifecycle into a generic authoring contract for all importers and generators | M | none | Complete for API contract |
| P3.2 | Add an authoring capability matrix for each source: LoadRunner, TrueClient MobileWeb, JMeter, DevWeb, HAR, recorder, sample, manual, AI-generated | M | P3.1 | Complete for backend source matrix |
| P3.3 | Add consistent draft health scoring across validation issues, runtime parity, unsupported constructs, missing data, and replay readiness | L | P3.1 | Complete for validation response scoring; dashboard badges remain future work |
| P3.4 | Add authoring session persistence for unsaved drafts and long-running repair workflows | L | P3.1 | Complete for API and SQLite persistence |
| P3.5 | Add review/approval handoff for migrated scripts before they become reusable scenario assets | M | P3.3 | Complete for backend approval gate; dashboard workflow remains future work |

Acceptance criteria:
- Every acquisition path lands in the same draft contract and health model.
- Users can leave and return to an unsaved migration repair session safely.
- Reusable script promotion passes through an approval endpoint that blocks invalid or unhealthy drafts and stamps approval metadata.

### Epic P3.2: Authoring Intelligence

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P3.6 | Expand correlation suggestions into guided variable creation and extractor insertion with preview diffs | L | P3.3 | Partial backend complete: session preview/apply endpoint with extractor insertion and field diffs |
| P3.7 | Add data-source fit checks for required columns, exhaustion policy, uniqueness, and row-count versus load model | M | P3.3 | Partial backend complete: validation response and health scoring cover source/column/row-count readiness |
| P3.8 | Add protocol-aware starter templates for common enterprise flows and CI/CD smoke tests | M | P3.2 | Partial backend complete: HTTP, browser, and SOAP template catalog endpoint |
| P3.9 | Add AI-assisted script generation guardrails: provenance, validation, diff review, and replay-before-save requirements | L | P3.1 | Partial backend complete: validation/health guardrails block promotion without review and replay evidence |

Acceptance criteria:
- Suggested authoring changes are previewable, reversible, and auditable.
- AI-generated drafts follow the same validation and replay gates as imported scripts.

## Workstream P4: Load Modeling

### Desired Outcome
Stormrunner supports the workload models enterprise performance engineers expect: closed VUser, open arrival-rate, staged ramps, goal-oriented runs, rendezvous spikes, iteration limits, mixed protocol groups, and capacity-aware validation.

### Epic P4.1: Workload Shape Library

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P4.1 | Publish a code-owned load-model support matrix for closed, open, stepped, spike, soak, stress, goal-based, and mixed workloads | M | none | Complete for backend matrix |
| P4.2 | Add reusable workload-shape templates in scenario authoring | M | P4.1 | Complete for backend template catalog |
| P4.3 | Add staged ramp segments with hold, ramp-up, ramp-down, and target throughput or VUser constraints per segment | L | P4.1 | Partial backend complete: group stages persist and derive compatibility ramp execution |
| P4.4 | Add capacity validation that checks modeled arrivals, VUsers, browser canaries, data-pool rows, and generator placement before save/run | L | P2.1, P4.1 | Partial backend complete: draft validation endpoint reports modeled demand, capacity, browser canary, data-pool, data-source fit, and placement findings |

Acceptance criteria:
- Scenario authors can pick common enterprise workload shapes without hand-building timing rules.
- Invalid load shapes fail before execution with actionable reasons.

### Epic P4.2: Advanced Execution Controls

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P4.5 | Add rendezvous points for synchronized VUser release at named actions | L | P4.1 | Complete: runtime `rendezvous` actions synchronize VUsers and load-model validation reports spike readiness |
| P4.6 | Add global iteration and transaction-count goals across all VUsers, not only per-VUser iteration loops | M | P4.1 | Partial backend complete: runtime enforces group-level `global_iterations` and `max_transactions` across VUsers |
| P4.7 | Add init-once and teardown-once action scopes for shared seed/setup flows | M | P4.1 | Partial backend complete: multi-group runtime honors `initialization: at_once` for group-level init/teardown |
| P4.8 | Add coordinated-omission reporting to historical reports, not only live metrics | M | P4.1, P6.1 | Partial backend complete: archive interval summaries, JSON reports, HTML reports, and Parquet exports preserve target TPS, throughput gap, and miss flags |

Acceptance criteria:
- Spike and once-only setup patterns are modeled explicitly.
- Open workload misses and coordinated omission are visible in reports and release gates.

## Workstream P5: Real-Time Monitoring

### Desired Outcome
Live monitoring stays under the documented two-second target, shows health of the telemetry pipeline itself, and gives operators actionable context during active runs.

### Epic P5.1: Telemetry SLA Enforcement

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P5.1 | Convert telemetry stage budgets into configurable thresholds for generator, transport, controller, fan-out, and browser-render lag | M | none | Complete - system settings now carry configurable telemetry budgets, live diagnostics evaluate measured lag stages against those budgets, and the Settings page exposes the controls. |
| P5.2 | Add sustained telemetry breach detection and run-level health state transitions | L | P5.1 | Complete - live telemetry diagnostics now drive run-level `healthy`, `degraded`, and `breached` transitions with sustained-warning detection and immediate breach on data-loss conditions. |
| P5.3 | Add alert hooks for telemetry freshness, dropped samples, websocket backpressure, and generator stream degradation | M | P5.2 | Complete - telemetry health transitions now emit websocket events and notification webhook events with severity, summary, and condition metadata. |
| P5.4 | Add live telemetry SLA summaries to run archives for post-run confidence review | M | P5.2, P6.1 | Complete - interval archives retain telemetry lag and health state, and archive reports aggregate monitoring confidence evidence for post-run review. |

Acceptance criteria:
- Under-2-second monitoring is enforced as a measurable pipeline budget.
- A run report can prove whether live telemetry was healthy during the run.

### Epic P5.2: Operator Monitoring Experience

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P5.5 | Add live dependency map from transaction dependency metadata and protocol classifications | L | P5.1 | Complete - live metrics now include a source-to-dependency map derived from dependency target rollups, protocol classification, latency, and error health, and the run summary renders the map for operators. |
| P5.6 | Add live error-pattern cards with first-seen, last-seen, affected groups, and snapshot links | M | P5.1 | Complete - failed action metrics retain last error/status context, the runner maintains normalized live error patterns across intervals, and the run summary displays first/latest sightings with affected actions, groups, and snapshot query links. |
| P5.7 | Add live capacity panel showing allocated, available, saturated, queued, and rejected demand by region and protocol | M | P2.1 | Complete - live metrics now include capacity status with allocated, available, saturated, queued, and rejected demand by protocol and assignment region, and the run status panel renders those live breakdowns. |
| P5.8 | Add monitor presets for run operator, performance engineer, platform admin, and release approver views | M | P5.5 | Complete - graph customization now includes role presets for run operator, performance engineer, platform admin, and release approver views with persisted chart order/visibility. |

Acceptance criteria:
- Active-run dashboards answer "is the test healthy?", "is the system under test healthy?", and "is Stormrunner healthy?" separately.
- Capacity and telemetry problems are distinguishable from application failures.

## Workstream P6: Historical Reporting

### Desired Outcome
Completed runs produce durable evidence for release decisions: standalone reports, trend analysis, baselines, provenance, raw exports, and explainable differences across runs.

### Epic P6.1: Archive And Report Model

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P6.1 | Define report data tiers: summary, intervals, sampled diagnostics, transaction archive, browser artifacts, provenance, and external metrics | M | none | Complete - archive reports now declare evidence tiers with present/missing status, counts, descriptions, and missing reasons, and the archive UI renders the tier coverage. |
| P6.2 | Add archive retention and pruning policies by project, release, run type, artifact type, and compliance class | L | P1.8 | Partial backend complete - system settings now persist archive retention defaults and scoped policies, the retention planner identifies expired archive bundles, and `POST /api/v1/archives/retention/plan` / `apply` provide dry-run and audited prune execution. Compliance-class artifact splitting remains future work. |
| P6.3 | Add report schema versioning and compatibility checks for standalone HTML, JSON, CSV, and Parquet outputs | M | P6.1 | Partial backend/UI complete - JSON and standalone HTML reports now declare report schema version, supported archive manifest schemas, output compatibility for JSONL and Parquet artifacts, and unsupported CSV export status for downstream tooling. |
| P6.4 | Add report completeness scoring for missing intervals, telemetry gaps, archive writer errors, and artifact truncation | M | P6.1 | Partial backend/UI complete - archive reports now calculate a completeness score with findings for missing intervals, telemetry gaps, writer errors, count mismatches, and missing artifacts, and the archive UI/HTML report surfaces the score. |

Acceptance criteria:
- Reports declare what evidence is present, missing, truncated, or generated under telemetry degradation.
- Retention behavior is explicit and auditable.

### Epic P6.2: Trend And Release Analytics

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P6.5 | Add release-level trend views for latency, throughput, error rate, SLA status, capacity, and resource signals | L | P6.1 | Partial UI complete - dashboard release trends now summarize SLA breaches, average report completeness, peak VUsers, CPU, memory, telemetry breaches, and per-run resource/SLA/completeness signals alongside latency, throughput, and error rate. |
| P6.6 | Add baseline comparison policy: pinned, latest successful, release default, or manually selected | M | P6.1 | Partial backend/UI complete - comparison baselines now expose the selected policy, pinned baselines win, and automatic selection prefers the latest successful comparable run before falling back to latest finished. |
| P6.7 | Add change attribution fields for script revision, scenario revision, environment, generator version, data revision, and runtime overrides | M | P1.6 | Partial backend/UI complete - archive reports now surface script, scenario, environment, generator version, data, and runtime override attribution from captured provenance. |
| P6.8 | Add exportable executive, engineer, and audit report profiles | M | P6.5 | Partial backend/UI complete - archive reports now declare executive, engineer, and audit export profiles, standalone HTML accepts a profile selector, and the archive UI exposes profile download links. |

Acceptance criteria:
- Release stakeholders can compare current runs against the right baseline without manual spreadsheet work.
- Reports show whether changes came from application behavior, load model changes, environment changes, or platform changes.

## Workstream P7: Scheduling

### Desired Outcome
Scheduling becomes an enterprise control plane for recurring, queued, blackout-aware, quota-aware, and approval-aware test execution.

### Epic P7.1: Schedule Governance

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P7.1 | Add schedule ownership, project quotas, and RBAC review to every schedule mutation and materialized run | M | P1.6 | Partial backend complete - schedule mutations now persist project ownership, owner identity, RBAC review metadata, project quota review details, revision/policy fields, and materialized occurrence records in schedule payloads and audit evidence |
| P7.2 | Add blackout windows, maintenance calendars, and environment availability windows | L | P1.8 | Partial backend complete - schedules now persist blackout and availability windows, expose them through the API, include them in schedule audit details, and skip disallowed cron candidates when calculating `next_run_at`; shared maintenance calendars remain future work |
| P7.3 | Add approval workflows for high-cost, off-hours, production, or quota-burst schedules | L | P1.8, P2.7 | Partial backend complete - schedules now support approval policies, pending approval IDs, persisted approval decisions, approve/reject endpoints, project-admin RBAC gating, and due-run blocking while approval is pending |
| P7.4 | Add schedule conflict detection across capacity pools, releases, environments, and project quotas | L | P2.6 | Partial backend complete - schedules now compute conflict summaries from project quota and capacity advisory signals, require conflict resolution before execution, and persist blocked materialization occurrences with `schedule_conflict` reason codes; release/environment conflict sources remain future work |

Acceptance criteria:
- Recurring runs cannot bypass RBAC, quotas, blackout windows, or capacity controls.
- Users can see why a scheduled run was delayed, rejected, or held for approval.

### Epic P7.2: Schedule Reliability And Operations

| ID | Story | Size | Dependencies | Status |
| --- | --- | --- | --- | --- |
| P7.5 | Add scheduler leader-election and replay-safe reconciliation for HA controller deployments | L | P1.2 | Partial backend complete - scenario scheduler now uses a durable database lease so only the active owner materializes due schedules, consults persisted occurrences before materializing due work, advances already-materialized planned times after restart without starting duplicate tests, and preserves same-revision blocked/failed occurrences for operator action; broader multi-controller restart soak evidence remains future work |
| P7.6 | Add schedule materialization audit events with reason codes and originating schedule revision | M | P7.1 | Backend complete for controller-owned schedule decisions - schedule occurrences are persisted with status, reason code, originating schedule revision, planned time, materialized time, and test ID, and the scheduler now writes audit events for materialized, blocked, skipped, and failed occurrence outcomes with reason code, approval, conflict, and test metadata |
| P7.7 | Add missed-run policy: skip, catch up once, catch up all, or require operator decision | M | P7.5 | Backend complete for first-pass policy execution - schedules persist missed-run policy, `skip` and `require_operator_decision` persist visible occurrence outcomes with reason codes, `catch_up_once` explicitly materializes one missed occurrence before advancing to the next future run, and `catch_up_all` materializes each missed planned time in bounded controller sweeps with occurrence/audit records |
| P7.8 | Add schedule health dashboard for due, queued, blocked, failed, skipped, and completed occurrences | M | P7.6 | Partial dashboard complete - `GET /api/v1/schedules/health` reports schedule totals, due/blocked/failed/approval counts, and persisted occurrence status counts; the Monitoring page now polls that endpoint and renders an operator card for due, blocked, pending approval, materialized, skipped, and completed occurrence pressure |

Acceptance criteria:
- Controller restarts do not duplicate scheduled runs or silently drop due work.
- Operators can audit each scheduled occurrence from recurrence rule to materialized run.

## Cross-Workstream Dependencies

| Dependency | Required By |
| --- | --- |
| HA controller and scheduler ownership | P1, P7 |
| Generator fleet health and capacity model | P2, P4, P5, P7 |
| Usage accounting and tenant ownership | P1, P2, P6, P7 |
| Unified authoring health model | P3, P4 |
| Typed telemetry and archive schema versions | P5, P6 |
| RBAC/audit foundation | P1, P3, P7 |

## Delivery Waves

| Wave | Scope | Exit Criteria |
| --- | --- | --- |
| Wave 1: Architecture Contracts | P1.1-P1.5, P6.1, P7.5 design | Operators can reason about ownership, HA responsibilities, and state boundaries. |
| Wave 2: Fleet And Capacity | P2.1-P2.8, P4.4, P7.4 | Capacity and placement decisions are explainable and protocol-aware. |
| Wave 3: Authoring And Load Modeling | P3.*, P4.1-P4.8 | Scenarios carry source health, workload shape, and advanced execution semantics before run. |
| Wave 4: Monitoring And Reporting | P5.*, P6.2-P6.8 | Live telemetry health and historical evidence support release decisions. |
| Wave 5: Scheduling Governance | P7.* | Recurring execution is quota-aware, blackout-aware, approval-aware, and HA-safe. |

## Release Gates

Before calling platform parity complete:

1. Controller HA and scheduler ownership are documented, tested, and restart-safe.
2. Generator fleet placement can explain capacity, protocol, version, drain, and regional decisions.
3. Script authoring health scoring is source-agnostic and enforced before save/run.
4. Closed, open, staged, goal-based, and rendezvous workload models have validation and reporting coverage.
5. Live telemetry SLA budgets are enforced and archived.
6. Historical reports include provenance, completeness scoring, baseline policy, and trend analytics.
7. Schedules honor RBAC, quotas, blackout windows, approvals, and HA reconciliation.

`GET /api/v1/system/parity-status` exposes the current machine-readable parity gate summary across architecture, fleet, authoring, load modeling, monitoring, reporting, and scheduling. Each area reports status, evidence endpoints, remaining work, and release-gate participation.

## Success Metrics

- Time from generator failure to operator-visible placement/drain diagnosis.
- Percentage of runs with complete provenance and report completeness score above target.
- Live telemetry samples delivered within the under-2-second SLA.
- Scheduled occurrences with explicit materialization outcome and reason code.
- Runs rejected before execution due to actionable capacity/load-model validation rather than failing mid-run.
- Imported or generated scripts reaching first successful replay without manual JSON editing.

## Related Documents

- `docs/LOADRUNNER_PARITY_EXECUTION_PLAN.md`
- `docs/PLATFORM_GAP_ANALYSIS.md`
- `docs/PLATFORM_GAP_IMPLEMENTATION_PLAN.md`
- `docs/RUNTIME_TELEMETRY_SLA_PLAN.md`
- `docs/SCRIPT_AUTHORING_CONSOLIDATION_ROADMAP.md`
- `docs/MOBILE_BROWSER_EMULATION_PLAN.md`
- `docs/BROWSER_CLIENT_METRICS_PLAN.md`
