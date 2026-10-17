# Full codebase review fixes

This change addresses F1–F11 from [the full codebase review](REVIEW.md). Security remains outside the review criteria. The broader refactoring backlog in that report remains a set of separate recommendations; this change extracts the seams needed to fix the reported behavior.

| Finding | Implementation and regression coverage |
| --- | --- |
| F1 — stale capacity work | Bind asynchronous capacity work to its launch attempt and execution lease term. Fence durable request/intent updates, preserve uncertain launch identities for recovery, and test late provider success/failure after takeover and same-process reacquisition. |
| F2 — warm provisioning launches ownerless burst capacity | Reject operator plans that require burst launches before creating a request or mutating provider capacity. Exercise the real ACI planner and retain run-owned direct/reverse capacity controls. |
| F3 — delayed recording save changes another draft | Extract request selection ownership, track session and request identity, and preserve newer edits while an earlier save completes. Test cross-session sequence collisions, same-request edits, and overlapping mutations. |
| F4 — stale recording detail selection | Give detail loads a selection generation and workspace scope. Test out-of-order successes, failures, deselection, and scope changes. |
| F5 — device save retry changes attestation | Retain the exact attested batch request and operation identity across uncertain responses. Test retry payload identity, project/folder changes, expired drafts, and agent reconnection. |
| F6 — cancellation during CLI poll skips abort | Share cancellation finalization across create, polling, authentication refresh, backoff, and timers. Test cancellation/deadlines, abort failure/disablement, retained evidence, and ordinary transport-error behavior. |
| F7 — JMS numeric object keys | Share the JMS/Kafka path walker and interpret numeric segments against their current container. Cover object keys, arrays, quoted punctuation, malformed paths, and established result formatting. |
| F8 — numeric extraction reformats or rounds identifiers | Decode JSONPath and database/network payload projections with exact JSON numbers, and format JMESPath float results using JSON's decimal/exponent rules. Cover ordinary seven-digit IDs in JSONPath, JMESPath, JMS, Kafka, real SQLite and network extraction; exact large integers; decimals/exponents; malformed documents; and unchanged JMESPath arithmetic. |
| F9 — failed or stale scenario history | Extract scoped loading/error/success state with retry and stale-response guards. Reopening refreshes successful histories; an open history offers Refresh runs. Cover authoritative empty results, new/updated runs, failed refreshes, pending-read deduplication, concurrent scenarios, and project round trips. |
| F10 — CLI reports unevaluated SLA as passing | Preserve the three-way SLA verdict in text and expose `sla_evaluated` in JSON. Execute the actual status command in subprocesses to verify output and existing exit policy. |
| F11 — explanation omits nested/lifecycle checks | Use the shared DSL walker to count checks across phases, branches, and embedded requests. Retain the root action count and test recommendations against the full authored check graph. |

## Review follow-ups

Independent area reviews were repeated after each set of corrections and the final source review found no remaining actionable issues. Follow-up corrections include quoted closing brackets in messaging paths; atomic pool-size publication; launch-time ownership; cancellation recovery and completion events; fenced intent and generator-membership writes; overlapping recording mutations; project round trips; and retrying a pending device save after re-pairing its agent.

A final scale-down review found that cancellation could leave surviving generators cordoned. Capacity cordons now form a temporary scheduling overlay scoped to the request, attempt, and ownership context. Attempt claims transfer that overlay, stale cleanup cannot release a successor's overlay, and expiry removes its scheduling effect. Operator cordon/drain state remains separate. Real-backend and SQLite tests cover cancellation, reacquisition, late completion, placement/inventory visibility, and operator-state preservation; the follow-up review was clean.

The browser regression review also corrected a heading selector to include its request-count badge and made delayed-response assertions wait for completion. The product selection behavior was correct in that failure capture. The corrected three-workflow regression spec passed under the dev-admin profile.

The user's independent evaluation exposed a remaining F8 formatting gap: JMESPath still rendered ordinary seven-digit IDs in exponent notation. New regressions reproduced that failure in the shared helper, real SQLite extraction, and network extraction before the fix. Float results now use JSON formatting, while the library's numeric representation and arithmetic remain unchanged. JMS and Kafka also have explicit seven-digit controls. The same follow-up added refresh behavior for successful scenario histories and corrected the refactoring inventory and delivery sequence in the review report.

## Validation

Main was fetched and integrated before rebuilds and complete API-suite runs. The implementation incorporated `4a1d26e19`, `d384ac787`, and finally `ec0577a7d` without conflicts. Incoming changes were reviewed for compatibility. Dashboard assets were generated before Go compilation that embeds them.

- `GOCACHE=/tmp/apexload-full-fixes-gocache GOMAXPROCS=2 go test -p 2 -count=1 -timeout=20m ./...` passed all 151 packages with tests on `d384ac787` plus the then-current fixes. The API package completed in 774.262 seconds.
- After integrating `ec0577a7d` and fixing the final temporary-cordon finding, the complete API package passed again in 731.472 seconds. `pkg/importartifact` and `cmd/controller` also passed; the new platform package `pkg/internal/winfs` has no runnable tests on macOS. The focused scale-down/ownership/cancellation regression run passed before that complete API run.
- `APEXLOAD_BROWSER_TESTS=require go test -p 1 -count=1 -timeout=10m ./pkg/protocol/browser`, with the same Go cache and concurrency settings, passed after the final main integration in 86.099 seconds. Browser-runtime availability was enforced.
- Dashboard type checks, lint, 708 unit-test files / 7,866 tests, and asset generation passed. The React type changes integrated from main received another type/lint pass and 184 tests across 13 affected component suites.
- The initial full dev-admin Playwright run had 303 passed, six skipped, and one failed. The failure was the recording test selector described above; after correcting it, all three regression workflows passed on rerun. The private archive fixture and auth-profile-specific cases account for the skips.
- The full local-auth Playwright run passed 243 workflows and skipped the one private archive fixture in 35.1 minutes. It included all three initial regression workflows; the later successful-history refresh extension is validated separately below.
- After the user's evaluation, complete `jsonextract`, JMS, Kafka, database, and network package tests passed again with `go test -p 2 -count=1 -timeout=5m`, covering the additional F8 regressions.
- The successful-history refresh follow-up passed 171 targeted dashboard tests, both TypeScript configurations, and changed-file lint. After another main synchronization and asset rebuild, all three regression workflows passed against the final code under dev-admin (3.6 seconds) and local-auth (23.6 seconds). Independent review of this follow-up and the numeric formatting correction found no remaining issues.
- The generated QA dashboard was refreshed from 88 to 89 tracked Playwright specs after staging the new regression file. The complete `docs/qa` package then passed in 5.892 seconds. Documentation and staged whitespace checks also passed.

Local validation uses macOS, SQLite, controlled provider/HTTP fixtures, and real browser runtimes. It does not claim live cloud provisioning or PostgreSQL qualification. Windows-specific HAR behavior is covered by the repository's Windows CI job rather than this local run. The broader refactoring recommendations in the original report are retained as separate work.

## Cleanup

After all builds, tests, browser checks, and follow-up fixes completed, `go clean -cache -testcache` cleared the dedicated session cache from 7,896,764 KiB to 8 KiB, reclaiming approximately 7.53 GiB. The worktree's Vite caches were removed and verified absent. `docker builder prune --all --force` completed successfully and reported 0 B reclaimed. No separate Buildx builder was used. Owned controller and Playwright processes exited, and temporary artifact-browser tabs were closed. Dependencies, deliverable assets, application data, and user-opened browser sessions were preserved.
