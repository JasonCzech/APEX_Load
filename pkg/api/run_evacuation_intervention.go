package api

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Each retry already bounds every participant RPC and retries it a fixed
// number of times. After this many failed rounds, the current owner keeps the
// run quarantined for intervention instead of spinning forever on an epoch
// that a restarted worker cannot accept. The initial inline round is separate.
const maxSameLeaderEvacuationCleanupRetries = 5
const sameLeaderEvacuationCleanupExhausted = "automatic evacuation stop retries exhausted"

// RunEvacuationQuarantine exposes structured quarantine diagnostics while an
// interrupted or fail-closed generator evacuation (or interrupted distributed
// run) is held in STOPPING awaiting generator stop/fence proof or explicit
// system-administrator attestation (REC-B1).
type RunEvacuationQuarantine struct {
	QuarantinedOwners          []string   `json:"quarantined_owners,omitempty"`
	RetryCount                 int        `json:"retry_count"`
	MaxRetries                 int        `json:"max_retries"`
	RetriesExhausted           bool       `json:"retries_exhausted,omitempty"`
	NextRetryAt                *time.Time `json:"next_retry_at,omitempty"`
	RequiredInterventionAction string     `json:"required_intervention_action,omitempty"`
	OriginalCause              string     `json:"original_cause,omitempty"`
}

func evacuationQuarantineRetryDelay(retryAttempts int) time.Duration {
	if retryAttempts <= 0 {
		return executionPlanRetryInitialDelay
	}
	shift := retryAttempts
	if shift > 16 {
		shift = 16
	}
	return min(executionPlanRetryInitialDelay*(1<<shift), executionPlanRetryMaxDelay)
}

func evacuationQuarantineOriginalCause(reason string) string {
	cause := withoutInterruptedDistributedStopReason(withoutEvacuationIntervention(reason))
	if index := strings.Index(cause, "; "+sameLeaderEvacuationCleanupExhausted); index >= 0 {
		cause = cause[:index]
	}
	return strings.TrimSpace(cause)
}

func evacuationQuarantineOwnersLocked(test *ManagedTest, evacuation bool) []string {
	if test == nil {
		return nil
	}
	ownerSet := make(map[string]struct{})
	if evacuation {
		if _, targets, err := interruptedEvacuationTargets(test.continuityCheckpoint, test.ID); err == nil {
			for _, target := range targets {
				if trimmed := strings.TrimSpace(target); trimmed != "" {
					ownerSet[trimmed] = struct{}{}
				}
			}
		}
	}
	for target := range test.remoteClientsByTarget {
		if trimmed := strings.TrimSpace(target); trimmed != "" {
			ownerSet[trimmed] = struct{}{}
		}
	}
	for _, shard := range test.GeneratorShards {
		if trimmed := strings.TrimSpace(shard.Target); trimmed != "" {
			ownerSet[trimmed] = struct{}{}
		}
	}
	if len(ownerSet) == 0 {
		return nil
	}
	owners := make([]string, 0, len(ownerSet))
	for target := range ownerSet {
		owners = append(owners, target)
	}
	sort.Strings(owners)
	return owners
}

func runEvacuationQuarantineStatusLocked(test *ManagedTest) *RunEvacuationQuarantine {
	if test == nil || test.Status != "STOPPING" {
		return nil
	}
	evacuation := interruptedEvacuationAwaitingProofLocked(test)
	distributed := interruptedDistributedRunLocked(test)
	if !evacuation && !distributed {
		return nil
	}
	exhausted := test.evacuationQuarantineNextRetryAt == nil && (strings.Contains(test.Error, sameLeaderEvacuationCleanupExhausted) || test.evacuationQuarantineRetries >= maxSameLeaderEvacuationCleanupRetries)
	retryCount := test.evacuationQuarantineRetries
	if exhausted && retryCount < maxSameLeaderEvacuationCleanupRetries {
		retryCount = maxSameLeaderEvacuationCleanupRetries
	}
	var nextRetryAt *time.Time
	if !exhausted {
		nextRetryAt = cloneTimePtr(test.evacuationQuarantineNextRetryAt)
	}
	return &RunEvacuationQuarantine{
		QuarantinedOwners:          evacuationQuarantineOwnersLocked(test, evacuation),
		RetryCount:                 retryCount,
		MaxRetries:                 maxSameLeaderEvacuationCleanupRetries,
		RetriesExhausted:           exhausted,
		NextRetryAt:                nextRetryAt,
		RequiredInterventionAction: fmt.Sprintf("POST /api/v1/tests/%s/interrupted-stop/resolve", test.ID),
		OriginalCause:              evacuationQuarantineOriginalCause(test.Error),
	}
}

// holdSameLeaderEvacuationForIntervention changes only the diagnostic. It does
// not supply a process fence, release capacity, end the run or close transports.
// Persist before retiring the retry worker so a controller crash cannot erase
// the unresolved state. Non-current and already-terminal incarnations retain
// their original cleanup path unless they have explicit durable fence proof.
func (tr *TestRunner) holdSameLeaderEvacuationForIntervention(test *ManagedTest, lastErr error) (bool, error) {
	return tr.holdEvacuationForIntervention(test, true, lastErr)
}

// holdInterruptedEvacuationForIntervention bounds successor-leader automatic
// process-fence retries on an interrupted evacuation without weakening fences.
// When a vanished or unreachable generator cannot acknowledge the successor's
// epoch claim after maxSameLeaderEvacuationCleanupRetries rounds, the successor
// persists the bounded intervention diagnostic while preserving the original
// failure cause and holding the run STOPPING until a newer term proves all
// participants or a system administrator attests termination.
func (tr *TestRunner) holdInterruptedEvacuationForIntervention(test *ManagedTest, lastErr error) (bool, error) {
	return tr.holdEvacuationForIntervention(test, false, lastErr)
}

func (tr *TestRunner) holdEvacuationForIntervention(test *ManagedTest, requireCanceledLocalRuntime bool, lastErr error) (bool, error) {
	if tr == nil || test == nil || lastErr == nil {
		return false, nil
	}
	tr.mu.Lock()
	if tr.tests[test.ID] != test || (requireCanceledLocalRuntime && test.executionCtx == nil) || !interruptedEvacuationAwaitingProofLocked(test) {
		tr.mu.Unlock()
		return false, nil
	}
	if tr.testStore == nil {
		tr.mu.Unlock()
		return false, errors.New("evacuation intervention requires durable test persistence")
	}
	if !requireCanceledLocalRuntime && test.executionFence == "" {
		if err := tr.claimTestExecutionFence(test); err != nil {
			tr.mu.Unlock()
			return false, err
		}
	}
	cause := test.Error
	if index := strings.Index(cause, uncertainEvacuationIntervention); index >= 0 {
		cause = cause[:index]
	}
	if index := strings.Index(cause, "; "+sameLeaderEvacuationCleanupExhausted); index >= 0 {
		cause = cause[:index]
	}
	if strings.TrimSpace(cause) == "" {
		cause = "generator evacuation state is uncertain: controller ownership changed before source and replacement cleanup was proven"
	}
	message := boundedtext.TruncateUTF8(scrub.Value(cause, scrub.Policy{}), 900) + "; " + sameLeaderEvacuationCleanupExhausted + ": " + boundedtext.TruncateUTF8(scrub.Value(lastErr.Error(), scrub.Policy{}), 700) + uncertainEvacuationIntervention
	if err := tr.persistStoredTestStatus(test.ID, test.executionFence, "STOPPING", nil, message); err != nil {
		tr.mu.Unlock()
		return false, fmt.Errorf("persist evacuation intervention: %w", err)
	}
	test.Error = message
	test.evacuationQuarantineRetries = maxSameLeaderEvacuationCleanupRetries
	test.evacuationQuarantineNextRetryAt = nil
	snapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()
	tr.broadcastEvent("test_stopping", snapshot)
	return true, nil
}
