# gRPC diagnostic capture review

Scope: the first gRPC diagnostic capture slice and the preceding shared-analysis
and collector foundations. This is not acceptance of the complete monitoring,
preparation, protocol-analysis, or Parquet-import program.

## Findings fixed

- Bidirectional streams could deadlock while writing before reading. They now
  send and receive concurrently, cancel blocked I/O, and join the sender.
- Partial streams could lose their actual status or replace it with local
  cleanup cancellation. Synchronous terminal observations take precedence over
  delayed transport events; receive caps leave remote completion unavailable.
- A malformed client-stream request could invent an RPC `Unknown` status.
  Local decoding failures now preserve partial counts without fabricating status.
- Missing first responses diluted legacy timing averages. Optional measurements
  now use independent denominators; measured zero remains a sample.
- Mutable optional diagnostics needed deep copies through metrics, wire, archive,
  and snapshot projections. Validation bounds untrusted identities and values.
- The new diagnostics were initially added only to a legacy summary surface.
  The active run Diagnostics view now includes the panel as well.
- Generated API contracts treated diagnostics as opaque objects. Bounded model
  discovery now emits typed observations and distributions, with route-level
  contract regression coverage.
- Import-preview credential regexes performed quadratic scans of long fields.
  Boundary-aware scans preserve quoted, encoded (including encoded initial
  characters), numeric-prefix, and suffix
  credential masking. The existing large-input regression remains enabled.
- The new browser fixture could close its request context during a polling
  handler. Teardown now drains handlers before the context is disposed.

## Verification

- Real gRPC fixtures cover all four modes, early server rejection, deadlines,
  cancellation, receive caps, malformed later requests, and full-duplex payloads.
- Protobuf, local/distributed aggregation, optional-zero JSON, and real
  JSONL-to-Parquet conversion have regression coverage.
- Targeted protocol race tests passed three consecutive runs after the final
  status fix. Generated API contract tests and documentation checks passed.
- Dashboard typecheck, lint, and the embedded production build passed. The
  complete unit gate passed 435 files and 4,640 tests again after the final
  encoded-name redaction fix.
- Narrow-screen browser capture was inspected. The complete dev-admin browser
  cycle had 170 passes, four configured skips, and the polling-teardown failure
  described above. After the fix, the new workflow passed five consecutive runs
  in each auth profile. The complete local-auth cycle passed 91 tests with one
  configured private-fixture skip. The original cycle's failed exit is retained;
  the focused repetitions verify the subsequent fix.
- After the final UI rebuild, the gRPC diagnostic and protected credential-edit
  workflows both passed in dev-admin and local-auth (two checks per profile).
- The final `go test -p 1 -ldflags=-w ./...` passed, including the API package
  (478.5 seconds). An earlier overlapping Go/dashboard build failed because Vite
  replaced embedded assets during Go compilation; the successful run used stable
  assets. No Go source changed after that pass's gate.

That pass's final self-review found no additional actionable defects in this
slice. The subsequent review below records later findings and their fixes.

## Follow-up review and fixes

- The engine's history estimator omitted the new inline gRPC histogram arrays.
  A regression retained 92,012,544 bytes of diagnostic structs under the
  67,108,864-byte history budget. The estimator now includes their full struct
  size across every action-metrics projection. The test checks actual retained
  diagnostic allocations, eviction, and preservation of the newest evidence.
- A stream that exceeded the decoded JSON response byte limit did not set
  `receive_limit_reached`. The shared stream receiver now marks that boundary
  as partial, preserving payload counts and keeping final status unavailable.
  A real gRPC test transfers two 3 MiB protobuf payloads whose combined JSON
  exceeds the response cap; it reproduced the missing flag before the fix.
- Both new regressions pass after the fixes. Targeted engine and gRPC race
  tests passed three consecutive runs. Formatting and documentation checks pass.
- The first full Go attempt exhausted disk space during the API tests, causing
  database/fixture failures. Verified obsolete APEX cache objects and that
  attempt's unused temporary archive sandbox were reclaimed before retrying.
  The original failed log is retained at
  `/tmp/apexload-review2-go-test.log`.
- The same attempt reported an intermittent pre-existing generator test failure
  while inspecting a concurrently disappearing temporary Parquet file
  (`TestGeneratorStagesReusableScriptWithAssignedTestID`). Fifty focused repeats
  passed without changes to that test or its implementation. This failure is
  retained as an unresolved intermittent observation, not described as fixed.
- The full `go test -p 1 -ldflags=-w ./...` rerun passed, including the API
  package (434.3 seconds) and generator package. Its log is
  `/tmp/apexload-review2-go-test-retry.log`. No source was changed to suppress
  either failure from the first attempt.
- The follow-up review found no additional actionable defects in the changed
  code. The broader roadmap remains outside this slice's acceptance.

## Remaining roadmap scope

Run-bound durable monitor jobs, additional adapters, zero-traffic preparation,
DuckDB packaging and native imports, the 10 GB acceptance run, deep WebSocket
diagnostics, and qualified diagnostic comparisons/findings remain unfinished.
See [measurement semantics](../../../PROTOCOL_DIAGNOSTICS.md) and
[the roadmap](../../../ROADMAP.md).
