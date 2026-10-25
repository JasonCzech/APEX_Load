# Evacuation roll-forward (REC-A)

Status: implemented behind an opt-in. Qualified locally only. Thirteen review rounds; round 13 found no High or Medium issue, and its three Low items are recorded below, not fixed.

## Contract

- **Opt-in.**
  - `APEXLOAD_EVACUATION_ROLLFORWARD_RECOVERY` (`WithEvacuationRollForwardRecovery`) is off by default.
  - It takes effect only together with `APEXLOAD_EVACUATION_ROLLBACK_RECOVERY` and run adoption.
  - With both settings on, a new evacuation journal is version 5, a superset of version 4:
    - `markRunEvacuationStaged` also records the duration offset, active duration and pause state AssignWork staged each replacement with (`StagedSteadyElapsed`, `StagedRemaining`, `StagedPaused`);
    - a new phase, `replacement_committing`, is written immediately before `CommitWork`. It records `CommittingAt` and `CommitTargetDuration`, the logical duration the replacement ends up with: what the leader sends in `AdjustDuration` right after `CommitWork`, or the staged duration when it sends none. The mark refuses a target longer than the staged duration;
    - `replacement_committed` advances from `replacement_committing`. Versions 3 and 4 still advance from `source_fenced`.
  - Otherwise journals stay version 4 or 3, exactly as before. Their JSON gains no fields.
- **Older peers.**
  - A controller that knows only versions 3 and 4 cannot read a version 5 journal: `decodeRunContinuityCheckpoint` rejects it, and the older evacuation reader does not recognize version 5.
  - Such a controller keeps the run `STOPPING` through its interrupted distributed stop path.
  - Enable the setting only once every replica reads version 5.
- **Commit intent before `CommitWork`.** If the `replacement_committing` write fails, `CommitWork` is never sent and the existing uncertain path stops the evacuation's participants.
- **Eligibility** (`evacuationRollForwardGuard`, `runEvacuationRollForwardPlan`). A run qualifies only when:
  - both settings are on and run adoption is enabled;
  - the run is `RUNNING` or `PAUSED`, with the continue failover policy, on the distributed backend;
  - the run's durable pause state equals `StagedPaused`;
  - the journal passes `interruptedEvacuationTargets`, is version 5, is in phase `replacement_committing` or `rolling_forward`, and is not abandoned;
  - the predecessor's lineage was armed (`Prior.ArmedAt`) and the source is not a prior completed target, as for a committed promotion;
  - no surviving participant's shards changed (`runEvacuationSurvivorsUnchanged`, shared with the rollback plan);
  - the capacity journal passes `validateRunEvacuationCapacityJournal`;
  - `0 < CommitTargetDuration ≤ StagedSteadyElapsed + StagedRemaining`;
  - each evacuated group is still part of the run and passes the workload refusals of `evacuateGenerator`: no iteration termination, no distributed rendezvous, `groupAllowsGeneratorLossReshard` and `groupAllowsStatelessDataReshard`.

  Anything else keeps the fail-closed path, including `source_fenced` journals of every version. A version 5 journal held at `source_fenced` is reconciled `STOPPING` with a reason that says the replacement never received `CommitWork`.
- **Successor roll-forward** (Phase A, `adoptOrphanedRuns`), in order:
  1. The adoption checkpoint is the predecessor's lineage over the next topology, with the fenced source kept for archive collection only, as a committed promotion builds it (`runEvacuationRollForwardCheckpoint`). A `rolling_forward` journal supplies it with the previous successor's intent.
  2. `persistRunEvacuationRollForwardIntent` writes phase `rolling_forward` through the lease-held compare-and-swap before any generator sees the rotated token. It records `RollForwardGeneration`, `RollForwardOwnerID`, `RollForwardStartedAt` and the planned checkpoint (`RollForwardCheckpoint`). `Prior` is unchanged. A failed write returns an error, so Phase A is retried.
  3. The batched epoch claim carries the adoption, with the prior proof chain, to the survivors and the replacement. The source is not claimed: it is durably fenced.
  4. Every replacement must be adopted. `REJECTED` (staged work), `UNKNOWN_TEST`, `ALREADY_STOPPED`, `ALREADY_COMPLETED` and an unreachable replacement fail the roll-forward. The claim that refused the adoption drained a staged assignment. `ALREADY_COMPLETED` is no proof of a commit: a generator ends any stop, including a stop of a staged assignment that never committed, with a completed run, as the rollback already assumes for a completed source. No participant may report a completed run the prior lineage never recorded (`unexpectedRunEvacuationCompletions`, shared with the rollback). A survivor stopped by the lost leader's uncertain path, or by a failed earlier successor, ends the same way, so its completion cannot show its share ran out. Completions recorded in `Prior.CompletedTargets` before the evacuation are trusted as before.
  5. `completeRunEvacuationRollForward` sends `AdjustDuration` with `CommitTargetDuration`, the successor's generation and the rotated token, to each replacement, and requires `Accepted`. The target is absolute, so a repeat is a no-op and an elapsed one ends the replacement's engines at once.
  6. `commitRunEvacuationRollForwardCapacity` checks each journaled replacement reservation against the journal, ignoring only state and owner term. A `pending` row is confirmed by the adopted replacement at the new epoch and then committed under the successor's lease. A row already committed, by the predecessor or by an earlier successor term, is left as it is. `adoptPodCapacity` then confirms and transfers every live and standby reservation of the run.
  7. Only then is the journal replaced by the plain checkpoint, and `installAdoptedRun` resumes the run on the next topology.
- **Failures stay fail-closed.**
  - Any problem before the journal is replaced goes to `failRunEvacuationRollForward`, which shares `failRunEvacuationRecovery` with the rollback:
    - it keeps the `rolling_forward` journal;
    - it first holds the run `STOPPING` with the evacuation-uncertain reason, and installs no runtime, so an administrator can attest;
    - only then does it force-stop each survivor and replacement this term adopted, with the rotated token. If the hold write fails, it is retried in the background and the stops are still sent. A stop that is not acknowledged is added to the held reason through the same hold.
  - Holding first matters because a stopped assignment later reports a completed run: a durable `RUNNING` row must never outlive the stops. If the controller is lost after the hold, a newer term fences what it adopted. This ordering, the one `failClosedUncertainEvacuation` uses, applies to the rollback too.
  - A panic before the swap is handled the same way. A panic while handling a failure is caught by `isolateRunAdoption`, which holds a journaled run. A panic after the swap takes the ordinary adoption-failure path.
  - That term's fail-closed recovery does not fence at or below `RollForwardGeneration`; `successorGeneration` covers both successor paths. A newer term fences the survivors and the replacement, never the durably fenced source, and records the run `FAILED`.
- **The hold and its retry** (`interruptedRunEvacuationHold`).
  - Each run has one hold retry, owned by the hold registry (`TestRunner.evacuationHolds`, `evacuationHoldRetrying`) rather than the terminal-transition retry. It writes the run's latest registered hold until none is left, so neither a later reason of the same hold nor a later current hold for the run, such as one from `isolateRunAdoption`, is lost. Starting and ending the retry are both decided under the registry lock, so a hold registered while a retry ends always gets one.
  - A hold is unregistered once its latest reason is written, once the run is terminal (for example after an administrator attestation) or its journal is gone, when the run is removed, and when the retry ends on shutdown. The retry never loops on a write whose term has ended or whose run is terminal; a claim refused in a current term is retried (see Remaining).
  - The retry does not wait for admission. Every hold is decided inside acquisition reconcile (Phase A), before admission; the terminal-transition retry, which needs admission, exited there before its first attempt.
  - Each hold carries the term passed by the code that decided it, and its single term gate is the fence claim with that term (`claimTestExecutionFenceWithTerm`). A refused claim drops the hold only when that term is no longer this process's term; the next term then decides the run from its journal, and a participant this term stopped reports a completion its prior lineage never recorded, which holds the run. A claim refused while the term is still current, for example by another owner's fence, keeps the hold registered and retries it.
  - Every hold of a run writes under the run's one write lock (`evacuationHoldWriteLock`), shared by all its hold objects, which spans reading the reason and writing it, and writes only while it is still the run's latest registered hold. A retry that read an earlier reason, or a write of a hold a later hold replaced, therefore cannot land after the later write.
  - A hold of an ended term, or of a run object that is no longer registered, never displaces a registered hold of the run (`register`): it could not be written, and a current hold it displaced would then write nothing. It is dropped and logged. Such a hold registers only when the run has no hold, and is then dropped at its own term and object gates.
- **Lease lost and won again by the same process.**
  - **Reload replaces lost-term objects.** A reload replaces any active run object that has no live runtime and is still fenced to a lease term this process lost (`lostTermExecutionFence`), instead of reusing it, and restores its runtime as for any new object. Teardown after a lost lease leaves such objects behind, and a goroutine of the lost term may still hold one; it must never write under the new term once the new term claims the run. The fresh object is claimed normally by Phase A, a hold, the fence recovery or an attestation, while the terminal writers require the registered object for the row, so a lost-term writer writes no row. A terminal run needs no fence and keeps its object, so a lease change does not re-hydrate every finished run.
  - **Phase A clearing.** Phase A also clears a stale fence on every run it selects, which has no live runtime, and leaves the fence empty while it claims the generators without holding the runner lock; the first hold or install then claims the term. Unlike `installRunAdoptionHandlesLocked`, it claims nothing itself. Because the reload before Phase A already replaced lost-term objects, no lost-term goroutine holds a selected object; the clearing only matters for an object that did not pass through that reload. Any other caller of `claimTestExecutionFence` during that window is an API or admission-time operation, and the replica is not admitted during Phase A.
  - **Reload keeps a newer hold.** A reload that read the row before a hold wrote `STOPPING` keeps the newer in-memory hold when the row is `RUNNING` or `PAUSED` under the same journal, as it already does for a pending stop proof, so the run stays attestable. Only a hold that `persist` wrote in the current term qualifies (`evacuationHoldOwner`, stamped after the durable write). A `STOPPING` set in memory without such a write, for example by an uncertain evacuation whose save failed, follows the row. In its own term that uncertain path keeps its runtime handles, which the existing pending-stop-proof rule covers, so a run object like this is only met after a lease change, where the next term's Phase A decides it.
- **Live-table finalization** (`finishLiveTables` for terminal writers, `finalizeRecoveredLiveTables` for tables a restore found already closing). Live tables are keyed by run ID, so a writer of another term must never finalize them. In order:
  1. **Owner check** (`liveTablesFinalizableBy`, terminal writers only). A writer with a fence finalizes only tables whose term owner equals that fence; this is what lets a writer whose object a same-term reload replaced still finalize them, since no other writer will. A writer with no fence must hold the registered object.
  2. **Ended term gives up.** Tables whose term is no longer this process's term can never be finalized here again, so the finalizer (`liveTableFinalizer`) returns at once, and so does any retry (the recovery's first attempt instead fails at `finalizeRunLiveTables`' term check, and its retry ends). It also returns once the run's tables were replaced.
  3. **Finalize the checked tables** (`finalizeRunLiveTables`), never whatever a later lookup by run ID finds, so tables a newer term restores meanwhile are never touched.
  4. **Retry until replaced.** A failure is retried under a key of run and term owner (`liveTableRetryKey`), so a retry left by an earlier term never takes the current term's slot. The retry repeats steps 2 and 3.

  The owner check covers every terminal writer, including local runs' artifact finalization. A recovery retry queued during Phase A usually exits before its first attempt, because the terminal-transition retry needs admission; that predates this slice.
- **A new term after `rolling_forward`** rolls forward again from the recorded intent. Its adoption carries the earlier term's token proof, the duration adjustment is repeated, and reservations the earlier term committed or transferred are accepted.
- **Rollback accepts version 5.** `runEvacuationRollbackPlan`, the same-leader journal restore and the abandoned marker accept version 4 or 5 journals in the phases they did before. The shared failure handling, the unrecorded-completion rule, the hold and its retry, and the reload rules above apply to rollback holds too.
- **No storage change.** `TransitionPodCapacity` changes only a row's state, never its owner term (`pkg/storage/pod_capacity.go`). A pending replacement row committed by the successor therefore keeps the predecessor's term, and the existing `AdoptPodCapacity` transfers it. No current-term exemption was needed.

## Invariants

- **Old and replacement never run concurrently.** The source was positively fenced before `replacement_committing` was written, it is never adopted, and it is excluded from every later participant set. A late predecessor write fails the lease-held compare-and-swap, and generators refuse its generation and token once the successor adopted the run.
- **No replayed initialization.** Adoption moves existing executions only. A replacement that never committed is drained, not restarted.
- **No resurrected stopped run.** A `STOPPING` run is not eligible.
- **No extended duration.** The successor sends the journaled target, which never exceeds the staged duration, before the journal is replaced.
- **Pause is preserved.** The durable pause state must equal the staged one.

## Validation

- **`TestEvacuationJournalVersionFiveFollowsRollForwardRecovery`** (`pkg/api`): version 3 with both settings off and with roll-forward alone, version 4 with rollback alone, version 5 with both. Only version 5 records the staged timing and the commit intent; older journals carry none of the new fields and skip the intent. The intent refuses a zero or longer target, and version 5 cannot be committed without it. `decodeRunContinuityCheckpoint` rejects the journal. That an older release's evacuation reader does not recognize version 5 follows from its fixed version list and is not tested here.
- **`TestVersionFiveJournalKeepsRollbackBehavior`**: a version 5 `prepared` or `staged` journal is rolled back by a successor, and its leader marks it abandoned and restores the prior checkpoint, as for version 4.
- **`TestEvacuationRollForwardAdoptsCommittedReplacement`** covers an ordinary replacement and an activated standby, a pending and a committed reservation, and a running and a paused run:
  - reconcile leaves the status unchanged, and so does the roll-forward;
  - the survivor and the replacement are adopted from the old term, and the source is neither claimed nor a participant but keeps its archive transport;
  - the replacement receives one `AdjustDuration` with the journaled target, the new term and the rotated token; the survivor's duration is not touched;
  - the durable checkpoint is the next topology under the new term, with the source archived;
  - the replacement and survivor capacity is committed under the new term, and the source's `releasing` row is unchanged;
  - a pending replacement row is confirmed before it is committed and again when transferred; a committed one only when transferred.
- **`TestEvacuationRollForwardHoldsUncommittedReplacement`**: `REJECTED`, `UNKNOWN_TEST`, `ALREADY_STOPPED` and an unreachable replacement keep the `rolling_forward` journal and hold the run `STOPPING` for attestation; the adopted survivor is force-stopped at the new term with the rotated token, the replacement receives no duration or capacity request, and its reservation stays pending under the old term.
- **`TestEvacuationRollForwardHoldsCompletedReplacement`** covers both ways a never-committed or stopped replacement reports a completed run:
  - the predecessor's uncertain path stopped the still-staged replacement after its commit intent: the run is held, the replacement gets no duration or capacity request, and its reservation stays pending;
  - a first successor failed after adopting, could not write its hold, and still stopped everything it adopted, leaving a `RUNNING` row under `rolling_forward`: the next term sees every target completed and holds the run instead of finishing it.
- **`TestEvacuationRecoveryHoldsUnrecordedCompletion`**, for roll-forward and rollback:
  - a survivor the lost leader stopped reports a completed run while the other participant is adopted: the run is held, and the adopted participant is force-stopped with the rotated token;
  - a first successor fails, cannot write its hold, stops the survivor and loses the other participant's stop: the next term holds the run instead of continuing without the survivor's shard;
  - a survivor recorded completed before the evacuation is trusted, and the run rolls forward or back as before.
- **`TestInterruptedRunEvacuationHoldRetry`**:
  - when both hold writes failed, the queued retry writes the later reason with the unacknowledged stop;
  - when a hold of an ended term occupies the run's retry slot, the retry writes the newer term's hold instead;
  - an ended term's hold alone is dropped, not written;
  - a hold that failed during acquisition reconcile, before admission, is written once the store recovers, while the replica is still reconciling;
  - when a later hold of the run is made while the retry writes an earlier hold, the later hold waits for the run's write lock and its reason ends durable and in memory;
  - a write of a hold that a later, already written hold replaced writes nothing;
  - a stale hold made after the current term's failed hold, with an ended term, the run's old object, or both, does not displace it, and the current reason ends durable and in memory;
  - when the hold gains a later reason while the retry writes an earlier one, the retry goes on until it has written the later one;
  - a claim refused while the term is current, by another owner's fence, keeps the hold registered and the run `RUNNING` until the fence is released, then writes it;
  - a retry ends once the run is terminal, its journal is gone, its hold was forgotten (`forgetEvacuationHold`) or the runner is shutting down, even while writes still fail.
- **`TestFailedEvacuationRecoveryHoldsBeforeStopping`**: for a failed roll-forward and a failed rollback, the durable status is already `STOPPING` when the first force stop is sent. Its stale-fence cases set the fence on the fixture's object directly; in production the reload replaces such an object first, so these cases cover the defensive clearing in Phase A. Moving the hold after the stops fails only this test.
- **`TestReloadKeepsANewerEvacuationHold`**:
  - a reload whose listing still shows the held run `RUNNING` or `PAUSED` under a roll-forward journal, or `RUNNING` under a rollback journal, each a candidate itself, keeps the in-memory `STOPPING` hold of this term on the same object, and an attestation then succeeds;
  - a row under another journal wins;
  - a `STOPPING` never written in this term, as left by an uncertain evacuation whose save failed, follows the row;
  - so does that `STOPPING` with a hold whose own write failed: the hold stamps the run only after its durable write;
  - a terminal run object fenced to a lost term is reused, not replaced;
  - the same left by a lost term is replaced with the row's `RUNNING` run, restored with its runtime (group specifications from the stored compiled scripts), and Phase A then holds it, because the participants report completions, and an attestation succeeds.
- **`TestLostTermRunRollsForwardAfterReloadAndReconcile`**: in production order, `reloadAndReconcile` (with its active-only reload) replaces an object fenced to a lost term, restores its runtime, and the run then rolls forward normally.
- **`TestTerminalWriterFinalizesOnlyItsTermsLiveTables`**:
  - a run whose object a same-term reload replaced still finalizes its live tables on its own terminal write;
  - a writer of an ended term neither finalizes its tables nor leaves a retry, and leaves tables a newer term restored untouched;
  - when a newer term takes the lease and restores the run's tables between the ownership check and the finalization (a test barrier, `afterLiveTableFinalizeCheck`), neither the checked tables of the ended term nor the newer term's are finalized, and the ended term's retry ends;
  - a retry of an earlier term holding its own slot does not drop the current term's retry, and the current retry ends once the tables are replaced. The test first asserts that the two terms' keys differ.
- **`TestRecoveredLiveTablesFinalization`**: a failed recovery finalization is retried under its term key and ends without touching the tables that replaced it. For an ended term, the first recovery attempt fails with "owner expired" and leaves the tables open, and its retry ends instead of failing on the expired owner every second.
- **`TestHeldRunAfterTheLeaseIsWonAgain`**: a hold written in one term, after the runner gets a new leadership object for a newer term (per-term fence tokens, as a lease won again) and reloads, is attested, or fenced by the newer term's recovery, which records the run `FAILED`. A lost-term writer still holding the old object changes neither the run's row nor its live tables, and the attested run's own terminal write then finalizes them.
- **`TestEvacuationRollForwardKeepsFailClosedPaths`**: version 3, 4 and 5 `source_fenced` journals, an abandoned journal, `STOPPING`, the fail policy, either setting off, a pause or resume since staging, a stateful-protocol, distributed-rendezvous, cursor-stateful-data or unknown evacuated group, and iteration termination. None is a candidate; each reconciles `STOPPING` with its journal unchanged and no generator claim. The version 5 `source_fenced` reason says the replacement never committed.
- **`TestEvacuationRollForwardPlanRejectsUnsafeJournals`**: version 4, a journal without a commit intent, an abandoned journal, an unarmed lineage, a completed source, a changed survivor, an invalid capacity claim, and a missing, longer, zero-remainder or negative-offset duration.
- **`TestRollingForwardJournalRejectsContradictions`**: a `replacement_committing` journal's participants exclude the source. Each contradiction in a `replacement_committing` or `rolling_forward` journal is refused by both the fail-closed recovery and the roll-forward plan: the version, the commit intent, the source fence, a replacement commit, the staged targets, a successor term in the wrong phase, the roll-forward term, owner and start, a rollback term, and the planned checkpoint's topology, archived source, lineage owner and term, token and newest intent. A `rolling_forward` journal is never rolled back.
- **`TestEvacuationRollForwardInterruptionBoundaries`**: a controller lost before `CommitWork` (the replacement is still staged) is held; one lost after `CommitWork` and before the capacity commit, after the capacity commit and before `AdjustDuration`, or after `AdjustDuration` and before the committed mark is rolled forward, repeating the adjustment where it had already been applied.
- **`TestEvacuationRollForwardFailuresKeepJournal`**: a rejected duration adjustment, an unconfirmed capacity request, a changed replacement row, a pending row from another term, a committed row from an older term than the journal's, a failed journal swap and a panic before the swap each keep the journal and hold the run `STOPPING`, with the adopted survivor and replacement force-stopped. Only the failed swap happens after the capacity was transferred.
- **`TestCompleteRunEvacuationRollForwardRequiresTheReplacement`**: the completion step itself refuses a replacement this term did not adopt and leaves the journal and the capacity unchanged.
- **`TestCommitRunEvacuationRollForwardCapacityRefusesUnprovableRows`**: a replacement holding a reservation the journal never recorded, a replacement that cannot confirm capacity, a missing capacity ledger and a lost reservation are each refused before any capacity request.
- **`TestEvacuationRollForwardUnusableNextCheckpointHoldsRun`**: a next checkpoint whose adoption intent cannot be planned holds the run `STOPPING` with its journal unchanged, and no generator is claimed.
- **`TestRollingForwardJournalWithTheSettingOffIsHeld`**: a `rolling_forward` journal reaching a term with the setting off is no candidate, reconciles `STOPPING`, and that term fences the survivor and the replacement, never the source, and records the run `FAILED`.
- **`TestVersionFiveCommittedEvacuationIsPromoted`**: a version 5 `replacement_committed` journal is promoted and its survivor and replacement adopted, with no further duration change, as for versions 3 and 4.
- **`TestEvacuationRollForwardIntentWriteFailureRetries`**: a failed `rolling_forward` write returns an error before any generator is claimed and leaves the journal and the `RUNNING` row unchanged; the same term's retry rolls forward.
- **`TestEvacuationRollForwardFailurePanicHoldsRun`**: a panic while force-stopping after a failed roll-forward still holds the run `STOPPING` under its `rolling_forward` journal.
- **`TestEvacuationRollForwardPanicAfterSwapIsOrdinaryFailure`**: a panic after the journal was replaced takes the ordinary adoption-failure path, with no evacuation-uncertain hold and no force stop.
- **`TestEvacuationRollForwardResumesAfterAnotherLeaderChange`**: a successor lost right after its intent, or after its claim, duration adjustment and capacity transfer, leaves the run `RUNNING`. The next term rolls it forward with the first term's token proof, repeats the adjustment, and ends with the capacity under the newer term.
- **`TestFailedRollForwardIsNotFencedInItsTerm`**: no same-term recovery is queued, and a newer term fences the survivor and the replacement, never the source, and records the run `FAILED`.
- **`TestEvacuationRollForwardRefusesLateOldOwner`**: inside the window between the successor's `rolling_forward` intent and its journal swap (during the epoch claim), and again after the roll-forward, the predecessor's committed mark, a raw journal write under its lease and a capacity commit under its lease all fail, and nothing changes. The committed mark is refused because the journal changed; the raw write and the capacity commit are refused by the lease.
- **`TestEvacuationRollForwardAgainstRealGenerator`** (in-process generator service for the replacement):
  - a staged replacement is refused, drained by the epoch claim, and no longer accepts a duration change;
  - a staged replacement the predecessor stopped reports a completed run to the successor's claim, and the run is held;
  - a committed replacement is adopted, refuses the predecessor's generation and token, keeps running, and accepts the journaled duration again with the rotated token;
  - a loss-tolerant replacement orphaned by a lapsed epoch, whose per-run lease the generator cleared, is rolled forward the same way: the duration adjustment does not need that lease. The old epoch's TTL (2 s) covers the setup, and the test then polls until the generator refuses the old generation as expired instead of sleeping. The orphan transition itself is not observed directly.
- **`TestEvacuationRollForwardExpiredDeadlineCompletes`** (in-process generator): a journaled target that already elapsed for the replacement still rolls forward, and the replacement's engines end at once.
- **`TestDistributedEvacuationRollForwardJournal`** drives a real version 5 evacuation through `evacuateGenerator` against an in-process generator:
  - `CommitWork` and the duration adjustment observe a durable `replacement_committing` journal with the paused staged timing, and the adjustment equals the journaled target;
  - a failed `replacement_committing` write never sends `CommitWork`;
  - a failed committed mark after `CommitWork` takes the uncertain path.
- **Existing tests** keep passing: the rollback suite's same-term check now matches the shared "recovery generation" wording, and its force stops are now checked for the rotated token.
- **Mutation passes.** Twenty mutations were each caught after round 1:
  - the first pass removed eleven checks (pause, workload, capacity confirmation, armed lineage, row identity, replacement adoption, capacity transfer, pending row term, planned checkpoint validation, the shared successor term, the intent's duration bound). The two that first survived gained `TestCompleteRunEvacuationRollForwardRequiresTheReplacement` and the "pending row from another term" case;
  - after round 1, those eleven were re-run, plus the rendezvous and data refusals, the unrecorded and lost reservation checks, the next-checkpoint hold, the completed-replacement refusal, the committed-row lower bound, removal of the hold, and moving the hold after the stops.

  Not mutated separately: the error branch of the rendezvous inspection, and the version check in `runEvacuationReplacementNeverCommitted`.

  After round 2, six more mutations, applied with `go test -overlay` rather than by editing source, were each caught: the unrecorded-completion check in the roll-forward and in the rollback, the hold retry's terminal check, its journal check, a second hold that does not share the first, and the roll-forward's not-adopted check.

  After round 3, three more were caught: a retry that writes its own hold rather than the run's latest, a term check that needs both owner and generation to change, and the not-adopted check again. The per-hold write lock is not covered by a test: the interleaving it prevents is not reproducible without a scheduling hook (covered since round 11, when the per-run write lock replaced it; see below).

  After round 4, four more were caught: a retry that waits for admission, a retry that ends after writing an earlier reason, dropping both the term check and the term-bound fence claim, and the not-adopted check. `completeRunEvacuationRollForward` now never sends a duration change to a replacement without an adopted client, so `TestCompleteRunEvacuationRollForwardRequiresTheReplacement` fails that last mutation on its problem-text assertion, without a panic.

  After round 5 the separate term pre-check is gone; the term-bound claim is the one gate. Four more were caught: Phase A not clearing a stale fence, a refused claim that always drops the hold, one that never drops it, and the reload not keeping the newer hold. Switching the claim to `claimTestExecutionFence` is also caught, by the "hold of an ended term" subtest.

  After round 6, five more were caught: a reload that keeps an unwritten `STOPPING` (no term stamp check), one that ignores the journal, one that keeps the hold only for a `RUNNING` listing, one that reuses a lost-term object, and the reload not keeping the newer hold at all.

  After round 7, five more were caught: never stamping the hold, stamping it before its write, skipping the pre-check that replaces a lost-term object (the write-lock re-check still replaced it, but without restoring its runtime), finalizing live tables for an unregistered object, and replacing a terminal run.

  After round 8, five more were caught: no live-table owner check, no replaced-tables check in the retry, a registration-only check (the round-7 behavior), never finalizing, and skipping the reload pre-check under `reloadAndReconcile`. The no-fence branch of `liveTablesFinalizableBy` has no dedicated test: replacing it with always-true or always-false survives.

  After round 9, three more were caught: finalizing by a fresh lookup instead of the checked tables, a per-run retry key, and an ended term's writer that still tries. Dropping the retry's replaced-tables check is also caught.

  After round 10, five more were caught, pinning the recovery path: a per-run recovery key, a recovery retry that looks the tables up again, no recovery retry, and an ended term that does not give up are each caught, as is a terminal writer that looks the tables up again (now against the newer-term race). The first recovery attempt, made right after the restore registered the tables, is equivalent to a lookup and is not mutated.

  After round 11, two more were caught: a hold write without the run's shared write lock, and a superseded hold that still writes.

  After round 12, three more were caught: a stale hold that always registers, and a staleness check that looks only at the term or only at the run object.

## Review

Round 1 found two High safety issues, five Medium test and docs issues, and four Low ones. All are fixed:
- **High: a completed replacement was accepted as committed.** On the generator, any controller stop ends in a completed run, including a stop of a staged assignment that never committed, and adoption then reports `ALREADY_COMPLETED`. Two paths reached it: the predecessor's uncertain path stopping a still-staged replacement after its commit intent, and a failed successor whose hold write failed after it stopped everything it adopted, leaving a `RUNNING` row. A later successor rolled forward with the shard missing, or reported a stopped run as running. Fixed: every replacement must be adopted, and the commit of a completed replacement's capacity without confirmation is gone. `TestEvacuationRollForwardHoldsCompletedReplacement` covers both paths, and the real-generator test shows a stopped staged replacement reporting a completed run.
- **High: force stops preceded the hold.** `failRunEvacuationRecovery`, shared with the rollback, stopped the adopted assignments before writing `STOPPING`. Fixed: the hold is written first, as `failClosedUncertainEvacuation` does, and the stops are sent even if it fails. `TestFailedEvacuationRecoveryHoldsBeforeStopping` checks the durable status at the first stop for both paths.
- **Medium: surviving mutations and missing cases.**
  - The rendezvous and cursor-stateful data refusals had no test: two fail-closed cases were added.
  - Capacity-ledger refusals were untested, including an unrecorded reservation whose check survived deletion: `TestCommitRunEvacuationRollForwardCapacityRefusesUnprovableRows`.
  - The unusable-next-checkpoint hold survived deletion: `TestEvacuationRollForwardUnusableNextCheckpointHoldsRun`.
  - Force stops were not checked for the rotated token: the fake records each stop's token, and both hold assertions require the token of the adopting claim.
  - A `rolling_forward` journal with the setting off and a version 5 committed promotion were untested: `TestRollingForwardJournalWithTheSettingOffIsHeld` and `TestVersionFiveCommittedEvacuationIsPromoted`. Remaining and the spec now list the REC-A exit cases still unexercised at this phase.
- **Low.**
  - A never-failing version check was removed, and the claim about older readers narrowed to what is tested.
  - The late old owner is now also tried inside the window between the intent and the swap.
  - The loss-tolerant subtest no longer races its setup against a 400 ms lapse: it uses a 2 s TTL and polls for the lapsed generation.
  - The committed-row lower bound has its own case.

After the fixes, every mutation in the Validation list is caught.

Round 2 found one High safety issue, one Low code issue, and test and docs gaps. All are fixed:
- **High: a stopped survivor reported a completed run, and the recovery continued without its shard.** Every controller stop ends in a completed run on the generator, so a survivor stopped by the lost leader's uncertain path, or force-stopped by a failed earlier successor whose hold write failed, looked finished. Roll-forward and rollback then completed `RUNNING` without its load, and its reservation was skipped. Fixed: both now fail closed on any participant that completed outside the prior lineage's recorded completions. `TestEvacuationRecoveryHoldsUnrecordedCompletion` covers both paths for both recoveries, plus a genuinely prior-completed survivor.
- **Low: the second hold could be lost or loop.** A retry queued for the first hold write dropped the second hold, losing the unacknowledged-stop reason, and a retry kept failing on a fenced write after an attestation. Fixed: both writes go through one hold whose retry writes the latest reason, and a retry ends once the run is terminal or its journal is gone (`TestInterruptedRunEvacuationHoldRetry`).
- **Docs.**
  - The rollback record now describes hold-first, with a dated note; its untested-write item points to the roll-forward tests.
  - The doc comments now say the run is held, then force-stopped.
  - Remaining and the spec list rolling-version refusal and pause across a second leader change.
  - The node-failure plan notes that a completed replacement proves no commit.
  - The held-reason claim is now true.
- **Tests.**
  - A data race in the failed-successor test (a background hold retry against the fixture's leadership swap) is gone: the fault store uses an atomic flag, and the test waits for the retry to end before the lease changes hands.
  - The claim-window hook no longer calls `t.Fatal` off the test goroutine.

Round 3 found no High or code Medium issue. Its Low safety findings and docs and test issues are fixed:
- **Low: a retry could overwrite a later reason.** `persist` read the reason once before writing, so a queued retry could overwrite the second hold's direct write. Fixed: a per-hold write lock spans the read and the write.
- **Low: one retry slot per run, not per hold.** A second hold object for the run, from `isolateRunAdoption` or from a newer term in the same process while an older term's retry still held the slot, lost its retry. Fixed: the retry writes the run's latest registered hold, and a hold is bound to its term and dropped once that term ends. Covered by the two new `TestInterruptedRunEvacuationHoldRetry` subtests.
- **Docs.**
  - The review status, the background-retry test reference and the mutation counts are corrected.
  - The node-failure plan now says a completed replacement does not prove a commit, and that any unrecorded completion holds the run.
  - The rollback record adds the unrecorded-completion rule to its steps and Remaining.
  - Remaining here describes the completion ambiguity precisely and merges the untested exit cases into one list.
  - The README entry notes that a survivor finishing during failover holds the run.
- **Tests.**
  - The not-adopted completion test asserts rather than panics.
  - The rollback branch of `TestEvacuationRecoveryHoldsUnrecordedCompletion` counts stops at the new term, checks the completed survivor got none, and checks attestation eligibility and the same-term recovery refusal.

Round 4 found one High issue and five Low ones, all fixed:
- **High: the hold retry never ran in production.** It was queued on the terminal-transition retry, which runs only while the replica is admitted. Every hold is decided in Phase A, inside acquisition reconcile, so the retry exited before its first attempt, and a run whose hold write failed stayed `RUNNING` with force-stopped participants until a leader change. Fixed: the hold registry owns a per-run retry that does not wait for admission and is gated on the hold's own term. The "during acquisition reconcile" subtest of `TestInterruptedRunEvacuationHoldRetry` covers it. The other retries queued around adoption, for adopted-run stop proofs (`adoption-resume-stop:`, `adoption-stop:`) and for interrupted-evacuation fences (`evacuation-guard:`), are queued from `resumeAdoptedRuns` in the admission callback, after reconcile, so they do not share the cause.
- **Low.**
  - The hold's term is now passed in by the code that decided it, not read at creation, so an unreadable claim can no longer disable the term check.
  - The fence is claimed with the hold's term, not the current one. (Round 5 narrowed the drop on a refused claim to an ended term.)
  - A registry entry is forgotten when the retry ends, and when a run is removed (`tests.go`, `persistence.go`, `timeslot_reservation.go`).
  - A retry that wrote an earlier reason while a later one arrived now goes on: the registry owns the retry and it ends only once no hold is left (the "reason changed during a retry write" subtest).
  - Docs: the mutation note no longer relies on a panic; the rollback record says a completion outside the prior lineage fails the rollback and holds the run; the README entry matches the record's completion wording.

Round 5 found one High issue, which both reviewers reproduced, and six Low ones. All are fixed:
- **High: a hold was dropped after the process lost and won its lease again.** Teardown after a lost lease leaves the run's object fenced to the old term, the reload keeps that object for a `RUNNING` row, and the hold is written before adoption installs the run. The term-bound claim was refused by that stale fence, and every refused claim dropped the hold, so the run stayed `RUNNING` with its participants stopped, and could not be attested. Fixed: Phase A clears the stale fence of every run it selects, and a refused claim drops the hold only once its term has ended. The stale-fence cases of `TestFailedEvacuationRecoveryHoldsBeforeStopping` and the "claim refused in a current term" subtest cover both.
- **Low.**
  - A reload racing the hold retry could put the in-memory status back to `RUNNING`; it now keeps the newer hold (`TestReloadKeepsANewerEvacuationHold`).
  - The shutdown exit of the retry has a subtest.
  - The "removed run" subtest is renamed "forgotten hold", and Remaining explains why the removal call sites only free the entry sooner.
  - The term check is now a single gate, and the mutation notes say what stays untested.
  - Docs: the review status and the round-4 mutation note are corrected.

Round 6 found two High issues, which the reviewer probed, and seven Low ones. All are fixed or recorded:
- **High: the reload's hold rule applied to a `STOPPING` never written.** An uncertain evacuation sets `STOPPING` in memory before its save; when the save failed and the lease was lost and won again, the reload kept that `STOPPING` over the `RUNNING` row, Phase A skipped the run, and its stale fence blocked attestation and the fence recovery. Fixed: only a hold written by `persist` in the current term is kept (`evacuationHoldOwner`), and a lost-term object is replaced.
- **High: a held run could not be attested after the lease was won again.** The reload reused the object still fenced to the lost term, and attestation and the fence recovery claim with the current term, which a stale fence refuses. Fixed: the reload replaces any run object with no runtime that is fenced to a lost term (`TestHeldRunAfterTheLeaseIsWonAgain`).
- **Low.**
  - Lost-term writers: the replacement keeps them off the new object; the generic terminal writer is recorded under Remaining.
  - Docs: the mutation note, the retry contract, the bounds of a refused-claim retry, the merged hold-before-stop bullet and the Phase A clearing are corrected.
  - The reload rule's journal and `PAUSED` conditions have tests.

Round 7 found no High or Medium issue. Its Low findings are fixed:
- **Live tables.** A lost-term writer could finalize the registered run's live tables, which are keyed by run ID, before the terminal writer's pointer check. The check now comes first (`TestHeldRunAfterTheLeaseIsWonAgain`).
- **Restore after replacement.** Skipping the replacement pre-check went unnoticed, because the fixture's groups had no stored scripts. They now do, and the "lost term" case checks the runtime was restored.
- **Stamp after the write.** A new "hold write failed" case checks the stamp is set only by a durable write.
- **Wording.** "Phase A decides" now says when that happens, and the stale-fence and new-leadership wording is precise.
- **Production order.** `TestLostTermRunRollsForwardAfterReloadAndReconcile` runs the replacement through `reloadAndReconcile`.
- **Terminal runs.** The replacement now excludes terminal runs, so a lease change no longer re-hydrates every finished run; a case checks a terminal run's object is reused.

Round 8 found no High issue, one Medium test gap and three Low findings. All are fixed:
- **Medium: nothing asserted that a registered run finalizes its live tables.** The attestation subtest now asserts it.
- **Low.**
  - **Regression.** The round-7 registration check skipped live-table finalization when a same-term reload had replaced the finishing run's object, leaving its tables attached. The check now lives in `finishLiveTables`, compares the writer's fence with the tables' term owner, and also guards its retry and local runs.
  - **Restore check.** The production-order test now asserts the runtime restore.
  - **Rollback.** The rollback record notes that the reload and replacement rules also apply to rollback holds, and a rollback case was added to the reload test.

Round 9 found no High issue, one Medium race, which was reproduced, and four Low findings. All are fixed or recorded:
- **Medium: finalization looked the tables up again.** `finishLiveTables` checked ownership on the tables it found, then `finalizeLiveTables` looked them up again by run ID. Tables a newer term restored in between were finalized despite the check. Fixed: `finalizeRunLiveTables` finalizes the checked tables; the test barrier case reproduces the race.
- **Low.**
  - The ended-term live-table case now asserts that no retry is left; such a writer gives up at once.
  - Live-table retries are keyed by run and term owner, so an earlier term's retry no longer drops the current one's.
  - Tables the new term never restores are recorded under Remaining.
  - Docs: the mutation paragraphs are in order, the round-8 count matches, and the reload test says "a candidate" for both journals.

Round 10 found no High or Medium issue. Its Low findings are fixed:
- **Recovery retry.** The recovery finalization's retry now shares the terminal writer's finalizer (`liveTableFinalizer`), so it ends for an ended term or replaced tables and finalizes exactly the tables it was given. `finalizeRecoveredLiveTables` is tested directly.
- **Race test.** The race test now moves the lease to a newer term at its barrier, as in production.
- **Retry keys.** The earlier-term retry test first checks the two terms' keys differ.
- **Contract.** The Contract is reorganized: failures, the hold and its retry, lease lost and won again, and live-table finalization in its step order. The rollback bullet no longer says rollback is unchanged.

Round 11 found no High or Medium issue. Its Low findings are fixed:
- **Safety: a queued retry of an earlier hold could overwrite a later hold of the run.** The write lock belonged to each hold object, so holds of one run were not serialized: a later hold, for example from `isolateRunAdoption`, could be written and unregistered while the earlier hold's retry write was in flight, which then landed with the older reason. Fixed: one write lock per run, and a hold writes only while it is the run's latest. Two new `TestInterruptedRunEvacuationHoldRetry` cases cover both.
- **Test.** The ended-term recovery case now asserts the owner-expired refusal and that the tables stay open.
- **Docs.** The mutation paragraphs are in order with counts, and the live-table steps are precise.

Round 12: the safety reviewer found nothing. The tests and docs reviewer found one Medium issue, which it verified with a scratch test, and two Low ones. All are fixed:
- **Medium: a current hold was lost to a later stale hold.** A hold registered unconditionally by run ID, and since round 11 only the latest hold writes. When the current term's hold failed to write, a lost-term goroutine still holding the run's old object could register its own hold, and the current hold then wrote nothing, while the stale one was dropped at its own gates. The run stayed `RUNNING` with its participants force-stopped. Fixed: a hold of an ended term or of an unregistered run object never displaces a registered hold (`register`). Covered by the new "stale hold after the current hold" cases.
- **Low.** The round-3 mutation note now points to the round-11 coverage, and the recovered live-table entry says what the ended term's first attempt does.

Round 13 was the last cycle: by agreement, review stopped once a round found no High or Medium issue. Neither reviewer did. The safety reviewer confirmed the registration rule never refuses or drops a current hold (a term that reads ended never reads current again, and a reload never leaves the run briefly unregistered) and that the check-and-swap loop cannot livelock. Three Low items are left open:
- The refusal log always says the current term's hold stands, even when the registered hold is itself stale (`register`).
- Two branches of `register` are not pinned by a test: refusing a stale hold only when another hold exists, and allowing a re-hold of the same object. Both are harmless (a stale hold that registers is dropped at its own gates), and the loop's retry branch needs a scheduling hook to test.
- The Contract's "registers only when the run has no hold" omits that a re-hold of the same hold object, as in `failRunEvacuationRecovery`'s second hold, registers whatever its term; read it as "no other hold".

## Remaining

- **Review.** Rounds 1 to 13 have run; see Review. Round 13's Low items stay open (below).
- **Live tables the new term never restores.** Phase A restores a run's live tables only for an adoption candidate. A run the new term holds fail-closed, or whose checkpoint it cannot decode, keeps the lost term's in-memory tables registered: the new term's terminal writer for it (attestation or fence recovery) is refused by the owner check, nothing finalizes those tables durably, and their memory and budget stay counted until the process restarts. Neither a terminal writer nor a recovery retry of an ended term retries them any more. Durable finalization of live tables for runs not adopted after a failover predates this slice; it is not addressed here.
- **Deployed qualification.** REC-A's exit still requires failure injection on a deployed cluster at every journal boundary, worker or node partition, late old-owner return and independent target counters. These tests use fake and in-process generators.
- **REC-A exit cases untested at this phase:**
  - a capability mismatch between the replacement and the run;
  - no spare capacity at the `replacement_committing` or `rolling_forward` phase;
  - the run's own deadline elapsing before the successor on a deployed cluster (exercised only with fake generators in the `staged`, `rolling_back`, `replacement_committing` and `rolling_forward` phases and an in-process replacement, not the `prepared` phase or an in-process survivor or source, since the [generator terminal cause slice](../roadmap-generator-terminal-cause-2026-10-24/README.md));
  - a stop requested while the roll-forward runs;
  - a worker or node partition;
  - rolling-version refusal by older controllers during an upgrade;
  - pause at the `rolling_forward` phase across a second leader change.
- **Not covered.**
  - a restart of a missing replacement for the eligible HTTP workload;
  - replay of work whose replacement never committed: such a run is held `STOPPING`.
- **Duration during the gap.** Between `CommitWork` and the successor's adjustment, the replacement runs on its staged duration. In its own logical clock it never exceeds the configured duration, but if the gap outlasts the fleet deadline it can run up to the staging time past it before the adjustment ends it.
- **Completion is ambiguous.** Resolved on 2026-10-24 for causes reported by current generators by the [generator terminal cause slice](../roadmap-generator-terminal-cause-2026-10-24/README.md): a participant whose generator reports that its duration ran out, at no less than the durable expectation, no longer holds the run, and a naturally completed replacement rolls forward. A completion from an older generator, an older archive or a pruned tombstone, a controller stop, executions that ended early, or a shorter duration still holds the run for attestation.
- **A failed roll-forward** leaves the run `STOPPING`, holding its capacity and timeslot, until a newer term fences it or an administrator attests that its generators stopped. That includes a replacement that never committed.
- **Write failures.** A failed `STOPPING` write in `failRunEvacuationRecovery` is retried in the background by the hold registry's retry. `TestEvacuationRollForwardHoldsCompletedReplacement` shows the stops are still sent and the next term holds the run, and `TestInterruptedRunEvacuationHoldRetry` covers the background retry.
- **Removal paths.** `forgetEvacuationHold` is called where a run is removed (`tests.go`, `persistence.go`, `timeslot_reservation.go`), but only the function itself is tested, not each call site. Those calls only free the entry sooner: `persist` already drops a hold whose run object is no longer registered (`tr.tests[test.ID] != test`), which ends the retry.
- **A claim refused in a current term** retries with backoff up to one second until the fence is released, the term ends, the run becomes terminal, its journal is gone, the run is removed or the runner shuts down. No production path is known to produce such a fence once the reload and Phase A have cleared lost-term fences; the subtest sets one directly.
- **Lost-term writers on a reused object.** The reload replaces active lost-term objects, so this slice's paths never hand a lost-term object to a new term. Adoption's install, which predates this slice, clears a fence the same way and relies on the same reload. The generic terminal writer (`completeTestRunUnderArchiveFence`) still writes with whatever fence the object carries at write time; a goroutine that kept a reused object across a lease change, if any path still allows one, would write under the newer term. It is not changed here because every run's completion goes through it.
- **Loss-tolerant runs.** The duration adjustment is sent before `secureAdoptedRuns` renews the per-run leases, because the generator's duration control does not require that lease (`planDurationWithTokenLocked`). The in-process test waits for the real epoch to lapse, so it does not use a controlled clock.
