package storage

import "strings"

// ReleaseSchedulerLeaseTermIfTestsIdle expires an exact durable lease term only
// when no test can be executing and no primary archive work remains pending.
// The lease row is locked before reading tests, using the same lock order as
// fenced start commits. A concurrent start either
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
	// An idle test may still own unpublished files on this leader's private
	// staging volume. Do not move collection/publication to a different replica
	// until that work is resolved (or collection explicitly exhausts its budget).
	if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM archive_finalization_jobs)
		OR EXISTS (SELECT 1` + currentArchiveCollection + ` AND c.resolved = 0 AND c.exhausted = 0)`).Scan(&busy); err != nil {
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
