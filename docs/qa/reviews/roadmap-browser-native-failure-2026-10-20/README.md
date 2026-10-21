# Native browser document failure evidence — 2026-10-20

This BE-A qualification slice exercises external native page closure in Chromium and WebKit (default viewports plus Pixel 8 and iPhone 15 Pro emulation respectively) and a genuine default-viewport Chromium renderer crash through its CDP crash command. It uses managed browser sessions and controlled local pages; no synthetic lifecycle event substitutes for the native close/crash callback.

## Scope and contracts

The fixture verifies configured mobile/touch flags and retained profile identity, independently reads the native page viewport and pixel ratio, then observes five document metrics, including a completed FCP and unfinished CLS/INP. Native failure must preserve completed records exactly, censor unresolved observations without values or eligibility, and retain source/document/lifecycle/window identity. An external page close may legitimately finalize LCP at its native hidden boundary. No other unfinished metric is promoted to final by that exception. Checks reject duplicate or additional measurement identities and require later managed teardown to leave the population unchanged. The crash-command worker is joined during cleanup even when an assertion fails.

The native page probe records `navigator.maxTouchPoints` independently. WebKit reports zero for the iPhone emulation cohort on this host despite its configured touch flag. This slice does not qualify native touch-device capability or touch dispatch.

No production capture, archive, import or analysis contract changes in this slice. The generated [browser acceptance inventory](../../BROWSER_ACCEPTANCE.md) lists each test's precise engine scope. Chromium's CDP renderer-crash result is not a WebKit renderer-crash claim.

## Integration and validation

PR #259 (`0eadba7de`) was integrated with the previously committed map-cohort work. Source merges had no conflicts; generated dashboard assets conflicted and were rebuilt from the combined sources. The first asset restore refused unmerged rename entries; rebuilding and staging the generated output resolved them. Other worktrees had no pending or branch-only changes in the relevant browser lifecycle files.

The final full Go suite passed 168 packages with no failures (10 package skips/no-test packages); conditional test skips are listed in the validation summary (`evidence/validation-summary.json`). The log contains the final native-page oracle and both mobile-profile cases. The 25-case native engine/profile matrix also passed five repetitions independently. Native page reads matched the configured viewport and pixel ratio; WebKit's observed zero touch points remains disclosed above.

Dashboard build/typecheck, full lint, 225 focused tests and bundle budgets passed. Browser workflows passed 15 dev-admin tests with one skip and 16 local-auth tests, including in-app Help navigation, reloads and the map cohort contract. Four embedded Help articles exactly match their source. The private HAR import passed with all stable fields equal to the prior slice: 803 requests, 476 actions and zero DSL issues. Only the historically randomized generated-script hash is excluded from comparison. Capture contents and target hosts are not included or replayed.

The initial native close test incorrectly required LCP censoring even when the native hidden boundary legitimately finalized it. The first inventory attempt redeclared an existing Chromium scope. The first full suite found a stale generated QA dashboard (91 versus 92 tracked Playwright specs). An added mobile oracle initially assumed positive native touch points in WebKit. Each failed attempt is retained with its correction in the review record (`evidence/review-rounds.json`); the final gates above supersede those attempts. Unavailable touch capability is now distinct from zero. The final source review found no remaining issues.

Source and asset hashes (`evidence/tested-hashes.json`), commands and integration inputs (`evidence/commands.json`), native observations (`evidence/native-summary.json`) and evidence checksums (`SHA256SUMS`) identify the tested work. Only private build caches were cleaned; shared caches were left for parallel work. Unrelated primary-checkout files retained their original hashes.

## Remaining qualification

This is local macOS evidence for the logged installed browser versions. Genuine BFCache restoration, browser-process loss, WebKit renderer crash, Linux/Windows native lanes, real device sessions, stable authored step identity, actual dispatch timing, HAR replay durability and the remaining BE/BI gates stay open. It does not certify an actual iPhone/iPad or Android session, another host or a published density target. The existing synthetic BFCache fixture remains explicitly synthetic.
