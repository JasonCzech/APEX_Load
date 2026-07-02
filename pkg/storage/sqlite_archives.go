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

func (s *SQLiteStore) SaveRunArchive(record *RunArchiveRecord) error {
	if record == nil {
		return errors.New("run archive record is nil")
	}
	record.SchemaVersion = normalizeTelemetrySchemaVersion(record.SchemaVersion, archiveRecordSchemaVersion)
	_, err := s.db.Exec(`INSERT INTO run_archives (test_id, ref_key, schema_version, archive_backend, archive_path, manifest_path, manifest_sha256, archive_uri, manifest_uri, transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		record.CreatedAt,
		record.CompletedAt,
	)
	return err
}

func (s *SQLiteStore) GetRunArchive(testID string) (*RunArchiveRecord, error) {
	var record RunArchiveRecord
	err := s.db.QueryRow(`SELECT test_id, COALESCE(ref_key, ''), schema_version, COALESCE(archive_backend, ''), archive_path, manifest_path, COALESCE(manifest_sha256, ''), COALESCE(archive_uri, ''), COALESCE(manifest_uri, ''), transaction_count, resource_sample_count, interval_summary_count, created_at, completed_at FROM run_archives WHERE test_id = ?`, testID).Scan(
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
	_, err := s.db.Exec(`INSERT INTO release_sla_waivers (id, release_id, project_id, scope, reason, approved_by, status, expires_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			release_id=excluded.release_id,
			project_id=excluded.project_id,
			scope=excluded.scope,
			reason=excluded.reason,
			approved_by=excluded.approved_by,
			status=excluded.status,
			expires_at=excluded.expires_at,
			updated_at=excluded.updated_at`,
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
	)
	return err
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
	if result == nil {
		return fmt.Errorf("external result import is required")
	}
	result.ID = strings.TrimSpace(result.ID)
	result.Provider = strings.ToLower(strings.TrimSpace(result.Provider))
	result.Name = strings.TrimSpace(result.Name)
	result.ReleaseID = strings.TrimSpace(result.ReleaseID)
	result.ScenarioID = strings.TrimSpace(result.ScenarioID)
	result.Source = strings.TrimSpace(result.Source)
	if result.ID == "" {
		return fmt.Errorf("external result import id is required")
	}
	if result.Provider == "" {
		return fmt.Errorf("external result import provider is required")
	}
	if result.Name == "" {
		result.Name = result.Provider + " import"
	}
	result.ProjectID = normalizeProjectID(result.ProjectID)
	if result.CreatedAt.IsZero() {
		result.CreatedAt = time.Now().UTC()
	}
	summaryJSON, err := json.Marshal(result.Summary)
	if err != nil {
		return fmt.Errorf("marshal external result summary: %w", err)
	}
	rawJSON, err := json.Marshal(result.RawResult)
	if err != nil {
		return fmt.Errorf("marshal external raw result: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO external_result_imports (id, provider, name, project_id, release_id, scenario_id, source, summary_json, raw_json, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			provider=excluded.provider,
			name=excluded.name,
			project_id=excluded.project_id,
			release_id=excluded.release_id,
			scenario_id=excluded.scenario_id,
			source=excluded.source,
			summary_json=excluded.summary_json,
			raw_json=excluded.raw_json`,
		result.ID,
		result.Provider,
		result.Name,
		result.ProjectID,
		result.ReleaseID,
		result.ScenarioID,
		result.Source,
		string(summaryJSON),
		string(rawJSON),
		result.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) ListExternalResultImports(filter ExternalResultImportFilter) ([]*ExternalResultImport, error) {
	if filter.Limit <= 0 {
		filter.Limit = 100
	}
	if filter.Offset < 0 {
		filter.Offset = 0
	}
	query := strings.Builder{}
	query.WriteString(`SELECT id, provider, name, COALESCE(project_id, ''), COALESCE(release_id, ''), COALESCE(scenario_id, ''), COALESCE(source, ''), COALESCE(summary_json, ''), COALESCE(raw_json, ''), created_at FROM external_result_imports WHERE 1 = 1`)
	args := make([]any, 0, 6)
	if projectID := strings.TrimSpace(filter.ProjectID); projectID != "" {
		query.WriteString(` AND project_id = ?`)
		args = append(args, normalizeProjectID(projectID))
	}
	if releaseID := strings.TrimSpace(filter.ReleaseID); releaseID != "" {
		query.WriteString(` AND release_id = ?`)
		args = append(args, releaseID)
	}
	if provider := strings.ToLower(strings.TrimSpace(filter.Provider)); provider != "" {
		query.WriteString(` AND provider = ?`)
		args = append(args, provider)
	}
	query.WriteString(` ORDER BY created_at DESC LIMIT ? OFFSET ?`)
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
	if err := row.Scan(
		&result.ID,
		&result.Provider,
		&result.Name,
		&result.ProjectID,
		&result.ReleaseID,
		&result.ScenarioID,
		&result.Source,
		&summaryJSON,
		&rawJSON,
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
	return &result, nil
}
