# Roadmap implementation: source collection windows

Date: 2026-10-19. Scope: the next bounded prerequisite of [TEL-A](../../../ROADMAP_IMPLEMENTATION_SPEC.md#tel-a--durable-windows-and-measured-end-to-end-age), following [replay-safe receipt coverage](../roadmap-telemetry-coverage-2026-10-19/README.md).

## Contract and scope

`WindowMs` remains the existing rate denominator: final drains can clamp it to the publication cadence, and multi-group batches can use an effective duration to preserve additive rates. It cannot establish actual source observation boundaries.

The generator now captures each result buffer's start and end boundary under the drain lock, using elapsed monotonic time mapped to the generator's published clock. Both endpoints use one clock anchor. These describe buffer observation, including idle intervals; they are not action start/end times or a qualified mapping onto controller time. Unknown initial boundaries stay unavailable.

Additive protobuf fields `MetricsBatch.collection_windows` and `collection_windows_complete` carry group names and nanosecond timestamps. Each current collection includes only the groups collected in it; lifecycle replays retain the same intervals and collection sequence. Direct publications without measured boundaries cannot inherit earlier intervals. Source epoch, execution generation and collection sequence scope the retained archive evidence.

Wire and archive projections retain at most 256 intervals per collection; group names retain the existing 512-byte limit. Sender selection is deterministic by group name. Empty, missing, invalid, duplicate or bounded-out evidence makes the complete flag false. The flag describes completeness of the collection's drain cohort, not delivery, exact workload coverage or clock qualification. Existing result, aggregate and rate evidence is preserved when provenance is incomplete.

The generator archive's JSONL telemetry and Parquet `telemetry_json` retain the same source identity and boundaries, including the process epoch before the publication broker stamps its envelope. Old messages and archives remain readable with provenance unavailable. No REST, DSL, database migration, policy-default or Parquet-column change is introduced. Receipt-time coverage remains `telemetry_coverage.v3`.

Durable controller collection cursors, source-window recovery across controllers, policy/cadence segmentation, qualified capture-to-render correlation and reference-workload qualification remain open. These raw intervals do not close TEL-A. Messaging and CloudTest outcome parity remain outside this slice.

## Validation

Base: `57c7ff947` on top of `4f75f1ac4e5ed198995258658c3b476597a34828` (`origin/main`, fetched and integrated before building and before the full API suite). Local macOS arm64, Go 1.26.9 and Node 22.23.3. Go uses a session-specific cache, `GOMAXPROCS=3` and `-p 1` to bound compilation resources. The full Go summary extracts package results and the failed test output from JSON events, with the raw-event SHA-256 retained. Other retained logs normalize trailing whitespace only.

- [Focused source-window tests](evidence/targeted.log): all six new tests passed, including the physical Parquet round trip and malformed-wire cases.
- [Full Go suite summary](evidence/full-go-summary.log): `go test -json -p 1 -parallel 4 -count=1 -timeout=20m ./...` completed with 167 packages passing and one failing package. Its sole failed test was the existing `TestScriptContentRevisionConcurrentInitialization`, which encountered `SQLITE_BUSY` while two stores initialized the same database. The full run therefore exited 1; it is not recorded as an unqualified pass.
- [Storage repetitions](evidence/storage-repeat.log): the failed test subsequently passed 20 consecutive runs. The [complete storage-package retry](evidence/storage-retry.log) also passed. [Scope check](evidence/storage-scope.txt): storage has no changes in this slice and depends on neither changed production package. The intermittent initialization issue is retained as an existing validation limitation; no unrelated storage fix is included.
- [Help tests](evidence/help-tests.log): all 34 content and contextual-help tests passed after the guide correction. The renderer's fragment/section-ownership scan found no errors.
- [Documentation check](evidence/docs.log): passed for 251 active Markdown files.
- [Acceptance inventory](evidence/inventory.log): regenerated and passed; all six new tests are included in the maintained telemetry race selector. `bash -n scripts/run-optin-suites.sh` passed.
- [Protobuf reproducibility](evidence/protobuf-freshness.txt): repeated generation produced identical output using protoc 5.29.3 and protoc-gen-go 1.36.11.
- [Final dashboard build](evidence/dashboard-build-final.log): both TypeScript checks and Vite bundling passed after the help-link correction. The existing large-chunk advisory remains. Asset generation finished before the final race run.

[Final telemetry race matrix](evidence/race-matrix.log): the exact selector maintained in `scripts/run-optin-suites.sh` passed for `pkg/api`, `pkg/distributed/generatorrpc` and `pkg/analysis`, including all six new tests. No races were reported. The retained log includes the command; this serial run used `GOGC=20`, `GOMAXPROCS=2`, `-p 1` and `-parallel 4` to keep memory bounded.

Earlier [full-suite](evidence/full-go-interrupted.log) and [race](evidence/race-matrix-interrupted.log) attempts were deliberately interrupted during compilation because concurrent compilation caused memory pressure. They are incomplete attempts, not passes. The large API package was then successfully precompiled with `GOGC=20` and `GOMAXPROCS=2` before restarting the full suite with its normal test settings. No source or assertion was weakened.

## Review

The implementation review checked drain lock ownership, per-collection group resets, lifecycle/terminal replay, idle and unknown boundaries, preservation of rate denominators, bounded allocation, malformed and legacy wire input, archive identity before broker stamping, and physical Parquet round trips.

Findings fixed during implementation and review:

- The original `_windows.go` filename was interpreted by Go as Windows-only source. Renamed it and its test file to the platform-neutral `collection_window.go` / `collection_window_test.go`, and regenerated the acceptance inventory.
- Both interval endpoints must use one published-clock anchor. The end is derived from the mapped start plus monotonic elapsed time, avoiding mixed anchors if an idle heartbeat re-anchors concurrently.
- Archive verification initially checked only the Parquet projection. It now reopens the written Parquet file and verifies collection identity and nanosecond boundaries from its telemetry column.
- The weighted-rate assertion now requires the expected 1,333 ms denominator instead of merely excluding the two raw group durations. Additional malformed-wire cases cover missing endpoints, empty entries and oversized group names.
- The help renderer deliberately disables links to repository documents outside its catalog. Removed the new repository-only evidence callout from the help guide; the QA index remains the navigable repository entry point. Final dashboard generation and help validation follow this edit.

The follow-up review checked the complete scoped diff, regenerated protobuf, test inventory, evidence claims and repository/help links. No findings remain in this slice. The unrelated intermittent storage initialization failure remains disclosed above; successful retries do not claim to repair it. No unrelated working-tree changes or generated dashboard bundles belong to this commit.

## Cleanup

Cleared the session-specific Go build/test cache (about 4.9 GiB; 8 KiB of metadata remains), about 1.9 GiB of abandoned compiler work directories and the test executable, and the isolated checkout’s disposable Vite caches. Docker’s unused build-cache prune completed with 0 B reclaimed. Another task’s active Go run and shared Go cache were left untouched. Dependencies, source, images, containers and application data were preserved. The final dashboard build remains in `/private/tmp/apex-source-windows/dashboard-dist`; generated bundles are excluded from the commit. The [tracked browser-test processes](evidence/browser-cleanup.txt) all exited, and no manual browser session was opened.
