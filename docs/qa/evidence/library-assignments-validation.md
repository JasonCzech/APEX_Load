# Library assignment validation — 2026-09-19

Scope: script folder moves and editable application/release assignments for
scripts and scenarios. Operator instructions: [Library assignments](../../LIBRARY_ASSIGNMENTS.md).

## Feature checks

- SQLite assignment tests pass: replacement/clearing, project and release
  ownership, archived retention, stale revisions, concurrent editors, deletion
  dependencies, migration from version 9, and restart persistence.
- PostgreSQL 16 assignment integration tests pass against a disposable real
  database, including concurrent revision checks and release-family resolution.
- Preservation tests pass for script content, scenario composition, historical
  runs, saved schedules, ordinary content edits, and unassigned clones.
- Assignment API and RBAC contract tests pass. OpenAPI, frontend contracts,
  endpoint catalogs, the QA contract matrix, and QA dashboard were regenerated.
- New assignment browser workflows pass in dev-admin and local-auth, including
  persistence, bulk failure recovery, assignment filtering, launch release
  selection and overrides. Local-auth also verifies viewer restrictions.

## Baseline failures and validation interruptions

`npm run test:cycle` was run. Existing unrelated failures remain in:

- `ux/Structure.test.tsx`: collapsed toolbar filter markup expectation.
- `TestExecutionCharts.interaction.test.tsx`: graph slider expects 31, receives 61.

The full `go test ./...` run was repeated after recovering disk space. Remaining
unrelated failures were:

- `TestDistributedExecutionBackendRunsRemoteMultiGroupEngine`: completion timing;
  passed in an isolated rerun.
- `TestOptInSuiteInventoryIsDocumented`: existing VUser footprint environment gates
  are missing from `OPTIN_TEST_SUITES.md`.
- `TestContainerBuildIsSelfContainedAndProfiled`: existing Dockerfile omits three
  user-guide copy declarations expected by the test.

Contract freshness and deletion-coverage failures caused by this feature were
fixed and their tests rerun successfully. A new CSS token failure was also fixed
and verified. An early Go run exhausted local disk space; its resulting storage
failures were discarded and the suite rerun after clearing regenerable build
caches and that run's temporary archives.

## Final results

- Dashboard cycle: 4,287 unit tests passed, with the two baseline failures above.
  Because those failures stop the cycle before its browser stage, the fresh
  embedded build and both complete browser profiles were run separately.
- Focused dashboard suite: 256 tests passed across seven files. After additional
  regression coverage, the final launch suite passed all 71 tests and the shared
  assignment dialog passed all eight tests. Final type checking and lint passed.
- Fresh embedded dashboard build passed.
- Complete dev-admin browser profile: 107 passed, 12 failed, three skipped.
  Eight failures match the previously observed baseline: command-center details,
  dev-admin landing, live metrics, both run lifecycle workflows, workspace
  hierarchy, authoring scope/history, and guided workspace setup.
- The other four dev-admin failures exposed a launch placement hydration race.
  Explicit operator choices are now preserved during workspace hydration and
  late assignment loading. With a fresh build, all affected launch workflows
  and the assignment workflows passed: seven passed, one local-auth-only test
  skipped. The entire dev-admin profile was not repeated after this fix.
- Complete local-auth browser profile: 36 passed, two failed. The workspace
  sign-in/build/import journey is a known baseline failure. The authoring
  workflow timed out but passed on a targeted rerun. Final local-auth assignment,
  viewer, authoring, and restored-draft/new-schedule workflows: five passed.
- SQLite storage, real PostgreSQL integration, API/RBAC, generated contract
  freshness, deletion coverage, and QA dashboard freshness checks passed.

The full suites are therefore not green; no remaining failure was reproduced
in the final focused assignment or launch checks.

## Local test cleanup

The disposable PostgreSQL container `apex-assignment-postgres` was stopped.
Docker refused to remove its registration because its metadata database became
read-only during the earlier disk-space interruption. Docker was not restarted
to avoid disrupting other containers. Test logs remain in `/tmp/apex-assignment-*`;
browser artifacts are in `dashboard/test-results/`.

## Uncommitted-change review follow-up

Reviewed all uncommitted application, storage, API, migration, contract, folder
move, documentation, and test changes. Fixed these launch findings:

- Late scenario metadata could overwrite an explicit application override before
  the operator chose a release. Explicit placement is now tracked independently
  from automatic suggestions.
- A workspace refresh could clear an explicitly selected schedule release.
  Schedule release choices now pin the launch placement too.
- Release creation could submit twice, apply a stale response after an
  application/project change, or reject without showing an error. It now fences
  duplicate requests and stale responses, displays errors, and clears them on
  retry.
- A release-list response started before creation could erase the new release
  from the selector. Successful creation supersedes that read and refreshes the
  catalog.
- Returning through a project without scenarios could leave the previous
  suggestion marked as already applied. Suggestion tracking now resets when
  the project changes.

Six regression tests were added; all 77 launch unit tests pass. Browser coverage
also exercises release creation failure/retry in an overridden application.
Focused storage, API/RBAC, schema, deletion, and generated-contract checks pass.

Review validation results:

- The dashboard cycle passed type checking and lint, then reported 4,296 passing
  unit tests and the same two baseline failures. The final project-return test
  was added afterward and passed in the 77-test launch suite.
- Final lint, type checking, embedded build, and `git diff --check` pass.
- Complete dev-admin profile: 111 passed, eight failed, three skipped. All eight
  failures are the baseline workflows listed above; all four earlier launch
  regressions now pass in the complete profile.
- Complete local-auth profile: 36 passed, two failed. These are the same
  authoring timeout and workspace journey failures recorded above.
- Focused browser verification with release-creation failure/retry coverage:
  dev-admin nine passed and one local-auth-only skip; local-auth four passed.
- Review logs: `/tmp/apex-review-*.log`. Browser artifacts:
  `dashboard/test-results/review/` and `dashboard/test-results/review-final/`.

No further actionable findings remain from this review. The full test suites
still have the separately tracked failures above.
