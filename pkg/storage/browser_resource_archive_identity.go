package storage

import (
	"context"
	"errors"
	"time"
)

// BrowserResourceArchiveIdentity is one atomic, exact read of the current run
// incarnation and its archive publication. Private materialization identity and
// durable paths are server authority; they are not a public page projection.
type BrowserResourceArchiveIdentity struct {
	TestID             string
	ProjectID          string
	MaterializationKey string
	StartTime          time.Time
	Archive            RunArchiveRecord
}

// ReadBrowserResourceArchiveIdentityContext resolves an initial display alias
// only after an exact primary-ID match. Selection occurs before the archive
// join, so an unarchived/tombstoned exact owner cannot fall through to another
// archived row whose display alias collides. Final callers must compare the
// returned canonical TestID to their admitted canonical identity.
func (s *SQLiteStore) ReadBrowserResourceArchiveIdentityContext(ctx context.Context, id string) (*BrowserResourceArchiveIdentity, error) {
	if ctx == nil || id == "" {
		return nil, errors.New("browser resource archive identity is required")
	}
	var result BrowserResourceArchiveIdentity
	a := &result.Archive
	err := s.db.QueryRowContext(ctx, `WITH selected_test AS (
  SELECT id, COALESCE(project_id,'') AS project_id, COALESCE(materialization_key,'') AS materialization_key, start_time
  FROM tests WHERE id=? OR ref_key=? ORDER BY CASE WHEN id=? THEN 0 ELSE 1 END, id LIMIT 1
 ) SELECT t.id, t.project_id, t.materialization_key, t.start_time,
 a.test_id, COALESCE(a.ref_key,''), a.schema_version, COALESCE(a.archive_backend,''), a.archive_path, a.manifest_path,
 COALESCE(a.manifest_sha256,''), COALESCE(a.archive_uri,''), COALESCE(a.manifest_uri,''), a.transaction_count, a.resource_sample_count,
 a.interval_summary_count, COALESCE(a.trend_point_json,''), a.created_at, a.completed_at
 FROM selected_test t JOIN run_archives a ON a.test_id=t.id
 WHERE NOT EXISTS(SELECT 1 FROM archive_cleanup_jobs cleanup WHERE cleanup.test_id=t.id)`, id, id, id).Scan(
		&result.TestID, &result.ProjectID, &result.MaterializationKey, &result.StartTime,
		&a.TestID, &a.RefKey, &a.SchemaVersion, &a.ArchiveBackend, &a.ArchivePath, &a.ManifestPath, &a.ManifestSHA256, &a.ArchiveURI, &a.ManifestURI,
		&a.TransactionCount, &a.ResourceSampleCount, &a.IntervalSummaryCount, &a.TrendPointJSON, &a.CreatedAt, &a.CompletedAt)
	if err != nil {
		return nil, err
	}
	a.SchemaVersion = normalizeTelemetrySchemaVersion(a.SchemaVersion, archiveRecordSchemaVersion)
	return &result, nil
}

// CompareAndSwapSettingContext retains the existing insert-if-absent contract
// with actual database cancellation. It does not replace the compatibility
// Store interface or its contextless method.
func (s *SQLiteStore) CompareAndSwapSettingContext(ctx context.Context, key, expected, value string) (bool, error) {
	if ctx == nil {
		return false, errors.New("setting context is required")
	}
	if expected == "" {
		result, err := s.db.ExecContext(ctx, `INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?) ON CONFLICT(key) DO NOTHING`, key, value, time.Now())
		if err != nil {
			return false, err
		}
		rows, err := result.RowsAffected()
		return rows == 1, err
	}
	result, err := s.db.ExecContext(ctx, `UPDATE app_settings SET value=?,updated_at=? WHERE key=? AND value=?`, value, time.Now(), key, expected)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}
