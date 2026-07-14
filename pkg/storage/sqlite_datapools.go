package storage

// SQLite store — datapools. Split from sqlite.go (same package, behavior-preserving).

import (
	"database/sql"
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
		if err := json.Unmarshal([]byte(schemaJSON), &pool.Schema); err != nil {
			return nil, fmt.Errorf("decode data pool %s schema: %w", id, err)
		}
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
			if err := json.Unmarshal([]byte(schemaJSON), &pool.Schema); err != nil {
				return nil, fmt.Errorf("decode data pool %s schema: %w", pool.ID, err)
			}
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existingPoolID string
	err = tx.QueryRow(`SELECT pool_id FROM shared_data_pool_rows WHERE id = ?`, row.ID).Scan(&existingPoolID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && existingPoolID != row.PoolID {
		return fmt.Errorf("data pool row %q belongs to pool %q, not %q", row.ID, existingPoolID, row.PoolID)
	}
	result, err := tx.Exec(`INSERT INTO shared_data_pool_rows (id, pool_id, data_json, status, version, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET data_json=excluded.data_json, status=excluded.status, version=shared_data_pool_rows.version + 1, updated_at=excluded.updated_at
		WHERE shared_data_pool_rows.pool_id = excluded.pool_id`,
		row.ID, row.PoolID, string(dataJSON), row.Status, row.Version, row.CreatedAt, row.UpdatedAt)
	if err != nil {
		return err
	}
	if affected, err := result.RowsAffected(); err != nil {
		return err
	} else if affected == 0 {
		return fmt.Errorf("data pool row %q belongs to another pool", row.ID)
	}
	return tx.Commit()
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
			if err := json.Unmarshal([]byte(dataJSON), &row.Data); err != nil {
				return nil, fmt.Errorf("decode data pool row %s: %w", row.ID, err)
			}
		}
		if row.Data == nil {
			row.Data = map[string]string{}
		}
		out = append(out, &row)
	}
	return out, rows.Err()
}

// GetDataPoolRowFromPool resolves a row only within its authorized parent pool
// without loading every row in a potentially large shared data set.
func (s *SQLiteStore) GetDataPoolRowFromPool(poolID string, id string) (*SharedDataPoolRow, error) {
	var row SharedDataPoolRow
	var dataJSON string
	err := s.db.QueryRow(`SELECT id, pool_id, data_json, COALESCE(status, ''), COALESCE(version, 1), created_at, updated_at
		FROM shared_data_pool_rows WHERE pool_id = ? AND id = ?`, poolID, id).Scan(
		&row.ID, &row.PoolID, &dataJSON, &row.Status, &row.Version, &row.CreatedAt, &row.UpdatedAt)
	if err != nil {
		return nil, err
	}
	if dataJSON != "" && dataJSON != "null" {
		if err := json.Unmarshal([]byte(dataJSON), &row.Data); err != nil {
			return nil, err
		}
	}
	if row.Data == nil {
		row.Data = map[string]string{}
	}
	return &row, nil
}

func (s *SQLiteStore) DeleteDataPoolRow(id string) error {
	_, err := s.db.Exec(`DELETE FROM shared_data_pool_rows WHERE id = ?`, id)
	return err
}

// DeleteDataPoolRowFromPool atomically scopes deletion to the authorized pool.
// The bool distinguishes a missing/mismatched row from a successful delete.
func (s *SQLiteStore) DeleteDataPoolRowFromPool(poolID string, id string) (bool, error) {
	result, err := s.db.Exec(`DELETE FROM shared_data_pool_rows WHERE id = ? AND pool_id = ?`, id, poolID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	return count > 0, err
}
