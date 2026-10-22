# Browser baseline inventory and qualification — 2026-10-21

## Candidate inventory foundation

The optional storage interface returns bounded canonical run identities for one
project/application/scenario, excluding the current run and non-complete runs.
An indexed ID cursor avoids timestamp-text ordering: legacy SQLite rows may
retain offsets, so completion ranking must compare decoded instants in Go.
At most 128 identities are returned with an explicit overflow flag. A latest
selector must refuse a partial inventory, enforce the current-run start fence,
and validate archived browser compatibility before selecting anything.

Schema release 75 adds the matching inventory index without changing earlier
migration fingerprints. Independent review found no issues after replacing the
initial timestamp-order query. The full storage suite passed in 90.416 seconds;
the earlier run caught a missing schema-release entry, which was fixed before
this passing run. Tests cover scope, self exclusion, status, pagination,
overflow, cancellation and invalid limits. PostgreSQL execution was not tested.
The log is `/private/tmp/apex-browser-durable-backend/baseline-candidates-storage.log`.

This is an inventory foundation, not completed latest-compatible admission.
Automatic selection and positive browser clock/source proof acquisition remain
open. Existing source archives and unrelated work are preserved.
