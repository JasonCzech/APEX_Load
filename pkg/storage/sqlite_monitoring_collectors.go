package storage

// SQLite store — monitoring collectors. Same SQL serves PostgreSQL via rewriteSQL.

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *SQLiteStore) SaveMonitoringCollector(collector *MonitoringCollector) error {
	if collector == nil {
		return fmt.Errorf("monitoring collector is required")
	}
	if strings.TrimSpace(collector.ID) == "" {
		return errors.New("monitoring collector id is required")
	}
	if strings.TrimSpace(collector.Name) == "" {
		return errors.New("monitoring collector name is required")
	}
	collector.ProjectID = normalizeProjectID(collector.ProjectID)
	if collector.Health == "" {
		collector.Health = "unreachable"
	}
	now := time.Now()
	if collector.CreatedAt.IsZero() {
		collector.CreatedAt = now
	}
	collector.UpdatedAt = now
	var packsJSON string
	if len(collector.AllowedMetricPacks) > 0 {
		encoded, err := json.Marshal(collector.AllowedMetricPacks)
		if err != nil {
			return fmt.Errorf("marshal allowed_metric_packs: %w", err)
		}
		packsJSON = string(encoded)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if collector.ProjectID, err = lockProjectForOwnedMutation(tx, collector.ProjectID); err != nil {
		return err
	}
	var currentProjectID string
	err = tx.QueryRow(`SELECT project_id FROM monitoring_collectors WHERE id = ?`, collector.ID).Scan(&currentProjectID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && normalizeProjectID(currentProjectID) != collector.ProjectID {
		return fmt.Errorf("monitoring collector %q belongs to another project", collector.ID)
	}
	result, err := tx.Exec(`INSERT INTO monitoring_collectors (
			id, project_id, name, description, token_hash, health, last_seen_at, allowed_metric_packs_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name=excluded.name, description=excluded.description,
			token_hash=excluded.token_hash, health=excluded.health, last_seen_at=excluded.last_seen_at,
			allowed_metric_packs_json=excluded.allowed_metric_packs_json, updated_at=excluded.updated_at
		WHERE monitoring_collectors.project_id = excluded.project_id`,
		collector.ID, collector.ProjectID, collector.Name, collector.Description, collector.TokenHash,
		collector.Health, collector.LastSeenAt, packsJSON, collector.CreatedAt, collector.UpdatedAt)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("monitoring collector %q belongs to another project", collector.ID)
	}
	return tx.Commit()
}

const monitoringCollectorSelect = `SELECT id, COALESCE(project_id, ''), name, COALESCE(description, ''),
	COALESCE(token_hash, ''), COALESCE(health, 'unreachable'), last_seen_at, COALESCE(allowed_metric_packs_json, ''),
	created_at, updated_at FROM monitoring_collectors`

func scanMonitoringCollector(row capacityRowScanner) (*MonitoringCollector, error) {
	var c MonitoringCollector
	var packsJSON string
	if err := row.Scan(&c.ID, &c.ProjectID, &c.Name, &c.Description, &c.TokenHash, &c.Health,
		&c.LastSeenAt, &packsJSON, &c.CreatedAt, &c.UpdatedAt); err != nil {
		return nil, err
	}
	if packsJSON != "" && packsJSON != "null" {
		if err := json.Unmarshal([]byte(packsJSON), &c.AllowedMetricPacks); err != nil {
			return nil, fmt.Errorf("unmarshal allowed_metric_packs: %w", err)
		}
	}
	if c.AllowedMetricPacks == nil {
		// Marshal an empty catalog as [] rather than null for API consistency.
		c.AllowedMetricPacks = []string{}
	}
	return &c, nil
}

func (s *SQLiteStore) GetMonitoringCollector(id string) (*MonitoringCollector, error) {
	return scanMonitoringCollector(s.db.QueryRow(monitoringCollectorSelect+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListMonitoringCollectors(projectID string, limit int, offset int) ([]*MonitoringCollector, error) {
	query := monitoringCollectorSelect
	args := []any{}
	if projectID != "" {
		query += ` WHERE project_id = ?`
		args = append(args, normalizeProjectID(projectID))
	}
	query += ` ORDER BY created_at DESC`
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
		if offset > 0 {
			query += ` OFFSET ?`
			args = append(args, offset)
		}
	}
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	collectors := make([]*MonitoringCollector, 0)
	for rows.Next() {
		c, err := scanMonitoringCollector(rows)
		if err != nil {
			return nil, err
		}
		collectors = append(collectors, c)
	}
	return collectors, rows.Err()
}

func (s *SQLiteStore) DeleteMonitoringCollector(id string) error {
	tx, e := s.db.Begin()
	if e != nil {
		return e
	}
	defer tx.Rollback()
	var project string
	if e = tx.QueryRow(`SELECT project_id FROM monitoring_collectors WHERE id=?`, id).Scan(&project); errors.Is(e, sql.ErrNoRows) {
		return nil
	} else if e != nil {
		return e
	}
	if _, e = lockProjectForOwnedMutation(tx, project); e != nil {
		return e
	}
	if _, e = tx.Exec(`DELETE FROM monitoring_collectors WHERE id=? AND project_id=?`, id, project); e != nil {
		return e
	}
	return tx.Commit()
}

func (s *SQLiteStore) UpdateMonitoringCollectorHealth(id string, health string, lastSeenAt time.Time) error {
	if health == "" {
		health = "healthy"
	}
	res, err := s.db.Exec(`UPDATE monitoring_collectors SET health = ?, last_seen_at = ?, updated_at = ? WHERE id = ?`,
		health, lastSeenAt, time.Now(), id)
	if err != nil {
		return err
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return fmt.Errorf("monitoring collector not found: %s", id)
	}
	return nil
}

// PatchMonitoringCollector updates only editor-supplied fields. In particular,
// it never rewrites token_hash or last_seen_at from a stale read, which could
// otherwise undo a concurrent token rotation or heartbeat.
func (s *SQLiteStore) PatchMonitoringCollector(id, projectID string, name, description, health *string, allowedMetricPacks *[]string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var owner string
	if err = tx.QueryRow(`SELECT project_id FROM monitoring_collectors WHERE id=?`, id).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	if owner != normalizeProjectID(projectID) {
		return false, nil
	}
	if _, err = lockProjectForOwnedMutation(tx, projectID); err != nil {
		return false, err
	}

	sets := make([]string, 0, 5)
	args := make([]any, 0, 7)
	if name != nil {
		sets = append(sets, "name = ?")
		args = append(args, *name)
	}
	if description != nil {
		sets = append(sets, "description = ?")
		args = append(args, *description)
	}
	if health != nil {
		sets = append(sets, "health = ?")
		args = append(args, *health)
	}
	if allowedMetricPacks != nil {
		packsJSON := ""
		if len(*allowedMetricPacks) > 0 {
			encoded, err := json.Marshal(*allowedMetricPacks)
			if err != nil {
				return false, fmt.Errorf("marshal allowed_metric_packs: %w", err)
			}
			packsJSON = string(encoded)
		}
		sets = append(sets, "allowed_metric_packs_json = ?")
		args = append(args, packsJSON)
	}
	if len(sets) == 0 {
		var exists int
		err := tx.QueryRow(`SELECT 1 FROM monitoring_collectors WHERE id = ? AND project_id = ?`, id, normalizeProjectID(projectID)).Scan(&exists)
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		if err != nil {
			return false, err
		}
		return true, tx.Commit()
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, time.Now(), id, normalizeProjectID(projectID))
	result, err := tx.Exec(`UPDATE monitoring_collectors SET `+strings.Join(sets, ", ")+` WHERE id = ? AND project_id = ?`, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected > 0, tx.Commit()
}

// UpdateMonitoringCollectorTokenHashIfUnchanged rotates only the credential
// column and is ownership-scoped. The old hash is the optimistic version: two
// concurrent rotations that both read the same credential cannot both return a
// plaintext token when only one new hash can remain active.
func (s *SQLiteStore) UpdateMonitoringCollectorTokenHashIfUnchanged(id, projectID, tokenHash, expectedTokenHash string) (bool, error) {
	result, err := s.db.Exec(`UPDATE monitoring_collectors SET token_hash = ?, updated_at = ?
		WHERE id = ? AND project_id = ? AND COALESCE(token_hash, '') = ?`,
		tokenHash, time.Now(), id, normalizeProjectID(projectID), expectedTokenHash)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected > 0, err
}

func (s *SQLiteStore) DeleteMonitoringCollectorFromProject(id, projectID string) (bool, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return false, e
	}
	defer tx.Rollback()
	var owner string
	if e = tx.QueryRow(`SELECT project_id FROM monitoring_collectors WHERE id=?`, id).Scan(&owner); errors.Is(e, sql.ErrNoRows) {
		return false, nil
	} else if e != nil {
		return false, e
	}
	if owner != normalizeProjectID(projectID) {
		return false, nil
	}
	if _, e = lockProjectForOwnedMutation(tx, projectID); e != nil {
		return false, e
	}
	r, e := tx.Exec(`DELETE FROM monitoring_collectors WHERE id=? AND project_id=?`, id, normalizeProjectID(projectID))
	if e != nil {
		return false, e
	}
	n, e := r.RowsAffected()
	if e != nil {
		return false, e
	}
	return n > 0, tx.Commit()
}
