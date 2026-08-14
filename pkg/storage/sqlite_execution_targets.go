package storage

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

var (
	_ ExecutionTargetStore = (*SQLiteStore)(nil)
	_ ExecutionTargetStore = (*PostgresStore)(nil)
)

type executionTargetSpecEnvelope struct {
	AzureContainerApps *AzureContainerAppsTargetSpec `json:"azure_container_apps,omitempty"`
	AWSECSFargate      *AWSECSFargateTargetSpec      `json:"aws_ecs_fargate,omitempty"`
	Static             *StaticExecutionTargetSpec    `json:"static,omitempty"`
}

func lockExecutionTargetTx(tx *storeTx, id string) (bool, error) {
	result, err := tx.Exec(`UPDATE execution_targets SET updated_at = updated_at WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func marshalExecutionTargetSpec(target *ExecutionTarget) (string, error) {
	encoded, err := json.Marshal(executionTargetSpecEnvelope{
		AzureContainerApps: target.AzureContainerApps,
		AWSECSFargate:      target.AWSECSFargate,
		Static:             target.Static,
	})
	return string(encoded), err
}

func marshalJSONOrDefault(value any, fallback string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fallback
	}
	return string(encoded)
}

const executionTargetSelect = `SELECT id, name, COALESCE(description, ''), kind,
	COALESCE(cloud_connection_id, ''), COALESCE(cluster_connection_id, ''), COALESCE(region, ''),
	COALESCE(connectivity_mode, 'direct'), COALESCE(standing_owner, 'apex'), COALESCE(burst_owner, 'disabled'),
	COALESCE(archive_mode, 'controller_pull'), COALESCE(reverse_token_hash, ''), COALESCE(reverse_token_expires_at, ''),
	COALESCE(reverse_previous_token_hash, ''), COALESCE(reverse_previous_token_valid_until, ''), COALESCE(spec_json, '{}'), COALESCE(runtime_profiles_json, '[]'),
	COALESCE(capabilities_json, '{}'), COALESCE(status, 'active'), last_checked_at,
	COALESCE(last_check_status, ''), COALESCE(last_check_message, ''), COALESCE(labels_json, ''),
	created_at, updated_at FROM execution_targets`

func scanExecutionTarget(row capacityRowScanner) (*ExecutionTarget, error) {
	var target ExecutionTarget
	var specJSON, profilesJSON, capabilitiesJSON, labelsJSON string
	var tokenExpiresAt, previousTokenValidUntil string
	var checked sql.NullTime
	if err := row.Scan(&target.ID, &target.Name, &target.Description, &target.Kind,
		&target.CloudConnectionID, &target.ClusterConnectionID, &target.Region,
		&target.ConnectivityMode, &target.StandingOwner, &target.BurstOwner,
		&target.ArchiveMode, &target.EnrollmentTokenHash, &tokenExpiresAt, &target.PreviousEnrollmentTokenHash, &previousTokenValidUntil,
		&specJSON, &profilesJSON, &capabilitiesJSON,
		&target.Status, &checked, &target.LastCheckStatus, &target.LastCheckMessage,
		&labelsJSON, &target.CreatedAt, &target.UpdatedAt); err != nil {
		return nil, err
	}
	var spec executionTargetSpecEnvelope
	if err := json.Unmarshal([]byte(specJSON), &spec); err != nil {
		return nil, fmt.Errorf("decode execution target specification: %w", err)
	}
	target.AzureContainerApps = spec.AzureContainerApps
	target.AWSECSFargate = spec.AWSECSFargate
	target.Static = spec.Static
	if err := json.Unmarshal([]byte(profilesJSON), &target.RuntimeProfiles); err != nil {
		return nil, fmt.Errorf("decode execution target runtime profiles: %w", err)
	}
	if err := json.Unmarshal([]byte(capabilitiesJSON), &target.Capabilities); err != nil {
		return nil, fmt.Errorf("decode execution target capabilities: %w", err)
	}
	if checked.Valid {
		value := checked.Time
		target.LastCheckedAt = &value
	}
	target.Labels = unmarshalTags(labelsJSON)
	target.EnrollmentTokenConfigured = target.EnrollmentTokenHash != ""
	if tokenExpiresAt != "" {
		parsed, err := time.Parse(time.RFC3339Nano, tokenExpiresAt)
		if err != nil {
			return nil, fmt.Errorf("decode execution target enrollment expiry: %w", err)
		}
		target.EnrollmentTokenExpiresAt = &parsed
	}
	if previousTokenValidUntil != "" {
		parsed, err := time.Parse(time.RFC3339Nano, previousTokenValidUntil)
		if err != nil {
			return nil, fmt.Errorf("decode execution target previous-token validity: %w", err)
		}
		target.PreviousEnrollmentTokenValidUntil = &parsed
	}
	return &target, nil
}

func validateExecutionTargetReferences(tx *storeTx, target *ExecutionTarget) error {
	switch target.Kind {
	case ExecutionTargetKubernetes:
		var id string
		if err := tx.QueryRow(`SELECT id FROM cluster_connections WHERE id = ?`, target.ClusterConnectionID).Scan(&id); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("cluster connection does not exist")
			}
			return err
		}
	case ExecutionTargetAzureContainerApps, ExecutionTargetAWSECSFargate:
		locked, err := lockCloudConnectionTx(tx, target.CloudConnectionID)
		if err != nil {
			return err
		}
		if !locked {
			return errors.New("cloud connection does not exist")
		}
		var provider string
		if err := tx.QueryRow(`SELECT provider FROM cloud_connections WHERE id = ?`, target.CloudConnectionID).Scan(&provider); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return errors.New("cloud connection does not exist")
			}
			return err
		}
		expected := CloudProviderAzure
		if target.Kind == ExecutionTargetAWSECSFargate {
			expected = CloudProviderAWS
		}
		if provider != expected {
			return fmt.Errorf("execution target kind %s requires a %s cloud connection", target.Kind, expected)
		}
	}
	return nil
}

func (s *SQLiteStore) CreateExecutionTarget(target *ExecutionTarget) error {
	if target == nil {
		return errors.New("execution target is required")
	}
	if err := target.Validate(); err != nil {
		return err
	}
	specJSON, err := marshalExecutionTargetSpec(target)
	if err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := validateExecutionTargetReferences(tx, target); err != nil {
		return err
	}
	now := time.Now()
	if target.CreatedAt.IsZero() {
		target.CreatedAt = now
	}
	target.UpdatedAt = now
	_, err = tx.Exec(`INSERT INTO execution_targets (
		id, name, description, kind, cloud_connection_id, cluster_connection_id, region,
		connectivity_mode, standing_owner, burst_owner, archive_mode, reverse_token_hash,
		reverse_token_expires_at, reverse_previous_token_hash, reverse_previous_token_valid_until,
		spec_json, runtime_profiles_json, capabilities_json, status, labels_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		target.ID, target.Name, target.Description, target.Kind, nullableString(target.CloudConnectionID),
		nullableString(target.ClusterConnectionID), target.Region, target.ConnectivityMode,
		target.StandingOwner, target.BurstOwner, target.ArchiveMode, target.EnrollmentTokenHash,
		executionTargetTimeString(target.EnrollmentTokenExpiresAt), target.PreviousEnrollmentTokenHash,
		executionTargetTimeString(target.PreviousEnrollmentTokenValidUntil), specJSON,
		marshalJSONOrDefault(target.RuntimeProfiles, "[]"), marshalJSONOrDefault(target.Capabilities, "{}"),
		target.Status, marshalTags(target.Labels), target.CreatedAt, target.UpdatedAt)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *SQLiteStore) UpdateExecutionTargetIfUnchanged(target *ExecutionTarget, expected time.Time) (bool, error) {
	if target == nil || expected.IsZero() {
		return false, errors.New("execution target and expected updated_at are required")
	}
	if err := target.Validate(); err != nil {
		return false, err
	}
	specJSON, err := marshalExecutionTargetSpec(target)
	if err != nil {
		return false, err
	}
	runtimeProfilesJSON := marshalJSONOrDefault(target.RuntimeProfiles, "[]")
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	locked, err := lockExecutionTargetTx(tx, target.ID)
	if err != nil {
		return false, err
	}
	if !locked {
		return false, nil
	}
	var current time.Time
	var currentRaw, currentKind, currentCloudConnectionID, currentClusterConnectionID, currentRegion string
	var currentConnectivityMode, currentStandingOwner, currentBurstOwner, currentArchiveMode string
	var currentSpecJSON, currentRuntimeProfilesJSON string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT), kind,
		COALESCE(cloud_connection_id, ''), COALESCE(cluster_connection_id, ''), COALESCE(region, ''),
		COALESCE(connectivity_mode, 'direct'), COALESCE(standing_owner, 'apex'),
		COALESCE(burst_owner, 'disabled'), COALESCE(archive_mode, 'controller_pull'),
		COALESCE(spec_json, '{}'), COALESCE(runtime_profiles_json, '[]')
		FROM execution_targets WHERE id = ?`, target.ID).Scan(&current, &currentRaw, &currentKind,
		&currentCloudConnectionID, &currentClusterConnectionID, &currentRegion, &currentConnectivityMode,
		&currentStandingOwner, &currentBurstOwner, &currentArchiveMode, &currentSpecJSON,
		&currentRuntimeProfilesJSON); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !current.Equal(expected) {
		return false, nil
	}
	placementChanged := currentKind != target.Kind ||
		currentCloudConnectionID != target.CloudConnectionID ||
		currentClusterConnectionID != target.ClusterConnectionID ||
		currentRegion != target.Region ||
		currentConnectivityMode != target.ConnectivityMode ||
		currentStandingOwner != target.StandingOwner ||
		currentBurstOwner != target.BurstOwner ||
		currentArchiveMode != target.ArchiveMode ||
		currentSpecJSON != specJSON ||
		currentRuntimeProfilesJSON != runtimeProfilesJSON
	if placementChanged {
		var references int
		if err := tx.QueryRow(`SELECT
			(SELECT COUNT(*) FROM fleet_pools WHERE execution_target_id = ?) +
			(SELECT COUNT(*) FROM domain_deployment_targets WHERE default_execution_target_id = ?) +
			(SELECT COUNT(*) FROM domain_execution_target_allowlist WHERE execution_target_id = ?)`,
			target.ID, target.ID, target.ID).Scan(&references); err != nil {
			return false, err
		}
		if references > 0 {
			return false, ErrExecutionTargetReferenced
		}
	}
	if err := validateExecutionTargetReferences(tx, target); err != nil {
		return false, err
	}
	target.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE execution_targets SET name = ?, description = ?, kind = ?,
		cloud_connection_id = ?, cluster_connection_id = ?, region = ?, connectivity_mode = ?,
		standing_owner = ?, burst_owner = ?, archive_mode = ?, reverse_token_hash = ?, reverse_token_expires_at = ?,
		reverse_previous_token_hash = ?, reverse_previous_token_valid_until = ?, spec_json = ?, runtime_profiles_json = ?,
		capabilities_json = ?, status = ?, labels_json = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`, target.Name, target.Description, target.Kind,
		nullableString(target.CloudConnectionID), nullableString(target.ClusterConnectionID), target.Region,
		target.ConnectivityMode, target.StandingOwner, target.BurstOwner, target.ArchiveMode, target.EnrollmentTokenHash,
		executionTargetTimeString(target.EnrollmentTokenExpiresAt), target.PreviousEnrollmentTokenHash,
		executionTargetTimeString(target.PreviousEnrollmentTokenValidUntil), specJSON,
		runtimeProfilesJSON, marshalJSONOrDefault(target.Capabilities, "{}"),
		target.Status, marshalTags(target.Labels), target.UpdatedAt, target.ID, currentRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

func executionTargetTimeString(value *time.Time) any {
	if value == nil || value.IsZero() {
		return nil
	}
	return value.UTC().Format(time.RFC3339Nano)
}

func (s *SQLiteStore) UpdateExecutionTargetHealth(id, status, message string, checkedAt time.Time, capabilities TargetCapabilities) error {
	result, err := s.db.Exec(`UPDATE execution_targets SET last_checked_at = ?, last_check_status = ?,
		last_check_message = ?, capabilities_json = ? WHERE id = ?`, checkedAt, status, message,
		marshalJSONOrDefault(capabilities, "{}"), strings.TrimSpace(id))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return sql.ErrNoRows
	}
	return nil
}

func (s *SQLiteStore) GetExecutionTarget(id string) (*ExecutionTarget, error) {
	return scanExecutionTarget(s.db.QueryRow(executionTargetSelect+` WHERE id = ?`, strings.TrimSpace(id)))
}

func (s *SQLiteStore) ListExecutionTargets() ([]*ExecutionTarget, error) {
	rows, err := s.db.Query(executionTargetSelect + ` ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	targets := make([]*ExecutionTarget, 0)
	for rows.Next() {
		target, err := scanExecutionTarget(rows)
		if err != nil {
			return nil, err
		}
		targets = append(targets, target)
	}
	return targets, rows.Err()
}

func (s *SQLiteStore) DeleteExecutionTargetIfUnreferenced(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	locked, err := lockExecutionTargetTx(tx, id)
	if err != nil {
		return false, err
	}
	if !locked {
		return false, sql.ErrNoRows
	}
	var references int
	if err := tx.QueryRow(`SELECT
		(SELECT COUNT(*) FROM fleet_pools WHERE execution_target_id = ?) +
		(SELECT COUNT(*) FROM domain_deployment_targets WHERE default_execution_target_id = ?) +
		(SELECT COUNT(*) FROM domain_execution_target_allowlist WHERE execution_target_id = ?)`, id, id, id).Scan(&references); err != nil {
		return false, err
	}
	if references > 0 {
		return false, nil
	}
	result, err := tx.Exec(`DELETE FROM execution_targets WHERE id = ?`, strings.TrimSpace(id))
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	if affected != 1 {
		return false, sql.ErrNoRows
	}
	return true, tx.Commit()
}

func (s *SQLiteStore) ListFleetPoolsForExecutionTarget(id string) ([]*FleetPool, error) {
	rows, err := s.db.Query(fleetPoolSelect+` WHERE execution_target_id = ? ORDER BY name ASC`, strings.TrimSpace(id))
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
