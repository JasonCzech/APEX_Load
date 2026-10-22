# Separate browser tuple panels — 2026-10-21

Archive panels may explicitly select up to 20 complete browser measurement tuples, each retaining its own final-document distribution, runtime cohort identity, missing values and counts. Query v3 and result v1 keep the legacy bucket array empty. Older queries still refuse mixed tuples. Limits fail explicitly instead of dropping a population. Exact-tuple comparisons disclose missing counterparts and refuse incomplete/excluded populations. They are workload comparisons, not baseline anomaly scoring. Multi-tuple derivations remain unavailable.

## Validation and review

- Full Go suite: **35,814 passing tests/subtests**, 38 explicit skips, zero failures, with native browser tests required. Private HAR import stable results match the prior baseline.
- Focused race checks: **111 passing tests/subtests** over three repetitions.
- The full dashboard cycle, both authentication profiles, generated contracts, exact embedded Help and documentation links passed. Actual local Chromium desktop and Pixel emulation verify authoring, replay, native HAR integrity, independent request counts, archived tuple series, saved panel reload and rendered CSV exports. Native managed browser chains retain archive/report/HAR evidence.
- Review fixed the completed-run integration gap: report Performance now exposes project panel inspection and authorized editing, with scoped requests and cancellation. The real browser workflow then passed in both profiles; the initial failed trace remains local. Final review found no remaining issues. Local evidence includes `evidence/validation-summary.json`, source/asset hashes, browser screenshots, the full dashboard-cycle log and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. This slice does not qualify actual devices or external hosts. Immutable comparison-to-panel input pins, authored page identity, compatible baseline selection and provisional live findings remain open.
