# Archive approval handoff — 2026-10-19

AUTH-C slice from the [remaining implementation spec](../../../ROADMAP_IMPLEMENTATION_SPEC.md#auth-c--lifecycle-edges-and-property-level-parity).

The legacy/plain direct-editor archive handoff now distinguishes a durable
Java approval session from a library save. Its receipt includes session identity
and destination. The review marks only the matching lane queued, prevents a
second save of the original lane, and preserves pending sibling drafts and their
shared protected source session. A later project, folder, options or review
generation rejects the reply. Edits made while queueing stay in the editor.
The shell acknowledges only the exact pending internal history return (origin
view/index and target hash/index); genuine navigation still guards unsaved
siblings. Batch validation messages no longer hide the session receipt, and the
accessible status counts queued approval sessions separately from library saves.

Protected current imports retain save-before-edit. Combined Java imports retain
their existing direct approval route. No library saved baseline, runtime approval,
credential authority, server receipt version, or replay permission is invented.

## Verification

Typecheck, lint, the embedded build and all **7,980 unit tests** passed (including the added protocol-gate regression).
The full dev-admin run passed 318 tests and skipped six profile/platform cases;
its sole failure was the old exact collection status text. After that assertion
was updated, the collection workflow passed its targeted recheck, for 319
applicable dev-admin cases verified. The full local-auth run passed
**258 tests** with one skip, including the corrected collection test.
The full cycle therefore retained exit status 1 from the historical assertion;
no production change followed it, and every applicable final case is verified.

The targeted lifecycle API checks, generated matrix freshness, and the attached
HAR multipart HTTP import passed. The original ZIP hash is unchanged: 803 source
requests, 476 converted actions, zero DSL issues, and 264 credential-binding
requirements. Structural conversion/safety fields match the retained main
baseline. The attestation-bearing script hash is not used as an equality claim.
No private capture or converted payload is committed. All 260 active Markdown
files pass the documentation checker; both browser profiles exercise Help.

[Validation summary](evidence/validation-summary.json),
[review rounds](evidence/review.txt), [commands](evidence/commands.txt),
[source and asset hashes](evidence/source-and-asset-sha256.json), and
[HAR comparison](evidence/external-har-comparison.json) retain exact scope.
Historical failed runs and the corrected checks are retained as compressed logs.
The [before-fix history prompt](evidence/history-return-1.png) and
[its diagnostic context](evidence/history-return-1-context.txt.gz) record the
retained-review routing finding. The
[collection assertion diagnostic](evidence/status-assertion-1-context.txt.gz)
records the test-only status update.

The browser fixture explicitly models a legacy plain response by removing the
attestation/expiry from a synthetic archive response. Conversion, creation and
reopening of approval sessions use real local HTTP. It does not qualify a bypass
of protected imports. Existing protected-import workflows run in the full cycle.

## Review findings addressed

- Normalized the projectless callback scope and reused the existing folder ref.
- Fenced the initial handoff as well as replies against changed destinations.
- Fixed a batch validation notice hiding the new queued-session receipt.
- Added a separate accessible queued-session count.
- Fixed the shell treating return to a retained review as navigation away.
  The real browser checks both the internal return and a genuine attempted exit,
  saves only the pending sibling, then reloads the durable Java approval session.
- Corrected new test selectors to match the observed full-document editor and
  scoped the success assertion away from the hidden retained review. The test
  hygiene branch and exact status expectations were updated without weakening
  their assertions.

## Cleanup

[Cleanup verification](evidence/cleanup.json): session Go build/test caches and
disposable Vite caches were cleared (approximately 2.8 GiB reclaimed). Docker
builder prune completed with 0 B reclaimed. Test controllers and Playwright
browsers exited; dependencies, embedded assets and source evidence are retained.
Unrelated primary-checkout files retain their starting hashes.

## Remaining scope

Scenario prerequisite handoff, durable intent receipts, contextual validation
revisions and the complete lifecycle property matrix remain open. No adapter is
retired in this slice. External qualification prerequisites remain unavailable.
