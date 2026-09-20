# Dashboard analysis implementation and review

Implemented September 20, 2026 following the dashboard Product Design audit.

## Operator workflow

Load Comparison now separates action, named-transaction, legacy, and group-summary evidence. It provides individual-run and release-summary views, explicit baseline/candidate selection across application releases, merged p50/p95/p99 or mean latency, successful/failed latency selection, successful throughput and errors, numeric load spacing, and missing-point gaps. A measurement window includes complete retained intervals only. Exploratory limits identify the highest **tested** individual-run load meeting all three limits and the minimum retained duration. Pooled release duration cannot qualify a short repeat. Release summaries expose contributing IDs and the range of measured repeat statistics.

Release Trend Analysis now charts latency, successful throughput, errors, and observed load. Within-release and cross-release modes use one filtered comparison model for the latest summary, row, expanded detail, and CSV. Baselines can be the previous eligible visible run, a fixed selected run, or a recorded baseline. Scenario, script, environment, data, pacing, workload, duration, achieved load and archive coverage qualify the comparison. Missing context remains unverified; known mismatches cannot produce a qualified regression. Transaction drill-down sorts by degradation/acceptance breach, latency, or traffic and retains baseline and population when opening the full report.

Both tabs offer browser-local saved comparisons and context-bearing CSV exports. Release-wide export belongs to the trend tab. Readiness and operations panels collapse during analysis. Tables retain semantic rows, labeled controls, keyboard navigation, and contained scrolling.

## Review corrections

- Include idle retained intervals in throughput denominators.
- Merge raw-event quantile sketches; never average stored percentiles.
- Share bounded sketches by transaction and observed load across intervals so a long run does not allocate a sketch every second.
- Reject invalid archived durations, report excluded runs, and keep missing denominators distinct from measured zero.
- Clear stale responses on filter changes; ignore late request completions.
- Compare normalized workload settings rather than runtime timestamps or object key order.
- Keep release changes as the intended comparison variable, including full-report qualification.
- Calculate transaction throughput using the same population's retained rate denominator.
- Preserve an explicitly selected fixed baseline when filters exclude it; show missing evidence rather than silently substitute another run. Regression coverage also verifies restoring the filters restores the selected baseline.
- Protect CSV text cells from spreadsheet formula interpretation.

## Evidence limits

Limits and regression rules are exploratory, versioned fixed rules, not statistical significance or edits to stored acceptance criteria. A percentile can be unavailable in an older archive; group summaries cannot recover successful throughput or event percentiles. Operators select a known steady-state interval; the application does not infer one or extrapolate a capacity ceiling. Legacy duration fallbacks are labeled and cannot qualify the highest tested load. Offered-arrival-rate axes and causal telemetry correlation require retained evidence beyond this change. Saved comparisons reload current evidence; CSV is the frozen export.

Automated coverage includes population/window filtering, merged distributions, idle intervals, long-run memory accounting, inconsistent denominators, comparison qualification, actual-baseline deltas, stale responses, saved-view restoration, report routing and browser workflows at desktop/narrow widths. The dedicated browser scenario uses deterministic archive fixtures with real workspace/run IDs; it is UI evidence, not an application performance benchmark.

## Validation results

- `go test ./...`: passed, including API schema freshness and the new long-run distribution tests.
- Dashboard typecheck, lint, and embedded production build: passed.
- Full Vitest run: 407 files and 4,352 tests passed.
- Final fixed-baseline correction: all 11 Release Trend panel tests passed.
- `workflow-dashboard-analysis.spec.ts`: passed with dev-admin and local-auth, including actual chart rendering, missing evidence, saved comparison restoration, filtered baseline deltas, report routing, and contained narrow-width scrolling.
- Full `npm run test:cycle` was executed. Its broader browser gate is not green: existing Command Center/overview, dev-preview, live-run identity/control, and workspace-selector journeys still expect the preceding navigation UI. These are separate from the passing dashboard-analysis workflow; they were not weakened or disabled by this change.
- Full browser results: dev-admin 114 passed, 8 failed, 4 skipped; local-auth 41 passed, 1 failed, 1 skipped. The final baseline correction was additionally checked with typecheck, lint, production build and the focused browser workflow in both profiles.
