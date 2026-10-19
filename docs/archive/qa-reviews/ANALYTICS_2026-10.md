# Completed analytics implementation reviews — October 2026

Status: Historical. Consolidated on 2026-10-19. These are completed, scoped implementation reviews, not closure of their parent requirements. Original dates, failed checks, pending acceptance and qualification limits remain below. Evidence files remain at their original paths. Use the [review index](../../qa/reviews/README.md) for outstanding work and other records.

## Records
- [Shared analytical contract review — 2026-10-03](#feature-completion-catalog-2026-10-03)
- [Native partition publication, pruning and query authority — 2026-10-05](#feature-completion-native-partitions-2026-10-05)
- [Filtered native analytics — 2026-10-01](#filtered-native-analytics-2026-10-01)
- [Live/grouped analytics and overlays — 2026-10-01](#live-grouped-overlays-2026-10-01)

<!-- Original record: docs/qa/reviews/feature-completion-catalog-2026-10-03/README.md; evidence retained in docs/qa/reviews/feature-completion-catalog-2026-10-03/ -->
<a id="feature-completion-catalog-2026-10-03"></a>

## Shared analytical contract review — 2026-10-03

Scope: the metric/query catalog foundation for R1/NA-1, scalar/DuckDB derivation identity, existing native storage validation, typed analysis capabilities and generated API contracts. This does not close the full native analytics program.

Independent review found incompatible derived statistics could share identity and native dimension/filter options exceeded executable combinations. Fixes retain bounded full transformation lineage and expose mode/population-specific query profiles tested against the actual validators. Repeat review found summary/histogram operations advertised without executors; those entries were removed until implemented. Required public list properties now serialize arrays, including empty filter lists. A final consistency regression rejects unsupported operations for empty bucket inputs. Adjacent review also found canonical/wire unit ambiguity and epoch-less raw rates could acquire qualified lineage. Fixes preserve legacy wire units with explicit catalog mapping, retain reset-only raw rate display and exclude unknown epochs from the new qualified comparisons/statistics. Final independent repeat review reported no additional actionable findings after these fixes.

Validation:

- Full shared analysis and storage suites passed; the storage profile matrix exercises each metric/aggregation, every filter subset and allowed property selection.
- Shared analysis race suite passed after the final consistency fix.
- Native DuckDB series/aggregation/counter/summary regressions passed, including retained statistic lineage and independent output copies.
- Focused API capabilities/native/messaging regressions passed. Generated OpenAPI/TypeScript contracts were refreshed.
- Pinned Node 22.22.0 dashboard typecheck, lint, relevant native panel unit tests (19) and production build passed.
- The real dashboard native panel save/reopen workflow and controller catalog assertion passed under dev-admin and persisted local-auth profiles, using disposable controllers/stores.
- Final frozen `go test -p 1 -timeout 20m ./...` passed with a dedicated temporary `GOCACHE`, `GOMAXPROCS=2` and `GOMEMLIMIT=2GiB` ([complete log](../../qa/reviews/feature-completion-catalog-2026-10-03/go-tests.txt)). Documentation checks passed for 99 active Markdown files. The API suite took 1009.088 seconds in that cold-cache run; it completed without failures.

Gate history: the first mutable run encountered stale QA inventory and concurrent source changes; inventory was regenerated and the frozen pre-decoder run passed. A later final run passed API tests but failed remaining builds because files disappeared from the shared Go cache. The successful dedicated-cache repeat supersedes that environmental failure.

Limits: the public catalog is independent of capture/project capability. Provider qualification, typed summary/histogram ingestion, broader measured joints, native worker isolation and release/scale evidence remain separate open gates. The waived 10 GB import benchmark was not run.

<!-- Original record: docs/qa/reviews/feature-completion-native-partitions-2026-10-05/README.md; evidence retained in docs/qa/reviews/feature-completion-native-partitions-2026-10-05/ -->
<a id="feature-completion-native-partitions-2026-10-05"></a>

## Native partition publication, pruning and query authority — 2026-10-05

This slice implements bounded producer partition manifests, selective artifact staging, and native query authority/window guards. The final independent review found no additional actionable source issue in the recorded 51-file freeze after the documented fixes. Focused current-source storage normal/race and real native API acceptance passed. The final current-bundle full Go and native shared/storage gates passed. A required dashboard cycle found an existing Source Find detachment bug; after its reviewed narrow correction, the required full dashboard cycle passed on unchanged source in both auth profiles. **This is bounded correctness and current-source merge-gate qualification, not full NA-2/NA-6 completion or scale/platform qualification.**

The reviewed source inventory is [the final independent receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/apexload-native-final-review-20261005T084732Z.json). Its SHA-256 is `99cb3c13accee7d6da491a8c10263e1e643620620c0a7b7313bd663777eb0133`. Earlier receipts and every failed/superseded gate remain in `evidence/`; none were replaced by successful output.

<a id="feature-completion-native-partitions-2026-10-05--reviewed-behavior"></a>

### Reviewed behavior

- Native normalization produces bounded partition metadata for every normalized output, including accepted/missing populations and known time/identity/metric bounds. The parent separately verifies the copied artifact byte count and SHA-256 before committing an immutable manifest. A new attempt requires manifest version 1; legacy version-0 datasets keep the full-scan fallback. A present malformed, foreign or mismatched proof fails closed.
- Query planning binds the whole effective query, dataset revision, mapping, manifest and catalog version. Frozen plans retain private mapping/geometry; staging and final response checks use that exact authority. Separate comparison sides charge both copies even when they request the same dataset and plan. Responses carry at most two selection reports.
- `total_artifact_bytes`, `staged_artifact_bytes` and `avoided_artifact_bytes` describe immutable objects selected/copied/omitted. They are **not physical DuckDB scan counters**. The retained production-SQL `EXPLAIN` test is separate evidence of projection and filter pushdown; API metadata does not expose it as measured scan savings.
- Approved comparisons and dataset overlays disclose their effective UTC microsecond window and retain the original requested window. Both comparison sides use the same effective bounds. Raw timestamps, actual execution start and whole merged distributions remain intact. Invalid boundary-spanning rates stay missing; transaction percentiles are computed only from eligible observations, never summary percentiles.
- Standalone series deadline covers initial metadata, native work and final authority. Empty selection still requires the native capability and admission path; a CGO-free build cannot silently return successful empty native results. Private-scope, association, readiness, revision and deadline checks are repeated before returning a frozen result, including after late annotation reads.
- Strict receipts retain the original publication result through later association/deletion revisions. Version-1 result digests canonicalize only zero-length output collections that durable `omitempty` encoding omits. Version-0 historical null/array/object digest shapes are unchanged. No compatibility acceptance was added for invalid version-1 receipts, and generic saves cannot erase, downgrade or replace the published manifest.
- Upload completion uses the same bounded metadata preflight as durable updates. A physically closed writer whose metadata cannot fit still receives a durable abandoned `metadata_limit` receipt and exact cleanup keys; it does not lose its seal or advance the durable cursor.

<a id="feature-completion-native-partitions-2026-10-05--current-source-focused-acceptance"></a>

### Current-source focused acceptance

The following immutable receipts match the final reviewed storage source hashes before and after execution. Each raw log hash and complete command/environment are recorded in its adjacent JSON receipt.

| Gate | Result | Duration | Evidence |
| --- | --- | ---: | --- |
| SQLite partition, publication, lifecycle and historical receipt regressions, three repeats | Pass | 3.879 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-partition-receipt-default-final.json), [log](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-partition-receipt-default-final.log) |
| Actual PostgreSQL receipt lifecycle/reopen, manifest reconciliation/refusals and upload physical authority, three repeats | Pass | 27.099 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-partition-receipt-postgres-normal-final.json), [log](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-partition-receipt-postgres-normal-final.log) |
| Combined SQLite and actual PostgreSQL targeted race checks, three repeats | Pass | 100.074 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-partition-receipt-sqlite-postgres-race-final.json), [log](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-partition-receipt-sqlite-postgres-race-final.log) |
| Real native API multi-file partition pruning/parity, time-window/rate/selection/admission and native import lifecycle through durable deletion | Pass | 135.720 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/api-native-physical-repeat3.json), [log](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/api-native-physical-repeat3.log) |

The PostgreSQL tests used the existing isolated PostgreSQL 16 fixture with unique owned schemas, separately opened stores, close/reopen persistence, and exact-schema cleanup. The new receipt fixture verified schema absence after each run. The container, images, application data and unrelated schemas were preserved. These metadata fixtures do not themselves prove full controller restart/HA or OS writer closure.

The focused PostgreSQL regex contains an optional native-attempt alternative with an extra `Analysis` prefix that matches no existing test. Consequently these normal/race receipts qualify the three exact PostgreSQL tests named above, not that additional native-attempt fixture. Earlier independently retained native-attempt evidence remains separately scoped.

Additional retained evidence covers producer statistics, signed/sanitized manifests, actual positive/pre-epoch nanosecond cast boundaries, protected identities, legacy all-object staging, excluded real objects, exact production-SQL `EXPLAIN`, generated contracts, and dashboard build/type checks. Those receipts describe their own source snapshots and scope. They are not replacements for the current-source broad gates below.

<a id="feature-completion-native-partitions-2026-10-05--chart-accessibility-correction-and-current-browser-evidence"></a>

### Chart accessibility correction and current browser evidence

The first local-auth native Analysis workflow exposed a real clipped comparison card. Its screen-reader-only chart description rendered as a visible, long serialized JSON paragraph because the only `.sr-only` rule was in a report stylesheet that the Analysis route does not load. Retained actual geometry showed a 662 px card with 1115 px scroll width and identity controls clipped under the sidebar. This was not a chart-toolbar width issue. The original failed trace, diagnostic source/attempts and exact overflow geometry remain in evidence.

The correction is scoped to direct `.exportable-chart > .sr-only` children. It supplies the component's own conventional 1 px clipped, absolute screen-reader description styling on every route, without hiding accessibility text or masking outer-card overflow. The unit test injects only the real component stylesheet and proves that the description and figure accessible label remain intact while an unrelated `.sr-only` element is unaffected. Browser assertions retain the original whole-fieldset bounds and center-hit checks and additionally verify no outer-card horizontal overflow and the actual clipped description geometry at the existing 760 px capture height.

| Gate | Result | Duration | Evidence |
| --- | --- | ---: | --- |
| Focused ExportableChart/SeriesChartExport/AnalysisComparison units, 61 tests | Pass | 1.861 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-chart-description-unit-repeat4.json) |
| Full dashboard typecheck | Pass | 32.408 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-chart-description-types-repeat2.json) |
| Full dashboard lint | Pass | 14.569 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-chart-description-lint-repeat1.json) |
| Typed dashboard build with the scoped correction | Pass | 34.437 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-chart-description-build-final.json) |
| Actual native-controller focused Analysis dev-admin browser, 3 tests | Pass | 35.478 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/browser-native-analysis-dev-admin-repeat1.json) |
| Actual native-controller focused Analysis local-auth browser, 3 tests | Pass | 38.640 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/browser-native-analysis-local-auth-repeat1.json) |

The [focused final UI receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-chart-focused-final-review.json), SHA-256 `81200e54d17624b160a529d3d0fc0fb5c5eaccee579d9e444eaa7a1026c53884`, retains the six-source and 247-file focused embed inventory plus 16 browser artifacts. Both profiles passed the geometry assertions, and retained screenshots were visually checked for readable labels/controls. Passed disposable controller workdirs and temporary diagnostic staging were verified removed. The browser workflows use bounded transport fixtures and do not constitute new native Parquet I/O measurements; the separately passing real API lifecycle/pruning fixture supplies that evidence.

Earlier style harness failures remain retained: a CSS raw import did not install the stylesheet; Vite rewrote a literal CSS asset URL; static Node imports disagreed with the intentionally browser-only app tsconfig. The final harness follows the repository's existing documented dynamic `node:fs` convention. One later command typo referenced nonexistent `test:unit` and executed no tests; it is separately retained and is not claimed as feature evidence. No production guard or browser geometry assertion was relaxed to fix those harness attempts. The first full dashboard cycle completed after 2265.696 s: types/lint/508 unit files/5545 tests/build passed, dev-admin had 232 passes/5 skips/one Source Find detachment failure, and local-auth passed 164/165 with one skip. Its complete failed receipt/log and 441 artifacts (84,226,034 bytes) remain retained; it is not a passing merge gate. Exact owned disposable runner directories were verified removed. The failure record is [the immutable complete-cycle artifact receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-dashboard-failed-cycle-retained-artifacts.json), SHA-256 `78abdefc1cd529eb6ec91e7f6dc9d712109866fb544227837d53aa7631cd0dfb`.

<a id="feature-completion-native-partitions-2026-10-05--source-find-correction-and-latest-bundle-qualification"></a>

### Source Find correction and latest bundle qualification

The complete cycle exposed the existing source editor losing its Find textbox when asynchronous secret-status metadata recreated CodeMirror configuration. The correction backports only stable module-level basic setup, a stable current-owner edit callback, and an assist-extension memo keyed to the exact validation function. It includes a meaningful adjacent component identity/current-owner regression and a controlled real metadata-response browser regression that preserves typed search text and keyboard navigation. No authoring revision/CAS/client changes were copied into this native slice. Root independently reviewed the exact four-file change clean before the build: [review receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-source-find-root-review.json), SHA-256 `f88ae018089e87696f6bf08a38ed89a77b23f59aeec8938ea17d4261f6bdbd9f`.

| Gate | Result | Duration | Evidence |
| --- | --- | ---: | --- |
| Affected editor/chart/comparison units, 249 tests in five files | Pass | 14.983 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-source-find-unit-repeat1.json) |
| Full dashboard typecheck | Pass | 30.910 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-source-find-types-repeat1.json) |
| Full dashboard lint | Pass | 13.791 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-source-find-lint-repeat1.json) |
| Typed dashboard build | Pass | 32.224 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-source-find-build-repeat1.json) |
| Actual native-controller Source Find and Analysis dev-admin, four tests | Pass | 36.180 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/browser-native-source-find-dev-admin-repeat1.json) |
| Actual native-controller Source Find and Analysis local-auth, four tests | Pass | 46.958 s | [Receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/browser-native-source-find-local-auth-repeat1.json) |

Both canonical-goals PNGs were visually reviewed, and the existing exact whole-control/card geometry assertions remained green. Twenty focused artifacts (18,377,769 bytes), ten reviewed source hashes, exact runner cleanup and 247 focused embed files are retained in [the focused freeze](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-source-find-focused-final-freeze.json), SHA-256 `a8ca82f962994c9e98e7d291fa281ef0bcabf5dbb1f873baf65b503a2772556a`.

The final required full cycle passed on unchanged reviewed source in 2115.341 s (10:04:44–10:40:00 UTC). Its typecheck, lint, 509 unit files/5546 tests and typed build passed; dev-admin passed 233/238 with five skips in 6.6 min, including the former Find failure, and local-auth passed 164/165 with one skip in 22.8 min. [The immutable cycle receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-dashboard-full-cycle-source-find-repeat1.json) retains raw SHA-256 `f3420611007c9306e4955fd8a68ccb35658ddd023ed2a1145825d99db8295fdb`. Matching final full Go passed in 1158.217 s (10:11:11–10:30:29 UTC): [receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-composed-full-go-source-find-final.json), raw SHA-256 `f1d4ddd7b574b01d5b6c100673c5124e0e1412629a4b71b31c5a488d8bc18bf5`. The exact final-cycle embed is [the final build freeze](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-source-find-full-cycle-embed-freeze.json), SHA-256 `968d4e595b698881c275b9013d83372cb826a94e35cd1c549683832813f3d89a`, containing 247 assets/8,423,322 bytes. The ten reviewed frontend sources and all 247 assets were verified unchanged after both profiles. [The final artifact and cleanup receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-dashboard-final-cycle-retained-artifacts.json), SHA-256 `91d803ee23d08ae04d7702c5e766c8363c80e248481ee3a916c187cd66ea213b`, preserves 439 browser artifacts/78,357,448 bytes and verifies removal of the two exact owned disposable controller directories. Both final canonical goals and identity comparison left PNGs were visually reviewed and remain readable. Earlier failed cycles/artifacts are preserved.

<a id="feature-completion-native-partitions-2026-10-05--reviewfix-evidence-and-superseded-gates"></a>

### Review/fix evidence and superseded gates

1. The actual native import DELETE initially returned `409 analytical attempt authority lost` after successful normalization, query and association: [API failed repeat 2](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/api-native-physical-repeat2.json). A focused before-fix storage test reproduced the self-invalidating digest/receipt across the omitted-empty durable boundary: [receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-receipt-empty-output-before-fix.json), [raw log](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-receipt-empty-output-before-fix.log), SHA-256 `5c948048a081b01b809bbe44c2302e72745b17fac25e7a6fb6590be7d8744fb9`. The version-1-only canonicalization fixed it without weakening strict receipt validation. New SQLite/PostgreSQL tests associate a real same-project run, preserve proof through deletion retries, reject stale/manifest-changing writes, retain the historical receipt, and close/reopen. They also cover cancellation, blocked cleanup before a seal, failure/remap and fresh-owner retry.
2. A coherent unknown-row worker fixture now includes the producer statistics required by the new contract while still proving per-measure population refusal. The old fixture's failure and the corrected meaningful test are retained.
3. The upload metadata-limit regression first exposed a fixture sizing mistake, then a corrected raw-pass/bounded-fail production seam. Both failed receipts remain. The fix preserves exact physical-close proof, cleanup and cursor behavior at the true bounded preflight limit, with the same exercise run on SQLite and actual PostgreSQL.
4. The whole-publication rate test originally asserted a count for an invalid rate even though rate values correctly remained nil. Its corrected independent average assertion proves the count and unchanged source timestamp/window; rate safety was not relaxed.
5. [The earlier composed full Go gate](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-composed-full-go-final.json) failed after 1171.286 s, with worker population and rate-fixture failures and source changes during execution. It is retained as failed/superseded, **not a passing merge gate**. The earlier native shared/storage passing repeat ran before the version-1 receipt fix and new receipt tests; [its receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-shared-storage-repeat1.json) is earlier-scope evidence, not final current-source qualification.

<a id="feature-completion-native-partitions-2026-10-05--pending-acceptance--do-not-mark-full-native-analytics-complete"></a>

### Pending acceptance — do not mark full native analytics complete

- Current-bundle full Go and the required full dashboard cycle passed as recorded above; retain any further required targeted native/API race checks separately. The backend full Go repeat passed 1192.219 s before the later chart correction ([receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-composed-full-go-repeat1.json)). The composed chart-bundle full Go also passed 1382.588 s ([receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-composed-full-go-chart-final.json), raw SHA-256 `e27a33b97007884bf58005cda3058ee9da44af28ed8ee99bcfa410ef45649772`); it became historical when the Source Find correction was applied. Both are backend/historical-bundle evidence. The final current-source native shared/storage repeat passed 165.280 s ([receipt](../../qa/reviews/feature-completion-native-partitions-2026-10-05/evidence/native-shared-storage-repeat2.json)). The final current-bundle full Go and dashboard receipts above supersede those historical bundles for the merge gate; all failed receipts remain preserved.
- Complete the reproducible **1 GiB compressed-input** multi-file acceptance on the specified 4 vCPU/16 GiB host with quota-controlled scratch. Record actual input size, reference results, uncached import/query duration, peak process RSS, spill, live telemetry lag, cancellation, disk exhaustion and contention. The user's earlier 10 GB waiver did not waive this 1 GiB gate. Current tests are correctness fixtures, not a scale benchmark.
- Qualify all five published native packaging lanes: Linux amd64/arm64, macOS amd64/arm64 and Windows amd64. This session's local Darwin arm64 native checks do not establish the other lanes.
- Complete full controller restart/recovery, HA takeover and remote artifact-backend lifecycle acceptance. Actual SQLite/PostgreSQL metadata close/reopen and two-store fencing evidence are narrower.
- Retain roadmap limits for richer histogram/summary operations, broader partition/directory semantics, saved analytical workflow breadth and other remaining native items. No arbitrary SQL, unsupported peer capability or empty native-capability success is advertised by this slice.

Build/test environment used the session's pinned Go 1.26.5 toolchain, shared task cache, `GOMAXPROCS=2`, package-build parallelism 1, 6 GiB Go memory setting, and test analytics limits of 512 MiB memory/128 MiB spill. These settings are not a claim that a hardware-qualified resource benchmark passed. Shared build caches remain in use by root's remaining gates; final session cleanup is deferred until all dependent work finishes.

<!-- Original record: docs/qa/reviews/filtered-native-analytics-2026-10-01/REVIEW.md; evidence retained in docs/qa/reviews/filtered-native-analytics-2026-10-01/ -->
<a id="filtered-native-analytics-2026-10-01"></a>

## Filtered native analytics — 2026-10-01

Status: implemented and reviewed; no remaining actionable findings in the delivered scope. Builds on published commit `10e9f08e` in the existing implementation worktree.

<a id="filtered-native-analytics-2026-10-01--delivered-scope"></a>

### Delivered scope

- Optional saved archive queries select action/business population, exact action identity, approved retained property categories, elapsed windows and bucket width. Existing run-total queries retain their behavior.
- A viewer-authorized, bounded archive query streams canonical JSONL observations and computes counts, error percentages and raw nearest-rank latency percentiles. Missing evidence remains missing. Limits: first 24 elapsed hours, 1,000 buckets, 100,000 selected observations, existing scan byte/record budgets, 30-second deadline and shared analytics admission.
- Saved comparisons use the same selection and elapsed window for two project-authorized runs, expose added/removed identities and warn when workload/version compatibility or evidence completeness is not established.
- Native panels show saved selection context and support CSV/SVG as applicable. Chart/table bucket selection opens a paginated inspector using the existing archive transaction API and redacted snapshot/header/body components.
- Updated backlog and capability ledger; broad analytics parity remains partial. General dimension grouping, filtered live timelines and monitor/imported overlays remain open.

<a id="filtered-native-analytics-2026-10-01--review-and-fixes"></a>

### Review and fixes

- Assigned the read-only POST query `results:read`, replacing the inferred `tests:run` service-account permission. Added route-body limits and explicit authorization characterization.
- Added exact action identity and exclusive-end filter options so adjacent bucket inspections reconcile without case collisions or boundary double counting; existing inclusive archive filters remain compatible.
- Reject unsupported dimensions, overlays and multiple-series requests explicitly. Queries exceeding raw limits return errors, not truncated results.
- Added retry controls for busy analytics admission and cancellation guards for stale frontend responses.
- Visual review found insufficient dark-theme contrast and table spacing; added scoped theme-aware panel/form styles and visible saved selection context.

<a id="filtered-native-analytics-2026-10-01--validation"></a>

### Validation

Full `go test -ldflags=-w -timeout 20m -p 1 ./...` passed, including generated contracts. Targeted `go test -race -ldflags=-w ./pkg/api ./pkg/storage` for native archive queries and route characterization passed. Documentation checks and `git diff --check` passed. Tests cover raw percentiles, population separation, missing/legacy evidence, query limits and cancellation, saved query round trips, project authorization, second-viewer access, comparison identity changes, and browser save/reopen/inspection in both auth profiles.

An early targeted race build overlapped replacement of embedded dashboard assets and failed to build; it is not counted as a passing test. Subsequent targeted Go builds were stopped to serialize overlapping API compilations; the final serialized race run passed. The first full Go run exposed two stale route characterization expectations (sample path and route count); both were corrected before the final run.

Dashboard validation:

- Typecheck, lint and production build passed; all 444 unit-test files / 4,676 tests passed.
- Broad `npm run test:cycle`: dev-admin 176 passed, 4 skipped, 2 timed out (execution teardown and JMeter import); local-auth 98 passed, 1 skipped. The broad command returned failure and is not recorded as an all-green run.
- Focused final browser reruns: dev-admin 15 passed, local-auth 10 passed, including both previously timed-out workflows. Screenshot-only final reruns passed in both profiles.
- Visual review: [dev-admin](../../qa/reviews/filtered-native-analytics-2026-10-01/filtered-panel-dev-admin.png) and [local-auth](../../qa/reviews/filtered-native-analytics-2026-10-01/filtered-panel-local-auth.png).

<a id="filtered-native-analytics-2026-10-01--limits"></a>

### Limits

This is archive-first. No live filtered history, arbitrary SQL, cloud acceptance, 10 GB benchmark, automatic regression verdict or new runtime SLA action is claimed. Browser fixtures exercise UI behavior over real saved definitions with controlled archive responses; Go tests exercise actual JSONL aggregation and API authorization.

<!-- Original record: docs/qa/reviews/live-grouped-overlays-2026-10-01/REVIEW.md; evidence retained in docs/qa/reviews/live-grouped-overlays-2026-10-01/ -->
<a id="live-grouped-overlays-2026-10-01"></a>

## Live/grouped analytics and overlays — 2026-10-01

Status: implemented and reviewed; no remaining actionable findings in this slice. Builds on published `3cb32972`. Broad-run limitations and successful reruns are recorded below.

<a id="live-grouped-overlays-2026-10-01--delivered-scope"></a>

### Delivered scope

- Versioned bounded action/transaction/property/region/dependency publication summaries survive history persistence and hydration; additive generator RPC fields distinguish supported evidence from old/mixed peers. Detailed evidence stays off the metrics WebSocket feed.
- Saved live selections refresh without overlapping requests. Unsupported independent-dimension intersections fail. Grouped archives compute percentiles from raw observations; 20-series, 1,000-bucket and 100,000-observation caps fail explicitly.
- Monitor and associated imported-dataset overlays reuse project authorization, durable artifacts, resource admission and analytical queries. Explicit unit axes preserve distinct measurements; archive lag correlation exposes coverage and unscored evidence.
- Existing saved archive comparisons and run-total panels retain their semantics. IBM MQ is explicitly very low priority.

<a id="live-grouped-overlays-2026-10-01--review-fixes"></a>

### Review fixes

- Fixed a distributed aggregate early return that bypassed evidence retention.
- Added fail-closed evidence versions for mixed peers and source-plan runtimes without side-channel summaries.
- Kept group-filtered responses from disclosing unrelated native summaries and compacted terminal resident evidence.
- Validated dataset association before analytical reads; rejected unsupported dataset dimension filters instead of ignoring them.
- Preserved project identity and missing-value quality in shared correlation inputs.
- Rendered each chart line from its own timestamps, so asynchronous overlay timestamps do not introduce artificial breaks in other series.
- Reset incompatible hidden group selections when changing population and corrected live-population labels.
- Added visible chart gaps for uncovered live publication intervals.
- Registered schema release 24 for the additive native interval evidence column, preserving all prior migration fingerprints.
- Added the missing local-auth login to the new browser test before API workspace creation; unauthenticated setup correctly returned 401.

<a id="live-grouped-overlays-2026-10-01--validation"></a>

### Validation

- Full Go suite executed. Its only failures were generated QA dashboard freshness and the missing schema release fingerprint. Both were fixed; the complete storage suite and QA freshness tests passed on rerun. API, generator, engine, protocol, runner and script packages passed in the broad run. The broad command itself was not rerun after these metadata fixes.
- Targeted API/storage/generator race tests passed. Native DuckDB import and overlay acceptance passed against real normalized Parquet, including association denial and ten aligned correlation pairs.
- SQLite persistence, clone, prior-schema upgrade, historical-record preservation and reopen checks passed. PostgreSQL integration passed again after schema release 24; the disposable database was removed.
- Dashboard typecheck, lint, build and five new panel tests passed. Two broad unit runs hit separate five-second timeouts in existing SecretBindingReview stress and recordingProxyCommands lock tests. Each complete affected file passed isolated reruns; neither broad unit command is recorded as all-green.
- Dev-admin browser suite: 179 passed, four profile/fixture skips. Local-auth broad suite: 96 passed, one fixture skip, three failures. Its new-test setup failure was fixed by adding login; the existing authoring and debugger timeouts, plus a later loaded-host analytics timeout, passed without changing assertions/timeouts when the full affected files were rerun (seven analytics/authoring tests and three debugger tests passed).
- Generated API contracts, QA dashboard freshness, documentation checks and whitespace checks passed. Broad commands with initial failures are not reported as clean single-pass runs.

Rendered evidence was inspected in both auth profiles: [dev-admin overlay](../../qa/reviews/live-grouped-overlays-2026-10-01/dev-admin-overlay.png), [local-auth overlay](../../qa/reviews/live-grouped-overlays-2026-10-01/local-auth-overlay.png).

<a id="live-grouped-overlays-2026-10-01--scope-limits"></a>

### Scope limits

Live queries use original publication intervals and fixed elapsed windows; live percentile re-bucketing, independent-dimension intersections and live lag correlation remain unavailable. Source-plan runtimes lacking summaries and older peers return explicit unavailable evidence. Browser transport fixtures exercise saved-definition/UI behavior; real Go fixtures exercise aggregation, persistence, RPC merging and authorization. The 10 GB benchmark remains waived. No live cloud or cross-platform release acceptance is claimed.
