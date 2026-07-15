package storage

// Narrow reference-key backfill primitives. Controller startup can run on
// several HA replicas at once, so filling a presentation-only key must never
// persist the rest of a stale aggregate snapshot.

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// EnsureScriptRefKey fills an empty script reference key and returns the
// durable winner and current aggregate timestamp. Advancing updated_at fences
// replicas that loaded the script before the key was installed.
func (s *SQLiteStore) EnsureScriptRefKey(id, proposedRefKey string) (string, time.Time, error) {
	return s.ensureVersionedRefKey("scripts", "id", "script", id, proposedRefKey)
}

// EnsureScenarioRefKey fills an empty scenario reference key and returns the
// durable winner and current aggregate timestamp. It updates no scenario child
// rows or editable content.
func (s *SQLiteStore) EnsureScenarioRefKey(id, proposedRefKey string) (string, time.Time, error) {
	return s.ensureVersionedRefKey("scenarios", "id", "scenario", id, proposedRefKey)
}

func (s *SQLiteStore) ensureVersionedRefKey(table, idColumn, entity, id, proposedRefKey string) (string, time.Time, error) {
	id = strings.TrimSpace(id)
	proposedRefKey = strings.TrimSpace(proposedRefKey)
	if id == "" || proposedRefKey == "" {
		return "", time.Time{}, fmt.Errorf("%s id and proposed reference key are required", entity)
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", time.Time{}, err
	}
	defer tx.Rollback()

	// table and idColumn are fixed internal constants from the wrappers above.
	selectQuery := fmt.Sprintf(`SELECT COALESCE(ref_key, ''), updated_at, CAST(updated_at AS TEXT)
		FROM %s WHERE %s = ?`, table, idColumn)
	if s.db.dialect == storeDialectPostgres {
		selectQuery += ` FOR UPDATE`
	}
	var durableRefKey, currentUpdatedRaw string
	var currentUpdatedAt time.Time
	if err := tx.QueryRow(selectQuery, id).Scan(&durableRefKey, &currentUpdatedAt, &currentUpdatedRaw); err != nil {
		return "", time.Time{}, err
	}
	durableRefKey = strings.TrimSpace(durableRefKey)
	if durableRefKey != "" {
		if err := tx.Commit(); err != nil {
			return "", time.Time{}, err
		}
		return durableRefKey, currentUpdatedAt, nil
	}

	updatedAt, err := preciseDatabaseTime(tx, s.db.dialect)
	if err != nil {
		return "", time.Time{}, err
	}
	if !updatedAt.After(currentUpdatedAt) {
		updatedAt = currentUpdatedAt.Add(time.Microsecond)
	}
	updateQuery := fmt.Sprintf(`UPDATE %s SET ref_key = ?, updated_at = ?
		WHERE %s = ? AND (ref_key IS NULL OR TRIM(ref_key) = '') AND CAST(updated_at AS TEXT) = ?`, table, idColumn)
	result, err := tx.Exec(updateQuery, proposedRefKey, updatedAt, id, currentUpdatedRaw)
	if err != nil {
		return "", time.Time{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return "", time.Time{}, err
	}
	if affected != 1 {
		return "", time.Time{}, fmt.Errorf("%s %q changed while its reference key was being backfilled", entity, id)
	}
	if err := tx.Commit(); err != nil {
		return "", time.Time{}, err
	}
	return proposedRefKey, updatedAt, nil
}

// EnsureRunArchiveRefKey fills only the archive catalog key. It coordinates on
// the owning test row with archive publication/retention and refuses to mutate
// a catalog entry once cleanup has installed its durable tombstone.
func (s *SQLiteStore) EnsureRunArchiveRefKey(testID, proposedRefKey string) (string, error) {
	testID = strings.TrimSpace(testID)
	proposedRefKey = strings.TrimSpace(proposedRefKey)
	if testID == "" || proposedRefKey == "" {
		return "", errors.New("test id and proposed archive reference key are required")
	}
	tx, err := s.db.Begin()
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	testQuery := `SELECT 1 FROM tests WHERE id = ?`
	if s.db.dialect == storeDialectPostgres {
		testQuery += ` FOR UPDATE`
	}
	var exists int
	if err := tx.QueryRow(testQuery, testID).Scan(&exists); err != nil {
		return "", err
	}
	var cleanupExists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM archive_cleanup_jobs WHERE test_id = ?`, testID).Scan(&cleanupExists); err != nil {
		return "", err
	}
	if cleanupExists != 0 {
		return "", ErrArchiveCleanupPending
	}
	if _, err := tx.Exec(`UPDATE run_archives SET ref_key = ?
		WHERE test_id = ? AND (ref_key IS NULL OR TRIM(ref_key) = '')`, proposedRefKey, testID); err != nil {
		return "", err
	}
	var durableRefKey string
	if err := tx.QueryRow(`SELECT COALESCE(ref_key, '') FROM run_archives WHERE test_id = ?`, testID).Scan(&durableRefKey); err != nil {
		return "", err
	}
	durableRefKey = strings.TrimSpace(durableRefKey)
	if durableRefKey == "" {
		return "", fmt.Errorf("run archive %q still has an empty reference key", testID)
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return durableRefKey, nil
}
