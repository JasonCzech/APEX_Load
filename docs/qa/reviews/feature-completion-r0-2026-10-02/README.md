# Feature completion R0 — prerequisite verification

Evaluated 2026-10-02 against immutable snapshot `025d5ed9dd1078396cd89907ce90ce840dfad5df` (the published implementation plus the approved completion specification). This record closes the outstanding final malformed-drop-counter API regression, refreshed targeted API race and actual PostgreSQL release-26 verification gaps. It is not acceptance of the new R1–R10 implementation work.

## Results

- Final checked-addition/negative/overflow API regression tables passed with `-count=1`. Coverage includes signed boundaries, invalid contributors, lower-bound retention, unavailable zero, fleet/archive reconciliation and source nonmutation.
- The refreshed targeted API race gate passed with `-race -count=1`, including those guards, baseline cumulative-counter/availability cases, missing/cold peers, runtime-history restoration, stream sequencing and publication fencing. Package test runtime: 2.946 seconds. Retained race output (`api-race.txt`).
- Actual PostgreSQL migration and round-trip acceptance passed against disposable PostgreSQL 16.14 (`aarch64-unknown-linux-musl`). `TestPostgresHistoryDropAvailabilityMigrationAndRoundTrip` dropped the new availability column and current migration receipt, reopened the real store, then verified measured zero versus SQL NULL availability. It executed without skips (1.33 seconds).
- SQLite migration/restart acceptance also passed (0.05 seconds). Combined storage test runtime: 1.788 seconds. Retained storage output (`storage-migration.txt`).
- No prerequisite assertion failure, data race or actionable R0 source defect was found. No source code was changed by this verification task.

## Reproduction

Toolchain: Go 1.26.5 on macOS arm64. Go builds ran serially with `GOMAXPROCS=2`, `GOMEMLIMIT=2GiB`, and `-p 1`; the memory limit is a soft runtime setting, not a claim about total compiler RSS. The native race compiler briefly reached approximately 4.4 GiB RSS. Existing shared caches were reused and were not deleted.

The final race command ran from a `git archive` snapshot under `/private/tmp`, isolating these prerequisite checks from concurrent feature development:

```sh
GOMAXPROCS=2 GOMEMLIMIT=2GiB go test -race -p 1 -count=1 -timeout 15m ./pkg/api \
  -run '^(TestRetainedDropCounterCheckedAddition|TestDistributed(InvalidDropCountersCannotCancelObservedLoss|NegativeDropCounterPresenceIsUnavailable|ReceiptAndGapDoNotInventDropCounters|DropCounter.*|Metrics.*)|TestBaselineTelemetry.*|TestTelemetryCumulativeCountersDoNotDoubleCount|TestArchiveTelemetryMissingClassificationUnavailable|TestRemoteArchive.*DropCounter.*|TestArchiveSnapshotPreservesIndependentCounterEvidence|TestRuntimeHistoryRestoresDropCounterAvailability)$'
```

The actual storage gate used `APEXLOAD_TEST_POSTGRES_URL` pointing only to the disposable loopback fixture, then ran:

```sh
GOMAXPROCS=2 GOMEMLIMIT=2GiB go test -p 1 -count=1 -v -timeout 5m \
  -tags postgres_integration ./pkg/storage \
  -run '^(TestPostgresHistoryDropAvailabilityMigrationAndRoundTrip|TestHistoryDropAvailabilitySurvivesRestartAndMigration)$'
```

The PostgreSQL container was limited to one CPU and 512 MiB, bound only to `127.0.0.1:55439`, and used temporary fixture credentials. Its test-owned schema was removed by the test. After success, the disposable container and source snapshot were removed; these evidence documents remain.

An initial race attempt in the shared mutable worktree ended before tests executed because concurrent new contract files invalidated vet/package symbols. It was not counted as a baseline test result. The successful immutable-snapshot retry above is the authoritative R0 outcome.

## Remaining acceptance boundaries

This targeted prerequisite gate does not replace the final whole-program Go/race/dashboard/native-platform gates. Real Azure and broker acceptance, the intentionally waived 10 GB benchmark, and pending specific approval for shared-cache deletion retain their existing status.
