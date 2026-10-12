package storage

import (
	"context"
	"database/sql"
	"errors"
	"math"
)

const MaxRendezvousSnapshotBytes = 2 << 20

type RendezvousRecord struct {
	RunID     string
	Group     string
	ProjectID string
	Revision  uint64
	Snapshot  []byte
}
type RendezvousStore interface {
	InitializeRendezvous(context.Context, RendezvousRecord, LiveTableExecutionTerm) error
	ClaimRendezvous(context.Context, string, string, string, LiveTableExecutionTerm) (*RendezvousRecord, error)
	SaveRendezvous(context.Context, RendezvousRecord, uint64, LiveTableExecutionTerm) error
}

func rendezvousSchemaStatements() []string {
	return []string{`CREATE TABLE IF NOT EXISTS run_rendezvous (
 run_id TEXT NOT NULL REFERENCES tests(id),
 group_name TEXT NOT NULL,
 project_id TEXT NOT NULL REFERENCES projects(id),
 revision BIGINT NOT NULL,
 owner_lease_name TEXT NOT NULL,
 owner_id TEXT NOT NULL,
 owner_generation BIGINT NOT NULL,
 snapshot_json TEXT NOT NULL,
 snapshot_hash TEXT NOT NULL,
 PRIMARY KEY(run_id,group_name)
)`}
}

func validateRendezvousRecord(r RendezvousRecord) error {
	if r.RunID == "" || r.Group == "" || len(r.Group) > 256 || r.ProjectID == "" || r.Revision > math.MaxInt64 {
		return ErrLiveTableConflict
	}
	return validateLivePayload(r.Snapshot, MaxRendezvousSnapshotBytes)
}
func lockRendezvousRun(ctx context.Context, tx *storeTx, run, project string) error {
	if _, err := lockProjectForOwnedMutationContext(ctx, tx, project); err != nil {
		return err
	}
	query := `SELECT COALESCE(project_id,''),status FROM tests WHERE id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	var actual, status string
	if err := tx.QueryRowContext(ctx, query, run).Scan(&actual, &status); err != nil {
		return err
	}
	if normalizeProjectID(actual) != project {
		return ErrLiveTableConflict
	}
	switch status {
	case "INITIALIZING", "STARTING", "RUNNING", "PAUSED", "QUEUED":
		return nil
	default:
		return ErrLiveTableClosed
	}
}
func readRendezvousTx(ctx context.Context, tx *storeTx, run, group, project string) (*RendezvousRecord, LiveTableExecutionTerm, error) {
	query := `SELECT revision,snapshot_json,snapshot_hash,owner_lease_name,owner_id,owner_generation FROM run_rendezvous WHERE run_id=? AND group_name=? AND project_id=?`
	if tx.dialect == storeDialectPostgres {
		query += ` FOR UPDATE`
	}
	r := &RendezvousRecord{RunID: run, Group: group, ProjectID: project}
	var body, hash string
	var owner LiveTableExecutionTerm
	if err := tx.QueryRowContext(ctx, query, run, group, project).Scan(&r.Revision, &body, &hash, &owner.LeaseName, &owner.OwnerID, &owner.Generation); err != nil {
		return nil, owner, err
	}
	if len(body) > MaxRendezvousSnapshotBytes || liveTableHash([]byte(body)) != hash {
		return nil, owner, ErrLiveTableCorrupt
	}
	r.Snapshot = []byte(body)
	return r, owner, nil
}
func (s *SQLiteStore) InitializeRendezvous(ctx context.Context, r RendezvousRecord, term LiveTableExecutionTerm) error {
	if err := validateRendezvousRecord(r); err != nil {
		return err
	}
	if r.Revision != 0 {
		return ErrLiveTableConflict
	}
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockRendezvousRun(ctx, tx, r.RunID, r.ProjectID); err != nil {
		return err
	}
	existing, owner, err := readRendezvousTx(ctx, tx, r.RunID, r.Group, r.ProjectID)
	if err == nil {
		if owner != term {
			return ErrLiveTableFenced
		}
		if existing.Revision != 0 || string(existing.Snapshot) != string(r.Snapshot) {
			return ErrLiveTableConflict
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	var count int
	if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM run_rendezvous WHERE run_id=?`, r.RunID).Scan(&count); err != nil {
		return err
	}
	if count >= 32 {
		return ErrLiveTableLimit
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO run_rendezvous(run_id,group_name,project_id,revision,owner_lease_name,owner_id,owner_generation,snapshot_json,snapshot_hash) VALUES(?,?,?,0,?,?,?,?,?)`, r.RunID, r.Group, r.ProjectID, term.LeaseName, term.OwnerID, term.Generation, string(r.Snapshot), liveTableHash(r.Snapshot))
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (s *SQLiteStore) ClaimRendezvous(ctx context.Context, run, group, project string, term LiveTableExecutionTerm) (*RendezvousRecord, error) {
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err = lockRendezvousRun(ctx, tx, run, project); err != nil {
		return nil, err
	}
	r, owner, err := readRendezvousTx(ctx, tx, run, group, project)
	if err != nil {
		return nil, err
	}
	if owner.LeaseName != term.LeaseName || owner.Generation > term.Generation || owner.Generation == term.Generation && owner.OwnerID != term.OwnerID {
		return nil, ErrLiveTableFenced
	}
	_, err = tx.ExecContext(ctx, `UPDATE run_rendezvous SET owner_id=?,owner_generation=? WHERE run_id=? AND group_name=?`, term.OwnerID, term.Generation, run, group)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return r, nil
}
func (s *SQLiteStore) SaveRendezvous(ctx context.Context, r RendezvousRecord, previous uint64, term LiveTableExecutionTerm) error {
	if err := validateRendezvousRecord(r); err != nil {
		return err
	}
	if r.Revision != previous+1 || r.Revision == 0 {
		return ErrLiveTableConflict
	}
	tx, err := s.beginLiveTableTx(ctx, term)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = lockRendezvousRun(ctx, tx, r.RunID, r.ProjectID); err != nil {
		return err
	}
	old, owner, err := readRendezvousTx(ctx, tx, r.RunID, r.Group, r.ProjectID)
	if err != nil {
		return err
	}
	if owner != term {
		return ErrLiveTableFenced
	}
	if old.Revision == r.Revision && string(old.Snapshot) == string(r.Snapshot) {
		return tx.Commit()
	}
	if old.Revision != previous {
		return ErrLiveTableConflict
	}
	_, err = tx.ExecContext(ctx, `UPDATE run_rendezvous SET revision=?,snapshot_json=?,snapshot_hash=? WHERE run_id=? AND group_name=?`, r.Revision, string(r.Snapshot), liveTableHash(r.Snapshot), r.RunID, r.Group)
	if err != nil {
		return err
	}
	return tx.Commit()
}
