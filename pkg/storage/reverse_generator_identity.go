package storage

import (
	"errors"
	"strings"
	"time"
)

// ReverseGeneratorIdentityStore binds the globally routable generator identity
// to one execution target. Ownership survives disconnects and controller
// restarts, and is never reassigned by presenting another target's token.
type ReverseGeneratorIdentityStore interface {
	ClaimReverseGeneratorIdentity(executionTargetID, generatorID string) (bool, error)
}

var (
	_ ReverseGeneratorIdentityStore = (*SQLiteStore)(nil)
	_ ReverseGeneratorIdentityStore = (*PostgresStore)(nil)
)

// ValidateReverseGeneratorIdentity keeps the identity safe to embed verbatim in
// a tunnel target. URL and target-metadata delimiters must never be interpreted
// as another generator after its ownership has already been checked.
func ValidateReverseGeneratorIdentity(generatorID string) error {
	if generatorID == "" || len(generatorID) > 256 {
		return errors.New("generator identity must contain between 1 and 256 characters")
	}
	for _, character := range generatorID {
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' || character >= '0' && character <= '9' || character == '-' || character == '_' || character == '.' {
			continue
		}
		return errors.New("generator identity must contain only letters, digits, dots, underscores, and hyphens")
	}
	return nil
}

func (s *SQLiteStore) ClaimReverseGeneratorIdentity(executionTargetID, generatorID string) (bool, error) {
	executionTargetID, generatorID = strings.TrimSpace(executionTargetID), strings.TrimSpace(generatorID)
	if executionTargetID == "" || generatorID == "" {
		return false, errors.New("execution target and generator identities are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	claimed, err := claimReverseGeneratorIdentityTx(tx, executionTargetID, generatorID)
	if err != nil || !claimed {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func claimReverseGeneratorIdentityTx(tx *storeTx, executionTargetID, generatorID string) (bool, error) {
	if err := ValidateReverseGeneratorIdentity(generatorID); err != nil {
		return false, err
	}
	// Inserting the unique generator ID serializes competing targets on both
	// SQLite and PostgreSQL. No caller may overwrite another target's owner.
	if _, err := tx.Exec(`INSERT INTO reverse_generator_owners (generator_id, execution_target_id, created_at)
		VALUES (?, ?, ?) ON CONFLICT(generator_id) DO NOTHING`, generatorID, executionTargetID, time.Now().UTC()); err != nil {
		return false, err
	}
	var owner string
	if err := tx.QueryRow(`SELECT execution_target_id FROM reverse_generator_owners WHERE generator_id = ?`, generatorID).Scan(&owner); err != nil {
		return false, err
	}
	if owner != executionTargetID {
		return false, nil
	}
	// Databases created before the ownership ledger retain authority in launch
	// intents and pool membership. Consult that evidence before accepting the
	// first enrollment after upgrade; never let its arrival order reassign an ID.
	var conflicts int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM capacity_instance_intents WHERE generator_id = ? AND execution_target_id <> ?) +
		(SELECT COUNT(*) FROM fleet_pool_members m JOIN fleet_pools p ON p.id = m.pool_id
		 WHERE m.generator_id = ? AND COALESCE(p.execution_target_id, '') <> '' AND p.execution_target_id <> ?) +
		(SELECT COUNT(*) FROM generator_metadata g JOIN fleet_pools p ON p.id = g.fleet_pool_id
		 WHERE g.generator_id = ? AND COALESCE(p.execution_target_id, '') <> '' AND p.execution_target_id <> ?)`,
		generatorID, executionTargetID, generatorID, executionTargetID, generatorID, executionTargetID).Scan(&conflicts); err != nil {
		return false, err
	}
	return conflicts == 0, nil
}
