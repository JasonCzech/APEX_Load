package api

import (
	"errors"
	"fmt"
	"strings"

	"github.com/jasonczech/apexload/pkg/internal/boundedtext"
	"github.com/jasonczech/apexload/pkg/scrub"
)

// Each retry already bounds every participant RPC and retries it a fixed
// number of times. After this many failed rounds, the current owner keeps the
// run quarantined for intervention instead of spinning forever on an epoch
// that a restarted worker cannot accept. The initial inline round is separate.
const maxSameLeaderEvacuationCleanupRetries = 5
const sameLeaderEvacuationCleanupExhausted = "automatic evacuation stop retries exhausted"

// holdSameLeaderEvacuationForIntervention changes only the diagnostic. It does
// not supply a process fence, release capacity, end the run or close transports.
// Persist before retiring the retry worker so a controller crash cannot erase
// the unresolved state. Non-current and already-terminal incarnations retain
// their original cleanup path unless they have explicit durable fence proof.
func (tr *TestRunner) holdSameLeaderEvacuationForIntervention(test *ManagedTest, lastErr error) (bool, error) {
	if tr == nil || test == nil || lastErr == nil {
		return false, nil
	}
	tr.mu.Lock()
	if tr.tests[test.ID] != test || test.executionCtx == nil || !interruptedEvacuationAwaitingProofLocked(test) {
		tr.mu.Unlock()
		return false, nil
	}
	if tr.testStore == nil {
		tr.mu.Unlock()
		return false, errors.New("evacuation intervention requires durable test persistence")
	}
	cause := test.Error
	if index := strings.Index(cause, uncertainEvacuationIntervention); index >= 0 {
		cause = cause[:index]
	}
	if index := strings.Index(cause, "; "+sameLeaderEvacuationCleanupExhausted); index >= 0 {
		cause = cause[:index]
	}
	message := boundedtext.TruncateUTF8(scrub.Value(cause, scrub.Policy{}), 900) + "; " + sameLeaderEvacuationCleanupExhausted + ": " + boundedtext.TruncateUTF8(scrub.Value(lastErr.Error(), scrub.Policy{}), 700) + uncertainEvacuationIntervention
	if err := tr.persistStoredTestStatus(test.ID, test.executionFence, "STOPPING", nil, message); err != nil {
		tr.mu.Unlock()
		return false, fmt.Errorf("persist evacuation intervention: %w", err)
	}
	test.Error = message
	snapshot := cloneManagedTestFieldsLocked(test)
	tr.mu.Unlock()
	tr.broadcastEvent("test_stopping", snapshot)
	return true, nil
}
