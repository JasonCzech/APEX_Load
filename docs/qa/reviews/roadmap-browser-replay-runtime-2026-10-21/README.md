# Bounded replay recovery runtime — 2026-10-21

This BE-B slice composes the verified replay backend, immutable receipts and terminal reconstruction into a bounded controller adapter. It remains passive until server admission, HTTP authorization and lifecycle cleanup wiring are enabled together.

## Contracts

The adapter captures its backend configuration and opens a scoped backend for each operation. Four read slots cover both status verification and staged downloads; a download owns its slot and private files until its idempotent close. Two publication slots limit simultaneous writers. Status verification has a 30-second deadline; complete materialization and publication have two-minute deadlines. Publication reserves at most 250 global / 100 project receipts and 8 GiB global / 2 GiB project bytes, including staging and cleanup-pending reservations, with at most two hours of terminal retention bounded by the owning lease.

Recovery requires an explicit project and an unexpired, joined, ready receipt. After verifying the report or complete native pair, it rechecks the same specification and expiry against current storage before returning data. Project loss, expiry, generation change and cleanup prevent delivery. Unknown or foreign-project identities are not found; known unavailable native evidence is not synthesized. Status-only reads do not assert current HAR availability.

Cleanup claims at most 32 joined receipts per 30-second sweep. A session-ID cursor advances past foreign source identities and failed deletions, then wraps for retries. The cursor is scheduling state, never writer proof. Only exact cleanup against the matching backend can release a reservation. Unknown files and unjoined writers remain retained; deleting only part of an authorized generation cannot acknowledge complete cleanup.

## Review

Review added an independent publication deadline and regression coverage for full foreign-backend pages, post-I/O receipt changes, admission/cancellation and cleanup retries. It also identified that generated store overlays must expose the new receipt methods, otherwise server tests could silently take a local-only path. The regenerated overlay adds only the eight replay-receipt methods. Regression checks exercise publication, recovery and cleanup through it. Final review found no remaining issues, including low severity.

This slice does not enable recovered HTTP routes or certify a cloud backend, native platform crash recovery or shared multi-host filesystem. No private import target is replayed.

## Validation

Initial targeted checks pass with 17 test/package events; expanded checks pass with 20. After regeneration and the final overlay regressions, three race-detector repetitions of runtime, backend, publisher, retained-report and reader-ownership tests pass with 422 events, no skips and no failures. Source hashes remained unchanged through that final run. Documentation checks pass for 324 active Markdown files; whitespace checks pass.

Raw local evidence under `/private/tmp/apex-browser-durable-backend/` includes `runtime-targeted.jsonl`, `runtime-final-targeted.jsonl`, `runtime-reviewed-race.jsonl`, `runtime-reviewed-source-hashes.json` and `runtime-docs.log`. The previous full Go and private HAR import gate is recorded in the [read-contract slice](../roadmap-browser-replay-read-contract-2026-10-21/README.md); a combined final-source gate remains required before merge. No dashboard assets or import behavior changed in this slice.
