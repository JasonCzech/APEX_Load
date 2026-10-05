package storage

import (
	"database/sql"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/prometheus"
)

// EnsurePrometheusSchema uses portable SQL and is idempotent for existing
// SQLite and PostgreSQL deployments. It is called by connection initialization.
func (s *SQLiteStore) EnsurePrometheusSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS prometheus_connections (
	 id TEXT PRIMARY KEY, project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,
	 name TEXT NOT NULL, revision BIGINT NOT NULL, verified_revision BIGINT NOT NULL DEFAULT 0,
	 payload_json TEXT NOT NULL, created_at TIMESTAMP NOT NULL, updated_at TIMESTAMP NOT NULL)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_prometheus_connections_project ON prometheus_connections(project_id,name,id)`)
	return err
}
func (s *SQLiteStore) CreatePrometheusConnection(c *PrometheusConnection) error {
	if err := c.Validate(); err != nil {
		return err
	}
	if err := s.EnsurePrometheusSchema(); err != nil {
		return err
	}
	c.Revision = 1
	c.VerifiedRevision = 0
	c.CreatedAt = time.Now().UTC()
	c.UpdatedAt = c.CreatedAt
	data, err := MarshalPrometheusConnectionPrivate(c)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT INTO prometheus_connections(id,project_id,name,revision,verified_revision,payload_json,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?)`, c.ID, c.ProjectID, c.Name, c.Revision, c.VerifiedRevision, string(data), c.CreatedAt, c.UpdatedAt)
	return err
}
func scanPrometheusConnection(row interface{ Scan(...any) error }) (*PrometheusConnection, error) {
	var data string
	var rev, verified int64
	if err := row.Scan(&data, &rev, &verified); err != nil {
		return nil, err
	}
	c, err := UnmarshalPrometheusConnectionPrivate([]byte(data))
	if err == nil {
		c.Revision = rev
		c.VerifiedRevision = verified
	}
	return c, err
}
func (s *SQLiteStore) GetPrometheusConnection(id string) (*PrometheusConnection, error) {
	if err := s.EnsurePrometheusSchema(); err != nil {
		return nil, err
	}
	return scanPrometheusConnection(s.db.QueryRow(`SELECT payload_json,revision,verified_revision FROM prometheus_connections WHERE id=?`, strings.TrimSpace(id)))
}
func (s *SQLiteStore) ListPrometheusConnections(projectID string) ([]*PrometheusConnection, error) {
	if err := s.EnsurePrometheusSchema(); err != nil {
		return nil, err
	}
	rows, err := s.db.Query(`SELECT payload_json,revision,verified_revision FROM prometheus_connections WHERE project_id=? ORDER BY name,id`, projectID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []*PrometheusConnection{}
	for rows.Next() {
		c, err := scanPrometheusConnection(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, c)
	}
	return result, rows.Err()
}
func (s *SQLiteStore) UpdatePrometheusConnectionIfRevision(c *PrometheusConnection, expected int64) (bool, error) {
	if err := c.Validate(); err != nil {
		return false, err
	}
	if expected <= 0 {
		return false, nil
	}
	previous, err := s.GetPrometheusConnection(c.ID)
	if err == sql.ErrNoRows {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if previous.ProjectID != c.ProjectID || previous.Revision != expected {
		return false, nil
	}
	candidate := *c
	candidate.Revision = expected + 1
	candidate.VerifiedRevision = 0
	candidate.Capabilities = prometheus.Capabilities{}
	candidate.LastCheckStatus = "unverified"
	candidate.LastCheckedAt = nil
	candidate.LastCheckMessage = ""
	if SamePrometheusIdentity(previous, &candidate) {
		candidate.Capabilities = previous.Capabilities
		candidate.LastCheckStatus = previous.LastCheckStatus
		candidate.LastCheckedAt = previous.LastCheckedAt
		candidate.LastCheckMessage = previous.LastCheckMessage
		if previous.VerifiedRevision == expected && previous.LastCheckStatus == "connected" {
			candidate.VerifiedRevision = candidate.Revision
		}
	}
	candidate.UpdatedAt = time.Now().UTC()
	data, err := MarshalPrometheusConnectionPrivate(&candidate)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`UPDATE prometheus_connections SET name=?,revision=?,verified_revision=?,payload_json=?,updated_at=? WHERE id=? AND project_id=? AND revision=?`, candidate.Name, candidate.Revision, candidate.VerifiedRevision, string(data), candidate.UpdatedAt, candidate.ID, candidate.ProjectID, expected)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if n == 1 {
		*c = candidate
	}
	return n == 1, err
}
func (s *SQLiteStore) UpdatePrometheusConnectionHealth(id, projectID string, expected int64, status, message string, caps prometheus.Capabilities, checkedAt time.Time) (bool, error) {
	c, err := s.GetPrometheusConnection(id)
	if err != nil {
		return false, err
	}
	if c.ProjectID != projectID || c.Revision != expected {
		return false, nil
	}
	c.LastCheckedAt = &checkedAt
	c.LastCheckStatus = status
	c.LastCheckMessage = message
	if len(message) > 1024 {
		c.LastCheckMessage = message[:1024]
	}
	c.Capabilities = caps
	c.VerifiedRevision = 0
	if status == "connected" {
		c.VerifiedRevision = expected
	}
	data, err := MarshalPrometheusConnectionPrivate(c)
	if err != nil {
		return false, err
	}
	result, err := s.db.Exec(`UPDATE prometheus_connections SET verified_revision=?,payload_json=? WHERE id=? AND project_id=? AND revision=?`, c.VerifiedRevision, string(data), id, projectID, expected)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (s *SQLiteStore) DeletePrometheusConnection(id, projectID string) (bool, error) {
	if err := s.EnsurePrometheusSchema(); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`DELETE FROM prometheus_connections WHERE id=? AND project_id=?`, id, projectID)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (s *SQLiteStore) DeletePrometheusConnectionIfRevision(id, projectID string, expected int64) (bool, error) {
	if expected <= 0 {
		return false, nil
	}
	if err := s.EnsurePrometheusSchema(); err != nil {
		return false, err
	}
	result, err := s.db.Exec(`DELETE FROM prometheus_connections WHERE id=? AND project_id=? AND revision=?`, id, projectID, expected)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
