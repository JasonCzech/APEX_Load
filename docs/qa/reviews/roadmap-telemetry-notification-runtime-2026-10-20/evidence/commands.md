# Runtime validation commands and source cohorts

Use the isolated task checkout and session-local Go cache/scratch, GOMAXPROCS=2, GOGC=30 and GOFLAGS=-p=1. Fetch/check integrated main before builds/full API suites. Finish dashboard asset generation before embedding Go tests or builds.

- Draft `go test -overlay=<runtime-overlay> -race -count=1 -timeout=5m -json ./pkg/api -run '^TestTelemetryNotification'` covers scrubbing, immutable recipients, partial retry, saturation, shutdown, atomic archive publication including source-lost recovery, project/scope authorization, deletion before send, digest-key concurrency, capability fallback, observer bounds, settings recovery and execution-leader polling.
- Draft/applied `go test -tags=postgres_integration -count=1 -timeout=5m -json ./pkg/storage -run 'TestTelemetryNotification|TestPostgresTelemetryNotification|TestSchema|TestSQLiteSchemaStatus|TestAnalysisUploadSchemaMigration|TestPostgresForeignKeySpecsCover'` exercises real SQLite/PostgreSQL publication, admission and schema convergence.
- `go generate ./pkg/api` generates routing/OpenAPI/catalog, dashboard API contract and test-store adapter from their owners; generated freshness tests remain required.
- `APEXLOAD_UPDATE_TELEMETRY_ACCEPTANCE=1 go test ./docs/qa -run TestTelemetryAcceptanceInventory` regenerates the code-owned matrix.
- `APEXLOAD_UPDATE_QA_API_CONTRACT_MATRIX=1 go test -count=1 -json ./pkg/api -run '^TestGeneratedAPIContractMatrixIsFresh$'` regenerates the route authorization matrix. The dependent QA dashboard is regenerated with `scripts/qa-generate-dashboard.py`; `go test -count=1 -timeout=5m -json ./docs/qa` verifies all dependent inventories.
- After correcting the catalog-failure adapter, `go test -race -count=5 -timeout=5m -json ./pkg/api -run '^Test(HandleDeleteTestWaitsForArchiveFinalizationFence|ArchiveFinalizationLostLeasePreservesCurrentSource)$'` exercises both legacy and atomic boundaries. `go test -count=1 -timeout=10m -json ./pkg/api -run '^Test(Archive|RetryArchive|HandleDeleteTest|DistributedCompletion|TelemetryNotification)'` reruns the broader archive cohort before the final full retry.
- Applied-source full Go regression uses `go test -count=1 -timeout=20m -json ./...`.
- Dashboard typecheck, lint, relevant Help units/build and `scripts/run-e2e.sh workflow-help.spec.ts` in both auth profiles check the owning documentation and deep-link heading.
- `python3 scripts/check-docs.py` checks documentation links and invariants.
- The external private-HAR test overlay POSTs the original ZIP through the actual multipart import handler with converted-script output disabled. Only safe aggregate summary and unchanged input hash are retained; no replay occurs.

Original failed draft fixtures and superseded cohorts are retained separately. Local SQL, HTTP and browser results do not establish external qualification.

## Access snapshot correction and final integrated gate

The completed `full-go-access-snapshot-stale.jsonl` attempt retains the explicit access fingerprint fixture failure. The correction is first exercised through `access-snapshot-overlay.json` with the route/auth/freshness selector below, then installed and rerun without that overlay. `draft-route-inventories.jsonl` passed 118 top-level tests and 304 subtests (28.294s).

```sh
go test -count=1 -timeout=10m -json ./pkg/api -run 'TestAccessRequirementSnapshotStable|Test.*Route|TestRBAC|TestGenericAuth|TestGenerated.*Fresh|TestSwagger|TestEndpoint|TestTelemetryNotificationStatus'
git fetch origin main
git merge-base --is-ancestor origin/main HEAD
# Same private-HAR environment and test-only overlay documented above.
go test -overlay=/private/tmp/apex-roadmap-notification-runtime/applied-har-overlay.json -timeout=20m -json ./...
```

The final integrated invocation permits Go's standard package test cache. Focused regression/race and route checks remain uncached; package output identifies any reused results. No production source is supplied through the private-HAR overlay.
