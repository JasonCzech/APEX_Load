# SQLite To PostgreSQL Migration Guide

APEX Load supports SQLite for local development and PostgreSQL for shared enterprise deployments. Use this guide when promoting an existing SQLite-backed controller to PostgreSQL.

## Preconditions

- Stop all controllers that write to the SQLite database.
- Back up the SQLite file and archive directory.
- Provision PostgreSQL 16 or newer.
- Set object archive storage before migrating production evidence when possible.
- Run the target controller once against PostgreSQL in a staging environment to initialize schema migrations.

## Export

Create a consistent SQLite export:

```bash
sqlite3 "$APEXLOAD_DB" ".backup apexload-migration.sqlite"
sqlite3 apexload-migration.sqlite ".dump" > apexload-migration.sql
```

For large databases, prefer a small migration utility that copies table-by-table with typed inserts instead of replaying raw SQLite DDL. The SQL dialects differ; the important artifact is the data, not the SQLite schema syntax.

## Import Strategy

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

## Validation Counts

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

## Cutover

1. Configure all controllers with the same PostgreSQL URL, shared object archive backend, Redis/Valkey URL, production auth mode, and `APEXLOAD_CONTROLLER_HA=true`. Do not add replicas until PostgreSQL, shared archives, Redis/Valkey coordination, and the execution lease are healthy.
2. Start one controller and verify readiness.
3. Start additional controller replicas with `APEXLOAD_CONTROLLER_HA=true` and identical shared-service settings.
4. Verify scripts, scenarios, schedules, recent runs, reports, archive downloads, service accounts, and audit queries.
5. Keep the SQLite backup until at least one full reporting and retention cycle completes.

## Rollback

Rollback is a service-level decision:

- Stop PostgreSQL-backed controllers.
- Restore the original SQLite file and filesystem archives.
- Start a single SQLite-backed controller.
- Do not run SQLite and PostgreSQL controllers against the same production traffic at the same time.
