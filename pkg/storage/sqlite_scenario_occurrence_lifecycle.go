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
)

// reconcileQueuedScenarioScheduleOccurrenceForTest changes the calendar row
// correlated to one durable test in the caller's test-lifecycle transaction.
// Test IDs are globally unique, but the count and placement checks deliberately
// fail closed if durable corruption could make the identity ambiguous.
func reconcileQueuedScenarioScheduleOccurrenceForTest(tx *storeTx, testID string, lifecycle scenarioScheduleOccurrenceLifecycle, lifecycleAt time.Time, failure string) error {
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

	var testProjectID, testScenarioID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, ''), COALESCE(scenario_id, '') FROM tests WHERE id = ?`, testID).
		Scan(&testProjectID, &testScenarioID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if strings.TrimSpace(testScenarioID) == "" {
		return nil
	}

	var occurrenceCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM scenario_schedule_occurrences WHERE test_id = ?`, testID).Scan(&occurrenceCount); err != nil {
		return err
	}
	if occurrenceCount == 0 {
		return nil
	}
	if occurrenceCount != 1 {
		return fmt.Errorf("test %q is correlated to %d scenario schedule occurrences", testID, occurrenceCount)
	}

	query := `SELECT id, schedule_id, scenario_id, COALESCE(project_id, ''), LOWER(TRIM(COALESCE(status, ''))), COALESCE(revision, 1)
		FROM scenario_schedule_occurrences WHERE test_id = ?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var occurrenceID, scheduleID, occurrenceScenarioID, occurrenceProjectID, occurrenceStatus string
	var occurrenceRevision int64
	if err := tx.QueryRow(query, testID).Scan(
		&occurrenceID, &scheduleID, &occurrenceScenarioID, &occurrenceProjectID, &occurrenceStatus, &occurrenceRevision,
	); err != nil {
		return err
	}
	if occurrenceStatus != "queued" {
		return nil
	}
	if strings.TrimSpace(occurrenceScenarioID) != strings.TrimSpace(testScenarioID) ||
		normalizeProjectID(occurrenceProjectID) != normalizeProjectID(testProjectID) {
		return fmt.Errorf("queued scenario occurrence %q placement does not match test %q", occurrenceID, testID)
	}

	scheduleQuery := `SELECT scenario_id, COALESCE(project_id, ''), COALESCE(last_run_test_id, '') FROM scenario_schedules WHERE id = ?`
	if tx.dialect == storeDialectPostgres {
		scheduleQuery += ` FOR UPDATE`
	}
	var scheduleScenarioID, scheduleProjectID, scheduleLastRunTestID string
	if err := tx.QueryRow(scheduleQuery, scheduleID).Scan(&scheduleScenarioID, &scheduleProjectID, &scheduleLastRunTestID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("queued scenario occurrence %q parent schedule is missing", occurrenceID)
		}
		return err
	}
	if strings.TrimSpace(scheduleScenarioID) != strings.TrimSpace(testScenarioID) ||
		normalizeProjectID(scheduleProjectID) != normalizeProjectID(testProjectID) {
		return fmt.Errorf("queued scenario occurrence %q parent schedule placement does not match test %q", occurrenceID, testID)
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
			testID, lifecycleAt, lifecycleAt, scheduleID, scheduleScenarioID, scheduleProjectID)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("queued scenario occurrence %q parent schedule changed during test promotion", occurrenceID)
		}
		result, err = tx.Exec(`UPDATE scenario_schedule_occurrences SET
			materialized_at = ?, status = 'materialized', reason_code = 'started', conflict_summary = '',
			transition_token = ?, revision = COALESCE(revision, 1) + 1, updated_at = ?
			WHERE id = ? AND schedule_id = ? AND test_id = ? AND LOWER(TRIM(COALESCE(status, ''))) = 'queued' AND COALESCE(revision, 1) = ?`,
			lifecycleAt, transitionToken, lifecycleAt, occurrenceID, scheduleID, testID, occurrenceRevision)
		if err != nil {
			return err
		}
		affected, err = result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("queued scenario occurrence %q changed during test promotion", occurrenceID)
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
		if strings.TrimSpace(scheduleLastRunTestID) == testID {
			nextRequiredAction := "operator_decision_required"
			if lifecycle == scenarioScheduleOccurrenceAborted {
				nextRequiredAction = ""
			}
			result, err := tx.Exec(`UPDATE scenario_schedules SET
				last_error = ?, next_required_action = ?, updated_at = ?, revision = COALESCE(revision, 1) + 1
				WHERE id = ? AND scenario_id = ? AND COALESCE(project_id, '') = ? AND COALESCE(last_run_test_id, '') = ?`,
				failure, nextRequiredAction, lifecycleAt, scheduleID, scheduleScenarioID, scheduleProjectID, testID)
			if err != nil {
				return err
			}
			affected, err := result.RowsAffected()
			if err != nil {
				return err
			}
			if affected != 1 {
				return fmt.Errorf("queued scenario occurrence %q parent schedule changed during terminalization", occurrenceID)
			}
		}
		reasonCode := "pending_promotion_failed"
		if lifecycle == scenarioScheduleOccurrenceAborted {
			reasonCode = "pending_promotion_aborted"
		}
		result, err := tx.Exec(`UPDATE scenario_schedule_occurrences SET
			materialized_at = NULL, status = 'failed', reason_code = ?, conflict_summary = ?,
			transition_token = ?, revision = COALESCE(revision, 1) + 1, updated_at = ?
			WHERE id = ? AND schedule_id = ? AND test_id = ? AND LOWER(TRIM(COALESCE(status, ''))) = 'queued' AND COALESCE(revision, 1) = ?`,
			reasonCode, failure, transitionToken, lifecycleAt, occurrenceID, scheduleID, testID, occurrenceRevision)
		if err != nil {
			return err
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if affected != 1 {
			return fmt.Errorf("queued scenario occurrence %q changed during terminalization", occurrenceID)
		}
	}
	return nil
}
