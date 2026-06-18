package storage

// SQLite store — reusable blocks. Same SQL serves PostgreSQL via rewriteSQL.

import (
	"fmt"
	"time"
)

func (s *SQLiteStore) SaveReusableBlock(block *ReusableBlock) error {
	if block == nil {
		return fmt.Errorf("reusable block is required")
	}
	block.ProjectID = normalizeProjectID(block.ProjectID)
	now := time.Now()
	if block.CreatedAt.IsZero() {
		block.CreatedAt = now
	}
	block.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO reusable_blocks (
			id, project_id, name, kind, component_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id, name=excluded.name, kind=excluded.kind,
			component_json=excluded.component_json, updated_at=excluded.updated_at`,
		block.ID, block.ProjectID, block.Name, block.Kind, block.Component, block.CreatedAt, block.UpdatedAt)
	return err
}

const reusableBlockSelect = `SELECT id, COALESCE(project_id, ''), name, COALESCE(kind, ''),
	COALESCE(component_json, ''), created_at, updated_at FROM reusable_blocks`

func scanReusableBlock(row capacityRowScanner) (*ReusableBlock, error) {
	var b ReusableBlock
	if err := row.Scan(&b.ID, &b.ProjectID, &b.Name, &b.Kind, &b.Component, &b.CreatedAt, &b.UpdatedAt); err != nil {
		return nil, err
	}
	return &b, nil
}

func (s *SQLiteStore) GetReusableBlock(id string) (*ReusableBlock, error) {
	return scanReusableBlock(s.db.QueryRow(reusableBlockSelect+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListReusableBlocks(projectID string) ([]*ReusableBlock, error) {
	query := reusableBlockSelect
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY created_at DESC`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	blocks := make([]*ReusableBlock, 0)
	for rows.Next() {
		b, err := scanReusableBlock(rows)
		if err != nil {
			return nil, err
		}
		blocks = append(blocks, b)
	}
	return blocks, rows.Err()
}

func (s *SQLiteStore) DeleteReusableBlock(id string) error {
	_, err := s.db.Exec(`DELETE FROM reusable_blocks WHERE id = ?`, id)
	return err
}
