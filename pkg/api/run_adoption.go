package api

import (
	"context"
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
	runAdoptionKeeperWait = 20 * time.Second
)

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
	_, committed := committedRunEvacuationCheckpoint(test.continuityCheckpoint, test.ID)
	return committed
}

// awaitRunAdoptionReconcile holds the generator epoch keeper until acquisition
// reconcile (which performs the adoption claims) finishes, bounded so a stuck
// reconcile cannot leave generators unclaimed.
func (tr *TestRunner) awaitRunAdoptionReconcile(ctx context.Context) {
	if !tr.runAdoptionEnabled() {
		return
	}
	deadline := time.Now().Add(runAdoptionKeeperWait)
	for tr.leadership.reconciling.Load() && tr.leadership.IsLeader() {
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

// runAdoptionStatus is the set of stored statuses a successor adopts. A
// STOPPING run is taken over only to finish the operator's stop cleanly.
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
		tr.mu.RUnlock()
		if _, committed := committedRunEvacuationCheckpoint(raw, test.ID); committed {
			promoted, promoteErr := tr.promoteCommittedRunEvacuationCheckpoint(ctx, test, raw, generation, ownerID)
			if promoteErr != nil {
				return fmt.Errorf("promote committed evacuation for %s: %w", test.ID, promoteErr)
			}
			raw = promoted
		}
		checkpoint, _, decodeErr := decodeRunContinuityCheckpoint(raw)
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
			if stopping {
				tr.finishStoppingRunAdoption(b, test, nil, nil, nil, generation, ownerID, candidate.newToken, []string{fmt.Sprintf("adoption checkpoint unusable (%v)", decodeErr)})
				continue
			}
			tr.failRunAdoption(b, test, nil, generation, ownerID, candidate.newToken, fmt.Sprintf("controller failover: the run's adoption checkpoint is unusable (%v)", decodeErr))
			continue
		}
		// No generator may see the rotation until its encrypted token and prior
		// proof chain survive a controller crash. A stale snapshot cannot replace
		// a newer intent from this or another lease owner.
		if err := tr.persistRunAdoptionIntent(test, raw, candidate.checkpoint, ownerID); err != nil {
			return fmt.Errorf("persist adoption intent for %s: %w", test.ID, err)
		}
		candidate.targets = checkpointTargets(checkpoint)
		for _, target := range candidate.targets {
			if slices.Contains(checkpoint.CompletedTargets, target) {
				continue
			}
			byTarget[target] = append(byTarget[target], adoption)
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
			if slices.Contains(candidate.checkpoint.CompletedTargets, target) {
				client, dialErr := b.retainRunAdoptionTarget(ctx, results, target)
				if dialErr != nil {
					problems = append(problems, fmt.Sprintf("%s: reconnect completed generator: %v", target, dialErr))
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
				tr.finishStoppingRunAdoption(b, candidate.test, reachable, adopted, archives, generation, ownerID, candidate.newToken, problems)
			})
			continue
		}
		tr.isolateRunAdoption(b, candidate.test, generation, ownerID, func() {
			capacityClients := make(map[string]generatorClient, len(adopted)+len(candidate.standbyTargets))
			for target, client := range adopted {
				capacityClients[target] = client
			}
			for _, target := range candidate.standbyTargets {
				result := results[target]
				if slices.Contains(candidate.targets, target) || result.err != nil || result.client == nil {
					problems = append(problems, fmt.Sprintf("standby %s could not be fenced for capacity transfer", target))
					continue
				}
				capacityClients[target] = result.client
			}
			if len(problems) == 0 {
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
				tr.bindRunAdoptionOutcome(b, candidate.test, reachable, adopted, archives, generation, ownerID, candidate.newToken, "FAILED", message)
				return
			}
			tr.installAdoptedRun(b, candidate, adopted, completed, archives, generation, ownerID)
		})
	}
	return nil
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

func (tr *TestRunner) installAdoptedRun(b *distributedExecutionBackend, candidate *runAdoptionCandidate, adopted, completed, archives map[string]generatorClient, generation uint64, ownerID string) {
	test := candidate.test
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
		for target := range completed {
			test.remoteTargetStates[target] = "complete"
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
	slog.Warn("run adoption failed; the run will be marked FAILED", "test_id", test.ID, "execution_generation", generation, "reason", message)
	tr.bindRunAdoptionOutcome(b, test, adopted, adopted, nil, generation, ownerID, token, "FAILED", message)
}

// bindRunAdoptionOutcome binds a run that will not resume to the new term, so
// stop and archive collection work under the rotated token, and records the
// outcome Phase B applies. Only adopted generators are stopped; the others in
// clients (already stopped or finished) serve the archive.
func (tr *TestRunner) bindRunAdoptionOutcome(b *distributedExecutionBackend, test *ManagedTest, clients, adopted, archives map[string]generatorClient, generation uint64, ownerID string, token [generatorrpc.RunSupervisionTokenSize]byte, status, message string) {
	tr.mu.Lock()
	err := tr.installRunAdoptionHandlesLocked(b, test, clients, nil, token)
	if err == nil {
		test.remoteArchiveClientsByTarget = archives
		for target := range clients {
			if _, running := adopted[target]; !running {
				test.remoteTargetStates[target] = "aborted"
			}
		}
		test.adoptedResume = &adoptedRunResume{generation: generation, ownerID: ownerID, adoptedAt: time.Now().UTC(), finishStatus: status, finishMessage: message}
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

// finishStoppingRunAdoption completes a run whose operator stop was in flight
// when the leader was lost. The stop is honoured as ABORTED, the outcome of an
// ordinary stop: generators still running the run under the rotated token are
// stopped, and the archive is collected from every reachable generator.
func (tr *TestRunner) finishStoppingRunAdoption(b *distributedExecutionBackend, test *ManagedTest, clients, adopted, archives map[string]generatorClient, generation uint64, ownerID string, token [generatorrpc.RunSupervisionTokenSize]byte, problems []string) {
	if len(problems) > 0 {
		slog.Warn("finishing a stopping run after controller failover with incomplete generator reach", "test_id", test.ID, "problems", strings.Join(problems, "; "))
	}
	slog.Info("finishing an in-flight stop after controller failover", "test_id", test.ID, "execution_generation", generation, "generators", len(clients), "still_running", len(adopted))
	tr.bindRunAdoptionOutcome(b, test, clients, adopted, archives, generation, ownerID, token, "ABORTED", "")
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
	if resume == nil || !ok || !active || executionCtx == nil {
		return
	}
	if resume.finishStatus != "" {
		// Decided in Phase A; finalized now that archive publication can run.
		b.completeDistributedTestRun(tr, test, resume.finishStatus, resume.finishMessage)
		return
	}
	if len(resume.adopted) == 0 {
		// Every generator finished its share during the gap: finalize from the
		// retained generator archives.
		b.completeDistributedTestRun(tr, test, "COMPLETE", "")
		return
	}
	fail := func(message string) {
		b.completeDistributedTestRun(tr, test, "FAILED", "controller failover: "+message)
	}
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
			fail(fmt.Sprintf("execution leadership changed before run leases were re-established: %v", claimErr))
			return
		}
		if err := b.renewGeneratorTestExecutionClients(executionCtx, test.ID, resume.adopted, resume.generation, resume.ownerID, leaseDeadline); err != nil {
			fail(fmt.Sprintf("re-establish generator run leases: %v", err))
			return
		}
		go b.renewGeneratorExecutionEpoch(executionCtx, tr, test, resume.generation)
	}
	if err := b.claimRunSupervisionClients(executionCtx, test.ID, resume.adopted, resume.generation, resume.ownerID, token[:]); err != nil {
		fail(fmt.Sprintf("re-establish generator run supervision: %v", err))
		return
	}
	b.startRunSupervisionKeeper(executionCtx, tr, test, resume.adopted, resume.generation, resume.ownerID, token)

	lifecycleCtx, lifecycleCancel := tr.executionOperationContext(context.Background())
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

	tr.broadcastEvent("test_adopted", eventSnapshot)
	for _, target := range orderedMetricsTargets(streams, b.configuredTargets()) {
		go b.consumeMetrics(lifecycleCtx, tr, test, target, streams[target])
	}
	if !stopping {
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
