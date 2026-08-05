package storage

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type scenarioScheduleOccurrenceLifecycle string

const (
	scenarioScheduleOccurrenceStarted scenarioScheduleOccurrenceLifecycle = "started"
	scenarioScheduleOccurrenceFailed  scenarioScheduleOccurrenceLifecycle = "failed"
	scenarioScheduleOccurrenceAborted scenarioScheduleOccurrenceLifecycle = "aborted"

	scenarioScheduleOccurrenceTestDeletedReason = "test_deleted_before_launch"
	scenarioScheduleOccurrenceTestDeletedError  = "scheduled test deleted before launch"
)

type lockedScenarioScheduleOccurrenceCorrelation struct {
	occurrenceID          string
	scheduleID            string
	occurrenceStatus      string
	occurrenceRevision    int64
	scheduleScenarioID    string
	scheduleProjectID     string
	scheduleLastRunTestID string
}

// lockScenarioScheduleOccurrenceCorrelation loads and validates the single
// calendar row for a test. PostgreSQL locks the parent schedule before the
// occurrence, matching operator-resolution transactions and avoiding the
// inverse lock order that can deadlock a concurrent resolution.
func lockScenarioScheduleOccurrenceCorrelation(tx *storeTx, testID string) (*lockedScenarioScheduleOccurrenceCorrelation, error) {
	var testProjectID, testScenarioID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, ''), COALESCE(scenario_id, '') FROM tests WHERE id = ?`, testID).
		Scan(&testProjectID, &testScenarioID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	if strings.TrimSpace(testScenarioID) == "" {
		return nil, nil
	}

	var occurrenceCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM scenario_schedule_occurrences WHERE test_id = ?`, testID).Scan(&occurrenceCount); err != nil {
		return nil, err
	}
	if occurrenceCount == 0 {
		return nil, nil
	}
	if occurrenceCount != 1 {
		return nil, fmt.Errorf("test %q is correlated to %d scenario schedule occurrences", testID, occurrenceCount)
	}

	correlation := &lockedScenarioScheduleOccurrenceCorrelation{}
	if err := tx.QueryRow(`SELECT id, schedule_id FROM scenario_schedule_occurrences WHERE test_id = ?`, testID).
		Scan(&correlation.occurrenceID, &correlation.scheduleID); err != nil {
		return nil, err
	}
	scheduleQuery := `SELECT scenario_id, COALESCE(project_id, ''), COALESCE(last_run_test_id, '') FROM scenario_schedules WHERE id = ?`
	if tx.dialect == storeDialectPostgres {
		scheduleQuery += ` FOR UPDATE`
	}
	if err := tx.QueryRow(scheduleQuery, correlation.scheduleID).Scan(
		&correlation.scheduleScenarioID, &correlation.scheduleProjectID, &correlation.scheduleLastRunTestID,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("scenario occurrence %q parent schedule is missing", correlation.occurrenceID)
		}
		return nil, err
	}

	occurrenceQuery := `SELECT scenario_id, COALESCE(project_id, ''), LOWER(TRIM(COALESCE(status, ''))), COALESCE(revision, 1)
		FROM scenario_schedule_occurrences WHERE id = ? AND schedule_id = ? AND test_id = ?`
	if tx.dialect == storeDialectPostgres {
		occurrenceQuery += ` FOR UPDATE`
	}
	var occurrenceScenarioID, occurrenceProjectID string
	if err := tx.QueryRow(occurrenceQuery, correlation.occurrenceID, correlation.scheduleID, testID).Scan(
		&occurrenceScenarioID, &occurrenceProjectID, &correlation.occurrenceStatus, &correlation.occurrenceRevision,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("scenario occurrence correlation for test %q changed while locking", testID)
		}
		return nil, err
	}
	if strings.TrimSpace(occurrenceScenarioID) != strings.TrimSpace(testScenarioID) ||
		normalizeProjectID(occurrenceProjectID) != normalizeProjectID(testProjectID) {
		return nil, fmt.Errorf("scenario occurrence %q placement does not match test %q", correlation.occurrenceID, testID)
	}
	if strings.TrimSpace(correlation.scheduleScenarioID) != strings.TrimSpace(testScenarioID) ||
		normalizeProjectID(correlation.scheduleProjectID) != normalizeProjectID(testProjectID) {
		return nil, fmt.Errorf("scenario occurrence %q parent schedule placement does not match test %q", correlation.occurrenceID, testID)
	}
	return correlation, nil
}

// reconcileQueuedScenarioScheduleOccurrenceForTestDeletion preserves an
// auditable terminal calendar row when its correlated test is deleted before
// launch. The generic graph cleanup below this call still removes references
// from already-terminal history, but a queued occurrence must not silently
// become an unreachable queued row after the parent schedule has advanced.
func reconcileQueuedScenarioScheduleOccurrenceForTestDeletion(tx *storeTx, testID string) error {
	if tx == nil {
		return errors.New("scenario occurrence deletion transaction is required")
	}
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return errors.New("scenario occurrence deletion test id is required")
	}

	correlation, err := lockScenarioScheduleOccurrenceCorrelation(tx, testID)
	if err != nil || correlation == nil {
		return err
	}
	if correlation.occurrenceStatus != "queued" {
		return nil
	}

	deletedAt, err := preciseDatabaseTime(tx, tx.dialect)
	if err != nil {
		return err
	}
	deletedAt = deletedAt.UTC()
	transitionToken := fmt.Sprintf("test-deleted:%s:%d", testID, deletedAt.UnixNano())

	// A newer materialization may already own the parent pointer. In that case,
	// retain its state and terminalize only this older occurrence.
	if strings.TrimSpace(correlation.scheduleLastRunTestID) == testID {
		result, err := tx.Exec(`UPDATE scenario_schedules SET
			last_run_test_id = NULL, last_run_at = NULL, last_error = ?, next_required_action = 'operator_decision_required',
			updated_at = ?, revision = COALESCE(revision, 1) + 1
			WHERE id = ? AND scenario_id = ? AND COALESCE(project_id, '') = ? AND COALESCE(last_run_test_id, '') = ?`,
			scenarioScheduleOccurrenceTestDeletedError, deletedAt, correlation.scheduleID,
			correlation.scheduleScenarioID, correlation.scheduleProjectID, testID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("scenario occurrence %q parent schedule changed during test deletion", correlation.occurrenceID)
		}
	}

	result, err := tx.Exec(`UPDATE scenario_schedule_occurrences SET
		test_id = NULL, materialized_at = NULL, status = 'failed', reason_code = ?, conflict_summary = ?,
		transition_token = ?, revision = COALESCE(revision, 1) + 1, updated_at = ?
		WHERE id = ? AND schedule_id = ? AND test_id = ? AND LOWER(TRIM(COALESCE(status, ''))) = 'queued' AND COALESCE(revision, 1) = ?`,
		scenarioScheduleOccurrenceTestDeletedReason, scenarioScheduleOccurrenceTestDeletedError, transitionToken, deletedAt,
		correlation.occurrenceID, correlation.scheduleID, testID, correlation.occurrenceRevision)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("queued scenario occurrence %q changed during test deletion", correlation.occurrenceID)
	}
	return nil
}

// reconcileQueuedScenarioScheduleOccurrenceForTest changes the calendar row
// correlated to one durable test in the caller's test-lifecycle transaction.
// In addition to queued promotion/cancellation, a caller that proves the prior
// test state was INITIALIZING may withdraw an already-published materialization
// when Stop wins before backend dispatch. Test IDs are globally unique, but the
// count and placement checks deliberately fail closed if durable corruption
// could make the identity ambiguous.
func reconcileQueuedScenarioScheduleOccurrenceForTest(tx *storeTx, testID string, lifecycle scenarioScheduleOccurrenceLifecycle, lifecycleAt time.Time, failure string, allowMaterializedPrelaunchAbort bool) error {
	if tx == nil {
		return errors.New("scenario occurrence lifecycle transaction is required")
	}
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return errors.New("scenario occurrence lifecycle test id is required")
	}
	if lifecycle != scenarioScheduleOccurrenceStarted && lifecycle != scenarioScheduleOccurrenceFailed && lifecycle != scenarioScheduleOccurrenceAborted {
		return fmt.Errorf("unsupported scenario occurrence lifecycle %q", lifecycle)
	}

	correlation, err := lockScenarioScheduleOccurrenceCorrelation(tx, testID)
	if err != nil || correlation == nil {
		return err
	}
	materializedPrelaunchAbort := allowMaterializedPrelaunchAbort &&
		lifecycle == scenarioScheduleOccurrenceAborted && correlation.occurrenceStatus == "materialized"
	if correlation.occurrenceStatus != "queued" && !materializedPrelaunchAbort {
		return nil
	}

	if lifecycleAt.IsZero() {
		var err error
		lifecycleAt, err = preciseDatabaseTime(tx, tx.dialect)
		if err != nil {
			return err
		}
	}
	lifecycleAt = lifecycleAt.UTC()
	transitionToken := fmt.Sprintf("test-lifecycle:%s:%s:%d", lifecycle, testID, lifecycleAt.UnixNano())

	switch lifecycle {
	case scenarioScheduleOccurrenceStarted:
		result, err := tx.Exec(`UPDATE scenario_schedules SET
			last_run_test_id = ?, last_run_at = ?, updated_at = ?, revision = COALESCE(revision, 1) + 1
			WHERE id = ? AND scenario_id = ? AND COALESCE(project_id, '') = ?`,
			testID, lifecycleAt, lifecycleAt, correlation.scheduleID, correlation.scheduleScenarioID, correlation.scheduleProjectID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("queued scenario occurrence %q parent schedule changed during test promotion", correlation.occurrenceID)
		}
		result, err = tx.Exec(`UPDATE scenario_schedule_occurrences SET
			materialized_at = ?, status = 'materialized', reason_code = 'started', conflict_summary = '',
			transition_token = ?, revision = COALESCE(revision, 1) + 1, updated_at = ?
			WHERE id = ? AND schedule_id = ? AND test_id = ? AND LOWER(TRIM(COALESCE(status, ''))) = 'queued' AND COALESCE(revision, 1) = ?`,
			lifecycleAt, transitionToken, lifecycleAt, correlation.occurrenceID, correlation.scheduleID, testID, correlation.occurrenceRevision)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("queued scenario occurrence %q changed during test promotion", correlation.occurrenceID)
		}
	case scenarioScheduleOccurrenceFailed, scenarioScheduleOccurrenceAborted:
		failure = strings.TrimSpace(failure)
		if failure == "" {
			if lifecycle == scenarioScheduleOccurrenceAborted {
				failure = "pending test cancelled before launch"
			} else {
				failure = "pending test failed before launch"
			}
		}
		// Only the schedule's current run pointer receives the summary. An older
		// pending occurrence may fail after a newer occurrence was materialized;
		// its own history row must change without overwriting newer schedule state.
		if strings.TrimSpace(correlation.scheduleLastRunTestID) == testID {
			nextRequiredAction := "operator_decision_required"
			if lifecycle == scenarioScheduleOccurrenceAborted {
				nextRequiredAction = ""
			}
			query := `UPDATE scenario_schedules SET
				last_error = ?, next_required_action = ?, updated_at = ?, revision = COALESCE(revision, 1) + 1
				WHERE id = ? AND scenario_id = ? AND COALESCE(project_id, '') = ? AND COALESCE(last_run_test_id, '') = ?`
			if materializedPrelaunchAbort {
				// The scheduler advanced next_run_at before releasing the lifecycle
				// gate, but this exact run never crossed the backend boundary. Keep the
				// test pointer for audit correlation while withdrawing the false
				// successful-start timestamp.
				query = `UPDATE scenario_schedules SET
					last_run_at = NULL, last_error = ?, next_required_action = ?, updated_at = ?, revision = COALESCE(revision, 1) + 1
					WHERE id = ? AND scenario_id = ? AND COALESCE(project_id, '') = ? AND COALESCE(last_run_test_id, '') = ?`
			}
			result, err := tx.Exec(query, failure, nextRequiredAction, lifecycleAt, correlation.scheduleID,
				correlation.scheduleScenarioID, correlation.scheduleProjectID, testID)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return fmt.Errorf("scenario occurrence %q parent schedule changed during terminalization", correlation.occurrenceID)
			}
		}
		reasonCode := "pending_promotion_failed"
		if lifecycle == scenarioScheduleOccurrenceAborted {
			reasonCode = "pending_promotion_aborted"
			if materializedPrelaunchAbort {
				reasonCode = "prelaunch_aborted"
			}
		}
		result, err := tx.Exec(`UPDATE scenario_schedule_occurrences SET
			materialized_at = NULL, status = 'failed', reason_code = ?, conflict_summary = ?,
			transition_token = ?, revision = COALESCE(revision, 1) + 1, updated_at = ?
			WHERE id = ? AND schedule_id = ? AND test_id = ? AND LOWER(TRIM(COALESCE(status, ''))) = ? AND COALESCE(revision, 1) = ?`,
			reasonCode, failure, transitionToken, lifecycleAt, correlation.occurrenceID, correlation.scheduleID, testID,
			correlation.occurrenceStatus, correlation.occurrenceRevision)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("scenario occurrence %q changed during terminalization", correlation.occurrenceID)
		}
	}
	return nil
}
