# Evacuation rollback (REC-A)

Status: implemented behind an opt-in; local qualification only.

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
  - the journal passes `interruptedEvacuationTargets`, is version 4, and is in phase `prepared`, `staged` or `rolling_back`;
  - the source is not a prior completed target;
  - no surviving participant's shards changed;
  - every capacity claim and pre-image matches the journal's replacement targets, term and frozen identity.

  Anything else keeps the fail-closed path: `source_fenced`, `replacement_committed` without promotion, `STOPPING`, and the fail policy.
- **Successor rollback** (Phase A, `adoptOrphanedRuns`), in order:
  1. The journal's prior checkpoint, whose shards equal `BeforeShards`, becomes the adoption checkpoint.
  2. `persistRunEvacuationRollbackIntent` writes phase `rolling_back`, with the planned adoption intent inside the journal, before any generator sees the rotated token.
  3. The same batched epoch claim adopts the prior participants and claims each replacement with no adoption for the run. That claim drains the staged replacement assignment before it is acknowledged.
  4. `completeRunEvacuationRollback` requires every replacement claim to be acknowledged and the source to be adopted, not completed.
  5. `rollbackRunEvacuationCapacity` then runs under the successor's term:
     - It releases the replacement reservation, or the activated standby reservation, and confirms the release with the generator.
     - For a standby, it then reserves the pre-image idle again and confirms it before committing it.
     - Each step resumes from the ledger it finds, including a standby the predecessor already returned idle.
  6. Only then is the journal replaced by the plain prior checkpoint, and adoption continues as usual.
  7. `adoptPodCapacity` skips, and `AdoptPodCapacity` accepts, an idle standby already reserved under the successor's term.
- **Failures stay fail-closed.**
  - The journal is replaced only after a complete rollback. That needs:
    - every prior participant adopted, and the source adopted rather than completed;
    - every replacement claim acknowledged;
    - every capacity step confirmed.
  - On any problem, `failRunEvacuationRollback`:
    - keeps the `rolling_back` journal;
    - force-stops each prior participant this term adopted, with the rotated token;
    - writes the run `STOPPING` with the evacuation-uncertain reason, and installs no runtime.
  - A later reader therefore never sees a `RUNNING` row with a plain checkpoint after a failed rollback.
  - That term's fail-closed recovery (`recoverInterruptedRunEvacuation`, `resumeInterruptedRunEvacuations`) does not fence at or below `RollbackGeneration`, because the epoch claim of a term that adopted the prior topology does not drain what it adopted.
  - A newer term fences every participant and records the failure. An administrator attestation also resolves it, because the run has no runtime.
- **Clean same-leader rollback** (`abandonRunEvacuationCheckpoint`):
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
- **Clean same-leader rollback, ordering.** The leader returns the evacuation's capacity first. It replaces the journal only once that succeeded, because the journal holds the standby pre-images.
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
- **`TestEvacuationRollbackPanicKeepsJournal`**: a panic in the capacity rollback keeps the journal, force-stops the adopted source and holds the run `STOPPING` for attestation.
- **`TestCleanSameLeaderRollbackRestoresPriorCheckpoint`**:
  - an armed run gets back its exact prior checkpoint, and the run can be re-armed with new VUser counts afterwards;
  - an unarmed run returns to no checkpoint.
- **`TestCleanRollbackLeavesOtherJournals`**: version 3, `source_fenced`, other-term and other-owner journals are not replaced. The owner check in `abandonRunEvacuationCheckpoint` is defence in depth: the store's lease-fenced swap already refuses another owner, so removing the check does not fail this test.
- **`TestVersionFourJournalCarriesFailoverHistory`**: a version 4 journal still supplies the run's failover history.
- **`TestEvacuationRollbackRefusesChangedLedger`** covers seven changes, each of which keeps the journal and holds the run `STOPPING`, and none of which reaches the generator's capacity RPC:
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
- **`TestVersionFourCommittedEvacuationIsPromoted`**: a version 4 `replacement_committed` journal is promoted and its replacement adopted, as for version 3.
- **`TestAdoptPodCapacityAcceptsCurrentTermStandby`** (`pkg/storage`, and the PostgreSQL integration test): adoption is refused while a standby reserved under the new term is still pending, and succeeds once it is committed. The existing `TestAdoptPodCapacityRequiresEveryStandby` still refuses an old-term standby that was left out.

## Review

Round 1 used two reviewers, one on safety and one on the tests and docs.
- **High:** after a problem such as a completed source, the journal was still replaced while the run stayed `RUNNING` on disk until Phase B. A successor in between could adopt the plain checkpoint and finalize the run `COMPLETE`. Fixed: the journal is replaced only after a complete rollback, and a failed rollback is held `STOPPING` at once.
- **Medium:** an unfenced replacement bound an adoption failure whose stop proof could not read the journal. The run was refused both recovery in that term and administrator attestation. Fixed: no runtime is installed, the adopted participants are force-stopped, and the run is eligible for attestation.
- The tests-and-docs reviewer was stopped before it reported, because its snapshot predated those fixes. Its scope moved to round 2.

Round 2 used two fresh reviewers. The safety reviewer confirmed both round-1 fixes and found:
- **Medium:** a version 4 journal left by a clean same-leader rollback kept a prior topology that went stale as VUsers changed. Fixed by `abandonRunEvacuationCheckpoint`.
- **Low:** a panic before the journal was replaced bound an ordinary adoption failure, which installed a runtime and blocked attestation. Fixed with a rollback-specific recovery.
- **Low:** failover history was not loaded from version 4 journals. Fixed in `setContinuityCheckpointLocked`.
The tests-and-docs reviewer of round 2 found weak tests: three mutations of safety checks survived. Its findings and the fixes:
- **Tests added:** changed replacement and standby rows, every capacity-journal validation (each asserting its error), predecessor release states, unprovable targets, a paused run, the journal swap failure, the same-term skip, and version 4 promotion. The fail-closed cases now check `evacuationRollbackGuard` directly. All three mutations now fail the tests.
- **Storage:** the exemption now also requires the standby to be committed, with a negative test for a pending one.
- **Remaining:** now lists the real-generator drain proof and the write failures that are still untested.
- **Docs:** the README.md entry names its prerequisites and the attestation, and the spec no longer contradicts itself.
- **Nit:** a missing claim result now has its own message. `resumeInterruptedRunEvacuations` also skips a `rolling_back` journal when the current term cannot be read.

Round 3 used two fresh reviewers. The safety reviewer confirmed the round-2 fixes and found:
- **Medium:** a rollback candidate whose prior checkpoint could not be used was marked `FAILED` by `failRunAdoption`. Before this change it was held `STOPPING`. Fixed: it is held `STOPPING`.
- **Low:** a panic inside the rollback failure handling reached `failRunAdoption`. Fixed in `isolateRunAdoption`.
- **Low:** a clean same-leader rollback replaced the journal before returning the capacity, which could lose the standby pre-image. Fixed: capacity is returned first, and the journal is kept if that fails.

A mutation pass after round 3 removed seven safety checks in `run_evacuation_rollback.go`. Tests were added for the two that survived and have their own effect: the replacement-row state and the standby generation bound. Now every removed check fails the tests except the defence-in-depth owner check described above. The force-stop mutation did not compile, but its effect is asserted by `assertFailedRollbackHeld`.

## Remaining

- **Failure injection.** REC-A's exit still requires failure injection at every journal boundary, worker or node partition, late old-owner return, and independent target counters. These tests use fake generators.
- **Not covered.**
  - a restart of a missing replacement for the eligible HTTP workload;
  - replay of source-fenced work.
- **Drain before release is not proven against a real generator.** That the replacement's epoch claim drains its staged assignment before the capacity release is accepted rests on the generator's existing epoch semantics. These tests use fake generators that accept every release.
- **The same-leader journal restore** (`abandonRunEvacuationCheckpoint`) is tested directly. Its four call sites in the evacuation flow, and the capacity-first ordering, have no integration test.
- **Some write failures are untested.** A failed `rolling_back` intent write aborts that Phase A attempt, and Phase A is retried. A failed `STOPPING` write in `failRunEvacuationRollback` is retried in the background. Neither has a dedicated test.
- **A completed source fails the run.** That happens even when the source finished naturally after a same-leader rollback, because it cannot be told apart from a stop issued before the controller was lost.
- **A failed rollback** leaves the run `STOPPING`, holding its capacity and timeslot, until a newer term fences it or an administrator attests that its generators stopped. That includes an unfenced replacement, a completed source and an unconfirmed capacity step.
