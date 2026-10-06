package storage

import (
	"database/sql"

	"errors"

	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/monitorprovider"
)

// MonitorInspection is an expiring administrative operation, independent of
// load-test identity. PayloadJSON is private configuration; ResultJSON contains
// only the bounded provider result projection.
type MonitorInspection struct {
	Provider           string     `json:"provider,omitempty"`
	ID                 string     `json:"id"`
	ProjectID          string     `json:"project_id"`
	ConnectionID       string     `json:"connection_id"`
	ConnectionRevision int64      `json:"connection_revision"`
	CollectorID        string     `json:"collector_id,omitempty"`
	Kind               string     `json:"kind"`
	State              string     `json:"status"`
	PayloadJSON        string     `json:"-"`
	ResultJSON         string     `json:"-"`
	Error              string     `json:"message,omitempty"`
	OwnerToken         string     `json:"-"`
	LeaseExpiresAt     *time.Time `json:"-"`
	CreatedAt          time.Time  `json:"created_at"`
	ExpiresAt          time.Time  `json:"expires_at"`
}

func (s *SQLiteStore) EnsureMonitorInspectionSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS appdynamics_inspections (id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,connection_id TEXT NOT NULL,connection_revision BIGINT NOT NULL,collector_id TEXT NOT NULL DEFAULT '',provider TEXT NOT NULL DEFAULT 'appdynamics',kind TEXT NOT NULL,state TEXT NOT NULL,payload_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',owner_token TEXT NOT NULL DEFAULT '',lease_expires_at DATETIME,created_at DATETIME NOT NULL,expires_at DATETIME NOT NULL)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_appdynamics_inspections_poll ON appdynamics_inspections(collector_id,state,expires_at)`)
	return err
}
func (s *SQLiteStore) createProviderInspection(op *MonitorInspection, provider string) error {
	if op == nil || strings.TrimSpace(op.ID) == "" || op.ProjectID == "" || op.ConnectionID == "" || op.ConnectionRevision < 1 || len(op.PayloadJSON) > 96<<10 || !op.ExpiresAt.After(op.CreatedAt) || op.ExpiresAt.Sub(op.CreatedAt) > time.Minute || (op.Kind != "test" && op.Kind != "discover" && (provider != "prometheus" || op.Kind != "preview")) || provider != monitorprovider.ProviderPrometheus {
		return errors.New("invalid provider inspection")
	}
	if err := s.EnsureMonitorInspectionSchema(); err != nil {
		return err
	}
	op.State = "pending"
	op.Provider = provider
	_, err := s.db.Exec(`INSERT INTO appdynamics_inspections(id,project_id,connection_id,connection_revision,collector_id,provider,kind,state,payload_json,created_at,expires_at) VALUES(?,?,?,?,?,?,?,'pending',?,?,?)`, op.ID, op.ProjectID, op.ConnectionID, op.ConnectionRevision, op.CollectorID, provider, op.Kind, op.PayloadJSON, op.CreatedAt.UTC(), op.ExpiresAt.UTC())
	return err
}

const monitorInspectionColumns = `id,project_id,connection_id,connection_revision,collector_id,provider,kind,state,payload_json,result_json,error,owner_token,lease_expires_at,created_at,expires_at`

func scanMonitorInspection(row interface{ Scan(...any) error }) (*MonitorInspection, error) {
	var op MonitorInspection
	var lease sql.NullTime
	err := row.Scan(&op.ID, &op.ProjectID, &op.ConnectionID, &op.ConnectionRevision, &op.CollectorID, &op.Provider, &op.Kind, &op.State, &op.PayloadJSON, &op.ResultJSON, &op.Error, &op.OwnerToken, &lease, &op.CreatedAt, &op.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if lease.Valid {
		at := lease.Time.UTC()
		op.LeaseExpiresAt = &at
	}
	return &op, nil
}
func (s *SQLiteStore) getProviderInspection(id, provider string) (*MonitorInspection, error) {
	if err := s.EnsureMonitorInspectionSchema(); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE appdynamics_inspections SET state='error',error='Inspection deadline expired',owner_token='',lease_expires_at=NULL WHERE id=? AND provider=? AND state IN ('pending','running') AND expires_at<=?`, id, provider, time.Now().UTC()); err != nil {
		return nil, err
	}
	return scanMonitorInspection(s.db.QueryRow(`SELECT `+monitorInspectionColumns+` FROM appdynamics_inspections WHERE id=? AND provider=?`, id, provider))
}

// List includes unexpired pending work and claims which may be recovered after
// expiry. Empty collector selects direct-controller work, never all collectors.
func (s *SQLiteStore) listProviderInspections(project, collector, provider string, limit int) ([]*MonitorInspection, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid inspection list limit")
	}
	if err := s.EnsureMonitorInspectionSchema(); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE appdynamics_inspections SET state='error',error='Inspection deadline expired',owner_token='',lease_expires_at=NULL WHERE provider=? AND collector_id=? AND state IN ('pending','running') AND expires_at<=?`, provider, collector, time.Now().UTC()); err != nil {
		return nil, err
	}
	query := `SELECT ` + monitorInspectionColumns + ` FROM appdynamics_inspections WHERE collector_id=? AND provider=? AND state IN ('pending','running') AND expires_at>?`
	args := []any{collector, provider, time.Now().UTC()}
	if project != "" {
		query += ` AND project_id=?`
		args = append(args, project)
	}
	query += ` ORDER BY created_at,id LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []*MonitorInspection{}
	for rows.Next() {
		op, err := scanMonitorInspection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) claimProviderInspection(id, owner, provider string, now, until time.Time) (bool, error) {
	if owner == "" || !until.After(now) || until.Sub(now) > 2*time.Minute {
		return false, errors.New("invalid inspection claim")
	}
	result, err := s.db.Exec(`UPDATE appdynamics_inspections SET state='running',owner_token=?,lease_expires_at=? WHERE id=? AND provider=? AND expires_at>? AND (state='pending' OR (state='running' AND lease_expires_at<=?))`, owner, until.UTC(), id, provider, now.UTC(), now.UTC())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (s *SQLiteStore) completeProviderInspection(id, owner, resultJSON, message, provider string, now time.Time) (bool, error) {
	if len(resultJSON) > 1<<20 || len(message) > 1024 || owner == "" {
		return false, errors.New("invalid inspection completion")
	}
	state := "complete"
	if message != "" {
		state = "error"
	}
	result, err := s.db.Exec(`UPDATE appdynamics_inspections SET state=?,result_json=?,error=?,lease_expires_at=NULL WHERE id=? AND provider=? AND state='running' AND owner_token=? AND expires_at>? AND lease_expires_at>?`, state, resultJSON, message, id, provider, owner, now.UTC(), now.UTC())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
