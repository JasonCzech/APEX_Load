package storage

// SQLite store — monitoring collectors. Same SQL serves PostgreSQL via rewriteSQL.

import (
	"encoding/json"
	"fmt"
	"time"
)

func (s *SQLiteStore) SaveMonitoringCollector(collector *MonitoringCollector) error {
	if collector == nil {
		return fmt.Errorf("monitoring collector is required")
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
	_, err := s.db.Exec(`INSERT INTO monitoring_collectors (
			id, project_id, name, description, token_hash, health, last_seen_at, allowed_metric_packs_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			project_id=excluded.project_id, name=excluded.name, description=excluded.description,
			token_hash=excluded.token_hash, health=excluded.health, last_seen_at=excluded.last_seen_at,
			allowed_metric_packs_json=excluded.allowed_metric_packs_json, updated_at=excluded.updated_at`,
		collector.ID, collector.ProjectID, collector.Name, collector.Description, collector.TokenHash,
		collector.Health, collector.LastSeenAt, packsJSON, collector.CreatedAt, collector.UpdatedAt)
	return err
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
	_, err := s.db.Exec(`DELETE FROM monitoring_collectors WHERE id = ?`, id)
	return err
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
	if affected, err := res.RowsAffected(); err == nil && affected == 0 {
		return fmt.Errorf("monitoring collector not found: %s", id)
	}
	return nil
}
