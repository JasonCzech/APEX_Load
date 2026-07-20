package storage

// Atomic capacity-reservation/test binding. A binding is represented on both
// durable rows (capacity_reservations.bound_test_id and tests.timeslot_id), so
// every production mutation of that relationship must commit both sides in the
// same transaction.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// CapacityBindingResult describes the durable outcome of a create or bind
// attempt without forcing API callers to infer a conflict from a second read.
type CapacityBindingResult string

const (
	CapacityBindingBound                 CapacityBindingResult = "bound"
	CapacityBindingFenced                CapacityBindingResult = "fenced"
	CapacityBindingReservationMissing    CapacityBindingResult = "reservation_missing"
	CapacityBindingTestMissing           CapacityBindingResult = "test_missing"
	CapacityBindingReservationUnapproved CapacityBindingResult = "reservation_unapproved"
	CapacityBindingReservationBound      CapacityBindingResult = "reservation_bound"
	CapacityBindingTestBound             CapacityBindingResult = "test_bound"
	CapacityBindingProjectMismatch       CapacityBindingResult = "project_mismatch"
	CapacityBindingWindowMismatch        CapacityBindingResult = "window_mismatch"
	CapacityBindingCapacityExceeded      CapacityBindingResult = "capacity_exceeded"
	CapacityBindingTestNotBindable       CapacityBindingResult = "test_not_bindable"
	CapacityBindingTestExists            CapacityBindingResult = "test_exists"
)

// ErrCapacityReservationStartInvalid marks a launch-time failure of the
// reciprocal reservation/test invariant. Callers must not persist an active
// test state when the bound slot was rejected, resized, removed, or re-bound.
var ErrCapacityReservationStartInvalid = errors.New("bound capacity reservation is not valid for test start")

// CapacityBindingReleaseResult distinguishes a stale lease from an already
// released pair. That distinction is important to HA retry workers: a stale
// epoch must stop instead of treating its no-op as successful cleanup.
type CapacityBindingReleaseResult string

const (
	CapacityBindingReleased        CapacityBindingReleaseResult = "released"
	CapacityBindingAlreadyReleased CapacityBindingReleaseResult = "already_released"
	CapacityBindingReleaseFenced   CapacityBindingReleaseResult = "fenced"
)

func (s *SQLiteStore) CreateTestAndBindCapacityReservation(test *TestRecord, reservationID string, boundAt time.Time) (CapacityBindingResult, error) {
	return s.createTestAndBindCapacityReservation(test, reservationID, boundAt, "", "", false)
}

func (s *SQLiteStore) CreateTestAndBindCapacityReservationIfLeaseHeld(test *TestRecord, reservationID string, boundAt time.Time, leaseName, leaseOwnerID string) (CapacityBindingResult, error) {
	return s.createTestAndBindCapacityReservation(test, reservationID, boundAt, leaseName, leaseOwnerID, true)
}

func (s *SQLiteStore) CreateTestAndBindCapacityReservationIfLeaseTermHeld(test *TestRecord, reservationID string, boundAt time.Time, leaseName, leaseOwnerID string, leaseGeneration uint64) (CapacityBindingResult, error) {
	return s.createTestAndBindCapacityReservationWithTerm(test, reservationID, boundAt, leaseName, leaseOwnerID, leaseGeneration)
}

func (s *SQLiteStore) createTestAndBindCapacityReservationWithTerm(test *TestRecord, reservationID string, boundAt time.Time, leaseName, leaseOwnerID string, leaseGeneration uint64) (CapacityBindingResult, error) {
	reservationID = strings.TrimSpace(reservationID)
	if test == nil || strings.TrimSpace(test.ID) == "" {
		return CapacityBindingTestMissing, fmt.Errorf("test and test id are required")
	}
	if reservationID == "" {
		return CapacityBindingReservationMissing, fmt.Errorf("capacity reservation id is required")
	}
	tx, err := s.beginExecutionLeaseTermTransaction(leaseName, leaseOwnerID, leaseGeneration)
	if err != nil || tx == nil {
		return CapacityBindingFenced, err
	}
	defer tx.Rollback()
	reservation, err := capacityReservationForUpdate(tx, s.db.dialect, reservationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CapacityBindingReservationMissing, nil
		}
		return CapacityBindingFenced, err
	}
	if existing, lookupErr := testForCapacityBindingUpdate(tx, s.db.dialect, test.ID); lookupErr == nil {
		if reservation.BoundTestID != nil && strings.TrimSpace(*reservation.BoundTestID) == test.ID &&
			existing.TimeslotID != nil && strings.TrimSpace(*existing.TimeslotID) == reservation.ID &&
			strings.TrimSpace(existing.MaterializationKey) == strings.TrimSpace(test.MaterializationKey) {
			return CapacityBindingBound, nil
		}
		return CapacityBindingTestExists, nil
	} else if !errors.Is(lookupErr, sql.ErrNoRows) {
		return CapacityBindingFenced, lookupErr
	}
	if result := validateCapacityBinding(reservation, test, boundAt, true); result != CapacityBindingBound {
		return result, nil
	}
	slotID := reservation.ID
	test.TimeslotID = &slotID
	if err := lockActiveReleaseForNewTest(tx, test); err != nil {
		return CapacityBindingFenced, err
	}
	if err := s.saveTestWithExecutor(tx, test); err != nil {
		return CapacityBindingFenced, err
	}
	result, err := tx.Exec(`UPDATE capacity_reservations SET bound_test_id=?, bound_test_status=?, updated_at=? WHERE id=? AND approval_state='approved' AND bound_test_id IS NULL`, test.ID, test.Status, boundAt.UTC(), reservation.ID)
	if err != nil {
		return CapacityBindingFenced, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return CapacityBindingReservationBound, err
	}
	if err := tx.Commit(); err != nil {
		return CapacityBindingFenced, err
	}
	return CapacityBindingBound, nil
}

func (s *SQLiteStore) createTestAndBindCapacityReservation(test *TestRecord, reservationID string, boundAt time.Time, leaseName, leaseOwnerID string, requireLease bool) (CapacityBindingResult, error) {
	reservationID = strings.TrimSpace(reservationID)
	if test == nil {
		return CapacityBindingTestMissing, fmt.Errorf("test is required")
	}
	if strings.TrimSpace(test.ID) == "" {
		return CapacityBindingTestMissing, fmt.Errorf("test id is required")
	}
	if reservationID == "" {
		return CapacityBindingReservationMissing, fmt.Errorf("capacity reservation id is required")
	}
	if requireLease {
		leaseName = strings.TrimSpace(leaseName)
		leaseOwnerID = strings.TrimSpace(leaseOwnerID)
		if leaseName == "" || leaseOwnerID == "" {
			return CapacityBindingFenced, fmt.Errorf("lease name and owner token are required")
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return CapacityBindingFenced, err
	}
	defer tx.Rollback()
	if requireLease {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
		if err != nil {
			return CapacityBindingFenced, err
		}
		if !held {
			return CapacityBindingFenced, nil
		}
	}

	reservation, err := capacityReservationForUpdate(tx, s.db.dialect, reservationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CapacityBindingReservationMissing, nil
		}
		return CapacityBindingFenced, err
	}
	if existing, err := testForCapacityBindingUpdate(tx, s.db.dialect, test.ID); err == nil {
		// A commit response can be lost after both writes become durable. Make the
		// create operation idempotent for that exact reciprocal pair so bounded
		// persistence retries do not turn a successful scheduled materialization
		// into a false failure.
		if reservation.BoundTestID != nil && strings.TrimSpace(*reservation.BoundTestID) == test.ID &&
			existing.TimeslotID != nil && strings.TrimSpace(*existing.TimeslotID) == reservation.ID {
			return CapacityBindingBound, nil
		}
		return CapacityBindingTestExists, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return CapacityBindingFenced, err
	}

	if result := validateCapacityBinding(reservation, test, boundAt, true); result != CapacityBindingBound {
		return result, nil
	}
	slotID := reservation.ID
	test.TimeslotID = &slotID
	if err := lockActiveReleaseForNewTest(tx, test); err != nil {
		return CapacityBindingFenced, err
	}
	if err := s.saveTestWithExecutor(tx, test); err != nil {
		return CapacityBindingFenced, err
	}
	result, err := tx.Exec(`UPDATE capacity_reservations
		SET bound_test_id = ?, bound_test_status = ?, updated_at = ?
		WHERE id = ? AND approval_state = 'approved' AND bound_test_id IS NULL`,
		test.ID, test.Status, boundAt.UTC(), reservation.ID)
	if err != nil {
		return CapacityBindingFenced, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CapacityBindingFenced, err
	}
	if affected != 1 {
		return CapacityBindingReservationBound, nil
	}
	if err := tx.Commit(); err != nil {
		return CapacityBindingFenced, err
	}
	return CapacityBindingBound, nil
}

func (s *SQLiteStore) BindCapacityReservationToTest(reservationID, testID string, boundAt time.Time) (CapacityBindingResult, error) {
	return s.bindCapacityReservationToTest(reservationID, testID, boundAt, time.Time{}, time.Time{}, "", "", false)
}

func (s *SQLiteStore) BindCapacityReservationToTestIfLeaseHeld(reservationID, testID string, boundAt time.Time, leaseName, leaseOwnerID string) (CapacityBindingResult, error) {
	return s.bindCapacityReservationToTest(reservationID, testID, boundAt, time.Time{}, time.Time{}, leaseName, leaseOwnerID, true)
}

func (s *SQLiteStore) BindCapacityReservationToTestWithWindow(reservationID, testID string, boundAt, windowStartAt, windowEndAt time.Time) (CapacityBindingResult, error) {
	return s.bindCapacityReservationToTest(reservationID, testID, boundAt, windowStartAt, windowEndAt, "", "", false)
}

func (s *SQLiteStore) BindCapacityReservationToTestWithWindowIfLeaseHeld(reservationID, testID string, boundAt, windowStartAt, windowEndAt time.Time, leaseName, leaseOwnerID string) (CapacityBindingResult, error) {
	return s.bindCapacityReservationToTest(reservationID, testID, boundAt, windowStartAt, windowEndAt, leaseName, leaseOwnerID, true)
}

func (s *SQLiteStore) bindCapacityReservationToTest(reservationID, testID string, boundAt, windowStartAt, windowEndAt time.Time, leaseName, leaseOwnerID string, requireLease bool) (CapacityBindingResult, error) {
	reservationID = strings.TrimSpace(reservationID)
	testID = strings.TrimSpace(testID)
	if reservationID == "" {
		return CapacityBindingReservationMissing, fmt.Errorf("capacity reservation id is required")
	}
	if testID == "" {
		return CapacityBindingTestMissing, fmt.Errorf("test id is required")
	}
	if requireLease {
		leaseName = strings.TrimSpace(leaseName)
		leaseOwnerID = strings.TrimSpace(leaseOwnerID)
		if leaseName == "" || leaseOwnerID == "" {
			return CapacityBindingFenced, fmt.Errorf("lease name and owner token are required")
		}
	}

	tx, err := s.db.Begin()
	if err != nil {
		return CapacityBindingFenced, err
	}
	defer tx.Rollback()
	if requireLease {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
		if err != nil {
			return CapacityBindingFenced, err
		}
		if !held {
			return CapacityBindingFenced, nil
		}
	}

	reservation, err := capacityReservationForUpdate(tx, s.db.dialect, reservationID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CapacityBindingReservationMissing, nil
		}
		return CapacityBindingFenced, err
	}
	test, err := testForCapacityBindingUpdate(tx, s.db.dialect, testID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CapacityBindingTestMissing, nil
		}
		return CapacityBindingFenced, err
	}
	if !windowStartAt.IsZero() || !windowEndAt.IsZero() {
		if windowStartAt.IsZero() || windowEndAt.IsZero() || !windowEndAt.After(windowStartAt) {
			return CapacityBindingWindowMismatch, nil
		}
		windowStartAt = windowStartAt.UTC()
		windowEndAt = windowEndAt.UTC()
		test.CapacityWindowStartAt = &windowStartAt
		test.CapacityWindowEndAt = &windowEndAt
	}
	if result := validateCapacityBinding(reservation, test, boundAt, false); result != CapacityBindingBound {
		return result, nil
	}

	result, err := tx.Exec(`UPDATE tests SET timeslot_id = ?, capacity_window_start_at = ?, capacity_window_end_at = ? WHERE id = ? AND timeslot_id IS NULL`, reservation.ID, test.CapacityWindowStartAt, test.CapacityWindowEndAt, test.ID)
	if err != nil {
		return CapacityBindingFenced, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return CapacityBindingFenced, err
	}
	if affected != 1 {
		return CapacityBindingTestBound, nil
	}
	result, err = tx.Exec(`UPDATE capacity_reservations
		SET bound_test_id = ?, bound_test_status = ?, updated_at = ?
		WHERE id = ? AND approval_state = 'approved' AND bound_test_id IS NULL`,
		test.ID, test.Status, boundAt.UTC(), reservation.ID)
	if err != nil {
		return CapacityBindingFenced, err
	}
	affected, err = result.RowsAffected()
	if err != nil {
		return CapacityBindingFenced, err
	}
	if affected != 1 {
		return CapacityBindingReservationBound, nil
	}
	if err := tx.Commit(); err != nil {
		return CapacityBindingFenced, err
	}
	return CapacityBindingBound, nil
}

func validateCapacityBinding(reservation *CapacityReservation, test *TestRecord, boundAt time.Time, allowRequestedTimeslot bool) CapacityBindingResult {
	if reservation == nil {
		return CapacityBindingReservationMissing
	}
	if test == nil {
		return CapacityBindingTestMissing
	}
	if strings.ToLower(strings.TrimSpace(reservation.ApprovalState)) != "approved" {
		return CapacityBindingReservationUnapproved
	}
	if reservation.BoundTestID != nil && strings.TrimSpace(*reservation.BoundTestID) != "" {
		return CapacityBindingReservationBound
	}
	if test.TimeslotID != nil && strings.TrimSpace(*test.TimeslotID) != "" &&
		(!allowRequestedTimeslot || strings.TrimSpace(*test.TimeslotID) != reservation.ID) {
		return CapacityBindingTestBound
	}
	if normalizeProjectID(reservation.ProjectID) != normalizeProjectID(test.ProjectID) {
		return CapacityBindingProjectMismatch
	}
	if !capacityBindingAllowedTestStatus(test.Status) {
		return CapacityBindingTestNotBindable
	}
	start := boundAt.UTC()
	end := time.Time{}
	windowStartSet := test.CapacityWindowStartAt != nil && !test.CapacityWindowStartAt.IsZero()
	windowEndSet := test.CapacityWindowEndAt != nil && !test.CapacityWindowEndAt.IsZero()
	if windowStartSet != windowEndSet {
		return CapacityBindingWindowMismatch
	}
	if windowStartSet && windowEndSet {
		start = test.CapacityWindowStartAt.UTC()
		end = test.CapacityWindowEndAt.UTC()
	} else {
		if test.ScheduledStartAt != nil && !test.ScheduledStartAt.IsZero() {
			start = test.ScheduledStartAt.UTC()
		}
		end = start.Add(time.Duration(test.Duration))
	}
	if start.Before(reservation.StartAt.UTC()) || end.After(reservation.EndAt.UTC()) {
		return CapacityBindingWindowMismatch
	}
	if reservation.RequestedCapacity != nil && reservation.RequestedCapacity.VUsers != nil &&
		test.TotalVUsers > *reservation.RequestedCapacity.VUsers {
		return CapacityBindingCapacityExceeded
	}
	return CapacityBindingBound
}

func capacityBindingAllowedTestStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "QUEUED", "PENDING", "SCHEDULED", "RESERVED", "SHAKEOUT_REVIEW":
		return true
	default:
		return false
	}
}

func capacityBindingTerminalTestStatus(status string) bool {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "COMPLETE", "FAILED", "ABORTED", "STOPPED", "CANCELLED":
		return true
	default:
		return false
	}
}

func capacityReservationForUpdate(tx *storeTx, dialect storeDialect, id string) (*CapacityReservation, error) {
	query := capacityReservationSelect + ` WHERE id = ?`
	if dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	return scanCapacityReservation(tx.QueryRow(query, id))
}

func testForCapacityBindingUpdate(tx *storeTx, dialect storeDialect, id string) (*TestRecord, error) {
	query := testSelectColumns() + ` FROM tests WHERE id = ?`
	if dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	test, err := scanTest(tx.QueryRow(query, id))
	if err != nil {
		return nil, err
	}
	return &test, nil
}

// validateCapacityReservationForTestStart locks and validates the reservation
// and reciprocal test row in the same transaction that will persist SHAKEOUT or
// INITIALIZING. The lock order is reservation -> test, matching reject/unbind,
// so a concurrent rejection and start have exactly one durable winner.
func validateCapacityReservationForTestStart(tx *storeTx, dialect storeDialect, test *TestRecord) (string, error) {
	if test == nil || test.TimeslotID == nil || strings.TrimSpace(*test.TimeslotID) == "" {
		return "", nil
	}
	slotID := strings.TrimSpace(*test.TimeslotID)
	reservation, err := capacityReservationForUpdate(tx, dialect, slotID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: timeslot %q no longer exists", ErrCapacityReservationStartInvalid, slotID)
		}
		return "", err
	}
	if strings.ToLower(strings.TrimSpace(reservation.ApprovalState)) != "approved" {
		return "", fmt.Errorf("%w: timeslot %q is %q", ErrCapacityReservationStartInvalid, slotID, reservation.ApprovalState)
	}
	if reservation.BoundTestID == nil || strings.TrimSpace(*reservation.BoundTestID) != strings.TrimSpace(test.ID) {
		return "", fmt.Errorf("%w: timeslot %q is no longer bound to test %q", ErrCapacityReservationStartInvalid, slotID, test.ID)
	}
	if normalizeProjectID(reservation.ProjectID) != normalizeProjectID(test.ProjectID) {
		return "", fmt.Errorf("%w: timeslot %q belongs to a different project", ErrCapacityReservationStartInvalid, slotID)
	}

	// Lock the reciprocal test row after the reservation. A committed reject
	// clears this pointer, so a stale in-memory TestRecord cannot restore it via
	// the full-row start upsert.
	durable, err := testForCapacityBindingUpdate(tx, dialect, test.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", fmt.Errorf("%w: bound test %q no longer exists", ErrCapacityReservationStartInvalid, test.ID)
		}
		return "", err
	}
	if durable.TimeslotID == nil || strings.TrimSpace(*durable.TimeslotID) != slotID {
		return "", fmt.Errorf("%w: test %q no longer carries timeslot %q", ErrCapacityReservationStartInvalid, test.ID, slotID)
	}

	windowStartSet := test.CapacityWindowStartAt != nil && !test.CapacityWindowStartAt.IsZero()
	windowEndSet := test.CapacityWindowEndAt != nil && !test.CapacityWindowEndAt.IsZero()
	if windowStartSet != windowEndSet {
		return "", fmt.Errorf("%w: test %q has an incomplete capacity window", ErrCapacityReservationStartInvalid, test.ID)
	}
	launchDuration := time.Duration(test.Duration)
	if windowStartSet {
		launchDuration = test.CapacityWindowEndAt.Sub(*test.CapacityWindowStartAt)
		if launchDuration < 0 {
			return "", fmt.Errorf("%w: test %q has an invalid capacity window", ErrCapacityReservationStartInvalid, test.ID)
		}
	}
	// CapacityWindowStartAt is the planned bind instant. At the execution
	// boundary the run consumes capacity at the database's current time, while
	// retaining the authoritative full-window span (delay/baselines included).
	// Never substitute a future ScheduledStartAt here: that would authorize an
	// early manual/pending start against capacity that does not exist yet.
	startAt, err := preciseDatabaseTime(tx, dialect)
	if err != nil {
		return "", err
	}
	endAt := startAt.Add(launchDuration)
	if startAt.Before(reservation.StartAt.UTC()) || endAt.After(reservation.EndAt.UTC()) {
		return "", fmt.Errorf("%w: timeslot %q no longer contains test %q launch window", ErrCapacityReservationStartInvalid, slotID, test.ID)
	}
	if reservation.RequestedCapacity != nil && reservation.RequestedCapacity.VUsers != nil && test.TotalVUsers > *reservation.RequestedCapacity.VUsers {
		return "", fmt.Errorf("%w: timeslot %q no longer covers %d VUsers", ErrCapacityReservationStartInvalid, slotID, test.TotalVUsers)
	}
	return slotID, nil
}

func (s *SQLiteStore) ClearCapacityReservationBindingAndTestIfLeaseHeld(reservationID, testID string, updatedAt time.Time, leaseName, leaseOwnerID string) (CapacityBindingReleaseResult, error) {
	return s.clearCapacityReservationBindingAndTest(reservationID, testID, updatedAt, leaseName, leaseOwnerID, true)
}

func (s *SQLiteStore) clearCapacityReservationBindingAndTest(reservationID, testID string, updatedAt time.Time, leaseName, leaseOwnerID string, requireLease bool) (CapacityBindingReleaseResult, error) {
	reservationID = strings.TrimSpace(reservationID)
	testID = strings.TrimSpace(testID)
	if reservationID == "" || testID == "" {
		return CapacityBindingAlreadyReleased, nil
	}
	if requireLease {
		leaseName = strings.TrimSpace(leaseName)
		leaseOwnerID = strings.TrimSpace(leaseOwnerID)
		if leaseName == "" || leaseOwnerID == "" {
			return CapacityBindingReleaseFenced, fmt.Errorf("lease name and owner token are required")
		}
	}
	tx, err := s.db.Begin()
	if err != nil {
		return CapacityBindingReleaseFenced, err
	}
	defer tx.Rollback()
	if requireLease {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
		if err != nil {
			return CapacityBindingReleaseFenced, err
		}
		if !held {
			return CapacityBindingReleaseFenced, nil
		}
	}

	// Lock in the same reservation -> test order as bind. Missing legacy rows are
	// tolerated so either surviving side can still be repaired by this release.
	if _, err := capacityReservationForUpdate(tx, s.db.dialect, reservationID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CapacityBindingReleaseFenced, err
	}
	if _, err := testForCapacityBindingUpdate(tx, s.db.dialect, testID); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return CapacityBindingReleaseFenced, err
	}
	changed := false
	result, err := tx.Exec(`UPDATE capacity_reservations
		SET bound_test_id = NULL, bound_test_status = NULL, updated_at = ?
		WHERE id = ? AND bound_test_id = ?`, updatedAt.UTC(), reservationID, testID)
	if err != nil {
		return CapacityBindingReleaseFenced, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return CapacityBindingReleaseFenced, err
	} else if affected > 0 {
		changed = true
	}
	result, err = tx.Exec(`UPDATE tests SET timeslot_id = NULL, capacity_window_start_at = NULL, capacity_window_end_at = NULL WHERE id = ? AND timeslot_id = ?`, testID, reservationID)
	if err != nil {
		return CapacityBindingReleaseFenced, err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return CapacityBindingReleaseFenced, err
	} else if affected > 0 {
		changed = true
	}
	if err := tx.Commit(); err != nil {
		return CapacityBindingReleaseFenced, err
	}
	if changed {
		return CapacityBindingReleased, nil
	}
	return CapacityBindingAlreadyReleased, nil
}

// RepairCapacityReservationBindings reconciles legacy one-sided rows. The
// reservation side is authoritative because the historical standalone bind
// wrote only bound_test_id; a test-only pointer is normally the residue of the
// historical terminal-release path and is cleared.
func (s *SQLiteStore) RepairCapacityReservationBindings() (int64, error) {
	return s.repairCapacityReservationBindings("", "", false)
}

func (s *SQLiteStore) RepairCapacityReservationBindingsIfLeaseHeld(leaseName, leaseOwnerID string) (bool, int64, error) {
	leaseName = strings.TrimSpace(leaseName)
	leaseOwnerID = strings.TrimSpace(leaseOwnerID)
	if leaseName == "" || leaseOwnerID == "" {
		return false, 0, fmt.Errorf("lease name and owner token are required")
	}
	changed, err := s.repairCapacityReservationBindings(leaseName, leaseOwnerID, true)
	if errors.Is(err, errCapacityBindingRepairFenced) {
		return false, changed, nil
	}
	return true, changed, err
}

var errCapacityBindingRepairFenced = errors.New("capacity binding repair fenced")

type capacityBindingRepairTest struct {
	id        string
	projectID string
	status    string
	timeslot  *string
}

func (s *SQLiteStore) repairCapacityReservationBindings(leaseName, leaseOwnerID string, requireLease bool) (int64, error) {
	// Keep both memory and lock duration bounded on large installations. Every
	// batch is independently transactional and (in HA) rechecks the lease, so a
	// takeover can stop repair between batches without exposing a half-repaired
	// pair inside any committed batch.
	const batchSize = 200
	var totalChanged int64
	testCursor := ""
	for {
		changed, processed, nextCursor, err := s.repairCapacityBindingTestBatch(testCursor, batchSize, leaseName, leaseOwnerID, requireLease)
		totalChanged += changed
		if err != nil {
			return totalChanged, err
		}
		if processed == 0 {
			break
		}
		testCursor = nextCursor
	}
	reservationCursor := ""
	for {
		changed, processed, nextCursor, err := s.repairOrphanCapacityReservationBatch(reservationCursor, batchSize, leaseName, leaseOwnerID, requireLease)
		totalChanged += changed
		if err != nil {
			return totalChanged, err
		}
		if processed == 0 {
			break
		}
		reservationCursor = nextCursor
	}
	return totalChanged, nil
}

func (s *SQLiteStore) repairCapacityBindingTestBatch(afterID string, limit int, leaseName, leaseOwnerID string, requireLease bool) (int64, int, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, afterID, err
	}
	defer tx.Rollback()
	if requireLease {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
		if err != nil {
			return 0, 0, afterID, err
		}
		if !held {
			return 0, 0, afterID, errCapacityBindingRepairFenced
		}
	}

	// Discover a bounded page without row locks, then take locks in the global
	// reservation -> test order used by bind/reject/reset. The execution lease
	// serializes HA writers; the fresh locked reads below remain authoritative.
	idRows, err := tx.Query(`SELECT id FROM tests t
		WHERE id > ? AND (timeslot_id IS NOT NULL OR EXISTS (
			SELECT 1 FROM capacity_reservations r WHERE r.bound_test_id = t.id
		)) ORDER BY id LIMIT ?`, afterID, limit)
	if err != nil {
		return 0, 0, afterID, err
	}
	testIDs := make([]string, 0, limit)
	for idRows.Next() {
		var id string
		if err := idRows.Scan(&id); err != nil {
			idRows.Close()
			return 0, 0, afterID, err
		}
		testIDs = append(testIDs, id)
	}
	if err := idRows.Close(); err != nil {
		return 0, 0, afterID, err
	}
	if err := idRows.Err(); err != nil {
		return 0, 0, afterID, err
	}
	if len(testIDs) == 0 {
		return 0, 0, afterID, nil
	}
	placeholders := capacityBindingPlaceholders(len(testIDs))
	reservationArgs := make([]any, 0, len(testIDs)*2)
	for _, id := range testIDs {
		reservationArgs = append(reservationArgs, id)
	}
	for _, id := range testIDs {
		reservationArgs = append(reservationArgs, id)
	}
	reservationQuery := capacityReservationSelect + ` WHERE bound_test_id IN (` + placeholders + `)
		OR id IN (SELECT timeslot_id FROM tests WHERE id IN (` + placeholders + `) AND timeslot_id IS NOT NULL)
		ORDER BY id`
	if s.db.dialect == storeDialectPostgres {
		reservationQuery += ` FOR UPDATE`
	}
	reservationRows, err := tx.Query(reservationQuery, reservationArgs...)
	if err != nil {
		return 0, 0, afterID, err
	}
	reservations := make([]*CapacityReservation, 0, len(testIDs))
	for reservationRows.Next() {
		reservation, err := scanCapacityReservation(reservationRows)
		if err != nil {
			reservationRows.Close()
			return 0, 0, afterID, err
		}
		reservations = append(reservations, reservation)
	}
	if err := reservationRows.Close(); err != nil {
		return 0, 0, afterID, err
	}
	if err := reservationRows.Err(); err != nil {
		return 0, 0, afterID, err
	}

	testArgs := make([]any, len(testIDs))
	for index, id := range testIDs {
		testArgs[index] = id
	}
	testQuery := `SELECT id, COALESCE(project_id, ''), COALESCE(status, ''), timeslot_id
		FROM tests WHERE id IN (` + placeholders + `) ORDER BY id`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
	}
	testRows, err := tx.Query(testQuery, testArgs...)
	if err != nil {
		return 0, 0, afterID, err
	}
	tests := make(map[string]*capacityBindingRepairTest, len(testIDs))
	for testRows.Next() {
		test := &capacityBindingRepairTest{}
		if err := testRows.Scan(&test.id, &test.projectID, &test.status, &test.timeslot); err != nil {
			testRows.Close()
			return 0, 0, afterID, err
		}
		tests[test.id] = test
	}
	if err := testRows.Close(); err != nil {
		return 0, 0, afterID, err
	}
	if err := testRows.Err(); err != nil {
		return 0, 0, afterID, err
	}

	// Pick at most one valid reservation for each test. Prefer an already
	// reciprocal pair, then the lexicographically first legacy reservation.
	candidates := make(map[string][]*CapacityReservation)
	repairableReservations := make([]*CapacityReservation, 0, len(reservations))
	for _, reservation := range reservations {
		if reservation.BoundTestID == nil || strings.TrimSpace(*reservation.BoundTestID) == "" {
			continue
		}
		test := tests[strings.TrimSpace(*reservation.BoundTestID)]
		if test == nil {
			// The reservation may have been selected only because a page test
			// points at it while it belongs to another test. Do not disturb that
			// other pair; the page test's stale pointer is cleared below.
			continue
		}
		repairableReservations = append(repairableReservations, reservation)
		if strings.ToLower(strings.TrimSpace(reservation.ApprovalState)) != "approved" ||
			capacityBindingTerminalTestStatus(test.status) ||
			normalizeProjectID(test.projectID) != normalizeProjectID(reservation.ProjectID) {
			continue
		}
		candidates[test.id] = append(candidates[test.id], reservation)
	}
	canonicalByTest := make(map[string]*CapacityReservation)
	for testID, options := range candidates {
		test := tests[testID]
		for _, option := range options {
			if test.timeslot != nil && strings.TrimSpace(*test.timeslot) == option.ID {
				canonicalByTest[testID] = option
				break
			}
		}
		if canonicalByTest[testID] == nil {
			canonicalByTest[testID] = options[0]
		}
	}
	canonicalByReservation := make(map[string]string)
	for testID, reservation := range canonicalByTest {
		canonicalByReservation[reservation.ID] = testID
	}

	databaseNow, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return 0, 0, afterID, err
	}
	var changed int64
	for _, reservation := range repairableReservations {
		desiredTestID := canonicalByReservation[reservation.ID]
		currentTestID := ""
		if reservation.BoundTestID != nil {
			currentTestID = *reservation.BoundTestID
		}
		desiredStatus := ""
		if test := tests[desiredTestID]; test != nil {
			desiredStatus = test.status
		}
		currentStatus := ""
		if reservation.BoundTestStatus != nil {
			currentStatus = *reservation.BoundTestStatus
		}
		bindingMatches := (desiredTestID == "" && reservation.BoundTestID == nil && reservation.BoundTestStatus == nil) ||
			(desiredTestID != "" && reservation.BoundTestID != nil && currentTestID == desiredTestID &&
				reservation.BoundTestStatus != nil && currentStatus == desiredStatus)
		if bindingMatches {
			continue
		}
		var desiredTestArg, desiredStatusArg any
		if desiredTestID != "" {
			desiredTestArg = desiredTestID
			desiredStatusArg = desiredStatus
		}
		result, err := tx.Exec(`UPDATE capacity_reservations
			SET bound_test_id = ?, bound_test_status = ?, updated_at = ? WHERE id = ?`,
			desiredTestArg, desiredStatusArg, databaseNow, reservation.ID)
		if err != nil {
			return 0, 0, afterID, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, 0, afterID, err
		}
		changed += affected
	}
	for testID, test := range tests {
		desiredTimeslot := ""
		if reservation := canonicalByTest[testID]; reservation != nil {
			desiredTimeslot = reservation.ID
		}
		currentTimeslot := ""
		if test.timeslot != nil {
			currentTimeslot = *test.timeslot
		}
		bindingMatches := (desiredTimeslot == "" && test.timeslot == nil) ||
			(desiredTimeslot != "" && test.timeslot != nil && currentTimeslot == desiredTimeslot)
		if bindingMatches {
			continue
		}
		var desiredTimeslotArg any
		if desiredTimeslot != "" {
			desiredTimeslotArg = desiredTimeslot
		}
		result, err := tx.Exec(`UPDATE tests SET timeslot_id = ? WHERE id = ?`, desiredTimeslotArg, testID)
		if err != nil {
			return 0, 0, afterID, err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return 0, 0, afterID, err
		}
		changed += affected
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, afterID, err
	}
	return changed, len(testIDs), testIDs[len(testIDs)-1], nil
}

func (s *SQLiteStore) repairOrphanCapacityReservationBatch(afterID string, limit int, leaseName, leaseOwnerID string, requireLease bool) (int64, int, string, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, 0, afterID, err
	}
	defer tx.Rollback()
	if requireLease {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwnerID)
		if err != nil {
			return 0, 0, afterID, err
		}
		if !held {
			return 0, 0, afterID, errCapacityBindingRepairFenced
		}
	}
	query := `SELECT id FROM capacity_reservations r
		WHERE id > ? AND bound_test_id IS NOT NULL AND
		(TRIM(bound_test_id) = '' OR NOT EXISTS (SELECT 1 FROM tests t WHERE t.id = r.bound_test_id))
		ORDER BY id LIMIT ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	rows, err := tx.Query(query, afterID, limit)
	if err != nil {
		return 0, 0, afterID, err
	}
	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return 0, 0, afterID, err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return 0, 0, afterID, err
	}
	if err := rows.Err(); err != nil {
		return 0, 0, afterID, err
	}
	if len(ids) == 0 {
		return 0, 0, afterID, nil
	}
	databaseNow, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return 0, 0, afterID, err
	}
	args := make([]any, 0, len(ids)+1)
	args = append(args, databaseNow)
	for _, id := range ids {
		args = append(args, id)
	}
	result, err := tx.Exec(`UPDATE capacity_reservations
		SET bound_test_id = NULL, bound_test_status = NULL, updated_at = ?
		WHERE id IN (`+capacityBindingPlaceholders(len(ids))+`)`, args...)
	if err != nil {
		return 0, 0, afterID, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return 0, 0, afterID, err
	}
	if err := tx.Commit(); err != nil {
		return 0, 0, afterID, err
	}
	return changed, len(ids), ids[len(ids)-1], nil
}

func capacityBindingPlaceholders(count int) string {
	placeholders := make([]string, count)
	for index := range placeholders {
		placeholders[index] = "?"
	}
	return strings.Join(placeholders, ", ")
}
