package storage

import (
	"context"
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
		persist_artifacts, generation, COALESCE(intent_token, ''), COALESCE(refresh_token, ''), attempts, COALESCE(last_error, ''), created_at, updated_at, COALESCE(prepared_manifest_json, '')
		FROM archive_finalization_jobs WHERE test_id = ?`, strings.TrimSpace(testID)).Scan(
		&job.TestID, &job.ProjectID, &recordJSON, &persistArtifacts, &job.Generation, &job.IntentToken, &job.RefreshToken, &job.Attempts,
		&job.LastError, &job.CreatedAt, &job.UpdatedAt, &job.PreparedManifestJSON,
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
		prepared_manifest_json='',
		updated_at=excluded.updated_at
	RETURNING generation, intent_token, created_at, updated_at`,
		job.TestID, job.ProjectID, string(recordJSON), persistArtifacts, job.IntentToken, strings.TrimSpace(job.RefreshToken), job.Attempts,
		strings.TrimSpace(job.LastError), job.CreatedAt, job.UpdatedAt,
	).Scan(&job.Generation, &job.IntentToken, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE run_archive_collection_intents SET resolved = 1 WHERE test_id = ?`, job.TestID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM run_archive_failures WHERE test_id = ?`, job.TestID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	job.PreparedManifestJSON = ""
	return nil
}

// SaveArchiveFinalizationJobIfOrphan is the create-only startup-recovery path.
// The durable test row is locked on PostgreSQL, and catalog/job absence is
// checked in the same transaction, so an orphan scan cannot overwrite a newer
// note refresh or resurrect publication after test deletion.
func (s *SQLiteStore) SaveArchiveFinalizationJobIfOrphan(job *ArchiveFinalizationJob) (bool, error) {
	return s.saveArchiveFinalizationJobIfOrphan(job, time.Time{}, "", "", "")
}

// SaveArchiveFinalizationJobIfOrphanForRun atomically binds recovery to the
// terminal incarnation inspected by the caller and the current execution lease.
func (s *SQLiteStore) SaveArchiveFinalizationJobIfOrphanForRun(job *ArchiveFinalizationJob, requestedAt time.Time, status, lease, owner string) (bool, error) {
	if job == nil || requestedAt.IsZero() || job.ArchiveRecord.CreatedAt.IsZero() || job.ArchiveRecord.CompletedAt.IsZero() || (status != "COMPLETE" && status != "FAILED" && status != "ABORTED") || (lease == "") != (owner == "") {
		return false, errors.New("invalid orphan recovery identity")
	}
	return s.saveArchiveFinalizationJobIfOrphan(job, requestedAt, status, lease, owner)
}

func (s *SQLiteStore) saveArchiveFinalizationJobIfOrphan(job *ArchiveFinalizationJob, requestedAt time.Time, status, lease, owner string) (bool, error) {
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
	// Recovery may prepare a replacement manifest (a failed-over archive at the
	// durable end time). It is committed with the job, never written to the
	// source before this fence, and restored by finalization.
	if job.PreparedManifestJSON != "" {
		if err := validatePreparedArchiveManifest(job.TestID, &job.ArchiveRecord, job.PreparedManifestJSON); err != nil {
			return false, err
		}
	}
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
	if lease != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, lease, owner)
		if err != nil || !held {
			return false, err
		}
	}
	projectQuery := `SELECT COALESCE(project_id, ''), start_time, end_time, status, COALESCE(initial_provenance_json, ''), COALESCE(execution_generation, 0) FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		projectQuery += ` FOR UPDATE`
	}
	var projectID, durableStatus, initialProvenance string
	var durableStart time.Time
	var durableEnd sql.NullTime
	var durableExecutionGeneration uint64
	if err := tx.QueryRow(projectQuery, job.TestID).Scan(&projectID, &durableStart, &durableEnd, &durableStatus, &initialProvenance, &durableExecutionGeneration); err != nil {
		if status != "" && errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if status != "" {
		incarnation, err := testRunIncarnationJSON(initialProvenance)
		if err != nil {
			return false, err
		}
		if !durableEnd.Valid || durableStatus != status ||
			!archiveIdentityTimestampEqual(s.db.dialect, durableStart, requestedAt) ||
			incarnation != job.ArchiveRecord.RunIncarnation ||
			durableExecutionGeneration != job.ArchiveRecord.ExecutionGeneration ||
			!archiveIdentityTimestampEqual(s.db.dialect, durableEnd.Time, job.ArchiveRecord.CompletedAt) {
			return false, nil
		}
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
	// Publication/retention permanently resolves collection. A leftover source
	// must never recreate work after its catalog and cleanup records disappear.
	var resolved int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM run_archive_collection_intents WHERE test_id = ? AND resolved = 1`, job.TestID).Scan(&resolved); err != nil {
		return false, err
	}
	if resolved != 0 {
		return false, nil
	}
	// The run lock can outlive the lease deadline while this transaction waits.
	if lease != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, lease, owner)
		if err != nil || !held {
			return false, err
		}
	}
	result, err := tx.Exec(`INSERT INTO archive_finalization_jobs (
		test_id, project_id, archive_record_json, persist_artifacts, generation, intent_token, refresh_token, attempts, last_error, prepared_manifest_json, created_at, updated_at
	) VALUES (?, ?, ?, ?, 1, ?, ?, 0, NULL, ?, ?, ?)
	ON CONFLICT(test_id) DO NOTHING`, job.TestID, job.ProjectID, string(recordJSON), persistArtifacts, job.IntentToken, strings.TrimSpace(job.RefreshToken), job.PreparedManifestJSON, job.CreatedAt, job.UpdatedAt)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected > 0 {
		if _, err := tx.Exec(`UPDATE run_archive_collection_intents SET resolved = 1 WHERE test_id = ?`, job.TestID); err != nil {
			return false, err
		}
		if _, err := tx.Exec(`DELETE FROM run_archive_failures WHERE test_id = ?`, job.TestID); err != nil {
			return false, err
		}
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
	return s.GetArchiveFinalizationJobContext(context.Background(), testID)
}

// GetArchiveFinalizationJobContext includes database pool waits in the caller deadline.
func (s *SQLiteStore) GetArchiveFinalizationJobContext(ctx context.Context, testID string) (*ArchiveFinalizationJob, error) {
	var (
		job              ArchiveFinalizationJob
		recordJSON       string
		persistArtifacts int
	)
	err := s.db.QueryRowContext(ctx, `SELECT test_id, project_id, archive_record_json,
		persist_artifacts, generation, COALESCE(intent_token, ''), COALESCE(refresh_token, ''), attempts, COALESCE(last_error, ''), created_at, updated_at, COALESCE(prepared_manifest_json, '')
		FROM archive_finalization_jobs WHERE test_id = ?`, strings.TrimSpace(testID)).Scan(
		&job.TestID, &job.ProjectID, &recordJSON, &persistArtifacts, &job.Generation, &job.IntentToken, &job.RefreshToken, &job.Attempts,
		&job.LastError, &job.CreatedAt, &job.UpdatedAt, &job.PreparedManifestJSON,
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
	return s.updateArchiveFinalizationRecordIfToken(testID, intentToken, record, nil)
}

func (s *SQLiteStore) updateArchiveFinalizationRecordIfToken(testID string, intentToken string, record *RunArchiveRecord, prepared *string) (bool, error) {
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
	jobQuery := `SELECT COALESCE(intent_token, ''), archive_record_json,
		CASE WHEN LENGTH(COALESCE(prepared_manifest_json, '')) > 0 THEN 1 ELSE 0 END
		FROM archive_finalization_jobs WHERE test_id = ?`
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
	var currentToken, currentRecordJSON string
	var hasPrepared int
	if err := tx.QueryRow(jobQuery, testID).Scan(&currentToken, &currentRecordJSON, &hasPrepared); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if currentToken != intentToken {
		return false, nil
	}
	if prepared == nil && hasPrepared != 0 {
		var currentRecord RunArchiveRecord
		if err := json.Unmarshal([]byte(currentRecordJSON), &currentRecord); err != nil {
			return false, fmt.Errorf("decode prepared archive record: %w", err)
		}
		// A late record-only write must not detach a committed recovery marker
		// from its digest. Advancing that digest requires another atomic prepare.
		if !strings.EqualFold(strings.TrimSpace(record.ManifestSHA256), strings.TrimSpace(currentRecord.ManifestSHA256)) {
			return false, errors.New("archive manifest changed after durable preparation")
		}
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, testID).Scan(&cleanupExists); err != nil {
		return false, err
	}
	if cleanupExists != 0 {
		return false, nil
	}
	var result sql.Result
	if prepared == nil {
		result, err = tx.Exec(`UPDATE archive_finalization_jobs
			SET archive_record_json = ?, updated_at = ?
			WHERE test_id = ? AND intent_token = ?`, string(encoded), time.Now().UTC(), testID, intentToken)
	} else {
		result, err = tx.Exec(`UPDATE archive_finalization_jobs
			SET archive_record_json = ?, prepared_manifest_json = ?, updated_at = ?
			WHERE test_id = ? AND intent_token = ?`, string(encoded), *prepared, time.Now().UTC(), testID, intentToken)
	}
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
	return s.completeArchiveFinalization(testID, intentToken, refreshToken, record, nil, nil)
}

func (s *SQLiteStore) completeArchiveFinalization(testID string, intentToken string, refreshToken string, record *RunArchiveRecord, identity *ArchiveFinalizationRunIdentity, notification *TelemetryNotificationPublication) (bool, error) {
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
	if identity != nil && identity.LeaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, identity.LeaseName, identity.LeaseOwner)
		if err != nil || !held {
			return false, err
		}
	}
	testQuery := `SELECT COALESCE(project_id, ''), start_time, end_time, status, COALESCE(initial_provenance_json, ''), COALESCE(execution_generation, 0) FROM tests WHERE id = ?`
	jobQuery := `SELECT COALESCE(intent_token, ''), COALESCE(project_id, ''), archive_record_json FROM archive_finalization_jobs WHERE test_id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
		jobQuery += ` FOR UPDATE`
	}
	var durableProjectID, durableStatus, initialProvenance string
	var durableStart time.Time
	var durableEnd sql.NullTime
	var durableExecutionGeneration uint64
	if err := tx.QueryRow(testQuery, testID).Scan(&durableProjectID, &durableStart, &durableEnd, &durableStatus, &initialProvenance, &durableExecutionGeneration); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if identity != nil {
		incarnation, err := testRunIncarnationJSON(initialProvenance)
		if err != nil {
			return false, err
		}
		if incarnation != identity.RunIncarnation || durableExecutionGeneration != identity.ExecutionGeneration {
			return false, nil
		}
	}
	if identity != nil && (normalizeProjectID(durableProjectID) != identity.ProjectID ||
		!durableEnd.Valid || durableStatus != identity.Status ||
		!archiveIdentityTimestampEqual(s.db.dialect, durableStart, identity.StartTime) ||
		!archiveIdentityTimestampEqual(s.db.dialect, durableEnd.Time, identity.EndTime)) {
		return false, nil
	}
	var currentToken, jobProject, currentRecordJSON string
	if err := tx.QueryRow(jobQuery, testID).Scan(&currentToken, &jobProject, &currentRecordJSON); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if currentToken != intentToken || (identity != nil && normalizeProjectID(jobProject) != identity.ProjectID) {
		return false, nil
	}
	if identity != nil {
		var currentRecord RunArchiveRecord
		if err := json.Unmarshal([]byte(currentRecordJSON), &currentRecord); err != nil {
			return false, err
		}
		// The inspected proof must still be the one owned by this token. A
		// retry can prepare new bytes without changing its opaque intent token.
		if !strings.EqualFold(strings.TrimSpace(currentRecord.ManifestSHA256), strings.TrimSpace(record.ManifestSHA256)) ||
			archivePublicationPointer(&currentRecord) != archivePublicationPointer(record) || currentRecord.ManifestURI != record.ManifestURI {
			return false, nil
		}
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
	// Acquiring the test/job locks may have waited past the lease deadline.
	// Retaining the lease row lock prevents takeover, but does not extend its TTL.
	if identity != nil && identity.LeaseName != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, identity.LeaseName, identity.LeaseOwner)
		if err != nil || !held {
			return false, err
		}
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
	if _, err := tx.Exec(`UPDATE run_archive_collection_intents SET resolved = 1 WHERE test_id = ?`, testID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM run_archive_failures WHERE test_id = ?`, testID); err != nil {
		return false, err
	}
	// A terminal run is never adopted again: drop its controller recovery
	// payload with the archive commit instead of retaining it in the row.
	if _, err := tx.Exec(`UPDATE tests SET telemetry_checkpoint_json = NULL WHERE id = ? AND telemetry_checkpoint_json IS NOT NULL AND status IN ('COMPLETE', 'FAILED', 'ABORTED')`, testID); err != nil {
		return false, err
	}
	// The archive now durably holds the evidence its accepted receipts
	// claim: they are published with it, in the same transaction, so a crash
	// after the commit cannot leave them accepted forever.
	if _, err := tx.Exec(`UPDATE run_evidence_receipts SET state = ?, updated_at = ? WHERE test_id = ? AND state = ?`, EvidenceReceiptPublished, time.Now().UTC(), testID, EvidenceReceiptAccepted); err != nil {
		return false, err
	}
	if identity != nil && identity.RequireAdjunctRefresh {
		// Catalog completion and recovery reconciliation are one transaction.
		// Preserve a newer dirty token, including one queued before the crash.
		now := time.Now().UTC()
		if _, err := tx.Exec(`INSERT INTO archive_refresh_jobs
			(test_id, project_id, generation, intent_token, created_at, updated_at)
			VALUES (?, ?, 1, ?, ?, ?) ON CONFLICT(test_id) DO NOTHING`,
			testID, durableProjectID, uuid.NewString(), now, now); err != nil {
			return false, err
		}
	} else if refreshToken = strings.TrimSpace(refreshToken); refreshToken != "" {
		if _, err := tx.Exec(`DELETE FROM archive_refresh_jobs WHERE test_id = ? AND intent_token = ?`, testID, refreshToken); err != nil {
			return false, err
		}
	}
	if notification != nil {
		if err := s.commitTelemetryNotificationPublicationTx(tx, testID, durableProjectID, record.ManifestSHA256, *notification); err != nil {
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
