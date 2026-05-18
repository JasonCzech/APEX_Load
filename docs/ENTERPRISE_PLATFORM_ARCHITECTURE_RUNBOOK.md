# Enterprise Platform Architecture Runbook

This runbook defines the production reference architecture for Stormrunner as a shared enterprise performance testing platform. It supports Workstream P1 in `docs/ENTERPRISE_PLATFORM_PARITY_EXECUTION_PLAN.md`.

## Reference Architecture

| Component | Primary responsibility | Durable state ownership |
| --- | --- | --- |
| Controller API | REST API, WebSocket streams, Auth/RBAC, scheduling, orchestration, capacity advice, archive/report APIs | Metadata records, run state, settings, audit events |
| Scenario scheduler | Materializes due scenario schedules into queued runs | Schedule records and materialized run records |
| Load generators | Execute protocol load, stream metrics, produce per-run artifacts | No durable control-plane state |
| Dashboard | Static React SPA served by the controller | No server-side state |
| Metadata store | Projects, applications, releases, scripts, scenarios, schedules, settings, service accounts, tests, audit events | System of record |
| Archive store | Immutable run bundles, manifests, JSONL/Parquet exports, reports, browser diagnostics | Evidence and reporting artifacts |
| Telemetry pipeline | Hot-path live metrics, Prometheus scrape output, WebSocket fan-out | Transient live state and downstream metrics |

Generators are replaceable workers. The controller and metadata store own intent, governance, scheduling decisions, and run history. Archive artifacts are immutable evidence once a run completes.

## State Ownership

| State family | Owner | Scope | Restart contract |
| --- | --- | --- | --- |
| System settings | Controller API and metadata store | Global | Reload from metadata on startup |
| Projects and folders | Controller API and metadata store | Project | Persisted and audit-scoped |
| Applications and releases | Controller API and metadata store | Project/application | Persisted; releases can be archived when no associated results block removal |
| Scripts | Controller API and metadata store | Project/folder | Persisted JSON DSL plus import metadata |
| Scenarios and schedules | Controller API, scheduler, metadata store | Project/scenario | Schedules reload and resume next due evaluation |
| Test runs | Controller API and metadata store | Project/release/scenario | Queued and terminal runs reload; pending work is reconciled |
| Generator inventory | Distributed execution backend | Controller runtime | Re-probed; not durable control-plane state |
| Live metrics | Controller runtime and WebSocket hub | Test run | Best-effort transient stream; archived summaries provide post-run evidence |
| Run archives | Archive writer/store | Test run | Immutable once finalized; metadata points to manifest path |
| Audit events | Controller API and metadata store | Global/project | Persisted with actor, action, target, and outcome |

## HA Controller Behavior

Stormrunner can run multiple controller API replicas for read-heavy and user-facing traffic only when all replicas share the same metadata and archive stores. Background loops must be single-active before multi-replica production:

- Scenario schedule materialization
- Queued-run reconciliation
- Generator inventory reconciliation
- Archive retention or pruning
- Notification retries

Until leader election or database advisory locks are implemented, run one active scheduler/controller worker and treat additional controller replicas as API-only. A production HA deployment should guard each singleton loop with a lease that records owner identity, lease expiry, and last heartbeat.

All reconciler loops must be idempotent:

- Schedule materialization uses a uniqueness key of `(schedule_id, planned_fire_time)` before creating a run.
- Queued-run reconciliation reads current status before mutating and only transitions from valid predecessor states.
- Generator inventory reconciliation replaces observations rather than accumulating stale capacity.
- Archive retention never rewrites completed manifests; it only updates retention metadata or removes eligible artifacts after policy checks.

## Restart Contracts

| Condition | Expected behavior |
| --- | --- |
| Controller restarts with queued tests | Reload queued tests and continue capacity/start reconciliation |
| Controller restarts during local run | Mark unresolved in-process run as interrupted or failed with an audit event unless the run can be safely reattached |
| Controller restarts during distributed run | Re-probe generator RPC, reattach metrics/archive streams when possible, otherwise mark run as interrupted with generator reachability details |
| Scheduler restarts | Evaluate persisted `next_run_at` values and materialize due schedules once |
| Archive writer restarts mid-run | Preserve partial files for diagnosis; final manifest is written only after successful finalize |
| Dashboard static assets missing | API remains available; readiness reports dashboard/static asset state once that check is added |

## Production Readiness Endpoint

`GET /api/v1/system/readiness` returns a machine-readable readiness response:

- `metadata_store`: metadata store is configured and probeable.
- `archive_writer`: archive root exists and is writable.
- `generator_rpc`: local mode is accepted; distributed mode requires at least one reachable generator.
- `telemetry_fanout`: WebSocket hub and metrics interval are initialized.
- `auth_mode`: production auth modes are ready; bypass/dev modes are degraded.
- `scheduler_state`: scheduler is initialized and reports enabled, due, and errored schedule counts.

Overall status is `ready`, `degraded`, or `not_ready`. `not_ready` returns HTTP 503 so Kubernetes readiness probes can hold traffic away from an unsafe controller.

The dashboard Settings > General surface also renders this payload for operators, including the aggregate state, per-check state, messages, and detail fields. Use the panel before release windows or controller maintenance to confirm the backend readiness probe matches what platform users will see.

## Upgrade And Migration Policy

Metadata migrations must be additive, idempotent, and safe to run at controller startup. SQLite remains suitable for local development and single-node evaluation. PostgreSQL is the production target for shared enterprise deployments because controller replicas and singleton leases need shared transactional coordination.

Archive bundles carry schema versions in both metadata and manifest files. Readers should preserve backwards compatibility for supported archive schema versions and expose report-completeness warnings for missing optional files.

Dashboard static assets are versioned with the controller binary. Production rollout should build the dashboard before packaging the controller and verify that root SPA routing serves `index.html`.

Rollback expectations:

- Additive metadata columns may remain after rollback.
- Older binaries may ignore newer optional archive fields.
- Destructive migrations require an explicit backup and release note.

## Failure Modes And Recovery

| Failure | Detection | Recovery |
| --- | --- | --- |
| Metadata store unavailable | `metadata_store=not_ready`, API mutation errors | Restore database connectivity, then restart or let controller retry |
| Archive root unwritable | `archive_writer=not_ready`, run finalize failures | Fix permissions or storage mount, then rerun affected tests if evidence is incomplete |
| All distributed generators unreachable | `generator_rpc=not_ready`, placement failures | Restore generator pods/services or switch to local execution only for small diagnostics |
| Some generators unreachable | `generator_rpc=degraded` | Drain or replace failing generators; capacity advice reflects available inventory |
| Auth bypass in production | `auth_mode=degraded` | Configure trusted headers or OIDC before exposing shared environments |
| Scheduler has prior errors | `scheduler_state=degraded` | Inspect schedule `last_error`, scenario references, release references, and capacity policy |
| Controller restart during run | Terminal run shows interruption or reattached state | Review audit events, generator logs, and archive completeness before promoting results |

## Operator Checklist

Before promoting an environment:

1. Configure a production metadata store.
2. Configure a durable archive root or object-store mounted path.
3. Set production Auth/RBAC mode.
4. Confirm `/api/v1/system/readiness` is `ready` or only intentionally `degraded`.
5. Confirm `/api/v1/system/inventory` reports expected generator regions and capacities.
6. Run a smoke scenario and verify live metrics, archive manifest, and report download.
7. Enable exactly one active scheduler loop until leader election is implemented.

## Plan Mapping

| Plan item | Status | Artifact |
| --- | --- | --- |
| P1.1 Control-plane boundaries | Complete | This runbook |
| P1.2 HA controller behavior | Complete for architecture contract; implementation remains in P7.5 | HA behavior and restart contract sections |
| P1.3 Production readiness checks | Complete for initial API surface and dashboard operator visibility; extend as new dependencies land | `/api/v1/system/readiness`, Settings > General |
| P1.4 Upgrade and migration policy | Complete | Upgrade and migration policy section |
| P1.5 Operator architecture runbook | Complete | This runbook |
