package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/distributed/generatorrpc"
	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
	"github.com/jasonczech/apexload/pkg/storage"
)

const interruptedEvacuationFenceProof = "controller ownership was interrupted during generator evacuation; successor confirmed process-epoch fences for all possible generators"

// interruptedEvacuationAttestedProof stands in for the successor's fences when
// a generator is gone for good: a system administrator attested through the
// interrupted-run override that every possible generator process is stopped.
const interruptedEvacuationAttestedProof = "controller ownership was interrupted during generator evacuation; a system administrator attested that every generator process that could run it is stopped"

// Same-leader intervention retains its own proof wording; historical successor
// proofs remain readable without pretending leadership changed.
const sameLeaderEvacuationAttestedProof = "generator evacuation cleanup was unproven; a system administrator attested that every generator process that could run it is stopped"

func interruptedEvacuationHasDurableFenceProof(test *ManagedTest) bool {
	return test != nil && test.Status == "FAILED" && test.EndedAt != nil &&
		(strings.HasPrefix(test.Error, interruptedEvacuationFenceProof) || strings.HasPrefix(test.Error, interruptedEvacuationAttestedProof) || strings.HasPrefix(test.Error, sameLeaderEvacuationAttestedProof))
}

func targetShardAssignmentChanged(before, next map[string][]GeneratorShardSummary, target string) bool {
	groups := make(map[string]struct{}, len(before)+len(next))
	for group := range before {
		groups[group] = struct{}{}
	}
	for group := range next {
		groups[group] = struct{}{}
	}
	for group := range groups {
		var oldShards, newShards []GeneratorShardSummary
		for _, shard := range before[group] {
			if shard.Target == target {
				oldShards = append(oldShards, shard)
			}
		}
		for _, shard := range next[group] {
			if shard.Target == target {
				newShards = append(newShards, shard)
			}
		}
		if !reflect.DeepEqual(oldShards, newShards) {
			return true
		}
	}
	return false
}

// A prior completion proves the target's old assignment stopped. It ceases
// to be sufficient only when the evacuation assigned new or changed work to
// that target. Removing its old shards cannot resurrect completed work.
func priorCompletedTargetStillTerminal(guard runEvacuationCheckpoint, target string) bool {
	if !slices.Contains(guard.Prior.CompletedTargets, target) {
		return false
	}
	for _, shards := range guard.NextShards {
		for _, shard := range shards {
			if shard.Target == target {
				return !targetShardAssignmentChanged(guard.BeforeShards, guard.NextShards, target)
			}
		}
	}
	return true
}

// interruptedEvacuationTargets is deliberately stricter than the normal guard
// decoder. A damaged journal cannot establish a complete participant set, so
// it stays STOPPING until an administrator attests its generators stopped.
func interruptedEvacuationTargets(raw, testID string) (runEvacuationCheckpoint, []string, error) {
	var guard runEvacuationCheckpoint
	if json.Unmarshal([]byte(raw), &guard) != nil || !interruptedRunEvacuation(raw) || guard.TestID != testID ||
		guard.Generation == 0 || guard.OwnerID == "" || guard.Source == "" || strings.TrimSpace(guard.Source) != guard.Source || guard.PreparedAt.IsZero() ||
		guard.Prior.Version != runContinuityCheckpointVersion || guard.Prior.ExecutionGeneration != guard.Generation ||
		guard.Prior.ExecutionLeaseOwnerID != guard.OwnerID || !reflect.DeepEqual(guard.Prior.Shards, guard.BeforeShards) {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal cannot establish the prior execution topology")
	}
	replacements, err := runEvacuationReplacementTargets(guard)
	if err != nil {
		return runEvacuationCheckpoint{}, nil, err
	}
	switch guard.Phase {
	case runEvacuationPhasePrepared:
		if !guard.StagedAt.IsZero() || len(guard.StagedTargets) != 0 || !guard.SourceFencedAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() {
			return runEvacuationCheckpoint{}, nil, errors.New("prepared evacuation has contradictory progress")
		}
	case runEvacuationPhaseStaged:
		if guard.StagedAt.IsZero() || !slices.Equal(guard.StagedTargets, replacements) || !guard.SourceFencedAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() {
			return runEvacuationCheckpoint{}, nil, errors.New("staged evacuation has contradictory progress")
		}
	case runEvacuationPhaseSourceFenced:
		if guard.StagedAt.IsZero() || !slices.Equal(guard.StagedTargets, replacements) || guard.SourceFencedAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() {
			return runEvacuationCheckpoint{}, nil, errors.New("source-fenced evacuation has contradictory progress")
		}
	case runEvacuationPhaseReplacementCommitted:
		if guard.StagedAt.IsZero() || !slices.Equal(guard.StagedTargets, replacements) || guard.SourceFencedAt.IsZero() || guard.ReplacementCommittedAt.IsZero() ||
			(guard.Version == runEvacuationRollForwardCheckpointVersion && guard.CommittingAt.IsZero()) {
			return runEvacuationCheckpoint{}, nil, errors.New("committed evacuation has contradictory progress")
		}
	case runEvacuationPhaseReplacementCommitting:
		// CommitWork may or may not have reached the replacement. The source
		// was positively fenced first.
		if guard.Version != runEvacuationRollForwardCheckpointVersion || guard.StagedAt.IsZero() || !slices.Equal(guard.StagedTargets, replacements) ||
			guard.SourceFencedAt.IsZero() || guard.CommittingAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() || guard.RollForwardGeneration != 0 || guard.RollForwardCheckpoint != nil {
			return runEvacuationCheckpoint{}, nil, errors.New("committing evacuation has contradictory progress")
		}
	case runEvacuationPhaseRollingForward:
		// A successor recorded its intent to adopt the next topology. The source
		// stays fenced; the survivors and the replacement are participants.
		if guard.Version != runEvacuationRollForwardCheckpointVersion || guard.StagedAt.IsZero() || !slices.Equal(guard.StagedTargets, replacements) ||
			guard.SourceFencedAt.IsZero() || guard.CommittingAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() ||
			guard.RollForwardGeneration <= guard.Generation || guard.RollForwardOwnerID == "" || guard.RollForwardStartedAt.IsZero() || guard.RollbackGeneration != 0 ||
			!validRollForwardCheckpoint(guard) {
			return runEvacuationCheckpoint{}, nil, errors.New("rolling-forward evacuation has contradictory progress")
		}
	case runEvacuationPhaseRollingBack:
		// A successor recorded its rollback intent from prepared or staged. The
		// source was never fenced, so it and the replacement stay participants.
		staged := !guard.StagedAt.IsZero()
		if !rollbackRunEvacuationCheckpointVersion(guard.Version) || guard.RollbackGeneration <= guard.Generation || guard.RollbackOwnerID == "" || guard.RollbackStartedAt.IsZero() ||
			(staged && !slices.Equal(guard.StagedTargets, replacements)) || (!staged && len(guard.StagedTargets) != 0) || !guard.SourceFencedAt.IsZero() || !guard.ReplacementCommittedAt.IsZero() {
			return runEvacuationCheckpoint{}, nil, errors.New("rolling-back evacuation has contradictory progress")
		}
	default:
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal has an unknown phase")
	}
	// CompletedTargets prove terminality for their current assignment. ArchiveTargets
	// only retain archive access to a historically retired generator: that target
	// may have been assigned live work again, so archive membership is not a
	// process fence for a shard in the prior or next topology.
	priorTerminal := make(map[string]struct{}, len(guard.Prior.CompletedTargets))
	for _, target := range guard.Prior.CompletedTargets {
		if target == "" || strings.TrimSpace(target) != target {
			return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal has an invalid prior terminal target")
		}
		priorTerminal[target] = struct{}{}
	}
	for _, target := range guard.Prior.ArchiveTargets {
		if target == "" || strings.TrimSpace(target) != target {
			return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal has an invalid prior archive target")
		}
	}
	// SourceFencedAt is published only after a forced StopTest ACK or a
	// conservative run/process lease-expiry fence. Once durable, the old source
	// need not reappear to prove an already-fenced assignment has stopped.
	sourceAlreadyFenced := guard.Phase == runEvacuationPhaseSourceFenced || guard.Phase == runEvacuationPhaseReplacementCommitted ||
		guard.Phase == runEvacuationPhaseReplacementCommitting || guard.Phase == runEvacuationPhaseRollingForward
	targetSet := make(map[string]struct{})
	if !sourceAlreadyFenced {
		targetSet[guard.Source] = struct{}{}
	}
	for _, shards := range guard.BeforeShards {
		for _, shard := range shards {
			if shard.Target == "" || strings.TrimSpace(shard.Target) != shard.Target {
				return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal has an invalid prior generator target")
			}
			if _, terminal := priorTerminal[shard.Target]; !terminal && (!sourceAlreadyFenced || shard.Target != guard.Source) {
				targetSet[shard.Target] = struct{}{}
			}
		}
	}
	for _, shards := range guard.NextShards {
		for _, shard := range shards {
			if shard.Target == "" || strings.TrimSpace(shard.Target) != shard.Target {
				return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal has an invalid next generator target")
			}
			if _, terminal := priorTerminal[shard.Target]; terminal && shard.Target != guard.Source &&
				!targetShardAssignmentChanged(guard.BeforeShards, guard.NextShards, shard.Target) {
				continue
			}
			targetSet[shard.Target] = struct{}{}
		}
	}
	for _, target := range guard.StagedTargets {
		if target == "" || strings.TrimSpace(target) != target {
			return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal has an invalid staged target")
		}
		targetSet[target] = struct{}{}
	}
	if len(targetSet) > maxRecoveryCapacityPlanPods {
		return runEvacuationCheckpoint{}, nil, errors.New("evacuation journal exceeds the bounded participant set")
	}
	targets := make([]string, 0, len(targetSet))
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	return guard, targets, nil
}

// recoverInterruptedRunEvacuation never treats an expired lease, a lost stream,
// or an unreachable generator as stop proof. An accepted successor process-
// epoch claim synchronously drains every lower-generation assignment before
// acknowledging, including staged replacement work omitted from adoption.
func (tr *TestRunner) recoverInterruptedRunEvacuation(test *ManagedTest) error {
	if tr == nil || test == nil {
		return nil
	}
	b, ok := tr.execution.(*distributedExecutionBackend)
	if !ok {
		return errors.New("distributed generator backend is unavailable for evacuation recovery")
	}
	tr.mu.RLock()
	current := tr.tests[test.ID] == test && (test.Status == "STOPPING" || test.Status == "FAILED")
	raw := test.continuityCheckpoint
	provenTerminal := interruptedEvacuationHasDurableFenceProof(test)
	tr.mu.RUnlock()
	if !current || !interruptedRunEvacuation(raw) {
		return nil
	}
	if provenTerminal {
		return tr.reconcileFencedInterruptedEvacuationTerminal(b, test, raw)
	}
	guard, targets, err := interruptedEvacuationTargets(raw, test.ID)
	if err != nil {
		return err
	}
	// The capacity ledger may contain a standby or a staged replacement whose
	// target is absent from a partially written topology journal. Such a target
	// is still a possible participant and must be fenced before terminality.
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return errors.New("evacuation recovery requires a durable pod-capacity ledger to enumerate every possible participant")
	}
	rows, listErr := store.ListPodCapacityReservations()
	if listErr != nil {
		return fmt.Errorf("list possible evacuation participants: %w", listErr)
	}
	var podClaims []storage.PodCapacityReservation
	targetSet := make(map[string]struct{}, len(targets))
	for _, target := range targets {
		targetSet[target] = struct{}{}
	}
	for _, row := range rows {
		if row.TestID != test.ID {
			continue
		}
		if row.Target == "" || strings.TrimSpace(row.Target) != row.Target {
			return errors.New("evacuation capacity ledger has an invalid participant target")
		}
		podClaims = append(podClaims, row)
		if (row.Target != guard.Source || guard.SourceFencedAt.IsZero()) &&
			!priorCompletedTargetStillTerminal(guard, row.Target) {
			targetSet[row.Target] = struct{}{}
		}
	}
	if len(targetSet) > maxRecoveryCapacityPlanPods {
		return errors.New("evacuation participant set exceeds the bounded recovery fleet")
	}
	targets = targets[:0]
	for target := range targetSet {
		targets = append(targets, target)
	}
	sort.Strings(targets)
	generation, owner, deadline, err := tr.generatorExecutionClaim()
	if err != nil {
		return err
	}
	if generation <= guard.Generation {
		return fmt.Errorf("successor generation %d does not fence evacuation generation %d", generation, guard.Generation)
	}
	if successor := guard.successorGeneration(); successor > 0 && generation <= successor {
		// That term may have adopted the prior or next topology. Its own epoch
		// claim cannot drain assignments it adopted; only a newer term fences them.
		return fmt.Errorf("successor generation %d does not fence evacuation recovery generation %d", generation, successor)
	}
	ctx, cancel := context.WithDeadline(context.Background(), minTime(deadline, time.Now().Add(runAdoptionClaimBudget)))
	defer cancel()
	byTarget := make(map[string][]generatorrpc.RunAdoption, len(targets))
	for _, target := range targets {
		byTarget[target] = nil
	}
	results := b.claimRunAdoptions(ctx, byTarget, generation, owner, deadline)
	defer func() {
		for _, result := range results {
			if result.client != nil {
				_ = result.client.Close()
			}
		}
	}()
	var failures []error
	for _, target := range targets {
		result, present := results[target]
		if !present || result.err != nil || result.client == nil {
			failures = append(failures, fmt.Errorf("%s: successor process fence was not acknowledged: %v", target, result.err))
		}
	}
	if len(failures) != 0 {
		return errors.Join(failures...)
	}
	// The claim ACK is tied to a renewable leader term. Recheck before
	// publishing terminal state; the durable status write also verifies the
	// same lease owner through test.executionFence.
	if currentGeneration, currentOwner, _, claimErr := tr.generatorExecutionClaim(); claimErr != nil || currentGeneration != generation || currentOwner != owner {
		return errNotExecutionLeader
	}
	tr.mu.Lock()
	current = tr.tests[test.ID] == test && (test.Status == "STOPPING" || test.Status == "FAILED") && test.continuityCheckpoint == raw
	if !current {
		tr.mu.Unlock()
		return errNotExecutionLeader
	}
	if err := tr.claimTestExecutionFence(test); err != nil || test.executionFence != owner {
		tr.mu.Unlock()
		return errNotExecutionLeader
	}
	status, failureReason := test.Status, withoutEvacuationIntervention(test.Error)
	tr.mu.Unlock()
	confirmedReason := interruptedEvacuationFenceProof
	if failureReason != "" {
		confirmedReason += ": " + failureReason
	}
	failureReason = boundedtext.TruncateUTF8(scrub.Value(confirmedReason, scrub.Policy{}), 2048)
	// Terminality releases the run's timeslot. The observed generator claims
	// remain available for pod-ledger release; if a capacity RPC is unavailable,
	// ordinary capacity reconciliation keeps that reservation quarantined.
	var completeErr error
	if status == "STOPPING" {
		completeErr = tr.completeTestRun(test, "FAILED", time.Now().UTC(), failureReason, nil)
	}
	tr.mu.RLock()
	terminal := tr.tests[test.ID] == test && test.Status == "FAILED" && test.EndedAt != nil
	tr.mu.RUnlock()
	if !terminal {
		return errors.Join(completeErr, errors.New("evacuation generator fences were confirmed but durable terminality remains incomplete"))
	}
	// A terminal write can succeed even when a later plan/timeslot side effect
	// fails. Perform archive and pod cleanup on that durable terminal result,
	// and allow a later retry/restart to repeat these idempotent operations.
	archiveErr := tr.recordInterruptedEvacuationArchiveFailure(test)
	if len(podClaims) > 0 {
		clients := make(map[string]generatorClient)
		for _, row := range podClaims {
			if result := results[row.Target]; result.client != nil {
				clients[row.Target] = result.client
			}
		}
		tr.releasePodClaims(podClaims, clients, owner, generation)
	}
	return errors.Join(completeErr, archiveErr)
}

// Once terminality and its exact process-fence proof are durable, retries only
// need to finish archive and capacity cleanup. Requiring vanished generators
// to acknowledge another epoch would strand otherwise safe post-terminal work.
func (tr *TestRunner) reconcileFencedInterruptedEvacuationTerminal(b *distributedExecutionBackend, test *ManagedTest, raw string) error {
	archiveErr, err := tr.reconcileFencedInterruptedEvacuationArchive(b, test, raw)
	if err != nil {
		return err
	}
	// Pod-capacity release dials generators; it runs outside the archive
	// commit fence so deletes and archive commits for this run do not wait on it.
	store, ok := tr.testStore.(podCapacityStore)
	if !ok {
		return errors.Join(archiveErr, errors.New("terminal evacuation cleanup requires a durable pod-capacity ledger"))
	}
	claims, err := store.ListPodCapacityReservations()
	if err != nil {
		return errors.Join(archiveErr, fmt.Errorf("list terminal evacuation capacity claims: %w", err))
	}
	var owned []storage.PodCapacityReservation
	for _, claim := range claims {
		if claim.TestID == test.ID {
			owned = append(owned, claim)
		}
	}
	if len(owned) > 0 {
		// A missing pod stays quarantined in releasing state; ordinary
		// inventory reconciliation releases it only with incarnation proof.
		tr.releasePodClaimsWithDial(b, owned)
	}
	return archiveErr
}

// reconcileFencedInterruptedEvacuationArchive retires the canceled runtime's
// transports and held evidence and records the archive outcome under the
// archive commit fence, so no archive collection uses those transports meanwhile.
// ownershipErr stops all cleanup; archiveErr is reported after capacity release.
func (tr *TestRunner) reconcileFencedInterruptedEvacuationArchive(b *distributedExecutionBackend, test *ManagedTest, raw string) (archiveErr, ownershipErr error) {
	unlockArchive := tr.lockArchiveCommitFence(test.ID)
	defer unlockArchive()
	_, owner, _, err := tr.generatorExecutionClaim()
	if err != nil {
		return nil, err
	}
	tr.mu.Lock()
	current := tr.tests[test.ID] == test && test.continuityCheckpoint == raw && interruptedEvacuationHasDurableFenceProof(test)
	if !current {
		tr.mu.Unlock()
		return nil, errNotExecutionLeader
	}
	if err := tr.claimTestExecutionFence(test); err != nil || test.executionFence != owner {
		tr.mu.Unlock()
		return nil, errNotExecutionLeader
	}
	tr.mu.Unlock()
	// Same-leader attestation can retain transports from the canceled runtime.
	// Close them only after the terminal proof and its ownership fence are durable.
	b.closeRemoteResources(tr, test)
	// Collection recovery hydrates a detached durable snapshot; no writer in
	// this canceled runtime can carry these held batches into another point.
	// Retire them with the normal explicit dropped-evidence accounting.
	tr.mu.Lock()
	releaseHeldJointPublicationsLocked(test)
	tr.mu.Unlock()
	return tr.recordInterruptedEvacuationArchiveFailure(test), nil
}

func (tr *TestRunner) resumeInterruptedRunEvacuations() {
	tr.mu.RLock()
	tests := make([]*ManagedTest, 0)
	for _, test := range tr.tests {
		if (test.Status == "STOPPING" || test.Status == "FAILED") && interruptedRunEvacuation(test.continuityCheckpoint) {
			tests = append(tests, test)
		}
	}
	tr.mu.RUnlock()
	for _, test := range tests {
		tr.mu.RLock()
		raw := test.continuityCheckpoint
		provenTerminal := interruptedEvacuationHasDurableFenceProof(test)
		tr.mu.RUnlock()
		if !provenTerminal {
			guard, _, err := interruptedEvacuationTargets(raw, test.ID)
			if err != nil {
				slog.Warn("interrupted evacuation cannot be fenced automatically; a system administrator can attest that its generators stopped (POST /api/v1/tests/{id}/interrupted-stop/resolve)", "test_id", test.ID, "error", err)
				continue
			}
			if generation, _, _, claimErr := tr.generatorExecutionClaim(); guard.successorGeneration() > 0 && (claimErr != nil || generation <= guard.successorGeneration()) {
				slog.Warn("a failed evacuation rollback or roll-forward waits for a newer controller term or an administrator attestation (POST /api/v1/tests/{id}/interrupted-stop/resolve)", "test_id", test.ID, "recovery_generation", guard.successorGeneration())
				continue
			}
		}
		tr.enqueueTerminalRetry("evacuation-guard:"+test.ID, "interrupted evacuation process fences", func() error {
			return tr.recoverInterruptedRunEvacuation(test)
		})
	}
}
