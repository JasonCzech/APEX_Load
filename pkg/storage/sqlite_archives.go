package storage

// SQLite store — archives. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ErrArchiveCleanupPending means retention/deletion has already installed a
// durable tombstone, so archive metadata must not be (re)published.
var ErrArchiveCleanupPending = errors.New("archive cleanup is pending")

func (s *SQLiteStore) SaveRunArchive(record *RunArchiveRecord) error {
	if record == nil {
		return errors.New("run archive record is nil")
	}
	record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existenceQuery := `SELECT 1 FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		existenceQuery += ` FOR UPDATE`
	}
	var exists int
	if err := tx.QueryRow(existenceQuery, record.TestID).Scan(&exists); err != nil {
		return err
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, record.TestID).Scan(&cleanupExists); err != nil {
		return err
	}
	if cleanupExists != 0 {
		return ErrArchiveCleanupPending
	}
	_, err = tx.Exec(`INSERT INTO run_archives (test_id, ref_key, schema_version, archive_backend, archive_path, manifest_path, manifest_sha256, archive_uri, manifest_uri, transaction_count, resource_sample_count, interval_summary_count, trend_point_json, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(test_id) DO UPDATE SET
		ref_key=CASE WHEN TRIM(COALESCE(excluded.ref_key, '')) = '' THEN run_archives.ref_key ELSE excluded.ref_key END,
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
		trend_point_json=CASE WHEN TRIM(COALESCE(excluded.trend_point_json, '')) = '' THEN run_archives.trend_point_json ELSE excluded.trend_point_json END,
		created_at=excluded.created_at,
		completed_at=excluded.completed_at`,
		record.TestID,
		record.RefKey,
		record.SchemaVersion,
		record.ArchiveBackend,
		record.ArchivePath,
		record.ManifestPath,
		record.ManifestSHA256,
		record.ArchiveURI,
		record.ManifestURI,
		record.TransactionCount,
		record.ResourceSampleCount,
		record.IntervalSummaryCount,
		record.TrendPointJSON,
		record.CreatedAt,
		record.CompletedAt,
	)
	if err != nil {
		return err
	}
	// A successful collection supersedes its earlier failure permanently, even
	// after retention eventually removes this catalog entry.
	if _, err := tx.Exec(`UPDATE run_archive_collection_intents SET resolved = 1 WHERE test_id = ?`, record.TestID); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM run_archive_failures WHERE test_id = ?`, record.TestID); err != nil {
		return err
	}
	var projectID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id,'') FROM tests WHERE id=?`, record.TestID).Scan(&projectID); err != nil {
		return err
	}
	if err := enqueueConfluencePublicationTx(tx, record.TestID, projectID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateRunArchiveTrendPointIfUnchanged updates only the derived trend
// projection for the publication described by expected. It deliberately does
// not route through SaveRunArchive: a release-trend request can hold an older
// catalog snapshot while a manifest refresh publishes a newer archive version.
// The old snapshot must never overwrite that newer publication's pointers or
// counts. The existing trend value participates in the CAS so concurrent
// repairs cannot overwrite one another either.
func (s *SQLiteStore) UpdateRunArchiveTrendPointIfUnchanged(expected *RunArchiveRecord, trendPointJSON string) (bool, error) {
	if expected == nil || strings.TrimSpace(expected.TestID) == "" {
		return false, errors.New("expected run archive is required")
	}
	if strings.TrimSpace(trendPointJSON) == "" {
		return false, errors.New("run archive trend point is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	testQuery := `SELECT 1 FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
	}
	var exists int
	if err := tx.QueryRow(testQuery, expected.TestID).Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, expected.TestID).Scan(&cleanupExists); err != nil {
		return false, err
	}
	if cleanupExists != 0 {
		return false, nil
	}

	result, err := tx.Exec(`UPDATE run_archives SET trend_point_json = ?
		WHERE test_id = ?
		  AND COALESCE(ref_key, '') = ?
		  AND schema_version = ?
		  AND COALESCE(archive_backend, '') = ?
		  AND archive_path = ?
		  AND manifest_path = ?
		  AND COALESCE(manifest_sha256, '') = ?
		  AND COALESCE(archive_uri, '') = ?
		  AND COALESCE(manifest_uri, '') = ?
		  AND transaction_count = ?
		  AND resource_sample_count = ?
		  AND interval_summary_count = ?
		  AND COALESCE(trend_point_json, '') = ?
		  AND created_at = ?
		  AND completed_at = ?`,
		trendPointJSON,
		expected.TestID,
		expected.RefKey,
		normalizeTelemetrySchemaVersion(expected.SchemaVersion, archiveRecordSchemaVersion),
		expected.ArchiveBackend,
		expected.ArchivePath,
		expected.ManifestPath,
		expected.ManifestSHA256,
		expected.ArchiveURI,
		expected.ManifestURI,
		expected.TransactionCount,
		expected.ResourceSampleCount,
		expected.IntervalSummaryCount,
		expected.TrendPointJSON,
		expected.CreatedAt,
		expected.CompletedAt,
	)
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

func (s *SQLiteStore) GetRunArchive(testID string) (*RunArchiveRecord, error) {
	var record RunArchiveRecord
	err := s.db.QueryRow(`SELECT test_id, COALESCE(ref_key, ''), schema_version, COALESCE(archive_backend, ''), archive_path, manifest_path, COALESCE(manifest_sha256, ''), COALESCE(archive_uri, ''), COALESCE(manifest_uri, ''), transaction_count, resource_sample_count, interval_summary_count, COALESCE(trend_point_json, ''), created_at, completed_at FROM run_archives WHERE test_id = ?`, testID).Scan(
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
		&record.TrendPointJSON,
		&record.CreatedAt,
		&record.CompletedAt,
	)
	if err != nil {
		return nil, err
	}
	record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
	return &record, nil
}

// DeleteRunArchive removes the persisted pointer to a run archive. Physical
// archive data is deliberately deleted by the API's archive backend first so
// a failed object-store deletion cannot be hidden by removing this record.
func (s *SQLiteStore) DeleteRunArchive(testID string) error {
	if strings.TrimSpace(testID) == "" {
		return errors.New("test id is required")
	}
	_, err := s.db.Exec(`DELETE FROM run_archives WHERE test_id = ?`, testID)
	return err
}

// DeleteRunArchiveIfUnchanged removes exactly the publication described by
// expected. Retention and cleanup work can outlive the catalog read that
// selected it, so deleting by test ID alone could erase a newer publication.
func (s *SQLiteStore) DeleteRunArchiveIfUnchanged(expected *RunArchiveRecord) (bool, error) {
	if expected == nil || strings.TrimSpace(expected.TestID) == "" {
		return false, errors.New("expected run archive is required")
	}
	result, err := s.db.Exec(`DELETE FROM run_archives
		WHERE test_id = ?
		  AND COALESCE(ref_key, '') = ?
		  AND schema_version = ?
		  AND COALESCE(archive_backend, '') = ?
		  AND archive_path = ?
		  AND manifest_path = ?
		  AND COALESCE(manifest_sha256, '') = ?
		  AND COALESCE(archive_uri, '') = ?
		  AND COALESCE(manifest_uri, '') = ?
		  AND transaction_count = ?
		  AND resource_sample_count = ?
		  AND interval_summary_count = ?
		  AND created_at = ?
		  AND completed_at = ?`,
		expected.TestID,
		expected.RefKey,
		normalizeTelemetrySchemaVersion(expected.SchemaVersion, archiveRecordSchemaVersion),
		expected.ArchiveBackend,
		expected.ArchivePath,
		expected.ManifestPath,
		expected.ManifestSHA256,
		expected.ArchiveURI,
		expected.ManifestURI,
		expected.TransactionCount,
		expected.ResourceSampleCount,
		expected.IntervalSummaryCount,
		expected.CreatedAt,
		expected.CompletedAt,
	)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *SQLiteStore) SaveReleaseSLAWaiver(waiver *ReleaseSLAWaiver) error {
	if waiver == nil {
		return fmt.Errorf("release SLA waiver is required")
	}
	waiver.ID = strings.TrimSpace(waiver.ID)
	waiver.ReleaseID = strings.TrimSpace(waiver.ReleaseID)
	waiver.Scope = strings.TrimSpace(waiver.Scope)
	waiver.Reason = strings.TrimSpace(waiver.Reason)
	waiver.ApprovedBy = strings.TrimSpace(waiver.ApprovedBy)
	waiver.Status = strings.TrimSpace(waiver.Status)
	if waiver.ID == "" {
		return fmt.Errorf("release SLA waiver id is required")
	}
	if waiver.ReleaseID == "" {
		return fmt.Errorf("release id is required")
	}
	if waiver.Scope == "" {
		waiver.Scope = "release"
	}
	if waiver.Reason == "" {
		return fmt.Errorf("release SLA waiver reason is required")
	}
	if waiver.Status == "" {
		waiver.Status = "active"
	}
	waiver.ProjectID = normalizeProjectID(waiver.ProjectID)
	now := time.Now().UTC()
	if waiver.CreatedAt.IsZero() {
		waiver.CreatedAt = now
	}
	waiver.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Validate the complete ownership chain rather than relying on the nullable
	// foreign keys alone. This also makes the check portable to legacy databases
	// whose release row may predate project_id backfills.
	releaseQuery := `SELECT COALESCE(r.project_id, ''), COALESCE(a.project_id, ''), r.archived_at
		FROM releases r JOIN applications a ON a.id = r.application_id WHERE r.id = ?`
	if s.db.dialect == storeDialectPostgres {
		releaseQuery += ` FOR UPDATE`
	}
	var releaseProjectID, applicationProjectID string
	var archivedAt sql.NullTime
	if err := tx.QueryRow(releaseQuery, waiver.ReleaseID).Scan(&releaseProjectID, &applicationProjectID, &archivedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("release not found: %s", waiver.ReleaseID)
		}
		return fmt.Errorf("load release SLA waiver parent: %w", err)
	}
	releaseProjectID = normalizeProjectID(releaseProjectID)
	applicationProjectID = normalizeProjectID(applicationProjectID)
	if releaseProjectID != applicationProjectID {
		return fmt.Errorf("release project does not match application project")
	}
	if waiver.ProjectID != releaseProjectID {
		return fmt.Errorf("release SLA waiver project does not match release project")
	}
	if archivedAt.Valid {
		return fmt.Errorf("cannot create or update an SLA waiver for an archived release")
	}

	ownerQuery := `SELECT release_id, COALESCE(project_id, '') FROM release_sla_waivers WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		ownerQuery += ` FOR UPDATE`
	}
	var currentReleaseID, currentProjectID string
	if err := tx.QueryRow(ownerQuery, waiver.ID).Scan(&currentReleaseID, &currentProjectID); err == nil {
		if strings.TrimSpace(currentReleaseID) != waiver.ReleaseID || normalizeProjectID(currentProjectID) != waiver.ProjectID {
			return fmt.Errorf("release SLA waiver id already belongs to another release or project")
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("load existing release SLA waiver: %w", err)
	}

	result, err := tx.Exec(`INSERT INTO release_sla_waivers (id, release_id, project_id, scope, reason, approved_by, status, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			scope=excluded.scope,
			reason=excluded.reason,
			approved_by=excluded.approved_by,
			status=excluded.status,
			expires_at=excluded.expires_at,
			updated_at=excluded.updated_at
		WHERE release_sla_waivers.release_id = excluded.release_id
		  AND CASE WHEN TRIM(COALESCE(release_sla_waivers.project_id, '')) = '' THEN ? ELSE release_sla_waivers.project_id END
		      = CASE WHEN TRIM(COALESCE(excluded.project_id, '')) = '' THEN ? ELSE excluded.project_id END`,
		waiver.ID,
		waiver.ReleaseID,
		waiver.ProjectID,
		waiver.Scope,
		waiver.Reason,
		waiver.ApprovedBy,
		waiver.Status,
		waiver.ExpiresAt,
		waiver.CreatedAt,
		waiver.UpdatedAt,
		DefaultProjectID,
		DefaultProjectID,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("release SLA waiver id already belongs to another release or project")
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListReleaseSLAWaivers(releaseID string) ([]*ReleaseSLAWaiver, error) {
	releaseID = strings.TrimSpace(releaseID)
	if releaseID == "" {
		return nil, fmt.Errorf("release id is required")
	}
	rows, err := s.db.Query(`SELECT id, release_id, COALESCE(project_id, ''), scope, reason, COALESCE(approved_by, ''), status, expires_at, created_at, updated_at
		FROM release_sla_waivers WHERE release_id = ? ORDER BY created_at DESC`, releaseID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	waivers := []*ReleaseSLAWaiver{}
	for rows.Next() {
		waiver, err := scanReleaseSLAWaiver(rows)
		if err != nil {
			return nil, err
		}
		waivers = append(waivers, waiver)
	}
	return waivers, rows.Err()
}

func scanReleaseSLAWaiver(row scanner) (*ReleaseSLAWaiver, error) {
	var waiver ReleaseSLAWaiver
	var expiresAt sql.NullTime
	if err := row.Scan(
		&waiver.ID,
		&waiver.ReleaseID,
		&waiver.ProjectID,
		&waiver.Scope,
		&waiver.Reason,
		&waiver.ApprovedBy,
		&waiver.Status,
		&expiresAt,
		&waiver.CreatedAt,
		&waiver.UpdatedAt,
	); err != nil {
		return nil, err
	}
	waiver.ProjectID = normalizeProjectID(waiver.ProjectID)
	if expiresAt.Valid {
		waiver.ExpiresAt = &expiresAt.Time
	}
	if waiver.ExpiresAt != nil && waiver.ExpiresAt.Before(time.Now().UTC()) && waiver.Status == "active" {
		waiver.Status = "expired"
	}
	return &waiver, nil
}

func (s *SQLiteStore) SaveExternalResultImport(result *ExternalResultImport) error {
	summaryJSON, rawJSON, archiveRecordJSON, err := prepareExternalResultImportInsert(result)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := validateExternalResultImportPlacement(tx, result, true); err != nil {
		return err
	}
	written, err := tx.Exec(`INSERT INTO external_result_imports (id, provider, name, project_id, application_id, release_id, scenario_id, source, summary_json, raw_json, archive_record_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider=excluded.provider,
			name=excluded.name,
			project_id=excluded.project_id,
			application_id=excluded.application_id,
			release_id=excluded.release_id,
			scenario_id=excluded.scenario_id,
			source=excluded.source,
			summary_json=excluded.summary_json,
			raw_json=excluded.raw_json,
			archive_record_json=excluded.archive_record_json
		WHERE external_result_imports.project_id=excluded.project_id`,
		result.ID,
		result.Provider,
		result.Name,
		result.ProjectID,
		nullableString(result.ApplicationID),
		result.ReleaseID,
		result.ScenarioID,
		result.Source,
		summaryJSON,
		rawJSON,
		archiveRecordJSON,
		result.CreatedAt,
	)
	if err != nil {
		return err
	}
	affected, err := written.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("external result import %q project ownership changed concurrently", result.ID)
	}
	return tx.Commit()
}

func (s *SQLiteStore) ListExternalResultImports(filter ExternalResultImportFilter) ([]*ExternalResultImport, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := strings.Builder{}
	query.WriteString(`SELECT id, provider, name, COALESCE(project_id, ''), COALESCE(application_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(source, ''), COALESCE(summary_json, ''), COALESCE(raw_json, ''), COALESCE(archive_record_json, ''), created_at FROM external_result_imports WHERE 1 = 1`)
	args := make([]any, 0, 7)
	if projectID := strings.TrimSpace(filter.ProjectID); projectID != "" {
		query.WriteString(` AND project_id = ?`)
		args = append(args, normalizeProjectID(projectID))
	}
	if applicationID := strings.TrimSpace(filter.ApplicationID); applicationID != "" {
		query.WriteString(` AND application_id = ?`)
		args = append(args, applicationID)
	}
	if releaseID := strings.TrimSpace(filter.ReleaseID); releaseID != "" {
		query.WriteString(` AND release_id = ?`)
		args = append(args, releaseID)
	}
	if provider := strings.ToLower(strings.TrimSpace(filter.Provider)); provider != "" {
		query.WriteString(` AND provider = ?`)
		args = append(args, provider)
	}
	query.WriteString(` ORDER BY created_at DESC, id DESC LIMIT ? OFFSET ?`)
	args = append(args, filter.Limit, filter.Offset)
	rows, err := s.db.Query(query.String(), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	imports := []*ExternalResultImport{}
	for rows.Next() {
		result, err := scanExternalResultImport(rows)
		if err != nil {
			return nil, err
		}
		imports = append(imports, result)
	}
	return imports, rows.Err()
}

func scanExternalResultImport(row scanner) (*ExternalResultImport, error) {
	var result ExternalResultImport
	var summaryJSON string
	var rawJSON string
	var archiveRecordJSON string
	if err := row.Scan(
		&result.ID,
		&result.Provider,
		&result.Name,
		&result.ProjectID,
		&result.ApplicationID,
		&result.ReleaseID,
		&result.ScenarioID,
		&result.Source,
		&summaryJSON,
		&rawJSON,
		&archiveRecordJSON,
		&result.CreatedAt,
	); err != nil {
		return nil, err
	}
	result.ProjectID = normalizeProjectID(result.ProjectID)
	if strings.TrimSpace(summaryJSON) != "" && strings.TrimSpace(summaryJSON) != "null" {
		if err := json.Unmarshal([]byte(summaryJSON), &result.Summary); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(rawJSON) != "" && strings.TrimSpace(rawJSON) != "null" {
		if err := json.Unmarshal([]byte(rawJSON), &result.RawResult); err != nil {
			return nil, err
		}
	}
	if strings.TrimSpace(archiveRecordJSON) != "" && strings.TrimSpace(archiveRecordJSON) != "null" {
		if err := json.Unmarshal([]byte(archiveRecordJSON), &result.ArchiveRecord); err != nil {
			return nil, err
		}
	}
	return &result, nil
}
