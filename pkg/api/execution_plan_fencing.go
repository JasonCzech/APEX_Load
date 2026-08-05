package api

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

var errExecutionPlanTestOwnership = errors.New("execution-plan test materialization ownership mismatch")
var errExecutionPlanRevisionConflict = errors.New("execution-plan revision conflict")

// executionLeaseTerm is the immutable durable fencing term captured by one
// execution-plan operation. Background work must retain this exact value; it
// may never adopt a later term merely because the same process reacquired
// leadership while an old goroutine was still alive.
type executionLeaseTerm struct {
	ownerID    string
	generation uint64
}

func (term executionLeaseTerm) valid() bool {
	return term.ownerID != "" && term.generation > 0
}

type executionLeaseTermReader interface {
	GetSchedulerLease(name string) (*storage.SchedulerLease, error)
}

func (tr *TestRunner) captureExecutionLeaseTerm() (executionLeaseTerm, error) {
	if tr == nil || !tr.durableExecutionFence {
		return executionLeaseTerm{}, nil
	}
	if tr.leadership == nil {
		return executionLeaseTerm{}, errNotExecutionLeader
	}
	ownerID, generation := tr.leadership.executionLeaseTerm()
	if ownerID == "" || generation == 0 {
		return executionLeaseTerm{}, errNotExecutionLeader
	}
	return executionLeaseTerm{ownerID: ownerID, generation: generation}, nil
}

func (tr *TestRunner) executionLeaseTermCurrent(term executionLeaseTerm) bool {
	if tr == nil || !tr.durableExecutionFence {
		return true
	}
	if !term.valid() || tr.currentExecutionFenceToken() != term.ownerID {
		return false
	}
	reader, ok := tr.scenarioStore.(executionLeaseTermReader)
	if !ok || reader == nil {
		return false
	}
	lease, err := reader.GetSchedulerLease(executionLeaseName)
	// executionLeaseTerm() already enforces the request-anchored local deadline.
	// Database lease timestamps are authored in the database clock domain and
	// must not be compared with this process's wall clock; the transactional
	// storage methods below perform the authoritative expiry check using DB time.
	return err == nil && lease != nil && lease.OwnerID == term.ownerID && lease.Generation == term.generation
}

// executionLeaseTermLocallyCurrent is the non-blocking half of the term check.
// It is safe while tr.mu is held because it consults only leadership's local
// epoch snapshot. Callers must perform executionLeaseTermCurrent immediately
// before taking tr.mu and rely on the durable transaction CAS at commit.
func (tr *TestRunner) executionLeaseTermLocallyCurrent(term executionLeaseTerm) bool {
	if tr == nil || !tr.durableExecutionFence {
		return true
	}
	if !term.valid() || tr.leadership == nil {
		return false
	}
	ownerID, generation := tr.leadership.executionLeaseTerm()
	return ownerID == term.ownerID && generation == term.generation
}

type leaseTermFencedExecutionPlanRunPersistence interface {
	CreateExecutionPlanRunIfAbsentAndLeaseTermHeld(run *storage.ExecutionPlanRun, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	CompareAndSwapExecutionPlanRunIfLeaseTermHeld(run *storage.ExecutionPlanRun, expectedRevision int64, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

type leaseTermFencedExecutionPlanSchedulePersistence interface {
	CreateExecutionPlanScheduleIfAbsentAndLeaseTermHeld(schedule *storage.ExecutionPlanSchedule, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	UpdateExecutionPlanScheduleIfRevisionAndLeaseTermHeld(schedule *storage.ExecutionPlanSchedule, expectedRevision int, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	DeleteExecutionPlanScheduleIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	CreateExecutionPlanScheduleOccurrenceIfAbsentAndLeaseTermHeld(occurrence *storage.ExecutionPlanScheduleOccurrence, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	CompareAndSwapExecutionPlanScheduleOccurrenceIfLeaseTermHeld(occurrence *storage.ExecutionPlanScheduleOccurrence, expectedRevision int64, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

type guardedExecutionPlanScheduleDeletePersistence interface {
	DeleteExecutionPlanScheduleGuarded(id string) (storage.ScheduleDeleteResult, error)
}

type leaseTermFencedExecutionPlanScheduleDeletePersistence interface {
	DeleteExecutionPlanScheduleGuardedIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (storage.ScheduleDeleteResult, error)
}

type leaseTermFencedExecutionPlanPersistence interface {
	SaveExecutionPlanIfLeaseTermHeld(plan *storage.ExecutionPlan, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	CreateExecutionPlanIfAbsentAndLeaseTermHeld(plan *storage.ExecutionPlan, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	CompareAndSwapExecutionPlanIfLeaseTermHeld(plan *storage.ExecutionPlan, expectedRevision int64, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	DeleteExecutionPlanIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	DeleteExecutionPlanGuardedIfLeaseTermHeld(id, leaseName, leaseOwnerID string, leaseGeneration uint64) (storage.ExecutionPlanDeleteResult, error)
}

type leaseTermFencedExecutionPlanApprovalPersistence interface {
	SaveExecutionPlanApprovalIfLeaseTermHeld(approval *storage.ExecutionPlanApproval, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	ClaimExecutionPlanApprovalIfLeaseTermHeld(id, runID string, updatedAt time.Time, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
	UpdateExecutionPlanApprovalResolutionIfLeaseTermHeld(id, toStatus, resolvedBy, reason string, resolvedAt time.Time, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

type leaseTermFencedExecutionPlanApprovalResolutionPersistence interface {
	UpdateExecutionPlanApprovalResolutionAndContentHashIfLeaseTermHeld(id, toStatus, resolvedBy, reason, contentHash string, resolvedAt time.Time, leaseName, leaseOwnerID string, leaseGeneration uint64) (bool, error)
}

type executionPlanApprovalResolutionPersistence interface {
	UpdateExecutionPlanApprovalResolutionAndContentHash(id, toStatus, resolvedBy, reason, contentHash string, resolvedAt time.Time) (bool, error)
}

type executionPlanMutationPersistence interface {
	SaveExecutionPlan(plan *storage.ExecutionPlan) error
	DeleteExecutionPlan(id string) error
}

type materializationFencedTestStatusPersistence interface {
	UpdateTestStatusIfActiveAndMaterializationKey(id, materializationKey, status string, endTime *time.Time, errStr string) (storage.TestStatusTransitionResult, error)
}

type leaseTermMaterializationFencedTestStatusPersistence interface {
	UpdateTestStatusIfActiveAndMaterializationKeyAndLeaseTermHeld(id, materializationKey, status string, endTime *time.Time, errStr, leaseName, leaseOwnerID string, leaseGeneration uint64) (storage.TestStatusTransitionResult, error)
}

type atomicExecutionPlanMutationPersistence interface {
	CreateExecutionPlanIfAbsent(plan *storage.ExecutionPlan) (bool, error)
	CompareAndSwapExecutionPlan(plan *storage.ExecutionPlan, expectedRevision int64) (bool, error)
}

type guardedExecutionPlanDeletePersistence interface {
	DeleteExecutionPlanGuarded(id string) (storage.ExecutionPlanDeleteResult, error)
}

func (tr *TestRunner) createExecutionPlanWithTerm(plan *storage.ExecutionPlan, term executionLeaseTerm) error {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return fmt.Errorf("execution-plan storage is not configured")
	}
	if !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	var created bool
	var err error
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanPersistence)
		if !ok {
			return fmt.Errorf("execution-plan storage does not support lease-term fencing")
		}
		created, err = store.CreateExecutionPlanIfAbsentAndLeaseTermHeld(plan, executionLeaseName, term.ownerID, term.generation)
	} else {
		store, ok := tr.execPlanScheduleStore.(atomicExecutionPlanMutationPersistence)
		if !ok {
			return fmt.Errorf("execution-plan storage does not support atomic creation")
		}
		created, err = store.CreateExecutionPlanIfAbsent(plan)
	}
	if err != nil {
		return err
	}
	if !created {
		if !tr.executionLeaseTermCurrent(term) {
			return errNotExecutionLeader
		}
		return errExecutionPlanRevisionConflict
	}
	return nil
}

func (tr *TestRunner) updateExecutionPlanWithTerm(plan *storage.ExecutionPlan, expectedRevision int64, term executionLeaseTerm) error {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return fmt.Errorf("execution-plan storage is not configured")
	}
	if expectedRevision <= 0 {
		return fmt.Errorf("execution-plan revision must be positive")
	}
	if !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	var updated bool
	var err error
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanPersistence)
		if !ok {
			return fmt.Errorf("execution-plan storage does not support lease-term fencing")
		}
		updated, err = store.CompareAndSwapExecutionPlanIfLeaseTermHeld(plan, expectedRevision, executionLeaseName, term.ownerID, term.generation)
	} else {
		store, ok := tr.execPlanScheduleStore.(atomicExecutionPlanMutationPersistence)
		if !ok {
			return fmt.Errorf("execution-plan storage does not support revision CAS")
		}
		updated, err = store.CompareAndSwapExecutionPlan(plan, expectedRevision)
	}
	if err != nil {
		return err
	}
	if !updated {
		if !tr.executionLeaseTermCurrent(term) {
			return errNotExecutionLeader
		}
		return errExecutionPlanRevisionConflict
	}
	return nil
}

func (tr *TestRunner) saveExecutionPlanWithTerm(plan *storage.ExecutionPlan, term executionLeaseTerm) error {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return fmt.Errorf("execution-plan storage is not configured")
	}
	if !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanPersistence)
		if !ok {
			return fmt.Errorf("execution-plan storage does not support lease-term fencing")
		}
		saved, err := store.SaveExecutionPlanIfLeaseTermHeld(plan, executionLeaseName, term.ownerID, term.generation)
		if err != nil {
			return err
		}
		if !saved {
			return errNotExecutionLeader
		}
		return nil
	}
	store, ok := tr.execPlanScheduleStore.(executionPlanMutationPersistence)
	if !ok {
		return fmt.Errorf("execution-plan storage does not support plan mutations")
	}
	return store.SaveExecutionPlan(plan)
}

func (tr *TestRunner) deleteExecutionPlanWithTerm(id string, term executionLeaseTerm) (storage.ExecutionPlanDeleteResult, error) {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return "", fmt.Errorf("execution-plan storage is not configured")
	}
	if !tr.executionLeaseTermCurrent(term) {
		return "", errNotExecutionLeader
	}
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanPersistence)
		if !ok {
			return "", fmt.Errorf("execution-plan storage does not support lease-term fencing")
		}
		result, err := store.DeleteExecutionPlanGuardedIfLeaseTermHeld(id, executionLeaseName, term.ownerID, term.generation)
		if err == nil && result == "" && !tr.executionLeaseTermCurrent(term) {
			return "", errNotExecutionLeader
		}
		return result, err
	}
	store, ok := tr.execPlanScheduleStore.(guardedExecutionPlanDeletePersistence)
	if !ok {
		return "", fmt.Errorf("execution-plan storage does not support guarded plan deletion")
	}
	return store.DeleteExecutionPlanGuarded(id)
}

func (tr *TestRunner) saveExecutionPlanApprovalWithTerm(approval *storage.ExecutionPlanApproval, term executionLeaseTerm) error {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return fmt.Errorf("execution-plan approval storage is not configured")
	}
	if !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanApprovalPersistence)
		if !ok {
			return fmt.Errorf("execution-plan approval storage does not support lease-term fencing")
		}
		saved, err := store.SaveExecutionPlanApprovalIfLeaseTermHeld(approval, executionLeaseName, term.ownerID, term.generation)
		if err != nil {
			return err
		}
		if !saved {
			return errNotExecutionLeader
		}
		return nil
	}
	return tr.execPlanScheduleStore.SaveExecutionPlanApproval(approval)
}

func (tr *TestRunner) claimExecutionPlanApprovalWithTerm(id, runID string, updatedAt time.Time, term executionLeaseTerm) (bool, error) {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return false, fmt.Errorf("execution-plan approval storage is not configured")
	}
	if !tr.executionLeaseTermCurrent(term) {
		return false, errNotExecutionLeader
	}
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanApprovalPersistence)
		if !ok {
			return false, fmt.Errorf("execution-plan approval storage does not support lease-term fencing")
		}
		claimed, err := store.ClaimExecutionPlanApprovalIfLeaseTermHeld(id, runID, updatedAt, executionLeaseName, term.ownerID, term.generation)
		if err == nil && !claimed && !tr.executionLeaseTermCurrent(term) {
			return false, errNotExecutionLeader
		}
		return claimed, err
	}
	return tr.execPlanScheduleStore.ClaimExecutionPlanApproval(id, runID, updatedAt)
}

func (tr *TestRunner) resolveExecutionPlanApprovalWithTerm(id, toStatus, resolvedBy, reason, contentHash string, resolvedAt time.Time, term executionLeaseTerm) (bool, error) {
	if tr == nil || tr.execPlanScheduleStore == nil {
		return false, fmt.Errorf("execution-plan approval storage is not configured")
	}
	if !tr.executionLeaseTermCurrent(term) {
		return false, errNotExecutionLeader
	}
	if tr.durableExecutionFence {
		store, ok := tr.execPlanScheduleStore.(leaseTermFencedExecutionPlanApprovalResolutionPersistence)
		if !ok {
			return false, fmt.Errorf("execution-plan approval storage does not support lease-term fencing")
		}
		updated, err := store.UpdateExecutionPlanApprovalResolutionAndContentHashIfLeaseTermHeld(id, toStatus, resolvedBy, reason, contentHash, resolvedAt, executionLeaseName, term.ownerID, term.generation)
		if err == nil && !updated && !tr.executionLeaseTermCurrent(term) {
			return false, errNotExecutionLeader
		}
		return updated, err
	}
	if store, ok := tr.execPlanScheduleStore.(executionPlanApprovalResolutionPersistence); ok {
		return store.UpdateExecutionPlanApprovalResolutionAndContentHash(id, toStatus, resolvedBy, reason, contentHash, resolvedAt)
	}
	return tr.execPlanScheduleStore.UpdateExecutionPlanApprovalResolution(id, toStatus, resolvedBy, reason, resolvedAt)
}

func (tr *TestRunner) failExecutionPlanStagedTestWithTerm(testID, detail string, term executionLeaseTerm, expectedMaterializationKeys ...string) error {
	if testID == "" {
		return nil
	}
	if !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	expectedKey := ""
	if len(expectedMaterializationKeys) > 0 {
		expectedKey = strings.TrimSpace(expectedMaterializationKeys[0])
	}
	if expectedKey == "" {
		tr.mu.RLock()
		if current := tr.tests[testID]; current != nil {
			expectedKey = strings.TrimSpace(current.MaterializationKey)
		}
		tr.mu.RUnlock()
	}
	if expectedKey == "" {
		return fmt.Errorf("%w: missing ownership marker for test %s", errExecutionPlanTestOwnership, testID)
	}
	now := time.Now().UTC()
	var durable *storage.TestRecord
	if tr.testStore != nil {
		var result storage.TestStatusTransitionResult
		var transitionErr error
		if tr.durableExecutionFence {
			store, ok := tr.testStore.(leaseTermMaterializationFencedTestStatusPersistence)
			if !ok {
				return fmt.Errorf("execution-plan test storage does not support lease-term materialization fencing")
			}
			transitionErr = retryDurableTransition(func() error {
				var err error
				result, err = store.UpdateTestStatusIfActiveAndMaterializationKeyAndLeaseTermHeld(
					testID, expectedKey, "FAILED", &now, detail, executionLeaseName, term.ownerID, term.generation,
				)
				return err
			})
		} else {
			store, ok := tr.testStore.(materializationFencedTestStatusPersistence)
			if !ok {
				return fmt.Errorf("execution-plan test storage does not support materialization fencing")
			}
			transitionErr = retryDurableTransition(func() error {
				var err error
				result, err = store.UpdateTestStatusIfActiveAndMaterializationKey(testID, expectedKey, "FAILED", &now, detail)
				return err
			})
		}
		if transitionErr != nil {
			return transitionErr
		}
		switch result {
		case storage.TestStatusTransitionUpdated, storage.TestStatusTransitionUnchanged:
		case storage.TestStatusTransitionMissing:
			return nil
		default:
			current, loadErr := tr.testStore.GetTest(testID)
			if errors.Is(loadErr, sql.ErrNoRows) || (loadErr == nil && current == nil) {
				return nil
			}
			if loadErr != nil {
				return loadErr
			}
			if strings.TrimSpace(current.MaterializationKey) != expectedKey {
				return fmt.Errorf("%w: test %s is not owned by this plan entry", errExecutionPlanTestOwnership, testID)
			}
			if isTerminalTestStatus(current.Status) {
				return nil
			}
			if !tr.executionLeaseTermCurrent(term) {
				return errNotExecutionLeader
			}
			return fmt.Errorf("%w: test %s has a newer state", errStoredTestTransitionFenced, testID)
		}
		var loadErr error
		durable, loadErr = tr.testStore.GetTest(testID)
		if errors.Is(loadErr, sql.ErrNoRows) || (loadErr == nil && durable == nil) {
			return nil
		}
		if loadErr != nil {
			return loadErr
		}
		if strings.TrimSpace(durable.MaterializationKey) != expectedKey {
			return fmt.Errorf("%w: test %s changed ownership after terminalization", errExecutionPlanTestOwnership, testID)
		}
	}
	if !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	tr.mu.Lock()
	slotID := ""
	if test := tr.tests[testID]; test != nil {
		ownedCache := strings.TrimSpace(test.MaterializationKey) == expectedKey && (durable == nil || sameStoredTestIdentity(test, durable))
		if durable == nil && !ownedCache {
			tr.mu.Unlock()
			return fmt.Errorf("%w: cached test %s is not owned by this plan entry", errExecutionPlanTestOwnership, testID)
		}
		if !ownedCache {
			// A follower/previous epoch may still retain a stale object for this ID.
			// The durable owned row is already terminal; never cancel or rewrite the
			// unrelated local handle while it awaits cache reconciliation.
		} else if test.executionFence != "" && test.executionFence != term.ownerID {
			tr.mu.Unlock()
			return errNotExecutionLeader
		} else {
			if test.cancel != nil {
				test.cancel()
			}
			test.Status = "FAILED"
			test.Error = detail
			test.EndedAt = &now
			test.PendingSince = nil
			test.PendingReason = ""
			test.QueuePosition = 0
			compactTerminalManagedTestLocked(test)
			if test.TimeslotID != nil {
				slotID = strings.TrimSpace(*test.TimeslotID)
			}
		}
	}
	tr.mu.Unlock()
	if slotID == "" && durable != nil && durable.TimeslotID != nil {
		slotID = strings.TrimSpace(*durable.TimeslotID)
	}
	if slotID == "" {
		return nil
	}
	releaseErr := tr.releaseCapacityReservationBinding(testID, slotID)
	if releaseErr != nil {
		tr.enqueueCapacityReservationReleaseRetry(testID, slotID)
	}
	return releaseErr
}

func (tr *TestRunner) startExecutionPlanTestWithTerm(testID string, term executionLeaseTerm) error {
	if !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	if tr.beforeExecutionPlanStartMutation != nil {
		tr.beforeExecutionPlanStartMutation()
	}
	// Thread the captured term through the actual StartTest state mutation. The
	// final validation and fence claim happen while tr.mu is held, so leadership
	// loss followed by same-process reacquisition in the window above cannot let
	// this old goroutine adopt the successor term.
	opts := StartTestOptions{}
	tr.mu.RLock()
	if test := tr.tests[testID]; test != nil && strings.TrimSpace(test.CreatedBySubject) != "" {
		opts.actor = testCreationActor{Type: "system", Subject: test.CreatedBySubject, DisplayName: test.CreatedByDisplayName}
	}
	tr.mu.RUnlock()
	if err := tr.startTestWithOptionsAndExpectedTerm(testID, opts, &term); err != nil {
		return err
	}
	if !tr.executionLeaseTermCurrent(term) {
		return errNotExecutionLeader
	}
	return nil
}
