# Dashboard audit implementation

Implemented September 12, 2026, following the [Dashboard audit](README.md).

## Changes

- Added a shared release-quality summary with recorded acceptance outcomes, archive coverage, latest report completeness, and explicit baseline availability. Completion remains an execution state.
- Elevated current attention items, put waiting runs ahead of historical archive gaps, grouped missing archives, and retained all alerts behind an expandable list.
- Included queued work in the active tab, displayed individual queue references and reasons, and routed queue actions to the selected run in Command Center.
- Replaced unsupported publication-progress claims with archive-unavailable language and retained access to results recovery.
- Added run-reference search and period, execution, evidence, and acceptance filters to Recent Runs.
- Made hidden narrow-screen VUser, duration, and archive-record fields available in expandable run details.
- Renamed Load Comparison and explained selected-series versus whole-run totals, metric populations, and missing comparison evidence. Clarified release CSV export scope.
- Applied the selected release consistently to run counts and history. Distinguished idle workloads, missing live metrics, disconnected streams, and stale system health.

## Review fixes

Live browser verification exposed a null capacity-warning list returned by the controller; the Dashboard now handles it. Review also corrected paused-run counting, unavailable metrics displayed as zero, cached report errors from other releases, premature baseline-absence claims while reports load, and a history-table height calculation that left only one visible row after adding the summary.

## Validation

- 63 focused unit/component tests passed across Dashboard, its data model, Load Comparison, and Release Trend.
- Browser inspection against existing local legacy data verified queue-to-Command-Center routing, evidence filtering, run identification, expandable narrow-screen details, and population/baseline explanations. No existing tests were started or modified.
- Added Playwright coverage for queue routing, keyboard navigation, filters, missing archive recovery, responsive details, and health failure/recovery in both authentication profiles.
- Type checking, lint, the production embedded build, and `git diff --check` passed.
- Browser profiles: **89/89 dev-admin** and **12/12 local-auth** passed. The two Dashboard workflows also passed a fresh dev-admin rerun after correcting authenticated fixture setup: the page must reload its project catalog after API seeding.
- Full unit suite: **4,080/4,082 passed**. An unchanged `SecretBindingReview` stress test exceeded its five-second timeout and its following test failed; all **60/60 tests in that file passed** when rerun alone (3.18 seconds of test execution). The full cycle therefore did not pass in one uninterrupted invocation; build and browser profiles were completed separately.

The shared checkout was being modified by concurrent tasks, producing unrelated routing, Results, and authoring failures during early runs. Final broad verification used a temporary copy of the committed baseline plus only the Dashboard implementation and tests. Every Dashboard source/test file was compared with the working checkout afterward and matched. This validates these changes independently; it does not certify the other concurrent edits.

## Screenshots

- [Implemented Dashboard](09-implemented-dashboard.jpg)
- [Narrow-screen run details](08-implemented-narrow-history.jpg)
