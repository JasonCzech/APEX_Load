package storage

// SQLite store — reusable blocks. Same SQL serves PostgreSQL via rewriteSQL.

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveReusableBlock(block *ReusableBlock) error {
	if block == nil {
		return fmt.Errorf("reusable block is required")
	}
	if strings.TrimSpace(block.ID) == "" {
		return errors.New("reusable block id is required")
	}
	block.ProjectID = normalizeProjectID(block.ProjectID)
	if strings.TrimSpace(block.Name) == "" {
		return errors.New("reusable block name is required")
	}
	if strings.TrimSpace(block.Component) == "" {
		return errors.New("reusable block component is required")
	}
	now := time.Now()
	if block.CreatedAt.IsZero() {
		block.CreatedAt = now
	}
	block.UpdatedAt = now
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Lock and validate the owning project in the same transaction as creation.
	// This prevents a concurrent project deletion from acknowledging a dangling
	// block, and keeps project ownership immutable if an internal caller retries an
	// existing ID with different scope.
	projectLock, err := tx.Exec(`UPDATE projects SET updated_at = updated_at WHERE id = ?`, block.ProjectID)
	if err != nil {
		return err
	}
	affected, err := projectLock.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("project not found: %s", block.ProjectID)
	}
	var currentProjectID string
	err = tx.QueryRow(`SELECT project_id FROM reusable_blocks WHERE id = ?`, block.ID).Scan(&currentProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(currentProjectID) != block.ProjectID {
		return fmt.Errorf("reusable block %q belongs to another project", block.ID)
	}
	result, err := tx.Exec(`INSERT INTO reusable_blocks (
			id, project_id, name, kind, component_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, kind=excluded.kind,
			component_json=excluded.component_json, updated_at=excluded.updated_at
		WHERE reusable_blocks.project_id = excluded.project_id`,
		block.ID, block.ProjectID, block.Name, block.Kind, block.Component, block.CreatedAt, block.UpdatedAt)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected != 1 {
		return fmt.Errorf("reusable block %q belongs to another project", block.ID)
	}
	return tx.Commit()
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
