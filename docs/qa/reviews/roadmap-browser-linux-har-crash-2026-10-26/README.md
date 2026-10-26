# Roadmap BE-B1 — Linux Native HAR Crash Qualification & PID-1 Zombie Fix (2026-10-26)

This record covers the `BE-B1` (`be-b1-linux-har-crash`) slice for Linux `/proc/<pid>/stat` zombie/dead state handling, PID-1 container crash reclamation on an `ext4` volume, and the authoritative Linux CI gate in [ci.yml](../../../../.github/workflows/ci.yml).

## Scope Delivered

1. **Linux `/proc/<pid>/stat` Zombie and Dead State Handling**:
   - Extracted `parseLinuxProcStatStart` and wired `harProcessStart` to delegate to it. After the NA-C writer-ownership slice moved process identity to `pkg/internal/procidentity`, it lives on as [`procidentity.ParseLinuxStatStart`](../../../../pkg/internal/procidentity/procidentity_stat.go), used by `processStart`.
   - After verifying a well-formed `/proc/<pid>/stat` line (closing `')'` delimiter, at least 20 post-`comm` fields, and unsigned 64-bit `starttime` at field 22), processes in state `'Z'` (zombie), `'X'` (dead), or `'x'` (dead) return `("", false, nil)`.
   - When `generator` runs as PID 1 of a Linux container without `--init` ([Dockerfile.browser-generator](../../../../Dockerfile.browser-generator)), a killed child's orphaned Playwright Node driver is reparented to PID 1 and transitions to state `'Z'` once its stdin-EOF graceful shutdown completes; treating `'Z'` as exited prevents reclamation from deadlocking while preserving fail-closed behavior on malformed `/proc/<pid>/stat` records.
2. **Authoritative Linux CI Gate**:
   - Added the `Native HAR ownership (Linux)` step (`if: matrix.lane == 'race'`, `APEXLOAD_BROWSER_TESTS: require`) in [ci.yml](../../../../.github/workflows/ci.yml) running `go test -race -count=1 -v -timeout=30m ./pkg/protocol/browser -run 'HAR'` and `go test -count=1 -v -timeout=30m ./pkg/api -run 'HAR|BrowserReplay'`.
3. **Documentation Alignment**:
   - Updated [BROWSER_HAR_WORKING_OWNERSHIP.md](../../../guides/BROWSER_HAR_WORKING_OWNERSHIP.md) at lines 71–73, 160–167, 381–387, and 595–601 to document Linux `'Z'`/`'X'`/`'x'` process-state reclamation, the `1,024` outstanding-request replay bound, and the Linux crash qualification record.

## Verification & Repeat Runs

- **Portable Unit & Regression Tests**:
  - `go test -race -count=2 -run "TestParseLinuxProcStatStartTreatsZombieAndDeadStatesAsExited|TestHAROwnershipReclaimsAbandonedGenerationsAndPreservesUnprovenOnes" ./pkg/protocol/browser` -> `ok github.com/jasonczech/apexload/pkg/protocol/browser 1.593s`
- **Three Consecutive Linux PID-1 Crash Qualification Runs (`ext4` Docker volume)**:
  - Executed `/test/browser-linux.test -test.count=3 -test.v -test.run=^TestHAROwnershipReclaimsKilledNativeRecording/chromium$` as PID 1 (`--entrypoint /test/browser-linux.test`) inside `apexload-controller:local` with `-v apexload-har-ext4:/tmp` (`ext4`-backed Docker volume, not a macOS FUSE bind mount):
    - Run 1: `engine=chromium pid=1 driver=34 exited after 104ms` (`PASS`, `1.10s`)
    - Run 2: `engine=chromium pid=1 driver=187 exited after 103ms` (`PASS`, `0.83s`)
    - Run 3: `engine=chromium pid=1 driver=332 exited after 53ms` (`PASS`, `0.86s`)
  - Across all three runs, the orphaned Playwright Node driver exited within `53–104 ms` of `SIGKILL` (well inside the 30-second `gracefullyProcessExitDoNotHang` / finalization budget), transitioned to a reparented child of PID 1, and its abandoned working generation was planned and pruned cleanly.
- **Workflow & Documentation Checks**:
  - `python3 scripts/ci/validate-github-actions.py` -> `validated 12 workflows and local composite actions`
  - `python3 scripts/check-docs.py` -> passed (`0` errors)
