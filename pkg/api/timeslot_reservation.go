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

// validateAndBindTimeslotLocked performs an early, user-friendly validation and
// records the requested slot on the in-memory test. The authoritative validation
// and both durable writes happen later in saveNewManagedTestRecord's single
// transaction. Caller holds tr.mu.
func (tr *TestRunner) validateAndBindTimeslotLocked(timeslotID string, test *ManagedTest) error {
	if tr.reservationStore == nil {
		return fmt.Errorf("capacity timeslots are not available")
	}
	reservation, err := tr.reservationStore.GetCapacityReservation(timeslotID)
	if err != nil || reservation == nil {
		return fmt.Errorf("timeslot %s not found", timeslotID)
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
					return nil
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
		// Compatibility for lightweight, single-controller test stores. tr.mu
		// serializes this fallback; production SQLite/PostgreSQL stores implement
		// the atomic interface above. Several narrow test doubles deliberately do
		// not implement durable lookup semantics, so retain their historical save.
		return tr.testStore.SaveTest(record)
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
		if lastErr == nil && result == storage.CapacityBindingBound {
			stored, storedErr := tr.testStore.GetTest(test.ID)
			reservation, reservationErr := tr.reservationStore.GetCapacityReservation(slotID)
			if storedErr == nil && reservationErr == nil && sameTestMaterialization(stored, record) && reservation != nil &&
				reservation.BoundTestID != nil && strings.TrimSpace(*reservation.BoundTestID) == test.ID {
				return nil
			}
			if storedErr == nil && stored != nil && !sameTestMaterialization(stored, record) {
				return fmt.Errorf("%w: test %s", errTestIdentityCollision, test.ID)
			}
			lastErr = errors.Join(storedErr, reservationErr)
		} else if lastErr == nil && result == storage.CapacityBindingTestExists {
			stored, lookupErr := tr.testStore.GetTest(test.ID)
			if lookupErr == nil && !sameTestMaterialization(stored, record) {
				return fmt.Errorf("%w: test %s", errTestIdentityCollision, test.ID)
			}
			lastErr = capacityBindingResultError(result)
		} else if lastErr == nil {
			lastErr = capacityBindingResultError(result)
		}
		if attempt+1 < durableTransitionAttempts {
			time.Sleep(durableTransitionRetryDelay)
		}
	}
	return fmt.Errorf("bind timeslot and persist test: %w", lastErr)
}

func sameTestMaterialization(stored, intended *storage.TestRecord) bool {
	if stored == nil || intended == nil || stored.ID != intended.ID {
		return false
	}
	if key := strings.TrimSpace(intended.MaterializationKey); key != "" {
		return strings.TrimSpace(stored.MaterializationKey) == key
	}
	return strings.TrimSpace(intended.RefKey) != "" && strings.TrimSpace(stored.RefKey) == strings.TrimSpace(intended.RefKey)
}

// BindCapacityTimeslot is the standalone bind operation used by the REST
// endpoint. It serializes with local test deletion under tr.mu and delegates the
// authoritative test/reservation locks and lease check to one store transaction.
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

	tr.mu.Lock()
	resolvedID := tr.resolveRefKeyLocked(requestedTestID)
	record, err := tr.testStore.GetTest(resolvedID)
	if err != nil {
		tr.mu.Unlock()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, storage.CapacityBindingTestMissing, nil
		}
		return nil, storage.CapacityBindingFenced, err
	}
	if record == nil {
		tr.mu.Unlock()
		return nil, storage.CapacityBindingTestMissing, nil
	}
	resolvedID = record.ID
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
		tr.mu.Unlock()
		if err == nil && result == storage.CapacityBindingFenced {
			err = errNotExecutionLeader
		}
		return nil, result, err
	}
	slotID := timeslotID
	record.TimeslotID = &slotID
	record.CapacityWindowStartAt = cloneTimePtr(&window.StartAt)
	record.CapacityWindowEndAt = cloneTimePtr(&window.EndAt)
	if current := tr.tests[resolvedID]; current != nil {
		current.TimeslotID = &slotID
		current.CapacityWindowStartAt = cloneTimePtr(&window.StartAt)
		current.CapacityWindowEndAt = cloneTimePtr(&window.EndAt)
	} else {
		managed := managedTestFromRecord(record)
		managed.TimeslotID = &slotID
		tr.tests[resolvedID] = managed
		tr.observeAndIndexRefKeyLocked(managed.RefKey, managed.ID)
	}
	tr.mu.Unlock()

	reservation, err := tr.reservationStore.GetCapacityReservation(timeslotID)
	if err != nil {
		return nil, storage.CapacityBindingBound, err
	}
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

// launchTimeslotRevalidationErrorLocked re-checks a run's bound capacity timeslot
// at the actual launch instant. A timeslot is validated and bound at create time,
// but between then and launch it can be rejected, re-bound, resized, or the run's
// effective window can drift — so trusting the bind-time fit lets a run launch
// into capacity it no longer holds. Returns a non-nil error (blocking the start)
// when the bound slot is gone, no longer approved, no longer bound to this run, or
// no longer fits. Runs without a bound timeslot are unaffected.
func (tr *TestRunner) launchTimeslotRevalidationErrorLocked(test *ManagedTest, now time.Time) error {
	if test == nil || test.TimeslotID == nil || tr.reservationStore == nil {
		return nil
	}
	slotID := strings.TrimSpace(*test.TimeslotID)
	if slotID == "" {
		return nil
	}
	reservation, err := tr.reservationStore.GetCapacityReservation(slotID)
	if err != nil || reservation == nil {
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
	} else if test.ScheduledStartAt != nil && test.ScheduledStartAt.After(launchStart) {
		// A future schedule still starts at its requested instant. A delayed
		// schedule starts now; trusting its stale timestamp could admit a run whose
		// actual end extends past the reservation.
		launchStart = test.ScheduledStartAt.UTC()
	}
	window := launchWindowForTest(test, launchStart)
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
// (validateAndBindTimeslotLocked) and the standalone bind endpoint — call it so
// they enforce identical window-fit and capacity invariants.
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

// boundTimeslotsActiveAtLocked returns the approved, bound timeslots whose window
// contains the instant `at`. Caller holds tr.mu. The error is propagated (not
// swallowed) so admission can fail CLOSED: silently treating a lookup failure as
// "no reservations" would drop every timeslot's protection and let other tenants
// into reserved capacity.
func (tr *TestRunner) boundTimeslotsActiveAtLocked(at time.Time) ([]*storage.CapacityReservation, error) {
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

// boundTimeslotBoundariesInWindowLocked returns the start/end instants of the
// approved, bound timeslots overlapping the launch window. They are fed into the
// launch-window sample set so the reservation-aware capacity check is evaluated
// at the exact instants a timeslot's capacity hold begins or ends, rather than
// only at fixed event/window boundaries. A lookup failure degrades to no extra
// boundaries — the per-sample reservation check still fails closed on its own.
func (tr *TestRunner) boundTimeslotBoundariesInWindowLocked(window LaunchWindow) []time.Time {
	if tr.reservationStore == nil {
		return nil
	}
	slots, err := tr.reservationStore.ListCapacityReservationsByTimeRange("", window.StartAt, window.EndAt)
	if err != nil {
		slog.Warn("launch window: bound-timeslot boundary lookup failed", "error", err)
		return nil
	}
	out := make([]time.Time, 0, len(slots)*2)
	for _, ts := range slots {
		if ts == nil || ts.ApprovalState != "approved" || ts.BoundTestID == nil || strings.TrimSpace(*ts.BoundTestID) == "" {
			continue
		}
		out = append(out, ts.StartAt.UTC(), ts.EndAt.UTC())
	}
	return out
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
