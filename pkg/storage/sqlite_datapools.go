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
	if pool == nil {
		return errors.New("data pool is required")
	}
	pool.ID = strings.TrimSpace(pool.ID)
	if pool.ID == "" {
		return errors.New("data pool id is required")
	}
	pool.Name = strings.TrimSpace(pool.Name)
	if pool.Name == "" {
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
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, pool.ProjectID); err != nil {
		return err
	}
	ownerQuery := `SELECT COALESCE(project_id, '') FROM shared_data_pools WHERE id = ?`
	if tx.dialect == storeDialectPostgres {
		ownerQuery += ` FOR UPDATE`
	}
	var existingProjectID string
	err = tx.QueryRow(ownerQuery, pool.ID).Scan(&existingProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(existingProjectID) != pool.ProjectID {
		return fmt.Errorf("data pool %q already belongs to project %q", pool.ID, normalizeProjectID(existingProjectID))
	}
	result, err := tx.Exec(`INSERT INTO shared_data_pools (id, name, description, schema_json, project_id, created_at, updated_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, schema_json=excluded.schema_json, updated_at=excluded.updated_at, archived_at=excluded.archived_at
		WHERE COALESCE(NULLIF(TRIM(shared_data_pools.project_id), ''), ?) = excluded.project_id`,
		pool.ID, pool.Name, pool.Description, string(schemaJSON), pool.ProjectID, pool.CreatedAt, pool.UpdatedAt, pool.ArchivedAt, DefaultProjectID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("data pool %q belongs to another project", pool.ID)
	}
	return tx.Commit()
}

// UpdateDataPoolIfUnchanged updates editor-owned fields only when the durable
// row still matches the snapshot loaded by the caller. Project ownership is an
// immutable authorization boundary and is deliberately never rewritten.
func (s *SQLiteStore) UpdateDataPoolIfUnchanged(pool *SharedDataPool, expectedUpdatedAt time.Time) (bool, error) {
	if pool == nil {
		return false, errors.New("data pool is required")
	}
	pool.ID = strings.TrimSpace(pool.ID)
	if pool.ID == "" {
		return false, errors.New("data pool id is required")
	}
	pool.Name = strings.TrimSpace(pool.Name)
	if pool.Name == "" {
		return false, errors.New("data pool name is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected data pool updated_at is required")
	}
	schemaJSON, err := json.Marshal(pool.Schema)
	if err != nil {
		return false, fmt.Errorf("marshal data pool schema: %w", err)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	pool.ProjectID = normalizeProjectID(pool.ProjectID)
	if _, err := lockProjectForOwnedMutation(tx, pool.ProjectID); err != nil {
		return false, err
	}
	var currentProjectID string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	currentQuery := `SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT) FROM shared_data_pools WHERE id = ?`
	if tx.dialect == storeDialectPostgres {
		currentQuery += ` FOR UPDATE`
	}
	if err := tx.QueryRow(currentQuery, pool.ID).Scan(&currentProjectID, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != normalizeProjectID(pool.ProjectID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	pool.ProjectID = normalizeProjectID(currentProjectID)
	pool.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE shared_data_pools SET name = ?, description = ?, schema_json = ?, archived_at = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, pool.Name, pool.Description, string(schemaJSON), pool.ArchivedAt, pool.UpdatedAt, pool.ID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
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
	return s.listDataPools(includeArchived, "")
}

func (s *SQLiteStore) ListDataPoolsForProject(includeArchived bool, projectID string) ([]*SharedDataPool, error) {
	return s.listDataPools(includeArchived, normalizeProjectID(projectID))
}

func (s *SQLiteStore) listDataPools(includeArchived bool, projectID string) ([]*SharedDataPool, error) {
	query := `SELECT id, name, COALESCE(description, ''), COALESCE(schema_json, ''), COALESCE(project_id, ''), created_at, updated_at, archived_at FROM shared_data_pools`
	args := make([]any, 0, 2)
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, projectID)
	}
	if !includeArchived {
		if projectID == "" {
			query += ` WHERE archived_at IS NULL`
		} else {
			query += ` AND archived_at IS NULL`
		}
	}
	query += ` ORDER BY updated_at DESC`
	rows, err := s.db.Query(query, args...)
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
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("data pool id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockDataPoolParentForMutation(tx, id); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	var bindingCount int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM scenario_data_pool_bindings WHERE pool_id = ?`, id).Scan(&bindingCount); err != nil {
		return err
	}
	if bindingCount > 0 {
		return errors.New("data pool is bound to one or more scenarios")
	}
	if _, err := tx.Exec(`DELETE FROM shared_data_pool_rows WHERE pool_id = ?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM shared_data_pools WHERE id = ?`, id); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) SaveDataPoolRow(row *SharedDataPoolRow) error {
	if row == nil {
		return errors.New("data pool row is required")
	}
	row.ID = strings.TrimSpace(row.ID)
	if row.ID == "" {
		return errors.New("data pool row id is required")
	}
	row.PoolID = strings.TrimSpace(row.PoolID)
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
	if _, err := lockDataPoolParentForMutation(tx, row.PoolID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("data pool not found: %s", row.PoolID)
		}
		return err
	}
	var existingPoolID string
	rowQuery := `SELECT pool_id FROM shared_data_pool_rows WHERE id = ?`
	if tx.dialect == storeDialectPostgres {
		rowQuery += ` FOR UPDATE`
	}
	err = tx.QueryRow(rowQuery, row.ID).Scan(&existingPoolID)
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

// UpdateDataPoolRowIfVersion performs an ownership-scoped optimistic update.
// The row version is the public concurrency token; a stale editor cannot
// silently overwrite a newer row or move it between pools.
func (s *SQLiteStore) UpdateDataPoolRowIfVersion(poolID string, row *SharedDataPoolRow, expectedVersion int) (bool, error) {
	if row == nil {
		return false, errors.New("data pool row is required")
	}
	row.ID = strings.TrimSpace(row.ID)
	poolID = strings.TrimSpace(poolID)
	row.PoolID = strings.TrimSpace(row.PoolID)
	if row.ID == "" {
		return false, errors.New("data pool row id is required")
	}
	if poolID == "" || row.PoolID != poolID {
		return false, errors.New("data pool id is required")
	}
	if expectedVersion <= 0 {
		return false, errors.New("expected data pool row version must be positive")
	}
	if row.Data == nil {
		row.Data = map[string]string{}
	}
	if row.Status == "" {
		row.Status = "active"
	}
	dataJSON, err := json.Marshal(row.Data)
	if err != nil {
		return false, fmt.Errorf("marshal data pool row: %w", err)
	}
	row.UpdatedAt = time.Now()
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockDataPoolParentForMutation(tx, poolID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	result, err := tx.Exec(`UPDATE shared_data_pool_rows
		SET data_json = ?, status = ?, version = version + 1, updated_at = ?
		WHERE id = ? AND pool_id = ? AND version = ?`, string(dataJSON), row.Status, row.UpdatedAt, row.ID, poolID, expectedVersion)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	row.Version = expectedVersion + 1
	return true, tx.Commit()
}

func (s *SQLiteStore) ListDataPoolRows(poolID string) ([]*SharedDataPoolRow, error) {
	return s.listDataPoolRows(poolID, 0, 0)
}

// ListDataPoolRowsPage is the bounded API-facing projection. Internal load
// execution keeps using ListDataPoolRows because it intentionally consumes the
// complete pool.
func (s *SQLiteStore) ListDataPoolRowsPage(poolID string, limit, offset int) ([]*SharedDataPoolRow, error) {
	if limit <= 0 {
		return []*SharedDataPoolRow{}, nil
	}
	if offset < 0 {
		offset = 0
	}
	return s.listDataPoolRows(poolID, limit, offset)
}

func (s *SQLiteStore) listDataPoolRows(poolID string, limit, offset int) ([]*SharedDataPoolRow, error) {
	query := `SELECT id, pool_id, data_json, COALESCE(status, ''), COALESCE(version, 1), created_at, updated_at FROM shared_data_pool_rows WHERE pool_id = ? ORDER BY created_at ASC`
	args := []any{poolID}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	rows, err := s.db.Query(query, args...)
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
	id = strings.TrimSpace(id)
	if id == "" {
		return errors.New("data pool row id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var poolID string
	if err := tx.QueryRow(`SELECT pool_id FROM shared_data_pool_rows WHERE id = ?`, id).Scan(&poolID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := lockDataPoolParentForMutation(tx, poolID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	if _, err := tx.Exec(`DELETE FROM shared_data_pool_rows WHERE id = ? AND pool_id = ?`, id, poolID); err != nil {
		return err
	}
	return tx.Commit()
}

// DeleteDataPoolRowFromPool atomically scopes deletion to the authorized pool.
// The bool distinguishes a missing/mismatched row from a successful delete.
func (s *SQLiteStore) DeleteDataPoolRowFromPool(poolID string, id string) (bool, error) {
	poolID = strings.TrimSpace(poolID)
	id = strings.TrimSpace(id)
	if poolID == "" {
		return false, errors.New("data pool id is required")
	}
	if id == "" {
		return false, errors.New("data pool row id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockDataPoolParentForMutation(tx, poolID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	result, err := tx.Exec(`DELETE FROM shared_data_pool_rows WHERE id = ? AND pool_id = ?`, id, poolID)
	if err != nil {
		return false, err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return count > 0, nil
}

// lockDataPoolParentForMutation serializes a pool or row mutation with project
// and pool deletion. The first lookup is deliberately unlocked: the project is
// then locked before the pool, which matches the hierarchy deletion order.
func lockDataPoolParentForMutation(tx *storeTx, poolID string) (string, error) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return "", errors.New("data pool id is required")
	}
	var projectID string
	if err := tx.QueryRow(`SELECT COALESCE(project_id, '') FROM shared_data_pools WHERE id = ?`, poolID).Scan(&projectID); err != nil {
		return "", err
	}
	projectID = normalizeProjectID(projectID)
	if _, err := lockProjectForOwnedMutation(tx, projectID); err != nil {
		return "", err
	}
	result, err := tx.Exec(`UPDATE shared_data_pools SET updated_at = updated_at
		WHERE id = ? AND COALESCE(NULLIF(TRIM(project_id), ''), ?) = ?`, poolID, DefaultProjectID, projectID)
	if err != nil {
		return "", err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", err
	}
	if affected != 1 {
		return "", sql.ErrNoRows
	}
	return projectID, nil
}
