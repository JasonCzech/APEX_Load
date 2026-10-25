# Evacuation rollback (REC-A)

Status: implemented behind an opt-in. Qualified locally only.

## Contract

- **Opt-in.**
  - `APEXLOAD_EVACUATION_ROLLBACK_RECOVERY` (`WithEvacuationRollbackRecovery`) is off by default.
  - When on, a new evacuation journal is version 4. It adds the replacement capacity claims (`CapacityClaims`) and the idle standby rows they activated (`StandbyPreImages`).
  - When off, journals stay version 3, exactly as before.
- **Older peers.**
  - An older controller cannot read a version 4 journal: `decodeRunContinuityCheckpoint` rejects it, and older releases only recognize versions 2 and 3.
  - Such a controller keeps the run `STOPPING` through its interrupted distributed stop path.
  - Enable the setting only once every replica reads version 4.
- **Eligibility** (`evacuationRollbackGuard`, `runEvacuationRollbackPlan`). A run qualifies only when:
  - the setting is on and run adoption is enabled;
  - the run is `RUNNING` or `PAUSED`, with the continue failover policy, on the distributed backend;
  - the journal passes `interruptedEvacuationTargets`, is version 4, is in phase `prepared`, `staged` or `rolling_back`, and is not abandoned (`AbandonedAt`);
  - the source is not a prior completed target;
  - no surviving participant's shards changed;
  - every capacity claim and pre-image matches the journal's replacement targets, term and frozen identity.

  Anything else keeps the fail-closed path: `source_fenced`, `replacement_committed` without promotion, abandoned journals, `STOPPING`, and the fail policy.
- **Successor rollback** (Phase A, `adoptOrphanedRuns`), in order:
  1. The journal's prior checkpoint, whose shards equal `BeforeShards`, becomes the adoption checkpoint.
  2. `persistRunEvacuationRollbackIntent` writes phase `rolling_back`, with the planned adoption intent inside the journal, before any generator sees the rotated token.
  3. The same batched epoch claim adopts the prior participants and claims each replacement with no adoption for the run. That claim drains the staged replacement assignment before it is acknowledged.
  4. `completeRunEvacuationRollback` requires every replacement claim to be acknowledged, the source to be adopted, not completed, and no participant completed outside `Prior.CompletedTargets`.
  5. `rollbackRunEvacuationCapacity` then runs under the successor's term:
     - It releases the replacement reservation, or the activated standby reservation, and confirms the release with the generator.
     - For a standby, it then reserves the pre-image idle again and confirms it before committing it.
     - Each step resumes from the ledger it finds, including a standby the predecessor already returned idle.
  6. Only then is the journal replaced by the plain prior checkpoint, and adoption continues as usual.
  7. `adoptPodCapacity` skips, and `AdoptPodCapacity` accepts, an idle standby already reserved under the successor's term.
- **Failures stay fail-closed.**
  - The journal is replaced only after a complete rollback. That needs:
    - every prior participant adopted, the source adopted rather than completed, and no participant completed outside `Prior.CompletedTargets`;
    - every replacement claim acknowledged;
    - every capacity step confirmed.
  - On any problem, `failRunEvacuationRollback`:
    - keeps the `rolling_back` journal;
    - first writes the run `STOPPING` with the evacuation-uncertain reason, and installs no runtime;
    - then force-stops each prior participant this term adopted, with the rotated token, and adds any stop that is not acknowledged to the held reason.

    Updated 2026-10-24: the hold now precedes the stops (previously the stops came first), and a participant that reports a completed run the prior lineage never recorded fails the rollback. Both came from the [roll-forward review](../roadmap-evacuation-rollforward-2026-10-24/README.md#review), as did two reload rules that apply to rollback holds too: a reload keeps a hold written in the current term over a row read before the write, and replaces a run object left fenced to a lease term this process lost.
  - A later reader therefore never sees a `RUNNING` row with a plain checkpoint after a failed rollback.
  - That term's fail-closed recovery (`recoverInterruptedRunEvacuation`, `resumeInterruptedRunEvacuations`) does not fence at or below `RollbackGeneration`, because the epoch claim of a term that adopted the prior topology does not drain what it adopted.
  - A newer term fences every participant and records the failure. An administrator attestation also resolves it, because the run has no runtime.
- **Clean same-leader rollback** (`restoreRunEvacuationPriorCheckpoint`):
  - Applies when the leader that wrote a version 4 `prepared` or `staged` journal positively rolls its staging back before the source fence:
    - staging failed and its rollback succeeded;
    - replacement lease establishment failed;
    - the replacement supervision claim failed;
    - the evacuation became stale.
  - That leader restores the checkpoint the journal replaced, or no checkpoint if the run was unarmed. Without this, a later successor would roll back to a topology from before any VUser change made afterwards.
  - Version 3 journals keep their earlier behaviour.
- **Other failures before anything was adopted**, and panics, also hold the run `STOPPING` (`holdInterruptedRunEvacuation`). That covers:
  - a rollback candidate whose prior checkpoint cannot be used before any claim, for example a failed live-state restore or an invalid adoption intent;
  - a panic before the journal is replaced, which goes to `failRunEvacuationRollback`;
  - a panic while handling a failed rollback, which `isolateRunAdoption` catches for any run still under an evacuation journal.

  None of these binds an ordinary adoption failure, which would make the run terminal while its participants may still run.
- **Dead journals left by this leader.** A journal can survive because the restore or marker write failed after a clean rollback, or because the staging rollback itself errored. The next re-arm of the run, such as a VUser scale, is refused by an unresolved journal. `writeRunContinuity` therefore marks a version 4 `prepared` or `staged` journal of the same term and owner abandoned. A re-arm never overlaps an evacuation of the run, so such a journal is dead, and its prior topology is about to go stale.
- **Clean same-leader rollback, ordering.** The leader returns the evacuation's capacity first. The journal holds the standby pre-images, so it is replaced only once the capacity is back:
  - The leader checks the ledger itself (`runEvacuationCapacityReturned`), because an ordinary release reports no error.
  - If the capacity is not back, the journal is marked abandoned at once (`markRunEvacuationCheckpointAbandoned`). A successor never rolls back to an abandoned journal and holds the run `STOPPING`, as for a version 3 journal.
  - The deferred capacity rollback then retries. If the ledger then shows the capacity returned, `finishRestore` restores the prior checkpoint.
  - While any journal remains, the run can neither start another evacuation (`beginRunEvacuationCheckpoint`) nor re-arm its checkpoint.
- **A new term after `rolling_back`** rolls back again, and its source adoption carries the earlier term's token proof.

## Validation

- **`TestEvacuationJournalVersionFollowsRollbackRecovery`** (`pkg/api`):
  - version 3 with no capacity when the setting is off, and version 4 with claims when it is on;
  - progress keeps the version it was written with;
  - an adoption reader rejects both versions.
- **`TestEvacuationRollbackReturnsRunToPriorTopology`** covers `prepared` and `staged`, each with an ordinary replacement and with an activated standby:
  - reconcile leaves the run `RUNNING`;
  - the source alone is adopted from the old term;
  - the replacement receives one epoch claim with no adoption, and is not a participant;
  - the durable checkpoint is the prior topology under the new term;
  - the source capacity is adopted;
  - an ordinary replacement reservation is released at the new epoch;
  - a standby is released and reserved idle again, at the pre-image's revision, under the new term.
- **`TestEvacuationRollbackKeepsFailClosedPaths`**: with the setting off, a `source_fenced` journal, a `STOPPING` run or the fail policy, the run is no adoption candidate and reconciles to `STOPPING`.
- **`TestEvacuationRollbackPlanRejectsUnsafeJournals`**: rejects a version 3 journal, a completed source, a changed survivor, a claim off the replacement targets, a committed claim, a claim from another term, an unclaimed pre-image, a pre-image that is not idle, and a changed pre-image budget.
- **`TestEvacuationRollbackUnfencedReplacementKeepsJournal`**:
  - an unreachable replacement keeps the `rolling_back` journal with its rollback generation and leaves the replacement capacity unchanged;
  - the adopted source is force-stopped at that term;
  - the run is durably `STOPPING`, with the evacuation-uncertain reason and no runtime, and is eligible for attestation;
  - the same term's recovery refuses to fence;
  - a newer term fences the source and the replacement and records the run `FAILED`.
- **`TestEvacuationRollbackResumesAfterAnotherLeaderChange`**: a successor lost right after writing its `rolling_back` intent leaves the run `RUNNING`. The next term rolls it back, its source adoption carries the first term's token proof, and the standby ends idle under the newer term.
- **`TestEvacuationRollbackFailsWhenSourceAlreadyCompleted`**:
  - a completed source keeps the journal, and the run is held `STOPPING` without stopping that source again;
  - the replacement capacity is unchanged;
  - a later successor does not treat the run as an ordinary adoption candidate.
- **`TestEvacuationRollbackCapacityFailureKeepsJournal`**: an unconfirmed release keeps the journal, holds the run `STOPPING` and leaves the reservation `releasing`.
- **`TestEvacuationRollbackAcceptsPredecessorRestoredStandby`**: a standby the predecessor already returned idle, committed or pending, is accepted. It is never released, and it ends committed under the new term.
- **`TestRestoredRunEvacuationStandbyMatchesFrozenIdentity`**: a newer idle revision is accepted, and changed peaks, incarnation, budget, demand, sharing or attempt are rejected.
- **`TestEvacuationRollbackUnusablePriorCheckpointHoldsRun`**: an out-of-order prior adoption intent holds the run `STOPPING` with its journal unchanged, and no generator is claimed.
- **`TestEvacuationRollbackFailurePanicHoldsRun`**: a panic while force-stopping after a failed rollback still holds the run `STOPPING` under its `rolling_back` journal.
- **`TestEvacuationRollbackPanicAfterSwapIsOrdinaryFailure`**: a panic after the journal was replaced takes the ordinary adoption-failure path. There is no evacuation-uncertain hold and no force stop in that term.
- **`TestEvacuationRollbackPanicKeepsJournal`**: a panic in the capacity rollback keeps the journal, force-stops the adopted source and holds the run `STOPPING` for attestation.
- **`TestCleanSameLeaderRollbackRestoresPriorCheckpoint`**:
  - an armed run gets back its prior checkpoint (the same topology and arming time), and the run can be re-armed with new VUser counts afterwards;
  - an unarmed run returns to no checkpoint.
- **`TestCleanRollbackLeavesOtherJournals`**: version 3, `source_fenced`, other-term and other-owner journals are not replaced. The owner check in `restoreRunEvacuationPriorCheckpoint` is defence in depth: the store's lease-fenced swap already refuses another owner, so removing the check does not fail this test.
- **`TestVersionFourJournalCarriesFailoverHistory`**: a version 4 journal still supplies the run's failover history.
- **`TestEvacuationRollbackRefusesChangedLedger`** covers seven changes, each of which keeps the journal and holds the run `STOPPING`. None of the six ledger changes reaches the generator's capacity RPC. The swap failure happens after the capacity was returned. The cases:
  - a replacement row that changed since the journal, or was committed;
  - a predecessor-restored standby from a newer term;
  - a replacement row without peaks;
  - a predecessor-restored standby with another incarnation;
  - a standby left `releasing`;
  - a final journal swap that fails.
- **`TestEvacuationRollbackResumesPredecessorRelease`**: a replacement reservation the predecessor left `releasing` is released at the new epoch, and one it already deleted needs no request.
- **`TestRollbackRunEvacuationCapacityRefusesUnprovableTargets`**: refuses a replacement client without the capacity RPC, and a ledger reservation the journal did not record.
- **`TestEvacuationRollbackAdoptsPausedRun`**: a paused run is rolled back and stays paused.
- **`TestFailedEvacuationRollbackIsNotRetriedInItsTerm`**: `resumeInterruptedRunEvacuations` queues no same-term recovery for a failed rollback.
- **`TestFailedEvacuationRollbackWaitsWhenTermIsUnknown`**: a replica that cannot read its term queues no recovery either. This is a regression check, not one that catches a mutation: an unreadable term reads as generation 0, which already fails the comparison with `RollbackGeneration`.
- **`TestRollingBackJournalRejectsContradictions`**: each contradiction in a `rolling_back` journal is refused by both the fail-closed recovery and the rollback plan. The contradictions are version 3, the rollback term, owner and start, the staged targets, the source fence and the replacement commit.
- **`TestRunEvacuationCapacityReturnedReadsTheLedger`**:
  - a pending or `releasing` replacement reservation is not returned, and a deleted one is;
  - a pending idle standby is not returned, and a committed idle one is;
  - a committed row that is still activated, or has another incarnation or budget, is not the returned standby;
  - a released standby is not returned idle.
- **`TestAbandonedEvacuationJournalIsNeverRolledBack`**:
  - the abandoned marker is durable and is not applied twice;
  - the rollback plan refuses the journal, while the fail-closed participants remain;
  - restoring the prior checkpoint still accepts an abandoned journal. The capacity gate lives in `evacuateGenerator`; see Remaining.
- **`TestReArmMarksADeadSameTermJournalAbandoned`**: a refused re-arm marks a same-term version 4 `prepared` or `staged` journal abandoned, and leaves version 3 and other-term journals unchanged. The owner check in `markRunEvacuationCheckpointAbandoned` is defence in depth: the store's lease-fenced swap already refuses another owner, so this test cannot distinguish it.
- **`TestVersionFourCommittedEvacuationIsPromoted`**: a version 4 `replacement_committed` journal is promoted and its replacement adopted, as for version 3.
- **`TestAdoptPodCapacityAcceptsCurrentTermStandby`** (`pkg/storage`, and the PostgreSQL integration test): adoption is refused while a standby reserved under the new term is still pending, and succeeds once it is committed. The existing `TestAdoptPodCapacityRequiresEveryStandby` still refuses an old-term standby that was left out.

## Review

Round 1 used two reviewers, one on safety and one on the tests and docs.
- **High:** after a problem such as a completed source, the journal was still replaced while the run stayed `RUNNING` on disk until Phase B. A successor in between could adopt the plain checkpoint and finalize the run `COMPLETE`. Fixed: the journal is replaced only after a complete rollback, and a failed rollback is held `STOPPING` at once.
- **Medium:** an unfenced replacement bound an adoption failure whose stop proof could not read the journal. The run was refused both recovery in that term and administrator attestation. Fixed: no runtime is installed, the adopted participants are force-stopped, and the run is eligible for attestation.
- The tests-and-docs reviewer was stopped before it reported, because its snapshot predated those fixes. Its scope moved to round 2.

Round 2 used two fresh reviewers. The safety reviewer confirmed both round-1 fixes and found:
- **Medium:** a version 4 journal left by a clean same-leader rollback kept a prior topology that went stale as VUsers changed. Fixed by `restoreRunEvacuationPriorCheckpoint`.
- **Low:** a panic before the journal was replaced bound an ordinary adoption failure, which installed a runtime and blocked attestation. Fixed with a rollback-specific recovery.
- **Low:** failover history was not loaded from version 4 journals. Fixed in `setContinuityCheckpointLocked`.
The tests-and-docs reviewer of round 2 found weak tests: three mutations of safety checks survived. Its findings and the fixes:
- **Tests added:** changed replacement and standby rows, the capacity-journal validations (each asserting its error), predecessor release states, unprovable targets, a paused run, the journal swap failure, the same-term skip, and version 4 promotion. The fail-closed cases now check `evacuationRollbackGuard` directly. All three mutations now fail the tests.
- **Storage:** the exemption now also requires the standby to be committed, with a negative test for a pending one.
- **Remaining:** now lists the real-generator drain proof and the write failures that are still untested.
- **Docs:** the README.md entry names its prerequisites and the attestation, and the spec no longer contradicts itself.
- **Nit:** a missing claim result now has its own message. `resumeInterruptedRunEvacuations` also skips a `rolling_back` journal when the current term cannot be read.

Round 3 used two fresh reviewers. The safety reviewer confirmed the round-2 fixes and found:
- **Medium:** a rollback candidate whose prior checkpoint could not be used was marked `FAILED` by `failRunAdoption`. Before this change it was held `STOPPING`. Fixed: it is held `STOPPING`.
- **Low:** a panic inside the rollback failure handling reached `failRunAdoption`. Fixed in `isolateRunAdoption`.
- **Low:** a clean same-leader rollback replaced the journal before returning the capacity, which could lose the standby pre-image. Fixed: capacity is returned first, and the journal is kept if that fails.

A mutation pass after round 3 removed seven safety checks in `run_evacuation_rollback.go`. Tests were added for the two that survived and have their own effect: the replacement-row state and the standby generation bound. The round-4 reviewer then found three more surviving mutations, listed under round 4 below. The force-stop mutation did not compile, but its effect is asserted by `assertFailedRollbackHeld`.

Round 4 used two fresh reviewers. The safety reviewer confirmed the round-3 fixes and found two Low issues, both in the same-leader journal restore when capacity is not returned:
- **Stale journal on a failed capacity return.** A capacity return that failed kept the journal forever, so its prior topology could go stale while the run was scaled.
- **Silent ordinary release.** An ordinary release reports no error, so the journal could be replaced while the replacement reservation remained.

The fix:
- the restore now checks the ledger itself (`runEvacuationCapacityReturned`);
- it retries after the deferred capacity rollback;
- otherwise it marks the journal abandoned (`AbandonedAt`, via `markRunEvacuationCheckpointAbandoned`). A successor never rolls back to an abandoned journal, and it stays on the fail-closed path.

The tests-and-docs reviewer found three surviving mutations: the standby budget against its claim, a claim without an incarnation, and the whole `rolling_back` consistency check. It also found several weak assertions and README overstatements. All of these are fixed:
- **New plan cases.** One changes the budget and demand together; one blanks the claim's incarnation.
- **`TestRollingBackJournalRejectsContradictions`.** Each contradiction is refused by both the fail-closed recovery and the rollback plan.
- **Durable checks in `TestEvacuationRollbackKeepsFailClosedPaths`.** It now checks the stored status, reason and unchanged journal.
- **`TestEvacuationRollbackResumesAfterAnotherLeaderChange`.** It reads the stored `RUNNING` status. It also checks the replacement fence and the final plain checkpoint.
- **`TestEvacuationRollbackRefusesChangedLedger`.** It now checks for no capacity RPC on every ledger change.
- **More identity cases** in `TestRestoredRunEvacuationStandbyMatchesFrozenIdentity`.
- **`TestFailedEvacuationRollbackWaitsWhenTermIsUnknown`.** A regression check for the unreadable-term skip; see Validation.
- **`TestRunEvacuationCapacityReturnedReadsTheLedger` and `TestAbandonedEvacuationJournalIsNeverRolledBack`.** They cover the safety fixes.

Round 5 used two fresh reviewers. The safety reviewer confirmed the round-4 fixes and found one Low issue. A journal could still survive unmarked when the restore or the abandoned-marker write itself failed, or when the staging rollback errored, and a later scale then made its topology stale. Fixed: `writeRunContinuity` now marks such a journal abandoned on the refused re-arm (`TestReArmMarksADeadSameTermJournalAbandoned`). Version 3 and other-term journals are left unchanged.

The round-5 tests-and-docs reviewer found nine issues, all fixed:
- **Tests.**
  - The ledger-return test now covers the standby's identity.
  - The swap-failure case asserts the capacity was returned.
  - The unreadable-term test is described as a regression check.
- **Naming.** The restore functions were renamed, from `abandon…` to `restoreRunEvacuationPriorCheckpoint`, `restoreGuard` and `finishRestore`.
- **Docs.** The abandoned-journal behaviour is added to this contract, the README.md entry and the spec. The four round-4 tests are listed under Validation. The abandon wiring inside `evacuateGenerator` is listed under Remaining.

Round 6 used two fresh reviewers. The safety reviewer found nothing: no caller of the re-arm can overlap an evacuation of the same run in the same term, and the marking can only remove rollback eligibility. The tests-and-docs reviewer found five Low or Nit issues, all fixed:
- the re-arm test now covers a `staged` journal;
- the marker's owner check is described as defence in depth;
- the order of the abandoned marker and the deferred restore is corrected here and in the spec;
- an overstated test claim is corrected;
- two leftover names from the rename are changed.

Round 7 used one tests-and-docs reviewer, because the code was unchanged since the clean round-6 safety review. It found one Low and three Nits, all fixed:
- `TestEvacuationRollbackPanicAfterSwapIsOrdinaryFailure` covers the switch back to ordinary panic handling once the journal is replaced;
- two code comments now give both reasons a journal is marked abandoned;
- the README.md entry and the spec now mention re-arm marking;
- a sentence that contradicted itself is reworded.

Round 8 used one tests-and-docs reviewer. It found nothing: both of its mutations were caught, and the README, the README.md entry and the spec match the code.

Gates on the reviewed code (`dd88b5f54`):
- the full `pkg/api` suite;
- the PostgreSQL storage suite (`-tags postgres_integration`), including `TestPodCapacityPostgresAtomicPlacement` with `checkAdoptCurrentTermStandbyPodCapacity`.

## Remaining

- **Failure injection.** REC-A's exit still requires failure injection at every journal boundary, worker or node partition, late old-owner return, and independent target counters. These tests use fake generators.
- **Not covered.**
  - a restart of a missing replacement for the eligible HTTP workload. Updated 2026-10-24: covered behind a further opt-in by the [replacement restart slice](../roadmap-evacuation-replacement-restart-2026-10-24/README.md), which restarts the moved share of a `source_fenced` journal on a new replacement;
  - replay of source-fenced work, beyond that restart.
- **Drain before release is not proven against a real generator.** That the replacement's epoch claim drains its staged assignment before the capacity release is accepted rests on the generator's existing epoch semantics. These tests use fake generators that accept every release.
- **The same-leader journal restore** (`restoreRunEvacuationPriorCheckpoint`) and the abandoned marker are tested directly. Inside `evacuateGenerator`, these have no integration test:
  - the four call sites;
  - the capacity-first ordering;
  - marking the journal abandoned when the capacity is not returned;
  - the deferred retry and restore (`finishRestore`).
- **Some write failures are untested.** A failed `rolling_back` intent write aborts that Phase A attempt, and Phase A is retried. A failed `STOPPING` write in `failRunEvacuationRollback` is retried in the background. The intent write failure has no dedicated test here; the failed `STOPPING` write and its retry are exercised by the roll-forward record's `TestEvacuationRecoveryHoldsUnrecordedCompletion` and `TestInterruptedRunEvacuationHoldRetry`, which share `failRunEvacuationRecovery`.
- **A completed source, or a participant with a completion outside `Prior.CompletedTargets`, fails the rollback and holds the run `STOPPING`.** That happens even when it finished naturally, for example the source after a same-leader rollback, because a controller stop ends the same way on the generator. Updated 2026-10-24: a current generator now reports why a run ended, and a source or survivor whose duration ran out, at no less than the run's durable duration, no longer fails the rollback; it is recorded completed in the restored checkpoint ([generator terminal cause](../roadmap-generator-terminal-cause-2026-10-24/README.md)). Other completions still hold the run.
- **A failed rollback** leaves the run `STOPPING`, holding its capacity and timeslot, until a newer term fences it or an administrator attests that its generators stopped. That includes an unfenced replacement, a completed source, a participant with an unrecorded completion and an unconfirmed capacity step.
