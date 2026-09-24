# JMeter source-plan and atomic batch review

## Findings fixed

1. Protected batch child capabilities could be consumed by older single-script
   create/mutation paths, bypassing whole-batch publication. Only the dedicated
   JMeter batch save can now claim those children. Ordinary create rejects batch
   drafts even when their child token is omitted. Legacy single-script repair
   also explicitly rejects a batch candidate.
2. The generator checked source/project compatibility only when a script already
   declared JMeter compatibility. Every manifested script now checks its project,
   and a compatibility-bearing manifest cannot admit an ordinary script.
3. A rejected assignment that omitted its manifest did not immediately abort an
   existing staged source plan. Failed staging now releases that plan's complete
   reservation regardless of the failed request's manifest presence, while
   retaining generation, supervision-token and committed-run fences.
4. The new draft guard would also constrain copies of successfully saved library
   scripts if their active batch marker survived persistence. Atomic save now
   converts the marker to `jmeter_batch_origin_v1` provenance. Saved assets retain
   source identity without depending on an expiring draft capability. Interrupted-save
   recovery normalizes the requested draft to the same provenance representation
   before comparing it with persisted assets.

## Regression coverage

Tests cover standalone child claims and tokenless partial creation, successful
whole-batch claims/save, persistent provenance, SQLite-backed repeated save
receipts and interrupted-save identity recovery, mismatched project/profile
identities, and reservation release after manifest omission.
The protected browser workflow also checks standalone creation is rejected before
repairing, saving and reopening the complete import.

The subsequent code review found no further actionable issues in the revised
candidate publication, save, staging, ownership and cleanup paths. Final verification:

- Focused batch tests pass, including SQLite receipt replay and recovery identity.
- API batch race tests pass (146 seconds); full generator/engine/protected-store
  race suites pass.
- Kafka/Event Hubs real-adapter build-tag tests pass; these are not authorized
  live-service conformance evidence.
- The protected local-auth browser journey passes, including the partial-save
  rejection, repairs, save/reopen and launch blocker.
- The full dashboard cycle passes: type checking, lint, 4,476 unit tests, fresh
  build, dev-admin browser workflows (137 passed, four skipped) and local-auth
  workflows (57 passed, one skipped).
- `go test -p 1 -timeout 30m ./...` passes on the final validation snapshot
  (API package: 522 seconds), including generated-artifact freshness checks.
  The snapshot isolates tests from concurrent dashboard rebuilds in the shared
  checkout; all nine reviewed source/test files were verified identical.
  Initial snapshot-only Git-history failures were resolved before this final run.

Strict client parity and authorized live-service conformance remain outside these
fixes.
