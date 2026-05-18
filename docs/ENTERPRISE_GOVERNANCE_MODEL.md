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

This is intentionally a primitive, not a billing engine. It gives quota, fair-share, and capacity-planning work a shared source of usage facts without requiring raw archive replay.

Known limits for the initial primitive:

- Region buckets use requested assignment metadata, not finalized per-generator placement.
- Archive bytes are computed from local archive paths visible to the controller.
- Historical rollups depend on runs loaded into the controller; a future persistent usage table should snapshot terminal runs.

## Audit Extensions

Current audit coverage includes script, scenario, schedule, test lifecycle, service account, settings, release, comparison baseline, recorder, and browser replay actions. Remaining governance events should be added as the related policy engines land:

| Event family | Example actions |
| --- | --- |
| Usage policy | `usage.policy.create`, `usage.policy.update`, `usage.policy.delete` |
| Quota and fair share | `capacity.quota.reject`, `capacity.fair_share.defer`, `capacity.reservation.consume` |
| Schedule decisions | `scenario.schedule.materialize`, `scenario.schedule.skip`, `scenario.schedule.reject`, `scenario.schedule.approval_required` |
| Run promotion | `test.result.promote`, `test.result.reject`, `test.baseline.update` |
| Retention | `archive.retention.prune`, `archive.retention.hold`, `archive.retention.release` |

Audit details should include the policy revision, evaluated project, actor, decision reason, and affected run or schedule ID.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P1.6 Ownership semantics | Complete | Ownership model table |
| P1.7 First-class teams decision | Complete | Teams decision section |
| P1.8 Usage accounting primitives | Complete for initial API surface; persistent snapshots remain future work | `/api/v1/system/usage` |
| P1.9 Governance audit extensions | Defined; implementation continues with policy engines | Audit extensions section |
