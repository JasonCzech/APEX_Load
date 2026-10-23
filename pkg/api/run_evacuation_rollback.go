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
	"time"

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
// prepared, staged or rolling_back journal that this successor may roll back.
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
	if guard.Version != runEvacuationRollbackCheckpointVersion {
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
	// The planner moves only the source's shards to targets new to the run. A
	// journal that changed any surviving participant cannot be undone by
	// adopting the prior topology.
	survivors := make(map[string]struct{})
	for _, topology := range []map[string][]GeneratorShardSummary{guard.BeforeShards, guard.NextShards} {
		for _, shards := range topology {
			for _, shard := range shards {
				if shard.Target != guard.Source && !slices.Contains(replacements, shard.Target) {
					survivors[shard.Target] = struct{}{}
				}
			}
		}
	}
	for target := range survivors {
		if targetShardAssignmentChanged(guard.BeforeShards, guard.NextShards, target) {
			return runEvacuationCheckpoint{}, nil, fmt.Errorf("evacuation changed surviving participant %s", target)
		}
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
// rotated token. The journal stays version 4 so an older peer still refuses it.
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
// any problem leaves the journal for failRunEvacuationRollback.
func (tr *TestRunner) completeRunEvacuationRollback(ctx context.Context, candidate *runAdoptionCandidate, results map[string]runAdoptionTargetResult, completed map[string]generatorClient, generation uint64, owner string) ([]string, []string) {
	rollback := candidate.rollback
	if completed[rollback.guard.Source] != nil {
		// A staged evacuation may have stopped its source just before the
		// controller was lost. Completed here cannot be told apart from that.
		return nil, []string{fmt.Sprintf("evacuation source %s no longer runs its prior assignment", rollback.guard.Source)}
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
	encoded, err := json.Marshal(rollback.guard.Prior)
	if err == nil {
		err = tr.swapRunContinuityCheckpoint(candidate.test, rollback.raw, string(encoded), owner)
	}
	if err != nil {
		return nil, []string{"record evacuation rollback: " + err.Error()}
	}
	return restored, nil
}

// failRunEvacuationRollback force-stops the prior participants this term just
// adopted and keeps the run STOPPING under its evacuation journal. Without a
// live runtime the run stays eligible for administrator attestation, and a
// newer term's fail-closed recovery fences every possible participant. This
// term cannot fence them itself: its epoch claim does not drain what it adopted.
func (tr *TestRunner) failRunEvacuationRollback(b *distributedExecutionBackend, candidate *runAdoptionCandidate, adopted map[string]generatorClient, clients []map[string]generatorClient, generation uint64, owner string, problems []string) {
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
	targets := make([]string, 0, len(adopted))
	for target := range adopted {
		targets = append(targets, target)
	}
	slices.Sort(targets)
	for _, target := range targets {
		ctx, cancel := context.WithTimeout(context.Background(), b.timeout)
		stop, err := stopGeneratorTestWithToken(ctx, adopted[target], test.ID, false, generation, candidate.newToken[:])
		cancel()
		if err != nil || !stop.Acknowledged {
			problems = append(problems, fmt.Sprintf("%s: force stop of the adopted prior assignment was not acknowledged: %v", target, err))
		}
	}
	slog.Warn("evacuation rollback failed; the run stays STOPPING until its generators are fenced", "test_id", test.ID, "execution_generation", generation, "problems", strings.Join(problems, "; "))
	tr.holdInterruptedRunEvacuation(test, "controller failover could not roll back the interrupted evacuation: "+strings.Join(problems, "; "))
}

// holdInterruptedRunEvacuation keeps a run under its evacuation journal
// STOPPING with no runtime, as reconcileStoredActiveTest does for a journal it
// cannot roll back: possible load is never reported terminal, and an
// administrator can attest the generators stopped.
func (tr *TestRunner) holdInterruptedRunEvacuation(test *ManagedTest, detail string) {
	reason := uncertainEvacuationReasonPrefix + detail
	reason = boundedtext.TruncateUTF8(scrub.Value(reason, scrub.Policy{}), 2048) + uncertainEvacuationIntervention
	persist := func() error {
		tr.mu.Lock()
		if tr.tests[test.ID] != test {
			tr.mu.Unlock()
			return nil
		}
		if err := tr.claimTestExecutionFence(test); err != nil {
			tr.mu.Unlock()
			return err
		}
		fence := test.executionFence
		tr.mu.Unlock()
		if err := tr.persistStoredTestStatus(test.ID, fence, "STOPPING", nil, reason); err != nil {
			return err
		}
		tr.mu.Lock()
		if tr.tests[test.ID] == test {
			test.Status, test.Error = "STOPPING", reason
		}
		tr.mu.Unlock()
		return nil
	}
	if err := persist(); err != nil {
		slog.Warn("persist interrupted evacuation hold remains retryable", "test_id", test.ID, "error", err)
		tr.enqueueTerminalRetry("evacuation-rollback:"+test.ID, "interrupted evacuation hold", persist)
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
	callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	request := podClaim(row, true)
	request.ExecutionGeneration = generation
	ack, err := capacity.ReservePodCapacity(callCtx, request)
	cancel()
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

// abandonRunEvacuationCheckpoint restores the checkpoint a version 4 journal
// replaced once the same leader has positively rolled its staging back before
// the source fence. Left in place, the journal's prior topology would go stale
// as the run's VUsers change, and a successor would roll back to it. Older
// journals keep their conservative fail-closed behavior.
func (tr *TestRunner) abandonRunEvacuationCheckpoint(test *ManagedTest, raw string, generation uint64, owner string) {
	var guard runEvacuationCheckpoint
	if tr == nil || test == nil || raw == "" || json.Unmarshal([]byte(raw), &guard) != nil || guard.Version != runEvacuationRollbackCheckpointVersion ||
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

// markRunEvacuationCheckpointAbandoned records that the journal's leader rolled
// the staging back but could not return its capacity. The journal and its
// pre-images stay; only the successor rollback is refused. It returns the
// journal now in effect.
func (tr *TestRunner) markRunEvacuationCheckpointAbandoned(test *ManagedTest, raw string, generation uint64, owner string) string {
	var guard runEvacuationCheckpoint
	if tr == nil || test == nil || raw == "" || json.Unmarshal([]byte(raw), &guard) != nil || guard.Version != runEvacuationRollbackCheckpointVersion ||
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
