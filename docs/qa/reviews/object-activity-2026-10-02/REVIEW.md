# Object activity and archive review — 2026-10-02

The object activity and archive implementation was reviewed, corrected, and
reviewed again. The final source review found no additional actionable issues.
Verification results are recorded below.

## Findings corrected

- A delayed successful modification or archive event could clear the deleted
  marker. Only successful creation and deletion now establish object existence.
- Committed queued and accepted changes were missing from attribution summaries.
  Summaries now include committed asynchronous outcomes and normalize legacy
  project snapshots.
- Moving between objects or projects could retain the previous history page.
  History pagination now resets with its scope.
- Restoration could replay missed schedules when no scheduler observed the
  archive interval, including controller downtime. A durable restoration boundary
  now skips overdue scenario and execution-plan occurrences and advances to future
  scheduling. Reusing an object ID does not inherit another incarnation's boundary.
- Run starts could create duplicate history across request and background paths,
  or collide when an ID was reused. Canonical start identities now include the run
  incarnation and distinguish queue admission from execution admission.
- Parent-object history omitted schedule changes, monitor bindings, object
  repository bindings, and runtime settings. These changes now record the parent
  object and a safe section name. Unsaved import previews do not produce committed
  object history.
- PostgreSQL timestamp text changes with the session time zone. Comparing text
  could hide archive state and bypass edit or execution blocking. PostgreSQL now
  compares incarnation timestamps as instants; SQLite retains exact comparisons.
  A real-database regression reproduced the defect before the fix and verifies
  reads, lists, idempotence, admission, restoration, and schedule boundaries across
  three session time zones after the fix.

Regression tests cover each correction. The final review also checked disclosure
boundaries, project and resource permissions, archive concurrency, migration
compatibility, and continued operation of active runs.

## Verification

- Focused API and SQLite activity/archive regressions: passed.
- PostgreSQL 16 activity/archive regressions, including time-zone transitions:
  passed; disposable database stopped after verification.
- Dashboard component regressions: passed.
- Full `go test ./... -timeout=20m`: passed with 23,217 test cases passing,
  17 skipped cases, and 117 packages passing (eight packages have no test files).
- Full dashboard `npm run test:cycle`: passed. Typecheck, lint, 4,697 unit tests,
  production build, 179 dev-admin workflows, and 100 local-auth workflows passed.
  Five dev-admin cases and one local-auth case were skipped for their configured
  auth profile or unavailable private fixture.
- Archive/restore, execution blocking, deleted history, audit warnings, and viewer
  permissions: passed in their applicable auth profiles.
- Rendered deleted-object Activity history: inspected in both auth profiles.
- Diff whitespace checks: passed.

The review/fix cycle is complete with no outstanding findings from the final
review or verification. Workflow test controllers and the disposable PostgreSQL
container were cleaned up.

## Integration with main before publication

The reviewed feature was integrated with main at `f03a09e6`. Existing migrations
through version 26 were preserved, and the activity and restoration migrations
were appended as versions 27 and 28. Main's newer run annotations now produce
parent-run modification history in both live requests and legacy audit backfill;
annotation deletion preserves the run's creator and existence history.

Verification of the integrated source passed:

- Full Go suite: 23,720 passing cases, 18 skipped cases, and 117 passing packages
  (six packages have no test files).
- Dashboard typecheck, lint, all 4,768 unit tests, and production build.
- Affected Playwright workflows: 16 passing dev-admin cases with three auth-profile
  skips, and 19 passing local-auth cases.
- PostgreSQL 16 archive/activity tests, including time-zone transitions.
- API permission contract matrix, OpenAPI, dashboard API types, and DSL schema
  freshness; annotation migration regressions and diff whitespace checks.

The integrated source review and verification have no outstanding findings.
Disposable browser controllers and the PostgreSQL container were cleaned up.
