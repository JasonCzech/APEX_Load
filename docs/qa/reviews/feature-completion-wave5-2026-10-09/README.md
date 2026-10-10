# Feature completion Wave 5 — 2026-10-09

Status: implemented on `claude/feature-completion-wave1` and reviewed. Each work package went through independent review rounds until no finding stayed open, then the whole wave was reviewed through four lenses until those were clean too. Every finding is fixed or documented below. The requirements these packages advance remain open; nothing here promotes support.

Wave 5 delivers five work packages of the [feature completion specification](../../../FEATURE_COMPLETION_SPEC.md), building on [Wave 4](../feature-completion-wave4-2026-10-09/README.md). It adds no schema release; the next free release is still 55.

## Work packages

| WP | Requirement | What landed | Commit |
| --- | --- | --- | --- |
| A8 | AUTH-3 | Authoring adapters retired behind single owners, with a decision, owner and parity proof per seam in `docs/qa/ADAPTER_RETIREMENT.md` and a ratchet against reintroduction. Import routes name their launcher; the scenario form keeps only canonical values; load previews share one stage model and draw start offsets | d306dfbd5 |
| E6 | BI-4 | The run report's Browser area (engine × device × group comparison, findings with the drill-down from finding to document to resources to HAR entry, interaction patterns, journey risk), the HAR entry API, the browser and journey policy editor, a saved-panel population and report sections | 6ee6ffa73 |
| E7 | BI-4 | The browser intelligence golden in Chromium and WebKit against the page's own measurements, a generated acceptance inventory (`docs/qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md`) and F4 acceptance requests | 5487d9d94 |
| B8 | NA-6, NA-5 | Native analytics acceptance goldens whose series come from DuckDB's own SQL, duplicate-file refusal at commit, the opt-in 1 GiB reference benchmark with F4 requests, and the NA-5 platform lanes in `docs/qa/ANALYTICS_ACCEPTANCE.md` | 3a69514c8 |
| C8 | TEL-4 | The telemetry fault matrix (`docs/qa/TELEMETRY_ACCEPTANCE.md`) as an opt-in race-detector suite, and the opt-in reference workload (two generators, a foreground dashboard, concurrent analyses) with F4 requests | 8b6b885fa |

Two merges of main came in during the wave: 61e050b78 (origin 7c5705332) before E7, and 76270de81 (origin 8ebd11cb6) after it. The second made main's RCA browser baseline and E6's report section declare the same type; 8538a670d keeps main's model and reports the baseline once, in the root-cause section. Per-package review fixes are 76fd07288, and the cross-cutting fixes are 4759d2ee8.

## Review

Each package was reviewed by an independent agent that read a frozen snapshot of its commit against its specification. Fixes were re-reviewed round by round against snapshots of the uncommitted working tree, each round against the previous round's findings and anything the fix introduced.

The first round raised 40 findings: 2 high, 9 medium and 29 low. The high ones were:
- **B8 and C8:** the reference harnesses called `syscall.Getrusage`, which breaks the Windows build of the API tests. The rusage and RSS helpers are now build-tagged, with a stub that records the values as not measured.
- **C8:** a run that stopped on a fatal error was written as a `pending` F4 request. It is now `failed`.

The review also found a gap in A8: the plan's script session absorption (the editor's paired baseline, dirty calculation, save state and navigation moving into `useScriptAuthoringSession`) was not done. It is recorded as a deviation rather than done in a review round. That is a deep move inside the 7,300-line script editor, already deferred once from A5.

Later rounds:
- **Round 2:** E7 was clean. The other packages had 16 findings, mostly partial fixes. The medium ones were generated API docs not regenerated after a new report field (found by two reviewers), and C8's live loss path counting successes only.
- **Round 3:** A8, the baseline integration and E6 were clean. B8 had 1 low and C8 2 (a reported lost publication excused a live shortfall of any size).
- **Round 4:** B8 was clean. C8 had 1 low (that bound was about four times too loose).
- **Round 5:** C8 was clean. The fixes were committed once, as 76fd07288.

A final review read the whole wave through four lenses: API contracts and security; analytics, telemetry and documentation; dashboard and authoring; and browser runtime and test evidence. It found 32 findings, 3 of them medium:
- **HAR entry scans were not admitted.** Each request scanned the whole archive with a 30-minute deadline and no permit, unlike the resource reader beside it. It now shares the reader's single scan permit and 10-second deadline, and answers a retryable 503 when busy or out of time.
- **The policy editor dropped unsaved edits** on a project switch, a settings tab switch or another policy, with no prompt. It now guards them and stays mounted across tab switches.
- **The reference workload's rate included start-up and drain**, so a short run failed the 90% gate without any real shortfall. The rate is now taken from the first archived sample to the last.

| Verification round | Findings | Examples |
| --- | --- | --- |
| 1 | 11 (2 medium) | A settings sub-tab switch still unmounted the policy editor. The drill-down end-to-end test still waited for the old status text. A materialization timeout answered 500 instead of 503. |
| 2 | 2 distinct (1 raised by three lenses) | The run report and export suggested evaluating under a named policy revision without saying that result never appears there. |
| 3 | 0 | |

Notable corrections from review:

- **Loss accounting.** The C8 harness accounts loss per path. The archive's samples, and separately the live totals (successes and recorded errors) plus the dropped-results counter, must account for every served request but those aborted in flight at the stop. A reported lost publication may account for at most one generator's largest window.
- **The live run total was short.** With that accounting, 20-second local runs showed the archive exact (43,189 served, 43,189 archived) while the live totals fell 1,900 to 5,900 short with no recorded errors and every loss counter at 0. A live window could go missing, and the final window was counted again by each terminal publication. This defect predates the wave, and the harness exposed it by failing the reference workload on the live path. It was not fixed in this wave. The follow-up fix, which also covers a stop requested on the controller, and its limits (after a controller takeover, and with older generators) are recorded in the C8 notes of the specification.
- **Exports.** Evaluation failures and store errors stay out of exported reports. Report CSV cells that an editor or the target authored never become spreadsheet formulas, while numbers are kept. `browser_intelligence` left the JSON report contract, which never filled it.
- **The Browser area** appears only for browser groups, document evidence or a pinned browser policy (journey risk, which any protocol may pin, stays on the Performance area). A Browser link to another run falls back to Summary, and loading states never read as absent evidence.
- **Template seeds.** Applying a template keeps the scenario's authored iterations, log level, auto transactions, error flags and browser settings that the template does not set.
- **Engine coverage.** The interaction outcome, keyboard target and touch tests that the inventory had marked Chromium-only run in WebKit too.

## Documented deviations and open work

The specification records each package's deviations in full; in brief:

- **Adapter retirement (A8):**
  - Script session absorption is not done (see Review). Validation is not bound to capability, policy or approval revisions, which have no client-visible revision yet.
  - TestHub keeps a form as its editing view, holding only canonical values.
  - The wizard's model chart keeps its own builder. A queued run's preview draws profile-mapped groups from their ramps. `NewTestPage`'s group edit draft stays a run-only projection.
- **Browser area (E6):**
  - Pages are compared by scenario group, at p75 with a document count only.
  - Findings, the baseline, interactions and journeys read the finalized archive, so the live page shows only the comparison.
  - A run pins its project's default policy of each kind; no per-run revision choice is offered.
  - A group that ran under several runtime cohorts cannot be made a saved panel from the dashboard, which never shows the cohort digest.
  - The drill-down end-to-end test uses transport fixtures beyond the real run and archive, and the journey-policy editor has component tests only.
  - The baseline appears in report exports only for projects that present root-cause v2.
- **Browser intelligence golden (E7):**
  - Baseline regressions are accepted by unit tests over seeded series, and layout mismatch and exclusion rules by unit tests only.
  - The golden drives the browser runtime directly, not the engine.
  - INP is not in the golden.
  - Nothing enforces the rule that baseline intelligence is not released before BE qualification passes.
- **Native analytics (B8):**
  - DECIMAL values beyond a double's precision are rounded, not refused.
  - A drifted file fails the import with a fixed message that names neither file nor column.
  - Conflicting files keep every observation, and identical content chunked differently is refused only at normalization.
  - Partition values are not read, so partition conflicts are neither resolved nor tested.
  - The benchmark does not measure spill, live lag, a cold replica, a worker crash, disk exhaustion or contention, and does not check scratch capacity.
  - The platform lanes run the native checks only, without install, uninstall or pinned hashes, and post no acceptance records.
- **Telemetry (C8):**
  - The reference workload failed on the live path (see Review; fixed after the wave, with the limits the specification lists). Its transport and sample-age P99 spikes remain unexplained, and generator processing includes the half-second aggregation window, so the default budgets read a run as breached.
  - It runs in one process, not on the reference controller shape, without collector traffic, an overload trial or native analytics, and it records no queue or byte volumes.
  - No test changes the cadence or policy within a run, and no acceptance record is published.

## Observations

- **Disk exhaustion during the gates.** Review snapshots in the session scratch directory (about 18 GB) and the Go build cache (43 GB) left 5.7 GB free. Nine DuckDB API tests then failed with 503 ("analytical admission unavailable"). Finished snapshot trees were removed and the build cache cleared, and the suite passed on the next run.
- **Load-sensitive tests.** Two HA adoption tests failed once on a coordinated-start barrier while a reference workload and a browser were running on the same host. They passed three times in a row in isolation.
- **Merge integration.** Main's RCA browser baseline (merged in 76270de81) and E6's report section both declared `ReportBrowserBaseline`, which broke the build until 8538a670d removed E6's copy.

## Gates

Final gates: macOS arm64, Go 1.26.5, Node 24, on 4759d2ee8 (main at 8ebd11cb6 merged in, and unchanged since).

| Gate | Command | Result |
| --- | --- | --- |
| API | `go test ./pkg/api/ -count=1` | pass (704 s) |
| Every other Go package | `go test` over `go list ./pkg/... ./cmd/...` without `pkg/api`, `-count=1` | 129 of 130 passed. `pkg/recorder` failed once on a WebSocket capture race in `TestNativeRecordingParityCorpus` (no Wave 5 change touches the recorder); it passed 5 of 5 alone and again as a whole package |
| Scripts, QA docs | `go test ./scripts ./docs/qa -count=1` | pass |
| Real Chromium and WebKit | `APEXLOAD_BROWSER_TESTS=require go test ./pkg/protocol/browser -count=1` | pass |
| API with DuckDB | `go test -tags analytics_duckdb ./pkg/analysis/... ./pkg/api -run 'Test(Analysis\|Native\|Parquet\|Container\|Lagged\|Bucket\|Worker\|RCAImported)'` | pass |
| Dashboard tests | `npx vitest run` | pass (6,703 tests in 600 files) |
| Dashboard lint, typecheck and contracts | `npm run lint`, `npm run typecheck`, `npm run contracts:check` | pass |
| Docs and MCP tools | `python3 scripts/check-docs.py`, `python3 -m pytest mcp/tests` | pass |
| Browser end-to-end | the Browser area, findings, HAR and journey specs, the retained resource inspector and the route specs, on a freshly built bundle, default and local-auth profiles | pass |

At the user's request the full end-to-end suite was not run for this wave; the specs above cover what it changed. The PostgreSQL suites were not rerun (no storage or SQL change). The opt-in reference workloads ran locally while being built and reviewed, and the telemetry reference workload fails on the live path as described above; the HA, RCA real-run and live-provider suites were not run.
