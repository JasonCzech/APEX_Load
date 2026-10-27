# QA Review Record — Browser Seeded-Regression Exit Through the Production Path (`BI-A4`, 2026-10-27)

## Scope

This slice closes `BI-A4` (`Seeded-regression exit through the production path`) by proving the end-to-end `BI-A` qualification, admission, baseline comparison, and `browser_findings.v2` provenance chain across two real managed Chromium runs launched through the controller's production HTTP and engine path (`TestBrowserSeededRegressionProductionPath` in [pkg/api/rca_browser_seeded_regression_test.go](../../../../pkg/api/rca_browser_seeded_regression_test.go)):

1. **Two Real Managed Chromium Runs Over a `>= 60 s` Steady Window (`>= 30` Post-Settle Buckets):**
   - Creates a scenario via `POST /api/v1/scenarios` and launches two real `98 s` (`VUsers: 2`, `RampUp: "0s"`) headless Chromium tests via `POST /api/v1/tests` and `POST /api/v1/tests/{id}/start`, pinning a `latest_compatible` `browser_policy` revision (`browser@1`).
   - Each VUser executes a 3-step script:
     - `warmup_settle` (`0 s..30.2 s` settle window, staggered across the two VUsers),
     - `steady_checkout_loop` (`33` iterations of `/checkout?id={{fn.uuid()}}` with `1750 ms` think time across `30.2 s..95.4 s`, producing `66` finalized main-frame navigation documents across `>= 30` post-settle 1 s buckets),
     - `park_steady` (navigates to `/park` at `~95.4 s` so the 33rd `/checkout` document unloads via `pagehide` inside the `"steady"` monotonic span before the `98 s` test duration ends).
   - Run 1 (`Baseline Storefront Run`) executes against the normal `/checkout` handler; Run 2 (`Regressed Candidate Storefront Run`) executes against `/checkout` with a seeded `+180 ms` server delay.
2. **Production `latest_compatible` Admission, `browser_baseline.v1` Anomaly Detection, and `browser_findings.v2` Provenance:**
   - Queues RCA via `POST /api/v1/tests/{id}/archive/root-cause/analyses` with an empty request body (`latest_compatible`), verifying that `SelectBrowserBaseline` freezes `Status: "selected"`, `SelectedTestID == baselineRow.ID`, `Policy: "browser@1"`, and a valid `Inputs.BrowserSelection` SHA-256 digest (`validateRCABrowserSelection`).
   - Runs `runNextRCAAnalysis` and `runNextEvaluationJob`, verifying that `GET /api/v1/tests/{id}/archive/root-cause/v2` reports `BrowserBaseline.Status == "complete"`, `Baseline.Qualification == "qualified"`, and `BrowserBaselineAnomaly` (`DirectionIncrease`, `BaselineBuckets >= 30`, `Effect >= EffectFloor`, `Z >= 3`) on `lcp` and `fcp`.
   - Verifies that `GET /api/v1/tests/{id}/archive/browser-findings` (`browser_findings.v2`) promotes `lcp` and `fcp` threshold findings to `State: "active"`, `Baseline.Status: "regressed"`, with exact `AnalysisID`, `BaselineRunID`, `BaselineArchiveSHA256`, `SelectionReceiptSHA256`, `SelectionStatus: "selected"`, and `PolicyPins`.
3. **Five Production Refusal / Exclusion Cases Against the Real Archives:**
   - `refusal_pause`: zero-VUser pause in the middle of `interval-summaries.jsonl` prevents any `>= 60 s` post-settle steady window from qualifying (`Selection.Status: "current_unqualified"`, `Reason: "load_levels_unavailable"`, finding `Baseline.Status: "unqualified"`).
   - `refusal_lost_source`: dropped observations (`DroppedObservations: 5`, `Incomplete: true`, `BrowserDocumentCoverageIncomplete: true`) refuse selection (`Status: "current_unqualified"`, `Reasons: ["current_document_evidence_incomplete", "source_loss"]`) and score explicit comparisons as `BrowserBaselineUnscored` (`Reason: "source_loss"`, finding `Baseline.Status: "unqualified"`).
   - `refusal_changed_script`: differing `BrowserAuthoredSources` `ScriptSHA256` / `StructureSHA256` rejects the baseline candidate (`Status: "no_compatible_candidate"`, `Rejected[0].Reasons: ["cohort_changed"]`) and marks explicit comparisons `BrowserBaselineUnscored` (`Reason: "cohort_changed"`, detail naming `"different or unrecorded script"`).
   - `refusal_incompatible_cohort`: differing runtime cohort `ViewportWidth` (`1920` vs `1440`) rejects the baseline candidate (`Status: "no_compatible_candidate"`, `Rejected[0].Reasons: ["cohort_changed"]`) and marks explicit comparisons `BrowserBaselineUnscored` (`Reason: "cohort_changed"`, detail naming `"viewport"`).
   - `refusal_missing_metric`: when `fcp` is unavailable in the baseline archive while present on the candidate archive, `SelectBrowserBaseline` rejects the baseline candidate (`Status: "no_compatible_candidate"`, `Rejected[0].Reasons: ["no_baseline_cohort"]`), the `fcp` finding reports `Baseline.Status: "not_selected"` / `Reason: "no_baseline_cohort"`, and explicit comparison marks `fcp` `BrowserBaselineUnscored` (`Reason: "no_baseline_cohort"`).

## Verification & Mutation Testing

- Real Playwright/Chromium production-path qualification (`APEXLOAD_BROWSER_TESTS=require`):
  - `APEXLOAD_BROWSER_TESTS=require go test -v -count=1 -timeout=15m ./pkg/api -run '^TestBrowserSeededRegressionProductionPath$'` (`PASS`, `200.83s`; all 5 refusal subtests pass).
- Acceptance inventory freshness:
  - `go test ./docs/qa -run '^TestBrowserIntelligenceAcceptanceInventory$'`
- Mutation verification (`go test -overlay`):
  - Mutated `pkg/api/rca_browser_selection.go` (`Status: "current_identity_unavailable"` -> `Status: "selected"`) via a temporary `-overlay` JSON mapping outside the workspace; verified `go test -overlay=... ./pkg/api -run '^TestRCABrowserSelectionAdmissionFreezesAndExportsRefusal$'` fails (`Exit code: 1`).
