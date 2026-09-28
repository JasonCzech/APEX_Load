package storage

import "strings"

// ReleaseSchedulerLeaseTermIfTestsIdle expires an exact durable lease term only
// when no test can be executing. The lease row is locked before reading tests,
// using the same lock order as fenced start commits. A concurrent start either
// commits first and blocks release, or loses its lease and cannot dispatch.
// Unknown statuses deliberately block this exceptional recovery operation.
func (s *SQLiteStore) ReleaseSchedulerLeaseTermIfTestsIdle(name, owner string, generation uint64) (bool, error) {
	name, owner = strings.TrimSpace(name), strings.TrimSpace(owner)
	tx, err := s.beginExecutionLeaseTermTransaction(name, owner, generation)
	if err != nil || tx == nil {
		return false, err
	}
	defer tx.Rollback()
	var busy bool
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM tests WHERE status IS NULL OR status NOT IN
		('DRAFT', 'PENDING', 'QUEUED', 'SHAKEOUT_REVIEW', 'COMPLETE', 'FAILED', 'ABORTED'))`).Scan(&busy); err != nil {
		return false, err
	}
	if busy {
		return false, nil
	}
	if _, err := tx.Exec(`UPDATE scheduler_leases SET expires_at = updated_at WHERE name = ? AND owner_id = ? AND generation = ?`, name, owner, generation); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
