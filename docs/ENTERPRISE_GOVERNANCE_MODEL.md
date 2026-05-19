# Enterprise Governance Model

This document defines Stormrunner's project-first governance model for enterprise parity. It supports P1.6 through P1.9 in `docs/ENTERPRISE_PLATFORM_PARITY_EXECUTION_PLAN.md`.

## Ownership Model

Stormrunner uses projects as the first-class tenant boundary. A project owns authoring assets, execution assets, schedules, data pools, live controls, archives, and retention policy. Global administrators manage platform settings and cross-project visibility.

| Resource | Owner scope | Project behavior | Audit scope | Retention behavior |
| --- | --- | --- | --- | --- |
| Project | Global catalog | Root tenant boundary | Global | Retained until deleted by project/system admin |
| Folder | Project | Organizes scripts and runs | Project | Deleted with project or explicit folder deletion |
| Application | Project | Groups releases and run history | Project | Retained while releases/results reference it |
| Release | Application/project | Governance context for scenario runs | Project | Archived before deletion when history exists |
| Script | Project/folder | Reusable DSL asset | Project | Retained while referenced by scenarios/results |
| Scenario | Derived from scripts/project | Reusable execution definition | Project | Retained with schedule and result references |
| Scenario schedule | Scenario/project | Recurring run intent | Project | Deleted or disabled explicitly |
| Shared data pool | Project | Reusable parameter data | Project | Can be archived to prevent new use |
| Test run | Project/release/scenario | Execution instance | Project | Retained with archive/report policy |
| Run archive | Test run/project | Immutable evidence bundle | Project | Pruned by future retention policy |
| Service account | Global identity, optional project roles | Can be limited to project roles/scopes | Global | Rotated, disabled, or deleted by system admin |
| Audit event | Global or project | Carries project ID when resource-scoped | Global/project | Retained by audit retention policy |

Legacy or imported resources without a known tenant normalize to the `unassigned` project. This keeps old evidence visible while making ownership gaps explicit.

## Teams Decision

Stormrunner does not need first-class teams before project-level quotas and chargeback. Existing identity integration can map IdP groups into global roles or project roles, while projects remain the stable resource boundary.

Add first-class teams only when at least one of these becomes necessary:

- A quota, budget, or approval policy must span multiple projects.
- Chargeback must roll up several projects under one organizational owner.
- Delegated administration needs team-owned service accounts or team-owned schedules.
- External identity groups are too volatile to use directly in policy records.

Until then, use projects for governance and preserve team-like concepts as external identity metadata.

## Usage Accounting Primitive

`GET /api/v1/system/usage` returns usage rollups from current run metadata and archive paths:

- Totals across the query scope.
- Optional `project_id` filter.
- Buckets by project, protocol, and requested generator region.
- Run counts, group counts, total VUsers, duration seconds, VUser-seconds, archive bytes, and status counts.

`GET /api/v1/system/usage/export` returns the same rollups as CSV for chargeback, capacity review, and external governance packs. The export includes one totals row plus project, protocol, and region bucket rows, with deterministic status-count encoding.

`POST /api/v1/system/usage/snapshots` captures the current rollup into durable metadata storage for audit evidence, and `GET /api/v1/system/usage/snapshots` lists saved snapshots. Both support the same optional `project_id` scope as the live usage API; snapshot capture is system-admin only while snapshot reads follow the global reader roles. The controller also captures a project-scoped snapshot when a run reaches a terminal completion path, giving usage history a durable checkpoint without depending on dashboard polling.

For governance windows and scheduled chargeback evidence, `general.usage_snapshot_interval_seconds` enables controller-managed all-project snapshots on a fixed wall-clock cadence. The default is `0` (disabled); set a positive interval in system settings before controller startup to enable automatic cadence capture.

This is intentionally a primitive, not a billing engine. It gives quota, fair-share, and capacity-planning work a shared source of usage facts without requiring raw archive replay.

Known limits for the initial primitive:

- Region buckets use requested assignment metadata, not finalized per-generator placement.
- Archive bytes are computed from local archive paths visible to the controller.
- Cadence capture is controller-local today; multi-controller deployments should run it from one active controller or pair it with an external scheduler until a durable usage-snapshot lease is added.

## Audit Extensions

Current audit coverage includes script, scenario, schedule, test lifecycle, service account, settings, release, comparison baseline, release SLA waivers, external result imports, recorder, browser replay, usage snapshots, retention pruning, and capacity governance actions. Capacity governance audit events are emitted when a run is blocked by insufficient generator capacity, deferred by quota/fair-share policy, or promoted by the system after capacity becomes available.

| Event family | Example actions |
| --- | --- |
| Usage policy | `usage.policy.create`, `usage.policy.update`, `usage.policy.delete` |
| Quota and fair share | `capacity.placement.reject`, `capacity.fair_share.defer`, `capacity.reservation.consume` |
| Schedule decisions | `scenario.schedule.occurrence.materialized`, `scenario.schedule.occurrence.skipped`, `scenario.schedule.occurrence.blocked`, `scenario.schedule.approval_required` |
| Run promotion | `test.pending.promote`, `test.result.promote`, `test.result.reject`, `test.baseline.update` |
| Analysis governance | `release.sla.waiver.create`, `external_result.import.create` |
| Retention | `archive.retention.prune`, `archive.retention.hold`, `archive.retention.release` |

Audit details should include the policy revision, evaluated project, actor, decision reason, and affected run or schedule ID.

Archive retention is exposed as an operator-controlled dry-run/apply workflow:

- `POST /api/v1/archives/retention/plan` returns expired archive candidates from the active retention policy without deleting artifacts.
- `POST /api/v1/archives/retention/apply` removes eligible archive directories and writes `archive.retention.prune` audit events with project, release, run status, policy, artifact type, and retention timing details.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P1.6 Ownership semantics | Complete | Ownership model table |
| P1.7 First-class teams decision | Complete | Teams decision section |
| P1.8 Usage accounting primitives | Complete for first-pass live rollups, CSV export, manual snapshot capture/listing, terminal-run snapshot capture, configurable cadence snapshots, and audit events | `/api/v1/system/usage`, `/api/v1/system/usage/export`, `/api/v1/system/usage/snapshots`, `general.usage_snapshot_interval_seconds` |
| P1.9 Governance audit extensions | Complete for current policy engines: settings, resource lifecycle, usage snapshots, retention pruning, schedule decisions, capacity rejection/defer, and pending-run promotion emit audit evidence | Audit extensions section |
