# Analysis workflows implementation review

Date: 2026-10-01. Branch: `codex/analysis-workflows`.

## Scope

The changes add run-bound Linux/Prometheus monitoring, deeper gRPC and WebSocket
diagnostics, durable preparation, and native Parquet import/query/comparison
workflows. New analytical collection, preparation, and imports require explicit
project flags. The capability ledger retains partial status where broader
acceptance or implementation is outstanding.

## Review and corrections

Review covered authorization, source and preparation revision races, durable
publication, restart/cancellation, artifact ownership and deletion, bounded
queries, metric semantics, protocol measurement availability, and UI state.
Corrections include:

- Bind analytical artifacts to a durable storage identity, so a replica with a
  different archive configuration cannot delete or publish evidence silently.
- Retain deletion retries when metadata reads fail, and compare cleanup deadlines
  in UTC regardless of the caller's timezone.
- Preserve preparation monitor snapshots, reject stale revisions, and allow
  cancellation and audit reads after disabling new preparation.
- Keep read-only comparison endpoints available to authorized viewers with the
  correct service-account scope; enforce bounded request contracts.
- Preserve all saved comparison selectors and the executed query when saving.
  Keep chart labels tied to returned evidence after editing the query form.
- Qualify protocol throughput baselines against retained sample coverage and
  preserve unavailable measurements separately from measured zero.
- Preserve single-owner collector spools, source identity, and sample retry
  deduplication across restart.
- Correct asynchronous UI state, keyboard navigation expectations, chart label
  clipping, concurrent process-fence test synchronization, and a CLI cancellation
  test that depended on a five-millisecond scheduling window.

Regression tests accompany the fixes. The final chart checks inspect label
bounds in a real browser, rather than relying on screenshots alone.

## Verified local checks

- PostgreSQL 16.15 migration, concurrent initialization, preparation consumption,
  monitor batch deduplication, restart recovery, mapping history, and cleanup
  integration tests passed against a disposable localhost instance.
- Dashboard typecheck, lint, production build, and all 4,658 unit tests passed.
- CGO-free analytics capability tests passed; generator and collector dependency
  graphs exclude DuckDB even with the analytics build tag.
- Documentation and whitespace checks passed.

- Native analytics checks passed for the shared analysis/DuckDB packages,
  controller API, storage and CLI, including import/rejection artifacts,
  preparation, monitoring and qualified protocol baselines.
- Targeted race checks passed for analysis, collectors, gRPC/WebSocket,
  storage, generator RPC, and the API. Final API checks include cleanup,
  preparation and recovery; collector checks include spool restart/locking.
- The corrected CLI cancellation regression passed 25 consecutive executions.
- The final affected Playwright workflows passed in both profiles: 18 dev-admin
  and 10 local-auth tests. The broader dashboard cycle also ran: its local-auth
  profile passed 94 tests with one skip; the two dev-admin assertion failures
  were corrected and are included in the passing final affected run.

- The final full `go test -ldflags=-w -p 1 ./...` gate passed. Passing package
  results were reused on the final aggregate run after the CLI correction;
  the preceding full API execution passed in 475 seconds.

The final review pass found no further actionable issues in the implemented
changes. This is a review conclusion for this diff, not a claim that the broader
roadmap or unexecuted acceptance environments are complete.

Key reproducible checks:

```sh
go test -ldflags=-w -p 1 ./...
go test -ldflags=-w -p 1 -tags analytics_duckdb ./pkg/analysis/... ./pkg/api ./pkg/storage ./cmd/apexctl -run 'Test(Analysis|Native|Parquet|Container|Preparation|Workflow|RunMonitor|BaselineProtocol|Lagged|Bucket)'
GOMAXPROCS=4 go test -race -ldflags=-w -p 1 ./pkg/api -run 'Test(Analysis|RunMonitor|Preparation|BaselineProtocol|InterruptedEvacuationRecoveryRequiresEveryProcessFence)'
go test -ldflags=-w -p 1 ./cmd/apexctl -run '^TestExecuteControllerCIAbortsOnCancellation$' -count=25
npm --prefix dashboard run typecheck
npm --prefix dashboard run lint
npm --prefix dashboard test
npm --prefix dashboard run build
APEXLOAD_E2E_PROFILE=dev-admin scripts/run-e2e.sh workflow-analysis-preparation.spec.ts workflow-protocol-diagnostics.spec.ts workflow-monitoring.spec.ts tier0-scenario-builder.spec.ts
APEXLOAD_E2E_PROFILE=local-auth scripts/run-e2e.sh workflow-analysis-preparation.spec.ts workflow-protocol-diagnostics.spec.ts workflow-monitoring.spec.ts
python3 scripts/check-docs.py
```

PostgreSQL checks require `APEXLOAD_TEST_POSTGRES_URL` pointing to a disposable
PostgreSQL 16 database and `-tags postgres_integration` on `./pkg/storage`, with
`TestPostgres(SchedulerLeaseGenerationMigration|ConcurrentInitialization|InitializationLockLifecycle|AnalysisWorkflows)` selected.

## Acceptance limits

The user explicitly skipped the 10 GB compressed-input benchmark. No large-input
throughput, peak RSS, spill usage, or telemetry-lag result is claimed.

Live acceptance across every Kubernetes, Azure and ECS target, container image
execution, and the cross-platform release matrix were not executed locally.
The Docker daemon did not respond, so PostgreSQL testing used a verified,
disposable native runtime instead. CI includes the native packaging and database
checks, but its remote results are not asserted here.

Additional monitor adapters (SSH, OTLP metrics ingestion, JMX/JDBC, CloudWatch,
Azure Monitor), complete dependency intelligence, and qualified statistical
anomaly delivery remain follow-on scope, as documented in the roadmap.
