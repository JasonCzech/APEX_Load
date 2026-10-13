package storage

// SQLite store — datapools. Split from sqlite.go (same package, behavior-preserving).

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
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
	// Postgres timestamptz stores microseconds; without truncation the value
	// returned to clients (nanoseconds) can never Equal the stored CAS token.
	pool.CreatedAt = pool.CreatedAt.Truncate(time.Microsecond)
	pool.ProjectID = normalizeProjectID(pool.ProjectID)
	pool.UpdatedAt = time.Now().Truncate(time.Microsecond)
	schemaJSON, err := json.Marshal(pool.Schema)
	if err != nil {
		return fmt.Errorf("marshal data pool schema: %w", err)
	}
	columnsJSON, err := encodeDataPoolColumns(pool)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := lockProjectForOwnedMutation(tx, pool.ProjectID); err != nil {
		return err
	}
	ownerQuery := `SELECT COALESCE(project_id, ''), updated_at FROM shared_data_pools WHERE id = ?`
	if tx.dialect == storeDialectPostgres {
		ownerQuery += ` FOR UPDATE`
	}
	var existingProjectID string
	var existingUpdatedAt time.Time
	err = tx.QueryRow(ownerQuery, pool.ID).Scan(&existingProjectID, &existingUpdatedAt)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(existingProjectID) != pool.ProjectID {
		return fmt.Errorf("data pool %q already belongs to project %q", pool.ID, normalizeProjectID(existingProjectID))
	}
	if err == nil && !pool.UpdatedAt.After(existingUpdatedAt) {
		pool.UpdatedAt = existingUpdatedAt.Add(time.Microsecond)
	}
	result, err := tx.Exec(`INSERT INTO shared_data_pools (id, name, description, schema_json, columns_json, project_id, created_at, updated_at, archived_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET name=excluded.name, description=excluded.description, schema_json=excluded.schema_json, columns_json=excluded.columns_json, updated_at=excluded.updated_at, archived_at=excluded.archived_at
		WHERE COALESCE(NULLIF(TRIM(shared_data_pools.project_id), ''), ?) = excluded.project_id`,
		pool.ID, pool.Name, pool.Description, string(schemaJSON), columnsJSON, pool.ProjectID, pool.CreatedAt, pool.UpdatedAt, pool.ArchivedAt, DefaultProjectID)
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
//
// It leaves the column order as stored, since imports maintain it without
// moving updated_at; on success pool.Columns holds the stored order.
func (s *SQLiteStore) UpdateDataPoolIfUnchanged(pool *SharedDataPool, expectedUpdatedAt time.Time) (bool, error) {
	return s.updateDataPoolIfUnchanged(pool, expectedUpdatedAt, false)
}

// UpdateDataPoolAndColumnsIfUnchanged is UpdateDataPoolIfUnchanged that also
// replaces the column order with pool.Columns, for an editor that changed it.
func (s *SQLiteStore) UpdateDataPoolAndColumnsIfUnchanged(pool *SharedDataPool, expectedUpdatedAt time.Time) (bool, error) {
	return s.updateDataPoolIfUnchanged(pool, expectedUpdatedAt, true)
}

func (s *SQLiteStore) updateDataPoolIfUnchanged(pool *SharedDataPool, expectedUpdatedAt time.Time, setColumns bool) (bool, error) {
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
	// The order is validated and written only when the caller replaces it.
	var columnsJSON any
	if setColumns {
		if columnsJSON, err = encodeDataPoolColumns(pool); err != nil {
			return false, err
		}
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
	var currentProjectID, currentColumnsJSON string
	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	currentQuery := `SELECT COALESCE(project_id, ''), updated_at, CAST(updated_at AS TEXT), COALESCE(columns_json, '') FROM shared_data_pools WHERE id = ?`
	if tx.dialect == storeDialectPostgres {
		currentQuery += ` FOR UPDATE`
	}
	if err := tx.QueryRow(currentQuery, pool.ID).Scan(&currentProjectID, &currentUpdatedAt, &currentUpdatedRaw, &currentColumnsJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if normalizeProjectID(currentProjectID) != normalizeProjectID(pool.ProjectID) || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	pool.ProjectID = normalizeProjectID(currentProjectID)
	if !setColumns {
		pool.Columns = nil
		if err := decodeDataPoolColumns(pool, currentColumnsJSON); err != nil {
			return false, err
		}
	}
	// Microsecond truncation keeps the returned token matchable on Postgres.
	pool.UpdatedAt = time.Now().Truncate(time.Microsecond)
	if !pool.UpdatedAt.After(currentUpdatedAt) {
		pool.UpdatedAt = currentUpdatedAt.Add(time.Microsecond)
	}
	var result sql.Result
	if setColumns {
		result, err = tx.Exec(`UPDATE shared_data_pools SET name = ?, description = ?, schema_json = ?, columns_json = ?, archived_at = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, pool.Name, pool.Description, string(schemaJSON), columnsJSON, pool.ArchivedAt, pool.UpdatedAt, pool.ID, currentUpdatedRaw)
	} else {
		result, err = tx.Exec(`UPDATE shared_data_pools SET name = ?, description = ?, schema_json = ?, archived_at = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, pool.Name, pool.Description, string(schemaJSON), pool.ArchivedAt, pool.UpdatedAt, pool.ID, currentUpdatedRaw)
	}
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

// UpdateDataPoolColumnsIfUnchanged replaces a pool's column order when it
// still equals expected. The order is a display hint maintained by imports, so
// it leaves updated_at, the editor's token for name and archive changes, alone.
func (s *SQLiteStore) UpdateDataPoolColumnsIfUnchanged(poolID string, expected, columns []string) (bool, error) {
	expectedJSON, err := encodeDataPoolColumns(&SharedDataPool{Columns: expected})
	if err != nil {
		return false, err
	}
	columnsJSON, err := encodeDataPoolColumns(&SharedDataPool{Columns: columns})
	if err != nil {
		return false, err
	}
	expectedText, _ := expectedJSON.(string)
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
	result, err := tx.Exec(`UPDATE shared_data_pools SET columns_json = ? WHERE id = ? AND COALESCE(columns_json, '') = ?`, columnsJSON, poolID, expectedText)
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
	var schemaJSON, columnsJSON string
	err := s.db.QueryRow(`SELECT id, name, COALESCE(description, ''), COALESCE(schema_json, ''), COALESCE(columns_json, ''), COALESCE(project_id, ''), created_at, updated_at, archived_at FROM shared_data_pools WHERE id = ?`, id).Scan(
		&pool.ID, &pool.Name, &pool.Description, &schemaJSON, &columnsJSON, &pool.ProjectID, &pool.CreatedAt, &pool.UpdatedAt, &pool.ArchivedAt)
	if err != nil {
		return nil, err
	}
	pool.ProjectID = normalizeProjectID(pool.ProjectID)
	if schemaJSON != "" && schemaJSON != "null" {
		if err := json.Unmarshal([]byte(schemaJSON), &pool.Schema); err != nil {
			return nil, fmt.Errorf("decode data pool %s schema: %w", id, err)
		}
	}
	if err := decodeDataPoolColumns(&pool, columnsJSON); err != nil {
		return nil, err
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
	query := `SELECT id, name, COALESCE(description, ''), COALESCE(schema_json, ''), COALESCE(columns_json, ''), COALESCE(project_id, ''), created_at, updated_at, archived_at FROM shared_data_pools`
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
		var schemaJSON, columnsJSON string
		if err := rows.Scan(&pool.ID, &pool.Name, &pool.Description, &schemaJSON, &columnsJSON, &pool.ProjectID, &pool.CreatedAt, &pool.UpdatedAt, &pool.ArchivedAt); err != nil {
			return nil, err
		}
		pool.ProjectID = normalizeProjectID(pool.ProjectID)
		if schemaJSON != "" && schemaJSON != "null" {
			if err := json.Unmarshal([]byte(schemaJSON), &pool.Schema); err != nil {
				return nil, fmt.Errorf("decode data pool %s schema: %w", pool.ID, err)
			}
		}
		if err := decodeDataPoolColumns(&pool, columnsJSON); err != nil {
			return nil, err
		}
		pools = append(pools, &pool)
	}
	return pools, rows.Err()
}

// MaxDataPoolColumns matches the engine's per-data-source column cap.
const MaxDataPoolColumns = 4_096

// NormalizeDataPoolColumns trims names and drops blanks and repeats, keeping
// the first occurrence. Names are case-sensitive, like row keys.
func NormalizeDataPoolColumns(columns []string) ([]string, error) {
	if len(columns) == 0 {
		return nil, nil
	}
	seen := make(map[string]struct{}, len(columns))
	normalized := make([]string, 0, len(columns))
	for _, column := range columns {
		column = strings.TrimSpace(column)
		if column == "" {
			continue
		}
		if _, dup := seen[column]; dup {
			continue
		}
		seen[column] = struct{}{}
		normalized = append(normalized, column)
	}
	if len(normalized) > MaxDataPoolColumns {
		return nil, fmt.Errorf("a data pool can have at most %d columns", MaxDataPoolColumns)
	}
	if len(normalized) == 0 {
		return nil, nil
	}
	return normalized, nil
}

func encodeDataPoolColumns(pool *SharedDataPool) (any, error) {
	columns, err := NormalizeDataPoolColumns(pool.Columns)
	if err != nil {
		return nil, err
	}
	pool.Columns = columns
	if len(columns) == 0 {
		return nil, nil
	}
	encoded, err := json.Marshal(columns)
	if err != nil {
		return nil, fmt.Errorf("marshal data pool columns: %w", err)
	}
	return string(encoded), nil
}

func decodeDataPoolColumns(pool *SharedDataPool, columnsJSON string) error {
	if columnsJSON == "" || columnsJSON == "null" {
		return nil
	}
	if err := json.Unmarshal([]byte(columnsJSON), &pool.Columns); err != nil {
		return fmt.Errorf("decode data pool %s columns: %w", pool.ID, err)
	}
	return nil
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

// DataPoolRowsMutationValidator validates the complete candidate pool snapshot
// inside the same parent-locked transaction as a direct row mutation.
type DataPoolRowsMutationValidator func(rows []*SharedDataPoolRow) error

func (s *SQLiteStore) SaveDataPoolRow(row *SharedDataPoolRow) error {
	return s.saveDataPoolRow(row, nil)
}

// SaveDataPoolRowIfValid rolls back the row write when validate rejects the
// resulting full-pool snapshot. The parent pool lock serializes this validation
// with every built-in direct and bulk row mutation.
func (s *SQLiteStore) SaveDataPoolRowIfValid(row *SharedDataPoolRow, validate DataPoolRowsMutationValidator) error {
	if validate == nil {
		return errors.New("data pool row validator is required")
	}
	return s.saveDataPoolRow(row, validate)
}

func (s *SQLiteStore) saveDataPoolRow(row *SharedDataPoolRow, validate DataPoolRowsMutationValidator) error {
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
	row.CreatedAt = row.CreatedAt.Truncate(time.Microsecond)
	row.UpdatedAt = time.Now().Truncate(time.Microsecond)
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
	isNew := errors.Is(err, sql.ErrNoRows)
	if err == nil && existingPoolID != row.PoolID {
		return fmt.Errorf("data pool row %q belongs to pool %q, not %q", row.ID, existingPoolID, row.PoolID)
	}
	if isNew {
		// A new row must sort after every existing row even when a prior bulk
		// import future-dated created_at with its base+i staggering.
		maxCreated, hasRows, maxErr := maxDataPoolRowCreatedAt(tx, row.PoolID)
		if maxErr != nil {
			return maxErr
		}
		if hasRows && !row.CreatedAt.After(maxCreated) {
			row.CreatedAt = maxCreated.Add(time.Microsecond)
			row.UpdatedAt = row.CreatedAt
		}
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
	if validate != nil {
		rows, err := listDataPoolRowsTx(tx, row.PoolID)
		if err != nil {
			return err
		}
		if err := validate(rows); err != nil {
			return fmt.Errorf("validate data pool runtime shape: %w", err)
		}
	}
	countDelta := 0
	if isNew {
		countDelta = 1
	}
	if err := advanceDataPoolRowsPageState(tx, row.PoolID, countDelta); err != nil {
		return err
	}
	return tx.Commit()
}

// UpdateDataPoolRowIfVersion performs an ownership-scoped optimistic update.
// The row version is the public concurrency token; a stale editor cannot
// silently overwrite a newer row or move it between pools.
func (s *SQLiteStore) UpdateDataPoolRowIfVersion(poolID string, row *SharedDataPoolRow, expectedVersion int) (bool, error) {
	return s.updateDataPoolRowIfVersion(poolID, row, expectedVersion, nil)
}

// UpdateDataPoolRowIfVersionAndValid combines row-version CAS with whole-pool
// runtime validation. A stale version or invalid candidate leaves both the row
// and the O(1) paging counters unchanged.
func (s *SQLiteStore) UpdateDataPoolRowIfVersionAndValid(poolID string, row *SharedDataPoolRow, expectedVersion int, validate DataPoolRowsMutationValidator) (bool, error) {
	if validate == nil {
		return false, errors.New("data pool row validator is required")
	}
	return s.updateDataPoolRowIfVersion(poolID, row, expectedVersion, validate)
}

func (s *SQLiteStore) updateDataPoolRowIfVersion(poolID string, row *SharedDataPoolRow, expectedVersion int, validate DataPoolRowsMutationValidator) (bool, error) {
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
	row.UpdatedAt = time.Now().Truncate(time.Microsecond)
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
	if validate != nil {
		rows, err := listDataPoolRowsTx(tx, poolID)
		if err != nil {
			return false, err
		}
		if err := validate(rows); err != nil {
			return false, fmt.Errorf("validate data pool runtime shape: %w", err)
		}
	}
	if err := advanceDataPoolRowsPageState(tx, poolID, 0); err != nil {
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
	query := `SELECT id, pool_id, data_json, COALESCE(status, ''), COALESCE(version, 1), created_at, updated_at FROM shared_data_pool_rows WHERE pool_id = ? ORDER BY created_at ASC, id ASC`
	args := []any{poolID}
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}
	return s.scanDataPoolRows(query, args...)
}

// ListDataPoolRowsPageAfter returns the next page in the pool's stored sort
// order, so full-pool sweeps (export) avoid O(n²) OFFSET rescans. afterKey is
// the opaque cursor returned by the previous call ("" for the first page);
// afterID is the last row's id.
//
// On SQLite the cursor is the row's raw stored created_at text: legacy rows
// were written through time.String() and carry Go monotonic-clock suffixes
// ("… m=+1.23") that a re-bound time.Time can never equal, so only stored-text
// vs stored-text comparison is consistent with the column's own (text) sort
// order. Postgres stores real microsecond timestamps, so the cursor round-trips
// through RFC3339Nano and compares natively.
func (s *SQLiteStore) ListDataPoolRowsPageAfter(poolID, afterKey, afterID string, limit int) ([]*SharedDataPoolRow, string, error) {
	if limit <= 0 {
		return []*SharedDataPoolRow{}, afterKey, nil
	}
	var cursor any = afterKey
	if s.db.dialect == storeDialectPostgres {
		parsed := time.Time{}
		if afterKey != "" {
			var err error
			parsed, err = time.Parse(time.RFC3339Nano, afterKey)
			if err != nil {
				return nil, "", fmt.Errorf("invalid data pool row cursor %q: %w", afterKey, err)
			}
		}
		cursor = parsed
	}
	rows, err := s.db.Query(`SELECT id, pool_id, data_json, COALESCE(status, ''), COALESCE(version, 1), created_at, updated_at, CAST(created_at AS TEXT)
		FROM shared_data_pool_rows
		WHERE pool_id = ? AND (created_at > ? OR (created_at = ? AND id > ?))
		ORDER BY created_at ASC, id ASC LIMIT ?`, poolID, cursor, cursor, afterID, limit)
	if err != nil {
		return nil, "", err
	}
	defer rows.Close()
	var out []*SharedDataPoolRow
	nextKey := afterKey
	for rows.Next() {
		var row SharedDataPoolRow
		var dataJSON string
		var rawCreated string
		if err := rows.Scan(&row.ID, &row.PoolID, &dataJSON, &row.Status, &row.Version, &row.CreatedAt, &row.UpdatedAt, &rawCreated); err != nil {
			return nil, "", err
		}
		if dataJSON != "" && dataJSON != "null" {
			if err := json.Unmarshal([]byte(dataJSON), &row.Data); err != nil {
				return nil, "", fmt.Errorf("decode data pool row %s: %w", row.ID, err)
			}
		}
		if row.Data == nil {
			row.Data = map[string]string{}
		}
		out = append(out, &row)
		if s.db.dialect == storeDialectPostgres {
			nextKey = row.CreatedAt.Format(time.RFC3339Nano)
		} else {
			nextKey = rawCreated
		}
	}
	return out, nextKey, rows.Err()
}

func (s *SQLiteStore) scanDataPoolRows(query string, args ...any) ([]*SharedDataPoolRow, error) {
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	return scanDataPoolRowsResult(rows)
}

func listDataPoolRowsTx(tx *storeTx, poolID string) ([]*SharedDataPoolRow, error) {
	rows, err := tx.Query(`SELECT id, pool_id, data_json, COALESCE(status, ''), COALESCE(version, 1), created_at, updated_at
		FROM shared_data_pool_rows WHERE pool_id = ? ORDER BY created_at ASC, id ASC`, poolID)
	if err != nil {
		return nil, err
	}
	return scanDataPoolRowsResult(rows)
}

func scanDataPoolRowsResult(rows *sql.Rows) ([]*SharedDataPoolRow, error) {
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

// CountDataPoolRows returns the durable O(1) row count maintained by every
// supported mutation. The scalar subquery preserves the historical missing
// pool behavior (zero rows, nil error) of COUNT(*) on the child table.
func (s *SQLiteStore) CountDataPoolRows(poolID string) (int, error) {
	var count int
	if err := s.db.QueryRow(`SELECT COALESCE((SELECT row_count FROM shared_data_pools WHERE id = ?), 0)`, poolID).Scan(&count); err != nil {
		return 0, err
	}
	return count, nil
}

// DataPoolRowsPageState returns the durable O(1) generation and count used to
// fence keyset pages. Unlike DataPoolRowsRevision, it never scans or hashes the
// pool contents on a read path.
func (s *SQLiteStore) DataPoolRowsPageState(poolID string) (string, int, error) {
	var revision int64
	var count int
	if err := s.db.QueryRow(`SELECT rows_revision, row_count FROM shared_data_pools WHERE id = ?`, poolID).Scan(&revision, &count); err != nil {
		return "", 0, err
	}
	return strconv.FormatInt(revision, 10), count, nil
}

// DataPoolRowsRevision returns a content/version fingerprint and row count from
// one database statement. Bulk replace uses it as an optimistic token so a
// concurrent same-count update or delete+add cannot be silently erased.
func (s *SQLiteStore) DataPoolRowsRevision(poolID string) (string, int, error) {
	rows, err := s.db.Query(`SELECT id, COALESCE(status, ''), COALESCE(version, 1), data_json
		FROM shared_data_pool_rows WHERE pool_id = ? ORDER BY id ASC`, poolID)
	if err != nil {
		return "", 0, err
	}
	return scanDataPoolRowsRevision(rows)
}

func advanceDataPoolRowsPageState(tx *storeTx, poolID string, countDelta int) error {
	result, err := tx.Exec(`UPDATE shared_data_pools
		SET rows_revision = rows_revision + 1, row_count = row_count + ?
		WHERE id = ? AND row_count + ? >= 0`, countDelta, poolID, countDelta)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("failed to advance data pool row paging state for %q", poolID)
	}
	return nil
}

// BulkReplaceDataPoolRows inserts rows in a single transaction, optionally
// deleting the pool's existing rows first. It exists for CSV import: looping
// SaveDataPoolRow would open a transaction and take the project+pool locks per
// row. File order is preserved by staggering created_at, which the row listing
// orders on.
func (s *SQLiteStore) BulkReplaceDataPoolRows(poolID string, rows []*SharedDataPoolRow, replace bool) error {
	_, err := s.bulkReplaceDataPoolRows(poolID, rows, replace, -1, "")
	return err
}

// BulkReplaceDataPoolRowsIfCount closes the import preparation window: the
// caller validates a snapshot, then this method verifies under the pool lock
// that no row was added or removed before applying the append/replace.
func (s *SQLiteStore) BulkReplaceDataPoolRowsIfCount(poolID string, rows []*SharedDataPoolRow, replace bool, expectedExistingRows int) (bool, error) {
	if expectedExistingRows < 0 {
		return false, errors.New("expected existing data pool row count must be non-negative")
	}
	return s.bulkReplaceDataPoolRows(poolID, rows, replace, expectedExistingRows, "")
}

// BulkReplaceDataPoolRowsIfRevision applies a full replacement only when the
// pool's row identities, versions, statuses, and data still match the snapshot
// fingerprint captured while the import was prepared.
func (s *SQLiteStore) BulkReplaceDataPoolRowsIfRevision(poolID string, rows []*SharedDataPoolRow, expectedRevision string) (bool, error) {
	if strings.TrimSpace(expectedRevision) == "" {
		return false, errors.New("expected data pool row revision is required")
	}
	return s.bulkReplaceDataPoolRows(poolID, rows, true, -1, expectedRevision)
}

// BulkAppendDataPoolRowsIfUnchanged protects the whole-pool shape check done by
// CSV append imports. A row update or a delete+add can leave COUNT(*) unchanged
// while changing columns or bytes enough to cross an engine limit, so count
// alone is not a sufficient optimistic token for append.
func (s *SQLiteStore) BulkAppendDataPoolRowsIfUnchanged(poolID string, rows, expectedExistingRows []*SharedDataPoolRow) (bool, error) {
	expectedRevision, err := dataPoolRowsRevision(expectedExistingRows)
	if err != nil {
		return false, err
	}
	return s.bulkReplaceDataPoolRows(poolID, rows, false, -1, expectedRevision)
}

func (s *SQLiteStore) bulkReplaceDataPoolRows(poolID string, rows []*SharedDataPoolRow, replace bool, expectedExistingRows int, expectedRevision string) (bool, error) {
	poolID = strings.TrimSpace(poolID)
	if poolID == "" {
		return false, errors.New("data pool id is required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if _, err := lockDataPoolParentForMutation(tx, poolID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, fmt.Errorf("data pool not found: %s", poolID)
		}
		return false, err
	}
	var activePool int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM shared_data_pools WHERE id = ? AND archived_at IS NULL`, poolID).Scan(&activePool); err != nil {
		return false, err
	}
	if activePool != 1 {
		return false, fmt.Errorf("data pool %q is archived; restore it before importing rows", poolID)
	}
	var currentRowCount int
	if err := tx.QueryRow(`SELECT row_count FROM shared_data_pools WHERE id = ?`, poolID).Scan(&currentRowCount); err != nil {
		return false, err
	}
	if expectedExistingRows >= 0 {
		if currentRowCount != expectedExistingRows {
			return false, nil
		}
	}
	if expectedRevision != "" {
		currentRevision, err := dataPoolRowsRevisionTx(tx, poolID)
		if err != nil {
			return false, err
		}
		if currentRevision != expectedRevision {
			return false, nil
		}
	}
	if replace {
		if _, err := tx.Exec(`DELETE FROM shared_data_pool_rows WHERE pool_id = ?`, poolID); err != nil {
			return false, err
		}
	}
	// Appended rows must sort after every existing row even when a prior huge
	// import future-dated created_at past the wall clock (base+i staggering).
	base := time.Now().Truncate(time.Microsecond)
	if !replace {
		maxCreated, hasRows, err := maxDataPoolRowCreatedAt(tx, poolID)
		if err != nil {
			return false, err
		}
		if hasRows && !base.After(maxCreated) {
			base = maxCreated.Add(time.Microsecond)
		}
	}
	inserted := make([]*SharedDataPoolRow, 0, len(rows))
	for i, row := range rows {
		if row == nil {
			continue
		}
		row.ID = strings.TrimSpace(row.ID)
		if row.ID == "" {
			return false, fmt.Errorf("data pool row %d id is required", i)
		}
		row.PoolID = poolID
		if row.Data == nil {
			row.Data = map[string]string{}
		}
		if row.Status == "" {
			row.Status = "active"
		}
		if row.Version <= 0 {
			row.Version = 1
		}
		row.CreatedAt = base.Add(time.Duration(len(inserted)) * time.Microsecond)
		row.UpdatedAt = row.CreatedAt
		inserted = append(inserted, row)
	}
	// Multi-row VALUES batches keep the project/pool lock hold proportional to
	// batches, not rows. 100 rows × 7 params stays under every driver's
	// placeholder ceiling.
	const bulkInsertBatchRows = 100
	for start := 0; start < len(inserted); start += bulkInsertBatchRows {
		end := start + bulkInsertBatchRows
		if end > len(inserted) {
			end = len(inserted)
		}
		var query strings.Builder
		query.WriteString(`INSERT INTO shared_data_pool_rows (id, pool_id, data_json, status, version, created_at, updated_at) VALUES `)
		args := make([]any, 0, (end-start)*7)
		for i := start; i < end; i++ {
			if i > start {
				query.WriteString(", ")
			}
			query.WriteString("(?, ?, ?, ?, ?, ?, ?)")
			row := inserted[i]
			dataJSON, err := json.Marshal(row.Data)
			if err != nil {
				return false, fmt.Errorf("marshal data pool row %d: %w", i, err)
			}
			args = append(args, row.ID, row.PoolID, string(dataJSON), row.Status, row.Version, row.CreatedAt, row.UpdatedAt)
		}
		if _, err := tx.Exec(query.String(), args...); err != nil {
			return false, err
		}
	}
	countDelta := len(inserted)
	if replace {
		countDelta -= currentRowCount
	}
	if len(inserted) > 0 || (replace && currentRowCount > 0) {
		if err := advanceDataPoolRowsPageState(tx, poolID, countDelta); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

type dataPoolRowRevisionValue struct {
	ID      string            `json:"id"`
	Status  string            `json:"status"`
	Version int               `json:"version"`
	Data    map[string]string `json:"data"`
}

func dataPoolRowsRevision(rows []*SharedDataPoolRow) (string, error) {
	ordered := append([]*SharedDataPoolRow(nil), rows...)
	sort.Slice(ordered, func(left, right int) bool {
		if ordered[left] == nil {
			return ordered[right] != nil
		}
		if ordered[right] == nil {
			return false
		}
		return ordered[left].ID < ordered[right].ID
	})
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	for index, row := range ordered {
		if row == nil {
			return "", fmt.Errorf("expected data pool row %d is nil", index)
		}
		data := row.Data
		if data == nil {
			data = map[string]string{}
		}
		if err := encoder.Encode(dataPoolRowRevisionValue{
			ID: row.ID, Status: row.Status, Version: row.Version, Data: data,
		}); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), nil
}

func dataPoolRowsRevisionTx(tx *storeTx, poolID string) (string, error) {
	rows, err := tx.Query(`SELECT id, COALESCE(status, ''), COALESCE(version, 1), data_json
		FROM shared_data_pool_rows WHERE pool_id = ? ORDER BY id ASC`, poolID)
	if err != nil {
		return "", err
	}
	revision, _, err := scanDataPoolRowsRevision(rows)
	return revision, err
}

func scanDataPoolRowsRevision(rows *sql.Rows) (string, int, error) {
	defer rows.Close()
	hash := sha256.New()
	encoder := json.NewEncoder(hash)
	count := 0
	for rows.Next() {
		var value dataPoolRowRevisionValue
		var dataJSON string
		if err := rows.Scan(&value.ID, &value.Status, &value.Version, &dataJSON); err != nil {
			return "", 0, err
		}
		if dataJSON != "" && dataJSON != "null" {
			if err := json.Unmarshal([]byte(dataJSON), &value.Data); err != nil {
				return "", 0, fmt.Errorf("decode data pool row %s: %w", value.ID, err)
			}
		}
		if value.Data == nil {
			value.Data = map[string]string{}
		}
		if err := encoder.Encode(value); err != nil {
			return "", 0, err
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return "", 0, err
	}
	return fmt.Sprintf("%x", hash.Sum(nil)), count, nil
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
	result, err := tx.Exec(`DELETE FROM shared_data_pool_rows WHERE id = ? AND pool_id = ?`, id, poolID)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		if err := advanceDataPoolRowsPageState(tx, poolID, -1); err != nil {
			return err
		}
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
	if count > 0 {
		if err := advanceDataPoolRowsPageState(tx, poolID, -int(count)); err != nil {
			return false, err
		}
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return count > 0, nil
}

// maxDataPoolRowCreatedAt returns the latest created_at in the pool in the
// column's own sort order, or hasRows=false for an empty pool. Callers hold the
// pool lock, so the value cannot move under them.
func maxDataPoolRowCreatedAt(tx *storeTx, poolID string) (time.Time, bool, error) {
	var maxCreated time.Time
	err := tx.QueryRow(`SELECT created_at FROM shared_data_pool_rows WHERE pool_id = ? ORDER BY created_at DESC LIMIT 1`, poolID).Scan(&maxCreated)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	return maxCreated, true, nil
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
