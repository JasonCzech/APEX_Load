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

// DynatraceOperation is an expiring administrative operation, independent of
// load-test identity. PayloadJSON is private configuration; ResultJSON contains
// only the bounded provider result projection.
type DynatraceOperation struct {
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

type DynatraceOperationStore interface {
	EnsureDynatraceOperationSchema() error
	DynatraceCollectorPublicKey(string, string, string, time.Time) (string, error)
	CreateDynatraceOperation(*DynatraceOperation) error
	GetDynatraceOperation(string) (*DynatraceOperation, error)
	ListDynatraceOperations(string, string, int) ([]*DynatraceOperation, error)
	ClaimDynatraceOperation(string, string, time.Time, time.Time) (bool, error)
	CompleteDynatraceOperation(string, string, string, string, time.Time) (bool, error)
}

func (s *SQLiteStore) EnsureDynatraceOperationSchema() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS dynatrace_operations (id TEXT PRIMARY KEY,project_id TEXT NOT NULL REFERENCES projects(id) ON DELETE CASCADE,connection_id TEXT NOT NULL,connection_revision BIGINT NOT NULL,collector_id TEXT NOT NULL DEFAULT '',kind TEXT NOT NULL,state TEXT NOT NULL,payload_json TEXT NOT NULL,result_json TEXT NOT NULL DEFAULT '',error TEXT NOT NULL DEFAULT '',owner_token TEXT NOT NULL DEFAULT '',lease_expires_at DATETIME,created_at DATETIME NOT NULL,expires_at DATETIME NOT NULL)`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_dynatrace_operations_poll ON dynatrace_operations(collector_id,state,expires_at)`)
	return err
}
func (s *SQLiteStore) CreateDynatraceOperation(op *DynatraceOperation) error {
	if op == nil || strings.TrimSpace(op.ID) == "" || op.ProjectID == "" || op.ConnectionID == "" || op.ConnectionRevision < 1 || len(op.PayloadJSON) > 96<<10 || !op.ExpiresAt.After(op.CreatedAt) || op.ExpiresAt.Sub(op.CreatedAt) > time.Minute || (op.Kind != "verify" && op.Kind != "discover" && op.Kind != "preview") {
		return errors.New("invalid Dynatrace inspection")
	}
	if err := s.EnsureDynatraceOperationSchema(); err != nil {
		return err
	}
	op.State = "pending"
	_, err := s.db.Exec(`INSERT INTO dynatrace_operations(id,project_id,connection_id,connection_revision,collector_id,kind,state,payload_json,created_at,expires_at) VALUES(?,?,?,?,?,?,'pending',?,?,?)`, op.ID, op.ProjectID, op.ConnectionID, op.ConnectionRevision, op.CollectorID, op.Kind, op.PayloadJSON, op.CreatedAt.UTC(), op.ExpiresAt.UTC())
	return err
}

const dynatraceOperationColumns = `id,project_id,connection_id,connection_revision,collector_id,kind,state,payload_json,result_json,error,owner_token,lease_expires_at,created_at,expires_at`

func scanDynatraceOperation(row interface{ Scan(...any) error }) (*DynatraceOperation, error) {
	var op DynatraceOperation
	var lease sql.NullTime
	err := row.Scan(&op.ID, &op.ProjectID, &op.ConnectionID, &op.ConnectionRevision, &op.CollectorID, &op.Kind, &op.State, &op.PayloadJSON, &op.ResultJSON, &op.Error, &op.OwnerToken, &lease, &op.CreatedAt, &op.ExpiresAt)
	if err != nil {
		return nil, err
	}
	if lease.Valid {
		at := lease.Time.UTC()
		op.LeaseExpiresAt = &at
	}
	return &op, nil
}
func (s *SQLiteStore) GetDynatraceOperation(id string) (*DynatraceOperation, error) {
	if err := s.EnsureDynatraceOperationSchema(); err != nil {
		return nil, err
	}
	return scanDynatraceOperation(s.db.QueryRow(`SELECT `+dynatraceOperationColumns+` FROM dynatrace_operations WHERE id=?`, id))
}

// List includes unexpired pending work and claims which may be recovered after
// expiry. Empty collector selects direct-controller work, never all collectors.
func (s *SQLiteStore) ListDynatraceOperations(project, collector string, limit int) ([]*DynatraceOperation, error) {
	if limit < 1 || limit > 100 {
		return nil, errors.New("invalid inspection list limit")
	}
	if err := s.EnsureDynatraceOperationSchema(); err != nil {
		return nil, err
	}
	query := `SELECT ` + dynatraceOperationColumns + ` FROM dynatrace_operations WHERE collector_id=? AND state IN ('pending','running') AND expires_at>?`
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
	out := []*DynatraceOperation{}
	for rows.Next() {
		op, err := scanDynatraceOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}
func (s *SQLiteStore) ClaimDynatraceOperation(id, owner string, now, until time.Time) (bool, error) {
	if owner == "" || !until.After(now) || until.Sub(now) > 2*time.Minute {
		return false, errors.New("invalid inspection claim")
	}
	result, err := s.db.Exec(`UPDATE dynatrace_operations SET state='running',owner_token=?,lease_expires_at=? WHERE id=? AND expires_at>? AND (state='pending' OR (state='running' AND lease_expires_at<=?))`, owner, until.UTC(), id, now.UTC(), now.UTC())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (s *SQLiteStore) CompleteDynatraceOperation(id, owner, resultJSON, message string, now time.Time) (bool, error) {
	return s.CompleteDynatraceOperationFenced(id, owner, resultJSON, message, now, "", "", 0)
}

// CompleteDynatraceOperationFenced binds direct inspection publication to the
// exact execution term as well as the independently claimed operation lease.
func (s *SQLiteStore) CompleteDynatraceOperationFenced(id, owner, resultJSON, message string, now time.Time, leaseName, leaseOwner string, generation uint64) (bool, error) {
	if len(resultJSON) > 1<<20 || len(message) > 1024 || owner == "" {
		return false, errors.New("invalid inspection completion")
	}
	var tx *storeTx
	var err error
	if leaseName != "" {
		tx, err = s.beginExecutionLeaseTermTransaction(leaseName, leaseOwner, generation)
		if err == nil && tx == nil {
			return false, errors.New("monitor execution owner fence rejected")
		}
	} else {
		tx, err = s.db.Begin()
	}
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var project string
	if err = tx.QueryRow(`SELECT project_id FROM dynatrace_operations WHERE id=?`, id).Scan(&project); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if _, err = lockProjectForOwnedMutation(tx, project); err != nil {
		return false, err
	}
	state := "complete"
	if message != "" {
		state = "error"
	}
	result, err := tx.Exec(`UPDATE dynatrace_operations SET state=?,result_json=?,error=?,lease_expires_at=NULL WHERE id=? AND state='running' AND owner_token=? AND expires_at>? AND lease_expires_at>?`, state, resultJSON, message, id, owner, now.UTC(), now.UTC())
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return n == 1, tx.Commit()
}

// DynatraceCollectorPublicKey returns the enrolled destination-bound key,
// never a key selected by an inspection caller.
func (s *SQLiteStore) DynatraceCollectorPublicKey(project, collector, endpoint string, now time.Time) (string, error) {
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
	if json.Unmarshal([]byte(packsJSON), &packs) != nil || json.Unmarshal([]byte(readinessPacksJSON), &readinessPacks) != nil || json.Unmarshal([]byte(destinationsJSON), &destinations) != nil || !slices.Contains(packs, "dynatrace") || !slices.Contains(readinessPacks, "dynatrace") || !slices.Contains(readinessPacks, "dynatrace.v1") {
		return "", errors.New("collector Dynatrace pack unavailable")
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
