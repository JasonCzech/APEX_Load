# Evacuation completion (REC-A)

Status: implemented. Qualified locally only, with fake and in-process generators. Three review rounds. Round 1 found no High, five Medium and several Low findings, and round 2 no High, two Medium and several Low; all are fixed. Round 3 found no High or Medium, so review stopped there; its Low findings are recorded, not fixed (see Review).

The [rollback](../roadmap-evacuation-rollback-2026-10-22/README.md), [roll-forward](../roadmap-evacuation-rollforward-2026-10-24/README.md), [generator terminal cause](../roadmap-generator-terminal-cause-2026-10-24/README.md) and [replacement restart](../roadmap-evacuation-replacement-restart-2026-10-24/README.md) records left three local gaps in REC-A. This slice closes them:

1. a run that finished while its generator still finalized the archive held every recovery, because the adoption answer could not say so;
2. live tables a lost lease term of the same process left in memory were never finalized durably, and their memory and budget stayed counted;
3. several REC-A exit cases were not exercised: a capability mismatch, no spare capacity, a stop during a roll-forward, a pause across a second leader change, and refusal by older controllers.

Deployed-cluster qualification is out of scope.

## Contract

### A completion still finalizing its terminal outcome

- **Wire (additive).** `RunAdoptionResult` gains `finalizing` (field 7, `bool`); the client maps it to `RunAdoptionResult.Finalizing`. A field was chosen over a new `RunAdoptionOutcome` value:
  - with a field, the outcome keeps the value it had before: `ALREADY_STOPPED` when the run's supervision tombstone exists (a supervised run that completed naturally, was stopped or failed leaves one in the tests), `UNKNOWN_TEST` otherwise. An older controller ignores the field and behaves exactly as before;
  - an older controller would treat an unknown outcome value as a refusal everywhere, including the restart's absence proof, which accepts `UNKNOWN_TEST`. A new value would therefore have changed its behaviour.
- **When a generator sets it** (`run_finalizing.go`). The adoption names a run with no assignment and no terminal tombstone yet, and the run's finalizing mark matches the adoption's prior generation and supervision token. Another lineage is told nothing.
- **The guarantee behind the flag: the assignment is gone and every execution ended.** Only `finishStoppedTestWithState` sets the mark, under `s.mu`. Each of its callers has already removed the assignment and seen every execution end:
  - `finishExecutionMatching`, for the last group: each group's callback runs after its engine's `RunAt` (or pooled owner) returned;
  - the completion timer of an assignment without executions;
  - `finishDetachedFencedTest`, after `waitForGeneratorExecutions` (controller stop, fence, lease expiry, failure, staged expiry).

  A drain that never ends escalates to self-termination and is never marked. The mark records the assignment's generation and token hash, and `markRunFinalizingLocked` refuses while an assignment of that generation is still in place. `completeStoppingTest` clears it after the terminal tombstone is published. Until then the stopping barrier keeps the test ID from being assigned again.
- **A narrow window stays conservative.** Between a caller releasing `s.mu` after removing the assignment and the mark, a claim reads the earlier answer and the run is held, as before this slice. A generator restart during finalization loses the mark, so a claim reads `UNKNOWN_TEST`, as before.
- **Test seam.** `WithArchiveFinalizationGate` calls a test's gate after the mark and before archive finalization; production generators leave it unset.
- **Controller wait** (`run_adoption_finalizing.go`). `claimRunAdoptions` runs `awaitFinalizingRunAdoptions` for each generator after its first claim:
  - while any answer is flagged, it claims again every 100 ms at the generation it already holds. Such a claim can only confirm (`adoptRunsLocked` with `rebind` false), and it carries only the finalizing runs' adoptions;
  - only flagged answers are replaced. A run the first claim refused or drained keeps that first answer, which is the one the recoveries judge;
  - a failed re-claim keeps the earlier answers and is tried again;
  - each re-claim's context ends with the wait, so a re-claim that hangs cannot hold the pass past it;
  - the wait first gives up the generator's claim slot, so no other generator's first claim, which carries its adoptions, queues behind it;
  - the wait is bounded by `defaultRunAdoptionFinalizingWait` (5 s; `distributedExecutionBackend.runAdoptionFinalizingWait` in tests) and by a third of what is left of the claim context (Phase A's 15 s budget and the lease), so the later steps that share that context (client retention, the roll-forward's commits, the restart's preparation and the capacity transfer) keep most of it. That is well inside the supervision TTL the first claim gave every adopted run (at least 30 s).

  Every caller gets the wait: Phase A (ordinary adoption, committed promotion, rollback, roll-forward, restart) and Phase B's stop proof (`confirmAdoptedRunStopped`). The interrupted-evacuation fence recovery claims no adoption, so it never waits.
- **The published outcome is judged by the existing paths.** A `DURATION_ELAPSED` completion at no less than the expectation is natural. A controller stop, an early end or a short duration is an unrecorded completion and holds the run. A `FAILED` run stays a problem, and so does `ALREADY_STOPPED` without a terminal state. Ordinary adoption takes the same wait, so a participant that finished during the gap is read as completed rather than failing the run with `ALREADY_STOPPED`; no test drives that path separately.
- **Still finalizing when the wait ends.** The answer keeps its earlier outcome and the flag, and the run is held as before. The restart's absence proof (`runEvacuationRestartAbsence`) refuses such an answer even when its outcome is `UNKNOWN_TEST`: the outcome the generator will publish is unknown.
- **Older peers and no new setting.** An older generator never sets the flag, so the controller makes no extra claim. An older controller ignores it. There is no setting and no journal change.

### Live tables of a lost lease term

- **Release** (`live_tables_lost_term.go`, `releaseLostTermLiveTables`). When this process acquires a new lease term, it removes from memory every run's live tables restored or prepared under another term:
  - when: in `reloadAndReconcile` right after the active reload, and again at the start of Phase A. That covers deployments without run adoption, and Phase A callers;
  - what: the tables are marked closed and leave `tr.liveTables`, so they no longer count against the run, project or controller budget, and a candidate's restore in Phase A never counts them. Their own budget field is left alone, so a restore in progress still deducts it from the recovery reservation;
  - kept: a summary already released after its finalization holds no budget and stays for the run view. The run view of a released run reads storage: `unavailable` for an active record, `closed` for a closed one.
- **No lost-term writer can write.** Its live clients, checkpoints and finalization check the table's term and refuse once the term ended. Storage requires the writer to hold the lease term (`beginLiveTableTx`). After the new term's restore, the owner of every table row is the new term (`ClaimLiveTable`).
- **Durable finalization under the new term** (`finishUnrestoredLiveTables`). A terminal writer (`finishLiveTables`) that finds no tables of its term in memory finalizes from storage, when all of these hold:
  - the controller runs with the durable execution fence;
  - the run has live data pools;
  - the writer holds the registered run object and the current term's fence. A lost-term writer, which holds the old object or the old fence, does nothing.

  It releases any ended term's tables still registered, then:
  - **checks storage** (`liveTablesAwaitingFinalization`): something is left if a durable table is still active, or closed without a finalization result, or a project-table attachment of the run is still active. Tables kept only in memory are skipped, not a reason to stop;
  - **restores** the tables under its own term for finalization only (`restoreRunLiveTables` with `finalizeOnly`): it claims each durable table and skips tables kept only in memory (the run view and the archive summary still list them, as unavailable, from their storage records); it does not require a table a binding names but storage never initialized, which a lease lost while the run launched leaves behind; it takes the run's active project-table attachments as they are (`collectUnrestoredProjectAttachments`) instead of re-attaching them; it registers the tables closed, so they never serve; and it finalizes nothing itself;
  - **registers** the restored tables only if its term is still this process's term, checked under `liveTablesMu`. A restore of either kind whose term ended during its reads registers nothing and fails, so it never replaces the tables a newer term restored; the attempt's retry then ends with the term;
  - **finalizes** them with the terminal status, which also detaches the project-table attachments. Attestation and fence recovery write `FAILED`, so a `discard` table is discarded and any other is recorded `pending` with `run_not_complete` for explicit recovery. A table the lost term had closed keeps its closed snapshot and is finalized the same way.

  Every failure, the restore's and the finalization's, returns to one retry under `liveTableRetryKey` with the current term; the retry ends with the term. A restore failure no retry can change (`liveTableRestoreUnrecoverable`: a stored table no binding names, or for an adoption restore an incomplete manifest, a run budget the bindings exceed, a binding mismatch, invalid metadata, corrupt recovery state, the recovery byte limit) ends the attempt without a retry and leaves the tables as storage has them; each attempt would otherwise claim and read the tables again every second for the whole term.
- **Tables kept only in memory.** A `memory` table exists only for a run that fails on controller loss. No other term can claim it, and storage holds no content for it. It is released, its record stays as the lost term left it, and nothing is finalized or retried for it; a durable table of the same run is finalized as above.
- **Project-table attachments** of a held run are detached when the run ends, even when the run has no run-scoped tables, so the project table can quiesce and be evicted. Independently, the project-table maintenance sweep detaches the attachments of any run that is no longer active.
- **A successor in another process** has no tables in memory. Its terminal write of a held run now takes the same path, which closes the earlier gap: live tables were never finalized durably for runs not adopted after a failover.

### REC-A exit cases

- **Capability mismatch.**
  - At the roll-forward: a replacement whose generator predates run adoption cannot be adopted, and its claim proves nothing about what it runs. The run is held, and with the restart on, its share is not restarted ("unreachable, which does not prove it stopped").
  - A replacement without the duration control (`AdjustDuration` unimplemented) cannot be shortened to the commit target. The run is held and its capacity is not committed.
  - At the restart, from a `source_fenced` journal or the roll-forward fallback: a spare without `evacuation_assignment_state_v1` is never given the share. The run is held with "no healthy generator with sufficient capacity".
  - An adopted replacement is not checked again for protocol capability: it already runs the share.
- **No spare capacity.** A roll-forward needs none: the replacement's reservation is journaled, and a `replacement_committing` journal rolls forward with no spare generator in the fleet. A replacement not adopted at `replacement_committing` or `rolling_forward` (from an earlier successor's stored intent) falls back to a restart. With no spare generator, or a spare without room, the restart holds the run under its intent and reserves nothing.
- **A stop during a roll-forward.** Phase A runs inside acquisition reconcile, before admission, so an operator's stop is refused as not admitted (`errNotExecutionLeader`) and the roll-forward completes. Retried after admission, before Phase B, the stop makes the installed run `STOPPING` and reaches the survivor and the replacement at the new generation with the rotated token. The test constructs the admission state itself (`reconciling`), because it calls Phase A directly rather than through the lease's acquisition callback. A stop made durable before the failover leaves the run `STOPPING`, which is never eligible (the roll-forward record's fail-closed test).
- **A pause across a second leader change.** A paused run's roll-forward begun by a successor that was lost, after its intent or after its claim, is rolled forward by the next term from the stored `rolling_forward` intent. The run stays `PAUSED`. A durable pause state that no longer matches the staged one is held. The fake survivor and replacement check proof chains as generators do, so after the first successor's claim only an adoption naming that successor's generation and token is accepted.
- **Rolling-version refusal, simulated.** `runEvacuationReadableVersion()` (6 unless a test sets the atomic `runEvacuationReadableOverride`) gates the three journal version checks. Lowering it reads a journal with every version check of an older release; the rest of that release's code is not reproduced. A version 4, 5 or 6 journal read by every older reader (versions 3 to 5) is:
  - not an adoption candidate and not an interrupted evacuation;
  - never promoted;
  - held `STOPPING` with the interrupted distributed stop reason, its journal unchanged and no generator claimed.

  Read by its own release, each is recognized.
- **No defect found.** These cases needed fake-generator knobs (`noAdoptionSupport`, `adjustErr`, graceful stops) and a heartbeat feature filter for the in-process spare. The only production change they needed is the `runEvacuationReadableVersion` seam, which leaves behaviour unchanged at its default. Round 1 found that the first version of the stop test passed for the wrong reason: Phase B failed the run on the fakes' missing metrics streams before the stop, and the stops it checked were Phase B's. The test now stops the run Phase A installed and asserts `STOPPING`.

## Invariants

- **A finalizing answer is never taken as an outcome.** It only starts a bounded wait. When the wait ends, the run is judged on the published outcome, or held.
- **A finalizing answer is truthful.** It is given only when the generator removed the assignment and every execution ended, for that exact lineage.
- **Nothing changes for older peers.** The outcome field carries the same value as before.
- **A run's live tables are never written by two terms.** Only the current term's registered writer restores or finalizes them, after claiming them in storage, and the lost term is refused in process and by storage.
- **Released live tables keep their durable state.** Their journal and checkpoint stay exactly as the lost term left them, until the new term finalizes them.
- **Strict-fail defaults.** No setting was added, and every refusal holds the run as before.

## Validation

- **`TestAdoptionReportsARunStillFinalizing`** (`pkg/distributed/generatorrpc`, real executing runs; the archive finalizer slots are held). Each case is supervised, so its earlier outcome is pinned to `ALREADY_STOPPED`:
  - a run whose duration elapsed, one a controller stopped and a failed one each reach the window with no assignment and no execution left;
  - a successor's claim, and a claim of the same generation, read the flag with the earlier outcome `ALREADY_STOPPED`, no terminal state and no cause;
  - another supervision token reads no flag, and a chained proof (the lineage only in an older proof) reads it;
  - decoded with a `RunAdoptionResult` schema built without field 7 (`protodesc`, `dynamicpb`), as an older controller reads it, the answer has the same outcome and keeps the flag as an unknown field;
  - after the release, the mark clears and a claim reads the published outcome, cause and duration.
- **`TestDrainingStopIsNotReportedFinalizing`**: a graceful stop removes the assignment while the VUser's request is still in flight at a held target. A claim made then reads no flag, and the test checks the request had not ended. Once the request ends, the run is finalizing, and then terminal with `CONTROLLER_STOP`.
- **`TestRunFinalizingMarkRequiresTheExactLineage`**: no mark while the assignment of that generation is in place; another generation or token is not finalizing; only its own generation clears it; a run without a supervision token is never finalizing.
- **`TestEvacuationRecoveryWaitsForAFinalizingCompletion`** (`pkg/api`, fake generators):
  - a roll-forward replacement and a survivor that finished naturally, and a rollback source that answered `UNKNOWN_TEST` while finalizing, are each asked again (same generation, only that run's adoption) and rolled forward or back with the participant completed. The swapped checkpoint records it; the survivor adopted by the first claim is not asked again;
  - a replacement whose published outcome is a controller stop is held as stopped by a controller;
  - a replacement or rollback source still finalizing when a 200 to 300 ms wait ends is held with the finalizing detail, after re-queries, within the bound;
  - every claim, first and re-query, is at Phase A's generation (the fake records each claim's generation);
  - an older generator that answers `ALREADY_STOPPED` without the flag is asked once and held.
- **`TestEvacuationRestartRefusesAStillFinalizingReplacement`** and **`TestRunEvacuationRestartAbsenceRefusesFinalizing`**: a replacement whose `UNKNOWN_TEST` answer is still flagged after the wait is not restarted, and the run is held with the reason. Without the flag, `UNKNOWN_TEST` is still absence proof.
- **`TestAwaitFinalizingRunAdoptionsReplacesOnlyFinalizingAnswers`**: two runs on one generator, one finalizing and one `REJECTED`. Re-queries carry only the finalizing run, its published completion replaces its answer, and the refused run keeps its first answer, although a later claim would read it differently.
- **`TestFinalizingWaitDoesNotHoldAClaimSlot`**: seventeen generators, one more than the claim slots, all still finalizing. Each re-query blocks until every generator's first claim arrived; none waits in vain, which is only possible if a waiting generator gave up its slot.
- **`TestFailedRequeryIsTriedAgain`**: the second claim fails; the third, at the same generation, publishes the completion, which replaces the flagged answer.
- **`TestFinalizingWaitLeavesTheClaimContextMostOfItsTime`**: with a 10 s wait configured and 1.5 s left in the claim context, the wait ends within a third of it.
- **`TestFinalizingWaitBound`**: `finalizingWait` is 5 s by default, the configured value without a deadline, and between 400 and 500 ms with 1.5 s left. The "still finalizing" cases also bound the elapsed time (2 s) and the number of claims (8).
- **`TestHungRequeryEndsWithTheWait`**: a re-query that hangs until its context ends, with a 10 s backend timeout, holds the pass less than 1 s past a 300 ms wait.
- **`TestEvacuationRollForwardWaitsForARealFinalizingReplacement`** (in-process replacement, 500 ms start barrier, 10 s RPC timeout; the generator's archive finalization gate holds the window). A committed replacement whose staged duration ran out has no active VUser and answers the successor's first claim `ALREADY_STOPPED` with the flag:
  - **published within the wait**: released after that answer, the re-query reads `ALREADY_COMPLETED`, `DURATION_ELAPSED`, at the commit target, and the run rolls forward with the replacement completed;
  - **not published within a 500 ms wait**: the run is held with the finalizing detail;
  - in both, every claim was at Phase A's generation, and the generator's epoch is still that generation afterwards.
- **`TestLostTermLiveTablesOfAHeldRun`** (attestation and fence recovery). A durable live table is prepared and served by the lost term; its run has a version 4 `source_fenced` journal and is fenced to the lost owner. Then, through `reloadAndReconcile`:
  - the run is held `STOPPING` on a new object;
  - the lost term's tables leave memory, the budget in use drops to zero, the lost object is closed, and its client refuses;
  - storage is unchanged: active, owned by the lost term, with its revision, and not finalized;
  - a lost-term writer holding the old object leaves the run's row `STOPPING` and the table's record unchanged, and leaves no retry; so does that unregistered object when it carries the current term's fence, and the registered object while it carries the lost term's fence;
  - the attestation, or the newer term's fence recovery, leaves the table closed and owned by the new term, with a `discard` finalization, and no retry.
- **`TestLostTermLiveTablesClosedBeforeTheirFinalization`**: the lost term closed its table, checkpoint and record included, and was lost before the finalization. The new term restores and finalizes it, and leaves no retry.
- **`TestUnrestoredLiveTableFinalizationFailureInsideTheRetry`**: the same closed table; the first attempt fails before the restore, and on the retry the finalization fails once. The one retry goes on and ends with the table finalized under the new term.
- **`TestLostTermDurableTableBesideAMemoryTable`**: a fail-policy run with a durable and a memory table. The durable one is finalized under the new term; the memory one stays active under the lost owner.
- **`TestLostTermProjectTableAttachmentIsDetached`**: a held run bound only to a project table. After the attestation its attachment is detached and settled.
- **`TestUnrecoverableUnrestoredLiveTablesAreNotRetried`**: two stored tables and a run that names only one; the attestation leaves no retry and both tables as storage has them.
- **`TestUnrestoredLiveTablesWithAnIncompleteManifest`**: a binding whose table storage never initialized; the table that exists is finalized under the new term.
- **`TestUnrestoredLiveTableRestoreOfAnEndedTermIsNotRegistered`**: a test barrier after the restore's reads makes the same leadership object lose the lease and win it again, and registers the newer term's tables. The ended term's restore fails as expired, the newer tables stay registered, the budget counts only theirs, and the ended term's next attempt gives up.
- **`TestFinalizeOnlyRestoreNeverServes`**: tables restored only to be finalized refuse a client request before any finalization.
- **`TestLostTermDurableTableBesideAMemoryTable`** also reads the run view and the archive summary after the attestation: the durable table is closed and the memory table is listed as unavailable.
- **`TestLostTermLiveTablesAlreadyFinalized`**: the lost term finalized its table completely. The attestation neither restores nor claims it again.
- **`TestUnrestoredLiveTableRetryEndsWithItsTerm`** (on a store that embeds the SQLite store, so it recovers for real; it waits for the asynchronous loss before the new term, and passed in 20 of 20 fresh processes): a finalization that fails while storage cannot list the tables is retried. The same leadership object then loses the lease and wins it again under a newer term, and storage recovers. The retry ends, an attempt of the ended term gives up without touching storage, and nothing is claimed or finalized. (A first version swapped the runner's leadership object, which the race detector flagged against the running retry; production never swaps it.)
- **`TestLostTermLiveTablesOfAnUndecodableCheckpoint`**: the predecessor's pinned-secret ciphertext is unusable, so Phase A holds the run before any restore and claims no generator. The tables are released, and the attestation finalizes them under the new term.
- **`TestLostTermLiveTablesOfAnAdoptedRun`**: a roll-forward candidate's tables are restored under the new term. The lost term's object is gone, and the budget in use is unchanged, not doubled.
- **`TestLostTermMemoryLiveTablesAreOnlyReleased`**: a fail-policy run's memory table, through `reloadAndReconcile` with run adoption unavailable, is released. After the attestation its record stays active under the lost owner with no finalization, and nothing is retried.
- **`TestReleaseLostTermLiveTablesKeepsTheCurrentTerm`**: only the other term's live tables go; the current term's tables and a released summary stay, and the budget counts the current term's.
- **`TestEvacuationExitCapabilityMismatch`**: a roll-forward replacement that predates run adoption (also with the restart on), one without the duration control, and a spare without the evacuation assignment state at the restart and at the roll-forward fallback. Each holds the run with its reason; nothing is assigned to the incapable spare, and no capacity is committed for the unshortened replacement.
- **`TestEvacuationExitNoSpareCapacity`**: a `replacement_committing` roll-forward with no spare generator completes. At `replacement_committing`, and at a stored `rolling_forward` intent taken over by another controller, a replacement that is not adopted, with no spare generator or a spare without room, is held under the restart intent with the reason, and the spare gets no assignment or reservation.
- **`TestStopRequestedWhileTheRollForwardRuns`**: as described in the Contract.
- **`TestPausedRollForwardAcrossASecondLeaderChange`**: after the intent or after the claim, the second successor rolls the durably paused run forward with the first successor's token proof, and the run stays `PAUSED`. A durable resume since staging is no candidate and is held, with its journal unchanged.
- **`TestOlderControllersHoldNewerJournals`**: version 4 `staged`, version 5 `replacement_committing` and a version 6 restart intent, read by every older reader and by their own release, as described in the Contract.
- **`TestEachJournalVersionCheckRefusesANewerVersion`**: each of the three version checks, on its own, accepts its versions at this release and at their own reader, and refuses them one reader older.
- **Existing suites**: the rollback, roll-forward, restart, terminal cause, adoption, continuity and live-table tests pass. The roll-forward fixture's assertion gained a status parameter (`assertRolledForwardWithStatus`).
- **Mutation pass** (`go test -overlay`): see Mutations.
- **Gates**:
  - `go build ./...`, `gofmt` and `golangci-lint run ./pkg/api/... ./pkg/distributed/...` (0 issues);
  - `go generate ./...` with no drift, and the proto regenerated with protoc 29.3 and protoc-gen-go 1.36.11, identical on a second run;
  - `go test ./pkg/distributed/...`;
  - the full `pkg/api` suite with a 45 m timeout, in twelve shards of one test binary: only the six `TestReplayArtifact*` tests already known to fail on macOS fail;
  - the new tests under `-race -count=2 -shuffle=on`;
  - `go test ./pkg/storage -run LiveTable`, and with `-tags postgres_integration` against the gate PostgreSQL container when it is reachable;
  - `python3 scripts/check-docs.py`.

  `pkg/engine` is untouched and was not run.

### Mutations

Applied with `go test -overlay` against the tests above. A mutant that failed to build was rewritten. Before review: 32 caught, 3 survived (below).

- **Caught, generator:**
  - the mark never matching; the token check dropped; the generation check dropped;
  - the mark set while the assignment of that generation is in place;
  - the mark never cleared;
  - the mark set at a stop's detach, while executions still drain;
  - `finishStoppedTestWithState` not marking;
  - the finalizing answer changing its outcome;
  - the flag never set;
  - the client dropping the flag (by the in-process roll-forward test).
- **Caught, controller wait:**
  - no wait at all;
  - re-queries carrying every adoption rather than only the finalizing ones;
  - an unbounded wait;
  - published answers not merged;
  - the wait keeping its claim slot;
  - the restart taking a still-finalizing `UNKNOWN_TEST` as absence proof.
- **Caught, live tables:**
  - no release; a release of the current term's tables; a release of finalized summaries;
  - the writer check dropped (a lost-term writer restored and finalized the tables);
  - no unrestored finalization at all;
  - a table kept only in memory treated as finalizable, and finalized tables treated as pending (after `TestLostTermLiveTablesAlreadyFinalized` was added);
  - the release dropped from Phase A, and from the acquisition reload;
  - the closing tables a restore finalized retried again (after `TestLostTermLiveTablesClosedBeforeTheirFinalization` checked for no retry);
  - a retry that goes on after its term ended (after `TestUnrestoredLiveTableRetryEndsWithItsTerm` was added).
- **Caught, exit cases:**
  - the version gate dropped from the three journal version checks;
  - the reshard placing a share on a generator without the evacuation assignment state;
  - the adoption-support check dropped;
  - the roll-forward's pause-state check dropped;
  - both admission checks of the stop path dropped.
- **Survived, and the code was simplified instead.**
  - The mark in `finishExecutionMatching`'s locked section, and in the timer path: `finishStoppedTestWithState` marks the same run moments later, so only the narrow window described under Remaining told them apart. Both were removed.
  - A finalizing clause in `selectAdoptionPriorLocked`: a chained proof already finds the lineage through the supervision tombstone. It was removed.
- **Survived and kept.** Dropping only the first of the stop path's two admission checks is equivalent, because the second check refuses the same stop.
- **Overstated before review.** The version-gate mutant dropped the gate from all three checks at once; only `supportedRunEvacuationCheckpointVersion` did any work in those tests. `TestEachJournalVersionCheckRefusesANewerVersion` now pins each check.

After review round 1, 24 mutants, all caught:
- **new:**
  - re-queries at generation + 1;
  - a failed re-query ending the wait;
  - no cap on the wait;
  - a failed finalization after the restore reported as success;
  - a memory table stopping the precheck, and the finalization-only restore not skipping memory tables;
  - the precheck ignoring active project-table attachments, and no attachments collected;
  - an unrecoverable restore retried;
  - the writer check without its fence half;
  - the adoption intent without the earlier successors' proofs;
  - the gate dropped from each of the three version checks on its own;
- **re-run on the reworked code:** every live-table release, writer, unrestored-finalization, precheck and ended-term mutant above, and the wait keeping its claim slot.

After review round 2, 12 mutants, all caught (the wrong-token mutant was also run against "after intent" alone, where it survives, as intended):
- registration without the term check;
- finalize-only tables registered open (after `TestFinalizeOnlyRestoreNeverServes` was added);
- an incomplete manifest refused in finalize-only mode, and any manifest accepted;
- memory tables left out of the archive summary, and out of the run view;
- re-queries on the claim context instead of the wait's;
- a cap of a half instead of a third, and the configured wait ignored;
- the writer check without its registered-object half;
- a memory table counted as pending (by `TestLostTermMemoryLiveTablesAreOnlyReleased`);
- an adoption whose proof of the first successor carries the wrong token: caught only by the "after claim" case of `TestPausedRollForwardAcrossASecondLeaderChange`, where the fake holds the run under that successor's token, and not by "after intent". This is the evidence that the EX-M2 fix gives "after claim" its own meaning.

## Review

Round 1 found no High issue, five Medium and fourteen distinct Low (fifteen listed; tests-L3 repeats FIN-L1). All are fixed:
- **LT-M1, a failed finalization inside a retry was lost** (reproduced by the reviewer). The restore's own recovery finalization queued its retry under the key the outer retry already held, so it was refused, and the outer attempt returned success. Fixed: the unrestored path restores for finalization only, finalizes nothing inside the restore, and returns every failure to its one retry. `TestUnrestoredLiveTableFinalizationFailureInsideTheRetry`.
- **LT-M2, a memory table kept its durable sibling from being finalized** (reproduced). Fixed: the precheck and the finalization-only restore skip memory tables. `TestLostTermDurableTableBesideAMemoryTable`.
- **LT-M3, project-table attachments of a run the new term never restored were not detached** at the run's end. Fixed: active attachments count as pending, and the finalization-only restore collects and detaches them, with no run tables at all. `TestLostTermProjectTableAttachmentIsDetached`. The maintenance sweep, which detaches attachments of runs no longer active, was a backstop already.
- **FIN-M1, re-queries were not checked for their generation.** Fixed: both fakes record each claim's generation, the tests require Phase A's, and the in-process test checks the generator's epoch afterwards.
- **EX-M2, the "after claim" case added nothing** because the fake checked no proof chain. Fixed: the fake can track the lineage it holds the run under and accept only adoptions whose proofs name it.
- **Low:**
  - **FIN-L1, a failed re-claim was untested:** `TestFailedRequeryIsTriedAgain`.
  - **FIN-L2, the wait could starve later Phase A steps:** capped at a third of what is left of the claim context; `TestFinalizingWaitLeavesTheClaimContextMostOfItsTime`.
  - **LT-L1, unrecoverable restores retried every second:** they now end the attempt; `TestUnrecoverableUnrestoredLiveTablesAreNotRetried`.
  - **LT-L2, the failing store could not recover:** it embeds the SQLite store.
  - **Tests-L1, the earlier outcome was not pinned:** pinned to `ALREADY_STOPPED`.
  - **Tests-L2, the older-controller decode used the new schema:** it decodes with a schema built without field 7.
  - **Tests-L3, a failed re-claim:** same as FIN-L1.
  - **Tests-L4, the writer check's fence half was untested:** a case with the registered object carrying the lost term's fence.
  - **Tests-L5, the budget zeroing:** removed. The remaining defensive branches are commented as defence in depth; the "restored by an earlier attempt" branch is exercised by `TestUnrestoredLiveTableFinalizationFailureInsideTheRetry`.
  - **Tests-L6, the rollback still-finalizing case:** asserts the re-queries, their generation and the time bound.
  - **Tests-L7, the slot test's timing margin:** replaced with a structural check.
  - **Tests-L8, the stop test:** asserts `STOPPING` exactly, and states that it constructs the admission state. It also fixed the test, which had passed on Phase B's stops (see Contract).
  - **Tests-L9, the version seam:** an atomic override behind `runEvacuationReadableVersion()`.
  - **Tests-L10, the version mutation claim:** corrected, and each check pinned.
  - **Tests-L11, record wording:** the roll-forward and restart records' Remaining, the spec, the plan and this record's lost-writer and mutation-count claims are corrected.

Round 2 found no High issue, two Medium and eight Low. All are fixed:
- **CS-M1, a finalize-only restore whose term ended mid-restore registered its tables over a newer term's** (reproduced). Fixed: a restore registers only while its term is current, checked under `liveTablesMu`, and finalize-only tables are registered closed. `TestUnrestoredLiveTableRestoreOfAnEndedTermIsNotRegistered`, `TestFinalizeOnlyRestoreNeverServes`.
- **CS-M2, `TestUnrestoredLiveTableRetryEndsWithItsTerm` flaked** (5 of 20 processes): the loss is handled asynchronously, so the term read empty right after `tick()`. Fixed: a shared `loseAndWinAgain` helper ticks until the newer term is visible; 20 of 20 fresh processes pass.
- **Low:**
  - **CS-L3, the run view hid a finalize-only run's memory table:** skipped records are kept on the run and listed as unavailable in the view and the archive summary.
  - **CS-L4, a hung re-query was not bounded by the wait:** each re-query's context ends with the wait; `TestHungRequeryEndsWithTheWait`.
  - **CS-L5, a data race on `releasedAt`:** read under `liveTablesMu` (`liveTablesReleased`).
  - **Design note, incomplete manifests:** finalize-only mode finalizes the tables that exist; `TestUnrestoredLiveTablesWithAnIncompleteManifest`.
  - **TD-L1, the wait bound was not pinned:** `TestFinalizingWaitBound`, and elapsed and claim-count bounds in the "still finalizing" cases.
  - **TD-L2, the registered-object half of the writer check was untested:** a case with the unregistered lost object carrying the current fence.
  - **TD-L3, stale comments:** the closed-table test now says the terminal writer finalizes.
  - **TD-L4, Validation overclaims:** the `UNKNOWN_TEST` alternative is dropped, and the EX-M2 evidence is the wrong-token mutant above.
  - **TD-L5, the memory-table test missed a mutant:** it asserts nothing is restored after the attestation.

Round 3 found no High or Medium, so the review stopped after it. Its code reviewer confirmed the round 2 fixes: the term check under `liveTablesMu` takes only the lease's own locks, which are never held while `liveTablesMu` or `tr.mu` is taken, so it cannot deadlock; finalize-only tables registered closed are finalized normally and refuse clients; a partial manifest finalizes only the run's own tables through the lease-checked claim. It ran the related `pkg/api` and `generatorrpc` tests, the new tests under `-race -count=2 -shuffle=on`, and the storage live-table tests on SQLite and PostgreSQL. Its tests reviewer caught all twelve round 2 mutants and ran both term-change tests 25 of 25 times one at a time and 48 of 48 with twelve processes at once; the still-finalizing cases stayed far inside their bounds under `-race` (316–335 ms against 2 s, two or three claims against eight). Their Low findings are recorded, not fixed:
- **A skipped memory table is always shown `unavailable`.** The run view (`handlers_live_tables.go`) and the archive summary (`live_tables_archive.go`) list a finalize-only run's skipped memory records as `unavailable` with no finalization, even when storage holds them `closed` with a finalization, which the storage-backed view would show. The frozen archive and report keep the `unavailable` state. Fix: take the state from the record and read its finalization during the finalize-only restore.
- **The lost-term release trusts the term it is given.** `releaseLostTermLiveTables` does not re-check that the caller's term is still current under `liveTablesMu`; a full lose-and-win cycle between `finishUnrestoredLiveTables` capturing the term and the call would drop the current term's tables. Reaching it needs that cycle inside one lock wait with the object and fence checks lining up. Fix: check `executionLeaseTermLocallyCurrent(term)` inside the function.
- **`TestFinalizingWaitBound` compares with the default constant**, so a changed default (for example 30 s) passes; the "5 s by default" claim above is not pinned.
- **The registration term check is tested for the finalize-only restore only.** Limiting it to finalize-only survives; an ended adoption restore registering is untested, though the claim above covers both kinds.
- **The ended-term test's `"owner expired"` match also matches the finalization error** the no-check mutant returns; the mutant is still caught by the next assertion.
- **Record wording.** The 10 s backend timeout in the hung re-query bullet does not matter, because each re-claim is capped at 2 s by `maxGeneratorEpochKeeperTransit`; the test catches the mutant because 2 s exceeds its 1 s bound. The `TestLostTermDurableTableBesideAMemoryTable` bullet appears twice in Validation. An unnamed stored table is detected only by count, so in finalize-only mode one binding without a stored table and one stored table no binding names balance out and the unnamed table would be finalized. The comment "at most four requeries" means four claims.

## Remaining

- **Review.** Three rounds ran; round 3's Low findings are recorded under Review.
- **Deployed qualification.** REC-A's exit still requires failure injection on a deployed cluster at every journal boundary, worker or node partition, late old-owner return and independent target counters. These tests use fake and in-process generators.
- **Finalization longer than the wait.** An archive that takes more than 5 s to finalize, or a generator that restarts while finalizing, still holds the run, as before.
- **The mark's window.** A claim between a caller releasing `s.mu` and the mark still reads the earlier answer and holds the run.
- **Unrestored live tables that cannot be restored.** A restore that fails for a reason no retry can change (a stored table no binding names, corrupt state, a binding mismatch, the recovery byte limit) ends without a retry, and its tables stay as storage has them until an operator acts. Failures that may pass (the controller or project budget, a key service, storage) are retried with backoff until the term ends, as other live-table finalizations are. Tables kept only in memory are released but never finalized; their records stay active under the lost owner.
- **The wait shares Phase A's context.** It takes at most a third of what is left; many generators finalizing at once still each take their share in parallel, so a slow fleet can leave less time for the later steps than before this slice.
- **A stop between Phase A and the restart on a run with no survivor.** Unchanged: it finds no generator to stop, so the run stays `STOPPING` until a newer term fences it or an administrator attests, although this term proved nothing runs it ([restart record](../roadmap-evacuation-replacement-restart-2026-10-24/README.md)). Finishing it from that proof needs the intent replaced and the share's reservation released by the stop path, and is not implemented.
- **Older-reader simulation.** The rolling-version tests lower the version checks only. The rest of an older release's code is not reproduced, and a real older binary is not run.
- **Worker or node partition, and live tables at a restart.** Neither is exercised. A restart of a run with live tables restores them as a candidate does, but no test drives it.
