# JMeter import fidelity verification — 2026-09-23

Source attachment SHA-256 remains `b92245ad1443767a6d9e20c87bc6988b04cff804368b5dcafca180e1be9b0cb0`. The attachment was not modified. Sanitized original and repaired fixtures were committed in `86b15ac1`.

The implementation and finding-to-test matrix are documented in [JMeter import fidelity](../../../JMETER_IMPORT_FIDELITY.md). The subsequent two-pass review and fixes are recorded in [Code review](CODE_REVIEW.md).

## Verified behavior

- The original fixture retains its HTTPS endpoint, 50 users, one-second ramp, and 200 iterations per user. Missing credential bindings, masked source values, and unresolved timezone semantics remain actionable blockers.
- UTC repair generates one timestamp with milliseconds. Source-timezone repair preserves the source wall-clock fields and records the literal-suffix decision. Unsupported date expressions remain blocked.
- HTTP extraction defaults overwrite an earlier trace ID on a miss; malformed expressions/responses and resource-limit errors fail instead of falling back.
- Local and generator-backed loopback tests each execute exactly 10,000 samples for 50 × 200 normal completion. A separate race-enabled test sends protobuf assignments and commits to two independent generator services over gRPC, with 25 users per shard, and verifies exactly 10,000 requests. Pause/resume, cancellation, failed samples, multiple groups, and older-generator rejection have focused regressions.
- Native CSV generation uses finalized full archives, excludes synthetic transaction duplicates, retains failed samples, uses first-byte latency, and rejects incomplete archives or failed writes. Distributed merge, redaction, concurrency, escaping, and extraction-retention limits are covered.
- The actual browser flow passes under both dev-admin and local-auth: upload, inspect source mappings and blockers, regenerate explicit repairs, save a two-user/three-iteration scenario, run six loopback samples, and download `traceId-results.csv` through the results UI. The file has one header and six sample rows.

## Checks

- Focused engine, HTTP, generator, and API archive race tests: passed.
- SQLite and PostgreSQL termination migration tests: passed against a disposable PostgreSQL 16 database; the container was removed afterward.
- Import contract registry and golden corpus checks: passed.
- Dashboard unit suite: 422 files / 4,465 tests passed.
- Focused JMeter browser workflows: two tests passed in each authentication profile.
- Full Go suite, including generated contract freshness: passed (`go test -timeout 30m -p 2 ./...`).
- The full dashboard cycle passed type checking, lint, all unit tests, and the embedded build. Its initial dev-admin profile ran all 141 cases: 125 passed, 4 fixture/profile-dependent cases skipped, and 12 failures. Nine stale navigation/label expectations were corrected; all 37 targeted dev-admin rechecks passed in an isolated checkout. The remaining three LoadRunner review cases also passed against the latest isolated build. Every initially failing case has a passing rerun.
- The full local-auth profile passed in an isolated checkout: 57 passed and one expected private-fixture case skipped. The isolated checkout avoided concurrent dashboard builds replacing shared embedded assets during controller compilation.

The initial `npm run test:cycle` invocation did not exit successfully. Its failed browser cases were corrected or rebuilt and all passed the isolated reruns above; local-auth was rerun in full. These are recovered validation results, not a claim that the original single invocation was green. No toolchain-related failure is counted as a pass or an accepted skip.

Go commands use `DEVELOPER_DIR=/Library/Developer/CommandLineTools`; dashboard commands use Node 22.22.0. The full Go run uses a 30-minute package timeout and two-package concurrency. Dashboard unit tests use four workers to avoid contention with other concurrent workspace validation. No required test is excluded.

The optional repository documentation checker also reports pre-existing audit-link and documentation-index issues outside this change; the new JMeter guide is indexed.
