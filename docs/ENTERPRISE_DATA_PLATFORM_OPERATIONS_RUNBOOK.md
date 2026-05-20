# Enterprise Data Platform Operations Runbook

This runbook covers the production data platform split used by HA Stormrunner deployments:

- PostgreSQL is the authoritative metadata store.
- Redis or Valkey is ephemeral live state, idempotency, rate-limit, and websocket fanout coordination.
- Object storage owns immutable run archive evidence.
- Prometheus-compatible remote-write owns long-term operational metrics.

SQLite, filesystem archives, and in-memory cache remain supported for local development and single-controller evaluations.

## Readiness Gate

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

## PostgreSQL Backup And Restore

Use managed PostgreSQL point-in-time recovery where possible. At minimum:

1. Enable continuous WAL archiving and daily logical dumps.
2. Retain backups longer than the longest audit and reporting retention policy.
3. Test restore into an isolated database before each major release.
4. Validate counts after restore for domains, repositories, categories, projects, releases, scripts, scenarios, tests, schedules, schedule occurrences, approvals, audit events, service accounts, usage snapshots, and archive metadata.

PostgreSQL is the only source of truth for scheduler leases and schedule occurrence reconciliation. Do not use Redis locks as a substitute for database leases.

## Object Archive Recovery

Run archives are immutable evidence. Configure bucket/container versioning and lifecycle rules outside Stormrunner:

- Keep `manifest.json`, transaction CSV/JSONL, resource samples, summaries, reports, browser evidence, and Parquet artifacts under the same run prefix.
- Use retention apply events from Stormrunner as the audit trail for prune/delete decisions.
- For accidental object deletion, restore the complete run prefix before serving archive downloads.
- If only local staging data is lost, Stormrunner can materialize missing local files from object storage when archive metadata includes object URIs.

## Redis Or Valkey Loss

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

## Metrics Retention

Stormrunner exposes compact runtime metrics and writes typed summaries to SQL/archive evidence. Long-term high-cardinality operational analysis belongs in the Prometheus-compatible backend.

Recommended retention boundaries:

- Live dashboard: seconds to minutes, websocket/cache backed.
- SQL interval summaries: release comparison and report queries.
- Prometheus remote-write: fleet, controller, and generator operations over weeks/months.
- Object archives: immutable run evidence according to compliance class.

## HA Controller Notes

Use at least two controller replicas with:

- shared PostgreSQL,
- shared Redis/Valkey,
- shared object archive backend,
- readiness probe on `/api/v1/system/readiness`,
- pod anti-affinity across nodes,
- durable scheduler leases in PostgreSQL.

The example HA deployment is in `deployments/k8s/ha/controller-ha-deployment.example.yaml`.
