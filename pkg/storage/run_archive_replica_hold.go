package storage

import (
	"database/sql"
	"errors"
	"time"
)

// HoldRunArchiveReplicaFloor retains a StatefulSet ordinal while the exact
// collection intent is unresolved. Higher later source ordinals only raise the
// floor. The hold is scoped to one installation and one workload incarnation.
func (s *SQLiteStore) HoldRunArchiveReplicaFloor(testID, token, fleet, namespace, workloadUID string, floor int, leaseName, owner string) (bool, error) {
	if testID == "" || token == "" || fleet == "" || namespace == "" || workloadUID == "" || floor <= 0 || floor > 100000 || (leaseName == "") != (owner == "") {
		return false, errors.New("invalid archive replica hold")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if leaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, owner)
		if err != nil || !held {
			return false, err
		}
	}
	query := `SELECT c.project_id, c.test_start_time, c.intent_token, c.exhausted, c.resolved
		FROM run_archive_collection_intents c JOIN tests t ON t.id = c.test_id
		WHERE c.test_id = ? AND c.project_id = COALESCE(NULLIF(t.project_id, ''), 'unassigned')
		AND c.test_start_time = t.start_time`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE OF c`
	}
	var project, currentToken string
	var started time.Time
	var exhausted, resolved int
	if err := tx.QueryRow(query, testID).Scan(&project, &started, &currentToken, &exhausted, &resolved); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if currentToken != token || exhausted != 0 || resolved != 0 {
		return false, nil
	}
	result, err := tx.Exec(`INSERT INTO run_archive_replica_holds
		(test_id, project_id, test_start_time, intent_token, fleet_id, namespace, workload_uid, replica_floor)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(test_id) DO UPDATE SET replica_floor = CASE
		WHEN run_archive_replica_holds.replica_floor > excluded.replica_floor THEN run_archive_replica_holds.replica_floor
		ELSE excluded.replica_floor END
		WHERE run_archive_replica_holds.project_id = excluded.project_id
		AND run_archive_replica_holds.test_start_time = excluded.test_start_time
		AND run_archive_replica_holds.intent_token = excluded.intent_token
		AND run_archive_replica_holds.fleet_id = excluded.fleet_id
		AND run_archive_replica_holds.namespace = excluded.namespace
		AND run_archive_replica_holds.workload_uid = excluded.workload_uid`,
		testID, project, started, token, fleet, namespace, workloadUID, floor)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return count == 1, nil
}

// GetRunArchiveReplicaFloor reads only current, unpublished pending collection
// intents. Resolution, exhaustion, deletion and publication drop the hold
// without a separate cleanup write.
func (s *SQLiteStore) GetRunArchiveReplicaFloor(fleet, namespace, workloadUID string) (int, error) {
	if fleet == "" || namespace == "" || workloadUID == "" {
		return 0, errors.New("archive replica floor scope is required")
	}
	var floor int
	err := s.db.QueryRow(`SELECT COALESCE(MAX(h.replica_floor), 0)
		FROM run_archive_replica_holds h
		JOIN run_archive_collection_intents c ON c.test_id = h.test_id
		JOIN tests t ON t.id = h.test_id
		WHERE h.fleet_id = ? AND h.namespace = ? AND h.workload_uid = ?
		AND c.project_id = h.project_id AND c.test_start_time = h.test_start_time
		AND c.intent_token = h.intent_token AND c.resolved = 0 AND c.exhausted = 0
		AND COALESCE(NULLIF(t.project_id, ''), 'unassigned') = h.project_id
		AND t.start_time = h.test_start_time
		AND NOT EXISTS (SELECT 1 FROM run_archives a WHERE a.test_id = h.test_id)
		AND NOT EXISTS (SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = h.test_id)`,
		fleet, namespace, workloadUID).Scan(&floor)
	return floor, err
}

func (s *SQLiteStore) HasPendingRunArchiveReplicaHolds(fleet string) (bool, error) {
	if fleet == "" {
		return false, errors.New("archive replica fleet is required")
	}
	var present int
	err := s.db.QueryRow(`SELECT 1 FROM run_archive_replica_holds h
		JOIN run_archive_collection_intents c ON c.test_id = h.test_id
		JOIN tests t ON t.id = h.test_id
		WHERE h.fleet_id = ? AND c.project_id = h.project_id
		AND c.test_start_time = h.test_start_time AND c.intent_token = h.intent_token
		AND c.resolved = 0 AND c.exhausted = 0
		AND COALESCE(NULLIF(t.project_id, ''), 'unassigned') = h.project_id
		AND t.start_time = h.test_start_time
		AND NOT EXISTS (SELECT 1 FROM run_archives a WHERE a.test_id = h.test_id)
		AND NOT EXISTS (SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = h.test_id)
		LIMIT 1`, fleet).Scan(&present)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return present == 1, err
}
