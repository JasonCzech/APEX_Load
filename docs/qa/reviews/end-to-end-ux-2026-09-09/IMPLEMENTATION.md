# UX review implementation — 9 September 2026

This delivery follows the 18 findings in AUDIT.md and adds project management controls in Settings. The original audit remains unchanged as the record of observed behavior.

| Item | Delivered change |
| --- | --- |
| UX-01 | Script picker flex children can shrink; the table stays inside the group, secondary columns yield at narrow widths, and search focus does not scroll the workspace sideways. |
| UX-02 | Reserved/queued views label planned load and unavailable samples, including the graph-wall selection. Historical execution summaries identify captured telemetry and display full timestamps. |
| UX-03 | Report summaries surface critical/warning findings, incomplete timing, insufficient observed load and sparse samples. Default exported risk commentary carries the same qualifications. Unknown rates use natural language. |
| UX-04 | Baseline comparisons display context differences, selected-population record counts and observed load next to the comparison. Recorded revisions are compared when available; absent revision evidence remains explicitly unverified. |
| UX-05 | Missing-archive pages retain run identity/status and open the execution overview snapshot. Results labels say unavailable rather than suggesting publication is pending. |
| UX-06 | Pagination counts script records; expanded details inherit their parent record's visibility. |
| UX-07 | Import folder controls use themed inputs and bounded spacing. Destination details are collapsed initially so the source form leads. Specialized HAR options wait for source selection. |
| UX-08 | Settings search recognizes theme, dark, word wrap, rows and motion, as well as additional administrative synonyms. |
| UX-09 | Monitoring administrative links respect permissions. Non-admin administrative deep links explain the restriction and provide a validated return destination. |
| UX-10 | Workflow choices and source headings are more compact. Narrow dashboard tables retain identity, status, date and actions; completed runs offer View results. |
| UX-11 | Library readiness uses “Static checks passed” rather than an unqualified “Ready”; scenario-specific validation remains separate. |
| UX-12 | Step changes focus and reveal the scenario step region. Checkmark guidance describes field validity, including defaults, rather than operator review. |
| UX-13 | Create opens new-script authoring. Scenario creation leads with workload shape and disables unavailable copying. Run configuration waits for a scenario. The final action says “Save and configure run.” |
| UX-14 | Run selector entries include reference/ID and creation time to disambiguate duplicate names. |
| UX-15 | New one-off and recurring schedules begin in the browser's time zone; existing saved schedule zones are retained. |
| UX-16 | User action cells are labelled as user actions, consistent with the separate service-account actions. |
| UX-17 | Sidebar entry is Help & documentation, with the operator guide as the default destination and explicit API-guide links preserved. |
| UX-18 | Repository browsing exposes Current workspace and All accessible projects modes and labels the active catalog scope. |
| Settings CRUD | Projects expose name editing, Save, Cancel and confirmed Delete. Domains, applications and releases use Edit terminology and themed controls; domain deletion uses the shared confirmation dialog. Permissions, project draft validation and deletion failure recovery are retained. |

## Review and validation

Manual browser checks covered project controls and deletion cancellation, Settings theme search, non-admin deep-link recovery, script creation, source-first OpenAPI import, scenario picker filtering/selection and wizard focus. Screenshots are in `verification/`.

The review/fix cycle additionally corrected picker flex overflow, missing-manifest report handling, graph-wall pre-start presentation, the app-shell help title, duplicate Cancel controls in shared confirmation dialogs, and stale workflow expectations. A server-backed browser regression exercises project edit focus, cancellation, saved rename persistence, deletion cancellation and confirmed deletion in a throwaway workspace. Unit regressions cover project deletion failures, evidence warnings, settings search and detail-row pagination.

Final validation:

- `cd dashboard && npm run test:cycle`: passed, exit 0. Type checking, lint, all 3,985 unit tests across 366 files, fresh embedded build, 77 dev-admin browser workflows and 9 local-auth browser workflows passed.
- `go test ./...`: passed for the report-risk implementation. The API package completed in 376.756 seconds.
- Final `go test ./pkg/api -run TestDefaultRiskCommentIncludesEvidenceLimitations -count=1`: passed against the current shared checkout.
- `git diff --check`: passed.

Concurrent Orchestration integration edits in the shared checkout were preserved; they are not part of this UX implementation. The full Go run preceded later concurrent archive-outcome edits; the final dashboard cycle and focused report-risk check included those edits.

## Scope and runtime notes

Sparse-sample warnings are cautionary presentation, not a new acceptance threshold. Baseline script revision equality remains unknown when the archive does not provide proof. Existing operator-written report commentary is preserved; updated default risk text applies when defaults are generated. The running development controller is an older build; backend report-default changes require a controller rebuild/restart. Browser workflows use a newly built controller with isolated data. No existing operator project or load test was deleted or started during manual verification.

## Follow-up code review and backlog completion

The follow-up [code review](CODE_REVIEW.md) fixed seven additional findings and completed the remaining acceptance gaps in UX-02, UX-04, UX-05, UX-08 and UX-16. All 18 audited work items are now marked Completed in `work-items.json`. Projects retain edit/save/cancel/delete controls; failed domain deletion now stays in its confirmation dialog with retry and duplicate-submission protection.

Final follow-up validation passed: 4,001 unit tests across 367 files, type checking, lint, a fresh embedded build, 78 dev-admin and 9 local-auth browser workflows, and the full Go suite using `go test -p 1 -timeout 20m ./...`. Browser search/focus checks and daylight-saving regressions are recorded in CODE_REVIEW.md. These results supersede the earlier validation counts above and include the concurrent archive-outcome integration changes present during validation. The running development controller was not restarted.
