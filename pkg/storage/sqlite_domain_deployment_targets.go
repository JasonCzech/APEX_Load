package storage

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// Both concrete stores satisfy the optional capability; Postgres reuses the
// same SQL through rewriteSQL.
var (
	_ DomainDeploymentTargetStore = (*SQLiteStore)(nil)
	_ DomainDeploymentTargetStore = (*PostgresStore)(nil)
)

const domainDeploymentTargetSelect = `SELECT domain_id, provider, COALESCE(default_cluster_id, ''),
	COALESCE(updated_by_subject, ''), created_at, updated_at
	FROM domain_deployment_targets`

// scanDomainDeploymentTarget is shared by Get and List so the two projections
// cannot drift (the fleetPoolSelect/scanFleetPool convention).
func scanDomainDeploymentTarget(row capacityRowScanner) (*DomainDeploymentTarget, error) {
	var target DomainDeploymentTarget
	if err := row.Scan(&target.DomainID, &target.Provider, &target.DefaultClusterID,
		&target.UpdatedBySubject, &target.CreatedAt, &target.UpdatedAt); err != nil {
		return nil, err
	}
	return &target, nil
}

// GetDomainDeploymentTarget returns nil with no error when the domain is
// unconstrained. Callers treat a nil target as "no constraint applies", which
// is the state of every domain until a superuser sets one.
func (s *SQLiteStore) GetDomainDeploymentTarget(domainID string) (*DomainDeploymentTarget, error) {
	target, err := scanDomainDeploymentTarget(
		s.db.QueryRow(domainDeploymentTargetSelect+` WHERE domain_id = ?`, strings.TrimSpace(domainID)))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return target, nil
}

func (s *SQLiteStore) ListDomainDeploymentTargets() ([]*DomainDeploymentTarget, error) {
	rows, err := s.db.Query(domainDeploymentTargetSelect + ` ORDER BY domain_id ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]*DomainDeploymentTarget, 0)
	for rows.Next() {
		target, err := scanDomainDeploymentTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

// UpsertDomainDeploymentTargetIfUnchanged writes the target under an
// optimistic-concurrency check on updated_at, so two superusers editing the
// same domain cannot silently overwrite each other.
//
// A zero expectedUpdatedAt means "there must be no row yet". That makes a first
// write explicit rather than letting a stale client clobber a target someone
// else created in the meantime.
func (s *SQLiteStore) UpsertDomainDeploymentTargetIfUnchanged(target *DomainDeploymentTarget, expectedUpdatedAt time.Time) (bool, error) {
	if target == nil {
		return false, errors.New("deployment target is required")
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	var currentCreatedAt time.Time
	// created_at is read here, in the same statement as the concurrency token,
	// rather than re-read after commit: a post-commit read that fails would
	// leave the caller holding a wrong CreatedAt with no error to notice.
	err = tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT), created_at FROM domain_deployment_targets WHERE domain_id = ?`,
		target.DomainID).Scan(&currentUpdatedAt, &currentUpdatedRaw, &currentCreatedAt)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		if !expectedUpdatedAt.IsZero() {
			// The caller believed a target existed; it does not (or was
			// deleted). Refuse rather than resurrect it under stale intent.
			return false, nil
		}
		now := time.Now()
		target.CreatedAt, target.UpdatedAt = now, now
		if _, err := tx.Exec(`INSERT INTO domain_deployment_targets (
				domain_id, provider, default_cluster_id, updated_by_subject, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)`,
			target.DomainID, target.Provider, nullableClusterID(target.DefaultClusterID),
			target.UpdatedBySubject, target.CreatedAt, target.UpdatedAt); err != nil {
			return false, err
		}
		return true, tx.Commit()
	case err != nil:
		return false, err
	}

	if expectedUpdatedAt.IsZero() || !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	target.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE domain_deployment_targets SET
			provider = ?, default_cluster_id = ?, updated_by_subject = ?, updated_at = ?
		WHERE domain_id = ? AND CAST(updated_at AS TEXT) = ?`,
		target.Provider, nullableClusterID(target.DefaultClusterID), target.UpdatedBySubject,
		target.UpdatedAt, target.DomainID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	target.CreatedAt = currentCreatedAt
	return true, tx.Commit()
}

// nullableClusterID keeps default_cluster_id NULL rather than '' so the
// foreign key is satisfiable. SQLite runs with foreign_keys=ON, and an empty
// string references no cluster_connections row.
func nullableClusterID(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

// DeleteDomainDeploymentTarget removes the constraint. Deleting is always
// allowed: removing a constraint can only widen what a pool may do, so it can
// never put an existing pool into a conflicting state.
func (s *SQLiteStore) DeleteDomainDeploymentTarget(domainID string) error {
	_, err := s.db.Exec(`DELETE FROM domain_deployment_targets WHERE domain_id = ?`, strings.TrimSpace(domainID))
	return err
}

// ListFleetPoolsForDomain backs the conflict report shown when a superuser
// retargets a domain, and the refusal to delete a domain that still owns pools.
func (s *SQLiteStore) ListFleetPoolsForDomain(domainID string) ([]*FleetPool, error) {
	trimmed := strings.TrimSpace(domainID)
	if trimmed == "" {
		return nil, nil
	}
	rows, err := s.db.Query(fleetPoolSelect+` WHERE domain_id = ? ORDER BY name ASC`, trimmed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	pools := make([]*FleetPool, 0)
	for rows.Next() {
		pool, err := scanFleetPool(rows)
		if err != nil {
			return nil, err
		}
		pools = append(pools, pool)
	}
	return pools, rows.Err()
}
