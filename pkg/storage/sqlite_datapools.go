package storage

// SQLite store — datapools. Split from sqlite.go (same package, behavior-preserving).

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveDataPool(pool *SharedDataPool) error {
	if pool.ID == "" {
		return errors.New("data pool id is required")
	}
	if strings.TrimSpace(pool.Name) == "" {
		return errors.New("data pool name is required")
	}
	if pool.CreatedAt.IsZero() {
		pool.CreatedAt = time.Now()
	}
	pool.ProjectID = normalizeProjectID(pool.ProjectID)
	pool.UpdatedAt = time.Now()
	schemaJSON, err := json.Marshal(pool.Schema)
	if err != nil {
		return fmt.Errorf("marshal data pool schema: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO shared_data_pools (id, name, description, schema_json, project_id, created_at, updated_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, schema_json=excluded.schema_json, project_id=excluded.project_id, updated_at=excluded.updated_at, archived_at=excluded.archived_at`,
		pool.ID, pool.Name, pool.Description, string(schemaJSON), pool.ProjectID, pool.CreatedAt, pool.UpdatedAt, pool.ArchivedAt)
	return err
}

func (s *SQLiteStore) GetDataPool(id string) (*SharedDataPool, error) {
	var pool SharedDataPool
	var schemaJSON string
	err := s.db.QueryRow(`SELECT id, name, COALESCE(description, ''), COALESCE(schema_json, ''), COALESCE(project_id, ''), created_at, updated_at, archived_at FROM shared_data_pools WHERE id = ?`, id).Scan(
		&pool.ID, &pool.Name, &pool.Description, &schemaJSON, &pool.ProjectID, &pool.CreatedAt, &pool.UpdatedAt, &pool.ArchivedAt)
	if err != nil {
		return nil, err
	}
	pool.ProjectID = normalizeProjectID(pool.ProjectID)
	if schemaJSON != "" && schemaJSON != "null" {
		_ = json.Unmarshal([]byte(schemaJSON), &pool.Schema)
	}
	return &pool, nil
}

func (s *SQLiteStore) ListDataPools(includeArchived bool) ([]*SharedDataPool, error) {
	query := `SELECT id, name, COALESCE(description, ''), COALESCE(schema_json, ''), COALESCE(project_id, ''), created_at, updated_at, archived_at FROM shared_data_pools`
	if !includeArchived {
		query += ` WHERE archived_at IS NULL`
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.Query(query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var pools []*SharedDataPool
	for rows.Next() {
		var pool SharedDataPool
		var schemaJSON string
		if err := rows.Scan(&pool.ID, &pool.Name, &pool.Description, &schemaJSON, &pool.ProjectID, &pool.CreatedAt, &pool.UpdatedAt, &pool.ArchivedAt); err != nil {
			return nil, err
		}
		pool.ProjectID = normalizeProjectID(pool.ProjectID)
		if schemaJSON != "" && schemaJSON != "null" {
			_ = json.Unmarshal([]byte(schemaJSON), &pool.Schema)
		}
		pools = append(pools, &pool)
	}
	return pools, rows.Err()
}

func (s *SQLiteStore) DeleteDataPool(id string) error {
	var bindingCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM scenario_data_pool_bindings WHERE pool_id = ?`, id).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 0 {
		return errors.New("data pool is bound to one or more scenarios")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM shared_data_pool_rows WHERE pool_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM shared_data_pools WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveDataPoolRow(row *SharedDataPoolRow) error {
	if row.ID == "" {
		return errors.New("data pool row id is required")
	}
	if row.PoolID == "" {
		return errors.New("data pool id is required")
	}
	if row.Data == nil {
		row.Data = map[string]string{}
	}
	if row.Status == "" {
		row.Status = "active"
	}
	if row.Version <= 0 {
		row.Version = 1
	}
	if row.CreatedAt.IsZero() {
		row.CreatedAt = time.Now()
	}
	row.UpdatedAt = time.Now()
	dataJSON, err := json.Marshal(row.Data)
	if err != nil {
		return fmt.Errorf("marshal data pool row: %w", err)
	}
	_, err = s.db.Exec(`INSERT INTO shared_data_pool_rows (id, pool_id, data_json, status, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data_json=excluded.data_json, status=excluded.status, version=shared_data_pool_rows.version + 1, updated_at=excluded.updated_at`,
		row.ID, row.PoolID, string(dataJSON), row.Status, row.Version, row.CreatedAt, row.UpdatedAt)
	return err
}

func (s *SQLiteStore) ListDataPoolRows(poolID string) ([]*SharedDataPoolRow, error) {
	rows, err := s.db.Query(`SELECT id, pool_id, data_json, COALESCE(status, ''), COALESCE(version, 1), created_at, updated_at FROM shared_data_pool_rows WHERE pool_id = ? ORDER BY created_at ASC`, poolID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*SharedDataPoolRow
	for rows.Next() {
		var row SharedDataPoolRow
		var dataJSON string
		if err := rows.Scan(&row.ID, &row.PoolID, &dataJSON, &row.Status, &row.Version, &row.CreatedAt, &row.UpdatedAt); err != nil {
			return nil, err
		}
		if dataJSON != "" && dataJSON != "null" {
			_ = json.Unmarshal([]byte(dataJSON), &row.Data)
		}
		if row.Data == nil {
			row.Data = map[string]string{}
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) DeleteDataPoolRow(id string) error {
	_, err := s.db.Exec(`DELETE FROM shared_data_pool_rows WHERE id = ?`, id)
	return err
}
