package storage

import (
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/analysis"
)

// ElasticInspection is an expiring administrative operation, independent of
// load-test identity. PayloadJSON is private configuration; ResultJSON contains
// only the bounded provider result projection.
type ElasticInspection struct {
	CleanupContinuations []*analysis.ProviderContinuation `json:"-"`
	ID                   string                           `json:"id"`
	ProjectID            string                           `json:"project_id"`
	ConnectionID         string                           `json:"connection_id"`
	ConnectionRevision   int64                            `json:"connection_revision"`
	CollectorID          string                           `json:"collector_id,omitempty"`
	Kind                 string                           `json:"kind"`
	State                string                           `json:"status"`
	PayloadJSON          string                           `json:"-"`
	ResultJSON           string                           `json:"-"`
	Error                string                           `json:"message,omitempty"`
	OwnerToken           string                           `json:"-"`
	LeaseExpiresAt       *time.Time                       `json:"-"`
	CreatedAt            time.Time                        `json:"created_at"`
	ExpiresAt            time.Time                        `json:"expires_at"`
}

type ElasticInspectionStore interface {
	EnsureElasticInspectionSchema() error
	ElasticCollectorPublicKey(string, string, string, time.Time) (string, error)
	CreateElasticInspection(*ElasticInspection) error
	GetElasticInspection(string) (*ElasticInspection, error)
	ListElasticInspections(string, string, int) ([]*ElasticInspection, error)
	ClaimElasticInspection(string, string, time.Time, time.Time) (bool, error)
	CompleteElasticInspection(string, string, string, string, time.Time) (bool, error)
	CancelElasticInspection(string, string, string, time.Time) (bool, error)
	CancelElasticInspectionIfOwner(string, string, string, string, time.Time) (bool, error)
	RetainElasticInspectionCleanup(string, string, []*analysis.ProviderContinuation, time.Time) (bool, error)
	ListElasticInspectionCleanup(string, string, int) ([]*ElasticInspection, error)
	ClearElasticInspectionCleanup(string, []*analysis.ProviderContinuation) (bool, error)
}

func (s *SQLiteStore) EnsureElasticInspectionSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS elastic_inspections (id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,connection_id TEXT NOT NULL,connection_revision BIGINT NOT NULL,collector_id TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL,state TEXT NOT NULL,payload_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '',cleanup_json TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',owner_token TEXT NOT NULL DEFAULT '',lease_expires_at DATETIME,created_at DATETIME NOT NULL,expires_at DATETIME NOT NULL)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_elastic_inspections_poll ON elastic_inspections(collector_id,state,expires_at)`)
	return err
}
func (s *SQLiteStore) CreateElasticInspection(op *ElasticInspection) error {
	if op == nil || strings.TrimSpace(op.ID) == "" || op.ProjectID == "" || op.ConnectionID == "" || op.ConnectionRevision < 1 || len(op.PayloadJSON) > 96<<10 || !op.ExpiresAt.After(op.CreatedAt) || op.ExpiresAt.Sub(op.CreatedAt) > 15*time.Minute || (op.Kind != "test" && op.Kind != "discover" && op.Kind != "fields" && op.Kind != "preview" && op.Kind != "verify" && op.Kind != "discover_indices" && op.Kind != "discover_fields") {
		return errors.New("invalid Elastic inspection")
	}
	if err := s.EnsureElasticInspectionSchema(); err != nil {
		return err
	}
	op.State = "pending"
	_, err := s.db.Exec(`INSERT INTO elastic_inspections(id,project_id,connection_id,connection_revision,collector_id,kind,state,payload_json,created_at,expires_at) VALUES(?,?,?,?,?,?,'pending',?,?,?)`, op.ID, op.ProjectID, op.ConnectionID, op.ConnectionRevision, op.CollectorID, op.Kind, op.PayloadJSON, op.CreatedAt.UTC(), op.ExpiresAt.UTC())
	return err
}

const elasticInspectionColumns = `id,project_id,connection_id,connection_revision,collector_id,kind,state,payload_json,result_json,cleanup_json,error,owner_token,lease_expires_at,created_at,expires_at`

func scanElasticInspection(row interface{ Scan(...any) error }) (*ElasticInspection, error) {
	var op ElasticInspection
	var lease sql.NullTime
	var cleanup string
	err := row.Scan(&op.ID, &op.ProjectID, &op.ConnectionID, &op.ConnectionRevision, &op.CollectorID, &op.Kind, &op.State, &op.PayloadJSON, &op.ResultJSON, &cleanup, &op.Error, &op.OwnerToken, &lease, &op.CreatedAt, &op.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if lease.Valid {
		at := lease.Time.UTC()
		op.LeaseExpiresAt = &at
	}
	if cleanup != "" {
		if err := json.Unmarshal([]byte(cleanup), &op.CleanupContinuations); err != nil {
			return nil, err
		}
	}
	return &op, nil
}
func (s *SQLiteStore) GetElasticInspection(id string) (*ElasticInspection, error) {
	if err := s.EnsureElasticInspectionSchema(); err != nil {
		return nil, err
	}
	return scanElasticInspection(s.db.QueryRow(`SELECT `+elasticInspectionColumns+` FROM elastic_inspections WHERE id=?`, id))
}

// List includes unexpired pending work and claims which may be recovered after
// expiry. Empty collector selects direct-controller work, never all collectors.
func (s *SQLiteStore) ListElasticInspections(project, collector string, limit int) ([]*ElasticInspection, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid inspection list limit")
	}
	if err := s.EnsureElasticInspectionSchema(); err != nil {
		return nil, err
	}
	query := `SELECT ` + elasticInspectionColumns + ` FROM elastic_inspections WHERE collector_id=? AND state IN ('pending','running') AND expires_at>? AND cleanup_json IN ('','[]')`
	args := []any{collector, time.Now().UTC()}
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
	out := []*ElasticInspection{}
	for rows.Next() {
		op, err := scanElasticInspection(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) ClaimElasticInspection(id, owner string, now, until time.Time) (bool, error) {
	if owner == "" || !until.After(now) || until.Sub(now) > 2*time.Minute {
		return false, errors.New("invalid inspection claim")
	}
	result, err := s.db.Exec(`UPDATE elastic_inspections SET state='running',owner_token=?,lease_expires_at=? WHERE id=? AND expires_at>? AND cleanup_json IN ('','[]') AND (state='pending' OR (state='running' AND (lease_expires_at<=? OR owner_token=?)))`, owner, until.UTC(), id, now.UTC(), now.UTC(), owner)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (s *SQLiteStore) CompleteElasticInspection(id, owner, resultJSON, message string, now time.Time) (bool, error) {
	if len(resultJSON) > 1<<20 || len(message) > 1024 || owner == "" {
		return false, errors.New("invalid inspection completion")
	}
	state := "complete"
	if message != "" {
		state = "error"
	}
	result, err := s.db.Exec(`UPDATE elastic_inspections SET state=?,result_json=?,error=?,lease_expires_at=NULL WHERE id=? AND state='running' AND owner_token=? AND expires_at>? AND lease_expires_at>?`, state, resultJSON, message, id, owner, now.UTC(), now.UTC())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

// ElasticCollectorPublicKey returns the enrolled destination-bound key,
// never a key selected by an inspection caller.
func (s *SQLiteStore) ElasticCollectorPublicKey(project, collector, endpoint string, now time.Time) (string, error) {
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
	if json.Unmarshal([]byte(packsJSON), &packs) != nil || json.Unmarshal([]byte(readinessPacksJSON), &readinessPacks) != nil || json.Unmarshal([]byte(destinationsJSON), &destinations) != nil || !slices.Contains(packs, "elastic") || !slices.Contains(readinessPacks, "elastic") || !slices.Contains(readinessPacks, "elastic.v1") {
		return "", errors.New("collector Elastic pack unavailable")
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

func (s *SQLiteStore) CancelElasticInspection(id, project, connection string, now time.Time) (bool, error) {
	r, err := s.db.Exec(`UPDATE elastic_inspections SET state='cancelled',lease_expires_at=NULL WHERE id=? AND project_id=? AND connection_id=? AND state IN ('pending','running')`, id, project, connection)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}

// CancelElasticInspectionIfOwner is for an executor relinquishing its claim.
// A stale executor must not cancel a claim acquired after its lease expired.
func (s *SQLiteStore) CancelElasticInspectionIfOwner(id, project, connection, owner string, now time.Time) (bool, error) {
	if owner == "" {
		return false, errors.New("inspection owner required")
	}
	r, err := s.db.Exec(`UPDATE elastic_inspections SET state='cancelled',lease_expires_at=NULL WHERE id=? AND project_id=? AND connection_id=? AND state='running' AND owner_token=?`, id, project, connection, owner)
	if err != nil {
		return false, err
	}
	n, err := r.RowsAffected()
	return n == 1, err
}
