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

func getArchiveFinalizationJobTx(tx *storeTx, testID string) (*ArchiveFinalizationJob, error) {
	var (
		job              ArchiveFinalizationJob
		recordJSON       string
		persistArtifacts int
	)
	err := tx.QueryRow(`SELECT test_id, project_id, archive_record_json,
		persist_artifacts, generation, COALESCE(intent_token, ''), COALESCE(refresh_token, ''), attempts, COALESCE(last_error, ''), created_at, updated_at
		FROM archive_finalization_jobs WHERE test_id = ?`, strings.TrimSpace(testID)).Scan(
		&job.TestID, &job.ProjectID, &recordJSON, &persistArtifacts, &job.Generation, &job.IntentToken, &job.RefreshToken, &job.Attempts,
		&job.LastError, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(recordJSON), &job.ArchiveRecord); err != nil {
		return nil, fmt.Errorf("decode archive finalization record for %s: %w", job.TestID, err)
	}
	job.PersistArtifacts = persistArtifacts != 0
	return &job, nil
}

func listProjectArchiveFinalizationJobsTx(tx *storeTx, projectID string) ([]*ArchiveFinalizationJob, error) {
	rows, err := tx.Query(`SELECT test_id, project_id, archive_record_json,
		persist_artifacts, generation, COALESCE(intent_token, ''), COALESCE(refresh_token, ''), attempts, COALESCE(last_error, ''), created_at, updated_at
		FROM archive_finalization_jobs WHERE project_id = ? ORDER BY test_id`, normalizeProjectID(projectID))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]*ArchiveFinalizationJob, 0)
	for rows.Next() {
		var (
			job              ArchiveFinalizationJob
			recordJSON       string
			persistArtifacts int
		)
		if err := rows.Scan(
			&job.TestID, &job.ProjectID, &recordJSON, &persistArtifacts, &job.Generation, &job.IntentToken, &job.RefreshToken, &job.Attempts,
			&job.LastError, &job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(recordJSON), &job.ArchiveRecord); err != nil {
			return nil, fmt.Errorf("decode archive finalization record for %s: %w", job.TestID, err)
		}
		job.PersistArtifacts = persistArtifacts != 0
		jobs = append(jobs, &job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) SaveArchiveFinalizationJob(job *ArchiveFinalizationJob) error {
	if job == nil {
		return errors.New("archive finalization job is nil")
	}
	job.TestID = strings.TrimSpace(job.TestID)
	if job.TestID == "" {
		return errors.New("archive finalization test id is required")
	}
	if strings.TrimSpace(job.ArchiveRecord.TestID) == "" {
		job.ArchiveRecord.TestID = job.TestID
	}
	if job.ArchiveRecord.TestID != job.TestID {
		return fmt.Errorf("archive finalization record test id %q does not match job %q", job.ArchiveRecord.TestID, job.TestID)
	}
	job.ProjectID = normalizeProjectID(job.ProjectID)
	job.IntentToken = uuid.NewString()
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	recordJSON, err := json.Marshal(job.ArchiveRecord)
	if err != nil {
		return fmt.Errorf("encode archive finalization record: %w", err)
	}
	persistArtifacts := 0
	if job.PersistArtifacts {
		persistArtifacts = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	projectQuery := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		projectQuery += ` FOR UPDATE`
	}
	var durableProjectID string
	if err := tx.QueryRow(projectQuery, job.TestID).Scan(&durableProjectID); err != nil {
		return err
	}
	if normalizeProjectID(durableProjectID) != job.ProjectID {
		return fmt.Errorf("archive finalization project %q does not match durable test project %q", job.ProjectID, durableProjectID)
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, job.TestID).Scan(&cleanupExists); err != nil {
		return err
	}
	if cleanupExists != 0 {
		return ErrArchiveCleanupPending
	}
	err = tx.QueryRow(`INSERT INTO archive_finalization_jobs (
		test_id, project_id, archive_record_json, persist_artifacts, generation, intent_token, refresh_token, attempts, last_error, created_at, updated_at
	) VALUES (?, ?, ?, ?, 1, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(test_id) DO UPDATE SET
		project_id=excluded.project_id,
		archive_record_json=excluded.archive_record_json,
		persist_artifacts=excluded.persist_artifacts,
		generation=archive_finalization_jobs.generation + 1,
		intent_token=excluded.intent_token,
		refresh_token=excluded.refresh_token,
		updated_at=excluded.updated_at
	RETURNING generation, intent_token, created_at, updated_at`,
		job.TestID, job.ProjectID, string(recordJSON), persistArtifacts, job.IntentToken, strings.TrimSpace(job.RefreshToken), job.Attempts,
		strings.TrimSpace(job.LastError), job.CreatedAt, job.UpdatedAt,
	).Scan(&job.Generation, &job.IntentToken, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

// SaveArchiveFinalizationJobIfOrphan is the create-only startup-recovery path.
// The durable test row is locked on PostgreSQL, and catalog/job absence is
// checked in the same transaction, so an orphan scan cannot overwrite a newer
// note refresh or resurrect publication after test deletion.
func (s *SQLiteStore) SaveArchiveFinalizationJobIfOrphan(job *ArchiveFinalizationJob) (bool, error) {
	if job == nil {
		return false, errors.New("archive finalization job is nil")
	}
	job.TestID = strings.TrimSpace(job.TestID)
	if job.TestID == "" {
		return false, errors.New("archive finalization test id is required")
	}
	if strings.TrimSpace(job.ArchiveRecord.TestID) == "" {
		job.ArchiveRecord.TestID = job.TestID
	}
	if job.ArchiveRecord.TestID != job.TestID {
		return false, fmt.Errorf("archive finalization record test id %q does not match job %q", job.ArchiveRecord.TestID, job.TestID)
	}
	job.ProjectID = normalizeProjectID(job.ProjectID)
	job.IntentToken = uuid.NewString()
	recordJSON, err := json.Marshal(job.ArchiveRecord)
	if err != nil {
		return false, fmt.Errorf("encode archive finalization record: %w", err)
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	persistArtifacts := 0
	if job.PersistArtifacts {
		persistArtifacts = 1
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	projectQuery := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		projectQuery += ` FOR UPDATE`
	}
	var projectID string
	if err := tx.QueryRow(projectQuery, job.TestID).Scan(&projectID); err != nil {
		return false, err
	}
	if normalizeProjectID(projectID) != job.ProjectID {
		return false, fmt.Errorf("archive finalization project %q does not match durable test project %q", job.ProjectID, projectID)
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, job.TestID).Scan(&cleanupExists); err != nil {
		return false, err
	}
	if cleanupExists != 0 {
		return false, nil
	}
	var catalogExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM run_archives WHERE test_id = ?`, job.TestID).Scan(&catalogExists); err != nil {
		return false, err
	}
	if catalogExists != 0 {
		return false, nil
	}
	result, err := tx.Exec(`INSERT INTO archive_finalization_jobs (
		test_id, project_id, archive_record_json, persist_artifacts, generation, intent_token, refresh_token, attempts, last_error, created_at, updated_at
	) VALUES (?, ?, ?, ?, 1, ?, ?, 0, NULL, ?, ?)
	ON CONFLICT(test_id) DO NOTHING`, job.TestID, job.ProjectID, string(recordJSON), persistArtifacts, job.IntentToken, strings.TrimSpace(job.RefreshToken), job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	if affected > 0 {
		job.Generation = 1
	}
	return affected > 0, nil
}

func (s *SQLiteStore) GetArchiveFinalizationJob(testID string) (*ArchiveFinalizationJob, error) {
	var (
		job              ArchiveFinalizationJob
		recordJSON       string
		persistArtifacts int
	)
	err := s.db.QueryRow(`SELECT test_id, project_id, archive_record_json,
		persist_artifacts, generation, COALESCE(intent_token, ''), COALESCE(refresh_token, ''), attempts, COALESCE(last_error, ''), created_at, updated_at
		FROM archive_finalization_jobs WHERE test_id = ?`, strings.TrimSpace(testID)).Scan(
		&job.TestID, &job.ProjectID, &recordJSON, &persistArtifacts, &job.Generation, &job.IntentToken, &job.RefreshToken, &job.Attempts,
		&job.LastError, &job.CreatedAt, &job.UpdatedAt,
	)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(recordJSON), &job.ArchiveRecord); err != nil {
		return nil, fmt.Errorf("decode archive finalization record for %s: %w", job.TestID, err)
	}
	job.PersistArtifacts = persistArtifacts != 0
	return &job, nil
}

func (s *SQLiteStore) ListArchiveFinalizationJobs(limit int) ([]*ArchiveFinalizationJob, error) {
	if limit <= 0 || limit > 10000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT test_id, project_id, archive_record_json,
		persist_artifacts, generation, COALESCE(intent_token, ''), COALESCE(refresh_token, ''), attempts, COALESCE(last_error, ''), created_at, updated_at
		FROM archive_finalization_jobs ORDER BY updated_at ASC, test_id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]*ArchiveFinalizationJob, 0)
	for rows.Next() {
		var (
			job              ArchiveFinalizationJob
			recordJSON       string
			persistArtifacts int
		)
		if err := rows.Scan(
			&job.TestID, &job.ProjectID, &recordJSON, &persistArtifacts, &job.Generation, &job.IntentToken, &job.RefreshToken, &job.Attempts,
			&job.LastError, &job.CreatedAt, &job.UpdatedAt,
		); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(recordJSON), &job.ArchiveRecord); err != nil {
			return nil, fmt.Errorf("decode archive finalization record for %s: %w", job.TestID, err)
		}
		job.PersistArtifacts = persistArtifacts != 0
		jobs = append(jobs, &job)
	}
	return jobs, rows.Err()
}

// MarkArchiveFinalizationJobFailed preserves the legacy storage API while
// resolving the current token first; the conditional update still cannot
// annotate a replacement intent.
func (s *SQLiteStore) MarkArchiveFinalizationJobFailed(testID string, finalizationErr error, updatedAt time.Time) error {
	job, err := s.GetArchiveFinalizationJob(testID)
	if err != nil {
		return err
	}
	return s.MarkArchiveFinalizationJobFailedIfToken(testID, job.IntentToken, finalizationErr, updatedAt)
}

func (s *SQLiteStore) MarkArchiveFinalizationJobFailedIfToken(testID string, intentToken string, finalizationErr error, updatedAt time.Time) error {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return errors.New("archive finalization test id is required")
	}
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	lastError := ""
	if finalizationErr != nil {
		lastError = finalizationErr.Error()
		if len(lastError) > 4096 {
			lastError = lastError[:4096]
		}
	}
	result, err := s.db.Exec(`UPDATE archive_finalization_jobs
		SET attempts = attempts + 1, last_error = ?, updated_at = ? WHERE test_id = ? AND COALESCE(intent_token, '') = ?`,
		lastError, updatedAt.UTC(), testID, strings.TrimSpace(intentToken))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) DeleteArchiveFinalizationJob(testID string) error {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return errors.New("archive finalization test id is required")
	}
	_, err := s.db.Exec(`DELETE FROM archive_finalization_jobs WHERE test_id = ?`, testID)
	return err
}

func (s *SQLiteStore) DeleteArchiveFinalizationJobIfToken(testID string, intentToken string) (bool, error) {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return false, errors.New("archive finalization test id is required")
	}
	intentToken = strings.TrimSpace(intentToken)
	if intentToken == "" {
		return false, errors.New("archive finalization intent token is required")
	}
	result, err := s.db.Exec(`DELETE FROM archive_finalization_jobs WHERE test_id = ? AND intent_token = ?`, testID, intentToken)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

// UpdateArchiveFinalizationRecordIfToken persists the exact publication
// pointer and refreshed manifest trust anchor before object I/O starts. The
// opaque token makes this a compare-and-swap: a stale leader cannot redirect a
// replacement intent or restore an intent canceled by retention/deletion.
func (s *SQLiteStore) UpdateArchiveFinalizationRecordIfToken(testID string, intentToken string, record *RunArchiveRecord) (bool, error) {
	testID = strings.TrimSpace(testID)
	intentToken = strings.TrimSpace(intentToken)
	if testID == "" || intentToken == "" || record == nil {
		return false, errors.New("valid archive finalization record update is required")
	}
	if strings.TrimSpace(record.TestID) != testID {
		return false, fmt.Errorf("archive record test id %q does not match finalization %q", record.TestID, testID)
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return false, fmt.Errorf("encode archive finalization record: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	testQuery := `SELECT 1 FROM tests WHERE id = ?`
	jobQuery := `SELECT COALESCE(intent_token, '') FROM archive_finalization_jobs WHERE test_id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
		jobQuery += ` FOR UPDATE`
	}
	var exists int
	if err := tx.QueryRow(testQuery, testID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var currentToken string
	if err := tx.QueryRow(jobQuery, testID).Scan(&currentToken); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if currentToken != intentToken {
		return false, nil
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, testID).Scan(&cleanupExists); err != nil {
		return false, err
	}
	if cleanupExists != 0 {
		return false, nil
	}
	result, err := tx.Exec(`UPDATE archive_finalization_jobs
		SET archive_record_json = ?, updated_at = ?
		WHERE test_id = ? AND intent_token = ?`, string(encoded), time.Now().UTC(), testID, intentToken)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

// CompleteArchiveFinalization atomically publishes the catalog pointer and
// acknowledges exactly the generation that produced it. A newer note refresh,
// retention cancellation, or test deletion makes the operation a no-op instead
// of allowing a stale worker to resurrect/overwrite archive metadata.
func (s *SQLiteStore) CompleteArchiveFinalization(testID string, intentToken string, refreshToken string, record *RunArchiveRecord) (bool, error) {
	testID = strings.TrimSpace(testID)
	intentToken = strings.TrimSpace(intentToken)
	if testID == "" || intentToken == "" || record == nil {
		return false, errors.New("valid archive finalization completion is required")
	}
	if strings.TrimSpace(record.TestID) != testID {
		return false, fmt.Errorf("archive record test id %q does not match completion %q", record.TestID, testID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	testQuery := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	jobQuery := `SELECT COALESCE(intent_token, '') FROM archive_finalization_jobs WHERE test_id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
		jobQuery += ` FOR UPDATE`
	}
	var durableProjectID string
	if err := tx.QueryRow(testQuery, testID).Scan(&durableProjectID); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var currentToken string
	if err := tx.QueryRow(jobQuery, testID).Scan(&currentToken); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if currentToken != intentToken {
		return false, nil
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, testID).Scan(&cleanupExists); err != nil {
		return false, err
	}
	if cleanupExists != 0 {
		return false, nil
	}
	var superseded *RunArchiveRecord
	if current, err := getRunArchiveTx(tx, testID); err == nil {
		if archivePublicationPointer(current) != "" && archivePublicationPointer(current) != archivePublicationPointer(record) {
			superseded = current
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
	if superseded != nil {
		if err := enqueueArchiveVersionCleanupTx(tx, durableProjectID, superseded); err != nil {
			return false, err
		}
	}
	if _, err := tx.Exec(`INSERT INTO run_archives (test_id, ref_key, schema_version, archive_backend, archive_path, manifest_path, manifest_sha256, archive_uri, manifest_uri, transaction_count, resource_sample_count, interval_summary_count, trend_point_json, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(test_id) DO UPDATE SET
			ref_key=excluded.ref_key,
			schema_version=excluded.schema_version,
			archive_backend=excluded.archive_backend,
			archive_path=excluded.archive_path,
			manifest_path=excluded.manifest_path,
			manifest_sha256=excluded.manifest_sha256,
			archive_uri=excluded.archive_uri,
			manifest_uri=excluded.manifest_uri,
			transaction_count=excluded.transaction_count,
			resource_sample_count=excluded.resource_sample_count,
			interval_summary_count=excluded.interval_summary_count,
			trend_point_json=excluded.trend_point_json,
			created_at=excluded.created_at,
			completed_at=excluded.completed_at`, record.TestID, record.RefKey, record.SchemaVersion,
		record.ArchiveBackend, record.ArchivePath, record.ManifestPath, record.ManifestSHA256,
		record.ArchiveURI, record.ManifestURI, record.TransactionCount, record.ResourceSampleCount,
		record.IntervalSummaryCount, record.TrendPointJSON, record.CreatedAt, record.CompletedAt); err != nil {
		return false, err
	}
	result, err := tx.Exec(`DELETE FROM archive_finalization_jobs WHERE test_id = ? AND intent_token = ?`, testID, intentToken)
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
	if refreshToken = strings.TrimSpace(refreshToken); refreshToken != "" {
		if _, err := tx.Exec(`DELETE FROM archive_refresh_jobs WHERE test_id = ? AND intent_token = ?`, testID, refreshToken); err != nil {
			return false, err
		}
	}
	if err := enqueueConfluencePublicationTx(tx, testID, durableProjectID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func archivePublicationPointer(record *RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	if uri := strings.TrimSpace(record.ArchiveURI); uri != "" {
		return uri
	}
	if uri := strings.TrimSpace(record.ManifestURI); uri != "" {
		return uri
	}
	return strings.TrimSpace(record.ArchivePath)
}
