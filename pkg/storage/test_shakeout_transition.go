package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"
)

const testShakeoutTransitionIdentitySelect = `SELECT COALESCE(ref_key, ''), COALESCE(project_id, ''), start_time,
		UPPER(TRIM(COALESCE(status, ''))), pending_since, COALESCE(error, ''),
		COALESCE(start_authorization_json, 'null') FROM tests WHERE id = ?`

func validShakeoutDestinationStatus(status string) bool {
	switch status {
	case "QUEUED", "PENDING", "SHAKEOUT_REVIEW":
		return true
	default:
		return false
	}
}

func shakeoutTransitionTimeEqual(left sql.NullTime, right *time.Time, dialect storeDialect) bool {
	if !left.Valid || right == nil || right.IsZero() {
		return !left.Valid && (right == nil || right.IsZero())
	}
	lhs := left.Time.UTC()
	rhs := right.UTC()
	if dialect == storeDialectPostgres {
		lhs = lhs.Truncate(time.Microsecond)
		rhs = rhs.Truncate(time.Microsecond)
	}
	return lhs.Equal(rhs)
}

func shakeoutTransitionAuthorizationEqual(left, right string) bool {
	var leftValue, rightValue any
	if json.Unmarshal([]byte(left), &leftValue) != nil || json.Unmarshal([]byte(right), &rightValue) != nil {
		return false
	}
	return reflect.DeepEqual(leftValue, rightValue)
}

// TransitionTestFromShakeout atomically publishes a completed shakeout result
// only while the exact durable test incarnation remains in SHAKEOUT. The row
// lock/conditional update makes Abort, delete/recreate, and another outcome
// transition single-winner operations.
func (s *SQLiteStore) TransitionTestFromShakeout(transition TestShakeoutTransition) (TestStatusTransitionResult, error) {
	transition.ID = strings.TrimSpace(transition.ID)
	transition.RefKey = strings.TrimSpace(transition.RefKey)
	transition.ProjectID = normalizeProjectID(transition.ProjectID)
	transition.Status = strings.ToUpper(strings.TrimSpace(transition.Status))
	if transition.ID == "" || transition.RefKey == "" || transition.StartTime.IsZero() {
		return TestStatusTransitionFenced, errors.New("shakeout transition requires test identity")
	}
	if !validShakeoutDestinationStatus(transition.Status) {
		return TestStatusTransitionFenced, fmt.Errorf("unsupported shakeout destination status %q", transition.Status)
	}
	if transition.Status == "PENDING" && (transition.PendingSince == nil || transition.PendingSince.IsZero()) {
		return TestStatusTransitionFenced, errors.New("pending shakeout transition requires pending_since")
	}
	if transition.Status != "PENDING" {
		transition.PendingSince = nil
	}
	authorizationJSON, err := marshalTestField("start_authorization_json", transition.StartAuthorization)
	if err != nil {
		return TestStatusTransitionFenced, err
	}

	leaseRequested := strings.TrimSpace(transition.ExecutionLeaseName) != "" ||
		strings.TrimSpace(transition.ExecutionLeaseOwnerID) != "" || transition.ExecutionLeaseGeneration != 0
	var tx *storeTx
	if leaseRequested {
		tx, err = s.beginExecutionLeaseTermTransaction(
			transition.ExecutionLeaseName,
			transition.ExecutionLeaseOwnerID,
			transition.ExecutionLeaseGeneration,
		)
		if err != nil {
			return TestStatusTransitionFenced, err
		}
		if tx == nil {
			return TestStatusTransitionFenced, nil
		}
	} else {
		tx, err = s.db.Begin()
		if err != nil {
			return TestStatusTransitionFenced, err
		}
	}
	defer tx.Rollback()

	identityQuery := testShakeoutTransitionIdentitySelect
	if s.db.dialect == storeDialectPostgres {
		identityQuery += ` FOR UPDATE`
	} else {
		// Own SQLite's single writer slot before inspecting the lifecycle row so
		// a concurrent abort cannot commit between the identity read and CAS.
		if _, err := tx.Exec(`UPDATE tests SET status = status WHERE id = ?`, transition.ID); err != nil {
			return TestStatusTransitionFenced, err
		}
	}
	var refKey, projectID, status, durableError, durableAuthorization string
	var startTime time.Time
	var durablePendingSince sql.NullTime
	if err := tx.QueryRow(identityQuery, transition.ID).Scan(
		&refKey, &projectID, &startTime, &status, &durablePendingSince, &durableError, &durableAuthorization,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TestStatusTransitionMissing, nil
		}
		return TestStatusTransitionFenced, err
	}
	identityTime := startTime.UTC()
	requestedTime := transition.StartTime.UTC()
	if s.db.dialect == storeDialectPostgres {
		identityTime = identityTime.Truncate(time.Microsecond)
		requestedTime = requestedTime.Truncate(time.Microsecond)
	}
	if strings.TrimSpace(refKey) != transition.RefKey || normalizeProjectID(projectID) != transition.ProjectID || !identityTime.Equal(requestedTime) {
		return TestStatusTransitionFenced, nil
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if status != "SHAKEOUT" {
		if status == transition.Status &&
			shakeoutTransitionTimeEqual(durablePendingSince, transition.PendingSince, s.db.dialect) &&
			strings.TrimSpace(durableError) == strings.TrimSpace(transition.Error) &&
			shakeoutTransitionAuthorizationEqual(durableAuthorization, authorizationJSON) {
			return TestStatusTransitionUnchanged, nil
		}
		return TestStatusTransitionFenced, nil
	}

	result, err := tx.Exec(`UPDATE tests SET status = ?, pending_since = ?, error = ?, start_authorization_json = ?
		WHERE id = ? AND ref_key = ? AND project_id = ? AND UPPER(TRIM(COALESCE(status, ''))) = 'SHAKEOUT'`,
		transition.Status, transition.PendingSince, strings.TrimSpace(transition.Error), authorizationJSON,
		transition.ID, transition.RefKey, transition.ProjectID)
	if err != nil {
		return TestStatusTransitionFenced, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return TestStatusTransitionFenced, err
	}
	if affected != 1 {
		return TestStatusTransitionFenced, nil
	}
	if err := tx.Commit(); err != nil {
		return TestStatusTransitionFenced, err
	}
	return TestStatusTransitionUpdated, nil
}
