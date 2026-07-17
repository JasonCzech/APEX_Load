# Second-Opinion Review — GitHub Actions CI Production-Readiness Change Set

**Date:** 2026-07-16
**Scope:** Uncommitted working tree on `main` (post-PR #83) — CI run orchestration (`pkg/api/ci_*`), GitHub client/auth/checks, storage (`sqlite_github.go`), release/deploy workflows and scripts, `apexctl ci`, protocol request policy — plus codebase-wide sweeps (auth, secrets, injection, storage integrity, runtime hot paths). Bruno-importer work explicitly excluded per request.
**Method:** 16-lens multi-agent finder pass + 3-verifier adversarial panel per finding (majority vote) + completeness critic + gap round; 44 raw findings → 35 after dedup → 25 confirmed, 17 refuted. Because the working tree was being actively fixed in a parallel session during the review, every confirmed finding was then **re-checked against the final tree**: 20 remain live, 5 were fixed mid-day. Ground truth: `go build ./...` passes; `go test ./pkg/api/ ./pkg/storage/ ./cmd/apexctl/ ./pkg/protocol/... ./scripts/` all green on the final tree.

Line numbers below reflect the tree as of this report.

---

## Critical

### C1. Controller-crashing nil dereference in `syncCIGitHubCheck`
`pkg/api/ci_checks.go:680-682` reassigns `link, err = store.ClaimGitHubCheckConclusion(...)` and on error returns `link.CheckRunURL, err`. **Every** error path of `ClaimGitHubCheckConclusion` (`pkg/storage/sqlite_github.go:818+`) returns a nil `*GitHubCheckLink`, so any transient storage error (SQLITE_BUSY, PG connection blip, concurrently-deleted link) panics. The call is reached from the 30s CI reconciler and the terminal-run observer — both launched via `Server.launchBackground` (`pkg/api/server.go:1532-1535`), which has **no deferred recover**. A routine DB hiccup kills the entire controller process and every running load test. Verified 3/3.

## High

### H1. All-green JUnit artifact for an SLA-breached run
`cmd/apexctl/junit.go:115` emits the SLA test case only when `sla.Evaluated`; otherwise it writes a *skipped* case labeled "no SLA thresholds configured". `evaluateLocalSLA` (`cmd/apexctl/sla.go:191-192`) sets `Evaluated = configured == evaluated` independently of `Breached`, so one evidence-less threshold (e.g. `--max-avg-server-processing-ms` against a target with no Server-Timing headers) plus one breaching threshold yields `Evaluated=false, Breached=true`: apexctl exits 1, but the JUnit artifact reports `tests=2, failures=0` with a factually wrong skip message and no breach details. Azure DevOps / Jenkins pipelines gating on the JUnit artifact record the failed gate as green. Verified 3/3.

### H2. 30s reconciler races in-flight `handleCreateCIRun` and can kill the run it recovers
`pkg/api/ci_checks.go:282` calls `recoverQueuedCIRun` from the leader's 30s tick with no minimum-intent-age guard or in-flight fence; `ListPendingGitHubCheckIntents` picks up an intent as soon as `CreateTest` persists the row — while the creating request is still inside the GitHub `CreateCheckRun` round-trip. If the reconciler's `StartTest` wins, the request thread's `StartTest` fails and `failCIRunBeforeExecution` (`ci_recovery.go:133`) — which only skips *terminal* statuses — terminalizes the now-RUNNING run as FAILED. No crash/restart needed; the window widens with GitHub latency. (`ci_recovery.go`'s own doc comment says recovery is only safe when "no request on this replica can still be in the middle of materializing these rows".) Verified 3/3.

### H3. Queued-CI-run recovery silently no-ops in HA mode
`pkg/api/ci_recovery.go:15` gates on `isExecutionLeader()`, which is `IsAdmitted()` = `IsLeader() && !reconciling` — but both call sites run where that predicate is false by construction: `NewTestRunner` (before leadership) and `reloadAndReconcile` inside the leadership `onAcquire` callback (where `tick()` holds `reconciling=true`, `execution_leadership.go:352-364`). The codebase even has `holdsExecutionLeadership()` documented "recovery code must use this" (`runner.go:1235`) — this code doesn't. In HA, QUEUED CI runs (and runs without a pending check intent, which the outbox path at `ci_checks.go:282` doesn't cover) are stranded forever after boot/failover. Verified 3/3.

### H4. Recovery force-FAILs a concurrently-started run and orphans its engine
`failCIRunBeforeExecutionWithPromotion` (`pkg/api/ci_recovery.go:133-153`) guards only on `ciRunTerminal` — not "still QUEUED" — before claiming the fence and completing the run as FAILED; `completeTestRunUnderArchiveFenceWithPendingPromotion` (`testexecution.go:2619`) sets `test.cancel = nil` **without calling it**. A run that another actor legitimately started is recorded FAILED while its VUsers keep generating load. Verified 3/3.

### H5. Release signing/verification universally opts out of the transparency log
`release.yml:935/941/962/968` sign with `--tlog-upload=false`; every verify path (`release.yml:988/994`, `verify-image-evidence.sh:102/173`, `verify-registry-image-evidence.sh:24/78`, `verify-sbom-attestation.sh:17`) passes `--insecure-ignore-tlog=true`; the blob path is plain `openssl dgst`. No Rekor, no TSA, no countersignature anywhere: a stolen release key signs malicious artifacts that verify cleanly with zero public audit trail. If keeping tlog off is a deliberate GHES/air-gap decision, it needs a compensating control and explicit documentation (see M7). Verified 3/3.

## Medium

- **M1. First-error abort in recovery loop can wedge HA admission.** `ci_recovery.go:29-36` returns on the first failing run (skipping the rest); via `reloadAndReconcile` → `onAcquire` retry loop (`server.go:343-357`, 250ms) a deterministic per-run error keeps the replica leader-but-never-admitted indefinitely. `recoverQueuedCIRun` error paths have zero test coverage.
- **M2. Caller-supplied `release-public-key` silently overrides the pinned trust anchor.** `action.yml:114` documents it as a fallback; `install.sh:116-119` gives it precedence — a compromised workflow input substitutes an attacker key and malicious binaries verify.
- **M3. Unconfirmed remote aborts silently swallowed.** Both abort sites discard errors (`_ = abortControllerCIRun(...)`, `cmd/apexctl/ci.go:634` and `:659`); on abort failure apexctl exits while the controller keeps driving load at the target, with no warning.
- **M4. Idempotency key collides for two gates on one script in one workflow run.** `deriveCIIdempotencyKey` (`ci.go:1531-1543`) hashes source + script identity only (no vusers/duration/SLA/name); second gate gets 409. The CLI's `-idempotency-key` flag exists but the composite action exposes no input for it.
- **M5. Byte-identical metadata comparison breaks idempotent create retries.** `validateExistingCIRun` (`ci_runs.go:1670-1684`) compares full `ciRunMetadata` JSON including server-stamped volatile fields (`PolicyRevision`, `CheckInstallationID`); a retry after a policy revision bump 409s instead of returning the existing run.
- **M6. Release-tag commit binding is fail-open.** `verify-release.sh:68-75` runs the check only if the arg or `.release-tag-commit` marker is present, and the marker is excluded from the signed asset-set comparison (line 52) — omitting it is both sufficient and undetectable.
- **M7. GHES docs contradict the artifacts.** `docs/GHES_GITHUB_ACTIONS_COMPATIBILITY.md:51` documents verification that fails as written (signatures need `--insecure-ignore-tlog`, never mentioned anywhere in docs/) while declaring portable signatures authoritative; the tlog opt-out is undisclosed.
- **M8. Deleting a CI test strands its PR check forever.** `deleteTestGraphTx` (`pkg/storage/sqlite_tests.go:1136-1139`) unconditionally deletes `github_check_links`/`github_check_intents`; nothing concludes the in_progress remote Check first, and the sweeper works off the just-deleted link rows.

## Low

- **L1.** `ApiClient.AbortCIRun` (`cmd/apexctl/ci_client.go:258`) is dead code — zero callers after the switch to `AbortCIRunWithReason`.
- **L2.** `apexctl run` exits 1 on a partial-evidence SLA breach but prints no per-threshold details in text or JSON (`cmd/apexctl/main.go:133-139, 203-212`) — same `Evaluated=false + Breached=true` root cause as H1.
- **L3.** `respondFailedCIRunStart` (`ci_runs.go:415`) has no `errNotExecutionLeader` → 503 mapping (unlike `handleAbortCIRun:496-497`); a leader transition during create yields 500 or a terminally-FAILED run instead of a retryable signal.
- **L4.** Policy-rejection errors echo hosts resolved from `{{secret.*}}` targets verbatim to the workflow caller (`ci_runs.go:1388-1391`) — secret-derived endpoint appears in workflow logs.
- **L5.** `ciSLAEvidenceComplete`'s `MaxErrorCount` branch (`ci_runs.go:1877-1879`) counts as evaluated with zero traffic — `require_sla` passes a run that produced no transactions.
- **L6.** Data race: `recordLiveMetrics` reads `test.GoalConfig` without `tr.mu` (`execution_backend.go:401`; caller unlocks at `metrics.go:503`) while `UpdateGoalTargetTPS` mutates it under the lock (`testexecution.go:938-941`).

---

## Fixed during the review (by the parallel session — verified fixed on the final tree)

1. Check-creation lease expiry / fencing (2 findings) — now a `ciGitHubCheckCreationClaimGuard` heartbeat renews the lease and cancels the operation context on loss, with `renewOrCancel()` immediately before the remote Create.
2. Orphaned remote Check on lost claim — durable check-intent outbox now survives the failure; the 30s reconciler adopts it.
3. Fabricated `test_id` in CREATE_FAILED evidence — now `deterministicCITestID`, matching the server-side derivation.
4. `TestTargetPolicyBlocksServiceWorkerBypassPaths` — now behaviorally asserts `shouldBlockRequestInfo` and `ServiceWorkers=="block"`.

Also fixed mid-day (caught by running the suites at review start): `pkg/storage` test **compile error** (`github_script_links_test.go` `claimed` redeclaration), 4 failing new CI tests (SLA summary rows, recovery UUID validation, abort-reason terminal-evidence guard), and 13 pre-existing runtime-control test failures.

## Deliberate behavior change to be aware of

`localExecutionFinalState` (`pkg/api/execution_backend.go:165`) now finalizes any local run whose status is STOPPING at engine exit as **ABORTED** (previously COMPLETE when the engine returned cleanly). This initially broke 13 pre-existing tests; the parallel session kept the new semantic and updated the tests to accept ABORTED. Consequence: **operator-stopped runs are now recorded ABORTED, not COMPLETE** — run history, dashboards, capacity release, `test_aborted` events, and execution-plan governance all see the new status. If stopping a healthy soak early should still archive as COMPLETE, this needs revisiting.

## Refuted findings (for the record)

17 plausible-sounding findings were killed by the adversarial panel, including: pre-upgrade `kind IS NULL` check links excluded from reconciliation (a backfill exists), `ciCheckMu` held across GitHub I/O serializing all creation (guarded scope is narrower), OIDC audience regression, `--abort-on-cancel=false` ignored, `distributedArchiveSLA` race, publish-release commit binding not forwarded, wall-clock lease expiry, and several coverage-gap claims. Full list in the session's workflow journal.
