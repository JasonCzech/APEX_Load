# Validation commands and source cohorts

Work used the isolated codex/roadmap-durable-cursors checkout, session-local GOCACHE/GOTMPDIR, GOMAXPROCS=2, GOGC=30 and GOFLAGS=-p=1. Main was fetched and verified integrated before builds and full API validation. Dashboard asset generation finished before these Go suites started.

- `go test -overlay=<storage-overlay> -tags=postgres_integration -count=1 -timeout=5m -json ./pkg/storage -run 'TestTelemetryNotificationOutbox|TestPostgresTelemetryNotificationOutbox'`: real SQLite/PostgreSQL boundaries, UTF-8 and final serialized-envelope limits.
- `go test -race -overlay=<storage-overlay> -count=1 -json ./pkg/storage -run TestTelemetryNotificationOutbox`: all seven default tests under the race detector.
- `go test -overlay=<storage-overlay> -count=1 -timeout=20m -json ./...`: full Go suite on the exact production source later installed. Input hashes identify the reviewed overlay. API test adapter regeneration followed this run's compilation.
- `go test -overlay=<reviewed-overlay> -count=1 -timeout=20m -json ./pkg/api`: repeats the entire API package with the final regenerated test adapter, including previously missing durable-cursor capabilities. The overlay does not include the subsequent runtime draft.
- Applied-source PostgreSQL/SQLite repeat and QA acceptance inventory checks are retained separately. Applied source hashes must match the reviewed overlay hashes.
- `APEXLOAD_UPDATE_TELEMETRY_ACCEPTANCE=1 go test ./docs/qa -run TestTelemetryAcceptanceInventory`: regenerates the code-owned inventory.
- `go run ./cmd/storeoverlay -check -out pkg/api/store_overlay_test.go`: confirms generated test-adapter freshness.
- `python3 scripts/check-docs.py`: repository links and documentation invariants.

The prior recovery slice's full dashboard cycle supplies the unchanged Help/browser cohort. This slice adds no UI code or embedded Help source. SQLite/PostgreSQL are local fixtures; they do not satisfy external reference-host or multi-host recovery qualification. Atomic archive publication and actual dispatch are deliberately not part of this storage-only commit.

The initial full Go run failed three storage checks (schema release, migration convergence and declared PostgreSQL foreign-key count). After appending release 68 and strengthening the upgrade/FK assertions, the installed sources are validated again with `go test -count=1 -timeout=20m -json ./...`; `full-go-applied.jsonl.gz` is the final full-suite gate. `full-go-draft.jsonl.gz` is preserved as a failed, superseded cohort. The schema-focused SQLite/PostgreSQL repetition includes `TestSchema`, `TestSQLiteSchemaStatus`, `TestAnalysisUploadSchemaMigration` and `TestPostgresForeignKeySpecsCover` in addition to the outbox tests.

`full-api-reviewed.jsonl.gz` is also superseded: it reached the 20-minute package timeout after 6,048 passing top-level tests, without an assertion failure. It must not be represented as a complete API pass. The final applied-source full Go run contains the required replacement API gate.
