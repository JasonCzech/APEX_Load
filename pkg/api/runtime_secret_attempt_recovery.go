package api

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

var errRuntimeSecretProvisioningActive = errors.New("runtime credential provisioning attempt is still active on this controller")

func (tr *TestRunner) markRuntimeOverrideSecretAttemptActive(id string) func() {
	id = strings.TrimSpace(id)
	if tr == nil || id == "" {
		return func() {}
	}
	tr.secretRecovery.mu.Lock()
	if tr.secretRecovery.active == nil {
		tr.secretRecovery.active = make(map[string]struct{})
	}
	tr.secretRecovery.active[id] = struct{}{}
	tr.secretRecovery.mu.Unlock()
	return func() {
		tr.secretRecovery.mu.Lock()
		delete(tr.secretRecovery.active, id)
		tr.secretRecovery.mu.Unlock()
	}
}

func (tr *TestRunner) runtimeOverrideSecretAttemptActive(id string) bool {
	if tr == nil {
		return false
	}
	tr.secretRecovery.mu.Lock()
	_, active := tr.secretRecovery.active[strings.TrimSpace(id)]
	tr.secretRecovery.mu.Unlock()
	return active
}

// scheduleRuntimeOverrideSecretAttemptReconciliation performs boot recovery for
// attempts whose lease has already expired and schedules a one-shot recovery
// for attempts created shortly before a controller restart.
func (tr *TestRunner) scheduleRuntimeOverrideSecretAttemptReconciliation() {
	if tr == nil || tr.secretAttemptStore == nil || !tr.beginRuntimeOverrideSecretRecovery() {
		return
	}
	defer tr.endRuntimeOverrideSecretRecovery()
	attempts, err := tr.secretAttemptStore.ListRuntimeSecretProvisioningAttempts()
	if err != nil {
		slog.Warn("runtime credential provisioning recovery scan failed", "error", err)
		tr.scheduleRuntimeOverrideSecretAttempt("", time.Now().Add(runtimeOverrideSecretRecoveryRetry))
		return
	}
	now := time.Now().UTC()
	for _, attempt := range attempts {
		if attempt == nil {
			continue
		}
		if !attempt.LeaseExpiresAt.After(now) {
			if tr.holdsExecutionLeadership() {
				if err := tr.reconcileRuntimeOverrideSecretAttempt(attempt); err != nil {
					slog.Warn("runtime credential provisioning recovery failed", "attempt_id", attempt.ID, "test_id", attempt.TestID, "error", err)
					tr.scheduleRuntimeOverrideSecretAttempt(attempt.ID, now.Add(runtimeOverrideSecretRecoveryRetry))
				}
			}
			continue
		}
		tr.scheduleRuntimeOverrideSecretAttempt(attempt.ID, attempt.LeaseExpiresAt)
	}
}

func (tr *TestRunner) scheduleRuntimeOverrideSecretAttempt(_ string, expiresAt time.Time) {
	if tr == nil {
		return
	}
	delay := time.Until(expiresAt)
	if delay < 0 {
		delay = 0
	}
	tr.secretRecovery.mu.Lock()
	if tr.secretRecovery.stopped {
		tr.secretRecovery.mu.Unlock()
		return
	}
	if tr.secretRecovery.timer != nil && !expiresAt.Before(tr.secretRecovery.at) {
		tr.secretRecovery.mu.Unlock()
		return
	}
	if tr.secretRecovery.timer != nil {
		tr.secretRecovery.timer.Stop()
	}
	tr.secretRecovery.generation++
	generation := tr.secretRecovery.generation
	tr.secretRecovery.at = expiresAt
	tr.secretRecovery.timer = time.AfterFunc(delay, func() {
		tr.secretRecovery.mu.Lock()
		if tr.secretRecovery.stopped || tr.secretRecovery.generation != generation {
			tr.secretRecovery.mu.Unlock()
			return
		}
		tr.secretRecovery.timer = nil
		tr.secretRecovery.at = time.Time{}
		// Register before releasing the admission guard. Shutdown can now stop
		// new callbacks and still wait for this already-owned callback.
		tr.beginRuntimeOverrideSecretRecoveryLocked()
		tr.secretRecovery.mu.Unlock()
		defer tr.endRuntimeOverrideSecretRecovery()
		if !tr.holdsExecutionLeadership() {
			return
		}
		if err := tr.reconcileExpiredRuntimeOverrideSecretAttempts(time.Now().UTC()); err != nil {
			slog.Warn("runtime credential provisioning recovery retry failed", "error", err)
			tr.scheduleRuntimeOverrideSecretAttempt("", time.Now().Add(runtimeOverrideSecretRecoveryRetry))
			return
		}
		// The timer is coalesced. After clearing expired attempts, rescan once to
		// arm it for the next future lease.
		tr.scheduleRuntimeOverrideSecretAttemptReconciliation()
	})
	tr.secretRecovery.mu.Unlock()
}

func (tr *TestRunner) stopRuntimeOverrideSecretAttemptReconciliation() {
	if tr == nil {
		return
	}
	tr.secretRecovery.mu.Lock()
	tr.secretRecovery.stopped = true
	tr.secretRecovery.generation++
	if tr.secretRecovery.timer != nil {
		tr.secretRecovery.timer.Stop()
	}
	tr.secretRecovery.timer = nil
	tr.secretRecovery.at = time.Time{}
	tr.secretRecovery.mu.Unlock()
}

func (tr *TestRunner) reconcileExpiredRuntimeOverrideSecretAttempts(now time.Time) error {
	if tr == nil || tr.secretAttemptStore == nil {
		return nil
	}
	if !tr.beginRuntimeOverrideSecretRecovery() {
		return errRunnerShuttingDown
	}
	defer tr.endRuntimeOverrideSecretRecovery()
	if !tr.holdsExecutionLeadership() {
		return errNotExecutionLeader
	}
	attempts, err := tr.secretAttemptStore.ListRuntimeSecretProvisioningAttempts()
	if err != nil {
		return fmt.Errorf("list runtime credential provisioning journals: %w", err)
	}
	var reconcileErr error
	for _, attempt := range attempts {
		if attempt == nil || attempt.LeaseExpiresAt.After(now) {
			continue
		}
		if err := tr.reconcileRuntimeOverrideSecretAttempt(attempt); err != nil {
			reconcileErr = errors.Join(reconcileErr, fmt.Errorf("reconcile runtime credential provisioning attempt %s: %w", attempt.ID, err))
		}
	}
	return reconcileErr
}

func (tr *TestRunner) reconcileRuntimeOverrideSecretAttempt(record *storage.RuntimeSecretProvisioningAttempt) error {
	if record == nil {
		return nil
	}
	// An expired lease means a previous controller may have died, but it does
	// not prove a non-cooperative provider call in this process has stopped. Do
	// not race compensation against a late successful create; panic unwinding
	// removes this marker and leaves the durable timer armed for recovery.
	if tr.runtimeOverrideSecretAttemptActive(record.ID) {
		return errRuntimeSecretProvisioningActive
	}
	resourceType := strings.ToLower(strings.TrimSpace(record.ResourceType))
	if resourceType == "" {
		resourceType = "test"
	}
	attempt := runtimeOverrideSecretAttempt{
		ID:           record.ID,
		Actor:        record.Actor,
		ResourceType: resourceType,
		ResourceID:   strings.TrimSpace(record.TestID),
		AuditActor: testCreationActor{
			Type:        record.AuditActorType,
			Subject:     record.AuditActorSubject,
			DisplayName: record.AuditActorDisplayName,
		},
		Names:          append([]string(nil), record.SecretNames...),
		LeaseExpiresAt: record.LeaseExpiresAt,
	}
	for _, name := range record.SecretNames {
		attempt.Candidates = append(attempt.Candidates, runtimeOverrideSecretRollbackCandidate{
			Name:      name,
			Ambiguous: true,
		})
	}
	if resourceType == "scenario" {
		if tr.scenarioStore == nil {
			return fmt.Errorf("scenario persistence is unavailable")
		}
		scenario, scripts, err := tr.scenarioStore.GetScenario(strings.TrimSpace(record.TestID))
		if err == nil && scenario != nil {
			tr.mu.RLock()
			projectID, singleProject := tr.scenarioProjectIDFromScriptsLocked(scripts)
			tr.mu.RUnlock()
			if singleProject &&
				normalizeProjectID(projectID) == normalizeProjectID(record.ProjectID) &&
				scenarioRuntimeCommitFingerprint(scenario, scripts) == strings.TrimSpace(record.CommitFingerprint) {
				return tr.completeRuntimeOverrideSecretAttempt(attempt)
			}
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("check durable scenario for runtime credential recovery: %w", err)
		}
		return tr.rollbackRuntimeOverrideSecretAttempt(record.ProjectID, record.TestID, attempt, "expired_provisioning_attempt")
	}
	if resourceType != "test" {
		return fmt.Errorf("unsupported runtime credential provisioning resource type %q", resourceType)
	}
	if tr.testStore == nil {
		return fmt.Errorf("test persistence is unavailable")
	}
	test, err := tr.testStore.GetTest(strings.TrimSpace(record.TestID))
	intended := &storage.TestRecord{
		ID:                 strings.TrimSpace(record.TestID),
		MaterializationKey: strings.TrimSpace(record.MaterializationKey),
		RefKey:             strings.TrimSpace(record.RefKey),
	}
	legacyRestoreAttempt := strings.TrimSpace(record.AuditActorSubject) == "controller:runtime-restore"
	if !legacyRestoreAttempt && err == nil && test != nil &&
		normalizeProjectID(test.ProjectID) == normalizeProjectID(record.ProjectID) &&
		sameTestMaterialization(test, intended) {
		// Matching durable ownership proves this exact materialization committed
		// even if the process died before deleting its journal. Test ID and project
		// alone are insufficient because a colliding run may own the same ID.
		return tr.completeRuntimeOverrideSecretAttempt(runtimeOverrideSecretAttempt{ID: record.ID, Actor: record.Actor})
	}
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("check durable test for runtime credential recovery: %w", err)
	}
	return tr.rollbackRuntimeOverrideSecretAttempt(record.ProjectID, record.TestID, attempt, "expired_provisioning_attempt")
}

// beginRuntimeOverrideSecretRecovery is shared by synchronous scans and timer
// callbacks. No recovery store/provider work may escape this shutdown fence.
func (tr *TestRunner) beginRuntimeOverrideSecretRecovery() bool {
	if tr == nil {
		return false
	}
	tr.secretRecovery.mu.Lock()
	defer tr.secretRecovery.mu.Unlock()
	if tr.secretRecovery.stopped {
		return false
	}
	tr.beginRuntimeOverrideSecretRecoveryLocked()
	return true
}
func (tr *TestRunner) beginRuntimeOverrideSecretRecoveryLocked() {
	if tr.secretRecovery.running == 0 {
		tr.secretRecovery.drained = make(chan struct{})
	}
	tr.secretRecovery.running++
}
func (tr *TestRunner) endRuntimeOverrideSecretRecovery() {
	tr.secretRecovery.mu.Lock()
	tr.secretRecovery.running--
	if tr.secretRecovery.running == 0 {
		close(tr.secretRecovery.drained)
	}
	tr.secretRecovery.mu.Unlock()
}

// waitRuntimeOverrideSecretAttemptReconciliation never fabricates cancellation
// of a store/provider call which ignores context. A successful drain proves all
// admitted recovery work has returned; a deadline reports the retained work.
func (tr *TestRunner) waitRuntimeOverrideSecretAttemptReconciliation(ctx context.Context) error {
	if tr == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	tr.stopRuntimeOverrideSecretAttemptReconciliation()
	tr.secretRecovery.mu.Lock()
	running, drained := tr.secretRecovery.running, tr.secretRecovery.drained
	tr.secretRecovery.mu.Unlock()
	if running == 0 {
		return nil
	}
	select {
	case <-drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
