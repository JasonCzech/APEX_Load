# Resolve import issues — implementation

Implemented against the current workspace dashboard, served from Vite at http://127.0.0.1:4012/. The adjacent review.md and JPGs document the pre-change baseline.

## Changes

- Removed the redundant Original import diagnostics section from LoadRunner review; detailed warnings and blockers remain in the issue workspace and conversion reference. Other importers retain their diagnostics.
- Kept the vertical issue queue; added descriptive titles, replay impact, accurate script-wide/location scope, source-versus-replay comparisons, and secondary technical evidence.
- Preserved individual converter warnings instead of replacing them with a generic summary. Deduplicated repeated warnings and consolidated nonblocking runtime coverage into the runtime issue; independent blockers remain visible. Warning identities remain stable when their order changes.
- Embedded the existing runtime editor with source/current/consequence rows and a local change preview before server-validated apply. Existing settings state and API remain shared with the Runtime tab.
- Moved the same credential editor into a selected credential issue using a stable portal host; switching placement preserves pending input and does not create another editor instance.
- Kept governed body, producer, parameter, extractor, proxy-secret, and TLS operations. Added a field-path-only repair summary and persistent preview/apply action row. Protected values are not included in the new summary.
- Added session-only “Reviewed with limitation,” explicit next-issue progression, retained repair drafts, keyboard queue navigation, and Escape-to-queue keyboard support. Removed the unused Back to issue list button and moved runtime edit/preview/apply controls above the table, as requested. Review notes do not clear execution requirements or claim source parity.
- Exact source handoffs retain the mapping identity and include “Back to this issue.” Ambiguous source locations are not assigned an arbitrary file. Fixed StrictMode mount selection overwriting the requested mapping.
- Protected-draft recovery appears when relevant; unsupported source behavior remains explicit. No new assertion-repair backend was invented for Search=ALL semantics.

## Verification

- Final typecheck, targeted ESLint, embedded Vite build, and git diff whitespace checks passed.
- Final affected component suite: **133 tests passed with the standard timeout**, covering issue repair/navigation, runtime editing, credential placement, warning grouping, ScriptImportReview, and SecretBindingReview. EnterpriseArchiveImport: **85 tests passed**, including removal of the duplicate diagnostics and retention of warnings from older responses. SourceMappingReview's StrictMode handoff regression also passed.
- Current-source LoadRunner browser suite: **15 passed, 1 optional private-fixture test skipped**. The final edit-control positioning/removal flow was rerun and passed at desktop and narrow widths.
- The required `npm run test:cycle` was invoked. The broad unit sweep overlapped the subsequent requested UI adjustments and reported 4,461 passed / 4 failed. Two were superseded layout tests and two were large-input timeouts; all affected tests passed in the final standard-timeout rerun. The full unit sweep was not rerun after that final adjustment.
- Full dev-admin browser profile: **131 passed, 6 failed, 4 skipped**. All runnable LoadRunner tests passed. Remaining failures: workspace hierarchy; project-scope/history navigation; JMeter shared review evidence; JMeter messaging preview; Source Mapping resize/restore timeout; guided workspace setup. Other tasks were also updating this shared workspace during validation.
- Full local-auth browser profile: all 15 runnable LoadRunner tests passed; one optional private-fixture test skipped. Remaining wider workflow checks are still running.

Current screenshots: [warning](issue-warning.png), [runtime](issue-runtime.png), [narrow layout](issue-narrow.png).

Runtime previews are local comparisons; backend validation occurs on apply. Review decisions are session notes, not persisted approvals. Unsupported source behavior is not declared repaired by acknowledging it.
