# Replay recovery scope and cleanup pagination — 2026-10-21

This BE-B storage slice supplies internal authorization metadata and bounded cleanup traversal for recovered native replay artifacts. It adds no schema release and does not enable recovered HTTP downloads by itself.

## Contracts

Scope lookup validates the retained receipt's session/project/generation and current project existence. It reports staging, ready or cleanup state, writer completion and expiry measured against database time. It grants no byte-read permission: callers must still enforce current project roles and obtain the exact project-scoped ready receipt. Deleted projects have no authorization scope; expired or unjoined receipts cannot become downloadable through this lookup. Invalid identity and corrupt receipts fail closed.

Cleanup can traverse a session-ID keyset in pages of at most 256 records, allowing a worker to advance past receipts from a different backend configuration. The cursor only schedules work; it never proves writer completion or releases quota. Callers reset it after reaching the end so failures remain retryable. Unjoined writers and unexpired ready artifacts are excluded. The existing expiry-ordered cleanup method retains its contract, and exact-generation physical cleanup plus finish acknowledgement remain required.

## Review

Review of the planned cleanup integration identified starvation if a worker repeatedly skipped foreign-backend receipts on the first page. Keyset traversal addresses that without adopting or deleting foreign objects. Follow-up review of SQL bounds, cursor ordering, transaction ownership, scope versus read authority, project deletion and retry behavior found no remaining findings, including low severity.

The PostgreSQL fixture is disposable and bound to loopback; it validates database behavior, not external HA, cloud-backend or native-browser qualification. Controller authorization, verified recovered reads and cleanup-worker wiring remain open.

## Validation

SQLite replay receipt/recovery checks pass with 18 test/subtest events. All five PostgreSQL replay-artifact integration tests pass against a disposable local PostgreSQL 16.15 fixture. Three combined race-detector repetitions pass with 69 events and no skips or failures.

Documentation checks pass for 321 active Markdown files; whitespace checks pass. Local logs and reviewed source hashes are retained under `/private/tmp/apex-browser-durable-backend/`: `recovery-storage-targeted.jsonl`, `recovery-postgres.jsonl`, `recovery-storage-race.jsonl`, `recovery-storage-docs.log`, and `recovery-storage-source-hashes.json`.

The broader API suite's earlier timeout remains an open serial final-source validation gate before merge. These targeted checks establish the new storage contracts, not full controller recovery or real browser qualification.
