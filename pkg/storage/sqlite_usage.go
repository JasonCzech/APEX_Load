package storage

// SQLite store — usage. Split from sqlite.go (same package, behavior-preserving).

import (
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveUsageAccountingSnapshot(snapshot *UsageAccountingSnapshot) error {
	if snapshot == nil {
		return fmt.Errorf("usage accounting snapshot is required")
	}
	if strings.TrimSpace(snapshot.ID) == "" {
		return fmt.Errorf("usage accounting snapshot id is required")
	}
	if strings.TrimSpace(snapshot.PayloadJSON) == "" {
		return fmt.Errorf("usage accounting snapshot payload is required")
	}
	snapshot.ProjectID = strings.TrimSpace(snapshot.ProjectID)
	if snapshot.GeneratedAt.IsZero() {
		snapshot.GeneratedAt = time.Now().UTC()
	}
	if snapshot.CreatedAt.IsZero() {
		snapshot.CreatedAt = time.Now().UTC()
	}
	_, err := s.db.Exec(`INSERT INTO usage_accounting_snapshots (id, project_id, generated_at, payload_json, created_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET project_id=excluded.project_id, generated_at=excluded.generated_at, payload_json=excluded.payload_json, created_at=excluded.created_at`,
		snapshot.ID,
		snapshot.ProjectID,
		snapshot.GeneratedAt,
		snapshot.PayloadJSON,
		snapshot.CreatedAt,
	)
	return err
}

func (s *SQLiteStore) DeleteUsageAccountingSnapshotsBefore(cutoff time.Time) (int64, error) {
	result, err := s.db.Exec(`DELETE FROM usage_accounting_snapshots WHERE created_at < ?`, cutoff.UTC())
	if err != nil {
		return 0, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return 0, err
	}
	return affected, nil
}

func (s *SQLiteStore) ListUsageAccountingSnapshots(projectID string, limit int, offset int) ([]*UsageAccountingSnapshot, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	if offset < 0 {
		offset = 0
	}
	query := `SELECT id, project_id, generated_at, payload_json, created_at FROM usage_accounting_snapshots`
	args := []any{}
	if strings.TrimSpace(projectID) != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY generated_at DESC, created_at DESC LIMIT ? OFFSET ?`
	args = append(args, limit, offset)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	snapshots := []*UsageAccountingSnapshot{}
	for rows.Next() {
		var snapshot UsageAccountingSnapshot
		if err := rows.Scan(&snapshot.ID, &snapshot.ProjectID, &snapshot.GeneratedAt, &snapshot.PayloadJSON, &snapshot.CreatedAt); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, &snapshot)
	}
	return snapshots, rows.Err()
}
