package storage

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// enqueueArchiveCleanupTx writes the outbox record in the same transaction that
// removes the owning test/project metadata. Re-inserting the same test refreshes
// the serialized pointer without erasing prior retry diagnostics.
func enqueueArchiveCleanupTx(tx *storeTx, projectID string, record *RunArchiveRecord) error {
	if tx == nil || record == nil || strings.TrimSpace(record.TestID) == "" {
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal archive cleanup record for %s: %w", record.TestID, err)
	}
	now := time.Now().UTC()
	_, err = tx.Exec(`INSERT INTO archive_cleanup_jobs (
			test_id, project_id, archive_record_json, attempts, last_error, created_at, updated_at)
		VALUES (?, ?, ?, 0, NULL, ?, ?)
		ON CONFLICT(test_id) DO UPDATE SET
			project_id=excluded.project_id,
			archive_record_json=excluded.archive_record_json,
			updated_at=excluded.updated_at`,
		record.TestID, normalizeProjectID(projectID), string(encoded), now, now)
	if err != nil {
		return fmt.Errorf("enqueue archive cleanup for %s: %w", record.TestID, err)
	}
	return nil
}

func enqueueArchiveVersionCleanupTx(tx *storeTx, projectID string, record *RunArchiveRecord) error {
	if tx == nil || record == nil || strings.TrimSpace(record.TestID) == "" {
		return nil
	}
	if strings.TrimSpace(record.ArchiveURI) == "" && strings.TrimSpace(record.ManifestURI) == "" && strings.TrimSpace(record.ArchivePath) == "" {
		return nil
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return fmt.Errorf("marshal superseded archive cleanup record for %s: %w", record.TestID, err)
	}
	now := time.Now().UTC()
	jobID := archiveVersionCleanupID(record)
	_, err = tx.Exec(`INSERT INTO archive_version_cleanup_jobs (
			id, test_id, project_id, archive_record_json, attempts, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, 0, NULL, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			archive_record_json=excluded.archive_record_json,
			updated_at=excluded.updated_at`,
		jobID, record.TestID, normalizeProjectID(projectID), string(encoded), now, now)
	if err != nil {
		return fmt.Errorf("enqueue superseded archive cleanup for %s: %w", record.TestID, err)
	}
	return nil
}

func archiveVersionCleanupID(record *RunArchiveRecord) string {
	if record == nil {
		return ""
	}
	// URI-bound publications are already globally unique. Hashing keeps the
	// primary key bounded and avoids embedding credentials or bucket names in it.
	identity := strings.Join([]string{
		strings.TrimSpace(record.ArchiveBackend),
		strings.TrimSpace(record.ArchiveURI),
		strings.TrimSpace(record.ManifestURI),
		strings.TrimSpace(record.ArchivePath),
		strings.TrimSpace(record.ManifestPath),
	}, "\x00")
	digest := sha256.Sum256([]byte(identity))
	return fmt.Sprintf("archive-version-%x", digest[:])
}

func (s *SQLiteStore) SaveArchiveVersionCleanupJob(job *ArchiveVersionCleanupJob) error {
	if job == nil || strings.TrimSpace(job.TestID) == "" {
		return errors.New("archive version cleanup job is required")
	}
	if strings.TrimSpace(job.ArchiveRecord.TestID) == "" {
		job.ArchiveRecord.TestID = strings.TrimSpace(job.TestID)
	}
	if job.ArchiveRecord.TestID != strings.TrimSpace(job.TestID) {
		return fmt.Errorf("archive version cleanup record test id %q does not match job %q", job.ArchiveRecord.TestID, job.TestID)
	}
	if job.ID == "" {
		job.ID = archiveVersionCleanupID(&job.ArchiveRecord)
	}
	encoded, err := json.Marshal(&job.ArchiveRecord)
	if err != nil {
		return fmt.Errorf("encode superseded archive cleanup record: %w", err)
	}
	now := time.Now().UTC()
	if job.CreatedAt.IsZero() {
		job.CreatedAt = now
	}
	job.UpdatedAt = now
	_, err = s.db.Exec(`INSERT INTO archive_version_cleanup_jobs (
			id, test_id, project_id, archive_record_json, attempts, last_error, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET archive_record_json=excluded.archive_record_json, updated_at=excluded.updated_at`,
		job.ID, job.TestID, normalizeProjectID(job.ProjectID), string(encoded), job.Attempts,
		strings.TrimSpace(job.LastError), job.CreatedAt, job.UpdatedAt)
	return err
}

func getRunArchiveTx(tx *storeTx, testID string) (*RunArchiveRecord, error) {
	var record RunArchiveRecord
	err := tx.QueryRow(`SELECT test_id, COALESCE(ref_key, ''), schema_version,
		COALESCE(archive_backend, ''), archive_path, manifest_path,
		COALESCE(manifest_sha256, ''), COALESCE(archive_uri, ''), COALESCE(manifest_uri, ''),
		transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at
		FROM run_archives WHERE test_id = ?`, testID).Scan(
		&record.TestID,
		&record.RefKey,
		&record.SchemaVersion,
		&record.ArchiveBackend,
		&record.ArchivePath,
		&record.ManifestPath,
		&record.ManifestSHA256,
		&record.ArchiveURI,
		&record.ManifestURI,
		&record.TransactionCount,
		&record.ResourceSampleCount,
		&record.IntervalSummaryCount,
		&record.CreatedAt,
		&record.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
	return &record, nil
}

func listProjectRunArchivesTx(tx *storeTx, projectID string) ([]*RunArchiveRecord, error) {
	rows, err := tx.Query(`SELECT ra.test_id, COALESCE(ra.ref_key, ''), ra.schema_version,
		COALESCE(ra.archive_backend, ''), ra.archive_path, ra.manifest_path,
		COALESCE(ra.manifest_sha256, ''), COALESCE(ra.archive_uri, ''), COALESCE(ra.manifest_uri, ''),
		ra.transaction_count, ra.resource_sample_count, ra.interval_summary_count, ra.created_at, ra.completed_at
		FROM run_archives ra JOIN tests t ON t.id = ra.test_id WHERE t.project_id = ?`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]*RunArchiveRecord, 0)
	for rows.Next() {
		var record RunArchiveRecord
		if err := rows.Scan(
			&record.TestID,
			&record.RefKey,
			&record.SchemaVersion,
			&record.ArchiveBackend,
			&record.ArchivePath,
			&record.ManifestPath,
			&record.ManifestSHA256,
			&record.ArchiveURI,
			&record.ManifestURI,
			&record.TransactionCount,
			&record.ResourceSampleCount,
			&record.IntervalSummaryCount,
			&record.CreatedAt,
			&record.CompletedAt,
		); err != nil {
			return nil, err
		}
		record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
		records = append(records, &record)
	}
	return records, rows.Err()
}

func listProjectExternalResultArchivesTx(tx *storeTx, projectID string) ([]*RunArchiveRecord, error) {
	rows, err := tx.Query(`SELECT id, COALESCE(archive_record_json, '')
		FROM external_result_imports WHERE project_id = ?
			AND COALESCE(TRIM(archive_record_json), '') NOT IN ('', 'null')`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	records := make([]*RunArchiveRecord, 0)
	for rows.Next() {
		var importID, encoded string
		if err := rows.Scan(&importID, &encoded); err != nil {
			return nil, err
		}
		var record RunArchiveRecord
		if err := json.Unmarshal([]byte(encoded), &record); err != nil {
			return nil, fmt.Errorf("decode external result archive %s: %w", importID, err)
		}
		if strings.TrimSpace(record.TestID) == "" {
			record.TestID = "external-result:" + strings.TrimSpace(importID)
		}
		records = append(records, &record)
	}
	return records, rows.Err()
}

func (s *SQLiteStore) GetArchiveCleanupJob(testID string) (*ArchiveCleanupJob, error) {
	var job ArchiveCleanupJob
	var encoded string
	err := s.db.QueryRow(`SELECT test_id, project_id, archive_record_json, attempts,
		COALESCE(last_error, ''), created_at, updated_at
		FROM archive_cleanup_jobs WHERE test_id = ?`, strings.TrimSpace(testID)).Scan(
		&job.TestID, &job.ProjectID, &encoded, &job.Attempts, &job.LastError, &job.CreatedAt, &job.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(encoded), &job.ArchiveRecord); err != nil {
		return nil, fmt.Errorf("decode archive cleanup job %s: %w", job.TestID, err)
	}
	if strings.TrimSpace(job.ArchiveRecord.TestID) == "" {
		job.ArchiveRecord.TestID = job.TestID
	}
	return &job, nil
}

func (s *SQLiteStore) SaveArchiveCleanupJob(job *ArchiveCleanupJob) error {
	if job == nil || strings.TrimSpace(job.TestID) == "" {
		return errors.New("archive cleanup job is required")
	}
	if strings.TrimSpace(job.ArchiveRecord.TestID) == "" {
		job.ArchiveRecord.TestID = strings.TrimSpace(job.TestID)
	}
	if job.ArchiveRecord.TestID != strings.TrimSpace(job.TestID) {
		return fmt.Errorf("archive cleanup record test id %q does not match job %q", job.ArchiveRecord.TestID, job.TestID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	testQuery := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
	}
	var durableProjectID string
	if err := tx.QueryRow(testQuery, job.TestID).Scan(&durableProjectID); err != nil {
		return err
	}
	if normalizeProjectID(durableProjectID) != normalizeProjectID(job.ProjectID) {
		return fmt.Errorf("archive cleanup project %q does not match durable test project %q", job.ProjectID, durableProjectID)
	}
	if err := enqueueArchiveCleanupTx(tx, job.ProjectID, &job.ArchiveRecord); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM archive_finalization_jobs WHERE test_id = ?`, job.TestID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM archive_refresh_jobs WHERE test_id = ?`, job.TestID); err != nil {
		return err
	}
	return tx.Commit()
}

// SaveArchiveCleanupJobIfArchiveUnchanged installs a retention tombstone only
// when the durable catalog still names the exact publication selected by the
// caller. The test-row lock is shared with archive publication, which closes
// the read-to-tombstone race on PostgreSQL; SQLite's write transaction provides
// the corresponding serialization there.
func (s *SQLiteStore) SaveArchiveCleanupJobIfArchiveUnchanged(job *ArchiveCleanupJob) (bool, error) {
	if job == nil || strings.TrimSpace(job.TestID) == "" {
		return false, errors.New("archive cleanup job is required")
	}
	if strings.TrimSpace(job.ArchiveRecord.TestID) == "" {
		job.ArchiveRecord.TestID = strings.TrimSpace(job.TestID)
	}
	if job.ArchiveRecord.TestID != strings.TrimSpace(job.TestID) {
		return false, fmt.Errorf("archive cleanup record test id %q does not match job %q", job.ArchiveRecord.TestID, job.TestID)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	testQuery := `SELECT COALESCE(project_id, '') FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
	}
	var durableProjectID string
	if err := tx.QueryRow(testQuery, job.TestID).Scan(&durableProjectID); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if normalizeProjectID(durableProjectID) != normalizeProjectID(job.ProjectID) {
		return false, fmt.Errorf("archive cleanup project %q does not match durable test project %q", job.ProjectID, durableProjectID)
	}
	current, err := getRunArchiveTx(tx, job.TestID)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !sameRunArchivePublicationVersion(current, &job.ArchiveRecord) {
		return false, nil
	}
	if err := enqueueArchiveCleanupTx(tx, job.ProjectID, &job.ArchiveRecord); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM archive_finalization_jobs WHERE test_id = ?`, job.TestID); err != nil {
		return false, err
	}
	if _, err := tx.Exec(`DELETE FROM archive_refresh_jobs WHERE test_id = ?`, job.TestID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func sameRunArchivePublicationVersion(left, right *RunArchiveRecord) bool {
	if left == nil || right == nil {
		return false
	}
	return left.TestID == right.TestID &&
		left.RefKey == right.RefKey &&
		normalizeTelemetrySchemaVersion(left.SchemaVersion, archiveRecordSchemaVersion) == normalizeTelemetrySchemaVersion(right.SchemaVersion, archiveRecordSchemaVersion) &&
		left.ArchiveBackend == right.ArchiveBackend &&
		left.ArchivePath == right.ArchivePath &&
		left.ManifestPath == right.ManifestPath &&
		left.ManifestSHA256 == right.ManifestSHA256 &&
		left.ArchiveURI == right.ArchiveURI &&
		left.ManifestURI == right.ManifestURI &&
		left.TransactionCount == right.TransactionCount &&
		left.ResourceSampleCount == right.ResourceSampleCount &&
		left.IntervalSummaryCount == right.IntervalSummaryCount &&
		left.CreatedAt.Equal(right.CreatedAt) &&
		left.CompletedAt.Equal(right.CompletedAt)
}

func (s *SQLiteStore) ListArchiveCleanupJobs(limit int) ([]*ArchiveCleanupJob, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT test_id, project_id, archive_record_json, attempts,
		COALESCE(last_error, ''), created_at, updated_at
		FROM archive_cleanup_jobs ORDER BY updated_at ASC, test_id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]*ArchiveCleanupJob, 0)
	for rows.Next() {
		var job ArchiveCleanupJob
		var encoded string
		if err := rows.Scan(&job.TestID, &job.ProjectID, &encoded, &job.Attempts, &job.LastError, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &job.ArchiveRecord); err != nil {
			return nil, fmt.Errorf("decode archive cleanup job %s: %w", job.TestID, err)
		}
		if strings.TrimSpace(job.ArchiveRecord.TestID) == "" {
			job.ArchiveRecord.TestID = job.TestID
		}
		jobs = append(jobs, &job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) MarkArchiveCleanupJobFailed(testID string, cleanupErr error, updatedAt time.Time) error {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return errors.New("test id is required")
	}
	message := ""
	if cleanupErr != nil {
		message = cleanupErr.Error()
		if len(message) > 4096 {
			message = message[:4096]
		}
	}
	result, err := s.db.Exec(`UPDATE archive_cleanup_jobs
		SET attempts = attempts + 1, last_error = ?, updated_at = ? WHERE test_id = ?`,
		message, updatedAt.UTC(), testID)
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

func (s *SQLiteStore) DeleteArchiveCleanupJob(testID string) error {
	testID = strings.TrimSpace(testID)
	if testID == "" {
		return errors.New("test id is required")
	}
	_, err := s.db.Exec(`DELETE FROM archive_cleanup_jobs WHERE test_id = ?`, testID)
	return err
}

func (s *SQLiteStore) ListArchiveVersionCleanupJobs(limit int) ([]*ArchiveVersionCleanupJob, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	rows, err := s.db.Query(`SELECT id, test_id, project_id, archive_record_json, attempts,
		COALESCE(last_error, ''), created_at, updated_at
		FROM archive_version_cleanup_jobs ORDER BY updated_at ASC, id ASC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	jobs := make([]*ArchiveVersionCleanupJob, 0)
	for rows.Next() {
		var job ArchiveVersionCleanupJob
		var encoded string
		if err := rows.Scan(&job.ID, &job.TestID, &job.ProjectID, &encoded, &job.Attempts, &job.LastError, &job.CreatedAt, &job.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(encoded), &job.ArchiveRecord); err != nil {
			return nil, fmt.Errorf("decode superseded archive cleanup job %s: %w", job.ID, err)
		}
		if strings.TrimSpace(job.ArchiveRecord.TestID) == "" {
			job.ArchiveRecord.TestID = job.TestID
		}
		jobs = append(jobs, &job)
	}
	return jobs, rows.Err()
}

func (s *SQLiteStore) MarkArchiveVersionCleanupJobFailed(id string, cleanupErr error, updatedAt time.Time) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("archive version cleanup id is required")
	}
	message := ""
	if cleanupErr != nil {
		message = cleanupErr.Error()
		if len(message) > 4096 {
			message = message[:4096]
		}
	}
	result, err := s.db.Exec(`UPDATE archive_version_cleanup_jobs
		SET attempts = attempts + 1, last_error = ?, updated_at = ? WHERE id = ?`,
		message, updatedAt.UTC(), id)
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

func (s *SQLiteStore) DeleteArchiveVersionCleanupJob(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("archive version cleanup id is required")
	}
	_, err := s.db.Exec(`DELETE FROM archive_version_cleanup_jobs WHERE id = ?`, id)
	return err
}
