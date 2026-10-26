# Debug summary transaction breakdown — 2026-10-26

Implemented in the existing `debug-completion-summary` isolated checkout on
`8195631417a338b7d84fe4104c703aacb45c6de5`. Fetched `origin/main` before builds;
there were no incoming commits. Existing HAR capture option changes remain in
this checkout and were preserved.

## Behavior

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

## Validation

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

## Cleanup

Both final browser workflows passed. No session controller/browser processes or
listeners on ports 18121/18122 remained. The isolated Go build/test cache was
cleaned from approximately 2.0 GiB to 8 KiB. Frontend `.vite`/`.vite-temp` caches
and the session dependency symlink were removed; installed dependencies were
preserved. Docker build-cache prune completed with 0 B reclaimed. Raw evidence
and generated deliverable assets are retained. This verification preceded the publication review.

The subsequent review/fix cycle and publication are recorded in
[the final publication review](debug-summary-har-final-review-2026-10-26.md).
