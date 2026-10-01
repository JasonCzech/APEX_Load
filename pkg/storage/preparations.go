package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"github.com/jasonczech/apexload/pkg/compute"
	"reflect"
	"time"
)

var ErrPreparationConflict = errors.New("preparation changed, expired, or was already consumed")

type Preparation struct {
	Capacity          compute.CapacityPolicy      `json:"capacity,omitempty"`
	ID                string                      `json:"id"`
	ProjectID         string                      `json:"project_id"`
	TestID            string                      `json:"test_id"`
	Revision          int64                       `json:"revision"`
	ExecutionRevision string                      `json:"execution_revision"`
	State             string                      `json:"state"`
	IdempotencyKey    string                      `json:"idempotency_key"`
	CreatedAt         time.Time                   `json:"created_at"`
	ExpiresAt         time.Time                   `json:"expires_at"`
	ReadyAt           time.Time                   `json:"ready_at,omitempty"`
	TTLSeconds        int                         `json:"ttl_seconds"`
	OwnerFence        string                      `json:"-"`
	Failure           string                      `json:"failure,omitempty"`
	Allocation        json.RawMessage             `json:"allocation,omitempty"`
	Monitors          *PreparationMonitorSnapshot `json:"monitors,omitempty"`
}
type PreparationStore interface {
	CreatePreparation(Preparation) (*Preparation, error)
	GetPreparation(string) (*Preparation, error)
	LatestPreparation(string) (*Preparation, error)
	TransitionPreparation(string, string, string, int64, string, json.RawMessage) (*Preparation, error)
	RenewPreparation(string, int64, string, time.Duration) (*Preparation, error)
	ConsumePreparation(string, int64, string, string, string) (*Preparation, error)
}

func (s *SQLiteStore) CreatePreparation(p Preparation) (*Preparation, error) {
	if err := p.Capacity.Validate(); err != nil {
		return nil, err
	}
	if p.ID == "" || p.TestID == "" || p.ProjectID == "" || len(p.ExecutionRevision) != 64 || len(p.IdempotencyKey) < 1 || len(p.IdempotencyKey) > 128 || p.ExpiresAt.Sub(p.CreatedAt) < 5*time.Minute || p.ExpiresAt.Sub(p.CreatedAt) > 4*time.Hour {
		return nil, errors.New("invalid preparation identity or TTL")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, p.ProjectID); err != nil {
		return nil, err
	}
	var owner, status string
	if err = tx.QueryRow(`SELECT project_id,status FROM tests WHERE id=?`, p.TestID).Scan(&owner, &status); err != nil {
		return nil, err
	}
	if owner != p.ProjectID {
		return nil, errors.New("preparation project mismatch")
	}
	var existingID string
	err = tx.QueryRow(`SELECT id FROM preparations WHERE test_id=? AND idempotency_key=?`, p.TestID, p.IdempotencyKey).Scan(&existingID)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		existing, err := s.GetPreparation(existingID)
		if err == nil && (existing.ExecutionRevision != p.ExecutionRevision || existing.ProjectID != p.ProjectID || existing.Capacity != p.Capacity) {
			return nil, ErrPreparationConflict
		}
		return existing, err
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if status != "QUEUED" && status != "PENDING" {
		return nil, errors.New("only queued runs may be prepared")
	}
	if p.OwnerFence != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, "controller-execution-leader", p.OwnerFence)
		if err != nil {
			return nil, err
		}
		if !held {
			return nil, ErrPreparationConflict
		}
	}
	if !p.ExpiresAt.After(time.Now()) {
		return nil, ErrPreparationConflict
	}
	var active int
	if err = tx.QueryRow(`SELECT COUNT(*) FROM preparations WHERE test_id=? AND state IN ('preparing','ready')`, p.TestID).Scan(&active); err != nil {
		return nil, err
	}
	if active > 0 {
		return nil, ErrPreparationConflict
	}
	p.State = "preparing"
	p.Monitors, err = preparationMonitorSnapshotTx(tx, p.ProjectID, p.TestID)
	if err != nil {
		return nil, err
	}
	p.TTLSeconds = int(p.ExpiresAt.Sub(p.CreatedAt).Seconds())
	p.Revision = 1
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`INSERT INTO preparations(id,project_id,test_id,revision,execution_revision,state,idempotency_key,owner_fence,created_at,expires_at,payload) VALUES(?,?,?,?,?,?,?,?,?,?,?)`, p.ID, p.ProjectID, p.TestID, p.Revision, p.ExecutionRevision, p.State, p.IdempotencyKey, p.OwnerFence, p.CreatedAt, p.ExpiresAt, string(body)); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &p, nil
}
func (s *SQLiteStore) GetPreparation(id string) (*Preparation, error) {
	var body, owner string
	if err := s.db.QueryRow(`SELECT payload,owner_fence FROM preparations WHERE id=?`, id).Scan(&body, &owner); err != nil {
		return nil, err
	}
	var p Preparation
	if err := json.Unmarshal([]byte(body), &p); err != nil {
		return nil, err
	}
	p.OwnerFence = owner
	return &p, nil
}
func (s *SQLiteStore) LatestPreparation(test string) (*Preparation, error) {
	var id string
	if err := s.db.QueryRow(`SELECT id FROM preparations WHERE test_id=? ORDER BY created_at DESC,id DESC LIMIT 1`, test).Scan(&id); err != nil {
		return nil, err
	}
	p, err := s.GetPreparation(id)
	if err != nil || (p.State != "preparing" && p.State != "ready") {
		return p, err
	}
	// A restarted controller terminalizes abandoned INITIALIZING runs through
	// normal execution recovery. Reflect that durable outcome in preparation
	// history without requiring the lost controller's ownership fence.
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, p.ProjectID); err != nil {
		return nil, err
	}
	var status string
	if err = tx.QueryRow(`SELECT status FROM tests WHERE id=?`, test).Scan(&status); err != nil {
		return nil, err
	}
	next := ""
	switch status {
	case "COMPLETE", "FAILED", "ABORTED", "CANCELLED":
		next = "failed"
	}
	if !p.ExpiresAt.After(time.Now()) {
		next = "expired"
	}
	if next == "" {
		return p, tx.Commit()
	}
	p.State = next
	p.Revision++
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	if _, err = tx.Exec(`UPDATE preparations SET state=?,revision=?,payload=? WHERE id=? AND revision=? AND state IN ('preparing','ready')`, p.State, p.Revision, string(body), p.ID, p.Revision-1); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return s.GetPreparation(id)
}

func (s *SQLiteStore) TransitionPreparation(id, from, to string, revision int64, owner string, allocation json.RawMessage) (*Preparation, error) {
	return s.transitionPreparation(id, from, to, revision, owner, allocation, 0, nil)
}

func (s *SQLiteStore) RenewPreparation(id string, revision int64, owner string, ttl time.Duration) (*Preparation, error) {
	if ttl < 5*time.Minute || ttl > 4*time.Hour {
		return nil, ErrPreparationConflict
	}
	return s.transitionPreparation(id, "ready", "ready", revision, owner, nil, ttl, nil)
}

func (s *SQLiteStore) ConsumePreparation(id string, revision int64, owner, subject, displayName string) (*Preparation, error) {
	return s.transitionPreparation(id, "ready", "consumed", revision, owner, nil, 0, &[2]string{subject, displayName})
}

func (s *SQLiteStore) transitionPreparation(id, from, to string, revision int64, owner string, allocation json.RawMessage, renewal time.Duration, actor *[2]string) (*Preparation, error) {
	valid := from == "preparing" && (to == "ready" || to == "failed" || to == "cancelled" || to == "expired") || from == "ready" && (to == "consumed" || to == "failed" || to == "cancelled" || to == "expired")
	valid = valid || from == "ready" && to == "ready" && renewal > 0
	if !valid || len(allocation) > 128<<10 {
		return nil, ErrPreparationConflict
	}
	p, err := s.GetPreparation(id)
	if err != nil {
		return nil, err
	}
	if p.Revision != revision || p.State != from || p.OwnerFence != owner {
		return nil, ErrPreparationConflict
	}
	now := time.Now().UTC()
	if (to == "ready" || to == "consumed") && !p.ExpiresAt.After(now) {
		return nil, ErrPreparationConflict
	}
	p.State = to
	if to == "ready" {
		if from == "preparing" {
			p.ReadyAt = now
		}
		if renewal > 0 {
			p.TTLSeconds = int(renewal.Seconds())
		}
		if p.TTLSeconds == 0 {
			p.TTLSeconds = int(p.ExpiresAt.Sub(p.CreatedAt).Seconds())
		}
		p.ExpiresAt = now.Add(time.Duration(p.TTLSeconds) * time.Second)
	}
	p.Revision++
	if allocation != nil {
		p.Allocation = allocation
	}
	body, err := json.Marshal(p)
	if err != nil {
		return nil, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = lockProjectForOwnedMutation(tx, p.ProjectID); err != nil {
		return nil, err
	}
	if owner != "" {
		held, err := schedulerLeaseHeldInTransaction(tx, s.db.dialect, "controller-execution-leader", owner)
		if err != nil {
			return nil, err
		}
		if !held {
			return nil, ErrPreparationConflict
		}
	}
	if actor != nil {
		result, err := tx.Exec(`UPDATE tests SET started_by_subject=?,started_by_display_name=? WHERE id=? AND project_id=? AND status='INITIALIZING'`, actor[0], actor[1], p.TestID, p.ProjectID)
		if err != nil {
			return nil, err
		}
		if n, err := result.RowsAffected(); err != nil {
			return nil, err
		} else if n != 1 {
			return nil, ErrPreparationConflict
		}
	}
	if to == "ready" || to == "consumed" {
		current, err := preparationMonitorSnapshotTx(tx, p.ProjectID, p.TestID)
		if err != nil {
			return nil, err
		}
		if !reflect.DeepEqual(current, p.Monitors) {
			return nil, ErrPreparationConflict
		}
	}
	result, err := tx.Exec(`UPDATE preparations SET state=?,revision=?,expires_at=?,payload=? WHERE id=? AND revision=? AND state=? AND owner_fence=?`, to, p.Revision, p.ExpiresAt, string(body), id, revision, from, owner)
	if err != nil {
		return nil, err
	}
	if n, err := result.RowsAffected(); err != nil {
		return nil, err
	} else if n != 1 {
		return nil, ErrPreparationConflict
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return p, nil
}
