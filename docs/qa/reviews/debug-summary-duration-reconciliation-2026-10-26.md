# Debug summary elapsed-time reconciliation — 2026-10-26

Integrated `origin/main` through `24d65c718` before builds. Work stays in the
attached isolated checkout, preserving the shared checkout's source changes.

## Diagnosis and correction

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

## Review and validation

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

## Cleanup and publication

The final transaction workflow passed in both profiles after the pagination fix.
No test controllers, browser sessions, or listeners on ports 18121/18122 remained.
The isolated Go build/test cache was reduced from approximately 2.0 GiB to 8 KiB.
Disposable frontend caches and the temporary dependency symlink were removed,
preserving installed dependencies and generated deliverable assets. Docker build
cache pruning completed with 0 B reclaimed. Raw browser evidence remains untracked.
The reviewed correction follows the previously requested commit/push workflow.
