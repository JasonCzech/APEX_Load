# Elastic implementation and qualification specification

## Boundaries and ownership

`pkg/elastic` owns product adapters, fixed read-only endpoints, query validation, field/index discovery, capability checks, normalization, bounded paging and presets. It depends on shared `pkg/analysis` contracts. It does not depend on API/storage or the load engine. `pkg/analysis` owns typed scalar evidence, metric semantics, coverage, continuation and terminal criteria. API/storage own project authorization, secret resolution, immutable snapshots, executor fencing, scheduling, artifact/receipt transactions and canonical heads. Collectors use the same provider normalization with local operator restrictions and existing sealed grants/spool replay.

The existing project ELK connection identity is extended additively. SQLite/PostgreSQL persist configuration revisions, verified revisions/capabilities, private credential references, source snapshots, private PIT/async continuation and transactional count/byte quotas. Legacy flat ELK connections and captures retain their IDs and readers. Common AppDynamics types have compatibility aliases so existing JSON wire fields and archive readers remain available.

Saved-revision inspections use typed verify/discover-indices/discover-fields/preview payloads, optimistic revision checks, bounded leases and 15-minute expiry. Results are scrubbed normalized projections; collector result grants are signed and assigned. Operation ownership and health updates compare the saved revision, so a late response cannot verify an edited connection.

Source preparation and Start freeze connections/sources atomically through the durable monitoring framework. Leader-only direct scheduling has bounded workers and per-job exclusion. Collector assignments retain execution-owner fences. Immutable artifact writes precede accepted receipts, canonical head replacement, continuation and quota updates. Duplicate sequence/digest retries are idempotent; conflicting retries fail. Public responses omit secrets, private handles and continuation state.

## Canonical semantics

Metric identity includes source/query, unit, statistic, dimensions and interval. Revising a bucket replaces its canonical head. Numeric values retain count/sum weights, approximation and settlement. Window kind distinguishes native buckets, exact query-window aggregates and instantaneous health API readings. Query-window aggregates preserve precise UTC interval boundaries.

Record identity includes job/source, query/signal, concrete index and document ID. Typed logs/errors, spans and transactions retain source event time, optional ingestion time and durable receipt time. Spans retain trace/span/parent IDs and duration; trace grouping never collapses span identity. Up to16 scalar projected attributes are retained, subject to8KiB total record size. Sensitive names and text are scrubbed before every persistence/disclosure boundary.

A source can lower retention limits. Per-query truncation remains durable and cannot later become complete. Canonical source/run and immutable artifact quotas are transactional. Complete independent metrics can be evaluated even if another signal is truncated. Load traffic never waits for monitoring or terminal rules.

## Qualification gates

The target capability bands are Elasticsearch 8.14+/9.x and OpenSearch 2.19/3.x. The [exact-version qualification report](ELASTIC_QUALIFICATION.md) records passing local vendor trials for Elasticsearch 8.14.3/9.1.0 and OpenSearch 2.19.0/3.3.0 with verified TLS, scoped Basic readers, filtered aliases, numeric metrics, exhaustive provider pagination and Elasticsearch ES|QL. Hosted/Serverless, Azure Native, vendor mTLS and actual collector-to-vendor deployments remain unqualified. Record vendor patch version, deployment flavor, authentication mode, TLS/CA/mTLS, direct/collector location and test artifacts for each additional real-service qualification.

| Gate | Required evidence |
|---|---|
| Security | Auth modes, private DNS approval, CA/mTLS, redirects, destination changes, secret replacement, permissions and no disclosure |
| DSL/ESQL | Enforced time/service/run filters, forbidden scope escape, named mappings, exact windows, mixed types, null/missing/zero and oversized malformed results |
| Exhaustive records | Over10,000 documents, equal timestamps, cross-index duplicate IDs, rollover, expired PIT restart, stable old/new OpenSearch tie paths and cleanup |
| Metrics | Weighted average, duration units, counters/resets, histogram/summary schema, revised buckets, approximate percentile/cardinality and exact terminal intervals |
| Traces | ECS/APM/OTel fields, parent gaps, upstream sampling, span identity, duration conversion and correlation disclosure |
| Runtime | Pause/resume, cancel/shutdown, throttling, outage, late indexing, deadline, fair bounded requests and no engine blocking |
| Durability | HA/fencing, duplicate/conflicting receipts, collector restart/replay, quota accounting and artifact integrity |
| Product | Connection→inspection→source→binding→live→archive in dev-admin/local-auth; collector path; unsupported ES|QL; partial/truncated views |
| Contracts | CLI/MCP parity, revision conflict, operation polling and generated OpenAPI/TypeScript freshness |

Run affected Go suites and full `go test ./...`, PostgreSQL integration tests where a service is available, MCP tests, dashboard typecheck/lint/unit and `npm run test:cycle`. Controlled high-volume fixtures establish code invariants; only real-service trials establish vendor compatibility. Performance targets in AGENTS.md remain unbenchmarked targets.

## Release scope

Read-only indexed telemetry plus independently optional cluster/node/index, Kibana and Logstash health is supported by the connector contract. AWS SigV4, CloudWatch, SQL/EQL/OpenSearch PPL, Kibana saved-object import, stack provisioning and writes are deferred. Prometheus-hosted OpenSearch observability metrics use the existing Prometheus source.

Feature disablement prevents new assignments; bounded active lifecycle cleanup and retained archive reading remain available. Operate first with optional sources in controlled projects, qualify exact environments, then explicitly enable customer projects. See [operating guide](ELASTIC.md) for setup, budgets and coverage interpretation.
