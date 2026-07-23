package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const powerBIPublishColumns = `id, test_id, project_id, workspace_id, COALESCE(dataset_id, ''), COALESCE(dataset_name, ''), schema_version, status, run_rows_pushed, transaction_rows_pushed, transaction_rows_total, COALESCE(error, ''), COALESCE(requested_by, ''), published_at, created_at, updated_at`

func scanPowerBIPublish(scan func(dest ...any) error) (*PowerBIPublish, error) {
	p := &PowerBIPublish{}
	var publishedAt sql.NullTime
	if err := scan(&p.ID, &p.TestID, &p.ProjectID, &p.WorkspaceID, &p.DatasetID, &p.DatasetName,
		&p.SchemaVersion, &p.Status, &p.RunRowsPushed, &p.TransactionRowsPushed, &p.TransactionRowsTotal,
		&p.Error, &p.RequestedBy, &publishedAt, &p.CreatedAt, &p.UpdatedAt); err != nil {
		return nil, err
	}
	if publishedAt.Valid {
		t := publishedAt.Time
		p.PublishedAt = &t
	}
	return p, nil
}

func (s *SQLiteStore) SavePowerBIPublish(p *PowerBIPublish) error {
	if p == nil || strings.TrimSpace(p.ID) == "" {
		return fmt.Errorf("powerbi publish requires an id")
	}
	now := time.Now().UTC()
	if p.CreatedAt.IsZero() {
		p.CreatedAt = now
	}
	p.UpdatedAt = now
	var publishedAt any
	if p.PublishedAt != nil {
		publishedAt = p.PublishedAt.UTC()
	}
	_, err := s.db.Exec(`INSERT INTO powerbi_publishes
		(id, test_id, project_id, workspace_id, dataset_id, dataset_name, schema_version, status, run_rows_pushed, transaction_rows_pushed, transaction_rows_total, error, requested_by, published_at, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			dataset_id = excluded.dataset_id,
			dataset_name = excluded.dataset_name,
			schema_version = excluded.schema_version,
			status = excluded.status,
			run_rows_pushed = excluded.run_rows_pushed,
			transaction_rows_pushed = excluded.transaction_rows_pushed,
			transaction_rows_total = excluded.transaction_rows_total,
			error = excluded.error,
			requested_by = excluded.requested_by,
			published_at = excluded.published_at,
			updated_at = excluded.updated_at`,
		p.ID, p.TestID, p.ProjectID, p.WorkspaceID, p.DatasetID, p.DatasetName, p.SchemaVersion, p.Status,
		p.RunRowsPushed, p.TransactionRowsPushed, p.TransactionRowsTotal, p.Error, p.RequestedBy,
		publishedAt, p.CreatedAt.UTC(), p.UpdatedAt.UTC())
	return err
}

// UpdatePowerBIPublishStatus advances a publish record's lifecycle fields.
// Counters and error text are overwritten; publishedAt is only set when
// non-nil so intermediate progress updates keep the column empty.
func (s *SQLiteStore) UpdatePowerBIPublishStatus(id string, status string, runRows int, txRows int, errMsg string, publishedAt *time.Time) error {
	var published any
	if publishedAt != nil {
		published = publishedAt.UTC()
	}
	res, err := s.db.Exec(`UPDATE powerbi_publishes
		SET status = ?, run_rows_pushed = ?, transaction_rows_pushed = ?, error = ?,
			published_at = COALESCE(?, published_at), updated_at = ?
		WHERE id = ?`,
		status, runRows, txRows, errMsg, published, time.Now().UTC(), id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("powerbi publish %q not found", id)
	}
	return nil
}

// GetLatestPowerBIPublishForDataset returns the most recent publish into a
// specific remote dataset, across all tests. Used for dataset-scoped schema
// drift detection: the per-test latest can miss a version mismatch when this
// test never published but others did.
func (s *SQLiteStore) GetLatestPowerBIPublishForDataset(datasetID string) (*PowerBIPublish, error) {
	row := s.db.QueryRow(`SELECT `+powerBIPublishColumns+` FROM powerbi_publishes
		WHERE dataset_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, datasetID)
	p, err := scanPowerBIPublish(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (s *SQLiteStore) GetLatestPowerBIPublishForTest(testID string) (*PowerBIPublish, error) {
	row := s.db.QueryRow(`SELECT `+powerBIPublishColumns+` FROM powerbi_publishes
		WHERE test_id = ? ORDER BY created_at DESC, id DESC LIMIT 1`, testID)
	p, err := scanPowerBIPublish(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return p, err
}

func (s *SQLiteStore) ListPowerBIPublishesForTest(testID string, limit int) ([]*PowerBIPublish, error) {
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	rows, err := s.db.Query(`SELECT `+powerBIPublishColumns+` FROM powerbi_publishes
		WHERE test_id = ? ORDER BY created_at DESC, id DESC LIMIT ?`, testID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*PowerBIPublish{}
	for rows.Next() {
		p, err := scanPowerBIPublish(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}
