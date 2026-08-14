package storage

import (
	"errors"
	"strings"
	"time"
)

var (
	_ CapacityInstanceIntentStore = (*SQLiteStore)(nil)
	_ CapacityInstanceIntentStore = (*PostgresStore)(nil)
)

const capacityInstanceIntentSelect = `SELECT id, execution_target_id, fleet_pool_id,
	COALESCE(test_id, ''), COALESCE(provisioning_request_id, ''), client_token, launch_nonce,
	COALESCE(generator_id, ''), COALESCE(provider_id, ''), state, archive_state, cleanup_deadline, created_at, updated_at
	FROM capacity_instance_intents`

func scanCapacityInstanceIntent(row capacityRowScanner) (*CapacityInstanceIntent, error) {
	var intent CapacityInstanceIntent
	if err := row.Scan(&intent.ID, &intent.ExecutionTargetID, &intent.FleetPoolID, &intent.TestID,
		&intent.ProvisioningRequestID, &intent.ClientToken, &intent.LaunchNonce, &intent.GeneratorID, &intent.ProviderID,
		&intent.State, &intent.ArchiveState, &intent.CleanupDeadline, &intent.CreatedAt, &intent.UpdatedAt); err != nil {
		return nil, err
	}
	return &intent, nil
}

func (s *SQLiteStore) CreateCapacityInstanceIntent(intent *CapacityInstanceIntent) error {
	if intent == nil || strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.ExecutionTargetID) == "" ||
		strings.TrimSpace(intent.FleetPoolID) == "" || strings.TrimSpace(intent.ClientToken) == "" ||
		strings.TrimSpace(intent.LaunchNonce) == "" || strings.TrimSpace(intent.GeneratorID) == "" {
		return errors.New("capacity instance intent identity, target, pool, client token, launch nonce, and generator id are required")
	}
	if intent.State == "" {
		intent.State = CapacityIntentPlanned
	}
	if intent.ArchiveState == "" {
		intent.ArchiveState = CapacityArchivePending
	}
	now := time.Now().UTC()
	if intent.CreatedAt.IsZero() {
		intent.CreatedAt = now
	}
	intent.UpdatedAt = now
	_, err := s.db.Exec(`INSERT INTO capacity_instance_intents
		(id, execution_target_id, fleet_pool_id, test_id, provisioning_request_id, client_token,
		 launch_nonce, generator_id, provider_id, state, archive_state, cleanup_deadline, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, intent.ID, intent.ExecutionTargetID,
		intent.FleetPoolID, nullableString(intent.TestID), nullableString(intent.ProvisioningRequestID),
		intent.ClientToken, intent.LaunchNonce, intent.GeneratorID, nullableString(intent.ProviderID), intent.State,
		intent.ArchiveState, intent.CleanupDeadline, intent.CreatedAt, intent.UpdatedAt)
	return err
}

func (s *SQLiteStore) UpdateCapacityInstanceIntentIfUnchanged(intent *CapacityInstanceIntent, expected time.Time) (bool, error) {
	if intent == nil || expected.IsZero() {
		return false, errors.New("capacity intent and expected updated_at are required")
	}
	intent.UpdatedAt = time.Now().UTC()
	result, err := s.db.Exec(`UPDATE capacity_instance_intents SET provider_id=?, state=?, archive_state=?,
		cleanup_deadline=?, updated_at=? WHERE id=? AND updated_at=?`, nullableString(intent.ProviderID),
		intent.State, intent.ArchiveState, intent.CleanupDeadline, intent.UpdatedAt, intent.ID, expected)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func (s *SQLiteStore) GetCapacityInstanceIntent(id string) (*CapacityInstanceIntent, error) {
	return scanCapacityInstanceIntent(s.db.QueryRow(capacityInstanceIntentSelect+` WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) ListCapacityInstanceIntents(executionTargetID, provisioningRequestID string) ([]*CapacityInstanceIntent, error) {
	query := capacityInstanceIntentSelect + ` WHERE 1=1`
	args := []any{}
	if strings.TrimSpace(executionTargetID) != "" {
		query += ` AND execution_target_id = ?`
		args = append(args, strings.TrimSpace(executionTargetID))
	}
	if strings.TrimSpace(provisioningRequestID) != "" {
		query += ` AND provisioning_request_id = ?`
		args = append(args, strings.TrimSpace(provisioningRequestID))
	}
	query += ` ORDER BY created_at, id`
	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]*CapacityInstanceIntent, 0)
	for rows.Next() {
		item, err := scanCapacityInstanceIntent(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}
