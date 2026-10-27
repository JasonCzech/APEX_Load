# Enterprise Platform Runbook

Runbooks and reference procedures for enterprise data services, platform operations, architecture, and database migration.


## Enterprise Data Platform Operations Runbook

This runbook covers the production data platform split used by HA APEX Load deployments:

- PostgreSQL is the authoritative metadata store.
- Redis or Valkey is ephemeral live state, idempotency, rate-limit, and websocket fanout coordination.
- Object storage owns immutable run archive evidence.
- Prometheus-compatible remote-write owns long-term operational metrics.

SQLite, filesystem archives, and in-memory cache remain supported for local development and single-controller evaluations.

### Readiness Gate

Use `GET /api/v1/system/readiness` before adding a controller pod to service. A production-ready response should include:

| Check | Expected production state |
| --- | --- |
| `metadata_store` | `ready`, `store_type=postgres`, migration version present, lease capability enabled |
| `archive_writer` | `ready`, object backend configured, staging directory writable |
| `cache_backend` | `ready`, `cache_backend=redis`, Redis URL configured, fanout channel visible |
| `generator_rpc` | `ready` or `degraded` only when a known generator is temporarily unreachable |
| `telemetry_fanout` | `ready`, websocket hub initialized |
| `auth_mode` | `ready`, trusted-header or OIDC mode for production |
| `scheduler_state` | `ready` or `degraded` with explicit schedule errors |

`cache_backend=redis` returning `not_ready` means the controller should stay out of service. Redis is not durable state, but losing it in HA mode breaks shared live state, fanout, API-key throttling, and idempotency protection.

### PostgreSQL Backup And Restore

Use managed PostgreSQL point-in-time recovery where possible. At minimum:

1. Enable continuous WAL archiving and daily logical dumps.
2. Retain backups longer than the longest audit and reporting retention policy.
3. Test restore into an isolated database before each major release.
4. Validate counts after restore for domains, repositories, categories, projects, releases, scripts, scenarios, tests, schedules, schedule occurrences, approvals, audit events, service accounts, usage snapshots, and archive metadata.

PostgreSQL is the only source of truth for scheduler leases and schedule occurrence reconciliation. Do not use Redis locks as a substitute for database leases.

### Object Archive Recovery

Run archives are immutable evidence. Configure bucket/container versioning and lifecycle rules outside APEX Load:

- Keep `manifest.json`, transaction CSV/JSONL, resource samples, summaries, reports, browser evidence, and Parquet artifacts under the same run prefix.
- Use retention apply events from APEX Load as the audit trail for prune/delete decisions.
- For accidental object deletion, restore the complete run prefix before serving archive downloads.
- If only local staging data is lost, APEX Load can materialize missing local files from object storage when archive metadata includes object URIs.

### Redis Or Valkey Loss

Redis/Valkey stores ephemeral state only. A cache restart may lose:

- active run snapshots used by dashboards,
- generator inventory cache entries,
- idempotency reservations,
- service-account rate-limit counters,
- websocket fanout messages in flight.

Expected recovery:

1. Controllers reconnect and readiness returns to `ready`.
2. Fresh live metrics repopulate active run snapshots.
3. Generator inventory cache repopulates on the next inventory/readiness pass.
4. Duplicate idempotency protection starts from an empty window after cache loss.
5. Durable run state, schedules, approvals, and archives remain intact in PostgreSQL/object storage.

For HA production, run Redis/Valkey as a managed service or use a replicated operator. The checked-in `deployments/k8s/ha/redis-valkey.example.yaml` is a reference baseline, not a complete managed-cache replacement.

### Metrics Retention

APEX Load exposes compact runtime metrics and writes typed summaries to SQL/archive evidence. Long-term high-cardinality operational analysis belongs in the Prometheus-compatible backend.

Recommended retention boundaries:

- Live dashboard: seconds to minutes, websocket/cache backed.
- SQL interval summaries: release comparison and report queries.
- Prometheus remote-write: fleet, controller, and generator operations over weeks/months.
- Object archives: immutable run evidence according to compliance class.

### HA Controller Notes

Use at least two controller replicas with:

- shared PostgreSQL,
- shared Redis/Valkey,
- shared object archive backend,
- readiness probe on `/api/v1/system/readiness`,
- pod anti-affinity across nodes,
- durable scheduler leases in PostgreSQL.

The example HA deployment is in `deployments/k8s/ha/controller-ha-deployment.example.yaml`.


## Enterprise Platform Architecture Runbook

This runbook defines the production reference architecture for APEX Load as a shared enterprise performance testing platform.

### Reference Architecture

| Component | Primary responsibility | Durable state ownership |
| --- | --- | --- |
| Controller API | REST API, WebSocket streams, Auth/RBAC, scheduling, orchestration, capacity advice, archive/report APIs | Metadata records, run state, settings, audit events |
| Scenario scheduler | Materializes due scenario schedules into queued runs | Schedule records and materialized run records |
| Load generators | Execute protocol load, stream metrics, produce per-run artifacts | Durable execution-generation high-water and retained run artifacts; no metadata intent ownership |
| Dashboard | Static React SPA served by the controller | No server-side state |
| Metadata store | Projects, applications, releases, scripts, scenarios, schedules, settings, service accounts, tests, audit events | System of record |
| Archive store | Immutable run bundles, manifests, JSONL/Parquet exports, reports, browser diagnostics | Evidence and reporting artifacts |
| Telemetry pipeline | Hot-path live metrics, Prometheus scrape output, WebSocket fan-out | Transient live state and downstream metrics |

Generators are replaceable workers. The controller and metadata store own intent, governance, scheduling decisions, and run history. Archive artifacts are immutable evidence once a run completes.

### State Ownership

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

### HA Controller Behavior

APEX Load can run multiple controller API replicas when they share PostgreSQL,
Redis/Valkey, and the configured archive backend. Execution is intentionally
single-owner even though read/API traffic is multi-replica. With
`APEXLOAD_CONTROLLER_HA=true`, a durable lease records an acquisition-specific
owner token, expiry, and monotonic generation. Execution-state writes verify the
owner/generation in the same database transaction; a newly elected replica stays
unadmitted until durable recovery finishes, and leadership loss synchronously
closes admission. Local runs and default `fail` policy distributed runs stop;
armed `continue` policy distributed runs can be handed off to a successor within
the bounded adoption grace.

The execution owner gates scenario and execution-plan materialization, queued-run
promotion/reconciliation, provisioning recovery, usage-accounting cadence,
archive finalization recovery, and retention mutations. Scenario occurrence and
plan-run uniqueness/CAS fences remain in place as a second layer. Observational
work such as read serving and generator inventory probes may run per replica.
In Kubernetes, execution/CI mutations use the selectorless active Service; its
EndpointSlice update is fenced by the same durable owner and generation.

This implementation does not by itself certify an AKS deployment. Keep one
controller by default. Before enabling multiple production replicas, pass a real
pod-termination/network-disruption exercise with PostgreSQL, Redis, the object
archive backend, and generators. The deterministic repository soak is described
in the AKS README [Integration readiness](../deployments/k8s/aks/README.md#integration-readiness) section and is a process-loss simulation, not that
production evidence.

All reconciler loops must be idempotent:

- Schedule materialization uses a uniqueness key of `(schedule_id, planned_fire_time)` before creating a run.
- Queued-run reconciliation reads current status before mutating and only transitions from valid predecessor states.
- Generator inventory reconciliation replaces observations rather than accumulating stale capacity.
- Archive retention never rewrites completed manifests; it only updates retention metadata or removes eligible artifacts after policy checks.

### Operating run continuity

`assignment.failover_policy` defaults to `fail`. Opt-in `continue` requires a
distributed run, HA execution fencing backed by shared PostgreSQL, and a key
wrapper that every successor can use to open the persisted adoption credential.
Check launch warnings and the run's public `continuity.armed` state; selecting
`continue` alone does not establish recoverability. Local VUsers stop with their
controller. Generator adoption capability and the exact persisted run lineage
must be accepted by the successor.

During bounded orphan grace, surviving generators continue load and retain local
artifacts. A higher-generation claim adopts the existing work and rotates its
supervision token; it does not replay the start. Expired grace or rejected
adoption follows fail-closed cleanup. Preserve generator epoch high-water state,
controller checkpoints, wrapping keys and retained archive sources across
restarts; never erase a fence to make adoption succeed. A replacement pod cannot
restore the old goroutines or protocol sessions from a volume.

Inspect `/api/v1/system/execution-readiness` for recovery/admission state and the
run's continuity history for adopted generations and observation gaps. Missing
observations remain gaps, not zero load. Verify terminal archive completeness;
partial or missing generator evidence cannot become a complete result merely
because execution reached a terminal status.

The [HA design and evidence record](HA_RUN_CONTINUITY_PLAN.md) preserves decisions,
protocol details and measured controller-failover results. Follow the
[node storage recovery procedure](NODE_STORAGE_RECOVERY.md) for state restoration.
Combined controller/generator loss and production node-loss qualification remain
open in the [node-failure readiness ledger](NODE_FAILURE_READINESS_PLAN.md);
controller-only adoption and same-host trials do not close those gates.

### Resolving an unproven generator evacuation

A canceled evacuation can stay `STOPPING` when a restarted replacement rejects
the old generation and cannot acknowledge an exact-generation stop. For the
current controller's canceled run with a durable evacuation journal, automatic
cleanup makes one initial round and at most five retry rounds. Each participant
RPC retains its existing bounded timeout and retry count. Exhaustion records
`automatic evacuation stop retries exhausted` durably before the worker retires.
A failed diagnostic write remains retryable without sending more stop rounds.

The run remains `STOPPING`; its possible participants and capacity stay held.
The run error preserves the original cause, unresolved stop errors and the
resolution action. A canceled controller context, expired lease, missing
heartbeat, Kubernetes `NotReady` state or reused pod name is not proof that a
process stopped. Do not erase epochs or change the global owner generation to
force cleanup.

A system administrator may use
`POST /api/v1/tests/{id}/interrupted-stop/resolve` only after independently
establishing that **every generator process that could execute the run is stopped
or fenced**, including staged replacements and old process incarnations. Send
`{"attestation":"all-generator-processes-stopped","note":"<operator evidence>"}`.
The route is audited. A missing attestation is rejected; an active runtime or an
already resolved run is ineligible. Transient persistence/ownership failure
returns an error and must not be treated as successful resolution.

A successful resolution first persists `FAILED` and the explicit proof while
preserving the cause. Cleanup then closes retained transports and reconciles
archive failure and capacity. Missing archive evidence stays incomplete;
capacity that lacks its own safe-release proof stays quarantined. The operation
does not create a successful result, advance the execution epoch or delete
retained source evidence. Successor recovery continues to use its existing
all-participant process-fence protocol. Terminal/stale-runtime cleanup and runs
without an eligible evacuation journal retain their existing cleanup contracts.

The same-leader intervention review records the local failure, persistence and
authorization regressions. Deployed
multi-host failure qualification remains pending. Historical successor proof
wording remains readable. Older controllers do not recognize the new same-leader
proof for post-terminal cleanup; retain the record and upgrade the cleanup owner
instead of erasing its journal or inventing another acknowledgement.

### Restart Contracts

| Condition | Expected behavior |
| --- | --- |
| Controller restarts with queued tests | Reload queued tests and continue capacity/start reconciliation |
| Controller restarts during local run | The successor claims the new execution term and marks an unresolved in-process run failed; a committed terminal manifest can be recovered through the archive outbox |
| Controller restarts during distributed run | Default `fail` policy stops stale work and fails unresolved runs without replaying the start. An armed `continue` run can be adopted within its grace; failed adoption must reconcile stopped work. Retained terminal artifacts remain eligible for archive recovery |
| Scheduler restarts | Evaluate persisted `next_run_at` values and materialize due schedules once |
| Archive writer restarts mid-run | Preserve partial files for diagnosis; final manifest is written only after successful finalize |
| Dashboard static assets missing | API remains available; readiness reports dashboard/static asset state once that check is added |

### Production Readiness Surface

`GET /api/v1/system/readiness` returns a machine-readable readiness response (see [Readiness Gate](#readiness-gate) for the expected production check states). Overall status is `ready`, `degraded`, or `not_ready`; `not_ready` returns HTTP 503 so Kubernetes readiness probes can hold traffic away from an unsafe controller.

The dashboard renders this payload in **Settings → Controller & runtime** (`#/settings?setting=platform`) and as a compact Monitoring card, including the aggregate state, per-check state, messages, and detail fields. Use these surfaces before release windows or controller maintenance to confirm the backend readiness probe matches what platform users will see.

### Upgrade And Migration Policy

Metadata migrations must be additive, idempotent, and safe to run at controller startup. SQLite remains suitable for local development and single-node evaluation. PostgreSQL is the production target for shared enterprise deployments because controller replicas and singleton leases need shared transactional coordination.

Archive bundles carry schema versions in both metadata and manifest files. Readers should preserve backwards compatibility for supported archive schema versions and expose report-completeness warnings for missing optional files.

Dashboard static assets are versioned with the controller binary. Production rollout should build the dashboard before packaging the controller and verify that root SPA routing serves `index.html`.

PostgreSQL integer width (one-time, on upgrade): earlier releases created every
schema `INTEGER` column as PostgreSQL `int4`, which cannot hold the int64 values
the schema stores (a test longer than about 2.1s could not be saved). The
controller now widens every int4 column in APEX Load's own tables to `BIGINT`
at startup. This is lossless, but PostgreSQL rewrites each affected table under
an `ACCESS EXCLUSIVE` lock, so the first start after upgrading takes time
proportional to table size; each table logs `widened PostgreSQL int4 columns to
BIGINT` with its elapsed time. Replicas serialize on an advisory lock, and later
starts skip the step. During a rolling upgrade, replicas still running the old
binary can log a transient `cached plan must not change result type` once per
cached statement; upgrading all replicas clears it. Tables in the same schema
that APEX Load did not create are never altered.

The controller also refuses to start with `APEXLOAD_CONTROLLER_HA=true` unless
`APEXLOAD_STORE=postgres`. The store backend is selected by `APEXLOAD_STORE`,
not by the presence of `APEXLOAD_DATABASE_URL`, which is ignored (with a
warning) on the SQLite store.

Rollback expectations:

- Additive metadata columns may remain after rollback.
- Columns widened from `int4` to `BIGINT` stay `BIGINT`; older binaries read and write them unchanged.
- Older binaries may ignore newer optional archive fields.
- Destructive migrations require an explicit backup and release note.

### Failure Modes And Recovery

| Failure | Detection | Recovery |
| --- | --- | --- |
| Metadata store unavailable | `metadata_store=not_ready`, API mutation errors | Restore database connectivity, then restart or let controller retry |
| Archive root unwritable | `archive_writer=not_ready`, run finalize failures | Fix permissions or storage mount, then rerun affected tests if evidence is incomplete |
| All distributed generators unreachable | `generator_rpc=not_ready`, placement failures | Restore generator pods/services or switch to local execution only for small diagnostics |
| Some generators unreachable | `generator_rpc=degraded` | Drain or replace failing generators; capacity advice reflects available inventory |
| Auth bypass in production | `auth_mode=degraded` | Configure trusted headers or OIDC before exposing shared environments |
| Scheduler has prior errors | `scheduler_state=degraded` | Inspect schedule `last_error`, scenario references, release references, and capacity policy |
| Controller restart during run | Default: run ends `FAILED` ("controller restarted before the run completed"). With `assignment.failover_policy=continue` on HA: the run stays `RUNNING`, `continuity.failovers` records the adoption, and the report gains a *Controller Failover* section | Default: review audit events, generator logs, and archive completeness before promoting results. Continue-policy runs: results are complete; check the failover section's unobserved gap and whether controller-driven shapes (stages, profiles, goals) moved during it. Adoption failures end `FAILED` with a per-generator reason (e.g. `generator predates run adoption`) |

### Operator Checklist

Before promoting an environment:

1. Configure a production metadata store.
2. Configure a durable archive root or object-store mounted path.
3. Set production Auth/RBAC mode.
4. Confirm `/api/v1/system/readiness` is `ready` or only intentionally `degraded`.
5. Confirm `/api/v1/system/inventory` reports expected generator regions and capacities.
6. Run a smoke scenario and verify live metrics, archive manifest, and report download.
7. Keep one controller replica unless HA prerequisites are configured. For HA,
   verify the shared PostgreSQL/Redis/archive backends, active EndpointSlice,
   durable generator state, and failover evidence in
   the AKS README [Integration readiness](../deployments/k8s/aks/README.md#integration-readiness) section.


## SQLite To PostgreSQL Migration Guide

APEX Load supports SQLite for local development and PostgreSQL for shared enterprise deployments. Use this guide when promoting an existing SQLite-backed controller to PostgreSQL.

### Preconditions

- Stop all controllers that write to the SQLite database.
- Back up the SQLite file and archive directory.
- Provision PostgreSQL 16 or newer.
- Set object archive storage before migrating production evidence when possible.
- Run the target controller once against PostgreSQL in a staging environment to initialize schema migrations.

### Export

Create a consistent SQLite export:

```bash
sqlite3 "$APEXLOAD_DB" ".backup apexload-migration.sqlite"
sqlite3 apexload-migration.sqlite ".dump" > apexload-migration.sql
```

For large databases, prefer a small migration utility that copies table-by-table with typed inserts instead of replaying raw SQLite DDL. The SQL dialects differ; the important artifact is the data, not the SQLite schema syntax.

### Import Strategy

1. Start with an empty PostgreSQL database.
2. Run the APEX Load controller once with:

```bash
APEXLOAD_STORE=postgres
APEXLOAD_DATABASE_URL=postgres://apexload:***@postgresql:5432/apexload?sslmode=require
```

3. Stop the controller after schema initialization succeeds.
4. Copy data in dependency order:

| Order | Data |
| --- | --- |
| 1 | domains, application repositories, repository categories |
| 2 | projects, applications compatibility rows, releases, folders |
| 3 | scripts, authoring sessions, data pools |
| 4 | scenarios, scenario scripts, schedules |
| 5 | schedule occurrences, approvals, scheduler leases |
| 6 | tests/runs, interval summaries, usage snapshots |
| 7 | run archive records, retention events, audit events |
| 8 | service accounts, service-account roles, settings |

5. Preserve IDs exactly. Existing scripts, scenarios, runs, archive metadata, RBAC grants, and dashboard links depend on stable IDs.

### Validation Counts

After import, compare source and target counts for:

- domains
- application repositories
- repository categories
- projects
- releases
- folders
- scripts
- authoring sessions
- data pools and rows
- scenarios and scenario scripts
- schedules, occurrences, and approvals
- tests/runs
- interval summaries
- run archive records and retention events
- audit events
- service accounts and role mappings
- usage snapshots
- settings

Then call:

```bash
curl -fsS "$APEXLOAD_URL/api/v1/system/readiness"
curl -fsS "$APEXLOAD_URL/api/v1/system/inventory"
```

Expected production values include `metadata_store=postgres`, a reachable archive backend, and `cache_backend=redis` for HA.

### Cutover

1. Configure all controllers with the same PostgreSQL URL, shared object archive backend, Redis/Valkey URL, production auth mode, and `APEXLOAD_CONTROLLER_HA=true`. Do not add replicas until PostgreSQL, shared archives, Redis/Valkey coordination, and the execution lease are healthy.
2. Start one controller and verify readiness.
3. Start additional controller replicas with `APEXLOAD_CONTROLLER_HA=true` and identical shared-service settings. See [HA Controller Behavior](ENTERPRISE_PLATFORM_RUNBOOK.md#ha-controller-behavior) for the execution-leader lease contract.
4. Verify scripts, scenarios, schedules, recent runs, reports, archive downloads, service accounts, and audit queries.
5. Keep the SQLite backup until at least one full reporting and retention cycle completes.

### Rollback

Rollback is a service-level decision:

- Stop PostgreSQL-backed controllers.
- Restore the original SQLite file and filesystem archives.
- Start a single SQLite-backed controller.
- Do not run SQLite and PostgreSQL controllers against the same production traffic at the same time.
