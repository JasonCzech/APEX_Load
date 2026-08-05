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

func pendingTransitionAuthorizationEquivalent(durableJSON, requestedJSON string) bool {
	var durable, requested any
	if json.Unmarshal([]byte(durableJSON), &durable) != nil || json.Unmarshal([]byte(requestedJSON), &requested) != nil {
		return false
	}
	stripApprovalTime := func(value any) {}
	stripApprovalTime = func(value any) {
		switch typed := value.(type) {
		case map[string]any:
			// approved_at is controller-generated transition metadata. A retry
			// after an ambiguous commit necessarily generates a new timestamp;
			// every operator-authored identity and authorization bound remains
			// part of the exact comparison.
			delete(typed, "approved_at")
			for _, child := range typed {
				stripApprovalTime(child)
			}
		case []any:
			for _, child := range typed {
				stripApprovalTime(child)
			}
		}
	}
	stripApprovalTime(durable)
	stripApprovalTime(requested)
	return reflect.DeepEqual(durable, requested)
}

const testPendingTransitionIdentitySelect = `SELECT COALESCE(ref_key, ''), COALESCE(project_id, ''), start_time, status,
		pending_since, COALESCE(start_authorization_json, 'null'),
		COALESCE(started_by_subject, ''), COALESCE(started_by_display_name, ''), COALESCE(error, '')
		FROM tests WHERE id = ?`

func postgresTestPendingTransitionLockOrderSQL() []string {
	revisionLocks := postgresStartAdmissionRevisionLockOrderSQL()
	return []string{
		testPendingTransitionIdentitySelect + ` FOR UPDATE`,
		revisionLocks[0],
		revisionLocks[1],
	}
}

// TransitionTestToPending performs a targeted, identity-fenced lifecycle
// update. The transaction serializes against admission-bearing content and,
// when supplied, the exact execution-leadership term. It never writes group,
// assignment, reservation-binding, or other mutable test columns.
func (s *SQLiteStore) TransitionTestToPending(transition TestPendingTransition) (TestStatusTransitionResult, error) {
	transition.ID = strings.TrimSpace(transition.ID)
	transition.RefKey = strings.TrimSpace(transition.RefKey)
	transition.ProjectID = normalizeProjectID(transition.ProjectID)
	transition.ExpectedStatus = strings.ToUpper(strings.TrimSpace(transition.ExpectedStatus))
	if transition.ID == "" || transition.RefKey == "" || transition.ExpectedStatus == "" || transition.StartTime.IsZero() {
		return TestStatusTransitionFenced, errors.New("pending transition requires test identity and expected status")
	}
	if transition.PendingSince.IsZero() {
		return TestStatusTransitionFenced, errors.New("pending transition requires pending_since")
	}
	startAuthorizationJSON, err := marshalTestField("start_authorization_json", transition.StartAuthorization)
	if err != nil {
		return "", err
	}

	var tx *storeTx
	leaseRequested := strings.TrimSpace(transition.ExecutionLeaseName) != "" ||
		strings.TrimSpace(transition.ExecutionLeaseOwnerID) != "" || transition.ExecutionLeaseGeneration != 0
	if leaseRequested {
		tx, err = s.beginExecutionLeaseTermTransaction(
			transition.ExecutionLeaseName,
			transition.ExecutionLeaseOwnerID,
			transition.ExecutionLeaseGeneration,
		)
		if err != nil {
			return "", err
		}
		if tx == nil {
			return TestStatusTransitionFenced, nil
		}
	} else {
		tx, err = s.db.Begin()
		if err != nil {
			return "", err
		}
	}
	defer tx.Rollback()

	// SQLite's no-op write owns the single writer slot before reading the
	// revision and test identity. PostgreSQL must instead lock the test row
	// first: reservation/project mutations already hold content rows when their
	// deferred triggers acquire the singleton admission-revision rows. Taking
	// revision before test here would invert that order and permit a deadlock.
	var admissionErr error
	if s.db.dialect == storeDialectSQLite {
		if _, err := tx.Exec(`UPDATE start_admission_revision SET revision = revision WHERE id = 1`); err != nil {
			return "", err
		}
		admissionErr = validateExpectedStartAdmissionRevision(tx, s.db.dialect, transition.ExpectedStartAdmissionRevision)
	}
	if admissionErr != nil && !errors.Is(admissionErr, ErrTestStartAdmissionRevisionChanged) {
		return "", admissionErr
	}

	identityQuery := testPendingTransitionIdentitySelect
	if s.db.dialect == storeDialectPostgres {
		identityQuery = postgresTestPendingTransitionLockOrderSQL()[0]
	}
	var refKey, projectID, status, durableAuthorizationJSON string
	var startedBySubject, startedByDisplayName, durableError string
	var startTime time.Time
	var durablePendingSince sql.NullTime
	if err := tx.QueryRow(identityQuery, transition.ID).Scan(
		&refKey, &projectID, &startTime, &status, &durablePendingSince, &durableAuthorizationJSON,
		&startedBySubject, &startedByDisplayName, &durableError,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return TestStatusTransitionMissing, nil
		}
		return "", err
	}
	startTimeMatches := startTime.UTC().Equal(transition.StartTime.UTC())
	if s.db.dialect == storeDialectPostgres {
		startTimeMatches = startTime.UTC().Truncate(time.Microsecond).Equal(transition.StartTime.UTC().Truncate(time.Microsecond))
	}
	if strings.TrimSpace(refKey) != transition.RefKey || normalizeProjectID(projectID) != transition.ProjectID || !startTimeMatches {
		return TestStatusTransitionFenced, nil
	}
	status = strings.ToUpper(strings.TrimSpace(status))
	if status != transition.ExpectedStatus {
		if status == "PENDING" {
			// pending_since is generated by the controller transition attempt,
			// not operator intent. A committed-write/lost-response retry creates a
			// new local timestamp; accept the durable timestamp when every actual
			// authorization/provenance field is identical and let the caller adopt
			// it from GetTest before publishing local state.
			if durablePendingSince.Valid && pendingTransitionAuthorizationEquivalent(durableAuthorizationJSON, startAuthorizationJSON) &&
				strings.TrimSpace(startedBySubject) == strings.TrimSpace(transition.StartedBySubject) &&
				strings.TrimSpace(startedByDisplayName) == strings.TrimSpace(transition.StartedByDisplayName) &&
				strings.TrimSpace(durableError) == "" {
				return TestStatusTransitionUnchanged, nil
			}
		}
		return TestStatusTransitionFenced, nil
	}
	if s.db.dialect == storeDialectPostgres {
		admissionErr = validateExpectedPostgresStartAdmissionRevision(tx, transition.ExpectedStartAdmissionRevision)
	}
	if admissionErr != nil {
		return "", admissionErr
	}

	result, err := tx.Exec(`UPDATE tests SET status = 'PENDING', pending_since = ?, error = '',
		start_authorization_json = ?, started_by_subject = ?, started_by_display_name = ?
		WHERE id = ? AND ref_key = ? AND project_id = ? AND status = ?`,
		transition.PendingSince.UTC(), startAuthorizationJSON,
		strings.TrimSpace(transition.StartedBySubject), strings.TrimSpace(transition.StartedByDisplayName),
		transition.ID, transition.RefKey, transition.ProjectID, transition.ExpectedStatus)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return TestStatusTransitionFenced, nil
	}
	if err := tx.Commit(); err != nil {
		return "", fmt.Errorf("commit pending test transition: %w", err)
	}
	return TestStatusTransitionUpdated, nil
}
