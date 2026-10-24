package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/api/testexec"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// HA run continuity, controller side (docs/HA_RUN_CONTINUITY_PLAN.md §5.5).
//
// A successor leader adopts continue-policy runs in two phases:
//
//   - Phase A, adoptOrphanedRuns, runs inside acquisition reconcile while public
//     admission is closed. It batches every run's adoption per generator into
//     that generator's first claim of the new generation (a claim that omits a
//     run fences it), then installs the run's remote handles with the rotated
//     supervision token so stop/abort work at once. Anything that cannot be
//     fully adopted is finalized FAILED through the ordinary distributed path.
//   - Phase B, resumeAdoptedRuns, runs first when admission opens. It confirms
//     supervision under the rotated token, opens metrics streams on the new
//     generation, and restarts metrics consumers and stage/profile drivers,
//     which recompute their position from the persisted run clock.
//
// The generator epoch keeper waits for Phase A (awaitRunAdoptionReconcile) so
// its first claim of the generation cannot fence the orphans first.
const (
	// defaultRunAdoptionGrace is how long generators keep an orphaned run alive
	// for a successor (decision D3). Generators cap it at 120s.
	defaultRunAdoptionGrace = 45 * time.Second
	// runAdoptionClaimBudget bounds the whole Phase A claim pass.
	runAdoptionClaimBudget = 15 * time.Second
	// runAdoptionKeeperWait bounds how long the epoch keeper defers to
	// reconcile before claiming anyway (orphans are then fenced, runs fail).
	runAdoptionKeeperWait       = 20 * time.Second
	maxAdoptedRunStopErrorBytes = 2048
)

var errUnprovableRunAdoptionStop = errors.New("adoption checkpoint cannot identify stop-proof targets")

type adoptedRunResume struct {
	generation uint64
	ownerID    string
	// adopted holds the generators now running the run under generation;
	// completed holds generators whose share finished during the gap.
	adopted   map[string]generatorClient
	completed map[string]generatorClient
	adoptedAt time.Time
	// finishStatus, when set, is an outcome Phase A already decided (FAILED
	// for an unadoptable run, ABORTED for an in-flight stop). It is applied in
	// Phase B because archive publication needs an admitted leader.
	finishStatus  string
	finishMessage string
	// finishTargets are the prior checkpoint's possibly-live participants.
	// A successor cannot publish finishStatus until each has positive stop or
	// terminal proof; a missing generator can outlive the controller lease.
	finishTargets []string
	// secured is set once Phase A has re-established the run's generator
	// leases and supervision under the rotated token; secureFailure carries a
	// failure for Phase B to apply (archive publication needs admission).
	secured       bool
	secureFailure string
}

func (tr *TestRunner) runAdoptionEnabled() bool {
	return runAdoptionImplemented && tr != nil && tr.leadership != nil && tr.durableExecutionFence && tr.continuityKeyWrapper != nil
}

// runAdoptionGrace is the adoption grace sent to generators for test, or zero
// when the run must keep today's fail-fast behaviour.
func (tr *TestRunner) runAdoptionGrace(test *ManagedTest) time.Duration {
	if !tr.runAdoptionEnabled() || test == nil || effectiveFailoverPolicy(test.Assignment) != failoverPolicyContinue {
		return 0
	}
	if validateLiveTableContinuation(test.DataPools, test.Assignment) != nil {
		return 0
	}
	return defaultRunAdoptionGrace
}

// isRunAdoptionCandidate reports whether a stored active run should be left
// RUNNING for adoption instead of being reconciled to FAILED.
func (tr *TestRunner) isRunAdoptionCandidate(test *ManagedTest) bool {
	if !tr.runAdoptionEnabled() || test == nil || !runAdoptionStatus(test.Status) {
		return false
	}
	if effectiveFailoverPolicy(test.Assignment) != failoverPolicyContinue {
		return false
	}
	if _, distributed := tr.execution.(*distributedExecutionBackend); !distributed {
		return false
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(test.continuityCheckpoint)
	if err == nil && present && len(checkpoint.Shards) > 0 {
		return true
	}
	if _, committed := committedRunEvacuationCheckpoint(test.continuityCheckpoint, test.ID); committed {
		return true
	}
	if _, _, rollback := tr.evacuationRollbackGuard(test, test.continuityCheckpoint); rollback {
		return true
	}
	_, _, rollForward := tr.evacuationRollForwardGuard(test, test.continuityCheckpoint)
	return rollForward
}

// awaitRunAdoptionReconcile holds the generator epoch keeper until generation's
// acquire-time adoption (Phase A) finishes, or reconcile ends without it,
// bounded so a stuck reconcile cannot leave generators unclaimed. Waiting for
// adoption alone, not the whole reconcile, lets the keeper claim the rest of
// the fleet as soon as orphans are safe.
func (tr *TestRunner) awaitRunAdoptionReconcile(ctx context.Context, generation uint64) {
	if !tr.runAdoptionEnabled() {
		return
	}
	deadline := time.Now().Add(runAdoptionKeeperWait)
	for tr.leadership.reconciling.Load() && tr.leadership.IsLeader() && tr.runAdoptionDoneGeneration.Load() < generation {
		if time.Now().After(deadline) {
			slog.Warn("generator epoch keeper stopped waiting for run adoption; unadopted orphans will be fenced")
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// markRunAdoptionDone records that acquire-time adoption finished for
// generation, releasing that term's generator epoch keeper.
func (tr *TestRunner) markRunAdoptionDone(generation uint64) {
	if tr == nil || generation == 0 {
		return
	}
	for {
		current := tr.runAdoptionDoneGeneration.Load()
		if current >= generation || tr.runAdoptionDoneGeneration.CompareAndSwap(current, generation) {
			return
		}
	}
}

// runAdoptionStatus is the set of stored statuses a successor adopts. A
// STOPPING run is taken over to finish either an operator stop or fail-closed
// teardown after an uncertain generator control.
func runAdoptionStatus(status string) bool {
	return status == "RUNNING" || status == "PAUSED" || status == "STOPPING"
}

type runAdoptionCandidate struct {
	stopping       bool
	test           *ManagedTest
	checkpoint     runContinuityCheckpoint
	newToken       [generatorrpc.RunSupervisionTokenSize]byte
	targets        []string
	standbyTargets []string
	// rollback is set when the run is returned to the prior topology of an
	// interrupted prepared or staged evacuation.
	rollback *runEvacuationRollback
	// rollForward is set when the run moves on to the next topology of an
	// evacuation interrupted once its replacement may have been committed.
	rollForward *runEvacuationRollForward
}

type runAdoptionTargetResult struct {
	client   generatorClient
	outcomes map[string]generatorrpc.RunAdoptionResult
	err      error
}

// adoptOrphanedRuns is Phase A. It returns an error only when this replica
// cannot act as leader (reconcile retries); per-run problems fail that run.
func (tr *TestRunner) adoptOrphanedRuns() error {
	if !tr.runAdoptionEnabled() {
		return nil
	}
	b, ok := tr.execution.(*distributedExecutionBackend)
	if !ok {
		return nil
	}
	tr.mu.RLock()
	tests := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if test.executionCtx == nil && test.cancel == nil && test.adoptedResume == nil && tr.isRunAdoptionCandidate(test) {
			tests = append(tests, test)
		}
	}
	tr.mu.RUnlock()
	if len(tests) == 0 {
		return nil
	}
	// An adopted run's batches were held by another controller (B5): any it
	// had not published are unknown here, even if this controller once
	// started the run.
	tr.mu.Lock()
	for _, test := range tests {
		test.jointHeldFromStart = false
		if test.executionCtx == nil && test.cancel == nil {
			// No live runtime: any fence is left from a term this process lost.
			// The reload before Phase A already replaced such an object, so a
			// lost-term goroutine cannot hold this one. Clear it, leaving the
			// fence empty while the generators are claimed; the first hold or
			// install then claims this term, as installRunAdoptionHandlesLocked
			// does.
			test.executionFence = ""
		}
	}
	tr.mu.Unlock()
	generation, ownerID, leaseDeadline, err := tr.generatorExecutionClaim()
	if err != nil {
		return err
	}
	ctx, cancel := context.WithDeadline(context.Background(), minTime(leaseDeadline, time.Now().Add(runAdoptionClaimBudget)))
	defer cancel()

	candidates := make([]*runAdoptionCandidate, 0, len(tests))
	byTarget := make(map[string][]generatorrpc.RunAdoption)
	for _, test := range tests {
		tr.mu.RLock()
		raw := test.continuityCheckpoint
		stopping := test.Status == "STOPPING"
		var rollback *runEvacuationRollback
		if guard, replacements, ok := tr.evacuationRollbackGuard(test, raw); ok {
			rollback = &runEvacuationRollback{guard: guard, raw: raw, replacements: replacements}
		}
		var rollForward *runEvacuationRollForward
		if guard, replacements, ok := tr.evacuationRollForwardGuard(test, raw); ok {
			rollForward = &runEvacuationRollForward{guard: guard, raw: raw, replacements: replacements}
		}
		tr.mu.RUnlock()
		if _, committed := committedRunEvacuationCheckpoint(raw, test.ID); committed {
			promoted, promoteErr := tr.promoteCommittedRunEvacuationCheckpoint(ctx, test, raw, generation, ownerID)
			if promoteErr != nil {
				return fmt.Errorf("promote committed evacuation for %s: %w", test.ID, promoteErr)
			}
			raw = promoted
		}
		checkpoint, _, decodeErr := decodeRunContinuityCheckpoint(raw)
		if rollback != nil {
			// The journal's prior checkpoint is the topology the run returns to.
			checkpoint, decodeErr = rollback.guard.Prior, nil
		}
		if rollForward != nil {
			// The next topology, under the predecessor's lineage.
			checkpoint, decodeErr = runEvacuationRollForwardCheckpoint(rollForward.guard), nil
		}
		if decodeErr == nil && (checkpoint.PinnedSecretCiphertext != "" || checkpoint.PinnedSecretKey != nil) {
			values, openErr := openRunPinnedSecrets(ctx, tr.continuityKeyWrapper, test.ID,
				checkpoint.PinnedSecretCiphertext, checkpoint.PinnedSecretKey)
			if openErr != nil {
				decodeErr = fmt.Errorf("pinned run secrets are unavailable: %w", openErr)
			} else if test.pinnedSecrets != nil {
				test.pinnedSecrets.setIfAbsent(values)
			}
		}
		if decodeErr == nil {
			decodeErr = tr.restoreLiveTables(ctx, test, executionLeaseTerm{ownerID: ownerID, generation: generation})
		}
		if decodeErr == nil {
			decodeErr = tr.restoreRendezvous(ctx, test, checkpoint.Shards, executionLeaseTerm{ownerID: ownerID, generation: generation})
		}
		if decodeErr == nil && checkpoint.ExecutionGeneration >= generation {
			decodeErr = fmt.Errorf("checkpoint generation %d is not older than %d", checkpoint.ExecutionGeneration, generation)
		}
		candidate := &runAdoptionCandidate{test: test, checkpoint: checkpoint, stopping: stopping}
		var adoption generatorrpc.RunAdoption
		if decodeErr == nil {
			candidate.checkpoint, adoption, decodeErr = tr.planRunAdoptionIntent(ctx, test.ID, checkpoint, generation, ownerID)
			copy(candidate.newToken[:], adoption.NewToken)
		}
		if decodeErr != nil {
			if errors.Is(decodeErr, errRunAdoptionKeyAccess) {
				// The checkpoint topology still identifies possible live work. A
				// transient KMS/Key Vault failure cannot turn that work into a
				// terminal outcome. Keep acquisition closed and retry this phase
				// with the same durable token lineage when key access returns.
				return fmt.Errorf("access run adoption key material for %s: %w", test.ID, decodeErr)
			}
			if rollback != nil {
				// Nothing was adopted. The journal's participants may still run, so
				// keep the run STOPPING as a non-rollback successor would.
				tr.dropRunRendezvous(test.ID, executionLeaseTerm{ownerID: ownerID, generation: generation})
				tr.holdInterruptedRunEvacuation(test, executionLeaseTerm{ownerID: ownerID, generation: generation}, fmt.Sprintf("controller failover could not use the evacuation journal's prior checkpoint (%v)", decodeErr))
				continue
			}
			if rollForward != nil {
				tr.dropRunRendezvous(test.ID, executionLeaseTerm{ownerID: ownerID, generation: generation})
				tr.holdInterruptedRunEvacuation(test, executionLeaseTerm{ownerID: ownerID, generation: generation}, fmt.Sprintf("controller failover could not use the evacuation journal's next checkpoint (%v)", decodeErr))
				continue
			}
			if stopping {
				tr.finishStoppingRunAdoption(b, test, nil, nil, nil, nil, generation, generation, ownerID, candidate.newToken, nil, []string{fmt.Sprintf("adoption checkpoint unusable (%v)", decodeErr)})
				continue
			}
			tr.failRunAdoption(b, test, nil, generation, ownerID, candidate.newToken, fmt.Sprintf("controller failover: the run's adoption checkpoint is unusable (%v)", decodeErr))
			continue
		}
		if err := tr.advanceAdoptedRunExecutionGeneration(test, generation, ownerID); err != nil {
			return fmt.Errorf("record adoption execution term for %s: %w", test.ID, err)
		}
		// No generator may see the rotation until its encrypted token and prior
		// proof chain survive a controller crash. A stale snapshot cannot replace
		// a newer intent from this or another lease owner.
		if rollback != nil {
			// The intent stays inside the journal until the replacement is fenced
			// and its capacity returned; any later reader still sees the journal.
			guard, rolling, rollbackErr := tr.persistRunEvacuationRollbackIntent(test, raw, rollback.guard, candidate.checkpoint, generation, ownerID)
			if rollbackErr != nil {
				return fmt.Errorf("persist evacuation rollback intent for %s: %w", test.ID, rollbackErr)
			}
			rollback.guard, rollback.raw = guard, rolling
			candidate.rollback = rollback
		} else if rollForward != nil {
			// The intent stays inside the journal until the replacement is
			// adopted, shortened and its capacity committed.
			guard, rolling, rollForwardErr := tr.persistRunEvacuationRollForwardIntent(test, raw, rollForward.guard, candidate.checkpoint, generation, ownerID)
			if rollForwardErr != nil {
				return fmt.Errorf("persist evacuation roll-forward intent for %s: %w", test.ID, rollForwardErr)
			}
			rollForward.guard, rollForward.raw = guard, rolling
			candidate.rollForward = rollForward
		} else if err := tr.persistRunAdoptionIntent(test, raw, candidate.checkpoint, ownerID); err != nil {
			return fmt.Errorf("persist adoption intent for %s: %w", test.ID, err)
		}
		candidate.targets = checkpointTargets(checkpoint)
		for _, target := range candidate.targets {
			if slices.Contains(checkpoint.CompletedTargets, target) || (stopping && slices.Contains(checkpoint.StoppedTargets, target)) {
				continue
			}
			byTarget[target] = append(byTarget[target], adoption)
		}
		if rollback != nil {
			// An epoch claim without this run's adoption drains the staged
			// replacement assignment before it is acknowledged.
			for _, target := range rollback.replacements {
				if _, exists := byTarget[target]; !exists {
					byTarget[target] = nil
				}
			}
		}
		candidates = append(candidates, candidate)
	}

	if err := tr.addStandbyCapacityAdoptionTargets(candidates, byTarget); err != nil {
		return err
	}

	results := b.claimRunAdoptions(ctx, byTarget, generation, ownerID, leaseDeadline)
	defer func() {
		for _, result := range results {
			if result.client != nil {
				_ = result.client.Close()
			}
		}
	}()
	for _, candidate := range candidates {
		adopted := make(map[string]generatorClient)
		completed := make(map[string]generatorClient)
		reachable := make(map[string]generatorClient)
		problems := make([]string, 0)
		unreachableGenerator := false
		for _, target := range candidate.targets {
			priorStopped := candidate.stopping && slices.Contains(candidate.checkpoint.StoppedTargets, target)
			if slices.Contains(candidate.checkpoint.CompletedTargets, target) || priorStopped {
				client, dialErr := b.retainRunAdoptionTarget(ctx, results, target)
				if dialErr != nil {
					if !priorStopped {
						problems = append(problems, fmt.Sprintf("%s: reconnect completed generator: %v", target, dialErr))
					}
					continue
				}
				completed[target], reachable[target] = client, client
				continue
			}
			result := results[target]
			if result.err != nil {
				unreachableGenerator = true
				problems = append(problems, fmt.Sprintf("%s: %v", target, result.err))
				continue
			}
			// Each run owns its transport handle: completing one run must not
			// disconnect other runs adopted in the same batched epoch claim.
			client, clientErr := b.retainAdoptionClient(ctx, target, result.client)
			if clientErr != nil {
				problems = append(problems, fmt.Sprintf("%s: retain generator client: %v", target, clientErr))
				continue
			}
			reachable[target] = client
			outcome, reported := result.outcomes[candidate.test.ID]
			switch {
			case !reported:
				problems = append(problems, target+": generator did not report the run")
			case outcome.Outcome == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED:
				adopted[target] = client
			case outcome.Outcome == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED:
				completed[target] = client
			default:
				detail := strings.TrimSpace(outcome.Detail)
				if outcome.TerminalState != "" {
					detail = strings.TrimSpace(detail + " (" + outcome.TerminalState + ")")
				}
				problems = append(problems, fmt.Sprintf("%s: %s %s", target, strings.TrimPrefix(outcome.Outcome.String(), "RUN_ADOPTION_OUTCOME_"), detail))
			}
		}
		archives := make(map[string]generatorClient, len(candidate.checkpoint.ArchiveTargets))
		for _, target := range candidate.checkpoint.ArchiveTargets {
			if reachable[target] != nil || archives[target] != nil {
				continue
			}
			client, archiveErr := b.retainRunAdoptionTarget(ctx, results, target)
			if archiveErr != nil {
				slog.Warn("could not reconnect retired generator archive after failover", "test_id", candidate.test.ID, "target", target, "error", archiveErr)
				continue
			}
			archives[target] = client
		}
		if candidate.stopping {
			// Every reachable generator serves the final archive; any that
			// never saw the stop was adopted above and is stopped now.
			tr.isolateRunAdoption(b, candidate.test, generation, ownerID, func() {
				tr.finishStoppingRunAdoption(b, candidate.test, reachable, adopted, completed, archives, generation, candidate.checkpoint.ExecutionGeneration, ownerID, candidate.newToken, candidate.targets, problems)
			})
			continue
		}
		tr.isolateRunAdoption(b, candidate.test, generation, ownerID, func() {
			var restoredStandbys []string
			if candidate.rollback != nil {
				// Until the journal is replaced, a panic must keep the rollback's
				// fail-closed handling rather than bind an ordinary adoption failure.
				journalKept := true
				defer func() {
					if journalKept {
						if recovered := recover(); recovered != nil {
							slog.Error("evacuation rollback panicked", "test_id", candidate.test.ID, "panic", recovered)
							tr.failRunEvacuationRollback(b, candidate, adopted, []map[string]generatorClient{reachable, archives}, generation, ownerID,
								append(problems, fmt.Sprintf("internal error while rolling back the evacuation (%v)", recovered)))
						}
					}
				}()
				if len(problems) == 0 {
					var rollbackProblems []string
					restoredStandbys, rollbackProblems = tr.completeRunEvacuationRollback(ctx, candidate, results, completed, generation, ownerID)
					problems = append(problems, rollbackProblems...)
					journalKept = len(rollbackProblems) > 0
				}
				if len(problems) > 0 {
					// The journal is replaced only after a complete rollback, so a
					// failed one stays on the fail-closed evacuation path.
					journalKept = false
					tr.failRunEvacuationRollback(b, candidate, adopted, []map[string]generatorClient{reachable, archives}, generation, ownerID, problems)
					return
				}
			}
			capacityClients := make(map[string]generatorClient, len(adopted))
			for target, client := range adopted {
				capacityClients[target] = client
			}
			for _, target := range restoredStandbys {
				capacityClients[target] = results[target].client
			}
			for _, target := range candidate.standbyTargets {
				result := results[target]
				if slices.Contains(candidate.targets, target) || result.err != nil || result.client == nil {
					problems = append(problems, fmt.Sprintf("standby %s could not be fenced for capacity transfer", target))
					continue
				}
				capacityClients[target] = result.client
			}
			if candidate.rollForward != nil {
				// Until the journal is replaced, a panic must keep the
				// roll-forward's fail-closed handling, as for a rollback.
				journalKept := true
				defer func() {
					if journalKept {
						if recovered := recover(); recovered != nil {
							slog.Error("evacuation roll-forward panicked", "test_id", candidate.test.ID, "panic", recovered)
							tr.failRunEvacuationRollForward(b, candidate, adopted, []map[string]generatorClient{reachable, archives}, generation, ownerID,
								append(problems, fmt.Sprintf("internal error while rolling the evacuation forward (%v)", recovered)))
						}
					}
				}()
				if len(problems) == 0 {
					rollForwardProblems := tr.completeRunEvacuationRollForward(ctx, b, candidate, adopted, completed, capacityClients, generation, ownerID)
					problems = append(problems, rollForwardProblems...)
					journalKept = len(rollForwardProblems) > 0
				}
				if len(problems) > 0 {
					// The journal is replaced only after a complete roll-forward,
					// which also transferred the run's capacity.
					journalKept = false
					tr.failRunEvacuationRollForward(b, candidate, adopted, []map[string]generatorClient{reachable, archives}, generation, ownerID, problems)
					return
				}
			} else if len(problems) == 0 {
				if err := tr.adoptPodCapacity(ctx, candidate.test.ID, capacityClients, generation, ownerID); err != nil {
					problems = append(problems, "capacity ownership transfer: "+err.Error())
				}
			}
			if len(problems) > 0 {
				message := "controller failover: the run could not be adopted by the new controller: " + strings.Join(problems, "; ")
				tr.mu.RLock()
				lossTolerant := assignmentToleratesGeneratorLoss(candidate.test.Assignment)
				tr.mu.RUnlock()
				if lossTolerant && unreachableGenerator {
					// Decision: evacuation needs a leader that watched the loss;
					// a generator lost while none was leading fails the run.
					message += " (generator-loss tolerance does not evacuate a generator lost while no controller was leading)"
				}
				// Stop adopted assignments, but also retain completed participants
				// for best-effort archive collection when another target was lost.
				tr.bindRunAdoptionOutcome(b, candidate.test, reachable, adopted, completed, archives, generation, candidate.checkpoint.ExecutionGeneration, ownerID, candidate.newToken, candidate.targets, "FAILED", message)
				return
			}
			tr.installAdoptedRun(b, candidate, adopted, completed, archives, generation, ownerID)
		})
	}
	tr.secureAdoptedRuns(b)
	return nil
}

// secureAdoptedRuns re-establishes generator run leases and supervision for
// every run Phase A adopted, before admission opens. The adoption claim gave
// each generator one supervision TTL to hear from the successor; leaving the
// re-claim to Phase B made the run's survival depend on how long the rest of
// acquisition recovery takes. Failures are recorded for Phase B to apply.
func (tr *TestRunner) secureAdoptedRuns(b *distributedExecutionBackend) {
	tr.mu.RLock()
	tests := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if resume := test.adoptedResume; resume != nil && resume.finishStatus == "" && len(resume.adopted) > 0 && !resume.secured && resume.secureFailure == "" {
			tests = append(tests, test)
		}
	}
	tr.mu.RUnlock()
	var secures sync.WaitGroup
	for _, test := range tests {
		secures.Add(1)
		go func() {
			defer secures.Done()
			tr.mu.RLock()
			resume := test.adoptedResume
			executionCtx := test.executionCtx
			token := test.remoteRunSupervisionToken
			active := tr.tests[test.ID] == test && runAdoptionStatus(test.Status) && test.Status != "STOPPING"
			tr.mu.RUnlock()
			if resume == nil || !active || executionCtx == nil {
				return
			}
			message := func() (message string) {
				defer func() {
					if recovered := recover(); recovered != nil {
						slog.Error("securing an adopted run panicked", "test_id", test.ID, "panic", recovered)
						message = fmt.Sprintf("internal error while re-establishing generator supervision (%v)", recovered)
					}
				}()
				return tr.secureAdoptedRunSupervision(b, test, resume, executionCtx, token)
			}()
			tr.mu.Lock()
			if test.adoptedResume == resume {
				if message != "" {
					resume.secureFailure = message
				} else {
					resume.secured = true
				}
			}
			tr.mu.Unlock()
		}()
	}
	secures.Wait()
}

// secureAdoptedRunSupervision moves an adopted run's generator-side keepalives
// to the new term: per-run leases for a loss-tolerant run, then supervision
// under the rotated token and its renewal keeper. It returns a failure message,
// or "" on success.
func (tr *TestRunner) secureAdoptedRunSupervision(b *distributedExecutionBackend, test *ManagedTest, resume *adoptedRunResume, executionCtx context.Context, token [generatorrpc.RunSupervisionTokenSize]byte) string {
	tr.excludeFinishedGenerators(b, test, resume.completed)
	tr.mu.RLock()
	lossTolerant := assignmentToleratesGeneratorLoss(test.Assignment)
	tr.mu.RUnlock()
	if lossTolerant {
		// A loss-tolerant run also holds a per-run lease on each generator; the
		// generator requires it before supervision. Re-establish it under the
		// new term, then hand it to the ordinary per-run renewal loop (which
		// also drives generator-loss evacuation from here on). A generator lost
		// during the gap already failed the run in Phase A.
		_, _, leaseDeadline, claimErr := tr.generatorExecutionClaim()
		if claimErr != nil {
			return fmt.Sprintf("execution leadership changed before run leases were re-established: %v", claimErr)
		}
		if err := b.renewGeneratorTestExecutionClients(executionCtx, test.ID, resume.adopted, resume.generation, resume.ownerID, leaseDeadline); err != nil {
			return fmt.Sprintf("re-establish generator run leases: %v", err)
		}
		go b.renewGeneratorExecutionEpoch(executionCtx, tr, test, resume.generation)
	}
	if err := b.claimRunSupervisionClients(executionCtx, test.ID, resume.adopted, resume.generation, resume.ownerID, token[:]); err != nil {
		return fmt.Sprintf("re-establish generator run supervision: %v", err)
	}
	b.startRunSupervisionKeeper(executionCtx, tr, test, resume.adopted, resume.generation, resume.ownerID, token)
	return ""
}

// Completed and retired participants still need archive access, but may share
// their sole reverse tunnel with a different run's active assignment.
func (b *distributedExecutionBackend) retainRunAdoptionTarget(ctx context.Context, results map[string]runAdoptionTargetResult, target string) (generatorClient, error) {
	result, present := results[target]
	if !present {
		dialCtx, cancel := context.WithTimeout(ctx, b.timeout)
		result.client, result.err = b.dial(dialCtx, target)
		cancel()
		results[target] = result
	}
	if result.err != nil {
		return nil, result.err
	}
	return b.retainAdoptionClient(ctx, target, result.client)
}

func (b *distributedExecutionBackend) retainAdoptionClient(ctx context.Context, target string, client generatorClient) (generatorClient, error) {
	if shared, ok := client.(interface {
		Retain() (*generatorrpc.Client, error)
	}); ok {
		return shared.Retain()
	}
	// In-process adapters may have their own independent dial handles.
	dialCtx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	return b.dial(dialCtx, target)
}

// A reverse generator has one physical tunnel. Let the epoch keeper share the
// transport already established by adoption instead of waiting for a second one.
func (tr *TestRunner) retainAdoptedEpochClients() map[string]generatorClient {
	clients := make(map[string]generatorClient)
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	for _, test := range tr.tests {
		if test.executionCtx == nil || len(test.continuityFailovers) == 0 || isTerminalTestStatus(test.Status) {
			continue
		}
		for target, client := range test.remoteClientsByTarget {
			if clients[target] != nil {
				continue
			}
			if shared, ok := client.(interface {
				Retain() (*generatorrpc.Client, error)
			}); ok {
				if retained, err := shared.Retain(); err == nil {
					clients[target] = retained
				}
			}
		}
	}
	return clients
}

// isolateRunAdoption contains a panic while adopting one run. The acquisition
// callback treats a panic as failed recovery and relinquishes leadership, so an
// unguarded bug in one run's adoption would stop this replica from ever
// leading. The affected run is failed instead and takeover continues.
func (tr *TestRunner) isolateRunAdoption(b *distributedExecutionBackend, test *ManagedTest, generation uint64, ownerID string, adopt func()) {
	defer func() {
		recovered := recover()
		if recovered == nil {
			return
		}
		slog.Error("run adoption panicked; failing the run", "test_id", test.ID, "panic", recovered)
		defer func() {
			if again := recover(); again != nil {
				slog.Error("failing a run after an adoption panic also panicked", "test_id", test.ID, "panic", again)
			}
		}()
		tr.mu.RLock()
		raw := test.continuityCheckpoint
		tr.mu.RUnlock()
		if interruptedRunEvacuation(raw) {
			// A run still under its evacuation journal may have unfenced
			// participants; it stays STOPPING rather than terminal.
			action := "rolling back the evacuation"
			var guard runEvacuationCheckpoint
			if json.Unmarshal([]byte(raw), &guard) == nil && guard.Phase == runEvacuationPhaseRollingForward {
				action = "rolling the evacuation forward"
			}
			tr.dropRunRendezvous(test.ID, executionLeaseTerm{ownerID: ownerID, generation: generation})
			tr.holdInterruptedRunEvacuation(test, executionLeaseTerm{ownerID: ownerID, generation: generation}, fmt.Sprintf("controller failover: internal error while %s (%v)", action, recovered))
			return
		}
		tr.failRunAdoption(b, test, nil, generation, ownerID, [generatorrpc.RunSupervisionTokenSize]byte{}, fmt.Sprintf("controller failover: internal error while adopting the run (%v)", recovered))
	}()
	adopt()
}

func checkpointTargets(checkpoint runContinuityCheckpoint) []string {
	unique := make(map[string]struct{})
	for _, shards := range checkpoint.Shards {
		for _, shard := range shards {
			if target := strings.TrimSpace(shard.Target); target != "" {
				unique[target] = struct{}{}
			}
		}
	}
	targets := make([]string, 0, len(unique))
	for target := range unique {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return targets
}

// claimRunAdoptions sends each generator its first claim of generation,
// carrying every adoption for that generator.
func (b *distributedExecutionBackend) claimRunAdoptions(ctx context.Context, byTarget map[string][]generatorrpc.RunAdoption, generation uint64, ownerID string, leaseDeadline time.Time) map[string]runAdoptionTargetResult {
	results := make(map[string]runAdoptionTargetResult, len(byTarget))
	var mu sync.Mutex
	var workers sync.WaitGroup
	// Same bound as the epoch keeper: a large fleet must not open hundreds of
	// connections at once while the new leader takes over.
	slots := make(chan struct{}, max(1, min(maxGeneratorEpochKeeperWorkers, len(byTarget))))
	for target, adoptions := range byTarget {
		workers.Add(1)
		slots <- struct{}{}
		go func() {
			defer func() { <-slots }()
			defer workers.Done()
			result := runAdoptionTargetResult{}
			dialCtx, dialCancel := context.WithTimeout(ctx, b.timeout)
			client, err := b.dial(dialCtx, target)
			dialCancel()
			if err == nil {
				var claim generatorrpc.ExecutionEpochResult
				for {
					claim, err = claimGeneratorExecutionEpochWithAdoptions(ctx, client, generation, ownerID, leaseDeadline, b.generatorEpochKeeperTransit(), adoptions)
					if err == nil || ctx.Err() != nil || (status.Code(err) != codes.Unavailable && status.Code(err) != codes.DeadlineExceeded) {
						break
					}
					// The reconnect or acknowledgement may lag the first attempt.
					// Retrying the same token rotation confirms an applied adoption.
					if !waitForGeneratorEpochKeeper(ctx, 100*time.Millisecond) {
						break
					}
				}
				if err == nil && !claim.AdoptionSupported {
					err = fmt.Errorf("generator predates run adoption")
				}
				if err == nil {
					result.outcomes = make(map[string]generatorrpc.RunAdoptionResult, len(claim.Adoptions))
					for _, outcome := range claim.Adoptions {
						result.outcomes[outcome.TestID] = outcome
					}
				}
				if err != nil {
					_ = client.Close()
					client = nil
				}
			}
			result.client, result.err = client, err
			mu.Lock()
			results[target] = result
			mu.Unlock()
		}()
	}
	workers.Wait()
	return results
}

// installRunAdoptionHandlesLocked binds test to the new term with the rotated
// token and the given generator clients. The caller holds tr.mu.
func (tr *TestRunner) installRunAdoptionHandlesLocked(b *distributedExecutionBackend, test *ManagedTest, clients map[string]generatorClient, shards map[string][]GeneratorShardSummary, token [generatorrpc.RunSupervisionTokenSize]byte) error {
	if tr.tests[test.ID] != test || !runAdoptionStatus(test.Status) {
		return fmt.Errorf("run is no longer active")
	}
	if test.executionCtx == nil && test.cancel == nil {
		// No live runtime: any fence is left from a term this process lost
		// (candidates are rehydrated or torn down). Binding is the explicit
		// takeover, so it is not an old goroutine adopting a newer term.
		test.executionFence = ""
	} else if test.cancel != nil {
		// Re-binding after a partial install (the adoption panic path): release
		// the context that install created before replacing it.
		test.cancel()
		test.cancel, test.executionCtx = nil, nil
	}
	if err := tr.installTestExecutionContextLocked(test, nil); err != nil {
		return err
	}
	if err := tr.claimTestExecutionFence(test); err != nil {
		test.cancel()
		test.cancel, test.executionCtx = nil, nil
		return err
	}
	test.executionBackend = b
	test.remoteRunSupervisionToken = token
	test.remoteClientsByTarget = cloneClientMap(clients)
	if len(shards) > 0 {
		test.remoteGroupShards = cloneGroupShardMap(shards)
		test.remoteGroupTargets = primaryTargetsFromShards(shards)
		test.GeneratorShards = flattenGroupShards(shards)
	}
	test.remoteTargetStates = make(map[string]string)
	return nil
}

// adoptedRunTotals uses the atomic telemetry checkpoint when available.
// Legacy recovery starts a taken-over run's totals from its stored history,
// not from a copy this process kept: a standby may hold an early hydration of
// the run, and a former owner its own count up to when it lost the run. The
// stored windows carry no run totals, so the start is their sum, bounded and
// approximate. If the history cannot be read, the totals start from the
// history the run holds, at its next window. It is read when the run resumes,
// after its generators are secured and before any of its windows is counted.
func (tr *TestRunner) adoptedRunTotals(test *ManagedTest) runTotalsAccumulator {
	tr.mu.RLock()
	if test.telemetryCheckpointRestored {
		totals := test.runTotals
		tr.mu.RUnlock()
		return totals
	}
	tr.mu.RUnlock()
	var totals runTotalsAccumulator
	if tr.historyStore == nil {
		return totals
	}
	restored := &ManagedTest{ID: test.ID}
	if err := tr.loadStoredTestHistoryLimit(restored, maxManagedTestHistory); err != nil {
		slog.Warn("failed to load stored history for an adopted run's totals", "test_id", test.ID, "error", err)
		return totals
	}
	tr.mu.RLock()
	groups := append([]TestGroup(nil), test.Groups...)
	tr.mu.RUnlock()
	totals.countWindow(nil, restored.History, groups)
	return totals
}

// installAdoptedRunTotalsLocked never replaces restored live state with the
// earlier copy captured before stream installation. A terminal collection may
// have been counted while streams were opening; its cursor and effects must
// remain together. Legacy history seeding retains its existing behavior.
func installAdoptedRunTotalsLocked(test *ManagedTest, legacy runTotalsAccumulator) {
	if !test.telemetryCheckpointRestored {
		test.runTotals = legacy
	}
}

func (tr *TestRunner) installAdoptedRun(b *distributedExecutionBackend, candidate *runAdoptionCandidate, adopted, completed, archives map[string]generatorClient, generation uint64, ownerID string) {
	test := candidate.test
	tr.restoreTelemetryCheckpoint(test, generation, candidate.checkpoint.ExecutionGeneration)
	tr.restoreTelemetryPolicySegments(test)
	// cloneClientMap returns nil for an empty map, and a run whose generators
	// all finished during the gap has no adopted clients.
	all := make(map[string]generatorClient, len(adopted)+len(completed))
	for target, client := range adopted {
		all[target] = client
	}
	for target, client := range completed {
		all[target] = client
	}
	adoptedAt := time.Now().UTC()
	failover := RunFailover{
		PriorGeneration: candidate.checkpoint.ExecutionGeneration, Generation: generation, AdoptedAt: adoptedAt,
		AdoptedGenerators: len(adopted), CompletedGenerators: len(completed),
	}
	tr.mu.Lock()
	if test.LiveMetrics != nil && !test.LiveMetrics.Timestamp.IsZero() && test.LiveMetrics.Timestamp.Before(adoptedAt) {
		observed := test.LiveMetrics.Timestamp.UTC()
		failover.LastObservedAt = &observed
		failover.GapSeconds = adoptedAt.Sub(observed).Seconds()
	}
	err := tr.installRunAdoptionHandlesLocked(b, test, all, candidate.checkpoint.Shards, candidate.newToken)
	if err == nil {
		test.remoteArchiveClientsByTarget = archives
		test.continuityFailovers = append(append([]RunFailover(nil), test.continuityFailovers...), failover)
		// A stage recorder this process kept from an earlier ownership samples
		// only from the adoption (a new one starts there anyway).
		test.telemetryStages.adopted(adoptedAt)
		for target := range completed {
			test.remoteTargetStates[target] = "complete"
		}
		// This controller requires each generator from the adoption; one
		// that completed during the gap is never required here.
		ledger := telemetrySourceLedgerLocked(test)
		ledger.markAdopted()
		for target := range adopted {
			ledger.assign(target, adoptedAt)
		}
		for target := range completed {
			ledger.assign(target, adoptedAt)
			ledger.retireAtAdoption(target, adoptedAt)
		}
		test.adoptedResume = &adoptedRunResume{
			generation: generation, ownerID: ownerID,
			adopted: cloneClientMap(adopted), completed: cloneClientMap(completed),
			adoptedAt: adoptedAt,
		}
	}
	tr.mu.Unlock()
	if err != nil {
		for _, client := range archives {
			_ = client.Close()
		}
		for _, client := range all {
			_ = client.Close()
		}
		slog.Warn("adopted run changed before its handles were installed", "test_id", test.ID, "error", err)
		return
	}
	// Re-arm under the new term so a second failover can adopt the run again.
	tr.armRunContinuity(context.Background(), test, generation, ownerID, candidate.newToken[:], candidate.checkpoint.Shards)
	slog.Info("run adopted after controller failover", "test_id", test.ID, "execution_generation", generation,
		"prior_generation", candidate.checkpoint.ExecutionGeneration, "adopted_generators", len(adopted), "completed_generators", len(completed))
}

// failRunAdoption finalizes a run that cannot be adopted: it stops any
// generators that did adopt it and collects a best-effort archive through the
// ordinary distributed failure path.
func (tr *TestRunner) failRunAdoption(b *distributedExecutionBackend, test *ManagedTest, adopted map[string]generatorClient, generation uint64, ownerID string, token [generatorrpc.RunSupervisionTokenSize]byte, message string) {
	defer tr.dropRunRendezvous(test.ID, executionLeaseTerm{ownerID: ownerID, generation: generation})
	slog.Warn("run adoption failed; the run will be marked FAILED", "test_id", test.ID, "execution_generation", generation, "reason", message)
	tr.bindRunAdoptionOutcome(b, test, adopted, adopted, nil, nil, generation, generation, ownerID, token, nil, "FAILED", message)
}

// bindRunAdoptionOutcome binds a run that will not resume to the new term, so
// stop and archive collection work under the rotated token, and records the
// outcome Phase B applies. Only adopted generators are stopped; the others in
// clients (already stopped or finished) serve the archive.
// priorGeneration is the adopted continuity term; passing generation itself
// means no snapshot is known to be current, so totals come from stored history.
func (tr *TestRunner) bindRunAdoptionOutcome(b *distributedExecutionBackend, test *ManagedTest, clients, adopted, completed, archives map[string]generatorClient, generation, priorGeneration uint64, ownerID string, token [generatorrpc.RunSupervisionTokenSize]byte, targets []string, status, message string) {
	tr.restoreTelemetryCheckpoint(test, generation, priorGeneration)
	tr.restoreTelemetryPolicySegments(test)
	tr.mu.Lock()
	err := tr.installRunAdoptionHandlesLocked(b, test, clients, nil, token)
	if err == nil {
		test.remoteArchiveClientsByTarget = archives
		adoptedAt := time.Now().UTC()
		ledger := telemetrySourceLedgerLocked(test)
		ledger.markAdopted()
		for target := range completed {
			test.remoteTargetStates[target] = "complete"
			// Completed during the gap: never observed or required here.
			ledger.assign(target, adoptedAt)
			ledger.retireAtAdoption(target, adoptedAt)
		}
		test.adoptedResume = &adoptedRunResume{generation: generation, ownerID: ownerID, completed: cloneClientMap(completed), adoptedAt: adoptedAt, finishStatus: status, finishMessage: message, finishTargets: append([]string(nil), targets...)}
	}
	tr.mu.Unlock()
	if err != nil {
		for _, client := range archives {
			_ = client.Close()
		}
		for _, client := range clients {
			_ = client.Close()
		}
		slog.Warn("run could not be bound to the new controller term", "test_id", test.ID, "outcome", status, "error", err)
	}
}

// finishStoppingRunAdoption completes a run whose stop was in flight when the
// leader was lost. An operator stop remains ABORTED; a persisted fail-closed
// control error remains FAILED. Generators still running the run under the
// rotated token are stopped, and archives are collected where reachable.
func (tr *TestRunner) finishStoppingRunAdoption(b *distributedExecutionBackend, test *ManagedTest, clients, adopted, completed, archives map[string]generatorClient, generation, priorGeneration uint64, ownerID string, token [generatorrpc.RunSupervisionTokenSize]byte, targets []string, problems []string) {
	defer tr.dropRunRendezvous(test.ID, executionLeaseTerm{ownerID: ownerID, generation: generation})
	if len(problems) > 0 {
		slog.Warn("finishing a stopping run after controller failover with incomplete generator reach", "test_id", test.ID, "problems", strings.Join(problems, "; "))
	}
	slog.Info("finishing an in-flight stop after controller failover", "test_id", test.ID, "execution_generation", generation, "generators", len(clients), "still_running", len(adopted))
	status, message := stoppingRunAdoptionOutcome(test.Error)
	tr.bindRunAdoptionOutcome(b, test, clients, adopted, completed, archives, generation, priorGeneration, ownerID, token, targets, status, message)
}

func stoppingRunAdoptionOutcome(reason string) (status, message string) {
	// Adoption replaces the evacuation journal, so the resolve instruction
	// recorded with an uncertain evacuation no longer applies.
	reason = strings.TrimSpace(withoutEvacuationIntervention(reason))
	if strings.HasPrefix(reason, "controller failover: pending ABORTED cleanup proof:") {
		return "ABORTED", reason
	}
	// The previous successor may have retained a FAILED outcome while waiting
	// for a generator's stop proof. Keep that outcome across another takeover,
	// even when the original failure reason precedes the cleanup marker.
	if strings.Contains(reason, "controller failover: pending FAILED cleanup proof:") {
		return "FAILED", reason
	}
	for _, prefix := range []string{
		"generator evacuation state is uncertain:",
		"controller failover: the run could not be adopted",
		"controller failover: pending FAILED cleanup proof:",
		"remote VUser state is uncertain:",
		"remote duration state is uncertain:",
		"remote pause/resume state is uncertain:",
		"persist coordinated distributed start:",
		"commit distributed work:",
	} {
		if strings.HasPrefix(reason, prefix) {
			return "FAILED", reason
		}
	}
	if strings.HasPrefix(reason, "ci abort reason:") {
		return "ABORTED", reason
	}
	return "ABORTED", ""
}

// confirmAdoptedRunStopped replays the already-persisted adoption intent and
// requires a positive stop or retained terminal outcome for every participant
// that was not already completed in the prior checkpoint. In particular,
// ALREADY_STOPPED without a terminal state can mean "drain in progress" or
// "supervision expired" and is not sufficient to release the run reservation.
func (tr *TestRunner) confirmAdoptedRunStopped(b *distributedExecutionBackend, test *ManagedTest, resume *adoptedRunResume) error {
	if len(resume.finishTargets) == 0 {
		return errUnprovableRunAdoptionStop
	}
	generation, ownerID, leaseDeadline, err := tr.generatorExecutionClaim()
	if err != nil {
		return err
	}
	if generation != resume.generation || ownerID != resume.ownerID {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	raw := test.continuityCheckpoint
	current := tr.tests[test.ID] == test && runAdoptionStatus(test.Status)
	token := test.remoteRunSupervisionToken
	tr.mu.RUnlock()
	if !current {
		return errNotExecutionLeader
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(raw)
	if err != nil || !present {
		return fmt.Errorf("%w: checkpoint decode: %v", errUnprovableRunAdoptionStop, err)
	}
	byTarget := make(map[string][]generatorrpc.RunAdoption)
	for _, target := range resume.finishTargets {
		if !slices.Contains(checkpointTargets(checkpoint), target) {
			return fmt.Errorf("%w: finish target %s is absent from the durable topology", errUnprovableRunAdoptionStop, target)
		}
		if !slices.Contains(checkpoint.CompletedTargets, target) && !slices.Contains(checkpoint.StoppedTargets, target) {
			byTarget[target] = nil
		}
	}
	// Every target may already have durable stop proof from an earlier retry.
	// Do not require a disappeared generator or key service after that point.
	if len(byTarget) == 0 {
		return nil
	}
	ctx, cancel := context.WithDeadline(context.Background(), minTime(leaseDeadline, time.Now().Add(runAdoptionClaimBudget)))
	defer cancel()
	_, adoption, err := tr.planRunAdoptionIntent(ctx, test.ID, checkpoint, generation, ownerID)
	if err != nil {
		return fmt.Errorf("replay stop-proof adoption intent: %w", err)
	}
	if !slices.Equal(adoption.NewToken, token[:]) {
		return fmt.Errorf("%w: installed token differs from the durable intent", errUnprovableRunAdoptionStop)
	}
	for target := range byTarget {
		byTarget[target] = []generatorrpc.RunAdoption{adoption}
	}
	results := b.claimRunAdoptions(ctx, byTarget, generation, ownerID, leaseDeadline)
	defer func() {
		for _, result := range results {
			if result.client != nil {
				_ = result.client.Close()
			}
		}
	}()
	var failures []error
	for _, target := range resume.finishTargets {
		if slices.Contains(checkpoint.CompletedTargets, target) || slices.Contains(checkpoint.StoppedTargets, target) {
			continue
		}
		result, present := results[target]
		if !present || result.err != nil || result.client == nil {
			failures = append(failures, fmt.Errorf("%s: adoption claim unavailable: %v", target, result.err))
			continue
		}
		outcome, reported := result.outcomes[test.ID]
		if !reported {
			failures = append(failures, fmt.Errorf("%s: generator did not report the run", target))
			continue
		}
		if (outcome.Outcome == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED && isTerminalRemoteLifecycle(outcome.TerminalState)) ||
			(outcome.Outcome == pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED && isTerminalRemoteLifecycle(outcome.TerminalState)) {
			if err := tr.persistRunAdoptionStoppedTarget(test, target, generation, ownerID); err != nil {
				failures = append(failures, fmt.Errorf("%s: persist retained terminal proof: %w", target, err))
				continue
			}
			checkpoint.StoppedTargets = append(checkpoint.StoppedTargets, target)
			continue
		}
		if outcome.Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED && outcome.Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED {
			failures = append(failures, fmt.Errorf("%s: adoption outcome %s does not prove stop", target, outcome.Outcome))
			continue
		}
		stopCtx, stopCancel := context.WithTimeout(ctx, b.timeout)
		stopped, stopErr := stopGeneratorTestWithToken(stopCtx, result.client, test.ID, false, generation, token[:])
		stopCancel()
		if stopErr != nil || !stopped.Acknowledged {
			failures = append(failures, fmt.Errorf("%s: exact-generation stop was not acknowledged: %v", target, stopErr))
			continue
		}
		if err := tr.persistRunAdoptionStoppedTarget(test, target, generation, ownerID); err != nil {
			failures = append(failures, fmt.Errorf("%s: persist confirmed force-stop proof: %w", target, err))
			continue
		}
		checkpoint.StoppedTargets = append(checkpoint.StoppedTargets, target)
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	tr.mu.Lock()
	if tr.tests[test.ID] == test && runAdoptionStatus(test.Status) {
		if test.remoteTargetStates == nil {
			test.remoteTargetStates = make(map[string]string)
		}
		for _, target := range resume.finishTargets {
			test.remoteTargetStates[target] = "ABORTED"
		}
	}
	tr.mu.Unlock()
	return nil
}

func (tr *TestRunner) markAdoptedRunStopUncertain(test *ManagedTest, resume *adoptedRunResume, stopErr error) error {
	if err := tr.persistAdoptedRunStopUncertainty(test, resume, stopErr); err != nil {
		return err
	}
	return stopErr
}

func (tr *TestRunner) persistAdoptedRunStopUncertainty(test *ManagedTest, resume *adoptedRunResume, stopErr error) error {
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if tr.tests[test.ID] != test || isTerminalTestStatus(test.Status) {
		return nil
	}
	previousStatus, previousError := test.Status, test.Error
	test.Status = "STOPPING"
	test.Error = adoptedRunStopUncertaintyMessage(resume, stopErr)
	if err := tr.saveManagedTestRecord(test); err != nil {
		test.Status, test.Error = previousStatus, previousError
		return fmt.Errorf("persist unconfirmed adopted-run stop: %w", err)
	}
	return nil
}

// A forced StopTest ACK waits for the generator's detached drain to finish,
// or reports an already-retained terminal generation. Graceful ACKs may have
// pending VUsers and are never used as terminal proof here.
func (tr *TestRunner) confirmResumedAdoptedRunStopped(b *distributedExecutionBackend, test *ManagedTest, resume *adoptedRunResume) error {
	generation, owner, leaseDeadline, err := tr.generatorExecutionClaim()
	if err != nil || generation != resume.generation || owner != resume.ownerID {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	current := tr.tests[test.ID] == test && test.Status == "STOPPING"
	raw := test.continuityCheckpoint
	token := test.remoteRunSupervisionToken
	tr.mu.RUnlock()
	if !current {
		return errNotExecutionLeader
	}
	checkpoint, present, err := decodeRunContinuityCheckpoint(raw)
	if err != nil || !present {
		return fmt.Errorf("%w: resumed run checkpoint decode: %v", errUnprovableRunAdoptionStop, err)
	}
	if len(resume.adopted) == 0 {
		return errUnprovableRunAdoptionStop
	}
	targets := make([]string, 0, len(resume.adopted))
	for target := range resume.adopted {
		if !slices.Contains(checkpointTargets(checkpoint), target) {
			return fmt.Errorf("%w: adopted target %s is absent from the durable topology", errUnprovableRunAdoptionStop, target)
		}
		targets = append(targets, target)
	}
	sort.Strings(targets)
	ctx, cancel := context.WithDeadline(context.Background(), minTime(leaseDeadline, time.Now().Add(runAdoptionClaimBudget)))
	defer cancel()
	var failures []error
	for _, target := range targets {
		if slices.Contains(checkpoint.StoppedTargets, target) {
			continue
		}
		callCtx, callCancel := context.WithTimeout(ctx, b.timeout)
		client, dialErr := b.dial(callCtx, target)
		if dialErr != nil {
			callCancel()
			failures = append(failures, fmt.Errorf("%s: reconnect for exact-generation force stop: %w", target, dialErr))
			continue
		}
		stop, stopErr := stopGeneratorTestWithToken(callCtx, client, test.ID, false, generation, token[:])
		callCancel()
		_ = client.Close()
		if stopErr != nil || !stop.Acknowledged {
			failures = append(failures, fmt.Errorf("%s: exact-generation force stop was not confirmed: %v", target, stopErr))
			continue
		}
		if err := tr.persistRunAdoptionStoppedTarget(test, target, generation, owner); err != nil {
			failures = append(failures, fmt.Errorf("%s: persist confirmed force-stop proof: %w", target, err))
			continue
		}
		checkpoint.StoppedTargets = append(checkpoint.StoppedTargets, target)
	}
	return errors.Join(failures...)
}

// completeDistributedTestRun reports terminal-write failure to the stop-proof
// worker. The distributed completion helper logs and enqueues its own retry,
// but returns no error; this worker must not retire while its row is still
// STOPPING after all generator proofs have been obtained.
func (tr *TestRunner) completeAdoptedRunAfterStopProof(b *distributedExecutionBackend, test *ManagedTest, status, message string) error {
	tr.mu.RLock()
	current := tr.tests[test.ID] == test
	terminal := current && isTerminalTestStatus(test.Status) && test.EndedAt != nil
	tr.mu.RUnlock()
	if !current || terminal {
		return nil
	}
	b.completeDistributedTestRun(tr, test, status, message)
	tr.mu.RLock()
	current = tr.tests[test.ID] == test
	terminal = current && isTerminalTestStatus(test.Status) && test.EndedAt != nil
	tr.mu.RUnlock()
	if !current || terminal {
		return nil
	}
	return errors.New("adopted run stop proof is confirmed but its terminal state is not durable")
}

func (tr *TestRunner) failResumedAdoptedRunWithStopProof(b *distributedExecutionBackend, test *ManagedTest, resume *adoptedRunResume, message string) {
	failure := &adoptedRunResume{
		generation: resume.generation, ownerID: resume.ownerID,
		finishStatus:  "FAILED",
		finishMessage: boundedtext.TruncateUTF8(scrub.Value("controller failover: "+message, scrub.Policy{}), maxAdoptedRunStopErrorBytes),
	}
	why := errors.New("force-stop proof is pending after " + failure.finishMessage)
	finish := func() error {
		tr.mu.RLock()
		current := tr.tests[test.ID] == test && runAdoptionStatus(test.Status)
		otherStop := test.Status == "STOPPING" && !strings.HasPrefix(test.Error, "controller failover: pending FAILED cleanup proof:")
		tr.mu.RUnlock()
		if !current || otherStop {
			return nil
		}
		if err := tr.persistAdoptedRunStopUncertainty(test, failure, why); err != nil {
			return err
		}
		tr.mu.RLock()
		cancel := test.cancel
		tr.mu.RUnlock()
		if cancel != nil {
			cancel() // stop local lease/supervision renewal while remote proof is pending
		}
		if err := tr.confirmResumedAdoptedRunStopped(b, test, resume); err != nil {
			return err
		}
		return tr.completeAdoptedRunAfterStopProof(b, test, "FAILED", failure.finishMessage)
	}
	if err := finish(); err != nil {
		slog.Warn("adopted run remains STOPPING until resumed work is force-stopped", "test_id", test.ID,
			"error", boundedtext.TruncateUTF8(scrub.Value(err.Error(), scrub.Policy{}), maxAdoptedRunStopErrorBytes))
		if !errors.Is(err, errUnprovableRunAdoptionStop) {
			tr.enqueueTerminalRetry("adoption-resume-stop:"+test.ID, "adopted run resume force-stop proof", finish)
		}
	}
}

func adoptedRunStopUncertaintyMessage(resume *adoptedRunResume, stopErr error) string {
	message := "controller failover: pending " + resume.finishStatus + " cleanup proof: "
	if resume.finishMessage != "" {
		message += scrub.Value(resume.finishMessage, scrub.Policy{}) + "; "
	}
	message += scrub.Value(stopErr.Error(), scrub.Policy{})
	return boundedtext.TruncateUTF8(message, maxAdoptedRunStopErrorBytes)
}

// resumeAdoptedRuns is Phase B, run when admission opens after acquisition.
func (tr *TestRunner) resumeAdoptedRuns() {
	if tr == nil {
		return
	}
	tr.mu.RLock()
	tests := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if test.adoptedResume != nil {
			tests = append(tests, test)
		}
	}
	tr.mu.RUnlock()
	// Runs are independent; resuming them concurrently keeps one slow
	// generator from delaying every other run's supervision re-claim (and
	// the active-endpoint publication that follows this call).
	var resumes sync.WaitGroup
	for _, test := range tests {
		resumes.Add(1)
		go func() {
			defer resumes.Done()
			tr.resumeAdoptedRun(test)
		}()
	}
	resumes.Wait()
	// Interrupted evacuation guards are intentionally not adoption candidates.
	// Once Phase A has installed all valid adoptions, independently fence every
	// possibly-live guard participant before terminalizing those runs.
	tr.resumeInterruptedRunEvacuations()
}

func (tr *TestRunner) resumeAdoptedRun(test *ManagedTest) {
	b, ok := tr.execution.(*distributedExecutionBackend)
	tr.mu.Lock()
	resume := test.adoptedResume
	test.adoptedResume = nil
	executionCtx := test.executionCtx
	token := test.remoteRunSupervisionToken
	active := tr.tests[test.ID] == test && runAdoptionStatus(test.Status)
	tr.mu.Unlock()
	if resume == nil {
		return
	}
	rendezvousTerm := executionLeaseTerm{ownerID: resume.ownerID, generation: resume.generation}
	rendezvousTransferred := false
	defer func() {
		if !rendezvousTransferred {
			tr.dropRunRendezvous(test.ID, rendezvousTerm)
		}
	}()
	if !ok || !active || executionCtx == nil {
		return
	}
	if resume.finishStatus != "" {
		// Phase A decided the outcome, but only Phase B can establish whether
		// every old participant has stopped. A missing generator may keep its
		// orphaned run alive; do not publish a terminal row on reachability alone.
		finish := func() error {
			if err := tr.persistAdoptedRunStopUncertainty(test, resume, errors.New("awaiting exact-generation generator stop proof")); err != nil {
				return err
			}
			if err := tr.confirmAdoptedRunStopped(b, test, resume); err != nil {
				return tr.markAdoptedRunStopUncertain(test, resume, err)
			}
			return tr.completeAdoptedRunAfterStopProof(b, test, resume.finishStatus, resume.finishMessage)
		}
		if err := finish(); err != nil {
			slog.Warn("adopted run remains STOPPING until generator cleanup is proven", "test_id", test.ID,
				"error", boundedtext.TruncateUTF8(scrub.Value(err.Error(), scrub.Policy{}), maxAdoptedRunStopErrorBytes))
			if !errors.Is(err, errUnprovableRunAdoptionStop) {
				tr.enqueueTerminalRetry("adoption-stop:"+test.ID, "adopted run stop proof", finish)
			}
		}
		return
	}
	if len(resume.adopted) == 0 {
		// Every generator finished its share during the gap: finalize from the
		// retained generator archives.
		b.completeDistributedTestRun(tr, test, "COMPLETE", "")
		return
	}
	fail := func(message string) { tr.failResumedAdoptedRunWithStopProof(b, test, resume, message) }
	// Phase A normally secured supervision already (secureAdoptedRuns).
	if resume.secureFailure != "" {
		fail(resume.secureFailure)
		return
	}
	if !resume.secured {
		if message := tr.secureAdoptedRunSupervision(b, test, resume, executionCtx, token); message != "" {
			fail(message)
			return
		}
	}

	tr.mu.RLock()
	liveShards := cloneGroupShardMap(test.remoteGroupShards)
	tr.mu.RUnlock()
	lifecycleCtx, lifecycleCancel := tr.executionOperationContext(context.Background())
	if err := tr.openLiveTableChannels(lifecycleCtx, test.ID, resume.generation, token[:], liveShards, resume.adopted); err != nil {
		lifecycleCancel()
		fail(err.Error())
		return
	}
	if err := tr.openRendezvousChannels(lifecycleCtx, test.ID, resume.generation, token[:], liveShards, resume.adopted); err != nil {
		lifecycleCancel()
		fail(err.Error())
		return
	}
	streams := make(map[string]*generatorrpc.MetricsStream, len(resume.adopted))
	for _, target := range orderedGeneratorTargets(resume.adopted, b.configuredTargets()) {
		stream, err := b.openMetricsStream(lifecycleCtx, resume.adopted[target], test.ID, resume.generation)
		if err != nil {
			for _, opened := range streams {
				_ = opened.CloseSend()
			}
			lifecycleCancel()
			fail(fmt.Sprintf("open generator metrics stream for %q: %v", target, err))
			return
		}
		streams[target] = stream
	}

	runTotals := tr.adoptedRunTotals(test)
	tr.mu.Lock()
	// Admission is already open, so an operator stop may have landed while
	// supervision was re-claimed. The streams are still installed then: the
	// stop finalizer needs the generators' terminal metrics.
	if tr.tests[test.ID] != test || !runAdoptionStatus(test.Status) || test.executionCtx != executionCtx {
		tr.mu.Unlock()
		for _, stream := range streams {
			_ = stream.CloseSend()
		}
		lifecycleCancel()
		return
	}
	first := orderedGeneratorTargets(resume.adopted, b.configuredTargets())[0]
	test.remoteTarget = first
	test.remoteClient = resume.adopted[first]
	test.remoteMetricsStream = streams[first]
	test.remoteMetricsStreamsByTarget = cloneMetricsStreamMap(streams)
	test.remoteMetricsBatchesByTarget = make(map[string]generatorrpc.MetricsBatch)
	installAdoptedRunTotalsLocked(test, runTotals)
	stopping := test.Status == "STOPPING"
	if !stopping {
		test.remoteFinalizing = false
		test.remoteStopFinalizerRunning = false
		test.remoteWorkloadCompleted = false
	}
	test.remoteLifecycleCtx = lifecycleCtx
	test.remoteLifecycleCancel = lifecycleCancel
	test.remoteMetricsLastPublishedAt = time.Time{}
	test.remoteMetricsFlushScheduled = false
	test.remoteMetricsAcceptedRevision = 0
	test.remoteMetricsPublishedRevision = 0
	test.remoteMetricsStoredRevisionByTarget = nil
	test.remoteMetricsGateRevision = 0
	test.remoteMetricsPublishGate = make(chan struct{}, 1)
	test.remoteMetricsPublishGate <- struct{}{}
	test.remoteGoalAcceptedErrorWindows = make(map[string]distributedGoalErrorAccumulator)
	test.remoteGoalPublishedErrorWindows = make(map[string]distributedGoalErrorAccumulator)
	test.remoteGoalUpdateRunning = false
	test.remoteGoalUpdateRevision = 0
	// Goal windows restart conservatively after adoption (plan §10).
	test.goalControl = &goalControllerState{}
	test.remoteActivated = true
	test.remoteActivationPending = false
	test.stageScheduleResume = !stopping
	test.remoteControlGeneration++
	eventSnapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()

	tr.retainRunRendezvousUntil(lifecycleCtx, test.ID, rendezvousTerm)
	rendezvousTransferred = true
	tr.broadcastEvent("test_adopted", eventSnapshot)
	for _, target := range orderedMetricsTargets(streams, b.configuredTargets()) {
		go b.consumeMetrics(lifecycleCtx, tr, test, target, streams[target])
	}
	if !stopping {
		go b.observeGeneratorClocks(lifecycleCtx, tr, test)
		preBaseline, _ := testexec.ResourceBaselineWindows(test.ResourceBaseline)
		preBaseline += testexec.StartDelayWindow(test.StartDelay)
		go tr.startDistributedStageSchedulesAfterBaseline(executionCtx, test, preBaseline)
	}
	slog.Info("adopted run resumed", "stopping", stopping, "test_id", test.ID, "execution_generation", resume.generation, "generators", len(resume.adopted))
}

// excludeFinishedGenerators removes generators whose share finished during
// the gap from supervision and run-lease renewal: they no longer hold the
// assignment, so every renewal against them would fail for the rest of the run.
func (tr *TestRunner) excludeFinishedGenerators(b *distributedExecutionBackend, test *ManagedTest, finished map[string]generatorClient) {
	if len(finished) == 0 {
		return
	}
	tr.mu.Lock()
	if test.remoteRunSupervisionFencedTargets == nil {
		test.remoteRunSupervisionFencedTargets = make(map[string]bool, len(finished))
	}
	for target := range finished {
		test.remoteRunSupervisionFencedTargets[target] = true
	}
	tr.mu.Unlock()
	for target := range finished {
		b.fenceTestExecutionLease(test, target)
	}
}

func minTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}
