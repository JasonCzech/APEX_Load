# Two-Week Review — Re-verification after the fix pass (fix commit `edab0967`)

> Status: Historical review snapshot. This file is preserved for audit context
> and should not be treated as current product documentation.

**Re-reviewed:** 2026-07-02, against `edab0967` ("Refactor performance testing platform architecture and telemetry" — 174 files, +6,194/−667), which fixes the 122 findings from the [2026-07-01 review](TWO_WEEK_REVIEW_2026-06-17_to_2026-06-29.md).
**Method:** 9 area agents re-classified every prior finding against current code (FIXED / PARTIAL / NOT_FIXED / REGRESSION) **and** hunted fresh bugs in the ~6,200 new lines; every negative verdict and fresh-bug claim went through a 3-vote adversarial panel. Plus a full baseline build/test run.

## Verdict

The fix pass is **strong and mostly complete** — but it is **not green, and it introduced new bugs.** Two things must be fixed before this ships: the Go test suite is red on `main`, and the pool-capacity fix traded one HIGH bug for another.

**Fix scorecard (122 prior findings, by original severity):**

| Severity | Fixed | Partial | Not fixed |
|----------|:-----:|:-------:|:---------:|
| CRITICAL | **5** | 0 | 0 |
| HIGH | **37** | 6 | 0 |
| MEDIUM | 21 | 3 | 26 |
| LOW | 19 | 0 | 5 |
| **Total** | **83** | **9** | **31** |

All 5 CRITICALs and 37 of 43 HIGHs are genuinely, verifiably fixed — I re-traced each. The session's engineering is good: F01's session-epoch race is closed by a targeted `UpdateUserIdentitySeen` (no more `SaveUser` on the hot path); F02's archive re-blessing is replaced by a scoped `test_notes.json`-only manifest update plus a `ManifestSHA256` trust anchor; F10's Postgres predicate is parameterized; F04's Java harness now requires a summary and honors lifecycle/return codes; the new `withAuthMutationLock` (BEGIN IMMEDIATE / LOCK TABLE) is a clean, dialect-correct fix for the last-admin races.

The problems are concentrated in three buckets below.

## Baseline: the tree is RED

- `go build ./...` ✅ · dashboard `tsc` ✅ · `vitest` 1533/1533 ✅
- **`go test ./...` ❌ FAILS** — `runners/java_vuser` fails `TestLRThinkTimeEmitsEventWithoutSleeping`. This violates the project's own testing policy and means CI running the full suite is red on `main`. Details in RN-BUILD below.

## Bucket 1 — New issues the fix pass introduced (regressions)

These did not exist before `edab0967`. Ranked by severity.

### RN-BUILD · HIGH · `go test ./...` is red — the new think_time test times `javac` + JVM startup
**`runners/java_vuser/lr_shim_test.go:166`** (`TestLRThinkTimeEmitsEventWithoutSleeping`)
The F42 **code** fix is correct: `lr.think_time(double)` (`runners/java_vuser/lrapi/lr.java:343-347`) now only emits an event and returns `PASS` — no `Thread.sleep`. But the test asserts total elapsed `< 750ms` while its `start` is set **before** `runHarnessFixture`, which shells out to `javac lr.java Harness.java Actions.java` (compiles three files cold) and then launches a JVM — all inside the timed window. Cold compile alone is ~3s. **Reproduced deterministically: 3.18s isolated, 4.24s under load.** The `"type":"think_time"` event assertion in the same test would pass; only the wall-clock assertion is broken.
**Fix:** time only `run.Run()` (compile/precompile outside the timed region), or drop the wall-clock assertion and keep the event assertion.

### RN-1 · HIGH (3/3 votes) · Pool-reservation global credit now over-admits the project's non-pool runs (double-spend)
**`pkg/api/timeslot_reservation.go:189-194`** → **`pkg/api/capacity.go:237-261`**
The F12 fix credits a project's active pool-scoped reservations into `projectReserved` in the global admission gate (and deliberately keeps them out of `totalReserved`). But `evaluateReservedCapacityBlockLocked` computes `projectReserved` for `test.ProjectID` on **every** run of that project — pool-pinned or not — and a larger `projectReserved` shrinks `sharedDelta` (`capacity.go:258`), i.e. treats more of the project's demand as already-reserved and off the shared envelope. So a **non-pool** run of the same project silently claims the pool reservation's credit against fleet-wide shared capacity, double-spending it.
**Failure:** Configured=10 generators; proj-c holds a global reservation of 6; pool-x has 4 members; proj-b holds an approved+bound pool-x timeslot for 4 and is running its pool-x run. proj-b launches a *non-pool* run of 4: the global gate credits `projectReserved=4` to it, `sharedDelta` collapses, and the run is admitted even though only `10−6−4(pool)=0` shared generators remain — over-subscribing the fleet. I re-traced this against live code and it holds; it is a direct consequence of the F12 fix.
**Fix:** apply the pool-reservation credit only when the *current* test is itself pool-pinned to that pool (or subtract pool-reserved counts from the effective envelope instead of crediting `projectReserved`). Add the double-spend scenario as a test.

### RN-2 · MEDIUM · F112 fix regresses metadata-cordon of an offline/unmapped generator
**`pkg/api/handlers_fleet_management.go:302`** → **`pkg/api/distributed_execution_backend.go:186-189`**
The F112 fix made `SetGeneratorDrainStateByID` error whenever the generator's identity isn't in the in-memory `idTargets` map — but that map is populated only after a successful probe/stream and is never persisted. So cordoning a *configured-but-currently-unreachable* generator (or any generator right after a controller restart, before the first inventory probe) now persists `Cordoned=true` via `SaveGeneratorMetadata` and **then** 400s in `reconcileMetadataCordonState` — a partial write plus an error the operator didn't get before.
**Fix:** treat a missing `idTargets` entry as a no-op-but-success for the metadata-cordon path (the cordon is already persisted and will be honored once the generator reconnects), or reconcile drain state before persisting.

### RN-3 · MEDIUM · F02 trust-anchor write introduces a data race on the shared archive record
**`pkg/api/test_notes.go:101-108`**
The F02/F49 fix added `refreshRunArchiveManifestTrust(record)` + `saveRunArchiveRecord(record)` inside the async note-sync. `refreshRunArchiveManifestTrust` mutates `record.ManifestSHA256` on the **shared** `*storage.RunArchiveRecord` returned by `GetRunArchive` (not a copy), guarded only by the per-test note lock — which does not exclude `handleVerifyRunArchive` / the audit report reader, which read `record.ManifestSHA256` concurrently. `go test -race` (which the testing policy calls for) would flag this.
**Fix:** compute the new trust anchor on a copy and swap under the storage lock, or serialize the record read/write against verify.

### RN-4 · LOW · Service Bus foreign-reply abandon budget can leave a message peek-locked
**`pkg/protocol/jms/realadapter_servicebus.go:275-285`**
Residual of the F05 fix: once a foreign reply hits the per-call abandon budget (`=1`), it is skipped (neither completed nor abandoned); if re-received later in the same call it stays peek-locked on the cached receiver until `LockDuration` lapses. Strictly better than the original loss, but still a transient reply-availability dent under heavy contention.


## Bucket 2 — Incomplete fixes (HIGH findings only partially closed)

Each of these fixed its headline scenario but left a reachable sub-case. All six were adversarially confirmed.

- **F12 — Pool reservation (`capacity_planning.go:450`, `timeslot_reservation.go:189`).** Headline "permanently-unadmittable reservation" is fixed, but part-2 (a reconcile-time guard so global + pool reservations can't jointly exceed the physical envelope) was skipped — and the credit mechanism introduced **RN-1**. Fix F12-part-2 and RN-1 together.
- **F13 — Fleet evacuation (`distributed_stream.go:281`, `distributed_resilience.go:153`).** The default case (evacuate without `tolerate_generator_loss` → test no longer killed) is fixed via `remoteTargetStateDone`. Residual: an operator-supplied evacuation `reason` that doesn't contain "evacuat" and isn't a known state string won't be recognized as an evacuation, so that path can still fail the test. Fix: mark evacuated targets with a dedicated sentinel state independent of the free-text reason.
- **F16 — Open-ended pause (`execution_plan_scheduler.go:154`, `scheduler.go:1364`).** Fixed for execution-plan schedules only. The **scenario-schedule** path (`upsertScenarioScheduleLocked`) is unchanged: `disabled_from` in the past + `disabled_until` empty still burns the full ~2.6M-iteration cron scan and returns the misleading "impossible day/month" 400. Fix: mirror `executionPlanSchedulePausedIndefinitely` into the scenario path.
- **F39 — Java replace-header setters (`java_http_idioms.go:303`).** `setHeader`/`setRequestProperty` now replace correctly. Residual: `if/else` branches setting the same header are flattened, so `if(x) b.header("Authorization",a); else b.header("Authorization",b);` still emits both values. No test covers the both-branch case.
- **F41 — `HARNESS_JAR` mode (`Makefile:71`, `java_vuser.go:253-269`).** The missing `Main-Class` is fixed (jar runs). But `commandArgs` still drops `j.cfg.Classpath` when `HarnessJar` is set (`-jar` ignores `-cp`), so `HARNESS_JAR` + a separate `APEXLOAD_JAVA_VUSER_CLASSPATH` still can't load user `Actions`. The passing test only works because it bundles the user class *into* the jar. Fix: use `-cp <jar>:<classpath> Harness` instead of `-jar` when a classpath is set.
- **F43 — Java event stream (`lr.java:515`, `java_vuser.go:453`).** The side-channel event file fixes stderr-gluing and flags truncation. Residual: `escape()` doesn't escape tabs/control chars (<0x20), so `lr.log("a\tb")` produces JSON that Go's `json.Unmarshal` rejects, and `parseRunnerEvents` silently skips the line — that transaction/event is dropped. Fix: escape all control chars in the Java emitter.

## Bucket 3 — Not yet addressed (still-open MEDIUM/LOW, adversarially confirmed)

The fix pass concentrated on CRITICAL/HIGH and left two clusters largely untouched at the MEDIUM level — **pool-capacity/timeslot edge cases** and **exec-plan/cron lifecycle** — plus the Java-importer fidelity MEDIUMs. 38 of the 40 still-open items were confirmed still-reproducible (F93 unconfirmed; the 5 LOWs are mostly out-of-scope items the session deliberately didn't attempt, e.g. F107 WS-on-revoke, F109 legacy SA migration).

| ID | Sev | Status | Cluster | Location | Finding |
|----|-----|--------|---------|----------|---------|
| F39 | H | PARTIAL | Archive/Java-importer | `pkg/convert/enterprisearchive/java_http_idioms.go:296` | Replace-semantics header setters (Apache setHeader, URLConnection setRequestProperty) and both-branch conditionals appen |
| F12 | H | PARTIAL | Pool capacity/timeslots | `pkg/api/capacity.go:225` | Pool-scoped reservations earn no credit in the global reserved-capacity gate, so an approved pool reservation can be per |
| F16 | H | PARTIAL | Exec-plan/cron | `pkg/api/scheduler.go:1343` | Open-ended pause (disabled_from set, disabled_until empty) makes schedule create/update fail with a misleading 'impossib |
| F13 | H | PARTIAL | Fleet | `pkg/api/distributed_stream.go:281` | Operator evacuation fails every impacted test that lacks tolerate_generator_loss, right after the API reports success |
| F41 | H | PARTIAL | Java sidecar/JMS | `Makefile:71` | APEXLOAD_JAVA_VUSER_HARNESS_JAR mode cannot execute any user code: Makefile jar lacks Main-Class and java -jar ignores t |
| F43 | H | PARTIAL | Java sidecar/JMS | `pkg/protocol/java_vuser/java_vuser.go:334` | Event stream shares raw stderr with JVM/user diagnostics and is silently lossy (interleaving, control characters, 8MiB t |
| F50 | M | NOT-FIXED | Archive/Java-importer | `pkg/api/handlers_archive.go:355` | JSON /archive/report unconditionally rebuilds every sibling archive in the release on each dashboard archive-tab load |
| F90 | M | NOT-FIXED | Archive/Java-importer | `pkg/convert/enterprisearchive/java_import.go:290` | Backfill residue classifier marks any unconsumed `String x = ...;` as converted/executable "consumed by the database lan |
| F91 | M | NOT-FIXED | Archive/Java-importer | `pkg/convert/enterprisearchive/java_http_idioms.go:14` | Concatenated URL literals are truncated to their first string fragment and emitted as executable HTTP requests in all fo |
| F92 | M | NOT-FIXED | Archive/Java-importer | `pkg/convert/enterprisearchive/java_http_idioms.go:121` | Split HttpRequest builder state shares Headers maps across build() snapshots — headers added after build leak into previ |
| F93 | M | NOT-FIXED | Archive/Java-importer | `pkg/convert/enterprisearchive/java_http_idioms.go:128` | HttpClient send dedup by request variable silently drops genuine repeated sends of the same request |
| F63 | M | PARTIAL | Auth/RBAC | `pkg/api/handlers_auth.go:1177` | Last-admin lockout guard is check-then-act: two concurrent demotions/disables of the two last admins can leave zero admi |
| F66 | M | NOT-FIXED | Pool capacity/timeslots | `pkg/api/handlers_timeslots.go:401` | Approval-time overbooking guard ignores settings-based static reservations, deterministically allowing over-commitment |
| F67 | M | NOT-FIXED | Pool capacity/timeslots | `pkg/api/handlers_timeslots.go:355` | Concurrent approvals of two overlapping timeslots both pass the overbooking guard (check and CAS are not atomic) |
| F68 | M | NOT-FIXED | Pool capacity/timeslots | `pkg/api/capacity.go:72` | Pool capacity protection is keyed on self-declared assignment.pool_id — runs targeting the same generators without it by |
| F69 | M | NOT-FIXED | Pool capacity/timeslots | `pkg/api/handlers_fleet_management.go:175` | Deleting a fleet pool silently orphans approved pool-scoped reservations (and pool quotas) into the 'silently inert' sta |
| F78 | M | NOT-FIXED | Pool capacity/timeslots | `pkg/api/timeslot_reservation.go:82` | Launch timeslot re-validation uses stale ScheduledStartAt, so a late launch validates against a window it will not actua |
| F79 | M | NOT-FIXED | Pool capacity/timeslots | `pkg/api/testexecution.go:1099` | PENDING→promotion path bypasses timeslot launch re-validation (and all start gates) |
| F80 | M | NOT-FIXED | Pool capacity/timeslots | `pkg/api/handlers_timeslots.go:341` | Concurrent approvals of overlapping pending reservations both pass the overbooking guard |
| F57 | M | PARTIAL | Desktop/dashboard | `desktop/keepawake_unix.go:18` | Sleep-inhibitor subprocess is orphaned on crash/force-quit (no caffeinate -w / Pdeathsig), permanently blocking system s |
| F89 | M | NOT-FIXED | Desktop/dashboard | `dashboard/src/components/TestNotesDialog.tsx:37` | TestNotesDialog draft is not registered with the navigation-guard/beforeunload system; nav channels outside the dialog d |
| F97 | M | NOT-FIXED | Desktop/dashboard | `pkg/api/telemetry.go:601` | threshold_alert WebSocket events are broadcast but consumed by nothing — Settings→Thresholds alert knobs are silent no-o |
| F95 | M | PARTIAL | Docs/PG dialect | `pkg/storage/factory_test.go:110` | Postgres guard test covers only 2 of the 3 coordinated boolean-rewrite categories — integer-literal comparison predicate |
| F71 | M | NOT-FIXED | Exec-plan/cron | `pkg/api/scheduler.go:1174` | Preview endpoint skips minute-adjacent occurrences: cursor double-advances by one minute vs the strictly-after evaluator |
| F72 | M | NOT-FIXED | Exec-plan/cron | `pkg/api/scheduler.go:952` | DST fall-back: schedules in the repeated local hour fire twice, contradicting the documented 'earliest matching instant |
| F73 | M | NOT-FIXED | Exec-plan/cron | `pkg/api/scheduler.go:226` | Unsatisfiable-but-valid schedules trigger an unbounded ~2.6M-iteration scan with time.LoadLocation per candidate — multi |
| F74 | M | NOT-FIXED | Exec-plan/cron | `pkg/api/execution_plan_scheduler.go:195` | Crash after occurrence anchor write leaves the slot permanently 'materializing' — the scheduled run is lost with no retr |
| F75 | M | NOT-FIXED | Exec-plan/cron | `pkg/api/handlers_execution_plans.go:166` | Deleting an execution plan orphans its schedules: they keep sweeping forever and become invisible and undeletable via th |
| F76 | M | NOT-FIXED | Exec-plan/cron | `pkg/api/execution_plan_scheduler.go:189` | Sweep writes back a stale full schedule row, silently reverting concurrent operator edits (Revision exists but is never |
| F77 | M | NOT-FIXED | Exec-plan/cron | `pkg/api/handlers_execution_plans.go:296` | Parallel materialization consumes the single-use approval even when zero tests are created |
| F70 | M | NOT-FIXED | Fleet | `pkg/api/distributed_resilience.go:92` | Evacuation leaves the evacuated target inside zero-VUser shard lists, permanently breaking later stage/goal/manual scali |
| F83 | M | NOT-FIXED | Import/engine | `pkg/convert/salesforce/analyzer.go:543` | Salesforce cheap-detect false-positives on generic HARs (any-host /login path prefix; any JSON body containing "descript |
| F84 | M | NOT-FIXED | Import/engine | `pkg/convert/trueclient/control_flow.go:104` | TruClient 'Do While' loop steps import as pre-test while loops — run-at-least-once semantics silently dropped |
| F85 | M | NOT-FIXED | Import/engine | `pkg/convert/salesforce/analyzer.go:1229` | Synthesized Salesforce bulk poll loop gives no failure signal when the job ends Failed/Aborted or the poll cap is exhaus |
| F94 | M | NOT-FIXED | Java sidecar/JMS | `api/proto/generator.proto:233` | OOM-guard truncation flags and the entire runner event stream are dropped in distributed runs and never surfaced in the |
| F101 | L | NOT-FIXED | Archive/Java-importer | `pkg/api/handlers.go:326` | Stop/abort actor attribution races run finalize; archived manifest can permanently record no stopped-by (plus an unsynch |
| F107 | L | NOT-FIXED | Auth/RBAC | `pkg/api/websocket.go:232` | Session revocation / disable / delete does not terminate live WebSocket streams |
| F109 | L | NOT-FIXED | Auth/RBAC | `pkg/api/auth_resolution.go:544` | Legacy scope-less service accounts hard-fail everywhere with no migration or access-review flag |
| F118 | L | NOT-FIXED | Desktop/dashboard | `dashboard/src/components/ScriptEditorActionEditor.tsx:541` | Dashboard structured loop editor has no do_while affordance — the flag is invisible and cannot be set/cleared from the U |
| F116 | L | NOT-FIXED | Import/engine | `pkg/convert/asyncapi/converter.go:1540` | Recursion-depth caps in AsyncAPI Avro sampling and WSDL simpleType resolution truncate silently — no diagnostic/warning |

Two of these are worth pulling forward from MEDIUM because they are silent-wrong-result, not mere edge cases:
- **F79** — the PENDING→promotion launch path bypasses timeslot re-validation *and all start gates*, so a run promoted from the queue can start outside its approved window / over capacity.
- **F90–F93** — Java-importer residue: unconsumed `String x = …` marked converted/executable, concatenated URLs truncated to their first fragment, shared builder header maps, deduped repeated sends — all silent-drop/mis-convert paths in `pkg/convert/enterprisearchive` that produce a script that looks converted but replays wrong.

## Remediation plan (follow-up)

**P0 — before this ships (green + no new HIGH):**
1. **RN-BUILD** — fix the think_time test timing so `go test ./...` passes. One-line change; this is gating.
2. **RN-1** — scope the pool-reservation credit to pool-pinned runs so it can't double-spend against shared capacity. Land with **F12-part-2** (reconcile-time joint-envelope guard) and a double-spend regression test.

**P1 — finish the partial HIGH fixes:** F13 (evacuation sentinel state), F16 (scenario-schedule open-ended pause), F39 (both-branch header), F41 (classpath in HARNESS_JAR mode), F43 (escape control chars). Plus **RN-3** (archive-record race — cheap, and `-race` will keep flagging it) and **RN-2** (cordon-offline regression).

**P2 — the deferred MEDIUM clusters:** pool-capacity/timeslot races (F66–F80: convert the check-then-act overbooking guards to CAS, key pool protection on validated assignment not self-declared `pool_id`, cascade pool-delete, re-validate on promotion — F79 first), exec-plan/cron lifecycle (F71–F77, F50), and Java-importer fidelity (F90–F93). These share root causes within each cluster, so batch by cluster.

**P3 — LOW / out-of-scope:** F101, F116, F118, and the deliberately-deferred F107/F109 (document as accepted-for-now or schedule).

## Confidence & method notes

- Re-review: 9 area agents → 3-vote adversarial confirmation of every PARTIAL/NOT_FIXED/regression/fresh-bug claim. 39 of 40 negative verdicts upheld; 1 fresh claim (a think_time-pacing importer bug) was overturned and dropped. RN-1 and RN-BUILD were additionally re-traced by hand.
- The CRITICAL/HIGH "FIXED" verdicts were each re-traced to the specific fix (`UpdateUserIdentitySeen`, scoped manifest update, `boolValue` predicate, harness summary requirement, batch-abandon) — not inferred from the commit message.
- Pre-existing, out-of-window items remain out of scope and were not re-counted here: WSDL C1/C2/C3, browser-replay clear-before-fill/timeouts/popup/mixed-ws.


---

# Fixes applied (2026-07-02, this pass)

Landed the P0 + partial-HIGH batch. `go build ./...` ✅, `go test ./...` ✅ (green — the tree was red before), dashboard `tsc` ✅. Each fix shipped with tests. Changes are uncommitted in the working tree (commit via GitHub Desktop).

**Fixed + tested:**
- **RN-BUILD (HIGH)** — greened the tree. `runners/java_vuser/lr_shim_test.go` now times only the JVM run (new `runHarnessFixtureWithInputTimed`), threshold widened to 1.5s so it still catches a 2s sleep but tolerates cold JVM start.
- **RN-1 + F12 (HIGH)** — pool-reservation double-spend. `pkg/api/capacity.go`: the shared (non-pool) gate now early-returns for pool-pinned runs (they're governed by the pool gate — this also cleanly fixes F12's "pool run unadmittable"), a non-pool run is credited only by its project's *global* reservations (`projectNonPoolReservedAt`, no pool credit), and `sharedGeneratorUsageLocked` excludes pool-pinned actives. Tests: `TestPoolReservationAdmitsOwnersPoolPinnedRun`, `TestPoolReservationDoesNotCreditNonPoolRun`.
- **F16 (HIGH)** — open-ended scenario-schedule pause. `pkg/api/scheduler.go` short-circuits `nextScenarioScheduleRun` when paused indefinitely (reusing `executionPlanSchedulePausedIndefinitely`); the next-run preservation guard now requires `existing.NextRunAt != nil` so *un-pausing* recomputes instead of staying stuck nil. Test covers create-paused + un-pause-recomputes.
- **F41 (HIGH)** — `HARNESS_JAR` + classpath. `pkg/protocol/java_vuser/java_vuser.go` launches via `-cp <jar:classpath> Harness` (not `-jar`, which ignores `-cp`) when both are set. Tests assert the arg form.
- **F43 (HIGH)** — Java event escaping. `runners/java_vuser/lrapi/lr.java` `escape()` now escapes `\t \b \f` and all control chars `<0x20` as `\uXXXX`, so a tab/control char in a message no longer yields JSON Go rejects (dropping the event).
- **F13 (HIGH)** — evacuation sentinel. `pkg/api/distributed_resilience.go` stores a fixed `remoteTargetStateEvacuated` sentinel instead of the free-text reason, so a custom operator reason is still recognized by `remoteTargetStateDone` and the run isn't failed. Test uses a custom reason.
- **RN-3 (MEDIUM)** — archive-record data race. `pkg/api/test_notes.go` publishes the refreshed trust anchor via copy-and-swap under `tr.mu` (`replaceRunArchiveManifestTrust`) instead of mutating the shared record in place. Verified race-free under `-race`.

## Second fix pass (2026-07-02): RN-2 + F39 completed

Both items that were deferred/reverted above are now fixed, each after two rounds of adversarial self-review that caught real flaws in my drafts. `go build ./...` ✅, targeted + full `pkg/api` and `pkg/convert/enterprisearchive` suites ✅.

- **RN-2 (MEDIUM) — FIXED.** The metadata cordon is now enforced on generator (re)connect. `distributedExecutionBackend` gains an `onIdentityRecorded` hook (fired only on a new/changed id↔target mapping, outside all backend locks); the Server registers `applyMetadataCordonOnReconnect`, which reconciles the live drain state to the persisted `metadata.Cordoned` **in both directions** — applies a cordon, clears a stale cordon on un-cordon — while never clobbering a `draining` state (read via the new `GeneratorDrainStateByID`). `reconcileMetadataCordonState` is now tolerant of an offline generator (the hook guarantees eventual application). Self-review round 1 caught an un-cordon-stuck-cordoned asymmetry; round 2 confirmed sound. Tests: cordon-applies-on-reconnect, and both-directions + draining-guard.
- **F39 (HIGH) — FIXED.** Same-name headers set in different branches of one if/else chain now emit once; **independent** sequential `if`s still keep both (append). Implemented with chain-aware branch keys (`<chain>#<idx>`) threaded through a sibling-aware `collectJavaParsedFunctionsInBranch`, a synthetic phase-block wrapper so top-level if/else siblings are detected, and per-builder-var branch tracking reset on `newBuilder()`. Self-review round 1 caught a false-positive that dropped independent-`if` headers; round 2 caught stale dedup state on builder-var reuse. Tests: if/else collapse, independent-if keep-both, builder-var reuse. Conservation invariant still holds (dropped branch statement gets a disposition via backfill).

- **RN-5 (MEDIUM, pre-existing data race) — FIXED.** `buildRunArchiveProvenance` (`pkg/api/execution_backend.go`) ran in the test-execution goroutine reading ~30 `ManagedTest` fields (including `PendingReason`, read by `buildRunArchiveGovernanceSnapshot`) without `tr.mu`, racing the pending-queue refresh's writes. It now snapshots the test under `tr.mu.RLock` once (`copied := *test`; `ManagedTest` has no embedded locks) and reads all fields from the private copy, keeping `GetSystemSettings`/`SystemInventory` (which take their own locks) outside. Verified: the two tests the `-race` detector originally flagged (`TestRunTestCreatesArchiveBundle`, `TestDeleteTestRemovesArchiveBundle`) plus a new targeted race test are all clean under `go test -race` (0 data races).
- **SQLite `busy_timeout` (robustness, bonus) — ADDED.** While verifying RN-5, the note-sync tests were found to flake (hang to a wait-timeout) when run together: the tests poll `store.GetRunArchive` (a SQLite read) while the async note-sync goroutine does `saveRunArchiveRecord` (a SQLite write), and `NewSQLiteStore` opened the DB with **no `busy_timeout`**, so brief read/write overlap failed immediately with "database is locked". This is a pre-existing production robustness gap (the controller has many concurrent DB accessors). `NewSQLiteStore` now opens with `_pragma=busy_timeout(5000)` (`pkg/storage/sqlite.go` `sqliteDSN`), so a writer waits for a concurrent reader instead of failing. The note-sync test group is now stable across repeated runs. (Also nudged the shared test `waitForTestCondition` deadline from 2s→15s so it tolerates parallel-suite load — a met condition still returns immediately.)

**Still open:**
- The ~30 deferred MEDIUM/LOW cluster items (pool-capacity/timeslot races F66–F80, exec-plan/cron lifecycle F71–F77, Java-importer fidelity F90–F93).
