# Runtime Telemetry SLA Plan

## Objective
Harden Stormrunner's runtime telemetry so the platform can meet the documented target of under 2 seconds from collection to dashboard display while also improving the quality of data retained for post-run analysis.

The key design rule is that not all captured data should travel through the same path. Summary metrics belong in the hot path. Diagnostic samples belong in a bounded near-real-time path. Full-fidelity records belong in durable archive storage.

## Current Runtime Data
Stormrunner already captures a solid synthetic-load baseline:

- Transaction timings, bytes, status, success, and error details
- HTTP timing phases such as DNS, connect, TLS, and TTFB
- Aggregated TPS, error rate, and latency percentiles
- Per-action and per-group summaries
- System CPU, memory, network, and disk metrics
- Goal-state and threshold evaluation
- Network latency probes
- Transaction, resource, and interval archives in JSONL and Parquet
- Browser-specific snapshots for replay and error investigation

## Main Gaps
Compared with a more complete application performance testing telemetry model, the largest gaps are:

- Correlation and dependency metadata are now available in live rollups and archive records, but protocol coverage is still incomplete
- Stage budgets are not yet enforced as explicit alert thresholds across the live telemetry pipeline
- Sustained SLA-breach reporting and baseline-versus-current comparisons are still limited

## Plan

### Phase 0: Lock The SLA Budget
- Define stage budgets for capture, aggregation, serialization, delivery, and dashboard render.
- Treat the under-2-second goal as a composed budget rather than a single undifferentiated target.

### Phase 1: Pipeline Hardening
- Expose collector drop counts.
- Expose websocket drop counts and client fan-out state.
- Bound in-memory live history so long tests do not create unbounded controller memory growth.
- Surface telemetry health in live metric payloads.

### Phase 2: Split Live Summary From Diagnostic Detail
- Keep the main `metrics_update` payload compact and bounded.
- Move slow samples, error samples, and heavy artifacts into separate diagnostic flows.
- Reserve archives for full-fidelity transaction detail.

### Phase 3: Improve Typed Retention
- Replace JSON-blob interval history with typed summary storage.
- Keep interval summaries, sampled events, and archive-grade transaction records as separate storage tiers.
- Add schema versioning for telemetry record families.

### Phase 4: Expand Toward Application Performance Testing
- Add correlation IDs or trace IDs.
- Add dependency target classification and protocol-specific error categories.
- Add ingest lag, freshness age, and pipeline health signals.
- Add baseline-versus-current comparison and sustained SLA breach reporting.

## Initial Implementation Started
The first implementation slice in this repository now does two things:

- Live metrics surface telemetry health counters for dropped collector results and websocket fan-out pressure.
- Managed test history is bounded in memory to prevent unbounded growth during long runs.

This is an intentional first step. It does not complete the telemetry plan, but it turns two major hidden SLA risks into visible runtime signals and prevents one source of controller memory drift.

The second implementation slice now extends freshness measurement to the dashboard edge:

- The controller publishes `collected_at`, `published_at`, and server-side `sample_age_ms` in live telemetry payloads.
- The dashboard records `client_received_at` for each `metrics_update` message and derives `delivery_lag_ms` plus `end_to_end_age_ms`.
- The execution view surfaces those values in the Telemetry Health card so stale samples can be distinguished from websocket fan-out drops.

The third implementation slice now starts separating hot-path summaries from diagnostic events:

- `metrics_update` remains the compact live summary message for charts and hero metrics.
- The controller emits sampled `telemetry_diagnostic` websocket events only when telemetry becomes stale, drops collector samples, drops websocket messages, or recovers to healthy.
- The dashboard stores a bounded recent diagnostic list and exposes it in Monitoring so degradation can be inspected without expanding every live metrics payload.

The fourth implementation slice now improves retention and distributed coverage:

- Interval history is persisted as typed summary rows in SQLite and read back through the existing history interface before falling back to legacy JSON blobs.
- Distributed generator metrics batches now carry generator-side dropped-result and freshness telemetry so the controller can aggregate remote telemetry instead of inferring it after the fact.
- The controller Monitoring page was revalidated against a restarted embedded dashboard bundle, including the sampled Telemetry Diagnostics card.

The fifth implementation slice now adds first-class correlation and dependency metadata:

- `protocol.Result` now carries `correlation_id`, `dependency_type`, and `dependency_target` metadata so protocols can emit downstream context without inventing a separate live event type.
- HTTP and JMS implementations classify dependency targets and stamp correlation identifiers, while engine action rollups and distributed transaction batches preserve bounded correlation samples plus aggregate correlation counts.
- Archive transaction records and Parquet exports now retain the same metadata, and archive queries can filter by correlation ID, dependency type, and dependency target.

The sixth implementation slice now adds protocol-aware sampled transaction diagnostics without growing `metrics_update`:

- The existing `telemetry_diagnostic` event now samples slow and error transaction hotspots from live transaction rollups instead of pushing heavier transaction detail into the hot-path metrics payload.
- Diagnostic samples carry transaction name, dependency metadata, bounded correlation IDs, and latency or error context so Monitoring can surface concrete transaction trouble spots during a run.
- The dashboard Monitoring view now renders those optional samples inline with the existing diagnostic stream while preserving the same bounded diagnostic list behavior.

The seventh implementation slice now adds schema versioning for typed telemetry storage families:

- Typed interval summaries in SQLite now persist an explicit `schema_version` instead of relying only on table shape.
- Store initialization migrates older SQLite databases by adding the new version column and backfilling existing typed summary and run archive rows.
- Typed telemetry reads now normalize blank legacy versions so existing databases remain readable while future telemetry-family migrations have a concrete contract to extend.

The eighth implementation slice now decomposes distributed freshness into generator, transport, and controller stages:

- Distributed generator batches now preserve generator collection and publish timestamps through controller aggregation instead of overloading a single publish timestamp.
- The controller stamps batch receipt time, computes generator processing lag plus transport lag before websocket fan-out, and then records controller fan-out lag at publish time.
- Typed SQLite interval summaries now persist the additional lag-stage timestamps and metrics under a new interval-summary schema version so stored history matches the live execution telemetry card.

The ninth implementation slice now turns those measured stages into configurable SLA budgets:

- System settings include explicit telemetry thresholds for sample age, generator processing lag, generator-to-controller transport lag, controller fan-out lag, and dashboard browser-render lag.
- Existing persisted settings are normalized with defaults of 2000 ms end-to-end sample age and 500 ms per stage so upgraded controllers enforce the live monitoring budget without manual migration.
- Live `telemetry_diagnostic` events now use the configured thresholds instead of a hard-coded sample-age check, and the Settings page exposes the budgets alongside the existing performance thresholds.

The tenth implementation slice now adds run-level telemetry health transitions:

- Active runs carry a `telemetry_health` summary and every live metrics payload carries `telemetry_status` so operators can separate application failures from Stormrunner monitoring health.
- Telemetry warnings enter `degraded` first and transition to `breached` only when they remain active for the sustained breach window; data-loss conditions such as collector drops still breach immediately.
- Recovery clears the breach timer and emits a healthy transition through the existing diagnostic sampling path.

## Recommended Next Steps
1. Expand correlation and dependency metadata coverage to the remaining protocols so distributed telemetry diagnostics stay consistent outside HTTP and JMS.
2. Add alert hooks for telemetry freshness, dropped samples, websocket backpressure, and generator stream degradation using the new run-level telemetry health state.
