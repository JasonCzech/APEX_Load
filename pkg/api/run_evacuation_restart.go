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

	pb "github.com/jasonczech/apexload/api/proto"
	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
)

// A missing-replacement restart (REC-A) starts the moved share of an
// interrupted evacuation again on a new replacement when nothing runs it any
// more: the source was fenced and the old replacement never committed, or a
// successor's epoch claim proved it holds no live assignment for the run.
//
// Phase A (adoptOrphanedRuns) proves both fences before any side effect. It
// records a replacement_restarting intent (version 6), adopts the survivors,
// claims the old replacement, makes the moved share's reservation the run's
// under this term and installs the survivors under the kept journal. Phase B
// (resumeAdoptedRun) then restarts the share through the ordinary
// generator-loss reshard (evacuateGeneratorFor), whose own journal replaces the
// intent before any replacement is staged. Every refusal before that journal
// holds the run STOPPING under its journal, as a failed roll-forward does.

// runEvacuationRestartAction names a failed restart in its held reason.
const runEvacuationRestartAction = "restart the missing replacement of"

// errNotRunEvacuationRestart means a journal is not one a restart can start
// from, so it keeps its other recovery or the fail-closed path.
var errNotRunEvacuationRestart = errors.New("evacuation journal is not a missing-replacement restart")

// runEvacuationRestart is a successor's plan to restart the share missing from
// target, the interrupted evacuation's replacement.
type runEvacuationRestart struct {
	guard   runEvacuationCheckpoint
	raw     string
	missing string
	// target is the logical duration the moved share may run to at most.
	target time.Duration
}

// runEvacuationRestartMode is what the generator-loss reshard needs to restart
// a missing share: the share's logical end, and the run's retired targets,
// which keep their archives and are never given the share again.
type runEvacuationRestartMode struct {
	target   time.Duration
	excluded []string
}

// remaining bounds the active duration a restarted share is given so that it
// never runs past the journaled logical end. A nil mode changes nothing.
func (m *runEvacuationRestartMode) remaining(remaining, steadyElapsed time.Duration) time.Duration {
	if m == nil {
		return remaining
	}
	return min(remaining, max(m.target-steadyElapsed, 0))
}

// adoptedRunRestart is a Phase A restart plan that Phase B carries out.
type adoptedRunRestart struct {
	missing  string
	target   time.Duration
	excluded []string
}

// runEvacuationRestartShape reports a journal whose replacement never ran its
// share or whose restart was interrupted before a new replacement committed:
// a version 5 or 6 journal held at source_fenced, a restart intent, or a
// restart's own prepared or staged journal.
func runEvacuationRestartShape(guard runEvacuationCheckpoint) bool {
	switch {
	case !rollForwardRunEvacuationCheckpointVersion(guard.Version):
		return false
	case guard.Phase == runEvacuationPhaseSourceFenced:
		return true
	case guard.Version != runEvacuationRestartCheckpointVersion:
		return false
	case guard.Phase == runEvacuationPhaseReplacementRestarting:
		return true
	}
	return !guard.SourceAbsentAt.IsZero() && (guard.Phase == runEvacuationPhasePrepared || guard.Phase == runEvacuationPhaseStaged)
}

// evacuationRestartGuard returns the restart plan of a stored run. It returns
// errNotRunEvacuationRestart for a journal of any other shape, and otherwise
// the reason the restart is refused. The caller holds tr.mu or owns test.
func (tr *TestRunner) evacuationRestartGuard(test *ManagedTest, raw string) (runEvacuationRestart, error) {
	if tr == nil || test == nil {
		return runEvacuationRestart{}, errNotRunEvacuationRestart
	}
	guard, _, err := interruptedEvacuationTargets(raw, test.ID)
	if err != nil || !runEvacuationRestartShape(guard) {
		return runEvacuationRestart{}, errNotRunEvacuationRestart
	}
	if !tr.evacuationReplacementRestart || !tr.evacuationRollbackRecovery || !tr.evacuationRollForwardRecovery || !tr.runAdoptionEnabled() {
		return runEvacuationRestart{}, errors.New("the missing-replacement restart is not enabled")
	}
	if test.Status != "RUNNING" && test.Status != "PAUSED" {
		return runEvacuationRestart{}, fmt.Errorf("the run is %s", test.Status)
	}
	if effectiveFailoverPolicy(test.Assignment) != failoverPolicyContinue {
		return runEvacuationRestart{}, errors.New("the run does not continue on controller failover")
	}
	if _, distributed := tr.execution.(*distributedExecutionBackend); !distributed {
		return runEvacuationRestart{}, errors.New("the run is not distributed")
	}
	guard, missing, target, err := runEvacuationRestartPlan(raw, test.ID)
	if err != nil {
		return runEvacuationRestart{}, err
	}
	if err := runEvacuationRestartRunEligible(test, guard); err != nil {
		return runEvacuationRestart{}, err
	}
	return runEvacuationRestart{guard: guard, raw: raw, missing: missing, target: target}, nil
}

// runEvacuationRestartRunEligible applies the run-level refusals: the moved
// groups' workload, and a run clock the remaining duration can be taken from.
func runEvacuationRestartRunEligible(test *ManagedTest, guard runEvacuationCheckpoint) error {
	if err := runEvacuationRollForwardWorkloadEligible(test, guard); err != nil {
		return fmt.Errorf("the moved workload cannot be restarted: %w", err)
	}
	if test.StartedAt == nil || test.StartedAt.IsZero() || test.Duration <= 0 {
		return errors.New("the remaining active duration cannot be proven: the run records no start or duration")
	}
	return nil
}

// runEvacuationRestartPlan validates a restart journal strictly, as the
// roll-forward plan does. It returns the replacement whose share is missing
// and the logical duration that share may run to at most.
func runEvacuationRestartPlan(raw, testID string) (runEvacuationCheckpoint, string, time.Duration, error) {
	guard, _, err := interruptedEvacuationTargets(raw, testID)
	if err != nil {
		return runEvacuationCheckpoint{}, "", 0, err
	}
	if !runEvacuationRestartShape(guard) {
		return runEvacuationCheckpoint{}, "", 0, errNotRunEvacuationRestart
	}
	if !guard.AbandonedAt.IsZero() {
		return runEvacuationCheckpoint{}, "", 0, errors.New("evacuation was abandoned by its leader")
	}
	if guard.Prior.ArmedAt.IsZero() {
		return runEvacuationCheckpoint{}, "", 0, errors.New("evacuation predecessor lineage was never armed")
	}
	if slices.Contains(guard.Prior.CompletedTargets, guard.Source) {
		return runEvacuationCheckpoint{}, "", 0, errors.New("evacuation source already completed in the prior topology")
	}
	replacements, err := runEvacuationReplacementTargets(guard)
	if err != nil {
		return runEvacuationCheckpoint{}, "", 0, err
	}
	if len(replacements) != 1 {
		return runEvacuationCheckpoint{}, "", 0, fmt.Errorf("a restart moves one replacement's share, and this evacuation has %d", len(replacements))
	}
	// A replacement the prior lineage records completed is never claimed, so
	// its completion would prove nothing about the moved share.
	if slices.Contains(guard.Prior.CompletedTargets, replacements[0]) {
		return runEvacuationCheckpoint{}, "", 0, errors.New("the evacuation's replacement is recorded completed in the prior topology")
	}
	if err := runEvacuationSurvivorsUnchanged(guard, replacements); err != nil {
		return runEvacuationCheckpoint{}, "", 0, err
	}
	if err := validateRunEvacuationCapacityJournal(guard, replacements); err != nil {
		return runEvacuationCheckpoint{}, "", 0, err
	}
	target := runEvacuationRestartTarget(guard)
	if target <= 0 {
		return runEvacuationCheckpoint{}, "", 0, errors.New("the remaining active duration cannot be proven: the journal records no duration for the moved share")
	}
	return guard, replacements[0], target, nil
}

// runEvacuationRestartTarget is the logical duration, in DurationAdjustment
// terms, the moved share may run to at most: the shortest of the duration a
// restart journal carries, the commit target and the staged duration. Zero
// means none is recorded.
func runEvacuationRestartTarget(guard runEvacuationCheckpoint) time.Duration {
	journaled := time.Duration(0)
	switch {
	case guard.CommitTargetDuration > 0:
		journaled = guard.CommitTargetDuration
	case guard.StagedRemaining > 0 && guard.StagedSteadyElapsed >= 0:
		journaled = guard.StagedSteadyElapsed + guard.StagedRemaining
	}
	switch {
	case guard.RestartTargetDuration <= 0:
		return journaled
	case journaled <= 0:
		return guard.RestartTargetDuration
	}
	return min(guard.RestartTargetDuration, journaled)
}

// runEvacuationRestartCheckpoint is the adoption checkpoint a restart installs:
// the next topology, with the missing share still on its old replacement,
// under the predecessor's lineage. A restart intent carries it. The old
// replacement keeps an archive only if CommitWork may have reached it.
func runEvacuationRestartCheckpoint(guard runEvacuationCheckpoint, missing string) runContinuityCheckpoint {
	if guard.RestartCheckpoint != nil {
		checkpoint := *guard.RestartCheckpoint
		checkpoint.Shards = cloneGroupShardMap(checkpoint.Shards)
		checkpoint.CompletedTargets = slices.Clone(checkpoint.CompletedTargets)
		checkpoint.ArchiveTargets = slices.Clone(checkpoint.ArchiveTargets)
		checkpoint.PendingAdoptions = slices.Clone(checkpoint.PendingAdoptions)
		return checkpoint
	}
	checkpoint := runEvacuationRollForwardCheckpoint(guard)
	checkpoint.ArchiveTargets = slices.Clone(checkpoint.ArchiveTargets)
	if !guard.CommittingAt.IsZero() && !slices.Contains(checkpoint.ArchiveTargets, missing) {
		checkpoint.ArchiveTargets = append(checkpoint.ArchiveTargets, missing)
		slices.Sort(checkpoint.ArchiveTargets)
	}
	return checkpoint
}

// validRestartCheckpoint requires a restart intent's planned checkpoint to be
// the predecessor's lineage over the next topology, with the recorded
// successor term as its newest adoption intent.
func validRestartCheckpoint(guard runEvacuationCheckpoint) bool {
	checkpoint := guard.RestartCheckpoint
	if checkpoint == nil || checkpoint.Version != runContinuityCheckpointVersion || checkpoint.ExecutionGeneration != guard.Generation ||
		checkpoint.ExecutionLeaseOwnerID != guard.OwnerID || !reflect.DeepEqual(checkpoint.RunSupervisionToken, guard.Prior.RunSupervisionToken) ||
		!reflect.DeepEqual(checkpoint.Shards, guard.NextShards) || !slices.Equal(checkpoint.ArchiveTargets, runEvacuationArchiveTargets(guard, checkpoint.ArchiveTargets)) ||
		len(checkpoint.PendingAdoptions) == 0 {
		return false
	}
	last := checkpoint.PendingAdoptions[len(checkpoint.PendingAdoptions)-1]
	return last.Generation == guard.RestartGeneration && last.OwnerID == guard.RestartOwnerID
}

// persistRunEvacuationRestartIntent records the successor's planned adoption
// as a version 6 replacement_restarting journal before any generator sees the
// rotated token, or, after a roll-forward found its replacement gone, in place
// of that term's rolling_forward intent. Prior is unchanged.
func (tr *TestRunner) persistRunEvacuationRestartIntent(test *ManagedTest, raw string, guard runEvacuationCheckpoint, planned runContinuityCheckpoint, generation uint64, owner string, target time.Duration) (runEvacuationCheckpoint, string, error) {
	next := guard
	next.Version = runEvacuationRestartCheckpointVersion
	next.Phase = runEvacuationPhaseReplacementRestarting
	next.RestartCheckpoint = &planned
	next.RestartGeneration = generation
	next.RestartOwnerID = owner
	next.RestartStartedAt = time.Now().UTC()
	next.RestartTargetDuration = target
	next.RestartExcluded = runEvacuationRestartExcluded(guard, planned)
	next.RollForwardGeneration, next.RollForwardOwnerID, next.RollForwardStartedAt, next.RollForwardCheckpoint = 0, "", time.Time{}, nil
	encoded, err := json.Marshal(next)
	if err != nil {
		return runEvacuationCheckpoint{}, "", err
	}
	if err := tr.swapRunContinuityCheckpoint(test, raw, string(encoded), owner); err != nil {
		return runEvacuationCheckpoint{}, "", err
	}
	return next, string(encoded), nil
}

// runEvacuationRestartAbsence says why an old replacement's answer to an
// accepted epoch claim does not prove it runs nothing of the run, or returns
// "" when it does. The claim drains every lower-generation assignment it does
// not adopt, staged work included, before it is acknowledged; a run still
// stopping may be draining, so it proves nothing until its outcome is terminal.
// A generator also answers REJECTED when the run already belongs to the
// claimed generation under another token. Only this term assigns at its own
// generation, and it never gives the share to the old replacement, so that
// answer cannot occur here; the answer carries no generation to tell it apart.
func runEvacuationRestartAbsence(outcome generatorrpc.RunAdoptionResult) string {
	switch outcome.Outcome {
	case pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST, pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED:
		return ""
	case pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED:
		if isTerminalRemoteLifecycle(outcome.TerminalState) {
			return ""
		}
		return "its run is still stopping"
	case pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED:
		return "it still runs its assignment"
	}
	return fmt.Sprintf("it answered %s", outcome.Outcome)
}

// runEvacuationRestartFallback turns a roll-forward whose replacement was not
// adopted into a restart when the setting allows it and the claim proved the
// replacement no longer runs the share. It returns nil, with the reason, when
// the roll-forward must fail as before.
func (tr *TestRunner) runEvacuationRestartFallback(candidate *runAdoptionCandidate, results map[string]runAdoptionTargetResult, adopted, completed map[string]generatorClient, completions, refused map[string]generatorrpc.RunAdoptionResult) (*runEvacuationRestart, string) {
	rollForward := candidate.rollForward
	if rollForward == nil || !tr.evacuationReplacementRestart || len(rollForward.replacements) != 1 {
		return nil, ""
	}
	target := rollForward.replacements[0]
	if adopted[target] != nil {
		return nil, ""
	}
	if completed[target] != nil {
		natural := tr.runEvacuationNaturalCompletions(candidate.test, rollForward.guard, rollForward.replacements, completions)
		if natural[target] || slices.Contains(rollForward.guard.Prior.CompletedTargets, target) {
			return nil, ""
		}
	}
	result, present := results[target]
	switch {
	case !present || result.err != nil || result.client == nil:
		return nil, fmt.Sprintf("the replacement %s could not be restarted: it is unreachable, which does not prove it stopped", target)
	case completed[target] == nil:
		outcome, reported := refused[target]
		if !reported {
			return nil, fmt.Sprintf("the replacement %s could not be restarted: its generator did not report the run", target)
		}
		if why := runEvacuationRestartAbsence(outcome); why != "" {
			return nil, fmt.Sprintf("the replacement %s could not be restarted: %s", target, why)
		}
	}
	guard := rollForward.guard
	restartTarget := runEvacuationRestartTarget(guard)
	if restartTarget <= 0 {
		return nil, "the replacement could not be restarted: the remaining active duration cannot be proven"
	}
	tr.mu.RLock()
	err := runEvacuationRestartRunEligible(candidate.test, guard)
	tr.mu.RUnlock()
	if err != nil {
		return nil, "the replacement could not be restarted: " + err.Error()
	}
	return &runEvacuationRestart{guard: guard, raw: rollForward.raw, missing: target, target: restartTarget}, ""
}

// prepareRunEvacuationRestart runs after the batched epoch claim adopted the
// survivors. It requires the old replacement to have answered that claim
// without a live assignment for the run, and a restart's absent source to have
// accepted it, then makes the moved share's journaled reservation the run's
// under this term. The intent stays in place; any problem leaves it for
// failRunEvacuationRecovery.
func (tr *TestRunner) prepareRunEvacuationRestart(ctx context.Context, candidate *runAdoptionCandidate, results map[string]runAdoptionTargetResult, reachable, adopted, completed map[string]generatorClient, completions, refused map[string]generatorrpc.RunAdoptionResult, capacityClients map[string]generatorClient, generation uint64, owner string) []string {
	restart := candidate.restart
	guard, missing := restart.guard, restart.missing
	// A stopped survivor ends in a completed run as a still-running one would
	// not; only the prior lineage's and natural completions are trusted. The
	// old replacement's completion only proves it runs nothing.
	survivorsCompleted := make(map[string]generatorClient, len(completed))
	for target, client := range completed {
		if target != missing {
			survivorsCompleted[target] = client
		}
	}
	natural := tr.runEvacuationNaturalCompletions(candidate.test, guard, []string{missing}, completions)
	delete(natural, missing)
	problems := unexpectedRunEvacuationCompletions(guard, []string{missing}, survivorsCompleted, completions, natural, tr.runEvacuationDuration(candidate.test))
	result, present := results[missing]
	switch {
	case !present || result.err != nil || reachable[missing] == nil:
		problems = append(problems, fmt.Sprintf("old replacement %s is unreachable, which does not prove it stopped: %v", missing, result.err))
	case adopted[missing] != nil:
		problems = append(problems, fmt.Sprintf("old replacement %s still runs its assignment, so its share cannot be restarted", missing))
	case completed[missing] != nil:
	default:
		outcome, reported := refused[missing]
		if !reported {
			problems = append(problems, fmt.Sprintf("old replacement %s did not report the run", missing))
		} else if why := runEvacuationRestartAbsence(outcome); why != "" {
			problems = append(problems, fmt.Sprintf("old replacement %s does not prove it stopped: %s", missing, why))
		}
	}
	if !guard.SourceAbsentAt.IsZero() && guard.SourceFencedAt.IsZero() {
		// A restart's own source was proven absent by an earlier term; this
		// term's claim must reach it too before the share moves on.
		if source := results[guard.Source]; source.err != nil || source.client == nil {
			problems = append(problems, fmt.Sprintf("restart source %s could not be fenced: %v", guard.Source, source.err))
		}
	}
	if len(problems) > 0 {
		return problems
	}
	// The moved share's reservation becomes the run's under this term, as a
	// roll-forward commits its replacement's; the restart then moves it.
	shareClient := map[string]generatorClient{missing: reachable[missing]}
	if err := tr.commitRunEvacuationRollForwardCapacity(ctx, guard, []string{missing}, shareClient, nil, nil, generation, owner); err != nil {
		return []string{"moved share capacity: " + err.Error()}
	}
	clients := make(map[string]generatorClient, len(capacityClients)+1)
	for target, client := range capacityClients {
		clients[target] = client
	}
	clients[missing] = reachable[missing]
	if err := tr.adoptPodCapacity(ctx, candidate.test.ID, clients, generation, owner); err != nil {
		return []string{"capacity ownership transfer: " + err.Error()}
	}
	return nil
}

// beginRunEvacuationRestartCheckpoint replaces this term's restart intent with
// the restart's own version 6 journal, before any replacement is staged. Its
// prior is this runtime's checkpoint under generation over the next topology
// of the interrupted evacuation, and its source, the old replacement, is
// recorded absent rather than fenced.
func (tr *TestRunner) beginRunEvacuationRestartCheckpoint(ctx context.Context, test *ManagedTest, expected string, generation uint64, owner, source string, token []byte, previous, next map[string][]GeneratorShardSummary, capacity runEvacuationCapacity, restart runEvacuationRestartMode) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, runContinuityArmTimeout)
	defer cancel()
	var intent runEvacuationCheckpoint
	if json.Unmarshal([]byte(expected), &intent) != nil || intent.Version != runEvacuationRestartCheckpointVersion || intent.Phase != runEvacuationPhaseReplacementRestarting ||
		intent.TestID != test.ID || intent.RestartGeneration != generation || intent.RestartOwnerID != owner || intent.RestartCheckpoint == nil {
		return "", errors.New("evacuation restart lacks this term's restart intent")
	}
	missing, err := runEvacuationReplacementTargets(intent)
	if err != nil || len(missing) != 1 || missing[0] != source || !reflect.DeepEqual(intent.NextShards, previous) {
		return "", errors.New("evacuation restart does not match its restart intent")
	}
	if restart.target <= 0 || restart.target > intent.RestartTargetDuration {
		return "", errors.New("evacuation restart would run the moved share past its journaled duration")
	}
	if generation == 0 || owner == "" || len(next) == 0 {
		return "", errors.New("invalid evacuation restart identity or topology")
	}
	tr.mu.RLock()
	failovers := append([]RunFailover(nil), test.continuityFailovers...)
	completedTargets, archiveTargets := runtimeRetiredTargetsLocked(test)
	pinnedSecrets := test.pinnedSecrets.snapshot()
	tr.mu.RUnlock()
	prior, err := tr.sealRunContinuityCheckpoint(ctx, test.ID, generation, owner, token, previous, failovers, completedTargets, archiveTargets, pinnedSecrets, *intent.RestartCheckpoint)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC()
	guard := runEvacuationCheckpoint{
		Version:               runEvacuationRestartCheckpointVersion,
		TestID:                test.ID,
		Source:                source,
		Generation:            generation,
		OwnerID:               owner,
		PreparedAt:            now,
		Phase:                 runEvacuationPhasePrepared,
		Prior:                 prior,
		BeforeShards:          cloneGroupShardMap(previous),
		NextShards:            cloneGroupShardMap(next),
		CapacityClaims:        clonePodCapacityReservations(capacity.claims),
		StandbyPreImages:      clonePodCapacityReservations(capacity.preImages),
		RestartTargetDuration: restart.target,
		RestartExcluded:       slices.Clone(intent.RestartExcluded),
		SourceAbsentAt:        now,
	}
	encoded, err := json.Marshal(guard)
	if err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := tr.swapRunContinuityCheckpoint(test, expected, string(encoded), owner); err != nil {
		return "", err
	}
	return string(encoded), nil
}

// restartEvacuationReplacement is Phase B of a restart. It secures any
// survivors, then restarts the missing share through the generator-loss
// reshard. On success the new replacement joins the adopted generators, whose
// metrics streams Phase B opens next, and it returns true. Any refusal before
// the restart's journal is replaced holds the run STOPPING, as a failed
// roll-forward does; an uncertain restart is left to the reshard's own
// fail-closed path. A stop that wins the race leaves the run to that stop.
func (tr *TestRunner) restartEvacuationReplacement(b *distributedExecutionBackend, test *ManagedTest, resume *adoptedRunResume, executionCtx context.Context, token [generatorrpc.RunSupervisionTokenSize]byte) bool {
	restart := resume.restart
	fail := func(problem string) bool {
		tr.failRunEvacuationRestart(b, test, resume, token, problem)
		return false
	}
	if resume.secureFailure != "" {
		return fail(resume.secureFailure)
	}
	if !resume.secured && len(resume.adopted) > 0 {
		if message := tr.secureAdoptedRunSupervision(b, test, resume, executionCtx, token); message != "" {
			return fail(message)
		}
		resume.secured = true
	}
	// With no survivor, the share's new replacement is secured with the rest
	// of Phase B once it runs: from then on the run has no journal to hold.
	tr.mu.RLock()
	before := cloneClientMap(test.remoteClientsByTarget)
	tr.mu.RUnlock()
	impacts, err := b.evacuateGeneratorFor(tr, test, restart.missing, false, false, &runEvacuationRestartMode{target: restart.target, excluded: restart.excluded})
	tr.mu.Lock()
	status, reason := test.Status, test.Error
	current := tr.tests[test.ID] == test
	journaled := current && interruptedRunEvacuation(test.continuityCheckpoint)
	restarted := 0
	for target, client := range test.remoteClientsByTarget {
		if before[target] == nil && client != nil {
			// Whatever took the share is adopted from here on: Phase B streams
			// its metrics, and every later stop or failure reaches it.
			resume.adopted[target] = client
			resume.restarted = append(resume.restarted, target)
			restarted++
		}
	}
	slices.Sort(resume.restarted)
	tr.mu.Unlock()
	switch {
	case !current:
		return false
	case status == "STOPPING" && strings.HasPrefix(reason, uncertainEvacuationReasonPrefix):
		// The reshard's fail-closed path holds the run and stops what it touched.
		slog.Warn("restart of a missing evacuation replacement failed closed", "test_id", test.ID, "error", err)
		return false
	case status != "RUNNING" && status != "PAUSED":
		// A stop requested meanwhile owns the run, and Phase B opens the
		// streams of every adopted generator for its finalizer. Without one
		// there is nothing to resume.
		return len(resume.adopted) > 0
	case err == nil && restarted == 1:
	case journaled:
		// No replacement committed: the run is held under its journal.
		if err == nil {
			err = errors.New("no replacement took the share")
		}
		return fail("restart the missing share: " + err.Error())
	default:
		// The restart's journal is already replaced, so nothing can hold the
		// run under it: every adopted generator is stopped with proof instead,
		// as for any failure once a run resumed.
		message := fmt.Sprintf("restart the missing share: %d new replacements took it", restarted)
		if err != nil {
			message = "restart the missing share: " + err.Error()
		}
		tr.failResumedAdoptedRunWithStopProof(b, test, resume, message)
		return false
	}
	vusers, groups := 0, make([]string, 0, len(impacts))
	for _, impact := range impacts {
		vusers += impact.VUsers
		groups = append(groups, impact.Group)
	}
	slices.Sort(groups)
	slog.Info("restarted a missing evacuation replacement after controller failover", "test_id", test.ID, "from", restart.missing,
		"execution_generation", resume.generation, "vusers", vusers, "groups", strings.Join(groups, ","))
	return true
}

// recordRunEvacuationRestartLocked adds the share a restart moved to the run's
// latest failover, before the reshard writes the run's checkpoint. The caller
// holds tr.mu.
func recordRunEvacuationRestartLocked(test *ManagedTest, impacts []GeneratorEvacuationImpact) {
	vusers, groups := 0, make([]string, 0, len(impacts))
	for _, impact := range impacts {
		if !impact.Refused {
			vusers += impact.VUsers
			groups = append(groups, impact.Group)
		}
	}
	test.continuityFailovers = withRestartedShare(test.continuityFailovers, vusers, groups)
}

// withRestartedShare returns failovers with a restarted share added to the
// latest one.
func withRestartedShare(failovers []RunFailover, vusers int, groups []string) []RunFailover {
	count := len(failovers)
	if count == 0 || vusers <= 0 {
		return failovers
	}
	failovers = slices.Clone(failovers)
	last := failovers[count-1]
	last.RestartedVUsers += vusers
	last.RestartedGroups = slices.Clone(last.RestartedGroups)
	for _, group := range groups {
		if !slices.Contains(last.RestartedGroups, group) {
			last.RestartedGroups = append(last.RestartedGroups, group)
		}
	}
	slices.Sort(last.RestartedGroups)
	failovers[count-1] = last
	return failovers
}

// withRunEvacuationRestartRecorded returns the checkpoint a successor installs
// from a restart's own journal with the restarted share recorded in its
// lineage's latest failover: that journal's prior was sealed before the
// share moved.
func withRunEvacuationRestartRecorded(checkpoint runContinuityCheckpoint, guard runEvacuationCheckpoint) runContinuityCheckpoint {
	if guard.SourceAbsentAt.IsZero() {
		return checkpoint
	}
	vusers, groups := 0, make([]string, 0, len(guard.BeforeShards))
	for group, shards := range guard.BeforeShards {
		if moved := shardVUsersForTarget(shards, guard.Source); moved > 0 {
			vusers += moved
			groups = append(groups, group)
		}
	}
	checkpoint.Failovers = withRestartedShare(checkpoint.Failovers, vusers, groups)
	return checkpoint
}

// runEvacuationRestartExcluded is every target a restart must never give the
// share: those the journal already excluded, its archive targets and its
// source. The journal carries it, so a target whose archive client a later
// term could not reach stays excluded.
func runEvacuationRestartExcluded(guard runEvacuationCheckpoint, checkpoint runContinuityCheckpoint) []string {
	excluded := slices.Clone(guard.RestartExcluded)
	for _, target := range append(slices.Clone(checkpoint.ArchiveTargets), guard.Source) {
		if !slices.Contains(excluded, target) {
			excluded = append(excluded, target)
		}
	}
	slices.Sort(excluded)
	return excluded
}

// failRunEvacuationRestart holds a run whose restart could not begin under
// its restart intent or its restart's interrupted journal, then cancels the
// runtime Phase A installed and force-stops every adopted generator, as a
// failed roll-forward does.
func (tr *TestRunner) failRunEvacuationRestart(b *distributedExecutionBackend, test *ManagedTest, resume *adoptedRunResume, token [generatorrpc.RunSupervisionTokenSize]byte, problem string) {
	tr.mu.Lock()
	if tr.tests[test.ID] == test && !isTerminalTestStatus(test.Status) {
		// Nothing may finish the run from its streams while it is held.
		test.remoteFinalizing = true
		test.remoteControlGeneration++
	}
	tr.mu.Unlock()
	candidate := &runAdoptionCandidate{test: test, newToken: token}
	tr.failRunEvacuationRecovery(b, candidate, cloneClientMap(resume.adopted), nil, resume.generation, resume.ownerID, runEvacuationRestartAction, []string{problem})
}

// evacuationRestartRefusal says why a stored run held by a restartable journal
// is not restarted, or returns "" for any other journal.
func (tr *TestRunner) evacuationRestartRefusal(test *ManagedTest, raw string) string {
	_, err := tr.evacuationRestartGuard(test, raw)
	if err == nil || errors.Is(err, errNotRunEvacuationRestart) {
		return ""
	}
	return err.Error()
}

// WithEvacuationReplacementRestart lets a successor restart the moved share of
// an interrupted evacuation whose replacement never committed or provably
// stopped, for the eligible stateless-HTTP workload. It takes effect only with
// WithEvacuationRollbackRecovery, WithEvacuationRollForwardRecovery and run
// adoption, and writes version 6 journals; enable it only once every
// controller replica reads them.
func WithEvacuationReplacementRestart(enabled bool) TestRunnerOption {
	return func(tr *TestRunner) {
		tr.evacuationReplacementRestart = enabled
	}
}
