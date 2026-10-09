# Feature completion Wave 4 — 2026-10-09

Status: implemented on `claude/feature-completion-wave1` and reviewed. Each work package went through independent review rounds until no finding stayed open, then the whole wave was reviewed once. Every finding is fixed or documented below. The requirements these packages advance remain open; nothing here promotes support.

Wave 4 delivers six work packages of the [feature completion specification](../../../FEATURE_COMPLETION_SPEC.md), building on [Wave 3](../feature-completion-wave3-2026-10-08/README.md). Schema release 54 (`dashboard_definition_revisions` and its project index) is additive; the next free release is 55. The once-per-run webhook claim is a row in `app_settings`, so it needs no schema change. Archived coverage gains `observer_lost`, per-source `required_ms` and `unknown_ms`, and `source_requirements`. Older archives keep working with the documented fallbacks.

## Work packages

| WP | Requirement | What landed | Commit |
| --- | --- | --- | --- |
| C7 | TEL-3 | Telemetry acceptance verdicts (`pass`, `fail` or `inconclusive`) judged against the policy definition frozen at Start, kept separate from the application outcome in CI. The verdict appears in CI, the live Telemetry card, the run report, saved comparisons and a once-per-run webhook | ec8202dc6 |
| E2 | BI-2 | `browser_baseline.v1` in the RCA engine: browser document metrics compared per exact measurement cohort and steady load level with the workload-qualified baseline run, scored with the shared three-sigma scorer and advisory only | 57ac7a2ca |
| E4 | BI-3 | Browser interaction patterns (`scripted_repetition`, `repeated_gesture`, `retry`, `dead_interaction`, `unexpected_navigation`) and per-layout heatmaps from a finalized run's interaction evidence, with coverage, in the run report | 27c744851 |
| B7 | NA-4 | Saved panel revisions (`?revision=N`, a read-only Saved revision choice), the panel `query_version`, `artifact_sha256` dataset pins in comparisons and overlays, and the `apexctl analysis` catalog, panel and query commands | ef858aa9d |
| A7 | AUTH-2 | The authoring lifecycle parity matrix: each fixture's exact static findings across the parser and twelve routes, import saves, the import route inventory and the AUTH-2 Playwright proofs, generated as `docs/qa/LIFECYCLE_PARITY_MATRIX.md` | 832bc5a1d |
| D8 | BE-4 | The browser acceptance inventory (`docs/qa/BROWSER_ACCEPTANCE.md`, generated and checked), new controlled-page fixtures in Chromium and WebKit, and the opt-in browser benchmark with F4 acceptance requests | 77ed46bd1 |

d8563572b merged main before the wave started. 08c8c5fda merged main's UX Medium fixes before the review fix commit, and 24ab68c4b merged its UX Low and Polish fixes before the last two verification rounds, which read the merged code. Per-package review findings were fixed in 8676790af, and the cross-cutting review's findings in b31fdc1ae. 0851d550b updated one of main's end-to-end specs that its UX Low fixes had left stale (see Gates).

## Review

Each package was reviewed by an independent agent that read a frozen snapshot of the implementation commit against its specification. Fixes were re-reviewed round by round, each round against the previous round's findings and anything the fix introduced, until every package was clean.

The first round raised 59 findings: 3 high, 20 medium and 36 low. The high ones were:
- C7: partial stage evidence could pass a `fail_ci` policy.
- E4: a response without patterns (`null`) crashed the run report.
- E4: retries were never detected, because each engine retry is a new execution with its own action ID.

Later rounds:
- **Round 2:** all six packages still had findings, mostly partial fixes and missing tests. The full dashboard suite also showed that E4's panel crashed the run report when a response lacked coverage.
- **Round 3:** D8 was clean.
- **Round 4:** E2 and B7 were clean.
- **Round 5:** C7 was clean.
- **Round 7:** E4, A7 and the gate fixes (below) were clean, which closed the per-package review.

The fixes were committed once, as 8676790af, after the closing round.

The gates found a defect that no package review had reached. Main's end-to-end specs showed that two Wave 3 panels echoed the raw server text "archive not found" in an alert:
- the Journey risk panel;
- the inspector's Browser HARs list, whose second alert was a strict-mode violation.

E4's new panel had the same pattern. All three now:
- say plainly when the run has no archive yet;
- offer Check again while an archive is missing or not finalized;
- show other failures through the shared notice, with a retry.

A final review read the whole wave through four lenses: API contracts and security; analytics and documentation; storage and HA; and runtime and protocol compatibility. It found 4 medium and 12 low findings. One medium was raised by both the storage and runtime lenses, so there were three distinct medium defects:
- **Coverage after a lost observer was failed instead of being a lower bound.** After the source ledger was lost (a restart before finalize, or a controller that never executed the run) or after an HA adoption, unobserved time was recorded as exact. A coverage criterion could therefore fail a run whose missing time was simply unknown.
- **`unexpected_navigation` fired on ordinary navigating steps.** A step whose wait was met after the navigation, waited for a stable DOM, or paused was flagged.
- **`browser_baseline` ignored the pinned `browser_policy`**, although the documentation said its effect floors and baseline mode governed it.

The fixes were then checked by verification agents, round after round, against a snapshot of the uncommitted working tree, and committed once (b31fdc1ae) when the cycle closed.

| Verification round | Findings (all low) | Examples |
| --- | --- | --- |
| 1 | 23 | Live coverage never marked a lost observer. On PostgreSQL the webhook claim did not wait for an uncommitted run delete, so it now takes `FOR SHARE`. The interactions route checked request cancellation before success, so it could panic on a nil error. |
| 2 | 8 | On an HA controller, the archive panels said "could not be found" while the archive was still being published. Report and webhook fallbacks for coverage recorded before per-source requirements existed. |
| 3 | 6 | Stale comments. The Check again buttons were never clicked in tests. |
| 4 | 2 | A test's failure message. A constants comment. |
| 5 | 2 | A served analysis note still said all pointer gestures with points repeat. Two loading labels kept "..." after main's Polish fixes standardized on "…". |

Main was merged (24ab68c4b) after round 3, so rounds 4 and 5 read the merged code. Round 5 also checked the merge against the fixes: the one conflict's resolution (`RunReportPage.tsx`) keeps main's report-focus anchors and the Wave 3 and 4 panels, and nothing main changed contradicts the fixed panels or the telemetry acceptance section. Round 5's two findings were wording only, so they were fixed and the cycle closed without another round.

Notable corrections from review:

- **Telemetry verdicts.** A stage distribution sampled from a failover onward is partial: only a maximum already above the limit fails. A percentile taken with excluded observations lies between bounds read from the archived stage digest at exact rank positions. Browser render is advisory and never decides the verdict. The webhook is announced once per run and outcome through a durable claim, so a verdict that changes is announced again.
- **Lost observer.** When the observing controller was lost (`observer_lost`: `ledger_lost` or `adopted`), live and archived coverage is a lower bound. A coverage criterion below its floor is then inconclusive, never a failure. Coverage is measured per source over that source's own requirement.
- **RCA browser baseline.** The baseline follows the pinned `browser_policy`: its floors only raise the defaults, and the modes `none` and `pinned_run` are honoured. The run's own document evidence loss makes the section partial or unavailable, and `detect.v1` no longer compares document series that the browser baseline would refuse.
- **Interaction patterns.**
  - Retries are recognized by action name and gesture kind.
  - Touch gestures, which have no dispatched points, repeat on one action name, gesture kind and target.
  - Heatmaps are keyed by device pixel ratio. Gestures whose engine is unknown are counted and disclosed.
  - The route answers 404 (no archive yet), 409 (not finalized), 500 (unreadable) or 503 (request cancelled).
- **Parity gaps found by the A7 matrix.** `dsl.Parse` reported only a script's first static finding. Debug and replay of a draft judged the masked copy, so a credential-bearing JVM argument read as a different finding than on every other route. Both are fixed.
- **Saved panel revisions.**
  - A revision read always reports the current revision, taken from one snapshot.
  - An invalid definition answers 400 and a store failure 500.
  - `apexctl analysis panel --revision` refuses a response for a different revision.

## Documented deviations and open work

The specification records each package's deviations in full; in brief:

- **Telemetry verdicts (C7):**
  - The webhook carries no evidence links and creates no incident. A dispatch lost after its claim is not retried.
  - A run that pinned a policy before definitions were frozen is inconclusive in mode `unknown`, which no gate enforces.
  - Only `apexctl ci run` shows the verdict, and no CLI selects a non-default policy. There is no server-side JUnit export.
  - Saved comparisons show the verdict and dropped results only.
  - The live verdict leaves breached time inconclusive until finalize, and a local run's live coverage is recorded only at finalize.
- **Browser baseline (E2):**
  - Authored action and route identity are not in the cohort key; the scenario group stands in, and only while the baseline ran the same script and configuration.
  - Only `outside_steady_load` is counted per bucket.
  - No catalog effect floors exist, so metrics other than timings and CLS are unscored.
  - Browser findings (E1) do not cite these results.
  - The run's side is the median of its bucket p75 values.
  - The analysis names its baseline run, so `latest_compatible` uses that run rather than selecting one.
- **Interaction patterns (E4):**
  - There are no screenshot overlays, no child-frame maps and no CLI command.
  - Patterns are found among the first 100,000 interactions.
  - Engine and profile come only from document evidence; without it they are disclosed as unknown, and engines that share one layout can share a map.
  - Cells are viewport positions at dispatch.
  - Distinct steps that share an action name are one step.
  - Dead interactions come only from post-action waits.
  - The repeat window is measured on capture time.
  - The protocol envelope does not carry the engine, because the strict decoder would break mixed generator versions.
- **Saved queries (B7):**
  - There is no result cache.
  - Alignment is by elapsed time only.
  - Coverage thresholds are not a saved setting.
  - The dashboard does not set pins.
  - A revision records its query version, not the policy or catalog versions it was saved under.
  - Panel options are not driven by capabilities, and there is no bucket drill-down or panel export.
- **Lifecycle parity (A7):**
  - Contextual runtime, install, policy and approval outcomes have no new cause-code fixtures.
  - The single-script import commits are not held to the persisted-copy check.
  - The import saves are exercised directly and held to it by a source check, not driven over HTTP.
  - There is one gRPC fixture and one Kafka fixture, and no governed-runtime fixture.
  - The AUTH-2 Playwright proofs are not mapped to every AUTH-2 property, and the recording proof mocks the recorder.
  - Launchers outside `/api/v1/scripts/import/` are not inventoried.
- **Browser acceptance (D8):**
  - No benchmark results are published; the five-browser-VUser ceiling stands. The benchmark's profile names differ from the evidence's profile IDs.
  - Some limits are tested without a browser.
  - Viewer/editor separation and project denial run in the local-auth profile only.
  - The walk starts from an archived run and downloads a synthetic HAR.
  - No test crashes a browser under a live generator.
  - Cache provenance is not asserted per response.
  - WebKit's desktop benchmark sets a viewport rather than using a device profile.
  - Cold launch is timed against a closed browser pool, not a cold host.

## Observations

- **Lint findings.** Running the full `npm run lint` during review surfaced two component-module exports that broke the fast-refresh rule. A Wave 3 stage-coverage formatter moved to `telemetryStageCoverage.ts`, and an E4 API helper is no longer exported.
- **Main's LoadRunner review spec.**
  - `workflow-loadrunner-review.spec.ts` failed intermittently in both profiles after main's UX Medium fixes made a reviewed note leave the unresolved count. Its count assertion passed only when it ran before the badge re-rendered.
  - Main's UX Low fixes (fce027a83) rewrote that assertion against the Review tab's accessible name. The merged code runs main's version.
- **Local environment.**
  - A Docker reset removed the PostgreSQL container and image mid-cycle, so the PostgreSQL gates failed on refused connections; they were rerun on a recreated container.
  - The worktree's `dashboard/node_modules` was a symlink into another worktree that had been removed. It was replaced with a copy built from the same lockfile.
  - The Go build cache regrew to 21–32 GB between gates. It was cleared, and every later gate checked free space first.
- **Uncommitted fixes across the merge.** The verification rounds read snapshots of the uncommitted working tree. Main merged cleanly underneath the uncommitted fixes, which touched none of the files main changed.

## Gates

Final gates: macOS arm64, Go 1.26.5, Node 24, PostgreSQL 16 in a local container. All ran on the merged tree (24ab68c4b) with the cross-cutting fixes applied; the fixes that followed its start changed only a comment, a served note and two labels.

| Gate | Command | Result |
| --- | --- | --- |
| API | `go test ./pkg/api/ -count=1` | pass (695 s) |
| Every other Go package | `go test` over `go list ./pkg/... ./cmd/...` without `pkg/api`, `-count=1` | pass (125 packages, 162 s) |
| Scripts, QA docs | `go test ./scripts ./docs/qa -count=1` | pass |
| Real Chromium and WebKit | `APEXLOAD_BROWSER_TESTS=require go test ./pkg/protocol/browser -count=1` | pass |
| PostgreSQL storage | `go test -tags postgres_integration ./pkg/storage -count=1` | pass |
| PostgreSQL API | `go test -tags postgres_integration ./pkg/api -run Postgres -count=1` | pass |
| API with DuckDB | `go test -tags analytics_duckdb ./pkg/analysis/... ./pkg/api -run 'Test(Analysis\|Native\|Parquet\|Container\|Lagged\|Bucket)'` | pass |
| Dashboard tests | `npx vitest run` | pass (6,596 tests) |
| Dashboard lint and typecheck | `npm run lint`, `npm run typecheck` | pass |
| Docs | `python3 scripts/check-docs.py` | pass |
| Browser end-to-end | `scripts/run-e2e.sh`, the full suite on a freshly built bundle, default and local-auth profiles | 271 passed and 6 skipped (default), 215 passed and 1 skipped (local-auth); one failure in both, see below |

The one end-to-end failure was main's. Its UX Low fixes (fce027a83) made links that open a new tab say so in their accessible names, but `workflow-monitoring.spec.ts:305` still looked for "Open in AppDynamics". The spec was updated in 0851d550b, and the whole spec file then passed in both profiles (16 of 16).

The HA, RCA real-run and live-provider opt-in suites, and the browser acceptance benchmark (`APEXLOAD_BROWSER_BENCHMARKS`), were not run for this wave.
