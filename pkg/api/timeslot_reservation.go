package api

// Capacity timeslot binding + reservation-aware admission. An approved, bound
// timeslot is treated as a transient, time-windowed per-project generator
// reservation: its reserved generators are fed into the same admission math that
// already protects static (settings-based) reservations, so other tenants are
// blocked from the capacity it holds during its window. With no bound timeslots
// active, the contribution is zero and admission behaves exactly as before.

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/storage"
)

// loadCapacityTimeslotEvidence performs the durable half of create-time
// timeslot validation. Callers load it before taking tr.mu; the authoritative
// validation and both durable writes still happen later in
// saveNewManagedTestRecord's single transaction.
func (tr *TestRunner) loadCapacityTimeslotEvidence(timeslotID string) (*storage.CapacityReservation, error) {
	if tr.reservationStore == nil {
		return nil, fmt.Errorf("capacity timeslots are not available")
	}
	reservation, err := tr.reservationStore.GetCapacityReservation(timeslotID)
	if err != nil || reservation == nil {
		return nil, fmt.Errorf("timeslot %s not found", timeslotID)
	}
	return reservation, nil
}

// validateAndApplyTimeslotEvidenceLocked validates immutable reservation
// evidence against the candidate and records the requested slot. Caller holds
// tr.mu, but this function performs no storage or network I/O.
func validateAndApplyTimeslotEvidenceLocked(reservation *storage.CapacityReservation, test *ManagedTest) error {
	if reservation == nil {
		return fmt.Errorf("timeslot not found")
	}
	if reservation.ApprovalState != "approved" {
		return fmt.Errorf("timeslot must be approved before it can be bound")
	}
	if reservation.BoundTestID != nil && strings.TrimSpace(*reservation.BoundTestID) != "" {
		return fmt.Errorf("timeslot is no longer bindable (already bound or not approved)")
	}
	if normalizeProjectID(reservation.ProjectID) != normalizeProjectID(test.ProjectID) {
		return fmt.Errorf("timeslot belongs to a different project than the test")
	}
	start := time.Now()
	if test.ScheduledStartAt != nil && !test.ScheduledStartAt.IsZero() {
		start = *test.ScheduledStartAt
	}
	window := launchWindowForTest(test, start)
	if err := reservationFitError(reservation, window.StartAt, window.EndAt, test.TotalVUsers); err != nil {
		return err
	}
	test.CapacityWindowStartAt = cloneTimePtr(&window.StartAt)
	test.CapacityWindowEndAt = cloneTimePtr(&window.EndAt)
	test.TimeslotID = &reservation.ID
	return nil
}

func capacityBindingResultError(result storage.CapacityBindingResult) error {
	switch result {
	case storage.CapacityBindingBound:
		return nil
	case storage.CapacityBindingFenced:
		return errNotExecutionLeader
	case storage.CapacityBindingReservationMissing:
		return fmt.Errorf("timeslot not found")
	case storage.CapacityBindingTestMissing:
		return fmt.Errorf("test not found")
	case storage.CapacityBindingReservationUnapproved:
		return fmt.Errorf("timeslot must be approved before it can be bound")
	case storage.CapacityBindingReservationBound:
		return fmt.Errorf("timeslot is no longer bindable (already bound or not approved)")
	case storage.CapacityBindingTestBound:
		return fmt.Errorf("test is already bound to a capacity timeslot")
	case storage.CapacityBindingProjectMismatch:
		return fmt.Errorf("test belongs to a different project than the timeslot")
	case storage.CapacityBindingWindowMismatch:
		return fmt.Errorf("the run window does not fit within the reserved timeslot")
	case storage.CapacityBindingCapacityExceeded:
		return fmt.Errorf("the run requests more VUsers than the timeslot allows (the slot reserves only its approved VUser capacity)")
	case storage.CapacityBindingTestNotBindable:
		return fmt.Errorf("test status cannot be bound to a capacity timeslot")
	case storage.CapacityBindingTestExists:
		return fmt.Errorf("test already exists")
	default:
		return fmt.Errorf("capacity timeslot binding failed (%s)", result)
	}
}

// saveNewManagedTestRecord is the create-only persistence path. Tests without a
// timeslot use the normal fenced save; tests with a timeslot require a unified
// store capable of committing the test row and reservation binding together.
func (tr *TestRunner) saveNewManagedTestRecord(test *ManagedTest) error {
	leaseTerm, err := tr.captureExecutionLeaseTerm()
	if err != nil {
		return err
	}
	return tr.saveNewManagedTestRecordWithTerm(test, leaseTerm)
}

var errTestIdentityCollision = errors.New("test identity is already owned by another materialization")
var errDurableActiveTestRequiresReconciliation = errors.New("durable active test requires leader reconciliation")

func hasExpectedTestParentRevision(record *storage.TestRecord) bool {
	return record != nil && (!record.ExpectedProjectUpdatedAt.IsZero() ||
		!record.ExpectedApplicationUpdatedAt.IsZero() ||
		!record.ExpectedReleaseUpdatedAt.IsZero())
}

// adoptDurableManagedTest replaces a rebuilt retry candidate with the exact row
// that already owns its deterministic materialization. RefKey and immutable
// group/runtime snapshots must come from storage after a prior commit wins.
func (tr *TestRunner) adoptDurableManagedTest(candidate *ManagedTest, record *storage.TestRecord) error {
	if candidate == nil || record == nil {
		return nil
	}
	executionFence := candidate.executionFence
	adopted := managedTestFromRecord(record)
	switch strings.ToUpper(strings.TrimSpace(adopted.Status)) {
	case "STARTING", "INITIALIZING", "RUNNING", "PAUSED", "STOPPING", "SHAKEOUT":
		// A cache-miss retry cannot safely attach a durable active row without its
		// live execution handles. Leave it to the normal HA reload path, which
		// claims the current execution fence and terminalizes interrupted work.
		return fmt.Errorf("%w: test %s is %s", errDurableActiveTestRequiresReconciliation, adopted.ID, adopted.Status)
	}
	adopted.executionFence = executionFence
	adopted.creationAdopted = true
	if isTerminalTestStatus(adopted.Status) {
		trimTerminalManagedTestRuntime(adopted)
	} else if err := tr.restoreManagedTestRuntimeAndRepair(adopted); err != nil {
		adopted.runtimeHydrationError = err.Error()
		slog.Error("failed to restore adopted durable test runtime", "test_id", adopted.ID, "error", err)
	}
	*candidate = *adopted
	return nil
}

func (tr *TestRunner) saveNewManagedTestRecordWithTerm(test *ManagedTest, leaseTerm executionLeaseTerm) error {
	if test == nil {
		return fmt.Errorf("test is required")
	}
	if !tr.executionLeaseTermCurrent(leaseTerm) {
		return errNotExecutionLeader
	}
	if test.TimeslotID == nil || strings.TrimSpace(*test.TimeslotID) == "" {
		if tr.testStore == nil {
			return nil
		}
		record := buildManagedTestRecord(test)
		if store, ok := tr.testStore.(createOnlyTestPersistence); ok {
			var lastErr error
			for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
				var created bool
				if tr.durableExecutionFence {
					termStore, termOK := tr.testStore.(leaseTermCreateOnlyTestPersistence)
					if !termOK {
						return fmt.Errorf("test persistence does not support create-only lease-term fencing")
					}
					created, lastErr = termStore.CreateTestIfAbsentIfLeaseTermHeld(record, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
				} else {
					created, lastErr = store.CreateTestIfAbsent(record)
				}
				if lastErr == nil && created {
					return nil
				}
				stored, lookupErr := tr.testStore.GetTest(test.ID)
				if lookupErr == nil && sameTestMaterialization(stored, record) {
					return tr.adoptDurableManagedTest(test, stored)
				}
				if lookupErr == nil && stored != nil {
					return fmt.Errorf("%w: test %s", errTestIdentityCollision, test.ID)
				}
				if lastErr == nil && errors.Is(lookupErr, sql.ErrNoRows) && !tr.executionLeaseTermCurrent(leaseTerm) {
					return errNotExecutionLeader
				}
				if lastErr == nil {
					lastErr = lookupErr
				}
				if attempt+1 < durableTransitionAttempts {
					time.Sleep(durableTransitionRetryDelay)
				}
			}
			return lastErr
		}
		if tr.durableExecutionFence {
			return fmt.Errorf("test persistence does not support atomic create-only insertion")
		}
		// A full-row SaveTest cannot atomically validate off-lock parent evidence.
		// Production SQLite/PostgreSQL implement the create-only contract above;
		// fail closed rather than silently accepting stale quota/release authority.
		if hasExpectedTestParentRevision(record) {
			return fmt.Errorf("test persistence does not support atomic parent-revision validation")
		}
		// Lightweight single-controller stores retain a compatibility path. A
		// dedicated mutex replaces the old (and now invalid) assumption that tr.mu
		// covered storage I/O, and the lookup prevents a concurrent same-ID save
		// from overwriting another materialization.
		tr.compatCreateMu.Lock()
		defer tr.compatCreateMu.Unlock()
		stored, lookupErr := tr.testStore.GetTest(record.ID)
		if lookupErr == nil && stored != nil {
			if sameTestMaterialization(stored, record) {
				return tr.adoptDurableManagedTest(test, stored)
			}
			return fmt.Errorf("%w: test %s", errTestIdentityCollision, record.ID)
		}
		if lookupErr != nil && !errors.Is(lookupErr, sql.ErrNoRows) {
			return fmt.Errorf("check compatibility test identity: %w", lookupErr)
		}
		saveErr := tr.testStore.SaveTest(record)
		if saveErr == nil {
			return nil
		}
		// A compatibility store may commit and lose its response. Resolve that
		// ambiguity before the caller compensates secrets for a row that exists.
		stored, lookupErr = tr.testStore.GetTest(record.ID)
		if lookupErr == nil && sameTestMaterialization(stored, record) {
			return tr.adoptDurableManagedTest(test, stored)
		}
		if lookupErr == nil && stored != nil {
			return fmt.Errorf("%w: test %s", errTestIdentityCollision, record.ID)
		}
		return errors.Join(saveErr, lookupErr)
	}
	store, ok := tr.reservationStore.(atomicCapacityBindingPersistence)
	if !ok || tr.testStore == nil || !sameStoreInstance(store, tr.testStore) {
		return fmt.Errorf("capacity reservation persistence does not support atomic test creation")
	}
	record := buildManagedTestRecord(test)
	now := time.Now().UTC()
	start := now
	if test.ScheduledStartAt != nil && test.ScheduledStartAt.After(start) {
		start = test.ScheduledStartAt.UTC()
	}
	window := launchWindowForTest(test, start)
	test.CapacityWindowStartAt = cloneTimePtr(&window.StartAt)
	test.CapacityWindowEndAt = cloneTimePtr(&window.EndAt)
	record.CapacityWindowStartAt = cloneTimePtr(&window.StartAt)
	record.CapacityWindowEndAt = cloneTimePtr(&window.EndAt)
	slotID := strings.TrimSpace(*test.TimeslotID)
	var lastErr error
	for attempt := 0; attempt < durableTransitionAttempts; attempt++ {
		var result storage.CapacityBindingResult
		if tr.durableExecutionFence {
			termStore, termOK := tr.reservationStore.(leaseTermAtomicCapacityBindingPersistence)
			if !termOK {
				return fmt.Errorf("capacity reservation persistence does not support create lease-term fencing")
			}
			result, lastErr = termStore.CreateTestAndBindCapacityReservationIfLeaseTermHeld(record, slotID, now, executionLeaseName, leaseTerm.ownerID, leaseTerm.generation)
		} else {
			result, lastErr = store.CreateTestAndBindCapacityReservation(record, slotID, now)
		}
		if lastErr == nil && (result == storage.CapacityBindingBound || result == storage.CapacityBindingTestExists) {
			stored, materialized, verifyErr := tr.capacityBindingMaterialized(record, slotID)
			if materialized {
				if stored != nil && (result == storage.CapacityBindingTestExists || stored.RefKey != record.RefKey) {
					if err := tr.adoptDurableManagedTest(test, stored); err != nil {
						return err
					}
				}
				return nil
			}
			if errors.Is(verifyErr, errTestIdentityCollision) {
				return verifyErr
			}
			lastErr = verifyErr
			if lastErr == nil {
				lastErr = capacityBindingResultError(result)
			}
		} else if lastErr == nil {
			lastErr = capacityBindingResultError(result)
		}
		if attempt+1 < durableTransitionAttempts {
			time.Sleep(durableTransitionRetryDelay)
		}
	}
	return fmt.Errorf("bind timeslot and persist test: %w", lastErr)
}

// capacityBindingMaterialized verifies the full durable outcome after either a
// successful create-and-bind result or an idempotent retry that finds the test
// already present. This makes a retry safe when the original transaction
// committed but its response was lost: the durable test identity and the
// reservation ownership must both match before creation is treated as complete.
func (tr *TestRunner) capacityBindingMaterialized(intended *storage.TestRecord, slotID string) (*storage.TestRecord, bool, error) {
	stored, storedErr := tr.testStore.GetTest(intended.ID)
	reservation, reservationErr := tr.reservationStore.GetCapacityReservation(slotID)
	if storedErr == nil && stored != nil && !sameTestMaterialization(stored, intended) {
		return stored, false, fmt.Errorf("%w: test %s", errTestIdentityCollision, intended.ID)
	}
	if storedErr != nil || reservationErr != nil {
		return stored, false, errors.Join(storedErr, reservationErr)
	}
	if stored == nil || reservation == nil || !sameTestMaterialization(stored, intended) ||
		reservation.BoundTestID == nil || strings.TrimSpace(*reservation.BoundTestID) != intended.ID {
		return stored, false, fmt.Errorf("test %s and timeslot %s were not durably materialized together", intended.ID, slotID)
	}
	return stored, true, nil
}

func sameTestMaterialization(stored, intended *storage.TestRecord) bool {
	if stored == nil || intended == nil || stored.ID != intended.ID {
		return false
	}
	if normalizeProjectID(stored.ProjectID) != normalizeProjectID(intended.ProjectID) {
		return false
	}
	if key := strings.TrimSpace(intended.MaterializationKey); key != "" {
		return strings.TrimSpace(stored.MaterializationKey) == key
	}
	return strings.TrimSpace(intended.RefKey) != "" && strings.TrimSpace(stored.RefKey) == strings.TrimSpace(intended.RefKey)
}

// BindCapacityTimeslot is the standalone bind operation used by the REST
// endpoint. It serializes with local test deletion through the per-test archive
// commit fence and delegates authoritative test/reservation locks and the lease
// check to one store transaction without holding tr.mu across storage I/O.
func (tr *TestRunner) BindCapacityTimeslot(timeslotID, requestedTestID string) (*storage.CapacityReservation, storage.CapacityBindingResult, error) {
	if !tr.isExecutionLeader() {
		return nil, storage.CapacityBindingFenced, errNotExecutionLeader
	}
	token := tr.currentExecutionFenceToken()
	if tr.durableExecutionFence && token == "" {
		return nil, storage.CapacityBindingFenced, errNotExecutionLeader
	}
	store, ok := tr.reservationStore.(atomicCapacityBindingPersistence)
	if !ok || tr.testStore == nil || !sameStoreInstance(store, tr.testStore) {
		return nil, storage.CapacityBindingFenced, fmt.Errorf("capacity reservation persistence does not support atomic binding")
	}
	windowStore, ok := tr.reservationStore.(fullWindowAtomicCapacityBindingPersistence)
	if !ok {
		return nil, storage.CapacityBindingFenced, fmt.Errorf("capacity reservation persistence does not support full-window atomic binding")
	}
	timeslotID = strings.TrimSpace(timeslotID)
	requestedTestID = strings.TrimSpace(requestedTestID)

	tr.mu.RLock()
	resolvedID := tr.resolveRefKeyLocked(requestedTestID)
	tr.mu.RUnlock()
	// Test deletion already owns this per-test fence across its durable metadata
	// transaction. Reusing it prevents a successful bind from rehydrating a test
	// that was deleted while the global runner mutex was deliberately released.
	unlockArchiveCommit := tr.lockArchiveCommitFence(resolvedID)
	defer func() { unlockArchiveCommit() }()

	record, err := tr.testStore.GetTest(resolvedID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.CapacityBindingTestMissing, nil
		}
		return nil, storage.CapacityBindingFenced, err
	}
	if record == nil {
		return nil, storage.CapacityBindingTestMissing, nil
	}
	canonicalID := strings.TrimSpace(record.ID)
	if canonicalID == "" {
		return nil, storage.CapacityBindingFenced, fmt.Errorf("capacity binding test has no durable identity")
	}
	if canonicalID != resolvedID {
		// GetTest accepts either an ID or ref key. A ref created by another
		// replica may not be present in this runner's in-memory ref index, so the
		// first fence can be keyed by the authored ref. Hand off to the canonical
		// ID and re-read under that fence before any mutation; deletion uses this
		// same canonical key.
		unlockArchiveCommit()
		resolvedID = canonicalID
		unlockArchiveCommit = tr.lockArchiveCommitFence(resolvedID)
		record, err = tr.testStore.GetTest(resolvedID)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, storage.CapacityBindingTestMissing, nil
			}
			return nil, storage.CapacityBindingFenced, err
		}
		if record == nil || strings.TrimSpace(record.ID) != resolvedID {
			return nil, storage.CapacityBindingTestMissing, nil
		}
	} else {
		resolvedID = canonicalID
	}
	now := time.Now().UTC()
	managed := managedTestFromRecord(record)
	start := now
	if managed.ScheduledStartAt != nil && managed.ScheduledStartAt.After(start) {
		start = managed.ScheduledStartAt.UTC()
	}
	window := launchWindowForTest(managed, start)
	var result storage.CapacityBindingResult
	if tr.durableExecutionFence {
		result, err = windowStore.BindCapacityReservationToTestWithWindowIfLeaseHeld(timeslotID, resolvedID, now, window.StartAt, window.EndAt, executionLeaseName, token)
	} else {
		result, err = windowStore.BindCapacityReservationToTestWithWindow(timeslotID, resolvedID, now, window.StartAt, window.EndAt)
	}
	if err != nil || result != storage.CapacityBindingBound {
		if err == nil && result == storage.CapacityBindingFenced {
			err = errNotExecutionLeader
		}
		return nil, result, err
	}
	reservation, err := tr.reservationStore.GetCapacityReservation(timeslotID)
	if err != nil {
		return nil, storage.CapacityBindingBound, err
	}
	refreshedRecord, err := tr.testStore.GetTest(resolvedID)
	if err != nil {
		return reservation, storage.CapacityBindingBound, err
	}
	if refreshedRecord == nil || refreshedRecord.TimeslotID == nil || strings.TrimSpace(*refreshedRecord.TimeslotID) != timeslotID ||
		reservation == nil || reservation.BoundTestID == nil || strings.TrimSpace(*reservation.BoundTestID) != resolvedID {
		return reservation, storage.CapacityBindingBound, fmt.Errorf("test %s and timeslot %s were not durably bound together", resolvedID, timeslotID)
	}
	// A test created by another replica may not exist in this runner's cache.
	// Rebuild its process-private engine state before publishing it: installing a
	// bare durable projection would make the now-bound queued test impossible to
	// start until a later HA refresh happened to repair it. Hydration may consult
	// durable scripts or persist a narrow legacy snapshot repair, so it must stay
	// outside tr.mu.
	hydrated := managedTestFromRecord(refreshedRecord)
	if isTerminalTestStatus(hydrated.Status) {
		trimTerminalManagedTestRuntime(hydrated)
	} else if err := tr.restoreManagedTestRuntimeAndRepair(hydrated); err != nil {
		hydrated.runtimeHydrationError = err.Error()
		slog.Error("failed to restore timeslot-bound test runtime", "test_id", hydrated.ID, "error", err)
	}
	tr.mu.Lock()
	if current := tr.tests[resolvedID]; current != nil {
		// The transaction bound the durable identity loaded above. A peer can
		// delete and recreate a terminal test with the same requested ID while
		// this replica still has the old object cached. Never project the new
		// row's binding onto that stale runtime identity.
		if sameStoredTestIdentity(current, refreshedRecord) {
			current.TimeslotID = cloneStringPtr(refreshedRecord.TimeslotID)
			current.CapacityWindowStartAt = cloneTimePtr(refreshedRecord.CapacityWindowStartAt)
			current.CapacityWindowEndAt = cloneTimePtr(refreshedRecord.CapacityWindowEndAt)
		}
	} else {
		tr.tests[resolvedID] = hydrated
		tr.observeAndIndexRefKeyLocked(hydrated.RefKey, hydrated.ID)
	}
	tr.mu.Unlock()
	return reservation, storage.CapacityBindingBound, nil
}

func (tr *TestRunner) repairCapacityReservationBindings() error {
	if tr == nil || tr.reservationStore == nil {
		return nil
	}
	store, ok := tr.reservationStore.(atomicCapacityBindingPersistence)
	if !ok || tr.testStore == nil || !sameStoreInstance(store, tr.testStore) {
		return nil
	}
	var (
		changed int64
		err     error
	)
	if tr.durableExecutionFence {
		token := tr.currentExecutionFenceToken()
		if token == "" {
			return errNotExecutionLeader
		}
		var held bool
		held, changed, err = store.RepairCapacityReservationBindingsIfLeaseHeld(executionLeaseName, token)
		if err == nil && !held {
			return errNotExecutionLeader
		}
	} else {
		changed, err = store.RepairCapacityReservationBindings()
	}
	if err != nil {
		return err
	}
	if changed > 0 {
		slog.Info("repaired legacy capacity timeslot bindings", "changed_rows", changed)
	}
	return nil
}

func capacityBindingMatchesRecord(test *ManagedTest, record *storage.TestRecord) bool {
	if test == nil || record == nil {
		return true
	}
	return strings.TrimSpace(trimPtr(test.TimeslotID)) == strings.TrimSpace(trimPtr(record.TimeslotID)) &&
		sameOptionalTime(test.CapacityWindowStartAt, record.CapacityWindowStartAt) &&
		sameOptionalTime(test.CapacityWindowEndAt, record.CapacityWindowEndAt)
}

// applyStoredCapacityBindingLocked refreshes the three reciprocal binding
// fields whose source of truth is the durable tests row. Caller holds tr.mu.
func applyStoredCapacityBindingLocked(test *ManagedTest, record *storage.TestRecord) bool {
	if test == nil || record == nil || capacityBindingMatchesRecord(test, record) {
		return false
	}
	test.TimeslotID = cloneStringPtr(record.TimeslotID)
	test.CapacityWindowStartAt = cloneTimePtr(record.CapacityWindowStartAt)
	test.CapacityWindowEndAt = cloneTimePtr(record.CapacityWindowEndAt)
	return true
}

func (tr *TestRunner) broadcastCapacityBindingUpdated(test *ManagedTest) {
	if tr == nil || tr.hub == nil || test == nil {
		return
	}
	tr.hub.BroadcastScopedJSON(test.ID, normalizeProjectID(test.ProjectID), map[string]any{
		"type":        "test_capacity_binding_updated",
		"test_id":     test.ID,
		"project_id":  normalizeProjectID(test.ProjectID),
		"test_name":   test.Name,
		"status":      test.Status,
		"timeslot_id": cloneStringPtr(test.TimeslotID),
		"timestamp":   time.Now().UTC(),
	})
}

// clearLiveCapacityReservationBinding is the local fast path after a storage
// transaction atomically clears reservation.bound_test_id and tests.timeslot_id.
// Cross-replica start/read paths independently refresh from the durable row.
func (tr *TestRunner) clearLiveCapacityReservationBinding(testID, slotID string) bool {
	if tr == nil {
		return false
	}
	testID = strings.TrimSpace(testID)
	slotID = strings.TrimSpace(slotID)
	if testID == "" || slotID == "" {
		return false
	}
	tr.mu.Lock()
	test := tr.tests[testID]
	if test == nil || strings.TrimSpace(trimPtr(test.TimeslotID)) != slotID {
		tr.mu.Unlock()
		return false
	}
	test.TimeslotID = nil
	test.CapacityWindowStartAt = nil
	test.CapacityWindowEndAt = nil
	snapshot := cloneManagedTestSummaryForReadLocked(test)
	tr.mu.Unlock()
	tr.broadcastCapacityBindingUpdated(snapshot)
	return true
}

// refreshTestCapacityBindingFromStore closes the HA invalidation gap at every
// execution boundary. A reject/edit may be served by another replica; querying
// the reciprocal durable test row prevents this controller from launching or
// serializing a stale timeslot pointer.
func (tr *TestRunner) refreshTestCapacityBindingFromStore(id string) error {
	if tr == nil || tr.testStore == nil || strings.TrimSpace(id) == "" {
		return nil
	}
	id = strings.TrimSpace(id)
	tr.mu.RLock()
	candidate := tr.tests[id]
	tr.mu.RUnlock()
	record, err := tr.testStore.GetTest(id)
	if err != nil {
		refreshErr := fmt.Errorf("refresh test capacity binding: %w", err)
		if errors.Is(err, sql.ErrNoRows) {
			tr.evictMissingManagedTestIdentity(id, candidate)
			return fmt.Errorf("%w: %s", errStoredTestMissing, id)
		}
		return errors.Join(errStartAdmissionEvidenceStale, refreshErr)
	}
	// A few focused compatibility stores return (nil, nil) for lookups. Their
	// runners have no cross-replica durable identity to reconcile.
	if record == nil {
		return nil
	}
	tr.mu.Lock()
	current := tr.tests[record.ID]
	if current == nil {
		tr.mu.Unlock()
		return nil
	}
	if !sameStoredTestIdentity(current, record) {
		tr.mu.Unlock()
		tr.evictMissingManagedTestIdentity(record.ID, candidate)
		return fmt.Errorf("%w: test %s durable identity changed before start", errStoredTestTransitionFenced, record.ID)
	}
	changed := applyStoredCapacityBindingLocked(current, record)
	// Adopt a durable ref key this replica does not have yet. sameStoredTestIdentity
	// above already proved this is the same test, but the start-commit fence
	// (validateExistingTestStartIdentity) compares ref_key and reads an absent
	// process-local key as a replaced identity. Without this, a leader can never
	// start a test whose ref key was minted by another replica: the refresh reports
	// the same test while the commit rejects it as "replaced". Not a capacity
	// binding change, so it must not trigger the binding broadcast below.
	if strings.TrimSpace(current.RefKey) == "" && strings.TrimSpace(record.RefKey) != "" {
		current.RefKey = record.RefKey
		tr.observeAndIndexRefKeyLocked(current.RefKey, current.ID)
	}
	var snapshot *ManagedTest
	if changed {
		snapshot = cloneManagedTestSummaryForReadLocked(current)
	}
	tr.mu.Unlock()
	if changed {
		tr.broadcastCapacityBindingUpdated(snapshot)
	}
	return nil
}

// evictMissingManagedTestIdentity removes only the exact process-local object
// observed before the durable read. A concurrent reload/recreation installs a
// different pointer and therefore cannot be erased by a stale not-found result.
func (tr *TestRunner) evictMissingManagedTestIdentity(id string, candidate *ManagedTest) bool {
	if tr == nil || candidate == nil {
		return false
	}
	tr.mu.Lock()
	defer tr.mu.Unlock()
	return tr.evictMissingManagedTestIdentityLocked(id, candidate)
}

func (tr *TestRunner) evictMissingManagedTestIdentityLocked(id string, candidate *ManagedTest) bool {
	if tr == nil || candidate == nil {
		return false
	}
	if tr.tests[id] != candidate {
		return false
	}
	if candidate.cancel != nil {
		candidate.cancel()
	}
	tr.removeRefKeyLocked(candidate.RefKey)
	tr.forgetRunSnapshotWriteStateLocked(id)
	if tr.storedHistoryCache != nil {
		tr.storedHistoryCache.deleteTest(id)
	}
	delete(tr.tests, id)
	tr.phaseLogs.Delete(id)
	delete(tr.archives, id)
	tr.archiveReadCaches().forgetRunArchiveCache(id)
	deleteGroupMetricsHistoryCache(id)
	return true
}

type testCapacityBindingListPersistence interface {
	ListTestCapacityBindings([]string) ([]storage.TestCapacityBindingState, error)
}

// refreshCapacityBindingsForHAListRead gives list/summary responses the same
// reciprocal-binding fidelity as single-test reads. Leaders deliberately skip
// the follower's full-map refresh, and followers can be inside its throttle
// window, so both use this lightweight bulk projection before serialization.
// It shares the coalescer every other HA read-refresh path uses: concurrent
// callers collapse onto one query, while a later sequential read performs a new
// query and can immediately observe a cross-replica mutation.
func (tr *TestRunner) refreshCapacityBindingsForHAListRead() {
	if tr == nil || tr.leadership == nil || tr.testStore == nil {
		return
	}
	reader, ok := tr.testStore.(testCapacityBindingListPersistence)
	if !ok {
		return
	}
	// No staleness window: a list read must be able to observe a binding change
	// committed by another replica immediately, so coalescing alone is what
	// bounds the query rate.
	if err := tr.capacityBindingRefresh.refresh(0, func() error {
		return tr.reconcileCapacityBindingsForHAListRead(reader)
	}); err != nil {
		slog.Warn("failed to refresh capacity bindings for HA test list", "error", err)
	}
}

func (tr *TestRunner) reconcileCapacityBindingsForHAListRead(reader testCapacityBindingListPersistence) error {
	tr.mu.RLock()
	testIDs := make([]string, 0, len(tr.tests))
	for id, test := range tr.tests {
		if test != nil {
			testIDs = append(testIDs, id)
		}
	}
	tr.mu.RUnlock()
	if len(testIDs) == 0 {
		return nil
	}
	sort.Strings(testIDs)
	states, err := reader.ListTestCapacityBindings(testIDs)
	if err != nil {
		return err
	}

	changed := make([]*ManagedTest, 0)
	tr.mu.Lock()
	for _, state := range states {
		record := &storage.TestRecord{
			ID:                    state.ID,
			ProjectID:             state.ProjectID,
			StartTime:             state.StartTime,
			TimeslotID:            state.TimeslotID,
			CapacityWindowStartAt: state.CapacityWindowStartAt,
			CapacityWindowEndAt:   state.CapacityWindowEndAt,
		}
		current := tr.tests[state.ID]
		if !sameStoredTestIdentity(current, record) || !applyStoredCapacityBindingLocked(current, record) {
			continue
		}
		changed = append(changed, cloneManagedTestSummaryForReadLocked(current))
	}
	tr.mu.Unlock()
	for _, snapshot := range changed {
		tr.broadcastCapacityBindingUpdated(snapshot)
	}
	return nil
}

func (tr *TestRunner) launchTimeslotRevalidationErrorWithEvidenceLocked(test *ManagedTest, now time.Time, evidence launchWindowEvidence) error {
	if test == nil || test.TimeslotID == nil || tr.reservationStore == nil {
		return nil
	}
	slotID := strings.TrimSpace(*test.TimeslotID)
	if slotID == "" {
		return nil
	}
	var reservation *storage.CapacityReservation
	if evidence.reservationErr != nil {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("load bound capacity timeslot %q: %w", slotID, evidence.reservationErr))
	}
	if evidence.reservationErr == nil {
		for _, candidate := range evidence.boundTimeslots {
			if candidate != nil && strings.TrimSpace(candidate.ID) == slotID {
				reservation = candidate
				break
			}
		}
	}
	if reservation == nil {
		return fmt.Errorf("bound capacity timeslot %q is no longer available", slotID)
	}
	if reservation.ApprovalState != "approved" {
		return fmt.Errorf("bound capacity timeslot %q is no longer approved (state %q)", slotID, reservation.ApprovalState)
	}
	if reservation.BoundTestID == nil || *reservation.BoundTestID != test.ID {
		return fmt.Errorf("bound capacity timeslot %q is no longer bound to this run", slotID)
	}
	launchStart := now.UTC()
	if test.StartedAt != nil && !test.StartedAt.IsZero() && (test.Status == "RUNNING" || test.Status == "PAUSED" || test.Status == "STOPPING") {
		launchStart = test.StartedAt.UTC()
	}
	// This is an execution-boundary check, not a planning check. A manual start
	// of a future SCHEDULED/RESERVED test runs now; validating against its future
	// ScheduledStartAt would let it consume capacity before the reservation begins.
	// Creation/binding already validates the planned window separately.
	window := launchWindowForTest(test, launchStart)
	if !evidence.covers(window) {
		return errors.Join(errStartAdmissionEvidenceStale, fmt.Errorf("bound capacity timeslot evidence is stale because the run window changed"))
	}
	if err := reservationFitError(reservation, window.StartAt, window.EndAt, test.TotalVUsers); err != nil {
		return fmt.Errorf("bound capacity timeslot %q no longer fits this run: %w", slotID, err)
	}
	return nil
}

func (tr *TestRunner) releaseCapacityReservationForTerminalStatus(testID, status string) error {
	if tr == nil || tr.reservationStore == nil || strings.TrimSpace(testID) == "" {
		return nil
	}
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "FAILED", "ABORTED", "STOPPED":
	default:
		return nil
	}
	var slotID string
	tr.mu.RLock()
	if test := tr.tests[testID]; test != nil && test.TimeslotID != nil {
		slotID = strings.TrimSpace(*test.TimeslotID)
	}
	tr.mu.RUnlock()
	if slotID == "" {
		return nil
	}
	return tr.releaseCapacityReservationBinding(testID, slotID)
}

// releaseCapacityReservationBinding releases a known test/slot pair without
// consulting tr.tests. Startup and leadership reconciliation call this before
// the hydrated test is inserted into the map, so map-only lookup silently leaks
// the binding and prevents the normal retry worker from knowing which slot to
// clear.
func (tr *TestRunner) releaseCapacityReservationBinding(testID, slotID string) error {
	if tr == nil || tr.reservationStore == nil {
		return nil
	}
	testID = strings.TrimSpace(testID)
	slotID = strings.TrimSpace(slotID)
	if testID == "" || slotID == "" {
		return nil
	}
	updatedAt := time.Now().UTC()
	released := false
	if tr.durableExecutionFence {
		token := tr.currentExecutionFenceToken()
		if token == "" {
			return errNotExecutionLeader
		}
		store, ok := tr.reservationStore.(atomicCapacityBindingPersistence)
		if !ok || tr.testStore == nil || !sameStoreInstance(store, tr.testStore) {
			return fmt.Errorf("capacity reservation persistence does not support execution-lease-fenced release")
		}
		result, err := store.ClearCapacityReservationBindingAndTestIfLeaseHeld(slotID, testID, updatedAt, executionLeaseName, token)
		if err != nil {
			slog.Warn("release capacity timeslot binding failed", "timeslot_id", slotID, "test_id", testID, "error", err)
			return fmt.Errorf("release capacity timeslot %s for test %s: %w", slotID, testID, err)
		}
		if result == storage.CapacityBindingReleaseFenced {
			return errNotExecutionLeader
		}
		released = result == storage.CapacityBindingReleased
	} else {
		var err error
		released, err = tr.reservationStore.ClearCapacityReservationBinding(slotID, testID, updatedAt)
		if err != nil {
			slog.Warn("release capacity timeslot binding failed", "timeslot_id", slotID, "test_id", testID, "error", err)
			return fmt.Errorf("release capacity timeslot %s for test %s: %w", slotID, testID, err)
		}
	}
	// A prior retry or takeover may already have cleared the durable pair. Once
	// the store confirms there was no error/fence, refresh local state even when
	// this call was an idempotent no-op.
	tr.mu.Lock()
	if test := tr.tests[testID]; test != nil && test.TimeslotID != nil && strings.TrimSpace(*test.TimeslotID) == slotID {
		test.TimeslotID = nil
		test.CapacityWindowStartAt = nil
		test.CapacityWindowEndAt = nil
	}
	tr.mu.Unlock()
	if released {
		slog.Debug("released capacity timeslot binding", "timeslot_id", slotID, "test_id", testID)
	}
	return nil
}

func (tr *TestRunner) enqueueCapacityReservationReleaseRetry(testID, slotID string) {
	if tr == nil || tr.reservationStore == nil || testID == "" || slotID == "" {
		return
	}
	key := "capacity-release:" + testID
	if !tr.beginExecutionPlanRetry(key) {
		return
	}
	go func() {
		defer tr.endExecutionPlanRetry(key)
		delay := executionPlanRetryInitialDelay
		for tr.executionPlanRetryAllowed() {
			if !tr.waitExecutionPlanRetry(delay) {
				return
			}
			err := tr.releaseCapacityReservationBinding(testID, slotID)
			if err == nil {
				return
			}
			slog.Warn("capacity timeslot release retry failed", "timeslot_id", slotID, "test_id", testID, "error", err)
			if !durableBackgroundRetryable(err) {
				return
			}
			delay = min(delay*2, executionPlanRetryMaxDelay)
		}
	}()
}

// reservationFitError reports why a run with the given window and VUser count does
// not fit a reservation (nil if it fits). Both bind paths — create-time
// evidence validation and the standalone bind endpoint — call it so they
// enforce identical window-fit and capacity invariants.
func reservationFitError(reservation *storage.CapacityReservation, start, end time.Time, totalVUsers int) error {
	if start.Before(reservation.StartAt) || end.After(reservation.EndAt) {
		return fmt.Errorf("the run window does not fit within the reserved timeslot")
	}
	if reservation.RequestedCapacity != nil && reservation.RequestedCapacity.VUsers != nil &&
		totalVUsers > *reservation.RequestedCapacity.VUsers {
		return fmt.Errorf("the run requests %d VUsers but the timeslot reserves only %d", totalVUsers, *reservation.RequestedCapacity.VUsers)
	}
	return nil
}

// loadBoundTimeslotsActiveAt performs the durable reservation lookup without
// requiring tr.mu. Admission paths that already own the runner lock must drop
// it, load this immutable evidence, and then revalidate their in-memory
// candidate before using the result.
func (tr *TestRunner) loadBoundTimeslotsActiveAt(at time.Time) ([]*storage.CapacityReservation, error) {
	if tr == nil {
		return nil, nil
	}
	if tr.reservationStore == nil {
		return nil, nil
	}
	// Half-open overlap with [at, at+1ns) selects reservations where
	// start_at <= at < end_at.
	all, err := tr.reservationStore.ListCapacityReservationsByTimeRange("", at, at.Add(time.Nanosecond))
	if err != nil {
		slog.Warn("capacity admission: bound-timeslot lookup failed; failing closed", "error", err, "at", at)
		return nil, err
	}
	out := make([]*storage.CapacityReservation, 0, len(all))
	for _, ts := range all {
		if ts == nil || ts.ApprovalState != "approved" || ts.BoundTestID == nil || strings.TrimSpace(*ts.BoundTestID) == "" {
			continue
		}
		out = append(out, ts)
	}
	return out, nil
}

// reservationTotalsWithTimeslots returns (totalReserved, projectReserved)
// generators at `at`, combining settings-based reservations with the supplied
// active bound timeslots.
func reservationTotalsWithTimeslots(settings SystemSettings, projectID string, at time.Time, activeSlots []*storage.CapacityReservation) (int, int) {
	totalReserved, projectReserved := capacityReservationTotalsAt(settings, projectID, at)
	for _, ts := range activeSlots {
		if timeslotPoolID(ts) != "" {
			g := timeslotReservedGenerators(settings, ts)
			if g > 0 && normalizeProjectID(ts.ProjectID) == normalizeProjectID(projectID) {
				projectReserved = saturatingAddNonNegative(projectReserved, g)
			}
			continue
		}
		g := timeslotReservedGenerators(settings, ts)
		if g <= 0 {
			continue
		}
		totalReserved = saturatingAddNonNegative(totalReserved, g)
		if normalizeProjectID(ts.ProjectID) == normalizeProjectID(projectID) {
			projectReserved = saturatingAddNonNegative(projectReserved, g)
		}
	}
	return totalReserved, projectReserved
}

// timeslotPoolID returns the fleet pool a bound timeslot is scoped to, or "".
func timeslotPoolID(ts *storage.CapacityReservation) string {
	if ts == nil || ts.RequestedCapacity == nil {
		return ""
	}
	return strings.TrimSpace(ts.RequestedCapacity.PoolID)
}

// poolReservationTotalsWithTimeslots returns (totalReserved, projectReserved)
// generators reserved on a specific fleet pool at `at`: settings reservations
// scoped to the pool plus bound timeslots scoped to the pool.
func poolReservationTotalsWithTimeslots(settings SystemSettings, projectID, poolID string, at time.Time, activeSlots []*storage.CapacityReservation) (int, int) {
	totalReserved, projectReserved := poolCapacityReservationTotalsAt(settings, projectID, poolID, at)
	for _, ts := range activeSlots {
		if timeslotPoolID(ts) != poolID || poolID == "" {
			continue
		}
		g := timeslotReservedGenerators(settings, ts)
		if g <= 0 {
			continue
		}
		totalReserved = saturatingAddNonNegative(totalReserved, g)
		if normalizeProjectID(ts.ProjectID) == normalizeProjectID(projectID) {
			projectReserved = saturatingAddNonNegative(projectReserved, g)
		}
	}
	return totalReserved, projectReserved
}

// timeslotReservedGenerators converts a timeslot's requested capacity into
// generators: an explicit generator count if given, else derived from the
// reserved VUsers via the configured per-generator density.
func timeslotReservedGenerators(settings SystemSettings, ts *storage.CapacityReservation) int {
	if ts == nil || ts.RequestedCapacity == nil {
		return 0
	}
	if g := ts.RequestedCapacity.Generators; g != nil && *g > 0 {
		return *g
	}
	if v := ts.RequestedCapacity.VUsers; v != nil && *v > 0 {
		perGen := settings.CapacityPlanning.Generator.MaxVUsers
		if perGen <= 0 {
			perGen = 10000
		}
		// 1+(v-1)/n is the same ceiling division without overflowing when a
		// client supplies an int near the platform maximum.
		return 1 + (*v-1)/perGen
	}
	return 0
}

// capacityChangeOverbookingConflict compares two complete capacity states and
// reports a conflict only when the proposed state increases overbooking while an
// approved timeslot is active. This preserves an explicit prior override (and
// the historical settings-only clamping behavior) while preventing either a new
// approval or a settings edit from making the combined promises less feasible.
func capacityChangeOverbookingConflict(
	beforeSettings, afterSettings SystemSettings,
	beforeSlots, afterSlots []*storage.CapacityReservation,
	poolEnvelopes map[string]int,
) string {
	envelopes := make(map[string]struct{})
	boundaries := make(map[time.Time]struct{})
	collectSettings := func(settings SystemSettings) {
		for _, reservation := range normalizeCapacityReservations(settings.CapacityPlanning.Reservations) {
			envelopes[strings.TrimSpace(reservation.PoolID)] = struct{}{}
			if reservation.StartsAt != nil {
				boundaries[reservation.StartsAt.UTC()] = struct{}{}
			}
			if reservation.EndsAt != nil {
				boundaries[reservation.EndsAt.UTC()] = struct{}{}
			}
		}
	}
	collectSlots := func(slots []*storage.CapacityReservation) {
		for _, slot := range slots {
			if slot == nil {
				continue
			}
			envelopes[timeslotPoolID(slot)] = struct{}{}
			boundaries[slot.StartAt.UTC()] = struct{}{}
			boundaries[slot.EndAt.UTC()] = struct{}{}
		}
	}
	collectSettings(beforeSettings)
	collectSettings(afterSettings)
	collectSlots(beforeSlots)
	collectSlots(afterSlots)

	times := make([]time.Time, 0, len(boundaries))
	for at := range boundaries {
		times = append(times, at)
	}
	sort.Slice(times, func(i, j int) bool { return times[i].Before(times[j]) })
	poolIDs := make([]string, 0, len(envelopes))
	for poolID := range envelopes {
		poolIDs = append(poolIDs, poolID)
	}
	sort.Strings(poolIDs)

	for _, poolID := range poolIDs {
		for _, at := range times {
			beforeTotal, _ := capacityEnvelopeUsageAt(beforeSettings, beforeSlots, poolID, at)
			afterTotal, afterDynamic := capacityEnvelopeUsageAt(afterSettings, afterSlots, poolID, at)
			// Settings-only oversubscription has historically been clamped at
			// admission. This fence is specifically for promises shared with
			// approved timeslots, so do not make an unrelated cosmetic settings
			// edit fail solely because of that pre-existing condition.
			if afterDynamic <= 0 {
				continue
			}
			beforeLimit, beforeKnown := capacityEnvelopeLimit(beforeSettings, poolEnvelopes, poolID)
			afterLimit, afterKnown := capacityEnvelopeLimit(afterSettings, poolEnvelopes, poolID)
			if !afterKnown {
				continue
			}
			beforeExcess := 0
			if beforeKnown {
				beforeExcess = maxInt(0, beforeTotal-beforeLimit)
			}
			afterExcess := maxInt(0, afterTotal-afterLimit)
			if afterExcess <= beforeExcess {
				continue
			}
			if poolID != "" {
				return fmt.Sprintf("capacity change would reserve %d generators in fleet pool %q at %s but the pool has only %d", afterTotal, poolID, at.Format(time.RFC3339), afterLimit)
			}
			return fmt.Sprintf("capacity change would reserve %d generators at %s but only %d are available", afterTotal, at.Format(time.RFC3339), afterLimit)
		}
	}
	return ""
}

func capacityEnvelopeLimit(settings SystemSettings, poolEnvelopes map[string]int, poolID string) (int, bool) {
	if poolID != "" {
		limit, known := poolEnvelopes[poolID]
		return limit, known
	}
	return settings.CapacityPlanning.Availability.ConfiguredAvailableGenerators, true
}

func capacityEnvelopeUsageAt(settings SystemSettings, slots []*storage.CapacityReservation, poolID string, at time.Time) (total, dynamic int) {
	poolID = strings.TrimSpace(poolID)
	for _, reservation := range normalizeCapacityReservations(settings.CapacityPlanning.Reservations) {
		if reservation.ReservedGenerators <= 0 || strings.TrimSpace(reservation.PoolID) != poolID || !capacitySettingsReservationActiveAt(reservation, at) {
			continue
		}
		total = saturatingAddNonNegative(total, reservation.ReservedGenerators)
	}
	for _, slot := range slots {
		if slot == nil || timeslotPoolID(slot) != poolID || at.Before(slot.StartAt) || !at.Before(slot.EndAt) {
			continue
		}
		units := timeslotReservedGenerators(settings, slot)
		if units <= 0 {
			continue
		}
		total = saturatingAddNonNegative(total, units)
		dynamic = saturatingAddNonNegative(dynamic, units)
	}
	return total, dynamic
}

func capacitySettingsReservationActiveAt(reservation CapacityReservation, at time.Time) bool {
	if reservation.StartsAt != nil && at.Before(*reservation.StartsAt) {
		return false
	}
	// Capacity windows are half-open: an ending reservation does not overlap one
	// that starts at the same instant.
	if reservation.EndsAt != nil && !at.Before(*reservation.EndsAt) {
		return false
	}
	return true
}
