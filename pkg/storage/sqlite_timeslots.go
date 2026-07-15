package storage

// SQLite store — capacity reservations (timeslots). Forward-booking of a future
// capacity window, persisted alongside scenario schedules. The same SQL serves
// the PostgreSQL backend via rewriteSQL.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const capacitySettingsFenceLockKey = "settings-reservations"

func acquireCapacitySettingsFence(tx *storeTx, updatedAt time.Time) error {
	_, err := tx.Exec(`INSERT INTO capacity_approval_locks (lock_key, updated_at) VALUES (?, ?)
		ON CONFLICT(lock_key) DO UPDATE SET updated_at=excluded.updated_at`, capacitySettingsFenceLockKey, updatedAt.UTC())
	return err
}

func (s *SQLiteStore) SaveCapacityReservation(reservation *CapacityReservation) error {
	if reservation == nil {
		return fmt.Errorf("capacity reservation is required")
	}
	reservation.ProjectID = normalizeProjectID(reservation.ProjectID)
	if reservation.ApprovalState == "" {
		reservation.ApprovalState = "pending"
	}
	reservation.StartAt = reservation.StartAt.UTC()
	reservation.EndAt = reservation.EndAt.UTC()
	now := time.Now().UTC()
	if reservation.CreatedAt.IsZero() {
		reservation.CreatedAt = now
	}
	reservation.UpdatedAt = now

	var requestedCapacityJSON string
	if reservation.RequestedCapacity != nil {
		encoded, err := json.Marshal(reservation.RequestedCapacity)
		if err != nil {
			return fmt.Errorf("marshal requested_capacity: %w", err)
		}
		requestedCapacityJSON = string(encoded)
	}

	_, err := s.db.Exec(`INSERT INTO capacity_reservations (
			id, project_id, scenario_id, start_at, end_at, timezone, requested_capacity_json,
			approval_state, approval_id, bound_test_id, bound_test_status, conflict_summary,
			created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id,
			scenario_id=excluded.scenario_id,
			start_at=excluded.start_at,
			end_at=excluded.end_at,
			timezone=excluded.timezone,
			requested_capacity_json=excluded.requested_capacity_json,
			approval_state=excluded.approval_state,
			approval_id=excluded.approval_id,
			bound_test_id=excluded.bound_test_id,
			bound_test_status=excluded.bound_test_status,
			conflict_summary=excluded.conflict_summary,
			updated_at=excluded.updated_at`,
		reservation.ID, reservation.ProjectID, reservation.ScenarioID, reservation.StartAt, reservation.EndAt,
		reservation.Timezone, requestedCapacityJSON,
		reservation.ApprovalState, reservation.ApprovalID, reservation.BoundTestID, reservation.BoundTestStatus,
		reservation.ConflictSummary, reservation.CreatedAt, reservation.UpdatedAt)
	return err
}

func (s *SQLiteStore) GetCapacityReservation(id string) (*CapacityReservation, error) {
	row := s.db.QueryRow(capacityReservationSelect+` WHERE id = ?`, id)
	return scanCapacityReservation(row)
}

func (s *SQLiteStore) ListCapacityReservations(projectID string, limit int, offset int) ([]*CapacityReservation, error) {
	args := []any{}
	query := capacityReservationSelect
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY start_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	return s.queryCapacityReservations(query, args...)
}

func (s *SQLiteStore) ListCapacityReservationsByTimeRange(projectID string, startAt, endAt time.Time) ([]*CapacityReservation, error) {
	startAt = startAt.UTC()
	endAt = endAt.UTC()
	// Half-open overlap: an existing reservation [start_at, end_at) overlaps the
	// requested window [startAt, endAt) iff start_at < endAt AND end_at > startAt.
	args := []any{}
	query := capacityReservationSelect + ` WHERE start_at < ? AND end_at > ?`
	args = append(args, endAt, startAt)
	if projectID != "" {
		query += ` AND project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY start_at ASC`
	return s.queryCapacityReservations(query, args...)
}

func (s *SQLiteStore) UpdateCapacityReservationDetails(reservation *CapacityReservation, resetApproval bool) error {
	if reservation == nil {
		return fmt.Errorf("capacity reservation is required")
	}
	reservation.ProjectID = normalizeProjectID(reservation.ProjectID)
	reservation.StartAt = reservation.StartAt.UTC()
	reservation.EndAt = reservation.EndAt.UTC()
	expectedUpdatedAt := reservation.UpdatedAt.UTC()
	reservation.UpdatedAt = time.Now().UTC()

	var requestedCapacityJSON string
	if reservation.RequestedCapacity != nil {
		encoded, err := json.Marshal(reservation.RequestedCapacity)
		if err != nil {
			return fmt.Errorf("marshal requested_capacity: %w", err)
		}
		requestedCapacityJSON = string(encoded)
	}

	if resetApproval {
		if reservation.ApprovalState == "" {
			reservation.ApprovalState = "pending"
		}
		tx, err := s.db.Begin()
		if err != nil {
			return err
		}
		defer tx.Rollback()
		lockQuery := `SELECT bound_test_id FROM capacity_reservations WHERE id = ?`
		if s.db.dialect == storeDialectPostgres {
			lockQuery += ` FOR UPDATE`
		}
		var previousBoundTestID *string
		if err := tx.QueryRow(lockQuery, reservation.ID).Scan(&previousBoundTestID); err != nil {
			return err
		}
		result, err := tx.Exec(`UPDATE capacity_reservations
			SET project_id = ?,
				scenario_id = ?,
				start_at = ?,
				end_at = ?,
				timezone = ?,
				requested_capacity_json = ?,
				approval_state = ?,
				approval_id = ?,
				bound_test_id = ?,
				bound_test_status = ?,
				conflict_summary = ?,
				updated_at = ?
			WHERE id = ? AND updated_at = ? AND approval_state = 'approved'`,
			reservation.ProjectID, reservation.ScenarioID, reservation.StartAt, reservation.EndAt,
			reservation.Timezone, requestedCapacityJSON,
			reservation.ApprovalState, reservation.ApprovalID, reservation.BoundTestID, reservation.BoundTestStatus,
			reservation.ConflictSummary, reservation.UpdatedAt, reservation.ID, expectedUpdatedAt)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("capacity reservation changed concurrently; reload and retry")
		}
		// Enlarging or moving an approved reservation invalidates its approval
		// and binding. Clear the test-side pointer in this same transaction so a
		// concurrent launch cannot retain a reservation that no longer protects it.
		if _, err := tx.Exec(`UPDATE tests SET timeslot_id = NULL, capacity_window_start_at = NULL, capacity_window_end_at = NULL WHERE timeslot_id = ?`, reservation.ID); err != nil {
			return err
		}
		return tx.Commit()
	}

	result, err := s.db.Exec(`UPDATE capacity_reservations
		SET project_id = ?,
			scenario_id = ?,
			start_at = ?,
			end_at = ?,
			timezone = ?,
			requested_capacity_json = ?,
			conflict_summary = ?,
			updated_at = ?
		WHERE id = ? AND updated_at = ?`,
		reservation.ProjectID, reservation.ScenarioID, reservation.StartAt, reservation.EndAt,
		reservation.Timezone, requestedCapacityJSON, reservation.ConflictSummary, reservation.UpdatedAt, reservation.ID, expectedUpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("capacity reservation changed concurrently; reload and retry")
	}
	return nil
}

func (s *SQLiteStore) UpdateCapacityReservationBinding(id string, testID *string, testStatus *string, updatedAt time.Time) (bool, error) {
	if testID == nil || strings.TrimSpace(*testID) == "" {
		return false, fmt.Errorf("test id is required")
	}
	// Retained for Store compatibility, but now delegates to the reciprocal
	// two-row transaction. testStatus is intentionally ignored; the durable test
	// row is the authoritative lifecycle source.
	result, err := s.BindCapacityReservationToTest(id, strings.TrimSpace(*testID), updatedAt)
	return result == CapacityBindingBound, err
}

// UpdateCapacityReservationBindingIfLeaseHeld closes the create-time gap
// between checking execution leadership and binding a timeslot. The reservation
// CAS and the durable lease-owner/expiry predicate are evaluated by one UPDATE,
// so an expired execution epoch cannot leave a binding for a test it may no
// longer create.
func (s *SQLiteStore) UpdateCapacityReservationBindingIfLeaseHeld(id string, testID *string, testStatus *string, updatedAt time.Time, leaseName, leaseOwnerID string) (bool, error) {
	if testID == nil || strings.TrimSpace(*testID) == "" {
		return false, fmt.Errorf("test id is required")
	}
	result, err := s.BindCapacityReservationToTestIfLeaseHeld(id, strings.TrimSpace(*testID), updatedAt, leaseName, leaseOwnerID)
	return result == CapacityBindingBound, err
}

func (s *SQLiteStore) ClearCapacityReservationBinding(id string, testID string, updatedAt time.Time) (bool, error) {
	result, err := s.clearCapacityReservationBindingAndTest(id, testID, updatedAt, "", "", false)
	return result == CapacityBindingReleased, err
}

func (s *SQLiteStore) UpdateCapacityReservationApprovalState(id string, fromStates []string, toState string, updatedAt time.Time) (bool, error) {
	// Atomic compare-and-set on approval_state: flip to toState only when the row
	// is still in one of fromStates. This closes the read-modify-write TOCTOU in
	// the approve/reject handler so a stale decision cannot clobber a concurrent
	// one (e.g. an approve landing after a reject, or a double-approve).
	if len(fromStates) == 0 {
		return false, fmt.Errorf("fromStates is required")
	}
	placeholders := make([]string, len(fromStates))
	args := make([]any, 0, len(fromStates)+3)
	args = append(args, toState, updatedAt.UTC(), id)
	for i, st := range fromStates {
		placeholders[i] = "?"
		args = append(args, st)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	lockQuery := `SELECT bound_test_id FROM capacity_reservations WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		lockQuery += ` FOR UPDATE`
	}
	var boundTestID *string
	if err := tx.QueryRow(lockQuery, id).Scan(&boundTestID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	clearBinding := strings.ToLower(strings.TrimSpace(toState)) != "approved"
	query := `UPDATE capacity_reservations SET approval_state = ?, updated_at = ?`
	if clearBinding {
		query += `, bound_test_id = NULL, bound_test_status = NULL`
	}
	query += ` WHERE id = ? AND approval_state IN (` + strings.Join(placeholders, ", ") + `)`
	result, err := tx.Exec(query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected == 0 {
		return false, nil
	}
	if clearBinding {
		if _, err := tx.Exec(`UPDATE tests SET timeslot_id = NULL, capacity_window_start_at = NULL, capacity_window_end_at = NULL WHERE timeslot_id = ?`, id); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// ApproveCapacityReservationWithCapacityFence atomically evaluates a pending
// reservation against both the current durable settings value and every approved
// overlapping reservation. The shared settings-reservations row lock is also
// taken by capacity-aware settings writes, so either write order observes the
// other before it commits.
func (s *SQLiteStore) ApproveCapacityReservationWithCapacityFence(
	id string,
	updatedAt time.Time,
	settingKey string,
	overbooked func(target *CapacityReservation, approved []*CapacityReservation, settingValue string) (bool, error),
) (bool, bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, false, fmt.Errorf("capacity reservation id is required")
	}
	if strings.TrimSpace(settingKey) == "" {
		return false, false, fmt.Errorf("capacity settings key is required")
	}
	if overbooked == nil {
		return false, false, fmt.Errorf("capacity overbooking evaluator is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	if err := acquireCapacitySettingsFence(tx, updatedAt); err != nil {
		return false, false, err
	}
	targetQuery := capacityReservationSelect + ` WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		targetQuery += ` FOR UPDATE`
	}
	target, err := scanCapacityReservation(tx.QueryRow(targetQuery, id))
	if err != nil {
		return false, false, err
	}
	if target.ApprovalState != "pending" {
		return false, false, nil
	}
	settingQuery := `SELECT value FROM app_settings WHERE key = ?`
	if s.db.dialect == storeDialectPostgres {
		settingQuery += ` FOR UPDATE`
	}
	settingValue := ""
	if err := tx.QueryRow(settingQuery, settingKey).Scan(&settingValue); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return false, false, err
	}
	rows, err := tx.Query(capacityReservationSelect+` WHERE id != ? AND approval_state = 'approved' AND start_at < ? AND end_at > ?`, target.ID, target.EndAt.UTC(), target.StartAt.UTC())
	if err != nil {
		return false, false, err
	}
	approved := make([]*CapacityReservation, 0)
	for rows.Next() {
		reservation, scanErr := scanCapacityReservation(rows)
		if scanErr != nil {
			rows.Close()
			return false, false, scanErr
		}
		approved = append(approved, reservation)
	}
	if err := rows.Close(); err != nil {
		return false, false, err
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}
	conflict, err := overbooked(target, approved, settingValue)
	if err != nil {
		return false, false, err
	}
	if conflict {
		return false, true, nil
	}
	result, err := tx.Exec(`UPDATE capacity_reservations SET approval_state='approved', updated_at=? WHERE id=? AND approval_state='pending' AND updated_at=?`, updatedAt.UTC(), id, target.UpdatedAt.UTC())
	if err != nil {
		return false, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, false, err
	}
	if affected != 1 {
		return false, false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return true, false, nil
}

// ApproveCapacityReservationWithinLimit serializes approvals that draw from the
// same capacity envelope and performs the overlap calculation inside the same
// transaction as the pending→approved transition. This closes the race where
// two different overlapping reservations both pass a pre-CAS aggregate check.
func (s *SQLiteStore) ApproveCapacityReservationWithinLimit(
	id string,
	updatedAt time.Time,
	capacity func(*CapacityReservation) (envelope string, maxUnits int),
	units func(*CapacityReservation) int,
) (bool, bool, error) {
	if strings.TrimSpace(id) == "" {
		return false, false, fmt.Errorf("capacity reservation id is required")
	}
	if capacity == nil {
		return false, false, fmt.Errorf("capacity reservation envelope resolver is required")
	}
	if units == nil {
		return false, false, fmt.Errorf("capacity reservation unit calculator is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, false, err
	}
	defer tx.Rollback()
	target, err := scanCapacityReservation(tx.QueryRow(capacityReservationSelect+` WHERE id = ?`, id))
	if err != nil {
		return false, false, err
	}
	if target.ApprovalState != "pending" {
		return false, false, nil
	}
	envelope, maxUnits := capacity(target)
	envelope = strings.TrimSpace(envelope)
	lockKey := "global"
	if envelope != "" {
		lockKey = "pool:" + envelope
	}
	if _, err := tx.Exec(`INSERT INTO capacity_approval_locks (lock_key, updated_at) VALUES (?, ?) ON CONFLICT(lock_key) DO UPDATE SET updated_at=excluded.updated_at`, lockKey, updatedAt.UTC()); err != nil {
		return false, false, err
	}
	// An unresolved envelope is not an unlimited envelope. Treat it as a
	// conflict so a transient/missing fleet snapshot cannot approve capacity
	// without a bound.
	if maxUnits < 0 {
		return false, true, nil
	}
	total := units(target)
	if total < 0 {
		return false, false, fmt.Errorf("capacity reservation unit calculator returned a negative value for %s", target.ID)
	}
	if total > maxUnits {
		return false, true, nil
	}
	rows, err := tx.Query(capacityReservationSelect+` WHERE id != ? AND approval_state = 'approved' AND start_at < ? AND end_at > ?`, target.ID, target.EndAt.UTC(), target.StartAt.UTC())
	if err != nil {
		return false, false, err
	}
	for rows.Next() {
		reservation, scanErr := scanCapacityReservation(rows)
		if scanErr != nil {
			rows.Close()
			return false, false, scanErr
		}
		reservationEnvelope := ""
		if reservation.RequestedCapacity != nil {
			reservationEnvelope = strings.TrimSpace(reservation.RequestedCapacity.PoolID)
		}
		if reservationEnvelope == envelope {
			reservationUnits := units(reservation)
			if reservationUnits < 0 {
				rows.Close()
				return false, false, fmt.Errorf("capacity reservation unit calculator returned a negative value for %s", reservation.ID)
			}
			// Compare before adding. Besides avoiding native-int wraparound, this
			// short-circuits work as soon as the envelope is known to be full.
			if reservationUnits > maxUnits-total {
				rows.Close()
				return false, true, nil
			}
			total += reservationUnits
		}
	}
	if err := rows.Close(); err != nil {
		return false, false, err
	}
	if err := rows.Err(); err != nil {
		return false, false, err
	}
	result, err := tx.Exec(`UPDATE capacity_reservations SET approval_state='approved', updated_at=? WHERE id=? AND approval_state='pending' AND updated_at=?`, updatedAt.UTC(), id, target.UpdatedAt.UTC())
	if err != nil {
		return false, false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, false, err
	}
	if affected != 1 {
		return false, false, nil
	}
	if err := tx.Commit(); err != nil {
		return false, false, err
	}
	return true, false, nil
}

func (s *SQLiteStore) DeleteCapacityReservationsEndedBefore(cutoff time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tests SET timeslot_id = NULL, capacity_window_start_at = NULL, capacity_window_end_at = NULL
		WHERE timeslot_id IN (
			SELECT reservation.id FROM capacity_reservations reservation
			WHERE reservation.end_at < ?
			  AND (TRIM(COALESCE(reservation.bound_test_id, '')) = '' OR NOT EXISTS (
				SELECT 1 FROM tests bound_test
				WHERE bound_test.id = reservation.bound_test_id
				  AND UPPER(TRIM(COALESCE(bound_test.status, ''))) NOT IN ('COMPLETE', 'FAILED', 'ABORTED', 'STOPPED', 'CANCELLED')
			  ))
		)`, cutoff.UTC()); err != nil {
		return 0, err
	}
	result, err := tx.Exec(`DELETE FROM capacity_reservations
		WHERE end_at < ?
		  AND (TRIM(COALESCE(bound_test_id, '')) = '' OR NOT EXISTS (
			SELECT 1 FROM tests bound_test
			WHERE bound_test.id = capacity_reservations.bound_test_id
			  AND UPPER(TRIM(COALESCE(bound_test.status, ''))) NOT IN ('COMPLETE', 'FAILED', 'ABORTED', 'STOPPED', 'CANCELLED')
		  ))`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *SQLiteStore) DeleteRejectedCapacityReservationsBefore(cutoff time.Time) (int64, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`UPDATE tests SET timeslot_id = NULL, capacity_window_start_at = NULL, capacity_window_end_at = NULL
		WHERE timeslot_id IN (
			SELECT reservation.id FROM capacity_reservations reservation
			WHERE reservation.approval_state = 'rejected' AND reservation.updated_at < ?
			  AND (TRIM(COALESCE(reservation.bound_test_id, '')) = '' OR NOT EXISTS (
				SELECT 1 FROM tests bound_test
				WHERE bound_test.id = reservation.bound_test_id
				  AND UPPER(TRIM(COALESCE(bound_test.status, ''))) NOT IN ('COMPLETE', 'FAILED', 'ABORTED', 'STOPPED', 'CANCELLED')
			  ))
		)`, cutoff.UTC()); err != nil {
		return 0, err
	}
	result, err := tx.Exec(`DELETE FROM capacity_reservations
		WHERE approval_state = 'rejected' AND updated_at < ?
		  AND (TRIM(COALESCE(bound_test_id, '')) = '' OR NOT EXISTS (
			SELECT 1 FROM tests bound_test
			WHERE bound_test.id = capacity_reservations.bound_test_id
			  AND UPPER(TRIM(COALESCE(bound_test.status, ''))) NOT IN ('COMPLETE', 'FAILED', 'ABORTED', 'STOPPED', 'CANCELLED')
		  ))`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *SQLiteStore) DeleteCapacityReservation(id string) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	lockQuery := `SELECT approval_state, bound_test_id FROM capacity_reservations WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		lockQuery += ` FOR UPDATE`
	}
	var approvalState string
	var boundTestID *string
	if err := tx.QueryRow(lockQuery, id).Scan(&approvalState, &boundTestID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if strings.EqualFold(strings.TrimSpace(approvalState), "approved") || (boundTestID != nil && strings.TrimSpace(*boundTestID) != "") {
		return fmt.Errorf("approved or bound capacity reservation cannot be deleted; reject and unbind it first")
	}
	if _, err := tx.Exec(`UPDATE tests SET timeslot_id = NULL, capacity_window_start_at = NULL, capacity_window_end_at = NULL WHERE timeslot_id = ?`, id); err != nil {
		return err
	}
	result, err := tx.Exec(`DELETE FROM capacity_reservations WHERE id = ? AND bound_test_id IS NULL AND approval_state != 'approved'`, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return tx.Commit()
	}
	return fmt.Errorf("capacity reservation changed concurrently; reload and retry")
}

const capacityReservationSelect = `SELECT id, COALESCE(project_id, ''), scenario_id, start_at, end_at,
	COALESCE(timezone, ''), COALESCE(requested_capacity_json, ''), COALESCE(approval_state, 'pending'),
	approval_id, bound_test_id, bound_test_status, COALESCE(conflict_summary, ''), created_at, updated_at
	FROM capacity_reservations`

func (s *SQLiteStore) queryCapacityReservations(query string, args ...any) ([]*CapacityReservation, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	reservations := make([]*CapacityReservation, 0)
	for rows.Next() {
		reservation, err := scanCapacityReservation(rows)
		if err != nil {
			return nil, err
		}
		reservations = append(reservations, reservation)
	}
	return reservations, rows.Err()
}

// capacityRowScanner is satisfied by both *sql.Row and *sql.Rows.
type capacityRowScanner interface {
	Scan(dest ...any) error
}

func scanCapacityReservation(row capacityRowScanner) (*CapacityReservation, error) {
	var (
		reservation           CapacityReservation
		requestedCapacityJSON string
	)
	// Nullable columns (scenario_id, approval_id, bound_test_id,
	// bound_test_status) scan straight into their *string fields: a SQL NULL
	// sets the pointer to nil, matching the GetScript/folder_id pattern.
	if err := row.Scan(
		&reservation.ID, &reservation.ProjectID, &reservation.ScenarioID, &reservation.StartAt, &reservation.EndAt,
		&reservation.Timezone, &requestedCapacityJSON, &reservation.ApprovalState,
		&reservation.ApprovalID, &reservation.BoundTestID, &reservation.BoundTestStatus, &reservation.ConflictSummary,
		&reservation.CreatedAt, &reservation.UpdatedAt,
	); err != nil {
		return nil, err
	}
	if requestedCapacityJSON != "" && requestedCapacityJSON != "null" {
		var capacity CapacityReservationRequest
		if err := json.Unmarshal([]byte(requestedCapacityJSON), &capacity); err != nil {
			return nil, fmt.Errorf("unmarshal requested_capacity_json: %w", err)
		}
		reservation.RequestedCapacity = &capacity
	}
	return &reservation, nil
}
