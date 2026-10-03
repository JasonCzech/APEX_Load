package storage

import "database/sql"

// Separate optional status interface avoids implying that an alternate store
// can perform administrative process-stop attestation through ordinary polls.
type ManagementCollectionStateStore interface {
	GetCurrentManagementPoll(string, string) (*ManagementPoll, error)
}

func (s *SQLiteStore) GetCurrentManagementPoll(project, source string) (*ManagementPoll, error) {
	tx, e := s.db.Begin()
	if e != nil {
		return nil, e
	}
	defer tx.Rollback()
	var id string
	if e = tx.QueryRow(`SELECT poll_id FROM monitor_collection_leases WHERE project_id=? AND source_id=?`, project, source).Scan(&id); e != nil {
		return nil, e
	}
	if id == "" {
		return nil, sql.ErrNoRows
	}
	return getManagementPollTx(tx, project, id)
}
