package generatorrpc

import (
	"crypto/sha256"
)

// A run's terminal transition removes its assignment first, then finalizes its
// archive, and only then publishes the terminal tombstone that adoption
// reports. A successor's adoption claim in between found no terminal state
// and answered ALREADY_STOPPED (natural completions leave a supervision
// tombstone) or UNKNOWN_TEST, which the successor's recoveries cannot tell
// from a run that is still draining. finalizingRun marks that window, so the
// adoption answer can say the outcome is still being finalized and a newer
// controller can wait for it.
//
// The mark is a proof that nothing of the run executes any more: it is set
// only by finishStoppedTestWithState, whose every caller has already removed
// the assignment and observed every execution end (the last group finishing
// in finishExecutionMatching, the completion timer of an assignment without
// executions, and a detached drain after waitForGeneratorExecutions). A drain
// that never ends escalates instead and is never marked. Between a caller's
// release of s.mu and the mark, a claim still reads the earlier answer, which
// only holds the run as before. A successor whose adoption names the run's
// lineage only in an older proof finds it through the supervision tombstone a
// supervised run leaves when its supervision is cleared
// (selectAdoptionPriorLocked). The stopping barrier
// (lifecycleCoordinator.stoppingTests) keeps the test ID from being assigned
// again until completeStoppingTest, which clears the mark after the terminal
// tombstone is published.
type finalizingRun struct {
	generation uint64
	tokenHash  [sha256.Size]byte
}

// markRunFinalizingLocked records that assignment's executions all ended and
// its assignment is gone. The caller holds s.mu and guarantees both.
func (s *Service) markRunFinalizingLocked(testID string, assignment assignedTest) {
	if current, assigned := s.assignments[testID]; assigned && current.executionGeneration == assignment.executionGeneration {
		return
	}
	if s.finalizingRuns == nil {
		s.finalizingRuns = make(map[string]finalizingRun)
	}
	s.finalizingRuns[testID] = finalizingRun{generation: assignment.executionGeneration, tokenHash: assignment.runSupervisionTokenHash}
}

// clearRunFinalizingLocked ends the window once the terminal outcome is
// published. The caller holds s.mu.
func (s *Service) clearRunFinalizingLocked(testID string, generation uint64) {
	if run, ok := s.finalizingRuns[testID]; ok && run.generation == generation {
		delete(s.finalizingRuns, testID)
	}
}

// runFinalizingLocked reports whether the exact lineage (generation and
// supervision token) of testID is finalizing. The caller holds s.mu.
func (s *Service) runFinalizingLocked(testID string, generation uint64, tokenHash [sha256.Size]byte) bool {
	run, ok := s.finalizingRuns[testID]
	return ok && run.generation == generation && !emptyRunSupervisionTokenHash(run.tokenHash) &&
		sameRunSupervisionTokenHash(run.tokenHash, tokenHash)
}
