package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

// runEvacuationRollback is a successor's plan to return an interrupted
// prepared or staged evacuation to its prior topology. The source never
// crossed its stop fence and no replacement received CommitWork, so the prior
// assignment is the only one that can still produce load.
type runEvacuationRollback struct {
	guard        runEvacuationCheckpoint
	raw          string
	replacements []string
}

// evacuationRollbackGuard reports whether a stored run is held by a version 4
// or 5 prepared, staged or rolling_back journal that this successor may roll back.
// Every other journal, and every journal while the recovery is disabled, keeps
// the fail-closed STOPPING path. The caller holds tr.mu or owns test.
func (tr *TestRunner) evacuationRollbackGuard(test *ManagedTest, raw string) (runEvacuationCheckpoint, []string, bool) {
	if tr == nil || test == nil || !tr.evacuationRollbackRecovery || !tr.runAdoptionEnabled() {
		return runEvacuationCheckpoint{}, nil, false
	}
	if test.Status != "RUNNING" && test.Status != "PAUSED" {
		return runEvacuationCheckpoint{}, nil, false
	}
	if effectiveFailoverPolicy(test.Assignment) != failoverPolicyContinue {
		return runEvacuationCheckpoint{}, nil, false
	}
	if _, distributed := tr.execution.(*distributedExecutionBackend); !distributed {
		return runEvacuationCheckpoint{}, nil, false
	}
	guard, replacements, err := runEvacuationRollbackPlan(raw, test.ID)
	if err != nil {
		return runEvacuationCheckpoint{}, nil, false
	}
	return guard, replacements, true
}

// runEvacuationRollbackPlan validates the journal strictly. Any doubt about the
// prior topology, the moved shard or the capacity it reserved keeps the run on
// the fail-closed path.
func runEvacuationRollbackPlan(raw, testID string) (runEvacuationCheckpoint, []string, error) {
	guard, _, err := interruptedEvacuationTargets(raw, testID)
	if err != nil {
		return runEvacuationCheckpoint{}, nil, err
	}
	if !rollbackRunEvacuationCheckpointVersion(guard.Version) {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal predates successor rollback")
	}
	switch guard.Phase {
	case runEvacuationPhasePrepared, runEvacuationPhaseStaged, runEvacuationPhaseRollingBack:
	default:
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation passed its source fence")
	}
	if !guard.AbandonedAt.IsZero() {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation was abandoned by its leader")
	}
	if slices.Contains(guard.Prior.CompletedTargets, guard.Source) {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation source already completed in the prior topology")
	}
	replacements, err := runEvacuationReplacementTargets(guard)
	if err != nil {
		return runEvacuationCheckpoint{}, nil, err
	}
	if err := runEvacuationSurvivorsUnchanged(guard, replacements); err != nil {
		return runEvacuationCheckpoint{}, nil, err
	}
	if err := validateRunEvacuationCapacityJournal(guard, replacements); err != nil {
		return runEvacuationCheckpoint{}, nil, err
	}
	return guard, replacements, nil
}

func validateRunEvacuationCapacityJournal(guard runEvacuationCheckpoint, replacements []string) error {
	claims := make(map[string]storage.PodCapacityReservation, len(guard.CapacityClaims))
	for _, claim := range guard.CapacityClaims {
		if _, duplicate := claims[claim.Target]; duplicate || !slices.Contains(replacements, claim.Target) || claim.TestID != guard.TestID ||
			claim.Generation != guard.Generation || claim.ReservationID == "" || claim.Incarnation == "" || claim.State != "pending" || len(claim.GroupPeaks) == 0 {
			return fmt.Errorf("evacuation capacity journal has an invalid replacement claim for %s", claim.Target)
		}
		claims[claim.Target] = claim
	}
	seen := make(map[string]struct{}, len(guard.StandbyPreImages))
	for _, standby := range guard.StandbyPreImages {
		claim, journaled := claims[standby.Target]
		if _, duplicate := seen[standby.Target]; duplicate || !journaled || !standbyCapacityReservation(standby) || standby.State != "committed" ||
			standby.TestID != guard.TestID || standby.Generation != guard.Generation || standby.ReservationID != claim.ReservationID ||
			standby.Incarnation != claim.Incarnation || standby.HeadroomPct != claim.HeadroomPct || !sameCapacityVector(standby.Budget, claim.Budget) ||
			!sameCapacityVector(standby.Budget, standby.Demand) {
			return fmt.Errorf("evacuation capacity journal has an invalid standby pre-image for %s", standby.Target)
		}
		seen[standby.Target] = struct{}{}
	}
	return nil
}

// persistRunEvacuationRollbackIntent replaces the journal's prior checkpoint
// with the successor's planned adoption intent before any generator sees the
// rotated token. The journal keeps its version so an older peer still refuses it.
func (tr *TestRunner) persistRunEvacuationRollbackIntent(test *ManagedTest, raw string, guard runEvacuationCheckpoint, planned runContinuityCheckpoint, generation uint64, owner string) (runEvacuationCheckpoint, string, error) {
	next := guard
	next.Phase = runEvacuationPhaseRollingBack
	next.Prior = planned
	next.RollbackGeneration = generation
	next.RollbackOwnerID = owner
	next.RollbackStartedAt = time.Now().UTC()
	encoded, err := json.Marshal(next)
	if err != nil {
		return runEvacuationCheckpoint{}, "", err
	}
	if err := tr.swapRunContinuityCheckpoint(test, raw, string(encoded), owner); err != nil {
		return runEvacuationCheckpoint{}, "", err
	}
	return next, string(encoded), nil
}

func (tr *TestRunner) swapRunContinuityCheckpoint(test *ManagedTest, expected, next, owner string) error {
	store, ok := tr.testStore.(interface {
		CompareAndSwapTestContinuityCheckpointIfLeaseHeld(string, string, string, string, string) (storage.TestStatusTransitionResult, error)
	})
	if !ok {
		return errors.New("test store cannot persist the evacuation rollback")
	}
	var result storage.TestStatusTransitionResult
	if err := tr.trackDurableWrite(func() error {
		var writeErr error
		result, writeErr = store.CompareAndSwapTestContinuityCheckpointIfLeaseHeld(test.ID, expected, next, executionLeaseName, owner)
		return writeErr
	}); err != nil {
		return err
	}
	if result != storage.TestStatusTransitionUpdated {
		return fmt.Errorf("evacuation rollback write not applied: %s", result)
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	if test.continuityCheckpoint != expected {
		return errors.New("continuity checkpoint changed during the evacuation rollback")
	}
	setContinuityCheckpointLocked(test, next)
	return nil
}

// completeRunEvacuationRollback runs after the batched epoch claim, once the
// prior participants were adopted. Each replacement's accepted claim drained
// its staged assignment, so the capacity it reserved can be returned. Only a
// complete rollback replaces the journal with the plain adoption checkpoint;
// any problem leaves the journal for failRunEvacuationRollback. A participant
// that reports a completed run the prior lineage never recorded is a problem.
func (tr *TestRunner) completeRunEvacuationRollback(ctx context.Context, candidate *runAdoptionCandidate, results map[string]runAdoptionTargetResult, completed map[string]generatorClient, completions map[string]generatorrpc.RunAdoptionResult, generation uint64, owner string) ([]string, []string) {
	rollback := candidate.rollback
	natural := tr.runEvacuationNaturalCompletions(candidate.test, rollback.guard, rollback.replacements, completions)
	if completed[rollback.guard.Source] != nil && !natural[rollback.guard.Source] {
		// A staged evacuation may have stopped its source just before the
		// controller was lost. Only a source whose duration ran out is done.
		return nil, []string{fmt.Sprintf("evacuation source %s no longer runs its prior assignment: %s", rollback.guard.Source,
			runEvacuationCompletionDoubt(completions[rollback.guard.Source], runEvacuationExpectedDuration(rollback.guard, rollback.replacements, rollback.guard.Source, tr.runEvacuationDuration(candidate.test))))}
	}
	if problems := unexpectedRunEvacuationCompletions(rollback.guard, rollback.replacements, completed, completions, natural, tr.runEvacuationDuration(candidate.test)); len(problems) > 0 {
		return nil, problems
	}
	var problems []string
	clients := make(map[string]generatorClient, len(rollback.replacements))
	for _, target := range rollback.replacements {
		result, present := results[target]
		switch {
		case !present || (result.err == nil && result.client == nil):
			problems = append(problems, fmt.Sprintf("staged replacement %s could not be fenced: no epoch claim result", target))
			continue
		case result.err != nil:
			problems = append(problems, fmt.Sprintf("staged replacement %s could not be fenced: %v", target, result.err))
			continue
		}
		clients[target] = result.client
	}
	if len(problems) > 0 {
		return nil, problems
	}
	restored, err := tr.rollbackRunEvacuationCapacity(ctx, rollback.guard, rollback.replacements, clients, generation, owner)
	if err != nil {
		return nil, []string{"evacuation capacity rollback: " + err.Error()}
	}
	// The plain checkpoint records a participant whose duration ran out, so a
	// later successor need not ask its generator, which may have pruned the
	// outcome, again.
	encoded, err := json.Marshal(withRunEvacuationNaturalCompletions(rollback.guard.Prior, natural))
	if err == nil {
		err = tr.swapRunContinuityCheckpoint(candidate.test, rollback.raw, string(encoded), owner)
	}
	if err != nil {
		return nil, []string{"record evacuation rollback: " + err.Error()}
	}
	return restored, nil
}

// failRunEvacuationRollback holds the run STOPPING under its evacuation
// journal, then force-stops the prior participants this term just adopted. Without a
// live runtime the run stays eligible for administrator attestation, and a
// newer term's fail-closed recovery fences every possible participant. This
// term cannot fence them itself: its epoch claim does not drain what it adopted.
func (tr *TestRunner) failRunEvacuationRollback(b *distributedExecutionBackend, candidate *runAdoptionCandidate, adopted map[string]generatorClient, clients []map[string]generatorClient, generation uint64, owner string, problems []string) {
	tr.failRunEvacuationRecovery(b, candidate, adopted, clients, generation, owner, "roll back", problems)
}

// failRunEvacuationRecovery holds the run under its journal for a failed
// rollback, roll-forward or restart (action), then cancels any runtime this
// term installed and force-stops what this term adopted with the rotated
// token, adding any unacknowledged stop to the held reason.
func (tr *TestRunner) failRunEvacuationRecovery(b *distributedExecutionBackend, candidate *runAdoptionCandidate, adopted map[string]generatorClient, clients []map[string]generatorClient, generation uint64, owner, action string, problems []string) {
	test := candidate.test
	defer tr.dropRunRendezvous(test.ID, executionLeaseTerm{ownerID: owner, generation: generation})
	defer func() {
		closed := make(map[generatorClient]struct{})
		for _, group := range clients {
			for _, client := range group {
				if _, done := closed[client]; client != nil && !done {
					closed[client] = struct{}{}
					_ = client.Close()
				}
			}
		}
	}()
	// Hold the run before any stop is sent, as failClosedUncertainEvacuation
	// does: a stopped assignment later reports a completed run, so a RUNNING
	// row must never outlive the stops. If this controller is lost after the
	// hold, a newer term fences the adopted assignments. A failed hold write
	// is retried in the background and the stops are still sent.
	detail := "controller failover could not " + action + " the interrupted evacuation: "
	slog.Warn("evacuation recovery failed; the run stays STOPPING until its generators are fenced", "test_id", test.ID, "action", action, "execution_generation", generation, "problems", strings.Join(problems, "; "))
	hold := tr.newInterruptedRunEvacuationHold(test, executionLeaseTerm{ownerID: owner, generation: generation})
	hold.hold(detail + strings.Join(problems, "; "))
	// A runtime this term installed, as a restart does before it runs, stops
	// renewing the run's leases and supervision once the run is held.
	tr.mu.Lock()
	var cancel context.CancelFunc
	if tr.tests[test.ID] == test {
		cancel = test.cancel
	}
	tr.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	targets := make([]string, 0, len(adopted))
	for target := range adopted {
		targets = append(targets, target)
	}
	slices.Sort(targets)
	var unstopped []string
	for _, target := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		stop, err := stopGeneratorTestWithToken(ctx, adopted[target], test.ID, false, generation, candidate.newToken[:])
		cancel()
		if err != nil || !stop.Acknowledged {
			unstopped = append(unstopped, fmt.Sprintf("%s: force stop of the adopted assignment was not acknowledged: %v", target, err))
		}
	}
	if len(unstopped) > 0 {
		slog.Warn("adopted assignments of a failed evacuation recovery were not stopped", "test_id", test.ID, "problems", strings.Join(unstopped, "; "))
		// The same hold: a retry still queued for the first reason writes this one.
		hold.hold(detail + strings.Join(append(problems, unstopped...), "; "))
	}
}

// unexpectedRunEvacuationCompletions reports each participant that completed
// although the journal's prior lineage never recorded it completed, unless its
// generator attributes the completion to the run's duration running out
// (natural). A generator ends every controller stop in a completed run too, so
// any other completion may be a stop by a lost controller or a failed earlier
// successor; it never proves the participant's share ran out.
func unexpectedRunEvacuationCompletions(guard runEvacuationCheckpoint, replacements []string, completed map[string]generatorClient, completions map[string]generatorrpc.RunAdoptionResult, natural map[string]bool, runDuration time.Duration) []string {
	targets := make([]string, 0, len(completed))
	for target := range completed {
		if !slices.Contains(guard.Prior.CompletedTargets, target) && !natural[target] {
			targets = append(targets, target)
		}
	}
	slices.Sort(targets)
	problems := make([]string, 0, len(targets))
	for _, target := range targets {
		role := "participant"
		if slices.Contains(replacements, target) {
			role = "replacement"
		}
		problems = append(problems, fmt.Sprintf("%s %s reports a completed run the journal never recorded: %s", role, target,
			runEvacuationCompletionDoubt(completions[target], runEvacuationExpectedDuration(guard, replacements, target, runDuration))))
	}
	return problems
}

// runEvacuationNaturalCompletions returns the participants whose generator
// attributes their completed run to its duration running out, at a logical
// duration no shorter than the durable expectation. Only these completions
// prove a participant's share is done.
func (tr *TestRunner) runEvacuationNaturalCompletions(test *ManagedTest, guard runEvacuationCheckpoint, replacements []string, completions map[string]generatorrpc.RunAdoptionResult) map[string]bool {
	runDuration := tr.runEvacuationDuration(test)
	natural := make(map[string]bool)
	for target, completion := range completions {
		if runEvacuationCompletionDoubt(completion, runEvacuationExpectedDuration(guard, replacements, target, runDuration)) == "" {
			natural[target] = true
		}
	}
	return natural
}

// runEvacuationDuration is the run's durable configured duration. A duration
// change is persisted before it is sent to the generators.
func (tr *TestRunner) runEvacuationDuration(test *ManagedTest) time.Duration {
	tr.mu.RLock()
	defer tr.mu.RUnlock()
	return test.Duration
}

// runEvacuationExpectedDuration is the logical duration, in
// DurationAdjustment.TargetDuration terms, a participant must have run to: the
// journaled commit target for a replacement, the run's duration for any other.
func runEvacuationExpectedDuration(guard runEvacuationCheckpoint, replacements []string, target string, runDuration time.Duration) time.Duration {
	if slices.Contains(replacements, target) {
		return guard.CommitTargetDuration
	}
	return runDuration
}

// runEvacuationCompletionDoubt says why a completed run does not prove that
// the participant's share ran out, or returns "" when it does.
func runEvacuationCompletionDoubt(completion generatorrpc.RunAdoptionResult, expected time.Duration) string {
	state := strings.TrimSpace(completion.TerminalState)
	switch {
	case completion.Outcome != pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED:
		return "its generator reported no completed outcome"
	case !strings.EqualFold(state, "COMPLETE") && !strings.EqualFold(state, "COMPLETED"):
		return fmt.Sprintf("its terminal state is %q", state)
	}
	switch completion.TerminalCause {
	case pb.RunTerminalCause_RUN_TERMINAL_CAUSE_UNSPECIFIED:
		return "its generator reports no terminal cause, so it cannot be told apart from a controller stop"
	case pb.RunTerminalCause_RUN_TERMINAL_CAUSE_CONTROLLER_STOP:
		return "it was stopped by a controller"
	case pb.RunTerminalCause_RUN_TERMINAL_CAUSE_EXECUTIONS_ENDED_EARLY:
		return "its executions ended before the run's duration ran out"
	case pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED:
	default:
		return fmt.Sprintf("its terminal cause %s is unknown", completion.TerminalCause)
	}
	switch {
	case expected <= 0:
		return "the run records no duration to compare it with"
	case completion.CompletedDuration < expected:
		return fmt.Sprintf("its duration ran out at %s, short of the expected %s", completion.CompletedDuration, expected)
	}
	return ""
}

// withRunEvacuationNaturalCompletions returns checkpoint with each naturally
// completed participant added to its completed targets.
func withRunEvacuationNaturalCompletions(checkpoint runContinuityCheckpoint, natural map[string]bool) runContinuityCheckpoint {
	completed := slices.Clone(checkpoint.CompletedTargets)
	added := make([]string, 0, len(natural))
	for target, done := range natural {
		if done && !slices.Contains(completed, target) {
			added = append(added, target)
		}
	}
	slices.Sort(added)
	checkpoint.CompletedTargets = append(completed, added...)
	return checkpoint
}

// holdInterruptedRunEvacuation keeps a run under its evacuation journal
// STOPPING with no runtime, as reconcileStoredActiveTest does for a journal it
// cannot roll back: possible load is never reported terminal, and an
// administrator can attest the generators stopped. term is the controller term
// that decided the hold.
func (tr *TestRunner) holdInterruptedRunEvacuation(test *ManagedTest, term executionLeaseTerm, detail string) {
	tr.newInterruptedRunEvacuationHold(test, term).hold(detail)
}

// interruptedRunEvacuationHold persists one run's hold for the controller term
// that decided it. The run's latest hold is registered in
// TestRunner.evacuationHolds, and one retry per run writes whichever hold is
// registered until none is left, so neither a later reason of the same hold
// nor a later hold for the run is lost to an earlier retry. Every hold of a
// run writes under the run's one write lock (evacuationHoldWriteLock), and
// only while it is still the run's latest hold.
type interruptedRunEvacuationHold struct {
	tr     *TestRunner
	test   *ManagedTest
	term   executionLeaseTerm
	mu     sync.Mutex
	reason string
}

// evacuationHoldWriteLock serializes every hold write of one run, across hold
// objects, so a write of a superseded hold cannot land after a later one.
func (tr *TestRunner) evacuationHoldWriteLock(testID string) *sync.Mutex {
	tr.evacuationHoldsMu.Lock()
	defer tr.evacuationHoldsMu.Unlock()
	if tr.evacuationHoldWrites == nil {
		tr.evacuationHoldWrites = make(map[string]*sync.Mutex)
	}
	lock := tr.evacuationHoldWrites[testID]
	if lock == nil {
		lock = &sync.Mutex{}
		tr.evacuationHoldWrites[testID] = lock
	}
	return lock
}

func (tr *TestRunner) newInterruptedRunEvacuationHold(test *ManagedTest, term executionLeaseTerm) *interruptedRunEvacuationHold {
	return &interruptedRunEvacuationHold{tr: tr, test: test, term: term}
}

func (h *interruptedRunEvacuationHold) hold(detail string) {
	reason := uncertainEvacuationReasonPrefix + detail
	reason = boundedtext.TruncateUTF8(scrub.Value(reason, scrub.Policy{}), 2048) + uncertainEvacuationIntervention
	h.mu.Lock()
	h.reason = reason
	h.mu.Unlock()
	tr, testID := h.tr, h.test.ID
	if !h.register() {
		slog.Warn("dropped an evacuation hold of an ended term or a replaced run object; the current term's hold of the run stands", "test_id", testID)
		return
	}
	if err := h.persist(); err != nil {
		slog.Warn("persist interrupted evacuation hold remains retryable", "test_id", testID, "error", err)
		tr.ensureEvacuationHoldRetry(testID)
	}
}

// register makes h the run's latest hold. A hold of an ended term, or of a run
// object that is no longer registered, never displaces a registered hold: it
// could not be written, and a current hold it displaced would then write
// nothing. Currency is read outside the registry lock, since run removal takes
// the runner lock before it.
func (h *interruptedRunEvacuationHold) register() bool {
	tr, testID := h.tr, h.test.ID
	for {
		tr.evacuationHoldsMu.Lock()
		existing := tr.evacuationHolds[testID]
		tr.evacuationHoldsMu.Unlock()
		if existing != nil && existing != h && !h.current() {
			return false
		}
		tr.evacuationHoldsMu.Lock()
		if tr.evacuationHolds[testID] != existing {
			tr.evacuationHoldsMu.Unlock()
			continue
		}
		if tr.evacuationHolds == nil {
			tr.evacuationHolds = make(map[string]*interruptedRunEvacuationHold)
		}
		tr.evacuationHolds[testID] = h
		tr.evacuationHoldsMu.Unlock()
		return true
	}
}

// current reports a hold of this process's current term for the registered
// run object.
func (h *interruptedRunEvacuationHold) current() bool {
	if !h.term.valid() || !h.tr.executionLeaseTermLocallyCurrent(h.term) {
		return false
	}
	h.tr.mu.RLock()
	defer h.tr.mu.RUnlock()
	return h.tr.tests[h.test.ID] == h.test
}

// persist writes the latest reason under the hold's own term. There is nothing
// left to hold once the run is terminal, for example after an administrator
// attestation, or its journal was replaced. Once the term has ended the hold is
// dropped rather than written under another lease: the next term decides the
// run from its journal, and a participant this term stopped reports a
// completion that its prior lineage never recorded, which holds the run. The
// term-bound fence claim is the one term gate; a claim refused while the term
// is still current keeps the hold for the retry.
func (h *interruptedRunEvacuationHold) persist() error {
	tr, test := h.tr, h.test
	// The run's write lock spans reading the reason and writing it. A hold a
	// later hold of the run replaced leaves the write to that one.
	write := tr.evacuationHoldWriteLock(test.ID)
	write.Lock()
	defer write.Unlock()
	tr.evacuationHoldsMu.Lock()
	latest := tr.evacuationHolds[test.ID] == h
	tr.evacuationHoldsMu.Unlock()
	if !latest {
		return nil
	}
	if !h.term.valid() {
		h.forget()
		return nil
	}
	h.mu.Lock()
	reason := h.reason
	h.mu.Unlock()
	tr.mu.Lock()
	if tr.tests[test.ID] != test || isTerminalTestStatus(test.Status) || !interruptedRunEvacuation(test.continuityCheckpoint) {
		tr.mu.Unlock()
		h.forget()
		return nil
	}
	if err := tr.claimTestExecutionFenceWithTerm(test, h.term); err != nil {
		tr.mu.Unlock()
		if !tr.executionLeaseTermLocallyCurrent(h.term) {
			h.forget()
			return nil
		}
		return err
	}
	fence := test.executionFence
	tr.mu.Unlock()
	if err := tr.persistStoredTestStatus(test.ID, fence, "STOPPING", nil, reason); err != nil {
		return err
	}
	tr.mu.Lock()
	if tr.tests[test.ID] == test && !isTerminalTestStatus(test.Status) {
		test.Status, test.Error = "STOPPING", reason
		test.evacuationHoldOwner = fence
	}
	tr.mu.Unlock()
	h.mu.Lock()
	written := h.reason == reason
	h.mu.Unlock()
	if written {
		h.forget()
	}
	return nil
}

// forget unregisters the hold unless a later hold replaced it.
func (h *interruptedRunEvacuationHold) forget() {
	h.tr.evacuationHoldsMu.Lock()
	if h.tr.evacuationHolds[h.test.ID] == h {
		delete(h.tr.evacuationHolds, h.test.ID)
	}
	h.tr.evacuationHoldsMu.Unlock()
}

// forgetEvacuationHold drops a removed run's hold; its retry then ends.
func (tr *TestRunner) forgetEvacuationHold(testID string) {
	tr.evacuationHoldsMu.Lock()
	delete(tr.evacuationHolds, testID)
	delete(tr.evacuationHoldWrites, testID)
	tr.evacuationHoldsMu.Unlock()
}

// ensureEvacuationHoldRetry starts the run's hold retry unless one runs. Both
// starting and ending are decided under evacuationHoldsMu, so a hold
// registered while a retry ends always gets a retry.
func (tr *TestRunner) ensureEvacuationHoldRetry(testID string) {
	tr.evacuationHoldsMu.Lock()
	defer tr.evacuationHoldsMu.Unlock()
	if _, running := tr.evacuationHoldRetrying[testID]; running || tr.evacuationHolds[testID] == nil {
		return
	}
	if tr.evacuationHoldRetrying == nil {
		tr.evacuationHoldRetrying = make(map[string]struct{})
	}
	tr.evacuationHoldRetrying[testID] = struct{}{}
	go tr.retryEvacuationHold(testID)
}

// retryEvacuationHold writes the run's registered hold until none is left. It
// does not wait for admission like the terminal-transition retries: holds are
// decided during acquisition reconcile (Phase A), and each hold is gated on its
// own term instead.
func (tr *TestRunner) retryEvacuationHold(testID string) {
	delay := executionPlanRetryInitialDelay
	for {
		timer := time.NewTimer(delay)
		<-timer.C
		tr.mu.RLock()
		shuttingDown := tr.shuttingDown
		tr.mu.RUnlock()
		tr.evacuationHoldsMu.Lock()
		h := tr.evacuationHolds[testID]
		if h == nil || shuttingDown {
			delete(tr.evacuationHolds, testID)
			delete(tr.evacuationHoldRetrying, testID)
			tr.evacuationHoldsMu.Unlock()
			return
		}
		tr.evacuationHoldsMu.Unlock()
		if err := h.persist(); err != nil {
			slog.Warn("interrupted evacuation hold retry failed", "test_id", testID, "error", err)
			if !durableBackgroundRetryable(err) {
				h.forget()
			}
		}
		delay = min(delay*2, executionPlanRetryMaxDelay)
	}
}

// rollbackRunEvacuationCapacity returns each replacement's reservation to its
// pre-evacuation state under the successor's term. A generator accepts
// capacity changes only at its current epoch, so an activated standby is
// released and reserved idle again rather than revised in place. Each step is
// idempotent: a retry, or a later successor, resumes from the ledger it finds.
// It returns the standby targets now held idle, whose ownership adoption
// transfers.
func (tr *TestRunner) rollbackRunEvacuationCapacity(ctx context.Context, guard runEvacuationCheckpoint, replacements []string, clients map[string]generatorClient, generation uint64, owner string) ([]string, error) {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		if len(guard.CapacityClaims) > 0 {
			return nil, errors.New("capacity ledger unavailable")
		}
		return nil, nil
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return nil, fmt.Errorf("list replacement reservations: %w", err)
	}
	current := make(map[string]storage.PodCapacityReservation)
	for _, row := range rows {
		if row.TestID == guard.TestID {
			current[row.Target] = row
		}
	}
	claims := make(map[string]storage.PodCapacityReservation, len(guard.CapacityClaims))
	for _, claim := range guard.CapacityClaims {
		claims[claim.Target] = claim
	}
	preImages := make(map[string]storage.PodCapacityReservation, len(guard.StandbyPreImages))
	for _, standby := range guard.StandbyPreImages {
		preImages[standby.Target] = standby
	}
	var restored []string
	var failures []error
	for _, target := range replacements {
		if err := ctx.Err(); err != nil {
			failures = append(failures, err)
			break
		}
		row, present := current[target]
		claim, journaled := claims[target]
		if !journaled {
			if present {
				failures = append(failures, fmt.Errorf("%s holds a reservation the evacuation journal did not record", target))
			}
			continue
		}
		capacity, ok := clients[target].(podCapacityClient)
		if !ok {
			failures = append(failures, fmt.Errorf("%s cannot confirm capacity changes", target))
			continue
		}
		if present && len(row.GroupPeaks) > 0 {
			if err := tr.releaseRunEvacuationReplacementClaim(ctx, store, capacity, row, claim, generation, owner); err != nil {
				failures = append(failures, fmt.Errorf("%s: %w", target, err))
				continue
			}
			present = false
		}
		standby, reserved := preImages[target]
		if !reserved {
			if present {
				failures = append(failures, fmt.Errorf("%s holds a reservation that is neither the replacement nor its standby", target))
			}
			continue
		}
		if err := tr.restoreRunEvacuationStandby(ctx, store, capacity, row, present, standby, generation, owner); err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", target, err))
			continue
		}
		restored = append(restored, target)
	}
	return restored, errors.Join(failures...)
}

// runEvacuationReleaseDrainAttempts and runEvacuationReleaseDrainRetryDelay
// bound the retries of a replacement release refused while the generator
// finishes the run's terminal transition.
var (
	runEvacuationReleaseDrainAttempts   = 20
	runEvacuationReleaseDrainRetryDelay = 250 * time.Millisecond
)

func (tr *TestRunner) releaseRunEvacuationReplacementClaim(ctx context.Context, store podCapacityStore, capacity podCapacityClient, row, claim storage.PodCapacityReservation, generation uint64, owner string) error {
	journaled := row
	journaled.State = claim.State
	if !reflect.DeepEqual(journaled, claim) || (row.State != "pending" && row.State != "releasing") {
		return errors.New("replacement reservation changed since the evacuation journal")
	}
	if row.State == "pending" {
		if err := tr.transitionPodLedger(store, row.Target, row.TestID, row.Incarnation, row.ReservationID, "releasing", executionLeaseName, owner, generation); err != nil {
			return fmt.Errorf("mark replacement reservation releasing: %w", err)
		}
	}
	request := podClaim(row, true)
	request.ExecutionGeneration = generation
	var ack *pb.PodCapacityAck
	var err error
	// A run whose generator already reported its terminal outcome may not have
	// closed its stop completion yet (a fence write follows the tombstone), and
	// the generator refuses the release until it has. That window is short, so
	// retry a few times. Rollback and roll-forward both release through here.
	for attempt := 1; ; attempt++ {
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		ack, err = capacity.ReservePodCapacity(callCtx, request)
		cancel()
		if err != nil || ack.GetAccepted() || ack.GetErrorMessage() != generatorrpc.PodCapacityStillDrainingMessage || attempt >= runEvacuationReleaseDrainAttempts {
			break
		}
		if sleepWithContext(ctx, runEvacuationReleaseDrainRetryDelay); ctx.Err() != nil {
			break
		}
	}
	if err != nil || !podCapacityAckConfirms(ack, row.Incarnation) {
		return fmt.Errorf("generator did not confirm the replacement release: %v %s", err, ack.GetErrorMessage())
	}
	if err := tr.transitionPodLedger(store, row.Target, row.TestID, row.Incarnation, row.ReservationID, "released", executionLeaseName, owner, generation); err != nil {
		return fmt.Errorf("record replacement release: %w", err)
	}
	return nil
}

func (tr *TestRunner) restoreRunEvacuationStandby(ctx context.Context, store podCapacityStore, capacity podCapacityClient, row storage.PodCapacityReservation, present bool, standby storage.PodCapacityReservation, generation uint64, owner string) error {
	if !present {
		row = standby
		row.Generation = generation
		row.State = "pending"
		if err := tr.reservePodLedger(store, []storage.PodCapacityReservation{row}, executionLeaseName, owner, generation); err != nil {
			return fmt.Errorf("reserve the standby idle again: %w", err)
		}
	}
	if !restoredRunEvacuationStandby(row, standby) || row.Generation == 0 || row.Generation > generation {
		return errors.New("standby reservation changed since the evacuation journal")
	}
	switch row.State {
	case "committed":
		return nil
	case "pending":
	default:
		return fmt.Errorf("standby reservation is %s", row.State)
	}
	// A pending idle row may be the predecessor's own rollback or this term's
	// reservation. Either way the generator must hold the idle claim at this
	// epoch before the ledger commits it.
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	request := podClaim(row, false)
	request.ExecutionGeneration = generation
	ack, err := capacity.ReservePodCapacity(callCtx, request)
	cancel()
	if err != nil || !podCapacityAckConfirms(ack, row.Incarnation) {
		return fmt.Errorf("generator did not confirm the idle standby: %v %s", err, ack.GetErrorMessage())
	}
	if err := tr.transitionPodLedger(store, row.Target, row.TestID, row.Incarnation, row.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
		return fmt.Errorf("commit the idle standby: %w", err)
	}
	return nil
}

// restoredRunEvacuationStandby accepts an idle row with the standby's frozen
// identity and budget. Its revision and owner term may differ: the predecessor's
// own rollback advances the revision, and this term re-reserves it.
func restoredRunEvacuationStandby(row, standby storage.PodCapacityReservation) bool {
	return standbyCapacityReservation(row) && row.TestID == standby.TestID && row.Target == standby.Target &&
		row.ReservationID == standby.ReservationID && row.Incarnation == standby.Incarnation && row.ModelVersion == standby.ModelVersion &&
		row.HeadroomPct == standby.HeadroomPct && sameCapacityVector(row.Budget, standby.Budget) && sameCapacityVector(row.Demand, standby.Budget) &&
		reflect.DeepEqual(row.RecoveryPlacement, standby.RecoveryPlacement)
}

// restoreRunEvacuationPriorCheckpoint restores the checkpoint a version 4 or 5
// journal replaced once the same leader has positively rolled its staging back before
// the source fence. Left in place, the journal's prior topology would go stale
// as the run's VUsers change, and a successor would roll back to it. Older
// journals keep their conservative fail-closed behavior.
func (tr *TestRunner) restoreRunEvacuationPriorCheckpoint(test *ManagedTest, raw string, generation uint64, owner string) {
	var guard runEvacuationCheckpoint
	if tr == nil || test == nil || raw == "" || json.Unmarshal([]byte(raw), &guard) != nil || !rollbackRunEvacuationCheckpointVersion(guard.Version) ||
		(guard.Phase != runEvacuationPhasePrepared && guard.Phase != runEvacuationPhaseStaged) || guard.TestID != test.ID || guard.Generation != generation || guard.OwnerID != owner {
		return
	}
	// A journal begun without an armed checkpoint restores that absence.
	restored := ""
	if !guard.Prior.ArmedAt.IsZero() {
		encoded, err := json.Marshal(guard.Prior)
		if err != nil {
			return
		}
		restored = string(encoded)
	}
	if err := tr.swapRunContinuityCheckpoint(test, raw, restored, owner); err != nil {
		slog.Warn("evacuation journal remains after a clean rollback; a successor may roll the run back to the journal's topology", "test_id", test.ID, "error", err)
	}
}

// markRunEvacuationCheckpointAbandoned marks a same-term version 4 or 5
// prepared or staged journal its leader no longer drives: a capacity return that did not
// complete, or a dead journal found on re-arm. The journal and its pre-images
// stay; only the successor rollback is refused. It returns the journal now in
// effect.
func (tr *TestRunner) markRunEvacuationCheckpointAbandoned(test *ManagedTest, raw string, generation uint64, owner string) string {
	var guard runEvacuationCheckpoint
	if tr == nil || test == nil || raw == "" || json.Unmarshal([]byte(raw), &guard) != nil || !rollbackRunEvacuationCheckpointVersion(guard.Version) ||
		(guard.Phase != runEvacuationPhasePrepared && guard.Phase != runEvacuationPhaseStaged) || guard.TestID != test.ID || guard.Generation != generation || guard.OwnerID != owner || !guard.AbandonedAt.IsZero() {
		return raw
	}
	guard.AbandonedAt = time.Now().UTC()
	encoded, err := json.Marshal(guard)
	if err != nil {
		return raw
	}
	if err := tr.swapRunContinuityCheckpoint(test, raw, string(encoded), owner); err != nil {
		slog.Warn("could not mark an evacuation journal abandoned; a successor may roll the run back to its topology", "test_id", test.ID, "error", err)
		return raw
	}
	return string(encoded)
}

// runEvacuationCapacityReturned checks the ledger itself, since an ordinary
// release reports no error: every replacement reservation is gone and every
// activated standby is idle and committed again.
func (tr *TestRunner) runEvacuationCapacityReturned(testID string, claims []storage.PodCapacityReservation, standbys map[string]storage.PodCapacityReservation) bool {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return false
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return false
	}
	current := make(map[string]storage.PodCapacityReservation)
	for _, row := range rows {
		if row.TestID == testID {
			current[row.Target] = row
		}
	}
	for _, claim := range claims {
		row, present := current[claim.Target]
		standby, reserved := standbys[claim.Target]
		switch {
		case reserved && (!present || row.State != "committed" || !restoredRunEvacuationStandby(row, standby)):
			return false
		case !reserved && present:
			return false
		}
	}
	return true
}

// WithEvacuationRollbackRecovery enables version 4 evacuation journals and
// successor rollback of prepared or staged evacuations. Enable it only once
// every controller replica runs a release that reads version 4 journals.
func WithEvacuationRollbackRecovery(enabled bool) TestRunnerOption {
	return func(tr *TestRunner) {
		tr.evacuationRollbackRecovery = enabled
	}
}
