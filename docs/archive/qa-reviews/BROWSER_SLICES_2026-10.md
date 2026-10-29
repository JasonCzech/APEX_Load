# Completed browser, HAR & replay slice reviews — October 2026

Status: Historical. Consolidated on 2026-10-29 from completed scoped review records under `docs/qa/reviews/`. Original dates, validation outcomes, failed intermediate checks, source hashes and qualification limits are preserved intact below. Untracked raw evidence remains local at the cited paths. Use the [active review index](../../qa/reviews/README.md) for current capstone records.

## Records
- [Browser workflow first-failure triage (`clm-import-2026-10-01/browser-failure-triage.md`)](#clm-import-2026-10-01-browser-failure-triage)
- [Debug completion summary and HAR export (`debug-completion-summary-2026-10-25.md`)](#debug-completion-summary-2026-10-25)
- [Debug HAR capture options (`debug-har-capture-options-2026-10-26.md`)](#debug-har-capture-options-2026-10-26)
- [Debug summary elapsed-time reconciliation — 2026-10-26 (`debug-summary-duration-reconciliation-2026-10-26.md`)](#debug-summary-duration-reconciliation-2026-10-26)
- [Debug summary and HAR publication review — 2026-10-26 (`debug-summary-har-final-review-2026-10-26.md`)](#debug-summary-har-final-review-2026-10-26)
- [Debug summary transaction breakdown — 2026-10-26 (`debug-summary-transactions-2026-10-26.md`)](#debug-summary-transactions-2026-10-26)
- [Browser document frontend qualification (`feature-completion-browser-document-2026-10-05/FRONTEND_VERIFICATION.md`)](#feature-completion-browser-document-2026-10-05-frontend-verification)
- [Browser document finality qualification — 2026-10-05 (`feature-completion-browser-document-finality-2026-10-05/README.md`)](#feature-completion-browser-document-finality-2026-10-05)
- [Browser document finalization isolation (`feature-completion-browser-frame-isolation-2026-10-05/README.md`)](#feature-completion-browser-frame-isolation-2026-10-05)
- [Retained resource-reader backend qualification (`feature-completion-browser-resources-2026-10-05/BACKEND_QUALIFICATION.md`)](#feature-completion-browser-resources-2026-10-05-backend-qualification)
- [Retained browser resource inspection — scoped qualification (`feature-completion-browser-resources-2026-10-05/README.md`)](#feature-completion-browser-resources-2026-10-05)
- [Native HAR ownership candidate review (`feature-completion-native-har-ownership-2026-10-05/README-v5-static-history.md`)](#feature-completion-native-har-ownership-2026-10-05-readme-v5-static-history)
- [Native HAR ownership qualification (`feature-completion-native-har-ownership-2026-10-05/README.md`)](#feature-completion-native-har-ownership-2026-10-05)
- [Second HAR: offline evaluation (`feature-completion-second-har-evaluation-2026-10-05/README.md`)](#feature-completion-second-har-evaluation-2026-10-05)
- [Browser authored identity foundation — 2026-10-20 (`roadmap-browser-authored-identity-2026-10-20/README.md`)](#roadmap-browser-authored-identity-2026-10-20)
- [Authored navigation page receipts — 2026-10-21 (`roadmap-browser-authored-pages-2026-10-21/README.md`)](#roadmap-browser-authored-pages-2026-10-21)
- [Browser baseline cadence qualification — 2026-10-21 (`roadmap-browser-baseline-cadence-2026-10-21/README.md`)](#roadmap-browser-baseline-cadence-2026-10-21)
- [Roadmap implementation: browser baseline qualification guards (`roadmap-browser-baseline-qualification-2026-10-19/README.md`)](#roadmap-browser-baseline-qualification-2026-10-19)
- [Browser benchmark memory units — 2026-10-20 (`roadmap-browser-benchmark-units-2026-10-20/README.md`)](#roadmap-browser-benchmark-units-2026-10-20)
- [Browser baseline bucket timestamps — 2026-10-21 (`roadmap-browser-bucket-grid-2026-10-21/README.md`)](#roadmap-browser-bucket-grid-2026-10-21)
- [HAR publication after a concurrent CDP detach — 2026-10-21 (`roadmap-browser-cdp-close-2026-10-21/README.md`)](#roadmap-browser-cdp-close-2026-10-21)
- [Browser stack cross-commit review — 2026-10-21 (`roadmap-browser-cross-commit-2026-10-21/README.md`)](#roadmap-browser-cross-commit-2026-10-21)
- [Native browser dispatch and qualified interaction analysis — 2026-10-20 (`roadmap-browser-dispatch-v2-2026-10-20/README.md`)](#roadmap-browser-dispatch-v2-2026-10-20)
- [Browser distributed RPC causal brackets and multi-generator steady-load reconciliation (BI-A1) (`roadmap-browser-distributed-clocks-2026-10-26/README.md`)](#roadmap-browser-distributed-clocks-2026-10-26)
- [Durable replay namespace in non-HA controllers — 2026-10-21 (`roadmap-browser-durable-namespace-2026-10-21/README.md`)](#roadmap-browser-durable-namespace-2026-10-21)
- [Evaluated browser baseline findings and RCA completion hook (BI-A3) (`roadmap-browser-findings-v2-2026-10-27/README.md`)](#roadmap-browser-findings-v2-2026-10-27)
- [Roadmap Review — Full Real Browser Intelligence Chain in Both Auth Profiles (`BI-C5`) (`roadmap-browser-full-chain-e2e-2026-10-27/README.md`)](#roadmap-browser-full-chain-e2e-2026-10-27)
- [Browser Ground-Truth Interaction Fixture (50+ Groups), Trusted Mobile Taps & SW Cache Provenance (`BE-A3`) (`roadmap-browser-ground-truth-fixture-2026-10-26/README.md`)](#roadmap-browser-ground-truth-fixture-2026-10-26)
- [Roadmap BE-B4 — HA Source-Cache HAR and Interaction Retention (2026-10-26) (`roadmap-browser-ha-source-cache-2026-10-26/README.md`)](#roadmap-browser-ha-source-cache-2026-10-26)
- [Native HAR capture body bounds and close/cancel race qualification — 2026-10-26 (`roadmap-browser-har-capture-races-2026-10-26/README.md`)](#roadmap-browser-har-capture-races-2026-10-26)
- [Native HAR capture omission retention — 2026-10-20 (`roadmap-browser-har-omissions-2026-10-20/README.md`)](#roadmap-browser-har-omissions-2026-10-20)
- [QA Review — Interaction Analysis Bounds, Keyboard Target Counts & Raw Interaction CSV Export (`BI-B4`) (`roadmap-browser-interaction-bounds-2026-10-27/README.md`)](#roadmap-browser-interaction-bounds-2026-10-27)
- [Browser Interaction Patterns v4 and UI/Report v4 Qualification Review (`BI-B2`) (`roadmap-browser-interaction-patterns-v4-2026-10-27/README.md`)](#roadmap-browser-interaction-patterns-v4-2026-10-27)
- [QA Review Record — `BE-A1 + BE-A2 + BI-B1` (`FeatureBrowserInteractionEvidenceV4` & Authored Page Receipt v3) (`roadmap-browser-interaction-v4-2026-10-26/README.md`)](#roadmap-browser-interaction-v4-2026-10-26)
- [BI-B3 — Structural Intra-Iteration Journey Attempts (`StepPath` Binding) (`roadmap-browser-journey-steps-2026-10-26/README.md`)](#roadmap-browser-journey-steps-2026-10-26)
- [Roadmap BE-B1 — Linux Native HAR Crash Qualification & PID-1 Zombie Fix (2026-10-26) (`roadmap-browser-linux-har-crash-2026-10-26/README.md`)](#roadmap-browser-linux-har-crash-2026-10-26)
- [QA Review Record — Browser Linux Memory Admission (`BE-A5`) (`roadmap-browser-linux-memory-admission-2026-10-26/README.md`)](#roadmap-browser-linux-memory-admission-2026-10-26)
- [QA Review Record — `BI-C1`: Provisional Live Browser Findings, Interactions, and Journeys (`roadmap-browser-live-provisional-2026-10-27/README.md`)](#roadmap-browser-live-provisional-2026-10-27)
- [Managed browser workflow qualification — 2026-10-20 (`roadmap-browser-managed-chain-2026-10-20/README.md`)](#roadmap-browser-managed-chain-2026-10-20)
- [Browser interaction map cohorts — 2026-10-20 (`roadmap-browser-map-cohorts-2026-10-20/README.md`)](#roadmap-browser-map-cohorts-2026-10-20)
- [Configured browser metric floors — 2026-10-21 (`roadmap-browser-metric-floors-2026-10-21/README.md`)](#roadmap-browser-metric-floors-2026-10-21)
- [Native HAR transfer capability negotiation — 2026-10-26 (`roadmap-browser-native-har-feature-2026-10-26/README.md`)](#roadmap-browser-native-har-feature-2026-10-26)
- [Native browser lifecycle fixtures — 2026-10-20 (`roadmap-browser-native-lifecycle-2026-10-20/README.md`)](#roadmap-browser-native-lifecycle-2026-10-20)
- [Rendered native waterfall/cache qualification — 2026-10-21 (`roadmap-browser-native-waterfall-2026-10-21/README.md`)](#roadmap-browser-native-waterfall-2026-10-21)
- [Browser baseline original-source qualification — 2026-10-21 (`roadmap-browser-original-baseline-2026-10-21/README.md`)](#roadmap-browser-original-baseline-2026-10-21)
- [Original authored-source binding — 2026-10-21 (`roadmap-browser-original-source-2026-10-21/README.md`)](#roadmap-browser-original-source-2026-10-21)
- [Observed post-action Sync clocks — 2026-10-21 (`roadmap-browser-outcome-clocks-2026-10-21/README.md`)](#roadmap-browser-outcome-clocks-2026-10-21)
- [Frozen browser comparison panels — 2026-10-21 (`roadmap-browser-panel-pins-2026-10-21/README.md`)](#roadmap-browser-panel-pins-2026-10-21)
- [QA Review — Saved Panel Policy and Baseline Selection Pins v2 (`BI-C3`) (2026-10-27) (`roadmap-browser-panel-pins-v2-2026-10-27/README.md`)](#roadmap-browser-panel-pins-v2-2026-10-27)
- [Browser pinned baseline admission — 2026-10-21 (`roadmap-browser-pinned-admission-2026-10-21/README.md`)](#roadmap-browser-pinned-admission-2026-10-21)
- [Immutable browser/journey policy selection — 2026-10-21 (`roadmap-browser-policy-selection-2026-10-21/README.md`)](#roadmap-browser-policy-selection-2026-10-21)
- [Chromium browser-process loss and pool replacement — 2026-10-20 (`roadmap-browser-process-loss-2026-10-20/README.md`)](#roadmap-browser-process-loss-2026-10-20)
- [Atomic browser evidence promotion — 2026-10-20 (`roadmap-browser-promotion-2026-10-20/README.md`)](#roadmap-browser-promotion-2026-10-20)
- [Browser durable receipt and qualification work (`roadmap-browser-proof-2026-10-22/README.md`)](#roadmap-browser-proof-2026-10-22)
- [QA Review Record — `BI-C4`: Provenance in Reports, RCA Selection UI & CLI (`apexctl schedule`, `ci run`, `run --api`) (`roadmap-browser-provenance-cli-2026-10-27/README.md`)](#roadmap-browser-provenance-cli-2026-10-27)
- [Browser baseline inventory and qualification — 2026-10-21 (`roadmap-browser-qualification-2026-10-21/README.md`)](#roadmap-browser-qualification-2026-10-21)
- [Native document receipt-clock acquisition (`roadmap-browser-receipt-clocks-2026-10-22/README.md`)](#roadmap-browser-receipt-clocks-2026-10-22)
- [QA Review Record — Replacement-Container Native HAR Reclamation (`BE-B2`) (`roadmap-browser-replacement-container-reclaim-2026-10-26/README.md`)](#roadmap-browser-replacement-container-reclaim-2026-10-26)
- [Immutable replay artifact backend — 2026-10-21 (`roadmap-browser-replay-backend-2026-10-21/README.md`)](#roadmap-browser-replay-backend-2026-10-21)
- [Controller replay recovery activation — 2026-10-21 (`roadmap-browser-replay-http-2026-10-21/README.md`)](#roadmap-browser-replay-http-2026-10-21)
- [Replay capture omission evidence — 2026-10-21 (`roadmap-browser-replay-omissions-2026-10-21/README.md`)](#roadmap-browser-replay-omissions-2026-10-21)
- [Terminal replay publication port — 2026-10-21 (`roadmap-browser-replay-publication-port-2026-10-21/README.md`)](#roadmap-browser-replay-publication-port-2026-10-21)
- [Lease-bound replay artifact publisher — 2026-10-21 (`roadmap-browser-replay-publisher-2026-10-21/README.md`)](#roadmap-browser-replay-publisher-2026-10-21)
- [Retained replay read contract — 2026-10-21 (`roadmap-browser-replay-read-contract-2026-10-21/README.md`)](#roadmap-browser-replay-read-contract-2026-10-21)
- [Replay artifact receipt storage — 2026-10-21 (`roadmap-browser-replay-receipts-2026-10-21/README.md`)](#roadmap-browser-replay-receipts-2026-10-21)
- [Replay recovery scope and cleanup pagination — 2026-10-21 (`roadmap-browser-replay-recovery-storage-2026-10-21/README.md`)](#roadmap-browser-replay-recovery-storage-2026-10-21)
- [Bounded replay recovery runtime — 2026-10-21 (`roadmap-browser-replay-runtime-2026-10-21/README.md`)](#roadmap-browser-replay-runtime-2026-10-21)
- [Filesystem replay source identity — 2026-10-21 (`roadmap-browser-replay-volume-identity-2026-10-21/README.md`)](#roadmap-browser-replay-volume-identity-2026-10-21)
- [Native request timing snapshots — 2026-10-21 (`roadmap-browser-request-timing-2026-10-21/README.md`)](#roadmap-browser-request-timing-2026-10-21)
- [Roadmap Review — Recurring Schedule Browser & Journey Policy Selection (`BI-C2`, 2026-10-26) (`roadmap-browser-schedule-policies-2026-10-26/README.md`)](#roadmap-browser-schedule-policies-2026-10-26)
- [QA Review Record — Browser Seeded-Regression Exit Through the Production Path (`BI-A4`, 2026-10-27) (`roadmap-browser-seeded-regression-2026-10-27/README.md`)](#roadmap-browser-seeded-regression-2026-10-27)
- [Frozen browser baseline selection — 2026-10-21 (`roadmap-browser-selection-2026-10-21/README.md`)](#roadmap-browser-selection-2026-10-21)
- [Browser baseline full measurement windows — 2026-10-21 (`roadmap-browser-source-windows-2026-10-21/README.md`)](#roadmap-browser-source-windows-2026-10-21)
- [Browser timeline and onset qualification (BI-A2) (`roadmap-browser-timeline-qualification-2026-10-26/README.md`)](#roadmap-browser-timeline-qualification-2026-10-26)
- [Separate browser tuple panels — 2026-10-21 (`roadmap-browser-tuple-panels-2026-10-21/README.md`)](#roadmap-browser-tuple-panels-2026-10-21)
- [Roadmap Review — `BE-B6`: UI-Driven Managed Chain Across Chromium and WebKit Cohorts (2026-10-27) (`roadmap-browser-ui-managed-chain-2026-10-27/README.md`)](#roadmap-browser-ui-managed-chain-2026-10-27)
- [Roadmap QA Review — Per-VUser Browser RSS Attribution and v3 Benchmark Contract (`BE-C1`) (`roadmap-browser-vuser-attribution-2026-10-26/README.md`)](#roadmap-browser-vuser-attribution-2026-10-26)
- [Roadmap implementation: shared HAR finalization deadline (`roadmap-har-finalization-budget-2026-10-19/README.md`)](#roadmap-har-finalization-budget-2026-10-19)
- [Native HAR outstanding-request limit — 2026-10-20 (`roadmap-har-inflight-2026-10-20/README.md`)](#roadmap-har-inflight-2026-10-20)
- [Roadmap implementation: monotonic browser timing (`roadmap-monotonic-browser-timing-2026-10-19/README.md`)](#roadmap-monotonic-browser-timing-2026-10-19)
- [QA Review — Replay HAR Crash, Shared-Filesystem, and Cloud-Emulator Qualification (`BE-B5` Local) (`roadmap-replay-har-qualification-2026-10-26/README.md`)](#roadmap-replay-har-qualification-2026-10-26)
- [Roadmap Review — Local TruClient Replay Primitives (`BE-B7a`, 2026-10-26) (`roadmap-truclient-replay-primitives-2026-10-26/README.md`)](#roadmap-truclient-replay-primitives-2026-10-26)
- [Native HAR capture on Windows (`windows-native-har-2026-10-16/README.md`)](#windows-native-har-2026-10-16)

<!-- Original record: docs/qa/reviews/clm-import-2026-10-01/browser-failure-triage.md; local evidence kept outside git -->
<a id="clm-import-2026-10-01-browser-failure-triage"></a>

## Browser workflow first-failure triage


These findings describe the first recorded failures from the isolated local review runs. Trace and screenshot artifacts remain in the private temporary review directory; no uploaded source or credentials are included here.

| Profile and workflow | First failure | Evidence and disposition |
| --- | --- | --- |
| Dev admin, nested archive collection | Strict locator error at the Java member selection | The unanchored member-name expression matched both the row selection button and `Remove JavaDemo.USZ`. Anchored member selectors now distinguish those buttons. This was a test locator defect. |
| Local authentication, nested archive collection | Entire test exceeded the default 30-second budget | The trace records every workflow assertion passing, including independent Java review, the web runtime tab, one saved script in the atomic save receipt, retained failed member, and Source Mapping after save. The final assertion completed about 47 seconds after setup began. The failure snapshot shows the web member saved and the other two reviews retained. No setup or save behavior defect was demonstrated. The collection spec now uses Playwright's `test.slow()` workflow budget; assertion and action timeouts remain unchanged. |
| Dev admin, existing JMeter source review | Entire test exceeded the default 30-second budget | After Hooks began 30.1 seconds after Before Hooks. All assertions and the final narrow screenshot subsequently completed in the trace, without an assertion error. The final screenshot completed about 38.6 seconds after setup began. |
| Local authentication, existing JMeter source review | Entire test exceeded the default 30-second budget | After Hooks began 30.2 seconds after Before Hooks. All assertions and the final narrow screenshot subsequently completed, without an assertion error. The final screenshot completed about 42.5 seconds after setup began. Authentication deliberately includes a 6.5-second pacing delay. |

The JMeter application code and assertions were left unchanged. The traces establish exhaustion of the whole-test budget; they do not establish which shared host activity caused the slower run. Avoid reporting these timeout results as a source-import or layout regression.

The dev-admin follow-up subsequently passed the anchored collection workflow in 25.5 seconds. Its only recorded failure was the existing JMeter whole-test timeout. Later focused runs using a one-off `--timeout=90000` passed: local-auth collection, JMeter source review, and transaction labels all passed in 43.8 seconds (individual tests 16.1, 13.1, and 13.1 seconds); dev-admin JMeter passed in 12.2 seconds (test 9.5 seconds). The original whole-test timeout traces remain intact.

For follow-up, run only the collection and JMeter source-review workflows, one authentication profile at a time, after the other browser/build jobs finish. Retain the original failed traces. Use the new collection workflow budget; for the unchanged JMeter spec, a one-off `--timeout=90000` run can separate a whole-test budget problem from a reproducible assertion or request failure. Such a diagnostic run does not replace the original failure record or silently change the suite's timeout policy.

<a id="clm-import-2026-10-01-browser-failure-triage--actual-source-review-stalled-while-reading-the-response"></a>
### Actual source review stalled while reading the response

The actual browser import also exposed a separate request-lifecycle problem: the controller reached its write deadline after returning HTTP 200 headers, and the renderer remained busy while waiting for the JSON body. This is distinct from Playwright's whole-test budget. The enterprise import and validation API wrappers now use the existing full-body JSON request helper with a 210-second deadline. It aborts pending reads, also rejects transports that ignore abort, clears its timer after completion or error, and tells the operator that the archive remains in the queue for another validation attempt. Other importer requests retain their existing behavior.

All 39 API-wrapper tests passed, including both enterprise endpoints with stalled headers, stalled bodies after HTTP 200, successful JSON completion, and HTTP 422 errors. The timeout tests check cancellation and cleared timers. Actual browser revalidation is recorded by the combined review; this unit evidence does not prove target replay.

<a id="clm-import-2026-10-01-browser-failure-triage--final-imported-project-selector-verification"></a>
### Final imported-project selector verification

The first final dev-admin collection check exposed a separate selector visibility defect: `Broken.USZ` was the first, automatically selected member, and the project picker was rendered only inside the successful-review panel. All three queue rows and their independent statuses were present, but `Review imported project` was unavailable from the failed member. The fix retains the picker beside successful review tabs and adds it to the failed/review-only fallback. A unit assertion now selects a failed member and uses the picker to return to a valid review. The original failing trace and screenshot were retained.

After that fix, the same final collection spec passed under both disposable profiles. Dev-admin passed in 26.3 seconds (test 16.8 seconds); local-auth passed in 29.3 seconds (test 21.8 seconds). Both used `--timeout=90000 --workers=1`. The workflow verifies the project selector, independent Java approval status, web runtime settings, one eligible script in the save receipt, preservation of the failed member after save, and Source Mapping navigation afterward.

Commands were run from `dashboard/`, using a separate private output root:

```sh
APEXLOAD_E2E_BASE_URL=http://127.0.0.1:4001 APEXLOAD_E2E_OUTPUT_ROOT=/private/tmp/apex-clm-review/final-collection-verification-fixed npx playwright test e2e/workflow-loadrunner-collection.spec.ts --timeout=90000 --workers=1 --reporter=list
APEXLOAD_E2E_BASE_URL=http://127.0.0.1:4002 APEXLOAD_E2E_PROFILE=local-auth APEXLOAD_E2E_OUTPUT_ROOT=/private/tmp/apex-clm-review/final-collection-verification-fixed npx playwright test e2e/workflow-loadrunner-collection.spec.ts --timeout=90000 --workers=1 --reporter=list
```

The sandbox initially denied Chromium's macOS MachPort registration before any application assertions; the approved unsandboxed browser runs supplied the results above. This launch restriction is separate from the selector defect. Dashboard type checking and scoped import/catalog/hooks lint passed, including a repeat lint check of `EnterpriseArchiveImport.tsx` after the final picker and Java naming edits. No dashboard build changed embedded assets during these checks, and the operator's CUA tab was left untouched.

<a id="clm-import-2026-10-01-browser-failure-triage--saved-review-session-completion-counts"></a>
### Saved review-session completion counts

The actual Java save confirmed a named authoring session, but the import counter still treated it as unsaved. The counter now records successful, current-generation review-session saves separately from library scripts and excludes them from drafts awaiting save. Revalidation invalidates that current-review receipt; duplicate idempotent saves count once, and a stale completion cannot update the active import.

The collection workflow now also saves its synthetic Java review session, checks the separate script/session counts and zero remaining drafts, and confirms the execution gate remains visible. Both profiles passed without retries: dev-admin 8.1 seconds (test 7.0 seconds) and local-auth 38.3 seconds (test 32.3 seconds). The same commands above were used with output root `/private/tmp/apex-clm-review/review-receipts-browser`. Focused component checks cover successful duplicate saves, revalidation, and stale completion.

<!-- Original record: docs/qa/reviews/debug-completion-summary-2026-10-25.md; local evidence kept outside git -->
<a id="debug-completion-summary-2026-10-25"></a>

## Debug completion summary and HAR export


Date: 2026-10-25

<a id="debug-completion-summary-2026-10-25--result"></a>
### Result

The main debug workspace opens Summary once per finalized session. It retains the
completed run's identity, source, timing, action outcomes, errors, and warning
contexts. Issue links restore the action evidence view. Current-draft changes are
identified without replacing the completed snapshot.

HTTP and browser debug sessions export redacted synthetic HAR 1.2 from retained
observations. Native recordings continue to be retired. Network validation
failures are not synthesized from authored action targets. Browser request
identities deduplicate overlapping observations, retaining the last observation;
separate repeated requests remain separate. HTTP capture follows version pools,
meta-refresh, and cache revalidation. Capture limits and missing detail are
explained in the summary.

HAR export preserves existing permission checks and audit records. Missing,
expired, and inaccessible HAR sessions return 404; active or unavailable exports
return 409. JSON/log authorization responses remain unchanged.

<a id="debug-completion-summary-2026-10-25--review"></a>
### Review

Two review passes covered completion ordering, snapshot retention and disclosure,
request provenance, deduplication, HTTP clone/config propagation, export
permissions, error handling, keyboard focus, and responsive layout. Findings
around the cleared active-session ID, HTTP policy clones, action-specific warning
deduplication, and HAR-specific authorization responses were fixed and tested.
No remaining findings were identified in the feature changes.

<a id="debug-completion-summary-2026-10-25--validation"></a>
### Validation

- Final dashboard build includes TypeScript checking and embedded asset generation.
- ESLint passes.
- Editor/summary/API-contract checks: 389 passed before the additional warning-context regression; that regression's summary file subsequently passed all six tests.
- Broad dashboard suite: 8,696 passed, two source-scanning tests exceeded their
  five-second timeout under concurrent load. Both files passed all eight tests
  when rerun with a 20-second limit. No repository timeout configuration changed.
- All six affected debugger Playwright workflows passed in dev-admin and again
  in local-auth. Coverage includes HTTP and real browser HAR downloads, failure
  links, reruns, stopped runs without traffic, focus, and narrow viewports.
- Affected HTTP, engine, and recording bridge package tests passed. Final HAR,
  route authorization, and generated API checks passed after the 404 correction.
- Documentation validation passes.
- The full `go test -timeout=20m ./...` run finished with failures in six
  replay-artifact recovery/cleanup tests and the earlier HAR authorization
  fixture. The HAR failure was corrected and its final targeted checks pass.
  The broad Go merge gate is therefore not green; do not interpret the focused
  feature checks as a passing full API suite.

The six remaining failures reproduced in isolation on the feature sources and
on unchanged `main` at the same base commit. They predate this feature. The
baseline comparison used exact Go source backups and restored the feature sources
automatically afterward.

The six remaining broad-run failures were:

- `TestReplayArtifactHTTPCleanupLifecycle`
- `TestReplayArtifactHTTPProjectDeletionAndNamespaceCleanup`
- `TestReplayArtifactRuntimeRecoversNativePair`
- `TestReplayArtifactRuntimeCleanupRequiresSourceAndJoin`
- `TestReplayArtifactRuntimeCleanupTraversesForeignPage`
- `TestReplayArtifactRuntimeCleanupThroughStoreOverlay`

<a id="debug-completion-summary-2026-10-25--local-evidence"></a>
### Local evidence

Raw artifacts are deliberately untracked:

- Full Go run: `/private/tmp/apex-debug-go-full.log`
- Isolated artifact rerun: `/private/tmp/apex-debug-artifact-rerun.log`
- Unchanged-main comparison: `/private/tmp/apex-debug-artifact-baseline.log`
- Final HAR/API checks: `/private/tmp/apex-debug-go-final-api.log`
- Dashboard unit run: `/private/tmp/apex-debug-dashboard-tests.log`
- Source-scanner rerun: `/private/tmp/apex-debug-ratchets.log`
- Final summary tests: `/private/tmp/apex-debug-summary-final.log`
- Final build: `/private/tmp/apex-debug-dashboard-final-build.log`
- Browser profiles: `/private/tmp/apex-debug-e2e-dev.log`, `/private/tmp/apex-debug-e2e-auth.log`
- Screenshots, traces, and controller logs: `/private/tmp/apex-debug-e2e/`

The original checkout's existing generated dashboard changes were preserved.
Implementation and generated dashboard outputs are copied to the managed
worktree attached to this chat, with source and output hashes verified.

<a id="debug-completion-summary-2026-10-25--cleanup-and-handoff"></a>
### Cleanup and handoff

- Cleared the isolated Go build/test cache (approximately 8 GB), frontend Vite
  caches, and the temporary npm download cache (approximately 44 MB).
- Ran `docker builder prune --all --force`; Docker reported 0 B reclaimed.
- Browser workflow runners closed their browsers and disposable controllers;
  a process check found no remaining session browser/controller processes.
- Preserved dependencies, generated dashboard outputs, and the raw QA evidence.
- Handed off the implementation in the managed worktree attached to this chat;
  the disposable implementation checkout is removed after hash verification.

<!-- Original record: docs/qa/reviews/debug-har-capture-options-2026-10-26.md; local evidence kept outside git -->
<a id="debug-har-capture-options-2026-10-26"></a>

## Debug HAR capture options


Date: 2026-10-26
Base: `819563141`

<a id="debug-har-capture-options-2026-10-26--result"></a>
### Result

Debug options now offers request-only, response-only, both, or no body retention.
Bodies are off by default. Per-body choices are 16, 64, 256, and 1,024 KiB;
run-wide choices are 1, 4, and 16 MiB, with defaults of 64 KiB and 4 MiB.
The dashboard enables intermediate redirects and measured timing phases by
default. Controls apply to the next debug run, survive named-profile saves,
and are recorded in the frozen completion summary and HAR metadata.

Capture observes dispatched HTTP exchanges, including resource requests, digest
retries, redirect hops, meta-refresh requests, and wire cache revalidation.
It preserves existing execution, redirect, resource-discovery, and cache policy.
HTTP version clones share the same body allowance. The final HTTP response uses
execution's existing bounded decoder for compressed content. Browser bodies
require complete, known-size, unencoded textual responses. Missing, binary,
invalid-UTF-8, oversized, or budget-exhausted bodies are omitted whole; exported
entries explain unavailable bodies. Empty retained bodies remain empty.

Credential, runtime-secret, and custom redaction precede publication and the
HAR writer applies the existing HAR scrubber again. Body-specific redaction also
handles quoted credential assignments in inline JavaScript, including textual
base64 HAR bodies. Stable-label redaction behavior is unchanged.

Measured body sizes remain distinct from redacted text length. HTTP timing uses
per-exchange traces; HAR connection time includes TLS. Browser timing comes from
its measured evidence, never from the worker's body-collection time. Optional
missing timing phases remain unavailable. Native HAR file disposal, session
retention, authorization, disclosure restrictions, and existing JSON/log exports
retain their existing behavior.

<a id="debug-har-capture-options-2026-10-26--review"></a>
### Review

Review covered capability propagation, frozen options, observer transparency,
resource counters and deduplication, body budgets, cloning, publication redaction,
HTTP and browser timing provenance, and optional-field compatibility. Findings
around redirected browser POST headers, resource coverage, unknown body sizes,
inline credential assignments, TLS phase semantics, and measured byte counts
were fixed and regression-tested. Final source review found no remaining issues.

<a id="debug-har-capture-options-2026-10-26--validation"></a>
### Validation

- Dashboard build, final typecheck, and lint pass.
- Full dashboard unit suite: all 8,700 tests across 749 files pass. The run uses
  a 20-second test timeout to accommodate source-scanning checks under load;
  repository timeout configuration is unchanged.
- All eight affected Playwright workflows pass in dev-admin and again in
  local-auth (16 passes). They inspect downloaded HAR bodies, redaction,
  redirect hops and timings; rerun with all options off; verify frozen summary
  options, focus and keyboard checkbox interaction; and retain existing summary,
  failure, stopped-run and responsive coverage.
- Native select key changes were unavailable in a plain HTML reproduction in
  this headless macOS Chromium runtime. Browser verification uses native form
  selection for dropdown values and exercises tab focus and Space toggles.
- All affected capture/replay/redaction package tests pass. Final targeted race
  checks also pass, including body sizes, TLS timing, resources, and inline
  credential redaction.
- Final HAR API, authorization, OpenAPI freshness, and generated contract checks
  pass (`pkg/api`, 157.331 seconds under concurrent validation).
- The final repository-wide Go run (`go test -p 4 -timeout=20m ./...`) completed
  with all non-API packages passing. The API package repeated the six previously
  documented replay-artifact failures and exhausted its 20-minute aggregate
  budget (1,203.165 seconds). It had just entered
  `TestScenarioRuntimeSettingsReportShowsScheduleOverride` at the cutoff. The
  full Go gate is not green and the timeout means later API tests did not all
  complete in that invocation. Feature-specific API checks pass separately.
  The cutoff test passes in isolation in 1.662 seconds, confirming that its
  appearance in the timeout does not establish an individual test failure.

The repeated baseline failures are `TestReplayArtifactHTTPCleanupLifecycle`,
`TestReplayArtifactHTTPProjectDeletionAndNamespaceCleanup`,
`TestReplayArtifactRuntimeRecoversNativePair`,
`TestReplayArtifactRuntimeCleanupRequiresSourceAndJoin`,
`TestReplayArtifactRuntimeCleanupTraversesForeignPage`, and
`TestReplayArtifactRuntimeCleanupThroughStoreOverlay`. Their unchanged-main
reproduction is recorded in the preceding debug-completion QA record.

Raw evidence is local and deliberately untracked:

- Focused capture checks: `/private/tmp/apex-har-options-focused.log`
- Affected package checks: `/private/tmp/apex-har-options-packages-final.log`
- Redaction and resource checks: `/private/tmp/apex-har-options-redaction.log`, `/private/tmp/apex-har-options-resources.log`
- Final review regressions: `/private/tmp/apex-har-options-final-review.log`
- Race checks: `/private/tmp/apex-har-options-race-complete.log`
- HAR API checks: `/private/tmp/apex-har-options-api-complete.log`
- API generation: `/private/tmp/apex-har-options-generate-final.log`
- Dashboard build: `/private/tmp/apex-har-options-build.log`
- Typecheck/lint: `/private/tmp/apex-har-options-typecheck-final.log`, `/private/tmp/apex-har-options-lint-complete.log`
- Dashboard unit suite: `/private/tmp/apex-har-options-dashboard-final.log`
- Browser profiles: `/private/tmp/apex-har-options-e2e-dev-final.log`, `/private/tmp/apex-har-options-e2e-auth-final.log`
- Browser artifacts and controller logs: `/private/tmp/apex-har-options-e2e/`
- Cutoff-test rerun: `/private/tmp/apex-har-options-api-cutoff.log`
- Full Go regression run: `/private/tmp/apex-har-options-go-full-final.log`

The earlier broad Go attempt was superseded after source changes and terminated;
only the final run is evidence for the complete implementation.

<a id="debug-har-capture-options-2026-10-26--cleanup"></a>
### Cleanup

- Cleared the isolated Go build/test cache (approximately 11 GiB, reduced to
  8 KiB) and the superseded build scratch directory (approximately 1.1 GiB).
- Removed frontend Vite caches after all frontend users finished; removed the
  temporary dependency symlink while preserving the shared dependency directory.
- Ran `docker builder prune --all --force`; Docker reported 0 B reclaimed.
- Both browser runners closed their browsers and disposable controllers. The
  cutoff run left no tracked native subprocesses; neither browser test port
  (18121 or 18122) has a remaining listener. The standalone native-select
  reproduction also closed its browser in a finally block.
- Preserved source changes, generated dashboard outputs, dependencies, and raw
  validation evidence in the attached managed worktree and local evidence paths.
  The original shared checkout was not modified by this feature work.

The subsequent review/fix cycle and publication are recorded in
[the final publication review](#debug-summary-har-final-review-2026-10-26).

<!-- Original record: docs/qa/reviews/debug-summary-duration-reconciliation-2026-10-26.md; local evidence kept outside git -->
<a id="debug-summary-duration-reconciliation-2026-10-26"></a>

## Debug summary elapsed-time reconciliation — 2026-10-26


Integrated `origin/main` through `24d65c718` before builds. Work stays in the
attached isolated checkout, preserving the shared checkout's source changes.

<a id="debug-summary-duration-reconciliation-2026-10-26--diagnosis-and-correction"></a>
### Diagnosis and correction

A parent elapsed span is not generally the sum of its children. Time between
children, debugger pauses, think time, and parent work add elapsed time. Overlapping
children share time and must be counted once. Repeated execution summaries include
elapsed gaps between executions. This expected behavior is now explained on the
summary and in the debugger guide.

The investigation also found and fixed three concrete problems:

- Group timestamps were subtracted through JavaScript's millisecond `Date.parse`,
  discarding the sub-millisecond precision retained by Go's RFC3339Nano timestamps.
  Boundary selection and subtraction now preserve nanoseconds using integer
  arithmetic before converting the duration to milliseconds.
- Group rows displayed elapsed spans while leaf rows displayed recorded action
  measurements, which can exclude work within the retained start/end span. The
  main column now consistently reads **Elapsed time (ms)**. Recorded action time
  remains visible separately under each step; raw timestamp precision is retained
  in the time elements and hover titles.
- Subtransaction grouping used opening order rather than the explicit parent
  argument. Overlapping sibling subtransactions now remain siblings under their
  declared parent, including either valid closing order.

Expanded parents now show child elapsed-span total, duplicate counts from overlap,
time outside the immediate child spans, and parent elapsed time. An interval union
avoids double counting, including triple overlap. Grandchildren are not added again.
Missing/withheld/inconsistent timing suppresses the reconciliation; unexecuted
branches are excluded. Display rounding is explicit and the equation uses an
approximation sign. The engine's transaction response-time metric is distinct from
this debug wall-clock display.

<a id="debug-summary-duration-reconciliation-2026-10-26--review-and-validation"></a>
### Review and validation

Review covered native transaction boundary semantics, RFC3339 precision and offsets,
interval accounting, static parent ownership versus execution order, incomplete
and unexecuted evidence, frozen snapshots, navigation, keyboard behavior, and
responsive rendering. Visual review found flat-table auto-pagination separating
expanded groups; the hierarchy now opts out so parents, explanations, and children
stay together. The repeat review found no further actionable findings.

- Six focused Vitest files: 350 tests passed, including precision, timezone
  boundaries, action/elapsed separation, sequential gaps, overlapping siblings,
  triple overlap, nested descendants, repeated executions, unexecuted branches,
  missing evidence, DOM labels, and existing editor behavior.
- Dashboard TypeScript checks, lint, production build, and documentation checks.
- All nine debugger Playwright workflows passed in each authentication profile.
  The transaction workflow now observes a real 30 ms overlapping sibling and a
  120 ms post-subtransaction think-time gap. It checks the reconciliation, declared
  sibling hierarchy, action timing labels, evidence navigation, keyboard scrolling,
  and stopped reruns. The final workflow is repeated in both profiles after the
  pagination fix and also verifies that all parent groups remain visible.
- Desktop and narrow screenshots were visually inspected. No Go/API contract
  source changed; the disposable controllers were built against the final assets.

Raw evidence: `/private/tmp/apex-debug-duration-unit.log`,
`/private/tmp/apex-debug-duration-typecheck.log`,
`/private/tmp/apex-debug-duration-lint.log`,
`/private/tmp/apex-debug-duration-build.log`,
`/private/tmp/apex-debug-duration-assets.log`,
`/private/tmp/apex-debug-duration-docs.log`,
`/private/tmp/apex-debug-duration-e2e-dev-admin.log`,
`/private/tmp/apex-debug-duration-e2e-local-auth.log`,
`/private/tmp/apex-debug-duration-e2e/`,
`/private/tmp/apex-debug-duration-final-dev-admin.log`,
`/private/tmp/apex-debug-duration-final-local-auth.log`, and
`/private/tmp/apex-debug-duration-final/`.

<a id="debug-summary-duration-reconciliation-2026-10-26--cleanup-and-publication"></a>
### Cleanup and publication

The final transaction workflow passed in both profiles after the pagination fix.
No test controllers, browser sessions, or listeners on ports 18121/18122 remained.
The isolated Go build/test cache was reduced from approximately 2.0 GiB to 8 KiB.
Disposable frontend caches and the temporary dependency symlink were removed,
preserving installed dependencies and generated deliverable assets. Docker build
cache pruning completed with 0 B reclaimed. Raw browser evidence remains untracked.
The reviewed correction follows the previously requested commit/push workflow.

<!-- Original record: docs/qa/reviews/debug-summary-har-final-review-2026-10-26.md; local evidence kept outside git -->
<a id="debug-summary-har-final-review-2026-10-26"></a>

## Debug summary and HAR publication review — 2026-10-26


Scope: the retained HAR capture controls and expandable transaction summary
changes following `819563141`. The user requested a review/fix cycle and a direct
commit/push to main. Work remains isolated from the shared dirty checkout.

<a id="debug-summary-har-final-review-2026-10-26--review-cycle"></a>
### Review cycle

The first publication review covered all source changes and generated contracts,
including capture propagation, body limits, redaction, timing provenance,
observation identity, session authorization/expiry, frozen transaction structure,
repeated executions, evidence links, and keyboard navigation.

Findings fixed with regression tests:

1. HTTP exports with measured send timing incorrectly claimed that wait included
   send. `wait_includes_send` now reflects whether send was separately measured.
2. Closing an earlier overlapping transaction discarded the later transaction's
   matching end marker. Crossing spans are now represented as siblings, so each
   retains its own recorded outcome and complete boundaries. Incomplete started
   transactions also link to their start evidence.
3. The HTTP observer treated pre-dispatch policy failures as retained traffic and
   could hide the final observed redirect when a proposed successor was refused.
   Capture now requires a connection attempt or an observed response. Failed
   connection attempts remain retained, even with detailed timings disabled;
   rejected authored targets remain absent. A redirect is intermediate only when
   its successor was actually observed.

The second review inspected these fixes and the complete staged source set.
It found no further actionable findings. Generated dashboard build artifacts
are excluded from the source commit, consistent with their existing ignore rules.

<a id="debug-summary-har-final-review-2026-10-26--validation"></a>
### Validation

The final dashboard build (including both TypeScript projects), lint, all 8,715
unit tests across 750 files, and focused Go regression/race checks passed.
All nine affected debugger Playwright workflows passed in each authentication
profile (18 passes), including actual HTTP/browser execution and downloaded HARs.
The affected Go linters reported zero issues. HAR API authorization, expiry,
option validation, OpenAPI freshness, and generated TypeScript contract checks
passed separately. Documentation checks passed.

The repository-wide `go test -p 4 -timeout=20m -json ./...` completed without a
timeout: all 169 non-API tested packages passed, and 37,703 test cases/subtests
passed. The API package finished in 971.749 seconds with only the six existing
native replay-artifact cleanup failures listed below. Their failure messages
match the previous unchanged-main reproduction; the full Go gate is therefore
not green. This feature does not change native HAR ownership or disposal.

- `TestReplayArtifactHTTPCleanupLifecycle`
- `TestReplayArtifactHTTPProjectDeletionAndNamespaceCleanup`
- `TestReplayArtifactRuntimeRecoversNativePair`
- `TestReplayArtifactRuntimeCleanupRequiresSourceAndJoin`
- `TestReplayArtifactRuntimeCleanupTraversesForeignPage`
- `TestReplayArtifactRuntimeCleanupThroughStoreOverlay`

Baseline evidence and reproduction are described in
[the earlier completion-summary review](#debug-completion-summary-2026-10-25).
An API test inventory audit confirmed that every top-level test completed or was
explicitly skipped by existing platform/integration gates. The follow-up batch
covered the 17 gated skips and completed successfully; no tail was left untested
by an aggregate timeout in this run.

Raw evidence: `/private/tmp/apex-debug-review-build.log`,
`/private/tmp/apex-debug-review-lint.log`,
`/private/tmp/apex-debug-review-unit.log`,
`/private/tmp/apex-debug-review-dashboard-full.log`,
`/private/tmp/apex-debug-review-regressions.log`,
`/private/tmp/apex-debug-review-race.log`,
`/private/tmp/apex-debug-review-api.log`,
`/private/tmp/apex-debug-review-contracts.log`,
`/private/tmp/apex-debug-review-docs.log`,
`/private/tmp/apex-debug-review-go-full.log`,
`/private/tmp/apex-debug-review-go-lint.log`,
`/private/tmp/apex-debug-review-api-test-list.log`,
`/private/tmp/apex-debug-review-api-remaining-list.txt`,
`/private/tmp/apex-debug-review-api-remaining.log`,
`/private/tmp/apex-debug-review-e2e-dev-admin.log`,
`/private/tmp/apex-debug-review-e2e-local-auth.log`, and
`/private/tmp/apex-debug-review-e2e/`.

<a id="debug-summary-har-final-review-2026-10-26--publication-and-cleanup"></a>
### Publication and cleanup

The reviewed source is committed for the user-requested direct push to main.
`origin/main` was fetched again after validation and had no incoming changes.
The shared checkout and its unrelated source changes were preserved.

No session browser/controller processes or listeners on ports 18121/18122
remained. The isolated Go build/test cache was cleaned from approximately
5.4 GiB to 8 KiB. The 54 MiB linter cache, disposable frontend caches, and temporary
dependency symlink were removed; installed dependencies and generated deliverable
assets were preserved. Docker build-cache prune completed with 0 B reclaimed.

<!-- Original record: docs/qa/reviews/debug-summary-transactions-2026-10-26.md; local evidence kept outside git -->
<a id="debug-summary-transactions-2026-10-26"></a>

## Debug summary transaction breakdown — 2026-10-26


Implemented in the existing `debug-completion-summary` isolated checkout on
`8195631417a338b7d84fe4104c703aacb45c6de5`. Fetched `origin/main` before builds;
there were no incoming commits. Existing HAR capture option changes remain in
this checkout and were preserved.

<a id="debug-summary-transactions-2026-10-26--behavior"></a>
### Behavior

The completion summary now lists the run's transaction definitions, phase,
recorded pass/fail outcome, start/end times, and elapsed duration. Parent rows
expand into subtransactions, page requests, control-flow descendants, and
individual repeated executions. Inspect opens the original debugger event.

The run admission captures only labels and structural coordinates, without
request bodies or authored targets. Completion freezes this outline with the
run evidence. Later edits cannot change the completed transaction hierarchy.
New runs reset expansion state. Withheld runtime evidence hides the table.

Marker completion outcomes remain authoritative, including explicit pass over
failed child requests. Group durations are elapsed spans, including debugger
pauses and think time; leaf durations are recorded measurements. Missing marker
completion, missing timestamps, and manually reported transactions do not acquire
invented timing. Unexecuted transactions are distinguished from missing event
evidence; payload truncation alone does not change transaction outcomes.

<a id="debug-summary-transactions-2026-10-26--validation"></a>
### Validation

- Dashboard TypeScript checks, production asset build, lint, and documentation checks passed.
- Six focused Vitest files passed: 357 tests covering transaction structure,
  nested/repeated execution identity, marker outcomes, stopped/unexecuted runs,
  missing evidence/timestamps, legacy request coordinates, duplicate evidence,
  cross-phase/case-insensitive markers, manual reporting, frozen snapshots,
  evidence links, disclosure restrictions, and existing editor behavior.
- All nine debugger Playwright workflows passed in each of `dev-admin` and
  `local-auth`. This includes actual HTTP/browser replay and HAR downloads.
- The final transaction workflow was repeated on the final asset build in both
  profiles. Enter/Space expansion, horizontal keyboard scrolling at 900 px,
  desktop/narrow layouts, pass/fail propagation, child Inspect navigation, and
  reset/unexecuted rows after a stopped rerun are covered.
- Desktop and narrow screenshots were visually reviewed. The table remains
  horizontally scrollable inside the editor at narrow widths.
- Source and test changes were reviewed; fixes included payload-truncation
  semantics, cross-phase/case-insensitive marker pairing, and manual timing.
  The final review found no further actionable issues in this change.
- This addition changes only dashboard source and documentation. No API/schema
  regeneration or new Go test suite was needed. This record does not supersede
  the earlier HAR capture option review's full Go suite limitations.

Raw evidence: `/private/tmp/apex-debug-transactions-unit.log`,
`/private/tmp/apex-debug-transactions-typecheck.log`,
`/private/tmp/apex-debug-transactions-lint.log`,
`/private/tmp/apex-debug-transactions-build.log`,
`/private/tmp/apex-debug-transactions-e2e-dev-admin.log`,
`/private/tmp/apex-debug-transactions-e2e-local-auth.log`,
`/private/tmp/apex-debug-transactions-e2e/`,
`/private/tmp/apex-debug-transactions-final-dev-admin.log`,
`/private/tmp/apex-debug-transactions-final-local-auth.log`, and
`/private/tmp/apex-debug-transactions-final/`.

<a id="debug-summary-transactions-2026-10-26--cleanup"></a>
### Cleanup

Both final browser workflows passed. No session controller/browser processes or
listeners on ports 18121/18122 remained. The isolated Go build/test cache was
cleaned from approximately 2.0 GiB to 8 KiB. Frontend `.vite`/`.vite-temp` caches
and the session dependency symlink were removed; installed dependencies were
preserved. Docker build-cache prune completed with 0 B reclaimed. Raw evidence
and generated deliverable assets are retained. This verification preceded the publication review.

The subsequent review/fix cycle and publication are recorded in
[the final publication review](#debug-summary-har-final-review-2026-10-26).

<!-- Original record: docs/qa/reviews/feature-completion-browser-document-2026-10-05/FRONTEND_VERIFICATION.md; local evidence kept outside git -->
<a id="feature-completion-browser-document-2026-10-05-frontend-verification"></a>

## Browser document frontend qualification


This record qualifies the unchanged dashboard bundle and its final affected repeats against the reviewed controller corrections. Overall repository completion remains subject to the final root-owned full Go gate and QA README. Full browser evidence/intelligence roadmap items remain partial.

<a id="feature-completion-browser-document-2026-10-05-frontend-verification--frozen-inputs"></a>
### Frozen inputs

| Input closure | Files | Canonical files-list SHA256 | Final bookend |
| --- | ---: | --- | --- |
| Dashboard, workflow drivers, generated DSL and explicitly bundled help | 1,508 | `671411253c0184e5f20e58c42da72dd4df848edf1eb9176a474659878299c135` | Source bookend (`evidence/frontend-document-final-reviewed-controller-source-bookend.json`) |
| Complete embedded dashboard, including build-time metadata | 247 | `d21effd983b4274051016e6045cf48cc223e2ed0311ca5cf23dbf60efc9b5c44` | Embedded bookend (`evidence/frontend-document-final-reviewed-controller-embedded-bookend.json`) |
| Final reviewed engine/archive/CLI loss, final-publication and actual-wire identity correction | 6 | `b3af9298582e169c779629ff8a526e3f0b187442d0ca37d73f24946fc086c398` | Controller correction bookend (`evidence/browser-document-loss-identity-final-controller-bookend.json`) |

No dashboard source or embedded file changed between the final complete dashboard cycle and the final corrected-controller repeats. Their verified proof indices check all three input closures; the repository-wide Go source closure is separately captured by the root-owned Go gate. Application/build sources stay frozen during those gates.

<a id="feature-completion-browser-document-2026-10-05-frontend-verification--final-dashboard-and-browser-checks"></a>
### Final dashboard and browser checks

The final complete dashboard cycle (`evidence/frontend-document-full-cycle-chart-fixed-final-1.json`) exited 0 in 2,301.268 seconds. It ran typecheck, lint, **513 unit files / 5,688 tests**, the embedded build, **238 dev-admin passes / 5 skips**, and **169 local-auth passes / 1 skip**. Its raw log (`evidence/frontend-document-full-cycle-chart-fixed-final-1.log`) SHA256 is `825edf874831dcdf5a7241e5089e6bebac7d54d24c6a06e495874c004c558e48`. Skips remain skips; they do not qualify their omitted workflows.

Backend-only review corrections followed that successful dashboard cycle. The final affected repeats used the corrected controller with the unchanged embedded bundle:

| Profile | Actual result | Helper duration | Raw log SHA256 |
| --- | --- | ---: | --- |
| Dev-admin (`evidence/frontend-document-corrected-controller-dev-browser-final-2.json`) | 8 passed, 0 skipped, 0 flaky, 0 unexpected | 67.652 s | `002ec3aedcb9ab52662c7eccbfa1cb7baa485ece50e125fa6185e87a0d2abf02` |
| Local-auth (`evidence/frontend-document-corrected-controller-local-browser-final-2.json`) | 8 passed, 0 skipped, 0 flaky, 0 unexpected | 96.626 s | `cda7dcf5ea26bf600d0880e08ac233475f92aff5a68db0fafd8c130f40c25d93` |

The affected set covers imported identity comparison save/reopen, the complete author journey and controlled deferred-focus regression, resource inspection, legacy live/archive availability and mixed-cohort comparison, independent live document populations, and latest archived document observations. These are registered actual-controller/UI workflows in both authentication profiles. Document measurement values are bounded synthetic transport fixtures; genuine native browser acquisition is qualified by its separate native evidence index.

The dev-admin proof index (`evidence/browser-corrected-controller-dev-proof-index-final-2.json`) and local-auth proof index (`evidence/browser-corrected-controller-local-proof-index-final-2.json`) each retain **three selected successful workflows, 22 original successful JSON attachment bodies and five PNGs**. The original list/JSON reporters remain beside the indices. No successful trace is claimed. Natural left-position document overview and actual keyboard-scrolled right-side captures retain before/after geometry for the whole heading, visible content owner, center hit, direct readable table and contained overflow. The author workflow retains the real Target selection/focus before and after controlled RAF release, then deletes through a global key press without locator refocus. Controllers were physically joined and both owned listeners were absent after the repeats.

<a id="feature-completion-browser-document-2026-10-05-frontend-verification--review-corrections-and-targeted-controller-qualification"></a>
### Review corrections and targeted controller qualification

Read-only independent reviews closed the standalone wire-null population, positive fractional cohort scale, document layout/capture, Source Find, exact inspector focus ownership and canonical chart-description overflow findings. Those reviews and the failing/full-repeat dashboard receipts remain in the evidence directory.

Controller follow-up review separated request-identity losses from document observation counts, derived incomplete coverage from either typed loss, published new coverage during idle/Stop, refused repeated unavailable-provider publication and prevented rejected-identity retry inflation. Provider/identity-ledger overflow retains incomplete coverage with an unknown omitted population, rather than inventing a distinct observation count. Raw request counters remain in the immutable sidecar. The targeted normal gate (`evidence/frontend-document-loss-and-run-identity-normal-reviewed-final.json`) and race gate (`evidence/frontend-document-loss-and-run-identity-race-reviewed-final.json`), each three repeats across engine/API/CLI, passed in 116.912 and 244.794 seconds. Later corrections have their own final gates below; those earlier results remain scoped to their captured inputs.

A separate overlay reproduced final evidence stranded after the active gauge reached zero during slow teardown. The final sample predicate retains accepted pending observations until snapshot publication acknowledges the window. Never-started and actual zero-gauge/blocked-teardown/finish-to-idle-or-Stop regressions prove exact final counts without repetition. The final engine normal repeat (`evidence/frontend-document-terminal-window-normal-clean-final.json`) passed in 7.015 seconds; the final race repeat (`evidence/frontend-document-terminal-window-race-clean-final.json`) passed in 18.044 seconds. The independent terminal-window review (`evidence/browser-document-terminal-window-independent-final.json`) has no remaining finding.

The actual CLI archive wire uses `manifest.test_id`, unlike the live path echo. The CLI now checks that actual identity, rejects malformed/decoded-duplicate/top-versus-manifest conflicts, and uses at most one bounded authenticated no-history lookup for an exact canonical-ID/RefKey/project match. Matching canonical IDs use no extra read. Legacy omitted/empty identities explicitly report `historical-requested-unverified`. A typed `api.RunArchiveReport` fixture, alias mismatches, oversized response, cancellation, denied lookup and redirect refusal are covered. The final CLI normal repeat (`evidence/frontend-document-cli-native-manifest-identity-normal-reviewed-final.json`) passed in 9.985 seconds; the final race repeat (`evidence/frontend-document-cli-native-manifest-identity-race-reviewed-final.json`) passed in 44.136 seconds. The independent actual-wire review (`evidence/browser-document-cli-actual-wire-independent-final.json`) is clean.

<a id="feature-completion-browser-document-2026-10-05-frontend-verification--retained-historical-failures-and-scope"></a>
### Retained historical failures and scope

The first complete dashboard cycle passed dev-admin but failed one local-auth authoring focus case. Its original log, trace inspection, exact source and `2a254…` embedded inventories remain historical; its Go pass qualifies only that captured source/bundle. Deterministic editor and browser regressions reproduced real deferred focus theft, which was corrected without delaying the operator assertion.

The second cycle exposed the already-reviewed native chart screen-reader description overflow in local-auth and was interrupted once that concrete failure was known. Its `c26c…` source/bundle, observed results and physical controller shutdown record remain retained without a complete-pass claim. The canonical component-scoped CSS/unit/geometry correction then passed the complete final cycle on `d21eff…`.

The first corrected-controller proof (`final-proof-1`) and the interrupted first loss-correction full Go gate are also historical: later terminal-window and actual manifest identity findings required new reviewed source and final proof (`final-proof-2`). Earlier full Go results on an unchanged dashboard bundle do not qualify later changed Go source.

No new health/CI score, CPU/RSS or action-count document denominator, inferred observation window, document identity from summary, or native HAR support is claimed. Genuine native capture, protected sidecars and dashboard inspection have separate scopes. BFCache/hidden-resume breadth, complete resource drill-down/HAR lifecycle, whole-page completeness, clock uncertainty, overhead/hardware/platform acceptance and full browser intelligence remain open. Only the user-waived 10 GB import benchmark is waived; the other program gates remain open.

<!-- Original record: docs/qa/reviews/feature-completion-browser-document-finality-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-browser-document-finality-2026-10-05"></a>

## Browser document finality qualification — 2026-10-05


Current composed source/test qualification: the default full Go suite, full dashboard cycle in both authentication profiles, affected API races, actual PostgreSQL cohort, SDK contract and generated-contract checks passed in the [composed review](INTEGRATION_2026-10.md#feature-completion-composed-review-2026-10-05). That qualification is scoped; the broader feature requirements and explicitly unrun acceptance remain open. The following component checkpoint predates these composed results. Its original bytes are preserved in the historical checkpoint (`README.component-checkpoint.txt`).

This neutral correction joins an already-running pagehide finalizer, waits for document admission before sending metrics, fences stale BFCache continuations, and includes current native child frames before finalization. Current native frames take priority over retained stale handles; the selected frame set is bounded at 256 and overflow is explicitly incomplete. The embedded pinned web-vitals algorithm is unchanged. This slice adds no target requests or interaction probes.

The prior stale-frame isolation prerequisite passed three repetitions, but a later twenty-repeat diagnostic reproduced missing final INP in four Chromium cases and one WebKit case. That earlier diagnostic is preserved as red evidence. The new network-free Node VM regressions and current-child-frame regression failed against the earlier production implementation. The first fixed green run is retained as superseded because it preceded selected-frame bound tests.

The final affected deterministic normal and race tests each passed three repetitions. The existing native identity/no-interaction oracle then passed twenty repetitions per engine in `APEXLOAD_BROWSER_TESTS=require` mode: forty Chromium/WebKit subtests, zero skips or failures. Its existing finality, exact request counters, identities, interaction, and background-data thresholds were retained; aggregate failure diagnostics remain available. Native test admission was coordinated with other session builds and the exact test process physically joined.

The final candidate inventory contains eleven direct source/module inputs, not a full transitive dependency inventory. Test receipts separately preserve the repository Go/module inventory before and after each execution; final runs contain 4,054 entries and unchanged source bookends. Nine installed SDK/driver/browser/Node inputs were hashed before and after the final native cohort and matched. That runtime bookend does not claim to inventory every native shared library or module-cache dependency. Original helper receipts are identified by SHA; duplicated repository inventories are stored once without losing their before/after association. Raw output bytes are preserved separately and hash checked.

Author and parent independent repeat reviews are clean, and both exact receipts are preserved with unchanged direct-input bookends. These focused checks do not qualify native HAR writer ownership, archive transfer/export, full browser evidence delivery, full Go, dashboard workflows, or the entire feature program. Root will run composed gates after the remaining independently qualified sources are combined. No private user HAR content or real target traffic was used.

See the qualification index (`evidence/qualification-index-v1.json`), final direct inputs (`evidence/apexload-neutral-document-finality-candidate-source-v1-20261005.json`), and the preserved red, superseded, and final check receipts under `evidence/`.

<!-- Original record: docs/qa/reviews/feature-completion-browser-frame-isolation-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-browser-frame-isolation-2026-10-05"></a>

## Browser document finalization isolation


A detached old frame could trigger page-wide censoring before a live sibling supplied its final document observation. Finalization now attempts each live frame before applying the shared end boundary. Frame collection is deterministic, excludes ended or missing frames, and removes duplicates. The adjacent regression explicitly exercises stale-first and live-first order.

Gate index (`evidence/frame-isolation-gate-index-v1.json`) records the exact two-file source freeze and unchanged Go source bookends. Required-mode native checks repeated the existing Chromium and WebKit identity/no-interaction oracle three times each, with zero skips and exact fixture counters of two main-document and four frame requests per engine attempt. The physically closed/joined-worker regression also passed. Ordered-frame and joined-worker regressions passed three times under the race detector. The helper, raw logs, and source inventory are retained.

This is a prerequisite bug fix. Extended HAR working-file ownership, archive/export, aggregate writer/RSS limits, cross-platform qualification, and the composed full Go/dashboard gates remain separate acceptance work.

The independent repeat review (`evidence/frame-isolation-independent-repeat-review.json`) found no additional actionable issues. The composed documentation check (`evidence/frame-isolation-docs-composed-1.json`) retained an existing missing authoring-qualification link; it remains pending until that separate slice is integrated.

<!-- Original record: docs/qa/reviews/feature-completion-browser-resources-2026-10-05/BACKEND_QUALIFICATION.md; local evidence kept outside git -->
<a id="feature-completion-browser-resources-2026-10-05-backend-qualification"></a>

## Retained resource-reader backend qualification


This records the scoped resource-reader API, shared wire/projection, storage and CLI work. The candidate remains frozen pending authored Help/baseline composition and full repository/dashboard qualification. It does not close the browser evidence/intelligence program.

<a id="feature-completion-browser-resources-2026-10-05-backend-qualification--verified-behavior"></a>
### Verified behavior

The bounded archive reader preserves native zero and false, explicit empty capture, absent capture, independent source clocks and transaction/resource locators. Pagination retains only limit+1 selected observations while counting the completed bounded scan. Signed 15-minute continuations bind the durable project/run authority, opened source digest, manifest, query and sort. Final authorization, archive replacement/deletion and cancellation refuse stale publication. The contextual project resolver shares the ten-second read deadline; legacy contextless global authentication and HTTP response writes have separate limits.

Actual controller and generator Parquet writers project selected fields from the exact original JSONL row. The original source remains unchanged. The private physical decoder bounds metadata/pages/retained allocations and rejects unsupported or malformed layouts without partial populations. Its 128 MiB retained-state accounting is a logical allocation bound, not process RSS evidence. Opaque resource JSON lexical statistics are omitted only on that column: the actual single-transaction/80-observation controller header fell from 197,404 to 22 bytes and generator header from 138,804 to 22 bytes. Historical oversized statistics still produce an explicit limit refusal. See the decision receipt (`evidence/opaque-projection-statistics-correction-decision.json`).

With explicit `APEXLOAD_BROWSER_TESTS=require` and `TZ=America/New_York`, genuine Chromium 149.0.7827.55 acquired 81 retained observations from 81 controlled fixture requests. The actual run archive retained one transaction plus separate document evidence. Both original JSONL and physical Parquet returned pages of 50 then 31, exact native identity filters and native-value order, with verified producer/source hashes and no additional target traffic. Eighty observed encoded-body zeros and 81 observed service-worker false values survived. Original offset timestamps remain unchanged; only the inspection projection is UTC-normalized. Twelve physical timestamp/parity cases cover valid offsets and explicit calendar, subnanosecond, offset and UTC-range refusals.

Actual PostgreSQL normal and two-repeat race gates verify fresh identity, exact-ID-before-alias/tombstones, one durable cursor-key winner across independent stores, current authorization, binding overflow and occupied-pool cancellation. All twelve uniquely owned schemas were dropped and their absence verified. Shared decoder/API/SQLite/physical writer/CLI tests and repeated races pass. Generated schemas, embedded copies, TypeScript bindings, the canonical API contract matrix and both required opt-in inventory gates are fresh.

<a id="feature-completion-browser-resources-2026-10-05-backend-qualification--final-scoped-receipts"></a>
### Final scoped receipts

| Gate | Receipt | Result |
|---|---|---|
| Actual writer header correction and historical refusal | header repeat (`evidence/opaque-projection-header-repeat.json`) | PASS, source unchanged |
| Genuine required Chromium acquisition, JSONL/physical Parquet and source offsets | actual acquisition (`evidence/actual-chromium-acquisition-opaque-stats-repeat.json`) | PASS, source unchanged |
| Actual PostgreSQL lifecycle | PostgreSQL normal (`evidence/resource-postgres-normal-initial.json`) | PASS, exact owned cleanup |
| Actual PostgreSQL two-repeat races | PostgreSQL race (`evidence/resource-postgres-race-initial.json`) | PASS, exact owned cleanup |
| Shared contracts/API/SQLite/generator/CLI affected checks | normal (`evidence/resource-affected-normal-final.json`) | PASS, source unchanged |
| Same affected checks, two-repeat races | race (`evidence/resource-affected-race-final.json`) | PASS, source unchanged |
| Canonical matrix regeneration via approved flag | matrix (`evidence/resource-m1-contract-matrix-regeneration.json`) | PASS |
| API/embedded docs/TS and opt-in freshness | freshness (`evidence/resource-generated-contracts-freshness-final.json`) | PASS, source unchanged |

The five generated public files and additional matrix/inventory inputs have matching before/after hashes (`evidence/generated-contract-inputs-final-freshness-bookends.json`). The matrix adds exactly one protected `results:read` resource operation. Existing opaque get/update response descriptions become their actual nullable storage DTOs through current inference; credential KeyHash remains excluded.

<a id="feature-completion-browser-resources-2026-10-05-backend-qualification--retained-development-findings"></a>
### Retained development findings

The backend evidence index (`evidence/backend-evidence-index.json`) preserves all original attempts. They include compilation/init-cycle corrections, auth-fixture corrections that preserve exact scope policy, physical dictionary width/retained-string budget regressions, the genuine source-offset refusal, a missing-import compilation failure, the genuine oversized-statistics failure, and the generator fixture's SDK-renamed footer lookup failure. Failed receipts are not acceptance evidence. The generator header test now independently decodes the actual wire footer.

The actual Chromium gate is excluded from ordinary affected unit/race commands because it has a separate explicit required-runtime receipt. Default/auto/skip are not its qualification. Controlled dashboard transport workflows and their PNGs remain separate from native acquisition; this document does not replace the parent UI/full-cycle receipts.

<a id="feature-completion-browser-resources-2026-10-05-backend-qualification--remaining-scope"></a>
### Remaining scope

Live ordinary-resource retention, HAR delivery, whole-page capture coverage, synchronized clocks, dependency inference, browser baseline/scoring intelligence, cross-engine density and packaged-platform acceptance are outside this reader qualification. No 1 GiB/10 GB native analytics or messaging-provider acceptance is inferred. Full composed repository and dashboard cycle gates remain required before commit.

<!-- Original record: docs/qa/reviews/feature-completion-browser-resources-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-browser-resources-2026-10-05"></a>

## Retained browser resource inspection — scoped qualification


Current composed source/test qualification: the default full Go suite, full dashboard cycle in both authentication profiles, affected API races, actual PostgreSQL cohort, SDK contract and generated-contract checks passed in the [composed review](INTEGRATION_2026-10.md#feature-completion-composed-review-2026-10-05). That qualification is scoped; the broader feature requirements and explicitly unrun acceptance remain open. The following component checkpoint predates these composed results. Its original bytes are preserved in the historical checkpoint (`README.component-checkpoint.txt`).

The candidate implements an authorized bounded archive resource reader, a closed CLI query and an explicitly opened dashboard inspector. It is awaiting authoring/Help composition and full repository/dashboard checks before commit. This record does not close the browser evidence or intelligence program.

The [backend qualification](#feature-completion-browser-resources-2026-10-05-backend-qualification) separates genuine required Chromium acquisition, immutable JSONL/physical Parquet transport, durable SQLite/PostgreSQL authority, bounded decoding and repeated races from dashboard evidence. The [user guide](../../guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#retained-resource-inspection) explains retained observations, availability, continuation expiry and interpretation limits.

<a id="feature-completion-browser-resources-2026-10-05--focused-dashboard-qualification"></a>
### Focused dashboard qualification

The inspector opens only on explicit request. Canonical run/project/archive ownership and request generations retire stale callbacks. The response is bounded and strictly validated; filters remain drafts until Apply, pagination uses opaque source-bound continuations, and retries retain the applied query. Native zero, false and phase points stay distinct from unavailable measurements. The table owns its keyboard-accessible horizontal scroll region.

| Gate | Receipt | Result |
|---|---|---|
| Eight affected dashboard test files | unit checks (`evidence/resource-all-affected-units-after-accessible-fix-1.json`) | PASS, 187 tests |
| Dashboard typecheck | typecheck (`evidence/resource-final-ui-typecheck-3.json`) | PASS |
| Dashboard lint | lint (`evidence/resource-final-ui-lint-3.json`) | PASS |
| Final focused embedded dashboard | build (`evidence/resource-final-ui-build-3.json`) | PASS, source unchanged |
| Dev-admin inspector and Help journeys | browser checks (`evidence/resource-fixed-ui-dev-3.json`) | PASS, 3 tests |
| Local-auth inspector and Help journeys | browser checks (`evidence/resource-fixed-ui-local-3.json`) | PASS, 3 tests |
| Independent complete pixel review | six-image review (`evidence/resource-fixed-ui-3-independent-visual-review-complete.json`) | No additional findings |

Both browser profiles exercise paging through 80 controlled retained observations, exact filters, page-size changes, query/cursor retirement, native measured zero/false, unavailable legacy fields, row disclosure and Help reload. The UI fixtures control archive transport and do not claim actual browser acquisition; the separate backend fixture establishes that fact. All six final PNG images were inspected. They remain with their hashed artifact index (`evidence/resource-fixed-ui-3-artifact-index.json`). The final visual record supersedes the preliminary partial review without deleting it.

The frontend evidence index (`evidence/frontend-evidence-index.json`) verifies the raw log hashes and records the exact frozen source/embed inventories. Earlier accessible-label and project-list fixture failures remain retained. The fixture correction reloads the real application after creating the project through its actual API; it does not replace authentication or weaken archive ownership assertions.

<a id="feature-completion-browser-resources-2026-10-05--integration-review-and-focused-checks"></a>
### Integration review and focused checks

The integration candidate includes the four authored Help guides and their catalog. The composed unit receipt (`evidence/resource-help-composed-units-20261005.json`) records 190 passing tests across eight affected files, including the additional Help composition cases. Its frozen source checks cover the frontend inputs recorded in that receipt; they do not establish full repository or browser qualification.

The parent source review (`evidence/resource-parent-composed-source-repeat-review.json`) rechecks 15 selected handler, authorization, continuation, deadline, durable-identity, protocol-projection, CLI and dashboard files with no additional findings. Its scope does not replace the separate physical Parquet review or the full program gates.

The preliminary composed documentation check (`evidence/resource-help-composed-docs-20261005.json`) remains a retained failure because the authoring guide references its real qualification record before that record has been integrated. Integrate the qualified authoring source and evidence, then rerun documentation checks; a placeholder record is not acceptance.

The parent physical-reader repeat review (`evidence/resource-parent-physical-reader-repeat-review-20261005.json`) also inspected the complete physical Parquet and JSONL readers and selected adjacent corruption/budget fixtures, with no additional findings. This review adds no execution or process-RSS claim.

<a id="feature-completion-browser-resources-2026-10-05--remaining-release-and-program-checks"></a>
### Remaining release and program checks

Compose the authored Help catalog and guides, prove the resource guide's browser-document link resolves to bundled content, regenerate the final embed and run the full Go repository suite and dashboard cycle against that exact candidate. Focused green receipts do not satisfy those pending gates. Preserve the typed generated contract changes for existing nullable storage responses as well as the new resource operation.

Live resource census, complete capture coverage, HAR delivery, synchronized source clocks, browser dependency/baseline intelligence, density/platform qualification and the broader telemetry/native analytics/messaging exits remain open. Retained observation counts establish no capture denominator, health verdict, server-processing time or causality.

A fresh independent repeat review inspected the archive readers, authorization/source ownership, opaque continuation, physical Parquet and JSONL paths, native availability, CLI cancellation and dashboard stale-response ownership. It found no actionable issues in the frozen resource-inspector slice; the exact receipt (`evidence/apexload-resource-inspector-independent-repeat-review-v1-20261005.json`) verifies 50 reviewed inputs against the prior backend and frontend qualification hashes. This is a static review and does not replace final composed gates.

<!-- Original record: docs/qa/reviews/feature-completion-native-har-ownership-2026-10-05/README-v5-static-history.md; local evidence kept outside git -->
<a id="feature-completion-native-har-ownership-2026-10-05-readme-v5-static-history"></a>

## Native HAR ownership candidate review


Status: v5 independent static repeat review found no additional findings in
the five-file delta, with all 33 direct inputs revalidated. Required-mode
native acceptance and heavy API checks remain pending. This bundle does not
establish release completion or full browser evidence acceptance.

The candidate adds private never-reused HAR generations, exact context
close-once ownership, one shared bounded teardown deadline, existing-descriptor
cross-process leases, durable no-replace scrubbed publication and an
identity-bound retirement ledger. It isolates stale native-frame finalizer
errors from live sibling document receipts. See the
[scope guide](../../BROWSER_EVIDENCE_ARCHITECTURE.md).

The coherent candidate direct source inventory is
`evidence/apexload-native-har-ownership-candidate-source-v5-20261005.json`:
33 direct inputs (23 changed/new Go sources/tests and 10 supporting references).
The before-strict-type draft is historical and superseded. Neither inventory
is a complete transitive build or native runtime inventory.

Parent reviews v1 through v4 identified actionable findings. Their exact
receipts, prior source freezes, red attempts and failure dispositions are
retained. The candidate fixes blocked CDP detach ownership, callback Goexit
false-success receipts, incomplete retirement recovery, retention bounds,
document close ownership and stale-frame isolation, planned-batch ledger
replacement, mixed recorded/unrecorded batch mutations, duplicate completed
pre-rename discovery, teardown phase budgets, and post-wait namespace identity.
Additional red-to-green fixtures require private raw-file reservation before
SDK admission, correct joined size metadata, and actual typed JSON scalars.
Unknown or unproven writers remain preserved.

The parent v5 static repeat review (`evidence/apexload-native-har-ownership-parent-review-v5-20261005.json`)
contains no additional static findings in the reviewed delta. It ran no Go
tests, and explicitly requires native/API execution and final composed gates.
The prior v4 finding receipt (`evidence/apexload-native-har-ownership-parent-review-v4-20261005.json`)
remains a changes-required result, with its disposition preserved.

The current v5 scoped qualification is
`evidence/native-har-v5-focused-final-2.json`
and `evidence/native-har-v5-race-final-1.json`:
the selected browser ownership, capture, retention and document regressions
passed twice normally and twice under the race detector. These commands use
short mode: the new native fixtures are skipped, and adjacent API batch/error
fixtures, including completed retirement before rename, are uncompiled pending
the coordinated heavy build slot. The exact frozen v4 pre-dedup API source
and overlay are preserved to execute a later red reproduction without changing
the v5 worktree; this prepared overlay has not been executed. No broader
API or required-mode native pass is inferred from these checks.

Each check records its command, elapsed time, exit code, raw-log SHA and
point-in-time repository Go/module hash stability. The latest checks froze
4,030 repository Go/module inputs; they do not inventory all dependencies in
the module cache or native runtime. Large original inventories remain in the
explicitly named private raw receipts. Compact digests, raw test logs and
direct source inventories are retained here. A historical broad short suite
cannot qualify required-mode native acceptance: some existing tests omit
short guards, used an auto runtime gate, and lacked verbose per-test execution.

The neutral ordered frame-finalizer patch has no HAR owner/ledger dependency.
It was separately applied and qualified in the parent integration worktree;
that prerequisite's unchanged actual-native oracle is separate from this
candidate's pending HAR-owner native runtime tests.

All fixture HARs, URLs, cookies and bodies are invented loopback or example
content. The user's original HAR and derived private body/header/URL content
are absent. No original-target replay, controller/API upload, native export
or archive-transfer acceptance is performed by these tests.

The 2 GiB working admission check is point-in-time, distinct from future
aggregate active-writer or RSS enforcement. Unknown legacy/crash writers are
preserved. Linux/macOS source identity checks do not establish Linux/Windows
runtime qualification. Full BE-3, managed native archive transfer/export,
aggregate writer/RSS control and full program delivery remain open.

Check history and failure dispositions (`evidence/check-history.json`).

<!-- Original record: docs/qa/reviews/feature-completion-native-har-ownership-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-native-har-ownership-2026-10-05"></a>

## Native HAR ownership qualification


Current composed source/test qualification: the default full Go suite, full dashboard cycle in both authentication profiles, affected API races, actual PostgreSQL cohort, SDK contract and generated-contract checks passed in the [composed review](INTEGRATION_2026-10.md#feature-completion-composed-review-2026-10-05). That qualification is scoped; the broader feature requirements and explicitly unrun acceptance remain open. The following component checkpoint predates these composed results. Its original bytes are preserved in the historical checkpoint (`README.component-checkpoint.txt`).

The v7 candidate preserves the qualified v6 production behavior and passes
the refreshed documentation, provenance and packaging gates. The scoped SDK,
browser, native HAR ownership and controller retention gates are listed below. Independent reviews
found no additional scoped findings after the recorded fixes. Composed full
repository and dashboard qualification remains the parent task's separate gate.
This bundle does not establish full browser evidence or program completion.

The change gives each HAR writer a private never-reused generation, exact
context close ownership, one bounded teardown budget and descriptor-backed
cross-process leases. Completed output is scrubbed, durably published without
replacement and discoverable by flat artifact retention. An identity-bound
retirement ledger recovers proven terminal deletions while preserving unknown
or replaced writers. See the [scope guide](../../BROWSER_EVIDENCE_ARCHITECTURE.md).

The local MIT-licensed Playwright Go subset remains pinned to v0.6100.0 and
its existing native driver and browser versions. Its optional owned HAR close
method enters the actual SDK close body once and retains that complete result.
Prior ordinary/server closes fail closed. An unfinished dispatcher callback
repeat returns an explicit error without blocking acknowledgment delivery or
poisoning the first owner. Completed repeats return the retained result. The
ordinary Close API keeps its early-nil compatibility. The upstream manifest,
patch notes, explicit dependency tests and packaging checks record the subset
and changed-file contract.

The final direct source inventory is
candidate v7 (`evidence/apexload-native-har-ownership-candidate-source-v7-20261005.json`).
Its 135 direct inputs cover owned sources/tests, the SDK subset, module metadata
and relevant packaging/reference files. It is not a full transitive build or
native runtime inventory. The v6 source inventory preserves the exact behavior
qualification basis; its draft and older source freezes remain historical.

The first docs check found twelve broken relative links in the pristine upstream
README, whose referenced examples were intentionally omitted. V7 preserves those
exact bytes as README.upstream.txt with an explicit original-path mapping in the
80-file upstream manifest, adds a concise local README and updates the integrity
test and patch note. The docs checker is unchanged and skips no new paths. Normal
and race packaging checks pass three times, and the docs check passes. Relative
to the 134-input v6 direct inventory, v7 retains 130 hashes, updates
four documentation/provenance/integrity-test paths and adds one exact upstream
README copy. Every SDK/APEX production Go, native and module input retains
its qualified v6 bytes. Native/API behavior gates were not repeated for this
documentation and integrity-test-only delta.

Final scoped checks are:

- SDK owned-close contracts three times normally and three times under the race
  detector: `native-har-owned-sdk-contract-final-3` and
  `native-har-owned-sdk-contract-race-final-3`.
- Affected browser ownership, capture, retirement, context and document
  finality regressions twice normally and twice under the race detector:
  `native-har-v6-focused-final-1` and `native-har-v6-race-final-1`. Short mode
  explicitly skips eight native test invocations; it is not native acceptance.
- Required-mode actual Chromium/WebKit HAR ownership fixtures three times:
  `native-har-owned-sdk-all-native-final-1`. These cover successful flush,
  deadline-held exact ownership, explicit close failure, prior close rejection
  and a synchronous OnClose repeat. The separately owned callback fixture's
  first pass is also retained.
- The unchanged actual document identity/no-interaction oracle twenty times
  per engine: `native-har-owned-sdk-document-finality-required20-final-1`.
  All forty engine subtests pass without skips and report exact target counters
  of two root and four frame requests. No oracle thresholds or extra probe
  traffic were introduced.
- Controller retention regressions twice normally and once under the race
  detector: `native-har-api-retention-final-1` and
  `native-har-api-retention-race-final-1`. SDK packaging, recording workflow
  filters and pinned native-version checks pass three times normally and under
  race after the docs correction: `native-har-owned-sdk-packaging-final-3` and
  `native-har-owned-sdk-packaging-race-final-1`. The corrected repository docs
  check is `native-har-docs-final-3`; its failed predecessor remains preserved.

Each execution retains its exact command, elapsed time, exit code, raw-log SHA
and point-in-time repository source stability. Final Go executions freeze
4,114 repository Go sources plus root module inputs, excluding complete module
cache/native dependencies. Compact receipts retain inventory digests and the
original private receipt SHA; complete direct inventories and raw logs are
included. Fourteen direct installed runtime/SDK/module input bookends and local
module-resolution receipts remain separately scoped and unchanged across the
final native cohort.

Reviews v1 through v4 and the first SDK draft found actionable issues. Their
changes-required receipts, original source/overlay inputs, failed assertions
and superseded attempts remain preserved. They include joined callback and
detach barriers, Goexit false success, deletion recovery, bounded maintenance,
ledger plan mutation and replacement refusal, duplicate pre-rename completed
discovery, namespace replacement, private raw admission, size metadata and
strict JSON fields. The original pinned SDK's prior-close native reproduction
demonstrates why ordinary Close returning nil cannot certify a writer join.
The deterministic unfinished-dispatcher repeat and recording path-filter
regressions are recorded red before their fixes. Failed compile/fixture
attempts are explicitly distinguished from production assertion failures.

The corrected SDK independent repeat review (`evidence/apexload-har-sdk-corrected-authoring-independent-review-v6-20261005.json`)
bookends all 134 direct inputs and reviews 39 relevant paths on v6. The v7 packaging repeat
review (`evidence/apexload-har-sdk-readme-packaging-authoring-independent-review-v7-20261005.json`)
finds no additional issues in the five-path documentation/provenance delta
and bookends all 135 final direct inputs. It runs no tests;
its scoped static result is separate from the execution receipts. Parent v5
review retains its exact narrower scope. The parent composed HAR/document/resource
review (`evidence/apexload-har-document-resource-composition-independent-review-v1-20261005.json`)
finds no additional scoped issues after bookending 147 composed inputs. Its
composition deliberately preserves the integration native test’s aggregate
failure logging without changing assertions; it is separate from this worktree’s
execution inventory.
The neutral document finality correction was separately reviewed and qualified
in the integration worktree; the owned HAR tree preserves that correction.

All fixture HARs, bodies, cookies and URLs are invented loopback or example
content. The user's original capture and derived private body/header/URL
content are absent. No original-target replay, controller/API upload, native
export or archive-transfer acceptance is claimed.

The 2 GiB working admission check is point-in-time; aggregate active-writer or
RSS enforcement remains separate. Unknown legacy/crash writers are preserved.
The actual native evidence qualifies the installed macOS arm64 runtimes, not
Linux or Windows runtime delivery. Full BE-3, managed native archive
transfer/export and full program completion remain open. Shared build-cache
cleanup is deferred until the parent task's composed checks and fixes join.

Check history and failure dispositions (`evidence/check-history.json`).

<!-- Original record: docs/qa/reviews/feature-completion-second-har-evaluation-2026-10-05/README.md; local evidence kept outside git -->
<a id="feature-completion-second-har-evaluation-2026-10-05"></a>

## Second HAR: offline evaluation


The user-supplied `iPad_LrgBan_ViewServices_6Oct26Rel_V0.1.har` is a useful HTTP-import compatibility sample. The existing shared HAR importer retained all 435 requests with static assets explicitly included. Offline conversion produced no blocking diagnostics; this does not establish successful replay or validated correlations. No application traffic, managed run, browser or API upload was performed.

<a id="feature-completion-second-har-evaluation-2026-10-05--capture-and-conversion"></a>
### Capture and conversion

| Observation | Result |
|---|---|
| Original input | 88,221,596 bytes; HAR 1.2 |
| Input SHA-256 | `4a145fb5f253fe0bea37b5d45b77c6e10a3b3375dfd483992b2d2d5ad90a05e4` |
| Requests retained | 435 / 435, zero filtered or skipped |
| Response-content provenance | 425 complete text, 3 complete binary, 7 unavailable |
| Base64-marked source responses | 407 |
| Generated status assertions | 435; authoring output, not executed assertions |
| Automatic correlations / extractors | 0 / 0 |
| Diagnostics | 1,307 informational, 869 warnings, 0 blockers |
| Source response errors | No 4xx, 5xx or status-zero entries |
| Offline probe process | PASS, 10.882 seconds total elapsed; source file and in-memory buffer unchanged |

All requests use HTTPS and contain iPad user-agent evidence. Most captured responses are JSON (394), with HTML, JavaScript, CSS, image and other content also present. These facts identify the captured request workload; they do not establish device rendering or successful user journeys.

<a id="feature-completion-second-har-evaluation-2026-10-05--evidence-limits"></a>
### Evidence limits

Every request and response reports the literal HTTP version `unknown`. The importer records unavailable-version quality and preserves its inherited execution policy; the capture cannot qualify HTTP/2 behavior.

Connection timing is invalid negative data on 420 requests. Send and wait timing is unavailable on 72 requests each. These values remain source quality gaps, not measured zero durations. DNS, SSL and blocked phases also contain negative values; their validity was not independently qualified by this evaluation. Timing from this capture must not be treated as a complete transport breakdown.

There are 404 page records and 404 distinct referenced page identities, but every page lacks `onContentLoad` and `onLoad`. No native resource-type or initiator fields or WebSocket entries were observed. Page references alone do not prove 404 navigations and cannot supply web vitals, native browser waterfalls, interaction latency or dependency causality.

Zero detected automatic correlations is a bounded importer result, not evidence that the flow is stateless. A replay draft still needs review of session values, dynamic identifiers and request dependencies before replay validation.

The capture contains credentials and cookies: 433 requests have cookies and 144 have authorization headers. The original capture, endpoints, header values, request/response bodies, private observations and generated executable script were not copied into this bundle or Git. Only counts, fixed classifications, file identity and helper source are retained.

<a id="feature-completion-second-har-evaluation-2026-10-05--reproducibility-and-scope"></a>
### Reproducibility and scope

The aggregate shape (`evidence/aggregate-shape.json`), quality inspection (`evidence/aggregate-quality.json`), conversion receipt (`evidence/offline-probe-1.json`) and aggregate-only raw output (`evidence/offline-probe-1.log`) preserve this evaluation. The Go helper (`helpers/offline-probe.go.txt`) uses the existing shared HAR parser and converter with a 100 MiB input bound, pins the original input hash and installs rejecting default HTTP transports. It recorded zero HTTP attempts. The runner (`helpers/offline-probe-runner.py.txt`) imposes a 90-second process deadline and records eleven directly inspected source/module hashes before and after; all remained unchanged. Its 10.882-second elapsed time includes Go compilation/startup, file reads and hashing, observation parsing, conversion and the source recheck; isolated conversion duration was not measured. That inventory is not a full transitive dependency inventory or a network-sandbox attestation.

The converter and its synthetic regression tests were previously qualified in the [HAR import compatibility slice](BROWSER_2026-10.md#feature-completion-har-import-compatibility-2026-10-05). This evaluation makes no source-code change, raises no API/capture limit, copies no private capture content into a fixture, and does not qualify API persistence, native HAR export, browser-runtime replay, source-clock alignment or the broader feature program.

<!-- Original record: docs/qa/reviews/roadmap-browser-authored-identity-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-authored-identity-2026-10-20"></a>

## Browser authored identity foundation — 2026-10-20


The engine binds browser actions to their exact serialized script and canonical structural DSL location before per-VUser interpolation. Duplicate display names remain distinct. Normal load, initialization/teardown and replay use the same versioned identity; loop iterations, retries and replay ordering do not change it. A changed authored script changes the digest and every dependent step identity. Open-workload iteration copies and observer-identity interpolation retain the original authored script binding; structurally changed copies remain unavailable.

<a id="roadmap-browser-authored-identity-2026-10-20--contract-and-scope"></a>
### Contract and scope

BrowserAuthoredStep version 1 contains the script SHA-256, structural path and a domain-separated SHA-256 step ID. Paths contain only lifecycle/control-flow tokens and numeric positions, never names, URLs, selectors or source labels. The protocol action's binding is engine-owned and excluded from JSON, so imported action JSON cannot supply it. Unknown/detached locations, aliased action trees, unsupported serialization and invalid structural bounds remain unavailable; construction cannot publish a partial index.

The immutable per-VUser index uses the existing DSL walker. The walker now checks roots and queued branches against the shared definition budget before allocating their pending paths, including queued siblings. This retains traversal order and the existing definition/depth limits.

This is the engine foundation only. Existing interaction/document records, archive formats, feature negotiation, Help and dashboard assets are unchanged. Authored page identity, retained step identity, actual dispatch timing, document/lifecycle joins and compatible baseline/policy workflows still require their subsequent slices. Historical records receive no invented identity. Local tests do not qualify external hosts, actual devices or source-export fidelity.

<a id="roadmap-browser-authored-identity-2026-10-20--validation-and-review"></a>
### Validation and review

The final full `go test -timeout=20m ./...` run passed with the private HAR import overlay and required native browser tests. Targeted race tests passed. Documentation checks passed for 297 active Markdown files; all 31 Help unit tests passed, and four embedded Help sources matched byte for byte. Browser Help workflows passed in both profiles: 13 dev-admin tests (one auth-specific skip) and 14 local-auth tests.

The supplied HAR retains 803 requests, 476 converted actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings and zero DSL validation issues. All stable compatibility fields match the previous slice. No private target traffic was replayed or private capture content committed.

Self-review fixed pre-allocation walker bounds, original-script binding across runtime copies and the executable-field coverage guard. A final review found no further issues. The earlier failed full run is retained alongside the corrected final run; it is not counted as passing evidence. See the validation summary (`evidence/validation-summary.json`), review rounds (`evidence/review-rounds.json`), tested source hashes (`evidence/tested-source-hashes-final.json`) and HAR comparison (`evidence/private-har-comparison-final.json`). Qualification remains limited to the local macOS/arm64 environment and the scope above.

<!-- Original record: docs/qa/reviews/roadmap-browser-authored-pages-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-authored-pages-2026-10-21"></a>

## Authored navigation page receipts — 2026-10-21


A separate versioned protocol-evidence receipt joins a supported original Chromium/WebKit navigation/reload/new-window definition to the actual native response and observed document/lifecycle. Already published document metrics remain immutable. Repeated original steps share a page definition ID and retain distinct runtime document IDs. Duplicate names remain distinct structural steps. History/SPA/click-created popup and other unproved cases remain unavailable; this does not qualify equivalent runtime state or baselines.

The archive report validates original-source provenance and retains at most 1,024 distinct bindings. Conflicting request, page or proof version/sequence removes the identity, duplicates do not add documents, and malformed/source-mismatched/dropped receipts remain visible. The dashboard displays up to 256 bindings and exports all retained bindings to CSV.

<a id="roadmap-browser-authored-pages-2026-10-21--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,852 passing tests/subtests**, 38 explicit skips, zero failures, with native browser tests required. Private HAR import stable results match the prior baseline.
- Focused race coverage: **120 passing tests/subtests** over three repetitions, including native Chromium/WebKit desktop and mobile-emulation archive chains.
- Dashboard typecheck, lint, targeted units, generated contracts, embedded build, exact Help-source verification and both browser authentication profiles passed. Help screenshots were inspected; documentation links passed.
- The report/CSV workflow exposes original page/step provenance and native document identity with explicit missingness, conflict and truncation. Version 1 preserves CDP joins; version 2 requires an unchanged exact native navigation request/sequence around a bounded document identity read. Conflicting proof versions/sequences are excluded. Initial intermittent redirect join failure and corrected reruns remain in the local evidence. Actual managed native chains preserve replay/run page definition equivalence, distinct runtime document IDs and unchanged target counters. The separate SDK timing-race fix is committed before this slice. Applied visual review found and removed an unsupported SVG export control from the receipt-only view. A regression assertion, typecheck/lint/units, rebuilt assets, both native browser authentication profiles and embedded/generated API checks pass after that UI-only fix; the full Go suite preceded it. Initial and corrected source/asset hashes and screenshots are retained. Final applied review found no remaining slice findings. Local records include `evidence/validation-summary.json`, source/asset hashes, native screenshots and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. No actual-device or external platform qualification is claimed. Broader authored outcomes, compatible baselines, replay durability, provisional live findings and external qualification remain open.

<!-- Original record: docs/qa/reviews/roadmap-browser-baseline-cadence-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-baseline-cadence-2026-10-21"></a>

## Browser baseline cadence qualification — 2026-10-21


This BI-A slice refuses to score browser document baselines whose interval bucket durations differ or are missing/nonpositive. Snapshot observations cannot substitute for interval bucket p75 values. A matching series ID is also checked against metric, unit, measurement family/group and actual runtime-cohort fields before it can qualify a comparison.

The regression tests first reproduced anomaly verdicts for incompatible cadence and for different tuples sharing an ID. Corrected results are unscored with `cadence_changed`, `cadence_unavailable` or the existing `cohort_changed` reason; they expose no statistical range, z-score or baseline evidence link. Equal-cadence inputs with unequal document counts retain the existing median-of-bucket-p75 calculation and equal bucket weighting. The `browser_baseline.v1` estimator, 30-bucket minimum, three-sigma rule and effect floors are unchanged.

Review found that changed eligibility must also change the RCA engine identity. The engine generation advances to v4 so newly requested assessments do not reuse v3 results. A worker refuses a queued analysis whose frozen engine identity differs from its current engine, including an unrecorded identity. Completed historical assessments remain readable and their stored payloads remain unchanged. Regression coverage exercises both historical reads and old queued jobs, followed by admission under the current engine.

Raw evidence uses the `baseline-cadence-` prefix under `/private/tmp/apex-browser-durable-backend/`. The initial failing regression log is retained alongside passing RCA race results. The final targeted RCA/API race gate passes (RCA 4.094 seconds; API 108.826 seconds), including all `TestRCA*` coverage, browser baseline cases, policy-floor propagation and engine-version tests. Documentation checks pass for 327 active Markdown files. A final review of the fixes, historical read path, queued-job fence, estimator compatibility and staged scope found no further issues, including low severity. The full repository gate passed at the preceding replay-omission commit; these subsequent qualification edits have targeted validation and still require the normal full CI merge gate. This slice does not activate `latest_compatible` selection or establish clock/source qualification, real two-run qualification or external browser benchmark acceptance.

<!-- Original record: docs/qa/reviews/roadmap-browser-baseline-qualification-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-browser-baseline-qualification-2026-10-19"></a>

## Roadmap implementation: browser baseline qualification guards


Date: 2026-10-19. Base: `86a9f5cdb`, with upstream `609bf1b1a` integrated. Scope: a bounded [BI-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#bi-a--qualified-baseline-selection-and-findings) correctness slice.

<a id="roadmap-browser-baseline-qualification-2026-10-19--contract"></a>
### Contract

A configured per-metric effect floor cannot lower the effective timing floor (the larger of the absolute floor and the baseline-relative floor) or CLS floor. Raised policy floors remain effective. Explicit nonpositive/non-finite per-metric floors and overflowing calculations cannot qualify a comparison; other units require a positive catalog/configured floor.

A known loss of document observations in either cohort leaves the comparison unscored with `source_loss` or `baseline_source_loss`, and the section partial. Current/baseline bucket and sample counts, current evidence and baseline identity remain available; an expected range, z-score or anomaly direction is not emitted for that comparison. Both loss caveats remain when both cohorts lost observations. This prevents a small observed delta from claiming a healthy comparison just as it prevents a large delta from claiming a qualified regression.

The `browser_baseline.v1` median-of-bucket-p75 estimator, equal bucket weighting, default 30-bucket minimum, three-sigma threshold, scope/load matching, advisory-only behavior and stored historical results are unchanged. This does not implement per-metric policy editing, `latest_compatible` selection, stable authored identity, missing cadence qualifications or the real two-run browser gate.

<a id="roadmap-browser-baseline-qualification-2026-10-19--validation-and-review"></a>
### Validation and review

Draft overlays reproduce the lower-floor and known-loss scoring defects against the original source. The complete RCA package passes with the draft. The applied-source full RCA package passed (0.238 s), focused race tests passed (1.147 s), API baseline/policy/evidence compatibility checks passed (1.918 s), and the generated acceptance inventory passed (1.234 s). The 258-file documentation check, help ownership/fragment check and whitespace check passed. The final full Go suite passed: 168 tested packages and 10 skipped/no-test packages, with no failures. The final scoped self-review found no further issues. The full trace (`evidence/baseline-full-go.jsonl.gz`), package outcomes (`evidence/baseline-full-go-summary.json`), exact commands, source hashes and focused logs are retained; SHA256SUMS covers the review evidence. Raw failures are retained; local deterministic series are not real-browser qualification.

<!-- Original record: docs/qa/reviews/roadmap-browser-benchmark-units-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-benchmark-units-2026-10-20"></a>

## Browser benchmark memory units — 2026-10-20


This [BE-C slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#be-c--publish-measured-browser-limits) makes the existing Chromium target of less than 250 MB explicit as 250,000,000 bytes. It does not relax it to 250 MiB or raise the supported density. Five-repetition and reference-host requirements remain unchanged.

New `apexload.browser_benchmark.v2` artifacts retain raw byte values, nullable missing measurements, the observed process population, attempted snapshot count and partial/unavailable sample counts. Host capacity has byte and explicitly named MiB fields. Historical v1 artifacts remain unchanged; their `peak_rss_mb` and `memory_mb` values were binary MiB. The Go RSS benchmark similarly reports bytes and MiB and refuses unavailable snapshots instead of reporting zero.

The observed process population includes shared driver/browser costs. A sampled maximum is not a continuously measured peak, complete upper bound or independent per-VUser attribution. Concurrent sessions, process enumeration and partial reads need independent accounting before performance qualification. The [owning guide](../../PERFORMANCE_BENCHMARKS.md#memory-units-and-measured-population) defines these limits.

<a id="roadmap-browser-benchmark-units-2026-10-20--validation-and-review"></a>
### Validation and review

- Applied API and generated-contract checks: 7 top-level tests and 0 subtests passed.
- Applied native harness/units: 7 top-level tests and 15 subtests passed; one local benchmark iteration per declared Chromium profile verifies execution, not reference-host performance.
- Applied units race suite: 4 top-level tests and 15 subtests passed.
- Full Go regression: 168 packages, 17,677 top-level tests and 17,368 subtests passed, including the actual private ZIP import through the test-only overlay.
- The private ZIP retained its original hash and matching aggregate import result: HTTP 200, 803 requests, 476 actions and zero DSL issues. No traffic replay or converted-content persistence.
- API/TypeScript generation, inventory, dashboard typecheck/lint/Help units/build, both Help profiles, documentation checks and evidence hashes passed. Exact source/asset/controller cohorts and the distinct combined RSS/joint draft are retained in the evidence bundle (`SHA256SUMS`).

Review corrected the API and owner guide's stale CDP-attribution label to the actual shared Playwright driver OS sampler and added an explicit denominator for partial/unavailable RSS sample counts. Final applied-source and staged review: no remaining actionable findings after the recorded unit, sample-denominator and process-population corrections.

One local iteration per Chromium profile verifies that the harness runs with the new units; it is not a five-run reference-host acceptance result. Linux/Windows/native reference and density qualifications remain pending, and no acceptance record is promoted.

<!-- Original record: docs/qa/reviews/roadmap-browser-bucket-grid-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-bucket-grid-2026-10-21"></a>

## Browser baseline bucket timestamps — 2026-10-21


<a id="roadmap-browser-bucket-grid-2026-10-21--scope"></a>
### Scope

Browser baseline comparison now requires unique, nonzero retained bucket timestamps
on each series' declared positive cadence. Duplicate publications cannot manufacture
the thirty-bucket minimum, and off-grid timestamps cannot silently receive equal
bucket weight. Both current and baseline series must qualify; invalid grids produce
an unscored `bucket_grid_invalid` reason with no z-score or expected range.

Whole missing buckets remain missing. Unordered input is sorted without changing
caller-owned evidence, and separate runs can retain independent time-grid origins.
The check rejects duration-overflow timestamp ranges rather than treating a saturated
`time.Duration` as evidence of a regular cadence.

The estimator remains `browser_baseline.v1`: median of current bucket p75 values
against baseline buckets at the same load. Engine identity advances to v5 so that
new requests cannot reuse an older assessment under the new qualification rules;
completed historical assessments remain readable and unchanged.

This is a retained-series qualification guard. It does not establish synchronization
of producer clocks, source-window completeness, compatibility of load windows, or
real browser-cohort qualification. The archive adapter still constructs its grid
from retained interval summaries. Latest-compatible selection and the broader BI-A
clock/source-window acceptance gates remain open.

<a id="roadmap-browser-bucket-grid-2026-10-21--validation-and-review"></a>
### Validation and review

- `go test ./pkg/rca -timeout=5m` passed in 0.347 seconds.
- `go test -race ./pkg/rca -timeout=5m` passed in 1.713 seconds.
- The full `go test -overlay=<private HAR overlay> -json -timeout=20m ./...`
  passed: 168 packages passed, 10 skipped, zero failures. The API package passed
  in 877.533 seconds, including historical-assessment compatibility. Latest main
  was fetched and already integrated before the run; packages ran serially.
- The attached private HAR imported successfully in 39.660 seconds, with 803 requests,
  476 actions and zero DSL validation issues. The private archive is not committed
  and was not replayed.
- Documentation checks passed for 331 active Markdown files; whitespace checks passed.
- Two independent agents reviewed code, tests and the qualification boundary with
  no findings. The final precommit review remained clean.

Raw logs and the private import summary are under
`/private/tmp/apex-browser-durable-backend/`: `bucket-grid-targeted.log`,
`bucket-grid-race.log`, `bucket-grid-full.jsonl` and `bucket-grid-har-summary.json`.
No UI behavior or browser protocol changed; these checks do not claim new live
browser or infrastructure qualification.

<!-- Original record: docs/qa/reviews/roadmap-browser-cdp-close-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-cdp-close-2026-10-21"></a>

## HAR publication after a concurrent CDP detach — 2026-10-21


The full browser metric-floor validation exposed a native Chromium managed-run failure: context close completed while diagnostic CDP detachment returned `No session with given id`. Treating that completed diagnostic call as an unjoined writer prevented native HAR publication.

CDP detach remains joined before retirement. Its returned error does not invalidate the independent successful `CloseHAROwned` receipt. A blocked, panicking or abandoned runtime join still retains ownership; a failed native close still prevents publication and preserves the raw generation. No unknown writer is reclaimed and the shared finalization deadline is unchanged.

<a id="roadmap-browser-cdp-close-2026-10-21--validation-and-review"></a>
### Validation and review

- Focused race validation: **140 passing tests/subtests** over ten repetitions, including four real desktop/mobile-emulation managed replay/run cohorts per repetition.
- Deterministic regressions verify completed detach errors with a joined writer, failed writer-close retention, blocked detach deadlines and abandoned runtime joins.
- Full Go suite with native browser tests required: **35,729 pass**, 38 explicit skips, zero failures. The supplied private HAR import compatibility check passed in that suite.
- Final self-review found no remaining issues. Local evidence includes `evidence/review-rounds.json`, `evidence/validation-summary.json`, tested source hashes and `evidence/SHA256SUMS`.

The first failed full run was stopped after its native workflow failure was diagnosed; it is not presented as a complete suite. The first failed suite is retained locally as `evidence/failed-full-go.jsonl.gz`; it is diagnostic evidence, not an accepted run. Only this Markdown record is committed.

<!-- Original record: docs/qa/reviews/roadmap-browser-cross-commit-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-cross-commit-2026-10-21"></a>

## Browser stack cross-commit review — 2026-10-21


Scope: the completed browser stack in draft PRs #261–#263, reviewed on source commit `6f826fc7e` against main `d639a646f`, including interactions with the earlier browser/HAR foundations already on main. This review does not close unfinished roadmap features or external qualification gates.

<a id="roadmap-browser-cross-commit-2026-10-21--findings-and-repair"></a>
### Findings and repair

The review found an unbounded keyboard-target aggregation table. Returning only the top 64 targets bounded the response, but did not bound the table accumulated while reading retained archive records. A failing regression reproduced 1,034 retained keys where the new limit is 1,024.

Each of at most 64 heatmaps now retains at most 1,024 distinct keyboard targets. Further gestures for existing retained targets continue to count exactly. Gestures for new targets beyond the limit are counted under `keyboard_targets_truncated`; map notes disclose that the displayed top 64 are selected from the retained targets. Pointer cells and other map cohorts remain independent. Raw archive evidence, interaction wire versions, pattern sampling and capture-loss accounting are unchanged.

The regression covers the aggregation boundary before output truncation, exact repeat counts, independent map cohorts, pointer cells, coverage reconciliation and visible scope notes. Existing dashboard rendering exposes map notes and generic exclusion reasons; report views expose exclusion counts.

<a id="roadmap-browser-cross-commit-2026-10-21--review-rounds"></a>
### Review rounds

1. Reviewed original-source and authored-step identity, negotiated dispatch/outcome versions, cohort and lifecycle joins, acceptance dependencies, policy selection and immutable pins, tuple panel limits, replay receipts and ownership, shutdown joins, SDK navigation/timing synchronization, HAR omission retention, and consumer disclosure. Earlier panel repairs remain preserved. Found the keyboard aggregation bound issue described above.
2. Reviewed the repair and its callers, including exact retained counts versus excluded gestures, old-reader compatibility, report/dashboard disclosure, policy/create flows and clone boundaries. No additional findings.

3. Final review of the scoped diff, regression, unchanged consumer rendering and validation record found no further issues, including low-severity findings.

<a id="roadmap-browser-cross-commit-2026-10-21--validation"></a>
### Validation

- Focused heatmap, cohort, qualified-interaction and report race tests: 87 passing test/subtest events across three repetitions.
- Full API and browser package suites: 13465 passing test/subtest events and 24 conditional skips, with both packages passing. Skips do not establish native/provider qualification.
- Cross-package browser/replay/policy race checks across protocol, engine, storage, RCA, generator RPC and record bridge: 550 passing test/subtest events.
- Targeted API shutdown/replay-ownership race checks: 10 passing test/subtest events.
- Private HAR import: all stable summary fields match the retained baseline; randomized script identity is excluded. The ZIP is not committed and its requests were not replayed.
- Documentation checks and whitespace checks pass; all 311 previously tested embedded asset hashes match. No frontend source or generated asset change is included in this repair.

Main was fetched and confirmed integrated before the full API suite. This incremental API-only repair uses the earlier full-repository and browser workflow qualification records as historical evidence; it does not claim another full `./...` or UI cycle.

<a id="roadmap-browser-cross-commit-2026-10-21--evidence-and-limits"></a>
### Evidence and limits

Raw local logs are under `/private/tmp/apex-browser-cross-commit-review/`: `heatmap-before.log` preserves the expected failing regression; `heatmap-after.jsonl`, `final-tests.jsonl`, `final-race.jsonl`, `recovery-race.jsonl` and `docs.log` preserve validation. Raw evidence is not committed.

The first broader race invocation was interrupted and is not claimed as a completed gate. Fresh completed runs supersede it. Previously tested embedded dashboard assets are unchanged (311 checked hashes); this API-only repair does not claim fresh native-browser or UI qualification. The private HAR is imported only, never replayed.

Windows native-worker CI investigation and exact PR-head CI remain separate release gates. No ownership or cleanup fence was weakened to bypass an unconfirmed CI failure. Separate-host performance, native release lanes, real TruClient exports and deployed recovery qualification remain pending as recorded in the [roadmap](../../ROADMAP.md).

<!-- Original record: docs/qa/reviews/roadmap-browser-dispatch-v2-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-dispatch-v2-2026-10-20"></a>

## Native browser dispatch and qualified interaction analysis — 2026-10-20


Raw interaction v2 retains engine-authored structural step identity, logical invocation/attempt identity, and independently observed native input clocks and geometry joined to a document lifecycle. Post-action capture time remains separate. Traffic-free capability negotiation preserves legacy v1 output for old assignments and refuses unsupported v2 before commit.

V3 pattern analysis uses qualified step/dispatch populations and keeps intended retries separate. Missing dispatch, authored binding, execution or known engine/profile remains excluded. Historical-only archives keep their existing algorithm; mixed archives preserve v1 rows and separate historical maps. Canceled, closed, invalid or unsupported waits cannot become missed target deadlines. Raw records, exact transfer receipts, report exports, the dashboard and embedded Help preserve these distinctions.

This slice does not close authored page identity, genuine BFCache, all native crash platforms, durable replay HAR ownership, broader declared assertion/dialog/download outcomes, baseline/policy/panel/live workflows or external qualification. Synthetic touch and untransformed child frames remain dispatch-unavailable. Local Chromium and WebKit are not actual-device qualification, reference-host density evidence or real-export fidelity.

<a id="roadmap-browser-dispatch-v2-2026-10-20--validation-and-review"></a>
### Validation and review

- Corrected full Go suite: **35,655 tests/subtests passed, 38 skipped**, with native browser tests required. The single stale capability expectation from the preceding run was corrected; that failed run and the earlier intentionally interrupted run remain in the evidence and are not passing qualifications.
- Native race checks: **57 passed**; generator negotiation race checks: **7 passed**; final targeted API checks: **17 passed**.
- Dashboard typecheck, lint and 39 focused unit tests passed. Browser intelligence and Help workflows passed in dev-admin (**16 passed, one auth-only skip**) and local-auth (**17 passed**). These UI workflows use transport fixtures; they do not establish the real managed-run chain.
- Documentation checks passed for 298 active Markdown files. Four embedded Help modules match their Markdown sources byte for byte. The reviewed build's asset digests are retained.
- The user's private capture imported successfully: 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings and zero DSL issues. All stable compatibility fields match the preceding slice. The original ZIP is unchanged; its target traffic was never replayed and its contents are not committed.
- Eleven self-review rounds cover transport negotiation, identity and clock attribution, bounds, finality, retries, historical records, authorization-facing reports and UI/Help disclosure. Every finding is fixed; the final round is clean.

Validation summary (`evidence/validation-summary.json`), review rounds (`evidence/review-rounds.json`), source hashes (`evidence/tested-source-hashes.json`), asset hashes (`evidence/tested-asset-hashes.json`), and checksums (`evidence/SHA256SUMS`) retain the reviewable record. The final comment/prose-only corrections have exact before/after replacements and hashes (`evidence/final-prose-review.json`); targeted checks passed after that non-executable delta.

The full run used `go test -json -timeout=20m ./...` with the retained private-HAR overlay and `APEXLOAD_BROWSER_TESTS=require`. Logs are compressed without changing their contents. Browser profile artifacts and the explicit local runtime/version evidence remain separate from any release acceptance record.

<!-- Original record: docs/qa/reviews/roadmap-browser-distributed-clocks-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-distributed-clocks-2026-10-26"></a>

## Browser distributed RPC causal brackets and multi-generator steady-load reconciliation (BI-A1)


This slice implements periodic controller↔generator RPC causal bracket acquisition, bounded crystal-drift projection into the controller monotonic clock domain, and multi-generator steady-load span intersection for [BI-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#bi-a--qualified-baseline-selection-and-findings).

<a id="roadmap-browser-distributed-clocks-2026-10-26--scope-and-design"></a>
### Scope and design

1. **Controller monotonic clock domain and envelope (`pkg/protocol/browser_clock_mapping.go`):**
   - `NewBrowserClockDomain(runID string)` creates the controller's run-scoped monotonic clock authority domain.
   - `BrowserClockBracket` records `[ControllerSendNS, ControllerRecvNS]` in the controller's monotonic domain alongside `OwnerMonoNS` in the generator's `BrowserSourceJournal` monotonic domain (`time.Since(j.start)`), `Authority`, and `OwnerID`.
   - `BrowserDistributedClockEnvelope` retains bounded per-owner brackets (`MaxBrowserClockBracketsPerOwner = 1024`), `MaxBracketRTTNS` (`DefaultBrowserClockMaxBracketRTTNS = int64(time.Second)`, 1 second), and `MaxBracketGapNS` (`DefaultBrowserClockMaxBracketGapNS = int64(45 * time.Second)`, 45 seconds).
   - `browserClockSkewBoundsNS` applies the conservative 200 ppm relative crystal-skew bound (`q*maxSkewPPM + (rem*maxSkewPPM+999_999)/1_000_000` with `addInt64Checked` overflow guards) when projecting any generator monotonic reading `ownerReadingNS` through every bracket `[ControllerSendNS, ControllerRecvNS]`:
     - Lower bound at bracket $b$: `b.ControllerSendNS + (ownerReadingNS - b.OwnerMonoNS) - skew(|ownerReadingNS - b.OwnerMonoNS|)`
     - Upper bound at bracket $b$: `b.ControllerRecvNS + (ownerReadingNS - b.OwnerMonoNS) + skew(|ownerReadingNS - b.OwnerMonoNS|)`
   - Taking the tightest intersection (`max` of lowers, `min` of uppers) across all brackets for that owner produces a conservative `[lowNS, highNS]` interval in the controller's monotonic domain without ever trusting generator wall-clock timestamps (`time.Now()` UTC) or dividing `RTT / 2`.

2. **Generator journal bracket sampling and RPC metadata transport (`pkg/protocol/browser_source_ledger.go`, `pkg/distributed/generatorrpc/browser_clock_brackets.go`):**
   - `BrowserSourceJournal.SampleClockBracket()` returns `BrowserClockBracketSample{Authority, OwnerID, MonoNS}` (`time.Since(j.start)` clamped non-negative and monotonic via `lastAtNS`).
   - Generator RPC handlers attach `x-apexload-browser-clock-bracket` response/header metadata on:
     - `CommitWork` (sampled before `exec.commit` starts VUsers so the initial bracket precedes `slot_expected`, `context_expected`, and `"steady"` when `RampUp == 0`)
     - `Heartbeat` (sampled for all active tests on the generator during periodic heartbeats)
     - `StopTest` (sampled after `exec.gracefulStop()` / `exec.stop()` completes so the closing bracket follows all generator activity)
     - `GetArchiveArtifact` and `StreamArchiveArtifact` (sampled during archive collection after test execution completes)
   - `generatorrpc.Client` brackets each RPC with `sendAt := time.Now()` and `recvAt := time.Now()`, pairs the observed generator sample with `[sendAt, recvAt]`, and exposes `Client.BrowserClockBrackets(testID)`.

3. **Zero-ramp steady phase ordering (`pkg/engine/group.go`):**
   - When `rampDelay == 0`, `MultiGroupEngine` now activates the prepared closed-group VUser batch via `activatePreparedVUserBatchWithAdmission`, marking all group VUsers running and calling `tryFinishClosedInitialRampLocked()` under `mge.mu.Lock()` before spawning the VUser goroutines (`spawnPreparedVUser`). This guarantees the `"steady"` phase event is written to `BrowserSourceJournal` before any zero-ramp VUser executes its first action or opens a browser context.

4. **Multi-generator steady-load reconciliation and RCA qualification (`pkg/protocol/browser_source_reconcile.go`, `pkg/api/rca_browser_source_proof.go`, `pkg/api/rca_browser_documents.go`, `pkg/rca/browser_qualification.go`):**
   - `BrowserSourceReconciler.ValidateDistributedClocks()` verifies that every expected remote owner has at least two brackets covering `[firstActivityNS, lastActivityNS]` (`browser_clock_brackets_unbounded`), no bracket exceeds `MaxBracketRTTNS` (`browser_clock_bracket_rtt_exceeded`), no adjacent bracket gap exceeds `MaxBracketGapNS` (`browser_clock_bracket_gap_exceeded`), brackets are pairwise drift-cone consistent (`browser_clock_brackets_inconsistent`), and all owners project into a single controller `BrowserClockDomain`.
   - `distributedSteadySpans()` projects each owner's `[startNS, endNS]` steady span conservatively into the controller domain (`[startHighNS, endLowNS]`, inner certainty bound where the generator is guaranteed to be in steady state) and intersects overlapping steady spans across all expected owners, summing `VUsers` across the participating generators.
   - `BrowserSourceReconciler.MapDistributed(o BrowserDocumentObservation)` projects each document's `[BeforeNS, AfterNS]` envelope into the controller's `BrowserClockDomain` so `protocol.ContainedInBrowserSteadySpan` and `rca.DetectBrowserBaseline` verify that every contributing bucket's `[WindowStart, WindowEnd]` lies within a single fleet-wide intersected steady-load span and `clockUncertainty <= bucket` (`observation_window_exceeds_bucket`).
   - Archive manifests persist `BrowserProofAuthorityMode = "distributed_envelope"` and `BrowserProofClockEnvelope`; `rcaArchiveAdapterVersion` is bumped to `"archive.11"`.

<a id="roadmap-browser-distributed-clocks-2026-10-26--verification-and-mutation-testing"></a>
### Verification and mutation testing

- `go test ./pkg/protocol -run 'TestBrowserSource' -count=1`: passed (`TestBrowserSourceDistributedClockBracketsAndMultiGeneratorReconciliation` covering 2-generator bracket reconciliation, 10-minute generator UTC wall-clock skew immunity, >1s RTT rejection `browser_clock_bracket_rtt_exceeded`, gap rejection `browser_clock_bracket_gap_exceeded`, unbounded activity rejection `browser_clock_brackets_unbounded`, inconsistent drift-cone rejection `browser_clock_brackets_inconsistent`, and conservative steady-span intersection).
- `go test ./pkg/engine -run 'TestBrowserSource' -count=1`: passed (`TestBrowserSourceSteadyPhasePrecedesZeroRampVUserExecution` verifying `"steady"` precedes VUser execution when `RampUp == 0`).
- `go test ./pkg/distributed/generatorrpc -run 'TestBrowserDocument|TestArchive' -count=1`: passed.
- `go test ./pkg/rca/... -count=1`: passed.
- `go test ./pkg/api -run 'TestRCABrowser|TestRCADistributedTwoGenerator|TestDistributedBrowserClockBrackets|TestBrowserSource' -count=1`: passed (`TestRCADistributedTwoGeneratorBrowserClockQualificationAndRTTRefusal` and `TestDistributedBrowserClockBracketsRPCTransport`).
- Mutation verification via `go test -overlay`:
  - Mutant 1 (bypass `b.ControllerRecvNS-b.ControllerSendNS > maxRTT` check in `pkg/protocol/browser_source_reconcile.go`): killed by `TestBrowserSourceDistributedClockBracketsAndMultiGeneratorReconciliation` and `TestRCADistributedTwoGeneratorBrowserClockQualificationAndRTTRefusal`.
  - Mutant 2 (use union `min(a.StartNS, b.StartNS)/max(a.EndNS, b.EndNS)` instead of conservative intersection `max(a.StartNS, b.StartNS)/min(a.EndNS, b.EndNS)` in `distributedSteadySpans`): killed by `TestBrowserSourceDistributedClockBracketsAndMultiGeneratorReconciliation`.

<!-- Original record: docs/qa/reviews/roadmap-browser-durable-namespace-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-durable-namespace-2026-10-21"></a>

## Durable replay namespace in non-HA controllers — 2026-10-21


Storage-backed controllers now acquire replay session leases whenever their store supports the durable coordination contract, even when HA execution leadership is disabled. Classic replay, browser replay and script debugging share this admission path. This prevents a restarted single controller from reusing a session ID still protected by an artifact receipt or cleanup tombstone.

A failed durable acquisition does not fall back to local ownership. Existing local-only adapters without that coordination capability keep their local behavior; they do not gain durable artifact publication. HA still requires the capability. Terminal retention, renewal, owner-fenced release and bounded operation deadlines reuse the existing lease lifecycle.

Cancellation after acquisition releases both the durable lease and the pending local entry. Non-HA cancellation reports its cancellation error rather than incorrectly reporting leadership loss. A lost renewal fences the active browser replay. An unjoined artifact writer remains reserved after process lease pruning; cleanup releases the namespace only after exact-generation physical removal is confirmed by the adapter.

<a id="roadmap-browser-durable-namespace-2026-10-21--validation-and-review"></a>
### Validation and review

Go 1.26.10 full suite passed: 35,938 tests/subtests, 38 explicit skips and no failures. Targeted race validation passed across three repetitions (63 assertions, no skips). The original private HAR import stable fields match the previous baseline. Tested source, base commit and embedded dashboard assets were verified unchanged. Documentation checks passed for 316 active Markdown files. Tests cover cross-controller ownership and stale release, retained IDs, unjoined and published receipt protection after process pruning, cleanup tombstones, failed database admission, cancellation after acquisition and renewal-loss termination. Existing HA and stopped-worker lifecycle checks are included in targeted repeated race validation. Raw logs remain local under `/private/tmp/apex-browser-durable-replay/`.

Review fixed the non-HA cancellation error classification and added a regression for both local and durable release. A second review of the final ownership transitions and tests found no further issues.

<a id="roadmap-browser-durable-namespace-2026-10-21--remaining-integration"></a>
### Remaining integration

This slice protects the durable namespace; it does not publish HAR/report bytes to an archive backend or enable recovered downloads. Backend publication, physical-writer proof, bounded cleanup and authenticated recovery reads remain open. Local macOS tests do not qualify multi-host HA or external storage failure lanes.

<!-- Original record: docs/qa/reviews/roadmap-browser-findings-v2-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-findings-v2-2026-10-27"></a>

## Evaluated browser baseline findings and RCA completion hook (BI-A3)


This slice upgrades browser intelligence findings from `browser_findings.v1` to `browser_findings.v2` (`apexload.browser_findings.v2`, `RuleVersion: 2`), replaces the unconditional `not_evaluated` placeholder on application-outcome findings with evaluated baseline statuses and immutable provenance links, and re-triggers browser findings evaluation when root-cause analysis completes in [BI-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#bi-a--qualified-baseline-selection-and-findings).

<a id="roadmap-browser-findings-v2-2026-10-27--contract-and-invariants"></a>
### Contract and invariants

1. **Evaluated baseline status and multi-load-level precedence (`pkg/api/browser_findings.go`, `pkg/rca/browser_baseline.go`, `pkg/api/rca_browser_documents.go`):**
   - `evaluateBrowserFindings` emits `RuleSet: "browser_findings.v2"` and `SchemaVersion: "apexload.browser_findings.v2"` (`RuleVersion: 2`) while preserving `browser_score.v1` (`Score.Version: "browser_score.v1"`).
   - Collection-health findings (`collection.absent`, `collection.incomplete`) and suppressed inapplicable cohort findings (`!finding.Applicable` when `mode != "none"`) report `Baseline.Status = "not_applicable"`.
   - Application-outcome findings (`threshold`) evaluate `BrowserFindingBaseline` against the run's completed RCA assessment when `payload.Inputs.Archive` matches `rcaEvidenceDigest(manifest)` and `rcaBrowserBaselineMatchesPolicy` confirms `PolicySHA256 == ref.SHA256`:
     - `regressed`: matched `rca.BrowserBaselineAnomaly` with `Direction != rca.DirectionDecrease` (including sub-threshold regressions where `severity == "good"`, emitted as `State: "active"`, `Severity: "needs_improvement"` while keeping absolute threshold scoring in `Score` distinct).
     - `within_baseline`: matched `rca.BrowserBaselineWithinRange` or `DirectionDecrease`.
     - `insufficient_evidence`: unscored baseline result or limitation with an insufficient-evidence reason (`insufficient_baseline`, `zero_baseline_variance`, `no_baseline_cohort`, `no_same_load_window`, `baseline_evidence_unavailable`, `run_evidence_incomplete`, `run_evidence_unavailable`, `levels_unavailable`, `no_effect_floor`, `outside_steady_load`, `results_truncated`, `metric_not_observed`, `no_qualified_final_observations`).
     - `unqualified`: unqualified baseline run (`Baseline.Qualification == BaselineUnqualified`), `current_unqualified` selection status, or qualification failure (`cohort_changed`, `browser_clock_unavailable`, `browser_source_coverage_unavailable`, `cadence_mismatch`, `source_script_changed`, `baseline_documents_incomplete`).
     - `not_selected`: `mode == "none"` (`policy_off`), `no_compatible_candidate`, or `no_baseline_run` (never reported as `within_baseline`).
     - `not_evaluated`: before RCA completes (`rca_not_completed`) or when the completed RCA's archive or policy digest does not match (`rca_archive_mismatch`, `rca_policy_mismatch`).
   - Across multiple load levels for a `(metric, cohort)` tuple, `evaluateFindingBaseline` selects the worst status deterministically (`regressed > unqualified > insufficient_evidence > within_baseline > not_selected > not_evaluated`), breaking ties by largest `|Z|`, lexicographic `Reason`, earliest `LevelStart`, and `Result.ID`.

2. **Immutable provenance links (`pkg/api/browser_findings.go`, `dashboard/src/components/runReport/BrowserFindingsPanel.tsx`, `cmd/apexctl/analysis_browser_findings.go`):**
   - Each application-outcome finding and `BrowserIntelligenceResult` carries `PolicyPins` (`browser_policy` and `journey_policy` revision/SHA-256 pins) and `BrowserFindingBaseline` provenance (`AnalysisID`, `SelectionReceiptSHA256`, `SelectionStatus`, `BaselineRunID`, `BaselineArchiveSHA256`, bounded `RejectedCandidates` excluding `self_baseline`).
   - `computeBrowserIntelligenceFingerprint` binds `rca_analysis_id`, `rca_revision`, `baseline_run_id`, `baseline_archive_sha256`, `selection_receipt_sha256`, `baseline_mismatch_reason`, and `policy_pins`.

3. **RCA completion re-trigger and monotonic job ordering (`pkg/api/rca_analyses.go`, `pkg/api/browser_findings_delivery.go`, `pkg/storage/feature_evaluation_jobs.go`):**
   - `runRCAAnalysis` invokes `s.autoQueueBrowserIntelligence(analysis.TestID)` after `CompleteRCAAnalysisFenced` commits a completed RCA assessment.
   - `autoQueueBrowserIntelligence` cancels superseded `Queued` system jobs for the same run before queuing the post-RCA evaluation job, and `runBrowserIntelligence` accepts `inputs.preRCAFingerprint` if a pre-RCA job was already `Running` when RCA completed.
   - `CreateEvaluationJob` enforces strictly monotonic `created_at` (`now = latestCreated.Time.Add(time.Microsecond)` when `!now.After(latestCreated.Time)`) under the project lock so back-to-back pre-RCA and post-RCA jobs sort newest-first in `ListEvaluationJobsForSubject` and suppress duplicate webhook transitions in `notifyBrowserFindingTransitions`.

<a id="roadmap-browser-findings-v2-2026-10-27--validation-and-mutation-testing"></a>
### Validation and mutation testing

- `cd dashboard && npm run typecheck && npx vitest run src/components/runReport/BrowserFindingsPanel.test.tsx src/labelCasing.test.ts && npm run build`: passed.
- `go test -race -count=2 -shuffle=on ./pkg/rca/... ./cmd/apexctl/... ./pkg/storage -run "TestEvaluationJobs|TestFeatureEvaluation"`: passed.
- `go test -race -count=2 -shuffle=on ./pkg/api -run "TestBrowserFindings|TestBrowserScore|TestBrowserCollection|TestBrowserIntelligence|TestRunBrowserFindings|TestRCACompletion|TestDetectBrowserBaseline|TestRCABrowser|TestSwaggerFreshness|TestOpenAPITSGeneratedFreshness"`: passed.
- Mutation verification via `go test -overlay`:
  - Mutant 1 (`case "regressed": return 0` -> `case "regressed": return 4` in `findingBaselineStatusRank`, `pkg/api/browser_findings.go`): killed by `TestBrowserFindingsV2EvaluatedBaselineStatusesAndPrecedence`.
  - Mutant 2 (`return payload.Assessment.BrowserBaseline.PolicySHA256 != "" && payload.Assessment.BrowserBaseline.PolicySHA256 == ref.SHA256` -> `return true` in `rcaBrowserBaselineMatchesPolicy`, `pkg/api/browser_findings.go`): killed by `TestRCACompletionReTriggersBrowserFindingsWithoutDuplicateWebhooks`.

<a id="roadmap-browser-findings-v2-2026-10-27--review-and-remaining"></a>
### Review and remaining

- Reviewed by code-safety and test/QA subagents to zero findings.
- Remaining in Track `BI-A`: the 95 s two-run seeded-regression production exit (`BI-A4`).

<!-- Original record: docs/qa/reviews/roadmap-browser-full-chain-e2e-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-full-chain-e2e-2026-10-27"></a>

## Roadmap Review — Full Real Browser Intelligence Chain in Both Auth Profiles (`BI-C5`)


**Date:** 2026-10-27
**Scope:** End-to-end Playwright proof of the real managed Browser Intelligence chain (`BI-1` through `BI-4`) across both `dev-admin` and `local-auth` profiles, browser and structural journey policy editors, two-run baseline comparison to saved `version: 2` (`query_version: 5`) panel, policy tombstone refusal (`422`), report exports (`HTML`, `CSV`, `PDF`, `DOCX`, and `browser-interactions?format=csv`), and `Viewer` RBAC / cross-project denial.

<a id="roadmap-browser-full-chain-e2e-2026-10-27--summary-of-changes"></a>
### Summary of Changes

1. **Structural Journey Policy Editor E2E (`BI-B3` / `BI-C5`):**
   - Added `journey policies pick structural step paths, save revisions, refuse stale edits and show past revisions` in [workflow-browser-intelligence.spec.ts](../../../dashboard/e2e/workflow-browser-intelligence.spec.ts):
     - Seeds a browser script with duplicate action names (`Submit` at `actions[0]` and `actions[2]`) so structural `step_path` disambiguation is required.
     - Opens `#/settings?setting=analysis-policies`, switches to `Journey policies`, and picks `${scriptId}\nactions[0]\naction` and `${scriptId}\nactions[2]\naction` directly through `Pick journey 1 start step from script` and `Pick journey 1 success step from script`.
     - Saves revision 1 (`start: { action: "Submit", step_path: "actions[0]" }` and `success: [{ action: "Submit", step_path: "actions[2]" }]`), verifies optimistic concurrency conflict (`409`) against an out-of-band revision 2 update, reloads current revision, saves revision 3, inspects past revision 1 read-only, and deletes the policy.

2. **Full Real Managed Chain E2E (`BI-C1` / `BI-A3` / `BI-A4` / `BE-B6` / `BI-C3` / `BI-C4` / `BI-C5`):**
   - Added `real managed Chromium runs drive live provisional intelligence, finalized findings, HAR drill-down, comparison panels, exports and viewer access` in [workflow-browser-intelligence.spec.ts](../../../dashboard/e2e/workflow-browser-intelligence.spec.ts) with zero `page.route` transport stubs:
     - Enables Root-Cause v2 on the project (`PUT /api/v1/projects/{id}/analysis-settings`), creates a default `browser_policy` (`baseline.mode: "latest_compatible"`, aggressive `lcp`/`fcp` thresholds) and a default `journey_policy` (`step_path: "actions[0]"` → `"actions[2]"`).
     - Launches Run 1 (baseline Chromium run) and pins it for baseline comparison (`POST /api/v1/tests/{id}/comparison-baseline/pin`).
     - Launches Run 2 (35 s candidate Chromium run with `har_capture: "always"`, `capture_interactions: true`, `capture_document_metrics: true`, `client_metrics: true`), verifies provisional live API responses (`provisional: true`, `baseline.status: "not_evaluated_live"`, `journeys[0].match_mode: "step_path"`) and Command Center UI (`Live browser intelligence (provisional — non-verdict)`), and waits for the 30 s cadence to advance sample counts before Run 2 completes.
     - Queues RCA (`POST /api/v1/tests/{id}/archive/root-cause/analyses`) and waits for finalized `browser_findings.v2` (`provisional` falsy, 64-hex `selection_receipt_sha256`, `analysis_id`, and `policy_pins`).
     - Inspects `#/report/{id}?section=browser`, verifies `Browser document comparison`, `Browser findings`, `Browser baseline` (`browser_selection.v1` + receipt digest), `Interaction patterns`, and `Journey risk` (`not.toHaveAttribute('data-provisional', 'true')`), drills down from the active finding's worst document to `Retained archive resource inspector`, verifies token redaction (`?token=[REDACTED]`), views the HAR entry, and downloads the redacted native `.har` and `.zip`.
     - Opens `#/report/{id}?section=comparison`, compares against Run 1, clicks **Save to panel**, and verifies `query_version: 5` (`browser_inputs.version: 2`) with `policy_pins` and single-run `selection_receipt_sha256`.
     - Verifies `HTML`, `CSV`, `PDF`, `DOCX`, and `browser-interactions?format=csv` exports carry `browser_findings.v2` provenance, `selection_receipt_sha256`, policy IDs, `interaction_map`, and `journey_risk`.
     - In `local-auth`, binds `local:user` as `viewer` on the domain, creates a second workspace for cross-project denial (`403` on policies, evaluations, and run creation), verifies `Viewer` can inspect redacted report/resources and download report exports (`200`), verifies raw native HAR and `?include=har` return `403`, verifies `Viewer` write attempts (`POST /api/v1/analysis/policies`, `DELETE /api/v1/analysis/policies/{id}`, `POST /api/v1/tests/{id}/archive/root-cause/analyses`, `POST /api/v1/monitoring/dashboard-definitions`) return `403`, and verifies `#/settings?setting=analysis-policies` blocks non-administrators.
     - Deletes the pinned `browser_policy` and verifies `POST /api/v1/tests/{id}/archive/native-series` returns `422` (`pinned analysis policy has been deleted`) and `#/report/{id}?section=performance` displays `Archive panel unavailable`.

3. **Acceptance Inventory & Suite Catalog:**
   - Updated [browser_intelligence_acceptance_test.go](../../qa/browser_intelligence_acceptance_test.go), [BROWSER_INTELLIGENCE_ACCEPTANCE.md](../../qa/BROWSER_INTELLIGENCE_ACCEPTANCE.md), [E2E_SUITES.md](../../qa/E2E_SUITES.md), and [ROADMAP_IMPLEMENTATION_SPEC.md](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md) while keeping the existing transport-fixture test explicitly labelled for its transport-fixture scope and verifying [DASHBOARD.md](../../qa/DASHBOARD.md) via `python3 scripts/qa-generate-dashboard.py --check`.

<a id="roadmap-browser-full-chain-e2e-2026-10-27--verification-evidence"></a>
### Verification Evidence

- **Playwright `dev-admin` profile:**
  - `APEXLOAD_E2E_PORT=18181 APEXLOAD_E2E_AUTH_PORT=18182 bash scripts/run-e2e.sh workflow-browser-intelligence.spec.ts` → `4 passed (52.4s)` (controller log `dashboard/test-results/controller-logs/dev-admin/run-TvDSLM/selected.log`).
- **Playwright `local-auth` profile:**
  - `APEXLOAD_E2E_PROFILE=local-auth APEXLOAD_E2E_PORT=18181 APEXLOAD_E2E_AUTH_PORT=18182 bash scripts/run-e2e.sh workflow-browser-intelligence.spec.ts` → `4 passed (1.6m)` (controller log `dashboard/test-results/controller-logs/local-auth/run-tUaQyU/selected.log`).
- **Acceptance & Docs Gates:**
  - `go test -v ./docs/qa -run '^(TestBrowserAcceptanceInventory|TestBrowserIntelligenceAcceptanceInventory)$'` → PASS.
  - Mutation check (`go test -overlay`) on `browser_intelligence_acceptance_test.go` mutating the full-chain Playwright test title → FAIL as expected (`Playwright test "mutated non-existent e2e test title" is not in workflow-browser-intelligence.spec.ts`).
  - `python3 scripts/qa-generate-dashboard.py --check` and `python3 scripts/check-docs.py` → PASS.

<!-- Original record: docs/qa/reviews/roadmap-browser-ground-truth-fixture-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-ground-truth-fixture-2026-10-26"></a>

## Browser Ground-Truth Interaction Fixture (50+ Groups), Trusted Mobile Taps & SW Cache Provenance (`BE-A3`)


<a id="roadmap-browser-ground-truth-fixture-2026-10-26--scope"></a>
### Scope

- **Roadmap slice:** `BE-A` (`BE-A3` — Ground-truth interaction fixture 50+ groups, trusted mobile taps, and service-worker/HTTP cache provenance)
- **Date:** 2026-10-26
- **Worktree / branch:** `be-c1-vuser-attribution`

<a id="roadmap-browser-ground-truth-fixture-2026-10-26--summary-of-changes"></a>
### Summary of Changes

1. **Trusted Mobile Single-Finger Taps ([touch_actions.go](../../../pkg/protocol/browser/touch_actions.go), [interaction_evidence.go](../../../pkg/protocol/browser/interaction_evidence.go)):**
   - Updated `shouldUseSyntheticTap()` to return `!b.touchMode` so `browser_tap` uses Playwright's native `locator.Tap()` when `hasTouch` is enabled (`b.touchMode == true`), delivering trusted (`event.isTrusted === true`) `pointerdown`/`touchstart`/`click` events while retaining `waitVisibleWithTimeout` on main-frame taps and falling back to `browserSyntheticTapScript` when `touchMode` is disabled.
   - Multi-finger and path gestures (`browser_double_tap`, `browser_long_press`, `browser_swipe`, `browser_drag_and_drop`, `browser_pinch_zoom`) continue to use synthetic touch dispatch and explicitly report `UnavailableReason: "synthetic_dispatch"`.

2. **Per-Response CDP Service-Worker & HTTP Cache Provenance ([resource_association.go](../../../pkg/protocol/browser/resource_association.go), [BROWSER_DOCUMENT_EVIDENCE.md](../../guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#document-and-navigation-evidence)):**
   - Extended `cdpRequestRecord` and `observeCDPResponse` / `observeCDPCacheHit` to capture CDP `response.fromServiceWorker` (`ServiceWorker: *bool`, `ServiceWorkerUnavailableReason: ""`) alongside HTTP disk/prefetch/memory cache provenance (`Cache: *bool`, `CacheUnavailableReason: ""`), setting `Protocol: ""` and `ProtocolUnavailableReason: "service_worker"` when `fromServiceWorker` is `true` and keeping `ServiceWorker == nil` (`"not_exposed"`) on WebKit.

3. **52-Interaction-Group Ground-Truth Fixture & Cross-Cohort Qualification ([interaction_groups_ground_truth_integration_test.go](../../../pkg/protocol/browser/interaction_groups_ground_truth_integration_test.go)):**
   - Added `pkg/protocol/browser/testdata/interaction_groups/` (`index.html`, `frame.html`, `app.css`, `sw.js`) with 50 main-frame interaction targets (`#grp-btn-1..40`, `#grp-link-41..45`, `#grp-input-46..50`) and 2 child-frame targets (`#iframe-btn-51`, `#iframe-btn-52`), seeded INP outliers (`350 ms` on group 1, `250 ms` on group 2, `64 ms` on group 50), 3 stage-chained layout shifts across 2 session windows, a ServiceWorker answering `/sw-cached.json`, and a cached `/app.css` stylesheet reused by `#child-frame`.
   - Verified across `chromium_desktop`, `chromium_pixel_8`, `webkit_desktop`, and `webkit_iphone_15_pro` that:
     - All 52 distinct interaction groups receive `event.isTrusted === true` (including `browser_tap` on `pixel_8` and `iphone_15_pro`).
     - At `interactionCount >= 50`, `web-vitals` `onINP` selects rank `floor(N/50) = 1` (`250 ms`) rather than the rank-0 maximum (`350 ms`).
     - Chromium `onCLS` matches the maximum session-window score (`Session Window 1`) rather than the total shift sum, while WebKit reports `Available: false, Value: nil, UnavailableReason: "unsupported"` for `cls`.
     - Chromium reports `ServiceWorker == true` (`ProtocolUnavailableReason == "service_worker"`) on `/sw-cached.json` and `Cache == false` then `Cache == true` (`ProtocolUnavailableReason == "cached"`) on `/app.css`, while WebKit preserves `Cache == nil` (`CacheUnavailableReason == "not_exposed"`).
     - `window.__apexGroundTruth` is read once prior to teardown with zero `/sidecar` or probe HTTP requests, no leftover provisional observations or duplicate final `MeasurementID`s, and complete secret redaction across interaction routes and resource origins.

<a id="roadmap-browser-ground-truth-fixture-2026-10-26--verification-mutation-evidence"></a>
### Verification & Mutation Evidence

- `APEXLOAD_BROWSER_TESTS=require go test -v ./pkg/protocol/browser -run "TestBrowserDocumentActualGroundTruthInteractionGroups|TestBrowserProtocolTouchActions|TestBrowserTapFallsBackToSyntheticWhenTouchModeOff|TestBrowserTapUsesSyntheticPath|TestCDPJoinAssociatesDocumentsCacheAndInitiators|TestBrowserActualTouchGesturesSaySyntheticDispatch|TestBrowserInteractionDispatchV2UnavailablePopulations" -count=1` — PASS (`19.03s`)
- `APEXLOAD_BROWSER_TESTS=require go test -v ./pkg/api -run "TestBrowserIntelligenceGoldenAcceptance" -count=1` — PASS (`5.96s`)
- `go test -overlay` mutation 1 (`shouldUseSyntheticTap` returning `true` in `pkg/protocol/browser/touch_actions.go`) — fails `TestBrowserTapUsesSyntheticPath` as expected.
- `go test -overlay` mutation 2 (`cdpServiceWorker(response)` returning `false` in `pkg/protocol/browser/resource_association.go`) — fails `TestCDPJoinAssociatesDocumentsCacheAndInitiators` as expected.

<!-- Original record: docs/qa/reviews/roadmap-browser-ha-source-cache-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-ha-source-cache-2026-10-26"></a>

## Roadmap BE-B4 — HA Source-Cache HAR and Interaction Retention (2026-10-26)


This record covers the `BE-B4` (`be-b4-ha-source-cache`) slice retaining optional browser HARs and raw interaction sidecars in the `_collection` source cache across HA archive collection retries.

<a id="roadmap-browser-ha-source-cache-2026-10-26--scope-delivered"></a>
### Scope Delivered

1. **Best-Effort Optional Evidence Caching (`optional_receipt.json`)**:
   - [`fetchRemoteArchiveBundles`](../../../pkg/api/distributed_archive.go) fetches required source files plus optional HAR and interaction files and invokes [`collectionSourceCache.save`](../../../pkg/api/archive_collection_cache.go), which writes required files and `receipt.json` first and then runs a best-effort [`collectionSourceCache.stageOptional`](../../../pkg/api/archive_collection_cache.go) pass inside the `.pending-<uuid>` directory before atomic `parent.Rename(stage, anchor.ReceiptSHA256)`.
   - `stageOptional` writes optional digests into `optional_receipt.json` bound to `anchor.ReceiptSHA256` without mutating `receipt.json` or SQL `SaveRunArchiveCollectionSource`, preventing cross-pod `ReceiptSHA256` drift when HA pods have different free disk space and keeping the renamed `anchor.ReceiptSHA256` directory immutable.
   - Exceeding `APEXLOAD_ARCHIVE_COLLECTION_MAX_BYTES`, `APEXLOAD_ARCHIVE_COLLECTION_CACHE_MAX_BYTES`, or the `64 MiB` free-space floor skips only the optional files that do not fit and never fails `save`.
2. **Cache-First Optional Retrieval, Live Fallback, and `source_unavailable` Classification**:
   - [`optionalEvidenceSource`](../../../pkg/api/archive_browser_har.go) serves cached optional files from [`cachedArchiveSource`](../../../pkg/api/archive_collection_cache.go) when present, falls back to the live generator client when an optional file was not cached and the generator is still reachable, and returns `errOptionalEvidenceSourceUnavailable` when the generator is lost and the optional file was not cached so [`fetchRemoteHARArtifacts`](../../../pkg/api/archive_browser_har.go) counts `source_unavailable` (not `transfer_failed`) and [`fetchRemoteInteractions`](../../../pkg/api/archive_browser_interactions.go) reports `source_unavailable`.
3. **Directory Entry Bounds & Cleanup Verification**:
   - Updated [`collectionSourceCache.decode`](../../../pkg/api/archive_collection_cache.go) and [`verifyCollectionCacheForCleanup`](../../../pkg/api/archive_collection_cache_cleanup.go) (`ReadDir(maxCollectionCacheDirectoryEntries + 1)`) to validate `optional_receipt.json` and verify size and SHA-256 of all cached optional files before `_collection` directory retirement.
   - Updated [BROWSER_HAR_WORKING_OWNERSHIP.md](../../BROWSER_EVIDENCE_ARCHITECTURE.md) to document `_collection` optional HAR and interaction retention.

<a id="roadmap-browser-ha-source-cache-2026-10-26--verification"></a>
### Verification

- `go test -race -count=2 -shuffle=on -run "TestDistributedArchiveTransfersHARsWithReceipts|TestDistributedArchiveMergesInteractionEvidenceWithReceipts|TestCachedArchiveSource|TestCollectionCache" ./pkg/api`
- `python3 scripts/check-docs.py`

<!-- Original record: docs/qa/reviews/roadmap-browser-har-capture-races-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-har-capture-races-2026-10-26"></a>

## Native HAR capture body bounds and close/cancel race qualification — 2026-10-26


This [BE-A4 slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#be-a--stable-authored-identity-and-qualified-document-evidence) qualifies the three runtime body/HAR capture bounds (`512 KiB` per-resource body capture limit, `32 MiB` per-session retained body budget, and `100 MiB` native HAR working-file cap) over chunked finite streams alongside all six permutations of `(cap-overflow, caller-cancel, native-close)` across both the 2-second mid-session close wait (`runtimeHARCloseWait`) and the 30-second teardown finalization budget (`runtimeHARFinalizationBudget`). It also updates the [earlier outstanding-request review record](#roadmap-har-inflight-2026-10-20) so its opening prose matches the implemented `1,024` outstanding-request bound (`maxRuntimeHARInFlightRequests`).

<a id="roadmap-browser-har-capture-races-2026-10-26--production-hardening"></a>
### Production hardening

- `browserRequestBody` (`pkg/protocol/browser/runtime_context.go`) now truncates oversized UTF-8 `PostDataBuffer` payloads with `boundedtext.TruncateUTF8` instead of slicing at a raw byte offset that could split a multi-byte UTF-8 rune and misclassify a text body as base64, and bounds binary request bodies prior to base64 encoding so the encoded representation stays within `512 KiB`.
- `recordNetworkTask` (`pkg/protocol/browser/runtime_context.go`) clamps negative unknown Playwright `RequestSizesResult` fields (`-1`) to `0` before accumulating `BytesReceived` and `noteHARBytes`.
- `harCaptureOwner` (`pkg/protocol/browser/har_working_lease.go`, `pkg/protocol/browser/har_capture.go`) now upgrades `keep` and `redactor` under `o.mu` (`upgradeFinish`) when `closeOwnedOversizedHARContext` marks `harErrorSeen = true` while teardown joins (`closeDone`, `documentDone`, `runtimeDone`) are still in flight. Even when caller cancellation enters `startFinish` before an in-flight overflow callback runs, `HARCaptureOnError` preserves the error-keep decision for `finalizeJoined` once the native writer joins.

<a id="roadmap-browser-har-capture-races-2026-10-26--validation-and-review"></a>
### Validation and review

- `TestHARCaptureChunkedFiniteBodyCapsSequential` streams chunked finite payloads sequentially (without `t.Parallel()` to bound `-race` RSS) across the `512 KiB` per-resource cap (including a 3-byte UTF-8 rune straddling the exact `512 KiB` boundary and binary base64 capping), the `32 MiB` session retained-body ring budget (`66 × 512 KiB = 33 MiB`), and the `100 MiB` native HAR working-file cap on a real owned `harCaptureOwner`.
- `TestHARCaptureAllSixOrdersOverflowCancelAndNativeClose` runs all six orders of `(cap_overflow, caller_cancel, native_close)` across `mid_session_2s_close_wait`, `teardown_30s_within_budget`, and `teardown_30s_expired_budget`, verifying that mid-session close never starts the 30-second teardown clock, caller cancellation never retires an unjoined writer or leaks reservations, and joined writers always retire their raw working files.
- Mutation verification via `go test -overlay` confirmed the new tests fail when `upgradeFinish` in `har_working_lease.go` or UTF-8 rune-safe truncation in `browserRequestBody` is reverted.
- Independent review rounds checked code safety and test/documentation fidelity; all findings were resolved before commit.

<!-- Original record: docs/qa/reviews/roadmap-browser-har-omissions-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-har-omissions-2026-10-20"></a>

## Native HAR capture omission retention — 2026-10-20


A native capture can fail before publishing an artifact. Previously that left a successful managed run with no native HAR and no capture-loss count in its archive. The capture owner now hands off bounded, closed-reason omission counts through local and distributed archive sinks, including their combined wrappers. Generator archive collection preserves those counts. The existing authorized HAR listing exposes the incomplete state.

A capture owner reports at most once. Deadline exhaustion, unretired ownership, joined capture failure and publication-queue exhaustion remain distinct. Invalid provider status becomes a sanitized unavailable count. Deliberate capture-policy discard remains uncounted. Reporting never changes physical ownership, retirement eligibility, target run outcome or target traffic.

The real managed fixture uses an owned loopback target and a private capture directory filled with unrelated files to exceed its bounded retention scan. It verifies successful target execution, no advertised native artifact, retained omission in the finalized manifest/listing, and unchanged unrelated files. It is local Chromium evidence, not Linux/Windows crash, actual-device or replay-durability qualification.

<a id="roadmap-browser-har-omissions-2026-10-20--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,676 passing tests/subtests**, 38 explicit skips; native browsers required. All stable private HAR import compatibility results match the earlier qualified import.
- Targeted race coverage passed **42 tests/subtests** across three repetitions, including the real managed failure path.
- Dashboard typecheck, lint, 31 Help unit tests and build passed. HAR/resource/Help Playwright workflows passed: dev-admin 21 passing with two auth-specific skips, local-auth 23 passing. The embedded ownership article exactly matches its repository source. Documentation links passed for 301 active Markdown files.
- Review fixed combined-sink forwarding and the ownership-label finding; the final round is clean. `evidence/review-rounds.json`, `evidence/validation-summary.json`, `evidence/tested-source-hashes.json`, `evidence/tested-asset-hashes.json` and `evidence/SHA256SUMS` retain the validation scope.

Earlier fixture/forwarding diagnostics and the full run before the final ownership-label correction are retained as such; they do not replace the final full-suite and race evidence. Raw evidence stays beside this Markdown record on disk in accordance with the parallel QA evidence cleanup; only the review record is committed. No private source HAR bytes are committed.

<!-- Original record: docs/qa/reviews/roadmap-browser-interaction-bounds-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-interaction-bounds-2026-10-27"></a>

## QA Review — Interaction Analysis Bounds, Keyboard Target Counts & Raw Interaction CSV Export (`BI-B4`)


- **Date:** 2026-10-27
- **Requirement / Track:** `BI-3` (`BI-B` — Interaction maps and authored journey outcomes)
- **Work Package:** `BI-B4`
- **Branch / Worktree:** `be-c1-vuser-attribution` (`/Users/jasonczech/Documents/GitHub/Project_Stormrunner/review-worktrees/be-b3`)

<a id="roadmap-browser-interaction-bounds-2026-10-27--scope-contract-changes"></a>
### Scope & Contract Changes

1. **Seven Interaction-Analysis Bounds & Population Separation ([browser_interaction_patterns.go](../../../pkg/api/browser_interaction_patterns.go)):**
   - Exposed the streaming vs pattern-retained population split on `BrowserInteractionCoverage`:
     - `streamed_interactions`: count of raw interaction records observed across the archive stream (contributing to coverage, cohort attribution, and heatmaps even beyond the 100,000 pattern retention cap).
     - `pattern_retained`: count of interaction records retained in the pattern analyzer (`maxRetainedInteractions = 100,000`).
     - `pattern_truncated`: count of interaction records beyond `maxRetainedInteractions` excluded from pattern detection while still contributing to streaming coverage and heatmaps.
   - Preserved `maxInteractionGenerationCohorts = 65,536` (`document_evidence_truncated`), `maxInteractionDocumentJoins = 100,000` (`document_joins_truncated`), `maxInteractionHeatmaps = 64` (`heatmaps_truncated`), `maxHeatmapTargets = 64` per map, `maxRetainedHeatmapTargets = 1,024` per map (`keyboard_targets_truncated`), `maxInteractionPatterns = 200` (`patterns_truncated`), and `maxInteractionPatternMembers = 16` (`MembersTruncated` on `BrowserInteractionPattern` while preserving true `Count`).
   - Added `patternTruncationPriority` so actionable patterns (`dead_interaction`, `unexpected_navigation`, `retry`, `repeated_gesture`) sort ahead of benign `scripted_repetition` before `[:maxInteractionPatterns]` truncation when more than 200 patterns are present.
   - Qualified historical v1 evidence in `newInteractionAnalyzer` with explicit notes explaining post-action geometry/scroll and capture-time repeat windows.

2. **Keyboard Target Counts & Report Format Parity ([report_browser_intelligence.go](../../../pkg/api/report_browser_intelligence.go), [report_section_templates.go](../../../pkg/api/report_section_templates.go), [BrowserInteractionsPanel.tsx](../../../dashboard/src/components/runReport/BrowserInteractionsPanel.tsx)):**
   - Added `Targets` (`formatReportHeatmapTargets`) and combined pointer+keyboard counts (`heatmap.Points + sumCounts(heatmap.Targets)`) to `InteractionMapView` so keyboard-only interactions are never rendered as `0 gestures` across HTML, PDF, and CSV report formats.
   - Surfaced `pattern_retained` / `pattern_truncated` coverage notices, `members_truncated` badges (`N of M IDs listed`), and combined pointer/keyboard gesture counts (`X pointer gestures · Y keyboard gestures (Z total)`) in `BrowserInteractionsPanel.tsx`.

3. **Raw Interaction Analysis CSV Export ([browser_interaction_patterns.go](../../../pkg/api/browser_interaction_patterns.go)):**
   - Extended `GET /api/v1/tests/{id}/archive/browser-interactions` with `?format=csv` (`writeBrowserInteractionAnalysisCSV`), returning `text/csv; charset=utf-8` with `coverage`, `pattern`, `heatmap`, `heatmap_note`, `limitation`, and `note` sections, `reportCSVSafeCell` formula-injection sanitization on untrusted strings, and `400 Bad Request` on unsupported `format` values.

<a id="roadmap-browser-interaction-bounds-2026-10-27--verification-mutation-testing"></a>
### Verification & Mutation Testing

- **Dashboard Typecheck, Unit Tests & Production Build:**
  - `cd dashboard && npm run typecheck && npx vitest run src/components/runReport/BrowserInteractionsPanel.test.tsx src/labelCasing.test.ts && npm run build` (`11 passed (11)`).
- **Go Race & Shuffle Suite:**
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestBrowserInteraction|TestRunArchiveBrowserInteractions|TestCapturedTapsRepeat|TestEngineRetries|TestDeadInteractions|TestRepetition|TestHeatmaps|TestBrowserGenerationCohorts|TestInteractionHeatmap|TestInteractionAnalysis|TestBrowserIntelligenceReportSections|TestArchiveAuthoredPage|TestOpenAPISpecIsFresh|TestRouteCatalogCharacterization'`
- **Mutation Verification (`go test -overlay`):**
  - `m1_pattern_priority`: Neutralized `patternTruncationPriority` in `findInteractionPatterns` so `scripted_repetition` sorts ahead of `dead_interaction` by count -> killed by `TestBrowserInteractionAnalysisSevenBoundsAndCSVReportParity`.
  - `m2_kbd_gesture_sum`: Omitted `sumCounts(heatmap.Targets)` from `InteractionMapView.Gestures` in `report_browser_intelligence.go` -> killed by `TestBrowserInteractionAnalysisSevenBoundsAndCSVReportParity`.

<!-- Original record: docs/qa/reviews/roadmap-browser-interaction-patterns-v4-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-interaction-patterns-v4-2026-10-27"></a>

## Browser Interaction Patterns v4 and UI/Report v4 Qualification Review (`BI-B2`)


<a id="roadmap-browser-interaction-patterns-v4-2026-10-27--scope"></a>
### Scope

This review covers `BI-B2` (`browser_interaction_patterns.v4` + UI/Report `.v4`):

- **`browser_interaction_patterns.v4` Evaluation (`pkg/api/browser_interaction_patterns.go`):**
  - Emits `Version: "browser_interaction_patterns.v4"` when raw v4 interaction evidence (`protocol.BrowserInteractionVersion4`) is present, while preserving `"browser_interaction_patterns.v3"` for raw v2/v3 archives and `"browser_interaction_patterns.v2"` for historical v1 archives.
  - Evaluates all declared outcomes in `g.DeclaredOutcomes()` without stopping after the first missed outcome:
    - Navigated interactions (`UnavailableReason == "document_changed"` or a `navigation` outcome with `Observed == "met"` or `Reason == "unexpected_navigation"`) emit `unexpected_navigation` for each missed in-page outcome (`element_visible`, `visible`), `assertion` outcome, or unexpected `navigation` outcome, never `dead_interaction`.
    - Non-navigated interactions emit `dead_interaction` for each missed page-response outcome (`sync` with `element_visible`/`visible`/`load`/`domcontentloaded`/`dialog`/`download`, or `dialog`, `download`, `assertion`, `navigation`), while ignoring missed non-page-response syncs (`dom_stable`, `network_idle`, `duration`).
    - Populates `outcome_kind`, `outcome_index`, `outcome_reason`, `declared`, and `deadline_ms` on `BrowserInteractionPattern` and derives `Pattern.ID` from `(kind, interaction_id, outcome.kind, outcome_index, outcome.declared)` so multiple missed outcomes on the same interaction receive distinct deterministic IDs.
  - Groups surviving attempts of the same logical invocation (`sameInvocation` with strictly increasing `Execution.Attempt`) into `retry` across non-consecutive attempt numbers (when intermediate attempts were dropped) and across interleaved unrelated gestures on the same document, while keeping all retry attempts (including lone surviving `Attempt > 1` records) out of `repeatedGestures`.
  - Anchors `repeatedGestures` windows (`2 s`, `3` gestures, `32 px`) strictly on renderer `Dispatch.At` when `BrowserInteractionHasDispatch(g.Version)` is true and ensures `"scripted"` or `"unknown"` provenance never receives the human-frustration label `"repeated_gesture"`.
- **Report & Dashboard v4 Support (`pkg/api/report_browser_intelligence.go`, `dashboard/src/components/runReport/BrowserInteractionsPanel.tsx`, `dashboard/e2e/workflow-browser-intelligence.spec.ts`):**
  - Extends `InteractionMapView` in `pkg/api/report_browser_intelligence.go` and `BrowserInteractionsPanel.tsx` to recognize `browser_interaction_patterns.v4`, surface `mapped_via_transform` child-frame transform counts, and include `outcome <kind>[<index>]` details in HTML, PDF, and CSV report exports.

<a id="roadmap-browser-interaction-patterns-v4-2026-10-27--verification"></a>
### Verification

- **Dashboard Typecheck, Vitest & Build:**
  - `cd dashboard && npm run typecheck`
  - `cd dashboard && npx vitest run src/components/runReport/BrowserInteractionsPanel.test.tsx src/labelCasing.test.ts`
  - `cd dashboard && npm run build`
- **Go Race & Shuffle Tests:**
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestBrowserInteraction|TestRunArchiveBrowserInteractions|TestCapturedTapsRepeat|TestEngineRetries|TestDeadInteractions|TestRepetition|TestHeatmaps|TestBrowserGenerationCohorts|TestInteractionHeatmap|TestInteractionAnalysis|TestBrowserIntelligenceReportSections|TestArchiveAuthoredPage'`
- **Mutation Verification (`go test -overlay`):**
  - `m1_multi_outcome_first_only`: Evaluating only `g.DeclaredOutcomes()[:1]` in `findInteractionPatterns` for v4 interactions is killed by `TestBrowserInteractionPatternsV4CombinedQualificationAndReportFormats` (`exit= 1`).
  - `m2_retry_consecutive`: Requiring consecutive `Attempt == prev + 1` instead of `Attempt > prev` on v4 retry grouping is killed by `TestBrowserInteractionPatternsV4CombinedQualificationAndReportFormats` (`exit= 1`).

<!-- Original record: docs/qa/reviews/roadmap-browser-interaction-v4-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-interaction-v4-2026-10-26"></a>

## QA Review Record — `BE-A1 + BE-A2 + BI-B1` (`FeatureBrowserInteractionEvidenceV4` & Authored Page Receipt v3)


- **Date:** `2026-10-27`
- **Branch:** `be-c1-vuser-attribution`
- **Packages:** `third_party/playwright-go`, `pkg/protocol`, `pkg/protocol/browser`, `pkg/distributed/generatorrpc`, `pkg/engine`, `pkg/api`
- **Roadmap Items:**
  - `BE-A1`: Child-Frame Interaction Mapping & Post-Action Geometry Provenance (`BrowserInteractionVersion4`)
  - `BE-A2`: Multi-Outcome Interaction Evidence (`Outcomes []BrowserInteractionOutcome` for `sync`, `dialog`, `download`, `assertion`, and `navigation`)
  - `BI-B1`: Authored-Page Binding v3 (`BrowserAuthoredPageVersion3`: `history_traversal`, `same_document_route`, `opened_by_action`, and `redirect_chain` with `RouteSeq`)

<a id="roadmap-browser-interaction-v4-2026-10-26--summary-of-changes"></a>
### Summary of Changes

1. **Playwright Navigation Commit & `CDPSession` Event Hardening (`third_party/playwright-go`):**
   - Extended `APEXNavigationCommit` and `frameImpl.onFrameNavigated` in `third_party/playwright-go/apex_navigation_commit.go` and `third_party/playwright-go/frame.go` to record `NewDocument`, `SameDocument`, and `Failed` flags from native `frameNavigated` protocol events.
   - Hardened `connection.Dispatch` and `replaceGuidsWithChannels` in `third_party/playwright-go/connection.go` so raw `CDPSession` events (such as `Page.downloadWillBegin` and `Page.downloadProgress`, which carry CDP download `guid` fields in multi-key maps) pass through without corrupting the Playwright channel table, and only single-key `{"guid": "..."}` maps are interpreted as Playwright channel references.

2. **Protocol Contracts (`pkg/protocol`):**
   - Added `BrowserAuthoredPageVersion3 = 3`, `RouteSeq uint64`, v3 association constants (`history_traversal`, `same_document_route`, `opened_by_action`, `redirect_chain`), and v3 unavailable reason constants in `pkg/protocol/browser_authored_page.go`.
   - Added `BrowserInteractionVersion4 = 4`, `"download"` pointer kind, `GeometryProvenance` (`"dispatch"` / `"post_action"`), `AuthoredPageID`, `RouteSeq`, `FrameChain` (`[]BrowserInteractionFrameHop` with `Invertible2D()` and `ApplyToPoint()`), and `Outcomes []BrowserInteractionOutcome` (`sync`, `dialog`, `download`, `assertion`, `navigation`) in `pkg/protocol/browser_interaction.go` and `pkg/protocol/browser_interaction_v2.go`.
   - Added `Begin`, `Finish`, and `ClampToDeadline` helpers to `BrowserOutcomeClock` in `pkg/protocol/browser_outcome_clock.go`.

3. **Distributed Capability Negotiation (`pkg/distributed/generatorrpc`, `pkg/engine`):**
   - Added `FeatureBrowserInteractionEvidenceV4 = "browser_interaction_evidence_v4"` in `pkg/distributed/generatorrpc/browser_documents.go`, negotiated v4 across homogeneous generator fleets in `pkg/distributed/generatorrpc/browser_interaction_version.go` (falling back to v3/v2/v1 on mixed fleets), and enabled v4 + v3 page bindings by default for local runs in `pkg/engine/config_protocols.go`.

4. **Browser Runtime Capture (`pkg/protocol/browser`):**
   - Implemented v3 authored-page bindings in `pkg/protocol/browser/authored_page.go` and `pkg/protocol/browser/authored_page_native_commit.go` for `browser_back` / `browser_forward` (`history_traversal`), same-document `browser_navigate` and post-action `history.pushState`/`replaceState`/hash transitions (`same_document_route` with monotonic `RouteSeq`), `browser_open_window` / `browser_switch_window` / action-opened popups (`opened_by_action`), and HTTP/client redirect chains (`redirect_chain`).
   - Extended the injected interaction observer in `pkg/protocol/browser/interaction_evidence.go` and `pkg/protocol/browser/interaction_dispatch.go` to walk `window.frameElement` up to `MaxBrowserInteractionFrameDepth = 8` same-origin hops, capturing 2D affine transforms, scroll/viewport offsets, border/padding, and explicit exclusion reasons (`cross_origin_ancestry`, `non_axis_aligned_or_3d_transform`, `frame_depth_exceeded`).
   - Recorded multi-outcome slices (`sync`, `dialog`, `download`, `assertion`, `navigation`) on v4 interactions in `pkg/protocol/browser/interaction_outcome_v2.go`, `pkg/protocol/browser/download_actions.go`, `pkg/protocol/browser/assertions.go`, and `pkg/protocol/browser/browser.go`.

5. **Controller Archive & Pattern Evaluators (`pkg/api`):**
   - Updated `archiveAuthoredPageAccumulator` in `pkg/api/archive_browser_authored_pages.go` to key bindings by `(SourceGeneration, DocumentID, LifecycleID, RouteSeq)` so distinct SPA route transitions within a single document lifecycle are retained without false conflict drops.
   - Updated `pkg/api/browser_interaction_patterns.go` to project v4 child-frame points into top-level viewport coordinates via `BrowserInteractionFrameHop.ApplyToPoint` (`MappedViaTransform = true`), exclude `post_action_geometry` and unmapped frames from heatmaps, and evaluate all declared outcomes in `DeclaredOutcomes()`.

<a id="roadmap-browser-interaction-v4-2026-10-26--verification-mutation-testing"></a>
### Verification & Mutation Testing

- **Targeted & Package Tests:**
  - `APEXLOAD_BROWSER_TESTS=require go test ./pkg/protocol ./pkg/protocol/browser ./pkg/distributed/generatorrpc ./pkg/engine -count=1`
  - `APEXLOAD_BROWSER_TESTS=require go test ./pkg/api -run "TestBrowser|TestArchive.*Browser|TestArchiveAuthoredPage" -count=1`
- **Overlay Mutation Checks (`go test -overlay`):**
  1. `pkg/protocol/browser_interaction_v2.go`: Disabling the non-empty `FrameChain` requirement on dispatch-qualified `child_frame` v4 interactions was killed by `TestBrowserInteractionV4OutcomesAndFrameChainContract`.
  2. `pkg/protocol/browser_authored_page.go`: Disabling the `RouteSeq > 0` requirement on `same_document_route` v3 bindings was killed by `TestBrowserAuthoredPageBindingV3Contract`.
  3. `pkg/api/browser_interaction_patterns.go`: Disabling `post_action_geometry` heatmap exclusion was killed by `TestBrowserInteractionV4ChildFrameMappingAndExclusions`.
  4. `pkg/api/archive_browser_authored_pages.go`: Omitting `RouteSeq` from the `archiveAuthoredPageAccumulator` dedup key was killed by `TestArchiveAuthoredPageAccumulatorRetainsDistinctRouteSeqs`.

<!-- Original record: docs/qa/reviews/roadmap-browser-journey-steps-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-journey-steps-2026-10-26"></a>

## BI-B3 — Structural Intra-Iteration Journey Attempts (`StepPath` Binding)


<a id="roadmap-browser-journey-steps-2026-10-26--scope"></a>
### Scope

This slice implements structural intra-iteration journey boundaries (`BI-B3`) across the VUser engine, archive pipeline, analysis policy API, journey accounting evaluator, and dashboard policy/report views:

- **Engine & Protocol Attribution (`pkg/protocol`, `pkg/engine`)**:
  - Exported `ValidBrowserAuthoredStepPath` and `ValidBrowserAuthoredDigest` in `pkg/protocol/browser_authored.go` and added `AuthoredStep *BrowserAuthoredStep` onto `protocol.Result` (`pkg/protocol/protocol.go`).
  - Wired `currentAuthoredAction` / `currentBrowserAuthoredStep()` onto `VUser` (`pkg/engine/vuser.go`, `pkg/engine/vuser_run.go`, `pkg/engine/control_parallel.go`) so leaf action results and automatic per-action transactions record their exact structural `StepPath`, while synthetic whole-iteration transactions remain free of leaf `AuthoredStep`.
  - Extended `sourceTransactionRuntimeState` (`pkg/engine/vuser_source_actions.go`) to capture the opening `lr_start_transaction` / `lr_start_sub_transaction` structural `AuthoredStep` and propagate it when `lr_end_transaction` / `lr_end_sub_transaction` or `lr_set_transaction` records the business transaction.
  - Added `ScriptJourneySteps(script *dsl.Script)` (`pkg/engine/browser_authored.go`) to enumerate structural iteration step boundaries (`step_path`, `kind`, `name`, `action_type`) and the script's `StructureSHA256`.
- **Archive Transport & Sanitization (`pkg/distributed/generatorrpc`, `pkg/api`)**:
  - Added `AuthoredStep *protocol.BrowserAuthoredStep` to `archiveTransactionEvent` (`pkg/distributed/generatorrpc/archive.go`) and `TransactionEvent` (`pkg/api/archive.go`, `pkg/api/archive_writer.go`), validated fail-closed in `sanitizeArchivedTransaction` (`pkg/api/archive_transaction_sanitize.go`).
- **Journey Boundary Policy, Step Catalog & Accounting (`pkg/api`)**:
  - Extended `JourneyBoundary` with optional `StepPath` and `StructureSHA256` (`pkg/api/journey_accounting.go`), validated via `ValidBrowserAuthoredStepPath` (`actions[...]`) and `ValidBrowserAuthoredDigest` (refusing `StructureSHA256` when `StepPath` is omitted).
  - Added `GET /api/v1/analysis/journey-steps` (`handleListAnalysisJourneySteps` in `pkg/api/analysis_policies.go`, registered in `pkg/api/route_registry_analysis.go`) returning per-script structural step catalogs for the project.
  - Updated `JourneyBoundary.matches` and `JourneyAccounting.MatchMode` (`"step_path"` vs `"name"`): when `StepPath` is configured and `event.AuthoredStep` is present, matching requires `event.AuthoredStep.StepPath == b.StepPath` (and `SourceStructureSHA256 == b.StructureSHA256` when pinned), disambiguating duplicate display names and surviving non-structural script edits; legacy archives without `AuthoredStep` fall back to `Name` only when `StructureSHA256` is not pinned and disclose `match_mode: "name"`.
  - Updated `closeIteration` (`pkg/api/journey_accounting.go`) with `beginOrdinal` / `seq` ordering so zero-duration (`begin == end`) loop passes complete each attempt before the next pass opens while zero-duration transaction wrappers open before and close after their enclosed steps.
- **Dashboard Editor & Run Report (`dashboard/src`)**:
  - Extended `JourneyDraft`, `JourneyPolicyFields` (`AnalysisPoliciesPanel.tsx`), and `analysisPoliciesApi.listJourneySteps` (`analysisPoliciesApi.ts`) with a structural script step picker and `step_path` inputs while preserving any existing `structure_sha256` on the policy revision until the step path is edited.
  - Updated `journeyRiskModel.ts` and `JourneyRiskPanel.tsx` to format structural `step_path` boundaries and display `Step path` vs `Name fallback` match modes.

<a id="roadmap-browser-journey-steps-2026-10-26--verification-evidence"></a>
### Verification Evidence

- **Go Unit & Race Tests**:
  - `go test -race -count=2 -shuffle=on ./pkg/protocol -run 'TestBrowserAuthored'`
  - `go test -race -count=2 -shuffle=on ./pkg/engine -run 'TestBrowserAuthored|TestBrowserOriginal'`
  - `go test -race -count=2 -shuffle=on ./pkg/distributed/generatorrpc -run 'TestArchive'`
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestListAnalysisJourneyStepsAndBoundaryValidation|TestJourneyAccountingStepPath|TestJourneyAccountingZeroDuration'`
- **Mutation Testing (`go test -overlay`)**:
  - Mutant 1 (`pkg/api/journey_accounting.go` `if event.AuthoredStep.StepPath != b.StepPath` -> `if false`): killed by `TestJourneyAccountingStepPathSeparatesDuplicateNamesAndSurvivesNonStructuralEdits` and `TestJourneyAccountingZeroDurationLoopTieBreaking`.
  - Mutant 2 (`pkg/api/journey_accounting.go` `if left.seq != right.seq` -> `if false`): killed by `TestJourneyAccountingZeroDurationLoopTieBreaking`.
- **Dashboard Checks**:
  - `cd dashboard && npm run typecheck && npm run lint && npm run lint:a11y`
  - `cd dashboard && npx vitest run src/components/analysisPolicies/policyEditorModel.test.ts src/components/analysisPolicies/AnalysisPoliciesPanel.test.tsx src/components/runReport/JourneyRiskPanel.test.tsx src/labelCasing.test.ts` (4 files, 24 tests passed)
  - `cd dashboard && npm run build && npm run test:bundle-budget && npm run bundle:check`

<a id="roadmap-browser-journey-steps-2026-10-26--remaining-open-items"></a>
### Remaining Open Items

- Outcome-deadline composition (`BI-B1` / `BI-B2`) and interaction map cohort extensions (`BI-B4`) are tracked in their dedicated slices.

<!-- Original record: docs/qa/reviews/roadmap-browser-linux-har-crash-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-linux-har-crash-2026-10-26"></a>

## Roadmap BE-B1 — Linux Native HAR Crash Qualification & PID-1 Zombie Fix (2026-10-26)


This record covers the `BE-B1` (`be-b1-linux-har-crash`) slice for Linux `/proc/<pid>/stat` zombie/dead state handling, PID-1 container crash reclamation on an `ext4` volume, and the authoritative Linux CI gate in [ci.yml](../../../.github/workflows/ci.yml).

<a id="roadmap-browser-linux-har-crash-2026-10-26--scope-delivered"></a>
### Scope Delivered

1. **Linux `/proc/<pid>/stat` Zombie and Dead State Handling**:
   - Extracted `parseLinuxProcStatStart` and wired `harProcessStart` to delegate to it. After the NA-C writer-ownership slice moved process identity to `pkg/internal/procidentity`, it lives on as [`procidentity.ParseLinuxStatStart`](../../../pkg/internal/procidentity/procidentity_stat.go), used by `processStart`.
   - After verifying a well-formed `/proc/<pid>/stat` line (closing `')'` delimiter, at least 20 post-`comm` fields, and unsigned 64-bit `starttime` at field 22), processes in state `'Z'` (zombie), `'X'` (dead), or `'x'` (dead) return `("", false, nil)`.
   - When `generator` runs as PID 1 of a Linux container without `--init` ([Dockerfile.browser-generator](../../../Dockerfile.browser-generator)), a killed child's orphaned Playwright Node driver is reparented to PID 1 and transitions to state `'Z'` once its stdin-EOF graceful shutdown completes; treating `'Z'` as exited prevents reclamation from deadlocking while preserving fail-closed behavior on malformed `/proc/<pid>/stat` records.
2. **Authoritative Linux CI Gate**:
   - Added the `Native HAR ownership (Linux)` step (`if: matrix.lane == 'race'`, `APEXLOAD_BROWSER_TESTS: require`) in [ci.yml](../../../.github/workflows/ci.yml) running `go test -race -count=1 -v -timeout=30m ./pkg/protocol/browser -run 'HAR'` and `go test -count=1 -v -timeout=30m ./pkg/api -run 'HAR|BrowserReplay'`.
3. **Documentation Alignment**:
   - Updated [BROWSER_HAR_WORKING_OWNERSHIP.md](../../BROWSER_EVIDENCE_ARCHITECTURE.md) to document Linux `'Z'`/`'X'`/`'x'` process-state reclamation, the `1,024` outstanding-request replay bound, the Script Debugger synthetic HAR export contract (`GET /api/v1/script-debug/sessions/{session_id}/export` with `?format=har`, governed by `runtime_profile.har_options`), and the Linux crash qualification record.

<a id="roadmap-browser-linux-har-crash-2026-10-26--verification-repeat-runs"></a>
### Verification & Repeat Runs

- **Portable Unit & Regression Tests**:
  - `go test -race -count=2 -run "TestParseLinuxProcStatStartTreatsZombieAndDeadStatesAsExited|TestHAROwnershipReclaimsAbandonedGenerationsAndPreservesUnprovenOnes" ./pkg/protocol/browser` -> `ok github.com/jasonczech/apexload/pkg/protocol/browser 1.593s`
- **Three Consecutive Linux PID-1 Crash Qualification Runs (`ext4` Docker volume)**:
  - Executed `/test/browser-linux.test -test.count=3 -test.v -test.run=^TestHAROwnershipReclaimsKilledNativeRecording/chromium$` as PID 1 (`--entrypoint /test/browser-linux.test`) inside `apexload-controller:local` with `-v apexload-har-ext4:/tmp` (`ext4`-backed Docker volume, not a macOS FUSE bind mount):
    - Run 1: `engine=chromium pid=1 driver=34 exited after 104ms` (`PASS`, `1.10s`)
    - Run 2: `engine=chromium pid=1 driver=187 exited after 103ms` (`PASS`, `0.83s`)
    - Run 3: `engine=chromium pid=1 driver=332 exited after 53ms` (`PASS`, `0.86s`)
  - Across all three runs, the orphaned Playwright Node driver exited within `53–104 ms` of `SIGKILL` (well inside the 30-second `gracefullyProcessExitDoNotHang` / finalization budget), transitioned to a reparented child of PID 1, and its abandoned working generation was planned and pruned cleanly.
- **Workflow & Documentation Checks**:
  - `python3 scripts/ci/validate-github-actions.py` -> `validated 12 workflows and local composite actions`
  - `python3 scripts/check-docs.py` -> passed (`0` errors)

<!-- Original record: docs/qa/reviews/roadmap-browser-linux-memory-admission-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-linux-memory-admission-2026-10-26"></a>

## QA Review Record — Browser Linux Memory Admission (`BE-A5`)


**Date:** 2026-10-26
**Scope:** `BE-A5` — Linux `/proc/<pid>/smaps_rollup` `Pss` process-tree sampling and real measured browser context memory admission (`measureBrowserMemoryUsage` / `measureBrowserMemoryBreakdown`).
**Primary Packages:** `pkg/protocol/browser`, `.github/workflows/ci.yml`

---

<a id="roadmap-browser-linux-memory-admission-2026-10-26--1-summary-of-changes"></a>
### 1. Summary of Changes

1. **Linux `/proc/<pid>/smaps_rollup` `Pss` Preference (`pkg/protocol/browser/runtime_metrics.go`, `pkg/protocol/browser/evidence_budget.go`):**
   - Extended `processSample` with `VmRSSKB`, `PSSKB`, and `PSSKnown`.
   - Updated `readProcProcessSamples` to retain `VmRSSKB` from `/proc/<pid>/stat` and, when `/proc/<pid>/smaps_rollup` is readable and yields a positive `Pss:`, set `PSSKB`, `PSSKnown = true`, and `RSSKB = PSSKB` so `processTreeRSS` sums proportional resident bytes instead of multiplying shared Chromium Zygote pages across every child process. When `smaps_rollup` is absent or unreadable, `readProcProcessSamples` falls back to `VmRSS`.
   - Added `browserMemoryBreakdown`, `measureBrowserMemoryBreakdown`, `processTreeMemoryTotals`, and `cgroupMemoryCurrent` so `measureBrowserMemoryUsage` prefers `Pss` on Linux for both the generator process (`/proc/self/smaps_rollup`) and the Playwright driver + browser descendant tree while exposing full `VmRSS`, `Pss`, and cgroup `memory.current` / `memory.max` figures for qualification.

2. **Subprocess & `/proc` Qualification Tests (`pkg/protocol/browser/evidence_budget_test.go`, `.github/workflows/ci.yml`):**
   - Added `TestProcProcessSamplesPreferSmapsRollupPSSOverVmRSS` verifying `smaps_rollup` `Pss` preference, `VmRSS` preservation, and `VmRSS` fallback when `smaps_rollup` is absent.
   - Added `TestBrowserMemoryAdmissionActual`, which calibrates 1 live headless Chromium context's `measureBrowserMemoryBreakdown` and spawns a re-exec'd child process with `APEXLOAD_BROWSER_MEMORY_LIMIT_MB` set so Context 1 is admitted with real `measureBrowserMemoryUsage` (`ContextsUnmeasured == 0`, `MemorySampleFailures == 0`), a subsequent context is refused after refreshing the live sample (`ContextsRefusedByMemory == 1`, `Execute` fails with `browser_memory_admission` and `browser_resource_limit_browser_memory_admission`), and teardown releases all live reservations.
   - Added `TestBrowserMemoryAdmissionActual|TestProcProcessSamplesPreferSmapsRollupPSSOverVmRSS|TestProcProcessSamplesIncludeThisProcess` to the Linux browser step in `.github/workflows/ci.yml`.

---

<a id="roadmap-browser-linux-memory-admission-2026-10-26--2-measured-linux-container-host-qualification-evidence"></a>
### 2. Measured Linux Container & Host Qualification Evidence

| Environment | Context 1 Generator RSS / PSS | Context 1 Driver+Browser Tree `VmRSS` | Context 1 Driver+Browser Tree `Pss` | Tree PIDs | Cgroup `memory.current` / `memory.max` | Child `APEXLOAD_BROWSER_MEMORY_LIMIT_MB` | Refused Context | Counters (`Refused` / `Unmeasured` / `Failures`) |
| :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- | :--- |
| Linux (`apexload-generator:local`, `--memory=2g`) | `29 MiB` / `28 MiB` | `1,015 MiB` | `399 MiB` | `10` | `960 MiB` / `2,048 MiB` | `700 MiB` | `b4` (`683 MiB + 128 MiB > 700 MiB`) | `1` / `0` / `0` |
| macOS Host (`darwin/arm64`) | `30 MiB` / `n/a` | `420 MiB` | `n/a` (`VmRSS` fallback) | `5` | `0 MiB` / `0 MiB` | `647 MiB` | `b3` (`579 MiB + 128 MiB > 647 MiB`) | `1` / `0` / `0` |

Notice that on Linux, summing raw `VmRSS` across the 10-process Playwright driver + Chromium process tree reports `1,015 MiB` (due to shared Zygote mappings), whereas summing `/proc/<pid>/smaps_rollup` `Pss` reports the true proportional resident footprint of `399 MiB` (`427 MiB` total including the `28 MiB` generator process).

---

<a id="roadmap-browser-linux-memory-admission-2026-10-26--3-verification-mutation-testing-go-test--overlay"></a>
### 3. Verification & Mutation Testing (`go test -overlay`)

- **Host & Linux Container Suites:**
  - `APEXLOAD_BROWSER_TESTS=require go test -v ./pkg/protocol/browser -run "TestBrowserMemoryAdmissionActual|TestProcProcessSamples|TestProcessTree|TestBrowserContextAdmission|TestBrowserMemoryUsageMeasuresTheDriverTree" -count=1` -> **PASS** (`2.86s` on macOS; `4.60s` in 2 GiB Linux container).
- **`go test -overlay` Mutation Checks:**
  - `mut1_pss` (`pkg/protocol/browser/evidence_budget.go`: disabled `smaps_rollup` `Pss` branch in `readProcProcessSamples`) -> **Caught** by `TestProcProcessSamplesPreferSmapsRollupPSSOverVmRSS`.
  - `mut2_admission` (`pkg/protocol/browser/evidence_budget.go`: disabled `projected > limit` refusal in `admitBrowserContext`) -> **Caught** by `TestBrowserMemoryAdmissionActual`.

<!-- Original record: docs/qa/reviews/roadmap-browser-live-provisional-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-live-provisional-2026-10-27"></a>

## QA Review Record — `BI-C1`: Provisional Live Browser Findings, Interactions, and Journeys


**Date:** 2026-10-27
**Roadmap Item:** `BI-C1` (`Provisional live findings, interactions & journeys`)
**Option Selected:** Option A — Distributed + HA Live Provisional Browser Intelligence
**Status:** Verified (Zero Open Findings)

<a id="roadmap-browser-live-provisional-2026-10-27--scope-contract-changes"></a>
### Scope & Contract Changes

1. **Incremental Engine & Protocol Flush ([browser_documents.go](../../../pkg/engine/browser_documents.go), [browser_har.go](../../../pkg/engine/browser_har.go), [vuser.go](../../../pkg/engine/vuser.go), [vuser_run.go](../../../pkg/engine/vuser_run.go), [messaging_progress.go](../../../pkg/engine/messaging_progress.go), [touch_actions.go](../../../pkg/protocol/browser/touch_actions.go)):**
   - Added `LiveBrowserDocumentEvidenceSink` in [browser_documents.go](../../../pkg/engine/browser_documents.go) and `LiveBrowserInteractionEvidenceSink` in [browser_har.go](../../../pkg/engine/browser_har.go), wiring `flushBrowserIterationEvidence()` in [vuser.go](../../../pkg/engine/vuser.go) (called from `prepareIteration` and `completeIteration` in [vuser_run.go](../../../pkg/engine/vuser_run.go)) and `flushBrowserInteractions()` in [messaging_progress.go](../../../pkg/engine/messaging_progress.go) (called from `state.sample(final)` in [browser_documents.go](../../../pkg/engine/browser_documents.go)) to fan out raw `protocol.BrowserDocumentEvidence` and `protocol.BrowserInteractionEvidence` at iteration boundaries and every `<= 30 s` alongside the run archive writer while deduplicating when the primary sink implements both interfaces.
   - Bounded WebKit mobile native `locator.Tap()` hit-target checks in [touch_actions.go](../../../pkg/protocol/browser/touch_actions.go) (`webkitElementUnoccludedAtCenter`) so pages without `<meta name="viewport">` fall back cleanly when unoccluded at center while preserving genuine occlusion failures and trusted touch dispatch on viewport-configured pages.

2. **Distributed Generator RPC Transport ([generator.proto](../../../api/proto/generator.proto), [service.go](../../../pkg/distributed/generatorrpc/service.go), [client.go](../../../pkg/distributed/generatorrpc/client.go)):**
   - Extended `MetricsBatch` in [generator.proto](../../../api/proto/generator.proto) with incremental `live_browser_documents_json`, `live_browser_interactions_json`, `live_journey_events` (`LiveJourneyEvent` carrying per-VUser iteration identity and `BrowserAuthoredStep` structural step paths), and `live_browser_truncation` (`LiveBrowserTruncation`).
   - Updated `combineSinks` (`combinedSink`) and `transactionBuffer` in [service.go](../../../pkg/distributed/generatorrpc/service.go) with strict per-window bounds (`maxWindowLiveBrowserDocumentRecords = 256` / `maxWindowLiveBrowserDocumentBytes = 2 << 20`, `maxWindowLiveBrowserInteractionRecords = 256` / `maxWindowLiveBrowserInteractionBytes = 2 << 20`, `maxWindowLiveJourneyEvents = 4096`), malformed-evidence drop accounting (`Clone() == nil`), and truncation counters drained into each `MetricsBatch`.

3. **Controller Live Evaluator, HA Cache Fanout & Stop-and-Join Barrier ([live_browser_intelligence.go](../../../pkg/api/live_browser_intelligence.go), [cache_backend.go](../../../pkg/api/cache_backend.go), [execution_backend.go](../../../pkg/api/execution_backend.go), [browser_findings.go](../../../pkg/api/browser_findings.go), [browser_findings_delivery.go](../../../pkg/api/browser_findings_delivery.go), [browser_interaction_patterns.go](../../../pkg/api/browser_interaction_patterns.go), [journey_accounting.go](../../../pkg/api/journey_accounting.go)):**
   - Added `liveBrowserIntelligenceState` and `ProvisionalBrowserIntelligenceSnapshot` on `ManagedTest` and `RunLiveStateSnapshot` (`provisional_browser_intelligence`), wired `combinedResultSink` in [execution_backend.go](../../../pkg/api/execution_backend.go), and evaluated provisional findings, interaction patterns, and journeys using the exact archive evaluators (`evaluateBrowserFindings`, `newInteractionAnalyzer`, `newJourneyAccumulator`) on a `30 s` ticker (`defaultLiveBrowserIntelligenceInterval`), persisting provisional snapshots to `LiveStateStore` (Redis/Valkey) so HA followers serve identical provisional responses.
   - Enforced controller-side memory bounds (`maxLiveBrowserInteractionRecords = 2048`, `maxLiveBrowserInteractionTotalCount = 100_000`, `maxLiveBrowserJourneyEvents = 100_000`) with `live_truncated: true` and `truncated_reasons` when exceeded (including malformed non-nil batches rejected by `Clone()`), and deduplicated repeated final generator collections by `(target, SourceEpoch, CollectionSequence)`.
   - Marked provisional finding baselines with `status: "not_evaluated_live"` and `reason: "not_evaluated_live"`.
   - Enforced a strict stop-and-join barrier (`stopAndJoinLiveBrowserIntelligence` / `stopAndJoinLiveBrowserIntelligenceByID`) before archive finalization and at the start of `runArchiveAttached`, and released provisional state (`releaseLiveBrowserIntelligenceLocked` clearing both `liveBrowserIntelligence` and `provisionalBrowserIntelligence`) once `ManifestSHA256` is attached so finalized archive verdicts atomically replace provisional live views.

4. **Dashboard Provisional Views ([CommandCenterBrowserGroups.tsx](../../../dashboard/src/components/CommandCenterBrowserGroups.tsx), [BrowserFindingsPanel.tsx](../../../dashboard/src/components/runReport/BrowserFindingsPanel.tsx), [BrowserInteractionsPanel.tsx](../../../dashboard/src/components/runReport/BrowserInteractionsPanel.tsx), [JourneyRiskPanel.tsx](../../../dashboard/src/components/runReport/JourneyRiskPanel.tsx)):**
   - Updated `BrowserFindingsPanel`, `BrowserInteractionsPanel`, `JourneyRiskPanel`, and `CommandCenterBrowserGroups` to render `provisional: true` and `live_truncated: true` badges/notices and `not_evaluated_live` baseline labels, polling while `provisional: true` and atomically replacing provisional views once the archive is finalized.

<a id="roadmap-browser-live-provisional-2026-10-27--verification-mutation-testing"></a>
### Verification & Mutation Testing

- **Go Unit, Integration & Race Tests:**
  - `go test -race -count=1 ./pkg/engine -run "TestBrowser|TestLive|TestCombinedSink|TestTransactionBuffer"`
  - `go test -count=1 ./pkg/protocol/browser -run "TestBrowserProtocolExecuteWebKitActions|TestBrowserProtocolValidationMatrixAcrossRepresentativeDeviceProfiles|TestBrowserTap"`
  - `go test -race -count=1 ./pkg/distributed/generatorrpc -run "TestBrowser|TestLive|TestCombinedSink|TestTransactionBuffer"`
  - `go test -race -count=2 -shuffle=on ./pkg/api -run "TestLiveBrowserIntelligence"`
  - `go test -count=1 ./pkg/api -run "TestBrowserIntelligence|TestRunArchiveBrowserInteractions|TestJourney|TestSwagger"`
- **Frontend Build & Vitest:**
  - `cd dashboard && npm run build`
  - `cd dashboard && npx vitest run src/components/runReport/BrowserFindingsPanel.test.tsx src/components/runReport/BrowserInteractionsPanel.test.tsx src/components/runReport/JourneyRiskPanel.test.tsx src/components/CommandCenterBrowserGroups.test.tsx src/components/CommandCenterPrototype.browserGroups.test.tsx src/labelCasing.test.ts`
- **Mutation Verification (`go test -overlay`):**
  - Mutant 1 (`pkg/api/browser_findings.go` omitting `inputs.provisional` guard returning `not_evaluated_live` in `evaluateFindingBaseline`): killed by `TestLiveBrowserIntelligenceLocalHAFollowerAndArchiveParity`.
  - Mutant 2 (`pkg/api/live_browser_intelligence.go` omitting `seenCollections` deduplication in `observeGeneratorBatch`): killed by `TestLiveBrowserIntelligenceDistributedTwoGeneratorsAndTruncation`.

<!-- Original record: docs/qa/reviews/roadmap-browser-managed-chain-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-managed-chain-2026-10-20"></a>

## Managed browser workflow qualification — 2026-10-20


The controlled fixture drives authenticated script authoring, native replay, a one-iteration managed run, finalized document reports, retained resources, native HAR entry/download and archive/replay ZIP exports through the real controller and native browser. It uses SQLite and only an owned loopback target. No protocol result or archive writer is substituted.

Four local cohorts are exercised: Chromium desktop and Pixel 8 emulation, and WebKit desktop and iPhone 15 Pro emulation. Document/report, resource and native HAR source generations must join. Retained resources and target request counters are independently checked; native HAR bytes must match the finalized digest and ZIP payload. Native runtime and emulation remain distinct from actual devices.

Persisted editor/viewer identities exercise current authorization: viewers can inspect reports/resources and HAR listings, but cannot download native HAR/ZIP or write scripts. Cross-project access is denied. Mobile synthetic gesture dispatch remains explicitly excluded from pattern scoring; desktop native dispatch qualifies.

The fixture waits for the controlled fetch response body before its visible completion signal, so resource retention is compared against completed traffic. Its HAR directory is private and explicitly configured. The exploratory shared-temp run exposed a capture-failure omission gap; fixing that product gap is a separate follow-up, not evidence that this successful path covers failures.

This closes the local authenticated author/replay/run/archive/download path; browser-driven dashboard traversal across Chromium and WebKit in both auth profiles is covered by the [UI-driven managed browser workflow slice](#roadmap-browser-ui-managed-chain-2026-10-27). Real TruClient export fidelity, live/policy/baseline/panel workflows, durable replay HAR recovery, reference-host performance, Linux/Windows and actual-device qualification remain pending.

<a id="roadmap-browser-managed-chain-2026-10-20--validation-and-review"></a>
### Validation and review

- Full API suite: **12,563 passing tests/subtests**, 13 explicit skips; native browser execution required. The private attached HAR import retained every stable compatibility result.
- The four-cohort workflow passed **three race-detector repetitions** (15 tests/subtests).
- Documentation checks passed for **300 active Markdown files**. Embedded dashboard/Help assets remain byte-identical to `79e6f4ad2`, whose Help modules were verified in both auth profiles. No UI implementation changed in this slice.
- Self-review corrected the fixture completion and supported-report findings; the final review found no issues. Review rounds (`evidence/review-rounds.json`), validation summary (`evidence/validation-summary.json`), source hashes (`evidence/source-hashes.json`) and checksums (`evidence/SHA256SUMS`) retain the scope. Controlled native ZIP/report/resource artifacts are retained alongside the compressed logs. Private source HAR bytes are not copied into the repository.

Authored identity across controller-applied runtime overrides remains a separate open item: this fixture qualifies within-run source joins, not replay-to-run script-digest equality.

<!-- Original record: docs/qa/reviews/roadmap-browser-map-cohorts-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-map-cohorts-2026-10-20"></a>

## Browser interaction map cohorts — 2026-10-20


This bounded BI-B slice separates exact recorded scroll cohorts and excludes unknown or conflicting context evidence from maps. Raw version-1 archives remain unchanged; derived analysis reports version 2. Stable authored identity, actual dispatch-time geometry and real-device qualification remain open in the [implementation plan](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md).

<a id="roadmap-browser-map-cohorts-2026-10-20--contract-and-scope"></a>
### Contract and scope

Maps separate route, layout, viewport, device pixel ratio, orientation, engine, profile and exact recorded scroll offsets on both axes. Signed zero is equivalent; fractional offsets remain distinct. An empty profile on a known engine represents the default browser. Missing layouts, untransformed child frames and absent/conflicting contexts contribute once to exclusion coverage. A conflicted context cannot become known again through a later record. The 64-map limit still admits compatible records into existing maps after refusing new cohorts.

The dashboard and HTML/PDF/CSV report views expose scroll cohorts and exclusion counts. Older exported analyses retain their ranges. CLI and MCP help describe the returned analysis version. The [in-app Help source](../../guides/BROWSER_INTELLIGENCE_WORKFLOWS.md#patterns-and-heatmaps) explains post-action geometry and the unchanged capture-time/action-name limitations of pattern detection.

<a id="roadmap-browser-map-cohorts-2026-10-20--validation"></a>
### Validation

Main advanced twice during validation. PR #257 (`a45bd5f02`) was integrated as `8a41940`; PR #258 (`419502149`) was then integrated as `06868f09`. Source merges had no conflicts. The first restoration conflicted only on generated assets, which were rebuilt; saved source hashes verified both restorations. The final validation ran on `06868f09` plus this slice, identified by source and asset hashes (`evidence/final-source-hashes.json`).

The final full Go suite passed 168 packages, with 10 package skips/no-test packages and no failures. Its conditional test skips are retained in the validation summary (`evidence/validation-summary.json`). Chromium and WebKit browser-intelligence golden acceptance and the private HAR import passed. Dashboard build/typecheck, full lint, 225 selected unit tests, bundle budgets and 24 MCP tests passed. Embedded Help equals its source. Browser workflows passed 17 tests with one skip in dev-admin and 16 tests in local-auth, including Help navigation and the new cohort display. The dev-admin run also covered the incoming report-results workflow.

All 10 stable private HAR aggregate fields match the earlier baseline: 803 requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings, zero DSL issues, 264 protected requirements and the source ZIP size. Random generated script identity is excluded from comparison. Capture contents and target hosts are not included in this evidence bundle. Review rounds (`evidence/review-rounds.json`) record fixes and a final review with no remaining findings. Evidence checksums (`SHA256SUMS`) make the retained results verifiable.

The first PR #257 browser attempt passed dev-admin but stopped at the fresh-main gate when PR #258 arrived; both profiles were rerun against the final bundle. Earlier results remain historical evidence and do not replace the final gates. Observed wall-clock gaps are recorded separately and these runs make no performance qualification claim.

An interruption removed the earlier temporary logs and process. Their reported results are not used as this record's final gate; fresh results are retained under evidence (`evidence/`). The first resumed targeted Go run found an incorrect expected count in the updated archive test: its three gestures all require exclusion when document evidence is unavailable. Both truncated and unreadable-document assertions now use the fixture population. The failed attempt is preserved. The corrected targeted run passed. A subsequent full Go run passed the private HAR import but the API package hit its 20-minute timeout during repeated contract generation with a 2 GiB memory limit and GOGC=25. Remaining packages from that old tree were stopped when main advanced. This invocation remains failed/interrupted; the integrated rerun uses GOMAXPROCS=4, GOGC=100 and a 6 GiB limit.

<a id="roadmap-browser-map-cohorts-2026-10-20--qualification-limits"></a>
### Qualification limits

The dashboard map test uses a clearly labeled response fixture over a real archived HTTP run. It verifies rendering and drill-down behavior, not native browser capture or device qualification. No changes execute or replay the private HAR's target traffic. This slice does not close BE-A–C or BI-A–C: dispatch-time provenance, authored identity/outcomes, real supported TruClient exports, real-device/platform and density cohorts, HAR recovery/ownership, baseline/policy workflows and broader evidence-chain acceptance remain subject to their existing gates.

<!-- Original record: docs/qa/reviews/roadmap-browser-metric-floors-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-metric-floors-2026-10-21"></a>

## Configured browser metric floors — 2026-10-21


Browser policies can set separate LCP, FCP and INP baseline effect floors. The API and policy editor accept optional values from 50 to 600,000 ms. The existing browser_baseline.v1 estimator applies the largest of the metric floor, overall absolute floor and relative floor. These values are independent of threshold-score metric inclusion and cannot lower existing thresholds.

Runs resolve floors through their immutable policy revision pins; editing a policy later does not rewrite historical inputs. Missing or legacy per-metric configuration keeps existing defaults. Invalid internal floors leave the affected metric unscored. Compatible-run selection and real two-run qualification remain separate work.

<a id="roadmap-browser-metric-floors-2026-10-21--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,729 passing tests/subtests**, 38 explicit skips, zero failures; native browser tests required. The supplied private HAR import retains every stable compatibility result.
- Focused policy/baseline race coverage: **168 passing tests/subtests** over three repetitions, including old pinned revisions, global/relative floor clamps, invalid values and admission policy pins.
- Dashboard typecheck, lint, 37 policy-editor/Help unit tests, generated contracts and build passed. Browser intelligence/editor/Help workflows passed in both profiles: dev-admin 18 pass/one auth-specific skip; local-auth 19 pass. Narrow editor and Help screenshots were reviewed. The embedded article matches the repository source, and documentation links passed.
- Review corrected the roadmap placement/status finding before application; the final round found no issues. Local records: `evidence/review-rounds.json`, `evidence/validation-summary.json`, tested source/asset hashes and `evidence/SHA256SUMS`.

The initial race filter unnecessarily included unrelated generated-code tests and was interrupted; those diagnostics are retained separately. The focused race run and required full Go suite are the acceptance evidence. The first full attempt exposed the independently reviewed [CDP teardown race](#roadmap-browser-cdp-close-2026-10-21); the successful full run includes that fix. Raw evidence remains local; only this Markdown record is committed. No private HAR source bytes or external qualification claims are included.

<!-- Original record: docs/qa/reviews/roadmap-browser-native-har-feature-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-native-har-feature-2026-10-26"></a>

## Native HAR transfer capability negotiation — 2026-10-26


Previously native HAR transfer advertised no runtime capability of its own, so mixed fleets degraded silently: a generator older than native HAR transfer or running on a platform without qualified HAR ownership ran browser scripts that requested HAR capture without declaring or transferring native HARs, and refusing only inside an individual peer's `AssignWork` RPC could still let earlier peers run `PreflightBrowserSessions` target traffic before a later peer rejected staging.

Browser generators now advertise `browser_native_har_v1` from `browserRuntimeFeatures` only when `browser.NativeHARSupported()` holds on that host platform (`SupportsBrowserNativeHAR()`). `dsl.RequiredBrowserRuntimeFeatures` emits `browser_native_har_v1` whenever a browser script's effective HAR capture policy (`har_capture_policy` overriding `har_capture`, matching the engine and browser runtime) resolves to `always`, `on_error`, `on_sla_breach`, or `manual`.

Controllers enforce `browser_native_har_v1` across placement (`groupRequiredRuntimeFeature`, preserved across lightweight capacity snapshots and terminal runtime compaction), pre-assignment validation across all planned targets before any peer receives `DistributeScript` or `AssignWork` (`distributed_execution_backend.go`), `Client.AssignWork` heartbeat capability negotiation (`browser_native_har_v1_capability_not_negotiated`, without forcing `PrepareOnly` on normal runs so observer session preflight continues to run when all peers qualify), shard evacuation (`applyEvacuationShardAdjustments`), and generator staging (`execution_staging.go`). Browser scripts whose effective HAR policy is `off` do not require `browser_native_har_v1` and continue to run across mixed fleets. A controller older than native HAR transfer still neither requires `browser_native_har_v1` nor fetches or acknowledges a newer generator's HARs, as documented in [Browser HAR working-file ownership](../../BROWSER_EVIDENCE_ARCHITECTURE.md#run-archive-transfer-and-receipts).

<a id="roadmap-browser-native-har-feature-2026-10-26--validation-and-review"></a>
### Validation and review

- Targeted race and shuffle runs (`-race -count=2 -shuffle=on`) across `pkg/dsl`, `pkg/protocol/browser`, `pkg/distributed/generatorrpc`, and the `pkg/api` native HAR capability and snapshot suites passed, verifying zero target HTTP or observer-preflight requests when any peer in a mixed fleet omits `browser_native_har_v1`.
- Mutation verification via `go test -overlay` confirmed the new tests fail when HAR feature emission in `dsl.RequiredBrowserRuntimeFeatures`, platform gating in `browserRuntimeFeatures`, or `Client.AssignWork` capability negotiation is neutralized.
- Dashboard Help unit/parity tests, `go vet`, `go build ./...`, and `python3 scripts/check-docs.py` passed.
- Independent review rounds checked code safety and test/documentation fidelity; all findings were resolved before commit.

<!-- Original record: docs/qa/reviews/roadmap-browser-native-lifecycle-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-native-lifecycle-2026-10-20"></a>

## Native browser lifecycle fixtures — 2026-10-20


Two independent native lifecycle fixtures extend the existing document evidence checks. They change no runtime defaults or wire contracts.

- **WebKit process loss:** an isolated child owns desktop and iPhone 15 Pro emulation contexts. The fixture verifies the exact WebKit executable under its own Playwright driver before killing it, observes native disconnect, preserves completed measurements, censors unfinished measurements, and verifies a fresh pool replacement with a new source generation. Later teardown cannot revise the lost populations or damage the replacement.
- **Genuine BFCache restore:** isolated desktop and Pixel 8 emulation contexts use bundled full Chromium with `--disable-back-forward-cache` removed. Trusted persisted `pageshow`, a preserved heap marker/time origin and one initial target request establish actual restoration. The original finalized FCP remains unchanged, restored measurements have a distinct lifecycle, and teardown leaves no provisional or duplicate measurements.

The BFCache fixture deliberately differs from the pinned default launch, which uses headless shell and disables BFCache. Its evidence cannot promote default-runtime BFCache support. macOS emulation is not an actual iPhone/Pixel device, Linux/Windows qualification or reference-host performance evidence.

<a id="roadmap-browser-native-lifecycle-2026-10-20--validation-and-review"></a>
### Validation and review

- The complete browser package passed **779 tests/subtests**, with three explicit benchmark/platform skips (`TestBrowserBenchmark`, `TestProcProcessSamplesIncludeThisProcess`, `TestBrowserVUserMarginalRSS`). Native runtime tests were required.
- Both new lifecycle fixtures passed **three race-detector repetitions** (six outer tests); BFCache exercises both desktop and Pixel 8 emulation in every repetition.
- Documentation checks passed for **299 active Markdown files**. Embedded assets are byte-identical to the preceding reviewed slice, including the Help modules verified there in both auth profiles. This test-only slice changes no dashboard or runtime behavior.
- Self-review fixed the roadmap prose finding; the final review found no issues. Review rounds (`evidence/review-rounds.json`), validation summary (`evidence/validation-summary.json`), source hashes (`evidence/source-hashes.json`) and checksums (`evidence/SHA256SUMS`) retain the scope and commands.

The exploratory BFCache logs are retained separately: headless-shell attempts did not restore, and the first full-Chromium attempt used an unsuitable `load` wait. The final fixture waits for native history commit and independently verifies restoration. These earlier failures are diagnostic evidence, not passing qualifications. Runtime versions and viewport/platform details appear in the native logs. The successful local cohorts use Chromium 149.0.7827.55 and WebKit 26.5 on macOS/arm64.

<!-- Original record: docs/qa/reviews/roadmap-browser-native-waterfall-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-native-waterfall-2026-10-21"></a>

## Rendered native waterfall/cache qualification — 2026-10-21


The controlled HTTP fixture retains independent receive/finish clocks, methods, user agents and request counts. Actual Chromium/WebKit desktop and Pixel/iPhone emulation runs retain native HARs and request evidence, then exercise the real resource inspector in both dashboard authentication profiles. The fixture uses no seeded measurements or intercepted API responses. Chromium must prove network/cache transitions; unexposed WebKit cache/document attribution remains explicitly unavailable. The displayed waterfall is reconciled only for a proved document join.

The server's delayed CSS response is checked against native request timing and HAR duration on this host. No reference-host performance, remote clock synchronization, actual-device or other-platform claim follows. Absolute navigation URLs leave the separate controller HTTP latency probe unconfigured; a retained diagnostic run proves that probe caused an earlier extra HEAD request. The capture assertion remains strict.

<a id="roadmap-browser-native-waterfall-2026-10-21--validation-and-review"></a>
### Validation and review

- **24 real browser cases passed**: three repetitions of four engine/profile cohorts in each authentication profile. Independent ledgers, raw native HAR timing, retained resource receipts and rendered evidence are retained locally.
- **81 focused native assertions passed** under the race detector across three repetitions. The external private HAR compatibility test passed with unchanged stable results.
- Dashboard typecheck/lint, focused units, generated embedded assets, exact Help-source comparison, both-profile Help workflows and documentation links passed. Narrow screenshots were inspected; scrollable inspection columns were positioned visibly for evidence capture.
- Review corrected the protected HAR listing, isolated runtime temp root (preceding slice), profile spelling, cache configuration and separate controller probe. Missing WebKit joins/cache provenance stay unavailable. Final review found no remaining findings in this slice.

This test/documentation slice does not change production measurement code. The full Go/dashboard merge gates remain required; this record reports its focused qualification only. Raw evidence remains local under `evidence/`, including `validation-summary.json` and `SHA256SUMS`; only this Markdown record is committed.

<!-- Original record: docs/qa/reviews/roadmap-browser-original-baseline-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-original-baseline-2026-10-21"></a>

## Browser baseline original-source qualification — 2026-10-21


<a id="roadmap-browser-original-baseline-2026-10-21--behavior"></a>
### Behavior

RCA browser baseline qualification now checks each shared scenario group's
retained `BrowserAuthoredSources` descriptor alongside its executable script
identity. Both descriptors must pass the existing version/digest validation and
match in original script and structure digests. Equal executable forms alone
cannot establish equal authored journeys. Equal originals cannot override an
existing executable mismatch or missing runtime identity.

Missing or malformed original provenance leaves the affected group's browser
comparison unscored through the existing `cohort_changed` result. An unrelated
group with matching valid original and executable identities remains comparable.
No original identity is synthesized for historical archives. This guard is
consumed only by browser detection; general RCA workload qualification is unchanged.

The archive adapter advances to `archive.7`, preventing new requests from reusing
assessments under the earlier qualification behavior. Stored historical results
remain readable. The estimator remains `browser_baseline.v1`; policy floors,
statistical parameters, archive formats and public API fields are unchanged.

<a id="roadmap-browser-original-baseline-2026-10-21--validation-and-review"></a>
### Validation and review

Latest `origin/main` was fetched and already integrated before validation.

- `go test -race ./pkg/api ./pkg/rca -run
  'Test(RCA|BrowserBaseline|BrowserPolicyMetricFloors|EngineVersion)' -timeout=5m`
  passed: API 133.710 seconds, RCA 1.283 seconds. Tests cover matching source and
  execution, changed source content/structure, missing source on either or both
  sides, malformed descriptors, runtime mismatches, nil/empty inputs, unrelated
  groups, independent result maps and the archive-to-RCA evidence path.
- Documentation checks passed for 329 active Markdown files. Whitespace checks
  passed. No route/API schema changes require generated artifact updates.
- The review traced original-source provenance through archive digest validation,
  baseline materialization and browser-only detection. It checked that original
  identity cannot override executable mismatches, that historical missing data
  stays unscored and that the adapter bump prevents stale assessment reuse. The
  final review round found no issues.

Raw logs are `/private/tmp/apex-browser-durable-backend/authored-baseline-race.log`
and `/private/tmp/apex-browser-durable-backend/authored-baseline-docs.log`.
The full Go suite remains the CI merge gate. There were no UI/import changes;
the preceding slice's private HAR import evidence and earlier browser verification
evidence are preserved, not represented as new runs here.

<a id="roadmap-browser-original-baseline-2026-10-21--remaining-work"></a>
### Remaining work

This is an additional prerequisite for latest-compatible selection, not its
activation. Clock/source binding, complete source-window qualification, frozen
selection receipts, browser-findings integration and real two-run qualification
remain open. No browser UI, HAR importer or recording behavior changes here.

<!-- Original record: docs/qa/reviews/roadmap-browser-original-source-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-original-source-2026-10-21"></a>

## Original authored-source binding — 2026-10-21


Managed browser creation binds an original source/structure descriptor before runtime overrides. Version-2 step identity is distinct from version-1 exact-engine-input identity. The engine validates structure before binding; replay events and retained managed interactions preserve the same authored definition, and archive provenance retains group source descriptors. Definition identity does not prove dispatch or runtime equivalence.

Compiled restart retains persisted proof. Legacy repair from a mutable script library clears original-source proof. Public DSL/runtime/replay JSON cannot mint trusted descriptors. Prepared generator work negotiates a fresh capability and exact acknowledgement, with source fields in assignment retry fingerprints. Old or stale peers and immediate assignments keep legacy identity.

Authored page identity, durable replay HAR, full compatible-baseline selection and external platform/device/reference-host qualification remain separate work.

<a id="roadmap-browser-original-source-2026-10-21--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,753 passing tests/subtests**, 38 explicit skips, zero failures, with native browser tests required. Private HAR import stable results match the prior baseline.
- Focused race coverage: **213 passing tests/subtests** over three repetitions, including native Chromium/WebKit desktop and mobile-emulation replay/run/archive chains.
- Dashboard typecheck, lint, targeted units, generated contracts, embedded build and Help-source verification passed. Browser intelligence, HAR, recording review and Help workflows passed in both auth profiles; Help screenshots were reviewed. Documentation links passed.
- Review corrected mutable-library restart proof and integer-narrowing findings in the prototype. Visual review also corrected a viewport-resize screenshot displacement; the target Help section was rechecked in both profiles. Full-suite review also found an oversized prepared group VUser startup frame. Construction now occurs outside the parked frame; five normal repetitions and race checks preserve the existing stack bound. Final applied review found no remaining issues. Local records include `evidence/review-rounds.json`, `evidence/validation-summary.json`, source/asset hashes and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. No private HAR source bytes or unavailable external qualification claims are included.

<!-- Original record: docs/qa/reviews/roadmap-browser-outcome-clocks-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-outcome-clocks-2026-10-21"></a>

## Observed post-action Sync clocks — 2026-10-21


Raw interaction evidence v3 adds optional actual wait clocks and freezes outcome context at the wait's return. Later delays and capture cannot change that result. Invalid/missing clocks and successes observed after the deadline remain unqualified. Raw v1/v2 interpretation and serialization stay unchanged; exact fresh generator capability/staging negotiation governs v3. Pattern windows continue to use native dispatch.

This slice covers supported Sync waits, not the wider authored assertion/dialog/download/navigation/state composition, page identity or external device/platform qualification.

<a id="roadmap-browser-outcome-clocks-2026-10-21--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,796 passing tests/subtests**, 38 explicit skips. Its only failure was the generated QA dashboard's tracked E2E count after the preceding policy commit. The one-line generated repair (`64dc75990`) passed the complete `docs/qa` package (47 tests/subtests); all other full-suite checks passed with native browser tests required. The failed original run and successful recheck are retained separately. Private HAR import stable results match the prior baseline.
- Focused race coverage: **237 passing tests/subtests** over three repetitions, including native Chromium/WebKit desktop and mobile-emulation archive chains.
- Dashboard typecheck, lint, targeted units, generated contracts, embedded build, exact Help-source verification and both browser authentication profiles passed. Help screenshots were inspected; documentation links passed.
- Review fixed legacy-version rejection fixtures and ensured outcome classification uses context state at wait return. Missing clocks, success observed after deadline and timeout observed before deadline cannot qualify an authored result. Final applied review found no remaining issue. Local records include `evidence/validation-summary.json`, source/asset hashes and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. No actual-device or external platform qualification is claimed.

<!-- Original record: docs/qa/reviews/roadmap-browser-panel-pins-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-panel-pins-2026-10-21"></a>

## Frozen browser comparison panels — 2026-10-21


Explicit query v4 retains evaluated run/publication, tuple and canonical query identities. Read and save validate actual authorized archive bytes. Changed, deleted or inaccessible inputs remain unavailable; opening from another run never retargets the saved comparison. Existing queries and metadata-only historical edits preserve their semantics. Baseline-only cohorts retain inspectable buckets and CSV rows with explicit missing current values/counts. This does not establish baseline eligibility or cross-publication transactionality.

<a id="roadmap-browser-panel-pins-2026-10-21--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,909 passing tests/subtests**, 38 explicit skips, zero failures. The private HAR import stable fields match the prior baseline.
- Focused race checks: **249 passing tests/subtests** over three repetitions, including publication integrity and saved query contracts.
- Full dashboard cycle, both authentication profiles, generated contracts, documentation links and exact embedded Help passed. Two controlled native browser runs exercise explicit older policy revision selection, comparison/save, cross-run reopen, pinned CSV, and deleted-input refusal. Local-auth also verifies scoped viewer inspection and separate write/HAR-download denial. Final typecheck and lint were repeated after the screenshot adjustment. The corrected native workflow was also rerun in both profiles after adding an explicit viewport assertion for the deleted-input screenshot. Final screenshots were inspected; the earlier full-cycle dev-admin capture did not show that offscreen state and is not used as visual proof.
- Final review found no remaining findings. Local evidence includes `evidence/validation-summary.json`, source/asset hashes and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. This preserves exact inputs; it does not establish baseline anomaly eligibility or qualify actual devices/external hosts.

<a id="roadmap-browser-panel-pins-2026-10-21--findings-addressed"></a>
### Findings addressed

- **P2 — Baseline-only cohorts:** retain their actual table and CSV buckets, including real zero values, while current values/counts remain explicitly missing.
- **P2 — Combined comparison limits:** enforce the selected series limit (at most 20) and 20,000-row budget over the union of current and baseline inputs, including different runtime cohorts. Both query v3/v4 previews and frozen saves reject an excessive union.
- **P3 — Visual proof:** scroll the deleted-input panel into view and assert its viewport visibility before capturing the screenshot. The corrected workflow passed in both authentication profiles and its captures were inspected.
- **P3 — Future-version fixture:** change the unknown-version test from v4 (now reserved for frozen browser inputs) to v5. Production rejection was correct; the old fixture expected the generic future-version error. The first full run had 35,908 passes, 38 skips and this one failing test; its complete logs and hashes remain local as `before-version-test-fix-*`.
- **P3 — Validation attribution:** capture the committed base alongside source/asset hashes and reject evidence packaging or commit if that base changes during validation.

<a id="roadmap-browser-panel-pins-2026-10-21--requested-public-site-recording"></a>
### Requested public-site recording

An actual APEX recording visited Salesforce home and clicked **What is CRM?**, **What is Salesforce?**, and **Customer Success**. The capture contains three clicks, six steps and 1,207 HAR requests. Its 103,706,431-byte HAR has SHA-256 `b1fd2641e8a3ca3c0c68d404a25bdd32863145bc35ea2312eaf1037f45fe68c3`. Multipart import returned HTTP 200, preserved the capture hash and produced zero DSL validation issues. Capture warnings include bounded response-body omission, background request/console failures, WebSockets and readiness hints.

One sequential replay was attempted and refused by Salesforce with HTTP 403 on initial navigation. **Live Salesforce replay is not qualified.** The successful recording/import does not replace the controlled browser comparisons above. Raw captures, converted DSL, import summary and failed replay report remain local under `output/playwright/salesforce-20261021/`; they are not committed. The original supplied private HAR ZIP has a separate compatibility check.

<!-- Original record: docs/qa/reviews/roadmap-browser-panel-pins-v2-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-panel-pins-v2-2026-10-27"></a>

## QA Review — Saved Panel Policy and Baseline Selection Pins v2 (`BI-C3`) (2026-10-27)


<a id="roadmap-browser-panel-pins-v2-2026-10-27--scope-parent-requirement"></a>
### Scope & Parent Requirement

- **Roadmap Track:** `BI-C` (`BI-4`: Complete policy, panel and evidence workflows)
- **Package:** `BI-C3` (`query_version: 5`, `storage.BrowserPanelInputs` `version: 2`, `BaselineComparisonWorkspace` "Save to panel", and `apexctl analysis replay-panel`)
- **Acceptance Authority:** [ROADMAP_IMPLEMENTATION_SPEC.md](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md), [BROWSER_INTELLIGENCE_WORKFLOWS.md](../../guides/BROWSER_INTELLIGENCE_WORKFLOWS.md)

<a id="roadmap-browser-panel-pins-v2-2026-10-27--contract-invariants"></a>
### Contract & Invariants

1. **Versioned Saved Browser Panel Inputs (`version: 2`, `query_version: 5`):**
   - [`BrowserPanelInputs`](../../../pkg/storage/browser_panel_inputs.go) (`version: 2`, bound to `query_version: 5`) extends `version: 1` (`query_version: 4`) with `policy_pins` (`[]BrowserPanelPolicyPin`: `kind` in `{"browser_policy", "journey_policy"}`, `id`, `revision >= 1`, 64-hex `sha256` in strictly ascending `(kind, id)` order) and `selection_receipt_sha256` (optional 64-hex SHA-256 digest of the RCA `browser_selection` receipt), plus per-run `policy_pins` on `BrowserPanelRunInput`.
   - `version: 1` (`query_version: 4`) queries and saved panels remain valid and forbid `version: 2` policy/selection fields so existing saved panels open unchanged.
2. **Server-Side Policy Tombstone & Selection Receipt Verification (`POST /api/v1/tests/{id}/archive/native-series` and `validateSavedBrowserPanelInputs`):**
   - [`verifyAndPopulateBrowserPanelV2`](../../../pkg/api/browser_panel_inputs.go) verifies every `browser_policy` and `journey_policy` pin against both the pinned revision (`GetAnalysisDefinition(projectID, id, revision)`) for SHA-256 digest integrity and the head revision (`GetAnalysisDefinition(projectID, id, 0)`) to detect policy tombstones (`Deleted == true`). Advancing a policy to a newer non-deleted revision keeps historical pinned revisions valid; deleting a policy refuses `query_version: 5` panel reads (`422`) and saves (`400`).
   - When a completed RCA analysis matches the current archive digest and pinned browser policy (`rcaBrowserBaselineMatchesPolicy`) and is compatible with `comparison_run_id`, `verifyAndPopulateBrowserPanelV2` populates `selection_receipt_sha256` from `payload.Inputs.BrowserSelection`, and [`bindBrowserPanelInputs`](../../../pkg/api/browser_panel_inputs.go) refuses any mismatch against saved `BrowserInputs`.
3. **Comparison → Saved Panel → Report → CLI Replay:**
   - [`BaselineComparisonWorkspace.tsx`](../../../dashboard/src/components/baselineComparison/BaselineComparisonWorkspace.tsx) adds **Save to panel**, freezing a `query_version: 5` separate-tuple browser document comparison query via [`freezeBrowserPanelDefinition`](../../../dashboard/src/components/browserPanelInputs.ts) and persisting the project dashboard definition.
   - [`BrowserTupleArchivePanel.tsx`](../../../dashboard/src/components/BrowserTupleArchivePanel.tsx) renders and exports (`selectionReceiptPin`, `policyPins`) the `version: 2` selection receipt and policy pins in CSV and the Run Report panel inspector.
   - [`replaySavedPanel`](../../../cmd/apexctl/analysis_workflows.go) implements `apexctl analysis replay-panel --id <panel_id> [--revision <n>]`, fetching the saved dashboard definition (verifying the requested revision when `--revision` is passed), re-executing each panel query against `POST /api/v1/tests/{id}/archive/native-series`, and verifying that the server's echoed `browser_inputs` match the saved pins.

<a id="roadmap-browser-panel-pins-v2-2026-10-27--validation"></a>
### Validation

- **Storage & API Unit/HTTP Race Suites:**
  - `go test -race -count=2 -shuffle=on ./pkg/storage -run 'TestBrowserPanel|TestValidateNativeArchiveQuery|TestDashboardDefinition'`
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestBrowserPanel|TestSwagger|TestOpenAPI'`
  - `go test -race -count=2 -shuffle=on ./cmd/apexctl -run 'TestAnalysisPanel|TestAnalysisReplayPanel'`
- **Dashboard Unit & Build Verification:**
  - `cd dashboard && npm run typecheck && npx vitest run src/components/browserPanelInputs.test.ts src/components/ArchivedNativePanel.inputs.test.tsx src/components/NativePanelAuthoring.inputs.test.tsx src/components/baselineComparison/BaselineComparisonWorkspace.test.tsx src/labelCasing.test.ts && npm run build`
- **Mutation Testing (`go test -overlay`):**
  - `m1_skip_tombstone_check`: Bypassed the head-revision tombstone check (`latestDef.Deleted`) in `verifyAndPopulateBrowserPanelV2` (`pkg/api/browser_panel_inputs.go`) — killed by `TestBrowserPanelInputPinsHTTPV2PolicyTombstoneAndSelectionReceipt`.
  - `m2_skip_selection_receipt_bind`: Omitted `expected.SelectionReceiptSHA256 != pins.SelectionReceiptSHA256` in `bindBrowserPanelInputs` (`pkg/api/browser_panel_inputs.go`) — killed by `TestBrowserPanelInputPinsV2PolicyAndSelectionPins` and `TestBrowserPanelInputPinsHTTPV2PolicyTombstoneAndSelectionReceipt`.

<a id="roadmap-browser-panel-pins-v2-2026-10-27--review"></a>
### Review

- **Round 1 (`BE-B3 Code Safety Reviewer` & `BE-B3 Test and QA Reviewer`):**
  - `BE-B3 Test and QA Reviewer`: 0 issues found.
  - `BE-B3 Code Safety Reviewer`: Found 4 issues (1 Medium, 3 Low) — all fixed:
    1. **[Medium]** `verifyAndPopulateBrowserPanelV2` performed redundant DB lookups on untrusted `q.BrowserInputs` policy pins and could bind a `selection_receipt_sha256` from a refused/mismatched RCA baseline selection when `q.ComparisonRunID != ""`. Fixed by verifying only manifest-derived `response.Current.policyPins` and `response.Baseline.policyPins` in `verifyAndPopulateBrowserPanelV2` (leaving `bindBrowserPanelInputs` to enforce exact equality with `q.BrowserInputs`) and requiring `validateRCABrowserSelection(payload) == nil` and baseline test ID compatibility before binding `selection_receipt_sha256`.
    2. **[Low]** Frontend `verifyBrowserPanelInputs` did not enforce trimmed `pin.id`, strictly ascending `kind`, or `policy_pins == current.policy_pins` parity with `pkg/storage`. Fixed in `dashboard/src/components/browserPanelInputs.ts` and updated v2 test fixtures.
    3. **[Low]** `BaselineComparisonWorkspace.tsx` `saveToPanel` could generate invalid `end_seconds <= start_seconds` or `> 1000` buckets with custom windows and did not prefer the `browser_documents` row matching `baselineRunId`. Fixed by clamping window bounds, scaling `bucket_seconds`, and preferring the `baselineRunId` document row.
    4. **[Low]** `apexctl analysis replay-panel` silently ignored `--file` and `--project`. Fixed in `cmd/apexctl/analysis_workflows.go` to reject `--file` and verify `def.ProjectID` when `--project` is supplied.
- **Round 2 (`BE-B3 Code Safety Reviewer` & `BE-B3 Test and QA Reviewer`):**
  - `BE-B3 Test and QA Reviewer`: 0 issues found.
  - `BE-B3 Code Safety Reviewer`: Found 1 Low regression (`verifyBrowserPanelInputs` dropped `tuple_ids` validation when rewritten). Fixed by restoring `validTupleList` in `dashboard/src/components/browserPanelInputs.ts` and adding an out-of-order `tuple_ids` assertion in `dashboard/src/components/browserPanelInputs.test.ts`.
- **Round 3 (`BE-B3 Code Safety Reviewer` & `BE-B3 Test and QA Reviewer`):** 0 issues found (0 High, 0 Medium, 0 Low).

<a id="roadmap-browser-panel-pins-v2-2026-10-27--remaining"></a>
### Remaining

- Report/RCA selection provenance when `browser_baseline` is `nil` and `apexctl schedule` / `ci run` / `run --api` policy flags (`BI-C4`).
- Provisional 30 s live findings, interactions, and journeys (`BI-C1`).
- Unstubbed full-chain Playwright E2E across both auth profiles (`BI-C5`).

<!-- Original record: docs/qa/reviews/roadmap-browser-pinned-admission-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-pinned-admission-2026-10-21"></a>

## Browser pinned baseline admission — 2026-10-21


<a id="roadmap-browser-pinned-admission-2026-10-21--scope"></a>
### Scope

BI-A now resolves an omitted RCA baseline from the current run's immutable
`browser_policy` revision when its mode is `pinned_run`. Admission persists the
resolved ID in the existing analysis request and its archive evidence digest in
the existing frozen inputs. Execution never consults a newer policy revision to
select a replacement. Explicit caller baselines retain their existing behavior;
the browser detector still enforces the run's policy independently of general RCA.

Same-project authorization, self-baseline rejection and archive availability
checks apply to policy-selected runs. Both explicit and policy-selected baselines
wait while their archives are finalizing. A changed baseline after admission
remains unavailable evidence instead of being silently replaced. Missing or
invalid policy pins retain the existing disclosed policy-unavailable behavior;
a policy-store failure refuses admission so it can be retried.

The archive adapter identity advances from `archive.5` to `archive.6`; saved
historical assessments remain readable, and queued assessments under an older
engine identity require a new request. The `browser_baseline.v1` estimator,
thresholds, stored schema and API field shapes are unchanged.

<a id="roadmap-browser-pinned-admission-2026-10-21--validation-and-review"></a>
### Validation and review

Latest `origin/main` was fetched and already integrated before validation.

- RCA/browser baseline race checks passed: `pkg/api` 161.130 seconds and
  `pkg/rca` 1.092 seconds. The selection covered `TestRCA*`, browser baseline,
  metric-floor, engine-version and embedded API documentation tests.
- The attached private HAR import regression passed without race instrumentation
  in 53.839 seconds: HTTP 200, 803 requests, 476 actions, 327 filtered assets,
  479 assertions, 37 extractors, 43 correlations, four warnings, 264 protected
  requirements and zero DSL validation issues. Its original ZIP hash is unchanged.
  All summary fields match the preceding compatibility run except the existing
  timestamp-sensitive script hash. No captured target was replayed.
- Documentation validation passed for 328 active Markdown files; whitespace
  checks passed. No dashboard, route or API schema changes require regeneration.
  Browser UI validation from earlier slices is not claimed as a fresh run here.
- The full Go suite remains the CI merge gate; this slice used targeted validation.

Review covered selection precedence, immutable policy revisions, queue reuse,
project authorization, finalization, changed baseline evidence, automatic queue
callers and engine-version compatibility. The review/fix cycle added the baseline
finalization guard, corrected stale cadence documentation and corrected test
fixtures to create a separate foreign-project run rather than attempting an
unsupported project move. The final review round found no further issues.

A combined race run with the large private HAR fixture exhausted its five-minute
budget inside import. That attempt is retained and is not a passing race-import
qualification. Separate RCA race checks and the established non-race import
regression passed afterward.

Raw logs are local under `/private/tmp/apex-browser-durable-backend/`:
`pinned-baseline-final-rca-race.log`, `pinned-baseline-private-har.log`,
`pinned-baseline-private-har-summary.json`, `pinned-baseline-docs.log`;
`pinned-baseline-final-race.log` retains the timed-out combined attempt.

<a id="roadmap-browser-pinned-admission-2026-10-21--remaining-qualification"></a>
### Remaining qualification

This slice does not activate `latest_compatible` search or browser-findings job
selection. Full authored identity, clock/source coverage and comparable-window
qualification remain required before automatic compatible selection. The real
two-run seeded-regression gate and missing external environments remain pending.
No dashboard code, recorder/import code or private HAR fixture was changed.

<!-- Original record: docs/qa/reviews/roadmap-browser-policy-selection-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-policy-selection-2026-10-21"></a>

## Immutable browser/journey policy selection — 2026-10-21


New run and revised queued copies can choose exact immutable browser/journey policy revisions. Drafts, copies, held/delayed runs and durable reloads retain selections. Omission preserves defaults; an explicit empty selection clears a copied selection. The server validates project, kind, revision and deletion visibility, computes the digest and freezes pins through the existing admission identity and ownership fences. Failed admission does not expose a candidate receipt; retries do not drift to a newer revision. Already admitted historical pins remain unchanged.

Schema release 73 adds an optional test configuration column. The selected revision and latest tombstone use one SQL snapshot on SQLite and PostgreSQL. Later policy edits do not rewrite a selection; deletion visible at resolution refuses admission. Deletion after that read has the same resolution boundary as existing defaults. Recurring schedule authoring still uses project defaults.

<a id="roadmap-browser-policy-selection-2026-10-21--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,765 passing tests/subtests**, 38 explicit skips, zero failures, with native browser tests required. Private HAR import stable results match the prior baseline.
- Focused race checks: **162 passing tests/subtests** over three repetitions; disposable PostgreSQL race checks: **3** passing tests/subtests.
- The full dashboard cycle ran 725 unit files / 8,086 unit tests and both complete authentication profiles. Its only browser failure was an existing Help heading sampled before ResizeObserver realignment. The separate test-only repair (`6972519be`) passed 20 repetitions in each profile; every other workflow passed in the complete cycle. Generated contracts, exact embedded Help and documentation links passed. Native managed browser chains retain archive/report/HAR evidence; real HTTP run fixtures verify policy pins and admission denial without claiming browser measurements.
- Final review found no remaining issues. Local evidence includes `evidence/validation-summary.json`, source/asset hashes, browser screenshots, the full dashboard-cycle log and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. This slice does not qualify native devices, external hosts, TruClient exports or baseline inference.

<!-- Original record: docs/qa/reviews/roadmap-browser-process-loss-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-process-loss-2026-10-20"></a>

## Chromium browser-process loss and pool replacement — 2026-10-20


This BE-A regression uses a real managed Chromium process with two concurrent contexts: the default desktop viewport and Pixel 8 emulation. The native CDP `Browser.crash` command terminates the whole browser process. The test runs in its own subprocess so it cannot terminate the parent suite's pooled browsers or an operator's browser.

<a id="roadmap-browser-process-loss-2026-10-20--contracts-and-scope"></a>
### Contracts and scope

The test observes native disconnection, checks both contexts' retained document measurements before managed teardown, and requires the pool to create a connected replacement browser with an independent source generation. Completed metrics must remain unchanged; pending measurements must be censored without invented values or qualification. Playwright reports this process loss through page/context closure, so the retained boundary is `context_closed`, not an invented renderer-crash classification. Reopening the pool and later tearing down the lost contexts must not alter those populations. The existing independent page viewport/pixel-ratio oracle and runtime cohort record identify the engine/profile under test.

No production capture, import, archive, Help or wire contract changes. No source recording is replayed. This is local Chromium/macOS evidence only. Pixel 8 is emulation, not an Android device. WebKit browser-process loss, genuine BFCache, Linux/Windows lanes, actual devices, stable authored identity, dispatch timing and replay HAR durability remain open.

<a id="roadmap-browser-process-loss-2026-10-20--validation-and-review"></a>
### Validation and review

The final affected browser package passed 749 tests/subtests with three declared skips (opt-in benchmarks and Linux `/proc`). The native document-failure matrix passed five repetitions: 30 leaf cases including five isolated whole-browser crashes, each affecting desktop and Pixel 8 contexts. Targeted API checks passed 43 tests/subtests, including retained document/archive contracts, generated API documentation and the supplied HAR import. The QA documentation package passed 47 tests/subtests; all 296 active Markdown files passed repository checks.

All 31 Help unit tests passed. Real browser Help workflows passed 13 dev-admin tests with one declared skip and 14 local-auth tests. Four embedded Help articles exactly match their Markdown sources. Existing dashboard assets and production code are unchanged; the full repository Go suite and full dashboard cycle were not repeated for this test/documentation slice and remain CI merge gates.

The private HAR comparison (`evidence/private-har-comparison.json`) matches every stable field from the preceding slice: 803 requests, 476 actions and zero DSL issues. The historically randomized generated-script hash is the sole excluded field. No private capture, imported script or target hostname is retained. The disclosure check (`evidence/private-har-disclosure-check.json`) records the capture-hostname scan of retained logs and aggregate summaries; it is not a general payload scanner.

The first fixture run incorrectly expected the crash-command worker to exit at browser disconnection. The driver leaves that command pending; cleanup now stops the isolated driver and verifies the worker joins. Self-review also strengthened replacement-source coverage and liveness, bounded subprocess output draining, and required an explicit child-test pass so an empty child run cannot appear successful. The final review round (`evidence/review-rounds.json`) found no remaining issues. A scratch Help comparison initially used the wrong directory for three guide files; correcting those paths produced the four exact matches above.

The validation summary (`evidence/validation-summary.json`), commands (`evidence/commands.json`), source hashes (`evidence/tested-hashes.json`) and evidence checksums (`SHA256SUMS`) identify the tested scope. Main at `0eadba7de` was already integrated and remained current before both controller builds. The parallel primary checkout had no overlapping browser changes; its unrelated launch configuration and package lock were left untouched. Cache and owned-process cleanup are recorded in cleanup (`evidence/cleanup.json`). Shared build/Docker caches remain available to parallel work.

<!-- Original record: docs/qa/reviews/roadmap-browser-promotion-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-browser-promotion-2026-10-20"></a>

## Atomic browser evidence promotion — 2026-10-20


A browser-intelligence pass now pins accepted BE-4 evidence for the same binary, commit, engine, profile, platform, runtime/capability digests, metric and measurement algorithm. Dependency verification and insertion share a writer-fenced database transaction. Current support is withdrawn if its dependency is missing, superseded, pending, failed or revoked; historical records remain unchanged. The record digest binds artifacts, actor, sequence and cohort. Separate metric/analysis cohorts remain separate acceptance subjects, and broader withdrawals cover narrower cohorts.

The API requires a backend that advertises the atomic promotion contract. CLI qualification files are bounded JSON objects with duplicate keys preserved for strict server validation. Exact cohort status queries validate only matching subjects. The optional schema columns preserve legacy acceptance history; legacy records cannot qualify new BI promotions. Schema release 72 records the change.

No production acceptance records were written. Tests use temporary SQLite databases and an owned disposable loopback PostgreSQL 16 fixture; this does not certify an external browser cohort, deployed database topology or actual device.

<a id="roadmap-browser-promotion-2026-10-20--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,717 passing tests/subtests**, 38 explicit skips; native browser tests required. The supplied private HAR import retains every stable compatibility result.
- Targeted SQLite/PostgreSQL/API/CLI race coverage: **144 passing tests/subtests** over three repetitions, including a concurrent BE withdrawal while promotion waits for the database writer fence.
- Dashboard typecheck, lint, 31 Help unit tests, generated API/TypeScript contracts and build passed. Help workflows passed in both profiles: dev-admin 15 pass/one auth-specific skip; local-auth 16 pass. The narrow Help screenshot was reviewed, and the embedded article matches its source exactly. Documentation checks passed for 302 active Markdown files.
- Review fixed atomic-backend capability, cohort scope, digest precision and status-query findings; the final round found no issues. Local records: `evidence/review-rounds.json`, `evidence/validation-summary.json`, tested source/asset hashes and `evidence/SHA256SUMS`.

Raw evidence remains beside this Markdown review locally; only this record is committed. No private HAR source bytes, production acceptance records or external qualification claims are included.

<!-- Original record: docs/qa/reviews/roadmap-browser-proof-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-browser-proof-2026-10-22"></a>

## Browser durable receipt and qualification work


This work implements the remaining receipt transport, conservative full-window mapping and independent source-closure contracts in [BI-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#bi-a--qualified-baseline-selection-and-findings). The supported local Chromium main-frame path now connects transport, expected-source inventory, native closure and whole-window load attribution. Unsupported discovery or clock domains remain explicitly unavailable. The per-slice notes below preserve their completion-time scope.

<a id="roadmap-browser-proof-2026-10-22--shared-receipt-contract"></a>
### Shared receipt contract

The native acquisition DTOs now have a passive owner in `pkg/protocol`; aliases preserve the existing local browser inspection API. The bounded v1 decoder rejects unknown versions, missing/duplicate fields, unknown fields, non-finite browser offsets, foreign identities, inconsistent host elapsed/wall-drift values and conflicting document/lifecycle identity tuples. Empty and partial snapshots remain readable and never imply completeness. The raw `browser_documents_v2` envelope and measurement values are unchanged.

Independent review found that individually valid receipts could disagree on a lifecycle's document, frame or browser time origin. The fix enforces one immutable tuple per lifecycle and per document, including restored lifecycles. Regression tests cover direct cloning and wire decoding while preserving partial snapshots.

Targeted contract/native round-trip tests passed. Both full affected package suites passed (`pkg/protocol`: 0.713 seconds; native-required `pkg/protocol/browser`: 98.923 seconds). Independent re-review found no remaining findings. Evidence is retained outside the repository at `/private/tmp/apex-browser-durable-backend/browser-receipt-contract-*`.

<a id="roadmap-browser-proof-2026-10-22--qualification-boundaries"></a>
### Qualification boundaries

- Receipt transport and integrity/recovery apply to negotiated local and generator evidence; legacy peers retain their original wire contracts.
- Positive baseline load attribution currently requires a local controller-owned monotonic domain, a complete single-page Chromium main-frame census, and a static closed workload. The whole selected population must fit one proven steady span. Dynamic/rate-shaped workloads, unknown transitions, missing sources, ambiguous discovery, and unsupported WebKit discovery remain unqualified.
- Distributed journals retain their source evidence and independent pre-dispatch expected-owner inventory. A generator clock is not substituted for the controller domain: distributed clock qualification remains unavailable without an independently acquired controller causal envelope.
- Monotonic bounds projected onto an owner UTC label are baseline coordinates, not precise event timestamps or alignment with other telemetry. Generic timeline/onset scoring requires separate positive timeline qualification and remains unavailable for this evidence. Raw browser document windows are preserved unchanged.
- Reference-host, native Linux/Windows, and multi-host release qualification remain dependent on the unavailable external environments previously identified by the user.

<a id="roadmap-browser-proof-2026-10-22--durable-receipt-transport"></a>
### Durable receipt transport

Receipt snapshots now have a separate bounded JSONL sidecar. Local and generator archives retain its count and SHA-256, include it in artifact transfer/integrity and RCA input identity, and recover it through the durable source collection cache. Cache filenames follow the manifest, preserving old three-file and document-only four-file caches alongside receipt-only and combined layouts. Receipts do not create action or document measurement samples.

Remote transport requires a fresh advertised capability, traffic-free staging, runtime provider/wiring validation and an exact version acknowledgement. Legacy assignments explicitly suppress the new sidecar. Group, dynamic, parallel and reinitialization configuration carry that choice. Receipt publication is serialized and deduplicated. A failed read, missing sink, failed write or wiring panic leaves sticky receipt-loss state, including a separate manifest flag when an archive sink exists; it never changes the raw v2 measurement population.

Independent review identified missing durable loss disclosure and missing staged-runtime validation. A further review rejected a panic fallback that treated unavailable wiring as version 1. All were fixed, with tests for no publication on unsupported/panicking wiring, retry/deduplication, retirement, local/generator finalization, cache restart with and without receipt payloads, corruption/missing remote artifacts, and RCA digest invalidation. Both independent reviewers then reported zero remaining findings. This slice transports acquisition evidence only; it does not qualify clocks or source closure.

Transport validation passed: targeted engine, generator RPC, API/archive and collection-cache checks (0.884 / 0.754 / 17.191 seconds), and engine receipt race checks (2.276 seconds). Logs: `/private/tmp/apex-browser-durable-backend/browser-receipt-transport-final2.log` and `/private/tmp/apex-browser-durable-backend/browser-receipt-transport-race.log`. Cross-package full-suite validation remains scheduled after the remaining proof slices.

<a id="roadmap-browser-proof-2026-10-22--causal-lifetime-and-mapping-contract"></a>
### Causal lifetime and mapping contract

Native receipt v2 adds an optional context lifetime: its source monotonic origin is established before `NewContext`, and its upper bound freezes only after owned native close and worker joins succeed. Concurrent close attempts share context-aware ownership; a cancelled waiter cannot publish closure. Version 2 is negotiated independently, and the explicit v1 projection omits lifetime fields so strict older peers remain readable. Artifact inventory describes the versioned receipt container accurately.

The pure mapper binds an exact final metric identity/revision, capture receipt, navigation or restored-lifecycle start, and raw window end. It returns the entire authority dispatch-before / terminal-acknowledgement-after envelope. No one-way delivery latency, sparse wall-clock sample or interpolation narrows that envelope. A qualifying steady span must contain the whole envelope in the exact same run/incarnation/monotonic domain; adjacent spans cannot be joined across pauses or unknown periods. This contract still needs the independent source ledger and trusted dispatcher acquisition/consumer wiring described above.

Independent reviews fixed strict-v1 compatibility, a missing raw-window-start check, premature closure under concurrent native closes, cancellation while waiting for close ownership, and inaccurate artifact metadata. Both reviewers then returned zero findings. Native Chromium and WebKit tests verify bound acquisition, retention and repeated-close immutability; deterministic fixtures cover pauses, foreign/restarted domains, delayed receipts, missing terminals and altered raw windows. Targeted protocol/browser/engine/generator/API checks passed (0.125 / 11.217 / 0.446 / 0.537 / 2.916 seconds), including HAR ownership regressions. Log: `/private/tmp/apex-browser-durable-backend/browser-causal-final2.log`.

<a id="roadmap-browser-proof-2026-10-22--independent-source-journal-and-native-closure"></a>
### Independent source journal and native closure

Receipt v3 adds a durable execution-owner journal: slot expectation precedes adapter `Init`, context expectation precedes native context creation, context terminal follows physical close, and slot terminal follows adapter teardown. Each write syncs the sidecar; its initial publication also syncs the run directory and parent. Failed writes and publisher panics prevent admission or invalidate closure. Older negotiated versions receive explicit projections without the new fields.

Chromium closure reconciles an independent native loader census with immutable document identities. This first supported census is limited to a single main-frame page: child frames, additional pages, ambiguous loaders, discovery overflow, lost acquisition, and WebKit remain unavailable. A joined native close can account for a departing document whose JavaScript end receipt was lost; it does not manufacture a metric. Raw document evidence remains unchanged.

The reader requires an independently supplied expected-owner inventory, contiguous journal sequences, successful terminal obligations, matching document/lifecycle identities, consistent same-process monotonic lifetime bounds, and reconciliation of all extra snapshots. Unknown generators or unbound snapshots cannot disappear from the population. The mapper binds the exact final measurement revision and raw window to conservative native creation/receipt-delivery bounds. It never estimates a clock offset from a URL, browser wall time, or one-way delivery.

Independent reviews found and fixed silent child-frame/page-overflow gaps, directory publication durability, impossible lifetime bounds, document identity conflicts, forgotten unbound snapshots, and noncanonical control records. Added tests cover these refusals, v1/v2/v3 projection, publisher failure/panic, engine admission ordering, and native Chromium mapping with silent-child/WebKit refusals. Targeted protocol/browser/engine/generator/API checks passed; protocol and engine journal race checks passed. Logs are under `/private/tmp/apex-browser-durable-backend/browser-source-*`. Engine phase hooks and archive/RCA qualification remain the next integration slice.

<a id="roadmap-browser-proof-2026-10-22--engine-phases-and-archive-consumption"></a>
### Engine phases and archive consumption

Static closed-workload phases are published from actual engine control boundaries. Initial ramp, pause, resumed spans, duration retirement, accepted load controls and unexpected departure are distinguished. A cancellation observed after it has already reached workers invalidates proof; it cannot be backdated into a clean boundary. Rejected controls do not alter proof. A native source census retains only bounded identity state after page closure, not the page's network request maps.

Local archives freeze the controller owner; distributed collection copies its independently persisted pre-dispatch owner inventory. Archive readers check all raw document/lifecycle identities against reconciled native contexts. Exact final revisions map to conservative same-process bounds. Every contributing window must fit a positively recorded load span, and a bucket or selected population cannot combine different spans across a pause. Browser target wall-clock shifts do not move the projected population. Archive adapter version 10 invalidates results produced under earlier qualification rules.

Two real Chromium runs, each containing four measured navigations, passed durable archive reconstruction and scored baseline comparisons. Constant-value metrics retain the existing zero-variance refusal. The fixtures also verify missing-source refusal and that baseline load proof cannot enable generic timeline scoring. Synthetic regressions cover target clock shifts, same-bucket and multi-bucket pauses, rejected controls, cancellation ordering, and mixed proof modes. The full protocol, RCA and engine package tests passed (0.902 / 0.189 / 81.987 seconds); native and archive logs are retained under `/private/tmp/apex-browser-durable-backend/browser-proof-*`. Integrated validation and cross-cutting review are recorded below.


The actual multi-group engine/native-browser fixture also passed normal duration retirement and unexpected early-exit refusal. Independent cross-cutting review found a missing original-source descriptor in single-group closed execution and a non-cancellable saved-panel identity lookup. Both were corrected with dispatch-provenance and occupied-SQL-pool/cancellation regressions. The source reconciler caches completed validation until new evidence arrives, keeping repeated context lookups bounded; archive projection loops honor cancellation. Review rounds and integrated validation are recorded below.

<a id="roadmap-browser-proof-2026-10-22--cross-cutting-review-and-integrated-validation"></a>
### Cross-cutting review and integrated validation

Three independent review scopes covered the complete branch: runtime/protocol/engine and the Playwright fork; archive qualification, selection, policies and frozen panels; and replay durability, recovery, authorization and cleanup. Fix/re-review rounds ended with zero findings. The closed-engine provenance regression also passes repeated execution. Saved-panel tests cover cancellation while waiting for the real SQL pool and reject foreign projects or run incarnations during archive materialization.

Integration with main at `7cbe89489` preserves its published v72 authoring-receipt migration identity, moves the unmerged browser migrations to v73–77, and adds the combined v78 schema fingerprint. A real ledger upgrade regression verifies the published v72 path. Separate independent reviews of the merge resolutions and regenerated API contracts, store overlays and dashboard assets found zero issues.

Integrated checks completed:

- Dashboard typecheck, lint, all 733 unit-test files (8,174 tests), production build and bundle budgets passed under Node 22.23.3.
- Nine affected Playwright suites passed in both auth profiles: dev-admin 43 passed / 2 profile-specific skips; local-auth 45 passed. Coverage includes authoring intent, recording review, browser HAR, intelligence, native panels and desktop/mobile waterfalls, run-policy selection, command center and in-app help. The narrow help layout was visually inspected.
- Protocol, engine and generator source/receipt race checks passed, as did the nested Playwright fork's timing/navigation race checks.
- Native-required API tests passed for two real Chromium archive comparisons and actual engine phase boundaries. The attached private HAR still imports successfully: 803 requests, 476 actions, 479 assertions, 37 extractors, 43 correlations, 327 filtered assets, four warnings and zero DSL validation issues. The source checksum and import compatibility counts are unchanged. The generated-script digest is excluded from this comparison, which does not assert byte-for-byte or semantic script equality. No private HAR content is committed.
- Documentation validation passed for 341 active Markdown files, and source whitespace checks passed. The generated editor chunk retains intentional whitespace inside its default JSON template string; the full staged whitespace check reports that generated-output exception.

The first complete Go run found a timing-sensitive archive regression assertion: a valid audit response took 1.072 seconds against a one-second test deadline. Three immediate repetitions passed. Review confirmed that the handler releases its lease before sibling archive reads. The regression now allows five seconds, keeps its inner materialization timeout longer at ten seconds, and requires all three sibling archives in JSON reports. HTML reports retain deadline and released-budget checks. Independent review of the corrected test returned zero findings, and three repetitions passed (6.575 seconds). The full browser package also exposed two pre-existing test fixtures that still expected the old native subscriptions or omitted newly used page-frame methods. Updating those fixtures preserved exact subscription order, zero network buffers and close/crash-before-session checks. Independent review found zero issues; the complete native-required browser package then passed (97.829 seconds). The complete Go run finished with 166 passing packages and only the API/browser failures described above. The corrected full API package passed in 876.213 seconds; together with the complete native-required browser rerun, all 168 tested packages have passing results. A subsequent repository-wide verification reused earlier results but started another full API execution, so that redundant run was stopped rather than counted as a passing full-suite invocation. No additional production changes followed the clean cross-cutting reviews. Local logs and browser artifacts are retained under `/private/tmp/apex-browser-durable-backend/`, including `browser-final-full-go.log`, `browser-final-api-reviewed.log`, `browser-final-native-package-reviewed.log`, `browser-final-archive-deadline-fixed2.log`, `browser-final-native-har-reviewed.log`, `browser-final-race.log`, `browser-final-fork-race.log`, `browser-e2e-{dev-admin,local-auth}.log`, and `final-e2e/`.

The retained Salesforce recording contains the requested home navigation and link-click sequence. The fresh replay was attempted, but Salesforce returned HTTP 403 on the first navigation; no successful external link-click coverage is claimed. The local browser workflows and real native qualification tests above passed independently of that external restriction.

Final publication verification found that the ignored asset directory omitted 42 newly generated JavaScript chunks from the integration commit. Those exact locally tested build outputs are now explicitly tracked. The entire built static file set is checked against staged Git objects, including the entry script and lazy-loaded help/workflow chunks; no rebuild or source change was needed.

<!-- Original record: docs/qa/reviews/roadmap-browser-provenance-cli-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-provenance-cli-2026-10-27"></a>

## QA Review Record — `BI-C4`: Provenance in Reports, RCA Selection UI & CLI (`apexctl schedule`, `ci run`, `run --api`)


**Date:** 2026-10-27
**Package:** `BI-C4` (`browser_findings.v2` & `browser_selection.v1` Provenance in Reports/RCA UI + CLI Analysis Policy Selection)
**Status:** Verified (Zero Open Findings)

<a id="roadmap-browser-provenance-cli-2026-10-27--scope"></a>
### Scope

1. **Report Exports (`HTML`, `PDF`, `DOCX`, `CSV`) for `browser_findings.v2` & `journey_risk`:**
   - Updated [report_browser_intelligence.go](../../../pkg/api/report_browser_intelligence.go) and [report_section_templates.go](../../../pkg/api/report_section_templates.go) to surface `Policy.SHA256` (`policy_sha256` CSV row + summary annotation), `PolicyPins` notes, per-finding `Baseline` summary and structured CSV rows (`baseline`, `baseline_status`, `baseline_run_id`, `baseline_archive_sha256`, `selection_receipt_sha256`, `analysis_id`, `rejected_candidates`), and `SampleDocumentID` + `DocumentLink` (`#/report/<run>?section=browser&document_id=<doc>`).
   - Fixed [report_docx_html.go](../../../pkg/api/report_docx_html.go) so SPA hash routes (`#/report/...`) with `&` query parameters do not render as unescaped Word XML `w:anchor` attributes.
2. **RCA `browser_selection.v1` Surface in Reports & Dashboard UI:**
   - Added `BrowserSelection *ReportBrowserSelection` (`json:"browser_selection,omitempty"`) to `ReportRootCause` in [report_root_cause.go](../../../pkg/api/report_root_cause.go) and rendered it across HTML, DOCX, PDF, and CSV (`browser_selection` rows) in [report_sections.go](../../../pkg/api/report_sections.go) and [report_section_templates.go](../../../pkg/api/report_section_templates.go).
   - Updated [rca.ts](../../../dashboard/src/types/rca.ts), [rcaModel.ts](../../../dashboard/src/components/rca/rcaModel.ts), [RcaBrowserBaseline.tsx](../../../dashboard/src/components/rca/RcaBrowserBaseline.tsx), [RootCausePanel.tsx](../../../dashboard/src/components/rca/RootCausePanel.tsx), [BrowserIntelligenceView.tsx](../../../dashboard/src/components/runReport/BrowserIntelligenceView.tsx), and [BrowserFindingsPanel.tsx](../../../dashboard/src/components/runReport/BrowserFindingsPanel.tsx) to surface `policy.sha256`, `baseline.rejected_candidates`, and `browser_selection` (including `inputs.browser_selection` receipt digest and rejected candidate reasons/digests even when `browser_baseline` is absent).
3. **CI Runs & CLI (`apexctl run`, `apexctl ci run`, `apexctl schedule`):**
   - Added `AnalysisPolicySelection` / `AnalysisPolicySelections` to [run.go](../../../pkg/runcontract/run.go) and `POST /api/v1/ci/runs` in [ci_runs.go](../../../pkg/api/ci_runs.go) (gated by `policy.AllowTelemetryPolicySelection`, bound into `Idempotency-Key` metadata, and echoed in `ciRunResponse`).
   - Added `--browser-policy` and `--journey-policy` (`<id>@<revision>` or `project_default`) to `apexctl run` ([main.go](../../../cmd/apexctl/main.go), [api_client.go](../../../cmd/apexctl/api_client.go)) and `apexctl ci run` ([ci.go](../../../cmd/apexctl/ci.go), [ci_client.go](../../../cmd/apexctl/ci_client.go)), failing closed (deleting/aborting the unconfirmed run) if an older controller does not echo the requested selection.
   - Added `apexctl schedule` (`list`, `preview`, `create`, `update`, `delete`, `occurrences`, `resolve`) in [schedule.go](../../../cmd/apexctl/schedule.go) supporting `--browser-policy` and `--journey-policy` with controller echo confirmation.

<a id="roadmap-browser-provenance-cli-2026-10-27--verification-evidence"></a>
### Verification Evidence

- **Unit & Integration Tests:**
  - `go test -v ./pkg/api -run 'TestBrowserIntelligenceReportSectionsCarryV2ProvenanceAndDeepLinks|TestRootCauseSectionRendersBrowserSelectionWithoutBaselineInEveryFormat|TestCIRunCarriesTheChosenAnalysisPolicySelections'`
  - `go test -v ./cmd/apexctl -run 'TestParseAnalysisPolicyFlag|TestParseRunArgsBrowserAndJourneyPoliciesRequireAPI|TestParseCIArgsBrowserAndJourneyPoliciesRequireControllerMode|TestAPIClientCreateTestEchoesAndCleansUpUnconfirmedAnalysisPolicySelections|TestExecuteScheduleLifecycleAndEchoVerification|TestExecuteControllerCIEchoesAndAbortsUnconfirmedAnalysisPolicySelections'`
  - `cd dashboard && npx vitest run src/components/runReport/BrowserFindingsPanel.test.tsx src/components/rca/RootCausePanel.test.tsx`
- **Mutation Verification (`go test -overlay`):**
  - Mutant 1 (`pkg/api/report_docx_html.go` dropping `!strings.HasPrefix(href, "#/")` on `w:anchor`): killed by `TestBrowserIntelligenceReportSectionsCarryV2ProvenanceAndDeepLinks` (`word/document.xml is not valid XML`).
  - Mutant 2 (`cmd/apexctl/schedule.go` bypassing schedule `analysis_policy_selections` echo check): killed by `TestExecuteScheduleLifecycleAndEchoVerification`.
  - Mutant 3 (`pkg/api/ci_runs.go` bypassing `!policy.AllowTelemetryPolicySelection` check): killed by `TestCIRunCarriesTheChosenAnalysisPolicySelections`.

<!-- Original record: docs/qa/reviews/roadmap-browser-qualification-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-qualification-2026-10-21"></a>

## Browser baseline inventory and qualification — 2026-10-21


<a id="roadmap-browser-qualification-2026-10-21--candidate-inventory-foundation"></a>
### Candidate inventory foundation

The optional storage interface returns bounded canonical run identities for one
project/application/scenario, excluding the current run and non-complete runs.
An indexed ID cursor avoids timestamp-text ordering: legacy SQLite rows may
retain offsets, so completion ranking must compare decoded instants in Go.
At most 128 identities are returned with an explicit overflow flag. A latest
selector must refuse a partial inventory, enforce the current-run start fence,
and validate archived browser compatibility before selecting anything.

Schema release 75 adds the matching inventory index without changing earlier
migration fingerprints. Independent review found no issues after replacing the
initial timestamp-order query. The full storage suite passed in 90.416 seconds;
the earlier run caught a missing schema-release entry, which was fixed before
this passing run. Tests cover scope, self exclusion, status, pagination,
overflow, cancellation and invalid limits. The same exercise subsequently passed
on PostgreSQL 16 in an isolated schema (2.100 seconds), including schema migration
and the expression index; the test schema was removed by its cleanup hook.
The log is `/private/tmp/apex-browser-durable-backend/baseline-candidates-storage.log`.

This is an inventory foundation, not completed latest-compatible admission.
Automatic selection and positive browser clock/source proof acquisition remain
open. Existing source archives and unrelated work are preserved.

<a id="roadmap-browser-qualification-2026-10-21--clock-and-source-coverage-safety-gate"></a>
### Clock and source-coverage safety gate

Browser document RCA projections now carry separate clock and source-coverage
qualification states. Existing archives have neither browser-to-host calibration
covering each measurement window nor an expected-source/terminal capture ledger;
the adapter therefore reports both states as unavailable. Generator-to-controller
clock observations, local execution, successful collection and no reported drops
cannot establish those proofs.

Both browser baseline comparison and generic in-run browser load scoring refuse
unqualified inputs. Baseline results retain current/baseline bucket and sample
counts without a z-score, expected range or anomaly verdict; section limitations
identify missing clock and source-coverage proof separately. Generic detection
excludes these browser series from load scoring with the same limitations.
Non-browser detection and native/raw browser observations remain unchanged.

Engine v7 prevents reuse of older qualification decisions for new requests while
retaining completed historical assessments. The estimator is still
`browser_baseline.v1`. No new sidecars are consumed by this gate, and no clock
samples are promoted into browser evidence. Future positive qualification must
include every newly consumed receipt in frozen evidence digests and freshness
checks, bind source generations to their actual host/controller clock domains,
and establish complete expected-source populations. It must not extrapolate
clock bounds across unmeasured windows without a justified contract.

Two independent reviewers found no remaining issues after fixing historical
engine-version coverage and using valid generator-clock/collection metadata in
the archive regression. The targeted RCA/API checks passed (API 4.994 seconds),
and the RCA race check passed in 1.577 seconds. Regression coverage includes
unknown qualification on both baseline sides, generic in-run scoring, valid
generator-only calibration, collected sources, local absence, preserved raw
points/counts, non-browser behavior and historical-assessment readability.
Documentation checks passed for 333 active Markdown files.

The full API suite passed in 957.971 seconds. The attached private HAR retained
its original archive digest and imported 803 requests into 476 actions, with
479 assertions, 37 extractors, 43 correlations and zero DSL validation issues.
It was not replayed or copied into the repository. The full Go run passed: 168 packages passed, 10 skipped, zero failures. Latest main was
fetched and already integrated before validation. The previous HTTP/3 fixture
timeout did not recur; that package passed in 2.207 seconds. Logs and the full
summary remain under `/private/tmp/apex-browser-durable-backend/` with the
`browser-qualification-` prefix.
Positive native proof acquisition, frozen latest-compatible selection/rejection
receipts and the controlled real-browser acceptance gate remain unfinished.


The retained Salesforce home-and-three-link recording was attempted once through
the real replay manager, with its 11 converted actions and stop-on-error behavior.
Salesforce refused the initial navigation with HTTP 403 (`Access Denied`). The
manager closed its owned browser session; no bypass or load run was attempted.
This external workflow is not qualified. Its local report and summary are
`qualification-salesforce-report.json` and `qualification-salesforce-summary.json`
under `/private/tmp/apex-browser-durable-backend/`; the raw recording stays local.

Private Go build/test caches and abandoned session build directories were cleaned
after validation, reclaiming approximately 6.3 GiB. Logs, private source fixtures,
dependencies and deliverables remain. Shared Go/Docker caches were left intact
for parallel work; no containers, images or volumes were removed. No frontend
build cache was created in this slice.

<!-- Original record: docs/qa/reviews/roadmap-browser-receipt-clocks-2026-10-22/README.md; local evidence kept outside git -->
<a id="roadmap-browser-receipt-clocks-2026-10-22"></a>

## Native document receipt-clock acquisition


This slice adds bounded, process-local diagnostic acquisition to the browser producer. It does **not** qualify a browser baseline or complete the clock/source proof work in [BI-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#bi-a--qualified-baseline-selection-and-findings).

<a id="roadmap-browser-receipt-clocks-2026-10-22--contract-and-implementation"></a>
### Contract and implementation

`BrowserDocumentReceiptEvidence()` takes an owned snapshot without accessing the browser. Each accepted lifecycle begin/end and immutable terminal measurement has a receipt binding its native document, lifecycle and frame IDs; metric receipts also bind the exact measurement ID and revision. Receipts retain the browser-supplied `timeOrigin`/offset separately from native host receipt time and elapsed monotonic nanoseconds from the source generation's origin. The existing v2 raw document timestamps and serialization contract remain unchanged.

A generation has at most 357 receipts: 51 admitted lifecycles, each with one begin, five metric terminals and one end. Overflow and forced lifecycle censorship retain incomplete status. Duplicate finals cannot revise receipts; source reset creates a new monotonic origin and empties the ledger. Reads own their returned slice. No target probes, extra native calls, timers or workers are introduced.

A sampled absolute difference greater than 100 ms between host wall-clock progression and monotonic elapsed time latches `wall_step_observed`. A false flag proves neither continuity between samples nor a maximum drift. Browser timestamps are target-supplied, and one-way delivery gives no maximum transport delay or synchronized mapping. Initial documents can disappear during navigation before delivering their terminal callback; native tests preserve that missingness instead of assuming completion.

<a id="roadmap-browser-receipt-clocks-2026-10-22--scope-still-open"></a>
### Scope still open

These snapshots are local diagnostic acquisition only. They are not yet retained in run archives, negotiated between mixed-version peers, exported by the API or consumed by RCA. The existing comparison gate remains unavailable. Remaining work includes versioned durable transport, independently established expected-source and terminal closure ledgers, authenticated browser/host/controller mapping with uncertainty over the full measurement window, evidence-digest binding, and real two-run qualification. Receipt counts, collection success and absence of observed clock steps cannot substitute for those proofs.

<a id="roadmap-browser-receipt-clocks-2026-10-22--validation-and-review"></a>
### Validation and review

- Full affected browser package: 841 passing test cases, three benchmark/platform skips, no failures (102.581 seconds). Native browser execution was required; Chromium 149.0.7827.55 and WebKit 26.5 ran locally on macOS.
- Targeted receipt tests passed, including immutable measurement joins, generation isolation, bounded storage, snapshot ownership, sampled forward/backward wall steps and native missing-terminal disclosure. The same receipt unit/native cases passed under the race detector (5.695 seconds).
- Private HAR API import passed (64.990 seconds): 803 source requests, 476 actions, 479 assertions, 37 extractors, 43 correlations, four warnings, 264 protected requirements and zero DSL validation issues. Source archive hash and every stable summary field match the preceding slice. The historical script hash retains fixture identity variability and is excluded from the summary comparison; no byte-for-byte generated-script equality is claimed. The private capture was neither replayed nor committed.
- The retained 11-action Salesforce home/link-click recording ran with document capture enabled through the product replay manager. Initial navigation returned HTTP 403 Access Denied, so link clicks did not execute. This is a blocked external replay, not positive qualification. The replay manager closed its owned session; native package tests shut down their browser pool.
- Documentation checks passed for 335 active Markdown files. No dashboard, in-app help registry, route or generated artifact changed. The full repository suite remains a CI merge gate; this pass ran the full affected package and the targeted API import regression.
- Two independent agents each completed a clean review and a clean re-review. The local review additionally tightened censorship disclosure after native testing exposed a missing initial-document end receipt.

Raw logs and summary evidence are outside the repository under `/private/tmp/apex-browser-durable-backend/browser-receipts-*`; only this Markdown review record is committed. The unrelated existing promotion evidence directory is preserved.

<!-- Original record: docs/qa/reviews/roadmap-browser-replacement-container-reclaim-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replacement-container-reclaim-2026-10-26"></a>

## QA Review Record — Replacement-Container Native HAR Reclamation (`BE-B2`)


**Date:** 2026-10-26
**Scope:** `BE-B2` — Version-3 native HAR working-generation manifests with inherited Unix `flock` writer-tree leases and replacement-container cross-PID-namespace reclamation on verified local filesystems.
**Primary Packages:** `pkg/protocol/browser`, `docs/guides/BROWSER_HAR_WORKING_OWNERSHIP.md`

---

<a id="roadmap-browser-replacement-container-reclaim-2026-10-26--1-summary-of-changes"></a>
### 1. Summary of Changes

1. **Inherited Unix Lease Across Playwright Driver `exec` (`pkg/protocol/browser/har_working_security_unix.go`, `pkg/protocol/browser/har_working_lease.go`, `pkg/protocol/browser/pool.go`):**
   - Added `setHARLeaseInheritable` (`f.SyscallConn()` + `SYS_FCNTL` `F_SETFD`) on Unix (`darwin` and `linux`) so `owner.lock` can be temporarily marked non-`FD_CLOEXEC` while spawning the Playwright Node driver (`startPlaywrightWithInheritedHARLeases`) and immediately restored to `FD_CLOEXEC` in the Go parent.
   - When an idle Playwright driver from an earlier closed session exists without having inherited a new pending local-filesystem HAR owner's lease (`pendingHAROwnerNeedsDriverInheritance`, `browserEvidenceBudget.liveCount() <= 1`, `!browserEvidenceBudget.hasOpenContext()`, and `activePoolOperations.Load() == 0`), `getBrowserInstanceLocked` recycles the idle pool so the fresh driver inherits the active `owner.lock` open file description, while `getBrowserInstanceForContext` holds an `activePoolOperations` token across `openAdmittedBrowserContext`.
   - Bumped `harOwnershipManifestVersion` to `3` (preserving the 6-entry generation directory whitelist `harWorkingGenerationEntries = 6`). `manifest.json` (`sealed`) and `writer-entered.json` (`writer_entered`) record `Version: 3` only on Unix when `harWorkingDirectoryIsLocal` (`localfs.IsLocal`) is `true` and (for `writer-entered.json`) the recorded writer PID inherited the `owner.lock` open file description. Otherwise `Version: 2` (or `Version: 1`) is written.

2. **Cross-PID-Namespace Replacement-Container Reclamation (`pkg/protocol/browser/har_process_identity.go`, `pkg/protocol/browser/har_working_lease.go`):**
   - Added `harProcessGoneWithLease` and `harProcessesGoneWithLease`:
     - Within the **same** PID namespace (`id.Namespace == host.namespace`), `procidentity.Gone` remains strictly mandatory even for version-3 manifests.
     - Across **different** PID namespaces (`id.Namespace != host.namespace`) on the **same kernel boot** (`id.Boot == host.boot`) and **same machine identity** (`id.Machine == host.machine`), reclamation is permitted only when `leaseCoversWriterTree` is `true` (Unix, `localfs.IsLocal == true`, `manifest.json` is version 3, any present `writer-entered.json` is version 3, and the caller holds or verified free the exclusive `flock` lease on `owner.lock`).
     - Version 1/2 manifests, non-local filesystems (NFS, SMB, FUSE), foreign machine/boot identities, and Windows containers remain preserved under the fail-safe rule.

---

<a id="roadmap-browser-replacement-container-reclaim-2026-10-26--2-verification-two-container-linux-qualification"></a>
### 2. Verification & Two-Container Linux Qualification

- **Host & Subprocess Qualification (`darwin/arm64` & `linux/arm64`):**
  - `APEXLOAD_BROWSER_TESTS=require go test -race -v ./pkg/protocol/browser -run "^(TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits|TestHAROwnershipReclaimsKilledNativeRecording|TestHAROwnershipPreservesForeignAndUnprovenGenerations)$" -count=1` -> **PASS** (`4.16s`).
  - Verified in both `TestHAROwnershipReclaimsKilledNativeRecording` (Chromium and WebKit) and `TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits` that:
    1. While the killed generator's orphaned writer process is still alive, a replacement container in a new PID namespace plans `0` generations because the kernel holds `flock(LOCK_EX)` on the inherited `owner.lock` open file description.
    2. Within the same PID namespace, `procidentity.Gone` remains mandatory and refuses live writers even when `leaseCoversWriterTree == true`.
    3. On a non-local filesystem (`harWorkingDirectoryIsLocal == false`) or foreign machine/boot, cross-namespace reclamation is refused even after the writer exits.
    4. Once the writer exits on a verified local filesystem, the replacement container in the new PID namespace plans and prunes the predecessor container's version-3 generation.
- **Two-Container Linux PID-Namespace Qualification on Shared `ext4` Volume (`apexload-generator:local`):**
  - Container 1 (`hostname=pod-gen-0`, `namespace="pid:[4026534490] time:[4026534751]"`, `boot="6adde715-2336-4efc-a2a8-71e6baeab40b"`) created generation `ae59fb60d13ec97207a6063a6ef7307c` with `version: 3` `manifest.json` (`pid: 1`) and `writer-entered.json` (`pid: 18`) on `ext4` volume `apexload-har-reclaim-ext4`, and was killed via `docker kill -s KILL`.
  - Container 2 (`hostname=pod-gen-0`, `namespace="pid:[4026534757] time:[4026534887]"`, `boot="6adde715-2336-4efc-a2a8-71e6baeab40b"`) ran `ReclaimRuntimeHARWorking` against the shared `ext4` volume and reclaimed the predecessor container's generation (`reclaimed=1 err=<nil>`).

---

<a id="roadmap-browser-replacement-container-reclaim-2026-10-26--3-mutation-verification-go-test--overlay"></a>
### 3. Mutation Verification (`go test -overlay`)

- `mut1_disable_v3_cross_ns` (`pkg/protocol/browser/har_process_identity.go`: forced `!leaseCoversWriterTree` to return `false`) -> **Caught** by `TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits` (`replacement container planned [], want 1 abandoned candidate`).
- `mut2_no_inherited_lease` (`pkg/protocol/browser/har_working_lease.go`: disabled `setLeaseInheritable(true)` in `startProcessWithInheritedLease`) -> **Caught** by `TestHAROwnershipReplacementContainerReclaimsOnlyAfterInheritedWriterExits` (replacement container prematurely planned the generation while the orphaned writer was still alive).

---

<a id="roadmap-browser-replacement-container-reclaim-2026-10-26--4-code-review-resolution"></a>
### 4. Code Review Resolution

- **Round 1 Findings Fixed:**
  1. `newHARCaptureOwner` (`pkg/protocol/browser/har_working_lease.go`) now assigns `o.lease` and `o.release` under `o.mu.Lock()`, sets `o.localFS` under `o.mu.Lock()` only after manifest setup completes, and guards `setLeaseInheritable` and `writerInheritedLease` with `o.mu.Lock()` (including requiring `!o.writerEntered` when enabling inheritance).
  2. `browserExecutableAvailable` (`pkg/protocol/browser/pool.go`) now spawns Playwright through `startPlaywrightWithInheritedHARLeases()` rather than `playwright.Run()`.


<!-- Original record: docs/qa/reviews/roadmap-browser-replay-backend-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-backend-2026-10-21"></a>

## Immutable replay artifact backend — 2026-10-21


This BE-B slice supplies a passive filesystem/object backend adapter for the existing replay artifact receipts. It does not enable durable replay publication or recovered HTTP downloads. Those next slices must reserve bytes and an exact owner/generation before staging, establish writer completion only after successful staging, publish through the storage CAS, authorize current project/receipt reads and take download admission before materializing bytes.

<a id="roadmap-browser-replay-backend-2026-10-21--contracts"></a>
### Contracts

- Each generation is a fresh 64-character lowercase hexadecimal identity. HAR and report have independent exact lengths and SHA-256 hashes, bounded at 256 MiB and 16 MiB respectively. No executable source is introduced by this adapter.
- Staging creates exclusive objects; no retry overwrites a prior attempt. Both objects must write, close and verify before success. Failed or ambiguous staging neither deletes objects nor supplies writer-join proof.
- Source fingerprints bind the current backend namespace. Object staging-root changes do not change remote authority; changing a filesystem root does. Reads never follow a persisted arbitrary URI.
- Reads verify both complete objects into private disk staging before returning readers. Missing, corrupt or mismatched evidence fails explicitly. There is no synthetic replacement, large HAR heap buffer or successful partial response. Closing the result removes its private staging directory; cleanup of backend bytes does not invalidate an admitted verified reader.
- Cleanup requires a joined cleanup tombstone and removes only its two generation-specific object names. Local cleanup removes an empty generation directory; foreign extra files remain and prevent reservation release. Failed writes retain their reservation until independent recovery establishes writer completion.
- `.apexload-replays` is reserved against run-ID and Windows-alias collisions. Ordinary run archive cleanup cannot recursively delete it. Replay receipt admission owns its byte budget independently of run-output backlog accounting.

<a id="roadmap-browser-replay-backend-2026-10-21--review"></a>
### Review

Implementation review found and fixed a collision with ordinary run archive namespaces. The dedicated reserved namespace, run-ID alias checks and deletion guards now preserve the replay owner. Additional review tightened exclusive local directory creation, exact-size output (the overflow probe is not written), empty-root refusal and handle-relative cleanup. Regression tests cover these boundaries.

Follow-up review of source authority, immutable writes, reader staging, cleanup ownership, namespace aliases, quota isolation and the regression tests found no further findings, including low severity. Final source hashes remained unchanged throughout qualification.

<a id="roadmap-browser-replay-backend-2026-10-21--validation"></a>
### Validation

Main `ceb3f9b83` was integrated without content changes as `da030fd81` before testing and remained current at completion. Actual Go 1.26.10 validation passed:

- Full `go test -timeout=20m ./...`, with the private-HAR import overlay: 35,966 passing test/subtest events and 46 conditional skips; no failed packages or tests.
- Adapter, namespace and archive deletion regressions: 42 passing events; independent controller backlog checks: 33 passing events.
- Three race-detector repetitions: 126 adapter/archive events and 99 controller backlog events, all passing.
- The supplied private HAR's stable import summary matches its retained baseline. Randomized script identity is excluded from comparison. Its requests were never replayed.
- Documentation checks pass for 318 active Markdown files; whitespace checks pass. All 311 previously tested embedded dashboard assets are unchanged. No new UI or native-browser qualification is claimed.

Raw logs: `full-go.jsonl`, `final-targeted.jsonl`, `backlog-targeted.jsonl`, `race.jsonl`, `backlog-race.jsonl`; source hashes: `tested-source-hashes.json`. Conditional skips retain their stated environment/platform gates.

<a id="roadmap-browser-replay-backend-2026-10-21--evidence-and-limits"></a>
### Evidence and limits

Local logs live under `/private/tmp/apex-browser-durable-backend/`. The object fixture uses Go Cloud's file-backed bucket implementation; it does not certify S3, Azure Blob or GCS behavior. The adapter is not yet wired into browser session completion or downloads, and it does not close crash/restart/HA, live cloud, real TruClient or reference-host acceptance. Existing native-versus-synthetic disclosure and the supplied private HAR import contract remain unchanged. See the [implementation specification](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#be-b--har-durability-and-real-importreplay-workflows).

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-http-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-http-2026-10-21"></a>

## Controller replay recovery activation — 2026-10-21


This BE-B slice enables the previously passive receipt-backed replay runtime at controller admission, authorized status/HAR/evidence reads, and lifecycle-owned cleanup. It does not enable durable storage for standalone recording bridges.

<a id="roadmap-browser-replay-http-2026-10-21--contracts"></a>
### Contracts

A native terminal replay publishes only through its admitted lease owner and immutable backend generation. Current project access is checked before recovery; status requires viewer access and evidence downloads require editor access. Global and development-bypass callers also resolve an exact project. A durable live cache entry cannot bypass backend verification. Status verifies the bounded report independently of HAR availability; missing native bytes return unavailable without a synthetic replacement. Local-only replay behavior remains available with explicit fidelity and durability headers.

Cleanup starts with the controller, runs only on the execution leader, and joins shutdown. It uses the bounded exact-generation runtime; no foreign volume, unknown file, unjoined writer or stale ownership permits reservation release. Recovery never fabricates a live replay or managed test. Generated OpenAPI retains existing response types and adds the unavailable response.

<a id="roadmap-browser-replay-http-2026-10-21--review-and-fixes"></a>
### Review and fixes

The cache-retention test previously expected local eviction to release the replay ID. It now verifies durable recovery and continued reservation, and uses a different ID to verify synthetic-only behavior for an uncaptured successor. Additional tests cover deleted projects, exact cleanup, namespace reuse, reader ownership, private-error nondisclosure and cleanup lifecycle. A later review found that approved cross-origin dashboards could not read the new durability header; the explicit CORS exposure list and its allowed/denied-origin regression now cover it, without wildcard header exposure.

Real browser verification exposed a pre-existing authoring check that required element selectors for every browser action. The check now follows catalog element-locator alternatives (with an offline fallback), accepts object-repository bindings and wait conditions, and does not invent locator requirements for WebSocket waits or page-level actions. A second review caught the separate generic-target check, including its offline fallback and nested actions. Browser actions now avoid that invalid unconditional target requirement; strengthened tests check all authoring errors, accept navigation selectors and object references, and preserve missing-locator errors for element actions at both levels.

<a id="roadmap-browser-replay-http-2026-10-21--validation-and-qualification-boundaries"></a>
### Validation and qualification boundaries

Targeted controller/recordbridge checks pass. The dashboard builds with its existing large-chunk warning; final full lint and 77 relevant authoring/import/mobile unit tests pass, including 58 authoring regressions. The affected HAR Help workflow passes in both dev-admin and local-auth profiles, including search, section links, reload and narrow layout.

The retained Salesforce recording covers the public home page and several clicked links. Its import required logical bindings for two original opaque DOM selector IDs; their exact original values were bound through the normal secret workflow in a disposable project. Manual editor checks in both auth profiles remove the false selector errors without changing the recorded actions; the valid duplicate-name warning remains. A sequential replay again received HTTP 403 on the first Salesforce navigation. Native capture was correctly disabled by the existing resolved-secret protection; its disclosed synthetic HAR cannot qualify native durability or successful external journey execution. No private HAR target was replayed.

Deterministic native-artifact tests verify byte-identical status/HAR/ZIP recovery through a second server and separate SQLite connection after manager shutdown, authorization, missing-native refusal and retained namespace ownership. They do not replace native crash, multi-host, cloud backend, real TruClient or reference-host qualification.

Raw local evidence is under `/private/tmp/apex-browser-durable-backend/`: `http-fixed-targeted.jsonl`, `http-authoring-unit-final.log`, `http-lint-final.log`, `http-dashboard-build-final.log`, `http-help-dev.log`, `http-help-local.log`, `http-focused-race.jsonl`, `http-cors-targeted.jsonl`, `http-final-full-go.jsonl`, `http-private-har-summary-final.json`, `http-final-backend-source-hashes.json`, `http-authoring-consumers.log`, `http-lint-reviewed-final.log`, `http-dashboard-reviewed-build.log`, `http-controller-reviewed-build.log`, `http-reviewed-help-local.log`, `http-reviewed-help-dev.log`, and the private `http-live/` fixture. Browser snapshots/screenshots are under `output/playwright/replay-recovery-20261021/`. Raw recordings, resolved bindings and binaries are not committed. The first race command also repeated expensive generated OpenAPI checks and was deliberately stopped; it is not a passing gate. The focused three-repetition race run passes with 113 test/package pass events and three opt-in native-browser skips. The initial full run was stopped to include the CORS review fix; it is not a passing gate. The final backend full Go gate passes: 36,249 test/package pass events, 56 skips, no failures; the API package completed in 921.256 seconds. The supplied private HAR ZIP import-only overlay matches all stable prior summary counts/digests (excluding only the timestamp-sensitive script hash), with zero DSL issues. Backend source hashes stayed unchanged throughout the run. The frontend-only second review fixes are validated separately by the final build and browser checks. The final rebuilt binary passes the HAR Help workflow and manual Salesforce-recording editor checks in both auth profiles; screenshots were visually reviewed. Final review found no remaining issues, including low severity.

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-omissions-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-omissions-2026-10-21"></a>

## Replay capture omission evidence — 2026-10-21


This BE-B slice retains bounded native capture omissions in final replay reports after protocol teardown. It supports omission-only providers, uses the existing closed reason/count validation, and turns malformed or panicking providers into the existing safe unavailable-status reason. Empty evidence remains absent and does not establish capture coverage. Replay success and progress-event ordering are unchanged.

Report clones own their count maps. Terminal recovery preserves the serialized counts exactly. Generated OpenAPI and TypeScript contracts and both hand-maintained replay interfaces include the optional field.

Requested native capture suppressed by resolved runtime secrets emits `browser_har_unavailable_har_runtime_secrets` once per context. Suppression opens no writer and creates no raw artifact. It is a policy decision, not a capture failure. Capture-off remains quiet. No secret values are included in the warning.

<a id="roadmap-browser-replay-omissions-2026-10-21--validation-and-review"></a>
### Validation and review

The focused regressions pass, including successful and failed actions, teardown ordering, omission-only providers, malformed reasons, provider panic, empty evidence, clone independence, retained recovery and secret-suppression behavior. The affected engine, recordbridge and browser race suites pass. Targeted API recovery/generated-contract checks, dashboard typecheck and lint, and documentation checks are recorded under `/private/tmp/apex-browser-durable-backend/` with the `omission-` prefix. The full Go/private-HAR import-only gate passes: 36,261 test/package pass events, 56 skips and no failures across 168 passed packages; the API package completed in 946.783 seconds. The supplied private HAR import matches every stable prior summary field, excluding only the timestamp-sensitive script hash.

The retained Salesforce home-page/link-click recording was replayed using its existing logical selector bindings. The first navigation returned HTTP 403; its final report retained the expected safe suppression warning and no capture-failure count. This does not qualify a successful external journey or native durability. The existing editor still displays all 11 actions and the valid duplicate-name warning. Browser screenshot: `output/playwright/replay-recovery-20261021/.playwright-cli/page-2026-10-21T20-48-46-932Z.png`. The owned verification browser was closed. Private HAR targets were not replayed.

Review found and fixed the missing hand-maintained client fields. A subsequent review checked teardown, progress-versus-terminal publication, current lease fences, clone ownership, recovery serialization and generated schema propagation, with no further findings. The final review and validation round found no remaining issues, including low-severity findings. External native/crash/multi-host/cloud and reference-host qualification remain open.

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-publication-port-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-publication-port-2026-10-21"></a>

## Terminal replay publication port — 2026-10-21


This BE-B slice adds the optional lease-bound publication boundary to the shared replay manager. It does not enable controller persistence: the controller publisher, recovered authorized reads and exact cleanup worker remain separate work. Existing local bridges keep their terminal-fence behavior.

<a id="roadmap-browser-replay-publication-port-2026-10-21--contracts"></a>
### Contracts

- Publication receives a detached, normalized terminal session/report and the joined native HAR reader. No synthetic HAR substitutes for missing native bytes. Readers remain owned by the worker until the synchronous publisher returns.
- Publication runs outside manager/session locks with a two-minute context deadline. The adapter must honor cancellation and stop using its input readers before returning. Ambiguous remote writer outcomes retain durable cleanup identity and quota; context expiry alone never establishes writer completion.
- Downloads return 409 with retry guidance while publication is pending. Completion callbacks run only after the publication decision. A failed publisher does not change the replay's successful action result.
- Optional capability results distinguish local-only, pending, durable and unavailable. This slice exposes these to adapters, not as a new HTTP/UI contract. A nil publisher remains local-only.
- A late publisher cannot settle an abandoned/replaced session. Execution epoch or retained-lease loss during publication suppresses the report and retires the local capability; the controller adapter must additionally enforce storage ownership at publication CAS.

<a id="roadmap-browser-replay-publication-port-2026-10-21--review"></a>
### Review

The first review found that cancellation reconciliation treated pending publication as an already-settled terminal fence. The fix suppresses pending report payloads after epoch loss and adds a regression. Further review preserved the original terminal-fence behavior for adapters without a publisher and stopped-before-start sessions, narrowing cancellation reconciliation to explicitly pending publication. Tests also exercise HTTP download ordering, detached snapshot mutation, native-only publication, storage failure, lease loss, abandonment and same-ID replacement.

A final review of lifecycle ordering, snapshot isolation, lock boundaries, native ownership, cancellation, nil capability compatibility and regression coverage found no remaining findings, including low severity.  Native Salesforce qualification and restart/HA qualification are not claimed by these deterministic port fixtures.

<a id="roadmap-browser-replay-publication-port-2026-10-21--validation"></a>
### Validation

Main `ceb3f9b83` was current before cross-package validation. Final reviewed source passes all bridge tests under three race-detector repetitions (381 passing test/subtest events) and the controller's replay handler, session, artifact, HAR and lifecycle regressions (94 passing events; one conditional skip). The supplied private HAR import separately passes and its stable summary matches the retained baseline, excluding randomized script identity. Its target requests were never replayed.

Documentation checks pass for 319 active Markdown files; whitespace checks pass. All 311 previously tested embedded dashboard assets remain unchanged. Raw local logs live under `/private/tmp/apex-browser-durable-backend/`: `publication-reviewed-race.jsonl`, `publication-reviewed-api.jsonl`, `publication-final-api.jsonl`, and `publication-private-har-summary.json`. `publication-reviewed-source-hashes.json` pins the final reviewed source. No dashboard assets or runtime browser qualification changed.

The broader Go suite was also started in this cycle, before the final local-only/stopped-before-start compatibility fixes. It reached 32,891 passing events and 42 conditional skips, then the API package hit its 20-minute timeout under concurrent compilation, without an assertion failure. The final bridge and controller checks above qualify the compatibility fixes. This is not a passing full-suite gate; serial final-source validation remains required before merge.

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-publisher-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-publisher-2026-10-21"></a>

## Lease-bound replay artifact publisher — 2026-10-21


This BE-B slice implements the controller-side publisher behind the optional replay publication port. It remains passive until controller admission, recovered HTTP authorization and exact cleanup wiring are completed. It does not enable restart downloads or certify live cloud storage.

<a id="roadmap-browser-replay-publisher-2026-10-21--contracts"></a>
### Contracts

The publisher binds session, project, lease owner and backend authority independently of the supplied terminal snapshot. It rejects mismatched session/report identity, missing terminal report/completion, nonterminal state, invalid HAR length and reports exceeding 16 MiB before writing artifacts. Native HAR input is streamed for hashing with a 256 MiB limit, rewound, then independently checked by immutable staging. Every attempt uses fresh cryptographic generation entropy.

Receipt admission precedes physical staging. Only successful verified staging supplies writer-completion proof. That proof is recorded with an independent bounded context even when caller cancellation prevents publication, permitting exact-generation cleanup later. Final publication still requires the original unexpired retained owner and project through the storage CAS.

Unknown reservation, staging, join or publication outcomes never trigger speculative deletion or namespace release. Failed staging/unknown join keeps the reservation counted and blocks a successor session ID. A verified writer whose lease is lost becomes cleanable without becoming downloadable. Publication errors preserve the execution result through the bridge port's separate durability result.

<a id="roadmap-browser-replay-publisher-2026-10-21--review"></a>
### Review

Review tightened the port documentation to distinguish joined input readers from ambiguous remote writers, whose durable ownership must survive an error return. Regression coverage was expanded for report identity/size boundaries and for join acknowledgement loss and cancellation after verified staging. Follow-up review of input authority, byte bounds, quotas, generation ownership, cancellation and cleanup found no remaining findings, including low severity.

Filesystem fixtures use real SQLite receipts and the real immutable backend; injected boundary failures wrap the actual store implementation. These tests do not substitute for Salesforce workflow, real TruClient, native platform or external HA qualification.

<a id="roadmap-browser-replay-publisher-2026-10-21--validation"></a>
### Validation

- Final targeted publisher/backend checks: 51 passing test/subtest events.
- Three race-detector repetitions of publisher/backend checks: 147 passing events. The two report identity/size cases added after that build began pass in the final normal run; production publisher code is unchanged between these runs.
- Documentation checks pass for 320 active Markdown files; whitespace checks pass.
- Logs and reviewed source hashes are retained locally under `/private/tmp/apex-browser-durable-backend/`: `publisher-final-targeted.jsonl`, `publisher-race.jsonl`, `publisher-reviewed-source-hashes.json`, and `publisher-docs.log`.

The earlier broad Go run timed out in the API package under concurrent compilation, with no assertion failures before timeout. It predates this publisher's integration and is not a full-suite pass for this slice. Serial final-source validation remains required before merge. The private HAR import baseline and unchanged embedded assets were checked in the preceding publication-port slice; this passive publisher adds no import or dashboard behavior.

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-read-contract-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-read-contract-2026-10-21"></a>

## Retained replay read contract — 2026-10-21


This BE-B slice separates bounded status-report verification from complete HAR/report download verification. It prepares restart recovery without enabling controller publication or changing HTTP routes.

<a id="roadmap-browser-replay-read-contract-2026-10-21--contracts"></a>
### Contracts

Status reads verify the published, joined receipt's source identity, report length and SHA-256 within the 16 MiB report bound. They do not read or claim current availability of the native HAR. Downloads continue to verify both complete objects before returning a reader. Missing native bytes remain unavailable.

Recovery accepts only one supported terminal session envelope with exact session/project/report identities and a nonzero completion timestamp. Unknown schema fields, trailing JSON and unscoped identities fail closed. Generic JSON numbers retain their precision. Detached reconstruction preserves the verified historical evidence without applying current field-truncation rules or importing local paths, leases or execution callbacks. It does not fabricate a managed test or register a running replay.

A durable-native snapshot cannot fall through the existing local-only synthetic HAR path. Ordinary local-only bridges retain their disclosed synthetic behavior. Controller recovered authorization, lifecycle cleanup and HTTP wiring remain separate work.

<a id="roadmap-browser-replay-read-contract-2026-10-21--review-and-fixes"></a>
### Review and fixes

Review found that publication accepted a zero completion timestamp although recovery correctly refused it. Publication now rejects that value before receipt admission. Review also identified that using the ordinary snapshot clone would reapply current evidence truncation to a historical report; recovery instead clones the portable JSON while preserving numeric precision. Regression tests cover both findings, native fallback refusal, source/length/hash/state/cancellation failures, historical evidence preservation, invalid identities and unsupported envelopes.

<a id="roadmap-browser-replay-read-contract-2026-10-21--validation"></a>
### Validation

Targeted checks pass with 66 test/package events. Three race-detector repetitions of the read, backend, publisher and replay HAR checks pass with 389 events. The existing opt-in `TestBrowserReplayActualNativeHAR` test is skipped in each repetition; no real-browser qualification is claimed by these checks. Final review found no remaining issues, including low severity, and the reviewed source hashes remained unchanged through validation.

The preceding fixed-source full Go gate at `7cc82b4f1` passed with 36,177 test/package events and 56 skips. It includes the private supplied HAR ZIP import check: its compatibility summary is unchanged apart from randomized script identity. No private target requests were replayed. That gate covers the prior committed stack; these subsequent read-contract changes are covered by the targeted/race checks above, with the combined final-source gate still required before merge.

Documentation checks pass for 323 active Markdown files, whitespace checks pass, and all 311 embedded dashboard asset hashes are unchanged. Raw local evidence under `/private/tmp/apex-browser-durable-backend/` includes `read-targeted.jsonl`, `read-reviewed-race.jsonl`, `read-reviewed-source-hashes.json`, `read-docs.log`, `serial-full-go.jsonl` and `serial-private-har-summary.json`. Filesystem and file-backed object fixtures do not qualify cloud backends, native platform crash recovery or shared multi-host storage.

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-receipts-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-receipts-2026-10-21"></a>

## Replay artifact receipt storage — 2026-10-21


This storage foundation reserves immutable, versioned terminal HAR/report identities before upload. The reservation requires the exact unexpired retained session/project/owner lease and a current project. It retains backend-namespace and artifact hashes, lengths and a fresh generation; it does not store arbitrary backend URLs or credentials.

Receipt count and byte admission include staging, ready and cleanup-pending objects. Unknown writers remain reserved after expiry. Only an explicit physical-writer join permits publication or cleanup. Publication rechecks terminal ownership against the database clock. A ready receipt protects the global session namespace after process pruning removes the lease; expiry alone does not release that namespace. Cleanup removes only the exact joined generation after physical deletion is confirmed.

Project deletion keeps cleanup metadata and revokes old replay leases. Recreating the project ID cannot authorize the old owner to reserve or publish. Reads and cleanup verify the serialized identity and reserved size against the stored row. Schema release 74 adds the receipt table and indexes; older application/schema combinations remain governed by the existing schema-version refusal contract.

<a id="roadmap-browser-replay-receipts-2026-10-21--validation-and-review"></a>
### Validation and review

Go 1.26.10 full suite passed: 35,925 passing tests/subtests and 38 explicit skips, with no failures. Targeted storage tests passed under the race detector across three repetitions (75 passing tests/subtests), and real PostgreSQL receipt tests passed across three repetitions (9 passes, no skips). Documentation validation passed for 314 active Markdown files.

The original private HAR import's stable compatibility fields match the prior baseline exactly. The tested source hashes, embedded dashboard asset hashes and base commit were verified unchanged after the run. This storage-only slice does not change in-app help or browser UI. Intermediate logs, schema fingerprint derivation and final evidence remain local under `/private/tmp/apex-browser-durable-replay/`. No raw QA artifacts are committed.

Review findings fixed: verify serialized receipt identity against its row before cleanup; explicitly version the payload; reject invalid UTF-8 rather than allow JSON serialization to change an identity. A second review of the final storage transitions and tests found no further issues.

<a id="roadmap-browser-replay-receipts-2026-10-21--remaining-integration"></a>
### Remaining integration

This slice does **not** make browser replay HARs durable yet. Controller publication, approved-backend staging, physical-writer proof, bounded cleanup, restart/HA reads and authenticated HAR/evidence downloads remain to be wired. All controller session kinds must consult the durable namespace, including non-HA execution. The standalone bridge retains its local behavior. Receipt methods do not themselves verify backend bytes or infer that a remote writer has stopped.

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-recovery-storage-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-recovery-storage-2026-10-21"></a>

## Replay recovery scope and cleanup pagination — 2026-10-21


This BE-B storage slice supplies internal authorization metadata and bounded cleanup traversal for recovered native replay artifacts. It adds no schema release and does not enable recovered HTTP downloads by itself.

<a id="roadmap-browser-replay-recovery-storage-2026-10-21--contracts"></a>
### Contracts

Scope lookup validates the retained receipt's session/project/generation and current project existence. It reports staging, ready or cleanup state, writer completion and expiry measured against database time. It grants no byte-read permission: callers must still enforce current project roles and obtain the exact project-scoped ready receipt. Deleted projects have no authorization scope; expired or unjoined receipts cannot become downloadable through this lookup. Invalid identity and corrupt receipts fail closed.

Cleanup can traverse a session-ID keyset in pages of at most 256 records, allowing a worker to advance past receipts from a different backend configuration. The cursor only schedules work; it never proves writer completion or releases quota. Callers reset it after reaching the end so failures remain retryable. Unjoined writers and unexpired ready artifacts are excluded. The existing expiry-ordered cleanup method retains its contract, and exact-generation physical cleanup plus finish acknowledgement remain required.

<a id="roadmap-browser-replay-recovery-storage-2026-10-21--review"></a>
### Review

Review of the planned cleanup integration identified starvation if a worker repeatedly skipped foreign-backend receipts on the first page. Keyset traversal addresses that without adopting or deleting foreign objects. Follow-up review of SQL bounds, cursor ordering, transaction ownership, scope versus read authority, project deletion and retry behavior found no remaining findings, including low severity.

The PostgreSQL fixture is disposable and bound to loopback; it validates database behavior, not external HA, cloud-backend or native-browser qualification. Controller authorization, verified recovered reads and cleanup-worker wiring remain open.

<a id="roadmap-browser-replay-recovery-storage-2026-10-21--validation"></a>
### Validation

SQLite replay receipt/recovery checks pass with 18 test/subtest events. All five PostgreSQL replay-artifact integration tests pass against a disposable local PostgreSQL 16.15 fixture. Three combined race-detector repetitions pass with 69 events and no skips or failures.

Documentation checks pass for 321 active Markdown files; whitespace checks pass. Local logs and reviewed source hashes are retained under `/private/tmp/apex-browser-durable-backend/`: `recovery-storage-targeted.jsonl`, `recovery-postgres.jsonl`, `recovery-storage-race.jsonl`, `recovery-storage-docs.log`, and `recovery-storage-source-hashes.json`.

The broader API suite's earlier timeout remains an open serial final-source validation gate before merge. These targeted checks establish the new storage contracts, not full controller recovery or real browser qualification.

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-runtime-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-runtime-2026-10-21"></a>

## Bounded replay recovery runtime — 2026-10-21


This BE-B slice composes the verified replay backend, immutable receipts and terminal reconstruction into a bounded controller adapter. It remains passive until server admission, HTTP authorization and lifecycle cleanup wiring are enabled together.

<a id="roadmap-browser-replay-runtime-2026-10-21--contracts"></a>
### Contracts

The adapter captures its backend configuration and opens a scoped backend for each operation. Four read slots cover both status verification and staged downloads; a download owns its slot and private files until its idempotent close. Two publication slots limit simultaneous writers. Status verification has a 30-second deadline; complete materialization and publication have two-minute deadlines. Publication reserves at most 250 global / 100 project receipts and 8 GiB global / 2 GiB project bytes, including staging and cleanup-pending reservations, with at most two hours of terminal retention bounded by the owning lease.

Recovery requires an explicit project and an unexpired, joined, ready receipt. After verifying the report or complete native pair, it rechecks the same specification and expiry against current storage before returning data. Project loss, expiry, generation change and cleanup prevent delivery. Unknown or foreign-project identities are not found; known unavailable native evidence is not synthesized. Status-only reads do not assert current HAR availability.

Cleanup claims at most 32 joined receipts per 30-second sweep. A session-ID cursor advances past foreign source identities and failed deletions, then wraps for retries. The cursor is scheduling state, never writer proof. Only exact cleanup against the matching backend can release a reservation. Unknown files and unjoined writers remain retained; deleting only part of an authorized generation cannot acknowledge complete cleanup.

<a id="roadmap-browser-replay-runtime-2026-10-21--review"></a>
### Review

Review added an independent publication deadline and regression coverage for full foreign-backend pages, post-I/O receipt changes, admission/cancellation and cleanup retries. It also identified that generated store overlays must expose the new receipt methods, otherwise server tests could silently take a local-only path. The regenerated overlay adds only the eight replay-receipt methods. Regression checks exercise publication, recovery and cleanup through it. Final review found no remaining issues, including low severity.

This slice does not enable recovered HTTP routes or certify a cloud backend, native platform crash recovery or shared multi-host filesystem. No private import target is replayed.

<a id="roadmap-browser-replay-runtime-2026-10-21--validation"></a>
### Validation

Initial targeted checks pass with 17 test/package events; expanded checks pass with 20. After regeneration and the final overlay regressions, three race-detector repetitions of runtime, backend, publisher, retained-report and reader-ownership tests pass with 422 events, no skips and no failures. Source hashes remained unchanged through that final run. Documentation checks pass for 324 active Markdown files; whitespace checks pass.

Raw local evidence under `/private/tmp/apex-browser-durable-backend/` includes `runtime-targeted.jsonl`, `runtime-final-targeted.jsonl`, `runtime-reviewed-race.jsonl`, `runtime-reviewed-source-hashes.json` and `runtime-docs.log`. The previous full Go and private HAR import gate is recorded in the [read-contract slice](#roadmap-browser-replay-read-contract-2026-10-21); a combined final-source gate remains required before merge. No dashboard assets or import behavior changed in this slice.

<!-- Original record: docs/qa/reviews/roadmap-browser-replay-volume-identity-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-replay-volume-identity-2026-10-21"></a>

## Filesystem replay source identity — 2026-10-21


Review before activating controller replay recovery found that a filesystem path alone could not distinguish independent disks mounted at the same path on different hosts. A cleanup worker could otherwise mistake absence on the wrong disk for successful deletion of the original artifacts. This slice fixes that ownership boundary before runtime activation.

<a id="roadmap-browser-replay-volume-identity-2026-10-21--contracts"></a>
### Contracts

The reserved replay namespace retains a random, immutable `source.v1` marker. Initialization syncs a private temporary file, publishes it with an exclusive hard link and syncs the containing directories. Concurrent openers converge on the same complete identity; corrupt or linked markers fail closed. The filesystem source digest now binds the configured path and persisted namespace identity. Object-backend source digests are unchanged.

Each filesystem adapter holds its opened root directory for its lifetime. Stage, verified reads and exact cleanup use that handle, so replacing the configured mount/path cannot redirect an admitted operation to another disk. Operations also recheck the retained marker, and successful staging rechecks it before providing publication proof. File creation/deletion and their directory entries are synced, including cleanup retries that find an already-absent generation.

A fresh replacement volume cannot read or acknowledge cleanup of the original receipt. Historical path-only receipts are preserved but refused: their digest cannot prove the volume. No migration silently adopts their files or releases their reservation. This implementation was still passive when the boundary was corrected.

The marker belongs to the archive namespace and must be preserved with an intentional restoration of that namespace. Copying it asserts the same source authority; it is not proof that independently cloned disks provide shared storage. The filesystem must support exclusive hard links and directory synchronization; unsupported operations fail closed. Actual multi-host filesystem, native Windows/Linux and cloud-backend qualification remain separate gates.

<a id="roadmap-browser-replay-volume-identity-2026-10-21--review"></a>
### Review

The initial fix added persistent source identity and held-root operations. Follow-up review added rechecks for a marker changed after opening and directory synchronization on cleanup retries. Regression tests cover reopening, concurrent initialization, same-path replacement, preserved foreign generations, legacy receipt refusal and corrupt/linked/changed markers. A final review of source ownership, publication ordering, read verification, cleanup and compatibility found no remaining findings, including low severity.

Controller recovered authorization/download/cleanup wiring remains open.

<a id="roadmap-browser-replay-volume-identity-2026-10-21--validation"></a>
### Validation

Initial targeted checks pass with 58 test/subtest events. After the final changed-marker fence was added, three race-detector repetitions of all replay volume, backend and publisher checks pass with 177 events, no skips and no failures. The final source hashes remained unchanged through that run.

Documentation checks pass for 322 active Markdown files; whitespace checks pass. Local evidence under `/private/tmp/apex-browser-durable-backend/` includes `volume-targeted.jsonl`, `volume-reviewed-race.jsonl`, `volume-reviewed-source-hashes.json` and `volume-docs.log`. Tests ran on the local macOS filesystem and the existing file-backed object fixture; they do not certify remote shared filesystems or native Windows/Linux behavior. The broader serial final-source Go gate remains required before merge.

<!-- Original record: docs/qa/reviews/roadmap-browser-request-timing-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-request-timing-2026-10-21"></a>

## Native request timing snapshots — 2026-10-21


The authored-page prototype reproduced a native timing race: a navigation consumer read the SDK's internal timing pointer while its dispatcher wrote response completion. The patched pinned SDK serializes response initialization and completion with detached timing snapshots. Callers cannot mutate the SDK's record, and partial native initializers retain previously observed fields. Driver/browser versions and target traffic are unchanged. The upstream provenance manifest retains original hashes and explicitly declares request.go/response.go as altered; CI and Makefile run the new dependency-module contract tests.

<a id="roadmap-browser-request-timing-2026-10-21--validation-and-review"></a>
### Validation and review

Full Go suite: **35,814 passing tests/subtests**, 38 explicit skips, zero failures. SDK race checks: **250** passing tests/subtests across ten repetitions; native runtime/recorder/API race checks: **93** across three repetitions. Both auth profiles passed the actual native browser panel/HAR workflow and related browser workflows: dev-admin had four passes and one explicit permission-test skip; all five passed under local-auth. The private HAR import retains the prior stable results. Documentation links passed, and final timing-slice review found no remaining findings. Raw validation logs, tested source/asset hashes, screenshots, the initial reproduced race and `evidence/SHA256SUMS` remain local; only this Markdown evidence record is committed. Authored-page receipts remain a separate uncommitted prototype. No actual-device or external-platform qualification is implied.

<!-- Original record: docs/qa/reviews/roadmap-browser-schedule-policies-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-schedule-policies-2026-10-26"></a>

## Roadmap Review — Recurring Schedule Browser & Journey Policy Selection (`BI-C2`, 2026-10-26)


<a id="roadmap-browser-schedule-policies-2026-10-26--scope"></a>
### Scope

Work package `BI-C2` (`BI-4`, `docs/ROADMAP_IMPLEMENTATION_SPEC.md` §BI-C):

1. **Durable Schedule Persistence (`pkg/storage`, Schema v85):**
   - Added `AnalysisPolicySelections any` (`json:"analysis_policy_selections,omitempty"`) to `storage.ScenarioSchedule` and `analysis_policy_selections_json TEXT` to `scenario_schedules` DDL and `schemaColumnMigrations` (schema v85 `scenario-schedule-analysis-policy-selections`).
   - Updated SQLite/PostgreSQL schedule create, CAS update, approval-atomic save, and scan paths in `pkg/storage/sqlite_schedules.go` and regenerated `docs/DATA_MODEL.md`.
2. **Schedule Validation, Materialization & Tombstone Refusal (`pkg/api`):**
   - Added `AnalysisPolicySelections` to `CreateScenarioScheduleRequest` and validated selected browser/journey policy revisions in `PreviewScenarioScheduleRuns`, `CreateScenarioSchedule`, and `UpdateScenarioSchedule` via `validateScenarioSchedulePolicySelections`.
   - Preserved existing `AnalysisPolicySelections` when omitted on partial `PUT /api/v1/scenarios/{id}/schedules/{schedule_id}` updates and cleared back to project defaults (`nil`) when explicitly passed `{}`.
   - Propagated `AnalysisPolicySelections` into `runScenarioScheduleLocked` so materialized scheduled runs pin the selected historical revision (`source: "run_selection"`) even after newer policy revisions are published, and fail materialization with `occurrence.Status = "failed"` and `occurrence.ReasonCode = "analysis_policy_unavailable"` when a selected policy is deleted before materialization.
   - Mapped configuration errors (`errAnalysisPolicySelectionUnavailable`) to `409 Conflict`, caller-supplied digest/malformed errors (`errInvalidRunAnalysisPolicySelection`) to `400 Bad Request`, and store read outages (`errRunPolicyStoreUnavailable`) to `503 Service Unavailable` across schedule preview, create, and update handlers.
3. **Dashboard Schedule Authoring & Catalog (`dashboard/`):**
   - Added `analysis_policy_selections` to `ScenarioSchedule`, `ScenarioScheduleRequest`, and `ScheduleDraft` (including `newTestDraft.ts` storage validation).
   - Mounted `<RunPolicySelector>` in the `NewTestPage.tsx` schedule form, wired `analysis_policy_selections` into debounced schedule preview, create, and update requests, and rendered pinned revision chips (`browser policy rN`, `journey policy rN`) in the saved schedules table and `NewTestScheduleCatalog.tsx`.

<a id="roadmap-browser-schedule-policies-2026-10-26--validation-mutation-verification"></a>
### Validation & Mutation Verification

- **Storage Unit & Migration Tests:**
  - `go test -race -count=1 ./pkg/storage -run "TestAnalysisPolicySelection|TestRunAnalysisPolicySelections|TestSQLiteStorePersistsScenarioSchedules|TestSchema"`
- **API Unit & Integration Tests:**
  - `go test -race -count=1 ./pkg/api -run "TestRunAnalysisPolicySelection|TestScenarioScheduleAnalysisPolicySelection"`
- **Dashboard Unit, Typecheck, Lint & Playwright E2E:**
  - `cd dashboard && npm run typecheck && npm run lint`
  - `cd dashboard && npx vitest run src/types/openapiContract.test.ts src/components/newTestDraft.test.ts src/components/NewTestScheduleCatalog.test.tsx src/components/NewTestPage.test.tsx src/labelCasing.test.ts`
  - `cd dashboard && npx playwright test e2e/workflow-run-policy-selection.spec.ts` (both `dev-admin` and `local-auth` profiles)
- **Mutation Checks (`go test -overlay`):**
  - Mutant 1 (`pkg/api/scheduler.go`): Drop `AnalysisPolicySelections: decodeStoredRunAnalysisPolicySelections(schedule.AnalysisPolicySelections)` in `runScenarioScheduleLocked` → caught by `TestScenarioScheduleAnalysisPolicySelectionValidationMaterializationAndTombstone` (materialized run falls back to project default rev 2 instead of pinned rev 1).
  - Mutant 2 (`pkg/api/scheduler.go`): Omit `occurrence.ReasonCode = "analysis_policy_unavailable"` on `errAnalysisPolicySelectionUnavailable` in `runScenarioScheduleLocked` → caught by `TestScenarioScheduleAnalysisPolicySelectionValidationMaterializationAndTombstone`.

<a id="roadmap-browser-schedule-policies-2026-10-26--remaining-dependencies"></a>
### Remaining Dependencies

- CLI/MCP parity for `--browser-policy` and `--journey-policy` on `apexctl schedule`, `apexctl ci run`, and `apexctl run --api` is tracked in `BI-C4`.

<!-- Original record: docs/qa/reviews/roadmap-browser-seeded-regression-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-seeded-regression-2026-10-27"></a>

## QA Review Record — Browser Seeded-Regression Exit Through the Production Path (`BI-A4`, 2026-10-27)


<a id="roadmap-browser-seeded-regression-2026-10-27--scope"></a>
### Scope

This slice closes `BI-A4` (`Seeded-regression exit through the production path`) by proving the end-to-end `BI-A` qualification, admission, baseline comparison, and `browser_findings.v2` provenance chain across two real managed Chromium runs launched through the controller's production HTTP and engine path (`TestBrowserSeededRegressionProductionPath` in [pkg/api/rca_browser_seeded_regression_test.go](../../../pkg/api/rca_browser_seeded_regression_test.go)):

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

<a id="roadmap-browser-seeded-regression-2026-10-27--verification-mutation-testing"></a>
### Verification & Mutation Testing

- Real Playwright/Chromium production-path qualification (`APEXLOAD_BROWSER_TESTS=require`):
  - `APEXLOAD_BROWSER_TESTS=require go test -v -count=1 -timeout=15m ./pkg/api -run '^TestBrowserSeededRegressionProductionPath$'` (`PASS`, `200.83s`; all 5 refusal subtests pass).
- Acceptance inventory freshness:
  - `go test ./docs/qa -run '^TestBrowserIntelligenceAcceptanceInventory$'`
- Mutation verification (`go test -overlay`):
  - Mutated `pkg/api/rca_browser_selection.go` (`Status: "current_identity_unavailable"` -> `Status: "selected"`) via a temporary `-overlay` JSON mapping outside the workspace; verified `go test -overlay=... ./pkg/api -run '^TestRCABrowserSelectionAdmissionFreezesAndExportsRefusal$'` fails (`Exit code: 1`).

<!-- Original record: docs/qa/reviews/roadmap-browser-selection-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-selection-2026-10-21"></a>

## Frozen browser baseline selection — 2026-10-21


Latest-compatible RCA admission now consumes the bounded inventory from the
[qualification foundation](#roadmap-browser-qualification-2026-10-21).
It refuses overflow, missing current identity, incomplete projections and
unqualified current evidence. It verifies candidate project/application/scenario,
canonical identity, run incarnation, execution generation, terminal state,
archive identity and pinned browser policy before comparing evidence.

Admission inspects the complete bounded inventory under a ten-second cooperative
deadline, using existing integrity-checked archive readers and materialization quotas.
It reads summary and browser-document evidence, not transaction populations.
Database timestamps only determine inspection order. Selection ranks compatible
candidates by the immutable manifest completion instant, with canonical ID as
the tie-breaker, and requires completion strictly before the current run starts.
This preserves nanosecond fences even when PostgreSQL stores microseconds.
Database identity and publication reads include connection-pool waits in that
deadline. Unknown publication state and outstanding jobs (including failed jobs
that can retry) abort admission. The current run is checked before and after
inspection as well as each candidate. Transient errors and concurrent
finalization abort admission rather than silently selecting an older candidate.
Truncated document projections report incomplete evidence; a matching prefix cannot qualify omitted later populations.

The versioned receipt records the policy, inventory count, outcome, rejected
candidates and reasons, and selected canonical ID/evidence digest when available.
Its hash participates in deduplication. The queue freezes the receipt and selected
archive; workers verify those inputs without repeating selection. Assessments
and exported bundles retain the receipt. Explicit caller baselines still take
precedence. Archive adapter version 9 prevents reuse under earlier admission
semantics, while completed historical assessments remain readable.

Current native archives still lack positive browser-to-controller clock and
complete expected-source proof. Their automatic selection therefore records a
refusal; raw observations remain available. Synthetic qualified inputs exercise
the positive selector mechanics, including zero-variance compatible baselines.
This slice does not qualify native production runs or complete BI-A acceptance.

Schema release 76 adds the corrected default-project expression index without
rewriting release 75. Query and index share the same default-project constant.
Legacy NULL/empty project IDs belong to `unassigned`; a separate project named
`default` is excluded.

<a id="roadmap-browser-selection-2026-10-21--validation"></a>
### Validation

Focused RCA and API tests passed after the initial independent review fixes.
Coverage includes exact offset/nanosecond ordering, stored-precision collisions,
start-boundary refusal, inventory overflow, missing proof, population coverage,
transient failure/cancellation, frozen policy edits, explicit precedence,
deduplication, receipt tampering and exported receipts.
Two independent reviewers reported no remaining findings after fixes for point
truncation, timestamp precision, exported-receipt coverage and legacy project
normalization, publication-state uncertainty and database deadlines. Final targeted
API checks passed (4.005 seconds). SQLite candidate/schema and connection-pool deadline checks passed (0.815 seconds); the same
candidate regression passed on PostgreSQL in an isolated schema (final rerun:
1.345 seconds).
RCA race checks passed (1.639 seconds). Documentation/link checks passed for 334
active Markdown files. The final full Go gate passed: 168 packages passed,
10 skipped, zero failures; 36,243 tests passed and 46 skipped. The API suite
passed in 919.830 seconds. The private HAR retained its original archive digest
and imported 803 requests into 476 actions, with 479 assertions, 37 extractors,
43 correlations and zero DSL validation issues. It was not replayed. Latest main
was fetched and already integrated before the full gate. Session logs stay
outside the repository under `/private/tmp/apex-browser-durable-backend/` with
the `browser-selection-` prefix. The private HAR and Salesforce recording remain
local and are not committed. The final test summary is
`browser-selection-final-summary.json` in the same session directory. The retained
Salesforce recording was replayed through the real manager with stop-on-error; the site refused the initial
navigation with HTTP 403 (Access Denied), so its three clicks did not run. The
manager closed its owned browser session. This is not a successful external
qualification.

Private Go build/test caches were cleared after all validation, reclaiming
approximately 7.6 GiB. Evidence logs, private fixtures, dependencies and deliverables
remain. Shared Go/Docker cache cleanup is deferred while parallel work is active;
no images, containers, volumes or application data were removed. This slice
created no frontend build cache. Native clock and complete expected-source proof
acquisition remains the next implementation slice.

<!-- Original record: docs/qa/reviews/roadmap-browser-source-windows-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-source-windows-2026-10-21"></a>

## Browser baseline full measurement windows — 2026-10-21


<a id="roadmap-browser-source-windows-2026-10-21--scope"></a>
### Scope

RCA now retains the earliest start and latest end of every final document
measurement contributing to a bucket's p75. Progress revisions and unfinished
measurements remain excluded. Raw archive evidence, native analytics panels,
bucket values and retained document counts are preserved.

Both current and baseline populations require recorded, valid full measurement
windows inside each bucket's admitting steady-load span. A final publication that
lands in a steady bucket does not prove its entire measurement occurred at that
load. Missing or malformed windows, ramp crossings and windows bridging a pause
between equal-load spans leave the comparison unscored, with
`source_window_invalid` or `baseline_source_window_invalid` and no z-score or
expected range. This conservatively refuses the affected comparison rather than
recalculating its distribution after dropping individual documents. Buckets outside
the compared spans remain excluded and cannot poison an otherwise valid comparison.

Window envelopes use stable UTC bucket timestamps, independent of point ordering.
Equal start/end timestamps remain valid; source-window containment has no rounding
tolerance. Malformed contributors cannot be hidden by other valid contributors.
Only emitted populated buckets retain envelopes in the bounded RCA projection.

The `browser_baseline.v1` estimator remains unchanged. Engine v6 prevents new
requests from reusing an older assessment; completed historical results remain
readable. These checks qualify recorded timestamp containment, not synchronization
of producer clocks or complete source coverage. Latest-compatible selection,
browser-findings integration and real controlled two-run qualification remain open.

<a id="roadmap-browser-source-windows-2026-10-21--validation-and-review"></a>
### Validation and review

Documentation checks passed for 332 active Markdown files; whitespace checks passed.

- Targeted browser-baseline RCA and API checks passed (RCA 0.144 seconds,
  API 4.201 seconds).
- `go test -race ./pkg/rca -timeout=5m` passed in 1.591 seconds.
- The attached private HAR imported successfully in 36.710 seconds: 803 requests,
  476 actions, 479 assertions, 37 extractors, 43 correlations and zero DSL
  validation issues. Its archive digest is unchanged; it was not replayed.
- The archive-to-RCA envelope regression passed explicitly in 1.652 seconds.
  A local review caught its original `_windows_test.go` filename, which Go treated
  as Windows-only. Renaming it to `_window_test.go` made it eligible on this host;
  the explicit run supplements the full suite started before that rename.
- Two independent agents reviewed the implementation and regressions without
  findings. Independent re-review after the test-discovery fix was also clean.
- The full API suite passed in 889.304 seconds, including historical-assessment
  compatibility. Latest main was fetched and already integrated before the full
  Go run; packages ran serially with a private cache.
- Full Go run: 167 packages passed, 10 skipped and 1 failed.
  It encountered one unchanged HTTP/3 fixture timeout:
  `pkg/internal/h3fixture/TestCloseReleasesGoroutines` timed out on its initial
  loopback QUIC GET before reaching the leak assertion. The complete fixture
  package then passed ten consecutive runs in 19.939 seconds. Independent review
  found no dependency on the modified code and no justified fixture change.
  The original failed run is retained; this is not described as a clean full run.

Logs are retained under `/private/tmp/apex-browser-durable-backend/`:
`source-window-targeted.log`, `source-window-race.log`,
`source-window-full.jsonl`, `source-window-envelope.log` and
`source-window-har-summary.json` and `source-window-h3-recheck.log`.
No UI or browser protocol behavior changed; these checks do not claim live
browser or infrastructure qualification.

<!-- Original record: docs/qa/reviews/roadmap-browser-timeline-qualification-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-timeline-qualification-2026-10-26"></a>

## Browser timeline and onset qualification (BI-A2)


This slice enables in-run `ScopeTime` onset detection and timeline alignment for qualified browser document series (`TimelineQualification = "qualified"`, `apexload.rca.engine.v8`) in [BI-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#bi-a--qualified-baseline-selection-and-findings).

<a id="roadmap-browser-timeline-qualification-2026-10-26--contract-and-invariants"></a>
### Contract and invariants

1. **Timeline qualification (`pkg/api/rca_browser_documents.go`, `pkg/rca/browser_qualification.go`, `pkg/rca/detect.go`):**
   - `rcaBrowserDocumentSeries` sets `s.Document.TimelineQualification = rca.BrowserEvidenceQualified` and `s.ClockUncertainty = t.clockUncertainty` only when:
     - `t.mapped && clockProofComplete` (`s.Document.ClockQualification == rca.BrowserEvidenceQualified`),
     - `s.Document.SourceCoverageQualification == rca.BrowserEvidenceQualified` and `!s.Document.Incomplete`,
     - `t.clockUncertainty >= 0 && t.clockUncertainty < bucket` (sub-bucket clock uncertainty), and
     - at least one populated bucket window in `s.Document.Windows` lies inside a verified steady-load span (`w.MonotonicVerified && !w.Invalid`).
   - Otherwise, `s.Document.TimelineQualification` remains `rca.BrowserEvidenceUnavailable`, and `rca.Detect` excludes the series with `rca.BrowserTimelineUnavailable` (`"browser_timeline_unavailable"`). `rca.Detect` also refuses `SeriesBrowserDocument` with `BrowserTimelineUnavailable` when `s.Document.Incomplete`, `s.Observation != ObservationInterval`, `s.Bucket <= 0`, `docUncertainty < 0 || docUncertainty >= s.Bucket`, or `!browserBucketGridValid(sortedPoints(s.Points), s.Bucket)`, and only counts non-browser series toward `LimitationBaselineUnmatched`.
   - Distributed observations whose `clockUncertainty >= bucket` mark their bucket window `Invalid: true` with `InvalidReason: "observation_window_exceeds_bucket"`.

2. **Invalid bucket filtering, span boundary enforcement, and uncertainty propagation (`pkg/rca/detect.go`):**
   - `usableForSeries(s Series, p SeriesPoint)` requires `s.Document != nil` and a present, non-invalid `DocumentWindow` at `p.At.UTC()` (`!ok || w.Invalid` returns `false`) for `SeriesBrowserDocument` before any point enters baseline construction, CUSUM scoring, or spike detection in `inRun`.
   - In `inRun`, `SeriesBrowserDocument` treats each `LoadLevel` independently (never pooling equal-load spans across pauses) and verifies `browserSourceWindowsWithin(s, usableSpan, levels[index])` before judging any level span (refusing spans that bridge distinct `MonotonicSpanID`s across a pause, mix raw and monotonic proof modes, or cross steady-load boundaries).
   - `ScopeLoad` cross-run comparison for `SeriesBrowserDocument` remains exclusively in `browser_baseline.v1` (`DetectBrowserBaseline`).
   - `detector.open` propagates `max(s.ClockUncertainty, s.Document.ClockUncertainty)` into `dir.uncertainty` (`Anomaly.OnsetUncertainty = spread + s.Bucket + clockUncertainty`).

3. **Engine version identity (`pkg/rca/assessment.go`):**
   - `EngineVersionBase` is bumped from `"apexload.rca.engine.v7"` to `"apexload.rca.engine.v8"`, preserving historical v7 assessments while preventing reuse under earlier qualification rules.

<a id="roadmap-browser-timeline-qualification-2026-10-26--validation-and-mutation-testing"></a>
### Validation and mutation testing

- `go test -race -count=2 -shuffle=on ./pkg/rca/...`: passed (`TestBrowserTimelineOnsetQualificationAndInvalidBucketFiltering`, `TestBrowserQualificationAlsoGuardsInRunDetection`).
- `go test -race -count=2 -shuffle=on -run "TestRCADistributedTwoGeneratorBrowserClockQualificationAndRTTRefusal|TestBrowserSourceSameBucketCannotBridgePause|TestRCABrowserQualificationVersionPreservesHistoricalAssessment" ./pkg/api`: passed.
- Mutation verification via `go test -overlay`:
  - Mutant 1 (`if !ok || w.Invalid` -> `if !ok && w.Invalid` in `usableForSeries`, `pkg/rca/detect.go`): killed by `TestBrowserTimelineOnsetQualificationAndInvalidBucketFiltering` (`invalid bucket 10 leaked into spikes`).
  - Mutant 2 (`dir.uncertainty = spread + s.Bucket + clockUncertainty` -> `dir.uncertainty = spread + s.Bucket` in `detector.open`, `pkg/rca/detect.go`): killed by `TestBrowserTimelineOnsetQualificationAndInvalidBucketFiltering` (`OnsetUncertainty = 1s, want >= 1.015s`).

<a id="roadmap-browser-timeline-qualification-2026-10-26--review-and-remaining"></a>
### Review and remaining

- Reviewed by code-safety and test/QA subagents to zero findings.
- Remaining in Track `BI-A`: evaluated baseline status in findings (`BI-A3`, `browser_findings.v2`) and the 95 s two-run seeded-regression production exit (`BI-A4`).

<!-- Original record: docs/qa/reviews/roadmap-browser-tuple-panels-2026-10-21/README.md; local evidence kept outside git -->
<a id="roadmap-browser-tuple-panels-2026-10-21"></a>

## Separate browser tuple panels — 2026-10-21


Archive panels may explicitly select up to 20 complete browser measurement tuples, each retaining its own final-document distribution, runtime cohort identity, missing values and counts. Query v3 and result v1 keep the legacy bucket array empty. Older queries still refuse mixed tuples. Limits fail explicitly instead of dropping a population. Exact-tuple comparisons disclose missing counterparts and refuse incomplete/excluded populations. They are workload comparisons, not baseline anomaly scoring. Multi-tuple derivations remain unavailable.

<a id="roadmap-browser-tuple-panels-2026-10-21--validation-and-review"></a>
### Validation and review

- Full Go suite: **35,814 passing tests/subtests**, 38 explicit skips, zero failures, with native browser tests required. Private HAR import stable results match the prior baseline.
- Focused race checks: **111 passing tests/subtests** over three repetitions.
- The full dashboard cycle, both authentication profiles, generated contracts, exact embedded Help and documentation links passed. Actual local Chromium desktop and Pixel emulation verify authoring, replay, native HAR integrity, independent request counts, archived tuple series, saved panel reload and rendered CSV exports. Native managed browser chains retain archive/report/HAR evidence.
- Review fixed the completed-run integration gap: report Performance now exposes project panel inspection and authorized editing, with scoped requests and cancellation. The real browser workflow then passed in both profiles; the initial failed trace remains local. Final review found no remaining issues. Local evidence includes `evidence/validation-summary.json`, source/asset hashes, browser screenshots, the full dashboard-cycle log and `evidence/SHA256SUMS`.

Raw evidence remains local; only this Markdown record is committed. This slice does not qualify actual devices or external hosts. Immutable comparison-to-panel input pins, authored page identity, compatible baseline selection and provisional live findings remain open.

<!-- Original record: docs/qa/reviews/roadmap-browser-ui-managed-chain-2026-10-27/README.md; local evidence kept outside git -->
<a id="roadmap-browser-ui-managed-chain-2026-10-27"></a>

## Roadmap Review — `BE-B6`: UI-Driven Managed Chain Across Chromium and WebKit Cohorts (2026-10-27)


<a id="roadmap-browser-ui-managed-chain-2026-10-27--contract"></a>
### Contract

`BE-B6` qualifies one unstubbed Playwright E2E workflow in [`dashboard/e2e/workflow-browser-har.spec.ts`](../../../dashboard/e2e/workflow-browser-har.spec.ts) that drives:

1. **Authoring in the dashboard UI**: Opening a `protocol: "browser"` script in [`ScriptEditor.tsx`](../../../dashboard/src/components/ScriptEditor.tsx), selecting the target `Browser Engine` (`chromium` or `webkit`) and `HAR capture` policy (`always`) alongside `Capture page timings and Web Vitals` and `Capture interaction evidence` in [`ScriptRuntimeSettingsView.tsx`](../../../dashboard/src/components/ScriptRuntimeSettingsView.tsx), and saving the script.
2. **Native browser replay in the UI**: Running a native browser replay session (`POST /api/v1/scripts/{id}/browser-replay/sessions` with `record_har: true`) from the `Native browser replay` section of [`ScriptBrowserEvidence.tsx`](../../../dashboard/src/components/ScriptBrowserEvidence.tsx), waiting for `Verified ✓`, and downloading the native replay HAR (`X-APEXLoad-HAR-Fidelity: native`) and replay evidence ZIP through [`BrowserReplayProgress.tsx`](../../../dashboard/src/components/BrowserReplayProgress.tsx).
3. **Managed run execution**: Launching a 1-iteration managed run from the `New Test` page (`/#/new-test`), waiting for run completion and archive manifest publication.
4. **Archived resource inspection and native HAR downloads without `page.route` stubs**: Navigating to the run report Diagnostics → Browser evidence view, opening the retained archive resource inspector against the real archived resource page, verifying query-token redaction (`?token=[REDACTED]`), drilling from the retained resource row into its native HAR entry, and downloading both the standalone native HAR (`fidelity: "native"`, no synthetic `_apexload` envelope) and the HAR-inclusive archive ZIP (`?include=har`).
5. **Persona RBAC enforcement in `local-auth`**: Verifying that a `viewer` bound to the domain lists both synthetic and native HARs (`can_download: false`), sees disabled `Download synthetic HAR` and `Download native HAR` controls with the editor-access notice, and receives `403 Forbidden` on direct native/synthetic archive HAR downloads, `?include=har` archive exports, and `/api/v1/browser-replay/sessions/{id}/har` + `/evidence`, while an unbound user is denied (`403`) across all endpoints.

<a id="roadmap-browser-ui-managed-chain-2026-10-27--invariants"></a>
### Invariants

- `ScriptRuntimeSettingsView` exposes `HAR capture` (`har_capture`: `off`, `always`, `on_error`, `on_sla_breach`, `manual`) under `Browser Emulation`, clearing `config.browser.har_capture` to `undefined` when reset to `off`.
- `ScriptEditor.tsx` wires `onStartNativeReplay` (`record_har: true`, calling `startBrowserReplaySession(scriptId, payload)` when `scriptId` is present and `startDraftBrowserReplaySession(payload)` for unsaved draft scripts without a `scriptId`) into `ScriptBrowserEvidence.tsx`, which renders `Run native browser replay` (fenced while starting or running) and `<BrowserReplayProgress>` with native replay HAR and evidence downloads.
- Neither `UI-driven Chromium managed chain authors, replays, runs and downloads native HARs without stubs` nor `UI-driven WebKit managed chain authors, replays, runs and downloads native HARs without stubs` installs any `page.route` stub.
- Raw query credentials (`secret-query-token`) never appear in the downloaded replay HAR, archived native HAR, per-request HAR download, or retained resource table.

<a id="roadmap-browser-ui-managed-chain-2026-10-27--validation"></a>
### Validation

- **Unit & Lint Gates (`dashboard/`)**:
  - `npm run typecheck` — pass (`0` errors).
  - `npm run lint` (`eslint . --max-warnings=0`) — pass (`0` errors, `0` warnings).
  - `npm run lint:a11y` — pass (`0` blocking errors).
  - `npx vitest run src/components/ScriptRuntimeSettingsView.browserEmulation.test.tsx src/components/ScriptBrowserEvidence.test.tsx src/components/runReport/BrowserInteractionsPanel.test.tsx src/components/runReport/JourneyRiskPanel.test.tsx src/components/CommandCenterBrowserGroups.test.tsx src/labelCasing.test.ts` — pass (`6` files, `38` tests).
  - `npm run build && npm run test:bundle-budget && npm run bundle:check` — pass (all route budgets within limits).
- **Mutation Verification**:
  - Removed `setNativeReplaySessionId(sessionId)` in `ScriptBrowserEvidence.tsx` and verified `src/components/ScriptBrowserEvidence.test.tsx` fails immediately (`expected undefined to be 'replay-session-1'`).
  - Mutated `e.target.value === 'off' ? undefined : ...` in `ScriptRuntimeSettingsView.tsx` and verified `src/components/ScriptRuntimeSettingsView.browserEmulation.test.tsx` fails immediately.
- **Playwright End-to-End (`scripts/run-e2e.sh workflow-browser-har.spec.ts`)**:
  - `dev-admin`: `3 passed, 1 skipped (16.3s)` (controller log retained at `dashboard/test-results/controller-logs/dev-admin/run-LxIBny/selected.log`).
  - `local-auth` (`APEXLOAD_E2E_PROFILE=local-auth`): `4 passed (1.1m)` (controller log retained at `dashboard/test-results/controller-logs/local-auth/run-Rr33Tg/selected.log`).
- **Inventory & Documentation Checks**:
  - `APEXLOAD_UPDATE_BROWSER_ACCEPTANCE=1 go test ./docs/qa -run TestBrowserAcceptanceInventory` and `go test ./docs/qa` — pass.
  - `python3 scripts/qa-generate-dashboard.py --check` and `go test ./pkg/api -run TestDashboardDocs` — pass.
  - `python3 scripts/check-docs.py` — pass.

<a id="roadmap-browser-ui-managed-chain-2026-10-27--review"></a>
### Review

- **Round 1**:
  - Code-safety reviewer (`06f0a2b8-77fa-4e27-9b93-447fd15a05ae`): Found `0 High, 0 Medium, 2 Low` — add a synchronous `nativeReplayInFlightRef` fence and `nativeReplayRunning` state in `ScriptBrowserEvidence.tsx` so `Run native browser replay` stays disabled until `<BrowserReplayProgress>` completes or is dismissed, and clarify `startBrowserReplaySession` vs `startDraftBrowserReplaySession` in the review record. Fixed.
  - Test/QA reviewer (`d5dc1e44-5ff9-410c-b4ba-c5c1e7b90ba1`): Found `0 High, 1 Medium, 1 Low` — extend `ScriptBrowserEvidence.test.tsx` to cover resolved `sessionId` rendering `<BrowserReplayProgress>`, `nativeReplayDisabled`/`nativeReplayTitle`, and `onComplete`/`onDismiss` transitions, and record the exact verified mutations in the review record. Fixed.
- **Round 2**: Re-reviewed all Round 1 fixes; zero High, Medium, or Low issues remained.

<a id="roadmap-browser-ui-managed-chain-2026-10-27--remaining"></a>
### Remaining

- Local macOS Chromium and WebKit UI-driven managed chain cohorts are qualified; Linux and Windows UI-driven managed chain cohorts remain tracked per host/engine in [`docs/qa/BROWSER_ACCEPTANCE.md`](../../qa/BROWSER_ACCEPTANCE.md).

<!-- Original record: docs/qa/reviews/roadmap-browser-vuser-attribution-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-browser-vuser-attribution-2026-10-26"></a>

## Roadmap QA Review — Per-VUser Browser RSS Attribution and v3 Benchmark Contract (`BE-C1`)


- **Date:** 2026-10-26
- **Requirement / Track:** `BE-4` / `BE-C` (`BE-C1`)
- **Worktree / Branch:** `be-c1-vuser-attribution`

<a id="roadmap-browser-vuser-attribution-2026-10-26--scope-and-problem-statement"></a>
### Scope and Problem Statement

Before this slice:

1. `TestBrowserBenchmark` (`pkg/protocol/browser/browser_benchmark_test.go`) and `BenchmarkBrowserRSSPerChromiumDeviceProfile` (`pkg/protocol/browser/mobile_benchmark_test.go`) sampled the combined Playwright driver + Chromium process tree and declared `RSSAttribution: "shared_playwright_driver_descendants_or_driver"`, which mixed shared browser/GPU/utility/driver overhead into per-VUser RSS and did not isolate individual `BrowserContext` renderer processes.
2. `TestBrowserBenchmark` recorded `CaptureOverheadMs` (navigation with document evidence capture vs. plain navigation) rather than action or touch-over-click-baseline overhead (`ActionOverheadTargetMs: 10`).
3. Legacy ambiguous unit fields remained in the contract (`RSSPerVUserTargetMB` on `BrowserBenchmarkAcceptance`, `memory_mib` in `benchmarkHost()`, and `rss_mib/op` in `BenchmarkBrowserRSSPerChromiumDeviceProfile`), capacity planning (`browserCapacityProfileForTier`) used raw decimal MB values (`180`, `220`, `260`, `280`) directly against binary MiB `Generator.MemoryMB` (`8192 = 8 * 1024 MiB`), and `observerMaxVUsers` (`10`) in `pkg/api/goal_controller.go` exceeded `MaximumClaimedVUsersPerGenerator: 5`.

<a id="roadmap-browser-vuser-attribution-2026-10-26--implemented-changes"></a>
### Implemented Changes

1. **CDP `SystemInfo.getProcessInfo` Per-VUser Renderer Attribution (`pkg/protocol/browser/vuser_attribution.go`):**
   - Added `parseCDPProcessInfo`, `queryBrowserCDPProcessInfo`, `snapshotBaselineRendererPIDs`, `attributeVUserRendererPIDs`, `parseLinuxSmapsPSSBytes`, `readLinuxProcessPSSBytes`, and `computeAttributedVUserMemory`.
   - On Chromium (`desktop_1080p`, `pixel_8`), opens a browser-level CDP session (`Browser.NewBrowserCDPSession()`) and diffs `SystemInfo.getProcessInfo` `renderer` PIDs before and after `BrowserContext` creation/navigation to attribute `per_vuser_rss_bytes` (plus Linux `/proc/<pid>/smaps_rollup` `Pss` in `per_vuser_pss_bytes`) separately from `fixed_overhead_bytes` (Playwright driver + `browser` + `gpu` + `utility` + `zygote` PIDs) with `rss_attribution: "cdp_renderer_per_vuser"`.
   - On WebKit (`desktop_1920x1080`, `iphone_15_pro`), where `NewBrowserCDPSession` and `SystemInfo.getProcessInfo` are unsupported, records `rss_attribution: "unavailable"` and leaves `per_vuser_rss_bytes`, `fixed_overhead_bytes`, and `peak_rss_bytes` `null`.
2. **Action Overhead and `apexload.browser_benchmark.v3` Schema (`pkg/protocol/browser/browser_benchmark_test.go`, `pkg/protocol/browser/mobile_benchmark_test.go`, `pkg/protocol/browser/vuser_footprint_benchmark_test.go`):**
   - Added `ActionOverheadMs` and `ActionOverheadKind` (`click_evidence_capture_overhead` on desktop, `touch_over_click_baseline` on mobile profiles) to `BrowserBenchmarkResult` and `action_overhead_ms/op` to `BenchmarkBrowserTouchOrClickActionLatency`.
   - Bumped `writeBrowserBenchmark` to emit `"schema": "apexload.browser_benchmark.v3"`, removed `info["memory_mib"]` from `benchmarkHost()`, removed `rss_mib/op` from `BenchmarkBrowserRSSPerChromiumDeviceProfile` (reporting `rss_bytes/op`, `per_vuser_rss_bytes/op`, and `fixed_overhead_bytes/op`), and kept `parseBrowserBenchmarkArtifact` compatible with both `v2` and `v3`.
   - Updated `TestBrowserVUserMarginalRSS` (`N = 1..5` concurrent contexts) to attribute renderer `VmRSS` (and Linux `Pss`) plus `FixedOverheadBytes` via `SystemInfo.getProcessInfo` and compute `computeMarginalRSSLinearRegression` in bytes.
3. **API Acceptance Contract, Capacity Unit Reconciliation, and Observer Cap (`pkg/api/browser_benchmark_evidence.go`, `pkg/api/capacity_planning.go`, `pkg/api/goal_controller.go`, `dashboard/src/types/system.ts`):**
   - Bumped `BrowserBenchmarkAcceptance.MeasurementVersion` to `"apexload.browser_benchmark.v3"`, set `RSSAttribution: "cdp_renderer_per_vuser"` (`RSSPerVUserTargetBytes: 250_000_000`) on Chromium profiles and `"unavailable"` (`RSSPerVUserTargetBytes: 0`) on WebKit, removed `RSSPerVUserTargetMB`, and regenerated OpenAPI/TypeScript contracts via `go generate ./pkg/api`.
   - Added `browserDecimalMBToMiB` in `pkg/api/capacity_planning.go` so the decimal MB tier budgets (`180`, `220`, `260`, `280` around `browserTargetRSSBytes = 250_000_000`) are converted into binary MiB (`1 << 20` bytes) before comparing against `Generator.MemoryMB` (`8192` MiB).
   - Lowered `observerMaxVUsers` from `10` to `5` in `pkg/api/goal_controller.go` so observer-group scaling cannot exceed `MaximumClaimedVUsersPerGenerator: 5`.

<a id="roadmap-browser-vuser-attribution-2026-10-26--verification-and-mutation-check"></a>
### Verification and Mutation Check

- **Unit and Contract Tests (`-race -count=2 -shuffle=on`):**
  - `go test -race -count=2 -shuffle=on ./pkg/protocol/browser -run 'TestBrowserBenchmark|TestParseCDPProcessInfo|TestParseLinuxSmapsPSSBytes|TestComputeMarginalRSSLinearRegression|TestMobileBrowserBenchmarkProfilesResolve'`
  - `go test -race -count=2 -shuffle=on ./pkg/api -run 'TestBrowserBenchmarkEvidenceUsesDecimalByteTargets|TestObserverGroup|TestEstimateCapacity|TestBrowserCapacityProfileReconcilesDecimalMBTargetToGeneratorMiB|TestOpenAPISpec'`
  - `go test ./docs/qa -run TestBrowserAcceptanceInventory`
- **Opt-In Local Real-Browser Verification (`darwin/arm64`, Chromium `149.0.7827.55`, WebKit `26.5`, Playwright `1.61.1`):**
  - Command:
    `APEXLOAD_BROWSER_BENCHMARKS=1 APEXLOAD_BROWSER_BENCHMARK_OUT=<scratch>/be-c1-bench APEXLOAD_COMMIT_SHA=$(git rev-parse HEAD) go test ./pkg/protocol/browser -run '^(TestBrowserBenchmark|TestBrowserVUserMarginalRSS)$' -v -timeout 10m`
  - Local `TestBrowserBenchmark` output (`<scratch>/be-c1-bench/browser-benchmark.json`, SHA-256 `69a0a5a8e8f2d7b49b1f6fa6d60972c6922cad5dbdb5fce9be3fe4265fab38b8`):
    - `chromium/desktop_1080p`: `per_vuser_rss_bytes = 99,680,256`, `fixed_overhead_bytes = 396,902,400`, `peak_rss_bytes = 493,256,704`, `rss_attribution = "cdp_renderer_per_vuser"`, `action_overhead_ms = -0.763` (`click_evidence_capture_overhead`), `leftover_processes = 0`.
    - `chromium/pixel_8`: `per_vuser_rss_bytes = 97,402,880`, `fixed_overhead_bytes = 394,608,640`, `peak_rss_bytes = 490,553,344`, `rss_attribution = "cdp_renderer_per_vuser"`, `action_overhead_ms = -1.332` (`touch_over_click_baseline`), `leftover_processes = 0`.
    - `webkit/desktop_1920x1080` and `webkit/iphone_15_pro`: `per_vuser_rss_bytes = null`, `fixed_overhead_bytes = null`, `peak_rss_bytes = null`, `rss_attribution = "unavailable"`, `rss_unavailable_samples = 15/15`, `leftover_processes = 0`.
  - Local `TestBrowserVUserMarginalRSS` (`N = 1..5` concurrent Chromium `desktop_1080p` contexts):
    - `N = 1`: renderer `VmRSS = 91,914,240` B, diffed VUser `91,914,240` B, fixed overhead `392,970,240` B.
    - `N = 5`: renderer `VmRSS = 460,439,552` B, diffed VUser `92,061,696` B, fixed overhead `442,843,136` B.
    - Linear regression (`N = 1..5`): marginal slope `92,135,424` bytes/VUser (`~92.14 MB` decimal), renderer intercept `-237,568` bytes.
- **Overlay Mutation Verification (`go test -overlay`):**
  - Mutant in `pkg/protocol/browser/vuser_attribution.go` (including shared `browser` PIDs inside `RendererPIDs` instead of `SharedBrowserPIDs`) killed by `TestParseCDPProcessInfoAndAttributeVUserRendererPIDs`.
  - Mutant in `pkg/api/goal_controller.go` (`observerMaxVUsers = 10` instead of `5`) killed by `TestObserverGroupMaxVUsersMatchesCanaryEnvelope`.

<a id="roadmap-browser-vuser-attribution-2026-10-26--remaining-dependencies-be-c2"></a>
### Remaining Dependencies (`BE-C2`)

- Local `darwin/arm64` results verify the `apexload.browser_benchmark.v3` attribution and unit contract only; they do **not** certify the **4 vCPU / 8 GB** reference generator cohort (`generator-canary-4vcpu-8gb`).
- Full reference-host qualification (`-count 5`, `TestBrowserBenchmark`, `TestBrowserVUserMarginalRSS`, and acceptance-record promotion) remains open under `BE-C2`.

<!-- Original record: docs/qa/reviews/roadmap-har-finalization-budget-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-har-finalization-budget-2026-10-19"></a>

## Roadmap implementation: shared HAR finalization deadline


Date: 2026-10-19. Scope: the shared close/flush/scrub deadline in [BE-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#be-a--stable-authored-identity-and-qualified-document-evidence).

<a id="roadmap-har-finalization-budget-2026-10-19--contract-and-limits"></a>
### Contract and limits

The first document join, runtime join or native close starts one monotonic 30-second deadline. Later phases and retries preserve it. The two-second caller wait remains independent. A blocked writer retains its exact lease, raw generation and admission charge; expiration is never proof of shutdown. A late successful physical join can retire the raw generation but cannot publish expired capture output.

Read/scrub/redaction and durable publication check the same deadline. Non-cancellable native calls and in-progress local work can finish after it while retaining ownership; their results cannot qualify publication. If expiry crosses creation of the public link, rollback checks the exact owned inode under the namespace lease. Foreign replacements remain untouched. Successful publication records completion before releasing the owner mutex, so later raw retirement or retries cannot retroactively expire it. Raw retirement uses its existing separately bounded namespace acquisition.

Exhaustion returns a named error and emits `har_finalization_budget_exhausted` with the generator-wide `har_finalization_exhausted` counter, once per owner, even if shutdown is still blocked. Capture policies, secret suppression, scrubbing, size/retention bounds, crash proofs and transfer timeouts remain unchanged. This slice does not implement actual 256 in-flight admission, force cancellation of filesystem/native work, per-run durable exhaustion receipts, or unavailable Linux/Windows qualification.

<a id="roadmap-har-finalization-budget-2026-10-19--validation-and-review"></a>
### Validation and review

The final draft overlay passed focused HAR ownership tests under the race detector (16.095 s), including private staging, existing collisions, crashes and late joins. The applied browser package passed (82.746 s), focused HAR race tests passed (15.705 s), and the browser acceptance inventory passed. Type checking, lint, help unit tests, the embedded dashboard build, documentation links and help ownership/fragment checks passed. Help and HAR Playwright workflows passed in both profiles: dev-admin 12 passed / 2 expected skips; local-auth 14 passed. The full Go suite passed with 168 tested packages and 10 skipped/no-test packages, with no failures. Final scoped self-review found no further issues. Logs, screenshots, source/asset hashes, the full trace and package outcomes are retained in evidence (`evidence/har-full-go-summary.json`). Deterministic writer fixtures are not native platform qualification.

<a id="roadmap-har-finalization-budget-2026-10-19--attached-har-import-compatibility"></a>
### Attached HAR import compatibility

The user-provided ZIP (38,797,527 bytes; identified by SHA-256 in the retained summaries) was streamed through the real multipart HTTP HAR import route in disposable local test servers, on both this change and `origin/main` at `609bf1b1a`. Both returned HTTP 200 with identical aggregate summaries. The initial generated-script hashes differed because each import receives a new draft-attestation capability. A private structural comparison confirmed that this token was the only remaining script difference after clearing the generated script ID and creation time; all action/body/header/correlation content was retained in that comparison. The normalized script digest and exact normalization paths are recorded. All 803 source requests are accounted for: 476 converted request actions and 327 filtered assets, with 479 assertions, 37 extractors and 43 detected correlations. Both imports reported zero DSL validation issues, four warnings and 264 secret-binding requirements. These review requirements are preserved; import success does not establish replay readiness.

The archive's hash was checked before and after each import and remained unchanged. Its capture, request/response contents, generated script and private diagnostic logs are not committed; only the aggregate comparison (`evidence/external-har-comparison.json`), summaries and the exact opt-in test harness are retained. The test sends no traffic to the recorded application. This exercises the default ZIP upload/review conversion, not every import option or later save/replay operation. The 30-second runtime capture-finalization deadline does not apply to uploaded HAR conversion; the existing 300 MiB expanded-upload bound is unchanged.

<!-- Original record: docs/qa/reviews/roadmap-har-inflight-2026-10-20/README.md; local evidence kept outside git -->
<a id="roadmap-har-inflight-2026-10-20"></a>

## Native HAR outstanding-request limit — 2026-10-20


This [BE-A / BE-3 slice](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#be-a--stable-authored-identity-and-qualified-document-evidence) closes an active native HAR context when its synchronous request callback observes more than 1,024 outstanding issued requests. Browser-queued requests count. The existing exact-owner close monitor performs closure, marks affected document observations censored and exposes `har_in_flight` as a resource failure. It does not add routing or change cache behavior.

This is overflow-triggered closure, not a scheduler or a guarantee that no callback arrives after the limit is crossed. The 512-entry request-identity inventory remains a separate bound. Completed sequential requests release their count; non-recording contexts retain their prior limits. Cancellation and overflow join the same native writer. A caller deadline cannot remove its working file or release its reservation before physical join; the existing shared 30-second finalization deadline is unchanged.

<a id="roadmap-har-inflight-2026-10-20--validation-and-review"></a>
### Validation and review

- Applied browser package: 387 top-level tests and 335 subtests passed, including actual Chromium/WebKit flood closure and document censoring. Environment-specific skips remain in the log.
- Applied race suite: 4 top-level tests passed for limits, counting, stale generations, close ownership and cancellation overlap.
- SDK lifecycle race suite: 9 top-level tests and 14 subtests passed. The identical controlled load-state regression fails with only the registration fix removed; five native navigation repetitions passed on each engine.
- Full Go suite: 168 packages, 17,654 top-level tests and 17,311 subtests passed. A test-only external overlay exercised the actual private HAR import in this same gate; all production code came from the installed checkout.
- The attached ZIP imported successfully: HTTP 200, 803 source requests, 476 actions, 327 filtered assets, 479 assertions, 37 extractors, 43 correlations, four warnings and zero DSL issues. Aggregate fields matched the preceding compatibility cohort.
- Type checking, lint, 34 Help units, embedded dashboard build, Help workflows in both auth profiles, generated browser inventory, documentation checks and evidence checksums passed. Exact workflow counts and source/controller hashes are retained in the evidence bundle (`SHA256SUMS`).

The private customer ZIP was exercised only through the real multipart import endpoint, without replaying traffic or saving converted content. Retained evidence contains aggregate counts and source hashes only. The matching import result does not claim byte-identical generated IDs or protected attestations.

Review corrected the native fixture's invalid action field and an existing synthetic-owner cleanup that left watchdogs alive beyond their tests. The original failed compile log is retained. An installed native navigation failure also led to a deterministic SDK regression for a load event lost between its state snapshot and listener registration. A narrow registration lock fixes that gap without extending timeouts or emitting synthetic events; explicit CI/Make dependency tests and vendor provenance cover the patch. The original failure and passing diagnostic repeats remain distinct evidence. Final applied-source and staged review found no remaining actionable findings after the fixture corrections and the independently reproduced SDK load-state registration fix.

An earlier full-suite attempt hit the unchanged 1 GiB archive free-space reserve and was stopped. Its failed/incomplete log is retained. After all session builds stopped, cleanup removed 11,004,120,362 bytes of obsolete cached API objects and abandoned scratch directories; the complete retry used the same source and limits.

Local macOS Chromium/WebKit tests establish this overflow/ownership behavior only. Linux/Windows native crash qualification, reference-host performance, actual-device qualification and real sanitized TruClient migration fixtures remain pending. No external qualification gate is promoted.

<!-- Original record: docs/qa/reviews/roadmap-monotonic-browser-timing-2026-10-19/README.md; local evidence kept outside git -->
<a id="roadmap-monotonic-browser-timing-2026-10-19"></a>

## Roadmap implementation: monotonic browser timing


Date: 2026-10-19. Scope: a bounded [TEL-A](../plans/ROADMAP_IMPLEMENTATION_SPEC_2026-10-28.md#tel-a--durable-windows-and-measured-end-to-end-age) browser measurement prerequisite, following [durable controller cursors](TELEMETRY_AND_PROTOCOL_SLICES_2026-10.md#roadmap-durable-cursors-2026-10-19). Base: `07c25d409`.

<a id="roadmap-monotonic-browser-timing-2026-10-19--contract"></a>
### Contract

The dashboard records local receipt identity and monotonic time while handling each live metrics update. These records live in a weak map, outside wire JSON and history. The advisory `browser_render` report measures local receipt to a post-commit animation-frame/timer callback with `performance.now()`. Wall time labels the observation only. Invalid, regressed or overflowing measurements remain unavailable; they cannot become measured zero.

Receipt identity belongs to one run. Distinct receipts sharing a wall timestamp remain distinct; latency-only updates and repeated renders cannot resample a receipt. Disabled sessions discard pending samples, and a late refusal from an obsolete reporting session cannot stop a newer one. One bounded page-lifetime visibility listener records hidden transitions starting at the first receipt, including before React enables the reporter. Opaque visibility epochs preserve event ordering even at zero or reduced clock precision.

The existing authenticated report, per-viewer/tab quotas, opt-in behavior, 64-sample queue, API shape and advisory stage remain compatible. Superseded callbacks, hidden receipts, queue eviction and failed reports remain best-effort gaps. This change supplies neither a complete eligible-publication denominator nor cross-host calibration, publication correlation, a measured end-to-end distribution or proof of visible presentation. The callback is a paint opportunity. Existing archives are preserved; their timing method is not retroactively qualified.

<a id="roadmap-monotonic-browser-timing-2026-10-19--validation"></a>
### Validation

- Dashboard type checking and embedded build passed; lint passed. The existing bundle-size warning remains advisory.
- Five focused timing, stream and help unit files passed: **57 tests** (log (`evidence/render-unit-final.log`)).
- Real live-run telemetry and searchable/reloadable narrow help workflows passed in **both auth profiles, two tests each** (dev-admin (`evidence/render-dev-admin-reviewed.log`), local-auth (`evidence/render-local-auth-reviewed.log`)). The browser fixture steps the callback wall clock forward by two minutes while preserving bounded monotonic intervals and the advisory stage. Unit fixtures cover both forward and backward steps.
- Final telemetry table and narrow help screenshots were visually inspected in both profiles (dev-admin table (`evidence/dev-admin-stages.png`), local-auth table (`evidence/local-auth-stages.png`), dev-admin help (`evidence/dev-admin-help.png`), local-auth help (`evidence/local-auth-help.png`)). Missing generator measurements remain unavailable; browser timing remains explicitly advisory.
- The generated telemetry acceptance inventory passed and was refreshed. Documentation checks passed for **253 active Markdown files**; the in-app guide ownership/fragment scan reported no findings. `git diff --check` passed.
- Validation used Node 22.23.3 and Go 1.26.9 on macOS arm64, disposable local controllers/databases, and the maintained Playwright runner. Latest `origin/main` was integrated before builds. Browser sessions and controllers were closed by the runner. Dedicated build caches remain in use for subsequent implementation slices.
- This slice changes dashboard timing and documentation, not Go API production behavior. The preceding [durable-cursor validation](TELEMETRY_AND_PROTOCOL_SLICES_2026-10.md#roadmap-durable-cursors-2026-10-19) retains the full Go-suite evidence; it is not relabelled as a new run here. Local browser checks do not qualify cross-host end-to-end latency.

Raw final logs and screenshots are retained under `evidence/`, with SHA-256 hashes (`evidence/SHA256SUMS`).

<a id="roadmap-monotonic-browser-timing-2026-10-19--review"></a>
### Review

Review found and fixed visibility transitions lost before reporter activation, non-finite rounded elapsed time that JSON could otherwise turn into null, repeated local receipts, stale-session refusals, the acceptance inventory's reference to a replaced visibility test, and an older guide paragraph that still claimed first paint. Regression assertions retain explicit unavailable and advisory behavior. A second review of final source, tests, generated inventory, help wording and retained evidence found no further actionable issues. Generated embedded bundles are validation outputs and are excluded from the scoped source commit.

<a id="roadmap-monotonic-browser-timing-2026-10-19--remaining-program-dependencies"></a>
### Remaining program dependencies

The user confirmed that separate reference hosts, Linux/Windows native runners, sanitized TruClient exports and a disposable multi-host recovery environment are unavailable. Their declared qualification gates remain pending. Implementation and local regression work continue; this local browser slice does not certify those environments or close TEL-A.

<!-- Original record: docs/qa/reviews/roadmap-replay-har-qualification-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-replay-har-qualification-2026-10-26"></a>

## QA Review — Replay HAR Crash, Shared-Filesystem, and Cloud-Emulator Qualification (`BE-B5` Local)


- **Date:** 2026-10-27
- **Package / Slice:** `BE-B5` local (`pkg/api`)
- **Status:** Qualified (local crash, two-controller shared filesystem, and containerized S3/Azure Blob/GCS object-store emulators; live production cloud credentials remain external)

<a id="roadmap-replay-har-qualification-2026-10-26--scope"></a>
### Scope

1. **Symlink-Resolved Local Replay Archive Root (`pkg/api/replay_artifact_backend.go`):**
   - `openReplayArtifactBackend` now resolves `backend.LocalRoot` via `resolveArchiveDirectoryRoot(backend.LocalRoot, false)` (`filepath.EvalSymlinks` after `filepath.Abs`) before calling `openArchiveDirectoryNoSymlink(backend.LocalRoot)`.
   - Fixes macOS `t.TempDir()` (`/var/folders/...` -> `/private/var/folders/...`) `ELOOP` failures across all 29 existing `TestReplayArtifact*` tests while keeping `O_NOFOLLOW` protection on the resolved directory root and all descendant components.

2. **Abandoned Native HAR Working-Directory Reclamation on Replay Cleanup (`pkg/api/replay_artifact_cleanup.go`):**
   - `(*Server).startReplayArtifactCleanup` now invokes `browser.ReclaimRuntimeHARWorking(ctx, strings.TrimSpace(os.Getenv("APEXLOAD_BROWSER_HAR_DIR")), 128)` alongside `s.replayArtifacts.Sweep(ctx)` on startup and each periodic sweep tick.
   - Ensures local `.apexload-har-work/<generation>/working.har` working directories left behind by a `SIGKILL`ed controller/replay process are reclaimed once both the owner process and writer tree have exited.

3. **Custom Endpoint & Emulator Support for S3, Azure Blob, and GCS Replay Backends (`pkg/api/archive_backend.go`):**
   - **S3 (`s3blob`):** Appends `s3ForcePathStyle=true` when `b.Endpoint` is configured, `disable_https=true` for `http://` endpoints, and defaults `region=us-east-1` when `b.Region` is empty on custom endpoints.
   - **Azure Blob (`azureblob`):** Opens buckets via a per-call `azureblob.URLOpener` (rather than `blob.DefaultURLMux()`'s process-global `lazyOpener` `sync.Once`) and formats `domain`, `protocol`, `localemu=true`, and `storage_account` query parameters when `b.Endpoint` is set.
   - **GCS (`gcsblob`):** Opens custom-endpoint / `STORAGE_EMULATOR_HOST` buckets via `gcsblob.OpenBucket` with `option.WithoutAuthentication()`, `option.WithEndpoint(...)`, `option.WithHTTPClient(...)`, and `cloudstorage.WithJSONReads()` (avoiding `gcsblob.lazyCredsOpener`'s process-global `sync.Once` and XML path-encoding incompatibilities in `fake-gcs-server`).

4. **Qualification Tests (`pkg/api/replay_artifact_qualification_test.go`):**
   - `TestReplayArtifactSIGKILLMidReplayAndRestartReclamation`: Spawns an isolated child controller helper process (`runReplaySIGKILLChild`) across three stages (`pre-stage`, `unjoined-stage`, `post-publish`), sends `SIGKILL` directly to the child PID, starts a replacement controller against the shared SQLite store and archive/HAR directories, and verifies:
     - `pre-stage` returns HTTP `404 Not Found` (before any artifact receipt is reserved) while its retained session lease blocks session-ID reuse (`TryAcquireReplaySessionLease`) and reclaims the dead child's `.apexload-har-work/<gen>/working.har` directory.
     - `unjoined-stage` returns HTTP `503 Service Unavailable` with no synthetic HAR fallback, preserves the unjoined receipt reservation against session-ID reuse across `Sweep`, and reclaims the dead child's `.apexload-har-work/<gen>/working.har` directory.
     - `post-publish` serves HTTP `200 OK` with `X-APEXLoad-Replay-Durability: durable`, `X-APEXLoad-HAR-Fidelity: native`, and byte-identical native HAR bytes, and reclaims the dead child's `.apexload-har-work` directory.
   - `TestReplayArtifactTwoControllersSharedFilesystemHandoff`: Verifies two concurrent `Server` instances sharing `PostgreSQL`/`SQLite` + a shared POSIX directory with `source.v1` volume identity immediately serve status and native HAR on Controller 2 without restart, while a third controller pointed at a foreign filesystem root fails closed with `503` and does not corrupt or delete the shared artifact during `Sweep`.
   - `TestReplayArtifactCloudEmulatorsCrashAndRestart`: Qualifies `IfNotExist` duplicate-generation rejection, Controller 1 publication -> shutdown/crash -> Controller 2 recovery with a fresh `APEXLOAD_ARCHIVE_STAGING_DIR`, unjoined `503` fail-closed retention, and project-delete `Sweep` cleanup across `s3-minio`, `azure-blob-azurite`, and `gcs-fake-gcs-server` (both deterministic in-process protocol servers and real Docker containers `bitnamilegacy/minio:2025.7.23-debian-12-r5`, `mcr.microsoft.com/azure-storage/azurite:latest`, and `fsouza/fake-gcs-server:latest`).

<a id="roadmap-replay-har-qualification-2026-10-26--verification-evidence"></a>
### Verification Evidence

- Existing replay artifact suite on macOS:
  - `go test -count=1 ./pkg/api ./pkg/storage ./pkg/recordbridge -run 'TestReplayArtifact'` -> 29/29 PASS.
- Qualification suite with `-race`:
  - `go test -race -count=1 ./pkg/api -run 'TestReplayArtifact(SIGKILLMidReplayAndRestartReclamation|TwoControllersSharedFilesystemHandoff|CloudEmulatorsCrashAndRestart)'` -> PASS (`21.756s`).
- Real Docker container qualification (`bitnamilegacy/minio:2025.7.23-debian-12-r5`, `mcr.microsoft.com/azure-storage/azurite:latest`, `fsouza/fake-gcs-server:latest`):
  - `APEXLOAD_TEST_MINIO_ENDPOINT=... APEXLOAD_TEST_AZURITE_ENDPOINT=... APEXLOAD_TEST_FAKE_GCS_ENDPOINT=... go test -v -count=1 ./pkg/api -run '^TestReplayArtifactCloudEmulatorsCrashAndRestart$'` -> PASS (`s3-minio` `0.25s`, `azure-blob-azurite` `0.24s`, `gcs-fake-gcs-server` `0.16s`).
- Mutation verification (`go test -overlay`):
  - Reverting `resolveArchiveDirectoryRoot(backend.LocalRoot, false)` to `filepath.Abs(backend.LocalRoot)` in `pkg/api/replay_artifact_backend.go` fails `TestReplayArtifactTwoControllersSharedFilesystemHandoff` (`symlink alias sourceSHA256 ... != canonical sourceSHA256 ...`).
  - Removing `browser.ReclaimRuntimeHARWorking` from `(*Server).startReplayArtifactCleanup` in `pkg/api/replay_artifact_cleanup.go` fails `TestReplayArtifactSIGKILLMidReplayAndRestartReclamation/pre-stage` (`working HAR file ... was not reclaimed after child SIGKILL + replacement server startup`).

<!-- Original record: docs/qa/reviews/roadmap-truclient-replay-primitives-2026-10-26/README.md; local evidence kept outside git -->
<a id="roadmap-truclient-replay-primitives-2026-10-26"></a>

## Roadmap Review — Local TruClient Replay Primitives (`BE-B7a`, 2026-10-26)


<a id="roadmap-truclient-replay-primitives-2026-10-26--scope"></a>
### Scope

This slice implements the local browser DSL, runtime, and TruClient importer primitives required to convert and replay supported TruClient 2.0 Web and Mobile Web step shapes without false semantic review gates:

1. **Append typing (`browser_fill` `params.clear: false`):**
   - Added `params.clear` (`FieldKindBool`) to `browser_fill` in `pkg/dsl/catalog_browser.go` (validated in `browserFillChecks`, regenerated in `docs/dsl.schema.json` and `docs/DSL_ACTION_REFERENCE.md`).
   - Updated `fillAction` in `pkg/protocol/browser/frame_actions.go` so `input_mode: "type"` with `clear: false` skips `locator.Fill("")`, focuses the element and places the selection caret at the end of existing text, and dispatches per-character keyboard events via `locator.PressSequentially`.
   - Updated TruClient `Type` step conversion in `pkg/convert/trueclient/steps.go` to map `Clear=false`/`0` to `action.Params["clear"] = false` without raising `truclient_append_typing_requires_review`.
2. **Tab close lifecycle (`browser_close_window`):**
   - Added `browser_close_window` (`target`, `params.index`, `sync`, `capture`) to `pkg/dsl/catalog_browser.go`, `pkg/dsl/browser_target.go`, and `pkg/dsl/catalog_test.go`.
   - Implemented `dispatchCloseWindow`, `tryCloseWindow`, `selectWindowToClose`, and `finalizePageDocuments` in `pkg/protocol/browser/frame_actions.go` (wired in `pkg/protocol/browser/action_dispatch.go` and `browserPageNavigation` in `pkg/protocol/browser/browser.go`), finalizing document evidence on the closing page's frames before `targetPage.Close()` so `page.OnClose` never marks the session's document clock incomplete, refusing to close the last remaining open page, and switching scope to an open remaining page.
   - Updated `switchWindowForAction` and `Close Tab` conversion in `pkg/convert/trueclient/steps.go` to emit `browser_close_window` without raising `truclient_tab_lifecycle_requires_review` or duplicating window-switch actions.
3. **Pre-armed download coalescing (`Click` + `Wait for File Download` → `browser_download`):**
   - Added `canCoalesceTruClientDownload` and `coalesceTruClientDownloadStep` in `pkg/convert/trueclient/converter.go` (plus `convertWaitForDownloadStep` and `applyTruClientDownloadArguments` in `pkg/convert/trueclient/steps.go`) so a selector-backed `Click` followed by `Wait for File Download` coalesces into a single pre-armed `browser_download` action while preserving `source_download_step_id` in `applyXMLAnchoredTransactions` (`pkg/convert/trueclient/source_contract.go`). Standalone selectorless `Wait for File Download` steps remain `source_review`.
4. **Network-idle completion (`Step network completed` + `NetworkEndThreshold`):**
   - Added `"Step network completed"` to `applyTruClientStepContract` and `applyTruClientCompletionContract` in `pkg/convert/trueclient/source_contract.go` (`action.Sync = &dsl.BrowserSyncPolicy{Type: "network_idle"}`).
   - Mapped `[TruClient] NetworkEndThreshold` in `pkg/convert/enterprisearchive/truclient_runtime.go` to `actions[].sync.stable_ms`.
   - Added `waitForNetworkQuiet` in `pkg/protocol/browser/wait_actions.go` backed by `lastNetworkActivity` / `inFlight` tracking in `pkg/protocol/browser/resource_evidence.go` so `network_idle` waits for in-flight post-interaction requests and the configured quiet window.

Sanitized real customer/VuGen `truclient2_web` and `truclient_mobile_web` export archives and external physical mobile device qualification remain tracked under `BE-B7b`.

<a id="roadmap-truclient-replay-primitives-2026-10-26--verification"></a>
### Verification

- **Unit & Importer Contract Tests:**
  - `go test -v -run "TestTruClientReplayPrimitivesAppendTypingCloseTabDownloadAndNetworkIdle" ./pkg/convert/trueclient` — PASS
  - `go test -v -run "TestPlannedTruClientBrowserDraftFinalizesSettingsParametersAndProvenance" ./pkg/convert/enterprisearchive` — PASS
  - `go test ./pkg/dsl/... ./pkg/convert/trueclient/... ./pkg/convert/enterprisearchive/...` — PASS
- **Live Chromium & WebKit Integration Replay:**
  - `go test -v -run "TestBrowserFillClearValidationAndCloseWindowUnit|TestTruClientReplayPrimitivesLiveChromiumAndWebKit" ./pkg/protocol/browser` — PASS on both `chromium` (`1.32s`) and `webkit` (`1.38s`), verifying append typing (`settled:prefix-suffix`), `Step network completed` quiet-window synchronization, popup tab activation and `Close Tab` with complete document evidence (`!docEvidence.Incomplete`), and pre-armed `browser_download` file save + v4 `download` outcome satisfaction.
- **Mutation Verification (`go test -overlay`):**
  - Mutation 1 (`be-b7a-overlay-clear.json` ignoring `params.clear: false` in `pkg/protocol/browser/frame_actions.go`): `TestTruClientReplayPrimitivesLiveChromiumAndWebKit` fails as expected (`settled:-suffix` instead of `settled:prefix-suffix` on both Chromium and WebKit).
  - Mutation 2 (`be-b7a-overlay-coalesce.json` disabling `canCoalesceTruClientDownload` in `pkg/convert/trueclient/converter.go`): `TestTruClientReplayPrimitivesAppendTypingCloseTabDownloadAndNetworkIdle` fails as expected (`ReviewOnlySteps: 1`).

<!-- Original record: docs/qa/reviews/windows-native-har-2026-10-16/README.md; local evidence kept outside git -->
<a id="windows-native-har-2026-10-16"></a>

## Native HAR capture on Windows


Status: Wave 1 (platform gate, PR #228) is on main. Waves 2–4 (spike and
`pkg/internal/winfs`, Windows ownership, portable tests, CI job, guide) are PR
#229. **Qualified on Windows:** the full HAR step passed three runs in a row on
the final code (see [Repeat runs](#windows-native-har-2026-10-16--repeat-runs)).

The project has no Windows machine. Every Windows statement below comes from
the Windows leg of `.github/workflows/desktop.yml` on GitHub-hosted runners.
Each one cites the run that showed it.

<a id="windows-native-har-2026-10-16--wave-1-platform-gate-d7"></a>
### Wave 1: platform gate (D7)

Evidence: desktop run 38947072872, job 117128780404 (commit fcb12aeea,
`windows-2025`). In its "Native HAR platform gate" step, with
`APEXLOAD_BROWSER_TESTS=require` and real Chromium:

- `TestNativeHARSupportedMatchesPlatform` passed: `NativeHARSupported()` was
  false on Windows.
- `TestUnsupportedPlatformBrowserVUserRunsWithoutHAR` passed. A HAR-enabled
  browser VUser ran its navigation and reported
  `browser_har_unavailable_har_platform_unsupported`, with nothing in its HAR
  directory. The generator logged `reason=har_platform_unsupported
  platform=windows` once.
- `TestUnsupportedPlatformHARNamespaceCreatesNothing` and
  `TestUnsupportedPlatformHARCaptureRunsWithoutRecordingAndSaysSoOnce` passed.
- `TestBrowserReplayRecordHARRefusedWithoutNativeHAR` passed on both routes:
  a replay asked for `record_har: true` got 422 and started nothing.
- `TestNativeHARRecordsItsEvidenceGeneration` skipped, as intended.

The same job then failed in its "Native analytics contracts and controller
build" step. Main fails that step too (8ae712536, job 117119599571), so the
failure is pre-existing and is being fixed in PR #230.

<a id="windows-native-har-2026-10-16--wave-2-wp1-phase-0-spike"></a>
### Wave 2, WP1: Phase 0 spike

Evidence: desktop run 38950445622, job 117140185924 (commit 161f8bd5c). Runner
image `windows-2025-vs2026` 20261002.596, Windows Server 2025, Go 1.26.9. The
temporary test `pkg/protocol/browser/har_windows_spike_test.go` printed one
`SPIKE` line per observation. It and its workflow step were removed once these
findings were recorded.

<a id="windows-native-har-2026-10-16--environment"></a>
#### Environment

- The token is elevated. The runner account is the built-in Administrator
  (RID 500), which Windows renders as `LA` in SDDL, and its token default owner
  is that same account. Code must compare descriptors as Windows renders them,
  not as strings built from SIDs.
- `%TEMP%` (`C:\Users\…\AppData\Local\Temp`) is on a local fixed NTFS
  volume. It is owned by Administrators, with inherited full access for SYSTEM,
  Administrators and the user. A directory made there with `os.Mkdir` is not
  private. `RUNNER_TEMP` (`D:\a\_temp`) also grants Users read.
- Microsoft Defender's service runs, but real-time, on-access and behavior
  protection are **off** on hosted runners. Antivirus contention cannot be
  measured there (see Q7).

<a id="windows-native-har-2026-10-16--host-and-process-identity-d6"></a>
#### Host and process identity (D6)

- `HKLM\SOFTWARE\Microsoft\Cryptography\MachineGuid` is present.
- `BootId` under `…\Session Manager\Memory Management\PrefetchParameters` is a
  REG_DWORD (14 on that boot).
- `ContainerType` is absent, so the runner is not a container.
- The physical DNS host name is present.
- A live process (this one) gives `WaitForSingleObject(h, 0)` = WAIT_TIMEOUT
  (258) and a creation time from `GetProcessTimes`.
- An exited child whose handle is still held gives WAIT_OBJECT_0.
- Once every handle to the exited child is closed, `OpenProcess` fails with
  ERROR_INVALID_PARAMETER (87). An unused PID gives the same error.
- PID 4 (System) opens with `PROCESS_QUERY_LIMITED_INFORMATION`.

D6 holds as specified.

<a id="windows-native-har-2026-10-16--q1-file-identity"></a>
#### Q1: file identity

`os.Lstat` and `os.Stat` load the file ID lazily, at `os.SameFile`, from
whatever the path names then:

- A `FileInfo` taken before the file was renamed away and replaced compared
  equal to the replacement.
- It did not compare equal to the moved original.

`os.Root.Lstat` and `os.Root.Stat` load it at the call: the same scenario gave
the moved original. `os.Root.Stat(".")` matches the opened directory.

`FILE_ID_INFO` gives a 128-bit ID whose high 64 bits are zero on NTFS. Its low
64 bits equal the 64-bit file index from `GetFileInformationByHandle`. Sys() of
a root `FileInfo` is `*syscall.Win32FileAttributeData`, which carries no ID, so
identity must be read from a handle (D3).

<a id="windows-native-har-2026-10-16--q2-osrootlink"></a>
#### Q2: `os.Root.Link`

`os.Root.Link` works on Windows. It is create-only: linking over an existing
name fails with ERROR_ALREADY_EXISTS (183). `os.Link` works too. No separate
create-only publish primitive is needed.

<a id="windows-native-har-2026-10-16--q3-sharing"></a>
#### Q3: sharing

- Handles from `os.Root.Open` and `os.Root.OpenFile` share delete. A file held
  through them can be renamed and removed.
- `os.Open` handles do **not** share delete. Rename and remove of a file held
  through one fail with ERROR_SHARING_VIOLATION (32). HAR code must never
  hold namespace objects through `os.Open`.
- A directory held by `OpenRoot`, or by `OpenRoot` plus `Open(".")`, can be
  renamed and removed.
- A directory with an open handle to a **child** cannot be renamed: ERROR_ACCESS_DENIED (5).
- A handle-relative `FILE_RENAME_INFORMATION_EX` without replace works, and
  fails with STATUS_OBJECT_NAME_COLLISION onto an existing name.
- POSIX delete through a handle removes the name at once while a share-all
  reader stays open, and the name can be reused immediately.
- `os.Remove` of a file another share-all handle holds also removed the name at
  once on this build. Delete-pending names can still come from other programs'
  legacy deletes.

<a id="windows-native-har-2026-10-16--q4-directory-durability"></a>
#### Q4: directory durability

`File.Sync` on a directory handle (`os.Root.Open(".")` or `os.Open`) fails with
ERROR_ACCESS_DENIED. `FlushFileBuffers` on a handle to the same directory
succeeds when it is opened for `GENERIC_WRITE` or for
`FILE_ADD_FILE|FILE_ADD_SUBDIRECTORY`. It fails for `FILE_WRITE_ATTRIBUTES`
or `GENERIC_READ`.

The durability boundary for a directory's entries is therefore
`FlushFileBuffers` on a write-access reopen of the directory
(`winfs.SyncDir`).

<a id="windows-native-har-2026-10-16--d2-and-q5-private-creation-and-the-drivers-flush"></a>
#### D2 and Q5: private creation and the driver's flush

`NtCreateFile` relative to the parent's handle, with `FILE_CREATE` and the
descriptor `D:P(A;OICI;FA;;;SY)(A;OICI;FA;;;<user>)`, creates a protected
directory owned by the user. Creating it again fails with
STATUS_OBJECT_NAME_COLLISION.

Inheritance:

- A generation directory below it inherits `(A;OICIID;FA;;;SY)(A;OICIID;FA;;;user)`.
- `working.har` inherits `(A;ID;FA;;;SY)(A;ID;FA;;;user)`.

A real Chromium context recording to the pre-created `working.har` flushed
2,081 bytes into the same file ID, with one link and the inherited private
DACL. The generation held only `working.har`.

<a id="windows-native-har-2026-10-16--q6-zero-length-lock"></a>
#### Q6: zero-length lock

`LockFileEx` (exclusive, 1 byte at offset 0) on a zero-length file:

- It conflicts with a second handle in the same process: ERROR_LOCK_VIOLATION (33).
- It conflicts with another process.
- It is released by unlock and by closing the holding handle.
- The file stays zero length.

<a id="windows-native-har-2026-10-16--q7-transient-errors"></a>
#### Q7: transient errors

2,000 cycles of create, write, rename and remove of a generation-shaped
directory gave no errors. Real-time protection was off (see Environment), so
this measures the filesystem, not antivirus. The bounded retry on sharing,
lock and delete-pending errors stays.

<a id="windows-native-har-2026-10-16--adjustments-to-the-design"></a>
#### Adjustments to the design

- **D4:** confirmed and required. A generation directory cannot be renamed
  while any handle to a child is open, so the owner lease and the generation
  root are closed before the rename.
  - A scanner briefly holding a child would fail the rename with
    ERROR_ACCESS_DENIED, which Windows also returns for real ACL refusals. So
    the generation's tombstone rename retries ERROR_ACCESS_DENIED within the
    same `runtimeHARCloseWait` bound as sharing errors, and then fails closed.
  - Directory removal also retries ERROR_DIR_NOT_EMPTY, which a delete-pending
    child causes.
- **D5:**
  - No create-only link primitive: `os.Root.Link` already is one.
  - `SyncDir` is `FlushFileBuffers` on a write-access reopen.
  - `RemoveAt` and `RemoveIfSame` count a name that is already delete-pending
    as removed.
- **D3:** confirmed. `os.Lstat` cannot be used for the parent before/after
  checks, and root `FileInfo`s carry no ID. `winfs.Lstat` stats through a
  zero-access handle, and record identity is read from a handle to the name.

<a id="windows-native-har-2026-10-16--wave-2-wp2-pkginternalwinfs"></a>
### Wave 2, WP2: `pkg/internal/winfs`

The first Windows run of the winfs unit tests (same job) failed three tests.
All three were test errors:

- The private-DACL test compared SDDL text against a string built from the SID,
  which Windows prints as `LA`.
- The rename and remove tests held their reader through `os.Open`, which does
  not share delete (Q3).

The tests now compare the descriptors as Windows renders them and hold readers
as scans do (share all). `pkg/importartifact`, switched to the shared volume
policy and write-through move, passed in that job.

<a id="windows-native-har-2026-10-16--wave-3-windows-ownership"></a>
### Wave 3: Windows ownership

Native HAR ownership on Windows, as the
[guide](../../BROWSER_EVIDENCE_ARCHITECTURE.md#windows) describes it:

- **D1–D4** (464c57f00):
  - handle-based privacy checks (owner, DACL, no reparse point, one link);
  - private creation through `NtCreateFile` with the descriptor;
  - identities read through handles, with the 128-bit file ID and owner SID in
    the ledger;
  - `:` refused in name components;
  - one retirement order on every platform, with the Windows retry bounds.
- **D6** (322e6270b): `MachineGuid` with the physical DNS host name, `BootId`,
  process creation times; crash reclamation disabled inside containers.
- **Replay store** (80c35d54e): the managed replay HAR root is created private;
  a root that is not private is not adopted, and the store uses a fresh private
  directory instead.
- **Platform flip** (872476f60): `NativeHARSupported()` is true on Windows.

<a id="windows-native-har-2026-10-16--wave-4-windows-tests-and-ci"></a>
### Wave 4: Windows tests and CI

The desktop job's "Native HAR ownership (Windows)" step (60ffc6c5c) installs
Chromium and WebKit and runs `pkg/internal/winfs`, the browser package's `HAR`
tests and `pkg/api`'s `HAR|BrowserReplay` tests with
`APEXLOAD_BROWSER_TESTS=require`. The runs, in order:

1. **Run 38966884185, job 117195439328** (8c7fc92c0). The browser suite: 98
   passed, 7 failed, and every failure was in a test (fixed in ad2108e01):
   - Ledger fixtures renamed generations with their files still open, which
     Windows refuses (D4).
   - A fixture wrote its artifact straight into the HAR directory, so it
     inherited `%TEMP%`'s ACL.
   - An SDDL comparison used a string built from the SID, not the descriptor
     Windows renders.
   - A namespace-replacement test assumed the rename succeeds. Windows refuses
     it while the lock is held.
   - A read-only directory test was defeated by the runner token's enabled
     backup privileges (below).
   - A waterfall test expected WebKit request timing that Windows does not
     expose.
2. **Run 38970933046, job 117208848160** (a7d9f2922). The browser HAR suite
   passed. Three `pkg/api` archive tests failed with
   `sync <dir>: Access is denied.`: two archive directory-sync helpers did not
   tolerate Windows' refusal of `File.Sync` on a directory. All six `pkg/api`
   directory syncs now flush through `winfs.SyncDir` (760731aef).
3. **CodeQL** reported a new path-injection alert. It was the same `os.Lstat`
   of `browser_har_dir` that `main` reports as alerts #309, #310 and #316,
   moved into a helper. The calls are back where `main` has them, and Windows
   pins identity separately (cc23bf40c).
4. **Run 38980828584, job 117241050682** (cc23bf40c; runner image
   `windows-2025-vs2026` 20261007.264.1). **The whole step passed**:
   `winfs`, the browser package (27.7 s) and `pkg/api` (22.1 s), 283 HAR and
   replay tests. Three subtests skipped:
   - WebKit's waterfall comparison, by design (below).
   - Two subtests of `TestHARArtifactOwnershipRetiresEntireRecordedCompletedBatch`,
     which built Unix identities. That test now builds platform identities
     (b10a9e6a4).

The job then failed in its later native analytics step, which `main` fails
too; that is PR #230's subject.

<a id="windows-native-har-2026-10-16--done-criteria-in-run-38980828584"></a>
#### Done criteria in run 38980828584

| Criterion | Tests | Result |
| --- | --- | --- |
| A HAR-enabled browser VUser publishes a scrubbed HAR into the run archive | `TestHARCaptureAlwaysPersistsScrubbedArtifact` (real Chromium), `TestLocalRunArchiveRetainsBrowserHARs`, `TestDistributedArchiveTransfersHARsWithReceipts` | passed |
| A browser replay with `record_har` serves its native HAR from a private store | `TestBrowserReplayActualNativeHAR` (real Chromium), `TestReplayHARRootIsPrivateOnWindows` | passed |
| A killed generator's generation is reclaimed after restart | `TestHAROwnershipReclaimsKilledGenerations` (write, scrub, publish), `TestHAROwnershipReclaimsKilledNativeRecording` (Chromium and WebKit drivers killed with `TerminateProcess`) | passed |
| `.apexload-har-work`'s DACL allows only the user and SYSTEM | `TestWindowsHARWorkingDirectoryDACLIsPrivate` | passed |
| Unsafe ACLs, UNC paths, delete-pending names and identity across rename | `TestWindowsHARRefusesUnsafeWorkingDirectoryACLs` (inherited Users, Everyone, NULL DACL, foreign owner), `TestWindowsHARRefusesUNCDirectory`, `TestWindowsHARRemoveCountsDeletePendingAsRemoved`, `TestHARPersistentIdentityAtIsStableAcrossRename` | passed |
| 200-cycle create, publish and retire stress | `TestWindowsHARCreatePublishRetireStress` (12.5 s) | passed |
| The full HAR ownership suite passes on Windows, Linux and macOS | Windows: this step. Linux: CI's race lane (the browser package under `-race`) and the three `pkg/api` shards. macOS: local runs of the browser package's HAR tests and `pkg/api`'s HAR, archive and replay tests | Windows passed except the two since-ported subtests, which pass in repeat run 1. macOS passed. Linux passed on b10a9e6a4 (CI run 38986149051): race job 117260064411 ran 19,755 tests with no failures, including the browser package (2m 4s; its only skips are two env-gated benchmarks), and all three `pkg/api` shards passed |

<a id="windows-native-har-2026-10-16--what-the-runs-showed-about-windows"></a>
#### What the runs showed about Windows

- **Backup privileges.** The runner token has `SeBackupPrivilege` and
  `SeRestorePrivilege` enabled. `os.Root` opens ask for backup intent, so this
  process's own opens get past a DACL. The read-only directory test disables
  both privileges for its duration, and the run logged that both had been
  enabled.
- **Rename of a locked directory.** Windows refuses to rename
  `.apexload-har-work` while a handle to its `namespace.lock` is open
  (`Access is denied.`). So the namespace-replacement attack the test simulates
  cannot happen there, and the test now checks the refusal.
- **WebKit timing.** Playwright's Windows WebKit exposes no request start, so
  its request-wait phase reports `not_exposed`. Its native HAR still records
  the server's timing: wait 301.6 ms and receive 202.6 ms against the
  fixture's 300 ms and 200 ms.
- **Token owner.** The runner's elevated token makes the user the owner of
  what it creates (`TestWindowsHARAcceptsElevatedTokenOwner`).

<a id="windows-native-har-2026-10-16--not-shown-on-windows"></a>
#### Not shown on Windows

- Behavior under on-access antivirus scanning: hosted runners run Defender
  with real-time protection off.
- Windows containers: the runner is not one. `TestWindowsHARHostIdentity`
  shows only that the container check reads it as a non-container; the
  disabled reclamation inside a container has not run.
- ReFS volumes and SMB shares: only the UNC refusal runs against a real
  path. `TestValidateVolume` checks the drive-type and filesystem policy with
  synthetic values.

<a id="windows-native-har-2026-10-16--repeat-runs"></a>
#### Repeat runs

The Windows HAR step must pass three runs in a row on the final code.
Each run is recorded here as it completes. Runs 1 and 2 passed on the HAR
code as of b10a9e6a4. `main` then changed one HAR test,
`TestHAROwnershipCrossProcessCleaner` (#234: it now publishes its ready
marker by rename and allows a slow child 8 s). It also added the Windows
browser memory sampler to the browser package (#233). So three runs on the
merged code (01a0b60ed and later) were needed; they are below.

| # | Run, job | Commit | HAR step |
| --- | --- | --- | --- |
| 1 | 38986149129, 117257968307 | b10a9e6a4 | passed: `winfs`, the browser package (40.1 s), `pkg/api` (39.0 s); 345 tests; the completed-batch subtests ran and passed; one skip, WebKit's waterfall comparison |
| 2 | 38994746174, 117284707561 | 8e6dbdea1 (the record and a main merge; no Go changes) | passed: `winfs`, the browser package (39.7 s), `pkg/api` (35.3 s); 345 tests; one skip, WebKit's waterfall comparison |

On the merged code (01a0b60ed and later):

| # | Run, job | Commit | HAR step |
| --- | --- | --- | --- |
| 1 | 39001525209, 117305285602 | e9d6b3ddc (#235's merge; no Go changes since 01a0b60ed) | passed: `winfs`, the browser package (45.7 s), `pkg/api` (43.9 s), and the memory step's browser tests; 349 tests; one skip, WebKit's waterfall comparison |
| 2 | 39005524599, 117317092061 | c6397573b (the record and #236's merge; no Go changes) | passed: `winfs`, the browser package (35.2 s), `pkg/api` (37.9 s), and the memory step's browser tests; 349 tests; one skip, WebKit's waterfall comparison |
| 3 | 39005524599 attempt 2, 117330597493 | c6397573b (a re-run of run 2's job) | passed: `winfs`, the browser package (46.5 s), `pkg/api` (39.3 s), and the memory step's browser tests; 349 tests; one skip, WebKit's waterfall comparison |

Later `main` merges bumped Go modules the HAR code does not use (`x/mod`, the
AWS SDK, `modernc.org/cc`; #237) and dashboard packages. `x/sys` and the
pinned Playwright driver are unchanged, so they leave the code under test as
qualified.

The last `main` merge before landing brought in #230, #238, #239 and #240,
none of which touches a HAR file. The repository's Actions budget was used up,
so CI could not run on that merge. It was checked locally on macOS: vet and
lint for Windows, Linux and macOS, and the browser package's HAR tests, the
`pkg/api` HAR, replay and archive tests and `pkg/importartifact`, all passing.

Each of these jobs then failed in its later native analytics step
(`TestAnalysisCleanupRetriesMetadataReadFailure` and similar), which `main`
fails too and PR #230 fixes. That PR's Windows job has since passed in full.
