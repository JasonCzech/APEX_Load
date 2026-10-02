# Browser workflow first-failure triage

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

## Actual source review stalled while reading the response

The actual browser import also exposed a separate request-lifecycle problem: the controller reached its write deadline after returning HTTP 200 headers, and the renderer remained busy while waiting for the JSON body. This is distinct from Playwright's whole-test budget. The enterprise import and validation API wrappers now use the existing full-body JSON request helper with a 210-second deadline. It aborts pending reads, also rejects transports that ignore abort, clears its timer after completion or error, and tells the operator that the archive remains in the queue for another validation attempt. Other importer requests retain their existing behavior.

All 39 API-wrapper tests passed, including both enterprise endpoints with stalled headers, stalled bodies after HTTP 200, successful JSON completion, and HTTP 422 errors. The timeout tests check cancellation and cleared timers. Actual browser revalidation is recorded by the combined review; this unit evidence does not prove target replay.

## Final imported-project selector verification

The first final dev-admin collection check exposed a separate selector visibility defect: `Broken.USZ` was the first, automatically selected member, and the project picker was rendered only inside the successful-review panel. All three queue rows and their independent statuses were present, but `Review imported project` was unavailable from the failed member. The fix retains the picker beside successful review tabs and adds it to the failed/review-only fallback. A unit assertion now selects a failed member and uses the picker to return to a valid review. The original failing trace and screenshot were retained.

After that fix, the same final collection spec passed under both disposable profiles. Dev-admin passed in 26.3 seconds (test 16.8 seconds); local-auth passed in 29.3 seconds (test 21.8 seconds). Both used `--timeout=90000 --workers=1`. The workflow verifies the project selector, independent Java approval status, web runtime settings, one eligible script in the save receipt, preservation of the failed member after save, and Source Mapping navigation afterward.

Commands were run from `dashboard/`, using a separate private output root:

```sh
APEXLOAD_E2E_BASE_URL=http://127.0.0.1:4001 APEXLOAD_E2E_OUTPUT_ROOT=/private/tmp/apex-clm-review/final-collection-verification-fixed npx playwright test e2e/workflow-loadrunner-collection.spec.ts --timeout=90000 --workers=1 --reporter=list
APEXLOAD_E2E_BASE_URL=http://127.0.0.1:4002 APEXLOAD_E2E_PROFILE=local-auth APEXLOAD_E2E_OUTPUT_ROOT=/private/tmp/apex-clm-review/final-collection-verification-fixed npx playwright test e2e/workflow-loadrunner-collection.spec.ts --timeout=90000 --workers=1 --reporter=list
```

The sandbox initially denied Chromium's macOS MachPort registration before any application assertions; the approved unsandboxed browser runs supplied the results above. This launch restriction is separate from the selector defect. Dashboard type checking and scoped import/catalog/hooks lint passed, including a repeat lint check of `EnterpriseArchiveImport.tsx` after the final picker and Java naming edits. No dashboard build changed embedded assets during these checks, and the operator's CUA tab was left untouched.

## Saved review-session completion counts

The actual Java save confirmed a named authoring session, but the import counter still treated it as unsaved. The counter now records successful, current-generation review-session saves separately from library scripts and excludes them from drafts awaiting save. Revalidation invalidates that current-review receipt; duplicate idempotent saves count once, and a stale completion cannot update the active import.

The collection workflow now also saves its synthetic Java review session, checks the separate script/session counts and zero remaining drafts, and confirms the execution gate remains visible. Both profiles passed without retries: dev-admin 8.1 seconds (test 7.0 seconds) and local-auth 38.3 seconds (test 32.3 seconds). The same commands above were used with output root `/private/tmp/apex-clm-review/review-receipts-browser`. Focused component checks cover successful duplicate saves, revalidation, and stale completion.
