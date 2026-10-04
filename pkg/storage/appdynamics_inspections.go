package storage

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"
)

// AppDynamicsInspection is an expiring administrative operation, independent of
// load-test identity. PayloadJSON is private configuration; ResultJSON contains
// only the bounded provider result projection.
type AppDynamicsInspection struct {
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

type AppDynamicsInspectionStore interface {
	EnsureAppDynamicsInspectionSchema() error
	AppDynamicsCollectorPublicKey(string, string, string, time.Time) (string, error)
	CreateAppDynamicsInspection(*AppDynamicsInspection) error
	GetAppDynamicsInspection(string) (*AppDynamicsInspection, error)
	ListAppDynamicsInspections(string, string, int) ([]*AppDynamicsInspection, error)
	ClaimAppDynamicsInspection(string, string, time.Time, time.Time) (bool, error)
	CompleteAppDynamicsInspection(string, string, string, string, time.Time) (bool, error)
}

func (s *SQLiteStore) EnsureAppDynamicsInspectionSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS appdynamics_inspections (id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,connection_id TEXT NOT NULL,connection_revision BIGINT NOT NULL,collector_id TEXT NOT NULL DEFAULT '',provider TEXT NOT NULL DEFAULT 'appdynamics',kind TEXT NOT NULL,state TEXT NOT NULL,payload_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',owner_token TEXT NOT NULL DEFAULT '',lease_expires_at DATETIME,created_at DATETIME NOT NULL,expires_at DATETIME NOT NULL)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_appdynamics_inspections_poll ON appdynamics_inspections(collector_id,state,expires_at)`)
	return err
}
func (s *SQLiteStore) createProviderInspection(op *AppDynamicsInspection, provider string) error {
	if op == nil || strings.TrimSpace(op.ID) == "" || op.ProjectID == "" || op.ConnectionID == "" || op.ConnectionRevision < 1 || len(op.PayloadJSON) > 96<<10 || !op.ExpiresAt.After(op.CreatedAt) || op.ExpiresAt.Sub(op.CreatedAt) > time.Minute || (op.Kind != "test" && op.Kind != "discover" && (provider != "prometheus" || op.Kind != "preview")) || (provider != "appdynamics" && provider != "prometheus") {
		return errors.New("invalid AppDynamics inspection")
	}
	if err := s.EnsureAppDynamicsInspectionSchema(); err != nil {
		return err
	}
	op.State = "pending"
	op.Provider = provider
	_, err := s.db.Exec(`INSERT INTO appdynamics_inspections(id,project_id,connection_id,connection_revision,collector_id,provider,kind,state,payload_json,created_at,expires_at) VALUES(?,?,?,?,?,?,?,'pending',?,?,?)`, op.ID, op.ProjectID, op.ConnectionID, op.ConnectionRevision, op.CollectorID, provider, op.Kind, op.PayloadJSON, op.CreatedAt.UTC(), op.ExpiresAt.UTC())
	return err
}

const appDynamicsInspectionColumns = `id,project_id,connection_id,connection_revision,collector_id,provider,kind,state,payload_json,result_json,error,owner_token,lease_expires_at,created_at,expires_at`

func scanAppDynamicsInspection(row interface{ Scan(...any) error }) (*AppDynamicsInspection, error) {
	var op AppDynamicsInspection
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
func (s *SQLiteStore) getProviderInspection(id, provider string) (*AppDynamicsInspection, error) {
	if err := s.EnsureAppDynamicsInspectionSchema(); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE appdynamics_inspections SET state='error',error='Inspection deadline expired',owner_token='',lease_expires_at=NULL WHERE id=? AND provider=? AND state IN ('pending','running') AND expires_at<=?`, id, provider, time.Now().UTC()); err != nil {
		return nil, err
	}
	return scanAppDynamicsInspection(s.db.QueryRow(`SELECT `+appDynamicsInspectionColumns+` FROM appdynamics_inspections WHERE id=? AND provider=?`, id, provider))
}

// List includes unexpired pending work and claims which may be recovered after
// expiry. Empty collector selects direct-controller work, never all collectors.
func (s *SQLiteStore) listProviderInspections(project, collector, provider string, limit int) ([]*AppDynamicsInspection, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid inspection list limit")
	}
	if err := s.EnsureAppDynamicsInspectionSchema(); err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`UPDATE appdynamics_inspections SET state='error',error='Inspection deadline expired',owner_token='',lease_expires_at=NULL WHERE provider=? AND collector_id=? AND state IN ('pending','running') AND expires_at<=?`, provider, collector, time.Now().UTC()); err != nil {
		return nil, err
	}
	query := `SELECT ` + appDynamicsInspectionColumns + ` FROM appdynamics_inspections WHERE collector_id=? AND provider=? AND state IN ('pending','running') AND expires_at>?`
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
	out := []*AppDynamicsInspection{}
	for rows.Next() {
		op, err := scanAppDynamicsInspection(rows)
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

// AppDynamicsCollectorPublicKey returns the enrolled destination-bound key,
// never a key selected by an inspection caller.
func (s *SQLiteStore) AppDynamicsCollectorPublicKey(project, collector, endpoint string, now time.Time) (string, error) {
	var health, packsJSON, readinessPacksJSON, destinationsJSON string
	var seen, ready time.Time
	err := s.db.QueryRow(`SELECT c.health,c.last_seen_at,c.allowed_metric_packs_json,r.packs,r.destinations,r.seen_at FROM monitoring_collectors c JOIN run_monitor_collector_readiness r ON r.collector_id=c.id AND r.project_id=c.project_id WHERE c.id=? AND c.project_id=?`, collector, project).Scan(&health, &seen, &packsJSON, &readinessPacksJSON, &destinationsJSON, &ready)
	if err != nil {
		return "", err
	}
	if health != "healthy" || seen.Before(now.Add(-90*time.Second)) || seen.After(now.Add(30*time.Second)) || ready.Before(now.Add(-90*time.Second)) || ready.After(now.Add(30*time.Second)) {
		return "", errors.New("collector enrollment is not ready")
	}
	var packs, readinessPacks, destinations []string
	if json.Unmarshal([]byte(packsJSON), &packs) != nil || json.Unmarshal([]byte(readinessPacksJSON), &readinessPacks) != nil || json.Unmarshal([]byte(destinationsJSON), &destinations) != nil || !slices.Contains(packs, "appdynamics") || !slices.Contains(readinessPacks, "appdynamics") || !slices.Contains(readinessPacks, "appdynamics.v1") {
		return "", errors.New("collector AppDynamics pack unavailable")
	}
	for _, destination := range destinations {
		if strings.HasPrefix(destination, endpoint+"#") {
			key := strings.TrimPrefix(destination, endpoint+"#")
			decoded, err := base64.StdEncoding.DecodeString(key)
			if err == nil && len(decoded) == 32 {
				return key, nil
			}
		}
	}
	return "", errors.New("collector destination enrollment unavailable")
}

// AppDynamics methods preserve the legacy API while sharing durable operation CAS.
func (s *SQLiteStore) CreateAppDynamicsInspection(op *AppDynamicsInspection) error {
	return s.createProviderInspection(op, "appdynamics")
}
func (s *SQLiteStore) GetAppDynamicsInspection(id string) (*AppDynamicsInspection, error) {
	return s.getProviderInspection(id, "appdynamics")
}
func (s *SQLiteStore) ListAppDynamicsInspections(project, collector string, limit int) ([]*AppDynamicsInspection, error) {
	return s.listProviderInspections(project, collector, "appdynamics", limit)
}
func (s *SQLiteStore) ClaimAppDynamicsInspection(id, owner string, now, until time.Time) (bool, error) {
	return s.claimProviderInspection(id, owner, "appdynamics", now, until)
}
func (s *SQLiteStore) CompleteAppDynamicsInspection(id, owner, result, message string, now time.Time) (bool, error) {
	return s.completeProviderInspection(id, owner, result, message, "appdynamics", now)
}
