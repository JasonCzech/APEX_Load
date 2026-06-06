# Enterprise Data Platform Upgrade Plan

## Summary

Upgrade APEX Load's production data platform from the current local-first SQLite/filesystem/in-memory model to an enterprise-ready split:

- PostgreSQL for authoritative control-plane metadata, governance, audit, scheduling, and run summaries.
- Prometheus remote-write plus long-term metrics storage for operational time-series.
- Object storage for immutable run archives, JSONL, Parquet, reports, and browser evidence.
- Redis or Valkey for ephemeral live state, cache, idempotency, coordination, and fanout support.

SQLite and filesystem archives remain the default for local development and single-node evaluation.

## Key Architecture Decisions

| Data family | Production owner | Local/dev owner | Notes |
| --- | --- | --- | --- |
| Domains, repositories, projects, scripts, scenarios, schedules, RBAC, settings, service accounts, audit events | PostgreSQL | SQLite | PostgreSQL is the production system of record. |
| Queued/terminal run metadata and interval summaries | PostgreSQL | SQLite | Keep typed summaries queryable; do not store every raw transaction in SQL. |
| Live run state, websocket fanout support, idempotency keys, rate limits, short-lived API caches | Redis/Valkey | In-memory fallback | Redis is not authoritative durable state. |
| High-cardinality operational metrics | Prometheus remote-write to Mimir, Thanos, or managed Prometheus | Local Prometheus scrape or `/metrics` only | APEX Load continues exposing `/metrics`; the metrics backend owns retention. |
| Full-fidelity run evidence | Object storage | Filesystem archive directory | Archives remain immutable and schema-versioned. |
| SQL-native trend analytics | Optional TimescaleDB extension on PostgreSQL | SQLite summaries | Add only after PostgreSQL is in place and dashboard trend needs justify it. |

## Implementation Phases

### Phase 1: Storage Backend Configuration And PostgreSQL Store

Status: implemented for backend selection, pgx-backed PostgreSQL initialization, shared SQL dialect translation, readiness/inventory metadata, and storage factory tests. Integration against a live PostgreSQL instance is gated by deployment credentials.

- Add storage backend selection:
  - `APEXLOAD_STORE=sqlite|postgres`, default `sqlite`.
  - `APEXLOAD_DB` remains the SQLite path.
  - `APEXLOAD_DATABASE_URL` is required for `postgres`.
  - `APEXLOAD_DATABASE_MAX_OPEN_CONNS`, `APEXLOAD_DATABASE_MAX_IDLE_CONNS`, and `APEXLOAD_DATABASE_CONN_MAX_LIFETIME` tune pooling.
- Implement a PostgreSQL-backed `storage.Store` using `database/sql` with pgx's stdlib driver.
- Keep the existing `storage.Store` interface as the compatibility contract; add methods only when a data-access pattern cannot be represented safely.
- Create PostgreSQL migrations matching the current SQLite schema, including the new domain/application repository hierarchy, scheduler leases, occurrences, approvals, service account roles, usage snapshots, typed interval summaries, and run archive metadata.
- Make migrations additive and idempotent. Run them at controller startup for both SQLite and PostgreSQL.
- Add readiness details for store type, migration version, connection status, and lease capability.

### Phase 2: Object Archive Store

Status: implemented for archive backend selection, object URI metadata, object-backend staging roots, readiness validation, persisted archive URI fields, provider-backed upload, materialization fallback, and deployment configuration examples.

- Introduce an archive storage abstraction with filesystem and object-store implementations.
- Add configuration:
  - `APEXLOAD_ARCHIVE_BACKEND=filesystem|s3|azure_blob|gcs`, default `filesystem`.
  - Keep `APEXLOAD_ARCHIVE_DIR` for filesystem mode.
  - Add provider-specific bucket/container, prefix, region/account, endpoint, and credentials-by-environment configuration.
- Store archive metadata in PostgreSQL/SQLite while writing archive content to object storage.
- Preserve current archive paths as compatibility locators, but normalize new records to provider URI fields such as `s3://bucket/prefix/run-id/manifest.json` or `azblob://container/prefix/run-id/manifest.json`.
- Keep archive bundles immutable after finalize. Retention apply may delete eligible objects and must persist audited prune events.
- Ensure archive download, report, comparison, browser diagnostics, JSONL, CSV, and Parquet endpoints work identically across filesystem and object storage.

### Phase 3: Enterprise Time-Series Retention

Status: implemented for Prometheus ServiceMonitor and remote-write deployment examples, inventory retention metadata, readiness-visible archive/metadata configuration, and operator documentation.

- Keep APEX Load's hot-path telemetry model: compact WebSocket summaries, typed interval summaries, full-fidelity archives.
- Add production deployment artifacts for Prometheus scraping and remote-write:
  - Prometheus scrape config or ServiceMonitor.
  - Remote-write examples for Grafana Mimir, Thanos Receive, and managed Prometheus-compatible endpoints.
  - Recommended labels: environment, domain, project, release, scenario, run, protocol, generator region, generator id.
- Do not ingest raw transaction events into PostgreSQL or Redis.
- Add dashboard/operator documentation explaining which questions are answered by live metrics, PostgreSQL summaries, Prometheus long-term metrics, and archives.
- TimescaleDB follow-up: the hypertable wiring is now implemented but **disabled by default**. Set `APEXLOAD_TIMESCALEDB=true` (PostgreSQL backend only) to have controller startup run `CREATE EXTENSION IF NOT EXISTS timescaledb` and convert `test_interval_summaries` into a hypertable (see `pkg/storage` `applyTimescaleDB` / `timescaleSetupStatements`). The flag is surfaced via `/api/v1/system/readiness` (`timescaledb_enabled`) and `StoreBackendInfo`.
  - Wired today: `test_interval_summaries` (no unique constraints — converts cleanly).
  - Gated/deferred: `usage_accounting_snapshots` (PRIMARY KEY `id`) and archive-resident resource samples require a composite key including the partition column before conversion; extend `timescaleHypertables` once those schema changes land.
  - Live conversion is verified against a TimescaleDB-enabled PostgreSQL instance only when the flag is enabled in a real deployment; default builds/tests cover config parsing, statement generation, and the SQLite/non-Postgres no-op path.

### Phase 4: Redis/Valkey Live State And Coordination

Status: implemented for backend selection, memory and Redis/Valkey cache implementations, active run snapshots, generator inventory snapshots, idempotency-key reservations, service-account API-key rate limiting, websocket fanout publishing/subscription, readiness degradation, and inventory metadata.

- Add cache backend selection:
  - `APEXLOAD_CACHE_BACKEND=memory|redis|valkey`, default `memory`. `valkey` is a
    Redis-wire-compatible alias that uses the same client/fanout path and reports
    `cache_backend: valkey` in readiness/inventory.
  - `APEXLOAD_REDIS_URL` is required for Redis/Valkey mode.
  - `APEXLOAD_REDIS_KEY_PREFIX`, `APEXLOAD_REDIS_TLS`, and timeout settings support shared enterprise clusters.
- Add small interfaces instead of leaking Redis across handlers:
  - `LiveStateStore` for active run snapshots, generator inventory snapshots, and short TTL dashboard summaries.
  - `IdempotencyStore` for guarded mutating API requests and import/run replay protection.
  - `RateLimitStore` for service-account/API-key request limits.
  - `FanoutBus` for optional cross-controller event propagation.
- Keep durable scheduling ownership in PostgreSQL leases. Redis locks must never be the only protection for schedule materialization or run state transitions.
- Add readiness checks for Redis mode and degrade gracefully to `not_ready` when Redis is configured but unavailable.

### Phase 5: HA Deployment And Operations

Status: implemented for HA controller and Redis/Valkey deployment examples, production data-platform config, expanded readiness checks, backup/restore runbook, Redis loss recovery expectations, metrics retention boundaries, and SQLite-to-PostgreSQL migration guidance.

- Add Kubernetes manifests or Helm-ready overlays for:
  - PostgreSQL connection secrets.
  - Redis/Valkey connection secrets.
  - archive object-store credentials.
  - Prometheus scrape and remote-write configuration.
  - controller replicas with singleton background loops protected by PostgreSQL durable leases.
- Update readiness to verify metadata store, archive store, cache backend, telemetry scrape state, scheduler lease capability, and migration version.
- Add backup/restore runbooks:
  - PostgreSQL point-in-time recovery.
  - object storage lifecycle and restore.
  - Redis loss recovery expectations.
  - metrics backend retention and disaster-recovery boundaries.
- Add a migration guide for SQLite-to-PostgreSQL export/import, including validation counts for domains, projects, scripts, scenarios, runs, schedules, audit events, service accounts, and archive metadata.

## Public Interfaces And Configuration

New environment variables:

```text
APEXLOAD_STORE=sqlite|postgres
APEXLOAD_DATABASE_URL=postgres://...
APEXLOAD_DATABASE_MAX_OPEN_CONNS=25
APEXLOAD_DATABASE_MAX_IDLE_CONNS=10
APEXLOAD_DATABASE_CONN_MAX_LIFETIME=30m
APEXLOAD_TIMESCALEDB=false   # opt-in; PostgreSQL only; converts time-series tables to hypertables

APEXLOAD_ARCHIVE_BACKEND=filesystem|s3|azure_blob|gcs
APEXLOAD_ARCHIVE_BUCKET=
APEXLOAD_ARCHIVE_CONTAINER=
APEXLOAD_ARCHIVE_PREFIX=apexload/archives
APEXLOAD_ARCHIVE_REGION=
APEXLOAD_ARCHIVE_ENDPOINT=

APEXLOAD_CACHE_BACKEND=memory|redis|valkey
APEXLOAD_REDIS_URL=redis://...
APEXLOAD_REDIS_KEY_PREFIX=apexload
APEXLOAD_REDIS_TLS=false
APEXLOAD_REDIS_TIMEOUT=2s
APEXLOAD_REDIS_FANOUT_CHANNEL=telemetry:fanout
APEXLOAD_API_KEY_RATE_LIMIT_PER_MINUTE=600
APEXLOAD_IDEMPOTENCY_KEY_TTL=15m
```

API behavior:

- Existing API payloads stay compatible.
- `/api/v1/system/readiness` includes metadata store, archive store, cache backend, metrics export, migration version, and lease capability details. The `cache_backend` check reports a measured `probe_latency_ms` and, for Redis/Valkey, a credential-free `endpoint` (scheme://host:port).
- `/api/v1/system/inventory` includes configured data platform backends without exposing secrets.
- Archive endpoints keep existing URLs and response shapes regardless of backing store.

## Test Plan

- Storage tests:
  - Run the existing storage contract against SQLite and PostgreSQL.
  - Verify migrations create and backfill domain/repository hierarchy, schedules, leases, audit events, service accounts, usage snapshots, run archives, and interval summaries.
  - Verify PostgreSQL transactional behavior for scheduler lease acquisition and occurrence materialization.
- Archive tests:
  - Run archive writer/reader contract against filesystem and mocked S3/Azure/GCS stores.
  - Verify manifest immutability, report generation, CSV/JSONL/Parquet downloads, retention dry-run/apply, and missing-object errors.
- Cache tests:
  - Verify memory and Redis implementations for TTL, idempotency, rate-limit counters, active run snapshots, and fanout publishing.
  - Verify Redis outage handling and readiness degradation.
- Telemetry tests:
  - Verify `/metrics` labels remain bounded.
  - Validate generated Prometheus remote-write/ServiceMonitor manifests.
  - Verify dashboard/operator docs match the emitted metrics and archive tiers.
- Integration tests:
  - Add docker-compose or testcontainer-based PostgreSQL and Redis smoke tests gated by environment flags.
  - Run `go test ./...`, dashboard tests if readiness/monitoring UI changes, and Kubernetes manifest validation.

## Assumptions And Defaults

- SQLite remains supported for local development and evaluation.
- PostgreSQL is mandatory for shared enterprise production deployments.
- Redis/Valkey is optional for single-controller deployments but required for multi-controller live-state coordination.
- Prometheus-compatible long-term storage is preferred over a custom APEX Load time-series database.
- TimescaleDB is optional and should not block PostgreSQL adoption.
- Full-fidelity raw transaction evidence belongs in object storage archives, not PostgreSQL.
- Object storage is the production archive target; local PVC archives remain for development and small single-node installs.
