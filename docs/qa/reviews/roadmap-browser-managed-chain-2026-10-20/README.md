# Managed browser workflow qualification — 2026-10-20

The controlled fixture drives authenticated script authoring, native replay, a one-iteration managed run, finalized document reports, retained resources, native HAR entry/download and archive/replay ZIP exports through the real controller and native browser. It uses SQLite and only an owned loopback target. No protocol result or archive writer is substituted.

Four local cohorts are exercised: Chromium desktop and Pixel 8 emulation, and WebKit desktop and iPhone 15 Pro emulation. Document/report, resource and native HAR source generations must join. Retained resources and target request counters are independently checked; native HAR bytes must match the finalized digest and ZIP payload. Native runtime and emulation remain distinct from actual devices.

Persisted editor/viewer identities exercise current authorization: viewers can inspect reports/resources and HAR listings, but cannot download native HAR/ZIP or write scripts. Cross-project access is denied. Mobile synthetic gesture dispatch remains explicitly excluded from pattern scoring; desktop native dispatch qualifies.

The fixture waits for the controlled fetch response body before its visible completion signal, so resource retention is compared against completed traffic. Its HAR directory is private and explicitly configured. The exploratory shared-temp run exposed a capture-failure omission gap; fixing that product gap is a separate follow-up, not evidence that this successful path covers failures.

This closes the local authenticated author/replay/run/archive/download path only. Real TruClient export fidelity, browser-driven dashboard navigation through a real managed run, live/policy/baseline/panel workflows, durable replay HAR recovery, reference-host performance, Linux/Windows and actual-device qualification remain pending.

## Validation and review

- Full API suite: **12,563 passing tests/subtests**, 13 explicit skips; native browser execution required. The private attached HAR import retained every stable compatibility result.
- The four-cohort workflow passed **three race-detector repetitions** (15 tests/subtests).
- Documentation checks passed for **300 active Markdown files**. Embedded dashboard/Help assets remain byte-identical to `79e6f4ad2`, whose Help modules were verified in both auth profiles. No UI implementation changed in this slice.
- Self-review corrected the fixture completion and supported-report findings; the final review found no issues. Review rounds (`evidence/review-rounds.json`), validation summary (`evidence/validation-summary.json`), source hashes (`evidence/source-hashes.json`) and checksums (`evidence/SHA256SUMS`) retain the scope. Controlled native ZIP/report/resource artifacts are retained alongside the compressed logs. Private source HAR bytes are not copied into the repository.

Authored identity across controller-applied runtime overrides remains a separate open item: this fixture qualifies within-run source joins, not replay-to-run script-digest equality.
