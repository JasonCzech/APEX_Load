# Flake Log

**Policy.** The first unexplained intermittent failure is logged. After a second occurrence it must
be fixed or quarantined within 24 hours, with an owner, root-cause entry, and dated un-quarantine
commitment. A quarantined test that passes its un-quarantine date without a fix is escalated to a defect in
[DEFECTS.md](DEFECTS.md) — it is never silently left disabled.

**Detection.** The nightly flake-hunt job (M0 week 3) runs `go test ./... -count=5 -shuffle=on` and
the Playwright suite repeatedly. CI's main `go-tests` job already runs `-race -shuffle=on -count=1`.

**Largest structural cause in this codebase:** no fake clock. Scheduling, lease generations, pacing
token buckets, and percentile windowing all read real `time.Now()`. The M0 `Clock` harness targets
this directly. Suspect timing before suspecting the test.

| Test | First seen | Rate | Root cause | Owner | Quarantined | Un-quarantine by | State |
|---|---|---|---|---|---|---|---|
| `workflow-loadrunner-review` protected-session failures and `workflow-jmeter-fidelity` publication 409 (local-auth) | 2026-10-20 | Broad runs reproduced failures; focused isolated import run passed 20 tests with 1 skip | Three retained LoadRunner errors explicitly report exhausted protected import-session capacity. Disposable databases still used the shared default filesystem import roots. The runner now isolates both roots per file and regression-tests inherited-root overrides. The JMeter 409 has no recorded inner cause; its historical cause is not inferred from passing repetitions. [Integration evidence](reviews/roadmap-e2e-isolation-2026-10-20/README.md) retains original failures and final whole-profile accounting. | Roadmap integration | No | n/a | **ISOLATION FIXED — full profile qualification recorded in linked review** |
| `TestRuntimePauseCancellationStrippedTokenCannotMintNewAuthority/expired` (`pkg/engine`) | 2026-10-16 | 1 race-lane failure (PR #228, job 117195991968) | Test race, not a product defect: the case waited on the parent context's `Done()`, but Go closes a parent's done channel before it cancels its children, so `Admit` could still see the lease context (`WithCancelCause` of the parent) as live. The test now waits on the lease's own context. 300 race-detector runs pass. | CI green follow-up | No | n/a | **FIXED — waits on the lease context** |
| `TestHAROwnershipCrossProcessCleaner/unknown_crash` (`pkg/protocol/browser`) | 2026-10-16 | 1 race-lane failure (PR #223, job 117156382539) | Test race: the child wrote its `ready` marker with a plain `os.WriteFile` while the parent polled every 5 ms and stopped at the first successful read, so it could read an empty marker; the parent also gave the race-built child only 3 s to restart. The marker is now published by rename and the wait is 8 s, inside the child's 10 s context. | CI green follow-up | No | n/a | **FIXED — atomic marker, 8 s start wait** |
| `TestHAROwnershipGoexitNeverInventsCompletion/redactor`, `TestHARCaptureManualKeepsOnlyRequestedSessions`, `TestHARCaptureAlwaysPersistsScrubbedArtifact` (`pkg/protocol/browser`) | 2026-10-16 | 3 race-lane failures in a day on main and unrelated PRs | Suspected fixed bounds too tight under `-race` on hosted runners: the Goexit cases wait 150 ms for each receipt, and `Browser.Teardown` caps HAR close and ownership proof at `runtimeHARCloseWait` (2 s), failing with "physical ownership remains unproven / context deadline exceeded". Not changed yet: these files are being changed by the native-HAR-on-Windows work (#228, #229), and the 2 s bound is a production teardown limit. | Native HAR on Windows (#228/#229) | No | n/a | **OBSERVED — handed to the HAR ownership work** |
| `workflow-loadrunner-review` batch save, guided repair, run-logic, unique allocation and fresh runtime diagnostic (local-auth) | 2026-10-01 | Five failures in one broad cycle; all five unchanged cases passed on a fresh controller | Browser setup, login, workspace seeding and reload timed out during concurrent validation; no changed-feature assertions failed. Fresh-controller cases passed in 7–9 seconds each. | Priority pass validation | No | n/a | **OBSERVED — unchanged fresh-controller retries passed** |
| `BrunoImport` large-list tests, `SecretBindingReview` 10,000-name test, `recordingProxyCommands` setter rollback | 2026-10-01 | Failures under concurrent full Go/UI validation; isolated 152-test retry passed | Host contention exceeded existing timing budgets; unchanged assertions pass in the complete 4,663-test run with two workers and a 30 s fixture budget. No tests disabled. | Diagnostics/collector validation | No | n/a | **OBSERVED — stable full unit run passed** |
| `TestBrowserDocumentFinishedProviderPublishesAfterZeroGaugeAck` and the idle browser-document window waits (`pkg/engine`) | 2026-10-07 | 1 of 3 isolated runs; 4 of 100 under stress | Test race, not a product defect: each document window is published in exactly one snapshot, and the helper polled only the latest snapshot every 5 ms while the collector also published every 5 ms, so a later snapshot could replace the one holding the window before the poll saw it. The waits now scan the collector's retained snapshots; 300 stressed runs and 20 race-detector runs pass. | Feature completion Wave 2 | No | n/a | **FIXED — waits scan retained snapshots** |
| `TestArchiveSourceIdentityGuardsUnaryAndStreamingReads`, `TestMetricsCollector_RecordTransaction`, Java process-containment probes | 2026-10-01 | One concurrent broad Go run; affected tests passed on isolated retry | Timing/resource contention suspected; no product-code change made for these failures. Final full Go gate recorded in the diagnostics review. | Diagnostics/collector validation | No | n/a | **OBSERVED — isolated retries passed** |
| `tier0-live-metrics` advancement and `workflow-library-assignments` | 2026-10-01 | One full dev-admin cycle; unchanged fresh-controller retry passed | Metrics readiness and API time budgets failed during concurrent validation. Final retry passed live advancement and library persistence (3 tests / 1 profile-specific skip). | Diagnostics/collector validation | No | n/a | **OBSERVED — fresh-controller retries passed** |

| `ScriptImportReview > pages ten thousand modal findings` | 2026-10-01 | 2 concurrent full-suite failures; isolated test passed in 1.36 s | The deliberate 10,000-finding render/parse fixture exceeded Vitest's default 5 s budget under concurrent Go/UI validation (5.49 s and 5.40 s). Functional pagination and replacement assertions passed in isolation. This scale fixture now has a 15 s infrastructure budget; all 10,000 inputs and bounded-DOM assertions are retained. | Protocol diagnostics validation | No | n/a | **FIXED — full 4,640-test unit gate and focused follow-up passed** |
| `ScriptImportReview > bounds text scanning` | 2026-10-01 | 2 full-suite 5 s timeouts | Unanchored assignment/credential regexes retried long delimiter-free identifiers from every character, producing quadratic scan work. Field boundaries now prevent repeated suffix scans; quoted and encoded-key regression cases retain disclosure protection. | Protocol diagnostics validation | No | n/a | **FIXED — full unit gate plus quoted, encoded, numeric-prefix and suffix regressions passed** |
| `workflow-protocol-diagnostics.spec.ts` polling teardown | 2026-10-01 | 1 full dev-admin cycle failure after assertions completed | A poll handler awaited `response.json()` while Playwright disposed its request context. The test now drains route handlers with `unrouteAll({ behavior: "wait" })` during teardown. | Protocol diagnostics validation | No | n/a | **FIXED — five consecutive repetitions passed in each auth profile** |
| `TestLapsedProcessEpochRejectsTheClaimAndFencesTheAssignmentLeaseDifferently` | 2026-08-18 | 2 of 5 runs (both full-suite) | 30 ms wall-clock TTL could not survive scheduler pressure | QA program | No — fixed at the cause | n/a | **FIXED** (`267e9697b`) |
| `TestRunPreparedTestRetainsTestIdentityOnPollTimeout` | 2026-08-18 | 2 full-suite failures; 0 of 10 isolated repetitions before diagnosis | A 10 ms command-wide deadline could expire during preparation, before a test identity existed | QA program | No — fixed at the cause | n/a | **FIXED** |
| `TestExecuteControllerCIAbortsOnCancellation` | 2026-08-18 | 1 concurrent full-suite failure; isolated and serial full-suite reruns passed | Under investigation: cancellation returned the expected result, but controller-teardown confirmation exceeded its deadline under three-lane CPU contention | QA program | No | n/a | **OBSERVED — first occurrence** |

## Dashboard draft restoration observation — 2026-09-30

During the design-consistency full dashboard cycle, `workflow-new-test.spec.ts` →
`New Test saves drafts without execution, retains both tabs and validates recurring triggers`
failed once in dev-admin at line 141: after **Restore saved draft**, Run Name became
`Duplicate workload` instead of `Saved draft only`. No retries were enabled. The
failure artifact is `dashboard/test-results/dev-admin/workflow-new-test-New-Test-bc9e9-alidates-recurring-triggers/`.
An isolated follow-up run passed (1/1); this does not erase the original signal.
Root cause: the Restore button became available from local storage before the
scenario catalog request completed. Its empty catalog then produced a false
“scenario has changed or is no longer available” error. Restore now waits for the
current project catalog, with a visible explanation. A delayed-catalog unit test
covers the race. Owner: dashboard UX follow-up. Status: fixed; delayed-catalog unit coverage and the draft workflow pass in
both dev-admin and local-auth. No quarantine. The theme
control matrix passed separately in both authentication profiles.

## FLAKE-3 — apexctl cancellation teardown confirmation

**Location:** `cmd/apexctl/ci_test.go:1196`

The S4 integrated verification ran `go test ./...`, all dashboard checks, and the complete
Playwright suite concurrently. `TestExecuteControllerCIAbortsOnCancellation` produced the expected
`cancelled` / `CANCELLED` result, but its cleanup confirmation returned:

```text
controller may still be running the cancelled CI load: context deadline exceeded
```

The exact test passed immediately in an isolated uncached run. A subsequent full repository run
with Go package parallelism limited to one also passed, including the previously failing
`cmd/apexctl` package. That evidence classifies this as a first intermittent occurrence, not a
confirmed product failure and not yet a mandatory quarantine. A second occurrence triggers the
policy above: diagnose and fix the teardown timing or quarantine with an expiry within 24 hours.

## FLAKE-2 — apexctl poll-timeout partial result

**Location:** `cmd/apexctl/api_client_test.go:108`

The clean isolated `go test ./...` verification for the generated M1 API contract matrix observed
two failures: `runPreparedTest` returned a nil partial result after its timeout, although the test
expected the created test identity and elapsed time to survive. Ten immediate isolated repetitions
passed after the first occurrence; the second full-suite occurrence crossed the mandatory-action
threshold.

**Root cause.** `PollTimeout` bounds the entire command, including upload, test creation, start, and
polling. The test set it to 10 ms while claiming to exercise a polling timeout. Under full-suite CPU
pressure that deadline could expire during upload or test creation. A nil result is therefore the
only correct outcome: no durable test identity exists yet. The existing preparation-timeout test
already owns that contract.

**Fix.** `ApiClient` now has an unexported polling-wait seam. This test invokes
`runPreparedTestContext` with a non-expiring preparation context and injects
`context.DeadlineExceeded` only when polling begins. It deterministically proves the intended
behavior without weakening the command-wide production timeout or replacing one fragile duration
with a larger one.

**Evidence:** 200 consecutive repetitions passed in 1.0 s, followed by 200 with the race detector
in 3.2 s. The full repository suite is rerun as part of the owning M1 commit verification.

## FLAKE-1 — a 30 ms real-clock TTL in the epoch-fencing tests

**Location:** `pkg/api/distributed_evacuation_postfence_test.go:79`

**Reproduction rate:** 2 failures in 5 runs. Both failures were full-suite parallel runs; all three
isolated runs of `pkg/api` passed.

| # | Conditions | Result |
|---|---|---|
| 1 | Full suite, `go test ./...`, default covermode | pass |
| 2 | Full suite, explicit package list, `-covermode=atomic` | **FAIL** after 277.6 s |
| 3 | `pkg/api` alone, no coverage | pass, 263.8 s |
| 4 | `pkg/api` alone, `-covermode=atomic` | pass, 268.4 s |
| 5 | Full suite, explicit package list, `-covermode=atomic` | **FAIL** after 281.7 s |

**Root cause.** The test installs an execution epoch with a **30-millisecond TTL**, then sleeps 90 ms
to let it lapse deliberately:

```go
installed, err := client.ClaimExecutionEpoch(ctx, generation, owner, 30*time.Millisecond)
if err != nil || !installed.Accepted {
        t.Fatalf("install execution epoch = (%+v, %v)", installed, err)
}
time.Sleep(90 * time.Millisecond)
```

The failure is at the **install**, not at the assertion the test exists to make:

```
install execution epoch = ({Accepted:false CurrentGeneration:9
  ExpiresAt:2026-08-18 14:22:25.981 -0400 EDT
  ErrorMessage:execution generation changed or fencing did not complete}, <nil>)
```

With 110 packages competing for CPU, the goroutine can lose the scheduler for longer than 30 ms
between installing the epoch and observing the result, so the epoch it just created has already
lapsed — or a prior subtest's fencing has not finished. The 30 ms TTL and the 90 ms sleep are
wall-clock assumptions that hold on an idle machine and break under load.

**Hypothesis raised and rejected.** The first theory was `-covermode=atomic` instrumentation. Run 4
falsified it: same instrumentation, isolated, passed. The cause is contention, not coverage.

**Why it is not quarantined.** This is a real bug in the test, not an unexplained intermittency, and
the fix is known. Quarantining would hide the single most concrete piece of evidence the program has
produced for the M0 week-3 work.

**Recommended fix — M0 week 3, not a TTL bump.** Raising 30 ms to, say, 500 ms would make the flake
rarer without making it impossible, and would slow the suite. The correct fix is to inject the
`Clock` interface into the epoch-expiry path so the test advances time deterministically instead of
sleeping. This test is the first concrete instance justifying that work, and it sits in the
execution-epoch fencing code that M6's HA failover drills depend on — so the harness pays for itself
twice.

**How this was diagnosed.** Only because `scripts/coverage-baseline.sh` now retains its test log.
The first occurrence was undiagnosable: the script deleted its own log on exit (QA-004), destroying
the evidence for the failure it had just detected. The retention fix produced a named root cause on
the very next failing run.

**Adjacent risk worth measuring.** CI runs `-race -shuffle=on -count=1 -timeout=20m -coverprofile`
over `./...` (`.github/workflows/ci.yml:301`). `pkg/api` alone consumes 263–282 s here *without* the
race detector, which typically costs several times that, on a runner slower than this machine,
against a 20-minute ceiling. A timing-fragile test inside a package already near its timeout budget
is a bad combination.


## FLAKE-1 fixed 2026-08-18 — cause removed, not papered over

`generatorrpc.WithClock` now injects the service's time source, and every
execution-epoch time read goes through `Service.nowTime()` instead of a hardcoded
`time.Now()` (7 call sites across `execution_epoch.go` and
`execution_epoch_manager.go`). It follows the idiom already used in
`pkg/api/execution_leadership.go:71`, `pkg/api/scheduler.go:58` and
`pkg/kubeops/auth.go:119` — `now func() time.Time`, defaulting to `time.Now` —
rather than introducing a competing `Clock` interface the repository would then
have two of.

`TestClockHeldStillKeepsLeaseValid` demonstrates the fix directly: it claims a
lease with **FLAKE-1's exact 30 ms TTL**, sleeps 100 ms of real time, and asserts
the lease is still valid, because the clock did not move. Under the old
wall-clock path that is precisely the state that produced the flake.

**The flaking test is migrated.** It now installs the epoch with a one-minute TTL
and lapses it via `clock.Advance`, instead of a 30 ms TTL followed by a 90 ms
sleep. Every assertion is unchanged — the test is about how two rejection shapes
are *classified*, and the timing was only the mechanism for reaching the lapsed
state. Only that mechanism changed, so the test is no weaker.

**Evidence it is actually fixed, not merely currently green:**

| Run | Result |
|---|---|
| 200 repetitions, `-race` | pass, 4.7 s |
| 100 repetitions, `-race`, under 8-way CPU contention | pass, 4.5 s |
| Full `pkg/api` (268 s) and `pkg/distributed/...` | pass |

The contention run matters most: CPU starvation is the condition that produced
both original failures, and the migrated test is indifferent to it. It is also
faster — 0.06 s instead of carrying a 90 ms sleep.

Detection is now systematic rather than accidental: `.github/workflows/flake-hunt.yml`
runs the suite nightly with `-count=5 -shuffle=on -race` and reports repeat
failures. It deliberately does not gate PRs — a flake-finder on the merge path
teaches people to re-run until green.

## Dashboard cross-cutting verification observations — 2026-09-30

The first dev-admin `npm run test:cycle` run observed two failures without retries:

- `tier0-live-metrics.spec.ts` / `live metrics advance while a run is executing`: the count stayed at 5 for the 60-second observation window.
- `workflow-execution.spec.ts` / `archive read failure is visible and retry restores the same run evidence`: the transactions download returned HTTP 500 after the injected report failure was removed.

Concurrent Go compilation and dashboard checks exhausted local disk space (613 MiB free when inspected). Rebuildable Go cache data was cleared; source and application data were preserved. Disk pressure is a suspected environmental contributor, not a confirmed root cause of either browser failure. The original failures remain recorded rather than being hidden by automatic retries. Owner: dashboard QA. No quarantine; first observation. Both original failures passed on the fresh-controller follow-up (the live-metrics and execution workflow files). Neither assertion was weakened and automatic retries remain disabled. Root cause remains unconfirmed; retain this first-occurrence signal for subsequent runs.

## VuGen collection verification — 2026-10-01

The first dev-admin run of the new `workflow-loadrunner-collection.spec.ts` failed deterministically: its unanchored project-name locator matched both the project selection button and its Remove button. The locator now anchors the project name at the beginning. The UI had already displayed all three synthetic member results independently. This is a test defect, not a quarantined product flake; retries remain disabled. Follow-up outcomes are recorded in `reviews/clm-import-2026-10-01/import-review.md`.

The final project-selector workflow then exposed a product defect: the first member was a failed archive, so the selector was hidden inside the successful-result panel. A shared selector now appears for failed and review-only members as well as successful drafts. The regression explicitly navigates away from a failed member through that selector. Its initial trace is retained; profile follow-up results are recorded in the same review. This deterministic navigation defect is not quarantined, and retries remain disabled.

## Retirement validation observation — 2026-10-06

The retirement validation encountered the previously recorded host-contention pattern while Go compilation and external analytics work ran concurrently. An unconstrained unit run was stopped after timing failures; a complete two-worker run reported 4,732 passes and seven failures. The failures included the native Bruno credential review and large SecretBindingReview fixtures exceeding the default five-second budget, followed by credential mock-state failures. Two recording support tests also lacked sandbox permission to bind localhost (`listen EPERM`). The complete permitted rerun passed all 4,739 tests in 454 files with two workers and a 30-second fixture budget, without changing or skipping assertions. Owner: integration retirement validation.

A concurrent scripts policy run also observed `test_shared_deadline_rejects_late_success` collecting five distinct deadline values rather than one. This is outside the retirement implementation. The isolated unchanged `TestNodeFaultHarnessSafety` retry passed all underlying harness tests in 18.7 seconds; no test was disabled and no harness code was changed.
