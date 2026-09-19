# LoadRunner import review redesign — 2026-09-19

The Review tab now presents a task queue after archive validation. Completed setup
and original diagnostics remain available in disclosures. The existing APEX shell,
project authorization, protected candidate API, and save paths remain in use.

## Operator experience

- Current readiness leads the page. Saving a draft does not assert replay parity.
- Findings are grouped into missing values/correlations, protected bodies,
  credentials, transport/runtime, and review decisions. Repeated consumers share
  a queue entry; each source location and consumer remains individually selectable.
- Search and status filters support unresolved, blocking, review-only, and resolved
  issues. Resolution is recorded only when refreshed findings remove the issue.
- Repair choices follow the finding type. A missing value cannot select body
  restoration; an opaque construct does not get an invented repair.
- Response-extractor configuration selects an actual preceding request. Source
  navigation returns to the retained issue selection.
- A redacted preview binds the operation and exact consumer before applying it.
  Input changes discard the preview. Expired candidates remain inspectable but
  cannot be mutated. Late responses cannot update an abandoned project/candidate.
- Allocation preview lives in Data & parameters, with explicit duration/iteration
  modes and planned VUsers. It does not launch a run or modify allocation state.
- The queue has bounded scrolling. Narrow layouts put the selected issue before
  the queue. Forms retain visible labels, keyboard focus, and textual statuses.

## Review corrections

Self-review corrected traversal of parallel branches, stale original validation
messages overriding current protected findings, late-response scope handling,
and a failure message that incorrectly guaranteed an interrupted apply had not
changed server state. Existing credential preparation and atomic batch-save tests
were retained and updated for the new labels and collapsed setup.

## Acceptance and limits

The local private `acc_wls_reg_1.zip` acceptance test checks 492 primary requests,
one `c_transaction1` queue entry with multiple consumers, searchable `tstamp`,
contextual repair choices, source evidence, and tablet overflow. The observed
`tstamp` group contains 64 affected locations; `c_transaction1` contains two.
The archive and its payloads are not committed. Local acceptance is opt-in with
`APEXLOAD_LR_ACCEPTANCE_ARCHIVE`; encrypted import storage must be configured for
repair controls. Normal CI uses sanitized archives.

No source correlations were invented, commented producers reactivated, saved
scripts rewritten, or Salesforce target traffic executed by this UI work.

## Validation

- Dashboard type checking, lint, and fresh embedded build passed.
- The full unit run passed 400 files / 4,307 tests. After the remaining focused
  additions and review fixes, all 136 affected unit tests passed.
- Final dedicated browser run: all nine tests passed, including the private
  archive, all seven LoadRunner review/save/repair workflows, and the separate
  transaction-label persistence regression.
- The authenticated profile passed all seven LoadRunner workflows.
- `npm run test:cycle` completed both profiles and remains unsuccessful due to
  broader failures. Local-auth finished with 38 passed, one failed, and one
  opt-in private-archive test skipped; its failure is the previously documented
  `workflow-local-auth.spec.ts` workspace journey.
- `git diff --check` passed. This redesign changed no Go implementation.

The first full dev-admin browser run had 110 passing, ten failing, and four
skipped tests. One failure was the old transaction-label test expecting the
removed preflight wording; its updated final run passed. Eight failures match
the workspace/run-control failures already documented in
[the import repair validation note](../../LOADRUNNER_IMPORT_REPAIR.md).
`workflow-author-experience.spec.ts` also timed out in dev-admin but passed in
local-auth. These other screens were not changed by this redesign.

Screenshots are retained locally outside the repository; they include private
archive source locations. The existing Docker controller was not restarted and
its open import draft was not replaced. Rebuild/restart the application to use
the new embedded dashboard in that running instance.
