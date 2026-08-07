package storage

// SQLite store — administrator-registered Kubernetes cluster connections.
// Same SQL serves the PostgreSQL backend via rewriteSQL.

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ClusterConnectionStore is satisfied by both backends; PostgresStore reaches
// these through the embedded SQLite implementation, with rewriteSQL handling
// the dialect differences.
var (
	_ ClusterConnectionStore = (*SQLiteStore)(nil)
	_ ClusterConnectionStore = (*PostgresStore)(nil)
)

const clusterConnectionSelect = `SELECT id, name, COALESCE(description, ''), kind,
	COALESCE(api_server_url, ''), namespace, COALESCE(generator_selector, ''),
	COALESCE(generator_statefulset, ''), COALESCE(generator_container, ''),
	COALESCE(ca_certificate_pem, ''), COALESCE(tls_mode, 'custom_ca'), COALESCE(auth_mode, 'in_cluster'),
	COALESCE(azure_tenant_id, ''), COALESCE(azure_client_id, ''), COALESCE(azure_scope, ''), COALESCE(azure_authority, ''),
	COALESCE(client_secret_ciphertext, ''), COALESCE(client_secret_wrapped_key, ''), COALESCE(client_secret_env_var, ''),
	COALESCE(static_token_ciphertext, ''), COALESCE(static_token_wrapped_key, ''),
	COALESCE(aws_region, ''), COALESCE(aws_cluster_name, ''), COALESCE(aws_credential_mode, ''),
	COALESCE(aws_access_key_id, ''), COALESCE(aws_role_arn, ''), COALESCE(aws_sts_endpoint, ''),
	COALESCE(aws_secret_key_ciphertext, ''), COALESCE(aws_secret_key_wrapped_key, ''),
	COALESCE(aws_external_id_ciphertext, ''), COALESCE(aws_external_id_wrapped_key, ''),
	COALESCE(status, 'active'), last_checked_at, COALESCE(last_check_status, ''), COALESCE(last_check_message, ''),
	COALESCE(labels_json, ''), created_at, updated_at
	FROM cluster_connections`

// scanClusterConnection is shared by Get and List so the two projections can
// never drift (the fleetPoolSelect/scanFleetPool convention).
func scanClusterConnection(row capacityRowScanner) (*ClusterConnection, error) {
	var conn ClusterConnection
	var labelsJSON string
	var lastCheckedAt sql.NullTime
	if err := row.Scan(&conn.ID, &conn.Name, &conn.Description, &conn.Kind,
		&conn.APIServerURL, &conn.Namespace, &conn.GeneratorSelector,
		&conn.GeneratorStatefulSet, &conn.GeneratorContainer,
		&conn.CACertificatePEM, &conn.TLSMode, &conn.AuthMode,
		&conn.AzureTenantID, &conn.AzureClientID, &conn.AzureScope, &conn.AzureAuthority,
		&conn.ClientSecretCiphertext, &conn.ClientSecretWrappedKey, &conn.ClientSecretEnvVar,
		&conn.StaticTokenCiphertext, &conn.StaticTokenWrappedKey,
		&conn.AWSRegion, &conn.AWSClusterName, &conn.AWSCredentialMode,
		&conn.AWSAccessKeyID, &conn.AWSRoleARN, &conn.AWSSTSEndpoint,
		&conn.AWSSecretKeyCiphertext, &conn.AWSSecretKeyWrappedKey,
		&conn.AWSExternalIDCiphertext, &conn.AWSExternalIDWrappedKey,
		&conn.Status, &lastCheckedAt, &conn.LastCheckStatus, &conn.LastCheckMessage,
		&labelsJSON, &conn.CreatedAt, &conn.UpdatedAt); err != nil {
		return nil, err
	}
	conn.Labels = unmarshalTags(labelsJSON)
	if lastCheckedAt.Valid {
		checked := lastCheckedAt.Time
		conn.LastCheckedAt = &checked
	}
	return &conn, nil
}

func (s *SQLiteStore) CreateClusterConnection(conn *ClusterConnection) error {
	if conn == nil || strings.TrimSpace(conn.ID) == "" {
		return errors.New("cluster connection id is required")
	}
	if err := conn.Validate(); err != nil {
		return err
	}
	now := time.Now()
	if conn.CreatedAt.IsZero() {
		conn.CreatedAt = now
	}
	conn.UpdatedAt = now
	if conn.Status == "" {
		conn.Status = "active"
	}
	_, err := s.db.Exec(`INSERT INTO cluster_connections (
			id, name, description, kind, api_server_url, namespace, generator_selector,
			generator_statefulset, generator_container, ca_certificate_pem, tls_mode, auth_mode,
			azure_tenant_id, azure_client_id, azure_scope, azure_authority,
			client_secret_ciphertext, client_secret_wrapped_key, client_secret_env_var,
			static_token_ciphertext, static_token_wrapped_key,
			aws_region, aws_cluster_name, aws_credential_mode, aws_access_key_id, aws_role_arn, aws_sts_endpoint,
			aws_secret_key_ciphertext, aws_secret_key_wrapped_key,
			aws_external_id_ciphertext, aws_external_id_wrapped_key,
			status, labels_json, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		conn.ID, conn.Name, conn.Description, conn.Kind, conn.APIServerURL, conn.Namespace, conn.GeneratorSelector,
		conn.GeneratorStatefulSet, conn.GeneratorContainer, conn.CACertificatePEM, conn.TLSMode, conn.AuthMode,
		conn.AzureTenantID, conn.AzureClientID, conn.AzureScope, conn.AzureAuthority,
		conn.ClientSecretCiphertext, conn.ClientSecretWrappedKey, conn.ClientSecretEnvVar,
		conn.StaticTokenCiphertext, conn.StaticTokenWrappedKey,
		conn.AWSRegion, conn.AWSClusterName, conn.AWSCredentialMode, conn.AWSAccessKeyID, conn.AWSRoleARN, conn.AWSSTSEndpoint,
		conn.AWSSecretKeyCiphertext, conn.AWSSecretKeyWrappedKey,
		conn.AWSExternalIDCiphertext, conn.AWSExternalIDWrappedKey,
		conn.Status, marshalTags(conn.Labels), conn.CreatedAt, conn.UpdatedAt)
	return err
}

// UpdateClusterConnectionIfUnchanged applies administrator edits under an
// optimistic-concurrency check on updated_at, so two admins editing the same
// connection cannot silently overwrite each other.
//
// The caller is responsible for carrying forward the existing ciphertext when
// the request omits a credential; this method writes exactly what it is given,
// so a caller that passes empty ciphertext WILL clear the stored credential.
func (s *SQLiteStore) UpdateClusterConnectionIfUnchanged(conn *ClusterConnection, expectedUpdatedAt time.Time) (bool, error) {
	if conn == nil || strings.TrimSpace(conn.ID) == "" {
		return false, errors.New("cluster connection id is required")
	}
	if expectedUpdatedAt.IsZero() {
		return false, errors.New("expected cluster connection updated_at is required")
	}
	if err := conn.Validate(); err != nil {
		return false, err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var currentUpdatedAt time.Time
	var currentUpdatedRaw string
	if err := tx.QueryRow(`SELECT updated_at, CAST(updated_at AS TEXT) FROM cluster_connections WHERE id = ?`, conn.ID).
		Scan(&currentUpdatedAt, &currentUpdatedRaw); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if !currentUpdatedAt.Equal(expectedUpdatedAt) {
		return false, nil
	}
	conn.UpdatedAt = time.Now()
	result, err := tx.Exec(`UPDATE cluster_connections SET
			name = ?, description = ?, kind = ?, api_server_url = ?, namespace = ?, generator_selector = ?,
			generator_statefulset = ?, generator_container = ?, ca_certificate_pem = ?, tls_mode = ?, auth_mode = ?,
			azure_tenant_id = ?, azure_client_id = ?, azure_scope = ?, azure_authority = ?,
			client_secret_ciphertext = ?, client_secret_wrapped_key = ?, client_secret_env_var = ?,
			static_token_ciphertext = ?, static_token_wrapped_key = ?,
			aws_region = ?, aws_cluster_name = ?, aws_credential_mode = ?,
			aws_access_key_id = ?, aws_role_arn = ?, aws_sts_endpoint = ?,
			aws_secret_key_ciphertext = ?, aws_secret_key_wrapped_key = ?,
			aws_external_id_ciphertext = ?, aws_external_id_wrapped_key = ?,
			status = ?, labels_json = ?, updated_at = ?
		WHERE id = ? AND CAST(updated_at AS TEXT) = ?`,
		conn.Name, conn.Description, conn.Kind, conn.APIServerURL, conn.Namespace, conn.GeneratorSelector,
		conn.GeneratorStatefulSet, conn.GeneratorContainer, conn.CACertificatePEM, conn.TLSMode, conn.AuthMode,
		conn.AzureTenantID, conn.AzureClientID, conn.AzureScope, conn.AzureAuthority,
		conn.ClientSecretCiphertext, conn.ClientSecretWrappedKey, conn.ClientSecretEnvVar,
		conn.StaticTokenCiphertext, conn.StaticTokenWrappedKey,
		conn.AWSRegion, conn.AWSClusterName, conn.AWSCredentialMode,
		conn.AWSAccessKeyID, conn.AWSRoleARN, conn.AWSSTSEndpoint,
		conn.AWSSecretKeyCiphertext, conn.AWSSecretKeyWrappedKey,
		conn.AWSExternalIDCiphertext, conn.AWSExternalIDWrappedKey,
		conn.Status, marshalTags(conn.Labels), conn.UpdatedAt, conn.ID, currentUpdatedRaw)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	return true, tx.Commit()
}

// UpdateClusterConnectionHealth records a probe result. It deliberately skips
// the CAS check and touches only probe-owned columns — the same reasoning as
// UpdateFleetPoolDesiredSize, so a background health write cannot restore stale
// administrator fields.
func (s *SQLiteStore) UpdateClusterConnectionHealth(id, status, message string, checkedAt time.Time) error {
	result, err := s.db.Exec(`UPDATE cluster_connections SET last_check_status = ?, last_check_message = ?, last_checked_at = ?
		WHERE id = ?`, status, message, checkedAt, id)
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

func (s *SQLiteStore) GetClusterConnection(id string) (*ClusterConnection, error) {
	return scanClusterConnection(s.db.QueryRow(clusterConnectionSelect+` WHERE id = ?`, id))
}

func (s *SQLiteStore) ListClusterConnections() ([]*ClusterConnection, error) {
	rows, err := s.db.Query(clusterConnectionSelect + ` ORDER BY name ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	connections := make([]*ClusterConnection, 0)
	for rows.Next() {
		conn, err := scanClusterConnection(rows)
		if err != nil {
			return nil, err
		}
		connections = append(connections, conn)
	}
	return connections, rows.Err()
}

// DeleteClusterConnectionIfUnreferenced refuses rather than cascading. A fleet
// pool left pointing at a deleted connection would fall back to the
// controller's own cluster on the next scale — i.e. start generators somewhere
// the administrator never chose. Returns false when pools still reference it.
func (s *SQLiteStore) DeleteClusterConnectionIfUnreferenced(id string) (bool, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	var referencing int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM fleet_pools WHERE cluster_id = ?`, id).Scan(&referencing); err != nil {
		return false, err
	}
	if referencing > 0 {
		return false, nil
	}
	// A domain's default cluster is a reference too. Without this check the
	// Postgres foreign key would surface as a raw 500 instead of the existing
	// 409, and on SQLite the delete would fail with an opaque constraint error.
	if err := tx.QueryRow(`SELECT COUNT(*) FROM domain_deployment_targets WHERE default_cluster_id = ?`, id).Scan(&referencing); err != nil {
		return false, err
	}
	if referencing > 0 {
		return false, nil
	}
	result, err := tx.Exec(`DELETE FROM cluster_connections WHERE id = ?`, id)
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

// ListFleetPoolsForCluster backs the "which pools would break" message on a
// refused delete.
func (s *SQLiteStore) ListFleetPoolsForCluster(clusterID string) ([]*FleetPool, error) {
	rows, err := s.db.Query(fleetPoolSelect+` WHERE cluster_id = ? ORDER BY name ASC`, clusterID)
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
