# Script and scenario application/release assignments

In the Scripts or Scenarios library, open an item's **More** menu and choose
**Change application/releases…**. Select an application and any existing releases
in the current project, then apply. Selecting **Unassigned (clear assignment)**
removes both the application and releases. An application can also be assigned
without any releases.

Select several local items to use the same action in the selection toolbar.
Bulk changes replace each item's complete assignment. The dialog previews the
replacement, skips unchanged items, and reports individual failures. Retry only
retries failed items. If another operator has changed an assignment, reload the
conflicting assignment and review it before applying again.

The **Assigned application/releases** column and assignment filters describe
editable library metadata. **Run releases** describes historical run associations.
Archived releases can remain assigned but cannot be newly assigned or selected
for a new launch. Repository browsing includes assigned items before their first
run and distinguishes assignments from run history.

Script changes require `manage_scripts`; scenario changes require `manage_tests`.
Assignments remain independent between a scenario and its scripts. They do not
change script definitions, scenario groups, existing runs, or saved schedules.
New and cloned assets begin unassigned.

For a new launch, the scenario's application is suggested. If it is unassigned,
explicitly assigned scripts suggest an application only when they agree. Assigned
releases are labeled in the release selector. Choose a release explicitly for
each fresh launch or new schedule, even when only one is assigned. You may choose
another application or release; changing application clears the pending release.
Restoring a saved draft preserves its chosen release.

The replacement API uses an assignment revision to reject stale writes. See the
[generated API specification](swagger.yaml) and
[endpoint catalog](api-endpoint-catalog.json) for the request and response contracts.
API and CLI execution clients retain their existing placement rules.

## Validation record

Point-in-time record (2026-09-19) for the folder-move and assignment feature; the full suites were not green at that time because of unrelated baseline failures.

- **Feature checks passed:** SQLite assignment tests (replacement/clearing, project and release ownership, archived retention, stale revisions, concurrent editors, deletion dependencies, migration from version 9, restart persistence); PostgreSQL 16 integration tests (concurrent revision checks, release-family resolution); preservation tests (script content, scenario composition, historical runs, saved schedules, unassigned clones); API/RBAC contract tests; regenerated OpenAPI, frontend contracts, endpoint catalogs, QA contract matrix and dashboard; new dev-admin and local-auth browser workflows (persistence, bulk failure recovery, filtering, launch release selection/overrides, viewer restrictions).
- **Baseline failures not caused by this feature:** `ux/Structure.test.tsx` toolbar filter markup; `TestExecutionCharts.interaction.test.tsx` slider; `TestDistributedExecutionBackendRunsRemoteMultiGroupEngine` timing (passes in isolation); `TestOptInSuiteInventoryIsDocumented`; `TestContainerBuildIsSelfContainedAndProfiled`; eight dev-admin browser workflows (command-center details, landing, live metrics, both run lifecycle workflows, workspace hierarchy, authoring scope/history, guided workspace setup) and two local-auth workflows (workspace sign-in/build/import journey; an authoring timeout that passed on rerun).
- **Fixed during validation:** a launch placement hydration race (explicit operator choices are now preserved during workspace hydration and late assignment loading), and a review follow-up covering late scenario metadata overwriting an explicit application override, workspace refresh clearing an explicit schedule release, duplicate/stale release-creation submissions, a release-list read erasing a newly created release, and stale suggestion tracking after a project change. Six regression tests were added.
- **Final results:** dashboard cycle 4,296 unit tests passing (plus the two baseline failures); 77 launch and eight shared-dialog unit tests pass; lint, type checking, embedded build and `git diff --check` pass; dev-admin profile 111 passed / eight baseline failures / three skipped; local-auth profile 36 passed / two baseline failures; SQLite, PostgreSQL, API/RBAC, generated-contract, deletion-coverage and QA-dashboard freshness checks pass.
