package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/dsl"
	"github.com/jasonczech/apexload/pkg/storage"
)

// runEvacuationRollForward is a successor's plan to finish an interrupted
// evacuation whose source was positively fenced and whose replacement may have
// received CommitWork. The source can no longer produce load, so the next
// topology is the only one that can. Only an adopted replacement proves it
// crossed its start barrier: a staged one is drained by the epoch claim. A
// completed one proves it only when its generator reports that the
// replacement's journaled duration ran out; any other completion may be a
// stopped staged assignment, so the run is held STOPPING. So is a survivor
// completed outside the prior lineage for any reason but its duration.
type runEvacuationRollForward struct {
	guard        runEvacuationCheckpoint
	raw          string
	replacements []string
}

// evacuationRollForwardGuard reports whether a stored run is held by a version
// 5 replacement_committing or rolling_forward journal that this successor may
// roll forward. Every other journal, and every journal while the recovery is
// disabled, keeps the fail-closed STOPPING path. The caller holds tr.mu or
// owns test.
func (tr *TestRunner) evacuationRollForwardGuard(test *ManagedTest, raw string) (runEvacuationCheckpoint, []string, bool) {
	if tr == nil || test == nil || !tr.evacuationRollbackRecovery || !tr.evacuationRollForwardRecovery || !tr.runAdoptionEnabled() {
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
	guard, replacements, err := runEvacuationRollForwardPlan(raw, test.ID)
	if err != nil {
		return runEvacuationCheckpoint{}, nil, false
	}
	// The replacement was staged in the run's pause state. A pause or resume
	// since then cannot have reached it.
	if paused := test.Status == "PAUSED" || test.PausedAt != nil; paused != guard.StagedPaused {
		return runEvacuationCheckpoint{}, nil, false
	}
	if runEvacuationRollForwardWorkloadEligible(test, guard) != nil {
		return runEvacuationCheckpoint{}, nil, false
	}
	return guard, replacements, true
}

// runEvacuationRollForwardPlan validates the journal strictly. Any doubt about
// the moved shard, its duration or the capacity it reserved keeps the run on
// the fail-closed path.
func runEvacuationRollForwardPlan(raw, testID string) (runEvacuationCheckpoint, []string, error) {
	guard, _, err := interruptedEvacuationTargets(raw, testID)
	if err != nil {
		return runEvacuationCheckpoint{}, nil, err
	}
	if guard.Version != runEvacuationRollForwardCheckpointVersion {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal predates successor roll-forward")
	}
	switch guard.Phase {
	case runEvacuationPhaseReplacementCommitting, runEvacuationPhaseRollingForward:
	case runEvacuationPhaseSourceFenced:
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation replacement never received CommitWork")
	default:
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal records no replacement commit")
	}
	if !guard.AbandonedAt.IsZero() {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation was abandoned by its leader")
	}
	// As for a committed promotion, the predecessor's lineage must have been
	// armed and must not already have retired the source.
	if guard.Prior.ArmedAt.IsZero() {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation predecessor lineage was never armed")
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
	// The successor sends the journaled target. It never exceeds what the
	// replacement was staged with, so roll-forward cannot extend the run.
	if guard.StagedSteadyElapsed < 0 || guard.StagedRemaining <= 0 || guard.CommitTargetDuration <= 0 ||
		guard.CommitTargetDuration > guard.StagedSteadyElapsed+guard.StagedRemaining {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal has no usable replacement duration")
	}
	return guard, replacements, nil
}

// runEvacuationReplacementNeverCommitted reports a version 5 journal held at
// source_fenced: its leader writes replacement_committing before sending
// CommitWork, so no replacement can have crossed its start barrier.
func runEvacuationReplacementNeverCommitted(raw string) bool {
	var guard runEvacuationCheckpoint
	return json.Unmarshal([]byte(raw), &guard) == nil && guard.Version == runEvacuationRollForwardCheckpointVersion && guard.Phase == runEvacuationPhaseSourceFenced
}

// runEvacuationSurvivorsUnchanged refuses a journal that changed any surviving
// participant. The planner moves only the source's shards to targets new to
// the run, so neither the prior nor the next topology can be adopted otherwise.
func runEvacuationSurvivorsUnchanged(guard runEvacuationCheckpoint, replacements []string) error {
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
			return fmt.Errorf("evacuation changed surviving participant %s", target)
		}
	}
	return nil
}

// runEvacuationRollForwardWorkloadEligible repeats the workload refusals
// evacuateGenerator applies to the moved groups. The successor may only finish
// an evacuation the run was still allowed to make.
func runEvacuationRollForwardWorkloadEligible(test *ManagedTest, guard runEvacuationCheckpoint) error {
	if test.TerminationMode == "iterations" {
		return errors.New("iteration work cannot be moved without a progress checkpoint")
	}
	groups := make(map[string]TestGroup, len(test.Groups))
	for _, group := range test.Groups {
		groups[group.Name] = group
	}
	for name, shards := range guard.BeforeShards {
		if shardVUsersForTarget(shards, guard.Source) <= 0 {
			continue
		}
		group, known := groups[name]
		if !known {
			return fmt.Errorf("evacuated group %s is not part of the run", name)
		}
		if specs, err := dsl.ScriptDistributedRendezvous(group.script); err != nil || len(specs) > 0 {
			return fmt.Errorf("%s: distributed rendezvous cannot be moved without a VUser progress checkpoint", name)
		}
		if ok, why := groupAllowsGeneratorLossReshard(group); !ok {
			return fmt.Errorf("%s: %s", name, why)
		}
		if ok, why := groupAllowsStatelessDataReshard(group); !ok {
			return fmt.Errorf("%s: %s", name, why)
		}
	}
	return nil
}

// runEvacuationRollForwardCheckpoint is the adoption checkpoint the run rolls
// forward to. A rolling_forward journal carries it with the successor's planned
// intent. Otherwise it is built as a committed promotion builds it: the
// predecessor's lineage over the next topology, with the fenced source kept
// for archive collection only.
func runEvacuationRollForwardCheckpoint(guard runEvacuationCheckpoint) runContinuityCheckpoint {
	if guard.RollForwardCheckpoint != nil {
		checkpoint := *guard.RollForwardCheckpoint
		checkpoint.Shards = cloneGroupShardMap(checkpoint.Shards)
		checkpoint.PendingAdoptions = slices.Clone(checkpoint.PendingAdoptions)
		return checkpoint
	}
	checkpoint := guard.Prior
	checkpoint.Shards = cloneGroupShardMap(guard.NextShards)
	checkpoint.CompletedTargets = slices.Clone(checkpoint.CompletedTargets)
	checkpoint.ArchiveTargets = slices.Clone(checkpoint.ArchiveTargets)
	if !slices.Contains(checkpoint.ArchiveTargets, guard.Source) {
		checkpoint.ArchiveTargets = append(checkpoint.ArchiveTargets, guard.Source)
		slices.Sort(checkpoint.ArchiveTargets)
	}
	checkpoint.PendingAdoptions = slices.Clone(checkpoint.PendingAdoptions)
	checkpoint.ArmedAt = time.Now().UTC()
	return checkpoint
}

// validRollForwardCheckpoint requires a rolling_forward journal's planned
// checkpoint to be the predecessor's lineage over the next topology, with the
// recorded successor term as its newest adoption intent.
func validRollForwardCheckpoint(guard runEvacuationCheckpoint) bool {
	checkpoint := guard.RollForwardCheckpoint
	if checkpoint == nil || checkpoint.Version != runContinuityCheckpointVersion || checkpoint.ExecutionGeneration != guard.Generation ||
		checkpoint.ExecutionLeaseOwnerID != guard.OwnerID || !reflect.DeepEqual(checkpoint.RunSupervisionToken, guard.Prior.RunSupervisionToken) ||
		!reflect.DeepEqual(checkpoint.Shards, guard.NextShards) || !slices.Contains(checkpoint.ArchiveTargets, guard.Source) || len(checkpoint.PendingAdoptions) == 0 {
		return false
	}
	last := checkpoint.PendingAdoptions[len(checkpoint.PendingAdoptions)-1]
	return last.Generation == guard.RollForwardGeneration && last.OwnerID == guard.RollForwardOwnerID
}

// persistRunEvacuationRollForwardIntent records the successor's planned
// adoption of the next topology inside the journal before any generator sees
// the rotated token. Prior is unchanged, and the journal keeps version 5, so an
// older peer still refuses it.
func (tr *TestRunner) persistRunEvacuationRollForwardIntent(test *ManagedTest, raw string, guard runEvacuationCheckpoint, planned runContinuityCheckpoint, generation uint64, owner string) (runEvacuationCheckpoint, string, error) {
	next := guard
	next.Phase = runEvacuationPhaseRollingForward
	next.RollForwardCheckpoint = &planned
	next.RollForwardGeneration = generation
	next.RollForwardOwnerID = owner
	next.RollForwardStartedAt = time.Now().UTC()
	encoded, err := json.Marshal(next)
	if err != nil {
		return runEvacuationCheckpoint{}, "", err
	}
	if err := tr.swapRunContinuityCheckpoint(test, raw, string(encoded), owner); err != nil {
		return runEvacuationCheckpoint{}, "", err
	}
	return next, string(encoded), nil
}

// completeRunEvacuationRollForward runs after the batched epoch claim adopted
// the survivors and the replacement. It requires every replacement adopted or
// naturally completed and no other completion the prior lineage did not
// record, applies the journaled duration to each adopted replacement, commits
// and transfers the run's capacity, releases a naturally completed
// replacement's pending reservation, and only then replaces the journal with
// the plain adoption checkpoint, which records the natural completions. Any
// problem leaves the journal for failRunEvacuationRollForward.
func (tr *TestRunner) completeRunEvacuationRollForward(ctx context.Context, b *distributedExecutionBackend, candidate *runAdoptionCandidate, adopted, completed map[string]generatorClient, completions map[string]generatorrpc.RunAdoptionResult, capacityClients map[string]generatorClient, generation uint64, owner string) []string {
	rollForward := candidate.rollForward
	guard := rollForward.guard
	// A stop ends in a completed run on the generator, of a still-staged
	// replacement as of a survivor, so only the prior lineage's completions
	// and completions whose duration ran out are trusted. Anything else may
	// be load this run lost.
	natural := tr.runEvacuationNaturalCompletions(candidate.test, guard, rollForward.replacements, completions)
	problems := unexpectedRunEvacuationCompletions(guard, rollForward.replacements, completed, completions, natural, tr.runEvacuationDuration(candidate.test))
	for _, target := range rollForward.replacements {
		// A completion outside the prior lineage is natural or already
		// reported above.
		if adopted[target] == nil && (completed[target] == nil || slices.Contains(guard.Prior.CompletedTargets, target)) {
			problems = append(problems, fmt.Sprintf("replacement %s was not adopted, so its shard never committed", target))
		}
	}
	if len(problems) > 0 {
		return problems
	}
	// The predecessor shortened the replacement right after CommitWork. The
	// target is absolute, so a repeat is a no-op, and an elapsed one ends the
	// replacement's engines at once. A replacement whose journaled duration
	// already ran out needs none.
	for _, target := range rollForward.replacements {
		client := adopted[target]
		if client == nil && natural[target] {
			continue
		}
		if client == nil {
			// Already a problem above; never send it a duration change.
			problems = append(problems, fmt.Sprintf("replacement %s has no adopted generator to shorten", target))
			continue
		}
		callCtx, cancel := context.WithTimeout(ctx, b.timeout)
		result, err := client.AdjustDuration(callCtx, generatorrpc.DurationAdjustment{
			TestID:                candidate.test.ID,
			ExecutionGeneration:   generation,
			TargetDuration:        guard.CommitTargetDuration,
			TargetDurationSeconds: int64(guard.CommitTargetDuration / time.Second),
			RunSupervisionToken:   candidate.newToken[:],
		})
		cancel()
		if err == nil && !result.Accepted {
			err = errors.New("generator rejected the duration adjustment")
		}
		if err != nil {
			problems = append(problems, fmt.Sprintf("replacement %s was not shortened to its journaled duration: %v", target, err))
		}
	}
	if len(problems) > 0 {
		return problems
	}
	if err := tr.commitRunEvacuationRollForwardCapacity(ctx, guard, rollForward.replacements, adopted, completed, natural, generation, owner); err != nil {
		return []string{"evacuation capacity commit: " + err.Error()}
	}
	if err := tr.adoptPodCapacity(ctx, candidate.test.ID, capacityClients, generation, owner); err != nil {
		return []string{"capacity ownership transfer: " + err.Error()}
	}
	// The plain checkpoint records each participant whose duration ran out, so
	// a later successor need not ask its generator, which may have pruned the
	// outcome, again.
	candidate.checkpoint = withRunEvacuationNaturalCompletions(candidate.checkpoint, natural)
	encoded, err := json.Marshal(candidate.checkpoint)
	if err == nil {
		err = tr.swapRunContinuityCheckpoint(candidate.test, rollForward.raw, string(encoded), owner)
	}
	if err != nil {
		return []string{"record evacuation roll-forward: " + err.Error()}
	}
	return nil
}

// commitRunEvacuationRollForwardCapacity brings each journaled replacement
// reservation to committed, as the predecessor would have after CommitWork. A
// pending row is first confirmed by the adopted replacement at this epoch. A
// row already committed, by the predecessor or by an earlier successor term,
// is left for adoptPodCapacity to transfer. A naturally completed replacement
// needs no capacity: its pending row is released through its retained client,
// a row an earlier term released is gone, and a committed row is left, as for
// any completed participant. Every other replacement was adopted.
func (tr *TestRunner) commitRunEvacuationRollForwardCapacity(ctx context.Context, guard runEvacuationCheckpoint, replacements []string, adopted, completed map[string]generatorClient, natural map[string]bool, generation uint64, owner string) error {
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		if len(guard.CapacityClaims) > 0 {
			return errors.New("capacity ledger unavailable")
		}
		return nil
	}
	rows, err := store.ListPodCapacityReservations()
	if err != nil {
		return fmt.Errorf("list replacement reservations: %w", err)
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
	for _, target := range replacements {
		row, present := current[target]
		claim, journaled := claims[target]
		switch {
		case !journaled && present:
			return fmt.Errorf("%s holds a reservation the evacuation journal did not record", target)
		case !journaled:
			continue
		case !present && natural[target] && adopted[target] == nil:
			continue
		case !present:
			return fmt.Errorf("%s lost its replacement reservation", target)
		}
		journal := row
		journal.State, journal.Generation = claim.State, claim.Generation
		if !reflect.DeepEqual(journal, claim) {
			return fmt.Errorf("%s: replacement reservation changed since the evacuation journal", target)
		}
		switch {
		case row.State == "committed" && row.Generation >= guard.Generation && row.Generation < generation:
			continue
		case natural[target] && adopted[target] == nil && (row.State == "pending" || row.State == "releasing") && row.Generation == guard.Generation:
			capacity, ok := completed[target].(podCapacityClient)
			if !ok {
				return fmt.Errorf("%s cannot confirm capacity changes", target)
			}
			if err := tr.releaseRunEvacuationReplacementClaim(ctx, store, capacity, row, claim, generation, owner); err != nil {
				return fmt.Errorf("%s: release the completed replacement's reservation: %w", target, err)
			}
			continue
		case row.State == "pending" && row.Generation == guard.Generation:
		default:
			return fmt.Errorf("%s: replacement reservation is %s under generation %d", target, row.State, row.Generation)
		}
		capacity, ok := adopted[target].(podCapacityClient)
		if !ok {
			return fmt.Errorf("%s cannot confirm capacity changes", target)
		}
		callCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		request := podClaim(row, false)
		request.ExecutionGeneration = generation
		ack, err := capacity.ReservePodCapacity(callCtx, request)
		cancel()
		if err != nil || !podCapacityAckConfirms(ack, row.Incarnation) {
			return fmt.Errorf("%s: generator did not confirm the replacement reservation: %v %s", target, err, ack.GetErrorMessage())
		}
		if err := tr.transitionPodLedger(store, row.Target, row.TestID, row.Incarnation, row.ReservationID, "committed", executionLeaseName, owner, generation); err != nil {
			return fmt.Errorf("%s: commit the replacement reservation: %w", target, err)
		}
	}
	return nil
}

// failRunEvacuationRollForward holds the run STOPPING under its
// rolling_forward journal, then force-stops the survivors and replacements this
// term adopted, exactly as a failed rollback does.
func (tr *TestRunner) failRunEvacuationRollForward(b *distributedExecutionBackend, candidate *runAdoptionCandidate, adopted map[string]generatorClient, clients []map[string]generatorClient, generation uint64, owner string, problems []string) {
	tr.failRunEvacuationRecovery(b, candidate, adopted, clients, generation, owner, "roll forward", problems)
}

// WithEvacuationRollForwardRecovery enables version 5 evacuation journals and
// successor roll-forward of an evacuation interrupted after its source fence,
// once its replacement may have received CommitWork. It takes effect only with
// WithEvacuationRollbackRecovery and run adoption. Enable it only once every
// controller replica runs a release that reads version 5 journals.
func WithEvacuationRollForwardRecovery(enabled bool) TestRunnerOption {
	return func(tr *TestRunner) {
		tr.evacuationRollForwardRecovery = enabled
	}
}
