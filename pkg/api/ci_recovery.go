package api

import (
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
)

// recoverQueuedCIRuns closes the durable crash window between CI test
// materialization, required Check creation, and StartTest. It is called only at
// process startup or after acquiring execution leadership, when no request on
// this replica can still be in the middle of materializing these rows.
func (tr *TestRunner) recoverQueuedCIRuns() error {
	if tr == nil || !tr.holdsExecutionLeadership() {
		return nil
	}
	term, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	tr.mu.RLock()
	ids := make([]string, 0)
	for id, test := range tr.tests {
		if test == nil || !strings.EqualFold(strings.TrimSpace(test.Status), "QUEUED") || test.StartedAt != nil {
			continue
		}
		if _, err := decodeCIRunMetadata(test.MaterializationKey); err == nil {
			ids = append(ids, id)
		}
	}
	tr.mu.RUnlock()
	for _, id := range ids {
		if err := tr.recoverQueuedCIRunWithTerm(id, term); err != nil {
			if errors.Is(err, errNotExecutionLeader) || errors.Is(err, errRunnerShuttingDown) {
				return err
			}
			slog.Warn("queued CI run recovery failed; continuing with remaining runs", "test_id", id, "error", err)
		}
	}
	return nil
}

func (tr *TestRunner) recoverQueuedCIRun(id string) error {
	term, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	return tr.recoverQueuedCIRunWithTerm(id, term)
}

func (tr *TestRunner) recoverQueuedCIRunWithTerm(id string, term executionLeaseTerm) error {
	if tr == nil || !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	tr.mu.RLock()
	id = tr.resolveRefKeyLocked(id)
	test := tr.tests[id]
	if test == nil || !strings.EqualFold(strings.TrimSpace(test.Status), "QUEUED") || test.StartedAt != nil {
		tr.mu.RUnlock()
		return nil
	}
	metadata, err := decodeCIRunMetadata(test.MaterializationKey)
	tr.mu.RUnlock()
	if err != nil {
		return nil
	}
	if metadata.PublishCheck {
		store := tr.githubCheckStore
		if store == nil {
			return fmt.Errorf("required GitHub Check storage is unavailable")
		}
		link, err := storedCIGitHubCheckForRun(store, test, metadata)
		if err != nil {
			return err
		}
		if link == nil {
			// The Check outbox reconciler will call this method after it has
			// recovered the authoritative remote Check and durable link.
			return nil
		}
	}
	if err := tr.startTestWithOptionsAndExpectedTerm(id, StartTestOptions{}, &term); err != nil {
		if errors.Is(err, errNotExecutionLeader) || errors.Is(err, errRunnerShuttingDown) {
			return err
		}
		message := "controller could not recover queued CI run: " + err.Error()
		if terminalErr := tr.failCIRunBeforeExecutionWithTerm(id, message, true, &term); terminalErr != nil {
			return errors.Join(err, terminalErr)
		}
		return nil
	}
	return nil
}

// validateCIRunPolicyForStart closes the gap between acceptance and execution.
// A queued or pending run is still subject to the exact Actions policy revision
// that authorized it; disabling or revising that policy revokes execution even
// when a controller restart or capacity queue delayed the start.
func (tr *TestRunner) validateCIRunPolicyForStart(id string) error {
	if tr == nil {
		return nil
	}
	tr.mu.RLock()
	test := tr.tests[id]
	if test == nil {
		tr.mu.RUnlock()
		return nil
	}
	projectID := test.ProjectID
	metadata, metadataErr := decodeCIRunMetadata(test.MaterializationKey)
	tr.mu.RUnlock()
	if metadataErr != nil {
		return nil
	}
	if tr.projectStore == nil {
		return fmt.Errorf("CI run trust policy is unavailable")
	}
	project, err := tr.projectStore.GetProject(normalizeProjectID(projectID))
	if err != nil || project == nil {
		return fmt.Errorf("CI run project trust policy is unavailable")
	}
	policy := normalizeGitHubProjectPolicy(project.Settings.GitHub).Actions
	if !policy.Enabled {
		return fmt.Errorf("GitHub Actions execution was disabled after this CI run was accepted")
	}
	if policy.Revision != metadata.PolicyRevision {
		return fmt.Errorf("GitHub Actions policy revision changed after this CI run was accepted")
	}
	return nil
}

func (tr *TestRunner) failCIRunBeforeExecution(id, message string) error {
	return tr.failCIRunBeforeExecutionWithPromotion(id, message, true)
}

// failCIRunBeforeExecutionDuringPromotion terminalizes one revoked pending row
// without starting another queue drain. The caller already owns the iterative
// promotion loop; recursively re-entering it grows one stack frame per revoked
// row and can exhaust the controller stack on a large recovered queue.
func (tr *TestRunner) failCIRunBeforeExecutionDuringPromotion(id, message string) error {
	return tr.failCIRunBeforeExecutionWithPromotion(id, message, false)
}

func (tr *TestRunner) failCIRunBeforeExecutionWithPromotion(id, message string, promotePending bool) error {
	return tr.failCIRunBeforeExecutionWithTerm(id, message, promotePending, nil)
}

func (tr *TestRunner) failCIRunBeforeExecutionWithTerm(id, message string, promotePending bool, expectedTerm *executionLeaseTerm) error {
	if err := tr.validateExpectedStartTerm(expectedTerm); err != nil {
		return err
	}
	tr.mu.Lock()
	current := tr.tests[id]
	if current == nil || !ciRunAwaitingExecution(current) {
		tr.mu.Unlock()
		return nil
	}
	if _, err := decodeCIRunMetadata(current.MaterializationKey); err != nil {
		tr.mu.Unlock()
		return fmt.Errorf("test is not a controller-backed CI run")
	}
	var claimErr error
	if expectedTerm != nil {
		claimErr = tr.claimTestExecutionFenceWithTerm(current, *expectedTerm)
	} else {
		claimErr = tr.claimTestExecutionFence(current)
	}
	if claimErr != nil {
		tr.mu.Unlock()
		return claimErr
	}
	tr.mu.Unlock()
	return tr.completeTestRunWithPendingPromotion(current, "FAILED", time.Now().UTC(), strings.TrimSpace(message), nil, promotePending)
}

func ciRunAwaitingExecution(test *ManagedTest) bool {
	if test == nil || test.StartedAt != nil || test.cancel != nil || test.executionCtx != nil {
		return false
	}
	switch strings.ToUpper(strings.TrimSpace(test.Status)) {
	case "QUEUED", "SCHEDULED", "PENDING", "RESERVED", "SHAKEOUT_REVIEW":
		return true
	default:
		return false
	}
}
