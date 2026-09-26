# HA Run Continuity Plan

Status: Approved (2026-09-25) — D1–D4 accepted as recommended; P0 complete (§9); F1–F6 fixed; P1 complete (§10); P2 complete (§11); P3 complete (§12): a run now survives leader loss; P4 complete (§13); all uncommitted
Owner: TBD
Scope: keep an in-flight **distributed** run alive when the HA execution leader
crashes, by having the new leader adopt the running generators instead of
failing the run.

## 1. Problem

With `APEXLOAD_CONTROLLER_HA=true`, losing the leader pod always ends the
in-flight run as `FAILED` ("controller restarted before the run completed").
The HA work (`006a8bbc`) only guaranteed *no double execution and no
cross-replica clobber*; continuity was never in scope, and
`docs/ENTERPRISE_CONTRACTS.md` (§ reconciliation) documents the fail-closed
behaviour.

## 2. Why a run cannot survive today (verified in code)

| # | Blocker | Where |
|---|---------|-------|
| B1 | **Generator epoch outlives the controller by ~0s.** The epoch TTL a generator is granted is the controller's *remaining* lease time minus transit, so it expires no later than the old leader's lease. On expiry the generator force-stops every assignment of that generation. A new leader cannot acquire the lease until it has expired, so load is always stopped before a successor exists. The 60s run-supervision deadman never matters — the epoch fires first. | `pkg/api/distributed_epoch.go` (safeTTL), `pkg/distributed/generatorrpc/execution_epoch.go` `expireExecutionEpochToken` → `forceStopFencedTests` |
| B2 | **A higher generation kills lower-generation work.** Even with B1 relaxed, the new leader's first `ClaimExecutionEpoch` (higher durable generation) fences and force-stops every older assignment. | `execution_epoch_manager.go` `planClaimLocked`, `testIDsBeforeGenerationLocked` |
| B3 | **The run-supervision token exists only in the dead pod's memory.** It is random per run and never persisted, so no successor can renew `ClaimRunSupervision`. | `distributed_execution_backend.go:867` (`remoteRunSupervisionToken`) |
| B4 | **Takeover unconditionally fails active runs.** | `persistence.go:769` `reconcileStoredActiveTest` |
| B5 | **Controller-side live state is memory-only:** generator clients, metrics streams, `goalControl`, goal error windows, legacy per-target SLA accumulators, threshold-alert dedupe, sustained-P99 `earlyStopBreachStartedAt`, stage/profile driver goroutines, `remoteActivated`/`remoteControlGeneration`. | `runner.go` `ManagedTest` unexported fields |
| B6 | **Reverse-connect generators dial a controller endpoint**, which in HA may land on a follower. Reconnect-to-leader behaviour is unverified. | `cmd/generator/reverse.go` |

## 3. What is already on our side

- **Raw results are durable on the generators.** The final archive is a merge
  of generator-local archives (`distributed_archive.go`
  `fetchRemoteArchiveBundles` / `mergeRemoteArchiveManifest`), so measured
  data from the whole run survives a controller loss.
- **Live history is persisted every tick** (`saveLiveMetricsHistory` →
  `test_history`) and rehydrated on reload, so the controller-owned archive
  overlay (target TPS, telemetry health) survives up to the last tick.
- **Most run configuration is persisted:** `Groups`, `StartedAt`,
  `GoalTargets` and the assignment settings are on the test row.
  *Correction (P1):* generator placement (`GeneratorShards`) is **not**; the
  adoption checkpoint carries it (§10).
- **Durable generation fencing already exists** — the exact safety primitive
  adoption needs, it just has no "carry this run over" path.
- **Stage schedules are absolute targets on a pause-aware run clock**
  (`runStageSchedule`), so a successor can recompute "where should we be now"
  instead of replaying history.

## 4. Answer to the open question: who drives the load shape?

**Mixed.**

| Runs autonomously on generators (continues through the gap) | Driven by the controller (holds at last value during the gap) |
|---|---|
| `ramp_up`, `target_duration`, `ramp_down`, pre/post resource baseline, group start offsets — all carried in `WorkAssignment` | Stage schedules (`stageDriven` groups → `scaleVUsersNow` → `AdjustVUsers`) |
| Duration completion (generator finishes and stops on its own) | Traffic profiles (`startTrafficProfileSchedules(ctx, test, nil)`) |
| | Goal-oriented target TPS (`goalControl`) |
| | Sustained-P99 auto-stop, threshold alerts, manual scale/pause |

Implications:

- A simple ramp → steady-state → ramp-down run needs only
  checkpoint + re-attach; its load shape is unaffected by the gap.
- Stage/profile/goal runs will **deviate from the authored shape** for the gap
  (they hold the last VUser count / target TPS). After adoption the drivers
  jump to the current target. The report must say so.
- If duration ends **during** the gap, generators complete on their own; the
  successor must finalize the run as COMPLETE rather than adopt it.

## 5. Design

### 5.1 Run continuity policy (opt-in)

New run field `failover_policy`: `fail` (default; today's behaviour, unchanged)
| `continue`. It is snapshotted at start. It only takes effect for distributed
runs on an HA controller. Otherwise launch preflight shows a warning ("continuity
not available: local backend / HA disabled") and the run behaves as `fail`.
Scenario-level default plus a per-run override, like other runtime settings.

### 5.2 Generator: bounded orphan grace

When the epoch or supervision lease lapses for a `continue` assignment, the
generator enters **ORPHANED** instead of force-stopping:

- Load continues (see decision D2). Metrics keep going to the local archive.
  Live batches are buffered, bounded, and drop-oldest with a counter.
- The grace period is set by the controller in the assignment
  (`adoption_grace`) and hard-capped by generator env
  `APEXLOAD_GENERATOR_MAX_ADOPTION_GRACE` (proposed default 45s, cap 120s).
- If the run is not adopted within the grace period, the generator force-stops
  it with the existing escalation path, `FAILED`, reason
  `controller_adoption_timeout`. The existing +2s hard-exit backstop still
  applies.
- `fail` assignments behave exactly as today.

**This relaxes a deliberate invariant.** Today load cannot outlive the
controller lease. After this change the bound becomes lease TTL plus adoption
grace, only for runs that opted in. Record this in the security posture doc.

### 5.3 Adoption protocol (atomic with the epoch claim)

Adoption must happen **inside** the higher-generation epoch claim. Otherwise
B2 fences the run before a separate RPC could reach it.

```proto
message RunAdoption {
  string test_id = 1;
  uint64 prior_generation = 2;
  bytes  prior_run_supervision_token = 3;  // proof of lineage
  bytes  new_run_supervision_token = 4;    // rotated on adoption
}
message ExecutionEpochClaim {
  ...
  repeated RunAdoption adoptions = 5;
}
message RunAdoptionResult {
  string test_id = 1;
  enum Outcome { ADOPTED = 0; ALREADY_COMPLETED = 1; ALREADY_STOPPED = 2; REJECTED = 3; UNKNOWN_TEST = 4; }
  Outcome outcome = 2;
  string  detail = 3;
  // assignment state at adoption: current VUsers per group, paused, elapsed
}
message ExecutionEpochAck {
  ...
  bool adoption_supported = 6;             // capability ack, like test_lease_accepted
  repeated RunAdoptionResult adoptions = 7;
}
```

The generator rebinds each assignment whose token hash matches and that is
still ORPHANED or active to the new generation, then fences everything else as
it does today. The metrics broker's generation fence moves with it. Older
generators ignore field 5 and leave `adoption_supported=false`. The controller
treats that as REJECTED and falls back to `fail`, the same rolling-upgrade
pattern as `test_lease_accepted`.

### 5.4 Controller: persist what adoption needs

- `failover_policy` (bool-ish column; **Postgres gotcha: three coordinated
  entries** for any new bool, per the dialect rewrite).
- Run-supervision token, **wrapped with the existing `pkg/keywrap` KEK**, never
  stored in plaintext. It is rotated on every adoption.
- `executionGeneration` of the owning term.
- Controller-driven driver state not already derivable: the goal-controller
  state snapshot and sustained-P99 breach start. Stage and profile position is
  recomputed from the run clock, not persisted.

### 5.5 Controller: takeover path

`reloadAndReconcile` changes to split `reconcileStoredActiveTest`:

| Stored status | `fail` policy | `continue` policy |
|---|---|---|
| INITIALIZING, SHAKEOUT | FAILED (today) | FAILED (ambiguous; unchanged) |
| RUNNING, PAUSED | FAILED (today) | **adopt** |
| STOPPING | FAILED (today) | re-issue Stop, then finalize normally |

The adopt steps:

1. Mark the run `continuity.state = recovering`. The status stays `RUNNING` or
   `PAUSED`, so there is no new top-level status and no enum ripple.
2. Open clients to the persisted shard targets and send `ClaimExecutionEpoch`
   with the adoptions. Per target, handle the outcome: `ADOPTED` continues;
   `ALREADY_COMPLETED` means finalize the run as COMPLETE through the normal
   archive merge; target lost or `REJECTED` hands over to the existing
   evacuation path (`distributed_resilience.go`) when capacity allows,
   otherwise the run FAILs with partial evidence.
3. Re-claim supervision with the rotated token, reopen metrics streams, and
   rebuild the `remote*` maps. Rebuild SLA accumulators from the generators'
   cumulative SLA counters. Legacy generators get marked "SLA partial".
4. Restart stage, profile and goal drivers at the recomputed offsets. Re-arm
   the duration finalizer.
5. Apply any queued operator Stop or Abort (§5.6). Mark
   `continuity.state = adopted` and record the gap.

### 5.6 Operator actions during the gap

- No leader: followers return 503, as today.
- Leader recovering: Stop and Abort are **accepted and queued** (202) and
  applied first thing after adoption. Scale, pause, duration and goal changes
  get 409 "run recovering".

### 5.7 Results and UX

- API: a `continuity` object on the test
  (`state, failover_at, adopted_at, gap_seconds, adopted_targets, lost_targets,
  shape_deviated`). Update swagger and route goldens.
- Command Center banner while recovering and after adoption.
- New report section, **"Controller failover"**, showing the window, affected
  groups and shape deviation. Register it in the report section registry so
  HTML/PDF/CSV/React all pick it up (the coverage-matrix test enforces this).
- Measured data inside the window is kept, because generators measured it. The
  controller overlays for the window (target TPS, telemetry health) are marked
  "not observed".
- Evidence-based baseline comparison (`f7ebb37a`): runs with a failover carry
  an evidence caveat.

### 5.8 Reverse-connect generators (B6)

Verified in P0 (§9, F5/F6). HA overlays and Helm already publish the leader
into the `apexload-controller-active` EndpointSlice. Reverse generators are
therefore correct only when their controller URL resolves through that Service.
Three required changes:

- The gateway refuses enrollment on followers with **503** plus a leader hint.
  It must not use a 4xx, because the connector treats every 4xx as permanent.
- The connector treats 429 and 503 as retryable.
- The connector caps backoff at ≤ grace/3 (15s at the 45s default) while HA is
  in use. Today the cap is 30s.

### Out of scope

- **Local-backend runs.** The VUsers die with the pod, so there is nothing to
  continue. These always fail.
- Non-execution write consistency across replicas (already a known, separate
  concern).

## 6. Phases

| Phase | Work | Rough size |
|---|---|---|
| **P0 Evidence + spikes** | kind cluster: 2 controllers + Postgres + 2 generators. Kill the leader with `kill -9` mid-run and capture today's timeline as baseline evidence. Answer: reverse-connect reconnection; metrics-broker behaviour with a new subscriber; coverage of cumulative SLA counters; generator archive retention TTL ≥ grace + finalize time. | 2–3 d |
| **P1 Persistence** | `failover_policy` (+ PG triple entry), keywrapped supervision token, generation, goal/breach snapshot; preflight warning. No behaviour change. | 3–4 d |
| **P2 Generator** | ORPHANED state + grace cap, adoption in `ClaimExecutionEpoch`, capability ack, token rotation, ALREADY_COMPLETED reporting. | 5–7 d |
| **P3 Controller takeover** | reconcile split, adoption orchestration, runtime rebuild, driver resume, queued Stop/Abort, partial adoption through evacuation. | 7–10 d |
| **P4 Surface** | `continuity` API + swagger, Command Center banner, report section + registry, baseline caveat, docs (ENTERPRISE_CONTRACTS reconciliation, security posture, HA yaml). | 3–5 d |
| **P5 Chaos validation** | automated failover e2e in CI on kind covering the §7 matrix. | 3–5 d |

About 5–7 engineer-weeks. P1 and P2 can proceed in parallel after P0.

## 7. Acceptance criteria

1. **`fail` policy is byte-identical to today.** The existing leadership,
   fencing and 503 tests pass unchanged.
2. **Simple shape:** `kill -9` the leader in steady state. The run reaches
   COMPLETE. Generator VUser counts never drop. The recorded gap is ≤ lease
   TTL + adoption time (target ≤ 40s). The final archive transaction count is
   within ±1% of a no-failover control run. The report shows the window.
3. **Stage-driven run:** stage transitions missed during the gap are applied
   within one driver tick after adoption, and `shape_deviated=true`.
4. **Duration ends during the gap:** the run is finalized COMPLETE with a full
   archive.
5. **No successor within grace:** generators stop within grace + 2s. The run
   is FAILED with `controller_adoption_timeout`.
6. **Stop during recovery:** it is applied immediately after adoption.
7. **Double failover:** a second leader loss during recovery is still adopted.
8. **Split brain:** a paused old leader (SIGSTOP longer than the TTL) resumes.
   Its generation is fenced, and its writes are rejected by lease-fenced
   persistence.
9. **Security:** an adoption with the wrong token, a stale prior generation or
   a different lease owner is REJECTED. The token never appears in plaintext
   in the DB or logs.
10. **Mixed fleet:** an old generator plus a new controller falls back to
    `fail` cleanly.

## 8. Decisions (accepted 2026-09-25, as recommended)

- **D1: Default policy.** Recommend **opt-in** (`fail` default) for the first
  release, then revisit default-on for HA after the P5 evidence.
- **D2: Load during the gap.** Recommend **keep running**. It preserves the
  test, and the grace bound limits the exposure. The alternative, pausing VUsers
  while orphaned, is safer against uncontrolled load but puts a dip in every
  failover run and distorts the results.
- **D3: Grace.** Recommend a 45s default and a 120s generator hard cap.
- **D4: SLA treatment of the window.** Recommend **include** the measured data
  and annotate the window. Excluding it would hide real target behaviour.

## 9. P0 results (2026-09-25)

Harness: [`scripts/ha-failover-harness.sh`](../scripts/ha-failover-harness.sh).
It runs 2 HA controllers and 2 generators as local processes, with a shared
throwaway Postgres and an HTTP target that counts requests per second. No
cluster is involved. The target's request log is the ground truth for whether
load continued.

### Baseline timeline (today's behaviour, 10 VUsers, 150s run, leader killed at +45.9s)

| t (s) | Event |
|---|---|
| 0 | run started on c1 (leader); target reaches ~40 req/s by +7s |
| **45.9** | `kill -9` of leader c1 |
| 46 – 71 | **load continues unchanged at ~33–38 req/s** (generators hold the epoch they were granted) |
| ~72 | load stops; no generator log line records why (F3) |
| **77.6** | c2 acquires the lease (31.7s after the kill = lease TTL + renew timing) |
| 78.7 | c2 reconcile marks the run `FAILED: controller restarted before the run completed` |

This confirms B1 empirically. Generators outlived the leader by about 26s,
but the successor arrived **about 5.6s after load had already stopped**. There
is no overlap in which a successor could adopt anything. During the gap the
follower served the stale `RUNNING` status. The adoption grace (D3: 45s)
covers the measured 31.7s takeover with about 13s to spare for adoption RPCs.

### Open questions answered

| Question | Answer | Plan impact |
|---|---|---|
| Can a metrics stream be re-subscribed mid-run? | **Yes.** `metricsBroker.registerLocked` gives a new subscriber a live snapshot anchored to the latest `batch_sequence` (so gap detection isn't reset) and replays terminal tombstones. It filters by execution generation. | Adoption must move the broker generation with the assignment (already in §5.3). Tombstone replay covers `ALREADY_COMPLETED`. |
| SLA state rebuildable? | **Yes for current generators.** `MetricsBatch.transaction_response_time_sla` (field 21) carries cumulative per-transaction counts. Only legacy generators rely on controller-side accumulation. | Legacy generators are marked "SLA partial" (already in §5.5). |
| Generator archive retention vs. grace | 24h default (`APEXLOAD_GENERATOR_ARCHIVE_TTL`) is far more than grace plus finalize time. | None. |
| Reverse-connect generators reach the leader? | Only if their URL goes through the `apexload-controller-active` Service. The gateway itself has no leader check (F6), and the connector's error handling is fragile (F5). | §5.8 updated. |
| Who drives the load shape? | Answered in §4 (mixed). | — |

### Findings (outside the continuity feature itself)

| ID | Sev | Finding | Evidence | Fix (status: **fixed 2026-09-25**, uncommitted) |
|---|---|---|---|---|
| **F2** | **Critical** | **On Postgres, no test longer than ~2.1s can be created.** `rewriteSQL` leaves SQLite `INTEGER` as Postgres `int4`, and `tests.duration_ns` is nanoseconds. Create fails with `persist test: … unable to encode 150000000000 … for int4`. HA requires Postgres, so **HA as shipped cannot run a realistic test.** The same int4 hazard applies to every `INTEGER` column holding int64 values (`cumulative_pause_ns`, byte counts, ms timestamps …). | Harness run 2 (Postgres store, pre-workaround); `pkg/storage/sqlite.go` `postgresSQLRewrites` has only one `BIGINT` entry (`size_bytes`) | Rewrite `INTEGER`→`BIGINT` for Postgres DDL after the boolean rewrites, and add a startup migration widening existing int4 columns. The migration must run before pooled prepared statements exist: changing a type under a live pool produced `cached plan must not change result type` on the follower during P0. **Done:** `postgresDDLIntegerType` in `rewriteSQL`; `ensurePostgresIntegerColumnsBigint` (owned tables only, advisory-locked, skip when nothing to do); `TestPostgresSchemaDeclaresNoInt4Columns` + real-Postgres `TestPostgresIntegerColumnsWidenToBigint` (legacy widen, foreign table untouched, 150s round-trip); operator note in ENTERPRISE_PLATFORM_RUNBOOK. Harness workaround removed. |
| **F1** | High | The binary accepts `APEXLOAD_CONTROLLER_HA=true` with the default SQLite store, and silently ignores a set `APEXLOAD_DATABASE_URL` unless `APEXLOAD_STORE=postgres`. Each replica then leases against its own file: **two concurrent leaders, both at generation 1**, and they fight over generators ("execution generation belongs to a different controller lease owner"). Helm rejects this configuration; raw manifests and the binary don't. | Harness run 1 (store unset) | **Done:** `validateHAStoreStartup` / `ignoredDatabaseURLWarning` in `cmd/controller`; verified live (controller exits naming `APEXLOAD_STORE=postgres`). |
| F3 | Medium | A generator's epoch-expiry safety stop (controller lost) logs nothing. Only the run-supervision expiry logs, and it never fires because the epoch expires first. | `g1.log`/`g2.log` contain only startup lines | **Done:** `generator_execution_epoch_expired` (and `generator_test_execution_lease_expired`) logged with test IDs and generation; seen in the post-fix harness run. |
| F5 | Medium | The reverse connector (`cmd/generator/reverse.go`) exits permanently on **any** 4xx, including the gateway's own **429** rate-limit response. Its backoff cap is 30s. | Code reading | **Done:** 408/429 retryable (`reverseEnrollmentRejectionIsPermanent`), backoff cap 15s. |
| F6 | Low | The generator gateway has no execution-leader check, so a reverse generator can enroll on a follower when routing is wrong. | `pkg/api/generator_gateway.go` | **Done:** gateway calls `requireExecutionLeader` (503, retryable). No leader hint: a follower knows the lease owner ID, not a routable URL; routing stays the active Service's job. |

### Next

- All six findings are fixed. The post-fix harness run (no workaround) creates
  the 150s test on a fresh Postgres, and both generators log the new expiry
  event naming the run.
- P1 is next. The harness is now workaround-free and serves as the P5
  regression baseline.

## 10. P1 results (2026-09-25): persistence, no behaviour change

What landed, and where it differs from the §5.1/§5.4 sketch:

| Piece | Implementation | Notes |
|---|---|---|
| Policy | `assignment.failover_policy` (`fail` \| `continue`, empty = fail) on `GeneratorAssignmentSettings` | The assignment already flows scenario → run → revision copies and persists in `assignment_json`, so the policy gets scenario defaults and per-run override with **no schema change**, following the `tolerate_generator_loss` precedent. Test-scoped: a group-level value is rejected. Swagger and `openapi.ts` regenerated (additive). |
| Checkpoint column | `tests.continuity_checkpoint_json`, schema release **v16** `ha-run-continuity-checkpoints` | Written **only** by `UpdateTestContinuityCheckpointIfLeaseHeld`: execution-lease fenced, and only an active run can be armed. `SaveTest` never touches it, so the whole-row save can't overwrite it. `TestRecord.ContinuityCheckpoint` is `json:"-"`. An empty value clears it in any status (P3 clears it on terminal). |
| Checkpoint content | `runContinuityCheckpoint` v1: generation, lease owner, **sealed supervision token**, per-group shard placement, armed-at | The token is wrapped with the controller key wrapper; AAD binds it to `test ID + generation`, so it can't be moved to another run or replayed from an older term. The plaintext token is never stored. |
| Arm point | `armRunContinuity` right after every generator accepts `ClaimRunSupervision` and before `CommitWork` | Never fails a run. It logs `run continuity not armed` with the reason when HA, the key wrapper, run supervision or checkpoint persistence is missing. |
| Preflight | warning `failover_continuity_unavailable` for a `continue` run, listing each reason it will still fail | While `runAdoptionImplemented = false` (until P3), the warning always states that adoption isn't implemented yet. That keeps P1 honest: arming happens, but a failover still fails the run. |
| Goal/auto-stop state | **Not persisted (decision)** | `goalControllerState` is only timing windows plus cumulative errors. After adoption the successor restarts those windows: at worst a scale or sustained-P99 auto-stop is delayed by one window. Error counts rebuild from the generators' cumulative metrics. |

Deferred to P3, since they only matter once adoption exists:
- Refresh the checkpoint when placement changes (scale, generator-loss
  evacuation).
- Clear it at terminal.

Tests:
- storage: `TestContinuityCheckpointIsLeaseFencedAndSurvivesFullSave`.
- api: failover-policy normalization; token seal bound to run and generation;
  preflight warning; arm persists a sealed checkpoint and recovers the exact
  token; arm without a wrapper leaves the run unarmed.
- The schema release test counts moved 15 → 16.

## 11. P2 results (2026-09-25): generator orphan grace and adoption

The protocol follows §5.3. Additions beyond the sketch: `RunAdoptionResult.terminal_state`, and an `UNSPECIFIED` enum zero value.

| Piece | Implementation |
|---|---|
| Opt-in | `WorkAssignment.adoption_grace`. Rejected (InvalidArgument) if negative, without a supervision token, or combined with `require_test_execution_lease`. *(Superseded 2026-09-26, §15: the combination is now supported.)* All groups of a run must request the same grace. |
| Cap | `APEXLOAD_GENERATOR_MAX_ADOPTION_GRACE`: default and hard maximum 120s. `0` disables orphaning on that generator. Invalid values stop generator startup. |
| Orphaning | `planExpiryLocked` → `orphanLocked`. When the controller epoch lapses, a committed, supervised, not-stopping run of that generation with grace > 0 keeps running instead of being fenced. Its supervision deadline is set to exactly `now + grace`, **shortening** a longer controller-granted lease, so the existing deadman enforces the grace. Logged as `generator_runs_orphaned`. |
| Timeout | An orphan still unadopted when the deadman fires stops `FAILED` with **`controller_adoption_timeout`** (`generator_run_adoption_timeout` event). The reason is valid in the crash journal. |
| Adoption | `ExecutionEpochClaim.adoptions`, applied in `commitPersistenceLocked` **before** the higher generation computes its lower-generation fence list, and applied even if the claim's context was cancelled after the generation became durable. Proof = prior generation + prior token, and the new token must differ. On success the assignment and supervision lease move to the new generation and token, the orphan mark clears, and the successor gets one supervision TTL to `ClaimRunSupervision` under the new token. Metrics follow automatically: batches take their generation from the live assignment. |
| Never adoptable | fail-policy runs (grace 0), staged (uncommitted) work, loss-tolerant runs, runs already stopping. A same-generation claim only **confirms** an adoption that already happened (idempotent retry after a lost ack). Any other run at a lower generation is already being fenced and is reported `REJECTED`. |
| Outcomes | `ADOPTED`, `ALREADY_COMPLETED`/`ALREADY_STOPPED` (from the retained terminal batch or supervision tombstone, with `terminal_state`), `REJECTED` (with detail), `UNKNOWN_TEST`. `adoption_supported=true` on every ack from new generators; older generators leave it false. |
| Client | `ClaimExecutionEpochWithAdoptions`, `ExecutionEpochResult.{AdoptionSupported, Adoptions}`, `Assignment.AdoptionGrace`. A zero grace is omitted from the wire, so fail-policy assignments are byte-identical. |

Tests (`run_adoption_test.go`, race-clean): each runs a real staged, supervised and committed run whose epoch lapses.
- adopt, then the rotated token supervises and the old token is dead, then the retry is idempotent;
- no successor → FAILED after the grace;
- a successor that doesn't adopt fences the orphan;
- a fail-policy run is fenced, not orphaned;
- wrong token, wrong generation or unknown run → rejected and fenced;
- claim-shape validation;
- grace validation and cap;
- env config;
- client round trip.

A negative control (orphaning disabled) fails the adoption and timeout tests.

**Constraints P3 must honour (found while building P2):**
1. **The successor's first claim of its generation on each generator must carry the adoptions.** The controller's generator epoch keeper begins claiming as soon as leadership is acquired. If that first claim goes out without adoptions, it fences every orphan. The keeper must wait for takeover reconciliation to build the adoption list, or be given it.
2. Before adopting, check the generator's `AdoptionSupported`. When it's false, the run can't be carried on that generator (partial adoption → evacuation or FAILED).
3. A run whose duration ended during the gap reports `ALREADY_COMPLETED`. Its terminal batch is retained under the **old** generation, so the successor must finalize it from the archive (`GetArchiveArtifact`), not by subscribing to metrics.
4. The generator archive manifest keeps the generation bound at start, so post-adoption archives name the prior generation. Harmless, but the report's failover section should say so.
5. The controller must send `adoption_grace` **only** once `runAdoptionImplemented` flips. Sending it earlier would let generators run up to 45s with no possible adopter.

## 12. P3 results (2026-09-25): controller takeover, **a run survives leader loss**

`runAdoptionImplemented` is now `true`. Controllers send `adoption_grace` = 45s
(D3) for supervised `continue` runs on an HA controller with a key wrapper.

### How takeover works (`pkg/api/run_adoption.go`)

1. **Reconcile keeps candidates.** `reconcileStoredActiveTest` no longer fails
   a RUNNING/PAUSED `continue` run that has a decodable checkpoint and a
   distributed backend. It leaves the run RUNNING in memory (the checkpoint is
   carried as `ManagedTest.continuityCheckpoint`).
2. **Phase A (`adoptOrphanedRuns`) runs inside acquisition reconcile, while
   admission is closed.** For each candidate it unseals the prior token and
   generates a rotated one. Adoptions are then **batched per generator**, and
   each generator receives **one** first claim of the new generation carrying
   all of its adoptions. This goes through `claimGeneratorExecutionEpochWithAdoptions`,
   which shares the lease-bounded transit and TTL rules with ordinary claims.
   - **Every generator adopts (or some completed):** the run is bound to the
     new term (execution context, fence, rotated token, clients, placement) and
     re-armed with a fresh checkpoint, so a **second failover** can adopt it again.
   - **Anything else** (unreachable, `REJECTED`, `UNKNOWN_TEST`,
     `ALREADY_STOPPED`, old generator): the run is finalized **FAILED** with a
     per-generator reason, through `completeDistributedTestRun`. That stops any
     generator that did adopt and collects a best-effort archive.
3. **Keeper gate.** When run adoption is enabled, the generator epoch keeper
   (`awaitRunAdoptionReconcile`) defers its first claim until reconcile
   finishes, bounded at 20s. `reconciling` is set in the same step that makes
   the replica leader, so the keeper can never observe leadership before
   reconcile starts. This closes the P2 constraint #1 trap.
4. **Phase B (`resumeAdoptedRuns`) runs first in `onAdmit`, before the active
   endpoint is published.** It claims supervision under the rotated token and
   starts the keeper, opens metrics streams on the new generation, installs the
   remote runtime (status and `StartedAt` untouched; goal windows restarted per
   §10), and starts metrics consumers and stage/profile drivers. The drivers
   recompute their position from the persisted run clock. If every generator
   finished during the gap, the run is finalized COMPLETE from the retained
   generator archives.
5. **Scale keeps the checkpoint current.** A successful distributed scale
   re-arms the checkpoint with the new placement.

### Bug found only end to end (fixed, with a regression test)

Running group executions on the generator keep their own copy of the
generation. Publication, snapshots and completion all require it to match the
assignment's. The first harness run adopted and resumed correctly, but the run
then went **silent**: no live or terminal metrics, stuck RUNNING. The P2 unit
tests missed it because their generator service had no `WithExecution()`, so
nothing executed. The fix: `adoptRunLocked` rebinds every group execution of
the run in the same critical section. The adoption tests now run real load
against a local HTTP server and require a live generation-2 batch after
adoption. A negative control confirms the test catches the bug.

### End-to-end evidence (`scripts/ha-failover-harness.sh`, Postgres, `kill -9` of the leader at +45s)

| | `continue` + failover | control: `continue`, no failover |
|---|---|---|
| Outcome | **COMPLETE** at +154.7s (150s run) | COMPLETE |
| Load at target | **continuous for all 151s**; 31–36 req/s through the kill (+40…+60s); never dropped | continuous |
| Requests seen by target | 4,971 | 4,897 |
| Archive transactions | 11,700 | 11,680 (**+0.2%**, within the ±1% criterion) |
| Takeover | orphaned at epoch expiry → lease acquired → adopted **~60 ms** after acquisition → resumed 20 ms later | — |

### §7 acceptance status: all ten exercised end to end (harness, Postgres, real processes)

| # | Criterion | Harness invocation | Result |
|---|---|---|---|
| 1 | `fail` policy unchanged | `FAILOVER_POLICY=fail` | ✅ no checkpoint; FAILED "controller restarted before the run completed", as before |
| 2 | Simple shape survives; archive ±1% | default | ✅ COMPLETE; load never dropped (151s); archive +0.2% vs the no-failover control |
| 3 | Stage-driven run resumes its schedule | `STAGES_JSON` (holds 2→6→3 VU/group), `KILL_AT=50` | ✅ **after a fix** (below): 6 VU held through the gap, stepped to 3 VU at +102s (the scheduled +42s+60s boundary), COMPLETE |
| 4 | Duration ends during the gap | `DURATION=60s KILL_AT=50` | ✅ **after a fix** (below): generators finished at +62s with no controller; successor adopted with 0 adopted / 2 completed generators and finalized COMPLETE from the retained archives |
| 5 | No successor → stop within grace | generator test | ✅ `TestOrphanStopsWithAdoptionTimeout…` |
| 6 | Stop an adopted run | `KILL_AT=40 STOP_AT=100` | ✅ stop accepted by the new leader (HTTP 200); load at the target went to 0 within 1s; ABORTED with an archive (the codebase's manual-stop outcome) |
| 7 | Double failover | `KILL_AT=40 RESTART_KILLED_AFTER=40 SECOND_KILL_AT=110 DURATION=200s` | ✅ generation 1→2→3; load continuous for 201s; COMPLETE; archive matches the 150s runs scaled to 200s |
| 8 | Split brain (paused, not dead, leader) | `SUSPEND_LEADER=1 RESUME_AFTER=55` | ✅ the thawed old leader found its lease expired; its stale-runtime teardown was **refused by the generators** (fenced generation); the run was unaffected (COMPLETE, same archive count as #2) |
| 9 | Wrong token/generation rejected; no plaintext token | tests | ✅ generator and controller tests |
| 10 | Mixed fleet (old generators) | `GENERATOR_BIN=<generator built from HEAD>` | ✅ FAILED with "generator predates run adoption" per generator |

### Bugs the end-to-end scenarios found (all fixed, with regression coverage)

1. **Adopted run went silent** (§12, above): generator per-execution generation copies.
2. **Takeover panicked when every generator had already finished** ("assignment to entry in nil map": `cloneClientMap` returns nil for an empty map). Worse, a panic in the acquisition callback made the replica **drop and re-take leadership every 10s indefinitely**. Fixes:
   - build the client map explicitly (`TestPhaseAInstallsAdoptedAndCompletedRuns`, with a negative control);
   - **`isolateRunAdoption`**: a panic while adopting one run now fails only that run, and takeover continues.
3. **The stage schedule restarted from stage 0 after adoption.** The resumed driver's readiness probe also rescaled to the initial VUsers. Fix: a resume mode (`ManagedTest.stageScheduleResume`) that skips the probe, fast-forwards completed stages against the persisted run clock (`testActiveRuntimeLocked`), and runs the current stage for its remainder only. Traffic profiles already jump to the active point; they get the same probe bypass so they don't dip.

### Still open
- ~~STOPPING runs are still failed on takeover.~~ **Fixed 2026-09-26 (§14).**
- The checkpoint is not cleared at terminal. It's harmless: the token is sealed and useless once the run ends. The history it holds is the source for the API and report after the run.
- While takeover reconcile runs (tens of ms), API mutations get the existing 503 "not the active execution leader". Plan §5.6's queued-stop idea was not needed: Phase A installs stop-capable handles before admission opens (verified by #6).

## 13. P4 results (2026-09-26): surface

| Surface | What | Verified |
|---|---|---|
| API | `ManagedTest.continuity` = `RunContinuityStatus{policy, armed, execution_generation, failovers[]}`, derived on read from the policy and the persisted checkpoint; never exposes the sealed token. Each `RunFailover` records prior→new generation, `adopted_at`, `last_observed_at` (newest persisted live sample before the gap), `gap_seconds`, and adopted/finished generator counts. History is carried forward on every re-arm, so it survives further failovers and outlives the run. Swagger and `openapi.ts` regenerated (additive). | Harness: `continuity` shows 1 failover, gap 24.4s / 32.2s across runs; unit test asserts no token leak |
| Dashboard: authoring | "Continue through controller failover" option card at scenario scope, beside "Tolerate generator loss". The two are **mutually exclusive** (each disables the other, matching the backend). The value is preserved through placement-mode changes and every root payload builder (scenario upsert, form model, draft clone); `fail` is omitted from payloads so existing ones are unchanged. Validation rejects it at group scope and in combination with loss tolerance. | vitest: toggle, exclusivity, mode switch, group scope hidden, payload preservation, validation |
| Dashboard: run page | "Controller failover" fact in *Infrastructure evidence* ("Continue through failover (armed) · survived N failovers"), plus a failover table (adopted at, generation, unobserved gap, generators adopted / finished during the gap). | vitest |
| Report | New registry section `controller_failover` (HTML, PDF, CSV; DOCX via HTML), included in the **engineer** and **audit** profiles next to *Completeness*. It states that measured results are complete, that controller overlays are absent for each gap, and that controller-driven shapes held during it. "No controller failover occurred" otherwise. Data travels in `RunArchiveManifest.continuity`, written only for runs that failed over. | Coverage-matrix test; dedicated render test; **harness: real report HTML and CSV of a failover run carry the section** |
| Baseline comparison | A run that failed over gets an evidence warning: "Survived N controller failover(s) with X s unobserved … stage, traffic-profile and goal control held their last value during each gap". | unit test |

The launch-preflight warning (§10) already covers the case where `continue` can't be honoured.

## 14. In-flight stop across failover (2026-09-26)

A `continue` run that was **STOPPING** when the leader died now ends the way the operator asked. Before, it ended FAILED ("controller restarted").

- STOPPING runs are adoption candidates (`runAdoptionStatus`).
- A generator that never received the stop (it reports `ADOPTED`) is taken over and then stopped under the rotated token.
- Generators that already stopped or finished are marked terminal. They aren't re-stopped (previously this logged generation-mismatch errors) and they serve the archive.
- The run ends **ABORTED**, the outcome of an ordinary stop, with its archive.
- Fail-policy STOPPING runs still end FAILED.

**Structural fix found end to end:** outcomes Phase A decides (FAILED for an unadoptable run, ABORTED for an in-flight stop) are now bound in Phase A but **applied in Phase B** (`adoptedRunResume.finishStatus`). Archive publication needs an admitted leader: during reconcile it was deferred to the retry outbox, so the run showed no archive at first.

Evidence:
- Harness `STOP_AT=45 KILL_AT=45` (stop in flight, leader killed in the same second): ABORTED with a 3,260-transaction archive and no warnings.
- Tests: `TestStoppingRunIsFinishedAsAbortedAfterFailover` (generator still running → adopted and token-fenced stop; generator already stopped), with a negative control, and `TestFailPolicyStoppingRunStillFails`.
- The basic failover scenario re-run afterwards: COMPLETE, 11,720 transactions.

The harness now keeps every archive under its run directory (`APEXLOAD_ARCHIVE_DIR`). Earlier runs had written generator archives into the user cache.

## 15. `continue` + `tolerate_generator_loss` (2026-09-26)

**Decision (user):** support the combination; **a generator lost while no controller is leading fails the run.** It is not evacuated: evacuation needs a leader that watched the loss. Generator loss while a leader is present keeps the ordinary evacuation behaviour.

**How it works:**
- A loss-tolerant run also holds a per-run lease on each generator, and a live controller deliberately lets one generator's lease lapse to fence it during evacuation. The generator must therefore tell controller loss from a deliberate fence when that lease lapses:
  - **`controllerSilentLocked`:** orphan only if the controller hasn't renewed the process epoch for at least half its last granted TTL, or the epoch is within 5s of lapsing. A live controller renews at most every 5s; a dead one has been silent for most of the TTL. Deadline proximity alone was not enough: run and process leases are renewed on different schedules, and end to end the run lease lapsed 17.6s after the kill, with the epoch more than 5s from expiry. That first attempt stopped the run instead of orphaning it.
  - Orphaning fences the old per-run lease, so neither a late timer nor the dead term can act on it.
- Adoption accepts loss-tolerant runs. In Phase B the successor re-establishes each generator's per-run lease before claiming supervision (the generator requires it), then hands the run to the ordinary per-run renewal loop, which also drives evacuation from then on.
- A generator unreachable at takeover fails the run with an explicit reason: "generator-loss tolerance does not evacuate a generator lost while no controller was leading". Adopted generators are stopped under the rotated token.
- The dashboard no longer makes the two options exclusive. With both selected, the card copy states the lost-during-gap rule.

**Evidence:**

| Check | Result |
|---|---|
| Harness `TOLERATE_LOSS=1` (leader killed) | run lease lapsed "with its controller" → orphaned; epoch expired 10s later; adopted; **COMPLETE**, load continuous for 151s, archive 11,700 (same as #2) |
| Harness `TOLERATE_LOSS=1 KILL_GENERATOR_AT=50` (generator killed during the gap) | **FAILED** with the explicit reason; the surviving generator stopped at adoption. No archive attached: the FAILED path collects archives best-effort, and a generator stopped at that moment hasn't finalized its archive yet. That is existing behaviour for any distributed FAILED run. |
| Generator tests | orphaned-and-adopted on controller death; **deliberate fence still stops**; realistic timing (run lease lapses 5.5s before the epoch) orphans; negative controls: always-orphan fails the fence test, window-only fails the realistic-timing test |
| Controller test | real-generator Phase A+B for a loss-tolerant run; without the Phase B lease re-establishment the run FAILs (negative control) |
