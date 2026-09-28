package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

func bumpArchiveRefreshJobTx(tx *storeTx, dialect storeDialect, testID string) (*ArchiveRefreshJob, error) {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return nil, errors.New("archive refresh test id is required")
	}
	projectQuery := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	if dialect == storeDialectPostgres {
		projectQuery += ` FOR UPDATE`
	}
	var projectID string
	if err := tx.QueryRow(projectQuery, testID).Scan(&projectID); err != nil {
		return nil, err
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, testID).Scan(&cleanupExists); err != nil {
		return nil, err
	}
	if cleanupExists != 0 {
		return nil, ErrArchiveCleanupPending
	}
	now := time.Now().UTC()
	job := &ArchiveRefreshJob{
		TestID:      testID,
		ProjectID:   normalizeProjectID(projectID),
		IntentToken: uuid.NewString(),
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	err := tx.QueryRow(`INSERT INTO archive_refresh_jobs (
		test_id, project_id, generation, intent_token, created_at, updated_at
	) VALUES (?, ?, 1, ?, ?, ?)
	ON CONFLICT(test_id) DO UPDATE SET
		project_id=excluded.project_id,
		generation=archive_refresh_jobs.generation + 1,
		intent_token=excluded.intent_token,
		updated_at=excluded.updated_at
	RETURNING generation, created_at, updated_at`, job.TestID, job.ProjectID,
		job.IntentToken, job.CreatedAt, job.UpdatedAt).Scan(&job.Generation, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	return job, nil
}

func (s *SQLiteStore) BumpArchiveRefreshJob(testID string) (*ArchiveRefreshJob, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	job, err := bumpArchiveRefreshJobTx(tx, s.db.dialect, testID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return job, nil
}

func scanArchiveRefreshJob(row rowScanner) (*ArchiveRefreshJob, error) {
	var job ArchiveRefreshJob
	if err := row.Scan(&job.TestID, &job.ProjectID, &job.Generation, &job.IntentToken, &job.CreatedAt, &job.UpdatedAt); err != nil {
		return nil, err
	}
	return &job, nil
}

func (s *SQLiteStore) GetArchiveRefreshJob(testID string) (*ArchiveRefreshJob, error) {
	return scanArchiveRefreshJob(s.db.QueryRow(`SELECT test_id, project_id, generation,
		intent_token, created_at, updated_at FROM archive_refresh_jobs WHERE test_id = ?`, strings.TrimSpace(testID)))
}

func (s *SQLiteStore) ListArchiveRefreshJobs(limit int) ([]*ArchiveRefreshJob, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT test_id, project_id, generation, intent_token,
		created_at, updated_at FROM archive_refresh_jobs
		ORDER BY updated_at ASC, test_id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]*ArchiveRefreshJob, 0)
	for rows.Next() {
		job, err := scanArchiveRefreshJob(rows)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) DeleteArchiveRefreshJob(testID string) error {
	_, err := s.db.Exec(`DELETE FROM archive_refresh_jobs WHERE test_id = ?`, strings.TrimSpace(testID))
	return err
}

func (s *SQLiteStore) DeleteArchiveRefreshJobIfToken(testID string, intentToken string) (bool, error) {
	testID = strings.TrimSpace(testID)
	intentToken = strings.TrimSpace(intentToken)
	if testID == "" || intentToken == "" {
		return false, errors.New("archive refresh test id and intent token are required")
	}
	result, err := s.db.Exec(`DELETE FROM archive_refresh_jobs WHERE test_id = ? AND intent_token = ?`, testID, intentToken)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// RebindArchiveRefreshToFinalization atomically snapshots the current catalog
// into a publication intent without acknowledging the dirty row. The finalizer
// clears only the observed refresh token after the new manifest is published.
func (s *SQLiteStore) RebindArchiveRefreshToFinalization(testID string, refreshToken string) (*ArchiveFinalizationJob, bool, error) {
	testID = strings.TrimSpace(testID)
	refreshToken = strings.TrimSpace(refreshToken)
	if testID == "" || refreshToken == "" {
		return nil, false, errors.New("valid archive refresh binding is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback()
	testQuery := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
	}
	var projectID string
	if err := tx.QueryRow(testQuery, testID).Scan(&projectID); errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	var currentRefreshToken string
	if err := tx.QueryRow(`SELECT intent_token FROM archive_refresh_jobs WHERE test_id = ?`, testID).Scan(&currentRefreshToken); errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	} else if err != nil {
		return nil, false, err
	}
	if currentRefreshToken != refreshToken {
		return nil, false, nil
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, testID).Scan(&cleanupExists); err != nil {
		return nil, false, err
	}
	if cleanupExists != 0 {
		return nil, false, nil
	}
	// Resolve the publication source only after taking the durable test-row lock.
	// A caller snapshot could otherwise race a concurrent catalog completion.
	var preparedManifest string
	record, recordErr := getRunArchiveTx(tx, testID)
	if errors.Is(recordErr, sql.ErrNoRows) {
		pending, pendingErr := getArchiveFinalizationJobTx(tx, testID)
		if errors.Is(pendingErr, sql.ErrNoRows) {
			return nil, false, nil
		}
		if pendingErr != nil {
			return nil, false, pendingErr
		}
		preparedManifest = pending.PreparedManifestJSON
		copy := pending.ArchiveRecord
		record = &copy
	} else if recordErr != nil {
		return nil, false, recordErr
	}
	encoded, err := jsonMarshalArchiveRecord(record)
	if err != nil {
		return nil, false, err
	}
	now := time.Now().UTC()
	job := &ArchiveFinalizationJob{
		TestID:               testID,
		ProjectID:            normalizeProjectID(projectID),
		ArchiveRecord:        *record,
		PersistArtifacts:     true,
		PreparedManifestJSON: preparedManifest,
		IntentToken:          uuid.NewString(),
		RefreshToken:         refreshToken,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	err = tx.QueryRow(`INSERT INTO archive_finalization_jobs (
		test_id, project_id, archive_record_json, persist_artifacts, generation,
		intent_token, refresh_token, prepared_manifest_json, attempts, last_error, created_at, updated_at
	) VALUES (?, ?, ?, 1, 1, ?, ?, ?, 0, NULL, ?, ?)
	ON CONFLICT(test_id) DO UPDATE SET
		project_id=excluded.project_id,
		archive_record_json=excluded.archive_record_json,
		persist_artifacts=1,
		generation=archive_finalization_jobs.generation + 1,
		intent_token=excluded.intent_token,
		refresh_token=excluded.refresh_token,
		prepared_manifest_json=excluded.prepared_manifest_json,
		updated_at=excluded.updated_at
	RETURNING generation, created_at, updated_at`, job.TestID, job.ProjectID,
		encoded, job.IntentToken, job.RefreshToken, job.PreparedManifestJSON, job.CreatedAt, job.UpdatedAt).Scan(&job.Generation, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return nil, false, err
	}
	if err := tx.Commit(); err != nil {
		return nil, false, err
	}
	return job, true, nil
}

func jsonMarshalArchiveRecord(record *RunArchiveRecord) (string, error) {
	encoded, err := json.Marshal(record)
	if err != nil {
		return "", fmt.Errorf("encode archive record: %w", err)
	}
	return string(encoded), nil
}
