# Browser interaction map cohorts — 2026-10-20

This bounded BI-B slice separates exact recorded scroll cohorts and excludes unknown or conflicting context evidence from maps. Raw version-1 archives remain unchanged; derived analysis reports version 2. Stable authored identity, actual dispatch-time geometry and real-device qualification remain open in the [implementation plan](../../../ROADMAP_IMPLEMENTATION_SPEC.md).

## Contract and scope

Maps separate route, layout, viewport, device pixel ratio, orientation, engine, profile and exact recorded scroll offsets on both axes. Signed zero is equivalent; fractional offsets remain distinct. An empty profile on a known engine represents the default browser. Missing layouts, untransformed child frames and absent/conflicting contexts contribute once to exclusion coverage. A conflicted context cannot become known again through a later record. The 64-map limit still admits compatible records into existing maps after refusing new cohorts.

The dashboard and HTML/PDF/CSV report views expose scroll cohorts and exclusion counts. Older exported analyses retain their ranges. CLI and MCP help describe the returned analysis version. The [in-app Help source](../../../guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#patterns-and-heatmaps) explains post-action geometry and the unchanged capture-time/action-name limitations of pattern detection.

## Validation

Main advanced twice during validation. PR #257 (`a45bd5f02`) was integrated as `8a41940`; PR #258 (`419502149`) was then integrated as `06868f09`. Source merges had no conflicts. The first restoration conflicted only on generated assets, which were rebuilt; saved source hashes verified both restorations. The final validation ran on `06868f09` plus this slice, identified by source and asset hashes (`evidence/final-source-hashes.json`).

The final full Go suite passed 168 packages, with 10 package skips/no-test packages and no failures. Its conditional test skips are retained in the validation summary (`evidence/validation-summary.json`). Chromium and WebKit browser-intelligence golden acceptance and the private HAR import passed. Dashboard build/typecheck, full lint, 225 selected unit tests, bundle budgets and 24 MCP tests passed. Embedded Help equals its source. Browser workflows passed 17 tests with one skip in dev-admin and 16 tests in local-auth, including Help navigation and the new cohort display. The dev-admin run also covered the incoming report-results workflow.

All 10 stable private HAR aggregate fields match the earlier baseline: 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings, zero DSL issues, 264 protected requirements and the source ZIP size. Random generated script identity is excluded from comparison. Capture contents and target hosts are not included in this evidence bundle. Review rounds (`evidence/review-rounds.json`) record fixes and a final review with no remaining findings. Evidence checksums (`SHA256SUMS`) make the retained results verifiable.

The first PR #257 browser attempt passed dev-admin but stopped at the fresh-main gate when PR #258 arrived; both profiles were rerun against the final bundle. Earlier results remain historical evidence and do not replace the final gates. Observed wall-clock gaps are recorded separately and these runs make no performance qualification claim.

An interruption removed the earlier temporary logs and process. Their reported results are not used as this record's final gate; fresh results are retained under evidence (`evidence/`). The first resumed targeted Go run found an incorrect expected count in the updated archive test: its three gestures all require exclusion when document evidence is unavailable. Both truncated and unreadable-document assertions now use the fixture population. The failed attempt is preserved. The corrected targeted run passed. A subsequent full Go run passed the private HAR import but the API package hit its 20-minute timeout during repeated contract generation with a 2 GiB memory limit and GOGC=25. Remaining packages from that old tree were stopped when main advanced. This invocation remains failed/interrupted; the integrated rerun uses GOMAXPROCS=4, GOGC=100 and a 6 GiB limit.

## Qualification limits

The dashboard map test uses a clearly labeled response fixture over a real archived HTTP run. It verifies rendering and drill-down behavior, not native browser capture or device qualification. No changes execute or replay the private HAR's target traffic. This slice does not close BE-A–C or BI-A–C: dispatch-time provenance, authored identity/outcomes, real supported TruClient exports, real-device/platform and density cohorts, HAR recovery/ownership, baseline/policy workflows and broader evidence-chain acceptance remain subject to their existing gates.
