package generatorrpc

import (
	"fmt"
	"strings"
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// HA run continuity, generator side (docs/HA_RUN_CONTINUITY_PLAN.md §5.2-5.3).
//
// A run staged with adoption_grace > 0 is orphaned rather than fenced when its
// controller's execution epoch lapses: it keeps running, bounded by the grace,
// through its existing run-supervision deadman. A successor controller adopts
// it by listing it in the adoptions of its first higher-generation
// ExecutionEpochClaim, proving lineage with the prior generation and
// supervision token. An orphan that is not adopted is either fenced by that
// claim (like any lower-generation work) or stopped by the deadman with
// controller_adoption_timeout.

// orphanLocked moves one assignment of the lapsing generation into the orphaned
// state when it opted in and is still live and supervised. It returns false for
// anything that must be fenced as before. The caller holds s.mu.
func (s *Service) orphanLocked(testID string, assignment assignedTest, generation uint64, now time.Time) bool {
	if assignment.adoptionGrace <= 0 || assignment.executionGeneration != generation || !assignment.committed ||
		emptyRunSupervisionTokenHash(assignment.runSupervisionTokenHash) {
		return false
	}
	if _, stopping := s.stoppingTests[testID]; stopping {
		return false
	}
	lease := s.runSupervisions[testID]
	if lease == nil || lease.generation != generation ||
		!sameRunSupervisionTokenHash(lease.tokenHash, assignment.runSupervisionTokenHash) ||
		lease.state != pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE || !now.Before(lease.deadline) {
		return false
	}
	if assignment.orphanedUntil.IsZero() {
		assignment.orphanedUntil = now.Add(assignment.adoptionGrace)
		s.assignments[testID] = assignment
	}
	if assignment.requireTestExecutionLease {
		// The lost controller's run lease is fenced for this generation: its
		// timer must not stop the orphan and the old term cannot renew it. The
		// successor claims a fresh run lease under its own generation.
		s.clearTestExecutionLeaseLocked(testID)
		s.recordTestExecutionLeaseFenceLocked(testID, generation)
	}
	// The deadman now enforces the grace exactly, shortening a longer
	// controller-granted lease: an orphan never runs past its grace.
	s.resetRunSupervisionTimerLocked(testID, lease, assignment.orphanedUntil)
	return true
}

func validateRunAdoptions(adoptions []*pb.RunAdoption) error {
	seen := make(map[string]struct{}, len(adoptions))
	for _, adoption := range adoptions {
		testID := strings.TrimSpace(adoption.GetTestId())
		if testID == "" {
			return status.Error(codes.InvalidArgument, "adoptions[].test_id is required")
		}
		if _, duplicate := seen[testID]; duplicate {
			return status.Errorf(codes.InvalidArgument, "adoptions lists test %q more than once", testID)
		}
		seen[testID] = struct{}{}
		if len(adoption.GetPriorProofs()) >= MaxRunAdoptionPriorProofs {
			return status.Error(codes.InvalidArgument, "too many adoption lineage proofs")
		}
		if !validRunSupervisionToken(adoption.GetNewRunSupervisionToken()) {
			return status.Error(codes.InvalidArgument, "invalid new adoption supervision token")
		}
		generations := make(map[uint64]bool)
		for _, proof := range adoptionPriorProofs(adoption) {
			generation := proof.GetGeneration()
			if generation == 0 || generations[generation] || !validRunSupervisionToken(proof.GetRunSupervisionToken()) {
				return status.Error(codes.InvalidArgument, "adoption requires distinct positive prior generations and valid tokens")
			}
			generations[generation] = true
			if sameRunSupervisionToken(runSupervisionTokenHash(proof.GetRunSupervisionToken()), adoption.GetNewRunSupervisionToken()) {
				return status.Error(codes.InvalidArgument, "adoption must rotate every prior supervision token")
			}
		}
	}
	return nil
}

// adoptRunsLocked resolves each requested adoption against generation. rebind
// is true only while committing a strictly higher generation, before its
// lower-generation fence list is computed; it is false for a same-generation
// claim, which can only confirm an adoption that already happened (a retry
// after a lost acknowledgement). The caller holds s.mu.
func (s *Service) adoptRunsLocked(generation uint64, adoptions []*pb.RunAdoption, rebind bool, now time.Time) []*pb.RunAdoptionResult {
	if len(adoptions) == 0 {
		return nil
	}
	results := make([]*pb.RunAdoptionResult, 0, len(adoptions))
	for _, adoption := range adoptions {
		results = append(results, s.adoptRunLocked(generation, adoption, rebind, now))
	}
	return results
}

func (s *Service) adoptRunLocked(generation uint64, adoption *pb.RunAdoption, rebind bool, now time.Time) *pb.RunAdoptionResult {
	for _, proof := range adoptionPriorProofs(adoption) {
		if proof.GetGeneration() >= generation {
			return &pb.RunAdoptionResult{TestId: strings.TrimSpace(adoption.GetTestId()), Outcome: pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, Detail: "prior proof generation is not older than the claimed generation"}
		}
	}
	adoption = s.selectAdoptionPriorLocked(adoption)
	testID := strings.TrimSpace(adoption.GetTestId())
	prior := adoption.GetPriorGeneration()
	result := func(outcome pb.RunAdoptionOutcome, detail string) *pb.RunAdoptionResult {
		return &pb.RunAdoptionResult{TestId: testID, Outcome: outcome, Detail: detail}
	}
	priorHash := runSupervisionTokenHash(adoption.GetPriorRunSupervisionToken())
	newHash := runSupervisionTokenHash(adoption.GetNewRunSupervisionToken())

	assignment, exists := s.assignments[testID]
	if exists && assignment.executionGeneration == generation {
		if sameRunSupervisionTokenHash(assignment.runSupervisionTokenHash, newHash) {
			return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "already adopted by this generation")
		}
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, "the run already belongs to this generation under a different token")
	}
	if prior >= generation {
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, fmt.Sprintf("prior generation %d is not older than %d", prior, generation))
	}
	if !exists {
		if state, terminal := s.metricsBroker.terminalState(testID, prior); terminal {
			outcome := pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED
			if strings.EqualFold(strings.TrimSpace(state), "COMPLETE") || strings.EqualFold(strings.TrimSpace(state), "COMPLETED") {
				outcome = pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_COMPLETED
			}
			return &pb.RunAdoptionResult{TestId: testID, Outcome: outcome, Detail: "the run finished before adoption", TerminalState: state}
		}
		if s.hasRunSupervisionTombstoneLocked(runSupervisionTombstoneKey{testID: testID, generation: prior, tokenHash: priorHash}) {
			return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, "the run's supervision already expired")
		}
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_UNKNOWN_TEST, "no assignment or retained outcome for this run")
	}
	if assignment.executionGeneration != prior {
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, fmt.Sprintf("the run belongs to generation %d, not %d", assignment.executionGeneration, prior))
	}
	if !sameRunSupervisionTokenHash(assignment.runSupervisionTokenHash, priorHash) {
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, "run supervision token does not match the assignment")
	}
	if _, stopping := s.stoppingTests[testID]; stopping {
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ALREADY_STOPPED, "the run is already stopping")
	}
	switch {
	case assignment.adoptionGrace <= 0:
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, "the run did not opt into adoption")
	case !assignment.committed:
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, "staged work cannot be adopted; it never crossed its start barrier")
	case !rebind:
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, "adoption must accompany the first claim of a new generation; this run is being fenced")
	}
	lease := s.runSupervisions[testID]
	if lease == nil || lease.generation != prior || !sameRunSupervisionTokenHash(lease.tokenHash, priorHash) ||
		lease.state != pb.RunSupervisionState_RUN_SUPERVISION_STATE_ACTIVE || !now.Before(lease.deadline) {
		return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, "the run has no live supervision lease to transfer")
	}

	// Capacity belongs to the same execution lineage as the assignment. Keep
	// the frozen reservation intact while transferring only its owner term.
	if claim := s.podCapacityClaims[testID]; claim != nil {
		if claim.ExecutionGeneration != prior || claim.Incarnation != s.podCapacityIncarnation {
			return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_REJECTED, "capacity reservation does not belong to the adopted lineage")
		}
		claim.ExecutionGeneration = generation
	}

	assignment.executionGeneration = generation
	assignment.runSupervisionTokenHash = newHash
	assignment.orphanedUntil = time.Time{}
	// Pause operation ordering belongs to a controller term. A successor starts
	// its counter afresh; old-term requests are already rejected by generation.
	assignment.pauseOperationID = ""
	assignment.pauseOperationSequence = 0
	s.assignments[testID] = assignment
	// Running group executions carry their own generation, and metrics
	// publication, snapshots and completion all require it to match the
	// assignment's; without this the adopted run would go silent.
	for _, execution := range s.executions[testID] {
		if execution != nil && execution.executionGeneration == prior {
			execution.executionGeneration = generation
		}
	}
	lease.generation = generation
	lease.tokenHash = newHash
	// The successor must confirm with ClaimRunSupervision under the new token
	// within one supervision TTL, exactly as for a freshly started run.
	s.resetRunSupervisionTimerLocked(testID, lease, now.Add(max(lease.ttl, minimumRunSupervisionTTL)))
	return result(pb.RunAdoptionOutcome_RUN_ADOPTION_OUTCOME_ADOPTED, "")
}
