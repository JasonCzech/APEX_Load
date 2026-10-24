package generatorrpc

import (
	"time"

	pb "github.com/jasonczech/apexload/api/proto"
)

// A generator ends both a controller stop and a run whose duration ran out
// COMPLETE. The terminal cause tells them apart for a successor controller
// (RunAdoptionResult.terminal_cause): the first lifecycle transition that
// removes the assignment, under s.mu, records it on the assignment.
//
//   - StopTest (planStopLocked): CONTROLLER_STOP, also for staged work and for
//     an assignment whose engines already reached their deadline.
//   - The last group finishing without error (finishExecutionMatching):
//     DURATION_ELAPSED when every group's engine reached the duration deadline
//     (engine.MultiGroupEngine.DurationDeadline), else EXECUTIONS_ENDED_EARLY.
//   - The completion timer, armed only without engine execution:
//     DURATION_ELAPSED for a committed, unpaused assignment whose own timer
//     fired.
//
// Fences, lease expiry, shutdown, staged expiry and a failed archive
// finalization end the run FAILED, and only a COMPLETE outcome carries a cause.
// The cause is retained beside the terminal tombstone and in the archive
// manifest, never in the terminal MetricsBatch: an older generator decodes the
// manifest's embedded batch strictly and would quarantine the archive.

// terminalOutcome is the cause and logical duration of one COMPLETE terminal
// transition. duration is set only for DURATION_ELAPSED.
type terminalOutcome struct {
	cause    pb.RunTerminalCause
	duration time.Duration
}

// assignmentTerminalOutcome is the outcome a terminal transition reports. The
// cause applies only to COMPLETE; the duration is the shortest logical target,
// in DurationAdjustment.target_duration terms, any engine of the assignment ran
// to.
func assignmentTerminalOutcome(assignment assignedTest, finalState string) terminalOutcome {
	if finalState != "COMPLETE" {
		return terminalOutcome{}
	}
	outcome := terminalOutcome{cause: assignment.terminalCause}
	if outcome.cause == pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED {
		if !assignment.deadlineReached || assignment.deadlineDuration <= 0 {
			return terminalOutcome{cause: pb.RunTerminalCause_RUN_TERMINAL_CAUSE_EXECUTIONS_ENDED_EARLY}
		}
		outcome.duration = assignment.deadlineDuration
	}
	return outcome
}

// setTerminalOutcome records outcome in the manifest Finalize writes.
func (w *archiveRecorder) setTerminalOutcome(outcome terminalOutcome) {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.manifest.TerminalCause, w.manifest.CompletedLogicalDuration = "", 0
	if outcome.cause != pb.RunTerminalCause_RUN_TERMINAL_CAUSE_UNSPECIFIED {
		w.manifest.TerminalCause = outcome.cause.String()
		w.manifest.CompletedLogicalDuration = outcome.duration
	}
}

// manifestTerminalOutcome reads the outcome a manifest recorded. A manifest
// written by an older generator, an unknown cause or a non-COMPLETE status
// yields UNSPECIFIED.
func manifestTerminalOutcome(manifest archiveManifest) terminalOutcome {
	value, known := pb.RunTerminalCause_value[manifest.TerminalCause]
	if !known || manifest.Status != "COMPLETE" {
		return terminalOutcome{}
	}
	outcome := terminalOutcome{cause: pb.RunTerminalCause(value)}
	if outcome.cause == pb.RunTerminalCause_RUN_TERMINAL_CAUSE_DURATION_ELAPSED {
		if manifest.CompletedLogicalDuration <= 0 {
			return terminalOutcome{}
		}
		outcome.duration = manifest.CompletedLogicalDuration
	}
	return outcome
}
