package generatorrpc

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

const (
	runtimeContainmentFailureReason = "local_java_process_containment_unproven"
	runtimeContainmentFinalError    = "local java_vuser process containment became unproven"
)

// WithRuntimeContainmentFailureSignal binds a process-global runtime safety
// signal to the generator lifecycle. Production uses the local java_vuser
// process-tree containment signal; the generic channel keeps the generator
// service independent of a concrete protocol implementation.
func WithRuntimeContainmentFailureSignal(failed <-chan struct{}) ServiceOption {
	return func(service *Service) {
		if service != nil {
			service.runtimeContainmentFailure = failed
		}
	}
}

func (s *Service) startRuntimeContainmentFailureMonitor() {
	if s == nil || s.runtimeContainmentFailure == nil {
		return
	}
	failed := s.runtimeContainmentFailure
	select {
	case <-failed:
		// Handle an already-terminal runtime synchronously so NewService cannot
		// briefly advertise readiness before the monitor goroutine is scheduled.
		s.handleRuntimeContainmentFailure()
	default:
		go func() {
			select {
			case <-failed:
				s.handleRuntimeContainmentFailure()
			case <-s.shutdown:
			}
		}()
	}
}

func (s *Service) handleRuntimeContainmentFailure() {
	requestedAt := time.Now().UTC()

	// Revoke admission and detach every active run in one state transition.
	// The detached records retain the exact execution.done barriers and capacity
	// reservations while the process owner performs its bounded exit.
	s.mu.Lock()
	if !s.draining {
		s.draining = true
		s.notifyReadinessChangedLocked()
	}
	testIDs := make([]string, 0, len(s.assignments))
	for testID := range s.assignments {
		testIDs = append(testIDs, testID)
	}
	sort.Strings(testIDs)
	stops := s.detachFencedTestsLocked(testIDs, ^uint64(0), false)
	s.mu.Unlock()

	outstanding := 0
	evidence := make([]SelfTerminationRequest, 0, len(stops))
	for index := range stops {
		stop := &stops[index]
		stop.finalState = "FAILED"
		stop.finalError = runtimeContainmentFinalError
		stop.escalationReason = runtimeContainmentFailureReason
		open := outstandingGeneratorExecutions(stop.executions)
		outstanding += open
		evidence = append(evidence, SelfTerminationRequest{
			TestID:              stop.testID,
			ExecutionGeneration: stop.assignment.executionGeneration,
			Reason:              runtimeContainmentFailureReason,
			Outstanding:         open,
			RequestedAt:         requestedAt,
		})
	}

	// Containment is already unproven, so configured ramp-down and the ordinary
	// supervision soft-drain window are unsafe. Hard-cancel every captured engine
	// synchronously before notifying the process owner or touching the filesystem.
	for _, stop := range stops {
		signalDetachedRunHard(stop)
	}

	// Hold the durable-store lifetime lock across watchdog-first notification
	// and the exact batch replacement. Main may begin shutdown as soon as it
	// receives the buffered request; without this critical section it can close
	// the state root before the containment evidence establishes the restart
	// fence. Notification remains first inside the lock, so a wedged fsync still
	// cannot defeat main's independent +2s exit watchdog.
	s.runSupervisionIntentMu.Lock()
	s.requestSelfTermination(SelfTerminationRequest{
		Reason:      runtimeContainmentFailureReason,
		Outstanding: outstanding,
		RequestedAt: requestedAt,
	})
	if s.runtimeContainmentBeforePersist != nil {
		s.runtimeContainmentBeforePersist()
	}
	persistErr := s.persistRuntimeContainmentEvidenceLocked(evidence)
	s.runSupervisionIntentMu.Unlock()
	if persistErr != nil {
		slog.Error("persist local Java containment-failure evidence",
			"event", "generator_runtime_containment_evidence_failed",
			"reason", runtimeContainmentFailureReason,
			"affected_runs", len(evidence),
			"outstanding_executions", outstanding,
			"error", persistErr,
		)
	}

	// Only after the exact batch is committed may the retained supervisors race
	// their own hard-grace escalation. Their hard signal is idempotent; invoking
	// it again transfers finalization ownership without reopening admission.
	results := make([]<-chan error, 0, len(stops))
	for _, stop := range stops {
		results = append(results, s.startDetachedRunDrain(stop, false))
	}
	slog.Error("local Java process containment became unproven",
		"event", "generator_runtime_containment_unproven",
		"reason", runtimeContainmentFailureReason,
		"affected_runs", len(evidence),
		"outstanding_executions", outstanding,
	)

	// Observe completion only to surface archive/finalization errors. Each drain
	// supervisor already owns its exact barriers and the process watchdog remains
	// independent if any of them never closes.
	go func() {
		for _, result := range results {
			select {
			case err := <-result:
				if err != nil {
					slog.Error("local Java containment cleanup remained unquiesced", "event", "generator_runtime_containment_cleanup_failed", "error", err)
				}
			case <-s.shutdown:
				return
			}
		}
	}()
}

// persistRuntimeContainmentEvidence appends all exact affected-run records with
// one journal replacement. It never persists the generator-scoped empty-ID
// notification used to start the process watchdog.
func (s *Service) persistRuntimeContainmentEvidence(requests []SelfTerminationRequest) error {
	if s == nil || len(requests) == 0 {
		return nil
	}
	s.runSupervisionIntentMu.Lock()
	defer s.runSupervisionIntentMu.Unlock()
	return s.persistRuntimeContainmentEvidenceLocked(requests)
}

func (s *Service) persistRuntimeContainmentEvidenceLocked(requests []SelfTerminationRequest) error {
	if len(requests) == 0 {
		return nil
	}
	records, err := s.readRunSupervisionCrashJournalLocked()
	if err != nil {
		return err
	}
	for _, request := range requests {
		if strings.TrimSpace(request.TestID) == "" {
			return fmt.Errorf("runtime containment evidence requires an exact test id")
		}
		record := runSupervisionCrashIntent{
			Version:             runSupervisionCrashVersion,
			Reason:              request.Reason,
			TestID:              request.TestID,
			ExecutionGeneration: request.ExecutionGeneration,
			Outstanding:         request.Outstanding,
			RequestedAt:         request.RequestedAt.UTC(),
		}
		if err := validateRunSupervisionCrashRecord(record); err != nil {
			return err
		}
		records = appendRunSupervisionCrashRecord(records, record)
	}
	if err := s.persistRunSupervisionCrashJournalLocked(records); err != nil {
		return err
	}
	s.setSafetyExitEvidenceLocked(records)
	return nil
}
