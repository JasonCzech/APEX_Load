package storage

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// RunArchiveFailureUnconfirmed is committed with terminality before collection
// or publication. It makes a crash in that interval visible after restart.
const RunArchiveFailureUnconfirmed = "archive_collection_unconfirmed"

const RunArchiveFailureLocalFinalization = "local_archive_failed"

const RunArchiveFailureRemoteCollection = "remote_collection_failed"
const RunArchiveFailureRemoteUnavailable = "remote_collection_unavailable"

// RunArchiveFailure describes unavailable or unconfirmed collection, not a retry job.
// Identity fields stay internal; only the fixed reason and recording time are public.
type RunArchiveFailure struct {
	TestID     string    `json:"-"`
	ProjectID  string    `json:"-"`
	StartTime  time.Time `json:"-"`
	EndTime    time.Time `json:"-"`
	Status     string    `json:"-"`
	Reason     string    `json:"reason"`
	RecordedAt time.Time `json:"recorded_at"`
}

// SaveRunArchiveFailure records only the exact durable terminal incarnation.
// A catalog record or publication job takes precedence over a late failure.
// An optional execution lease fences HA producers in the same transaction.
func (s *SQLiteStore) SaveRunArchiveFailure(failure *RunArchiveFailure, leaseName, leaseOwner string) (bool, error) {
	if failure == nil || strings.TrimSpace(failure.TestID) == "" || failure.StartTime.IsZero() || failure.EndTime.IsZero() ||
		(failure.Status != "FAILED" && failure.Status != "ABORTED" && failure.Status != "COMPLETE") ||
		(failure.Reason != RunArchiveFailureRemoteCollection && failure.Reason != RunArchiveFailureRemoteUnavailable && failure.Reason != RunArchiveFailureLocalFinalization) {
		return false, errors.New("invalid archive collection failure")
	}
	if (leaseName == "") != (leaseOwner == "") {
		return false, errors.New("archive failure lease name and owner must be supplied together")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if leaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, leaseName, leaseOwner)
		if err != nil || !held {
			return false, err
		}
	}
	query := `SELECT COALESCE(project_id, ''), start_time, end_time, status FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var project, status string
	var start time.Time
	var end sql.NullTime
	if err := tx.QueryRow(query, failure.TestID).Scan(&project, &start, &end, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	canonical := func(at time.Time) time.Time {
		if s.db.dialect == storeDialectPostgres {
			return at.Truncate(time.Microsecond)
		}
		return at
	}
	if normalizeProjectID(project) != normalizeProjectID(failure.ProjectID) || status != failure.Status || !end.Valid ||
		!start.Equal(canonical(failure.StartTime)) || !end.Time.Equal(canonical(failure.EndTime)) {
		return false, nil
	}
	var exists int
	if err := tx.QueryRow(`SELECT CASE WHEN EXISTS (SELECT 1 FROM run_archives WHERE test_id = ?)
		OR EXISTS (SELECT 1 FROM archive_finalization_jobs WHERE test_id = ?) THEN 1 ELSE 0 END`, failure.TestID, failure.TestID).Scan(&exists); err != nil {
		return false, err
	}
	if exists != 0 {
		return false, nil
	}
	_, err = tx.Exec(`INSERT INTO run_archive_failures (test_id, project_id, test_start_time, test_end_time, test_status, reason, recorded_at)
		SELECT id, COALESCE(NULLIF(project_id, ''), 'unassigned'), start_time, end_time, status, ?, ? FROM tests WHERE id = ?
        ON CONFLICT(test_id) DO UPDATE SET project_id = excluded.project_id, test_start_time = excluded.test_start_time,
        test_end_time = excluded.test_end_time, test_status = excluded.test_status, reason = excluded.reason, recorded_at = excluded.recorded_at
        WHERE run_archive_failures.project_id <> excluded.project_id OR run_archive_failures.test_start_time <> excluded.test_start_time
        OR run_archive_failures.test_end_time <> excluded.test_end_time OR run_archive_failures.test_status <> excluded.test_status
        OR run_archive_failures.reason = ?`, failure.Reason, time.Now().UTC(), failure.TestID, RunArchiveFailureUnconfirmed)
	if err != nil {
		return false, err
	}
	// The run is terminal and never adopted again. Without an archive commit to
	// drop its recovery checkpoint, drop it here; the revision is retained.
	if _, err := tx.Exec(`UPDATE tests SET telemetry_checkpoint_json = NULL WHERE id = ? AND telemetry_checkpoint_json IS NOT NULL`, failure.TestID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// GetRunArchiveFailure excludes deleted/reused runs and superseded failures in a
// single read. A publication intent means the archive has a source and can retry.
func (s *SQLiteStore) GetRunArchiveFailure(testID string) (*RunArchiveFailure, error) {
	var failure RunArchiveFailure
	err := s.db.QueryRow(`SELECT f.test_id, f.project_id, f.test_start_time, f.test_end_time, f.test_status, f.reason, f.recorded_at
		FROM run_archive_failures f JOIN tests t ON t.id = f.test_id
		WHERE f.test_id = ? AND f.project_id = COALESCE(NULLIF(t.project_id, ''), 'unassigned')
		AND f.test_start_time = t.start_time AND f.test_end_time = t.end_time AND f.test_status = t.status
		AND (f.reason <> ? OR t.started_at IS NOT NULL)
		AND NOT EXISTS (SELECT 1 FROM run_archives a WHERE a.test_id = f.test_id)
		AND NOT EXISTS (SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = f.test_id)`, testID, RunArchiveFailureUnconfirmed).Scan(
		&failure.TestID, &failure.ProjectID, &failure.StartTime, &failure.EndTime, &failure.Status, &failure.Reason, &failure.RecordedAt)
	if err != nil {
		return nil, err
	}
	return &failure, nil
}

// recordUnconfirmedTerminalArchive shares the lifecycle transaction: a terminal
// run row cannot commit without either existing publication evidence or a durable
// explanation that collection has not been confirmed. A pre-run terminal row
// has no execution archive to collect and does not receive this marker. A later
// observed failure replaces this placeholder; catalog/publication intent always
// takes precedence.
// Identity comes from the locked/updated database row, including its native time
// precision. This is deliberately not a collection retry or proof of data loss.
// The same transaction drops the run's telemetry recovery checkpoint: a terminal
// run is never adopted again, whether or not its archive ever commits.
func recordUnconfirmedTerminalArchive(tx *storeTx, id, status string) error {
	if status != "COMPLETE" && status != "FAILED" && status != "ABORTED" {
		return nil
	}
	if _, err := tx.Exec(`UPDATE tests SET telemetry_checkpoint_json = NULL WHERE id = ? AND status = ? AND telemetry_checkpoint_json IS NOT NULL`, id, status); err != nil {
		return err
	}
	_, err := tx.Exec(`INSERT INTO run_archive_failures (test_id, project_id, test_start_time, test_end_time, test_status, reason, recorded_at)
 SELECT id, COALESCE(NULLIF(project_id, ''), 'unassigned'), start_time, end_time, status, ?, ? FROM tests
 WHERE id = ? AND status = ? AND end_time IS NOT NULL AND started_at IS NOT NULL
 AND NOT EXISTS (SELECT 1 FROM run_archives a WHERE a.test_id = tests.id)
 AND NOT EXISTS (SELECT 1 FROM archive_finalization_jobs j WHERE j.test_id = tests.id)
 ON CONFLICT(test_id) DO UPDATE SET project_id = excluded.project_id, test_start_time = excluded.test_start_time,
 test_end_time = excluded.test_end_time, test_status = excluded.test_status, reason = excluded.reason, recorded_at = excluded.recorded_at
 WHERE run_archive_failures.project_id <> excluded.project_id OR run_archive_failures.test_start_time <> excluded.test_start_time
 OR run_archive_failures.test_end_time <> excluded.test_end_time OR run_archive_failures.test_status <> excluded.test_status`,
		RunArchiveFailureUnconfirmed, time.Now().UTC(), id, status)
	return err
}
