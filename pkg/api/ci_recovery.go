package api

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// recoverQueuedCIRuns closes the durable crash window between CI test
// materialization, required Check creation, and StartTest. It is called only at
// process startup or after acquiring execution leadership, when no request on
// this replica can still be in the middle of materializing these rows.
func (tr *TestRunner) recoverQueuedCIRuns() error {
	if tr == nil || !tr.isExecutionLeader() {
		return nil
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
		if err := tr.recoverQueuedCIRun(id); err != nil {
			if errors.Is(err, errNotExecutionLeader) || errors.Is(err, errRunnerShuttingDown) {
				return err
			}
			return fmt.Errorf("recover queued CI run %s: %w", id, err)
		}
	}
	return nil
}

func (tr *TestRunner) recoverQueuedCIRun(id string) error {
	if tr == nil || !tr.isExecutionLeader() {
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
	if err := tr.StartTest(id); err != nil {
		if errors.Is(err, errNotExecutionLeader) || errors.Is(err, errRunnerShuttingDown) {
			return err
		}
		message := "controller could not recover queued CI run: " + err.Error()
		if terminalErr := tr.failCIRunBeforeExecution(id, message); terminalErr != nil {
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
	if tr == nil || !tr.isExecutionLeader() {
		return errNotExecutionLeader
	}
	tr.mu.Lock()
	current := tr.tests[id]
	if current == nil || ciRunTerminal(current.Status) {
		tr.mu.Unlock()
		return nil
	}
	if _, err := decodeCIRunMetadata(current.MaterializationKey); err != nil {
		tr.mu.Unlock()
		return fmt.Errorf("test is not a controller-backed CI run")
	}
	if claimErr := tr.claimTestExecutionFence(current); claimErr != nil {
		tr.mu.Unlock()
		return claimErr
	}
	tr.mu.Unlock()
	return tr.completeTestRunWithPendingPromotion(current, "FAILED", time.Now().UTC(), strings.TrimSpace(message), nil, promotePending)
}
